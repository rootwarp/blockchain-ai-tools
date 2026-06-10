package signing

import (
	"context"
	"crypto/ecdsa"
	"errors"
	"os"
	"runtime"

	gokeystore "github.com/ethereum/go-ethereum/accounts/keystore"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
)

// signingKey is the unexported, sealed implementation of the SigningKey interface.
//
// ADR-003: exactly two public methods (Address, SignTx) are exposed through the
// interface. There is no accessor for the raw *ecdsa.PrivateKey; the key cannot
// escape the WithSigningKey callback via a type assertion on the public interface.
//
// pwBytes holds the same backing array as the passwordBytes slice read inside
// WithSigningKey. This allows internal tests (package signing) to use the
// captured-pointer technique: stash a reference to sk.pwBytes inside fn via a
// closure, then verify after WithSigningKey returns that the deferred ZeroBytes
// call has zeroed the shared backing array.
//
// ADR-009 best-effort limitation: Go's runtime may retain transient copies of
// both pwBytes and key.D created by GC moves or stack copies. The deferred zeroing
// clears the buffers we own; the test proves those specific buffers are zeroed
// and documents that transient copies may remain.
type signingKey struct {
	addr    common.Address
	key     *ecdsa.PrivateKey
	pwBytes []byte // shared backing array with passwordBytes in WithSigningKey
}

// Address implements SigningKey.
func (k *signingKey) Address() common.Address {
	return k.addr
}

// SignTx implements SigningKey. It delegates to types.SignTx with the held
// private key; the key is never exposed directly.
func (k *signingKey) SignTx(tx *types.Transaction, signer types.Signer) (*types.Transaction, error) {
	return types.SignTx(tx, signer, k.key)
}

