// Tests for Signer orchestration — Issue 2.6.
// Internal tests (package signing).
package signing

import (
	"bytes"
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"math/big"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
)

// ── Test helpers ──────────────────────────────────────────────────────────────

// newTestSigner creates a Signer backed by the weak (n=2) fixture vault and a
// buf-backed slog.Logger. Returns the signer, the log buffer, and the vault address.
func newTestSigner(t *testing.T) (*Signer, *bytes.Buffer, common.Address) {
	t.Helper()
	vault, err := NewFileKeyVault(VaultOptions{
		KeystorePath: testdataFile(t, "keystore-weak.json"),
		PasswordPath: testdataFile(t, "password.txt"),
	})
	if err != nil {
		t.Fatalf("NewFileKeyVault: %v", err)
	}
	var logBuf bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&logBuf, &slog.HandlerOptions{Level: slog.LevelDebug}))
	s := NewSigner(vault, SignerOptions{Logger: logger})
	return s, &logBuf, vault.Address()
}

// legacyReq returns a minimal valid legacy (type 0) TxRequest for testing.
func legacyReq() TxRequest {
	return TxRequest{
		Type:     "legacy",
		ChainID:  "1",
		Nonce:    "0",
		To:       "0x9858EfFD232B4033E47d90003D41EC34EcaEda94",
		Value:    "1000000000000000000",
		Data:     "0x",
		Gas:      "21000",
		GasPrice: "20000000000",
	}
}

// eip1559Req returns a minimal valid EIP-1559 (type 2) TxRequest for testing.
func eip1559Req() TxRequest {
	return TxRequest{
		Type:                 "eip1559",
		ChainID:              "1",
		Nonce:                "0",
		To:                   "0x9858EfFD232B4033E47d90003D41EC34EcaEda94",
		Value:                "1000000000000000000",
		Data:                 "0x",
		Gas:                  "21000",
		MaxFeePerGas:         "40000000000",
		MaxPriorityFeePerGas: "2000000000",
	}
}

// testWriteFile writes content to a temp file and returns its path.
func testWriteFile(t *testing.T, name string, content []byte, perm os.FileMode) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(p, content, perm); err != nil {
		t.Fatalf("testWriteFile(%q): %v", name, err)
	}
	return p
}

// parseBigHex parses a 0x-prefixed hex string as a big.Int. Returns nil on error.
func parseBigHex(s string) *big.Int {
	s = strings.TrimPrefix(s, "0x")
	s = strings.TrimPrefix(s, "0X")
	n, ok := new(big.Int).SetString(s, 16)
	if !ok {
		return nil
	}
	return n
}

// decodeHex decodes a 0x-prefixed hex string to bytes.
func decodeHex(s string) ([]byte, error) {
	s = strings.TrimPrefix(s, "0x")
	s = strings.TrimPrefix(s, "0X")
	return hex.DecodeString(s)
}

// ── Happy-path tests ──────────────────────────────────────────────────────────

