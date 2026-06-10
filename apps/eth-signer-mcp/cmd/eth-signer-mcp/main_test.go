//go:build !windows

package main

// main_test.go — binary-level smoke tests for eth-signer-mcp (issue 1.8).
//
// These tests build and drive the real eth-signer-mcp binary as a child process.
// They exercise:
//   (a) initialize + tools/list over child-process stdio (real MCP session).
//   (b) --http with token file exits non-zero with "Phase 3" in stderr.
//   (c) SIGINT during an idle stdio session causes clean exit (exit code 0).
//
// Prerequisites:
//   - getTestBinary() is defined in fsperm_test.go (same package, !windows build tag).
//   - tempFiles600() is defined in config_test.go.
//
// All tests bound with ≤10s timeout contexts per the issue acceptance criteria.

import (
	"context"
	"errors"
	"io"
	"os"
	"os/exec"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// TestBinary_Stdio_Initialize drives a real MCP initialize + tools/list session
// through the child process's stdin/stdout.
//
// Acceptance criteria:
//   - initialize round-trip completes: server advertises Name == "eth-signer-mcp"
//     and a non-empty Version (Version is "<unknown>" under go build from source
//     without VCS stamps; we assert non-empty, not a concrete value).
//   - tools/list returns an empty list (no tools registered in Phase 1).
//   - Closing the client session (which closes child stdin) causes exit code 0.
func TestBinary_Stdio_Initialize(t *testing.T) {
	bin := getTestBinary(t)
	ks, pw := tempFiles600(t)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	// Use mcp.CommandTransport: handles pipe creation, process start, and
	// shutdown (closes stdin, waits for the process).
	cmd := exec.CommandContext(ctx, bin, "--keystore", ks, "--password-file", pw)

	client := mcp.NewClient(
		&mcp.Implementation{Name: "test-client", Version: "v0.0.1"},
		nil,
	)
	transport := &mcp.CommandTransport{Command: cmd}

	// client.Connect starts the binary and performs the MCP initialize handshake.
	cs, err := client.Connect(ctx, transport, nil)
	if err != nil {
		t.Fatalf("client.Connect: %v", err)
	}

	// (a) Assert initialize round-trip.
	result := cs.InitializeResult()
	if result == nil {
		t.Fatal("InitializeResult() is nil")
	}
	if result.ServerInfo == nil {
		t.Fatal("InitializeResult.ServerInfo is nil")
	}
	if got := result.ServerInfo.Name; got != "eth-signer-mcp" {
		t.Errorf("ServerInfo.Name = %q; want %q", got, "eth-signer-mcp")
	}
	// Version is not a stable value: it may be "<unknown>" (go build without VCS),
	// a semver (release build), or a pseudo-version.  Assert non-empty.
	if result.ServerInfo.Version == "" {
		t.Error("ServerInfo.Version is empty; want non-empty")
	}

	// (b) Assert tools/list returns empty.
	toolsResult, err := cs.ListTools(ctx, nil)
	if err != nil {
		t.Fatalf("cs.ListTools: %v", err)
	}
	if toolsResult == nil {
		t.Fatal("ListTools result is nil")
	}
	if len(toolsResult.Tools) != 0 {
		t.Errorf("len(Tools) = %d; want 0 (no tools in Phase 1)", len(toolsResult.Tools))
	}

	// (c) Close client: CommandTransport closes child stdin, server gets EOF,
	// RunStdio returns nil, binary exits 0.
	// cs.Close() internally calls pipeRWC.Close() which waits for the process.
	if err := cs.Close(); err != nil {
		// Accept "closed" errors: if the server already exited cleanly, the pipe
		// may be closed from the server side first.  We care about exit code, not
		// whether stdin.Close() returns an error.
		if !isClosedPipeError(err) {
			t.Errorf("cs.Close: %v", err)
		}
	}
}

// TestBinary_HTTPFlag_Phase3Error verifies that --http (with a token file, so
// validate() passes) exits non-zero and prints a stable "Phase 3" message.
//
// The Streamable HTTP transport arrives in Phase 3; it is NEVER called "SSE"
// (Phase Conventions).  This test pins the stable error substring so callers
// can rely on it.
func TestBinary_HTTPFlag_Phase3Error(t *testing.T) {
	bin := getTestBinary(t)
	ks, pw := tempFiles600(t)

	// Write a dummy token file (only its existence matters; no validation yet).
	dir := t.TempDir()
	tokenPath := dir + "/token.txt"
	if err := os.WriteFile(tokenPath, []byte("dummy-token"), 0o600); err != nil {
		t.Fatalf("write token file: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	cmd := exec.CommandContext(ctx, bin,
		"--keystore", ks,
		"--password-file", pw,
		"--http",
		"--http-auth-token-file", tokenPath,
	)
	var stderr strings.Builder
	cmd.Stderr = &stderr

	err := cmd.Run()

	// Must exit non-zero.
	if err == nil {
		t.Fatal("binary exited 0; want non-zero (--http not yet implemented)")
	}
	var exitErr *exec.ExitError
	if !errors.As(err, &exitErr) {
		t.Fatalf("unexpected error type: %T %v", err, err)
	}
	if exitErr.ExitCode() == 0 {
		t.Error("exit code = 0; want non-zero for --http in Phase 1")
	}

	// Must mention "Phase 3" in stderr (stable substring per issue 1.8 spec).
	// NEVER assert "SSE" — the transport is MCP Streamable HTTP (Phase Conventions).
	stderrStr := stderr.String()
	if !strings.Contains(stderrStr, "Phase 3") {
		t.Errorf("stderr does not contain %q\nstderr: %s", "Phase 3", stderrStr)
	}
}

// TestBinary_Stdio_SIGINTCleanExit verifies that SIGINT during an idle stdio
// session causes clean exit (exit code 0).
//
// Flow:
//  1. Start binary with keystore/password; connect client (initialize).
//  2. Send SIGINT to the child process.
//  3. Wait for the process to exit (≤5 s within the 10 s test budget).
//  4. Assert exit code == 0.
//
// SIGINT → signal.NotifyContext cancels ctx → RunStdio returns nil → exit 0.
// This verifies that RunStdio normalises context.Canceled to nil (see stdio.go).
func TestBinary_Stdio_SIGINTCleanExit(t *testing.T) {
	bin := getTestBinary(t)
	ks, pw := tempFiles600(t)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	// Set up stdin/stdout pipes manually so we control the process lifecycle.
	// We need access to cmd.Process for SIGINT, which CommandTransport obscures.
	cmd := exec.CommandContext(ctx, bin, "--keystore", ks, "--password-file", pw)
	stdinPipe, err := cmd.StdinPipe()
	if err != nil {
		t.Fatalf("StdinPipe: %v", err)
	}
	stdoutPipe, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatalf("StdoutPipe: %v", err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatalf("cmd.Start: %v", err)
	}
	t.Cleanup(func() {
		// Belt-and-suspenders: kill if the test itself fails/panics.
		if cmd.Process != nil {
			_ = cmd.Process.Kill()
		}
	})

	// Connect MCP client via the pipes.  IOTransport.Reader = server's stdout
	// (client reads), IOTransport.Writer = server's stdin (client writes).
	client := mcp.NewClient(
		&mcp.Implementation{Name: "test-client-sigint", Version: "v0.0.1"},
		nil,
	)
	cs, err := client.Connect(ctx, &mcp.IOTransport{
		Reader: stdoutPipe,
		Writer: stdinPipe,
	}, nil)
	if err != nil {
		t.Fatalf("client.Connect (SIGINT test): %v", err)
	}
	t.Cleanup(func() { _ = cs.Close() })

	// Verify initialize completed (server is running and idle).
	if result := cs.InitializeResult(); result == nil || result.ServerInfo == nil {
		t.Fatal("initialize did not complete before SIGINT")
	}

	// Send SIGINT.  The binary's signal.NotifyContext handles it, cancels ctx,
	// RunStdio returns nil, and the binary exits 0.
	if err := cmd.Process.Signal(syscall.SIGINT); err != nil {
		t.Fatalf("Signal(SIGINT): %v", err)
	}

	// Wait for the process to exit.  Use a goroutine + channel so we can apply
	// a timeout without wedging if the signal is not handled.
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()

	select {
	case <-time.After(5 * time.Second):
		t.Fatal("binary did not exit within 5s after SIGINT")
	case waitErr := <-done:
		// exit 0: cmd.Wait() returns nil.
		// exit non-zero / signal-killed: cmd.Wait() returns *exec.ExitError.
		if waitErr != nil {
			var exitErr *exec.ExitError
			if errors.As(waitErr, &exitErr) {
				if exitErr.ExitCode() != 0 {
					t.Errorf("exit code = %d after SIGINT; want 0 (clean shutdown)", exitErr.ExitCode())
				}
				// ExitCode == 0 on some platforms even when Wait returns non-nil;
				// this is fine — clean exit.
			} else {
				t.Errorf("cmd.Wait: unexpected error type %T: %v", waitErr, waitErr)
			}
		}
		// waitErr == nil means exit 0. ✓
	}
}

// isClosedPipeError reports whether err is an expected "pipe closed / already
// closed" error that can arise when the server closes its stdin side before the
// client does.  We don't treat these as test failures.
func isClosedPipeError(err error) bool {
	if err == nil {
		return false
	}
	// Prefer typed checks; fall back to specific substrings. A bare "closed"
	// substring was too broad (it would swallow unrelated errors that merely
	// contain the word), so match the concrete phrasings the os/io layers use.
	if errors.Is(err, io.EOF) || errors.Is(err, io.ErrClosedPipe) {
		return true
	}
	msg := err.Error()
	return strings.Contains(msg, "file already closed") ||
		strings.Contains(msg, "use of closed file") ||
		strings.Contains(msg, "closed pipe") ||
		strings.Contains(msg, "broken pipe") ||
		strings.Contains(msg, "EOF")
}
