//go:build tools

// This file is excluded from normal builds (go:build tools is never satisfied
// by the toolchain). Its sole purpose is to hold blank imports of the direct
// dependencies so that `go mod tidy` does not prune them from go.mod before
// real code adds genuine imports.
//
// Current status (issue 1.7 complete):
//   - urfave/cli/v3: genuinely imported by cmd/eth-signer-mcp/main.go — removed here.
//   - MCP SDK (github.com/modelcontextprotocol/go-sdk/mcp): now genuinely imported
//     by internal/server/spike_smoke_test.go (issue 1.7) — removed here.
//   - go-ethereum, jsonschema-go: still pending real imports (issues 1.5/1.8).
//
// The 1.10 polish pass shrinks this file further once real imports hold each pin.
package main

import (
	// go-ethereum — crypto, keystore, and core/types (internal/signing,
	// issue 1.5 onward).
	_ "github.com/ethereum/go-ethereum/crypto"

	// jsonschema-go — schema inference for mcp.AddTool (internal/server,
	// issue 1.8; there is no SDK-embedded jsonschema package).
	_ "github.com/google/jsonschema-go/jsonschema"
)
