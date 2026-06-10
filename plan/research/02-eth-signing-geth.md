# Research: Signing Ethereum Transactions Offline with go-ethereum v1.17.3

## Summary

go-ethereum v1.17.3 exposes a tight, three-package surface — `accounts/keystore`, `crypto`, `core/types` — that lets us decrypt a Web3 Secret Storage keystore in memory, build either a legacy (type 0, EIP-155) or EIP-1559 (type 2) transaction, sign it with a chain-aware `Signer`, and emit a broadcast-ready EIP-2718 envelope, all without any network I/O. The recipe is: `keystore.DecryptKey(json, password)` → build `types.LegacyTx` / `types.DynamicFeeTx` → wrap with `types.NewTx` → choose `types.LatestSignerForChainID(chainID)` → `types.SignTx` → emit RLP with `tx.MarshalBinary()` → extract `{r, s, v}` via `tx.RawSignatureValues()` and cross-check the sender with `types.Sender`. The only subtleties that bite in practice are the v-vs-yParity distinction between transaction types, the EIP-2718 typed-vs-legacy envelope shape, and a few zeroing/keystore details that we keep in §Common Pitfalls.

## Key Concepts

### EIP-2718 typed transaction envelope

EIP-2718 frames every post-Berlin transaction as `TransactionType || TransactionPayload`, where `TransactionType` is "a positive unsigned 8-bit number between `0` and `0x7f`" and `TransactionPayload` is an opaque byte array whose interpretation depends on the type [1]. Legacy transactions remain bare RLP lists whose first byte falls in `[0xc0, 0xfe]`, so a decoder can branch on the first byte: `<= 0x7f` is typed, `>= 0xc0` is legacy [1]. In go-ethereum this distinction is materialised by `Transaction.MarshalBinary`: for legacy txs it emits a single RLP list; for typed txs it prefixes the type byte and emits `type || rlp(payload)`.

### Legacy (type 0) signing with EIP-155 replay protection

EIP-155 changes the signing hash from six fields to **nine RLP-encoded elements** — `(nonce, gasprice, startgas, to, value, data, chainid, 0, 0)` — and rewrites `v` as `{0,1} + CHAIN_ID * 2 + 35` (equivalently `CHAIN_ID * 2 + 36` for parity 1) [2]. EIP-155 itself only says legacy `v ∈ {27, 28}` signatures remain valid in parallel; it does **not** specify what to do when `chainId` is null or zero [2]. Go-ethereum's "nil/0 chainId ⇒ Homestead `v=27/28`" fallback is an implementation detail of `LatestSignerForChainID(nil) ⇒ HomesteadSigner`; for this signer `chainId` is required input, so this corner is moot.

### EIP-1559 (type 2) signing with `signature_y_parity`

EIP-1559 introduces an EIP-2718 type-`0x02` transaction whose wire form is `0x02 || rlp([chain_id, nonce, max_priority_fee_per_gas, max_fee_per_gas, gas_limit, destination, amount, data, access_list, signature_y_parity, signature_r, signature_s])` [3]. The signed digest is `keccak256(0x02 || rlp([chain_id, nonce, max_priority_fee_per_gas, max_fee_per_gas, gas_limit, destination, amount, data, access_list]))` [3]. The spec field name is literally `signature_y_parity` — the LSB of the curve point's `y` coordinate, value `0` or `1`. We expose it in our PRD output contract as `yParity` (the more common informal name) but the spec's identifier is `signature_y_parity`.

### Keystore decryption

`keystore.DecryptKey(keyjson []byte, auth string) (*Key, error)` parses a Web3 Secret Storage JSON blob, runs scrypt over the password to derive the KDF key, decrypts the ciphertext, and returns a `*Key` containing the plaintext ECDSA private key plus the cached address and the keystore UUID [4][5]:

```go
type Key struct {
    Id         uuid.UUID
    Address    common.Address
    PrivateKey *ecdsa.PrivateKey
}
```

