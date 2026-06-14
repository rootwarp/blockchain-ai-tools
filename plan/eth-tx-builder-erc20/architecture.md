# Software Architecture: eth-tx-builder ERC-20 Extension

> Status: FINAL. Consolidates the three scored candidates
> (`architecture.minimal-footprint.candidate.md`,
> `architecture.shared-core.candidate.md`,
> `architecture.layered-modules.candidate.md`) into a single
> design. The Open Questions section captures every assumption the
> candidates flagged but did not fully resolve.

## Overview

The ERC-20 extension to the `eth-tx-builder` Claude Code skill ships as exactly
**one new helper file** (`build_erc20.py`), **one new test file**
(`test_build_erc20.py`), and **two prose-only edits** to `SKILL.md` and
`README.md`. The v1 ETH-send path (`build_send_eth.py` +
`test_build_send_eth.py`) stays **bit-for-bit unchanged** — the v1 regression
suite is the verification.

DRY reuse is achieved via a **read-only import edge**: `build_erc20.py` does
`import build_send_eth as _core` and consumes the small, named symbol surface
already exposed at v1 module level (`NETWORKS`, `network_config`, `rpc_call`,
`validate_hex_address`, `parse_hex_int`, `compute_max_fee`, `fetch_nonce`,
`fetch_base_fee`, `fetch_tip`, `RPCError`). No v1 file is edited; no third
module (`_common.py` / `_tx_core.py`) is extracted; the v1 file itself plays the
dual role of CLI + de facto shared library.

Inside `build_erc20.py`, code is organized into **seven labeled in-file
sections** that form a strict downward dependency graph (Layer 1 → 4). The
sections map 1:1 onto the seven distinct change axes the PRD already
identifies (selectors, amount format, contract reads, gas policy, summary
wording, op composition, CLI dispatch). Functions are pure-given-an-injected
`rpc` callable — matching v1's house style exactly — and the
fatal-vs-best-effort policy from research overview §2 is enforced
**structurally** by return-type discipline (`Optional[str]` for the one
enrichment read, exceptions everywhere else) rather than convention. The
`eth_estimateGas` no-fallback policy is also structural: there is no
`try/except` around `estimate_gas` in any intermediate layer, so a future
maintainer adding a silent fallback has to *insert* one — a visible code-review
flag.

The architecture leaves a one-line migration seam for the future. When (and
only when) a third sibling helper lands (`build_erc721.py`, say) or the
read-only v1 constraint is relaxed, the `_core` alias target moves from
`build_send_eth` to a freshly-extracted `_tx_core.py` and both consumers
recompile unchanged.

## Architecture Principles

- **Bit-for-bit v1 preservation** — `build_send_eth.py` and
  `test_build_send_eth.py` MUST NOT change. This is a HARD constraint from PRD
  P0 §17. The architecture is shaped around it: no v1 edits, no extraction out
  of v1, no monkey-patching. The v1 regression suite is the proof.
- **One-way import edge** — `build_erc20.py` imports `build_send_eth as
  _core`; the reverse never happens. The graph is a strict two-node DAG with
  one edge. The single underscore-prefixed alias signals "library, not for
  re-export" and makes every cross-module call grep-able as `_core.`.
- **DRY without extraction** — every line of fee / RPC / network / address-
  validation / `RPCError` logic in `build_erc20.py` goes through an imported
  symbol from `_core`. There is no duplication of v1 plumbing in v1's
  timeframe; the only "shared core" is the v1 file itself.
- **Strict downward layering inside `build_erc20.py`** — seven labeled
  sections (Layer 1: `abi_codec`, `amount_codec`; Layer 2: `contract_reads`,
  `gas_estimator`, `summary`; Layer 3: `tx_assembly`; Layer 4: `cli_dispatch`).
  Each layer imports only layers numbered below it; no peer imports within a
  layer; only Layer 3 fans out across Layer 2. The file reads top-down in
  dependency order so a single read mirrors the graph.
- **Pure functions with injected `rpc`** — every chain-touching function takes
  `rpc=_core.rpc_call` as a kwarg default. Tests pass a stub
  (`unittest.mock.Mock`). No globals, no I/O at import, no module-level
  network state. Matches v1 verbatim.
- **`do_*` builders return `(tx, ctx, warnings)`; never print** — printing is
  the CLI dispatcher's job. This keeps `do_transfer` / `do_approve` /
  `do_transfer_from` deterministic and trivially unit-testable on dict
  equality. Matches v1's `build_tx_request` shape exactly.
- **Error-and-stop for correctness reads; warn-don't-block for enrichment
  reads** — reconciled per research overview §2. Reads whose values flow into
  calldata or gas (`decimals`, `eth_estimateGas`) are fatal; reads that only
  enrich the human summary (`symbol`, `allowance` soft-check, future
  `balanceOf` pre-check) degrade gracefully. **Structurally enforced** at the
  type level: `decode_symbol` returns `Optional[str]`; everything else raises.
- **No-fallback on `eth_estimateGas` is enforced by absence of try/except** —
  no intermediate layer catches `RPCError` from `estimate_gas`. The only
  `try/except` in the entire estimate path is `cli_dispatch.main()`, and it
  exits 1 — it does not construct a tx. The absence of intermediate handlers
  is a load-bearing design fact; an in-code comment in `gas_estimator` flags
  this for future maintainers.
- **Integer-only amount conversion** — `human_to_base_units` is `str → str →
  int`. No `float()`, no `decimal.Decimal`, no `fractions.Fraction`. Tests
  assert both positively (golden vectors) and negatively (`inspect.getsource`
  scan for the substring `"float("` in the conversion function body).
- **Stdlib only** — `argparse`, `json`, `re`, `sys`, `urllib.request`
  (transitively via `_core.rpc_call`). Same set v1 uses; no new deps. Selectors
  are hardcoded constants with derivation comments; no runtime Keccak.
- **Stdout = JSON only; stderr = humans only** — operators can pipe stdout
  into the signer or `jq` cleanly. Three stderr conventions: bare text for the
  summary block, `WARNING:` prefix for non-fatal soft-checks and
  `--approve-max`, `error:` prefix (lowercase, matches v1) for fatal exit-1
  paths.

## System Context Diagram

```text
                ┌──────────────────────────────────┐
                │ Operator (Claude Code skill user)│
                └────────────────┬─────────────────┘
                                 │ shell invocation
                                 ▼
   ┌──────────────────────────────────────────────────────────────────┐
   │  .claude/skills/eth-tx-builder/build_erc20.py                    │
   │  (this architecture; 7 layered in-file sections)                 │
   └────┬──────────────────────────┬───────────────────────┬──────────┘
        │ import (read-only):      │ stdout = JSON         │ stderr = summary
        │ NETWORKS, network_config,│   (one print at       │   + WARNING:s
        │ rpc_call, validate_hex_  │    end, exactly       │   + error:s
        │ address, parse_hex_int,  │    one shape)         │
        │ compute_max_fee,         │                       │
        │ fetch_nonce/base_fee/tip,│                       │
        │ RPCError                 │                       │
        ▼                          ▼                       ▼
   ┌──────────────────────────┐               ┌──────────────────────────┐
   │ build_send_eth.py (v1,   │               │ eth-signer-mcp           │
   │ READ-ONLY)               │               │ sign_transaction         │
   │ - own CLI for ETH send   │               │ (offline, separate proc; │
   │ - dual role: importable  │               │  operator pastes JSON in)│
   │   library for ERC-20     │               └──────────────────────────┘
   │ - test_build_send_eth.py │
   │   stays unchanged        │
   └────────────┬─────────────┘
                │ HTTPS JSON-RPC (urllib.request, 15s timeout, no retry)
                ▼
   ┌──────────────────────────────────────────────────────────────┐
   │ Public RPC endpoints (publicnode.com mainnet / hoodi)        │
   │ Methods used: eth_call (decimals/symbol/allowance),          │
   │ eth_estimateGas, eth_getTransactionCount, eth_getBlockByNumber,│
   │ eth_maxPriorityFeePerGas                                     │
   └──────────────────────────────────────────────────────────────┘
```

External dependencies: the public RPC endpoints from
`build_send_eth.NETWORKS`, and the downstream offline signer
(`eth-signer-mcp`) the operator pastes the JSON into. Both helpers run as
**separate Python processes** — the SKILL.md "router" prose tells the Claude
Code agent which one to invoke.

## Module Overview

| Module | Layer | Responsibility | Owns Data | Depends On | Communication |
|---|---:|---|---|---|---|
| `build_send_eth.py` (v1, **read-only**) | n/a | Build EIP-1559 ETH-send TxRequest; dual role as the de facto shared library for `build_erc20.py` | `NETWORKS` constant; `RPCError` class; `USER_AGENT`, `DEFAULT_TIP_WEI` | publicnode RPC (transitively) | sync (function calls); outbound RPC |
| `build_erc20.py` (NEW) | n/a (one file, seven sections) | Build EIP-1559 ERC-20 `transfer`/`approve`/`transferFrom` TxRequest | None (constants only) | `_core` (`build_send_eth`), publicnode RPC | sync (function calls); outbound RPC |
| └ `abi_codec` (in-file section) | 1 (leaf) | Selector constants + 32-byte ABI word encode/decode for `decimals`/`symbol`/`allowance` returns | ERC-20 ABI knowledge | stdlib only | sync function calls |
| └ `amount_codec` (in-file section) | 1 (leaf) | Human decimal-string ↔ base-unit integer; `MAX_UINT256` constant | Amount arithmetic with no float | stdlib only | sync function calls |
| └ `contract_reads` (in-file section) | 2 | `fetch_decimals` / `fetch_symbol` / `fetch_allowance` over injected `rpc` | What an `eth_call` to the token says today | `abi_codec`, `_core.RPCError` | sync + `rpc` injection |
| └ `gas_estimator` (in-file section) | 2 | `estimate_gas` with `from`-populated call object, +20% buffer, 300k cap, no fallback | The gas number for the final tx | stdlib + `_core.RPCError` | sync + `rpc` injection |
| └ `summary` (in-file section) | 2 | Render the stderr summary text + emit all `WARNING:` lines | Summary wording, field order | `amount_codec` (for base→human render) | pure str → stderr write |
| └ `tx_assembly` (in-file section) | 3 | `do_transfer` / `do_approve` / `do_transfer_from` — compose calldata, reads, gas, v1 fees into TxRequest | The op-level business logic | `abi_codec`, `amount_codec`, `contract_reads`, `gas_estimator`, `_core` fee helpers | sync function calls |
| └ `cli_dispatch` (in-file section) | 4 (top) | argparse subparsers + address validation + dispatch + print JSON | CLI shape (the public contract) | `tx_assembly`, `summary`, `_core.validate_hex_address` | argparse + stdout/stderr |
| `test_build_send_eth.py` (v1, **read-only**) | n/a | v1 regression suite | — | `build_send_eth` (in-process import) | unit tests |
| `test_build_erc20.py` (NEW) | n/a | Unit tests; one `TestCase` class per Layer 1–4 section, plus integration tests at `TestTxAssembly` and `TestCliDispatch` | — | `build_erc20` (and transitively `_core`) | unit tests |
| `SKILL.md` (edited; prose only) | n/a | Router doc: tell the agent which helper to invoke | — | both helpers (named in prose) | docs |
| `README.md` (edited; prose only) | n/a | File list + manual e2e (hoodi) | — | both helpers (named in prose) | docs |

The seven in-file sections of `build_erc20.py` are logical modules, not
separate Python files. Each is bracketed by `# === Layer N: <name>
=================` banners; the file reads top-down in dependency order; the
test file mirrors the section layout (one `TestCase` per section, plus the
two composition layers for cross-section tests).

## Module Dependency Graph

```text
                                       ┌────────────────────┐
                                       │   cli_dispatch     │   Layer 4 (top)
                                       └─────────┬──────────┘
                                                 │
                       ┌─────────────────────────┼──────────────────────────┐
                       ▼                         ▼                          ▼
              ┌──────────────┐           ┌──────────────┐         ┌──────────────────┐
              │ tx_assembly  │           │   summary    │         │ _core.validate_  │
              │  (Layer 3)   │           │  (Layer 2)   │         │ hex_address      │
              └──┬───┬───┬───┘           └──────┬───────┘         └──────────────────┘
                 │   │   │                      │
                 │   │   │                      ▼
                 │   │   │              ┌──────────────┐
                 │   │   │              │ amount_codec │   Layer 1 (leaf)
                 │   │   │              └──────────────┘
                 │   │   │
        ┌────────┘   │   └─────────┐
        ▼            ▼             ▼
┌──────────────┐ ┌──────────────┐ ┌──────────────────────┐
│ contract_    │ │ gas_         │ │ _core.fetch_nonce /  │
│ reads        │ │ estimator    │ │  fetch_base_fee /    │
│  (Layer 2)   │ │  (Layer 2)   │ │  fetch_tip /         │
└──────┬───────┘ └──────────────┘ │  compute_max_fee /   │
       │              ▲           │  network_config      │
       ▼              │           └──────────────────────┘
┌──────────────┐      │
│  abi_codec   │      │ (also depends on _core.RPCError)
│  (Layer 1)   │      │
└──────────────┘      │
                      │
                (_core.RPCError;
                 _core.rpc_call as
                 default for `rpc`)


External-edge graph (CLI/test/process boundary):

test_build_send_eth.py ──▶ build_send_eth.py     (v1 path, unchanged)

test_build_erc20.py    ──▶ build_erc20.py ──▶ build_send_eth.py (as _core)
                                          ──▶ publicnode RPC (via _core.rpc_call)

SKILL.md  (prose) ──names──▶ build_send_eth.py
                  ──names──▶ build_erc20.py
```

