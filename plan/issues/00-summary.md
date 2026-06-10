# eth-signer-mcp — Cross-Phase Issue Roll-Up

This is the engineering-lead roll-up across the four phases of
`apps/eth-signer-mcp`: a single Go binary exposing an **offline Ethereum
transaction signer as an MCP server** over two transports — stdio and MCP
**Streamable HTTP** — built by a single developer working a single sequential
stream. The four phases (Foundations → Signing core → HTTP transport →
Release) are broken into 36 independently shippable issues across
[`01-phase-1.md`](./01-phase-1.md) through [`04-phase-4.md`](./04-phase-4.md);
every number below is computed directly from those files' issue tables.

A planning principle shapes every phase: **there is no separate polish
phase.** The old "P1 polish" phase is dissolved — each former polish item
(JSON slog from day one, `get_address` + `hash`/`from` output fields, the
fsperm `--strict-perms` wiring, HTTP request logging) lands in the phase
where the relevant code is first written, complete, once. Instead, **every
phase ends with an explicit in-phase polish pass** (1.10, 2.12, 3.9; in
Phase 4 the final sweep 4.4 plays that role), so the code stays clean
continuously rather than accruing a cleanup backlog.

## Estimation Approach

- **Story points, Fibonacci-capped at 3.** Every issue is sized 1, 2, or 3
  points — never more. Anything larger was split into properly sized issues
  with their own `N.M` IDs (no `a`/`b`/`c` suffixes anywhere).
- **~0.5 day per point** is the working heuristic where it applies; spike,
  vault, and test-matrix issues carry day estimates set by their scope notes
  rather than the multiplier (e.g. 2.2 is 3 points / 2.5 days). Per-phase
  task days sum exactly to the phase budget.
- **Single implementer, single stream.** One code-writer takes phases and
  issues in order; there are no parallel tracks to coordinate.
- **Definition of done per issue:** `make lint`, `make test`, `make build`
  green; the issue's acceptance criteria all checked; **CI green** — and CI
  is real from issue 1.2 onward (lint incl. depguard, test, build,
  govulncheck, `GOOS=windows` compile), so every later "in CI" claim refers
  to an existing workflow, not an assumption.
- **Risk-aware sizing.** The top schedule risk — MCP SDK API drift — is
  de-risked by a dedicated **Phase 1 spike (issue 1.7)** whose findings
  Phases 2 and 3 consume, not by padding every estimate.
