//go:build !windows

package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/urfave/cli/v3"
)

// ---- helpers ---------------------------------------------------------------

// writeTestFile creates a file in a unique sub-directory of t.TempDir() with
// the given content and an explicit chmod (umask-independent).
func writeTestFile(t *testing.T, content []byte, mode os.FileMode) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "testfile")
	// WriteFile with 0o600 first, then Chmod to the desired mode so the umask
	// cannot silently narrow the permissions we care about testing.
	if err := os.WriteFile(path, content, 0o600); err != nil {
		t.Fatalf("writeTestFile: WriteFile: %v", err)
	}
	if err := os.Chmod(path, mode); err != nil {
		t.Fatalf("writeTestFile: Chmod(%04o): %v", mode, err)
	}
	return path
}

// skipIfRoot skips the current test when running as root (uid 0), because
// the kernel ignores permission bits for root, making chmod-based tests vacuous.
func skipIfRoot(t *testing.T) {
	t.Helper()
	if os.Geteuid() == 0 {
		t.Skip("skipping permission-bit tests: running as root (kernel ignores mode bits for uid 0)")
	}
}

// bufLogger returns a JSON slog.Logger writing to buf at debug level.
func bufLogger(buf *bytes.Buffer) *slog.Logger {
	return slog.New(slog.NewJSONHandler(buf, &slog.HandlerOptions{Level: slog.LevelDebug}))
}

// parseLogLine unmarshals a single JSON log line into a map.
func parseLogLine(t *testing.T, line []byte) map[string]any {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal(line, &m); err != nil {
		t.Fatalf("parseLogLine: %v\nline: %s", err, line)
	}
	return m
}

// splitLogLines returns non-empty JSON lines from buf.
func splitLogLines(buf *bytes.Buffer) [][]byte {
	var lines [][]byte
	for _, line := range bytes.Split(buf.Bytes(), []byte("\n")) {
		if len(bytes.TrimSpace(line)) > 0 {
			lines = append(lines, line)
		}
	}
	return lines
}

// ---- checkPerms unit tests -------------------------------------------------

// TestCheckPerms_Mode600 verifies that a file with mode 0600 is reported as not
// too open and no error is returned.
func TestCheckPerms_Mode600(t *testing.T) {
	skipIfRoot(t)
	t.Parallel()

	path := writeTestFile(t, []byte("keydata"), 0o600)
	tooOpen, err := checkPerms(path)
	if err != nil {
		t.Fatalf("checkPerms(0600) error = %v, want nil", err)
	}
	if tooOpen {
		t.Error("checkPerms(0600) tooOpen = true, want false")
	}
}

// TestCheckPerms_Mode640_TooOpen verifies that a file with mode 0640 (group-readable)
// is reported as tooOpen.
func TestCheckPerms_Mode640_TooOpen(t *testing.T) {
	skipIfRoot(t)
	t.Parallel()

	path := writeTestFile(t, []byte("keydata"), 0o640)
	tooOpen, err := checkPerms(path)
	if err != nil {
		t.Fatalf("checkPerms(0640) error = %v, want nil", err)
	}
	if !tooOpen {
		t.Error("checkPerms(0640) tooOpen = false, want true")
	}
}

// TestCheckPerms_Mode644_TooOpen verifies that a file with mode 0644 (group+world
// readable) is reported as tooOpen.
func TestCheckPerms_Mode644_TooOpen(t *testing.T) {
	skipIfRoot(t)
	t.Parallel()

	path := writeTestFile(t, []byte("keydata"), 0o644)
	tooOpen, err := checkPerms(path)
	if err != nil {
		t.Fatalf("checkPerms(0644) error = %v, want nil", err)
	}
	if !tooOpen {
		t.Error("checkPerms(0644) tooOpen = false, want true")
	}
}

// TestCheckPerms_Mode022_TooOpen verifies that a file with mode 0622 (group+world
// writable) is also reported as tooOpen.
func TestCheckPerms_Mode022_TooOpen(t *testing.T) {
	skipIfRoot(t)
	t.Parallel()

	path := writeTestFile(t, []byte("keydata"), 0o622)
	tooOpen, err := checkPerms(path)
	if err != nil {
		t.Fatalf("checkPerms(0622) error = %v, want nil", err)
	}
	if !tooOpen {
		t.Error("checkPerms(0622) tooOpen = false, want true")
	}
}

// TestCheckPerms_MissingFile verifies that a missing file returns (false, err).
func TestCheckPerms_MissingFile(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "no-such-file")
	tooOpen, err := checkPerms(path)
	if err == nil {
		t.Fatal("checkPerms(missing) error = nil, want error")
	}
	if tooOpen {
		t.Error("checkPerms(missing) tooOpen = true, want false on error")
	}
}

