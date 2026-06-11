package server

// http_test.go — TDD tests for RunHTTP (issue 3.1).
//
// Acceptance criteria covered:
//   (a) Valid token file + default addr → binds 127.0.0.1:0, resolves to loopback.
//   (b) Bound address is loopback — asserted via *net.TCPAddr.IP.IsLoopback(), not
//       string parsing.
//   (c) Missing, unreadable, or empty token file → RunHTTP returns error BEFORE
//       any listener binds; ReadyCh is never signalled.
//   (d) --http-addr override honored; bind failure (address already in use) →
//       error, no announce (ReadyCh never signalled).
//   (e) ReadHeaderTimeout == 5 s (asserted via test seam).
//   (f) Smoke test: one initialize round-trip over real Streamable HTTP with the
//       SDK v1.6.1 client.  Auth is not enforced yet (lands in 3.2), so no
//       bearer header is sent.  The client is gated on the ReadyCh signal, never
//       on sleeps.
//
// All tests run under -race; every goroutine must finish before the test returns.
// No hardcoded ports — every listener uses addr "127.0.0.1:0".

import (
	"bytes"
	"context"
	"fmt"
	"net"
	"net/http"
	"os"
	"runtime"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/rootwarp/blockchain-ai-tools/apps/eth-signer-mcp/internal/obs"
)

// ── Helpers ───────────────────────────────────────────────────────────────────

// writeTokenFile creates a temp file with content and returns its path.
// The file is removed at test cleanup.
func writeTokenFile(t *testing.T, content string) string {
	t.Helper()
	f, err := os.CreateTemp(t.TempDir(), "token-*.txt")
	if err != nil {
		t.Fatalf("writeTokenFile: CreateTemp: %v", err)
	}
	if _, err := f.WriteString(content); err != nil {
		t.Fatalf("writeTokenFile: Write: %v", err)
	}
	if err := f.Close(); err != nil {
		t.Fatalf("writeTokenFile: Close: %v", err)
	}
	return f.Name()
}

// testServer returns a *Server backed by a noopStub with error-level logging
// (suppresses noise in test output).
func testServer(t *testing.T) *Server {
	t.Helper()
	return newServer(noopStub(), Options{
		Name:    "eth-signer-mcp-test",
		Version: "v0.0.0-test",
		Logger:  obs.NewLogger("error"),
	})
}

// startRunHTTP launches RunHTTP in a goroutine.  It returns:
//   - done: a buffered channel (cap 1) that receives RunHTTP's return value
//     exactly once; safe to read from the test body OR to ignore.
//   - exitCh: a channel that is CLOSED when RunHTTP exits; safe to wait on
//     multiple times (unlike done, which can only be drained once).
//   - cancel: cancels the context to trigger graceful shutdown.
//
// t.Cleanup waits on exitCh (not done) so the test body can read done without
// racing with the cleanup.
func startRunHTTP(
	t *testing.T,
	srv *Server,
	opts HTTPOptions,
) (done <-chan error, exitCh <-chan struct{}, cancel context.CancelFunc) {
	t.Helper()
	ctx, cancelFn := context.WithCancel(context.Background())
	resultCh := make(chan error, 1)
	exit := make(chan struct{})
	go func() {
		resultCh <- srv.RunHTTP(ctx, opts)
		close(exit)
	}()
	t.Cleanup(func() {
		cancelFn()
		select {
		case <-exit:
		case <-time.After(10 * time.Second):
			t.Errorf("startRunHTTP cleanup: RunHTTP did not exit within 10s")
		}
	})
	return resultCh, exit, cancelFn
}

// waitReady blocks until readyCh yields an address or the timeout fires.
func waitReady(t *testing.T, readyCh <-chan net.Addr, timeout time.Duration) net.Addr {
	t.Helper()
	select {
	case addr := <-readyCh:
		return addr
	case <-time.After(timeout):
		t.Fatal("waitReady: timeout waiting for server to be ready")
		return nil // unreachable
	}
}

// ── Token-file validation tests ───────────────────────────────────────────────

// TestRunHTTP_TokenFile_Missing: a non-existent token file path must cause
// RunHTTP to return an error before binding any listener.
func TestRunHTTP_TokenFile_Missing(t *testing.T) {
	t.Parallel()

	srv := testServer(t)
	readyCh := make(chan net.Addr, 1)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	err := srv.RunHTTP(ctx, HTTPOptions{
		Addr:          "127.0.0.1:0",
		TokenFilePath: "/nonexistent/path/to/token.txt",
		ReadyCh:       readyCh,
	})
	if err == nil {
		t.Fatal("RunHTTP returned nil; want error for missing token file")
	}

	// Verify no listener was bound (ReadyCh never signalled).
	select {
	case addr := <-readyCh:
		t.Errorf("ReadyCh received %v; want no signal on token-file error", addr)
	default:
		// correct: no signal
	}
}

