# Research: Offline Byte-Identical Parity for `eth-signer-mcp` Signed Transactions

## Verdict

Yes, with caveats. We can verify, fully offline and reproducibly, that our signed-transaction output is byte-identical to two reference signers (Foundry `cast mktx` and ethers.js v6) across legacy (EIP-155) and EIP-1559 transactions, by checking in a small set of golden vectors anchored on the EIP-155 spec's own worked example [1], plus deterministic fresh vectors generated once and committed as fixtures. The acceptance bar in the PRD (byte-identical RLP, signature verifies, RLP round-trips) is achievable as a pure Go test suite with **zero network access at test time** — but the Foundry CLI version MUST be pinned, the ethers v6 reference run MUST be a one-shot fixture generator (not run in CI), and a small list of parity gotchas (v vs yParity, `data: "0x"`, contract-creation, RLP leading-zero trimming) MUST be covered as explicit cases.

## Context

- **Question:** Can we prove our `eth-signer-mcp` output is the same bytes a developer would get from `cast wallet sign-tx` / `cast mktx` (Foundry) and ethers v6, **without** any network or live RPC, and reproducibly in CI?
- **Why it matters:** The PRD's success metric is byte-identical RLP against a reference signer for both transaction types and at least two chainIds, plus a clean `core/types.Transaction.UnmarshalBinary` round-trip [PRD §Success Metrics]. If we can't lock this down as offline golden tests, the acceptance bar is unverifiable in CI.

## Findings

### What works

#### 1. EIP-155 spec ships a complete worked example

EIP-155 itself contains a fully-specified test vector with private key, message, signing hash, and v/r/s — perfect as a golden vector with the strongest possible provenance (the spec). Verbatim values from the EIP [1]:

- nonce = `9`
- gasprice = `20 * 10^9` wei = `0x4a817c800`
- startgas (gas limit) = `21000` = `0x5208`
- to = `0x3535353535353535353535353535353535353535`
- value = `10^18` wei = `0xde0b6b3a7640000`
- data = empty (RLP empty string `0x80`)
- chainId = `1`
- private key = `0x4646464646464646464646464646464646464646464646464646464646464646`

Expected signing-message RLP (the EIP-155 "to be signed" pre-image, with the chainId trick: `[nonce, gasprice, startgas, to, value, data, chainId, 0, 0]`):

```
0xec098504a817c800825208943535353535353535353535353535353535353535880de0b6b3a764000080018080
```

Expected keccak256 signing hash:

```
0xdaf5a779ae972f972197303d7b574746c7ef83eadac0f2791ad23db92e4c8e53
```

Expected signature:

- `v = 37` (i.e. `chainId * 2 + 35 = 1*2+35 = 37`)
- `r = 0x28ef61340bd939bc2195fe537567866003e1a15d3c71ff63e1590620aa636276`
- `s = 0x67cbe9d8997f761aecb703304b3800ccf555c9f3dc64214b297fb1966a3b6d83`

These are decimal `18515461264373351373200002665853028612451056578545711640558177340181847433846` (r) and `46948507304638947509940763649030358759909902576025900602547168820602576006531` (s) in the spec text [1].

This gives us a tier-0 golden vector that is byte-pinned by the EIP itself — independent of any tool's version drift.

#### 2. go-ethereum gives us all the primitives we need

From the official `core/types` API [9]:

