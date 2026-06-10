// Package signing — parity_test.go — Issue 2.10.
//
// Byte-identical parity suite: loads every golden vector from
// testdata/vectors/*.json and asserts that our signer produces output that is
// bit-for-bit identical to the reference values produced by ethers v6 (and cast
// where available).
//
// CANARY NOTICE: This suite is the sentinel for go-ethereum RLP drift. If you
// bump the go-ethereum version and any vector starts failing, re-run
// scripts/regen-vectors.sh to regenerate the golden values, then commit the
// updated vectors alongside the go-ethereum version bump. Do NOT paper over a
// mismatch by only updating the expected value in isolation — always verify the
// new value against an independent oracle first.
//
// Vector file format (testdata/vectors/*.json):
//
//	Signing vector   — has "expected" key with raw_tx/tx_hash/r/s/v fields.
//	Rejection vector — has "expected_error" key with an error code string.
//
// Suite assertions for signing vectors:
//  1. result.RawTransaction == expected.raw_tx   (byte-identical; the headline assertion)
//  2. result.Signature.{R,S,V} match expected.r/s/v  (hex-normalised big.Int comparison)
//  3. result.Hash == expected.tx_hash
//  4. result.From == FixtureTestAddress (checksummed)
//  5. types.Transaction.UnmarshalBinary(raw) succeeds; rt.Hash().Hex() == result.Hash
//  6. Independent sender recovery via types.Sender == FixtureTestAddress
//
// Suite assertions for rejection vectors:
//  1. SignTransaction returns a *ToolError with Code == expected_error
//  2. A panicking fake vault is used — asserting the vault is NEVER invoked
//     (the panicking vault would cause the test to fail/panic if called)
//
// Count canary:
//
//	The suite asserts exactly 9 signing vectors and 2 rejection vectors (11 total).
//	A dropped or renamed file causes this assertion to fail loudly.
package signing

import (
	"context"
	"encoding/json"
	"errors"
	"math/big"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
)

// ── Vector schema types ───────────────────────────────────────────────────────

// vectorTx mirrors the "tx" field in both signing and rejection vector files.
// It maps directly to TxRequest fields; all fields are strings to match JSON.
type vectorTx struct {
	Type                 string `json:"type"`
	ChainID              string `json:"chainId"`
	Nonce                string `json:"nonce"`
	To                   string `json:"to,omitempty"`
	Value                string `json:"value"`
	Data                 string `json:"data"`
	Gas                  string `json:"gas"`
	GasPrice             string `json:"gasPrice,omitempty"`
	MaxFeePerGas         string `json:"maxFeePerGas,omitempty"`
	MaxPriorityFeePerGas string `json:"maxPriorityFeePerGas,omitempty"`
}

// vectorExpected holds the reference outputs for a signing vector.
type vectorExpected struct {
	RawTx  string `json:"raw_tx"`
	TxHash string `json:"tx_hash"`
	R      string `json:"r"`
	S      string `json:"s"`
	V      string `json:"v"`
}

// signingVector is the parsed form of a signing vector file.
type signingVector struct {
	Name     string         `json:"name"`
	Tx       vectorTx       `json:"tx"`
	Expected vectorExpected `json:"expected"`
}

// rejectionVector is the parsed form of a rejection vector file.
type rejectionVector struct {
	Name          string   `json:"name"`
	Tx            vectorTx `json:"tx"`
	ExpectedError string   `json:"expected_error"`
}

// rawVectorFile is used to discriminate between signing and rejection vectors
// by checking which top-level keys are present before full unmarshalling.
type rawVectorFile struct {
	Name          string          `json:"name"`
	Tx            vectorTx        `json:"tx"`
	Expected      json.RawMessage `json:"expected"`
	ExpectedError string          `json:"expected_error"`
}

// ── Vector loader ─────────────────────────────────────────────────────────────

