# Software Architecture: eth-tx-builder ERC-20 Extension (Layered Modules)

> Candidate axis: **layered-modules** — decompose `build_erc20.py` into focused,
> separately-testable units (an ABI codec, a contract-reads module, a gas/fee
> assembly module, and a thin CLI dispatch layer) so future operations (permit,
> additional networks, decoder features) slot in without rework. Pure functions
> with an injected `rpc` callable, testable in isolation.

## Overview

The ERC-20 extension lives entirely in
`.claude/skills/eth-tx-builder/build_erc20.py` and ships with a sibling
`test_build_erc20.py`. Internally, the file is organized into **five layered
modules** (Python source-level modules, expressed as labeled sections inside the
single file) that depend strictly downward — codec → reads → estimator → assembly
→ CLI. The v1 path (`build_send_eth.py` + `test_build_send_eth.py`) is **bit-for-bit
unchanged**; `build_erc20.py` imports a small, well-bounded set of helpers from
it (network map, RPC plumbing, address/int validators, fee strategy) without
modifying the file.

The guiding principle is **clean seams for v2 features**: when `permit`,
`balanceOf` pre-checks, additional networks, or a generic ABI decoder land, each
hits exactly one module. The CLI grows new subcommand wiring; new selectors and
encode/decode pairs land in the codec; new reads land in the reads module; the
gas estimator is invariant. No layer changes when another layer extends.

## Architecture Principles

- **Strict downward dependency** — Layer N depends only on layers `< N`. There
  are no upward calls, no callbacks across layers (except the injected `rpc`
  callable, which is data not code), and no peer imports inside a layer.
- **Pure functions with injected `rpc`** — Every function that touches the chain
  takes `rpc` as a parameter; the default is `rpc_call` from `build_send_eth`.
  Unit tests pass a stub. No globals, no I/O at import time, no module-level
  state. This is the v1 style; the extension matches it exactly.
- **Single-responsibility module boundaries** — Each labeled section owns one
  domain concern (codec / reads / estimator / assembly / dispatch). If a change
  touches more than one section, the boundary is wrong.
- **Stdlib only** — No `eth-abi`, no `web3.py`, no `pycryptodome`, no
  `requests`. Only the imports already used by `build_send_eth.py` (`argparse`,
  `json`, `re`, `sys`, `urllib.request`). Enforced by the test surface.
- **No float arithmetic on token amounts** — Every numeric path uses integer
  string manipulation. Tests assert no `float(...)` call appears in the
  conversion path.
- **Error-and-stop for correctness-bearing failures; warn-and-continue for
  enrichment failures** — Hard error if a failed read or estimate would silently
  corrupt calldata or gas (decimals parse, gas estimate, address validation).
  Soft warning if the failed read only enriches the summary (symbol, allowance
  soft-check, balanceOf soft-check). This is the reconciled posture from
  research §2.
- **v1 untouched** — `build_send_eth.py` and `test_build_send_eth.py` are
  read-only inputs to this architecture. Reuse via import; never edit.

## System Context Diagram

```text
                ┌──────────────────────────────────┐
                │ Operator (Claude Code skill user)│
                └────────────────┬─────────────────┘
                                 │ shell invocation
                                 ▼
   ┌─────────────────────────────────────────────────────────────────┐
   │  .claude/skills/eth-tx-builder/build_erc20.py                   │
   │  (this architecture)                                            │
   └─────────┬───────────────────────────────────────┬───────────────┘
             │ imports (read-only)                   │ stdout = JSON
             │                                       │ stderr = summary + WARNINGs
             ▼                                       ▼
   ┌──────────────────────────┐               ┌──────────────────────────┐
   │ build_send_eth.py (v1)   │               │ eth-signer-mcp           │
   │ NETWORKS, rpc_call,      │               │ sign_transaction         │
   │ validate_hex_address,    │               │ (offline, separate proc) │
   │ parse_hex_int,           │               └──────────────────────────┘
   │ fetch_nonce/base_fee/tip,│
   │ compute_max_fee, RPCError│
   └────────────┬─────────────┘
                │ HTTPS JSON-RPC (urllib)
                ▼
   ┌──────────────────────────┐
   │ Public RPC endpoint      │
   │ (publicnode mainnet/hoodi)│
   └──────────────────────────┘
```

External dependencies: the public RPC endpoints (`ethereum-rpc.publicnode.com`,
`ethereum-hoodi-rpc.publicnode.com`) defined in `build_send_eth.NETWORKS`, and
the downstream signer process (out-of-band: operator pipes the JSON in).

## Module Overview

| Module | Layer | Responsibility | Owns | Depends On | Communication |
|---|---:|---|---|---|---|
| `abi_codec` | 1 (leaf) | Selector constants + 32-byte word encode + return decode for `decimals`/`symbol`/`allowance` | ERC-20 ABI knowledge | (stdlib only) | sync function calls |
| `contract_reads` | 2 | `fetch_decimals` / `fetch_symbol` / `fetch_allowance` over an injected `rpc` | What an `eth_call` to the token says today | `abi_codec`, v1 `RPCError` | sync calls + `rpc` injection |
| `gas_estimator` | 2 | `estimate_gas` with `from`-populated call object, +20% buffer, 300k cap, no-fallback | The gas number for the final tx | (stdlib + v1 `RPCError`) | sync calls + `rpc` injection |
| `amount_codec` | 1 (leaf) | Human decimal-string ↔ base-unit integer conversion; `MAX_UINT256` constant | Amount arithmetic with no float | (stdlib only) | sync function calls |
| `tx_assembly` | 3 | `build_transfer` / `build_approve` / `build_transfer_from` — compose calldata, reads, gas, fees into a `TxRequest` dict | The op-level `do_*` business logic | `abi_codec`, `amount_codec`, `contract_reads`, `gas_estimator`, v1 fees/nonce | sync function calls |
| `summary` | 2 | Render the stderr human summary + emit all `WARNING:` lines | Summary text shape | `amount_codec` (for human ↔ base render) | pure str → stderr write |
| `cli_dispatch` | 4 (top) | argparse subparsers + argument validation + dispatch to `tx_assembly` + print JSON | CLI shape (the public contract) | `tx_assembly`, `summary`, v1 `validate_hex_address` | argparse + stdout/stderr |

Each module is a labeled section inside `build_erc20.py` with a leading
"Layer N — <name>" comment banner and a `# -- end of layer N --` close. The
sections appear in dependency order so a top-to-bottom read mirrors the
dependency graph.

> **Why seven sections for a "small scope" file?** See ADR-001. The short answer:
> the seven names correspond to seven *distinct change axes* called out in the
> PRD (new selector / new read / fee policy change / new write op / new amount
> format / new warning / new subcommand). Collapsing them would re-fuse two
> change axes into one section and make every future P1/P2 feature touch more
> code than it should. The total file is still ~500–700 lines; each section is
> small.

## Module Dependency Graph