The struct's comment notes "privkey in this struct is always in plaintext" [5]. That is the only place plaintext key material exists, and the caller (us) is responsible for zeroing it.

### Signer selection

`types.LatestSignerForChainID(chainID *big.Int) Signer` returns the most modern signer that can sign any transaction relevant to that chain — concretely a `londonSigner` for non-nil `chainID`, which handles legacy, EIP-2930, and EIP-1559 transactions uniformly [6]. For `nil` chainID it falls back to `HomesteadSigner` (pre-EIP-155 `v ∈ {27, 28}`); we never pass nil because chainId is required input.

## How It Works

The end-to-end signing flow has six steps. Each call below is exactly one go-ethereum API.

1. **Decrypt the keystore.** `keystore.DecryptKey(jsonBytes, password)` returns `*Key`; `key.PrivateKey` is the `*ecdsa.PrivateKey` we will sign with [4][5]. The password bytes are no longer needed past this call — zero them.
2. **Build the inner tx data.** Populate a `types.LegacyTx` or `types.DynamicFeeTx` struct from the validated JSON input. Field types come from go-ethereum: `*big.Int` for amounts, `uint64` for nonce/gas, `*common.Address` for `To` (nil for contract creation), `[]byte` for `Data` [6].
3. **Wrap with `types.NewTx`.** `NewTx(inner TxData) *Transaction` deep-copies the inner via `inner.copy()` and stores it in an opaque `Transaction` [6]. (The "wraps" shorthand is loose — it copies.)
4. **Pick the signer.** `signer := types.LatestSignerForChainID(chainID)` returns the right `Signer` for both legacy-with-EIP-155 and EIP-1559 paths [6].
5. **Sign.** `signedTx, err := types.SignTx(tx, signer, key.PrivateKey)` produces a new `*Transaction` with `{V, R, S}` populated. The hash that is actually signed depends on the type: for legacy with EIP-155, the 9-element hash from [2]; for EIP-1559, the `keccak256(0x02 || rlp(...))` digest from [3]. Internally `crypto.Sign` calls into either cgo libsecp256k1 (forces low-s) or the nocgo decred fallback (`SignCompact` is RFC6979/BIP-0062 canonical) — either way the output is low-s canonical, which is what gives us parity with `cast` and ethers v6 without any explicit normalization step.
6. **Emit and verify.** `raw, _ := signedTx.MarshalBinary()` returns the broadcast-ready bytes — a plain RLP list for legacy, or `0x02 || rlp(payload)` for EIP-1559, per EIP-2718 [1]. Use `signedTx.RawSignatureValues()` for `{r, s, v}`, `signedTx.Hash()` for the tx hash, and `types.Sender(signer, signedTx)` to recover the address and compare it against `key.Address` as a self-check.

The `v` returned from `RawSignatureValues()` differs by type:

| Type | What `v` holds | Encoding |
|------|----------------|----------|
| Legacy + EIP-155 | EIP-155 `v` = `chainId*2 + 35` or `chainId*2 + 36` | Full `*big.Int` |
| Legacy pre-155 | `27` or `28` (Homestead — never used by us since chainId is required) | Small `*big.Int` |
| EIP-1559 (type 2) | `signature_y_parity` (`0` or `1`) | `*big.Int` whose value is `0` or `1` |

Our PRD output contract exposes EIP-1559's `v` as the yParity (PRD P0-SIGN-4); we render it as `"0x0"` or `"0x1"` to make the type explicit.

## Code Examples

### Decrypt the keystore and zero the password

```go
import (
    "github.com/ethereum/go-ethereum/accounts/keystore"
)

func decryptKey(jsonBytes []byte, password []byte) (*keystore.Key, error) {
    // DecryptKey takes the password as string; we still control the source slice
    // and zero it on return so the plaintext lives in memory for the minimum time.
    defer clear(password)
    key, err := keystore.DecryptKey(jsonBytes, string(password))
    if err != nil {
        return nil, err
    }
    return key, nil
}
```

