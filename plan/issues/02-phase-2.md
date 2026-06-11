# Phase 2: Signing core — vault, tx validate/build, signer, both tools, parity

## Phase Overview

- **Goal:** The complete offline signing path, end to end over stdio: `sign_transaction`
  and `get_address` registered with full inferred schemas; validation running entirely
  before key material is touched; keystore decrypt-sign-zero inside `WithSigningKey` with
  panic-safe deferred zeroing and a decrypt semaphore of 1; the six-code error taxonomy
  with the locked `{"code","message"}` wire encoding; one audit line per successful
  signing; and **byte-identical RLP parity** against `cast wallet sign-tx` and ethers v6
  on committed golden vectors including all edge cases.
- **Issue count:** 12 issues, **25 total points**
- **Estimated duration:** **~14 working days** (single-stream)
- **Packages touched:** `internal/signing` (everything except the Phase 1 secret files),
  `internal/server` (tool registration, handlers, `errors.go`), `cmd/eth-signer-mcp`
  (vault + signer wiring, `--chain-id` guard).
- **ADRs:** ADR-003, ADR-004, ADR-007 (now load-bearing), ADR-009, ADR-010, ADR-006
  partially (decrypt semaphore; HTTP-side bounds land in Phase 3).
- **Version pins:** go-ethereum **v1.17.3**, modelcontextprotocol/go-sdk **v1.6.1**
  (server and test client), `github.com/google/jsonschema-go` for schema inference.

**Entry criteria:**

- Phase 1 exit criteria all green: `bin/eth-signer-mcp` builds, parses all flags, logs
  JSON on stderr, answers MCP `initialize` + an empty `tools/list` over stdio; CI
  (lint incl. depguard, test, build, govulncheck, `GOOS=windows` compile) green on `main`.
- `internal/signing` secret hygiene shipped: `Secret[T]`, `ZeroBytes`/`ZeroBigInt`,
  `Sentinel` with encoded-forms registration, leak-scan tests.
- SDK spike note committed, answering: in-memory transport pattern, `mcp.AddTool`
  inference behavior with `jsonschema-go` tags, request-id source on `CallToolRequest`.
- Slim depguard block committed; `internal/signing/offline_test.go` scaffold compiles
  and runs (vacuously green — this phase makes it load-bearing).

**Exit criteria (the parity gate):**

- [ ] **Byte-identical RLP parity vs `cast wallet sign-tx` and ethers v6** on every
      golden vector, both tx types, chainId 1 and 11155111, including the edge cases:
      EIP-155 `v` vs yParity, empty `data` (`"0x"` → RLP `0x80`), zero `value`, contract
      creation (`to` omitted), padded/leading-zero nonce; plus rejection of a
      checksum-failing mixed-case address and of `chainId = 0` with the correct codes.
- [ ] Recovered sender == keystore address on every signed vector; every RLP round-trips
      through `core/types.Transaction.UnmarshalBinary` to the same hash.
- [ ] All six error codes observable over MCP as `IsError: true` + `{"code","message"}`
      compact JSON in `Content[0]`, asserted by JSON-parsing in e2e tests.
- [ ] Validation failures never touch the vault (panicking-fake-vault test green for
      every failure class).
- [ ] Zeroing tests green on success and panic paths; a signing-path panic leaves the
      server serving; leak scan (raw + encoded forms) green over all captured logs and
      outputs.
- [ ] Offline-import test load-bearing and green; depguard green; CI green on all of
      the above.
- [ ] One audit line per successful signing with `request_id`, `tx_hash`, `chain_id`,
      `nonce` — and nothing from the tx body.
- [ ] `get_address` returns the EIP-55 address without reading the password file
      (tested with an unreadable password file).

### Phase Assumptions (recorded inline)

- The keystore lifecycle contract is locked and stated identically everywhere: the
  keystore JSON + address are a **boot-time snapshot** (read eagerly at vault
  construction, fail fast — a missing or empty `address` field in the keystore JSON is
  a startup `keystore_error` with a clear message); the **password file is re-read on
  every signing call** (rotation works without restart); **rotating the keystore file
  requires a restart**; a mid-run decrypt failure returns `password_error`.
- The chain-id guard lives **only** in the `Signer` constructor, wired by `cmd` from
  `--chain-id`. No per-request guard field exists anywhere.
- Tool output includes `rawTransaction`, `signature{r,s,v}`, `hash`, and `from`
  **from day one** — no later retrofit, no `omitempty` staging.
- Golden vectors are committed under
  `apps/eth-signer-mcp/internal/signing/testdata/vectors/`; regeneration is a
  developer-only script (`scripts/regen-vectors.sh`) pinned via `.foundry-version`
  (v1.7.1 at time of writing; any single stable tag satisfies the design). **CI never
  invokes Foundry or Node.**
- Zeroing is best-effort per ADR-009: deferred `clear` + `runtime.KeepAlive` on password
  bytes and the key scalar, including panic paths; Go's runtime may retain transient
  copies (GC moves, stack copies) — tests assert the buffers we own are cleared and the
  limitation is documented, not over-claimed.
- The latency contract (ADR-010) is asserted in this phase as a benchmark criterion on
  issue 2.6: non-KDF overhead (total signing time minus measured KDF time) **< 10 ms on
  both** the standard-scrypt and light-scrypt fixtures. End-to-end latency is dominated
  by scrypt on every call by design (~0.5–1 s standard, ~50 ms light); no warm-path
  claims exist.

---

## Phase Summary

| Issue | Title                                                                | Points | ~Days | Blocked by |
|-------|----------------------------------------------------------------------|--------|-------|------------|
| 2.1   | Test keystore fixtures (standard + light scrypt)                     | 1      | 0.5   | —          |
| 2.2   | Keystore vault: snapshot, `WithSigningKey`, semaphore, zeroing       | 3      | 2.5   | 2.1        |
| 2.3   | Wire-contract structs: `TxRequest`/`SignResult`/`AddressResult`      | 2      | 1.0   | —          |
| 2.4   | `validate.go`: presence/type rules, EIP-55, chainId≠0, data cap, guard | 3    | 1.5   | 2.3        |
| 2.5   | `build.go`: `LegacyTx` / `DynamicFeeTx` construction                 | 2      | 1.0   | 2.4        |
| 2.6   | Signer orchestration + error taxonomy + audit line + panic recovery  | 3      | 2.0   | 2.2, 2.5   |
| 2.7   | Tool registration: `sign_transaction` + `get_address`; error wire encoding | 3 | 1.5   | 2.6        |
| 2.8   | Offline-import test load-bearing + depguard verification             | 1      | 0.5   | 2.6        |
| 2.9   | Golden parity vectors + regen tooling (`cast` + ethers v6)           | 3      | 1.5   | 2.1        |
| 2.10  | Byte-identical parity suite (all edge cases)                         | 2      | 1.0   | 2.6, 2.9   |
| 2.11  | Stdio end-to-end test (full binary surface)                          | 1      | 0.5   | 2.7        |
| 2.12  | Phase polish pass                                                    | 1      | 0.5   | 2.1–2.11   |

**Total: 25 points / 14 task days.**

## Phase Execution Plan

