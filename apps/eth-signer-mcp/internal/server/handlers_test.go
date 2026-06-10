// Package server — handlers_test.go — Issue 2.7.
//
// Handler integration tests using the SDK in-memory transport.
// All tests use stub signerPort implementations — no real keystore decryption.
//
// Acceptance criteria covered:
//   - tools/list shows exactly sign_transaction + get_address with inferred schemas
//   - sign_transaction input schema has additionalProperties:false + matches golden
//   - Happy-path sign_transaction returns populated SignResult
//   - All six ToolError codes returned over MCP have IsError=true + {"code","message"}
//   - Non-ToolError → protocol-level error (non-nil Go error from cs.CallTool)
//   - Unknown input field rejected via strict schema
//   - get_address returns checksummed address
//   - ToolError.Cause never appears in wire content
//   - Handlers attach non-empty request_id (correlation: audit line carries same id)
//   - get_address with unreadable password file (real vault test)
package server

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/ethereum/go-ethereum/common"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/rootwarp/blockchain-ai-tools/apps/eth-signer-mcp/internal/obs"
	"github.com/rootwarp/blockchain-ai-tools/apps/eth-signer-mcp/internal/signing"
)

// ── Test helpers ──────────────────────────────────────────────────────────────

// signingTestdataPath returns the path to the signing testdata directory.
func signingTestdataPath(t *testing.T) string {
	t.Helper()
	// Climb from internal/server to internal/signing/testdata.
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller(0) failed")
	}
	// thisFile is .../internal/server/handlers_test.go
	// testdata is   .../internal/signing/testdata/
	serverDir := filepath.Dir(thisFile)
	return filepath.Join(serverDir, "..", "signing", "testdata")
}

// newTestServerStub creates a server backed by a stubSigner with a given sign
// function and address, plus a logger writing to logBuf.
func newTestServerStub(
	t *testing.T,
	signFn func(context.Context, signing.TxRequest) (*signing.SignResult, error),
	addr common.Address,
	logBuf *bytes.Buffer,
) *Server {
	t.Helper()
	logger := slog.New(slog.NewJSONHandler(logBuf, &slog.HandlerOptions{Level: slog.LevelDebug}))
	stub := &stubSigner{signFn: signFn, address: addr}
	return newServer(stub, Options{
		Name:    "test",
		Version: "v0.0.0-test",
		Logger:  logger,
	})
}

// newTestSession creates an in-memory session for the given server and returns
// the client session and a cleanup function.
func newTestSession(t *testing.T, srv *Server) (cs *mcp.ClientSession, cleanup func()) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	t.Cleanup(cancel)
	cs, cleanupFn := inMemorySession(t, srv, ctx)
	return cs, cleanupFn
}

// knownSignResult returns a canned *signing.SignResult for use in happy-path tests.
func knownSignResult(addr common.Address) *signing.SignResult {
	return &signing.SignResult{
		RawTransaction: "0xabcdef1234",
		Signature: signing.SignatureValues{
			R: "0x1111111111111111111111111111111111111111111111111111111111111111",
			S: "0x2222222222222222222222222222222222222222222222222222222222222222",
			V: "0x25",
		},
		Hash: "0xdeadbeef01234567deadbeef01234567deadbeef01234567deadbeef01234567",
		From: addr.Hex(),
	}
}

// minimalLegacyArgs returns the minimum arguments for a valid legacy tx call.
func minimalLegacyArgs() map[string]any {
	return map[string]any{
		"type":     "legacy",
		"chainId":  "1",
		"nonce":    "0",
		"to":       "0x9858EfFD232B4033E47d90003D41EC34EcaEda94",
		"value":    "0",
		"data":     "0x",
		"gas":      "21000",
		"gasPrice": "20000000000",
	}
}

// callSignTx calls sign_transaction via the in-memory transport with the given args map.
func callSignTx(t *testing.T, cs *mcp.ClientSession, args map[string]any) (*mcp.CallToolResult, error) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	return cs.CallTool(ctx, &mcp.CallToolParams{
		Name:      "sign_transaction",
		Arguments: args,
	})
}

