---
name: eth-query
description: Use when the user wants an Ethereum account's holdings — its native ETH balance and/or its ERC-20 token balances (USDT/USDC/stETH/eETH) decoded to human amounts. Phrases like "what does this address hold", "show balances", "token/portfolio balances", "USDC balance". A high-level combined reader built on the eth-jsonrpc skill (balance + batch) and the ERC20.md token list. Reads only; does not sign, build, or broadcast. ERC-20 balances are Ethereum-mainnet only.
---

# eth-query

A high-level **holdings reader**: given an address + network, report the native ETH
balance and the decoded ERC-20 balances for the curated `ERC20.md` tokens
(USDT/USDC/stETH/eETH) in one combined view. It is the user-friendly layer above
`eth-jsonrpc`, which exposes only raw (un-decoded) balances and leaves ERC-20
decimals/ABI decoding out of scope.

This skill **reads only** — it never signs (`sign_transaction` MCP tool), builds
(`eth-tx-builder`), or broadcasts (`eth-jsonrpc broadcast`). It does not bundle code;
it orchestrates `eth-jsonrpc`'s `eth_rpc.py` and reads `ERC20.md` at runtime.

## Inputs

- **address** — `0x` + 40 hex. If the user names one, use it. If they mean their own
  / the signer's account ("my holdings", "the signer's balances"), resolve it by
  calling the `get_address` MCP tool (needs `eth-signer-mcp` connected). If no address
  and no self-reference, **ask** — never guess.
- **network** — `mainnet`, `hoodi`, `sepolia`, or `holesky`. **Never assume.** If not
  named, ask with `AskUserQuestion` (offer mainnet/hoodi/sepolia; holesky deprecated
  Sept 2025, offer on request). Picking the wrong chain silently returns a misleading
  result. Note: **ERC-20 balances require mainnet** (see Network handling).
- **scope** — `all` (default), `native` (ETH only), or `tokens` (ERC-20 only).

## Scope

| scope    | native ETH | ERC-20 (ERC20.md) |
|----------|:----------:|:-----------------:|
| `all`    | ✓          | ✓ (mainnet only)  |
| `native` | ✓          | —                 |
| `tokens` | —          | ✓ (mainnet only)  |

Default is `all`. Honor an explicit user narrowing ("just my USDC", "only ETH").

## Procedure

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

## Precision (exact decimal conversion)

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

## Network handling (ERC-20 is mainnet-only)

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

## Error handling

- **`eth_rpc.py` failure** (non-zero exit / `error:` on stderr): surface it and stop
  that section. Never present a guessed or partial number as if it were real.
- **Per-token resilience:** a `batch` entry that comes back as an error envelope
  (`{"id":i,"error":{...}}`) marks only *that* token's balance as `<error: msg>` — the
  other tokens still report. One bad token never sinks the whole report.
- **Bad address:** reject `0x`+40-hex validation failures up front with a clear message.
- **Rebasing note:** stETH/eETH balances grow with rewards and have no per-rebase
  `Transfer` event; `balanceOf` is still exact, so no special handling is needed.

## Output

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

## Worked example

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

## Out of scope

- Ad-hoc / arbitrary token addresses and on-chain `decimals()` discovery — `ERC20.md`
  list only.
- ERC-20 balances on non-mainnet networks.
- Signing (`sign_transaction`), building (`eth-tx-builder`), broadcasting
  (`eth-jsonrpc broadcast`), or any general `eth_*` passthrough (`eth-jsonrpc call`).
- Multi-address or multi-network fan-out in one invocation.
