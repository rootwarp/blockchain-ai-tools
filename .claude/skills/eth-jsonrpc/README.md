# eth-jsonrpc

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
cd .claude/skills/eth-jsonrpc
python3 -m unittest test_eth_rpc -v
```

No live RPC calls are made — the transport is mocked, and the `--wait` poll loop
uses an injected clock/sleep.

## Manual end-to-end (use hoodi — testnet, no real funds)

### call

Two cheap read calls to confirm the `call` op and verify hoodi `chainId`:

```bash
cd .claude/skills/eth-jsonrpc
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
cd .claude/skills/eth-jsonrpc
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
cd .claude/skills/eth-jsonrpc
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
| `sepolia` | 11155111 | `https://ethereum-sepolia-rpc.publicnode.com` |
| `holesky` | 17000 | `https://ethereum-holesky-rpc.publicnode.com` |

**Holesky was deprecated September 2025.** Prefer `hoodi` for new work; holesky
remains accessible for legacy contracts.

## Phase 2 manual end-to-end (new features: --decode, sepolia, --read-only-strict)

Captured against live endpoints from `.claude/skills/eth-jsonrpc/`:

```bash
# 1. --decode: chainId with human-readable decimal
$ python3 eth_rpc.py call --network hoodi --decode --method eth_chainId --params '[]'
{
  "hex": "0x88bb0",
  "decimal": 560048
}

# 2. sepolia chainId (verifies the new network entry)
$ python3 eth_rpc.py call --network sepolia --method eth_chainId --params '[]'
"0xaa36a7"

# 3. --read-only-strict on an allowlisted method (succeeds)
$ python3 eth_rpc.py call --network hoodi --read-only-strict --method eth_chainId --params '[]'
"0x88bb0"

# 4. --read-only-strict refuses a method outside the documented read surface
$ python3 eth_rpc.py call --network hoodi --read-only-strict --method net_version --params '[]'
error: method net_version is not in the documented eth_* read surface (--read-only-strict)
# exit code 1
```

`0xaa36a7` = 11155111 (sepolia chainId). `--decode` of `eth_chainId` exposes the
decimal alongside the raw hex; the raw passthrough is unchanged when `--decode` is
omitted.

## Phase 3.4 manual end-to-end (--decode eth_feeHistory / eth_getProof)

Captured live against hoodi (decoded fields land at the top level next to a
`raw` echo of the original response):

```bash
# eth_feeHistory with --decode
$ python3 eth_rpc.py call --network hoodi --decode \
    --method eth_feeHistory --params '[1, "latest", [25]]'
{
  "raw": {
    "oldestBlock": "0x2df886",
    "reward": [["0x4e39ff0"]],
    "baseFeePerGas": ["0x3db5e366", "0x3626688e"],
    "gasUsedRatio": [0.0099396],
    "baseFeePerBlobGas": ["0x36d821e9", "0x3b5307ff"],
    "blobGasUsedRatio": [1]
  },
  "oldestBlock": 3012742,
  "baseFeePerGas": [1035330406, 908486798],
  "baseFeePerGasGwei": ["1.035330406", "0.908486798"],
  "gasUsedRatio": [0.0099396],
  "reward": [[82026480]]
}

# eth_getProof with --decode (EOA; accountProof elided for brevity)
$ python3 eth_rpc.py call --network hoodi --decode \
    --method eth_getProof \
    --params '["0x6302794A4F2487a2540c40E2dbB211Ff6AF1CD20", [], "latest"]'
{
  "raw": {
    "address": "0x6302794a4f2487a2540c40e2dbb211ff6af1cd20",
    "accountProof": ["0xf90211...", "..."],
    "balance": "0xc7d5bd5cc534c60",
    "codeHash": "0xc5d2460186f7233c927e7db2dcc703c0e500b653ca82273b7bfad8045d85a470",
    "nonce": "0x1",
    "storageHash": "0x56e81f171bcc55a6ff8345e692c0f86e5b48e01b996cadc001622fb5e363b421",
    "storageProof": []
  },
  "balance": 899976474358140000,
  "nonce": 1,
  "storageProof": []
}
```

`baseFeePerGas`/`reward` decode to wei ints (with a `baseFeePerGasGwei` companion);
`gasUsedRatio` stays as floats; proof byte-arrays (`accountProof`, `storageProof[].proof`)
and digests (`codeHash`, `storageHash`) are left as raw hex — only `balance`, `nonce`,
and `storageProof[].value` are numeric-decoded.

## Future operations (not yet implemented)

- ERC-20 balance / `balanceOf` via `call --method eth_call` with decoded output.
  (Realized by the `eth-ops` skill — see `../eth-ops/README.md` — which decodes
  `ERC20.md` token balances on top of `balance` + `batch`.)
- Block-tag selection for `balance` (currently fixed at `latest`; `call` accepts
  any block tag the operator passes in `--params`).

Each reuses the network table and RPC plumbing here.
