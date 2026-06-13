package signing

import (
	"context"
	"crypto/ecdsa"
	"errors"
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
//   - other DecryptKey errors (unknown cipher, corrupted ciphertext) → *ToolError{Code: CodeInternalError}
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
	passwordBytes, err := readPasswordFile(v.passwordPath, v.readFileFn)
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
	//
	// ADR-009 known residual: gokeystore.DecryptKey accepts only a string password.
	// The string(passwordBytes) conversion allocates an immutable Go string that is
	// NOT controlled by the deferred ZeroBytes call above; it persists in the heap
	// until GC reclaims it. This is an explicit best-effort limitation: Go runtime
	// retains this copy beyond our control, analogous to (but distinct from) the
	// GC-move and stack-copy cases documented in ZeroBytes/ZeroBigInt. go-ethereum
	// v1.17.3 does not expose a []byte-password API. The observable security
	// requirement ("no secrets in logs or outputs") is still met; memory-level
	// erasure of this copy requires a custom scrypt+AES shim.
	key, err := gokeystore.DecryptKey(v.keystoreJSON, string(passwordBytes))
	if err != nil {
		if errors.Is(err, gokeystore.ErrDecrypt) {
			// ErrDecrypt means the MAC check failed — wrong password. This is a
			// caller-visible, caller-correctable condition: password_error.
			return &ToolError{
				Code:    CodePasswordError,
				Message: "keystore decryption failed; check the password",
				Cause:   err,
			}
		}
		// Any other error from DecryptKey (e.g. unknown cipher algorithm, corrupted
		// ciphertext, unsupported KDF) is NOT a wrong-password issue — it is an
		// internal configuration or file-integrity problem. Map to internal_error so
		// operators understand this requires attention beyond a simple password change.
		return &ToolError{
			Code:    CodeInternalError,
			Message: "keystore decryption failed due to an internal error",
			Cause:   err,
		}
	}

	// Always cache the address from the *decrypted* key (the source of truth).
	// This self-heals any present-but-wrong top-level "address" parsed at boot
	// via permissive HexToAddress (Finding 1) and also covers the optional/absent case.
	// The write is under sem; see file_vault.go for visibility notes.
	v.address = key.Address

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

// readPasswordFile reads the password file at path using readFn, strips any trailing
// CR/LF characters (\r and \n), and returns the result as a []byte. The returned
// slice is the one that WithSigningKey registers for deferred zeroing; it must not
// be resliced or replaced by callers.
//
// Stripping both CR and LF handles:
//   - Unix files ending in \n (the standard; exercised by the testdata/password.txt fixture)
//   - Windows files ending in \r\n (common when password files are created or
//     transferred on Windows; leaving \r causes DecryptKey to fail with a cryptic
//     "check the password" error with no line-ending diagnostic)
//
// The stripped CR/LF bytes are not secret material and are left un-zeroed in the
// backing array (they are at positions len(b)..cap(b)-1 after the reslice).
//
// readFn is normally os.ReadFile; the fileKeyVault stores it as readFileFn to
// allow internal tests to intercept the call via the captured-pointer technique
// (e.g. to verify zeroing on the wrong-password path, where fn is never called
// and signingKey.pwBytes is never created). Tests must not share a vault instance
// across goroutines when overriding readFileFn.
func readPasswordFile(path string, readFn func(string) ([]byte, error)) ([]byte, error) {
	b, err := readFn(path)
	if err != nil {
		return nil, err
	}
	// Strip trailing CR/LF in-place (reslice only; ZeroBytes later zeroes the
	// actual password bytes at indices 0..len(b)-1; the stripped bytes beyond
	// len(b) are non-secret and intentionally not zeroed).
	for len(b) > 0 && (b[len(b)-1] == '\n' || b[len(b)-1] == '\r') {
		b = b[:len(b)-1]
	}
	return b, nil
}
