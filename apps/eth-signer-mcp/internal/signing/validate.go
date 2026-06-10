// Package signing — validate.go — Issue 2.4.
// Input validation for sign_transaction: runs entirely before key material is touched.
package signing

import (
	"encoding/hex"
	"errors"
	"math/big"
	"strings"

	"github.com/ethereum/go-ethereum/common"
)

// maxDataBytes is the maximum decoded length of the data field: 256 KiB (262,144 bytes).
// Callers that reach the decoded-length check have already confirmed the 0x prefix and
// even hex length, so len(decoded) is the canonical measure.
const maxDataBytes = 256 * 1024 // 262,144

// parsedTx is the normalised intermediate representation produced by validate and
// consumed by build.go (Issue 2.5).  All string inputs have been parsed into their
// canonical Go types; every rule check has passed.
//
// Fields are unexported: parsedTx is an internal handoff struct used only within
// the signing package.
type parsedTx struct {
	txType    uint8    // 0 (legacy/EIP-155) or 2 (EIP-1559)
	chainID   *big.Int // non-nil and > 0 (guaranteed by validate)
	nonce     uint64
	gas       uint64
	to        *common.Address // nil → contract creation
	value     *big.Int        // non-nil; zero is valid
	data      []byte          // non-nil; []byte{} for "0x" → RLP encodes as 0x80
	gasPrice  *big.Int        // non-nil for txType 0; nil for txType 2
	gasTipCap *big.Int        // non-nil for txType 2 (maxPriorityFeePerGas); nil for type 0
	gasFeeCap *big.Int        // non-nil for txType 2 (maxFeePerGas); nil for type 0
}

