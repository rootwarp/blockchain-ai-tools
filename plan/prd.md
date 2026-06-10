# PRD: Ethereum Signer MCP Server (`eth-signer-mcp`)

## Overview

`eth-signer-mcp` is a local Model Context Protocol (MCP) server that signs
Ethereum transactions using a locally-stored Web3 Secret Storage keystore. It
exposes its functionality as MCP tools so that an AI agent (or any MCP client)
can request a signature over a fully-specified transaction and receive a
broadcast-ready signed transaction back. The server is strictly offline: it
performs no network calls and never broadcasts.

This is the first app in the `blockchain-ai-tools` monorepo and will live at
`apps/eth-signer-mcp/` as Go module
`github.com/rootwarp/blockchain-ai-tools/apps/eth-signer-mcp` (Go 1.26).

## Problem Statement

Developers building agentic workflows that touch Ethereum need a safe,
local-only way to let an AI agent produce signed transactions without
embedding private keys into the agent runtime, the MCP client, or arbitrary
tool code. Today, the practical options are:

- Paste a hex private key into a tool / env var (unacceptable security).
- Have the agent shell out to `cast wallet sign-tx` (works, but no structured
  contract, no MCP integration, awkward password handling).
- Use a hosted signer / KMS (overkill for local development, adds
  infrastructure and network exposure).

What is missing is a small, auditable, local MCP server that:

1. Loads a standard go-ethereum keystore file using a password file (no inline
   secrets).
2. Accepts a fully-formed transaction in JSON over MCP.
3. Returns a broadcast-ready RLP-encoded signed transaction plus the raw
   signature components.
4. Has a tightly bounded scope: signs what it is told to sign, makes no
   network calls, never broadcasts.

This unlocks AI-agent workflows that can prepare, review, and (via a separate
broadcaster) submit Ethereum transactions, while keeping key material isolated
to a single small process the developer controls.

## Goals

- Provide a single MCP tool, `sign_transaction`, that turns a JSON transaction
  spec into a broadcast-ready signed transaction.
- Support **EIP-1559 (type 2)** and **legacy (type 0)** transactions.
- Be **strictly offline** — no RPC, no network egress of any kind.
- Be **safe by default** — secrets live in files referenced by CLI flags, are
  decrypted only at signing time, and are wiped from memory (best-effort
  zeroing, see P0-SEC-1/2) immediately after use.
- Ship as a small, auditable Go binary that drops into the existing monorepo
  conventions (`make new-app` style).

## Non-Goals (v1)

- Not a wallet manager. No HD derivation, no mnemonic import, no key
  generation, no multi-account selection, no keystore directories.
- Not a transaction builder. The server does not fetch nonce, estimate gas,
  pick fees, resolve ENS, or fill in any missing field. The caller provides a
  fully-specified transaction.
- Not a broadcaster. The server never calls `eth_sendRawTransaction` or any
  other RPC method.
- Not a message signer. EIP-191 `personal_sign` and EIP-712 typed-data
  signing are explicitly out of scope for v1 (planned as P2).
- Not a hosted / network-exposed service. The HTTP transport is intended for
  local automation only and binds to `127.0.0.1` by default.

## Success Metrics

Functional / correctness:

- For a curated set of known test vectors (legacy + EIP-1559), the
  RLP-encoded output is **byte-identical** to a reference signer (e.g.
  `cast wallet sign-tx` and/or ethers.js v6) when given the same inputs.
- Signatures verify against the address derived from the loaded keystore
  (recovered sender == keystore address) for every signed transaction.
- The returned RLP decodes cleanly via `core/types.Transaction.UnmarshalBinary`
  and round-trips back to the same hash.
- Test coverage includes both transaction types and at least one mainnet-style
  chainId and one non-mainnet chainId.

Operational:

- Cold-start to "ready to accept MCP calls" in under 200 ms on a developer
  laptop.
