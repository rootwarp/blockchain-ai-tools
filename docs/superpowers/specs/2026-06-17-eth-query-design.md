# eth-query skill — design

- **Date:** 2026-06-17
- **Status:** Approved (brainstorming), pending implementation plan
- **Type:** New Claude Code skill (instructions-only)

## Context & motivation

The repo has two Ethereum skills:

- **`eth-jsonrpc`** — low-level JSON-RPC companion. Has a `balance` op (native ETH)
  and a `batch` op, and its `ERC20.md` usage section documents reading ERC-20
  `balanceOf` via `batch` + `eth_call`. It **explicitly lists "ABI decoding for
  `eth_call` return data" and decimals conversion as out of scope** — so today,
  reading an ERC-20 balance means hand-building calldata and manually dividing by
  `10**decimals`.
- **`eth-tx-builder`** — builds (does not sign) native + ERC-20 transactions.

`ERC20.md` (repo root) is the curated, mainnet-only token list (USDT, USDC, stETH,
eETH) with contract addresses and decimals, plus a documented `balanceOf` workflow.

**Gap:** there is no single, user-friendly "what does this account hold" reader that
returns native ETH **and** decoded ERC-20 balances (human amounts) in one shot.
`eth-query` fills exactly that gap.

## Goals

- Given an **address** + **network**, produce a combined **holdings report**: native
  ETH balance plus decoded ERC-20 balances for the `ERC20.md` tokens.
- Decode ERC-20 raw base units into **exact** human amounts using each token's
  decimals (no float precision loss).
- Be a thin **orchestration recipe** over existing tooling — no new bundled code.

## Non-goals

- No signing, building, or broadcasting (those are `sign_transaction` /
  `eth-tx-builder` / `eth-jsonrpc broadcast`).
- No ad-hoc / arbitrary token addresses — **`ERC20.md` list only** (USDT, USDC,
  stETH, eETH).
- No general `eth_*` passthrough (that is `eth-jsonrpc`).
- No bundled Python script or automated test suite — see Design.

## Design overview

`eth-query` is an **instructions-only skill**: a `SKILL.md` (+ `README.md`) that
documents the *procedure* for orchestrating the skills already in the repo. There is
**no `eth_query.py`** and no pytest suite. Claude executes the recipe directly,
driving `eth-jsonrpc`'s bundled `eth_rpc.py` and reading `ERC20.md` at runtime as the
token source of truth.

### Files

```
.claude/skills/eth-query/
  SKILL.md     # frontmatter (name/description) + the procedure
  README.md    # human-facing overview, mirrors the other two skills
```

No other files. (Matches the "just instructions for using other skills" intent.)

## Interface / invocation semantics

Conceptual inputs the skill resolves before acting:

- **address** — `0x` + 40 hex. If the user means their own / the signer's address,
  resolve via the `get_address` MCP tool. If no address and no self-reference, **ask**.
- **network** — `mainnet | hoodi | sepolia | holesky`. **Never assume**; ask if not
  named (same discipline as `eth-jsonrpc`). Offer mainnet/hoodi/sepolia (holesky
  deprecated Sept 2025, offer on request).
- **scope** — `all` (default) | `native` | `tokens`. Controls which sections of the
  report are produced.

## Procedure

1. **Resolve inputs** (address, network, scope) per the rules above. Validate the
   address shape up front for a clean error.

2. **Native ETH** (when scope is `all` or `native`): invoke the **`eth-jsonrpc`
   `balance`** op:
   ```bash
   cd .claude/skills/eth-jsonrpc
   python3 eth_rpc.py balance --network <net> --address 0x<40hex>
   ```
   Capture `balanceWei` and `balanceEth`.

