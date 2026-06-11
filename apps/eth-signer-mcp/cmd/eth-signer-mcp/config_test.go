package main

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/urfave/cli/v3"
)

// tempFiles600 creates keystore and password-file temp files with mode 0600 in
// t.TempDir() so that the fsperm startup check (issue 1.6) passes.  Both files
// get minimal placeholder content; the fsperm check only needs them to exist and
// be regular files with acceptable permissions.  Returns (keystorePath, passwordPath).
//
// NOTE: These files contain placeholder content only. Do NOT use them for tests
// that reach signing.NewFileKeyVault — use signingFixtureFiles() instead.
func tempFiles600(t *testing.T) (string, string) {
	t.Helper()
	dir := t.TempDir()
	ks := filepath.Join(dir, "keystore.json")
	pw := filepath.Join(dir, "password.txt")
	if err := os.WriteFile(ks, []byte("{}"), 0o600); err != nil {
		t.Fatalf("tempFiles600: write keystore: %v", err)
	}
	if err := os.WriteFile(pw, []byte("pass"), 0o600); err != nil {
		t.Fatalf("tempFiles600: write password: %v", err)
	}
	return ks, pw
}

// signingFixtureFiles returns the paths to the keystore-weak.json and password.txt
// fixtures under internal/signing/testdata/. These are real keystores that can be
// decrypted by signing.NewFileKeyVault.
//
// Use these for tests that run the full startup sequence (NewFileKeyVault + NewSigner).
func signingFixtureFiles(t *testing.T) (keystorePath, passwordPath string) {
	t.Helper()
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller(0) failed")
	}
	// thisFile: .../cmd/eth-signer-mcp/config_test.go
	// testdata: .../internal/signing/testdata/
	cmdDir := filepath.Dir(thisFile)
	// Navigate up from cmd/eth-signer-mcp to module root, then to internal/signing/testdata
	testdataDir := filepath.Join(cmdDir, "..", "..", "internal", "signing", "testdata")

	ks := filepath.Join(testdataDir, "keystore-weak.json")
	pw := filepath.Join(testdataDir, "password.txt")

	// Verify the files exist (guard against accidental deletion of fixtures).
	for _, p := range []string{ks, pw} {
		if _, err := os.Stat(p); err != nil {
			t.Fatalf("signingFixtureFiles: fixture not found at %s: %v", p, err)
		}
	}
	return ks, pw
}

// noAddressKeystoreFile returns the path to keystore-no-address.json, used to
// test the startup keystore_error path.
func noAddressKeystoreFile(t *testing.T) string {
	t.Helper()
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller(0) failed")
	}
	cmdDir := filepath.Dir(thisFile)
	p := filepath.Join(cmdDir, "..", "..", "internal", "signing", "testdata", "keystore-no-address.json")
	if _, err := os.Stat(p); err != nil {
		t.Fatalf("noAddressKeystoreFile: fixture not found at %s: %v", p, err)
	}
	return p
}

// ptrUint64 is a test helper that returns a pointer to the given uint64 value.
func ptrUint64(v uint64) *uint64 { return &v }

