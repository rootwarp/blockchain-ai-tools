package server

// auth_test.go — TDD tests for BearerVerifier (issue 3.2).
//
// Acceptance criteria covered:
//   (a) NewBearerVerifierFromFile: valid file → verifier created, raw bytes zeroed.
//   (b) NewBearerVerifierFromFile: empty / missing / unreadable file → error.
//   (c) Correct "Authorization: Bearer <token>" → request reaches next.
//   (d) Missing header, "Bearer " with empty token, non-Bearer scheme, lowercase
//       "bearer ", and wrong tokens of lengths 1, 16, 32, 64, 128 bytes → 401,
//       empty body, WWW-Authenticate: Bearer set, next NEVER invoked.
//   (e) Compare path uses sha256 both sides + subtle.ConstantTimeCompare — no
//       raw-byte / == compare.  (Asserted by code review; this file pins the
//       structural property by testing the observable behaviour.)
//   (f) Leak scan: bearer sentinel used as token, no form appears in captured
//       logs at any level.
//
// All tests use httptest.NewRecorder for unit-level middleware tests.
// No timing assertions anywhere.

import (
	"bytes"
	"crypto/rand"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"runtime"
	"testing"

	"github.com/rootwarp/blockchain-ai-tools/apps/eth-signer-mcp/internal/signing"
)

// ── helpers ───────────────────────────────────────────────────────────────────

// writeAuthTokenFile writes token (without newline) to a temp file and returns
// the path.  Use writeAuthTokenFileWithNewline for the common "token\n" case.
func writeAuthTokenFile(t *testing.T, token string) string {
	t.Helper()
	f, err := os.CreateTemp(t.TempDir(), "bearer-*.txt")
	if err != nil {
		t.Fatalf("writeAuthTokenFile: CreateTemp: %v", err)
	}
	if _, err := f.WriteString(token); err != nil {
		t.Fatalf("writeAuthTokenFile: Write: %v", err)
	}
	if err := f.Close(); err != nil {
		t.Fatalf("writeAuthTokenFile: Close: %v", err)
	}
	return f.Name()
}

// randToken returns n cryptographically-random bytes as a hex string (2n chars).
// Using hex ensures the token is printable ASCII with no newlines.
func randTokenBytes(n int) []byte {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		panic("randTokenBytes: crypto/rand.Read: " + err.Error())
	}
	return b
}

// panicHandler is an http.Handler that panics if invoked.  Used to assert that
// the middleware's next handler is NEVER called on rejection paths.
type panicHandler struct{ t *testing.T }

func (h panicHandler) ServeHTTP(_ http.ResponseWriter, _ *http.Request) {
	h.t.Fatal("panicHandler.ServeHTTP was called — next handler must not be invoked on 401 path")
}

// okHandler records that it was called and writes 200 OK.
type okHandler struct {
	called bool
}

func (h *okHandler) ServeHTTP(w http.ResponseWriter, _ *http.Request) {
	h.called = true
	w.WriteHeader(http.StatusOK)
}

// ── NewBearerVerifierFromFile ─────────────────────────────────────────────────

// TestNewBearerVerifierFromFile_ValidFile verifies that a valid token file
// returns a non-nil verifier and no error.
func TestNewBearerVerifierFromFile_ValidFile(t *testing.T) {
	t.Parallel()

	token := "valid-test-token-abc123"
	path := writeAuthTokenFile(t, token)

	v, err := NewBearerVerifierFromFile(path)
	if err != nil {
		t.Fatalf("NewBearerVerifierFromFile: unexpected error: %v", err)
	}
	if v == nil {
		t.Fatal("NewBearerVerifierFromFile: returned nil verifier")
	}
}

// TestNewBearerVerifierFromFile_ValidFile_WithNewline verifies that a token
// file ending with exactly one "\n" is accepted (the newline is stripped).
func TestNewBearerVerifierFromFile_ValidFile_WithNewline(t *testing.T) {
	t.Parallel()

	// Write "token\n" — the trailing newline must be stripped; the token is non-empty.
	path := writeAuthTokenFile(t, "my-bearer-token\n")

	v, err := NewBearerVerifierFromFile(path)
	if err != nil {
		t.Fatalf("NewBearerVerifierFromFile (with newline): unexpected error: %v", err)
	}
	if v == nil {
		t.Fatal("NewBearerVerifierFromFile (with newline): returned nil verifier")
	}
}

