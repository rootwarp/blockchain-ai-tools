---
name: eth-ops
description: Use as the front door for Ethereum operations — both reading and moving funds. Reads (no signing): an account's holdings (native ETH + decoded ERC-20 balances USDT/USDC/stETH/eETH), a single balance, any eth_* read, or node diagnostics. Writes (gated): send ETH, ERC-20 transfer/approve/transferFrom, or broadcast a signed tx — eth-ops conducts build → sign → broadcast end-to-end with explicit human confirmation before signing and before broadcasting. Phrases like "send 0.1 ETH to…", "transfer 50 USDC", "approve…", "what does this address hold", "broadcast this raw tx". Orchestrates the eth-jsonrpc + eth-tx-builder skills and the eth-signer MCP signer; instructions-only (no bundled code).
---

# eth-ops

The front-door **orchestrator** for Ethereum operations. `eth-ops` classifies what you
want, then drives the right underlying skill/tool — answering reads directly and
conducting fund-moving operations through a gated `build → sign → broadcast` pipeline.

It delegates everything; it adds routing, confirmation gates, and clear presentation:

- **reads** → `eth-jsonrpc` (`balance`, `call`, `batch`, diagnostics)
- **build a tx** → `eth-tx-builder` (`build_send_eth.py`, `build_erc20.py`)
- **sign** → the `eth-signer` MCP tools (`sign_transaction`, `get_address`)
- **broadcast** → `eth-jsonrpc` (`broadcast`)

Instructions-only — no bundled code. It never builds, signs, broadcasts, or makes RPC
calls itself; it orchestrates the skills that do.

## Intent routing

Classify the request into one intent and act. When ambiguous, **ask** which is meant —
never guess, and **never escalate a read into a write**.

| Intent | Action | Gates |
|---|---|---|
| Holdings (ETH + decoded ERC-20) | `eth-jsonrpc` `balance` + `batch` — see Reads → Holdings | none |
| Single balance / generic `eth_*` read / diagnostics | `eth-jsonrpc` `balance` / `call` / `net-version` / `client-version` | none |
| Send ETH / ERC-20 transfer / approve / transferFrom | Fund-moving pipeline (build → sign → broadcast) | **two** |
| Build only (want the `TxRequest`, no sign) | `eth-tx-builder`, return JSON, stop | none |
| Broadcast only (have a signed raw tx) | `eth-jsonrpc` `broadcast` | one (before broadcast) |
| My address | `mcp__eth-signer__get_address` | none |

## Inputs (common)

Resolve and confirm before acting on any intent:

- **network** — `mainnet`, `hoodi`, `sepolia`, or `holesky`. **Never assume.** If not
  named, ask with `AskUserQuestion` (offer mainnet/hoodi/sepolia; holesky deprecated
  Sept 2025). The wrong chain silently produces misleading results or sends funds on the
  wrong network.
- **address / sender** — a **name** from `address_book.md` **or** a `0x` + 40-hex
  address. A literal `0x`+40-hex input is used **as-is** (a name never shadows it; it is
  reverse-annotated per the display rule); otherwise the token is a **name**, resolved to
  `0x` hex via *Resolve a name → address* (below), which **stops** on an unknown /
  duplicate / ambiguous-network result — it never guesses. For "me"/"my"/"the signer",
  resolve via the `mcp__eth-signer__get_address` tool (needs `eth-signer` MCP connected).
  For a read of someone else's account, use the name or address the user gives. If neither
  is available, **ask**.
- **scope** (holdings reads only) — `all` (default), `native`, or `tokens`.
- **operation specifics** (writes) — recipient/spender, amount, and (ERC-20) token
  address, per the Fund-moving pipeline.

### Resolve a name → address

The single, authoritative procedure for turning any address-typed input
(recipient / spender / sender / holder) that is **not** a literal `0x` address into a
`0x` hex address. It is the address-book analogue of the `ERC20.md` token-table read
above — a **parallel** inline procedure for address inputs; it **replaces nothing** in
that read. No code, no script. **Every other section that mentions resolution points
here; this is the one place it is defined.** Hard-code **zero** name→address pairs —
`address_book.md` is the runtime source of truth (mirroring the `ERC20.md` note that the
file, not this skill, holds the mappings).

A **STOP** means: do not resolve, do not build/sign/broadcast — surface the reason and
ask the user to correct or clarify. Steps are ordered so earlier ones gate later ones
(**load → validate → match → disambiguate → validate-address → carry forward**).

**Phase A — Load the table** (mirrors the `ERC20.md` token-table read):

1. **Read `address_book.md`** (repo root). Locate the single entries table by its header
   `| Name | Address | Network | Notes |` and the dashed delimiter row beneath it. **If
   the file does not exist, STOP** any name input with "no `address_book.md` found" —
   **never** fall through to treating the name as an address. (A literal `0x` input still
   works without a book.)
2. **Take every body row** and capture the four **positional** cells: `name`, `address`,
   `network`, `notes`. Trim surrounding ASCII whitespace. **Strip the backticks** from
   `address`.
