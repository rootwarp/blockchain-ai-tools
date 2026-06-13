package signing

import (
	"encoding/json"
	"fmt"
	"os"
	"sync/atomic"

	"github.com/ethereum/go-ethereum/common"
)

// fileKeyVault is a KeyVault backed by a keystore file on disk.
//
// Design invariants (lifecycle contract — locked):
//   - keystoreJSON holds the raw keystore ciphertext read at construction.
//     It is safe to keep in memory indefinitely because it is encrypted;
//     the key material is never exposed without the password.
//   - Keystore-file rotation mid-run is NOT detected by design. The snapshot
//     read at construction is authoritative until the process restarts.
//   - The password file is re-read on every WithSigningKey call so password
//     rotation works without restarting the process.
//   - sem is a channel-based semaphore of capacity 1 that serialises decryptions.
//     Only one WithSigningKey call may run the KDF at a time; concurrent callers
//     wait in the select on ctx.Done() vs sem, so they respect cancellation.
//   - address is synchronised via atomic.Pointer (see field comment). Nil
//     sentinel means no address has been discovered yet (optional-address
//     keystore, pre-first-decrypt). Immutable once set; all reads and writes
//     go through Load/Store/CompareAndSwap — no bare field access.
type fileKeyVault struct {
	keystoreJSON []byte                         // ciphertext snapshot; safe to hold long-term
	passwordPath string                         // path re-read on every WithSigningKey call
	address      atomic.Pointer[common.Address] // synchronised via atomic.Pointer; nil = not yet discovered. Immutable once known: discover-only write in decrypt.go uses CompareAndSwap(nil, &addr). Declared address stored at construction; nil for optional-address keystores until first successful decrypt.
	sem          chan struct{}                  // capacity 1 — send to acquire, receive to release
	readFileFn   func(string) ([]byte, error)   // normally os.ReadFile; injectable per-instance for tests
}

// keystoreAddressOnly is used solely to extract the top-level "address" field from
// a Web3 Secret Storage v3 keystore JSON document. All other fields are ignored at
// construction time; the full JSON is stored as-is in keystoreJSON for decryption.
type keystoreAddressOnly struct {
	Address string `json:"address"`
}

// newFileKeyVault is the internal constructor called by NewFileKeyVault (defined in
// vault.go). Separating the declaration from the entry point keeps vault.go clean and
// allows internal tests to call either form.
func newFileKeyVault(opts VaultOptions) (*fileKeyVault, error) {
	// Read the keystore file eagerly — boot-time snapshot, fail fast.
	data, err := os.ReadFile(opts.KeystorePath)
	if err != nil {
		return nil, &ToolError{
			Code:    CodeKeystoreError,
			Message: "keystore file could not be read",
			Cause:   err,
		}
	}

	// Parse only the top-level "address" field; the full JSON is stored for later
	// decryption. json.Unmarshal on a small struct is cheap and catches malformed JSON.
	var ks keystoreAddressOnly
	if err := json.Unmarshal(data, &ks); err != nil {
		return nil, &ToolError{
			Code:    CodeKeystoreError,
			Message: "keystore JSON is malformed",
			Cause:   err,
		}
	}

	// sem is a buffered channel of capacity 1; sending acquires the semaphore (blocks
	// if full), receiving releases it. Initialised empty so the first caller proceeds
	// immediately without a pre-send.
	sem := make(chan struct{}, 1)

	v := &fileKeyVault{
		keystoreJSON: data,
		passwordPath: opts.PasswordPath,
		sem:          sem,
		readFileFn:   os.ReadFile,
	}

	// Top-level "address" is optional per the Web3 Secret Storage spec (ethereum.org
	// notes it is "unnecessary and compromises privacy"; official vectors omit it).
	// If present and non-empty, validate it strictly and store it as the declared address
	// (the pointer becomes non-nil and immutable — discover-only writes in decrypt.go
	// will no-op via CAS). If absent or "", leave the pointer nil; discovery happens on
	// first successful WithSigningKey (see decrypt.go).
	if ks.Address != "" {
		parsed, valErr := validateKeystoreAddressField(ks.Address)
		if valErr != nil {
			return nil, &ToolError{
				Code:    CodeKeystoreError,
				Message: "keystore top-level \"address\" field is malformed",
				Cause:   valErr,
			}
		}
		v.address.Store(&parsed)
	}

	return v, nil
}

// validateKeystoreAddressField validates the top-level "address" field of a Web3
// Secret Storage keystore JSON document and returns the parsed address.
//
// Accepted forms (per Web3 Secret Storage spec and geth tooling):
//   - Web3 standard: 40 lowercase hex chars WITHOUT prefix: "9858effd..."
//     (this is the format produced by geth and most wallet tooling)
//   - 0x-prefixed all-lowercase hex: "0xabcdef..."
//   - 0x-prefixed all-uppercase hex: "0xABCDEF..."
//   - 0x-prefixed EIP-55 mixed-case (must pass checksum): "0x9858EfFD..."
//
// Rejected forms:
//   - Short or long strings that fail common.IsHexAddress
//   - Non-hex characters
//   - 0x-prefixed mixed-case that fails the EIP-55 checksum
//   - Strings that are neither a valid 40-char bare hex nor a valid 42-char
//     0x-prefixed hex (e.g. "0x" + 39 chars, garbage prefixes, etc.)
//
// Returns (addr, nil) on success; (common.Address{}, error) on failure.
// Error messages are static and never echo the input value.
func validateKeystoreAddressField(s string) (common.Address, error) {
	// common.IsHexAddress accepts both "0x"-prefixed 42-char and bare 40-char hex.
	// We require exactly one of the two canonical lengths:
	//   - 40 chars: Web3 standard bare hex (no prefix) — all-lowercase or all-uppercase
	//   - 42 chars: 0x-prefixed hex
	if !common.IsHexAddress(s) {
		return common.Address{}, fmt.Errorf("address field contains invalid hex characters or wrong length")
	}

	// Determine the hex portion (without any prefix) for mixed-case checking.
	var hexPart string
	if len(s) == 42 {
		hexPart = s[2:] // "0x" + 40 hex
	} else {
		hexPart = s // bare 40-char hex (Web3 standard)
	}

	// EIP-55 rule: apply only when the address contains both upper- and lower-case
	// hex letters. All-lowercase and all-uppercase are accepted checksum-agnostic.
	// The EIP-55 checksum is defined over the 40 hex chars regardless of prefix.
	if hasMixedCase(hexPart) {
		// common.HexToAddress(s).Hex() always returns "0x" + EIP-55 checksummed 40 hex.
		canonical := common.HexToAddress(s).Hex()
		// Normalize both to "0x" + hexPart for comparison.
		normalized := "0x" + hexPart
		if canonical != normalized {
			return common.Address{}, fmt.Errorf("mixed-case address field does not pass EIP-55 checksum")
		}
	}

	return common.HexToAddress(s), nil
}

// Address returns the discovered or declared address, or the zero address if
// discovery has not yet occurred. Safe to log; no password required.
// Reads are race-clean (synchronised via the field's atomic pointer).
func (v *fileKeyVault) Address() common.Address {
	if p := v.address.Load(); p != nil {
		return *p
	}
	return common.Address{}
}

// AddressPointer returns a pointer to the discovered or declared address, or
// nil if discovery has not yet occurred. Non-nil means the address is known;
// nil means the optional-address keystore has not yet completed a first
// successful WithSigningKey call. Reads are race-clean (atomic.Pointer).
func (v *fileKeyVault) AddressPointer() *common.Address {
	return v.address.Load()
}
