//go:build !windows

package server

// leakaudit_e2e_test.go — Issue 4.4: end-to-end leak audit.
//
// Drives the full signing path on both transports at debug log level and scans
// every captured byte for the fixture private key in all encoded forms.
//
// Coverage matrix:
//   - Transport:  in-memory (stdio-equivalent) + real Streamable HTTP subprocess.
//   - Path class: happy path (get_address + sign_transaction audit line included),
//     all six error codes (invalid_input, unsupported_type, chain_id_mismatch,
//     keystore_error, password_error, internal_error).
//   - Encoded forms scanned: raw, hex-lower, hex-upper, base64-std, base64-raw,
//     base64-url, base64-rawurl, decimal (via signing.FixtureKeySentinel).
//   - Positive control: each captured stream must contain a known non-secret marker
//     ("tx_hash") proving the logger was actually emitting at debug level.
//
// All error-code flows use the in-memory transport for speed.
// The HTTP transport is used only for the happy path.
// The password_error flow uses a real signer with a wrong-password file so that
// password bytes are actually read before the decrypt fails.
// The internal_error flow uses a panicVault (panics in WithSigningKey) to trigger
// the signer.go panic-recovery path (defer func / recover), which returns CodeInternalError.
//
// Skipped under -short (requires ~15 s for light-scrypt decrypts).

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/ethereum/go-ethereum/common"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/rootwarp/blockchain-ai-tools/apps/eth-signer-mcp/internal/signing"
)

// ── panicVault ────────────────────────────────────────────────────────────────

// panicKeyVault implements signing.KeyVault and panics in WithSigningKey.
// When wrapped by signing.NewSigner, the panic is caught by signer.go's
// defer/recover, which emits a REDACTED error log and returns CodeInternalError.
// This triggers the internal_error path without modifying production code.
type panicKeyVault struct {
	addr common.Address
}

func (p *panicKeyVault) Address() common.Address { return p.addr }

func (p *panicKeyVault) WithSigningKey(_ context.Context, _ func(signing.SigningKey) error) error {
	panic("panicKeyVault: test-induced panic for internal_error leak-audit path")
}

// ── leak-audit helper ─────────────────────────────────────────────────────────

// leakAuditScan scans output with the fixture key sentinel and fails the test
// if any key material forms are found. Address forms are excluded because the
// EIP-55 checksummed address legitimately appears in startup logs.
//
// desc identifies the stream (e.g. "in-memory happy path log") in failure messages.
// SAFETY: never include output bytes or any encoded key form in failure messages;
// report only form names.
func leakAuditScan(t *testing.T, desc string, output []byte) {
	t.Helper()
	sent := signing.FixtureKeySentinel()
	leaked := sent.Scan(output)
	var keyLeaks []string
	for _, form := range leaked {
		if form == "address-checksummed" || form == "address-lower-nox" {
			continue // address legitimately present in startup/audit logs
		}
		keyLeaks = append(keyLeaks, form)
	}
	if len(keyLeaks) > 0 {
		t.Errorf("leak audit [%s]: fixture private key leaked in form(s): %v (sentinel: %q)",
			desc, keyLeaks, sent.Name)
	}
}

// assertToolErrorCode checks that a CallTool response encodes the expected ToolError code.
// Mirrors assertHTTPToolError from http_e2e_test.go.
func assertToolErrorCode(
	t *testing.T,
	result *mcp.CallToolResult,
	callErr error,
	wantCode string,
	desc string,
) {
	t.Helper()
	if callErr != nil {
		t.Fatalf("assertToolErrorCode(%q) [%s]: protocol error: %v", wantCode, desc, callErr)
	}
	if result == nil {
		t.Fatalf("assertToolErrorCode(%q) [%s]: result is nil", wantCode, desc)
	}
	if !result.IsError {
		t.Fatalf("assertToolErrorCode(%q) [%s]: IsError=false; want true", wantCode, desc)
	}
	if len(result.Content) == 0 {
		t.Fatalf("assertToolErrorCode(%q) [%s]: Content is empty", wantCode, desc)
	}
	tc, ok := result.Content[0].(*mcp.TextContent)
	if !ok {
		t.Fatalf("assertToolErrorCode(%q) [%s]: Content[0] is %T; want *mcp.TextContent",
			wantCode, desc, result.Content[0])
	}
	var decoded map[string]json.RawMessage
	if err := json.Unmarshal([]byte(tc.Text), &decoded); err != nil {
		t.Fatalf("assertToolErrorCode(%q) [%s]: Content[0].Text not valid JSON: %v",
			wantCode, desc, err)
	}
	var gotCode string
	if err := json.Unmarshal(decoded["code"], &gotCode); err != nil {
		t.Fatalf("assertToolErrorCode(%q) [%s]: unmarshal code: %v", wantCode, desc, err)
	}
	if gotCode != wantCode {
		t.Errorf("assertToolErrorCode [%s]: code=%q; want %q; text: %s",
			desc, gotCode, wantCode, tc.Text)
	}
}

