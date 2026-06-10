// Package server — errors.go — Issue 2.7.
// toolResult is the single crossing point from *signing.ToolError to the
// locked MCP wire encoding: IsError=true + Content[0] = compact JSON
// {"code":"...","message":"..."}, nil Go error returned to the SDK.
//
// Non-ToolError errors (e.g. context.Canceled, io.EOF) are NOT encoded here;
// they are returned as-is to the caller, which must propagate them as a
// protocol-level error (non-nil Go error from the handler). The SDK will then
// emit a JSON-RPC error response rather than a tool result.
//
// Wire encoding contract (ADR-004):
//   - IsError = true
//   - Content[0] = TextContent whose Text is compact JSON with EXACTLY two
//     fields, in order: "code" then "message". No indentation.
//   - Cause (on ToolError) is NEVER serialised — only Code and Message cross
//     the wire.
//   - Non-ToolError → (nil, err) returned to the SDK as a protocol error.
package server

import (
	"encoding/json"
	"errors"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/rootwarp/blockchain-ai-tools/apps/eth-signer-mcp/internal/signing"
)

// toolErrorPayload is the exact JSON shape that crosses the wire for a
// tool-level error. Field order in the JSON output is alphabetically stable
// because json.Marshal preserves struct field order. We declare them in
// code→message order to match the architecture's canonical wire shape.
//
// Compact (no indentation) by design — the client JSON-parses this text and
// does not need human-readable whitespace.
type toolErrorPayload struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

// toolResult converts a signing error to the MCP wire encoding for the
// tool-level error path, or propagates it as a protocol-level error.
//
// For *signing.ToolError:
//   - Marshals {"code":"…","message":"…"} as compact JSON.
//   - Sets IsError = true and Content[0] = TextContent(json).
//   - Returns (result, nil) — the nil Go error keeps the JSON-RPC session alive.
//
// For any other error (e.g. context.Canceled):
//   - Returns (nil, err) — protocol-level system failure. The caller must
//     propagate this as a *mcpjsonrpc.Error so the SDK emits a JSON-RPC error.
func toolResult(err error) (*mcp.CallToolResult, error) {
	var te *signing.ToolError
	if !errors.As(err, &te) {
		// Non-ToolError: protocol-level failure. Return as-is for the caller
		// to wrap as a JSON-RPC error.
		return nil, err
	}

	payload, jsonErr := json.Marshal(toolErrorPayload{
		Code:    te.Code,
		Message: te.Message,
	})
	if jsonErr != nil {
		// Fallback: this cannot happen for static strings, but be safe.
		payload = []byte(`{"code":"internal_error","message":"failed to encode error"}`)
	}

	result := &mcp.CallToolResult{
		Content: []mcp.Content{
			&mcp.TextContent{Text: string(payload)},
		},
		IsError: true,
	}
	return result, nil
}