3. **Normalize for matching:** `name` → ASCII-lowercase; `network` → ASCII-lowercase (an
   empty string stays empty = global).
4. **Skip-and-warn malformed rows** — do **not** hard-fail the whole book; one bad row
   must not disable resolution for the rest (mirrors the per-token "one bad token never
   sinks the whole report" resilience). A row is malformed (skip it, warn) if **any** of:
   - it does **not** have exactly **four cells** (a short/long row — usually a dropped or
     extra pipe);
   - `name` is empty, contains any character outside `[A-Za-z0-9._-]` (including internal
     whitespace, control, or zero-width/invisible characters), or is shaped like a
     `0x`+40-hex address (validating **stored** names too is what makes the duplicate
     check in B.6 sound);
   - `address` (after backtick-strip) is **not** exactly `0x` + 40 hex with no stray
     characters;
   - `network` is non-empty and **not** one of `mainnet` / `hoodi` / `sepolia` /
     `holesky` (case-insensitive).

   The surviving rows are the **valid candidate set** used for all matching below (and for
   reverse-annotation).

**Phase B — Resolve a name → address** (front door):

1. **Literal address wins, always (G-1).** If the input is **exactly** `0x` + 40 hex,
   **take it as-is** and **do not consult the book for resolution** (you may still
   reverse-annotate it per the display rule — a name **never** overrides a literal input).
   Otherwise, treat the input as a name and continue.
2. **Validate the name token *before* matching (G-2).** Trim outer ASCII whitespace;
   require the token **entirely** within `[A-Za-z0-9._-]`. **Reject** (treat as
   not-found; do **not** silently strip) any token with non-ASCII, internal whitespace,
   control, or zero-width/invisible characters. A `0x`+hex-shaped token is **not** a valid
   name → STOP ("that looks like an address, not a name").
3. **Match by ASCII-invariant case-fold only (G-3).** Compare the lowercased token to each
   candidate row's lowercased `name` using **ASCII lowercasing only** —
   locale-independent, **never** locale-aware or full-Unicode folding. Collect **all**
   candidate rows whose `name` matches.
4. **Exact match or STOP — never fuzzy (G-4).** If **no** row matches: **STOP** and
   report "name not found in `address_book.md`." **Never** fuzzy-match, pick a closest
   entry, autocomplete, or coerce the token into a raw address.
5. **Single global match → resolve.** If the matching set is exactly **one row** with an
   **empty** Network (global) and there are no named rows for that name → resolve to that
   row's address; go to B.7.
6. **Disambiguate by network — the `(Name, Network)` key (G-5, G-6).** If the matching set
   has **more than one row**, OR a single row that is **network-scoped** (named):
   1. **Resolve the operation's network first** — **never** assume it (the existing
      "never assume network" rule). If it is not yet known, determine it (ask via
      `AskUserQuestion`) **before** picking a row.
   2. **Forbidden-combination check:** if the matching set includes **both** a global row
      *and* one or more named rows for the same name → **STOP** (ambiguous by
      construction; the authoring invariant forbids it).
   3. **Filter** the matching set to rows whose `network` == the operation's network,
      then:
      - **exactly one** → resolve to it;
      - **zero** → **STOP** and report ("name has no entry for network `<net>`"); **do
        not** fall back to a global or other-network row;
      - **more than one** → **duplicate collision: STOP** and report the colliding rows;
        **never** pick first/last.
   4. A **single network-scoped row** whose `network` ≠ the operation's network → **STOP**
      (no fall-back).
7. **Validate the resolved (stored) address before use (G-7).** The resolved value must be
   exactly `0x` + 40 hex with no stray characters (Phase A.4 already enforced this;
   re-confirm). Then:
   - **mixed-case and fails EIP-55** → surface a **warning** but **proceed**:
     "`address_book.md` entry `<name>` has a non-EIP-55 checksum address — verify before
     sending." Treat EIP-55 as a **typo** check only, **never** anti-poisoning (a poisoned
     look-alike is a valid, correctly-checksummed address).
   - **all-lowercase or all-uppercase** → accept **silently** (no checksum information
     exists).
   - **canonical mixed case that passes** → accept silently.
8. **Carry the resolved EIP-55 address forward unchanged (C4).** Pass it to
   `eth-tx-builder` / `eth-jsonrpc` **exactly** as a literal address would be passed. The
   downstream tools — and the signer — never see the name.

**Phase C — Resolution decision summary** (every branch decided from the rows alone — no
heuristics; every ambiguity maps to **STOP**, never a guess):

