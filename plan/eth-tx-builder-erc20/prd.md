# PRD: eth-tx-builder ERC-20 extension

## Overview

Extend the existing `eth-tx-builder` Claude Code skill so it can build the full
ERC-20 token movement set — `transfer`, `approve`, `transferFrom` — in addition
to native ETH transfers. The skill continues to produce a ready-to-sign EIP-1559
`TxRequest` JSON for the `eth-signer-mcp` `sign_transaction` tool. It does NOT
sign and does NOT broadcast. The native ETH-send path stays exactly as today;
ERC-20 lives in a new helper script alongside it.

## Problem Statement

Today the skill can only emit a native value transfer (`build_send_eth.py`,
fixed `gas=21000`, `data="0x"`). Any user who wants to move an ERC-20 token —
the dominant on-chain transfer pattern outside of ETH itself — has to hand-
construct the calldata, fetch token `decimals()` themselves, compute base
units without floating-point drift, and pick a gas limit by trial and error.
That's the exact category of error-prone, easy-to-misread work the skill was
created to remove for ETH transfers.

The three operations covered by v1 are the canonical ERC-20 movement set:

- **`transfer(to, amount)`** — the holder sends their own tokens.
- **`approve(spender, amount)`** — the holder authorizes a spender (typically
  a contract: DEX router, bridge, escrow) to pull tokens later.
- **`transferFrom(from, to, amount)`** — the spender pulls previously
  approved tokens from the holder's account.

Together they cover the lifecycle of every ordinary ERC-20 interaction.

## Goals

- **Primary goal:** an operator can build a ready-to-sign `TxRequest` for any
  ERC-20 `transfer`, `approve`, or `transferFrom` on `mainnet` or `hoodi` with
  a single CLI invocation, using human-readable amounts and a raw token
  contract address, with no new Python dependencies.
- **Safety goal:** the summary surfaces every input that could cause a costly
  mistake — token symbol, decimals, the resolved base-unit amount, the
  spender (for `approve`), and the source account (for `transferFrom`) — so
  the user can catch errors before signing.
- **Preserve v1:** the native ETH-send path is unchanged. `build_send_eth.py`
  and its tests stay untouched; the existing flow continues to work
  bit-for-bit.
- **Keep the house style:** stdlib only, error-and-stop, mocked-RPC unit
  tests, no float arithmetic on token amounts, signer stays offline.

## Success Metrics

- **Functional coverage:** each of the three operations (`transfer`,
  `approve`, `transferFrom`) produces a `TxRequest` JSON that
  `eth-signer-mcp` accepts via `sign_transaction` and, when signed and
  broadcast on `hoodi`, executes successfully on-chain (verified in the
  manual end-to-end checklist).
- **Real-token decimals read:** the `decimals()` RPC read works against a
  real ERC-20 deployed on `hoodi` (operator picks one for the manual e2e).
- **Zero new dependencies:** `python3 build_erc20.py --help` works on a
  fresh stdlib-only Python 3.8+ install. `requirements.txt` is not added.
- **No regression in ETH-send:** `python3 -m unittest
  test_build_send_eth -v` continues to pass with no edits to
  `build_send_eth.py` or `test_build_send_eth.py`.
- **Test coverage of the new helper:** `test_build_erc20.py` covers, at
  minimum: selector + arg encoding for each of the three ops; happy-path
  `do_transfer` / `do_approve` / `do_transferFrom` with mocked RPC;
  `decimals()` parsing (including 0 and 18); human → base unit conversion
  with no float drift; `--approve-max` path; `--from` validation and
  allowance soft-check warning; `eth_estimateGas` failure surfaces as
  error-and-stop (no fallback); address validation; `symbol()` failure is
  non-fatal (summary degrades gracefully).

## Target Users

- **Primary: the skill operator driving Claude Code.** Wants to move an
  ERC-20 from a managed keystore on `mainnet` or `hoodi`, picks the operation
  intent in chat, gets a JSON they can paste into the signer.
