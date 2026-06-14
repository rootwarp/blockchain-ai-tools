# eth-tx-builder ERC-20 — Cross-Phase Issue Summary

A new `build_erc20.py` sibling helper layered onto the existing
`.claude/skills/eth-tx-builder/` skill, adding the three ERC-20 movement ops
(`transfer`, `approve`, `transfer-from`) plus iterative follow-ups. The helper
imports the v1 `build_send_eth.py` as `_core` for shared plumbing and produces a
ready-to-sign EIP-1559 `TxRequest` JSON for `eth-signer-mcp`. Three phases,
mapped to the PRD priority tiers (P0 / P1 / P2). Phase 1 is the only
release-gating deliverable; Phases 2 and 3 are independently shippable
follow-ups and neither gates the other.

- **Total issues:** 30
- **Total points:** 55
- **Phases:** 3 (`P0` must-ship, `P1` iterate, `P2` opportunistic)

Phase files:

- [Phase 1 — P0: core ERC-20 builder, tests, docs, hoodi e2e (MUST SHIP)](01-phase-1.md)
- [Phase 2 — P1: balanceOf pre-check, approve-race guard, sepolia/holesky, `--summary-only`](02-phase-2.md)
- [Phase 3 — P2: `--revoke`, polished `bytes32` decode, optional `permit`](03-phase-3.md)

## Breakdown Quality

An adversarial sizing/dependency pass was run over the previous breakdown and
its findings have been applied across all three phase files. The summary below
reflects the revised set; the prior versions of this file should not be relied
on.

Findings applied:

- **Issue 1.10 split into 1.10a (docs prose) + 1.10b (hoodi live-network e2e).**
  Bundling the SKILL.md / README.md prose work with a network-dependent
  multi-op broadcast confused the unit of work and made the original 1.10
  unschedulable without network access. The split lets prose review proceed
  offline; 1.10b owns only the live runs.
- **Issue 2.8 split into 2.8a (docs), 2.8b (cross-feature regression matrix),
  2.8c (hoodi e2e re-run + Phase-1 byte-identical fixture diff + commit on
  `develop`).** Same shape as the 1.10 split — separate the SKILL.md/README
  docs, the table-driven regression matrix, and the live-network e2e + commit
  so they can be reviewed and re-run independently.
- **Permit 5-pt issue (formerly 3.7) deferred to its own PRD per project plan
  DL-10.** That issue violated the project's ≤3-pt rubric and had a
  non-testable AC (it depended on a PRD that did not exist). Phase 3 now
  contains Issue 3.6 (the spike that produces the full draft
  `eth-tx-builder-erc20-permit` PRD skeleton + resolves the previously
  undeclared `eth-signer-mcp` EIP-712 external blocker) and Issue 3.7 (a 1-pt
  deferred-placeholder/tracking issue recording the deferral decision and
  pointing at the fresh PRD). Any actual `permit` implementation ships under
  its own PRD + phase plan.
- **Missing dependency edges fixed.** Issue 1.1 now creates the
  `test_build_erc20.py` shell (including `make_fake_rpc`) so 1.2 / 1.3 / 1.6
  do not transitively depend on 1.2 just to get a file to add a `TestCase`
  into. Issue 1.7 explicitly pins `Blocked by: 1.3` (it directly consumes
  `human_to_base_units` and `MAX_UINT256`). Issue 2.7 (`--summary-only`) was
  made independent — its prior cross-feature ACs that hid 2.7 → 2.2 / 2.7 →
  2.4 dependencies were relocated to 2.8b's regression matrix. Issue 3.2 now
  explicitly calls out the additive Phase-1 `op_label` touchpoint to
  `summary_ctx` (Sub-step 0) as the first sub-step rather than a hidden side
  edit; the change is backward-compatible and Phase-1 tests stay green.

## Estimation Approach