- Signing computation excluding the keystore KDF is sub-millisecond.
  End-to-end signing latency is dominated by the keystore's scrypt parameters
  and is paid on EVERY call because no decrypted key material is ever cached:
  expect ~0.5–1 s for standard-scrypt keystores (geth default, N=262144) and
  ~50 ms for light-scrypt keystores (N=4096). The acceptance benchmark uses
  both parameter sets and asserts the non-KDF overhead (total minus KDF time)
  stays under 10 ms.
- Zero secrets in logs at any log level (verified by test that scans log
  output for the password / private key bytes and known keystore fragments).

Adoption (informal, v1):

- The server can be launched from a Claude Desktop / MCP-client config and
  produce a verified signed transaction end-to-end in a single session,
  without code changes to the client.

## Target Users

Primary persona: **Local agent developer.** A backend / blockchain engineer
who runs an AI agent (e.g. Claude Desktop, an in-house MCP client, or a CLI
agent) on their own workstation and wants the agent to be able to sign
transactions against a development or low-value account, with the private key
material under their direct control on disk.

Secondary persona: **Local automation.** A developer wiring this server into a
local automation pipeline (a script, a `make` target, a local job runner) via
the HTTP transport, on the same host. Any exposure beyond `localhost` is
explicitly out of the supported threat model for v1.

Out-of-scope persona: **Production / multi-tenant signer.** This is not a KMS,
not a custodial signer, and not a hosted service. Users with those needs
should use a real KMS / HSM / remote signer.

## User Stories

- As an agent developer, I want to launch `eth-signer-mcp` from my MCP client
  config with `--keystore` and `--password-file` so my agent can sign
  transactions without seeing the private key.
- As an agent developer, I want to ask the server for the loaded account's
  address (`get_address`) so I can confirm in the agent UI which account will
  sign before I approve the tool call.
- As an agent developer, I want to send a fully-specified EIP-1559
  transaction JSON and get back a hex RLP I can copy into a broadcaster, so my
  agent never has to handle the private key directly.
- As an agent developer, I want the server to refuse signing if the JSON's
  `chainId` does not match a `--chain-id` guard I configured at launch, so a
  prompt-injected mistake cannot accidentally sign a mainnet transaction
  against a testnet-intended setup.
- As a security-conscious operator, I want the server to refuse to start (or
  loudly warn) if the keystore or password file is world- or group-readable,
  so I cannot misconfigure it silently.
- As an automation author, I want to run the same server over HTTP on
  `127.0.0.1` with a bearer token so a local script can call it without
  spawning a subprocess per request.

## Functional Requirements

### Must Have (P0)

**CLI (`urfave/cli`)**

- P0-CLI-1: `--keystore <path>` (required) — path to a Web3 Secret Storage
  JSON keystore file. Single account.
- P0-CLI-2: `--password-file <path>` (required) — path to a file containing
  the keystore password. Trailing newline is stripped. Password is never
  accepted inline as a flag.
- P0-CLI-3: Transport selection: stdio is the default. `--http` enables the
  Streamable HTTP transport. `--http-addr <host:port>` configures the bind address
  (default `127.0.0.1:0` — ephemeral port, printed on startup).
- P0-CLI-4: `--http-auth-token-file <path>` — required when `--http` is set;
  contents are used as the expected bearer token. No token, no HTTP.
- P0-CLI-5: Optional `--chain-id <uint64>` guard. When set, any signing
  request whose JSON `chainId` differs is rejected before any key material is
  touched.
- P0-CLI-6: `--help` / `--version` surfaced via `urfave/cli` defaults.

**MCP server**

- P0-MCP-1: Built on `github.com/modelcontextprotocol/go-sdk` (official Go
  SDK).
- P0-MCP-2: Supports the **stdio** transport (default) and the **Streamable
  HTTP** transport (when `--http` is set). Both transports expose the same tools and
  the same JSON schemas.
- P0-MCP-3: Exposes the MCP tool `sign_transaction` with a strict JSON schema
  for its input and output (see "Input / Output Contract" below).
- P0-MCP-4: Errors are returned as structured MCP tool errors with a short,
  non-sensitive message; secrets are never included in error payloads.

**Signing**

- P0-SIGN-1: Supports **legacy (type 0)** transactions with EIP-155 replay
  protection (`v = chainId * 2 + 35 / 36`).
