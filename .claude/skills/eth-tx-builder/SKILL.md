---
name: eth-tx-builder
description: Use when the user wants to build or generate an Ethereum transaction — either a native ETH transfer OR an ERC-20 transfer / approve / transferFrom — for the eth-signer-mcp signer. Produces a ready-to-sign sign_transaction TxRequest JSON from a network (mainnet/hoodi), the required addresses, and an amount. Does not sign.
---

# eth-tx-builder

Generate a ready-to-sign Ethereum `TxRequest` JSON for the `eth-signer-mcp`
`sign_transaction` tool. Supports two classes of operation:

- **Native ETH send** — EIP-1559 value transfer (v1, `build_send_eth.py`)
- **ERC-20 token operations** — `transfer`, `approve`, and `transferFrom` (v2,
  `build_erc20.py`)

This skill does **not** sign — it produces the transaction data; signing is a
separate, explicit step the user takes afterward.

## Inputs — native ETH send

1. **network** — `mainnet` or `hoodi`.
2. **destination** — recipient EOA address (`0x` + 40 hex).
3. **amount** — amount to send, in **gwei** (1 gwei = 1e9 wei = 1e-9 ETH).

If any are missing, ask for them before proceeding.

## Inputs — ERC-20 transfer / approve / transferFrom

1. **network** — `mainnet` or `hoodi`.
2. **token** — ERC-20 contract address (`0x` + 40 hex).
3. **sender** — signing account address (`0x` + 40 hex); obtained from `get_address`.
4. **subcommand-specific addresses and amount:**
   - `transfer`: `--to` (recipient address); `--amount` (human-readable, e.g. `1.5`).
   - `approve`: `--spender` (spender address); `--amount` (human-readable) **or**
     `--approve-max` (grant unlimited authority — mutually exclusive with `--amount`).
   - `transfer-from`: `--from` (token holder address whose allowance is spent);
     `--to` (recipient address); `--amount` (human-readable).

If any required argument is missing, ask for it before proceeding.

## Prerequisite

The `eth-signer-mcp` server MUST be connected as an MCP server in this session
(this skill calls its `get_address` tool to learn the sender). If `get_address`
is not available, tell the user to connect the signer and stop — do not guess a
sender address.

## Procedure

1. **Identify the intent** and route to the correct helper:
   - Native ETH transfer → use `build_send_eth.py` (follow the native-ETH steps
     below).
   - ERC-20 operation (`transfer`, `approve`, `transferFrom`) → use
     `build_erc20.py <subcommand> ...` (follow the ERC-20 steps below).

### Native ETH send

2. **Validate inputs:** network is `mainnet` or `hoodi`; destination looks like
   `0x` + 40 hex; amount is a non-negative integer.
3. **Get the sender:** call the `get_address` MCP tool. Use its `address` as the
   sender (the account whose nonce we query and that will sign).
4. **Build the transaction:** run the bundled helper from the skill directory:

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
5. **Present to the user**, and stop:
   - the `TxRequest` JSON exactly as printed (ready to paste into `sign_transaction`); and
   - a human-readable summary: **network + chainId**, **from → to**, the **amount in
     gwei AND the resulting `value` in wei AND the ETH equivalent**, the **nonce**, and
     **maxFeePerGas / maxPriorityFeePerGas**.

   Make the amount units loud: gwei is a tiny unit, so showing wei + ETH lets the
   user catch a mis-entered amount before they sign.

### ERC-20 operation

2. **Validate inputs:** network is `mainnet` or `hoodi`; all addresses look like
   `0x` + 40 hex; amount is a human-readable decimal string (e.g. `1.5`) unless
   `--approve-max` is used.
3. **Get the sender:** call the `get_address` MCP tool. Use its `address` as
   `--sender` (and as the spender for `transfer-from`).
