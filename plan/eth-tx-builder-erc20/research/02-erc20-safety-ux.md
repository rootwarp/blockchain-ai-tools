> Reconstructed from the research-workflow transcript; figures reconciled against adversarial verification (see [`00-overview.md`](00-overview.md) §3).

# Research: ERC-20 transfer / approve / transferFrom — Safety UX for a Transaction Builder

**Angle:** `erc20-safety-ux` (informs PRD §7, §11, §16, Out-of-Scope)
**Template:** D — Best Practices

## Summary

A transaction *builder* (not a signer) should treat its summary block as the
user's last line of defense. The eth-signer-mcp signer is offline-by-design and
will sign whatever JSON it receives that passes basic format checks — so the
builder's stderr summary is, in practice, the **last human-readable review
surface**. For the eth-tx-builder ERC-20 extension this means:

1. Print a single-screen stderr summary with operation, token
   (symbol + address + decimals), the resolved base-unit amount **alongside**
   the human amount, and the safety-critical counterparties (spender / source
   `from`) each on their own labeled line.
2. Gate `--approve-max` behind a loud multi-line warning naming token and
   spender, exactly as the PRD specifies.
3. Keep allowance soft-checks non-fatal for `transferFrom` — the warn-don't-block
   posture in PRD §11 matches what reputable wallets do.
4. Emit advisory notes for two niche cases: the non-zero → non-zero approve race
   (legacy tokens like USDT will *revert*; frontrunning, while rare, is the
   canonical ERC-20 hazard) and fee-on-transfer / rebasing tokens (the displayed
   amount is not the delivered amount).

The PRD's choices are well-aligned with industry practice. The only material
gaps surfaced by research: (a) the approve-race advisory currently sits in P1 —
it arguably belongs in P0 as a one-line cheap heuristic, because it doubles as a
USDT-style "your tx will revert" pre-flight check; (b) the summary should render
the resolved base-unit amount and decimals on lines adjacent to the human amount
so off-by-N-zeros mistakes pop visually.

## Approach: loud summary + targeted soft-warnings, hard-stop only when calldata would be wrong

**How it works.** The stderr summary block always shows: operation; network +
chainId; token address + symbol (or "(unavailable)") + decimals on the same
line; human amount AND resolved base-unit amount (or "MAX UINT256") on adjacent
lines; counterparty fields named by role ("spender=", "source=", "recipient=",
"signer="). Soft-warnings (stderr, `WARNING:` prefix) fire for `--approve-max`,
a non-zero current allowance when emitting a new non-zero approve, low allowance
on `transferFrom`, and low balance on `transfer` (P1). Hard error-and-stop only
for malformed addresses, malformed amount, `decimals()` parse failure, and
`eth_estimateGas` failure. Symbol failure degrades gracefully
("(symbol unavailable)").

**Why this one.** It matches the PRD's house style — error-and-stop for inputs
that would yield a doomed tx; warn-don't-block for inputs that *might* be
intentional in a multi-step workflow — and it matches what reputable wallets do.
The builder is one layer behind the wallet, so it cannot rely on Blockaid-style
remote reputation, but it *can* always provide deterministic on-chain context
(decimals, symbol, current allowance).

**Trade-offs.** More RPC calls (one per soft-check); a longer summary block;
some risk of "warning fatigue" if every approve fires a non-zero-allowance
advisory.

### Alternatives considered (and why rejected as defaults)

- **Strict mode** — refuse non-zero → non-zero approves without `--force`.
  Justified only for a USDT-heavy user base. Breaks warn-don't-block for what is,
  on modern tokens (USDC, DAI, OZ-derived), a fully legitimate op. EIP-20 only
  says clients "SHOULD" set to zero first — a recommendation, not a requirement.
- **Auto two-tx bundle** — emit `approve(0)` then `approve(new)` automatically.
  Wrong for this builder: scope is one TxRequest per invocation; two-tx flows
  need nonce coordination the PRD explicitly disclaims (Out of Scope: nonce
  queueing / multi-tx coordination). Breaks the "one build = one signable JSON"
  contract.

## Implementation guidelines

### Fields the stderr summary MUST surface

For every op:

