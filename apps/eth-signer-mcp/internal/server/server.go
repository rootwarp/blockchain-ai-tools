// Package server implements the MCP integration layer for eth-signer-mcp:
// *mcp.Server construction, tool registration and handlers, error wire-encoding,
// stdio and Streamable HTTP transports, bearer auth, and request logging.
//
// Issue 1.7 lands the SDK spike smoke test; Issue 1.8 adds server.go / stdio.go
// and the full boot test.
// Issue 2.7 adds sign_transaction and get_address tool registration and the
// error wire-encoding contract (ADR-004).
package server

import (
	"context"
	"log/slog"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/rootwarp/blockchain-ai-tools/apps/eth-signer-mcp/internal/signing"
)

// Options configures the MCP server instance.
type Options struct {
	Name, Version string // advertised on MCP initialize
	Logger        *slog.Logger
}

// Server wraps the *mcp.Server and its supporting state.
type Server struct {
	mcpServer *mcp.Server
	logger    *slog.Logger
}

// New constructs the *mcp.Server advertising opts.Name and opts.Version on the
// MCP initialize handshake, registers sign_transaction and get_address against
// the given signer, and returns a *Server ready to run a transport.
//
// Both tools are registered once at construction time using mcp.AddTool, which
// infers the JSON Schema from the typed input structs (signing.TxRequest and
// struct{}) via github.com/google/jsonschema-go. additionalProperties:false
// falls out of struct inference — unknown fields are rejected (PRD strict schema).
//
// Tool descriptions state supported tx types (0 and 2) and that the result is
// NOT broadcast anywhere.
func New(signer *signing.Signer, opts Options) *Server {
	return newServer(signer, opts)
}

// newServer is the internal constructor that accepts a signerPort interface.
// New delegates here; tests can call newServer with a stub signerPort directly
// to avoid real keystore decryption in handler tests.
func newServer(sp signerPort, opts Options) *Server {
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

	// Register sign_transaction.
	//
	// Input:  signing.TxRequest  — inferred schema (additionalProperties:false).
	// Output: *signing.SignResult — all fields present; no omitempty on hash/from.
	// Types 0 and 2 supported; result is never broadcast (offline signer only).
	mcp.AddTool(mcpSrv,
		&mcp.Tool{
			Name: "sign_transaction",
			Description: "Sign a fully-specified Ethereum transaction (type 0 / legacy or " +
				"type 2 / EIP-1559) with the loaded keystore. " +
				"Supported types: 0x0 (legacy, EIP-155) and 0x2 (EIP-1559). " +
				"The signed transaction is returned as a hex-encoded RLP string " +
				"(rawTransaction) ready for eth_sendRawTransaction, " +
				"along with signature components and the transaction hash. " +
				"The result is NOT broadcast — the caller is responsible for submission.",
		},
		makeSignTransactionHandler(sp, logger),
	)

	// Register get_address.
	//
	// Input:  struct{} (no arguments) — empty object schema.
	// Output: *signing.AddressResult — EIP-55 checksummed address.
	// Served from the boot-time keystore snapshot; no password file read.
	mcp.AddTool(mcpSrv,
		&mcp.Tool{
			Name: "get_address",
			Description: "Return the EIP-55 checksummed Ethereum address of the loaded " +
				"keystore account. " +
				"This is a read-only operation served from the boot-time keystore snapshot; " +
				"the password file is NOT read and no KDF runs on this path. " +
				"Safe to call even if the password file has been rotated or made unreadable.",
		},
		makeGetAddressHandler(sp),
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
