# Constructing and Signing Ethereum Transactions: Serialization, Digest Formation, and ECDSA Signing for Native Transfers and Contract Invocations

**Technical Report**
*eth-signer-mcp · Blockchain × AI Tools (Go monorepo)*
June 13, 2026

---

## Abstract

Correctly assembling and signing an Ethereum transaction *off-line* — without a node to validate or relay it — requires composing several loosely coupled specifications: the typed-transaction envelope, Recursive Length Prefix (RLP) serialization, the contract Application Binary Interface (ABI), per-type signing-digest construction, and the Elliptic Curve Digital Signature Algorithm (ECDSA) over the secp256k1 curve. This report gives a self-contained, primary-source-verified account of that pipeline for the two canonical operations — a native ETH (value) transfer and a smart-contract function invocation — and shows that the two differ in only four transaction fields. We specify the exact field ordering and byte-level serialization for legacy and EIP-1559 transactions, distinguish the unsigned signing pre-image from the broadcast payload, derive the keccak-256 signing digest for each transaction variant, and detail the ECDSA signature together with its recovery identifier, the EIP-155 `v` / typed-transaction `yParity` encodings, and the EIP-2 low-`s` canonical-signature constraint. We then present a reference realization in Go using go-ethereum (v1.17.3) for both operations and reconcile it with an existing offline signer (`eth-signer-mcp`), identifying ABI calldata construction as the sole caller-side responsibility. Every normative claim was checked against primary sources — the Ethereum Yellow Paper, the relevant Ethereum Improvement Proposals (EIPs), the execution specifications, the Solidity ABI specification, and the go-ethereum source — by an adversarial verification pass; residual uncertainties and one corrected claim are recorded inline as numbered *Remarks*.

**Keywords:** Ethereum; transaction signing; RLP serialization; EIP-1559; EIP-155; contract ABI; ECDSA; secp256k1; offline signing; go-ethereum.

---

## 1. Introduction

### 1.1 Motivation

An offline transaction signer must reproduce, byte for byte, the serialization and signing rules enforced by Ethereum consensus, with no opportunity to consult a node for validation before the signed artifact is handed to a relayer. Subtle deviations are not gracefully degraded: a non-minimal integer encoding, a mis-ordered field, an incorrect `v` value, or a non-canonical (high-`s`) signature yields a transaction that is rejected as invalid, silently re-hashed under malleability, or — in the worst case, where the recipient or amount is misplaced — irreversibly misdirects funds. A precise, end-to-end account of the construction-and-signing pipeline is therefore a prerequisite for any correct signer implementation.

### 1.2 Scope

We treat two operations in depth:

1. a **native ETH transfer** (moving value to an externally owned account, EOA); and
2. a **smart-contract function invocation** (calling a deployed contract).

The newer transaction types (EIP-2930 access-list, EIP-4844 blob, EIP-7702 set-code) are summarized for completeness but not realized in code. Network propagation, fee-market dynamics, and gas-estimation algorithms are out of scope; we treat gas parameters as inputs.

### 1.3 Contributions

This report (i) consolidates the construction-and-signing pipeline from its constituent specifications into one primary-source-verified account; (ii) makes explicit the precise field-level delta between a native transfer and a contract invocation; (iii) provides byte-level worked examples of both RLP serialization and ABI calldata; (iv) gives a compilable Go reference implementation for both operations; and (v) reconciles that implementation with the production `eth-signer-mcp` signer, isolating the one caller-side responsibility (ABI encoding).

### 1.4 Verification methodology and notational conventions

The technical content was produced by a multi-source survey and then subjected to an adversarial verification pass in which each load-bearing claim — field orderings, the `v`/`yParity` arithmetic, the function selector value, RLP prefix bands, and the low-`s` rule — was independently checked against a primary source. Claims that survived are stated plainly; the one claim that was refuted and corrected, and the few that remain uncertain or revision-dependent, are recorded as numbered *Remarks*.

We write `‖` for byte concatenation, `keccak256(·)` for the Ethereum-padding Keccak-256 hash (distinct from NIST SHA3-256), and `RLP(·)` for the encoding of Section 3. Byte values are hexadecimal (`0x…`); integers are big-endian unless noted. `n` denotes the secp256k1 group order.

### 1.5 Organization

Section 2 surveys the transaction-type taxonomy and the four-field native-vs-contract delta. Section 3 specifies RLP serialization and the unsigned-vs-broadcast distinction. Section 4 specifies the input `data` field, including ABI calldata. Section 5 derives the signing digest per transaction type. Section 6 details ECDSA signing, the recovery identifier, and signature attachment. Section 7 gives the Go reference implementation and the reconciliation. Section 8 discusses common implementation errors, and Section 9 concludes. Appendix A provides a practitioner checklist.

---

## 2. Transaction Types

### 2.1 The typed-transaction envelope

An Ethereum transaction is either *legacy* — a bare RLP list with no type byte, conceptually "type 0" — or *typed* per EIP-2718 [2], whose envelope is the concatenation `TransactionType ‖ TransactionPayload`. The scheme is unambiguous because RLP guarantees that the first byte of any encoded list is `≥ 0xc0` (Section 3.1); type identifiers are restricted to the range `0x00`–`0x7f`, so a leading type byte can never be mistaken for the start of a legacy RLP list. The value `0xff` is reserved. EIP-2718 further recommends that the type byte be the first byte of the *signed* payload, preventing reuse of a signature across transaction types; EIP-1559 [5] adopts this by including the `0x02` prefix inside the signing pre-image (Section 5).

### 2.2 Taxonomy

Table 1 enumerates the current transaction types.

**Table 1.** Ethereum transaction types.

