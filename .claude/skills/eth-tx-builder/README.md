# eth-tx-builder

A Claude Code skill that generates a ready-to-sign Ethereum `TxRequest` JSON for
the [`eth-signer-mcp`](../../../apps/eth-signer-mcp/README.md) `sign_transaction`
tool. v1 supports one operation: **send ETH** (native EIP-1559 value transfer).

It does **not** sign. The skill produces transaction *data*; the user signs
separately with the signer.

## Files

- `SKILL.md` — the skill Claude follows (inputs → get_address → helper → present JSON).
- `build_send_eth.py` — stdlib-only helper: network map, gwei→wei, RPC (nonce + fees),
  fee math, and the `TxRequest` JSON. No third-party packages.
- `test_build_send_eth.py` — unit tests (mocked RPC; no live network).

## Prerequisites

- `python3` (3.8+), stdlib only.
- The `eth-signer-mcp` server connected as an MCP server in the session (the skill
  calls its `get_address` tool for the sender address).
- Outbound network access to the public RPC endpoints (hardcoded per network).

## Run the tests

```bash
cd .claude/skills/eth-tx-builder
python3 -m unittest test_build_send_eth -v
```

No live RPC calls are made — the transport is mocked.

## Manual end-to-end (use hoodi — testnet, no real funds)

```bash
cd .claude/skills/eth-tx-builder
python3 build_send_eth.py \
  --network hoodi \
  --to 0x70997970C51812dc3A010C7d01b50e0d17dc79C8 \
  --amount-gwei 1000 \
  --sender <your-keystore-address>
```

Expect a `TxRequest` JSON on stdout with `type: eip1559`, `chainId: 560048`,
a live `nonce`, and EIP-1559 fee fields. Optionally paste it into the signer's
`sign_transaction` tool to confirm the schema is accepted.

## Networks

| network | chainId | RPC |
|---|---|---|
| `mainnet` | 1 | `https://ethereum-rpc.publicnode.com` |
| `hoodi` | 560048 | `https://ethereum-hoodi-rpc.publicnode.com` |

## Future operations (not yet implemented)

- ERC-20 `transfer` — ABI calldata in `data`, `to` = token contract, `value` = 0,
  `gas` from `eth_estimateGas`.
- Arbitrary contract call — caller-supplied ABI + `eth_estimateGas`.

Each reuses the network table, fee strategy, and RPC plumbing here.
