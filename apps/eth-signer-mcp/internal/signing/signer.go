// Package signing — signer.go — Issue 2.6.
// Signer orchestration: validate → build → vault.WithSigningKey → SignTx →
// defensive sender-check → encode SignResult → emit audit line.
package signing

import (
	"context"
	"encoding/hex"
	"fmt"
	"log/slog"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/common/hexutil"
	"github.com/ethereum/go-ethereum/core/types"
)

// SignerOptions carries the construction-time configuration for a Signer.
// The ChainIDGuard is the ONLY home of the chain-id guard — it is stored here
// at construction and passed to validate() on every call. No per-request guard
// field exists anywhere in the codebase (locked architecture decision).
type SignerOptions struct {
	// ChainIDGuard, when non-nil, requires every signing request to carry a
	// chainId equal to *ChainIDGuard. A mismatch → chain_id_mismatch before the
	// vault is ever touched. Wired by cmd from --chain-id; nil means no guard.
	ChainIDGuard *uint64

	// Logger is the slog.Logger used for the per-signing audit line and for
	// logging redacted information about recovered panics. If nil, slog.Default()
	// is used. Injected here so callers can plug in the shared application logger
	// without importing internal/obs from the signing package.
	Logger *slog.Logger
}

// Signer is the central orchestrator for signing transactions. It owns the
// chain-id guard (the only copy in the system) and delegates key material
// access entirely to the KeyVault.
//
// SignTransaction is safe for concurrent use: the guard is read-only after
// construction and vault.WithSigningKey serialises KDF calls internally.
// Panic recovery in SignTransaction leaves the Signer fully operational for
// subsequent calls.
type Signer struct {
	vault  KeyVault
	guard  *uint64      // the ONLY copy of the chain-id guard; immutable after NewSigner
	logger *slog.Logger // never nil after construction; falls back to slog.Default()
}

// NewSigner constructs a Signer with the given vault and options. The returned
// Signer is immediately ready for concurrent use.
//
// The logger falls back to slog.Default() when opts.Logger is nil, so callers
// that have not yet configured a logger get working (default) output rather than
// a nil-pointer panic.
func NewSigner(vault KeyVault, opts SignerOptions) *Signer {
	logger := opts.Logger
	if logger == nil {
		logger = slog.Default()
	}
	return &Signer{
		vault:  vault,
		guard:  opts.ChainIDGuard,
		logger: logger,
	}
}

// Address returns the Ethereum address from the vault's boot-time keystore
// snapshot. It is safe to call without a password and without triggering a KDF.
func (s *Signer) Address() common.Address {
	return s.vault.Address()
}

