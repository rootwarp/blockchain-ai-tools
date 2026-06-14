# Phase 3: P2 — `--revoke`, polished bytes32 decode, optional `permit`

## Phase Overview

- **Goal:** Ship the opportunistic PRD §P2 follow-ups named in project plan
  §Phase 3: an `approve --revoke` shorthand that resolves to `approve(spender,
  0)`, a polished `bytes32` `symbol()` decoder hardened against the historical
  legacy formats (MKR, DGD, and friends), and — only if real operator demand
  surfaces — an EIP-2612 `permit` builder. Phase 3 is **opportunistic**: no
  task is on the critical path; each ships only if a real user request
  justifies it. The phase exists to keep the temptations named-but-bounded
  (PRD §Risks "scope creep") rather than letting every adjacent op (NFT
  mints, swaps, `transferAndCall`) accrete into the ERC-20 helper.
- **Issue count:** 8 issues, ~12 total points.
- **Estimated duration:** ~9 days single-stream if every issue ships;
  individual tasks are independently shippable so the realistic ship list is
  "whichever subset is requested." Per project plan DL-10, `permit` is
  explicitly deferred to its own future PRD — Issue 3.6 is the go/no-go
  spike (which now also produces the full draft `eth-tx-builder-erc20-permit`
  PRD skeleton and resolves the `eth-signer-mcp` EIP-712 external-blocker
  question), and Issue 3.7 is a 1-pt deferred-placeholder/tracking issue
  that records the deferral decision — NOT an implementation slot. Any
  actual `permit` builder ships under its own PRD + phase plan as a sibling
  `build_erc20_permit.py`.
- **Entry criteria:**
  - Phase 1 (P0) is merged on `develop` and the manual hoodi e2e closed
    (project plan §Phase 1 Exit Criteria, DL-6).
  - `python3 -m unittest test_build_erc20 -v` and
    `python3 -m unittest test_build_send_eth -v` both pass on `develop` —
    Phase 3 only ships on top of a green Phase 1 (project plan §Phase 3
    Dependency Graph).
  - Phase 2 is **not** required. Phase 3 is an independent follow-up to
    Phase 1, identical posture to Phase 2 (project plan §Phase 2 / 3 are
    independent follow-ups; neither gates the other; DL-9 / DL-10).
- **Exit criteria (per issue; Phase 3 is not all-or-nothing):**
  - Each issue that ships lands on `develop` with the test suite green,
    SKILL.md / README.md updated for any new flag, summary phrasing, or
    decoder coverage, and at least one worked example added.
  - The Phase 1 hoodi manual e2e (`transfer`, `approve --amount`,
    `transfer-from`) still passes after every Phase 3 merge — Phase 3 is
    additive; no Phase 3 issue alters the Phase 1 happy-path JSON.
  - `python3 build_erc20.py --help` still works on a fresh stdlib-only
    Python 3.8+ install (PRD success metric §3 — Phase 3 introduces no new
    third-party deps).
  - For `--revoke` (Issue 3.2): manual e2e against hoodi runs a
    `approve --revoke` against the Phase 1 e2e token and confirms the
    resulting calldata sets allowance to zero on-chain.
  - For the polished `bytes32` decoder (Issue 3.4): unit-test fixtures
    drawn from the `d-xo/weird-erc20` catalog (MKR, DGD, MANA, KNC where
    applicable) decode to the expected ticker; the fallback still returns
    `None` on unknown malformations (best-effort posture preserved).
  - For `permit` (Issues 3.6 + 3.7): no `permit` code lands in this phase.
    Issue 3.6 produces an ADR (go/no-go), resolves the `eth-signer-mcp`
    EIP-712 external-blocker question, and ships the full draft
    `eth-tx-builder-erc20-permit` PRD skeleton (Overview, Problem, Goals,
    P0/P1/P2 Functional Requirements, Out of Scope — each a real section,
    not a paragraph each). Issue 3.7 is a 1-pt deferred-placeholder issue
    that records the deferral decision and the fresh-PRD pointer in the
    draft PRD (or an ADR). Any actual `permit` implementation ships under
    its own PRD + phase plan as a sibling `build_erc20_permit.py` (project
    plan DL-10) — NOT in this phase.

## Assumptions (recorded; not blocking)

These are inherited from the upstream artifacts and explicitly carried into
Phase 3. None requires escalation; each is the default the PRD, research,
architecture, and project plan already endorse.

- **PA-1 — Phase 3 is a bounded backlog, not a commitment.** Project plan
  DL-10; nothing here is gated for a fixed release date. The phase exists so
  the PRD §P2 list is captured-but-bounded.
- **PA-2 — Single-file skill layout stays for `--revoke` and the
  `bytes32` polish.** Architecture ADR-001 / ADR-002 still bind: any new
  logic for Issues 3.1–3.4 lands inside `build_erc20.py` in the existing
  seven labeled in-file sections, with no new files in the skill directory.
- **PA-3 — `permit` is deferred to its own future PRD.** Project plan
  DL-10 and PRD §P2.2 explicitly defer `permit` (EIP-2612) to its own
  helper (`build_erc20_permit.py`) with its own PRD. In this Phase 3,
  Issue 3.6 is paper-only (ADR + draft PRD skeleton + EIP-712 signer
  external-blocker resolution); Issue 3.7 is a 1-pt deferred-placeholder
  tracking issue that records the deferral decision and points at the
  fresh PRD — no `permit` code lands in `build_erc20.py` and no
  `build_erc20_permit.py` is created in this phase. This keeps ADR-001's
  "single new file, dual-role v1 import" contract intact AND honours
  DL-10's "fresh PRD before fresh phase plan before code" cascade.
- **PA-4 — Stdlib-only.** PRD non-functional requirement. Phase 3 adds no
  third-party deps; the only new imports allowed are `inspect` and
  `unittest.mock` (test-only, already present in `test_build_erc20.py`).
- **PA-5 — `do_*` still returns `(tx_dict, summary_ctx, warnings_list)`.**
  Architecture ADR-004 holds. The `--revoke` shorthand reuses
  `do_approve`; no new `do_*` is introduced for it.
- **PA-6 — Hardcoded selectors stay; no runtime Keccak.** Architecture
  ADR-005 binds Phase 3. `encode_approve` is reused for `--revoke`; no
  new selector is needed.
- **PA-7 — Structural fatal-vs-best-effort split preserved.** Architecture
  ADR-006. The polished `bytes32` decoder (Issue 3.4) still returns
  `Optional[str]`; it never raises. The `--revoke` path still propagates
  `decimals()` / `estimate_gas` failures as fatal.
- **PA-8 — Stdout = JSON only; stderr = summary + warnings + errors.**
  Architecture ADR-009. Phase 3 adds no new stdout content; the `--revoke`
  summary and the polished decoder both speak only on stderr.
- **PA-9 — Address validation stays at the CLI layer.** Architecture
  ADR-010. `--revoke` does NOT introduce a new address arg; it reuses
  `--spender` and `--sender` already validated by
  `cli_dispatch._validate_addresses`.
- **PA-10 — Each issue in this phase is independently shippable.** The
  issues below are sequenced for a single code-writer, but the only hard
  intra-phase dependency is "spike before implementation" for `permit`
  (Issue 3.6 blocks Issue 3.7) and "every shipped feature before docs"
  (Issue 3.5 / 3.8). Issue 3.1 → 3.2 and Issue 3.3 → 3.4 are paired
  spike → implementation sequences but no Phase 3 issue blocks another
  Phase 3 issue *outside* its own pair.
- **PA-11 — Develop is the integration branch.** Per repo memory; every
  Phase 3 commit lands on `develop`, never on `main` without a prompt.

## Phase Summary

| Issue | Title | Points | Type | Blocked by | Scope | Files |
|-------|-------|--------|------|------------|-------|-------|
| 3.1 | `--revoke` design spike — argparse three-way mutex, summary wording, op naming | 1 | spike | — | 0.5-1 day | new ADR appended to `architecture.md`; no code |
| 3.2 | Implement `approve --revoke` shorthand + CLI wiring (incl. Phase 1 `op_label` touchpoint) | 2 | feature | 3.1 | 1-2 days | `.claude/skills/eth-tx-builder/build_erc20.py` (`cli_dispatch` + `summary` + `tx_assembly`; Phase-1 `do_transfer` / `do_transfer_from` / `do_approve` all get `op_label` added to `summary_ctx` — additive, backward-compatible), `test_build_erc20.py` (`TestCliDispatch`, `TestTxAssembly`, `TestSummary`) |
| 3.3 | `bytes32` decode polish — catalog spike against `d-xo/weird-erc20` | 1 | spike | — | 0.5-1 day | new ADR appended to `architecture.md`; no code |
| 3.4 | Implement polished `bytes32` `decode_symbol` fallback | 2 | feature | 3.3 | 1-2 days | `build_erc20.py` (`abi_codec.decode_symbol`), `test_build_erc20.py` (`TestAbiCodec`) |
| 3.5 | SKILL.md + README docs update for whichever of `--revoke` / `bytes32` polish shipped (ANY-OF conditional) | 2 | docs | 3.2 OR 3.4 (ANY-OF: at least one of them must be done; covers only the shipped feature(s)) | 1 day | `.claude/skills/eth-tx-builder/SKILL.md`, `.claude/skills/eth-tx-builder/README.md` |
| 3.6 | `permit` go/no-go spike + EIP-712 signer external-blocker resolution + full draft `eth-tx-builder-erc20-permit` PRD skeleton | 2 | spike | — | 1-2 days | new ADR appended to `architecture.md`; new file `plan/eth-tx-builder-erc20-permit/prd.md` (full draft skeleton: Overview, Problem, Goals, P0/P1/P2, Out of Scope); no code in `build_erc20.py` |
| 3.7 | `permit` deferred placeholder — record deferral decision and fresh-PRD pointer (project plan DL-10) | 1 | chore | 3.6 | <0.5 day | edit to draft PRD or new ADR; **no code**, **no `build_erc20_permit.py` file** |
| 3.8 | Phase 3 manual e2e + commit on develop | 1 | chore | every Phase 3 feature that landed (3.2 and/or 3.4), plus 3.5 (docs); 3.7 has no e2e | 0.5-1 day | manual run + commit on `develop`; e2e output captured in README |