- `types.NewTx(inner TxData) *Transaction` (note: per overview §3, this *copies* the inner; "wraps" is loose).
- Inner txdata types `LegacyTx{Nonce, GasPrice, Gas, To, Value, Data, V, R, S}`, `DynamicFeeTx{ChainID, Nonce, GasTipCap, GasFeeCap, Gas, To, Value, Data, AccessList, V, R, S}`, `AccessListTx{...}`.
- `types.LatestSignerForChainID(chainID *big.Int) Signer` — yields an EIP-155 signer for `chainID != 0`, falls back to `HomesteadSigner` for nil/zero (geth implementation detail per overview §3) [9].
- `types.SignTx(tx, signer, privKey) (*Transaction, error)` — produces the signed tx.
- `tx.MarshalBinary() ([]byte, error)` — produces the canonical wire encoding (legacy = bare RLP; typed = `type-byte || rlp(payload)`) [9].
- `tx.UnmarshalBinary(b []byte) error` — decodes the canonical encoding for both legacy and EIP-2718 typed transactions, satisfying the round-trip acceptance requirement [9].
- `tx.Hash() common.Hash` — final tx hash (after signature).
- `tx.RawSignatureValues() (v, r, s *big.Int)` — pulls out `{r, s, v}` for our P0 output contract.
- `tx.WithSignature(signer, sig)` — accepts a signature in `[R || S || V]` form where V is 0 or 1, for the "sign-the-hash-ourselves" path [search result, [9]].

Low-s parity holds on both go-ethereum build paths (cgo libsecp256k1 and the nocgo decred fallback) because the *underlying libraries* produce canonical low-s — not because geth runs an explicit normalize step (per overview §3 caveat). This is the load-bearing reason our output will match `cast` / ethers byte-for-byte without us doing any extra normalization.

#### 3. EIP-1559 wire format is fully specified