// TestCheckPerms_Directory verifies that a directory (not a regular file) returns
// (false, err).
func TestCheckPerms_Directory(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	tooOpen, err := checkPerms(dir)
	if err == nil {
		t.Fatal("checkPerms(directory) error = nil, want error")
	}
	if tooOpen {
		t.Error("checkPerms(directory) tooOpen = true, want false on error")
	}
}

// ---- applyPermChecks unit tests --------------------------------------------

// TestApplyPermChecks_Mode600_NoOutput verifies that a file with mode 0600
// produces no log output and returns nil.
func TestApplyPermChecks_Mode600_NoOutput(t *testing.T) {
	skipIfRoot(t)
	t.Parallel()

	path := writeTestFile(t, []byte("keydata"), 0o600)
	var buf bytes.Buffer
	err := applyPermChecks([]string{path}, false, bufLogger(&buf))
	if err != nil {
		t.Fatalf("applyPermChecks(0600, strict=false) = %v, want nil", err)
	}
	if buf.Len() > 0 {
		t.Errorf("applyPermChecks(0600) produced log output, want none:\n%s", buf.String())
	}
}

// TestApplyPermChecks_Mode644_NoStrictPerms_Warn verifies that a file with mode 0644
// and !strictPerms produces exactly one WARN log line whose "path" attribute equals
// the file path, and that nil is returned (process continues).
func TestApplyPermChecks_Mode644_NoStrictPerms_Warn(t *testing.T) {
	skipIfRoot(t)
	t.Parallel()

	path := writeTestFile(t, []byte("keydata"), 0o644)
	var buf bytes.Buffer
	err := applyPermChecks([]string{path}, false /*strictPerms*/, bufLogger(&buf))
	if err != nil {
		t.Fatalf("applyPermChecks(0644, strict=false) = %v, want nil (warn, not error)", err)
	}

	lines := splitLogLines(&buf)
	if len(lines) != 1 {
		t.Fatalf("expected 1 log line, got %d:\n%s", len(lines), buf.String())
	}

	entry := parseLogLine(t, lines[0])
	if entry["level"] != "WARN" {
		t.Errorf("log level = %q, want WARN", entry["level"])
	}
	if entry["path"] != path {
		t.Errorf("log path = %v, want %q", entry["path"], path)
	}
	if msg, _ := entry["msg"].(string); msg == "" {
		t.Error("log msg is empty, want non-empty")
	}
}

// TestApplyPermChecks_Mode644_StrictPerms_Exit2 verifies that a file with mode 0644
// and strictPerms=true returns an ExitCoder with code 2, and an ERROR is logged.
func TestApplyPermChecks_Mode644_StrictPerms_Exit2(t *testing.T) {
	skipIfRoot(t)
	t.Parallel()

	path := writeTestFile(t, []byte("keydata"), 0o644)
	var buf bytes.Buffer
	err := applyPermChecks([]string{path}, true /*strictPerms*/, bufLogger(&buf))
	if err == nil {
		t.Fatal("applyPermChecks(0644, strict=true) = nil, want ExitCoder(2)")
	}

	var ec cli.ExitCoder
	if !errors.As(err, &ec) {
		t.Fatalf("error does not implement cli.ExitCoder: %T %v", err, err)
	}
	if ec.ExitCode() != 2 {
		t.Errorf("ExitCode() = %d, want 2", ec.ExitCode())
	}

	// Verify that an ERROR log line was emitted with the correct path.
	lines := splitLogLines(&buf)
	if len(lines) == 0 {
		t.Fatal("no log lines produced, want at least one ERROR line")
	}
	entry := parseLogLine(t, lines[0])
	if entry["level"] != "ERROR" {
		t.Errorf("log level = %q, want ERROR", entry["level"])
	}
	if entry["path"] != path {
		t.Errorf("log path = %v, want %q", entry["path"], path)
	}
}

// TestApplyPermChecks_MissingFile_Exit2 verifies that a missing path returns
// ExitCoder(2) immediately and an ERROR is logged.
func TestApplyPermChecks_MissingFile_Exit2(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "no-such-file")
	var buf bytes.Buffer
	err := applyPermChecks([]string{path}, false, bufLogger(&buf))
	if err == nil {
		t.Fatal("applyPermChecks(missing) = nil, want ExitCoder(2)")
	}

	var ec cli.ExitCoder
	if !errors.As(err, &ec) {
		t.Fatalf("error does not implement cli.ExitCoder: %T %v", err, err)
	}
	if ec.ExitCode() != 2 {
		t.Errorf("ExitCode() = %d, want 2", ec.ExitCode())
	}

	lines := splitLogLines(&buf)
	if len(lines) == 0 {
		t.Fatal("no log lines produced, want ERROR line for missing file")
	}
	entry := parseLogLine(t, lines[0])
	if entry["level"] != "ERROR" {
		t.Errorf("log level = %q, want ERROR", entry["level"])
	}
	if entry["path"] != path {
		t.Errorf("log path = %v, want %q", entry["path"], path)
	}
}

