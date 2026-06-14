# PRD: eth-tx-builder ERC-20 permit (EIP-2612) sibling helper

## Status / Lifecycle

**Status: Draft — awaiting demand surfacing.**

Implementation of EIP-2612 `permit` is OUT OF SCOPE for
`plan/eth-tx-builder-erc20` Phase 3, per project plan DL-10 ("fresh PRD before
fresh phase plan before code"). ADR-014 (`plan/eth-tx-builder-erc20/
architecture.md`) assessed the go/no-go and concluded: no operator demand has
surfaced yet; the full draft PRD skeleton (this document) is the Phase 3
deliverable so a future session has a reviewable artifact rather than a blank
canvas. If and when demand surfaces naming a concrete router workflow, the
implementation ships under THIS PRD's own phase plan as a sibling helper
`build_erc20_permit.py` — NOT under the `eth-tx-builder-erc20` Phase 3 plan
and NOT inside `build_erc20.py`.

Pointers:
- Project plan DL-10: `plan/eth-tx-builder-erc20/project-plan.md` §Decision Log.
- ADR-014 (go/no-go analysis, Q1–Q5): `plan/eth-tx-builder-erc20/architecture.md`
  (after ADR-013).
- This draft PRD: `plan/eth-tx-builder-erc20-permit/prd.md` (the document you
  are reading).

---

## Overview

EIP-2612 defines a `permit` function that allows an ERC-20 token holder to
authorize a spender via an off-chain signature (EIP-712 typed-data) rather than
an on-chain `approve` transaction. The holder signs a structured message — the
`permit` parameters encoded as EIP-712 typed data — and passes the resulting
`(v, r, s)` signature alongside the standard `approve` parameters. The spender
(typically a DEX router or lending protocol) calls `permit(owner, spender,
value, deadline, v, r, s)` on the token contract, which atomically grants the
allowance and consumes the signature. The token contract verifies the signature
using the EIP-712 domain separator (which encodes the token's name, version,
and chain ID) and the holder's current nonce.

The key UX benefit is **gasless single-transaction approval**: instead of the
conventional two-transaction flow (holder broadcasts `approve`, then
router/spender calls `transferFrom`), the router can call `permit` and then
`transferFrom` in a single transaction. The holder never pays for the `approve`
transaction — the router pays instead. This matters for DEX aggregators, lending
protocols, and router-based swap flows that want to offer a seamless "one click"
UX without asking the user to pre-approve tokens in a separate step.

---

## Problem Statement

Today's ERC-20 approve-then-action workflow has two concrete operator pain
points that EIP-2612 `permit` directly addresses:

**Two-transaction UX.** The standard `approve` + router-action workflow requires
at minimum two on-chain transactions. For a DEX swap, the operator must: (1)
broadcast `approve(router, amount)`, wait for confirmation, then (2) broadcast
the swap. During Phase 3 of this project, the `--revoke` shorthand was added as
a complementary mitigation for revocation after the allowance is no longer
needed — but the root cause (the two-transaction pattern) is unresolved. The
`permit` builder removes the first transaction entirely for tokens that implement
EIP-2612.

**`approve(MAX_UINT256)` security exposure.** A common alternative to the
two-transaction problem is granting unlimited approval (`--approve-max` in this
skill). Phase 1 already gates this behind an explicit flag and a loud stderr
warning, and Phase 3's `--revoke` provides the off-ramp. However, the unlimited
grant remains live until explicitly revoked, creating a window of exposure.
`permit`-based allowances carry a mandatory `deadline` parameter — the allowance
expires automatically, removing the need for a separate revoke step.

---

## Goals

1. **Primary goal:** an operator can build the EIP-712 typed-data structure AND
   the `permit(...)` calldata for any EIP-2612-compliant token in a single CLI
   invocation via `build_erc20_permit.py`, using human-readable amounts and a
   token contract address, with no new Python dependencies beyond those in the
   existing skill.

2. **Compose-into-workflow goal:** the permit output is composable into a
   permit-then-action workflow. The typed-data JSON output feeds an external
   EIP-712 signer (Frame, MetaMask, hardware wallet, `cast wallet sign-typed-data`)
   to produce `(v, r, s)`, which the operator then supplies in a second
   invocation to assemble the final `permit(...)` calldata with the real
   signature.

3. **Verified against a golden vector:** the keccak256 implementation and
   EIP-712 domain separator computation must be validated against a published
   golden vector (USDC `permit` on mainnet is the reference — a known
   `nonces(owner)` value, a known `DOMAIN_SEPARATOR()`, and a known signed
   permit tx verifiable on Etherscan).

4. **Scope boundary:** the builder is a sibling helper `build_erc20_permit.py`,
   NOT a new subcommand inside `build_erc20.py`. It ships under this PRD's own
   phase plan per project plan DL-10 and ADR-014 (Q2).

5. **Stdlib-only (default path):** the keccak256 implementation is hand-written
   (~50–100 lines); no third-party cryptographic dependencies are introduced
   unless the hand-write fails validation against reference vectors (in which
   case the fresh phase plan must explicitly open the dep conversation).

---

## Functional Requirements

### P0 — Must have for v1

**P0.1 — Build `permit` calldata.**

User story: as an operator, I want to run:
```bash
python3 build_erc20_permit.py permit \
  --network mainnet \
  --token 0xA0b86991... \
  --owner 0xHolder... \
  --spender 0xRouter... \
  --amount 100 \
  --deadline 1800000000 \
  --v 27 \
  --r 0xabc... \
  --s 0xdef... \
  --sender 0xHolder...
```
and receive a `TxRequest` JSON for a `permit(owner, spender, value, deadline,
v, r, s)` call ready for `eth-signer-mcp sign_transaction`.

Acceptance-criterion stubs:
- AC-P0.1a: the output `data` field encodes the seven `permit(...)` parameters
  correctly per ERC-20 ABI encoding rules.
- AC-P0.1b: the `to` field is the token contract address; `value` is `"0"`.
- AC-P0.1c: exit 0 on success; exit 1 on any encoding, RPC, or validation error.
- AC-P0.1d: the calldata matches a known golden vector (USDC mainnet permit tx
  on Etherscan, decoded and verified against the hand-built encoding).

**P0.2 — Emit EIP-712 typed-data structure.**

User story: as an operator preparing to sign a permit off-chain, I want the
builder to emit the EIP-712 typed-data JSON object (conforming to `eth_signTypedData_v4`)
alongside the permit calldata, so I can pipe it to my external signer.

Acceptance-criterion stubs:
- AC-P0.2a: the typed-data JSON includes `domain`, `types`, `primaryType`, and
  `message` fields per EIP-712 canonical form.
- AC-P0.2b: `domain` encodes `name`, `version`, `chainId`, and
  `verifyingContract` correctly, using values read live from the token contract
  (`name()`, `version()` if present, `DOMAIN_SEPARATOR()` recompute path).
- AC-P0.2c: `message` encodes `owner`, `spender`, `value`, `nonce` (read live
  from `nonces(owner)`), and `deadline`.
- AC-P0.2d: the typed-data hash (EIP-712 `hashStruct` of the message, combined
  with the domain separator via `keccak256("\x19\x01" + domainSep + structHash)`)
  matches the digest a compliant EIP-712 signer would compute for the same
  parameters. Validated against the USDC mainnet golden vector.

**P0.3 — Live contract reads for domain separator construction.**

User story: as an operator, I do not want to manually look up the token's
`name()`, `version()`, or current nonce — the builder should read them for me.

Acceptance-criterion stubs:
- AC-P0.3a: `nonces(owner)` is read via `eth_call` and included in the
  typed-data `message.nonce` field.
- AC-P0.3b: `name()` is read via `eth_call` and used to construct the EIP-712
  domain. If the call fails, the builder exits 1 with a clear error (domain
  separator computation requires `name()`; it is fatal, not best-effort).
- AC-P0.3c: `version()` is read via `eth_call` if the token exposes it;
  defaults to `"1"` if the call reverts or the token does not implement it (this
  is the EIP-2612 fallback for tokens that omit `version()`).
- AC-P0.3d: `DOMAIN_SEPARATOR()` is optionally read for cross-check. If the
  token exposes it, the builder compares the live value against the recomputed
  domain separator and warns if they differ. Best-effort: does not block the
  build.

**P0.4 — Keccak256 validation against reference vectors.**

User story: as an operator shipping a permit-based workflow to production, I
need to know the hand-written keccak256 implementation is byte-exact.

Acceptance-criterion stubs:
- AC-P0.4a: the test suite includes at minimum three NIST Keccak reference
  vectors (empty input, short ASCII, 200-byte input) where the hand-written
  keccak256 output is compared byte-for-byte against the known hash.
- AC-P0.4b: the EIP-712 domain separator hash for USDC mainnet (known
  `name="USD Coin"`, `version="2"`, `chainId=1`,
  `verifyingContract=0xA0b86991...`) matches the on-chain `DOMAIN_SEPARATOR()`
  value verifiable via Etherscan.
- AC-P0.4c: the full EIP-712 typed-data digest for a known USDC permit (from a
  real mainnet signed permit tx) matches the hand-computed digest.

### P1 — Should have for v1+

**P1.1 — `--deadline-in-hours` shorthand.**

User story: as an operator who does not want to compute Unix timestamps manually,
I want to pass `--deadline-in-hours 24` and have the builder resolve the
deadline to `int(time.time()) + 24*3600`, with the resolved Unix timestamp
printed in the stderr summary.

Acceptance-criterion stubs:
- AC-P1.1a: `--deadline-in-hours <N>` is mutually exclusive with `--deadline
  <unix-timestamp>`; exactly one is required.
- AC-P1.1b: the stderr summary shows both the human-readable expiry ("deadline:
  2026-06-15T12:34:56Z") and the raw Unix timestamp.
- AC-P1.1c: the deadline is validated to be in the future (warn, don't block,
  if the deadline is within 5 minutes of `time.time()`).

**P1.2 — Pretty-printed EIP-712 typed-data summary to stderr.**

User story: as an operator reviewing the permit before signing, I want the
stderr summary to display the typed-data fields in human-readable form so I can
sanity-check them without parsing the raw JSON.

Acceptance-criterion stubs:
- AC-P1.2a: the stderr summary block includes `owner`, `spender`, `value`
  (in human-readable units), `nonce`, and `deadline` (in ISO-8601 UTC).
- AC-P1.2b: the summary includes a note when `version()` was defaulted to
  `"1"` (the token did not implement it).
- AC-P1.2c: the summary includes the computed EIP-712 digest in hex, so the
  operator can cross-check it against the external signer before confirming.

### P2 — Later

**P2.1 — `eth-signer-mcp` EIP-712 integration (Path A from ADR-014 Q4).**

User story: as an operator using `eth-signer-mcp` as the signer, I want a new
`sign_typed_data` MCP tool that accepts the EIP-712 typed-data JSON and returns
the `(v, r, s)` signature, so the permit workflow is end-to-end within the
existing MCP stack without an external signer.

Acceptance-criterion stubs:
- AC-P2.1a: a parallel `eth-signer-mcp` enhancement PRD is drafted before
  this AC is attempted.
- AC-P2.1b: `build_erc20_permit.py` gains a `--sign-via-mcp` mode that
  pipes the typed-data to `eth-signer-mcp sign_typed_data` and receives
  `(v, r, s)` back, then assembles the complete `permit(...)` calldata in a
  single invocation.
- AC-P2.1c: the integration is end-to-end tested against a hoodi EIP-2612
  token with the MCP signer.

**P2.2 — ERC-3009 `transferWithAuthorization` sibling.**

User story: as an operator using USDC on networks where ERC-3009 is the
preferred authorization mechanism, I want a sibling `build_erc20_3009.py`
helper for `transferWithAuthorization(from, to, value, validAfter, validBefore,
nonce, v, r, s)`.

Acceptance-criterion stubs:
- AC-P2.2a: this requires its own PRD (ERC-3009's `transferWithAuthorization`
  has different typed-data schema and nonce semantics from EIP-2612).
- AC-P2.2b: the ERC-3009 sibling is a separate file, not a subcommand of
  `build_erc20_permit.py`.

---

## Out of Scope

- **ERC-3009 `transferWithAuthorization`.** A distinct standard with different
  typed-data schema and authorization semantics (USDC on some chains uses
  ERC-3009 instead of EIP-2612). Ships as its own sibling and PRD if pursued
  (P2.2 above).
- **Non-EIP-2612 `permit` variants.** DAI's older permit (non-standard function
  signature, boolean `allowed` rather than `uint256 amount`), Aave aTokens'
  custom permit. These require per-token custom encoding not covered by the
  EIP-2612 standard; out of scope for v1. The builder validates that the token
  implements EIP-2612 `nonces(address)` and exits 1 if it does not (rather than
  silently producing wrong calldata for a non-compliant token).
- **Signer-side EIP-712 support in `eth-signer-mcp`.** As resolved by ADR-014
  Q4 (Path B for v1): the builder emits the EIP-712 typed-data for an external
  signer; `eth-signer-mcp` EIP-712 integration is a v2 follow-up (P2.1 above).
  Until that ships, the operator uses Frame, MetaMask, or `cast wallet
  sign-typed-data` to produce `(v, r, s)`.
- **EIP-1271 contract signatures.** Smart contract wallets that implement
  `isValidSignature(bytes32, bytes)` require different `ecrecover` logic on the
  verifier side. The permit builder assumes EOA signer (standard `ecrecover`)
  for v1.
- **Gasless relay / meta-transaction submission.** The builder produces the
  calldata; broadcasting (including submitting to a gasless relayer) is owned
  by the `eth-rpc` skill's `broadcast` op or the operator's chosen relay
  service.
- **Permit aggregation / batch.** Building multiple permit signatures in one
  invocation. One permit per invocation, matching the Phase 1 pattern for
  ERC-20 operations.
- **Symbol → address registry.** The caller supplies the token contract
  address, as in `build_erc20.py`.