- **Secondary: testnet developers on `hoodi`.** Need to approve a router or
  move a freshly-deployed token to a test wallet; safety-rail UX matters
  less to them than working calldata.
- **Tertiary: scripted / repeated builds.** May invoke the helper directly
  from a shell for reproducible test setups. The CLI is the public contract.

## User Stories / Use Cases

- **As an operator,** I want to run
  `python3 build_erc20.py transfer --network mainnet --token 0xA0b8...
  --to 0x70997... --amount 1.5 --sender 0xabc...`
  and get a `TxRequest` JSON for a 1.5-token transfer, with the decimals
  fetched live and shown in the summary, so I can confirm "1.5 USDC" really
  resolved to `1500000` base units before signing.
- **As an operator,** I want to run
  `python3 build_erc20.py approve --network mainnet --token 0xA0b8...
  --spender 0xRouter... --amount 100 --sender 0xabc...`
  and get a bounded-allowance approval, with the spender shown loudly in
  the summary, so I don't accidentally approve the wrong contract.
- **As an operator wiring a router,** I want `--approve-max` to emit an
  unlimited (max-uint256) approval, with a loud stderr warning naming the
  spender and token, so the unlimited grant is deliberate, not accidental.
- **As a spender,** I want to run
  `python3 build_erc20.py transfer-from --network mainnet --token 0xA0b8...
  --from 0xHolder... --to 0xDest... --amount 50 --sender 0xabc...`
  and have the skill (a) emit the right calldata, (b) loudly summarize that
  I'm spending HOLDER's allowance, and (c) warn me if the current
  `allowance(HOLDER, me)` is less than 50 — but still produce the JSON,
  because the allowance might be granted later in the same workflow.
- **As an operator,** if `eth_estimateGas` fails (token reverts on the
  simulated call — wrong address, no balance, paused contract), I want the
  skill to print the underlying error and stop, NOT silently fall back to
  a hardcoded gas limit that would later revert on-chain and burn the gas.
- **As a v1 user with existing flows,** I want my old
  `python3 build_send_eth.py --network ... --to ... --amount-gwei ...
  --sender ...` invocations to keep working exactly as before, untouched.

## Assumptions

- **Token standard:** the target contract implements the standard ERC-20
  surface — `decimals() returns (uint8)`, `symbol() returns (string)`,
  `balanceOf(address) returns (uint256)`,
  `allowance(address,address) returns (uint256)`,
  `transfer(address,uint256) returns (bool)`,
  `approve(address,uint256) returns (bool)`,
  `transferFrom(address,address,uint256) returns (bool)`. Non-standard or
  weird tokens (fee-on-transfer, rebasing, `decimals` reverting, `symbol`
  returning `bytes32` instead of `string`) are warned about, not handled.
- **Amount semantics:** the `--amount` value is the *requested* transfer
  amount. For fee-on-transfer tokens the delivered amount may be lower; the
  skill cannot detect this and does not try.
- **Signer identity:** the `--sender` address (sourced via `get_address` per
  SKILL.md) is the signer. For `transferFrom`, the signer is the *spender*;
  the `--from` argument is the *source* (the holder).
- **Approve race:** the well-known ERC-20 "approve race" (changing a non-zero
  allowance to a new non-zero allowance) is acknowledged but not papered
  over; operators concerned about it must approve `0` first themselves.
- **chainId guard downstream:** if the signer was started with `--chain-id`,
  it must match the network's chainId or `sign_transaction` returns
  `chain_id_mismatch`. Same caveat as the v1 ETH path.
- **Networks:** `mainnet` and `hoodi` only in v1, matching the existing
  network map. No new public RPC endpoints.
- **RPC posture:** the skill makes outbound RPC calls (now more than v1 —
  one extra `eth_call` for `decimals()`, one for `symbol()`, one for
  `eth_estimateGas`, plus one for `allowance` on `transfer-from`). The
  signer remains strictly offline; the two concerns stay separate.

