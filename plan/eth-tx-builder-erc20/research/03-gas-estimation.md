> Reconstructed from the research-workflow transcript; figures reconciled against adversarial verification (see [`00-overview.md`](00-overview.md) §3).

# Research: eth_estimateGas behavior for ERC-20 contract calls — buffering & failure policy

**Angle:** `gas-estimation` (informs PRD §9 gas policy; §Risks no-fallback policy)
**Template:** B — Technical Deep Dive

## Summary

The PRD's gas-estimation design is sound. Send `eth_estimateGas` with
`{from: sender, to: token, data, value: "0x0"}` against `"latest"`, multiply the
result by 1.2 (integer math `(est * 12) // 10`), cap at `300_000`, and STOP on
any RPC error. Two refinements worth documenting:

1. **Include `from`** even though the spec marks it optional. ERC-20 logic
   branches on `msg.sender`; a missing `from` collapses simulation onto
   `address(0)`, which has no balance/allowance and triggers deterministic
   reverts.
2. **Do NOT include `gasPrice` / `maxFeePerGas`** in the estimate call. Some node
   configs enforce a balance check (`gasPrice * gas <= balance`) that produces
   spurious reverts on zero-ETH accounts.

## eth_estimateGas request shape (spec)

Per the Ethereum execution-apis OpenRPC spec, `eth_estimateGas` takes a
`GenericTransaction` (required) and a `BlockNumberOrTag` (optional, default
`'latest'`). The `from` field is marked **OPTIONAL** in the spec; in practice it
is operationally **REQUIRED** for any ERC-20 call because the contract branches
on `msg.sender`.

Canonical call object for an ERC-20 build:

```json
{
  "from":  "0xSENDER...",
  "to":    "0xTOKEN_CONTRACT...",
  "data":  "0xa9059cbb...<padded recipient><padded amount>",
  "value": "0x0"
}
```

Block tag: `"latest"`. Do NOT include `gasPrice`, `maxFeePerGas`,
`maxPriorityFeePerGas`, or `gas`. When fee params are present, some node
implementations enforce `gasPrice * gas + value <= balance`, causing
estimateGas to fail with "insufficient funds for gas * price + value" even when
the contract call itself would succeed.

### Why `from` matters (despite the spec saying optional)

ERC-20 reference implementations branch on `msg.sender`:

- `transfer(to, amount)` → `require(_balances[msg.sender] >= amount)` — needs the
  sender's token balance.
- `transferFrom(from, to, amount)` →
  `require(_allowances[from][msg.sender] >= amount)` — needs the allowance the
  holder granted to the *spender* (`msg.sender`).
- USDC / USDT add `notBlacklisted(msg.sender)` and `notBlacklisted(to)`.

If `from` is omitted, simulation can run with `msg.sender = address(0)`, which
has zero token balance and zero allowance everywhere, so transfer/transferFrom
revert deterministically. (Note: exactly how a given node defaults a missing
`from` is **weakly sourced** community evidence — one Optimism issue suggests
geth may substitute a managed account when one is loaded, which would be even
more misleading. This skill side-steps the whole question by **always populating
`from` itself**, so node-specific defaulting behavior is moot.)

## Revert error shape (spec)

On a reverting simulation, the standardized response is:

```json
{
  "jsonrpc": "2.0",
  "id": 1,
  "error": { "code": 3, "message": "execution reverted", "data": "0x08c379a0..." }
}
```

`code: 3` is the Ethereum spec's allocation; some clients (and older geth paths)
historically returned `-32000`. go-ethereum PR #31456 normalized this so both
`eth_call` and `eth_estimateGas` return `code 3` on revert regardless of whether
a reason string was supplied.

`data` is the raw EVM revert bytes:

- `revert("msg")` → `0x08c379a0` (`Error(string)` selector) + 32-byte offset +
  32-byte length + UTF-8 bytes padded to a 32-byte multiple.
- Custom errors → `0x<4-byte selector>` + ABI-encoded args.
- No reason → empty data (`0x`).

Surface this verbatim — the operator needs it to distinguish "wrong token
address" vs. "insufficient balance" vs. "paused/blacklisted".

## How the estimator works

geth's `DoEstimateGas` runs the call through the EVM simulator and does a
**binary search with a hard gas cap** between a lower bound (the calldata's
intrinsic gas) and an upper bound (the gas cap / block gas limit), narrowing
toward the smallest gas value that completes without OOG.

Two known accuracy issues:

1. **EIP-150 63/64 rule (sub-call gas forwarding).** At most 63/64 of remaining
   gas can be passed to a sub-call. When estimation succeeds at the top level
   but a nested call would OOG, on-chain replay can revert. Mitigating this
   shortfall is the **caller's** job — i.e. the +20% buffer (or the contract
   author's) — not something geth papers over by inflating the top-level
   estimate.