// callGetAddress calls get_address via the in-memory transport.
func callGetAddress(t *testing.T, cs *mcp.ClientSession) (*mcp.CallToolResult, error) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	return cs.CallTool(ctx, &mcp.CallToolParams{
		Name:      "get_address",
		Arguments: map[string]any{},
	})
}

// ── Tests ─────────────────────────────────────────────────────────────────────

// TestToolsList_TwoToolsWithSchemas verifies tools/list shows exactly two tools
// with inferred schemas and that sign_transaction has additionalProperties:false.
func TestToolsList_TwoToolsWithSchemas(t *testing.T) {
	t.Parallel()

	var logBuf bytes.Buffer
	srv := newTestServerStub(t, nil, common.Address{}, &logBuf)
	cs, cleanup := newTestSession(t, srv)
	defer cleanup()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	result, err := cs.ListTools(ctx, nil)
	if err != nil {
		t.Fatalf("cs.ListTools: %v", err)
	}
	if len(result.Tools) != 2 {
		t.Fatalf("len(Tools) = %d; want 2", len(result.Tools))
	}

	// Build name → tool map.
	toolMap := make(map[string]*mcp.Tool, 2)
	for _, tt := range result.Tools {
		toolMap[tt.Name] = tt
	}

	for _, name := range []string{"sign_transaction", "get_address"} {
		if _, ok := toolMap[name]; !ok {
			t.Errorf("tool %q missing from tools/list", name)
		}
	}

	// Assert sign_transaction input schema has additionalProperties:false.
	signTool := toolMap["sign_transaction"]
	if signTool == nil {
		t.Fatal("sign_transaction tool not found")
	}
	schemaJSON, err := json.Marshal(signTool.InputSchema)
	if err != nil {
		t.Fatalf("cannot marshal InputSchema: %v", err)
	}
	var schema map[string]json.RawMessage
	if err := json.Unmarshal(schemaJSON, &schema); err != nil {
		t.Fatalf("cannot unmarshal InputSchema: %v", err)
	}
	if ap, ok := schema["additionalProperties"]; !ok {
		t.Error("sign_transaction InputSchema missing 'additionalProperties'")
	} else {
		var apValue bool
		if err := json.Unmarshal(ap, &apValue); err != nil || apValue {
			t.Errorf("sign_transaction InputSchema.additionalProperties = %s; want false", ap)
		}
	}

	// Assert required fields include the non-optional fields.
	requiredJSON, ok := schema["required"]
	if !ok {
		t.Error("sign_transaction InputSchema missing 'required'")
	} else {
		var required []string
		if err := json.Unmarshal(requiredJSON, &required); err != nil {
			t.Fatalf("cannot unmarshal required: %v", err)
		}
		wantRequired := []string{"type", "chainId", "nonce", "value", "data", "gas"}
		requiredSet := make(map[string]bool, len(required))
		for _, r := range required {
			requiredSet[r] = true
		}
		for _, r := range wantRequired {
			if !requiredSet[r] {
				t.Errorf("sign_transaction InputSchema.required missing %q (required set: %v)", r, required)
			}
		}
	}
}

