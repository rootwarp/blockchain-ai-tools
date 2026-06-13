# Wiring eth-signer-mcp into an AI Agent

This guide is for AI-application builders. By the end you will know how an LLM
agent discovers the two tools this server exposes, how to assemble a
**fully-specified** transaction that `sign_transaction` will accept (the server
fetches *nothing* — no nonce, no gas, no fees, no balances), how to handle the
signed result it returns (you broadcast it — the server never does), and how to
surface its errors and approval prompts to a human. The worked example uses the
committed golden-vector payloads so you can reproduce it byte-for-byte.

For the full operator setup (build, flags, transports, keystore lifecycle) see
the [app README](../../README.md). For live captures and golden-vector parity
proof see [the demo walkthrough](../demo.md).

---

## 1. Mental model: this is a signer, not a wallet or a node

eth-signer-mcp is a **strictly-offline signer**. It holds one keystore, decrypts
it for the duration of a single signing call, returns broadcast-ready signed RLP,
and zeroes the key. That is the entire job. It has **no chain state** and makes
**no outbound network calls — by construction** (`internal/signing` imports no
HTTP/RPC client; enforced by `internal/signing/offline_test.go` = ADR-007 and
`depguard` rules in `.golangci.yml` = ADR-008). See [README §1](../../README.md).

Concretely, the server will **never**:

- look up the account **nonce**,
- estimate **gas**,
- read the **base fee** or suggest a priority fee,
- check the account **balance** or whether a tx will succeed,
- **broadcast** the signed transaction (no `eth_sendRawTransaction`).

This means the agent — or a *companion read-only RPC tool* that you wire in
alongside this signer — is responsible for two things this server deliberately
does not do:

1. **Supply every field** of the transaction (nonce, gas, fee fields, etc.),
   fully resolved against the target network, *before* calling
   `sign_transaction`.
2. **Submit the `rawTransaction`** that comes back, via your own RPC path.

A clean agent topology is therefore *two* tools: a read-only chain tool (your
own `eth_getTransactionCount`, `eth_gasPrice`/`eth_feeHistory`,
`eth_estimateGas`, `eth_sendRawTransaction` wrapper) plus this offline signer.
The signer is the only component that ever touches the private key, and it never
touches the network. Keep that boundary in your system prompt and tool wiring.

---

## 2. Discovery: the two tools the model sees

On MCP `initialize` + `tools/list`, the agent receives exactly two tools. These
are the verbatim description strings registered in
`internal/server/server.go` — this is the text your model reasons over:

### `sign_transaction`

> Sign a fully-specified Ethereum transaction (type 0 / legacy or type 2 /
> EIP-1559) with the loaded keystore. Supported types: 0x0 (legacy, EIP-155)
> and 0x2 (EIP-1559). The signed transaction is returned as a hex-encoded RLP
> string (rawTransaction) ready for eth_sendRawTransaction, along with signature
> components and the transaction hash. The result is NOT broadcast — the caller
> is responsible for submission.

### `get_address`

> Return the EIP-55 checksummed Ethereum address of the loaded keystore account.
> This is a read-only operation served from the boot-time keystore snapshot; the
> password file is NOT read and no KDF runs on this path. Safe to call even if
> the password file has been rotated or made unreadable.

