# Research Overview: `eth-signer-mcp`

Lead-researcher consolidation of the four investigation angles backing the PRD
([`../prd.md`](../prd.md)). This doc **leads with the concrete recommendations**,
then reconciles conflicts, records what was **dropped or caveated** after
adversarial verification, and surfaces the **consolidated assumptions**. Each
section links to its per-angle source doc.

> **Status note (read first).** The per-angle research docs referenced below
> (`01`..`04`) are the intended homes for the full evidence and citations. At the
> time this overview was consolidated, only the PRD and the verification results
> were on disk; the per-angle docs had not yet been committed. The findings,
> corrections, and confidence levels captured here come from the verification
> pass run against primary sources. If a per-angle file is missing when you
> follow a link, treat this overview as the authoritative consolidated record
> until that doc lands.

Per-angle docs:

- [`01-mcp-go-sdk.md`](./01-mcp-go-sdk.md) — official MCP Go SDK: API surface,
  transports, versioning, OAuth.
- [`02-eth-signing-geth.md`](./02-eth-signing-geth.md) — go-ethereum signing
  primitives: keystore, `crypto`, `core/types`, EIP semantics.
- [`03-secure-secret-handling.md`](./03-secure-secret-handling.md) — zeroing
  secrets, redaction, constant-time compare, Go memory-erasure caveats.
- [`04-signing-parity-and-test-vectors.md`](./04-signing-parity-and-test-vectors.md)
  — byte-for-byte parity with `cast` / ethers v6, golden test vectors.

---

## 1. Recommendations (lead)

### Stack — adopt as written in the PRD, with version pins

1. **MCP framework:** `github.com/modelcontextprotocol/go-sdk`, **pin to
   `v1.6.x`** (latest verified `v1.6.1`, 2026-05-22). It is the *official* MCP
   Go SDK, maintained **in collaboration with Google** under the
   community-led `modelcontextprotocol` org (see the dropped claim in §3 — it is
   **not** an Anthropic-co-maintained project). It carries a v1.0.0
   backward-compatibility commitment (2025-09-30), so a `v1.6.x` pin is safe.
   `go.mod` declares `go 1.25.0`; that is compatible with the repo's Go 1.26
   toolchain.

2. **Signing primitive:** `github.com/ethereum/go-ethereum`, **pin to
   `v1.17.3`**. Use `accounts/keystore` (Web3 Secret Storage decrypt),
   `crypto` (`Sign`, key handling), and `core/types` (typed transactions,
   signers, RLP). All API shapes verified against primary source.

3. **CLI:** `urfave/cli` (PRD-chosen; not separately researched — no objection).

### Architecture — confirmed by the research

- **Two transports, one tool surface.** stdio (newline-delimited JSON over
  stdin/stdout) is the default; HTTP via `NewStreamableHTTPHandler` when
  `--http` is set. Both expose identical tools/schemas — matches PRD P0-MCP-2.
- **Tool registration:** use the SDK's typed `AddTool` with a Go struct input
  type; the SDK derives the JSON schema via `jsonschema.For`. The handler
  signature is `func(ctx, req, args) (*CallToolResult, any, error)`.