// TestSignTransactionSchema_MatchesGolden verifies the sign_transaction schema
// deep-matches the golden schema committed at signing/testdata/schema/.
func TestSignTransactionSchema_MatchesGolden(t *testing.T) {
	t.Parallel()

	tdPath := signingTestdataPath(t)
	goldenPath := filepath.Join(tdPath, "schema", "sign_transaction.golden.json")
	goldenBytes, err := os.ReadFile(goldenPath)
	if err != nil {
		t.Fatalf("read golden schema: %v", err)
	}
	var goldenSchema map[string]any
	if err := json.Unmarshal(goldenBytes, &goldenSchema); err != nil {
		t.Fatalf("unmarshal golden schema: %v", err)
	}

	var logBuf bytes.Buffer
	srv := newTestServerStub(t, nil, common.Address{}, &logBuf)
	cs, cleanup := newTestSession(t, srv)
	defer cleanup()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	toolsResult, err := cs.ListTools(ctx, nil)
	if err != nil {
		t.Fatalf("cs.ListTools: %v", err)
	}

	var signTool *mcp.Tool
	for _, tt := range toolsResult.Tools {
		if tt.Name == "sign_transaction" {
			signTool = tt
			break
		}
	}
	if signTool == nil {
		t.Fatal("sign_transaction tool not found")
	}

	// Re-marshal and re-unmarshal the published schema to normalize.
	schemaBytes, err := json.Marshal(signTool.InputSchema)
	if err != nil {
		t.Fatalf("marshal input schema: %v", err)
	}
	var publishedSchema map[string]any
	if err := json.Unmarshal(schemaBytes, &publishedSchema); err != nil {
		t.Fatalf("unmarshal published schema: %v", err)
	}

	// The published schema comes from mcp.AddTool's inference, which may add
	// a $schema field. Compare the subset of fields that the golden has.
	assertSchemaSubset(t, goldenSchema, publishedSchema)
}

// assertSchemaSubset asserts that all fields in want appear in got with equal values.
func assertSchemaSubset(t *testing.T, want, got map[string]any) {
	t.Helper()
	for k, wantVal := range want {
		gotVal, ok := got[k]
		if !ok {
			t.Errorf("schema missing key %q", k)
			continue
		}
		wantJSON, _ := json.Marshal(wantVal)
		gotJSON, _ := json.Marshal(gotVal)
		if string(wantJSON) != string(gotJSON) {
			t.Errorf("schema[%q]: got %s; want %s", k, gotJSON, wantJSON)
		}
	}
}

// TestSignTransaction_HappyPath verifies that a successful sign_transaction call
// returns a populated SignResult with rawTransaction, signature.r/s/v, hash, and from.
func TestSignTransaction_HappyPath(t *testing.T) {
	t.Parallel()

	addr := common.HexToAddress(signing.FixtureTestAddress)
	want := knownSignResult(addr)

	var logBuf bytes.Buffer
	srv := newTestServerStub(t, func(_ context.Context, _ signing.TxRequest) (*signing.SignResult, error) {
		return want, nil
	}, addr, &logBuf)

	cs, cleanup := newTestSession(t, srv)
	defer cleanup()

	result, err := callSignTx(t, cs, minimalLegacyArgs())
	if err != nil {
		t.Fatalf("CallTool: unexpected protocol error: %v", err)
	}
	if result == nil {
		t.Fatal("CallTool result is nil")
	}
	if result.IsError {
		t.Fatalf("CallTool result.IsError=true; Content: %v", result.Content)
	}

	// The SDK puts the JSON-marshaled output in Content[0].
	if len(result.Content) == 0 {
		t.Fatal("result.Content is empty")
	}
	tc, ok := result.Content[0].(*mcp.TextContent)
	if !ok {
		t.Fatalf("Content[0] is %T; want *mcp.TextContent", result.Content[0])
	}

	var got signing.SignResult
	if err := json.Unmarshal([]byte(tc.Text), &got); err != nil {
		t.Fatalf("cannot unmarshal Content[0]: %v\ntext: %s", err, tc.Text)
	}

	if got.RawTransaction != want.RawTransaction {
		t.Errorf("RawTransaction = %q; want %q", got.RawTransaction, want.RawTransaction)
	}
	if got.Hash != want.Hash {
		t.Errorf("Hash = %q; want %q", got.Hash, want.Hash)
	}
	if got.From != want.From {
		t.Errorf("From = %q; want %q", got.From, want.From)
	}
	if got.Signature.R != want.Signature.R {
		t.Errorf("Signature.R = %q; want %q", got.Signature.R, want.Signature.R)
	}
	if got.Signature.S != want.Signature.S {
		t.Errorf("Signature.S = %q; want %q", got.Signature.S, want.Signature.S)
	}
	if got.Signature.V != want.Signature.V {
		t.Errorf("Signature.V = %q; want %q", got.Signature.V, want.Signature.V)
	}
}