**Use `get_address` first, and use it freely.** It takes no arguments (empty
object `{}`), returns `{"address":"<EIP-55 checksummed>"}`, and is served from
the boot-time keystore snapshot — **no password read, no KDF, sub-millisecond**.
It is the cheap read-only call for the agent to learn *which account it is about
to sign for*, and it is safe even if the password file has been rotated or made
unreadable (source: `internal/signing/result.go` `AddressResult`,
`internal/server/server.go`). By contrast, every `sign_transaction` call pays the
full scrypt cost (see [§7](#7-latency-expectations-for-agent-ux)).

The input schema for both tools is inferred from typed Go structs with
`additionalProperties:false`, so **unknown fields are rejected** — do not invent
parameters. The full schema lives in the [tool reference](04-tool-reference.md).

---

## 3. Building a valid `sign_transaction` call

The server validates strictly and signs exactly what you give it. The agent (or
its read-only chain tool) must resolve and supply every field. Use this
checklist before every call.

**Encoding rule for all numeric fields:** every numeric field is a **string**,
either a **decimal** integer (`"1"`, `"21000"`, `"0"` — no leading zeros except
`"0"`) or a **0x-hex** string (`"0x1"`, `"0x5208"`; leading zeros are
normalised). This applies to `chainId`, `nonce`, `value`, `gas`, and the fee
fields (source: `internal/signing/request.go`).

**Required for every transaction** (the inferred-schema required set is `type`,
`chainId`, `nonce`, `value`, `data`, `gas`):

- [ ] **`type`** — `"0x0"`/`"legacy"` (type 0, EIP-155) **or** `"0x2"`/`"eip1559"`
      (type 2). Anything else → `unsupported_type`.
- [ ] **`chainId`** — must match the **target network** you intend to broadcast
      to (e.g. `"1"` mainnet, `"11155111"` Sepolia). **Must not be zero.** Get
      this wrong and you have signed a transaction for the wrong chain.
- [ ] **`nonce`** — a **fresh** nonce for the signing account on the target
      network. The server will not fetch it; resolve it via your read-only RPC
      tool (`eth_getTransactionCount` at `pending`) right before signing.
- [ ] **`value`** — amount in **wei** (`"1000000000000000000"` = 1 ETH). Zero is
      allowed.
- [ ] **`data`** — `0x`-prefixed, even-length hex. `"0x"` means empty calldata.
      Max **256 KiB decoded** (262,144 bytes; 524,290 hex chars incl. `0x`),
      enforced in `validate.go`.
- [ ] **`gas`** — the gas limit. Estimate it with your read-only RPC tool
      (`eth_estimateGas`) and add headroom yourself.

**Optional / conditional fields:**

- [ ] **`to`** — `0x`-prefixed 20-byte recipient. **Omit for contract creation.**
      Mixed-case addresses must pass the **EIP-55 checksum**; all-lowercase and
      all-uppercase are accepted without a checksum check.
- [ ] **Fee fields — pick the set that matches `type`:**
  - **Type 0 / legacy:** supply **`gasPrice`** (wei); omit the EIP-1559 fields.
  - **Type 2 / EIP-1559:** supply **`maxFeePerGas`** and
    **`maxPriorityFeePerGas`** (wei); omit `gasPrice`.
  - Mixing the wrong fee field with a type is an `invalid_input` error.
- [ ] **`accessList`** — must be **empty** in v1; a non-empty list is rejected
      (`invalid_input`). It exists only to satisfy strict schema inference; do
      not populate it.

The full per-field schema, including the V-value semantics and the
checksum/length rules, is in the [tool reference](04-tool-reference.md).

---

## 4. A worked agent turn

This is one complete agent turn signing the **legacy-mainnet golden vector**.
The values below are byte-reproducible against the committed fixture, so you can
diff your client's behaviour against them.

> ⚠️ **Test-only fixture — do not send real funds.** The address below belongs
> to the committed test keystore
> (`apps/eth-signer-mcp/internal/signing/testdata/keystore-light.json`, light
> scrypt N=4096, password file `testdata/password.txt`). It is a low-value
> demo/CI key. In production, point `--keystore` / `--password-file` at your own
> chmod-600 files.

**Step 1 — learn the signing account (`get_address`, no arguments):**

```json
// tools/call → get_address
{ "name": "get_address", "arguments": {} }
```

Result:

```json
{ "address": "0x9858EfFD232B4033E47d90003D41EC34EcaEda94" }
```

The agent now knows it will sign for `0x9858…da94`. (In a real turn this is also
the account whose nonce/balance your read-only chain tool queries.)

**Step 2 — construct the fully-specified payload.** Here the agent (or its chain
tool) has already resolved `nonce`, `gas`, and `gasPrice` against mainnet. This
is a legacy (type 0) transfer of 1 ETH back to the signing account with
`0xdeadbeef` calldata:

```json
// tools/call → sign_transaction
{
  "name": "sign_transaction",
  "arguments": {
    "type": "0x0",
    "chainId": "1",
    "nonce": "0",
    "to": "0x9858EfFD232B4033E47d90003D41EC34EcaEda94",
    "value": "1000000000000000000",
    "data": "0xdeadbeef",
    "gas": "100000",
    "gasPrice": "20000000000"
  }
}
```

**Step 3 — receive the signed result.** All `SignResult` fields are always
present (source: `internal/signing/result.go`):

```json
{
  "rawTransaction": "0xf871808504a817c800830186a0949858effd232b4033e47d90003d41ec34ecaeda94880de0b6b3a764000084deadbeef26a082dc9bb5c5916b7728febf0a7269cef831012cf86eb0f2a4c69aa77ba92755dea073b452e7edc29e44a5f2b6887e90d59aec9ca1b81c858f592362e45a4e9ffcac",
  "signature": {
    "r": "0x82dc9bb5c5916b7728febf0a7269cef831012cf86eb0f2a4c69aa77ba92755de",
    "s": "0x73b452e7edc29e44a5f2b6887e90d59aec9ca1b81c858f592362e45a4e9ffcac",
    "v": "0x26"
  },
  "hash": "0xfa41dd66cc50da67d7e59d1b7277e794cbe69d5b10deb88a9ca2930fae65a239",
  "from": "0x9858EfFD232B4033E47d90003D41EC34EcaEda94"
}
```

Notes for the agent:

- `from` is **recovered from the signature** and equals the keystore address —
  a useful sanity check that the right key signed.
- For legacy/EIP-155, `v` is `chainId*2+35` or `+36` (`0x26` = 38 here); for
  EIP-1559 it is the yParity `0x0` or `0x1`.

**Step 4 — hand off for broadcast (out of scope for this server).** The server's
job ends at the `rawTransaction` string. The agent passes that hex blob to its
**own** read-only/RPC tool, which calls `eth_sendRawTransaction`. eth-signer-mcp
does not broadcast and has no endpoint that will:

```text
agent → your RPC tool: eth_sendRawTransaction("0xf871…ffcac") → tx hash on-chain
```

The on-chain transaction hash returned by `eth_sendRawTransaction` will match the
`hash` field above (`0xfa41dd66…a239`).

For the EIP-1559 (type 2) variant of this turn — same account, `nonce` `"42"`,
`maxFeePerGas`/`maxPriorityFeePerGas` instead of `gasPrice`, yielding
`rawTransaction` `0x02f878…2b99` and `hash` `0x8490c945…a78a` — see
[the demo walkthrough](../demo.md).

---

## 5. Approvals & safety

**The server trusts the MCP client's approval flow.** Per the PRD threat model,
a malicious or prompt-injected agent is an in-scope adversary, and the *primary
gate* is the client's own tool-call approval UI — **the server signs exactly
what it is told and does not validate semantic intent** (it cannot tell a
"drain wallet" transaction from a normal one) (source:
[`plan/prd.md` §Security & Threat Model](../../../../plan/prd.md)).

Practical guidance for agent builders:

- **Surface the high-stakes fields to the human before signing.** When your
  client renders the `sign_transaction` approval prompt, show at minimum
  **`to`**, **`value`** (in both wei and ETH), and **`chainId`**. These are the
  fields a human can sanity-check at a glance and the ones a prompt-injection
  attack would manipulate. Do not bury them.
- **Treat `get_address` as no-approval, `sign_transaction` as always-approval.**
  `get_address` is read-only and cheap; `sign_transaction` mutates value and
  should require explicit human confirmation every time.
- **The `--chain-id` guard is a backstop, not the primary control.** If the
  operator launched the server with `--chain-id <N>`, any request whose
  `chainId` differs is refused with `chain_id_mismatch` before any key material
  is touched. That protects against *wrong-network* signing, but it does not
  protect against a malicious `to`/`value` on the *right* network — that is what
  the human approval is for.

---

## 6. Error handling for agents

Tool errors come back with `IsError: true`; `Content[0]` is a text item whose
value is compact JSON with **exactly two fields**: `{"code":"…","message":"…"}`
(ADR-004; source: `internal/server/handlers.go`, `errors.go`). The agent should
parse `code` and branch on it — do **not** retry blindly. Wire shape:

```json
{"isError":true,"content":[{"type":"text","text":"{\"code\":\"password_error\",\"message\":\"keystore decryption failed; check the password\"}"}]}
```

How an agent should react to each `code`:

| `code` | Cause | Agent reaction |
|--------|-------|----------------|
| `invalid_input` | Missing/malformed field, bad EIP-55 checksum, `chainId=0`, non-empty `accessList`, oversize `data`, or a fee field that doesn't match the `type` | **Fix the field** and re-call. The `message` names the problem. This is a payload bug, not a transient failure. |
| `chain_id_mismatch` | Request `chainId` ≠ the operator's `--chain-id` guard | **Wrong network.** Re-resolve the target chain; align `chainId` or confirm with the human which network was intended. Do not just resend. |
| `unsupported_type` | `type` is not `0x0`/`0x2` | Use **`0x0` (legacy) or `0x2` (EIP-1559)**. Types 1/3/4 are not supported (see [§8](#8-what-not-to-ask-it-to-do)). |
| `password_error` | Password file unreadable or wrong password (keystore MAC failure) | **Operator/config issue — do not retry blindly.** The server stays running; surface to the human to fix the password file, then retry the same payload. |
| `keystore_error` | Keystore missing/malformed/no usable `"address"` | **Operator issue.** Usually fatal at startup; if seen at call time, escalate to the human — retrying the same call will not help. |
| `internal_error` | Recovered panic, sender mismatch, or non-`ErrDecrypt` failure | Unexpected. The underlying `Cause` is logged server-side and **never sent to the caller**. Surface as an opaque failure; do not loop. |

Rule of thumb: only `invalid_input` and `chain_id_mismatch` are *agent-fixable*
by editing the payload. `password_error`/`keystore_error` are *operator* issues,
and `internal_error` is a bug to report. Cross-link your users to
[Troubleshooting](07-troubleshooting.md) for operator-side fixes.

---

## 7. Latency expectations for agent UX

Every `sign_transaction` call decrypts the keystore from scratch — the scrypt
KDF in `keystore.DecryptKey` dominates and the decrypted key is **never cached**
(ADR-010). Design your timeouts and spinners around the keystore's scrypt
parameters (source: [README §7](../../README.md)):

| Keystore type | scrypt N | Latency per `sign_transaction` |
|---------------|----------|--------------------------------|
| Standard (geth default) | 262,144 | ~0.5–1 s |
| Light (`geth account new --lightkdf`) | 4,096 | ~50 ms |
| Weak test-only | 2 | ~1 ms (CI fixtures only) |

Non-KDF signing compute is sub-millisecond, so the table above *is* the
end-to-end cost. Two consequences for agent UX:

- **`get_address` is effectively instant** (no KDF) — call it without a spinner.
- **`sign_transaction` needs a visible "signing…" state** and a tool-call
  timeout comfortably above 1 s (a few seconds) for standard-scrypt keystores.
- **Calls are serialised:** a semaphore of 1 means at most one signing operation
  runs at a time (ADR-006), so concurrent `sign_transaction` calls queue rather
  than parallelise. Do not fan out signing requests expecting parallel latency;
  budget for sequential execution.

For dev loops, point the server at a **light-scrypt** keystore to keep iteration
fast.

---

## 8. What NOT to ask it to do

These are out of scope in v1 (source: [README §1](../../README.md),
[`plan/prd.md`](../../../../plan/prd.md)). Do not put them in the agent's tool
descriptions or system prompt as capabilities — the server will reject or simply
cannot do them:

- **Broadcasting.** No `eth_sendRawTransaction`, no network submission of any
  kind. The agent broadcasts the `rawTransaction` via its own RPC tool.
- **Message signing.** No EIP-191 `personal_sign`, no EIP-712 typed-data
  signing. This signer only signs full transactions.
- **Unsupported tx types.** Types 1 (EIP-2930), 3 (EIP-4844/blob), and 4
  (EIP-7702) are not supported → `unsupported_type`. Use **`0x0`** or **`0x2`**.
- **Wallet management.** No key generation, no HD derivation, no multi-account.
  One instance signs for exactly **one** account; `get_address` returns that one
  address.

If a user asks the agent to "send", "broadcast", or "sign this message," the
agent should recognise those as outside this tool's contract: sign with
`sign_transaction` and hand off, or decline the message-signing request entirely.

---

*See also:* [Tool reference](04-tool-reference.md) (full schema) ·
[Troubleshooting](07-troubleshooting.md) (operator fixes) ·
[app README](../../README.md) · [demo walkthrough](../demo.md) ·
[PRD](../../../../plan/prd.md) · [architecture](../../../../plan/architecture.md).
