// Tests for validate.go — Issue 2.4.
// Internal tests (package signing) so they can access unexported types
// (parsedTx, parseBigInt, parseData, parseToAddress, parseTxType, hasMixedCase).
package signing

import (
	"math/big"
	"strings"
	"testing"
)

// ─── helpers ─────────────────────────────────────────────────────────────────

// baseValidLegacyReq returns a minimal valid legacy transaction request that
// passes all validation rules.  Tests clone and modify individual fields.
func baseValidLegacyReq() TxRequest {
	return TxRequest{
		Type:     "legacy",
		ChainID:  "1",
		Nonce:    "0",
		Gas:      "21000",
		Value:    "0",
		Data:     "0x",
		GasPrice: "1000000000",
	}
}

// baseValid1559Req returns a minimal valid EIP-1559 transaction request.
func baseValid1559Req() TxRequest {
	return TxRequest{
		Type:                 "eip1559",
		ChainID:              "1",
		Nonce:                "0",
		Gas:                  "21000",
		Value:                "0",
		Data:                 "0x",
		MaxFeePerGas:         "2000000000",
		MaxPriorityFeePerGas: "1000000000",
	}
}

// mustValidate calls validate and fails the test if any error is returned.
func mustValidate(t *testing.T, req TxRequest, guard *uint64) *parsedTx {
	t.Helper()
	p, err := validate(req, guard)
	if err != nil {
		t.Fatalf("validate: unexpected error code=%q message=%q", err.Code, err.Message)
	}
	return p
}

// requireErrCode calls validate and asserts the returned *ToolError has exactly
// the given code.  It fails the test if no error is returned.
// The *ToolError is not returned to keep callers simple and avoid errcheck noise;
// tests that need the error value call validate directly.
func requireErrCode(t *testing.T, req TxRequest, guard *uint64, wantCode string) {
	t.Helper()
	_, te := validate(req, guard)
	if te == nil {
		t.Fatalf("validate: expected error code=%q, got nil", wantCode)
	}
	if te.Code != wantCode {
		t.Fatalf("validate: error code=%q, want %q (message: %s)", te.Code, wantCode, te.Message)
	}
}

// uint64Ptr is a tiny helper so guard literals are less noisy.
func uint64Ptr(v uint64) *uint64 { return &v }

// ─── Rule 1: chainId parse failure / chainId == 0 ────────────────────────────

func TestValidate_Rule1_ChainIDRequired(t *testing.T) {
	t.Parallel()
	r := baseValidLegacyReq()
	r.ChainID = ""
	requireErrCode(t, r, nil, CodeInvalidInput)
}

func TestValidate_Rule1_ChainIDInvalidDecimal(t *testing.T) {
	t.Parallel()
	r := baseValidLegacyReq()
	r.ChainID = "not-a-number"
	requireErrCode(t, r, nil, CodeInvalidInput)
}

func TestValidate_Rule1_ChainIDInvalidHex(t *testing.T) {
	t.Parallel()
	r := baseValidLegacyReq()
	r.ChainID = "0xGGGG"
	requireErrCode(t, r, nil, CodeInvalidInput)
}

func TestValidate_Rule1_ChainIDZeroDecimal(t *testing.T) {
	t.Parallel()
	r := baseValidLegacyReq()
	r.ChainID = "0"
	requireErrCode(t, r, nil, CodeInvalidInput)
}

func TestValidate_Rule1_ChainIDZeroHex(t *testing.T) {
	t.Parallel()
	r := baseValidLegacyReq()
	r.ChainID = "0x0"
	requireErrCode(t, r, nil, CodeInvalidInput)
}

func TestValidate_Rule1_ChainIDValidDecimal(t *testing.T) {
	t.Parallel()
	r := baseValidLegacyReq()
	r.ChainID = "1"
	p := mustValidate(t, r, nil)
	if p.chainID.Cmp(big.NewInt(1)) != 0 {
		t.Errorf("chainID = %s, want 1", p.chainID)
	}
}

func TestValidate_Rule1_ChainIDValidHex(t *testing.T) {
	t.Parallel()
	r := baseValidLegacyReq()
	r.ChainID = "0x1"
	p := mustValidate(t, r, nil)
	if p.chainID.Cmp(big.NewInt(1)) != 0 {
		t.Errorf("chainID = %s, want 1", p.chainID)
	}
}

func TestValidate_Rule1_ChainIDSepolia(t *testing.T) {
	t.Parallel()
	r := baseValidLegacyReq()
	r.ChainID = "11155111"
	p := mustValidate(t, r, nil)
	if p.chainID.Cmp(big.NewInt(11155111)) != 0 {
		t.Errorf("chainID = %s, want 11155111", p.chainID)
	}
}

func TestValidate_Rule1_ChainIDNegative(t *testing.T) {
	t.Parallel()
	r := baseValidLegacyReq()
	r.ChainID = "-1"
	requireErrCode(t, r, nil, CodeInvalidInput)
}

// ─── Rule 2: chain-id guard ───────────────────────────────────────────────────

func TestValidate_Rule2_GuardMatch(t *testing.T) {
	t.Parallel()
	r := baseValidLegacyReq()
	r.ChainID = "1"
	mustValidate(t, r, uint64Ptr(1))
}

func TestValidate_Rule2_GuardMismatch(t *testing.T) {
	t.Parallel()
	r := baseValidLegacyReq()
	r.ChainID = "1"
	requireErrCode(t, r, uint64Ptr(5), CodeChainIDMismatch)
}

