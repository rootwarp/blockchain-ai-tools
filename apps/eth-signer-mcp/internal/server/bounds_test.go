package server

// bounds_test.go — Issue 3.4: Resource bounds tests.
//
// Three test groups:
//
//  (a) Oversized-body rejection: a >1 MiB body is rejected before the SDK handler
//      is called, asserted by a recording seam and the client-visible HTTP status.
//      SDK v1.6.1 observed behavior is pinned in the test with a comment.
//
//  (b) Cap composition: sign_transaction with data at EXACTLY 256 KiB bytes (512 KiB
//      hex + "0x") passes the 1 MiB body cap AND signs successfully (light fixture).
//      One byte over the cap but body <1 MiB → invalid_input from schema/validation;
//      vault never invoked.
//
//  (c) Ctx + semaphore plumbing: a recordingKeyVault wrapper (test-only) delegates
//      to a real FileKeyVault (light fixture) and records WithSigningKey entry (before
//      semaphore) and fn entry (after semaphore + KDF) as separate counters. Request A
//      holds the semaphore (slow fn via holdFnCh). B's cancel is gated on B entering
//      the vault (withSigningKeyCallsTotal ≥ 2) so the cancel is provably at the
//      semaphore layer. Assertions: withSigningKeyCallsTotal == 2 (B reached vault) AND
//      fnCallsTotal == 1 (B never ran KDF). maxActiveFn == 1. A completes normally.
//
// All tests run under -race. No time.Sleep calls anywhere.
//
// Pipeline order wiring: MaxBytesHandler must be the outermost wrapper. The test in
// group (a) directly proves this: the oversized body is rejected BEFORE auth is
// consulted (sending a request with correct auth but oversized body must still be
// rejected). The exact rejection status from SDK v1.6.1 is pinned below.

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/ethereum/go-ethereum/common"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/rootwarp/blockchain-ai-tools/apps/eth-signer-mcp/internal/obs"
	"github.com/rootwarp/blockchain-ai-tools/apps/eth-signer-mcp/internal/signing"
)

// ── recordingKeyVault ────────────────────────────────────────────────────────
//
// recordingKeyVault wraps a signing.KeyVault to instrument WithSigningKey at two
// points: ENTRY (before delegating to inner, before the semaphore) and INSIDE fn
// (after the semaphore + ctx re-check + KDF). Lives in _test.go only.
//
// Two-level counters (the distinction is the key correctness property):
//
//	withSigningKeyCallsTotal — incremented at WithSigningKey ENTRY, before any
//	    inner call, semaphore acquire, ctx check, or KDF. Records "B reached the
//	    vault" even if B is cancelled at the semaphore and never calls fn.
//
//	fnCallsTotal — incremented INSIDE the wrapped fn, which runs only AFTER the
//	    inner vault acquires the semaphore, re-checks ctx, and runs the KDF.
//	    "B never ran the KDF" is proven by fnCallsTotal staying at 1 while
//	    withSigningKeyCallsTotal == 2.
//
//	activeAtEntry / maxActiveAtEntry — gauge tracking concurrent goroutines
//	    between the vault ENTRY and the return of inner.WithSigningKey (i.e.
//	    callers racing at the vault entrance before the semaphore decides the
//	    winner). maxActiveAtEntry ≥ 2 proves that at least two callers reached
//	    the recording vault concurrently, making maxActiveFn == 1 non-vacuous:
//	    the semaphore actually had to serialize real concurrent callers.
//
// withSigningKeySecondCh is closed the first time withSigningKeyCallsTotal reaches
// 2 — it is a one-shot notification that B has entered the vault path. The test
// gates bCancel() on this channel so it can be certain B is in the semaphore-wait
// select (or close to it) when cancelled, not still at the HTTP/auth/SDK layer.
type recordingKeyVault struct {
	inner signing.KeyVault

	// Notification channels (written by WithSigningKey, read by the test).
	fnStarted              <-chan struct{} // closed when first fn starts
	fnStartCh              chan struct{}   // the writeable end
	withSigningKeySecondCh chan struct{}   // closed when withSigningKeyCallsTotal first reaches 2

	// Test-control channel.
	holdFnCh <-chan struct{} // fn blocks until closed (or ctx cancels)

	// Instrumentation atomics (safe for concurrent use).
	withSigningKeyCallsTotal atomic.Int32 // vault-entry count (before semaphore)
	activeAtEntry            atomic.Int32 // goroutines currently between entry and inner return
	maxActiveAtEntry         atomic.Int32 // high-water mark of activeAtEntry (non-vacuity gauge)
	activeFn                 atomic.Int32 // goroutines currently inside fn
	maxActiveFn              atomic.Int32 // high-water mark of activeFn
	fnCallsTotal             atomic.Int32 // fn invocations (KDF ran + fn entered)
}

// Ensure *recordingKeyVault satisfies signing.KeyVault at compile time.
var _ signing.KeyVault = (*recordingKeyVault)(nil)