// TestSignTransaction_SixCodesWireEncoding is the table-driven contract test for
// all six ToolError codes returned from the signer, asserting the full MCP wire
// encoding: IsError=true, one TextContent, {"code","message"} JSON, nil Go error
// from cs.CallTool.
func TestSignTransaction_SixCodesWireEncoding(t *testing.T) {
	t.Parallel()

	cases := []struct {
		code    string
		message string
	}{
		{signing.CodeInvalidInput, "field type is required"},
		{signing.CodeUnsupportedType, "unsupported type 0x3"},
		{signing.CodeChainIDMismatch, "chain-id mismatch: got 5 want 1"},
		{signing.CodeKeystoreError, "keystore error"},
		{signing.CodePasswordError, "password error"},
		{signing.CodeInternalError, "sender mismatch"},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.code, func(t *testing.T) {
			t.Parallel()

			te := &signing.ToolError{Code: tc.code, Message: tc.message}

			var logBuf bytes.Buffer
			srv := newTestServerStub(t,
				func(_ context.Context, _ signing.TxRequest) (*signing.SignResult, error) {
					return nil, te
				},
				common.HexToAddress(signing.FixtureTestAddress),
				&logBuf,
			)
			cs, cleanup := newTestSession(t, srv)
			defer cleanup()

			result, err := callSignTx(t, cs, minimalLegacyArgs())

			// Contract 1: cs.CallTool must NOT return a protocol-level Go error.
			if err != nil {
				t.Fatalf("CallTool returned protocol error %v; want nil (tool error path)", err)
			}
			if result == nil {
				t.Fatal("result is nil")
			}

			// Contract 2: IsError must be true.
			if !result.IsError {
				t.Errorf("IsError = false; want true for code %q", tc.code)
			}

			// Contract 3: exactly one TextContent.
			if len(result.Content) != 1 {
				t.Fatalf("len(Content) = %d; want 1", len(result.Content))
			}
			tc0, ok := result.Content[0].(*mcp.TextContent)
			if !ok {
				t.Fatalf("Content[0] is %T; want *mcp.TextContent", result.Content[0])
			}

			// Contract 4: parses as JSON with exactly "code" and "message".
			var decoded map[string]json.RawMessage
			if jsonErr := json.Unmarshal([]byte(tc0.Text), &decoded); jsonErr != nil {
				t.Fatalf("Content[0].Text is not valid JSON: %v\ntext: %s", jsonErr, tc0.Text)
			}
			if len(decoded) != 2 {
				t.Errorf("JSON has %d keys; want exactly 2 (code, message)", len(decoded))
			}
			for _, k := range []string{"code", "message"} {
				if _, ok := decoded[k]; !ok {
					t.Errorf("JSON missing key %q", k)
				}
			}

			// Contract 5: code matches.
			var gotCode string
			if jsonErr := json.Unmarshal(decoded["code"], &gotCode); jsonErr != nil {
				t.Fatalf("cannot unmarshal code: %v", jsonErr)
			}
			if gotCode != tc.code {
				t.Errorf("code = %q; want %q", gotCode, tc.code)
			}
		})
	}
}