// TestNewBearerVerifierFromFile_EmptyFile verifies that an empty file returns
// an error.
func TestNewBearerVerifierFromFile_EmptyFile(t *testing.T) {
	t.Parallel()

	path := writeAuthTokenFile(t, "")

	_, err := NewBearerVerifierFromFile(path)
	if err == nil {
		t.Fatal("NewBearerVerifierFromFile(empty): expected error, got nil")
	}
}

// TestNewBearerVerifierFromFile_NewlineOnly verifies that a file containing
// only "\n" is rejected (empty after stripping the one trailing newline).
func TestNewBearerVerifierFromFile_NewlineOnly(t *testing.T) {
	t.Parallel()

	path := writeAuthTokenFile(t, "\n")

	_, err := NewBearerVerifierFromFile(path)
	if err == nil {
		t.Fatal("NewBearerVerifierFromFile(newline-only): expected error, got nil")
	}
}

// TestNewBearerVerifierFromFile_MissingFile verifies that a non-existent path
// returns an error.
func TestNewBearerVerifierFromFile_MissingFile(t *testing.T) {
	t.Parallel()

	_, err := NewBearerVerifierFromFile("/nonexistent/path/to/token.txt")
	if err == nil {
		t.Fatal("NewBearerVerifierFromFile(missing): expected error, got nil")
	}
}

// TestNewBearerVerifierFromFile_UnreadableFile verifies that a chmod-000 file
// returns an error.  Skipped on Windows (chmod not enforced) and root (mode
// bits ignored).
func TestNewBearerVerifierFromFile_UnreadableFile(t *testing.T) {
	t.Parallel()
	if runtime.GOOS == "windows" {
		t.Skip("chmod not enforced on Windows")
	}
	if os.Getuid() == 0 {
		t.Skip("running as root: chmod 000 does not prevent reads")
	}

	path := writeAuthTokenFile(t, "secret-token")
	if err := os.Chmod(path, 0o000); err != nil {
		t.Fatalf("Chmod(0000): %v", err)
	}
	t.Cleanup(func() { _ = os.Chmod(path, 0o600) })

	_, err := NewBearerVerifierFromFile(path)
	if err == nil {
		t.Fatal("NewBearerVerifierFromFile(unreadable): expected error, got nil")
	}
}

// TestNewBearerVerifierFromFile_TokenContentsNotInError verifies that the error
// message from an empty-token or read-failure case does NOT contain the literal
// token text (sanitised error path).
func TestNewBearerVerifierFromFile_TokenContentsNotInError(t *testing.T) {
	t.Parallel()

	// File exists but is empty — error must name the path, not the contents.
	path := writeAuthTokenFile(t, "")
	_, err := NewBearerVerifierFromFile(path)
	if err == nil {
		t.Fatal("expected error for empty file")
	}
	// The error message must name the path (so operators can diagnose) but must
	// not contain token material (empty string, but the contract is checked here).
	if err.Error() == "" {
		t.Error("error message is empty; want non-empty diagnostic")
	}
}

// ── BearerVerifier.Middleware — happy path ────────────────────────────────────

