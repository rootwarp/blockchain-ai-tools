package server

// http.go — Issue 3.1: Streamable HTTP transport (RunHTTP).
//
// RunHTTP is the second transport over the same *mcp.Server that RunStdio uses
// (ADR-002: one server, two transports; tools are registered once in New).
//
// Startup sequence:
//  1. Validate the bearer token file before binding — the listener MUST NOT
//     bind if this step fails (operators get a clean failure, not a half-broken
//     listening process).
//  2. Bind the listener (net.Listen "tcp" on opts.Addr).
//  3. Print the resolved bound address to stderr; log it; signal ReadyCh.
//  4. Construct the SDK StreamableHTTPHandler with DNS-rebinding protection on
//     (DisableLocalhostProtection stays false, the default).
//  5. Assemble the http.Server pipeline.  Future middleware slots in as
//     func(http.Handler) http.Handler without re-plumbing (3.2 bearer auth,
//     3.3 reqlog, 3.4 MaxBytesHandler).
//  6. Serve in a goroutine; on ctx.Done(), gracefully shut down (skeleton here;
//     full drain semantics and the 3 s grace window are finalised in 3.7).
//
// A clean ctx-cancel shutdown returns nil (mirrors normalizeShutdownErr for the
// stdio transport). Any other error is returned unchanged.

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/rootwarp/blockchain-ai-tools/apps/eth-signer-mcp/internal/signing"
)

// HTTPOptions configures the Streamable HTTP transport for RunHTTP.
type HTTPOptions struct {
	// Addr is the TCP address to listen on (e.g. "127.0.0.1:0" for an
	// ephemeral loopback port).  Defaults to "127.0.0.1:0" when empty.
	Addr string

	// TokenFilePath is the path to the bearer auth token file (required).
	// The file is read at startup; an unreadable or empty file causes RunHTTP
	// to return an error BEFORE any listener is bound.  Token contents are
	// never logged — error messages name the path and error class only.
	TokenFilePath string

	// ReadyCh is an optional test seam.  When non-nil, the resolved bound
	// net.Addr is sent here after Listen succeeds and the announce line has
	// been printed.  Tests gate their client requests on this channel instead
	// of sleeping.  Production callers leave it nil.
	ReadyCh chan<- net.Addr

	// stderrW, if non-nil, receives the announce line instead of os.Stderr.
	// Test-only; production code leaves this nil (defaults to os.Stderr).
	stderrW io.Writer

	// captureHTTPSrvCh, if non-nil, receives the *http.Server immediately
	// before Serve is called.  Test-only; lets tests inspect server config.
	captureHTTPSrvCh chan<- *http.Server
}

