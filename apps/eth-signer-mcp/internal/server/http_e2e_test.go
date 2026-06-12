//go:build !windows

package server

// http_e2e_test.go — Issue 3.8: HTTP e2e test (full binary surface over Streamable HTTP).
//
// Full-surface end-to-end test over real Streamable HTTP against the eth-signer-mcp
// binary, plus the process-management helpers shared by parity_transport_test.go.
//
// What's covered:
//   - Binary build once per test-binary invocation (sync.Once, self-contained: no make).
//   - HTTP binary launch + stderr announce-line scraping (no sleeps on the happy path).
//   - initialize, tools/list (exactly 2 tools, strict schemas), get_address, sign_transaction.
//   - Error paths via JSON-parsed Content[0] (never substring-match):
//       invalid_input, unsupported_type, chain_id_mismatch, password_error.
//   - keystore_error via startup-refusal subprocess exit code assertion.
//   - Audit+reqlog line correlation on the HTTP-side stderr (same request_id).
//   - Leak scan (raw + encoded forms) over HTTP-side stderr after process exit.
//   - Teardown: SIGTERM → exit 0 (graceful shutdown regression check).
//   - Harness leaves NO zombie processes: t.Cleanup Kill + goroutine drain.
//   - No external network access; no Foundry/Node; green under go test -race.
//
// Skipped under -short (requires ~15 s for light-scrypt decrypts).
//
// NOTE: internal_error is NOT force-able through the real binary without a fault
// hook (the recovered sender must differ from vault.Address()). It is covered at
// the contract-test level in handlers_test.go / TestSignTransaction_SixCodesWireEncoding.

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/rootwarp/blockchain-ai-tools/apps/eth-signer-mcp/internal/signing"
)

// ─── syncBuffer ─────────────────────────────────────────────────────────────────
//
// syncBuffer is a goroutine-safe bytes.Buffer for subprocess stderr capture.
// os/exec's internal stderr-copier goroutine writes concurrently with the test
// goroutine reading; bytes.Buffer is not safe for concurrent use (DATA RACE under
// -race).  A single mutex guards all access and Bytes() returns a snapshot copy.

type syncBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (s *syncBuffer) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.buf.Write(p)
}

// Bytes returns a safe copy of the accumulated bytes under the lock.
func (s *syncBuffer) Bytes() []byte {
	s.mu.Lock()
	defer s.mu.Unlock()
	b := s.buf.Bytes()
	out := make([]byte, len(b))
	copy(out, b)
	return out
}

func (s *syncBuffer) String() string { return string(s.Bytes()) }

// ─── Binary build (sync.Once) ────────────────────────────────────────────────────

var (
	e2eBinOnce     sync.Once
	e2eBinPath     string
	e2eBinBuildErr error
)

// e2eModuleRoot returns the module root directory (the directory containing go.mod)
// derived from this source file's runtime location — never from os.Getwd().
//
//	This file: .../apps/eth-signer-mcp/internal/server/http_e2e_test.go
//	Module root: .../apps/eth-signer-mcp/
func e2eModuleRoot() (string, bool) {
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		return "", false
	}
	// thisFile = ".../internal/server/http_e2e_test.go"
	// Two directories up from internal/server/ is the module root.
	return filepath.Join(filepath.Dir(thisFile), "..", ".."), true
}

// getE2EBinary returns the path to a built eth-signer-mcp binary, building it
// exactly once per test-binary invocation via sync.Once.
//
// Building uses `go build ./cmd/eth-signer-mcp` with the module root as the
// working directory — no make dependency, no hardcoded absolute paths.
func getE2EBinary(t *testing.T) string {
	t.Helper()
	e2eBinOnce.Do(func() {
		root, ok := e2eModuleRoot()
		if !ok {
			e2eBinBuildErr = errors.New("getE2EBinary: runtime.Caller(0) failed")
			return
		}
		outDir, err := os.MkdirTemp("", "eth-signer-mcp-e2e-*")
		if err != nil {
			e2eBinBuildErr = fmt.Errorf("getE2EBinary: MkdirTemp: %w", err)
			return
		}
		e2eBinPath = filepath.Join(outDir, "eth-signer-mcp")
		buildCmd := exec.Command("go", "build", "-o", e2eBinPath, "./cmd/eth-signer-mcp")
		buildCmd.Dir = root
		if out, buildErr := buildCmd.CombinedOutput(); buildErr != nil {
			e2eBinBuildErr = fmt.Errorf("getE2EBinary: go build: %w\n%s", buildErr, out)
		}
	})
	if e2eBinBuildErr != nil {
		t.Fatalf("getE2EBinary: binary build failed: %v", e2eBinBuildErr)
	}
	return e2eBinPath
}

