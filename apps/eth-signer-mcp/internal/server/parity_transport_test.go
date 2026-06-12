//go:build !windows

package server

// parity_transport_test.go — Issue 3.8: stdio/HTTP transport parity.
//
// Proves ADR-002: one server, two transports — identical outputs on both.
//
// For ≥1 legacy and ≥1 EIP-1559 golden vector, asserts:
//   - sign_transaction results are BYTE-IDENTICAL across stdio and HTTP transports
//     (rawTransaction, r, s, v, hash, from all equal), AND byte-identical to the
//     committed golden.  The golden vectors are the SINGLE SOURCE OF TRUTH; we do
//     NOT re-derive expected outputs with go-ethereum calls.
//   - tools/list schema documents are DEEP-EQUAL across stdio and HTTP.
//   - Captured stderr from both transports passes the fixture key leak scan
//     (address forms excluded; they legitimately appear in startup logs).
//   - HTTP-side stderr shows a reqlog line + audit line sharing one request_id.
//
// Reused helpers (all in package server):
//   getE2EBinary       — http_e2e_test.go (binary build, sync.Once)
//   launchHTTPBinary   — http_e2e_test.go (HTTP subprocess + announce scrape)
//   syncBuffer         — http_e2e_test.go (goroutine-safe stderr capture)
//   loadGoldenRawTx    — concurrent_test.go (committed raw_tx from JSON vector file)
//   sdkClient          — bounds_test.go (SDK v1.6.1 session + bearer round-tripper)
//   signingTestdataPath — handlers_test.go (testdata directory path)
//   writeTokenFile     — http_test.go
//   randTokenBytes     — auth_test.go
//   hexEncodeBytes     — auth_test.go
//   parseLogLines      — reqlog_test.go
//   reqlogLines        — reqlog_test.go
//
// Skipped under -short.

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/rootwarp/blockchain-ai-tools/apps/eth-signer-mcp/internal/signing"
)

// ─── Vector types ────────────────────────────────────────────────────────────────

// parityVectorTx mirrors the "tx" object in a golden vector JSON file.
// Field names match the JSON keys and the signing.TxRequest argument map.
type parityVectorTx struct {
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

// parityVectorExpected mirrors the "expected" object in a golden vector JSON file.
type parityVectorExpected struct {
	RawTx  string `json:"raw_tx"`
	TxHash string `json:"tx_hash"`
	R      string `json:"r"`
	S      string `json:"s"`
	V      string `json:"v"`
}

// parityVector holds all fields of a golden signing vector used in parity assertions.
type parityVector struct {
	Name     string               `json:"name"`
	Tx       parityVectorTx       `json:"tx"`
	Expected parityVectorExpected `json:"expected"`
}

// loadParityVector reads and parses a golden vector file from testdata/vectors/.
// vectorFile is the bare filename (e.g. "legacy-mainnet.json").
//
// The parsed file path is resolved from this file's runtime location — never from
// os.Getwd().  signingTestdataPath (handlers_test.go) provides the base directory.
func loadParityVector(t *testing.T, vectorFile string) parityVector {
	t.Helper()
	tdPath := signingTestdataPath(t) // handlers_test.go
	path := filepath.Join(tdPath, "vectors", vectorFile)
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("loadParityVector %q: %v", vectorFile, err)
	}
	var v parityVector
	if jsonErr := json.Unmarshal(data, &v); jsonErr != nil {
		t.Fatalf("loadParityVector %q: json.Unmarshal: %v", vectorFile, jsonErr)
	}
	if v.Expected.RawTx == "" {
		t.Fatalf("loadParityVector %q: expected.raw_tx is empty", vectorFile)
	}
	return v
}