// validate parses and validates a TxRequest, returning a normalised *parsedTx on
// success or a *ToolError{Code: …} on the first rule violation.
//
// The guard parameter carries the chain-id guard from the Signer constructor (Issue 2.6).
// Pass nil for no guard.  The guard value is NOT stored here — its sole owner is the
// Signer.
//
// Rule order (LOCKED — ordering is asserted by dedicated tests; do not reorder):
//
//  1. chainId parse failure or chainId == 0            → invalid_input
//     (chainId = 0 selects the replay-unprotected Homestead signer via
//     LatestSignerForChainID; we reject it explicitly to prevent silent footguns.)
//  2. guard != nil && chainId != *guard                → chain_id_mismatch
//  3. type ∉ {"0x0","legacy","0x2","eip1559"}          → unsupported_type
//     (Types 1, 3, 4 are P2; any other string is unsupported.)
//  4. required fields absent per type                  → invalid_input
//     (always required: nonce, gas, value;
//     legacy-only: gasPrice; 1559-only: maxFeePerGas, maxPriorityFeePerGas)
//  5. type-inappropriate fields present                → invalid_input
//     (gasPrice on type 2; maxFeePerGas/maxPriorityFeePerGas on type 0)
//  6. non-empty accessList                             → invalid_input
//  7. numeric fields fail decimal / 0x-hex parse,
//     or gas / nonce exceed uint64 range               → invalid_input
//  8. data: not 0x-prefixed even hex or > 256 KiB     → invalid_input
//     ("0x" → []byte{}, non-nil → RLP encodes as 0x80)
//  9. to: malformed address or EIP-55 checksum mismatch→ invalid_input
//     (empty/absent to → nil address = contract creation;
//     all-lower / all-upper accepted checksum-agnostic;
//     mixed-case must pass EIP-55 checksum via common.HexToAddress(to).Hex())
//
// Double-coverage note: the JSON schema inferred from TxRequest (Issue 2.3) enforces
// additionalProperties:false and the required-field set at the SDK layer.  However,
// because google/jsonschema-go v0.4.3 struct tags are description-only, the schema
// does NOT enforce hex/decimal patterns, maxLength on data, or the EIP-55 rule.
// validate is therefore the SOLE authoritative enforcer of those constraints.
// Schema rejection (when it fires) is a bonus UX layer, not the contract.
//
// Error messages are static and NEVER echo raw input field values.  A caller-supplied
// secret (e.g. calldata embedding a key scalar) must not be reflectable into logs or
// the wire response.
func validate(req TxRequest, guard *uint64) (*parsedTx, *ToolError) {
	// ── Rule 1: parse chainId ────────────────────────────────────────────────
	//
	// A missing chainId is caught here (empty string fails parseBigInt).
	// chainId == 0 is rejected explicitly: LatestSignerForChainID(0) silently
	// falls back to the replay-unprotected Homestead signer — an unacceptable
	// footgun regardless of caller intent.
	if req.ChainID == "" {
		return nil, &ToolError{
			Code:    CodeInvalidInput,
			Message: "chainId: field is required",
		}
	}
	chainID, err := parseBigInt(req.ChainID)
	if err != nil {
		return nil, &ToolError{
			Code:    CodeInvalidInput,
			Message: "chainId: must be a decimal or 0x-hex integer",
		}
	}
	if chainID.Sign() == 0 {
		return nil, &ToolError{
			Code:    CodeInvalidInput,
			Message: "chainId: must not be zero (would select the replay-unprotected Homestead signer)",
		}
	}

	// ── Rule 2: chain-id guard ───────────────────────────────────────────────
	//
	// Runs before type and required-field checks so that a misconfigured client
	// sees chain_id_mismatch even on an otherwise-invalid request (ordering test
	// in validate_test.go asserts this property).
	if guard != nil {
		if chainID.Cmp(new(big.Int).SetUint64(*guard)) != 0 {
			return nil, &ToolError{
				Code:    CodeChainIDMismatch,
				Message: "chainId: does not match the chain-id guard configured on the signer",
			}
		}
	}

	// ── Rule 3: transaction type ─────────────────────────────────────────────
	txType, typeErr := parseTxType(req.Type)
	if typeErr != nil {
		return nil, typeErr
	}

	// ── Rule 4: required field presence ─────────────────────────────────────
	//
	// chainId is already checked in rule 1.  Remaining always-required fields:
	// nonce, gas, value.  Type-specific: gasPrice (legacy), maxFeePerGas and
	// maxPriorityFeePerGas (1559).
	if req.Nonce == "" {
		return nil, &ToolError{Code: CodeInvalidInput, Message: "nonce: field is required"}
	}
	if req.Gas == "" {
		return nil, &ToolError{Code: CodeInvalidInput, Message: "gas: field is required"}
	}
	if req.Value == "" {
		return nil, &ToolError{Code: CodeInvalidInput, Message: "value: field is required"}
	}
	switch txType {
	case 0:
		if req.GasPrice == "" {
			return nil, &ToolError{Code: CodeInvalidInput, Message: "gasPrice: required for legacy (type 0) transactions"}
		}
	case 2:
		if req.MaxFeePerGas == "" {
			return nil, &ToolError{Code: CodeInvalidInput, Message: "maxFeePerGas: required for EIP-1559 (type 2) transactions"}
		}
		if req.MaxPriorityFeePerGas == "" {
			return nil, &ToolError{Code: CodeInvalidInput, Message: "maxPriorityFeePerGas: required for EIP-1559 (type 2) transactions"}
		}
	}

	// ── Rule 5: type-inappropriate fields ───────────────────────────────────
	//
	// Reject fields that belong to the other transaction type.  This prevents
	// silent confusion where, e.g., a 1559 request also carries a gasPrice that
	// would be ignored — a likely client-side bug.
	switch txType {
	case 2:
		if req.GasPrice != "" {
			return nil, &ToolError{Code: CodeInvalidInput, Message: "gasPrice: not applicable for EIP-1559 (type 2) transactions"}
		}
	case 0:
		if req.MaxFeePerGas != "" {
			return nil, &ToolError{Code: CodeInvalidInput, Message: "maxFeePerGas: not applicable for legacy (type 0) transactions"}
		}
		if req.MaxPriorityFeePerGas != "" {
			return nil, &ToolError{Code: CodeInvalidInput, Message: "maxPriorityFeePerGas: not applicable for legacy (type 0) transactions"}
		}
	}

	// ── Rule 6: accessList ───────────────────────────────────────────────────
	//
	// The accessList field is present in the schema (additionalProperties:false
	// requires it to be declared) but non-empty lists are not supported in v1.
	if len(req.AccessList) > 0 {
		return nil, &ToolError{
			Code:    CodeInvalidInput,
			Message: "accessList: non-empty access lists are not supported in v1",
		}
	}

	// ── Rule 7: parse numeric fields ─────────────────────────────────────────
	//
	// All numeric fields accept decimal ("9") and 0x-hex ("0x9", "0x0009").
	// Leading zeros in hex normalise to the canonical value.
	// gas and nonce must fit in uint64 (they index the Ethereum gas and nonce
	// registers; values > 2^64-1 are not encodeable in the tx RLP).
	nonce, err := parseUint64(req.Nonce)
	if err != nil {
		return nil, &ToolError{
			Code:    CodeInvalidInput,
			Message: "nonce: must be a decimal or 0x-hex integer fitting in uint64",
		}
	}
	gas, err := parseUint64(req.Gas)
	if err != nil {
		return nil, &ToolError{
			Code:    CodeInvalidInput,
			Message: "gas: must be a decimal or 0x-hex integer fitting in uint64",
		}
	}
	value, err := parseBigInt(req.Value)
	if err != nil {
		return nil, &ToolError{
			Code:    CodeInvalidInput,
			Message: "value: must be a decimal or 0x-hex integer",
		}
	}

	var gasPrice, gasTipCap, gasFeeCap *big.Int
	switch txType {
	case 0:
		gasPrice, err = parseBigInt(req.GasPrice)
		if err != nil {
			return nil, &ToolError{
				Code:    CodeInvalidInput,
				Message: "gasPrice: must be a decimal or 0x-hex integer",
			}
		}
	case 2:
		gasTipCap, err = parseBigInt(req.MaxPriorityFeePerGas)
		if err != nil {
			return nil, &ToolError{
				Code:    CodeInvalidInput,
				Message: "maxPriorityFeePerGas: must be a decimal or 0x-hex integer",
			}
		}
		gasFeeCap, err = parseBigInt(req.MaxFeePerGas)
		if err != nil {
			return nil, &ToolError{
				Code:    CodeInvalidInput,
				Message: "maxFeePerGas: must be a decimal or 0x-hex integer",
			}
		}
	}

	// ── Rule 8: data field ───────────────────────────────────────────────────
	data, dataErr := parseData(req.Data)
	if dataErr != nil {
		return nil, dataErr
	}

	// ── Rule 9: to field (EIP-55 checksum) ───────────────────────────────────
	to, toErr := parseToAddress(req.To)
	if toErr != nil {
		return nil, toErr
	}

	return &parsedTx{
		txType:    txType,
		chainID:   chainID,
		nonce:     nonce,
		gas:       gas,
		to:        to,
		value:     value,
		data:      data,
		gasPrice:  gasPrice,
		gasTipCap: gasTipCap,
		gasFeeCap: gasFeeCap,
	}, nil
}