// ─── HTTP binary process state ──────────────────────────────────────────────────

// httpBinaryState holds the live state of an HTTP binary subprocess launched by
// launchHTTPBinary.  Use waitCh to receive the result of cmd.Wait() (called
// after all stderr output has been consumed by the scanner goroutine).  doneCh
// is closed when the scanner+wait goroutine exits — t.Cleanup drains this to
// prevent goroutine leaks.
type httpBinaryState struct {
	addr   net.Addr    // resolved TCP address from the announce line
	stderr *syncBuffer // all stderr output (accumulated as lines arrive)
	cmd    *exec.Cmd   // the subprocess command

	// waitCh receives the result of cmd.Wait(), called only AFTER all stderr
	// is consumed by the scanner.  Buffered cap-1: the goroutine never blocks.
	// Read exactly ONCE from the test body OR let it stay buffered.
	waitCh <-chan error

	// doneCh is closed when the scanner+wait goroutine exits.  t.Cleanup waits
	// on this channel (with timeout) to ensure no goroutine outlives the test.
	doneCh <-chan struct{}
}

// launchHTTPBinary starts the eth-signer-mcp binary in HTTP mode, waits up to
// 5 s for the "listening on" announce line, and returns the process state.
//
// The announce-line scrape uses a retry goroutine (not a sleep): the goroutine
// reads stderr line by line and signals on announceCh when the listening-on line
// is found, satisfying the "no sleeps on the happy path" requirement.
//
// Cleanup ordering: launchHTTPBinary registers a t.Cleanup that Kill-s the process
// and drains doneCh.  Register launchHTTPBinary BEFORE sdkClient so LIFO ordering
// closes SDK sessions before killing the process.
//
// extraArgs are appended verbatim (e.g. "--chain-id", "5" for a guard test).
func launchHTTPBinary(
	t *testing.T,
	bin, ks, pw, tokenFile string,
	extraArgs ...string,
) *httpBinaryState {
	t.Helper()

	args := []string{
		"--keystore", ks,
		"--password-file", pw,
		"--http",
		"--http-auth-token-file", tokenFile,
	}
	args = append(args, extraArgs...)

	ctx, ctxCancel := context.WithTimeout(context.Background(), 120*time.Second)
	cmd := exec.CommandContext(ctx, bin, args...)

	stderrPipe, pipeErr := cmd.StderrPipe()
	if pipeErr != nil {
		ctxCancel()
		t.Fatalf("launchHTTPBinary: StderrPipe: %v", pipeErr)
	}

	if startErr := cmd.Start(); startErr != nil {
		ctxCancel()
		t.Fatalf("launchHTTPBinary: cmd.Start: %v", startErr)
	}

	stderrBuf := new(syncBuffer)
	announceCh := make(chan string, 1)
	waitChInternal := make(chan error, 1) // buffered: goroutine never blocks on send
	doneChInternal := make(chan struct{}) // closed when goroutine exits

	// Scanner + Wait goroutine.
	//
	// All stderr is read BEFORE cmd.Wait() is called, satisfying Go's requirement
	// from StderrPipe docs: "it is incorrect to call Wait before all reads from the
	// pipe have completed."  waitChInternal is buffered (cap 1) so the goroutine
	// sends immediately (no reader needed), then returns → doneChInternal closed.
	go func() {
		defer close(doneChInternal)
		scanner := bufio.NewScanner(stderrPipe)
		for scanner.Scan() {
			line := scanner.Text()
			_, _ = stderrBuf.Write([]byte(line + "\n"))
			if strings.Contains(line, "listening on") {
				select {
				case announceCh <- line:
				default: // already buffered
				}
			}
		}
		// All stderr consumed — safe to call Wait now.
		waitChInternal <- cmd.Wait()
	}()

	// Safety-net cleanup: registered BEFORE sdkClient so LIFO ordering closes
	// SDK sessions before the process is killed.
	t.Cleanup(func() {
		ctxCancel()
		if cmd.Process != nil {
			_ = cmd.Process.Kill()
		}
		// Drain doneCh so the goroutine fully exits before the test finishes.
		// If the test body already sent SIGTERM and waitCh was consumed, doneCh
		// is already closed by the time t.Cleanup runs.
		select {
		case <-doneChInternal:
		case <-time.After(8 * time.Second):
			t.Logf("launchHTTPBinary cleanup: goroutine did not exit within 8s")
		}
	})

	// Wait for the announce line (5 s deadline, no sleeps on the happy path).
	var addr net.Addr
	select {
	case line := <-announceCh:
		// Announce format: "eth-signer-mcp listening on 127.0.0.1:PORT"
		// Extract the last space-separated field.
		idx := strings.LastIndex(line, " ")
		if idx < 0 {
			t.Fatalf("launchHTTPBinary: announce line has no space: %q", line)
		}
		hostPort := strings.TrimSpace(line[idx+1:])
		tcpAddr, resolveErr := net.ResolveTCPAddr("tcp", hostPort)
		if resolveErr != nil {
			t.Fatalf("launchHTTPBinary: resolve %q: %v", hostPort, resolveErr)
		}
		addr = tcpAddr
	case <-time.After(5 * time.Second):
		t.Fatal("launchHTTPBinary: timeout (5s) waiting for 'listening on' announce line")
	}

	return &httpBinaryState{
		addr:   addr,
		stderr: stderrBuf,
		cmd:    cmd,
		waitCh: waitChInternal,
		doneCh: doneChInternal,
	}
}