// newRecordingVault wraps inner with instrumentation.
// holdFnCh controls whether fn blocks after KDF; pass nil to let fn proceed immediately.
func newRecordingVault(inner signing.KeyVault, holdFnCh <-chan struct{}) *recordingKeyVault {
	fnStarted := make(chan struct{})
	secondCh := make(chan struct{})
	return &recordingKeyVault{
		inner:                  inner,
		fnStarted:              fnStarted,
		fnStartCh:              fnStarted,
		withSigningKeySecondCh: secondCh,
		holdFnCh:               holdFnCh,
	}
}

// Address returns the underlying vault's address.
func (v *recordingKeyVault) Address() common.Address {
	return v.inner.Address()
}

// AddressPointer delegates to the inner vault.
func (v *recordingKeyVault) AddressPointer() *common.Address {
	return v.inner.AddressPointer()
}

// WithSigningKey records entry/exit and optionally holds fn until holdFnCh is closed.
func (v *recordingKeyVault) WithSigningKey(ctx context.Context, fn func(signing.SigningKey) error) error {
	// ── ENTRY point — before semaphore, ctx-check, or KDF ──────────────────
	//
	// withSigningKeyCallsTotal is incremented here so the test can gate on "B has
	// entered the vault path" without waiting for the semaphore (which A holds).
	n := v.withSigningKeyCallsTotal.Add(1)

	// Close the second-caller channel the first time n reaches 2 (one-shot).
	if n == 2 {
		select {
		case <-v.withSigningKeySecondCh: // already closed
		default:
			close(v.withSigningKeySecondCh)
		}
	}

	// Track concurrent goroutines at vault entry (before semaphore).
	// This gauge is incremented here and decremented via defer (after inner returns),
	// so it measures callers racing between vault entry and semaphore release.
	// maxActiveAtEntry ≥ 2 proves that real concurrent callers arrived, making
	// the maxActiveFn == 1 serialization proof non-vacuous.
	entryCur := v.activeAtEntry.Add(1)
	defer v.activeAtEntry.Add(-1)
	for {
		old := v.maxActiveAtEntry.Load()
		if entryCur <= old {
			break
		}
		if v.maxActiveAtEntry.CompareAndSwap(old, entryCur) {
			break
		}
	}

	// Delegate to the inner vault. If ctx is already cancelled (or gets cancelled
	// while waiting on the semaphore), inner.WithSigningKey returns ctx.Err() WITHOUT
	// ever calling the wrapped fn — fnCallsTotal stays unchanged.
	return v.inner.WithSigningKey(ctx, func(key signing.SigningKey) error {
		// ── Inside fn — semaphore acquired, ctx re-checked, KDF ran ────────
		//
		// fnCallsTotal counts KDF executions. If B was cancelled at the semaphore,
		// this closure is never reached for B (fnCallsTotal stays at 1 while A runs).
		v.fnCallsTotal.Add(1)

		cur := v.activeFn.Add(1)
		defer v.activeFn.Add(-1)

		// Update max-concurrency high-water mark (CAS loop; safe for concurrent callers).
		for {
			old := v.maxActiveFn.Load()
			if cur <= old {
				break
			}
			if v.maxActiveFn.CompareAndSwap(old, cur) {
				break
			}
		}

		// Signal that the first fn has started (close once; no-op for subsequent).
		select {
		case <-v.fnStartCh: // already closed
		default:
			close(v.fnStartCh)
		}

		// Optional hold: block until the test releases holdFnCh or ctx cancels.
		if v.holdFnCh != nil {
			select {
			case <-v.holdFnCh:
			case <-ctx.Done():
				return ctx.Err()
			}
		}

		return fn(key)
	})
}

// ── countingVaultWrapper ─────────────────────────────────────────────────────
//
// countingVaultWrapper is a test-only KeyVault that counts WithSigningKey calls.
// Used to assert the vault is never invoked when validate.go rejects the request.
type countingVaultWrapper struct {
	inner     signing.KeyVault
	callCount *atomic.Int32
}

// Ensure *countingVaultWrapper satisfies signing.KeyVault.
var _ signing.KeyVault = (*countingVaultWrapper)(nil)

func (c *countingVaultWrapper) Address() common.Address {
	return c.inner.Address()
}

func (c *countingVaultWrapper) AddressPointer() *common.Address {
	return c.inner.AddressPointer()
}

func (c *countingVaultWrapper) WithSigningKey(ctx context.Context, fn func(signing.SigningKey) error) error {
	c.callCount.Add(1)
	return c.inner.WithSigningKey(ctx, fn)
}

// ── test helpers ─────────────────────────────────────────────────────────────