| Condition (from the candidate rows) | Outcome |
|-------------------------------------|---------|
| Input is literal `0x`+40-hex | Use as-is; reverse-annotate; never resolve via book |
| `address_book.md` not present | **STOP** — "no address_book.md found" (no fall-through) |
| Name token fails charset / has invisible chars / is `0x`-shaped | **STOP** — "not a valid name / not found" |
| No row matches (ASCII-fold) | **STOP** — "name not found" (never fuzzy) |
| Exactly one **global** row matches, no named rows | Resolve to it |
| Exactly one **named** row matches, op network == it | Resolve to it |
| Exactly one named row matches, op network differs / unknown→resolve→differs | **STOP** (no fall-back) |
| Multiple rows; network filter → exactly one | Resolve to it |
| Multiple rows; network filter → zero | **STOP** (no global/other-network fall-back) |
| Multiple rows; network filter → >1 (same `(Name, Network)`) | **STOP** — duplicate collision |
| Matches include a global row **and** named rows for one name | **STOP** — forbidden combination |
| Resolved address mixed-case + fails EIP-55 | **WARN**, then resolve |
| Resolved address all-lower / all-upper / valid mixed | Resolve silently |

## Reads (no gates)

Reads never sign or broadcast. Confirm network/address per Inputs, then:

### Holdings (ETH + decoded ERC-20)

Give an address + network → native ETH balance plus decoded ERC-20 balances for the
curated `ERC20.md` tokens, with `scope = all|native|tokens`.

#### Scope

| scope    | native ETH | ERC-20 (ERC20.md) |
|----------|:----------:|:-----------------:|
| `all`    | ✓          | ✓ (mainnet only)  |
| `native` | ✓          | —                 |
| `tokens` | —          | ✓ (mainnet only)  |

Default is `all`. Honor an explicit user narrowing ("just my USDC", "only ETH").

#### Procedure

1. **Resolve** address, network, and scope per Inputs. The queried holder/address may be
   a **name**; resolve it to `0x` hex via *Resolve a name → address* first (same
   STOP-on-ambiguity rules). Validate the address is `0x` + 40 hex before any network
   call; on a bad address, stop with a clear message.

2. **Native ETH** (scope `all` or `native`) — run the `eth-jsonrpc` `balance` op:

   ```bash
   cd .claude/skills/eth-jsonrpc
   python3 eth_rpc.py balance --network <net> --address 0x<40hex>
   ```

   Capture `balanceWei` and `balanceEth` from the JSON it prints. If it exits
   non-zero / prints `error:`, surface that and stop the native section.

3. **ERC-20 balances** (scope `all` or `tokens`; **mainnet only** — see Network
   handling):
   1. **Read `ERC20.md`** (repo root) and take its token table: for each row capture
      `symbol`, `address`, and `decimals` (USDT 6, USDC 6, stETH 18, eETH 18).
   2. For each token, build `balanceOf` calldata:
      `0x70a08231` + `000000000000000000000000` + `<40-hex account, no 0x prefix>`.
   3. Send **one** batch — an `eth_call` per token — on mainnet:

      ```bash
      cd .claude/skills/eth-jsonrpc
      python3 eth_rpc.py batch --network mainnet --calls '[
        {"method":"eth_call","params":[{"to":"0xdAC17F958D2ee523a2206206994597C13D831ec7","data":"0x70a08231000000000000000000000000<ACCT>"},"latest"]},
        {"method":"eth_call","params":[{"to":"0xA0b86991c6218b36c1d19D4a2e9Eb0cE3606eB48","data":"0x70a08231000000000000000000000000<ACCT>"},"latest"]},
        {"method":"eth_call","params":[{"to":"0xae7ab96520DE3A18E5e111B5EaAb095312D7fE84","data":"0x70a08231000000000000000000000000<ACCT>"},"latest"]},
        {"method":"eth_call","params":[{"to":"0x35fA164735182de50811E8e2E824cFb9B6118ac2","data":"0x70a08231000000000000000000000000<ACCT>"},"latest"]}
      ]'
      ```

      (`<ACCT>` = the 40-hex account, no `0x`. Keep batch order = table order so each
      result maps back to its token by index.)

      The four `to` addresses above mirror `ERC20.md` (the runtime source of truth) —
      if `ERC20.md`'s token set changes, this template and the Worked example below
      must be updated to match.
   4. For each result envelope, decode to an exact human amount with that token's
      decimals (see Precision). Handle per-entry errors per Error handling.

4. **Assemble and present** the combined report (see Output).

#### Precision (exact decimal conversion)

A `balanceOf` result is a 32-byte hex integer (raw base units). Convert to a human
amount with **exact integer math** (never float — float loses precision at 18
decimals). Use this one-liner per result:

```bash
python3 -c "import sys;raw=int(sys.argv[1],16);d=int(sys.argv[2]);q,r=divmod(raw,10**d);print((f'{q}.{r:0{d}d}'.rstrip('0').rstrip('.')) if d and r else f'{q}')" <HEXRESULT> <DECIMALS>
```

Examples:
- `... 0x...1312d00 6` → `20` (20 USDT, exact)
- `... 0x...112210f47de98115 18` → `1.234567890123456789`
- a zero result (`0x0…0`) → `0`

`balanceOf` (not summed `Transfer` logs) is the source of truth for the current
balance — this is what makes the rebasing tokens (stETH, eETH) correct.

#### Network handling (ERC-20 is mainnet-only)

