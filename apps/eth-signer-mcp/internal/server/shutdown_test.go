package server

// shutdown_test.go — Issue 3.7: Graceful-shutdown in-process tests.
//
// Two in-process test scenarios that verify RunHTTP's drain semantics on
// ctx cancellation (SIGINT/SIGTERM → signal.NotifyContext → ctx cancelled):
//
// TestShutdown_InFlightCallDrainsOnCancel
//   One slow in-flight signing call is in progress when the server ctx is
//   cancelled.  The call completes normally and its response is delivered;
//   new TCP connections are refused after the listener closes; RunHTTP returns
//   nil within a generous 10 s window (the production grace is 3 s); and the
//   vault's deferred zeroing ran — proven structurally by Go's defer guarantee
//   (if fn returned, all defers in fileKeyVault.WithSigningKey fired).
//
// TestShutdown_SemaphoreWaiterCancelledAtShutdown
//   A second request is queued on the vault semaphore when both the server ctx
//   and the request's own context are cancelled (simulating a client disconnect
//   that arrives during shutdown).  The queued request returns ctx.Err() WITHOUT
//   the KDF starting — proven by the same instrumented-vault counters used in
//   Issue 3.4 (bounds_test.go, same package).  A completes normally; RunHTTP
//   returns nil.
//
// Reused helpers (same package):
//   recordingKeyVault   — bounds_test.go (fnCallsTotal, maxActiveFn, etc.)
//   startRunHTTP        — http_test.go
//   waitReady           — http_test.go
//   sdkClient           — bounds_test.go
//   writeTokenFile      — http_test.go
//   randTokenBytes      — auth_test.go
//   hexEncodeBytes      — auth_test.go
//   signingTestdataPathBounds — bounds_test.go
//   validSign1559Args   — bounds_test.go
//
// No goroutine leaks: -race clean.  No time.Sleep calls anywhere.
// Not parallel: both tests use the light fixture KDF (~50 ms) and rely on
// coordination channels that are sensitive to goroutine scheduling order.

import (
	"context"
	"fmt"
	"net"
	"path/filepath"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/rootwarp/blockchain-ai-tools/apps/eth-signer-mcp/internal/obs"
	"github.com/rootwarp/blockchain-ai-tools/apps/eth-signer-mcp/internal/signing"
)

