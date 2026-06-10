// Tests for build.go — Issue 2.5.
// Internal tests (package signing) so they can access unexported types
// (buildTx, parsedTx) directly.
package signing

import (
	"bytes"
	"crypto/ecdsa"
	"math/big"
	"testing"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/crypto"
)

// ─── construction helpers ─────────────────────────────────────────────────────

// makeParsedLegacy returns a minimal valid parsedTx for a legacy (type 0) tx.
func makeParsedLegacy() *parsedTx {
	// Use a valid 42-character EIP-55 address (40 hex digits + "0x" prefix).
	// The previous literal "0xd3CdA913deB6f4967b2Ef3aa68f5A843B5C4B70" was only
	// 41 characters (39 hex digits); common.HexToAddress silently zero-padded it
	// to a different address.  validate.go correctly rejects non-42-char addresses,
	// but internal tests that bypass validate must use valid inputs.
	to := common.HexToAddress("0xd8dA6BF26964aF9D7eEd9e03E53415D37aA96045")
	return &parsedTx{
		txType:   0,
		chainID:  big.NewInt(1),
		nonce:    5,
		gas:      21000,
		to:       &to,
		value:    big.NewInt(1e18),
		data:     []byte{},
		gasPrice: big.NewInt(1e9),
	}
}

// makeParsed1559 returns a minimal valid parsedTx for an EIP-1559 (type 2) tx.
func makeParsed1559() *parsedTx {
	to := common.HexToAddress("0xd8dA6BF26964aF9D7eEd9e03E53415D37aA96045")
	return &parsedTx{
		txType:    2,
		chainID:   big.NewInt(1),
		nonce:     5,
		gas:       21000,
		to:        &to,
		value:     big.NewInt(1e18),
		data:      []byte{},
		gasTipCap: big.NewInt(1e9),
		gasFeeCap: big.NewInt(2e9),
	}
}

// throwawayKey generates a fresh ephemeral ECDSA key for signing in tests.
// The returned key has no persistent value; any signing results are only used
// to verify structural properties (type, field mapping, round-trips).
func throwawayKey(t *testing.T) *ecdsa.PrivateKey {
	t.Helper()
	key, err := crypto.GenerateKey()
	if err != nil {
		t.Fatalf("crypto.GenerateKey: %v", err)
	}
	return key
}

// signRoundTrip signs tx with key, marshals to bytes, and unmarshals into a
// fresh Transaction for round-trip value checks.
func signRoundTrip(t *testing.T, tx *types.Transaction, chainID *big.Int, key *ecdsa.PrivateKey) *types.Transaction {
	t.Helper()
	signer := types.LatestSignerForChainID(chainID)
	signed, err := types.SignTx(tx, signer, key)
	if err != nil {
		t.Fatalf("SignTx: %v", err)
	}
	raw, err := signed.MarshalBinary()
	if err != nil {
		t.Fatalf("MarshalBinary: %v", err)
	}
	rt := new(types.Transaction)
	if err := rt.UnmarshalBinary(raw); err != nil {
		t.Fatalf("UnmarshalBinary: %v", err)
	}
	return rt
}

// signBytes signs tx with key and returns the MarshalBinary bytes.
func signBytes(t *testing.T, tx *types.Transaction, signer types.Signer, key *ecdsa.PrivateKey) []byte {
	t.Helper()
	signed, err := types.SignTx(tx, signer, key)
	if err != nil {
		t.Fatalf("SignTx: %v", err)
	}
	raw, err := signed.MarshalBinary()
	if err != nil {
		t.Fatalf("MarshalBinary: %v", err)
	}
	return raw
}

// ─── Legacy (type 0) ─────────────────────────────────────────────────────────

func TestBuildTx_LegacyType(t *testing.T) {
	t.Parallel()
	tx, _ := buildTx(makeParsedLegacy())
	if tx.Type() != 0 {
		t.Fatalf("tx.Type() = %d, want 0", tx.Type())
	}
}