> If a Phase 3 issue does **not** ship in this iteration, drop its row from
> Issues 3.5 (docs) and 3.8 (e2e) — the docs update and manual e2e only
> cover what landed. Issue 3.5's dependency is an explicit **ANY-OF**:
> it can run once at least one of 3.2 / 3.4 has shipped, and it covers
> only the shipped feature(s).
>
> **Every issue in Phase 3 is ≤2 points** per the project-wide ≤3pt rubric.
> The previously-oversized `permit` implementation issue (3.7, formerly
> 5pt) has been removed in favour of (a) Issue 3.6 (2pt) producing the
> full draft `eth-tx-builder-erc20-permit` PRD skeleton + EIP-712 signer
> external-blocker resolution, and (b) Issue 3.7 (1pt) recording the
> deferral decision. Any actual `permit` builder ships under its own
> PRD + phase plan per project plan DL-10 — never as a 5-pt slot here.

## Phase Execution Plan

Single-stream, single code-writer. Day slots assume everything in Phase 3
ships up to and including Issue 3.5 (docs), plus the `permit` paper-only
spike + deferral placeholder (3.6 / 3.7). If a subset is requested,
skip the rows for issues not in scope (the dependency edges still hold for
whatever remains).

| Day | Issue |
|-----|-------|
| 1 | 3.1 `--revoke` design spike |
| 2 | 3.2 Implement `approve --revoke` (incl. `op_label` Phase 1 touchpoint sub-step) |
| 3 | 3.2 cont. |
| 4 | 3.3 `bytes32` polish catalog spike |
| 5 | 3.4 Implement polished `bytes32` `decode_symbol` |
| 6 | 3.4 cont. |
| 7 | 3.5 SKILL.md + README docs update (covers whichever of 3.2 / 3.4 shipped) |
| 8 | 3.6 `permit` go/no-go spike + EIP-712 signer external-blocker resolution |
| 9 | 3.6 cont. (full draft `eth-tx-builder-erc20-permit` PRD skeleton) |
| 9 | 3.7 `permit` deferred placeholder (record decision; <0.5 day, folded into Day 9) |
| 10 | 3.8 Phase 3 manual e2e + commit on develop |

> Issues 3.6 and 3.7 are **paper-only**: no `permit` code lands in this
> Phase 3, no `build_erc20_permit.py` is created. Any actual `permit`
> implementation runs under its own fresh PRD + phase plan per project
> plan DL-10. Issue 3.7 is small enough (<0.5 day) that it shares Day 9
> with the tail of Issue 3.6's PRD-skeleton drafting.

---

## Issues

### Issue 3.1: `--revoke` design spike — argparse three-way mutex, summary wording, op naming

- **Points:** 1
- **Type:** spike
- **Priority:** P0 (within Phase 3 — gates the implementation issue)
- **Blocked by:** none
- **Blocks:** 3.2
- **Scope:** 0.5-1 day

**Description:**

PRD §P2.3 names the `approve --revoke` shorthand as a "trivial to add
later" convenience that emits `approve(spender, 0)`. Three small design
decisions need a paper resolution before code lands so the implementer
doesn't get stuck mid-PR on an argparse shape or summary phrasing:

1. **Argparse mutex shape.** Today `--amount` and `--approve-max` live in
   a `add_mutually_exclusive_group(required=True)` (architecture
   Assumption 13). `--revoke` makes this a **three-way** mutex: at most
   one of `{--amount, --approve-max, --revoke}` may be set, and exactly
   one is required. Decide whether argparse's `add_mutually_exclusive_group`
   plus `required=True` cleanly supports three options (it does — it's
   pairwise mutual exclusion across the whole group), or whether a manual
   post-parse check is clearer.
2. **Summary op naming.** When `--revoke` is set, should the stderr
   summary's "operation" line say `revoke` (clearer intent) or `approve`
   (technically accurate — the calldata IS an `approve` call)? Both have
   merit; the spike picks one and records the rationale.
