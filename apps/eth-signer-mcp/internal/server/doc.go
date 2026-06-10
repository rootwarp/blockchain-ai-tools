// Package server implements the MCP integration layer for eth-signer-mcp:
// *mcp.Server construction, tool registration and handlers, error wire-encoding,
// stdio and Streamable HTTP transports, bearer auth, and request logging.
//
// Issue 1.7 lands the SDK spike smoke test; Issue 1.8 adds server.go / stdio.go
// and the full boot test.
package server