- `operation:` `transfer` | `approve` | `transfer-from`
- `network:` `<name>` (chainId `<id>`)
- `token:` `<address>` (`<symbol or "unavailable">`, decimals=`<N>`)
- `amount (human):` `<as typed>`
- `amount (base units):` `<integer>` — **on the line immediately after the
  human amount**, so a wrong-decimals slip shows visually (e.g. `1.5` →
  `1500000` for a 6-decimal token vs. `1500000000000000000` for an 18-decimal
  token is an instant tell)
- `nonce / gas / maxFeePerGas / maxPriorityFeePerGas:` per v1 style

Per op:

- `transfer`: `from (signer) = <sender>` / `to = <to>`
- `approve`: `holder (signer) = <sender>` / `spender = <spender>` — render the
  `spender` line with a prominent label; it is the high-blast-radius field
- `transfer-from`: `source (pulling allowance) = <from>` / `recipient = <to>` /
  `signer (spender) = <sender>` — three distinct roles, three distinct labels,
  exactly as PRD §16 specifies

### Warnings the builder MUST emit (stderr, `WARNING:` prefix)

| Trigger | Wording sketch | PRD coverage |
|---|---|---|
| `--approve-max` always | `WARNING: --approve-max grants UNLIMITED transfer authority on <SYM> (<token>) to spender <spender>. Revoke later with approve(spender, 0).` | PRD §7 |
| non-zero current allowance + new non-zero approve | `WARNING: current allowance to <spender> is <N> (<human>); changing a non-zero allowance to a new non-zero allowance is the classic ERC-20 "approve race" and legacy tokens (USDT, KNC) will REVERT this tx. Set allowance to 0 first if your token requires it.` | PRD P1 §3 — recommend promoting to P0 |
| `transferFrom` allowance below requested | `WARNING: current allowance(holder=<from>, spender=<sender>) is <N>; requested transfer is <M>. This tx will revert unless allowance is increased before broadcast.` | PRD §11 |
| `transfer` balance below requested (P1) | `WARNING: sender balance is <N>; requested transfer is <M>. This tx will revert.` | PRD P1 §2 |
| `decimals()` returns > 36 | hard error (suspicious / overflow risk) | PRD Technical Considerations |

### Disclaimers to print once-per-build to docs/help (not the summary — would be noise)

- Fee-on-transfer tokens: the delivered amount may be lower than the requested
  amount; this builder cannot detect this and does not try.
- Rebasing tokens: balances may change without a `Transfer` event; allowance and
  balance soft-checks are point-in-time only.
- Zero-value transfers are legal under EIP-20 and may be a legitimate intent or
  an address-poisoning artifact.

### What NOT to do

- **Do not silently fall back when `eth_estimateGas` fails.** A 300k cap on a
  doomed tx still burns gas at broadcast. The node's error is more useful than a
  guessed gas limit. (See [`03-gas-estimation.md`](03-gas-estimation.md).)
- **Do not auto-emit a second `approve(0)` tx.** The builder's contract is
  one-build-one-JSON; multi-tx coordination is out of scope.
- **Do not collapse the human amount and base-unit amount onto one line.** Two
  adjacent lines is the cheapest "is this what you meant?" cue.
- **Do not enforce EIP-55 mixed-case in the builder** — the PRD defers this to
  the signer downstream. EIP-55 catches typos with **~99.9753%** reliability
  (the EIP-55 spec's own figure: a 0.0247% false-pass rate), but it does **NOT**
  catch a valid-but-wrong address — which is exactly what address-poisoning
  exploits — and enforcing it would reject perfectly valid lowercase addresses
  pasted from a script.

## Common pitfalls

- **Relying on `decimals()` returning a `uint8`.** Several real tokens return
  `uint256`, and high-decimals tokens (e.g. YAM-V2 at `decimals=24`) are well
  into overflow-risk territory if downstream code uses `int8` arithmetic. *Avoid
  by* reading the low byte (`& 0xff`), rejecting values > 36, and never assuming
  decimals fits in anything narrower than Python int.
- **Blanket-warning on every non-zero → non-zero approve.** Modern tokens (USDC,
  DAI, OpenZeppelin v5) do *not* require `approve(0)` first; the race requires
  an active frontrunner and a contract without its own race protection. Treating
  every overwrite as danger creates warning fatigue. *Avoid by* keeping the
  wording explanatory ("legacy tokens like USDT *will revert*").
- **Trusting `symbol()` as a security anchor.** A malicious token can claim
  `symbol = "USDC"` while implementing arbitrary transfer logic. *Avoid by*
  always showing the address adjacent to the symbol so the operator's eye
  anchors on the address, not the friendly name.
- **Warning on zero-value transfers.** EIP-20 *requires* zero-value transfers to
  be normal transfers that fire the `Transfer` event. Zero-value `transferFrom`
  is used as a vector in address-poisoning scams, but the *builder* user is
  constructing their own tx — refusing to build a zero-value transfer would be
  paternalistic. Allow, do not warn.
- **Forgetting spender-label loudness.** Reputable wallets single out the spender
  field; the builder's summary should give it equal visual weight (its own
  labeled line, not buried in a JSON dump).