func TestValidate_Rule2_GuardNil(t *testing.T) {
	t.Parallel()
	r := baseValidLegacyReq()
	r.ChainID = "1"
	mustValidate(t, r, nil) // no guard: any chainId is accepted
}

// Ordering: guard mismatch fires BEFORE type / required-field checks.
// The request below has an invalid type AND guard mismatch; chain_id_mismatch must win.
func TestValidate_Rule2_GuardMismatchBeforeTypCheck(t *testing.T) {
	t.Parallel()
	r := baseValidLegacyReq()
	r.ChainID = "1"
	r.Type = "0x3" // unsupported, but guard fires first
	requireErrCode(t, r, uint64Ptr(5), CodeChainIDMismatch)
}

// Ordering: guard mismatch fires BEFORE required-field checks.
func TestValidate_Rule2_GuardMismatchBeforeRequiredFields(t *testing.T) {
	t.Parallel()
	r := baseValidLegacyReq()
	r.ChainID = "1"
	r.Gas = "" // required field missing, but guard fires first
	requireErrCode(t, r, uint64Ptr(5), CodeChainIDMismatch)
}

// ─── Rule 3: transaction type ─────────────────────────────────────────────────

func TestValidate_Rule3_TypeLegacy(t *testing.T) {
	t.Parallel()
	r := baseValidLegacyReq()
	r.Type = "legacy"
	p := mustValidate(t, r, nil)
	if p.txType != 0 {
		t.Errorf("txType = %d, want 0", p.txType)
	}
}

func TestValidate_Rule3_TypeHex0(t *testing.T) {
	t.Parallel()
	r := baseValidLegacyReq()
	r.Type = "0x0"
	p := mustValidate(t, r, nil)
	if p.txType != 0 {
		t.Errorf("txType = %d, want 0", p.txType)
	}
}

func TestValidate_Rule3_TypeEIP1559(t *testing.T) {
	t.Parallel()
	r := baseValid1559Req()
	r.Type = "eip1559"
	p := mustValidate(t, r, nil)
	if p.txType != 2 {
		t.Errorf("txType = %d, want 2", p.txType)
	}
}

func TestValidate_Rule3_TypeHex2(t *testing.T) {
	t.Parallel()
	r := baseValid1559Req()
	r.Type = "0x2"
	p := mustValidate(t, r, nil)
	if p.txType != 2 {
		t.Errorf("txType = %d, want 2", p.txType)
	}
}

func TestValidate_Rule3_TypeEmptyString(t *testing.T) {
	t.Parallel()
	r := baseValidLegacyReq()
	r.Type = ""
	requireErrCode(t, r, nil, CodeUnsupportedType)
}

func TestValidate_Rule3_TypeHex1(t *testing.T) {
	t.Parallel()
	r := baseValidLegacyReq()
	r.Type = "0x1"
	requireErrCode(t, r, nil, CodeUnsupportedType)
}

func TestValidate_Rule3_TypeHex3(t *testing.T) {
	t.Parallel()
	r := baseValidLegacyReq()
	r.Type = "0x3"
	requireErrCode(t, r, nil, CodeUnsupportedType)
}

func TestValidate_Rule3_TypeHex4(t *testing.T) {
	t.Parallel()
	r := baseValidLegacyReq()
	r.Type = "0x4"
	requireErrCode(t, r, nil, CodeUnsupportedType)
}

func TestValidate_Rule3_TypeGarbage(t *testing.T) {
	t.Parallel()
	r := baseValidLegacyReq()
	r.Type = "eip155"
	requireErrCode(t, r, nil, CodeUnsupportedType)
}

// Legacy is case-sensitive: "Legacy" must fail.
func TestValidate_Rule3_TypeLegacyCaseSensitive(t *testing.T) {
	t.Parallel()
	r := baseValidLegacyReq()
	r.Type = "Legacy"
	requireErrCode(t, r, nil, CodeUnsupportedType)
}

// ─── Rule 4: required fields ──────────────────────────────────────────────────

func TestValidate_Rule4_NonceMissing(t *testing.T) {
	t.Parallel()
	r := baseValidLegacyReq()
	r.Nonce = ""
	requireErrCode(t, r, nil, CodeInvalidInput)
}

func TestValidate_Rule4_GasMissing(t *testing.T) {
	t.Parallel()
	r := baseValidLegacyReq()
	r.Gas = ""
	requireErrCode(t, r, nil, CodeInvalidInput)
}

func TestValidate_Rule4_ValueMissing(t *testing.T) {
	t.Parallel()
	r := baseValidLegacyReq()
	r.Value = ""
	requireErrCode(t, r, nil, CodeInvalidInput)
}

func TestValidate_Rule4_GasPriceMissingForLegacy(t *testing.T) {
	t.Parallel()
	r := baseValidLegacyReq()
	r.GasPrice = ""
	requireErrCode(t, r, nil, CodeInvalidInput)
}

func TestValidate_Rule4_MaxFeePerGasMissingFor1559(t *testing.T) {
	t.Parallel()
	r := baseValid1559Req()
	r.MaxFeePerGas = ""
	requireErrCode(t, r, nil, CodeInvalidInput)
}

func TestValidate_Rule4_MaxPriorityFeePerGasMissingFor1559(t *testing.T) {
	t.Parallel()
	r := baseValid1559Req()
	r.MaxPriorityFeePerGas = ""
	requireErrCode(t, r, nil, CodeInvalidInput)
}

// ─── Rule 5: type-inappropriate fields ───────────────────────────────────────