| Day | Work |
|-----|------|
| 1   | 2.1 fixtures; start 2.2 vault |
| 2   | 2.2 vault (snapshot, semaphore) |
| 3   | 2.2 vault finish (zeroing + panic tests) |
| 4   | 2.3 wire-contract structs + golden schema test |
| 5   | 2.4 validation rules |
| 6   | 2.4 finish; start 2.5 build |
| 7   | 2.5 finish; start 2.6 signer orchestration |
| 8   | 2.6 signer (error taxonomy, audit line, panic recovery) |
| 9   | 2.6 finish (sender check, benchmark); start 2.7 tools |
| 10  | 2.7 finish (both tools, wire-encoding contract tests, cmd wiring) |
| 11  | 2.8 offline gate + mutation check; start 2.9 vectors |
| 12  | 2.9 finish (regen script + full vector matrix) |
| 13  | 2.10 parity suite |
| 14  | 2.11 stdio e2e; 2.12 polish pass |

---

## Issues

### Issue 2.1: Test keystore fixtures (standard + light scrypt)

- **Points:** 1
- **Type:** chore
- **Priority:** P0
- **Blocked by:** none
- **Blocks:** 2.2, 2.9
- **Scope:** ~0.5 day

**Description:**
Commit dev-only Web3 Secret Storage keystores for **one** throwaway test key under
`apps/eth-signer-mcp/internal/signing/testdata/`, in three KDF strengths plus the
malformed-keystore startup fixtures, with password files and a README documenting them
as low-value test keys. The same key backs every downstream vault, signer, parity, and
e2e test, and its raw scalar doubles as the leak-scan sentinel source (encoded forms
registered per the Phase 1 `Sentinel` helper).

**Implementation Notes:**

- Files to create (all under `apps/eth-signer-mcp/internal/signing/testdata/`):
  - `keystore-standard.json` — **standard scrypt** (geth default, N=262144). Generated
    with geth tooling (`geth account import` of the test key into a scratch keystore
    dir). Feeds the 2.6 benchmark and lifecycle tests where real parameters matter.
  - `keystore-light.json` — **light scrypt** (N=4096), generated with geth tooling
    (`geth account import --lightkdf`). The default fixture for integration/e2e tests.
  - `keystore-weak.json` — **weakened scrypt (n=2, r=8, p=1)**, generated by a small
    Go helper using go-ethereum v1.17.3's `keystore.EncryptKey` (geth CLI cannot emit
    n=2). The default fixture for fast unit tests; documented as "test-only weakened
    KDF — never use this pattern in production".
  - `keystore-no-address.json` and `keystore-empty-address.json` — hand-edited copies
    of `keystore-weak.json` with the top-level `address` field removed / set to `""`.
    These are the locked-decision fixtures for the startup `keystore_error` case
    (consumed by 2.2).
  - `password.txt` — single-line password matching all three keystores, **with** a
    trailing `\n` so the strip-trailing-newline path is exercised.
  - `gen_fixtures.go` — `//go:build ignore` generator (key → three keystore JSONs) so
    the fixtures are reproducible; invocation documented in the README.
  - `README.md` — derived address, the raw private-key hex inside a clearly-marked
    "test-only / do not reuse" block (single disclosure path; 2.9's regen script links
    here), KDF parameters per file, generation commands, and the malformed-fixture
    purpose.
- All three valid keystores encrypt the **same** key, so golden vectors (2.9) verify
  against any of them and tests pick the cheapest KDF that still proves the point.
- Cross-check the fixtures once with a non-go-ethereum decryptor (`cast wallet
  decrypt-keystore`) before committing; note the check in the PR description.

**Acceptance Criteria:**

- [x] All three valid keystore files decrypt with `password.txt` (trailing newline
      stripped) via go-ethereum v1.17.3 `keystore.DecryptKey`, yielding the same key
      and the README-documented address.
- [x] `keystore-standard.json` has scrypt N=262144 and `keystore-light.json` N=4096
      (asserted by a fixture-sanity test reading the JSON `crypto.kdfparams`); both were
      produced by geth tooling (commands recorded in the README).
- [x] `keystore-weak.json` has scrypt n=2 and decrypts in well under 100 ms.
- [x] `keystore-no-address.json` / `keystore-empty-address.json` exist and differ from
      `keystore-weak.json` only in the `address` field (the 2.2 startup-failure
      fixtures).
- [x] `README.md` carries the "test-only key, weakened/dev KDF, never reuse" warning,
      the address, the raw key hex in the marked block, and regeneration instructions.
- [x] The fixture key's scalar is registered as a leak-scan sentinel (raw + lower/upper
      hex + base64 + decimal forms) via the Phase 1 `Sentinel` helper.
- [x] `make test` and `make lint` stay green (fixtures only; a fixture-sanity test is
      the single new test).

**Testing Notes:**

- One `fixtures_test.go` sanity test: decrypt all three, assert same address, assert KDF
  parameters per file. Keep the standard-scrypt decrypt in a single test (it costs
  ~0.5–1 s) and skip it under `-short`.

---

### Issue 2.2: Keystore vault: snapshot, `WithSigningKey`, semaphore, zeroing

- **Points:** 3
- **Type:** feature
- **Priority:** P0
- **Blocked by:** 2.1
- **Blocks:** 2.6
- **Scope:** ~2.5 days

**Description:**
Implement `signing.NewFileKeyVault(VaultOptions) (KeyVault, error)` and
`WithSigningKey(ctx, fn)` per ADR-003/009/010 and the locked lifecycle contract. The
keystore JSON and its address are read **eagerly at construction** (boot-time snapshot,
fail fast — including the explicit missing/empty-`address` startup case); the password
file is **re-read on every call**; decrypts are serialized by an internal **semaphore of
1** with the request `ctx` checked **before** scrypt starts; the callback receives a
sealed `SigningKey` (only operation: `SignTx`); and password bytes + the key scalar are
best-effort zeroed in a `defer` — **including on panic**.

**Implementation Notes:**

- Files to create (under `apps/eth-signer-mcp/internal/signing/`):
  - `vault.go` — `KeyVault` / `SigningKey` interfaces + `VaultOptions{KeystorePath,
    PasswordPath}` exactly per the architecture's public API.
  - `file_vault.go` — `fileKeyVault` struct: `keystoreJSON []byte` (ciphertext snapshot,
    safe to hold), `passwordPath string`, `address common.Address`, `sem chan struct{}`
    (cap 1). Constructor: read the file, `json.Unmarshal` enough to extract the
    top-level `address` field, validate non-empty hex, `common.HexToAddress`.
  - `decrypt.go` — per-call body: acquire semaphore (`select` on `ctx.Done()` vs `sem`),
    re-check `ctx.Err()` after acquiring and **before** any password read or KDF work,
    read + trailing-`\n`-strip the password, `keystore.DecryptKey(v.keystoreJSON,
    password)`, wrap the result in the unexported sealed `signingKey`, run `fn`, and in
    `defer`s: `ZeroBytes(password)`, `ZeroBigInt(key.PrivateKey.D)`,
    `runtime.KeepAlive(key)` — registered **before** `fn` runs so they fire on panic.
  - `file_vault_test.go`, `decrypt_test.go`.
- **Startup `keystore_error` case (locked decision, explicit):** a missing or empty
  `address` field in the keystore JSON fails `NewFileKeyVault` with a
  `*ToolError{Code: "keystore_error"}` whose message clearly names the problem (e.g.
  `keystore JSON has no usable "address" field; re-export the keystore`). Missing file,
  unreadable file, and malformed JSON are the same code with case-specific messages.
  `cmd` exits non-zero on any constructor error (fail fast). Until 2.6 lands
  `errors.go`, define the `ToolError` type + code constants here (2.6 keeps them in
  `errors.go`; moving the declaration then is fine).
