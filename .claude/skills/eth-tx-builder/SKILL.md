---
name: eth-tx-builder
description: Use when the user wants to build or generate an Ethereum transaction to send ETH (a native value transfer) for the eth-signer-mcp signer. Produces a ready-to-sign sign_transaction TxRequest JSON from a network (mainnet/hoodi), a destination EOA, and an amount in gwei. Does not sign.
---

# eth-tx-builder

Generate a ready-to-sign Ethereum `TxRequest` JSON for the `eth-signer-mcp`
`sign_transaction` tool. **v1 supports one operation: send ETH (native value
transfer, EIP-1559 type 2).** This skill does **not** sign — it produces the
transaction data; signing is a separate, explicit step the user takes afterward.

## Inputs

1. **network** — `mainnet` or `hoodi`.
2. **destination** — recipient EOA address (`0x` + 40 hex).
3. **amount** — amount to send, in **gwei** (1 gwei = 1e9 wei = 1e-9 ETH).

If any are missing, ask for them before proceeding.

## Prerequisite

The `eth-signer-mcp` server MUST be connected as an MCP server in this session
(this skill calls its `get_address` tool to learn the sender). If `get_address`
is not available, tell the user to connect the signer and stop — do not guess a
sender address.

## Procedure

1. **Validate inputs:** network is `mainnet` or `hoodi`; destination looks like
   `0x` + 40 hex; amount is a non-negative integer.
2. **Get the sender:** call the `get_address` MCP tool. Use its `address` as the
   sender (the account whose nonce we query and that will sign).
3. **Build the transaction:** run the bundled helper from the skill directory:

   ```bash
   python3 build_send_eth.py \
     --network <network> \
     --to <destination> \
     --amount-gwei <amount> \
     --sender <address-from-get_address>
   ```

   - On success it prints the `TxRequest` JSON to stdout.
   - On failure (RPC error, bad input) it prints `error: ...` to stderr and exits
     non-zero. **Surface the error and stop — do not present a partial transaction.**
4. **Present to the user**, and stop:
   - the `TxRequest` JSON exactly as printed (ready to paste into `sign_transaction`); and
   - a human-readable summary: **network + chainId**, **from → to**, the **amount in
     gwei AND the resulting `value` in wei AND the ETH equivalent**, the **nonce**, and
     **maxFeePerGas / maxPriorityFeePerGas**.

   Make the amount units loud: gwei is a tiny unit, so showing wei + ETH lets the
   user catch a mis-entered amount before they sign.

## Notes

- Networks are hardcoded in `build_send_eth.py`: `mainnet` → chainId 1, `hoodi`
  → chainId 560048; each with a public RPC endpoint.
- Fees follow the standard wallet heuristic: `maxPriorityFeePerGas` from the node
  (fallback 1 gwei), `maxFeePerGas = baseFee*2 + tip`.
- `gas` is fixed at `21000` (a plain ETH transfer's intrinsic cost).
- **chain-id guard (downstream):** if the signer was started with `--chain-id`, it
  must equal this network's chainId or `sign_transaction` will return
  `chain_id_mismatch`. This skill does not sign, but flag it so the later sign step
  isn't a surprise.
- This skill makes outbound RPC calls; the `eth-signer-mcp` signer itself remains
  strictly offline. The two concerns are separate.

## Out of scope (v1)

ERC-20 / contract calls (need ABI calldata + `eth_estimateGas`), transaction types
other than EIP-1559, broadcasting. These are planned follow-on operations.