// TestApplyPermChecks_KeystoreAndPasswordFile_BothChecked verifies that both
// paths (keystore AND password-file) are independently checked.
//
// Keystore is 0644, password-file is 0644: both must produce WARN lines.
func TestApplyPermChecks_BothPaths_EachWarned(t *testing.T) {
	skipIfRoot(t)
	t.Parallel()

	keystore := writeTestFile(t, []byte("ks"), 0o644)
	password := writeTestFile(t, []byte("pw"), 0o644)

	var buf bytes.Buffer
	err := applyPermChecks([]string{keystore, password}, false, bufLogger(&buf))
	if err != nil {
		t.Fatalf("applyPermChecks(both 0644, strict=false) = %v, want nil", err)
	}

	lines := splitLogLines(&buf)
	if len(lines) != 2 {
		t.Fatalf("expected 2 WARN lines (one per path), got %d:\n%s", len(lines), buf.String())
	}
	// Both lines must be WARN with a non-empty path.
	for i, line := range lines {
		entry := parseLogLine(t, line)
		if entry["level"] != "WARN" {
			t.Errorf("line %d: level = %q, want WARN", i, entry["level"])
		}
	}
}

// TestApplyPermChecks_KeystoreTooOpen_PasswordOK verifies that when the keystore
// is too-open and the password file is fine, a WARN is emitted for the keystore only.
func TestApplyPermChecks_KeystoreTooOpen_PasswordOK(t *testing.T) {
	skipIfRoot(t)
	t.Parallel()

	keystore := writeTestFile(t, []byte("ks"), 0o644)
	password := writeTestFile(t, []byte("pw"), 0o600)

	var buf bytes.Buffer
	err := applyPermChecks([]string{keystore, password}, false, bufLogger(&buf))
	if err != nil {
		t.Fatalf("applyPermChecks = %v, want nil", err)
	}

	lines := splitLogLines(&buf)
	if len(lines) != 1 {
		t.Fatalf("expected 1 WARN line (keystore only), got %d:\n%s", len(lines), buf.String())
	}
	entry := parseLogLine(t, lines[0])
	if entry["path"] != keystore {
		t.Errorf("log path = %v, want keystore path %q", entry["path"], keystore)
	}
}

// TestApplyPermChecks_PasswordTooOpen_KeystoreOK verifies that when the password
// file is too-open and the keystore is fine, a WARN is emitted for the password only.
func TestApplyPermChecks_PasswordTooOpen_KeystoreOK(t *testing.T) {
	skipIfRoot(t)
	t.Parallel()

	keystore := writeTestFile(t, []byte("ks"), 0o600)
	password := writeTestFile(t, []byte("pw"), 0o644)

	var buf bytes.Buffer
	err := applyPermChecks([]string{keystore, password}, false, bufLogger(&buf))
	if err != nil {
		t.Fatalf("applyPermChecks = %v, want nil", err)
	}

	lines := splitLogLines(&buf)
	if len(lines) != 1 {
		t.Fatalf("expected 1 WARN line (password only), got %d:\n%s", len(lines), buf.String())
	}
	entry := parseLogLine(t, lines[0])
	if entry["path"] != password {
		t.Errorf("log path = %v, want password path %q", entry["path"], password)
	}
}

// ---- binary-level exit-code tests ------------------------------------------
//
// These tests build the real eth-signer-mcp binary and drive it as a child
// process to assert the OS process exit code is exactly 2.  They are bounded
// by a 10-second timeout context.

// testBin is built exactly once per test-binary invocation (sync.Once) to avoid
// repeated go build calls.  The temp directory is intentionally leaked: the test
// binary's lifetime is the process lifetime.
var (
	testBinOnce     sync.Once
	testBinPath     string
	testBinBuildErr error
)

// getTestBinary returns the path to the built eth-signer-mcp binary, building it
// on the first call.
func getTestBinary(t *testing.T) string {
	t.Helper()
	testBinOnce.Do(func() {
		dir, err := os.MkdirTemp("", "eth-signer-mcp-testbin-*")
		if err != nil {
			testBinBuildErr = fmt.Errorf("MkdirTemp: %w", err)
			return
		}
		testBinPath = filepath.Join(dir, "eth-signer-mcp")
		// "." targets the current package directory (cmd/eth-signer-mcp).
		cmd := exec.Command("go", "build", "-o", testBinPath, ".")
		if out, err := cmd.CombinedOutput(); err != nil {
			testBinBuildErr = fmt.Errorf("go build: %w\n%s", err, out)
		}
	})
	if testBinBuildErr != nil {
		t.Fatalf("cannot build test binary: %v", testBinBuildErr)
	}
	return testBinPath
}

