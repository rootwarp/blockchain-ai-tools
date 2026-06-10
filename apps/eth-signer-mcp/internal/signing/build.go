// Package signing — build.go — Issue 2.5.
// Converts a validated *parsedTx into an unsigned go-ethereum transaction plus
// the matching Signer.  This function is INFALLIBLE given a validated parsedTx;
// all rejection (field presence, type checks, numeric range, EIP-55, chainId≠0)
// has already been done in validate.go (Issue 2.4).
package signing

import (
	"fmt"
	"math/big"

	"github.com/ethereum/go-ethereum/core/types"
)

// buildTx constructs the appropriate unsigned *types.Transaction and its
// matching types.Signer from a validated *parsedTx.
//
// Design notes:
//
//   - types.LatestSignerForChainID is used for BOTH transaction types.
//     For type 0 (legacy) it returns a London-era signer that nonetheless
//     applies EIP-155 replay protection (chainID*2+35/36 for v).
//     For type 2 (EIP-1559) it returns the London signer with yParity (0/1).
//     The chainID is guaranteed non-zero by validate.go; chainID=0 would select
//     the replay-unprotected Homestead signer — that path is blocked upstream.
//
//   - Zero value: if parsedTx.value is nil (which should not occur given a
//     valid parsedTx, but is defensive), buildTx substitutes new(big.Int) (0).
//     validate.go always produces a non-nil value, but the guard is cheap.
//
//   - Empty data: validate.go decodes "0x" as []byte{} (non-nil empty slice).
//     We preserve that as-is.  go-ethereum's RLP encoder encodes an empty
//     byte slice as 0x80 (RLP empty string), which is the correct Ethereum
//     wire encoding for empty calldata — not 0xc0 (RLP empty list).
//
//   - Values are *big.Int end-to-end.  There is NO uint64 cast for value,
//     gasPrice, gasTipCap, or gasFeeCap — values larger than 2^64 wei survive.
//
//   - For type 2, AccessList is left nil (empty by validation; nil and empty
//     AccessList are RLP-equivalent in go-ethereum).
//
// Field mapping:
//
//	Type 0 (LegacyTx):
//	  Nonce    ← parsedTx.nonce
//	  GasPrice ← parsedTx.gasPrice
//	  Gas      ← parsedTx.gas
//	  To       ← parsedTx.to  (nil = contract creation)
//	  Value    ← parsedTx.value
//	  Data     ← parsedTx.data
//
//	Type 2 (DynamicFeeTx):
//	  ChainID  ← parsedTx.chainID
//	  Nonce    ← parsedTx.nonce
//	  GasTipCap← parsedTx.gasTipCap  (a.k.a. maxPriorityFeePerGas)
//	  GasFeeCap← parsedTx.gasFeeCap  (a.k.a. maxFeePerGas)
//	  Gas      ← parsedTx.gas
//	  To       ← parsedTx.to  (nil = contract creation)
//	  Value    ← parsedTx.value
//	  Data     ← parsedTx.data
//	  AccessList: nil (always empty by validate.go)
func buildTx(p *parsedTx) (*types.Transaction, types.Signer) {
	signer := types.LatestSignerForChainID(p.chainID)

	// Defensive zero: validate always produces a non-nil value, but guard anyway.
	value := p.value
	if value == nil {
		value = new(big.Int)
	}

	// data must be non-nil for correct RLP encoding.  validate.go produces
	// []byte{} for "0x", so this branch is never hit in practice; it guards
	// against accidental nil in future callers.
	data := p.data
	if data == nil {
		data = []byte{}
	}

	switch p.txType {
	case 2:
		tx := types.NewTx(&types.DynamicFeeTx{
			ChainID:   p.chainID,
			Nonce:     p.nonce,
			GasTipCap: p.gasTipCap, // maxPriorityFeePerGas
			GasFeeCap: p.gasFeeCap, // maxFeePerGas
			Gas:       p.gas,
			To:        p.to, // nil → contract creation
			Value:     value,
			Data:      data,
			// AccessList is intentionally nil (empty by validation).
		})
		return tx, signer

	case 0: // legacy / EIP-155
		tx := types.NewTx(&types.LegacyTx{
			Nonce:    p.nonce,
			GasPrice: p.gasPrice,
			Gas:      p.gas,
			To:       p.to, // nil → contract creation
			Value:    value,
			Data:     data,
		})
		return tx, signer

	default:
		// validate.go guarantees txType is 0 or 2 (types 1/3/4 are planned for a
		// future phase).  Any other value reaching here means a new type was added
		// to validate.go without a corresponding case in this switch — a programmer
		// error that must not silently fabricate a wrong-type transaction.
		panic(fmt.Sprintf("buildTx: unhandled txType %d (validate.go contract violated)", p.txType))
	}
}