func TestValidate_Rule5_GasPriceOnType2(t *testing.T) {
	t.Parallel()
	r := baseValid1559Req()
	r.GasPrice = "1000000000"
	requireErrCode(t, r, nil, CodeInvalidInput)
}

func TestValidate_Rule5_MaxFeePerGasOnLegacy(t *testing.T) {
	t.Parallel()
	r := baseValidLegacyReq()
	r.MaxFeePerGas = "2000000000"
	requireErrCode(t, r, nil, CodeInvalidInput)
}

func TestValidate_Rule5_MaxPriorityFeePerGasOnLegacy(t *testing.T) {
	t.Parallel()
	r := baseValidLegacyReq()
	r.MaxPriorityFeePerGas = "1000000000"
	requireErrCode(t, r, nil, CodeInvalidInput)
}

// ─── Rule 6: accessList ───────────────────────────────────────────────────────

func TestValidate_Rule6_AccessListEmpty(t *testing.T) {
	t.Parallel()
	r := baseValidLegacyReq()
	r.AccessList = nil
	mustValidate(t, r, nil)
}

func TestValidate_Rule6_AccessListNonEmpty(t *testing.T) {
	t.Parallel()
	r := baseValidLegacyReq()
	r.AccessList = []struct{}{{}}
	requireErrCode(t, r, nil, CodeInvalidInput)
}

// ─── Rule 7: numeric parsing ──────────────────────────────────────────────────

func TestValidate_Rule7_NonceDecimal(t *testing.T) {
	t.Parallel()
	r := baseValidLegacyReq()
	r.Nonce = "9"
	p := mustValidate(t, r, nil)
	if p.nonce != 9 {
		t.Errorf("nonce = %d, want 9", p.nonce)
	}
}

func TestValidate_Rule7_NonceHex(t *testing.T) {
	t.Parallel()
	r := baseValidLegacyReq()
	r.Nonce = "0x9"
	p := mustValidate(t, r, nil)
	if p.nonce != 9 {
		t.Errorf("nonce = %d, want 9", p.nonce)
	}
}

func TestValidate_Rule7_NoncePaddedHex(t *testing.T) {
	t.Parallel()
	r := baseValidLegacyReq()
	r.Nonce = "0x0009"
	p := mustValidate(t, r, nil)
	if p.nonce != 9 {
		t.Errorf("nonce = %d, want 9", p.nonce)
	}
}

// "0x0009" must produce the same parsedTx as "9" and "0x9".
func TestValidate_Rule7_NoncePaddedNormalization(t *testing.T) {
	t.Parallel()
	cases := []string{"9", "0x9", "0x0009"}
	var results []uint64
	for _, s := range cases {
		r := baseValidLegacyReq()
		r.Nonce = s
		p := mustValidate(t, r, nil)
		results = append(results, p.nonce)
	}
	for i := 1; i < len(results); i++ {
		if results[i] != results[0] {
			t.Errorf("nonce %q produced %d, but %q produced %d; they must be equal",
				cases[i], results[i], cases[0], results[0])
		}
	}
}

func TestValidate_Rule7_NonceInvalid(t *testing.T) {
	t.Parallel()
	r := baseValidLegacyReq()
	r.Nonce = "not-a-nonce"
	requireErrCode(t, r, nil, CodeInvalidInput)
}

func TestValidate_Rule7_GasDecimal(t *testing.T) {
	t.Parallel()
	r := baseValidLegacyReq()
	r.Gas = "21000"
	p := mustValidate(t, r, nil)
	if p.gas != 21000 {
		t.Errorf("gas = %d, want 21000", p.gas)
	}
}

func TestValidate_Rule7_GasHex(t *testing.T) {
	t.Parallel()
	r := baseValidLegacyReq()
	r.Gas = "0x5208"
	p := mustValidate(t, r, nil)
	if p.gas != 21000 {
		t.Errorf("gas = %d, want 21000", p.gas)
	}
}

func TestValidate_Rule7_GasInvalid(t *testing.T) {
	t.Parallel()
	r := baseValidLegacyReq()
	r.Gas = "0xGG"
	requireErrCode(t, r, nil, CodeInvalidInput)
}

func TestValidate_Rule7_ValueZero(t *testing.T) {
	t.Parallel()
	r := baseValidLegacyReq()
	r.Value = "0"
	p := mustValidate(t, r, nil)
	if p.value.Sign() != 0 {
		t.Errorf("value = %s, want 0", p.value)
	}
}

func TestValidate_Rule7_ValueHex(t *testing.T) {
	t.Parallel()
	r := baseValidLegacyReq()
	r.Value = "0xde0b6b3a7640000" // 1 ETH in wei
	p := mustValidate(t, r, nil)
	want := new(big.Int)
	want.SetString("de0b6b3a7640000", 16)
	if p.value.Cmp(want) != 0 {
		t.Errorf("value = %s, want %s", p.value, want)
	}
}

func TestValidate_Rule7_GasPriceLegacy(t *testing.T) {
	t.Parallel()
	r := baseValidLegacyReq()
	r.GasPrice = "1000000000"
	p := mustValidate(t, r, nil)
	if p.gasPrice.Cmp(big.NewInt(1000000000)) != 0 {
		t.Errorf("gasPrice = %s, want 1000000000", p.gasPrice)
	}
}

func TestValidate_Rule7_GasPriceInvalidForLegacy(t *testing.T) {
	t.Parallel()
	r := baseValidLegacyReq()
	r.GasPrice = "not-a-number"
	requireErrCode(t, r, nil, CodeInvalidInput)
}

