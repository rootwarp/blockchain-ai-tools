# Software Architecture: eth-tx-builder ERC-20 Extension (minimal-footprint candidate)

## Overview

This candidate optimizes for **smallest, simplest, lowest-regression change** to
extend the `eth-tx-builder` Claude Code skill with ERC-20 `transfer`, `approve`,
and `transfer-from` builders. The entire net delta is **one new helper file**
(`build_erc20.py`), **one new test file** (`test_build_erc20.py`), and two
prose-only edits to `SKILL.md` and `README.md`. The v1 ETH-send module
(`build_send_eth.py` and `test_build_send_eth.py`) is **never touched**, and the
new module **does not import from it** — the few v1 helpers it needs are
duplicated verbatim, gated behind a single one-line "DRIFT NOTE" comment.

The guiding principle is *minimum blast radius*: a code reviewer can verify the
change with a single-file diff plus a single-file test diff. There is no
extraction of a shared library, no edit to `build_send_eth.py`, no cross-file
import edge, no refactor anywhere. If the duplication ever becomes a maintenance
problem, an explicit migration path to a shared `_common.py` library is
documented in §ADR-002 and §Service Extraction Path — but the v1 architecture
already chose to keep `build_send_eth.py` standalone, and this candidate
preserves that posture.

## Architecture Principles

- **Bit-for-bit v1 preservation** — `build_send_eth.py` and
  `test_build_send_eth.py` MUST NOT change. This is a HARD constraint from the
  PRD (P0 §17, "Files NOT touched"). The architecture is shaped around it: no
  shared module is extracted, no import edge is created, no helper is
  parametrized. The v1 regression suite is the verification.
- **One file, one purpose** — `build_erc20.py` is the entire ERC-20 module. It
  contains its own CLI parser, ABI encoders, RPC helpers (duplicated from v1),
  decoders, gas/fee logic, and summary printer. The single-sentence
  responsibility: *Build a ready-to-sign EIP-1559 TxRequest JSON for an ERC-20
  transfer / approve / transferFrom*.
- **Stdlib only** — no `eth-abi`, no `pycryptodome`, no `web3.py`, no
  `requests`. Same set of imports v1 uses (`argparse`, `json`, `re`, `sys`,
  `urllib.request`) plus stdlib-only additions if the implementer wants them
  (none are required).
- **Pure-with-injected-rpc functional core** — `build_tx_*` and the encoder /
  decoder helpers are pure relative to an injected `rpc` callable. Tests stub
  `rpc`, matching v1's house style.
- **Error-and-stop, never silent fallback** — bad input, missing `decimals()`,
  `eth_estimateGas` failure: print `error:` to stderr, exit 1. No hidden default
  gas limit. (Research §3.1, PRD §9 — the no-fallback policy is what makes
  this safe.)
- **Warn-don't-block for non-fatal soft-checks** — `symbol()` failure,
  allowance soft-check below requested amount, `--approve-max`: print
  `WARNING:` to stderr, still emit JSON. (Research §1.6–1.8, PRD §10–11.)
- **Stdout = JSON only; stderr = humans only** — operators can pipe stdout
  into the signer; humans read the summary on stderr. (PRD §16, research §1.6.)