// TestSigner_HappyPathLegacy verifies the full orchestration for a legacy (type 0)
// transaction: SignResult has all required fields populated and V is EIP-155 form.
func TestSigner_HappyPathLegacy(t *testing.T) {
	t.Parallel()
	s, _, vaultAddr := newTestSigner(t)

	result, err := s.SignTransaction(context.Background(), legacyReq())
	if err != nil {
		t.Fatalf("SignTransaction: %v", err)
	}

	if result.RawTransaction == "" || !strings.HasPrefix(result.RawTransaction, "0x") {
		t.Errorf("RawTransaction = %q; want non-empty 0x-prefixed hex", result.RawTransaction)
	}
	if result.Hash == "" || !strings.HasPrefix(result.Hash, "0x") {
		t.Errorf("Hash = %q; want non-empty 0x-prefixed hash", result.Hash)
	}
	if result.From != vaultAddr.Hex() {
		t.Errorf("From = %q, want %q", result.From, vaultAddr.Hex())
	}
	if result.Signature.R == "" || result.Signature.S == "" || result.Signature.V == "" {
		t.Errorf("Signature components missing: r=%q s=%q v=%q",
			result.Signature.R, result.Signature.S, result.Signature.V)
	}

	// Verify round-trip: RLP → UnmarshalBinary → same hash.
	rawBytes, decErr := decodeHex(result.RawTransaction)
	if decErr != nil {
		t.Fatalf("decodeHex(RawTransaction): %v", decErr)
	}
	var rt types.Transaction
	if unmarshalErr := rt.UnmarshalBinary(rawBytes); unmarshalErr != nil {
		t.Fatalf("UnmarshalBinary: %v", unmarshalErr)
	}
	if rt.Hash().Hex() != result.Hash {
		t.Errorf("round-trip hash = %q, want %q", rt.Hash().Hex(), result.Hash)
	}

	// For legacy (type 0) + chainID=1: V must be 37 or 38 (EIP-155: chainID*2+35/36).
	bigV := parseBigHex(result.Signature.V)
	if bigV == nil {
		t.Fatalf("cannot parse V = %q as big.Int", result.Signature.V)
	}
	v := bigV.Int64()
	if v != 37 && v != 38 {
		t.Errorf("legacy V = %d; want 37 or 38 (EIP-155 for chainID=1)", v)
	}

	// Independent sender recovery.
	signer := types.LatestSignerForChainID(rt.ChainId())
	sender, senderErr := types.Sender(signer, &rt)
	if senderErr != nil {
		t.Fatalf("types.Sender: %v", senderErr)
	}
	if sender.Hex() != vaultAddr.Hex() {
		t.Errorf("recovered sender = %q, want %q", sender.Hex(), vaultAddr.Hex())
	}
}

// TestSigner_HappyPathEIP1559 verifies the full orchestration for a type 2
// transaction: V must be yParity (0 or 1).
func TestSigner_HappyPathEIP1559(t *testing.T) {
	t.Parallel()
	s, _, vaultAddr := newTestSigner(t)

	result, err := s.SignTransaction(context.Background(), eip1559Req())
	if err != nil {
		t.Fatalf("SignTransaction: %v", err)
	}

	if result.RawTransaction == "" || !strings.HasPrefix(result.RawTransaction, "0x") {
		t.Errorf("RawTransaction = %q; want non-empty 0x-prefixed hex", result.RawTransaction)
	}
	if result.From != vaultAddr.Hex() {
		t.Errorf("From = %q, want %q", result.From, vaultAddr.Hex())
	}

	rawBytes, decErr := decodeHex(result.RawTransaction)
	if decErr != nil {
		t.Fatalf("decodeHex(RawTransaction): %v", decErr)
	}
	var rt types.Transaction
	if unmarshalErr := rt.UnmarshalBinary(rawBytes); unmarshalErr != nil {
		t.Fatalf("UnmarshalBinary: %v", unmarshalErr)
	}
	if rt.Hash().Hex() != result.Hash {
		t.Errorf("round-trip hash = %q, want %q", rt.Hash().Hex(), result.Hash)
	}
	if rt.Type() != 2 {
		t.Errorf("tx type = %d, want 2", rt.Type())
	}

	// For type 2, V must be yParity: 0 or 1.
	bigV := parseBigHex(result.Signature.V)
	if bigV == nil {
		t.Fatalf("cannot parse V = %q", result.Signature.V)
	}
	if v := bigV.Int64(); v != 0 && v != 1 {
		t.Errorf("EIP-1559 V = %d; want 0 or 1 (yParity)", v)
	}

	// Independent sender recovery.
	signer := types.LatestSignerForChainID(rt.ChainId())
	sender, senderErr := types.Sender(signer, &rt)
	if senderErr != nil {
		t.Fatalf("types.Sender: %v", senderErr)
	}
	if sender.Hex() != vaultAddr.Hex() {
		t.Errorf("recovered sender = %q, want %q", sender.Hex(), vaultAddr.Hex())
	}
}