- **Confusing the three roles in `transferFrom`.** The signer is the *spender*,
  `--from` is the *source* (the holder whose allowance is pulled), `--to` is the
  *recipient*. Conflating any pair is a bug class worth its own summary section.
- **Assuming `approve(spender, 0)` revokes everywhere.** EIP-2612 / Permit2
  grants are off-chain signature-based; revoking them requires a different path.
  The v1 builder does not emit permits (Out of Scope), but the `--approve-max`
  revoke hint should not over-promise — it covers on-chain allowance state only.

## Real-world examples

- **MetaMask** displays a spending-cap request with an editable cap (it does
  *not* auto-fill the dapp's proposed amount) and a field naming the spender
  contract, which the user is encouraged to verify on a block explorer. The
  local fields (token, cap, spender) remain the primary trust surface even after
  Blockaid-powered remote reputation was added. *(Caveat: exact UI labels are
  not verbatim in official docs — describe the behavior, not the strings.)*
- **Rabby** flags unlimited-approval requests prominently and asks the user to
  set a specific cap; it ships a built-in revoke tool. *(Caveat: the "red color"
  styling is from secondary coverage, not an official doc — describe as
  "unlimited approval flagged.")*
- **Etherscan Token Approvals tool** surfaces Approved Spender, Original
  Allowance, Current Allowance (decremented by spent), token symbol, and total
  at-risk amount — the reference "fields a user needs to audit an approval" set.
- **OpenZeppelin** deprecated `safeApprove` because of the non-zero → non-zero
  gotcha; the documented mitigations are `safeIncreaseAllowance` /
  `safeDecreaseAllowance`, and `forceApprove` was added for USDT-style tokens
  that revert on overwrite.
- **EIP-20 itself**: clients "SHOULD make sure to create user interfaces in such
  a way that they set the allowance first to `0` before setting it to another
  value for the same spender," and "Transfers of 0 values MUST be treated as
  normal transfers." The "SHOULD" is what makes a strict-refuse default
  unjustified.
- **Trail of Bits token-integration checklist** flags fee-on-transfer /
  deflationary tokens as a manual-review concern: transfer/transferFrom should
  not take a fee.
- **`d-xo/weird-erc20`** catalogs USDT/KNC (revert on non-zero → non-zero
  approve), STA/PAXG (fee on transfer), Ampleforth-style rebasing, USDC/Gemini
  USD with 2–6 decimals, YAM-V2 with 24 decimals — all reasons the
  "displayed = delivered" assumption is fragile and worth a disclaimer.
- **Unlimited-allowance exploits** (Bancor 2020, Furucombo 2021, UniCats) show
  that allowance-related drains are not theoretical — unlimited approvals expose
  the entire token-holding wallet, not just the deposited position.
- **Address-poisoning losses.** Zero-value / dust / counterfeit-token
  poisoning relies on the EIP-20 rule that 0-value transfers always succeed and
  emit `Transfer`. Adversarial verification puts the loss at **~$90M confirmed,
  up to ~$144M potential** across *all* poisoning variants (zero-value, dust,
  counterfeit-token) — not from zero-value `transferFrom` alone. *(Caveat: the
  Coinbase zero-transfer blog source 403'd, but the mechanism — `transferFrom`
  does not check balance/approval when amount is 0 — is independently derivable
  from EIP-20 spec text.)*
- **Approval-exploit losses.** Use Revoke.cash's documented **"10 exploits,
  >$80M in 2024."** (An earlier ">$200M" figure could not be verified at the
  cited URL and is dropped.)

## Critique of the PRD's specific decisions