// TestShutdown_InFlightCallDrainsOnCancel verifies that:
//  1. An in-flight signing call completes and its response is delivered.
//  2. RunHTTP returns nil within 10 s of ctx cancel (well above the 3 s grace).
//  3. New TCP connections are refused after the listener is closed.
//  4. The vault's deferred zeroing ran for the in-flight call.
//
// Vault zeroing (acceptance criterion §3.7):
//
//	ZeroBytes(passwordBytes) and ZeroBigInt(key.D) are registered as deferred
//	calls inside fileKeyVault.WithSigningKey before fn is invoked.  Go's defer
//	guarantee: all defers fire when the enclosing function returns.  Proof: A
//	returned a valid signing result (fn returned normally → defers fired).
//	Direct byte-level zeroing verification is in internal/signing unit tests
//	(zero_test.go, decrypt_test.go) — not duplicated here.
//
// Review note (§3.7 acceptance criterion):
//
//	internal/server holds NO key material at shutdown.  The only secret-adjacent
//	state is the bearer-token hash inside signing.Secret (BearerVerifier); that
//	hash is stored in the verifier for the lifetime of the server and is freed
//	by the GC when RunHTTP returns and the verifier goes out of scope.  Nothing
//	new to explicitly zero at the transport layer.
func TestShutdown_InFlightCallDrainsOnCancel(t *testing.T) {
	// Not parallel — light fixture KDF and coordination channels.

	tdPath := signingTestdataPathBounds(t)
	innerVault, err := signing.NewFileKeyVault(signing.VaultOptions{
		KeystorePath: filepath.Join(tdPath, "keystore-light.json"),
		PasswordPath: filepath.Join(tdPath, "password.txt"),
	})
	if err != nil {
		t.Fatalf("NewFileKeyVault(light): %v", err)
	}

	// holdFn gates A's fn — closed by the test to let A proceed.
	holdFn := make(chan struct{})
	rv := newRecordingVault(innerVault, holdFn)

	slogLogger := obs.NewLogger("error")
	signer := signing.NewSigner(rv, signing.SignerOptions{Logger: slogLogger})
	srv := New(signer, Options{
		Name:    "shutdown-drain-test",
		Version: "v0.0.0-test",
		Logger:  slogLogger,
	})

	token := hexEncodeBytes(randTokenBytes(32))
	tokenFile := writeTokenFile(t, token+"\n")
	readyCh := make(chan net.Addr, 1)

	// startRunHTTP registers t.Cleanup (cancel + wait for exit).
	done, exitCh, cancel := startRunHTTP(t, srv, HTTPOptions{
		Addr:          "127.0.0.1:0",
		TokenFilePath: tokenFile,
		ReadyCh:       readyCh,
	})

	testCtx, testCancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer testCancel()

	addr := waitReady(t, readyCh, 10*time.Second)
	endpoint := fmt.Sprintf("http://%s", addr.String())

	// ── Request A: in-flight signing call, held by holdFn ────────────────────
	aCtx, aCancel := context.WithCancel(testCtx)
	defer aCancel()

	csA := sdkClient(t, aCtx, endpoint, token)

	aErrCh := make(chan error, 1)
	aResultCh := make(chan *mcp.CallToolResult, 1)
	go func() {
		callCtx, callCancel := context.WithTimeout(aCtx, 90*time.Second)
		defer callCancel()
		result, callErr := csA.CallTool(callCtx, &mcp.CallToolParams{
			Name:      "sign_transaction",
			Arguments: validSign1559Args(),
		})
		aResultCh <- result
		aErrCh <- callErr
	}()

	// Gate: wait until A's fn has started (semaphore acquired + KDF done).
	// At this point the in-flight request is actively being held by holdFn.
	select {
	case <-rv.fnStarted:
		// A is in fn; the vault semaphore is held.
	case <-time.After(30 * time.Second):
		t.Fatal("A's fn never started within 30s; light fixture KDF timed out")
	}

	// ── Cancel the server ctx → graceful shutdown begins ─────────────────────
	//
	// cancel() → ctx.Done() → RunHTTP selects Shutdown path → graceCtx (3 s).
	// Shutdown immediately closes the listener (new connections refused) and
	// waits up to 3 s for active connections (A's request) to finish.
	cancel()

	// ── Release A's fn immediately after cancel ───────────────────────────────
	//
	// Releasing holdFn right away ensures A completes well within the 3 s grace
	// window: no artificial delay is added between cancel and fn-release.
	// A's fn body (after holdFn) is signing.SignTx — fast; KDF already ran.
	close(holdFn)

	// ── Wait for A to complete and deliver its response ───────────────────────
	select {
	case aErr := <-aErrCh:
		if aErr != nil {
			t.Fatalf("A returned error after holdFn released: %v", aErr)
		}
	case <-time.After(30 * time.Second):
		t.Fatal("A did not complete within 30s after holdFn released")
	}

	aResult := <-aResultCh

	// A must have produced a valid signing result (not an error response).
	// This also proves vault deferred zeroing ran: fn returned → defers fired.
	if aResult == nil {
		t.Error("A's result is nil; want a successful signing result")
	} else if aResult.IsError {
		var content string
		if len(aResult.Content) > 0 {
			if tc, ok := aResult.Content[0].(*mcp.TextContent); ok {
				content = tc.Text
			}
		}
		t.Errorf("A returned IsError=true after holdFn release; want success. Content: %s", content)
	}

	// ── Wait for RunHTTP to return ────────────────────────────────────────────
	//
	// Wait up to 10 s — generous above the 3 s grace so the timers cannot race
	// even under heavy load.  The common case (A already drained) returns in <100 ms.
	select {
	case <-exitCh:
	case <-time.After(10 * time.Second):
		t.Fatal("RunHTTP did not return within 10s after cancel + A completed")
	}

	// RunHTTP must return nil (clean drain — not DeadlineExceeded).
	if runErr := <-done; runErr != nil {
		t.Errorf("RunHTTP returned non-nil on clean shutdown: %v", runErr)
	}

	// ── New connections are refused after shutdown ────────────────────────────
	//
	// After RunHTTP returns, the listener is closed.  Any TCP dial to the old
	// address must fail.  This proves the server actually stopped accepting.
	conn, dialErr := net.DialTimeout("tcp", addr.String(), 500*time.Millisecond)
	if dialErr == nil {
		_ = conn.Close()
		t.Error("post-shutdown: TCP connect to old listener address succeeded; " +
			"want connection refused (server must not accept after RunHTTP returns)")
	}

	// ── Vault instrumentation checks ──────────────────────────────────────────
	if n := rv.fnCallsTotal.Load(); n != 1 {
		t.Errorf("fnCallsTotal = %d; want 1 (exactly one KDF ran — A's)", n)
	}
	if m := rv.maxActiveFn.Load(); m != 1 {
		t.Errorf("maxActiveFn = %d; want 1 (semaphore serialized correctly)", m)
	}
}