// TestSigner_Address verifies that Address() returns the boot-time cached address.
func TestSigner_Address(t *testing.T) {
	t.Parallel()
	s, _, _ := newTestSigner(t)
	if got := s.Address().Hex(); got != FixtureTestAddress {
		t.Errorf("Address() = %q, want %q", got, FixtureTestAddress)
	}
}

// ── Chain-id guard tests ──────────────────────────────────────────────────────

// TestSigner_GuardMismatch verifies that a chain-id guard set in NewSigner
// returns chain_id_mismatch when the request carries a different chainId.
func TestSigner_GuardMismatch(t *testing.T) {
	t.Parallel()
	vault, err := NewFileKeyVault(VaultOptions{
		KeystorePath: testdataFile(t, "keystore-weak.json"),
		PasswordPath: testdataFile(t, "password.txt"),
	})
	if err != nil {
		t.Fatalf("NewFileKeyVault: %v", err)
	}

	guardVal := uint64(5) // guard = chain 5; request uses chain 1
	s := NewSigner(vault, SignerOptions{ChainIDGuard: &guardVal})

	_, signErr := s.SignTransaction(context.Background(), legacyReq()) // chainId: "1"
	if signErr == nil {
		t.Fatal("expected chain_id_mismatch error, got nil")
	}
	var te *ToolError
	if !errors.As(signErr, &te) {
		t.Fatalf("error type = %T (%v), want *ToolError", signErr, signErr)
	}
	if te.Code != CodeChainIDMismatch {
		t.Errorf("Code = %q, want %q", te.Code, CodeChainIDMismatch)
	}
}

// ── Vault-never-touched tests (panicking fake vault) ─────────────────────────

// panicKeyVault is a KeyVault whose WithSigningKey panics if called.
// Used to assert that validation failures never invoke the vault.
type panicKeyVault struct {
	addr common.Address
}

func (p *panicKeyVault) Address() common.Address { return p.addr }
func (p *panicKeyVault) WithSigningKey(_ context.Context, _ func(SigningKey) error) error {
	panic("panicKeyVault.WithSigningKey called — validation must prevent this")
}

// newSignerWithPanicVault returns a Signer backed by panicKeyVault.
func newSignerWithPanicVault(t *testing.T) *Signer {
	t.Helper()
	pv := &panicKeyVault{addr: common.HexToAddress(FixtureTestAddress)}
	return NewSigner(pv, SignerOptions{})
}

// TestSigner_VaultNeverTouchedOnInvalidInput verifies that for every invalid_input
// failure class, the vault is never invoked (panicKeyVault would panic if called).
func TestSigner_VaultNeverTouchedOnInvalidInput(t *testing.T) {
	t.Parallel()
	s := newSignerWithPanicVault(t)

	cases := []struct {
		name string
		req  TxRequest
	}{
		{"missing_chainId", TxRequest{Type: "legacy", Nonce: "0", Gas: "21000", Value: "0", Data: "0x", GasPrice: "1"}},
		{"zero_chainId", TxRequest{Type: "legacy", ChainID: "0", Nonce: "0", Gas: "21000", Value: "0", Data: "0x", GasPrice: "1"}},
		{"missing_nonce", TxRequest{Type: "legacy", ChainID: "1", Gas: "21000", Value: "0", Data: "0x", GasPrice: "1"}},
		{"missing_gas", TxRequest{Type: "legacy", ChainID: "1", Nonce: "0", Value: "0", Data: "0x", GasPrice: "1"}},
		{"missing_value", TxRequest{Type: "legacy", ChainID: "1", Nonce: "0", Gas: "21000", Data: "0x", GasPrice: "1"}},
		{"missing_gasPrice_legacy", TxRequest{Type: "legacy", ChainID: "1", Nonce: "0", Gas: "21000", Value: "0", Data: "0x"}},
		{"bad_address", TxRequest{Type: "legacy", ChainID: "1", Nonce: "0", Gas: "21000", Value: "0", Data: "0x", GasPrice: "1", To: "0xBADADDRESS"}},
		{"data_too_large", TxRequest{Type: "legacy", ChainID: "1", Nonce: "0", Gas: "21000", Value: "0",
			Data:     "0x" + strings.Repeat("ff", 262145),
			GasPrice: "1"}},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			// Should not panic — panicKeyVault is never called on validation failures.
			_, signErr := s.SignTransaction(context.Background(), tc.req)
			if signErr == nil {
				t.Fatalf("expected error for %q, got nil", tc.name)
			}
			var te *ToolError
			if !errors.As(signErr, &te) {
				t.Fatalf("error type = %T (%v), want *ToolError", signErr, signErr)
			}
			if te.Code != CodeInvalidInput {
				t.Errorf("Code = %q, want %q", te.Code, CodeInvalidInput)
			}
		})
	}
}