// parseTxType returns the numeric transaction type (0 or 2) for the accepted type
// strings, or a *ToolError{Code: CodeUnsupportedType} for any other value.
//
// Accepted (exact, case-sensitive):
//   - "0x0" or "legacy" → type 0 (EIP-155 legacy)
//   - "0x2" or "eip1559" → type 2 (EIP-1559)
//
// Types 1, 3, and 4 are planned for a future phase; any other string —
// including the empty string — returns unsupported_type.
func parseTxType(s string) (uint8, *ToolError) {
	switch s {
	case "0x0", "legacy":
		return 0, nil
	case "0x2", "eip1559":
		return 2, nil
	default:
		return 0, &ToolError{
			Code:    CodeUnsupportedType,
			Message: `type: unsupported transaction type; accepted values are "0x0", "legacy", "0x2", "eip1559"`,
		}
	}
}

// parseBigInt parses a decimal or 0x-prefixed hex string into a non-negative *big.Int.
//
//   - Empty string → error.
//   - "0x" alone (no hex digits after the prefix) → error.
//   - "0x..." hex (leading zeros accepted, e.g. "0x0009" → 9).
//   - Decimal string ("-1" or any negative) → error.
//   - Any other non-numeric content → error.
//
// This function is a building block for validate; callers map its error to a
// static *ToolError message (the error message from parseBigInt itself is not
// surfaced to callers or the wire).
func parseBigInt(s string) (*big.Int, error) {
	if s == "" {
		return nil, errors.New("empty string")
	}
	n := new(big.Int)
	var ok bool
	if strings.HasPrefix(s, "0x") || strings.HasPrefix(s, "0X") {
		hexDigits := s[2:]
		if hexDigits == "" {
			return nil, errors.New("hex number has no digits after the 0x prefix")
		}
		_, ok = n.SetString(hexDigits, 16)
	} else {
		_, ok = n.SetString(s, 10)
	}
	if !ok {
		return nil, errors.New("not a valid integer")
	}
	if n.Sign() < 0 {
		return nil, errors.New("negative value not allowed")
	}
	return n, nil
}

// parseUint64 parses a decimal or 0x-hex string into a uint64.
// Returns an error if parseBigInt fails or the value exceeds 2^64-1.
func parseUint64(s string) (uint64, error) {
	n, err := parseBigInt(s)
	if err != nil {
		return 0, err
	}
	if !n.IsUint64() {
		return 0, errors.New("value exceeds uint64 range")
	}
	return n.Uint64(), nil
}

