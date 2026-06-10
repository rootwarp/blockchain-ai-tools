package main

import (
	"context"
	"strings"
	"testing"

	"github.com/urfave/cli/v3"
)

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

	tests := []struct {
		name    string
		args    []string
		wantErr string
	}{
		{
			name:    "http_without_token_file",
			args:    []string{"eth-signer-mcp", "--keystore", "/k", "--password-file", "/p", "--http"},
			wantErr: "--http-auth-token-file",
		},
		{
			name: "http_with_token_file_ok",
			args: []string{
				"eth-signer-mcp", "--keystore", "/k", "--password-file", "/p",
				"--http", "--http-auth-token-file", "/t",
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

	valid := []string{"debug", "info", "warn", "error", "DEBUG", "INFO", "WARN", "ERROR"}
	for _, lvl := range valid {
		t.Run("valid_"+lvl, func(t *testing.T) {
			t.Parallel()
			cmd := newCommand()
			args := []string{"eth-signer-mcp", "--keystore", "/k", "--password-file", "/p", "--log-level", lvl}
			if err := cmd.Run(context.Background(), args); err != nil {
				t.Fatalf("cmd.Run() = %v, want nil for valid log-level %q", err, lvl)
			}
		})
	}

	t.Run("invalid_garbage", func(t *testing.T) {
		t.Parallel()
		cmd := newCommand()
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
func TestRun_SuccessExit(t *testing.T) {
	t.Parallel()

	cmd := newCommand()
	args := []string{"eth-signer-mcp", "--keystore", "/k", "--password-file", "/p"}
	if err := cmd.Run(context.Background(), args); err != nil {
		t.Fatalf("cmd.Run() = %v, want nil (exit 0) for valid args", err)
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