From EIP-1559 [2] (re-confirming the overview's §3 caveat — the spec field is literally `signature_y_parity`):

- Wire format: `0x02 || rlp([chain_id, nonce, max_priority_fee_per_gas, max_fee_per_gas, gas_limit, destination, amount, data, access_list, signature_y_parity, signature_r, signature_s])`
- Signing pre-image: `keccak256(0x02 || rlp([chain_id, nonce, max_priority_fee_per_gas, max_fee_per_gas, gas_limit, destination, amount, data, access_list]))`
- `signature_y_parity` is the LSB of the y-coordinate, encoded as boolean (0 or 1).

The single `0x02` type byte placement is the EIP-2718 typed-envelope rule [3]:

- Bytes `0x00`–`0x7f` → typed transaction.
- Bytes `0xc0`–`0xfe` → legacy (the leading byte is always the RLP list prefix).
- `0xff` → reserved.

This is what lets `core/types.UnmarshalBinary` unambiguously discriminate types from the first byte [9].

#### 4. Foundry `cast mktx` is the correct offline reference

Per the Foundry CLI reference [4][5], `cast mktx` does exactly what we need:

- Synopsis: `cast mktx [OPTIONS] [TO] [SIG] [ARGS]...` (or `--create <BYTECODE>` for contract creation).
- `--private-key <RAW_PRIVATE_KEY>` — raw private key, no keystore needed for the test oracle.
- `--nonce <NONCE>`, `--chain <CHAIN>` (or chainId), `--gas-limit <GAS_LIMIT>`, `--gas-price <PRICE>`, `--priority-gas-price <PRICE>` (EIP-1559 only), `--value <VALUE>`.
- `--legacy` forces a legacy (type 0) transaction instead of the default EIP-1559.
- `--create` for contract-creation (omits `to`).
- Stdout: the signed RLP as `0x`-prefixed hex (broadcast-ready; the dapptools `seth mktx`/`seth publish` analogue) [10].

`cast wallet sign-tx` exists but its inputs are a partial JSON; `cast mktx` is the cleaner, version-stable offline oracle (matches overview §1 recommendation). **Per overview §3 caveat, cast stdout has drifted across nightlies** — Foundry has done "structured stdout contract" / `JsonEnvelope` work [7]. Pin to a single stable tag when regenerating golden fixtures — any one stable tag satisfies the design; pick the current stable at implementation time (`v1.7.1`, released 2026-05-08, as of June 2026 [8]).

Foundry stable cadence is still moving (v1.5.0 → v1.5.1 = bugfix for solc 0.8.31 [8]; nightlies daily [7]). **Recommendation: pin the exact stable tag in a `tools/regen-vectors.sh` script header and record it in the fixture filename or fixture metadata file.**

#### 5. ethers v6 BaseWallet is the correct offline JS reference

From ethers v6 [6] (cross-checked against the `base-wallet.ts` source):

- `new Wallet(privateKey)` (or `new BaseWallet(new SigningKey(privateKey))`) — provider is optional; *with no provider only offline methods can be used*.
- `wallet.signTransaction(tx: TransactionRequest): Promise<string>` — returns the signed serialized hex.
- `Transaction.from(txLike)` builds a Transaction; once signed it exposes `serialized` (signed RLP hex), `unsignedSerialized` (pre-image), `hash`, and a separate `signature` with `{r, s, v, yParity}` [6].
- For legacy: set `gasPrice` and `chainId`; for EIP-1559: set `maxFeePerGas`, `maxPriorityFeePerGas`, `chainId` (do **not** set `gasPrice`).
- `type: 0` forces legacy/EIP-155; default infers from fields.

This is more than enough to generate fixtures once with a known private key + tx params, capture `serialized`, and check in the result.

#### 6. RLP round-trip is built into `core/types`

`Transaction.UnmarshalBinary` decodes both legacy and EIP-2718 typed encodings [9]. The acceptance check becomes a trivial 3-liner: `MarshalBinary → bytes → UnmarshalBinary → assert tx.Hash() == original.Hash() && bytes2 == bytes1`. This satisfies the PRD's "RLP decodes cleanly via `core/types.Transaction.UnmarshalBinary` and round-trips back to the same hash" requirement [PRD §Success Metrics].

### What doesn't work (parity-breaking edge cases — must be tested explicitly)

These are the cases that look fine on paper but historically diverge across libraries. Each MUST appear as its own golden vector.

#### A. `v` vs `yParity` confusion (the most common bug)

- Legacy (EIP-155): `v = chainId * 2 + 35` or `+36`. For chainId=1 with parity 0, `v = 37`; with parity 1, `v = 38` [1].
- EIP-1559 (per spec): the field is literally `signature_y_parity` and it is exactly `0` or `1` [2]. Our output contract uses the conventional name "yParity" — the overview already notes this is a documentation issue, not a wire-format one.
- Wrong-direction bugs: emitting `v=27/28` on an EIP-155 chain (forgot the chainId offset), or emitting `v=37/38` inside a type-2 tx (wrong domain). Both produce a syntactically valid RLP that *decodes* but does not equal the reference signer.
- Test plan: assert numerical `v` on both transaction types and check the `signature_y_parity` byte position in the type-2 RLP directly.

#### B. Empty `data` ("0x")

- Geth: `Data: nil` and `Data: []byte{}` both RLP-encode to the empty string `0x80` — same bytes on the wire.
- ethers v6: `data: "0x"` and omitted are equivalent — same bytes.
- `cast mktx` without a SIG argument: empty data — same bytes.
- The PRD requires `data: "0x"` to be a valid input [PRD §Input/Output Contract]; the input validator must normalize to `[]byte{}` before constructing the geth inner tx.

#### C. Contract creation (`to` omitted)

- Geth: leave `To` as `nil` (it's `*common.Address`) — RLP-encodes as the empty string `0x80`.
- ethers v6: omit `to` or set `to: null` — same effect.
- `cast mktx`: use `--create <BYTECODE>` (no positional `TO`).
- Parity bug: passing `to = "0x"` or `to = "0x0000000000000000000000000000000000000000"` are *different* transactions; the zero address is a real address (and famously a token-burn address), not contract creation.
- Test plan: one golden vector per type for contract creation; assert the encoded `to` field is `0x80`.

#### D. Zero `value`

- Per RLP rules, zero integers encode as the empty string `0x80` (not `0x00`) [3].
- Geth's `big.Int` zero serializes correctly via `rlp` package; the trap is hand-rolling the RLP and emitting `0x00`.
- Test plan: one zero-value vector for each type; assert the `value` field encodes as `0x80`.

#### E. RLP leading-zero trimming

- RLP integer encoding requires the minimal byte representation: a leading `0x00` byte is forbidden. E.g. `nonce = 1` → `0x01`, *not* `0x0001`; `gasPrice = 0` → `0x80`, *not* `0x00`.
- `big.Int.Bytes()` already trims leading zeros; using it directly via geth's `rlp` package is safe.
- Where this bites you: parsing user-supplied hex like `"0x09"` into a `big.Int` is fine, but parsing `"0x0009"` and re-emitting must produce `0x09`. ethers v6 and `cast` both normalize; our input parser MUST normalize before constructing the tx.
- Test plan: include a golden vector with a hex input string that has *padded* leading zeros and assert byte-identical output.

#### F. chainId handling

- Legacy + EIP-155 with `chainId = 0`: geth's `LatestSignerForChainID(0)` falls back to `HomesteadSigner` (v=27/28), *not* EIP-155 — overview §3 correction. The PRD requires `chainId` in input; we will reject `0` at the input validator (defensive) and rely on `LatestSignerForChainID(chainID)` for everything else.
- EIP-1559: chainId is in the payload itself and is mandatory — there is no chainId=0 case for type 2 in practice.
- Test plan: one mainnet (`0x1`) and one non-mainnet (`0xaa36a7` = Sepolia, or `0x5` = Goerli) vector per type, per the PRD success metric.

### Open questions

- **Single Foundry pin vs matrix.** Single pin is enough to satisfy the PRD; a matrix would catch regressions but doubles CI complexity. Recommendation: single pin, captured in fixture metadata.
- **Should we also generate vectors from `web3.py` / `py-evm`?** Two oracles (geth-flavored Foundry + ethers v6) are the minimum the PRD asks for. A third (Python) is optional; if added, it'd be a one-off generator script, not in CI.
- **Where to store the fixture-generator scripts.** Proposed: `apps/eth-signer-mcp/internal/signer/testdata/regen.sh` (or per-tool sub-scripts). Treat as "manual run before bumping pins"; do not invoke in CI.

## Proof of Concept

### Golden vector file shape (proposed, JSON)

One file per vector, checked into `apps/eth-signer-mcp/internal/signer/testdata/vectors/`:

```json
{
  "name": "eip155-spec-example",
  "source": "EIP-155 worked example (spec, primary)",
  "tool": "spec",
  "tool_version": "EIP-155 (final)",
  "private_key": "0x4646464646464646464646464646464646464646464646464646464646464646",
  "tx": {
    "type": "0x0",
    "chainId": "0x1",
    "nonce": "0x9",
    "to": "0x3535353535353535353535353535353535353535",
    "value": "0xde0b6b3a7640000",
    "data": "0x",
    "gas": "0x5208",
    "gasPrice": "0x4a817c800"
  },
  "expected": {
    "signing_hash": "0xdaf5a779ae972f972197303d7b574746c7ef83eadac0f2791ad23db92e4c8e53",
    "v": "0x25",
    "r": "0x28ef61340bd939bc2195fe537567866003e1a15d3c71ff63e1590620aa636276",
    "s": "0x67cbe9d8997f761aecb703304b3800ccf555c9f3dc64214b297fb1966a3b6d83",
    "raw_tx": "<filled by regen.sh from cast mktx --legacy --private-key ... --nonce 9 --chain 1 --gas-limit 21000 --gas-price 20000000000 --value 1ether 0x3535353535353535353535353535353535353535>",
    "tx_hash": "<filled by regen.sh>"
  }
}
```

For the EIP-155 spec example, the `expected.signing_hash` / `expected.v` / `expected.r` / `expected.s` are baked into the EIP itself; the `expected.raw_tx` / `expected.tx_hash` are filled once by the regen script and committed.

### Fixture-regeneration script (proposed shape — `regen.sh`, manual run)

```bash
#!/usr/bin/env bash
# Pin the tools. Update this header, regenerate, commit fixtures.
set -euo pipefail
FOUNDRY_PIN="v1.7.1"   # foundry-rs/foundry — current stable tag at implementation time
ETHERS_PIN="6.x"       # ethers v6 latest stable at regen time; capture in metadata

cast --version | tee /tmp/cast-version.txt

# EIP-1559 fresh vector, mainnet
cast mktx \
  --private-key "$PK" \
  --nonce 9 --chain 1 \
  --gas-limit 21000 \
  --gas-price 2000000000 \
  --priority-gas-price 1000000000 \
  --value 1ether \
  0x3535353535353535353535353535353535353535 \
  > vectors/eip1559-mainnet.raw

# Legacy/EIP-155 fresh vector, mainnet
cast mktx --legacy \
  --private-key "$PK" \
  --nonce 9 --chain 1 \
  --gas-limit 21000 \
  --gas-price 20000000000 \
  --value 1ether \
  0x3535353535353535353535353535353535353535 \
  > vectors/legacy-mainnet.raw

# Same with non-mainnet chainId (Sepolia = 11155111 = 0xaa36a7)
# ...

# ethers v6 cross-check, run via `node tools/ethers-regen.mjs`:
#   const tx = Transaction.from({...});
#   const wallet = new Wallet(PK);
#   const signed = await wallet.signTransaction(tx);
#   console.log(signed);
# Capture into vectors/*-ethers.raw, assert equal to *.raw (cast).
```

The CI test then never invokes these — it just reads the JSON fixtures, runs our signer over the same inputs, and asserts `our_raw == fixture.expected.raw_tx` byte-for-byte.

### Go-side parity test (proposed shape)

```go
// apps/eth-signer-mcp/internal/signer/parity_test.go
func TestParity_GoldenVectors(t *testing.T) {
    for _, fname := range mustList(t, "testdata/vectors/*.json") {
        t.Run(filepath.Base(fname), func(t *testing.T) {
            v := loadVector(t, fname)
            tx, sig, hash := signFromVector(t, v.PrivateKey, v.Tx)

            // (a) byte-identical raw RLP vs golden
            got := hexutil.Encode(mustBin(t, tx))
            if got != v.Expected.RawTx {
                t.Fatalf("raw tx mismatch:\n got  %s\n want %s", got, v.Expected.RawTx)
            }

            // (b) signature components match
            assertEq(t, sig.R, v.Expected.R)
            assertEq(t, sig.S, v.Expected.S)
            assertEq(t, sig.V, v.Expected.V)

            // (c) tx hash matches
            assertEq(t, hash.Hex(), v.Expected.TxHash)

            // (d) RLP round-trip via core/types.Transaction.UnmarshalBinary
            var rt types.Transaction
            if err := rt.UnmarshalBinary(common.FromHex(got)); err != nil {
                t.Fatalf("UnmarshalBinary: %v", err)
            }
            if rt.Hash() != hash {
                t.Fatalf("round-trip hash mismatch")
            }
        })
    }
}
```

### Acceptance flow (CI, fully offline)

1. CI runs `go test ./apps/eth-signer-mcp/...` only — no `cast`, no `node`.
2. Test reads each `vectors/*.json`, runs our signer, asserts byte-identical `rawTransaction` and matching `{r, s, v}` and `hash`.
3. Round-trip `UnmarshalBinary` assertion provides P0 acceptance for "RLP decodes cleanly … and round-trips back to the same hash" [PRD §Success Metrics].
4. A *separate* `make regen-vectors` target (not in CI) is the only place that needs `cast` and `node` installed; running it requires the pinned versions and produces a diff against the committed fixtures.

### Minimal golden-vector set to check in (recommended)

The smallest set that hits the PRD acceptance bar and covers the parity-breaking edge cases:

| # | Name | Type | chainId | `to` | `value` | `data` | Notes |
|---|------|------|---------|------|---------|--------|-------|
| 1 | `eip155-spec-example` | legacy | 1 | `0x3535…35` | 1 ETH | `0x` | Tier-0: spec-pinned [1] |
| 2 | `eip1559-mainnet-simple` | type 2 | 1 | `0x3535…35` | 1 ETH | `0x` | `cast` + ethers cross-check |
| 3 | `eip1559-sepolia-simple` | type 2 | 11155111 | `0x3535…35` | 1 ETH | `0x` | Non-mainnet chainId per PRD |
| 4 | `legacy-sepolia-simple` | legacy | 11155111 | `0x3535…35` | 1 ETH | `0x` | Non-mainnet chainId per PRD |
| 5 | `eip1559-contract-creation` | type 2 | 1 | *omitted* | 0 | non-trivial bytecode | `to`-omitted case |
| 6 | `legacy-zero-value` | legacy | 1 | `0x3535…35` | 0 | `0x` | `value=0` → RLP `0x80` |
| 7 | `eip1559-padded-nonce-input` | type 2 | 1 | `0x3535…35` | 1 ETH | `0x` | Input nonce is `"0x000009"`; output unaffected — validates input normalization |

Six fresh vectors (#2–#7) generated once by the pinned `cast mktx` + ethers v6, plus the spec-pinned #1, total 7 files. Each file is ~1 KB, so the checked-in fixture set is well under 10 KB.

## Effort Estimate

- Fixture format + spec example #1 hand-typed: ~1 hour.
- `tools/regen-vectors.sh` + `tools/ethers-regen.mjs` writing fresh vectors #2–#7: ~3 hours (most of it is wiring `cast mktx` argument permutations and confirming byte-equality between the two oracles for each).
- Go-side test scaffolding (vector loader + parity asserts + round-trip): ~2 hours.
- Total: well within Phase 2 of the PRD's milestones (the "P0 signing" PR). Adds <500 LoC to the app.

## Risks

- **Foundry stdout format drift (caveated by overview §3).** Stable releases (v1.5.0 → v1.5.1) plus visible "structured stdout contract" / `JsonEnvelope` work in the nightlies [7][8] mean `cast mktx`'s raw-hex output format could shift. **Mitigation:** Pin the exact stable tag in the regen script; capture the `cast --version` output in a sidecar file next to the fixtures so future regenerations can diff cleanly. The CI tests never invoke `cast`, so any drift only matters at fixture-regeneration time.
- **ethers v6 default type inference.** Omitting `gasPrice` and providing `maxFeePerGas` causes ethers to choose type 2; mixing them is ambiguous. **Mitigation:** Always set `type: 0` or `type: 2` explicitly in the regen script's TransactionRequest objects.
- **Geth nil-chainId fallback to Homestead** (per overview §3 correction). If a future code path inadvertently calls `LatestSignerForChainID(nil)`, we silently emit v=27/28 instead of v=37/38 — passes RLP decode, breaks parity. **Mitigation:** Input validator rejects `chainId = 0` and `chainId = nil` before the signer is called; one explicit unit test (not a parity vector) asserts the rejection.
- **Underlying secp256k1 library swap.** If go-ethereum upstream replaces the decred nocgo fallback or the cgo libsecp256k1 binding, the low-s guarantee could shift. **Mitigation:** Version-pin go-ethereum (overview §1 — `v1.17.3`); the round-trip + parity tests are the structural backstop. A change here would fail the parity tests loudly before reaching production.
- **AccessList in EIP-1559 RLP.** PRD allows `accessList: []` (empty array) but rejects non-empty for v1 [PRD §Input/Output Contract]. The empty access list still occupies a position in the RLP list (`0xc0`, an empty list). **Mitigation:** The geth `DynamicFeeTx{AccessList: nil}` and `AccessList: types.AccessList{}` cases must encode to the same bytes (`0xc0` for the empty list field). Add a unit test that confirms both Go paths produce identical bytes; the parity vectors already exercise the empty case implicitly.
- **MarshalBinary on legacy returns bare RLP, not type-prefixed.** Per EIP-2718 leading-byte rule, legacy txs are detected by the RLP list prefix (`0xc0`–`0xfe`) [3], so there is no `0x00` prefix on legacy. **Mitigation:** Document and unit-test this explicitly; a future contributor could mistakenly prepend `0x00` thinking "type 0 means a type byte of 0."

## Sources

[1] [EIP-155: Simple replay attack protection](https://eips.ethereum.org/EIPS/eip-155) — Ethereum Improvement Proposals. Provides the worked example used as our spec-pinned tier-0 golden vector (nonce 9, recipient `0x3535…35`, mainnet, private key `0x4646…46`, signing hash, v=37, r/s in both decimal and 32-byte hex).

[2] [EIP-1559: Fee market change for ETH 1.0 chain](https://eips.ethereum.org/EIPS/eip-1559) — Ethereum Improvement Proposals. Defines the type-2 wire format `0x02 || rlp([chain_id, ...])`, the signing pre-image, and the `signature_y_parity` field name.

[3] [EIP-2718: Typed Transaction Envelope](https://eips.ethereum.org/EIPS/eip-2718) — Ethereum Improvement Proposals. Defines the leading-byte discrimination (`0x00`–`0x7f` typed, `0xc0`–`0xfe` legacy) that makes `UnmarshalBinary` round-tripping work for both transaction types.

[4] [`cast mktx` reference](https://getfoundry.sh/cast/reference/mktx/) — Foundry official docs. CLI flags for the offline transaction-builder oracle: `--private-key`, `--nonce`, `--chain`, `--gas-limit`, `--gas-price`, `--priority-gas-price`, `--legacy`, `--value`, `--create`. Stdout is `0x`-prefixed signed RLP hex.

[5] [`cast mktx` Foundry Book page (book.getfoundry.sh redirect)](https://book.getfoundry.sh/reference/cast/cast-mktx) — Cross-reference for the same command synopsis.

[6] [ethers v6 Transactions API](https://docs.ethers.org/v6/api/transaction/) — Official ethers.js v6 docs. Documents `Transaction.from(txLike)`, `serialized` (signed RLP), `unsignedSerialized`, signature with `{r, s, v, yParity}`, and the field requirements for type 2 vs legacy.

[7] [foundry-rs/foundry releases](https://github.com/foundry-rs/foundry/releases) — GitHub release feed. Nightly cadence as of June 2026; ongoing work captioned "feat(cast): migrate cast wallet sign and sign-auth to documented output channels" and "feat(cast): structured stdout contract for cast wallet new" — substantiates the overview's "stdout has drifted; pin Foundry" caveat.

[8] [Foundry releases](https://github.com/foundry-rs/foundry/releases) — GitHub. Stable release history (v1.5.1 bugfix for solc 0.8.31; v1.7.0 2026-04-28; v1.7.1 2026-05-08, the current stable as of June 2026). Any single stable tag works as the pin in `regen-vectors.sh`; use the current stable at implementation time.

[9] [`go-ethereum/core/types` package docs](https://pkg.go.dev/github.com/ethereum/go-ethereum/core/types) — Official go-ethereum API reference. Signatures for `NewTx`, `LatestSignerForChainID`, `SignTx`, `MarshalBinary`, `UnmarshalBinary`, `Hash`, `RawSignatureValues`, `WithSignature` (`[R||S||V]` with V in {0,1}), and inner-tx structs `LegacyTx`, `DynamicFeeTx`, `AccessListTx`.

[10] [Foundry issue #1273 — "cast mktx / sign transactions without broadcasting"](https://github.com/foundry-rs/foundry/issues/1273) — Original design-intent issue: mirror `seth mktx`/`seth publish` semantics, sign without RPC, return raw hex on stdout — confirms `cast mktx` as the intended offline oracle.