// parseData validates and decodes the data field.
//
// Rules:
//   - Must start with "0x" or "0X".
//   - Must have an even number of hex digits after the prefix.
//   - Decoded byte length must be ≤ maxDataBytes (256 KiB = 262,144 bytes).
//   - "0x" decodes to []byte{} (non-nil), so RLP encodes it as 0x80 (empty string),
//     not 0xc0 (empty list) — matching Ethereum's wire encoding for empty calldata.
//
// Returns *ToolError{Code: CodeInvalidInput} on any violation.
func parseData(s string) ([]byte, *ToolError) {
	if len(s) < 2 || s[0] != '0' || (s[1] != 'x' && s[1] != 'X') {
		return nil, &ToolError{
			Code:    CodeInvalidInput,
			Message: "data: must be a 0x-prefixed even-length hex string",
		}
	}
	hexPart := s[2:]
	if hexPart == "" {
		// "0x" → empty data; non-nil []byte{} ensures correct RLP encoding (0x80).
		return []byte{}, nil
	}
	if len(hexPart)%2 != 0 {
		return nil, &ToolError{
			Code:    CodeInvalidInput,
			Message: "data: hex string must have even length (each byte is two hex digits)",
		}
	}
	b, decErr := hex.DecodeString(hexPart)
	if decErr != nil {
		return nil, &ToolError{
			Code:    CodeInvalidInput,
			Message: "data: contains invalid hex characters",
		}
	}
	if len(b) > maxDataBytes {
		return nil, &ToolError{
			Code:    CodeInvalidInput,
			Message: "data: decoded length exceeds the 256 KiB limit",
		}
	}
	return b, nil
}

// parseToAddress validates the to field and applies the EIP-55 mixed-case checksum rule.
//
// Rules:
//   - Empty string → nil (contract creation; valid for both tx types).
//   - Must be exactly 42 characters (0x/0X prefix + 40 hex chars = 20 bytes).
//   - common.IsHexAddress must pass (valid hex, correct length).
//   - All-lowercase and all-uppercase addresses are accepted checksum-agnostic.
//   - Mixed-case (contains both upper- and lower-case hex letters a–f / A–F) must
//     match the EIP-55 checksum: compare against common.HexToAddress(to).Hex().
//
// Returns *ToolError{Code: CodeInvalidInput} on any violation.
func parseToAddress(s string) (*common.Address, *ToolError) {
	if s == "" {
		return nil, nil // contract creation
	}
	// Require the 0x/0X prefix explicitly.  common.IsHexAddress also accepts
	// unprefixed 40-char hex; we enforce the prefix by checking len == 42.
	// (40 hex chars + 2-char prefix = 42.)
	if len(s) != 42 || !common.IsHexAddress(s) {
		return nil, &ToolError{
			Code:    CodeInvalidInput,
			Message: "to: malformed address; expected 0x-prefixed 20-byte hex (42 chars)",
		}
	}
	// hexPart is the 40-character hex portion after the "0x"/"0X" prefix.
	hexPart := s[2:]

	// EIP-55 rule: apply only when the address contains both upper- and lower-case
	// hex letters (a–f / A–F).  All-lowercase and all-uppercase are accepted as-is.
	if hasMixedCase(hexPart) {
		// common.HexToAddress(s).Hex() returns the canonical EIP-55 form:
		//   "0x" (lowercase) + checksummed 40-char hex.
		canonical := common.HexToAddress(s).Hex()
		// Normalise the input prefix to lowercase "0x" for comparison
		// (0X-prefixed inputs are otherwise always rejected by the string equality).
		normalized := "0x" + hexPart
		if canonical != normalized {
			return nil, &ToolError{
				Code:    CodeInvalidInput,
				Message: "to: EIP-55 checksum mismatch; use a correctly checksummed address, all-lowercase, or all-uppercase",
			}
		}
	}

	addr := common.HexToAddress(s)
	return &addr, nil
}

// hasMixedCase reports whether a hex string (without the "0x" prefix) contains
// both upper-case (A–F) and lower-case (a–f) hex letters.
//
// Returns false for:
//   - Strings with no hex letters (pure digit strings like "1234567890...").
//   - All-lowercase hex strings.
//   - All-uppercase hex strings.
//   - The empty string.
//
// Returns true only when BOTH upper- and lower-case hex letters are present,
// meaning EIP-55 checksum verification is applicable.
func hasMixedCase(s string) bool {
	hasLower, hasUpper := false, false
	for _, c := range s {
		switch {
		case c >= 'a' && c <= 'f':
			hasLower = true
		case c >= 'A' && c <= 'F':
			hasUpper = true
		}
		if hasLower && hasUpper {
			return true
		}
	}
	return false
}
