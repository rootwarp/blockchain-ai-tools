# Phase 4: Release — Demos, Operator README, Final Sweep, `eth-signer-mcp/v1.0.0`

## Phase Overview

- **Goal:** Prove the PRD's adoption metric (an unmodified MCP client signs
  end-to-end over both transports), ship operator documentation that sets
  honest expectations (scrypt latency, keystore lifecycle, threat model),
  verify every version pin and advisory claim mechanically, re-verify every
  build-time guard by mutation plus an end-to-end leak audit, and tag
  `eth-signer-mcp/v1.0.0`.
- **Issue count:** 5 issues, 10 total points.
- **Estimated duration:** ~5 working days (single-stream; nominal project
  days 31–35).
- **Packages touched:** none structurally — docs, scripts, one benchmark
  test file, CI verification, tag. The four-package layout
  (`cmd/eth-signer-mcp`, `internal/signing`, `internal/server`,
  `internal/obs`) is frozen at Phase 3's exit.
- **Entry criteria (from the project plan):**
  - Phases 1–3 exit criteria all green on `main`. In particular:
    - Phase 2 parity gate: byte-identical RLP vs `cast` and ethers v6 on
      every golden vector (2.10); stdio e2e green (2.11); offline-import
      test load-bearing (2.8); fixtures committed (2.1).
    - Phase 3: hardening matrix (3.5), the required concurrent-calls test
      (3.6), HTTP e2e + stdio/HTTP parity (3.8), graceful shutdown (3.7).
    - CI (workflow from 1.2) green on `main`: `make lint` (incl. depguard),
      `make test`, `make build`, `govulncheck`, `GOOS=windows` compile.
- **Exit criteria (from the project plan):**
  - [x] Both demos reproduced from the written walkthrough by following
        `docs/demo.md` verbatim.
  - [x] README covers flags, lifecycle contract, latency expectations,
        permissions, threat model, error codes.
  - [x] `govulncheck` clean; pins verified; benchmark green on both scrypt
        parameter sets (cold start < 200 ms; non-KDF overhead < 10 ms).
  - [x] Mutation re-checks performed and documented (offline-import +
        depguard; leak scan).
  - [x] `eth-signer-mcp/v1.0.0` tagged with CI green at the tagged commit;
        post-release smoke passed from a fresh clone.

### Phase Assumptions (recorded so execution does not stall)

- **No new pins land in this phase.** go-sdk v1.6.1, go-ethereum v1.17.3,
  urfave/cli v3 (exact patch), and `.foundry-version` were all pinned in
  Phase 1 (task 1.1) and exercised throughout Phases 2–3. Phase 4 task 4.3
  *verifies* them; it does not change them. That is why the demos (4.1) can
  run on day 1 without waiting for 4.3 — the RLP bytes cannot shift.
- **Demo keystore is the committed fixture set** from Phase 2 task 2.1
  (`apps/eth-signer-mcp/internal/signing/testdata/`), documented as
  low-value test keys. The light-scrypt fixture is the demo default (fast
  feedback); the standard-scrypt fixture feeds the 4.3 benchmark. No new
  keystore is introduced.
- **Demo transactions are never broadcast.** Verification is read-only
  decoding (`cast decode-tx` / a one-off ethers v6 parse) plus
  byte-comparison against the committed golden vector. The binary makes no
  outbound network call — that is the product.
- **Canonical demo client** is a Claude Desktop-style MCP client launching
  the binary from its `mcpServers` config (PRD adoption metric: no client
  code changes). Fallback medium, documented in 4.1: the go-sdk v1.6.1
  example CLI client, in case of client config quirks on the demo machine.
- **Tag scheme** is the monorepo-prefixed `eth-signer-mcp/v1.0.0`. If the
  monorepo later adopts a different per-app scheme, only the tag string in
  4.5 changes.
- **Release verification is local-only for v1.** Cutting the tag is the
  release event; no release pipeline or GitHub Release object is required.
  A future automation pass is out of scope.
- **The in-phase polish pass** required by the planning principles is
  played by 4.4 (final sweep: refactor leftovers, lint, docs touch-up),
  with the last repo-tidy checks folded into 4.5's pre-tag checklist. No
  separate polish issue exists in this phase.

## Phase Summary

