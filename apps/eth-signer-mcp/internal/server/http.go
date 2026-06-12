package server

// http.go — Issues 3.1 + 3.2: Streamable HTTP transport (RunHTTP).
//
// RunHTTP is the second transport over the same *mcp.Server that RunStdio uses
// (ADR-002: one server, two transports; tools are registered once in New).
//
// Startup sequence:
//  1. Validate and hash the bearer token file via NewBearerVerifierFromFile —
//     the listener MUST NOT bind if this step fails (fail-fast: operators get a
//     clean error, not a half-open process).  Issue 3.2 replaced the 3.1
//     SHA-256 placeholder with the real BearerVerifier constructor.
//  2. Bind the listener (net.Listen "tcp" on opts.Addr).
//  3. Print the resolved bound address to stderr; log it; signal ReadyCh.
//  4. Construct the SDK StreamableHTTPHandler with DNS-rebinding protection on
//     (DisableLocalhostProtection stays false, the default).
//  5. Assemble the http.Server pipeline (outermost → innermost):
//       MaxBytesHandler (3.4) → reqlog (3.3) → bearer auth (3.2) → SDK handler
//     Each layer is a func(http.Handler) http.Handler — future layers slot in
//     without re-plumbing this function.
//  6. Serve in a goroutine; on ctx.Done(), gracefully shut down with a 5 s
//     grace window (full drain semantics finalised in 3.7).
//
// A clean ctx-cancel shutdown returns nil (mirrors normalizeShutdownErr for the
// stdio transport). Any other error is returned unchanged.

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
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

	// ── Step 1: Validate the bearer token file and build the verifier ──────
	//
	// NewBearerVerifierFromFile reads the file, strips exactly one trailing '\n'
	// (not TrimSpace — tokens may contain inner spaces), rejects an empty result,
	// stores sha256(token) inside a signing.Secret, and zeroes the raw bytes.
	//
	// The listener MUST NOT bind if this step fails — operators get a clean
	// startup failure, not a half-broken listening process.
	//
	// SECURITY: token contents are NEVER logged or echoed; error messages name
	// the path and error class only (enforced inside NewBearerVerifierFromFile).
	verifier, err := NewBearerVerifierFromFile(opts.TokenFilePath)
	if err != nil {
		return err
	}

	// ── Step 2: Bind the listener ──────────────────────────────────────────
	//
	// The listener MUST NOT bind if Step 1 fails (early returns above handle
	// every failure path).
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return fmt.Errorf("RunHTTP listen %q: %w", addr, err)
	}

	// Defense-in-depth: reject a non-loopback bound address even if validate()
	// was bypassed (ADR-006 loopback-only invariant).  This guards against
	// mis-configuration in future call sites that do not go through cmd.
	tcpBound, ok := ln.Addr().(*net.TCPAddr)
	if !ok || !tcpBound.IP.IsLoopback() {
		_ = ln.Close()
		return fmt.Errorf("RunHTTP: bound address is not loopback (ADR-006); check --http-addr")
	}

	// ── Step 3: Announce the bound address ─────────────────────────────────
	//
	// Print to stderr (defaulting to os.Stderr; overridden by stderrW in
	// tests).  The 3.8 e2e harness parses the exact
	// "eth-signer-mcp listening on 127.0.0.1:<port>" shape — keep it stable.
	// The announce line is written BEFORE signalling ReadyCh so that any reader
	// of ReadyCh can be certain the line has already been emitted.
	stderrW := opts.stderrW
	if stderrW == nil {
		stderrW = os.Stderr
	}
	_, _ = fmt.Fprintf(stderrW, "eth-signer-mcp listening on %s\n", ln.Addr())
	s.logger.Info("http server listening", "addr", ln.Addr().String())

	// Signal test seams AFTER the announce line has been written.
	// Use a select so a slow/absent receiver + ctx cancellation does not leak
	// the open listener (the blocking send would otherwise hang RunHTTP).
	if opts.ReadyCh != nil {
		select {
		case opts.ReadyCh <- ln.Addr():
		case <-ctx.Done():
			_ = ln.Close()
			return ctx.Err()
		}
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
	// Final assembly order, outermost → innermost (each line wraps what came before):
	//
	//   [3.4] http.MaxBytesHandler → [3.3] reqlog → [3.2] bearer auth → [SDK handler]
	//
	// Layer semantics (outermost first):
	//
	//   [3.4] MaxBytesHandler — 1 MiB body cap; wraps the ENTIRE pipeline so an
	//         oversized body is rejected BEFORE reqlog, auth, or the SDK ever see the
	//         body content. Uses http.MaxBytesReader internally; the body read fails
	//         with *http.MaxBytesError when the limit is exceeded, causing the SDK's
	//         json.Decoder to return an error.  The SDK returns HTTP 400 for this
	//         decode failure (SDK v1.6.1 observed behavior; see bounds_test.go for
	//         the pinned assertion).
	//
	//   [3.3] reqlog — newRequestLogMiddleware wraps auth; sits OUTSIDE auth so even
	//         401/403 responses are request-logged (issue 3.3 pipeline-order contract).
	//
	//   [3.2] bearer auth — verifier.Middleware wraps the SDK handler; 401 fires
	//         BEFORE the SDK ever sees the body (no MCP session state for bad requests).
	//
	//   [SDK] StreamableHTTPHandler — DNS-rebinding 403 fires at the top of ServeHTTP;
	//         tool dispatch and JSON-RPC response handling run here.
	//
	// This is the SINGLE pipeline-assembly path (3.9 polish requirement).  All tests
	// use RunHTTP, not custom-assembled pipelines, to stay aligned with production.
	var pipeline http.Handler = sdkHandler
	pipeline = verifier.Middleware(pipeline)               // [3.2] 401 before SDK sees body
	pipeline = newRequestLogMiddleware(s.logger)(pipeline) // [3.3] reqlog outside auth — even 401s are logged
	pipeline = http.MaxBytesHandler(pipeline, 1<<20)       // [3.4] 1 MiB body cap — outermost layer (ADR-006)

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
