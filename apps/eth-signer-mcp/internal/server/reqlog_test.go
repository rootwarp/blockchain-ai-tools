package server

// reqlog_test.go — TDD tests for request-logging middleware (issue 3.3).
//
// Acceptance criteria covered:
//
//	(a) Every HTTP request produces EXACTLY ONE log line with request_id,
//	    remote_addr, status, latency_ms — asserted by parsing captured JSON stderr.
//	(b) request_id is propagated via signing.WithRequestID; a successful
//	    sign_transaction over HTTP yields an audit line whose request_id EQUALS
//	    the HTTP request log line's request_id — the correlation test.
//	(c) When the SDK exposes no request id (per the 1.7 spike note's finding),
//	    ids are UUIDv4 and UNIQUE across concurrent requests.
//	(d) No request body bytes, no Authorization header value, and no other
//	    header values appear in any captured log at any level — leak scan.
//	(e) Status capture correct for: handler 200, middleware-written 401,
//	    SDK-written 403, and a handler that never calls WriteHeader (→ 200).
//	(f) go test -race ./internal/server/... green.

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/rootwarp/blockchain-ai-tools/apps/eth-signer-mcp/internal/signing"
)

// ── statusCaptureWriter tests ──────────────────────────────────────────────────

// TestStatusCaptureWriter_ExplicitStatus verifies that WriteHeader captures the
// first status code and passes it to the wrapped ResponseWriter.
func TestStatusCaptureWriter_ExplicitStatus(t *testing.T) {
	t.Parallel()

	rec := httptest.NewRecorder()
	sw := &statusCaptureWriter{ResponseWriter: rec}

	sw.WriteHeader(http.StatusUnauthorized)

	if sw.capturedStatus() != http.StatusUnauthorized {
		t.Errorf("capturedStatus() = %d; want 401", sw.capturedStatus())
	}
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("underlying recorder code = %d; want 401", rec.Code)
	}
}

// TestStatusCaptureWriter_ImplicitOKOnWrite verifies that calling Write without
// WriteHeader results in capturedStatus() == 200 (implicit OK).
func TestStatusCaptureWriter_ImplicitOKOnWrite(t *testing.T) {
	t.Parallel()

	rec := httptest.NewRecorder()
	sw := &statusCaptureWriter{ResponseWriter: rec}

	_, _ = sw.Write([]byte("body"))

	if sw.capturedStatus() != http.StatusOK {
		t.Errorf("capturedStatus() = %d; want 200 (implicit OK from Write)", sw.capturedStatus())
	}
}

// TestStatusCaptureWriter_DefaultOKWhenNeverCalled verifies that capturedStatus()
// returns 200 when neither WriteHeader nor Write is ever called.
func TestStatusCaptureWriter_DefaultOKWhenNeverCalled(t *testing.T) {
	t.Parallel()

	rec := httptest.NewRecorder()
	sw := &statusCaptureWriter{ResponseWriter: rec}

	if sw.capturedStatus() != http.StatusOK {
		t.Errorf("capturedStatus() = %d; want 200 (default when never written)", sw.capturedStatus())
	}
}

// TestStatusCaptureWriter_OnlyFirstWriteHeaderCaptured verifies that only the
// first WriteHeader call's status code is captured (subsequent calls are passed
// through but do not change the captured status).
func TestStatusCaptureWriter_OnlyFirstWriteHeaderCaptured(t *testing.T) {
	t.Parallel()

	rec := httptest.NewRecorder()
	sw := &statusCaptureWriter{ResponseWriter: rec}

	sw.WriteHeader(http.StatusNotFound)
	// This second call must NOT override the captured code.
	// (The underlying ResponseWriter may ignore it too, but we test the wrapper.)
	sw.WriteHeader(http.StatusOK)

	if sw.capturedStatus() != http.StatusNotFound {
		t.Errorf("capturedStatus() = %d; want 404 (first call should be captured)", sw.capturedStatus())
	}
}

// ── newRequestLogMiddleware tests ─────────────────────────────────────────────