- The constructor must **not** read the password file's contents and must **not** call
  `keystore.DecryptKey` — both are per-call work.
- Error mapping inside `WithSigningKey`: missing/unreadable password file and
  `keystore.ErrDecrypt` (wrong password) → `password_error`; ctx already cancelled →
  `ctx.Err()` unwrapped (system error, not a ToolError).
- Sealed key: unexported `signingKey` struct exposing only `Address() common.Address`
  and `SignTx(tx *types.Transaction, signer types.Signer) (*types.Transaction, error)`
  (delegating to `types.SignTx` with the held key). No raw-key accessor; the
  `*ecdsa.PrivateKey` never escapes `fn` (ADR-003).
- Keystore-file rotation mid-run is **not detected by design** — the snapshot is
  authoritative until restart (lifecycle contract; document on the type).

**Acceptance Criteria:**

- [x] `NewFileKeyVault` against each 2.1 fixture succeeds; `Address()` returns the
      documented address; no password read and no `DecryptKey` call at construction
      (proven by constructing with a wrong-password file and asserting success).
- [x] `NewFileKeyVault` against `keystore-no-address.json` **and**
      `keystore-empty-address.json` fails with `keystore_error` and a message naming
      the missing/empty `address` field — the locked startup case, both fixtures.
- [x] Missing / unreadable / malformed-JSON keystore → constructor `keystore_error`.
- [x] **Password re-read per call:** a test changes the password-file contents between
      two `WithSigningKey` calls; the second call succeeds with the **new** password
      (and a third call with a wrong password returns `password_error`).
- [x] After `WithSigningKey` returns, the key scalar's `D.Bits()` slice and the
      password byte slice are all zeros (captured-pointer technique: stash pointers
      inside `fn` via closure, inspect after return).
- [x] A panic inside `fn` propagates **after** the deferred zeroing runs (test recovers
      the panic and inspects the zeroed buffers).
- [x] Wrong password → `password_error`; password bytes still zeroed.
- [x] **Semaphore of 1:** two goroutines call `WithSigningKey` concurrently; the second
      observably enters only after the first's `fn` returns (instrumented ordering —
      e.g. channel handshakes inside `fn` — not wall-clock sleeps).
- [x] **ctx before KDF:** a pre-cancelled ctx returns `ctx.Err()` and the KDF never
      starts (assert via a sub-100 ms deadline against the standard-scrypt fixture,
      whose KDF alone takes ~0.5–1 s).
- [x] No compile-path exposes the raw private key: `SigningKey` has exactly two methods.
- [x] Leak scan over logs captured during all vault tests is green (raw + encoded
      sentinel forms).

**Testing Notes:**

- Unit tests default to `keystore-weak.json` (n=2) for speed; the ctx-before-KDF test
  and one full-decrypt test use `keystore-standard.json` and skip under `-short`.
- The captured-pointer zeroing assertion is the subtlest test of the phase — comment the
  technique in the test so future maintainers understand what it proves (and per
  ADR-009, what it does not: Go may retain transient copies).

---

### Issue 2.3: Wire-contract structs: `TxRequest`/`SignResult`/`AddressResult`

- **Points:** 2
- **Type:** feature
- **Priority:** P0
- **Blocked by:** none
- **Blocks:** 2.4, 2.7
- **Scope:** ~1 day

**Description:**
Define the typed structs that **are** the wire contract (no DTO/adapter layer, per the
architecture): `TxRequest`, `SignatureValues`, `SignResult` (with `hash` and `from`
**from day one**), and `AddressResult`, carrying `json` + `jsonschema` tags per the
architecture's public API. `signing` carries the tags without importing the MCP SDK
(struct tags are plain strings). A golden schema test pins the inferred JSON schema —
including `additionalProperties: false` — so accidental wire changes fail loudly.

**Implementation Notes:**

- Files to create (under `apps/eth-signer-mcp/internal/signing/`):
  - `request.go` — `TxRequest` exactly per the architecture: `type`, `chainId`, `nonce`,
    `to` (omitempty), `value`, `data`, `gas`, `gasPrice` (omitempty, legacy only),
    `maxFeePerGas` / `maxPriorityFeePerGas` (omitempty, 1559 only), `accessList`
    (omitempty, must be empty in v1). `jsonschema` tags add hex/decimal patterns and
    `maxLength` for `data` (512 KiB hex chars + `0x` prefix = 524,290 chars), using the
    tag vocabulary confirmed by the Phase 1 SDK spike note against
    `github.com/google/jsonschema-go`.
  - `result.go` — `SignatureValues{R,S,V}` (json `r`/`s`/`v`; `V` documented as yParity
    for type 2 and EIP-155 `v` for legacy), `SignResult{RawTransaction, Signature,
    Hash, From}` (json `rawTransaction`, `signature`, `hash`, `from`; `from` always
    EIP-55 checksummed), `AddressResult{Address}`.
  - `schema_test.go` + `testdata/schema/sign_transaction.golden.json` and
    `testdata/schema/get_address_result.golden.json` — generate the schema with
    `jsonschema.For[TxRequest](...)` (test-only dependency; pure schema lib, allowed by
    depguard/ADR-007) and byte-compare against the committed golden after normalization.
- Whatever the tag surface cannot express (per the spike note) is **not** forced into
  tags — it is enforced in `validate.go` (2.4) and noted in a comment on the struct.
- No `omitempty` on `hash`/`from` in `SignResult` — full output shape ships now (locked
  decision; no later retrofit).

**Acceptance Criteria:**

- [x] All four structs compile in `internal/signing` with `json` + `jsonschema` tags
      matching the architecture's public API; field-by-field doc comments state the
      accepted encodings (decimal or `0x`-hex) and per-type applicability.
- [x] The golden schema test pins the inferred `TxRequest` schema, asserting
      `additionalProperties: false`, the required-field set, the `data` `maxLength`,
      and the hex patterns; any tag change fails the test until the golden is
      consciously regenerated.
- [x] `SignResult` contains `rawTransaction`, `signature{r,s,v}`, `hash`, `from` with
      **no** `omitempty` on `hash`/`from`; a marshalling test proves all keys are
      always present.
- [x] `internal/signing` still imports no MCP SDK package and no internal package
      (depguard + offline scaffold stay green; `jsonschema-go` appears in test files
      only).
- [x] `make lint` and `make test` green.

**Testing Notes:**

- The golden schema test is the load-bearing one: it converts "someone renamed a JSON
  field" from a silent wire break into a red diff. Regeneration is a deliberate
  `-update`-flag path, documented in the test.
- End-to-end confirmation that `mcp.AddTool` publishes the same schema lands in 2.7/2.11
  (`tools/list` assertions).

---

### Issue 2.4: `validate.go`: presence/type rules, EIP-55, chainId≠0, data cap, guard

- **Points:** 3
- **Type:** feature
- **Priority:** P0
- **Blocked by:** 2.3
- **Blocks:** 2.5
- **Scope:** ~1.5 days

**Description:**
Implement validation that runs **entirely before key material is touched**: per-type
required-field presence, type-inappropriate-field rejection, decimal-or-hex numeric
parsing, the **EIP-55 mixed-case checksum rule**, **`chainId = 0` rejection**, the
**`data` ≤ 256 KiB** cap, non-empty `accessList` rejection, unsupported-type mapping,
and the chain-id guard check (`chain_id_mismatch`). Every failure returns a `*ToolError`
with the correct stable code; the vault is never reachable from any failure path.

