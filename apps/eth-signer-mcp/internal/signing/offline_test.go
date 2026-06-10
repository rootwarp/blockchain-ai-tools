// Package signing_test contains tests for the signing package.
package signing_test

import (
	"testing"

	"golang.org/x/tools/go/packages"
)

// TestOfflineImports is the structural enforcement of ADR-007
// (PRD P0-SIGN-5/P0-SEC-6). It passes VACUOUSLY in Phase 1 —
// internal/signing imports neither go-ethereum nor any network package
// yet, so an empty result proves nothing about offline-ness. It becomes
// load-bearing in Phase 2 when accounts/keystore, core/types, and crypto
// land; do not treat a green run before then as an offline guarantee.
//
// Note: packages.Load must run from inside the module; make test cds per
// module, so CI is fine. Running go test from the repo root (outside the
// module) may not resolve the module context correctly.
func TestOfflineImports(t *testing.T) {
	t.Parallel()

	const pkgPath = "github.com/rootwarp/blockchain-ai-tools/apps/eth-signer-mcp/internal/signing"

	forbidden := []string{
		"net/http",
		"net/rpc",
		"github.com/ethereum/go-ethereum/ethclient",
		"github.com/ethereum/go-ethereum/rpc",
	}

	cfg := &packages.Config{
		Mode: packages.NeedName | packages.NeedImports | packages.NeedDeps,
	}

	pkgs, err := packages.Load(cfg, pkgPath)
	if err != nil {
		t.Fatalf("packages.Load: %v", err)
	}
	if len(pkgs) == 0 {
		t.Fatal("packages.Load returned no packages for signing")
	}

	// Walk the transitive import graph and collect all reachable package paths.
	visited := make(map[string]bool)
	for _, pkg := range pkgs {
		walkImports(pkg, visited)
	}

	// Fail if any forbidden package is reachable from internal/signing.
	for _, f := range forbidden {
		if visited[f] {
			t.Errorf("internal/signing transitively imports forbidden package %q (violates ADR-007 offline invariant)", f)
		}
	}
}

// walkImports recursively walks the transitive import graph starting from pkg,
// collecting all reachable package paths into visited. It is the ADR-007
// offline-import walker — a ~20-line helper, no sub-package ceremony needed.
func walkImports(pkg *packages.Package, visited map[string]bool) {
	if pkg == nil || visited[pkg.PkgPath] {
		return
	}
	visited[pkg.PkgPath] = true
	for _, imp := range pkg.Imports {
		walkImports(imp, visited)
	}
}
