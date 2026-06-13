// Package obs provides structured logging and build-info utilities for
// eth-signer-mcp.
//
// # Logger
//
// NewLogger returns a JSON-handler *slog.Logger writing to stderr.
// An unparseable level falls back to info — the constructor is infallible
// and never returns an error or panics.
//
// # Build information
//
// Build reads runtime/debug.ReadBuildInfo and returns an Info value
// containing version, commit, build date, and Go version. Every field
// that cannot be determined is set to the literal "<unknown>" — never
// empty, never a panic. This includes builds under go test, which carry
// no VCS stamping.
//
// # Redaction rules (enforcement lives in internal/signing)
//
// The following rules apply to every log statement in the application.
// Violating them is a security defect, not a lint warning:
//
//  1. No secret material at any log level, raw or encoded. Even at DEBUG,
//     a hex-, base64-, or decimal-encoded private key is a leak.
//
//  2. Secrets are only ever held in signing.Secret[T], which redacts on
//     every print/serialize path: fmt.Stringer, fmt.GoStringer,
//     fmt.Formatter (all verbs), json.Marshaler, and slog.LogValuer all
//     return "[REDACTED]".
//
//  3. Never pass a value that contains a Secret — directly or transitively
//     through a struct field, pointer, slice/array element, or map value —
//     to slog. The JSON encoder behind slog.NewJSONHandler reflects through
//     every such nested composite, bypassing the slog.LogValuer interface.
//     The only safe shape is a top-level attribute:
//     slog.Info("...", "k", secret), where LogValuer is honoured. The
//     known-leak anti-pattern test in internal/signing/antipattern_test.go
//     asserts that this leak DOES occur (via a struct carrier), to keep the
//     rule visible and to detect any future slog change that alters the
//     behaviour.
//
//  4. The transaction body (calldata/to/value) is never logged at any
//     level. Phase 2 enforces this in signing.Signer's audit line.
package obs