The `ERC20.md` addresses exist on **Ethereum mainnet only** — they do not exist (or
resolve to unrelated code) on hoodi/sepolia/holesky. Therefore:

- **scope `all` on a non-mainnet network** — report the native ETH balance, **skip the
  ERC-20 section**, and say so: "ERC-20 holdings are mainnet-only; skipped on <net>."
- **scope `tokens` on a non-mainnet network** — **stop** with a clear error explaining
  the mainnet-only constraint; do not query (the addresses are meaningless there).
- **scope `native`** — any network is fine.

When ERC-20 is in scope (per the rules above), always run the batch with
`--network mainnet`, regardless of where the native balance was read, and label the
report's token section "mainnet".

#### Error handling

- **`eth_rpc.py` failure** (non-zero exit / `error:` on stderr): surface it and stop
  that section. Never present a guessed or partial number as if it were real.
- **Per-token resilience:** a `batch` entry that comes back as an error envelope
  (`{"id":i,"error":{...}}`) marks only *that* token's balance as `<error: msg>` — the
  other tokens still report. One bad token never sinks the whole report.
- **Bad address:** reject `0x`+40-hex validation failures up front with a clear message.
- **Rebasing note:** stETH/eETH balances grow with rewards and have no per-rebase
  `Transfer` event; `balanceOf` is still exact, so no special handling is needed.

#### Output

Present a combined holdings report. Show the network prominently (so a wrong-chain
query is obvious), both raw and decoded for native ETH, and decoded amounts for each
token with its decimals; zero balances shown explicitly as `0`. **Annotate the queried
address with its book name** per the *Address-book display / reverse-annotation* rule
below — the header still carries the full address. Example shape (a booked address and
the un-booked variant):

````
Holdings for 0xABCD…1234 (alice)   (network: mainnet)

Native ETH
  1.234567890123456789 ETH   (1234567890123456789 wei)

ERC-20 tokens (ERC20.md, mainnet)
  USDT    1250.5            (decimals 6)
  USDC    0                 (decimals 6)
  stETH   3.5  (rebasing)   (decimals 18)
  eETH    <error: execution reverted>
````

If the queried address is not in the book, the header reads
`Holdings for 0xABCD…1234 (no book entry)   (network: mainnet)` instead. When scope is
`native` or `tokens`, show only that section.

#### Address-book display / reverse-annotation

The **single** rule for showing a book name beside any address — used by holdings and
single-balance reports above and, by pointer, at both gates and the broadcast-only route.
It is **annotation, not substitution**: the name is added *beside* the address; the full
checksummed address is **never** removed from an authorization surface.

- **Form:** `0x<4>…<4> (name)` — a **4+4-truncated, EIP-55-cased** address (the stored
  value is checksummed) followed by the book name in parentheses, e.g.
  `0xABCD…1234 (alice)`.
- **Truncation is a readability label, never a verification control** — it is **LOCKED at
  4 leading + 4 trailing hex**, applied uniformly everywhere. **Never** go below 4+4, and
  **never** drop the **full** address from a gate.
- **Un-booked address** → annotate **explicitly** as `0x<4>…<4> (no book entry)`, so a
  *missing* expected name is a **visible alarm**, not a silent omission.
- **Lookup:** reverse-look the address up against the loaded candidate rows (Phase A) by
  **case-insensitive hex compare**. When the operation's network is known and the matched
  row is network-scoped, prefer the row whose `Network` == the operation's network; if it
  matches **only** a row scoped to a *different* network, still surface the name but make
  the scope visible (e.g. `0xAbc…123 (beacon — book entry for mainnet)` on a sepolia
  read). This is a display refinement only; it never changes resolution.

### Single balance / generic read / diagnostics

- **Single ETH balance** — `eth-jsonrpc` `balance` (`eth_rpc.py balance --network <net> --address 0x<40hex>`); the queried holder/address may be a **name**, resolved to `0x` hex via *Resolve a name → address* first (same STOP-on-ambiguity rules). Present wei + ETH, reverse-annotating the queried address the same way (`Balance for 0xAbc…123 (alice)` / `Balance for 0xAbc…123 (no book entry)`) per the *Address-book display / reverse-annotation* rule above.
- **Any `eth_*` read** — `eth-jsonrpc` `call` (optionally `--decode`); for the method list and flags see `../eth-jsonrpc/SKILL.md`.
- **Diagnostics** — `eth-jsonrpc` `net-version` / `client-version`.

## Fund-moving pipeline (build → sign → broadcast)

For "send ETH", "transfer/approve/transferFrom an ERC-20", run all six steps in order.
**Stop immediately on any error** — never advance to sign or broadcast on a failed step.

1. **Resolve inputs** (per Inputs): network; **sender** via `mcp__eth-signer__get_address`
   (or explicit); recipient/spender; amount; and (ERC-20) the token contract address.
   - **recipient / spender / sender may each be a name**: resolve every one to `0x` hex
     via *Resolve a name → address* **before** building. **STOP the whole pipeline** on
     any unknown / duplicate / ambiguous-network / malformed result — **never** advance to
     build/sign. The resolved hex is passed to the builder unchanged.
   - Native amount goes to the builder in **gwei** (`--amount-gwei`); convert if the user
     speaks in ETH (1 ETH = 1e9 gwei). ERC-20 amount is **human-readable** (`--amount`);
     the builder applies token decimals.