// ─── Wire-error assertion helper ────────────────────────────────────────────────

// assertHTTPToolError asserts that a CallTool response encodes a ToolError with
// the expected code.  Mirrors assertToolErrorCode from the stdio e2e (cmd package)
// with the same discipline: JSON-parse Content[0], never substring-match.
//
// Checks:
//   - callErr is nil (no protocol-level transport failure)
//   - result.IsError == true
//   - Content[0] is *mcp.TextContent
//   - Its text is valid JSON with exactly the keys "code" and "message"
//   - The "code" field equals wantCode
func assertHTTPToolError(
	t *testing.T,
	result *mcp.CallToolResult,
	callErr error,
	wantCode string,
) {
	t.Helper()
	if callErr != nil {
		t.Fatalf("assertHTTPToolError(%q): protocol error: %v; want tool-level error result",
			wantCode, callErr)
	}
	if result == nil {
		t.Fatalf("assertHTTPToolError(%q): result is nil; want IsError=true result", wantCode)
	}
	if !result.IsError {
		t.Fatalf("assertHTTPToolError(%q): IsError=false; want true", wantCode)
	}
	if len(result.Content) == 0 {
		t.Fatalf("assertHTTPToolError(%q): Content is empty; want one TextContent", wantCode)
	}
	tc, ok := result.Content[0].(*mcp.TextContent)
	if !ok {
		t.Fatalf("assertHTTPToolError(%q): Content[0] is %T; want *mcp.TextContent",
			wantCode, result.Content[0])
	}
	var decoded map[string]json.RawMessage
	if err := json.Unmarshal([]byte(tc.Text), &decoded); err != nil {
		t.Fatalf("assertHTTPToolError(%q): Content[0].Text is not valid JSON: %v\ntext: %s",
			wantCode, err, tc.Text)
	}
	if len(decoded) != 2 {
		t.Errorf("assertHTTPToolError(%q): JSON has %d keys; want exactly 2 (code, message)\ntext: %s",
			wantCode, len(decoded), tc.Text)
	}
	for _, k := range []string{"code", "message"} {
		if _, exists := decoded[k]; !exists {
			t.Errorf("assertHTTPToolError(%q): JSON missing key %q\ntext: %s",
				wantCode, k, tc.Text)
		}
	}
	var gotCode string
	if err := json.Unmarshal(decoded["code"], &gotCode); err != nil {
		t.Fatalf("assertHTTPToolError(%q): unmarshal code: %v", wantCode, err)
	}
	if gotCode != wantCode {
		t.Errorf("assertHTTPToolError: code=%q; want %q\ntext: %s", gotCode, wantCode, tc.Text)
	}
}

// ─── Main HTTP e2e test ──────────────────────────────────────────────────────────