// RunHTTP starts the MCP Streamable HTTP transport, binds on opts.Addr, and
// serves until ctx is cancelled.  See the file header for the full startup
// sequence.
func (s *Server) RunHTTP(ctx context.Context, opts HTTPOptions) error {
	addr := opts.Addr
	if addr == "" {
		addr = "127.0.0.1:0"
	}

	// ── Step 1: Validate the bearer token file BEFORE binding any listener ──
	//
	// Read, strip exactly one trailing '\n' (not TrimSpace — tokens may contain
	// inner spaces or other whitespace), reject if empty.
	//
	// SECURITY: token contents are NEVER logged or echoed; error messages
	// name the path and error class only.
	raw, err := os.ReadFile(opts.TokenFilePath)
	if err != nil {
		return fmt.Errorf("token file %q: %w", opts.TokenFilePath, err)
	}

	tokenBytes := raw
	if len(tokenBytes) > 0 && tokenBytes[len(tokenBytes)-1] == '\n' {
		tokenBytes = tokenBytes[:len(tokenBytes)-1]
	}
	if len(tokenBytes) == 0 {
		signing.ZeroBytes(raw) // zero the raw token (best-effort, ADR-009)
		return fmt.Errorf("token file %q: empty after stripping trailing newline", opts.TokenFilePath)
	}

	// Hash the token; store sha256(token) inside a signing.Secret so it never
	// leaks via fmt/slog/JSON.  Zero the raw bytes immediately.
	//
	// The 3.2 BearerVerifier constructor replaces this placeholder once it
	// exists.  For now we hold the hash until RunHTTP exits so the Secret
	// (and the zeroing below) are exercised from the first commit.
	hash := sha256.Sum256(tokenBytes)
	tokenHash := signing.NewSecret(hash) // sha256(token); prevents direct logging
	signing.ZeroBytes(raw)               // zero raw token (best-effort, ADR-009)
	_ = tokenHash                        // used by 3.2 verifier; suppress unused warning

	// ── Step 2: Bind the listener ──────────────────────────────────────────
	//
	// The listener MUST NOT bind if Step 1 fails (early returns above handle
	// every failure path).
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return fmt.Errorf("RunHTTP listen %q: %w", addr, err)
	}

	// ── Step 3: Announce the bound address ─────────────────────────────────
	//
	// Print to stderr (defaulting to os.Stderr; overridden by stderrW in
	// tests).  The 3.8 e2e harness parses the exact
	// "eth-signer-mcp listening on 127.0.0.1:<port>" shape — keep it stable.
	stderrW := opts.stderrW
	if stderrW == nil {
		stderrW = os.Stderr
	}
	_, _ = fmt.Fprintf(stderrW, "eth-signer-mcp listening on %s\n", ln.Addr())
	s.logger.Info("http server listening", "addr", ln.Addr().String())

	// Signal test seams AFTER the announce line has been written.
	if opts.ReadyCh != nil {
		opts.ReadyCh <- ln.Addr()
	}

	// ── Step 4: Construct the SDK StreamableHTTPHandler ────────────────────
	//
	// Deliberately zero-valued fields (see docs/mcp-sdk-spike.md §Question 2):
	//   Stateless:               false — stateful sessions (correct for this tool server)
	//   JSONResponse:            false — text/event-stream responses (MCP spec default)
	//   EventStore:              nil   — no stream-resumption persistence needed for Phase 3
	//   SessionTimeout:          0     — idle sessions never timed out (tunable in 3.9 polish)
	//   DisableLocalhostProtection: false — DNS-rebinding guard ON per ADR-006 and the
	//                                       1.7 spike note §Question 2 ("Never set to true
	//                                       in production"); this is the default so no
	//                                       explicit assignment is needed.
	//   CrossOriginProtection:   nil   — deprecated; wrap the handler with middleware instead
	//                                    (spike note §Q2; our pipeline approach supersedes it).
	sdkHandler := mcp.NewStreamableHTTPHandler(
		func(*http.Request) *mcp.Server { return s.mcpServer },
		&mcp.StreamableHTTPOptions{
			Logger: s.logger,
			// DisableLocalhostProtection is intentionally left at its zero value
			// (false), keeping the rebinding guard ON.  See docs/mcp-sdk-spike.md
			// §Question 2 for the full field survey and the ADR-006 rationale.
		},
	)

	// ── Step 5: Assemble the http.Server pipeline ──────────────────────────
	//
	// Current assembly order (outermost → innermost):
	//   [SDK handler]
	//
	// Future middleware will slot in as func(http.Handler) http.Handler:
	//   [3.2] pipeline = bearerVerifier.Middleware(pipeline)   // 401 before SDK sees body
	//   [3.3] pipeline = reqlogMiddleware(pipeline, s.logger)  // req-id + latency log
	//   [3.4] pipeline = http.MaxBytesHandler(pipeline, 1<<20) // 1 MiB body cap (outermost)
	var pipeline http.Handler = sdkHandler

	httpSrv := &http.Server{
		Handler: pipeline,
		// ReadHeaderTimeout guards against Slowloris-style slow-header stalls
		// even on loopback.  5 s is generous for any well-behaved MCP client
		// (ADR-006; confirmed during 1.7 spike as the only non-default knob).
		ReadHeaderTimeout: 5 * time.Second,
	}

	// Expose http.Server to tests via the capture channel (test-only seam).
	if opts.captureHTTPSrvCh != nil {
		opts.captureHTTPSrvCh <- httpSrv
	}

	// ── Step 6: Serve and graceful shutdown ────────────────────────────────
	//
	// Shutdown skeleton — full drain semantics (3 s grace window, in-flight
	// signing call completion, queued-request ctx.Err() on cancel) are
	// finalised in issue 3.7.
	serveErrCh := make(chan error, 1)
	go func() {
		serveErr := httpSrv.Serve(ln)
		if errors.Is(serveErr, http.ErrServerClosed) {
			serveErrCh <- nil
		} else {
			serveErrCh <- serveErr
		}
	}()

	select {
	case serveErr := <-serveErrCh:
		// Serve exited unexpectedly — not triggered by Shutdown.
		return serveErr
	case <-ctx.Done():
		// ctx cancelled (SIGINT/SIGTERM or test cancel) — graceful shutdown.
		graceCtx, graceCancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer graceCancel()
		if shutdownErr := httpSrv.Shutdown(graceCtx); shutdownErr != nil {
			return shutdownErr
		}
		<-serveErrCh // wait for Serve to finish
		return nil   // clean ctx-cancel → nil (mirrors normalizeShutdownErr)
	}
}
