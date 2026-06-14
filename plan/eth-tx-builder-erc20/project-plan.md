# Project Plan: eth-tx-builder ERC-20 extension

## Summary

Extend the existing `.claude/skills/eth-tx-builder/` skill with ERC-20
`transfer` / `approve` / `transfer-from` builders by adding one new helper
file (`build_erc20.py`), one new test file (`test_build_erc20.py`), and
prose-only edits to `SKILL.md` / `README.md`. The v1 ETH-send path
(`build_send_eth.py` + `test_build_send_eth.py`) stays **bit-for-bit
frozen** for the entire P0 phase. DRY reuse happens through
`import build_send_eth as _core`; the v1 file plays the dual role of CLI +
de facto shared library. Inside `build_erc20.py` the code is organized into
seven labeled in-file sections forming a strict downward dependency DAG
across four layers (Layer 1: `abi_codec`, `amount_codec`; Layer 2:
`contract_reads`, `gas_estimator`, `summary`; Layer 3: `tx_assembly`;
Layer 4: `cli_dispatch`).

The work splits into three phases mirroring the PRD's P0 / P1 / P2
priorities, but shaped as a real implementation sequence rather than a copy
of the buckets:

- **Phase 1 — P0 implementation (must ship).** Build the leaf layers first
  (selectors + golden-vector ABI tests, then amount codec), then Layer 2
  (`contract_reads`, `gas_estimator`, `summary`), then Layer 3
  (`tx_assembly` builders), then Layer 4 (`cli_dispatch` + warnings wiring),
  then the docs sweep and the hoodi end-to-end. This is the bulk of the
  work and the only commitment required to call the extension "shipped."
- **Phase 2 — P1 follow-ups.** Smaller, independently-shippable
  enhancements that loosen the P0 freeze on `build_send_eth.py`: add
  `sepolia` / `holesky` to `NETWORKS` (the only Phase 2 task that touches
  v1), `balanceOf` pre-check on `transfer`, `approve`-race soft warning,
  and `--summary-only`.
- **Phase 3 — P2 opportunistic.** Bounded backlog: `approve --revoke`
  shorthand, polished `bytes32` symbol decode, and (only if demand
  surfaces) an EIP-2612 `permit` builder that gets its own dependency
  conversation.

Phase 1 is the only critical path. Phase 2 and Phase 3 are independent
follow-ups; they do not gate Phase 1's release. Single-developer effort,
single-stream — no parallel work-streams.

## Prerequisites

- Local Python 3.8+ on `PATH` (skill is stdlib-only; matches v1 runtime).
- Network access to the publicnode hoodi endpoint
  (`https://ethereum-hoodi-rpc.publicnode.com`) for the Phase 1 manual e2e.
- A real ERC-20 token deployed on hoodi that the operator can read against
  (for `decimals()` / `symbol()` confirmation) and ideally transfer / approve
  against (for the Phase 1 exit criterion against a live signer flow). The
  PRD success metrics require the e2e to demonstrate that all three ops
  produce signer-accepted JSON that executes on hoodi.
- A connected `eth-signer-mcp` session (or a keystore the operator can paste
  JSON into manually) for the Phase 1 e2e sign-and-broadcast steps.
- The four upstream artifacts approved and in-tree:
  - `plan/eth-tx-builder-erc20/prd.md`
  - `plan/eth-tx-builder-erc20/research/00-overview.md` (+ `01-`, `02-`,
    `03-` once persisted)
  - `plan/eth-tx-builder-erc20/architecture.md`
  - Existing skill files (`build_send_eth.py`, `SKILL.md`, `README.md`,
    `test_build_send_eth.py`).
- Familiarity with the existing skill layout (flat directory, no
  `__init__.py`, `python3 build_send_eth.py ...` invocation pattern,
  `python3 -m unittest test_build_send_eth -v` test pattern).
- Development happens on `develop` per repo convention (`develop` is the
  integration branch; `main` is release-only — no PR or merge to `main`
  without an explicit operator ask).

---

## Phase 1: P0 — ERC-20 builder, tests, docs, hoodi e2e (MUST SHIP)

**Goal:** Ship a self-contained, releasable `build_erc20.py` exposing the
three ERC-20 movement subcommands (`transfer`, `approve`, `transfer-from`)
with all P0 PRD requirements satisfied — stdlib-only, hardcoded selectors,
human-amount → base-unit conversion via live `decimals()` read,
`eth_estimateGas` with +20% buffer / 300k cap / no fallback, `--approve-max`
gated and warned, `transfer-from` allowance soft-check, stdout=JSON /
stderr=summary, integer-only arithmetic — plus a full
`test_build_erc20.py` mirroring the seven-section layout, updated
`SKILL.md` and `README.md`, and a confirmed hoodi end-to-end against a real
ERC-20.

**Duration estimate:** Large — the bulk of the project. Single-developer,
single-stream; sequenced strictly by the architecture's Layer 1 → 4 graph.

**P0 freeze constraint:** Throughout Phase 1, `build_send_eth.py` and
`test_build_send_eth.py` are **bit-for-bit untouched**. Verified at exit by
`python3 -m unittest test_build_send_eth -v` passing unmodified. (The
freeze relaxes in Phase 2 only to add `sepolia` / `holesky`.)

### Tasks

- [ ] **Task 1.1 — Stand up the `build_erc20.py` skeleton.**
  Create `.claude/skills/eth-tx-builder/build_erc20.py` with the
  shebang, module docstring (which restates the imported `_core` symbol
  contract per architecture §Module Details), `import argparse, json, sys`,
  and `import build_send_eth as _core`. Add the seven section banner
  comments (`# === Layer 1: abi_codec ===`, etc.) in dependency order top to
  bottom. Add the `if __name__ == "__main__": sys.exit(main())` guard with
  a stub `main` returning `0`. Verify `python3 build_erc20.py` exits 0 and
  `python3 -c "import build_erc20"` succeeds.
  - Dependencies: none.
  - Complexity: low.

- [ ] **Task 1.2 — Implement Layer 1 `abi_codec`: selectors + encoders +
  decoders, with golden-vector tests.**
  In `build_erc20.py` Layer 1 `abi_codec` section: define `SEL_TRANSFER`,
  `SEL_APPROVE`, `SEL_TRANSFER_FROM`, `SEL_DECIMALS`, `SEL_SYMBOL`,
  `SEL_ALLOWANCE` as module-level hex-string constants, each with the
  one-line derivation comment (architecture ADR-005). Add `MAX_DECIMALS =
  36`. Implement `_encode_address`, `_encode_uint256` (rejects negative and
  `>= 2**256`), `_pack_call`, `encode_transfer`, `encode_approve`,
  `encode_transfer_from`, `encode_decimals_call`, `encode_symbol_call`,
  `encode_allowance_call`, `decode_decimals` (low byte; raises on
  `> MAX_DECIMALS`), `decode_symbol` (returns `Optional[str]`: standard ABI
  `string` first, then `bytes32`-with-null-trim fallback, then `None`),
  `decode_allowance`. In `test_build_erc20.py` add `TestAbiCodec` with the
  selector-constant equality checks, the golden-vector calldata bit-pattern
  checks for `transfer` / `approve` / `transfer-from` against a known-good
  USDC mainnet vector (research §01), and the decoder cases (`decimals`
  0/6/18/24 OK and 37 rejected; `symbol` standard + `bytes32` + malformed →
  `None`; `allowance` 0 + max-uint).
  - Dependencies: Task 1.1.
  - Complexity: medium (volume; golden-vector hand-verification is the
    careful step).

