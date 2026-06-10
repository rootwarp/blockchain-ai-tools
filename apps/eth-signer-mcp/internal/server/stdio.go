package server

import (
	"context"
	"errors"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// RunStdio runs the MCP server on the SDK's stdio transport (mcp.StdioTransport),
// handling one session until the client closes the connection or ctx is cancelled.
//
// Transport symbol (v1.6.1): mcp.StdioTransport{} — a zero-value struct that
// connects os.Stdin (reader) to os.Stdout (writer) with newline-delimited JSON.
// It implements the mcp.Transport interface via (*StdioTransport).Connect.
// The server is run via (*mcp.Server).Run(ctx, transport), which:
//   - connects the transport
//   - waits for either a session-end event or ctx cancellation
//   - on clean EOF from the client: returns nil (session ended cleanly)
//   - on ctx cancellation: closes the session and returns ctx.Err()
//     (context.Canceled or context.DeadlineExceeded)
//
// RunStdio normalises ctx.Err() to nil so that a SIGINT/SIGTERM-initiated
// shutdown (which cancels ctx via signal.NotifyContext) causes RunStdio to
// return nil and the binary to exit 0 — the same clean-exit result as a
// natural stdin EOF.  This matches the acceptance criterion "exit 0 on stdin
// EOF" and "SIGINT during idle stdio session exits cleanly".
//
// STDOUT DISCIPLINE: The SDK's StdioTransport writes MCP JSON-RPC frames to
// os.Stdout.  Nothing else in this package may write to os.Stdout.  All logs
// go to os.Stderr via the injected obs logger.
func (s *Server) RunStdio(ctx context.Context) error {
	err := s.runWithTransport(ctx, &mcp.StdioTransport{})
	// Normalise context cancellation to nil: a SIGINT/SIGTERM shutdown is
	// clean (expected), not an error.  The SDK returns ctx.Err() on cancel.
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return nil
	}
	return err
}
