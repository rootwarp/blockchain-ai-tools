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
// call. The second return value is false if no request ID has been set.
//
// The signing package uses this to populate the audit log line; callers that do
// not set a request ID (e.g. in tests that call SignTransaction directly) will
// receive an empty string and ok == false.
func RequestIDFromContext(ctx context.Context) (string, bool) {
	v := ctx.Value(requestIDKey{})
	if v == nil {
		return "", false
	}
	id, ok := v.(string)
	return id, ok
}