- **Testable phase exit gates.** Each phase ends on a verifiable gate
  (Phase 2's byte-identical parity vs `cast`/ethers v6 is the archetype), so
  progress is measured by passing gates, not issue counts.

## Phase Table

Computed from the issue tables in the four phase files:

| Phase | File | Focus | Issues | Points | Task days |
|-------|------|-------|--------|--------|-----------|
| 1 | [`01-phase-1.md`](./01-phase-1.md) | Foundations: scaffold, CI, CLI/config, obs, secret hygiene, fsperm, stdio boot, SDK spike | 10 | 14 | 7 |
| 2 | [`02-phase-2.md`](./02-phase-2.md) | Signing core: vault, tx validate/build, signer, both tools, golden-vector parity | 12 | 25 | 14 |
| 3 | [`03-phase-3.md`](./03-phase-3.md) | HTTP transport: Streamable HTTP, bearer auth, hardening matrix, resource bounds, shutdown | 9 | 17 | 9 |
| 4 | [`04-phase-4.md`](./04-phase-4.md) | Release: demos, operator README, pin verification, final sweep, v1.0.0 tag | 5 | 10 | 5 |
| **Total** | | | **36** | **66** | **35** |

The project plan carries a **~3-day contingency float** on top of the
35 task days — **~38 working days** total. The float is drawn down
explicitly as needed; it is not padded into individual issues.

## Single-Stream Execution Plan

Phase order with the plan's nominal cumulative day windows:

| Order | Phase | Day window | What ships at exit |
|-------|-------|------------|--------------------|
| 1 | Foundations | Days 1–7 | Runnable binary: full CLI flag set, JSON logs on stderr, rich `--version`, fsperm warn/`--strict-perms` refuse, MCP `initialize` + empty `tools/list` over stdio; CI workflow live; SDK spike note committed; depguard + offline-import scaffold in place |
| 2 | Signing core | Days 8–21 | Complete offline signing path over stdio: `sign_transaction` + `get_address` (full output shape incl. `hash`/`from`), six-code error taxonomy on the wire, decrypt-sign-zero with semaphore + panic-safe zeroing, audit line, byte-identical RLP parity vs `cast`/ethers v6 on 11 golden vectors, stdio e2e |
| 3 | HTTP transport | Days 22–30 | Same tool surface over Streamable HTTP on `127.0.0.1`: bearer 401, SDK rebind 403, 1 MiB body cap, pinned pipeline order, required concurrent-calls test, request logging + `request_id` correlation, graceful shutdown, stdio/HTTP parity test |
| 4 | Release | Days 31–35 | Both demos (unmodified MCP client + HTTP script), operator README, pins + advisories verified mechanically, acceptance benchmark, mutation re-checks + end-to-end leak audit, CHANGELOG/release notes, `eth-signer-mcp/v1.0.0` tag + fresh-clone smoke |
| — | Contingency | ~3 days | Absorbed as needed → ~38 working days |

## Dependency Map

Within each phase the issue tables carry full intra-phase ordering; the
chains that **cross phase boundaries** are:

```text
1.1 (scaffold + pins) ──▶ everything

1.2 (CI workflow) ──▶ 2.8 (gates in CI) ──▶ 3.6 (CI gate, never skipped)
                                        └─▶ 4.3 (govulncheck evidence) ──▶ 4.5 (CI green at tag)

1.3 (CLI flags incl. HTTP flags) ──▶ 3.1 (cmd routes --http)
1.6 (fsperm machinery) ──────────▶ 3.2 (token-file perm check) ──▶ 4.4 (refusal-line scan)

1.5 (Secret/Zero/Sentinel + encoded forms)
  ├─▶ 1.9 (depguard/offline scaffold binds to signing)
  ├─▶ 2.1 (fixture key registered as sentinel) ─▶ 2.2/2.6/2.11 leak scans
  ├─▶ 3.2 (token hash in Secret) / 3.3 (HTTP-log leak scan) / 3.6 (burst scan)
  └─▶ 4.4 (end-to-end leak audit, all encoded forms)

1.7 (SDK spike note) ──▶ 1.8 (in-memory transport pattern)
  ├─▶ 2.3 (jsonschema tag surface) / 2.7 (request-id decision, registration shape)
  └─▶ 3.1 (StreamableHTTPOptions) / 3.3 (request-id source) / 3.5 (pipeline-order pins)

1.8 (stdio shell) ──▶ 2.7 (tools register on this server) ──▶ 2.11 ──▶ 3.8

1.9 (offline-import scaffold, vacuous) ──▶ 2.8 (load-bearing + mutation) ──▶ 4.4 (mutation re-check)

2.1 (fixtures) ──▶ 2.2 (vault) ──▶ 2.6 (signer) ──▶ 2.7 ──▶ 2.11
            └────▶ 2.9 (golden vectors) ──▶ 2.10 (parity suite)
2.2 (decrypt semaphore + ctx-before-KDF) ──▶ 3.4 (semaphore under HTTP) ──▶ 3.6 / 3.7
2.6 (WithRequestID helpers, audit line) ──▶ 3.3 (correlation test)
2.9 (golden vectors) ──▶ 2.10 / 2.11 / 3.8 / 4.1 (see fan-out below)
2.6 (bench groundwork) ──▶ 4.3 (acceptance benchmark, both scrypt sets)

3.5 (hardened pipeline) + 3.7 (shutdown) ──▶ 3.8 (HTTP e2e + parity) ──▶ 4.1 (demos)
3.8 (e2e harness) ──▶ 4.4 (leak-audit reuses it)
4.1 ──▶ 4.2 ──▶ 4.5;  4.3 ──▶ 4.4 ──▶ 4.5 (tag)
```

Every issue ID referenced in any phase file's Dependencies/Blocked-by field
was spot-checked against the issue tables; **no dangling references exist**.

## Notable Fan-Out Points

Issues whose outputs are consumed far beyond their own phase — slips here
multiply:

- **1.5 — shared leak-scan helper with encoded forms.** The `Sentinel`
  derives raw + lowercase/uppercase hex + base64 + decimal-scalar forms, and
  new secret types must register theirs. Consumed by: `obs`'s log tests
  (1.5), the fixture key (2.1), vault/signer/e2e scans (2.2/2.6/2.11),
  bearer-token scans (3.2/3.3), the concurrent burst (3.6), and the Phase 4
  end-to-end leak audit (4.4).
- **1.9 → 2.8 → 4.4 — the offline-import chain.** Vacuous scaffold in
  Phase 1, made load-bearing with a deliberate-violation mutation in 2.8,
  re-checked by the same mutation in the 4.4 final sweep.
- **1.2 — CI.** Every "enforced on every commit" claim in all four phase
  files points at this one workflow; 3.6's never-skipped gate and 4.5's
  green-at-the-tag requirement both ride it.
- **2.9 — golden vectors.** The dual-oracle (cast + ethers v6) vector set is
  consumed by the parity suite (2.10), the stdio e2e (2.11), the HTTP e2e +
  transport-parity test (3.8), and the release demos (4.1).
- **1.7 — SDK spike note.** Beyond unblocking 1.8, it is cited by 2.3/2.7
  (tag surface, request-id decision), 3.1/3.3 (option names, id source), and
  3.5 — whose pipeline-order regression assertions carry failure messages
  pointing at the note, so an SDK bump that changes observed behavior fails
  with the right diagnostic.

## Risk Flags

Aggregated across the phase files, highest-leverage first:

1. **MCP SDK v1.6.x API surface** (typed-tool registration,
   StreamableHTTPOptions, middleware hooks). *Mitigated:* the Phase 1 spike
   (1.7) validates the integration surface before any signing code depends
   on it, and v1.6.1 is pinned for both the server and every test client.
2. **Parity edge cases** (EIP-155 `v` vs yParity, empty `data` → `0x80`,
   contract creation, padded/leading-zero nonce). This is the PRD's #1
   success metric. *Mitigated:* per-edge-case golden fixtures (2.9) verified
   by **two independent oracles** (`cast` + ethers v6) that must agree
   byte-for-byte, plus the byte-identical suite (2.10) and rejection vectors.
3. **Secret leakage via encoded log forms** — a hex/base64/decimal rendering
   would evade a raw-bytes scan. *Mitigated:* the encoded-forms sentinel scan
   from 1.5 runs in `signing`, `obs`, both e2e suites, the concurrent burst,
   and the 4.4 audit; new secret types must register their forms.
4. **Key zeroing on panic.** Best-effort per ADR-009 — Go may retain
   transient copies (GC moves, stack copies); stated honestly, never
   over-claimed. The panic-path zeroing test lands in 2.2/2.6 and the
   observable requirement (no secrets in any output) is test-enforced.
5. **Resource exhaustion** (each standard-scrypt decrypt ≈ 256 MiB).
   *Mitigated:* decrypt semaphore of 1 + ctx-before-KDF (2.2), 1 MiB
   `MaxBytesHandler` + 256 KiB data cap (3.4), and the **REQUIRED,
   never-waived concurrent-calls integration test (3.6)** proving the bound.
6. **Operator mis-binds HTTP off localhost.** *Mitigated:* three layers per
   ADR-006, each pinned by the 3.5 matrix — loopback default bind (asserted
   on the resolved address), bearer 401 before the SDK handler, SDK
   DNS-rebinding 403 — plus a pipeline-order regression test and
   off-localhost documented as unsupported.
7. **urfave/cli v3 idiom drift** between patch releases. *Mitigated:* exact
   patch pin (1.1), locked v3 patterns recorded in 1.3, and
   `--help`/`--version`/bad-flag smoke tests in CI from Phase 1.
8. **Depguard's honest limits.** It enforces **package-level import edges
   only** — it cannot see symbols, so interface-vs-concrete discipline is
   code-review enforced. Stated plainly in 1.9 and the `.golangci.yml`
   comments; a deliberate-violation test proves the globs actually fire.
9. **Scrypt latency surprises operators.** ~0.5–1 s per call on
   standard-scrypt keystores (geth default, N=262144), ~50 ms light scrypt —
   paid on **every** call by design (no decrypted-key cache, ADR-010), no
   warm path. Documented for operators in the 4.2 README and `--help`; the
   4.3 benchmark asserts the *non-KDF overhead* (< 10 ms) so the cost stays
   attributable.
10. **Foundry/ethers regeneration drift.** *Mitigated:* `.foundry-version`
    pin + committed `cast --version` capture + dual-oracle byte-compare;
    **CI never invokes Foundry or Node** — vectors are committed, regen is a
    developer-only script.

**Not a risk (deliberately):** there is no open go-ethereum advisory item.
v1.17.3 is **not affected** by GO-2026-4314/-4315/-4507/-4508/-4511 (all
fixed at or before v1.17.0; 4511 is an ECIES key-validation flaw, not a
DoS). Future advisories are tracked mechanically by `govulncheck` in CI
(1.2) and verified once more at release (4.3); no manual advisory claims
appear anywhere in the shipped docs.

## Totals

- **36 issues** across 4 phases (10 + 12 + 9 + 5).
- **66 story points** (14 + 25 + 17 + 10), every issue ≤ 3 points.
- **35 task days** (7 + 14 + 9 + 5), summing exactly to the phase budgets.
- **~3 days contingency float** held at the project level → **~38 working
  days**, single developer, single stream.
- Four polish passes (1.10, 2.12, 3.9, 4.4) — one per phase, by principle.
- Ends at the monorepo-prefixed tag **`eth-signer-mcp/v1.0.0`** with CI
  green at the tagged commit and a fresh-clone post-release smoke.