| Type | Specification | First byte | Fee model | Purpose |
|---|---|---|---|---|
| Legacy | EIP-155 [3] | `≥ 0xc0` (RLP list) | single `gasPrice` | backward compatibility |
| 1 (AccessList) | EIP-2930 [4] | `0x01` | `gasPrice` + access list | warm-storage pre-declaration |
| **2 (FeeMarket)** | EIP-1559 [5] | `0x02` | `maxPriorityFeePerGas` + `maxFeePerGas` | **modern default** |
| 3 (Blob) | EIP-4844 [6] | `0x03` | adds a blob-gas market | rollup blob data (`to` MUST NOT be nil) |
| 4 (SetCode) | EIP-7702 [7] | `0x04` | EIP-1559 fees + `authorizationList` | EOA code delegation (Pectra, May 2025; `to` MUST NOT be nil) |

### 2.3 Type selection

For both native transfers and contract calls on mainnet, the EIP-1559 Type 2 (`0x02`) format is the appropriate default: its dynamic base fee yields reliable inclusion and avoids the over- and under-payment failure modes of a static `gasPrice`. The legacy format remains fully valid and is still required for chains or tooling that predate or do not implement EIP-1559, for some test and private networks, and wherever the pre-2718 wire format must be produced; where legacy is used, the EIP-155 (chain-bound) variant should be preferred over the pre-155 form because it provides replay protection.

> **Remark 1 (verification — the "default" is convention, not specification).** The claim that Type 2 is *the* recommended default was downgraded during verification from a normative fact to a well-supported industry convention. No primary specification declares a normative default; wallet documentation [8] describes Type 2's advantages without designating it the standard. It is the de-facto default in mainstream wallets and libraries since the London fork. Implementations targeting EOA smart-account flows may deliberately default to Type 4 on those code paths.

### 2.4 Field-level distinction: native transfer vs. contract invocation

For a fixed transaction type, exactly four fields distinguish the two operations (Table 2). A transaction is a function invocation precisely when `data` is non-empty and `to` is a contract address; the EVM then executes the code at `to` against `data` [2], [9], [12].

**Table 2.** Field-level differences between a native transfer and a contract call.

| Field | Native ETH transfer | Contract function call |
|---|---|---|
| `to` | recipient **EOA** address (20 bytes) | the **contract** address (20 bytes) |
| `value` | wei amount, **> 0** | **0** for non-payable functions; > 0 only if the function is `payable` |
| `data` | **`0x`** (empty) | 4-byte selector + ABI-encoded arguments (non-empty) |
| `gasLimit` | **exactly 21000** (fixed intrinsic cost) | 21000 + calldata cost + execution gas; obtain via `eth_estimateGas`, **never** a fixed 21000 |

> **Remark 2 (the token-transfer trap).** In an ERC-20 `transfer`, the *token* recipient is an argument *inside* `data`; the `to` field holds the *token contract* and `value` is 0. In a native transfer the recipient *is* `to` and `data` is empty. Conflating the two is a classic, fund-losing error.

A third operation, **contract creation**, sets `to` to nil/empty (legal only for legacy, Type 1, and Type 2), places init code in `data`, and pays an additional `Gtxcreate = 32000` gas above the 21000 base. go-ethereum encodes the pre-summed constant `TxGasContractCreation = 53000 = 21000 + 32000`; the "additional 32000" is the marginal cost, so both phrasings are consistent.

---

## 3. Raw Transaction Serialization (RLP)

### 3.1 Encoding rules

RLP (Yellow Paper [1], Appendix B) serializes exactly two structural categories — **byte strings** and **lists**. It is a *structural* encoder: aside from the canonical encoding of non-negative integers, it imposes no type semantics [10]. Decoding is driven by the first byte `b0` according to the prefix bands of Table 3.

**Table 3.** RLP prefix bands (interpreting the first byte `b0`).

| `b0` range | Meaning | Length / value |
|---|---|---|
| `0x00`–`0x7f` | single byte, self-encoding | the byte **is** the value |
| `0x80`–`0xb7` | short string | `len = b0 − 0x80` (`0x80` ⇒ empty string) |
| `0xb8`–`0xbf` | long string | `lenOfLen = b0 − 0xb7`, then big-endian length, then data |
| `0xc0`–`0xf7` | short list | `payloadLen = b0 − 0xc0` (`0xc0` ⇒ empty list) |
| `0xf8`–`0xff` | long list | `lenOfLen = b0 − 0xf7`, then big-endian length, then payload |

The boundary at `0xc0` cleanly separates strings from lists. Long forms use a length-of-length: a string prefix is `0xb7 + byteLen(len)` and a list prefix is `0xf7 + byteLen(payloadLen)`. For example, a 1024-byte string has length `0x0400`, which requires 2 bytes, giving prefix `0xb7 + 2 = 0xb9`, followed by `04 00` and then the 1024 bytes.

### 3.2 Scalar (integer) encoding

Quantities — `nonce`, `gasPrice`, `value`, `chainId`, the fee caps, `yParity`, and the signature scalars — are encoded as the **shortest** big-endian byte array with **no leading zeros**. Consequently:

- The integer `0` encodes as the empty string `0x80` — **never** `0x00`.
- The single byte `0x00` (a one-byte *string*, e.g. raw byte data) encodes as `0x00`, which is distinct from the scalar `0`.
- `yParity = 0` encodes as `0x80`; `yParity = 1` encodes as `0x01`. (Encoding `0x00` for parity 0 is non-canonical.)
- Decoders **MUST reject** non-minimal scalars (those with leading zeros) as non-canonical.

The empty byte string is `0x80` and the empty list is `0xc0`. A `to` address is a 20-byte string, encoded with the `0x94` prefix followed by the 20 bytes; an empty `to` (contract creation) is `0x80`.

