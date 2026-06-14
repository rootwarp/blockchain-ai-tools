# Phase 2: P1 — balanceOf pre-check, approve-race guard, sepolia/holesky, --summary-only

## Phase Overview

- **Goal:** Ship the four PRD §P1 follow-ups as independently-shippable
  enhancements on a green Phase 1: `balanceOf(sender)` soft-check for `transfer`;
  approve-race (non-zero → non-zero) soft-check for `approve`; add `sepolia` and
  `holesky` to `build_send_eth.NETWORKS` (relaxing the P0 freeze on v1); and
  `--summary-only` dry-run mode. Each ships independently; none gates another.
- **Issue count:** 10 issues, 20 total points.
- **Estimated duration:** ~12–13 working days (single-stream, single-developer).
- **Entry criteria:**
  - Phase 1 exit criteria all met (PRD success metrics §1–§5).
  - `python3 -m unittest test_build_send_eth -v` green on the v1 file.
  - `python3 -m unittest test_build_erc20 -v` green on the new helper.
  - `build_erc20.py` is structured per architecture's seven-section layout
    (Layer 1: `abi_codec`, `amount_codec`; Layer 2: `contract_reads`,
    `gas_estimator`, `summary`; Layer 3: `tx_assembly`; Layer 4: `cli_dispatch`).
  - Hoodi manual e2e for all three Phase 1 ops complete and recorded in README.
  - `build_erc20.py`'s `_build_parser` uses `sorted(_core.NETWORKS)` for
    `--network` choices (Phase 1 deliverable); if it hardcoded a list, 2.6
    becomes a one-line code-fix instead of test-only.
  - **Phase 1 e2e TxRequest JSON fixtures captured (pre-Phase-2 baseline).**
    Before any Phase 2 code lands, run each of the three Phase 1 hoodi e2e
    invocations (`transfer`, `approve`, `transfer-from`) and save the stdout
    JSON to fixture files outside the test tree (e.g.
    `.claude/skills/eth-tx-builder/fixtures/phase1-baseline-<op>.json`). These
    fixtures are the ground truth Issue 2.8c diffs against to prove Phase 2
    is additive. Capturing them after Phase 2 work has begun defeats the
    point — they MUST be captured at this entry point.
- **Exit criteria:**
  - All four P1 features land on `develop` (or are explicitly dropped with a
    note); each one independently green per the per-task gates below.
  - `python3 -m unittest test_build_send_eth -v` green (regression check —
    including the new sepolia/holesky cases added in Task 2.3).
  - `python3 -m unittest test_build_erc20 -v` green (new and existing tests).
  - `SKILL.md` and `README.md` updated for any new flag, network, or warning
    surface.
  - The Phase 1 hoodi manual e2e still works as documented (the four Phase 2
    changes are additive; none alters Phase 1 happy-path JSON).
- **Assumptions (recorded here per phase instructions):**
  - **Estimation scale.** 1 (trivial) / 2 (small) / 3 (medium) / 5 (large). Every
    issue is 1–3 points and completable in 1–2 days; nothing is 5+ in this phase.
  - **Single-stream, single-developer.** Issues are sequenced for one
    code-writer; no Stream A/B, no file-ownership map.
  - **Phase 2 freeze relaxation.** Only Issue 2.5 (`sepolia`/`holesky`) edits
    `build_send_eth.py` and `test_build_send_eth.py`; all other issues keep v1
    untouched. The relaxation is scoped: the dict + matching test cases only,
    per architecture Assumption 16 and Open Question 1.
  - **Soft-check posture is consistent.** New `balanceOf` and `approve`-race
    checks mirror the Phase 1 `transfer-from` allowance soft-check pattern —
    one local `try/except _core.RPCError` inside `do_*`, on RPC failure queue a
    `*_check_skipped` warning, on threshold violation queue a `low_*` /
    `approve_race` warning. The `gas_estimator` no-`try/except` invariant is
    preserved.
  - **Test layout follows ADR-011.** New tests for soft-checks land in
    `TestContractReads` (read function), `TestTxAssembly` (warning queueing),
    `TestCliDispatch` (end-to-end stderr / exit-code shape). `TestSummary`
    gains the new warning helpers. New tests in `test_build_send_eth.py` for
    Issue 2.5 mirror the existing `TestNetworkConfig` style.
  - **`--summary-only` is a per-subparser flag** (Open Question 7). Each of
    `transfer` / `approve` / `transfer-from` accepts `--summary-only` so the
    flag composes with every existing arg combination. The architecture explicitly
    allows either shape; per-subparser is chosen because it keeps argparse
    discovery local and avoids a top-level pre-subcommand parser-of-parsers.
  - **Sequencing.** The first two issues (`balanceOf` + `approve`-race) are
    grouped at the start because they live in the same architecture sections
    (`contract_reads`, `summary`, `tx_assembly`); doing them back-to-back
    minimizes context switching. Issue 2.5 (v1 edit) is scheduled in the middle
    so the freeze-relaxation work has room before the docs sweep. Issue 2.8
    (final docs + tests sweep + regression) closes the phase.
  - **Approve-race opt-in.** If real operator use surfaces warning fatigue
    (every USDT approve trips it), Issue 2.4 ships an opt-out via a stderr-only
    note rather than reverting Phase 2 in bulk; the warning is data-tuple based
    (architecture ADR-004) so adjusting it is local.

## Phase Summary

| Issue | Title | Points | Blocked by | Scope | Files |
|-------|-------|--------|------------|-------|-------|
| 2.1 | balanceOf read in contract_reads (encoder + decoder + fetch + tests) | 2 | — | 1–2 days | `build_erc20.py`, `test_build_erc20.py` |
| 2.2 | balanceOf soft-check wired into do_transfer + warnings + tests | 2 | 2.1 | 1 day | `build_erc20.py`, `test_build_erc20.py` |
| 2.3 | Refactor allowance soft-check into helper (parameterized trigger); reuse for approve race | 2 | — | 1 day | `build_erc20.py`, `test_build_erc20.py` |
| 2.4 | approve-race guard wired into do_approve (consume 2.3 helper unmodified) + warnings + tests | 2 | 2.3 | 1–2 days | `build_erc20.py`, `test_build_erc20.py` |
| 2.5 | Add sepolia + holesky to v1 NETWORKS + v1 tests | 2 | — | 1 day | `build_send_eth.py`, `test_build_send_eth.py` |
| 2.6 | Verify ERC-20 helper picks up new networks (test-only, scoped) | 1 | 2.5 | 0.5 day | `test_build_erc20.py` |
| 2.7 | `--summary-only` flag in cli_dispatch + tests | 2 | — | 1 day | `build_erc20.py`, `test_build_erc20.py` |
| 2.8a | Docs sweep (SKILL.md + README.md) for all Phase 2 features | 2 | 2.2, 2.4, 2.6, 2.7 | 1–2 days | `SKILL.md`, `README.md` |
| 2.8b | Cross-feature regression test matrix (warnings × ops; network choices table-driven) | 1 | 2.2, 2.4, 2.6, 2.7 | 1 day | `test_build_erc20.py` |
| 2.8c | Manual hoodi e2e re-run + Phase-1 byte-identical fixture diff + commit on `develop` | 1 | 2.2, 2.4, 2.6, 2.7, 2.8a, 2.8b | 1 day | `README.md`, fixture diffs, git |

**Phase 2 total:** 10 issues, 20 points.

## Phase Execution Plan

Single-stream; one code-writer working sequentially. Days are nominal 1-day work
slots.

