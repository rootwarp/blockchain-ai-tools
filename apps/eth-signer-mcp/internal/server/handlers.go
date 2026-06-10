// Package server — handlers.go — Issue 2.7.
// Handler closures for sign_transaction and get_address.
//
// Each handler:
//  1. Generates a UUIDv4 request_id (crypto/rand, no new dependency).
//  2. Attaches the ID to the context via signing.WithRequestID.
//  3. Calls the signer and routes the result through toolResult / protocol error.
//  4. Logs the ToolError.Cause (if any) with request_id before encoding.
//
// Tool-level vs protocol-level error routing (ADR-004):
//   - *signing.ToolError → toolResult encodes {"code","message"}; IsError=true; nil Go error.
//   - any other error   → wrapped as *mcpjsonrpc.Error; SDK propagates as JSON-RPC error.
package server

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"

	"github.com/ethereum/go-ethereum/common"
	mcpjsonrpc "github.com/modelcontextprotocol/go-sdk/jsonrpc"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/rootwarp/blockchain-ai-tools/apps/eth-signer-mcp/internal/signing"
)

// signerPort is the minimal interface the server handlers require of the signer.
// Using an interface allows handler tests to inject stubs without real keystore
// decryption. The public New function accepts *signing.Signer which satisfies
// this interface.
type signerPort interface {
	// SignTransaction validates req, signs it, and returns a fully-populated
	// SignResult on success, or a *signing.ToolError on any signing failure.
	// Non-ToolError errors (e.g. context.Canceled) are system failures.
	SignTransaction(ctx context.Context, req signing.TxRequest) (*signing.SignResult, error)

	// Address returns the EIP-55 checksummed account address from the
	// boot-time keystore snapshot. Safe to call without a password.
	Address() common.Address
}

// generateRequestID returns a UUID v4 string generated from crypto/rand.
// Format: xxxxxxxx-xxxx-4xxx-yxxx-xxxxxxxxxxxx (RFC 4122 variant bits set).
// This avoids a new uuid dependency — crypto/rand is stdlib.
func generateRequestID() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		// crypto/rand failure is catastrophic on any real OS; use a zero UUID
		// as a last resort so the handler doesn't panic.
		return "00000000-0000-4000-8000-000000000000"
	}
	b[6] = (b[6] & 0x0f) | 0x40 // version 4
	b[8] = (b[8] & 0x3f) | 0x80 // RFC 4122 variant
	return fmt.Sprintf("%s-%s-%s-%s-%s",
		hex.EncodeToString(b[0:4]),
		hex.EncodeToString(b[4:6]),
		hex.EncodeToString(b[6:8]),
		hex.EncodeToString(b[8:10]),
		hex.EncodeToString(b[10:]),
	)
}

// makeSignTransactionHandler returns a ToolHandlerFor for the sign_transaction
// tool. It:
//  1. Generates a request_id and attaches it to ctx.
//  2. Calls signer.SignTransaction(ctx, args).
//  3. Routes *signing.ToolError via toolResult (IsError wire encoding).
//  4. Routes non-ToolError as a protocol-level JSON-RPC error.
//  5. Logs ToolError.Cause with request_id before wire encoding (Cause not serialised).
func makeSignTransactionHandler(
	sp signerPort,
	logger *slog.Logger,
) func(context.Context, *mcp.CallToolRequest, signing.TxRequest) (*mcp.CallToolResult, *signing.SignResult, error) {
	return func(
		ctx context.Context,
		_ *mcp.CallToolRequest,
		args signing.TxRequest,
	) (*mcp.CallToolResult, *signing.SignResult, error) {
		reqID := generateRequestID()
		ctx = signing.WithRequestID(ctx, reqID)

		result, err := sp.SignTransaction(ctx, args)
		if err != nil {
			// Log ToolError.Cause here — it must never be serialised to the wire
			// but is valuable for operator debugging. Non-ToolErrors have no Cause.
			var te *signing.ToolError
			if errors.As(err, &te) && te.Cause != nil {
				logger.Error("sign_transaction: tool error with cause",
					"request_id", reqID,
					"code", te.Code,
					"cause", te.Cause.Error(),
				)
			}

			toolRes, toolErr := toolResult(err)
			if toolErr != nil {
				// Non-ToolError: log and propagate as a protocol-level JSON-RPC error.
				// The raw error message may contain context (e.g. "context canceled")
				// and is safe to include in internal logs, but the wire message is
				// intentionally generic to avoid leaking internal state.
				logger.Error("sign_transaction: system failure",
					"request_id", reqID,
					"error", toolErr.Error(),
				)
				return nil, nil, &mcpjsonrpc.Error{
					Code:    mcpjsonrpc.CodeInternalError,
					Message: "internal server error",
				}
			}
			return toolRes, nil, nil
		}

		return nil, result, nil
	}
}

// makeGetAddressHandler returns a ToolHandlerFor for the get_address tool.
// get_address is served from the boot-time signer.Address() snapshot — no
// password file is read, no KDF runs. This makes it safe to call even if the
// password file has been rotated or made unreadable since startup.
func makeGetAddressHandler(
	sp signerPort,
) func(context.Context, *mcp.CallToolRequest, struct{}) (*mcp.CallToolResult, *signing.AddressResult, error) {
	return func(
		ctx context.Context,
		_ *mcp.CallToolRequest,
		_ struct{},
	) (*mcp.CallToolResult, *signing.AddressResult, error) {
		addr := sp.Address()
		result := &signing.AddressResult{
			Address: addr.Hex(), // EIP-55 checksummed via common.Address.Hex()
		}
		return nil, result, nil
	}
}
