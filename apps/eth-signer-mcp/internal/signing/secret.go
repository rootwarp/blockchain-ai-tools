// Package signing contains everything that touches key material: secret-hygiene
// primitives (Secret[T], ZeroBytes, ZeroBigInt, Sentinel), the keystore vault,
// transaction parsing/validation/building, signing orchestration, and the
// tool-error taxonomy. It never imports internal/server, internal/obs, or any
// HTTP/RPC client package — the offline invariant is enforced by ADR-007 and ADR-008.
package signing

import (
	"fmt"
	"io"
	"log/slog"
)

// Secret is a generic redacting wrapper that prevents secret values from
// appearing in logs, formatted output, or JSON serialisation. It implements:
//   - fmt.Stringer     → "[REDACTED]"
//   - fmt.GoStringer   → "[REDACTED]" (catches %#v)
//   - fmt.Formatter    → "[REDACTED]" for every verb fmt routes through Formatter
//   - json.Marshaler   → `"[REDACTED]"`
//   - slog.LogValuer   → slog.StringValue("[REDACTED]")
//
// KNOWN GAP (%T / %p): Go's fmt package does not consult the Formatter (or any
// interface) for the %T and %p verbs. %T prints only the type name — no value
// bytes, so it is safe. %p on a non-pointer Secret prints fmt's bad-verb error,
// which includes the struct's field values — a potential leak. NEVER use %p on
// a Secret. This gap is tracked by TestSecret_Format_KnownVerbGaps and is
// accepted under the ADR-009 threat model (the observable requirement is "no
// secrets in logs/outputs", enforced by the leak-scan tests over real log paths).
//
// The only way to read the wrapped value is Expose(). No Unwrap or Value aliases
// exist: any additional accessor would widen the surface that reflection might
// exploit (see the known-leak antipattern test in antipattern_test.go and
// rule 3 in obs/doc.go).
//
// USAGE RULE: never embed a Secret inside a struct that is passed to slog.
// The slog JSON handler reflects through struct fields and may bypass LogValue
// on nested values. Pass secrets as explicit key-value pairs:
//
//	slog.Info("msg", "key", mySecret)   // correct: LogValue IS called
//	slog.Info("msg", "s", myStruct)     // DANGEROUS if myStruct has a Secret field
type Secret[T any] struct {
	v T
}

// NewSecret wraps v in a Secret[T].
func NewSecret[T any](v T) Secret[T] {
	return Secret[T]{v: v}
}

// Expose returns the wrapped value. It is the ONLY legitimate read path.
func (s Secret[T]) Expose() T {
	return s.v
}

// String implements fmt.Stringer.
func (s Secret[T]) String() string {
	return "[REDACTED]"
}

// GoString implements fmt.GoStringer (used by %#v).
func (s Secret[T]) GoString() string {
	return "[REDACTED]"
}

// Format implements fmt.Formatter, writing "[REDACTED]" for every verb that fmt
// routes through the Formatter interface (%v, %+v, %#v, %s, %q, %x, %X, %d, …).
//
// It does NOT cover %T or %p: fmt handles those two verbs itself and never calls
// Formatter for them (see the type doc's KNOWN GAP note). %T is safe (type name
// only); %p must never be used on a Secret.
func (s Secret[T]) Format(f fmt.State, verb rune) {
	_, _ = io.WriteString(f, "[REDACTED]")
}

// MarshalJSON implements json.Marshaler.
// Returns `"[REDACTED]"` as a JSON string literal.
func (s Secret[T]) MarshalJSON() ([]byte, error) {
	return []byte(`"[REDACTED]"`), nil
}

// LogValue implements slog.LogValuer.
func (s Secret[T]) LogValue() slog.Value {
	return slog.StringValue("[REDACTED]")
}