`clear(password)` zeroes the underlying byte slice in place (Go 1.21+ builtin). Note that converting `[]byte` → `string` for `DecryptKey` makes a copy that we cannot reach; that's a real but small leak we accept in v1 because (a) the password is also on disk by design, and (b) the keystore-derived private key is the asset we care most about.

### Sign a legacy (type 0, EIP-155) transaction

```go
import (
    "math/big"

    "github.com/ethereum/go-ethereum/common"
    "github.com/ethereum/go-ethereum/core/types"
)

func signLegacy(
    key *keystore.Key,
    chainID *big.Int,
    nonce uint64,
    to common.Address,
    value *big.Int,
    gasLimit uint64,
    gasPrice *big.Int,
    data []byte,
) (*types.Transaction, error) {
    inner := &types.LegacyTx{
        Nonce:    nonce,
        GasPrice: gasPrice,
        Gas:      gasLimit,
        To:       &to, // nil for contract creation
        Value:    value,
        Data:     data,
    }
    tx := types.NewTx(inner)
    signer := types.LatestSignerForChainID(chainID)
    return types.SignTx(tx, signer, key.PrivateKey)
}
```

### Sign an EIP-1559 (type 2) transaction

```go
func signDynamicFee(
    key *keystore.Key,
    chainID *big.Int,
    nonce uint64,
    to common.Address,
    value *big.Int,
    gasLimit uint64,
    tipCap, feeCap *big.Int,
    data []byte,
) (*types.Transaction, error) {
    inner := &types.DynamicFeeTx{
        ChainID:    chainID,
        Nonce:      nonce,
        GasTipCap:  tipCap, // maxPriorityFeePerGas
        GasFeeCap:  feeCap, // maxFeePerGas
        Gas:        gasLimit,
        To:         &to,
        Value:      value,
        Data:       data,
        AccessList: nil, // v1 rejects non-empty access lists upstream of this call
    }
    tx := types.NewTx(inner)
    signer := types.LatestSignerForChainID(chainID)
    return types.SignTx(tx, signer, key.PrivateKey)
}
```

### Emit broadcast-ready RLP, extract `{r, s, v}`, hash, recover sender

```go
import (
    "encoding/hex"
    "fmt"

    "github.com/ethereum/go-ethereum/core/types"
)

type SignedOutput struct {
    RawTransaction string // 0x-prefixed
    R, S, V        string // 0x-prefixed
    Hash           string // 0x-prefixed
    From           string // EIP-55 checksum
}

func emit(signed *types.Transaction, signer types.Signer, expected common.Address) (*SignedOutput, error) {
    raw, err := signed.MarshalBinary() // EIP-2718 envelope: typed prefix or legacy RLP list
    if err != nil {
        return nil, err
    }
    v, r, s := signed.RawSignatureValues()
    recovered, err := types.Sender(signer, signed)
    if err != nil {
        return nil, fmt.Errorf("recover sender: %w", err)
    }
    if recovered != expected {
        return nil, fmt.Errorf("sender mismatch: keystore=%s recovered=%s",
            expected.Hex(), recovered.Hex())
    }
    return &SignedOutput{
        RawTransaction: "0x" + hex.EncodeToString(raw),
        R:              "0x" + r.Text(16),
        S:              "0x" + s.Text(16),
        V:              "0x" + v.Text(16),
        Hash:           signed.Hash().Hex(),
        From:           recovered.Hex(),
    }, nil
}
```

For EIP-1559, the `v` returned by `RawSignatureValues()` is `0` or `1` (the `signature_y_parity` of [3]), so the rendered hex will be `"0x0"` or `"0x1"`. For legacy with EIP-155 it is the full `chainId*2 + 35/36` value. The PRD exposes both under the same JSON field `signature.v` with this contract.

### Zero the private key after signing

