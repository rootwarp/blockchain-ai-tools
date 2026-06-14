# eth-tx-builder

A Claude Code skill that generates a ready-to-sign Ethereum `TxRequest` JSON for
the [`eth-signer-mcp`](../../../apps/eth-signer-mcp/README.md) `sign_transaction`
tool. Supports **native ETH send** (v1) and **ERC-20 token operations** (v2:
`transfer`, `approve`, `transferFrom`).

It does **not** sign. The skill produces transaction *data*; the user signs
separately with the signer.

## Files

- `SKILL.md` — the skill Claude follows (inputs → get_address → helper → present JSON).
- `build_send_eth.py` — stdlib-only helper: native ETH transfer (network map,
  gwei→wei, RPC for nonce + fees, fee math, TxRequest JSON). No third-party packages.
- `test_build_send_eth.py` — unit tests for `build_send_eth.py` (mocked RPC; no live network).
- `build_erc20.py` — stdlib-only helper: ERC-20 `transfer`, `approve`, `transferFrom`
  (ABI calldata encoding, `decimals()` read, `eth_estimateGas` with +20% buffer/300k cap,
  human-amount conversion, stdout=JSON / stderr=summary discipline). No third-party packages.
- `test_build_erc20.py` — unit tests for `build_erc20.py` (mocked RPC; no live network).

## Prerequisites

- `python3` (3.8+), stdlib only.
- The `eth-signer-mcp` server connected as an MCP server in the session (the skill
  calls its `get_address` tool for the sender address).
- Outbound network access to the public RPC endpoints (hardcoded per network).

## Run the tests

```bash
cd .claude/skills/eth-tx-builder
python3 -m unittest test_build_send_eth -v
python3 -m unittest test_build_erc20 -v
```

No live RPC calls are made — the transport is mocked in both test files.

## Manual end-to-end (use hoodi — testnet, no real funds)

### Native ETH send

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

## Manual end-to-end (hoodi) — ERC-20 operations

> **Prerequisites for this section:**
> - A funded hoodi test wallet connected to `eth-signer-mcp` (enough hoodi ETH for
>   three EIP-1559 broadcasts at the gas cap of 300,000).
> - A standard-surface ERC-20 deployed on hoodi with a `decimals()` / `symbol()` /
>   `transfer` / `approve` / `transferFrom` surface (no fee-on-transfer or rebasing).
>   The test wallet must hold a non-zero token balance large enough to cover both the
>   `transfer` run and the `transfer-from` run.
>
> **Serialization requirement:** the `approve` run MUST be broadcast and confirmed
> mined on hoodi (poll `eth_getTransactionReceipt` until `status = 1`) BEFORE the
> `transfer-from` run is attempted. The `transfer-from` transaction depends on the
> on-chain approval; running it before the approve mines will produce a revert.

### ERC-20 transfer

```bash
cd .claude/skills/eth-tx-builder
python3 build_erc20.py transfer \
  --network hoodi \
  --token <TOKEN> \
  --to <TO> \
  --amount <AMOUNT> \
  --sender <SENDER>
```

<insert stderr summary block here — filled in by Issue 1.10b>

<insert stdout JSON here — filled in by Issue 1.10b>

<insert paste-to-signer step + signer response — filled in by Issue 1.10b>

<insert broadcast step + tx hash — filled in by Issue 1.10b>

<insert on-chain confirmation (block number / receipt status) — filled in by Issue 1.10b>

### ERC-20 approve --amount

```bash
cd .claude/skills/eth-tx-builder
python3 build_erc20.py approve \
  --network hoodi \
  --token <TOKEN> \
  --spender <SPENDER> \
  --amount <AMOUNT> \
  --sender <SENDER>
```

> Use `--amount` (bounded) for the e2e — not `--approve-max` — to avoid leaving an
> unlimited grant on the test wallet. After this transaction mines, the `<SPENDER>`
> (the signer wallet) is authorized to call `transferFrom` on behalf of `<SENDER>`
> up to `<AMOUNT>` tokens.

<insert stderr summary block here — filled in by Issue 1.10b>

<insert stdout JSON here — filled in by Issue 1.10b>

<insert paste-to-signer step + signer response — filled in by Issue 1.10b>

<insert broadcast step + tx hash — filled in by Issue 1.10b>

<insert on-chain confirmation (block number / receipt status) — filled in by Issue 1.10b>

### ERC-20 transfer-from

> Run this step only after the `approve --amount` transaction above is confirmed mined
> (receipt `status = 1`).

```bash
cd .claude/skills/eth-tx-builder
python3 build_erc20.py transfer-from \
  --network hoodi \
  --token <TOKEN> \
  --from <FROM> \
  --to <TO> \
  --amount <AMOUNT> \
  --sender <SENDER>
```

(`<FROM>` is the token holder whose allowance was set by the `approve` step;
`<SENDER>` is the signer / spender wallet that was granted that allowance.)

<insert stderr summary block here — filled in by Issue 1.10b>

<insert stdout JSON here — filled in by Issue 1.10b>

<insert paste-to-signer step + signer response — filled in by Issue 1.10b>

<insert broadcast step + tx hash — filled in by Issue 1.10b>

<insert on-chain confirmation (block number / receipt status) — filled in by Issue 1.10b>

## Networks

| network | chainId | RPC |
|---|---|---|
| `mainnet` | 1 | `https://ethereum-rpc.publicnode.com` |
| `hoodi` | 560048 | `https://ethereum-hoodi-rpc.publicnode.com` |