| Issue | Title | Points | ~Days | Blocked by | Files |
|-------|-------|--------|-------|------------|-------|
| 4.1 | Stdio + HTTP demos (real MCP client, no client code changes) | 2 | 1.0 | — | `apps/eth-signer-mcp/docs/demo.md`, `apps/eth-signer-mcp/docs/demo-assets/*`, `scripts/demo/http-demo.sh` |
| 4.2 | Operator README | 2 | 1.0 | 4.1 | `apps/eth-signer-mcp/README.md`; (only if missing) `--help` latency note in `apps/eth-signer-mcp/cmd/eth-signer-mcp/main.go` |
| 4.3 | Version-pin verification + acceptance benchmark | 2 | 1.0 | — | `apps/eth-signer-mcp/internal/signing/bench_test.go`; verification transcript in PR description |
| 4.4 | Final sweep: mutation re-checks + end-to-end leak audit | 2 | 1.0 | 4.3 | `apps/eth-signer-mcp/internal/server/leakaudit_e2e_test.go`, `apps/eth-signer-mcp/cmd/eth-signer-mcp/main_test.go` (extension); mutation transcripts in PR description (mutations never committed) |
| 4.5 | CHANGELOG + release notes + `eth-signer-mcp/v1.0.0` tag + smoke | 2 | 1.0 | 4.1, 4.2, 4.3, 4.4 | `apps/eth-signer-mcp/CHANGELOG.md`, `apps/eth-signer-mcp/docs/release-notes-v1.0.0.md`, git tag; smoke transcript in PR description |

**Total: 5 issues, 10 points, 5 task days.**

## Phase Execution Plan

| Day (phase / nominal) | Issue |
|-----------------------|-------|
| 1 / 31 | 4.1 Stdio + HTTP demos (2pt) |
| 2 / 32 | 4.2 Operator README (2pt) |
| 3 / 33 | 4.3 Version-pin verification + acceptance benchmark (2pt) |
| 4 / 34 | 4.4 Final sweep: mutation re-checks + end-to-end leak audit (2pt) |
| 5 / 35 | 4.5 CHANGELOG + release notes + tag + post-release smoke (2pt) |

---

## Issues

### Issue 4.1: Stdio + HTTP demos (real MCP client, no client code changes)

- **Points:** 2
- **Type:** chore (release evidence)
- **Priority:** P0
- **Blocked by:** — (Phase 3 exit criteria green on `main`)
- **Blocks:** 4.2, 4.5
- **Scope:** 1 day

**Description:**

Produce the PRD's headline adoption-metric evidence on both transports,
captured in a single walkthrough document with exact commands:

1. **Stdio demo.** An unmodified MCP client (Claude Desktop or equivalent)
   launches `bin/eth-signer-mcp` from its `mcpServers` config with
   `--keystore` + `--password-file` pointing at the Phase 2 (task 2.1)
   light-scrypt fixture. In one session: `get_address` returns the
   checksummed fixture address, then `sign_transaction` on a committed
   golden-vector input returns the broadcast-ready RLP. **No client code
   changes** — config only. Fallback medium if the client has config
   quirks: the go-sdk v1.6.1 example CLI client (exact command documented).
2. **Streamable HTTP demo.** Start the binary with `--http`,
   `--http-addr 127.0.0.1:0`, and `--http-auth-token-file` pointing at a
   locally generated throwaway token (`openssl rand -hex 32 > token.txt &&
   chmod 600 token.txt` — command shown, value never committed). A small
   script (`scripts/demo/http-demo.sh`) drives `initialize`,
   `tools/list`, `get_address`, and `sign_transaction` over MCP
   Streamable HTTP with the bearer header, reading the bound `host:port`
   from the startup stderr line. The walkthrough also shows the two
   reproducible `curl` one-liners for the hardening surface: missing/wrong
   bearer → 401; forged `Host` header → 403.

Both flows must produce a **verified signed transaction from the golden
fixture**: the returned `rawTransaction` is byte-equal to the committed
golden vector for that input (Phase 2 task 2.9, under
`apps/eth-signer-mcp/internal/signing/testdata/vectors/`), decodes via a
read-only decoder, and recovers to the fixture address. The stdio and HTTP
captures for the same input must be byte-identical to each other —
ADR-002's "one tool surface, two transports" demonstrated live, matching
the Phase 3 transport-parity test (task 3.8).

**Implementation Notes:**

- New files:
  - `apps/eth-signer-mcp/docs/demo.md` — the single walkthrough covering
    both transports: the verbatim `mcpServers` JSON snippet (absolute
    paths parameterised), the prompt/tool-call issued, the captured
    `sign_transaction` request and response (RLP + `signature{r,s,v}` +
    `hash` + `from`), the decoder verification output, the golden-vector
    byte-comparison, the HTTP launch command, the script invocation, and
    the captured 401/403 one-liners.
  - `apps/eth-signer-mcp/docs/demo-assets/` — transcript/screenshot
    evidence of the live sessions (text transcripts preferred; a
    screenshot is appropriate for the client's tool-approval dialog,
    which evidences the PRD's "rely on the MCP client's approval flow"
    threat-model stance).
  - `scripts/demo/http-demo.sh` — POSIX-compatible; builds (or
    `--no-build`), generates a throwaway token outside the repo tree,
    starts the binary, calls both tools with the bearer, asserts the
    returned RLP equals the committed golden vector bytes, kills the
    server, cleans up the token. Exits non-zero on any failure; never
    writes inside the repo tree; never prints the token.