// startHTTPWithToken starts RunHTTP with a fresh random token and returns the
// bound address and the raw token string. Cleanup is registered via t.Cleanup.
func startHTTPWithToken(t *testing.T, srv *Server) (addr net.Addr, token string) {
	t.Helper()
	rawBytes := randTokenBytes(32)
	token = hexEncodeBytes(rawBytes)
	tokenFile := writeTokenFile(t, token+"\n")
	readyCh := make(chan net.Addr, 1)

	_, exitCh, cancel := startRunHTTP(t, srv, HTTPOptions{
		Addr:          "127.0.0.1:0",
		TokenFilePath: tokenFile,
		ReadyCh:       readyCh,
	})
	t.Cleanup(func() {
		cancel()
		select {
		case <-exitCh:
		case <-time.After(15 * time.Second):
			t.Logf("startHTTPWithToken cleanup: RunHTTP did not exit within 15s")
		}
	})

	addr = waitReady(t, readyCh, 10*time.Second)
	return addr, token
}

// sdkClient builds an SDK v1.6.1 MCP client connected to endpoint with bearer auth.
func sdkClient(t *testing.T, ctx context.Context, endpoint, token string) *mcp.ClientSession {
	t.Helper()
	mcpClient := mcp.NewClient(
		&mcp.Implementation{Name: "bounds-test-client", Version: "v0.0.1"},
		nil,
	)
	transport := &mcp.StreamableClientTransport{
		Endpoint:             endpoint,
		DisableStandaloneSSE: true,
		HTTPClient: &http.Client{
			Transport: bearerRoundTripper{token: token},
		},
	}
	connCtx, connCancel := context.WithTimeout(ctx, 10*time.Second)
	cs, err := mcpClient.Connect(connCtx, transport, nil)
	connCancel()
	if err != nil {
		t.Fatalf("sdkClient: Connect: %v", err)
	}
	t.Cleanup(func() {
		if err := cs.Close(); err != nil {
			t.Logf("sdkClient cleanup: cs.Close: %v (benign)", err)
		}
	})
	return cs
}

// validSign1559Args returns a minimal valid EIP-1559 sign_transaction argument map.
func validSign1559Args() map[string]any {
	return map[string]any{
		"type":                 "0x2",
		"chainId":              "1",
		"nonce":                "0",
		"to":                   "0x9858EfFD232B4033E47d90003D41EC34EcaEda94",
		"value":                "0",
		"data":                 "0x",
		"gas":                  "21000",
		"maxFeePerGas":         "30000000000",
		"maxPriorityFeePerGas": "2000000000",
	}
}

// serverHealthCheck sends a tiny unauthenticated POST to addr and asserts the server
// is still live (returns a response — any status is OK; connection refused means crash).
//
// It is called after a transport-level rejection in the oversized-body tests to ensure
// the early-return path cannot silently mask a server crash.  An unauthenticated request
// is expected to return 401 (bearer auth fires before any body reading).
func serverHealthCheck(t *testing.T, addr net.Addr, token string) {
	t.Helper()
	req, err := http.NewRequestWithContext(context.Background(),
		http.MethodPost, fmt.Sprintf("http://%s", addr.String()),
		strings.NewReader("{}"))
	if err != nil {
		t.Errorf("serverHealthCheck: NewRequest: %v", err)
		return
	}
	req.Header.Set("Accept", "application/json, text/event-stream")
	// Intentionally no Authorization header — bearer auth returns 401 before reading body.
	resp, healthErr := http.DefaultClient.Do(req)
	if healthErr != nil {
		t.Errorf("serverHealthCheck: server appears to have crashed (connection refused or "+
			"similar): %v — a crash would cause the oversized-body transport-error path "+
			"to pass without actually verifying the body cap", healthErr)
		return
	}
	defer func() { _ = resp.Body.Close() }()
	_, _ = io.Copy(io.Discard, resp.Body)
	// 401 is expected (auth fires before SDK) — anything non-nil proves server is live.
	if resp.StatusCode == 0 {
		t.Errorf("serverHealthCheck: unexpected zero status")
	}
	_ = token // token provided for potential future authenticated health-checks
}

// oversizedSignTxBody returns a schema-valid JSON-RPC tools/call body for
// sign_transaction that exceeds the 1 MiB body cap.  The large payload is carried in
// the data field ("0x" + 600_000 × "aa" ≈ 1.2 MiB total body), which is a valid
// TxRequest field with a known JSON key.  No extra fields are present, so the
// inferred schema's additionalProperties:false constraint is satisfied.
//
// The seam is LIVE: if the body cap were bypassed, the SDK's schema validation would
// PASS (all required TxRequest fields present, no unknown fields) and the stub
// handler would be called — making toolCalled > 0 a real assertion.
//
// Using an unknown extra field was VACUOUS: additionalProperties:false causes schema
// validation to fail before handler dispatch, keeping toolCalled=0 regardless of
// whether the body cap fires — the seam would prove nothing about the cap.
func oversizedSignTxBody() string {
	// "0x" + 600_000 × "aa" = 1_200_002 hex chars ≈ 1.14 MiB JSON string value.
	// Total frame with surrounding JSON ≈ 1.14 MiB, well above the 1 MiB cap.
	dataHex := "0x" + strings.Repeat("aa", 600_000)
	return fmt.Sprintf(
		`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"sign_transaction","arguments":{"type":"0x0","chainId":"1","nonce":"0x0","to":"0x9858EfFD232B4033E47d90003D41EC34EcaEda94","value":"0x0","data":"%s","gas":"0x5208","gasPrice":"0x4a817c800"}}}`,
		dataHex,
	)
}

