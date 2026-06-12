package server

// reqlog.go — Issue 3.3: Request-id + HTTP request-logging middleware.
//
// Middleware pipeline position (ADR-006, docs/mcp-sdk-spike.md §Question 3):
//
//	[3.4] MaxBytesHandler → [3.3] reqlog → [3.2] bearer auth → [SDK handler]
//
// reqlog sits OUTSIDE bearer auth so that even rejected (401/403) requests are
// logged with their status codes. This is required by the 3.5 pipeline-order
// regression test.
//
// Request-id generation (docs/mcp-sdk-spike.md §Question 4):
// The SDK (v1.6.1) does not expose a request-id accessor on CallToolRequest.
// We therefore generate a UUIDv4 from crypto/rand for every request; this keeps
// the implementation stdlib-only and avoids a uuid dependency.
// generateRequestID is defined in handlers.go and shared here.
//
// Latency measurement: latency_ms is measured to handler return. For Streamable
// HTTP responses that may stream, this is the time to first flush — acceptable
// for this tool's request shapes, which complete within a single call-response.
//
// Log fields (exactly): request_id, remote_addr, status, latency_ms.
// NO URL / query echo, NO header values (especially not Authorization), NO body bytes.
// One line per request, emitted on completion (not one on entry + one on exit).

import (
	"log/slog"
	"net/http"
	"time"

	"github.com/rootwarp/blockchain-ai-tools/apps/eth-signer-mcp/internal/signing"
)

// statusCaptureWriter wraps http.ResponseWriter to capture the first HTTP status
// code written by the handler chain.
//
// Default behavior matches net/http's own contract:
//   - If WriteHeader is never called but Write is, the status is 200 (implicit OK).
//   - If neither WriteHeader nor Write is called, capturedStatus returns 200.
//   - Only the FIRST WriteHeader call is captured; subsequent calls are forwarded
//     to the underlying ResponseWriter but do not change the captured code.
//
// This is used by the reqlog middleware to record the final response status for
// the structured log line.
type statusCaptureWriter struct {
	http.ResponseWriter
	status  int
	written bool // true once WriteHeader or Write has been called
}

// WriteHeader captures the first status code and forwards it to the underlying
// ResponseWriter. Subsequent calls are forwarded but do not change the captured code.
func (w *statusCaptureWriter) WriteHeader(status int) {
	if !w.written {
		w.status = status
		w.written = true
	}
	w.ResponseWriter.WriteHeader(status)
}

// Write forwards the body to the underlying ResponseWriter. If WriteHeader has
// not been called yet, it implicitly captures 200 (matching the http.ResponseWriter
// contract that the first Write sends an implicit 200 header).
func (w *statusCaptureWriter) Write(b []byte) (int, error) {
	if !w.written {
		w.status = http.StatusOK
		w.written = true
	}
	return w.ResponseWriter.Write(b)
}

// capturedStatus returns the HTTP status code that was (or will be) sent to the
// client. Returns 200 if neither WriteHeader nor Write was ever called.
func (w *statusCaptureWriter) capturedStatus() int {
	if !w.written {
		return http.StatusOK
	}
	return w.status
}

// newRequestLogMiddleware returns a func(http.Handler) http.Handler that:
//  1. Generates a UUIDv4 request_id (via generateRequestID, stdlib-only).
//  2. Attaches the id to the request context via signing.WithRequestID.
//  3. Calls next.ServeHTTP with the enriched context.
//  4. On return, emits exactly ONE info-level structured log line via logger with
//     fields: request_id, remote_addr, status, latency_ms.
//
// The middleware never logs URL paths, query parameters, header names/values,
// or request body bytes — only the four listed fields plus the standard slog
// fields (time/level/msg).
//
// Inject the logger from Server.logger so the reqlog output uses the same
// handler configuration (JSON, level, etc.) as the rest of the server.
func newRequestLogMiddleware(logger *slog.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			// Generate a UUIDv4 request_id for this request.
			// The SDK (v1.6.1) does not expose an accessor for a request-id on
			// CallToolRequest (docs/mcp-sdk-spike.md §Question 4), so we generate
			// our own. generateRequestID uses crypto/rand and requires no extra dep.
			id := generateRequestID()

			// Attach the id to the context so downstream handlers — including the
			// sign_transaction handler — can read it via signing.RequestIDFromContext
			// and propagate it to the signing audit line.
			ctx := signing.WithRequestID(r.Context(), id)

			// Wrap the ResponseWriter to capture the status code.
			sw := &statusCaptureWriter{ResponseWriter: w}
			start := time.Now()

			// Call the next handler (bearer auth → SDK handler) with the enriched context.
			next.ServeHTTP(sw, r.WithContext(ctx))

			// Emit exactly ONE info-level log line after the handler returns.
			// Latency is measured to handler return; for Streamable HTTP responses
			// that may stream, this is the time to first flush — acceptable for
			// this tool's request shapes.
			logger.Info("http request",
				"request_id", id,
				"remote_addr", r.RemoteAddr,
				"status", sw.capturedStatus(),
				"latency_ms", time.Since(start).Milliseconds(),
			)
		})
	}
}