- **Unit:** relative story points on a Fibonacci-ish scale (1 / 2 / 3 / 5),
  where 1 ≈ a focused ~1-day change or paper spike, 2 ≈ a 1–2-day feature with
  its own test class, 3 ≈ a ~2-day change touching multiple sections or
  carrying cross-feature integration. Points are complexity/risk estimates, not
  a literal day count.
- **Sizing rule (now uniformly enforced):** **every issue is ≤3 points.** The
  previously-5pt permit implementation slot has been removed; only Issue 3.6's
  paper-only spike (2 pts) and Issue 3.7's deferral placeholder (1 pt) remain.
  Any future `permit` implementation ships under its own PRD + phase plan.
- **Target scope:** every issue is completable in 1–2 days. Issues estimated at
  3 pts (Phase 1: 1.2 abi_codec, 1.7 tx_assembly, 1.8 cli_dispatch) are
  justified by 2-day internal sub-structure documented inside each issue and
  do not exceed 2 days of work for a single code-writer.
- **Execution model:** **single-stream, single code-writer by default.** No
  Stream A/B partitioning; the per-phase execution plans are one ordered day
  table each, and the cross-phase plan below is one continuous ordering across
  all phases.
- **Inputs:** estimates are derived bottom-up from the approved PRD
  (`../prd.md`), architecture + ADRs (`../architecture.md`), project plan
  (`../project-plan.md`), and the research set (`../research/`). Every issue
  cites the upstream decision it implements; no estimate introduces a new
  design choice.
- **Day-count vs. points:** each phase's execution table expresses calendar
  days for a single code-writer; points are the independent sizing signal. The
  two are intentionally close but not identical (docs-heavy, spike, and e2e
  issues skew the day count above their point value).
- **Spikes are pointed.** Phase 3's three paper spikes (3.1 `--revoke` design,
  3.3 `bytes32` catalog, 3.6 `permit` go/no-go + draft-PRD-skeleton +
  EIP-712-signer-blocker resolution) are sized like any other issue because
  they gate code (or, in 3.6's case, produce a reviewable ADR + a draft PRD
  artifact).

## Phase Table

| Phase | Theme | Priority | Issues | Points | Release-gating? |
|-------|-------|----------|--------|--------|-----------------|
| [Phase 1](01-phase-1.md) | Core ERC-20 builder (`transfer`/`approve`/`transfer-from`), tests, docs, hoodi e2e | P0 | 12 | 23 | **Yes — only must-ship phase** |
| [Phase 2](02-phase-2.md) | `balanceOf` pre-check, approve-race guard, sepolia/holesky, `--summary-only`, docs/regression/e2e sweep | P1 | 10 | 20 | No — independently shippable |
| [Phase 3](03-phase-3.md) | `--revoke`, polished `bytes32` decode, paper-only `permit` spike + draft PRD + deferral placeholder | P2 | 8 | 12 | No — opportunistic, per-issue ship |
| **Total** | — | — | **30** | **55** | — |

## Execution Plan

Default is single-stream: one code-writer works issues in order. Each day slot
represents ~1 day of focused work. Days are 1-indexed per phase; the
cross-phase ordering assumes Phase 1 ships first, then Phase 2 and Phase 3 are
scheduled by demand (neither blocks the other).

### Phase 1 — single-stream order (12 issues / 23 pts; ~16 days)

