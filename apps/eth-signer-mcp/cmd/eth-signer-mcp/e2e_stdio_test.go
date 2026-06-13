//go:build !windows

package main

// e2e_stdio_test.go — Issue 2.11: Stdio end-to-end test (full binary surface).
//
// This file exercises the complete Phase 2 eth-signer-mcp surface over a real
// stdio MCP session by launching the built binary as a subprocess and driving it
// via the SDK v1.6.1 mcp.CommandTransport against the keystore-light.json fixture
// (~50 ms KDF per decrypt call).
//
// Key patterns:
//   - getTestBinary(t):  build binary once via sync.Once (defined in fsperm_test.go)
//   - mcp.CommandTransport:  manages stdio pipes to the subprocess
//   - e2eTestdataPath:  resolves absolute paths to signing testdata fixtures
//   - mainCmd.Stderr captured to bytes.Buffer for audit-line/leak-scan assertions
//
// Skip under -short; total ~15 s (light-scrypt ~50 ms × several decrypt calls).
// Run:  go test ./cmd/eth-signer-mcp/ -run TestE2E_Stdio_FullSession -v
//
// Binary parity anchor:
//
//	The happy-path sign_transaction result must be byte-identical to the
//	committed legacy-mainnet.json reference vector (binary-level parity).
//
// Error-code coverage over the wire (JSON-parsed Content[0]):
//
//	invalid_input     — chainId:"0" passes schema but hits our rule-1 validator
//	unsupported_type  — type:"0x3"
//	chain_id_mismatch — second subprocess --chain-id 5, chainId-1 request
//	password_error    — subprocess with wrong password file
//	keystore_error    — startup refusal (non-zero exit + stderr assertion)
//	internal_error    — NOT force-able through the real binary without a fault
//	                    hook; covered at contract-test level in 2.7 / handlers_test.go

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/rootwarp/blockchain-ai-tools/apps/eth-signer-mcp/internal/signing"
)

// ─── syncBuffer ────────────────────────────────────────────────────────────────

// syncBuffer is a goroutine-safe bytes.Buffer replacement for subprocess stderr
// capture.  os/exec's stderr-copier goroutine writes to the buffer concurrently
// with the test goroutine reading it; bytes.Buffer is NOT safe for concurrent
// read+write (DATA RACE under -race).  A single mutex guards all access.
//
// Bytes() returns a copy of the underlying slice so callers cannot race on the
// returned slice while the copier goroutine is still writing.
type syncBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (s *syncBuffer) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.buf.Write(p)
}

// Bytes returns a copy of the accumulated bytes.  The copy is taken under the
// lock so callers get a consistent snapshot even if the subprocess is still
// running.
func (s *syncBuffer) Bytes() []byte {
	s.mu.Lock()
	defer s.mu.Unlock()
	b := s.buf.Bytes()
	out := make([]byte, len(b))
	copy(out, b)
	return out
}

// String returns the accumulated output as a string (safe for concurrent use).
func (s *syncBuffer) String() string {
	return string(s.Bytes())
}

// ─── Path helpers ──────────────────────────────────────────────────────────────

// e2eTestdataPath returns the absolute path to the signing testdata directory,
// derived from this test file's source location via runtime.Caller.
//
//	This file:  .../cmd/eth-signer-mcp/e2e_stdio_test.go
//	Testdata:   .../internal/signing/testdata/
func e2eTestdataPath(t *testing.T) string {
	t.Helper()
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("e2eTestdataPath: runtime.Caller(0) failed")
	}
	cmdDir := filepath.Dir(thisFile)
	return filepath.Join(cmdDir, "..", "..", "internal", "signing", "testdata")
}

// ─── Vector loader ─────────────────────────────────────────────────────────────

// e2eVectorTx mirrors the "tx" object in a golden vector JSON file.
type e2eVectorTx struct {
	Type                 string `json:"type"`
	ChainID              string `json:"chainId"`
	Nonce                string `json:"nonce"`
	To                   string `json:"to,omitempty"`
	Value                string `json:"value"`
	Data                 string `json:"data"`
	Gas                  string `json:"gas"`
	GasPrice             string `json:"gasPrice,omitempty"`
	MaxFeePerGas         string `json:"maxFeePerGas,omitempty"`
	MaxPriorityFeePerGas string `json:"maxPriorityFeePerGas,omitempty"`
}