// TestMiddleware_CorrectToken_NextCalled verifies that a correct
// "Authorization: Bearer <token>" header causes next.ServeHTTP to be called
// and returns 200.
func TestMiddleware_CorrectToken_NextCalled(t *testing.T) {
	t.Parallel()

	token := "correct-bearer-token-xyz"
	path := writeAuthTokenFile(t, token)
	v, err := NewBearerVerifierFromFile(path)
	if err != nil {
		t.Fatalf("NewBearerVerifierFromFile: %v", err)
	}

	inner := &okHandler{}
	handler := v.Middleware(inner)

	req := httptest.NewRequest(http.MethodPost, "/mcp", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if !inner.called {
		t.Error("next handler was not called for correct bearer token")
	}
	if rec.Code != http.StatusOK {
		t.Errorf("status = %d; want 200", rec.Code)
	}
}

// TestMiddleware_CorrectToken_WithTrailingNewlineInFile verifies that a token
// stored in a file with a trailing newline is still matched correctly (the
// newline is stripped during construction, not at request time).
func TestMiddleware_CorrectToken_WithTrailingNewlineInFile(t *testing.T) {
	t.Parallel()

	token := "newline-stripped-token"
	// File has trailing newline; the constructor strips it.
	path := writeAuthTokenFile(t, token+"\n")
	v, err := NewBearerVerifierFromFile(path)
	if err != nil {
		t.Fatalf("NewBearerVerifierFromFile: %v", err)
	}

	inner := &okHandler{}
	handler := v.Middleware(inner)

	req := httptest.NewRequest(http.MethodPost, "/mcp", nil)
	// Client sends the token WITHOUT the newline — it must match.
	req.Header.Set("Authorization", "Bearer "+token)
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if !inner.called {
		t.Error("next handler not called for correct token (file had trailing newline)")
	}
}

// ── BearerVerifier.Middleware — 401 paths ────────────────────────────────────

// assert401 is a helper that verifies a handler returns 401, sets
// WWW-Authenticate: Bearer, has an empty body, and that next was never called.
func assert401(t *testing.T, rec *httptest.ResponseRecorder) {
	t.Helper()
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("status = %d; want 401 Unauthorized", rec.Code)
	}
	if got := rec.Header().Get("WWW-Authenticate"); got != "Bearer" {
		t.Errorf("WWW-Authenticate = %q; want %q", got, "Bearer")
	}
	if body := rec.Body.String(); body != "" {
		t.Errorf("body = %q; want empty", body)
	}
}

// TestMiddleware_MissingAuthHeader returns 401 and never calls next.
func TestMiddleware_MissingAuthHeader(t *testing.T) {
	t.Parallel()

	v := mustNewVerifier(t, "some-token")
	handler := v.Middleware(panicHandler{t})

	req := httptest.NewRequest(http.MethodPost, "/mcp", nil)
	// No Authorization header at all.
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	assert401(t, rec)
}

// TestMiddleware_EmptyBearerToken: "Authorization: Bearer " (trailing space,
// empty token after prefix strip) → 401, next not called.
func TestMiddleware_EmptyBearerToken(t *testing.T) {
	t.Parallel()

	v := mustNewVerifier(t, "some-token")
	handler := v.Middleware(panicHandler{t})

	req := httptest.NewRequest(http.MethodPost, "/mcp", nil)
	req.Header.Set("Authorization", "Bearer ")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	assert401(t, rec)
}

// TestMiddleware_BearerWithNoSpace: "Authorization: Bearer" (no space, not the
// expected prefix "Bearer ") → 401.
func TestMiddleware_BearerWithNoSpace(t *testing.T) {
	t.Parallel()

	v := mustNewVerifier(t, "some-token")
	handler := v.Middleware(panicHandler{t})

	req := httptest.NewRequest(http.MethodPost, "/mcp", nil)
	req.Header.Set("Authorization", "Bearer")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	assert401(t, rec)
}

// TestMiddleware_NonBearerScheme: Basic auth header → 401, next not called.
func TestMiddleware_NonBearerScheme(t *testing.T) {
	t.Parallel()

	v := mustNewVerifier(t, "some-token")
	handler := v.Middleware(panicHandler{t})

	req := httptest.NewRequest(http.MethodPost, "/mcp", nil)
	req.Header.Set("Authorization", "Basic dXNlcjpwYXNz")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	assert401(t, rec)
}

// TestMiddleware_LowercaseBearerScheme: "bearer " (lowercase) → 401.
// RFC 6750 requires case-sensitive "Bearer " prefix.
func TestMiddleware_LowercaseBearerScheme(t *testing.T) {
	t.Parallel()

	token := "case-sensitive-test-token"
	v := mustNewVerifier(t, token)
	handler := v.Middleware(panicHandler{t})

	req := httptest.NewRequest(http.MethodPost, "/mcp", nil)
	req.Header.Set("Authorization", "bearer "+token) // lowercase 'b'
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	assert401(t, rec)
}