func TestBuildTx_LegacyFieldMapping(t *testing.T) {
	t.Parallel()
	p := makeParsedLegacy()
	tx, _ := buildTx(p)

	if tx.Nonce() != p.nonce {
		t.Errorf("Nonce: got %d, want %d", tx.Nonce(), p.nonce)
	}
	if tx.Gas() != p.gas {
		t.Errorf("Gas: got %d, want %d", tx.Gas(), p.gas)
	}
	if tx.GasPrice().Cmp(p.gasPrice) != 0 {
		t.Errorf("GasPrice: got %s, want %s", tx.GasPrice(), p.gasPrice)
	}
	if tx.To() == nil || *tx.To() != *p.to {
		t.Errorf("To: got %v, want %v", tx.To(), p.to)
	}
	if tx.Value().Cmp(p.value) != 0 {
		t.Errorf("Value: got %s, want %s", tx.Value(), p.value)
	}
	if !bytes.Equal(tx.Data(), p.data) {
		t.Errorf("Data: got %x, want %x", tx.Data(), p.data)
	}
}

func TestBuildTx_LegacySignerChainID(t *testing.T) {
	t.Parallel()
	p := makeParsedLegacy()
	_, signer := buildTx(p)
	if signer.ChainID().Cmp(p.chainID) != 0 {
		t.Errorf("signer.ChainID() = %s, want %s", signer.ChainID(), p.chainID)
	}
}

// ─── EIP-1559 (type 2) ───────────────────────────────────────────────────────

func TestBuildTx_1559Type(t *testing.T) {
	t.Parallel()
	tx, _ := buildTx(makeParsed1559())
	if tx.Type() != 2 {
		t.Fatalf("tx.Type() = %d, want 2", tx.Type())
	}
}

func TestBuildTx_1559FieldMapping(t *testing.T) {
	t.Parallel()
	p := makeParsed1559()
	tx, _ := buildTx(p)

	if tx.Nonce() != p.nonce {
		t.Errorf("Nonce: got %d, want %d", tx.Nonce(), p.nonce)
	}
	if tx.Gas() != p.gas {
		t.Errorf("Gas: got %d, want %d", tx.Gas(), p.gas)
	}
	// GasTipCap = maxPriorityFeePerGas
	if tx.GasTipCap().Cmp(p.gasTipCap) != 0 {
		t.Errorf("GasTipCap (maxPriorityFeePerGas): got %s, want %s", tx.GasTipCap(), p.gasTipCap)
	}
	// GasFeeCap = maxFeePerGas
	if tx.GasFeeCap().Cmp(p.gasFeeCap) != 0 {
		t.Errorf("GasFeeCap (maxFeePerGas): got %s, want %s", tx.GasFeeCap(), p.gasFeeCap)
	}
	// ChainID is embedded in the type-2 tx body (DynamicFeeTx.ChainID), distinct
	// from the signer's chain ID (asserted separately in TestBuildTx_1559SignerChainID).
	if tx.ChainId().Cmp(p.chainID) != 0 {
		t.Errorf("ChainId (tx body): got %s, want %s", tx.ChainId(), p.chainID)
	}
	if tx.To() == nil || *tx.To() != *p.to {
		t.Errorf("To: got %v, want %v", tx.To(), p.to)
	}
	if tx.Value().Cmp(p.value) != 0 {
		t.Errorf("Value: got %s, want %s", tx.Value(), p.value)
	}
	if !bytes.Equal(tx.Data(), p.data) {
		t.Errorf("Data: got %x, want %x", tx.Data(), p.data)
	}
}

func TestBuildTx_1559SignerChainID(t *testing.T) {
	t.Parallel()
	p := makeParsed1559()
	_, signer := buildTx(p)
	if signer.ChainID().Cmp(p.chainID) != 0 {
		t.Errorf("signer.ChainID() = %s, want %s", signer.ChainID(), p.chainID)
	}
}