| Day | Issue | Notes |
|-----|-------|-------|
| 1   | 1.1 Skeleton — `build_erc20.py` + `test_build_erc20.py` shells (1pt) | Creates both source AND test-file shells (incl. `make_fake_rpc`) so 1.2/1.3/1.6 just add `TestCase` classes. |
| 2   | 1.2 abi_codec — selectors + encoders (3pt, day 1 of 2) | Blocked by 1.1 only. |
| 3   | 1.2 cont. — decoders + `TestAbiCodec` golden vectors | |
| 4   | 1.3 amount_codec + `TestAmountCodec` no-`float` invariant (2pt) | Blocked by 1.1. |
| 5   | 1.4 contract_reads + `TestContractReads` (2pt) | Blocked by 1.1, 1.2. |
| 6   | 1.5 gas_estimator (no-fallback) + `TestGasEstimator` (2pt) | Blocked by 1.1, 1.4. |
| 7   | 1.6 summary + `TestSummary` (2pt) | Blocked by 1.1, 1.3. |
| 8   | 1.7 tx_assembly `do_*` composers (3pt, day 1 of 2) | Blocked by 1.3, 1.4, 1.5, 1.6 (1.3 pin made explicit — direct consumption of `human_to_base_units` + `MAX_UINT256`). |
| 9   | 1.7 cont. — `TestTxAssembly` cross-section integration | |
| 10  | 1.8 cli_dispatch argparse + dispatcher (3pt, day 1 of 2) | Blocked by 1.1, 1.7. |
| 11  | 1.8 cont. — `TestCliDispatch` end-to-end CLI tests | |
| 12  | 1.9 Full test-suite green (v1 regression + new) (1pt) | Blocked by 1.2–1.8. |
| 13  | 1.10a SKILL.md + README.md prose (1pt) | Blocked by 1.8. Pure prose, no live network. |
| 14  | 1.10b Hoodi e2e — pre-flight + `transfer` + `approve --amount` (2pt, day 1 of 2) | Blocked by 1.9 + 1.10a. Wait for `approve` to mine before next day. |
| 15  | 1.10b cont. — `transfer-from` e2e + record three transcripts in README | |
| 16  | 1.11 Commit on `develop` (1pt) | Blocked by 1.9, 1.10a, 1.10b. |

### Phase 2 — single-stream order (10 issues / 20 pts; ~12 days)

| Day | Issue | Notes |
|-----|-------|-------|
| 1   | 2.1 balanceOf read primitives (2pt, day 1 of 2) | No intra-Phase-2 blocker. |
| 2   | 2.1 cont. — `TestAbiCodec` + `TestContractReads` extensions | |
| 3   | 2.2 balanceOf soft-check in `do_transfer` + warning + tests (2pt) | Blocked by 2.1. |
| 4   | 2.3 Extract allowance soft-check helper (parameterized `trigger`) (2pt) | No intra-Phase-2 blocker. Helper API pinned here so 2.4 consumes it unmodified. |
| 5   | 2.4 approve-race guard in `do_approve` + warnings + tests (2pt, day 1 of 2) | Blocked by 2.3. Consumes 2.3's helper byte-identically. |
| 6   | 2.4 cont. | |
| 7   | 2.5 Add sepolia + holesky to v1 NETWORKS + v1 tests (2pt) | The only Phase 2 issue that edits v1 files. Diff-hygiene gated. |
| 8   | 2.6 Verify ERC-20 helper picks up new networks (test-only) (1pt) | Blocked by 2.5. |
| 9   | 2.7 `--summary-only` flag + tests (2pt) | **Independent (Blocked by: none).** Cross-feature interactions with `low_balance` / `approve_race` are deferred to 2.8b's regression matrix. |
| 10  | 2.8a Docs sweep (SKILL.md + README.md) (2pt, day 1 of 2) | Blocked by 2.2, 2.4, 2.6, 2.7. |
| 11  | 2.8a cont. + 2.8b cross-feature regression matrix (1pt) | 2.8b blocked by 2.2, 2.4, 2.6, 2.7. |
| 12  | 2.8c Hoodi e2e re-run + Phase-1 byte-identical fixture diff + commit on `develop` (1pt) | Blocked by 2.2, 2.4, 2.6, 2.7, 2.8a, 2.8b. |

### Phase 3 — single-stream order (8 issues / 12 pts; ~10 days, opportunistic)