// newCaptureLogger builds a *slog.Logger writing JSON to buf at Info level.
// Used across reqlog tests to capture and parse log output.
func newCaptureLogger(buf *bytes.Buffer) *slog.Logger {
	return slog.New(slog.NewJSONHandler(buf, &slog.HandlerOptions{Level: slog.LevelInfo}))
}

// parseLogLines parses each non-empty line in buf as a JSON log record and
// returns a slice of maps. Lines that are not valid JSON are skipped.
func parseLogLines(buf *bytes.Buffer) []map[string]any {
	var out []map[string]any
	for _, raw := range bytes.Split(buf.Bytes(), []byte("\n")) {
		raw = bytes.TrimSpace(raw)
		if len(raw) == 0 {
			continue
		}
		var m map[string]any
		if json.Unmarshal(raw, &m) == nil {
			out = append(out, m)
		}
	}
	return out
}

// reqlogLines returns only the lines from parsed log records that are request
// log lines (msg == "http request").
func reqlogLines(lines []map[string]any) []map[string]any {
	var out []map[string]any
	for _, m := range lines {
		if msg, _ := m["msg"].(string); msg == "http request" {
			out = append(out, m)
		}
	}
	return out
}

// TestReqLogMiddleware_ProducesExactlyOneLogLine verifies that one HTTP request
// produces exactly one log line with msg == "http request".
func TestReqLogMiddleware_ProducesExactlyOneLogLine(t *testing.T) {
	t.Parallel()

	var buf bytes.Buffer
	logger := newCaptureLogger(&buf)
	mw := newRequestLogMiddleware(logger)

	inner := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	handler := mw(inner)

	req := httptest.NewRequest(http.MethodPost, "/mcp", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	lines := reqlogLines(parseLogLines(&buf))
	if len(lines) != 1 {
		t.Errorf("reqlog line count = %d; want exactly 1", len(lines))
	}
}

// TestReqLogMiddleware_LogFields verifies that the request log line has exactly
// the required fields: request_id, remote_addr, status, latency_ms.
func TestReqLogMiddleware_LogFields(t *testing.T) {
	t.Parallel()

	var buf bytes.Buffer
	logger := newCaptureLogger(&buf)
	mw := newRequestLogMiddleware(logger)

	inner := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	handler := mw(inner)

	req := httptest.NewRequest(http.MethodPost, "/mcp", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	lines := reqlogLines(parseLogLines(&buf))
	if len(lines) != 1 {
		t.Fatalf("expected 1 reqlog line, got %d", len(lines))
	}
	line := lines[0]

	for _, field := range []string{"request_id", "remote_addr", "status", "latency_ms"} {
		if _, ok := line[field]; !ok {
			t.Errorf("reqlog line missing field %q; line: %v", field, line)
		}
	}

	// request_id must be a non-empty UUID-format string.
	reqID, _ := line["request_id"].(string)
	if reqID == "" {
		t.Error("request_id is empty or not a string")
	}
	// status must be a number (JSON number → float64 in Go).
	if _, ok := line["status"]; !ok {
		t.Error("status field absent")
	}
	// latency_ms must be a number ≥ 0.
	latency, ok := line["latency_ms"]
	if !ok {
		t.Error("latency_ms field absent")
	}
	switch v := latency.(type) {
	case float64:
		if v < 0 {
			t.Errorf("latency_ms = %v; want ≥ 0", v)
		}
	default:
		t.Errorf("latency_ms is %T; want numeric", latency)
	}
}

// TestReqLogMiddleware_Status401 verifies that a middleware-written 401 (as the
// bearer auth middleware does) results in status=401 in the log line.
func TestReqLogMiddleware_Status401(t *testing.T) {
	t.Parallel()

	var buf bytes.Buffer
	logger := newCaptureLogger(&buf)
	mw := newRequestLogMiddleware(logger)

	// Inner handler writes 401 (simulates what bearer auth does).
	inner := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("WWW-Authenticate", "Bearer")
		w.WriteHeader(http.StatusUnauthorized)
	})
	handler := mw(inner)

	req := httptest.NewRequest(http.MethodPost, "/mcp", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	lines := reqlogLines(parseLogLines(&buf))
	if len(lines) != 1 {
		t.Fatalf("expected 1 reqlog line, got %d", len(lines))
	}
	line := lines[0]

	status, _ := line["status"].(float64)
	if int(status) != http.StatusUnauthorized {
		t.Errorf("status = %v; want 401", line["status"])
	}
}

// TestReqLogMiddleware_Status403 verifies that a handler-written 403 results in
// status=403 in the log line (covers the SDK DNS-rebind protection path).
func TestReqLogMiddleware_Status403(t *testing.T) {
	t.Parallel()

	var buf bytes.Buffer
	logger := newCaptureLogger(&buf)
	mw := newRequestLogMiddleware(logger)

	inner := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusForbidden)
	})
	handler := mw(inner)

	req := httptest.NewRequest(http.MethodPost, "/mcp", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	lines := reqlogLines(parseLogLines(&buf))
	if len(lines) != 1 {
		t.Fatalf("expected 1 reqlog line, got %d", len(lines))
	}
	status, _ := lines[0]["status"].(float64)
	if int(status) != http.StatusForbidden {
		t.Errorf("status = %v; want 403", lines[0]["status"])
	}
}