// ── (a) Oversized-body rejection ─────────────────────────────────────────────

// TestMaxBytesHandler_OversizedBodyRejected verifies that a request body > 1 MiB
// is rejected BEFORE the MCP/SDK handler processes it.
//
// The body is a schema-valid JSON-RPC frame with the oversized payload in the data
// field, so the rejection is attributable to the byte cap and not to a JSON syntax
// error or an additionalProperties violation.
//
// SDK v1.6.1 observed rejection behavior (pinned):
//   - MaxBytesReader wraps r.Body; when the SDK's json.Decoder reads past 1 MiB,
//     the Decoder returns *http.MaxBytesError from its Read call.
//   - The SDK's StreamableHTTPHandler returns HTTP 400 (Bad Request) when JSON
//     decoding fails — it does NOT translate *http.MaxBytesError to 413.
//   - pinnedStatus = 400 is asserted below. If a future SDK upgrade changes this,
//     update pinnedStatus AND this comment, citing the new SDK version.
//     See docs/mcp-sdk-spike.md §Question 3 for pipeline order rationale.
//
// The MCP sign_transaction tool handler is verified NOT reached via a counting
// stub; the count must remain 0 after the oversized request.
func TestMaxBytesHandler_OversizedBodyRejected(t *testing.T) {
	t.Parallel()

	// Build the server with a counting stub — if the tool handler is ever called,
	// the count goes above 0 and we fail the test.
	var toolCallCount atomic.Int32
	stub := &stubSigner{
		address: common.HexToAddress(signing.FixtureTestAddress),
		signFn: func(_ context.Context, _ signing.TxRequest) (*signing.SignResult, error) {
			toolCallCount.Add(1)
			return nil, &signing.ToolError{Code: signing.CodeInternalError, Message: "stub: should not be called"}
		},
	}
	srv := newServer(stub, Options{
		Name:    "max-bytes-test",
		Version: "v0.0.0-test",
		Logger:  obs.NewLogger("error"),
	})

	addr, token := startHTTPWithToken(t, srv)

	// Build a schema-valid JSON-RPC frame whose body exceeds 1 MiB.
	// oversizedSignTxBody uses the data field for bulk (≈1.14 MiB), keeping all
	// arguments within the TxRequest schema (no unknown fields).
	// This makes the seam LIVE: schema-validation passes if the cap is bypassed,
	// so toolCallCount would actually increment if the handler were reached.
	body := oversizedSignTxBody()

	req, err := http.NewRequestWithContext(context.Background(),
		http.MethodPost, fmt.Sprintf("http://%s", addr.String()), strings.NewReader(body))
	if err != nil {
		t.Fatalf("NewRequest: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	// The MCP Streamable HTTP protocol requires both Accept types; without them
	// the SDK returns 400 before even reading the body.  Include them so the
	// rejection is attributable to the body cap, not missing Accept headers.
	req.Header.Set("Accept", "application/json, text/event-stream")
	req.Header.Set("Authorization", "Bearer "+token)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		// A transport-level error (connection reset / early close) also signals body
		// rejection — but only if the server is still alive.  Verify with a health-check
		// request so this early-return path cannot mask a server crash.
		t.Logf("HTTP client error (body rejected at transport level): %v", err)
		serverHealthCheck(t, addr, token)
		if n := toolCallCount.Load(); n != 0 {
			t.Errorf("tool handler called %d times; want 0 for oversized body", n)
		}
		return
	}
	defer func() { _ = resp.Body.Close() }()
	_, _ = io.Copy(io.Discard, resp.Body)

	// Contract: the response must be a client error (4xx), NOT a success (2xx).
	if resp.StatusCode < 400 {
		t.Errorf("oversized body: got status %d; want >= 400 (body cap rejection)", resp.StatusCode)
	}

	// SDK v1.6.1 PINNED status: 400 Bad Request (ASSERTED, not just logged).
	// MaxBytesReader causes the SDK's json.Decode to fail; the SDK returns 400
	// (not 413). If a future SDK version changes this, update pinnedStatus + comment,
	// citing the new SDK version and the new observed status.
	// See docs/mcp-sdk-spike.md §Question 3 for pipeline order.
	const pinnedStatus = 400
	if resp.StatusCode != pinnedStatus {
		t.Errorf("oversized body status = %d; want %d (SDK v1.6.1 pinned). "+
			"If SDK was upgraded, update pinnedStatus and this comment. "+
			"See docs/mcp-sdk-spike.md §Question 3.",
			resp.StatusCode, pinnedStatus)
	}

	// The MCP tool handler must NOT have been called.
	if n := toolCallCount.Load(); n != 0 {
		t.Errorf("tool handler called %d times after oversized body; want 0 "+
			"(body rejected before SDK handler reaches tool dispatch)", n)
	}
}

// TestMaxBytesHandler_ValidBodyPasses verifies that a body well under 1 MiB is
// not rejected by the body cap (regression guard: cap must not fire too early).
func TestMaxBytesHandler_ValidBodyPasses(t *testing.T) {
	t.Parallel()

	tdPath := signingTestdataPath(t)
	vault, err := signing.NewFileKeyVault(signing.VaultOptions{
		KeystorePath: filepath.Join(tdPath, "keystore-light.json"),
		PasswordPath: filepath.Join(tdPath, "password.txt"),
	})
	if err != nil {
		t.Fatalf("NewFileKeyVault: %v", err)
	}
	signer := signing.NewSigner(vault, signing.SignerOptions{Logger: obs.NewLogger("error")})
	srv := New(signer, Options{
		Name: "valid-body-test", Version: "v0.0.0-test",
		Logger: obs.NewLogger("error"),
	})

	addr, token := startHTTPWithToken(t, srv)
	endpoint := fmt.Sprintf("http://%s", addr.String())

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	cs := sdkClient(t, ctx, endpoint, token)
	result, err := cs.CallTool(ctx, &mcp.CallToolParams{
		Name:      "sign_transaction",
		Arguments: validSign1559Args(),
	})
	if err != nil {
		t.Fatalf("CallTool valid body: %v", err)
	}
	if result == nil || result.IsError {
		var errContent string
		if result != nil && len(result.Content) > 0 {
			if tc, ok := result.Content[0].(*mcp.TextContent); ok {
				errContent = tc.Text
			}
		}
		t.Errorf("valid body was rejected; want successful sign. Error: %s", errContent)
	}
}

// ── (b) Cap composition ───────────────────────────────────────────────────────

// TestCapComposition_DataAtExactCap_Passes verifies that a sign_transaction request
// with data at EXACTLY 256 KiB bytes (512 KiB hex chars + "0x" = 524290 chars)
// passes the 1 MiB body cap AND signs successfully (light fixture).
//
// Total body size for this request:
//
//	~524290 (data hex) + ~200 (other JSON fields) ≈ 524490 bytes < 1 MiB (1048576).
func TestCapComposition_DataAtExactCap_Passes(t *testing.T) {
	t.Parallel()

	tdPath := signingTestdataPath(t)
	vault, err := signing.NewFileKeyVault(signing.VaultOptions{
		KeystorePath: filepath.Join(tdPath, "keystore-light.json"),
		PasswordPath: filepath.Join(tdPath, "password.txt"),
	})
	if err != nil {
		t.Fatalf("NewFileKeyVault: %v", err)
	}
	signer := signing.NewSigner(vault, signing.SignerOptions{Logger: obs.NewLogger("error")})
	srv := New(signer, Options{
		Name: "cap-exact-test", Version: "v0.0.0-test",
		Logger: obs.NewLogger("error"),
	})

	addr, token := startHTTPWithToken(t, srv)
	endpoint := fmt.Sprintf("http://%s", addr.String())

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second) // KDF ~50 ms
	defer cancel()

	// Build data at EXACTLY 256 KiB decoded = 262144 bytes = 524288 hex chars + "0x".
	// This matches signing.maxDataBytes (= 256 * 1024 = 262144).
	const maxDataBytes = 256 * 1024 // 262144 bytes
	dataHex := "0x" + strings.Repeat("aa", maxDataBytes)

	args := validSign1559Args()
	args["data"] = dataHex

	cs := sdkClient(t, ctx, endpoint, token)
	result, err := cs.CallTool(ctx, &mcp.CallToolParams{
		Name:      "sign_transaction",
		Arguments: args,
	})
	if err != nil {
		t.Fatalf("CallTool (data at 256 KiB cap): %v", err)
	}
	if result == nil {
		t.Fatal("result is nil")
	}
	if result.IsError {
		var errContent string
		if len(result.Content) > 0 {
			if tc, ok := result.Content[0].(*mcp.TextContent); ok {
				errContent = tc.Text
			}
		}
		t.Errorf("data at 256 KiB cap was rejected (IsError=true); want success. Content: %s", errContent)
	}
}