4. **Build the transaction:** run the appropriate subcommand from the skill directory:

   ```bash
   # ERC-20 transfer
   python3 build_erc20.py transfer \
     --network <network> \
     --token <token-address> \
     --to <recipient> \
     --amount <human-amount> \
     --sender <address-from-get_address>

   # ERC-20 approve (bounded amount)
   python3 build_erc20.py approve \
     --network <network> \
     --token <token-address> \
     --spender <spender-address> \
     --amount <human-amount> \
     --sender <address-from-get_address>

   # ERC-20 approve (unlimited — use --approve-max only when the user explicitly
   # requests it and understands the implications)
   python3 build_erc20.py approve \
     --network <network> \
     --token <token-address> \
     --spender <spender-address> \
     --approve-max \
     --sender <address-from-get_address>

   # ERC-20 transferFrom
   python3 build_erc20.py transfer-from \
     --network <network> \
     --token <token-address> \
     --from <holder-address> \
     --to <recipient> \
     --amount <human-amount> \
     --sender <address-from-get_address>
   ```

   Output discipline:
   - **stdout** — exactly the `TxRequest` JSON (safe to pipe to the signer or `jq`).
   - **stderr** — human-readable summary block, any `WARNING:` messages, and
     (on failure) `error: ...`.
   - On failure (RPC error, bad input, gas estimation failure) the helper prints
     `error: ...` to stderr and exits non-zero. **Surface the error and stop.**

5. **Present to the user**, and stop:
   - the `TxRequest` JSON exactly as printed on stdout (ready to paste into
     `sign_transaction`); and
   - the summary from stderr, which includes: operation, network + chainId, token
     address, token symbol, decimals, human amount, base-unit amount, role-specific
     addresses, nonce, gas, maxFeePerGas, maxPriorityFeePerGas.

## Safety surface (ERC-20)

- **`--approve-max`** — requires an explicit flag; cannot be combined with `--amount`.
  The helper prints a loud `WARNING:` block on stderr naming the token, spender, and
  the grant of unlimited transfer authority, plus a reminder to revoke with
  `approve(spender, 0)` if no longer needed. Only use when the user explicitly and
  knowingly requests it.
- **`transfer-from` allowance soft-check** — the helper reads the current allowance
  via `eth_call` before building the transaction. If the allowance is below the
  requested amount, it prints a `WARNING:` (warn-don't-block): the transaction is
  still assembled because the allowance could increase before broadcast. If the
  allowance RPC call itself fails, a separate `WARNING:` notes the check was skipped
  and the build continues.
- **`eth_estimateGas` — no fallback** — failures surface immediately as `error: ...`
  + exit 1. There is no silent fallback to a hardcoded gas number; a transaction that
  would revert on-chain (e.g. because of an insufficient balance or missing approval)
  is caught here and reported, not signed and broadcast to burn its gas budget.

## Notes

- Networks are hardcoded in both helpers: `mainnet` → chainId 1, `hoodi` → chainId
  560048; each with a public RPC endpoint.
- Fees follow the standard wallet heuristic: `maxPriorityFeePerGas` from the node
  (fallback 1 gwei), `maxFeePerGas = baseFee*2 + tip`.
- For native ETH: `gas` is fixed at `21000` (intrinsic cost of a plain transfer).
  For ERC-20: `gas` is `eth_estimateGas` + 20% buffer, capped at 300,000.
- Human-readable amounts (e.g. `1.5`) are converted to base units using the token's
  `decimals()` return value — read live from the chain. Conversion is integer-only:
  no floating-point arithmetic is used anywhere on the amount path.
- **chain-id guard (downstream):** if the signer was started with `--chain-id`, it
  must equal this network's chainId or `sign_transaction` will return
  `chain_id_mismatch`. This skill does not sign, but flag it so the later sign step
  isn't a surprise.
- This skill makes outbound RPC calls; the `eth-signer-mcp` signer itself remains
  strictly offline. The two concerns are separate.

## Out of scope (v1)

- permit / gasless approvals (EIP-2612)
- ERC-721 / ERC-1155 token transfers
- DEX routers and swaps
- Multi-token batch operations
- Fee-on-transfer / rebasing token handling
- Gasless / meta-transaction relaying
- Signing (handled by `eth-signer-mcp`)
- Broadcasting (handled by a separate broadcast tool / skill)
- Transaction types other than EIP-1559
- Arbitrary contract calls
