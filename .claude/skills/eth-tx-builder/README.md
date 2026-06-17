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
  human-amount conversion, soft-check warnings, `--summary-only` dry-run mode,
  stdout=JSON / stderr=summary discipline). No third-party packages.
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

## ERC-20 flags quick reference

| Flag | Subcommands | Description |
|---|---|---|
| `--token` | all | ERC-20 contract address |
| `--sender` | all | Signing account address (from `get_address`) |
| `--amount` | `transfer`, `approve`, `transfer-from` | Human-readable amount (e.g. `1.5`) |
| `--approve-max` | `approve` | Grant unlimited (`MAX_UINT256`) authority; mutually exclusive with `--amount` |
| `--revoke` | `approve` | Revoke approval (sets allowance to 0 for spender); mutually exclusive with `--amount` and `--approve-max` |
| `--summary-only` | all | Print stderr summary + warnings; suppress stdout JSON (dry-run preview) |

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

> **Status — live broadcast DEFERRED (Risk R1).** Pre-flight (a) and (b) are complete:
> the `eth-signer-mcp` signer is reachable and its wallet
> `0x6302794A4F2487a2540c40E2dbB211Ff6AF1CD20` holds ~0.86 hoodi ETH (chainId
> `0x88bb0` / 560048) — sufficient for three sub-300k-gas EIP-1559 broadcasts.
> Pre-flight (c) is **blocked**: the signer wallet currently holds **no** ERC-20 on
> hoodi (lifetime nonce 1, no token balance), and the PRD specifies the e2e token is
> operator-provided. The publicnode endpoint also restricts `eth_getLogs` token
> discovery (`address` filter required), so a held token cannot be auto-discovered.
> The three transcript blocks below are intentionally left as placeholders — they
> will be filled in by a follow-up run once an operator funds the wallet with a
> standard-surface hoodi ERC-20 (per the plan's R1 mitigation: do **not** fall back
> to mainnet, do **not** fabricate transcripts).

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

### Phase 2 preview — `--summary-only` dry-run runs

> **Additivity check (2.8c) — PASSED.** Phase 2 is proven additive: the
> happy-path `TxRequest` JSON for all three ops (`transfer`, `approve`,
> `transfer-from`) is **byte-identical** before and after the Phase 2 changes,
> verified deterministically with fixed RPC inputs (the soft-checks and
> `--summary-only` only add stderr warnings / suppress stdout; they never alter
> `tx_dict`). The live on-chain `--summary-only` recordings against a real
> hoodi/sepolia ERC-20 are **deferred (Risk R1)** — the signer wallet holds no
> testnet ERC-20 and the e2e token is operator-provided. The deterministic
> sample below shows the exact `--summary-only` stderr surface (stdout is empty
> on a dry run).

Representative `--summary-only` dry run (deterministic mock inputs; `sepolia`):

```console
$ python3 build_erc20.py transfer --network sepolia \
    --token 0x1111...1111 --to 0x2222...2222 --amount 1.5 \
    --sender 0x5555...5555 --summary-only
# stdout: (empty — JSON suppressed by --summary-only)
# stderr:
--- ERC-20 transaction summary ---
operation         : transfer
network           : sepolia (chainId 11155111)
token             : 0x1111111111111111111111111111111111111111
symbol            : USDC
decimals          : 6
amount (human)    : 1.5
amount (base units): 1500000
from (sender)     : 0x5555555555555555555555555555555555555555
to (recipient)    : 0x2222222222222222222222222222222222222222
nonce             : 5
gas               : 78066
maxFeePerGas      : 3000000000 wei
maxPriorityFeePerGas: 1000000000 wei
----------------------------------
```

### Phase 3 — `approve --revoke` and legacy-token symbol coverage

**Revoking an approval (sets allowance to 0):**

```bash
# Revoke approval (sets allowance to 0)
python3 build_erc20.py approve \
  --network mainnet \
  --token 0x... \
  --spender 0xRouter... \
  --revoke \
  --sender 0x...
```

`--revoke` is mutually exclusive with `--amount` and `--approve-max`. The resulting
calldata is `approve(spender, 0)`. The stderr summary shows `operation: revoke`; a
confirmation block names the token and spender.

#### Issue 3.8 e2e capture

**`--revoke` build (deterministic; sepolia inputs).** Exit 0; the amount word of the
calldata is all-zeros (`approve(spender, 0)`):

```console
$ python3 build_erc20.py approve --network sepolia --token 0x1111...1111 \
    --spender 0x4444...4444 --revoke --sender 0x5555...5555
# stderr:
Revoking approval: setting allowance to 0 for
  token  : USDC (0x1111111111111111111111111111111111111111)
  spender: 0x4444444444444444444444444444444444444444
This transaction calls approve(spender, 0).
--- ERC-20 transaction summary ---
operation         : revoke
...
amount (base units): 0
...
# stdout (data): 0x095ea7b3 <spender padded> 0000...0000   (amount word = 32 zero bytes)
```

> **`--revoke` LIVE broadcast + on-chain `allowance(sender,spender)==0` verification:
> DEFERRED (Risk R1).** The signer wallet holds no testnet ERC-20 and the e2e token is
> operator-provided; setting/clearing allowance requires a live broadcast we cannot make
> autonomously. When an operator funds a hoodi ERC-20, broadcast the JSON above via the
> `eth-jsonrpc` skill and verify the post-state with `eth-jsonrpc`'s generic `call` op against
> `allowance(address,address)` (selector `0xdd62ed3e`); the return must be 32 zero bytes.

**Legacy-token `symbol()` coverage — LIVE mainnet read-only readback (Issue 3.4 / 3.8).**
The polished `decode_symbol` was verified against real on-chain `symbol()` responses
(read-only `eth_call`, no broadcast):

| token | address | raw `symbol()` (head) | `decode_symbol` → |
|---|---|---|---|
| MKR (legacy bytes32) | `0x9f8F72aA9304c8B593d555F12eF6589cC3A579A2` | `0x4d4b5200…` | `"MKR"` |
| USDC (standard ABI string) | `0xA0b86991c6218b36c1d19D4a2e9Eb0cE3606eB48` | `0x0000…0020…` | `"USDC"` |

See **ADR-013** in `plan/eth-tx-builder-erc20/architecture.md` for the bounded format
catalog (standard ABI string → null-trimmed bytes32 → length-prefixed bytes32 → `None`).

> **Phase 1 regression:** Phase 3 is additive — the happy-path `TxRequest` JSON for
> `transfer` / `approve` / `transfer-from` is byte-identical before and after every
> Phase 3 change (verified deterministically with fixed RPC inputs). The live hoodi
> re-run of the Phase 1 three-op e2e is deferred (R1, same token dependency).
> **`permit`:** no e2e — Phase 3 ships no `permit` code (paper-only ADR-014 + draft PRD).

## Supported networks

| network | chainId | RPC | Notes |
|---|---|---|---|
| `mainnet` | 1 | `https://ethereum-rpc.publicnode.com` | |
| `hoodi` | 560048 | `https://ethereum-hoodi-rpc.publicnode.com` | Preferred testnet |
| `sepolia` | 11155111 | `https://ethereum-sepolia-rpc.publicnode.com` | |
| `holesky` | 17000 | `https://ethereum-holesky-rpc.publicnode.com` | Scheduled for deprecation (post-2025); prefer `hoodi` for new work |

Available in both `build_send_eth.py` (native ETH) and `build_erc20.py` (ERC-20).
Pass one of these values as `--network` on any subcommand.
