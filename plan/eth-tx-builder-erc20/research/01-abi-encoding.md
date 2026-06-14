> Reconstructed from the research-workflow transcript; figures reconciled against adversarial verification (see [`00-overview.md`](00-overview.md) §3).

# Research: ABI Encoding — Hand-Encoding ERC-20 Calldata in Pure Python Stdlib

**Angle:** `abi-encoding` (informs PRD §8 calldata encoding; §6, §10 decode paths)
**Template:** B — Technical Deep Dive

## Summary

Hand-encoding ERC-20 calldata and decoding `eth_call` returns in pure Python
stdlib (no `eth-abi`, `web3`, `pycryptodome`, or any Keccak library) is
straightforward and rests on three well-specified rules:

1. **Hardcode the six known 4-byte function selectors** derived from the
   canonical Keccak-256 hash of each signature — no live hashing needed.
2. **ABI-encode every static argument as a 32-byte word** — addresses are
   left-padded with 12 zero bytes (the low 20 bytes hold the address);
   `uint256` is left zero-padded big-endian.
3. **Decode return data as fixed-width words** (`decimals` = low byte of word 0;
   `allowance` = full word 0), or — for `symbol()` — follow the standard ABI
   dynamic-string layout (offset word + length word + UTF-8 bytes right-padded
   to a 32-byte multiple), falling back to a null-trimmed first-word read for
   legacy `bytes32` tokens (MKR is the canonical example).

All six selectors were verified against two independent corpora (the 4byte
directory and `metachris/eth-go-bindings`) **and** against the literal
Keccak-256 hash. Two `transfer` calldata blobs taken from real USDC mainnet
transactions on Etherscan give unit-test-grade reference vectors that decode
arithmetically.

## Function selectors

The function selector is the **first 4 bytes** (left, high-order in big-endian)
of the Keccak-256 (SHA-3) hash of the **canonical function signature**. The
signature is the function name + parenthesised comma-separated parameter types,
**no spaces and no parameter names**; return types are NOT part of the
signature.

| Function | Canonical signature | Selector | Notes |
|---|---|---|---|
| transfer | `transfer(address,uint256)` | `0xa9059cbb` | write |
| approve | `approve(address,uint256)` | `0x095ea7b3` | write |
| transferFrom | `transferFrom(address,address,uint256)` | `0x23b872dd` | write |
| decimals | `decimals()` | `0x313ce567` | read, OPTIONAL per EIP-20, returns `uint8` |
| symbol | `symbol()` | `0x95d89b41` | read, OPTIONAL per EIP-20, returns dynamic `string` (canonical) |
| allowance | `allowance(address,address)` | `0xdd62ed3e` | read, returns `uint256` |

Anchoring hashes (full Keccak-256, leading 4 bytes are the selector):

- `keccak256("transfer(address,uint256)")` =
  `0xa9059cbb2ab09eb219583f4a59a5d0623ade346d962bcd4e46b11da047c9049b`
- `keccak256("approve(address,uint256)")` =
  `0x095ea7b334ae44009aa867bfb386f5c3b4b443ac6f0ee573fa91c4608fbadfba`

Parameter names from EIP-20 (`_to`, `_value`, etc.) are omitted in the canonical
form. The signatures used are exactly:
`transfer(address,uint256)`, `approve(address,uint256)`,
`transferFrom(address,address,uint256)`, `decimals()`, `symbol()`,
`allowance(address,address)`.

### Selector-collision caveat

Every 32-bit selector collides with other obscure signatures in
`4byte.directory` (4-byte truncation of a 256-bit hash makes this inherent). It
is irrelevant here because this skill only ever **emits** the canonical ERC-20
selector — it never disambiguates an unknown incoming selector. Worth a one-line
code comment so a future *decoder* feature never looks up by selector blindly.

## Argument encoding (the 32-byte word rules)

Verbatim from the Solidity Contract ABI Specification:

- **address** — equivalent to `uint160`, i.e. 20 raw bytes, encoded as a 32-byte
  word by left-padding with 12 zero bytes (high-order side). "address: equivalent
  to uint160, except for the assumed interpretation and language typing."
- **uint<M>** — big-endian encoding, padded on the higher-order (left) side with
  zero-bytes such that the total length is 32 bytes. `uint256` is therefore a
  32-byte big-endian word with leading zero bytes.
