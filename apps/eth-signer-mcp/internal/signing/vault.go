package signing

import (
	"context"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
)

// KeyVault provides access to a keystore-backed signing key.
//
// Lifecycle contract (locked):
//   - The keystore JSON is a boot-time snapshot — read eagerly at vault
//     construction. Any I/O or JSON-parse error causes immediate failure (fail fast).
//     The top-level "address" field is optional (per Web3 Secret Storage spec);
//     if absent/empty the vault's address is unknown until the first successful
//     WithSigningKey call discovers it from the decrypted key.
//   - The password file is re-read on every WithSigningKey call, so password
//     rotation works without restarting the process.
//   - Rotating the keystore file itself requires a restart; the snapshot read at
//     construction is authoritative until then.
//   - AddressPointer() is the authoritative nil-vs-non-nil indicator for address
//     discovery. Callers that need to distinguish "address unknown" from "address
//     known" must use AddressPointer(), not Address().
type KeyVault interface {
	// Address returns the account address from the boot-time keystore snapshot
	// (zero if the optional top-level "address" field was absent/empty at construction).
	// For such keystores it is updated to the real value on first successful
	// WithSigningKey (best-effort visibility to concurrent readers). Safe to log;
	// does NOT read the password file.
	Address() common.Address

	// AddressPointer returns a pointer to the discovered or declared address, or
	// nil if the address is not yet known (optional-address keystore before first
	// successful WithSigningKey). A non-nil return is the discovered address.
	//
	// Nil means "not yet discovered"; non-nil means "known". Phase-1 immutable-once-
	// known semantics: one-way transition nil → non-nil (never goes back to nil once set).
	AddressPointer() *common.Address

	// WithSigningKey re-reads the password file, decrypts the keystore snapshot
	// (serialised by an internal semaphore of 1; ctx is checked both before and
	// immediately after acquiring the semaphore, before the KDF starts), hands a
	// sealed SigningKey to fn, and best-effort zeroes all secret material before
	// returning — including on panic. The SigningKey MUST NOT escape fn.
	//
	// Error mapping:
	//   - ctx cancelled before or after acquiring the semaphore → ctx.Err() (not a ToolError)
	//   - missing/unreadable password file  → *ToolError{Code: CodePasswordError}
	//   - keystore.ErrDecrypt (wrong password) → *ToolError{Code: CodePasswordError}
	WithSigningKey(ctx context.Context, fn func(SigningKey) error) error
}

// SigningKey is the sealed interface passed to the WithSigningKey callback.
// ADR-003: exactly two methods are exposed; the underlying *ecdsa.PrivateKey is
// never accessible outside fn. No type assertion yields the raw key.
//
// NOTE: the name "SigningKey" is mandated by the architecture public API
// (plan/architecture.md §Public API surface). The "signing.SigningKey" stutter
// is intentional and accepted; the nolint suppresses the revive exported rule.
//
//nolint:revive // name is locked by the architecture public API contract
type SigningKey interface {
	// Address returns the Ethereum address corresponding to this signing key.
	Address() common.Address
	// SignTx signs the given transaction with this key using the provided signer.
	SignTx(tx *types.Transaction, signer types.Signer) (*types.Transaction, error)
}

// VaultOptions carries the file paths needed to construct a FileKeyVault.
type VaultOptions struct {
	// KeystorePath is the path to the Web3 Secret Storage keystore JSON file.
	// The file is read eagerly at construction (boot-time snapshot). A missing,
	// unreadable or malformed keystore returns an error at construction time.
	// The top-level "address" field is optional.
	KeystorePath string

	// PasswordPath is the path to the password file. It is re-read inside every
	// WithSigningKey call to support password rotation without restart.
	// The constructor does NOT read this file.
	PasswordPath string
}

// NewFileKeyVault constructs a KeyVault backed by a keystore file on disk.
//
// It reads the keystore file eagerly and holds the JSON ciphertext snapshot
// in memory. The top-level "address" is optional per spec; if absent or empty
// the initial Address() returns the zero address (discovery on first decrypt).
// The password file is NOT read at construction.
//
// Error codes:
//   - *ToolError{Code: CodeKeystoreError} — missing/unreadable/malformed file.
func NewFileKeyVault(opts VaultOptions) (KeyVault, error) {
	return newFileKeyVault(opts)
}