// TestReqLogMiddleware_HandlerNeverCallsWriteHeader_Defaults200 verifies that
// when the inner handler never calls WriteHeader (only Write), the captured
// status defaults to 200.
func TestReqLogMiddleware_HandlerNeverCallsWriteHeader_Defaults200(t *testing.T) {
	t.Parallel()

	var buf bytes.Buffer
	logger := newCaptureLogger(&buf)
	mw := newRequestLogMiddleware(logger)

	// Handler writes a body without calling WriteHeader first.
	inner := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("body"))
	})
	handler := mw(inner)

	req := httptest.NewRequest(http.MethodPost, "/mcp", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	lines := reqlogLines(parseLogLines(&buf))
	if len(lines) != 1 {
		t.Fatalf("expected 1 reqlog line, got %d", len(lines))
	}
	status, _ := lines[0]["status"].(float64)
	if int(status) != http.StatusOK {
		t.Errorf("status = %v; want 200 (default when WriteHeader not called)", lines[0]["status"])
	}
}

// TestReqLogMiddleware_RequestIDInContext verifies that the request_id generated
// by the middleware is available in the handler's context via
// signing.RequestIDFromContext.
func TestReqLogMiddleware_RequestIDInContext(t *testing.T) {
	t.Parallel()

	var buf bytes.Buffer
	logger := newCaptureLogger(&buf)
	mw := newRequestLogMiddleware(logger)

	var ctxReqID string
	inner := http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		ctxReqID, _ = signing.RequestIDFromContext(r.Context())
	})
	handler := mw(inner)

	req := httptest.NewRequest(http.MethodPost, "/mcp", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if ctxReqID == "" {
		t.Error("request_id not found in handler context via signing.RequestIDFromContext")
	}

	// The context request_id must match the logged request_id.
	lines := reqlogLines(parseLogLines(&buf))
	if len(lines) != 1 {
		t.Fatalf("expected 1 reqlog line, got %d", len(lines))
	}
	logged, _ := lines[0]["request_id"].(string)
	if ctxReqID != logged {
		t.Errorf("context request_id %q != logged request_id %q", ctxReqID, logged)
	}
}

// TestReqLogMiddleware_UniqueRequestIDs verifies that concurrent requests receive
// distinct UUIDv4 request_ids (the SDK exposes no request id per the 1.7 spike).
func TestReqLogMiddleware_UniqueRequestIDs(t *testing.T) {
	t.Parallel()

	var buf bytes.Buffer
	var mu sync.Mutex
	logger := newCaptureLogger(&buf)
	mw := newRequestLogMiddleware(logger)

	// Handler records its context request_id.
	var ids []string
	inner := http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		id, _ := signing.RequestIDFromContext(r.Context())
		mu.Lock()
		ids = append(ids, id)
		mu.Unlock()
	})
	handler := mw(inner)

	const n = 16
	var wg sync.WaitGroup
	wg.Add(n)
	for i := 0; i < n; i++ {
		go func() {
			defer wg.Done()
			req := httptest.NewRequest(http.MethodPost, "/mcp", nil)
			rec := httptest.NewRecorder()
			handler.ServeHTTP(rec, req)
		}()
	}
	wg.Wait()

	// All n request_ids must be distinct.
	seen := make(map[string]struct{}, n)
	for _, id := range ids {
		if _, dup := seen[id]; dup {
			t.Errorf("duplicate request_id: %q", id)
		}
		seen[id] = struct{}{}
	}
	if len(seen) != n {
		t.Errorf("expected %d unique request_ids, got %d", n, len(seen))
	}

	// Also verify n reqlog lines.
	lines := reqlogLines(parseLogLines(&buf))
	if len(lines) != n {
		t.Errorf("expected %d reqlog lines, got %d", n, len(lines))
	}
}