// e2eVector holds the fields of a golden signing vector used in e2e assertions.
type e2eVector struct {
	Tx       e2eVectorTx `json:"tx"`
	Expected struct {
		RawTx  string `json:"raw_tx"`
		TxHash string `json:"tx_hash"`
	} `json:"expected"`
}

// loadE2EVector reads and parses a signing vector file from testdata/vectors/.
func loadE2EVector(t *testing.T, tdPath, name string) e2eVector {
	t.Helper()
	path := filepath.Join(tdPath, "vectors", name)
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("loadE2EVector %q: %v", name, err)
	}
	var v e2eVector
	if err := json.Unmarshal(data, &v); err != nil {
		t.Fatalf("loadE2EVector %q: unmarshal: %v", name, err)
	}
	return v
}

// vectorTxToArgs converts an e2eVectorTx to the Arguments map for mcp.CallToolParams.
// Fields with empty string values (declared omitempty) are omitted from the map.
func vectorTxToArgs(tx e2eVectorTx) map[string]any {
	m := map[string]any{
		"type":    tx.Type,
		"chainId": tx.ChainID,
		"nonce":   tx.Nonce,
		"value":   tx.Value,
		"data":    tx.Data,
		"gas":     tx.Gas,
	}
	if tx.To != "" {
		m["to"] = tx.To
	}
	if tx.GasPrice != "" {
		m["gasPrice"] = tx.GasPrice
	}
	if tx.MaxFeePerGas != "" {
		m["maxFeePerGas"] = tx.MaxFeePerGas
	}
	if tx.MaxPriorityFeePerGas != "" {
		m["maxPriorityFeePerGas"] = tx.MaxPriorityFeePerGas
	}
	return m
}

// ─── Wire-error assertion helper ───────────────────────────────────────────────

// assertToolErrorCode asserts that a CallTool response encodes a ToolError with
// the expected code.  Checks:
//   - callErr is nil   (no protocol-level transport failure)
//   - result.IsError == true
//   - Content[0] is a *mcp.TextContent
//   - Its text is valid JSON with exactly the keys "code" and "message"
//   - The "code" field equals wantCode
func assertToolErrorCode(
	t *testing.T,
	result *mcp.CallToolResult,
	callErr error,
	wantCode string,
) {
	t.Helper()
	if callErr != nil {
		t.Fatalf("assertToolErrorCode(%q): got protocol error %v; want tool-level error result",
			wantCode, callErr)
	}
	if result == nil {
		t.Fatalf("assertToolErrorCode(%q): result is nil; want IsError=true result", wantCode)
	}
	if !result.IsError {
		t.Fatalf("assertToolErrorCode(%q): IsError=false; want true", wantCode)
	}
	if len(result.Content) == 0 {
		t.Fatalf("assertToolErrorCode(%q): Content is empty; want one TextContent", wantCode)
	}
	tc, ok := result.Content[0].(*mcp.TextContent)
	if !ok {
		t.Fatalf("assertToolErrorCode(%q): Content[0] is %T; want *mcp.TextContent",
			wantCode, result.Content[0])
	}
	var decoded map[string]json.RawMessage
	if err := json.Unmarshal([]byte(tc.Text), &decoded); err != nil {
		t.Fatalf("assertToolErrorCode(%q): Content[0].Text is not valid JSON: %v\ntext: %s",
			wantCode, err, tc.Text)
	}
	if len(decoded) != 2 {
		t.Errorf("assertToolErrorCode(%q): JSON has %d keys; want exactly 2 (code, message)\ntext: %s",
			wantCode, len(decoded), tc.Text)
	}
	for _, k := range []string{"code", "message"} {
		if _, ok := decoded[k]; !ok {
			t.Errorf("assertToolErrorCode(%q): JSON missing key %q\ntext: %s", wantCode, k, tc.Text)
		}
	}
	var gotCode string
	if err := json.Unmarshal(decoded["code"], &gotCode); err != nil {
		t.Fatalf("assertToolErrorCode(%q): unmarshal code: %v", wantCode, err)
	}
	if gotCode != wantCode {
		t.Errorf("assertToolErrorCode: code = %q; want %q\ntext: %s", gotCode, wantCode, tc.Text)
	}
}

