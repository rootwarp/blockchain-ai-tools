# eth-query

A Claude Code skill that reports an Ethereum account's **holdings** — native ETH
balance plus decoded ERC-20 balances for the curated [`ERC20.md`](../../../ERC20.md)
tokens (USDT/USDC/stETH/eETH) — in one combined view. It is the high-level reader
above [`eth-jsonrpc`](../eth-jsonrpc/README.md), which exposes only raw balances and
leaves ERC-20 decimals/decoding out of scope.

Reads only. Does **not** sign, build (`eth-tx-builder`), or broadcast.

## Files

- `SKILL.md` — the skill Claude follows (resolve inputs → `eth-jsonrpc` balance/batch
  → decode with `ERC20.md` decimals → combined report). No bundled code.

## How it works

- **Native ETH:** delegates to `eth-jsonrpc`'s `balance` op (`eth_rpc.py balance`).
- **ERC-20:** reads the `ERC20.md` token table, builds `balanceOf` calldata, and runs
  one `eth-jsonrpc` `batch` of `eth_call`s on **mainnet**, decoding each result with
  the token's decimals via an exact integer one-liner.

## Prerequisites

- `python3` (3.8+), stdlib only (used for the inline decode one-liner and by the
  `eth-jsonrpc` helper it calls).
- The sibling `eth-jsonrpc` skill present at `../eth-jsonrpc/eth_rpc.py`.
- `ERC20.md` at the repo root (the token source of truth).
- Outbound access to the public RPC endpoints; for the "my holdings" UX, the
  `eth-signer-mcp` server connected (for `get_address`).

## Scope and constraints

- Tokens: `ERC20.md` list only (no ad-hoc addresses).
- **ERC-20 balances are Ethereum-mainnet only** — skipped (scope `all`) or refused
  (scope `tokens`) on testnets. Native ETH works on any network.

## Manual end-to-end

See the "Worked example" section of `SKILL.md` for a captured mainnet run.
