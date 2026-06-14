---
name: eth-rpc
description: Use when the user wants to query an Ethereum account's ETH balance, broadcast a signed transaction, run any eth_* JSON-RPC read method, batch multiple calls, or get diagnostic info (net version, client version). Operations — balance, broadcast, call, batch, net-version, client-version — on mainnet/hoodi or a custom endpoint. Does not sign and does not build transactions.
---

# eth-rpc

The read/write RPC companion to the `eth-signer-mcp` signer. Six operations:

- **balance** — query the ETH balance of an EOA.
- **broadcast** — propagate an already-signed raw transaction to the network.
- **call** — generic `eth_*` JSON-RPC read passthrough for any method on the
  documented read surface.
- **batch** — send multiple JSON-RPC calls in one request; returns per-entry
  result/error envelopes.
- **net-version** — diagnostic: call `net_version`; returns chainId + netVersion.
- **client-version** — diagnostic: call `web3_clientVersion`; returns chainId + clientVersion.

This skill makes outbound JSON-RPC calls; the signer itself stays offline. It
does **not** sign (that is the `sign_transaction` MCP tool) and does **not** build
transactions (that is the `eth-tx-builder` skill).

## Inputs

- **network** — `mainnet` or `hoodi` (required for balance and broadcast;
  optional for call/batch/net-version/client-version when using `--rpc-url` +
  `--chain-id` instead).
- balance: **address** — the EOA to query (optional; see below).
- broadcast: **raw-tx** — the `0x`-prefixed signed transaction hex (from the
  signer's `sign_transaction` `rawTransaction` field).
- call: **method** + **params** — the JSON-RPC method name and a JSON array of
  parameters.
- batch: **calls** — a JSON array of `{"method": str, "params": list}` objects.

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
  [--timeout <seconds>] \
  [--max-body-bytes <int>]
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

#### `--max-body-bytes`

Cap the response body size to defend against large `eth_getLogs` results that
could exhaust memory. Default is unset (unbounded). When set, the skill reads
at most `N+1` bytes and raises `error: response exceeds --max-body-bytes` if the
response is larger (ADR-013). Both `call` and `batch` support this flag.

```bash
python3 eth_rpc.py call --network hoodi --max-body-bytes 8388608 \
  --method eth_getLogs --params '[{"fromBlock":"0x...","toBlock":"0x..."}]'
```

If a wide-range `eth_getLogs` hits the limit, narrow the block range and retry
rather than raising the cap.

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

### batch

Send multiple JSON-RPC calls in a single HTTP request (ADR-012). Each entry is a
`{"method": str, "params": list}` object. The skill assigns positional ids
(`0..N-1`) and returns a per-entry envelope list.

```bash
python3 eth_rpc.py batch \
  (--network <mainnet|hoodi> | --rpc-url <url> --chain-id <int>) \
  --calls <json-array-or-"-"> \
  [--allow-write] \
  [--timeout <seconds>] \
  [--max-body-bytes <int>]
```

#### Input shape

```json
[
  {"method": "eth_chainId",     "params": []},
  {"method": "eth_blockNumber", "params": []}
]
```

Do not include `id` or `jsonrpc` fields — those are injected by the skill.

#### Output shape

```json
[
  {"id": 0, "result": "0x88bb0"},
  {"id": 1, "result": "0x2df761"}
]
```

Each entry is either `{"id": int, "result": Any}` (success) or
`{"id": int, "error": {"code": int, "message": str}}` (node error or skill
refusal). Positional order is always preserved even if the server responds
out of order.

#### Denylist in batch

The same denylist applies per-entry. A refused entry lands as a synthetic error
envelope at its original index (code `-32601`); the other entries are forwarded
to the node. Use `--allow-write` to bypass the denylist for all entries; the
warning is printed exactly once per invocation.

#### Empty batch rejected

`--calls '[]'` is rejected with `error: --calls must be a non-empty JSON array`.

#### Worked example

```bash
python3 eth_rpc.py batch --network hoodi --calls '[
  {"method": "eth_chainId",     "params": []},
  {"method": "eth_blockNumber", "params": []}
]'
```

### net-version

Quick diagnostic wrapper around `net_version`. Use `call --method net_version
--params '[]'` for the raw result; this subcommand is for operator ergonomics.

```bash
python3 eth_rpc.py net-version \
  (--network <mainnet|hoodi> | --rpc-url <url> --chain-id <int>) \
  [--timeout <seconds>]
```

Output: `{"chainId": "...", "netVersion": "..."}`.

### client-version

Quick diagnostic wrapper around `web3_clientVersion`.

```bash
python3 eth_rpc.py client-version \
  (--network <mainnet|hoodi> | --rpc-url <url> --chain-id <int>) \
  [--timeout <seconds>]
```

Output: `{"chainId": "...", "clientVersion": "..."}`.

## Procedure

1. Identify the operation (balance, broadcast, call, batch, net-version, or
   client-version) and the network. Validate inputs.
2. For balance with no address, call `get_address` first (see above).
3. Run the bundled helper from the skill directory:

   ```bash
   cd .claude/skills/eth-rpc
   python3 eth_rpc.py <op> ...
   ```

   - On success it prints a result JSON to stdout.
   - On failure it prints `error: ...` to stderr and exits non-zero. **Surface the
     error and stop — do not present a partial result.**
4. Present the result (balance: wei + ETH; broadcast: tx hash and, with `--wait`,
   the receipt summary; call/batch: raw JSON-RPC result/envelope pretty-printed).

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
- `net-version` and `client-version` are curated wrappers (wrapped dict output,
  same style as `balance`). Use `call --method ...` for the raw JSON-RPC result.

## Out of scope

- ABI decoding for `eth_call` return data or `eth_getLogs` topics/data.
- `debug_*` / `trace_*` namespaces (many nodes refuse them; not documented).
- Subscription / websocket transports (`eth_subscribe`).
- Multi-network parallel fan-out.
- `--params @file` syntax (deferred to P1).
- Decoded output for `eth_feeHistory` / `eth_getProof` (deferred to Phase 2+).