- **dynamic string** — `enc(X) = enc(enc_utf8(X))` interpreted as `bytes`: a
  `uint256` byte-length prefix followed by the raw UTF-8 bytes, **right-padded**
  with zero bytes to a multiple of 32. When `string` is the only return value of
  a function, the return data begins with a 32-byte **offset word equal to
  `0x20` (32)** pointing to the length-and-bytes tail.

Note the padding asymmetry: integers and addresses are **left-padded**; strings
and bytes (and the UTF-8 bytes of a string) are **right-padded**.

### Calldata layout

```
calldata = selector (4 bytes) || arg0 (32 bytes) || arg1 (32 bytes) || ...
```

For `transfer(to, amount)`:

```
0xa9059cbb
000000000000000000000000 <20-byte to address>
<uint256 amount, big-endian, left zero-padded to 32 bytes>
```

In Python stdlib, each word is produced via `int.to_bytes(32, 'big')` (or
`format(n, '064x')` for the hex form); decode via `int.from_bytes(word, 'big')`
or `int(hex, 16)`. `bytes.fromhex` / `.hex()` round-trip the hex string. No
third-party big-int helper is needed. Everything is big-endian throughout.

## Verified real-world test vectors (USDC mainnet)

USDC contract: `0xA0b86991c6218b36c1d19D4a2e9Eb0cE3606eB48`, `decimals() = 6`
(confirmed on Etherscan). Both vectors below decode arithmetically.

**Vector A** — Etherscan tx
`0x1c6e8fe42957b559274996bf9accdecdbb7d0ba62101a1c7053060269388ba9d`

```
0xa9059cbb
000000000000000000000000890e560a6012bfa5d0d71a4a107dba4aed698f38
00000000000000000000000000000000000000000000000000000000c9a84c50
```
→ `transfer(0x890e560a6012bFA5d0d71a4a107dBa4Aed698f38, 3383250000)`
= **3,383.25 USDC** at 6 decimals. Check: `0xc9a84c50` = 3,383,250,000. ✓

**Vector B** — Etherscan tx
`0x924c1e6a83dfadc95cb1924bcacffbf9a0935ffcd0cd19293667976e049ba7b4`

```
0xa9059cbb
000000000000000000000000a49053f705a560f0717bc2e96dddcfe7edb7f98a
000000000000000000000000000000000000000000000000000000009502f900
```
→ `transfer(0xA49053F705A560f0717Bc2e96DdDcFe7EDB7f98a, 2500000000)`
= **2,500 USDC** at 6 decimals. Check: `0x9502f900` = 2,500,000,000. ✓

These double as drop-in encode/decode regression fixtures.

## Return-data decoding

### Static returns (single 32-byte word)

The EVM emits one 32-byte word for `decimals()` (a `uint8`) and `allowance()`
(a `uint256`). No generic ABI return decoder is needed:

- `decimals()` → `int(word_hex, 16) & 0xff` (mask the low byte; per PRD,
  reject suspicious values — the PRD caps at >36).
- `allowance()` → `int(word_hex, 16)` (full word).

### Dynamic `string` return — `symbol()`

When `symbol()` returns a single dynamic `string`, the return data is:

```
word 0:  offset = 0x20 (32)            # always 0x20 for a sole dynamic return
word 1:  length L (uint256)
word 2+: L UTF-8 bytes, right-padded with 0x00 to a 32-byte multiple
```

Decode: read the length word, slice `L` bytes from the tail, `.decode('utf-8')`.

### Legacy `bytes32` symbol fallback (MKR pattern)

Some legacy tokens return `bytes32` from `symbol()` rather than a dynamic
`string`. MKR (Maker, `0x9f8F72aA9304c8B593d555F12eF6589cC3A579A2`) is the
canonical example — its Etherscan ABI literally declares `symbol()` outputs
`bytes32`, and the `d-xo/weird-erc20` corpus names it as the reference
"Non-string metadata" token. For these, the raw 32-byte return is the symbol
left-aligned with trailing zero bytes (e.g. `b'MKR\x00\x00...'`), decoded by
trimming trailing NULs and decoding as UTF-8/ASCII. This is the fallback path
when the standard dynamic-string decode (reading the `0x20` offset word) fails
or yields non-printable output. `ethers.js` maintains a separate
`decodeBytes32String` path for exactly this case.

The v1 helper only needs the simple null-trimmed UTF-8 fallback (MKR pattern); a
bullet-proof decoder against every historical token shape is P2 / out of scope.

## Assumptions

- Selectors are kept as module-level hex-string constants (no live keccak
  hashing in `build_erc20.py`); the PRD §8 already locks this in.