2. **Build** → `eth-tx-builder` (it fetches the live nonce + fees and prints a complete
   `TxRequest` JSON to stdout):

   ```bash
   cd .claude/skills/eth-tx-builder
   # native ETH send:
   python3 build_send_eth.py --network <net> --to <recipient> --amount-gwei <gwei> --sender <from>
   # ERC-20 transfer:
   python3 build_erc20.py transfer --network <net> --token <token> --to <recipient> --amount <amt> --sender <from>
   ```

   For **approve** / **transferFrom** and advanced flags (`--approve-max`, `--revoke`,
   etc.), use the matching `build_erc20.py` subcommand (`approve`, `transfer-from` —
   the CLI uses `transfer-from`, not `transferFrom`) — see `../eth-tx-builder/SKILL.md`
   for its exact flags (eth-ops does not duplicate them). ERC-20 builds also print a
   human-readable **summary + warnings to stderr**; capture it for Gate 1.

3. **🚦 Gate 1 — before signing.** Present the transaction **decoded**, then require an
   explicit affirmative ("yes"/"confirm"); on anything else, **abort without signing**.
   - Each address shown (recipient / spender / sender) is reverse-annotated
     `to 0xAbc…123 (alice)` (or `(no book entry)`) per the *Address-book display /
     reverse-annotation* rule; the **full** decoded `0x…` address already on the gate is
     **retained** — never truncate *that*.
   - Native: `to`, value in **ETH** (and wei), `gas`, `maxFeePerGas` / `maxPriorityFeePerGas`
     (gwei), `nonce`, `chainId`.
   - ERC-20: the builder's stderr **summary** (function, token, recipient/spender, human
     amount, warnings) alongside the raw `TxRequest`.

4. **Sign** → call the `mcp__eth-signer__sign_transaction` tool with the `TxRequest`
   object from step 2. It returns `rawTransaction` (a `0x`-prefixed signed hex string).
   On error, surface it and **stop** (nothing has been broadcast).

5. **🚦 Gate 2 — before broadcasting.** Present the signed `rawTransaction` plus a
   restated decoded summary (network, to, value/token+amount). The restated address is
   reverse-annotated `to 0xAbc…123 (alice)` / `(no book entry)` per the *Address-book
   display / reverse-annotation* rule, with the **full** `0x…` retained. **For any
   new/edited book entry** (first send to it), keep the **full** recipient hex prominent
   and recommend a small test transfer first (first-send-unverified, G-10). **On
   `mainnet`, add an explicit "this moves real funds on Ethereum mainnet" callout.**
   Require a *second* explicit affirmative; on anything else, **abort without
   broadcasting** — hand the signed raw tx back to the user instead of sending it.

6. **Broadcast** → `eth-jsonrpc`:

   ```bash
   cd .claude/skills/eth-jsonrpc
   python3 eth_rpc.py broadcast --network <net> --raw-tx <rawTransaction> --wait --wait-timeout 120
   ```

   Report `txHash` always; with `--wait`, also `status` (mined/failed/pending),
   `blockNumber`, `gasUsed`, `effectiveGasPrice`. `status: failed` = included but
   reverted (still a real broadcast). `status: pending` = not mined within the timeout;
   offer to keep polling. (`--wait` can exceed the default Bash timeout — run it with a
   raised timeout or in the background.)

## Standalone routes

Sub-cases of the pipeline, for when the user only wants one step:

- **Build only** — user wants the `TxRequest` to inspect or sign elsewhere: run the
  `eth-tx-builder` build (pipeline step 2), present the JSON, and **stop** (no gates, no
  sign).
- **Broadcast only** — user already has a signed raw tx: go straight to **Gate 2**.
  eth-ops cannot independently decode an arbitrary signed blob, so present the **raw tx
  hex**, the **target network**, and whatever the user stated it does (recipient/amount).
  If the user states the recipient (by name or address), reverse-annotate it
  `to 0xAbc…123 (alice)` / `(no book entry)` per the *Address-book display /
  reverse-annotation* rule with the full value present — the annotation labels only what
  the user stated (eth-ops still cannot decode the blob). Add the mainnet "real funds"
  callout, and require an explicit "yes" confirming they are broadcasting *this exact raw
  tx to this network* (irreversible). Then broadcast (pipeline step 6). Never broadcast
  without that gate.
- **My address** — call `mcp__eth-signer__get_address` and report the address.

## Safety invariants

- A **read intent never triggers a write.**
- `mcp__eth-signer__sign_transaction` is **never** called without passing **Gate 1**;
  `eth_rpc.py broadcast` is **never** called without passing **Gate 2**. There is no
  auto-confirm path.
