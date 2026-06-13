package server

// auth.go — Issue 3.2: Bearer auth middleware (SHA-256 + constant-time compare).
//
// BearerVerifier holds only sha256(expected_token) wrapped in signing.Secret
// so the hash never leaks via fmt/slog/JSON.  The raw token bytes are zeroed
// immediately after hashing (best-effort, ADR-009).
//
// Middleware implements RFC 6750 bearer-token extraction (case-sensitive
// "Bearer " prefix) and compares sha256(supplied) vs sha256(expected) using
// subtle.ConstantTimeCompare.  Hashing both sides first neutralises the
// length-leak short-circuit that would otherwise fire for tokens whose raw
// bytes differ in length from the stored token.
//
// Security properties enforced here:
//   - 401 fires BEFORE the SDK handler sees the body — the SDK handler is
//     NEVER called on any rejection path.
//   - No response body on 401 — nothing derived from the token or its hash
//     is included in the response.
//   - WWW-Authenticate: Bearer is set BEFORE WriteHeader (HTTP spec).
//   - Headers and body of rejected requests are not logged anywhere in this
//     file.  Misconfiguration is reported via structured startup logs only
//     (not here — the constructor is infallible on valid input).

import (
	"crypto/sha256"
	"crypto/subtle"
	"fmt"
	"net/http"
	"os"
	"strings"

	"github.com/rootwarp/blockchain-ai-tools/apps/eth-signer-mcp/internal/signing"
)

// BearerVerifier holds the SHA-256 hash of the expected bearer token wrapped
// in a signing.Secret so the hash value cannot escape via fmt or slog.
//
// The only way to create a BearerVerifier is via NewBearerVerifierFromFile.
type BearerVerifier struct {
	// tokenHash is sha256(expected_token).  The raw token bytes are never stored
	// after construction; they are zeroed before NewBearerVerifierFromFile returns.
	tokenHash signing.Secret[[32]byte]
}

// NewBearerVerifierFromFile reads the bearer token from path, strips exactly
// one trailing '\n' and then one trailing '\r' (to handle CRLF line endings
// from Windows editors and git-on-Windows), rejects an empty result, stores
// sha256(token) inside a signing.Secret, zeroes the raw token bytes, and
// returns the verifier.
//
// Stripping order:
//  1. One trailing '\n' (Unix / CRLF second byte).
//  2. One trailing '\r' (CRLF first byte, if present after the '\n' is gone).
//
// This handles:
//   - Unix "token\n"   → stored as sha256("token")
//   - Windows "token\r\n" → stored as sha256("token")
//   - No newline "token"  → stored as sha256("token")
//
// Do NOT use strings.TrimSpace — tokens may legitimately contain inner spaces
// or other whitespace that the operator deliberately included; we only strip
// the platform line-ending suffix.
//
// Error cases (caller gets a sanitised message naming path + error class only;
// token contents are NEVER logged or echoed):
//   - File unreadable / missing → wrapped os error.
//   - File empty (or newline/CRLF-only) → descriptive error.
//
// SECURITY: token bytes are zeroed before this function returns (best-effort,
// ADR-009).  Only the sha256 hash — itself wrapped in a signing.Secret — is
// retained in the returned BearerVerifier.
func NewBearerVerifierFromFile(path string) (*BearerVerifier, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("token file %q: %w", path, err)
	}

	// Strip exactly one trailing '\n', then one trailing '\r'.
	// Order matters: LF is always last in both Unix (\n) and Windows (\r\n)
	// line endings; stripping \n first exposes the \r for the second strip.
	tokenBytes := raw
	if len(tokenBytes) > 0 && tokenBytes[len(tokenBytes)-1] == '\n' {
		tokenBytes = tokenBytes[:len(tokenBytes)-1]
	}
	if len(tokenBytes) > 0 && tokenBytes[len(tokenBytes)-1] == '\r' {
		tokenBytes = tokenBytes[:len(tokenBytes)-1]
	}

	if len(tokenBytes) == 0 {
		// Zero raw before returning to limit window where the (empty) bytes
		// occupy memory (best-effort cleanup even for empty inputs).
		signing.ZeroBytes(raw)
		return nil, fmt.Errorf("token file %q: empty after stripping trailing newline", path)
	}

	// Hash the token; store sha256(token) inside a signing.Secret.
	// The hash itself never leaves the Secret — slog/fmt both produce "[REDACTED]".
	hash := sha256.Sum256(tokenBytes)
	v := &BearerVerifier{
		tokenHash: signing.NewSecret(hash),
	}

	// Zero the raw token bytes immediately (best-effort, ADR-009).
	// This must happen AFTER hashing and BEFORE returning to the caller so
	// that the raw bytes are not retained in the caller's stack frame.
	signing.ZeroBytes(raw)

	return v, nil
}

// Middleware returns an http.Handler that enforces bearer authentication.
//
// The middleware extracts the "Authorization: Bearer <token>" header using a
// case-sensitive prefix match per RFC 6750.  Any of the following causes an
// immediate 401 response with "WWW-Authenticate: Bearer" and an empty body;
// next is NEVER called:
//   - Missing or empty Authorization header.
//   - Header present but not starting with the exact string "Bearer " (space
//     included; case-sensitive — "bearer" or "BEARER" are rejected).
//   - "Bearer " prefix found but the remainder (the token) is empty.
//   - Token present but sha256(supplied) != sha256(expected) — comparison
//     performed by subtle.ConstantTimeCompare on the 32-byte hashes so no
//     raw-byte or string == comparison is ever made.
//
// On success (hash match), next.ServeHTTP is called with the original request
// and response writer unmodified.
//
// Pipeline position (per ADR-006, 1.7 spike note §Q3):
//
//	[MaxBytesHandler] → [reqlog] → [BearerVerifier.Middleware] → [SDK handler]
//
// The 401 must fire BEFORE the SDK handler sees the body to prevent any MCP
// session state from being created for unauthorised requests.
func (v *BearerVerifier) Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		authHeader := r.Header.Get("Authorization")

		// CutPrefix is case-sensitive: "Bearer " (capital B, trailing space).
		// Missing header, non-Bearer scheme, lowercase "bearer", "Bearer" without
		// trailing space — all yield ok==false → 401.
		token, ok := strings.CutPrefix(authHeader, "Bearer ")
		if !ok || token == "" {
			// Set WWW-Authenticate BEFORE WriteHeader (HTTP spec requirement).
			w.Header().Set("WWW-Authenticate", "Bearer")
			w.WriteHeader(http.StatusUnauthorized)
			// No body: nothing derived from the token or the stored hash.
			return
		}

		// Hash the supplied token.  Comparing sha256 hashes (both sides 32 bytes)
		// via ConstantTimeCompare eliminates the length-leak short-circuit that
		// would fire for mismatched-length raw tokens.
		suppliedHash := sha256.Sum256([]byte(token))
		storedHash := v.tokenHash.Expose()

		if subtle.ConstantTimeCompare(suppliedHash[:], storedHash[:]) != 1 {
			w.Header().Set("WWW-Authenticate", "Bearer")
			w.WriteHeader(http.StatusUnauthorized)
			return
		}

		// Auth passed — call next without modifying the request or response.
		next.ServeHTTP(w, r)
	})
}