// TestE2E_HTTP_FullSession drives a complete MCP HTTP session against the real
// eth-signer-mcp binary using the keystore-light.json fixture (~50 ms/decrypt).
//
// Session sequence:
//  1. initialize:         server name "eth-signer-mcp", non-empty version.
//  2. tools/list:         exactly sign_transaction + get_address; strict schemas.
//  3. get_address:        returns EIP-55 checksummed fixture address.
//  4. sign_transaction:   happy path; result byte-identical to legacy-mainnet.json.
//  5. Error paths (JSON-parsed Content[0], never substring-match):
//     5a. invalid_input    — chainId:"0" (passes schema, hits rule-1)
//     5b. unsupported_type — type:"0x3"
//     5c. chain_id_mismatch — separate subprocess with --chain-id 5, chainId-1 request
//     5d. password_error   — separate subprocess with wrong password
//     5e. keystore_error   — startup refusal via subprocess exit code + stderr
//  6. HTTP-side stderr: reqlog+audit correlation (same request_id per signing call).
//  7. Leak scan over all HTTP-side stderr (raw + encoded forms of fixture key).
//  8. Teardown: SIGTERM → process exits 0 (graceful shutdown regression check).
//
// NOTE: internal_error is NOT force-able through the real binary without a fault hook
// (recovered sender must differ from vault.Address()).  Covered at the contract-test
// level in handlers_test.go / TestSignTransaction_SixCodesWireEncoding.
//
// Skipped under -short.  Total runtime ~15 s (light-scrypt ~50 ms × several decrypts
// plus subprocess launch overhead).
func TestE2E_HTTP_FullSession(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping HTTP e2e test under -short (requires ~15 s for light-scrypt decrypts)")
	}
	// Not t.Parallel() — uses the light-fixture KDF and subprocess launch.

	bin := getE2EBinary(t)
	tdPath := signingTestdataPath(t) // defined in handlers_test.go
	ks := filepath.Join(tdPath, "keystore-light.json")
	pw := filepath.Join(tdPath, "password.txt")
	noAddrKs := filepath.Join(tdPath, "keystore-no-address.json")

	// Golden vector for happy-path binary-parity assertion.
	// loadGoldenRawTx defined in concurrent_test.go (same package).
	goldenLegacyRawTx := loadGoldenRawTx(t, "legacy-mainnet.json")
	legacyArgs := map[string]any{
		"type":     "0x0",
		"chainId":  "1",
		"nonce":    "0",
		"to":       "0x9858EfFD232B4033E47d90003D41EC34EcaEda94",
		"value":    "1000000000000000000",
		"data":     "0xdeadbeef",
		"gas":      "100000",
		"gasPrice": "20000000000",
	}

	// ── Main subprocess: fresh random token ─────────────────────────────────────
	//
	// randTokenBytes and hexEncodeBytes defined in auth_test.go (same package).
	// writeTokenFile defined in http_test.go (same package).
	rawToken := randTokenBytes(32)
	tokenStr := hexEncodeBytes(rawToken)
	defer signing.ZeroBytes(rawToken)
	tokenFile := writeTokenFile(t, tokenStr+"\n")

	// launchHTTPBinary registers t.Cleanup (Kill + goroutine drain).
	// MUST be called BEFORE sdkClient so LIFO ordering closes the session before Kill.
	proc := launchHTTPBinary(t, bin, ks, pw, tokenFile)

	testCtx, testCancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer testCancel()

	endpoint := fmt.Sprintf("http://%s", proc.addr.String())

	// sdkClient defined in bounds_test.go (same package).
	// Its t.Cleanup (session close) runs BEFORE the proc t.Cleanup (Kill) via LIFO.
	cs := sdkClient(t, testCtx, endpoint, tokenStr)

	// ── Step 1: initialize ───────────────────────────────────────────────────────
	initResult := cs.InitializeResult()
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

	// ── Step 2: tools/list — exactly 2 tools, strict schemas ────────────────────
	toolsResult, toolsErr := cs.ListTools(testCtx, nil)
	if toolsErr != nil {
		t.Fatalf("step 2: ListTools: %v", toolsErr)
	}
	if len(toolsResult.Tools) != 2 {
		names := make([]string, len(toolsResult.Tools))
		for i, tt := range toolsResult.Tools {
			names[i] = tt.Name
		}
		t.Fatalf("step 2: len(Tools) = %d; want 2. Got: %v", len(toolsResult.Tools), names)
	}
	toolMap := make(map[string]*mcp.Tool, 2)
	for i := range toolsResult.Tools {
		toolMap[toolsResult.Tools[i].Name] = toolsResult.Tools[i]
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
					t.Errorf("step 2: sign_transaction additionalProperties = %s; want false", ap)
				}
			}
		}
	}
	// Assert get_address InputSchema has additionalProperties:false.
	if addrTool := toolMap["get_address"]; addrTool != nil {
		schemaJSON, _ := json.Marshal(addrTool.InputSchema)
		var schema map[string]json.RawMessage
		if jsonErr := json.Unmarshal(schemaJSON, &schema); jsonErr == nil {
			if ap, exists := schema["additionalProperties"]; !exists {
				t.Error("step 2: get_address InputSchema missing 'additionalProperties'")
			} else {
				var apVal bool
				if jsonErr := json.Unmarshal(ap, &apVal); jsonErr != nil || apVal {
					t.Errorf("step 2: get_address additionalProperties = %s; want false", ap)
				}
			}
		}
	}

	// ── Step 3: get_address — EIP-55 checksummed fixture address ────────────────
	{
		callCtx, callCancel := context.WithTimeout(testCtx, 10*time.Second)
		addrResult, addrErr := cs.CallTool(callCtx, &mcp.CallToolParams{
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
			t.Fatalf("step 3: Content[0] is %T; want *mcp.TextContent", addrResult.Content[0])
		}
		var ar signing.AddressResult
		if jsonErr := json.Unmarshal([]byte(addrTC.Text), &ar); jsonErr != nil {
			t.Fatalf("step 3: unmarshal AddressResult: %v\ntext: %s", jsonErr, addrTC.Text)
		}
		if ar.Address != signing.FixtureTestAddress {
			t.Errorf("step 3: get_address = %q; want %q", ar.Address, signing.FixtureTestAddress)
		}
	}

	// ── Step 4: sign_transaction happy path (binary-level parity) ───────────────
	{
		callCtx, callCancel := context.WithTimeout(testCtx, 15*time.Second)
		signResult, signErr := cs.CallTool(callCtx, &mcp.CallToolParams{
			Name:      "sign_transaction",
			Arguments: legacyArgs,
		})
		callCancel()
		if signErr != nil {
			t.Fatalf("step 4: sign_transaction protocol error: %v", signErr)
		}
		if signResult == nil || signResult.IsError {
			t.Fatalf("step 4: sign_transaction returned error; Content: %v", signResult.Content)
		}
		if len(signResult.Content) == 0 {
			t.Fatal("step 4: sign_transaction Content is empty")
		}
		signTC, ok := signResult.Content[0].(*mcp.TextContent)
		if !ok {
			t.Fatalf("step 4: Content[0] is %T; want *mcp.TextContent", signResult.Content[0])
		}
		var sr signing.SignResult
		if jsonErr := json.Unmarshal([]byte(signTC.Text), &sr); jsonErr != nil {
			t.Fatalf("step 4: unmarshal SignResult: %v\ntext: %s", jsonErr, signTC.Text)
		}
		// Binary-parity: rawTransaction must match the committed golden.
		// Source of truth: committed JSON file (loadGoldenRawTx), NOT go-ethereum.
		if sr.RawTransaction != goldenLegacyRawTx {
			t.Errorf("step 4: RawTransaction mismatch:\n  got:  %s\n  want: %s",
				sr.RawTransaction, goldenLegacyRawTx)
		}
		if sr.From != signing.FixtureTestAddress {
			t.Errorf("step 4: From = %q; want %q", sr.From, signing.FixtureTestAddress)
		}
	}

	// ── Step 5a: invalid_input ────────────────────────────────────────────────────
	// chainId:"0" passes schema (valid string) but triggers rule-1 validator:
	// chainId==0 is replay-unprotected → invalid_input.
	{
		callCtx, callCancel := context.WithTimeout(testCtx, 10*time.Second)
		invResult, invErr := cs.CallTool(callCtx, &mcp.CallToolParams{
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
		assertHTTPToolError(t, invResult, invErr, signing.CodeInvalidInput)
	}

	// ── Step 5b: unsupported_type ─────────────────────────────────────────────────
	// type:"0x3" passes schema but rule-3 maps it to unsupported_type.
	{
		callCtx, callCancel := context.WithTimeout(testCtx, 10*time.Second)
		unsuppResult, unsuppErr := cs.CallTool(callCtx, &mcp.CallToolParams{
			Name: "sign_transaction",
			Arguments: map[string]any{
				"type":    "0x3",
				"chainId": "1",
				"nonce":   "0",
				"value":   "0",
				"data":    "0x",
				"gas":     "21000",
			},
		})
		callCancel()
		assertHTTPToolError(t, unsuppResult, unsuppErr, signing.CodeUnsupportedType)
	}

	// ── Step 5c: chain_id_mismatch (separate subprocess with --chain-id 5) ───────
	// A subprocess with --chain-id 5 guards against non-5 chainIds.
	// Sending the legacy-mainnet tx (chainId:"1") triggers chain_id_mismatch.
	{
		cmRawToken := randTokenBytes(32)
		cmTokenStr := hexEncodeBytes(cmRawToken)
		defer signing.ZeroBytes(cmRawToken)
		cmTokenFile := writeTokenFile(t, cmTokenStr+"\n")

		// launchHTTPBinary + sdkClient: LIFO cleanups close session before kill.
		cmProc := launchHTTPBinary(t, bin, ks, pw, cmTokenFile, "--chain-id", "5")
		cmEndpoint := fmt.Sprintf("http://%s", cmProc.addr.String())
		cmCS := sdkClient(t, testCtx, cmEndpoint, cmTokenStr)

		cmCallCtx, cmCallCancel := context.WithTimeout(testCtx, 10*time.Second)
		cmResult, cmErr := cmCS.CallTool(cmCallCtx, &mcp.CallToolParams{
			Name:      "sign_transaction",
			Arguments: legacyArgs, // chainId:"1", guard=5 → mismatch
		})
		cmCallCancel()
		assertHTTPToolError(t, cmResult, cmErr, signing.CodeChainIDMismatch)

		// Leak scan on chain-id mismatch subprocess stderr.
		// Close session first so the subprocess drains cleanly via its LIFO cleanup.
		// sdkClient's cleanup handles cs.Close(); proc's cleanup handles Kill+drain.
		_ = cmCS.Close() // preemptive close; sdkClient cleanup is idempotent (logs benign err)
		// signal SIGTERM so binary exits cleanly (tests graceful shutdown too).
		if err := cmProc.cmd.Process.Signal(syscall.SIGTERM); err != nil {
			t.Logf("step 5c: SIGTERM: %v (may already have exited)", err)
		}
		select {
		case <-cmProc.waitCh:
		case <-time.After(8 * time.Second):
			t.Log("step 5c: chain-id subprocess did not exit within 8s after SIGTERM")
		}
		select {
		case <-cmProc.doneCh:
		case <-time.After(4 * time.Second):
			t.Log("step 5c: scanner goroutine did not finish within 4s after wait timeout")
		}

		cmStderrBytes := cmProc.stderr.Bytes()
		cmLeaked := signing.FixtureKeySentinel().Scan(cmStderrBytes)
		var cmKeyLeaks []string
		for _, form := range cmLeaked {
			if form == "address-checksummed" || form == "address-lower-nox" {
				continue // address legitimately present in startup logs
			}
			cmKeyLeaks = append(cmKeyLeaks, form)
		}
		if len(cmKeyLeaks) > 0 {
			t.Errorf("step 5c: fixture key leaked in chain-id subprocess stderr: forms=%v",
				cmKeyLeaks)
		}
	}

	// ── Step 5d: password_error (separate subprocess with wrong password) ─────────
	// A subprocess with a wrong password file.  Vault construction succeeds
	// (password is only read at signing time), but the signing call fails with
	// password_error (ErrDecrypt from go-ethereum's keystore).
	{
		wrongPwPath := filepath.Join(t.TempDir(), "wrong-password.txt")
		if err := os.WriteFile(wrongPwPath, []byte("definitely-wrong-password\n"), 0o600); err != nil {
			t.Fatalf("step 5d: write wrong password: %v", err)
		}

		pwRawToken := randTokenBytes(32)
		pwTokenStr := hexEncodeBytes(pwRawToken)
		defer signing.ZeroBytes(pwRawToken)
		pwTokenFile := writeTokenFile(t, pwTokenStr+"\n")

		pwProc := launchHTTPBinary(t, bin, ks, wrongPwPath, pwTokenFile)
		pwEndpoint := fmt.Sprintf("http://%s", pwProc.addr.String())
		pwCS := sdkClient(t, testCtx, pwEndpoint, pwTokenStr)

		pwCallCtx, pwCallCancel := context.WithTimeout(testCtx, 15*time.Second)
		pwResult, pwErr := pwCS.CallTool(pwCallCtx, &mcp.CallToolParams{
			Name:      "sign_transaction",
			Arguments: legacyArgs,
		})
		pwCallCancel()
		assertHTTPToolError(t, pwResult, pwErr, signing.CodePasswordError)

		// Leak scan on password-error subprocess stderr.
		_ = pwCS.Close()
		if err := pwProc.cmd.Process.Signal(syscall.SIGTERM); err != nil {
			t.Logf("step 5d: SIGTERM: %v (may already have exited)", err)
		}
		select {
		case <-pwProc.waitCh:
		case <-time.After(8 * time.Second):
			t.Log("step 5d: password-error subprocess did not exit within 8s after SIGTERM")
		}
		select {
		case <-pwProc.doneCh:
		case <-time.After(4 * time.Second):
			t.Log("step 5d: scanner goroutine did not finish within 4s after wait timeout")
		}

		pwStderrBytes := pwProc.stderr.Bytes()
		pwLeaked := signing.FixtureKeySentinel().Scan(pwStderrBytes)
		var pwKeyLeaks []string
		for _, form := range pwLeaked {
			if form == "address-checksummed" || form == "address-lower-nox" {
				continue
			}
			pwKeyLeaks = append(pwKeyLeaks, form)
		}
		if len(pwKeyLeaks) > 0 {
			t.Errorf("step 5d: fixture key leaked in password-error subprocess stderr: forms=%v",
				pwKeyLeaks)
		}
	}

	// ── Step 5e: keystore_error (startup refusal, HTTP mode) ──────────────────────
	// A subprocess with keystore-no-address.json must exit non-zero BEFORE the HTTP
	// server starts.  We supply --http and a valid token file (needed for the startup
	// permission check); the binary fails during vault construction (step 4 of run()).
	//
	// NOTE: internal_error is NOT force-able through the real binary without a fault
	// hook.  It is covered by TestSignTransaction_SixCodesWireEncoding in handlers_test.go.
	{
		ksErrRaw := randTokenBytes(16)
		defer signing.ZeroBytes(ksErrRaw) // NOTE 4: zero raw bytes per ADR-009 hygiene
		ksErrTokenFile := writeTokenFile(t, hexEncodeBytes(ksErrRaw)+"\n")

		ksCtx, ksCancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer ksCancel()

		ksCmd := exec.CommandContext(ksCtx, bin,
			"--keystore", noAddrKs,
			"--password-file", pw,
			"--http",
			"--http-auth-token-file", ksErrTokenFile,
		)
		var ksStderr syncBuffer
		ksCmd.Stderr = &ksStderr

		ksRunErr := ksCmd.Run()
		if ksRunErr == nil {
			t.Fatal("step 5e: binary exited 0; want non-zero (no-address keystore)")
		}
		var ksExitErr *exec.ExitError
		if !errors.As(ksRunErr, &ksExitErr) {
			t.Fatalf("step 5e: unexpected error type %T: %v", ksRunErr, ksRunErr)
		}
		if ksExitErr.ExitCode() == 0 {
			t.Error("step 5e: exit code = 0; want non-zero for no-address keystore")
		}
		ksStderrStr := ksStderr.String()
		// strings.Contains is justified here: the binary exits before the HTTP server
		// starts, so there is no MCP Content[0] to JSON-parse. The startup error is
		// emitted as a plain-text line to stderr (fmt.Fprintf in main.go run()). This
		// is NOT precedent against the JSON-parse discipline used for all MCP error paths.
		if !strings.Contains(ksStderrStr, "keystore_error") {
			t.Errorf("step 5e: stderr missing 'keystore_error'\nstderr: %s", ksStderrStr)
		}
		// Leak scan on keystore-error subprocess stderr.
		ksLeaked := signing.FixtureKeySentinel().Scan(ksStderr.Bytes())
		var ksKeyLeaks []string
		for _, form := range ksLeaked {
			if form == "address-checksummed" || form == "address-lower-nox" {
				continue
			}
			ksKeyLeaks = append(ksKeyLeaks, form)
		}
		if len(ksKeyLeaks) > 0 {
			t.Errorf("step 5e: fixture key leaked in keystore-error subprocess stderr: forms=%v",
				ksKeyLeaks)
		}
	}

	// ── Teardown: close session, SIGTERM, wait for exit 0 ────────────────────────
	//
	// Close the client session before sending SIGTERM so the server has no in-flight
	// connections to drain; Shutdown completes immediately within the 3 s grace window.
	// sdkClient's t.Cleanup also tries cs.Close() (benign duplicate — logs only).
	if closeErr := cs.Close(); closeErr != nil {
		t.Logf("teardown: cs.Close: %v (benign — session may already be closed)", closeErr)
	}

	// SIGTERM → graceful shutdown (3 s grace) → exit 0.
	if sigErr := proc.cmd.Process.Signal(syscall.SIGTERM); sigErr != nil {
		t.Logf("teardown: SIGTERM: %v (may have already exited)", sigErr)
	}

	// Wait for the binary to exit + stderr scanner to finish.
	var mainWaitErr error
	select {
	case mainWaitErr = <-proc.waitCh:
	case <-time.After(8 * time.Second):
		t.Fatal("teardown: HTTP binary did not exit within 8s after SIGTERM")
	}
	// Drain doneCh (scanner goroutine) — should be fast since waitCh already closed.
	select {
	case <-proc.doneCh:
	case <-time.After(2 * time.Second):
		t.Log("teardown: stderr scanner did not finish within 2s (proceeding)")
	}

	// Assert exit code 0 (graceful shutdown via signal.NotifyContext).
	if mainWaitErr != nil {
		var exitErr *exec.ExitError
		if errors.As(mainWaitErr, &exitErr) {
			t.Errorf("teardown: exit code = %d; want 0 (SIGTERM → graceful shutdown)",
				exitErr.ExitCode())
		} else {
			t.Errorf("teardown: cmd.Wait: %v", mainWaitErr)
		}
	}

	// ── Step 6: Audit-line + reqlog correlation ────────────────────────────────────
	//
	// After the process exits, all stderr is in proc.stderr.Bytes().
	// Parse log lines and find the audit line from the happy-path sign_transaction
	// (step 4) and its matching reqlog line.  Both must carry the same request_id.
	//
	// parseLogLines and reqlogLines defined in reqlog_test.go (same package).
	stderrBytes := proc.stderr.Bytes()
	allLines := parseLogLines(bytes.NewBuffer(stderrBytes))

	// Find audit line(s) — msg contains "signed successfully".
	var auditLines []map[string]any
	for _, m := range allLines {
		if msg, _ := m["msg"].(string); strings.Contains(msg, "signed successfully") {
			auditLines = append(auditLines, m)
		}
	}
	// There should be exactly ONE audit line (the step-4 happy-path call).
	if len(auditLines) == 0 {
		t.Errorf("step 6: no audit line found in HTTP stderr\nstderr:\n%s", stderrBytes)
	} else {
		// Verify required audit fields.
		auditLine := auditLines[0]
		for _, field := range []string{"request_id", "tx_hash", "chain_id", "nonce"} {
			if _, ok := auditLine[field]; !ok {
				t.Errorf("step 6: audit line missing field %q", field)
			}
		}

		// Find matching reqlog line with the same request_id.
		auditRID, _ := auditLine["request_id"].(string)
		if auditRID == "" {
			t.Error("step 6: audit line has empty request_id")
		} else {
			rqLines := reqlogLines(allLines)
			var matched bool
			for _, rl := range rqLines {
				if rid, _ := rl["request_id"].(string); rid == auditRID {
					matched = true
					break
				}
			}
			if !matched {
				t.Errorf("step 6: audit request_id=%q has no matching reqlog line "+
					"(reqlog must propagate the same request_id as the signing context)",
					auditRID)
			}
		}
	}

	// ── Step 7: Leak scan over all HTTP-side stderr ───────────────────────────────
	//
	// All captured stderr from the main subprocess (steps 1-4, 5a-5b) is scanned
	// for fixture private-key material.  Address forms are excluded because the
	// address legitimately appears in the "keystore loaded" startup log line.
	leaked := signing.FixtureKeySentinel().Scan(stderrBytes)
	var keyLeaks []string
	for _, form := range leaked {
		if form == "address-checksummed" || form == "address-lower-nox" {
			continue // address legitimately present in startup logs
		}
		keyLeaks = append(keyLeaks, form)
	}
	if len(keyLeaks) > 0 {
		t.Errorf("step 7: fixture private key material leaked in HTTP stderr: forms=%v", keyLeaks)
	}
}