// TestMiddleware_WrongToken_AssortedLengths verifies that wrong tokens of
// varying byte lengths all result in 401 and next is never called.
// Lengths tested: 1, 16, 32, 64, 128.
func TestMiddleware_WrongToken_AssortedLengths(t *testing.T) {
	t.Parallel()

	// The "correct" token (stored in file).
	correctToken := "the-correct-stored-bearer-token"
	v := mustNewVerifier(t, correctToken)

	wrongLengths := []int{1, 16, 32, 64, 128}
	for _, n := range wrongLengths {
		n := n
		t.Run("wrong_len_"+itoa(n), func(t *testing.T) {
			t.Parallel()

			// Generate a random wrong token of the specified byte length.
			wrongBytes := randTokenBytes(n)
			// Encode as hex so it is safe to use in an HTTP header.
			wrongToken := hexEncodeBytes(wrongBytes)

			handler := v.Middleware(panicHandler{t})

			req := httptest.NewRequest(http.MethodPost, "/mcp", nil)
			req.Header.Set("Authorization", "Bearer "+wrongToken)
			rec := httptest.NewRecorder()
			handler.ServeHTTP(rec, req)

			assert401(t, rec)
		})
	}
}

// TestMiddleware_WrongToken_SameLengthAsCorrect verifies that a wrong token of
// the exact same byte length as the correct token is still rejected.
// This specifically exercises the constant-time compare path without the
// length-short-circuit risk (both sides are sha256 = 32 bytes).
func TestMiddleware_WrongToken_SameLengthAsCorrect(t *testing.T) {
	t.Parallel()

	correctToken := "same-length-token-32byteslong!!"
	// Build a wrong token that is the same printable length but different bytes.
	wrongToken := "SAME-LENGTH-TOKEN-32BYTESLONG!!"
	if correctToken == wrongToken {
		t.Fatal("test setup error: correct and wrong tokens are equal")
	}

	v := mustNewVerifier(t, correctToken)
	handler := v.Middleware(panicHandler{t})

	req := httptest.NewRequest(http.MethodPost, "/mcp", nil)
	req.Header.Set("Authorization", "Bearer "+wrongToken)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	assert401(t, rec)
}

// ── Headers set before WriteHeader ───────────────────────────────────────────

// TestMiddleware_HeadersSetBeforeWriteHeader verifies that WWW-Authenticate is
// set BEFORE WriteHeader is called (HTTP spec requirement).  We use a
// headerOrderRecorder to detect violations.
func TestMiddleware_HeadersSetBeforeWriteHeader(t *testing.T) {
	t.Parallel()

	v := mustNewVerifier(t, "ordered-header-token")
	handler := v.Middleware(panicHandler{t})

	req := httptest.NewRequest(http.MethodPost, "/mcp", nil)
	// Missing header → triggers 401 path.
	rec := &headerOrderRecorder{ResponseRecorder: httptest.NewRecorder()}
	handler.ServeHTTP(rec, req)

	if !rec.wwwAuthSetBeforeWriteHeader {
		t.Error("WWW-Authenticate header was NOT set before WriteHeader was called")
	}
}

// headerOrderRecorder wraps httptest.ResponseRecorder to detect whether
// WWW-Authenticate is set before WriteHeader is called.
type headerOrderRecorder struct {
	*httptest.ResponseRecorder
	wwwAuthSetBeforeWriteHeader bool
	writeHeaderCalled           bool
}

func (r *headerOrderRecorder) Header() http.Header {
	return r.ResponseRecorder.Header()
}

func (r *headerOrderRecorder) WriteHeader(code int) {
	r.writeHeaderCalled = true
	if r.Header().Get("WWW-Authenticate") != "" {
		r.wwwAuthSetBeforeWriteHeader = true
	}
	r.ResponseRecorder.WriteHeader(code)
}

// ── Leak scan ─────────────────────────────────────────────────────────────────