## Functional Requirements

### Must Have (P0)

1. **New helper `build_erc20.py`** in
   `.claude/skills/eth-tx-builder/`. Stdlib only. Three subcommands:
   `transfer`, `approve`, `transfer-from`. Each prints a `TxRequest` JSON
   on success (exit 0) and `error: <message>` to stderr on failure (exit 1).

2. **CLI shape — `transfer`:**

   ```
   python3 build_erc20.py transfer \
     --network <mainnet|hoodi> \
     --token <0x-address> \
     --to <0x-address> \
     --amount <human-readable-decimal-string> \
     --sender <0x-address>
   ```

3. **CLI shape — `approve`:**

   ```
   python3 build_erc20.py approve \
     --network <mainnet|hoodi> \
     --token <0x-address> \
     --spender <0x-address> \
     (--amount <human-readable-decimal-string> | --approve-max) \
     --sender <0x-address>
   ```

   `--amount` and `--approve-max` are mutually exclusive; one is required.

4. **CLI shape — `transfer-from`:**

   ```
   python3 build_erc20.py transfer-from \
     --network <mainnet|hoodi> \
     --token <0x-address> \
     --from <0x-address> \
     --to <0x-address> \
     --amount <human-readable-decimal-string> \
     --sender <0x-address>
   ```

5. **Token identification — contract address only.** `--token` accepts a
   `0x` + 40 hex address. The skill does NOT maintain a symbol registry
   (USDC → address, etc.); name-to-address resolution is the caller's
   responsibility (the Claude Code assistant can do it before invoking).

6. **Human-readable amounts → base units via `decimals()`.** For every op
   except `--approve-max`:
   - The skill calls `eth_call` to the token's `decimals()` method
     (selector `0x313ce567`) on `latest`, parses the returned uint8, and
     uses it to convert `--amount` to base units.
   - Conversion is integer-only: split `--amount` on the decimal point,
     pad/truncate the fractional part to exactly `decimals` digits, and
     concatenate. Reject more fractional digits than `decimals` with a
     clear error (`amount has more fractional digits (N) than token
     decimals (M)`).
   - Floats are NEVER used; the conversion path is `str → str → int`.
   - Reject negative amounts, empty strings, multiple decimal points,
     non-digit characters, and `0` only if `--amount 0` was passed (zero
     is *allowed*: a zero-transfer or zero-approve is a legitimate op).

7. **`--approve-max` for unlimited approvals.**
   - Encodes `amount = 2**256 - 1` (max uint256) into the calldata.
   - Skips the `--amount` arg (mutually exclusive).
   - Before printing the JSON, writes a loud multi-line stderr warning
     naming the token (with symbol if available), the spender, and the
     fact that this grants unlimited transfer authority until revoked.
     Example wording: `WARNING: --approve-max grants UNLIMITED transfer
     authority on <SYMBOL> (<token-addr>) to spender <spender-addr>.
     Revoke later with approve(spender, 0) if no longer needed.`

8. **Calldata encoding — stdlib + hardcoded selectors.**
   - Function selectors are module-level constants with a one-line
     derivation comment:
     - `transfer(address,uint256)`     → `0xa9059cbb`
     - `approve(address,uint256)`      → `0x095ea7b3`
     - `transferFrom(address,address,uint256)` → `0x23b872dd`
     - (Read selectors, used internally for `eth_call`:
       `decimals()` → `0x313ce567`, `symbol()` → `0x95d89b41`,
       `allowance(address,address)` → `0xdd62ed3e`.)
   - Arguments are encoded as 32-byte (64-hex-char) left-padded words.
     Addresses pad to the right of 12 zero bytes; `uint256` values pad
     left-zero to 32 bytes.
   - No Keccak dependency. No `eth-abi`. No `pycryptodome`. All ABI
     encoding is implemented inline with string concatenation and
     `int.to_bytes` / `bytes.fromhex`.

