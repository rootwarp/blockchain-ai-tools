// Package signing — context.go — Issue 2.6.
// Request-ID context helpers. Defined here (in signing) so the signing package
// requires no internal imports; the server package calls these helpers to attach
// and read the request ID on the context passed to SignTransaction.
package signing

import "context"

// requestIDKey is the unexported context key type for request IDs.
// Using a package-private type prevents collision with keys from other packages
// that might also store strings in context values.
type requestIDKey struct{}

// WithRequestID returns a copy of ctx carrying the given request ID. The ID is
// retrievable in any downstream function via RequestIDFromContext. A second call
// produces a new context with the new ID; the parent context is unchanged.
//
// The request ID is used to correlate the per-signing audit log line with the
// originating MCP tool call. The server package (internal/server) generates or
// propagates the ID before calling Signer.SignTransaction.
func WithRequestID(ctx context.Context, id string) context.Context {
	return context.WithValue(ctx, requestIDKey{}, id)
}

// RequestIDFromContext returns the request ID stored in ctx by a prior WithRequestID
// call, and a boolean indicating whether an ID was set.
//
// Returns ("", false) if no request ID has been set (e.g. in tests that call
// SignTransaction directly without going through the server handler).
//
// The returned ID is used only as an opaque correlation token in log output. It is
// never parsed, interpreted, or included in any wire response. Callers producing
// request IDs (e.g. the server handler's UUIDv4 generator) are responsible for
// keeping the ID length reasonable; excessively long IDs inflate every log line
// that carries the field. The UUIDv4 format (36 chars) is the expected maximum.
func RequestIDFromContext(ctx context.Context) (string, bool) {
	v := ctx.Value(requestIDKey{})
	if v == nil {
		return "", false
	}
	id, ok := v.(string)
	return id, ok
}