- **`--approve-max` gating (§7) — Confirmed, with a wording refinement.** The
  design (mutual exclusion with `--amount`, mandatory multi-line stderr warning
  naming token + symbol + spender, plus a revoke hint) matches industry best
  practice. Refinement: the revoke hint should not promise that
  `approve(spender, 0)` revokes *all* future grants — it only revokes on-chain
  allowance; a held permit signature remains valid until its deadline. One
  sentence acknowledging this is enough.
- **Allowance soft-check on `transferFrom` (§11) — Confirmed as designed.**
  Warn-don't-block is correct because the PRD supports the multi-step
  approve → transferFrom-in-one-session workflow. Non-fatal on RPC error is also
  correct (some tokens reject the allowance call shape).
- **Approve race-check (P1 §3) — Recommend promoting to P0.** It doubles as a
  "your tx will revert" pre-flight check for USDT-class legacy tokens, and it's
  one extra `allowance(sender, spender)` call — identical in shape to the
  `transferFrom` soft-check the PRD already commits to. If P0 isn't appropriate,
  at minimum land it in Phase 2 alongside the `transferFrom` allowance check.
- **Decimals cap at 36 — Confirmed.** Standard is 0–18, exotic is 24, anything
  higher is suspicious; a 200-decimals token would overflow before calldata is
  even emitted.
- **Symbol best-effort with bytes32 fallback (§10) — Confirmed.** Degrading to
  "(unavailable)" is correct — symbol failure is not a security signal; security
  comes from the *address* line being adjacent.
- **Summary on stderr, JSON on stdout (§16) — Confirmed.** Operators piping into
  a signer want clean JSON; the summary is for humans. Confirmation surfaces
  should be scannable summaries, not byte dumps.
- **Fee-on-transfer / rebasing out of scope — Confirmed.** Generic detection is
  impossible without an `eth_call` pre/post-balance simulation the builder does
  not do; the SKILL.md disclaimer is the right and only practical mitigation.

## Assumptions

- The builder cannot assume the operator has a hardware wallet or wallet UI at
  signing time — the offline signer signs whatever passes format checks, so the
  stderr summary is the last human-readable review surface. This raises the bar
  for summary information density vs. a builder that runs in front of MetaMask.
