package signing

import (
	"encoding/json"
	"os"

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
type fileKeyVault struct {
	keystoreJSON []byte                       // ciphertext snapshot; safe to hold long-term
	passwordPath string                       // path re-read on every WithSigningKey call
	address      common.Address               // boot snapshot (or zero if absent); overwritten on first successful decrypt with the key's true address (see decrypt.go). Readers observe best-effort (may briefly see zero or stale during first discovery on optional-addr keystores); the sem serializes writers. Analogous to ADR-009 best-effort docs.
	sem          chan struct{}                // capacity 1 — send to acquire, receive to release
	readFileFn   func(string) ([]byte, error) // normally os.ReadFile; injectable per-instance for tests
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

	// Top-level "address" is optional per the Web3 Secret Storage spec (ethereum.org
	// notes it is "unnecessary and compromises privacy"; official vectors omit it).
	// If absent or "", store zero address; discovery happens on first successful
	// WithSigningKey (see decrypt.go).
	addr := common.Address{}
	if ks.Address != "" {
		// common.HexToAddress accepts both checksummed and lowercase hex; it handles
		// the optional "0x" prefix. The vault exposes the canonical checksummed form
		// via Address().Hex() so all callers work with EIP-55 addresses.
		addr = common.HexToAddress(ks.Address)
	}

	// sem is a buffered channel of capacity 1; sending acquires the semaphore (blocks
	// if full), receiving releases it. Initialised empty so the first caller proceeds
	// immediately without a pre-send.
	sem := make(chan struct{}, 1)

	return &fileKeyVault{
		keystoreJSON: data,
		passwordPath: opts.PasswordPath,
		address:      addr,
		sem:          sem,
		readFileFn:   os.ReadFile,
	}, nil
}

// Address returns the Ethereum address from the boot snapshot (or zero for
// optional absent top-level field) or the value discovered on first decrypt.
// Visibility of the one-time lazy write is best-effort (see struct comment);
// safe to log, no password required.
func (v *fileKeyVault) Address() common.Address {
	return v.address
}