- P0-SIGN-2: Supports **EIP-1559 (type 2)** transactions.
- P0-SIGN-3: Uses `github.com/ethereum/go-ethereum` `crypto`,
  `accounts/keystore`, and `core/types` packages as the signing primitive.
- P0-SIGN-4: Returns the **RLP-encoded signed raw transaction** (`0x`-prefixed
  hex) **and** the raw signature components `{r, s, v}` (all
  `0x`-prefixed hex). For type 2 transactions, `v` is the `yParity` value
  (0 or 1); for legacy, `v` is the EIP-155 value.
- P0-SIGN-5: Strictly offline. No outbound network calls. No nonce fetching.
  No gas estimation. No broadcasting.
- P0-SIGN-6: Caller supplies all transaction fields. Server validates that
  required fields for the chosen type are present; missing or
  type-inappropriate fields cause a structured error.

**Security**

- P0-SEC-1: The keystore password is read from the password file at the moment
  of signing, used to decrypt the key, and the password bytes are zeroed
  best-effort immediately after use (deferred zeroing + `runtime.KeepAlive`,
  including on panic paths).
- P0-SEC-2: The decrypted private key lives in memory only for the duration of
  one signing operation and is zeroed best-effort (`crypto/ecdsa` field + any
  intermediate buffers; deferred zeroing + `runtime.KeepAlive`, including on
  panic paths) immediately after the signature is produced. Go's runtime may
  retain transient copies (GC moves, stack copies); this limitation is
  accepted — see architecture ADR-009.
- P0-SEC-3: No secret value (password, decrypted key, keystore JSON contents)
  is ever logged at any log level. A test enforces this.
- P0-SEC-4: On startup, the server checks file permissions on the keystore
  and password files. If either is world-readable or group-readable, the
  server logs a clear warning; a `--strict-perms` flag (P1) upgrades the
  warning to a hard refusal.
- P0-SEC-5: HTTP transport binds to `127.0.0.1` by default and requires a
  bearer auth token (from `--http-auth-token-file`). Requests without a
  matching `Authorization: Bearer <token>` header are rejected with 401
  before any signing logic runs.
- P0-SEC-6: No telemetry, no analytics, no auto-update, no outbound
  connections of any kind from the server process.

### Should Have (P1)