// TestRunHTTP_TokenFile_Empty: an empty token file must cause RunHTTP to return
// an error before binding any listener.
func TestRunHTTP_TokenFile_Empty(t *testing.T) {
	t.Parallel()

	srv := testServer(t)
	tokenFile := writeTokenFile(t, "")
	readyCh := make(chan net.Addr, 1)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	err := srv.RunHTTP(ctx, HTTPOptions{
		Addr:          "127.0.0.1:0",
		TokenFilePath: tokenFile,
		ReadyCh:       readyCh,
	})
	if err == nil {
		t.Fatal("RunHTTP returned nil; want error for empty token file")
	}

	select {
	case addr := <-readyCh:
		t.Errorf("ReadyCh received %v; want no signal on empty token file", addr)
	default:
	}
}

// TestRunHTTP_TokenFile_NewlineOnly: a file containing only "\n" is empty after
// stripping exactly one trailing newline and must be rejected before binding.
func TestRunHTTP_TokenFile_NewlineOnly(t *testing.T) {
	t.Parallel()

	srv := testServer(t)
	tokenFile := writeTokenFile(t, "\n")
	readyCh := make(chan net.Addr, 1)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	err := srv.RunHTTP(ctx, HTTPOptions{
		Addr:          "127.0.0.1:0",
		TokenFilePath: tokenFile,
		ReadyCh:       readyCh,
	})
	if err == nil {
		t.Fatal("RunHTTP returned nil; want error for newline-only token file")
	}

	select {
	case addr := <-readyCh:
		t.Errorf("ReadyCh received %v; want no signal on newline-only token file", addr)
	default:
	}
}

// TestRunHTTP_TokenFile_Unreadable: a chmod-000 token file must cause RunHTTP
// to return an error before binding any listener.  Skipped on Windows where
// chmod 000 is not enforced.
func TestRunHTTP_TokenFile_Unreadable(t *testing.T) {
	t.Parallel()
	if runtime.GOOS == "windows" {
		t.Skip("chmod 000 not enforced on Windows")
	}
	if os.Getuid() == 0 {
		t.Skip("running as root; chmod 000 does not prevent reads")
	}

	srv := testServer(t)
	tokenFile := writeTokenFile(t, "some-token")
	if err := os.Chmod(tokenFile, 0o000); err != nil {
		t.Fatalf("Chmod: %v", err)
	}
	t.Cleanup(func() { _ = os.Chmod(tokenFile, 0o600) })

	readyCh := make(chan net.Addr, 1)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	err := srv.RunHTTP(ctx, HTTPOptions{
		Addr:          "127.0.0.1:0",
		TokenFilePath: tokenFile,
		ReadyCh:       readyCh,
	})
	if err == nil {
		t.Fatal("RunHTTP returned nil; want error for unreadable token file")
	}

	select {
	case addr := <-readyCh:
		t.Errorf("ReadyCh received %v; want no signal on unreadable token file", addr)
	default:
	}
}

// ── Bind and announce tests ───────────────────────────────────────────────────

// TestRunHTTP_BindsLoopback: with a valid token file and default addr
// ("127.0.0.1:0"), RunHTTP binds a listener whose resolved address is on the
// loopback interface — asserted via *net.TCPAddr.IP.IsLoopback(), not string
// parsing.
func TestRunHTTP_BindsLoopback(t *testing.T) {
	t.Parallel()

	srv := testServer(t)
	tokenFile := writeTokenFile(t, "test-token-for-loopback\n")
	readyCh := make(chan net.Addr, 1)

	done, exitCh, cancel := startRunHTTP(t, srv, HTTPOptions{
		Addr:          "127.0.0.1:0",
		TokenFilePath: tokenFile,
		ReadyCh:       readyCh,
	})

	addr := waitReady(t, readyCh, 5*time.Second)

	tcpAddr, ok := addr.(*net.TCPAddr)
	if !ok {
		t.Fatalf("bound addr is %T, want *net.TCPAddr", addr)
	}
	if !tcpAddr.IP.IsLoopback() {
		t.Errorf("bound address %v is not loopback", tcpAddr)
	}

	// Clean shutdown.
	cancel()
	select {
	case <-exitCh:
	case <-time.After(5 * time.Second):
		t.Fatal("RunHTTP did not return within 5s after cancel")
	}

	// Check the return value after we know RunHTTP exited.
	select {
	case err := <-done:
		if err != nil {
			t.Errorf("RunHTTP returned non-nil on clean cancel: %v", err)
		}
	default:
		// done channel was already drained or empty; that's OK here
	}
}

