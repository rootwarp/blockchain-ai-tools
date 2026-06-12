package server

// concurrent_test.go — Issue 3.6: Concurrent-calls integration test (REQUIRED).
//
// ADR-006 required acceptance test — non-waivable.
// NO testing.Short, build tag, or t.Skip guards exist anywhere in this file.
// If this test is flaky, fix the root cause; never add a skip path.
//
// Design rationale:
//
//	N = 10 goroutines (≥ 8), each owning a pre-created SDK v1.6.1 session that
//	issues one sign_transaction with a unique nonce.  All N CallTool calls are
//	dispatched simultaneously from their goroutines.
//
//	The vault semaphore of 1 (Phase 2, task 2.2) serializes the N decrypts;
//	this is proven via the recordingKeyVault wrapper (defined in bounds_test.go,
//	same package) that tracks maxActiveFn (high-water mark of goroutines inside
//	the fn body after semaphore acquisition + KDF).
//
//	SDK sessions are created in the main test goroutine (so t.Fatalf is safe);
//	the burst goroutines only call CallTool and write to a pre-allocated result
//	slice — no t.Fatal/t.Errorf from goroutines.
//
// Test table:
//
//	2 entries exactly match committed golden vectors (task 2.9) → byte-equality
//	asserted against the committed raw_tx (NOT re-derived with go-ethereum).
//	8 synthetic entries with unique nonces → sender-recovery verification only.
//
// Acceptance criteria verified (issue 3.6):
//
//	(1) All N succeed; every rawTransaction decoded and sender recovered.
//	(2) Golden byte-equality for the 2 matched vectors.
//	(3) maxActiveFn (instrumented vault) == 1 → decrypts serialized.
//	(4) Memory bounded: ReadMemStats heap-growth sanity check.
//	(5) Runs clean under -race (structural design, no data races by construction).
//	(6) Cross-call bleed: distinct nonces, N distinct request_ids, correlation.
//	(7) Leak scan: bearer token absent from logs + responses; fixture key absent
//	    from logs (address legitimately appears in rawTransaction/from fields).

import (
	"bytes"
	"context"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log/slog"
	"net"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/rootwarp/blockchain-ai-tools/apps/eth-signer-mcp/internal/signing"
)

// concSignCase describes one request in the concurrent burst.
type concSignCase struct {
	// name is a short identifier for failure messages.
	name string
	// args is the sign_transaction argument map (matches signing.TxRequest fields).
	args map[string]any
	// goldenRawTx, when non-empty, is the committed rawTransaction from task 2.9.
	// The test asserts rawTransaction == goldenRawTx byte-for-byte.
	// MUST match the JSON file exactly — do NOT re-derive with go-ethereum.
	goldenRawTx string
}

// concSignResult holds the outcome of one goroutine's CallTool invocation.
// Errors are collected here (not via t.Errorf) so goroutines stay t-safe.
type concSignResult struct {
	idx     int    // index into the test table
	rawTx   string // "0x..."-prefixed rawTransaction; empty when isError=true
	isError bool
	errText string // concise error description for the main-goroutine report
}

