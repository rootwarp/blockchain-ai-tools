// Package signing_test contains tests for the signing package.
package signing_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// TestDepguardRuleFires proves that the depguard signing-offline-and-leaf
// rule actually fires on a deliberate violation. A depguard config that
// silently never matches is worse than no config (ADR-008, Issue 1.9).
//
// Steps:
//  1. Skip if golangci-lint is not on PATH (always present in CI per issue 1.2).
//  2. Write internal/signing/zz_depguard_violation.go (//go:build depguard_violation)
//     containing a forbidden import; remove it via t.Cleanup even on failure.
//  3. Run golangci-lint from the module directory with --build-tags depguard_violation.
//  4. Assert non-zero exit AND a depguard diagnostic naming net/http.
func TestDepguardRuleFires(t *testing.T) {
	// 1. Skip if golangci-lint not on PATH.
	if _, err := exec.LookPath("golangci-lint"); err != nil {
		t.Skip("golangci-lint not found on PATH; skipping deliberate-violation test")
	}

	// Determine the module root from the test working directory.
	// go test sets cwd to the package directory; this test file lives in
	// internal/signing/, so the module root is two levels up.
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatalf("os.Getwd: %v", err)
	}
	moduleDir := filepath.Clean(filepath.Join(cwd, "../.."))

	// 2. Write the violation file, guarded by a build tag so it never
	// contaminates the real build. Placement: internal/signing/ so that
	// the signing-offline-and-leaf rule glob fires on it.
	violationFile := filepath.Join(moduleDir, "internal", "signing", "zz_depguard_violation.go")
	const violationSrc = `//go:build depguard_violation

// This file is written by TestDepguardRuleFires to verify that the
// signing-offline-and-leaf depguard rule fires. It is always removed by
// t.Cleanup and must never be committed.
package signing

import _ "net/http"
`
	if err := os.WriteFile(violationFile, []byte(violationSrc), 0600); err != nil {
		t.Fatalf("WriteFile violation: %v", err)
	}
	// t.Cleanup runs even when assertions fail — the tree stays clean.
	t.Cleanup(func() {
		if removeErr := os.Remove(violationFile); removeErr != nil && !os.IsNotExist(removeErr) {
			t.Logf("cleanup: could not remove violation file %s: %v", violationFile, removeErr)
		}
	})

	// 3. Run golangci-lint with the violation build tag active.
	// The --build-tags flag activates the zz_depguard_violation.go file
	// (guarded by //go:build depguard_violation), which imports "net/http" —
	// a package denied by the signing-offline-and-leaf rule.
	cmd := exec.Command("golangci-lint", "run",
		"--build-tags", "depguard_violation",
		"./internal/signing/...")
	cmd.Dir = moduleDir
	out, err := cmd.CombinedOutput()

	// 4. Assert non-zero exit (linter must report the violation).
	if err == nil {
		t.Errorf("expected golangci-lint to exit non-zero on depguard violation, but exited 0;\noutput:\n%s", out)
		return
	}

	// Assert the output contains a depguard diagnostic naming net/http.
	// This proves the glob in signing-offline-and-leaf actually fires —
	// the known failure mode is a path pattern that silently never matches.
	outStr := string(out)
	if !strings.Contains(outStr, "depguard") || !strings.Contains(outStr, "net/http") {
		t.Errorf("expected depguard diagnostic naming net/http in output;\ngot:\n%s", outStr)
	}
}