- [ ] **Task 1.3 — Implement Layer 1 `amount_codec` with the no-`float`
  invariant test.**
  In `build_erc20.py` Layer 1 `amount_codec` section: define
  `MAX_UINT256 = (1 << 256) - 1`. Implement `human_to_base_units(amount_str,
  decimals) -> int` using pure string manipulation (`str → str → int`):
  split on `"."`, validate halves with `re.fullmatch`, reject empties,
  multi-dot, non-digits, negatives, and `len(frac) > decimals`; right-pad
  `frac` to exactly `decimals` digits; concatenate; `int(..., 10)`.
  Implement `base_units_to_human(amount, decimals) -> str` for the summary
  renderer. In `test_build_erc20.py` add `TestAmountCodec` with the
  PRD-listed golden vectors (`"0"`, `"0.0"`, `"1"`, `"1.5"`, `"0.000001"`,
  large), the negative-path rejections (`""`, `"-1"`, `"1..5"`, `"1.5.0"`,
  `"abc"`, `"1.0000001"` at decimals=6), a `base_units_to_human` round-trip
  on the same vectors, the `MAX_UINT256 == (1 << 256) - 1` assertion, and
  the negative invariant check via `inspect.getsource(b.human_to_base_units)`
  NOT containing the substring `"float("` (architecture ADR-008).
  - Dependencies: Task 1.1.
  - Complexity: low–medium (the negative-source assertion is the careful
    bit).

