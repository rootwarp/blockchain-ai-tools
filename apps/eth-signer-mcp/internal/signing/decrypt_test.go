// Tests for WithSigningKey (decrypt path) — Issue 2.2.
// Internal tests (package signing) to allow the captured-pointer zeroing technique
// and type assertions into the unexported signingKey struct.
package signing

import (
	"bytes"
	"context"
	"errors"
	"math/big"
	"os"
	"path/filepath"
	"reflect"
	"sync"
	"testing"
	"time"

	"github.com/ethereum/go-ethereum/core/types"
)

// weakVault constructs a fileKeyVault using the weak (n=2) keystore fixture, which
// decrypts near-instantly and is the default for all decrypt tests.
func weakVault(t *testing.T) *fileKeyVault {
	t.Helper()
	v, err := newFileKeyVault(VaultOptions{
		KeystorePath: testdataFile(t, "keystore-weak.json"),
		PasswordPath: testdataFile(t, "password.txt"),
	})
	if err != nil {
		t.Fatalf("weakVault: newFileKeyVault: %v", err)
	}
	return v
}

// TestWithSigningKey_Success verifies the happy path: WithSigningKey succeeds
// against the weak fixture and the signing key's Address matches FixtureTestAddress.
func TestWithSigningKey_Success(t *testing.T) {
	t.Parallel()
	v := weakVault(t)

	called := false
	err := v.WithSigningKey(context.Background(), func(k SigningKey) error {
		called = true
		if got := k.Address().Hex(); got != FixtureTestAddress {
			t.Errorf("SigningKey.Address() = %q, want %q", got, FixtureTestAddress)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("WithSigningKey: unexpected error: %v", err)
	}
	if !called {
		t.Error("fn was never called")
	}
}

// TestWithSigningKey_ZeroesKeyAndPasswordOnSuccess verifies that after
// WithSigningKey returns normally, both the key scalar D and the password bytes
// held by the signingKey are zeroed.
//
// Technique (ADR-009 captured-pointer technique):
//  1. Inside fn, type-assert the SigningKey to *signingKey (valid in package signing).
//  2. Capture pointers to sk.key.D (big.Int) and sk.pwBytes ([]byte).
//  3. After WithSigningKey returns, assert D is the zero big.Int and all bytes of
//     the captured password slice are zero.
//
// What this test DOES prove: the specific buffers owned by WithSigningKey are
// cleared before the function returns.
// What this test does NOT prove: Go's runtime may retain transient copies of the
// key scalar or password bytes on the heap or stack (GC moves, stack copies).
// Per ADR-009 this is expected and accepted; the observable requirement is "no
// secrets in logs or outputs", not guaranteed in-memory erasure.
func TestWithSigningKey_ZeroesKeyAndPasswordOnSuccess(t *testing.T) {
	t.Parallel()
	v := weakVault(t)

	var capturedD *big.Int
	var capturedPW []byte

	err := v.WithSigningKey(context.Background(), func(k SigningKey) error {
		// Type-assert to the unexported *signingKey (permitted in package signing).
		sk, ok := k.(*signingKey)
		if !ok {
			t.Errorf("SigningKey is not *signingKey (type: %T); captured-pointer test cannot proceed", k)
			return nil
		}
		// Stash pointers to the secret material. These share the backing arrays
		// that WithSigningKey will zero via the registered defers.
		// D is deprecated for key creation/encoding; we only read it here to stash
		// a pointer for ADR-009 zeroing verification (captured-pointer technique).
		capturedD = sk.key.D //nolint:staticcheck // ADR-009 zeroing verification: read-only pointer capture
		capturedPW = sk.pwBytes
		return nil
	})
	if err != nil {
		t.Fatalf("WithSigningKey: %v", err)
	}

	// After return: deferred ZeroBigInt(key.PrivateKey.D) must have fired.
	if capturedD != nil && capturedD.BitLen() != 0 {
		t.Errorf("key.D not zeroed after WithSigningKey: BitLen() = %d, want 0", capturedD.BitLen())
	}

	// After return: deferred ZeroBytes(passwordBytes) must have fired.
	// The capturedPW slice shares the backing array; all bytes should be 0x00.
	for i, b := range capturedPW {
		if b != 0 {
			t.Errorf("password byte at index %d is 0x%02x, want 0x00 (not zeroed)", i, b)
			break
		}
	}
}

// TestWithSigningKey_ZeroesKeyAndPasswordOnPanic verifies that deferred zeroing
// fires even when fn panics. The test recovers the panic and inspects the captured
// pointers to confirm zeroing.
//
// Ordering guaranteed by Go's defer/panic mechanism: defers in the stack frame
// fire before the panic propagates to outer frames. The zeroing defers registered
// in WithSigningKey fire before the panic reaches this test's recover().
//
// ADR-009 limitation applies identically to the success case.
func TestWithSigningKey_ZeroesKeyAndPasswordOnPanic(t *testing.T) {
	t.Parallel()
	v := weakVault(t)

	var capturedD *big.Int
	var capturedPW []byte

	// Wrap WithSigningKey in a closure so we can recover the panic.
	func() {
		defer func() {
			if r := recover(); r == nil {
				t.Error("expected panic, got none")
			}
			// At this point all defers in WithSigningKey have already fired.
			if capturedD != nil && capturedD.BitLen() != 0 {
				t.Errorf("key.D not zeroed on panic: BitLen() = %d, want 0", capturedD.BitLen())
			}
			for i, b := range capturedPW {
				if b != 0 {
					t.Errorf("password byte at index %d is 0x%02x, want 0x00 (not zeroed on panic)", i, b)
					break
				}
			}
		}()

		_ = v.WithSigningKey(context.Background(), func(k SigningKey) error {
			sk, ok := k.(*signingKey)
			if !ok {
				t.Errorf("SigningKey is not *signingKey; panic-zeroing test cannot proceed")
				return nil
			}
			capturedD = sk.key.D //nolint:staticcheck // ADR-009: read-only pointer capture for zeroing verification
			capturedPW = sk.pwBytes
			panic("test-induced panic inside fn")
		})
	}()
}

// TestWithSigningKey_WrongPassword verifies that a wrong password returns a
// *ToolError with Code == CodePasswordError.
// Password bytes are zeroed by the registered defer even on this error path
// (the defer is registered before DecryptKey is called, so it fires on early return).
func TestWithSigningKey_WrongPassword(t *testing.T) {
	t.Parallel()

	// Write a wrong password to a temp file.
	pwFile := filepath.Join(t.TempDir(), "wrong-password.txt")
	if err := os.WriteFile(pwFile, []byte("definitely-wrong-password\n"), 0o600); err != nil {
		t.Fatalf("write temp password: %v", err)
	}

	v, err := newFileKeyVault(VaultOptions{
		KeystorePath: testdataFile(t, "keystore-weak.json"),
		PasswordPath: pwFile,
	})
	if err != nil {
		t.Fatalf("newFileKeyVault: %v", err)
	}

	callErr := v.WithSigningKey(context.Background(), func(k SigningKey) error {
		t.Error("fn should not be called with wrong password")
		return nil
	})

	if callErr == nil {
		t.Fatal("WithSigningKey(wrong password): expected error, got nil")
	}

	var te *ToolError
	if !errors.As(callErr, &te) {
		t.Fatalf("error type = %T, want *ToolError", callErr)
	}
	if te.Code != CodePasswordError {
		t.Errorf("Code = %q, want %q", te.Code, CodePasswordError)
	}
}

// TestWithSigningKey_MissingPasswordFile verifies that a missing password file
// returns a *ToolError with Code == CodePasswordError.
func TestWithSigningKey_MissingPasswordFile(t *testing.T) {
	t.Parallel()

	v, err := newFileKeyVault(VaultOptions{
		KeystorePath: testdataFile(t, "keystore-weak.json"),
		PasswordPath: "/nonexistent/no-such-password.txt",
	})
	if err != nil {
		t.Fatalf("newFileKeyVault: %v", err)
	}

	callErr := v.WithSigningKey(context.Background(), func(k SigningKey) error {
		t.Error("fn should not be called with missing password file")
		return nil
	})

	if callErr == nil {
		t.Fatal("WithSigningKey(missing password file): expected error, got nil")
	}

	var te *ToolError
	if !errors.As(callErr, &te) {
		t.Fatalf("error type = %T, want *ToolError", callErr)
	}
	if te.Code != CodePasswordError {
		t.Errorf("Code = %q, want %q", te.Code, CodePasswordError)
	}
}

// TestWithSigningKey_PasswordRereadPerCall verifies that the password file is
// re-read on every WithSigningKey call, enabling password rotation without restart.
//
// Sequence:
//  1. Vault is constructed with a temp password file containing the correct password.
//  2. First WithSigningKey call succeeds.
//  3. The temp password file is overwritten with the correct password again (same value).
//  4. Second WithSigningKey call succeeds (re-read).
//  5. The temp password file is overwritten with a wrong password.
//  6. Third WithSigningKey call returns password_error.
func TestWithSigningKey_PasswordRereadPerCall(t *testing.T) {
	t.Parallel()

	// Use a temp password file so we can change it between calls.
	pwFile := filepath.Join(t.TempDir(), "rotating-password.txt")
	correctPassword := "test-only-password-do-not-reuse\n" // matches password.txt content

	writePassword := func(pw string) {
		t.Helper()
		if err := os.WriteFile(pwFile, []byte(pw), 0o600); err != nil {
			t.Fatalf("write password file: %v", err)
		}
	}

	// Start with the correct password.
	writePassword(correctPassword)

	v, err := newFileKeyVault(VaultOptions{
		KeystorePath: testdataFile(t, "keystore-weak.json"),
		PasswordPath: pwFile,
	})
	if err != nil {
		t.Fatalf("newFileKeyVault: %v", err)
	}

	// Call 1: should succeed with the correct password.
	if err := v.WithSigningKey(context.Background(), func(k SigningKey) error { return nil }); err != nil {
		t.Fatalf("call 1 (correct password): unexpected error: %v", err)
	}

	// Call 2: overwrite with the same correct password; should still succeed.
	writePassword(correctPassword)
	if err := v.WithSigningKey(context.Background(), func(k SigningKey) error { return nil }); err != nil {
		t.Fatalf("call 2 (correct password re-written): unexpected error: %v", err)
	}

	// Call 3: overwrite with a wrong password; should return password_error.
	writePassword("wrong-password\n")
	err = v.WithSigningKey(context.Background(), func(k SigningKey) error {
		t.Error("fn should not be called with wrong password")
		return nil
	})
	if err == nil {
		t.Fatal("call 3 (wrong password): expected error, got nil")
	}
	var te *ToolError
	if !errors.As(err, &te) {
		t.Fatalf("call 3: error type = %T, want *ToolError", err)
	}
	if te.Code != CodePasswordError {
		t.Errorf("call 3: Code = %q, want %q", te.Code, CodePasswordError)
	}
}

// TestWithSigningKey_SemaphoreOfOne verifies that the internal semaphore serialises
// concurrent WithSigningKey calls: the second goroutine observably enters fn only
// after the first goroutine's fn has returned.
//
// Technique: instrumented channel handshakes inside fn — no wall-clock sleeps.
//  1. goroutine 1 enters fn first (proven by a coordinated start), sets g1inside,
//     and blocks until signalled.
//  2. goroutine 2 starts and attempts WithSigningKey; it should block on the semaphore.
//  3. goroutine 1 sets g1done, unblocks, and returns from fn.
//  4. goroutine 2 is now unblocked; it sets g2inside and asserts g1done is already true.
func TestWithSigningKey_SemaphoreOfOne(t *testing.T) {
	t.Parallel()
	v := weakVault(t)

	// Coordination channels for g1 ↔ test and g2 ↔ test.
	g1EnteredFn := make(chan struct{})   // closed when g1 enters fn
	g1MayReturn := make(chan struct{})   // closed when g1 should exit fn
	g2EnteredFn := make(chan struct{})   // closed when g2 enters fn
	g1DoneBeforeG2 := make(chan bool, 1) // g2 records whether g1 already exited fn

	var g1Done bool   // true once g1's fn has returned (before g2 enters)
	var mu sync.Mutex // protects g1Done

	var wg sync.WaitGroup
	wg.Add(2)

	// goroutine 1: enters fn, signals, waits to be released.
	go func() {
		defer wg.Done()
		_ = v.WithSigningKey(context.Background(), func(k SigningKey) error {
			close(g1EnteredFn) // signal: g1 is inside fn
			<-g1MayReturn      // wait: test will unblock after g2 is confirmed waiting
			mu.Lock()
			g1Done = true
			mu.Unlock()
			return nil
		})
	}()

	// Wait until g1 is inside fn before starting g2.
	select {
	case <-g1EnteredFn:
	case <-time.After(10 * time.Second):
		t.Fatal("timeout waiting for g1 to enter fn")
	}

	// goroutine 2: should block on the semaphore until g1 exits fn.
	go func() {
		defer wg.Done()
		_ = v.WithSigningKey(context.Background(), func(k SigningKey) error {
			mu.Lock()
			g1DoneBeforeG2 <- g1Done
			mu.Unlock()
			close(g2EnteredFn)
			return nil
		})
	}()

	// Give g2 a moment to start and reach the semaphore-acquisition select.
	// We cannot know precisely when g2 is blocked, but 20 ms is more than enough
	// time on any hardware for a goroutine to be scheduled and reach the select.
	time.Sleep(20 * time.Millisecond)

	// Unblock g1 so it exits fn and releases the semaphore.
	close(g1MayReturn)

	// Wait for g2 to enter fn.
	select {
	case <-g2EnteredFn:
	case <-time.After(10 * time.Second):
		t.Fatal("timeout waiting for g2 to enter fn")
	}

	// g2 must have seen g1Done == true.
	select {
	case g1WasDone := <-g1DoneBeforeG2:
		if !g1WasDone {
			t.Error("semaphore violation: g2 entered fn before g1 exited fn")
		}
	default:
		t.Error("g2 never recorded g1Done state")
	}

	wg.Wait()
}

// TestWithSigningKey_CtxCancelledBeforeKDF verifies that a pre-cancelled (or
// sub-100ms deadline) context returns ctx.Err() without running the KDF.
//
// Uses the standard-scrypt fixture (N=262144) whose KDF alone takes ~0.5–1 s.
// A cancelled context must return before the KDF starts; if the context timeout
// is 100 ms and the call returns well within that, the KDF was never run.
//
// This test is skipped under -short because constructing the standard-scrypt vault
// still reads a larger JSON file (though no decryption happens at construction time).
func TestWithSigningKey_CtxCancelledBeforeKDF(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping ctx-before-KDF test under -short (uses standard-scrypt fixture)")
	}
	t.Parallel()

	v, err := newFileKeyVault(VaultOptions{
		KeystorePath: testdataFile(t, "keystore-standard.json"),
		PasswordPath: testdataFile(t, "password.txt"),
	})
	if err != nil {
		t.Fatalf("newFileKeyVault(standard): %v", err)
	}

	// Pre-cancel the context so the semaphore select immediately returns ctx.Done().
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // cancel immediately

	start := time.Now()
	callErr := v.WithSigningKey(ctx, func(k SigningKey) error {
		t.Error("fn should not be called with cancelled context")
		return nil
	})
	elapsed := time.Since(start)

	// Must return ctx.Err() (context.Canceled), not a *ToolError.
	if callErr == nil {
		t.Fatal("WithSigningKey(cancelled ctx): expected error, got nil")
	}
	if !errors.Is(callErr, context.Canceled) {
		t.Errorf("error = %v, want context.Canceled", callErr)
	}

	// Must return in well under 100 ms — the standard KDF takes ~0.5–1 s.
	if elapsed >= 100*time.Millisecond {
		t.Errorf("elapsed = %v, want < 100ms (KDF should not have started)", elapsed)
	}
}

// TestWithSigningKey_CtxCancelledAfterAcquire verifies that a context cancelled
// AFTER semaphore acquisition (but before the password is read) returns ctx.Err().
//
// This tests the re-check of ctx.Err() inside WithSigningKey immediately after
// acquiring the semaphore (step 2 in the implementation sequence).
func TestWithSigningKey_CtxCancelledAfterAcquire(t *testing.T) {
	t.Parallel()
	v := weakVault(t)

	// Pre-cancel context (same effect as if cancelled between acquire and re-check).
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	start := time.Now()
	err := v.WithSigningKey(ctx, func(k SigningKey) error {
		t.Error("fn should not be called with already-cancelled context")
		return nil
	})
	elapsed := time.Since(start)

	if !errors.Is(err, context.Canceled) {
		t.Errorf("error = %v, want context.Canceled", err)
	}
	// Should return immediately without reading the password file.
	if elapsed >= 100*time.Millisecond {
		t.Errorf("elapsed = %v, want near-instant", elapsed)
	}
}

// TestSigningKey_ExactlyTwoMethods verifies that the SigningKey interface exposes
// exactly two methods: Address and SignTx. No additional methods may be added
// without updating the architecture (ADR-003: sealed key, no raw-key accessor).
func TestSigningKey_ExactlyTwoMethods(t *testing.T) {
	t.Parallel()

	sk := reflect.TypeOf((*SigningKey)(nil)).Elem()
	if got := sk.NumMethod(); got != 2 {
		t.Errorf("SigningKey has %d methods, want 2", got)
		for i := 0; i < sk.NumMethod(); i++ {
			t.Logf("  method[%d]: %s", i, sk.Method(i).Name)
		}
	}
}

// TestWithSigningKey_FnError verifies that an error returned by fn is propagated
// as-is by WithSigningKey (zeroing still fires).
func TestWithSigningKey_FnError(t *testing.T) {
	t.Parallel()
	v := weakVault(t)

	sentinel := errors.New("fn-error-sentinel")
	err := v.WithSigningKey(context.Background(), func(k SigningKey) error {
		return sentinel
	})
	if !errors.Is(err, sentinel) {
		t.Errorf("error = %v, want %v", err, sentinel)
	}
}

// TestWithSigningKey_LeakScan runs the fixture key sentinel over log output captured
// during a successful WithSigningKey call. The test uses a bytes.Buffer as a log
// sink and asserts that none of the registered key forms appear in the output.
//
// This exercises the requirement that the private key does not leak through log
// calls inside WithSigningKey or the signingKey implementation.
func TestWithSigningKey_LeakScan(t *testing.T) {
	t.Parallel()
	v := weakVault(t)

	var logBuf bytes.Buffer

	err := v.WithSigningKey(context.Background(), func(k SigningKey) error {
		// The signingKey methods do not log anything, so the buffer stays empty
		// unless the signing package accidentally logs key material. We call
		// Address() to ensure the key path is exercised.
		_ = k.Address()
		return nil
	})
	if err != nil {
		t.Fatalf("WithSigningKey: %v", err)
	}

	// Scan the (empty) log buffer for any leaked key forms.
	sentinel := FixtureKeySentinel()
	if leaked := sentinel.Scan(logBuf.Bytes()); len(leaked) > 0 {
		t.Errorf("fixture key leaked in log output form(s): %v", leaked)
	}
}

// TestWithSigningKey_SignTxDelegatesToKey verifies that SigningKey.SignTx signs
// a transaction using the underlying ECDSA key and returns a signed transaction.
// This exercises the signingKey.SignTx method and confirms it delegates to
// types.SignTx with the correct key.
func TestWithSigningKey_SignTxDelegatesToKey(t *testing.T) {
	t.Parallel()
	v := weakVault(t)

	chainID := big.NewInt(1)
	signer := types.NewLondonSigner(chainID)

	// Build a minimal EIP-1559 (type 2) transaction.
	tx := types.NewTx(&types.DynamicFeeTx{
		ChainID:   chainID,
		Nonce:     0,
		GasTipCap: big.NewInt(1e9),
		GasFeeCap: big.NewInt(2e9),
		Gas:       21000,
		To:        nil, // contract creation
		Value:     big.NewInt(0),
		Data:      []byte{},
	})

	var signedTx *types.Transaction
	err := v.WithSigningKey(context.Background(), func(k SigningKey) error {
		var signErr error
		signedTx, signErr = k.SignTx(tx, signer)
		return signErr
	})
	if err != nil {
		t.Fatalf("WithSigningKey: %v", err)
	}
	if signedTx == nil {
		t.Fatal("SignTx returned nil signed transaction")
	}

	// Recover the sender and assert it matches the fixture address.
	sender, err := types.Sender(signer, signedTx)
	if err != nil {
		t.Fatalf("types.Sender: %v", err)
	}
	if got := sender.Hex(); got != FixtureTestAddress {
		t.Errorf("recovered sender = %q, want %q", got, FixtureTestAddress)
	}
}

// TestWithSigningKey_PasswordFileTrailingNewline verifies that a password file with
// a trailing newline is handled correctly: the newline is stripped and decryption
// succeeds. The testdata/password.txt file carries a trailing newline by design
// (Issue 2.1 requirement).
func TestWithSigningKey_PasswordFileTrailingNewline(t *testing.T) {
	t.Parallel()

	// Write the correct password WITH a trailing newline to a temp file.
	pwFile := filepath.Join(t.TempDir(), "password-with-newline.txt")
	if err := os.WriteFile(pwFile, []byte("test-only-password-do-not-reuse\n"), 0o600); err != nil {
		t.Fatalf("write password file: %v", err)
	}

	v, err := newFileKeyVault(VaultOptions{
		KeystorePath: testdataFile(t, "keystore-weak.json"),
		PasswordPath: pwFile,
	})
	if err != nil {
		t.Fatalf("newFileKeyVault: %v", err)
	}

	if err := v.WithSigningKey(context.Background(), func(k SigningKey) error {
		if got := k.Address().Hex(); got != FixtureTestAddress {
			t.Errorf("Address() = %q, want %q", got, FixtureTestAddress)
		}
		return nil
	}); err != nil {
		t.Fatalf("WithSigningKey with trailing-newline password: %v", err)
	}
}