// TestSigner_VaultNeverTouchedOnUnsupportedType verifies that an unsupported
// transaction type returns unsupported_type without invoking the vault.
func TestSigner_VaultNeverTouchedOnUnsupportedType(t *testing.T) {
	t.Parallel()
	s := newSignerWithPanicVault(t)

	req := TxRequest{
		Type:    "0x1", // type 1 is not supported in v1
		ChainID: "1",
		Nonce:   "0",
		Gas:     "21000",
		Value:   "0",
		Data:    "0x",
	}
	_, signErr := s.SignTransaction(context.Background(), req)
	if signErr == nil {
		t.Fatal("expected unsupported_type error, got nil")
	}
	var te *ToolError
	if !errors.As(signErr, &te) {
		t.Fatalf("error type = %T, want *ToolError", signErr)
	}
	if te.Code != CodeUnsupportedType {
		t.Errorf("Code = %q, want %q", te.Code, CodeUnsupportedType)
	}
}

// TestSigner_VaultNeverTouchedOnChainIDMismatch verifies that a guard mismatch
// returns chain_id_mismatch without invoking the vault.
func TestSigner_VaultNeverTouchedOnChainIDMismatch(t *testing.T) {
	t.Parallel()
	pv := &panicKeyVault{addr: common.HexToAddress(FixtureTestAddress)}
	guardVal := uint64(5)
	s := NewSigner(pv, SignerOptions{ChainIDGuard: &guardVal})

	_, signErr := s.SignTransaction(context.Background(), legacyReq()) // chainId: "1"
	if signErr == nil {
		t.Fatal("expected chain_id_mismatch error, got nil")
	}
	var te *ToolError
	if !errors.As(signErr, &te) {
		t.Fatalf("error type = %T, want *ToolError", signErr)
	}
	if te.Code != CodeChainIDMismatch {
		t.Errorf("Code = %q, want %q", te.Code, CodeChainIDMismatch)
	}
}

// ── Wrong-password test ───────────────────────────────────────────────────────

// TestSigner_WrongPassword verifies that a wrong password returns password_error
// and the Cause is never leaked to logs.
func TestSigner_WrongPassword(t *testing.T) {
	t.Parallel()

	wrongPwFile := testWriteFile(t, "wrong-pw.txt", []byte("definitely-wrong\n"), 0o600)
	vault, err := NewFileKeyVault(VaultOptions{
		KeystorePath: testdataFile(t, "keystore-weak.json"),
		PasswordPath: wrongPwFile,
	})
	if err != nil {
		t.Fatalf("NewFileKeyVault: %v", err)
	}

	var logBuf bytes.Buffer
	// Use Debug level so a future Debug-level log of Cause/key material would be caught.
	logger := slog.New(slog.NewJSONHandler(&logBuf, &slog.HandlerOptions{Level: slog.LevelDebug}))
	s := NewSigner(vault, SignerOptions{Logger: logger})

	_, signErr := s.SignTransaction(context.Background(), legacyReq())
	if signErr == nil {
		t.Fatal("expected password_error, got nil")
	}
	var te *ToolError
	if !errors.As(signErr, &te) {
		t.Fatalf("error type = %T, want *ToolError", signErr)
	}
	if te.Code != CodePasswordError {
		t.Errorf("Code = %q, want %q", te.Code, CodePasswordError)
	}

	// Cause must not appear in any captured log output.
	sentinel := FixtureKeySentinel()
	if leaked := sentinel.Scan(logBuf.Bytes()); len(leaked) > 0 {
		t.Errorf("key sentinel leaked in log output form(s): %v", leaked)
	}
}