**Implementation Notes:**

- Files to create (under `apps/eth-signer-mcp/internal/signing/`): `validate.go`,
  `validate_test.go`.
- Shape: `validate(req TxRequest, guard *uint64) (*parsedTx, *ToolError)` returning a
  normalized intermediate (`parsedTx`: tx type, `*big.Int` numerics, `[]byte` data,
  `*common.Address` to, chainID) consumed by 2.5's `build.go`. The guard **value** is a
  parameter — its only owner is the `Signer` constructor (2.6); no guard state lives
  here.
- Rule order and codes:
  1. `chainId` parse failure → `invalid_input`; **`chainId == 0` → `invalid_input`**
     (no replay-unprotected Homestead signatures; `LatestSignerForChainID(0)` would
     silently fall back to the Homestead signer — reject explicitly).
  2. Guard set and `chainId != *guard` → `chain_id_mismatch`.
  3. `type` ∉ {`0x0`, `legacy`, `0x2`, `eip1559`} → `unsupported_type` (types 1/3/4 are
     P2 by decision).
  4. Required fields per type (`gasPrice` for legacy; `maxFeePerGas` +
     `maxPriorityFeePerGas` for 1559; `nonce`, `gas`, `value`, `chainId` always) —
     missing → `invalid_input`.
  5. Type-inappropriate fields (`gasPrice` on type 2; 1559 fee fields on legacy) →
     `invalid_input`.
  6. Non-empty `accessList` → `invalid_input`.
  7. Numeric fields accept decimal (`"9"`) and `0x`-hex (`"0x9"`, `"0x0009"`) via
     `big.Int.SetString`; padded leading zeros normalize to the canonical value;
     `gas`/`nonce` must fit uint64.
  8. `data`: `0x`-prefixed even-length hex; decoded length > 262,144 bytes (256 KiB) →
     `invalid_input`; `"0x"` decodes to `[]byte{}` (non-nil, so RLP encodes `0x80`).
  9. **EIP-55 rule:** if `to` contains mixed-case hex, the checksum MUST validate
     (compare against `common.HexToAddress(to).Hex()`); failure → `invalid_input`.
     All-lowercase / all-uppercase accepted checksum-agnostic. Empty/absent `to` →
     contract creation (`nil` address). Malformed `to` (bad length/chars) →
     `invalid_input`.
- Error messages are static and never echo raw input field values (a caller-supplied
  secret must not be reflectable into logs or the wire).
- Note in `validate.go`'s doc comment which rules are double-covered by the inferred
  schema (`additionalProperties: false`, patterns, maxLength) and that validate is the
  authoritative layer — schema rejection is a bonus, not the contract.

**Acceptance Criteria:**

- [x] Table-driven tests cover every rule above with at least one accept and one reject
      case per rule, asserting the exact `ToolError` code.
- [x] **EIP-55 vectors:** a checksum-correct mixed-case `to` is accepted; a
      checksum-**failing** mixed-case `to` (single letter case-flipped) →
      `invalid_input`; the same address all-lowercase and all-uppercase are both
      accepted.
- [x] `chainId: "0"` and `chainId: "0x0"` → `invalid_input`.
- [x] `data` of exactly 256 KiB decoded bytes is accepted; 256 KiB + 1 byte →
      `invalid_input`.
- [x] Guard mismatch returns `chain_id_mismatch` even when the request is otherwise
      invalid in later rules (ordering locked by a dedicated test).
- [x] `nonce: "0x0009"` normalizes to the same `parsedTx` as `"9"` and `"0x9"`.
- [x] Contract creation (`to` omitted **and** `to: ""`) yields nil address for both tx
      types.
- [x] No error message contains any raw input value (asserted by scanning produced
      messages for the inputs of every reject case).
- [x] Fuzz tests on the numeric/hex/address parsers find no panics (the server never
      panics on malformed input).
- [x] `make lint` and `make test` green; `internal/signing` imports stay clean.

**Testing Notes:**

- Keep the rejection inputs aligned with 2.9's committed rejection vectors
  (checksum-failing address, chainId=0) so unit tests and the parity suite assert the
  same cases at two levels.

---

### Issue 2.5: `build.go`: `LegacyTx` / `DynamicFeeTx` construction

- **Points:** 2
- **Type:** feature
- **Priority:** P0
- **Blocked by:** 2.4
- **Blocks:** 2.6
- **Scope:** ~1 day

**Description:**
Turn a validated request into an unsigned go-ethereum transaction plus the matching
signer: `types.NewTx(&types.LegacyTx{...})` with the EIP-155 signer for type 0,
`types.NewTx(&types.DynamicFeeTx{...})` for type 2, both via
`types.LatestSignerForChainID(chainID)`. Handle the edge cases that drive the parity
matrix: contract creation (`To` nil), empty `data` (`[]byte{}` → RLP `0x80`), zero
`value`, and big-int fields parsed without precision loss.

**Implementation Notes:**

- Files to create (under `apps/eth-signer-mcp/internal/signing/`): `build.go`,
  `build_test.go`.
- Shape: `buildTx(p *parsedTx) (*types.Transaction, types.Signer)` — infallible given a
  validated `parsedTx`; all rejection happened in 2.4.
- `types.LatestSignerForChainID(chainID)` covers both types (EIP-155 semantics for
  legacy, London/1559 for type 2); chainID is guaranteed non-zero by 2.4.
- `LegacyTx{Nonce, GasPrice, Gas, To, Value, Data}`; `DynamicFeeTx{ChainID, Nonce,
  GasTipCap, GasFeeCap, Gas, To, Value, Data}` (note geth's field names: `GasTipCap` =
  `maxPriorityFeePerGas`, `GasFeeCap` = `maxFeePerGas`); `AccessList` left nil (empty
  by validation).
- Zero `value` via `new(big.Int)`; geth encodes it as `0x80` in RLP. Empty data stays
  `[]byte{}`, not nil, for the same reason.
- Values are `*big.Int` end to end — no `uint64` round-trip for `value`/fee fields, so
  > 2^64 wei values survive (precision-loss test).

**Acceptance Criteria:**

- [x] Legacy build: correct field mapping, `Tx.Type() == 0`, EIP-155-capable signer for
      the chainID.
- [x] 1559 build: correct field mapping incl. `GasTipCap`/`GasFeeCap`, `Tx.Type() == 2`.
- [x] Contract creation builds with `Tx.To() == nil` for both types.
- [x] Empty-data build RLP-encodes the data field as `0x80` (asserted on the unsigned
      tx's RLP or via a signed round-trip in test).
- [x] Zero-value build succeeds and survives `MarshalBinary`/`UnmarshalBinary`
      round-trip.
- [x] A `value` above 2^64 wei round-trips without precision loss.
- [x] Padded-nonce input (via 2.4) produces byte-identical unsigned tx to the canonical
      nonce.
- [x] `make lint` and `make test` green.

**Testing Notes:**

- This issue's tests assert structure and encoding of the **unsigned** tx; signed
  byte-parity against external oracles is 2.10's job. Keep one signed smoke test (any
  throwaway key via `crypto.GenerateKey`) proving `SignTx` + `UnmarshalBinary`
  round-trips to the same hash.

---

### Issue 2.6: Signer orchestration + error taxonomy + audit line + panic recovery

- **Points:** 3
- **Type:** feature
- **Priority:** P0
- **Blocked by:** 2.2, 2.5
- **Blocks:** 2.7, 2.8, 2.10
- **Scope:** ~2 days