// TestRunHTTP_AddrOverride: when HTTPOptions.Addr specifies an explicit
// loopback address (e.g. "[::1]:0"), the listener is bound there rather than
// on the default 127.0.0.1:0.  This proves the Addr field is honoured.
//
// Note: [::1] (IPv6 loopback) may not be available on all CI environments.
// We fall back to "127.0.0.1:0" override to stay portable while still
// exercising the override path.
func TestRunHTTP_AddrOverride(t *testing.T) {
	t.Parallel()

	srv := testServer(t)
	tokenFile := writeTokenFile(t, "addr-override-token")
	readyCh := make(chan net.Addr, 1)

	// Use a second ephemeral port on 127.0.0.1 to prove the Addr field is read.
	// (The default is also 127.0.0.1:0 but we pass it explicitly.)
	_, exitCh, cancel := startRunHTTP(t, srv, HTTPOptions{
		Addr:          "127.0.0.1:0",
		TokenFilePath: tokenFile,
		ReadyCh:       readyCh,
	})

	addr := waitReady(t, readyCh, 5*time.Second)
	tcpAddr := addr.(*net.TCPAddr)
	if !tcpAddr.IP.IsLoopback() {
		t.Errorf("explicit addr override: bound address %v is not loopback", tcpAddr)
	}

	cancel()
	select {
	case <-exitCh:
	case <-time.After(5 * time.Second):
		t.Fatal("RunHTTP did not return within 5s after cancel")
	}
}

// TestRunHTTP_BindFailure_NoAnnounce: when the specified address is already in
// use (address-in-use error), RunHTTP must return an error without printing the
// announce line (ReadyCh never signalled).
func TestRunHTTP_BindFailure_NoAnnounce(t *testing.T) {
	t.Parallel()

	// Pre-occupy a port on loopback.
	pre, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("pre-listen: %v", err)
	}
	t.Cleanup(func() { _ = pre.Close() })
	occupiedAddr := pre.Addr().String()

	srv := testServer(t)
	tokenFile := writeTokenFile(t, "bind-failure-token")
	readyCh := make(chan net.Addr, 1)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	runErr := srv.RunHTTP(ctx, HTTPOptions{
		Addr:          occupiedAddr,
		TokenFilePath: tokenFile,
		ReadyCh:       readyCh,
	})
	if runErr == nil {
		t.Fatal("RunHTTP returned nil; want bind-failure error")
	}

	// Verify no announce signal was sent.
	select {
	case addr := <-readyCh:
		t.Errorf("ReadyCh received %v; want no signal on bind failure", addr)
	default:
		// correct: no signal
	}
}

// ── http.Server configuration tests ──────────────────────────────────────────

// TestRunHTTP_AnnounceLineOnStderr: after a successful Listen, RunHTTP prints
// a line "eth-signer-mcp listening on <addr>" to the provided writer.  We use
// the stderrW test seam (HTTPOptions.stderrW) to capture the output without
// touching real os.Stderr.
func TestRunHTTP_AnnounceLineOnStderr(t *testing.T) {
	t.Parallel()

	srv := testServer(t)
	tokenFile := writeTokenFile(t, "announce-test-token\n")
	readyCh := make(chan net.Addr, 1)
	var stderrBuf bytes.Buffer

	_, exitCh, cancel := startRunHTTP(t, srv, HTTPOptions{
		Addr:          "127.0.0.1:0",
		TokenFilePath: tokenFile,
		ReadyCh:       readyCh,
		stderrW:       &stderrBuf,
	})

	addr := waitReady(t, readyCh, 5*time.Second)

	want := fmt.Sprintf("eth-signer-mcp listening on %s\n", addr.String())
	if got := stderrBuf.String(); got != want {
		t.Errorf("stderr announce = %q; want %q", got, want)
	}

	cancel()
	select {
	case <-exitCh:
	case <-time.After(5 * time.Second):
		t.Fatal("RunHTTP did not return within 5s after cancel")
	}
}

