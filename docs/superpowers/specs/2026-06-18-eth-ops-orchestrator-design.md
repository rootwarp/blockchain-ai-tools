# eth-ops orchestrator skill — design

- **Date:** 2026-06-18
- **Status:** Approved (brainstorming), pending implementation plan
- **Type:** Skill redesign + rename (instructions-only)
- **Supersedes:** `docs/superpowers/specs/2026-06-17-eth-query-design.md` (the
  reads-only `eth-query` holdings reader). That skill is renamed and broadened here.

## Context & motivation

`eth-query` was built this session as an instructions-only **holdings reader**
(native ETH + decoded ERC-20 balances) orchestrating only `eth-jsonrpc`. The user
wants it promoted to a **full operations orchestrator** — a single front door that
also drives transaction building, signing, and broadcasting by delegating to the
other skills/tools. Because it now writes as well as reads, the name `eth-query`
("query" implies read-only) is replaced by **`eth-ops`**.

The repo already has the pieces; nothing about them changes:

- **`eth-jsonrpc`** — reads (`balance`, `call`, `batch`, diagnostics) and `broadcast`
  of a signed raw tx.
- **`eth-tx-builder`** — builds a ready-to-sign `TxRequest` JSON. `build_send_eth.py`
  (native) and `build_erc20.py {transfer,approve,transfer-from}` (ERC-20). **It
  already queries the RPC for the live nonce (`eth_getTransactionCount … pending`)
  and fees (baseFee + `eth_maxPriorityFeePerGas`)**, so its output is complete and
  broadcast-ready — the orchestrator does not pre-fetch nonce/gas.
- **`eth-signer` MCP** — `mcp__eth-signer__sign_transaction(TxRequest) → rawTransaction`
  and `mcp__eth-signer__get_address() → address`.

`eth-ops` is the conductor that wires `build → sign → broadcast` together, with human
confirmation gates, and answers reads directly.

## Goals

- One front-door skill that **classifies user intent** and dispatches to the right
  underlying skill/tool.
- **Conduct the full fund-moving pipeline** end-to-end (build → sign → broadcast →
  confirm) with **two explicit human confirmation gates**.
- Keep answering **reads** (holdings, single balance, generic `eth_*`, diagnostics)
  directly via `eth-jsonrpc`.
- Remain **instructions-only** — no bundled Python, no tests. Pure orchestration recipe.

## Non-goals

- No new transaction logic, ABI encoding, signing, or RPC transport of its own — all
  delegated. `eth-ops` adds routing + gating + presentation only.
