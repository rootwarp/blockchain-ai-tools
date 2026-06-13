// Tests for context helpers — Issue 2.6.
// Internal tests (package signing).
package signing

import (
	"context"
	"testing"
)

// TestWithRequestID_StoresAndRetrieves verifies that a request ID stored via
// WithRequestID is retrievable via RequestIDFromContext.
func TestWithRequestID_StoresAndRetrieves(t *testing.T) {
	t.Parallel()

	ctx := WithRequestID(context.Background(), "req-abc-123")
	id, ok := RequestIDFromContext(ctx)
	if !ok {
		t.Fatal("RequestIDFromContext: ok = false, expected true")
	}
	if id != "req-abc-123" {
		t.Errorf("id = %q, want %q", id, "req-abc-123")
	}
}

// TestRequestIDFromContext_Missing verifies that RequestIDFromContext returns
// (empty-string, false) when no request ID has been set.
func TestRequestIDFromContext_Missing(t *testing.T) {
	t.Parallel()

	id, ok := RequestIDFromContext(context.Background())
	if ok {
		t.Errorf("ok = true for context without request ID, want false")
	}
	if id != "" {
		t.Errorf("id = %q for context without request ID, want empty string", id)
	}
}

// TestWithRequestID_Override verifies that a second WithRequestID call on the
// same context chain produces the new value, while the parent context retains
// the original.
func TestWithRequestID_Override(t *testing.T) {
	t.Parallel()

	parent := WithRequestID(context.Background(), "first")
	child := WithRequestID(parent, "second")

	id, ok := RequestIDFromContext(child)
	if !ok || id != "second" {
		t.Errorf("child ctx: id = %q, ok = %v; want %q, true", id, ok, "second")
	}

	// Parent context should still carry the first ID, unchanged.
	id, ok = RequestIDFromContext(parent)
	if !ok || id != "first" {
		t.Errorf("parent ctx: id = %q, ok = %v; want %q, true", id, ok, "first")
	}
}

// TestWithRequestID_EmptyID verifies that an empty string ID is stored and
// retrieved faithfully (empty is a valid, if unusual, request ID).
func TestWithRequestID_EmptyID(t *testing.T) {
	t.Parallel()

	ctx := WithRequestID(context.Background(), "")
	id, ok := RequestIDFromContext(ctx)
	// ok should be true even for empty string (the key is present).
	if !ok {
		t.Errorf("ok = false for empty-ID context, want true")
	}
	if id != "" {
		t.Errorf("id = %q, want empty string", id)
	}
}
