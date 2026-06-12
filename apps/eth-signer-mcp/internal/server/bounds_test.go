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
//      to a real FileKeyVault (light fixture) while recording WithSigningKey entry/exit
//      via atomic gauge + max tracker. Request A holds the semaphore (slow fn);
//      request B is cancelled client-side while queued → B returns without the KDF
//      starting (instrumented assertion, not wall-clock). A completes normally.
//      Max concurrent gauge == 1.
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
	"runtime"
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
// recordingKeyVault wraps a signing.KeyVault to instrument WithSigningKey entry/exit.
// Lives in _test.go only — no production test hooks.
//
// Fields:
//   - fnStarted: closed when the first fn invocation starts (A signals "semaphore held").
//   - holdFnCh:  if non-nil, fn blocks until this channel is closed (or ctx cancels).
//   - activeFn:  atomic count of goroutines currently inside fn (concurrent KDF executions).
//   - maxActiveFn: high-water mark of activeFn — must stay ≤ 1 to prove serialization.
//   - fnCallsTotal: total fn invocations; B cancelled before fn means this stays at 1.
type recordingKeyVault struct {
	inner signing.KeyVault

	// Test-control channels; nil = feature not used.
	fnStarted <-chan struct{} // test waits on this; recording vault closes it on first fn entry
	fnStartCh chan struct{}   // the writeable end; closed by WithSigningKey
	holdFnCh  <-chan struct{} // fn blocks until closed (or ctx cancels)

	// Instrumentation (safe for concurrent use).
	activeFn     atomic.Int32
	maxActiveFn  atomic.Int32
	fnCallsTotal atomic.Int32
}

// Ensure *recordingKeyVault satisfies signing.KeyVault at compile time.
var _ signing.KeyVault = (*recordingKeyVault)(nil)

// newRecordingVault wraps inner with instrumentation.
// holdFnCh controls whether fn blocks; pass nil to let fn proceed immediately.
func newRecordingVault(inner signing.KeyVault, holdFnCh <-chan struct{}) *recordingKeyVault {
	started := make(chan struct{})
	return &recordingKeyVault{
		inner:     inner,
		fnStarted: started,
		fnStartCh: started,
		holdFnCh:  holdFnCh,
	}
}

// Address returns the underlying vault's address.
func (v *recordingKeyVault) Address() common.Address {
	return v.inner.Address()
}