3. **`--approve-max` + `--revoke` conflict messaging.** Argparse's
   default error for a violated `add_mutually_exclusive_group` is
   `error: argument --revoke: not allowed with argument --approve-max`.
   Acceptable, or do we want a custom helpful message ("use exactly one
   of --amount, --approve-max, --revoke")? Spike picks one.

This is a **paper spike**: produce a short ADR (3-5 paragraphs) appended
to `plan/eth-tx-builder-erc20/architecture.md` recording the decisions
before Issue 3.2's implementation starts. No code lands in this issue.

**Implementation Notes:**

- Files likely affected: `plan/eth-tx-builder-erc20/architecture.md` only.
  Add a new ADR (`ADR-012: --revoke argparse mutex shape + summary op
  naming`).
- Approach:
  - Decide (1): **use the existing `add_mutually_exclusive_group(
    required=True)`** and add `--revoke` as a third entry. Argparse
    supports three or more mutex entries natively; no manual check is
    needed. Records "argparse handles the three-way mutex correctly" with
    a one-line code reference.
  - Decide (2): **summary operation reads "revoke"** when `--revoke` is
    set. Reasoning: the operator chose the revoke shorthand specifically
    to signal intent; the summary should echo that intent. The calldata
    line in the summary still shows the underlying `approve(spender, 0)`
    so technical accuracy is preserved.
  - Decide (3): **accept argparse's default error**. Adding a custom
    helpful message would require a manual check (defeating decision 1).
    A small SKILL.md note suffices for operator-friendly guidance.
  - Recommendation for `do_approve` signature: add a `revoke=False`
    kwarg (mirror of `approve_max=False`). When True, set
    `amount_base = 0` and skip the human-amount path entirely. No
    interaction with `human_to_base_units` so `decimals` is still
    fetched (architecture ADR-006 — the read is fatal, preserving the
    Phase 1 contract; the summary still names the decimals for the
    operator's review).
- Key decisions to record explicitly: (1) argparse three-way mutex with
  `required=True`; (2) summary op label = "revoke"; (3) argparse default
  error message is acceptable; (4) `do_approve` gains `revoke=False`
  kwarg; (5) `decimals()` is still read (Phase 1's fatal-or-skip
  contract is preserved).
- Watch out for: the temptation to skip the `decimals()` read for
  `--revoke` "since we don't need to convert any human amount." Don't —
  the summary still names the token decimals, and skipping the read
  would create a special-case path that diverges from `do_approve`'s
  `approve_max=True` flow (which also skips human conversion but still
  reads `decimals`). Symmetry beats micro-optimization.
- New files to create: none (ADR is appended to `architecture.md`).

**Acceptance Criteria:**
- [x] A new ADR (numbered `ADR-012`, dated, status `Accepted`) is
      appended to `plan/eth-tx-builder-erc20/architecture.md` covering
      the three decisions above with explicit "Status / Context /
      Decision / Alternatives / Consequences" sections matching the
      existing ADR house style.
- [x] The ADR explicitly records the `do_approve` signature change:
      adds `revoke=False` kwarg; when True, `amount_base = 0`;
      `decimals()` is still read (preserves the fatal-or-skip contract).
- [x] The ADR cross-references PRD §P2.3, architecture ADR-005 (no
      runtime Keccak — `encode_approve` is reused, no new selector),
      and architecture Assumption 13 (existing two-way mutex).
- [x] The ADR records the summary op label decision ("revoke" vs.
      "approve") and the rationale.
- [x] No code changes are committed in this issue.

**Testing Notes:**
- This is a paper spike; no automated tests. Reviewer-led check is the
  exit criterion.

---

### Issue 3.2: Implement `approve --revoke` shorthand + CLI wiring

- **Points:** 2
- **Type:** feature
- **Priority:** P1 (within Phase 3)
- **Blocked by:** 3.1
- **Blocks:** 3.5 (docs), 3.8 (e2e)
- **Scope:** 1-2 days

**Description:**

Implement the `--revoke` flag on the `approve` subparser per the design
decided in Issue 3.1's ADR-012. Operator-facing CLI gains a third
mutex entry in the `approve` group:

```
python3 build_erc20.py approve \
  --network <mainnet|hoodi> \
  --token <0x-address> \
  --spender <0x-address> \
  (--amount <human-readable-decimal-string> | --approve-max | --revoke) \
  --sender <0x-address>
```

Internally, `--revoke` resolves to `amount_base = 0`; calldata is
`approve(spender, 0)` using the existing `encode_approve` (no new
selector or encoder is needed — reuses architecture ADR-005). The summary
names the op as "revoke" (per ADR-012 decision 2) rather than "approve"
for clarity, while the calldata line still shows the underlying
`approve(spender, 0)` so technical accuracy is preserved.

> **Phase 1 touchpoint.** The summary refactor that lets the op label
> render "revoke" requires adding an `op_label` field to `summary_ctx`
> across the Phase 1 `do_*` functions (`do_transfer`, `do_transfer_from`,
> `do_approve`). This is a **backward-compatible additive change** — the
> existing summary-render path keeps working; only the previously
> hard-coded "operation" line now reads from `op_label`. This is folded
> into this issue as the first sub-step (rather than a separate issue)
> because it is small, mechanical, and Issue 3.2 is the only consumer.

**Implementation Notes:**

- **Sub-step 0 — Phase 1 `op_label` touchpoint (do this first):**
  Before any `--revoke` code lands, add an `op_label` field to
  `summary_ctx` across the three Phase 1 `do_*` functions —
  `do_transfer` (Phase 1 Issue 1.3): `summary_ctx["op_label"] = "transfer"`;
  `do_transfer_from` (Phase 1 Issue 1.5): `summary_ctx["op_label"] = "transfer-from"`;
  `do_approve` (Phase 1 Issue 1.4): `summary_ctx["op_label"] = "approve"`
  (existing two paths) or `"revoke"` (new path, set later in Sub-step 4).
  Refactor `summary.render_summary` to read the operation line from
  `summary_ctx["op_label"]` instead of the per-subcommand hard-coded
  string. This is **additive and backward-compatible** — the rendered
  output is byte-identical for every Phase 1 path because the previously
  hard-coded value matches the new `op_label` value. Add a regression
  test in `TestSummary` that pins the byte-identical Phase 1 output
  before adding any Phase 3 logic. Then proceed with the `--revoke`
  sub-steps below.
- Files likely affected:
  - `.claude/skills/eth-tx-builder/build_erc20.py`:
    - **`tx_assembly.do_transfer`, `do_transfer_from`, `do_approve`
      (Phase 1 touchpoint per Sub-step 0):** add
      `summary_ctx["op_label"] = "<op>"` to each. Backward-compatible
      additive change; no callers need to update because Phase 1 callers
      do not read `op_label`.
    - **`summary.render_summary` (Phase 1 touchpoint per Sub-step 0):**
      replace the hard-coded "operation" string with
      `summary_ctx["op_label"]`. The pre-existing Phase 1 output stays
      byte-identical.
    - **`cli_dispatch._build_parser`**: extend the existing
      `add_mutually_exclusive_group(required=True)` on the `approve`
      subparser with a third entry, `parser.add_argument("--revoke",
      action="store_true", help="Revoke approval (sets allowance to 0
      for spender).")`. No new parser group; reuse the existing mutex.
    - **`cli_dispatch.main`**: when dispatching `approve`, forward
      `revoke=args.revoke` into `do_approve` alongside the existing
      `approve_max=args.approve_max`. argparse's three-way mutex
      guarantees exactly one is True (or `--amount` is set), so no
      manual cross-check is needed.
    - **`tx_assembly.do_approve`**: extend signature to
      `do_approve(network, token, spender, amount, sender, *,
      approve_max=False, revoke=False, rpc=_core.rpc_call)`. Branch
      logic: if `revoke`, set `amount_base = 0`, skip
      `human_to_base_units`, queue an `approve_revoke` summary marker
      so the summary op label can render "revoke"; if `approve_max`,
      existing path; otherwise the existing human-amount path.
      `decimals()` and `symbol()` are still fetched in every branch
      (architecture ADR-006 — `decimals` is fatal, `symbol` best-effort
      — preserved verbatim from Phase 1).
    - **`summary.render_summary`**: extend `summary_ctx` to carry a
      new `op_label` field (string: `"transfer"` / `"approve"` /
      `"revoke"` / `"transfer-from"`). The existing summary block
      already prints "operation" — change it to read from
      `op_label` rather than hard-coding "approve" for the approve
      subcommand. When `op_label == "revoke"`, the summary's
      "operation" line reads `operation: revoke`. The "amount"
      line shows `amount (base units): 0  -> approve(spender, 0)`
      to keep technical accuracy.
    - **`summary`**: add `warn_approve_revoke(symbol, token, spender)`
      — an info-level (not "WARNING:") stderr line confirming the
      revoke is targeting the right token + spender. Reuses the
      multi-line style of `warn_approve_max`. Even though it's
      informational, route it through `emit_warning` so it appears
      in `warnings_list` and tests can pin it.
  - `.claude/skills/eth-tx-builder/test_build_erc20.py`:
    - Extend `TestCliDispatch`:
      - `approve` with `--revoke` exits 0, JSON on stdout, summary on
        stderr including the `operation: revoke` line.
      - `approve` with both `--revoke` and `--amount` → argparse
        rejects, exits 2 (argparse's default mutex error); stderr
        names the offending args.
      - `approve` with both `--revoke` and `--approve-max` → argparse
        rejects, exits 2.
      - `approve` with `--revoke` and the existing `--amount` /
        `--approve-max` all absent → argparse rejects "one of the
        arguments is required."
    - Extend `TestTxAssembly`:
      - `do_approve(..., revoke=True)` happy path: calldata's amount
        word is all-zeros (32-byte zero word — verify bit-pattern),
        `warnings_list` contains `("approve_revoke", {symbol, token,
        spender})`, `summary_ctx["op_label"] == "revoke"`.
      - `do_approve(..., revoke=True)` does NOT call `human_to_base_units`
        (assert via the `mock.patch` pattern already used elsewhere in
        the test file).
      - `do_approve(..., revoke=True)` still calls `fetch_decimals`
        (FATAL on failure — assert `RPCError` from `fetch_decimals`
        propagates and no JSON is emitted).
      - `do_approve(..., revoke=True, approve_max=True)` is rejected at
        the function level with `ValueError("--revoke and
        --approve-max are mutually exclusive")` as a defense-in-depth
        check (argparse already rejects this at the CLI layer, but the
        function-level check guards against direct callers in future
        sibling helpers).
    - Extend `TestSummary`:
      - `render_summary(ctx)` with `op_label="revoke"` includes
        `operation: revoke` in the returned text.
      - `warn_approve_revoke` writes the multi-line confirmation block
        to stderr (capture with `unittest.mock.patch('sys.stderr',
        io.StringIO())`) naming the symbol, token, and spender.
- Approach:
  - The implementation is mechanical once Issue 3.1's ADR-012 fixes the
    design. The biggest risk is summary phrasing drift — keep
    `render_summary`'s line-by-line layout identical to Phase 1 except
    for the op-label line.
  - Reuse `encode_approve` with `amount_base = 0`. No new selector or
    encoder; the existing 32-byte zero-word handling in
    `_encode_uint256(0)` is already test-covered by `TestAbiCodec`
    (Phase 1 Task 1.2's "0" vector).
  - Symmetry with `approve_max`: every place `approve_max` is
    threaded (kwarg, dispatch, summary warning) gets a matching
    `revoke` thread. Grep for `approve_max` in `build_erc20.py` and
    mirror; this is the simplest correctness check.
- Key decisions: (a) reuse the existing two-entry mutex group rather
  than creating a new three-entry group (argparse handles the wider
  mutex natively); (b) `op_label` on `summary_ctx` is the single source
  of truth for the summary's operation line (refactor the existing
  hard-coded "approve" out into `op_label`); (c) `warn_approve_revoke`
  is informational, not a WARNING — operators chose `--revoke`
  deliberately, so a loud warning would be noise.
- Watch out for: argparse's three-entry mutex error message format —
  some Python versions phrase it slightly differently
  (`argument --revoke: not allowed with argument --amount` vs.
  `argument --revoke: not allowed with argument --approve-max`). Tests
  should match on a substring (`"not allowed with"`) rather than the
  full string to avoid version-coupling.
- Watch out for: the `warnings_list` shape contract from architecture
  ADR-004 — every entry is a `(kind, payload_dict)` tuple. The new
  `("approve_revoke", {...})` entry must follow that shape, not a
  bare string.
- Watch out for: defense-in-depth check inside `do_approve`. The
  function-level `ValueError("--revoke and --approve-max are mutually
  exclusive")` only fires when a direct caller passes both kwargs (the
  CLI argparse layer already rejects this at the operator level). Test
  the function-level check via direct invocation, not via `main`.
- New files to create: none.

**Acceptance Criteria:**
- [x] **Phase 1 touchpoint (Sub-step 0):** `do_transfer`,
      `do_transfer_from`, and `do_approve` all set
      `summary_ctx["op_label"]` to a string (`"transfer"` /
      `"transfer-from"` / `"approve"` respectively). The change is
      purely additive — no Phase 1 caller reads `op_label` and no Phase 1
      caller breaks.
- [x] **Phase 1 touchpoint regression test:** `TestSummary` includes a
      byte-identical pinning test for each of the three Phase 1
      `render_summary` outputs (`transfer`, `approve --amount`,
      `transfer-from`), proving the refactor does NOT change Phase 1's
      stderr output. `python3 -m unittest test_build_erc20 -v` passes
      both **before** the `--revoke` code lands (validating the additive
      Phase 1 change alone) AND after (validating the full Phase 3
      Issue 3.2 change).
- [x] `summary.render_summary` reads the operation line from
      `summary_ctx["op_label"]` (no hard-coded subcommand-specific
      strings remain).
- [x] `cli_dispatch._build_parser` adds `--revoke` to the existing
      `approve` mutex group; `add_mutually_exclusive_group(
      required=True)` still has exactly one required choice across
      `{--amount, --approve-max, --revoke}`.
- [x] `do_approve` signature is
      `do_approve(network, token, spender, amount, sender, *,
      approve_max=False, revoke=False, rpc=_core.rpc_call)`.
- [x] When `revoke=True`, `amount_base = 0`; `human_to_base_units` is
      NOT called; `encode_approve(spender, 0)` is used; the resulting
      32-byte amount word is all-zeros.
- [x] When `revoke=True`, `fetch_decimals` and `fetch_symbol` are still
      called (preserves the Phase 1 fatal-or-skip contract per ADR-006).
- [x] When `revoke=True` AND `approve_max=True` are both passed to
      `do_approve` directly (bypassing argparse), `ValueError("--revoke
      and --approve-max are mutually exclusive")` is raised; no JSON is
      emitted.
- [x] `summary_ctx["op_label"] == "revoke"` when `revoke=True`,
      `"approve"` for the existing two paths, `"transfer"` for
      `do_transfer`, `"transfer-from"` for `do_transfer_from`.
- [x] `render_summary(ctx)` reads from `op_label`; the rendered text
      includes `operation: revoke` when applicable.
- [x] `warnings_list` contains `("approve_revoke", {symbol, token,
      spender})` on the revoke path; `warn_approve_revoke` writes the
      multi-line confirmation to stderr; `emit_warning` dispatches on
      `"approve_revoke"`.
- [x] `TestCliDispatch` covers: `--revoke` happy path (exit 0, JSON on
      stdout, summary + revoke confirmation on stderr); three argparse
      rejection cases (revoke+amount, revoke+approve-max, none of the
      three).
- [x] `TestTxAssembly` covers: revoke happy path with bit-pattern
      assertion on the amount word, no `human_to_base_units` call
      verified via mock, `fetch_decimals` still called, defense-in-depth
      `ValueError` on mixed kwargs.
- [x] `TestSummary` covers: `render_summary` with `op_label="revoke"`
      and `warn_approve_revoke` stderr capture.
- [x] `python3 -m unittest test_build_erc20 -v` passes locally.
- [x] The existing `transfer` / `transfer-from` / non-revoke `approve`
      paths are unchanged; their `TestCliDispatch` / `TestTxAssembly`
      cases pass unmodified.
- [x] `python3 -m unittest test_build_send_eth -v` still passes — Phase
      3 does not touch `build_send_eth.py`.

**Testing Notes:**
- All tests inject `rpc=Mock()` from `unittest.mock` per the existing
  Phase 1 pattern. No network calls.
- The bit-pattern assertion on the all-zeros amount word is the
  highest-leverage test (catches "did we accidentally call
  `encode_approve(spender, MAX_UINT256)` from the revoke branch?").
  Pin the literal expected calldata via the same golden-vector
  technique Phase 1 Task 1.2 used.
- For the defense-in-depth check, call `do_approve` directly with both
  kwargs True; this is the only test that bypasses argparse.

---

### Issue 3.3: `bytes32` decode polish — catalog spike against `d-xo/weird-erc20`

- **Points:** 1
- **Type:** spike
- **Priority:** P0 (within Phase 3 — gates the implementation issue)
- **Blocked by:** none
- **Blocks:** 3.4
- **Scope:** 0.5-1 day

**Description:**

PRD §P2.4 names "bytes32 symbol decode polish" as a niche improvement
against the half-dozen historical token formats (MKR, DGD, etc.) that
return `bytes32` instead of the standard ABI `string` for `symbol()`.
The Phase 1 `decode_symbol` already handles the common case (standard
ABI `string`, then a `bytes32`-with-null-trim fallback, then `None`), but
the fallback is minimal — research §02-erc20-safety-ux flags edge cases
where the legacy decoder returns the wrong ticker or `None` for tokens
that real operators want surfaced in the summary.

This spike catalogs the legacy formats from `d-xo/weird-erc20` and any
other operator-flagged tokens, picks the exact fallback variants to
implement in Issue 3.4, and records the bounded list as an ADR so the
implementation has a clear "ship list" and a clear "still returns
`None`" tail.

**Implementation Notes:**

- Files likely affected:
  - `plan/eth-tx-builder-erc20/architecture.md` — add ADR-013
    (`Polished bytes32 symbol decode — bounded format catalog`).
  - Optionally: a working scratch file in `plan/eth-tx-builder-erc20/
    research/04-bytes32-symbol-catalog.md` capturing the per-token
    response shapes. Defer creation to the spike-writer's judgement —
    the ADR itself can carry the catalog if it's short.
- Approach:
  - Catalog the relevant `d-xo/weird-erc20` entries (MKR, DGD, MANA,
    SAI, REP, KNC where applicable) plus any operator-supplied tokens.
    For each, record: (a) the literal hex returned by `symbol()` on
    `eth_call`; (b) the human-readable ticker that should be decoded;
    (c) the format variant (null-padded ASCII, length-prefix-then-data
    even though it claims to be `bytes32`, etc.).
  - Decide the **bounded fallback ladder**: standard ABI `string`
    (Phase 1) → null-trimmed `bytes32` ASCII (Phase 1) → new variant
    1 → new variant 2 → ... → `None`. Each variant is a small,
    independently-testable decoder rule.
  - Decide where the implementation lives: extend the existing
    `decode_symbol` in `abi_codec`, NOT in a new section. ADR-002's
    "seven labeled sections" stays — no new section is needed.
  - Decide test fixtures: per-token literal hex response → expected
    ticker pairs, plus malformed-response → `None` cases. Fixtures
    are static dicts in `TestAbiCodec`, no `rpc` mock needed.
- Key decisions: the spike picks a **finite list** of variants
  (project plan R10 mitigation: "scope Task 3.2 to a finite list
  (MKR, DGD); stop when the catalog is exhausted"). The list is the
  ADR's "ship list"; anything outside it remains best-effort and
  returns `None`.
- Watch out for: legacy tokens where the response is technically
  malformed (e.g. `bytes32` returned without the 32-byte-multiple
  padding ABI typically uses). The decoder ladder must defensively
  handle short responses — slice with bounds, not raw byte arithmetic.
- Watch out for: the temptation to chase every weird-erc20 entry.
  The ADR explicitly bounds the catalog so the implementation doesn't
  drift into an infinite-tail problem (project plan R10).
- New files to create: none (ADR is appended to `architecture.md`);
  optional `research/04-bytes32-symbol-catalog.md` if the catalog is
  long enough to warrant its own file.

**Acceptance Criteria:**
- [x] A new ADR (numbered `ADR-013`, dated, status `Accepted`) is
      appended to `plan/eth-tx-builder-erc20/architecture.md` covering
      the bounded format catalog with explicit "Status / Context /
      Decision / Alternatives / Consequences" sections matching the
      existing ADR house style.
- [x] The ADR records the **exact list** of legacy formats to handle
      (e.g. "MKR-style null-padded ASCII; DGD-style length-prefixed
      bytes32; ...") with one literal-hex example per variant pulled
      from a real on-chain response or the `d-xo/weird-erc20` repo.
- [x] The ADR records the **fallback ladder order**: standard ABI
      `string` → null-trimmed `bytes32` ASCII → new variant 1 →
      new variant 2 → ... → `None`. Order matters because variants
      may accept the same byte shape with different decodings.
- [x] The ADR cross-references PRD §P2.4 (the source requirement),
      architecture ADR-006 (the `Optional[str]` return contract that
      ADR-013 preserves), and project plan R10 (the scope-bound
      mitigation).
- [x] The ADR records the "still returns `None`" tail explicitly:
      formats outside the bounded list remain best-effort, not
      promoted to "supported."
- [x] No code changes are committed in this issue.

**Testing Notes:**
- This is a paper spike; no automated tests. Reviewer-led check is
  the exit criterion.

---

### Issue 3.4: Implement polished `bytes32` `decode_symbol` fallback

- **Points:** 2
- **Type:** feature
- **Priority:** P1 (within Phase 3)
- **Blocked by:** 3.3
- **Blocks:** 3.5 (docs), 3.8 (e2e)
- **Scope:** 1-2 days

**Description:**

Implement the bounded `bytes32` `decode_symbol` fallback ladder decided
by Issue 3.3's ADR-013. The Phase 1 `decode_symbol` is replaced — but
its existing contract is preserved verbatim: returns `Optional[str]`,
never raises (architecture ADR-006). On the polished implementation, the
new variants slot into the existing fallback ladder; unknown
malformations still return `None` (best-effort posture preserved).

**Implementation Notes:**

- Files likely affected:
  - `.claude/skills/eth-tx-builder/build_erc20.py`:
    - **`abi_codec.decode_symbol`** is extended with the bounded
      fallback ladder from ADR-013. Implementation shape:
      ```python
      def decode_symbol(hex_result):
          # Standard ABI string (Phase 1)
          decoded = _try_decode_abi_string(hex_result)
          if decoded is not None:
              return decoded
          # bytes32 null-trimmed ASCII (Phase 1)
          decoded = _try_decode_bytes32_null_trimmed(hex_result)
          if decoded is not None:
              return decoded
          # New variant 1 (per ADR-013)
          decoded = _try_decode_<variant1>(hex_result)
          if decoded is not None:
              return decoded
          # ... additional variants ...
          # Best-effort fallback (Phase 1)
          return None
      ```
      Each `_try_decode_*` helper returns `Optional[str]`; the ladder
      short-circuits at the first non-`None`.
    - The existing two helpers (`_try_decode_abi_string`,
      `_try_decode_bytes32_null_trimmed`) are extracted from the
      Phase 1 `decode_symbol` body if they aren't already separate
      functions — this is the "polish" structural refactor, before
      adding new variants.
    - Each new `_try_decode_<variant>` helper is a small focused
      function that returns the ticker string on a match or `None`
      otherwise. No exceptions escape — the helpers catch
      `UnicodeDecodeError`, `ValueError`, and any indexing errors and
      return `None`.
  - `.claude/skills/eth-tx-builder/test_build_erc20.py`:
    - Extend `TestAbiCodec` with `TestDecodeSymbolPolished` (a
      sub-class or sibling `TestCase`):
      - One test per bounded variant from ADR-013: input literal hex →
        expected decoded ticker. Use fixtures lifted verbatim from
        the ADR's worked examples.
      - One regression test per Phase 1 path: standard ABI `string`
        for USDC still decodes to `"USDC"`; standard bytes32 null-
        trimmed for legacy MKR still decodes correctly.
      - One malformed-response test per variant: a truncated or
        otherwise-broken response still returns `None` (never
        raises). Catches the "defense against legacy junk" property.
      - One "outside the catalog" test: a deliberately-crafted hex
        response that matches no variant returns `None`. Confirms
        the bounded-catalog property is preserved.
- Approach:
  - Refactor first: pull `_try_decode_abi_string` and
    `_try_decode_bytes32_null_trimmed` out of the Phase 1
    `decode_symbol` body into named helpers. This is a no-op
    behavior change; existing `TestAbiCodec` cases must pass without
    modification.
  - Then add the ADR-013 variants one helper at a time, each gated
    by its own test fixture.
  - Keep every helper's exception swallow tight: catch only the
    specific exceptions that genuinely indicate "this variant doesn't
    apply" (UnicodeDecodeError, ValueError, IndexError). Don't catch
    `Exception` broadly — that hides real bugs.
- Key decisions: (a) refactor before extending — the existing
  fallback ladder becomes the spine, new variants are insertions
  rather than rewrites; (b) per-variant `_try_decode_*` helpers
  (not a single megafunction) so each variant is independently
  testable and the ladder's flow is grep-able; (c) the `Optional[str]`
  contract is preserved end-to-end — the polished implementation
  must NEVER raise (architecture ADR-006).
- Watch out for: ordering matters. A variant that accepts the same
  byte shape as a later variant must be tried first (or vice versa,
  depending on whether one is a superset). ADR-013's ladder order
  is the source of truth.
- Watch out for: byte-length checks. Some legacy responses are
  shorter than 32 bytes (some are longer with extra padding). Use
  `len(raw)` bounds checks defensively; don't assume the response
  is a multiple of 32 bytes.
- Watch out for: regression on the Phase 1 paths. The polished
  implementation must NOT break USDC's standard ABI `string` decode
  or the existing legacy null-trimmed bytes32 decode. Run the
  existing `TestAbiCodec.test_decode_symbol_standard` and
  `test_decode_symbol_bytes32` cases unchanged.
- New files to create: none.

**Acceptance Criteria:**
- [x] `abi_codec.decode_symbol` is refactored to a fallback ladder of
      `_try_decode_*` helpers, one per variant, short-circuiting on
      the first non-`None` return.
- [x] Every variant named in ADR-013 has a corresponding
      `_try_decode_*` helper.
- [x] Every variant has a unit test in `TestAbiCodec` (or a new
      `TestDecodeSymbolPolished` `TestCase`) with: (a) a happy-path
      fixture from ADR-013's worked examples; (b) a malformed-input
      fixture that returns `None`.
- [x] The Phase 1 USDC standard ABI `string` decode still works
      unchanged.
- [x] The Phase 1 legacy null-trimmed bytes32 decode (MKR, etc.) still
      works unchanged.
- [x] An "outside the catalog" hex response returns `None`,
      confirming the bounded-catalog property.
- [x] `decode_symbol` NEVER raises, regardless of input — even
      bizarre / malformed / empty bytes. Test this explicitly.
- [x] `python3 -m unittest test_build_erc20 -v` passes locally;
      every Phase 1 `TestAbiCodec` case passes unmodified.
- [x] `python3 -m unittest test_build_send_eth -v` still passes
      (Phase 3 does not touch `build_send_eth.py`).
- [x] No new top-level imports in `build_erc20.py` (architecture
      PA-4: stdlib-only; the polish reuses existing `bytes.fromhex`,
      slicing, and `.decode("utf-8", "ignore")` patterns).

**Testing Notes:**
- All tests are pure unit tests against static fixtures (no `rpc`
  mock needed — `decode_symbol` is a pure function).
- The per-variant fixtures should be lifted verbatim from ADR-013's
  worked examples to avoid drift between the ADR and the tests.
- The "outside the catalog" test is the highest-leverage scope-creep
  guard — without it, future contributors can quietly accrete
  variants without updating the ADR.

---

### Issue 3.5: SKILL.md + README docs update for whichever of `--revoke` / `bytes32` polish shipped

- **Points:** 2
- **Type:** docs
- **Priority:** P1 (within Phase 3)
- **Blocked by:** **ANY-OF** Issue 3.2 OR Issue 3.4 — explicitly NOT all-of.
  At least one of 3.2 / 3.4 must have landed on `develop`; this issue
  can run as soon as the first of the two is done, and it covers only
  the feature(s) that actually shipped. If only 3.2 shipped, this issue
  documents only `--revoke`; if only 3.4 shipped, only the `bytes32`
  polish; if both shipped, both. If neither shipped, this issue does
  NOT fire.
- **Blocks:** 3.8 (e2e)
- **Scope:** 1 day (less if only one of 3.2 / 3.4 shipped)

**Description:**

Add documentation for whatever Phase 3 features shipped (excluding
`permit`, which has its own docs path via its own PRD per project plan
DL-10 — Issue 3.6 owns the draft PRD skeleton; this issue does NOT
pre-document `permit`). This is a single combined docs pass at the end
of the Phase 3 core work, not per-issue updates, to keep the SKILL.md /
README diff coherent and avoid two small overlapping docs PRs.

The dependency is **ANY-OF, not all-of**: this issue runs whenever at
least one of 3.2 / 3.4 has shipped, covering only what's actually
on `develop`. The acceptance criteria below are conditional on which
feature(s) shipped — see the "If 3.2 shipped" / "If 3.4 shipped"
gating in each bullet.

**Implementation Notes:**

- Files likely affected:
  - `.claude/skills/eth-tx-builder/SKILL.md`:
    - **Inputs section** (if Issue 3.2 shipped): in the "ERC-20
      transfer / approve / transferFrom" subsection, mention the new
      `--revoke` flag as a third mutex entry alongside `--amount` and
      `--approve-max`, with a one-line note: "Use `--revoke` to set
      allowance to 0 (revoke a prior approval). Equivalent to
      `approve(spender, 0)`."
    - **Procedure section** (if Issue 3.2 shipped): add a line in the
      operation-selection step pointing the agent at `--revoke` when
      the operator's intent is "remove a previous allowance."
    - **Out of scope (v1) section**: remove the `--revoke` shorthand
      from any "out of scope" placeholder if Phase 3 listed it
      provisionally; `--revoke` is now in-scope.
    - **Notes / caveats**: add a SKILL.md note explaining that
      `approve(spender, 0)` is the standard ERC-20 revocation
      mechanism but is **not universally honoured** by every spender
      (project plan R9 mitigation). Operators using routers with
      custom permission systems should double-check the spender's
      docs.
    - **(if Issue 3.4 shipped)** A short prose update in the
      "symbol display" notes mentioning the broader legacy-token
      coverage from ADR-013, listing the new variants by ticker
      symbol where it doesn't bloat the doc. Stop short of
      enumerating every byte format.
  - `.claude/skills/eth-tx-builder/README.md`:
    - **File list**: no change (Phase 3 ships inside `build_erc20.py`).
    - **Example invocations** (if Issue 3.2 shipped): add a worked
      `--revoke` example mirroring the existing `approve --amount`
      and `approve --approve-max` examples:
      ```bash
      # Revoke approval (sets allowance to 0)
      python3 build_erc20.py approve \
        --network mainnet \
        --token 0xA0b86991... \
        --spender 0xRouter... \
        --revoke \
        --sender 0x...
      ```
    - **Manual end-to-end** subsection: add a Phase 3 entry for the
      revoke e2e against hoodi (lifted from Issue 3.8's capture).
    - **(if Issue 3.4 shipped)** A one-line note in the existing
      "summary fields" prose mentioning the expanded legacy-token
      coverage. Do not list every variant — point at ADR-013.
- Approach:
  - Make this issue a **conditional** docs sweep: only document what
    actually shipped. The exit criteria are "every shipped Phase 3
    feature has at least one worked example in README and is
    mentioned in SKILL.md."
  - Keep the prose terse — the worked examples carry the load.
- Key decisions: one combined docs PR, not two. Cuts review burden.
- Watch out for: drift between SKILL.md / README examples and the
  actual CLI shape. Copy CLI invocations from `--help` output (or
  Issue 3.8's manual e2e capture) rather than paraphrasing from
  memory.
- Watch out for: the "approve(0) is not universally honoured" note.
  This is the project plan R9 mitigation; the wording should be
  factual ("standard ERC-20 mechanism but not all spenders honour
  it") rather than alarmist.
- Watch out for: do NOT pre-document `permit`. If Issue 3.6 spins
  out a fresh PRD, that PRD's docs update is a separate task. This
  issue covers `--revoke` and `bytes32` polish only.
- New files to create: none.

**Acceptance Criteria:**

The acceptance criteria are explicitly **conditional on which of 3.2 /
3.4 shipped** — this issue runs as soon as at least one of them is on
`develop` (ANY-OF), and covers only the shipped feature(s).

**Always (unconditional):**
- [ ] At least one of Issue 3.2 or Issue 3.4 has shipped on `develop`
      before this issue starts (the ANY-OF entry criterion).
- [ ] No broken cross-links to other doc sections.
- [ ] `python3 build_erc20.py --help` and `python3 build_erc20.py
      approve --help` still work; example invocations in docs match
      the actual help output.
- [ ] No `*.md` file is reformatted wholesale; only added /
      replaced sections (keeps the diff readable).
- [ ] Phase 1's "ETH send" SKILL.md content is unchanged.
- [ ] Phase 1's README "Manual end-to-end" entries for the three
      base ops are unchanged.
- [ ] No `permit` content is added to SKILL.md or README in this
      issue (Issue 3.6 owns the draft PRD skeleton; `permit` docs
      ship under the fresh PRD).

**Conditional on Issue 3.2 (`--revoke`) having shipped:**
- [ ] SKILL.md "Inputs" section mentions `--revoke` as a third mutex
      entry for `approve`, with the one-line explanation.
- [ ] README has a worked `--revoke` example mirroring the Phase 1
      `approve --amount` and `approve --approve-max` examples.
- [ ] SKILL.md "Notes / caveats" includes the "approve(0) is the
      standard mechanism but not universally honoured" note
      (project plan R9 mitigation).
- [ ] SKILL.md "Procedure" step has a routing hint for
      "operator intent = remove a previous allowance" → `--revoke`.

**Conditional on Issue 3.4 (polished `bytes32` decode) having shipped:**
- [ ] SKILL.md "symbol display" notes mention the broader legacy-token
      coverage and cross-reference ADR-013.
- [ ] README has a one-line acknowledgement of the expanded coverage
      (no enumeration of byte formats — point at ADR-013).

**Conditional on neither 3.2 nor 3.4 having shipped:**
- [ ] This issue does NOT fire. The Phase 3 docs sweep is moot;
      Issue 3.8 inherits the docs-untouched state and only re-runs
      the Phase 1 regression e2e.

**Testing Notes:**
- No automated tests; reviewer-led check is the exit criterion.
- Use Issue 3.8's manual e2e output as the source of truth for the
  `--revoke` worked example in README.

---

### Issue 3.6: `permit` go/no-go spike + EIP-712 signer external-blocker resolution + full draft `eth-tx-builder-erc20-permit` PRD skeleton

- **Points:** 2
- **Type:** spike
- **Priority:** P2 (within Phase 3 — explicitly conditional on operator demand)
- **Blocked by:** none
- **Blocks:** 3.7 (the deferred-placeholder tracking issue)
- **Scope:** 1-2 days

**Description:**

PRD §P2.2 names EIP-2612 `permit` as a "nice to have" that is
out-of-scope by default because `permit` requires signing a typed-data
(EIP-712) digest, which expands the skill's responsibility beyond
"build calldata." Project plan §Phase 3 Task 3.3 records the deferral:
ship as a separate helper `build_erc20_permit.py` matching the
architecture's "service extraction path" (a new sibling, not a fifth
section inside `build_erc20.py`) IF a real router workflow needs it,
**and only under its own fresh PRD per project plan DL-10**.

This spike answers **five** questions before any implementation is
scheduled (no `permit` code lands in this Phase 3 regardless of the
outcome — implementation is owned by the fresh PRD's own phase plan):

1. **Demand check.** Does a real operator workflow currently need
   `permit`? If no, the spike's recommendation is "defer indefinitely;
   the fresh PRD does not get drafted further until demand surfaces."
2. **Sibling vs. extension.** Should `permit` live in a new sibling
   helper (`build_erc20_permit.py`) or as a fourth subcommand in
   `build_erc20.py`? Architecture ADR-001 and project plan DL-10
   default to sibling; the spike confirms or contests.
3. **Dependency posture.** Can `permit` stay stdlib-only (hand-written
   EIP-712 hashing — same approach as the existing ABI codec)? If
   yes, the fresh PRD inherits the stdlib-only constraint. If no, the
   fresh PRD must explicitly open the dep conversation.
4. **(NEW — undeclared external-blocker resolution) `eth-signer-mcp`
   EIP-712 / typed-data signing support.** Today `eth-signer-mcp`'s
   README explicitly states "EIP-191 `personal_sign` and EIP-712
   typed-data signing: not supported" (`apps/eth-signer-mcp/README.md`
   §Out-of-scope). The `permit` calldata builder is useless without a
   signer for the EIP-712 digest, so this is a **real undeclared
   external blocker**. The spike must answer: (a) is adding EIP-712
   support to `eth-signer-mcp` a small extension (likely — go-ethereum
   supports it) or a non-trivial new tool / handler? (b) does the
   `permit` PRD depend on a parallel `eth-signer-mcp` enhancement
   PRD? (c) what is the integration contract (the calldata-builder
   emits the typed-data structure; the signer consumes it via a new
   `sign_typed_data` MCP tool)? The spike either declares this
   dependency as a separate parallel work-stream (with its own PRD
   pointer) or resolves it (e.g. "use an external EIP-712 signer
   like Frame / Metamask for v1; `eth-signer-mcp` integration is a
   v2 follow-up").
5. **Full draft `eth-tx-builder-erc20-permit` PRD skeleton.** The
   project plan DL-10 cascade ("fresh PRD before fresh phase plan
   before code") means the PRD itself is this spike's **primary
   deliverable** — not a paragraph each but a **fully-drafted
   skeleton**: Overview (1-2 paragraphs framing the workflow);
   Problem Statement (what operator pain `permit` removes; the
   gas-free approval UX); Goals (success metrics, scope boundary);
   Functional Requirements broken into P0 / P1 / P2 (each with
   specific user stories and acceptance-criterion stubs); Out of
   Scope (explicitly: ERC-3009 transferWithAuthorization;
   non-EIP-2612 permit variants; signer-side EIP-712 support if
   that's spun out as its own PRD). The fresh PRD is the source of
   truth for any future `permit` work; this spike makes it real
   enough to be reviewed and either approved (greenlight) or
   shelved (defer).

This is a **paper-only spike**: produce a short ADR (5-7 paragraphs)
appended to `plan/eth-tx-builder-erc20/architecture.md` AND the full
draft PRD skeleton at `plan/eth-tx-builder-erc20-permit/prd.md`.
**No `permit` code lands in this Phase 3 regardless of the spike's
outcome** — implementation is owned by the fresh PRD's own phase plan
(project plan DL-10). Issue 3.7 only records the deferral decision.

**Implementation Notes:**

- Files likely affected:
  - `plan/eth-tx-builder-erc20/architecture.md` — add ADR-014
    (`Optional permit (EIP-2612) sibling helper — go/no-go + EIP-712
    signer external-blocker resolution`).
  - **NEW (always — not conditional)** file
    `plan/eth-tx-builder-erc20-permit/prd.md` containing the **full
    draft PRD skeleton**: Overview, Problem Statement, Goals,
    Functional Requirements broken into P0 / P1 / P2 sections (each
    with at least 2-3 specific user stories with acceptance-criterion
    stubs), and Out of Scope. Not "a paragraph each" — this is a
    real first-pass PRD that a future planning session can iterate
    on. If the spike's demand check concludes "defer indefinitely,"
    the PRD still gets drafted but its Status front-matter says
    "Draft — awaiting demand surfacing"; the artifact exists so
    Issue 3.7 has somewhere concrete to point.
- Approach:
  - **Demand check (Q1):** scan operator-facing repo discussions,
    project plan R11 ("permit brings cryptographic complexity"), and
    any pull requests / issues. The default conclusion is "no demand
    surfaced yet"; the spike records this honestly. If demand HAS
    surfaced since Phase 1 closed, name the workflow and proceed to
    Q2-Q4 with the demand on record.
  - **Sibling vs. extension (Q2):** confirm the project plan DL-10
    default (sibling, `build_erc20_permit.py`) by listing the concrete
    benefits — independent shippability, no contamination of the
    Phase 1 single-file architecture, no expansion of
    `build_erc20.py`'s `_core` import contract, easier rollback. List
    the costs — a second SKILL.md routing entry, a second test file,
    duplicated `_core` plumbing. Costs are manageable; recommendation
    holds.
  - **Dependency posture (Q3):** confirm or contest the stdlib-only
    viability for EIP-712 hashing. The hashing requires keccak256 —
    which the Python stdlib does NOT ship natively (PRD assumption 9:
    `hashlib.sha3_256` is SHA-3, NOT keccak). Two paths: (a) hand-
    write a keccak256 implementation (~50-100 lines of Python; well-
    trodden territory); (b) accept a single new dep (`pycryptodome`
    or similar). Recommendation: hand-write — preserves stdlib-only,
    avoids the dep conversation, mirrors the existing "hardcoded ABI
    selectors" precedent. Note: this is non-trivial; the fresh PRD
    must include a "validate keccak256 against known vectors" test
    surface.
  - **(NEW) `eth-signer-mcp` EIP-712 support (Q4):** read
    `apps/eth-signer-mcp/README.md` §Out-of-scope (which today reads
    "EIP-191 `personal_sign` and EIP-712 typed-data signing: not
    supported"). Confirm this remains the current state. Then decide:
    - **Path A:** spin out a parallel `eth-signer-mcp-eip712` PRD
      (or a minor PRD addendum to the existing signer's plan) that
      adds a new MCP tool (`sign_typed_data` or similar) using
      go-ethereum's EIP-712 utilities. The `permit` PRD declares
      this signer enhancement as a **hard external dependency** in
      its P0 prerequisites.
    - **Path B:** accept that the `permit` builder's v1 ships
      assuming an **external** EIP-712 signer (Frame, Metamask,
      hardware wallet) and `eth-signer-mcp` integration is a v2
      follow-up. The builder emits the typed-data structure for the
      operator to sign elsewhere.
    - **Recommendation (default):** Path B for v1 (avoids coupling
      the `permit` PRD timeline to a signer PRD timeline) with Path A
      named explicitly as the v2 follow-up. The fresh PRD records
      this two-step plan; this spike's ADR records the choice and
      the reasoning.
    Whatever the decision, the ADR must EXPLICITLY name the
    `eth-signer-mcp` EIP-712 gap — until this spike, it was an
    **undeclared external blocker**, which is exactly why the
    adversarial review flagged it.
  - **(NEW) Full draft PRD skeleton (Q5):** the primary deliverable.
    Write `plan/eth-tx-builder-erc20-permit/prd.md` with:
    - **Overview:** what `permit` is, the gasless approval UX, the
      target workflow (DEX / router approvals that today require
      a separate `approve` tx).
    - **Problem Statement:** the operator pain — two-tx UX for
      router workflows; `approve(max)` security risk; explicit
      reference to the Phase 1 `--revoke` shorthand as the
      complementary mitigation today.
    - **Goals:** ship a `build_erc20_permit.py` sibling helper that
      emits both the calldata for `permit(...)` AND the EIP-712
      typed-data digest the operator signs; the signed digest +
      calldata get composed into a permit-then-action workflow.
    - **Functional Requirements:**
      - **P0** (must ship for v1): build calldata for
        `permit(address,address,uint256,uint256,uint8,bytes32,bytes32)`;
        emit the EIP-712 typed-data structure in the JSON output;
        verify against a published golden vector (e.g. USDC permit on
        mainnet). Include `nonces(owner)` read, `name()` / `version()`
        reads, `DOMAIN_SEPARATOR()` read or recompute.
      - **P1** (nice-to-have): `--deadline` shorthand (e.g.
        `--deadline-in-hours 24`); pretty-printed typed-data summary
        to stderr.
      - **P2** (later): `eth-signer-mcp` EIP-712 integration (per
        Q4 Path A); ERC-3009 transferWithAuthorization sibling.
    - **Out of Scope:** ERC-3009 transferWithAuthorization
      (separate PRD if pursued); non-EIP-2612 permit variants
      (DAI's older variant; the spike records whether v1 covers
      this); signer-side EIP-712 support (if Q4 chose Path B, this
      is out-of-scope until the v2 follow-up PRD); EIP-1271 contract
      signatures.
- Key decisions to record explicitly: (1) demand confirmed /
  deferred; (2) sibling architecture confirmed; (3) stdlib-only
  viability via hand-written keccak; (4) `eth-signer-mcp` EIP-712
  gap explicitly named, with v1 path (Path B) recommended and Path A
  as v2; (5) full draft PRD skeleton lands at
  `plan/eth-tx-builder-erc20-permit/prd.md`; (6) **no `permit` code
  lands in this Phase 3** regardless of demand outcome — Issue 3.7
  is the deferred-placeholder tracking issue.
- Watch out for: the temptation to start implementing `permit`
  "while I'm thinking about it." Don't — the spike is paper-only and
  the entire implementation is owned by the fresh PRD's own phase
  plan per project plan DL-10.
- Watch out for: the keccak256 hand-write claim. Test vectors and
  byte-exact verification are needed before the spike can confidently
  say "stdlib-only is viable." If the spike-writer can't validate
  keccak in <1 day, the recommendation flips to "dep needed, fresh
  PRD must include the dep conversation."
- Watch out for: the EIP-712 domain separator. `permit` involves a
  per-token domain separator that includes the token's `name()` and
  `version()` — both potentially missing from non-compliant tokens.
  The PRD's P0 must record this as a research item.
- Watch out for: confusing this spike's draft PRD with a final PRD.
  The skeleton is a first pass; it gets iterated under its own
  planning workflow before code starts.
- New files to create: ADR appended to `architecture.md`; **full
  draft PRD skeleton (NOT conditional)** at
  `plan/eth-tx-builder-erc20-permit/prd.md`.

**Acceptance Criteria:**
- [x] A new ADR (numbered `ADR-014`, dated, status either `Accepted`
      with greenlight or `Deferred` with no-go) is appended to
      `plan/eth-tx-builder-erc20/architecture.md` covering the five
      questions above with explicit "Status / Context / Decision /
      Alternatives / Consequences" sections matching the existing
      ADR house style.
- [x] **(Q1)** The ADR records the demand-check result (concrete
      workflow named, or "no demand surfaced").
- [x] **(Q2)** The ADR records the sibling-vs-extension decision with
      the concrete benefits / costs analysis.
- [x] **(Q3)** The ADR records the stdlib-only viability for EIP-712
      keccak, either confirming or flipping to "dep required."
- [x] **(Q4, new external-blocker resolution)** The ADR EXPLICITLY
      records the current `eth-signer-mcp` EIP-712 state ("not
      supported today, per `apps/eth-signer-mcp/README.md`
      §Out-of-scope"), AND records the chosen v1 path
      (Path A = signer enhancement PRD, or Path B = external EIP-712
      signer for v1 with signer integration as v2), AND records the
      reasoning. The ADR explicitly names this as the previously-
      undeclared external blocker; if Path A is chosen, the ADR
      points at the parallel signer-PRD slot.
- [x] **(Q5)** The full draft PRD skeleton at
      `plan/eth-tx-builder-erc20-permit/prd.md` exists with: real
      Overview section (1-2 paragraphs, not a placeholder); real
      Problem Statement section (operator pain framed); real Goals
      section (at least 2-3 specific success criteria); real
      Functional Requirements section broken into P0 / P1 / P2
      subsections (each with at least 2-3 user stories or feature
      bullets with acceptance-criterion stubs); real Out of Scope
      section (at minimum: ERC-3009; non-EIP-2612 permit variants;
      signer-side EIP-712 if Path B was chosen; EIP-1271 contract
      signatures).
- [x] The PRD skeleton exists EVEN IF the demand check concluded
      "defer indefinitely" — in that case its Status front-matter
      reads "Draft — awaiting demand surfacing" so Issue 3.7 has
      somewhere concrete to point at. The artifact is the source of
      truth for any future `permit` work.
- [x] The ADR cross-references PRD §P2.2, project plan DL-10 and
      Task 3.3, project plan R11 (cryptographic complexity), and
      `apps/eth-signer-mcp/README.md` §Out-of-scope.
- [x] **No code changes are committed in this issue.** No changes to
      `build_erc20.py`, `test_build_erc20.py`, `build_erc20_permit.py`
      (which does NOT yet exist), or any other code file.
- [x] Issue 3.7 (the deferred-placeholder tracking issue) can run
      next, regardless of greenlight / defer outcome. Issue 3.6
      always feeds into Issue 3.7.

**Testing Notes:**
- This is a paper spike; no automated tests. Reviewer-led check is
  the exit criterion.

---

### Issue 3.7: `permit` deferred placeholder — record deferral decision and fresh-PRD pointer (project plan DL-10)

- **Points:** 1
- **Type:** chore
- **Priority:** P2 (within Phase 3)
- **Blocked by:** 3.6
- **Blocks:** — (Issue 3.8's e2e does NOT include `permit`; this issue ships no testable code)
- **Scope:** <0.5 day

**Description:**

Implementation of the EIP-2612 `permit` builder is **OUT OF SCOPE for
this phase**, per project plan DL-10's "fresh PRD before fresh phase
plan before code" cascade. This issue is a tiny deferred-placeholder /
tracking issue whose sole purpose is to record the deferral decision
clearly so future operators don't re-litigate it from scratch.

The deferral logic:
1. Project plan DL-10 explicitly defers `permit` to its own future
   PRD. The PRD is drafted in Issue 3.6.
2. The previously-planned 5-pt "Optional `permit` builder
   (`build_erc20_permit.py`)" issue violated the project's ≤3pt
   estimation rubric, and its acceptance criteria depended on a PRD
   that does not yet exist (a non-testable AC).
3. The correct posture is: this Phase 3 produces the spike + draft
   PRD; ANY actual `permit` implementation runs under its own
   fresh PRD + phase plan as a sibling `build_erc20_permit.py`.
4. If (and only if) Issue 3.6 greenlights the spike, the fresh PRD
   gets fleshed out and an `eth-tx-builder-erc20-permit` phase plan
   gets drafted under its own planning workflow — NOT here.

This issue records that posture as a one-stop deferral note so future
sessions don't waste cycles asking "wait, did Phase 3 try to ship
`permit`?" The answer is: no, Phase 3 produced the spike + PRD
skeleton, and the deferral is intentional per DL-10.

**Implementation Notes:**

- Files likely affected (ONE of the two, not both):
  - `plan/eth-tx-builder-erc20-permit/prd.md` — add a Status /
    Lifecycle section near the top of the draft PRD (created by
    Issue 3.6) recording: (a) this issue's deferral decision; (b)
    pointer to project plan DL-10; (c) pointer to ADR-014; (d) the
    statement "implementation is OUT OF SCOPE for `plan/
    eth-tx-builder-erc20` Phase 3 — any `build_erc20_permit.py` code
    ships under THIS PRD's own phase plan."
  - **OR** — if the planner prefers to keep the deferral note in the
    `eth-tx-builder-erc20` architecture stream rather than the new
    PRD — append a brief ADR (`ADR-015: Phase 3 defers permit
    implementation to its own PRD per DL-10`) to
    `plan/eth-tx-builder-erc20/architecture.md` with the same
    content. ADR is preferred if Issue 3.6's PRD draft is unstable
    (Status = "Draft — awaiting demand surfacing").
- Approach:
  - This issue ships **no code**. Zero changes to `build_erc20.py`,
    `test_build_erc20.py`, SKILL.md, README.md, or any source file.
  - The deferral note is 3-5 sentences. Keep it terse — the goal is
    to be findable, not to repeat the spike's full reasoning.
- Key decisions: (a) the deferral is intentional, not an oversight;
  (b) implementation never lands in `plan/eth-tx-builder-erc20/`
  Phase 3 — only in the fresh PRD's own phase plan; (c) the artifact
  recording the deferral is either the draft PRD (preferred) or a
  small ADR; (d) Issue 3.8's e2e does NOT cover `permit` at all.
- Watch out for: the temptation to "while we're here, write a
  scaffold." Don't — a scaffold is code, and code belongs in the
  fresh PRD's phase plan. Even the simplest stub creates an
  unmaintained `build_erc20_permit.py` that future planners have to
  decide whether to keep or delete.
- Watch out for: re-opening the demand-check question. Issue 3.6 is
  the place for that. This issue only records the answer.
- New files to create: none (the draft PRD already exists from
  Issue 3.6, or an ADR is appended to the existing
  `architecture.md`).

**Acceptance Criteria:**
- [x] The deferral decision is recorded in EXACTLY ONE place: either
      the draft PRD at `plan/eth-tx-builder-erc20-permit/prd.md`
      (preferred) or as `ADR-015` appended to
      `plan/eth-tx-builder-erc20/architecture.md`. Not both.
      → Recorded in `plan/eth-tx-builder-erc20-permit/prd.md`
      §Status/Lifecycle (PRD location chosen, not ADR-015).
- [x] The recorded note explicitly states: "Implementation of
      EIP-2612 `permit` is OUT OF SCOPE for `plan/
      eth-tx-builder-erc20` Phase 3, per project plan DL-10. If
      Issue 3.6's ADR-014 greenlit implementation, the actual
      `build_erc20_permit.py` ships under the
      `eth-tx-builder-erc20-permit` PRD's own phase plan as a
      sibling helper, NOT under this PRD."
- [x] The recorded note points at: project plan DL-10; ADR-014
      (from Issue 3.6); the draft PRD at
      `plan/eth-tx-builder-erc20-permit/prd.md`.
- [x] **No code is committed in this issue.** No changes to
      `build_erc20.py`, `test_build_erc20.py`, SKILL.md, README.md,
      or any source file. No new file `build_erc20_permit.py` is
      created. No new file `test_build_erc20_permit.py` is created.
- [x] Existing test suites still pass (because nothing changed):
      `python3 -m unittest test_build_erc20 -v` and
      `python3 -m unittest test_build_send_eth -v` both pass
      unmodified.
- [x] Issue 3.8's e2e plan does NOT include a `permit` row (Phase 3
      ships no `permit` code, so there's no e2e).

**Testing Notes:**
- No automated tests; this issue ships no code. Reviewer-led check
  is the exit criterion: confirm the deferral note is recorded and
  findable from project plan DL-10.

---

### Issue 3.8: Phase 3 manual e2e + commit on develop

- **Points:** 1
- **Type:** chore
- **Priority:** P1 (within Phase 3 — closes Phase 3)
- **Blocked by:** every Phase 3 feature issue that landed (any subset of
  3.2, 3.4), plus 3.5 (docs). Issues 3.6 / 3.7 are paper-only and
  produce no e2e — they are NOT in this issue's blocked-by list.
- **Blocks:** —
- **Scope:** 0.5-1 day

**Entry criteria / assumptions (record explicitly to avoid hidden
external dependencies):**

- **`eth-rpc` generic `call` passthrough is the post-state verifier
  for `--revoke`.** This issue's `--revoke` e2e reads
  `allowance(sender, spender)` on hoodi after broadcast to confirm
  the allowance is zero. The sibling `eth-rpc` skill ships a generic
  `call` passthrough that can issue an arbitrary `eth_call` with
  hand-encoded calldata (per the eth-rpc Phase 1 plan, which is
  currently implementation-ready per repo memory and the
  `feat/eth-rpc-phase-2` branch's recent commits). The calldata for
  `allowance(address,address)` is the well-known 4-byte selector
  `0xdd62ed3e` followed by two 32-byte zero-padded addresses. The
  e2e invokes `eth-rpc call --network hoodi --to <token> --data
  0xdd62ed3e<owner_padded><spender_padded>` and asserts the 32-byte
  return is all-zeros.
- **Assumption A1:** the `eth-rpc` `call` op accepts hand-encoded
  hex calldata via `--data <hex>` (or equivalent). **Verify before
  starting the e2e.** Read the eth-rpc Phase 1 issue file
  (`plan/eth-rpc-extension/issues/01-phase-1.md` or equivalent) to
  confirm the `call` op's CLI shape. If confirmed, no scope change
  needed.
- **Assumption A2 (fallback if A1 is wrong):** if the `eth-rpc`
  `call` op does NOT accept hand-encoded calldata, this issue
  scopes in a tiny verifier — a 10-15 line Python one-shot script
  (`plan/eth-tx-builder-erc20/research/04-revoke-allowance-verifier.py`
  or a snippet pasted directly into the README e2e capture) that
  encodes the `allowance` selector + args and submits an `eth_call`
  via `eth-rpc`'s lowest-level RPC passthrough (`eth-rpc rpc
  eth_call ...`) or via a direct `curl` to the hoodi JSON-RPC
  endpoint. This is small (well under the 1-point budget) so it
  fits in this issue without splitting.
- **Why state this explicitly:** before this revision the
  `allowance` post-state verification step silently depended on
  `eth-rpc` features that may or may not exist. Naming the
  assumption (and the fallback) makes the dependency visible so a
  future operator running the e2e doesn't hit a surprise.

**Description:**

Run the manual e2e against hoodi for every Phase 3 feature that
shipped, capture the output verbatim in the README, and commit the
combined Phase 3 work on `develop`. Matches Phase 1's e2e pattern
(project plan DL-6) and respects the repo memory that `develop` is the
integration branch.

**Implementation Notes:**

- Files likely affected:
  - `.claude/skills/eth-tx-builder/README.md` — append the Phase 3
    manual-e2e output verbatim under the existing "Manual end-to-end
    (hoodi)" section (a Phase 3-specific subsection keeps the file
    structure readable).
  - No `build_erc20.py` / `test_build_erc20.py` changes in this
    issue.
- Approach (run only the steps for features that shipped):
  - **`--revoke` (Issue 3.2 shipped):**
    ```bash
    cd .claude/skills/eth-tx-builder
    python3 build_erc20.py approve \
      --network hoodi \
      --token <Phase 1 e2e token addr> \
      --spender <Phase 1 e2e spender addr> \
      --revoke \
      --sender <Phase 1 e2e sender addr>
    ```
    Confirm: (a) helper exits 0; (b) stderr summary shows
    `operation: revoke` and the multi-line revoke confirmation block
    (per Issue 3.2); (c) stdout JSON pastes into `eth-signer-mcp`
    `sign_transaction` and signs; (d) when broadcast (via `eth-rpc`'s
    `broadcast` op, or any RPC client), the transaction executes
    successfully on-chain AND sets the on-chain allowance to 0
    (verify by reading `allowance(sender, spender)` via `eth-rpc`
    after broadcast — `allowance == 0`).
    **Post-state verification details (per Assumption A1 above):**
    use `eth-rpc`'s generic `call` op with hand-encoded
    `allowance(address,address)` calldata. Selector is `0xdd62ed3e`;
    the two arg words are `<owner>` and `<spender>` zero-left-padded
    to 32 bytes. Expected return: 32-byte all-zeros (`0x000...000`).
    If `eth-rpc call --data <hex>` is not available (Assumption A2
    fallback fires), use the tiny verifier script noted in the entry
    criteria above. Capture the literal output in README.
  - **Polished `bytes32` decode (Issue 3.4 shipped):**
    If a known legacy-format token exists on hoodi (unlikely — most
    legacy bytes32 tokens are mainnet-only), the e2e is to run a
    `transfer` of 0 base units against the token and confirm the
    summary shows the decoded ticker. If no such token exists on
    hoodi, the e2e is **mainnet read-only**: a `transfer` build
    against an MKR or similar mainnet token with `--amount 0` to
    confirm `decimals()` + `symbol()` read without broadcasting.
    Capture either way in README.
  - **`permit` (Issues 3.6 / 3.7): NO e2e.** Phase 3 ships no
    `permit` code (Issue 3.6 is paper-only — ADR + draft PRD;
    Issue 3.7 is the deferred-placeholder note). Any actual `permit`
    e2e runs under the fresh `eth-tx-builder-erc20-permit` PRD's own
    phase plan, NOT here. The Phase 3 commit message references
    ADR-014 (the go/no-go decision) but no `permit` calldata or
    typed-data is produced.
  - Commit on `develop` with a message referencing the PRD,
    architecture (with the new ADR numbers — ADR-012 / ADR-013, and
    ADR-014 if Issue 3.6 ran, and ADR-015 if Issue 3.7 chose the
    ADR path over the draft-PRD-edit path), and the Phase 3 issues
    that landed. Do **not** PR or merge to `main` unprompted.
- Watch out for: any e2e failure here is a release blocker for the
  feature in question, not a "tweak and ship" — Phase 3 features
  rolled back from this issue must also be rolled back from
  Issue 3.5's docs.
- Watch out for: the `--revoke` e2e is the highest-stakes — it
  changes on-chain state. Use a low-value spender (e.g. a fresh
  test address) for the e2e to avoid disturbing real workflows.
- Watch out for: re-confirming Phase 1's on-chain success. Phase 3
  is additive; Phase 1's `transfer` / `approve --amount` /
  `transfer-from` e2e MUST still pass. If they don't, Phase 3
  introduced a regression that needs to be tracked back to a
  specific issue and rolled back.
- New files to create: none.

**Acceptance Criteria:**
- [ ] Every Phase 3 feature that shipped (subset of `--revoke`,
      polished `bytes32` decode) has a successful manual e2e
      captured in README, with the literal CLI invocation and
      the literal output.
- [ ] If `--revoke` shipped: post-broadcast `allowance(sender,
      spender) == 0` is verified on hoodi via `eth-rpc`'s generic
      `call` op with hand-encoded `allowance(address,address)`
      calldata (selector `0xdd62ed3e`). The verification command
      and the 32-byte all-zeros return are captured in README.
      If Assumption A1 turned out false, the fallback verifier
      from Assumption A2 is captured instead, with a note
      explaining which path was taken.
- [ ] If polished `bytes32` decode shipped: at least one
      manual-readback confirms a legacy-format ticker decodes
      correctly (mainnet read-only acceptable if no such token
      exists on hoodi).
- [ ] **No `permit` e2e is run** — Issues 3.6 / 3.7 ship no
      `permit` code, so there is nothing to e2e. The Phase 3
      commit message references ADR-014 but does NOT claim
      `permit` was tested.
- [ ] Phase 1's three manual e2e ops (`transfer`, `approve
      --amount`, `transfer-from`) STILL pass on hoodi after the
      Phase 3 commits — re-run as a regression check and capture
      a "Phase 1 regression OK" note in README.
- [ ] `cd .claude/skills/eth-tx-builder && python3 -m unittest
      test_build_erc20 -v` is still green after the docs commit.
- [ ] `cd .claude/skills/eth-tx-builder && python3 -m unittest
      test_build_send_eth -v` is still green after the docs commit
      (Phase 3 does not touch v1).
- [ ] The combined Phase 3 work is committed on `develop` with a
      message that references the PRD (`plan/eth-tx-builder-erc20/
      prd.md`), project plan, and ADRs added in this phase
      (ADR-012 / ADR-013, and ADR-014 if Issue 3.6 ran, and
      ADR-015 if Issue 3.7 chose the ADR path over the
      draft-PRD-edit path). Issue 3.6's new draft PRD at
      `plan/eth-tx-builder-erc20-permit/prd.md` is also part of the
      commit if it landed.
- [ ] No commit / PR / merge targets `main`.

**Testing Notes:**
- Manual e2e is the only network-touching step in Phase 3 (apart
  from the Phase 1 regression re-run). Unit tests stay mocked.
- If hoodi is unreachable at the time of the e2e, the `--revoke`
  e2e MUST wait — `--revoke` cannot be e2e'd against mainnet
  without burning real allowance state, which is a separate operator
  decision. Defer the e2e and the Phase 3 commit; do not fall back
  to mainnet for this issue.
- **Validate the `eth-rpc` `call` op CLI shape (Assumption A1) at
  the START of the e2e**, not after the broadcast. If A1 is wrong,
  fall back to A2 (the tiny verifier script or `curl` snippet)
  BEFORE running the broadcast — the post-state verification must
  work, and the operator should not learn this 5 minutes after
  irreversibly setting an on-chain allowance.
- The Phase 1 regression re-run is the single best protection
  against silent Phase 3 regressions. Don't skip it even when
  pressed for time.
