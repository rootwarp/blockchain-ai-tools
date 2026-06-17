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
## Scope
## Procedure
## Precision (exact decimal conversion)
## Network handling (ERC-20 is mainnet-only)
## Error handling
## Output
## Worked example
## Out of scope