- The operator base skews toward mainnet operational use (the secondary "testnet
  developer" persona tolerates noisier warnings), justifying erring toward more
  warnings, not fewer.
- The PRD's "warn don't block" posture is the right default; do not propose
  flipping to strict-refuse for the approve race.
- Promoting the approve-race soft-check from P1 to P0 (one extra
  `allowance(sender, spender)` call) is cheap enough to justify in Phase 1, but
  the PRD owner may keep it in P1 for scope reasons — both are defensible.
- Fee-on-transfer / rebasing detection is genuinely out of scope: a robust check
  needs `eth_call` pre/post-balance simulation. The SKILL.md disclaimer is the
  only practical mitigation at the builder layer.
- EIP-2612 permits and Permit2 are out of scope for v1; noted only so the
  `--approve-max` revoke hint is scoped to on-chain allowances and does not
  over-promise.
- The research does NOT recommend the builder enforce EIP-55 checksums — the PRD
  already defers checksum enforcement to the signer downstream.
- Loss figures for poisoning and approval exploits come from aggregated chain
  analysis (corrected against adversarial verification, §3); treat as
  directional, not precise.
- "Render base-unit amount on the line immediately after the human amount" is
  opinion informed by general crypto-UX guidance plus the implicit MetaMask /
  Rabby pattern, not a single citable best-practice document.

## Sources

1. [EIP-20: Token Standard](https://eips.ethereum.org/EIPS/eip-20) — `approve(0)` SHOULD recommendation; 0-value transfers MUST be normal.
2. [Fee-on-transfer & Rebase Tokens — ERC-20 Security Bug You Need to Know](https://medium.com/@0xnolo/fee-on-transfer-rebase-tokens-an-erc-20-security-bug-you-need-to-know-f4e5badea1ee) — 0xnolo, Medium.
3. [Uniswap V2 common errors / fee-on-transfer K-errors](https://docs.uniswap.org/protocol/V2/reference/smart-contracts/common-errors) — Uniswap docs.
4. [OpenZeppelin Contracts: SafeERC20.sol (v4.9.6)](https://github.com/OpenZeppelin/openzeppelin-contracts/blob/v4.9.6/contracts/token/ERC20/utils/SafeERC20.sol) — `safeApprove` (value == 0) || (allowance == 0) enforcement.
5. [OpenZeppelin ERC20 API docs](https://docs.openzeppelin.com/contracts/4.x/api/token/erc20) — `safeApprove` deprecation; `increaseAllowance`/`decreaseAllowance`; `forceApprove`.
6. [Zero-Transfer Phishing — Part 1: Attack Analysis](https://www.coinbase.com/blog/zero-transfer-phishing-part-1-attack-analysis) — Coinbase. *(Source 403'd in verification; mechanism derivable from EIP-20.)*
7. [SWC-114: Transaction Order Dependence (approve race)](http://swcregistry.io/docs/SWC-114/) — Smart Contract Weakness Classification. *(Registry URL 403s; canonical numeric example is 1000→500. Structural claim holds.)*
8. [Etherscan Information Center: Token Approvals](https://info.etherscan.com/tokenapprovals/) — fields displayed in approval audit.
9. [USDT requires approve(0) before changing approval (Code4rena finding)](https://github.com/code-423n4/2022-07-axelar-findings/issues/114) — Tether revert behavior on non-zero → non-zero approve.
10. [How to customize token approvals with a spending cap](https://support.metamask.io/configure/tokens/how-to-customize-token-approvals-with-a-spending-cap/) — MetaMask. *(Describe behavior; exact labels not verbatim in docs.)*
11. [d-xo/weird-erc20](https://github.com/d-xo/weird-erc20) — catalog of fee-on-transfer, rebasing, approve-race, bytes32-symbol, and exotic-decimals tokens.
12. [Trail of Bits Token Integration Checklist](https://secure-contracts.com/development-guidelines/token_integration.html) — fee-on-transfer flag; decimals must be uint8.
13. [Crypto UX Handbook — Sending](https://www.cryptouxhandbook.com/sending) / [OneKey: Wallet Confirmation Message](https://onekey.so/blog/ecosystem/whats-a-wallet-confirmation-message/) — confirmation surfaces should be human-readable summaries.
14. [Token Integration Checklist (ethereum.org)](https://ethereum.org/developers/tutorials/token-integration-checklist/) — decimals-as-uint256 footgun; ERC-20 race flagged.
15. [Etherscan: What is Address Poisoning?](https://info.etherscan.com/what-is-address-poisoning/) — zero-value transfers seed lookalike addresses.
16. [Unlimited ERC20 Allowances Considered Harmful](https://kalis.me/unlimited-erc20-allowances/) — Rosco Kalis (revoke.cash). Bancor 2020, Furucombo 2021, UniCats.
17. [What Are EIP2612 Permit Signatures?](https://revoke.cash/learn/approvals/what-are-eip2612-permit-signatures) — Revoke.cash. Off-chain permits ≠ `approve(0)`. *(Approval-loss figure corrected to "10 exploits, >$80M in 2024", §3.)*
18. [Examining Permit Signatures (off-chain phishing)](https://slowmist.medium.com/examining-permit-signatures-is-phishing-of-tokens-possible-via-off-chain-signatures-bfb5723a5e9) — SlowMist.
19. [EIP-55: Mixed-case checksum address encoding](https://blog.btdt.dev/posts/web3/eip-55---should-we-care/) — typo catch; does not catch valid-but-wrong addresses. *(Reliability corrected to ~99.9753% per EIP-55 spec, §3.)*
20. [Characterizing Ethereum Address Poisoning Attack (CCS 2024)](https://www.sigsac.org/ccs/CCS2024/assets/pfaubmkaccs2024submissions/new/p986-guan.pdf) — Guan et al. *(Loss corrected to ~$90M confirmed / ~$144M potential across all poisoning variants, §3.)*
21. [Rabby token-approval UX (secondary coverage)](https://ashelectricalsystem.ca/why-managing-token-approvals-and-gas-fees-with-rabby-wallet-is-a-game-changer-for-defi-users/) — unlimited-approval flagging + built-in revoke. *(Secondary; describe behavior, not styling.)*
22. [Tradeoff: Unlimited Approval in ERC20](https://blocksecteam.medium.com/unlimited-approval-in-erc20-convenience-or-security-1c8dce421ed7) — BlockSec. ~10% of supply at risk under unlimited approvals.
23. [MetaMask Security Alerts by Blockaid](https://metamask.io/news/metamask-security-alerts-by-blockaid-the-new-normal-for-a-safer-transaction) — remote reputation layer; ~$1.15M Ledger Connect Kit protection.