2. **State drift between estimation and inclusion.** Block N's state at estimate
   time, block N+k's at mining time — transfers, approvals, or admin actions can
   change the answer.

Both argue for a buffer; neither justifies a silent hardcoded fallback when
estimation outright reverts.

### When eth_estimateGas reverts vs. errors

| Cause | Node return | Surfaced error data |
|---|---|---|
| Sender has insufficient token balance (`transfer`) | code 3 "execution reverted" | `0x08c379a0…"ERC20: transfer amount exceeds balance"` or a custom error |
| Spender has insufficient allowance (`transferFrom`) | code 3 "execution reverted" | `0x08c379a0…"ERC20: insufficient allowance"` |
| Token paused / sender or recipient blacklisted | code 3 "execution reverted" | `"Pausable: paused"` / `"Blacklistable: account is blacklisted"` |
| Wrong token address (EOA / non-ERC20 contract) | code 3 "execution reverted" | empty `0x` or garbage |
| `from` has zero ETH and node enforces balance check | code -32000 (legacy) / impl-specific | "insufficient funds…" / "gas required exceeds allowance (0)" |
| Node refused (gas cap, rate limit, transport) | code -32000 / transport error | impl-specific |

The skill does not need to discriminate — for all of them the right action is
"print the error, exit 1, do NOT emit a TxRequest."

## Buffering & cap recommendation

### Measured ERC-20 gas costs

Execution gas (excludes the 21,000 intrinsic baseline + ~600–1,100 calldata gas
for the 68-byte ERC-20 call). OpenZeppelin figures are from
alephao/solidity-benchmarks (Solidity 0.8.22, via-IR):

| Operation | OZ baseline | USDC-class (with blacklist) | Tax/reflection/fee-on-transfer |
|---|---:|---:|---:|
| `transfer` to existing holder | ~20.7k | ~35–50k | can exceed 100k |
| `transfer` to NEW holder (cold SSTORE) | ~37.8k | ~50–65k | can exceed 100k |
| `approve` | ~32.5k | ~45–55k | varies |
| `transferFrom` to existing holder | ~27.9k | ~45–55k | can exceed 100k |
| `transferFrom` to NEW holder | ~45.1k | ~55–70k | can exceed 100k |

> Verified figures: alephao `transfer` ~20.7k (existing) / ~37.8k (new),
> `approve` ~32.5k, `transferFrom` **~27.9k (existing) / ~45.1k (new)** (27,933 /
> 45,055). The new-recipient case dominates because it pays the cold-SSTORE
> first-write cost. USDC ≈ 40% pricier than DAI due to blacklist checks; real
> USDC/USDT mainnet `transfer` ≈ 65k (Bitget). Reflection / fee-on-transfer
> tokens **can exceed 100k**; a precise upper bound is not reliably sourced, so
> no single figure is claimed.

Approximate total transaction gas (intrinsic + calldata + execution):

- Standard OZ ERC-20 `transfer` to a new holder: ~21,000 + ~1,100 + ~37,800 ≈
  **~60k**.
- USDC mainnet `transfer`: **~65k** (real-world observed).

### Why +20% buffer is correct

- Industry consensus (MetaMask, Safe, Hardhat, Remix, Web3.js guidance) sits at
  **10–25%**; the PRD's 20% is squarely mainstream.
- Integer math `(est * 12) // 10` rounds down by < 1 gas unit — irrelevant given
  kilo-gas estimate variance.
- The buffer absorbs the EIP-150 63/64 sub-call gap and state drift between
  estimate and inclusion.
- Lower (+5%) leaves no margin for a single inter-block storage change; higher
  (+50%) wastes max-fee headroom (`maxFeePerGas * gas` is what the wallet locks)
  for routine transfers.

### Why the 300k cap is correct

- It is a defensive ceiling **≈ 5–6× the measured OZ `transferFrom`-to-new-holder
  cost (~45k)** — a mathematically sound ratio.
- Covers USDC/USDT mainnet transfers (~65k) with ample headroom.
- Sits ~100× below a typical ~30M block gas limit, so it never trips node-side
  caps.
- Above the cap, a "successful" estimate likely points at a pathological
  contract the operator should investigate before signing.

The cap is a safety net, not a guarantee. Operators dealing with non-standard
tokens (per PRD §Assumptions) accept that a build may need a manual override
(P2 future work).

## Why a silent hardcoded-gas fallback is dangerous

A naive design might catch the `eth_estimateGas` error and substitute a constant
(`gas = 100_000`) so the build still completes. This is **actively unsafe**:

1. **The simulation reverted for a reason** — wrong token address, insufficient
   balance, missing allowance, blacklist, or pause. Ignoring the error does not
   make those conditions disappear.
2. **On-chain replay also reverts** — the EVM runs the same calldata against the
   same (or evolved) state; the revert reproduces.