// vectorsDir returns the path to testdata/vectors/.
func vectorsDir(t *testing.T) string {
	t.Helper()
	return filepath.Join("testdata", "vectors")
}

// loadAllVectors reads every *.json file in testdata/vectors/ and returns the
// signing and rejection vectors separately.
// It fails the test immediately if any file cannot be read or parsed.
func loadAllVectors(t *testing.T) (signing []signingVector, rejection []rejectionVector) {
	t.Helper()

	dir := vectorsDir(t)
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("loadAllVectors: ReadDir(%q): %v", dir, err)
	}

	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}
		path := filepath.Join(dir, entry.Name())
		data, readErr := os.ReadFile(path)
		if readErr != nil {
			t.Fatalf("loadAllVectors: ReadFile(%q): %v", path, readErr)
		}

		var raw rawVectorFile
		if parseErr := json.Unmarshal(data, &raw); parseErr != nil {
			t.Fatalf("loadAllVectors: json.Unmarshal(%q): %v", path, parseErr)
		}

		switch {
		case raw.ExpectedError != "":
			// Rejection vector: has "expected_error" key.
			rv := rejectionVector{
				Name:          raw.Name,
				Tx:            raw.Tx,
				ExpectedError: raw.ExpectedError,
			}
			rejection = append(rejection, rv)

		case len(raw.Expected) > 0:
			// Signing vector: has "expected" key.
			var exp vectorExpected
			if parseErr := json.Unmarshal(raw.Expected, &exp); parseErr != nil {
				t.Fatalf("loadAllVectors: parse expected in %q: %v", path, parseErr)
			}
			sv := signingVector{
				Name:     raw.Name,
				Tx:       raw.Tx,
				Expected: exp,
			}
			signing = append(signing, sv)

		default:
			t.Fatalf("loadAllVectors: %q has neither 'expected' nor 'expected_error'", path)
		}
	}

	return signing, rejection
}

// ── Helper: convert vectorTx → TxRequest ────────────────────────────────────

func txRequestFromVector(v vectorTx) TxRequest {
	return TxRequest{
		Type:                 v.Type,
		ChainID:              v.ChainID,
		Nonce:                v.Nonce,
		To:                   v.To,
		Value:                v.Value,
		Data:                 v.Data,
		Gas:                  v.Gas,
		GasPrice:             v.GasPrice,
		MaxFeePerGas:         v.MaxFeePerGas,
		MaxPriorityFeePerGas: v.MaxPriorityFeePerGas,
	}
}

// ── R/S/V normalisation helper ───────────────────────────────────────────────

// normaliseHexBig parses a 0x-prefixed (or bare) hex string as a *big.Int.
// Returns nil on parse error. Used to normalise r/s/v values before comparison
// so that leading-zero differences (e.g. "0x0" vs "0x00") never cause spurious
// mismatches.
func normaliseHexBig(s string) *big.Int {
	s = strings.TrimPrefix(s, "0x")
	s = strings.TrimPrefix(s, "0X")
	if s == "" {
		s = "0"
	}
	n, ok := new(big.Int).SetString(s, 16)
	if !ok {
		return nil
	}
	return n
}

// ── Fixture vault ─────────────────────────────────────────────────────────────

// newParityVault returns a real FileKeyVault backed by the weak (n=2) fixture.
// Using the weak fixture keeps the full suite well under the 5 s SLA even when
// all 9 signing vectors are exercised serially (n=2 KDF is ~1 ms per call).
func newParityVault(t *testing.T) KeyVault {
	t.Helper()
	vault, err := NewFileKeyVault(VaultOptions{
		KeystorePath: testdataFile(t, "keystore-weak.json"),
		PasswordPath: testdataFile(t, "password.txt"),
	})
	if err != nil {
		t.Fatalf("newParityVault: NewFileKeyVault: %v", err)
	}
	return vault
}

// ── Count canary ─────────────────────────────────────────────────────────────