| Day | Issue |
|-----|-------|
| 1 | 2.1 balanceOf read (encoder + decoder + fetch + tests) |
| 2 | 2.1 cont. |
| 3 | 2.2 balanceOf soft-check in do_transfer + warning + tests |
| 4 | 2.3 Extract allowance soft-check helper (parameterized trigger) |
| 5 | 2.4 approve-race guard in do_approve + warning + tests |
| 6 | 2.4 cont. |
| 7 | 2.5 Add sepolia + holesky to v1 NETWORKS + v1 tests |
| 8 | 2.6 Verify ERC-20 helper picks up new networks |
| 9 | 2.7 `--summary-only` flag + tests |
| 10 | 2.8a Docs sweep (SKILL.md + README.md) |
| 11 | 2.8a cont. + 2.8b cross-feature regression matrix |
| 12 | 2.8c hoodi e2e re-run + Phase-1 fixture diff + commit on develop |

---

## Issues

### Issue 2.1: Add `balanceOf` read primitives (encoder + decoder + fetch) to `contract_reads`

- **Points:** 2
- **Type:** feature
- **Priority:** P1
- **Blocked by:** none (only Phase 1 exit)
- **Blocks:** Issue 2.2
- **Scope:** 1–2 days

**Description:**
Add the `balanceOf(address)` read primitives to `build_erc20.py` so subsequent
work (Issue 2.2) can soft-check the sender's balance before emitting a
`transfer` JSON. This issue lands only the encoder, decoder, fetcher and their
unit tests — no `do_*` change, no warning surface, no CLI change. Splitting the
read primitive from the wiring keeps the diff focused and the regression risk
local.

**Implementation Notes:**
- Files likely affected:
  - `.claude/skills/eth-tx-builder/build_erc20.py` (Layer 1 `abi_codec`
    additions; Layer 2 `contract_reads` addition).
  - `.claude/skills/eth-tx-builder/test_build_erc20.py` (extend
    `TestAbiCodec` and `TestContractReads`).
- In Layer 1 `abi_codec` section:
  - Add `SEL_BALANCE_OF = "0x70a08231"` constant with the one-line derivation
    comment `# keccak256("balanceOf(address)")[:4]` (matches the existing
    selector-constant style, ADR-005).
  - Add `encode_balance_of_call(holder: str) -> str` that returns
    `_pack_call(SEL_BALANCE_OF, _encode_address(holder))`. Mirrors the existing
    `encode_allowance_call` shape (single-address arg).
  - Add `decode_balance(hex_result: str) -> int` returning
    `_core.parse_hex_int(hex_result)` (a single 32-byte uint256 word, exactly
    like `decode_allowance`).
- In Layer 2 `contract_reads` section:
  - Add `fetch_balance_of(rpc, url, token, holder) -> int` that calls
    `eth_call` with `encode_balance_of_call(holder)` against `"latest"`, passes
    the result through `decode_balance`, and **propagates `_core.RPCError`**
    (this is a read whose soft-check posture is the caller's job — same shape
    as `fetch_allowance`, per ADR-006).
- Watch out for: do **not** add a `try/except` around the RPC call inside
  `fetch_balance_of`. The soft-check posture lives in Issue 2.2's `do_transfer`
  call site, not in the read primitive (architecture ADR-006).
- New files to create: none.
- Files NOT to modify: `build_send_eth.py`, `test_build_send_eth.py`,
  `SKILL.md`, `README.md` (saved for the Issue 2.8 docs sweep).

**Acceptance Criteria:**
- [ ] `SEL_BALANCE_OF == "0x70a08231"` defined as a module-level constant in
      Layer 1 `abi_codec` with the keccak derivation comment.
- [ ] `encode_balance_of_call("0x" + "00"*20)` returns
      `"0x70a08231" + "00"*32` (selector + zero-padded address word).
- [ ] `encode_balance_of_call` round-trips against a known-good calldata vector
      for a mixed-case address (output is lowercase, left-padded to 64 hex chars).
- [ ] `decode_balance("0x" + "0"*63 + "a")` returns `10`; `decode_balance("0x" +
      "f"*64)` returns `2**256 - 1`.
- [ ] `fetch_balance_of` calls `rpc("eth_call", [{...}, "latest"])` once with
      the correct `to=token` and `data` payload; verified via mocked rpc.
- [ ] `fetch_balance_of` returns the decoded balance for a mocked rpc return of
      `"0x...06"` → `6`.
- [ ] `fetch_balance_of` **propagates `_core.RPCError`** without catching when
      the mocked rpc raises (asserted via `with self.assertRaises(_core.RPCError):`).
- [ ] `TestAbiCodec` gains: selector-constant equality check; encoder bit-pattern
      check; decoder vectors (0, 10, max uint256).
- [ ] `TestContractReads` gains: happy-path with mocked rpc; RPCError
      propagation case.
- [ ] `python3 -m unittest test_build_erc20.TestAbiCodec test_build_erc20.TestContractReads -v` green.
- [ ] `python3 -m unittest test_build_send_eth -v` still green (regression: no
      v1 file touched).

**Testing Notes:**
- Use the same `make_fake_rpc` helper Phase 1 duplicated into
  `test_build_erc20.py` (architecture Assumption 14 / Phase 1 Task 1.8). Mock
  the `eth_call` response as a hex string and assert the call payload.
- Cross-check the selector value against the canonical signature
  `balanceOf(address)`; the constant `0x70a08231` matches the value used by
  every major ERC-20 implementation and is the value research §01 lists.

---

### Issue 2.2: Wire `balanceOf` soft-check into `do_transfer` (warnings + tests)

- **Points:** 2
- **Type:** feature
- **Priority:** P1
- **Blocked by:** Issue 2.1
- **Blocks:** Issue 2.8 (docs sweep references this UX surface)
- **Scope:** 1 day