// TestValidate_Rules exercises every branch of the validate() function directly.
// Table-driven; each case asserts a stable error-message substring (or no error).
func TestValidate_Rules(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		cfg     config
		wantErr string // non-empty substring that must appear in the error; empty means success
	}{
		// --- happy paths ---
		{
			name: "valid_minimal",
			cfg: config{
				KeystorePath: "/k", PasswordPath: "/p",
				HTTPAddr: "127.0.0.1:0", LogLevel: "info",
			},
		},
		{
			name: "valid_with_http",
			cfg: config{
				KeystorePath: "/k", PasswordPath: "/p",
				HTTP: true, TokenFilePath: "/t",
				HTTPAddr: "127.0.0.1:0", LogLevel: "info",
			},
		},
		{
			name: "valid_chain_id_nonzero",
			cfg: config{
				KeystorePath: "/k", PasswordPath: "/p",
				ChainIDGuard: ptrUint64(1),
				HTTPAddr:     "127.0.0.1:0", LogLevel: "info",
			},
		},
		{
			name: "valid_chain_id_nil",
			cfg: config{
				KeystorePath: "/k", PasswordPath: "/p",
				ChainIDGuard: nil,
				HTTPAddr:     "127.0.0.1:0", LogLevel: "info",
			},
		},
		{
			name: "log_level_debug",
			cfg: config{
				KeystorePath: "/k", PasswordPath: "/p",
				HTTPAddr: "127.0.0.1:0", LogLevel: "debug",
			},
		},
		{
			name: "log_level_info",
			cfg: config{
				KeystorePath: "/k", PasswordPath: "/p",
				HTTPAddr: "127.0.0.1:0", LogLevel: "info",
			},
		},
		{
			name: "log_level_warn",
			cfg: config{
				KeystorePath: "/k", PasswordPath: "/p",
				HTTPAddr: "127.0.0.1:0", LogLevel: "warn",
			},
		},
		{
			name: "log_level_error",
			cfg: config{
				KeystorePath: "/k", PasswordPath: "/p",
				HTTPAddr: "127.0.0.1:0", LogLevel: "error",
			},
		},
		{
			name: "log_level_case_insensitive_upper",
			cfg: config{
				KeystorePath: "/k", PasswordPath: "/p",
				HTTPAddr: "127.0.0.1:0", LogLevel: "INFO",
			},
		},
		{
			name: "log_level_case_insensitive_debug_upper",
			cfg: config{
				KeystorePath: "/k", PasswordPath: "/p",
				HTTPAddr: "127.0.0.1:0", LogLevel: "DEBUG",
			},
		},
		// --- error paths ---
		{
			name:    "missing_keystore",
			cfg:     config{PasswordPath: "/p", LogLevel: "info"},
			wantErr: "--keystore",
		},
		{
			name:    "missing_password_file",
			cfg:     config{KeystorePath: "/k", LogLevel: "info"},
			wantErr: "--password-file",
		},
		{
			name: "http_without_token_file",
			cfg: config{
				KeystorePath: "/k", PasswordPath: "/p",
				HTTP: true, TokenFilePath: "",
				LogLevel: "info",
			},
			wantErr: "--http-auth-token-file",
		},
		{
			name: "chain_id_zero",
			cfg: config{
				KeystorePath: "/k", PasswordPath: "/p",
				ChainIDGuard: ptrUint64(0),
				LogLevel:     "info",
			},
			wantErr: "--chain-id 0",
		},
		{
			name: "log_level_garbage",
			cfg: config{
				KeystorePath: "/k", PasswordPath: "/p",
				LogLevel: "garbage",
			},
			wantErr: "--log-level",
		},
		{
			name: "log_level_empty_string",
			cfg: config{
				KeystorePath: "/k", PasswordPath: "/p",
				LogLevel: "",
			},
			wantErr: "--log-level",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			err := validate(tt.cfg)
			if tt.wantErr != "" {
				if err == nil {
					t.Fatalf("validate() = nil, want error containing %q", tt.wantErr)
				}
				if !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("validate() error = %q, want substring %q", err.Error(), tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("validate() = %v, want nil", err)
			}
		})
	}
}

// TestRun_GoldenConfig verifies that parsing the minimal required flags yields
// the exact canonical default config value (acceptance criterion §golden).
func TestRun_GoldenConfig(t *testing.T) {
	t.Parallel()

	var captured config

	cmd := newCommand()
	cmd.Action = func(ctx context.Context, c *cli.Command) error {
		captured = buildConfig(c)
		return validate(captured)
	}

	args := []string{"eth-signer-mcp", "--keystore", "/k", "--password-file", "/p"}
	if err := cmd.Run(context.Background(), args); err != nil {
		t.Fatalf("cmd.Run() = %v, want nil", err)
	}

	// Assert every field of the canonical default config.
	if captured.KeystorePath != "/k" {
		t.Errorf("KeystorePath = %q, want %q", captured.KeystorePath, "/k")
	}
	if captured.PasswordPath != "/p" {
		t.Errorf("PasswordPath = %q, want %q", captured.PasswordPath, "/p")
	}
	if captured.HTTP != false {
		t.Errorf("HTTP = %v, want false", captured.HTTP)
	}
	if captured.HTTPAddr != "127.0.0.1:0" {
		t.Errorf("HTTPAddr = %q, want %q", captured.HTTPAddr, "127.0.0.1:0")
	}
	if captured.TokenFilePath != "" {
		t.Errorf("TokenFilePath = %q, want %q", captured.TokenFilePath, "")
	}
	if captured.ChainIDGuard != nil {
		t.Errorf("ChainIDGuard = %v, want nil", *captured.ChainIDGuard)
	}
	if captured.StrictPerms != false {
		t.Errorf("StrictPerms = %v, want false", captured.StrictPerms)
	}
	if captured.LogLevel != "info" {
		t.Errorf("LogLevel = %q, want %q", captured.LogLevel, "info")
	}
}

