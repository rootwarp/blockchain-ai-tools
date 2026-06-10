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
	"errors"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/rootwarp/blockchain-ai-tools/apps/eth-signer-mcp/internal/obs"
	"github.com/rootwarp/blockchain-ai-tools/apps/eth-signer-mcp/internal/server"
	"github.com/rootwarp/blockchain-ai-tools/apps/eth-signer-mcp/internal/signing"
	"github.com/urfave/cli/v3"
)

func main() {
	cmd := newCommand()
	if err := cmd.Run(context.Background(), os.Args); err != nil {
		// cli.Exit errors carry a specific exit code (e.g. 2 for permission
		// failures).  The framework's HandleExitCoder already called os.Exit with
		// that code before Run returned in normal execution, so this branch is only
		// reached when cli.OsExiter is overridden (e.g. during integration tests).
		// Honour the embedded code so callers always observe the correct process
		// exit status. errors.As (not a bare type assertion) so a wrapped
		// ExitCoder is still recognised.
		var ec cli.ExitCoder
		if errors.As(err, &ec) {
			os.Exit(ec.ExitCode())
		}
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
// IMPORTANT: the returned *cli.Command must NOT be reused across multiple Run
// calls. urfave/cli v3 sets each flag's hasBeenSet=true on first parse and never
// resets it, so a second Run on the same instance sees stale IsSet() results and
// would misclassify --chain-id presence (an absent flag read as &0, which
// validate() rejects as replay-unprotected). Always obtain a fresh instance via
// newCommand() per Run. main() runs once per process and is therefore safe.
//
// cmd.Version is set here (not inside the Action) because urfave/cli v3 handles
// --version before the Action fires; wiring inside run() would be a no-op.
// obs.Build().String() returns the version+build-info fields only — urfave/cli v3's
// DefaultPrintVersion prepends "{cmd.Name} version " automatically, so the full
// output is: "eth-signer-mcp version <Version> (commit <Commit>, built <Date>, <GoVersion>)".
func newCommand() *cli.Command {
	return &cli.Command{
		Name:    "eth-signer-mcp",
		Usage:   "offline Ethereum signer MCP server (stdio by default; Streamable HTTP via --http in Phase 3)",
		Version: obs.Build().String(), // Issue 1.4: all four fields on --version
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

// run is the cli Action implementing the startup sequence for eth-signer-mcp:
// parse → validate → logger → fsperm checks → vault → signer → server → RunStdio.
//
// Startup sequence (architecture Flow D):
//  1. Parse flags + validate (urfave/cli v3 layer)
//  2. Construct logger (obs.NewLogger)
//  3. File-permission checks (issue 1.6, wired once — fsperm is wired once per
//     Phase Conventions)
//  4. Construct signing.FileKeyVault (fail fast: exit non-zero on any error)
//  5. Construct signing.Signer with ChainIDGuard from --chain-id (ONLY place guard enters)
//  6. Construct *server.Server with signer (issue 2.7: sign_transaction + get_address)
//  7. Guard against --http (Streamable HTTP lands in Phase 3; clean error now)
//  8. Install signal.NotifyContext for SIGINT/SIGTERM graceful shutdown
//  9. Run the server on the stdio transport
//
// Exit-code contract: a cli.Exit("…", 2) value is returned for permission
// failures (missing/unreadable path, or too-open + --strict-perms). The
// framework's HandleExitCoder translates this to an OS exit code of 2 before
// returning from cmd.Run; main() also checks for cli.ExitCoder as a
// belt-and-suspenders fallback (e.g. when OsExiter is overridden in tests).
//
// STDOUT DISCIPLINE: nothing in this function may write to os.Stdout.  Stdout
// carries MCP JSON-RPC frames written by the SDK's StdioTransport.  All logs
// go to os.Stderr via obs.NewLogger.
//
// There is exactly ONE error-return path through run(), so any deferred
// cleanup will execute even on early failures.
func run(ctx context.Context, cmd *cli.Command) error {
	cfg := buildConfig(cmd)
	if err := validate(cfg); err != nil {
		return err
	}

	// Step 2: construct the logger from the validated log level.
	// Never log secret material at any level — see package obs for redaction rules.
	logger := obs.NewLogger(cfg.LogLevel)
	logger.Info("eth-signer-mcp starting", "log_level", cfg.LogLevel)

	// Step 3: file-permission startup check — wired once, final form
	// (architecture Flow D, Phase Conventions: "fsperm is wired once").
	// Checks keystore and password-file paths before any transport starts.
	// Fail fast if either path is missing, not a regular file, or (when
	// --strict-perms is set) group/world accessible.
	if err := applyPermChecks(
		[]string{cfg.KeystorePath, cfg.PasswordPath},
		cfg.StrictPerms,
		logger,
	); err != nil {
		return err
	}

	// Step 4: construct the KeyVault (boot-time snapshot, fail fast).
	//
	// Any failure here is a startup error — the keystore is missing, malformed,
	// or has no usable address field. The error is a *signing.ToolError with
	// Code == signing.CodeKeystoreError and a clear message. We print the message
	// to stderr and exit non-zero so the operator can diagnose without reading logs.
	//
	// The constructor reads the keystore JSON and address but does NOT read the
	// password file (per the lifecycle contract: password is re-read on each signing
	// call to support password rotation without restart).
	vault, err := signing.NewFileKeyVault(signing.VaultOptions{
		KeystorePath: cfg.KeystorePath,
		PasswordPath: cfg.PasswordPath,
	})
	if err != nil {
		// Print the keystore_error message to stderr so it is visible even when
		// the log format is JSON (operators running the binary directly may not
		// parse JSON log lines). The logger also emits it for structured log
		// consumers.
		fmt.Fprintf(os.Stderr, "eth-signer-mcp: keystore startup error: %v\n", err)
		logger.Error("keystore startup error", "error", err.Error())
		return fmt.Errorf("keystore startup error: %w", err)
	}
	logger.Info("keystore loaded", "address", vault.Address().Hex())

	// Step 5: construct the Signer.
	//
	// The ChainIDGuard is threaded from --chain-id here and NOWHERE ELSE.
	// This is the ONLY place the guard enters the system (architecture §locked decision).
	// cfg.ChainIDGuard is nil when --chain-id is not set (no guard);
	// non-nil when explicitly set (validate() has already rejected 0).
	signer := signing.NewSigner(vault, signing.SignerOptions{
		ChainIDGuard: cfg.ChainIDGuard,
		Logger:       logger,
	})

	// Step 6: construct the MCP server with both tools registered.
	srv := server.New(signer, server.Options{
		Name:    "eth-signer-mcp",
		Version: obs.Build().Version,
		Logger:  logger,
	})

	// Step 7: guard against --http (Streamable HTTP transport arrives in Phase 3).
	// The flag exists in Phase 1 so --help is truthful; the transport is not yet wired.
	// Never use the word "SSE" — the second transport is MCP Streamable HTTP
	// (Phase Conventions: "The second transport … is never called 'HTTP/SSE'").
	if cfg.HTTP {
		return fmt.Errorf("the Streamable HTTP transport arrives in Phase 3; " +
			"use stdio (default) for now")
	}

	// Step 8: wire signal.NotifyContext for SIGINT/SIGTERM graceful shutdown.
	// When the OS delivers SIGINT or SIGTERM, ctx is cancelled, which propagates
	// to RunStdio, which closes the session and returns nil (normalised by RunStdio).
	ctx, stop := signal.NotifyContext(ctx, os.Interrupt, syscall.SIGTERM)
	defer stop()

	// Step 9: run the MCP server on the stdio transport.
	// Returns nil on clean EOF (client closed stdin) or on SIGINT/SIGTERM.
	return srv.RunStdio(ctx)
}