- [ ] **Task 1.4 — Implement Layer 2 `contract_reads`: fatal vs.
  best-effort split enforced by signature.**
  In `build_erc20.py` Layer 2 `contract_reads` section: implement
  `fetch_decimals(rpc, url, token) -> int` (calls `eth_call` with
  `encode_decimals_call()`; passes the result through `decode_decimals`;
  propagates `_core.RPCError` — **FATAL**). Implement
  `fetch_symbol(rpc, url, token) -> Optional[str]` (catches all exceptions
  — `RPCError`, decode failures — and returns `None`; **best-effort**).
  Implement `fetch_allowance(rpc, url, token, holder, spender) -> int`
  (calls `eth_call` with `encode_allowance_call(holder, spender)`;
  propagates `_core.RPCError` — soft-check posture is the caller's job).
  In `test_build_erc20.py` add `TestContractReads`: `fetch_decimals` with
  mocked rpc returning `"0x...06"` → `6`; mocked rpc raising propagates;
  `fetch_symbol` with mocked rpc returning USDC bytes → `"USDC"`; mocked
  rpc raising → `None`; decoder returning `None` → `None`;
  `fetch_allowance` with mocked rpc returning `"0x...0a"` → `10`; mocked
  rpc raising propagates.
  - Dependencies: Task 1.2.
  - Complexity: low.

- [ ] **Task 1.5 — Implement Layer 2 `gas_estimator` with no `try/except`
  and the regression test for the no-fallback invariant.**
  In `build_erc20.py` Layer 2 `gas_estimator` section: define
  `GAS_BUFFER_NUM = 12`, `GAS_BUFFER_DEN = 10`, `GAS_CAP = 300_000`.
  Implement `_apply_buffer_cap(est) -> int` returning
  `min((est * GAS_BUFFER_NUM) // GAS_BUFFER_DEN, GAS_CAP)`. Implement
  `estimate_gas(rpc, url, sender, token, data) -> int` building the call
  object `{"from": sender, "to": token, "data": data, "value": "0x0"}`
  against `"latest"`, parsing the hex result via `_core.parse_hex_int`, and
  returning `_apply_buffer_cap(est)`. **NO `try/except` in this function —
  `RPCError` propagates by design (architecture ADR-007).** Place the
  multi-line in-code comment explaining why a future maintainer adding a
  silent fallback would let a doomed tx burn its gas budget. In
  `test_build_erc20.py` add `TestGasEstimator`: `estimate_gas` with mocked
  rpc returning `"0xfe1f"` (65055) → buffered (78066); mocked rpc returning
  `"0x3d090"` (250000) → capped (300_000); mocked rpc raising `RPCError`
  → propagates with no catch; `_apply_buffer_cap` table: `0 → 0`, `1 → 1`,
  `250_000 → 300_000`, `1_000_000 → 300_000`.
  - Dependencies: Task 1.4 (consumes the same `_core.parse_hex_int` and
    `_core.RPCError` plumbing patterns).
  - Complexity: low.

- [ ] **Task 1.6 — Implement Layer 2 `summary`: warning dispatcher and
  stderr renderer.**
  In `build_erc20.py` Layer 2 `summary` section: implement
  `render_summary(ctx) -> str` (pure; returns the human-readable block with
  every PRD §16 field), `print_summary(ctx) -> None` (writes to stderr),
  and the per-warning helpers `warn_approve_max(symbol, token, spender)`,
  `warn_low_allowance(holder, spender, current, requested, decimals)`,
  `warn_allowance_check_skipped(reason)`, and `warn_symbol_unavailable()`
  (optional). Implement `emit_warning(kind, payload) -> None` as the
  dispatcher consuming `(kind, payload_dict)` tuples (architecture ADR-004).
  All warning lines use the `WARNING:` prefix; all error lines (downstream
  in `cli_dispatch`) use the lowercase `error:` prefix (architecture ADR-009).
  In `test_build_erc20.py` add `TestSummary`: `render_summary` returns text
  containing every expected label (`"token"`, `"decimals"`,
  `"amount (base units)"`, etc.); `warn_approve_max` writes the multi-line
  warning text to stderr (capture with `unittest.mock.patch('sys.stderr')`);
  `warn_low_allowance` and `warn_allowance_check_skipped` each emit a
  `WARNING:` line.
  - Dependencies: Task 1.3 (uses `base_units_to_human` for the render).
  - Complexity: low–medium (mostly text plumbing).

- [ ] **Task 1.7 — Implement Layer 3 `tx_assembly`: the three `do_*`
  composers returning `(tx_dict, summary_ctx, warnings_list)`.**
  In `build_erc20.py` Layer 3 `tx_assembly` section: implement the
  `_build_eip1559_envelope` internal helper. Implement `do_transfer`,
  `do_approve` (with `approve_max=False` kwarg), `do_transfer_from`
  following the eight-step skeleton in architecture §Module Details:
  resolve `(chain_id, url)` via `_core.network_config`; `fetch_decimals`
  (FATAL); `fetch_symbol` (best-effort); resolve `amount_base`
  (`MAX_UINT256` for approve-max, otherwise `human_to_base_units`); build
  calldata via the matching `abi_codec.encode_*`; (transfer-from only)
  `try fetch_allowance; except _core.RPCError → queue
  warn_allowance_check_skipped; else if allowance < requested → queue
  warn_low_allowance` — **this is the only `try/except` for `RPCError`
  outside `cli_dispatch`**; call `estimate_gas` (no try/except — FATAL on
  fail); fetch nonce / base fee / tip / `compute_max_fee` via `_core`;
  return `(tx_dict, summary_ctx, warnings_list)` with the exact v1 TxRequest
  shape (`to` = token contract, `value` = `"0"`, `data` = calldata,
  `gas` = buffered + capped estimate, all numeric fields as decimal
  strings). In `test_build_erc20.py` add `TestTxAssembly` covering: each
  `do_*` happy path (assert `tx_dict` shape + keys, `summary_ctx` keys,
  `warnings_list == []`); `do_approve` with `approve_max=True`
  (calldata amount word is all-Fs, warnings contains `("approve_max",
  {...})`); `do_transfer_from` with low allowance (warnings contains
  `("low_allowance", {...})`, JSON still built); `do_transfer_from` with
  `fetch_allowance` raising `RPCError` (warnings contains
  `("allowance_check_skipped", {...})`, JSON still built); `do_*` with
  `fetch_decimals` raising `RPCError` propagates (no JSON); `do_*` with
  `estimate_gas` raising `RPCError` propagates (no JSON — the FATAL +
  no-fallback regression check).
  - Dependencies: Tasks 1.4, 1.5, 1.6.
  - Complexity: medium (composition layer; this is where the `try/except`
    placement discipline matters).

- [ ] **Task 1.8 — Implement Layer 4 `cli_dispatch`: argparse, address
  validation, dispatcher, and the stdout/stderr split.**
  In `build_erc20.py` Layer 4 `cli_dispatch` section: implement
  `_build_parser() -> argparse.ArgumentParser` with three subparsers
  (`transfer`, `approve`, `transfer-from`), each with the PRD-defined
  required flags. For `approve` use
  `add_mutually_exclusive_group(required=True)` for `--amount` /
  `--approve-max` (architecture Assumption 13). Subcommand `choices` for
  `--network` is `sorted(_core.NETWORKS)`. Implement
  `_validate_addresses(args) -> None` calling `_core.validate_hex_address`
  for every address argument present on the parsed args (architecture
  ADR-010). Implement `main(argv=None) -> int` with the structure:
  `try: parse args; _validate_addresses(args); dispatch to the chosen
  do_*; for w in warnings: summary.emit_warning(w);
  summary.print_summary(ctx); print(json.dumps(tx, indent=2)); return 0
  except (ValueError, _core.RPCError) as e: print("error: %s" % e,
  file=sys.stderr); return 1`. This is the **only**
  `try/except (ValueError, _core.RPCError)` in the entire codebase
  (architecture ADR-007). In `test_build_erc20.py` add `TestCliDispatch`:
  top-level `--help` lists all three subcommands; each subcommand's
  `--help` is present; approve enforces `--amount` XOR `--approve-max`
  (both → argparse rejects, neither → argparse rejects); `main` with bad
  address returns 1 + `error:` on stderr + no JSON on stdout; `main` happy
  path returns 0 + JSON on stdout + summary on stderr; `main` with
  `RPCError` from `estimate_gas` returns 1 + no JSON on stdout (the
  no-fallback regression assertion at the CLI layer); `main` with
  `--approve-max` returns 0 + `WARNING:` on stderr + JSON on stdout;
  `main` with low allowance on `transfer-from` returns 0 + `WARNING:` on
  stderr + JSON on stdout. Duplicate the `make_fake_rpc` helper from
  `test_build_send_eth.py` with the one-line architecture-mandated comment
  (Assumption 14).
  - Dependencies: Task 1.7.
  - Complexity: medium (argparse + dispatcher + a lot of CLI test surface).

- [ ] **Task 1.9 — Run the v1 regression check and the full new test
  suite green.**
  From `.claude/skills/eth-tx-builder/`:
  `python3 -m unittest test_build_send_eth -v` MUST pass with zero changes
  to its file or to `build_send_eth.py` (the P0 freeze check). Then
  `python3 -m unittest test_build_erc20 -v` MUST pass for all seven
  `TestCase` classes. If either fails, fix in code (never by editing the v1
  files; the freeze is absolute for Phase 1).
  - Dependencies: Tasks 1.2–1.8.
  - Complexity: low (verification step).

- [ ] **Task 1.10 — Update `SKILL.md` (prose only).**
  Broaden the description string to include ERC-20. Split the "Inputs"
  section into "ETH send" (existing content unchanged) and "ERC-20 transfer
  / approve / transferFrom" (new). Add a routing step at the top of
  "Procedure": (1) identify intent (native ETH vs ERC-20), (2) call the
  appropriate helper script. Update "Out of scope (v1)" — remove ERC-20
  (now in scope) and add the new explicit non-goals (permit, ERC-721/1155,
  swaps, multi-token batch, fee-on-transfer / rebasing handling, gasless
  meta-tx, signing, broadcasting). Mention the `--approve-max` warning
  posture and the stdout-JSON / stderr-summary split. No code edits — pure
  prose.
  - Dependencies: Task 1.8 (CLI shape stable before documenting it).
  - Complexity: low (prose).

- [ ] **Task 1.11 — Update `README.md` (prose only).**
  Add file-list entries for `build_erc20.py` and `test_build_erc20.py`.
  Add a "Manual end-to-end (hoodi)" section with the three runs (one
  `transfer`, one `approve --amount`, one `transfer-from`) against a real
  ERC-20 deployed on hoodi, ending in a paste-to-signer step. Update the
  test invocation snippet to run both v1 (`test_build_send_eth`) and the
  new (`test_build_erc20`) files. No code edits.
  - Dependencies: Task 1.8 (CLI shape stable before documenting it).
  - Complexity: low.

- [ ] **Task 1.12 — Execute the hoodi manual end-to-end against a real
  ERC-20.**
  Pick a real ERC-20 deployed on hoodi (PRD success metric: "the
  `decimals()` RPC read works against a real ERC-20 deployed on `hoodi`").
  Run `python3 build_erc20.py transfer --network hoodi --token <addr>
  --to <addr> --amount <human> --sender <addr>` and confirm: (a) the
  helper exits 0; (b) the stderr summary names the symbol, decimals,
  resolved base-unit amount, and addresses; (c) the stdout JSON pastes
  into `eth-signer-mcp` `sign_transaction` and signs; (d) when broadcast
  (via `eth-rpc`'s `broadcast` op, or any RPC client), the transaction
  executes successfully on-chain. Repeat for `approve --amount` and
  `transfer-from`. Record the three confirmations in the README's manual
  e2e section. If any of (a)–(d) fail for any op, surface as a Phase 1
  blocker and do **not** ship.
  - Dependencies: Tasks 1.9, 1.10, 1.11.
  - Complexity: medium (live network; depends on having an ERC-20 ready to
    play with on hoodi).

- [ ] **Task 1.13 — Commit on `develop`.**
  Per repo memory, `develop` is the integration branch; do not PR or merge
  to `main` unprompted. Commit messages reference the PRD, architecture,
  and (for the e2e commit) the hoodi token used. Suggested commit shape:
  one commit per Layer or per task group, ending with the docs + e2e
  commits so the history reads top-down.
  - Dependencies: Tasks 1.9, 1.12.
  - Complexity: low.

### Phase 1 Exit Criteria (must all be true; ties directly to PRD success metrics)

- **Functional coverage:** each of `transfer`, `approve`, `transfer-from`
  produces a `TxRequest` JSON that `eth-signer-mcp` `sign_transaction`
  accepts and, when broadcast on hoodi, **executes successfully on-chain**
  (PRD success metric §1 — verified by Task 1.12).
- **Real-token `decimals()`:** the `decimals()` RPC read works against a
  real ERC-20 deployed on hoodi (PRD success metric §2 — verified by Task
  1.12).
- **Zero new dependencies:** `python3 build_erc20.py --help` works on a
  fresh stdlib-only Python 3.8+ install; no `requirements.txt`,
  `pyproject.toml`, or vendored package is added (PRD success metric §3 —
  verified by Task 1.9 plus a manual check from a clean venv).
- **No regression in ETH-send:** `python3 -m unittest test_build_send_eth
  -v` passes with **zero edits** to `build_send_eth.py` or
  `test_build_send_eth.py` (PRD success metric §4; the P0 freeze —
  verified by Task 1.9 plus PR-review confirmation that those two files
  have no diff vs. their pre-Phase-1 SHAs).
- **Test coverage of the new helper:** `python3 -m unittest test_build_erc20
  -v` is green and covers every case enumerated in PRD §P0 §20 (the
  per-section `TestCase` classes from architecture ADR-011) — verified by
  Task 1.9.
- **No-fallback regression check passes:** the `TestTxAssembly` and
  `TestCliDispatch` regressions assert that when `estimate_gas` raises,
  exit code is 1 and stdout is empty (architecture ADR-007).
- **No-`float` regression check passes:**
  `inspect.getsource(b.human_to_base_units)` does NOT contain `"float("`
  (architecture ADR-008).
- **Stdout / stderr discipline:** `TestCliDispatch` asserts that on the
  happy path stdout contains exactly the JSON and the summary + warnings
  live on stderr (architecture ADR-009).
- **SKILL.md** is updated with the routing step, the split inputs, the
  refreshed "Out of scope" list, and references both helpers.
- **README.md** lists `build_erc20.py` + `test_build_erc20.py`, includes
  the hoodi manual e2e for all three ops, and the test invocation snippet
  covers both test files.
- **Commit landed on `develop`.**

### Phase 1 Risks / Assumptions

- **R1 (Low / High) — Hoodi ERC-20 unavailable or non-standard.** If the
  operator can't find a standard ERC-20 on hoodi to e2e against,
  Task 1.12 stalls. Mitigation: pick a token with `decimals` + `symbol`
  + `transfer` known good in advance; the PRD assumption set already
  requires the standard ERC-20 surface.
- **R2 (Low / High) — `eth_estimateGas` returns a value that broadcasts
  but underestimates on a particular hoodi token.** The +20% buffer +
  300k cap mitigates this; if the broadcast reverts due to OOG, the
  Phase 1 e2e fails (this is the correct signal: the test surfaces a
  bad real-world token rather than the build helper).
- **R3 (Low / Medium) — Golden-vector USDC mainnet calldata not
  hand-verifiable from research alone.** Mitigation: cross-check Task
  1.2 vectors against a second source (Etherscan "input data" decode of
  a known USDC transfer; an `eth-abi` round-trip in a scratch script —
  the script is not committed). The selectors themselves are verified in
  research §01.
- **R4 (Low / Medium) — publicnode rate-limits one of the six to seven
  sequential RPC calls during the e2e.** Mitigation: rerun; build path
  is idempotent; `_core.rpc_call` already enforces a 15-second timeout
  with no retry by design.
- **R5 (Medium / Low) — A future v1 maintainer renames a symbol in
  `build_send_eth.py` between Phase 1 plan and ship.** Mitigation: the
  P0 freeze is operationally enforced; PR review checks v1 SHA;
  `test_build_erc20.py` would fail at import-load if a symbol were
  renamed underneath us.
- **A1 — Single new file `build_erc20.py`** (architecture §Overview).
- **A2 — `import build_send_eth as _core`; no `_common.py` extraction
  in Phase 1; no edit to v1** (architecture ADR-001).
- **A3 — Seven labeled in-file sections in strict downward DAG**
  (architecture ADR-002).
- **A4 — `do_*` returns `(tx_dict, summary_ctx, warnings_list)`; CLI
  prints** (architecture ADR-004).
- **A5 — Hardcoded selectors with derivation comments; no runtime
  Keccak** (architecture ADR-005).
- **A6 — Structural fatal-vs-best-effort split via signatures**
  (architecture ADR-006).
- **A7 — `eth_estimateGas` no-fallback enforced by absence of
  try/except in middle layers** (architecture ADR-007).
- **A8 — Integer-only amount conversion; no `float()` on the amount
  path** (architecture ADR-008).
- **A9 — Stdout = JSON only; stderr = summary + warnings + errors**
  (architecture ADR-009).
- **A10 — Address validation at the CLI layer only** (architecture
  ADR-010).
- **A11 — `make_fake_rpc` helper duplicated (~12 lines) into
  `test_build_erc20.py`** with the architecture-mandated one-line
  comment (Assumption 14).
- **A12 — P0 freeze applies to Phase 1 only.** Phase 2 will edit
  `build_send_eth.NETWORKS` to add `sepolia`/`holesky` (architecture
  Open Question 1, Assumption 16) — see Phase 2 below.

---

## Phase 2: P1 — balanceOf pre-check, approve-race guard, sepolia/holesky, --summary-only

**Goal:** Ship the four PRD §P1 follow-ups as independently-shippable
enhancements on top of a green Phase 1. None of them is on the critical
path; each can land or skip without blocking the others. Phase 2 is also
the first time the v1 freeze relaxes — Task 2.3 edits
`build_send_eth.NETWORKS` directly (architecture Open Question 1,
recommended path (a)) and adds matching tests in
`test_build_send_eth.py`.

**Duration estimate:** Medium — one focused work block per sub-feature;
each is independently shippable.

**Freeze relaxation:** In Phase 2 the "no-edit `build_send_eth.py`"
constraint applies only to the parts of v1 that Phase 2 doesn't need to
change. Task 2.3 specifically edits `NETWORKS` and adds matching v1 test
cases; every other Phase 2 task continues to leave v1 untouched. The full
v1 ETH-send behavior — gwei → wei, fee logic, RPC plumbing, address
validation, RPCError — stays bit-for-bit identical to Phase 1.

### Tasks

- [ ] **Task 2.1 — Implement `balanceOf` pre-check on `transfer` (PRD
  §P1 §2).**
  Add `encode_balance_of_call(holder) -> str`, `decode_balance(hex_result)
  -> int`, and `fetch_balance_of(rpc, url, token, holder)` to the
  appropriate Layer 1 / Layer 2 sections of `build_erc20.py`. Define
  `SEL_BALANCE_OF = "0x70a08231"` with derivation comment. In
  `do_transfer`, wrap `fetch_balance_of(rpc, url, token, sender)` in a
  local `try/except _core.RPCError`: on success, if balance < requested
  base units, queue a `low_balance` warning; on `RPCError`, queue a
  `balance_check_skipped` warning. Mirror the `transfer-from` allowance
  soft-check posture (warn-don't-block). Add `warn_low_balance` and
  `warn_balance_check_skipped` to `summary`. Add new `TestContractReads`
  case for `fetch_balance_of`, new `TestTxAssembly` cases for both
  warning paths, new `TestCliDispatch` cases for the low-balance and
  skipped paths. Update `SKILL.md` notes to mention the new soft-check
  posture for `transfer`. Update `README.md` manual e2e to include the
  warning text path.
  - Dependencies: Phase 1 complete and green.
  - Complexity: low–medium (mirrors the existing allowance soft-check
    exactly).

- [ ] **Task 2.2 — Implement `approve`-race guard (PRD §P1 §3).**
  Add a `fetch_allowance` call inside `do_approve` for the non-zero →
  non-zero detection: when `amount` is non-zero AND not `approve_max`,
  read `allowance(sender, spender)` via a local `try/except
  _core.RPCError` (same posture as Task 2.1). If the existing allowance
  is non-zero and not equal to the requested amount, queue an
  `approve_race` warning naming the existing allowance and the
  well-known SWC-114 race; on RPC failure, queue an
  `approve_race_check_skipped` warning. Build still emits JSON
  unmodified. Add `warn_approve_race` and `warn_approve_race_check_skipped`
  to `summary`. Add new `TestTxAssembly` cases for both warning paths
  and new `TestCliDispatch` cases. Update SKILL.md to mention the race
  posture and that operators concerned should approve 0 first.
  - Dependencies: Phase 1 complete. Independent of Task 2.1.
  - Complexity: low–medium.

- [ ] **Task 2.3 — Add `sepolia` and `holesky` to `build_send_eth.NETWORKS`
  (PRD §P1 §4; relaxes the P0 freeze).**
  This is the **only Phase 2 task that edits v1**. In
  `build_send_eth.py` add two `NETWORKS` entries: `"sepolia": (11155111,
  "https://ethereum-sepolia-rpc.publicnode.com")` and `"holesky":
  (17000, "https://ethereum-holesky-rpc.publicnode.com")` (chainIds
  verified per the sibling `eth-rpc` Phase 2 plan and `ethereum-lists`).
  In `test_build_send_eth.py` add two new `TestNetworkConfig` test cases
  (mirroring the existing `test_mainnet` / `test_hoodi` shape). Because
  `build_erc20.py` imports `_core.NETWORKS` (and its subcommand `choices`
  use `sorted(_core.NETWORKS)`), the new networks become available for
  free across all three ERC-20 ops with zero `build_erc20.py` changes.
  Update SKILL.md and README.md to list the four networks. Note
  Holesky's Sep 2025 deprecation in SKILL.md notes so operators prefer
  hoodi for new work (mirrors the sibling skill's posture).
  - Dependencies: Phase 1 complete and green (architecture Assumption 16
    explicitly limits the freeze to Phase 1).
  - Complexity: low (mechanical edit + tests + docs).

- [ ] **Task 2.4 — Implement `--summary-only` (PRD §P1 §5).**
  Add an optional `--summary-only` flag to every subparser (or a
  top-level pre-subcommand flag — choose at task review for argparse
  symmetry). In `cli_dispatch.main()`, after `dispatch` returns and
  before `print(json.dumps(tx, indent=2))`, short-circuit the JSON print
  if `args.summary_only` is True. The summary block on stderr and the
  warnings still print as usual. Add new `TestCliDispatch` case: with
  `--summary-only`, stdout is empty, stderr contains the summary, exit
  code is 0. Update SKILL.md / README.md with an example.
  - Dependencies: Phase 1 complete. Independent of Tasks 2.1–2.3.
  - Complexity: low (the architecture already separates `render_summary`
    pure from `print_summary` I/O; this is a two-line dispatch tweak per
    architecture Open Question 7).

### Phase 2 Exit Criteria (per task; Phase 2 is not all-or-nothing)

- Each task above lands on `develop` independently. Per-task gates:
  - `python3 -m unittest test_build_send_eth -v` is green
    (regression — including the new sepolia/holesky cases after Task 2.3).
  - `python3 -m unittest test_build_erc20 -v` is green (new and existing
    tests).
  - SKILL.md / README.md updated for any new flag, network, or warning
    surface.
  - The Phase 1 hoodi manual e2e still works as documented (the four
    Phase 2 changes are additive; none alters Phase 1 happy-path JSON).
- Phase 2 overall is "complete" when all four tasks have either landed
  or been explicitly dropped (e.g. balanceOf pre-check determined to add
  noise without value); none is a hard commitment.

### Phase 2 Risks / Assumptions

- **R6 (Low / Low) — Holesky endpoint deprecated mid-rollout.**
  Mitigation: SKILL.md flag-down on add; if publicnode retires the
  Holesky endpoint before Task 2.3 ships, drop Holesky from the dict
  edit.
- **R7 (Medium / Low) — Approve-race warning is too noisy in practice
  (every legitimate USDT approval flow triggers it).** Mitigation:
  Task 2.2 is independently shippable; if real usage shows it's noise,
  it can be reverted without affecting other Phase 2 tasks.
- **R8 (Low / Medium) — Editing v1 in Task 2.3 introduces an unrelated
  v1 regression (whitespace, gofmt-style noise).** Mitigation: limit
  Task 2.3's edit surface to the dict + new test cases; PR review
  diff-check; both test files run.
- **A13 — Network additions ride on the architecture's import strategy
  for free** — `build_erc20.py` re-derives `choices` from
  `_core.NETWORKS` and needs zero edit (architecture Assumption 16).

---

## Phase 3: P2 — `--revoke`, polished `bytes32` decode, optional `permit`

**Goal:** Bounded backlog for the PRD §P2 nice-to-haves. None of these is
committed; each ships only if a real user request justifies it. Phase 3
exists to keep the temptations named-but-bounded (PRD §Risks "scope
creep" — once ERC-20 lands every adjacent op looks tempting).

**Duration estimate:** Small per task; entirety is opportunistic — may
ship in fragments or not at all.

### Tasks

- [ ] **Task 3.1 — `approve --revoke` shorthand (PRD §P2 §3).**
  Add a `--revoke` flag to the `approve` subparser, mutually exclusive
  with both `--amount` and `--approve-max` in the same argparse group
  (now a three-way mutex). Internally, `--revoke` resolves to
  `amount_base = 0`; calldata is `approve(spender, 0)`. No new selector
  or encoder is needed (reuse `encode_approve`). The summary names the
  op as "revoke" rather than "approve" for clarity. New `TestCliDispatch`
  cases for the new mutex branches and the revoke happy path. Update
  SKILL.md and README.md with a worked example.
  - Dependencies: Phase 1 complete.
  - Complexity: low (a few lines of CLI plumbing + tests).

- [ ] **Task 3.2 — Polished `bytes32` symbol decode (PRD §P2 §4).**
  Strengthen `decode_symbol`'s `bytes32` fallback against the historical
  formats catalogued in `d-xo/weird-erc20` (MKR, DGD, etc.). Add new
  `TestAbiCodec` cases for each format. Failure mode unchanged: when
  even the polished fallback fails, return `None` (best-effort posture
  remains intact). Update SKILL.md notes to mention the broader
  legacy-token coverage. **Niche** — ship only if a real token surface
  surfaces in operator use.
  - Dependencies: Phase 1 complete.
  - Complexity: low (focused per-format coverage).

- [ ] **Task 3.3 — Optional `permit` (EIP-2612) builder (PRD §P2 §2;
  only if demand emerges).**
  Out of scope by default — `permit` requires signing a typed-data
  (EIP-712) digest, which expands the skill's responsibility beyond
  "build calldata" and likely brings in new selectors + a domain
  separator + careful nonce handling. If a real router workflow needs
  it, ship as a separate helper `build_erc20_permit.py` (matching the
  architecture's "service extraction path" note for new siblings) with
  its own SKILL.md routing entry. Has its own dependency conversation
  (could remain stdlib-only with hand-written EIP-712 hashing — same
  approach as the existing ABI codec — or may want `eth-account`-grade
  helpers; that's a fresh PRD if so).
  - Dependencies: Phase 1 complete; new sibling helper architecture
    (out of scope here).
  - Complexity: medium (EIP-712 typed-data hashing in stdlib is
    non-trivial).

### Phase 3 Exit Criteria

- Phase 3 has no fixed exit; each task is independently green per the
  same test-suite + manual-e2e bar.
- No Phase 3 task is committed to ship; this section is the bounded
  backlog.

### Phase 3 Risks / Assumptions

- **R9 (Low / Low) — `--revoke` confuses operators who expected it to
  also unset infinite approvals at routers that don't honour
  `approve(0)`.** Mitigation: SKILL.md note explains that revoke-via-
  approve-0 is the standard ERC-20 mechanism but not universally
  honoured by every spender.
- **R10 (Low / Medium) — `bytes32` polish chases an infinite tail of
  legacy formats.** Mitigation: scope Task 3.2 to a finite list (MKR,
  DGD); stop when the catalog is exhausted.
- **R11 (Low / Medium) — `permit` brings cryptographic complexity that
  doesn't fit stdlib-only.** Mitigation: defer to its own PRD; do not
  ship until that conversation closes.
- **A14 — Scope-creep gate: any new ERC-20 adjacent op (NFT mints,
  swaps, transferAndCall) lands as a new ticket, not as an in-phase
  add to Phase 3.**

---

## Dependency Graph

Inter-phase: Phase 1 has no upstream code dependencies inside this
project — only the four upstream artifacts (PRD, research, architecture,
existing skill files). Phase 2 tasks depend on Phase 1 being green; each
Phase 2 task is independent of every other Phase 2 task. Phase 3 tasks
depend on Phase 1; one optional Phase 3 task (3.3 `permit`) is its own
PRD.

```text
Phase 1 (P0 — must ship)
  1.1 skeleton ────► 1.2 abi_codec ─┬─► 1.4 contract_reads ───► 1.7 tx_assembly
                                    │                              │
              1.1 ────► 1.3 amount_codec ──► 1.6 summary  ────────►│
                                                                   │
                            1.4 ──► 1.5 gas_estimator ────────────►│
                                                                   ▼
                                                       1.8 cli_dispatch
                                                                   │
                                            1.2..1.8 ──► 1.9 test suite green
                                                                   │
                                                       1.8 ──► 1.10 SKILL.md
                                                       1.8 ──► 1.11 README.md
                                                                   │
                                            1.9 + 1.10 + 1.11 ──► 1.12 hoodi e2e
                                                                   │
                                            1.9 + 1.12 ──► 1.13 commit on develop
                                                                   │
                                                            [Phase 1 EXIT]

Phase 2 (P1 — independent follow-ups; Phase 1 EXIT gates them all)
  Phase 1 EXIT ──► 2.1 balanceOf pre-check on transfer
                  ► 2.2 approve-race guard
                  ► 2.3 sepolia + holesky (edits v1 NETWORKS + v1 tests)
                  ► 2.4 --summary-only

Phase 3 (P2 — opportunistic, bounded backlog)
  Phase 1 EXIT ──► 3.1 approve --revoke
                  ► 3.2 polished bytes32 symbol decode
                  ► 3.3 permit (own PRD; not committed)
```

Intra-phase blocking (Phase 1; the architecture's Layer 1 → 4 graph):

- 1.2 (abi_codec) and 1.3 (amount_codec) are Layer 1 leaves; both block
  only on 1.1 (skeleton).
- 1.4 (contract_reads) blocks on 1.2 (uses encoders + decoders).
- 1.5 (gas_estimator) blocks on 1.4 (uses the same `_core.parse_hex_int`
  + `_core.RPCError` patterns that 1.4 established).
- 1.6 (summary) blocks on 1.3 (uses `base_units_to_human` for the render).
- 1.7 (tx_assembly) blocks on 1.4, 1.5, 1.6 — composes Layer 2 into
  Layer 3.
- 1.8 (cli_dispatch) blocks on 1.7 — dispatches to `do_*` and prints.
- 1.9 (test suite green) blocks on 1.2–1.8 (all test classes are
  populated by then).
- 1.10 (SKILL.md) and 1.11 (README.md) block on 1.8 (CLI shape stable
  before documenting it).
- 1.12 (hoodi e2e) blocks on 1.9 (everything green), 1.10, and 1.11
  (the e2e steps are documented in the README before they run).
- 1.13 (commit) blocks on 1.9 + 1.12.

## Risk Register

| Risk | Phase | Impact | Likelihood | Mitigation |
|---|---|---|---|---|
| Hoodi ERC-20 unavailable or non-standard, e2e (Task 1.12) cannot run | 1 | High (Phase 1 cannot exit) | Low | Operator picks a known-good token in advance; PRD assumptions require the standard ERC-20 surface. |
| `eth_estimateGas` underestimates on a particular hoodi token; broadcast reverts on OOG | 1 | High | Low | +20% buffer + 300k cap (PRD §9); cap is ~5–6× measured OZ `transferFrom`-new (research §3). If a token still OOGs, the test is correctly surfacing a bad token rather than a build bug. |
| Golden-vector USDC mainnet calldata in Task 1.2 not hand-verifiable from research alone | 1 | Medium | Low | Cross-check via Etherscan input-data decode or a scratch `eth-abi` round-trip (script not committed); selectors themselves are research-verified. |
| publicnode rate-limits one of the 6–7 sequential RPC calls during the e2e | 1 | Medium | Low | Build is idempotent; operator retries; `_core.rpc_call` 15-second timeout is inherited from v1. |
| Future maintainer renames a symbol in `build_send_eth.py` between Phase 1 plan and ship, breaking `_core.<name>` | 1 | Medium | Low (in v1 timeframe); Medium (years later) | P0 freeze enforced; top-of-file docstring in `build_erc20.py` lists imported names; `test_build_erc20.py` fails at import-load on rename. (Architecture Risks row 2.) |
| Future maintainer adds a silent fallback to `eth_estimateGas` "for robustness" | 1+ | Very High (doomed tx burns gas budget) | Low in v1; Medium years later | Structural: absence of `try/except` around `estimate_gas` in middle layers is the invariant (architecture ADR-007). In-code multi-line comment explains why. `TestTxAssembly` + `TestCliDispatch` regression assert exit 1 + empty stdout on estimate failure. |
| ABI selector typo / bit-pattern bug in encoder | 1 | High (on-chain revert at broadcast) | Medium without tests; Low with tests | Selector constants tested for literal-equality (`TestAbiCodec`); calldata tested against golden vectors from research §01. |
| `decimals()` returns a hostile or oversized value | 1 | High (wrong base-unit amount; over/under-spend) | Low | `decode_decimals` masks to low byte + rejects `> MAX_DECIMALS` (36); test covers 0/6/18/24 OK and 37 rejected. Stderr summary shows `decimals=<N>` adjacent to base-unit amount as last-line-of-defense visual check. |
| Float drift in amount conversion (a future maintainer "fixes" the parser with `float()`) | 1+ | High (silent corruption of 18-decimal amounts) | Low | `human_to_base_units` is string-only; `TestAmountCodec` asserts `inspect.getsource(...)` does NOT contain `"float("` (architecture ADR-008). |
| Stdout pollution by a stray `print` in a helper breaks pipe-to-signer | 1+ | Medium | Low | All non-JSON output uses `sys.stderr.write` or `print(..., file=sys.stderr)`; `TestCliDispatch` asserts stdout is exactly the JSON on the happy path. |
| `--approve-max` warning lost to a stderr redirect | 1+ | High (unlimited approval signed unseen) | Operator choice | Documented in SKILL.md + README; warning is on stderr deliberately so it precedes the JSON in interactive use. |
| Phase 2 Task 2.3 v1 edit introduces an unrelated v1 regression (whitespace, gofmt-style noise) | 2 | Medium | Low | Limit Task 2.3's diff to the dict entries + new test cases; PR review diff-check; both test files run. |
| Approve-race warning (Task 2.2) is too noisy in practice (every legitimate USDT approve triggers it) | 2 | Low (operator UX friction) | Medium | Task 2.2 is independently shippable; can be reverted without affecting other Phase 2 tasks. |
| Phase 3 `permit` (Task 3.3) brings EIP-712 cryptographic complexity that breaks stdlib-only | 3 | Medium | Medium if it ships | Defer to its own PRD; ship as a separate helper `build_erc20_permit.py` if at all (architecture §Service Extraction Path). |
| Section-boundary drift over time inside `build_erc20.py` (no language-level enforcement of layering) | 1+ | Medium (architectural decay) | Medium over years | Test layout mirrors sections (architecture ADR-011); `# === Layer N: ===` banner comments are grep targets; PR review checklist asks "did this PR touch only one section?" |
| Scope creep — once ERC-20 lands, every adjacent op (NFT mints, swaps, transferAndCall) looks tempting | 1–3 | Medium | Medium | PRD §Out of Scope is explicit; Phase 3 lists the bounded follow-ups; new ideas land as new tickets, not in-phase. |

## Technical Spikes / Open Questions

Recorded from the architecture's Open Questions and from natural decisions
that need confirmation at execution time. None blocks Phase 1.

1. **Hoodi ERC-20 token selection for the e2e (Task 1.12).** No
   research-blessed token list exists; the operator picks one ahead of
   Task 1.12. Failure to pick a standard-surface token is a Phase 1
   blocker, not a code-fix.
2. **Golden-vector source for ABI calldata (Task 1.2).** Research §01
   names the USDC mainnet vectors as the reference. Decision deferred to
   Task 1.2 review: hand-construct from spec, cross-check against
   Etherscan, or use a scratch `eth-abi` round-trip (not committed)?
   Recommendation: hand-construct, then cross-check.
3. **`MAX_UINT256` placement** (architecture Open Question 4): in
   `abi_codec` or `amount_codec`? Architecture places it in
   `amount_codec`; confirmable at Task 1.3 review.
4. **`do_transfer_from` order of operations when `fetch_decimals` already
   failed** (architecture Open Question 5): the simplest behavior
   (`fetch_decimals` raise short-circuits the rest) is what the
   architecture specifies; confirmable at Task 1.7 review.
5. **`approve`-race upgrade from P1 to P0** (architecture Open Question
   2): adds one extra `allowance(sender, spender)` RPC call to
   `do_approve`. Current placement is P1 (Task 2.2); revisit only if
   operator demand says it should ship in P0.
6. **`balanceOf` pre-check on `transfer` upgrade from P1 to P0**
   (architecture Open Question 3): same posture as the approve-race
   question; current placement is P1 (Task 2.1).
7. **`--summary-only` argparse shape** (architecture Open Question 7):
   per-subparser flag or top-level pre-subcommand flag? Decision
   deferred to Task 2.4 review; the architecture has no preference
   beyond "it's a two-line change in `cli_dispatch.main()`."
8. **Promote `make_fake_rpc` test helper to a shared module**
   (architecture Assumption 14): deferred until the v1 read-only
   constraint is fully relaxed (which Phase 2 starts to do for
   `NETWORKS` but not for test helpers).
9. **Re-export `_core.RPCError` from `build_erc20.py`** (architecture
   Open Question 6): no external consumer exists yet; defer until a
   need surfaces.
10. **CI wiring of Python tests into `make test`** (sibling
    `eth-rpc` Open Q): currently `make test` is Go-focused; Python
    tests run manually. Out of scope here; a small repo-wide change to
    add a Python step is plausible, but not gated on this project.

## Decision Log

Key planning decisions captured here for traceability. Architectural
ADRs are in `plan/eth-tx-builder-erc20/architecture.md`; planning-time
decisions follow.

- **DL-1 — Three phases, mirroring PRD P0 / P1 / P2 priorities but
  shaped as an implementation sequence.** Phase 1 follows the
  architecture's Layer 1 → 4 dependency graph (Task 1.2 / 1.3 leaf
  codecs first, with golden-vector tests; then Layer 2; then Layer 3
  tx_assembly; then Layer 4 cli_dispatch; then docs; then hoodi e2e).
  Phase 2 / 3 are independent follow-ups.
- **DL-2 — Phase 1 is the only must-ship.** Phase 2 and Phase 3 are
  independent follow-ups; neither gates Phase 1's release. This keeps
  the critical path crisp and respects the PRD's P0/P1/P2 priority.
- **DL-3 — Phase 1 is self-contained.** Even without Phase 2 / Phase 3,
  Phase 1 delivers operator-usable, tested, documented value: all three
  ERC-20 ops reachable in one command on mainnet + hoodi with full
  safety summary.
- **DL-4 — P0 freeze ends at Phase 1's exit, not indefinitely.**
  Architecture Assumption 16 + Open Question 1 explicitly limit the
  "no edit `build_send_eth.py`" constraint to Phase 1 delivery. Phase 2
  Task 2.3 is the first task to edit v1 — it adds `sepolia`/`holesky`
  to `NETWORKS` directly + adds matching v1 test cases. Phase 2's
  exit criteria say so explicitly.
- **DL-5 — Leaf layers first; golden vectors gate Layer 2.** Task 1.2
  (`abi_codec`) ships with golden-vector calldata tests because a
  selector typo or bit-pattern bug is unrecoverable on-chain. Layer 2
  doesn't start until Layer 1 tests are green; this is sequencing for
  risk, not just for graph cleanliness.
- **DL-6 — Hoodi manual e2e is part of Phase 1 exit.** PRD success
  metric §1 requires that each of the three ops produces a JSON the
  signer accepts and that executes on hoodi. Task 1.12 is the
  confirmation; Phase 1 does not exit without it. (Mirrors the sibling
  `eth-rpc` plan's posture for its A5 hoodi chainId check.)
- **DL-7 — Single-stream, single-developer.** No parallel work-streams;
  no Stream A/B. The architecture's Layer 1 → 4 graph is the work
  order; the developer sequences through it.
- **DL-8 — Commit on `develop`, not `main`.** Per repo memory:
  `develop` is the integration branch; `main` is release-only. No PR /
  merge to `main` is taken unprompted. Suggested commit shape: one
  commit per Phase 1 task (or per Layer for tighter granularity),
  ending with docs + e2e so history reads top-down.
- **DL-9 — Phase 2 tasks are independent and order-agnostic.** Any of
  the four Phase 2 tasks can ship first; none blocks another. Task 2.3
  is the only one that edits v1 — sequence it at the operator's
  preference. The sibling `eth-rpc` Phase 2 plan adds the same two
  networks; if that lands first there, this task can become a
  rebase-only no-op.
- **DL-10 — Phase 3 is a bounded backlog, not a commitment.** Phase 3
  exists to name PRD §P2 follow-ups so they aren't lost; no Phase 3
  task is on the critical path. Task 3.3 (`permit`) explicitly defers
  to its own PRD.

## Assumptions

Recorded directly from the upstream artifacts; the PRD, research, and
architecture already encode defaults for each. No planning-time
deviations.

- The single new helper file is `build_erc20.py`; no extracted
  `_common.py` in Phase 1 (architecture ADR-001).
- `import build_send_eth as _core`; the 10-symbol contract is documented
  in `build_erc20.py`'s top-of-file docstring (architecture ADR-001).
- Seven labeled in-file sections in strict downward DAG; layers map 1:1
  onto PRD change axes (architecture ADR-002).
- `do_*` returns `(tx_dict, summary_ctx, warnings_list)`; CLI prints
  (architecture ADR-004).
- Hardcoded selectors with derivation comments; no runtime Keccak
  (architecture ADR-005).
- Structural fatal-vs-best-effort split via return signatures
  (`decode_symbol` → `Optional[str]`; everything else raises)
  (architecture ADR-006).
- `eth_estimateGas` no-fallback enforced by absence of `try/except`
  around `estimate_gas` in middle layers (architecture ADR-007).
- Integer-only amount conversion; no `float()` anywhere on the amount
  path; `inspect.getsource` negative assertion (architecture ADR-008).
- Stdout = JSON only; stderr = summary + warnings + errors (architecture
  ADR-009).
- Address validation once, at the CLI layer (architecture ADR-010).
- Test file mirrors module layout — one `TestCase` per Layer 1–4
  section (architecture ADR-011).
- `make_fake_rpc` test helper duplicated ~12 lines from
  `test_build_send_eth.py` into `test_build_erc20.py` with a one-line
  comment noting the v1 read-only constraint (architecture Assumption 14).
- Stdlib-only runtime (Python 3.8+); no new deps; no `requirements.txt`.
- `_core.NETWORKS` is the single source of truth; subcommand `choices`
  use `sorted(_core.NETWORKS)` so Phase 2 Task 2.3 picks up
  `sepolia`/`holesky` for free across all three ops with zero
  `build_erc20.py` change.
- P0 freeze applies to Phase 1 only (architecture Assumption 16); Phase 2
  Task 2.3 edits v1 with its own v1 test additions.
- Hoodi `chainId == 560048` is already inherited from v1's `NETWORKS`;
  no separate confirmation needed (v1 has been live on hoodi since the
  ETH-send path shipped).
- Selectors do not need a runtime verify step; hardcoded constants +
  golden-vector tests against USDC mainnet vectors deliver the same
  byte-level guarantee a runtime Keccak would (architecture Assumption 9).
- Operators run on macOS / Linux with Python 3.8+; Windows is not a v1
  target (architecture Assumption 17).
