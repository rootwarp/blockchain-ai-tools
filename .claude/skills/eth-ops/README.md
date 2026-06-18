# eth-ops

The front-door **orchestrator** skill for Ethereum operations. `eth-ops` classifies a
request and drives the right underlying skill/tool — answering reads directly and
conducting fund-moving operations through a gated `build → sign → broadcast` pipeline.
Instructions-only; it adds routing, confirmation gates, and presentation, and delegates
everything else.

## What it routes to

- **reads** (holdings, single balance, generic `eth_*`, diagnostics) → [`eth-jsonrpc`](../eth-jsonrpc/README.md)
- **build a tx** (native send, ERC-20 transfer/approve/transferFrom) → [`eth-tx-builder`](../eth-tx-builder/README.md)
- **sign** → the `eth-signer` MCP tools (`sign_transaction`, `get_address`)
- **broadcast** → [`eth-jsonrpc`](../eth-jsonrpc/README.md) (`broadcast`)

## Files

- `SKILL.md` — the skill Claude follows: intent routing, the gated pipeline, read
  procedures (incl. the decoded-holdings report over [`ERC20.md`](../../../ERC20.md)),
  and safety invariants. No bundled code.

## Safety model

Fund-moving requests pass **two explicit human confirmation gates** — one before
signing (`sign_transaction`) and one before broadcasting (the irreversible step), with a
"real funds" callout on mainnet. A read intent never triggers a write; any failed step
stops the pipeline before signing or broadcasting.

## Prerequisites

- `python3` (3.8+), stdlib only (the delegated helpers + the holdings decimals one-liner).
- Sibling skills present: `../eth-jsonrpc/eth_rpc.py`, `../eth-tx-builder/build_send_eth.py`
  + `build_erc20.py`.
- The `eth-signer` MCP server connected (for `get_address` + `sign_transaction`).
- `ERC20.md` at the repo root (holdings token source of truth).
- Outbound access to the public RPC endpoints.

## Examples

See the "Worked examples" section of `SKILL.md` — a captured mainnet holdings read and a
narrated hoodi `build → gates → broadcast` pipeline.