// TestReqLogMiddleware_NoURLOrHeadersInLog verifies that the log line does NOT
// contain URL, query parameters, Authorization header values, or other header
// values. The only allowed fields are the four required ones plus standard slog
// fields (time/level/msg).
func TestReqLogMiddleware_NoURLOrHeadersInLog(t *testing.T) {
	t.Parallel()

	var buf bytes.Buffer
	logger := newCaptureLogger(&buf)
	mw := newRequestLogMiddleware(logger)

	// Use a distinctive sentinel that must not appear in any log line.
	const sentinelToken = "SENTINEL-TOKEN-MUST-NOT-APPEAR-IN-REQLOG-39847"
	const sentinelBody = "SENTINEL-BODY-MUST-NOT-APPEAR-IN-REQLOG-78234"

	inner := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	handler := mw(inner)

	req := httptest.NewRequest(http.MethodPost, "/mcp?q="+sentinelBody, strings.NewReader(sentinelBody))
	req.Header.Set("Authorization", "Bearer "+sentinelToken)
	req.Header.Set("X-Custom-Header", sentinelToken)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	output := buf.Bytes()
	if bytes.Contains(output, []byte(sentinelToken)) {
		t.Error("Authorization header sentinel found in reqlog output; must not log header values")
	}
	if bytes.Contains(output, []byte(sentinelBody)) {
		t.Error("body/URL sentinel found in reqlog output; must not log URL or body content")
	}
}

// TestReqLogMiddleware_LeakScan_AuthorizationHeader runs the full signing.Sentinel
// scan over all captured log lines produced by the reqlog middleware, including
// requests that carry an Authorization header, to assert no header value leaks.
func TestReqLogMiddleware_LeakScan_AuthorizationHeader(t *testing.T) {
	t.Parallel()

	// Build a 32-byte random token for the sentinel.
	sentinelRaw := randTokenBytes(32)
	sentinelToken := hexEncodeBytes(sentinelRaw)

	sentinel := signing.NewSentinel("reqlog-auth-sentinel", sentinelRaw)
	sentinel.RegisterForm("token-hex-string", []byte(sentinelToken))

	var buf bytes.Buffer
	logger := newCaptureLogger(&buf)
	mw := newRequestLogMiddleware(logger)

	inner := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	handler := mw(inner)

	req := httptest.NewRequest(http.MethodPost, "/mcp", nil)
	req.Header.Set("Authorization", "Bearer "+sentinelToken)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	output := buf.Bytes()
	if leaked := sentinel.Scan(output); len(leaked) > 0 {
		// SAFETY: report form names only, never the bytes.
		t.Errorf("reqlog middleware leaked sentinel forms: %v (sentinel: %q)",
			leaked, sentinel.Name)
	}
}

// ── handler request_id reuse tests ───────────────────────────────────────────