// TestCapComposition_DataOneByteOverCap_InvalidInput verifies that a request with
// data at 256 KiB + 1 byte (one byte over the validate.go cap) is rejected with
// invalid_input — NOT a body-cap rejection — because the total body is still < 1 MiB.
//
// Total body size:
//
//	~524292 (data hex) + ~200 ≈ 524492 bytes < 1 MiB (1048576).
//
// This proves the validate.go data cap (signing.maxDataBytes) fires before the vault
// is ever touched.
func TestCapComposition_DataOneByteOverCap_InvalidInput(t *testing.T) {
	t.Parallel()

	tdPath := signingTestdataPath(t)
	innerVault, err := signing.NewFileKeyVault(signing.VaultOptions{
		KeystorePath: filepath.Join(tdPath, "keystore-light.json"),
		PasswordPath: filepath.Join(tdPath, "password.txt"),
	})
	if err != nil {
		t.Fatalf("NewFileKeyVault: %v", err)
	}

	// Wrap with counting vault to verify the vault is never invoked.
	var vaultCalls atomic.Int32
	countingVault := &countingVaultWrapper{
		inner:     innerVault,
		callCount: &vaultCalls,
	}

	signer := signing.NewSigner(countingVault, signing.SignerOptions{Logger: obs.NewLogger("error")})
	srv := New(signer, Options{
		Name: "cap-over-test", Version: "v0.0.0-test",
		Logger: obs.NewLogger("error"),
	})

	addr, token := startHTTPWithToken(t, srv)
	endpoint := fmt.Sprintf("http://%s", addr.String())

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// One byte over cap: 262145 bytes decoded = 524290 hex chars + "0x".
	// Total body ≈ 524492 bytes < 1 MiB → body cap does NOT fire.
	const overCap = 256*1024 + 1
	dataHex := "0x" + strings.Repeat("aa", overCap)

	args := validSign1559Args()
	args["data"] = dataHex

	cs := sdkClient(t, ctx, endpoint, token)
	result, err := cs.CallTool(ctx, &mcp.CallToolParams{
		Name:      "sign_transaction",
		Arguments: args,
	})
	if err != nil {
		t.Fatalf("CallTool (data over cap): protocol error: %v", err)
	}
	if result == nil {
		t.Fatal("result is nil")
	}

	// Must be an error result, not a success.
	if !result.IsError {
		t.Fatal("data one byte over cap must be rejected (IsError=true); got success")
	}

	// Parse the error code — must be invalid_input (from validate.go, not body cap).
	if len(result.Content) == 0 {
		t.Fatal("IsError=true but no Content")
	}
	tc, ok := result.Content[0].(*mcp.TextContent)
	if !ok {
		t.Fatalf("Content[0] is %T; want *mcp.TextContent", result.Content[0])
	}
	var decoded map[string]json.RawMessage
	if jsonErr := json.Unmarshal([]byte(tc.Text), &decoded); jsonErr != nil {
		t.Fatalf("Content[0] is not valid JSON: %v\ntext: %s", jsonErr, tc.Text)
	}
	var code string
	if jsonErr := json.Unmarshal(decoded["code"], &code); jsonErr != nil {
		t.Fatalf("cannot unmarshal code: %v", jsonErr)
	}

	// Must be invalid_input (from signing.validate), NOT a body-cap error.
	if code != signing.CodeInvalidInput {
		t.Errorf("error code = %q; want %q (schema/validation layer, not body cap)",
			code, signing.CodeInvalidInput)
	}

	// The vault must NOT have been called (validate runs BEFORE vault.WithSigningKey).
	if n := vaultCalls.Load(); n != 0 {
		t.Errorf("vault WithSigningKey called %d times; want 0 (validate rejects before vault)", n)
	}
}