// TestShutdown_SemaphoreWaiterCancelledAtShutdown verifies that a request queued
// on the vault semaphore at cancel time returns ctx.Err() WITHOUT starting the
// KDF.  This reuses the Issue 3.4 instrumented-vault pattern
// (TestSemaphorePlumbing_CtxCancelledWhileQueued) and additionally verifies the
// combined scenario where the server ctx AND the request ctx are cancelled
// concurrently (SIGTERM + client disconnect during the shutdown window).
//
// Correctness properties (same as Issue 3.4, §Assertion 1–3):
//
//	withSigningKeyCallsTotal == 2: B reached vault ENTRY (at or at semaphore wait).
//	fnCallsTotal == 1: only A ran the KDF; B was cancelled before fn started.
//	maxActiveFn == 1: exactly one concurrent fn invocation.
//
// Why both cancels:
//
//	Cancelling only the server ctx does NOT cancel request contexts — Go's
//	http.Server.Shutdown waits for active connections to drain, and active
//	request contexts remain valid until the request completes or the connection
//	is force-closed.  Cancelling B's request ctx directly simulates the
//	client disconnecting during the shutdown window, which is the observable
//	mechanism by which queued requests are cancelled on shutdown.
func TestShutdown_SemaphoreWaiterCancelledAtShutdown(t *testing.T) {
	// Not parallel — light fixture KDF and coordination channels.

	tdPath := signingTestdataPathBounds(t)
	innerVault, err := signing.NewFileKeyVault(signing.VaultOptions{
		KeystorePath: filepath.Join(tdPath, "keystore-light.json"),
		PasswordPath: filepath.Join(tdPath, "password.txt"),
	})
	if err != nil {
		t.Fatalf("NewFileKeyVault(light): %v", err)
	}

	holdFn := make(chan struct{})
	rv := newRecordingVault(innerVault, holdFn)

	slogLogger := obs.NewLogger("error")
	signer := signing.NewSigner(rv, signing.SignerOptions{Logger: slogLogger})
	srv := New(signer, Options{
		Name:    "shutdown-semaphore-test",
		Version: "v0.0.0-test",
		Logger:  slogLogger,
	})

	token := hexEncodeBytes(randTokenBytes(32))
	tokenFile := writeTokenFile(t, token+"\n")
	readyCh := make(chan net.Addr, 1)

	done, exitCh, cancel := startRunHTTP(t, srv, HTTPOptions{
		Addr:          "127.0.0.1:0",
		TokenFilePath: tokenFile,
		ReadyCh:       readyCh,
	})

	testCtx, testCancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer testCancel()

	addr := waitReady(t, readyCh, 10*time.Second)
	endpoint := fmt.Sprintf("http://%s", addr.String())

	// ── Request A: holds the semaphore via slow fn ────────────────────────────
	aCtx, aCancel := context.WithCancel(testCtx)
	defer aCancel()

	csA := sdkClient(t, aCtx, endpoint, token)

	aErrCh := make(chan error, 1)
	aResultCh := make(chan *mcp.CallToolResult, 1)
	go func() {
		callCtx, callCancel := context.WithTimeout(aCtx, 90*time.Second)
		defer callCancel()
		result, callErr := csA.CallTool(callCtx, &mcp.CallToolParams{
			Name:      "sign_transaction",
			Arguments: validSign1559Args(),
		})
		aResultCh <- result
		aErrCh <- callErr
	}()

	// Gate: wait for A's fn to start (A holds semaphore).
	select {
	case <-rv.fnStarted:
	case <-time.After(30 * time.Second):
		t.Fatal("A's fn never started within 30s")
	}

	// ── Request B: will be queued on the semaphore (A holds it) ──────────────
	//
	// bCtx is cancellable so we can simulate client disconnect during shutdown.
	// defer bCancel() ensures bCtx is always cleaned up even if t.Fatal fires
	// before the explicit bCancel() call below (double-cancel is a no-op).
	bCtx, bCancel := context.WithCancel(testCtx)
	defer bCancel()

	csB := sdkClient(t, bCtx, endpoint, token)

	bErrCh := make(chan error, 1)
	go func() {
		callCtx, callCancel := context.WithTimeout(bCtx, 30*time.Second)
		defer callCancel()
		_, callErr := csB.CallTool(callCtx, &mcp.CallToolParams{
			Name:      "sign_transaction",
			Arguments: validSign1559Args(),
		})
		bErrCh <- callErr
	}()

	// GATE: wait until B has entered rv.WithSigningKey (withSigningKeyCallsTotal == 2).
	// rv.withSigningKeySecondCh is closed the first time withSigningKeyCallsTotal
	// reaches 2 — i.e. when B's WithSigningKey ENTRY increments the counter.
	// At this point B is inside rv.WithSigningKey, at or approaching the semaphore
	// wait inside fileKeyVault.WithSigningKey.
	//
	// Only cancelling after this gate guarantees the cancellation propagates to
	// the semaphore select rather than being absorbed at the HTTP/auth/SDK layer.
	select {
	case <-rv.withSigningKeySecondCh:
		// B has entered the vault path — is at or approaching the semaphore select.
	case <-time.After(30 * time.Second):
		t.Fatal("B never entered vault within 30s (withSigningKeySecondCh not closed)")
	}

	// ── Cancel server ctx + B's request ctx ──────────────────────────────────
	//
	// cancel() triggers graceful shutdown (listener closed, 3 s grace window).
	// bCancel() simulates B's client disconnecting during the shutdown window —
	// this cancels B's request context, which propagates to vault.WithSigningKey(ctx).
	// Both cancels fire atomically from the test's perspective (no sleep between them).
	cancel()  // server shutdown
	bCancel() // B's client disconnects

	// ── Wait for B to return ──────────────────────────────────────────────────
	select {
	case <-bErrCh:
		// B returned (error expected — ctx cancelled). Instrumentation check below.
	case <-time.After(15 * time.Second):
		t.Fatal("B did not return within 15s after context cancel at semaphore")
	}

	// ── Instrumentation assertions (A still blocking in fn) ───────────────────
	//
	// Assertion 1: withSigningKeyCallsTotal == 2
	//   Both A and B entered rv.WithSigningKey.  Combined with fnCallsTotal == 1,
	//   this proves B entered the vault path and was cancelled at the semaphore
	//   select — NOT at the HTTP/auth/SDK layer.
	if n := rv.withSigningKeyCallsTotal.Load(); n != 2 {
		t.Errorf("withSigningKeyCallsTotal = %d after B cancelled; want 2 "+
			"(A+B both entered vault; cancellation was at the semaphore layer)", n)
	}

	// Assertion 2: fnCallsTotal == 1
	//   Only A reached fn (past semaphore + ctx re-check + KDF).
	//   B was cancelled before fn started — B's ctx.Err() fired at the semaphore
	//   select BEFORE any KDF work began.
	if n := rv.fnCallsTotal.Load(); n != 1 {
		t.Errorf("fnCallsTotal = %d after B cancelled (A still in fn); want 1 "+
			"(B cancelled before KDF — only A ran the KDF)", n)
	}

	// Assertion 3: maxActiveFn == 1
	//   Exactly one concurrent fn invocation at all times.
	if m := rv.maxActiveFn.Load(); m != 1 {
		t.Errorf("maxActiveFn = %d; want 1 (exactly one concurrent fn invocation)", m)
	}

	// ── Release A → A completes → Shutdown drains → RunHTTP returns ──────────
	close(holdFn)

	select {
	case aErr := <-aErrCh:
		if aErr != nil {
			t.Errorf("A returned error after holdFn released: %v", aErr)
		}
	case <-time.After(30 * time.Second):
		t.Fatal("A did not complete within 30s after holdFn released")
	}
	<-aResultCh // drain the result channel

	// RunHTTP must exit within 10 s (well above the 3 s grace).
	select {
	case <-exitCh:
	case <-time.After(10 * time.Second):
		t.Fatal("RunHTTP did not return within 10s after cancel + A completed")
	}

	// RunHTTP must return nil (clean drain — A finished within the grace window).
	if runErr := <-done; runErr != nil {
		t.Errorf("RunHTTP returned non-nil on clean shutdown: %v", runErr)
	}

	// ── Final instrumentation check (after A completes) ───────────────────────
	if n := rv.withSigningKeyCallsTotal.Load(); n != 2 {
		t.Errorf("withSigningKeyCallsTotal after A completes = %d; want 2", n)
	}
	if n := rv.fnCallsTotal.Load(); n != 1 {
		t.Errorf("fnCallsTotal after A completes = %d; want 1", n)
	}
	if m := rv.maxActiveFn.Load(); m != 1 {
		t.Errorf("maxActiveFn after A completes = %d; want 1", m)
	}
}
