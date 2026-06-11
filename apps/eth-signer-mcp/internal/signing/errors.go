package signing

import "log/slog"

// Error code constants for ToolError. These codes are stable and cross the wire.
// They mirror the six PRD codes defined in the architecture's §Error Handling section.
const (
	// CodeKeystoreError is returned when the keystore file is missing, unreadable,
	// malformed, or has an unusable "address" field. This is a boot-time failure.
	CodeKeystoreError = "keystore_error"

	// CodePasswordError is returned when the password file cannot be read or when
	// DecryptKey reports a wrong-password MAC failure (keystore.ErrDecrypt).
	// A non-ErrDecrypt DecryptKey failure maps to CodeInternalError instead.
	CodePasswordError = "password_error"

	// CodeInvalidInput is returned when a tool request fails input validation
	// (missing fields, hex parse errors, EIP-55 checksum failure, chainId=0, etc.).
	CodeInvalidInput = "invalid_input"

	// CodeUnsupportedType is returned when the requested transaction type is not
	// supported (v1 supports types 0 and 2 only).
	CodeUnsupportedType = "unsupported_type"

	// CodeChainIDMismatch is returned when the request's chainId differs from the
	// chain-id guard configured at signer construction.
	CodeChainIDMismatch = "chain_id_mismatch"

	// CodeInternalError is returned when an unexpected internal failure occurs
	// (e.g. sender-mismatch after signing, recovered panic, non-ErrDecrypt decrypt
	// failure). The Cause field holds the underlying error for logs; it is never
	// serialised to the wire.
	CodeInternalError = "internal_error"
)

// ToolError is the single structured error type returned by signing operations.
// Code and Message cross the wire (JSON-encoded by server/errors.go); Cause is
// logs-only and MUST NEVER be serialised or included in user-facing output.
type ToolError struct {
	// Code is a stable, machine-readable error code (one of the Code* constants).
	// It is included in the wire response.
	Code string

	// Message is a short, human-readable explanation safe for the wire.
	// It must never echo raw input values (a caller-supplied secret must not be
	// reflectable into logs or the wire response).
	Message string

	// Cause holds the underlying error, if any. It is for logging only and is
	// NEVER serialised or forwarded to callers. Any log site that emits Cause
	// must pass the cause as a structured field, not interpolate it into the
	// Message string.
	//
	// The json:"-" tag enforces at the encoding layer that Cause is never
	// marshalled, regardless of how the enclosing struct is serialised.
	// (Defence-in-depth: the server layer must also only encode Code+Message.)
	Cause error `json:"-"`
}

// Error implements the error interface. It returns "Code: Message" — it does NOT
// interpolate Cause.Error() to prevent accidental leakage of internal error details
// or secret-bearing error messages into log lines that embed the error string.
// The Cause is available via Unwrap() for errors.Is/As, and must be logged
// separately at the call site (e.g. as a distinct slog field).
//
// Nil-receiver safe: calling Error() on a nil *ToolError returns a static string
// rather than panicking, preventing accidental nil-pointer dereferences at log sites.
func (e *ToolError) Error() string {
	if e == nil {
		return "internal_error: <nil ToolError>"
	}
	return e.Code + ": " + e.Message
}

// Unwrap returns the underlying cause so errors.Is/As work through the chain.
// The cause is accessible programmatically but is never included in Error().
func (e *ToolError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Cause
}

// LogValue implements slog.LogValuer so that passing a *ToolError directly to
// slog (e.g. slog.Info("err", "tool_err", te)) never leaks the Cause field.
//
// Only Code and Message are included in the returned log value.  The Cause field
// is logs-only context that must be logged separately by the call site — never
// surfaced here to prevent accidental leakage of internal error details or
// secret-bearing error messages.
func (e *ToolError) LogValue() slog.Value {
	if e == nil {
		return slog.GroupValue(
			slog.String("code", "internal_error"),
			slog.String("message", "<nil ToolError>"),
		)
	}
	return slog.GroupValue(
		slog.String("code", e.Code),
		slog.String("message", e.Message),
	)
}