3. **The full gas budget is burned.** Per EIP-3529, refunds are capped at 1/5
   (20%) of total gas used and only apply to storage zero-outs; a revert
   produces zero refund for rolled-back writes. The tx pays intrinsic gas (21k) +
   calldata (~1k) + all execution gas up to the REVERT point.
4. **Operators may not notice immediately** — the signer returns a valid signed
   tx; the on-chain failure surfaces on Etherscan minutes later, gas already
   spent.

The PRD's "error-and-stop" policy correctly puts diagnosis at build time (free)
rather than at broadcast time (expensive and irreversible).

## Code examples (Python stdlib only)

### Correct estimate call

```python
def estimate_gas(rpc, sender_hex, token_hex, calldata_hex):
    # 'from' is MANDATORY for ERC-20 estimates even though the JSON-RPC spec
    # marks it optional. ERC-20 logic branches on msg.sender (balance,
    # allowance, blacklist); omitting 'from' collapses simulation onto
    # address(0) and reverts deterministically.
    #
    # Do NOT include gasPrice / maxFeePerGas: some node configs enforce a
    # balance check (gasPrice * gas <= balance) when fee fields are present,
    # producing spurious reverts on zero-ETH accounts.
    call = {"from": sender_hex, "to": token_hex, "data": calldata_hex, "value": "0x0"}
    est = int(rpc("eth_estimateGas", [call, "latest"]), 16)
    buffered = (est * 12) // 10           # +20%, integer math
    return min(buffered, 300_000)         # safety cap
```

### Correct error surfacing — NO fallback

```python
try:
    gas = estimate_gas(rpc, sender, token, data)
except RPCError as e:
    # spec: code 3 + raw EVM revert data (geth normalized in PR #31456).
    # Older nodes may return -32000 / "execution reverted".
    sys.stderr.write(
        f"error: eth_estimateGas failed: {e}\n"
        f"  This usually means the simulated call reverts. Common causes:\n"
        f"    - wrong --token address (not an ERC-20 contract)\n"
        f"    - --sender has insufficient token balance (for transfer)\n"
        f"    - --sender lacks allowance from --from (for transfer-from)\n"
        f"    - token is paused or one of the parties is blacklisted\n"
        f"  Refusing to fall back to a hardcoded gas limit, which would\n"
        f"  sign a transaction that burns its full gas budget on revert.\n"
    )
    sys.exit(1)
```

## Common pitfalls

- **Omitting `from`.** Spec says optional; ERC-20 reality says mandatory.
  Without it, transfer/transferFrom simulate from address(0) and revert.
- **Including fee fields in the estimate call.** Some clients gate on
  `gasPrice * gas <= balance`; strip all fee fields.
- **Using `"pending"` as the block tag.** Pending state includes mempool txs
  that may not be mined together; `"latest"` is the right anchor.
- **Buffering by a constant.** `est + 20_000` is too generous for warm-storage
  calls and too tight for cold + first-write + blacklist tokens. Multiplicative
  (`* 1.2`) scales with the actual call's complexity.
- **Catching the revert and continuing.** The silent-fallback anti-pattern. The
  rationale must live in code comments because future maintainers will be tempted
  to add a fallback "for robustness."
- **Trusting an estimate above the cap.** If the node returns > 300k for a
  routine ERC-20 transfer, something is wrong — pathological token, wrong
  calldata, or a buggy node.

## Recommendation (final)

Keep the PRD spec as written:

- Estimate object `{from: --sender, to: --token, data, value: "0x0"}` against
  `"latest"` (no fee fields).
- Buffer `gas = (eth_estimateGas_result * 12) // 10`.
- Cap `gas = min(gas, 300_000)`.
- On any RPC error: print the underlying error to stderr, exit 1, do NOT emit a
  TxRequest. No fallback path; the rationale lives in a code comment so future
  maintainers don't relax it.

The +20% buffer and 300k cap are well within industry consensus (10–25% buffer;
cap ≈ 5–6× standard ERC-20 `transferFrom`-new) and accommodate USDC/USDT-class
tokens with margin.

## Assumptions

- The skill targets standard ERC-20 contracts (OpenZeppelin-derived,
  USDC/USDT-class, vanilla Solmate). Exotic tokens (ERC-777 hooks, ERC-1363
  `transferAndCall`, rebasing, high-cost transfer-tax tokens) are out of scope
  per PRD §Assumptions; the 300k cap accommodates them as a safety net, not a
  guarantee.
- The skill always populates `from` = `--sender` in the estimate call object.
  Implicit in PRD §9 but restated because the spec marks `from` optional.
- The skill does NOT include `gasPrice` / `maxFeePerGas`, only
  `{from, to, data, value: "0x0"}`, avoiding the balance-check footgun.
