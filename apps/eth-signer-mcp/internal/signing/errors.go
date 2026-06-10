package signing

// Error code constants for ToolError. These codes are stable and cross the wire.
// They mirror the six PRD codes defined in the architecture's §Error Handling section.
//
// NOTE (interim): This file is introduced in Issue 2.2 as an interim home for the
// ToolError type and code constants. Issue 2.6 will land the full signer orchestration
// alongside the complete six-code taxonomy in this same file; the declarations here
// will be kept in place (not moved).
const (
	// CodeKeystoreError is returned when the keystore file is missing, unreadable,
	// malformed, or has an unusable "address" field. This is a boot-time failure.
	CodeKeystoreError = "keystore_error"

	// CodePasswordError is returned when the password file cannot be read or when
	// DecryptKey reports a wrong-password MAC failure.
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
	// (e.g. sender-mismatch after signing, recovered panic). The Cause field holds
	// the underlying error for logs; it is never serialised to the wire.
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
	Cause error
}

// Error implements the error interface.
// It includes the cause if present, so wrapped errors are diagnosable in logs
// without any serialisation to the wire (the server layer encodes only Code and
// Message).
func (e *ToolError) Error() string {
	if e.Cause != nil {
		return e.Code + ": " + e.Message + ": " + e.Cause.Error()
	}
	return e.Code + ": " + e.Message
}

// Unwrap returns the underlying cause so errors.Is/As work through the chain.
func (e *ToolError) Unwrap() error {
	return e.Cause
}