// exitCodeFromError extracts the OS process exit code from an *exec.ExitError.
// Returns 0 for nil (success), calls t.Fatal for other error types.
func exitCodeFromError(t *testing.T, err error) int {
	t.Helper()
	if err == nil {
		return 0
	}
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		return exitErr.ExitCode()
	}
	t.Fatalf("unexpected error running binary: %T %v", err, err)
	return -1
}

// TestBinary_StrictPerms_ExitCode2 drives the real binary with a group-readable
// keystore and --strict-perms and asserts OS exit code 2.
func TestBinary_StrictPerms_ExitCode2(t *testing.T) {
	skipIfRoot(t)

	bin := getTestBinary(t)

	keystore := writeTestFile(t, []byte("{}"), 0o644) // group-readable → too open
	password := writeTestFile(t, []byte("pass"), 0o600)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	cmd := exec.CommandContext(ctx, bin,
		"--keystore", keystore,
		"--password-file", password,
		"--strict-perms",
	)
	err := cmd.Run()
	code := exitCodeFromError(t, err)
	if code != 2 {
		t.Errorf("exit code = %d, want 2 (--strict-perms + group-readable keystore)", code)
	}
}

// TestBinary_MissingKeystore_ExitCode2 drives the real binary with a missing
// keystore path and asserts OS exit code 2.
func TestBinary_MissingKeystore_ExitCode2(t *testing.T) {
	bin := getTestBinary(t)

	password := writeTestFile(t, []byte("pass"), 0o600)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	cmd := exec.CommandContext(ctx, bin,
		"--keystore", filepath.Join(t.TempDir(), "no-such-keystore"),
		"--password-file", password,
	)
	err := cmd.Run()
	code := exitCodeFromError(t, err)
	if code != 2 {
		t.Errorf("exit code = %d, want 2 (missing keystore path)", code)
	}
}

// TestBinary_MissingPasswordFile_ExitCode2 drives the real binary with a missing
// password-file path and asserts OS exit code 2.
func TestBinary_MissingPasswordFile_ExitCode2(t *testing.T) {
	bin := getTestBinary(t)

	keystore := writeTestFile(t, []byte("{}"), 0o600)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	cmd := exec.CommandContext(ctx, bin,
		"--keystore", keystore,
		"--password-file", filepath.Join(t.TempDir(), "no-such-password"),
	)
	err := cmd.Run()
	code := exitCodeFromError(t, err)
	if code != 2 {
		t.Errorf("exit code = %d, want 2 (missing password-file path)", code)
	}
}

// TestBinary_DirectoryAsKeystore_ExitCode2 drives the real binary with a directory
// supplied as the --keystore path and asserts OS exit code 2.
func TestBinary_DirectoryAsKeystore_ExitCode2(t *testing.T) {
	bin := getTestBinary(t)

	password := writeTestFile(t, []byte("pass"), 0o600)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	cmd := exec.CommandContext(ctx, bin,
		"--keystore", t.TempDir(), // directory, not a regular file
		"--password-file", password,
	)
	err := cmd.Run()
	code := exitCodeFromError(t, err)
	if code != 2 {
		t.Errorf("exit code = %d, want 2 (directory as keystore)", code)
	}
}

// TestBinary_Mode600_ExitCode0 verifies that valid 0600 files allow the binary to
// proceed (exit 0 since Phase 1 has no server yet — run() returns nil after fsperm).
func TestBinary_Mode600_ExitCode0(t *testing.T) {
	skipIfRoot(t)

	bin := getTestBinary(t)

	keystore := writeTestFile(t, []byte("{}"), 0o600)
	password := writeTestFile(t, []byte("pass"), 0o600)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	cmd := exec.CommandContext(ctx, bin,
		"--keystore", keystore,
		"--password-file", password,
	)
	err := cmd.Run()
	code := exitCodeFromError(t, err)
	if code != 0 {
		t.Errorf("exit code = %d, want 0 (mode 0600, no --strict-perms)", code)
	}
}

// TestBinary_Mode644_NoStrictPerms_ExitCode0 verifies that a too-open file without
// --strict-perms only warns and still exits 0.
func TestBinary_Mode644_NoStrictPerms_ExitCode0(t *testing.T) {
	skipIfRoot(t)

	bin := getTestBinary(t)

	keystore := writeTestFile(t, []byte("{}"), 0o644)
	password := writeTestFile(t, []byte("pass"), 0o600)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	cmd := exec.CommandContext(ctx, bin,
		"--keystore", keystore,
		"--password-file", password,
	)
	err := cmd.Run()
	code := exitCodeFromError(t, err)
	if code != 0 {
		t.Errorf("exit code = %d, want 0 (mode 0644, no --strict-perms should warn not refuse)", code)
	}
}