// ─── Full-session E2E test ──────────────────────────────────────────────────────

// TestE2E_Stdio_FullSession drives a complete MCP stdio session against the real
// eth-signer-mcp binary using the keystore-light.json fixture (~50 ms/decrypt).
//
// Sequence (all in one test function per issue spec):
//  1. initialize:            server name "eth-signer-mcp", version present.
//  2. tools/list:            exactly sign_transaction + get_address; additionalProperties:false.
//  3. get_address:           returns checksummed fixture address.
//  4. sign_transaction:      happy path; result byte-identical to legacy-mainnet.json reference.
//  5. Error paths (each via JSON-parsed Content[0] for {code, message}):
//     5a. invalid_input     — chainId:"0" (rule-1 validator, passes schema)
//     5b. unsupported_type  — type:"0x3"
//     5c. chain_id_mismatch — second subprocess --chain-id 5, chainId-1 request
//     5d. password_error    — subprocess with wrong password file
//     5e. keystore_error    — startup refusal (non-zero exit + stderr check)
//     note: internal_error not force-able without fault hook; see 2.7 / handlers_test.go
//  6. Audit-line + sentinel leak-scan over captured stderr.
//  7. stdin EOF → subprocess exits 0 (clean shutdown).
//
// Skipped under -short.  Total runtime ~15 s.
func TestE2E_Stdio_FullSession(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping stdio e2e test under -short (requires ~15s for light-scrypt decrypts)")
	}

	bin := getTestBinary(t)
	tdPath := e2eTestdataPath(t)

	keystorePath := filepath.Join(tdPath, "keystore-light.json")
	passwordPath := filepath.Join(tdPath, "password.txt")

	// Load the legacy-mainnet golden vector for binary-parity assertion (Step 4).
	mainnetVec := loadE2EVector(t, tdPath, "legacy-mainnet.json")

	// ── Main session ─────────────────────────────────────────────────────────
	// Steps 1-5b run in a single long-lived session against the real binary.
	// Stderr is captured to mainStderr for the Step-6 audit/leak assertions.

	mainCtx, mainCancel := context.WithTimeout(context.Background(), 30*time.Second)
	t.Cleanup(mainCancel)

	var mainStderr syncBuffer
	mainCmd := exec.CommandContext(mainCtx, bin,
		"--keystore", keystorePath,
		"--password-file", passwordPath,
	)
	mainCmd.Stderr = &mainStderr

	mainClient := mcp.NewClient(
		&mcp.Implementation{Name: "e2e-test-client", Version: "v0.0.1"},
		nil,
	)
	mainCS, err := mainClient.Connect(mainCtx, &mcp.CommandTransport{Command: mainCmd}, nil)
	if err != nil {
		t.Fatalf("main session Connect: %v", err)
	}
	// Dump stderr on test failure for diagnostics.
	t.Cleanup(func() {
		if t.Failed() {
			t.Logf("main session captured stderr:\n%s", mainStderr.String())
		}
	})

	// ── Step 1: initialize ────────────────────────────────────────────────────
	initResult := mainCS.InitializeResult()
	if initResult == nil {
		t.Fatal("step 1: InitializeResult() returned nil")
	}
	if initResult.ServerInfo == nil {
		t.Fatal("step 1: InitializeResult.ServerInfo is nil")
	}
	if got := initResult.ServerInfo.Name; got != "eth-signer-mcp" {
		t.Errorf("step 1: ServerInfo.Name = %q; want %q", got, "eth-signer-mcp")
	}
	if initResult.ServerInfo.Version == "" {
		t.Error("step 1: ServerInfo.Version is empty; want non-empty")
	}

	// ── Step 2: tools/list ────────────────────────────────────────────────────
	toolsResult, err := mainCS.ListTools(mainCtx, nil)
	if err != nil {
		t.Fatalf("step 2: ListTools: %v", err)
	}
	if len(toolsResult.Tools) != 2 {
		names := make([]string, len(toolsResult.Tools))
		for i, tt := range toolsResult.Tools {
			names[i] = tt.Name
		}
		t.Fatalf("step 2: len(Tools) = %d; want 2. Got: %v", len(toolsResult.Tools), names)
	}
	toolMap := make(map[string]*mcp.Tool, 2)
	for _, tt := range toolsResult.Tools {
		toolMap[tt.Name] = tt
	}
	for _, name := range []string{"sign_transaction", "get_address"} {
		if _, ok := toolMap[name]; !ok {
			t.Errorf("step 2: tool %q missing from tools/list", name)
		}
	}
	// Assert sign_transaction InputSchema has additionalProperties:false.
	if signTool := toolMap["sign_transaction"]; signTool != nil {
		schemaJSON, _ := json.Marshal(signTool.InputSchema)
		var schema map[string]json.RawMessage
		if jsonErr := json.Unmarshal(schemaJSON, &schema); jsonErr == nil {
			if ap, exists := schema["additionalProperties"]; !exists {
				t.Error("step 2: sign_transaction InputSchema missing 'additionalProperties'")
			} else {
				var apVal bool
				if jsonErr := json.Unmarshal(ap, &apVal); jsonErr != nil || apVal {
					t.Errorf("step 2: sign_transaction InputSchema.additionalProperties = %s; want false", ap)
				}
			}
		}
	}

	// ── Step 3: get_address ───────────────────────────────────────────────────
	{
		callCtx, callCancel := context.WithTimeout(mainCtx, 10*time.Second)
		addrResult, addrErr := mainCS.CallTool(callCtx, &mcp.CallToolParams{
			Name:      "get_address",
			Arguments: map[string]any{},
		})
		callCancel()
		if addrErr != nil {
			t.Fatalf("step 3: get_address protocol error: %v", addrErr)
		}
		if addrResult == nil || addrResult.IsError {
			t.Fatalf("step 3: get_address returned IsError=true or nil result")
		}
		if len(addrResult.Content) == 0 {
			t.Fatal("step 3: get_address Content is empty")
		}
		addrTC, ok := addrResult.Content[0].(*mcp.TextContent)
		if !ok {
			t.Fatalf("step 3: get_address Content[0] is %T; want *mcp.TextContent",
				addrResult.Content[0])
		}
		var addrRes signing.AddressResult
		if jsonErr := json.Unmarshal([]byte(addrTC.Text), &addrRes); jsonErr != nil {
			t.Fatalf("step 3: unmarshal AddressResult: %v\ntext: %s", jsonErr, addrTC.Text)
		}
		if addrRes.Address != signing.FixtureTestAddress {
			t.Errorf("step 3: get_address = %q; want %q", addrRes.Address, signing.FixtureTestAddress)
		}
	}

	// ── Step 4: sign_transaction happy path (binary-level parity) ─────────────
	{
		callCtx, callCancel := context.WithTimeout(mainCtx, 10*time.Second)
		signResult, signErr := mainCS.CallTool(callCtx, &mcp.CallToolParams{
			Name:      "sign_transaction",
			Arguments: vectorTxToArgs(mainnetVec.Tx),
		})
		callCancel()
		if signErr != nil {
			t.Fatalf("step 4: sign_transaction protocol error: %v", signErr)
		}
		if signResult == nil || signResult.IsError {
			t.Fatalf("step 4: sign_transaction returned error result; Content: %v",
				signResult.Content)
		}
		if len(signResult.Content) == 0 {
			t.Fatal("step 4: sign_transaction Content is empty")
		}
		signTC, ok := signResult.Content[0].(*mcp.TextContent)
		if !ok {
			t.Fatalf("step 4: sign_transaction Content[0] is %T; want *mcp.TextContent",
				signResult.Content[0])
		}
		var gotSign signing.SignResult
		if jsonErr := json.Unmarshal([]byte(signTC.Text), &gotSign); jsonErr != nil {
			t.Fatalf("step 4: unmarshal SignResult: %v\ntext: %s", jsonErr, signTC.Text)
		}
		// Binary-parity: rawTransaction must be byte-identical to the committed reference.
		if gotSign.RawTransaction != mainnetVec.Expected.RawTx {
			t.Errorf("step 4: RawTransaction mismatch:\n  got:  %s\n  want: %s",
				gotSign.RawTransaction, mainnetVec.Expected.RawTx)
		}
		if gotSign.Hash != mainnetVec.Expected.TxHash {
			t.Errorf("step 4: Hash mismatch:\n  got:  %s\n  want: %s",
				gotSign.Hash, mainnetVec.Expected.TxHash)
		}
		if gotSign.From != signing.FixtureTestAddress {
			t.Errorf("step 4: From = %q; want %q", gotSign.From, signing.FixtureTestAddress)
		}
	}

	// ── Step 5a: invalid_input ────────────────────────────────────────────────
	// chainId:"0" passes JSON schema validation (it's a valid string) but triggers
	// our rule-1 validator: chainId==0 is replay-unprotected → invalid_input.
	{
		callCtx, callCancel := context.WithTimeout(mainCtx, 10*time.Second)
		invResult, invErr := mainCS.CallTool(callCtx, &mcp.CallToolParams{
			Name: "sign_transaction",
			Arguments: map[string]any{
				"type":     "0x0",
				"chainId":  "0", // rule-1: chainId==0 → invalid_input
				"nonce":    "0",
				"value":    "0",
				"data":     "0x",
				"gas":      "21000",
				"gasPrice": "20000000000",
			},
		})
		callCancel()
		assertToolErrorCode(t, invResult, invErr, signing.CodeInvalidInput)
	}

	// ── Step 5b: unsupported_type ─────────────────────────────────────────────
	// type:"0x3" passes JSON schema validation (any string is accepted) but our
	// rule-3 validator maps it to unsupported_type (types 1, 3, 4 are Phase 2).
	{
		callCtx, callCancel := context.WithTimeout(mainCtx, 10*time.Second)
		unsuppResult, unsuppErr := mainCS.CallTool(callCtx, &mcp.CallToolParams{
			Name: "sign_transaction",
			Arguments: map[string]any{
				"type":    "0x3",
				"chainId": "1",
				"nonce":   "0",
				"value":   "0",
				"data":    "0x",
				"gas":     "21000",
				// No gasPrice / 1559 fee fields: rule-3 fires before rule-4.
			},
		})
		callCancel()
		assertToolErrorCode(t, unsuppResult, unsuppErr, signing.CodeUnsupportedType)
	}

	// ── Step 7: Clean shutdown ────────────────────────────────────────────────
	// Closing the client session sends EOF to the subprocess stdin.
	// RunStdio returns nil on EOF → process exits 0.
	mainCloseErr := mainCS.Close()
	if mainCloseErr != nil && !isClosedPipeError(mainCloseErr) {
		t.Errorf("step 7: mainCS.Close: %v", mainCloseErr)
	}
	// Assert exit 0. The SDK's pipeRWC.Close() calls cmd.Wait() synchronously on
	// the normal path, populating ProcessState before returning. On the early-return
	// path (stdin.Close() error), cmd.Wait() is not called internally — so we call
	// it here to guarantee ProcessState is always set. If the SDK already called
	// it, Go returns "exec: Wait was already called" (not an ExitError), in which
	// case we fall back to the already-populated ProcessState.
	{
		waitErr := mainCmd.Wait()
		var ee *exec.ExitError
		if errors.As(waitErr, &ee) {
			// cmd.Wait() returned the exit status directly (early-return path).
			if ee.ExitCode() != 0 {
				t.Errorf("step 7: subprocess exit code = %d; want 0 (stdin EOF → clean shutdown)", ee.ExitCode())
			}
		} else if waitErr == nil {
			// cmd.Wait() returned nil: exit 0 confirmed.
		} else if isClosedPipeError(waitErr) || strings.Contains(waitErr.Error(), "Wait was already called") {
			// SDK already called cmd.Wait(); ProcessState is populated. Use it.
			if ps := mainCmd.ProcessState; ps != nil && ps.ExitCode() != 0 {
				t.Errorf("step 7: subprocess exit code = %d; want 0 (stdin EOF → clean shutdown)", ps.ExitCode())
			}
		} else {
			t.Errorf("step 7: cmd.Wait: %v", waitErr)
		}
	}

	// ── Step 6: Audit-line and leak-scan assertions ────────────────────────────
	// Run AFTER Close() so the subprocess has fully exited and stderr is complete.
	stderrBytes := mainStderr.Bytes()

	// Find the single audit line emitted by the signer for successful signing.
	// The signer.go audit line has msg "signing: transaction signed successfully"
	// with fields: request_id, tx_hash, chain_id, nonce.
	var auditLine map[string]any
	for _, line := range bytes.Split(stderrBytes, []byte("\n")) {
		line = bytes.TrimSpace(line)
		if len(line) == 0 {
			continue
		}
		var m map[string]any
		if jsonErr := json.Unmarshal(line, &m); jsonErr != nil {
			continue // skip non-JSON lines
		}
		msg, _ := m["msg"].(string)
		if strings.Contains(msg, "signed successfully") {
			if auditLine != nil {
				t.Error("step 6: found more than one audit line for successful signing; want exactly one")
			}
			auditLine = m
		}
	}
	if auditLine == nil {
		t.Fatalf("step 6: no audit line found in captured stderr\nstderr:\n%s", stderrBytes)
	}
	// All four required audit fields must be present.
	for _, field := range []string{"request_id", "tx_hash", "chain_id", "nonce"} {
		if _, ok := auditLine[field]; !ok {
			t.Errorf("step 6: audit line missing required field %q", field)
		}
	}
	// Tx body values must NOT appear in any log line (defensive privacy).
	// Note: mainnetVec.Tx.To is the same as the keystore fixture address, so it
	// legitimately appears in the "keystore loaded" startup log — we do not check it.
	// We check value and calldata bytes (truly distinctive, never appear in other logs).
	txBodyValues := []string{
		mainnetVec.Tx.Value, // "1000000000000000000" (1 ETH)
		"deadbeef",          // mainnetVec.Tx.Data = "0xdeadbeef", calldata bytes without 0x
	}
	stderrStr := string(stderrBytes)
	for _, v := range txBodyValues {
		if v != "" && strings.Contains(stderrStr, v) {
			t.Errorf("step 6: stderr contains tx body value %q; must not appear in any log line", v)
		}
	}
	// Sentinel leak scan: raw + encoded forms of the fixture private key must be
	// absent from captured stderr.  Reports form names (not bytes) per the
	// sanitised-failure-message rule in sentinel.go.
	//
	// Address forms exclusion: FixtureKeySentinel() registers the EIP-55
	// checksummed address as an additional sentinel form.  In this e2e context the
	// keystore address legitimately appears in the "keystore loaded" startup log
	// line (main.go: logger.Info("keystore loaded", "address", vault.Address().Hex())).
	// That is expected behaviour — not a private-key leak.  We therefore skip the
	// address-* forms here, which are only meaningful in isolated unit-test log
	// buffers that do not include startup output.  All raw private-key forms
	// (raw bytes, hex, base64, decimal) are still asserted.
	{
		sentinel := signing.FixtureKeySentinel()
		leaked := sentinel.Scan(stderrBytes)
		var keyLeaks []string
		for _, form := range leaked {
			if form == "address-checksummed" || form == "address-lower-nox" {
				continue // address legitimately present in startup logs
			}
			keyLeaks = append(keyLeaks, form)
		}
		if len(keyLeaks) > 0 {
			t.Errorf("step 6: fixture private key material leaked in stderr in form(s): %v", keyLeaks)
		}
	}

	// ── Step 5c: chain_id_mismatch ────────────────────────────────────────────
	// A second subprocess with --chain-id 5 guards against non-5 chainIds.
	// Sending the legacy-mainnet tx (chainId:"1") triggers chain_id_mismatch.
	{
		chainCtx, chainCancel := context.WithTimeout(context.Background(), 15*time.Second)
		t.Cleanup(chainCancel)

		chainCmd := exec.CommandContext(chainCtx, bin,
			"--keystore", keystorePath,
			"--password-file", passwordPath,
			"--chain-id", "5",
		)
		var chainStderr syncBuffer
		chainCmd.Stderr = &chainStderr
		t.Cleanup(func() {
			if t.Failed() {
				t.Logf("step 5c (chain_id_mismatch) subprocess stderr:\n%s", chainStderr.String())
			}
		})
		chainClient := mcp.NewClient(
			&mcp.Implementation{Name: "e2e-chain-id-client", Version: "v0.0.1"},
			nil,
		)
		chainCS, chainConnErr := chainClient.Connect(
			chainCtx, &mcp.CommandTransport{Command: chainCmd}, nil,
		)
		if chainConnErr != nil {
			t.Fatalf("step 5c (chain_id_mismatch): Connect: %v", chainConnErr)
		}
		t.Cleanup(func() {
			if err := chainCS.Close(); err != nil && !isClosedPipeError(err) {
				t.Logf("step 5c cleanup: chainCS.Close: %v", err)
			}
		})

		callCtx, callCancel := context.WithTimeout(chainCtx, 10*time.Second)
		mismatchResult, mismatchErr := chainCS.CallTool(callCtx, &mcp.CallToolParams{
			Name:      "sign_transaction",
			Arguments: vectorTxToArgs(mainnetVec.Tx), // chainId:"1", guard=5 → mismatch
		})
		callCancel()
		assertToolErrorCode(t, mismatchResult, mismatchErr, signing.CodeChainIDMismatch)
		// Sentinel scan: key material must not appear in chain-id subprocess stderr.
		{
			leaked := signing.FixtureKeySentinel().Scan(chainStderr.Bytes())
			var keyLeaks []string
			for _, form := range leaked {
				if form == "address-checksummed" || form == "address-lower-nox" {
					continue // address legitimately present in startup logs
				}
				keyLeaks = append(keyLeaks, form)
			}
			if len(keyLeaks) > 0 {
				t.Errorf("step 5c: fixture private key material leaked in chain-id subprocess stderr in form(s): %v", keyLeaks)
			}
		}
	}

	// ── Step 5d: password_error ───────────────────────────────────────────────
	// A subprocess launched with a wrong password file. Vault construction succeeds
	// (the password file is only read at signing time), but the signing call fails
	// with password_error (ErrDecrypt from go-ethereum's keystore).
	{
		wrongPwPath := filepath.Join(t.TempDir(), "wrong-password.txt")
		if err := os.WriteFile(wrongPwPath, []byte("definitely-wrong-password\n"), 0o600); err != nil {
			t.Fatalf("step 5d: write wrong password file: %v", err)
		}

		pwCtx, pwCancel := context.WithTimeout(context.Background(), 15*time.Second)
		t.Cleanup(pwCancel)

		pwCmd := exec.CommandContext(pwCtx, bin,
			"--keystore", keystorePath,
			"--password-file", wrongPwPath,
		)
		var pwStderr syncBuffer
		pwCmd.Stderr = &pwStderr
		t.Cleanup(func() {
			if t.Failed() {
				t.Logf("step 5d (password_error) subprocess stderr:\n%s", pwStderr.String())
			}
		})
		pwClient := mcp.NewClient(
			&mcp.Implementation{Name: "e2e-pw-client", Version: "v0.0.1"},
			nil,
		)
		pwCS, pwConnErr := pwClient.Connect(
			pwCtx, &mcp.CommandTransport{Command: pwCmd}, nil,
		)
		if pwConnErr != nil {
			t.Fatalf("step 5d (password_error): Connect: %v", pwConnErr)
		}
		t.Cleanup(func() {
			if err := pwCS.Close(); err != nil && !isClosedPipeError(err) {
				t.Logf("step 5d cleanup: pwCS.Close: %v", err)
			}
		})

		callCtx, callCancel := context.WithTimeout(pwCtx, 10*time.Second)
		pwResult, pwErr := pwCS.CallTool(callCtx, &mcp.CallToolParams{
			Name:      "sign_transaction",
			Arguments: vectorTxToArgs(mainnetVec.Tx),
		})
		callCancel()
		assertToolErrorCode(t, pwResult, pwErr, signing.CodePasswordError)
		// Sentinel scan: key material must not appear in password-error subprocess stderr.
		// This is the highest-risk path: vault.WithSigningKey attempts decryption before
		// returning ErrDecrypt. Any verbose error wrapping that captured intermediate state
		// would show up here.
		{
			leaked := signing.FixtureKeySentinel().Scan(pwStderr.Bytes())
			var keyLeaks []string
			for _, form := range leaked {
				if form == "address-checksummed" || form == "address-lower-nox" {
					continue // address legitimately present in startup logs
				}
				keyLeaks = append(keyLeaks, form)
			}
			if len(keyLeaks) > 0 {
				t.Errorf("step 5d: fixture private key material leaked in password-error subprocess stderr in form(s): %v", keyLeaks)
			}
		}
	}

	// ── Step 5e: keystore_error (startup refusal) ──────────────────────────────
	// A subprocess pointed at a malformed keystore must exit non-zero before the
	// MCP session is established (boot-time keystore_error). Use a temp bad-JSON
	// (no-address fixture no longer triggers error at startup; address is optional).
	// We run the command directly without the SDK transport.
	//
	// NOTE: internal_error is NOT force-able through the real binary without a
	// fault hook (e.g. a key whose recovered sender differs from vault.Address()).
	// It is covered at the contract-test level in Issue 2.7 /
	// TestSignTransaction_SixCodesWireEncoding in handlers_test.go.
	{
		ksCtx, ksCancel := context.WithTimeout(context.Background(), 10*time.Second)
		t.Cleanup(ksCancel)

		badKs := filepath.Join(t.TempDir(), "bad-for-keystore-error.json")
		if werr := os.WriteFile(badKs, []byte("{not-json-keystore}"), 0o600); werr != nil {
			t.Fatalf("step 5e: write bad ks: %v", werr)
		}

		ksCmd := exec.CommandContext(ksCtx, bin,
			"--keystore", badKs,
			"--password-file", passwordPath,
		)
		var ksStderr syncBuffer
		ksCmd.Stderr = &ksStderr

		ksRunErr := ksCmd.Run()
		if ksRunErr == nil {
			t.Fatal("step 5e: binary exited 0; want non-zero (bad keystore)")
		}
		var exitErr *exec.ExitError
		if !errors.As(ksRunErr, &exitErr) {
			t.Fatalf("step 5e: unexpected error type %T: %v", ksRunErr, ksRunErr)
		}
		if exitErr.ExitCode() == 0 {
			t.Error("step 5e: exit code = 0; want non-zero for bad keystore")
		}
		// stderr must carry the "keystore_error" code string, emitted by:
		//   fmt.Fprintf(os.Stderr, "...: %v\n", err) where err.Error() contains the code.
		ksStderrStr := ksStderr.String()
		if !strings.Contains(ksStderrStr, "keystore_error") {
			t.Errorf("step 5e: stderr does not contain %q\nstderr: %s",
				"keystore_error", ksStderrStr)
		}
		// Sentinel scan: key material must not appear in keystore-error subprocess stderr.
		{
			leaked := signing.FixtureKeySentinel().Scan(ksStderr.Bytes())
			var keyLeaks []string
			for _, form := range leaked {
				if form == "address-checksummed" || form == "address-lower-nox" {
					continue // address legitimately present in startup logs (if any)
				}
				keyLeaks = append(keyLeaks, form)
			}
			if len(keyLeaks) > 0 {
				t.Errorf("step 5e: fixture private key material leaked in keystore-error subprocess stderr in form(s): %v", keyLeaks)
			}
		}
	}
}