// TestParityVectors_CountCanary asserts that exactly 9 signing vectors and
// 2 rejection vectors (11 total) are present in testdata/vectors/.
//
// This canary fires loudly when a vector file is accidentally dropped or renamed,
// catching the failure at the suite level before any individual subtest runs.
func TestParityVectors_CountCanary(t *testing.T) {
	t.Parallel()

	signing, rejection := loadAllVectors(t)

	const wantSigning = 9
	const wantRejection = 2
	const wantTotal = wantSigning + wantRejection

	if got := len(signing); got != wantSigning {
		t.Errorf("signing vector count = %d, want %d", got, wantSigning)
	}
	if got := len(rejection); got != wantRejection {
		t.Errorf("rejection vector count = %d, want %d", got, wantRejection)
	}
	if got := len(signing) + len(rejection); got != wantTotal {
		t.Errorf("total vector count = %d, want %d", got, wantTotal)
	}
}

// ── Signing parity tests ──────────────────────────────────────────────────────

// TestParityVectors_Signing runs a subtest per signing vector. Each subtest:
//  1. Signs the vector's tx using the real weak-fixture vault.
//  2. Asserts result.RawTransaction == expected.raw_tx (byte-identical).
//  3. Asserts r/s/v match (normalised big.Int comparison to tolerate leading-zero
//     format differences; see comment below on format normalisation).
//  4. Asserts result.Hash == expected.tx_hash.
//  5. Asserts result.From == FixtureTestAddress.
//  6. Round-trips the raw RLP through UnmarshalBinary; asserts rt.Hash() == result.Hash.
//  7. Independently recovers the sender via types.Sender and asserts == FixtureTestAddress.
//
// Format normalisation note:
// The vector files store r/s/v as 0x-prefixed hex with no leading zeros (e.g.
// "0x0" for yParity=0, "0x26" for V=38). Our signer encodes via hexutil.EncodeBig,
// which also produces no-leading-zero hex. Both sides therefore produce the same
// string for normal values.  However, to be safe against any edge-case where
// hexutil.EncodeBig and the vector generator diverge on zero-padding, we compare
// the numeric big.Int values rather than the raw strings.  If a mismatch is ever
// detected between the string forms, normaliseHexBig is the point to investigate.
func TestParityVectors_Signing(t *testing.T) {
	t.Parallel()

	vault := newParityVault(t)
	signer := NewSigner(vault, SignerOptions{}) // no chain-id guard for golden vectors

	signingVecs, _ := loadAllVectors(t)

	for _, vec := range signingVecs {
		vec := vec // capture
		t.Run(vec.Name, func(t *testing.T) {
			t.Parallel()

			req := txRequestFromVector(vec.Tx)
			result, err := signer.SignTransaction(context.Background(), req)
			if err != nil {
				t.Fatalf("SignTransaction: %v", err)
			}

			// ── Assertion 1: byte-identical raw transaction (headline) ──────────
			if got, want := result.RawTransaction, vec.Expected.RawTx; got != want {
				t.Errorf("RawTransaction mismatch:\n  got:  %s\n  want: %s", got, want)
			}

			// ── Assertion 2: r/s/v match (normalised big.Int) ───────────────────
			//
			// We compare numeric values (not raw strings) to be safe against any
			// leading-zero format difference between hexutil.EncodeBig and the
			// vector oracle. In practice both should produce identical strings; if
			// they diverge, the numeric comparison will still pass while the debug
			// line below will show the format difference.
			gotR := normaliseHexBig(result.Signature.R)
			wantR := normaliseHexBig(vec.Expected.R)
			if gotR == nil || wantR == nil || gotR.Cmp(wantR) != 0 {
				t.Errorf("r mismatch: got %s, want %s", result.Signature.R, vec.Expected.R)
			}

			gotS := normaliseHexBig(result.Signature.S)
			wantS := normaliseHexBig(vec.Expected.S)
			if gotS == nil || wantS == nil || gotS.Cmp(wantS) != 0 {
				t.Errorf("s mismatch: got %s, want %s", result.Signature.S, vec.Expected.S)
			}

			gotV := normaliseHexBig(result.Signature.V)
			wantV := normaliseHexBig(vec.Expected.V)
			if gotV == nil || wantV == nil || gotV.Cmp(wantV) != 0 {
				t.Errorf("v mismatch: got %s, want %s", result.Signature.V, vec.Expected.V)
			}

			// ── Assertion 3: hash matches ────────────────────────────────────────
			if got, want := result.Hash, vec.Expected.TxHash; got != want {
				t.Errorf("Hash mismatch:\n  got:  %s\n  want: %s", got, want)
			}

			// ── Assertion 4: from == fixture address ─────────────────────────────
			if got, want := result.From, FixtureTestAddress; got != want {
				t.Errorf("From = %q, want %q", got, want)
			}

			// ── Assertion 5 & 6: RLP round-trip + independent sender recovery ───
			rawBytes, hexErr := decodeHex(result.RawTransaction)
			if hexErr != nil {
				t.Fatalf("decodeHex(RawTransaction): %v", hexErr)
			}

			var rt types.Transaction
			if unmarshalErr := rt.UnmarshalBinary(rawBytes); unmarshalErr != nil {
				t.Fatalf("UnmarshalBinary: %v", unmarshalErr)
			}

			if got, want := rt.Hash().Hex(), result.Hash; got != want {
				t.Errorf("round-trip hash = %q, want %q", got, want)
			}

			// Independent sender recovery (does not use the signer's own check).
			chainID := rt.ChainId()
			if chainID == nil {
				chainID = new(big.Int)
			}
			ethSigner := types.LatestSignerForChainID(chainID)
			sender, senderErr := types.Sender(ethSigner, &rt)
			if senderErr != nil {
				t.Fatalf("types.Sender: %v", senderErr)
			}
			if got, want := sender.Hex(), FixtureTestAddress; got != want {
				t.Errorf("recovered sender = %q, want %q", got, want)
			}
		})
	}
}

