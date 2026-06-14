---
name: eth-rpc
description: Use when the user wants to query an Ethereum account's ETH balance or broadcast/propagate an already-signed raw transaction for the eth-signer-mcp signer. Operations — balance (eth_getBalance of an EOA) and broadcast (eth_sendRawTransaction, optionally waiting for the receipt) — on mainnet/hoodi. Does not sign and does not build transactions.
---

# eth-rpc

The read/write RPC companion to the `eth-signer-mcp` signer. Three operations:

- **balance** — query the ETH balance of an EOA.
- **broadcast** — propagate an already-signed raw transaction to the network.
- **call** — generic `eth_*` JSON-RPC read passthrough for any method on the
  documented read surface.

This skill makes outbound JSON-RPC calls; the signer itself stays offline. It
does **not** sign (that is the `sign_transaction` MCP tool) and does **not** build
transactions (that is the `eth-tx-builder` skill).

## Inputs

- **network** — `mainnet` or `hoodi` (required for balance and broadcast;
  optional for call when using `--rpc-url` + `--chain-id` instead).
- balance: **address** — the EOA to query (optional; see below).
- broadcast: **raw-tx** — the `0x`-prefixed signed transaction hex (from the
  signer's `sign_transaction` `rawTransaction` field).
- call: **method** + **params** — the JSON-RPC method name and a JSON array of
  parameters.

## Operations

### balance

```bash
python3 eth_rpc.py balance --network <mainnet|hoodi> --address 0x<40hex>
```

- If the user does **not** specify an address (e.g. "what's my balance",
  "the signer's balance"), call the `get_address` MCP tool first and pass its
  `address` as `--address`. This requires the `eth-signer-mcp` server to be
  connected; if `get_address` is unavailable, tell the user to connect the signer
  or supply an explicit address.
- If the user names any other address, pass it directly — no `get_address` needed.
- Present the result making units loud: show **both** `balanceWei` and `balanceEth`.

### broadcast

```bash
python3 eth_rpc.py broadcast --network <mainnet|hoodi> --raw-tx 0x02... [--wait] [--wait-timeout 120]
```

- Take the signer's `rawTransaction` hex as `--raw-tx`.
- Add `--wait` when the user wants confirmation (poll for the receipt). On submit
  this needs no MCP — only the raw tx.
- Report `txHash` always. With `--wait`, also report `status` (`mined` / `failed`
  / `pending`), `blockNumber`, `gasUsed`, and `effectiveGasPrice`.
- `status: failed` means the tx was included but reverted (receipt status `0x0`) —
  still a successful broadcast. `status: pending` means it has not been mined
  within the timeout; offer to keep polling.

### call

Prefer **balance** or **broadcast** when they apply — they provide decoded output
and `--wait` receipt semantics. Use **call** for everything else.

```bash
python3 eth_rpc.py call \
  (--network <mainnet|hoodi> | --rpc-url <url> --chain-id <int>) \
  --method <jsonrpc-method-name> \
  --params <json-array-or-"-"> \
  [--allow-write] \
  [--timeout <seconds>]
```

#### Documented read surface

The following methods are in scope for the `call` op (the passthrough is
method-agnostic at runtime; this list governs documentation and the manual e2e):

`eth_getBalance`, `eth_getCode`, `eth_getStorageAt`, `eth_getTransactionCount`,
`eth_getTransactionByHash`, `eth_getTransactionByBlockHashAndIndex`,
`eth_getTransactionByBlockNumberAndIndex`, `eth_getTransactionReceipt`,
`eth_getBlockByNumber`, `eth_getBlockByHash`,
`eth_getBlockTransactionCountByNumber`, `eth_getBlockTransactionCountByHash`,
`eth_getLogs`, `eth_call`, `eth_estimateGas`, `eth_gasPrice`, `eth_feeHistory`,
`eth_maxPriorityFeePerGas`, `eth_chainId`, `eth_blockNumber`, `eth_syncing`,
`eth_accounts`, `eth_protocolVersion`, `eth_getProof`.

Notes on specific methods:

- **`eth_getCode`** requires a block parameter (address + block tag or number) per
  the `ethereum/execution-apis` OpenRPC specification.
- **`eth_estimateGas`** accepts only a block tag or hex number as the optional
  second argument. It does **not** accept the EIP-1898 hash-object form
  `{"blockHash": "0x..."}` — only `eth_call` supports that.
- **`eth_protocolVersion`** is absent from the `ethereum/execution-apis` OpenRPC
  spec but is still implemented by major clients. Example illustrative value:
  `"0x41"` (may differ by client and version; treat as informational).

#### `--params` shape

Must be a JSON array string (top-level type list). Anything else is rejected.

- Inline: `--params '["0x...", "latest"]'`
- Stdin: `--params -` reads the rest of stdin as a JSON array (useful for large
  `eth_getLogs` filter objects).
- `@file` (deferred to P1 — not yet supported).

Block tags (`"latest"`, `"pending"`, `"finalized"`, `"safe"`, `"earliest"`) and
hex addresses/quantities are the operator's responsibility; no per-method
validation is applied beyond "is a JSON array".

#### Denylist and `--allow-write`

By default the following methods are refused with a `ValueError`:

- Explicit: `eth_sendRawTransaction`, `eth_sendTransaction`, `eth_sign`,
  `eth_signTransaction`, `eth_signTypedData`, `eth_signTypedData_v3`,
  `eth_signTypedData_v4`
- Prefix-matched: any method starting with `personal_`, `admin_`, `miner_`,
  `engine_`, or `clique_`

The refusal message for send methods points at the curated `broadcast` op. To
bypass the denylist (e.g. on a private dev node), pass `--allow-write`:

```bash
python3 eth_rpc.py call --network hoodi --method eth_sendRawTransaction \
  --params '["0x02..."]' --allow-write
```

A warning is always printed to stderr when `--allow-write` is active:

```
warning: --allow-write bypasses the call denylist
```

#### Custom endpoint

To target a node not in the built-in `NETWORKS` map, pass both flags together:

```bash
python3 eth_rpc.py call --rpc-url http://127.0.0.1:8545 --chain-id 31337 \
  --method eth_blockNumber --params '[]'
```

`--rpc-url` and `--chain-id` are required together; they are mutually exclusive
with `--network`. HTTP URLs are allowed only for loopback hosts (`127.0.0.1`,
`localhost`, `::1`) — relying on documented `urllib.parse.SplitResult.hostname`
semantics (bracket-stripping + lowercasing). Non-loopback HTTP is refused.

#### Worked examples

```bash
python3 eth_rpc.py call --network hoodi --method eth_chainId --params '[]'
```

```bash
python3 eth_rpc.py call --network hoodi --method eth_blockNumber --params '[]'
```

```bash
python3 eth_rpc.py call --network hoodi \
  --method eth_getTransactionReceipt \
  --params '["0xd6133a2b2713dd86f4abe32421aed32f9945aed046dbc80751f5a03871799e85"]'
```

```bash
python3 eth_rpc.py call --network mainnet \
  --method eth_call \
  --params '[{"to":"0x...","data":"0x..."}, "latest"]'
```

```bash
python3 eth_rpc.py call --rpc-url http://127.0.0.1:8545 --chain-id 31337 \
  --method eth_blockNumber --params '[]'
```

## Procedure

1. Identify the operation (balance, broadcast, or call) and the network. Validate inputs.
2. For balance with no address, call `get_address` first (see above).
3. Run the bundled helper from the skill directory:

   ```bash
   cd .claude/skills/eth-rpc
   python3 eth_rpc.py <balance|broadcast|call> ...
   ```

   - On success it prints a result JSON to stdout.
   - On failure it prints `error: ...` to stderr and exits non-zero. **Surface the
     error and stop — do not present a partial result.**
4. Present the result (balance: wei + ETH; broadcast: tx hash and, with `--wait`,
   the receipt summary; call: raw JSON-RPC result pretty-printed).

## Notes

- **`--wait` runtime:** the poll loop can run longer than the default Bash timeout.
  Run `broadcast --wait` with a raised Bash timeout (e.g. set `timeout` to comfortably
  exceed `--wait-timeout`) or in the background, and keep `--wait-timeout` within that
  budget.
- Networks are hardcoded in `eth_rpc.py`: `mainnet` → chainId 1, `hoodi` → 560048,
  each with a public RPC endpoint.
- Balance is always read at the `latest` block.
- Block return fields: note that `totalDifficulty` is a legacy field and may be
  absent or `null` on post-merge nodes — do not rely on it.
- This skill never signs and never builds a transaction. To build a send-ETH tx,
  use `eth-tx-builder`; to sign, use the signer's `sign_transaction` MCP tool.
- For the full `call` method list and denylist details, see the `call` section above.

## Out of scope (v1)

- ABI decoding for `eth_call` return data or `eth_getLogs` topics/data.
- `debug_*` / `trace_*` namespaces (many nodes refuse them; not documented).
- JSON-RPC batch requests (array of requests).
- Subscription / websocket transports (`eth_subscribe`).
- Multi-network parallel fan-out.
- `--params @file` syntax (deferred to P1).