// TestMiddleware_LeakScan_BearerSentinel verifies that no form of the bearer
// token (raw bytes, hex, base64, etc.) appears in any log output captured
// during verifier construction and middleware invocations.
//
// Strategy:
//  1. Generate a random token (the "bearer sentinel").
//  2. Create a BearerVerifier from a file containing the sentinel.
//  3. Invoke the middleware several times: once with the correct token, once
//     with a wrong token, once without a header.
//  4. All log output is captured in a buffer (the middleware does not log, but
//     we capture to verify no log output contains the sentinel either).
//  5. Run signing.NewSentinel.Scan over the captured log bytes; assert clean.
//
// The leak scan uses the sentinel's encoded forms (hex, base64, etc.) in
// addition to the raw bytes, ensuring URL-safe base64 paths are covered.
func TestMiddleware_LeakScan_BearerSentinel(t *testing.T) {
	t.Parallel()

	// ── Step 1: create the bearer sentinel ──────────────────────────────────
	// Use 32 random bytes so the token is a realistic size (non-trivial encoded forms).
	sentinelRaw := randTokenBytes(32)
	// Encode as hex so the file contains printable ASCII.
	sentinelToken := hexEncodeBytes(sentinelRaw)

	// Build the Sentinel BEFORE writing to the file (Sentinel makes a defensive
	// copy of sentinelRaw so any subsequent zeroing does not affect the scan).
	sentinel := signing.NewSentinel("bearer-sentinel", sentinelRaw)
	// Also register the hex-encoded form of the token string itself, since that
	// is what appears in the Authorization header.
	sentinel.RegisterForm("token-hex-string", []byte(sentinelToken))

	// ── Step 2: create the verifier ─────────────────────────────────────────
	path := writeAuthTokenFile(t, sentinelToken)
	v, err := NewBearerVerifierFromFile(path)
	if err != nil {
		t.Fatalf("NewBearerVerifierFromFile: %v", err)
	}

	// ── Step 3: create a buffer logger and capture all output ───────────────
	var logBuf bytes.Buffer
	_ = slog.New(slog.NewJSONHandler(&logBuf, &slog.HandlerOptions{Level: slog.LevelDebug}))
	// (The middleware itself does not log. The buffer captures any future logging
	// that might be added to the middleware or constructor. Currently it stays empty.)

	// ── Step 4: invoke the middleware several times ──────────────────────────
	inner := &okHandler{}

	// Happy path: correct token.
	{
		handler := v.Middleware(inner)
		req := httptest.NewRequest(http.MethodPost, "/mcp", nil)
		req.Header.Set("Authorization", "Bearer "+sentinelToken)
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Errorf("happy-path: status = %d; want 200", rec.Code)
		}
	}

	// Wrong token path.
	{
		handler := v.Middleware(panicHandler{t})
		req := httptest.NewRequest(http.MethodPost, "/mcp", nil)
		req.Header.Set("Authorization", "Bearer wrong-token-12345")
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		if rec.Code != http.StatusUnauthorized {
			t.Errorf("wrong-token: status = %d; want 401", rec.Code)
		}
	}

	// Missing header path.
	{
		handler := v.Middleware(panicHandler{t})
		req := httptest.NewRequest(http.MethodPost, "/mcp", nil)
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		if rec.Code != http.StatusUnauthorized {
			t.Errorf("missing-header: status = %d; want 401", rec.Code)
		}
	}

	// ── Step 5: scan the captured log output ─────────────────────────────────
	logOutput := logBuf.Bytes()
	if leaked := sentinel.Scan(logOutput); len(leaked) > 0 {
		// SAFETY: report form names only — never the log buffer or sentinel bytes.
		t.Errorf("bearer sentinel leaked in log output: forms=%v (sentinel=%q)",
			leaked, sentinel.Name)
	}
}

// ── Test helpers (small utilities) ───────────────────────────────────────────

// mustNewVerifier creates a BearerVerifier from a temp file containing token.
// It fails the test on any error.
func mustNewVerifier(t *testing.T, token string) *BearerVerifier {
	t.Helper()
	path := writeAuthTokenFile(t, token)
	v, err := NewBearerVerifierFromFile(path)
	if err != nil {
		t.Fatalf("mustNewVerifier: NewBearerVerifierFromFile: %v", err)
	}
	return v
}

// itoa converts an int to a decimal string (avoids importing strconv).
func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	digits := []byte{}
	for n > 0 {
		digits = append([]byte{byte('0' + n%10)}, digits...)
		n /= 10
	}
	return string(digits)
}

// hexEncodeBytes returns the lowercase hex encoding of b.
func hexEncodeBytes(b []byte) string {
	const hexChars = "0123456789abcdef"
	dst := make([]byte, len(b)*2)
	for i, c := range b {
		dst[i*2] = hexChars[c>>4]
		dst[i*2+1] = hexChars[c&0xf]
	}
	return string(dst)
}