- Each gate shows **decoded, human-meaningful** details (not just opaque hex) so the
  user can verify what they authorize.
- **Any** delegated step that errors **stops** the flow; eth-ops never signs or
  broadcasts a transaction that did not build/sign cleanly.
- Network is always confirmed before acting. The `ERC20.md` curated list is mainnet-only
  for **holdings**; **builds** accept any token address the user names.
- eth-ops uses exactly the gas/nonce/fees `eth-tx-builder` produced — it never invents them.

**Address-book resolution safety (ordered).** Ordered so earlier rules gate later ones
(validate → match → disambiguate → display → curate); each is an unconditional STOP/rule,
not advice:

- **G-1 — Literal address wins, always.** An input that is exactly `0x` + 40 hex is taken
  as-is and is **never** resolved through, or overridden by, the book. A name can never
  shadow a literal address; address-shaped names are invalid in the book. The resolved hex
  is passed downstream unchanged — the builders, signer, and RPC layer never see a name.
- **G-2 — Validate the name token before matching.** Trim outer ASCII whitespace; require
  the token entirely within `[A-Za-z0-9._-]`. **Reject** (treat as not-found; do not
  silently strip) any token with non-ASCII, internal whitespace, control, or
  zero-width/invisible characters. A `0x`-hex-shaped token is not a valid name.
- **G-3 — Match by ASCII-invariant case-fold only.** Compare case-insensitively via ASCII
  lowercasing (locale-independent); **never** locale-aware or full-Unicode folding. Apply
  the same validation to **stored** names so an invisible-char pseudo-duplicate can't
  masquerade as a valid row.
- **G-4 — Exact match or STOP; never fuzzy.** No match → **stop** and report "name not
  found"; never fuzzy-match, pick a closest entry, or coerce the token into a raw address.
- **G-5 — Duplicate name = error.** Any name with more than one row after the network
  filter is an error: **stop** and report the colliding rows; never pick first/last.
- **G-6 — Resolve network first, then require exactly one row.** Determine the operation's
  network first (never assume it); a known network with **no** matching row → **stop** (no
  fall-back to a global/other-network row); never auto-choose among candidate network
  rows. A global row *and* named rows for the same name is ambiguous → **stop**.
- **G-7 — Validate the stored address before use.** Must be exactly `0x` + 40 hex, no
  stray characters. Mixed-case + fails EIP-55 → **warn but proceed**; treat EIP-55 as a
  **typo** check only, **never** anti-poisoning. All-lower/all-upper accepted silently.
- **G-8 — Show the name at every authorization point, with the full hex.** Annotate the
  address with its book name (or `(no book entry)`) in holdings/balance reports and at
  **both** gates, including the broadcast-only route; the **full** checksummed address is
  always also present. The book annotates — the gates with full decoded details remain the
  real backstop.
- **G-9 — Curate addresses only from a verified, independent source.** Add/edit an address
  **only** after verifying it against a trusted independent source; **never** paste an
  address from transaction history or a block-explorer "from" field.
- **G-10 — Treat any not-known-verified entry as unverified (fail-safe).** `eth-ops` is
  stateless and cannot reliably tell whether an entry is new or edited since last use, so
  default to unverified: unless you positively know an entry is verified, keep the **full**
  recipient hex prominent at Gate 2 and recommend a small test transfer before a full-value
  send — never skip the nudge on the assumption it is already trusted.

## Worked examples

### Read — holdings on mainnet (captured live)

Balances are time-varying (they change block to block); this example demonstrates the
correct procedure and output shape, not fixed values.

**Target address:** `0xd8dA6BF26964aF9D7eEd9e03E53415D37aA96045` (vitalik.eth)
**Network:** mainnet  **Scope:** all

---

**Step A — native ETH balance:**

```bash
cd .claude/skills/eth-jsonrpc
python3 eth_rpc.py balance --network mainnet --address 0xd8dA6BF26964aF9D7eEd9e03E53415D37aA96045
```

Captured output:
```json
{
  "network": "mainnet",
  "chainId": "1",
  "address": "0xd8dA6BF26964aF9D7eEd9e03E53415D37aA96045",
  "blockTag": "latest",
  "balanceWei": "5690705707505023098",
  "balanceEth": "5.690705707505023098"
}
```

---

**Step B — ERC-20 batch (one `eth_call` per token, single HTTP request):**

```bash
cd .claude/skills/eth-jsonrpc
python3 eth_rpc.py batch --network mainnet --calls '[
  {"method":"eth_call","params":[{"to":"0xdAC17F958D2ee523a2206206994597C13D831ec7","data":"0x70a08231000000000000000000000000d8da6bf26964af9d7eed9e03e53415d37aa96045"},"latest"]},
  {"method":"eth_call","params":[{"to":"0xA0b86991c6218b36c1d19D4a2e9Eb0cE3606eB48","data":"0x70a08231000000000000000000000000d8da6bf26964af9d7eed9e03e53415d37aa96045"},"latest"]},
  {"method":"eth_call","params":[{"to":"0xae7ab96520DE3A18E5e111B5EaAb095312D7fE84","data":"0x70a08231000000000000000000000000d8da6bf26964af9d7eed9e03e53415d37aa96045"},"latest"]},
  {"method":"eth_call","params":[{"to":"0x35fA164735182de50811E8e2E824cFb9B6118ac2","data":"0x70a08231000000000000000000000000d8da6bf26964af9d7eed9e03e53415d37aa96045"},"latest"]}
]'
```