// SignTransaction validates req, builds the unsigned transaction, and delegates
// signing to the vault. On success it returns a fully populated SignResult and
// emits exactly one info-level audit log line.
//
// Error taxonomy (returned as *ToolError):
//   - invalid_input    — validation failure (missing/bad fields, EIP-55, data cap, chainId=0)
//   - unsupported_type — tx type is not 0 or 2
//   - chain_id_mismatch— request chainId differs from the ChainIDGuard
//   - password_error   — password file unreadable or keystore.ErrDecrypt (wrong password)
//   - keystore_error   — vault returns a keystore-level error (typically boot-time, not here)
//   - internal_error   — sender mismatch, panic recovery, non-ErrDecrypt decrypt failure
//
// Non-*ToolError errors (e.g. context.Canceled) are system errors returned as-is.
//
// Panic recovery: if a panic occurs inside vault.WithSigningKey's callback, the
// vault's deferred zeroing fires first (ADR-009), then this function's recover()
// catches the panic, emits a REDACTED log line (the raw panic value is NEVER logged),
// and returns a *ToolError{Code: CodeInternalError}. The Signer remains usable.
func (s *Signer) SignTransaction(ctx context.Context, req TxRequest) (result *SignResult, retErr error) {
	// Retrieve the request ID for the audit line; empty string if not set.
	reqID, _ := RequestIDFromContext(ctx)

	// Panic recovery: must be registered before any code that could panic.
	//
	// Deferred zeroing inside vault.WithSigningKey (ADR-009) fires FIRST because it
	// is registered deeper on the call stack; by the time recover() here catches the
	// panic, the key scalar and password bytes are already zeroed.
	//
	// IMPORTANT: the raw panic value (r) is NEVER logged — it may contain key material
	// or other secret-bearing data. Only a static redacted message is emitted.
	defer func() {
		if r := recover(); r != nil {
			s.logger.Error("signing: panic recovered; key material already zeroed by vault",
				"request_id", reqID,
				// Intentionally no "panic_value" key — raw panic value must not be logged.
			)
			result = nil
			retErr = &ToolError{
				Code:    CodeInternalError,
				Message: "an internal error occurred during signing",
			}
		}
	}()

	// ── Step 1: Validate (vault never touched on any failure here) ────────────
	//
	// s.guard is the ONLY chain-id guard in the system. It is passed here and
	// nowhere else. validate() is not stateful; the guard value is its parameter.
	parsed, toolErr := validate(req, s.guard)
	if toolErr != nil {
		return nil, toolErr
	}

	// ── Step 2: Build the unsigned transaction ───────────────────────────────
	builtTx, gethSigner := buildTx(parsed)

	// ── Step 3: Sign inside the vault ────────────────────────────────────────
	//
	// All of the following executes inside vault.WithSigningKey's callback fn.
	// The vault handles semaphore acquisition, password re-read, KDF, and deferred
	// zeroing of password bytes and key scalar (including on panic paths).
	var signResult *SignResult

	vaultErr := s.vault.WithSigningKey(ctx, func(key SigningKey) error {
		// Sign the transaction.
		signedTx, signErr := key.SignTx(builtTx, gethSigner)
		if signErr != nil {
			return &ToolError{
				Code:    CodeInternalError,
				Message: "transaction signing failed",
				Cause:   signErr,
			}
		}

		// Encode the signed transaction to RLP.
		raw, marshalErr := signedTx.MarshalBinary()
		if marshalErr != nil {
			return &ToolError{
				Code:    CodeInternalError,
				Message: "failed to encode signed transaction to RLP",
				Cause:   marshalErr,
			}
		}

		// Extract signature components.
		// V, R, S as returned by RawSignatureValues:
		//   - type 0 (legacy, EIP-155): V = chainID*2+35 or chainID*2+36
		//   - type 2 (EIP-1559): V = yParity (0 or 1)
		sigV, sigR, sigS := signedTx.RawSignatureValues()

		// Defensive sender recovery: the recovered address must match the vault's
		// boot-time address. A mismatch indicates a key configuration error —
		// the keystore does not match the private key being used — and must never
		// silently produce a result attributed to the wrong address.
		//
		// Both addresses are non-secret (cached/derived public data) and are safe
		// to include in the error message on the wire and in logs.
		sender, senderErr := types.Sender(gethSigner, signedTx)
		if senderErr != nil {
			return &ToolError{
				Code:    CodeInternalError,
				Message: "failed to recover sender address from signed transaction",
				Cause:   senderErr,
			}
		}

		keystoreAddr := s.vault.Address()
		if sender != keystoreAddr {
			// Locked wording (Issue 2.6 spec): name BOTH addresses in the message.
			// Both are public (non-secret) cached/derived addresses.
			return &ToolError{
				Code: CodeInternalError,
				Message: fmt.Sprintf(
					"recovered sender %s does not match keystore address %s",
					sender.Hex(), keystoreAddr.Hex(),
				),
			}
		}

		// Encode SignResult.
		// rawTransaction: "0x" + hex of MarshalBinary output (full RLP).
		// r, s, v: 0x-prefixed hex quantities via hexutil.EncodeBig (no leading zeros).
		// hash: signedTx.Hash().Hex() (0x-prefixed Keccak-256).
		// from: EIP-55 checksummed address via common.Address.Hex().
		signResult = &SignResult{
			RawTransaction: "0x" + hex.EncodeToString(raw),
			Signature: SignatureValues{
				R: hexutil.EncodeBig(sigR),
				S: hexutil.EncodeBig(sigS),
				V: hexutil.EncodeBig(sigV),
			},
			Hash: signedTx.Hash().Hex(),
			From: sender.Hex(), // EIP-55 checksummed
		}
		return nil
	})

	if vaultErr != nil {
		return nil, vaultErr
	}

	// ── Step 4: Audit log (exactly one info-level line per successful signing) ─
	//
	// Locked fields: request_id, tx_hash, chain_id, nonce.
	// The tx body — to, value, calldata — is NEVER logged (may be operator-sensitive).
	s.logger.Info("signing: transaction signed successfully",
		"request_id", reqID,
		"tx_hash", signResult.Hash,
		"chain_id", parsed.chainID.String(),
		"nonce", parsed.nonce,
	)

	return signResult, nil
}
