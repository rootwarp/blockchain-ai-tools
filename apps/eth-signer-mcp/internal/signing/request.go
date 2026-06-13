package signing

// TxRequest is the typed wire-contract input for the sign_transaction MCP tool.
// It carries json and jsonschema tags that drive mcp.AddTool schema inference via
// github.com/google/jsonschema-go. The signing package carries these tags without
// importing the MCP SDK — struct tags are plain strings (ADR-011 superseded).
//
// Accepted numeric encodings (all fields):
//   - Decimal integer string: "1", "21000", "0" (no leading zeros, except "0" itself)
//   - 0x-prefixed hex string: "0x1", "0x5208", "0x0009" (leading zeros normalised by parser)
//
// Per-type applicability:
//   - type 0 / "legacy":   use gasPrice; omit maxFeePerGas, maxPriorityFeePerGas
//   - type 2 / "eip1559":  use maxFeePerGas, maxPriorityFeePerGas; omit gasPrice
//
// TAG SURFACE LIMITATIONS (as of github.com/google/jsonschema-go v0.4.3):
// The jsonschema struct tag is description-only in this library version; it does
// NOT support WORD=value annotations. As a result, the following constraints
// cannot be expressed in the inferred schema and are enforced exclusively in
// validate.go (Issue 2.4):
//   - Hex/decimal patterns (e.g. "^0x[0-9a-fA-F]+$") on numeric/address fields
//   - maxLength for the data field (512 KiB bytes = 524,290 hex chars incl. 0x)
//   - EIP-55 checksum rule on the to field
//
// The end-to-end schema strictness (additionalProperties:false, required set) is
// confirmed by schema_test.go and re-asserted in 2.7/2.11 via tools/list assertions.
type TxRequest struct {
	// Type selects the transaction type.
	// Accepted values: "0x0" or "legacy" (type 0, EIP-155), "0x2" or "eip1559"
	// (type 2, EIP-1559). Types 1, 3, and 4 are not supported in v1 (unsupported_type).
	Type string `json:"type" jsonschema:"Transaction type: \"0x0\" or \"legacy\" (EIP-155) or \"0x2\" or \"eip1559\" (EIP-1559)"`

	// ChainID identifies the network.
	// Accepted: decimal ("1", "11155111") or 0x-hex ("0x1"). Must not be zero
	// (chainId=0 would select the replay-unprotected Homestead signer — rejected).
	ChainID string `json:"chainId" jsonschema:"Network chain ID: decimal or 0x-hex integer; must not be zero"`

	// Nonce is the sender account's transaction count.
	// Accepted: decimal or 0x-hex (leading zeros normalised: \"0x0009\" == \"9\").
	Nonce string `json:"nonce" jsonschema:"Sender nonce: decimal or 0x-hex integer"`

	// To is the recipient address (omit or leave empty for contract creation).
	// Accepted: 0x-prefixed 20-byte hex. Mixed-case addresses must pass EIP-55
	// checksum validation; all-lowercase and all-uppercase are accepted without
	// checksum verification.
	To string `json:"to,omitempty" jsonschema:"Recipient address (0x-prefixed, 20 bytes); omit for contract creation. Mixed-case: must satisfy EIP-55 checksum"`

	// Value is the amount of ether to transfer, in wei.
	// Accepted: decimal or 0x-hex. Zero is valid.
	Value string `json:"value" jsonschema:"Wei value to transfer: decimal or 0x-hex integer; zero allowed"`

	// Data is the ABI-encoded call data (for calls) or init code (for deployment).
	// Accepted: 0x-prefixed even-length hex string. "0x" encodes as zero bytes (RLP 0x80).
	// Decoded byte length must not exceed 256 KiB (262,144 bytes); the corresponding
	// hex representation limit is 524,290 chars (including the 0x prefix).
	//
	// NOTE: maxLength=524290 cannot be expressed in the jsonschema struct tag with
	// google/jsonschema-go v0.4.3 (tag is description-only). Enforced in validate.go.
	Data string `json:"data" jsonschema:"0x-prefixed hex call data or init code; \"0x\" for empty; max 256 KiB decoded"`

	// Gas is the gas limit for the transaction.
	// Accepted: decimal or 0x-hex.
	Gas string `json:"gas" jsonschema:"Gas limit: decimal or 0x-hex integer"`

	// GasPrice is the gas price in wei per gas unit.
	// Applicable to type 0 (legacy) only; must be omitted for type 2.
	// Accepted: decimal or 0x-hex.
	GasPrice string `json:"gasPrice,omitempty" jsonschema:"Gas price in wei (legacy/type-0 only): decimal or 0x-hex integer"`

	// MaxFeePerGas is the maximum total fee per gas (base + priority), in wei.
	// Applicable to type 2 (EIP-1559) only; must be omitted for type 0.
	// Accepted: decimal or 0x-hex.
	MaxFeePerGas string `json:"maxFeePerGas,omitempty" jsonschema:"Max fee per gas in wei (EIP-1559/type-2 only): decimal or 0x-hex integer"`

	// MaxPriorityFeePerGas is the maximum miner tip per gas, in wei.
	// Applicable to type 2 (EIP-1559) only; must be omitted for type 0.
	// Accepted: decimal or 0x-hex.
	MaxPriorityFeePerGas string `json:"maxPriorityFeePerGas,omitempty" jsonschema:"Max priority fee per gas in wei (EIP-1559/type-2 only): decimal or 0x-hex integer"`

	// AccessList is the EIP-2930 access list.
	// Must be empty in v1 (non-empty → invalid_input). Present to satisfy strict
	// schema inference (additionalProperties:false); validation rejects non-empty lists.
	AccessList []struct{} `json:"accessList,omitempty" jsonschema:"EIP-2930 access list; must be empty in v1 (non-empty rejected)"`
}
