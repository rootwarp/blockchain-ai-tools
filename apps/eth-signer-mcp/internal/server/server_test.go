package server

// server_test.go — MCP SDK v1.6.1 in-memory smoke tests for internal/server.
//
// This file supersedes spike_smoke_test.go (issue 1.7).  All spike test
// content has been merged here; spike_smoke_test.go is removed.
//
// Tests covered:
//   (a) initialize round-trip: server name/version in Options match what the
//       client sees via InitializeResult.ServerInfo (issue 1.7 smoke + 1.8 growth).
//   (b) tools/list returns an empty slice — no tools registered in Phase 1.
//   (c) ctx-cancel: cancelling the serve context causes RunStdio (backed by the
//       in-memory transport) to return within 1 s (catches hung-loop regressions).
//
// Shared helpers:
//   inMemorySession sets up a server.New(*Server) connected to a test *mcp.Client
//   over mcp.NewInMemoryTransports().
//
// All tests are bound with timeout contexts so a hung handshake cannot wedge CI.

import (
	"context"
	"errors"
	"io"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/rootwarp/blockchain-ai-tools/apps/eth-signer-mcp/internal/obs"
)

// discardCloser is an io.WriteCloser that discards all writes.  Used to supply
// a no-op stdout for the ctx-cancel test without touching real os.Stdout.
type discardCloser struct{}

func (discardCloser) Write(p []byte) (int, error) { return len(p), nil }
func (discardCloser) Close() error                { return nil }

// inMemorySession connects a *Server (obtained from server.New) to a test
// *mcp.Client over the SDK's in-memory transport.  It returns the client
// session and a cleanup function that closes the session pair gracefully.
//
// Critical ordering (per spike note, issue 1.7, and SDK docs):
//
//	server.Connect is called FIRST so the server is ready to receive
//	'initialize' before the client sends it.
func inMemorySession(
	t *testing.T,
	srv *Server,
	ctx context.Context,
) (clientSession *mcp.ClientSession, cleanup func()) {
	t.Helper()

	t1, t2 := mcp.NewInMemoryTransports()

	// Server must be connected before the client — the client sends 'initialize'
	// synchronously inside Connect, which stalls if the server is not ready.
	serverSession, err := srv.mcpServer.Connect(ctx, t1, nil)
	if err != nil {
		t.Fatalf("inMemorySession: srv.Connect: %v", err)
	}

	client := mcp.NewClient(
		&mcp.Implementation{Name: "test-client", Version: "v0.0.1"},
		nil,
	)
	cs, err := client.Connect(ctx, t2, nil)
	if err != nil {
		_ = serverSession.Close()
		t.Fatalf("inMemorySession: client.Connect: %v", err)
	}

	cleanup = func() {
		if err := cs.Close(); err != nil {
			t.Errorf("inMemorySession cleanup: cs.Close: %v", err)
		}
		if err := serverSession.Wait(); err != nil {
			t.Errorf("inMemorySession cleanup: serverSession.Wait: %v", err)
		}
	}
	return cs, cleanup
}

// TestInMemorySmoke_Initialize verifies the initialize round-trip:
//   - advertised server name equals Options.Name
//   - advertised server version equals Options.Version
//
// Version under go test is "<unknown>" per obs.Build() (issue 1.4); we assert
// presence (not a concrete value) to stay stable across build environments.
func TestInMemorySmoke_Initialize(t *testing.T) {
	t.Parallel()

	const wantName = "eth-signer-mcp-test"

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	logger := obs.NewLogger("error") // suppress noise in test output
	srv := New(Options{
		Name:    wantName,
		Version: obs.Build().Version, // "<unknown>" under go test
		Logger:  logger,
	})

	cs, cleanup := inMemorySession(t, srv, ctx)
	defer cleanup()

	result := cs.InitializeResult()
	if result == nil {
		t.Fatal("InitializeResult() is nil")
	}
	if result.ServerInfo == nil {
		t.Fatal("InitializeResult.ServerInfo is nil")
	}

	// Name must match exactly.
	if got := result.ServerInfo.Name; got != wantName {
		t.Errorf("ServerInfo.Name = %q; want %q", got, wantName)
	}

	// Version under go test is "<unknown>" per issue 1.4.
	// Assert non-empty rather than a concrete value.
	if got := result.ServerInfo.Version; got == "" {
		t.Errorf("ServerInfo.Version is empty; want non-empty (e.g. <unknown> under go test)")
	}
}