- P1-MCP-1: MCP tool `get_address` — returns the checksummed Ethereum address
  derived from the loaded keystore. Does not require the password (uses the
  keystore's stored address field, cross-checked at first signing).
- P1-OUT-1: In addition to RLP + `{r, s, v}`, the `sign_transaction` response
  includes the transaction hash and the sender address derived from the
  keystore, so callers can confirm the signer without recovering it
  themselves.
- P1-SEC-1: `--strict-perms` flag that turns the permission warning from
  P0-SEC-4 into a startup refusal.
- P1-OBS-1: Structured logging (JSON) with configurable level
  (`--log-level`). All redaction rules from P0-SEC-3 apply.
- P1-OBS-2: Every successful signing emits one info-level structured log line
  with `request_id`, `tx_hash`, `chain_id`, `nonce`; tx body / calldata /
  `to` / `value` are never logged (calldata may be operator-sensitive). The
  file-based audit log remains P2 (P2-OBS-1).
- P1-CLI-1: `--version` reports build info (commit, build date, Go version).

### Nice to Have (P2)

- P2-SIGN-1: EIP-191 `personal_sign` message signing as a separate
  `sign_message` MCP tool.
- P2-SIGN-2: EIP-712 typed-data signing as a separate `sign_typed_data` MCP
  tool.
- P2-SIGN-3: EIP-2930 (type 1, access list) support.
- P2-SIGN-4: EIP-4844 (type 3, blob) support.
- P2-SIGN-5: EIP-7702 (type 4, setCode) transactions.
- P2-UX-1: Optional in-server human confirmation step (e.g. a terminal prompt
  on stdio or a local web confirm UI) before any signature is produced.
- P2-KEY-1: Support a keystore directory with multiple accounts and a
  `list_accounts` MCP tool, with explicit account selection per request.
- P2-OBS-1: Audit log of signed transaction hashes (no secrets) to a
  configurable file.

## Non-Functional Requirements

- **Platform:** Linux and macOS, amd64 and arm64. Windows is best-effort
  (file-permission semantics differ).
- **Footprint:** Single static Go binary. No runtime dependencies beyond the
  Go standard library and `go-ethereum` / `modelcontextprotocol/go-sdk`
  transitive deps.
- **Performance:** Cold start under 200 ms. Signing computation excluding the
  keystore KDF is sub-millisecond; end-to-end latency is dominated by the
  keystore's scrypt parameters and is paid on every call because no decrypted
  key material is ever cached (~0.5–1 s standard-scrypt, ~50 ms light-scrypt).
  The acceptance benchmark asserts non-KDF overhead (total minus KDF time)
  stays under 10 ms.
- **Resource bounds:** Keystore decrypts are serialized via a semaphore of 1
  (each standard-scrypt decrypt costs ~256 MiB), and the request context is
  checked before scrypt starts. HTTP request bodies are capped at 1 MiB via
  `http.MaxBytesHandler`; the `data` field is capped at 256 KiB of bytes
  (512 KiB hex chars) in schema validation.
- **Reliability:** Server never panics on malformed input; all input errors
  are returned as structured MCP errors. A panic in the signing path must
  result in immediate zeroing of any in-flight key material before the
  process exits.
- **Security posture:** See "Security & Threat Model" below.
- **Logs:** No secrets at any level. Default level `info`. Stderr by default.

## Input / Output Contract

### MCP tool: `sign_transaction`

Input JSON schema (informal). All numeric fields are accepted as either
decimal strings or `0x`-prefixed hex strings. Addresses are
`0x`-prefixed hex (EIP-55 checksum validated when mixed-case input is given;
normalized to checksummed form on output).
`data` is `0x`-prefixed hex (empty `"0x"` is allowed).

Common fields:

- `type` (string, required): `"0x0"` / `"legacy"` for legacy, `"0x2"` /
  `"eip1559"` for EIP-1559.
- `chainId` (string, required).
- `nonce` (string, required).
- `to` (string, optional — omit for contract creation).
- `value` (string, required; `"0x0"` allowed).
- `data` (string, required; `"0x"` allowed).
- `gas` (string, required; gas limit).

Legacy-only:

- `gasPrice` (string, required).

EIP-1559-only:

- `maxFeePerGas` (string, required).
- `maxPriorityFeePerGas` (string, required).
- `accessList` (array, optional — if provided, must be empty in v1; non-empty
  is rejected as out of scope).

Validation rules:

- All required fields for the chosen `type` must be present. Unknown fields
  are rejected (strict schema).
- `chainId` must equal `--chain-id` if that flag is set, otherwise it is
  accepted as-is.
- Fields inappropriate to the type (e.g. `gasPrice` on a type-2 tx, or
  `maxFeePerGas` on a legacy tx) are rejected.
- A mixed-case `to` address that fails EIP-55 checksum validation is rejected
  with `invalid_input`; all-lowercase / all-uppercase addresses are accepted
  checksum-agnostic.
- `chainId` of 0 is rejected with `invalid_input` — no replay-unprotected
  signatures.
- `data` longer than 256 KiB of bytes (512 KiB hex chars) is rejected with
  `invalid_input`.

#### Example input — legacy (type 0)

```json
{
  "type": "0x0",
  "chainId": "0x1",
  "nonce": "0x9",
  "to": "0x3535353535353535353535353535353535353535",
  "value": "0xde0b6b3a7640000",
  "data": "0x",
  "gas": "0x5208",
  "gasPrice": "0x4a817c800"
}
```

#### Example input — EIP-1559 (type 2)

```json
{
  "type": "0x2",
  "chainId": "0x1",
  "nonce": "0x9",
  "to": "0x3535353535353535353535353535353535353535",
  "value": "0xde0b6b3a7640000",
  "data": "0x",
  "gas": "0x5208",
  "maxFeePerGas": "0x77359400",
  "maxPriorityFeePerGas": "0x3b9aca00"
}
```

#### Output (P0 + P1)

P0 fields are always present; P1 fields are included once P1-OUT-1 lands.

```json
{
  "rawTransaction": "0x02f86c0109843b9aca0084773594008252089435353535353535353535353535353535353535350880c001a0...a0...",
  "signature": {
    "r": "0x...",
    "s": "0x...",
    "v": "0x1"
  },
  "hash": "0x...",
  "from": "0xAbCd..."
}
```

Notes:

- `rawTransaction` is broadcast-ready: it can be passed directly to
  `eth_sendRawTransaction` by some other tool.
- For EIP-1559, `signature.v` is the `yParity` (`"0x0"` or `"0x1"`). For
  legacy with EIP-155, `signature.v` is the EIP-155 value
  (`chainId * 2 + 35 / 36`).
- `hash` (P1) is the canonical transaction hash of the signed transaction.
- `from` (P1) is the EIP-55-checksummed address derived from the keystore at
  load time and cross-checked against the recovered sender of every signed
  transaction.

### MCP tool: `get_address` (P1)

- Input: none.
- Output: `{ "address": "0xAbCd..." }` (EIP-55 checksummed).

### Error contract

All errors are returned as MCP tool errors with a stable `code` string and a
short human-readable `message`. Defined codes for v1:

- `invalid_input` — JSON schema / field validation failure.
- `unsupported_type` — `type` field is not `0x0`/`legacy` or `0x2`/`eip1559`
  (types 1, 3, and 4 are explicitly out of scope for v1).
- `chain_id_mismatch` — `--chain-id` guard rejected the request.
- `keystore_error` — keystore file unreadable / malformed.
- `password_error` — password file unreadable / decryption failed.
- `internal_error` — anything else. Message never includes secret material.

Wire encoding: tool errors are returned with `IsError: true` and `Content[0]`
a TextContent whose text is a compact JSON object
`{"code":"<stable_code>","message":"<short non-sensitive message>"}`. Both
`code` and `message` cross the wire; internal causes are logs-only and never
serialized into the response.

## Security & Threat Model

### Assets

- The keystore JSON file (encrypted private key at rest).
- The password file (plaintext password at rest, protected by filesystem
  permissions).
- The decrypted ECDSA private key (in memory, briefly, only during a signing
  operation).

### In-scope adversaries

- A malicious or prompt-injected AI agent / MCP client that crafts arbitrary
  `sign_transaction` requests. Mitigation: the client's own approval flow is
  the primary gate (the server trusts the MCP client to obtain user
  approval); the `--chain-id` guard is a backstop against
  sign-on-the-wrong-network mistakes; the server only signs what it is told,
  but it does not validate semantic intent (it cannot tell a "drain wallet"
  tx from a normal one).
- A local non-root process on the same host attempting to read the keystore
  or password file via filesystem permissions. Mitigation: P0-SEC-4 permission
  checks; documented guidance to chmod 600.
- A local process attempting to talk to the HTTP transport without
  authorization. Mitigation: bind to `127.0.0.1`, require a bearer token from
  a file (P0-SEC-5).

### Out-of-scope adversaries (v1)

- Root / kernel-level attackers on the same host. (No mitigation possible at
  this layer; out of scope.)
- Side-channel attacks against scrypt or ECDSA. (Trusting go-ethereum's
  implementations.)
- Attackers with network access to a non-localhost HTTP bind. (Operator's
  responsibility; documented as unsupported.)

### Hardening rules (enforced)

- Password file is read, used, and zeroed within a single function scope
  (best-effort: deferred zeroing + `runtime.KeepAlive`, including on panic
  paths; the Go runtime may retain transient copies — accepted per ADR-009).
- Decrypted key is zeroed immediately after each signing operation (same
  best-effort caveat).
- No secret is ever included in a log line, an error message, or an MCP
  response payload.
- A unit test scans captured log output for known secret fragments — the raw
  sentinel and its derived encodings (hex, base64, decimal) — to enforce the
  no-secret-logging rule.
- The server makes no outbound network calls. This is structurally enforced
  by not importing any HTTP/RPC client packages in the signing path; a test
  asserts the absence of `net/http` clients (only the server side) in the
  signing module.

## UX / Design Notes

- The server is a non-interactive process by default. A developer wires it
  into their MCP client config (stdio) or starts it as a local daemon (HTTP).
- Startup log lines (info level) state: which transport is active, the loaded
  account's address, whether `--chain-id` is enforced, and any permission
  warnings. They never state the password file path's contents or any
  keystore field beyond the address.
- All tool input/output schemas are advertised via MCP's standard tool
  description mechanism so MCP clients can render argument forms.

## Out of Scope (v1)

Explicitly NOT in v1:

- EIP-191 `personal_sign` and EIP-712 typed-data signing.
- EIP-2930 (type 1), EIP-4844 (type 3), and EIP-7702 (type 4) transactions.
- HD wallet derivation (BIP-32 / BIP-44).
- Mnemonic / seed phrase import.
- Hardware wallets (Ledger, Trezor, etc.).
- Keystore directories with multiple accounts; multi-account selection.
- Broadcasting transactions (no `eth_sendRawTransaction`, no RPC client).
- Gas estimation, nonce fetching, fee suggestion — any network round trip.
- ENS resolution.
- Any non-EVM chain.
- Hosted / remote / multi-tenant deployment.

## Assumptions / Open Questions

These were chosen as sensible defaults during PRD drafting. The user can
correct any of these at the planning gate before implementation starts.

- **Repo placement & name:** App lives at `apps/eth-signer-mcp/`, Go module
  `github.com/rootwarp/blockchain-ai-tools/apps/eth-signer-mcp`. Rename if
  desired.
- **chainId source:** Required in the input JSON. Optional `--chain-id` CLI
  flag acts as a guard: if set and the JSON's `chainId` differs, the request
  is rejected before any key material is touched. Default behavior trusts the
  JSON's `chainId`.
- **CLI flags (urfave/cli):**
  - `--keystore <path>` (required).
  - `--password-file <path>` (required).
  - `--http` (bool, default false) and `--http-addr <host:port>` (default
    `127.0.0.1:0`).
  - `--http-auth-token-file <path>` (required when `--http` is set).
  - `--chain-id <uint64>` (optional guard).
  - `--strict-perms` (P1, optional).
  - `--log-level <level>` (P1, default `info`).
- **MCP tools surface for v1:** `sign_transaction` (P0) and `get_address`
  (P1). No `list_accounts` (single-file keystore by design).
- **Output extras:** P0 returns RLP + `{r, s, v}`. P1 also returns the
  transaction `hash` and the keystore-derived `from` address for caller
  confirmation.
- **Security defaults (P0):** never log password / private key / keystore
  contents; zero password bytes and decrypted key material immediately after
  use; decrypt only at signing time; no secret ever leaves the process;
  offline by default; HTTP binds to `127.0.0.1` and requires a bearer auth
  token; warn (or, with `--strict-perms`, refuse) on world-/group-readable
  keystore or password files.
- **Threat model:** Primary user is a developer running this locally
  alongside an AI agent / MCP client on their own workstation (stdio). HTTP
  transport targets local automation; non-localhost exposure is the
  operator's risk and is unsupported.
- **Approval:** Rely on the MCP client's own tool-call approval flow. No
  separate in-server confirmation step in v1 (noted as P2-UX-1).
- **Acceptance:** Signatures verify against the keystore-derived address;
  signed tx RLP-decodes cleanly; outputs match a reference signer
  (`cast wallet sign-tx` or ethers.js v6) on known test vectors; both legacy
  and EIP-1559 paths covered by tests.
- **Signing primitive:** `github.com/ethereum/go-ethereum` `crypto`,
  `accounts/keystore`, and `core/types` packages.
- **MCP SDK:** `github.com/modelcontextprotocol/go-sdk` (official Go SDK).

### Open questions for the user to confirm

- Confirm app name `eth-signer-mcp` (or propose another).
- Confirm the optional `--chain-id` guard semantics (set => reject mismatched
  JSON `chainId`; unset => trust JSON).
- Confirm `--http-auth-token-file` as the auth model for the HTTP transport
  (vs. mTLS, vs. no auth on localhost).
- Confirm `--strict-perms` belongs at P1 (vs. promoting to P0 default-on).
- Confirm the P1 output extras (`hash`, `from`) are wanted; if not, they
  drop to P2.

## Milestones & Phases

Planning principle: there is **no separate polishing phase**. Each phase
delivers its polish items in-phase (JSON logging, `--strict-perms`, rich
`--version` in Phase 1; `get_address` and the `hash`/`from` output in
Phase 2; request logging in Phase 3), and each phase ends with an explicit
polish pass (refactor/simplify, lint, docs) so code stays clean continuously.

Four phases, ~38 working days total, single developer:

Phase 1 — Foundations (~7 days):

- Scaffold the module, CI workflow, dependency pins.
- `urfave/cli` flags + config struct; fsperm checks wired (warn by default,
  `--strict-perms` refusal).
- Observability complete: JSON slog, `--log-level`, redaction, build info +
  rich `--version`.
- Secret type + zeroing helpers + leak-scan helper.
- Stdio MCP server boots (`initialize` + empty `tools/list`); MCP SDK spike
  (in-memory transport, HTTP options, middleware pipeline order).
- Phase polish pass.

Phase 2 — Signing core (~14 days):

- Keystore fixtures (standard + light scrypt); keystore vault with zeroing +
  panic tests.
- Tx parse/build/validate (EIP-55 rule, chainId != 0, type-appropriate
  fields, data cap); signer orchestration + error taxonomy + signing audit
  log line; decrypt semaphore.
- `sign_transaction` + `get_address` tools (output includes `hash` + `from`
  from day one); error wire-encoding contract tests; offline-import test
  load-bearing.
- Golden parity vectors vs `cast` / ethers v6 (byte-identical); stdio e2e.
- Phase polish pass.

Phase 3 — HTTP transport (~9 days):

- Streamable HTTP server + `127.0.0.1` bind; bearer auth (SHA-256 +
  constant-time); hardening matrix.
- Resource bounds (MaxBytesHandler, concurrency); request logging +
  `request_id`.
- HTTP e2e + stdio/HTTP parity + concurrent-calls test + signal shutdown.
- Phase polish pass.

Phase 4 — Release (~5 days):

- Stdio + HTTP demos; operator README; version-pin verification (incl.
  govulncheck).
- Final sweep (lint/test/depguard/offline-import re-check, end-to-end leak
  audit); CHANGELOG + release notes; `eth-signer-mcp/v1.0.0` tag;
  post-release smoke.

P2 items are tracked separately and not committed for v1.

## Risks & Mitigations

- **Risk:** Prompt-injected agent crafts a malicious transaction.
  **Mitigation:** Server signs only what the caller submits; rely on MCP
  client's approval UI; `--chain-id` guard prevents wrong-network mistakes;
  document that the server does not validate semantic intent.
- **Risk:** Secret material leaked via logs.
  **Mitigation:** P0-SEC-3 plus a test that scans captured logs for known
  secret fragments.
- **Risk:** Keystore or password file world-readable.
  **Mitigation:** Permission check on startup; warn by default, refuse with
  `--strict-perms`.
- **Risk:** HTTP transport accidentally exposed to the network.
  **Mitigation:** Default bind `127.0.0.1`; bearer token required; documented
  as unsupported off-localhost.
- **Risk:** Subtle differences from reference signers on edge cases (e.g.
  v / yParity encoding, empty `data`, contract-creation tx with `to`
  omitted).
  **Mitigation:** Test vector parity against `cast` and ethers.js v6 as part
  of the acceptance bar.
- **Risk:** Drift between Go SDK version and MCP spec.
  **Mitigation:** Pin the SDK version in `go.mod`; document the supported
  MCP protocol revision in the README at implementation time. The SDK API
  surface is de-risked by a dedicated Phase 1 spike (in-memory transport,
  Streamable HTTP options, middleware pipeline order).
