package server

// hardening_test.go — Issue 3.5: Hardening matrix tests (bind / 403 / 401 / pipeline order).
//
// Production-equivalence test matrix for ADR-006.  Each hardening layer is asserted
// independently against the REAL RunHTTP pipeline (127.0.0.1:0 listener, real net.Listen
// path) — not against httptest.NewServer.  A real listener is required because:
//
//   - (a) The bind assertion must exercise the production net.Listen code path.
//   - (b) The DNS-rebinding check must see a real local socket address vs. a real
//         Host header.  httptest.NewServer does not use the same binding path.
//
// Pipeline-order regression tests pin the MaxBytes → reqlog → auth → SDK ordering
// against SDK v1.6.1 observed behavior.  Every failure message cites the spike note
// (spikeNoteRef constant) so that a future SDK upgrade that changes observed behavior
// fails loudly with the correct diagnostic.
//
// Concurrency: each sub-test runs its own RunHTTP instance to keep isolation strict;
// no shared mutable state crosses sub-test boundaries.  No time.Sleep anywhere —
// synchronisation is entirely via channels.  The full file runs clean under
// go test -race -count=10 ./internal/server/...
//
// SDK v1.6.1 observed behaviors pinned in this file:
//   (b) Host: evil.example.com with valid bearer → 403 (SDK DNS-rebinding protection)
//   (c) Missing/malformed/wrong bearer → 401 (auth middleware, pos 3 in pipeline)
//   (d) >1 MiB body + valid bearer → 400 (SDK json.Decode fails on MaxBytesError)
//   (i) >1 MiB body + bad bearer → 401 (auth header-only check fires before body read)
//   (ii) Unauthorised request → reqlog line with status=401 (reqlog outside auth)
//   (iii) Bad bearer + rebound Host → 401 (auth pos 3 fires before SDK pos 4)

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/ethereum/go-ethereum/common"
	"github.com/rootwarp/blockchain-ai-tools/apps/eth-signer-mcp/internal/obs"
	"github.com/rootwarp/blockchain-ai-tools/apps/eth-signer-mcp/internal/signing"
)

// spikeNoteRef is the path to the SDK spike note, included in every pipeline-order
// failure message so that a future SDK upgrade that changes v1.6.1's observed
// behavior fails with the right diagnostic.  Path is relative to the app module root.
const spikeNoteRef = "docs/mcp-sdk-spike.md"

// ── (a) Bind layer ─────────────────────────────────────────────────────────────

// TestHardeningMatrix_Bind verifies that the default configuration's resolved
// listener address is loopback — asserted STRUCTURALLY on *net.TCPAddr.IP.IsLoopback(),
// never by string-matching "127.0.0.1".
//
// This is the ADR-006 loopback-only invariant.  A non-loopback bind would expose the
// signing service to non-local network traffic.  RunHTTP enforces this structurally
// (rejects non-loopback bound addresses even if cmd validation is bypassed).
func TestHardeningMatrix_Bind(t *testing.T) {
	t.Parallel()

	srv := testServer(t)
	tokenFile := writeTokenFile(t, "hardening-bind-test-token\n")
	readyCh := make(chan net.Addr, 1)

	_, exitCh, cancel := startRunHTTP(t, srv, HTTPOptions{
		Addr:          "127.0.0.1:0", // default — explicit for clarity in the matrix
		TokenFilePath: tokenFile,
		ReadyCh:       readyCh,
	})

	addr := waitReady(t, readyCh, 5*time.Second)

	// Type-assert to *net.TCPAddr for a STRUCTURAL assertion on the IP.
	// This catches a mis-bind that produces the correct string but wrong type
	// (e.g. a Unix socket or an IPv6 address that happens to print as loopback).
	tcpAddr, ok := addr.(*net.TCPAddr)
	if !ok {
		t.Fatalf("hardening bind: bound addr type = %T; want *net.TCPAddr", addr)
	}
	if !tcpAddr.IP.IsLoopback() {
		t.Errorf("hardening bind: bound IP %v is NOT loopback (ADR-006 violation). "+
			"Default configuration must always bind to a loopback address to prevent "+
			"exposure to non-local network traffic. "+
			"See %s §Question 2 (DisableLocalhostProtection).",
			tcpAddr.IP, spikeNoteRef)
	}

	cancel()
	select {
	case <-exitCh:
	case <-time.After(5 * time.Second):
		t.Fatal("hardening bind: RunHTTP did not return within 5s after cancel")
	}
}