9. **Gas estimation — `eth_estimateGas` with buffer and cap, no fallback.**
   - Build the populated call object `{from, to=token, data,
     value="0x0"}`, send `eth_estimateGas`, multiply the returned value by
     **1.2** (integer math: `(est * 12) // 10`), and cap at **300_000**.
   - If `eth_estimateGas` raises (any `RPCError` — node refused,
     simulation reverted, transport failed), the skill MUST surface the
     underlying error message and exit 1. It MUST NOT fall back to a
     hardcoded gas limit. The rationale is explicit in code comments:
     a silent fallback would let a transaction that will definitely
     revert on-chain get signed and burn its gas budget.

10. **Symbol lookup is best-effort, never fatal.** The skill calls
    `eth_call` to `symbol()` to enrich the summary. If the call fails or
    returns malformed data, the skill prints `(symbol unavailable)` in
    the summary and continues. Symbol is decoded as ABI `string`
    (offset, length, UTF-8 bytes); `bytes32`-style symbols (some legacy
    tokens) are accepted as a fallback by treating the result as a
    null-trimmed UTF-8 byte string when the standard decode fails.

11. **`transfer-from` allowance soft-check.**
    - Before printing JSON, the skill calls
      `allowance(from=--from, spender=--sender)` via `eth_call`.
    - If the returned allowance is less than the requested base-unit
      amount, print a loud stderr warning:
      `WARNING: current allowance is <N> (<human>); requested transfer
      is <M> (<human>). This transaction will revert unless allowance
      is increased before broadcast.`
    - The JSON is still printed; the operator decides whether to
      proceed (the allowance may legitimately be granted later in the
      same multi-step workflow).
    - If the `allowance` RPC itself errors, print a stderr warning
      noting the soft-check was skipped and continue. Allowance failure
      is NOT fatal (some tokens reject the call shape; this should not
      block the build).

12. **`--from` and `--sender` are distinct for `transfer-from`.**
    `--from` is validated as `0x` + 40 hex and is NOT auto-filled from
    `get_address`. The summary names them as "spending allowance of
    FROM" and "signer / spender = SENDER" respectively.

13. **Address validation.** All address arguments (`--token`, `--to`,
    `--spender`, `--from`, `--sender`) are validated against
    `^0x[0-9a-fA-F]{40}$`. EIP-55 checksum enforcement is downstream
    in the signer; the helper does format-only.

14. **Reuse v1 plumbing.** `build_erc20.py` reuses the v1 network map
    semantics (chainId + RPC URL per network) and the v1 fee strategy
    (`maxPriorityFeePerGas` from node with 1 gwei fallback;
    `maxFeePerGas = baseFee*2 + tip`). It MAY do so by importing the
    relevant constants/functions from `build_send_eth.py` if that does
    not require modifying `build_send_eth.py`, or it MAY duplicate them
    verbatim — implementer's choice, as long as `build_send_eth.py`
    stays bit-for-bit unchanged.

15. **Output is a single-shape `TxRequest` JSON.** Same fields the v1
    helper emits, with:
    - `to` = token contract address (not the recipient),
    - `value` = `"0"` (no ETH transferred),
    - `data` = the ABI-encoded calldata,
    - `gas` = the buffered + capped estimate,
    - all other fields (`type`, `chainId`, `nonce`,
      `maxFeePerGas`, `maxPriorityFeePerGas`) per v1 semantics.
    - All numeric fields are decimal strings (matches v1).

16. **Loud human-readable summary on stdout — but separated from the
    JSON.** The TxRequest JSON is printed on stdout in a parseable form
    (operators may pipe it to `jq` or feed it directly to the signer).
    The human-readable confirmation summary is printed to **stderr**
    so it does not pollute stdout. Summary fields:
    - operation (transfer / approve / transfer-from)
    - network + chainId
    - token: address, symbol (or "(unavailable)"), decimals
    - human amount and resolved base-unit amount (or "MAX UINT256")
    - for `transfer`: from (= sender) → to
    - for `approve`: holder (= sender) → spender
    - for `transfer-from`: source = `--from`, recipient = `--to`,
      signer/spender = `--sender`
    - nonce, gas, maxFeePerGas, maxPriorityFeePerGas