// TestInMemorySmoke_ToolsListEmpty verifies that tools/list returns an empty
// list when no tools are registered (Phase 1 — no tools yet).
func TestInMemorySmoke_ToolsListEmpty(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	logger := obs.NewLogger("error")
	srv := New(Options{
		Name:    "eth-signer-mcp-test",
		Version: "v0.0.0-test",
		Logger:  logger,
	})

	cs, cleanup := inMemorySession(t, srv, ctx)
	defer cleanup()

	result, err := cs.ListTools(ctx, nil)
	if err != nil {
		t.Fatalf("cs.ListTools: %v", err)
	}
	if result == nil {
		t.Fatal("ListTools result is nil")
	}
	if len(result.Tools) != 0 {
		t.Errorf("len(result.Tools) = %d; want 0", len(result.Tools))
	}
}

// TestRunStdio_CtxCancel verifies that cancelling the serve context causes
// RunStdio to return within 1 second.  Uses an IOTransport backed by
// io.Pipe so we can control both ends without touching real os.Stdin/Stdout.
//
// Why io.Pipe: using an io.Pipe reader ensures that when the transport is
// closed (on ctx cancel), ioConn.Close() closes the read side of the pipe,
// which unblocks any pending json.Decoder.Decode() call and allows the SDK's
// internal decode goroutine to exit cleanly (no goroutine leak).
func TestRunStdio_CtxCancel(t *testing.T) {
	t.Parallel()

	ctx, serveCancel := context.WithCancel(context.Background())

	logger := obs.NewLogger("error")
	srv := New(Options{
		Name:    "eth-signer-mcp-test",
		Version: "v0.0.0-test",
		Logger:  logger,
	})

	// io.Pipe: the server reads from pr; pw is held but never written to.
	// ioConn.Close() will close pr (via rwc.Close()), unblocking any Decode.
	pr, pw := io.Pipe()
	t.Cleanup(func() { _ = pw.Close() }) // belt-and-suspenders; ioConn.Close closes pr

	done := make(chan error, 1)
	go func() {
		done <- srv.runWithTransport(ctx, &mcp.IOTransport{
			Reader: pr,
			Writer: discardCloser{},
		})
	}()

	// Give the server a moment to start its read loop before cancelling.
	time.Sleep(10 * time.Millisecond)
	serveCancel()

	select {
	case <-time.After(1 * time.Second):
		t.Fatal("RunStdio did not return within 1s after ctx cancel")
	case err := <-done:
		// Accept nil or context.Canceled — both are valid graceful-shutdown signals.
		// The SDK's Server.Run returns ctx.Err() on cancel; our RunStdio normalises
		// that to nil.  runWithTransport passes the SDK error through as-is.
		if err != nil && !errors.Is(err, context.Canceled) {
			t.Errorf("RunStdio returned unexpected error on ctx cancel: %v", err)
		}
	}
}

// TestRunStdio_CtxCancelNormalisedToNil verifies that RunStdio itself returns
// nil when the context is cancelled (the SIGINT/SIGTERM graceful-shutdown path),
// not ctx.Err().
//
// The SDK's Server.Run returns ctx.Err() (context.Canceled) on cancellation;
// RunStdio normalises that to nil so the binary exits 0 on graceful shutdown
// (see stdio.go comment).  This test exercises RunStdio's normalisation branch
// for coverage.
//
// Stdin discipline: RunStdio uses mcp.StdioTransport which wraps os.Stdin.
// A pre-cancelled context causes Server.Run to detect ctx.Done() immediately
// and close the connection (which closes os.Stdin).  No other server test uses
// os.Stdin, so the close is safe within this test binary.
func TestRunStdio_CtxCancelNormalisedToNil(t *testing.T) {
	// Not parallel: RunStdio closes os.Stdin on exit, which is a one-time
	// operation within the test binary.  Other server tests use IOTransport
	// with pipes and are unaffected, but we avoid scheduling ambiguity.

	logger := obs.NewLogger("error")
	srv := New(Options{
		Name:    "eth-signer-mcp-test",
		Version: "v0.0.0-test",
		Logger:  logger,
	})

	// Pre-cancel the context so Server.Run exits immediately on <-ctx.Done().
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	done := make(chan error, 1)
	go func() {
		done <- srv.RunStdio(ctx)
	}()

	select {
	case <-time.After(2 * time.Second):
		t.Fatal("RunStdio did not return within 2s with pre-cancelled context")
	case err := <-done:
		if err != nil {
			t.Errorf("RunStdio(cancelled ctx) = %v; want nil (normalised from ctx.Canceled)", err)
		}
	}
}