- No custody of keys (signing is the `eth-signer` MCP server's job).
- No multi-tx batching/queuing, no gas-strategy tuning beyond what `eth-tx-builder`
  already does, no multi-account or multi-network fan-out in one request.

## Identity & intent routing

`eth-ops` reads the user's request, classifies it into one intent, and acts. Routing
table (goes near the top of `SKILL.md`):

| Intent | Action | Gates |
|---|---|---|
| **Holdings** (ETH + decoded ERC-20) | `eth-jsonrpc` `balance` + `batch` (the retained `eth-query` holdings procedure) | none (read) |
| **Single balance / generic `eth_*` read / diagnostics** | `eth-jsonrpc` `balance` / `call` / `net-version` / `client-version` | none (read) |
| **Send ETH / ERC-20 transfer / approve / transferFrom** | **full pipeline** (build → sign → broadcast) | **two** |
| **Build only** (user wants the `TxRequest`, no sign) | `eth-tx-builder`, return JSON, stop | none |
| **Broadcast only** (user has a signed raw tx) | `eth-jsonrpc broadcast` | one (before broadcast) |
| **My address** | `mcp__eth-signer__get_address` | none |

When the intent is ambiguous, `eth-ops` asks which the user means rather than guessing
— and **never** escalates a read into a write.

## Fund-moving pipeline (the conductor core)

Example trigger: "send 0.1 ETH to 0xABC… on hoodi" / "transfer 50 USDC to 0xABC… on
mainnet".

1. **Resolve inputs.**
   - **network** — ask if not named; never assume (same discipline as the other skills).
   - **sender** — `mcp__eth-signer__get_address` for "me"/"my", else an explicit
     address. If the signer MCP is unavailable and no explicit sender, stop and say so.
   - **recipient/spender**, **amount**, and (ERC-20) **token address**.
   - Native amount is passed to the builder in **gwei** (`--amount-gwei`); ERC-20
     amount is **human-readable** (`--amount`, builder applies token decimals).
2. **Build** → `eth-tx-builder`:
   - Native: `python3 build_send_eth.py --network <net> --to <to> --amount-gwei <g> --sender <from>`
   - ERC-20 transfer: `python3 build_erc20.py transfer --network <net> --token <tok> --to <to> --amount <amt> --sender <from>`
   - approve / transfer-from: the matching subcommand + its flags.
   - stdout = the `TxRequest` JSON (used in step 4). For ERC-20, **stderr carries a
     human-readable summary + warnings** (e.g. approve-race) — capture it for Gate 1.
   - Any build error → surface and **stop** (do not proceed to sign).
3. **🚦 Gate 1 — before signing.** Present the **fully decoded** transaction:
   - Native: `to`, value in **ETH** (and wei), `gas`, `maxFeePerGas`/`maxPriorityFeePerGas`
     (gwei), `nonce`, `chainId`.
   - ERC-20: the `eth-tx-builder` stderr **summary** (function, token, recipient/spender,
     human amount, any warnings) plus the raw `TxRequest`.
   - Require an explicit affirmative ("yes"/"confirm"). Anything else → **abort, do not
     sign**.
4. **Sign** → `mcp__eth-signer__sign_transaction(<TxRequest>)` → `rawTransaction` hex.
   - Sign error → surface and **stop** (nothing was broadcast).
5. **🚦 Gate 2 — before broadcasting.** Present the signed `rawTransaction` + a
   re-stated decoded summary (to/value/token/amount/network). On **mainnet**, add an
   explicit **"this moves real funds on Ethereum mainnet"** callout. Require a second
   explicit affirmative. Anything else → **abort, do not broadcast** (the signed tx is
   discarded / offered back to the user, never sent).
6. **Broadcast** → `eth-jsonrpc`:
   `python3 eth_rpc.py broadcast --network <net> --raw-tx <hex> --wait [--wait-timeout N]`
   Report `txHash` always; with `--wait`, also `status` (mined/failed/pending),
   `blockNumber`, `gasUsed`, `effectiveGasPrice`. `status: failed` = included but
   reverted (still a real broadcast); `pending` = not mined in time, offer to keep
   polling.

## Read intents (no gates)

- **Holdings** — the existing `eth-query` procedure verbatim: resolve address+network,
  `eth-jsonrpc balance` for ETH, `eth-jsonrpc batch` of `balanceOf` `eth_call`s for the
  `ERC20.md` tokens (mainnet-only), decode with the exact integer one-liner. Combined
  report with `scope = all|native|tokens`.
- **Single balance** — `eth-jsonrpc balance`.
- **Generic `eth_*` read** — `eth-jsonrpc call` (optionally `--decode`).
- **Diagnostics** — `eth-jsonrpc net-version` / `client-version`.

## Safety invariants

- A **read intent never triggers a write.**
- `sign_transaction` is **never** called without passing Gate 1; `broadcast` is
  **never** called without passing Gate 2. No "auto-confirm" path exists.
- Each gate shows decoded, human-meaningful details (not just opaque hex) so the user
  can verify *what* they are authorizing.
- **Any** underlying step that errors stops the pipeline; `eth-ops` never broadcasts a
  tx that did not build and sign cleanly.
- Network is always confirmed before acting. The curated `ERC20.md` token list is
  mainnet-only for holdings; **builds** accept any token address the user names
  (`eth-tx-builder` handles arbitrary tokens).
- `eth-ops` does not invent gas/nonce/fees — it uses exactly what `eth-tx-builder`
  produced.

## Rename mechanics (`eth-query` → `eth-ops`)

- Move the skill directory `.claude/skills/eth-query/` → `.claude/skills/eth-ops/`
  (preserve git history via `git mv`).
- Update `SKILL.md` frontmatter `name: eth-ops` and rewrite its `description` to a
  read+write orchestrator trigger (holdings/balance **and** "send/transfer/approve/
  broadcast" phrasing), disambiguating it as the front door over `eth-jsonrpc` +
  `eth-tx-builder` + the signer.
- Update the cross-links that currently point at `eth-query`:
  - `.claude/skills/eth-jsonrpc/README.md` (the "Realized by the `eth-query` skill …"
    note) → `eth-ops`.
  - `ERC20.md` (the "use the `eth-query` skill" sentence) → `eth-ops`.
- Update `README.md` content + internal paths.
- Update the memory pointer (`eth-query-skill.md` / `MEMORY.md`) to `eth-ops`.
- The old spec/plan filenames stay (historical); this spec records the supersession.

## File structure (after)

```
.claude/skills/eth-ops/
  SKILL.md     # frontmatter + intent routing + pipeline (2 gates) + read procedures + safety
  README.md    # human-facing overview of the orchestrator
```

`SKILL.md` is rewritten from the `eth-query` holdings doc: the holdings content is
retained as the "Holdings" read branch; the routing table, fund-moving pipeline, gates,
standalone routes, and safety invariants are new. No bundled code.

## Verification

Instructions-only, so no automated tests. Verification during implementation:

- **Reads** — re-run the holdings + balance flows on mainnet/hoodi (read-only; the
  existing worked example still holds).
- **Build** — run `eth-tx-builder` for a native and an ERC-20 op on **hoodi** (testnet)
  and confirm `eth-ops`'s Gate-1 decoded presentation matches the `TxRequest`.
- **Pipeline dry-run** — exercise build → Gate 1 → `sign_transaction` → Gate 2 on
  **hoodi**, stopping at/declining Gate 2 (so nothing is broadcast) to prove the gates
  hold. A full broadcast e2e is **optional** and operator-driven (needs a funded hoodi
  account); the broadcast step itself is already covered by `eth-jsonrpc`.
- `SKILL.md` carries a worked **read** example and a narrated **pipeline** example
  (build → gates → broadcast) using hoodi values.

## Out of scope

- Building/signing/broadcasting logic itself (delegated, unchanged).
- Key custody; multi-tx/batch sending; gas-strategy tuning; multi-account/multi-network
  fan-out.
- Non-mainnet ERC-20 **holdings** (curated list is mainnet-only); ad-hoc token
  *holdings* discovery. (Ad-hoc token *sends* are fine — the builder takes any token.)