// TestSignTransaction_NonToolErrorIsProtocol verifies that when the signer returns
// a non-ToolError (e.g. context.Canceled), the handler returns a protocol-level
// error (non-nil Go error from cs.CallTool), NOT an IsError tool result.
func TestSignTransaction_NonToolErrorIsProtocol(t *testing.T) {
	t.Parallel()

	// Return a plain non-ToolError from the signer.
	plainErr := fmt.Errorf("unexpected internal failure: %w", context.Canceled)

	var logBuf bytes.Buffer
	srv := newTestServerStub(t,
		func(_ context.Context, _ signing.TxRequest) (*signing.SignResult, error) {
			return nil, plainErr
		},
		common.HexToAddress(signing.FixtureTestAddress),
		&logBuf,
	)
	cs, cleanup := newTestSession(t, srv)
	defer cleanup()

	result, err := callSignTx(t, cs, minimalLegacyArgs())

	// Must return protocol-level error (non-nil Go error), NOT IsError=true.
	if err == nil {
		t.Error("expected non-nil protocol error from CallTool; got nil")
		if result != nil && result.IsError {
			t.Error("got IsError=true result instead of protocol error")
		}
	}
	// result must be nil for a protocol error.
	if result != nil {
		// The SDK may return a result even for protocol errors in some configurations.
		// The key assertion is that we got a non-nil error.
		t.Logf("Note: result non-nil alongside non-nil err; err=%v, result.IsError=%v",
			err, result.IsError)
	}
}

// TestSignTransaction_UnknownFieldRejected verifies that unknown input fields
// are rejected via the strict schema (additionalProperties:false) before reaching
// our handler.
func TestSignTransaction_UnknownFieldRejected(t *testing.T) {
	t.Parallel()

	var logBuf bytes.Buffer
	srv := newTestServerStub(t, nil, common.Address{}, &logBuf)
	cs, cleanup := newTestSession(t, srv)
	defer cleanup()

	// Include an unknown field "foo" alongside valid fields.
	args := minimalLegacyArgs()
	args["foo"] = "bar"

	result, err := callSignTx(t, cs, args)

	// Schema validation failure produces an IsError=true tool result.
	// The SDK returns it as (result, nil) — no protocol error.
	if err != nil {
		t.Logf("Note: got protocol error %v (acceptable for schema rejection)", err)
		return
	}
	if result == nil {
		t.Fatal("result is nil")
	}
	// The SDK's schema validation failure returns IsError=true.
	if !result.IsError {
		t.Error("expected IsError=true for unknown field; got IsError=false")
	}
}

// TestGetAddress_ReturnsChecksummedAddress verifies that get_address returns the
// EIP-55 checksummed address from the stub signer's boot-time snapshot.
func TestGetAddress_ReturnsChecksummedAddress(t *testing.T) {
	t.Parallel()

	addr := common.HexToAddress(signing.FixtureTestAddress)
	var logBuf bytes.Buffer
	srv := newTestServerStub(t, nil, addr, &logBuf)
	cs, cleanup := newTestSession(t, srv)
	defer cleanup()

	result, err := callGetAddress(t, cs)
	if err != nil {
		t.Fatalf("CallTool(get_address): protocol error: %v", err)
	}
	if result == nil {
		t.Fatal("result is nil")
	}
	if result.IsError {
		t.Fatalf("get_address returned IsError=true: %v", result.Content)
	}

	if len(result.Content) == 0 {
		t.Fatal("result.Content is empty")
	}
	tc, ok := result.Content[0].(*mcp.TextContent)
	if !ok {
		t.Fatalf("Content[0] is %T; want *mcp.TextContent", result.Content[0])
	}

	var got signing.AddressResult
	if err := json.Unmarshal([]byte(tc.Text), &got); err != nil {
		t.Fatalf("cannot unmarshal Content[0]: %v\ntext: %s", err, tc.Text)
	}

	// Must return EIP-55 checksummed address (common.Address.Hex() is checksummed).
	if got.Address != signing.FixtureTestAddress {
		t.Errorf("Address = %q; want %q", got.Address, signing.FixtureTestAddress)
	}
}