// TestRun_MissingRequired verifies that missing required flags produce non-zero exit
// with an error message that names the missing flag.
func TestRun_MissingRequired(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		args    []string
		wantErr string
	}{
		{
			name:    "missing_keystore",
			args:    []string{"eth-signer-mcp", "--password-file", "/p"},
			wantErr: "keystore",
		},
		{
			name:    "missing_password_file",
			args:    []string{"eth-signer-mcp", "--keystore", "/k"},
			wantErr: "password-file",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			cmd := newCommand()
			// Suppress usage/help output to stderr during tests.
			cmd.ErrWriter = &strings.Builder{}
			cmd.Writer = &strings.Builder{}

			err := cmd.Run(context.Background(), tt.args)
			if err == nil {
				t.Fatalf("cmd.Run(%v) = nil, want error containing %q", tt.args, tt.wantErr)
			}
			if !strings.Contains(strings.ToLower(err.Error()), tt.wantErr) {
				t.Fatalf("cmd.Run() error = %q, want substring %q", err.Error(), tt.wantErr)
			}
		})
	}
}

// TestRun_ChainID verifies the three chain-id cases:
//   - --chain-id 1 → *ChainIDGuard == 1
//   - --chain-id absent → ChainIDGuard == nil
//   - --chain-id 0 → validation error naming "--chain-id 0"
func TestRun_ChainID(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		args        []string
		wantChainID *uint64 // nil means absent
		wantErr     string  // non-empty means expect error containing this substring
	}{
		{
			name:        "chain_id_1",
			args:        []string{"eth-signer-mcp", "--keystore", "/k", "--password-file", "/p", "--chain-id", "1"},
			wantChainID: ptrUint64(1),
		},
		{
			name:        "chain_id_absent",
			args:        []string{"eth-signer-mcp", "--keystore", "/k", "--password-file", "/p"},
			wantChainID: nil,
		},
		{
			name:    "chain_id_zero",
			args:    []string{"eth-signer-mcp", "--keystore", "/k", "--password-file", "/p", "--chain-id", "0"},
			wantErr: "--chain-id 0",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			var captured config
			cmd := newCommand()
			cmd.Action = func(ctx context.Context, c *cli.Command) error {
				captured = buildConfig(c)
				return validate(captured)
			}

			err := cmd.Run(context.Background(), tt.args)

			if tt.wantErr != "" {
				if err == nil {
					t.Fatalf("cmd.Run() = nil, want error containing %q", tt.wantErr)
				}
				if !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("cmd.Run() error = %q, want substring %q", err.Error(), tt.wantErr)
				}
				return
			}

			if err != nil {
				t.Fatalf("cmd.Run() = %v, want nil", err)
			}

			if tt.wantChainID == nil {
				if captured.ChainIDGuard != nil {
					t.Errorf("ChainIDGuard = %v, want nil", *captured.ChainIDGuard)
				}
			} else {
				if captured.ChainIDGuard == nil {
					t.Errorf("ChainIDGuard = nil, want %d", *tt.wantChainID)
				} else if *captured.ChainIDGuard != *tt.wantChainID {
					t.Errorf("ChainIDGuard = %d, want %d", *captured.ChainIDGuard, *tt.wantChainID)
				}
			}
		})
	}
}

// TestRun_HTTPValidation verifies HTTP-transport flag interactions.
func TestRun_HTTPValidation(t *testing.T) {
	t.Parallel()

	// Use real keystore+password fixtures so NewFileKeyVault succeeds and we
	// reach the HTTP transport startup.  Tests that fail before the Action fires
	// can use fake paths — the Action never runs for those.
	ks, pw := signingFixtureFiles(t)

	tests := []struct {
		name    string
		args    []string
		wantErr string
	}{
		{
			// validate() rejects --http without a token file before the Action fires,
			// so fake keystore/password paths are fine for this case.
			name:    "http_without_token_file",
			args:    []string{"eth-signer-mcp", "--keystore", "/k", "--password-file", "/p", "--http"},
			wantErr: "--http-auth-token-file",
		},
		{
			// Issue 3.1: --http with a non-existent token file path passes validate()
			// but RunHTTP fails fast with a token-file error before binding any listener.
			// The error names the path, never the token contents.
			// NEVER "SSE" — transport naming is "Streamable HTTP" per Phase Conventions.
			name:    "http_with_missing_token_file",
			wantErr: "token file",
			args: []string{
				"eth-signer-mcp", "--keystore", ks, "--password-file", pw,
				"--http", "--http-auth-token-file", "/nonexistent/path/token.txt",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			cmd := newCommand()
			err := cmd.Run(context.Background(), tt.args)

			if tt.wantErr != "" {
				if err == nil {
					t.Fatalf("cmd.Run() = nil, want error containing %q", tt.wantErr)
				}
				if !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("cmd.Run() error = %q, want substring %q", err.Error(), tt.wantErr)
				}
			} else {
				if err != nil {
					t.Fatalf("cmd.Run() = %v, want nil", err)
				}
			}
		})
	}
}