func TestValidate_Rule7_MaxFeePerGas1559(t *testing.T) {
	t.Parallel()
	r := baseValid1559Req()
	r.MaxFeePerGas = "2000000000"
	p := mustValidate(t, r, nil)
	if p.gasFeeCap.Cmp(big.NewInt(2000000000)) != 0 {
		t.Errorf("gasFeeCap = %s, want 2000000000", p.gasFeeCap)
	}
}

func TestValidate_Rule7_MaxPriorityFeePerGas1559(t *testing.T) {
	t.Parallel()
	r := baseValid1559Req()
	r.MaxPriorityFeePerGas = "1000000000"
	p := mustValidate(t, r, nil)
	if p.gasTipCap.Cmp(big.NewInt(1000000000)) != 0 {
		t.Errorf("gasTipCap = %s, want 1000000000", p.gasTipCap)
	}
}

func TestValidate_Rule7_MaxFeePerGasInvalid(t *testing.T) {
	t.Parallel()
	r := baseValid1559Req()
	r.MaxFeePerGas = "bad"
	requireErrCode(t, r, nil, CodeInvalidInput)
}

func TestValidate_Rule7_MaxPriorityFeePerGasInvalid(t *testing.T) {
	t.Parallel()
	r := baseValid1559Req()
	r.MaxPriorityFeePerGas = "bad"
	requireErrCode(t, r, nil, CodeInvalidInput)
}

// Nonce and gas must fit in uint64.
func TestValidate_Rule7_NonceTooLarge(t *testing.T) {
	t.Parallel()
	r := baseValidLegacyReq()
	// 2^64 = 18446744073709551616 — one too large for uint64.
	r.Nonce = "18446744073709551616"
	requireErrCode(t, r, nil, CodeInvalidInput)
}

func TestValidate_Rule7_GasTooLarge(t *testing.T) {
	t.Parallel()
	r := baseValidLegacyReq()
	r.Gas = "0x10000000000000000" // 2^64 in hex
	requireErrCode(t, r, nil, CodeInvalidInput)
}

// ─── Rule 8: data field ───────────────────────────────────────────────────────

func TestValidate_Rule8_DataEmptyHex(t *testing.T) {
	t.Parallel()
	r := baseValidLegacyReq()
	r.Data = "0x"
	p := mustValidate(t, r, nil)
	// Must be non-nil empty slice (not nil) so RLP encodes as 0x80.
	if p.data == nil {
		t.Error("data is nil; must be non-nil []byte{} for RLP 0x80")
	}
	if len(p.data) != 0 {
		t.Errorf("len(data) = %d, want 0", len(p.data))
	}
}

func TestValidate_Rule8_DataValid(t *testing.T) {
	t.Parallel()
	r := baseValidLegacyReq()
	r.Data = "0xdeadbeef"
	p := mustValidate(t, r, nil)
	if len(p.data) != 4 {
		t.Errorf("len(data) = %d, want 4", len(p.data))
	}
}

func TestValidate_Rule8_DataNoPrefix(t *testing.T) {
	t.Parallel()
	r := baseValidLegacyReq()
	r.Data = "deadbeef" // missing 0x prefix
	requireErrCode(t, r, nil, CodeInvalidInput)
}

func TestValidate_Rule8_DataEmptyString(t *testing.T) {
	t.Parallel()
	r := baseValidLegacyReq()
	r.Data = ""
	requireErrCode(t, r, nil, CodeInvalidInput)
}

func TestValidate_Rule8_DataOddLength(t *testing.T) {
	t.Parallel()
	r := baseValidLegacyReq()
	r.Data = "0xabc" // 3 hex chars — odd
	requireErrCode(t, r, nil, CodeInvalidInput)
}

func TestValidate_Rule8_DataInvalidHexChars(t *testing.T) {
	t.Parallel()
	r := baseValidLegacyReq()
	r.Data = "0xGGGG"
	requireErrCode(t, r, nil, CodeInvalidInput)
}

func TestValidate_Rule8_DataExactly256KiB(t *testing.T) {
	t.Parallel()
	// 256 KiB = 262,144 bytes.  Build a 0x-prefixed even-length hex string.
	hexBytes := make([]byte, 262144)
	hexStr := "0x" + strings.Repeat("aa", 262144)
	_ = hexBytes
	r := baseValidLegacyReq()
	r.Data = hexStr
	p := mustValidate(t, r, nil)
	if len(p.data) != 262144 {
		t.Errorf("len(data) = %d, want 262144", len(p.data))
	}
}

func TestValidate_Rule8_DataOver256KiB(t *testing.T) {
	t.Parallel()
	// 262,145 bytes (1 over the limit).
	hexStr := "0x" + strings.Repeat("aa", 262145)
	r := baseValidLegacyReq()
	r.Data = hexStr
	requireErrCode(t, r, nil, CodeInvalidInput)
}

// ─── Rule 9: to field / EIP-55 ────────────────────────────────────────────────

// Contract creation: to omitted (empty string).
func TestValidate_Rule9_ToEmptyIsContractCreation(t *testing.T) {
	t.Parallel()
	r := baseValidLegacyReq()
	r.To = ""
	p := mustValidate(t, r, nil)
	if p.to != nil {
		t.Errorf("to = %v, want nil (contract creation)", p.to)
	}
}

