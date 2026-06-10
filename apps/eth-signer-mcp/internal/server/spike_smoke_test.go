package server

// spike_smoke_test.go — MCP SDK v1.6.1 in-memory initialize smoke test.
//
// Deliverable for issue 1.7 (MCP SDK spike). Verifies:
//   - mcp.NewInMemoryTransports() pattern (server connects first, then client).
//   - initialize round-trip: server name and version advertised in
//     InitializeResult.ServerInfo match what was passed to mcp.NewServer.
//
// Bound with a timeout context so a hung handshake cannot wedge CI.
//
// Issue 1.8 grows this test (alongside server.go and stdio.go) into the full
// boot test including tools/list emptiness and ctx-cancel assertions.

import (
	"context"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// TestInMemorySmoke_Initialize is the Phase 1 SDK spike smoke test.
// It constructs a bare *mcp.Server (no tools registered), connects a test
// client over the SDK's in-memory transport, and asserts that the MCP
// initialize handshake round-trips the server name and version correctly.
func TestInMemorySmoke_Initialize(t *testing.T) {
	const (
		wantName    = "eth-signer-mcp"
		wantVersion = "v0.0.0-test"
	)

	// Bound the entire handshake with a timeout so a hung initialize cannot
	// wedge CI.
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	// Construct a bare server — no tools, no logger noise in tests.
	srv := mcp.NewServer(
		&mcp.Implementation{
			Name:    wantName,
			Version: wantVersion,
		},
		nil, // ServerOptions: defaults are fine for the smoke test
	)

	// Connect server to t1 FIRST — the SDK server must be ready to handle
	// 'initialize' before the client sends it. If the order is reversed the
	// handshake stalls.
	t1, t2 := mcp.NewInMemoryTransports()
	serverSession, err := srv.Connect(ctx, t1, nil)
	if err != nil {
		t.Fatalf("srv.Connect: %v", err)
	}

	// client.Connect sends 'initialize' + 'notifications/initialized'
	// synchronously and returns a fully initialized *mcp.ClientSession.
	client := mcp.NewClient(
		&mcp.Implementation{Name: "test-client", Version: "v0.0.1"},
		nil,
	)
	clientSession, err := client.Connect(ctx, t2, nil)
	if err != nil {
		// Ensure the server-side goroutines are cleaned up on failure.
		_ = serverSession.Close()
		t.Fatalf("client.Connect: %v", err)
	}

	// Assert server name and version round-trip through InitializeResult.
	initResult := clientSession.InitializeResult()
	if initResult == nil {
		t.Fatal("clientSession.InitializeResult() is nil")
	}
	if initResult.ServerInfo == nil {
		t.Fatal("InitializeResult.ServerInfo is nil")
	}
	if got := initResult.ServerInfo.Name; got != wantName {
		t.Errorf("ServerInfo.Name = %q; want %q", got, wantName)
	}
	if got := initResult.ServerInfo.Version; got != wantVersion {
		t.Errorf("ServerInfo.Version = %q; want %q", got, wantVersion)
	}

	// Clean shutdown: close client first, then wait for server session to end.
	if err := clientSession.Close(); err != nil {
		t.Errorf("clientSession.Close: %v", err)
	}
	if err := serverSession.Wait(); err != nil {
		t.Errorf("serverSession.Wait: %v", err)
	}
}