Captured output (id 0=USDT, 1=USDC, 2=stETH, 3=eETH):
```json
[
  {"id": 0, "result": "0x00000000000000000000000000000000000000000000000000000000114cce4b"},
  {"id": 1, "result": "0x0000000000000000000000000000000000000000000000000000000001dc7d01"},
  {"id": 2, "result": "0x00000000000000000000000000000000000000000000000000000968428753d6"},
  {"id": 3, "result": "0x0000000000000000000000000000000000000000000000000000000000000000"}
]
```

---

**Step C — decode each hex result to human amount (exact integer math):**

```bash
# USDT, decimals 6
python3 -c "import sys;raw=int(sys.argv[1],16);d=int(sys.argv[2]);q,r=divmod(raw,10**d);print((f'{q}.{r:0{d}d}'.rstrip('0').rstrip('.')) if d and r else f'{q}')" 0x00000000000000000000000000000000000000000000000000000000114cce4b 6
# → 290.246219

# USDC, decimals 6
python3 -c "import sys;raw=int(sys.argv[1],16);d=int(sys.argv[2]);q,r=divmod(raw,10**d);print((f'{q}.{r:0{d}d}'.rstrip('0').rstrip('.')) if d and r else f'{q}')" 0x0000000000000000000000000000000000000000000000000000000001dc7d01 6
# → 31.227137

# stETH, decimals 18
python3 -c "import sys;raw=int(sys.argv[1],16);d=int(sys.argv[2]);q,r=divmod(raw,10**d);print((f'{q}.{r:0{d}d}'.rstrip('0').rstrip('.')) if d and r else f'{q}')" 0x00000000000000000000000000000000000000000000000000000968428753d6 18
# → 0.000010343397413846

# eETH, decimals 18
python3 -c "import sys;raw=int(sys.argv[1],16);d=int(sys.argv[2]);q,r=divmod(raw,10**d);print((f'{q}.{r:0{d}d}'.rstrip('0').rstrip('.')) if d and r else f'{q}')" 0x0000000000000000000000000000000000000000000000000000000000000000 18
# → 0
```

---

**Assembled holdings report:**

```
Holdings for 0xd8dA6BF26964aF9D7eEd9e03E53415D37aA96045   (network: mainnet)

Native ETH
  5.690705707505023098 ETH   (5690705707505023098 wei)

ERC-20 tokens (ERC20.md, mainnet)
  USDT    290.246219              (decimals 6)
  USDC    31.227137               (decimals 6)
  stETH   0.000010343397413846   (rebasing)   (decimals 18)
  eETH    0                      (rebasing)   (decimals 18)
```

### Write — native send on hoodi (build → gates → broadcast)

Sending `0.001 ETH` (= `1000000` gwei) to `0x…dEaD` on hoodi. Amounts/nonce are
time-varying; this shows the flow, not fixed values.

1. Build:
   ```bash
   cd .claude/skills/eth-tx-builder
   python3 build_send_eth.py --network hoodi --to 0x000000000000000000000000000000000000dEaD --amount-gwei 1000000 --sender 0xd8dA6BF26964aF9D7eEd9e03E53415D37aA96045
   ```
   → `TxRequest`:
   ```json
   {
     "type": "eip1559",
     "chainId": "560048",
     "nonce": "0",
     "to": "0x000000000000000000000000000000000000dEaD",
     "value": "1000000000000000",
     "data": "0x",
     "gas": "21000",
     "maxFeePerGas": "1985761842",
     "maxPriorityFeePerGas": "54413832"
   }
   ```
2. **🚦 Gate 1** — eth-ops presents: to `0x…dEaD`, value `0.001 ETH`, gas `21000`,
   maxFee `1985761842` wei (~1.986 gwei), nonce `0`, chainId `560048`. User confirms → proceed.
3. Sign: `mcp__eth-signer__sign_transaction(<TxRequest>)` → `rawTransaction` `0x02f8…`.
4. **🚦 Gate 2** — eth-ops presents the signed `0x02f8…` + summary (hoodi, 0.001 ETH →
   0x…dEaD). User confirms → proceed. (On mainnet this step adds a real-funds callout.)
5. Broadcast:
   ```bash
   cd .claude/skills/eth-jsonrpc
   python3 eth_rpc.py broadcast --network hoodi --raw-tx 0x02f8… --wait --wait-timeout 120
   ```
   → `{ "txHash": "0x…", "status": "mined", "blockNumber": …, "gasUsed": …, "effectiveGasPrice": … }`

