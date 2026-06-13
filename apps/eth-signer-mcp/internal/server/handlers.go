// Package server — handlers.go — Issue 2.7.
// Handler closures for sign_transaction and get_address.
//
// sign_transaction handler:
//  1. Generates a UUIDv4 request_id (crypto/rand, no new dependency).
//  2. Attaches the ID to the context via signing.WithRequestID.
//  3. Calls the signer and routes the result through toolResult / protocol error.
//  4. Logs the ToolError.Cause (if any) with request_id before encoding.
//
// get_address handler does NOT generate a request_id — no signing occurs and no
// audit line is emitted; the address is served from the boot-time snapshot.
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

	// AddressPointer returns a pointer to the account address if it is known,
	// or nil if the address has not yet been discovered (optional-address
	// keystore before the first successful sign_transaction call).
	AddressPointer() *common.Address
}

// generateRequestID returns a UUID v4 string generated from crypto/rand.
// Format: xxxxxxxx-xxxx-4xxx-yxxx-xxxxxxxxxxxx (RFC 4122 variant bits set).
// This avoids a new uuid dependency — crypto/rand is stdlib.
//
// On Go 1.20+ (and definitely on Go 1.26 used here), crypto/rand.Read never
// returns an error — the runtime panics at init time if the OS CSPRNG is
// unavailable. The previous zero-UUID fallback was therefore dead code that
// would, if ever hit, silently share one UUID across all concurrent requests
// (destroying audit correlation). We now panic explicitly so any hypothetical
// future failure surface is immediately visible rather than silently corrupting
// log correlation (CR finding 4).
func generateRequestID() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		panic(fmt.Errorf("generateRequestID: crypto/rand failed: %w", err))
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
//  1. Reuses an existing request_id from ctx (set by the HTTP reqlog middleware)
//     when present; generates a new UUIDv4 when absent (stdio path, Phase 2
//     behaviour unchanged).
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
		// Reuse the request_id set by the HTTP reqlog middleware when present and
		// valid. On the stdio path (no middleware), none is set → generate a new one.
		//
		// Guard conditions for regeneration (CR finding 3):
		//   !ok          — no id in context (stdio / in-memory transport path)
		//   reqID == ""  — RequestIDFromContext can return ("", true) for an explicit
		//                  empty value; an empty id is as bad as none for correlation.
		//   len > 64     — defensive cap against accidental over-length ids from future
		//                  callers; UUIDv4 is 36 chars; anything beyond 64 is suspicious.
		reqID, ok := signing.RequestIDFromContext(ctx)
		if !ok || reqID == "" || len(reqID) > 64 {
			reqID = generateRequestID()
			ctx = signing.WithRequestID(ctx, reqID)
		}

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
// get_address is served from the vault's address pointer — no password file is
// read, no KDF runs. This makes it safe to call even if the password file has
// been rotated or made unreadable since startup.
//
// Pre-discovery contract: if the vault's AddressPointer() returns nil (i.e. the
// keystore has no declared address and sign_transaction has not yet been called
// successfully), get_address returns IsError:true with code "address_unknown".
// Once the address is discovered (after the first successful sign_transaction),
// subsequent calls return the EIP-55 checksummed address.
func makeGetAddressHandler(
	sp signerPort,
) func(context.Context, *mcp.CallToolRequest, struct{}) (*mcp.CallToolResult, *signing.AddressResult, error) {
	return func(
		ctx context.Context,
		_ *mcp.CallToolRequest,
		_ struct{},
	) (*mcp.CallToolResult, *signing.AddressResult, error) {
		ptr := sp.AddressPointer()
		if ptr == nil {
			err := &signing.ToolError{
				Code:    signing.CodeAddressUnknown,
				Message: "address not yet discovered; call sign_transaction once or configure a keystore with a declared address",
			}
			toolRes, _ := toolResult(err)
			return toolRes, nil, nil
		}
		return nil, &signing.AddressResult{Address: ptr.Hex()}, nil
	}
}
