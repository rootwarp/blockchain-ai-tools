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
- **address / sender** — `0x` + 40 hex. For "me"/"my"/"the signer", resolve via the
  `mcp__eth-signer__get_address` tool (needs `eth-signer` MCP connected). For a read of
  someone else's account, use the address the user names. If neither is available, **ask**.
- **scope** (holdings reads only) — `all` (default), `native`, or `tokens`.
- **operation specifics** (writes) — recipient/spender, amount, and (ERC-20) token
  address, per the Fund-moving pipeline.

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

1. **Resolve** address, network, and scope per Inputs. Validate the address is `0x` +
   40 hex before any network call; on a bad address, stop with a clear message.

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
token with its decimals; zero balances shown explicitly as `0`. Example shape:

````
Holdings for 0xABCD…1234   (network: mainnet)

Native ETH
  1.234567890123456789 ETH   (1234567890123456789 wei)

ERC-20 tokens (ERC20.md, mainnet)
  USDT    1250.5            (decimals 6)
  USDC    0                 (decimals 6)
  stETH   3.5  (rebasing)   (decimals 18)
  eETH    <error: execution reverted>
````

When scope is `native` or `tokens`, show only that section.

### Single balance / generic read / diagnostics

- **Single ETH balance** — `eth-jsonrpc` `balance` (`eth_rpc.py balance --network <net> --address 0x<40hex>`); present wei + ETH.
- **Any `eth_*` read** — `eth-jsonrpc` `call` (optionally `--decode`); for the method list and flags see `../eth-jsonrpc/SKILL.md`.
- **Diagnostics** — `eth-jsonrpc` `net-version` / `client-version`.

## Fund-moving pipeline (build → sign → broadcast)

For "send ETH", "transfer/approve/transferFrom an ERC-20", run all six steps in order.
**Stop immediately on any error** — never advance to sign or broadcast on a failed step.

1. **Resolve inputs** (per Inputs): network; **sender** via `mcp__eth-signer__get_address`
   (or explicit); recipient/spender; amount; and (ERC-20) the token contract address.
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
   etc.), use the matching `build_erc20.py` subcommand — see `../eth-tx-builder/SKILL.md`
   for its exact flags (eth-ops does not duplicate them). ERC-20 builds also print a
   human-readable **summary + warnings to stderr**; capture it for Gate 1.

3. **🚦 Gate 1 — before signing.** Present the transaction **decoded**, then require an
   explicit affirmative ("yes"/"confirm"); on anything else, **abort without signing**.
   - Native: `to`, value in **ETH** (and wei), `gas`, `maxFeePerGas` / `maxPriorityFeePerGas`
     (gwei), `nonce`, `chainId`.
   - ERC-20: the builder's stderr **summary** (function, token, recipient/spender, human
     amount, warnings) alongside the raw `TxRequest`.

4. **Sign** → call the `mcp__eth-signer__sign_transaction` tool with the `TxRequest`
   object from step 2. It returns `rawTransaction` (a `0x`-prefixed signed hex string).
   On error, surface it and **stop** (nothing has been broadcast).

5. **🚦 Gate 2 — before broadcasting.** Present the signed `rawTransaction` plus a
   restated decoded summary (network, to, value/token+amount). **On `mainnet`, add an
   explicit "this moves real funds on Ethereum mainnet" callout.** Require a *second*
   explicit affirmative; on anything else, **abort without broadcasting** — hand the
   signed raw tx back to the user instead of sending it.

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
- **Broadcast only** — user already has a signed raw tx: go straight to **Gate 2**
  (present the decoded signed tx, mainnet callout, require "yes"), then broadcast
  (pipeline step 6). Never broadcast without that gate.
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
