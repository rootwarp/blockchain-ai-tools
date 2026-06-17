# ERC-20 Token List

Curated set of ERC-20 contracts to track per account. Used with the `eth-jsonrpc`
skill's `batch` + `eth_call balanceOf` workflow to read balances directly (no
log-scan discovery needed) — see [Usage](#usage-with-eth-jsonrpc).

> **Network: Ethereum mainnet (chainId 1).** These addresses exist on mainnet
> only. On hoodi/sepolia/holesky they do not exist (or live at different
> test-deployment addresses) — do not reuse them against a testnet endpoint.

## Tokens

| Token | Symbol | Contract address | Decimals | Issuer | Notes |
|-------|--------|------------------|----------|--------|-------|
| Tether USD | USDT | `0xdAC17F958D2ee523a2206206994597C13D831ec7` | 6 | Tether | |
| USD Coin | USDC | `0xA0b86991c6218b36c1d19D4a2e9Eb0cE3606eB48` | 6 | Circle | |
| Lido Staked Ether | stETH | `0xae7ab96520DE3A18E5e111B5EaAb095312D7fE84` | 18 | Lido | rebasing — `balanceOf` grows with rewards, no `Transfer` event per rebase |
| ether.fi ETH | eETH | `0x35fA164735182de50811E8e2E824cFb9B6118ac2` | 18 | ether.fi | rebasing — same as stETH |

Addresses verified against each token's Etherscan token page (see [Sources](#sources)).

## Usage with `eth-jsonrpc`

`balanceOf(address)` is an `eth_call`. Build the calldata as the 4-byte selector
`0x70a08231` followed by the 32-byte left-padded account address, then batch one
call per token in a single request.

Calldata = `0x70a08231` + `000000000000000000000000` + `<40-hex account, no 0x>`

For a one-shot, decoded holdings report (ETH + all tokens below, human amounts), use
the `eth-query` skill, which automates this batch + decimals workflow.

```bash
# Replace <ACCT> with the 40-hex account address (no 0x prefix) in each "data".
cd .claude/skills/eth-jsonrpc
python3 eth_rpc.py batch --network mainnet --calls '[
  {"method": "eth_call", "params": [{"to": "0xdAC17F958D2ee523a2206206994597C13D831ec7", "data": "0x70a08231000000000000000000000000<ACCT>"}, "latest"]},
  {"method": "eth_call", "params": [{"to": "0xA0b86991c6218b36c1d19D4a2e9Eb0cE3606eB48", "data": "0x70a08231000000000000000000000000<ACCT>"}, "latest"]},
  {"method": "eth_call", "params": [{"to": "0xae7ab96520DE3A18E5e111B5EaAb095312D7fE84", "data": "0x70a08231000000000000000000000000<ACCT>"}, "latest"]},
  {"method": "eth_call", "params": [{"to": "0x35fA164735182de50811E8e2E824cFb9B6118ac2", "data": "0x70a08231000000000000000000000000<ACCT>"}, "latest"]}
]'
```

Each result is a 32-byte hex integer (the raw base-unit balance). Convert to a
human amount with the **Decimals** column: `human = int(result, 16) / 10**decimals`
(USDT/USDC → `/10**6`, stETH/eETH → `/10**18`). A zero result (`0x0…0`) means the
account holds none of that token. `balanceOf` (not summed `Transfer` logs) is the
source of truth for current balance — this is what makes rebasing tokens correct.

## Sources

- [USDT — Etherscan](https://etherscan.io/token/0xdac17f958d2ee523a2206206994597c13d831ec7)
- [USDC — Etherscan](https://etherscan.io/token/0xa0b86991c6218b36c1d19d4a2e9eb0ce3606eb48)
- [stETH — Etherscan](https://etherscan.io/token/0xae7ab96520de3a18e5e111b5eaab095312d7fe84)
- [eETH — Etherscan](https://etherscan.io/token/0x35fa164735182de50811e8e2e824cfb9b6118ac2)