17. **`build_send_eth.py` and `test_build_send_eth.py` are unchanged.**
    The v1 ETH-send path is preserved bit-for-bit. Verified by the
    existing `test_build_send_eth` regression suite.

18. **SKILL.md updates.**
    - Description string broadened to include ERC-20.
    - "Inputs" section split into "ETH send" (existing) and "ERC-20
      transfer / approve / transferFrom".
    - "Procedure" section gains a router step: identify the intent
      (native ETH vs ERC-20 op), call the appropriate helper script.
    - "Out of scope (v1)" updated to remove ERC-20 (now in scope) and
      explicitly list the new non-goals (permit, ERC-721/1155, swaps,
      multi-token batch, fee-on-transfer / rebasing handling, gasless
      meta-tx, signing, broadcasting).

19. **README.md updates.** New file list entry for `build_erc20.py` and
    `test_build_erc20.py`; new "Manual end-to-end" section using
    `hoodi` and a real ERC-20 token deployed there.

20. **Unit tests** in `test_build_erc20.py` covering, at minimum:
    - selector + arg encoding bit-pattern check for each of the three
      ops (compare against a known-good hex string);
    - `do_transfer` / `do_approve` / `do_transfer_from` happy path
      with mocked `rpc`;
    - human → base unit conversion: 0, 0.0, 1, 1.5, 0.000001, max
      uint256-ish (large decimal), too-many-fractional-digits rejected,
      negatives rejected, non-numeric rejected, multi-dot rejected;
    - `decimals()` parsing for `0`, `6`, `18`;
    - `--approve-max` produces calldata with `2**256 - 1` and triggers
      the stderr warning;
    - `symbol()` failure path: summary degrades, build still succeeds;
    - `allowance` soft-check: low allowance warns + still emits JSON;
      `allowance` RPC error warns + still emits JSON;
    - `eth_estimateGas` failure: error-and-stop, no JSON printed;
    - address validation: bad `--token`, bad `--from`, bad `--sender`,
      bad `--to`, bad `--spender` each produce a clear error;
    - gas buffering math: `(est * 12) // 10` and cap at 300_000;
    - argparse smoke: `--help` for each subcommand and the top-level
      `--help` lists all three subcommands.

### Should Have (P1)

1. **Decimal-respecting summary in the JSON output.** Bundle a
   one-line, optional `// human` companion print or a separate
   `--summary-only` mode so operators can confirm without parsing the
   JSON. (Strictly secondary; the stderr summary already covers this.)

2. **Cheap sanity reads in the summary.** When emitting `transfer`,
   also call `balanceOf(sender)` via `eth_call` and warn if the sender
   balance is less than the requested base-unit amount (same
   "warn, don't block" posture as the `transfer-from` allowance check).

3. **Tighter `approve` race guard.** Detect a non-zero current
   allowance (`allowance(sender, spender) != 0`) when emitting a new
   non-zero approve and print a stderr note pointing at the known
   race. Don't block; operators who know what they're doing skip the
   `approve(0)` step.

4. **Additional networks.** Wire `sepolia` and `holesky` into the
   network map (mirroring the sibling `eth-rpc` Phase 2 plan), so the
   ERC-20 helper benefits as well. Two-line change + new tests.

5. **`--summary-only` (no JSON).** Dry-run mode: print the summary,
   skip the JSON. Useful for "what would this do?" preview without
   exposing the calldata in shell history.

### Nice to Have (P2)

1. **Other ERC-20 admin reads:** `totalSupply()`, `balanceOf()` as
   first-class CLI subcommands (low value vs the sibling `eth-rpc`
   `call` passthrough, which can already do these).

2. **`permit` (EIP-2612) builder.** Useful for routers that prefer
   permit over approve; out of scope here because permit requires
   signing a typed-data digest, which would expand the skill's
   responsibility beyond "build calldata."