go-ethereum has an internal `zeroKey(k *ecdsa.PrivateKey)` helper in `accounts/keystore/keystore.go` (around the unlock/sign call sites) [7], but it is **unexported**, so we re-implement it inline:

```go
import (
    "crypto/ecdsa"
)

func zeroKey(k *ecdsa.PrivateKey) {
    if k == nil || k.D == nil {
        return
    }
    clear(k.D.Bits()) // zeroes the big.Int limbs in place
}
```

Call `defer zeroKey(key.PrivateKey)` at the top of every function that holds a `*keystore.Key`. Treat this as best-effort: the Go GC may have already copied limbs elsewhere; do not claim *guaranteed* erasure.

## Common Pitfalls

- **Confusing `v` for type-2 with EIP-155 `v`.** `RawSignatureValues()` returns the literal stored `V` for whatever transaction type the tx is. For type 2 it is the `signature_y_parity` (0/1) from [3]; for legacy with EIP-155 it is `chainId*2 + 35/36` from [2]. Render them per-type in the output JSON, and document which it is — anyone hand-comparing with ethers v6 will hit this immediately.
- **Forgetting EIP-2718's leading-byte branch.** `MarshalBinary` already does the right thing (`type || rlp(payload)` for typed, plain `rlp(list)` for legacy [1]), so do **not** wrap typed bytes in an outer RLP list — that produces a payload that no node will accept. Cross-check by round-tripping `UnmarshalBinary(MarshalBinary(tx))`.
- **Reading the EIP-155 "nil chainId" path as spec-mandated.** EIP-155 does not specify what to do for null/0 chainId [2]; the "falls back to Homestead v=27/28" behavior is an implementation detail of `LatestSignerForChainID(nil) ⇒ HomesteadSigner`. We require chainId, so we never exercise this — but don't repeat the misattribution in docs.
- **Treating low-s as a go-ethereum normalization step.** The underlying lib produces low-s (cgo libsecp256k1 forces it; the nocgo decred `SignCompact` path is RFC6979/BIP-0062 canonical). go-ethereum does **not** call an explicit normalize-after-sign step in `crypto.Sign`. Parity with `cast` and ethers v6 follows from the underlying lib, not from a geth call we can point at.
- **`NewTx` semantics — copy, not wrap.** `types.NewTx(inner)` deep-copies the inner via `inner.copy()` and stores the copy. Mutating the original `LegacyTx` / `DynamicFeeTx` after the `NewTx` call has no effect on the produced `*Transaction` [6]. Treat the inner as throwaway input.
- **Sender mismatch is silent if you forget to check.** `types.Sender(signer, tx)` returns the address recovered from the signature. Compare it against `key.Address` on every signature; a mismatch means the signer is wrong for the tx type or the chainId in the tx and the chainId in the signer disagree (the EIP-155 hash bakes chainId in, so a wrong signer produces a wrong recovered address rather than an error).
- **Zeroing is best-effort.** `clear(k.D.Bits())` zeroes the current `big.Int` limbs in place, but Go's escape analysis and GC may have copied them. There is no portable way to "guarantee" full erasure in Go; do not claim it. Pair zeroing with a hard scope limit on the key lifetime and a log-scanning test (PRD P0-SEC-3) rather than implying cryptographic-grade erasure.
- **Misreading the recent Go vulnerability-DB advisories as open on v1.17.x.** Per pkg.go.dev/vuln, GO-2026-4314/-4315 were fixed in v1.16.8, GO-2026-4507/-4511 in v1.16.9, and GO-2026-4508 in v1.17.0 — our pinned v1.17.3 is affected by none of them (4511 is an ECIES public-key-validation flaw in the RLPx handshake, CVE-2026-26315, not a plain DoS). All five were p2p-path issues anyway, and this signer imports no p2p packages and makes no network calls. Future advisories are caught automatically by the `govulncheck` step in CI; no manual "watch and bump" tracking is needed.

## Further Reading

