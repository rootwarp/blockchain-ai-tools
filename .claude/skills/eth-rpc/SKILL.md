---
name: eth-rpc
description: Use when the user wants to query an Ethereum account's ETH balance or broadcast/propagate an already-signed raw transaction for the eth-signer-mcp signer. Operations — balance (eth_getBalance of an EOA) and broadcast (eth_sendRawTransaction, optionally waiting for the receipt) — on mainnet/hoodi. Does not sign and does not build transactions.
---

# eth-rpc

The read/write RPC companion to the `eth-signer-mcp` signer. Two operations:

- **balance** — query the ETH balance of an EOA.
- **broadcast** — propagate an already-signed raw transaction to the network.

This skill makes outbound JSON-RPC calls; the signer itself stays offline. It
does **not** sign (that is the `sign_transaction` MCP tool) and does **not** build
transactions (that is the `eth-tx-builder` skill).

## Inputs

- **network** — `mainnet` or `hoodi` (required for both operations).
- balance: **address** — the EOA to query (optional; see below).
- broadcast: **raw-tx** — the `0x`-prefixed signed transaction hex (from the
  signer's `sign_transaction` `rawTransaction` field).

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

## Procedure

1. Identify the operation (balance or broadcast) and the network. Validate inputs.
2. For balance with no address, call `get_address` first (see above).
3. Run the bundled helper from the skill directory:

   ```bash
   cd .claude/skills/eth-rpc
   python3 eth_rpc.py <balance|broadcast> ...
   ```

   - On success it prints a result JSON to stdout.
   - On failure it prints `error: ...` to stderr and exits non-zero. **Surface the
     error and stop — do not present a partial result.**
4. Present the result (balance: wei + ETH; broadcast: tx hash and, with `--wait`,
   the receipt summary).

## Notes

- **`--wait` runtime:** the poll loop can run longer than the default Bash timeout.
  Run `broadcast --wait` with a raised Bash timeout (e.g. set `timeout` to comfortably
  exceed `--wait-timeout`) or in the background, and keep `--wait-timeout` within that
  budget.
- Networks are hardcoded in `eth_rpc.py`: `mainnet` → chainId 1, `hoodi` → 560048,
  each with a public RPC endpoint.
- Balance is always read at the `latest` block.
- This skill never signs and never builds a transaction. To build a send-ETH tx,
  use `eth-tx-builder`; to sign, use the signer's `sign_transaction` MCP tool.

## Out of scope (v1)

ERC-20 / contract-call decoding, event/log queries, block-tag selection, gas
oracles, and nonce management.