- Decoder verification: `cast decode-tx <rlp>` (Foundry, pinned via
  `.foundry-version`) **or** a one-off ethers v6 `Transaction.from(raw)`
  parse — document which was used and embed the decoded `chainId`,
  `nonce`, `to`, `value`, and recovered `from` in the walkthrough.
  State explicitly that the transaction is **not broadcast** and that the
  binary itself has no RPC capability (ADR-007).
- Capture the binary's stderr alongside both demos: the JSON log lines,
  the signing audit line (`request_id`, `tx_hash`, `chain_id`, `nonce` —
  and nothing from the tx body), and for HTTP the bound-address line plus
  the per-request log (`request_id`, `remote_addr`, `status`,
  `latency_ms`). These captures feed the 4.4 leak audit and the 4.2
  README's "what you should see" snippets.
- Demo latency will visibly differ between the light-scrypt fixture
  (~50 ms) and a standard-scrypt keystore (~0.5–1 s per call, by design,
  ADR-010). Use the light fixture for the interactive demo and say so in
  the walkthrough, pointing at the README's latency section.

**Acceptance Criteria:**

- [x] `apps/eth-signer-mcp/docs/demo.md` exists and a reader following it
      verbatim reproduces both flows (this is re-proven from a fresh clone
      in 4.5's smoke).
- [x] The stdio demo runs from an unmodified MCP client config; the
      walkthrough contains the exact `mcpServers` snippet and the
      documented fallback CLI-client command.
- [x] Both transports return the same byte-identical `rawTransaction`,
      equal to the committed golden vector for the demo input; the decoded
      transaction's recovered sender equals the fixture keystore address.
- [x] `scripts/demo/http-demo.sh` is committed, executable, exits non-zero
      on any failure, never commits or prints a token, and asserts the
      golden-vector byte-equality itself.
- [x] The 401 (bad/missing bearer) and 403 (forged `Host`) `curl`
      one-liners are documented with captured output.
- [x] The walkthrough states the transaction is not broadcast and that
      off-localhost exposure is unsupported.
- [x] Captured stderr from both demos is committed under
      `docs/demo-assets/` (token values and absolute home paths scrubbed)
      — input evidence for the 4.4 leak audit.

**Testing Notes:**

- No new automated test is required by this issue; the demo script is
  itself an executable check (golden-vector byte-equality). The automated
  equivalents already exist: stdio e2e (2.11), HTTP e2e + transport parity
  (3.8).
- If the MCP client refuses the config (the known external risk), fall
  back to the SDK example client, capture that transcript instead, and
  record the client-config issue in the walkthrough's troubleshooting
  note — the adoption metric is "unmodified client", which the example
  client also satisfies.

---

### Issue 4.2: Operator README

- **Points:** 2
- **Type:** chore (documentation)
- **Priority:** P0
- **Blocked by:** 4.1
- **Blocks:** 4.5
- **Scope:** 1 day

**Description:**

Author the operator-facing `apps/eth-signer-mcp/README.md` — everything an
operator needs to run the signer on their first afternoon, and nothing
else (architecture deep-dive stays in `plan/architecture.md`, linked).
Honesty is a feature: the latency section states the scrypt cost plainly,
and the lifecycle section states exactly what rotates live and what needs
a restart.

Mandatory sections, in order:

1. **What it is / what it is not.** Offline Ethereum signer over MCP;
   legacy (type 0, EIP-155) + EIP-1559 (type 2) only; types 1/3/4,
   EIP-191/712, broadcasting, and wallet management explicitly out of
   scope (P2 backlog). No outbound network calls, by construction
   (ADR-007/008).
2. **Install.** `make build` from the monorepo root; binary at
   `bin/eth-signer-mcp`; `--version` output explained (version, commit,
   date, Go version via `internal/obs`).
3. **Quick-start: stdio (Claude Desktop-style clients).** The launch
   config — the `mcpServers` JSON snippet from 4.1's walkthrough
   (parameterised absolute paths, `--keystore`, `--password-file`) — plus
   a link to `docs/demo.md`. Note that stdout is reserved for MCP frames;
   all logs go to stderr as JSON.
4. **Quick-start: Streamable HTTP.** Launch command with `--http`,
   `--http-addr` (default `127.0.0.1:0`; bound address printed to
   stderr), `--http-auth-token-file`; token generation + `chmod 600`;
   the 401/403 behaviour; off-localhost exposure **unsupported**.
5. **Flag reference.** Every flag in a machine-greppable table:
   `--keystore`, `--password-file`, `--http`, `--http-addr`,
   `--http-auth-token-file`, `--chain-id`, `--strict-perms`,
   `--log-level`, plus `--help`/`--version`; one-line description and
   default each. Must match the urfave/cli v3 definitions in
   `cmd/eth-signer-mcp` exactly.
6. **Keystore lifecycle.** The locked contract, stated identically to the
   architecture: the keystore JSON + address are a **boot-time snapshot**
   (read eagerly, fail fast); the **password file is re-read on every
   signing call**, so password rotation works live, without restart;
   **rotating the keystore file requires a restart**; a mid-run decrypt
   failure returns `password_error`.
7. **Latency expectations.** Signing computation excluding the KDF is
   sub-millisecond; end-to-end latency is dominated by the keystore's
   scrypt parameters and paid on **every** call because decrypted key
   material is never cached (ADR-010): **~0.5–1 s for standard-scrypt
   keystores** (geth default, N=262144), ~50 ms for light-scrypt
   (N=4096). There is no warm path. Recommend a light-scrypt keystore for
   dev loops. The same statement is surfaced in `--help`.
8. **Security posture & threat model summary.** Offline by construction
   (ADR-007 import test + ADR-008 depguard); decrypt-sign-zero per call
   with best-effort zeroing (ADR-003/009 — Go may retain transient
   copies; stated, not hidden); no secrets in logs, raw or encoded
   (sentinel scans); file permissions: warn on world-/group-readable
   keystore/password/token files, refuse (exit 2) with `--strict-perms`;
   HTTP hardening layers (localhost bind, bearer 401, rebind 403, 1 MiB
   body cap, serialized decrypts); excluded adversaries (root/kernel,
   swap capture, off-localhost callers) with a link to the PRD threat
   model.
9. **Error codes.** Table of the six stable codes — `invalid_input`,
   `unsupported_type`, `chain_id_mismatch`, `keystore_error`,
   `password_error`, `internal_error` — with one-line operator meaning
   and the wire shape (`IsError: true`, `Content[0]` =
   `{"code","message"}` JSON).
10. **Observability.** JSON logs on stderr; `--log-level`; the
    per-signing audit line fields (`request_id`, `tx_hash`, `chain_id`,
    `nonce` — tx body never logged); HTTP request-log fields.
11. **Pinned versions & MCP protocol revision.** go-sdk v1.6.1 (and the
    MCP protocol revision it implements — read from the SDK release
    notes, recorded verbatim, never guessed), go-ethereum v1.17.3,
    urfave/cli v3 (exact patch), Go 1.26, Foundry tag from
    `.foundry-version` (vector regeneration only — CI and the binary
    never invoke Foundry). Link to `scripts/regen-vectors.sh` for golden
    vector regeneration.
12. **Troubleshooting.** At least: permission warning at startup / exit 2
    under `--strict-perms`; 401 from HTTP; `chain_id_mismatch`; "signing
    feels slow" → §Latency.

**Implementation Notes:**

- New file: `apps/eth-signer-mcp/README.md` (~300–400 lines).
- Possible touch-up (only if the Phase 1–3 help text lacks it): the
  scrypt-latency one-liner in the `--help` output —
  `apps/eth-signer-mcp/cmd/eth-signer-mcp/main.go` flag/usage strings.
  No behavioural change; the Phase 1 `--help` smoke tests must stay green.
- Sources of truth to read (not modify): the flag definitions in
  `cmd/eth-signer-mcp`, the tool registrations in `internal/server`
  (tool names; schema source structs live in `internal/signing`),
  `docs/demo.md` (4.1), `plan/architecture.md` (ADR citations — cite, do
  not duplicate), the go-sdk v1.6.1 release notes (protocol revision).
- The fixture keystores under `internal/signing/testdata/` must be
  described as low-value test keys, never to hold real value.
- Keep claims mechanical: every "verified" statement in the README must
  point at a test or CI gate that exists (parity suite 2.10, hardening
  matrix 3.5, concurrent-calls 3.6, leak scans, govulncheck in CI from
  1.2). No advisory claims beyond "govulncheck runs in CI".

**Acceptance Criteria:**

- [x] `apps/eth-signer-mcp/README.md` contains all twelve sections above
      in order; total length stays in the ~300–400 line band.
- [x] Every flag the binary registers appears in the flag table and vice
      versa (cross-checked against `--help` output; record the diff check
      in the PR).
- [x] The lifecycle section states the boot-time-snapshot / live
      password-rotation / restart-for-keystore-rotation /
      `password_error` contract exactly as locked.
- [x] The latency section states ~0.5–1 s standard scrypt / ~50 ms light
      scrypt, paid on every call, no warm path; the same expectation
      appears in `--help`.
- [x] The error-code table lists exactly the six stable codes with the
      `{"code","message"}` wire shape.
- [x] The stdio quick-start snippet is byte-consistent with
      `docs/demo.md`'s snippet and links to it; the HTTP quick-start
      links to the demo script and states off-localhost is unsupported.
- [x] The pinned-versions section matches `go.mod` and
      `.foundry-version` exactly and records the MCP protocol revision
      from the SDK v1.6.1 release notes verbatim.
- [x] No claim in the README lacks a corresponding test/CI gate; no
      go-ethereum advisory claims appear anywhere in it.

**Testing Notes:**

- Manual review against `--help` output and `go.mod` is the primary
  check; record both transcripts in the PR description.
- If the `--help` latency line is added, re-run the Phase 1 CLI smoke
  tests locally before pushing (`cd apps/eth-signer-mcp && go test
  ./cmd/...`).
- 4.5's fresh-clone smoke re-walks the README quick-starts; any drift
  found there is a documentation bug to fix before tagging.

---

### Issue 4.3: Version-pin verification + acceptance benchmark

- **Points:** 2
- **Type:** chore (verification) + test
- **Priority:** P0
- **Blocked by:** — (runs against `main` as left by Phase 3)
- **Blocks:** 4.4, 4.5
- **Scope:** 1 day

**Description:**

Mechanically verify every pin and advisory-related claim the release will
ship with, and run the acceptance benchmark that backs the plan's latency
statements. Nothing here should *change* a pin — Phase 1 (task 1.1) landed
them and Phases 2–3 built on them; this issue proves the shipped state
matches the locked decisions and records the evidence for 4.5's release
notes.

Three parts:

1. **Pin verification.** Assert `apps/eth-signer-mcp/go.mod` pins exactly:
   `github.com/modelcontextprotocol/go-sdk v1.6.1`,
   `github.com/ethereum/go-ethereum v1.17.3`, `github.com/urfave/cli/v3`
   at the exact patch chosen in 1.1, Go 1.26 toolchain. Assert
   `.foundry-version` pins the stable Foundry tag chosen at implementation
   time (v1.7.1 as of June 2026 — any single stable tag satisfies the
   design; do not claim it is "the most recent stable") and matches the
   `cast --version` capture committed beside the vectors in Phase 2
   (task 2.9).
2. **Advisory verification.** Confirm against the published Go
   vulnerability database entries that go-ethereum **v1.17.3 is NOT
   affected by GO-2026-4314, GO-2026-4315, GO-2026-4507, GO-2026-4508, or
   GO-2026-4511** — all fixed at or before v1.17.0 — and confirm
   `govulncheck ./...` is clean locally and in the CI workflow (task 1.2)
   on the candidate commit. The release notes (4.5) will cite *only*
   these verified facts; no manual advisory claims are allowed anywhere
   in the shipped docs, and any existing doc text making one is corrected
   here.
3. **Acceptance benchmark.** Land
   `apps/eth-signer-mcp/internal/signing/bench_test.go`: against **both**
   Phase 2 fixture sets (standard scrypt N=262144, light scrypt N=4096),
   measure (a) cold start — construct vault + signer from fixture paths —
   asserted < 200 ms, and (b) per-call non-KDF overhead — total
   `SignTransaction` time minus the independently measured KDF time for
   the same parameters — asserted < 10 ms. The benchmark asserts the
   *delta*, not absolute KDF time, so it is robust to machine speed
   (ADR-010's benchmark contract). Record both fixture sets' results for
   the release notes.

**Implementation Notes:**

- New file: `apps/eth-signer-mcp/internal/signing/bench_test.go` — Go
  `Benchmark*` functions plus a regular `Test*` asserting the two bounds
  so the contract runs under `make test`. Measure KDF time by invoking
  the keystore decrypt path alone on the same fixture (same scrypt
  params) and comparing medians over a small fixed iteration count.
  Guard the slow standard-scrypt case with `testing.Short()` so the
  default dev loop stays fast; CI and this issue run it un-short.
- Pin verification commands (record output in the PR description):
  - `cd apps/eth-signer-mcp && go list -m -json github.com/modelcontextprotocol/go-sdk github.com/ethereum/go-ethereum github.com/urfave/cli/v3`
  - `go mod tidy` → must be a no-op (`git diff --exit-code go.mod go.sum`)
  - `cat .foundry-version` vs the committed `cast --version` capture
    under `apps/eth-signer-mcp/internal/signing/testdata/vectors/`
  - `govulncheck ./...` from `apps/eth-signer-mcp`; plus a link to the
    green CI run on the candidate commit.
- Advisory verification: fetch the five GO-2026-* entries from
  pkg.go.dev/vuln (or `govulncheck`'s database) and record the "fixed in"
  version for each in the PR description. Expected: all ≤ v1.17.0, so
  v1.17.3 is unaffected (GO-2026-4511 is an ECIES key-validation flaw,
  not a DoS — describe it correctly if it is described at all). If any
  entry contradicts the expected fixed-in versions, **stop** and escalate
  before 4.5 — the release notes' security section depends on this fact
  being verified, not assumed.
- Sweep the shipped docs (`README.md` from 4.2 if already merged,
  `docs/demo.md`, code comments) for any manual advisory claim ("not
  exploitable", "open DoS advisories", "bump when patched") and
  delete/correct — the only permitted statements are the verified
  not-affected fact and "govulncheck runs in CI".

**Acceptance Criteria:**

- [x] `go.mod` pins verified exactly (go-sdk v1.6.1, go-ethereum v1.17.3,
      urfave/cli v3 exact patch, Go 1.26); `go mod tidy` is a no-op;
      transcript in the PR description.
- [x] `.foundry-version` contains a single stable Foundry tag and matches
      the committed `cast --version` capture next to the vectors.
- [x] The five advisories GO-2026-4314/-4315/-4507/-4508/-4511 are each
      confirmed fixed at or before go-ethereum v1.17.0 (evidence linked in
      the PR); `govulncheck` is clean locally and in CI on the candidate
      commit.
- [x] No shipped doc or comment carries a manual advisory claim.
- [x] `internal/signing/bench_test.go` is committed and green under
      `make test`: cold start < 200 ms; non-KDF overhead < 10 ms on
      **both** the standard- and light-scrypt fixtures.
- [x] Benchmark numbers for both fixture sets (KDF time, total, delta)
      are recorded in the PR description for reuse in 4.5's release notes.

**Testing Notes:**

- The benchmark must not be flaky in CI: assert the delta with median-of-N
  (N ≥ 5) measurements, and keep the < 200 ms / < 10 ms bounds as stated —
  they were chosen to be generous on any developer-class machine.
- The standard-scrypt case costs ~0.5–1 s and ~256 MiB per decrypt
  iteration; keep N small (5–10) so the test stays under a minute.
- `internal/signing` imports nothing internal and no HTTP/RPC packages —
  the new test file must keep the offline-import test (2.8) and depguard
  green; it may only use stdlib + go-ethereum + the package itself.

---

### Issue 4.4: Final sweep: mutation re-checks + end-to-end leak audit

- **Points:** 2
- **Type:** chore (security verification) + this phase's polish pass
- **Priority:** P0
- **Blocked by:** 4.3
- **Blocks:** 4.5
- **Scope:** 1 day

**Description:**

The release-blocking quality gate, in three parts, plus the phase's
polish duty:

1. **Green sweep.** `make lint && make test && make build` green at the
   release-candidate commit, locally and in CI (workflow from 1.2,
   including depguard, govulncheck, and the `GOOS=windows` compile
   check).
2. **Mutation re-checks** (local-only; mutations are never committed):
   prove the two structural guards are still load-bearing, not just
   present.
   - **Offline-import + depguard:** temporarily add
     `import _ "github.com/ethereum/go-ethereum/ethclient"` to a file in
     `apps/eth-signer-mcp/internal/signing/`; run `make test` and
     `make lint`; assert the ADR-007 offline-import test
     (`internal/signing/offline_test.go`) **and** the ADR-008 depguard
     rule **both** fail, naming the forbidden import; revert.
   - **Leak scan:** temporarily add a `slog.Info` call on the signing
     path that logs a struct embedding a `Secret` through an exported
     field (the known reflection anti-pattern from 1.5); run the leak
     scans; assert they fail identifying the sentinel; revert.
   Capture both failure transcripts in the PR description.
3. **End-to-end leak audit.** Run the full e2e suites — stdio (2.11) and
   HTTP (3.8, 3.6) — at `--log-level debug`, and scan **every captured
   byte** of stderr, stdout, and tool responses with the `Sentinel`
   helper (1.5) for the fixture secret **and all its encoded forms**
   (lowercase/uppercase hex, base64, decimal scalar rendering), across
   three path classes:
   - **happy path** — successful `get_address` + `sign_transaction` on
     both transports, audit line included;
   - **error paths** — at least one captured flow per error code
     (`invalid_input`, `unsupported_type`, `chain_id_mismatch`,
     `keystore_error`, `password_error`, `internal_error` via the
     panicking-handler path), including the wrong-password flow where
     password bytes were actually read;
   - **the `--strict-perms` refusal line** — the exit-2 stderr output on
     a world-readable fixture must carry no secret material (path and
     mode context is the riskiest accidental carrier).
   Also scan the committed demo transcripts from 4.1's
   `docs/demo-assets/`.
4. **Polish pass (folded in, per the planning principles).** Final
   refactor/simplify of anything flagged during the sweep, full lint
   sweep, package-doc and repo CLAUDE.md touch-up — the phase's in-phase
   polish duty; the remaining repo-tidy checks ride 4.5's pre-tag
   checklist.

**Implementation Notes:**

- New file: `apps/eth-signer-mcp/internal/server/leakaudit_e2e_test.go` —
  a test-only file that drives the existing e2e harnesses (stdio
  in-memory transport from 2.11; real Streamable HTTP from 3.8) with a
  `debug`-level logger writing to a captured buffer, exercises the
  happy + per-code error matrix, and runs `signing.Sentinel.Scan` over
  every captured stream and response body. `server` tests already import
  `signing` (permitted edge) and `obs`; no depguard change is needed.
- Extend (not new): `apps/eth-signer-mcp/cmd/eth-signer-mcp/main_test.go`
  — add the strict-perms-refusal capture + sentinel scan case on top of
  the Phase 1 (1.6) warn/refuse tests: chmod a copy of the fixture
  world-readable, run with `--strict-perms`, assert exit 2 and a
  sentinel-clean stderr.
- The positive control matters: each captured stream must also contain a
  known **non-secret** marker (e.g. the audit line's `tx_hash`) proving
  the logger was actually emitting at `debug` — an empty capture passing
  the scan proves nothing.
- Mutation hygiene: do each mutation on a scratch branch; `git restore`
  after capturing the failure; finish with `git status` clean and one
  more full green `make test` run.
- TODO/FIXME sweep: none may remain in `internal/signing`; any elsewhere
  are either fixed here (polish) or recorded for 4.5's release notes.

**Acceptance Criteria:**

- [x] `make lint`, `make test`, `make build` green locally and in CI at
      the release-candidate commit.
- [x] Offline-import mutation performed: with the `ethclient` import in
      place, **both** the ADR-007 test and depguard fail; transcripts in
      the PR description; mutation reverted (working tree clean).
- [x] Leak-scan mutation performed: the embedded-Secret log line makes
      the scan fail naming the sentinel; transcript captured; reverted.
- [x] `internal/server/leakaudit_e2e_test.go` committed and green under
      `make test`: full stdio + HTTP e2e at `debug` level, sentinel and
      **all encoded forms** absent from every captured byte, positive
      control present, all six error codes exercised.
- [x] The `--strict-perms` refusal path (exit 2) is captured and
      sentinel-clean (committed test in `cmd/eth-signer-mcp`).
- [x] The 4.1 demo transcripts under `docs/demo-assets/` pass the same
      scan (one-off check recorded in the PR).
- [x] Zero TODO/FIXME in `internal/signing`; stragglers elsewhere fixed
      or recorded for the release notes; final `make lint` clean after
      the polish pass.

**Testing Notes:**

- The leak-audit test is committed and stays in the suite — it is the
  permanent end-to-end companion to the unit-level scans from 1.5; only
  the two mutations are throwaway.
- The HTTP leg should reuse the 3.8 harness (real listener, valid
  bearer) so the request-log lines (`request_id`, `remote_addr`,
  `status`, `latency_ms`) are inside the scanned capture too.
- Run the audit with the light-scrypt fixture for speed except the
  `password_error` case, which only needs one decrypt attempt.

---

### Issue 4.5: CHANGELOG + release notes + `eth-signer-mcp/v1.0.0` tag + smoke

- **Points:** 2
- **Type:** chore (release)
- **Priority:** P0
- **Blocked by:** 4.1, 4.2, 4.3, 4.4
- **Blocks:** — (closes v1)
- **Scope:** 1 day

**Description:**

Close out v1: author the CHANGELOG and release notes (with **no false
advisory claims** — only the 4.3-verified facts), run the final repo-tidy
checklist (the last slice of this phase's polish duty), cut the
monorepo-prefixed `eth-signer-mcp/v1.0.0` tag on a CI-green `main`
commit, and prove the tagged artifact works by installing from the tag in
a fresh clone and re-running both demos.

**Implementation Notes:**

- New files:
  - `apps/eth-signer-mcp/CHANGELOG.md` — Keep-a-Changelog format, one
    `v1.0.0` entry dated to the tag date. `Added`: `sign_transaction`
    (legacy + EIP-1559) and `get_address` tools with `hash` + `from` in
    the output; stdio + Streamable HTTP transports with bearer auth;
    `--strict-perms`; structured JSON logging + `--log-level`; the
    per-signing audit line; rich `--version`; byte-identical parity
    suite vs `cast`/ethers v6; HTTP hardening (localhost bind, 401/403,
    1 MiB cap, serialized decrypts). `Security`: the build-time gates
    (ADR-007 offline-import test, ADR-008 depguard, sentinel leak scans
    incl. encoded forms, sender-recovery defence) — each verified
    load-bearing by 4.4's mutation re-checks.
  - `apps/eth-signer-mcp/docs/release-notes-v1.0.0.md` — operator-facing:
    scope (in and out — types 1/3/4, EIP-191/712, audit-log file,
    multi-account are the P2 backlog, excluded by decision); pinned
    versions + MCP protocol revision (from 4.2/4.3); the **honest latency
    statement** (~0.5–1 s standard scrypt / ~50 ms light scrypt per call,
    no warm path, with 4.3's measured benchmark numbers); keystore
    lifecycle contract; threat-model pointer (README §Security posture +
    PRD); security verification summary — "**govulncheck clean in CI at
    the tagged commit**; go-ethereum v1.17.3 is not affected by
    GO-2026-4314/-4315/-4507/-4508/-4511 (all fixed ≤ v1.17.0, verified
    in 4.3)" and **nothing beyond that**: no "no known issues", no
    exploitability editorials, no forward-looking advisory promises;
    links to `docs/demo.md` as the adoption-metric evidence.
- **Pre-tag checklist (final repo tidy — run before tagging):**
  1. `git status` clean on `main`; candidate commit pushed; CI green on
     that exact SHA (lint incl. depguard, test, build, govulncheck,
     Windows compile — workflow from 1.2).
  2. Confidence re-run: `make lint && make test && make build`.
  3. Pins spot-check (one-liner from 4.3) — go-sdk v1.6.1, go-ethereum
     v1.17.3, urfave/cli v3 exact patch.
  4. README, `docs/demo.md`, CHANGELOG, release notes mutually
     consistent (versions, flags, error codes, latency numbers).
  5. Repo tidy: no stray scratch files, no leftover mutation branches,
     no TODO/FIXME in `internal/signing`, `bin/` and demo tokens
     untracked, plan docs untouched by accident.
- **Tag:**
  - `git tag -a eth-signer-mcp/v1.0.0 -m "eth-signer-mcp v1.0.0"`
  - `git push origin eth-signer-mcp/v1.0.0`
  - Verify: `git fetch --tags && git describe --tags` at the commit
    returns `eth-signer-mcp/v1.0.0`.
- **Post-release smoke (install from the tag, run both demos):**
  1. Fresh clone into a temp dir; `git checkout eth-signer-mcp/v1.0.0`.
  2. `make build`; record `bin/eth-signer-mcp --version` output
     (version/commit/date/Go).
  3. `make test` — record pass counts (parity suite and offline-import
     test explicitly).
  4. Walk the stdio demo from `docs/demo.md` verbatim (fallback client
     acceptable); confirm the signed RLP byte-matches the golden vector
     and recovers to the fixture address.
  5. Run `scripts/demo/http-demo.sh`; confirm it exits 0; re-run the
     401/403 one-liners.
  6. Record the full transcript in the PR description. **Any failure is
     a v1 blocker:** fix forward on `main` and re-tag (replace the tag
     only if it has not been announced); do not record a red smoke as
     done.

**Acceptance Criteria:**

- [x] `CHANGELOG.md` exists in Keep-a-Changelog format; the `v1.0.0`
      entry's `Added`/`Security` content maps to shipped, tested
      behaviour only.
- [x] `docs/release-notes-v1.0.0.md` exists with scope, pins + protocol
      revision, measured benchmark numbers, the honest latency
      statement, lifecycle contract, threat-model pointer, and P2
      backlog; its only advisory content is the 4.3-verified
      not-affected fact plus the govulncheck-in-CI statement.
- [x] Every pre-tag checklist item passed and is recorded in the PR
      description; CI is green at the exact tagged SHA.
- [x] Tag `eth-signer-mcp/v1.0.0` exists at that commit and is pushed;
      `git describe --tags` confirms.
- [x] Post-release smoke from a fresh clone of the tag: build + test
      green; **both demos** reproduced (stdio RLP byte-matches the
      golden vector; HTTP script exits 0; 401/403 reproduce); transcript
      recorded.
- [x] Final repo tidy done: clean tree, no scratch artifacts, docs
      mutually consistent — the phase's polish duty fully discharged.

**Testing Notes:**

- The smoke is deliberately performed from the tag in a fresh clone, not
  the working tree — it is the "new operator's first afternoon" check
  and the only step that validates the *published* state end-to-end.
- If the smoke surfaces a docs-vs-behaviour mismatch, that is a
  documentation bug: fix the doc, land it, re-tag per the blocker rule
  above. Behaviour changes at this point are out of scope for v1 and
  would reopen 4.4.
