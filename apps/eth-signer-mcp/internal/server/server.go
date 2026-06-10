// Package server implements the MCP integration layer for eth-signer-mcp:
// *mcp.Server construction, tool registration and handlers, error wire-encoding,
// stdio and Streamable HTTP transports, bearer auth, and request logging.
//
// Issue 1.7 lands the SDK spike smoke test; Issue 1.8 adds server.go / stdio.go
// and the full boot test.
package server

import (
	"context"
	"log/slog"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// Options configures the MCP server instance.
type Options struct {
	Name, Version string // advertised on MCP initialize
	Logger        *slog.Logger
}

// Server wraps the *mcp.Server and its supporting state.
//
// Phase 1 signature: New takes only Options — there is nothing to sign yet.
//
// Phase 2 extends New to New(signer *signing.Signer, opts Options) when tools
// register (architecture's final signature from §internal/server Public API).
// This change is expected and intentional, not drift.
type Server struct {
	mcpServer *mcp.Server
	logger    *slog.Logger
}

// New constructs the *mcp.Server advertising opts.Name and opts.Version on the
// MCP initialize handshake, and returns a *Server ready to run a transport.
//
// No tools are registered in Phase 1; tools/list returns an empty list.
// Phase 2 registers sign_transaction and get_address against a Signer.
func New(opts Options) *Server {
	logger := opts.Logger
	if logger == nil {
		logger = slog.Default()
	}

	mcpSrv := mcp.NewServer(
		&mcp.Implementation{
			Name:    opts.Name,
			Version: opts.Version,
		},
		&mcp.ServerOptions{
			Logger: logger,
		},
	)

	return &Server{
		mcpServer: mcpSrv,
		logger:    logger,
	}
}

// runWithTransport runs the server on an arbitrary mcp.Transport.  It is the
// shared implementation behind RunStdio; it allows tests to inject a pipe-based
// transport without touching os.Stdin/Stdout.
func (s *Server) runWithTransport(ctx context.Context, t mcp.Transport) error {
	return s.mcpServer.Run(ctx, t)
}
