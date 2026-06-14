# Software Architecture: eth-tx-builder ERC-20 Extension (Shared-Core Candidate)

> **Candidate:** `shared-core` — DRY reuse by direct import from `build_send_eth.py`.
> **Sibling candidates:** `architecture.modular-monolith.candidate.md`,
> `architecture.extraction-first.candidate.md`,
> `architecture.scale-first.candidate.md`.

## Overview

This candidate extends the `eth-tx-builder` skill with ERC-20 support
(`transfer`, `approve`, `transfer-from`) by **importing the v1 plumbing
directly from `build_send_eth.py`** as if it were a library module, while
adding only the ERC-20-specific code (ABI codec, contract reads, gas
estimation, subcommand CLI, summary printer) in a new sibling file,
`build_erc20.py`.

`build_send_eth.py` and `test_build_send_eth.py` are treated as **read-only**.
No code is extracted out of `build_send_eth.py` into a shared module. The v1
file IS the shared core; `build_erc20.py` simply `import build_send_eth as
_core`. This satisfies the PRD's bit-for-bit unchanged constraint (P0 §17, NFR
Risks) while maximizing DRY reuse (PRD §14, Open Question #1 default
recommendation "import, not duplicate"). Tests stay self-contained per file
(no test-helper sharing across files in v1; promotion to a shared test helper
is an explicit P2).

## Architecture Principles

- **Read-only v1 import contract.** `build_send_eth.py` is a *de facto* library
  for `build_erc20.py`; the public surface is the names listed in PRD §14
  (`NETWORKS`, `network_config`, `rpc_call`, `fetch_nonce`, `fetch_base_fee`,
  `fetch_tip`, `compute_max_fee`, `validate_hex_address`, `parse_hex_int`,
  `RPCError`, plus `USER_AGENT` and `DEFAULT_TIP_WEI` as constants used by
  the fee helpers). No edits, no monkey-patching, no extraction.
- **DRY without re-org.** Every line of fee/RPC/network/validation logic in
  `build_erc20.py` MUST go through an imported symbol from `build_send_eth.py`.
  Drift between the two helpers is structurally prevented by absence of
  duplicated code.
- **Additive only.** `build_erc20.py` introduces *new* concerns (ABI encoding,
  ERC-20 reads, gas estimation, subcommand CLI). It does NOT re-implement any
  v1 plumbing.
- **One-way dependency.** `build_erc20.py` imports `build_send_eth`; the
  reverse never happens. This guarantees no circular dependencies and keeps
  the v1 unit-test suite hermetic.
- **Determinism for tests.** All RPC-touching functions accept an injected
  `rpc` callable (matching v1 style). Tests stub `rpc`; no network in unit
  tests.
- **Error-and-stop vs. warn-don't-block split, applied consistently.**
  Calldata-correctness reads (`decimals`, `eth_estimateGas`) are fatal on
  failure; enrichment reads (`symbol`, `allowance`, `balanceOf`) degrade
  gracefully. (Research overview §2.)
- **Stdout is JSON, stderr is human.** The JSON `TxRequest` is the only thing
  on stdout; the summary and warnings go to stderr (PRD §16).
- **Stdlib only.** Imports limited to the v1 set
  (`argparse, json, re, sys, urllib.request`) plus whatever `build_send_eth`
  already imports transitively. No new third-party deps.

---

## System Context Diagram

```text
                                +-----------------------------------------------+
                                |             eth-tx-builder skill              |
                                |                                               |
   +--------------+             |   +----------------+    +------------------+  |
   |              |   stdout    |   |                |    |                  |  |
   |  Operator /  | <---------- |   | build_send_eth | <- | build_erc20.py   |  |
   |  Claude Code |   stderr    |   |   .py (v1, RO) |    | (new, P0)        |  |
   |              | <---------- |   |                |    |                  |  |
   +-------+------+             |   +-------+--------+    +------+-----------+  |
           |                    |           |                    |              |
           | TxRequest JSON     |           |  imports: NETWORKS,|              |
           v                    |           |  rpc_call, ...     |              |
   +--------------+             |           +---<----------------+              |
   | eth-signer-  |             |                                               |
   |    mcp       |  (offline)  +---------------+---------------+---------------+
   | sign_tx tool |                             |               |
   +------+-------+                             |  outbound RPC | outbound RPC
          |                                     v               v
          v                            +------------------+ +-----------------+
   signed raw tx ->                    | publicnode (RPC) | | publicnode (RPC)|
   eth-rpc broadcast                   |    mainnet       | |     hoodi       |
                                       +------------------+ +-----------------+
```

The boundary that matters here is **inside** the skill: `build_erc20.py`
depends on `build_send_eth.py`, never the other way round. The signer remains
strictly offline; this skill only talks RPC.

---

## Module Overview

| Module | Responsibility | Owns Data | Depends On | Communication |
|---|---|---|---|---|
| `build_send_eth.py` (v1, **read-only**) | ETH-send TxRequest build + shared plumbing (network map, RPC, fees, validation, errors) | `NETWORKS` constant; `RPCError` type | — | sync (function calls); outbound RPC |
| `build_erc20.py` (new) | ERC-20 `transfer`/`approve`/`transfer-from` TxRequest build, ABI encoding, ERC-20 reads, gas estimation, subcommand CLI, stderr summary | None (constants only: selectors, MAX_GAS_CAP, etc.) | `build_send_eth` (import-only), outbound RPC | sync (function calls); outbound RPC |
| `test_build_send_eth.py` (v1, **read-only**) | Regression suite for v1 plumbing and ETH-send path | — | `build_send_eth` | unit tests |
| `test_build_erc20.py` (new) | Unit tests for ERC-20 helper (selectors, encoding, decoders, subcommand happy paths, error/warning paths) | — | `build_erc20` (and transitively `build_send_eth`) | unit tests |
| `SKILL.md` (edit) | Skill manifest: descriptions, inputs, procedure | — | — | docs |
| `README.md` (edit) | Operator-facing docs + manual e2e | — | — | docs |