// ── Sender-mismatch test ──────────────────────────────────────────────────────

// mismatchedAddressVault wraps a real vault but overrides Address() to return a
// different address, simulating the sender-mismatch scenario.
type mismatchedAddressVault struct {
	inner       KeyVault
	claimedAddr common.Address
}

func (w *mismatchedAddressVault) Address() common.Address { return w.claimedAddr }
func (w *mismatchedAddressVault) WithSigningKey(ctx context.Context, fn func(SigningKey) error) error {
	return w.inner.WithSigningKey(ctx, fn)
}

// TestSigner_SenderMismatch verifies that a vault whose Address() differs from the
// recovered sender after signing returns internal_error whose message names BOTH
// the cached (claimed) address and the recovered address.
func TestSigner_SenderMismatch(t *testing.T) {
	t.Parallel()

	realVault, err := NewFileKeyVault(VaultOptions{
		KeystorePath: testdataFile(t, "keystore-weak.json"),
		PasswordPath: testdataFile(t, "password.txt"),
	})
	if err != nil {
		t.Fatalf("NewFileKeyVault: %v", err)
	}

	// wrongAddr != real signer address, simulating a key rotation / config mismatch.
	wrongAddr := common.HexToAddress("0x1111111111111111111111111111111111111111")
	wv := &mismatchedAddressVault{inner: realVault, claimedAddr: wrongAddr}

	var logBuf bytes.Buffer
	// Use Debug level so a future Debug-level log of Cause/addresses would be caught.
	logger := slog.New(slog.NewJSONHandler(&logBuf, &slog.HandlerOptions{Level: slog.LevelDebug}))
	s := NewSigner(wv, SignerOptions{Logger: logger})

	_, signErr := s.SignTransaction(context.Background(), legacyReq())
	if signErr == nil {
		t.Fatal("expected internal_error for sender mismatch, got nil")
	}
	var te *ToolError
	if !errors.As(signErr, &te) {
		t.Fatalf("error type = %T, want *ToolError", signErr)
	}
	if te.Code != CodeInternalError {
		t.Errorf("Code = %q, want %q", te.Code, CodeInternalError)
	}

	// Message must name BOTH: the claimed (cached) address and the recovered address.
	msg := te.Message
	if !strings.Contains(msg, wrongAddr.Hex()) {
		t.Errorf("Message does not contain claimed address %q: %q", wrongAddr.Hex(), msg)
	}
	// Recovered sender is the real fixture address.
	if !strings.Contains(msg, FixtureTestAddress) {
		t.Errorf("Message does not contain recovered address %q: %q", FixtureTestAddress, msg)
	}
}

// ── Panic recovery tests ──────────────────────────────────────────────────────

// panicInFnVault wraps a real vault and injects a panic inside the signing callback
// on the first call, then behaves normally thereafter (server-keeps-serving proof).
type panicInFnVault struct {
	inner     KeyVault
	panicOnce bool
	panicVal  any
}

func (p *panicInFnVault) Address() common.Address { return p.inner.Address() }
func (p *panicInFnVault) WithSigningKey(ctx context.Context, fn func(SigningKey) error) error {
	if p.panicOnce {
		p.panicOnce = false
		// Delegate to the inner vault (triggering real decrypt + deferred zeroing),
		// then panic inside fn to verify the zeroing runs before the panic propagates.
		return p.inner.WithSigningKey(ctx, func(_ SigningKey) error {
			panic(p.panicVal)
		})
	}
	return p.inner.WithSigningKey(ctx, fn)
}