Acyclicity verification:

- `build_send_eth.py` has zero imports of any skill-local module (stdlib only).
- `build_erc20.py` imports `build_send_eth` and stdlib; the reverse never
  happens.
- Inside `build_erc20.py`: Layer 1 (`abi_codec`, `amount_codec`) imports
  nothing internal; Layer 2 imports only Layer 1 (plus `_core`); Layer 3
  (`tx_assembly`) imports Layers 1–2 (plus `_core`); Layer 4 (`cli_dispatch`)
  imports Layer 3 (plus `summary` for warning emission and
  `_core.validate_hex_address` for address validation). No layer imports a
  peer; only Layer 3 fans out across Layer 2.
- Test isolation: `python3 -m unittest test_build_send_eth -v` does NOT load
  `build_erc20.py` (the v1 test file does not import it). This preserves the
  PRD success metric "No regression in ETH-send."

---

## Module Details

### Module: `build_send_eth.py` (v1, READ-ONLY)

**Responsibility:** Build the EIP-1559 ETH-send TxRequest (the original v1
job) AND, by virtue of being importable, serve as the shared-core library
that `build_erc20.py` reuses.

**Hard constraint (PRD P0 §17):** This file MUST NOT be edited as part of the
ERC-20 delta. Its bytes are unchanged; its test suite
(`test_build_send_eth.py`) is unchanged. Both continue to pass as the v1
regression check. Enforced operationally by (a) PRD P0 §17, (b) the CI step
`python3 -m unittest test_build_send_eth -v`, and (c) code review against the
pre-extension SHA of this file.

**Domain Entities (unchanged):** network config, RPC endpoint, EIP-1559 fee
fields, ETH-send TxRequest.

**Data Store:** None. Pure in-memory; one module-level dict (`NETWORKS`).

**Public surface consumed by `build_erc20.py` (the de facto contract):**