**Why no shared library module?** Extraction of common helpers into a third
file (e.g. `_tx_core.py`) is explicitly disallowed by the hard constraint
("MUST remain bit-for-bit unchanged ... no extraction of code out of
`build_send_eth.py`"). The shared-core in this candidate IS
`build_send_eth.py` — used directly as an importable module.

---

## Module Dependency Graph

```text
                              +-------------------------+
                              |   build_send_eth.py     |
                              |   (v1, READ-ONLY)       |
                              |                         |
                              |   exports (no re-org):  |
                              |     NETWORKS            |
                              |     USER_AGENT          |
                              |     DEFAULT_TIP_WEI     |
                              |     RPCError            |
                              |     network_config()    |
                              |     validate_hex_address|
                              |     parse_hex_int()     |
                              |     compute_max_fee()   |
                              |     rpc_call()          |
                              |     fetch_nonce()       |
                              |     fetch_base_fee()    |
                              |     fetch_tip()         |
                              +-----------+-------------+
                                          ^
                                          |  import build_send_eth as _core
                                          |  (one-way, additive only)
                                          |
                              +-----------+-------------+
                              |    build_erc20.py       |
                              |    (new, P0)            |
                              |                         |
                              |  adds:                  |
                              |    SELECTORS (constants)|
                              |    _encode_address      |
                              |    _encode_uint256      |
                              |    _pack_call           |
                              |    parse_human_amount   |
                              |    decode_decimals      |
                              |    decode_symbol        |
                              |    decode_allowance     |
                              |    fetch_decimals       |
                              |    fetch_symbol         |
                              |    fetch_allowance      |
                              |    estimate_gas         |
                              |    build_transfer       |
                              |    build_approve        |
                              |    build_transfer_from  |
                              |    print_summary        |
                              |    main / subparsers    |
                              +-------------------------+

test_build_send_eth.py  --->  build_send_eth.py     (untouched)
test_build_erc20.py     --->  build_erc20.py        (transitively build_send_eth)
```

**Verification of acyclicity.** `build_send_eth.py` has zero imports of any
skill-local module; it only imports stdlib. `build_erc20.py` imports
`build_send_eth` and stdlib. The graph is a strict DAG: two nodes, one edge.

**Verification of test isolation.**
`python3 -m unittest test_build_send_eth -v` MUST not load `build_erc20.py`
at all (the v1 test file does not import it). This preserves the PRD success
metric "No regression in ETH-send."

---

## Module Details

### Module: `build_send_eth.py` (v1, read-only consumer of nothing; provider of plumbing)

**Responsibility:** Build the EIP-1559 ETH-send TxRequest (the original v1
job) AND, *by virtue of being importable*, serve as the shared-core library
that `build_erc20.py` reuses.

**Status:** Bit-for-bit unchanged from the pre-extension state. PRD P0 §17.

**Domain Entities (unchanged):** network config, RPC endpoint, EIP-1559 fee
fields, ETH-send TxRequest.

**Data Store:** None. Pure in-memory; one module-level dict (`NETWORKS`).

**Public API (consumed by `build_erc20.py`):**

| Name | Kind | Used by build_erc20 for |
|---|---|---|
| `NETWORKS` | dict constant | (Read-only) introspection only if needed; subcommand `choices` re-uses `sorted(_core.NETWORKS)` |
| `USER_AGENT` | str constant | Not used directly by `build_erc20`, but used transitively via `_core.rpc_call`. No need to re-export. |
| `DEFAULT_TIP_WEI` | int constant | Implicitly via `_core.fetch_tip`. Direct use unlikely. |
| `RPCError` | exception class | Caught in `build_erc20` around `estimate_gas`, `fetch_decimals`, `fetch_symbol`, `fetch_allowance` |
| `network_config(network)` | fn → (chain_id, url) | Resolving `--network` to chain_id + RPC URL |
| `validate_hex_address(addr)` | fn → addr | All address arg validation (`--token`, `--to`, `--spender`, `--from`, `--sender`) |
| `parse_hex_int(s)` | fn → int | Decoding RPC hex responses (decimals word, allowance word, estimate result, etc.) |
| `compute_max_fee(base, tip)` | fn → int | Reused unchanged (`baseFee*2 + tip`) |
| `rpc_call(url, method, params, timeout=15)` | fn → result | The default `rpc` callable injected into `build_erc20` build functions |
| `fetch_nonce(rpc, url, sender)` | fn → int | Same as v1 |
| `fetch_base_fee(rpc, url)` | fn → int | Same as v1 |
| `fetch_tip(rpc, url)` | fn → int | Same as v1 (with 1-gwei fallback) |

**Events Published:** None (sync CLI).

**Events Consumed:** None.

**Internal Structure:** Single-file Python module. Unchanged.

**Key Design Decisions (unchanged from v1, restated for completeness):**

- Network map is hardcoded; no `--rpc-url` override in v1 (PRD Out-of-Scope).
- Fees follow the standard wallet heuristic (`baseFee*2 + tip`, 1 gwei tip
  fallback).
- All RPC-touching functions take an injected `rpc` callable for testability.
- Errors raise `ValueError` (input) or `RPCError` (transport/RPC); `main()`
  prints `error: <msg>` and exits 1.

**Failure Modes:**

- Same as today. No change in behavior because no code in this file changes.

**Constraint:** "No edits" is enforced operationally by:

1. PRD P0 §17 + Success Metric "No regression in ETH-send."
2. The CI step `python3 -m unittest test_build_send_eth -v` must pass.
3. Code review against the pre-extension SHA of this file.

---

### Module: `build_erc20.py` (new, P0)

**Responsibility:** Produce a ready-to-sign EIP-1559 `TxRequest` for any of
the three ERC-20 movement operations (`transfer`, `approve`, `transfer-from`)
on supported networks, using human-readable amounts, calldata encoded
in-stdlib, gas estimated live with a buffer+cap, and a loud stderr summary —
reusing v1 plumbing by direct import.

**Domain Entities:**

- ERC-20 operation (one of `transfer`, `approve`, `transfer-from`).
- ABI-encoded calldata word (32-byte hex).
- Token metadata (decimals, optional symbol).
- Per-op counterparties (token, recipient, spender, source `from`, signer).
- Gas-estimation result (raw, buffered, capped).

**Data Store:** None. Module-level constants only.

**Constants (module level):**

| Name | Value | Source |
|---|---|---|
| `SEL_TRANSFER` | `"0xa9059cbb"` | `keccak("transfer(address,uint256)")[:4]` |
| `SEL_APPROVE` | `"0x095ea7b3"` | `keccak("approve(address,uint256)")[:4]` |
| `SEL_TRANSFER_FROM` | `"0x23b872dd"` | `keccak("transferFrom(address,address,uint256)")[:4]` |
| `SEL_DECIMALS` | `"0x313ce567"` | `keccak("decimals()")[:4]` |
| `SEL_SYMBOL` | `"0x95d89b41"` | `keccak("symbol()")[:4]` |
| `SEL_ALLOWANCE` | `"0xdd62ed3e"` | `keccak("allowance(address,address)")[:4]` |
| `MAX_UINT256` | `(1 << 256) - 1` | ABI uint256 max |
| `GAS_BUFFER_NUM`, `GAS_BUFFER_DEN` | `12, 10` | +20% buffer (PRD §9) |
| `MAX_GAS_CAP` | `300_000` | PRD §9 |
| `DECIMALS_SUSPICIOUS_CAP` | `36` | PRD Tech Considerations |

Selectors are written as literal hex strings with a one-line derivation
comment; the file does NOT import any Keccak library (PRD NFR).

**Public API (CLI; consumed by operators and SKILL.md):**

| Subcommand | Args | Output | Description |
|---|---|---|---|
| `transfer` | `--network --token --to --amount --sender` | TxRequest JSON (stdout) + summary (stderr) | Holder sends own tokens |
| `approve` | `--network --token --spender (--amount \| --approve-max) --sender` | TxRequest JSON + summary + `WARNING:` on `--approve-max` | Authorize spender |
| `transfer-from` | `--network --token --from --to --amount --sender` | TxRequest JSON + summary + soft-check `WARNING:` if allowance < amount | Spender pulls approved tokens |

Top-level `--help` lists all three subcommands; each subcommand has its own
`--help`.

**Public API (internal; used by `test_build_erc20.py`):**

Pure helpers (no I/O):

| Name | Signature | Purpose |
|---|---|---|
| `_encode_address(addr_hex)` | `(str) -> str` | 64-hex-char left-padded address word |
| `_encode_uint256(n)` | `(int) -> str` | 64-hex-char left-padded uint256 word; rejects negatives / `>= 2**256` |
| `_pack_call(selector_hex, *args_hex)` | `(str, *str) -> str` | Concatenate selector + words, prepend `0x` |
| `parse_human_amount(s, decimals)` | `(str, int) -> int` | `str -> str -> int`, no float; rejects negatives, multi-dot, too-many-fractional-digits, non-numeric |
| `decode_decimals(hex_word)` | `(str) -> int` | `int(word, 16) & 0xff`; rejects `> DECIMALS_SUSPICIOUS_CAP` |
| `decode_symbol(hex_bytes)` | `(str) -> str \| None` | Tries ABI `string` first, falls back to NUL-trimmed `bytes32`; returns `None` on failure |
| `decode_allowance(hex_word)` | `(str) -> int` | `int(word, 16)` |

RPC-touching helpers (accept injected `rpc`):

| Name | Signature | Calls | Failure |
|---|---|---|---|
| `fetch_decimals(rpc, url, token)` | `-> int` | `eth_call` `decimals()` on `latest` | Raises `ValueError`/`_core.RPCError` — **fatal** in build funcs |
| `fetch_symbol(rpc, url, token)` | `-> str \| None` | `eth_call` `symbol()` | Catches all; returns `None` on any failure |
| `fetch_allowance(rpc, url, token, holder, spender)` | `-> int \| None` | `eth_call` `allowance(holder, spender)` | Catches all; returns `None` on failure (soft) |
| `estimate_gas(rpc, url, sender, token, calldata)` | `-> int` | `eth_estimateGas` with `{from, to, data, value:"0x0"}` against `"latest"` | Lets `_core.RPCError` propagate; the build wrapper applies buffer + cap, surfaces error |

Build functions (pure given injected `rpc`):

| Name | Signature | Purpose |
|---|---|---|
| `build_transfer(network, token, to, amount_str, sender, rpc=_core.rpc_call)` | `-> (tx_dict, summary_ctx)` | Builds ERC-20 transfer TxRequest |
| `build_approve(network, token, spender, amount_str, sender, approve_max=False, rpc=_core.rpc_call)` | `-> (tx_dict, summary_ctx)` | Builds approve TxRequest; if `approve_max`, sets amount to MAX_UINT256 and the summary marks it |
| `build_transfer_from(network, token, src, to, amount_str, sender, rpc=_core.rpc_call)` | `-> (tx_dict, summary_ctx)` | Builds transferFrom TxRequest; populates allowance soft-check result in `summary_ctx` |

Each build function returns the JSON-ready TxRequest dict (matching v1 shape
exactly: `type, chainId, nonce, to, value, data, gas, maxFeePerGas,
maxPriorityFeePerGas`) and a `summary_ctx` dict the CLI driver uses to
generate the stderr summary. **Returning the summary context (instead of
printing inside the build) keeps the build functions pure for testing.**

CLI driver:

| Name | Signature | Purpose |
|---|---|---|
| `print_summary(op, ctx)` | `(str, dict) -> None` | Emit the multi-line stderr summary; deterministic format for snapshot tests |
| `_warn(msg)` | `(str) -> None` | `sys.stderr.write("WARNING: " + msg + "\n")` |
| `main(argv=None)` | `-> int` | Argparse subparsers; dispatch; print JSON to stdout, summary+warnings to stderr; exit code |

**Events Published / Consumed:** None (sync CLI).

**Internal Structure:**

```
build_erc20.py
  # ---- imports ----
  import argparse
  import json
  import sys
  import build_send_eth as _core         # shared plumbing (read-only import)

  # ---- constants ----
  # selectors, MAX_UINT256, GAS_BUFFER_*, MAX_GAS_CAP, DECIMALS_SUSPICIOUS_CAP

  # ---- ABI codec (pure) ----
  _encode_address, _encode_uint256, _pack_call

  # ---- amount parsing (pure) ----
  parse_human_amount

  # ---- decoders (pure) ----
  decode_decimals, decode_symbol, decode_allowance

  # ---- contract reads (rpc-injected) ----
  fetch_decimals, fetch_symbol, fetch_allowance

  # ---- gas estimation (rpc-injected) ----
  estimate_gas, _apply_buffer_cap

  # ---- build helpers (rpc-injected, return (tx, summary_ctx)) ----
  _build_common_eip1559_fields    # internal: nonce/fees via _core helpers
  build_transfer, build_approve, build_transfer_from

  # ---- presentation ----
  print_summary, _warn

  # ---- CLI ----
  main, if __name__ == "__main__": sys.exit(main())
```

**Key Design Decisions:**

- **DD-1: import the v1 file as a module; do not extract.** Satisfies PRD §14
  and the hard constraint. Trade-off: `build_send_eth.py`'s shape (which
  symbols are public) is now load-bearing for `build_erc20.py` as well; we
  encode this in a single docstring at the top of `build_erc20.py` listing
  the imported names and the fact that they MUST exist on the v1 module.
