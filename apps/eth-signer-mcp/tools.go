//go:build tools

// This file is excluded from normal builds (go:build tools is never satisfied
// by the toolchain). Its sole purpose is to hold blank imports of the direct
// dependencies so that `go mod tidy` does not prune them from go.mod before
// real code adds genuine imports.
//
// Current status (issue 2.1):
//   - urfave/cli/v3: genuinely imported by cmd/eth-signer-mcp — held by a real import.
//   - MCP SDK (github.com/modelcontextprotocol/go-sdk/mcp): imported by
//     internal/server — held by a real import.
//   - jsonschema-go: pulled transitively by the MCP SDK (a real, non-test
//     dependency) — held by a real import.
//   - go-ethereum: imported by internal/signing test files since issue 2.1
//     (fixtures_test.go uses accounts/keystore). Production code imports
//     (accounts/keystore, core/types, crypto) land in issues 2.2+.
//     This pin can be removed once any production file in internal/signing imports
//     go-ethereum directly. Scheduled for cleanup in issue 2.12 polish pass.
//
// When all entries above are held by real imports, this file becomes empty and
// should be deleted.
package main

import (
	// go-ethereum — crypto, accounts/keystore, and core/types land in
	// internal/signing in Phase 2; pinned here until then.
	_ "github.com/ethereum/go-ethereum/crypto"
)