3. **`approve(0)` shorthand.** Convenience: a `--revoke` flag on the
   `approve` subcommand that sets amount to 0. Trivial to add later.

4. **Bytes32 symbol decode polish.** Make the legacy-token symbol
   decoder bullet-proof against the half-dozen historical formats
   (MKR, DGD, etc.). Niche.

## Non-Functional Requirements

- **Stdlib only.** No `eth-abi`, no `pycryptodome`, no `web3.py`, no
  `requests`. The only allowed imports are those already used by
  `build_send_eth.py` (`argparse`, `json`, `re`, `sys`,
  `urllib.request`).
- **Determinism.** `build_tx_*` functions are pure relative to an
  injected `rpc` callable, matching the v1 style. Tests stub `rpc`.
- **No float arithmetic on token amounts.** Conversion is integer
  string manipulation only. Tests assert no `float(...)` calls in
  the conversion path (lint or `grep` is fine).
- **Performance.** A `transfer` build issues 4 RPC calls (`decimals`,
  optional `symbol`, nonce, base fee, `maxPriorityFeePerGas`,
  `estimateGas` — call it ~6 in practice). A `transfer-from` build
  adds one (`allowance`). All sequential, all bounded by `rpc_call`'s
  15-second timeout. No parallelism, no caching.
- **Security posture.**
  - The signer stays strictly offline; this helper makes outbound
    HTTP only.
  - `--approve-max` is gated behind an explicit flag and a loud
    stderr warning. No code path silently emits max-uint256.
  - `eth_estimateGas` failures stop the build. No silent fallback that
    would cause an on-chain revert.
  - No private key material is touched here.
- **Robustness.** Bad input → error-and-stop with a clear message,
  matching the v1 pattern. The skill never produces a partial
  `TxRequest`.

## Technical Considerations

- **Files added:**
  `.claude/skills/eth-tx-builder/build_erc20.py`,
  `.claude/skills/eth-tx-builder/test_build_erc20.py`.
- **Files edited:**
  `.claude/skills/eth-tx-builder/SKILL.md`,
  `.claude/skills/eth-tx-builder/README.md`.
- **Files NOT touched:**
  `.claude/skills/eth-tx-builder/build_send_eth.py`,
  `.claude/skills/eth-tx-builder/test_build_send_eth.py`.
- **Shared code strategy:** the implementer can either (a) import a
  small set of helpers from `build_send_eth.py` (e.g. `NETWORKS`,
  `network_config`, `rpc_call`, `validate_hex_address`, `parse_hex_int`,
  `compute_max_fee`, `fetch_nonce`, `fetch_base_fee`, `fetch_tip`,
  `RPCError`) without modifying that file, or (b) duplicate them in
  `build_erc20.py`. Architecture-stage decision, not PRD.
- **ABI encoding helpers** (all local to `build_erc20.py`):
  - `_encode_address(addr_hex: str) -> str` — strip `0x`, lowercase,
    left-pad with 24 zero hex chars to 64 hex chars.
  - `_encode_uint256(n: int) -> str` — integer to 64-hex-char left
    zero-padded string. Reject negative; reject `>= 2**256`.
  - `_pack_call(selector_hex: str, *args_hex: str) -> str` —
    concatenate selector and arg words, prepend `0x`.
- **`decimals()` decode:** the call returns a 32-byte word; the
  effective value is the low byte (`int(result, 16) & 0xff`). Reject
  values > 36 as suspicious (cap matches well-known token surveys;
  standard tokens are 0–18; 24 is reasonable max for exotic chains).
- **`symbol()` decode:** standard ABI `string` (32-byte offset,
  32-byte length, UTF-8 bytes padded to multiple of 32). If decode
  raises or yields non-printable garbage, fall back to a
  null-trimmed UTF-8 read of the first 32 bytes (handles legacy
  `bytes32` tokens like MKR). If even that fails, omit from summary.