**Description:**
Implement `signing.NewSigner(vault, SignerOptions{ChainIDGuard, Logger})` — the **only**
home of the chain-id guard — and `SignTransaction(ctx, req)`: validate → build →
`vault.WithSigningKey` → `SignTx` → `MarshalBinary` + `RawSignatureValues` → defensive
recovered-sender check → encode `SignResult`. Land the full `ToolError` taxonomy in
`errors.go`, the request-id context helpers, panic recovery that keeps the server
serving, the per-signing **audit log line**, and the **non-KDF-overhead benchmark**
(< 10 ms on both scrypt parameter sets).

**Implementation Notes:**

- Files to create (under `apps/eth-signer-mcp/internal/signing/`):
  - `signer.go` — `Signer`, `SignerOptions{ChainIDGuard *uint64, Logger *slog.Logger}`,
    `NewSigner`, `SignTransaction`, `Address()`.
  - `errors.go` — `ToolError{Code, Message, Cause}` + the six code constants
    (`invalid_input`, `unsupported_type`, `chain_id_mismatch`, `keystore_error`,
    `password_error`, `internal_error`); `Cause` is logs-only, **never** serialized
    (move the 2.2 interim declarations here).
  - `context.go` — `WithRequestID` / `RequestIDFromContext` (defined in `signing` so it
    imports nothing internal).
  - `signer_test.go`, `bench_test.go`.