// ── (b) Rebind layer ──────────────────────────────────────────────────────────

// TestHardeningMatrix_Rebind_ValidBearer_Returns403 verifies that the SDK's
// DNS-rebinding protection fires when a non-loopback Host header is presented,
// even with a valid bearer token.
//
// Pipeline flow (outermost → innermost):
//
//	MaxBytesHandler → reqlog → bearer auth (PASSES: valid token) →
//	SDK ServeHTTP → 403 (DNS-rebinding check at the top of ServeHTTP, pos 4).
//
// SDK v1.6.1 observed behavior (pinned, see docs/mcp-sdk-spike.md §Question 2):
//
//	DisableLocalhostProtection: false (default) causes the SDK to reject with 403
//	when the server is bound to a loopback address but the request's Host header
//	is non-loopback.  Check fires at the very top of StreamableHTTPHandler.ServeHTTP:
//	  if util.IsLoopback(localAddr) && !util.IsLoopback(req.Host) { → 403 }
//
// Status assertion: 403.  The SDK response body is not our contract (it may change
// between SDK versions); only the status code is pinned here.
func TestHardeningMatrix_Rebind_ValidBearer_Returns403(t *testing.T) {
	t.Parallel()

	const token = "rebind-valid-bearer-403-hardening"
	// noopStub panics if SignTransaction is called; since the SDK 403s before
	// dispatching tools, the panic never fires — this is the recording seam.
	srv := testServer(t)
	tokenFile := writeTokenFile(t, token+"\n")
	readyCh := make(chan net.Addr, 1)

	_, exitCh, cancel := startRunHTTP(t, srv, HTTPOptions{
		Addr:          "127.0.0.1:0",
		TokenFilePath: tokenFile,
		ReadyCh:       readyCh,
	})

	addr := waitReady(t, readyCh, 5*time.Second)

	// Build a raw HTTP request to the real server address (TCP connects to 127.0.0.1:PORT)
	// but with a non-loopback Host header.  Go's http.Client sends req.Host as the
	// Host: header when it is non-empty — the server sees "evil.example.com" as req.Host.
	req, err := http.NewRequestWithContext(context.Background(),
		http.MethodPost,
		fmt.Sprintf("http://%s/mcp", addr.String()),
		strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{}}`),
	)
	if err != nil {
		t.Fatalf("hardening rebind: NewRequest: %v", err)
	}
	// Forge the Host header.  The SDK checks:
	//   util.IsLoopback(localAddr="127.0.0.1:PORT") = true
	//   util.IsLoopback("evil.example.com")          = false
	//   → 403 Forbidden fires at the top of SDK ServeHTTP.
	req.Host = "evil.example.com"
	req.Header.Set("Authorization", "Bearer "+token) // VALID bearer — auth MUST pass
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("hardening rebind: HTTP client error: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	_, _ = io.Copy(io.Discard, resp.Body)

	// SDK v1.6.1 PINNED: 403 Forbidden.
	// If a future SDK upgrade changes this (e.g. different status code or field rename),
	// update wantStatus and this comment, cite the new SDK version.
	// See docs/mcp-sdk-spike.md §Question 2 (DisableLocalhostProtection field survey)
	// and §Question 3 (pipeline order — DNS-rebinding 403 fires inside SDK ServeHTTP).
	const wantStatus = http.StatusForbidden
	if resp.StatusCode != wantStatus {
		t.Errorf("hardening rebind: Host: evil.example.com with VALID bearer: got %d; want %d. "+
			"SDK v1.6.1 DNS-rebinding protection (DisableLocalhostProtection: false) must "+
			"return 403 when server is loopback-bound but Host header is non-loopback. "+
			"If SDK was upgraded and behavior changed, update wantStatus and this comment. "+
			"See %s §Question 2 and §Question 3.",
			resp.StatusCode, wantStatus, spikeNoteRef)
	}

	cancel()
	select {
	case <-exitCh:
	case <-time.After(5 * time.Second):
		t.Fatal("hardening rebind: RunHTTP did not return within 5s after cancel")
	}
}

// ── (c) Auth layer ────────────────────────────────────────────────────────────

// TestHardeningMatrix_Auth_Returns401_SigningNeverRan verifies that every
// missing-/malformed-/wrong-bearer variant returns 401 AND that the SDK handler
// and all signing logic were provably never invoked.
//
// Recording seam: each sub-test builds its own *Server backed by a stubSigner
// whose SignTransaction method atomically records invocations and fails the test
// if called.  Zero invocations after the 401 response = signing logic never ran.
//
// Pipeline: MaxBytesHandler → reqlog → bearer auth (401, pos 3) → SDK NEVER CALLED.
// SDK v1.6.1 observed behavior: 401 + empty body + WWW-Authenticate: Bearer header.
func TestHardeningMatrix_Auth_Returns401_SigningNeverRan(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name       string
		authHeader string // empty string → no Authorization header
	}{
		{"missing_header", ""},
		{"malformed_no_bearer_prefix", "Token abc123"},
		{"malformed_lowercase_bearer", "bearer correct-token"}, // RFC 6750: case-sensitive "Bearer "
		{"malformed_bearer_no_space", "Bearer"},                // no space after Bearer
		{"wrong_token", "Bearer definitely-wrong-xyz"},
		{"empty_token_after_bearer", "Bearer "},
	}

	const correctToken = "hardening-auth-correct-token-001"

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			// Per-sub-test recording seam: counts SignTransaction calls.
			// Any call fails the test immediately with a clear message.
			var signCalled atomic.Int32
			stub := &stubSigner{
				address: common.Address{},
				signFn: func(_ context.Context, _ signing.TxRequest) (*signing.SignResult, error) {
					n := signCalled.Add(1)
					t.Errorf("hardening auth %q: SignTransaction called (n=%d). "+
						"Bearer auth at position 3 MUST return 401 before the SDK handler "+
						"(position 4) dispatches tools. "+
						"See %s §Question 3 (pipeline order: auth wraps SDK handler).",
						tc.name, n, spikeNoteRef)
					return nil, &signing.ToolError{
						Code:    signing.CodeInternalError,
						Message: "hardening: stub must not be called on 401 path",
					}
				},
			}

			srv := newServer(stub, Options{
				Name:    "hardening-auth-" + tc.name,
				Version: "v0.0.0-test",
				Logger:  obs.NewLogger("error"),
			})

			tokenFile := writeTokenFile(t, correctToken+"\n")
			readyCh := make(chan net.Addr, 1)

			_, exitCh, cancel := startRunHTTP(t, srv, HTTPOptions{
				Addr:          "127.0.0.1:0",
				TokenFilePath: tokenFile,
				ReadyCh:       readyCh,
			})

			addr := waitReady(t, readyCh, 5*time.Second)

			req, err := http.NewRequestWithContext(context.Background(),
				http.MethodPost,
				fmt.Sprintf("http://%s/mcp", addr.String()),
				// Use a sign_transaction call body so the stub would be invoked
				// if the SDK handler is ever reached — proving the seam is live.
				strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"sign_transaction","arguments":{}}}`),
			)
			if err != nil {
				t.Fatalf("hardening auth %q: NewRequest: %v", tc.name, err)
			}
			if tc.authHeader != "" {
				req.Header.Set("Authorization", tc.authHeader)
			}
			req.Header.Set("Content-Type", "application/json")
			req.Header.Set("Accept", "application/json, text/event-stream")

			resp, err := http.DefaultClient.Do(req)
			if err != nil {
				t.Fatalf("hardening auth %q: HTTP error: %v", tc.name, err)
			}
			defer func() { _ = resp.Body.Close() }()
			body, _ := io.ReadAll(resp.Body)

			// SDK v1.6.1 PINNED: 401 Unauthorized.
			if resp.StatusCode != http.StatusUnauthorized {
				t.Errorf("hardening auth %q: got status %d; want 401. "+
					"Bearer auth middleware (pos 3) must reject before SDK (pos 4). "+
					"See %s §Question 3.",
					tc.name, resp.StatusCode, spikeNoteRef)
			}
			// 401 responses must carry an empty body (auth.go contract).
			if len(body) > 0 {
				t.Errorf("hardening auth %q: 401 response body has %d bytes; want empty. "+
					"auth.go must write no body on 401 paths.",
					tc.name, len(body))
			}
			// WWW-Authenticate: Bearer must be set (RFC 6750 §3.1).
			if got := resp.Header.Get("WWW-Authenticate"); got != "Bearer" {
				t.Errorf("hardening auth %q: WWW-Authenticate = %q; want %q",
					tc.name, got, "Bearer")
			}

			cancel()
			select {
			case <-exitCh:
			case <-time.After(5 * time.Second):
				t.Fatalf("hardening auth %q: RunHTTP did not return within 5s after cancel", tc.name)
			}

			// Final seam assertion: signing logic must not have been invoked.
			if n := signCalled.Load(); n != 0 {
				t.Errorf("hardening auth %q: SignTransaction called %d times; want 0. "+
					"Auth (pos 3) must reject 401 before SDK (pos 4) dispatches any tool. "+
					"See %s §Question 3.",
					tc.name, n, spikeNoteRef)
			}
		})
	}
}

