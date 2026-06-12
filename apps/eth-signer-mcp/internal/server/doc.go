// Package server implements the MCP integration layer for eth-signer-mcp:
// *mcp.Server construction, tool registration and handlers, error wire-encoding,
// stdio transport (RunStdio), and Streamable HTTP transport (RunHTTP) with
// bearer auth, request-id logging, resource bounds, and graceful shutdown.
//
// # Streamable HTTP pipeline
//
// RunHTTP assembles the middleware pipeline in a single place (http.go, Step 5).
// All tests exercise this pipeline via RunHTTP — no test builds its own
// divergent pipeline.  The pipeline order, outermost to innermost, is:
//
//	[1] http.MaxBytesHandler  — 1 MiB body cap (ADR-006); wraps the entire
//	                            chain so an oversized body is rejected before
//	                            reqlog, auth, or the SDK reads any bytes.
//	[2] reqlog middleware     — generates a UUIDv4 request_id, stores it in
//	                            the request context via signing.WithRequestID,
//	                            and emits one structured log line per request
//	                            (method, remote_addr, status, latency_ms) after
//	                            the inner handler returns.  Sits OUTSIDE auth so
//	                            even 401/403 responses are request-logged.
//	[3] bearer auth           — BearerVerifier.Middleware checks the
//	                            "Authorization: Bearer <token>" header using
//	                            sha256 on both sides and subtle.ConstantTimeCompare.
//	                            Returns 401 (empty body, WWW-Authenticate: Bearer)
//	                            BEFORE the SDK sees the request body.
//	[4] SDK handler           — mcp.NewStreamableHTTPHandler with
//	                            DisableLocalhostProtection: false (default).
//	                            The SDK's DNS-rebinding guard fires at the top of
//	                            ServeHTTP and returns 403 when the server is
//	                            loopback-bound but the request Host header is
//	                            non-loopback.  Tool dispatch and JSON-RPC response
//	                            handling run here.
//
// # Hardening layers (ADR-006)
//
// Bind / loopback-only: RunHTTP enforces that the bound address is a loopback
// interface (*net.TCPAddr.IP.IsLoopback()), rejecting any non-loopback bind
// even when cmd-level validation is bypassed (defense-in-depth).
//
// Bearer 401: auth middleware (position 3 in the pipeline) validates the
// Authorization header before the SDK handler runs.  On rejection it writes
// no response body, preventing any MCP error-response body from being returned
// on the 401 path.
//
// SDK rebinding 403: the SDK's built-in DNS-rebinding protection (position 4)
// rejects requests whose Host header is non-loopback when the server is bound
// to a loopback address.  Auth at position 3 wraps the SDK handler at position
// 4, so a request with both a bad bearer AND a forged Host header receives 401
// (auth fires first), not 403.
//
// 1 MiB body cap: http.MaxBytesHandler (position 1, outermost) rejects bodies
// that exceed 1 MiB.  Because auth is header-only (never reads r.Body), the
// 401 path never triggers the body-cap reader — a large malicious body incurs
// no extra allocation on the 401 path.
//
// Decrypt-semaphore serialization: the signing.FileKeyVault holds a
// capacity-1 semaphore that serializes KDF invocations across concurrent HTTP
// requests.  A request ctx cancelled while queued on the semaphore returns
// ctx.Err() without starting the KDF, proven by the concurrent-calls
// integration test (concurrent_test.go).
//
// Graceful shutdown: on ctx.Done() (SIGINT/SIGTERM via signal.NotifyContext),
// RunHTTP calls http.Server.Shutdown with a 3 s grace context derived from
// context.Background() so the drain window is independent of the
// already-cancelled signal ctx.  In-flight signing calls complete normally;
// queued requests observe ctx.Err() and return early.  RunHTTP returns nil on
// a clean drain and propagates the Shutdown error if the grace window expires.
package server
