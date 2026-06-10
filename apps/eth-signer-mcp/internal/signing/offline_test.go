// Package signing_test — offline_test.go — ADR-007 import-graph enforcement.
//
// # ADR-007: Offline Invariant
//
// internal/signing must never, directly or transitively, import any package
// that can initiate a network connection. This is a hard security boundary:
// the signing package holds key material and must remain completely offline to
// prevent key exfiltration via side-channels that a network-capable dependency
// could enable.
//
// This test loads the signing package's full transitive import graph using
// golang.org/x/tools/go/packages and fails immediately if any of the following
// forbidden paths appear anywhere in that graph:
//
//   - "net/http" — Go's standard HTTP client/server. Any package importing it
//     can open outbound TCP connections. A signing package that can reach
//     net/http could, in principle, exfiltrate key material over HTTP.
//
//   - "net/rpc" — Go's built-in RPC framework. Equivalent risk to net/http:
//     it can open network connections and expose services over TCP.
//
//   - "github.com/ethereum/go-ethereum/ethclient" — go-ethereum's Ethereum
//     JSON-RPC client. Directly dials an Ethereum node over HTTP/WebSocket.
//     A signing package importing this could broadcast transactions or leak
//     addresses to an external node without the caller's knowledge.
//
//   - "github.com/ethereum/go-ethereum/rpc" — go-ethereum's underlying RPC
//     transport layer, which ethclient depends on. Importing it alone is
//     sufficient to open dial connections to arbitrary endpoints.
//
// # Why internal/server's net/http is out of scope
//
// internal/server legitimately imports net/http for the MCP Streamable HTTP
// server (Phase 3). That is a *server-side* use — it accepts inbound connections
// rather than initiating outbound ones — and it lives in a separate package.
// internal/signing is prohibited from importing internal/server (ADR-008: signing
// is a leaf node), so internal/server's net/http use is structurally unreachable
// from this package's import graph. This test only walks internal/signing's graph;
// internal/server is never in scope here.
//
// # Phase 2 load-bearing
//
// In Phase 1 the signing package imported nothing from go-ethereum, so this test
// passed vacuously — an empty graph has no forbidden paths. In Phase 2,
// accounts/keystore, core/types, and crypto land in internal/signing, creating a
// substantial real import graph. The test now walks that graph and a passing run is
// a meaningful offline guarantee, not an empty assertion.
//
// # Failure output
//
// When a forbidden path is found, the error names the direct importer:
//
//	ADR-007: "github.com/some/dep" → "net/http" (offline invariant violated; PRD P0-SIGN-5/P0-SEC-6)
//
// This makes CI failures diagnosable without a local checkout.
//
// # Performance
//
// packages.Load is configured with NeedName|NeedImports|NeedDeps only — the
// minimum set required to walk the import graph. Type information, syntax trees,
// and compiled objects are not requested. The test completes in well under 5 s.
//
// Note: packages.Load must run from inside the module; make test cds per module,
// so CI is fine. Running go test from the repo root (outside the module) may not
// resolve the module context correctly.
package signing_test

import (
	"testing"

	"golang.org/x/tools/go/packages"
)

// TestOfflineImports enforces ADR-007: internal/signing must not transitively
// import any HTTP/RPC client package. See the package-level doc comment above
// for the full rationale and forbidden-path explanations.
func TestOfflineImports(t *testing.T) {
	t.Parallel()

	const pkgPath = "github.com/rootwarp/blockchain-ai-tools/apps/eth-signer-mcp/internal/signing"

	// Deny-list: any of these appearing in the transitive import graph is a
	// violation. This is a deny-list (not an allow-list) so that legitimate new
	// dependencies — go-ethereum crypto primitives, standard library packages,
	// etc. — can be added without modifying this test. Only network-capable
	// packages need to appear here.
	forbidden := []string{
		"net/http",
		"net/rpc",
		"github.com/ethereum/go-ethereum/ethclient",
		"github.com/ethereum/go-ethereum/rpc",
	}

	cfg := &packages.Config{
		// NeedName+NeedImports+NeedDeps is the minimal mode that loads the full
		// transitive import graph. Type info, syntax, and compiled objects are
		// not needed and would slow the walk significantly.
		Mode: packages.NeedName | packages.NeedImports | packages.NeedDeps,
	}

	pkgs, err := packages.Load(cfg, pkgPath)
	if err != nil {
		t.Fatalf("packages.Load: %v", err)
	}
	if len(pkgs) == 0 {
		t.Fatal("packages.Load returned no packages for signing")
	}

	// Walk the transitive import graph, collecting all reachable package paths
	// (visited) and recording the direct importer of each package (parent).
	// The parent map enables diagnostic messages that name the offending edge.
	visited := make(map[string]bool)
	parent := make(map[string]string)
	for _, pkg := range pkgs {
		walkImports(pkg, visited, parent)
	}

	// Fail if any forbidden package is reachable from internal/signing.
	// The error names the direct importer so the violation is diagnosable from
	// CI logs alone without a local checkout.
	for _, f := range forbidden {
		if visited[f] {
			importer := parent[f]
			if importer == "" {
				importer = pkgPath // direct import from the root
			}
			t.Errorf("ADR-007: %q → %q (offline invariant violated; PRD P0-SIGN-5/P0-SEC-6)",
				importer, f)
		}
	}
}

// walkImports recursively walks the transitive import graph starting from pkg,
// collecting all reachable package paths into visited. For each package
// encountered for the first time, parent records the PkgPath of the direct
// importer — this allows forbidden-package diagnostics to name the offending
// edge ("X imports Y") rather than just the forbidden path.
//
// It is the ADR-007 offline-import walker. Cycles are handled by the visited
// guard: a package is only processed once regardless of how many paths lead to
// it.
func walkImports(pkg *packages.Package, visited map[string]bool, parent map[string]string) {
	if pkg == nil || visited[pkg.PkgPath] {
		return
	}
	visited[pkg.PkgPath] = true
	for _, imp := range pkg.Imports {
		if imp == nil {
			continue
		}
		// Record the first importer we encounter for each package so we can
		// name the edge in failure output. Only set when imp has not yet been
		// visited to preserve the first-encountered importer.
		if !visited[imp.PkgPath] {
			parent[imp.PkgPath] = pkg.PkgPath
		}
		walkImports(imp, visited, parent)
	}
}