// ── (d) Body cap ─────────────────────────────────────────────────────────────

// TestHardeningMatrix_BodyCap_OversizedRejected re-asserts the 1 MiB body cap
// inside the hardening matrix so the matrix is complete on its own.
//
// A >1 MiB syntactically valid JSON body (oversized via a "_pad" field, so the
// rejection is attributable to the byte cap and not to a JSON-syntax failure) is
// sent with a VALID bearer token.  The SDK handler receives the request but the
// body read triggers MaxBytesReader, causing json.Decoder to fail.
//
// SDK v1.6.1 observed behavior (pinned):
//
//	MaxBytesReader causes json.Decode to return *http.MaxBytesError; the SDK
//	returns HTTP 400 (Bad Request) for a JSON-decode failure.  It does NOT
//	translate *http.MaxBytesError to 413.
//	See also: bounds_test.go TestMaxBytesHandler_OversizedBodyRejected (same pin).
func TestHardeningMatrix_BodyCap_OversizedRejected(t *testing.T) {
	t.Parallel()

	// Tool-call counter: must stay 0 if the body cap fires before tool dispatch.
	var toolCalled atomic.Int32
	stub := &stubSigner{
		address: common.Address{},
		signFn: func(_ context.Context, _ signing.TxRequest) (*signing.SignResult, error) {
			toolCalled.Add(1)
			return nil, &signing.ToolError{
				Code:    signing.CodeInternalError,
				Message: "hardening body-cap: stub must not reach signing on oversized body",
			}
		},
	}
	srv := newServer(stub, Options{
		Name:    "hardening-bodycap-test",
		Version: "v0.0.0-test",
		Logger:  obs.NewLogger("error"),
	})

	addr, token := startHTTPWithToken(t, srv)

	// Build a >1 MiB syntactically valid JSON-RPC frame.
	// 1_100_000 bytes of padding pushes the body to ≈1.1 MiB, well over the 1 MiB cap.
	// The _pad field is valid JSON so the rejection is from the byte cap, not syntax.
	pad := strings.Repeat("Z", 1_100_000)
	body := fmt.Sprintf(
		`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"sign_transaction","arguments":{"type":"0x0","chainId":"1","nonce":"0","to":"0x9858EfFD232B4033E47d90003D41EC34EcaEda94","value":"0","data":"0x","gas":"21000","gasPrice":"20000000000","_pad":%q}}}`,
		pad,
	)

	req, err := http.NewRequestWithContext(context.Background(),
		http.MethodPost,
		fmt.Sprintf("http://%s/mcp", addr.String()),
		strings.NewReader(body),
	)
	if err != nil {
		t.Fatalf("hardening body-cap: NewRequest: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	req.Header.Set("Authorization", "Bearer "+token) // VALID bearer so auth passes

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		// Transport-level rejection (connection reset after MaxBytesError or early close).
		// Server is still alive — verify with a health-check so this path cannot mask a crash.
		t.Logf("hardening body-cap: transport-level error (body rejected before full read): %v", err)
		serverHealthCheck(t, addr, token)
		if n := toolCalled.Load(); n != 0 {
			t.Errorf("hardening body-cap: tool handler called %d times on transport error path; want 0", n)
		}
		return
	}
	defer func() { _ = resp.Body.Close() }()
	_, _ = io.Copy(io.Discard, resp.Body)

	// Must be a client error (4xx), not a success.
	if resp.StatusCode < 400 {
		t.Errorf("hardening body-cap: got status %d; want >= 400. "+
			"MaxBytesHandler (pos 1, outermost) must reject before full request processing. "+
			"See %s §Question 3 (pipeline order).",
			resp.StatusCode, spikeNoteRef)
	}

	// SDK v1.6.1 PINNED: 400 Bad Request.
	// MaxBytesReader causes the SDK's json.Decode to fail; the SDK returns 400, not 413.
	// If SDK was upgraded and now returns a different status, update pinnedBodyCapStatus
	// and this comment, citing the new SDK version and the new observed behavior.
	// See docs/mcp-sdk-spike.md §Question 3 (pipeline order table).
	const pinnedBodyCapStatus = 400
	if resp.StatusCode != pinnedBodyCapStatus {
		t.Errorf("hardening body-cap: status = %d; want %d (SDK v1.6.1 pinned: "+
			"MaxBytesReader → json.Decode fail → 400). "+
			"If SDK was upgraded, update pinnedBodyCapStatus and this comment. "+
			"See %s §Question 3.",
			resp.StatusCode, pinnedBodyCapStatus, spikeNoteRef)
	}

	if n := toolCalled.Load(); n != 0 {
		t.Errorf("hardening body-cap: tool handler called %d times; want 0. "+
			"Oversized body must be rejected before tool dispatch reaches the signing layer.",
			n)
	}
}

// ── Pipeline-order regression tests ──────────────────────────────────────────

// TestHardeningMatrix_PipelineOrder_OversizedBadToken pins the behavior of the
// "both-fail" case where the request body exceeds 1 MiB AND the bearer token is wrong.
//
// Pipeline: MaxBytesHandler (1) → reqlog (2) → bearer auth (3) → SDK (4).
//
// SDK v1.6.1 observed behavior (pinned):
//
//	Auth at position 3 checks the Authorization header ONLY — it does NOT read the
//	request body.  MaxBytesReader is wrapped (position 1, outermost) but is never
//	triggered because auth returns 401 before any body bytes are consumed.
//
//	Observed response: 401 Unauthorized.
//
// NOTE: This observed behavior differs from the issue description's assertion of
// "fails on SIZE".  Our bearer auth implementation (auth.go, Middleware) reads ONLY
// the Authorization header; it does not buffer or read r.Body.  Therefore:
//   - MaxBytesReader is set up (outermost layer wraps the body reader).
//   - Auth is consulted (checks the header → wrong token → 401).
//   - MaxBytesReader is NEVER triggered (body is never read on the 401 path).
//
// Security consequence: auth is efficiently header-only; it never buffers the body,
// so a large malicious body incurs no extra allocation on the 401 path.
// If this assertion ever changes to a size-related status (400/413), it would indicate
// auth.go now reads the body before checking credentials — a performance regression.
// See docs/mcp-sdk-spike.md §Question 3 for the confirmed pipeline order.
func TestHardeningMatrix_PipelineOrder_OversizedBadToken(t *testing.T) {
	t.Parallel()

	const correctToken = "pipeline-reg-i-correct-token"
	srv := testServer(t) // noopStub — signing must never run on 401 path
	tokenFile := writeTokenFile(t, correctToken+"\n")
	readyCh := make(chan net.Addr, 1)

	_, exitCh, cancel := startRunHTTP(t, srv, HTTPOptions{
		Addr:          "127.0.0.1:0",
		TokenFilePath: tokenFile,
		ReadyCh:       readyCh,
	})

	addr := waitReady(t, readyCh, 5*time.Second)

	// Oversized body (same construction as (d) body-cap test).
	pad := strings.Repeat("W", 1_100_000)
	body := fmt.Sprintf(
		`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"sign_transaction","arguments":{"type":"0x0","chainId":"1","nonce":"0","to":"0x9858EfFD232B4033E47d90003D41EC34EcaEda94","value":"0","data":"0x","gas":"21000","gasPrice":"20000000000","_pad":%q}}}`,
		pad,
	)

	req, err := http.NewRequestWithContext(context.Background(),
		http.MethodPost,
		fmt.Sprintf("http://%s/mcp", addr.String()),
		strings.NewReader(body),
	)
	if err != nil {
		t.Fatalf("pipeline-reg-i: NewRequest: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	req.Header.Set("Authorization", "Bearer WRONG-TOKEN-XYZ") // BAD bearer

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		// Transport-level error: the server likely sent 401 (from auth's header check)
		// and then closed the connection while the client was still transmitting the
		// large body.  This is acceptable — it means auth fired first (header-only),
		// MaxBytesReader was set up but the body was never read.
		t.Logf("pipeline-reg-i: transport-level error (auth likely sent 401 then closed): %v. "+
			"Acceptable: auth (pos 3) fired on header; body never read; MaxBytesReader not triggered. "+
			"See %s §Question 3.", err, spikeNoteRef)
		serverHealthCheck(t, addr, correctToken)
		cancel()
		select {
		case <-exitCh:
		case <-time.After(5 * time.Second):
			t.Fatal("pipeline-reg-i: RunHTTP did not return within 5s after cancel")
		}
		return
	}
	defer func() { _ = resp.Body.Close() }()
	_, _ = io.Copy(io.Discard, resp.Body)

	// SDK v1.6.1 PINNED: 401 Unauthorized.
	//
	// Auth (pos 3) checks the Authorization header and returns 401 without reading
	// the body.  MaxBytesReader (pos 1) is wrapped but NEVER triggered.
	//
	// Diagnostic guide for future failures:
	//   400 or 413 → auth.go now reads the body before checking credentials (regression;
	//                investigate auth.go Middleware for inadvertent r.Body reads).
	//   200         → both auth and body-cap checks are broken.
	//   403         → SDK's DNS-rebinding check fired (unexpected; no forged Host here).
	//
	// See docs/mcp-sdk-spike.md §Question 3 for the confirmed locked nesting.
	const pinnedOversizedBadTokenStatus = http.StatusUnauthorized // 401
	if resp.StatusCode != pinnedOversizedBadTokenStatus {
		t.Errorf("pipeline-reg-i (oversized+bad-token): got status %d; want %d. "+
			"Auth (pos 3) checks header only — body is NEVER read on the 401 path. "+
			"A size error (400/413) would indicate auth now reads the body (regression). "+
			"See %s §Question 3.",
			resp.StatusCode, pinnedOversizedBadTokenStatus, spikeNoteRef)
	}

	cancel()
	select {
	case <-exitCh:
	case <-time.After(5 * time.Second):
		t.Fatal("pipeline-reg-i: RunHTTP did not return within 5s after cancel")
	}
}

// TestHardeningMatrix_PipelineOrder_UnauthorizedIsReqlogged pins the pipeline
// ordering between reqlog (position 2) and bearer auth (position 3).
//
// reqlog sits OUTSIDE auth in the pipeline, so even requests rejected with 401
// must produce exactly one structured log line with status=401.
//
// SDK v1.6.1 observed behavior (pinned):
//
//	reqlog middleware wraps auth; after auth returns 401, reqlog's deferred
//	log emission fires and records status=401 in the captured JSON log.
//	An unauthorised request ALWAYS produces one reqlog line.
//
// If this assertion fails (no 401 reqlog line found): reqlog is INSIDE auth,
// which means auth fires before reqlog, and rejected requests are not logged.
// This is a pipeline order regression.
// See docs/mcp-sdk-spike.md §Question 3 for the confirmed locked nesting.
func TestHardeningMatrix_PipelineOrder_UnauthorizedIsReqlogged(t *testing.T) {
	t.Parallel()

	var logBuf bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&logBuf, &slog.HandlerOptions{Level: slog.LevelInfo}))
	srv := newServer(noopStub(), Options{
		Name:    "hardening-reqlog-order-test",
		Version: "v0.0.0-test",
		Logger:  logger,
	})

	const token = "pipeline-reg-ii-test-token"
	tokenFile := writeTokenFile(t, token+"\n")
	readyCh := make(chan net.Addr, 1)

	_, exitCh, cancel := startRunHTTP(t, srv, HTTPOptions{
		Addr:          "127.0.0.1:0",
		TokenFilePath: tokenFile,
		ReadyCh:       readyCh,
	})

	addr := waitReady(t, readyCh, 5*time.Second)

	// Send a request without an Authorization header — auth will return 401.
	// We use http.Post (not the SDK client) to keep this test simple and direct.
	resp, err := http.Post(
		fmt.Sprintf("http://%s/mcp", addr.String()),
		"application/json",
		strings.NewReader(`{}`),
	)
	if err != nil {
		t.Fatalf("pipeline-reg-ii: POST: %v", err)
	}
	_ = resp.Body.Close()

	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("pipeline-reg-ii: no-auth request: got %d; want 401", resp.StatusCode)
	}

	cancel()
	select {
	case <-exitCh:
	case <-time.After(5 * time.Second):
		t.Fatal("pipeline-reg-ii: RunHTTP did not return within 5s after cancel")
	}

	// Parse captured log lines and find the 401 reqlog entry.
	allLines := parseLogLines(&logBuf)
	rqLines := reqlogLines(allLines)

	var found401 bool
	for _, line := range rqLines {
		if s, _ := line["status"].(float64); int(s) == http.StatusUnauthorized {
			found401 = true
			// request_id must be present and non-empty even for rejected requests.
			if rid, _ := line["request_id"].(string); rid == "" {
				t.Errorf("pipeline-reg-ii: reqlog line for 401 has empty request_id. " +
					"reqlog must generate and attach a request_id for ALL requests " +
					"(including those rejected by auth at pos 3).")
			}
			break
		}
	}
	if !found401 {
		t.Errorf("pipeline-reg-ii: no reqlog line with status=401 found after unauthorised request. "+
			"reqlog (pos 2) must sit OUTSIDE auth (pos 3) so that rejected requests are logged. "+
			"If 401 requests are NOT logged, reqlog is inside auth — pipeline order is WRONG. "+
			"See %s §Question 3. Captured reqlog lines: %v",
			spikeNoteRef, rqLines)
	}
}