| Day | Issue | Notes |
|-----|-------|-------|
| 1   | 3.1 `--revoke` design spike (1pt) | Paper only — new ADR-012. |
| 2   | 3.2 Implement `approve --revoke` (2pt, day 1 of 2) | Blocked by 3.1. **Sub-step 0:** additive `op_label` Phase-1 touchpoint to `summary_ctx` across `do_transfer` / `do_transfer_from` / `do_approve`; backward-compatible; Phase-1 tests pinned green before adding `--revoke` code. |
| 3   | 3.2 cont. | |
| 4   | 3.3 `bytes32` polish catalog spike (1pt) | Paper only — new ADR-013. |
| 5   | 3.4 Polished `bytes32` `decode_symbol` (2pt, day 1 of 2) | Blocked by 3.3. |
| 6   | 3.4 cont. | |
| 7   | 3.5 SKILL.md + README docs for whichever of 3.2 / 3.4 shipped (2pt) | **ANY-OF blocker** on {3.2, 3.4}. Covers only what shipped. |
| 8   | 3.6 `permit` go/no-go + EIP-712 signer external-blocker resolution (2pt, day 1 of 2) | Paper only — new ADR-014 + full draft `eth-tx-builder-erc20-permit` PRD skeleton. Independent of 3.1–3.5. |
| 9   | 3.6 cont. (full draft PRD skeleton) **+** 3.7 deferred placeholder (1pt; <0.5 day folded into Day 9) | 3.7 blocked by 3.6. **No `permit` code in this phase.** |
| 10  | 3.8 Phase 3 manual e2e + commit on `develop` (1pt) | Blocked by every Phase 3 feature that landed (3.2 and/or 3.4) + 3.5 (docs). 3.6 / 3.7 are paper-only — no e2e. |

## Dependency Map

Edges below are derived from the revised phase files (not the prior summary).
"`A → B`" means "A blocks B." Within a phase, only intra-phase edges are
shown; Phase 1 exit is the universal entry criterion for Phases 2 and 3.

### Phase 1 (12 issues)

```text
1.1 ──┬──▶ 1.2 ──┬──▶ 1.4 ──▶ 1.5 ──┐
      │          │                  │
      ├──▶ 1.3 ──┼──────────────────┼──▶ 1.7 ──▶ 1.8 ──▶ 1.9 ──┬──▶ 1.10b ──▶ 1.11
      │          │                  │                          │
      ├──▶ 1.4   ├──▶ 1.6 ──────────┘                          │
      ├──▶ 1.5                                                 │
      ├──▶ 1.6                                                 │
      └──▶ 1.8                                       1.8 ──▶ 1.10a ──┘
                                                              │
                                                              └──▶ 1.10b ──▶ 1.11
```

Key Phase 1 edges (explicit blockers on each issue):

- `1.1 → 1.2, 1.3, 1.4, 1.5, 1.6, 1.8` (skeleton creates BOTH source + test-file
  shells; no other issue needs to create the test file).
- `1.2 → 1.4, 1.7` (consumes `encode_*_call`, `decode_*`).
- `1.3 → 1.6, 1.7` (explicit: `do_*` directly call `human_to_base_units` and
  reference `MAX_UINT256` in the `approve_max=True` branch).
- `1.4 → 1.5, 1.7`.
- `1.5 → 1.7`.
- `1.6 → 1.7`.
- `1.7 → 1.8`.
- `1.2–1.8 → 1.9`.
- `1.8 → 1.10a` (docs describe the final CLI surface).
- `1.9 → 1.10b, 1.11`.
- `1.10a → 1.10b, 1.11` (1.10a lands the e2e section skeleton 1.10b fills in).
- `1.10b → 1.11`.

### Phase 2 (10 issues)

```text
2.1 ──▶ 2.2 ──┐
              │
2.3 ──▶ 2.4 ──┼──▶ 2.8a ──┐
              │            │
2.5 ──▶ 2.6 ──┤            ├──▶ 2.8c
              │            │
2.7 ──────────┴──▶ 2.8b ───┘
```

Key Phase 2 edges:

