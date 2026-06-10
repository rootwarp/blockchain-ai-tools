// Package main is the entry point for the eth-signer-mcp binary.
//
// Why this file lives under cmd/eth-signer-mcp/ rather than at the module root:
//
//  1. Architecture layout — the module is structured as one cmd composition root
//     plus three internal packages (internal/signing, internal/server, internal/obs).
//     The module root is kept package-free so the directory tree mirrors these four
//     concern clusters cleanly.
//
//  2. Binary naming — `go build ./cmd/eth-signer-mcp` produces a binary named
//     "eth-signer-mcp" (matching the cmd directory). A root-level main.go would
//     produce a binary named after the module's last path segment, which is also
//     "eth-signer-mcp" but is a coincidence rather than a contract; the cmd/
//     convention makes the intent explicit.
//
//  3. Future-proofing — keeping the module root package-free prevents the scaffolder
//     or future contributors from inadvertently reintroducing a root-level main.go
//     that conflicts with this entry point. If additional binaries are ever needed
//     (e.g. a migration tool), they add a second cmd/<name>/ directory without
//     disturbing the existing layout.
package main

import "fmt"

func main() {
	fmt.Println("eth-signer-mcp: hello from github.com/rootwarp/blockchain-ai-tools/apps/eth-signer-mcp")
}
