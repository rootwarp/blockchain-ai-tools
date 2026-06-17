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
   4. For each result envelope, decode to an exact human amount with that token's
      decimals (see Precision). Handle per-entry errors per Error handling.

4. **Assemble and present** the combined report (see Output).
## Precision (exact decimal conversion)
## Network handling (ERC-20 is mainnet-only)
## Error handling
## Output
## Worked example
## Out of scope