3. **ERC-20 balances** (when scope is `all` or `tokens`; **mainnet only**):
   1. **Read `ERC20.md`** (repo root) and take the token table: for each row,
      `symbol`, `address`, `decimals`.
   2. For each token, build `balanceOf` calldata:
      `0x70a08231` + `000000000000000000000000` + `<40-hex account, no 0x>`.
   3. Run **one** `eth-jsonrpc` `batch` with an `eth_call` per token on **mainnet**:
      ```bash
      cd .claude/skills/eth-jsonrpc
      python3 eth_rpc.py batch --network mainnet --calls '[ {"method":"eth_call",
        "params":[{"to":"<token>","data":"0x70a08231...<acct>"},"latest"]}, ... ]'
      ```
   4. For each returned 32-byte hex result, decode to an exact human amount with the
      token's decimals (see Precision).

4. **Assemble + present** the combined report (see Output), units loud, zero balances
   shown explicitly as `0`.

## Precision (exact decimal conversion)

`ERC20.md`'s `int(result,16)/10**decimals` is float-lossy for 18-decimal tokens. The
skill prescribes an **exact** integer conversion via an inline one-liner (no bundled
file), e.g.:

```bash
python3 -c "import sys;raw=int(sys.argv[1],16);d=int(sys.argv[2]);q,r=divmod(raw,10**d);print(f'{q}.{r:0{d}d}'.rstrip('0').rstrip('.') if d else str(q))"  <hexresult> <decimals>
```

Produces a full-precision decimal string (e.g. `1234.56`, or the integer when the
remainder is zero). The exact formatting (trailing-zero trimming, etc.) is finalized
during implementation; the invariant is **no float rounding**.

## Network handling (mainnet-only ERC-20)

`ERC20.md` addresses exist on **mainnet only**.

- **scope `all` on a non-mainnet network:** return the native ETH balance and **skip
  the ERC-20 section** with an explicit note ("ERC-20 holdings are mainnet-only;
  skipped on `<net>`").
- **scope `tokens` on a non-mainnet network:** **hard-stop** with a clear message
  explaining the mainnet-only constraint.
- **scope `native`:** any network is fine.

## Error handling

- **Subprocess failure** (`eth_rpc.py` exits non-zero / prints `error:`): surface the
  error and stop that section — do not present a partial/guessed result.
- **Per-token resilience:** if a `batch` entry returns an error envelope
  (`{"id":i,"error":{...}}`), record that token's `error` and **continue** — one bad
  token does not sink the whole report.
- **Rebasing tokens** (stETH, eETH): `balanceOf` is the source of truth (what we
  call), so balances are correct with no special handling — note this in the report.

## Output / report format

A combined holdings summary, e.g.:

```
Address: 0x...      Network: mainnet
Native:  1.234567890123456789 ETH  (1234567890123456789 wei)
Tokens (ERC20.md, mainnet):
  USDT   1,250.00         (decimals 6)
  USDC   0                (decimals 6)
  stETH  3.50…  (rebasing) (decimals 18)
  eETH   <error: …>
```

The skill shows both raw base units and decoded amounts where it aids verification,
and always shows the network so a wrong-chain query is obvious.

## Triggering / description (disambiguation)

`SKILL.md` frontmatter `description` is tuned to fire on holdings-shaped requests —
"what does this account hold", "token balances", "portfolio", "USDT/USDC/stETH/eETH
balance", "all balances" — while making clear it is the **high-level combined reader**
that decodes ERC-20 amounts, distinct from `eth-jsonrpc` (low-level, raw) and
`eth-tx-builder` (builds txs). A one-line cross-reference is added to `eth-jsonrpc`'s
and `ERC20.md`'s docs pointing at `eth-query` for the decoded holdings view (light
cross-linking only; no behavior change to those skills).

## Verification

No automated tests (instructions-only skill). Instead `SKILL.md` carries a **worked
example** (a known mainnet address → expected report shape) for manual verification,
and the procedure is exercised end-to-end once against mainnet during implementation.

## Out of scope

- Ad-hoc token addresses; on-chain `decimals()` discovery.
- Non-mainnet ERC-20 token lists.
- Signing / building / broadcasting.
- Multi-address or multi-network fan-out in a single invocation.
