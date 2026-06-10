package server

// server_test.go — MCP SDK v1.6.1 in-memory smoke tests for internal/server.
//
// This file supersedes spike_smoke_test.go (issue 1.7).  All spike test
// content has been merged here; spike_smoke_test.go is removed.
//
// Tests covered:
//   (a) initialize round-trip: server name/version in Options match what the
//       client sees via InitializeResult.ServerInfo (issue 1.7 smoke + 1.8 growth).
//   (b) tools/list returns exactly two tools (sign_transaction, get_address)
//       — both registered in Phase 2 (issue 2.7).
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
	"fmt"
	"io"
	"testing"
	"time"

	"github.com/ethereum/go-ethereum/common"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/rootwarp/blockchain-ai-tools/apps/eth-signer-mcp/internal/obs"
	"github.com/rootwarp/blockchain-ai-tools/apps/eth-signer-mcp/internal/signing"
)

// ── Stub signer ───────────────────────────────────────────────────────────────

// stubSigner is a minimal signerPort for smoke tests that only need the server
// to boot and respond to initialize/tools/list without actual signing.
type stubSigner struct {
	signFn  func(ctx context.Context, req signing.TxRequest) (*signing.SignResult, error)
	address common.Address
}

func (s *stubSigner) SignTransaction(ctx context.Context, req signing.TxRequest) (*signing.SignResult, error) {
	if s.signFn != nil {
		return s.signFn(ctx, req)
	}
	return nil, &signing.ToolError{Code: signing.CodeInternalError, Message: "stub: SignTransaction not set"}
}

func (s *stubSigner) Address() common.Address {
	return s.address
}

// noopStub returns a stubSigner whose SignTransaction panics if called.
// Suitable for initialize/tools-list tests that must NOT invoke the signer.
func noopStub() *stubSigner {
	return &stubSigner{
		address: common.Address{},
		signFn: func(_ context.Context, _ signing.TxRequest) (*signing.SignResult, error) {
			panic("stub: SignTransaction should not be called in this test")
		},
	}
}

// ── discardCloser ─────────────────────────────────────────────────────────────

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
	srv := newServer(noopStub(), Options{
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

// TestInMemorySmoke_ToolsListRegistered verifies that tools/list returns exactly
// two tools — sign_transaction and get_address — after Phase 2 tool registration
// (issue 2.7). Supersedes the Phase 1 "ToolsListEmpty" assertion.
func TestInMemorySmoke_ToolsListRegistered(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	logger := obs.NewLogger("error")
	srv := newServer(noopStub(), Options{
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
	// Phase 2: exactly 2 tools registered.
	if len(result.Tools) != 2 {
		names := make([]string, len(result.Tools))
		for i, tt := range result.Tools {
			names[i] = tt.Name
		}
		t.Fatalf("len(result.Tools) = %d; want 2 (sign_transaction, get_address). Got: %v",
			len(result.Tools), names)
	}

	// Build name set for assertion.
	toolMap := make(map[string]bool, 2)
	for _, tt := range result.Tools {
		toolMap[tt.Name] = true
	}
	for _, wantName := range []string{"sign_transaction", "get_address"} {
		if !toolMap[wantName] {
			t.Errorf("tool %q not in tools/list", wantName)
		}
	}
}

// TestRunWithTransport_CtxCancel verifies that cancelling the serve context
// causes runWithTransport to return within 1 second.  Uses an IOTransport
// backed by io.Pipe so we can control both ends without touching real
// os.Stdin/Stdout.
//
// Why io.Pipe: using an io.Pipe reader ensures that when the transport is
// closed (on ctx cancel), ioConn.Close() closes the read side of the pipe,
// which unblocks any pending json.Decoder.Decode() call and allows the SDK's
// internal decode goroutine to exit cleanly (no goroutine leak).
func TestRunWithTransport_CtxCancel(t *testing.T) {
	t.Parallel()

	ctx, serveCancel := context.WithCancel(context.Background())

	logger := obs.NewLogger("error")
	srv := newServer(noopStub(), Options{
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

// TestNormalizeShutdownErr verifies the context-cancellation normalisation that
// RunStdio applies: a SIGINT/SIGTERM graceful shutdown surfaces as ctx.Err()
// from the SDK, which must be mapped to nil so the binary exits 0; any other
// error passes through unchanged.
//
// This exercises the normalisation directly (rather than via RunStdio, which
// would close the process-wide os.Stdin through mcp.StdioTransport — a land-mine
// for any future test in this binary that reads os.Stdin).
func TestNormalizeShutdownErr(t *testing.T) {
	t.Parallel()

	otherErr := errors.New("real transport failure")
	tests := []struct {
		name string
		in   error
		want error
	}{
		{"nil", nil, nil},
		{"canceled", context.Canceled, nil},
		{"deadline", context.DeadlineExceeded, nil},
		{"wrapped canceled", fmt.Errorf("serve: %w", context.Canceled), nil},
		{"other error passes through", otherErr, otherErr},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := normalizeShutdownErr(tt.in); got != tt.want {
				t.Errorf("normalizeShutdownErr(%v) = %v; want %v", tt.in, got, tt.want)
			}
		})
	}
}