- **DD-2: import as `_core` (single alias).** The leading underscore signals
  "library, not for further re-export" and the single alias makes it trivial
  to grep for every cross-module call site (`_core.`). Tests can monkey-patch
  `_core.rpc_call` on the `build_erc20` namespace if needed, mirroring the v1
  test pattern.
- **DD-3: build functions return `(tx_dict, summary_ctx)`; printing lives in
  the CLI driver.** Keeps `build_transfer` / `build_approve` /
  `build_transfer_from` deterministic and easy to assert on. Matches v1's
  pattern (`build_tx_request` returns a dict; `main()` does the I/O).
- **DD-4: `estimate_gas` does not include fee fields in the call object.**
  Research §3 + research/03-gas-estimation §"Why ... not include
  `gasPrice`/`maxFeePerGas`". The estimate object is `{from, to, data,
  value:"0x0"}`.
- **DD-5: `decimals()` failure is fatal; `symbol()` failure is graceful;
  `allowance()` failure is graceful.** Matches research overview §2 ("read
  the correctness depends on" vs. "read that enriches the summary").
- **DD-6: human → base units is integer-only.** `parse_human_amount` does
  not call `float()` anywhere. A test asserts the source contains no
  `float(` call inside the conversion function (or uses `inspect.getsource`
  + substring check).
- **DD-7: `--approve-max` is mutually exclusive with `--amount` and triggers
  a multi-line stderr WARNING.** Implemented with argparse's
  `add_mutually_exclusive_group(required=True)`; the warning is printed in
  `main()` before the JSON.
- **DD-8: allowance soft-check on `transfer-from` warns but still emits
  JSON.** Returning `(allowance_value | None)` in `summary_ctx` lets the CLI
  driver decide; `None` means "RPC failed, skipped check"; an int below the
  requested amount triggers the warning.
- **DD-9: `_apply_buffer_cap(est) = min((est * 12) // 10, 300_000)`** —
  exact PRD formula; tested with boundary values (0, 1, 250_000 → 300_000
  cap, 250_001 → exactly 300_000 since `(250_001*12)//10 = 300_001` capped).

**Failure Modes:**

| Trigger | Behavior |
|---|---|
| `--token`/`--to`/`--spender`/`--from`/`--sender` malformed | Catch `ValueError` from `_core.validate_hex_address`, print `error: ...`, exit 1 |
| `parse_human_amount` rejects input (negatives, too-many-frac-digits, multi-dot, non-numeric) | Same: `error: ...`, exit 1 |
| `fetch_decimals` fails (RPC error or suspicious value) | Same: `error: ...`, exit 1 |
| `fetch_symbol` fails | Warn-print-nothing; summary shows `(symbol unavailable)`; build continues |
| `fetch_allowance` (transfer-from soft-check) fails | Warn `WARNING: allowance soft-check skipped: <err>`; build continues |
| `fetch_allowance` returns value below requested | Warn `WARNING: current allowance ... requested ... tx will revert ...`; build continues |
| `estimate_gas` fails (RPCError of any shape) | Print full error from node, exit 1, do NOT emit JSON. No fallback. |
| `_core.NETWORKS` does not contain the requested network | Same as v1: `_core.network_config` raises `ValueError` → `error: ...`, exit 1 |
| v1 file rename / symbol removal | Top-of-file `import build_send_eth as _core` fails — surfaces immediately at startup, not at runtime. Caught by `make test`. |

---

## Cross-Cutting Concerns

### Authentication & Authorization

N/A inside the skill. The skill talks RPC to public endpoints; the
`eth-signer-mcp` server (consumer of the produced JSON) handles signer
identity. The `--sender` value is sourced upstream via `get_address`
(SKILL.md procedure) and treated as data here.

### Logging & Observability

- All diagnostic output goes to **stderr**: summaries, warnings, and final
  `error: ...` messages on failure.
- **Stdout** is reserved for the JSON `TxRequest` so operators can pipe
  cleanly: `python3 build_erc20.py transfer ... | jq .`.
- No structured logging framework. The skill is a short-lived CLI; PRD NFR
  "stdlib only" + small surface area.
- WARNING lines start with `WARNING:` (allcaps); errors start with
  `error:` (lowercase) — matches v1.

### Error Handling

- Two error categories, both inherited from v1:
  - `ValueError` for input validation (addresses, amounts, network).
  - `_core.RPCError` for transport / JSON-RPC failures.
- Pattern in `main()`:

  ```python
  try:
      tx, ctx = dispatch(args)
  except (ValueError, _core.RPCError) as e:
      print("error: %s" % e, file=sys.stderr)
      return 1
  ```

- Surfacing revert data on `eth_estimateGas` failure: `_core.RPCError`
  already wraps the JSON-RPC `{code, message, data}` in its message; we
  pass that string through verbatim (research/03-gas-estimation §"Surface
  the revert reason").

### Configuration

- Networks live in `_core.NETWORKS` (v1 source of truth). `build_erc20.py`
  pulls subcommand `choices` from `sorted(_core.NETWORKS)`. Adding
  `sepolia`/`holesky` in PRD Phase 2 means editing the v1 file. (See §Open
  Questions below — this is the one place the "v1 read-only" constraint
  bites later.)
- No env vars in v1.
- No `--rpc-url` override in v1 (PRD Out-of-Scope).

---

## Data Flow Diagrams

### Flow: `transfer 1.5 USDC` (P0 happy path)

```text
Operator
   |
   |  python3 build_erc20.py transfer --network mainnet
   |    --token 0xUSDC --to 0xRecipient --amount 1.5 --sender 0xMe
   v
[build_erc20.main]
   |
   |-- argparse -> args namespace
   |
   |-- _core.network_config("mainnet") -> (1, "https://...publicnode.com")
   |
   |-- _core.validate_hex_address(token, to, sender)        x3
   |
   |-- fetch_decimals(rpc, url, token)
   |     `-- rpc("eth_call", [{to:token, data:SEL_DECIMALS}, "latest"])
   |         -> "0x06"  -> 6  (decode_decimals)
   |
   |-- parse_human_amount("1.5", 6) -> 1_500_000
   |
   |-- fetch_symbol(rpc, url, token)
   |     `-- rpc("eth_call", [{to:token, data:SEL_SYMBOL}, "latest"])
   |         -> "0x..." -> "USDC"  (decode_symbol; bytes32 fallback ready)
   |     (failure -> None, summary degrades)
   |
   |-- calldata = _pack_call(SEL_TRANSFER,
   |                         _encode_address(to),
   |                         _encode_uint256(1_500_000))
   |
   |-- estimate_gas(rpc, url, sender, token, calldata) -> est
   |     `-- rpc("eth_estimateGas", [{from, to:token, data, value:"0x0"}, "latest"])
   |     `-- _apply_buffer_cap(est) -> min((est*12)//10, 300_000)
   |     (failure -> raise -> main prints error, exit 1, NO JSON)
   |
   |-- _core.fetch_nonce / _core.fetch_base_fee / _core.fetch_tip
   |     -> _core.compute_max_fee
   |
   |-- assemble tx_dict (v1 shape; to=token, value="0", data=calldata, gas=buffered_cap)
   |
   |-- print_summary("transfer", ctx) -> stderr
   |
   `-- json.dumps(tx_dict, indent=2) -> stdout
```

### Flow: `transfer-from` allowance soft-check (warn-don't-block)

```text
[build_transfer_from]
   |
   |-- decimals = fetch_decimals(...)
   |-- amount   = parse_human_amount(...)
   |-- fetch_allowance(rpc, url, token, src, sender)
   |     +-- ok, value=N: ctx["allowance"] = N
   |     `-- RPCError:    ctx["allowance"] = None (sentinel "skipped")
   |
   |-- estimate_gas / _core fees / assemble tx
   |
   `-- return (tx, ctx)

[main]
   |
   |-- if ctx.allowance is None: _warn("allowance soft-check skipped: ...")
   |-- elif ctx.allowance < amount: _warn("current allowance ... requested ...")
   |-- print_summary(...)
   `-- print(json.dumps(tx))
```

### Flow: `eth_estimateGas` failure (error-and-stop)

```text
[build_transfer]
   |
   |-- ... validations + fetch_decimals + parse + pack calldata
   |-- estimate_gas raises _core.RPCError("RPC error for eth_estimateGas: {...}")
   v
[main]
   |
   |-- caught -> print("error: " + str(e)) to stderr
   |-- exit 1
   `-- (nothing printed to stdout; no partial TxRequest)
```

---

## Infrastructure & Deployment

### Deployment Model

- **Monorepo, in-tree.** Both files live under
  `.claude/skills/eth-tx-builder/` (per PRD Technical Considerations).
- **No build step.** Pure Python stdlib; invocation is
  `python3 build_erc20.py <subcommand> ...`.
- **No packaging.** Not a wheel, not a console-script entry-point. The
  Claude Code skill invokes it via direct subprocess (matches v1 SKILL.md
  procedure).
- **CI.** `python3 -m unittest test_build_send_eth -v` and
  `python3 -m unittest test_build_erc20 -v` run from the skill directory.
  Both must pass.

### Scaling Strategy

- N/A. This is a CLI tool with one invocation = one TxRequest. The two
  scaling concerns are (1) RPC endpoint rate-limits, which neither file
  controls (public RPCs are shared infrastructure) and (2) human-throughput
  on the operator side, which an interactive Claude session moderates.

### Service Extraction Path

| Module | Extraction readiness | Notes |
|---|---|---|
| `build_send_eth.py` | **Ready now** (and is, in this candidate, already acting as a library for `build_erc20.py`). Could be split off into a sibling Python package (`tx_core/`) without behavioral change. The hard constraint forbids doing so now. | Future: if a third helper appears (e.g. `build_erc721.py`), promote `build_send_eth.py`'s helpers into a `_tx_core.py` and have all three consumers import it. The shared-core principle scales by replacing the alias `_core = build_send_eth` with `_core = tx_core`. |
| `build_erc20.py` | **Ready now** — its dependency on v1 is import-only and replaceable with the same `_core` alias pattern. | No data store, no global state; trivially relocatable. |

**The shared-core pattern's extraction story.** When the v1 read-only
constraint is relaxed (later phase, or a future repo refactor), the only
mechanical step is to extract `build_send_eth.py`'s top-half (plumbing) into
`_tx_core.py`, leaving `build_send_eth.py` with just `build_tx_request` +
`main` (the ETH-send-specific code) importing the new core. Every existing
call in `build_erc20.py` (already prefixed `_core.`) keeps working by
changing one line. The `_core` alias is the seam.

---

## Technology Choices

| Concern | Choice | Rationale |
|---|---|---|
| Language | Python 3.8+ | Matches v1; PRD NFR "stdlib only" |
| Std libs | `argparse, json, re, sys, urllib.request` | Exact v1 set; no new imports introduced by `build_erc20.py` |
| Module layout | Two sibling files + direct `import build_send_eth as _core` | Hard constraint; maximizes DRY without extraction |
| ABI codec | Hand-coded, hex-string concatenation, `int.to_bytes` | PRD §8 + research/01-abi-encoding §"Argument encoding" |
| Keccak | None. Selectors hardcoded as constants. | PRD NFR; research §"Hardcode the six selectors" |
| Big-int math | Python `int` | Native arbitrary precision; no overflow at uint256 |
| RPC transport | `_core.rpc_call` (v1 `urllib.request.urlopen`) | Reuse v1 |
| Test framework | `unittest` + `unittest.mock` | Matches v1; stdlib only |

---

## ADRs

### ADR-001: Reuse v1 plumbing by direct module import, not by extraction.

- **Status:** Accepted (this is the candidate's defining choice).
- **Context:** PRD §14 leaves the share-vs-duplicate question to architecture
  stage. Hard constraint forbids modifying `build_send_eth.py` or extracting
  code from it.
- **Decision:** `build_erc20.py` does `import build_send_eth as _core` and
  consumes the named symbols listed in PRD §14 verbatim.
- **Alternatives Considered:**
  1. **Duplicate the helpers in `build_erc20.py`.** Doubles the code
     surface; introduces drift risk between two `compute_max_fee`,
     `rpc_call`, and `fetch_*` implementations. Rejected: violates PRD
     intent (DRY) without any benefit beyond moot file-independence.
  2. **Extract helpers into `_tx_core.py` and have both files import it.**
     Cleanest long-term, but explicitly forbidden by the hard constraint
     ("no extraction of code out of build_send_eth.py, treat it as a
     read-only module"). Rejected for v1; reserved for a future phase
     when the constraint is relaxed.
- **Consequences:**
  - **Plus:** zero duplication; structural prevention of drift; `make test`
    on the v1 file remains untouched; the shared-core IS the v1 file.
  - **Minus:** `build_send_eth.py` now has *two* roles (CLI + library);
    its public symbol set is load-bearing for `build_erc20.py`. Mitigated
    by listing the imported names in a top-of-file docstring in
    `build_erc20.py` so a future v1 rename surfaces a clear failure.
  - **Minus:** loading `build_erc20.py` always loads `build_send_eth.py`
    (negligible cost; one module).
  - **Plus:** matches Python convention — many stdlib modules are also
    "scripts" via `python3 -m`. The dual-role pattern is well-precedented.

### ADR-002: Build functions return `(tx_dict, summary_ctx)`; CLI driver prints.

- **Status:** Accepted.
- **Context:** PRD §16 mandates a stderr summary; PRD success metrics demand
  unit tests that assert on TxRequest shape and on warning emission.
- **Decision:** `build_transfer / build_approve / build_transfer_from`
  return a 2-tuple: the v1-shape `tx_dict` for stdout, and a `summary_ctx`
  dict carrying the fields the CLI driver needs to render the human summary
  and emit warnings (symbol, decimals, human/base amount, counterparty
  labels, allowance result for transfer-from).
- **Alternatives Considered:**
  - Build prints summary directly (matches a naive port of v1's `main()`
    flow). Rejected: couples I/O to logic, breaks test purity, makes
    snapshot-style summary assertions harder.
  - Build returns `tx_dict` only; CLI re-derives summary fields. Rejected:
    forces the CLI to re-run RPC reads or re-parse amounts.
- **Consequences:** Tests for `build_*` assert on dict equality (and on
  `summary_ctx` keys); a separate `print_summary(op, ctx)` test asserts on
  rendered output via `io.StringIO` (matches v1 test style).

### ADR-003: Selectors hardcoded; no live Keccak.

- **Status:** Accepted.
- **Context:** PRD NFR "stdlib only"; research §"Hardcode the six
  selectors."
- **Decision:** All six selectors are module-level string constants with
  one-line derivation comments referencing the canonical signature.
- **Alternatives Considered:** Vendor a minimal Keccak in pure Python.
  Rejected: extra surface for security review, no upside given selectors
  are stable forever (function-signature ABI doesn't change).
- **Consequences:** A test compares the constants against known-good values
  (the table in research/01-abi-encoding); if a future maintainer mis-types
  a selector, the test fails immediately.

### ADR-004: `eth_estimateGas` failure is fatal; no fallback.

- **Status:** Accepted.
- **Context:** PRD §9 and Risks; research/03-gas-estimation §"Why a silent
  hardcoded-gas fallback is dangerous."
- **Decision:** `_core.RPCError` from `eth_estimateGas` propagates to
  `main()`, which prints the error and exits 1. The error message includes
  the node's revert data verbatim (`code 3` + `data`).
- **Alternatives Considered:** Hardcoded fallback (e.g. 100k). Rejected:
  signed tx would burn its gas budget on revert at broadcast.
- **Consequences:** Operators see the underlying revert reason ("ERC20:
  transfer amount exceeds balance", "Pausable: paused", etc.) at build
  time. Test asserts no JSON on stdout when estimate fails.

### ADR-005: `decimals()` failure is fatal; `symbol()` and `allowance()` are best-effort.

- **Status:** Accepted.
- **Context:** Research overview §2 reconciliation: a read whose result
  determines calldata correctness is fatal; a read that only enriches the
  summary degrades gracefully.
- **Decision:**
  - `fetch_decimals` raises on RPC error or suspicious value (>36) —
    `main()` exits 1.
  - `fetch_symbol` returns `None` on any failure; summary shows
    `(symbol unavailable)`.
  - `fetch_allowance` (transfer-from soft-check) returns `None` on any
    failure; CLI emits a `WARNING:` line and proceeds.
- **Consequences:** Three different error postures, applied
  consistently. Tests cover each.

### ADR-006: One-way dependency; `build_erc20.py` imports `build_send_eth`, never the reverse.

- **Status:** Accepted.
- **Context:** Hard constraint + acyclic dependency principle.
- **Decision:** Single import line at the top of `build_erc20.py`:
  `import build_send_eth as _core`. `build_send_eth.py` MUST NOT import
  `build_erc20`.
- **Alternatives Considered:** None justifiable.
- **Consequences:** Tests in `test_build_send_eth.py` never import
  `build_erc20` — preserved by leaving the v1 test file untouched.

### ADR-007: CLI is a subparser, not three sibling scripts.

- **Status:** Accepted.
- **Context:** PRD P0 §1: "three subcommands."
- **Decision:** `python3 build_erc20.py transfer ...`,
  `python3 build_erc20.py approve ...`,
  `python3 build_erc20.py transfer-from ...`. Implemented via
  `argparse.add_subparsers(required=True)`.
- **Alternatives Considered:** Three separate Python files. Rejected:
  triples the test surface and the import-from-`build_send_eth` line; no
  upside.
- **Consequences:** The CLI driver dispatches on `args.command`. Each
  subparser configures only the args it needs; mutually exclusive group on
  `approve` for `--amount` vs `--approve-max`.

---

## Open Questions

1. **Adding networks (sepolia / holesky, PRD P1).** The `NETWORKS` dict
   lives in `build_send_eth.py`, the read-only file. PRD P1 explicitly
   plans to add networks. Two paths:
   (a) accept that "read-only" only applies during the P0 phase and edit
   the v1 dict in P1, OR
   (b) introduce an additive layer in `build_erc20.py`:
   `EXTRA_NETWORKS = {...}` + a `resolve_network(name)` helper that
   consults both maps. (b) preserves the constraint indefinitely but
   produces two divergent surfaces (one helper sees `mainnet`+`hoodi`,
   the other sees all four). **Recommendation:** when P1 lands, relax
   the read-only constraint to "no behavior change to ETH-send path,"
   add the two networks to the v1 dict, and add v1 tests. Until then,
   v1 mainnet/hoodi suffices.

2. **Promoting approve-race check from P1 to P0** (research §"Critique of
   the PRD's specific decisions"). One extra `allowance(sender, spender)`
   call. Architecturally fits the same warn-don't-block pattern as the
   transfer-from soft-check. Decision deferred to PRD owner; the
   architecture supports either placement.

3. **Test-helper sharing between `test_build_send_eth.py` and
   `test_build_erc20.py`.** The v1 file defines `make_fake_rpc`; the new
   test file will want the same shape. Hard constraint says the v1 *test*
   file is unchanged. **Recommendation:** copy `make_fake_rpc` into the
   new test file with a one-line comment ("duplicated from
   `test_build_send_eth.py` to preserve v1 test isolation; promote to
   `_test_helpers.py` when the read-only constraint is relaxed"). 12
   lines of duplication is well below the threshold where drift bites.

4. **Should `build_erc20.py` expose `RPCError` for downstream re-use?**
   E.g. `RPCError = _core.RPCError` at module top so external tests can
   catch it via `build_erc20.RPCError`. No external consumer exists yet;
   defer until a need surfaces.

---

## Risks

| Risk | Mitigation |
|---|---|
| Future maintainer edits `build_send_eth.py` and breaks an imported symbol used by `build_erc20.py`. | Top-of-file docstring in `build_erc20.py` lists the imported names; `test_build_erc20.py` imports the same names from the `_core` module directly to exercise the import edge; CI fails fast on missing-name. |
| Future maintainer adds a fallback to `eth_estimateGas` in `build_erc20.py` "for robustness." | ADR-004 + a code comment at the call site explaining why this is intentional. Test asserts no JSON on stdout when estimate fails. |
| Drift between v1 and `build_erc20.py`'s fee handling. | Structurally prevented: there is only one `compute_max_fee` / `fetch_*` implementation, in v1. |
| ABI selector typo. | Test compares constants against the canonical hex strings from research/01-abi-encoding's verified table. |
| `decimals()` returning a malformed or hostile value. | `decode_decimals` masks to low byte, rejects > 36 (PRD Tech Considerations); test covers 0, 6, 18, and 37 (rejected). |
| Float drift in amount conversion. | `parse_human_amount` is `str -> str -> int`; test grep / `inspect.getsource` asserts no `float(` in the function. |
| `build_send_eth.py` file rename (refactor, package extraction). | The single `import build_send_eth as _core` line becomes the only thing to update. Catch via CI (import error at startup). |
| Read-only constraint forces NETWORKS duplication when adding sepolia/holesky. | See Open Question #1. |
| Stdout pollution by accidental `print` in build/decode paths. | `print_summary` and `_warn` write to `sys.stderr` explicitly; CI test asserts stdout is exactly the JSON. |

---

## Assumptions

(In place of clarification questions, per the task brief.)

1. **The v1 module is import-safe.** `import build_send_eth as _core` runs
   the v1 module body at import time. Inspection of
   `build_send_eth.py` shows the module body only defines names and the
   `NETWORKS` dict; the `if __name__ == "__main__":` guard prevents
   `main()` from running on import. Safe.

2. **Both files live in the same directory** (`.claude/skills/eth-tx-builder/`),
   so `import build_send_eth` works without `sys.path` games. The PRD
   Technical Considerations §"Files added" confirms this layout.

3. **The PRD's `--token`, `--to`, `--from`, `--sender`, `--spender`
   address validation is format-only.** This matches v1
   `validate_hex_address` semantics; no checksum enforcement here.

4. **The PRD's "stdlib only" applies to runtime AND tests.** The v1 test
   file uses `unittest` + `unittest.mock` only; the new test file will
   too.

5. **`make_fake_rpc` is duplicated, not shared.** Hard constraint on the
   v1 test file blocks extraction; ~12 lines of duplication is acceptable
   (Open Question #3).

6. **`fetch_decimals` failure includes both transport failure and an
   out-of-band value (> 36).** Both surface as `error: ...` and exit 1.
   The PRD treats the decimals path as fatal for calldata correctness.

7. **`eth_call` block tag is `"latest"`** for `decimals`, `symbol`,
   `allowance`, matching `eth_estimateGas`'s `"latest"`. Research
   confirms this; PRD §6 implies it.

8. **No extra RPC retries.** The skill issues each RPC once; `_core.rpc_call`
   already enforces a 15-second timeout. No exponential backoff or retry
   layer — PRD NFR Performance and the v1 pattern.

9. **The `decode_symbol` fallback handles MKR-style `bytes32` symbols
   only.** A fully bullet-proof legacy decoder is P2 (PRD Nice-to-Have);
   v1 trims trailing NULs and decodes as UTF-8; on failure, returns `None`.

10. **Summary stderr output is line-oriented, ASCII-only, with a stable
    layout** (a future feature is `--summary-only`, PRD P1). Snapshot
    tests can match on lines rather than block-wise diff.

11. **`approve --approve-max` is mutually exclusive with `--amount` at
    the argparse layer**, not at the build-function layer. The build
    function accepts an optional `approve_max=False` and resolves to
    `MAX_UINT256` when set; the CLI guarantees they are not both supplied.

12. **No `--rpc-url` override.** Reuses v1 NETWORKS only (PRD
    Out-of-Scope).

13. **Test files run via `python3 -m unittest test_<name> -v` from inside
    the skill directory.** Matches v1; the README will reflect this.

14. **The `_core` alias is a private convention** — external code (none
    today) doesn't reach into `build_erc20._core`. If a need arises,
    promote to `RPCError = _core.RPCError` as a re-export (Open
    Question #4).

15. **CI invariant.** The repo's `make test` runs both test files; both
    must be green. If a future change to the v1 file removes a symbol
    `build_erc20.py` imports, the test run fails at import time, before
    any test case runs — clearly attributable.

---

## Architecture Quality Checklist

- [x] **No circular dependencies.** Two-node DAG: `build_erc20 ->
  build_send_eth`. The v1 test file imports v1 only; the new test file
  imports the new module (and transitively v1).
- [x] **Each module has a single, clear responsibility.**
  `build_send_eth.py`: build ETH-send TxRequest + (dual role) shared
  plumbing. `build_erc20.py`: build ERC-20 TxRequest variants by reusing
  the plumbing.
- [x] **No shared databases.** No databases at all.
- [x] **Inter-module communication is through a defined interface.** The
  PRD §14 symbol list IS the interface; documented in the top of
  `build_erc20.py`.
- [x] **Every module can be tested in isolation.** Both build functions
  accept an injected `rpc`; v1 tests already prove this works.
- [x] **Cross-cutting concerns standardized.** Error format (`error:`,
  `WARNING:`), logging destination (stderr), exit codes (0/1) — all
  follow v1.
- [x] **Failure modes defined.** §"Failure Modes" table per module +
  cross-cutting §"Error Handling".
- [x] **Service extraction path clear.** When the v1 read-only constraint
  is relaxed, swap the `_core` alias target from `build_send_eth` to a
  freshly extracted `_tx_core.py`. One-line change.
- [x] **Data flow traceable.** §"Data Flow Diagrams" shows transfer
  happy-path, transfer-from soft-check, and estimate-fail-stop.
- [x] **Module count is justified.** Two files for two distinct CLI
  concerns; further splitting would force the extraction this candidate
  is forbidden to do.