```text
                                      ┌──────────────────┐
                                      │  cli_dispatch    │   Layer 4 (top)
                                      └────────┬─────────┘
                                               │
                       ┌───────────────────────┼───────────────────────┐
                       ▼                       ▼                       ▼
              ┌──────────────┐         ┌──────────────┐         ┌──────────────┐
              │ tx_assembly  │         │   summary    │         │ (v1.validate │
              │   (Layer 3)  │         │   (Layer 2)  │         │ _hex_address)│
              └──┬───┬───┬───┘         └──────┬───────┘         └──────────────┘
                 │   │   │                    │
                 │   │   │                    ▼
                 │   │   │            ┌──────────────┐
                 │   │   │            │ amount_codec │   Layer 1 (leaf)
                 │   │   │            └──────────────┘
                 │   │   │
        ┌────────┘   │   └─────────┐
        ▼            ▼             ▼
┌──────────────┐ ┌──────────────┐ ┌──────────────┐
│ contract_    │ │ gas_         │ │ (v1.fetch_   │
│ reads        │ │ estimator    │ │ nonce/base_  │
│  (Layer 2)   │ │  (Layer 2)   │ │ fee/tip)     │
└──────┬───────┘ └──────────────┘ └──────────────┘
       │                ▲
       ▼                │
┌──────────────┐        │
│  abi_codec   │        │
│  (Layer 1)   │        │
└──────────────┘        │
                        │
                  (v1.RPCError)
```

Verify (no circular dependencies):

- Layer 1 (`abi_codec`, `amount_codec`) imports nothing internal.
- Layer 2 (`contract_reads`, `gas_estimator`, `summary`) imports only Layer 1
  (plus v1 plumbing read-only).
- Layer 3 (`tx_assembly`) imports Layers 1 and 2.
- Layer 4 (`cli_dispatch`) imports Layer 3 (and Layer 2 for warning emission)
  but nothing imports `cli_dispatch`.

There are no peer imports inside a layer: `gas_estimator` does not call
`contract_reads`; `summary` does not call `gas_estimator`; `tx_assembly` is the
only place that fans out across Layer 2.

---

## Module Details

### Module: `abi_codec` (Layer 1)

**Responsibility:** Encode ERC-20 function calldata and decode `eth_call` return
words. Owns all ERC-20 ABI knowledge.

**Domain entities:**

- Function selectors (six hex constants, one per ERC-20 method used).
- 32-byte ABI words (addresses left-pad-zero-12, uint256 left-zero-pad-32,
  dynamic string offset/length/utf8 tail).

**Public API:**

| Function | Input | Output | Description |
|---|---|---|---|
| `_encode_address(addr_hex: str) -> str` | `0x` + 40 hex | 64-hex-char word | Lowercase, strip `0x`, left-pad 24 zero chars |
| `_encode_uint256(n: int) -> str` | non-neg int < 2**256 | 64-hex-char word | Big-endian, left-zero-padded |
| `_pack_call(selector_hex: str, *args_hex: str) -> str` | hex strings | `0x` + concatenated hex | Calldata builder |
| `encode_transfer(to, amount_base) -> str` | address, int | calldata hex | `transfer(address,uint256)` |
| `encode_approve(spender, amount_base) -> str` | address, int | calldata hex | `approve(address,uint256)` |
| `encode_transfer_from(from_, to, amount_base) -> str` | addr, addr, int | calldata hex | `transferFrom(address,address,uint256)` |
| `encode_decimals_call() -> str` | — | `0x313ce567` | Static read selector |
| `encode_symbol_call() -> str` | — | `0x95d89b41` | Static read selector |
| `encode_allowance_call(holder, spender) -> str` | addr, addr | calldata hex | `allowance(address,address)` read |
| `decode_decimals(hex_result: str) -> int` | 32-byte hex word | 0–36 | `int(w,16) & 0xff` then range check (>36 → `ValueError`) |
| `decode_symbol(hex_result: str) -> str \| None` | dynamic-string return | str / None | Standard ABI string; fall back to null-trimmed bytes32; None if both fail |
| `decode_allowance(hex_result: str) -> int` | 32-byte hex word | uint256 int | `int(w,16)` |

**Constants:**

- `SELECTOR_TRANSFER = "0xa9059cbb"`
- `SELECTOR_APPROVE = "0x095ea7b3"`
- `SELECTOR_TRANSFER_FROM = "0x23b872dd"`
- `SELECTOR_DECIMALS = "0x313ce567"`
- `SELECTOR_SYMBOL = "0x95d89b41"`
- `SELECTOR_ALLOWANCE = "0xdd62ed3e"`
- `MAX_DECIMALS = 36` (defensive ceiling — exotic but plausible; >36 is suspicious)

Each selector carries a one-line comment recording the canonical signature it
hashes from (e.g. `# keccak256("transfer(address,uint256)")[:4]`). No live
Keccak hashing; the selector-collision caveat (research §1.5) is noted in a
single comment block above the constants.

**Data store:** None. Purely functional.

**Events Published / Consumed:** None.

**Internal structure (within `build_erc20.py`):**

```text
# === Layer 1: abi_codec =====================================================
# Selector constants
# _encode_address / _encode_uint256 / _pack_call
# encode_transfer / encode_approve / encode_transfer_from
# encode_decimals_call / encode_symbol_call / encode_allowance_call
# decode_decimals / decode_symbol / decode_allowance
# === end Layer 1 ============================================================
```

**Key design decisions:**

- Selectors are hardcoded module-level constants (no Keccak dependency). The
  derivation is in a comment; the verification lives in
  `test_build_erc20.py` (encode-equality tests against the USDC mainnet test
  vectors from research §3).
- `decode_symbol` returns `Optional[str]` rather than raising — symbol is the
  one read whose failure is non-fatal at the caller layer. Returning `None`
  lets `tx_assembly` show `(unavailable)` without a try/except dance.
- `decode_decimals` *does* raise on `> MAX_DECIMALS` because a wrong decimals
  silently corrupts the amount. This is the codec enforcing the
  enrichment-vs-correctness split at the lowest possible layer.

**Failure modes:**

- Malformed input (negative uint, address that is not 40 hex chars) → `ValueError`
  (caught one layer up).
- `decode_decimals` returning a `> 36` value → `ValueError` raised; surfaces as
  hard error in CLI.
- `decode_symbol` failing entirely → returns `None`; caller displays
  `(unavailable)`.

**Reuse from v1:** None. Codec is greenfield.

---

### Module: `amount_codec` (Layer 1)

**Responsibility:** Convert human-readable decimal strings (e.g. `"1.5"`) to
base-unit integers using a decimals value, and vice versa for display. Owns the
`MAX_UINT256` constant.

**Public API:**

| Function | Input | Output | Description |
|---|---|---|---|
| `human_to_base_units(amount_str: str, decimals: int) -> int` | `"1.5"`, `6` | `1500000` | Integer-only conversion; reject more fractional digits than `decimals` |
| `base_units_to_human(amount: int, decimals: int) -> str` | `1500000`, `6` | `"1.5"` | For summary rendering |
| `MAX_UINT256` | — | `2**256 - 1` | Constant for `--approve-max` |