| Name | Kind | Used by `build_erc20.py` for |
|---|---|---|
| `NETWORKS` | dict constant | Read-only; subcommand `choices` re-uses `sorted(_core.NETWORKS)` |
| `RPCError` | exception class | Caught in `cli_dispatch.main()` around all `do_*` calls; structurally NOT caught inside `gas_estimator.estimate_gas` |
| `network_config(network)` | fn → (chain_id, url) | Resolving `--network` to (chain_id, RPC URL) inside `tx_assembly.do_*` |
| `validate_hex_address(addr)` | fn → addr | All address arg validation (`--token`, `--to`, `--spender`, `--from`, `--sender`) at the `cli_dispatch` layer |
| `parse_hex_int(s)` | fn → int | Decoding RPC hex responses (decimals word, allowance word, estimate result, etc.) inside `contract_reads` and `gas_estimator` |
| `compute_max_fee(base, tip)` | fn → int | Reused unchanged (`baseFee*2 + tip`) inside `tx_assembly` |
| `rpc_call(url, method, params, timeout=15)` | fn → result | The default `rpc` callable injected into `build_erc20`'s build functions and reads |
| `fetch_nonce(rpc, url, sender)` | fn → int | Inside `tx_assembly.do_*` |
| `fetch_base_fee(rpc, url)` | fn → int | Inside `tx_assembly.do_*` |
| `fetch_tip(rpc, url)` | fn → int | Inside `tx_assembly.do_*` (includes v1's 1-gwei fallback) |
| `USER_AGENT` | str constant | Not used directly; used transitively via `_core.rpc_call` |
| `DEFAULT_TIP_WEI` | int constant | Not used directly; used transitively via `_core.fetch_tip` |

This list IS the contract. `build_erc20.py`'s top-of-file docstring restates
it so a future v1 rename surfaces as an immediate import-time `AttributeError`
when `test_build_erc20.py` loads — caught by CI, attributable to the v1
change. (See Risks below.)

**Internal Structure:** Single-file Python module. Unchanged.

**Failure Modes:** Same as today. No change in behavior because no code in
this file changes.

---

### Module: `build_erc20.py` (NEW, P0)

**Responsibility:** Produce a ready-to-sign EIP-1559 TxRequest for any of the
three ERC-20 movement ops (`transfer`, `approve`, `transfer-from`) on
`mainnet` or `hoodi`, using human-readable amounts and a token contract
address, with no new Python dependencies and no float arithmetic on token
amounts.

**Domain Entities:**

- `TxRequest` — the JSON shape the `eth-signer-mcp` `sign_transaction` tool
  accepts (`type`, `chainId`, `nonce`, `to`, `value`, `data`, `gas`,
  `maxFeePerGas`, `maxPriorityFeePerGas`). Same shape v1 emits, with
  ERC-20-specific values in `to` (= token contract), `value` (= `"0"`), `data`
  (= ABI-encoded calldata), `gas` (= buffered + capped estimate).
- `Network` — `mainnet` or `hoodi`. Sourced from `_core.NETWORKS` (no
  duplication).
- `TokenMetadata` — in-memory record built from `decimals()` (fatal on
  failure) and `symbol()` (best-effort, `Optional[str]`). Not persisted.
- `Allowance` (transfer-from only) — soft-check result; integer base-unit
  uint256, or `None` if the read itself failed.

**Data Store:** None. Module-level constants only.

**Module-level constants (with derivation comments):**

```python
# === ERC-20 ABI selectors ============================================
# Selectors are keccak256(canonical_signature)[:4]. Hardcoded because the
# Python stdlib does not ship Keccak (hashlib.sha3_256 is NOT keccak;
# SHA-3 finalisation differs). Verified against the USDC mainnet test
# vectors in research/01-abi-encoding.

SEL_TRANSFER       = "0xa9059cbb"   # keccak256("transfer(address,uint256)")[:4]
SEL_APPROVE        = "0x095ea7b3"   # keccak256("approve(address,uint256)")[:4]
SEL_TRANSFER_FROM  = "0x23b872dd"   # keccak256("transferFrom(address,address,uint256)")[:4]
SEL_DECIMALS       = "0x313ce567"   # keccak256("decimals()")[:4]
SEL_SYMBOL         = "0x95d89b41"   # keccak256("symbol()")[:4]
SEL_ALLOWANCE      = "0xdd62ed3e"   # keccak256("allowance(address,address)")[:4]

# === amount / gas policy =============================================
MAX_UINT256        = (1 << 256) - 1     # for --approve-max
MAX_DECIMALS       = 36                 # research §1.4; rejects hostile values
GAS_BUFFER_NUM     = 12                 # PRD §9: +20% buffer as (est * 12) // 10
GAS_BUFFER_DEN     = 10
GAS_CAP            = 300_000            # PRD §9: hard ceiling
```

**Public CLI contract (consumed by operators and SKILL.md):**

| Subcommand | Required flags | Output (stdout) | Output (stderr) | Exit |
|---|---|---|---|---|
| `transfer` | `--network --token --to --amount --sender` | `TxRequest` JSON | summary | 0 |
| `approve` | `--network --token --spender --sender` + (`--amount` ⊕ `--approve-max`) | `TxRequest` JSON | summary + `WARNING:` on `--approve-max` | 0 |
| `transfer-from` | `--network --token --from --to --amount --sender` | `TxRequest` JSON | summary + `WARNING:` on low/missing allowance | 0 |
| any subcommand | bad input / fatal RPC error / estimate failure | (nothing) | `error: <msg>` | 1 |

Top-level `--help` lists all three subcommands; each subcommand has its own
`--help`. Subcommand name `transfer-from` is hyphenated at the CLI to match
PRD examples; the Python function is `do_transfer_from` (underscore).

**Internal Structure** (one file, seven labeled in-file sections, dependency
order top-to-bottom):

```text
build_erc20.py
├── #!/usr/bin/env python3 + module docstring
├── # The docstring lists the imported _core symbols (the contract):
│   #   NETWORKS, network_config, validate_hex_address, parse_hex_int,
│   #   compute_max_fee, rpc_call, fetch_nonce, fetch_base_fee, fetch_tip,
│   #   RPCError. If any of these disappear from build_send_eth, this
│   #   module fails at import.
├── import argparse, json, sys
├── import build_send_eth as _core          # the read-only library import
│
├── # === Layer 1: abi_codec ===========================================
├── SEL_* constants + MAX_DECIMALS
├── _encode_address(addr_hex)      -> 64-hex word, lowercase, left-pad 24 zeros
├── _encode_uint256(n)             -> 64-hex word; reject n < 0 or n >= 2**256
├── _pack_call(selector_hex, *args_hex)
├── encode_transfer(to, amount_base)
├── encode_approve(spender, amount_base)
├── encode_transfer_from(from_, to, amount_base)
├── encode_decimals_call()         -> SEL_DECIMALS
├── encode_symbol_call()           -> SEL_SYMBOL
├── encode_allowance_call(holder, spender)
├── decode_decimals(hex_result)    -> int 0..36; raises ValueError on > MAX_DECIMALS
├── decode_symbol(hex_result)      -> Optional[str]  (the asymmetric case)
├── decode_allowance(hex_result)   -> int
├── # === end Layer 1: abi_codec =======================================
│
├── # === Layer 1: amount_codec ========================================
├── MAX_UINT256
├── human_to_base_units(amount_str, decimals) -> int
│       # str -> str -> int, no float. Rejects negatives, multi-dot,
│       # non-digits, more frac digits than `decimals`.
├── base_units_to_human(amount, decimals)    -> str  (for summary render)
├── # === end Layer 1: amount_codec ====================================
│
├── # === Layer 2: contract_reads ======================================
├── fetch_decimals(rpc, url, token)             -> int  (RAISES on failure: FATAL)
├── fetch_symbol(rpc, url, token)               -> Optional[str]  (swallows; best-effort)
├── fetch_allowance(rpc, url, token, holder, spender) -> int  (propagates RPCError; soft-check is caller's job)
├── # === end Layer 2: contract_reads ==================================
│
├── # === Layer 2: gas_estimator =======================================
├── GAS_BUFFER_NUM, GAS_BUFFER_DEN, GAS_CAP
├── estimate_gas(rpc, url, sender, token, data) -> int
│       # Builds {from, to, data, value:"0x0"} against "latest".
│       # NO try/except. RPCError propagates by design.
│       # In-code multi-line comment explains why no fallback (ADR-007).
├── _apply_buffer_cap(est)                       -> int
│       # min((est * GAS_BUFFER_NUM) // GAS_BUFFER_DEN, GAS_CAP)
├── # === end Layer 2: gas_estimator ===================================
│
├── # === Layer 2: summary =============================================
├── render_summary(ctx)                          -> str  (pure)
├── print_summary(ctx)                           -> None (writes to stderr)
├── warn_approve_max(symbol, token, spender)     -> None
├── warn_low_allowance(holder, spender, current, requested, decimals) -> None
├── warn_allowance_check_skipped(reason)         -> None
├── warn_symbol_unavailable()                    -> None  (optional, info only)
├── emit_warning(kind, payload)                  -> None  (dispatcher)
├── # === end Layer 2: summary =========================================
│
├── # === Layer 3: tx_assembly =========================================
├── _build_eip1559_envelope(chain_id, nonce, to, data, gas, base_fee, tip)
│       # internal: assemble the v1-shape TxRequest dict
├── do_transfer(network, token, to, amount, sender, *, rpc=_core.rpc_call)
│       -> (tx_dict, summary_ctx, warnings_list)
├── do_approve(network, token, spender, amount, sender, *,
│              approve_max=False, rpc=_core.rpc_call)
│       -> (tx_dict, summary_ctx, warnings_list)
├── do_transfer_from(network, token, from_, to, amount, sender, *, rpc=_core.rpc_call)
│       -> (tx_dict, summary_ctx, warnings_list)
├── # === end Layer 3: tx_assembly =====================================
│
├── # === Layer 4: cli_dispatch ========================================
├── _build_parser()                              -> argparse.ArgumentParser
├── _validate_addresses(args)                    -> None  (raises ValueError)
├── main(argv=None)                              -> int
│       # try:
│       #   parse args; validate addresses; dispatch to do_*;
│       #   for w in warnings: summary.emit_warning(w)
│       #   summary.print_summary(ctx)
│       #   print(json.dumps(tx, indent=2))
│       #   return 0
│       # except (ValueError, _core.RPCError) as e:
│       #   print("error: %s" % e, file=sys.stderr)
│       #   return 1
├── if __name__ == "__main__": sys.exit(main())
├── # === end Layer 4: cli_dispatch ====================================
```

**`tx_assembly.do_*` skeleton** (all three ops follow this pattern):

1. `chain_id, url = _core.network_config(network)`.
2. (Address validation already happened in `cli_dispatch._validate_addresses`.)
3. `decimals = contract_reads.fetch_decimals(rpc, url, token)`  — raises on
   RPC failure or `> MAX_DECIMALS`. **FATAL.**
4. `symbol = contract_reads.fetch_symbol(rpc, url, token)` — returns `None`
   on any failure. **Best-effort.**
5. Resolve `amount_base`:
   - `do_approve` with `approve_max=True` → `amount_base = MAX_UINT256`;
     queue `warn_approve_max`.
   - Otherwise → `amount_base = amount_codec.human_to_base_units(amount, decimals)`.
6. Build `calldata` via the matching `abi_codec.encode_*`.
7. (For `do_transfer_from` only) `try: allowance = fetch_allowance(...)`
   - On `RPCError` → queue `warn_allowance_check_skipped`.
   - On `allowance < amount_base` → queue `warn_low_allowance`.
   - Otherwise → no warning.
   This is the ONE try/except for `RPCError` outside `cli_dispatch.main()`;
   it is local to the soft-check and does not extend to `estimate_gas`.
8. `gas = gas_estimator.estimate_gas(rpc, url, sender, token, calldata)` —
   propagates `RPCError` on failure. **FATAL. No fallback. No try/except.**
9. `nonce = _core.fetch_nonce(rpc, url, sender)`,
   `base_fee = _core.fetch_base_fee(rpc, url)`,
   `tip = _core.fetch_tip(rpc, url)`,
   `max_fee = _core.compute_max_fee(base_fee, tip)`.
10. Return `(tx_dict, summary_ctx, warnings_list)`.

The returned `tx_dict` matches the v1 TxRequest shape exactly:

```python
{
    "type": "eip1559",
    "chainId": str(chain_id),
    "nonce": str(nonce),
    "to": token,             # the token contract, NOT the recipient
    "value": "0",            # no ETH
    "data": calldata,        # ABI-encoded
    "gas": str(gas),
    "maxFeePerGas": str(max_fee),
    "maxPriorityFeePerGas": str(tip),
}
```

`summary_ctx` is a dict with stable keys
(`operation`, `network`, `chain_id`, `token`, `symbol`, `decimals`,
`human_amount`, `base_amount`, `is_max_uint`, role-specific addresses per
op, `nonce`, `gas`, `max_fee`, `max_priority_fee`).

`warnings_list` is a list of `(kind, payload_dict)` tuples consumed by
`summary.emit_warning`. Warnings are data, not output — keeping them
serializable means snapshot tests can pin them exactly.

**Key Design Decisions** (full ADRs below):
- **ADR-001:** Reuse v1 plumbing by `import build_send_eth as _core`; no
  duplication, no extraction.
- **ADR-002:** Seven labeled in-file sections in a strict downward DAG
  (Layer 1–4). No peer imports within a layer; only Layer 3 fans out across
  Layer 2.
- **ADR-003:** Pure functions with injected `rpc` (`rpc=_core.rpc_call`
  kwarg default).
- **ADR-004:** `do_*` returns `(tx_dict, summary_ctx, warnings_list)`. CLI
  driver prints; `do_*` never does.
- **ADR-005:** Hardcoded selectors; no runtime Keccak. Module-level
  constants with derivation comments.
- **ADR-006:** Structural fatal-vs-best-effort split — `decode_symbol`
  returns `Optional[str]`; everything else raises. Enforced at the type
  level, not by convention.
- **ADR-007:** `eth_estimateGas` failure is fatal; **no fallback, ever**.
  Enforced structurally by absence of `try/except` around `estimate_gas` in
  any intermediate layer. The only `RPCError` catcher is
  `cli_dispatch.main()`, which exits 1.
- **ADR-008:** Integer-only token amount conversion; no `float()` /
  `Decimal` / `Fraction`. Test asserts both golden vectors and (via
  `inspect.getsource`) absence of the substring `"float("` in the
  conversion function body.
- **ADR-009:** Stdout = JSON only; stderr = summary + warnings + errors.
  `error:` (lowercase) for fatal exit 1 (matches v1); `WARNING:` (allcaps)
  for soft warnings.
- **ADR-010:** Address validation happens once, at the CLI layer
  (`cli_dispatch._validate_addresses` using `_core.validate_hex_address`).
  `do_*` assumes already-validated hex.
- **ADR-011:** Test file mirrors the seven-section layout (one
  `TestCase` class per section), grep-able and runnable in isolation via
  `python3 -m unittest test_build_erc20.TestAbiCodec` etc.

**Failure Modes:**

| Trigger | Layer that detects | Behavior |
|---|---|---|
| Bad CLI input (malformed address, mutual exclusion violation, multiple decimal points, negative amount, more frac digits than decimals) | `cli_dispatch._validate_addresses` or `amount_codec.human_to_base_units` raises `ValueError` | `error: <msg>` to stderr; exit 1; no JSON; no warnings emitted |
| `decimals()` RPC failure | `contract_reads.fetch_decimals` propagates `RPCError` | `error: <msg>` to stderr; exit 1; no JSON. **FATAL.** |
| `decimals()` returns value > `MAX_DECIMALS` (36) | `abi_codec.decode_decimals` raises `ValueError` | `error: token decimals() returned suspicious value ...`; exit 1; no JSON. **FATAL.** |
| `symbol()` RPC failure or decode failure | `contract_reads.fetch_symbol` swallows; returns `None` | Summary shows `(unavailable)`; build continues. **Best-effort.** |
| `allowance()` RPC failure on `transfer-from` | `tx_assembly.do_transfer_from` catches `RPCError` around `fetch_allowance` only | Queues `warn_allowance_check_skipped`; build continues; JSON still emitted. **Soft.** |
| `allowance()` returns value < requested on `transfer-from` | `tx_assembly.do_transfer_from` queues `warn_low_allowance` | Warning to stderr; JSON still emitted. **Soft.** |
| `eth_estimateGas` failure (revert, transport, rate-limit) | `gas_estimator.estimate_gas` does NOT catch; `RPCError` propagates | `error: eth_estimateGas failed: <node msg>` from `cli_dispatch.main()`; exit 1; no JSON. **FATAL — no fallback.** |
| `--approve-max` selected | `cli_dispatch` sets `approve_max=True` | `tx_assembly.do_approve` queues `warn_approve_max`; multi-line warning to stderr; JSON still emitted |
| Unknown network | `_core.network_config(network)` raises `ValueError` | `error: <msg>`; exit 1 |
| publicnode outage / rate-limit on any read | The current call's RPCError propagates | `error: <msg>`; exit 1; operator retries (no auto-retry by design; PRD Out-of-Scope) |
| v1 file rename / symbol removal | `import build_send_eth as _core` raises `ImportError`/`AttributeError` at module-load time | Tests fail at import; CI catches; cross-skill attribution is clear |
| Process killed mid-build | No state persisted | Rerun is idempotent; the helper never partially-emits a TxRequest (the JSON is a single `print()` at the end of `cli_dispatch.main()`) |

**Reliability / SLO posture:** none claimed. The helper is a one-shot CLI;
if it fails the operator re-runs. Six to seven sequential RPC reads, each
bounded by the inherited 15-second timeout of `_core.rpc_call`. No retry, no
parallelism, no caching.

---

### Module: `test_build_send_eth.py` (v1, READ-ONLY)

**Responsibility:** v1 regression suite. Must continue to pass byte-for-byte
unchanged. The success criterion is: after the ERC-20 delta lands,
`python3 -m unittest test_build_send_eth -v` still passes.

Does NOT import `build_erc20`. The v1 test surface is hermetic.

---

### Module: `test_build_erc20.py` (NEW)

**Responsibility:** Unit + integration regression coverage for
`build_erc20.py`. Test classes mirror the seven-section layout for grep-able,
runnable-in-isolation tests.

**Internal Structure (one `TestCase` per section):**

```text
test_build_erc20.py
├── import unittest, json
├── import unittest.mock as mock
├── import build_erc20 as b
│
├── class TestAbiCodec(unittest.TestCase):
│       # Selector constants match the canonical hex strings (research §01)
│       # _encode_address: mixed-case input → lowercase output + 24-zero left-pad
│       # _encode_uint256: 0, 1, 2**256-1, reject -1, reject 2**256
│       # _pack_call: USDC transfer calldata bit-equality vs research §01 vector
│       # encode_transfer / encode_approve / encode_transfer_from: bit-pattern
│       # encode_allowance_call: bit-pattern; encode_decimals_call/symbol_call: equality
│       # decode_decimals: 0, 6, 18, 24 OK; 37 raises ValueError
│       # decode_symbol: standard ABI string OK; bytes32 fallback (MKR-style) OK;
│       #   malformed → None (not a raise)
│       # decode_allowance: 0, max-uint
│
├── class TestAmountCodec(unittest.TestCase):
│       # human_to_base_units golden vectors:
│       #   ("0", 6) → 0; ("0.0", 6) → 0; ("1", 18) → 10**18; ("1.5", 6) → 1500000;
│       #   ("0.000001", 18) → 10**12; ("1000000.5", 6) → 1_000_000_500_000
│       # rejections: "", "-1", "1..5", "1.5.0", "abc", "1.0000001" (decimals=6)
│       # base_units_to_human round-trip on the same vectors
│       # MAX_UINT256 == (1 << 256) - 1
│       # Negative assertion: inspect.getsource(human_to_base_units) does NOT
│       #   contain the substring "float(" (ADR-008)
│
├── class TestContractReads(unittest.TestCase):
│       # fetch_decimals: mock rpc returns "0x...06" → 6; rpc raises → propagates
│       # fetch_symbol: mock rpc returns USDC bytes → "USDC"; rpc raises → None;
│       #   decoder returns None → None
│       # fetch_allowance: mock rpc returns "0x...0a" → 10; rpc raises → propagates
│
├── class TestGasEstimator(unittest.TestCase):
│       # estimate_gas: mock rpc returns "0xfe1f" (65055) → 78066 buffered
│       # estimate_gas: mock rpc returns "0x3d090" (250000) → 300000 capped
│       # estimate_gas: mock rpc raises RPCError → propagates (NO catch)
│       # _apply_buffer_cap: 0 → 0; 1 → 1; 250_000 → 300_000; 1_000_000 → 300_000
│
├── class TestSummary(unittest.TestCase):
│       # render_summary returns text that contains the expected labels
│       #   ("token", "decimals", "amount (base units)", "spender", ...)
│       # warn_approve_max prints the multi-line warning to stderr
│       # warn_low_allowance + warn_allowance_check_skipped emit "WARNING:" lines
│
├── class TestTxAssembly(unittest.TestCase):
│       # do_transfer happy path with mocked rpc:
│       #   - asserts the tx_dict shape (matches v1 keys + types)
│       #   - asserts summary_ctx keys
│       #   - asserts warnings == []
│       # do_approve happy path; do_approve with approve_max=True:
│       #   - amount word in calldata is all-Fs (MAX_UINT256)
│       #   - warnings_list contains ("approve_max", {...})
│       # do_transfer_from happy path; do_transfer_from with low allowance:
│       #   - warnings_list contains ("low_allowance", {...}); JSON still built
│       # do_transfer_from with fetch_allowance raising RPCError:
│       #   - warnings_list contains ("allowance_check_skipped", {...}); JSON still built
│       # do_*: when fetch_decimals raises, RPCError propagates (no JSON)
│       # do_*: when estimate_gas raises, RPCError propagates (no JSON; FATAL)
│
├── class TestCliDispatch(unittest.TestCase):
│       # Top-level --help lists all 3 subcommands (argparse smoke)
│       # transfer --help: required args present
│       # approve --help: --amount XOR --approve-max enforced (argparse rejects both)
│       # transfer-from --help: --from + --to + --amount required
│       # main() bad address → exit 1, error: to stderr, no JSON
│       # main() happy path → exit 0, JSON to stdout, summary to stderr
│       # main() RPCError from estimate_gas → exit 1, no JSON on stdout (the
│       #   no-fallback regression check)
│       # main() with --approve-max → exit 0, WARNING: present, JSON on stdout
│       # main() with low allowance on transfer-from → exit 0, WARNING:, JSON
│
└── if __name__ == "__main__": unittest.main()
```

**Test dependencies:** `import build_erc20 as b` only (transitively pulls in
`build_send_eth` as `_core`). Does NOT import `build_send_eth` directly. Uses
`unittest.mock.Mock()` for the injected `rpc` callable, matching v1's
posture exactly.

**Test isolation:** `make_fake_rpc` test helper is duplicated from
`test_build_send_eth.py` with a one-line comment explaining the duplication
is mandatory because the v1 test file is read-only. ~12 lines, acceptable.

---

### Module: `SKILL.md` (edited; prose only)

**Responsibility:** Tell the Claude Code agent (a) the inputs each operation
needs and (b) which Python helper to invoke.

**Edits:**

- Description string broadened: `"...build an Ethereum transaction (native
  ETH transfer OR ERC-20 transfer/approve/transferFrom)..."`
- "Inputs" section split into two subsections:
  - "Inputs — native ETH send" (existing content, unchanged).
  - "Inputs — ERC-20 transfer / approve / transferFrom" (new): token
    address; `--to` (transfer / transfer-from), `--spender` (approve),
    `--from` (transfer-from); amount (human-readable) or `--approve-max`.
- "Procedure" gains a top routing step:
  1. Identify intent: native ETH transfer → use `build_send_eth.py`
     (existing procedure unchanged).
  2. Identify intent: ERC-20 transfer / approve / transfer-from → use
     `build_erc20.py` with the chosen subcommand.
- "Out of scope (v1)" updated: ERC-20 removed; explicit list of new
  non-goals (permit, ERC-721/1155, swaps, multi-token batch,
  fee-on-transfer / rebasing handling, gasless meta-tx, signing,
  broadcasting).

**No runtime contract change for the existing ETH path:** the Claude Code
agent reaching the "send ETH" branch still calls `build_send_eth.py` with
the exact arguments it does today.

---

### Module: `README.md` (edited; prose only)

**Responsibility:** File-list orientation + manual end-to-end checklist for
operators.

**Edits:**

- File list adds `build_erc20.py` and `test_build_erc20.py` rows.
- New "Manual end-to-end (hoodi)" section: three runs (`transfer`,
  `approve`, `transfer-from`) against a real ERC-20 deployed on hoodi,
  with a paste-to-signer step at the end.
- Test invocation snippet updated to run both v1 and ERC-20 test files.

---

## Cross-Cutting Concerns

### Authentication & Authorization

None at the helper layer. The helper does not hold keys, does not sign,
does not broadcast. Identity (the sending account) is supplied as the
`--sender` CLI arg. The signer (`eth-signer-mcp`) is a separate offline
process that holds keys and performs signing downstream. The two concerns
stay separate.

### Logging & Observability

- **Stdout = TxRequest JSON only.** Operators may pipe stdout into the
  signer; nothing else must appear there. A test asserts stdout contains
  only valid JSON on the happy path.
- **Stderr = humans only.** Three prefix conventions, applied consistently:
  - `error: <msg>` — fatal, exit 1, no JSON emitted (matches v1 lowercase).
  - `WARNING: <msg>` — soft, exit 0, JSON still emitted. Used for
    `--approve-max`, low allowance, allowance RPC skipped, symbol
    unavailable.
  - (bare text, no prefix) — the summary block.
- **No structured logging, no log levels, no third-party logger.** This is
  a one-shot CLI; stderr text is the entire observability surface. Matches
  v1.
- **No metrics, no traces, no error reporting service.** Out of scope for
  a stdlib-only single-file helper.

### Error Handling

- **Two exception classes only:** `ValueError` (input / encoding /
  validation) and `_core.RPCError` (transport / JSON-RPC failures, reused
  from v1).
- **The CLI's top-level `try/except (ValueError, _core.RPCError)` is the
  ONLY place exceptions become exit codes.** Lower layers raise; the CLI
  catches. Pattern:

  ```python
  try:
      tx, ctx, warns = dispatch(args)
  except (ValueError, _core.RPCError) as e:
      print("error: %s" % e, file=sys.stderr)
      return 1
  ```

- **The error-and-stop vs warn-and-continue split is enforced
  structurally** by which exception each layer catches:
  - `contract_reads.fetch_symbol` catches and returns `None` → swallows.
  - `tx_assembly.do_transfer_from` catches `RPCError` only around
    `fetch_allowance` (the designated soft-check); everything else
    propagates.
  - No other layer catches `RPCError`.
- **The `eth_estimateGas` no-fallback policy is documented as a load-bearing
  fact:** an in-code multi-line comment in `gas_estimator.estimate_gas`
  explains why a future maintainer adding a silent fallback would burn the
  full gas budget on revert. ADR-007 makes this a tracked architectural
  decision so any change requires updating the ADR.

### Configuration

- **All configuration is CLI flags.** No env vars, no config files, no
  feature flags. Matches v1 exactly.
- **Networks** (`mainnet` / `hoodi`) and their RPC URLs come from
  `_core.NETWORKS`. PRD P1 plans to add `sepolia` / `holesky`; the
  recommended path is to edit `build_send_eth.NETWORKS` directly in the P1
  phase (the read-only constraint applies to P0 delivery, not indefinitely
  — see Open Questions). `build_erc20.py` picks up the new entries for
  free via the imported `_core.NETWORKS`.
- **No `--rpc-url` override** in v1 (PRD Out-of-Scope; deferred to P2 if
  needed).

### Code Reuse Strategy (the central cross-cutting decision)

The PRD's Technical Considerations explicitly leaves the import-vs-duplicate
question to the architecture stage. This architecture **chooses import**:

1. **DRY.** Single source of truth for `NETWORKS`, fee strategy, RPC
   plumbing, address validation, hex parsing, `RPCError`. Drift is
   structurally prevented by absence of duplicated code.
2. **Bit-for-bit compatibility.** Every imported symbol is already
   module-level in v1; no edit to `build_send_eth.py` is required.
3. **One-way edge, clear contract.** `build_erc20.py` does
   `import build_send_eth as _core`; the leading underscore signals
   "library, not for re-export" and makes every cross-module call grep-able
   as `_core.`.
4. **Failure attribution.** If a future v1 maintainer renames or removes a
   symbol, `import build_send_eth as _core` (or a subsequent
   `_core.<name>` access) raises at module-load time inside
   `build_erc20.py`'s test run — caught by CI, clearly attributable to the
   v1 change.
5. **Cheap migration seam.** When (and only when) a third sibling helper
   lands or the v1 read-only constraint is relaxed, the `_core` alias
   target moves from `build_send_eth` to a freshly-extracted `_tx_core.py`.
   One-line change in `build_erc20.py`; v1's CLI keeps working unchanged
   because `build_send_eth.py` itself just becomes a thin
   `_tx_core`-importing CLI.

The alternatives (duplicate v1 helpers in `build_erc20.py`; extract a
`_common.py` now) are explicitly rejected:

- **Duplicate** would introduce ~70 lines of drift-prone code with the only
  control being a `DRIFT NOTE` comment. Comments are routinely ignored.
- **Extract `_common.py` now** would require editing `build_send_eth.py` to
  consume the shared module — directly violating PRD P0 §17.

The single accepted cost is that `build_send_eth.py` now has a dual role
(CLI + library) and its public symbol set is load-bearing for
`build_erc20.py`. Mitigated by the top-of-file docstring in
`build_erc20.py` that lists the imported names and by tests that exercise
the import edge.

---

## Data Flow Diagrams

### Flow 1 — `transfer` (happy path)

```text
Operator
  │ python3 build_erc20.py transfer --network mainnet
  │   --token 0xUSDC --to 0xRecipient --amount 1.5 --sender 0xMe
  ▼
cli_dispatch.main()
  │ argparse → args
  │ _validate_addresses(args)  →  _core.validate_hex_address × 3 (token, to, sender)
  ▼
tx_assembly.do_transfer
  │
  ├──▶ _core.network_config("mainnet") → (1, "https://ethereum-rpc.publicnode.com")
  │
  ├──▶ contract_reads.fetch_decimals(rpc, url, token)
  │       ├──▶ abi_codec.encode_decimals_call() → "0x313ce567"
  │       ├──▶ rpc("eth_call", [{to:token, data:"0x313ce567"}, "latest"]) → "0x000...06"
  │       └──▶ abi_codec.decode_decimals("0x000...06") → 6
  │
  ├──▶ contract_reads.fetch_symbol(rpc, url, token) → "USDC"  (or None on failure)
  │
  ├──▶ amount_codec.human_to_base_units("1.5", 6) → 1_500_000
  │
  ├──▶ abi_codec.encode_transfer(to=0xRecipient, amount_base=1_500_000) → calldata
  │
  ├──▶ gas_estimator.estimate_gas(rpc, url, sender, token, calldata)
  │       ├──▶ rpc("eth_estimateGas", [{from:sender, to:token, data, value:"0x0"}, "latest"]) → "0xfe1f"
  │       ├──▶ _apply_buffer_cap(65055) → (65055 * 12) // 10 = 78066 → min(78066, 300_000) = 78066
  │       │   (NO try/except. If rpc raises, RPCError propagates; do_* does NOT catch.)
  │
  ├──▶ _core.fetch_nonce / _core.fetch_base_fee / _core.fetch_tip
  ├──▶ _core.compute_max_fee(base_fee, tip)
  │
  └──▶ return (tx_dict, summary_ctx, warnings=[])
  │
  ▼
cli_dispatch.main()
  │ for w in warnings: summary.emit_warning(w)   # none here
  │ summary.print_summary(ctx)                   → stderr
  │ print(json.dumps(tx, indent=2))              → stdout
  └ return 0
```

### Flow 2 — `approve --approve-max` (warning path, still succeeds)

```text
Operator
  │ python3 build_erc20.py approve --network mainnet --token 0xUSDC
  │   --spender 0xRouter --approve-max --sender 0xMe
  ▼
cli_dispatch.main() (argparse: --amount XOR --approve-max group; approve_max=True)
  ▼
tx_assembly.do_approve(..., approve_max=True)
  │
  ├──▶ network_config + fetch_decimals + fetch_symbol  (same as Flow 1)
  │
  ├──▶ amount_base = MAX_UINT256                       (skip human_to_base_units)
  │
  ├──▶ abi_codec.encode_approve(spender, MAX_UINT256)  → calldata with all-Fs amount word
  │
  ├──▶ gas_estimator.estimate_gas(...)                 → buffered + capped (FATAL on fail)
  │
  ├──▶ _core fees + nonce
  │
  └──▶ warnings.append(("approve_max", {symbol, token, spender}))
  │
  ▼
cli_dispatch.main()
  │ for w in warnings: summary.emit_warning(w)
  │     └──▶ summary.warn_approve_max(symbol, token, spender) → stderr multi-line warn
  │              "WARNING: --approve-max grants UNLIMITED transfer authority on
  │               <SYM> (<token-addr>) to spender <spender-addr>.
  │               Revoke later with approve(spender, 0) if no longer needed."
  │ summary.print_summary(ctx with is_max_uint=True, base_amount="MAX UINT256") → stderr
  │ print(json.dumps(tx, indent=2)) → stdout
  └ return 0
```

### Flow 3 — `transfer-from` with low or missing allowance

```text
Operator
  │ python3 build_erc20.py transfer-from --network mainnet --token 0xUSDC
  │   --from 0xHolder --to 0xDest --amount 50 --sender 0xSpender
  ▼
cli_dispatch.main() → tx_assembly.do_transfer_from
  │
  ├──▶ fetch_decimals → 6
  ├──▶ fetch_symbol   → "USDC"
  ├──▶ human_to_base  → 50_000_000
  ├──▶ encode_transfer_from(from=0xHolder, to=0xDest, amount=50_000_000) → calldata
  │
  ├──▶ try:
  │       allowance = contract_reads.fetch_allowance(rpc, url, token, holder=0xHolder, spender=0xSpender)
  │     except _core.RPCError as e:
  │       warnings.append(("allowance_check_skipped", {"reason": str(e)}))
  │     else:
  │       if allowance < 50_000_000:
  │         warnings.append(("low_allowance", {holder, spender, current=allowance,
  │                                             requested=50_000_000, decimals=6}))
  │
  ├──▶ estimate_gas (still runs; soft-check is advisory only) — FATAL on fail
  ├──▶ _core fees + nonce
  │
  └──▶ return tx, ctx, warnings
  │
  ▼
cli_dispatch.main()
  │ for w in warnings: summary.emit_warning(w)
  │     └──▶ warn_low_allowance or warn_allowance_check_skipped → stderr
  │ summary.print_summary(ctx) → stderr
  │ print(json.dumps(tx, indent=2)) → stdout
  └ return 0
```

### Flow 4 — `eth_estimateGas` failure (NO FALLBACK; the structural invariant)

```text
gas_estimator.estimate_gas
  │
  ├──▶ rpc("eth_estimateGas", [...])  raises _core.RPCError("execution reverted: ERC20: transfer amount exceeds balance")
  │
  └── (NO try/except in this function) → RPCError propagates up
       │
       ▼
       tx_assembly.do_transfer / do_approve / do_transfer_from
       │
       └── (NO try/except around estimate_gas) → RPCError propagates further
            │
            ▼
            cli_dispatch.main()
              │ except (ValueError, _core.RPCError) as e:
              │   print("error: %s" % e, file=sys.stderr)   → stderr: error message including the revert reason
              │   return 1                                   → no JSON printed on stdout
```

**The structure of "no `try/except` in the middle layers around
`estimate_gas`" is itself a load-bearing design fact.** The gas-fallback
anti-pattern would be a *new* `try/except` inserted in one of the middle
layers, which is now visibly absent everywhere it could be added. A test
(in `TestTxAssembly` and `TestCliDispatch`) asserts that on
`estimate_gas` raising, exit code is 1 and stdout is empty.

---

## Infrastructure & Deployment

### Deployment Model

- **In-tree, flat directory.** Both helpers live at
  `.claude/skills/eth-tx-builder/` alongside one another. No `__init__.py`,
  no package, no `sys.path` games — `import build_send_eth` works because
  both files are in the same directory and Python is launched from there
  (matches the v1 invocation pattern).
- **No build step.** Pure Python stdlib; invocation is
  `python3 build_erc20.py <subcommand> ...` (or
  `python3 build_send_eth.py ...` for the v1 path).
- **No packaging.** Not a wheel, not a console-script entry-point. The
  Claude Code skill invokes via direct subprocess.
- **CI.** `python3 -m unittest test_build_send_eth -v` and
  `python3 -m unittest test_build_erc20 -v` both run from the skill
  directory. Both must pass.

### Scaling Strategy

Not applicable. The helper is a one-shot CLI; performance budget is bounded
by 6–7 sequential RPC calls (each at 15s timeout via `_core.rpc_call`),
typically ~1–3 seconds in practice. Bottleneck is the public RPC endpoint,
shared with every other publicnode user — out of our control.

### Service Extraction Path

This architecture is deliberately *un-extracted at runtime* (no separate
processes, no service boundary). At the code-organization level, the seven
in-file sections of `build_erc20.py` map cleanly onto separately-extractable
units if and when this functionality moves beyond a CLI script.

| Section / Module | Extraction readiness | Trigger |
|---|---|---|
| `abi_codec` | **Ready now.** Trivially a standalone Python package. A hypothetical sibling MCP server doing ABI decode would import it as-is. | Sibling that needs ABI encode/decode (e.g. `build_erc721.py`, an MCP decoder). |
| `amount_codec` | **Ready now.** Pure functions, no chain knowledge. Could be lifted into a `libs/eth-format/` Go package if the Go monorepo grows a sibling builder. | Cross-language need. |
| `contract_reads` | **Ready now.** Depends on `abi_codec` and on a generic `rpc` callable — both portable. | Sibling that needs ERC-20 reads. |
| `gas_estimator` | **Ready now.** The +20%/300k policy is the only opinionated piece. | Sibling that wants the same gas policy. |
| `summary` | **Ready now.** UI text rendering; could ship with the CLI in any language. | UI overhaul. |
| `tx_assembly` | **Ready now.** Composes Layer 1 and Layer 2; would move whole. | Multi-helper consolidation. |
| `cli_dispatch` | **Ready now.** Thin shim; trivially replaced by an MCP tool handler or HTTP endpoint that calls `do_*` and serializes the result. | Wrap as MCP / HTTP service. |
| `_core` (= `build_send_eth.py`) | **Needs work.** Dual role (CLI + library) is the only "shared-from-v1" coupling. | A third sibling helper lands OR the read-only v1 constraint is lifted. |
| Hypothetical `_tx_core.py` | **Does not exist in v1.** Would expose the v1 plumbing list (NETWORKS, RPCError, rpc_call, fetch_nonce/base_fee/tip, compute_max_fee, network_config, validate_hex_address, parse_hex_int, USER_AGENT, DEFAULT_TIP_WEI). | Same trigger as above. |

**The shared-core extraction story.** When the v1 read-only constraint is
relaxed (a third helper or a future repo refactor), the only mechanical step
is to extract `build_send_eth.py`'s plumbing into `_tx_core.py`, leaving
`build_send_eth.py` with just `build_tx_request` + `main` (the ETH-send-
specific code) importing the new core. Every existing call in
`build_erc20.py` (already prefixed `_core.`) keeps working by changing one
line: `import _tx_core as _core`. The `_core` alias is the seam.

Hypothetical FFI / network service extraction is **out of scope,
indefinitely** — there is no reason to extract a service from a one-shot
CLI.

---

## Technology Choices

| Concern | Choice | Rationale |
|---|---|---|
| Language | Python 3.8+ | Matches v1; PRD NFR "stdlib only" |
| Std libs | `argparse`, `json`, `re`, `sys`, `urllib.request` (transitive via `_core`) | Exact v1 set; no new imports |
| Module layout | Two sibling files (`build_send_eth.py`, `build_erc20.py`) + `import build_send_eth as _core` | Hard v1 constraint; maximizes DRY without extraction; one-edge DAG |
| Internal structure | Seven labeled in-file sections in dependency order (Layer 1–4) | Matches v1's single-file convention; structural enforcement of layering |
| ABI encoding | Hand-coded, hex-string concatenation, `int.to_bytes` | PRD §8 + research/01-abi-encoding |
| Keccak | None. Selectors hardcoded as constants with derivation comments. | PRD NFR; `hashlib.sha3_256` is NOT keccak |
| Big-int math | Python `int` | Arbitrary precision; no overflow at uint256 |
| Amount math | Integer string manipulation only (`str → str → int`); no `float`, no `Decimal`, no `Fraction` | PRD NFR; ADR-008; `inspect.getsource` negative assertion |
| HTTP | `_core.rpc_call` (v1 `urllib.request.urlopen`) | Reuse v1; 15s timeout, no retry |
| RPC injection | `rpc=_core.rpc_call` kwarg default on every chain-touching function | Matches v1 testability pattern |
| JSON | `json` stdlib | Matches v1 |
| Address regex | `_core.validate_hex_address` (which uses `^0x[0-9a-fA-F]{40}$`) | Matches v1; checksum is downstream in the signer |
| Test framework | `unittest` + `unittest.mock` | Stdlib; matches v1 (`test_build_send_eth.py`) |
| Logging | `sys.stderr.write` + `print(file=...)` | No structured logger; CLI is the surface |
| Concurrency | None (sequential) | 6–7 RPC calls bounded by 15s timeout each; PRD §Performance accepts this |
| Lint / format | None added | Repo's Go lint config does not cover Python; consistent with v1 |
| CI | Existing `make test` (Go-focused) unchanged; Python tests run manually as part of PR review | Two extra manual commands; same as existing v1 verification |

---

## ADRs (Architecture Decision Records)

### ADR-001: Reuse v1 plumbing by direct module import; do NOT duplicate; do NOT extract

- **Status:** Accepted.
- **Context:** ERC-20 helper needs v1's network map, EIP-1559 fee logic,
  nonce fetch, address validation, hex parsing, `RPCError`. PRD HARD-FORBIDS
  editing `build_send_eth.py`. Three options: (a) import from v1, (b) extract
  `_common.py` (requires editing v1), (c) duplicate verbatim.
- **Decision:** (a) — `import build_send_eth as _core`. Consume the named
  symbol list documented in this architecture. No edits to v1; no third
  module extracted.
- **Alternatives Considered:**
  - **(b) Extract `_common.py`.** Cleanest long-term shape but violates the
    hard constraint (would need to edit v1 to import from the new module).
    Rejected for P0; revisit when the constraint is lifted.
  - **(c) Duplicate v1 helpers verbatim.** Doubles the surface; ~70 lines of
    drift-prone code with only a DRIFT NOTE comment as control. Rejected:
    comments are routinely ignored, and DRY without extraction is achievable
    by (a).
- **Consequences:**
  - (+) Zero duplication; structural prevention of drift; `make test` on the
    v1 file remains untouched.
  - (+) Single source of truth for the network map, fee strategy, RPC
    transport, address validation, and error class.
  - (+) When the v1 read-only constraint relaxes, migrating to a true
    `_tx_core.py` is a one-line change to the `_core` alias.
  - (−) `build_send_eth.py` now has a dual role (CLI + library); its public
    symbol set is load-bearing for `build_erc20.py`. Mitigated by the top-of-
    file docstring in `build_erc20.py` listing the imported names; a future
    v1 rename surfaces as `ImportError` at test load-time.
  - (−) Loading `build_erc20.py` always loads `build_send_eth.py`. Negligible
    cost; one module body.

### ADR-002: Seven labeled in-file sections, strict downward DAG

- **Status:** Accepted.
- **Context:** PRD scope is small (one new script, three subcommands). A
  naive design might collapse everything into 2–3 sections (encoding + io +
  main). However, the PRD already names seven distinct change axes (new
  selector, new amount format, new contract read, gas policy tweak, new
  warning, new op composition, new subcommand). Decomposing along those axes
  makes each future P1/P2 item land in exactly one section.
- **Decision:** Seven sections in dependency order:
  - Layer 1 (leaves): `abi_codec`, `amount_codec`.
  - Layer 2: `contract_reads`, `gas_estimator`, `summary`.
  - Layer 3: `tx_assembly`.
  - Layer 4 (top): `cli_dispatch`.
  No peer imports within a layer; only Layer 3 fans out across Layer 2;
  file reads top-down in dependency order.
- **Alternatives Considered:**
  - **3-section design** (codec + io + main). Rejected: `permit` (P2)
    would touch all three; `balanceOf` pre-check (P1) would touch two.
    Defeats the layering goal.
  - **Sub-package** (`erc20/__init__.py`, `erc20/encode.py`, etc.). Rejected
    as scope inflation for ~500-line scope; introduces directory shape,
    `sys.path` discipline, complicates the SKILL.md invocation pattern.
  - **Helper file** (`_erc20_helpers.py`) alongside `build_erc20.py`.
    Rejected — adds a file with no clean ownership boundary; encoders /
    decoders / RPC helpers are tightly coupled to the `do_*` functions
    anyway.
- **Consequences:**
  - (+) Each future PRD item lands in exactly one section. `--summary-only`
    (P1 §5) is two lines in `cli_dispatch`. `balanceOf` pre-check (P1 §2) is
    one new function in `contract_reads` + one new warning in `summary` +
    one new call site in `tx_assembly.do_transfer`. `permit` (P2) is new
    selectors + new encode in `abi_codec` + new `do_permit` in `tx_assembly`
    + new subcommand in `cli_dispatch`.
  - (+) Mirrors v1 (one runnable file). No directory shape, no
    `__init__.py`.
  - (−) `build_erc20.py` is ~3× the size of v1 (~500–700 lines vs ~170).
    Acceptable for a single CLI with clear section banners; revisit if the
    file exceeds ~800 lines or a third helper lands.
  - (−) Layer boundaries are convention-only (no language-level
    enforcement). Mitigated by (1) test layout mirroring sections (ADR-011),
    (2) `# === Layer N: <name> ===` banner comments as grep-targets, and
    (3) the file reading top-down so an upward call would be visually
    obvious.

### ADR-003: Pure functions with injected `rpc` (v1 style)

- **Status:** Accepted.
- **Context:** Testability and the no-globals discipline already established
  by v1.
- **Decision:** Every function that touches the chain takes
  `rpc=_core.rpc_call` as a kwarg default. Tests pass a stub
  (`unittest.mock.Mock`). No module-level RPC state, no globals.
- **Alternatives Considered:**
  - **Class-based `Builder` with `rpc` in `__init__`.** Rejected: more
    machinery than needed; v1 doesn't do this; harder to mix-and-match stubs
    per test method.
  - **`functools.partial`-based DI.** Rejected: less grep-able for future
    maintainers; no advantage over a kwarg.
- **Consequences:**
  - (+) Matches v1 verbatim; reviewers don't have to learn a new pattern.
  - (+) Tests stay fast (no network), match v1 style exactly, no fixtures
    required.
  - (+) Easy to swap rpc transport later (httpx, async) without changing
    function bodies — though no such swap is planned.
  - (−) Function signatures all carry `rpc` as a trailing kwarg. Minor
    verbosity.

### ADR-004: `do_*` functions return `(tx_dict, summary_ctx, warnings_list)`; CLI prints

- **Status:** Accepted.
- **Context:** v1's `build_tx_request` returns a dict; `main` does the I/O.
  We want the new `do_*` functions to preserve that split for testability.
  PRD §16 mandates a stderr summary and (for some paths) `WARNING:` lines.
- **Decision:** Each `do_*` returns a 3-tuple:
  - `tx_dict` — the JSON-ready TxRequest dict (matches v1 shape exactly,
    with ERC-20 values in `to`/`value`/`data`/`gas`).
  - `summary_ctx` — dict consumed by `summary.print_summary`.
  - `warnings_list` — list of `(kind, payload_dict)` tuples consumed by
    `summary.emit_warning`.
  Printing — JSON to stdout, summary + warnings to stderr — happens
  exclusively in `cli_dispatch.main()`.
- **Alternatives Considered:**
  - **`do_*` prints summary directly.** Rejected: couples I/O to logic,
    breaks test purity, makes snapshot-style assertions harder.
  - **`do_*` returns `tx_dict` only; CLI re-derives summary.** Rejected:
    forces the CLI to re-run RPC reads or re-parse amounts.
  - **Warnings as exceptions.** Rejected: warnings are not errors; raising
    fights the language and complicates the success path.
- **Consequences:**
  - (+) Tests for `build_*` assert on dict equality + `warnings_list`
    contents without capturing output.
  - (+) Warnings are serializable data; reordering or batching at the CLI
    layer is trivial.
  - (+) Adding a new warning kind (P1 race-guard, balanceOf pre-check) is
    a `(kind, payload)` tuple addition plus one `summary.warn_*` function —
    no new control-flow plumbing.
  - (−) Slightly more bookkeeping in `do_*`. Pattern is simple and
    consistent across all three ops.

### ADR-005: Hardcoded selectors; no runtime Keccak

- **Status:** Accepted.
- **Context:** PRD NFR "stdlib only". `hashlib.sha3_256` is *not* keccak
  (SHA-3 finalisation differs). Vendoring a pure-Python keccak would
  violate stdlib-only. The six ERC-20 selectors are publicly known and
  cross-verified against multiple primary sources.
- **Decision:** Module-level hex-string constants
  (`SEL_TRANSFER = "0xa9059cbb"`, etc.), each with a one-line derivation
  comment naming the canonical signature
  (`# keccak256("transfer(address,uint256)")[:4]`).
- **Alternatives Considered:**
  - **Vendor a pure-Python keccak.** Rejected: violates stdlib-only; extra
    surface for security review; no upside since selectors are stable
    forever.
  - **Compute via subprocess to a system tool.** Rejected: adds runtime
    dependency on an external binary; non-portable.
- **Consequences:**
  - (+) Stdlib-only constraint satisfied.
  - (+) Tests compare selectors against canonical hex strings byte-for-byte;
    a typo fails immediately.
  - (+) Calldata can be golden-vector tested against the USDC mainnet
    vectors from research/01-abi-encoding.
  - (−) New ERC-20 read (e.g. `balanceOf`) requires a new module-level
    constant + comment. Trivial maintenance.

### ADR-006: Structural fatal-vs-best-effort split via return types

- **Status:** Accepted.
- **Context:** Research overview §2 reconciles "decimals is OPTIONAL per
  EIP-20" with "estimate failure is fatal" as: reads whose result determines
  calldata or gas correctness are fatal; reads that only enrich the human
  summary degrade gracefully. We want to enforce this structurally rather
  than by convention.
- **Decision:** Encode the policy in the return type of each decode/fetch
  function:
  - `decode_decimals` raises `ValueError` on out-of-range (`> 36`);
    `fetch_decimals` propagates `RPCError`. **FATAL.**
  - `decode_symbol` returns `Optional[str]` (asymmetric case);
    `fetch_symbol` swallows all failures and returns `None`. **BEST-EFFORT.**
  - `decode_allowance` returns `int`; `fetch_allowance` propagates
    `RPCError`. The soft-check posture is the caller's responsibility:
    `tx_assembly.do_transfer_from` is the ONE place that wraps
    `fetch_allowance` in a local `try/except RPCError`.
  - `estimate_gas` propagates `RPCError`. **FATAL. No try/except in
    intermediate layers** (see ADR-007).
- **Alternatives Considered:**
  - **All reads raise; CLI catches everything.** Rejected: would force a
    try/except dance for symbol in `tx_assembly`, blurring the
    enrichment-vs-correctness split.
  - **All reads return `Optional`.** Rejected: would let `decimals` failure
    silently produce wrong calldata.
  - **Convention-only enforcement.** Rejected: type-level signal is more
    durable than a code comment.
- **Consequences:**
  - (+) Each function's signature documents its failure posture.
  - (+) The split is structurally enforced rather than convention-only.
  - (+) Matches PRD acceptance criteria exactly (P0 §6, §9, §10, §11).
  - (−) `decode_symbol` is the asymmetric case in the codec; documented in
    its docstring.

### ADR-007: `eth_estimateGas` failure is fatal; no fallback ever — enforced by absence of try/except

- **Status:** Accepted.
- **Context:** PRD §9 + research §3 are emphatic: a hardcoded gas fallback
  would let a doomed tx get signed and burn its full gas budget on revert.
  A future maintainer might be tempted to add `gas = 100_000` "for
  robustness."
- **Decision:** `gas_estimator.estimate_gas` does NOT catch `RPCError` at
  all. `tx_assembly.do_*` does NOT catch `RPCError` around the estimate
  call. Only `cli_dispatch.main()` catches `RPCError`, and only to format
  the error and exit 1 — never to construct a tx. The **absence of a
  `try/except`** in the middle layers is itself a load-bearing design fact;
  an in-code multi-line comment in `gas_estimator.estimate_gas` makes this
  explicit, citing research §03's gas-budget-burn argument.
  
  A test in `TestTxAssembly` (and again in `TestCliDispatch`) asserts that
  on `estimate_gas` raising, exit code is 1 and stdout is empty — so any
  future addition of a silent fallback fails the test.
- **Alternatives Considered:**
  - **Fall back to `GAS_CAP` (300_000).** Rejected: the cap is a ceiling,
    not a guess; using it as a default disguises real errors.
  - **Fall back per-op** (e.g. 65k for `transfer`). Rejected: same failure
    mode at on-chain replay; per-op fallback only delays the revert by
    hiding the diagnostic.
  - **Optional `--gas-fallback` CLI flag** for "I know what I'm doing"
    operators. Rejected for v1; PRD §Out-of-Scope §Per-method gas fallback
    closes this door explicitly. Could be revisited in v2 with a loud
    opt-in flag, but the default would still be no-fallback.
- **Consequences:**
  - (+) Build is correct-by-construction: there is no code path that
    silently substitutes a gas number.
  - (+) A future maintainer adding a fallback has to **insert** a
    `try/except` into a module that has none — a visible review-flag.
  - (+) Operators see the node's revert reason at build time (free) rather
    than at broadcast time (expensive).
  - (−) If publicnode rate-limits the estimate but the call would succeed
    on a self-hosted node, the build fails. Acceptable for v1; `--rpc-url`
    override is a P2 escape hatch in the PRD.

### ADR-008: Integer-only token amount conversion; no `float()` anywhere on the amount path

- **Status:** Accepted.
- **Context:** PRD Non-Functional Requirements: "No float arithmetic on
  token amounts." Float drift on 18-decimal tokens silently corrupts values
  (`float("0.1") * 10**18` ≠ `100000000000000000`).
- **Decision:** `amount_codec.human_to_base_units(s, decimals)` is pure
  string manipulation: split on `"."`, validate halves with `re.fullmatch`,
  reject `len(frac) > decimals`, right-pad `frac` to `decimals` digits,
  concatenate, `int(..., 10)`. Never calls `float()`, `decimal.Decimal`, or
  `fractions.Fraction`.
  
  Test asserts both:
  - **Positive:** golden vectors (`"0"`, `"0.0"`, `"1.5"`, `"0.000001"`,
    max-decimals, large values).
  - **Negative:** `inspect.getsource(b.human_to_base_units)` does NOT
    contain the substring `"float("`.
- **Alternatives Considered:**
  - **`decimal.Decimal`.** Stdlib and exact, but adds a layer that can be
    misused. Rejected for simplicity; pure string-split is enough.
  - **`fractions.Fraction`.** Overkill; same objection.
- **Consequences:**
  - (+) No float drift; testable arithmetically.
  - (+) Operates correctly on any decimals 0–36 (PRD ceiling).
  - (+) Negative assertion via `inspect.getsource` catches a future
    maintainer "fixing" the conversion with `float()`.
  - (−) Error messages are bespoke (no generic numeric parser). Acceptable;
    PRD calls for specific wording for each rejection.

### ADR-009: Stdout = JSON only; stderr = summary + warnings + errors

- **Status:** Accepted.
- **Context:** PRD §16 mandates a clean stdout/stderr split so operators
  can pipe stdout into the signer or `jq`. v1 prints JSON only on stdout
  (errors on stderr) but does not print a summary. ERC-20 helper adds a
  summary and warnings; the discipline must hold.
- **Decision:**
  - **Stdout:** exactly one `print(json.dumps(tx, indent=2))` call, only on
    the happy path, at the end of `cli_dispatch.main()`.
  - **Stderr:** `sys.stderr.write(...)` for `error:`, `WARNING:`, and the
    summary block. The summary always prints on the happy path; the JSON
    immediately follows on stdout.
  - **Order of stderr writes** (within one `main()` call): all queued
    warnings first (via `summary.emit_warning(w)`), then the summary block
    (`summary.print_summary(ctx)`), then the JSON on stdout. Terminal
    output interleaves cleanly because stderr is line-buffered.
  - **Prefixes:** `error: <msg>` (lowercase, matches v1) for fatal exit-1;
    `WARNING: <msg>` (allcaps) for soft warnings; bare text for the summary
    block.
- **Alternatives Considered:**
  - **Print both JSON and summary on stdout, separated by a marker.**
    Rejected — breaks `jq` / pipe-to-signer.
  - **`--summary-only` and `--json-only` flags.** Rejected — added complexity
    for v1; PRD P1 §5 already lists `--summary-only` as a Phase-2 item.
- **Consequences:**
  - (+) `python3 build_erc20.py transfer ... | jq .` Just Works.
  - (+) `python3 build_erc20.py transfer ... 2>/dev/null` gives the
    operator only the JSON.
  - (+) Tests assert the stdout/stderr split exactly.
  - (−) Operators redirecting stderr lose the safety summary; this is
    explicit, opt-in behavior, not a default.

### ADR-010: Address validation happens once, at the CLI layer

- **Status:** Accepted.
- **Context:** PRD §13 says format-only address validation, once. We could
  re-validate inside `do_*` for safety-in-depth.
- **Decision:** Validate all addresses in `cli_dispatch._validate_addresses`
  using `_core.validate_hex_address`. `do_*` accepts already-validated
  hex.
- **Alternatives Considered:**
  - **Validate again in `do_*`.** Rejected: redundant; two error paths for
    the same condition; tests would need to cover both layers.
- **Consequences:**
  - (+) Single source of truth for validation; clear error messages from
    one place.
  - (+) `do_*` stays focused on composition rather than input scrubbing.
  - (−) Calling `do_*` directly (e.g. from a future MCP wrapper) requires
    the caller to validate first. Documented in `do_*` docstrings.

### ADR-011: Test file mirrors module layout (one TestCase per Layer 1–4 section)

- **Status:** Accepted.
- **Context:** v1 `test_build_send_eth.py` is loosely organized; for a
  seven-section file we want a deliberate test layout that doubles as a
  drift detector.
- **Decision:** `test_build_erc20.py` defines exactly one `TestCase` class
  per Layer 1–4 section: `TestAbiCodec`, `TestAmountCodec`,
  `TestContractReads`, `TestGasEstimator`, `TestSummary`, `TestTxAssembly`,
  `TestCliDispatch`. Each class tests only its section's public API;
  cross-section integration tests live in `TestTxAssembly` and
  `TestCliDispatch` (the composition layers).
- **Alternatives Considered:**
  - **One flat file with per-function tests.** Rejected: grep-ability
    suffers; under-tested sections become invisible.
- **Consequences:**
  - (+) `python3 -m unittest test_build_erc20.TestAbiCodec` runs codec
    tests in isolation.
  - (+) Test counts per layer are visible at a glance; under-tested layers
    stand out.
  - (+) A function that doesn't fit any existing `TestCase` is a smell —
    PR reviewer should ask whether the section boundary is wrong.
  - (−) Slight overhead in setup boilerplate per class. Negligible.

---

## Assumptions

(Consolidated from all three candidates' assumption lists; recorded here per
the workflow instruction not to ask the user.)

1. **The skill directory layout stays flat.** No `__init__.py`, no
   `erc20/` subdir, no test-runner config. PRD names the files at flat
   paths; existing v1 layout is flat. `import build_send_eth` resolves
   because both files live in the same directory and Python is launched
   from there.
2. **`build_send_eth.py` is import-safe.** Inspection confirms the module
   body only defines names and the `NETWORKS` dict; the
   `if __name__ == "__main__":` guard prevents `main()` from running on
   import.
3. **Public RPC endpoints (`publicnode.com`) are sufficient.** v1
   hardcodes them; PRD does not call for `--rpc-url` (explicitly Out of
   Scope). `_core.NETWORKS` is the single source of truth.
4. **Python 3.8+ stdlib is the runtime.** PRD success metric "Zero new
   dependencies" verified by `python3 build_erc20.py --help` on a fresh
   install. No version check is added to the helper.
5. **Tests run from the skill directory** via
   `python3 -m unittest test_build_erc20 -v` (mirroring v1). No `pytest`,
   no `tox`, no `Makefile` target — the existing repo Makefile targets are
   Go-focused.
6. **The PRD's P1/P2 items are not in scope for the P0 delta.**
   `balanceOf` pre-check, approve race guard, `sepolia`/`holesky`,
   `--summary-only`, `--revoke`, polished bytes32 decode, `permit` — all
   deferred. Architecture leaves room for each (each lands in exactly one
   layered section).
7. **`eth-signer-mcp`'s `sign_transaction` accepts the ERC-20 TxRequest
   shape today** (same as v1, with non-zero `data` and zero `value`). PRD
   does not request signer-side changes; consistent with `eth-signer-mcp`'s
   validate.go accepting any well-formed EIP-1559 TxRequest.
8. **`hoodi` is reachable from dev / CI machines** for the manual e2e
   check. v1 already assumes this.
9. **Selectors do not need a runtime verify step.** Hardcoded constants
   plus golden-vector tests against the USDC mainnet vectors in research
   §01-abi-encoding deliver the same byte-level guarantee a runtime keccak
   would.
10. **`eth_call` block tag is `"latest"`** for `decimals`, `symbol`,
    `allowance`, matching `eth_estimateGas`'s `"latest"`.
11. **No extra RPC retries.** `_core.rpc_call` enforces a 15-second
    timeout; no exponential backoff. PRD NFR Performance and v1 posture.
12. **The `decode_symbol` fallback handles MKR-style `bytes32` symbols
    only.** Bullet-proof legacy decoder polish is P2 (PRD Nice-to-Have).
13. **`--approve-max` is mutually exclusive with `--amount` at the
    argparse layer** (via `add_mutually_exclusive_group(required=True)`),
    not at the build-function layer. `do_approve` takes an optional
    `approve_max=False` and resolves to `MAX_UINT256` when set.
14. **`make_fake_rpc` test helper is duplicated** (~12 lines) from
    `test_build_send_eth.py` into `test_build_erc20.py` with a one-line
    comment noting the hard constraint on the v1 test file. Promote to
    `_test_helpers.py` when the read-only constraint is relaxed.
15. **CI invariant.** The repo's Python tests are run manually as part of
    PR review (existing posture). If a future change to v1 removes a symbol
    `build_erc20.py` imports, `python3 -m unittest test_build_erc20 -v`
    fails at import time, clearly attributable.
16. **The "no edit to `build_send_eth.py`" constraint applies to the P0
    delta**, not indefinitely. PRD P1 §4 explicitly plans to add
    `sepolia`/`holesky` to `NETWORKS`; that P1 work will edit v1 with its
    own test additions. The architecture documents the seam; the freeze
    applies to Phase 1 delivery.
17. **Operators run on macOS / Linux with Python 3.8+;** Windows is not a
    v1 target (matches v1 posture).
18. **Summary stderr output is line-oriented, ASCII-only, with a stable
    layout** (P1 `--summary-only` will reuse the same renderer).

---

## Open Questions

Captured from the three candidates' Open Questions. None block P0; all are
confirmable at review or deferrable to follow-up.

1. **`sepolia` / `holesky` (PRD P1 §4).** The `NETWORKS` dict lives in the
   read-only v1 file. Two paths:
   - (a) Accept that "read-only" applies during P0 only; in P1, edit
     `build_send_eth.NETWORKS` directly with v1 test additions.
   - (b) Add an `EXTRA_NETWORKS` overlay in `build_erc20.py` + a
     `resolve_network(name)` helper that consults both maps.
   **Recommendation:** (a). It is the simplest path and matches the
   `--rpc-url` non-goal. (b) produces two divergent network surfaces and
   slowly accretes complexity.
2. **Promoting `approve`-race check from P1 §3 to P0.** Adds one extra
   `allowance(sender, spender)` RPC call to `do_approve`. Architecturally
   fits the same warn-don't-block pattern as the `transfer-from`
   soft-check. Architecture supports either placement; default per PRD
   is P1.
3. **`balanceOf(sender)` pre-check on `transfer` (PRD P1 §2).** Same
   warn-don't-block pattern; one new `fetch_balance_of` in
   `contract_reads`, one new warning in `summary`, one new queue site in
   `tx_assembly.do_transfer`. Architecture supports it cleanly.
4. **Should `MAX_UINT256` live in `abi_codec` or `amount_codec`?** Both are
   defensible. Architecture places it in `amount_codec` because it is an
   *amount* value, not an ABI encoding rule. Confirmable at review.
5. **`do_transfer_from` order of operations when `fetch_decimals` already
   failed.** Currently `do_transfer_from` runs reads in order; a raise in
   `fetch_decimals` short-circuits the rest (no allowance check). This is
   the simplest behavior; a token that fails `decimals()` is unbuildable
   anyway. Confirmable at review.
6. **Re-export `RPCError` from `build_erc20.py`?** E.g.
   `RPCError = _core.RPCError` at module top so external tests could catch
   it via `build_erc20.RPCError`. No external consumer exists yet; defer
   until a need surfaces.
7. **`--summary-only` (PRD P1 §5).** Architecture supports it: the
   `summary` section already separates `render_summary` (pure) from
   `print_summary` (I/O). `cli_dispatch` adds the flag and short-circuits
   before the `print(json.dumps(tx))` call. Two-line change.
8. **User-Agent drift.** Architecture inherits `_core.USER_AGENT`
   transitively via `_core.rpc_call`. If v1 ever bumps it, ERC-20 picks up
   the new value automatically. No drift risk under the import strategy.
9. **CI wiring of Python tests into `make test`.** Currently
   `make test` is Go-focused; Python tests run manually. Adding a Python
   step to `make test` is a small repo-wide change; defer to a separate
   PR if desired.
10. **Automated regression check that `build_send_eth.py` is byte-identical
    post-delta.** The existing controls (PRD P0 §17, code review,
    `test_build_send_eth` running unchanged) are believed sufficient. A
    git-blame-style or hash-check assertion would add CI scope; defer
    unless requested.

---

## Risks

| Risk | Likelihood | Impact | Mitigation |
|---|---|---|---|
| A regression in `build_send_eth.py` slips in despite the no-edit rule (e.g. stray gofmt, whitespace) | Very low | v1 path breaks | `test_build_send_eth.py` is the regression check; CI / PR review must run it; constraint is documented in PRD P0 §17 |
| Future maintainer edits `build_send_eth.py` and breaks an imported symbol used by `build_erc20.py` | Medium over years | `build_erc20.py` fails at import | Top-of-file docstring in `build_erc20.py` lists imported names; `test_build_erc20.py` exercises the import edge; CI fails fast and is clearly attributable |
| Future maintainer adds a silent fallback to `eth_estimateGas` "for robustness" | Low in v1; Medium years later | Doomed tx gets signed and burns gas budget | Structural: absence of `try/except` in middle layers is the invariant. ADR-007 makes this a tracked architectural decision. In-code multi-line comment explains why. Regression test asserts no JSON on stdout when estimate fails |
| ABI selector typo / bit-pattern bug in encoder | Medium without tests; Low with tests | On-chain revert at broadcast | Selector constants tested against canonical hex strings; calldata tested against USDC mainnet golden vectors from research §01 |
| `decimals()` returns hostile value (255, 200) | Low | Wrong base-unit amount; potential over/under-spend | `decode_decimals` masks to low byte; rejects `> MAX_DECIMALS` (36); test covers 0, 6, 18, 24 OK and 37 rejected. Stderr summary always shows `decimals=<N>` adjacent to base-unit amount as a last-line-of-defense visual check |
| Float drift in amount conversion (a future maintainer "fixes" the parser with `float()`) | Low | Silent corruption of 18-decimal amounts | `human_to_base_units` is string-only; test asserts via `inspect.getsource` that the substring `"float("` is absent from the function body |
| `--approve-max` warning quieted by stderr redirect | Operator choice | Unlimited approval signed without seeing the warning | Documented in SKILL.md / README; warning is on stderr deliberately so it precedes the JSON in interactive use |
| publicnode rate-limits one of the 6–7 sequential RPC calls | Low for occasional skill use; Higher under load | Build fails with `error:` + exit 1 | RPC error propagates as fatal; operator retries; `--rpc-url` override is a PRD P2 escape hatch |
| A bytes32-symbol token (e.g. MKR) trips standard `string` decode and fallback also fails | Low for v1 token universe | Summary shows `(unavailable)`; build still succeeds | Symbol failure is non-fatal by design (ADR-006); PRD §10 |
| `allowance` soft-check blocks a legitimate multi-step `approve→transferFrom` workflow | Zero by design | n/a | Soft-check warns only; build proceeds; PRD §11 |
| Stdout pollution by a stray `print` in build / decode paths | Low | Breaks pipe-to-signer | All non-JSON output uses `sys.stderr.write` explicitly; test asserts stdout is exactly the JSON on happy path |
| Section boundaries inside `build_erc20.py` drift over time (no language-level enforcement of layering) | Medium over time | Architectural decay | Test layout mirrors sections (ADR-011); banner comments are grep targets; PR review checklist asks "did this PR touch only one section?" |
| Seven-section design is overkill if P1/P2 backlog never materializes | Acknowledged | Marginal verbosity | ADR-001/ADR-002 justification is each section maps to a distinct PRD change axis; if backlog stalls, the layering still pays for itself in test isolation and grep-ability |
| Stdlib-only constraint blocks future `permit` (EIP-2612 needs EIP-712 typed-data hashing) | Medium long-term | `permit` can't ship as part of this helper | PRD P2; gets its own helper (`build_erc20_permit.py`) and its own dependency conversation |

---

## Architecture Quality Checklist

- [x] **No circular dependencies between modules.** Two-node external DAG:
  `build_erc20 → build_send_eth`. Inside `build_erc20.py`: strict downward
  DAG across Layers 1–4 with no peer imports; only Layer 3 fans out across
  Layer 2.
- [x] **Each module has a single, clear responsibility.**
  - `build_send_eth.py`: build ETH-send TxRequest + (dual role) shared
    plumbing.
  - `build_erc20.py`: build ERC-20 TxRequest variants by composing layered
    sections.
  - Each in-file section (Layer 1–4) owns one PRD change axis.
- [x] **No shared databases / module owns its data.** Both helpers are
  stateless; the only "data store" is publicnode's RPC view, which is
  read-only.
- [x] **All inter-module communication goes through defined interfaces.**
  The 10-symbol `_core` import list is the v1 ↔ ERC-20 contract; documented
  in `build_erc20.py`'s top-of-file docstring. The public CLI is the
  operator-facing contract.
- [x] **Every module can be tested in isolation with mocked dependencies.**
  `rpc` is injectable as `rpc=_core.rpc_call` on every chain-touching
  function. Test classes mirror sections (ADR-011) and can run in isolation
  via `python3 -m unittest test_build_erc20.TestAbiCodec` etc.
- [x] **Cross-cutting concerns are standardized.** stdout/stderr discipline
  (ADR-009), `error:` vs `WARNING:` prefix conventions, `RPCError`
  propagation pattern (ADR-007), error format (matches v1). All applied
  uniformly.
- [x] **Failure modes are defined.** §Module Details enumerates every
  identifiable failure mode and its handling per layer.
- [x] **Service extraction path is clear.** §Infrastructure & Deployment ›
  Service Extraction Path names each section's extraction readiness, the
  trigger (third helper lands or v1 read-only constraint lifts), and the
  mechanical work (move `_core` alias target to `_tx_core.py`; one-line
  change).
- [x] **Data flow is traceable.** §Data Flow Diagrams cover the three
  happy paths + the no-fallback estimate failure path.
- [x] **Module count is justified.** Two runnable files (v1 + ERC-20),
  seven labeled in-file sections inside ERC-20 each mapped to a PRD
  change axis (ADR-001/ADR-002), two test files, two prose edits. Not
  under-split (would force re-touching v1); not over-split (sub-packages
  add directory shape without earning the boundary).
- [x] **`build_send_eth.py` and `test_build_send_eth.py` are bit-for-bit
  unchanged.** Hard constraint; ADR-001 specifies import-only reuse.

---

### ADR-012: `--revoke` argparse mutex shape + summary op naming

- **Status:** Accepted. (2026-06-14)
- **Context:** PRD §P2.3 names the `approve --revoke` shorthand as a
  "trivial to add later" convenience that emits `approve(spender, 0)`.
  Three small design decisions must be resolved before the implementation
  issue (3.2) proceeds: (1) the argparse mutex shape for a third mutex
  entry in the `approve` subparser; (2) the summary "operation" line
  wording when `--revoke` is set; (3) whether argparse's default mutex
  conflict message is acceptable or a custom message is needed.

  Today the `approve` subparser holds a
  `add_mutually_exclusive_group(required=True)` with two entries:
  `--amount` and `--approve-max` (architecture Assumption 13). Adding
  `--revoke` makes this a three-way mutex: exactly one of
  `{--amount, --approve-max, --revoke}` must be set.

- **Decision:**

  **(1) Argparse mutex shape:** Add `--revoke` as a third entry into the
  **existing** `add_mutually_exclusive_group(required=True)` on the
  `approve` subparser. Argparse's mutual-exclusion group enforces
  pairwise exclusion across all members regardless of group size; a
  three-entry group works natively without any manual post-parse check.
  No new group is created; no extra argument is added outside the group.
  Implementation: `amt_group.add_argument("--revoke",
  action="store_true", help="Revoke approval (sets allowance to 0 for
  spender).")` where `amt_group` is the existing exclusive group.

  **(2) Summary op naming:** When `--revoke` is set, the stderr
  summary's "operation" line reads **`revoke`** (not `approve`).
  Rationale: the operator chose the revoke shorthand specifically to
  signal intent; the summary should echo that intent for clarity. The
  calldata line in the summary still names the underlying
  `approve(spender, 0)` so technical accuracy is preserved at a
  different line in the block. This requires generalising
  `summary.render_summary` to read the operation label from
  `summary_ctx["op_label"]` rather than a per-subcommand hard-coded
  string — a purely additive refactor whose rendered output is
  byte-identical for every Phase 1 path (see Phase 1 touchpoint in
  Issue 3.2 implementation notes).

  **(3) Mutex conflict message:** Accept argparse's default error:
  `argument --revoke: not allowed with argument --amount` (or
  `--approve-max`). Adding a custom "use exactly one of
  --amount/--approve-max/--revoke" message would require a manual
  post-parse check, defeating decision (1)'s goal of argparse-native
  enforcement. A short SKILL.md note covers the operator-friendly
  guidance without adding code complexity.

  **(4) `do_approve` signature change:** `do_approve` gains a
  `revoke=False` keyword-only argument (mirroring `approve_max=False`).
  When `revoke=True`:
  - `amount_base = 0`; `human_to_base_units` is NOT called.
  - `encode_approve(spender, 0)` is used (no new selector; reuses ADR-005).
  - `op_label = "revoke"` is set in `summary_ctx` (decision 2 above).
  - A `("approve_revoke", {...})` warning tuple is queued (ADR-004 shape).

  If both `revoke=True` and `approve_max=True` are passed directly to
  `do_approve` (bypassing argparse), `ValueError("--revoke and
  --approve-max are mutually exclusive")` is raised as defense-in-depth
  for direct callers.

  **(5) `decimals()` is still read on the revoke path.** The summary
  still names the token's decimals for the operator's review, and
  symmetry with the `approve_max=True` path (which also skips human
  conversion but still reads `decimals`) is preserved. Skipping the
  `decimals()` read would create an asymmetric special case and violate
  the Phase 1 fatal-or-skip contract (architecture ADR-006). The
  `fetch_symbol` read is also preserved (best-effort; ADR-006).

- **Alternatives Considered:**

  - **Manual post-parse check instead of extending the group.** A
    manual check (`if args.revoke and args.amount: parser.error(...)`)
    would produce a fully custom error message. Rejected: adds ~5 lines
    of branching; duplicates the mutex logic that argparse already
    enforces; the default error message is adequate for operators.

  - **Summary op label reads "approve" (technically accurate).**
    Rejected: operators who chose `--revoke` chose it deliberately; the
    summary echoing "approve" would obscure the intent and make it harder
    to audit a summary block at a glance. The calldata line (`amount
    (base units): 0`) preserves the technical accuracy; the op label is
    the intent summary, not the ABI method name.

  - **Skip `decimals()` on the revoke path.** Rejected for two reasons:
    symmetry with `approve_max=True` (which also doesn't need the decimal
    count for amount conversion but still reads it) and the summary block
    always shows `decimals: N` as an operator sanity check. The micro-
    optimization is not worth the asymmetric code path.

- **Consequences:**

  - (+) `--revoke` is a zero-new-group, zero-manual-check extension of
    the existing two-entry mutex. The implementation is mechanical: add
    one `add_argument` call, one forwarding line in `main`, and one new
    branch in `do_approve`.
  - (+) The Phase 1 `render_summary` refactor (reading `op_label` from
    `summary_ctx`) is additive and byte-identical for existing paths;
    any regression is caught by the `TestSummary` pinning tests added
    in Issue 3.2.
  - (+) `warn_approve_revoke` is informational (no `WARNING:` prefix)
    rather than alarming — operators chose revoke deliberately.
  - (+) Defense-in-depth `ValueError` in `do_approve` guards future
    direct callers without adding CLI-layer complexity.
  - (-) The `do_approve` function now branches three ways
    (`revoke` / `approve_max` / human-amount). This is the expected
    complexity growth for a three-entry mutex; the branch is
    short-circuiting and independently testable.

  Cross-references: PRD §P2.3 (source requirement); architecture
  ADR-005 (no new selector — `encode_approve` is reused); architecture
  Assumption 13 (the existing two-way mutex this ADR extends).

---

### ADR-013: Polished bytes32 symbol decode — bounded format catalog

- **Status:** Accepted. (2026-06-14)
- **Context:** PRD §P2.4 names "bytes32 symbol decode polish" as a niche
  improvement against historical legacy token formats (MKR, DGD, and
  friends) that return `bytes32` instead of the standard ABI `string` for
  `symbol()`. The Phase 1 `decode_symbol` already handles two cases:
  (1) standard ABI `string` layout and (2) null-trimmed raw `bytes32`
  ASCII (the MKR pattern). The Phase 1 fallback uses a broad
  `except Exception` which is wider than necessary and does not explicitly
  enumerate which formats are supported. Research §02-erc20-safety-ux flags
  the `d-xo/weird-erc20` catalog as the bounded reference; project plan
  R10 explicitly bounds the task to a finite ship list to prevent scope
  creep.

  The two legacy on-chain formats NOT yet handled by Phase 1's named
  ladder but present in real deployed tokens are:

  **Format A — MKR-style null-padded ASCII bytes32 (Phase 1 already
  handles this):** The raw 32-byte response is the ASCII ticker
  right-padded with null bytes. Example — MKR token (mainnet
  `0x9f8F72aA9304c8B593d555F12eF6589cC3A579A2`), `symbol()` returns
  exactly 32 bytes: `4d4b52` followed by 29 null bytes
  (`0x4d4b520000000000000000000000000000000000000000000000000000000000`).
  Null-stripping gives `b"MKR"`.

  **Format B — DGD-style length-prefixed bytes32:** The raw 32-byte
  response encodes a length-prefixed string inside a single 32-byte word.
  The first byte is the byte length of the ticker; the following bytes
  are the ASCII ticker. Example — DGD token
  (`0xE0B7927c4aF23765Cb51314A0E0521A9645F0E2A`, now defunct but widely
  cited in the weird-erc20 catalog), `symbol()` returns a 32-byte word
  whose first byte is `0x03` (length 3) and the next three bytes are
  `444744` ("DGD"), followed by 28 null bytes:
  `0x0344474400000000000000000000000000000000000000000000000000000000`.
  This format is DISTINCT from the MKR-style null-pad — the first byte is
  a length prefix, not part of the ticker. Without explicit handling, the
  null-trimmed Phase 1 fallback would decode it as `"\x03DGD"` (with a
  non-printable leading byte), which the `isprintable()` guard would then
  reject, returning `None`.

  **Format C — Non-printable tail guard (Phase 1's `isprintable()` check
  is sufficient):** Some responses contain valid ASCII ticker characters
  followed by non-printable bytes that are not null (e.g. garbage padding
  from non-standard implementations). The existing Phase 1 null-trimmed
  path already rejects these via `isprintable()`. No new variant helper is
  needed; this is an implicit guard in the existing ladder.

- **Decision:**

  **Bounded ship list (three variants total, two new):**

  | Variant | Name | Hex example (32 bytes) | Expected ticker |
  |---------|------|------------------------|-----------------|
  | Phase 1 — standard ABI `string` | `_try_decode_abi_string` | `0x0000...0020` + `0000...0003` + `4d4b520000...` | e.g. `"MKR"` (if returned as ABI string) |
  | Phase 1 — null-padded ASCII bytes32 | `_try_decode_bytes32_null_trimmed` | `4d4b520000000000000000000000000000000000000000000000000000000000` | `"MKR"` |
  | NEW — DGD-style length-prefixed bytes32 | `_try_decode_bytes32_length_prefixed` | `0344474400000000000000000000000000000000000000000000000000000000` | `"DGD"` |

  **Fallback ladder order (short-circuit on first non-None):**

  1. `_try_decode_abi_string(hex_result)` — standard ABI dynamic `string`.
     Checked first because it is the most common modern format (USDC, DAI,
     WETH, etc.). If the response has an offset word of `0x20` (32), a
     valid length word, and sufficient bytes, decode as UTF-8.
  2. `_try_decode_bytes32_null_trimmed(hex_result)` — raw 32-byte
     response with the ticker left-aligned, null-padded on the right.
     Catches MKR, MANA (mainnet), and other early tokens. The
     `isprintable()` guard already rejects responses with non-printable
     bytes.
  3. `_try_decode_bytes32_length_prefixed(hex_result)` — raw 32-byte
     response where byte 0 is the string length and bytes 1..len are the
     ASCII ticker. Catches DGD and similar length-prefixed formats.
     Validate: `length = raw[0]`, then `raw[1:1+length]` must be printable
     ASCII and length must be in range `[1, 31]` (a 0-length prefix is not
     a valid ticker; a length ≥ 32 overflows the 32-byte word and is junk).
  4. Return `None` — unknown / unsupported format. Best-effort posture
     preserved; the summary shows `(unavailable)` and the build continues
     (architecture ADR-006).

  **Order rationale:** ABI `string` first because it is unambiguous (the
  offset word 0x20 is a strong discriminator). Null-trimmed second because
  it is the most common legacy format and the byte pattern (ticker at
  offset 0, nulls from offset len(ticker)) does not overlap with
  length-prefixed (which has a non-printable byte at offset 0 for any
  ticker shorter than `\x20`). Length-prefixed third because its byte 0
  could theoretically collide with a short ticker whose first byte happens
  to be a low-value printable character — but in practice, the
  null-trimmed step rejects length-prefixed responses via `isprintable()`
  since `\x03` is non-printable, so the ordering is safe.

  **Explicit "still returns None" tail:** Any `symbol()` response that
  does not match one of the three above ladder steps (including non-standard
  encodings, ABI errors, empty responses, non-UTF-8 garbage, or responses
  shorter than 1 byte) returns `None`. These formats are outside the
  bounded catalog and remain best-effort. The scope-creep guard test
  (`_try_decode_*` returns `None` for a response that matches no variant)
  enforces this property in the test suite.

  **Implementation shape (Issue 3.4):**
  ```python
  def _try_decode_abi_string(hex_result):  # Optional[str]
      ...

  def _try_decode_bytes32_null_trimmed(hex_result):  # Optional[str]
      ...

  def _try_decode_bytes32_length_prefixed(hex_result):  # Optional[str]
      # raw = bytes.fromhex(hex_result.lstrip("0x") or hex_result[2:])
      # length = raw[0] if len(raw) >= 1 else None
      # validate 1 <= length <= 31 and len(raw) >= 1 + length
      # ticker = raw[1:1+length].decode("ascii")
      # validate ticker.isprintable()
      ...

  def decode_symbol(hex_result):  # Optional[str]
      for fn in (_try_decode_abi_string,
                 _try_decode_bytes32_null_trimmed,
                 _try_decode_bytes32_length_prefixed):
          result = fn(hex_result)
          if result is not None:
              return result
      return None
  ```

  Each helper catches only `UnicodeDecodeError`, `ValueError`, and
  `IndexError` — not broad `Exception`. The `decode_symbol` outer
  function catches all remaining exceptions to preserve the
  `Optional[str]` + never-raises contract (architecture ADR-006).

- **Alternatives Considered:**

  - **Chase the full `d-xo/weird-erc20` catalog** (MANA, KNC, SNT, REP,
    etc.). Rejected per project plan R10: MANA and KNC use the same
    null-padded bytes32 as MKR and are already handled by Phase 1's
    null-trimmed fallback. REP and SAI return standard ABI `string`.
    The only genuinely distinct new format in the catalog that would
    otherwise return `None` is DGD-style length-prefixed bytes32.
    Chasing the full catalog adds variants that are already handled or
    where the delta is negligible.

  - **Single monolithic `decode_symbol` with nested branches.** Rejected:
    harder to test per-variant in isolation and harder to extend in
    future phases. The per-variant `_try_decode_*` helper shape matches
    the existing per-function test structure (ADR-011).

  - **Accept the Phase 1 broad `except Exception`.** Rejected: broad
    exception swallowing hides real bugs (e.g. `AttributeError` from a
    programming mistake). The polished implementation catches only the
    specific exceptions that genuinely indicate "this variant doesn't
    apply" per-helper, with a final broad catch only at the
    `decode_symbol` boundary.

- **Consequences:**

  - (+) DGD-style tokens that previously returned `None` (summary shows
    `(unavailable)`) now decode correctly.
  - (+) The ladder is finite and exhaustively tested. The
    "outside-the-catalog" test guards against silent future accretion.
  - (+) Each `_try_decode_*` helper is a pure function, independently
    testable in `TestAbiCodec` / `TestDecodeSymbolPolished`.
  - (+) The `Optional[str]` contract and never-raises guarantee are
    preserved end-to-end (architecture ADR-006).
  - (+) No new top-level imports. Reuses `bytes.fromhex`, slicing,
    `.decode("utf-8", "ignore")` / `.decode("ascii")` (PA-4 stdlib-only).
  - (-) The three-variant ladder adds ~40 lines to `abi_codec`. Acceptable
    for the scope of the improvement; ADR-002's "revisit if file exceeds
    ~800 lines" note still applies.

  Cross-references: PRD §P2.4 (source requirement); architecture ADR-006
  (the `Optional[str]` return contract that ADR-013 preserves); project
  plan R10 (scope-bound mitigation — "scope Task 3.2 to a finite list
  (MKR, DGD); stop when the catalog is exhausted").