- Orchestration inside `WithSigningKey`'s `fn`: `signedTx, err := key.SignTx(builtTx,
  gethSigner)`; `raw, _ := signedTx.MarshalBinary()`; `v, r, s :=
  signedTx.RawSignatureValues()`; recover the sender via `types.Sender(gethSigner,
  signedTx)`.
- **Sender-mismatch check (locked wording):** if recovered sender != `vault.Address()`,
  return `internal_error` with a message that **names both addresses** — e.g.
  `recovered sender 0x… does not match keystore address 0x…` (both are non-secret
  cached/derived addresses; safe on the wire and in logs).
- `SignResult` encoding: `rawTransaction` `0x`-hex of `MarshalBinary`; `r`/`s`/`v`
  `0x`-hex quantities; `V` is yParity (0/1) for type 2 and EIP-155 `v`
  (`chainID*2+35/36`) for legacy — whatever `RawSignatureValues` returns for the chosen
  signer, documented; `hash` = `signedTx.Hash().Hex()`; `from` EIP-55 checksummed via
  `common.Address.Hex()`.
- **Panic recovery:** `SignTransaction` wraps orchestration in `defer`/`recover`. The
  vault's inner `defer` (2.2) zeroes key material first; the recover then maps to
  `internal_error` with a **redacted** log line (never log the raw panic value) and
  returns a `*ToolError` — the process keeps serving.
- **Audit line (locked):** exactly one info-level line per **successful** signing:
  `request_id`, `tx_hash`, `chain_id`, `nonce`. The tx body — `calldata`, `to`,
  `value` — is **never** logged at any level.
- **Vault-never-touched guarantee:** a fake `KeyVault` whose `WithSigningKey` panics if
  invoked, run against every validation-failure class (each `invalid_input` variant,
  `unsupported_type`, `chain_id_mismatch`).
- **Benchmark (locked criterion, ADR-010):** `bench_test.go` measures, for both
  `keystore-standard.json` and `keystore-light.json`: (a) total `SignTransaction` time
  and (b) KDF-only time (timing `keystore.DecryptKey` directly on the same fixture),
  and asserts median **(a − b) < 10 ms**. Implement as a `Benchmark*` pair plus a
  guarded `Test*` assertion using medians over several iterations so it is robust to
  machine speed (the delta, not absolute KDF time, is asserted).

**Acceptance Criteria:**

- [x] Happy path against the weak fixture returns a `SignResult` with non-empty
      `rawTransaction`, populated `r`/`s`/`v`, `hash == signedTx.Hash()`, and `from`
      equal to the EIP-55 keystore address — for both legacy and 1559 requests, with
      the correct `v` shape each (`chainID*2+35/36` vs yParity 0/1).
- [x] The chain-id guard exists **only** as the `NewSigner` constructor parameter; grep
      proves no per-request guard field; guard mismatch → `chain_id_mismatch`.
- [x] Every validation-failure class returns the right code and **never** invokes the
      vault (panicking-fake-vault test per class).
- [x] Wrong-password fixture → `password_error`; the error's `Cause` never appears in
      any captured output (leak scan + wire assertion in 2.7).
- [x] Sender-mismatch (fake vault returning a key whose address differs from
      `Address()`) → `internal_error` whose message contains **both** the cached
      keystore address and the recovered address.
- [x] A panic injected inside the signing path is recovered: key material already
      zeroed (2.2's technique), result is `internal_error`, the logged line is
      redacted, and a **subsequent** `SignTransaction` on the same `Signer` succeeds
      (server-keeps-serving proof).
- [x] Exactly one audit line per success with exactly `request_id`, `tx_hash`,
      `chain_id`, `nonce`; captured logs contain no `to`, no `value`, no calldata
      bytes (asserted against a request with distinctive values), and pass the
      encoded-forms leak scan.
- [x] Benchmark/test asserts non-KDF overhead < 10 ms on **both** standard- and
      light-scrypt fixtures (standard run skipped under `-short`, executed in CI's
      full run and recorded for the Phase 4 acceptance benchmark).
- [x] `go test -race` green over the package.

**Testing Notes:**

- Use the weak fixture for orchestration tests; standard/light only in the benchmark
  and one lifecycle test.
- The redacted-panic test should panic with a sentinel-bearing value and assert the
  sentinel (raw + encoded forms) is absent from captured logs.

---

### Issue 2.7: Tool registration: `sign_transaction` + `get_address`; error wire encoding

- **Points:** 3
- **Type:** feature
- **Priority:** P0
- **Blocked by:** 2.6
- **Blocks:** 2.11
- **Scope:** ~1.5 days

**Description:**
Register **both** tools on the Phase 1 `*mcp.Server` via `mcp.AddTool` (schema inference
backed by the external `github.com/google/jsonschema-go` package):
`sign_transaction` (`signing.TxRequest` → `*signing.SignResult`) and `get_address`
(no input → `*signing.AddressResult`, served from the boot-time snapshot address — no
password read). Implement `server/errors.go` as the **single crossing point** from
`*signing.ToolError` to the locked wire encoding: `IsError: true` + `Content[0]` a
TextContent containing compact JSON `{"code","message"}`, nil Go error. Wire the real
vault + signer + `--chain-id` guard in `cmd`.

**Implementation Notes:**

- Files to create/modify:
  - `apps/eth-signer-mcp/internal/server/handlers.go` — handler closures for both
    tools. Each generates/propagates a `request_id` (SDK-provided id if the Phase 1
    spike found one on `CallToolRequest`, else UUIDv4) and attaches it via
    `signing.WithRequestID` before calling the signer.
  - `apps/eth-signer-mcp/internal/server/errors.go` — `toolResult(err error)
    (*mcp.CallToolResult, error)`: for `*signing.ToolError`, marshal
    `{"code":…,"message":…}` compact (no indentation, stable key order via a small
    struct), set it as the single TextContent and `IsError: true`, return nil Go error;
    for any other error return `(nil, err)` — protocol-level, reserved for
    system failures. `Cause` is logged (with `request_id`) and never serialized.
  - `apps/eth-signer-mcp/internal/server/server.go` (modify Phase 1 shell) —
    `server.New(signer *signing.Signer, opts Options)` registers both tools once;
    `RunStdio` unchanged.
  - `apps/eth-signer-mcp/internal/server/handlers_test.go`, `errors_test.go` — stub
    signer + the SDK's in-memory transport (pattern from the spike note).
  - `apps/eth-signer-mcp/cmd/eth-signer-mcp/main.go` (modify) — construct
    `signing.NewFileKeyVault` (fail fast on constructor error, exit non-zero with the
    `keystore_error` message), `signing.NewSigner(vault,
    SignerOptions{ChainIDGuard: cfg.ChainIDGuard, Logger: logger})` — the only place
    the guard enters the system — then `server.New(signer, …)`.
- `get_address` handler calls `signer.Address()` (snapshot) and returns the EIP-55
  string; it must work when the password file is unreadable.
- Tool descriptions state supported tx types (0 and 2) and that the result is **not**
  broadcast anywhere.

**Acceptance Criteria:**

- [x] `tools/list` over the in-memory transport shows **exactly two** tools,
      `sign_transaction` and `get_address`, with inferred schemas; the
      `sign_transaction` input schema deep-matches 2.3's golden (incl.
      `additionalProperties: false`).
- [x] Happy-path `sign_transaction` over MCP returns structured content with
      `rawTransaction`, `signature.r/s/v`, `hash`, `from` all populated — full output
      shape from day one.
- [x] **Wire-encoding contract tests JSON-parse `Content[0]`** for **all six** codes
      (stub signer returning each `*signing.ToolError` in turn): `IsError == true`,
      exactly one TextContent, its text parses as JSON with exactly the keys `code`
      and `message`, `code` matches, and the handler returned a nil Go error.
- [x] A stub signer returning a non-`ToolError` error surfaces as a protocol-level
      error (non-nil Go error path), not as an `IsError` result.
- [x] Unknown input field (`{"foo":"bar"}` alongside valid fields) is rejected via the
      strict schema (asserted end-to-end through the SDK).
- [x] `get_address` returns the checksummed fixture address **with the password file
      chmod'd unreadable** (no password read on this path).
- [x] `ToolError.Cause` set to a sentinel-bearing error never appears in `Content[0]`
      or anywhere on the wire (leak-scan over the captured transport bytes).
- [x] Handlers attach a non-empty `request_id`; the 2.6 audit line emitted during a
      handler-driven signing carries the same id (correlation test).
- [x] `cmd` wiring: missing/no-address keystore at startup exits non-zero with a clear
      `keystore_error` message (smoke test against `keystore-no-address.json`);
      `--chain-id` is plumbed only into `NewSigner`.
- [x] `make lint` (depguard: `server` imports only `signing` + `obs`) and `make test`
      green.

**Testing Notes:**

- Handler tests use a stub `*signing.Signer`-shaped seam (small local interface in
  `server` or a function field) so no keystore decrypt runs; the fixture-integrated
  path is 2.11's job.
- Keep `errors_test.go` table-driven over the six codes — it is the contract test the
  Phase 3 HTTP e2e will re-run over real HTTP.

---

### Issue 2.8: Offline-import test load-bearing + depguard verification

- **Points:** 1
- **Type:** chore
- **Priority:** P0
- **Blocked by:** 2.6
- **Blocks:** phase exit
- **Scope:** ~0.5 day

**Description:**
With go-ethereum's `accounts/keystore`, `core/types`, and `crypto` now imported by
`internal/signing`, the ADR-007 import-graph test is no longer vacuous. Verify it walks
the real dependency tree, prove it **load-bearing by deliberate violation** (mutation
captured in the PR description), and confirm depguard still pins the package edges — both
gates green in CI.

**Implementation Notes:**

- Files to modify: `apps/eth-signer-mcp/internal/signing/offline_test.go` (Phase 1
  scaffold) — ensure it loads `./internal/signing` with
  `golang.org/x/tools/go/packages` (`NeedImports|NeedDeps|NeedName`), recursively walks
  `Imports` with a visited set, and fails on any of: `net/http`, `net/rpc`,
  `github.com/ethereum/go-ethereum/ethclient`, `github.com/ethereum/go-ethereum/rpc`.
- Failure output must name the chain: importing package → forbidden path (so the
  violation is diagnosable from CI logs alone).
- Deny-list (not allow-list) so legitimate new deps don't churn the test; top-of-file
  comment cites ADR-007 and explains why each path is forbidden and why
  `internal/server`'s `net/http` (server-side) is out of scope of this walk.
- **Mutation (locked):** temporarily add a blank import of
  `github.com/ethereum/go-ethereum/ethclient` to a `signing` file; run the test
  (must fail naming the path) and `make lint` (depguard must also fail); revert.
  **Capture both failure outputs in the PR description** — this is the deliberate-
  violation evidence the Phase 4 final sweep re-checks.
- Confirm the depguard block needs no edits now that `signing`'s real import set
  exists (go-ethereum crypto/keystore/types are allowed; `ethclient`/`rpc` are not).

**Acceptance Criteria:**

- [x] `go test ./internal/signing/ -run TestOfflineImports` passes against the real
      Phase 2 dependency tree (go-ethereum keystore/types/crypto in scope).
- [x] The PR description records the mutation: deliberate `ethclient` import → the
      offline test failed naming package and path **and** depguard failed; revert
      committed.
- [x] Failing output format names the importer and the forbidden import path.
- [x] The test completes in under 5 seconds locally (`packages.Load` mode tuned).
- [x] CI runs both gates (the test via `make test`, depguard via `make lint`) and is
      green on the merge commit.

**Testing Notes:**

- The negative case cannot live in the committed tree (it would break the build by
  design) — the PR-description capture is the locked mechanism; Phase 4's final sweep
  repeats it.

---

### Issue 2.9: Golden parity vectors + regen tooling (`cast` + ethers v6)

- **Points:** 3
- **Type:** chore
- **Priority:** P0
- **Blocked by:** 2.1
- **Blocks:** 2.10
- **Scope:** ~1.5 days

**Description:**
Author the developer-only golden-vector regeneration tooling and commit the full vector
matrix under `apps/eth-signer-mcp/internal/signing/testdata/vectors/`. Vectors are
produced by **two independent oracles** — `cast wallet sign-tx` (Foundry, pinned via
`.foundry-version`; v1.7.1 as of June 2026 — any single stable tag satisfies the design)
and a one-off ethers v6 Node script — which must agree byte-for-byte before a vector is
committed. **CI never invokes Foundry or Node**; regeneration is a manual developer step
(e.g. before a go-ethereum bump).

**Implementation Notes:**

- Files to create:
  - `scripts/regen-vectors.sh` — (1) asserts `cast --version` matches
    `.foundry-version`, aborting on mismatch; (2) for each entry in its vector-spec
    table, runs `cast wallet sign-tx` with the fixture private key (sourced from the
    2.1 README's single disclosure block — link, don't duplicate) fully offline;
    (3) runs `scripts/regen-vectors-ethers.mjs` for the same spec; (4) **byte-compares
    the two oracles' raw signed tx** and exits non-zero on any mismatch; (5) writes the
    canonical `vectors/<name>.json` and refreshes `vectors/cast-version.txt`.
  - `scripts/regen-vectors-ethers.mjs` — ethers v6 one-shot: build the tx (set
    `type: 0` explicitly for legacy — ethers v6 infers 1559 otherwise), sign with the
    fixture key, print `Transaction.serialized`.
  - `.foundry-version` (repo root) — pinned stable tag (`v1.7.1`).
  - `apps/eth-signer-mcp/internal/signing/testdata/vectors/cast-version.txt` — captured
    `cast --version` output committed beside the fixtures.
  - Signing vectors (all keyed to the 2.1 fixture key, so the real vault reproduces
    them):
    1. `legacy-mainnet.json` — type 0, chainId 1, simple transfer (EIP-155 `v` =
       `chainID*2+35/36` asserted here).
    2. `legacy-sepolia.json` — type 0, chainId 11155111.
    3. `1559-mainnet.json` — type 2, chainId 1 (`v` = yParity 0/1 asserted here).
    4. `1559-sepolia.json` — type 2, chainId 11155111.
    5. `legacy-empty-data-zero-value.json` — type 0, `data: "0x"`, `value: "0"`
       (empty data must encode `0x80`).
    6. `1559-empty-data-zero-value.json` — type 2, same edge.
    7. `legacy-contract-creation.json` — type 0, `to` omitted, non-empty `data`.
    8. `1559-contract-creation.json` — type 2, `to` omitted, non-empty `data`.
    9. `legacy-padded-nonce.json` — type 0, input `nonce: "0x0009"`; expected bytes
       are the canonical encoding.
  - Rejection vectors (no oracle output; expected code instead):
    10. `reject-bad-checksum.json` — mixed-case `to` with one case-flipped character;
        `expected_error: "invalid_input"`.
    11. `reject-chainid-zero.json` — `chainId: "0"`; `expected_error: "invalid_input"`.
  - `vectors/README.md` — fixture-matrix table (name, type, chainId, edge case),
    regen procedure, oracle versions, and the test-only-key pointer to the 2.1 README.
- Vector JSON schema: `name`, `tx` (the exact `TxRequest` JSON sent on the wire),
  `expected.raw_tx`, `expected.tx_hash`, `expected.r/s/v`, plus `expected_error` for
  rejection vectors; a `meta` block with tool versions + regen timestamp for drift
  audits.

**Acceptance Criteria:**

- [x] `scripts/regen-vectors.sh` regenerates the **entire** matrix in one run on a
      machine with pinned Foundry + Node and exits non-zero on any cast/ethers
      byte mismatch or `cast --version` / `.foundry-version` mismatch.
- [x] All 11 vector files exist, conform to the documented schema, and the 9 signing
      vectors carry byte-identical output from both oracles (recorded in `meta`).
- [x] `.foundry-version` (v1.7.1) and `vectors/cast-version.txt` are committed and
      mutually consistent.
- [x] Signing vectors use the 2.1 fixture key (one disclosure path: the 2.1 README);
      the script contains a pointer, not a second copy of the key's provenance story.
- [x] The script performs no network calls (both oracles run fully offline).
- [x] CI does **not** execute the script; `make test` has no dependency on `cast` or
      `node` (verified by CI passing on a runner without them).

**Testing Notes:**

- The script's correctness is proven by the dual-oracle byte-compare across the whole
  matrix; the Go-side consumption lands in 2.10.
- Future go-ethereum bumps re-run this script; the `meta` block makes drift
  attributable to a specific oracle/tool version.

---

### Issue 2.10: Byte-identical parity suite (all edge cases)

- **Points:** 2
- **Type:** feature
- **Priority:** P0
- **Blocked by:** 2.6, 2.9
- **Blocks:** 2.11 (fixture confidence), phase exit
- **Scope:** ~1 day

**Description:**
Implement `internal/signing/parity_test.go`: for every signing vector, drive the **real**
path — `Signer.SignTransaction` through `FileKeyVault` (weak-scrypt fixture; same key) —
and assert the output `rawTransaction` is **byte-identical** to the committed reference,
`r`/`s`/`v` match, the recovered sender equals the keystore address, and the RLP decodes
via `UnmarshalBinary` and round-trips to the same hash. Rejection vectors assert the
exact `ToolError` code **and** that the vault was never invoked. This is the PRD's #1
success metric in code and the phase exit gate.

**Implementation Notes:**

- Files to create (under `apps/eth-signer-mcp/internal/signing/`):
  - `parity_test.go` — loads every `vectors/*.json`, `t.Run(vector.Name, …)` subtests.
  - `vectors_test.go` (or a helper in `parity_test.go`) — shared vector loader +
    schema struct.
- Signing-vector flow: real `NewFileKeyVault` on `keystore-weak.json` (n=2 decrypt keeps
  the full matrix fast while still exercising decrypt → sign → zero on every vector) →
  `NewSigner` (no guard) → `SignTransaction(ctx, vector.Tx)` → assert:
  1. `result.RawTransaction == expected.raw_tx` (byte-identical hex).
  2. `result.Signature.{R,S,V}` match `expected.r/s/v` — locking EIP-155 `v` for
     legacy and yParity for type 2 against the external oracles.
  3. `result.Hash == expected.tx_hash`; `result.From` == the checksummed fixture
     address.
  4. `types.Transaction.UnmarshalBinary(raw)` succeeds and `rt.Hash().Hex() ==
     result.Hash` (round-trip).
  5. Independent sender recovery in the test: `types.Sender(
     types.LatestSignerForChainID(chainID), rt)` == keystore address.
- Rejection-vector flow: `NewSigner` over the **panicking fake vault** from 2.6 →
  `SignTransaction` → assert `*ToolError` with `vector.ExpectedError` and that no panic
  occurred (vault untouched).
- Subtest count assertion: exactly 9 signing + 2 rejection vectors consumed, so a
  dropped file fails loudly.
- Top-of-file comment: this suite is the canary for go-ethereum RLP drift; re-run
  `scripts/regen-vectors.sh` on any geth bump.

**Acceptance Criteria:**

- [x] All 9 signing vectors pass **byte-identical** `rawTransaction` equality against
      the cast/ethers reference, covering: both types × chainId {1, 11155111}, EIP-155
      `v` vs yParity, empty `data` (`0x` → `0x80`), zero `value`, contract creation
      (`to` omitted), padded/leading-zero nonce.
- [x] `r`/`s`/`v` match the reference on every signing vector.
- [x] Recovered sender == keystore address on every signing vector (in-test recovery,
      independent of the signer's own defensive check).
- [x] Every output RLP round-trips through `UnmarshalBinary` to the same hash.
- [x] `reject-bad-checksum.json` → `invalid_input`; `reject-chainid-zero.json` →
      `invalid_input`; the fake vault proves no key material was touched for either.
- [x] The suite asserts it consumed exactly 11 vectors (9 + 2).
- [x] Full suite completes in under ~5 seconds (weak-scrypt fixture) and runs in plain
      `make test` / CI — no external tools.
- [x] `make lint` and `make test` green.

**Testing Notes:**

- Using the real vault (not an in-memory double) makes every parity subtest also a
  decrypt-sign-zero integration test; the n=2 fixture keeps that affordable.
- If a vector ever fails after a geth bump, the committed `meta` blocks (2.9) identify
  whether the oracle or go-ethereum moved.

---

### Issue 2.11: Stdio end-to-end test (full binary surface)

- **Points:** 1
- **Type:** feature
- **Priority:** P0
- **Blocked by:** 2.7
- **Blocks:** phase exit
- **Scope:** ~0.5 day

**Description:**
Prove the whole Phase 2 stack over the real transport: an SDK v1.6.1 test client drives
the **actual binary** (subprocess over stdio, launched with the light-scrypt fixture)
through `initialize` → `tools/list` → `get_address` → `sign_transaction` happy path →
one error path per code, asserting errors by **JSON-parsing `Content[0]`**, and scanning
all captured stderr for the audit line and sentinel leaks.

**Implementation Notes:**

- Files to create: `apps/eth-signer-mcp/cmd/eth-signer-mcp/e2e_stdio_test.go`.
- Build the binary once per test run (`go build` into `t.TempDir()`, cached via
  `sync.Once`); launch via the SDK test client's command/stdio transport (pattern per
  the Phase 1 spike note), flags: `--keystore …/keystore-light.json --password-file
  …/password.txt`; capture stderr to a buffer for log assertions.
- Sequence and assertions:
  1. `initialize` succeeds; server name/version present.
  2. `tools/list` → exactly `sign_transaction` + `get_address`, strict schemas
     (`additionalProperties: false` visible in the published input schema).
  3. `get_address` → the checksummed fixture address.
  4. `sign_transaction` with `legacy-mainnet.json`'s `tx` → result matches that
     vector's `expected.raw_tx`/`hash`/`from` (binary-level parity anchor).
  5. One call per error code: missing field → `invalid_input`; `type: "0x3"` →
     `unsupported_type`; relaunch (or second subprocess) with `--chain-id 5` and a
     chainId-1 request → `chain_id_mismatch`; subprocess with a wrong-password file →
     `password_error`; subprocess pointed at `keystore-no-address.json` → **startup
     refusal** with the `keystore_error` message on stderr + non-zero exit (the
     keystore code is a boot-time failure by the lifecycle contract); `internal_error`
     is covered at the contract-test level in 2.7 (not force-able through the real
     binary without a fault hook — note this in the test).
  6. Every wire error asserted by JSON-parsing `Content[0]` for `{code, message}`.
  7. Captured stderr contains exactly one audit line for the successful signing with
     `request_id`/`tx_hash`/`chain_id`/`nonce`, contains no `to`/`value`/calldata from
     the request, and passes the raw + encoded-forms sentinel scan.
- Clean shutdown: close the client → stdin EOF → subprocess exits 0 (asserted).
  Use `t.Cleanup` for process + temp-file teardown; skip under `-short`; dump captured
  stdio/stderr via `t.Log` on failure.

**Acceptance Criteria:**

- [x] Full MCP session against the real binary completes: `initialize`, `tools/list`
      (both tools, strict schemas), `get_address`, happy-path `sign_transaction`.
- [x] The happy-path result is byte-identical to the committed `legacy-mainnet.json`
      reference — binary-level parity.
- [x] Error paths observed over the wire for `invalid_input`, `unsupported_type`,
      `chain_id_mismatch`, `password_error` — each via JSON-parsed `Content[0]`; the
      no-address keystore is refused at startup with a clear `keystore_error` message
      and non-zero exit.
- [x] Audit-line and leak-scan assertions over captured stderr pass.
- [x] stdin EOF → exit 0; no orphan processes or temp files after the run.
- [x] Runs in `make test` (skipped under `-short`), total under ~15 s on a developer
      laptop (light-scrypt decrypts ~50 ms each).

**Testing Notes:**

- Using the pinned SDK test client (same v1.6.1) doubles as a compatibility check of
  the published tool surface; the Phase 3 HTTP e2e will mirror this test's structure
  over Streamable HTTP.

---

### Issue 2.12: Phase polish pass

- **Points:** 1
- **Type:** chore
- **Priority:** P0
- **Blocked by:** 2.1–2.11
- **Blocks:** phase exit
- **Scope:** ~0.5 day

**Description:**
Close the phase with the planning-principle polish task: refactor/simplify
`internal/signing` and `internal/server` now that all the pieces exist, run a full lint
sweep, and touch up docs (package docs, fixture/vector READMEs, repo CLAUDE.md command
notes if anything changed). No behavior changes; the full test suite is the safety net.

**Implementation Notes:**

- Refactor targets to evaluate (apply where they simplify, skip where they don't —
  record the decision in the PR):
  - Consolidate any validation/parse helpers duplicated between `validate.go` and
    `build.go`; ensure `parsedTx` is the single normalized handoff.
  - Ensure `errors.go` (signing) and `errors.go` (server) are the only two places that
    construct/encode `ToolError`s; inline any stragglers from 2.2's interim
    declarations.
  - Deduplicate test helpers (fixture paths, vector loader, captured-log scanner,
    panicking fake vault) into shared `_test.go` helpers within each package.
  - Re-read every public doc comment in `internal/signing` against the architecture's
    public API section; fix drift in whichever direction is correct.
- Docs: package doc for `internal/signing` states the lifecycle contract (boot-time
  snapshot, per-call password re-read, restart for keystore rotation) and the ADR-009
  best-effort-zeroing caveat verbatim-consistent with the architecture; `testdata/`
  READMEs reflect the final fixture/vector inventory.
- Lint sweep: `make lint` with zero issues and zero new `//nolint` (any pre-existing
  ones justified inline); `make fmt`, `make vet`, `make tidy` clean.

**Acceptance Criteria:**

- [ ] `make lint` reports zero issues across the module; no unexplained `//nolint`
      directives; `gofmt -s`/`go vet`/`go mod tidy` produce no diff.
- [ ] No TODO/FIXME/dead-code stubs remain in `internal/signing` or `internal/server`
      from this phase (grep-verified; intentional P2 markers carry an issue reference).
- [ ] `ToolError` construction/encoding confirmed confined to the two `errors.go`
      files (grep-verified).
- [ ] Package docs and `testdata/` READMEs updated and consistent with the architecture
      (lifecycle contract + zeroing caveat present in the `signing` package doc).
- [ ] Full suite green after refactor: `make test` (incl. parity suite, stdio e2e,
      offline gate, leak scans, `-race` where configured) and CI green on the merge
      commit.
- [ ] No exported-API drift beyond the architecture's public API section (reviewed
      diff of `go doc ./internal/signing ./internal/server` before/after).

**Testing Notes:**

- Pure refactor discipline: every commit in this issue keeps `make test` green; any
  test that must change to accommodate a refactor is a smell — stop and reconsider the
  refactor instead.

---

## Phase Risk Notes

- **Parity edge cases** (yParity vs EIP-155 `v`, empty data `0x80`, contract creation,
  padded/leading-zero nonce) are the phase's top risk — which is why 2.9 (vectors) and
  2.10 (suite) are separately budgeted, per-edge-case, dual-oracle, and byte-identical.
- **Zeroing tests are subtle:** Go may copy buffers (GC moves, stack copies). Tests
  assert the buffers we own are cleared; ADR-009's limitation is documented in the
  package doc (2.12), not over-claimed.
- **Scrypt slowness in the test loop:** the weakened n=2 fixture keeps unit tests and
  the parity matrix fast; standard/light fixtures appear only where the parameters are
  the point (benchmark, ctx-before-KDF, lifecycle, e2e).
- **Offline gate regression:** ADR-007 becomes load-bearing in 2.8; the
  deliberate-violation mutation result is captured in that PR's description and
  re-checked in the Phase 4 final sweep.
- **Foundry output drift:** `.foundry-version` pin + committed `cast-version.txt` +
  dual-oracle byte-compare; CI never invokes Foundry/Node, so an upstream release
  cannot break the build.