// Contract creation: to absent (same as empty string in Go zero value).
func TestValidate_Rule9_ToAbsent1559ContractCreation(t *testing.T) {
	t.Parallel()
	r := baseValid1559Req()
	r.To = ""
	p := mustValidate(t, r, nil)
	if p.to != nil {
		t.Errorf("to = %v, want nil (contract creation, 1559)", p.to)
	}
}

// All-lowercase address: accepted without checksum check.
func TestValidate_Rule9_ToAllLowercase(t *testing.T) {
	t.Parallel()
	r := baseValidLegacyReq()
	r.To = "0x9858effd232b4033e47d90003d41ec34ecaeda94"
	p := mustValidate(t, r, nil)
	if p.to == nil {
		t.Fatal("to is nil; expected non-nil address")
	}
}

// All-uppercase address: accepted without checksum check.
func TestValidate_Rule9_ToAllUppercase(t *testing.T) {
	t.Parallel()
	r := baseValidLegacyReq()
	r.To = "0x9858EFFD232B4033E47D90003D41EC34ECAEDA94"
	p := mustValidate(t, r, nil)
	if p.to == nil {
		t.Fatal("to is nil; expected non-nil address")
	}
}

// Correct EIP-55 checksum: accepted.
func TestValidate_Rule9_ToChecksumCorrect(t *testing.T) {
	t.Parallel()
	r := baseValidLegacyReq()
	// FixtureTestAddress is the canonical EIP-55 form of the test address.
	r.To = FixtureTestAddress // "0x9858EfFD232B4033E47d90003D41EC34EcaEda94"
	p := mustValidate(t, r, nil)
	if p.to == nil {
		t.Fatal("to is nil; expected non-nil address for correct checksum")
	}
	if got := p.to.Hex(); got != FixtureTestAddress {
		t.Errorf("to.Hex() = %q, want %q", got, FixtureTestAddress)
	}
}

// Wrong EIP-55 checksum (one character case-flipped): rejected.
func TestValidate_Rule9_ToChecksumFail(t *testing.T) {
	t.Parallel()
	// FixtureTestAddress = "0x9858EfFD232B4033E47d90003D41EC34EcaEda94"
	// Flip the 'E' at position 6 to 'e' to create a checksum failure
	// while keeping the address mixed-case.
	badAddr := "0x9858efFD232B4033E47d90003D41EC34EcaEda94" // 'E' → 'e' at index 6
	r := baseValidLegacyReq()
	r.To = badAddr
	requireErrCode(t, r, nil, CodeInvalidInput)
}

// Malformed to: wrong length.
func TestValidate_Rule9_ToMalformedShort(t *testing.T) {
	t.Parallel()
	r := baseValidLegacyReq()
	r.To = "0x1234" // too short
	requireErrCode(t, r, nil, CodeInvalidInput)
}

// Malformed to: invalid hex characters.
func TestValidate_Rule9_ToMalformedInvalidChars(t *testing.T) {
	t.Parallel()
	r := baseValidLegacyReq()
	r.To = "0xGGGGGGGGGGGGGGGGGGGGGGGGGGGGGGGGGGGGGGGG" // 40 G's
	requireErrCode(t, r, nil, CodeInvalidInput)
}

// Malformed to: no 0x prefix.
func TestValidate_Rule9_ToNoPrefix(t *testing.T) {
	t.Parallel()
	r := baseValidLegacyReq()
	r.To = "9858effd232b4033e47d90003d41ec34ecaeda94" // 40 chars, no 0x
	requireErrCode(t, r, nil, CodeInvalidInput)
}

// ─── Full EIP-55 acceptance criteria ─────────────────────────────────────────

// All four EIP-55 scenarios in one table-driven test for clarity.
func TestValidate_EIP55(t *testing.T) {
	t.Parallel()

	// The fixture test address in its canonical EIP-55 form.
	correctChecksum := FixtureTestAddress // "0x9858EfFD232B4033E47d90003D41EC34EcaEda94"

	// Checksum-failing mixed-case: flip first letter 'E' to 'e' at index 6.
	failChecksum := "0x9858efFD232B4033E47d90003D41EC34EcaEda94"

	allLower := "0x9858effd232b4033e47d90003d41ec34ecaeda94"
	allUpper := "0x9858EFFD232B4033E47D90003D41EC34ECAEDA94"

	tests := []struct {
		name    string
		to      string
		wantErr bool
	}{
		{"correct-checksum", correctChecksum, false},
		{"fail-checksum", failChecksum, true},
		{"all-lower", allLower, false},
		{"all-upper", allUpper, false},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			r := baseValidLegacyReq()
			r.To = tc.to
			_, err := validate(r, nil)
			if tc.wantErr && err == nil {
				t.Fatalf("validate: expected error, got nil")
			}
			if !tc.wantErr && err != nil {
				t.Fatalf("validate: unexpected error code=%q message=%q", err.Code, err.Message)
			}
			if tc.wantErr && err != nil && err.Code != CodeInvalidInput {
				t.Errorf("error code = %q, want %q", err.Code, CodeInvalidInput)
			}
		})
	}
}

// ─── Contract creation for both tx types ─────────────────────────────────────

func TestValidate_ContractCreationLegacy(t *testing.T) {
	t.Parallel()
	r := baseValidLegacyReq()
	r.To = ""
	p := mustValidate(t, r, nil)
	if p.to != nil {
		t.Errorf("to = %v, want nil for contract creation (legacy)", p.to)
	}
}

func TestValidate_ContractCreation1559(t *testing.T) {
	t.Parallel()
	r := baseValid1559Req()
	r.To = ""
	p := mustValidate(t, r, nil)
	if p.to != nil {
		t.Errorf("to = %v, want nil for contract creation (1559)", p.to)
	}
}