**Validation rules** (raises `ValueError` with explicit messages):

- Empty string.
- Negative (leading `-`).
- Multiple decimal points.
- Non-digit characters outside the single decimal point.
- More fractional digits than `decimals` (clear message:
  `"amount has more fractional digits (N) than token decimals (M)"`).

`"0"` and `"0.0"` are valid (zero ops are legitimate).

**Key design decisions:**

- Pure string → string → int conversion. The test surface includes a `grep`-style
  assertion that the conversion function body contains no `float(`.
- `human_to_base_units` takes `decimals` as a parameter, not from a global —
  enabling table-driven tests for `decimals ∈ {0, 6, 18, 24}` without monkey-
  patching.

**Failure modes:** All input violations raise `ValueError`. Caller turns these
into `error: ...` exit-1 messages.

**Reuse from v1:** None.

---

### Module: `contract_reads` (Layer 2)

**Responsibility:** Read ERC-20 state by composing `abi_codec` encoders with the
v1 `rpc_call`. Each read returns a single typed value or raises `RPCError`.

**Public API:**

| Function | Input | Output | Description |
|---|---|---|---|
| `fetch_decimals(rpc, url, token) -> int` | rpc, url, addr | 0–36 int | `eth_call(decimals())` + `decode_decimals` |
| `fetch_symbol(rpc, url, token) -> Optional[str]` | rpc, url, addr | str or None | `eth_call(symbol())` + best-effort decode |
| `fetch_allowance(rpc, url, token, holder, spender) -> int` | rpc, url, 3 addrs | uint256 | `eth_call(allowance(...))` + `decode_allowance` |