- **Deliberate duplication beats shared-state coupling for v1** — duplicating
  ~70 lines of v1 helpers is cheaper *in this single delta* than (a) editing
  `build_send_eth.py` to expose them as a public surface, (b) extracting a
  shared `_common.py` and updating both helpers' imports, or (c) introducing a
  package boundary. The PRD explicitly permits this trade-off (Tech
  Considerations: "MAY do so by importing... or it MAY duplicate them
  verbatim — implementer's choice"). See ADR-001 for the full rationale.

## System Context Diagram

```text
┌──────────────────┐     CLI args      ┌────────────────────────┐
│ Claude Code      │ ───────────────▶  │  build_erc20.py        │
│ skill operator   │                   │  (this candidate)      │
│ (or shell user)  │ ◀────── JSON ──── │  - parses CLI          │
└──────────────────┘   (stdout)        │  - ABI-encodes call    │
        │                              │  - duplicated v1 RPC   │
        │ ◀──── summary (stderr) ───── │    helpers + fee logic │
        │                              │  - reads decimals/     │
        ▼                              │    symbol/allowance    │
┌──────────────────┐                   │  - eth_estimateGas     │
│ eth-signer-mcp   │                   │  - emits TxRequest     │
│ (OFFLINE; signs  │                   └────────────────────────┘
│ TxRequest the    │                              │
│ operator pastes) │                              │ HTTPS JSON-RPC
└──────────────────┘                              ▼
        │                              ┌────────────────────────┐
        │ submits signed tx            │  publicnode.com        │
        ▼                              │  (mainnet / hoodi)     │
┌──────────────────┐                   │  - eth_call            │
│ eth-rpc /        │                   │  - eth_estimateGas     │
│ broadcaster      │                   │  - eth_getTrans...     │
│ (separate skill) │                   │  - eth_getBlock...     │
└──────────────────┘                   │  - eth_maxPriority...  │
                                       └────────────────────────┘

[unchanged, parallel path]
┌──────────────────┐                   ┌────────────────────────┐
│ Claude Code      │ ──── CLI args ──▶ │  build_send_eth.py     │
│ skill operator   │ ◀──── JSON ─────  │  (v1, BIT-FOR-BIT      │
│ (ETH-send path)  │  (stdout)         │   UNCHANGED)           │
└──────────────────┘                   └────────────────────────┘
```

The two helpers run in **separate Python processes** (different `python3` CLI
invocations chosen by the SKILL.md router). They share no memory, no imports,
no in-process state. The only "shared" thing is documentation
(`SKILL.md`/`README.md`) and the publicnode RPC endpoints.

## Module Overview

| Module | Responsibility | Owns Data | Depends On | Communication |
|--------|----------------|-----------|------------|---------------|
| `build_send_eth.py` (v1, unchanged) | Build a ready-to-sign EIP-1559 native ETH transfer TxRequest. | None (stateless; queries RPC) | publicnode RPC | sync (HTTPS JSON-RPC), CLI in/out |
| `build_erc20.py` (NEW) | Build a ready-to-sign EIP-1559 ERC-20 transfer/approve/transferFrom TxRequest. | None (stateless; queries RPC) | publicnode RPC | sync (HTTPS JSON-RPC), CLI in/out |
| `SKILL.md` (edited, prose only) | Router doc: tell the Claude Code agent which helper to invoke and with which inputs. | N/A (prose) | `build_send_eth.py`, `build_erc20.py` (named in prose) | docs only |
| `README.md` (edited, prose only) | File list and manual end-to-end checklist (hoodi e2e). | N/A (prose) | both helpers (named in prose) | docs only |
| `test_build_send_eth.py` (v1, unchanged) | Regression suite for the v1 ETH-send module. | N/A | `build_send_eth` (import only) | in-process import |
| `test_build_erc20.py` (NEW) | Regression suite for the ERC-20 module. | N/A | `build_erc20` (import only) | in-process import |

**Important:** `build_erc20.py` does **not** appear in `build_send_eth.py`'s
"Depends On" column, and `build_send_eth.py` does **not** appear in
`build_erc20.py`'s. They are siblings, not parent/child. The duplicated
helpers live inside `build_erc20.py` as private functions with a single
"DRIFT NOTE" comment block at the top.

## Module Dependency Graph

```text
build_send_eth.py  ─────────▶  publicnode RPC  (HTTPS JSON-RPC)
       ▲
       │ (import only)
       │
test_build_send_eth.py


build_erc20.py     ─────────▶  publicnode RPC  (HTTPS JSON-RPC)
       ▲
       │ (import only)
       │
test_build_erc20.py


SKILL.md (prose) ─names─▶ build_send_eth.py
                 └names─▶ build_erc20.py

README.md (prose) ─names─▶ build_send_eth.py
                  └names─▶ build_erc20.py
```

- **No circular dependencies.** `build_erc20.py` does not import
  `build_send_eth.py` (or vice versa). The only inbound edge to either helper
  is from its own test file (a unit-test import).
- **No shared mutable state.** Two separate `python3` processes; no
  in-process channel between them.
- **The SKILL.md "router" is prose**, not a runtime import — it is read by
  the Claude Code agent, which chooses which helper to invoke. There is no
  Python-level coupling.

## Module Details

### Module: `build_erc20.py` (NEW)

**Responsibility:** Build a ready-to-sign EIP-1559 TxRequest JSON for an ERC-20
`transfer`, `approve`, or `transferFrom` operation against `mainnet` or `hoodi`,
in a single self-contained script that touches no other source file.

**Domain Entities:**
- `TxRequest` — the output JSON shape the `eth-signer-mcp` `sign_transaction`
  tool accepts (`type`, `chainId`, `nonce`, `to`, `value`, `data`, `gas`,
  `maxFeePerGas`, `maxPriorityFeePerGas`). Same shape as v1, with
  ERC-20-specific values in `to`/`value`/`data`/`gas`.
- `Network` — `mainnet` or `hoodi` (chain id + RPC URL pair). Duplicated map
  from v1 (see ADR-001).
- `TokenMetadata` — in-memory record built from `decimals()` (fatal on failure)
  and `symbol()` (best-effort). Not persisted.
- `Allowance` (transfer-from only) — soft-check result; integer base-unit
  uint256.

**Data Store:**
- None. The module is stateless. Inputs come from CLI; outputs go to stdout
  (JSON) and stderr (summary + warnings). RPC responses are read-only.

**Public API (interface to other modules / agents):**

CLI is the public contract. There is no in-process import API.

| Subcommand | Required flags | Output (stdout) | Output (stderr) | Exit |
|---|---|---|---|---|
| `transfer` | `--network --token --to --amount --sender` | `TxRequest` JSON | summary | 0 |
| `approve` | `--network --token --spender --sender` + (`--amount` ⊕ `--approve-max`) | `TxRequest` JSON | summary + (warning if `--approve-max`) | 0 |
| `transfer-from` | `--network --token --from --to --amount --sender` | `TxRequest` JSON | summary + (warning if low allowance) | 0 |
| any | bad input / RPC error / estimate failure | (nothing) | `error: <msg>` | 1 |

**Internal Structure** (single file, sectioned by comment banners):

```
build_erc20.py
├── #!/usr/bin/env python3 + docstring
├── imports: argparse, json, re, sys, urllib.request
├── # --- DRIFT NOTE -----------------------------------------------------
│   # The next block is duplicated VERBATIM from build_send_eth.py.
│   # If you change either copy, change the other or extract _common.py.
│   # The PRD HARD-FORBIDS editing build_send_eth.py for this delta.
│   # ---------------------------------------------------------------------
├── NETWORKS                       # from v1 (mainnet, hoodi)
├── DEFAULT_TIP_WEI                # from v1 (1 gwei)
├── USER_AGENT                     # from v1 ("eth-tx-builder/1.0")
├── network_config()               # from v1
├── validate_hex_address()         # from v1
├── parse_hex_int()                # from v1
├── compute_max_fee()              # from v1
├── class RPCError                 # from v1
├── rpc_call()                     # from v1
├── fetch_nonce()                  # from v1
├── fetch_base_fee()               # from v1
├── fetch_tip()                    # from v1
├── # --- END DRIFT NOTE BLOCK -------------------------------------------
├──
├── # --- ERC-20 selectors (hardcoded; no keccak dep) ---------------------
├── SEL_TRANSFER       = "0xa9059cbb"     # transfer(address,uint256)
├── SEL_APPROVE        = "0x095ea7b3"     # approve(address,uint256)
├── SEL_TRANSFER_FROM  = "0x23b872dd"     # transferFrom(address,address,uint256)
├── SEL_DECIMALS       = "0x313ce567"     # decimals()
├── SEL_SYMBOL         = "0x95d89b41"     # symbol()
├── SEL_ALLOWANCE      = "0xdd62ed3e"     # allowance(address,address)
├── MAX_UINT256        = (1 << 256) - 1
├── MAX_DECIMALS       = 36                # PRD cap (research §1.4)
├── GAS_BUFFER_NUMER   = 12
├── GAS_BUFFER_DENOM   = 10
├── GAS_CAP            = 300_000
├──
├── # --- ABI encode / decode helpers (stdlib only) ----------------------
├── _encode_address(addr)          # 0x + 24 zeros + 40 lowercase hex
├── _encode_uint256(n)             # reject < 0 or >= 2**256; 64-hex left-pad
├── _pack_call(selector, *words)   # "0x" + selector[2:] + ''.join(words)
├──
├── _decode_uint256_word(hex_data, word_idx=0)
├── _decode_decimals(hex_data)     # int(word,16) & 0xff; cap MAX_DECIMALS
├── _decode_symbol_string(hex_data) # std ABI string; raises on malformed
├── _decode_symbol_bytes32(hex_data) # null-trimmed UTF-8 fallback (MKR)
├── _decode_symbol(hex_data)       # try string, fall back to bytes32, else None
├──
├── # --- amount string-math conversion (no float) -----------------------
├── _parse_amount(human, decimals)  # str→str→int, rejects negatives,
│                                   # multi-dot, non-digits, too-many-frac
├──
├── # --- RPC reads against the token ------------------------------------
├── fetch_decimals(rpc, url, token)        # fatal on failure
├── fetch_symbol(rpc, url, token)          # returns str or None
├── fetch_allowance(rpc, url, token, holder, spender)  # returns int or None
├──
├── # --- ERC-20 calldata builders --------------------------------------
├── build_transfer_data(to, amount_base)
├── build_approve_data(spender, amount_base)
├── build_transfer_from_data(from_, to, amount_base)
├──
├── # --- gas estimation (no fallback) ----------------------------------
├── estimate_gas(rpc, url, sender, token, data)
│   # buffer (est*12)//10, cap at GAS_CAP; RPCError propagates (FATAL)
├──
├── # --- summary printer (stderr only) --------------------------------
├── _print_summary(op, network, chain_id, token, symbol, decimals,
│                  human_amount, base_amount, nonce, gas, max_fee, tip,
│                  ...op-specific roles...)
├──
├── # --- top-level builders (pure relative to rpc) ---------------------
├── do_transfer(args, rpc=rpc_call)
├── do_approve(args, rpc=rpc_call)
├── do_transfer_from(args, rpc=rpc_call)
├──
├── # --- CLI -----------------------------------------------------------
├── main(argv=None)
│   ├── argparse: top-level parser + 3 subparsers
│   ├── dispatch to do_transfer / do_approve / do_transfer_from
│   └── error-and-stop on (ValueError, RPCError)
├── if __name__ == "__main__": sys.exit(main())
```

**Key Design Decisions** (full ADRs below):
- ADR-001: Duplicate v1 helpers verbatim (do not import, do not edit
  `build_send_eth.py`).
- ADR-002: Single-file module; no `_common.py` extraction.
- ADR-003: Pure-function `do_*` builders with injected `rpc` (testability;
  matches v1 house style).
- ADR-004: Module-level hardcoded ERC-20 selectors; no keccak runtime dep.
- ADR-005: `decimals()` is fatal-on-failure; `symbol()` is best-effort;
  allowance soft-check is best-effort. Reconciles ABI vs gas angles
  (Research overview §2).
- ADR-006: `eth_estimateGas` failure is fatal — no silent fallback.
- ADR-007: Integer-only amount conversion (no `float`); reject any path that
  would call `float()` on a token amount.
- ADR-008: Stdout = JSON, stderr = summary + warnings. Operators may pipe
  stdout into the signer.

**Failure Modes:**
- **Bad CLI input (malformed address, malformed amount, both `--amount` and
  `--approve-max`, neither, etc.)** → argparse / explicit `ValueError` →
  `error: <msg>` to stderr, exit 1. No JSON emitted.
- **`decimals()` RPC failure or returns > `MAX_DECIMALS`** →
  `error: token decimals() ...` to stderr, exit 1. No JSON. (Fatal: the
  base-unit amount cannot be computed without it.)
- **`symbol()` failure** → summary prints `(symbol unavailable)`; build
  continues. (Best-effort.)
- **`eth_estimateGas` failure (revert / RPC transport)** → `error:
  eth_estimateGas failed: ...` to stderr, exit 1. No JSON. **NO FALLBACK to
  a hardcoded gas limit** (PRD §9; research §3 §5).
- **`allowance()` failure on `transfer-from`** → `WARNING: allowance
  soft-check skipped: ...` to stderr; build continues, JSON emitted.
- **`allowance()` returns less than requested** → `WARNING: current
  allowance is N (human); requested transfer is M (human). This tx will
  revert unless allowance is increased before broadcast.` to stderr; build
  continues, JSON emitted. (Multi-step workflows are legitimate; PRD §11.)
- **Public RPC outage (publicnode.com unreachable)** → each `rpc_call` raises
  `RPCError`; whichever RPC step needs it propagates as fatal error and
  exits 1. No retry / cache.
- **Process killed mid-build** → no state persisted; rerun is idempotent.
  The skill never partially-emits a TxRequest (every successful build
  writes the JSON in one `print()`).

**Reliability / SLO posture:** none claimed. The helper is a one-shot
CLI; if it fails the operator re-runs. Six sequential RPC reads, each
bounded by a 15-second timeout (inherited from the duplicated
`rpc_call`).

### Module: `build_send_eth.py` (v1, UNCHANGED)

**Responsibility:** Build a ready-to-sign EIP-1559 TxRequest JSON for a
native ETH value transfer on `mainnet` or `hoodi`.

**Hard constraint (PRD P0 §17):** This file MUST NOT be edited as part of
the ERC-20 delta. Its bytes are unchanged; its test suite
(`test_build_send_eth.py`) is unchanged; both continue to pass as the
v1 regression check.

**Interaction with `build_erc20.py`:** none at runtime. The two helpers
are sibling processes invoked by the SKILL.md router. There is **no
import edge**, **no shared module**, **no shared state**.

(All other module-detail fields — Domain Entities, Data Store, Public API,
Internal Structure, etc. — are the v1 architecture's, unchanged. Refer to
the v1 source as the source of truth.)

### Module: `SKILL.md` (edited; prose-only)

**Responsibility:** Tell the Claude Code agent (a) the inputs each operation
needs and (b) which Python helper to invoke.

**Edits:**
- Description string broadened: `"...build an Ethereum transaction (native ETH
  transfer OR ERC-20 transfer/approve/transferFrom)..."`
- "Inputs" section split into two subsections:
  - "Inputs — native ETH send" (existing content, unchanged).
  - "Inputs — ERC-20 transfer / approve / transferFrom" (new): token address;
    `--to` (transfer / transfer-from), `--spender` (approve), `--from`
    (transfer-from); amount (human-readable) or `--approve-max`.
- "Procedure" gains a top step:
  1. Identify intent: native ETH transfer → use `build_send_eth.py` (existing
     procedure unchanged).
  2. Identify intent: ERC-20 transfer / approve / transfer-from → use
     `build_erc20.py` with the chosen subcommand.
- "Out of scope (v1)" updated: ERC-20 removed; explicit list of new
  non-goals (permit, ERC-721/1155, swaps, multi-token batch,
  fee-on-transfer / rebasing handling, gasless meta-tx, signing,
  broadcasting).

**No runtime contract change** for the existing ETH path: the Claude Code
agent reaching the "send ETH" branch still calls `build_send_eth.py` with
the exact arguments it does today.

### Module: `README.md` (edited; prose-only)

**Responsibility:** File-list orientation + manual end-to-end checklist for
operators.

**Edits:**
- File list adds `build_erc20.py` and `test_build_erc20.py` rows.
- New "Manual end-to-end (hoodi)" section: three runs (transfer, approve,
  transfer-from) against a real ERC-20 deployed on hoodi, with a
  paste-to-signer step at the end.

### Module: `test_build_erc20.py` (NEW)

**Responsibility:** Unit + integration regression coverage for
`build_erc20.py`.

**Internal Structure:**
- `class TestSelectorEncoding` — bit-pattern checks for each of the three ops
  against a known-good hex string (use the USDC transfer vectors from
  Research §01-abi-encoding).
- `class TestUint256Encoding` — boundaries (0, 1, 2**256-1, reject negatives,
  reject 2**256).
- `class TestAddressEncoding` — mixed-case input, lowercase output, padding.
- `class TestAmountParsing` — 0, 0.0, 1, 1.5, 0.000001, max-decimals,
  too-many-frac rejected, negatives rejected, multi-dot rejected, non-digit
  rejected, empty rejected.
- `class TestDecimalsDecode` — 0, 6, 18, 24 OK; 37 rejected; RPC error
  rejected.
- `class TestSymbolDecode` — standard string OK; bytes32 fallback (MKR);
  malformed → None.
- `class TestGasEstimate` — `(est * 12) // 10` math; cap at 300_000;
  RPC error propagates (no fallback).
- `class TestDoTransfer` — happy path with mocked `rpc`; verifies output
  TxRequest shape matches v1's shape with ERC-20 values.
- `class TestDoApprove` — happy path; `--approve-max` produces MAX_UINT256
  data and emits the warning.
- `class TestDoTransferFrom` — happy path; allowance low warning;
  allowance RPC error warning; both still emit JSON.
- `class TestEstimateGasFailureFatal` — RPC error from `eth_estimateGas`
  is fatal; no JSON; exit code 1.
- `class TestAddressValidation` — each of `--token`, `--to`, `--spender`,
  `--from`, `--sender` rejects bad input.
- `class TestCli` — argparse smoke; `--help` for each subcommand; top-level
  `--help` lists all three.

**Test dependencies:** `import build_erc20 as b` only. Does NOT import
`build_send_eth`. Uses `unittest.mock` and a stub `rpc` callable, exactly
matching v1's test posture.

### Module: `test_build_send_eth.py` (v1, UNCHANGED)

**Responsibility:** v1 regression suite. Must continue to pass byte-for-byte
unchanged. The success criterion is: after the ERC-20 delta lands,
`python3 -m unittest test_build_send_eth -v` still passes.

## Cross-Cutting Concerns

### Authentication & Authorization

None at the helper layer. The helper does not hold keys, does not sign, does
not broadcast. Identity (the sending account) is supplied as the `--sender`
CLI arg. The signer (`eth-signer-mcp`) is a separate offline process that
holds keys and performs signing downstream. The two concerns stay separate.

### Logging & Observability

- **Stdout = TxRequest JSON only.** Operators may pipe stdout into the
  signer; nothing else must appear there.
- **Stderr = humans only.** Two prefix conventions:
  - `error: <msg>` — fatal, exit 1, no JSON emitted (matches v1).
  - `WARNING: <msg>` — soft, exit 0, JSON still emitted. Used for
    `--approve-max`, low allowance, symbol unavailable, allowance RPC
    skipped.
- **No structured logging, no JSON logs, no log levels.** This is a
  one-shot CLI; stderr text is the entire observability surface. Matches v1.
- **No metrics, no traces, no error reporting service.** Out of scope for a
  stdlib-only single-file helper.

### Error Handling

- Common shape: every fatal path raises a Python `ValueError` (input /
  validation) or `RPCError` (RPC transport / response). The single top-level
  `main()` `try/except` converts both into `error: <msg>` on stderr + exit 1.
- Warnings are direct `sys.stderr.write("WARNING: ...\n")` calls inside the
  `do_*` functions; they do NOT raise.
- No exception types cross the module boundary, because there is no module
  boundary to cross — everything is internal.

### Configuration

- **All configuration is CLI flags.** No env vars, no config files, no
  feature flags. Matches v1 exactly. Adding env-var support would be a
  scope expansion this candidate explicitly rejects.
- **Networks** (`mainnet` / `hoodi`) and their RPC URLs are hardcoded in
  the `NETWORKS` constant inside `build_erc20.py` (duplicated from v1, per
  ADR-001). Sepolia/holesky are PRD P1 — a future delta adds them by editing
  two lines of the duplicated map (and the v1 map separately, when v1 picks
  them up).

### Code Reuse Strategy (the central cross-cutting decision)

The PRD's Tech Considerations explicitly leaves the import-vs-duplicate
question to architecture stage. This candidate **chooses duplicate**, for
five reasons:

1. **Bit-for-bit constraint compatibility.** Importing from
   `build_send_eth.py` is technically possible without editing it — the
   helpers it would expose (`NETWORKS`, `rpc_call`, `fetch_nonce`,
   `fetch_base_fee`, `fetch_tip`, `compute_max_fee`, `validate_hex_address`,
   `parse_hex_int`, `RPCError`) are already module-level. But adding an
   import edge means `build_erc20.py` depends on v1 for correctness, which
   subtly couples them: a future v1 refactor (renaming a helper, changing
   a signature, adding side effects to module load) could break v1's
   sibling silently. Duplication isolates the two.
2. **Single-file review property.** A reviewer of this delta opens exactly
   two new files (`build_erc20.py`, `test_build_erc20.py`) and confirms the
   v1 files are bit-identical. That is the simplest possible review.
3. **No `__init__.py` / package layout.** `.claude/skills/eth-tx-builder/`
   is a flat directory of scripts. Avoiding cross-script imports keeps it
   that way; no new directory shape, no `__init__.py`, no module-search-path
   subtleties.
4. **PRD explicitly permits it.** "...or it MAY duplicate them verbatim —
   implementer's choice, as long as `build_send_eth.py` stays bit-for-bit
   unchanged."
5. **Migration path is explicit and cheap when needed.** When (and only
   when) a third helper needs the same plumbing — `build_send_erc721.py`,
   say — extract `_common.py` then. With two helpers, the duplication is
   ~70 lines; with three or more, the case for a shared module flips.
   See ADR-002 and Service Extraction Path.

The cost of duplication is one fragile place: if someone "fixes" `rpc_call`
in v1 and forgets `build_erc20.py`, drift bug. Mitigations:
- **Single DRIFT NOTE comment block** in `build_erc20.py` headers the
  duplicated region with: *"This block is duplicated VERBATIM from
  build_send_eth.py as of YYYY-MM-DD. If you change either copy, change
  the other or extract `_common.py`."*
- **Project-plan task** to revisit duplication at the start of any phase
  that adds a third helper.
- **No further mitigation in v1.** A `make verify-duplicate` lint script
  would itself be a scope expansion; the comment block is enough for two
  helpers.

## Data Flow Diagrams

### Flow 1 — `transfer` (happy path)

```text
operator shell ──cli args──▶ build_erc20.py main()
                                   │
                                   ▼
                             argparse → args
                                   │
                                   ▼
                             do_transfer(args, rpc=rpc_call)
                                   │
                       ┌───────────┼───────────┬──────────────┐
                       │           │           │              │
                       ▼           ▼           ▼              ▼
            validate_hex_address  network_   _parse_amount  fetch_decimals
            (token, to, sender)   config       (deferred)   ──RPC──▶ publicnode
                       │           │           │              │
                       └───────────┴───────────┴──────────────┘
                                   │
                          decimals OK → _parse_amount(human, decimals) → base_amount
                                   │
                                   ▼
                          build_transfer_data(to, base_amount) → calldata
                                   │
                       ┌───────────┴───────────┐
                       ▼                       ▼
            fetch_nonce          fetch_base_fee + fetch_tip
            ──RPC──▶ publicnode  ──RPC──▶ publicnode (block + maxPriority)
                       │                       │
                       └───────────┬───────────┘
                                   ▼
                          fetch_symbol(token) ──RPC──▶ publicnode
                          (best-effort; failure → None)
                                   │
                                   ▼
                          estimate_gas(sender, token, calldata)
                          ──RPC──▶ publicnode (eth_estimateGas)
                          (failure → error+exit 1, NO FALLBACK)
                                   │
                                   ▼
                          buffer = (est * 12) // 10; cap 300_000
                                   │
                                   ▼
                          compute_max_fee(base, tip)
                                   │
                                   ▼
                          assemble TxRequest dict
                                   │
                       ┌───────────┴───────────┐
                       ▼                       ▼
            print(json.dumps(tx))   _print_summary(...) → stderr
            → stdout
                       │
                       ▼
                   exit 0
```

### Flow 2 — `approve --approve-max` (warning path)

```text
operator shell ──cli args──▶ build_erc20.py main()
                                   │
                                   ▼
                             do_approve(args, rpc=rpc_call)
                                   │
                       (skip _parse_amount; use MAX_UINT256 directly)
                                   │
                       (still calls fetch_decimals + fetch_symbol so
                        the warning can name the token by symbol)
                                   │
                                   ▼
                          build_approve_data(spender, MAX_UINT256)
                                   │
                                   ▼
                          [nonce + fees + estimate, same as Flow 1]
                                   │
                                   ▼
                          stderr ──▶ "WARNING: --approve-max grants
                                       UNLIMITED transfer authority on
                                       <SYM> (<token>) to spender
                                       <spender>. Revoke later with
                                       approve(spender, 0)."
                                   │
                                   ▼
                          stdout ──▶ TxRequest JSON
                          stderr ──▶ summary block
                                   │
                                   ▼
                                exit 0
```

### Flow 3 — `transfer-from` with low allowance

```text
operator shell ──cli args──▶ build_erc20.py main()
                                   │
                                   ▼
                             do_transfer_from(args, rpc=rpc_call)
                                   │
                       [decimals + parse_amount + symbol + nonce + fees]
                                   │
                                   ▼
                          fetch_allowance(token, holder=--from,
                                          spender=--sender) ──RPC──▶ node
                          (failure → WARNING: soft-check skipped;
                           returned < requested → WARNING: tx will
                           revert unless allowance increased)
                                   │
                                   ▼
                          build_transfer_from_data(from, to, base_amount)
                                   │
                                   ▼
                          estimate_gas(...) ──RPC──▶ node
                          (success required; failure is fatal)
                                   │
                                   ▼
                          stdout ──▶ TxRequest JSON
                          stderr ──▶ summary + (any WARNINGs)
                                   │
                                   ▼
                                exit 0
```

### Flow 4 — `eth_estimateGas` failure (no-fallback policy)

```text
do_transfer / do_approve / do_transfer_from
                   │
                   ▼
        estimate_gas(...) ──RPC──▶ publicnode
                   │
                   ▼
            RPC returns code=3 + revert data
                   │
                   ▼
            rpc_call() raises RPCError
                   │
                   ▼  (propagates up; do NOT catch)
            main()'s top-level except
                   │
                   ▼
            stderr ──▶ "error: eth_estimateGas failed: <node msg>"
            (NO TxRequest JSON on stdout; NO hardcoded gas fallback)
                   │
                   ▼
                exit 1
```

## Infrastructure & Deployment

### Deployment Model

- **Polyrepo-of-one.** The skill lives at
  `.claude/skills/eth-tx-builder/`. Two scripts side by side, each a flat
  Python file with no package boundary. There is no build step, no
  packaging, no distribution.
- **Runtime:** Python 3.8+ stdlib only. Verified with
  `python3 build_erc20.py --help` on a fresh Python install (PRD success
  metric "Zero new dependencies").
- **Invocation:** SKILL.md routes the Claude Code agent to invoke
  `python3 build_erc20.py <subcommand> ...` (or `python3 build_send_eth.py
  ...` for the v1 path). Operators may also run the script directly from
  a shell.
- **No daemon, no server, no scheduled job.** Each invocation is one
  process, six-or-so RPC calls, one stdout JSON, exit.

### Scaling Strategy

Not applicable. The helper is a one-shot CLI; load is whatever the operator
generates. Bottleneck is the public RPC endpoint, which is shared with
every other publicnode user and out of our control.

### Service Extraction Path

This candidate is deliberately *un-extracted*. Each helper is already a
standalone runnable. The "future microservice" question reduces to "when
do we extract a shared library, not a service":

| Module | Extraction posture | When to extract |
|---|---|---|
| `build_erc20.py` | **Keep as single file.** Ready to extract `_common.py` immediately if a third helper lands. | A third helper (e.g. `build_send_erc721.py` for ERC-721) needs the same network map / RPC / fee plumbing. |
| `build_send_eth.py` | **Untouched.** Will be retrofitted to import from `_common.py` *at the same time* the third helper lands — not before. | Same trigger as above. The PRD's no-edit constraint expires once a real call for shared code arrives. |
| Hypothetical `_common.py` | **Does not exist in v1.** Would expose: `NETWORKS`, `RPCError`, `rpc_call`, `fetch_nonce`, `fetch_base_fee`, `fetch_tip`, `compute_max_fee`, `validate_hex_address`, `parse_hex_int`, `USER_AGENT`, `DEFAULT_TIP_WEI`. | At extraction time, write the file, point both helpers' imports at it, run both test suites, ship the diff. ~30 line addition + ~70 line deletion across the two helpers. |
| Hypothetical FFI / network service | **Out of scope, indefinitely.** No reason to extract a service from a one-shot CLI. | N/A. |

The duplicated code IS the extraction-ready surface: every duplicated
helper is already a module-level function with a stable signature.
Extracting later is mechanical, not redesign work.

## Technology Choices

| Concern | Choice | Rationale |
|---------|--------|-----------|
| Language | Python 3.8+ | Matches v1 helper. House style. Stdlib has everything we need. |
| Imports allowed | `argparse`, `json`, `re`, `sys`, `urllib.request` | The exact v1 set. Zero new deps. |
| ABI encoding | Hand-rolled, hex-string concatenation | Stdlib only; selectors are hardcoded constants (no keccak runtime dep). Research §1.1 verifies all six selectors. |
| Keccak | None (no runtime hashing) | Selectors precomputed; one-line comment with `keccak256(sig)` for each. |
| HTTP client | `urllib.request` | What v1 uses. publicnode requires an explicit User-Agent; reuse v1's `USER_AGENT` constant. |
| JSON | `json` stdlib | Matches v1. |
| Address regex | `^0x[0-9a-fA-F]{40}$` | Matches v1's `_ADDR_RE`. Checksum enforcement is downstream in the signer. |
| Integer math for amounts | `int.to_bytes`, `bytes.fromhex`, `int(hex, 16)` | No float; explicit string-split for decimal-point handling. PRD non-negotiable. |
| Test framework | `unittest` (stdlib) | Matches v1 (`test_build_send_eth.py`). |
| Mocking | `unittest.mock` (stdlib) | Matches v1. |
| Lint / format | None added | The repo's Go lint config does not cover Python; consistent with v1 (no Python lint pre-existing). Operators can run `python3 -m py_compile` ad-hoc. |
| CI | Existing `make test` (Go-focused) is unchanged; v1 and ERC-20 test files run via `python3 -m unittest test_build_send_eth -v` and `python3 -m unittest test_build_erc20 -v` from the skill dir | Two extra manual commands in PR review; same as the existing manual v1 verification. A `make` target wiring is out of scope for the minimal-footprint candidate. |

## ADRs (Architecture Decision Records)

### ADR-001: Duplicate v1 helpers in `build_erc20.py`; do NOT import from `build_send_eth.py`

- **Status:** Accepted.
- **Context:** The ERC-20 helper needs the v1 network map, EIP-1559 fee
  logic, nonce fetch, address validation, hex parsing, and `RPCError`. The
  PRD HARD-FORBIDS editing `build_send_eth.py`. Three options exist:
  (a) import from `build_send_eth.py` (an import edge but no edits),
  (b) extract `_common.py` (requires editing v1 to import from it,
  violating the hard constraint), (c) duplicate the helpers in
  `build_erc20.py`.
- **Decision:** (c) — duplicate. A single DRIFT NOTE comment block heads
  the duplicated region.
- **Alternatives Considered:**
  - **(a) Import.** Allowed under PRD wording, but creates a runtime
    coupling that complicates v1's "standalone, no internal deps" promise.
    The next refactor of v1 — even a benign rename — could break the ERC-20
    helper silently. Rejected for minimum-footprint posture (the import edge
    *is* a footprint).
  - **(b) Extract `_common.py`.** The cleanest long-term shape, but requires
    editing `build_send_eth.py` to consume the shared module — directly
    violating the hard constraint. Rejected. Reconsider when the constraint
    is lifted (e.g. a third helper lands).
- **Consequences:**
  - **+** Net change is exactly two new files + two prose edits; no edit
    to any existing source file.
  - **+** Single-file review property: a reviewer never has to chase a
    cross-file behavior.
  - **+** v1 path cannot regress because of the ERC-20 delta — there is no
    runtime touchpoint.
  - **−** Two copies of ~70 lines of helpers. Future drift risk if someone
    "fixes" one and forgets the other. Mitigated by the DRIFT NOTE comment
    and the explicit migration path in §Service Extraction Path.

### ADR-002: Single-file `build_erc20.py`; no package, no `_common.py`

- **Status:** Accepted.
- **Context:** Some implementations would extract ABI encoders, RPC
  helpers, summary printers into separate submodules. The skill directory
  is currently a flat list of scripts (no `__init__.py`, no package).
- **Decision:** Keep `build_erc20.py` as a single file with comment-banner
  sections. Do not introduce a package, do not split into multiple files.
- **Alternatives Considered:**
  - **Sub-package** (`erc20/__init__.py`, `erc20/encode.py`,
    `erc20/decode.py`, `erc20/cli.py`). Rejected as scope inflation for a
    ~500-line module; introduces directory shape; complicates the SKILL.md
    invocation example (`python3 -m erc20 transfer ...`).
  - **Helper file** (`_erc20_helpers.py`) alongside `build_erc20.py`.
    Rejected — adds a file with no clear ownership boundary; the
    encode/decode/RPC helpers are tightly coupled to the do_* functions
    anyway.
- **Consequences:**
  - **+** Minimum file count; minimum review surface.
  - **+** Mirrors v1 (`build_send_eth.py` is also one file).
  - **−** `build_erc20.py` is ~3× the size of v1 (≈500 lines vs ≈170).
    Acceptable for a single CLI with clear section banners; revisit if
    it exceeds ~800 lines or a third helper lands.

### ADR-003: Pure-function `do_*` builders with injected `rpc` callable

- **Status:** Accepted.
- **Context:** v1's `build_tx_request` accepts `rpc=rpc_call` as a default
  argument and is testable by passing a stub. The PRD requires the same
  testability for ERC-20 (P0 §20, "happy-path `do_transfer` / `do_approve`
  / `do_transfer_from` with mocked RPC").
- **Decision:** Each of `do_transfer`, `do_approve`, `do_transfer_from`
  takes `args` (parsed argparse Namespace) and an `rpc=rpc_call` default;
  tests pass a `unittest.mock.Mock()` stub. All sub-helpers
  (`fetch_decimals`, `fetch_symbol`, `fetch_allowance`, `estimate_gas`,
  duplicated `fetch_nonce` / `fetch_base_fee` / `fetch_tip`) receive `rpc`
  as a parameter; none import `rpc_call` lexically.
- **Alternatives Considered:**
  - **Class-based session** (`RpcSession` with methods). Rejected — class
    overhead with no shared state.
  - **Module-level global override** (monkey-patch `rpc_call` in tests).
    Rejected — fragile, leaks across tests, doesn't match v1.
- **Consequences:**
  - **+** Tests stay fast (no network), match v1 style exactly, no fixtures
    required.
  - **+** Easy to swap rpc transport later (httpx, async) without changing
    the function bodies — though no such swap is planned.
  - **−** Function signatures all carry `rpc` as the last param; minor
    verbosity.

### ADR-004: Hardcoded selectors; no runtime keccak

- **Status:** Accepted.
- **Context:** The six ERC-20 selectors are publicly known and
  cross-verified against four primary sources (Solidity ABI spec, EIP-20,
  `metachris/eth-go-bindings`, `4byte.directory`). Computing them at
  runtime requires a keccak256 implementation, which is not in the Python
  stdlib (`hashlib.sha3_256` is *not* keccak; SHA-3 finalisation differs).
- **Decision:** Module-level hex-string constants
  (`SEL_TRANSFER = "0xa9059cbb"`, etc.), each with a one-line derivation
  comment naming the canonical signature. Selectors and a one-line
  collision caveat live in the comment for future-proofing decoder work.
- **Alternatives Considered:**
  - **Vendor a pure-Python keccak.** Rejected — violates "stdlib only"
    PRD constraint.
  - **Compute via subprocess to a system tool.** Rejected — adds
    runtime dependency on an external binary, non-portable.
- **Consequences:**
  - **+** Stdlib-only constraint satisfied.
  - **+** Tests can compare selectors against hardcoded strings byte-for-byte.
  - **−** New ERC-20 read in the future (e.g. `balanceOf`) requires a
    new module-level constant + comment. Trivial maintenance.

### ADR-005: `decimals()` is fatal-on-failure; `symbol()` and allowance soft-check are best-effort

- **Status:** Accepted.
- **Context:** Research overview §2 calls out the apparent conflict
  between the ABI angle ("decimals is best-effort per EIP-20 OPTIONAL")
  and the gas angle ("estimate failure is fatal"). The reconciliation:
  reads that the **correctness of calldata or gas** depends on are
  fatal; reads that only **enrich the human-readable summary** degrade
  gracefully.
- **Decision:**
  - **Fatal:** `decimals()` (drives base-unit amount), `eth_estimateGas`
    (drives `gas` field), all explicit `validate_hex_address` calls.
  - **Best-effort with warning:** `symbol()` (summary only), `allowance`
    soft-check on `transfer-from` (informational guard).
  - **`--approve-max` warning** prints unconditionally before the JSON.
- **Alternatives Considered:**
  - **All-fatal.** Rejected — `symbol()` failure shouldn't block a
    legitimate build (the operator already supplied the verified token
    address); allowance failure shouldn't block multi-step
    approve→transferFrom workflows.
  - **All-best-effort.** Rejected — a missing/bogus `decimals()` would
    silently corrupt the amount conversion; the PRD's stated risk
    posture (P0 §6, §9) rejects this.
- **Consequences:**
  - **+** Aligns with research-overview principle.
  - **+** Matches PRD acceptance criteria exactly (P0 §6, §9, §10, §11).
  - **−** Two distinct stderr conventions to keep straight (`error:`
    vs `WARNING:`); covered by tests.

### ADR-006: `eth_estimateGas` failure is fatal — NO hardcoded fallback

- **Status:** Accepted.
- **Context:** Research §03-gas-estimation §"Why a silent hardcoded-gas
  fallback is dangerous": a failing estimate means the simulated call
  reverts; the on-chain replay will revert; the operator burns the full
  gas budget. The PRD §9 is explicit. A future maintainer might be
  tempted to add `gas = 100_000` "for robustness."
- **Decision:**
  - On `eth_estimateGas` failure, propagate `RPCError` to `main()`'s
    top-level except; print `error: eth_estimateGas failed: <node msg>`
    to stderr; exit 1. No JSON on stdout.
  - The rationale lives in a multi-line comment at the `estimate_gas`
    function so a future maintainer reads it before "fixing" the
    fragility. Comment matches the example in research §03 §"Correct
    error surfacing — NO fallback".
- **Alternatives Considered:**
  - **Fall back to 300_000 (the cap).** Rejected — the cap is a ceiling,
    not a guess; using it as a default disguises real errors.
  - **Fall back per-op (e.g., 65k for `transfer`).** Rejected — same
    failure mode at on-chain replay; per-op fallback only delays the
    revert by hiding the diagnostic.
- **Consequences:**
  - **+** Operators get the node's revert reason at build time (free)
    rather than at broadcast time (expensive).
  - **+** Matches every reputable wallet's posture (no silent estimate
    bypass).
  - **−** If publicnode rate-limits the estimate but the call would
    succeed on a self-hosted node, the build fails. Acceptable for
    v1; PRD Out-of-Scope explicitly lists `--rpc-url` override as a
    P2 escape hatch if needed.

### ADR-007: Integer-only token amount conversion; no `float` anywhere on the amount path

- **Status:** Accepted.
- **Context:** PRD Non-Functional Requirements: "No float arithmetic on
  token amounts." Float drift on 18-decimal tokens silently corrupts
  values (e.g. `float("0.1") * 10**18` ≠ `100000000000000000`).
- **Decision:**
  - `_parse_amount(human, decimals)` is a pure string-manipulation
    function: split on `"."`, validate each half against `^\d+$`
    (allowing empty fractional / integer parts but not both empty),
    reject `len(frac) > decimals`, right-pad `frac` to `decimals`
    digits, concatenate, `int(..., 10)`. Never calls `float()`,
    `decimal.Decimal`, or any other numeric type that could introduce
    drift.
  - Test asserts (a) bit-pattern outputs on golden vectors, (b)
    grep-style: no `float(` occurrence in the amount-conversion
    function.
- **Alternatives Considered:**
  - **`decimal.Decimal`.** Stdlib, exact, but adds a layer that can be
    misused. Rejected for simplicity; pure string-split is enough.
  - **`fractions.Fraction`.** Overkill; same objection.
- **Consequences:**
  - **+** No float drift; testable arithmetically.
  - **+** Operates correctly on any decimals value the PRD permits
    (0–36).
  - **−** Error messages are bespoke (no generic numeric parser).
    Acceptable; PRD calls for specific wording for each rejection.

### ADR-008: Stdout = JSON; stderr = summary + warnings + errors

- **Status:** Accepted.
- **Context:** PRD §16 mandates a clean stdout/stderr split so operators
  can pipe stdout into the signer. v1 prints JSON only on stdout
  (errors go to stderr) but does not print a summary. The ERC-20 helper
  adds a summary and warnings; the discipline must hold.
- **Decision:**
  - Stdout: exactly one `print(json.dumps(tx, indent=2))` call, only on
    the happy path.
  - Stderr: `sys.stderr.write(...)` for `error:`, `WARNING:`, and the
    summary block. The summary always prints on the happy path; the
    JSON immediately follows on stdout.
  - The order of writes is: any WARNINGs (as they occur during the
    do_* function), summary block (just before JSON emission), JSON
    on stdout. Terminal output interleaves cleanly because stderr is
    line-buffered.
- **Alternatives Considered:**
  - **Print both JSON and summary on stdout, separated by a marker.**
    Rejected — breaks `jq` / pipe-to-signer.
  - **`--summary-only` and `--json-only` flags.** Rejected — added
    complexity for v1; PRD §P1 lists `--summary-only` for Phase 2.
- **Consequences:**
  - **+** `python3 build_erc20.py transfer ... | jq .` Just Works.
  - **+** `python3 build_erc20.py transfer ... 2>/dev/null` gives the
    operator only the JSON.
  - **−** Operators redirecting stderr lose the safety summary; this is
    explicit, opt-in behavior, not a default.

## Assumptions

This candidate makes the following assumptions (recorded per the user's
instructions; none required asking the user, all are derivable from the
PRD + research):

1. **The skill directory layout stays flat.** No `__init__.py`, no
   `erc20/` subdir, no test-runner config. The PRD names the files at
   their flat paths (`.claude/skills/eth-tx-builder/build_erc20.py` and
   `test_build_erc20.py`) and the existing v1 layout is flat.
2. **Public RPC endpoints (`publicnode.com`) are sufficient.** v1 hardcodes
   them; the PRD does not call for `--rpc-url` (explicitly Out of Scope).
   The duplicated `NETWORKS` map uses the same URLs as v1.
3. **Python 3.8+ stdlib is the runtime.** PRD success metric "Zero new
   dependencies" verified by `python3 build_erc20.py --help` on a fresh
   install. No version check is added to the helper.
4. **Tests run from the skill directory** via `python3 -m unittest
   test_build_erc20 -v` (mirroring v1). No `pytest`, no `tox`, no
   `Makefile` target — the existing repo Makefile targets are Go-focused.
5. **The PRD's P1/P2 items are not in scope for this candidate.**
   `balanceOf` pre-check, approve race guard, `sepolia`/`holesky` network
   additions, `--summary-only`, `--revoke`, polished bytes32 decode,
   `permit` — all deferred. The architecture leaves room for each
   (single-file module, comment banner per concern), but adds none.
6. **The DRIFT NOTE comment is enough drift mitigation for v1.** No
   automated lint or `make verify-duplicate` target is added; the
   project-plan tracks revisiting duplication when a third helper lands
   (per ADR-001 and the Service Extraction Path).
7. **No CI changes.** The repo's `make test` is Go-focused; Python tests
   are run manually as part of PR review (existing posture). Wiring
   Python tests into `make test` would be a scope expansion beyond
   "minimal footprint."
8. **`eth-signer-mcp`'s `sign_transaction` accepts the ERC-20 TxRequest
   shape today** (same shape as v1's ETH-send output, with non-zero `data`
   and zero `value`). The PRD does not request signer-side changes; this
   is consistent with `eth-signer-mcp`'s validate.go accepting any
   well-formed EIP-1559 TxRequest.
9. **`hoodi` is reachable from CI / dev machines** for the manual
   end-to-end check in PRD success criteria. v1 already assumes this.
10. **Selectors do not need a runtime verify step.** The six selectors are
    hardcoded; a runtime keccak would violate stdlib-only. The test suite
    asserts the encoded calldata matches the USDC mainnet vectors in
    research §01-abi-encoding §"Verified real-world test vectors", which
    is the same guarantee via a different path.

## Data Flow Diagrams

(See §Data Flow Diagrams above — Flows 1–4 cover the happy path, the
warning paths, the soft-check, and the no-fallback estimate failure.)

## Open Questions

1. **Should the project-plan include a `make verify-duplicate` target?**
   This candidate says no for v1 (single DRIFT NOTE comment is enough at
   two-helper scale). Worth confirming with the user before adding
   tooling that would expand the footprint.
2. **Should the duplicated `NETWORKS` map's `sepolia` / `holesky` entries
   land in this delta or in a follow-up PRD-P1 phase?** This candidate
   says follow-up (PRD P1 §4 explicitly). The map is trivially extensible.
3. **Is there a risk of the duplicated `User-Agent` string drifting?**
   This candidate copies the literal `"eth-tx-builder/1.0"` from v1.
   If v1 ever bumps to `1.1`, the duplicate may not. Mitigated by the
   DRIFT NOTE; revisit at extraction time.

## Risks

| Risk | Likelihood | Impact | Mitigation |
|---|---|---|---|
| **v1 helpers drift between the two copies** (someone fixes a bug in `rpc_call` in v1, forgets `build_erc20.py`, or vice versa) | Medium over years; Low in v1 timeframe | Subtle bugs in one path | DRIFT NOTE comment; project-plan task to revisit at next helper; extract `_common.py` at three-helper threshold |
| **A regression in `build_send_eth.py` slips in despite the no-edit rule** (e.g. a stray gofmt / whitespace) | Very low | v1 path breaks | `test_build_send_eth.py` is the regression check; CI / PR review must run it |
| **A bug in the ABI encoders silently produces wrong calldata** | Medium without tests; Low with the PRD's required golden-vector tests | On-chain revert at broadcast | Test suite includes the verified USDC mainnet vectors from research §01 (bit-pattern checks) |
| **`eth_estimateGas` failure handling regresses (someone adds a silent fallback)** | Low in v1; Medium years later | TxRequest gets signed and burns gas on a doomed tx | Multi-line "do not fall back" comment in `estimate_gas`; test asserts RPC error propagates / no JSON emitted on stdout |
| **`decimals()` returns suspicious value (>36) and is silently accepted** | Low | Wrong base-unit amount; over-/under-spend | Hardcoded `MAX_DECIMALS = 36` cap; test asserts 37 is rejected |
| **`--approve-max` warning gets quieted by stderr redirect** | Operator choice | Unlimited approval signed without operator seeing the warning | Documented in SKILL.md / README; the warning is on stderr deliberately so it precedes the JSON in tty mode |
| **publicnode rate-limits one of the six sequential RPC calls** | Low for occasional skill use; Higher under load | Build fails | RPC error propagates → `error:` + exit 1; operator retries; `--rpc-url` override is a P2 escape hatch in the PRD's open questions |
| **A bytes32 symbol token (e.g. MKR) trips the standard `string` decode and the fallback also fails** | Low for the v1 token universe | Summary shows "(symbol unavailable)"; build still succeeds | Symbol failure is non-fatal by design; PRD §10 |
| **Allowance soft-check on `transfer-from` blocks a legitimate multi-step workflow** | Zero by design | n/a (warning, not block) | The soft-check warns only; the build proceeds |

## Architecture Quality Checklist

- [x] **No circular dependencies between modules.** `build_erc20.py` does
  not import `build_send_eth.py`; v1 does not import the new module;
  test files import only their own subject.
- [x] **Each module has a single, clear responsibility.** `build_erc20.py`:
  *Build an ERC-20 TxRequest.* `build_send_eth.py`: *Build an ETH-send
  TxRequest.* SKILL.md: *Route the agent.* README.md: *Orient the operator.*
- [x] **No shared databases / module owns its data.** Both helpers are
  stateless; the only "data store" is publicnode's RPC view, which is
  read-only.
- [x] **All inter-module communication goes through defined interfaces.**
  Module-to-module: zero (no in-process import edges). The CLI is the
  contract; agents read SKILL.md to know which CLI to invoke.
- [x] **Every module can be tested in isolation.**
  `test_build_send_eth.py` imports only `build_send_eth`;
  `test_build_erc20.py` imports only `build_erc20`. Mocked `rpc`
  callable replaces the network.
- [x] **Cross-cutting concerns are standardized.** stdout/stderr discipline
  (ADR-008), `error:` vs `WARNING:` prefix conventions, `RPCError`
  propagation pattern. All applied uniformly inside `build_erc20.py`.
- [x] **Failure modes are defined.** §Failure Modes lists every
  identifiable mode and its handling.
- [x] **Service extraction path is clear.** §Service Extraction Path
  spells out exactly when and how `_common.py` extraction happens. Both
  helpers are already structurally ready (helpers are module-level
  functions with stable signatures).
- [x] **Data flow is traceable.** §Data Flow Diagrams cover the three ops
  + the no-fallback estimate failure.
- [x] **Module count is justified.** Two runnable modules (v1 + ERC-20),
  two test modules, two prose modules. The minimum that satisfies the
  PRD. Not under-split (a single mega-helper would force editing v1);
  not over-split (a sub-package or `_common.py` would touch v1 or add
  files that don't earn their keep at two-helper scale).