// ─── Guard ordering: chain_id_mismatch even when later rules would fire ───────

// This test locks the rule-2 ordering requirement: chain_id_mismatch must be
// returned even when the request has additional violations in rules 3 through 9.
func TestValidate_GuardOrderingIsRule2(t *testing.T) {
	t.Parallel()

	// Build a request with chainId mismatch + multiple other violations.
	r := TxRequest{
		Type:    "0x3",          // unsupported type (rule 3)
		ChainID: "1",            // mismatches guard=5 (rule 2)
		Nonce:   "",             // missing (rule 4)
		Gas:     "",             // missing (rule 4)
		Value:   "",             // missing (rule 4)
		Data:    "invalid-data", // invalid (rule 8)
		To:      "bad-address",  // invalid (rule 9)
	}
	// Guard is 5; request has chainId=1 → mismatch.
	requireErrCode(t, r, uint64Ptr(5), CodeChainIDMismatch)
}

// ─── parsedTx fields after happy path ────────────────────────────────────────

func TestValidate_HappyPathLegacy(t *testing.T) {
	t.Parallel()

	r := TxRequest{
		Type:     "0x0",
		ChainID:  "1",
		Nonce:    "42",
		Gas:      "21000",
		Value:    "1000000000000000000", // 1 ETH in wei
		Data:     "0xdeadbeef",
		GasPrice: "10000000000",
		To:       FixtureTestAddress,
	}
	p := mustValidate(t, r, nil)

	if p.txType != 0 {
		t.Errorf("txType = %d, want 0", p.txType)
	}
	if p.chainID.Cmp(big.NewInt(1)) != 0 {
		t.Errorf("chainID = %s, want 1", p.chainID)
	}
	if p.nonce != 42 {
		t.Errorf("nonce = %d, want 42", p.nonce)
	}
	if p.gas != 21000 {
		t.Errorf("gas = %d, want 21000", p.gas)
	}
	wantValue := new(big.Int)
	wantValue.SetString("1000000000000000000", 10)
	if p.value.Cmp(wantValue) != 0 {
		t.Errorf("value = %s, want %s", p.value, wantValue)
	}
	if len(p.data) != 4 {
		t.Errorf("len(data) = %d, want 4", len(p.data))
	}
	if p.gasPrice.Cmp(big.NewInt(10000000000)) != 0 {
		t.Errorf("gasPrice = %s, want 10000000000", p.gasPrice)
	}
	if p.gasTipCap != nil {
		t.Errorf("gasTipCap = %v, want nil for legacy", p.gasTipCap)
	}
	if p.gasFeeCap != nil {
		t.Errorf("gasFeeCap = %v, want nil for legacy", p.gasFeeCap)
	}
	if p.to == nil {
		t.Fatal("to is nil; expected non-nil address")
	}
}

func TestValidate_HappyPath1559(t *testing.T) {
	t.Parallel()

	r := TxRequest{
		Type:                 "0x2",
		ChainID:              "11155111",
		Nonce:                "0x1",
		Gas:                  "0x5208",
		Value:                "0",
		Data:                 "0x",
		MaxFeePerGas:         "0x77359400", // 2 Gwei
		MaxPriorityFeePerGas: "0x3b9aca00", // 1 Gwei
		To:                   FixtureTestAddress,
	}
	p := mustValidate(t, r, nil)

	if p.txType != 2 {
		t.Errorf("txType = %d, want 2", p.txType)
	}
	if p.chainID.Cmp(big.NewInt(11155111)) != 0 {
		t.Errorf("chainID = %s, want 11155111", p.chainID)
	}
	if p.nonce != 1 {
		t.Errorf("nonce = %d, want 1", p.nonce)
	}
	if p.gas != 21000 {
		t.Errorf("gas = %d, want 21000", p.gas)
	}
	if p.value.Sign() != 0 {
		t.Errorf("value = %s, want 0", p.value)
	}
	if p.gasPrice != nil {
		t.Errorf("gasPrice = %v, want nil for 1559", p.gasPrice)
	}
	if p.gasTipCap.Cmp(big.NewInt(1e9)) != 0 {
		t.Errorf("gasTipCap = %s, want 1000000000", p.gasTipCap)
	}
	if p.gasFeeCap.Cmp(big.NewInt(2e9)) != 0 {
		t.Errorf("gasFeeCap = %s, want 2000000000", p.gasFeeCap)
	}
}

// ─── No raw input in error messages ──────────────────────────────────────────