// TestSigner_PanicRecovery verifies:
//  1. A panic inside the signing path is caught by SignTransaction's defer/recover.
//  2. The returned error is CodeInternalError.
//  3. The logged line does NOT contain the panic value (redacted).
//  4. A subsequent SignTransaction on the same Signer succeeds (server-keeps-serving).
func TestSigner_PanicRecovery(t *testing.T) {
	t.Parallel()

	realVault, err := NewFileKeyVault(VaultOptions{
		KeystorePath: testdataFile(t, "keystore-weak.json"),
		PasswordPath: testdataFile(t, "password.txt"),
	})
	if err != nil {
		t.Fatalf("NewFileKeyVault: %v", err)
	}

	// Use a distinctive sentinel string that MUST NOT appear in logs.
	panicSentinel := "PANIC-SENTINEL-MUST-NOT-APPEAR-IN-LOGS-XYZZY"
	pv := &panicInFnVault{
		inner:     realVault,
		panicOnce: true,
		panicVal:  fmt.Sprintf("test panic carrying sentinel: %s", panicSentinel),
	}

	var logBuf bytes.Buffer
	// Use Debug level so a future Debug-level log of the panic value would be caught.
	logger := slog.New(slog.NewJSONHandler(&logBuf, &slog.HandlerOptions{Level: slog.LevelDebug}))
	s := NewSigner(pv, SignerOptions{Logger: logger})

	// First call: triggers panic, must recover and return internal_error.
	_, signErr := s.SignTransaction(context.Background(), legacyReq())
	if signErr == nil {
		t.Fatal("expected error from panic recovery, got nil")
	}
	var te *ToolError
	if !errors.As(signErr, &te) {
		t.Fatalf("error type = %T, want *ToolError", signErr)
	}
	if te.Code != CodeInternalError {
		t.Errorf("Code = %q, want %q", te.Code, CodeInternalError)
	}

	// Panic value (with sentinel) MUST NOT appear in logs.
	if bytes.Contains(logBuf.Bytes(), []byte(panicSentinel)) {
		t.Errorf("panic sentinel appeared in logs — redaction failed")
	}

	// Second call: Signer must keep serving normally.
	result, err2 := s.SignTransaction(context.Background(), legacyReq())
	if err2 != nil {
		t.Fatalf("second SignTransaction after panic: %v", err2)
	}
	if result == nil || result.RawTransaction == "" {
		t.Error("second SignTransaction: expected valid result after panic recovery")
	}
}

// ── Audit line tests ──────────────────────────────────────────────────────────

// TestSigner_AuditLine verifies that exactly one info-level audit log line is
// emitted per successful signing, containing request_id, tx_hash, chain_id, nonce
// — and that to, value, and calldata are NEVER logged.
//
// chain_id is emitted as a numeric uint64 (e.g. the JSON value 1, not "1" or "0x1")
// for better log aggregation in downstream log tools.
func TestSigner_AuditLine(t *testing.T) {
	t.Parallel()
	s, logBuf, _ := newTestSigner(t)

	// Distinctive values that must NOT appear in logs.
	// Using an address distinct from the signer's own address to catch leaks.
	distinctiveTo := "0x9858EfFD232B4033E47d90003D41EC34EcaEda94"
	distinctiveValue := "7654321000000000000" // 7.65 ETH in wei
	distinctiveDataContent := "c0ffee"        // hex content inside data field
	req := TxRequest{
		Type:     "legacy",
		ChainID:  "1",
		Nonce:    "42",
		To:       distinctiveTo,
		Value:    distinctiveValue,
		Data:     "0x" + distinctiveDataContent,
		Gas:      "21000",
		GasPrice: "20000000000",
	}
	reqID := "test-audit-request-id-9876"
	ctx := WithRequestID(context.Background(), reqID)

	result, err := s.SignTransaction(ctx, req)
	if err != nil {
		t.Fatalf("SignTransaction: %v", err)
	}

	logOutput := logBuf.Bytes()

	// tx_hash must appear exactly once.
	txHashCount := bytes.Count(logOutput, []byte(result.Hash))
	if txHashCount != 1 {
		t.Errorf("tx_hash appears %d times in logs, want exactly 1", txHashCount)
	}

	// Required audit fields: request_id, tx_hash, chain_id, nonce.
	for _, required := range []string{reqID, `"chain_id"`, `"nonce"`, `"tx_hash"`, `"request_id"`} {
		if !bytes.Contains(logOutput, []byte(required)) {
			t.Errorf("audit line missing field/value %q in: %s", required, logOutput)
		}
	}

	// chain_id must be a numeric value in the JSON log (uint64, not a string).
	// For chainId=1, the JSON must contain "chain_id":1 (numeric), not "chain_id":"1".
	if !bytes.Contains(logOutput, []byte(`"chain_id":1`)) {
		t.Errorf("audit line chain_id is not numeric uint64 in JSON output: %s", logOutput)
	}

	// Tx body MUST NOT be logged at any level: to, value, calldata.
	// distinctiveTo is the to-address used above; assert it does not appear in logs.
	if bytes.Contains(logOutput, []byte(distinctiveTo)) {
		t.Errorf("'to' field (%q) leaked into logs", distinctiveTo)
	}
	if bytes.Contains(logOutput, []byte(distinctiveValue)) {
		t.Errorf("'value' field (%q) leaked into logs", distinctiveValue)
	}
	if bytes.Contains(logOutput, []byte(distinctiveDataContent)) {
		t.Errorf("'data' hex content (%q) leaked into logs", distinctiveDataContent)
	}

	// Encoded-forms leak scan.
	sentinel := FixtureKeySentinel()
	if leaked := sentinel.Scan(logOutput); len(leaked) > 0 {
		t.Errorf("key sentinel leaked in audit logs form(s): %v", leaked)
	}
}

