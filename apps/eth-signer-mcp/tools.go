//go:build tools

// This file is excluded from normal builds (go:build tools is never satisfied
// by the toolchain). Its sole purpose is to hold blank imports of the four
// direct dependencies so that `go mod tidy` does not prune them from go.mod
// before real code in issues 1.3/1.7/1.8 adds genuine imports.
//
// Once each package is genuinely imported by production code, its blank import
// here should be removed (the 1.10 polish pass shrinks this file to only the
// pins not yet held by real imports).
package main

import (
	// MCP Go SDK — server and test-client surface (Phase 1 spike, issue 1.7;
	// wired in internal/server starting issue 1.8).
	_ "github.com/modelcontextprotocol/go-sdk/mcp"

	// go-ethereum — crypto, keystore, and core/types (internal/signing,
	// issue 1.5 onward).
	_ "github.com/ethereum/go-ethereum/crypto"

	// urfave/cli v3 — CLI framework (cmd/eth-signer-mcp, issue 1.3).
	_ "github.com/urfave/cli/v3"

	// jsonschema-go — schema inference for mcp.AddTool (internal/server,
	// issue 1.7/1.8; there is no SDK-embedded jsonschema package).
	_ "github.com/google/jsonschema-go/jsonschema"
)
