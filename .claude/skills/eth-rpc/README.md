# eth-rpc

A Claude Code skill that acts as the read/write RPC companion to the
[`eth-signer-mcp`](../../../apps/eth-signer-mcp/README.md) signer. Two operations:

- **balance** — query the ETH balance of an EOA (`eth_getBalance`).
- **broadcast** — propagate an already-signed raw transaction
  (`eth_sendRawTransaction`), optionally waiting for the receipt.

It makes outbound RPC calls; the signer itself stays offline. It does **not** sign
and does **not** build transactions (see `eth-tx-builder` for building).

## Files

- `SKILL.md` — the skill Claude follows (operation → optional `get_address` → helper → present).
- `eth_rpc.py` — stdlib-only helper: network map, validation, RPC, balance + broadcast.
- `test_eth_rpc.py` — unit tests (mocked RPC; no live network).

## Prerequisites

- `python3` (3.8+), stdlib only.
- For the balance "default to the signer" UX, the `eth-signer-mcp` server connected
  as an MCP server (the skill calls `get_address`). Querying an explicit address, or
  broadcasting, needs no MCP.
- Outbound network access to the public RPC endpoints (hardcoded per network).

## Run the tests

```bash
cd .claude/skills/eth-rpc
python3 -m unittest test_eth_rpc -v
```

No live RPC calls are made — the transport is mocked, and the `--wait` poll loop
uses an injected clock/sleep.

## Manual end-to-end (use hoodi — testnet, no real funds)

Query a balance:

```bash
cd .claude/skills/eth-rpc
python3 eth_rpc.py balance \
  --network hoodi \
  --address 0x6302794A4F2487a2540c40E2dbB211Ff6AF1CD20
```

Expect JSON with `balanceWei` and `balanceEth`.

Broadcast a signed tx (built by `eth-tx-builder`, signed by the signer's
`sign_transaction` tool), waiting for confirmation:

```bash
python3 eth_rpc.py broadcast \
  --network hoodi \
  --raw-tx 0x02f8... \
  --wait --wait-timeout 120
```

Expect JSON with `txHash` and, once mined, `status: mined` plus `blockNumber`,
`gasUsed`, and `effectiveGasPrice`.

## Networks

| network | chainId | RPC |
|---|---|---|
| `mainnet` | 1 | `https://ethereum-rpc.publicnode.com` |
| `hoodi` | 560048 | `https://ethereum-hoodi-rpc.publicnode.com` |

## Future operations (not yet implemented)

- ERC-20 balance / `balanceOf` via `eth_call`.
- Block-tag selection (`latest` is currently fixed).
- Event/log queries.

Each reuses the network table and RPC plumbing here.
