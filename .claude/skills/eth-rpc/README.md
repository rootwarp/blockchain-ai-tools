# eth-rpc

A Claude Code skill that acts as the read/write RPC companion to the
[`eth-signer-mcp`](../../../apps/eth-signer-mcp/README.md) signer. Six operations:

- **balance** — query the ETH balance of an EOA (`eth_getBalance`).
- **broadcast** — propagate an already-signed raw transaction
  (`eth_sendRawTransaction`), optionally waiting for the receipt.
- **call** — generic `eth_*` JSON-RPC read passthrough for any method on the
  documented read surface.
- **batch** — send multiple JSON-RPC calls in one request; returns per-entry
  result/error envelopes.
- **net-version** — diagnostic: `net_version` → `{chainId, netVersion}`.
- **client-version** — diagnostic: `web3_clientVersion` → `{chainId, clientVersion}`.

It makes outbound RPC calls; the signer itself stays offline. It does **not** sign
and does **not** build transactions (see `eth-tx-builder` for building).

Prefer `balance` / `broadcast` for those flows; use `call` for everything else —
see `SKILL.md` for the full method list, `--decode` examples, `--read-only-strict`
guidance, and `--params @file` usage.

## Files

- `SKILL.md` — the skill Claude follows (operation → optional `get_address` → helper → present).
- `eth_rpc.py` — stdlib-only helper: network map, validation, RPC, balance + broadcast + call + batch + diagnostics.
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

### call

Two cheap read calls to confirm the `call` op and verify hoodi `chainId`:

```bash
cd .claude/skills/eth-rpc
python3 eth_rpc.py call --network hoodi --method eth_chainId    --params '[]'
python3 eth_rpc.py call --network hoodi --method eth_blockNumber --params '[]'
```

Captured output:

```
"0x88bb0"
```

```
"0x2df761"
```

`"0x88bb0"` is hex for 560048 (hoodi `chainId`). `eth_blockNumber` returns the
current chain head as a hex quantity.

### batch

Two-call batch confirming `eth_chainId` + `eth_blockNumber` via the new batch op
(re-confirms hoodi chainId = `"0x88bb0"`, Phase 1 Assumption A5):

```bash
cd .claude/skills/eth-rpc
python3 eth_rpc.py batch --network hoodi --calls '[
  {"method": "eth_chainId",     "params": []},
  {"method": "eth_blockNumber", "params": []}
]'
```

Captured output:

```json
[
  {
    "id": 0,
    "result": "0x88bb0"
  },
  {
    "id": 1,
    "result": "0x2df796"
  }
]
```

`id: 0` confirms hoodi `chainId = "0x88bb0"` (= 560048, re-confirms Phase 1 Assumption A5).
`id: 1` is the current chain head hex quantity.

### net-version and client-version

```bash
python3 eth_rpc.py net-version    --network hoodi
python3 eth_rpc.py client-version --network hoodi
```

Captured output:

```json
{
  "chainId": "560048",
  "netVersion": "560048"
}
```

```json
{
  "chainId": "560048",
  "clientVersion": "Geth/v1.17.1-stable-16783c16/linux-amd64/go1.25.7"
}
```

### balance

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

- ERC-20 balance / `balanceOf` via `call --method eth_call` with decoded output.
- Block-tag selection for `balance` (currently fixed at `latest`; `call` accepts
  any block tag the operator passes in `--params`).
- `--params @file` for large `eth_getLogs` filter objects (deferred to P1).
- Decoded output for `eth_feeHistory` / `eth_getProof` (deferred to Phase 2+).

Each reuses the network table and RPC plumbing here.