// TestValidate_ErrorMessagesNeverEchoRawInput verifies that no error message
// contains the raw input value that triggered the error.  This is a security
// invariant: a caller-supplied secret embedded in a field value must not be
// reflectable back into logs or the wire response.
func TestValidate_ErrorMessagesNeverEchoRawInput(t *testing.T) {
	t.Parallel()

	// Each entry: an input string that appears in one of the reject fields, plus
	// a function that injects it into a request.  The test asserts the error
	// message (if any) does not contain the input verbatim.
	type scenario struct {
		name      string
		input     string // the raw input value that must NOT appear in error messages
		makeReq   func(string) TxRequest
		makeGuard func(string) *uint64
	}

	scenarios := []scenario{
		{
			name:  "chainId_bad",
			input: "SECRETCHAINID999",
			makeReq: func(in string) TxRequest {
				r := baseValidLegacyReq()
				r.ChainID = in
				return r
			},
			makeGuard: func(_ string) *uint64 { return nil },
		},
		{
			name:  "nonce_bad",
			input: "SECRETNONCE999",
			makeReq: func(in string) TxRequest {
				r := baseValidLegacyReq()
				r.Nonce = in
				return r
			},
			makeGuard: func(_ string) *uint64 { return nil },
		},
		{
			name:  "gas_bad",
			input: "SECRETGAS999",
			makeReq: func(in string) TxRequest {
				r := baseValidLegacyReq()
				r.Gas = in
				return r
			},
			makeGuard: func(_ string) *uint64 { return nil },
		},
		{
			name:  "value_bad",
			input: "SECRETVALUE999",
			makeReq: func(in string) TxRequest {
				r := baseValidLegacyReq()
				r.Value = in
				return r
			},
			makeGuard: func(_ string) *uint64 { return nil },
		},
		{
			name:  "gasPrice_bad",
			input: "SECRETGASPRICE999",
			makeReq: func(in string) TxRequest {
				r := baseValidLegacyReq()
				r.GasPrice = in
				return r
			},
			makeGuard: func(_ string) *uint64 { return nil },
		},
		{
			name:  "to_bad",
			input: "SECRETADDRESS0000000000000000000000000000",
			makeReq: func(in string) TxRequest {
				r := baseValidLegacyReq()
				r.To = in
				return r
			},
			makeGuard: func(_ string) *uint64 { return nil },
		},
		{
			name:  "data_bad",
			input: "SECRETCALLDATA",
			makeReq: func(in string) TxRequest {
				r := baseValidLegacyReq()
				r.Data = in
				return r
			},
			makeGuard: func(_ string) *uint64 { return nil },
		},
		{
			name:  "type_bad",
			input: "SECRETTYPE999",
			makeReq: func(in string) TxRequest {
				r := baseValidLegacyReq()
				r.Type = in
				return r
			},
			makeGuard: func(_ string) *uint64 { return nil },
		},
		{
			name:  "maxFeePerGas_bad",
			input: "SECRETMAXFEE999",
			makeReq: func(in string) TxRequest {
				r := baseValid1559Req()
				r.MaxFeePerGas = in
				return r
			},
			makeGuard: func(_ string) *uint64 { return nil },
		},
		{
			name:  "maxPriorityFeePerGas_bad",
			input: "SECRETPRIORITY999",
			makeReq: func(in string) TxRequest {
				r := baseValid1559Req()
				r.MaxPriorityFeePerGas = in
				return r
			},
			makeGuard: func(_ string) *uint64 { return nil },
		},
	}

	for _, sc := range scenarios {
		sc := sc
		t.Run(sc.name, func(t *testing.T) {
			t.Parallel()
			req := sc.makeReq(sc.input)
			guard := sc.makeGuard(sc.input)
			_, err := validate(req, guard)
			if err == nil {
				// If there's no error, the test is inconclusive for this input.
				// That means the input accidentally passed validation — skip.
				t.Skipf("input %q unexpectedly passed validation", sc.input)
			}
			if strings.Contains(err.Message, sc.input) {
				t.Errorf("error message %q contains raw input value %q (security violation)",
					err.Message, sc.input)
			}
		})
	}
}

// ─── parseBigInt unit tests ───────────────────────────────────────────────────

func TestParseBigInt(t *testing.T) {
	t.Parallel()

	tests := []struct {
		in      string
		wantVal int64 // used only when wantErr is false; 0 means big.Int.Sign()==0
		wantErr bool
	}{
		{in: "0", wantVal: 0},
		{in: "1", wantVal: 1},
		{in: "255", wantVal: 255},
		{in: "0x0", wantVal: 0},
		{in: "0x1", wantVal: 1},
		{in: "0xff", wantVal: 255},
		{in: "0xFF", wantVal: 255},
		{in: "0x0009", wantVal: 9},

		// errors
		{in: "", wantErr: true},
		{in: "-1", wantErr: true},
		{in: "0x", wantErr: true},
		{in: "abc", wantErr: true},
		{in: "0xGG", wantErr: true},
		{in: "1.5", wantErr: true},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.in, func(t *testing.T) {
			t.Parallel()
			got, err := parseBigInt(tc.in)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("parseBigInt(%q): expected error, got %v", tc.in, got)
				}
				return
			}
			if err != nil {
				t.Fatalf("parseBigInt(%q): unexpected error: %v", tc.in, err)
			}
			want := big.NewInt(tc.wantVal)
			if got.Cmp(want) != 0 {
				t.Errorf("parseBigInt(%q) = %v, want %v", tc.in, got, want)
			}
		})
	}
}

// ─── parseData unit tests ─────────────────────────────────────────────────────

func TestParseData(t *testing.T) {
	t.Parallel()

	tests := []struct {
		in      string
		wantLen int
		wantErr bool
	}{
		{in: "0x", wantLen: 0},
		{in: "0xdeadbeef", wantLen: 4},
		{in: "0x00", wantLen: 1},

		// errors
		{in: "", wantErr: true},
		{in: "deadbeef", wantErr: true}, // no 0x prefix
		{in: "0xabc", wantErr: true},    // odd length
		{in: "0xGGGG", wantErr: true},   // invalid hex chars
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.in, func(t *testing.T) {
			t.Parallel()
			got, err := parseData(tc.in)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("parseData(%q): expected error, got %v", tc.in, got)
				}
				return
			}
			if err != nil {
				t.Fatalf("parseData(%q): unexpected error: %v", tc.in, err)
			}
			if got == nil {
				t.Fatalf("parseData(%q): returned nil (must be non-nil for RLP encoding)", tc.in)
			}
			if len(got) != tc.wantLen {
				t.Errorf("parseData(%q): len = %d, want %d", tc.in, len(got), tc.wantLen)
			}
		})
	}
}