- All four optional EIP-20 metadata helpers (name/symbol/decimals) actually
  exist on the token contract; the PRD already treats `decimals()` failure as
  fatal and `symbol()` failure as best-effort, so this assumption is bounded by
  graceful-degradation paths.
- The implementer uses Python 3 `int.to_bytes(32, 'big')` (or
  `format(n, '064x')`) and `bytes.fromhex` / `int(hex, 16)` — all stdlib, all
  deterministic — for every word encode/decode. No third-party big-int helpers.
- The legacy `bytes32` symbol fallback only needs the simple null-trimmed UTF-8
  case (MKR pattern); a fully bullet-proof decoder against every historical
  token shape (P2) is out of scope for the v1 helper.
- Static-return decoding reads the single 32-byte word the EVM emits; no generic
  ABI return decoder. `uint8` decode is `int(hex_word, 16) & 0xff`.
- The 4byte hash-collision warning is irrelevant: the helper only ever EMITS the
  canonical ERC-20 selector; it never disambiguates an unknown selector.
- Endianness is exclusively big-endian throughout — the EVM word, the hex
  rendering, and the Python conversions all agree.
- For the dynamic-string `symbol()` decode, the offset word is always `0x20`
  because `symbol()` returns exactly one value. A multi-value return would need
  a more general decoder (out of scope; EIP-20 `symbol()` returns one string).

## Sources

- [EIP-20: Token Standard](https://eips.ethereum.org/EIPS/eip-20) — Vitalik Buterin & Fabian Vogelsteller, 2015 (Final). Canonical function signatures; OPTIONAL status of name/symbol/decimals.
- [Contract ABI Specification (PlatON Solidity docs mirror)](https://platon-solidity.readthedocs.io/en/latest/abi-spec.html) — Solidity authors. Used because `docs.soliditylang.org` returned 403; content identical to upstream. Verbatim quotes for the function-selector rule, address ≡ uint160, uint<M> left-zero-padded big-endian, string `enc(enc_utf8(X))`, and the head/tail offset rule.
- [Understanding the Function Selector in Solidity](https://rareskills.io/post/function-selector) — RareSkills. Authoritative restatement of the 4-byte selector rule.
- [Deep Mental Models for Solidity ABI Encoding: Part 1](https://www.decipherclub.com/solidity-abi-encoding-part-1/) — DecipherClub. Worked transfer example with 32-byte word breakdown.
- [Deep Dive into abi.encode: Types, Padding, and Disassembly](https://medium.com/@scourgedev/deep-dive-into-abi-encode-types-padding-and-disassembly-84472f1b4543) — 0xScourgedev, Medium. Confirms strings/bytes are right-padded (vs. left-padding for integers/addresses).
- [erc20 Go bindings — metachris/eth-go-bindings](https://pkg.go.dev/github.com/metachris/eth-go-bindings/erc20) — selector table cross-confirmation for all six selectors.
- [Ethereum Signature Database — 0xa9059cbb](https://www.4byte.directory/signatures/?bytes4_signature=0xa9059cbb) — 4byte.directory. Independent selector lookup.
- [Etherscan tx 0x1c6e8fe42957…](https://etherscan.io/tx/0x1c6e8fe42957b559274996bf9accdecdbb7d0ba62101a1c7053060269388ba9d) — USDC transfer test vector #1.
- [Etherscan tx 0x924c1e6a83df…](https://etherscan.io/tx/0x924c1e6a83dfadc95cb1924bcacffbf9a0935ffcd0cd19293667976e049ba7b4) — USDC transfer test vector #2.
- [Etherscan USDC token page](https://etherscan.io/token/0xa0b86991c6218b36c1d19d4a2e9eb0ce3606eb48) — confirms USDC contract address + decimals = 6.
- [Etherscan MKR token page](https://etherscan.io/token/0x9f8f72aa9304c8b593d555f12ef6589cc3a579a2) — confirms `symbol()` declared return type is `bytes32`.
- [d-xo/weird-erc20 — Non-string metadata](https://github.com/d-xo/weird-erc20) — documents MKR's bytes32 metadata pattern; `Bytes32Metadata.sol` reference impl for the legacy fallback.
- [ethers-io/ethers.js discussion #4198 — bytes32 name decode](https://github.com/ethers-io/ethers.js/discussions/4198) — confirms a separate `decodeBytes32String` path is needed for legacy tokens like MKR.
