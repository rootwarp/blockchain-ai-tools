// Package server implements the MCP integration layer for eth-signer-mcp:
// *mcp.Server construction, tool registration and handlers, error wire-encoding,
// stdio and Streamable HTTP transports, bearer auth, and request logging.
//
// Issue 1.7 delivered the SDK spike smoke test (spike_smoke_test.go).
// Issue 1.8 adds server.go (New, *Server), stdio.go (RunStdio), and the full
// boot test in server_test.go (spike_smoke_test.go merged here and removed).
package server