// ─── Contract creation (to == nil) ───────────────────────────────────────────

func TestBuildTx_LegacyContractCreation(t *testing.T) {
	t.Parallel()
	p := makeParsedLegacy()
	p.to = nil
	p.data = []byte{0x60, 0x60} // minimal init code
	tx, _ := buildTx(p)
	if tx.To() != nil {
		t.Errorf("To: expected nil for contract creation, got %v", tx.To())
	}
}

func TestBuildTx_1559ContractCreation(t *testing.T) {
	t.Parallel()
	p := makeParsed1559()
	p.to = nil
	p.data = []byte{0x60, 0x60}
	tx, _ := buildTx(p)
	if tx.To() != nil {
		t.Errorf("To: expected nil for contract creation, got %v", tx.To())
	}
}

// ─── Empty data → RLP 0x80 ───────────────────────────────────────────────────

// TestBuildTx_LegacyEmptyData_RLP80 verifies that "0x" (empty data) is
// represented in-memory as a non-nil []byte{} so that the go-ethereum RLP
// encoder produces 0x80 (empty string) rather than omitting the field.
//
// Verification note: the non-nil / length-0 pre-condition on tx.Data() is the
// load-bearing assertion here.  go-ethereum's RLP encoder treats nil []byte and
// []byte{} identically at the wire level (both encode as 0x80), so the 0x80
// byte-scan below is not a discriminating proof — it passes even for nil data.
// The scan is retained as documentation that the field is present in the wire
// output; byte-level oracle parity (confirming the exact encoding against cast
// and ethers v6) is Issue 2.10's job.
func TestBuildTx_LegacyEmptyData_RLP80(t *testing.T) {
	t.Parallel()
	p := makeParsedLegacy()
	p.data = []byte{} // explicit empty; validate.go produces this for "0x"
	tx, signer := buildTx(p)

	// Non-nil empty slice check (precondition for correct RLP).
	if tx.Data() == nil {
		t.Fatal("tx.Data() is nil; want non-nil []byte{} for correct RLP 0x80 encoding")
	}
	if len(tx.Data()) != 0 {
		t.Fatalf("tx.Data() length = %d; want 0", len(tx.Data()))
	}

	// Sign and marshal to get the raw RLP bytes.
	raw := signBytes(t, tx, signer, throwawayKey(t))

	// Scan for 0x80 as documentation that the data field appears in the wire
	// output.  This is not a discriminating proof (nil []byte also encodes as
	// 0x80); the nil pre-condition above is the real guard.
	found := false
	for _, b := range raw {
		if b == 0x80 {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("0x80 not found in signed tx bytes; raw=%x", raw)
	}
}

func TestBuildTx_1559EmptyData_RLP80(t *testing.T) {
	t.Parallel()
	p := makeParsed1559()
	p.data = []byte{}
	tx, signer := buildTx(p)

	if tx.Data() == nil {
		t.Fatal("tx.Data() is nil; want non-nil []byte{}")
	}
	if len(tx.Data()) != 0 {
		t.Fatalf("tx.Data() length = %d; want 0", len(tx.Data()))
	}

	raw := signBytes(t, tx, signer, throwawayKey(t))
	// Scan for 0x80 as documentation that the data field appears in the wire
	// output.  See TestBuildTx_LegacyEmptyData_RLP80 for the caveat: the nil
	// pre-condition above is the real guard; the scan is not discriminating.
	found := false
	for _, b := range raw {
		if b == 0x80 {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("0x80 not found in signed tx bytes; raw=%x", raw)
	}
}

// ─── Zero value: MarshalBinary / UnmarshalBinary round-trip ──────────────────

func TestBuildTx_LegacyZeroValue_RoundTrip(t *testing.T) {
	t.Parallel()
	p := makeParsedLegacy()
	p.value = new(big.Int) // zero
	tx, _ := buildTx(p)

	rt := signRoundTrip(t, tx, p.chainID, throwawayKey(t))
	if rt.Value().Sign() != 0 {
		t.Errorf("round-tripped value = %s, want 0", rt.Value())
	}
}

func TestBuildTx_1559ZeroValue_RoundTrip(t *testing.T) {
	t.Parallel()
	p := makeParsed1559()
	p.value = new(big.Int)
	tx, _ := buildTx(p)

	rt := signRoundTrip(t, tx, p.chainID, throwawayKey(t))
	if rt.Value().Sign() != 0 {
		t.Errorf("round-tripped value = %s, want 0", rt.Value())
	}
}

// ─── >2^64 value round-trip (precision-loss guard) ───────────────────────────

// TestBuildTx_LargeValue_NoPrecisionLoss asserts that value fields are carried
// as *big.Int end-to-end with no uint64 round-trip.  A value of 2^128 wei would
// be silently truncated to 0 by any uint64 cast.
func TestBuildTx_LargeValue_NoPrecisionLoss(t *testing.T) {
	t.Parallel()

	// 2^128 — well beyond uint64 (max ≈ 1.8×10^19 ≈ 18 ETH × 10^9).
	bigVal := new(big.Int).Lsh(big.NewInt(1), 128)

	p := makeParsedLegacy()
	p.value = new(big.Int).Set(bigVal)
	tx, _ := buildTx(p)

	rt := signRoundTrip(t, tx, p.chainID, throwawayKey(t))
	if rt.Value().Cmp(bigVal) != 0 {
		t.Errorf("value after round-trip = %s, want %s (precision loss detected)", rt.Value(), bigVal)
	}
}

func TestBuildTx_1559LargeValue_NoPrecisionLoss(t *testing.T) {
	t.Parallel()

	bigVal := new(big.Int).Lsh(big.NewInt(1), 128)

	p := makeParsed1559()
	p.value = new(big.Int).Set(bigVal)
	tx, _ := buildTx(p)

	rt := signRoundTrip(t, tx, p.chainID, throwawayKey(t))
	if rt.Value().Cmp(bigVal) != 0 {
		t.Errorf("value after round-trip = %s, want %s (precision loss detected)", rt.Value(), bigVal)
	}
}

// ─── Padded nonce: byte-identical to canonical nonce ─────────────────────────

// TestBuildTx_PaddedNonce_ByteIdentical drives the full validate → buildTx path
// for three nonce representations that must produce the same unsigned tx:
//
//   - "0x0009" (padded hex)
//   - "9"      (decimal)
//   - "0x9"    (unpadded hex)
//
// The nonce is normalised by parseUint64 inside validate(), so all three should
// produce parsedTx.nonce == 9.  Signing with the same throwaway key makes the
// resulting bytes comparable.
func TestBuildTx_PaddedNonce_ByteIdentical(t *testing.T) {
	t.Parallel()

	req1 := baseValidLegacyReq()
	req2 := baseValidLegacyReq()
	req3 := baseValidLegacyReq()
	req1.Nonce = "0x0009"
	req2.Nonce = "9"
	req3.Nonce = "0x9"

	p1 := mustValidate(t, req1, nil)
	p2 := mustValidate(t, req2, nil)
	p3 := mustValidate(t, req3, nil)

	// Sanity: all parsed nonces must equal 9.
	for i, p := range []*parsedTx{p1, p2, p3} {
		if p.nonce != 9 {
			t.Errorf("parsedTx[%d].nonce = %d, want 9", i, p.nonce)
		}
	}

	key := throwawayKey(t)
	signer := types.LatestSignerForChainID(p1.chainID)

	tx1, _ := buildTx(p1)
	tx2, _ := buildTx(p2)
	tx3, _ := buildTx(p3)

	raw1 := signBytes(t, tx1, signer, key)
	raw2 := signBytes(t, tx2, signer, key)
	raw3 := signBytes(t, tx3, signer, key)

	if !bytes.Equal(raw1, raw2) {
		t.Errorf("nonce \"0x0009\" vs \"9\": byte mismatch\n  0x0009: %x\n  9:      %x", raw1, raw2)
	}
	if !bytes.Equal(raw1, raw3) {
		t.Errorf("nonce \"0x0009\" vs \"0x9\": byte mismatch\n  0x0009: %x\n  0x9:    %x", raw1, raw3)
	}
}

// ─── Defensive nil guards ─────────────────────────────────────────────────────

// TestBuildTx_NilValue_TreatedAsZero exercises the defensive value==nil guard in
// buildTx.  Under normal operation validate.go always produces a non-nil value,
// but buildTx must not panic if given nil (defensive programming contract).
func TestBuildTx_NilValue_TreatedAsZero(t *testing.T) {
	t.Parallel()
	p := makeParsedLegacy()
	p.value = nil // trigger defensive branch
	tx, _ := buildTx(p)
	if tx.Value() == nil {
		t.Fatal("tx.Value() is nil after nil-value guard; want new(big.Int) (zero)")
	}
	if tx.Value().Sign() != 0 {
		t.Errorf("tx.Value() = %s, want 0 (zero value for nil guard)", tx.Value())
	}
}

// TestBuildTx_NilData_TreatedAsEmpty exercises the defensive data==nil guard in
// buildTx.  Normally validate.go produces []byte{} for "0x"; the nil guard
// prevents a panic if a caller constructs parsedTx with Data=nil directly.
func TestBuildTx_NilData_TreatedAsEmpty(t *testing.T) {
	t.Parallel()
	p := makeParsedLegacy()
	p.data = nil // trigger defensive branch
	tx, _ := buildTx(p)
	if tx.Data() == nil {
		t.Fatal("tx.Data() is nil after nil-data guard; want []byte{}")
	}
	if len(tx.Data()) != 0 {
		t.Errorf("tx.Data() length = %d, want 0", len(tx.Data()))
	}
}

// ─── Signed smoke tests ───────────────────────────────────────────────────────

// TestBuildTx_SignedRoundTrip_Legacy signs a legacy tx with a throwaway key and
// verifies that MarshalBinary + UnmarshalBinary produces the same hash.
// This is a structural smoke test; byte-identical oracle parity is Issue 2.10's job.
func TestBuildTx_SignedRoundTrip_Legacy(t *testing.T) {
	t.Parallel()
	p := makeParsedLegacy()
	tx, signer := buildTx(p)

	key := throwawayKey(t)
	signed, err := types.SignTx(tx, signer, key)
	if err != nil {
		t.Fatalf("SignTx: %v", err)
	}
	wantHash := signed.Hash()

	raw, err := signed.MarshalBinary()
	if err != nil {
		t.Fatalf("MarshalBinary: %v", err)
	}
	rt := new(types.Transaction)
	if err := rt.UnmarshalBinary(raw); err != nil {
		t.Fatalf("UnmarshalBinary: %v", err)
	}
	if rt.Hash() != wantHash {
		t.Errorf("hash mismatch: got %s, want %s", rt.Hash(), wantHash)
	}
}

// TestBuildTx_SignedRoundTrip_1559 mirrors the smoke test for EIP-1559.
func TestBuildTx_SignedRoundTrip_1559(t *testing.T) {
	t.Parallel()
	p := makeParsed1559()
	tx, signer := buildTx(p)

	key := throwawayKey(t)
	signed, err := types.SignTx(tx, signer, key)
	if err != nil {
		t.Fatalf("SignTx: %v", err)
	}
	wantHash := signed.Hash()

	raw, err := signed.MarshalBinary()
	if err != nil {
		t.Fatalf("MarshalBinary: %v", err)
	}
	rt := new(types.Transaction)
	if err := rt.UnmarshalBinary(raw); err != nil {
		t.Fatalf("UnmarshalBinary: %v", err)
	}
	if rt.Hash() != wantHash {
		t.Errorf("hash mismatch: got %s, want %s", rt.Hash(), wantHash)
	}
}