// WithSigningKey implements KeyVault.
//
// Sequence:
//  1. Acquire the semaphore (one decrypt at a time) — respects ctx cancellation
//     via select on ctx.Done() vs sem.
//  2. Re-check ctx.Err() immediately after acquiring and BEFORE any password I/O
//     or KDF work (guards the window between the select and the password read).
//  3. Read and strip the trailing "\n" from the password file.
//  4. Register deferred zeroing for passwordBytes BEFORE decrypting, so it fires
//     even if DecryptKey returns an error.
//  5. Call keystore.DecryptKey (the KDF-heavy step).
//  6. Register deferred zeroing for key.PrivateKey.D + KeepAlive BEFORE fn runs,
//     so both defers fire on panic inside fn.
//  7. Wrap the key in the sealed signingKey (sharing the passwordBytes backing
//     array so internal tests can verify zeroing via the captured-pointer technique).
//  8. Call fn; return its error.
//  9. On return (normal or panic), all registered defers fire:
//     first ZeroBytes(passwordBytes), then ZeroBigInt(key.D)+KeepAlive(key),
//     finally release the semaphore.
//
// Error mapping:
//   - ctx cancelled → ctx.Err() (not a *ToolError; system error)
//   - unreadable password file → *ToolError{Code: CodePasswordError}
//   - keystore.ErrDecrypt (wrong password / MAC fail) → *ToolError{Code: CodePasswordError}
//   - other DecryptKey errors → *ToolError{Code: CodePasswordError}
func (v *fileKeyVault) WithSigningKey(ctx context.Context, fn func(SigningKey) error) error {
	// 1. Acquire semaphore. The select respects context cancellation so callers
	//    never wait forever when the context is cancelled or timed out.
	select {
	case v.sem <- struct{}{}:
		// Semaphore acquired; proceed.
	case <-ctx.Done():
		// Context cancelled while waiting for the semaphore.
		return ctx.Err()
	}

	// 2. Re-check context AFTER acquiring and BEFORE any password I/O or KDF work.
	//    This covers the race window: the context may have been cancelled between the
	//    select above and this check. If so, release the semaphore and return early
	//    rather than loading and decrypting the password unnecessarily.
	if err := ctx.Err(); err != nil {
		<-v.sem // release
		return err
	}

	// Ensure the semaphore is always released when WithSigningKey returns.
	// This defer is registered first so it fires last (LIFO), after the zeroing
	// defers below have already cleared secret material.
	defer func() { <-v.sem }()

	// 3. Read password file (re-read on every call to support password rotation).
	passwordBytes, err := readPasswordFile(v.passwordPath)
	if err != nil {
		return &ToolError{
			Code:    CodePasswordError,
			Message: "password file could not be read",
			Cause:   err,
		}
	}

	// 4. Register password zeroing BEFORE calling DecryptKey so it fires even on
	//    a wrong-password error. The defer captures passwordBytes by reference; the
	//    slice header is fixed at this point, so ZeroBytes always clears the right
	//    backing array regardless of reslicing done by readPasswordFile.
	defer ZeroBytes(passwordBytes)

	// 5. Decrypt the keystore snapshot using the password.
	key, err := gokeystore.DecryptKey(v.keystoreJSON, string(passwordBytes))
	if err != nil {
		if errors.Is(err, gokeystore.ErrDecrypt) {
			return &ToolError{
				Code:    CodePasswordError,
				Message: "keystore decryption failed; check the password",
				Cause:   err,
			}
		}
		// Other DecryptKey errors (e.g. internal crypto failure) also map to
		// password_error: the ciphertext is validated at construction; failures
		// here are password or key material issues.
		return &ToolError{
			Code:    CodePasswordError,
			Message: "keystore decryption failed",
			Cause:   err,
		}
	}

	// 6. Register key-scalar zeroing BEFORE fn runs so it fires on panic inside fn.
	//    ZeroBigInt zeros the backing word-slice of key.PrivateKey.D and re-normalises
	//    via SetInt64(0). KeepAlive prevents the compiler from optimising away the clear.
	//    These two defers fire before the semaphore-release defer (LIFO order).
	//
	//    Note: ecdsa.PrivateKey.D is deprecated in Go 1.26 (discouraged for key creation
	//    and encoding). Zeroing D after use is the established go-ethereum key-zeroing
	//    pattern per ADR-009, and is explicitly endorsed by ZeroBigInt's own doc comment
	//    ("same approach used by go-ethereum's key-zeroing code"). We suppress the
	//    staticcheck SA1019 warning here because the deprecation targets key creation /
	//    modification paths, not zeroing-after-use.
	defer func() {
		ZeroBigInt(key.PrivateKey.D) //nolint:staticcheck // ADR-009: zeroing after use; see comment above
		runtime.KeepAlive(key)
	}()

	// 7. Wrap the decrypted key in the sealed signingKey.
	//    pwBytes shares the backing array with passwordBytes so internal tests can
	//    stash a reference to sk.pwBytes inside fn and verify zeroing after return
	//    (captured-pointer technique, ADR-009 best-effort: the test proves the owned
	//    buffer is cleared; transient Go-runtime copies are not verified).
	sk := &signingKey{
		addr:    key.Address,
		key:     key.PrivateKey,
		pwBytes: passwordBytes,
	}

	// 8. Invoke fn. Any panic propagates after all registered defers fire.
	return fn(sk)
}

// readPasswordFile reads the password file at path, strips a single trailing "\n"
// if present, and returns the result as a []byte. The returned slice is the one
// that WithSigningKey registers for deferred zeroing; it must not be resliced or
// replaced by callers.
func readPasswordFile(path string) ([]byte, error) {
	b, err := readFile(path)
	if err != nil {
		return nil, err
	}
	// Strip a single trailing newline. The fixture password.txt carries one by
	// design so the strip path is always exercised in tests (Issue 2.1 requirement).
	// We operate in-place (reslice) so ZeroBytes later zeroes the actual password
	// bytes, not the newline. The newline position in the backing array is not
	// sensitive and is left un-zeroed deliberately (it is not secret material).
	if len(b) > 0 && b[len(b)-1] == '\n' {
		b = b[:len(b)-1]
	}
	return b, nil
}

// readFile is the thin wrapper around os.ReadFile used by readPasswordFile.
func readFile(path string) ([]byte, error) {
	return os.ReadFile(path)
}