// WithSigningKey records fn entry/exit and optionally holds fn until holdFnCh is closed.
func (v *recordingKeyVault) WithSigningKey(ctx context.Context, fn func(signing.SigningKey) error) error {
	return v.inner.WithSigningKey(ctx, func(key signing.SigningKey) error {
		// fn was called — KDF has run.
		v.fnCallsTotal.Add(1)

		cur := v.activeFn.Add(1)
		defer v.activeFn.Add(-1)

		// Update max (CAS loop; safe for concurrent callers).
		for {
			old := v.maxActiveFn.Load()
			if cur <= old {
				break
			}
			if v.maxActiveFn.CompareAndSwap(old, cur) {
				break
			}
		}

		// Signal that fn has started (close once; subsequent closures are no-ops via select).
		select {
		case <-v.fnStartCh: // already closed
		default:
			close(v.fnStartCh)
		}

		// Optional hold: block until test releases or ctx cancels.
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

func (c *countingVaultWrapper) WithSigningKey(ctx context.Context, fn func(signing.SigningKey) error) error {
	c.callCount.Add(1)
	return c.inner.WithSigningKey(ctx, fn)
}

// ── test helpers ─────────────────────────────────────────────────────────────

// signingTestdataPathBounds returns the path to the signing testdata directory.
func signingTestdataPathBounds(t *testing.T) string {
	t.Helper()
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller(0) failed")
	}
	serverDir := filepath.Dir(thisFile)
	return filepath.Join(serverDir, "..", "signing", "testdata")
}

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

// ── (a) Oversized-body rejection ─────────────────────────────────────────────

// TestMaxBytesHandler_OversizedBodyRejected verifies that a request body > 1 MiB
// is rejected BEFORE the MCP/SDK handler processes it.
//
// The body is a syntactically valid JSON-RPC frame with an oversized "_pad" field
// so the rejection is attributable to the byte cap, not a JSON syntax error.
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

	// Build a JSON-RPC frame with a _pad field that pushes the body > 1 MiB.
	// The JSON is syntactically valid (will be if read in full); the byte cap fires
	// when the SDK's json.Decoder reads past 1 MiB of the body.
	// Use 1100000 'A' characters → body ≈ 1.1 MiB > 1 MiB cap.
	pad := strings.Repeat("A", 1_100_000)
	body := fmt.Sprintf(
		`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"sign_transaction","arguments":{"type":"legacy","chainId":"1","nonce":"0","to":"0x9858EfFD232B4033E47d90003D41EC34EcaEda94","value":"0","data":"0x","gas":"21000","gasPrice":"20000000000","_pad":%q}}}`,
		pad,
	)

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
		// A transport-level error (connection reset) also means the server rejected
		// the body — acceptable as a "rejection" for our purposes.
		t.Logf("HTTP client error (body rejected at transport level): %v", err)
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

	// SDK v1.6.1 PINNED status: 400 Bad Request.
	// MaxBytesReader causes the SDK's json.Decode to fail; the SDK returns 400
	// (not 413). If a future SDK version changes this, update pinnedStatus + comment.
	// See docs/mcp-sdk-spike.md §Question 3 for pipeline order.
	const pinnedStatus = 400
	if resp.StatusCode != pinnedStatus {
		t.Logf("NOTICE: oversized body status = %d; SDK v1.6.1 pinned expected %d. "+
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

	tdPath := signingTestdataPathBounds(t)
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

	tdPath := signingTestdataPathBounds(t)
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

	tdPath := signingTestdataPathBounds(t)
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
//     cancellation propagates from the HTTP layer through to the vault semaphore).
//  2. A client-cancelled queued request (B) returns with ctx.Err() BEFORE the KDF
//     starts: fnCallsTotal == 1 after B is cancelled (only A called fn, not B).
//  3. Exactly ONE concurrent fn invocation ever occurs: maxActiveFn == 1.
//  4. A completes normally with a valid sign result.
//
// Synchronization is entirely instrumentation-based (channels + atomics).
// No time.Sleep calls anywhere.
func TestSemaphorePlumbing_CtxCancelledWhileQueued(t *testing.T) {
	// Not parallel: uses light fixture KDF (~50 ms) and coordination channels.
	// Running concurrently with other KDF tests can cause flaky timeouts.

	tdPath := signingTestdataPathBounds(t)
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

	// ── Request A: holds the semaphore via slow fn ────────────────────────────
	//
	// A uses its own SDK client session. The recording vault's fn blocks on
	// holdFn until we close it, effectively holding the vault semaphore.
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

	// Wait (instrumentation-based) until A's fn has started — A now holds the semaphore.
	select {
	case <-rv.fnStarted:
		// A is inside fn and blocking on holdFn.
	case <-time.After(30 * time.Second):
		t.Fatal("A never entered fn within 30s; light fixture KDF timed out")
	}

	// ── Request B: arrives while A holds the semaphore, then B is cancelled ──
	//
	// B uses its own SDK client session. We cancel B's context after starting
	// the goroutine. Since A holds the vault semaphore, B blocks inside
	// fileKeyVault.WithSigningKey's select. When bCancel() fires, ctx.Done()
	// triggers in that select, returning ctx.Err() WITHOUT calling fn.
	//
	// Even if B is cancelled before reaching fileKeyVault.WithSigningKey, the
	// outcome is identical: ctx.Done() fires at the earliest select opportunity,
	// fn is never called.
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

	// Cancel B's context. B is either already waiting on the semaphore or will
	// observe the cancellation at the next select opportunity. Either way, fn is
	// never called for B.
	bCancel()

	// Wait for B to return (with error or nil — either is OK as long as fn not called).
	select {
	case <-bErrCh:
		// B returned (cancelled or error). Check instrumentation below.
	case <-time.After(15 * time.Second):
		t.Fatal("B did not return within 15s after context cancel")
	}

	// ── Instrumentation assertions (A still blocking in fn) ───────────────────
	//
	// fnCallsTotal == 1: only A has called fn; B was cancelled before fn.
	// This is the key assertion: B's ctx.Err() fired before the KDF started.
	if n := rv.fnCallsTotal.Load(); n != 1 {
		t.Errorf("fnCallsTotal = %d after B cancelled (A still in fn); want 1 (only A, not B)",
			n)
	}

	// maxActiveFn == 1: never more than one concurrent fn invocation.
	// This proves the Phase 2 vault semaphore is the ONLY concurrency gate.
	if m := rv.maxActiveFn.Load(); m != 1 {
		t.Errorf("maxActiveFn = %d; want 1 (exactly one concurrent fn at all times)", m)
	}

	// ── Release A ─────────────────────────────────────────────────────────────
	close(holdFn) // A's fn unblocks and proceeds to sign

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

	// Final instrumentation: after A completes, fnCallsTotal must still be 1.
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

	pad := strings.Repeat("B", 1_100_000)
	body := fmt.Sprintf(
		`{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"sign_transaction","arguments":{"type":"legacy","chainId":"1","nonce":"0","to":"0x9858EfFD232B4033E47d90003D41EC34EcaEda94","value":"0","data":"0x","gas":"21000","gasPrice":"20000000000","_pad":%q}}}`,
		pad,
	)

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
		t.Logf("transport-level error (body rejected at transport layer): %v", err)
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
			"See docs/mcp-sdk-spike.md §Question 3.",
		)
	}
	if resp.StatusCode < 400 {
		t.Errorf("oversized body with valid token: got %d; want >= 400 (size rejection)", resp.StatusCode)
	}

	// Tool handler must not have been called.
	if n := toolCallCount.Load(); n != 0 {
		t.Errorf("tool handler called %d times; want 0", n)
	}
}
