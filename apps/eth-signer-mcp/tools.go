//go:build tools

// This file is excluded from normal builds (go:build tools is never satisfied
// by the toolchain). Its sole purpose is to hold blank imports of the direct
// dependencies so that `go mod tidy` does not prune them from go.mod before
// real code adds genuine imports.
//
// Current status (issue 1.10 polish):
//   - urfave/cli/v3: genuinely imported by cmd/eth-signer-mcp — held by a real import.
//   - MCP SDK (github.com/modelcontextprotocol/go-sdk/mcp): imported by
//     internal/server — held by a real import.
//   - jsonschema-go: pulled transitively by the MCP SDK (a real, non-test
//     dependency) — held by a real import; dropped here in 1.10.
//   - go-ethereum: NOT yet imported (internal/signing is stdlib-only in Phase 1);
//     this pin must stay until Phase 2 wires crypto/accounts/keystore/core/types.
//
// When Phase 2 adds the go-ethereum imports to internal/signing, this file
// becomes empty and should be deleted.
package main

import (
	// go-ethereum — crypto, accounts/keystore, and core/types land in
	// internal/signing in Phase 2; pinned here until then.
	_ "github.com/ethereum/go-ethereum/crypto"
)