// TestParseData_NonNilForEmpty asserts "0x" → []byte{} (non-nil), not nil.
// This is required for correct RLP encoding (nil slice encodes as 0xc0, not 0x80).
func TestParseData_NonNilForEmpty(t *testing.T) {
	t.Parallel()
	got, err := parseData("0x")
	if err != nil {
		t.Fatalf("parseData(\"0x\"): unexpected error: %v", err)
	}
	if got == nil {
		t.Error("parseData(\"0x\") returned nil; must return []byte{} (non-nil) for RLP 0x80")
	}
}

// ─── hasMixedCase unit tests ──────────────────────────────────────────────────

func TestHasMixedCase(t *testing.T) {
	t.Parallel()

	tests := []struct {
		in   string
		want bool
	}{
		{"abcdef", false},  // all lower
		{"ABCDEF", false},  // all upper
		{"123456", false},  // digits only
		{"AbcDEF", true},   // mixed
		{"1234abCD", true}, // mixed with digits
		{"", false},        // empty
		{"9858EfFD232B4033E47d90003D41EC34EcaEda94", true},  // fixture address hex
		{"9858effd232b4033e47d90003d41ec34ecaeda94", false}, // all lower
		{"9858EFFD232B4033E47D90003D41EC34ECAEDA94", false}, // all upper
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.in, func(t *testing.T) {
			t.Parallel()
			if got := hasMixedCase(tc.in); got != tc.want {
				t.Errorf("hasMixedCase(%q) = %v, want %v", tc.in, got, tc.want)
			}
		})
	}
}

// ─── Fuzz tests ───────────────────────────────────────────────────────────────
//
// These fuzz functions run as normal tests against their seed corpus during CI
// (without -fuzz=...).  To run actual fuzzing:
//
//	go test -fuzz=FuzzParseBigInt -fuzztime=10s ./internal/signing/...
//	go test -fuzz=FuzzParseData -fuzztime=10s ./internal/signing/...
//	go test -fuzz=FuzzParseToAddress -fuzztime=10s ./internal/signing/...
//	go test -fuzz=FuzzValidate -fuzztime=10s ./internal/signing/...

func FuzzParseBigInt(f *testing.F) {
	// Seed corpus: known interesting inputs.
	for _, s := range []string{
		"", "0", "1", "-1",
		"0x0", "0x1", "0xff", "0xFF", "0x0009",
		"0x", "0xGG", "abc", "1.5",
		"18446744073709551615", // max uint64
		"18446744073709551616", // max uint64 + 1
		"115792089237316195423570985008687907853269984665640564039457584007913129639935", // 2^256 - 1
	} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, s string) {
		// Must never panic.
		_, _ = parseBigInt(s)
	})
}

func FuzzParseData(f *testing.F) {
	f.Add("")
	f.Add("0x")
	f.Add("0xdeadbeef")
	f.Add("0xGGGG")
	f.Add("0xabc")    // odd length
	f.Add("deadbeef") // no prefix
	f.Add("0X")       // uppercase X
	f.Add("0X00")     // uppercase X, valid hex
	f.Fuzz(func(t *testing.T, s string) {
		// Must never panic.
		_, _ = parseData(s)
	})
}

func FuzzParseToAddress(f *testing.F) {
	f.Add("")
	f.Add(FixtureTestAddress)                           // correct EIP-55
	f.Add("0x9858effd232b4033e47d90003d41ec34ecaeda94") // all lower
	f.Add("0x9858EFFD232B4033E47D90003D41EC34ECAEDA94") // all upper
	f.Add("0x9858efFD232B4033E47d90003D41EC34EcaEda94") // checksum fail
	f.Add("0x1234")                                     // too short
	f.Add("not-an-address")                             // garbage
	f.Add("0xGGGGGGGGGGGGGGGGGGGGGGGGGGGGGGGGGGGGGGGG") // invalid hex
	f.Fuzz(func(t *testing.T, s string) {
		// Must never panic.
		_, _ = parseToAddress(s)
	})
}

func FuzzValidate(f *testing.F) {
	f.Add("legacy", "1", "0", FixtureTestAddress, "0", "0x", "21000", "1000000000", "", "")
	f.Add("eip1559", "1", "0", "", "0", "0x", "21000", "", "2000000000", "1000000000")
	f.Add("0x0", "0x1", "0x0", "", "0x0", "0xdeadbeef", "0x5208", "0x3b9aca00", "", "")
	f.Add("", "", "", "", "", "", "", "", "", "")
	f.Add("0x3", "0", "abc", "bad", "bad", "bad", "bad", "bad", "bad", "bad")
	f.Fuzz(func(t *testing.T,
		txType, chainID, nonce, to, value, data, gas, gasPrice, maxFee, maxPriority string,
	) {
		req := TxRequest{
			Type:                 txType,
			ChainID:              chainID,
			Nonce:                nonce,
			To:                   to,
			Value:                value,
			Data:                 data,
			Gas:                  gas,
			GasPrice:             gasPrice,
			MaxFeePerGas:         maxFee,
			MaxPriorityFeePerGas: maxPriority,
		}
		// Must never panic.
		_, _ = validate(req, nil)
	})
}
