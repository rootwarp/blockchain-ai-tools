//go:build !windows

package main

import (
	"fmt"
	"log/slog"
	"os"

	"github.com/urfave/cli/v3"
)

// checkPerms reports whether the file at path has group- or world-accessible
// permission bits set.
//
// Symlink handling: os.Stat follows symlinks, so checkPerms checks the
// target file's permissions, not the symlink's own mode bits.  This is
// intentional — what matters is whether the actual secret file (keystore
// JSON or password text) is accessible to other OS users.
//
// Return values:
//
//   - (false, nil):  file exists, is a regular file, and has mode 0600 or
//     stricter (no group/world bits set).
//   - (true, nil):   file exists and is regular, but Mode().Perm()&0o077 != 0
//     (at least one group- or world-readable/writable/executable bit is set).
//   - (false, err):  file is missing, is not a regular file, or stat failed.
func checkPerms(path string) (tooOpen bool, err error) {
	info, err := os.Stat(path)
	if err != nil {
		return false, fmt.Errorf("cannot stat: %w", err)
	}
	if !info.Mode().IsRegular() {
		return false, fmt.Errorf("not a regular file: %q (mode %s)", path, info.Mode().Type())
	}
	if info.Mode().Perm()&0o077 != 0 {
		return true, nil
	}
	return false, nil
}

// applyPermChecks iterates over paths, calls checkPerms for each, and takes the
// appropriate action:
//
//   - stat error / not a regular file:   logs ERROR with "path" attr, returns
//     cli.Exit(…, 2) immediately (fail fast — a typo'd path must not boot a server
//     that can never sign).
//   - too open + strictPerms == true:    logs ERROR, returns cli.Exit(…, 2).
//   - too open + strictPerms == false:   logs WARN and continues.
//   - ok (mode 0600 or stricter):        silent, continues.
//
// Paths are logged; file contents are NEVER logged.
func applyPermChecks(paths []string, strictPerms bool, logger *slog.Logger) error {
	for _, p := range paths {
		tooOpen, err := checkPerms(p)
		if err != nil {
			logger.Error("cannot check file permissions", "path", p, "error", err)
			return cli.Exit("startup aborted: cannot check file permissions", 2)
		}
		if tooOpen && strictPerms {
			logger.Error("refusing to start: file is group/world accessible; chmod 600", "path", p)
			return cli.Exit("startup aborted: refusing to start: file is group/world accessible; chmod 600", 2)
		}
		if tooOpen {
			logger.Warn("file is group/world accessible; consider chmod 600 (use --strict-perms to refuse)", "path", p)
		}
	}
	return nil
}
