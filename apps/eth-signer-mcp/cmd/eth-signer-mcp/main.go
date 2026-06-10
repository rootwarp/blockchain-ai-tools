// Package main is the entry point for the eth-signer-mcp binary.
//
// Why this file lives under cmd/eth-signer-mcp/ rather than at the module root:
//
//  1. Architecture layout — the module is structured as one cmd composition root
//     plus three internal packages (internal/signing, internal/server, internal/obs).
//     The module root is kept package-free so the directory tree mirrors these four
//     concern clusters cleanly.
//
//  2. Binary naming — `go build ./cmd/eth-signer-mcp` produces a binary named
//     "eth-signer-mcp" (matching the cmd directory). A root-level main.go would
//     produce a binary named after the module's last path segment, which is also
//     "eth-signer-mcp" but is a coincidence rather than a contract; the cmd/
//     convention makes the intent explicit.
//
//  3. Future-proofing — keeping the module root package-free prevents the scaffolder
//     or future contributors from inadvertently reintroducing a root-level main.go
//     that conflicts with this entry point. If additional binaries are ever needed
//     (e.g. a migration tool), they add a second cmd/<name>/ directory without
//     disturbing the existing layout.
package main

import (
	"context"
	"fmt"
	"os"

	"github.com/urfave/cli/v3"
)

func main() {
	cmd := newCommand()
	if err := cmd.Run(context.Background(), os.Args); err != nil {
		fmt.Fprintf(os.Stderr, "eth-signer-mcp: %v\n", err)
		os.Exit(1)
	}
}

// newCommand constructs the root *cli.Command with the complete v1 flag set.
//
// All flags — including the HTTP-transport flags whose behaviour arrives in Phase 3 —
// are defined here so the CLI contract is frozen once and --help is truthful for
// the full life of v1 (architecture §cmd/eth-signer-mcp; plan issue 1.3).
//
// newCommand is a factory so tests can re-create the command with a custom Action
// (e.g. to capture the parsed config) without mutating the production action.
//
// Version is intentionally empty here. Issue 1.4 wires obs.Build() so that
// --version prints version, commit, build date, and Go version automatically.
func newCommand() *cli.Command {
	return &cli.Command{
		Name:  "eth-signer-mcp",
		Usage: "offline Ethereum signer MCP server (stdio by default; Streamable HTTP via --http in Phase 3)",
		// Version: set by Issue 1.4 from obs.Build().Version; urfave/cli v3 prints it on --version.
		Flags: []cli.Flag{
			&cli.StringFlag{
				Name:     "keystore",
				Usage:    "path to Web3 Secret Storage JSON keystore file (required)",
				Required: true,
			},
			&cli.StringFlag{
				Name:     "password-file",
				Usage:    "path to file containing the keystore password; never provide the password inline (required)",
				Required: true,
			},
			&cli.BoolFlag{
				Name:  "http",
				Usage: "select Streamable HTTP transport (Phase 3); default is stdio",
				Value: false,
			},
			&cli.StringFlag{
				Name:  "http-addr",
				Usage: "bind address for Streamable HTTP transport; ephemeral port by default",
				Value: "127.0.0.1:0",
			},
			&cli.StringFlag{
				Name:  "http-auth-token-file",
				Usage: "path to bearer token file; required when --http is set",
			},
			&cli.Uint64Flag{
				// --chain-id has no default: nil when unset means "no guard".
				// Use cmd.IsSet("chain-id") in buildConfig to distinguish "not provided"
				// (nil ChainIDGuard) from "provided as 0" (&0, which validate() rejects).
				Name:  "chain-id",
				Usage: "optional chain-id guard; signing is refused if the transaction's chain-id does not match; 0 is rejected (replay-unprotected)",
			},
			&cli.BoolFlag{
				Name:  "strict-perms",
				Usage: "refuse to start (exit 2) if keystore or password file is group- or world-readable; default warns only",
				Value: false,
			},
			&cli.StringFlag{
				Name:  "log-level",
				Usage: "log verbosity: debug|info|warn|error (case-insensitive)",
				Value: "info",
			},
		},
		Action: run,
	}
}

// buildConfig reads the parsed CLI flags from cmd into a config value.
//
// --chain-id nilability: urfave/cli v3 has no nilable uint64 flag type.
// We use cmd.IsSet("chain-id") to distinguish two cases:
//   - flag absent            → ChainIDGuard = nil  (no chain-id guard)
//   - flag present (any val) → ChainIDGuard = &value (validate() then rejects 0)
func buildConfig(cmd *cli.Command) config {
	cfg := config{
		KeystorePath:  cmd.String("keystore"),
		PasswordPath:  cmd.String("password-file"),
		HTTP:          cmd.Bool("http"),
		HTTPAddr:      cmd.String("http-addr"),
		TokenFilePath: cmd.String("http-auth-token-file"),
		StrictPerms:   cmd.Bool("strict-perms"),
		LogLevel:      cmd.String("log-level"),
	}
	if cmd.IsSet("chain-id") {
		v := cmd.Uint64("chain-id")
		cfg.ChainIDGuard = &v
	}
	return cfg
}

// run is the cli Action: parse → build config → validate → (future seam).
// On validation failure it returns the error, which main() writes to stderr and
// exits 1. On success, it currently returns nil (exit 0) until later issues
// complete the startup sequence.
func run(ctx context.Context, cmd *cli.Command) error {
	cfg := buildConfig(cmd)
	if err := validate(cfg); err != nil {
		return err
	}

	// TODO(1.4): logger := obs.NewLogger(cfg.LogLevel)
	// TODO(1.4): cmd (root) Version = obs.Build().Version
	// TODO(1.6): for _, p := range []string{cfg.KeystorePath, cfg.PasswordPath} { checkPerms(p, ...) }
	// TODO(1.8): srv := server.New(server.Options{Name: "eth-signer-mcp", Version: obs.Build().Version, Logger: logger})
	// TODO(1.8): if cfg.HTTP { return fmt.Errorf("Streamable HTTP transport arrives in Phase 3") }
	// TODO(1.8): ctx, stop := signal.NotifyContext(ctx, os.Interrupt, syscall.SIGTERM); defer stop()
	// TODO(1.8): return srv.RunStdio(ctx)

	return nil
}