- Networks in v1 (mainnet, hoodi) both run go-ethereum-derived clients with
  EIP-3529 (Berlin/London/Merge) gas semantics; no special-casing for pre-3529
  nodes.
- `"latest"` is the right block tag (vs. `"pending"`); PRD §9 already specifies
  it.
- `(est * 12) // 10` is acceptably accurate as a 20% buffer; off-by-one rounding
  is harmless because the cap absorbs per-call noise.
- The "gas required exceeds allowance" error class is treated as a hard revert
  (error-and-stop), same as code-3 reverts; the distinction is shown to the
  operator in the raw message but does not change the no-fallback decision.

## Sources

1. [eth_estimateGas — Ethereum Execution APIs](https://ethereum.github.io/execution-apis/api/methods/eth_estimateGas/) — `from` optional, block default `latest`, error code 3 on revert with raw EVM data.
2. [eth_estimateGas — MetaMask docs](https://docs.metamask.io/services/reference/ethereum/json-rpc-methods/eth_estimategas/) — confirms `from` optional; code-3 revert response shape.
3. [eth_estimateGas — Alchemy docs](https://www.alchemy.com/docs/reference/eth-estimategas) — block parameter defaults to `'latest'`.
4. [Issue #1998 — Optimism (default sender / address(0))](https://github.com/ethereum-optimism/optimism/issues/1998) — community evidence that a missing `from` has unpredictable runtime behavior. *(Weakly sourced; this skill always populates `from`, §3.)*
5. [Issue #2869 — zkevm-node (estimateGas requires funded from)](https://github.com/0xPolygon/zkevm-node/issues/2869) — "no `from` → simulation reverts" failure mode.
6. [Discussion #1944 — ethers.js (balance-check footgun)](https://github.com/ethers-io/ethers.js/discussions/1944) — fee fields in the call object cause spurious "insufficient funds" errors.
7. [Discussion #4498 — ethers.js (estimateGas on empty account)](https://github.com/ethers-io/ethers.js/discussions/4498) — confirms the from-address-balance footgun.
8. [Discussion #4272 — ethers.js (transfer amount exceeds balance)](https://github.com/ethers-io/ethers.js/discussions/4272) — error returned when `from` lacks token balance.
9. [PR #31456 — go-ethereum (code 3 from call/estimateGas)](https://github.com/ethereum/go-ethereum/pull/31456) — normalized code 3 for revert across DoCall and DoEstimateGas.
10. [ERC-20 Solidity benchmarks 0.8.22 via-IR — alephao](https://github.com/alephao/solidity-benchmarks/blob/main/benchmarks/0.8.22-via-ir/ERC20.md) — measured gas. *(transferFrom corrected to ~27.9k / ~45.1k, §3.)*
11. [Ethereum Gas Fee USDC Transfer Cost — Bitget](https://www.bitget.com/wiki/ethereum-gas-fee-usdc-transfer-cost) — USDC/USDT mainnet `transfer` ≈ 65k.
12. [USDC blacklist cost — CryptoSlate](https://cryptoslate.com/usdc-blacklist-cost-users-an-extra-3-6-million-per-month/) — USDC ≈ 40% pricier than DAI (blacklist checks).
13. [ERC20 Weirdness & Attacks Part 1 — 33audits](https://33audits.hashnode.dev/erc20-weirdness-attacks-part-1) — USDC/USDT `notBlacklisted` modifier reverts when either party is blacklisted.
14. [The Pitfalls of eth_estimateGas — Arkis](https://arkis.xyz/blog/the-pitfalls-of-eth-estimategas) — EIP-150 63/64 sub-call gas forwarding; why estimates can undershoot for nested calls.
15. [Estimate Gas Dynamically: Best Practices — Hedera](https://hedera.com/blog/estimate-gas-dynamically/) — 20–25% buffer consensus.
16. [Gas estimation — Safe Wallet](https://help.safe.global/en/articles/40828-gas-estimation) — production wallet 10–20% safety buffer.
17. [EIP-1559: Fee market change](https://eips.ethereum.org/EIPS/eip-1559) — intrinsic gas formula (21000 + 16/non-zero + 4/zero calldata byte + access list).
18. [EIP-3529: Reduction in refunds](https://eips.ethereum.org/EIPS/eip-3529) — refund cap = 1/5 of tx gas; reverts produce zero refund for rolled-back writes.
19. [Etherscan: Reasons for Failed Transactions](https://info.etherscan.com/reason-for-failed-transaction/) — on-chain reverts consume gas spent (no execution-gas refund).
20. [Issue #664 — Optimism (estimate reverts but tx succeeds)](https://github.com/ethereum-optimism/optimism/issues/664) — rare L2-specific counter-case; not applicable to v1's geth-derived mainnet/hoodi targets, so it does not justify a fallback.