> **Remark 3 (revision-dependent bound).** The Yellow Paper's exact wording of the maximum encodable size (the Shanghai-revision text states `2^64` bytes) varies by revision. The prefix bands and length-of-length scheme above are confirmed and stable; cite a dated Yellow Paper revision if an exact bound is required.

### 3.3 Field ordering per transaction type

**Legacy** (a bare RLP list, no type prefix):

```
RLP([nonce, gasPrice, gasLimit, to, value, data, v, r, s])
```

**EIP-1559 Type 2** (typed envelope; `0x02` is a raw prefix byte *outside* the RLP list — it is **not** itself RLP-encoded):

```
0x02 ‖ RLP([chainId, nonce, maxPriorityFeePerGas, maxFeePerGas,
            gasLimit, to, value, data, accessList, yParity, r, s])
```

The `accessList` has the shape `List[(address, List[storageKey])]`. On the wire, each entry is a 20-byte address (`0x94`-prefixed) followed by a list of 32-byte storage keys (`0xa0`-prefixed); for an ordinary transfer or call it is the empty list `0xc0`. (EIP-1559's Python dataclass types these elements as `int`, but the canonical wire framing is fixed-width, as confirmed against the execution specifications [11].)

### 3.4 Unsigned pre-image vs. final broadcast payload

The byte sequence that is *signed* is not the byte sequence that is *broadcast*: the signing pre-image omits the signature, and the broadcast payload appends it (Table 4).

**Table 4.** Signing pre-image versus broadcast payload.

| | Signed pre-image | Broadcast (final) |
|---|---|---|
| Legacy (EIP-155) | `RLP([nonce, gasPrice, gasLimit, to, value, data, chainId, 0, 0])` — **9 elements**, trailing `(chainId, 0, 0)` | `RLP([nonce, gasPrice, gasLimit, to, value, data, v, r, s])` — `v` carries `chainId` |
| EIP-1559 | `0x02 \|\| RLP([chainId, nonce, maxPriorityFeePerGas, maxFeePerGas, gasLimit, to, value, data, accessList])` — **9 fields, no signature** | `0x02 \|\| RLP([… same 9 fields …, yParity, r, s])` — **12 fields** |

Because the two RLP lists differ in element count, they carry different length prefixes. The JSON-RPC method `eth_sendRawTransaction` accepts the **final** form, `0x`-hex-encoded.

### 3.5 Worked example

Listing 1 illustrates minimal big-endian encoding, the 20-byte `to` string, and the scalar-zero → `0x80` rule for a legacy field list. The signature slots are shown as zero **placeholders** purely to expose the encoding; **the result is not a validly signed transaction**.

**Listing 1.** Byte-level RLP encoding of a legacy field list (signature fields are placeholders).

```
Fields:
  nonce    = 0                                            -> 0x80
  gasPrice = 0x04a817c800 (20 Gwei)                       -> 0x85 04a817c800        (5 bytes, no leading zero)
  gasLimit = 21000 = 0x5208                               -> 0x82 5208
  to       = 0x3535...35 (20 bytes of 0x35)               -> 0x94 3535...35         (21 bytes total)
  value    = 1 ETH = 0x0de0b6b3a7640000                   -> 0x88 0de0b6b3a7640000  (8 bytes)
  data     = (empty)                                      -> 0x80
  v=0, r=0, s=0  (placeholders)                           -> 0x80, 0x80, 0x80

Payload (concatenated item encodings):
  80                       (nonce)     1
  85 04a817c800            (gasPrice)  6
  82 5208                  (gasLimit)  3
  94 3535...35             (to)       21
  88 0de0b6b3a7640000      (value)     9
  80                       (data)      1
  80 80 80                 (v,r,s)     3
  --------------------------------------
  payload length = 44 bytes = 0x2c  (< 56 -> short list)

List prefix: 0xc0 + 0x2c = 0xec

Final RLP (45 bytes):
  ec 80 85 04a817c800 82 5208
     94 3535353535353535353535353535353535353535
     88 0de0b6b3a7640000 80 80 80 80
```

A *real* signed legacy transaction replaces the three `0x80` placeholders with the EIP-155 `v` and the 32-byte `r` and `s` (each encoded as `0xa0 ‖ <32 bytes>`). This pushes the payload past 55 bytes, so the list prefix switches to the long form `0xf8 <len>`.

---

## 4. Construction of the Input Data Field

### 4.1 Native transfer

For a native transfer, `data = 0x` (empty): there is no selector and no ABI encoding. (Sending empty calldata *to a contract* would invoke its `receive()` function if present, otherwise its `fallback()`; non-empty calldata whose selector matches no function invokes `fallback()`. These cases are irrelevant when the recipient is an EOA.)

### 4.2 Function selector

For a contract call, `data = selector ‖ ABI-encoded arguments` and `to` is the contract address [12]. The **selector** is the first 4 bytes (the high-order bytes) of `keccak256` of the function's *canonical signature*. The hash is the Ethereum-padding Keccak-256, **not** NIST SHA3-256. The canonical signature is the function name followed by a parenthesized, comma-separated list of canonical parameter types, with **no spaces, no parameter names, no return types, and no data-location keywords**:

- use `uint256`/`int256`, never the `uint`/`int` aliases; `fixed128x18`, never bare `fixed`;
- retain `address`, `bool`, `bytesM`, `bytes`, `string`;
- render tuples/structs as `(T1,T2,…)`; append `[]` or `[k]` for arrays.

### 4.3 Argument encoding

Every value is padded to a 32-byte (256-bit) word. The encoding is **not** self-describing — the decoder must know the static types [12]:

- `uintN`, `address` (= `uint160`), `bool` (1 = true, 0 = false): right-aligned, **left** zero-padded;
- `intN`: big-endian two's-complement, **sign-extended** on the left (`0xff` bytes for negative values, `0x00` otherwise);
- `bytesM` (fixed, `1 ≤ M ≤ 32`): left-aligned with **trailing** zero padding;
- dynamic `bytes`/`string`: `enc(length)` followed by the bytes right-padded to a 32-byte multiple (a `string` is its UTF-8 bytes);
- dynamic `T[]`: `enc(length)` followed by the elements as a tuple; fixed `T[k]`: a `k`-element tuple (dynamic iff `T` is dynamic).

### 4.4 Head–tail layout for dynamic types

For an argument tuple `(X1 … Xk)`, the encoding is `head(X1) … head(Xk) tail(X1) … tail(Xk)`. A *static* `Xi` has `head = enc(Xi)` and an empty tail. A *dynamic* `Xi` has `head = a 32-byte offset` — measured **in bytes**, from the start of *this tuple's* encoding to where its tail begins — and `tail = enc(Xi)`. The first dynamic offset therefore equals the total head size (`k · 32` for a flat argument list).

### 4.5 Worked example: ERC-20 `transfer(address,uint256)`

Both arguments are static, so the encoding is head-only with no tail. With recipient `0x5B38Da6a701c568545dCfcB03FcB875f56beddC4` and amount `1e18`, the calldata is shown in Listing 2.

**Listing 2.** ABI calldata for ERC-20 `transfer(address,uint256)`.

```
selector = keccak256("transfer(address,uint256)")[:4] = 0xa9059cbb
  (full hash: a9059cbb2ab09eb219583f4a59a5d0623ade346d962bcd4e46b11da047c9049b)

data (68 bytes):
  a9059cbb                                                          <- 4-byte selector
  0000000000000000000000005b38da6a701c568545dcfcb03fcb875f56beddc4  <- word1: address, left-padded 12 zero bytes (uint160)
  0000000000000000000000000000000000000000000000000de0b6b3a7640000  <- word2: uint256 amount = 1000000000000000000

Concatenated:
  0xa9059cbb0000000000000000000000005b38da6a701c568545dcfcb03fcb875f56beddc40000000000000000000000000000000000000000000000000de0b6b3a7640000

Transaction fields:  to = <ERC-20 contract address>,  value = 0,  data = <the 68 bytes above>
```

> **Remark 4 (token decimals).** The amount `1e18` assumes 18 decimals. Token amounts depend on the token's `decimals()`; for example, USDC uses 6 decimals, so 1 USDC is `1000000`, not `1e18`.

Listing 3 shows the canonical specification example for dynamic arguments, `sam(bytes,bool,uint256[])` applied to `("dave", true, [1,2,3])` (selector `0xa5643bf2`). Offsets count bytes from the start of the argument tuple; the first offset equals the head size (3 words = `0x60`).

**Listing 3.** ABI calldata with dynamic arguments (offsets in bytes from the tuple start).

```
a5643bf2
0000…0060   head[0] bytes  -> offset 0x60 = 96  (= 3 head words)
0000…0001   head[1] bool   -> 1 (static, inline)
0000…00a0   head[2] uint[] -> offset 0xa0 = 160
0000…0004   tail bytes: length 4
6461766500…0000   tail bytes: "dave" right-padded
0000…0003   tail array: length 3
0000…0001 / 0000…0002 / 0000…0003   elements
```

This standard call encoding is distinct from `abi.encodePacked`, which is **not** used for external-call calldata.

---

## 5. Signing-Digest Formation

The signed digest is a 32-byte `keccak256` hash over a type-specific serialization of the fields **excluding the signature** (Yellow Paper [1], Appendix F; EIP-155 [3]; EIP-1559 [5]). The general rule is that legacy transactions hash a plain RLP list, whereas typed transactions prepend the type byte by **concatenation**, not by RLP-encoding it: `h(T) = keccak256(Tx ‖ RLP(L_X(T)))`. The digest for each variant is given by Equations (1)–(3).

**Legacy, pre-EIP-155** — 6 elements (valid on any EVM chain; replay risk):

```
(1)   sighash = keccak256( RLP([nonce, gasPrice, gasLimit, to, value, data]) )
```

**Legacy, EIP-155** — 9 elements; append `chainId, 0, 0`, where the two zeros occupy the `r` and `s` slots of the pre-image:

```
(2)   sighash = keccak256( RLP([nonce, gasPrice, gasLimit, to, value, data, chainId, 0, 0]) )
```

**EIP-1559 Type 2** — 9 fields, prefixed with `0x02`, no signature:

```
(3)   sighash = keccak256( 0x02 ‖ RLP([chainId, nonce, maxPriorityFeePerGas, maxFeePerGas,
                                       gasLimit, to, value, data, accessList]) )
```

For completeness, the EIP-2930 Type 1 digest is `keccak256(0x01 ‖ RLP([chainId, nonce, gasPrice, gasLimit, to, value, data, accessList]))`. EIP-1559 states verbatim that the signature elements "represent a secp256k1 signature over `keccak256(0x02 || rlp([chain_id, nonce, max_priority_fee_per_gas, max_fee_per_gas, gas_limit, destination, amount, data, access_list]))`", confirming Equation (3).

---

## 6. Cryptographic Signing

### 6.1 ECDSA over secp256k1

Signing computes `(v, r, s) = ECDSA_SIGN(sighash, privKey)` on the secp256k1 curve `y² = x³ + 7` over the prime field `p = 2²⁵⁶ − 2³² − 977`. The scalars `r` and `s` are 32 bytes each. The group order is

```
n = secp256k1n
  = 0xFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFEBAAEDCE6AF48A03BBFD25E8CD0364141
  = 115792089237316195423570985008687907852837564279074904382605163141518161494337.
```

> **Remark 5 (verification — provenance of curve constants).** The parameters `a = 0` and cofactor `h = 1` are standard secp256k1 facts but trace to SEC 2 [14] rather than to the Yellow Paper text examined. The order `n`, the prime `p`, and `b = 7` are confirmed against the Yellow Paper [1] and the execution specifications [11].

The **recovery identifier** `recId ∈ {0,1}` encodes the parity of the `y`-coordinate of the curve point whose `x`-coordinate is `r`: `0` denotes even `y`, `1` denotes odd `y`. (The Yellow Paper admits a raw range `[0,3]` but declares the upper two invalid for transactions.)

### 6.2 Recovery identifier and the `v` / `yParity` encodings

The stored signature-parity value depends on the transaction form, as summarized in Table 5.

**Table 5.** Encoding of the recovery identifier (`chainId = 1` shown for illustration).

| Form | Stored value | Example |
|---|---|---|
| Legacy pre-155 | `v = recId + 27` | 27 or 28 |
| Legacy EIP-155 | `v = recId + chainId·2 + 35` | 37 or 38 |
| Typed (1, 2, …) | `yParity = recId` (stored directly as 0 or 1) | 0 or 1 |

EIP-155 states that "the `v` of the signature MUST be set to `{0,1} + CHAIN_ID*2 + 35`", with the six-field fallback retaining `v = {0,1} + 27`. For typed transactions, `chainId` is a dedicated field and the stored value is the bare parity bit.

On the **decode** side (recovering `recId` prior to `ecrecover`): legacy pre-155 gives `recId = v − 27`; legacy EIP-155 gives `recId = (v − 35) mod 2` and `chainId = (v − 35) / 2`; typed gives `recId = yParity`.

### 6.3 Signature malleability and the EIP-2 low-`s` constraint

EIP-2 [13] (Homestead) declares that "all transaction signatures whose s-value is greater than `secp256k1n/2` are now considered invalid." The Yellow Paper [1] encodes the validity constraints as

```
0 < r < secp256k1n
0 < s < secp256k1n/2 + 1     (canonical LOW-S; stricter than the ecrecover precompile, which permits s < secp256k1n)
v ∈ {0, 1}
```

with

```
secp256k1n/2 = 0x7FFFFFFFFFFFFFFFFFFFFFFFFFFFFFFF5D576E7357A4501DDFE92F46681B20A0
             = 57896044618658097711785492504343953926418782139537452191302581570759080747168.
```

The rationale is malleability: for any valid `(r, s)`, the pair `(r, n − s)` with flipped parity is also a valid signature but yields a different transaction hash. Restricting `s ≤ n/2` selects a single canonical signature and thereby eliminates this malleability, which is otherwise a hazard for user interfaces, accounting, and dependency tracking. Most libraries — including go-ethereum's secp256k1 backends — already emit low-`s` signatures.

### 6.4 Signature attachment and sender recovery

The final transaction replaces the signature placeholders: a legacy transaction sets `(v, r, s)` in the last three list slots, while a typed transaction appends `(yParity, r, s)` to the RLP list. The result is then serialized per Section 3. Sender recovery (Yellow Paper `S(T)`) proceeds as

```
pubkey = ECDSARECOVER(sighash, recId, r, s)   # 64-byte uncompressed key (two 256-bit integers; no 0x04 prefix)
sender = keccak256(pubkey)[12:32]             # the rightmost 160 bits = the last 20 of the 32 hash bytes
```

The on-chain `ecrecover` precompile (address `0x01`) takes `v ∈ {27,28}`, applies the looser bound `s < secp256k1n`, and returns `keccak256(pubkey)[12:32]` left-padded to 32 bytes. An implementation invariant is that recovering the sender from a self-signed transaction must yield the signer's own address.

---

## 7. Implementation in Go (go-ethereum)

All listings compile against go-ethereum v1.17.3 [15], the version pinned by this repository. The shared skeleton is: build the transaction → (for contract calls only) ABI-pack `data` → select a signer → hash (implicit in `SignTx`, explicit in the low-level path) → sign → `MarshalBinary` → hex-encode.

### 7.1 Relevant go-ethereum APIs

The following facts were verified against the v1.17.3 source [15]:

- `types.NewTx(&types.DynamicFeeTx{…})` / `&types.LegacyTx{…}` build the unsigned transaction. The monetary fields (`Value`, `GasPrice`, `GasFeeCap`, `GasTipCap`) are `*big.Int` end-to-end, so amounts exceeding `2⁶⁴` wei are preserved.
- `To == nil` denotes **contract creation**; a function call must set `To` to the contract address. Empty calldata is best passed as `[]byte{}` (RLP `0x80`), not `nil`.
- `types.LatestSignerForChainID(chainID)` returns the most permissive signer when `chainID` is non-nil; a nil or zero `chainID` falls back to the replay-unprotected Homestead signer and should be rejected.

> **Remark 6 (verification — the signer is Prague, not London; corrected).** An initial claim that `LatestSignerForChainID` returns a "London" signer was *refuted*. In v1.17.3 its body is `if chainID != nil { signer = NewPragueSigner(chainID) } else { signer = HomesteadSigner{} }` — that is, a **Prague** signer, and `NewLondonSigner` is *not* equivalent: `NewPragueSigner` is a strict superset that additionally accepts EIP-4844 blob and EIP-7702 set-code transactions. However, both produce identical `V` encodings for Type 0 (EIP-155 `chainId·2 + 35/36`) and Type 2 (bare `yParity` 0/1), so every functional consequence in this section is unchanged. `LatestSignerForChainID` is preferred for forward compatibility.

- `crypto.Sign(digestHash, priv)` requires `digestHash` to be **exactly 32 bytes** (returning an *error*, not panicking, otherwise) and returns a **65-byte** `[R ‖ S ‖ V]` signature with `V` (the recovery id) equal to 0 or 1 in the last byte. The relevant constants are `DigestLength = 32`, `SignatureLength = 65`, and `RecoveryIDOffset = 64`. Its output is already canonical low-`s` (enforced by the underlying decred/libsecp256k1 backend rather than by an explicit normalization step), so no manual low-`s` or ±27 adjustment is needed before `WithSignature`.
- `tx.WithSignature(signer, sig)` takes the 65-byte `[R ‖ S ‖ V]` signature, returns a **new** signed `*Transaction` (the original is unmodified), and lets the signer convert `V` to the per-type encoding.
- `signedTx.MarshalBinary()` produces the broadcast serialization: plain RLP for legacy, and `type byte ‖ RLP(payload)` for typed transactions (a leading `0x02` for EIP-1559). `signedTx.Hash()` mirrors this. For a legacy transaction, `MarshalBinary()` equals `rlp.EncodeToBytes(signedTx)`.

### 7.2 Native transfer, EIP-1559 (high-level path)

Listing 4 is the high-level path, identical in structure to the one used by this repository.

**Listing 4.** Native ETH transfer as an EIP-1559 transaction (high-level `types.SignTx`).

```go
package main

import (
	"crypto/ecdsa"
	"fmt"
	"math/big"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/common/hexutil"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/crypto"
)

func transferEIP1559(priv *ecdsa.PrivateKey) (string, error) {
	chainID := big.NewInt(1) // mainnet; MUST be non-zero (0/nil -> Homestead, no replay protection)
	to := common.HexToAddress("0x70997970C51812dc3A010C7d01b50e0d17dc79C8")

	// 1. Build the unsigned EIP-1559 tx (values are *big.Int, no uint64 cast).
	tx := types.NewTx(&types.DynamicFeeTx{
		ChainID:   chainID,
		Nonce:     0,
		GasTipCap: big.NewInt(2_000_000_000),            // maxPriorityFeePerGas: 2 gwei
		GasFeeCap: big.NewInt(30_000_000_000),           // maxFeePerGas: 30 gwei
		Gas:       21000,                                // plain ETH transfer
		To:        &to,                                  // non-nil; nil would mean contract creation
		Value:     big.NewInt(1_000_000_000_000_000_000), // 1 ETH in wei
		Data:      nil,                                  // empty calldata
		// AccessList left nil (empty)
	})

	// 2. Select the signer (EIP-155 + all typed types when chainID != nil).
	signer := types.LatestSignerForChainID(chainID)

	// 3. Sign (the 32-byte sighash is computed inside SignTx via signer.Hash(tx)).
	signedTx, err := types.SignTx(tx, signer, priv)
	if err != nil {
		return "", fmt.Errorf("sign: %w", err)
	}

	// 4. Defensive sender check (repo pattern, signer.go:176).
	from, err := types.Sender(signer, signedTx)
	if err != nil {
		return "", fmt.Errorf("recover sender: %w", err)
	}
	if from != crypto.PubkeyToAddress(priv.PublicKey) {
		return "", fmt.Errorf("sender mismatch: %s", from.Hex())
	}

	// 5. Serialize for broadcast: type 2 -> 0x02 || RLP(payload).
	raw, err := signedTx.MarshalBinary()
	if err != nil {
		return "", fmt.Errorf("marshal: %w", err)
	}
	fmt.Println("txHash:", signedTx.Hash().Hex())
	return hexutil.Encode(raw), nil // "0x02..." for eth_sendRawTransaction
}
```

### 7.3 Native transfer, legacy (low-level path)

Listing 5 shows the explicit `signer.Hash → crypto.Sign → tx.WithSignature` path (documented here but not used by the repository) and legacy `MarshalBinary` (plain RLP, no type prefix).

**Listing 5.** Native ETH transfer as a legacy (EIP-155) transaction (low-level signing path).

```go
func transferLegacy(priv *ecdsa.PrivateKey) (string, error) {
	chainID := big.NewInt(1)
	to := common.HexToAddress("0x70997970C51812dc3A010C7d01b50e0d17dc79C8")

	tx := types.NewTx(&types.LegacyTx{
		Nonce:    0,
		GasPrice: big.NewInt(20_000_000_000), // 20 gwei
		Gas:      21000,
		To:       &to,
		Value:    big.NewInt(1_000_000_000_000_000_000), // 1 ETH
		Data:     nil,
	})

	signer := types.LatestSignerForChainID(chainID) // EIP-155 replay protection via non-zero chainID

	h := signer.Hash(tx)            // 32-byte signing hash (common.Hash)
	sig, err := crypto.Sign(h[:], priv) // 65-byte [R||S||V], canonical low-s, V in {0,1}
	if err != nil {
		return "", fmt.Errorf("crypto.Sign: %w", err)
	}
	signedTx, err := tx.WithSignature(signer, sig) // converts V to chainID*2+35/36; returns NEW tx
	if err != nil {
		return "", fmt.Errorf("WithSignature: %w", err)
	}

	raw, err := signedTx.MarshalBinary() // legacy: plain RLP (no type-byte prefix)
	if err != nil {
		return "", fmt.Errorf("marshal: %w", err)
	}
	return hexutil.Encode(raw), nil // "0xf8..."
}
```

### 7.4 Contract invocation

`abi.JSON(…).Pack("method", args…)` produces the 4-byte selector together with the 32-byte-padded arguments. Go argument types must match the ABI types (`address → common.Address`, `uint256 → *big.Int`, `bytes → []byte`, and so on). `To` is the contract and `Value` is 0 (Listing 6).

**Listing 6.** Smart-contract call (ERC-20 `transfer`) as an EIP-1559 transaction, with ABI packing.

```go
package main

import (
	"crypto/ecdsa"
	"fmt"
	"math/big"
	"strings"

	"github.com/ethereum/go-ethereum/accounts/abi"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/common/hexutil"
	"github.com/ethereum/go-ethereum/core/types"
)

// Minimal ERC-20 ABI containing only the method we call.
const erc20ABI = `[{"name":"transfer","type":"function","inputs":[{"name":"to","type":"address"},{"name":"amount","type":"uint256"}],"outputs":[{"name":"","type":"bool"}]}]`

func erc20TransferEIP1559(priv *ecdsa.PrivateKey) (string, error) {
	chainID := big.NewInt(1)
	contract := common.HexToAddress("0xA0b86991c6218b36c1d19D4a2e9Eb0cE3606eB48") // USDC
	recipient := common.HexToAddress("0x70997970C51812dc3A010C7d01b50e0d17dc79C8")
	amount := big.NewInt(1_000_000) // 1 USDC (6 decimals)

	// 0. ABI-encode the call data: 4-byte selector + 32-byte-padded args.
	parsedABI, err := abi.JSON(strings.NewReader(erc20ABI))
	if err != nil {
		return "", fmt.Errorf("parse abi: %w", err)
	}
	data, err := parsedABI.Pack("transfer", recipient, amount)
	if err != nil {
		return "", fmt.Errorf("abi pack: %w", err)
	}

	// 1. Build the tx. To = CONTRACT (not nil), Value = 0, Data = packed calldata.
	tx := types.NewTx(&types.DynamicFeeTx{
		ChainID:   chainID,
		Nonce:     0,
		GasTipCap: big.NewInt(2_000_000_000),
		GasFeeCap: big.NewInt(30_000_000_000),
		Gas:       60000, // contract calls cost > 21000; use eth_estimateGas in production
		To:        &contract,
		Value:     big.NewInt(0),
		Data:      data,
	})

	// 2-3. Select signer and sign.
	signer := types.LatestSignerForChainID(chainID)
	signedTx, err := types.SignTx(tx, signer, priv)
	if err != nil {
		return "", fmt.Errorf("sign: %w", err)
	}

	// 4. Serialize (type 2 -> 0x02 || RLP(payload)).
	raw, err := signedTx.MarshalBinary()
	if err != nil {
		return "", fmt.Errorf("marshal: %w", err)
	}
	fmt.Println("selector+args:", hexutil.Encode(data)) // 0xa9059cbb...
	fmt.Println("txHash:", signedTx.Hash().Hex())
	return hexutil.Encode(raw), nil
}
```

For the legacy / low-level contract-call variant, combine the `abi.Pack` step of Listing 6 with the `signer.Hash → crypto.Sign → tx.WithSignature` pattern of Listing 5, building a `types.LegacyTx{}` with `To = &contract`, `Value = big.NewInt(0)`, and `Data = data`. The signature components can be read back with `signedTx.RawSignatureValues()` (where legacy `V = chainId·2 + 35/36`). For runnable examples only, a key may be loaded with `crypto.HexToECDSA("…")`; **real keys must never be hardcoded.** This repository instead decrypts a keystore via `accounts/keystore.DecryptKey(json, password)` and uses `key.PrivateKey`.

### 7.5 Reconciliation with `apps/eth-signer-mcp/internal/signing`

This repository already contains a correct, production-grade signer, verified by reading the package and its `go.mod`. It supports **legacy (Type 0, EIP-155)** and **EIP-1559 (Type 2)** only. The flow is: `validate.go` normalizes inputs → `build.go` (`buildTx`) calls `types.NewTx` with `&types.LegacyTx{}` or `&types.DynamicFeeTx{}` and selects `types.LatestSignerForChainID(chainID)` for both → `decrypt.go`'s `signingKey.SignTx` delegates to the high-level `types.SignTx` → `signer.go` serializes with `signedTx.MarshalBinary()` to `"0x"+hex` and performs a defensive `types.Sender()` round-trip. The signer explicitly rejects `chainId == 0` (which would select the replay-unprotected signer). It uses only the high-level path; `crypto.Sign` and `tx.WithSignature` appear nowhere in non-test code. Empty calldata is forced to `[]byte{}` so that RLP encodes `0x80`, and all amounts are `*big.Int`.

The single gap relevant to the contract-call operation is that the repository does **not** ABI-encode calls: `data` is accepted as caller-supplied, pre-encoded `0x` hex (capped at 256 KiB), and `accounts/abi` is never imported. A contract call therefore already works at the wire level — pass the packed calldata as `data` — but the `abi.JSON(…).Pack(…)` step must be performed by the caller before invoking the signer (precisely the step added in Listing 6). This is a deliberate design choice: server-side packing would widen the validation and attack surface and breach the package's offline, no-extra-dependencies posture. Other intentional scope limits are that Types 1/3/4 are excluded and `accessList` must be empty.

> **Remark 7 (documentation inconsistency, not a code defect).** A doc comment in `request.go` states "512 KiB", whereas the enforced cap (in `validate.go`) is `256 · 1024 = 262144` bytes.

---

## 8. Discussion: Common Implementation Errors

The following recurring error classes account for most invalid or malleable transactions produced by hand-rolled signers.

1. **Non-minimal RLP integers.** Encoding the scalar `0` as `0x00` instead of `0x80`, or `yParity = 0` as `0x00`. Decoders reject leading zeros; always reduce to minimal big-endian, with zero as `0x80`.
2. **Incorrect `v`.** Using `recId + 27` on an EIP-155 chain (which requires `recId + chainId·2 + 35`), or placing an EIP-155 `v` into a typed transaction (which uses bare `yParity` 0/1); also off-by-one errors in `recId`.
3. **High-`s` signature.** Submitting `s > secp256k1n/2`, invalid since EIP-2; normalize to low-`s` (go-ethereum's `crypto.Sign` does this).
4. **Omitting or zeroing `chainId`.** This drops replay protection and, in go-ethereum, silently selects the Homestead signer; reject `chainId == 0`.
5. **Incorrect `gasLimit`.** Hardcoding 21000 for a contract call (which then reverts or runs out of gas); estimate via `eth_estimateGas`.
6. **Nonce reuse.** Produces conflicting transactions, of which one is dropped or replaces the other.
7. **Signing the wrong pre-image.** RLP-encoding the type byte instead of concatenating it, including the signature in the hashed list, or hashing the broadcast bytes rather than the unsigned pre-image.
8. **Native-vs-token confusion.** Placing the token recipient in `to` (it belongs in `data`); `to` must be the token contract and `value` must be 0.
9. **`To = nil` on a call.** This triggers contract creation rather than a function call.
10. **Passing non-digest input to `crypto.Sign`.** It requires exactly 32 bytes and returns an error otherwise; always pass `signer.Hash(tx)`.

---

## 9. Conclusion

We have given a primary-source-verified account of the Ethereum transaction construction-and-signing pipeline for native transfers and contract invocations, showing that the two operations differ only in the `to`, `value`, `data`, and `gasLimit` fields. The pipeline is fully determined by RLP serialization (with its minimal-integer and prefix-band rules), the EIP-2718 typed envelope, per-type keccak-256 digest formation, and ECDSA over secp256k1 subject to the EIP-2 low-`s` constraint and the EIP-155 / typed parity encodings. The accompanying Go reference implementation realizes both operations end-to-end, and the reconciliation with `eth-signer-mcp` shows that the existing signer is correct and complete at the wire level for its supported types (legacy and EIP-1559), with ABI calldata construction left, by design, to the caller. Natural directions for future work are extending the signer to Types 1/3/4 and providing a shared ABI-encoding library module so that callers need not hand-roll calldata. The verification posture — adversarial checking of each load-bearing claim against a primary source, with refuted and uncertain items recorded as Remarks — is recommended for any change to consensus-critical serialization or signing code.

---

## References

[1] G. Wood, *Ethereum: A Secure Decentralised Generalised Transaction Ledger* (Ethereum Yellow Paper). https://ethereum.github.io/yellowpaper/paper.pdf

[2] *EIP-2718: Typed Transaction Envelope*, Ethereum Improvement Proposals. https://eips.ethereum.org/EIPS/eip-2718

[3] *EIP-155: Simple Replay Attack Protection*, Ethereum Improvement Proposals. https://eips.ethereum.org/EIPS/eip-155

[4] *EIP-2930: Optional Access Lists*, Ethereum Improvement Proposals. https://eips.ethereum.org/EIPS/eip-2930

[5] *EIP-1559: Fee Market Change for ETH 1.0 Chain*, Ethereum Improvement Proposals. https://eips.ethereum.org/EIPS/eip-1559

[6] *EIP-4844: Shard Blob Transactions*, Ethereum Improvement Proposals. https://eips.ethereum.org/EIPS/eip-4844

[7] *EIP-7702: Set Code Transaction for EOAs*, Ethereum Improvement Proposals. https://eips.ethereum.org/EIPS/eip-7702

[8] *Transaction Types*, MetaMask Developer Documentation. https://docs.metamask.io

[9] *EIP-7623: Increase Calldata Cost*, Ethereum Improvement Proposals. https://eips.ethereum.org/EIPS/eip-7623

[10] *Recursive-Length Prefix (RLP) Serialization*, Ethereum.org Developer Documentation. https://ethereum.org/en/developers/docs/data-structures-and-encoding/rlp/

[11] *Ethereum Execution Layer Specification (execution-specs)*. https://github.com/ethereum/execution-specs

[12] *Contract ABI Specification*, Solidity Documentation. https://docs.soliditylang.org/en/latest/abi-spec.html

[13] *EIP-2: Homestead Hard-fork Changes*, Ethereum Improvement Proposals. https://eips.ethereum.org/EIPS/eip-2

[14] *SEC 2: Recommended Elliptic Curve Domain Parameters*, v2.0, Standards for Efficient Cryptography Group (SECG), 2010. https://www.secg.org/sec2-v2.pdf

[15] *go-ethereum*, v1.17.3 — `core/types/transaction.go`, `core/types/transaction_signing.go`, `crypto/signature_cgo.go` / `signature_nocgo.go`, `accounts/abi`. https://github.com/ethereum/go-ethereum/tree/v1.17.3

---

## Appendix A. Practitioner Checklist

- [ ] **Transaction type:** EIP-1559 Type 2 by default; legacy (EIP-155) only for compatibility; never pre-155 (no replay protection).
- [ ] **Operation fields:** native transfer → `to` = EOA, `value` > 0, `data` = `0x`, `gasLimit` = 21000. Contract call → `to` = contract, `value` = 0 (unless payable), `data` = selector + args, `gasLimit` from `eth_estimateGas`.
- [ ] **Calldata:** build `data` = `keccak256(canonicalSig)[:4]` + 32-byte-word ABI arguments (`abi.Pack` in Go).
- [ ] **chainId:** present and non-zero.
- [ ] **nonce:** correct, monotonic, never reused for a given sender.
- [ ] **Digest:** typed = `keccak256(typeByte ‖ RLP(fields-without-signature))`; EIP-155 legacy = `keccak256(RLP([…, chainId, 0, 0]))`.
- [ ] **Signature:** low-`s` `(r, s)` and `recId ∈ {0,1}`; encode `v` (legacy `recId + chainId·2 + 35`) or `yParity` (typed, 0/1).
- [ ] **Serialization:** legacy = plain RLP; typed = `typeByte ‖ RLP([…, yParity, r, s])`; in Go, `signedTx.MarshalBinary()`.
- [ ] **Verification:** recover the sender from the signed transaction and confirm it matches the expected address.