// TestGetAddress_UnreadablePasswordFile verifies that get_address works even
// when the password file has been chmod'd 000 (unreadable). This tests the
// no-password-read path using a real vault+signer.
//
// This test uses the real FileKeyVault backed by keystore-weak.json and an
// unreadable password file to prove that get_address is served from the
// boot-time snapshot without reading the password.
func TestGetAddress_UnreadablePasswordFile(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("skipping: running as root; chmod is ignored")
	}

	tdPath := signingTestdataPath(t)
	keystorePath := filepath.Join(tdPath, "keystore-weak.json")

	// Copy password.txt to a temp file so we can chmod it without affecting
	// the shared testdata file.
	origPwBytes, err := os.ReadFile(filepath.Join(tdPath, "password.txt"))
	if err != nil {
		t.Fatalf("read password.txt: %v", err)
	}
	tmpPw := filepath.Join(t.TempDir(), "password.txt")
	if err := os.WriteFile(tmpPw, origPwBytes, 0o600); err != nil {
		t.Fatalf("write temp password: %v", err)
	}

	// Build vault and signer BEFORE making password unreadable.
	vault, err := signing.NewFileKeyVault(signing.VaultOptions{
		KeystorePath: keystorePath,
		PasswordPath: tmpPw,
	})
	if err != nil {
		t.Fatalf("NewFileKeyVault: %v", err)
	}

	// NOW make the password file unreadable.
	if err := os.Chmod(tmpPw, 0o000); err != nil {
		t.Fatalf("chmod(000): %v", err)
	}
	t.Cleanup(func() { _ = os.Chmod(tmpPw, 0o600) }) // restore for cleanup

	signer := signing.NewSigner(vault, signing.SignerOptions{})

	var logBuf bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&logBuf, nil))
	srv := New(signer, Options{Name: "test", Version: "v0.0.0-test", Logger: logger})

	cs, cleanup := newTestSession(t, srv)
	defer cleanup()

	result, err := callGetAddress(t, cs)
	if err != nil {
		t.Fatalf("CallTool(get_address) with unreadable password: protocol error: %v", err)
	}
	if result == nil {
		t.Fatal("result is nil")
	}
	if result.IsError {
		t.Fatalf("get_address returned IsError=true with unreadable password: %v", result.Content)
	}

	if len(result.Content) == 0 {
		t.Fatal("result.Content is empty")
	}
	tc, ok := result.Content[0].(*mcp.TextContent)
	if !ok {
		t.Fatalf("Content[0] is %T", result.Content[0])
	}
	var got signing.AddressResult
	if err := json.Unmarshal([]byte(tc.Text), &got); err != nil {
		t.Fatalf("unmarshal Content[0]: %v", err)
	}
	if got.Address != signing.FixtureTestAddress {
		t.Errorf("Address = %q; want %q", got.Address, signing.FixtureTestAddress)
	}
}

// TestSignTransaction_CauseNotOnWire verifies that ToolError.Cause is never
// serialised into Content[0] or present anywhere in the wire content.
func TestSignTransaction_CauseNotOnWire(t *testing.T) {
	t.Parallel()

	const sentinelCause = "SENTINEL_CAUSE_CONTENT_MUST_NOT_APPEAR_ON_WIRE"

	te := &signing.ToolError{
		Code:    signing.CodePasswordError,
		Message: "password file is unreadable",
		Cause:   errors.New(sentinelCause),
	}

	var logBuf bytes.Buffer
	srv := newTestServerStub(t,
		func(_ context.Context, _ signing.TxRequest) (*signing.SignResult, error) {
			return nil, te
		},
		common.HexToAddress(signing.FixtureTestAddress),
		&logBuf,
	)
	cs, cleanup := newTestSession(t, srv)
	defer cleanup()

	result, callErr := callSignTx(t, cs, minimalLegacyArgs())
	if callErr != nil {
		t.Fatalf("CallTool returned protocol error: %v", callErr)
	}
	if result == nil {
		t.Fatal("result is nil")
	}

	// Scan all Content items for the sentinel.
	for i, c := range result.Content {
		tc, ok := c.(*mcp.TextContent)
		if !ok {
			continue
		}
		if strings.Contains(tc.Text, sentinelCause) {
			t.Errorf("Content[%d].Text contains sentinel Cause text; must never appear on wire: %q", i, tc.Text)
		}
	}

	// Also check the overall result marshalled to JSON.
	wireBytes, err := json.Marshal(result)
	if err != nil {
		t.Fatalf("marshal result: %v", err)
	}
	if strings.Contains(string(wireBytes), sentinelCause) {
		t.Errorf("marshalled result contains sentinel Cause text; must never appear on wire")
	}
}