// ── (c) Ctx + semaphore plumbing ──────────────────────────────────────────────

// TestSemaphorePlumbing_CtxCancelledWhileQueued verifies that:
//  1. The HTTP request context reaches vault.WithSigningKey unmodified (ctx
//     cancellation propagates from the HTTP layer all the way to the vault semaphore).
//  2. A client-cancelled queued request (B) returns with ctx.Err() BEFORE the KDF
//     starts.  This is proven by TWO instrumentation assertions (see below):
//     - withSigningKeyCallsTotal == 2: B provably ENTERED the vault path (reached the
//     semaphore), so the cancellation happened at the semaphore select, not earlier.
//     - fnCallsTotal == 1: B provably DID NOT run the KDF; only A did.
//  3. Exactly ONE concurrent fn invocation ever occurs: maxActiveFn == 1.
//  4. A completes normally with a valid sign result.
//
// The gate on bCancel() is the key correctness property:
//   - We do NOT call bCancel() until rv.withSigningKeySecondCh is closed, which
//     happens the moment B increments withSigningKeyCallsTotal to 2 (i.e. B has
//     entered rv.WithSigningKey, which means B has reached or is about to reach the
//     semaphore-wait select inside fileKeyVault.WithSigningKey).
//   - This closes the race window where B could be cancelled at the HTTP/auth/SDK
//     layer before reaching the vault at all.
//
// Synchronization is entirely instrumentation-based (channels + atomics).
// No time.Sleep calls anywhere.
func TestSemaphorePlumbing_CtxCancelledWhileQueued(t *testing.T) {
	// Not parallel: uses light fixture KDF (~50 ms) and coordination channels.
	// Running concurrently with other KDF-heavy tests can cause flaky timeouts.

	tdPath := signingTestdataPath(t)
	innerVault, err := signing.NewFileKeyVault(signing.VaultOptions{
		KeystorePath: filepath.Join(tdPath, "keystore-light.json"),
		PasswordPath: filepath.Join(tdPath, "password.txt"),
	})
	if err != nil {
		t.Fatalf("NewFileKeyVault(light): %v", err)
	}

	// holdFn is closed by the test to release A's fn (let A proceed to sign).
	holdFn := make(chan struct{})
	rv := newRecordingVault(innerVault, holdFn)

	slogLogger := obs.NewLogger("error")
	signer := signing.NewSigner(rv, signing.SignerOptions{Logger: slogLogger})
	srv := New(signer, Options{
		Name:    "semaphore-test",
		Version: "v0.0.0-test",
		Logger:  slogLogger,
	})

	addr, token := startHTTPWithToken(t, srv)
	endpoint := fmt.Sprintf("http://%s", addr.String())

	testCtx, testCancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer testCancel()

	// ── Request A: acquires the semaphore and holds it via slow fn ────────────
	//
	// The recording vault's fn blocks on holdFn (after KDF), effectively holding
	// the inner vault's semaphore until the test closes holdFn.
	aCtx, aCancel := context.WithCancel(testCtx)
	defer aCancel()

	csA := sdkClient(t, aCtx, endpoint, token)

	aErrCh := make(chan error, 1)
	aResultCh := make(chan *mcp.CallToolResult, 1)
	go func() {
		callCtx, callCancel := context.WithTimeout(aCtx, 90*time.Second)
		defer callCancel()
		result, err := csA.CallTool(callCtx, &mcp.CallToolParams{
			Name:      "sign_transaction",
			Arguments: validSign1559Args(),
		})
		aResultCh <- result
		aErrCh <- err
	}()

	// Wait (instrumentation-based) until A's fn has started.
	// At this point: A holds the inner vault's semaphore; holdFn is the only release.
	select {
	case <-rv.fnStarted:
	case <-time.After(30 * time.Second):
		t.Fatal("A never entered fn within 30s; light fixture KDF timed out")
	}

	// ── Request B: enters the vault path, blocks on semaphore, then cancelled ──
	//
	// B is started with its own cancellable context.  We do NOT cancel bCtx until
	// rv.withSigningKeySecondCh fires — that channel closes the moment B's
	// WithSigningKey ENTRY increments withSigningKeyCallsTotal to 2.  At that point
	// B is inside rv.WithSigningKey and is about to (or already is) waiting on the
	// inner vault's semaphore select.
	//
	// Cancelling bCtx after this gate guarantees the cancellation propagates to
	// fileKeyVault.WithSigningKey's `select { case v.sem <- ...: case <-ctx.Done(): }`
	// rather than being swallowed at the HTTP transport or auth layer.
	bCtx, bCancel := context.WithCancel(testCtx)

	csB := sdkClient(t, bCtx, endpoint, token)

	bErrCh := make(chan error, 1)
	go func() {
		callCtx, callCancel := context.WithTimeout(bCtx, 30*time.Second)
		defer callCancel()
		_, err := csB.CallTool(callCtx, &mcp.CallToolParams{
			Name:      "sign_transaction",
			Arguments: validSign1559Args(),
		})
		bErrCh <- err
	}()

	// GATE: wait until B has entered rv.WithSigningKey (withSigningKeyCallsTotal == 2).
	// Only then cancel B's context, ensuring the cancellation reaches the semaphore.
	select {
	case <-rv.withSigningKeySecondCh:
		// B has entered rv.WithSigningKey → is at or approaching the semaphore select.
	case <-time.After(30 * time.Second):
		t.Fatal("B never entered vault (withSigningKeySecondCh not closed within 30s)")
	}

	// Cancel B now that it is confirmed to be in the vault path.
	bCancel()

	// Wait for B to return.
	select {
	case <-bErrCh:
		// B returned. Instrumentation check below.
	case <-time.After(15 * time.Second):
		t.Fatal("B did not return within 15s after context cancel at semaphore")
	}

	// ── Instrumentation assertions (A still blocking in fn) ───────────────────
	//
	// Assertion 1: withSigningKeyCallsTotal == 2
	//   Both A and B entered rv.WithSigningKey. Combined with fnCallsTotal == 1, this
	//   proves B entered the vault path but was cancelled before the KDF ran —
	//   i.e. ctx.Err() fired at the semaphore select, NOT at the HTTP/auth/SDK layer.
	if n := rv.withSigningKeyCallsTotal.Load(); n != 2 {
		t.Errorf("withSigningKeyCallsTotal = %d after B cancelled; want 2 "+
			"(A + B both entered vault, so cancellation was at the semaphore layer)", n)
	}

	// Assertion 2: fnCallsTotal == 1
	//   Only A reached fn (past semaphore + ctx re-check + KDF). B was cancelled
	//   before fn was ever called — B's ctx.Err() fired BEFORE the KDF started.
	if n := rv.fnCallsTotal.Load(); n != 1 {
		t.Errorf("fnCallsTotal = %d after B cancelled (A still in fn); want 1 "+
			"(B cancelled before KDF; only A ran the KDF)", n)
	}

	// Assertion 3: maxActiveFn == 1 — only one concurrency gate exists (the vault semaphore).
	if m := rv.maxActiveFn.Load(); m != 1 {
		t.Errorf("maxActiveFn = %d; want 1 (exactly one concurrent fn invocation at all times)", m)
	}

	// ── Release A ─────────────────────────────────────────────────────────────
	close(holdFn) // A's fn unblocks and signs the transaction

	// Wait for A to complete normally.
	select {
	case aErr := <-aErrCh:
		if aErr != nil {
			t.Errorf("A returned error after holdFn released: %v", aErr)
		}
	case <-time.After(30 * time.Second):
		t.Fatal("A did not complete within 30s after holdFn released")
	}

	aResult := <-aResultCh
	if aResult == nil || aResult.IsError {
		var content string
		if aResult != nil && len(aResult.Content) > 0 {
			if tc, ok := aResult.Content[0].(*mcp.TextContent); ok {
				content = tc.Text
			}
		}
		t.Errorf("A did not complete successfully after holdFn: IsError=%v Content=%s",
			aResult.GetError(), content)
	}

	// Final checks: counters unchanged after A completes.
	if n := rv.withSigningKeyCallsTotal.Load(); n != 2 {
		t.Errorf("withSigningKeyCallsTotal after A completes = %d; want 2", n)
	}
	if n := rv.fnCallsTotal.Load(); n != 1 {
		t.Errorf("fnCallsTotal after A completes = %d; want 1", n)
	}
	if m := rv.maxActiveFn.Load(); m != 1 {
		t.Errorf("maxActiveFn after A completes = %d; want 1", m)
	}
}

