# Golden Signing Vectors

> **CI never invokes Foundry or Node.** These vectors are committed static JSON.
> `make test` / `make lint` have no dependency on `cast` or `node`.

Regeneration is a manual developer step (e.g. before a go-ethereum bump).
See [Regeneration Procedure](#regeneration-procedure) below.

---

## Test Key

**Single disclosure path:** see
[`../README.md`](../README.md) — the "Test Key" block there is the only place
the raw private key hex appears (besides `gen_fixtures.go`).

Address: `0x9858EfFD232B4033E47d90003D41EC34EcaEda94`

All 9 signing vectors use this key so the real vault (Issue 2.10's parity suite)
can reproduce them byte-for-byte.

---

## Vector Matrix

| File | Tx Type | ChainId | Edge Case |
|------|---------|---------|-----------|
| `legacy-mainnet.json` | 0 (legacy) | 1 | EIP-155 v = chainId×2+35/36 |
| `legacy-sepolia.json` | 0 (legacy) | 11155111 | Large-chainId EIP-155 v |
| `1559-mainnet.json` | 2 (EIP-1559) | 1 | yParity v = 0 or 1 |
| `1559-sepolia.json` | 2 (EIP-1559) | 11155111 | yParity on non-mainnet |
| `legacy-empty-data-zero-value.json` | 0 | 1 | `data:"0x"` → RLP `0x80`; `value:"0"` |
| `1559-empty-data-zero-value.json` | 2 | 1 | Same empty-data/zero-value for type 2 |
| `legacy-contract-creation.json` | 0 | 1 | `to` omitted → contract creation |
| `1559-contract-creation.json` | 2 | 1 | Contract creation for EIP-1559 |
| `legacy-padded-nonce.json` | 0 | 1 | Input nonce `"0x0009"` → canonical `9` |
| `reject-bad-checksum.json` | — | — | **Rejection**: EIP-55 checksum failure |
| `reject-chainid-zero.json` | — | — | **Rejection**: `chainId:"0"` |

---

## JSON Schema

Each **signing vector** file:
```json
{
  "name": "...",
  "tx": {
    "type":                    "0x0" | "0x2",
    "chainId":                 "<decimal string>",
    "nonce":                   "<decimal or 0x-hex string>",
    "to":                      "<EIP-55 address>",
    "value":                   "<decimal wei string>",
    "data":                    "0x<hex>",
    "gas":                     "<decimal string>",
    "gasPrice":                "<decimal string>",
    "maxFeePerGas":            "<decimal string>",
    "maxPriorityFeePerGas":    "<decimal string>"
  },
  "expected": {
    "raw_tx":  "0x<rlp hex>",
    "tx_hash": "0x<32-byte hex>",
    "r":       "0x<32-byte hex>",
    "s":       "0x<32-byte hex>",
    "v":       "0x<hex>"
  },
  "meta": {
    "oracles": { "ethers": "<version>", "cast": "<version or note>" },
    "regenerated_at": "<RFC3339 timestamp>"
  }
}
```

**`v` encoding** matches `go-ethereum`'s `RawSignatureValues()`:
- Type 0 (legacy + EIP-155): `v = chainId×2 + 35 + yParity`
  (e.g. chainId=1, yParity=1 → v=0x26=38)
- Type 2 (EIP-1559): `v = yParity` (0x0 or 0x1)

Each **rejection vector** file:
```json
{
  "name": "...",
  "tx": { ... },
  "expected_error": "<error code>",
  "meta": { "note": "...", "regenerated_at": "..." }
}
```

The `tx` field uses the same TxRequest field names as the wire contract.
Issue 2.10's `parity_test.go` feeds `tx` directly to `Signer.SignTransaction`.

---

## Oracles

| Oracle | Version | Notes |
|--------|---------|-------|
| **ethers v6** | 6.16.0 | All 9 signing vectors generated and verified here |
| **cast** (Foundry) | v1.7.1 (intended) | **Not run** — Foundry unavailable in this env |

See `cast-version.txt` for details. The committed vectors were produced solely
with ethers v6. Cast cross-check is deferred to a Foundry-equipped machine.

---

## Regeneration Procedure

### Requirements

- **Node.js** (any recent version; tested with v25)
- **ethers v6**: `npm install ethers@6` inside `scripts/`
- **Foundry** (optional but recommended for dual-oracle verification):
  install v1.7.1 per the repo's `.foundry-version`

### Steps

```sh
# From repo root:

# 1. Install ethers (if not already present in scripts/node_modules)
npm install --prefix scripts ethers@6

# 2. Run the regen script
scripts/regen-vectors.sh
#    → when cast is absent: runs ethers-only (warns, exits 0)
#    → when cast is present: runs both oracles, byte-compares, exits non-zero on mismatch

# 3. Verify output
git diff apps/eth-signer-mcp/internal/signing/testdata/vectors/
```

### On a Foundry-equipped machine (full dual-oracle)

```sh
# Ensure pinned Foundry version
cat .foundry-version       # v1.7.1
cast --version             # must match

npm install --prefix scripts ethers@6
scripts/regen-vectors.sh   # runs both oracles, byte-compares, writes cast-version.txt
```

### After a go-ethereum bump

Re-run `scripts/regen-vectors.sh`. If the raw_tx values change, the parity
suite (`internal/signing/parity_test.go`) will fail, guiding you to update
the vectors. The `meta.regenerated_at` and `meta.oracles` fields make drift
attributable to a specific oracle/tool version.

---

*Issue 2.9 — generated 2026-06-11*