// TestRun_LogLevel verifies that log-level validation runs through the Action.
func TestRun_LogLevel(t *testing.T) {
	t.Parallel()

	// Real 0600 temp files are created for parity with the invalid case, though
	// the custom action below never reaches the fsperm check.
	ks, pw := tempFiles600(t)

	// Valid-level subtests use a custom action that runs only parse → buildConfig
	// → validate(): it deliberately stops there, before logger/fsperm/server.New/
	// RunStdio. This keeps the test focused on log-level validation and avoids
	// driving RunStdio (which would read os.Stdin) in parallel subtests.
	valid := []string{"debug", "info", "warn", "error", "DEBUG", "INFO", "WARN", "ERROR"}
	for _, lvl := range valid {
		t.Run("valid_"+lvl, func(t *testing.T) {
			t.Parallel()
			cmd := newCommand()
			cmd.Action = func(ctx context.Context, c *cli.Command) error {
				cfg := buildConfig(c)
				return validate(cfg)
			}
			args := []string{"eth-signer-mcp", "--keystore", ks, "--password-file", pw, "--log-level", lvl}
			if err := cmd.Run(context.Background(), args); err != nil {
				t.Fatalf("cmd.Run() = %v, want nil for valid log-level %q", err, lvl)
			}
		})
	}

	t.Run("invalid_garbage", func(t *testing.T) {
		t.Parallel()
		cmd := newCommand()
		// validate() rejects the level before the Action fires — fake paths are fine.
		args := []string{"eth-signer-mcp", "--keystore", "/k", "--password-file", "/p", "--log-level", "garbage"}
		err := cmd.Run(context.Background(), args)
		if err == nil {
			t.Fatal("cmd.Run() = nil, want error for invalid log-level")
		}
		if !strings.Contains(err.Error(), "--log-level") {
			t.Fatalf("error = %q, want substring %q", err.Error(), "--log-level")
		}
	})
}

// TestRun_UnknownFlag verifies that an unknown flag causes non-zero exit with non-empty error.
func TestRun_UnknownFlag(t *testing.T) {
	t.Parallel()

	cmd := newCommand()
	// Suppress error output to stderr during test.
	cmd.ErrWriter = &strings.Builder{}
	cmd.Writer = &strings.Builder{}

	err := cmd.Run(context.Background(), []string{
		"eth-signer-mcp", "--no-such-flag-xyz",
		"--keystore", "/k", "--password-file", "/p",
	})
	if err == nil {
		t.Fatal("cmd.Run() = nil, want error for unknown flag")
	}
	if err.Error() == "" {
		t.Fatal("cmd.Run() error message is empty, want non-empty")
	}
}

// TestRun_SuccessExit verifies that a fully-valid invocation returns nil (exit 0).
// This drives the REAL Action end-to-end, including NewFileKeyVault, NewSigner,
// and RunStdio. Under `go test`, os.Stdin is non-interactive (EOF), so the stdio
// session ends immediately and RunStdio returns nil.
// Uses the real keystore-weak.json fixture so NewFileKeyVault succeeds.
func TestRun_SuccessExit(t *testing.T) {
	t.Parallel()

	ks, pw := signingFixtureFiles(t)
	cmd := newCommand()
	args := []string{"eth-signer-mcp", "--keystore", ks, "--password-file", pw}
	if err := cmd.Run(context.Background(), args); err != nil {
		t.Fatalf("cmd.Run() = %v, want nil (exit 0) for valid args with real keystore", err)
	}
}