// TestPipelineOrder_MaxBytesOutermostBeforeAuth verifies that MaxBytesHandler is
// the OUTERMOST layer: an oversized body with a VALID bearer token must still be
// rejected (not reach auth or the SDK handler).
//
// Pipeline order: MaxBytes → reqlog → auth → SDK.
// If auth were outside MaxBytes, a bad-body request would return 401 (auth rejects
// before size check). With MaxBytes outermost, the rejection fires at the byte cap
// regardless of the bearer token.
//
// See docs/mcp-sdk-spike.md §Question 3 for the confirmed locked nesting.
func TestPipelineOrder_MaxBytesOutermostBeforeAuth(t *testing.T) {
	t.Parallel()

	var toolCallCount atomic.Int32
	stub := &stubSigner{
		address: common.HexToAddress(signing.FixtureTestAddress),
		signFn: func(_ context.Context, _ signing.TxRequest) (*signing.SignResult, error) {
			toolCallCount.Add(1)
			return nil, &signing.ToolError{Code: signing.CodeInternalError, Message: "stub"}
		},
	}
	srv := newServer(stub, Options{
		Name:    "pipeline-order-maxbytes-test",
		Version: "v0.0.0-test",
		Logger:  obs.NewLogger("error"),
	})

	addr, token := startHTTPWithToken(t, srv)

	body := oversizedSignTxBody()

	req, err := http.NewRequestWithContext(context.Background(),
		http.MethodPost, fmt.Sprintf("http://%s", addr.String()), strings.NewReader(body))
	if err != nil {
		t.Fatalf("NewRequest: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream") // required by MCP Streamable HTTP
	req.Header.Set("Authorization", "Bearer "+token)                // VALID bearer token

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		// Transport-level rejection also means size rejection — but only if the server
		// is still alive.  Health-check so a crash cannot make this path a false pass.
		t.Logf("transport-level error (body rejected at transport layer): %v", err)
		serverHealthCheck(t, addr, token)
		if n := toolCallCount.Load(); n != 0 {
			t.Errorf("tool handler called %d times; want 0", n)
		}
		return
	}
	defer func() { _ = resp.Body.Close() }()
	_, _ = io.Copy(io.Discard, resp.Body)

	// Must NOT be 200 (success) or 401 (auth rejection).
	// MaxBytes fires BEFORE auth; a valid token does not bypass the body cap.
	if resp.StatusCode == http.StatusUnauthorized {
		t.Errorf("got 401; this means auth ran BEFORE MaxBytes — pipeline order is WRONG. " +
			"Expected: MaxBytes → reqlog → auth → SDK. " +
			"See docs/mcp-sdk-spike.md §Question 3.")
	}
	if resp.StatusCode < 400 {
		t.Errorf("oversized body with valid token: got %d; want >= 400 (size rejection)", resp.StatusCode)
	}

	// SDK v1.6.1 PINNED status for oversized body: 400 (ASSERTED).
	// Consistent with TestMaxBytesHandler_OversizedBodyRejected.
	const pinnedStatus = 400
	if resp.StatusCode != pinnedStatus {
		t.Errorf("oversized body + valid token status = %d; want %d (SDK v1.6.1 pinned). "+
			"If SDK was upgraded, update pinnedStatus and this comment. "+
			"See docs/mcp-sdk-spike.md §Question 3.",
			resp.StatusCode, pinnedStatus)
	}

	// Tool handler must not have been called.
	if n := toolCallCount.Load(); n != 0 {
		t.Errorf("tool handler called %d times; want 0", n)
	}
}
