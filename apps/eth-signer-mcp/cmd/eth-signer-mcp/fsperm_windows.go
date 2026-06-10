//go:build windows

package main

import "log/slog"

// checkPerms is a no-op on Windows.
//
// Windows uses ACL-based (Access Control List) access control rather than
// POSIX mode bits, so the 0o077 group/world-readable check that checkPerms
// performs on POSIX systems does not map to Windows semantics.  File security
// on Windows is governed by the operating system's ACLs, which typically
// provide equivalent or stronger protection than POSIX mode bits for files
// created through normal Windows tooling.
//
// This file satisfies the GOOS=windows compile check introduced in the CI
// workflow (issue 1.2): GOOS=windows GOARCH=amd64 go build ./...
func checkPerms(_ string) (tooOpen bool, err error) {
	return false, nil
}

// applyPermChecks is a no-op on Windows (see checkPerms for rationale).
func applyPermChecks(_ []string, _ bool, _ *slog.Logger) error {
	return nil
}
