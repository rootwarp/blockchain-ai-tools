//go:build !windows

package main

// main_test.go — binary-level smoke tests for eth-signer-mcp (issues 1.8, 3.1).
//
// These tests build and drive the real eth-signer-mcp binary as a child process.
// They exercise:
//   (a) initialize + tools/list over child-process stdio (real MCP session).
//   (b) --http with token file starts the Streamable HTTP server (Phase 3, issue 3.1)
//       and exits 0 on SIGTERM.  The transport is NEVER called "HTTP/SSE".
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
	ks, pw := signingFixtureFiles(t) // real keystore so NewFileKeyVault succeeds

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

	// (b) Assert tools/list returns exactly 2 tools (sign_transaction, get_address).
	toolsResult, err := cs.ListTools(ctx, nil)
	if err != nil {
		t.Fatalf("cs.ListTools: %v", err)
	}
	if toolsResult == nil {
		t.Fatal("ListTools result is nil")
	}
	if len(toolsResult.Tools) != 2 {
		names := make([]string, len(toolsResult.Tools))
		for i, tt := range toolsResult.Tools {
			names[i] = tt.Name
		}
		t.Errorf("len(Tools) = %d; want 2 (sign_transaction, get_address). Got: %v",
			len(toolsResult.Tools), names)
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

// TestBinary_HTTP_StartsAndShutsDown verifies that --http starts the Streamable
// HTTP server, prints the announce line to stderr, and exits 0 on SIGTERM.
//
// Phase 3 (issue 3.1): the transport is now real.  The Phase 1 placeholder
// ("Phase 3" error message) is replaced by a proper start+shutdown test.
//
// Flow:
//  1. Start binary with --http, keystore, password-file, and a token file.
//  2. Read stderr until the "eth-signer-mcp listening on" announce line appears.
//  3. Send SIGTERM to the child process.
//  4. Assert exit code == 0 (clean shutdown via signal.NotifyContext).
//
// NEVER refer to this transport as "HTTP/SSE" — it is MCP Streamable HTTP
// (Phase Conventions).
func TestBinary_HTTP_StartsAndShutsDown(t *testing.T) {
	bin := getTestBinary(t)
	ks, pw := signingFixtureFiles(t)

	dir := t.TempDir()
	tokenPath := dir + "/token.txt"
	if err := os.WriteFile(tokenPath, []byte("test-http-startup-token\n"), 0o600); err != nil {
		t.Fatalf("write token file: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	cmd := exec.CommandContext(ctx, bin,
		"--keystore", ks,
		"--password-file", pw,
		"--http",
		"--http-auth-token-file", tokenPath,
	)
	stderrPipe, err := cmd.StderrPipe()
	if err != nil {
		t.Fatalf("StderrPipe: %v", err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatalf("cmd.Start: %v", err)
	}
	t.Cleanup(func() {
		if cmd.Process != nil {
			_ = cmd.Process.Kill()
		}
	})

	// Read stderr byte-by-byte until we find the announce line or timeout.
	announceFound := make(chan string, 1)
	go func() {
		buf := make([]byte, 0, 512)
		tmp := make([]byte, 1)
		for {
			n, readErr := stderrPipe.Read(tmp)
			if n > 0 {
				buf = append(buf, tmp[:n]...)
				// Check each line as it arrives.
				for {
					idx := strings.IndexByte(string(buf), '\n')
					if idx < 0 {
						break
					}
					line := string(buf[:idx])
					buf = buf[idx+1:]
					if strings.Contains(line, "listening on") {
						announceFound <- line
						return
					}
				}
			}
			if readErr != nil {
				return
			}
		}
	}()

	var announceLine string
	select {
	case announceLine = <-announceFound:
		// Server announced its bound address.
	case <-time.After(10 * time.Second):
		t.Fatal("timeout waiting for announce line in stderr")
	}

	// Verify the announce line contains the expected prefix.
	if !strings.Contains(announceLine, "eth-signer-mcp listening on") {
		t.Errorf("announce line = %q; want to contain %q", announceLine, "eth-signer-mcp listening on")
	}

	// Send SIGTERM → signal.NotifyContext cancels → RunHTTP returns nil → exit 0.
	if err := cmd.Process.Signal(syscall.SIGTERM); err != nil {
		t.Fatalf("Signal(SIGTERM): %v", err)
	}

	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()

	select {
	case err := <-done:
		if err != nil {
			var exitErr *exec.ExitError
			if errors.As(err, &exitErr) {
				t.Errorf("exit code = %d; want 0 (clean shutdown)", exitErr.ExitCode())
			} else {
				t.Errorf("cmd.Wait: %v", err)
			}
		}
	case <-time.After(8 * time.Second):
		t.Fatal("binary did not exit within 8s after SIGTERM")
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
	ks, pw := signingFixtureFiles(t) // real keystore so NewFileKeyVault succeeds

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

// TestBinary_NoAddressKeystore_ExitNonZero verifies that starting the binary with
// a keystore that has no usable "address" field causes non-zero exit and a clear
// keystore_error message on stderr (issue 2.7 cmd wiring smoke test).
func TestBinary_NoAddressKeystore_ExitNonZero(t *testing.T) {
	bin := getTestBinary(t)
	_, pw := signingFixtureFiles(t)
	ks := noAddressKeystoreFile(t)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	var stderr strings.Builder
	cmd := exec.CommandContext(ctx, bin, "--keystore", ks, "--password-file", pw)
	cmd.Stderr = &stderr

	err := cmd.Run()

	// Must exit non-zero.
	if err == nil {
		t.Fatal("binary exited 0; want non-zero exit for no-address keystore")
	}
	var exitErr *exec.ExitError
	if !errors.As(err, &exitErr) {
		t.Fatalf("unexpected error type %T: %v", err, err)
	}
	if exitErr.ExitCode() == 0 {
		t.Error("exit code = 0; want non-zero for no-address keystore")
	}

	// stderr must contain "keystore" to identify the error category.
	stderrStr := stderr.String()
	if !strings.Contains(strings.ToLower(stderrStr), "keystore") {
		t.Errorf("stderr does not contain 'keystore'\nstderr: %s", stderrStr)
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