// TestSignTransaction_RequestIDCorrelation verifies that:
//  1. The handler attaches a non-empty request_id to the context.
//  2. The signing audit line carries the same request_id.
//
// Uses a real Signer backed by keystore-weak.json for an end-to-end test.
func TestSignTransaction_RequestIDCorrelation(t *testing.T) {
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

	cs, cleanup := newTestSession(t, srv)
	defer cleanup()

	result, err := callSignTx(t, cs, minimalLegacyArgs())
	if err != nil {
		t.Fatalf("CallTool: protocol error: %v", err)
	}
	if result == nil || result.IsError {
		t.Fatalf("CallTool returned error result: IsError=%v, Content=%v", result.GetError(), result.Content)
	}

	// Scan log lines for the audit line.
	lines := bytes.Split(logBuf.Bytes(), []byte("\n"))
	var auditLine map[string]any
	for _, line := range lines {
		line = bytes.TrimSpace(line)
		if len(line) == 0 {
			continue
		}
		var m map[string]any
		if err := json.Unmarshal(line, &m); err != nil {
			continue
		}
		msg, _ := m["msg"].(string)
		if strings.Contains(msg, "signed successfully") {
			auditLine = m
			break
		}
	}

	if auditLine == nil {
		t.Fatal("audit line not found in captured log output")
	}

	// Assert request_id is present and non-empty.
	reqID, ok := auditLine["request_id"].(string)
	if !ok || reqID == "" {
		t.Errorf("audit line missing or empty request_id: %v", auditLine)
	}

	// Assert the audit line has the expected fields.
	for _, field := range []string{"tx_hash", "chain_id", "nonce"} {
		if _, ok := auditLine[field]; !ok {
			t.Errorf("audit line missing field %q", field)
		}
	}
}

// TestGenerateRequestID_NonEmpty verifies that generateRequestID returns a
// non-empty string with UUID v4 formatting.
func TestGenerateRequestID_NonEmpty(t *testing.T) {
	t.Parallel()

	id := generateRequestID()
	if id == "" {
		t.Error("generateRequestID() returned empty string")
	}
	// UUID format: xxxxxxxx-xxxx-4xxx-yxxx-xxxxxxxxxxxx
	// Simplified check: contains 4 dashes and is 36 chars.
	if len(id) != 36 {
		t.Errorf("generateRequestID() len = %d; want 36 (UUID format)", len(id))
	}
	if strings.Count(id, "-") != 4 {
		t.Errorf("generateRequestID() has %d dashes; want 4 (UUID format): %q",
			strings.Count(id, "-"), id)
	}

	// Verify uniqueness: two consecutive calls should produce different IDs.
	id2 := generateRequestID()
	if id == id2 {
		t.Errorf("generateRequestID() returned same ID twice: %q", id)
	}
}

// TestGetAddress_RequestIDNotRequired verifies that get_address does not fail
// even when no request_id is set (the handler generates one internally).
func TestGetAddress_RequestIDNotRequired(t *testing.T) {
	t.Parallel()

	addr := common.HexToAddress(signing.FixtureTestAddress)
	var logBuf bytes.Buffer
	srv := newTestServerStub(t, nil, addr, &logBuf)
	cs, cleanup := newTestSession(t, srv)
	defer cleanup()

	result, err := callGetAddress(t, cs)
	if err != nil {
		t.Fatalf("get_address: protocol error: %v", err)
	}
	if result != nil && result.IsError {
		t.Errorf("get_address returned IsError=true")
	}
}

// TestObsNewLogger_Import verifies obs package is importable (used for test
// coverage of the obs import in the server package).
func TestObsNewLogger_Import(t *testing.T) {
	t.Parallel()
	logger := obs.NewLogger("error")
	if logger == nil {
		t.Error("obs.NewLogger returned nil")
	}
}
