package signing

// SignatureValues holds the raw ECDSA signature components of a signed
// Ethereum transaction. All values are 0x-prefixed hex strings.
//
// V interpretation (per transaction type):
//   - Type 0 (legacy, EIP-155): V is the EIP-155 replay-protection value,
//     computed as chainID*2+35 or chainID*2+36. For example, mainnet (chainId=1)
//     yields V=37 or V=38.
//   - Type 2 (EIP-1559): V is the yParity value, either 0 or 1.
//
// R and S are 32-byte quantities as returned by go-ethereum's
// types.Transaction.RawSignatureValues(), hex-encoded with 0x prefix.
type SignatureValues struct {
	// R is the 0x-prefixed hex ECDSA r component.
	R string `json:"r"`
	// S is the 0x-prefixed hex ECDSA s component.
	S string `json:"s"`
	// V is the 0x-prefixed hex ECDSA v component.
	// For type 2 (EIP-1559): yParity (0 or 1).
	// For type 0 (legacy, EIP-155): chainID*2+35 or chainID*2+36.
	V string `json:"v"`
}

// SignResult is the typed wire-contract output for the sign_transaction MCP tool.
// It is returned on success and carries all fields from day one — no omitempty
// staging, no later retrofit (locked decision: Phase Assumption §2.3).
//
// All fields are always present in the JSON output regardless of their value.
// The from field is always an EIP-55 checksummed address.
type SignResult struct {
	// RawTransaction is the 0x-prefixed hex-encoded RLP of the signed transaction,
	// ready for broadcast via eth_sendRawTransaction.
	RawTransaction string `json:"rawTransaction"`

	// Signature holds the raw ECDSA signature components.
	Signature SignatureValues `json:"signature"`

	// Hash is the 0x-prefixed Keccak-256 hash of the signed transaction
	// (the transaction ID / txHash).
	// NOTE: NO omitempty — always present in the output (locked decision).
	Hash string `json:"hash"`

	// From is the EIP-55 checksummed sender address recovered from the signature.
	// It always equals the keystore address confirmed at signing time.
	// NOTE: NO omitempty — always present in the output (locked decision).
	From string `json:"from"`
}

// AddressResult is the typed wire-contract output for the get_address MCP tool.
// It returns the EIP-55 checksummed account address from the boot-time keystore
// snapshot without reading the password file.
//
// IMPORTANT: for optional-address keystores before the first successful
// sign_transaction call, the get_address tool returns IsError:true with code
// "address_unknown" instead of returning an AddressResult. Callers MUST inspect
// IsError before treating this type as the result.
type AddressResult struct {
	// Address is the EIP-55 checksummed Ethereum address of the loaded keystore
	// account once known. For optional-address keystores before the first successful
	// sign_transaction, the get_address tool returns IsError:true with code
	// "address_unknown" instead of returning an AddressResult; callers MUST inspect
	// IsError before treating this field as truth.
	Address string `json:"address"`
}