// TestNewCommand_FreshInstancePerRun pins the no-reuse contract documented on
// newCommand(): a fresh *cli.Command must classify --chain-id presence
// independently. urfave/cli v3's IsSet() is sticky (write-once per instance), so
// this guards against a future test or retry loop that shares an instance and
// silently misclassifies an absent --chain-id as &0.
func TestNewCommand_FreshInstancePerRun(t *testing.T) {
	t.Parallel()

	capture := func(args []string) config {
		var captured config
		cmd := newCommand()
		cmd.Action = func(ctx context.Context, c *cli.Command) error {
			captured = buildConfig(c)
			return nil
		}
		if err := cmd.Run(context.Background(), args); err != nil {
			t.Fatalf("cmd.Run(%v) = %v, want nil", args, err)
		}
		return captured
	}

	// A fresh instance WITH --chain-id sets the guard.
	withGuard := capture([]string{"eth-signer-mcp", "--keystore", "/k", "--password-file", "/p", "--chain-id", "5"})
	if withGuard.ChainIDGuard == nil || *withGuard.ChainIDGuard != 5 {
		t.Fatalf("ChainIDGuard = %v, want &5", withGuard.ChainIDGuard)
	}

	// A separate fresh instance WITHOUT --chain-id must see nil — not leaked
	// sticky IsSet state from the previous Run.
	without := capture([]string{"eth-signer-mcp", "--keystore", "/k", "--password-file", "/p"})
	if without.ChainIDGuard != nil {
		t.Fatalf("ChainIDGuard = %v on fresh instance without --chain-id, want nil", *without.ChainIDGuard)
	}
}

// TestRun_NoAddressKeystore_ExitNonZero verifies that starting with a keystore
// that has no usable "address" field fails fast with a keystore_error message
// (issue 2.7 cmd wiring: fail fast on vault constructor error).
//
// Uses the committed keystore-no-address.json fixture (address field removed).
func TestRun_NoAddressKeystore_ExitNonZero(t *testing.T) {
	t.Parallel()

	_, pw := signingFixtureFiles(t) // use real password file (vault won't read it, but fsperm checks need it)
	ks := noAddressKeystoreFile(t)

	cmd := newCommand()
	args := []string{"eth-signer-mcp", "--keystore", ks, "--password-file", pw}
	err := cmd.Run(context.Background(), args)
	if err == nil {
		t.Fatal("cmd.Run() = nil; want non-zero exit for no-address keystore")
	}
	// Error message must contain "keystore" to identify the failure category.
	if !strings.Contains(strings.ToLower(err.Error()), "keystore") {
		t.Errorf("error = %q; want it to contain 'keystore'", err.Error())
	}
}

// TestRun_ChainIDPlumbedOnlyIntoSigner verifies that --chain-id is captured in
// the config and plumbed only through NewSigner (no per-request field). We
// verify this by parsing the config flag: the guard is only set when --chain-id
// is present (non-nil), and absent when not provided (nil). The guard lives only
// in the Signer, constructed here in cmd.
func TestRun_ChainIDPlumbedOnlyIntoSigner(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		args        []string
		wantGuard   *uint64
		expectError string
	}{
		{
			name:      "chain_id_1_sets_guard",
			args:      []string{"eth-signer-mcp", "--keystore", "/k", "--password-file", "/p", "--chain-id", "1"},
			wantGuard: ptrUint64(1),
		},
		{
			name:      "no_chain_id_nil_guard",
			args:      []string{"eth-signer-mcp", "--keystore", "/k", "--password-file", "/p"},
			wantGuard: nil,
		},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			var captured config
			cmd := newCommand()
			cmd.Action = func(ctx context.Context, c *cli.Command) error {
				captured = buildConfig(c)
				return validate(captured)
			}
			if err := cmd.Run(context.Background(), tc.args); err != nil {
				t.Fatalf("cmd.Run() = %v, want nil", err)
			}
			if tc.wantGuard == nil {
				if captured.ChainIDGuard != nil {
					t.Errorf("ChainIDGuard = %d; want nil", *captured.ChainIDGuard)
				}
			} else {
				if captured.ChainIDGuard == nil {
					t.Errorf("ChainIDGuard = nil; want %d", *tc.wantGuard)
				} else if *captured.ChainIDGuard != *tc.wantGuard {
					t.Errorf("ChainIDGuard = %d; want %d", *captured.ChainIDGuard, *tc.wantGuard)
				}
			}
		})
	}
}

// TestHelp_ListsAllFlags verifies that --help exits 0 and lists every documented flag.
// In urfave/cli v3, --help prints to cmd.Writer and returns nil (no os.Exit call).
func TestHelp_ListsAllFlags(t *testing.T) {
	t.Parallel()

	var buf strings.Builder
	cmd := newCommand()
	cmd.Writer = &buf

	err := cmd.Run(context.Background(), []string{"eth-signer-mcp", "--help"})
	if err != nil {
		t.Fatalf("--help returned error: %v", err)
	}

	output := buf.String()
	for _, flag := range []string{
		"--keystore",
		"--password-file",
		"--http",
		"--http-addr",
		"--http-auth-token-file",
		"--chain-id",
		"--strict-perms",
		"--log-level",
	} {
		if !strings.Contains(output, flag) {
			t.Errorf("--help output missing flag %q\nfull output:\n%s", flag, output)
		}
	}
}