- **`allowance(holder, spender)` decode:** single 32-byte uint256
  word.
- **`eth_estimateGas` params:** `{"from": sender, "to": token,
  "data": calldata, "value": "0x0"}` against `"latest"`. The result
  is a hex quantity.
- **Error-message style:** matches v1 (`error: <message>` to stderr,
  exit 1). New warning messages go to stderr with a leading
  `WARNING:` token (not `error:`), so callers can grep stdout for
  the JSON and stderr for diagnostics.

## UX / Design Notes

- **Example invocations (ship in SKILL.md + README):**

  ```bash
  # ERC-20 transfer: send 1.5 USDC on mainnet
  python3 build_erc20.py transfer \
    --network mainnet \
    --token 0xA0b86991c6218b36c1d19D4a2e9Eb0cE3606eB48 \
    --to 0x70997970C51812dc3A010C7d01b50e0d17dc79C8 \
    --amount 1.5 \
    --sender 0x...

  # Bounded approve: authorize a router for 100 tokens
  python3 build_erc20.py approve \
    --network mainnet \
    --token 0xA0b86991... \
    --spender 0xRouter... \
    --amount 100 \
    --sender 0x...

  # Unlimited approve (deliberate, prints loud warning)
  python3 build_erc20.py approve \
    --network mainnet \
    --token 0xA0b86991... \
    --spender 0xRouter... \
    --approve-max \
    --sender 0x...

  # Spender pulls tokens via transferFrom
  python3 build_erc20.py transfer-from \
    --network mainnet \
    --token 0xA0b86991... \
    --from 0xHolder... \
    --to 0xDest... \
    --amount 50 \
    --sender 0x...
  ```

- **Summary layout (stderr):** human-readable block, easy to scan,
  with the safety-critical fields (spender, source `from`,
  approve-max flag, base-unit amount) emphasized via labels.

- **Stdout discipline:** stdout contains exactly the JSON. Operators
  can `python3 build_erc20.py transfer ... | jq .` or pipe directly
  into the signer.

## Out of Scope (v1)

- **EIP-2612 `permit` / `transferWithAuthorization`** (typed-data
  signatures; outside "build calldata" remit).
- **ERC-721 / ERC-1155** (NFTs, multi-token standards).
- **DEX routers / swaps** (Uniswap V2/V3/V4, 1inch, etc.).
- **Multi-token batch** (one operation per invocation).
- **Fee-on-transfer / rebasing token quirks.** Warned in
  documentation; the skill emits the requested amount and accepts that
  delivered amount may differ for non-standard tokens.
