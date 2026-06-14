# Phase 1: P0 — Core ERC-20 Builder, Tests, Docs, Hoodi E2E (MUST SHIP)

## Phase Overview

- **Goal:** Ship a self-contained, releasable `build_erc20.py` exposing the three
  ERC-20 movement subcommands (`transfer`, `approve`, `transfer-from`) on
  `mainnet` and `hoodi`. Every PRD P0 requirement is in scope: stdlib-only,
  hardcoded selectors with derivation comments, human-amount → base-unit
  conversion via a live `decimals()` read, `eth_estimateGas` with +20% buffer /
  300k cap / no fallback, `--approve-max` gated and warned loudly,
  `transfer-from` allowance soft-check, integer-only arithmetic, and the
  stdout=JSON / stderr=summary discipline. The phase also ships
  `test_build_erc20.py` mirroring the seven-section layout (one `TestCase` per
  Layer 1–4 section), updates to `SKILL.md` and `README.md`, and a hoodi
  end-to-end against a real ERC-20.
- **Issue count:** 12 issues, 23 total points
- **Estimated duration:** ~16 days single-stream
- **Entry criteria:**
  - PRD (`plan/eth-tx-builder-erc20/prd.md`), architecture
    (`plan/eth-tx-builder-erc20/architecture.md`), and project plan
    (`plan/eth-tx-builder-erc20/project-plan.md`) are approved and in-tree.
  - Local Python 3.8+ on `PATH`.
  - Existing v1 tests pass at HEAD:
    `cd .claude/skills/eth-tx-builder && python3 -m unittest test_build_send_eth -v`.
  - Network access to `https://ethereum-hoodi-rpc.publicnode.com` (needed by
    Issue 1.10b's manual e2e).
  - A real, standard-surface ERC-20 token deployed on hoodi that the operator
    can read against and ideally transfer / approve against (PRD success
    metrics §1, §2).
  - A connected `eth-signer-mcp` session (or a keystore the operator can paste
    JSON into manually) for the e2e sign + broadcast steps.
- **Exit criteria (ties directly to PRD success metrics + project plan §Phase 1
  Exit):**
  - **Functional coverage:** each of `transfer`, `approve`, `transfer-from`
    produces a `TxRequest` JSON that `eth-signer-mcp` `sign_transaction`
    accepts and, when broadcast on hoodi, executes successfully on-chain (PRD
    success metric §1; verified by Issue 1.10b).
  - **Real-token `decimals()`:** the `decimals()` RPC read works against a
    real ERC-20 deployed on hoodi (PRD success metric §2; verified by Issue
    1.10b).
  - **Zero new dependencies:** `python3 build_erc20.py --help` works on a
    fresh stdlib-only Python 3.8+ install; no `requirements.txt`,
    `pyproject.toml`, or vendored package is added (PRD success metric §3).
  - **No regression in ETH-send:**
    `cd .claude/skills/eth-tx-builder && python3 -m unittest test_build_send_eth -v`
    passes with zero edits to `build_send_eth.py` or `test_build_send_eth.py`
    (PRD success metric §4; the P0 freeze).
  - **Test coverage of the new helper:**
    `python3 -m unittest test_build_erc20 -v` is green across all seven
    `TestCase` classes and covers every case enumerated in PRD §P0 §20.
  - **No-fallback regression check passes:** `TestTxAssembly` and
    `TestCliDispatch` assert that on `estimate_gas` raising, exit code is 1
    and stdout is empty (architecture ADR-007).
  - **No-`float` regression check passes:**
    `inspect.getsource(b.human_to_base_units)` does NOT contain the substring
    `"float("` (architecture ADR-008).
  - **Stdout / stderr discipline:** `TestCliDispatch` asserts that on the
    happy path stdout contains exactly the JSON and the summary + warnings
    live on stderr (architecture ADR-009).
  - `SKILL.md` updated with the routing step, split inputs, refreshed "Out of
    scope" list, and references both helpers.
  - `README.md` lists `build_erc20.py` + `test_build_erc20.py`, includes the
    hoodi manual e2e for all three ops, and the test invocation snippet
    covers both test files.
  - Phase 1 commit landed on `develop` (`develop` is the integration branch;
    do NOT PR / merge to `main` unprompted).

### Phase Assumptions (recorded from upstream artifacts; not asks of the implementer)

- **A1 — Single new file.** `.claude/skills/eth-tx-builder/build_erc20.py`
  + `test_build_erc20.py`. No `_common.py` extraction in Phase 1 (architecture
  ADR-001).
- **A2 — `import build_send_eth as _core`.** The 10-symbol contract
  (`NETWORKS`, `network_config`, `rpc_call`, `validate_hex_address`,
  `parse_hex_int`, `compute_max_fee`, `fetch_nonce`, `fetch_base_fee`,
  `fetch_tip`, `RPCError`) is documented in `build_erc20.py`'s top-of-file
  docstring (architecture ADR-001).
- **A3 — Seven labeled in-file sections in strict downward DAG** (architecture
  ADR-002). Layer 1: `abi_codec`, `amount_codec`. Layer 2: `contract_reads`,
  `gas_estimator`, `summary`. Layer 3: `tx_assembly`. Layer 4: `cli_dispatch`.
  No peer imports within a layer; only Layer 3 fans out across Layer 2.
- **A4 — Pure functions with injected `rpc=_core.rpc_call`** on every
  chain-touching function (architecture ADR-003).
- **A5 — `do_*` returns `(tx_dict, summary_ctx, warnings_list)`; CLI prints**
  (architecture ADR-004). `do_*` never writes to stdout/stderr.
- **A6 — Hardcoded selectors with derivation comments; no runtime Keccak**
  (architecture ADR-005). `hashlib.sha3_256` is NOT Keccak.
- **A7 — Structural fatal-vs-best-effort split via return signatures**
  (architecture ADR-006). `decode_symbol` → `Optional[str]`; everything else
  raises.
- **A8 — `eth_estimateGas` no-fallback enforced by absence of `try/except`**
  around `estimate_gas` in middle layers (architecture ADR-007). The
  *absence* of a handler is itself the invariant.
- **A9 — Integer-only amount conversion; no `float()`** anywhere on the
  amount path; `inspect.getsource` negative assertion (architecture ADR-008).
- **A10 — Stdout = JSON only; stderr = summary + warnings + errors**
  (architecture ADR-009). `error:` lowercase (matches v1) for fatal exit-1;
  `WARNING:` allcaps for soft warnings; bare text for the summary block.
- **A11 — Address validation once, at the CLI layer** via
  `_core.validate_hex_address` (architecture ADR-010). `do_*` accepts
  pre-validated hex.
- **A12 — Test file mirrors module layout — one `TestCase` per Layer 1–4
  section** (architecture ADR-011): `TestAbiCodec`, `TestAmountCodec`,
  `TestContractReads`, `TestGasEstimator`, `TestSummary`, `TestTxAssembly`,
  `TestCliDispatch`.
- **A13 — `make_fake_rpc` test helper is duplicated** (~12 lines) from
  `test_build_send_eth.py` into `test_build_erc20.py` with a one-line comment
  noting the v1 read-only constraint (architecture Assumption 14).
- **A14 — `--approve-max` mutual exclusion enforced at argparse** via
  `add_mutually_exclusive_group(required=True)` for `--amount` /
  `--approve-max` (architecture Assumption 13).
- **A15 — `--network` subcommand `choices` are `sorted(_core.NETWORKS)`** so
  Phase 2 network additions (sepolia / holesky) propagate for free.
- **A16 — P0 freeze is absolute for Phase 1.** `build_send_eth.py` and
  `test_build_send_eth.py` are bit-for-bit untouched. The freeze relaxes only
  in Phase 2 (architecture Assumption 16).
- **A17 — Commit on `develop`.** `develop` is the integration branch; no PR
  or merge to `main` unprompted (per repo memory).
- **A18 — Python 3.8+ runtime, stdlib only.** Allowed imports are the v1 set:
  `argparse`, `json`, `re` (only if needed by `amount_codec`), `sys`,
  `urllib.request` (transitive via `_core.rpc_call`).
- **A19 — `MAX_UINT256` lives in `amount_codec`** (it is an amount value, not
  an ABI encoding rule; architecture Open Question 4).

## Phase Summary

| Issue | Title | Points | Blocked by | Scope | Files |
|-------|-------|--------|------------|-------|-------|
| 1.1 | Skeleton: `build_erc20.py` + `test_build_erc20.py` shells + `_core` import + section banners | 1 | — | 1 day | `build_erc20.py`, `test_build_erc20.py` (both new shells) |
| 1.2 | Layer 1 `abi_codec` — selectors, encoders, decoders + `TestAbiCodec` | 3 | 1.1 | 2 days | `build_erc20.py` (`abi_codec` section), `test_build_erc20.py` (`TestAbiCodec`) |
| 1.3 | Layer 1 `amount_codec` + `TestAmountCodec` (no-`float` invariant) | 2 | 1.1 | 1-2 days | `build_erc20.py` (`amount_codec` section), `test_build_erc20.py` (`TestAmountCodec`) |
| 1.4 | Layer 2 `contract_reads` + `TestContractReads` | 2 | 1.1, 1.2 | 1-2 days | `build_erc20.py` (`contract_reads` section), `test_build_erc20.py` (`TestContractReads`) |
| 1.5 | Layer 2 `gas_estimator` (no-fallback) + `TestGasEstimator` | 2 | 1.1, 1.4 | 1-2 days | `build_erc20.py` (`gas_estimator` section), `test_build_erc20.py` (`TestGasEstimator`) |
| 1.6 | Layer 2 `summary` (warning dispatcher + stderr renderer) + `TestSummary` | 2 | 1.1, 1.3 | 1-2 days | `build_erc20.py` (`summary` section), `test_build_erc20.py` (`TestSummary`) |
| 1.7 | Layer 3 `tx_assembly` — three `do_*` composers + `TestTxAssembly` | 3 | 1.3, 1.4, 1.5, 1.6 | 2 days | `build_erc20.py` (`tx_assembly` section), `test_build_erc20.py` (`TestTxAssembly`) |
| 1.8 | Layer 4 `cli_dispatch` — argparse + dispatch + `TestCliDispatch` | 3 | 1.1, 1.7 | 2 days | `build_erc20.py` (`cli_dispatch` section), `test_build_erc20.py` (`TestCliDispatch`) |
| 1.9 | Full test-suite green (new + v1 regression) | 1 | 1.2–1.8 | 0.5 day | (verification; no source edits) |
| 1.10a | `SKILL.md` + `README.md` docs updates (prose only, no live network) | 1 | 1.8 | 1 day | `.claude/skills/eth-tx-builder/SKILL.md`, `.claude/skills/eth-tx-builder/README.md` |
| 1.10b | Hoodi manual e2e for all three ops (`transfer`, `approve`, `transfer-from`) | 2 | 1.9, 1.10a | 1-2 days | `.claude/skills/eth-tx-builder/README.md` (fill in e2e transcript placeholders) |
| 1.11 | Commit on `develop` | 1 | 1.9, 1.10a, 1.10b | 0.5 day | (git only) |

## Phase Execution Plan

| Day | Issue |
|-----|-------|
| 1 | 1.1 Skeleton: `build_erc20.py` + `test_build_erc20.py` shells |
| 2 | 1.2 abi_codec selectors + encoders |
| 3 | 1.2 cont. decoders + `TestAbiCodec` golden vectors |
| 4 | 1.3 amount_codec + `TestAmountCodec` no-`float` invariant |
| 5 | 1.4 contract_reads + `TestContractReads` |
| 6 | 1.5 gas_estimator + `TestGasEstimator` |
| 7 | 1.6 summary + `TestSummary` |
| 8 | 1.7 tx_assembly `do_*` composers |
| 9 | 1.7 cont. `TestTxAssembly` cross-section integration |
| 10 | 1.8 cli_dispatch argparse + dispatcher |
| 11 | 1.8 cont. `TestCliDispatch` end-to-end CLI tests |
| 12 | 1.9 Full test-suite green (v1 regression + new) |
| 13 | 1.10a `SKILL.md` + `README.md` prose updates (skeleton + placeholders, no live network) |
| 14 | 1.10b Hoodi pre-flight + `transfer` e2e + `approve` e2e (wait for `approve` to mine) |
| 15 | 1.10b cont. `transfer-from` e2e + record three transcripts in README |
| 16 | 1.11 Commit on `develop` |

---

## Issues

### Issue 1.1: Skeleton — `build_erc20.py` shell + `test_build_erc20.py` shell + `_core` import + section banners

- **Points:** 1
- **Type:** chore
- **Priority:** P0
- **Blocked by:** none
- **Blocks:** 1.2, 1.3, 1.4, 1.5, 1.6, 1.8
- **Scope:** 1 day

**Description:**
Create the empty shells of BOTH `build_erc20.py` AND `test_build_erc20.py`
in `.claude/skills/eth-tx-builder/`. The source shell gets the shebang,
module docstring (which restates the imported `_core` 10-symbol contract
per architecture §Module Details), the stdlib imports, the `import
build_send_eth as _core` edge, the seven Layer 1–4 section banner comments
in dependency order top to bottom, and a stub `main()` returning `0`. The
test shell gets `import unittest`, `import build_erc20 as b`, the
`make_fake_rpc` helper stub (with the mandated one-line v1-read-only
comment per architecture Assumption 14), and `if __name__ == "__main__":
unittest.main()` at the bottom. Verify both modules import cleanly without
running anything. Creating the test shell here removes the test-file
creation dependency tangle: Issues 1.2 / 1.3 / 1.4 / 1.5 / 1.6 / 1.8 then
ONLY add their own `TestCase` class to an already-existing file, so none
of them carry an implicit "Blocked by 1.2" for file creation.

**Implementation Notes:**
- New files to create: `.claude/skills/eth-tx-builder/build_erc20.py`,
  `.claude/skills/eth-tx-builder/test_build_erc20.py`.
- Files NOT to modify (P0 freeze):
  `.claude/skills/eth-tx-builder/build_send_eth.py`,
  `.claude/skills/eth-tx-builder/test_build_send_eth.py`.
- `build_erc20.py` shell:
  - Shebang: `#!/usr/bin/env python3`.
  - Module docstring lists the `_core` symbol contract: `NETWORKS`,
    `network_config`, `rpc_call`, `validate_hex_address`, `parse_hex_int`,
    `compute_max_fee`, `fetch_nonce`, `fetch_base_fee`, `fetch_tip`,
    `RPCError`. This makes a future v1 rename surface as
    `ImportError`/`AttributeError` at test load-time, clearly attributable
    (architecture ADR-001).
  - Imports: `import argparse`, `import json`, `import sys`. `re` is added
    in Issue 1.3 when `amount_codec` needs it. `import build_send_eth as
    _core` is the one cross-module edge.
  - Section banners in order (with closing banners) — `abi_codec`,
    `amount_codec`, `contract_reads`, `gas_estimator`, `summary`,
    `tx_assembly`, `cli_dispatch`. Format: `# === Layer N: <name> ===` /
    `# === end Layer N: <name> ===` (architecture §Module Details).
  - Stub `main(argv=None)` returns `0`.
  - Entry guard: `if __name__ == "__main__": sys.exit(main())`.
- `test_build_erc20.py` shell:
  - `import unittest`.
  - `import build_erc20 as b` (the alias the per-section test classes will
    consume).
  - `make_fake_rpc(responses)` helper stub with the mandated one-line
    comment: `# Duplicated from test_build_send_eth.py because the v1 test
    file is read-only for Phase 1 (architecture A14).` Ship the full ~12-
    line helper here so Issue 1.4 / 1.5 / 1.7 can use it without re-adding
    it; the body matches the v1 helper. (If preferred, the body can be a
    minimal stub and Issue 1.7 fleshes it out — but Issue 1.7 calls it for
    `do_*` integration tests, so shipping the full body here is the
    cleanest choice.)
  - No `TestCase` classes yet — those land in their owning issues
    (`TestAbiCodec` in 1.2, `TestAmountCodec` in 1.3, etc.).
  - Bottom of file: `if __name__ == "__main__": unittest.main()`.
- Watch out for: `build_send_eth.py` is import-safe (its `if __name__ ==
  "__main__":` guard prevents `main()` from running on import; architecture
  Assumption 2). Confirm by `python3 -c "import build_erc20"` from the
  skill directory. The empty `test_build_erc20.py` (no `TestCase` classes)
  is valid: `python3 -m unittest test_build_erc20 -v` reports `Ran 0
  tests in ... OK` — that is the expected pre-1.2 state.

**Acceptance Criteria:**
- [x] `build_erc20.py` exists at `.claude/skills/eth-tx-builder/build_erc20.py`.
- [x] Top-of-file docstring lists the 10 `_core` symbols the module will
      consume.
- [x] `import build_send_eth as _core` is the only non-stdlib import in
      `build_erc20.py`.
- [x] Seven Layer 1–4 section banner pairs exist in dependency order top to
      bottom, each with an opening `# === Layer N: <name> ===` and a closing
      `# === end Layer N: <name> ===` line.
- [x] Stub `main()` returns `0`; entry guard `if __name__ == "__main__":
      sys.exit(main())` is present.
- [x] `test_build_erc20.py` exists at
      `.claude/skills/eth-tx-builder/test_build_erc20.py`.
- [x] `test_build_erc20.py` contains `import unittest`, `import build_erc20
      as b`, the `make_fake_rpc` helper with the mandated one-line v1-read-
      only comment (architecture A14), and `if __name__ == "__main__":
      unittest.main()` at the bottom.
- [x] `test_build_erc20.py` contains NO `TestCase` classes at this point
      (those are added by the owning issues).
- [x] From the skill directory: `python3 build_erc20.py` exits 0.
- [x] From the skill directory: `python3 -c "import build_erc20"` succeeds
      with no output.
- [x] From the skill directory: `python3 -c "import test_build_erc20"`
      succeeds with no output.
- [x] From the skill directory: `python3 -m unittest test_build_erc20 -v`
      runs and reports `Ran 0 tests` + `OK` (no test classes yet).
- [x] `build_send_eth.py` and `test_build_send_eth.py` are byte-identical to
      their pre-Phase-1 SHAs (`git diff` shows no changes).

**Testing Notes:**
- This issue creates both shell files so that downstream issues
  (1.2 / 1.3 / 1.4 / 1.5 / 1.6 / 1.8) each ONLY add a single `TestCase`
  class. None of them carry an implicit "blocked by 1.2 to create the
  file" dependency anymore — that tangle is dissolved here.
- Manual smoke: `python3 build_erc20.py` from the skill dir exits 0;
  `python3 -m unittest test_build_erc20 -v` reports 0 tests OK.

---

### Issue 1.2: Layer 1 `abi_codec` — selectors, encoders, decoders + `TestAbiCodec` golden vectors

- **Points:** 3
- **Type:** feature
- **Priority:** P0
- **Blocked by:** 1.1
- **Blocks:** 1.4, 1.7
- **Scope:** 2 days

**Description:**
Implement the `abi_codec` section: six selector constants with derivation
comments, the address / uint256 / call-pack helpers, the three calldata
encoders for the writes (`transfer` / `approve` / `transferFrom`), the
read-call encoders (`decimals` / `symbol` / `allowance`), and the three
decoders (`decimals` low-byte + sanity cap, `symbol` standard ABI string
with `bytes32`-null-trim fallback returning `Optional[str]`, `allowance`
single uint256 word). Ship with `TestAbiCodec` doing selector-equality and
bit-pattern golden-vector checks against the USDC mainnet vectors in
research §01.

**Implementation Notes:**
- Files affected: `build_erc20.py` (Layer 1 `abi_codec` section),
  `test_build_erc20.py` (ADD `TestAbiCodec` class only — the file shell,
  imports, `make_fake_rpc` helper, and `unittest.main()` entry already
  exist from Issue 1.1; other test classes ship with their owning issues).
- Internal sub-structure (≈ 2 days): day 1 = the six selectors + the
  three writer encoders + the three reader encoders + the `_encode_*` /
  `_pack_call` helpers; day 2 = the three decoders (including the
  asymmetric `decode_symbol` `Optional[str]` contract) + `TestAbiCodec`
  golden vectors.
- Constants (all module-level, with one-line derivation comments matching
  architecture §Module Details):
  - `SEL_TRANSFER       = "0xa9059cbb"` — `keccak256("transfer(address,uint256)")[:4]`
  - `SEL_APPROVE        = "0x095ea7b3"` — `keccak256("approve(address,uint256)")[:4]`
  - `SEL_TRANSFER_FROM  = "0x23b872dd"` — `keccak256("transferFrom(address,address,uint256)")[:4]`
  - `SEL_DECIMALS       = "0x313ce567"` — `keccak256("decimals()")[:4]`
  - `SEL_SYMBOL         = "0x95d89b41"` — `keccak256("symbol()")[:4]`
  - `SEL_ALLOWANCE      = "0xdd62ed3e"` — `keccak256("allowance(address,address)")[:4]`
  - `MAX_DECIMALS       = 36` (PRD/research §1.4 hostile-value ceiling).
- Encoders:
  - `_encode_address(addr_hex)` — strip leading `0x`, lowercase, left-pad
    with 24 zero hex chars to 64 hex chars.
  - `_encode_uint256(n)` — 64-hex-char left zero-padded; reject `n < 0` and
    `n >= 2**256` with `ValueError`.
  - `_pack_call(selector_hex, *args_hex)` — concatenate selector + arg
    words, return with `0x` prefix.
  - `encode_transfer(to, amount_base)` — `_pack_call(SEL_TRANSFER,
    _encode_address(to), _encode_uint256(amount_base))`.
  - `encode_approve(spender, amount_base)` — analogous.
  - `encode_transfer_from(from_, to, amount_base)` — analogous (three args).
  - `encode_decimals_call()` → `SEL_DECIMALS`.
  - `encode_symbol_call()` → `SEL_SYMBOL`.
  - `encode_allowance_call(holder, spender)` —
    `_pack_call(SEL_ALLOWANCE, _encode_address(holder), _encode_address(spender))`.
- Decoders:
  - `decode_decimals(hex_result)` — `int(hex_result, 16) & 0xff`. Raise
    `ValueError("token decimals() returned suspicious value %d (cap %d)"
    % (value, MAX_DECIMALS))` if `value > MAX_DECIMALS`.
  - `decode_symbol(hex_result) -> Optional[str]` — try standard ABI `string`
    decode (32-byte offset, 32-byte length, UTF-8 bytes padded to multiple
    of 32). On any failure, fall back to a null-trimmed UTF-8 read of the
    first 32 bytes (handles legacy `bytes32` tokens like MKR). If even that
    yields a non-printable result, return `None`. **Asymmetric case: returns
    `None` rather than raising (architecture ADR-006).**
  - `decode_allowance(hex_result)` — `int(hex_result, 16)` (single uint256).
- Golden vectors: research §01-abi-encoding names the USDC mainnet vectors.
  Hand-construct selector + calldata strings from the spec, then cross-check
  against an Etherscan input-data decode of a known USDC transfer (Phase 1
  Risk R3 mitigation). Do NOT commit any scratch `eth-abi` round-trip
  script — the golden values themselves are the artifact.
- Test class `TestAbiCodec` (`test_build_erc20.py`, new file):
  - Selector constants match the canonical hex strings (literal equality).
  - `_encode_address` of a mixed-case input gives a lowercase, left-padded
    64-hex-char string.
  - `_encode_uint256`: `0`, `1`, `2**256 - 1` round-trip; `-1` raises
    `ValueError`; `2**256` raises `ValueError`.
  - `_pack_call`: USDC `transfer` calldata bit-equality vs the research §01
    golden vector.
  - `encode_transfer`, `encode_approve`, `encode_transfer_from`: bit-pattern
    equality vs golden vectors (or vs `_pack_call(SEL_*, _encode_address(...),
    _encode_uint256(...))` reconstructed in the test for parity).
  - `encode_allowance_call`: bit-pattern equality.
  - `encode_decimals_call` and `encode_symbol_call`: equal to their
    selectors.
  - `decode_decimals`: `0`, `6`, `18`, `24` OK; `37` raises `ValueError`;
    `255` (a one-byte hostile value masked to low byte) is rejected.
  - `decode_symbol`: standard ABI string with `"USDC"` payload returns
    `"USDC"`; `bytes32` MKR-style (`"MKR\x00...\x00"`) returns `"MKR"`;
    completely malformed hex returns `None` (no raise).
  - `decode_allowance`: `0`, `2**256 - 1`.

**Acceptance Criteria:**
- [x] The six `SEL_*` constants are defined as module-level hex strings with
      one-line derivation comments.
- [x] `MAX_DECIMALS = 36` is defined.
- [x] `_encode_address`, `_encode_uint256`, `_pack_call`, `encode_transfer`,
      `encode_approve`, `encode_transfer_from`, `encode_decimals_call`,
      `encode_symbol_call`, `encode_allowance_call` are implemented.
- [x] `decode_decimals`, `decode_symbol`, `decode_allowance` are
      implemented. `decode_symbol` returns `Optional[str]`; everything else
      raises on bad input.
- [x] `test_build_erc20.py` (already created in Issue 1.1) gains a
      `TestAbiCodec` class; the file's pre-existing imports, `make_fake_rpc`
      helper, and `if __name__ == "__main__": unittest.main()` block are
      preserved untouched.
- [x] `TestAbiCodec` includes selector-equality, encoder bit-pattern golden
      vectors (at least one calldata vector per writer encoder), decoder
      table tests, and the `decimals > MAX_DECIMALS` rejection.
- [x] From the skill directory: `python3 -m unittest
      test_build_erc20.TestAbiCodec -v` is green.
- [x] `build_send_eth.py` and `test_build_send_eth.py` are byte-identical
      to their pre-Phase-1 SHAs.

**Testing Notes:**
- Hand-verify the USDC mainnet vectors against an Etherscan input-data
  decode (Phase 1 Risk R3). Do not commit any round-trip helper script.
- The decoders run on hex-string inputs — tests can pass synthetic 32-byte
  words built from `("0x" + "0" * 62 + "06")` style literals.
- Test isolation: this is the only `TestCase` populated in this issue;
  classes for later sections are added by their owning issues.

---

### Issue 1.3: Layer 1 `amount_codec` + `TestAmountCodec` (no-`float` invariant)

- **Points:** 2
- **Type:** feature
- **Priority:** P0
- **Blocked by:** 1.1
- **Blocks:** 1.6, 1.7 (direct: `human_to_base_units` + `MAX_UINT256`)
- **Scope:** 1-2 days

**Description:**
Implement the `amount_codec` section: `MAX_UINT256`, the integer-only
`human_to_base_units(amount_str, decimals)` converter, and the
`base_units_to_human(amount, decimals)` renderer for the summary. Ship with
`TestAmountCodec` covering PRD-listed golden vectors, negative-path
rejections, a round-trip on the same vectors, the `MAX_UINT256` constant
check, and the negative invariant assertion via `inspect.getsource` that
the substring `"float("` is absent from the conversion function body
(architecture ADR-008).

**Implementation Notes:**
- Files affected: `build_erc20.py` (Layer 1 `amount_codec` section),
  `test_build_erc20.py` (add `TestAmountCodec`).
- Add `import re` at the top of `build_erc20.py` (the converter uses
  `re.fullmatch`).
- Constants:
  - `MAX_UINT256 = (1 << 256) - 1` (architecture Open Question 4 places this
    in `amount_codec`, not `abi_codec`).
- `human_to_base_units(amount_str, decimals) -> int`:
  - Validate `amount_str` is a `str`; reject empty.
  - Reject leading `-` (negatives) with a clear error.
  - Split on `"."`. If the split yields more than 2 parts, reject (multi-dot).
  - Validate the integer and fractional halves with
    `re.fullmatch(r"\d*", ...)`. Reject non-digit characters.
  - Reject `len(frac) > decimals` with `error: amount has more fractional
    digits (N) than token decimals (M)`.
  - Right-pad the fractional half with `'0'` to exactly `decimals` digits.
  - Concatenate `int_part + frac_padded`; if both halves are empty after
    splitting (e.g. `"."`), reject.
  - Return `int(concat, 10)`. `0` is permitted (PRD §6 allows zero-transfer
    and zero-approve).
  - **Hard rule:** no `float()`, no `decimal.Decimal`, no
    `fractions.Fraction`. The conversion path is `str → str → int`.
- `base_units_to_human(amount, decimals) -> str`:
  - Renderer for the summary. Integer-string manipulation: render `amount`
    as a left-zero-padded decimal string of at least `decimals + 1` chars,
    insert `"."` `decimals` from the right, strip trailing zeros, strip
    trailing `.`. `decimals == 0` returns `str(amount)`.
- Test class `TestAmountCodec`:
  - Golden vectors (positive): `("0", 6) → 0`; `("0.0", 6) → 0`;
    `("1", 18) → 10**18`; `("1.5", 6) → 1_500_000`; `("0.000001", 18) →
    10**12`; `("1000000.5", 6) → 1_000_000_500_000`.
  - Rejections: `""`, `"-1"`, `"1..5"`, `"1.5.0"`, `"abc"`,
    `"1.0000001"` at `decimals=6` each raise `ValueError`. Assert via
    `assertRaises(ValueError, ...)`.
  - `base_units_to_human` round-trip on the positive vectors.
  - `MAX_UINT256 == (1 << 256) - 1`.
  - **Negative invariant:** `import inspect; src =
    inspect.getsource(b.human_to_base_units); self.assertNotIn("float(",
    src)`. This is the ADR-008 guard.

**Acceptance Criteria:**
- [x] `MAX_UINT256` is defined with the exact value `(1 << 256) - 1`.
- [x] `human_to_base_units(amount_str, decimals)` is implemented as
      string-only manipulation; no `float()`, `decimal.Decimal`, or
      `fractions.Fraction` anywhere in the function body.
- [x] `base_units_to_human(amount, decimals)` is implemented and
      round-trips the positive vectors.
- [x] Empty string, negatives, multi-dot, non-digit, and `len(frac) >
      decimals` inputs raise `ValueError` with clear messages.
- [x] `TestAmountCodec` covers all PRD-listed positive vectors and
      negative-path rejections.
- [x] `TestAmountCodec` asserts via `inspect.getsource(b.human_to_base_units)`
      that the substring `"float("` is absent from the function body.
- [x] From the skill directory: `python3 -m unittest
      test_build_erc20.TestAmountCodec -v` is green.
- [x] `build_send_eth.py` and `test_build_send_eth.py` are unchanged.

**Testing Notes:**
- The `inspect.getsource` assertion is the load-bearing regression check;
  future maintainers "fixing" the converter with `float()` will fail the
  test immediately.
- The 18-decimal vectors are the realistic stress case (ETH-like tokens).
  Add `("0.123456789012345678", 18) → 123_456_789_012_345_678` if helpful
  for confidence.

---

### Issue 1.4: Layer 2 `contract_reads` + `TestContractReads`

- **Points:** 2
- **Type:** feature
- **Priority:** P0
- **Blocked by:** 1.1 (test shell), 1.2 (`encode_*_call` + `decode_*` consumed)
- **Blocks:** 1.5, 1.7
- **Scope:** 1-2 days

**Description:**
Implement the `contract_reads` section: `fetch_decimals` (FATAL on
`RPCError`; raises on `> MAX_DECIMALS`), `fetch_symbol` (best-effort;
returns `Optional[str]`; swallows all exceptions), `fetch_allowance`
(propagates `_core.RPCError`; soft-check posture is the caller's job). Ship
with `TestContractReads` mocking the injected `rpc` callable.

**Implementation Notes:**
- Files affected: `build_erc20.py` (Layer 2 `contract_reads` section),
  `test_build_erc20.py` (add `TestContractReads`).
- All three fetchers take `rpc` as a kwarg defaulting to `_core.rpc_call`
  (architecture ADR-003).
- `fetch_decimals(rpc, url, token) -> int`:
  - Build call object `{"to": token, "data": encode_decimals_call()}`.
  - Invoke `rpc(url, "eth_call", [call_obj, "latest"])`.
  - Pass the hex result through `decode_decimals`.
  - **No try/except.** `_core.RPCError` propagates by design (FATAL —
    architecture ADR-006).
- `fetch_symbol(rpc, url, token) -> Optional[str]`:
  - Build call object `{"to": token, "data": encode_symbol_call()}`.
  - Invoke `rpc(url, "eth_call", [call_obj, "latest"])`.
  - Pass the hex result through `decode_symbol`.
  - **Catch every exception** (`_core.RPCError`, `ValueError`, any
    `Exception` raised during decode); return `None` on any failure.
    Best-effort posture (architecture ADR-006).
- `fetch_allowance(rpc, url, token, holder, spender) -> int`:
  - Build call object `{"to": token, "data":
    encode_allowance_call(holder, spender)}`.
  - Invoke `rpc(url, "eth_call", [call_obj, "latest"])`.
  - Pass the hex result through `decode_allowance`.
  - **No try/except.** `_core.RPCError` propagates. The soft-check
    `try/except` lives in `tx_assembly.do_transfer_from` (Issue 1.7).
- Test class `TestContractReads`:
  - `fetch_decimals`: mock `rpc` returns `"0x" + "0" * 62 + "06"` → `6`;
    mock `rpc` raises `_core.RPCError("boom")` → propagates (`assertRaises`).
  - `fetch_symbol`: mock `rpc` returns a USDC-style standard ABI string →
    `"USDC"`; mock `rpc` raises `_core.RPCError` → returns `None`; mock
    `rpc` returns malformed hex such that `decode_symbol` returns `None`
    → returns `None`.
  - `fetch_allowance`: mock `rpc` returns `"0x" + "0" * 62 + "0a"` → `10`;
    mock `rpc` raises `_core.RPCError` → propagates.
- Use `unittest.mock.Mock()` for `rpc`; assert on `mock_rpc.call_args` to
  confirm the right method (`"eth_call"`) and block tag (`"latest"`) are
  used.

**Acceptance Criteria:**
- [x] `fetch_decimals`, `fetch_symbol`, `fetch_allowance` are implemented
      with `rpc=_core.rpc_call` kwarg defaults.
- [x] `fetch_decimals` raises on `_core.RPCError` propagation and on
      `decimals > MAX_DECIMALS`.
- [x] `fetch_symbol` returns `Optional[str]`, never raises, returns `None`
      on RPC failure or decode failure.
- [x] `fetch_allowance` propagates `_core.RPCError`; no internal
      `try/except`.
- [x] `TestContractReads` covers happy paths and failure paths for each
      fetcher, asserting on the right RPC method and block tag.
- [x] From the skill directory: `python3 -m unittest
      test_build_erc20.TestContractReads -v` is green.
- [x] `build_send_eth.py` and `test_build_send_eth.py` are unchanged.

**Testing Notes:**
- Mock the injected `rpc` callable with `unittest.mock.Mock`; tests do not
  hit the network.
- `fetch_symbol`'s "any exception" catch is broad on purpose (ADR-006);
  prefer `except Exception:` over a narrow list so a future decode bug
  doesn't break the build.

---

### Issue 1.5: Layer 2 `gas_estimator` (no-fallback) + `TestGasEstimator`

- **Points:** 2
- **Type:** feature
- **Priority:** P0
- **Blocked by:** 1.1 (test shell), 1.4 (mirrors v1 estimator + reuses the
  `contract_reads`-style rpc-injection pattern; sequenced after to keep
  Layer 2 work serialized)
- **Blocks:** 1.7
- **Scope:** 1-2 days

**Description:**
Implement the `gas_estimator` section: `_apply_buffer_cap` (+20% buffer,
300_000 cap, integer math) and `estimate_gas` (no `try/except`,
`_core.RPCError` propagates by design — architecture ADR-007). Ship with
`TestGasEstimator` covering the buffer/cap table and the no-fallback
regression assertion.

**Implementation Notes:**
- Files affected: `build_erc20.py` (Layer 2 `gas_estimator` section),
  `test_build_erc20.py` (add `TestGasEstimator`).
- Constants:
  - `GAS_BUFFER_NUM = 12`
  - `GAS_BUFFER_DEN = 10`
  - `GAS_CAP        = 300_000`
- `_apply_buffer_cap(est) -> int`:
  - `return min((est * GAS_BUFFER_NUM) // GAS_BUFFER_DEN, GAS_CAP)`
- `estimate_gas(rpc, url, sender, token, data) -> int`:
  - Build call object `{"from": sender, "to": token, "data": data,
    "value": "0x0"}`.
  - Invoke `rpc(url, "eth_estimateGas", [call_obj, "latest"])`.
  - Parse the hex result via `_core.parse_hex_int`.
  - Return `_apply_buffer_cap(est)`.
  - **NO try/except.** Add an in-code multi-line comment above the function
    body explaining: "A silent fallback to a hardcoded gas number would let
    a tx that will definitely revert on-chain get signed and burn its full
    gas budget. See architecture ADR-007 and research §03. RPCError
    propagation is load-bearing — do NOT add a try/except around the rpc()
    call." Reference ADR-007 and research §03 explicitly so a future
    maintainer adding a fallback has to read the rationale.
- Test class `TestGasEstimator`:
  - `_apply_buffer_cap` table: `0 → 0`, `1 → 1` (since `1 * 12 // 10 == 1`),
    `100_000 → 120_000`, `250_000 → 300_000` (capped), `1_000_000 →
    300_000` (capped).
  - `estimate_gas` with mocked `rpc` returning `"0xfe1f"` (65055) →
    `(65055 * 12) // 10 == 78066`.
  - `estimate_gas` with mocked `rpc` returning `"0x3d090"` (250_000) →
    `300_000` (capped).
  - `estimate_gas` with mocked `rpc` raising `_core.RPCError("execution
    reverted: ERC20: transfer amount exceeds balance")` →
    `assertRaises(_core.RPCError)` (no internal catch).
  - Assert the mock was called with method `"eth_estimateGas"` and a
    call object containing `from`, `to`, `data`, `value: "0x0"` keys.

**Acceptance Criteria:**
- [x] `GAS_BUFFER_NUM`, `GAS_BUFFER_DEN`, `GAS_CAP` are defined with the
      PRD-specified values.
- [x] `_apply_buffer_cap` is implemented with integer math.
- [x] `estimate_gas` is implemented; the in-code comment above it explains
      the no-fallback policy and references ADR-007 + research §03.
- [x] `estimate_gas` contains no `try`, no `except`, no `finally` —
      grep-check: `grep -E "try:|except|finally:"
      build_erc20.py` shows zero matches inside the function body.
- [x] `TestGasEstimator` covers the buffer/cap table, the buffered happy
      path, the capped happy path, and the `RPCError`-propagation
      regression.
- [x] From the skill directory: `python3 -m unittest
      test_build_erc20.TestGasEstimator -v` is green.
- [x] `build_send_eth.py` and `test_build_send_eth.py` are unchanged.

**Testing Notes:**
- The structural "no try/except" rule is the invariant: a regression that
  silently swallows RPCError into a fallback gas number would fail the
  `assertRaises` in `TestGasEstimator` (no fallback was returned).
- `_core.parse_hex_int` already raises `ValueError` on malformed hex;
  surface that to the CLI rather than catching here (the CLI converts both
  `ValueError` and `RPCError` to `error: ...` + exit 1).

---

### Issue 1.6: Layer 2 `summary` (warning dispatcher + stderr renderer) + `TestSummary`

- **Points:** 2
- **Type:** feature
- **Priority:** P0
- **Blocked by:** 1.1 (test shell), 1.3 (`base_units_to_human` consumed by `render_summary`)
- **Blocks:** 1.7
- **Scope:** 1-2 days

**Description:**
Implement the `summary` section: the pure `render_summary` (returns the
human-readable PRD §16 block as a string), the I/O `print_summary` (writes
to stderr), the per-warning emitters
(`warn_approve_max`, `warn_low_allowance`,
`warn_allowance_check_skipped`, `warn_symbol_unavailable`), and the
`emit_warning(kind, payload)` dispatcher consuming the `(kind,
payload_dict)` tuples produced by `tx_assembly`. Ship with `TestSummary`.

**Implementation Notes:**
- Files affected: `build_erc20.py` (Layer 2 `summary` section),
  `test_build_erc20.py` (add `TestSummary`).
- `render_summary(ctx) -> str`:
  - Pure function. Returns a multi-line ASCII block with the PRD §16
    fields: operation, network + chainId, token address, token symbol (or
    `(unavailable)` when ctx['symbol'] is `None`), decimals, human amount,
    resolved base-unit amount (or `MAX UINT256` when `ctx['is_max_uint']`),
    role-specific addresses (transfer: from/sender → to; approve: holder
    → spender; transfer-from: source from / recipient to /
    signer-spender sender), nonce, gas, maxFeePerGas, maxPriorityFeePerGas.
  - Uses `base_units_to_human` from `amount_codec` to render the
    base-unit amount for human review.
  - Labels are stable strings so tests can grep them (`"token"`,
    `"decimals"`, `"amount (base units)"`, `"spender"`, `"source (from)"`,
    `"signer / spender"`, `"nonce"`, `"gas"`, etc.).
- `print_summary(ctx) -> None`:
  - `sys.stderr.write(render_summary(ctx))` plus a trailing newline if
    missing.
- Warning emitters (each writes to stderr):
  - `warn_approve_max(symbol, token, spender)` — multi-line `WARNING:`
    block per PRD §7: `"WARNING: --approve-max grants UNLIMITED transfer
    authority on <SYMBOL> (<token-addr>) to spender <spender-addr>.\n
    Revoke later with approve(spender, 0) if no longer needed."`. When
    `symbol is None`, render `<SYMBOL>` as `<unknown>`.
  - `warn_low_allowance(holder, spender, current, requested, decimals)` —
    `WARNING: current allowance is <N> (<human>); requested transfer is
    <M> (<human>). This transaction will revert unless allowance is
    increased before broadcast.` Use `base_units_to_human` for the human
    figures.
  - `warn_allowance_check_skipped(reason)` — `WARNING: allowance soft-check
    skipped: <reason>. Build continues.`
  - `warn_symbol_unavailable()` — optional info note `WARNING: token
    symbol() unavailable; summary may be less informative.` Optional;
    decide at review whether to emit at all (architecture flags it as
    optional / info-only).
- `emit_warning(kind, payload) -> None`:
  - Dispatcher mapping `kind ∈ {"approve_max", "low_allowance",
    "allowance_check_skipped", "symbol_unavailable"}` to the matching
    `warn_*` function. Pass through the payload dict as kwargs.
  - On an unknown `kind`, raise `ValueError` (defensive — a typo in
    `tx_assembly` should surface in tests).
- Test class `TestSummary`:
  - `render_summary` for a synthetic `ctx` with all required keys returns a
    string containing every expected label (test by `self.assertIn(label,
    text)` for each).
  - `render_summary` with `ctx['symbol'] = None` includes `(unavailable)`
    in the output.
  - `render_summary` with `ctx['is_max_uint'] = True` includes `MAX
    UINT256`.
  - `warn_approve_max`: patch `sys.stderr` (`unittest.mock.patch('sys.stderr',
    new_callable=io.StringIO)`); call the function; assert the captured
    output contains the multi-line warning text and the `WARNING:` prefix.
  - `warn_low_allowance` writes a `WARNING:` line to stderr containing
    the holder, spender, current, and requested values.
  - `warn_allowance_check_skipped("transport error")` writes a
    `WARNING:` line containing `"transport error"`.
  - `emit_warning("approve_max", {...})` delegates to `warn_approve_max`.
  - `emit_warning("unknown_kind", {})` raises `ValueError`.

**Acceptance Criteria:**
- [x] `render_summary` returns a string with every PRD §16 field; pure (no
      stderr write).
- [x] `print_summary` writes the rendered text to stderr (uses
      `sys.stderr.write`, not `print` to stdout).
- [x] `warn_approve_max`, `warn_low_allowance`,
      `warn_allowance_check_skipped` write `WARNING:`-prefixed text to
      stderr.
- [x] `emit_warning(kind, payload)` dispatches by `kind`; unknown kinds
      raise `ValueError`.
- [x] `TestSummary` covers each `render_summary` label and each warning
      emitter via `sys.stderr` capture.
- [x] From the skill directory: `python3 -m unittest
      test_build_erc20.TestSummary -v` is green.
- [x] `build_send_eth.py` and `test_build_send_eth.py` are unchanged.

**Testing Notes:**
- Capture stderr with `unittest.mock.patch('sys.stderr', new_callable=
  io.StringIO)` — matches v1 testing patterns.
- The summary text is the operator-facing safety net; keep the labels
  stable so `TestSummary` doubles as a documentation regression.
- Bare summary lines have no prefix. Only warnings use `WARNING:` and only
  fatal errors (in `cli_dispatch`) use lowercase `error:` (architecture
  ADR-009).

---

### Issue 1.7: Layer 3 `tx_assembly` — three `do_*` composers + `TestTxAssembly`

- **Points:** 3
- **Type:** feature
- **Priority:** P0
- **Blocked by:** 1.3, 1.4, 1.5, 1.6
- **Scope:** 2 days

> Internal sub-structure (≈ 2 days): day 1 = `_build_eip1559_envelope`
> helper + `do_transfer` + `do_approve` (both bounded and `approve_max`
> paths) + happy-path `TestTxAssembly` cases; day 2 = `do_transfer_from`
> with the allowance soft-check (the only `try/except RPCError` outside
> `cli_dispatch`) + the no-fallback regression tests (`fetch_decimals`
> raises, `estimate_gas` raises).

**Description:**
Implement the `tx_assembly` section: `_build_eip1559_envelope` (internal
helper assembling the v1-shape TxRequest dict) plus `do_transfer`,
`do_approve` (with `approve_max=False` kwarg), and `do_transfer_from`
following the architecture eight-step skeleton. Each `do_*` returns
`(tx_dict, summary_ctx, warnings_list)`. The **only** `try/except RPCError`
outside `cli_dispatch.main()` is inside `do_transfer_from` around
`fetch_allowance` for the soft-check (architecture ADR-007). Ship with
`TestTxAssembly` covering happy paths, the `--approve-max` path, both
`transfer-from` soft-check branches, and both no-fallback regressions
(`fetch_decimals` raises, `estimate_gas` raises).

Note on the explicit `1.3` dependency: `tx_assembly` directly calls
`amount_codec.human_to_base_units` (in every `do_*`) and consumes
`MAX_UINT256` (in the `approve_max=True` branch of `do_approve`). Both
symbols are introduced in Issue 1.3, so 1.3 is a direct blocker even
though `amount_codec` is logically "Layer 1" and `tx_assembly` is "Layer
3" — the layer DAG is upper-bound on dependencies, not a guarantee that
every Layer 2 issue blocks every Layer 3 issue.

**Implementation Notes:**
- Files affected: `build_erc20.py` (Layer 3 `tx_assembly` section),
  `test_build_erc20.py` (add `TestTxAssembly`).
- `_build_eip1559_envelope(chain_id, nonce, to, data, gas, base_fee, tip)`
  — returns the `tx_dict` shape with all numeric fields as decimal strings:
  ```python
  {
      "type": "eip1559",
      "chainId": str(chain_id),
      "nonce": str(nonce),
      "to": to,
      "value": "0",
      "data": data,
      "gas": str(gas),
      "maxFeePerGas": str(_core.compute_max_fee(base_fee, tip)),
      "maxPriorityFeePerGas": str(tip),
  }
  ```
- All three `do_*` functions take `rpc=_core.rpc_call` as a kwarg default
  (architecture ADR-003).
- `do_transfer(network, token, to, amount, sender, *, rpc=_core.rpc_call)`:
  1. `chain_id, url = _core.network_config(network)`.
  2. `decimals = contract_reads.fetch_decimals(rpc, url, token)`. FATAL.
  3. `symbol = contract_reads.fetch_symbol(rpc, url, token)`. Best-effort.
  4. `amount_base = amount_codec.human_to_base_units(amount, decimals)`.
  5. `calldata = abi_codec.encode_transfer(to, amount_base)`.
  6. `gas = gas_estimator.estimate_gas(rpc, url, sender, token, calldata)`.
     FATAL; no try/except.
  7. `nonce = _core.fetch_nonce(rpc, url, sender)`,
     `base_fee = _core.fetch_base_fee(rpc, url)`,
     `tip = _core.fetch_tip(rpc, url)`.
  8. Assemble `tx_dict = _build_eip1559_envelope(chain_id, nonce, token,
     calldata, gas, base_fee, tip)`.
  9. Build `summary_ctx` with stable keys (`operation="transfer"`,
     `network`, `chain_id`, `token`, `symbol`, `decimals`, `human_amount`,
     `base_amount=amount_base`, `is_max_uint=False`, `from_=sender`, `to`,
     `nonce`, `gas`, `max_fee=_core.compute_max_fee(base_fee, tip)`,
     `max_priority_fee=tip`).
  10. Return `(tx_dict, summary_ctx, warnings=[])`.
- `do_approve(network, token, spender, amount, sender, *,
  approve_max=False, rpc=_core.rpc_call)`:
  - Same outline as `do_transfer` but:
    - If `approve_max=True`: skip `human_to_base_units`; set `amount_base =
      MAX_UINT256`; queue `("approve_max", {"symbol": symbol, "token":
      token, "spender": spender})` in `warnings`.
    - `calldata = abi_codec.encode_approve(spender, amount_base)`.
    - `summary_ctx['operation'] = "approve"`; include `is_max_uint =
      approve_max`; role-specific addresses are `holder=sender` and
      `spender`.
- `do_transfer_from(network, token, from_, to, amount, sender, *,
  rpc=_core.rpc_call)`:
  - Same outline as `do_transfer` but:
    - `calldata = abi_codec.encode_transfer_from(from_, to, amount_base)`.
    - **Allowance soft-check** (the only `try/except RPCError` outside
      `cli_dispatch`):
      ```python
      try:
          current = contract_reads.fetch_allowance(rpc, url, token, from_, sender)
      except _core.RPCError as e:
          warnings.append(("allowance_check_skipped", {"reason": str(e)}))
      else:
          if current < amount_base:
              warnings.append(("low_allowance", {
                  "holder": from_, "spender": sender,
                  "current": current, "requested": amount_base,
                  "decimals": decimals,
              }))
      ```
    - Then `estimate_gas` (FATAL — no surrounding try/except).
    - `summary_ctx['operation'] = "transfer-from"`; include source `from_`,
      recipient `to`, signer/spender `sender`.
- Test class `TestTxAssembly`:
  - `make_fake_rpc(responses)` helper: duplicated from
    `test_build_send_eth.py` with a one-line comment per architecture
    Assumption 14: `# Duplicated from test_build_send_eth.py because the v1
    test file is read-only for Phase 1 (architecture A14).` ~12 lines.
  - Happy-path `do_transfer`: assert `tx_dict` shape (matches v1 keys +
    string-typed numerics), `summary_ctx` keys, `warnings_list == []`,
    `tx_dict["to"]` is the token address, `tx_dict["value"] == "0"`,
    `tx_dict["data"]` starts with `SEL_TRANSFER`.
  - Happy-path `do_approve` (bounded amount): analogous.
  - `do_approve(approve_max=True)`: assert `tx_dict["data"]` ends with the
    all-Fs 64-hex-char word (the MAX_UINT256 encoding); `warnings_list`
    contains `("approve_max", {...})`.
  - Happy-path `do_transfer_from`: with `fetch_allowance` returning a
    value ≥ requested, `warnings_list == []`.
  - `do_transfer_from` with low allowance: mock `fetch_allowance` to
    return a small value; assert `warnings_list` contains
    `("low_allowance", {...})` with the expected payload; assert
    `tx_dict` was still built (JSON still emitted downstream).
  - `do_transfer_from` with `fetch_allowance` raising `_core.RPCError`:
    assert `warnings_list` contains `("allowance_check_skipped",
    {"reason": ...})`; assert `tx_dict` was still built.
  - **No-fallback regressions:**
    - `do_transfer` (or any `do_*`) when `fetch_decimals` raises
      `_core.RPCError`: `assertRaises(_core.RPCError)`. No `tx_dict`.
    - `do_transfer` when `estimate_gas` raises `_core.RPCError`:
      `assertRaises(_core.RPCError)`. No `tx_dict`. (This is the
      ADR-007 regression.)

**Acceptance Criteria:**
- [x] `_build_eip1559_envelope` produces a dict with the v1 TxRequest shape
      and all numeric fields as decimal strings.
- [x] `do_transfer`, `do_approve(approve_max=False/True)`,
      `do_transfer_from` are implemented per the architecture eight-step
      skeleton and return `(tx_dict, summary_ctx, warnings_list)`.
- [x] The only `try/except RPCError` outside `cli_dispatch` is the
      `transfer-from` allowance soft-check (grep-check: `grep -n "except _core.RPCError"
      build_erc20.py` should show ~1 match inside `tx_assembly`).
- [x] `TestTxAssembly` covers each happy path, the `--approve-max` path,
      the `transfer-from` low-allowance soft-check, the `transfer-from`
      allowance RPC-error soft-check, and the two `RPCError`-propagation
      regressions (`fetch_decimals` raises, `estimate_gas` raises).
- [x] `make_fake_rpc` helper is duplicated into `test_build_erc20.py`
      with the architecture Assumption 14 comment.
- [x] From the skill directory: `python3 -m unittest
      test_build_erc20.TestTxAssembly -v` is green.
- [x] `build_send_eth.py` and `test_build_send_eth.py` are unchanged.

**Testing Notes:**
- Use `make_fake_rpc({"eth_call": [...], "eth_estimateGas": "0xfe1f",
  "eth_getTransactionCount": "0x05", "eth_getBlockByNumber": {...},
  "eth_maxPriorityFeePerGas": "0x3b9aca00"})` to script the multi-call
  responses for each `do_*` happy-path test.
- For the `eth_call` reads, the helper must distinguish `decimals`,
  `symbol`, `allowance` by inspecting the call's `data` selector. Either
  use a list-of-responses queue keyed by method (simplest) or a callable
  that returns by selector.

---

### Issue 1.8: Layer 4 `cli_dispatch` — argparse + dispatch + `TestCliDispatch`

- **Points:** 3
- **Type:** feature
- **Priority:** P0
- **Blocked by:** 1.1 (test shell), 1.7 (`do_*` composers consumed)
- **Blocks:** 1.9, 1.10a
- **Scope:** 2 days

**Description:**
Implement the `cli_dispatch` section: `_build_parser` (three subparsers
matching PRD §FR P0 §2–4), `_validate_addresses` (calls
`_core.validate_hex_address` for every address arg), and `main(argv=None)`
with the architecture-mandated structure (try/except is the **only**
exception handler in the codebase). Ship with `TestCliDispatch` covering
argparse smoke, address validation, the happy paths, the `--approve-max`
path, the low-allowance soft-check path, and the no-fallback regression at
the CLI layer.

**Implementation Notes:**
- Files affected: `build_erc20.py` (Layer 4 `cli_dispatch` section;
  replaces the stub `main` from Issue 1.1), `test_build_erc20.py` (add
  `TestCliDispatch`).
- `_build_parser() -> argparse.ArgumentParser`:
  - Top-level parser description: "Build a ready-to-sign EIP-1559 ERC-20
    TxRequest JSON for eth-signer-mcp."
  - Three subparsers via `parser.add_subparsers(dest="op", required=True)`:
    - `transfer`: `--network` (`choices=sorted(_core.NETWORKS)`, required),
      `--token` (required), `--to` (required), `--amount` (required),
      `--sender` (required).
    - `approve`: `--network`, `--token`, `--spender`, `--sender` (all
      required); a `mutually_exclusive_group(required=True)` containing
      `--amount` and `--approve-max` (the latter as `action="store_true"`).
    - `transfer-from`: `--network`, `--token`, `--from` (Python keyword:
      use `dest="from_"`), `--to`, `--amount`, `--sender` (all required).
  - Subcommand names use hyphens (`transfer-from`); function names use
    underscores (`do_transfer_from`).
- `_validate_addresses(args) -> None`:
  - For each address attribute present on `args` (`token`, `to`, `spender`,
    `from_`, `sender`), call `_core.validate_hex_address(value)`. The
    helper raises `ValueError` on bad input; caught by `main`.
  - Architecture ADR-010: validation happens once, here; `do_*` accepts
    already-validated hex.
- `main(argv=None) -> int`:
  - Structure (the **only** `try/except (ValueError, _core.RPCError)` in
    the entire codebase — architecture ADR-007):
    ```python
    parser = _build_parser()
    args = parser.parse_args(argv)
    try:
        _validate_addresses(args)
        if args.op == "transfer":
            tx, ctx, warns = tx_assembly.do_transfer(args.network, args.token,
                                                    args.to, args.amount, args.sender)
        elif args.op == "approve":
            tx, ctx, warns = tx_assembly.do_approve(args.network, args.token,
                                                   args.spender, args.amount,
                                                   args.sender,
                                                   approve_max=args.approve_max)
        elif args.op == "transfer-from":
            tx, ctx, warns = tx_assembly.do_transfer_from(args.network, args.token,
                                                         args.from_, args.to,
                                                         args.amount, args.sender)
        for w_kind, w_payload in warns:
            summary.emit_warning(w_kind, w_payload)
        summary.print_summary(ctx)
        print(json.dumps(tx, indent=2))
        return 0
    except (ValueError, _core.RPCError) as e:
        print("error: %s" % e, file=sys.stderr)
        return 1
    ```
- Replace the stub `main` introduced in Issue 1.1.
- Test class `TestCliDispatch`:
  - **Argparse smoke:** `main(["--help"])` exits 0 (catch SystemExit);
    capture stdout; assert it lists `transfer`, `approve`, `transfer-from`.
    Each subcommand's `--help` is present and lists the required flags.
  - **Mutex enforcement:** `main(["approve", "--network", "hoodi", "--token",
    "0x...", "--spender", "0x...", "--sender", "0x...", "--amount", "1",
    "--approve-max"])` exits non-zero via argparse (catch SystemExit).
    Same for neither `--amount` nor `--approve-max` set.
  - **Address validation failure:** `main(["transfer", "--network", "hoodi",
    "--token", "not-an-address", ...])` returns 1; stderr contains `error:`;
    stdout is empty.
  - **Happy path:** mock the `tx_assembly` dispatch (or use `make_fake_rpc`
    end-to-end) to return a fixed `(tx, ctx, [])`. Capture stdout +
    stderr. Assert: returns 0; stdout is exactly the JSON of `tx` (parses
    with `json.loads`); stderr contains the summary labels.
  - **No-fallback regression at the CLI layer:** mock `tx_assembly.do_transfer`
    (or have `make_fake_rpc` make `eth_estimateGas` raise) so that
    `_core.RPCError("execution reverted")` propagates. Assert: `main`
    returns 1; stderr contains `error:` and the underlying message;
    **stdout is empty** (this is the ADR-007 regression at the CLI).
  - **`--approve-max` happy path:** mock so `do_approve(approve_max=True)`
    returns `(tx, ctx, [("approve_max", {...})])`. Assert: returns 0;
    stderr contains the `WARNING:` text and the summary; stdout is the
    JSON.
  - **`transfer-from` low-allowance happy path:** mock so
    `do_transfer_from` returns `(tx, ctx, [("low_allowance", {...})])`.
    Assert: returns 0; stderr contains the `WARNING:` text; stdout is the
    JSON.

**Acceptance Criteria:**
- [x] `_build_parser` creates three subparsers (`transfer`, `approve`,
      `transfer-from`) with the PRD-defined required flags.
- [x] `approve` enforces `--amount` XOR `--approve-max` via
      `add_mutually_exclusive_group(required=True)`.
- [x] `--network` `choices` come from `sorted(_core.NETWORKS)` (so Phase 2
      adds propagate automatically — architecture Assumption 15).
- [x] `_validate_addresses` invokes `_core.validate_hex_address` on every
      address arg present on `args`.
- [x] `main` is the **only** `try/except (ValueError, _core.RPCError)` in
      `build_erc20.py` (grep-check: that exact except clause appears at
      most once in the file).
- [x] `TestCliDispatch` covers: top-level `--help`, each subcommand
      `--help`, `approve` mutex (both / neither), address validation
      failure, happy path for each subcommand, the `--approve-max` warning
      path, the `transfer-from` low-allowance soft-check path, and the
      CLI-layer no-fallback regression (`RPCError` from `estimate_gas` →
      exit 1 + empty stdout).
- [x] From the skill directory: `python3 -m unittest
      test_build_erc20.TestCliDispatch -v` is green.
- [x] `build_send_eth.py` and `test_build_send_eth.py` are unchanged.

**Testing Notes:**
- Argparse exits via `SystemExit` on `--help` and on mutex violations.
  Wrap with `self.assertRaises(SystemExit)` and inspect the exit code via
  the raised exception.
- Capture stdout/stderr with `unittest.mock.patch('sys.stdout', new_callable
  =io.StringIO)` (and analogous for stderr). Avoid `capsys` (that's
  `pytest`).
- The empty-stdout assertion on the no-fallback regression is the
  signature regression check for ADR-007 at the CLI layer.

---

### Issue 1.9: Full test-suite green — v1 regression + new

- **Points:** 1
- **Type:** chore
- **Priority:** P0
- **Blocked by:** 1.2, 1.3, 1.4, 1.5, 1.6, 1.7, 1.8
- **Blocks:** 1.10b, 1.11
- **Scope:** 0.5 day

**Description:**
Run both unittest suites from the skill directory and confirm both are
green with **zero** edits to v1 files. This is the P0 freeze check (PRD
success metric §4). If either fails, fix in the new code (never by editing
`build_send_eth.py` or `test_build_send_eth.py`; the freeze is absolute for
Phase 1).

**Implementation Notes:**
- No source edits in this issue; verification only.
- Commands (from `.claude/skills/eth-tx-builder/`):
  ```
  python3 -m unittest test_build_send_eth -v
  python3 -m unittest test_build_erc20 -v
  ```
- Confirm `git diff -- build_send_eth.py test_build_send_eth.py` is empty.
- Also confirm:
  - `python3 -c "import build_erc20"` succeeds.
  - `python3 build_erc20.py --help` lists all three subcommands.
  - `python3 build_erc20.py transfer --help` shows the required flags.
- Stdlib-only smoke check: `python3 -c "import build_erc20; print('ok')"`
  on a fresh Python 3.8+ install with no pip packages installed (a
  venv-from-scratch is sufficient).

**Acceptance Criteria:**
- [x] `python3 -m unittest test_build_send_eth -v` reports OK with the
      same number of tests as pre-Phase-1.
- [x] `python3 -m unittest test_build_erc20 -v` reports OK with all seven
      `TestCase` classes contributing tests.
- [x] `git diff -- build_send_eth.py test_build_send_eth.py` is empty.
- [x] `python3 build_erc20.py --help` lists `transfer`, `approve`,
      `transfer-from` subcommands.
- [x] Stdlib-only smoke: `python3 -c "import build_erc20"` succeeds from a
      Python 3.8+ env with no third-party packages.

**Testing Notes:**
- If `test_build_send_eth -v` fails after Phase 1 changes, the freeze is
  broken — the failure is a bug in `build_erc20.py` (e.g. an import-time
  side effect), not a license to edit v1.
- Record the v1 test count and `build_send_eth.py` SHA in the PR description
  so reviewers can confirm the byte-for-byte freeze.

---

### Issue 1.10a: `SKILL.md` + `README.md` docs updates (prose only)

- **Points:** 1
- **Type:** docs
- **Priority:** P0
- **Blocked by:** 1.8 (CLI shape stable — the docs describe the final CLI
  surface)
- **Blocks:** 1.10b, 1.11
- **Scope:** 1 day

**Description:**
Update `SKILL.md` (description, split inputs, routing step, refreshed "Out
of scope") and `README.md` (file list, manual-e2e section skeleton with
placeholders, test invocation snippet). **No live network is exercised in
this issue** — only prose edits that describe the final CLI surface
landed in Issue 1.8. The actual hoodi e2e runs (live broadcasts +
transcripts) land in Issue 1.10b, which depends on this issue having
established the e2e-section skeleton in the README.

**Implementation Notes:**
- Files affected: `.claude/skills/eth-tx-builder/SKILL.md`,
  `.claude/skills/eth-tx-builder/README.md`. **Prose only** — no code edits
  to either file.
- `SKILL.md` edits:
  - Broaden description: `"...build an Ethereum transaction (native ETH
    transfer OR ERC-20 transfer / approve / transferFrom)..."`.
  - Split "Inputs" into two subsections:
    - "Inputs — native ETH send" (existing content unchanged).
    - "Inputs — ERC-20 transfer / approve / transferFrom" (new): token
      address; `--to` (transfer / transfer-from), `--spender` (approve),
      `--from` (transfer-from); amount (human-readable) or `--approve-max`.
  - Top of "Procedure": add a router step.
    1. Identify intent: native ETH transfer → `build_send_eth.py`
       (existing procedure unchanged).
    2. Identify intent: ERC-20 → `build_erc20.py <subcommand> ...`.
  - Add notes on the safety surface: `--approve-max` requires a deliberate
    flag and prints a loud `WARNING:`; `transfer-from` allowance is
    soft-checked (warn-don't-block); `eth_estimateGas` has no fallback —
    failures surface as `error:` + exit 1.
  - Mention the stdout=JSON / stderr=summary discipline so operators can
    pipe stdout into the signer or `jq`.
  - "Out of scope (v1)": remove ERC-20 (now in scope). Add explicit
    non-goals: permit (EIP-2612), ERC-721 / ERC-1155, DEX routers /
    swaps, multi-token batch, fee-on-transfer / rebasing handling,
    gasless meta-tx, signing, broadcasting.
- `README.md` edits:
  - File list adds: `build_erc20.py` (helper) and `test_build_erc20.py`
    (tests).
  - Add a "Manual end-to-end (hoodi)" **section skeleton** with three
    subsections (`transfer`, `approve --amount`, `transfer-from`) and, in
    each, the **placeholders** for the captured artifacts that Issue
    1.10b will fill in:
    - The full CLI invocation (template with `<TOKEN>` / `<TO>` /
      `<FROM>` / `<SPENDER>` / `<SENDER>` placeholders).
    - `<insert stderr summary block here — filled in by Issue 1.10b>`.
    - `<insert stdout JSON here — filled in by Issue 1.10b>`.
    - `<insert paste-to-signer step + signer response — filled in by
      Issue 1.10b>`.
    - `<insert broadcast step + tx hash — filled in by Issue 1.10b>`.
    - `<insert on-chain confirmation (block number / receipt status) —
      filled in by Issue 1.10b>`.
    - Document the explicit `approve`-before-`transfer-from` ordering
      requirement in the section preamble so the e2e runner cannot miss
      it.
  - Update the test invocation snippet to run both files:
    ```
    cd .claude/skills/eth-tx-builder
    python3 -m unittest test_build_send_eth -v
    python3 -m unittest test_build_erc20 -v
    ```

**Acceptance Criteria:**
- [x] `SKILL.md` description is broadened to include ERC-20.
- [x] `SKILL.md` "Inputs" is split into "native ETH send" and "ERC-20" subsections.
- [x] `SKILL.md` "Procedure" includes a top-level routing step (native ETH
      vs ERC-20 → which helper).
- [x] `SKILL.md` mentions the `--approve-max` warning posture, the
      `transfer-from` allowance soft-check, the no-fallback estimate
      policy, and the stdout=JSON / stderr=summary split.
- [x] `SKILL.md` "Out of scope (v1)" removes ERC-20 and adds the explicit
      new non-goals (permit, NFTs, DEX routers, multi-token batch,
      fee-on-transfer, gasless, signing, broadcasting).
- [x] `README.md` file list includes `build_erc20.py` and
      `test_build_erc20.py`.
- [x] `README.md` has a "Manual end-to-end (hoodi)" section skeleton with
      three subsections (`transfer`, `approve --amount`, `transfer-from`)
      containing the explicit placeholders Issue 1.10b will fill in.
- [x] `README.md` "Manual end-to-end (hoodi)" preamble explicitly states
      that the `approve` run MUST land on-chain before the
      `transfer-from` run is attempted.
- [x] `README.md` test invocation snippet runs both `test_build_send_eth`
      and `test_build_erc20`.
- [x] No live network calls made in this issue (verified by absence of
      filled-in tx hashes in the README — placeholders only).
- [x] `build_send_eth.py` and `test_build_send_eth.py` are unchanged.

**Testing Notes:**
- This issue intentionally stops at "skeleton + placeholders" so that
  Issue 1.10b can run independently against a live hoodi RPC + funded
  wallet without also gating prose review on network availability.
- A doc-only review here is fast and reversible; surfacing prose nits in
  1.10a means 1.10b can focus on the e2e execution itself.

---

### Issue 1.10b: Hoodi manual end-to-end for all three ops

- **Points:** 2
- **Type:** verification
- **Priority:** P0
- **Blocked by:** 1.9 (suites green), 1.10a (e2e section skeleton in
  README ready to fill in)
- **Blocks:** 1.11
- **Scope:** 1-2 days

**Description:**
Execute the hoodi manual end-to-end against a real, standard-surface
ERC-20 for all three ops, capturing transcripts into the README section
skeleton landed by Issue 1.10a. This is the PRD success metric §1 + §2
confirmation; Phase 1 does not exit without it. The e2e is **serialized
on-chain by design**: `approve` MUST mine before `transfer-from` runs,
because `transfer-from` depends on the prior approval.

**Implementation Notes:**
- Files affected: `.claude/skills/eth-tx-builder/README.md` (fill in the
  three e2e transcript subsections that 1.10a left as placeholders).
  **No source edits.**
- **Pre-flight check (MUST complete BEFORE any e2e run):**
  1. **`eth-signer-mcp` reachable.** Smoke `get_address` (or equivalent
     read tool) and confirm a non-error response. Record the wallet
     address that will sign.
  2. **Test wallet funded with hoodi ETH.** Read the wallet balance via
     `eth_getBalance` against `https://ethereum-hoodi-rpc.publicnode.com`
     and confirm it is large enough to cover three EIP-1559 txs at the
     gas cap (300_000) × the maxFeePerGas seen on hoodi. If
     insufficient, top up from a hoodi faucet and re-check before
     proceeding.
  3. **Standard-surface ERC-20 selected on hoodi.** Pick a real, standard
     ERC-20 deployed on hoodi (PRD assumption set requires standard
     `decimals` / `symbol` / `transfer` / `approve` / `transferFrom`).
     Confirm via `eth_call` that `decimals()` returns a sane value (≤ 18)
     and `symbol()` decodes. Confirm the source wallet has a non-zero
     token balance large enough for both the `transfer` and
     `transfer-from` (the `transfer-from` source is the same wallet).
- **E2E ordering (MANDATORY, serialized):**
  1. **`transfer` run** — confirm (a) helper exits 0, (b) stderr summary
     names symbol, decimals, base-unit amount, and addresses, (c) stdout
     JSON pastes into `eth-signer-mcp` `sign_transaction` and signs,
     (d) signed raw broadcasts on hoodi and executes successfully
     (receipt status = 1). Record tx hash + block number.
  2. **`approve --amount` run** — bounded amount (NOT `--approve-max`
     for the e2e — avoid leaving an unbounded grant on the test wallet).
     Same (a)–(d) checks. Record tx hash + block number. **Wait until
     the approve tx is MINED on hoodi (poll `eth_getTransactionReceipt`
     until non-null status = 1) BEFORE running the next step.**
  3. **`transfer-from` run** — depends on step 2 having mined; the
     spender (signer wallet) is now authorized to move `amount` from the
     source wallet. Same (a)–(d) checks. Record tx hash + block number.
- **Transcript capture** — for each of the three runs, fill the
  README's 1.10a-prepared placeholder block with:
  - The full CLI invocation (with real addresses, NOT placeholders).
  - The captured stderr summary block.
  - The stdout JSON.
  - The paste-to-signer step + signer response.
  - The broadcast step (`eth-rpc` or equivalent) + tx hash.
  - The on-chain confirmation (block number + receipt status = 1).
- **Failure handling** — if any of (a)–(d) fail for any op, surface as a
  Phase 1 blocker and do NOT proceed to Issue 1.11; fix the code and
  re-run the e2e. Critically: if the `transfer-from` step fails because
  the `approve` step's tx is not yet mined, that is **operator error**
  (re-run after polling for mined status), NOT a code defect.

**Acceptance Criteria:**
- [x] **Pre-flight (a):** `eth-signer-mcp` reachable; signer wallet
      address recorded.
- [x] **Pre-flight (b):** test wallet hoodi-ETH balance verified
      sufficient for three EIP-1559 broadcasts.
- [ ] **Pre-flight (c):** standard-surface ERC-20 chosen on hoodi;
      `decimals()` and `symbol()` decode; source wallet has non-zero token
      balance large enough for `transfer` + `transfer-from`.
- [ ] **Ordering:** `approve` tx mined on hoodi (`eth_getTransactionReceipt`
      returns non-null with status = 1) BEFORE `transfer-from` is broadcast.
- [ ] **`transfer`:** stdout JSON accepted by `eth-signer-mcp`; broadcast
      tx on hoodi with receipt status = 1; tx hash + block number recorded
      in README.
- [ ] **`approve --amount`:** stdout JSON accepted by signer; broadcast
      with receipt status = 1; tx hash + block number recorded in README;
      tx confirmed mined before next step.
- [ ] **`transfer-from`:** stdout JSON accepted by signer; broadcast with
      receipt status = 1; tx hash + block number recorded in README.
- [ ] All three transcript blocks in `README.md` "Manual end-to-end
      (hoodi)" section have been filled in (no remaining `<insert ...
      here>` placeholders).
- [ ] `decimals()` RPC read works against the chosen real ERC-20 on hoodi
      (PRD success metric §2; captured in the `transfer` transcript at
      minimum).
- [x] `build_send_eth.py` and `test_build_send_eth.py` are unchanged.

**Testing Notes:**
- Pick the hoodi ERC-20 in advance (Phase 1 Risk R1). A standard-surface
  token avoids decode / encode edge cases that would muddy the signal.
- The e2e may need multiple rounds: a fresh wallet, a faucet for hoodi
  ETH, and a token mint or transfer to the test wallet. Plan for an
  extra day if the test-token setup is fresh.
- publicnode rate-limits are a known low-likelihood / medium-impact risk
  (Phase 1 Risk R4); rerun the e2e if a single RPC call fails with a
  transport error.
- The `approve` → mined → `transfer-from` serialization is the load-bearing
  ordering rule. It cannot be parallelized or interleaved.

---

### Issue 1.11: Commit on `develop`

- **Points:** 1
- **Type:** chore
- **Priority:** P0
- **Blocked by:** 1.9, 1.10a, 1.10b
- **Scope:** 0.5 day

**Description:**
Land the Phase 1 work on `develop` (the integration branch). Suggested
commit shape: one commit per Layer or per issue, ending with the docs +
e2e commits so the history reads top-down. Commit messages reference the
PRD, architecture, and (for the e2e commit) the hoodi token used. Do NOT
PR or merge to `main` unprompted.

**Implementation Notes:**
- Files affected: git only; no source edits in this issue.
- Per repo memory (CLAUDE.md auto-memory): `develop` is the integration
  branch; commit on `develop`; `main` is release-only.
- Suggested commit sequence (squash to taste during PR review):
  - `feat(eth-tx-builder): add build_erc20.py + test_build_erc20.py shells (issue 1.1)`
  - `feat(eth-tx-builder): abi_codec selectors + encoders + decoders (issue 1.2)`
  - `feat(eth-tx-builder): amount_codec with no-float invariant (issue 1.3)`
  - `feat(eth-tx-builder): contract_reads (decimals/symbol/allowance) (issue 1.4)`
  - `feat(eth-tx-builder): gas_estimator no-fallback policy (issue 1.5)`
  - `feat(eth-tx-builder): summary renderer + warning dispatcher (issue 1.6)`
  - `feat(eth-tx-builder): tx_assembly do_transfer/approve/transfer-from (issue 1.7)`
  - `feat(eth-tx-builder): cli_dispatch argparse + main + tests (issue 1.8)`
  - `docs(eth-tx-builder): SKILL.md + README.md ERC-20 update (issue 1.10a)`
  - `docs(eth-tx-builder): hoodi e2e transcripts (issue 1.10b)`
- Commit body for each references the upstream artifacts
  (`plan/eth-tx-builder-erc20/{prd,architecture,project-plan}.md`) and any
  ADR / PRD section the work touches (e.g. `ADR-007` for the gas_estimator
  commit).
- The e2e commit body includes the hoodi token address used for the e2e
  and the three confirming tx hashes.

**Acceptance Criteria:**
- [ ] All Phase 1 changes are committed on the `develop` branch (no
      changes on `main`).
- [ ] Working tree is clean (`git status` shows no uncommitted changes)
      after the commit sequence lands.
- [ ] Final state: `cd .claude/skills/eth-tx-builder && python3 -m unittest
      test_build_send_eth test_build_erc20 -v` is green from a clean
      checkout of `develop`.
- [ ] `git diff main..develop -- .claude/skills/eth-tx-builder/build_send_eth.py
      .claude/skills/eth-tx-builder/test_build_send_eth.py` is empty (P0
      freeze confirmation; the two v1 files are byte-identical to `main`).

**Testing Notes:**
- Per repo memory: do NOT PR / merge to `main` unprompted. If the operator
  wants a release, they will ask.
- The byte-for-byte v1 confirmation via `git diff main..develop` is the
  final PR-review gate for PRD success metric §4.