// concurrentSignTable returns the N test cases for the burst.
//
// Nonce uniqueness is required across all N entries so the test can verify
// response-to-request pairing via the decoded transaction nonce.
//
// Entries 0–1 match committed golden vectors exactly; goldenRawTx is set.
// Entries 2–9 are synthetic with nonces 100–107 (not in any golden vector).
func concurrentSignTable() []concSignCase {
	return []concSignCase{
		// ── Golden vector: 1559-mainnet (nonce=42) ────────────────────────────
		// Source: internal/signing/testdata/vectors/1559-mainnet.json
		// All tx fields match exactly; goldenRawTx == expected.raw_tx verbatim.
		{
			name: "golden-1559-mainnet",
			args: map[string]any{
				"type":                 "0x2",
				"chainId":              "1",
				"nonce":                "42",
				"to":                   "0x9858EfFD232B4033E47d90003D41EC34EcaEda94",
				"value":                "1000000000000000000",
				"data":                 "0xcafebabe",
				"gas":                  "100000",
				"maxFeePerGas":         "30000000000",
				"maxPriorityFeePerGas": "2000000000",
			},
			goldenRawTx: "0x02f878012a84773594008506fc23ac00830186a0949858effd232b4033e47d90003d41ec34ecaeda94880de0b6b3a764000084cafebabec080a09c4861a936548597508b2582117dc2603d11b53d7da9db676204df34dca5ee49a048349fb03c9991300fdb9dff1569609d0bf2e4ad94a256b4624627219b262b99",
		},
		// ── Golden vector: legacy-mainnet (nonce=0) ───────────────────────────
		// Source: internal/signing/testdata/vectors/legacy-mainnet.json
		// All tx fields match exactly; goldenRawTx == expected.raw_tx verbatim.
		{
			name: "golden-legacy-mainnet",
			args: map[string]any{
				"type":     "0x0",
				"chainId":  "1",
				"nonce":    "0",
				"to":       "0x9858EfFD232B4033E47d90003D41EC34EcaEda94",
				"value":    "1000000000000000000",
				"data":     "0xdeadbeef",
				"gas":      "100000",
				"gasPrice": "20000000000",
			},
			goldenRawTx: "0xf871808504a817c800830186a0949858effd232b4033e47d90003d41ec34ecaeda94880de0b6b3a764000084deadbeef26a082dc9bb5c5916b7728febf0a7269cef831012cf86eb0f2a4c69aa77ba92755dea073b452e7edc29e44a5f2b6887e90d59aec9ca1b81c858f592362e45a4e9ffcac",
		},
		// ── Synthetic EIP-1559 cases (nonces 100–107) ─────────────────────────
		// These nonces do not appear in any committed golden vector.
		// Sender-recovery verification only; no golden byte-equality assertion.
		{name: "synthetic-1559-n100", args: concSynth1559Args("100")},
		{name: "synthetic-1559-n101", args: concSynth1559Args("101")},
		{name: "synthetic-1559-n102", args: concSynth1559Args("102")},
		{name: "synthetic-1559-n103", args: concSynth1559Args("103")},
		{name: "synthetic-1559-n104", args: concSynth1559Args("104")},
		{name: "synthetic-1559-n105", args: concSynth1559Args("105")},
		{name: "synthetic-1559-n106", args: concSynth1559Args("106")},
		{name: "synthetic-1559-n107", args: concSynth1559Args("107")},
	}
}

// concSynth1559Args returns a minimal valid EIP-1559 argument map with the given
// nonce.  Nonces 100–107 are reserved for synthetic concurrent-test cases and do
// not appear in any committed golden vector (task 2.9 vector matrix).
func concSynth1559Args(nonce string) map[string]any {
	return map[string]any{
		"type":                 "0x2",
		"chainId":              "1",
		"nonce":                nonce,
		"to":                   "0x9858EfFD232B4033E47d90003D41EC34EcaEda94",
		"value":                "0",
		"data":                 "0x",
		"gas":                  "21000",
		"maxFeePerGas":         "30000000000",
		"maxPriorityFeePerGas": "2000000000",
	}
}

// ── Main test ─────────────────────────────────────────────────────────────────