// TestHardeningMatrix_PipelineOrder_BadTokenReboundHost_Returns401 pins the
// "both-fail" case where the bearer token is wrong AND the Host header is forged.
//
// Pipeline: MaxBytesHandler (1) → reqlog (2) → bearer auth (3) → SDK (4).
//
// The SDK's DNS-rebinding 403 fires inside ServeHTTP (position 4, innermost).
// Bearer auth fires at position 3, which WRAPS the SDK handler.
//
// SDK v1.6.1 observed behavior (pinned):
//
//	Auth (pos 3) fires first → 401.  The SDK handler (pos 4) is never called →
//	the DNS-rebinding 403 check (inside SDK ServeHTTP) is never reached.
//
// Diagnostic guide for future failures:
//
//	403 → auth is no longer wrapping the SDK handler (pipeline order regression);
//	       the SDK handler runs before auth and its DNS-rebinding check fires first.
//	       This would mean the SDK handler can be accessed without valid auth.
//	200 → both auth and DNS-rebinding checks are broken.
//
// See docs/mcp-sdk-spike.md §Question 3 for the confirmed locked nesting
// (auth wraps SDK handler: auth at pos 3, SDK at pos 4).
func TestHardeningMatrix_PipelineOrder_BadTokenReboundHost_Returns401(t *testing.T) {
	t.Parallel()

	const correctToken = "pipeline-reg-iii-correct-token"
	srv := testServer(t) // noopStub — signing must never run
	tokenFile := writeTokenFile(t, correctToken+"\n")
	readyCh := make(chan net.Addr, 1)

	_, exitCh, cancel := startRunHTTP(t, srv, HTTPOptions{
		Addr:          "127.0.0.1:0",
		TokenFilePath: tokenFile,
		ReadyCh:       readyCh,
	})

	addr := waitReady(t, readyCh, 5*time.Second)

	req, err := http.NewRequestWithContext(context.Background(),
		http.MethodPost,
		fmt.Sprintf("http://%s/mcp", addr.String()),
		strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{}}`),
	)
	if err != nil {
		t.Fatalf("pipeline-reg-iii: NewRequest: %v", err)
	}
	// Both conditions that would cause rejection:
	req.Host = "evil.example.com"                             // would cause SDK 403 at pos 4
	req.Header.Set("Authorization", "Bearer WRONG-TOKEN-XYZ") // causes auth 401 at pos 3
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("pipeline-reg-iii: HTTP error: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	_, _ = io.Copy(io.Discard, resp.Body)

	// SDK v1.6.1 PINNED: 401 Unauthorized.
	// Auth (pos 3) wraps the SDK handler (pos 4).  Bad token → auth returns 401
	// before the SDK handler runs.  The SDK's DNS-rebinding 403 (pos 4) is NEVER
	// reached because auth short-circuits at pos 3.
	//
	// A 403 response would indicate auth is NO LONGER wrapping the SDK handler
	// (pipeline regression): the SDK handler would then be accessible without valid
	// authentication — a critical security regression.
	// See docs/mcp-sdk-spike.md §Question 3 for the confirmed locked nesting.
	const pinnedBadTokenReboundStatus = http.StatusUnauthorized // 401
	if resp.StatusCode != pinnedBadTokenReboundStatus {
		t.Errorf("pipeline-reg-iii (bad-token+rebound Host): got status %d; want %d (401). "+
			"Auth (pos 3) must fire before SDK (pos 4). "+
			"A 403 would mean auth no longer wraps the SDK handler — CRITICAL pipeline regression. "+
			"See %s §Question 3.",
			resp.StatusCode, pinnedBadTokenReboundStatus, spikeNoteRef)
	}

	cancel()
	select {
	case <-exitCh:
	case <-time.After(5 * time.Second):
		t.Fatal("pipeline-reg-iii: RunHTTP did not return within 5s after cancel")
	}
}

// ── Leak scan ─────────────────────────────────────────────────────────────────

// TestHardeningMatrix_LeakScan runs the signing.Sentinel leak scan over all logs
// captured during 401 and 403 rejection paths in the hardening matrix.
//
// A sentinel bearer token is used; all its encoded forms (raw bytes, hex-lower,
// hex-upper, base64 variants, decimal) must be absent from every captured log line,
// regardless of whether the request was admitted or rejected.
//
// Paths exercised (each produces a reqlog line that must not contain the token):
//
//   - 403 path: valid sentinel token + forged Host header
//   - 401 path (missing bearer)
//   - 401 path (wrong bearer)
//   - 401 path (lowercase "bearer" prefix — RFC 6750 case sensitivity)
//
// The bearer token appears in the Authorization header of the request; reqlog.go
// must not log any header values.  auth.go has no logger and writes no body.
func TestHardeningMatrix_LeakScan(t *testing.T) {
	t.Parallel()

	// Build a 32-byte random sentinel token (hex-encoded for safe HTTP header use).
	sentinelRaw := randTokenBytes(32)
	sentinelToken := hexEncodeBytes(sentinelRaw)

	sentinel := signing.NewSentinel("hardening-bearer-sentinel", sentinelRaw)
	sentinel.RegisterForm("token-hex-string", []byte(sentinelToken))

	// Buffer logger: capture ALL log output during the matrix for the leak scan.
	var logBuf bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&logBuf, &slog.HandlerOptions{Level: slog.LevelInfo}))
	srv := newServer(noopStub(), Options{
		Name:    "hardening-leakscan-test",
		Version: "v0.0.0-test",
		Logger:  logger,
	})

	tokenFile := writeTokenFile(t, sentinelToken+"\n")
	readyCh := make(chan net.Addr, 1)

	_, exitCh, cancel := startRunHTTP(t, srv, HTTPOptions{
		Addr:          "127.0.0.1:0",
		TokenFilePath: tokenFile,
		ReadyCh:       readyCh,
	})

	addr := waitReady(t, readyCh, 5*time.Second)

	// doPost is a local helper that sends a POST to the server with the specified
	// Authorization header and Host override, then discards the response.
	doPost := func(desc, authHeader, hostOverride string) {
		t.Helper()
		req, err := http.NewRequestWithContext(context.Background(),
			http.MethodPost,
			fmt.Sprintf("http://%s/mcp", addr.String()),
			strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{}}`),
		)
		if err != nil {
			t.Errorf("leak-scan %q: NewRequest: %v", desc, err)
			return
		}
		if hostOverride != "" {
			req.Host = hostOverride
		}
		if authHeader != "" {
			req.Header.Set("Authorization", authHeader)
		}
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Accept", "application/json, text/event-stream")
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			// Transport-level error is acceptable (e.g. connection reset on 403 path).
			t.Logf("leak-scan %q: HTTP transport error (may be benign): %v", desc, err)
			return
		}
		defer func() { _ = resp.Body.Close() }()
		_, _ = io.Copy(io.Discard, resp.Body)
	}

	// 403 path: valid sentinel token + non-loopback Host → SDK 403
	doPost("403-rebind", "Bearer "+sentinelToken, "evil.example.com")

	// 401 paths: various malformed/missing Authorization headers
	doPost("401-missing", "", "")
	doPost("401-wrong-token", "Bearer totally-wrong-token-xyz", "")
	doPost("401-lowercase-bearer", "bearer "+sentinelToken, "") // RFC 6750: case-sensitive

	cancel()
	select {
	case <-exitCh:
	case <-time.After(5 * time.Second):
		t.Fatal("hardening leak-scan: RunHTTP did not return within 5s after cancel")
	}

	// Leak scan: sentinel forms (raw, hex, base64, decimal, etc.) must not appear
	// anywhere in the captured log output from the 401/403 paths.
	output := logBuf.Bytes()
	if leaked := sentinel.Scan(output); len(leaked) > 0 {
		// SAFETY: report form NAMES only — never the bytes or the token value.
		t.Errorf("hardening leak-scan: sentinel forms found in captured logs: forms=%v "+
			"(sentinel name: %q). "+
			"Check reqlog.go (must not log Authorization header values) and "+
			"auth.go (must not log token or derived bytes on any path).",
			leaked, sentinel.Name)
	}
}