(Steps 3–5 outputs are illustrative — the example shows the gated flow; a real
end-to-end broadcast is operator-driven and optional.)

### Address-book resolution (success + failure paths)

These demonstrate the *Resolve a name → address* behavior and the G-1…G-10 invariants.
All name→address pairs below are **example-only** — never a runtime source of truth (the
live book is `address_book.md`); amounts/nonces are time-varying.

**(W1) Success — name → send, reverse name at BOTH gates (hoodi):**

```text
User: send 0.001 ETH to alice on hoodi

eth-ops:
  • Resolve "alice" → exactly one global row → 0xAbc1…d123 (EIP-55 OK).
  • Build (eth-tx-builder), then:

  🚦 Gate 1 (before signing)
    to     0xAbc1…d123 (alice)     [full: 0xAbc1...EXAMPLE...d123]
    value  0.001 ETH  (1000000000000000 wei)
    gas 21000 · maxFee … · nonce … · chainId 560048
    → confirm? yes

  Sign → rawTransaction 0x02f8…

  🚦 Gate 2 (before broadcasting)
    network hoodi
    to     0xAbc1…d123 (alice)     [full: 0xAbc1...EXAMPLE...d123]
    value  0.001 ETH
    raw    0x02f8…
    → confirm broadcast this exact tx? yes
  Broadcast → txHash 0x… (status mined)
```

**(W2) Success — name → holdings read (mainnet):**

```text
User: what does vitalik hold

eth-ops:
  • Resolve "vitalik" → global row → 0xd8dA6BF2…A96045.
  • Run holdings (native + ERC20.md batch), then annotate the header:

  Holdings for 0xd8dA…6045 (vitalik)   (network: mainnet)
  Native ETH  5.690705707505023098 ETH  (…wei)
  ERC-20 tokens (ERC20.md, mainnet)
    USDT 290.246219 · USDC 31.227137 · stETH 0.0000103… · eETH 0
```

**(W3) Failure — unknown name (typo) STOPS, no fall-through (G-4):**

```text
User: send 0.05 ETH to alise        # typo for "alice"

eth-ops:
  • Validate "alise" → valid token shape.
  • Match → NO row matches.
  • STOP: "No entry 'alise' in address_book.md. I won't guess or fuzzy-match a
    close name, and I won't treat it as a raw address. Did you mean a name in the
    book, or paste the 0x address?"
  (nothing built, signed, or broadcast)
```

**(W4) Failure — duplicate name = error, not a silent pick (G-5):**

```text
Book has two GLOBAL rows named "treasury" (authoring-invariant violation):
  | treasury | 0x1111…1111 | | old |
  | treasury | 0x2222…2222 | | new |

User: send 100 USDC to treasury on mainnet

eth-ops:
  • Match "treasury" → 2 rows, same effective network (both global).
  • STOP: "address_book.md is ambiguous: 'treasury' matches 2 rows
    (0x1111…1111 and 0x2222…2222). I won't pick one. Disambiguate the book
    (unique name, or use the Network column) or paste the 0x address."
```

**(W5) Failure — network mismatch STOPS, no fall-back to another network (G-6):**

```text
Book:
  | beacon | 0x…mainnet… | mainnet | per-chain |
  | beacon | 0x…sepolia… | sepolia | per-chain |

User: send 0.1 ETH to beacon on holesky

eth-ops:
  • Match "beacon" → 2 network-scoped rows (mainnet, sepolia).
  • Operation network = holesky (stated). Filter to network == holesky → ZERO rows.
  • STOP: "'beacon' has no entry for holesky (only mainnet, sepolia). I won't
    fall back to another network's address." (decision-table: multiple rows,
    network filter → zero → STOP)
```

**(W6) Warning — bad EIP-55 checksum warns then proceeds (G-7):**

```text
Book: | carol | 0xAbCdEf…WRONGCASE…1234 | | mixed-case, fails EIP-55 |

User: send 0.01 ETH to carol on hoodi

eth-ops:
  • Resolve "carol" → single global row.
  • Validate stored address: mixed-case AND fails EIP-55.
  • WARN: "address_book.md entry 'carol' has a non-EIP-55 checksum address —
    verify before sending (this is a typo check only, not proof it's the right
    address)." → still resolves.
  🚦 Gate 1 / Gate 2 show  to 0xAbCd…1234 (carol)  with the FULL hex; gates
  remain the backstop.
```

## Out of scope

- Implementing building, signing, broadcasting, or RPC transport **itself** — all
  delegated to `eth-tx-builder`, the `eth-signer` MCP signer, and `eth-jsonrpc`. eth-ops
  adds only routing, gating, and presentation.
- Key custody (the `eth-signer` MCP server holds the keys).
- Multi-transaction batching/queuing; gas-strategy tuning beyond what `eth-tx-builder`
  does; multi-account or multi-network fan-out in one request.
- Ad-hoc token *holdings* discovery and non-mainnet ERC-20 **holdings** (the curated
  `ERC20.md` list is mainnet-only). Ad-hoc token **sends** are fine — the builder takes
  any token address.