// parityTxToArgs converts a parityVectorTx to the Arguments map for mcp.CallToolParams.
// Fields with empty string values (declared omitempty) are omitted from the map.
func parityTxToArgs(tx parityVectorTx) map[string]any {
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

// ─── Parity signing table ─────────────────────────────────────────────────────────

// parityCase is one entry in the parity test table.
type parityCase struct {
	name   string
	vector parityVector
}

// parityVectorTable returns the table of vectors for the parity test.
// MUST include at least one legacy (type 0x0) and one EIP-1559 (type 0x2) vector.
func parityVectorTable(t *testing.T) []parityCase {
	t.Helper()
	return []parityCase{
		{
			name:   "legacy-mainnet",
			vector: loadParityVector(t, "legacy-mainnet.json"),
		},
		{
			name:   "1559-mainnet",
			vector: loadParityVector(t, "1559-mainnet.json"),
		},
	}
}

// ─── Parity: sign_transaction result ─────────────────────────────────────────────

// TestTransportParity_SignResult proves ADR-002 byte-level parity for the
// sign_transaction tool: for ≥1 legacy and ≥1 EIP-1559 golden vector, the stdio
// and HTTP SignResults are BYTE-IDENTICAL to each other AND to the committed golden
// (rawTransaction, r, s, v, hash, from).
//
// The golden vectors are the SINGLE SOURCE OF TRUTH — expected outputs are read
// from the committed JSON files, never re-derived with go-ethereum calls.
//
// Skipped under -short.
func TestTransportParity_SignResult(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping parity test under -short (requires ~10 s for light-scrypt decrypts)")
	}
	// Not t.Parallel(): uses the light-fixture KDF.

	bin := getE2EBinary(t)           // http_e2e_test.go
	tdPath := signingTestdataPath(t) // handlers_test.go
	ks := filepath.Join(tdPath, "keystore-light.json")
	pw := filepath.Join(tdPath, "password.txt")

	table := parityVectorTable(t)
	if len(table) < 2 {
		t.Fatalf("parityVectorTable must return ≥2 entries (≥1 legacy + ≥1 EIP-1559); got %d",
			len(table))
	}

	testCtx, testCancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer testCancel()

	// ── HTTP subprocess (one per test, shared across all table entries) ───────────
	httpRawToken := randTokenBytes(32) // auth_test.go
	httpTokenStr := hexEncodeBytes(httpRawToken)
	defer signing.ZeroBytes(httpRawToken)
	httpTokenFile := writeTokenFile(t, httpTokenStr+"\n") // http_test.go

	// Register proc cleanup BEFORE sdkClient (LIFO: session closes before Kill).
	httpProc := launchHTTPBinary(t, bin, ks, pw, httpTokenFile)
	httpEndpoint := fmt.Sprintf("http://%s", httpProc.addr.String())
	httpCS := sdkClient(t, testCtx, httpEndpoint, httpTokenStr) // bounds_test.go

	for _, tc := range table {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			args := parityTxToArgs(tc.vector.Tx)

			// ── Stdio transport run ──────────────────────────────────────────────
			//
			// Launch a fresh stdio subprocess for each vector.  Using CommandTransport:
			//   - mcp.CommandTransport calls cmd.Start() internally.
			//   - Stderr captured in syncBuffer; cmd.Wait() (called by cs.Close()) waits
			//     for the internal stderr-copy goroutine → all stderr available after Close.
			var stdioStderr syncBuffer
			stdioCmd := newStdioCmd(t, bin, ks, pw)
			stdioCmd.Stderr = &stdioStderr

			stdioClient := mcp.NewClient(
				&mcp.Implementation{Name: "parity-stdio-client", Version: "v0.0.1"},
				nil,
			)
			stdioConnCtx, stdioConnCancel := context.WithTimeout(testCtx, 15*time.Second)
			stdioCS, stdioConnErr := stdioClient.Connect(
				stdioConnCtx, &mcp.CommandTransport{Command: stdioCmd}, nil,
			)
			stdioConnCancel()
			if stdioConnErr != nil {
				t.Fatalf("[%s] stdio Connect: %v", tc.name, stdioConnErr)
			}

			// Sign over stdio.
			stdioCallCtx, stdioCallCancel := context.WithTimeout(testCtx, 15*time.Second)
			stdioResult, stdioCallErr := stdioCS.CallTool(stdioCallCtx, &mcp.CallToolParams{
				Name:      "sign_transaction",
				Arguments: args,
			})
			stdioCallCancel()

			// Close stdio session — waits for all stderr to be copied (cmd.Wait inside).
			if closeErr := stdioCS.Close(); closeErr != nil {
				t.Logf("[%s] stdioCS.Close: %v (benign)", tc.name, closeErr)
			}

			if stdioCallErr != nil {
				t.Fatalf("[%s] stdio CallTool error: %v", tc.name, stdioCallErr)
			}
			if stdioResult == nil || stdioResult.IsError {
				t.Fatalf("[%s] stdio sign_transaction returned error", tc.name)
			}
			if len(stdioResult.Content) == 0 {
				t.Fatalf("[%s] stdio sign_transaction Content is empty", tc.name)
			}
			stdioTC, ok := stdioResult.Content[0].(*mcp.TextContent)
			if !ok {
				t.Fatalf("[%s] stdio Content[0] is %T; want *mcp.TextContent",
					tc.name, stdioResult.Content[0])
			}
			var stdioSR signing.SignResult
			if jsonErr := json.Unmarshal([]byte(stdioTC.Text), &stdioSR); jsonErr != nil {
				t.Fatalf("[%s] unmarshal stdio SignResult: %v", tc.name, jsonErr)
			}

			// ── HTTP transport run ───────────────────────────────────────────────
			httpCallCtx, httpCallCancel := context.WithTimeout(testCtx, 15*time.Second)
			httpResult, httpCallErr := httpCS.CallTool(httpCallCtx, &mcp.CallToolParams{
				Name:      "sign_transaction",
				Arguments: args,
			})
			httpCallCancel()

			if httpCallErr != nil {
				t.Fatalf("[%s] HTTP CallTool error: %v", tc.name, httpCallErr)
			}
			if httpResult == nil || httpResult.IsError {
				t.Fatalf("[%s] HTTP sign_transaction returned error", tc.name)
			}
			if len(httpResult.Content) == 0 {
				t.Fatalf("[%s] HTTP sign_transaction Content is empty", tc.name)
			}
			httpTC, ok2 := httpResult.Content[0].(*mcp.TextContent)
			if !ok2 {
				t.Fatalf("[%s] HTTP Content[0] is %T; want *mcp.TextContent",
					tc.name, httpResult.Content[0])
			}
			var httpSR signing.SignResult
			if jsonErr := json.Unmarshal([]byte(httpTC.Text), &httpSR); jsonErr != nil {
				t.Fatalf("[%s] unmarshal HTTP SignResult: %v", tc.name, jsonErr)
			}

			// ── Parity assertions ────────────────────────────────────────────────
			//
			// Source of truth: committed golden JSON — NOT re-derived go-ethereum calls.
			golden := tc.vector.Expected

			// rawTransaction: must be byte-identical across both transports and to the golden.
			if stdioSR.RawTransaction != golden.RawTx {
				t.Errorf("[%s] stdio rawTransaction != golden:\n  got:  %s\n  want: %s",
					tc.name, stdioSR.RawTransaction, golden.RawTx)
			}
			if httpSR.RawTransaction != golden.RawTx {
				t.Errorf("[%s] HTTP rawTransaction != golden:\n  got:  %s\n  want: %s",
					tc.name, httpSR.RawTransaction, golden.RawTx)
			}
			if stdioSR.RawTransaction != httpSR.RawTransaction {
				t.Errorf("[%s] stdio rawTransaction != HTTP rawTransaction:\n  stdio: %s\n  http:  %s",
					tc.name, stdioSR.RawTransaction, httpSR.RawTransaction)
			}

			// signature.r
			if stdioSR.Signature.R != golden.R {
				t.Errorf("[%s] stdio r != golden:\n  got:  %s\n  want: %s",
					tc.name, stdioSR.Signature.R, golden.R)
			}
			if httpSR.Signature.R != golden.R {
				t.Errorf("[%s] HTTP r != golden:\n  got:  %s\n  want: %s",
					tc.name, httpSR.Signature.R, golden.R)
			}

			// signature.s
			if stdioSR.Signature.S != golden.S {
				t.Errorf("[%s] stdio s != golden:\n  got:  %s\n  want: %s",
					tc.name, stdioSR.Signature.S, golden.S)
			}
			if httpSR.Signature.S != golden.S {
				t.Errorf("[%s] HTTP s != golden:\n  got:  %s\n  want: %s",
					tc.name, httpSR.Signature.S, golden.S)
			}

			// signature.v
			if stdioSR.Signature.V != golden.V {
				t.Errorf("[%s] stdio v != golden:\n  got:  %s\n  want: %s",
					tc.name, stdioSR.Signature.V, golden.V)
			}
			if httpSR.Signature.V != golden.V {
				t.Errorf("[%s] HTTP v != golden:\n  got:  %s\n  want: %s",
					tc.name, httpSR.Signature.V, golden.V)
			}

			// hash
			if stdioSR.Hash != golden.TxHash {
				t.Errorf("[%s] stdio hash != golden:\n  got:  %s\n  want: %s",
					tc.name, stdioSR.Hash, golden.TxHash)
			}
			if httpSR.Hash != golden.TxHash {
				t.Errorf("[%s] HTTP hash != golden:\n  got:  %s\n  want: %s",
					tc.name, httpSR.Hash, golden.TxHash)
			}

			// from — must equal fixture address on both transports.
			if stdioSR.From != signing.FixtureTestAddress {
				t.Errorf("[%s] stdio from = %q; want %q", tc.name, stdioSR.From, signing.FixtureTestAddress)
			}
			if httpSR.From != signing.FixtureTestAddress {
				t.Errorf("[%s] HTTP from = %q; want %q", tc.name, httpSR.From, signing.FixtureTestAddress)
			}

			// Cross-transport equality (belt-and-suspenders; covered above field-by-field too).
			if stdioSR.RawTransaction != httpSR.RawTransaction ||
				stdioSR.Signature.R != httpSR.Signature.R ||
				stdioSR.Signature.S != httpSR.Signature.S ||
				stdioSR.Signature.V != httpSR.Signature.V ||
				stdioSR.Hash != httpSR.Hash ||
				stdioSR.From != httpSR.From {
				t.Errorf("[%s] stdio SignResult != HTTP SignResult; individual field errors above",
					tc.name)
			}

			// ── HTTP stderr: reqlog+audit correlation for this vector's call ─────
			// Note: the HTTP session is shared across table entries; after the test
			// suite finishes, the HTTP stderr is scanned holistically in
			// TestTransportParity_SignResult's outer scope (post-teardown).
			// Individual per-call correlation is verified in TestE2E_HTTP_FullSession.

			// ── Leak scan on stdio stderr for this vector ─────────────────────
			stdioStderrBytes := stdioStderr.Bytes()
			stdioLeaked := signing.FixtureKeySentinel().Scan(stdioStderrBytes)
			var stdioKeyLeaks []string
			for _, form := range stdioLeaked {
				if form == "address-checksummed" || form == "address-lower-nox" {
					continue
				}
				stdioKeyLeaks = append(stdioKeyLeaks, form)
			}
			if len(stdioKeyLeaks) > 0 {
				t.Errorf("[%s] stdio stderr leaks key material: forms=%v", tc.name, stdioKeyLeaks)
			}
		})
	}

	// ── Teardown: SIGTERM HTTP subprocess → assert exit 0 ─────────────────────────
	// Close the shared HTTP session before SIGTERM (sdkClient's t.Cleanup also runs).
	if closeErr := httpCS.Close(); closeErr != nil {
		t.Logf("parity HTTP teardown: httpCS.Close: %v (benign)", closeErr)
	}
	if sigErr := httpProc.cmd.Process.Signal(syscall.SIGTERM); sigErr != nil {
		t.Logf("parity HTTP teardown: SIGTERM: %v (may have exited)", sigErr)
	}
	var httpWaitErr error
	select {
	case httpWaitErr = <-httpProc.waitCh:
	case <-time.After(8 * time.Second):
		t.Fatal("parity HTTP teardown: HTTP binary did not exit within 8s after SIGTERM")
	}
	select {
	case <-httpProc.doneCh:
	case <-time.After(2 * time.Second):
		t.Log("parity HTTP teardown: stderr scanner did not finish within 2s")
	}
	if httpWaitErr != nil {
		t.Errorf("parity HTTP teardown: exit error: %v; want exit 0 on SIGTERM", httpWaitErr)
	}

	// ── Leak scan on HTTP stderr (all vectors combined) ──────────────────────────
	httpStderrBytes := httpProc.stderr.Bytes()
	httpLeaked := signing.FixtureKeySentinel().Scan(httpStderrBytes)
	var httpKeyLeaks []string
	for _, form := range httpLeaked {
		if form == "address-checksummed" || form == "address-lower-nox" {
			continue
		}
		httpKeyLeaks = append(httpKeyLeaks, form)
	}
	if len(httpKeyLeaks) > 0 {
		t.Errorf("parity HTTP stderr leaks key material: forms=%v", httpKeyLeaks)
	}

	// ── HTTP stderr reqlog+audit correlation (for all vectors combined) ─────────
	allHTTPLines := parseLogLines(bytes.NewBuffer(httpStderrBytes))
	rqLines := reqlogLines(allHTTPLines)

	var auditLines []map[string]any
	for _, m := range allHTTPLines {
		if msg, _ := m["msg"].(string); strings.Contains(msg, "signed successfully") {
			auditLines = append(auditLines, m)
		}
	}

	// Each audit line must have a matching reqlog line with the same request_id.
	for i, al := range auditLines {
		auditRID, _ := al["request_id"].(string)
		if auditRID == "" {
			t.Errorf("parity audit line %d: empty request_id", i)
			continue
		}
		var matched bool
		for _, rl := range rqLines {
			if rid, _ := rl["request_id"].(string); rid == auditRID {
				matched = true
				break
			}
		}
		if !matched {
			t.Errorf("parity audit line %d: request_id=%q has no matching reqlog line",
				i, auditRID)
		}
	}
}