// TestSigner_NilVaultPanics verifies that NewSigner panics when passed a nil vault,
// making wiring errors diagnosable at construction time rather than at signing time.
func TestSigner_NilVaultPanics(t *testing.T) {
	t.Parallel()
	defer func() {
		if r := recover(); r == nil {
			t.Error("NewSigner(nil vault): expected panic, got none")
		}
	}()
	_ = NewSigner(nil, SignerOptions{})
}

// TestSigner_NoAuditLineOnValidationError verifies that no log is emitted for a
// validation failure (no signing occurred).
func TestSigner_NoAuditLineOnValidationError(t *testing.T) {
	t.Parallel()
	s, logBuf, _ := newTestSigner(t)

	req := legacyReq()
	req.ChainID = "0" // fails validation

	_, err := s.SignTransaction(context.Background(), req)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if logBuf.Len() > 0 {
		t.Errorf("unexpected log output on validation failure: %s", logBuf.String())
	}
}

// ── Context-cancellation test ─────────────────────────────────────────────────

// TestSigner_CtxCancelled verifies that a pre-cancelled context propagates
// ctx.Err() as a system error (not a ToolError).
func TestSigner_CtxCancelled(t *testing.T) {
	t.Parallel()
	s, _, _ := newTestSigner(t)

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // pre-cancel

	_, signErr := s.SignTransaction(ctx, legacyReq())
	if signErr == nil {
		t.Fatal("expected error from cancelled ctx, got nil")
	}
	if !errors.Is(signErr, context.Canceled) {
		t.Errorf("error = %T(%v), want context.Canceled", signErr, signErr)
	}
}

// TestSigner_NilLogger verifies that a nil logger does not cause a panic.
func TestSigner_NilLogger(t *testing.T) {
	t.Parallel()
	vault, err := NewFileKeyVault(VaultOptions{
		KeystorePath: testdataFile(t, "keystore-weak.json"),
		PasswordPath: testdataFile(t, "password.txt"),
	})
	if err != nil {
		t.Fatalf("NewFileKeyVault: %v", err)
	}
	// Logger is nil — should not panic.
	s := NewSigner(vault, SignerOptions{})
	result, signErr := s.SignTransaction(context.Background(), legacyReq())
	if signErr != nil {
		t.Fatalf("SignTransaction (nil logger): %v", signErr)
	}
	if result == nil || result.RawTransaction == "" {
		t.Error("expected valid result with nil logger")
	}
}