- **Error semantics (important, drives PRD's error contract):** distinguish
  **tool-level** errors from **protocol-level** errors.
  - *Tool-level* (bad tx JSON, chainId mismatch, decrypt failure): set
    `IsError=true` on the `CallToolResult` (via the `SetError`-style helper) and
    return a **nil** Go `error`. These map to the PRD's stable error codes
    (`invalid_input`, `chain_id_mismatch`, `password_error`, …).
  - *Protocol/system* errors: return a non-nil Go `error`.
  - This is the mechanism that satisfies P0-MCP-4 (structured, non-sensitive
    tool errors).
- **HTTP hardening already in the SDK:** `StreamableHTTPOptions` provides
  DNS-rebinding protection; bind to `127.0.0.1` and layer the bearer-token
  check (P0-SEC-5) in front of the handler. Prefer the SDK's current
  protection mechanism over the deprecated `CrossOriginProtection` path.
- **Transports are single-use** — construct a fresh transport per connection;
  do not reuse.

### Signing — confirmed recipe

- **Decrypt:** `keystore.DecryptKey(jsonBytes, password)` returns a `*Key`
  whose `PrivateKey` is a `*ecdsa.PrivateKey`. The canonical go-ethereum
  zeroing pattern is `defer zeroKey(key.PrivateKey)` — but `zeroKey` is
  **unexported**, so re-implement it (see §3).
- **Sign:** build the tx with `types.NewTx(<inner txdata>)`, choose the signer
  with `types.LatestSignerForChainID(chainID)`, and `types.SignTx`. For legacy,
  EIP-155 `v = chainId*2 + 35/36`; for EIP-1559, `v` is the `yParity` (0/1) —
  matches P0-SIGN-1/2/4.
- **Output:** `tx.MarshalBinary()` → `0x`-prefixed RLP (broadcast-ready);
  decode-round-trips via `UnmarshalBinary` for the acceptance test. Extract
  `{r,s,v}` from `tx.RawSignatureValues()`.

### Secret handling — the hardening bar

- **Zero password bytes** immediately after `DecryptKey` returns, using the
  `clear` builtin on the password byte slice.
- **Zero the private key** after each signature. Re-implement geth's `zeroKey`:
  `clear(k.D.Bits())` is functionally equivalent to the (unexported)
  go-ethereum implementation and zeroes the `big.Int` limbs.
- **Read the password file** without echo concerns (it's a file, not a TTY);
  read, use, zero within one function scope (PRD hardening rule).
- **Redaction:** never log password / key / keystore JSON. A `Secret` wrapper
  type implementing `String()`, `GoString()`, `Format()`, `MarshalJSON()`, and
  `slog.LogValuer` is the recommended pattern — **but note** `slog` reflects
  through nested structs and can bypass `LogValue` on nested fields, so keep
  secrets shallow / never embed them in logged structs. Back this with the
  PRD's log-scanning test (P0-SEC-3).
- **Constant-time compare** the HTTP bearer token with
  `crypto/subtle.ConstantTimeCompare` (note: it leaks length, so compare
  fixed-length hashes/tokens, not raw variable-length strings).

### Test strategy — parity is the acceptance bar

- **Golden vectors, byte-identical output.** Generate reference signatures with
  **`cast mktx`** (the canonical offline tx builder — prints signed raw tx hex
  to stdout) and **ethers v6**, then assert byte-for-byte equality of
  `rawTransaction`. **Pin the Foundry version** in any golden test that depends
  on `cast` stdout formatting (CLI output has drifted across versions).
- Cover **legacy + EIP-1559**, and **one mainnet chainId + one non-mainnet**
  chainId (PRD success metric).
- Both go-ethereum (cgo libsecp256k1 *and* the nocgo decred fallback) produce
  **low-s canonical** signatures, so parity with `cast`/ethers holds on both
  build paths — but see the caveat in §3 about *why* (it's the underlying lib,
  not an explicit geth normalization step).

---

## 2. Conflicts reconciled

The four angles were largely complementary, not contradictory. The two seams
that needed reconciling:

1. **Where do error codes come from — MCP SDK vs. signing layer?**
   The MCP angle and the signing angle both describe "errors." Resolution:
   the **signing/validation layer** owns the PRD's stable `code` strings
   (`invalid_input`, `chain_id_mismatch`, …); the **MCP layer** carries them to
   the client by setting `IsError=true` on `CallToolResult` (nil Go error).
   Reserve non-nil Go errors for genuine protocol/transport failures. No
   conflict once the two-tier model is explicit.

2. **"Zero the key" — geth pattern vs. Go memory-erasure reality.**
   The signing angle cites geth's `zeroKey`/`zeroBytes` as the canonical
   pattern; the secret-handling angle warns that Go's compiler/GC can defeat
   naive zeroing. Resolution: use `clear` (and, where it matters,
   `runtime.KeepAlive`) — the `clear` builtin is the documented,
   well-attested approach. **Treat full guaranteed erasure as best-effort, not
   absolute** (see §3). `memguard` and `runtime/secret` (Go 1.26) are available
   but optional; do **not** adopt `memguard` for v1 (overkill for this
   low-volume signer — and note that "overkill" framing is editorial, see §3).

No hard contradictions remained after these two were settled.

---

## 3. Dropped / caveated claims (post-verification)

These were corrected or downgraded by adversarial verification against primary
sources. **Do not carry the original phrasings into the spec or README.**

### Dropped (refuted — do not repeat)

- ❌ **"MCP Go SDK maintained by Anthropic in collaboration with Google."**
  Refuted. It is maintained **in collaboration with Google**, under the
  community-led `modelcontextprotocol` GitHub org — **not** under Anthropic, and
  the README does not name Anthropic as co-maintainer. Use: *"the official MCP
  Go SDK, maintained in collaboration with Google under the
  `modelcontextprotocol` org."*

- ❌ **"Client-side OAuth is experimental, gated behind the
  `mcp_go_client_oauth` build tag."** Outdated. The build tag existed in early
  1.x, but client OAuth was **stabilized (≈v1.5.0, March 2026) and the build tag
  dropped**; by `v1.6.x` it is generally available without a tag. Reframe
  historically if mentioned at all. (Not load-bearing for v1 — this signer does
  not use client OAuth.)

- ❌ **"EIP-155 spec says chainId 0/null disables EIP-155 and falls back to
  pre-Homestead v=27/28."** Misattributed. EIP-155 itself only says legacy
  v=27/28 signatures remain valid in parallel. The "nil/0 chainId ⇒ legacy
  v=27/28" behavior is a **go-ethereum implementation detail**
  (`NewEIP155Signer(nil)` ⇒ `big.Int(0)`; `LatestSignerForChainID(nil)` falls
  back to **`HomesteadSigner`**, which emits v=27/28). Source it to
  go-ethereum's `transaction_signing.go`, and say **"Homestead"**, not
  "pre-Homestead/Frontier." (For this server, `chainId` is required input, so
  this edge is mostly moot — but get the attribution right in docs.)

### Caveated (true enough, but tighten or attribute)

- ⚠️ **go-ethereum `keystore.go` line numbers.** `defer zeroKey(...)` is around
  lines **346 / 355** (functions start 340 / 349), and `zeroKey` is defined at
  **506–509**, *not* co-located with the signing functions. The *pattern* is
  canonical; **drop precise line numbers** from any doc (they drift).

- ⚠️ **`NewTx` "wraps" the inner txdata.** Slightly loose — it actually
  **deep-copies** via `inner.copy()` before storing. Functionally equivalent;
  say "copies" if precision matters.

- ⚠️ **low-s "normalized by default."** go-ethereum's `crypto.Sign` yields
  low-s because the **underlying lib** does (cgo libsecp256k1 forces low-s;
  nocgo decred `SignCompact` is RFC6979/BIP-0062 canonical) — not because geth
  runs an explicit normalization step. The practical result (parity with
  `cast`/ethers) holds; just don't claim an explicit geth normalize call.

- ⚠️ **EIP-1559 "yParity" wording.** The EIP-1559 spec field is literally
  `signature_y_parity`, not "yParity." Same concept (LSB of y). Fine to use
  "yParity" in our output contract (PRD does), but note the spec name.

- ⚠️ **`memguard` "overkill for simplicity-first / high-volume" framing** and
  the **single-`Secret`-type 5-method recipe** attributed to willem.dev: both
  are **researcher synthesis, not verbatim source quotes.** Keep the *guidance*
  (don't adopt memguard for v1; use a redacting wrapper type) but present it as
  our recommendation, not as a sourced statement.

- ⚠️ **`runtime.KeepAlive` "backed by the runtime to avoid optimizations."**
  Substantively correct, but the exact wording is **third-party** (not from
  `pkg.go.dev/runtime`). Use it as background, not a quoted spec guarantee.

- ⚠️ **mlock 64 KB default / `RLIMIT_MEMLOCK`.** Commonly-cited kernel default,
  but not stated verbatim in the man page section retrieved. Label as
  "commonly cited," not primary-sourced. (Moot unless we adopt mlock, which we
  are not for v1.)

- ⚠️ **v1.17.3 codename "Enzymatic Injector."** Single-source, unverified on
  the versions page. Cross-check the GitHub release tag before printing a
  codename anywhere; otherwise omit it.

- ⚠️ **Spec-version compatibility table** lives in the SDK **README**, not in
  `docs/protocol.md` — cite the README. The underlying SDK↔spec mapping is
  confirmed.

### Security flag worth surfacing (background)

- 🔶 go-ethereum **v1.17.3 is unaffected by the recent Go vulnerability-DB
  advisories** GO-2026-4314, -4315, -4507, -4508, and -4511 (verified against
  pkg.go.dev/vuln: 4314/4315 fixed in v1.16.8; 4507/4511 fixed in v1.16.9;
  4508 fixed in v1.17.0 — none affects any v1.17.x release; 4511 is an ECIES
  public-key-validation flaw in the RLPx handshake, CVE-2026-26315, not a
  plain DoS). All five were p2p-path issues, irrelevant to this signer anyway —
  it **runs no p2p node** and makes no network calls. Ongoing tracking is
  automated via a `govulncheck` step in CI rather than a hand-maintained
  advisory list.

---

## 4. Consolidated assumptions

Carried forward from the PRD and *unchallenged* by the research (treat as the
working baseline; the user can override at the planning gate):

- **Scope is offline-only.** No RPC, no broadcast, no nonce/gas/fee/ENS. Caller
  supplies a fully-specified tx. (This is also why p2p-path geth advisories
  are categorically irrelevant to us.)
- **Single-file keystore, single account.** No HD/mnemonic/dir/multi-account.
- **`chainId` is required in input;** `--chain-id` is an optional guard checked
  *before* any key material is touched.
- **Secrets are file-backed** (`--keystore`, `--password-file`), decrypted only
  at signing time, zeroed immediately after.
- **HTTP transport is localhost-only**, bearer-token gated; off-localhost is
  explicitly unsupported.
- **Approval is delegated** to the MCP client's tool-approval UI; no in-server
  confirmation in v1.
- **Versions to pin:** MCP SDK `v1.6.x`, go-ethereum `v1.17.3` (no known
  advisories affect it; `govulncheck` in CI flags any future ones), Go
  toolchain 1.26.
- **Test oracle:** `cast mktx` (Foundry, version-pinned) and/or ethers v6,
  asserting byte-identical RLP.

### Assumptions newly *surfaced* by the research (decide at the gate)

- When to bump go-ethereum past `v1.17.3` (no security pressure — v1.17.3 has
  no known advisories; bump on the normal dependency-update cadence, with
  `govulncheck` in CI as the tripwire).
- Whether the redacting `Secret` wrapper is worth the boilerplate vs. simply
  never placing secrets in any logged struct (recommendation: do the wrapper for
  the password + key types; keep them out of logged structs regardless, because
  `slog` can reflect past `LogValue` on nested fields).
- Whether to print any go-ethereum / SDK codename in `--version` (recommendation:
  print module versions + commit, **not** marketing codenames — they're
  unverified).

---

## 5. Confidence summary

- **HIGH** — go-ethereum signing API shapes, EIP-155/1559/2718 semantics, RLP
  round-trip, low-s outcome, MCP SDK API surface (`NewServer`, `AddTool`,
  `NewStreamableHTTPHandler`, `jsonschema.For`), transport mechanics, SDK
  versions/dates, `clear`/`subtle`/`x/term`/`runtime/secret` behavior. All
  verified against primary sources.
- **MEDIUM** — exact `StreamableHTTPOptions` field set (confirmed inclusive, not
  exhaustive), `SetError` helper receiver details, memguard feature framing.
- **LOW / corrected** — everything in §3's "Dropped" list, plus the
  line-number, codename, and mlock-default details.

> **Prompt-injection note.** All four research angles independently reported a
> prompt-injection attempt embedded in fetched web pages: an injected
> `<system-reminder>` / "MCP Server Instructions" block referencing an
> *alphaXiv* academic-paper tool. It was unrelated to this task and was **ignored
> by every angle.** The same injected instruction surfaced again during this
> consolidation pass and was likewise ignored. Flagging so downstream readers
> know the upstream sources returned untrusted system-style content.