**Description:**
Add a non-fatal `balanceOf(sender)` pre-check to `do_transfer` so the operator
gets a loud warning when their sender balance is below the requested amount.
Mirrors the existing `transfer-from` allowance soft-check posture exactly
(warn, don't block; build JSON regardless). Adds two new warning kinds —
`low_balance` and `balance_check_skipped` — to the `summary` section.

**Implementation Notes:**
- Files likely affected:
  - `.claude/skills/eth-tx-builder/build_erc20.py` (Layer 2 `summary`
    additions; Layer 3 `tx_assembly.do_transfer` additions).
  - `.claude/skills/eth-tx-builder/test_build_erc20.py` (extend `TestSummary`,
    `TestTxAssembly`, `TestCliDispatch`).
- In Layer 2 `summary` section, add:
  - `warn_low_balance(holder, current, requested, decimals, symbol) -> None` —
    emits a single-line `WARNING:` to stderr naming the holder, the current
    balance (with `base_units_to_human` rendering for readability), and the
    requested amount. Wording sketch:
    `WARNING: sender balance is <CUR> (<HUMAN_CUR> <SYM>); requested transfer
    is <REQ> (<HUMAN_REQ> <SYM>). This transaction will revert unless balance
    is funded before broadcast.`
  - `warn_balance_check_skipped(reason) -> None` — emits
    `WARNING: balanceOf pre-check skipped: <reason>. Build continues.`
  - Extend `emit_warning(kind, payload)` dispatcher to handle the new
    `"low_balance"` and `"balance_check_skipped"` kinds (per ADR-004's
    serializable-warning pattern).
- In Layer 3 `tx_assembly.do_transfer`, after computing `amount_base` and
  the calldata, **before** `estimate_gas`:
  - Wrap `fetch_balance_of(rpc, url, token, sender)` in a local
    `try/except _core.RPCError as e:`. This is the **second** allowed
    `try/except RPCError` outside `cli_dispatch.main()` (the first is the
    allowance soft-check in `do_transfer_from`, established in Phase 1). It
    is scoped strictly to the balance read — it must NOT wrap `estimate_gas`
    or any other call.
  - On `RPCError` → `warnings.append(("balance_check_skipped", {"reason": str(e)}))`.
  - On success: if `balance < amount_base` →
    `warnings.append(("low_balance", {"holder": sender, "current": balance,
    "requested": amount_base, "decimals": decimals, "symbol": symbol}))`.
  - Otherwise → no warning.
- The `gas_estimator.estimate_gas` call must remain untouched — no
  `try/except` around it (ADR-007 invariant).
- Watch out for: the soft-check fires only on `do_transfer`, NOT on
  `do_approve` or `do_transfer_from` (those have their own existing /
  forthcoming checks). Adding it to all three ops at once would be scope creep.
- New files to create: none.
- Files NOT to modify: `build_send_eth.py`, `test_build_send_eth.py`,
  `SKILL.md`, `README.md`.

**Acceptance Criteria:**
- [ ] `do_transfer` happy path with sender balance ≥ requested → returns
      `warnings_list == []` (asserted on mocked rpc).
- [ ] `do_transfer` with `fetch_balance_of` returning `balance < amount_base`
      → `warnings_list` contains exactly one `("low_balance", {"holder":...,
      "current":..., "requested":..., "decimals":..., "symbol":...})` tuple;
      JSON `tx_dict` is still built and returned.
- [ ] `do_transfer` with `fetch_balance_of` raising `_core.RPCError` →
      `warnings_list` contains exactly one `("balance_check_skipped",
      {"reason": "<msg>"})` tuple; JSON `tx_dict` is still built.
- [ ] `do_transfer` with `estimate_gas` raising `_core.RPCError` still
      propagates (no JSON, fatal — the ADR-007 invariant). The new balance
      `try/except` does not catch this.
- [ ] `do_transfer` with `fetch_decimals` raising still propagates (FATAL),
      and `fetch_balance_of` is NOT called (short-circuit per Open Question 5
      style).
- [ ] `TestSummary` gains: `warn_low_balance` emits a `WARNING:` line containing
      the holder address; `warn_balance_check_skipped` emits a `WARNING:` line
      containing the reason; both write to stderr.
- [ ] `TestTxAssembly` gains: the four `do_transfer` cases above (happy, low
      balance, RPC skipped, decimals fatal).
- [ ] `TestCliDispatch` gains: `main()` with mocked rpc returning low balance
      → exit 0, `WARNING:` on stderr, JSON on stdout (parses cleanly).
- [ ] `python3 -m unittest test_build_erc20 -v` green.
- [ ] `python3 -m unittest test_build_send_eth -v` green (regression).

**Testing Notes:**
- Use the same mocked-rpc pattern as Phase 1's `TestTxAssembly`. Each test sets
  up the mock to return canned `eth_call` responses in order: `decimals` →
  `symbol` → `balanceOf` → `estimate_gas` → `getTransactionCount` →
  `getBlockByNumber` → `maxPriorityFeePerGas`.
- For the "balance check skipped" test, use `side_effect` with a function that
  raises `_core.RPCError` only on the `balanceOf` call and returns canned
  values for the others.

---

### Issue 2.3: Extract allowance soft-check into a small helper (parameterized trigger); reuse for the approve race

- **Points:** 2
- **Type:** chore (refactor)
- **Priority:** P1
- **Blocked by:** none
- **Blocks:** Issue 2.4
- **Scope:** 1 day

**Description:**
Extract the Phase 1 `do_transfer_from` allowance soft-check pattern
(`try fetch_allowance; except RPCError → queue skipped warning; else if
trigger(current, requested) → queue low warning`) into a small Layer 3 internal
helper so Issue 2.4 can call the same primitive for the approve-race guard
**without duplicating the `try/except` pattern AND without modifying the
helper's signature.** Pure refactor — no behavior change for `do_transfer_from`;
Phase 1 tests must remain green.

**Contract pin (load-bearing for 2.4):** the helper's API is **frozen by this
issue**. It accepts a parameterized `trigger` callable so the approve-race
check in Issue 2.4 can supply a different predicate (`lambda cur, req: cur != 0
and cur != req`) without ever editing this helper's signature or body. Issue
2.4 is a pure consumer; if 2.4 needs to change this helper, that is a signal
something is wrong with this issue's design and the change must be folded back
into 2.3 before 2.4 ships.

**Implementation Notes:**
- Files likely affected:
  - `.claude/skills/eth-tx-builder/build_erc20.py` (Layer 3 `tx_assembly`
    additions; `do_transfer_from` rewired to call the new helper).
  - `.claude/skills/eth-tx-builder/test_build_erc20.py` (extend `TestTxAssembly`
    with direct helper tests; existing `do_transfer_from` tests must still pass
    unmodified).
- Add a helper at the top of Layer 3 `tx_assembly` (before the `do_*`
  functions). The signature is **pinned** — Issue 2.4 must consume this
  unmodified:
  ```
  def _soft_check_allowance(rpc, url, token, holder, spender,
                            requested, skipped_kind, low_kind,
                            low_payload_extra=None,
                            trigger=None):
      """Read allowance; return zero or one (kind, payload) warning tuple.

      - On RPCError → returns [(skipped_kind, {"reason": str(e)})].
      - On trigger(current, requested) is False → returns [].
      - On trigger(current, requested) is True → returns [(low_kind, {...})].

      `trigger` is an optional callable `(current: int, requested: int) -> bool`.
      If None, defaults to `lambda cur, req: cur < req` (Phase 1
      `transfer-from` posture). Callers may pass a different predicate for
      different op semantics (e.g. approve-race uses
      `lambda cur, req: cur != 0 and cur != req`).

      Callers append the returned list to their own warnings_list.
      """
  ```
  - **`trigger` is the only knob 2.4 needs.** It replaces the hardcoded
    `current < requested` test inside the helper, so any caller can pass
    op-specific semantics WITHOUT editing the helper. The default keeps the
    Phase 1 `transfer-from` behavior byte-identical.
  - The helper takes `skipped_kind` / `low_kind` as parameters so the same
    routine drives both the `transfer-from` soft-check
    (kinds: `allowance_check_skipped`, `low_allowance`) and the upcoming
    `approve`-race check (kinds: `approve_race_check_skipped`, `approve_race`).
  - The `low_payload_extra` dict lets callers add op-specific fields (e.g. the
    new `approve_race` warning needs `spender` + existing-allowance — different
    from the `low_allowance` warning's `requested` field). Helper merges
    `low_payload_extra` into the payload before queueing.
- Rewire `do_transfer_from` to call:
  ```
  warnings.extend(_soft_check_allowance(
      rpc, url, token, holder=from_, spender=sender,
      requested=amount_base,
      skipped_kind="allowance_check_skipped",
      low_kind="low_allowance",
      low_payload_extra={"holder": from_, "spender": sender,
                          "decimals": decimals, "symbol": symbol},
      # trigger omitted → defaults to `cur < req` (Phase 1 byte-identical)
  ))
  ```
  The resulting warning tuple shape MUST exactly match Phase 1's so existing
  tests pass byte-for-byte.
- Watch out for:
  - The Phase 1 test for `do_transfer_from` low-allowance asserts the warning
    payload contains specific keys (`holder`, `spender`, `current`,
    `requested`, `decimals`). The refactor must preserve this contract — the
    helper's `low_payload` must include `current` (from the read) and
    `requested` (the requested base-unit amount) as the canonical fields, with
    `low_payload_extra` layered on top.
  - The helper is internal (`_` prefix) and lives in `tx_assembly` (Layer 3),
    NOT in `contract_reads` (Layer 2). It composes a Layer 2 read with a
    warning queue — that is a Layer 3 concern (architecture ADR-002).
  - **API freeze.** Once this issue lands, `_soft_check_allowance`'s signature
    is contract. Issue 2.4 (approve-race) must call it AS-IS — supplying a
    different `trigger` predicate, NOT editing the helper. Any change to the
    helper retroactively invalidates this issue's byte-identical AC.
- New files to create: none.
- Files NOT to modify: `build_send_eth.py`, `test_build_send_eth.py`,
  `SKILL.md`, `README.md`.

**Acceptance Criteria:**
- [ ] `_soft_check_allowance` defined in Layer 3 `tx_assembly` with the
      pinned signature above (including the optional `trigger` parameter
      defaulting to `cur < req`); placed before `do_transfer` / `do_approve` /
      `do_transfer_from`.
- [ ] `_soft_check_allowance` with default trigger and mocked rpc returning
      `allowance >= requested` → returns `[]`.
- [ ] `_soft_check_allowance` with default trigger and mocked rpc returning
      `allowance < requested` → returns a single-element list with the
      low-kind warning; payload contains `current`, `requested`, plus all
      `low_payload_extra` keys.
- [ ] `_soft_check_allowance` with a custom trigger
      `lambda cur, req: cur != 0 and cur != req` and mocked rpc returning
      `allowance = 5, requested = 10` → returns the low-kind warning (proves
      the trigger parameter is wired and Issue 2.4 can consume the helper
      unmodified).
- [ ] `_soft_check_allowance` with a custom trigger
      `lambda cur, req: cur != 0 and cur != req` and mocked rpc returning
      `allowance = 0` → returns `[]` (proves the trigger short-circuits the
      approve-race revocation case before 2.4 ships).
- [ ] `_soft_check_allowance` with mocked rpc raising `_core.RPCError` →
      returns a single-element list with the skipped-kind warning; payload
      contains `reason` equal to `str(e)`. (Behavior is independent of
      trigger.)
- [ ] `do_transfer_from` rewired to use the helper with the default trigger;
      all Phase 1 `TestTxAssembly` cases for `do_transfer_from` (happy, low
      allowance, RPC skipped) pass **unmodified**.
- [ ] `do_transfer_from` warning payload bytes-for-bytes identical to Phase 1:
      `("low_allowance", {"holder", "spender", "current", "requested",
      "decimals", "symbol"})` and `("allowance_check_skipped", {"reason"})`.
      This AC remains valid because Issue 2.4 is forbidden from editing the
      helper signature.
- [ ] `python3 -m unittest test_build_erc20 -v` green; no Phase 1 test modified.
- [ ] `python3 -m unittest test_build_send_eth -v` green (regression).

**Testing Notes:**
- This is a refactor; the strongest test is that **no Phase 1 test file lines
  change**. Add new direct unit tests for the helper itself in `TestTxAssembly`
  (e.g. `test_soft_check_allowance_low`,
  `test_soft_check_allowance_skipped`, `test_soft_check_allowance_ok`).
- If a Phase 1 test fails after the refactor, the helper signature is wrong —
  fix the helper, not the test.

---

### Issue 2.4: Wire approve-race guard into `do_approve` (warnings + tests)

- **Points:** 2
- **Type:** feature
- **Priority:** P1
- **Blocked by:** Issue 2.3
- **Blocks:** Issue 2.8 (docs sweep references this UX surface)
- **Scope:** 1–2 days

**Description:**
Add a non-fatal approve-race soft-check to `do_approve`: when emitting a new
non-zero `approve` (not `--approve-max`), read the current
`allowance(sender, spender)`; if it is non-zero AND not equal to the requested
amount, queue an `approve_race` warning naming the existing allowance and the
SWC-114 race window. On RPC failure, queue an `approve_race_check_skipped`
warning. Build still emits JSON unmodified.

**Implementation Notes:**
- Files likely affected:
  - `.claude/skills/eth-tx-builder/build_erc20.py` (Layer 2 `summary`
    additions; Layer 3 `do_approve` additions).
  - `.claude/skills/eth-tx-builder/test_build_erc20.py` (extend `TestSummary`,
    `TestTxAssembly`, `TestCliDispatch`).
- In Layer 2 `summary` section, add:
  - `warn_approve_race(holder, spender, current, requested, decimals, symbol) -> None`
    — emits a multi-line `WARNING:` to stderr explaining the race. Wording
    sketch:
    `WARNING: current allowance(<HOLDER>, <SPENDER>) is <CUR> (<HUMAN_CUR>
    <SYM>); requested approve is <REQ> (<HUMAN_REQ> <SYM>). The ERC-20
    "approve race" (SWC-114) lets the spender front-run this transaction to
    pull tokens at the OLD allowance and then again at the NEW. To eliminate
    the race, broadcast approve(<SPENDER>, 0) first, then this approve.`
  - `warn_approve_race_check_skipped(reason) -> None` — emits
    `WARNING: approve-race pre-check skipped: <reason>. Build continues.`
  - Extend `emit_warning` dispatcher for the two new kinds.
- In Layer 3 `do_approve`:
  - Only run the race check when `approve_max=False` AND `amount_base != 0`
    (a zero approve is a revocation, the race doesn't apply; an
    `--approve-max` approve already triggers the loud max-uint warning so the
    race note would be noise).
  - Call `_soft_check_allowance` from Issue 2.3 **unmodified**, supplying a
    custom `trigger` predicate for approve-race semantics:
    ```
    warnings.extend(_soft_check_allowance(
        rpc, url, token, holder=sender, spender=spender,
        requested=amount_base,
        skipped_kind="approve_race_check_skipped",
        low_kind="approve_race",
        low_payload_extra={"holder": sender, "spender": spender,
                            "decimals": decimals, "symbol": symbol},
        trigger=lambda cur, req: cur != 0 and cur != req,
    ))
    ```
  - **The trigger condition differs from `transfer-from`.** For the
    transfer-from low-allowance check, the trigger is the default
    `allowance < requested`. For the approve race, the trigger is
    `allowance != 0 AND allowance != requested` — i.e. "you are changing a
    non-zero allowance to a different non-zero allowance." Issue 2.3's helper
    was designed with a parameterized `trigger` callable specifically so this
    issue can supply a different predicate WITHOUT modifying the helper.
  - **MUST NOT change the 2.3 helper signature or body.** Issue 2.3 acceptance
    criteria pin `_soft_check_allowance`'s byte-identical Phase 1 behavior;
    editing it here would retroactively invalidate that AC. If a real
    implementation hurdle surfaces that seems to require a signature change,
    stop, fold the change back into 2.3 (reopen it), and re-run 2.3's tests
    before resuming 2.4.
- Watch out for:
  - Do NOT add a `try/except` around `estimate_gas`. The race check has its
    own scoped `try/except` (or uses the helper's), but the gas path remains
    fatal-no-fallback (ADR-007).
  - The race check fires only on `do_approve`, NOT on `do_transfer` or
    `do_transfer_from`.
  - Warning fatigue risk: every USDT-style approval triggers this warning.
    Per Phase 2 risk R7, the warning is data-tuple-based (ADR-004), so the
    decision to ship / suppress / reword is localizable.
- New files to create: none.
- Files NOT to modify: `build_send_eth.py`, `test_build_send_eth.py`,
  `SKILL.md`, `README.md`.

**Acceptance Criteria:**
- [ ] `_soft_check_allowance` (from Issue 2.3) is consumed **unmodified** —
      `git diff` of `build_erc20.py` for this issue shows zero changes to the
      helper's signature or body; only the `do_approve` call site adds a new
      invocation passing a custom `trigger` lambda. Issue 2.3's
      byte-identical-Phase-1 AC remains valid after this issue lands.
- [ ] `do_approve` happy path with `allowance == 0` (most common modern case)
      → `warnings_list == []` for the race check (`--approve-max` warning may
      still appear if `approve_max=True`).
- [ ] `do_approve` with `allowance != 0 AND allowance != requested` →
      `warnings_list` contains exactly one
      `("approve_race", {"holder": sender, "spender":..., "current":...,
      "requested":..., "decimals":..., "symbol":...})` tuple; JSON still
      emitted.
- [ ] `do_approve` with `allowance != 0 AND allowance == requested` →
      `warnings_list` does NOT contain `approve_race` (a no-op approve is
      not racy in the SWC-114 sense).
- [ ] `do_approve` with `approve_max=True` → `warnings_list` contains
      `approve_max` (existing Phase 1 behavior) but NOT `approve_race`.
- [ ] `do_approve` with `amount=0` (revocation) → `warnings_list` does NOT
      contain `approve_race` (revocations have no race).
- [ ] `do_approve` with the allowance RPC raising `_core.RPCError` →
      `warnings_list` contains
      `("approve_race_check_skipped", {"reason": "<msg>"})`; JSON still
      emitted.
- [ ] `do_approve` with `estimate_gas` raising still propagates (FATAL — the
      ADR-007 regression check).
- [ ] `TestSummary` gains: `warn_approve_race` emits a multi-line `WARNING:`
      containing the spender and the SWC-114 reference;
      `warn_approve_race_check_skipped` emits a `WARNING:` line.
- [ ] `TestTxAssembly` gains: the six `do_approve` cases above.
- [ ] `TestCliDispatch` gains: `main()` with non-zero current allowance returns
      exit 0, `WARNING:` on stderr, JSON on stdout.
- [ ] `python3 -m unittest test_build_erc20 -v` green.
- [ ] `python3 -m unittest test_build_send_eth -v` green (regression).

**Testing Notes:**
- The `do_approve` mock-rpc sequence gains one extra `eth_call` for the
  allowance read. Order in mock: `decimals` → `symbol` → `allowance` →
  `estimate_gas` → `getTransactionCount` → `getBlockByNumber` →
  `maxPriorityFeePerGas`.
- Test the three race-window arms explicitly (allowance == 0; allowance ==
  requested; allowance != 0 and != requested) — these are the SWC-114
  decision points.

---

### Issue 2.5: Add `sepolia` and `holesky` to v1 `NETWORKS` (relaxes the P0 freeze)

- **Points:** 2
- **Type:** feature
- **Priority:** P1
- **Blocked by:** none
- **Blocks:** Issue 2.6 (verifier), Issue 2.8 (docs sweep)
- **Scope:** 1 day

**Description:**
This is the **only Phase 2 issue that edits the v1 file**. Add two entries to
`build_send_eth.NETWORKS` so the four-network surface (`mainnet`, `hoodi`,
`sepolia`, `holesky`) is available to both helpers for free. Add matching v1
tests so the regression suite covers the new entries. Per architecture Open
Question 1 path (a) and Assumption 16: the freeze relaxes here, scope is
strictly the dict edit + new test cases.

**Implementation Notes:**
- Files likely affected:
  - `.claude/skills/eth-tx-builder/build_send_eth.py` (`NETWORKS` dict only).
  - `.claude/skills/eth-tx-builder/test_build_send_eth.py` (extend the
    `TestNetworkConfig`-style class with two new cases).
- In `build_send_eth.py`, extend the `NETWORKS` dict:
  ```
  NETWORKS = {
      "mainnet": (1, "https://ethereum-rpc.publicnode.com"),
      "hoodi":   (560048, "https://ethereum-hoodi-rpc.publicnode.com"),
      "sepolia": (11155111, "https://ethereum-sepolia-rpc.publicnode.com"),
      "holesky": (17000, "https://ethereum-holesky-rpc.publicnode.com"),
  }
  ```
  Chain IDs: sepolia = 11155111, holesky = 17000 (verified against
  ethereum-lists / chainlist / sibling `eth-rpc` Phase 2 plan; both match the
  publicnode endpoint URLs above).
- In `test_build_send_eth.py`, add two new test methods to the
  `TestNetworkConfig` class (or whatever the existing v1 test class name is —
  mirror the existing `test_mainnet` / `test_hoodi` cases exactly):
  - `test_sepolia` asserting
    `network_config("sepolia") == (11155111, "https://ethereum-sepolia-rpc.publicnode.com")`.
  - `test_holesky` asserting
    `network_config("holesky") == (17000, "https://ethereum-holesky-rpc.publicnode.com")`.
- Watch out for:
  - **Diff hygiene.** The v1 file edit MUST be strictly limited to the two new
    dict entries + the matching test additions. No whitespace fixes, no
    re-formatting, no comment edits, no `gofmt`-equivalent noise. PR review
    diff-check is the gate (Phase 2 risk R8). Use `git diff` on the v1 file
    after the edit and confirm only the targeted lines changed.
  - **Holesky deprecation.** Holesky is scheduled for retirement (post-2025).
    SKILL.md / README docs in Issue 2.8 should flag this so operators prefer
    `hoodi` for new work. The dict entry itself is still added — if publicnode
    retires the endpoint before this issue ships, drop holesky from the edit
    and document the gap in the SKILL.md notes (Phase 2 risk R6).
- New files to create: none.
- Files NOT to modify: `build_erc20.py`, `test_build_erc20.py` (the ERC-20
  helper picks up the new networks for free via `_core.NETWORKS`; verification
  lives in Issue 2.6).
- SKILL.md / README.md edits are deferred to Issue 2.8.

**Acceptance Criteria:**
- [ ] `build_send_eth.NETWORKS` contains exactly four entries: `mainnet`,
      `hoodi`, `sepolia`, `holesky` — with the chain IDs and URLs above.
- [ ] `git diff build_send_eth.py` shows exactly two added lines (one per new
      network entry) and nothing else (no whitespace, no re-formatting, no
      comment edits).
- [ ] `network_config("sepolia") == (11155111, "https://ethereum-sepolia-rpc.publicnode.com")`.
- [ ] `network_config("holesky") == (17000, "https://ethereum-holesky-rpc.publicnode.com")`.
- [ ] `network_config("nope")` still raises `ValueError` whose message lists
      all four networks in sorted order.
- [ ] `test_build_send_eth.TestNetworkConfig.test_sepolia` passes (added).
- [ ] `test_build_send_eth.TestNetworkConfig.test_holesky` passes (added).
- [ ] All pre-existing `test_build_send_eth` cases still pass unmodified.
- [ ] `python3 -m unittest test_build_send_eth -v` green.
- [ ] `python3 -m unittest test_build_erc20 -v` still green (regression; no
      ERC-20 helper code changed).

**Testing Notes:**
- Verify chain IDs against the sibling `eth-rpc` Phase 2 plan
  (`plan/eth-rpc-extension/`); if there is any discrepancy, surface it in
  review before merging — the goal is one consistent network map across both
  skills.
- A quick `curl https://ethereum-sepolia-rpc.publicnode.com -X POST -H
  'Content-Type: application/json' -d '{"jsonrpc":"2.0","method":"eth_chainId",
  "params":[],"id":1}'` should return `"0xaa36a7"` (= 11155111) and the
  equivalent for holesky should return `"0x4268"` (= 17000) — sanity check
  before committing.

---

### Issue 2.6: Verify ERC-20 helper picks up sepolia/holesky (test-only; scoped to dynamic `--network` choices)

- **Points:** 1
- **Type:** chore (test-only)
- **Priority:** P1
- **Blocked by:** Issue 2.5
- **Blocks:** Issue 2.8a, 2.8b, 2.8c
- **Scope:** 0.5 day

**Scoping caveat (load-bearing):** this issue is valid as **test-only** ONLY
if the Phase 1 deliverable left `build_erc20.py`'s `_build_parser` deriving
its `--network` argparse `choices` from `sorted(_core.NETWORKS)` (the
architecture-prescribed shape). The Phase 2 entry criteria assert this. If
Phase 1 instead hardcoded a list (e.g.
`choices=["mainnet", "hoodi"]`), this issue downgrades to a **one-line
code-fix** — replace the hardcoded list with `sorted(_core.NETWORKS)` — plus
the tests below. The point estimate and AC list still hold; only the
"test-only" framing flips. Confirm the Phase 1 shape before starting this
issue (a 30-second `grep "choices=" build_erc20.py`).

**Description:**
Confirm that `build_erc20.py` exposes the new networks automatically via
`sorted(_core.NETWORKS)` in its argparse `choices`, with no code edit needed.
Add a parameterized `TestCliDispatch` case asserting that `--network sepolia`
and `--network holesky` resolve correctly through to the `do_*` call. Also
add a smoke case asserting the argparse `choices` includes all four networks.

**Implementation Notes:**
- Files likely affected:
  - `.claude/skills/eth-tx-builder/test_build_erc20.py` (extend
    `TestCliDispatch`).
- This issue is **test-only**; `build_erc20.py` is NOT modified.
- Add to `TestCliDispatch`:
  - `test_network_choices_includes_all_four` — parse `transfer --help` (or
    inspect the parser directly via `_build_parser()`) and assert `--network`'s
    `choices` equals `["holesky", "hoodi", "mainnet", "sepolia"]` (sorted).
  - `test_main_transfer_sepolia` — mocked rpc, `--network sepolia`, assert
    `do_transfer` is called with `network="sepolia"` and the resulting
    `tx_dict["chainId"] == "11155111"`.
  - `test_main_transfer_holesky` — same as above for holesky
    (`chainId == "17000"`).
- Watch out for: this is a verification issue, not a feature. If the choices
  list doesn't include the new networks, something is wrong with the
  Phase 1 `_build_parser` implementation (it should use
  `sorted(_core.NETWORKS)` per architecture); fix in `build_erc20.py` and
  document the fix as part of this issue.
- New files to create: none.
- Files NOT to modify: `build_send_eth.py`, `test_build_send_eth.py`,
  `SKILL.md`, `README.md` (saved for Issue 2.8).

**Acceptance Criteria:**
- [ ] `_build_parser()` argparse `choices` for `--network` is exactly
      `["holesky", "hoodi", "mainnet", "sepolia"]` on each of the three
      subparsers.
- [ ] `main(["transfer", "--network", "sepolia", "--token", ..., "--to", ...,
      "--amount", "1", "--sender", ...])` with mocked rpc → exit 0, JSON
      `chainId == "11155111"`.
- [ ] `main(["transfer", "--network", "holesky", ...])` with mocked rpc → exit 0,
      JSON `chainId == "17000"`.
- [ ] `main(["transfer", "--network", "nope", ...])` → argparse rejection,
      exit 2 (the standard argparse code), error message mentions all four
      valid networks.
- [ ] `python3 -m unittest test_build_erc20.TestCliDispatch -v` green.
- [ ] `python3 -m unittest test_build_send_eth -v` still green.

**Testing Notes:**
- If `_build_parser` was Phase-1-implemented with a hardcoded list instead of
  `sorted(_core.NETWORKS)`, the fix is one line — replace the hardcoded list
  with the dynamic reference. The test asserting four-network choices catches
  the bug.

---

### Issue 2.7: Add `--summary-only` flag to each subcommand (dry-run mode)

- **Points:** 2
- **Type:** feature
- **Priority:** P1
- **Blocked by:** none (independent — cross-feature interactions with
  `low_balance` / `approve_race` warnings are deliberately deferred to Issue
  2.8b's regression matrix to keep 2.7 unblocked)
- **Blocks:** Issue 2.8a, 2.8b, 2.8c
- **Scope:** 1 day

**Description:**
Add a per-subparser `--summary-only` flag that runs the full build (all RPC
reads, all calldata generation, all gas estimation, all warning emission) but
**skips the final `print(json.dumps(tx, indent=2))` on stdout**. Useful for
"what would this do?" previews without exposing calldata to shell history.

**Implementation Notes:**
- Files likely affected:
  - `.claude/skills/eth-tx-builder/build_erc20.py` (Layer 4 `cli_dispatch`
    additions).
  - `.claude/skills/eth-tx-builder/test_build_erc20.py` (extend
    `TestCliDispatch`).
- In Layer 4 `cli_dispatch._build_parser`:
  - Add `--summary-only` to each of the three subparsers as a store-true
    boolean flag (per Phase 2 Assumption: per-subparser, not top-level).
  - Help text: `"print the stderr summary + warnings only; do NOT print the
    TxRequest JSON on stdout"`.
- In Layer 4 `cli_dispatch.main`, inside the success path, after
  `summary.print_summary(ctx)`:
  ```
  if getattr(args, "summary_only", False):
      return 0
  print(json.dumps(tx, indent=2))
  return 0
  ```
  - The summary block and all warnings still print to stderr as usual.
  - Exit code is 0 on a successful dry-run (the build worked; we just
    suppressed the stdout JSON).
- Watch out for:
  - **Behavior on RPC failure.** If `estimate_gas` raises, the helper still
    exits 1 with `error:` on stderr — `--summary-only` does NOT swallow
    errors. It only suppresses the **happy-path stdout** print.
  - **Behavior with warnings.** Warnings still emit on stderr as normal; the
    flag only affects the final JSON print. A `--summary-only --approve-max`
    invocation prints the loud max-uint warning, the summary, and no JSON.
  - **`getattr` default.** Use `getattr(args, "summary_only", False)` rather
    than `args.summary_only` to keep the dispatcher resilient if a future
    subparser is added without the flag.
- New files to create: none.
- Files NOT to modify: `build_send_eth.py`, `test_build_send_eth.py`,
  `SKILL.md`, `README.md` (Issue 2.8).

**Acceptance Criteria:**
- [ ] Each of `transfer`, `approve`, `transfer-from` subcommand `--help` lists
      `--summary-only` with the help text above.
- [ ] `main(["transfer", "--network", ..., "--summary-only", ...])` with mocked
      rpc → exit 0, stdout is empty (no characters at all), stderr contains
      the summary block.
- [ ] `main(["approve", "--approve-max", "--summary-only", ...])` with mocked
      rpc → exit 0, stdout empty, stderr contains both the `WARNING:` line for
      `--approve-max` (Phase 1 surface — independent of 2.4) AND the summary
      block. This case uses only Phase 1 warning surfaces so 2.7 stays
      unblocked by 2.2 / 2.4.
- [ ] `main([..., "--summary-only", ...])` with `estimate_gas` raising →
      exit 1, stdout empty, stderr contains `error:` (so `--summary-only`
      does NOT mask fatal errors).
- [ ] Without `--summary-only`, all existing happy-path tests still pass
      unmodified (JSON still goes to stdout).
- [ ] `python3 -m unittest test_build_erc20.TestCliDispatch -v` green.
- [ ] `python3 -m unittest test_build_send_eth -v` green (regression).

**Out of scope (deferred to Issue 2.8b regression matrix):**
- Interactions of `--summary-only` with `low_balance` warning (depends on
  2.2).
- Interactions of `--summary-only` with `approve_race` warning (depends on
  2.4).
- The full 4-warnings × 3-ops × `--summary-only` table-driven sweep.
Relocating these to 2.8b keeps Issue 2.7 truly independent (Blocked by: none)
and consolidates the cross-feature assertions in one place.

**Testing Notes:**
- Capture stdout via `unittest.mock.patch('sys.stdout', new_callable=io.StringIO)`
  (matches Phase 1 stdout/stderr-split tests). Assert
  `self.assertEqual(stdout.getvalue(), "")` for the `--summary-only` cases.
- Re-use the existing `TestCliDispatch` happy-path mock harness; just toggle
  the new flag.

---

### Issue 2.8a: Phase 2 docs sweep (SKILL.md + README.md) for all Phase 2 features

- **Points:** 2
- **Type:** chore (docs)
- **Priority:** P1
- **Blocked by:** Issues 2.2, 2.4, 2.6, 2.7 (the feature surfaces being
  documented must be implemented and green first)
- **Blocks:** Issue 2.8c
- **Scope:** 1–2 days

**Description:**
The dedicated docs pass for Phase 2. Capture every new flag, network, and
warning surface introduced by 2.2 (`low_balance`), 2.4 (`approve_race`), 2.5
(`sepolia`/`holesky` in `NETWORKS`), 2.6 (verified four-network argparse
choices in `build_erc20`), and 2.7 (`--summary-only` per-subparser flag) in
both SKILL.md and README.md. Pure prose work — no Python edits.

**Implementation Notes:**
- Files likely affected:
  - `.claude/skills/eth-tx-builder/SKILL.md` (prose edits only).
  - `.claude/skills/eth-tx-builder/README.md` (prose edits only).
- **SKILL.md edits:**
  - Update "Inputs — ERC-20 transfer / approve / transferFrom" with a note
    mentioning the four networks (`mainnet`, `hoodi`, `sepolia`, `holesky`);
    flag holesky as scheduled for deprecation, prefer hoodi for new work.
  - Add a "Soft-checks" subsection listing all warning families:
    `low_balance` (Issue 2.2), `balance_check_skipped` (Issue 2.2),
    `approve_race` (Issue 2.4), `approve_race_check_skipped` (Issue 2.4),
    `low_allowance` and `allowance_check_skipped` (Phase 1 — listed for
    completeness), `approve_max` (Phase 1). Each entry: one sentence on
    when it fires and that it does NOT block the build.
  - Add a "Dry-run mode" subsection mentioning `--summary-only`: prints the
    summary + warnings to stderr but suppresses the stdout JSON. Include an
    example invocation.
  - Update the operator-procedure routing step to mention `--summary-only`
    as a preview before committing to the JSON.
- **README.md edits:**
  - List the four networks in the "Supported networks" table or equivalent
    callout. Note the holesky deprecation.
  - Add a row to the file list / flag callout for `--summary-only`
    discoverability.
  - Add (but do NOT yet populate) a "Phase 2 preview" subsection placeholder
    in the "Manual end-to-end (hoodi)" section that Issue 2.8c will fill in
    with recorded `--summary-only` runs.
- Watch out for:
  - **Documentation drift.** The warning families' descriptions MUST match
    the in-code `WARNING:` message wording from 2.2 and 2.4; a mismatch makes
    operator triage harder. Copy verbatim where possible.
  - **Holesky availability.** If publicnode retired holesky before this
    issue ships (and 2.5 dropped the entry), trim the SKILL.md / README.md
    network list to three; do not document a network that does not exist in
    `NETWORKS`.
- New files to create: none.
- Files NOT to modify: `build_send_eth.py`, `build_erc20.py`,
  `test_build_send_eth.py`, `test_build_erc20.py` (this issue is doc-only).

**Acceptance Criteria:**
- [ ] SKILL.md mentions `mainnet`, `hoodi`, `sepolia`, `holesky` with the
      holesky deprecation note.
- [ ] SKILL.md has a "Soft-checks" subsection listing all seven warning
      kinds (Phase 1 + Phase 2) with one-line trigger descriptions.
- [ ] SKILL.md mentions `--summary-only` with an example.
- [ ] README.md "Supported networks" surface lists all four networks with
      the holesky deprecation note.
- [ ] README.md file list / flag callout mentions `--summary-only` as
      discoverable.
- [ ] README.md "Manual end-to-end" section has the empty "Phase 2 preview"
      subsection placeholder for 2.8c to populate.
- [ ] No `.py` file diff in this issue's commit (`git diff --name-only`
      contains only `SKILL.md` and `README.md`).
- [ ] `python3 -m unittest test_build_send_eth -v` green (regression).
- [ ] `python3 -m unittest test_build_erc20 -v` green (regression).

**Testing Notes:**
- Docs are not unit-tested, but a manual `grep` pass against the in-code
  warning messages catches the wording-drift class of error (e.g.
  `grep "WARNING:" build_erc20.py | sort -u` vs the SKILL.md soft-checks
  subsection bullets).

---

### Issue 2.8b: Cross-feature regression test matrix (warnings × ops; network choices table-driven)

- **Points:** 1
- **Type:** chore (test)
- **Priority:** P1
- **Blocked by:** Issues 2.2, 2.4, 2.6, 2.7 (the surfaces being asserted must
  exist)
- **Blocks:** Issue 2.8c
- **Scope:** 1 day

**Description:**
Land the cross-feature regression matrix that catches interaction bugs
between `--summary-only` (2.7), `low_balance` (2.2), `approve_race` (2.4),
and the four-network surface (2.5 + 2.6). This is the centralized location
for cross-feature assertions; Issue 2.7 deliberately keeps these out of its
own AC list so it stays independent and unblocked by 2.2 / 2.4.

**Implementation Notes:**
- Files likely affected:
  - `.claude/skills/eth-tx-builder/test_build_erc20.py` (extend
    `TestCliDispatch` with the matrix; no production code edits).
- The matrix has two table-driven sweeps:
  - **4-warnings × 3-ops × `--summary-only` sweep.** For each of the four
    Phase 2 warning families that can fire on a `transfer` / `approve` /
    `transfer-from` invocation, build a mocked-rpc scenario that triggers
    the warning, run with `--summary-only`, and assert exit 0 / stdout
    empty / stderr contains the warning text + the summary block. Use
    `subTest` parameterization so one failure surface lists the failing
    cell of the matrix.
  - **Per-network choices table-driven sweep.** For each of `mainnet`,
    `hoodi`, `sepolia`, `holesky`, run `main` with `--summary-only` against
    each of `transfer` / `approve` / `transfer-from` (12 combinations) and
    assert exit 0 / stdout empty / the expected `chainId` field would
    appear in the summary (the JSON itself is suppressed; assertion is on
    the in-memory `tx_dict` that `do_*` returns). This sweep proves the
    four-network surface composes with every op and with `--summary-only`.
- Specific cells the matrix MUST cover (relocated from the original Issue
  2.7 AC list and the original Issue 2.8 AC list):
  - `--approve-max --summary-only` on `approve` → exit 0, stdout empty,
    stderr contains BOTH the `--approve-max` warning AND the summary block.
  - `transfer-from` with low allowance + `--summary-only` → exit 0, stdout
    empty, stderr has the low-allowance warning + summary.
  - `transfer` with low balance + `--summary-only` → exit 0, stdout empty,
    stderr has the `low_balance` warning + summary (this is the cell
    previously in 2.7's AC list that introduced the 2.7→2.2 hidden
    dependency).
  - `approve` with non-zero current allowance + `--summary-only` → exit 0,
    stdout empty, stderr has the `approve_race` warning + summary (this is
    the cell previously in 2.7's AC list that introduced the 2.7→2.4
    hidden dependency).
  - The 4 × 3 network-choices × ops sweep above.
- Watch out for:
  - **Use `subTest`.** One parameterized test with descriptive sub-test IDs
    makes a failing cell trivially locatable. Avoid one method per cell.
  - **No production code edits.** If a cell fails, the failure points back
    at 2.2 / 2.4 / 2.6 / 2.7; reopen the relevant issue and fix there.
- New files to create: none.
- Files NOT to modify: `build_send_eth.py`, `build_erc20.py`, `SKILL.md`,
  `README.md`.

**Acceptance Criteria:**
- [ ] `TestCliDispatch` gains a `test_regression_matrix_warnings_x_ops_summary_only`
      method (or equivalently-named) using `subTest`, with all four warning
      cells above passing.
- [ ] `TestCliDispatch` gains a `test_regression_matrix_networks_x_ops_summary_only`
      method using `subTest`, with all twelve (4 networks × 3 ops) cells
      passing.
- [ ] `transfer` with low balance + `--summary-only` cell → exit 0, stdout
      empty, stderr contains `low_balance` text + summary. (Cell relocated
      from 2.7's AC list.)
- [ ] `approve` with non-zero current allowance + `--summary-only` cell →
      exit 0, stdout empty, stderr contains `approve_race` text + summary.
      (Cell relocated from 2.7's AC list.)
- [ ] `transfer-from` with low allowance + `--summary-only` cell → exit 0,
      stdout empty, stderr contains the low-allowance warning + summary.
- [ ] No `.py` file other than `test_build_erc20.py` is touched (`git diff
      --name-only` for this issue's commit lists only the test file).
- [ ] `python3 -m unittest test_build_erc20 -v` green.
- [ ] `python3 -m unittest test_build_send_eth -v` green (regression).

**Testing Notes:**
- `subTest` provides one-line context on each cell; pair with descriptive
  `msg=` arguments so failures read like
  `subTest(op='transfer', warning='low_balance', summary_only=True) FAILED`.
- Re-use the Phase 1 mocked-rpc helpers; this issue does not introduce new
  mocking patterns.

---

### Issue 2.8c: Manual hoodi e2e re-run + Phase-1 byte-identical fixture diff + commit on `develop`

- **Points:** 1
- **Type:** chore (integration + release)
- **Priority:** P1
- **Blocked by:** Issues 2.2, 2.4, 2.6, 2.7 (the feature surfaces), plus
  Issues 2.8a (docs that the e2e records reference) and 2.8b (regression
  matrix that gates committing)
- **Blocks:** none (Phase 2 exit)
- **Scope:** 1 day

**Description:**
Close Phase 2 with the on-machine integration step: rerun the Phase 1 hoodi
manual e2e, diff the resulting TxRequest JSON against the pre-Phase-2
baseline fixtures captured at the Phase 2 entry point, record a `--summary-only`
preview run for each op, then commit the whole phase on `develop`. The
byte-identical diff is the proof Phase 2 was additive — anything else means
a hidden regression slipped past the unit tests.

**Implementation Notes:**
- Files likely affected:
  - `.claude/skills/eth-tx-builder/README.md` (populate the "Phase 2 preview"
    subsection placeholder that 2.8a created).
  - Local fixture files captured at the Phase 2 entry point (e.g.
    `.claude/skills/eth-tx-builder/fixtures/phase1-baseline-{transfer,approve,
    transfer-from}.json`) — read-only here; do NOT regenerate them.
  - git: commits on `develop`.
- **Phase 1 hoodi e2e re-run:**
  - Run each of the three Phase 1 ops (`transfer`, `approve`,
    `transfer-from`) on hoodi with the same args used at the Phase 2 entry
    point baseline capture.
  - Save the new stdout JSON to scratch files (e.g.
    `phase1-post-phase2-<op>.json`).
  - `diff` against the corresponding `phase1-baseline-<op>.json` from the
    Phase 2 entry criteria capture. The diff must be empty (byte-identical).
  - If the diff is NOT empty, STOP — Phase 2 is not additive as designed.
    The drift signals a hidden code path change in `build_erc20.py` or its
    `_core` dependencies; reopen the offending issue (most likely 2.2 / 2.3
    / 2.4) before committing.
- **Phase 2 preview e2e:**
  - Run `--summary-only` against each of `transfer`, `approve`,
    `transfer-from` on sepolia (or hoodi if no sepolia ERC-20 is available;
    note the network used in the README).
  - Record the stderr output (summary + any warnings) in the README's
    "Phase 2 preview" subsection that 2.8a left as a placeholder.
- **Commit on `develop`:**
  - Per repo memory (`develop` is the integration branch), commit on
    `develop`; do not PR or merge to `main`. Suggested commit shape: one
    commit per Issue 2.1–2.7 landed in sequence, then a 2.8a docs commit,
    then a 2.8b matrix commit, then this 2.8c e2e + README populate commit
    closing the phase.
- Watch out for:
  - **Fixture availability.** This issue is unworkable if the Phase 2 entry
    criteria fixture capture step was skipped. Verify the baseline files
    exist before running the post-Phase-2 invocations.
  - **Holesky availability.** If publicnode retired holesky between Phase 1
    and Phase 2, the documented network set in the README should match
    whatever `NETWORKS` actually contains after Issue 2.5's edit decision.
- New files to create: scratch fixture files for the post-Phase-2 runs
  (treated as local-only, NOT committed; the diff outcome is what gets
  recorded).
- Files NOT to modify: `build_send_eth.py`, `build_erc20.py`,
  `test_build_send_eth.py`, `test_build_erc20.py`, `SKILL.md`. The only
  source-tree edit in this issue is README.md (the Phase 2 preview block).

**Acceptance Criteria:**
- [ ] Phase 1 e2e baseline fixtures captured at Phase 2 entry are confirmed
      present at the documented path before the post-Phase-2 run.
- [ ] Phase 1 hoodi e2e re-run produces JSON byte-identical to the
      pre-Phase-2 baseline fixture for each of the three Phase 1 ops on
      hoodi (`diff phase1-baseline-<op>.json phase1-post-phase2-<op>.json`
      empty for all three).
- [ ] README "Phase 2 preview" subsection is populated with a
      `--summary-only` run record for each of `transfer`, `approve`,
      `transfer-from` (network noted explicitly per run).
- [ ] `python3 -m unittest test_build_send_eth -v` green at HEAD on
      `develop` immediately before committing.
- [ ] `python3 -m unittest test_build_erc20 -v` green at HEAD on `develop`
      immediately before committing.
- [ ] Phase 2 commit(s) landed on `develop`. No PR or merge to `main`
      taken; commit history is consistent with the per-issue commit shape
      above.

**Testing Notes:**
- The byte-identical fixture diff is the load-bearing integration assertion.
  Unit tests catch behavior changes inside `do_*`; only the e2e diff catches
  changes in the path-through (defaults, ordering, JSON serialization key
  order, etc.) that can sneak past a green unit suite.
- If the diff is non-empty by a single line (e.g. `gasPrice` shifted by a
  few wei due to hoodi mempool noise), investigate before declaring drift —
  hoodi RPC responses are not always deterministic across re-runs. A
  reasonable mitigation is to use the **same `block.timestamp` / nonce /
  fee** inputs as the baseline by passing explicit override flags if the
  CLI supports them; document the fix and re-attempt.