- `2.1 → 2.2`.
- `2.3 → 2.4` (2.3's helper API is pinned; 2.4 consumes it unmodified).
- `2.5 → 2.6`.
- `2.7` is **independent** — `Blocked by: none`. Its cross-feature interactions
  with `low_balance` (2.2) and `approve_race` (2.4) were relocated to 2.8b's
  regression matrix so 2.7 stays unblocked.
- `2.2, 2.4, 2.6, 2.7 → 2.8a` (docs cover the implemented surfaces).
- `2.2, 2.4, 2.6, 2.7 → 2.8b` (regression matrix asserts the implemented
  surfaces).
- `2.2, 2.4, 2.6, 2.7, 2.8a, 2.8b → 2.8c` (e2e + commit closes Phase 2).

### Phase 3 (8 issues)

```text
3.1 ──▶ 3.2 ──┐                  ┌──▶ 3.5 ──┐
              ├──▶ {ANY-OF 3.2,3.4} ───────┤
3.3 ──▶ 3.4 ──┘                  └──▶ 3.5 ──┴──▶ 3.8

3.6 ──▶ 3.7  (paper only; 3.6 also independent of 3.1–3.5)
```

Key Phase 3 edges:

- `3.1 → 3.2` (spike before implementation).
- `3.3 → 3.4` (spike before implementation).
- `3.5` blocker is **ANY-OF {3.2, 3.4}** — runs as soon as at least one
  shipped, covers only what shipped. If neither shipped, 3.5 does not fire.
- `3.6 → 3.7` (3.7 records the deferral decision the 3.6 spike produced; 3.7
  ships no code).
- `3.2 and/or 3.4 (whichever shipped) + 3.5 → 3.8`. **3.6 / 3.7 are paper-only
  and do not feed 3.8's e2e.**

### Cross-phase notes

- Phase 1 must exit (success metrics §1–§5) before either Phase 2 or Phase 3
  starts. Phases 2 and 3 are independent of each other (project plan DL-9 /
  DL-10).
- Phase 2's `2.5` is the only Phase-2 issue that edits v1 files
  (`build_send_eth.py`, `test_build_send_eth.py`) — explicitly relaxing the
  Phase 1 P0 freeze per architecture Assumption 16.
- Phase 3's `3.2` Sub-step 0 (the `op_label` Phase-1 touchpoint) is additive
  and backward-compatible; Phase-1 tests stay green throughout the refactor.

## Risk Flags

Runtime / process risk register, re-derived from the current phase files. Risk
IDs (R1–R13) are stable across this document so cross-references in the phase
files keep resolving.

| ID | Risk | Phase | Likelihood | Impact | Mitigation (recorded in phase files) |
|----|------|-------|------------|--------|--------------------------------------|
| R1 | **Hoodi token availability** — no standard-surface ERC-20 reachable on hoodi at e2e time blocks PRD success metric §1 + §2. | 1 (1.10b), echoed in 2.8c, 3.8 | Low | High (release-gate for Phase 1) | Pre-flight check (c) in 1.10b verifies a standard-surface token with sane `decimals()` / `symbol()` and a non-zero balance for the test wallet **before** any broadcast. If unavailable, defer 1.10b — do not fall back to mainnet. |
| R2 | **`eth_estimateGas` underestimates real-tx gas** — buffered estimate + 300k cap clears the on-chain reality but tokens with hostile branches can revert anyway. | 1 (1.5), 1.10b | Low–Med | Med | No-fallback policy in `gas_estimator` (ADR-007) surfaces estimate failures as exit-1; hoodi e2e is the empirical check. `TestGasEstimator.assertRaises(_core.RPCError)` is the structural regression. |
| R3 | **Golden-vector verification drift** — the USDC mainnet calldata vectors used to gate `abi_codec` could be wrong, masking a real encoding bug. | 1 (1.2) | Low | High | Hand-verify against Etherscan input-data decode (1.2 Implementation Notes); do NOT commit a scratch `eth-abi` round-trip helper. |
| R4 | **publicnode rate-limit / transport flake** — repeated `eth_call` / `eth_estimateGas` from a long e2e session can be soft-rate-limited. | 1 (1.10b), 2 (2.8c), 3 (3.8) | Low | Low–Med | Phase 1 Risk R4 documented; rerun on transport error. The hoodi e2e is the only network-touching step in Phase 1; Phase 2/3 e2e are similarly bounded. |
| R5 | **v1 symbol rename breaks `_core` contract** — `build_erc20.py`'s top-of-file docstring lists the 10 imported symbols; a future v1 rename would explode at test load time. | All | Low | High (silent ImportError at test time) | The docstring contract is documented (1.1, ADR-001); `_core.RPCError` and friends are referenced directly so an `AttributeError` surfaces immediately on rename. |
| R6 | **Holesky deprecation** — publicnode may retire the holesky endpoint before / during Phase 2. | 2 (2.5, 2.8a) | Med | Low (dict-only edit) | 2.5 Implementation Notes: if endpoint is retired before 2.5 ships, drop the entry; SKILL.md / README docs flag holesky as scheduled for deprecation regardless and prefer hoodi. |
| R7 | **approve-race warning noise** — every USDT-style approval trips the `approve_race` warning, causing alert fatigue. | 2 (2.4) | Med | Low–Med | Warning is data-tuple based (ADR-004); wording / suppression decisions are localizable in `summary.warn_approve_race`. Project plan R7 lists opt-out path. |
| R8 | **Freeze-relaxation diff hygiene** — 2.5 edits the v1 file; a careless edit (whitespace, re-format, comment shuffle) breaks the P0 freeze invariant. | 2 (2.5) | Med | High (v1 byte-for-byte freeze is a load-bearing AC across the project) | `git diff build_send_eth.py` must show exactly two added lines after 2.5; reviewer-gated. |
| R9 | **`approve --revoke` not universally honoured** — some spenders (router contracts with custom permission systems) ignore `approve(spender, 0)`. | 3 (3.2, 3.5) | Low | Low (operator-facing, recoverable) | SKILL.md note (3.5 conditional AC) explicitly records that `approve(0)` is the standard mechanism but not universally honoured; operators should double-check spender docs. |
| R10 | **`bytes32` decode catalog tail** — chasing every weird-erc20 entry can drift the polish into an infinite tail. | 3 (3.3, 3.4) | Low | Low | ADR-013 (Issue 3.3) bounds the catalog explicitly; anything outside the list still returns `None` (best-effort posture preserved). 3.4 includes an "outside the catalog" regression test. |
| R11 | **`permit` / EIP-712 stdlib fit + `eth-signer-mcp` EIP-712 gap** — keccak256 is not in the Python stdlib (sha3_256 is SHA-3, not keccak); and `eth-signer-mcp`'s README explicitly lists EIP-712 typed-data signing as out-of-scope, which would block any `permit` workflow end-to-end. | 3 (3.6) | High (signer gap is a hard external blocker) | High (would prevent any `permit` ship without parallel signer work) | 3.6's spike explicitly resolves both: (a) keccak256 hand-write feasibility recorded in ADR-014 with a "validate against known vectors" test surface, (b) the `eth-signer-mcp` EIP-712 gap is explicitly named in the ADR with v1 path (external EIP-712 signer) and v2 path (signer enhancement PRD) both recorded. **No `permit` code lands in Phase 3** — implementation is owned by the fresh PRD's own phase plan. |
| R12 | **Develop-branch process drift** — habitual operators may PR to `main` instead of committing on `develop`. | All (1.11, 2.8c, 3.8) | Low | Med (release hygiene) | Per repo memory, `develop` is the integration branch; every Phase commit-and-close issue (1.11, 2.8c, 3.8) explicitly states "no PR or merge to `main` unprompted." |
| R13 | **Network reachability at e2e time** — hoodi or the chosen publicnode endpoint is unreachable when the live-network e2e issues run. | 1 (1.10b), 2 (2.8c), 3 (3.8) | Low–Med | Med (defers the phase exit, not the implementation) | Each e2e issue's Testing Notes record the defer-do-not-fall-back-to-mainnet rule. 3.8 additionally records the `eth-rpc` `call` op A1/A2 fallback so a missing post-state verifier doesn't surprise the operator mid-e2e. |