Each call internally builds the `eth_call` params:
`[{"to": token, "data": <encoded call>}, "latest"]`. `from` is **not** sent on
reads (read calls don't branch on msg.sender for the calls we make).

**Data store:** None. Read-through to chain state via `rpc`.

**Failure modes:**

- `fetch_decimals` — propagates `RPCError`; propagates `ValueError` from
  `decode_decimals` (>36). Both are hard errors at the caller.
- `fetch_symbol` — catches `RPCError` *and* any decode failure and returns
  `None`. Symbol is best-effort, so this layer is the right place to swallow
  the exception; callers never need to know there was an error. (The CLI
  still emits a non-fatal `WARNING:` line on `None`, via `summary`.)
- `fetch_allowance` — propagates `RPCError`. Caller (`tx_assembly` for
  `transfer-from`) decides whether to treat it as a soft warning (yes: PRD §11).

**Reuse from v1:** Uses `rpc_call` (default value for `rpc` param) and
`RPCError`, both imported from `build_send_eth`.

---

### Module: `gas_estimator` (Layer 2)

**Responsibility:** Run `eth_estimateGas` with the correct call shape, apply
the +20% buffer, cap at 300k, and surface the underlying error on failure with
no fallback.

**Public API:**

| Function | Input | Output | Description |
|---|---|---|---|
| `estimate_gas(rpc, url, sender, token, data) -> int` | rpc, url, sender, token, calldata hex | int (≤ 300_000) | Buffered + capped estimate |

**Constants:**

- `GAS_BUFFER_NUM = 12`, `GAS_BUFFER_DEN = 10` → +20% buffer (integer math
  `(est * 12) // 10`).
- `GAS_CAP = 300_000`.

**Call object built internally:**

```python
{"from": sender, "to": token, "data": data, "value": "0x0"}
```

against `"latest"`. No fee fields (research §3: fee fields trip the balance
check on zero-ETH senders).

**Failure modes:**

- `RPCError` (revert, transport, node refused) → propagated. Caller (CLI dispatch)
  catches at the top level, prints the error with the no-fallback message
  template, and exits 1. The rationale ("a silent fallback would let a
  transaction that will revert get signed and burn its full gas budget") is
  documented in an in-code comment so future maintainers don't relax it.

**Reuse from v1:** Uses `rpc_call` (default) and `RPCError`.

---

### Module: `summary` (Layer 2)

**Responsibility:** Render the stderr human-readable summary and emit all
`WARNING:` lines. Owns the wording, the field order, and the human/base-units
"adjacent lines" layout from research §1 (which exists to make off-by-N-zeros
errors pop visually).

**Public API:**

| Function | Input | Output | Description |
|---|---|---|---|
| `render_summary(ctx: dict) -> str` | summary context | multi-line str | Build summary text |
| `print_summary(ctx: dict) -> None` | — | — | Write `render_summary(ctx)` to stderr |
| `warn_approve_max(symbol, token, spender) -> None` | strings | — | Write the `--approve-max` multi-line warning to stderr |
| `warn_low_allowance(holder, spender, current, requested, decimals) -> None` | — | — | `transferFrom` soft-check warning |
| `warn_allowance_check_skipped(reason: str) -> None` | — | — | When `allowance` RPC itself fails |
| `warn_symbol_unavailable() -> None` | — | — | Optional, mostly informational |

`ctx` is a plain dict with keys: `operation`, `network`, `chain_id`, `token`,
`symbol`, `decimals`, `human_amount`, `base_amount`, `is_max_uint`, role-specific
addresses (varies by op), `nonce`, `gas`, `max_fee`, `max_priority_fee`.

**Key design decisions:**

- All stderr writes for human consumption funnel through this module. The CLI
  layer never writes summary text directly; it only writes the
  `error: ...` exit-1 lines (matching v1).
- `render_summary` returns text so it is trivially testable as
  `assertIn("spender = 0xRouter...", render_summary(ctx))`.
- Wording matches research §2 (verified against MetaMask/Rabby behavior
  descriptions); concrete strings live here so the test suite can pin them.

**Failure modes:** None — pure text rendering. Any
input-shape problem is a programming error (the dict didn't get populated
correctly upstream), which surfaces as a `KeyError` in tests.

**Reuse from v1:** None.

---

### Module: `tx_assembly` (Layer 3)

**Responsibility:** Compose calldata + reads + gas + fees into a `TxRequest` dict
for each of the three ops. This is the only layer where the v1 fee strategy and
the new ERC-20 stack meet.

**Public API:**

| Function | Input | Output | Description |
|---|---|---|---|
| `do_transfer(network, token, to, amount, sender, *, rpc=rpc_call) -> (tx_dict, summary_ctx)` | validated args | TxRequest + summary ctx | `transfer` builder |
| `do_approve(network, token, spender, amount, sender, *, approve_max=False, rpc=rpc_call) -> (tx_dict, summary_ctx, warnings)` | — | — | `approve` builder (bounded or `--approve-max`) |
| `do_transfer_from(network, token, from_, to, amount, sender, *, rpc=rpc_call) -> (tx_dict, summary_ctx, warnings)` | — | — | `transfer-from` builder, runs allowance soft-check |

Each `do_*` function follows the same skeleton:

1. Resolve `(chain_id, url)` via `build_send_eth.network_config(network)`.
2. (Address validation already happened at the CLI layer.)
3. `decimals = contract_reads.fetch_decimals(rpc, url, token)` (hard fail).
4. `symbol = contract_reads.fetch_symbol(rpc, url, token)` (best-effort `None`).
5. Resolve `amount_base`:
   - `do_approve` with `approve_max=True` → `amount_base = MAX_UINT256`.
   - Otherwise `amount_base = amount_codec.human_to_base_units(amount, decimals)`.
6. Build `calldata` via the matching `abi_codec.encode_*`.
7. (For `do_transfer_from`) Read `fetch_allowance(holder=from_, spender=sender)`;
   if it raises `RPCError`, queue a `warn_allowance_check_skipped` warning;
   if it returns < `amount_base`, queue a `warn_low_allowance` warning.
8. `gas = gas_estimator.estimate_gas(rpc, url, sender, token, calldata)` (hard
   fail propagates `RPCError`).
9. v1-derived fees: `nonce = fetch_nonce(rpc, url, sender)`,
   `base_fee = fetch_base_fee(rpc, url)`, `tip = fetch_tip(rpc, url)`,
   `max_fee = compute_max_fee(base_fee, tip)`.
10. Return `(tx_dict, summary_ctx, warnings)`.

The returned `tx_dict` matches the v1 TxRequest shape exactly:

```python
{
    "type": "eip1559",
    "chainId": str(chain_id),
    "nonce": str(nonce),
    "to": token,             # the token contract, not the recipient
    "value": "0",            # no ETH
    "data": calldata,        # ABI-encoded
    "gas": str(gas),
    "maxFeePerGas": str(max_fee),
    "maxPriorityFeePerGas": str(tip),
}
```

**Data store:** None. Composes other modules.

**Failure modes:**

- `RPCError` from `fetch_decimals` / `estimate_gas` / v1 fee fetchers → propagate.
- `ValueError` from `amount_codec.human_to_base_units` (bad amount) or
  `decode_decimals` (>36) → propagate.
- `fetch_symbol` failure → swallowed at Layer 2; `summary_ctx['symbol'] = None`.
- `fetch_allowance` failure → swallowed locally with a queued warning; build
  still produces JSON (PRD §11).

**Key design decisions:**

- Returns a `(tx_dict, summary_ctx, warnings)` tuple instead of printing.
  Printing lives at the CLI layer. This keeps `do_*` pure and unit-testable with
  no `capsys`/`StringIO` plumbing — the v1 style.
- Address validation is the CLI layer's job; `do_*` assumes already-valid hex.
  This avoids double-validation (PRD §13 says format-only, once is enough).
- `do_approve(..., approve_max=True)` is the same `do_*` function with a flag,
  not a separate function. The PRD's mutual exclusion is enforced at the CLI
  layer; `tx_assembly` just gets a clean boolean.

**Reuse from v1:** Imports `network_config`, `fetch_nonce`, `fetch_base_fee`,
`fetch_tip`, `compute_max_fee`, `rpc_call`, `RPCError`.

---

### Module: `cli_dispatch` (Layer 4)

**Responsibility:** argparse subparsers for the three subcommands; argument
validation (format-only address checks); dispatch to the matching `do_*`; print
the JSON to stdout and the summary/warnings to stderr.

**Subcommands** (all return exit 0 on success, exit 1 on error):

- `transfer` — `--network`, `--token`, `--to`, `--amount`, `--sender`
- `approve` — `--network`, `--token`, `--spender`,
  (`--amount` XOR `--approve-max`), `--sender`
- `transfer-from` — `--network`, `--token`, `--from`, `--to`, `--amount`,
  `--sender`

**Dispatch flow:**

```python
def main(argv=None):
    parser = ...                   # top-level + 3 subparsers
    args = parser.parse_args(argv)
    try:
        validate_addresses(args)   # uses v1.validate_hex_address per role
        if args.command == "transfer":
            tx, ctx, warns = do_transfer(...)
        elif args.command == "approve":
            tx, ctx, warns = do_approve(..., approve_max=args.approve_max)
        elif args.command == "transfer-from":
            tx, ctx, warns = do_transfer_from(...)
        for w in warns:
            summary.emit_warning(w)
        summary.print_summary(ctx)
        print(json.dumps(tx, indent=2))
        return 0
    except (ValueError, RPCError) as e:
        print("error: %s" % e, file=sys.stderr)
        return 1
```

**Public API:** `main(argv=None) -> int` — only the entry point. Everything else
is private (`_build_parser`, `_validate_addresses`, etc.).

**Key design decisions:**

- The CLI layer is the *only* place that knows about argparse, sys.stderr, and
  exit codes. Pulling these out of `do_*` is what makes `do_*` testable.
- Argument validation here is format-only (matches v1 §13).
- The `--amount` / `--approve-max` mutual exclusion is expressed via
  argparse's `add_mutually_exclusive_group(required=True)`.
- The subcommand name uses `transfer-from` (hyphenated) at the CLI to match the
  PRD's user-facing examples; the Python function is `do_transfer_from`.

**Failure modes:**

- argparse exits 2 itself on shape errors (`--help` etc.). The skill matches v1
  in honoring argparse's exit codes for those.
- Top-level `try/except (ValueError, RPCError)` mirrors v1's pattern exactly.

**Reuse from v1:** Imports `validate_hex_address`. Mirrors v1's `main()` shape.

---

## Cross-Cutting Concerns

### Authentication & Authorization

None at this layer. The skill makes only outbound RPC calls to public endpoints
(no auth required). The signer (`eth-signer-mcp`) handles all key material
out-of-band. No secrets ever touch `build_erc20.py`.

### Logging & Observability

- **stdout = JSON only.** Operators pipe to `jq` or the signer.
- **stderr = summary + warnings + errors.** Three distinct prefixes:
  - `error: <msg>` — hard error, exit 1, matches v1.
  - `WARNING: <msg>` — soft warning, continues.
  - (Plain text, no prefix) — the summary block.
- No third-party logging library. `sys.stderr.write` and `print(file=...)` only,
  matching v1's posture.

### Error Handling

- Two exception classes only: `ValueError` (input/encoding errors) and
  `RPCError` (transport / node errors, reused from v1).
- The CLI's top-level `try/except (ValueError, RPCError)` is the *only* place
  exceptions become exit codes. Lower layers raise; the CLI catches.
- The error-and-stop vs warn-and-continue split is enforced *structurally* by
  which exception each layer catches:
  - `contract_reads.fetch_symbol` catches → swallows.
  - `tx_assembly` catches `RPCError` only around `fetch_allowance` (the
    designated soft-check); everything else propagates.
- The `eth_estimateGas` no-fallback policy is documented in an in-code comment
  in `gas_estimator.estimate_gas`, with a one-line rationale referencing
  research §03's gas-budget-burn argument.

### Configuration

- **Network map.** Reused verbatim from `build_send_eth.NETWORKS`. Adding a
  network is a one-line edit to `build_send_eth.NETWORKS` (the v1 PRD already
  accounts for `sepolia`/`holesky` as P1) — `build_erc20.py` picks it up for
  free via the imported `network_config`.
- **Selectors.** Hex string constants in `abi_codec`. Adding a new ERC-20 op
  (e.g. `decimals()`-but-uint256 variant) is one new constant + one new encode/
  decode pair, both contained in Layer 1.
- **Buffer / cap.** Module-level constants in `gas_estimator`. Changing the
  policy is one edit; the constants are named (`GAS_BUFFER_NUM`,
  `GAS_BUFFER_DEN`, `GAS_CAP`) so the comment is the documentation.
- No env vars, no config files, no feature flags. The CLI is the
  configuration surface.

---

## Data Flow Diagrams

### `transfer` happy path

```text
Operator
  │ python3 build_erc20.py transfer --network mainnet --token 0xUSDC --to 0x... --amount 1.5 --sender 0x...
  ▼
cli_dispatch (parse args; validate addresses via v1.validate_hex_address)
  │
  ▼
tx_assembly.do_transfer
  │
  ├──▶ build_send_eth.network_config("mainnet") → (1, "https://ethereum-rpc.publicnode.com")
  │
  ├──▶ contract_reads.fetch_decimals(rpc, url, token)
  │       │
  │       ├──▶ abi_codec.encode_decimals_call() → "0x313ce567"
  │       ├──▶ rpc("eth_call", [{"to":token,"data":"0x313ce567"}, "latest"]) → "0x000...06"
  │       └──▶ abi_codec.decode_decimals("0x000...06") → 6
  │
  ├──▶ contract_reads.fetch_symbol(rpc, url, token) → "USDC"  (or None on failure)
  │
  ├──▶ amount_codec.human_to_base_units("1.5", 6) → 1500000
  │
  ├──▶ abi_codec.encode_transfer(to=0x..., amount_base=1500000) → calldata
  │
  ├──▶ gas_estimator.estimate_gas(rpc, url, sender, token, calldata)
  │       │
  │       ├──▶ rpc("eth_estimateGas", [{from,to,data,value:"0x0"}, "latest"]) → "0xfe1f" (65055)
  │       ├──▶ (65055 * 12) // 10 → 78066
  │       └──▶ min(78066, 300_000) → 78066
  │
  ├──▶ build_send_eth.fetch_nonce / fetch_base_fee / fetch_tip / compute_max_fee
  │
  └──▶ return (tx_dict, summary_ctx, warnings=[])
  │
  ▼
cli_dispatch
  │
  ├──▶ summary.print_summary(ctx)            → stderr
  └──▶ print(json.dumps(tx, indent=2))       → stdout
```

### `approve --approve-max` path

```text
Operator
  │ python3 build_erc20.py approve --network mainnet --token 0xUSDC --spender 0xRouter --approve-max --sender 0x...
  ▼
cli_dispatch (validates addresses; sees args.approve_max=True)
  ▼
tx_assembly.do_approve(..., approve_max=True)
  │
  ├──▶ network_config + fetch_decimals + fetch_symbol  (same as above)
  │
  ├──▶ amount_base = MAX_UINT256 = 2**256 - 1          (no human_to_base call)
  │
  ├──▶ abi_codec.encode_approve(spender, MAX_UINT256)  → calldata with all-Fs amount word
  │
  ├──▶ gas_estimator.estimate_gas(...)                 → buffered + capped
  │
  ├──▶ fees + nonce                                    (v1)
  │
  └──▶ warnings.append(("approve_max", {symbol, token, spender}))
  │
  ▼
cli_dispatch
  │
  ├──▶ for w in warnings: summary.emit_warning(w)
  │       └──▶ summary.warn_approve_max(symbol, token, spender) → stderr multi-line warn
  │
  ├──▶ summary.print_summary(ctx with is_max_uint=True, base_amount="MAX UINT256") → stderr
  │
  └──▶ print(json.dumps(tx, indent=2)) → stdout
```

### `transfer-from` with low allowance

```text
Operator
  │ python3 build_erc20.py transfer-from --network mainnet --token 0xUSDC --from 0xHolder --to 0xDest --amount 50 --sender 0xSpender
  ▼
cli_dispatch → tx_assembly.do_transfer_from
  │
  ├──▶ fetch_decimals → 6
  ├──▶ fetch_symbol   → "USDC"
  ├──▶ human_to_base  → 50000000
  ├──▶ encode_transfer_from(from=0xHolder, to=0xDest, amount=50000000) → calldata
  │
  ├──▶ try: contract_reads.fetch_allowance(holder=0xHolder, spender=0xSpender)
  │     ├── success → 30000000
  │     │     │
  │     │     └──▶ 30000000 < 50000000 → warnings.append(("low_allowance", {...}))
  │     │
  │     └── RPCError → warnings.append(("allowance_check_skipped", {reason}))
  │
  ├──▶ estimate_gas      (still runs; the soft-check is advisory only)
  ├──▶ fees + nonce
  │
  └──▶ return tx, ctx, warnings
  │
  ▼
cli_dispatch  (emits warnings to stderr, then summary, then JSON to stdout)
```

### `eth_estimateGas` failure path (no fallback)

```text
gas_estimator.estimate_gas
  │
  ├──▶ rpc("eth_estimateGas", [...]) raises RPCError("execution reverted: ERC20: insufficient balance")
  │
  └── (NO try/except) → RPCError propagates up
       │
       ▼
       tx_assembly.do_transfer  (also no try/except for this path)
       │
       ▼
       cli_dispatch
         │
         ├──▶ except RPCError as e:
         │     print("error: %s" % e, file=sys.stderr)  → stderr: error message including the revert reason
         │     return 1                                  → no JSON printed
```

The structure of "no try/except in the middle layers" is itself a design
decision: the gas-fallback anti-pattern would be a *new* try/except inserted in
the middle, which is now visibly absent everywhere it could be added.

---

## Infrastructure & Deployment

### Deployment Model

- **Single Python file** (`build_erc20.py`) deployed alongside `build_send_eth.py`
  in `.claude/skills/eth-tx-builder/`. No build step; runs on `python3` from the
  system.
- **Logical modules, single file.** The seven layered "modules" are sections in
  one file. This matches the v1 pattern (`build_send_eth.py` is also one file)
  and the PRD §6 directive "stdlib only, no requirements.txt." Splitting into
  multiple files would force a Python package structure or a sys.path dance,
  neither of which the skill needs.
- **Test file:** `test_build_erc20.py`, also one file, organized by
  `TestAbiCodec`, `TestAmountCodec`, `TestContractReads`, `TestGasEstimator`,
  `TestSummary`, `TestTxAssembly`, `TestCliDispatch` classes — one per module,
  mirroring the layer split for grep-ability.

### Scaling Strategy

Not relevant: this is a build-time CLI tool, not a service. Performance budget
is bounded by the 6–7 RPC calls per build (≤ 105s in the worst case at 15s
timeout each, in practice ~1–3s).

### Service Extraction Path

The seven layered modules map cleanly onto separately-extractable units if and
when this functionality moves beyond a CLI script. Most useful framings:

- **`abi_codec` extraction:** trivially a standalone Python package. A
  hypothetical sibling MCP server doing ABI decode would import it as-is.
  *Ready now.*
- **`contract_reads` extraction:** depends on `abi_codec` and on a generic
  `rpc` callable — both portable. *Ready now.*
- **`gas_estimator` extraction:** identical posture; the +20%/300k policy is
  the only opinionated piece. *Ready now.*
- **`amount_codec` extraction:** pure functions, no chain knowledge at all.
  Could be lifted into a `libs/eth-format/` Go package if the Go monorepo grows
  a sibling builder. *Ready now, even cross-language.*
- **`summary` extraction:** UI text rendering; could ship with the CLI in any
  language. *Ready now.*
- **`tx_assembly`:** composes Layer 1 and Layer 2; would move whole. *Ready now.*
- **`cli_dispatch`:** thin shim; trivially replaced by an MCP tool handler or
  HTTP endpoint that calls `do_*` and serializes the result. *Ready now.*

The v1 plumbing (`build_send_eth.*` functions imported here) is the only
"shared-from-v1" coupling; when v1 is itself extracted into a `libs/eth-fees`
shared module, both files cut over together.

---

## Technology Choices

| Concern | Choice | Rationale |
|---|---|---|
| Language | Python 3.8+ (stdlib only) | PRD §Non-Functional: "stdlib only," matches v1 |
| ABI encoding | Hand-rolled in `abi_codec` | No Keccak dependency; selectors hardcoded |
| ABI decoding | Hand-rolled in `abi_codec` | Three return shapes (uint8, uint256, string) — small enough to hand-roll |
| Big-int arithmetic | Python `int` | Arbitrary precision; no `decimal`/`fractions` needed (no float allowed anyway) |
| HTTP | `urllib.request` (via v1 `rpc_call`) | Stdlib; matches v1 |
| Argparse | `argparse` (via subparsers) | Stdlib; matches v1 |
| Test framework | `unittest` + `unittest.mock` | Stdlib; matches v1 (`test_build_send_eth.py` uses these) |
| Logging | `sys.stderr.write` / `print(file=...)` | No structured logger; the CLI is the surface |
| Concurrency | None (sequential) | 6–7 RPC calls bounded by 15s timeout each; PRD §Performance accepts this |

---

## ADRs (Architecture Decision Records)

### ADR-001: Seven labeled sections, not fewer, for an ~600-line file

- **Status:** Accepted
- **Context:** The PRD scope (one new script, three subcommands) is small.
  A naive design might collapse everything into 2–3 sections ("encoding" + "io"
  + "main"). The layered-modules axis requires us to justify each boundary.
- **Decision:** Seven sections aligned to seven distinct PRD change axes:
  - Selector / encoding (`abi_codec`) ← future: more ABI types, decoders
  - Amount math (`amount_codec`) ← future: alternate amount formats
  - Reads (`contract_reads`) ← future: `balanceOf`, `totalSupply`
  - Estimation (`gas_estimator`) ← future: policy tweaks, custom strategies
  - UI text (`summary`) ← future: P1 race-guard warnings, P2 wording polish
  - Op composition (`tx_assembly`) ← future: `permit`, `revoke` shorthand
  - Dispatch (`cli_dispatch`) ← future: new subcommands, `--summary-only`
- **Alternatives considered:**
  - **3-section design** (codec + io + main). Rejected: `permit` (P2) would touch
    `codec` *and* `io` *and* `main` simultaneously, defeating the layering goal.
  - **Two-file split** (`build_erc20.py` + `_erc20_abi.py`). Rejected: PRD
    §Non-Functional locks "stdlib only" + the v1 file is one file; multi-file
    introduces a sys.path / import discipline the skill doesn't need.
  - **Inline everything** (no labeled sections). Rejected: future maintainers
    grepping for "where does decimals decode live" win when sections are named.
- **Consequences:**
  - (+) Each P1/P2 PRD item lands in exactly one section. `--summary-only` (P1
    §5) is two lines in `cli_dispatch`. `balanceOf` pre-check (P1 §2) is one new
    function in `contract_reads` + one new warning in `summary` + one new call
    in `tx_assembly.do_transfer`. `permit` (P2) is new selectors + new encode in
    `abi_codec` + new `do_permit` in `tx_assembly` + new subcommand in
    `cli_dispatch`.
  - (−) Marginal verbosity in a single file. Mitigated by the section banner
    comments being grep-targets and the layer-ordered file layout reading
    top-down.

### ADR-002: Import-then-reuse helpers from `build_send_eth.py` (don't duplicate)

- **Status:** Accepted
- **Context:** PRD §Technical Considerations and §Open Questions both surface
  the import-vs-duplicate question. The constraint is that `build_send_eth.py`
  must stay bit-for-bit unchanged.
- **Decision:** Import the following symbols from `build_send_eth` into
  `build_erc20.py`: `NETWORKS`, `network_config`, `rpc_call`,
  `validate_hex_address`, `parse_hex_int`, `compute_max_fee`, `fetch_nonce`,
  `fetch_base_fee`, `fetch_tip`, `RPCError`.
- **Alternatives considered:**
  - **Duplicate** these symbols verbatim into `build_erc20.py`. Rejected: two
    copies of the fee strategy means two places to track future Phase-2
    `sepolia`/`holesky` additions, two copies to keep in sync if the fee
    heuristic evolves, and twice the surface for "did I update both?" bugs.
  - **Refactor shared helpers into a third file** (`_eth_common.py`). Rejected:
    requires modifying `build_send_eth.py` to drop its definitions and import
    from the third file, which violates the hard constraint.
- **Consequences:**
  - (+) Single source of truth for network map + fee strategy + RPC plumbing.
  - (+) Existing `test_build_send_eth.py` keeps full coverage for the
    imported symbols; `test_build_erc20.py` does not have to re-test them.
  - (−) The import path is `build_send_eth` (not a package), so
    `build_erc20.py` must run from the same directory (it does — both live in
    `.claude/skills/eth-tx-builder/`). Documented in the file header.
  - (−) Any future change to a v1 symbol must be reviewed against ERC-20 callers.
    Mitigated: the imported list above is small (10 symbols) and the
    architectural contract is "v1 stays bit-for-bit," so a v1 change is already a
    deliberate cross-skill event.

### ADR-003: Pure functions with injected `rpc` (v1 style)

- **Status:** Accepted
- **Context:** Testability and the no-globals discipline already established by
  the v1 helper.
- **Decision:** Every function that touches the chain takes `rpc` as a keyword
  argument with `rpc_call` as the default. Tests pass a stub (`mock.MagicMock`
  in v1 style). No module-level RPC state, no globals.
- **Alternatives considered:**
  - **Class-based `Builder` with `rpc` in `__init__`.** Rejected: more
    machinery than needed; v1 doesn't do this; harder to mix-and-match stubs
    per test method.
  - **`functools.partial`-based DI.** Rejected: marginally fancier, less
    grep-able for future maintainers, and no advantage over a kwarg.
- **Consequences:**
  - (+) Matches v1 verbatim; reviewers don't have to learn a new pattern.
  - (+) Tests for `do_transfer` etc. mock `rpc` and assert on the call sequence.
  - (−) The function signatures get one more parameter. Acceptable — the v1
    `build_tx_request` already does this.

### ADR-004: `do_*` functions return `(tx, ctx, warnings)`, never print

- **Status:** Accepted
- **Context:** v1's `build_tx_request` returns a dict; `main` prints. We want to
  preserve that split so the new `do_*` functions stay equally unit-testable.
- **Decision:** Each `do_*` returns a 3-tuple:
  - `tx`: the TxRequest dict.
  - `ctx`: the summary context dict (consumed by `summary.print_summary`).
  - `warnings`: a list of `(kind, payload)` pairs the CLI emits via `summary`.
- **Alternatives considered:**
  - **Print directly in `do_*`.** Rejected: tests would need `capsys`/StringIO,
    departing from v1's pattern.
  - **Raise warning exceptions.** Rejected: warnings are not errors; raising
    fights the language and complicates the success path.
- **Consequences:**
  - (+) `test_build_erc20.py` can assert on the returned tuple without
    capturing output.
  - (+) Warnings are data; reordering or batching them at the CLI layer is
    trivial.
  - (−) Slightly more bookkeeping in `do_*`. The pattern is simple and
    consistent across all three ops.

### ADR-005: No layer crosses to a peer; only Layer 3 fans out across Layer 2

- **Status:** Accepted
- **Context:** Easy mistake: `gas_estimator` could call `contract_reads` to
  enrich the estimate; `summary` could query `contract_reads` for fresh data.
  Both are tempting and both break the layering.
- **Decision:** No Layer-2 module imports another Layer-2 module. The only place
  that fans out across Layer 2 is `tx_assembly` (Layer 3). `summary` is read-
  only on the `ctx` it receives.
- **Alternatives considered:**
  - **Allow Layer 2 peer imports.** Rejected: would let `gas_estimator` import
    `contract_reads` "just for `fetch_decimals` once" — the start of the slow
    drift to entanglement.
- **Consequences:**
  - (+) The dependency graph stays a strict tree.
  - (−) `tx_assembly` is the busiest module. Acceptable: composition is its
    one job.

### ADR-006: `decode_symbol` returns `Optional[str]`; everything else raises

- **Status:** Accepted
- **Context:** The PRD distinguishes fatal reads (`decimals`, `estimateGas`)
  from non-fatal reads (`symbol`, `allowance` soft-check). Research §2
  reconciles the apparent contradiction as "enrichment vs correctness."
- **Decision:** `decode_symbol` (and `fetch_symbol`) returns `None` on failure
  instead of raising. Every other decode/fetch raises `ValueError` or
  propagates `RPCError`. The `allowance` soft-check is handled at the
  `tx_assembly` layer (one explicit try/except around `fetch_allowance` in
  `do_transfer_from`), not by changing `fetch_allowance`'s return type.
- **Alternatives considered:**
  - **All reads raise; CLI catches everything.** Rejected: would force a
    try/except dance for symbol in `tx_assembly`, and the choice of which to
    swallow would migrate upward, blurring the enrichment-vs-correctness split.
  - **All reads return `Optional`.** Rejected: would let `decimals` failure
    silently produce wrong calldata, which the PRD says is unacceptable.
- **Consequences:**
  - (+) Each function's signature documents its failure posture.
  - (+) The split is structurally enforced rather than convention-only.
  - (−) Symbol decode is the asymmetric case in the codec. Documented in the
    function docstring.

### ADR-007: No fallback on `eth_estimateGas` failure, ever — enforced structurally

- **Status:** Accepted
- **Context:** PRD §9 + research §3 are emphatic: a hardcoded gas fallback
  would let a doomed tx get signed and burn its full gas budget.
- **Decision:** `gas_estimator.estimate_gas` does not catch `RPCError` at all.
  `tx_assembly.do_*` does not catch `RPCError` around the call. Only
  `cli_dispatch.main` catches `RPCError`, and only to format the error and
  exit 1 — never to construct a tx. The absence of a try/except in the middle
  layers is itself a load-bearing design fact; an in-code comment in
  `gas_estimator` makes this explicit.
- **Alternatives considered:**
  - **Optional `--gas-fallback` CLI flag** for "I know what I'm doing"
    operators. Rejected for v1; PRD §Out-of-Scope §Per-method gas fallback
    closes this door explicitly. Could be revisited in v2 with a loud opt-in
    flag, but the *default* would still be no-fallback.
- **Consequences:**
  - (+) The build is correct-by-construction: there is no code path that
    silently substitutes a gas number.
  - (+) Future maintainer adding a fallback has to *insert* a try/except into
    a module that has none, which is a visible review-flag.
  - (−) Operators chasing a transient revert (e.g. an ephemeral mempool race)
    can't override. Acceptable: re-run the build a moment later.

### ADR-008: Address validation lives at the CLI layer, not in `do_*`

- **Status:** Accepted
- **Context:** PRD §13 says format-only address validation, once. We could
  re-validate inside `do_*` for safety-in-depth.
- **Decision:** Validate once at the CLI layer (via v1's `validate_hex_address`).
  `do_*` accepts already-validated hex.
- **Alternatives considered:**
  - **Validate again in `do_*`.** Rejected: redundant, would mean two error
    paths for the same condition, and tests would need to cover both layers.
- **Consequences:**
  - (+) Single source of truth for validation; clear error message.
  - (−) Calling `do_*` directly (e.g. from a future MCP wrapper) requires the
    caller to validate first. Documented in `do_*` docstrings.

### ADR-009: Test file mirrors module layout (one TestClass per layered module)

- **Status:** Accepted
- **Context:** v1 `test_build_send_eth.py` is loosely organized; for a
  seven-section file we want a more deliberate test layout.
- **Decision:** `test_build_erc20.py` defines exactly one `TestCase` class per
  module section (`TestAbiCodec`, `TestAmountCodec`, `TestContractReads`,
  `TestGasEstimator`, `TestSummary`, `TestTxAssembly`, `TestCliDispatch`). Each
  class tests only its module's public API; cross-module tests live in
  `TestTxAssembly` and `TestCliDispatch` (the composition layers).
- **Alternatives considered:**
  - **One flat file with per-function tests.** Rejected for a 7-section file:
    grep-ability suffers.
- **Consequences:**
  - (+) `python3 -m unittest test_build_erc20.TestAbiCodec` runs the codec
    tests in isolation.
  - (+) Test counts per layer are visible at a glance; under-tested layers
    stand out.
  - (−) Slight overhead in setup boilerplate per class. Negligible.

---

## Open Questions

1. **Should `do_transfer_from` skip the allowance soft-check when `fetch_decimals`
   already failed?** Currently the architecture has `do_transfer_from` run reads
   in order (`fetch_decimals` first; a raise there short-circuits the rest).
   This is the simplest and probably correct behavior, but worth confirming
   with the PRD owner: a token that fails `decimals()` is unbuildable anyway,
   so there is no allowance to check.
2. **Should `tx_assembly` accept `rpc` as a positional or keyword arg?** v1 uses
   `rpc=rpc_call` as a keyword default. The architecture proposes the same.
   Mostly a style nit.
3. **Should the `MAX_UINT256` constant live in `abi_codec` or `amount_codec`?**
   Both are defensible. The architecture places it in `amount_codec` because
   it is an *amount* value, not an ABI encoding rule. Confirmable at review.
4. **Should the file header docstring spell out the layer list explicitly?**
   Recommended; it doubles as the "table of contents" for new readers.

## Risks

- **Risk: section boundaries drift over time.** Without a hard file-level
  separator, a contributor may add a function to the "wrong" section.
  **Mitigation:** the test layout (ADR-009) mirrors the module layout, so a
  function that doesn't fit any existing `TestCase` is a smell. PR review
  checklist includes "did this PR touch only one section?" — a yes confirms the
  layering held; a no triggers a discussion of whether the boundary needs
  updating.
- **Risk: v1 file changes invalidate the imports.** `build_send_eth.py` is
  bit-for-bit frozen *now*; a future v1 change could rename or remove an
  imported symbol.
  **Mitigation:** the import list is documented (ADR-002) and small (10
  symbols). A future v1 modification PR should grep for these symbol names; if
  found, the modification touches `build_erc20.py` too — a cross-skill review.
- **Risk: the seven-section design is overkill for the small PRD scope.**
  Acknowledged in ADR-001 with the justification that each section maps to a
  distinct PRD change axis. If the P1/P2 backlog never materializes, the
  layering pays no rent. Mitigation is just discipline: keep each section small
  (most are ~50–100 lines).
- **Risk: future contributor adds a silent gas fallback.** Even with the
  in-code comment in `gas_estimator`, well-meaning robustness changes are the
  classic regression here.
  **Mitigation:** PR checklist + (optional) a regression test that monkey-patches
  `rpc` to raise on `eth_estimateGas` and asserts the build exits 1 with no
  JSON on stdout. ADR-007 makes this a load-bearing decision so any change to
  it requires updating the ADR.
- **Risk: stdlib-only constraint slows future enhancement.** `permit` (EIP-2612)
  requires EIP-712 typed-data hashing, which in turn needs Keccak-256. That is
  beyond stdlib.
  **Mitigation:** PRD §Out-of-Scope already lists `permit` as v2/never-in-this-
  helper. If it lands, it gets its own helper (`build_erc20_permit.py`) and
  its own dependency conversation.
- **Risk: contract returns malicious decimals (e.g. 255 wrapping to a small
  number).** The `& 0xff` mask + `> 36` reject handles plausible cases; an
  adversarial token returning a value that decodes as 5 but means 18 is
  undetectable.
  **Mitigation:** the loud stderr summary always shows `decimals=<N>` adjacent
  to the resolved base-unit amount, giving the operator a last-line-of-defense
  visual check. Trail of Bits guidance ("decimals must be uint8") is documented
  in code comments.

---

## Assumptions

Recorded here since the workflow does not allow questions to the user.

1. The skill ships as a single Python file per the v1 convention; "modules" here
   means logical sections in the file with explicit banner comments, not
   separate `.py` files.
2. The import-from-v1 path resolves because both files live in
   `.claude/skills/eth-tx-builder/` and Python is launched from that directory
   (matches v1 invocation pattern).
3. `build_send_eth.py` does not edit the helper symbols listed in ADR-002.
   The hard constraint guarantees this for v1; future changes are scoped as a
   cross-skill event.
4. The test file may exceed 500 lines because of the seven `TestCase` classes;
   PRD's "stdlib only" applies to both implementation and tests, but does not
   cap file length.
5. The PRD's "build_send_eth.py and test_build_send_eth.py MUST stay bit-for-
   bit unchanged" is treated as an architecture-level invariant; any candidate
   that touches them is invalid.
6. Phase-2 `sepolia`/`holesky` will be added by editing
   `build_send_eth.NETWORKS` (one PR that does also need a v1 change). This
   does technically modify `build_send_eth.py`, but it is a *Phase 2* event
   outside the v1 frozen window. The architecture documents the seam; the v1
   freeze applies to *Phase 1* delivery.
7. The "small scope" of the PRD justifies the seven-section split via ADR-001's
   future-change-axes argument; the splits are not gratuitous given the
   identified P1/P2 backlog.
8. The CLI subcommand name `transfer-from` (hyphenated) matches the PRD's
   user-facing examples; the internal Python function name is `do_transfer_from`
   (underscore). Both are stable contracts.
9. Operators run on macOS / Linux with Python 3.8+; Windows is not a v1 target
   (matches v1 posture).

---

## Architecture Quality Checklist

- [x] **No circular dependencies between modules** — verified via the dependency
  graph: Layer 1 has no internal deps; Layer 2 imports only Layer 1; Layer 3
  imports Layers 1–2; Layer 4 imports Layer 3. No peer imports within a layer.
- [x] **Each module has a single, clear responsibility** describable in one
  sentence — see Module Overview table.
- [x] **No shared databases** — N/A; no persistent state anywhere in this
  skill.
- [x] **All inter-module communication goes through defined interfaces** — the
  public-API tables document every cross-section call. Section-private helpers
  are leading-underscore.
- [x] **Every module can be tested in isolation with mocked dependencies** —
  `rpc` is injectable; ADR-009 mirrors module layout in test classes.
- [x] **Cross-cutting concerns are standardized** — error format, warning
  format, stdout/stderr discipline, structured logging style all documented in
  Cross-Cutting Concerns.
- [x] **Failure modes are defined** — each module section enumerates them.
- [x] **Service extraction path is clear** — Infrastructure & Deployment ›
  Service Extraction Path notes each module as "Ready now."
- [x] **Data flow is traceable** — Data Flow Diagrams cover the three happy
  paths plus the no-fallback failure path.
- [x] **Module count is justified** — ADR-001 maps each of the seven sections
  to a distinct PRD change axis.
- [x] **`build_send_eth.py` and `test_build_send_eth.py` are bit-for-bit
  unchanged** — hard constraint; ADR-002 specifies import-only reuse.