// ── Rejection vector tests ────────────────────────────────────────────────────

// TestParityVectors_Rejection runs a subtest per rejection vector. Each subtest:
//  1. Constructs a Signer backed by a panicking fake vault (panicKeyVault).
//  2. Calls SignTransaction with the vector's tx.
//  3. Asserts the error is a *ToolError with Code == expected_error.
//  4. Asserts no panic occurred (the panicKeyVault would panic if called, proving
//     that validation prevented vault invocation for these invalid inputs).
//
// The panicking vault is the same type used in signer_test.go (defined in that
// file; reused here because both are internal tests in package signing).
func TestParityVectors_Rejection(t *testing.T) {
	t.Parallel()

	_, rejectionVecs := loadAllVectors(t)

	for _, vec := range rejectionVecs {
		vec := vec // capture
		t.Run(vec.Name, func(t *testing.T) {
			t.Parallel()

			// Use a panicking vault to prove validation fires before vault access.
			pv := &panicKeyVault{addr: common.HexToAddress(FixtureTestAddress)}
			s := NewSigner(pv, SignerOptions{}) // no guard needed for rejection vectors

			req := txRequestFromVector(vec.Tx)

			// Call must not panic (the panicking vault proves vault was not reached).
			_, signErr := s.SignTransaction(context.Background(), req)
			if signErr == nil {
				t.Fatalf("expected error %q, got nil", vec.ExpectedError)
			}

			var te *ToolError
			if !errors.As(signErr, &te) {
				t.Fatalf("error type = %T (%v), want *ToolError", signErr, signErr)
			}
			if te.Code != vec.ExpectedError {
				t.Errorf("Code = %q, want %q", te.Code, vec.ExpectedError)
			}
		})
	}
}