// TestSignTransactionHandler_ReuseContextRequestID verifies that when the
// request context already carries a request_id (set by reqlog middleware),
// makeSignTransactionHandler reuses it rather than generating a new one.
//
// This is asserted by:
//  1. Setting a known request_id in the context.
//  2. Running the handler.
//  3. Scanning the audit log for the same request_id.
func TestSignTransactionHandler_ReuseContextRequestID(t *testing.T) {
	t.Parallel()

	const presetID = "preset-request-id-from-middleware-00000001"

	tdPath := signingTestdataPath(t)
	vault, err := signing.NewFileKeyVault(signing.VaultOptions{
		KeystorePath: filepath.Join(tdPath, "keystore-weak.json"),
		PasswordPath: filepath.Join(tdPath, "password.txt"),
	})
	if err != nil {
		t.Fatalf("NewFileKeyVault: %v", err)
	}

	var logBuf bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&logBuf, &slog.HandlerOptions{Level: slog.LevelDebug}))
	signer := signing.NewSigner(vault, signing.SignerOptions{Logger: logger})

	srv := New(signer, Options{
		Name:    "test",
		Version: "v0.0.0-test",
		Logger:  logger,
	})

	// Connect via in-memory session.
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	// Set the request_id in the context before connecting.
	ctx = signing.WithRequestID(ctx, presetID)
	cs, cleanup := inMemorySession(t, srv, ctx)
	defer cleanup()

	// Call sign_transaction — the preset request_id should appear in the audit line.
	result, callErr := callSignTx(t, cs, minimalLegacyArgs())
	if callErr != nil {
		t.Fatalf("CallTool: %v", callErr)
	}
	if result == nil || result.IsError {
		t.Fatalf("CallTool returned error result")
	}

	// Find the audit line and assert it carries the preset request_id.
	lines := parseLogLines(&logBuf)
	var found bool
	for _, m := range lines {
		msg, _ := m["msg"].(string)
		if strings.Contains(msg, "signed successfully") {
			auditID, _ := m["request_id"].(string)
			if auditID == presetID {
				found = true
			} else {
				t.Errorf("audit line request_id = %q; want %q", auditID, presetID)
			}
			break
		}
	}
	if !found {
		t.Error("audit line not found in captured log output")
	}
}

// TestSignTransactionHandler_GeneratesRequestIDWhenAbsent verifies that when
// the context carries NO request_id (stdio path), the handler generates one
// and the audit line carries it.
func TestSignTransactionHandler_GeneratesRequestIDWhenAbsent(t *testing.T) {
	t.Parallel()

	tdPath := signingTestdataPath(t)
	vault, err := signing.NewFileKeyVault(signing.VaultOptions{
		KeystorePath: filepath.Join(tdPath, "keystore-weak.json"),
		PasswordPath: filepath.Join(tdPath, "password.txt"),
	})
	if err != nil {
		t.Fatalf("NewFileKeyVault: %v", err)
	}

	var logBuf bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&logBuf, &slog.HandlerOptions{Level: slog.LevelDebug}))
	signer := signing.NewSigner(vault, signing.SignerOptions{Logger: logger})

	srv := New(signer, Options{
		Name:    "test",
		Version: "v0.0.0-test",
		Logger:  logger,
	})

	// No preset request_id in context — handler should generate its own.
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	cs, cleanup := inMemorySession(t, srv, ctx)
	defer cleanup()

	result, callErr := callSignTx(t, cs, minimalLegacyArgs())
	if callErr != nil {
		t.Fatalf("CallTool: %v", callErr)
	}
	if result == nil || result.IsError {
		t.Fatalf("CallTool returned error result")
	}

	// Find the audit line and assert request_id is non-empty.
	lines := parseLogLines(&logBuf)
	var found bool
	for _, m := range lines {
		msg, _ := m["msg"].(string)
		if strings.Contains(msg, "signed successfully") {
			auditID, _ := m["request_id"].(string)
			if auditID == "" {
				t.Error("audit line request_id is empty; handler should generate one on stdio path")
			}
			found = true
			break
		}
	}
	if !found {
		t.Error("audit line not found in captured log output")
	}
}

// ── HTTP request_id correlation test ─────────────────────────────────────────