- [go-ethereum `accounts/keystore` package — DecryptKey + Key struct](https://pkg.go.dev/github.com/ethereum/go-ethereum@v1.17.3/accounts/keystore) — official godoc for v1.17.3; the exact API entry points we call.
- [go-ethereum `core/types` package — NewTx, SignTx, LatestSignerForChainID, Transaction methods](https://pkg.go.dev/github.com/ethereum/go-ethereum@v1.17.3/core/types) — official godoc for v1.17.3; signing & serialization surface.
- [EIP-2718: Typed Transaction Envelope](https://eips.ethereum.org/EIPS/eip-2718) — the `TransactionType || TransactionPayload` framing and the legacy vs. typed leading-byte rule.
- [EIP-1559: Fee market change with type 2 transactions](https://eips.ethereum.org/EIPS/eip-1559) — type-2 envelope, signing digest, the `signature_y_parity` field name.
- [EIP-155: Simple replay attack protection](https://eips.ethereum.org/EIPS/eip-155) — the 9-element signing hash and the `chainId*2 + 35/36` `v` formula.

## Sources

[1] [EIP-2718: Typed Transaction Envelope](https://eips.ethereum.org/EIPS/eip-2718) — ethereum.org EIP index, retrieved 2026-06-10. Source for the typed envelope shape (`TransactionType || TransactionPayload`), the `[0, 0x7f]` typed vs. `[0xc0, 0xfe]` legacy first-byte distinction, and the rationale for branching on the first byte.

[2] [EIP-155: Simple replay attack protection](https://eips.ethereum.org/EIPS/eip-155) — ethereum.org EIP index, retrieved 2026-06-10. Source for the 9-element signing hash `(nonce, gasprice, startgas, to, value, data, chainid, 0, 0)`, the `v = {0,1} + CHAIN_ID * 2 + 35` formula, and confirmation that the spec does not define behavior for null/zero `chainId`.

[3] [EIP-1559: Fee market change for ETH 1.0 chain](https://eips.ethereum.org/EIPS/eip-1559) — ethereum.org EIP index, retrieved 2026-06-10. Source for the `0x02 || rlp([...])` envelope, the `keccak256(0x02 || rlp(...))` signing digest, and the literal `signature_y_parity` field name.

[4] [go-ethereum `accounts/keystore` package documentation](https://pkg.go.dev/github.com/ethereum/go-ethereum@v1.17.3/accounts/keystore) — pkg.go.dev godoc for v1.17.3, retrieved 2026-06-10. Source for `DecryptKey(keyjson []byte, auth string) (*Key, error)`.

[5] [go-ethereum `accounts/keystore/key.go` — `Key` struct definition](https://pkg.go.dev/github.com/ethereum/go-ethereum@v1.17.3/accounts/keystore#Key) — pkg.go.dev godoc for v1.17.3, retrieved 2026-06-10. Source for the `Key` struct fields (`Id`, `Address`, `PrivateKey`) and the "privkey in this struct is always in plaintext" comment.

[6] [go-ethereum `core/types` package documentation](https://pkg.go.dev/github.com/ethereum/go-ethereum@v1.17.3/core/types) — pkg.go.dev godoc for v1.17.3, retrieved 2026-06-10. Source for `NewTx`, `LatestSignerForChainID`, `SignTx`, `Sender`, `LegacyTx`, `DynamicFeeTx`, and `Transaction.{MarshalBinary, RawSignatureValues, Hash, Type}` signatures.

[7] [go-ethereum `accounts/keystore` package source — `zeroKey` helper](https://pkg.go.dev/github.com/ethereum/go-ethereum@v1.17.3/accounts/keystore) — pkg.go.dev godoc for v1.17.3, retrieved 2026-06-10. Source for the canonical `defer zeroKey(key.PrivateKey)` pattern; `zeroKey` is unexported, so we re-implement it inline (precise line numbers intentionally omitted — they drift across releases).
