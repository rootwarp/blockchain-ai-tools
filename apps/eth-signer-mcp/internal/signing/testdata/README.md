# testdata — eth-signer-mcp Signing Fixtures

> **WARNING: TEST-ONLY KEY MATERIAL. DO NOT REUSE. DO NOT SEND REAL FUNDS.**
>
> The private key in this directory is committed in plaintext.
> It has no security value. Every form of this key is known to anyone who reads this file.

---

## Contents

| File | Purpose | KDF |
|------|---------|-----|
| `keystore-standard.json` | Web3 Secret Storage v3, standard parameters | scrypt N=262144, r=8, p=1 |
| `keystore-light.json` | Web3 Secret Storage v3, light parameters | scrypt N=4096, r=8, p=1 |
| `keystore-weak.json` | Web3 Secret Storage v3, **test-only weakened KDF** | scrypt N=2, r=8, p=1 |
| `keystore-no-address.json` | Optional address (spec): top-level `"address"` field removed | (copy of weak, for optional-address + discovery tests) |
| `keystore-empty-address.json` | Optional address (spec): top-level `"address"` set to `""` | (copy of weak, for optional-address + discovery tests) |
| `password.txt` | Passphrase for all three keystores, **with a trailing `\n`** | — |
| `gen_fixtures.go` | `//go:build ignore` generator (Go program) | — |

All three valid keystores (`standard`, `light`, `weak`) encrypt the **same** private key.

---

## Test Key (single disclosure path — TEST-ONLY)

```
⚠️  DO NOT REUSE — TEST-ONLY — COMMITTED IN PLAINTEXT ⚠️

Address (EIP-55):  0x9858EfFD232B4033E47d90003D41EC34EcaEda94
Private key (hex): 1ab42cc412b618bdea3a599e3c9bae199ebf030895b039e9db1e30dafb12b727
Password:          test-only-password-do-not-reuse
```

The raw private key scalar appears in the following files (all clearly marked TEST-ONLY;
do not add new copies):

| File | Purpose |
|------|---------|
| This `README.md` | Primary human-readable disclosure |
| `gen_fixtures.go` | Generates the three keystore files from this key |
| `scripts/regen-vectors.sh` | Passes the key to `cast wallet sign-tx` for dual-oracle comparison |
| `scripts/regen-vectors-ethers.mjs` | Passes the key to ethers v6 for vector generation |
| `internal/signing/fixture_sentinel.go` | Derives leak-scan sentinel (hex constant, not raw scalar) |

Downstream tests use `signing.FixtureKeySentinel()` to detect leaks in captured output.

---

## KDF Parameters per File

| File | N | r | p | Notes |
|------|---|---|---|-------|
| `keystore-standard.json` | 262,144 | 8 | 1 | geth default; ~0.5–1 s decrypt |
| `keystore-light.json` | 4,096 | 8 | 1 | geth `--lightkdf`; ~50 ms decrypt |
| `keystore-weak.json` | **2** | 8 | 1 | **TEST-ONLY WEAKENED KDF — NEVER USE IN PRODUCTION**; ~1 ms decrypt |

The weakened fixture (N=2) exists so that fast unit tests can exercise the full
decrypt → sign → zero path without waiting for KDF. Use `keystore-light.json` for
integration tests and `keystore-standard.json` only where the actual KDF parameters
are the test subject (benchmarks, ctx-cancellation-before-KDF tests).

---

## Optional-Address Fixtures

`keystore-no-address.json` and `keystore-empty-address.json` are hand-edited copies
of `keystore-weak.json`. Per the Web3 Secret Storage spec the top-level `"address"`
is optional (and omitting it is privacy-friendly). These fixtures exercise the
new (post-addr-opt) behaviour:

- Construction succeeds; initial Address() is the zero address.
- On first successful WithSigningKey (using the password), the address is discovered
  from the decrypted key and cached; subsequent Address()/get_address return the
  real value.

- **`keystore-no-address.json`** — the top-level `"address"` field is **absent**.
- **`keystore-empty-address.json`** — the top-level `"address"` field is present
  but set to `""`.

Both are otherwise identical to `keystore-weak.json` (same ciphertext, same salt);
they decrypt successfully via DecryptKey (the old constructor error path is gone).

---

## Generating / Regenerating the Fixtures

The keystores are generated programmatically using go-ethereum v1.17.3's
`keystore.EncryptKey`. **The `geth` CLI is not used** because `geth account import`
cannot produce a keystore with N=2; the Go path covers all three KDF strengths.

```sh
cd apps/eth-signer-mcp/internal/signing/testdata
go run gen_fixtures.go
```

The generator:
1. Derives the Ethereum address from the fixed private key (`testPrivKeyHex` in
   `gen_fixtures.go`).
2. Encrypts the key under three scrypt parameter sets.
3. Writes `keystore-standard.json`, `keystore-light.json`, `keystore-weak.json`, and
   `password.txt`.
4. Round-trip verifies each file before exiting.

After running the generator, (re)create the optional-address fixtures by hand:

```sh
cp keystore-weak.json keystore-no-address.json
# Remove the "address" field from keystore-no-address.json

cp keystore-weak.json keystore-empty-address.json
# Set "address": "" in keystore-empty-address.json
```

### Equivalent geth commands (for audit / documentation only)

These would produce standard and light variants if `geth` were available. They
**cannot** reproduce the exact ciphertext (random IV/salt) but would produce valid
keystores decrypting to the same address:

```sh
# Import the raw private key file into a scratch keystore directory:
echo "1ab42cc412b618bdea3a599e3c9bae199ebf030895b039e9db1e30dafb12b727" > /tmp/testkey.hex
geth account import --keystore /tmp/geth-ks-standard /tmp/testkey.hex
# → produces standard scrypt (N=262144)

geth account import --keystore /tmp/geth-ks-light --lightkdf /tmp/testkey.hex
# → produces light scrypt (N=4096)
```

The weakened N=2 variant cannot be produced by `geth` CLI — it requires the Go API.

---

## Leak-Scan Sentinel

`signing.FixtureKeySentinel()` returns a `Sentinel` pre-loaded with all encoded
forms of the private key scalar (raw, hex-lower, hex-upper, base64-std, base64-raw,
base64-url, base64-rawurl, decimal, checksummed address). Downstream tests use it to
assert that captured log/output contains no key material in any common encoding.

---

*Issue 2.1 — generated 2026-06-10*