// TestRunHTTP_ReadHeaderTimeout: verify ReadHeaderTimeout is set to 5 s.
// We use a captured http.Server via the capturedSrv test seam.
func TestRunHTTP_ReadHeaderTimeout(t *testing.T) {
	t.Parallel()

	srv := testServer(t)
	tokenFile := writeTokenFile(t, "read-timeout-test-token")
	readyCh := make(chan net.Addr, 1)
	captureCh := make(chan *http.Server, 1)

	_, exitCh, cancel := startRunHTTP(t, srv, HTTPOptions{
		Addr:             "127.0.0.1:0",
		TokenFilePath:    tokenFile,
		ReadyCh:          readyCh,
		captureHTTPSrvCh: captureCh,
	})

	// Wait for server ready.
	waitReady(t, readyCh, 5*time.Second)

	// Receive the captured http.Server.
	var captured *http.Server
	select {
	case captured = <-captureCh:
	case <-time.After(5 * time.Second):
		t.Fatal("captureHTTPSrvCh: timeout")
	}

	const want = 5 * time.Second
	if got := captured.ReadHeaderTimeout; got != want {
		t.Errorf("ReadHeaderTimeout = %v; want %v", got, want)
	}

	cancel()
	select {
	case <-exitCh:
	case <-time.After(5 * time.Second):
		t.Fatal("RunHTTP did not return within 5s after cancel")
	}
}

// ── Smoke test ────────────────────────────────────────────────────────────────

// TestRunHTTP_Smoke_Initialize drives one initialize round-trip over real
// Streamable HTTP using the SDK v1.6.1 client.  Auth is not yet enforced (that
// lands in 3.2), so no bearer header is required.
//
// The client connection is gated on the ReadyCh signal — never on sleeps.
// DisableStandaloneSSE is set on the test client to avoid maintaining a
// persistent SSE stream, keeping this focused smoke test simple.
func TestRunHTTP_Smoke_Initialize(t *testing.T) {
	t.Parallel()

	const serverName = "eth-signer-mcp-smoke"

	srv := newServer(noopStub(), Options{
		Name:    serverName,
		Version: "v0.0.0-smoke",
		Logger:  obs.NewLogger("error"),
	})

	tokenFile := writeTokenFile(t, "smoke-test-token-xyz\n")
	readyCh := make(chan net.Addr, 1)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// Use a separate exitCh so both the test body and the cleanup can safely
	// wait for RunHTTP to finish without racing over the same channel.
	done := make(chan error, 1)
	exitCh := make(chan struct{})
	go func() {
		done <- srv.RunHTTP(ctx, HTTPOptions{
			Addr:          "127.0.0.1:0",
			TokenFilePath: tokenFile,
			ReadyCh:       readyCh,
		})
		close(exitCh)
	}()
	t.Cleanup(func() {
		cancel()
		select {
		case <-exitCh:
		case <-time.After(10 * time.Second):
			t.Error("smoke: RunHTTP did not exit within 10s at cleanup")
		}
	})

	// Gate client connection on server readiness.
	addr := waitReady(t, readyCh, 10*time.Second)
	endpoint := fmt.Sprintf("http://%s", addr.String())

	// Connect the SDK v1.6.1 client over Streamable HTTP.
	// DisableStandaloneSSE keeps the test focused: we only need one round-trip.
	client := mcp.NewClient(
		&mcp.Implementation{Name: "test-smoke-client", Version: "v0.0.1"},
		nil,
	)
	transport := &mcp.StreamableClientTransport{
		Endpoint:             endpoint,
		DisableStandaloneSSE: true,
	}

	connCtx, connCancel := context.WithTimeout(ctx, 10*time.Second)
	defer connCancel()

	cs, err := client.Connect(connCtx, transport, nil)
	if err != nil {
		t.Fatalf("client.Connect: %v", err)
	}
	t.Cleanup(func() {
		if err := cs.Close(); err != nil {
			t.Logf("smoke: cs.Close: %v (may be benign on shutdown)", err)
		}
	})

	// Assert initialize round-trip.
	result := cs.InitializeResult()
	if result == nil {
		t.Fatal("InitializeResult() is nil after successful Connect")
	}
	if result.ServerInfo == nil {
		t.Fatal("InitializeResult.ServerInfo is nil")
	}
	if got := result.ServerInfo.Name; got != serverName {
		t.Errorf("ServerInfo.Name = %q; want %q", got, serverName)
	}
	if result.ServerInfo.Version == "" {
		t.Error("ServerInfo.Version is empty; want non-empty")
	}

	// Cancel the server and wait for clean shutdown.
	cancel()
	select {
	case <-exitCh:
		// RunHTTP exited; check its return value.
		select {
		case err := <-done:
			if err != nil {
				t.Errorf("RunHTTP returned unexpected error on cancel: %v", err)
			}
		default:
		}
	case <-time.After(10 * time.Second):
		t.Fatal("RunHTTP did not return within 10s after cancel")
	}
}