// collectTextContent extracts the text from Content[0] and appends it to buf.
// If the content is empty or wrong type, nothing is appended.
func collectTextContent(result *mcp.CallToolResult, buf *bytes.Buffer) {
	if result == nil || len(result.Content) == 0 {
		return
	}
	tc, ok := result.Content[0].(*mcp.TextContent)
	if !ok {
		return
	}
	buf.WriteString(tc.Text)
}

// ── Main leak audit test ──────────────────────────────────────────────────────

// TestLeakAudit_FullE2E is the end-to-end leak audit committed test (Issue 4.4).
// It proves the fixture private key does not appear in any captured output across
// the full signing path, all error codes, and both transports.
func TestLeakAudit_FullE2E(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping leak audit test under -short (requires ~15 s for light-scrypt decrypts)")
	}
	// Not t.Parallel() — builds and runs binary subprocesses; uses light-scrypt KDF.

	tdPath := signingTestdataPath(t) // handlers_test.go
	ksLight := filepath.Join(tdPath, "keystore-light.json")
	pwPath := filepath.Join(tdPath, "password.txt")

	testCtx, testCancel := context.WithTimeout(context.Background(), 180*time.Second)
	defer testCancel()

	// allResponseBodies collects every response body text from all calls so we
	// can scan them in bulk at the end.
	var allResponseBodies bytes.Buffer

	// ── Section 1: in-memory happy path (real vault + debug-level logger) ────────
	//
	// Uses keystore-light.json (~50 ms/decrypt) with a debug-level logger capturing
	// all log output in inMemHappyBuf.  The positive control asserts that "tx_hash"
	// appears in the captured output (proving the debug-level audit line fired).

	var inMemHappyBuf bytes.Buffer
	happyLogger := slog.New(slog.NewJSONHandler(&inMemHappyBuf, &slog.HandlerOptions{Level: slog.LevelDebug}))

	happyVault, err := signing.NewFileKeyVault(signing.VaultOptions{
		KeystorePath: ksLight,
		PasswordPath: pwPath,
	})
	if err != nil {
		t.Fatalf("in-mem happy path: NewFileKeyVault: %v", err)
	}
	happySigner := signing.NewSigner(happyVault, signing.SignerOptions{Logger: happyLogger})
	happySrv := New(happySigner, Options{
		Name:    "leak-audit-happy",
		Version: "v0.0.0-test",
		Logger:  happyLogger,
	})

	happyCS, happyCleanup := inMemorySession(t, happySrv, testCtx)
	defer happyCleanup()

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

	// get_address — happy path
	{
		callCtx, callCancel := context.WithTimeout(testCtx, 10*time.Second)
		addrResult, addrErr := happyCS.CallTool(callCtx, &mcp.CallToolParams{
			Name:      "get_address",
			Arguments: map[string]any{},
		})
		callCancel()
		if addrErr != nil || addrResult == nil || addrResult.IsError {
			t.Fatalf("in-mem get_address: unexpected error: callErr=%v isError=%v",
				addrErr, addrResult.GetError())
		}
		if got := addrResult.Content; len(got) > 0 {
			if tc, ok := got[0].(*mcp.TextContent); ok {
				if !strings.Contains(tc.Text, signing.FixtureTestAddress) {
					t.Errorf("in-mem get_address: response does not contain fixture address")
				}
			}
		}
		collectTextContent(addrResult, &allResponseBodies)
	}

	// sign_transaction — happy path; audit line fires at info level within debug context
	var happyTxHash string
	{
		callCtx, callCancel := context.WithTimeout(testCtx, 15*time.Second)
		signResult, signErr := happyCS.CallTool(callCtx, &mcp.CallToolParams{
			Name:      "sign_transaction",
			Arguments: legacyArgs,
		})
		callCancel()
		if signErr != nil || signResult == nil || signResult.IsError {
			t.Fatalf("in-mem sign_transaction happy path: unexpected error: callErr=%v isError=%v",
				signErr, signResult.GetError())
		}
		collectTextContent(signResult, &allResponseBodies)

		// Extract tx_hash for positive control.
		if len(signResult.Content) > 0 {
			if tc, ok := signResult.Content[0].(*mcp.TextContent); ok {
				var sr signing.SignResult
				if jsonErr := json.Unmarshal([]byte(tc.Text), &sr); jsonErr == nil {
					happyTxHash = sr.Hash
				}
			}
		}
	}

	// Positive control: debug-level audit line must contain "tx_hash".
	// An empty capture that passes the sentinel scan would be meaningless.
	inMemHappyBytes := inMemHappyBuf.Bytes()
	if !bytes.Contains(inMemHappyBytes, []byte("tx_hash")) {
		t.Error("in-mem happy path: positive control FAIL — 'tx_hash' not found in debug log output; logger may not be at debug level")
	}
	if happyTxHash == "" {
		t.Error("in-mem happy path: positive control FAIL — tx_hash not parsed from sign_transaction response")
	}

	// ── Section 2: in-memory error paths ──────────────────────────────────────
	//
	// Each error code is exercised via a separate in-memory session with an
	// appropriate signer/vault.  Log output + response bodies are scanned.

	// 2a: invalid_input — chainId:"0" passes schema but triggers rule-1 rejection.
	{
		var errBuf bytes.Buffer
		errLogger := slog.New(slog.NewJSONHandler(&errBuf, &slog.HandlerOptions{Level: slog.LevelDebug}))
		errSrv := New(happySigner, Options{Name: "audit-invalid-input", Version: "v0.0.0-test", Logger: errLogger})
		errCS, errCleanup := inMemorySession(t, errSrv, testCtx)
		defer errCleanup()

		callCtx, callCancel := context.WithTimeout(testCtx, 10*time.Second)
		r, rErr := errCS.CallTool(callCtx, &mcp.CallToolParams{
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
		assertToolErrorCode(t, r, rErr, signing.CodeInvalidInput, "invalid_input")
		collectTextContent(r, &allResponseBodies)
		leakAuditScan(t, "error path: invalid_input log", errBuf.Bytes())
	}

	// 2b: unsupported_type — type:"0x3" passes schema but rule-3 → unsupported_type.
	{
		var errBuf bytes.Buffer
		errLogger := slog.New(slog.NewJSONHandler(&errBuf, &slog.HandlerOptions{Level: slog.LevelDebug}))
		errSrv := New(happySigner, Options{Name: "audit-unsupported-type", Version: "v0.0.0-test", Logger: errLogger})
		errCS, errCleanup := inMemorySession(t, errSrv, testCtx)
		defer errCleanup()

		callCtx, callCancel := context.WithTimeout(testCtx, 10*time.Second)
		r, rErr := errCS.CallTool(callCtx, &mcp.CallToolParams{
			Name: "sign_transaction",
			Arguments: map[string]any{
				"type":    "0x3", // unsupported type
				"chainId": "1",
				"nonce":   "0",
				"value":   "0",
				"data":    "0x",
				"gas":     "21000",
			},
		})
		callCancel()
		assertToolErrorCode(t, r, rErr, signing.CodeUnsupportedType, "unsupported_type")
		collectTextContent(r, &allResponseBodies)
		leakAuditScan(t, "error path: unsupported_type log", errBuf.Bytes())
	}

	// 2c: chain_id_mismatch — signer with ChainIDGuard=5, request with chainId:"1".
	{
		guardFive := uint64(5)
		var errBuf bytes.Buffer
		errLogger := slog.New(slog.NewJSONHandler(&errBuf, &slog.HandlerOptions{Level: slog.LevelDebug}))
		cmVault, cmVaultErr := signing.NewFileKeyVault(signing.VaultOptions{
			KeystorePath: ksLight,
			PasswordPath: pwPath,
		})
		if cmVaultErr != nil {
			t.Fatalf("chain_id_mismatch: NewFileKeyVault: %v", cmVaultErr)
		}
		cmSigner := signing.NewSigner(cmVault, signing.SignerOptions{
			ChainIDGuard: &guardFive,
			Logger:       errLogger,
		})
		cmSrv := New(cmSigner, Options{Name: "audit-chain-mismatch", Version: "v0.0.0-test", Logger: errLogger})
		cmCS, cmCleanup := inMemorySession(t, cmSrv, testCtx)
		defer cmCleanup()

		callCtx, callCancel := context.WithTimeout(testCtx, 10*time.Second)
		r, rErr := cmCS.CallTool(callCtx, &mcp.CallToolParams{
			Name:      "sign_transaction",
			Arguments: legacyArgs, // chainId:"1" but guard=5 → mismatch
		})
		callCancel()
		assertToolErrorCode(t, r, rErr, signing.CodeChainIDMismatch, "chain_id_mismatch")
		collectTextContent(r, &allResponseBodies)
		leakAuditScan(t, "error path: chain_id_mismatch log", errBuf.Bytes())
	}

	// 2d: keystore_error — stub signer returning CodeKeystoreError.
	{
		var errBuf bytes.Buffer
		errLogger := slog.New(slog.NewJSONHandler(&errBuf, &slog.HandlerOptions{Level: slog.LevelDebug}))
		ksStub := &stubSigner{
			address: common.HexToAddress(signing.FixtureTestAddress),
			signFn: func(_ context.Context, _ signing.TxRequest) (*signing.SignResult, error) {
				return nil, &signing.ToolError{Code: signing.CodeKeystoreError, Message: "keystore unavailable"}
			},
		}
		ksSrv := newTestServerStub(t, ksStub.signFn, ksStub.address, &errBuf)
		// Override with debug-level logger (newTestServerStub uses debug internally — already debug)
		_ = errLogger // errBuf already has debug-level output from newTestServerStub
		ksCS, ksCleanup := inMemorySession(t, ksSrv, testCtx)
		defer ksCleanup()

		callCtx, callCancel := context.WithTimeout(testCtx, 10*time.Second)
		r, rErr := ksCS.CallTool(callCtx, &mcp.CallToolParams{
			Name:      "sign_transaction",
			Arguments: legacyArgs,
		})
		callCancel()
		assertToolErrorCode(t, r, rErr, signing.CodeKeystoreError, "keystore_error")
		collectTextContent(r, &allResponseBodies)
		leakAuditScan(t, "error path: keystore_error log", errBuf.Bytes())
	}

	// 2e: password_error — real signer with a wrong password file.
	// Password bytes are actually read before the decrypt fails (ErrDecrypt).
	{
		wrongPwPath := filepath.Join(t.TempDir(), "wrong-password.txt")
		if writeErr := os.WriteFile(wrongPwPath, []byte("definitely-wrong-password\n"), 0o600); writeErr != nil {
			t.Fatalf("password_error: write wrong password: %v", writeErr)
		}

		var errBuf bytes.Buffer
		errLogger := slog.New(slog.NewJSONHandler(&errBuf, &slog.HandlerOptions{Level: slog.LevelDebug}))
		pwVault, pwVaultErr := signing.NewFileKeyVault(signing.VaultOptions{
			KeystorePath: ksLight, // use light fixture — one decrypt attempt is enough
			PasswordPath: wrongPwPath,
		})
		if pwVaultErr != nil {
			t.Fatalf("password_error: NewFileKeyVault: %v", pwVaultErr)
		}
		pwSigner := signing.NewSigner(pwVault, signing.SignerOptions{Logger: errLogger})
		pwSrv := New(pwSigner, Options{Name: "audit-password-error", Version: "v0.0.0-test", Logger: errLogger})
		pwCS, pwCleanup := inMemorySession(t, pwSrv, testCtx)
		defer pwCleanup()

		callCtx, callCancel := context.WithTimeout(testCtx, 15*time.Second)
		r, rErr := pwCS.CallTool(callCtx, &mcp.CallToolParams{
			Name:      "sign_transaction",
			Arguments: legacyArgs,
		})
		callCancel()
		assertToolErrorCode(t, r, rErr, signing.CodePasswordError, "password_error")
		collectTextContent(r, &allResponseBodies)
		leakAuditScan(t, "error path: password_error log", errBuf.Bytes())
	}

	// 2f: internal_error — panicKeyVault causes signer.go's defer/recover to fire.
	// The recovered panic emits a REDACTED log line and returns CodeInternalError.
	// This is the "panicking-handler path" noted in the issue: the real signer's
	// panic-recovery path (not a stub returning the code directly).
	{
		var errBuf bytes.Buffer
		errLogger := slog.New(slog.NewJSONHandler(&errBuf, &slog.HandlerOptions{Level: slog.LevelDebug}))
		panicVault := &panicKeyVault{addr: common.HexToAddress(signing.FixtureTestAddress)}
		panicSigner := signing.NewSigner(panicVault, signing.SignerOptions{Logger: errLogger})
		panicSrv := New(panicSigner, Options{Name: "audit-internal-error", Version: "v0.0.0-test", Logger: errLogger})
		panicCS, panicCleanup := inMemorySession(t, panicSrv, testCtx)
		defer panicCleanup()

		callCtx, callCancel := context.WithTimeout(testCtx, 10*time.Second)
		r, rErr := panicCS.CallTool(callCtx, &mcp.CallToolParams{
			Name:      "sign_transaction",
			Arguments: legacyArgs,
		})
		callCancel()
		assertToolErrorCode(t, r, rErr, signing.CodeInternalError, "internal_error (panic recovery)")
		collectTextContent(r, &allResponseBodies)
		leakAuditScan(t, "error path: internal_error log", errBuf.Bytes())
	}

	// ── Scan in-memory happy path output ──────────────────────────────────────
	leakAuditScan(t, "in-memory happy path log", inMemHappyBytes)
	leakAuditScan(t, "all response bodies", allResponseBodies.Bytes())

	// ── Section 3: HTTP transport happy path (binary with --log-level debug) ─────
	//
	// The binary subprocess is launched with --log-level debug so the audit line
	// AND per-request reqlog lines appear in stderr.  We scan ALL stderr bytes.

	bin := getE2EBinary(t)

	httpRawToken := randTokenBytes(32)
	httpTokenStr := hexEncodeBytes(httpRawToken)
	defer signing.ZeroBytes(httpRawToken)
	httpTokenFile := writeTokenFile(t, httpTokenStr+"\n")

	// launchHTTPBinary registers t.Cleanup (Kill + goroutine drain).
	// MUST be registered BEFORE sdkClient so LIFO ordering closes session before Kill.
	httpProc := launchHTTPBinary(t, bin, ksLight, pwPath, httpTokenFile, "--log-level", "debug")

	httpEndpoint := fmt.Sprintf("http://%s", httpProc.addr.String())
	httpCS := sdkClient(t, testCtx, httpEndpoint, httpTokenStr)

	var httpResponseBodies bytes.Buffer

	// HTTP get_address
	{
		callCtx, callCancel := context.WithTimeout(testCtx, 10*time.Second)
		addrResult, addrErr := httpCS.CallTool(callCtx, &mcp.CallToolParams{
			Name:      "get_address",
			Arguments: map[string]any{},
		})
		callCancel()
		if addrErr != nil || addrResult == nil || addrResult.IsError {
			t.Fatalf("HTTP get_address: unexpected error: callErr=%v", addrErr)
		}
		collectTextContent(addrResult, &httpResponseBodies)
	}

	// HTTP sign_transaction
	var httpTxHash string
	{
		callCtx, callCancel := context.WithTimeout(testCtx, 15*time.Second)
		signResult, signErr := httpCS.CallTool(callCtx, &mcp.CallToolParams{
			Name:      "sign_transaction",
			Arguments: legacyArgs,
		})
		callCancel()
		if signErr != nil || signResult == nil || signResult.IsError {
			t.Fatalf("HTTP sign_transaction: unexpected error: callErr=%v", signErr)
		}
		collectTextContent(signResult, &httpResponseBodies)

		if len(signResult.Content) > 0 {
			if tc, ok := signResult.Content[0].(*mcp.TextContent); ok {
				var sr signing.SignResult
				if jsonErr := json.Unmarshal([]byte(tc.Text), &sr); jsonErr == nil {
					httpTxHash = sr.Hash
				}
			}
		}
	}

	// Teardown: close session, SIGTERM, wait for exit.
	if closeErr := httpCS.Close(); closeErr != nil {
		t.Logf("HTTP teardown: httpCS.Close: %v (benign)", closeErr)
	}
	if sigErr := httpProc.cmd.Process.Signal(syscall.SIGTERM); sigErr != nil {
		t.Logf("HTTP teardown: SIGTERM: %v (may already have exited)", sigErr)
	}
	select {
	case <-httpProc.waitCh:
	case <-time.After(8 * time.Second):
		t.Fatal("HTTP teardown: binary did not exit within 8s after SIGTERM")
	}
	select {
	case <-httpProc.doneCh:
	case <-time.After(2 * time.Second):
		t.Log("HTTP teardown: stderr scanner did not finish within 2s (proceeding)")
	}

	// Collect all HTTP stderr bytes (available after process exit + scanner drain).
	httpStderrBytes := httpProc.stderr.Bytes()

	// Positive control for HTTP: "tx_hash" must appear in HTTP debug stderr.
	if !bytes.Contains(httpStderrBytes, []byte("tx_hash")) {
		t.Error("HTTP positive control FAIL — 'tx_hash' not found in HTTP binary debug stderr; " +
			"logger may not be at debug level or audit line was not emitted")
	}
	if httpTxHash == "" {
		t.Error("HTTP positive control FAIL — tx_hash not parsed from HTTP sign_transaction response")
	}

	// Scan HTTP stderr for key material.
	leakAuditScan(t, "HTTP binary stderr", httpStderrBytes)
	// Scan HTTP response bodies.
	leakAuditScan(t, "HTTP response bodies", httpResponseBodies.Bytes())
}