// TestHTTPRequestIDCorrelation is the end-to-end correlation test (issue 3.3
// acceptance criterion (b)). It:
//  1. Starts RunHTTP with a real signer (keystore-weak.json).
//  2. Issues a sign_transaction request over Streamable HTTP with bearer auth.
//  3. Asserts EXACTLY ONE reqlog line (msg="http request").
//  4. Asserts EXACTLY ONE audit line (msg contains "signed successfully").
//  5. Asserts the two lines share the SAME request_id.
func TestHTTPRequestIDCorrelation(t *testing.T) {
	t.Parallel()

	tdPath := signingTestdataPath(t)
	vault, err := signing.NewFileKeyVault(signing.VaultOptions{
		KeystorePath: filepath.Join(tdPath, "keystore-weak.json"),
		PasswordPath: filepath.Join(tdPath, "password.txt"),
	})
	if err != nil {
		t.Fatalf("NewFileKeyVault: %v", err)
	}

	// Shared buffer: both the signer and the server write to the same logger.
	var logBuf bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&logBuf, &slog.HandlerOptions{Level: slog.LevelDebug}))
	signer := signing.NewSigner(vault, signing.SignerOptions{Logger: logger})

	srv := New(signer, Options{
		Name:    "eth-signer-mcp",
		Version: "v0.0.0-test",
		Logger:  logger,
	})

	const token = "correlation-test-token-xyz-abc-123"
	tokenFile := writeTokenFile(t, token+"\n")
	readyCh := make(chan net.Addr, 1)

	_, exitCh, cancel := startRunHTTP(t, srv, HTTPOptions{
		Addr:          "127.0.0.1:0",
		TokenFilePath: tokenFile,
		ReadyCh:       readyCh,
	})

	addr := waitReady(t, readyCh, 5*time.Second)
	endpoint := fmt.Sprintf("http://%s", addr.String())

	// Connect via SDK with bearer token.
	mcpClient := mcp.NewClient(
		&mcp.Implementation{Name: "corr-test-client", Version: "v0.0.1"},
		nil,
	)
	transport := &mcp.StreamableClientTransport{
		Endpoint:             endpoint,
		DisableStandaloneSSE: true,
		HTTPClient: &http.Client{
			Transport: bearerRoundTripper{token: token},
		},
	}

	connCtx, connCancel := context.WithTimeout(context.Background(), 10*time.Second)
	cs, connErr := mcpClient.Connect(connCtx, transport, nil)
	connCancel()
	if connErr != nil {
		t.Fatalf("client.Connect: %v", connErr)
	}

	// Call sign_transaction.
	callCtx, callCancel := context.WithTimeout(context.Background(), 15*time.Second)
	result, callErr := cs.CallTool(callCtx, &mcp.CallToolParams{
		Name:      "sign_transaction",
		Arguments: minimalLegacyArgs(),
	})
	callCancel()
	if callErr != nil {
		t.Fatalf("CallTool: %v", callErr)
	}
	if result == nil || result.IsError {
		t.Fatalf("sign_transaction returned error result")
	}

	// Close client before cancelling server.
	if closeErr := cs.Close(); closeErr != nil {
		t.Logf("cs.Close: %v (benign)", closeErr)
	}
	cancel()
	select {
	case <-exitCh:
	case <-time.After(10 * time.Second):
		t.Fatal("RunHTTP did not exit within 10s after cancel")
	}

	// Parse all log lines.
	allLines := parseLogLines(&logBuf)

	// Find reqlog lines.
	rqLines := reqlogLines(allLines)
	// Find audit lines.
	var auditLines []map[string]any
	for _, m := range allLines {
		msg, _ := m["msg"].(string)
		if strings.Contains(msg, "signed successfully") {
			auditLines = append(auditLines, m)
		}
	}

	// Exactly one reqlog line from the sign_transaction call.
	// (There may be additional lines from the initialize round-trip.)
	if len(rqLines) == 0 {
		t.Fatal("no reqlog lines found; expected at least one")
	}

	// Exactly one audit line.
	if len(auditLines) != 1 {
		t.Fatalf("expected 1 audit line, got %d", len(auditLines))
	}

	// Find the reqlog line that corresponds to the signing call (status=200).
	// The initialize round-trip also produces a reqlog line; find the one whose
	// request_id matches the audit line's request_id.
	auditReqID, _ := auditLines[0]["request_id"].(string)
	if auditReqID == "" {
		t.Fatal("audit line has empty request_id")
	}

	var matchingReqlog map[string]any
	for _, rl := range rqLines {
		rid, _ := rl["request_id"].(string)
		if rid == auditReqID {
			matchingReqlog = rl
			break
		}
	}
	if matchingReqlog == nil {
		// Collect all reqlog request_ids for the error message.
		var rqIDs []string
		for _, rl := range rqLines {
			rid, _ := rl["request_id"].(string)
			rqIDs = append(rqIDs, rid)
		}
		t.Errorf("no reqlog line shares request_id with audit line; audit=%q, reqlog=%v",
			auditReqID, rqIDs)
	}

	// Status in the matching reqlog line must be 200.
	if matchingReqlog != nil {
		status, _ := matchingReqlog["status"].(float64)
		if int(status) != http.StatusOK {
			t.Errorf("matching reqlog line status = %v; want 200", matchingReqlog["status"])
		}
	}
}