- **Gasless meta-transactions** (ERC-4337, relayers).
- **Signing** (still owned by `eth-signer-mcp`).
- **Broadcasting** (owned by `eth-rpc`'s `broadcast` op).
- **Nonce queueing / multi-tx coordination** (one transaction per
  build).
- **EIP-7702 / account abstraction primitives.**
- **Custom RPC endpoint override** (`--rpc-url`). The v1 network map
  is reused; the sibling `eth-rpc` skill is where custom-endpoint UX
  lives. If demand emerges, parity with `eth-rpc`'s `--rpc-url +
  --chain-id` pattern is a P2 candidate.
- **Symbol → address registry.** The caller supplies the contract
  address.
- **Per-method gas fallback when `eth_estimateGas` fails.** Explicit
  non-goal — see P0 §9.

## Open Questions

These are confirmable at architecture stage; PRD encodes the
recommended default.

1. **Shared-code strategy.** Import helpers from `build_send_eth.py`
   vs duplicate them. Recommendation: import (DRY, lower drift risk),
   provided no edits to `build_send_eth.py` are required. If imports
   would require touching the v1 file, duplicate instead.
2. **Symbol decode coverage.** Standard ABI `string` + legacy
   `bytes32` fallback is enough for the top ~100 tokens by market
   cap. Outlier tokens may still display "(unavailable)" — acceptable
   for v1.
3. **Allowance soft-check on `approve` race.** Currently a P1 item
   (#3). Whether to upgrade to P0 depends on operator demand. PRD
   default: P1.
4. **`balanceOf` pre-check on `transfer`.** Same — P1 (#2) for now.
5. **Stdout vs stderr split for the summary.** PRD locks stderr for
   the summary, stdout for the JSON. Confirm at review.

## Milestones & Phases

- **Phase 1 — P0 implementation.**
  1. Create `build_erc20.py` with the three subcommands, ABI
     encoding helpers, `decimals()` / `symbol()` / `allowance()` reads,
     `eth_estimateGas` flow, and summary printing.
  2. Create `test_build_erc20.py` with the test surface listed in
     P0/§20.
  3. Update `SKILL.md` (description, inputs, procedure, out-of-scope).
  4. Update `README.md` (file list, manual e2e for hoodi).
  5. Verify: `cd .claude/skills/eth-tx-builder && python3 -m unittest
     test_build_erc20 -v` passes; `python3 -m unittest
     test_build_send_eth -v` still passes unchanged; manual e2e against
     hoodi for all three operations using a real ERC-20.

- **Phase 2 — P1.**
  1. Add `balanceOf` pre-check for `transfer`.
  2. Add `approve` race guard (non-zero → non-zero detection).
  3. Add `sepolia` / `holesky` to the network map (mirror the
     `eth-rpc` Phase 2 plan).
  4. Implement `--summary-only` dry-run.

- **Phase 3 — P2.** As needed: `--revoke` shorthand, polished
  `bytes32` symbol decode, optional `permit` builder if demand
  emerges.

## Risks & Mitigations

- **Risk: operator approves the wrong spender** (DEX router phishing,
  copy-paste error).
  **Mitigation:** loud stderr summary names the spender; unlimited
  approve gated behind `--approve-max` with its own multi-line
  warning. Token symbol shown when available to anchor "is this the
  token I think it is?"

- **Risk: `eth_estimateGas` succeeds but underestimates,
  transaction reverts on-chain.**
  **Mitigation:** +20% buffer and 300_000 cap. The buffer absorbs
  typical inter-block variance; the cap prevents pathological tokens
  from burning massive gas.

- **Risk: `eth_estimateGas` fails (token reverts on simulation:
  paused, blocklisted sender, insufficient balance) and a naive
  fallback would emit a doomed transaction.**
  **Mitigation:** explicit no-fallback policy. Estimate failure stops
  the build. The error message surfaces the underlying node response.

- **Risk: human → base unit conversion rounds incorrectly.**
  **Mitigation:** integer-only string arithmetic, no `float`. Test
  surface includes edge cases (zero, more fractional digits than
  decimals, very small amounts on 18-decimal tokens).

- **Risk: token returns nonstandard `decimals()` shape (reverts,
  returns 0, returns very large value).**
  **Mitigation:** parse defensively, reject suspicious values (>36),
  surface the error and stop. The conservative cap prevents accidental
  blow-out amounts.

- **Risk: `transferFrom` build emitted for an allowance the spender
  doesn't have, transaction reverts on broadcast.**
  **Mitigation:** allowance soft-check with stderr warning. Warning
  does not block, because the multi-step workflow (approve, then
  transferFrom in the same session) is legitimate.

- **Risk: fee-on-transfer / rebasing tokens deliver less than
  requested.**
  **Mitigation:** documented in SKILL.md and the assumptions
  section. The skill emits the requested amount; the delivered amount
  is the token's behaviour. Out of scope to detect.

- **Risk: regression breaks the v1 ETH-send path.**
  **Mitigation:** `build_send_eth.py` and `test_build_send_eth.py`
  are NOT touched. The new helper lives in a separate file. CI runs
  both test files.

- **Risk: scope creep — once ERC-20 lands, every adjacent op
  (permit, transferAndCall, NFT mints, swaps) looks tempting.**
  **Mitigation:** explicit "Out of scope" list; P1/P2 keep the
  temptations named but bounded.