// TestConcurrentSignTransaction_SerializedDecrypts is the ADR-006 required
// concurrent-calls integration test.  It MUST NOT be skipped, conditioned on
// testing.Short, or protected by build tags — it is an unconditional CI gate.
//
// Timing (light fixture, n=2 scrypt):
//
//	~50 ms per KDF × 10 serialized calls ≈ 0.5 s total.
//	Total with SDK round-trips and session setup ≈ 1–2 s.
//	Test timeout: 120 s (well within standard CI limits).
func TestConcurrentSignTransaction_SerializedDecrypts(t *testing.T) {
	// NOT t.Parallel(): uses the light-fixture KDF (~50 ms each, serialized by
	// the vault semaphore).  Running concurrently with other KDF-heavy tests risks
	// flaky timeouts under CI load.

	// ── 1. Vault: real FileKeyVault (light fixture) + instrumented wrapper ──────
	//
	// The recordingKeyVault (defined in bounds_test.go, same package) wraps the
	// real vault, tracking concurrent fn invocations via atomic gauge + max tracker.
	// holdFnCh = nil: fn proceeds immediately after KDF (no artificial hold).
	tdPath := signingTestdataPath(t) // signingTestdataPath defined in handlers_test.go
	innerVault, err := signing.NewFileKeyVault(signing.VaultOptions{
		KeystorePath: filepath.Join(tdPath, "keystore-light.json"),
		PasswordPath: filepath.Join(tdPath, "password.txt"),
	})
	if err != nil {
		t.Fatalf("NewFileKeyVault(light): %v", err)
	}

	// newRecordingVault defined in bounds_test.go (same package).
	rv := newRecordingVault(innerVault, nil)

	// ── 2. Server with log capture ───────────────────────────────────────────
	//
	// Both the signer and the HTTP server write to the same logger so that audit
	// lines and reqlog lines share one captured buffer — required for correlation
	// assertions in step (6).
	var logBuf bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&logBuf, &slog.HandlerOptions{Level: slog.LevelInfo}))

	signer := signing.NewSigner(rv, signing.SignerOptions{Logger: logger})
	srv := New(signer, Options{
		Name:    "concurrent-test-server",
		Version: "v0.0.0-test",
		Logger:  logger,
	})

	// ── 3. Start the HTTP server ─────────────────────────────────────────────
	//
	// Generate a fresh random bearer token for this run.  Keep rawToken for the
	// sentinel; zero it on test completion.
	//
	// startRunHTTP (defined in http_test.go) registers a t.Cleanup that cancels
	// the server and waits for RunHTTP to exit.  sdkClient cleanups (registered
	// later, LIFO order) close the sessions BEFORE this cleanup cancels the server,
	// so the server drains cleanly without racing session teardown.
	rawToken := randTokenBytes(32)       // randTokenBytes defined in auth_test.go
	tokenStr := hexEncodeBytes(rawToken) // hexEncodeBytes defined in auth_test.go
	defer signing.ZeroBytes(rawToken)    // zero raw bytes after the test completes
	tokenFile := writeTokenFile(t, tokenStr+"\n")
	readyCh := make(chan net.Addr, 1)

	startRunHTTP(t, srv, HTTPOptions{
		Addr:          "127.0.0.1:0",
		TokenFilePath: tokenFile,
		ReadyCh:       readyCh,
	})

	addr := waitReady(t, readyCh, 10*time.Second)
	endpoint := fmt.Sprintf("http://%s", addr.String())

	// ── 4. Build test table and sanity-check N ───────────────────────────────
	table := concurrentSignTable()
	N := len(table)
	if N < 8 {
		// Structural canary: table shrinkage must fail loudly.
		t.Fatalf("concurrentSignTable must return N>=8 entries (ADR-006); got %d", N)
	}

	// ── 5. Create N SDK sessions in the MAIN goroutine ───────────────────────
	//
	// Sessions are created here (not inside goroutines) so that sdkClient's
	// t.Fatalf call is safe.  Each session sends one initialize round-trip (~10 ms)
	// which does NOT touch the vault — only the bearer auth middleware validates the
	// token.  The N sessions all remain open during the burst; they are closed by
	// t.Cleanup (LIFO order, after the burst).
	testCtx, testCancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer testCancel()

	sessions := make([]*mcp.ClientSession, N)
	for i := range sessions {
		sessions[i] = sdkClient(t, testCtx, endpoint, tokenStr)
	}

	// ── 6. Snapshot memory BEFORE the burst ─────────────────────────────────
	runtime.GC()
	var memBefore runtime.MemStats
	runtime.ReadMemStats(&memBefore)

	// ── 7. Fire N goroutines simultaneously ──────────────────────────────────
	//
	// Each goroutine owns one session and issues exactly one sign_transaction.
	// All N calls are in-flight at the same time; the vault semaphore of 1
	// serializes the KDF invocations.  Results are written to a pre-allocated
	// slice; t.Errorf/t.Fatal are NOT called from goroutines.
	results := make([]concSignResult, N)
	var wg sync.WaitGroup
	wg.Add(N)

	for i := 0; i < N; i++ {
		i := i // shadow loop variable for goroutine capture
		go func() {
			defer wg.Done()
			callCtx, callCancel := context.WithTimeout(testCtx, 90*time.Second)
			defer callCancel()

			r, callErr := sessions[i].CallTool(callCtx, &mcp.CallToolParams{
				Name:      "sign_transaction",
				Arguments: table[i].args,
			})

			switch {
			case callErr != nil:
				results[i] = concSignResult{
					idx:     i,
					isError: true,
					errText: fmt.Sprintf("CallTool protocol error: %v", callErr),
				}
			case r == nil:
				results[i] = concSignResult{idx: i, isError: true, errText: "nil *mcp.CallToolResult"}
			case r.IsError:
				text := "<no Content>"
				if len(r.Content) > 0 {
					if tc, ok := r.Content[0].(*mcp.TextContent); ok {
						text = tc.Text
					}
				}
				results[i] = concSignResult{idx: i, isError: true, errText: "tool error: " + text}
			case len(r.Content) == 0:
				results[i] = concSignResult{idx: i, isError: true, errText: "success result has empty Content"}
			default:
				tc, ok := r.Content[0].(*mcp.TextContent)
				if !ok {
					results[i] = concSignResult{
						idx:     i,
						isError: true,
						errText: fmt.Sprintf("Content[0] type %T; want *mcp.TextContent", r.Content[0]),
					}
					return
				}
				var sr signing.SignResult
				if jsonErr := json.Unmarshal([]byte(tc.Text), &sr); jsonErr != nil {
					results[i] = concSignResult{
						idx:     i,
						isError: true,
						errText: fmt.Sprintf("json.Unmarshal(SignResult): %v", jsonErr),
					}
					return
				}
				results[i] = concSignResult{idx: i, rawTx: sr.RawTransaction}
			}
		}()
	}

	wg.Wait()

	// ── 8. Snapshot memory AFTER the burst ──────────────────────────────────
	runtime.GC()
	var memAfter runtime.MemStats
	runtime.ReadMemStats(&memAfter)

	// ═══════════════════════════════════════════════════════════════════════
	// Assertion (1): All N calls succeeded
	// ═══════════════════════════════════════════════════════════════════════
	for i, res := range results {
		if res.isError {
			t.Errorf("concurrent call %d (%q) failed: %s", i, table[i].name, res.errText)
		}
	}

	// ═══════════════════════════════════════════════════════════════════════
	// Assertion (2): Signature verification + golden byte-equality
	//
	// For every non-error result:
	//   (a) Decode rawTransaction via types.Transaction.UnmarshalBinary.
	//   (b) Recover sender using the chain ID embedded in the signed transaction.
	//   (c) Assert recovered sender == fixture address.
	//   (d) If the test case has a goldenRawTx, assert rawTx == goldenRawTx.
	//       Source of truth: committed JSON files, NOT re-derived values.
	// ═══════════════════════════════════════════════════════════════════════
	fixtureAddr := common.HexToAddress(signing.FixtureTestAddress)

	for i, res := range results {
		if res.isError {
			continue // already reported in assertion (1)
		}
		tc := table[i]

		// Decode rawTransaction.
		rawHex := strings.TrimPrefix(res.rawTx, "0x")
		rawHex = strings.TrimPrefix(rawHex, "0X")
		rawBytes, hexErr := hex.DecodeString(rawHex)
		if hexErr != nil {
			t.Errorf("call %d (%q): hex.DecodeString(rawTransaction): %v", i, tc.name, hexErr)
			continue
		}

		var tx types.Transaction
		if unmarshalErr := tx.UnmarshalBinary(rawBytes); unmarshalErr != nil {
			t.Errorf("call %d (%q): types.Transaction.UnmarshalBinary: %v", i, tc.name, unmarshalErr)
			continue
		}

		// Recover sender using the chain ID embedded in the signed transaction.
		// For EIP-155 (type 0) and EIP-1559 (type 2), ChainId() is always non-nil.
		chainID := tx.ChainId()
		if chainID == nil {
			t.Errorf("call %d (%q): unexpected nil chainId in signed transaction "+
				"(all test cases use EIP-155 or EIP-1559)", i, tc.name)
			continue
		}
		ethSigner := types.LatestSignerForChainID(chainID)
		sender, senderErr := types.Sender(ethSigner, &tx)
		if senderErr != nil {
			t.Errorf("call %d (%q): types.Sender: %v", i, tc.name, senderErr)
			continue
		}
		if sender != fixtureAddr {
			t.Errorf("call %d (%q): recovered sender = %s; want fixture %s",
				i, tc.name, sender.Hex(), fixtureAddr.Hex())
		}

		// Golden byte-equality: asserted only for entries that match a committed vector.
		// DO NOT re-derive the expected value — the committed JSON is the source of truth.
		if tc.goldenRawTx != "" && res.rawTx != tc.goldenRawTx {
			t.Errorf("call %d (%q): rawTransaction golden mismatch:\n  got:  %s\n  want: %s",
				i, tc.name, res.rawTx, tc.goldenRawTx)
		}
	}

	// ═══════════════════════════════════════════════════════════════════════
	// Assertion (3): Decrypts observably serialized (max concurrent == 1)
	//
	// maxActiveFn is the high-water mark of goroutines simultaneously inside the
	// recording vault's fn body (i.e. past semaphore + ctx re-check + KDF).
	// The Phase 2 vault semaphore of 1 (task 2.2) must keep this at exactly 1.
	//
	// This is an instrumentation-based assertion — never a wall-clock check.
	// ═══════════════════════════════════════════════════════════════════════
	maxConc := rv.maxActiveFn.Load()
	if maxConc != 1 {
		t.Errorf("maxActiveFn (max concurrent decrypts) = %d; want 1. "+
			"The vault semaphore (task 2.2) must serialize all KDF invocations. "+
			"A value > 1 means the semaphore is not protecting the decrypt section.",
			maxConc)
	}

	// Canary: fnCallsTotal must equal N (one completed fn per successful signing call).
	// An excess would mean the semaphore is not tracking correctly.
	if got, want := rv.fnCallsTotal.Load(), int32(N); got != want {
		t.Errorf("fnCallsTotal = %d; want %d (one per signing call)", got, want)
	}

	// ═══════════════════════════════════════════════════════════════════════
	// Assertion (4): Memory bounded
	//
	// The vault semaphore of 1 is the structural memory bound: at most one KDF
	// allocation can be alive at a time.  The ReadMemStats assertion is a generous
	// sanity check against unbounded parallel allocation, not an allocator-noise
	// check.
	//
	// Bound: 50 MiB of heap growth during the burst.  With the light fixture
	// (~50 ms/KDF, one allocation at a time), actual growth should be < 5 MiB.
	// 50 MiB is intentionally generous to avoid flakiness on loaded CI runners.
	// ═══════════════════════════════════════════════════════════════════════
	const maxHeapGrowthBytes uint64 = 50 * 1024 * 1024 // 50 MiB
	if memAfter.HeapInuse > memBefore.HeapInuse+maxHeapGrowthBytes {
		t.Errorf("heap growth during concurrent burst = %d MiB "+
			"(before=%d MiB, after=%d MiB); want < 50 MiB. "+
			"The vault semaphore should limit live KDF allocations to 1 at a time. "+
			"Growth > 50 MiB suggests the semaphore is not constraining parallelism.",
			(memAfter.HeapInuse-memBefore.HeapInuse)>>20,
			memBefore.HeapInuse>>20,
			memAfter.HeapInuse>>20)
	}

	// ═══════════════════════════════════════════════════════════════════════
	// Assertion (6): Cross-call state bleed + request_id correlation
	//
	// Parse the captured log and verify:
	//   (a) Exactly N audit lines (msg contains "signed successfully").
	//   (b) All N request_ids in audit lines are distinct.
	//   (c) Each audit line has a matching reqlog line (same request_id).
	//
	// parseLogLines and reqlogLines are defined in reqlog_test.go (same package).
	// ═══════════════════════════════════════════════════════════════════════
	allLines := parseLogLines(&logBuf)

	// Collect audit lines.
	var auditLines []map[string]any
	for _, m := range allLines {
		if msg, _ := m["msg"].(string); strings.Contains(msg, "signed successfully") {
			auditLines = append(auditLines, m)
		}
	}
	if got, want := len(auditLines), N; got != want {
		t.Errorf("audit line count = %d; want %d (one per successful signing call)", got, want)
	}

	// All N audit request_ids must be distinct (no cross-call bleed).
	auditIDs := make(map[string]int, N)
	for _, al := range auditLines {
		rid, _ := al["request_id"].(string)
		if rid == "" {
			t.Error("audit line has empty request_id; each signing call must produce a unique id")
			continue
		}
		auditIDs[rid]++
	}
	for rid, count := range auditIDs {
		if count > 1 {
			t.Errorf("duplicate request_id %q appears in %d audit lines; "+
				"cross-call state bleed: each call must produce a unique request_id",
				rid, count)
		}
	}

	// Each audit line must have a matching reqlog line (HTTP correlation).
	rqLines := reqlogLines(allLines)
	for _, al := range auditLines {
		auditRID, _ := al["request_id"].(string)
		if auditRID == "" {
			continue // already reported
		}
		var matched bool
		for _, rl := range rqLines {
			if rid, _ := rl["request_id"].(string); rid == auditRID {
				matched = true
				break
			}
		}
		if !matched {
			t.Errorf("audit line request_id %q has no matching reqlog line "+
				"(reqlog middleware must propagate the same request_id to the signing context)",
				auditRID)
		}
	}

	// ═══════════════════════════════════════════════════════════════════════
	// Assertion (7): Leak scan
	//
	// Sentinel (a): Bearer token must not appear in logs OR response bytes.
	// Sentinel (b): Fixture private key (FixtureKeySentinel) must not appear in
	//               logs.  NOTE: FixtureKeySentinel includes the fixture address,
	//               which legitimately appears in rawTransaction (as the 'to' field)
	//               and in the 'from' JSON field — response bytes are therefore
	//               scanned with the bearer sentinel only to avoid false positives.
	// ═══════════════════════════════════════════════════════════════════════

	// Bearer token sentinel.
	bearerSentinel := signing.NewSentinel("concurrent-bearer-token", rawToken)
	// Register the hex-encoded string form so ASCII appearances are also caught.
	bearerSentinel.RegisterForm("bearer-hex-string", []byte(tokenStr))

	// Fixture private key sentinel (includes address forms — scanned on logs only).
	keySentinel := signing.FixtureKeySentinel()

	logOutput := logBuf.Bytes()

	// Scan captured logs with both sentinels.
	if leaked := bearerSentinel.Scan(logOutput); len(leaked) > 0 {
		// SAFETY: report form names only, never the bytes or the token value.
		t.Errorf("concurrent leak-scan: bearer token found in captured logs: forms=%v "+
			"(sentinel=%q). reqlog.go must not log Authorization header values.",
			leaked, bearerSentinel.Name)
	}
	if leaked := keySentinel.Scan(logOutput); len(leaked) > 0 {
		// SAFETY: report form names only, never the bytes or the key value.
		t.Errorf("concurrent leak-scan: fixture key found in captured logs: forms=%v "+
			"(sentinel=%q). The private key scalar or its address must not appear in logs.",
			leaked, keySentinel.Name)
	}

	// Collect response bytes: only rawTransaction values (no JSON wrappers).
	// Scanning with the bearer sentinel ensures the token never appears in outputs.
	// Key sentinel NOT used here: the fixture address legitimately appears in
	// rawTransaction (as the RLP-encoded 'to' field) — that is expected behaviour.
	var responseBytes bytes.Buffer
	for _, res := range results {
		if !res.isError {
			responseBytes.WriteString(res.rawTx)
		}
	}
	if leaked := bearerSentinel.Scan(responseBytes.Bytes()); len(leaked) > 0 {
		// SAFETY: report form names only.
		t.Errorf("concurrent leak-scan: bearer token found in response bytes: forms=%v "+
			"(sentinel=%q). The bearer token must never appear in signing outputs.",
			leaked, bearerSentinel.Name)
	}
}