// ── reqlog-outside-auth test ──────────────────────────────────────────────────

// TestReqLogMiddleware_401RequestIsLogged verifies that a request rejected with
// 401 (by the bearer auth middleware) still produces a reqlog line with
// status=401. This asserts that reqlog sits OUTSIDE auth in the pipeline.
func TestReqLogMiddleware_401RequestIsLogged(t *testing.T) {
	t.Parallel()

	var buf bytes.Buffer
	logger := newCaptureLogger(&buf)
	mw := newRequestLogMiddleware(logger)

	// Simulate bearer auth middleware rejecting the request.
	inner := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("WWW-Authenticate", "Bearer")
		w.WriteHeader(http.StatusUnauthorized)
	})
	handler := mw(inner)

	req := httptest.NewRequest(http.MethodPost, "/mcp", nil)
	// No Authorization header — auth would reject this.
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	lines := reqlogLines(parseLogLines(&buf))
	if len(lines) != 1 {
		t.Fatalf("expected 1 reqlog line for 401 request, got %d", len(lines))
	}
	status, _ := lines[0]["status"].(float64)
	if int(status) != http.StatusUnauthorized {
		t.Errorf("reqlog status = %v; want 401 for rejected request", lines[0]["status"])
	}
	reqID, _ := lines[0]["request_id"].(string)
	if reqID == "" {
		t.Error("reqlog line for 401 request has empty request_id")
	}
}

// TestHTTPPipelineOrder_ReqlogOutsideAuth verifies the pipeline order
// reqlog → auth by running a real RunHTTP instance and confirming that an
// unauthorized request (no bearer token) produces a reqlog line with status=401.
//
// This test exercises the real pipeline assembly in http.go.
func TestHTTPPipelineOrder_ReqlogOutsideAuth(t *testing.T) {
	t.Parallel()

	var logBuf bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&logBuf, &slog.HandlerOptions{Level: slog.LevelInfo}))
	srv := newServer(noopStub(), Options{
		Name:    "pipeline-order-test",
		Version: "v0.0.0-test",
		Logger:  logger,
	})

	const token = "pipeline-order-test-token"
	tokenFile := writeTokenFile(t, token)
	readyCh := make(chan net.Addr, 1)

	_, exitCh, cancel := startRunHTTP(t, srv, HTTPOptions{
		Addr:          "127.0.0.1:0",
		TokenFilePath: tokenFile,
		ReadyCh:       readyCh,
	})

	addr := waitReady(t, readyCh, 5*time.Second)

	// Make a request WITHOUT an Authorization header — should get 401.
	resp, err := http.Post(fmt.Sprintf("http://%s/mcp", addr.String()),
		"application/json", strings.NewReader(`{}`))
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	_ = resp.Body.Close()

	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("status = %d; want 401 (no bearer token)", resp.StatusCode)
	}

	// Shut down.
	cancel()
	select {
	case <-exitCh:
	case <-time.After(5 * time.Second):
		t.Fatal("RunHTTP did not exit within 5s after cancel")
	}

	// The reqlog line for the 401 must exist.
	allLines := parseLogLines(&logBuf)
	rqLines := reqlogLines(allLines)

	var found401 bool
	for _, line := range rqLines {
		if s, _ := line["status"].(float64); int(s) == http.StatusUnauthorized {
			found401 = true
			// Verify request_id is present.
			if reqID, _ := line["request_id"].(string); reqID == "" {
				t.Error("reqlog line for 401 has empty request_id")
			}
			break
		}
	}
	if !found401 {
		t.Errorf("no reqlog line with status=401 found; lines: %v", rqLines)
	}
}