// ─── Parity: tools/list schema ────────────────────────────────────────────────────

// TestTransportParity_ToolsListSchema proves that the tools/list schema documents
// are DEEP-EQUAL across stdio and HTTP transports.
//
// Both the tool list (exactly 2 tools: sign_transaction + get_address) and the
// full InputSchema for each tool must be identical across transports.  Schemas
// are deep-equal by marshaling to JSON and unmarshaling into comparable Go values
// (reflect.DeepEqual on the schema map), which is deterministic regardless of
// JSON field ordering differences.
//
// Skipped under -short.
func TestTransportParity_ToolsListSchema(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping parity schema test under -short")
	}
	// Not t.Parallel(): launches subprocesses.

	bin := getE2EBinary(t)
	tdPath := signingTestdataPath(t)
	ks := filepath.Join(tdPath, "keystore-light.json")
	pw := filepath.Join(tdPath, "password.txt")

	testCtx, testCancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer testCancel()

	// ── Stdio tools/list ──────────────────────────────────────────────────────────
	var stdioStderr syncBuffer
	stdioCmd := newStdioCmd(t, bin, ks, pw)
	stdioCmd.Stderr = &stdioStderr

	stdioClient := mcp.NewClient(
		&mcp.Implementation{Name: "parity-schema-stdio-client", Version: "v0.0.1"},
		nil,
	)
	stdioConnCtx, stdioConnCancel := context.WithTimeout(testCtx, 15*time.Second)
	stdioCS, stdioConnErr := stdioClient.Connect(
		stdioConnCtx, &mcp.CommandTransport{Command: stdioCmd}, nil,
	)
	stdioConnCancel()
	if stdioConnErr != nil {
		t.Fatalf("schema parity: stdio Connect: %v", stdioConnErr)
	}

	stdioListCtx, stdioListCancel := context.WithTimeout(testCtx, 10*time.Second)
	stdioToolsResult, stdioListErr := stdioCS.ListTools(stdioListCtx, nil)
	stdioListCancel()

	if closeErr := stdioCS.Close(); closeErr != nil {
		t.Logf("schema parity: stdioCS.Close: %v (benign)", closeErr)
	}

	if stdioListErr != nil {
		t.Fatalf("schema parity: stdio ListTools: %v", stdioListErr)
	}

	// ── HTTP tools/list ───────────────────────────────────────────────────────────
	httpRawToken := randTokenBytes(32)
	httpTokenStr := hexEncodeBytes(httpRawToken)
	defer signing.ZeroBytes(httpRawToken)
	httpTokenFile := writeTokenFile(t, httpTokenStr+"\n")

	httpProc := launchHTTPBinary(t, bin, ks, pw, httpTokenFile)
	httpEndpoint := fmt.Sprintf("http://%s", httpProc.addr.String())
	httpCS := sdkClient(t, testCtx, httpEndpoint, httpTokenStr)

	httpListCtx, httpListCancel := context.WithTimeout(testCtx, 10*time.Second)
	httpToolsResult, httpListErr := httpCS.ListTools(httpListCtx, nil)
	httpListCancel()

	if httpListErr != nil {
		t.Fatalf("schema parity: HTTP ListTools: %v", httpListErr)
	}

	// ── Tool count parity ─────────────────────────────────────────────────────────
	if len(stdioToolsResult.Tools) != len(httpToolsResult.Tools) {
		t.Errorf("schema parity: tool count: stdio=%d, HTTP=%d; want equal",
			len(stdioToolsResult.Tools), len(httpToolsResult.Tools))
	}
	if len(stdioToolsResult.Tools) != 2 {
		t.Errorf("schema parity: stdio tool count = %d; want 2", len(stdioToolsResult.Tools))
	}

	// Build tool maps for comparison.
	stdioTools := make(map[string]*mcp.Tool, len(stdioToolsResult.Tools))
	for i := range stdioToolsResult.Tools {
		stdioTools[stdioToolsResult.Tools[i].Name] = stdioToolsResult.Tools[i]
	}
	httpTools := make(map[string]*mcp.Tool, len(httpToolsResult.Tools))
	for i := range httpToolsResult.Tools {
		httpTools[httpToolsResult.Tools[i].Name] = httpToolsResult.Tools[i]
	}

	// ── Schema deep-equal for each tool ──────────────────────────────────────────
	for _, toolName := range []string{"sign_transaction", "get_address"} {
		stdioTool, stdioOK := stdioTools[toolName]
		httpTool, httpOK := httpTools[toolName]
		if !stdioOK {
			t.Errorf("schema parity: tool %q missing from stdio tools/list", toolName)
			continue
		}
		if !httpOK {
			t.Errorf("schema parity: tool %q missing from HTTP tools/list", toolName)
			continue
		}

		// Marshal both schemas to JSON and unmarshal to generic maps for deep-equal.
		// Using JSON round-trip makes comparison field-order-independent.
		stdioSchemaJSON, stdioMarshalErr := json.Marshal(stdioTool.InputSchema)
		if stdioMarshalErr != nil {
			t.Fatalf("schema parity: marshal stdio %s schema: %v", toolName, stdioMarshalErr)
		}
		httpSchemaJSON, httpMarshalErr := json.Marshal(httpTool.InputSchema)
		if httpMarshalErr != nil {
			t.Fatalf("schema parity: marshal HTTP %s schema: %v", toolName, httpMarshalErr)
		}

		var stdioSchemaMap, httpSchemaMap any
		if err := json.Unmarshal(stdioSchemaJSON, &stdioSchemaMap); err != nil {
			t.Fatalf("schema parity: unmarshal stdio %s schema: %v", toolName, err)
		}
		if err := json.Unmarshal(httpSchemaJSON, &httpSchemaMap); err != nil {
			t.Fatalf("schema parity: unmarshal HTTP %s schema: %v", toolName, err)
		}

		if !reflect.DeepEqual(stdioSchemaMap, httpSchemaMap) {
			t.Errorf("schema parity: %s InputSchema differs between stdio and HTTP:\n  stdio: %s\n  HTTP:  %s",
				toolName, stdioSchemaJSON, httpSchemaJSON)
		}

		// Also compare tool descriptions (belt-and-suspenders).
		if stdioTool.Description != httpTool.Description {
			t.Errorf("schema parity: %s Description differs:\n  stdio: %q\n  HTTP:  %q",
				toolName, stdioTool.Description, httpTool.Description)
		}
	}

	// ── Teardown: SIGTERM HTTP subprocess ─────────────────────────────────────────
	if closeErr := httpCS.Close(); closeErr != nil {
		t.Logf("schema parity HTTP teardown: httpCS.Close: %v (benign)", closeErr)
	}
	if sigErr := httpProc.cmd.Process.Signal(syscall.SIGTERM); sigErr != nil {
		t.Logf("schema parity HTTP teardown: SIGTERM: %v", sigErr)
	}
	select {
	case <-httpProc.waitCh:
	case <-time.After(8 * time.Second):
		t.Log("schema parity HTTP teardown: binary did not exit within 8s")
	}
	select {
	case <-httpProc.doneCh:
	case <-time.After(2 * time.Second):
	}

	// ── Leak scan: stdio stderr ───────────────────────────────────────────────────
	stdioStderrBytes := stdioStderr.Bytes()
	stdioLeaked := signing.FixtureKeySentinel().Scan(stdioStderrBytes)
	var stdioKeyLeaks []string
	for _, form := range stdioLeaked {
		if form == "address-checksummed" || form == "address-lower-nox" {
			continue
		}
		stdioKeyLeaks = append(stdioKeyLeaks, form)
	}
	if len(stdioKeyLeaks) > 0 {
		t.Errorf("schema parity: stdio stderr leaks key material: forms=%v", stdioKeyLeaks)
	}
}

// ─── Helper: newStdioCmd ─────────────────────────────────────────────────────────

// newStdioCmd creates an *exec.Cmd for the binary in stdio mode (default transport).
// The caller is responsible for setting cmd.Stderr and connecting via CommandTransport.
//
// Not started here — mcp.CommandTransport.Connect calls cmd.Start() internally.
func newStdioCmd(t *testing.T, bin, ks, pw string) *exec.Cmd {
	t.Helper()
	return exec.Command(bin, "--keystore", ks, "--password-file", pw)
}
