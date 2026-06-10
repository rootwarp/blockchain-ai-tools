# Software Architecture (Candidate, Scale-First): Ethereum Signer MCP Server

> **Candidate variant:** `scale-first`. This document optimizes for a
> distributed-ready design with clear seams between the **signer core**, the
> **transport layer**, and the **keystore/secret loader** so each component can
> later run or scale independently. Day-one packaging is a single binary
> (modular monolith) to satisfy the PRD's "small, auditable Go binary"
> constraint, but every module boundary is **defined as if it could be
> extracted to a separate process** without rewrites.
>
> A sibling `simplicity-first` candidate (not in this file) would collapse some
> of these seams and is the trade-off this document is consciously *not*
> making. See §ADRs for the deferred trade-offs.

---

## Overview

`eth-signer-mcp` is a local MCP server that signs fully-specified Ethereum
transactions using a locally-stored Web3 Secret Storage keystore, exposes that
capability as MCP tools (`sign_transaction`, `get_address`), and runs strictly
offline (no RPC, no broadcast, no outbound network egress). The PRD scope is
intentionally narrow — single account, single-file keystore, two transaction
types (legacy EIP-155 + EIP-1559), two transports (stdio + HTTP/SSE on
loopback).

The **scale-first** architecture decomposes that small surface area along
three structural seams identified in the brief:

1. **Transport layer** — protocol framing, auth, request lifecycle.
2. **Signer core** — pure, deterministic transaction-signing domain logic.
3. **Keystore / secret loader** — file-backed key material, password handling,
   secret hygiene.

These three are kept **independently substitutable behind interfaces**,
communicate through a **typed, async-ready signing job contract**, and ship
day-one as one binary linked together but separated by Go module boundaries
inside the monorepo. The internal contract between transport and core is
**already shaped as a request/response message** (not a function call on a
domain object), so dropping in a queue, a gRPC channel, or a remote-process
keystore later is a packaging change, not an architectural one.

The guiding principle is: **today it's a binary; tomorrow it's three
processes; the code does not change shape between those two worlds.**

---

## Architecture Principles

Project-specific, on top of the defaults (single-responsibility modules, loose
coupling, interface-first, data ownership, microservice-aware):

- **Three structural seams, never crossed in-process.** Transport never
  imports keystore internals; signer core never imports transport types;
  keystore never imports MCP types. Every cross-seam call goes through an
  interface defined in a shared `signer-api` contract module.
- **Signing is a job, not a method call.** The transport layer constructs a
  `SignJob` (validated, secret-free, fully self-describing) and hands it to a
  `SignerCore` over a Go `interface` whose only method
  (`Submit(ctx, SignJob) (SignResult, error)`) is shaped to be turned into an
  RPC or queue boundary later without changing the call site.
- **Secrets live in the keystore module, behind a sealed handle.** No other
  module ever sees plaintext password bytes or `*ecdsa.PrivateKey`. The signer
  core asks the keystore for a *signing capability*, not a key.
- **Offline-only is structurally enforced.** Modules with signing
  responsibilities must not import any `net/http` *client* or RPC package; a
  build-time test asserts the import graph.
- **Stateless transports, stateful core (deliberately tiny state).** Transport
  handlers carry no per-session signing state. Core's only mutable state is
  per-job; the keystore module's only mutable state is short-lived plaintext
  during one signing operation.
- **Asynchronous boundaries from day one.** Even the in-process call from
  transport to core is shaped as a context-cancellable submit/await pair, so a
  future "submit to queue, await response on reply topic" implementation is a
  plug-in, not a rewrite.
- **No cross-module direct DB access** is a degenerate concern here (no DB) —
  but the same rule applies to *files*: only the keystore module reads the
  keystore JSON or the password file; the signer core and transport never
  touch the filesystem for secret material.
- **One config struct per module, never shared.** Each module owns its own
  config type; the bootstrap module is the only place that knows about all of
  them.

---

## System Context Diagram

```text
                         ┌─────────────────────────────┐
                         │     MCP Client              │
                         │  (Claude Desktop / CLI /    │
                         │   local automation script)  │
                         └──────┬───────────────┬──────┘
                                │ stdio         │ HTTP/SSE
                                │ (default)     │ (--http, 127.0.0.1)
                                ▼               ▼
        ┌─────────────────────────────────────────────────────┐
        │                eth-signer-mcp binary                │
        │                                                     │
        │   ┌────────────┐   ┌────────────┐   ┌───────────┐  │
        │   │ Transport  │──▶│ Signer     │──▶│ Keystore  │  │
        │   │  Layer     │   │  Core      │   │  Loader   │  │
        │   └────────────┘   └────────────┘   └─────┬─────┘  │
        │         ▲                                  │       │
        │         │ slog                             │ read  │
        │   ┌─────┴──────┐                           ▼       │
        │   │  Observ /  │             ┌───────────────────┐ │
        │   │  Redact    │             │ keystore.json     │ │
        │   └────────────┘             │ password.txt      │ │
        │                              │ (operator's disk) │ │
        └──────────────────────────────┴───────────────────┴─┘

           NO outbound network from the binary. Strictly offline.
```

The system has **no external services** in v1 — that is a PRD invariant. The
"external" boundary is the operator's filesystem (keystore + password +
optional bearer-token file) and the MCP client process on the same host.

---

## Module Overview

The monorepo lays modules out as a single deployable app (`apps/eth-signer-mcp/`)
that depends on a small set of shared libraries (`libs/*`). The library split
is the scale-first decision: each `libs/*` module is the future home of a
process or service. The app module is the only place transport, core, and
keystore touch hands.

| Module | Kind | Responsibility | Owns Data | Depends On | Communication |
|--------|------|----------------|-----------|------------|---------------|
| `libs/signer-api` | shared contract | Defines `SignJob`, `SignResult`, `SignerCore` interface, `Keystore` interface, error codes. No logic, no I/O. | — | (none) | — (types only) |
| `libs/secret` | shared library | `Secret[T]` wrapper, `clear`-based zeroing helpers, log-redaction `slog.LogValuer`. | — | — | function calls (in-process), zero-cost copy across a boundary |
| `libs/ethsign` | shared library | Pure go-ethereum signing primitives: build `LegacyTx`/`DynamicFeeTx`, choose signer, `SignTx`, `MarshalBinary`, `RawSignatureValues`, sender recovery. Stateless. | — | — | function calls; pure functions |
| `libs/mcp-tools` | shared library | MCP tool registration helpers: maps `signer-api` error codes to `CallToolResult.SetError`, derives JSON schemas via the SDK's `jsonschema.For`. | — | `signer-api` | function calls |
| `libs/obs` | shared library | Structured logging (`slog`) wrapper, log-level config, redaction guardrails, build-info. | — | `secret` | function calls |
| `apps/eth-signer-mcp/internal/transport` | app module | Wires MCP SDK (stdio + HTTP), session/auth lifecycle, request demarshalling, **does not sign**. | — | `signer-api`, `mcp-tools`, `obs` | calls `SignerCore.Submit(ctx, job)` |
| `apps/eth-signer-mcp/internal/core` | app module | Implements `SignerCore`: validates `SignJob`, applies `--chain-id` guard, asks `Keystore` for a `SigningCapability`, invokes `libs/ethsign`, emits `SignResult`. | per-job ephemeral state only | `signer-api`, `ethsign`, `obs` | calls `Keystore.WithSigningCapability(ctx, fn)` |
| `apps/eth-signer-mcp/internal/keystore` | app module | Implements `Keystore`: file-backed, permission-checks, decrypts on demand, exposes a sealed `SigningCapability` (closure-bound). Owns the *only* code path that touches password bytes / private key. | keystore.json, password file (filesystem; read-only access) | `signer-api`, `secret`, `obs` | exposes `Keystore` interface |
| `apps/eth-signer-mcp/internal/bootstrap` | app module | CLI parsing (`urfave/cli`), config loading, dependency wiring (composition root). The only place that *new*s concrete types from each seam. | — | all of the above | function calls; constructs and wires |
| `apps/eth-signer-mcp/cmd/eth-signer-mcp` | app entrypoint | `main`. Calls `bootstrap.Run`. | — | `bootstrap` | — |

**Module count justification.** Five libraries + four internal packages is on
the higher end deliberately. The PRD's surface is small, but the scale-first
brief explicitly asks for transport / signer-core / keystore-loader to be
*extractable*. The minimum decomposition that satisfies that is:

- A contract module everyone agrees on (`signer-api`).
- A pure signing engine (`ethsign`) that knows nothing about MCP or files.
- A secret-handling primitive (`secret`) reused by keystore + observability.
- A transport-side helper (`mcp-tools`) so the transport doesn't bake MCP SDK
  details into its core path.
- An observability helper (`obs`) so each module logs through a single audited
  redaction layer.

Below that count, you start mixing concerns; above it, you fragment for no
benefit. The internal-package split inside `apps/eth-signer-mcp` is the
deliberate seam where each module is a candidate for a future independent
binary.

---

## Module Dependency Graph

```text
                                  cmd/eth-signer-mcp
                                          │
                                          ▼
                                   internal/bootstrap
                            ┌─────────┬──────┬────────┐
                            ▼         ▼      ▼        ▼
              internal/transport  internal/core  internal/keystore
                            │              │           │
                            │              │           │
                            ▼              ▼           ▼
                  libs/mcp-tools     libs/ethsign   libs/secret
                            │              │           │
                            └──────┐       │           │
                                   ▼       ▼           ▼
                              libs/signer-api ◀── libs/secret ──▶  libs/obs
                                   ▲                                  ▲
                                   │                                  │
                  (every module imports signer-api;       (only modules that log
                   no module imports another module's     import obs; obs imports
                   internal package; no cycles.)          secret to redact safely.)
```

### Verified dependency rules

- `libs/signer-api` is a **leaf** (it imports nothing in this repo). Every
  other module imports it.
- `libs/secret` is a leaf except for stdlib.
- `libs/ethsign` imports `signer-api` and go-ethereum. **Does not import
  `libs/secret`** — the keystore is the one that hands `ethsign` a
  `*ecdsa.PrivateKey` inside a closure scope; `ethsign` receives raw types but
  is not responsible for their lifecycle.
- `libs/mcp-tools` imports `signer-api` and the MCP Go SDK. **Does not import
  `internal/core` or `internal/keystore`** — it is a stateless adapter.
- `libs/obs` imports `libs/secret`. Nothing else.
- `internal/transport` imports `signer-api`, `mcp-tools`, `obs`. **Does not
  import `internal/core` or `internal/keystore` directly** — it receives a
  `signer_api.SignerCore` from bootstrap.
- `internal/core` imports `signer-api`, `ethsign`, `obs`. **Does not import
  `internal/keystore`** — it receives a `signer_api.Keystore` from bootstrap.
- `internal/keystore` imports `signer-api`, `secret`, `obs`. **Does not import
  `internal/core` or `internal/transport`.**
- `internal/bootstrap` imports everything *constructor* side; the composition
  root.

**Result:** no circular dependencies. The graph is a DAG with `bootstrap` at
the root and `signer-api` / `secret` at the leaves.

A `go vet` rule plus a lightweight import-graph test (e.g. `go list -deps` in
a Make target) enforces the rule that `internal/transport` does not import
`internal/core` symbols and vice versa.

---

## Module Details

### Module: `libs/signer-api`

**Responsibility:** Defines the cross-seam contract — input/output types,
errors, interfaces — that transport, core, and keystore agree on. No logic, no
imports of go-ethereum, no imports of MCP.

**Domain Entities:**

- `SignJob` — a validated, secret-free signing request: chain id, txn type,
  nonce, to, value, data, gas, type-specific fee fields, optional
  `--chain-id` guard echo, request id.
- `SignResult` — the broadcast-ready raw transaction hex, signature components,
  optional tx hash, sender address.
- `Code` — stable error code (`invalid_input`, `unsupported_type`,
  `chain_id_mismatch`, `keystore_error`, `password_error`, `internal_error`).
  Mirrors the PRD's error contract verbatim.
- `Error` — pairs a `Code` with a non-sensitive `Message` and (optional) cause.
  Cause is *not* serialized over the boundary; only `Code` and `Message` cross.

**Data Store:** none.

**Public API:**

```go
package signerapi

type TxKind string
const (
    TxLegacy   TxKind = "legacy"
    TxEIP1559  TxKind = "eip1559"
)

// SignJob is the on-the-wire shape between transport and core. It is
// designed to be JSON-serializable so a future RPC/queue boundary is a
// transport swap, not a refactor.
type SignJob struct {
    RequestID  string  // idempotency / tracing; opaque to core
    Kind       TxKind
    ChainID    *big.Int
    Nonce      uint64
    To         *common.Address // nil for contract creation
    Value      *big.Int
    Data       []byte
    Gas        uint64

    // Legacy-only
    GasPrice   *big.Int

    // EIP-1559-only
    MaxFeePerGas         *big.Int
    MaxPriorityFeePerGas *big.Int

    // Guard echo: the chain id the operator pinned at launch (may be nil
    // if no --chain-id flag is set). Core enforces equality with ChainID.
    GuardChainID *big.Int
}

type SignResult struct {
    RawTransaction []byte         // broadcast-ready, EIP-2718 envelope
    R, S, V        *big.Int
    Hash           common.Hash    // post-sign canonical hash
    From           common.Address // recovered sender (cross-checks keystore.Address)
}

type SignerCore interface {
    // Submit blocks until the job is signed or the context is cancelled.
    // Tool-level failures are returned as (zero, *Error{Code: ...});
    // protocol/system failures are returned as a plain non-Error error.
    Submit(ctx context.Context, job SignJob) (SignResult, error)
}

// SigningCapability is a sealed handle the keystore hands to the core.
// The core can sign with it; it cannot extract the private key.
type SigningCapability interface {
    // Address returns the keystore-derived account address. Safe to log.
    Address() common.Address

    // SignTx applies the keystore's private key to the given tx using the
    // chain-aware signer. The capability is single-use per call; the
    // keystore zeroes the key when the surrounding closure returns.
    SignTx(tx *types.Transaction, signer types.Signer) (*types.Transaction, error)
}

type Keystore interface {
    // Address returns the account address; cheap (loaded once at startup,
    // not from the encrypted key).
    Address() common.Address

    // WithSigningCapability decrypts the keystore on demand, hands the
    // resulting SigningCapability to fn, and zeroes all secret material
    // before returning — regardless of how fn returns.
    WithSigningCapability(ctx context.Context, fn func(SigningCapability) error) error
}

type Error struct {
    Code    Code
    Message string
    Cause   error // never marshalled across a boundary
}
func (e *Error) Error() string { ... }
```

**Events Published / Consumed:** none in v1. The interface is **shaped** to
support a future `SignJob` → queue → `SignResult` flow (see "Service Extraction
Path"); for v1 the implementation is synchronous in-process.

**Internal Structure:**

```
libs/signer-api/
├── types.go        # SignJob, SignResult, TxKind
├── errors.go       # Code, Error
├── core.go         # SignerCore interface
├── keystore.go     # Keystore, SigningCapability interfaces
└── tests/          # shape tests: JSON-marshal SignJob → unmarshal → equal
```

**Key Design Decisions:**

- **Interface-first** — the contract is decided before either side's
  implementation, so transport and core can evolve in parallel.
- **`SignJob` is JSON-marshallable** — even though v1 doesn't cross a process
  boundary, the shape constraint forces no live `*ecdsa.PrivateKey`, no
  `func()` callbacks, no `io.Reader` references inside the type. This is the
  load-bearing decision that makes future extraction free.
- **`SigningCapability` is a sealed handle** — the keystore never returns a
  `*ecdsa.PrivateKey` to the core. The core gets a "sign this tx with the
  signer I picked" method whose implementation is the only place plaintext
  key material is touched. This is the seam that lets the keystore later run
  in a different process or behind an HSM/KMS without changing the core.

**Failure Modes:** N/A — purely declarative module.

---

### Module: `libs/secret`

**Responsibility:** Provide the redacting `Secret[T]` wrapper and zeroing
helpers so that every other module can handle and log values without leaking
them.

**Domain Entities:**

- `Secret[T]` — generic wrapper implementing `fmt.Stringer`, `fmt.GoStringer`,
  `fmt.Formatter`, `json.Marshaler`, and `slog.LogValuer`, all returning
  `[REDACTED]` placeholders. `Expose()` is the only path to the inner value.
- `ZeroBytes(b []byte)` — wraps `clear(b)` + `runtime.KeepAlive(b)`.
- `ZeroPrivateKey(k *ecdsa.PrivateKey)` — re-implements geth's unexported
  `zeroKey` as `clear(k.D.Bits()); runtime.KeepAlive(k)`.

**Data Store:** none.

**Public API (interface to other modules):**

| Function | Input | Output | Description |
|----------|-------|--------|-------------|
| `secret.New[T](v T) Secret[T]` | any value | wrapper | Wrap a value so default formatting paths redact it |
| `Secret[T].Expose() T` | — | inner | Only path to the underlying value; greppable |
| `secret.ZeroBytes(b []byte)` | mutable slice | — | Best-effort zero of the bytes |
| `secret.ZeroPrivateKey(k *ecdsa.PrivateKey)` | key | — | Best-effort zero of the scalar limbs |

**Events Published / Consumed:** none.

**Internal Structure:**

```
libs/secret/
├── secret.go            # Secret[T], all five interface implementations
├── zero.go              # ZeroBytes, ZeroPrivateKey
└── tests/
    ├── redact_test.go   # the log-scanning test (satisfies PRD P0-SEC-3)
    └── zero_test.go     # post-zero: clear() behaviour assertions
```

**Key Design Decisions:**

- **Five-interface coverage.** Catches `%s`, `%v`, `%+v`, `%#v`, `%q`, `%x`,
  `encoding/json`, and `slog`. Plus a documented rule (and lint guideline)
  that `Secret` is never embedded in a struct that is itself logged.
- **No external deps.** Pure stdlib. Keeps the audit surface tiny.

**Failure Modes:** none — pure value types. The threat is *misuse* (embedding
in a logged struct), addressed by the log-scanning test plus the import-graph
rule that `secret` is the only module that hands the `Expose()` return value
to `string(...)`.

---

### Module: `libs/ethsign`

**Responsibility:** Pure go-ethereum signing primitives. Given a validated
inner-tx description, a chain id, and a `SigningCapability`, return the
EIP-2718 envelope plus signature components and post-sign hash. No
filesystem, no MCP, no logging of secrets.

**Domain Entities:**

- `BuildLegacyInner(SignJob) *types.LegacyTx`
- `BuildDynamicFeeInner(SignJob) *types.DynamicFeeTx`
- `EnvelopeAndSign(cap SigningCapability, tx *types.Transaction, signer types.Signer) (*SignedEnvelope, error)`
- `SignedEnvelope` — `{ Raw []byte; R, S, V *big.Int; Hash common.Hash; From common.Address }`

**Data Store:** none.

**Public API:** small set of pure functions; signing entry point takes a
`SigningCapability` (defined in `signer-api`), so `ethsign` does not import
the keystore module and cannot accidentally pull plaintext key material into
its scope.

**Events Published / Consumed:** none.

**Internal Structure:**

```
libs/ethsign/
├── legacy.go       # LegacyTx build + EIP-155 specifics
├── eip1559.go      # DynamicFeeTx build
├── envelope.go     # MarshalBinary, sender recovery cross-check
├── signer.go       # LatestSignerForChainID wrapper, chain-id-zero guard
└── tests/
    ├── parity_test.go    # golden-vector parity (PRD success metrics)
    └── roundtrip_test.go # MarshalBinary → UnmarshalBinary → assert hash
```

**Key Design Decisions:**

- **Receives `SigningCapability`, not `*ecdsa.PrivateKey`.** Even though both
  are in-process today, the call shape is the one we want for tomorrow's
  remote-keystore path. `ethsign` literally cannot leak a private key it
  never holds.
- **Chain-id zero is rejected at the entrance.** Avoids the geth Homestead
  fallback footgun (per research §3 caveat) without depending on caller
  discipline.
- **Golden-vector tests live here.** They are pure-functional and require no
  filesystem or MCP; cross-checking against `cast mktx` / ethers v6 happens
  at fixture-regeneration time (separate make target, not in CI).

**Failure Modes:**

- Invalid inner tx (e.g. `gasPrice` on a type-2 tx) → typed `*signerapi.Error`
  with `Code: invalid_input`.
- `SignTx` failure → wrapped as `internal_error` with sanitized message.
- Sender-recovery mismatch (keystore address ≠ recovered) → `internal_error`;
  the signed tx is discarded. (This is a defensive check that should never
  fire; if it does, we have a bug.)

---

### Module: `libs/mcp-tools`

**Responsibility:** MCP SDK adapter helpers. Provide a tiny, well-typed layer
between the MCP Go SDK's `AddTool` / `CallToolResult` machinery and our
`signer-api.SignerCore`.

**Domain Entities:**

- `RegisterSignTransaction(server *mcp.Server, core signerapi.SignerCore)`
- `RegisterGetAddress(server *mcp.Server, addr common.Address)` (P1)
- `mapError(*signerapi.Error) *mcp.CallToolResult` — translates our stable
  codes to `result.SetError(err)` + nil Go error.

**Data Store:** none.

**Public API:** registration helpers that take a `*mcp.Server` and the
relevant `signer-api` collaborator.

**Events Published / Consumed:** none.

**Internal Structure:**

```
libs/mcp-tools/
├── register.go       # AddTool wiring for sign_transaction + get_address
├── schema.go         # input/output struct types (jsonschema tags)
├── errors.go         # map signerapi.Code → CallToolResult.SetError
└── tests/
    └── schema_test.go # asserts jsonschema-derived schema matches PRD I/O contract
```

**Key Design Decisions:**

- **Tool input/output structs live here, not in transport.** The handler
  function is `func(ctx, req, in) (*CallToolResult, out, error)`; the SDK's
  `jsonschema.For` derives the schema from `in`/`out`. Keeping these structs
  next to the registration logic makes the MCP-facing contract one
  greppable module.
- **No business logic.** `RegisterSignTransaction`'s handler **only**
  validates JSON shape, constructs a `SignJob`, calls `core.Submit`, maps
  `*signerapi.Error` → `CallToolResult.SetError` / nil Go error, and otherwise
  returns the typed `out`. Signing semantics live in core/`ethsign`.

**Failure Modes:**

- Tool-level error → `CallToolResult.SetError` + nil Go error. PRD's
  `invalid_input`, `chain_id_mismatch`, `keystore_error`, `password_error`,
  `unsupported_type` all flow through here.
- Protocol/transport error (rare) → non-nil Go error returned to the SDK.

---

### Module: `libs/obs`

**Responsibility:** Centralize structured logging, log-level config, build-info
("version" output), and redaction guardrails.

**Domain Entities:**

- `Logger` — a thin `*slog.Logger` wrapper that adds the redaction-safe
  attribute helpers and the request-id propagation key.
- `BuildInfo` — commit / build date / Go version (P1).

**Data Store:** none.

**Public API:** `obs.New(level slog.Level) *Logger`,
`obs.WithRequestID(ctx, id)`, `obs.BuildInfo()`.

**Events Published / Consumed:** none.

**Internal Structure:**

```
libs/obs/
├── logger.go        # slog wrapper; default JSON to stderr at info
├── build.go         # build-info via runtime/debug.ReadBuildInfo
└── tests/
    └── redact_test.go  # composes with libs/secret and asserts nothing leaks
                        # at debug level
```

**Key Design Decisions:**

- **Single audit point for "is a value safe to log."** All other modules
  call `obs.New(...)` and never touch `slog` directly.
- **Depends on `secret`** so the redaction integration test lives close to the
  redacting wrapper.

**Failure Modes:** none. A logger that fails to write to stderr is reported on
stderr and the process continues; logging is best-effort, not a failure
domain.

---

### Module: `apps/eth-signer-mcp/internal/transport`

**Responsibility:** Run MCP servers over stdio or HTTP/SSE; handle MCP
lifecycle (`initialize`, `tools/list`, `tools/call`); enforce HTTP bearer
auth; do **not** touch keys, files, or signing logic.

**Domain Entities:**

- `Server` — encapsulates one of two transports (stdio / HTTP). Has-a
  `*mcp.Server` registered with our tool set; uses `libs/mcp-tools` to wire
  the handlers.
- `AuthMiddleware` — constant-time bearer-token check on hashed tokens.
- `ListenerOptions` — bind address, token, SSE session timeout.

**Data Store:** none beyond ephemeral HTTP session state managed by the SDK.

**Public API (within the app):**

```go
package transport

type Options struct {
    HTTPEnabled    bool
    HTTPAddr       string
    HTTPTokenHash  [32]byte    // sha256 of bearer token; never raw bytes
    SessionTimeout time.Duration
}

func Run(ctx context.Context, opts Options, core signerapi.SignerCore, addr common.Address, log *obs.Logger) error
```

`Run` blocks until the context is cancelled; returns the first transport-level
error. It calls `mcptools.RegisterSignTransaction(server, core)` and (P1)
`mcptools.RegisterGetAddress(server, addr)`.

**Events Published / Consumed:** none.

**Internal Structure:**

```
internal/transport/
├── server.go       # Run(); selects stdio vs HTTP
├── stdio.go        # &mcp.StdioTransport{} wiring
├── http.go         # NewStreamableHTTPHandler + bearerAuth middleware
├── auth.go         # SHA-256 + subtle.ConstantTimeCompare
└── tests/
    ├── auth_test.go        # length-leak, missing-header, wrong-token cases
    └── lifecycle_test.go   # initialize, tools/list, then graceful shutdown
```

**Key Design Decisions:**

- **HTTP bearer auth pre-hashes both sides to SHA-256 before
  constant-time compare.** Avoids the length leak in
  `subtle.ConstantTimeCompare` documented in research §03.
- **`DisableLocalhostProtection` is left at `false`.** The SDK rejects DNS
  rebinding for free; we rely on it.
- **`CrossOriginProtection` is deprecated; we use a middleware chain
  instead** (research §01).
- **Transports are single-use** — `NewStreamableHTTPHandler` constructs a
  fresh transport per session (research §01). We do not reuse transport
  objects.
- **No knowledge of `internal/core` or `internal/keystore`.** Transport
  receives a `signer-api.SignerCore` from bootstrap; it is testable with a
  mock core that returns canned `SignResult` / `*signerapi.Error`.

**Failure Modes:**

- Transport blows up (e.g. HTTP listener closes) → return the error from
  `Run`; the bootstrap layer logs and exits.
- A tool-level error from core → handed back to MCP client via
  `CallToolResult.SetError`; transport itself does not fail.
- Bearer token mismatch on HTTP → 401, no key material is touched, no log
  line includes any token bytes.

---

### Module: `apps/eth-signer-mcp/internal/core`

**Responsibility:** Implement `signer-api.SignerCore`. Validate `SignJob`,
enforce the `--chain-id` guard, call into `libs/ethsign` and through to
`signer-api.Keystore` to produce a `SignResult`. **The only module that knows
how to translate a `SignJob` into a signed transaction.**

**Domain Entities:**

- `Core` — concrete `SignerCore`; holds a `Keystore`, the optional
  `--chain-id` guard, and a `*obs.Logger`.

**Data Store:** ephemeral per-job state only (`*types.Transaction` under
construction). No persistence.

**Public API:** `signer-api.SignerCore` interface (single method `Submit`).

**Events Published / Consumed:** none in v1.

**Internal Structure:**

```
internal/core/
├── core.go           # type Core; Submit() entry point
├── validate.go       # type-specific field validation; --chain-id guard
├── orchestrate.go    # keystore.WithSigningCapability(fn) → ethsign.EnvelopeAndSign
└── tests/
    ├── submit_test.go         # uses fake Keystore that returns a deterministic
    │                          # capability; asserts SignResult byte-shape
    └── guard_test.go          # chain-id mismatch is rejected BEFORE keystore is touched
```

**Key Design Decisions:**

- **Guard runs first.** PRD requires "rejected before any key material is
  touched." A test fixture asserts the fake Keystore's
  `WithSigningCapability` is never invoked on a guard-failure path.
- **The "decrypt → sign → zero" sequence is one closure call.** Core invokes
  `keystore.WithSigningCapability(ctx, func(cap) error { ... })`; everything
  that touches the decrypted key happens inside that callback. When the
  callback returns, the keystore module is responsible for zeroing.
- **No filesystem access** — by contract.

**Failure Modes:**

- Validation failure → `*signerapi.Error{Code: invalid_input | unsupported_type}`.
- Guard mismatch → `*signerapi.Error{Code: chain_id_mismatch}`.
- Keystore unavailable / password wrong → propagate the keystore module's
  `password_error` / `keystore_error`.
- Sender mismatch (recovered ≠ keystore) → `internal_error`. The capability
  is dropped, key is zeroed.
- Panic in signing path → `recover` in `Submit`, zero key via a `defer`, log
  a sanitized record, return `internal_error`.

---

### Module: `apps/eth-signer-mcp/internal/keystore`

**Responsibility:** Be the **only** module that touches the keystore JSON, the
password file, and the decrypted private key. Expose a sealed
`SigningCapability` to the core.

**Domain Entities:**

- `FileKeystore` — concrete `Keystore`; holds keystore-JSON bytes (read once
  at startup), password file path, cached address, optional `--strict-perms`.
- `SigningCapability` (concrete) — closure-bound; lives only for the duration
  of a `WithSigningCapability` call.

**Data Store:** filesystem (read-only): `--keystore <path>`,
`--password-file <path>`. No write access.

**Public API:** `signer-api.Keystore` interface, plus a constructor used by
bootstrap:

```go
package keystore

type Options struct {
    KeystorePath    string
    PasswordPath    string
    StrictPerms     bool
}

func New(opts Options, log *obs.Logger) (signerapi.Keystore, error)
```

`New` reads the keystore JSON eagerly (small file; safer to fail fast),
extracts the cached `Address` field, runs permission checks, and stashes the
JSON bytes for on-demand decryption. **It does not read the password file at
startup** — that happens at signing time per PRD P0-SEC-1.

**Events Published / Consumed:** none.

**Internal Structure:**

```
internal/keystore/
├── file.go                  # FileKeystore struct; New()
├── perms.go                 # P0-SEC-4 permission check; --strict-perms
├── capability.go            # SigningCapability concrete impl
├── decrypt.go               # WithSigningCapability: read pw → DecryptKey → fn(cap) → zero
└── tests/
    ├── decrypt_test.go         # round-trip with a fixture keystore
    ├── zero_test.go            # post-call: clear() ran, scalar limbs are zero
    ├── perms_test.go           # warns by default, refuses with --strict-perms
    └── nopassword_log_test.go  # log capture: scan for password sentinel
```

**Key Design Decisions:**

- **Sealed capability.** `SigningCapability.SignTx` is a method whose
  receiver holds the `*ecdsa.PrivateKey` in a closure. There is no
  `Capability.PrivateKey()` accessor. When `WithSigningCapability` returns,
  the closure is unreachable; the deferred zero runs.
- **Eager keystore JSON read, lazy password read.** Keystore JSON is small
  and safe to hold (it's ciphertext); the password is only on disk at
  signing time per PRD P0-SEC-1.
- **`runtime.KeepAlive` after zero.** Per research §03, places a
  `runtime.KeepAlive(key)` after the `clear(k.D.Bits())` to defeat
  dead-store elimination as a best-effort measure.

**Failure Modes:**

- Keystore JSON missing / malformed → `*signerapi.Error{Code: keystore_error}`.
- Password file missing / unreadable → `password_error`.
- `keystore.DecryptKey` fails (wrong password / scrypt failure) →
  `password_error`. Password bytes and key (if any partial state) are
  zeroed.
- Permission warning → logged at warn level; `--strict-perms` upgrades to
  startup refusal.
- The `fn` callback returns an error → propagated, but key is still zeroed
  via `defer` first.

---

### Module: `apps/eth-signer-mcp/internal/bootstrap`

**Responsibility:** Composition root. Parse CLI, build configs, construct
concrete `Keystore` → `Core` → register tools → run `Transport`. The only
module that imports every other internal package.

**Domain Entities:**

- `Config` — fully-resolved runtime config (paths, flags, log level).
- `Run(ctx, args) error` — main entry called by `cmd/eth-signer-mcp/main.go`.

**Data Store:** none.

**Public API:** `bootstrap.Run(ctx context.Context, args []string) error`.

**Events Published / Consumed:** none.

**Internal Structure:**

```
internal/bootstrap/
├── config.go        # urfave/cli definitions; flag → Config
├── run.go           # Run(): construct keystore, core, transport
├── version.go       # ldflags-injected version string (P1)
└── tests/
    └── wiring_test.go    # smoke: stdio start → tools/list returns sign_transaction
```

**Key Design Decisions:**

- **The only place `internal/keystore`, `internal/core`, `internal/transport`
  are imported together.** Every other module sees only the `signer-api`
  interfaces.
- **Strict CLI validation up front.** `--http` without `--http-auth-token-file`
  fails fast; permission checks fire before any tool is registered.
- **No globals.** Every concrete type is constructed and passed; the only
  state outside `Run`'s scope is the CLI parse result.

**Failure Modes:**

- Bad flags → `urfave/cli` prints help and exits non-zero.
- Construction error (keystore missing, perms refused under `--strict-perms`,
  HTTP token file missing) → returned from `Run`; `main` prints sanitized
  error and exits non-zero.

---

### Module: `apps/eth-signer-mcp/cmd/eth-signer-mcp`

**Responsibility:** `main`. Builds a context tied to `SIGINT` / `SIGTERM`,
calls `bootstrap.Run`, exits with the right code.

Trivially small. No interesting decisions.

---

## Cross-Cutting Concerns

### Authentication & Authorization

- **stdio:** zero auth — caller is the MCP client that spawned the
  subprocess; OS-level process boundary is the trust line. (PRD P0-MCP-2.)
- **HTTP:** bearer token from file, SHA-256-hashed at startup, constant-time
  comparison against the SHA-256 of the supplied header (research §03). No
  token, no HTTP — checked in `bootstrap`. Bind to `127.0.0.1` by default;
  SDK's `DisableLocalhostProtection=false` provides DNS-rebinding protection
  for free (research §01). No multi-tenant auth; this is a single-user local
  signer.

### Logging & Observability

- All logging goes through `libs/obs` → `slog`.
- Default level `info`, configurable via `--log-level` (P1).
- Stderr by default. JSON handler.
- **Redaction guardrails:** any sensitive value passes through
  `libs/secret.Secret[T]`; the keystore module never logs the password file
  contents; the signing path never logs the private key; the HTTP middleware
  never logs the raw `Authorization` header.
- A log-scanning test scans all log output at every level for a sentinel
  password and a sentinel-key fragment; CI fails if either appears. (Satisfies
  PRD P0-SEC-3.)
- Each request carries a `request_id` (UUIDv4 by the transport when the SDK
  doesn't provide one) propagated through `context.Context` and emitted as a
  log attribute on every line touching that request.

### Error Handling

Three error tiers, mapped clearly:

1. **Tool-level errors** (PRD codes: `invalid_input`, `unsupported_type`,
   `chain_id_mismatch`, `keystore_error`, `password_error`, `internal_error`)
   are typed as `*signerapi.Error` and returned by `core.Submit`. The
   `libs/mcp-tools` handler translates them to
   `CallToolResult.SetError(err) + nil Go error` per research §01.
2. **Protocol/transport errors** (JSON-RPC framing, HTTP listener) are
   non-nil Go errors returned to the MCP SDK.
3. **Operational/bootstrap errors** (bad flags, missing files, refused perms)
   are returned from `bootstrap.Run` and printed by `main` with codes.

`*signerapi.Error.Message` is the only field that crosses the wire. The
internal `Cause` is for logs (sanitized) and is never serialized.

### Configuration

- **CLI flags** are the only configuration surface. No env vars (out of scope
  for v1; can be added without an architectural change).
- Each module owns its config struct; `bootstrap` is the *only* place all
  configs are visible together. Adding a new flag means adding a field to one
  module's config struct and one line in `bootstrap.config.go`.
- File paths are absolute by default; relative paths are resolved against the
  process working directory at the boundary of `bootstrap`.

---

## Data Flow Diagrams

### sign_transaction (stdio, happy path)

```text
Client                  Transport          Core            Keystore         ethsign
  │                        │                 │                │                │
  │── tools/call ─────────▶│                 │                │                │
  │                        │ build SignJob   │                │                │
  │                        │ (validates JSON)│                │                │
  │                        │                 │                │                │
  │                        │── Submit(job) ─▶│                │                │
  │                        │                 │ guard chainId  │                │
  │                        │                 │                │                │
  │                        │                 │── WithSigCap ─▶│                │
  │                        │                 │                │ read pw file   │
  │                        │                 │                │ DecryptKey()   │
  │                        │                 │                │ clear(pw)      │
  │                        │                 │                │                │
  │                        │                 │◀── fn(cap) ────│                │
  │                        │                 │── EnvelopeAndSign(cap, tx) ────▶│
  │                        │                 │                │                │ build inner
  │                        │                 │                │                │ sign with cap
  │                        │                 │                │                │ MarshalBinary
  │                        │                 │                │                │ recover sender
  │                        │                 │◀────── *SignedEnvelope ────────│
  │                        │                 │                │                │
  │                        │                 │ assemble       │                │
  │                        │                 │ SignResult     │                │
  │                        │                 │                │                │
  │                        │                 │ (fn returns)   │                │
  │                        │                 │   ────▶ defer zero key & pw     │
  │                        │                 │                │                │
  │                        │◀── (result, nil)│                │                │
  │                        │                 │                │                │
  │◀── tools/call result ──│                 │                │                │
```

Every "the key is plaintext" moment is **between** `DecryptKey()` and the
deferred zero inside `WithSigCap`. Nothing else in the system ever holds a
plaintext key.

### sign_transaction (guard mismatch — no key material touched)

```text
Client → Transport → Core (validates) → Core (guard check) → reject
                                              │
                                              ▼
                              *signerapi.Error{Code: chain_id_mismatch}
                                              │
                                              ▼
                                Transport: SetError(...) + nil Go error
                                              │
                                              ▼
                                       Client (isError: true)

       Keystore is NEVER invoked. Asserted by a test fixture that
       fails if Keystore.WithSigningCapability is called.
```

### Startup (modular monolith)

```text
main → bootstrap.Run(ctx, args)
        ├─ parse flags via urfave/cli
        ├─ obs.New(level) → logger
        ├─ keystore.New(opts, log) → FileKeystore
        │     ├─ read keystore.json (small)
        │     ├─ permission checks (P0-SEC-4)
        │     └─ extract cached Address
        ├─ core.New(keystore, guardChainID, log) → SignerCore
        ├─ if httpMode:
        │     load token file → sha256 → opts.HTTPTokenHash
        │     clear(rawTokenBytes)
        ├─ transport.Run(ctx, opts, core, address, log)
        │     ├─ mcp.NewServer(...)
        │     ├─ mcptools.RegisterSignTransaction(server, core)
        │     ├─ mcptools.RegisterGetAddress(server, address)  (P1)
        │     └─ server.Run(ctx, transport)   // blocks
        └─ propagate first error
```

---

## Infrastructure & Deployment

### Deployment Model

- **Day-one packaging:** single Go binary at `apps/eth-signer-mcp/`; the only
  app deployed. Operator runs it as a child of the MCP client (stdio) or as a
  local daemon (HTTP).
- **Monorepo structure:** `apps/eth-signer-mcp/` plus `libs/signer-api`,
  `libs/secret`, `libs/ethsign`, `libs/mcp-tools`, `libs/obs`. Each `libs/*`
  is an independent Go module managed by `go.work` per the existing
  monorepo conventions.
- **Build:** `make build` produces `bin/eth-signer-mcp`. No CGO required by
  default (go-ethereum compiles with the decred nocgo fallback; `cgo`
  libsecp256k1 is an opt-in build).

### Scaling Strategy

**For v1, the system is single-user and single-process.** Scaling is "make
the binary fast enough on a developer laptop." Performance budget per PRD:
cold start <200 ms, signing latency p50 <50 ms after first use.

What changes if scale demand appears:

- **Transport** can scale horizontally trivially (it is stateless and
  read-only with respect to keystore JSON). Multiple HTTP fronts could share
  one signer.
- **Signer Core** is stateless per job; the bottleneck is *not* CPU but the
  serialized access to the keystore (one decrypt per signature). It can run
  as a pool of workers behind a queue.
- **Keystore** is the *hot seat*. A high-volume signing service would replace
  `FileKeystore` with an HSM/KMS-backed `Keystore` implementation — same
  interface, different backend. The `SigningCapability` seam is exactly the
  shape KMS sign-with-key APIs already expose.

### Service Extraction Path

For each module, the readiness rating maps to "how much work to split this
into its own process."

| Module | Extraction readiness | What's needed |
|--------|---------------------|---------------|
| `libs/signer-api` | **Ready now** | Already shaped as a wire contract; `SignJob`/`SignResult` are JSON-clean. |
| `libs/secret` | **Keep together** | A primitive; copy it wherever you need it. |
| `libs/ethsign` | **Ready now** | Pure functions; receives `SigningCapability` over the wire == "ask remote keystore to sign this tx". |
| `libs/mcp-tools` | **Ready now** | Stateless adapter; ships with whatever process holds the transport. |
| `libs/obs` | **Keep together** | Per-process concern; each extracted process has its own. |
| `internal/transport` | **Ready** | Stateless; needs RPC client for `SignerCore` instead of in-process interface. |
| `internal/core` | **Ready** | Already submits jobs through an interface. Swap the in-proc keystore impl for an RPC keystore client. |
| `internal/keystore` | **Ready** | Already the only module that touches secret files. Re-package as `apps/eth-keystore-service/` with the same `signer-api.Keystore` interface served over gRPC or a queue worker. |
| `internal/bootstrap` | **Splits per binary** | Each extracted process gets its own bootstrap. |

**A plausible v2 topology (scale path, sketch):**

```text
┌────────────────┐      ┌────────────────┐      ┌─────────────────┐
│ transport-svc  │─────▶│ signer-core    │─────▶│ keystore-svc    │
│ (stateless,    │ RPC  │ (worker pool,  │ RPC  │ (HSM/KMS-backed,│
│  horizontally  │      │  N replicas)   │      │  hardware       │
│  scaled)       │      │                │      │  bottleneck)    │
└────────────────┘      └────────────────┘      └─────────────────┘
        │                       │                       │
        │ stdout                │ slog → otel           │ slog → otel
        └──── shared obs (otel collector) ──────────────┘
```

The contracts are unchanged from v1: `SignJob` over the wire, `SignResult`
back, `Keystore` interface served by `keystore-svc`. The transport-svc and
signer-core are wired with RPC clients; the keystore-svc gets the
`SigningCapability` semantics on its own server boundary (sign with key id X,
do not return the key).

No file in the v1 codebase needs to be re-shaped to land the v2 topology.
That is the scale-first promise.

---

## Technology Choices

| Concern | Choice | Rationale |
|---------|--------|-----------|
| Language | Go 1.26 | Repo standard (`go.work`); MCP SDK and go-ethereum both first-class Go. |
| CLI | `urfave/cli` v3 | PRD-mandated; mature, no objections from research. |
| MCP framework | `github.com/modelcontextprotocol/go-sdk` v1.6.x | Official SDK; v1.0.0 backward-compat commitment; typed AddTool with JSON-schema inference; SDK ships built-in DNS-rebinding protection (research §01). |
| Signing primitives | `github.com/ethereum/go-ethereum` v1.17.3 | Authoritative implementation; pinned per research §02; revisit on patched v1.17.x for the DoS advisories (not exploitable in our offline path but worth a bump). |
| Logging | `log/slog` (stdlib) + `libs/secret` redaction | No third-party dep; structured JSON; native `LogValuer` integration. |
| Config | CLI flags only (v1) | Smallest config surface that satisfies PRD; env vars / files easy to add later under `bootstrap`. |
| Wire format (cross-seam) | JSON-clean Go structs (`SignJob`) | Forces extraction-friendly shape today; trivially serializable later. |
| Test oracle | `cast mktx` (Foundry, pinned) + ethers v6 | Per research §04; vectors regenerated by a separate `make` target, never invoked by CI. |
| API style (future RPC) | TBD (gRPC vs JSON-over-HTTP) | Deferred; the `signer-api` interface is RPC-style by design. |
| Message bus (future) | TBD (NATS / RabbitMQ / SQS) | Only relevant once v2 extraction happens; out of v1 scope. |

---

## ADRs (Architecture Decision Records)

### ADR-001: Three-module structural split (transport / core / keystore)

- **Status:** Accepted.
- **Context:** The scale-first brief asks for transport, signer core, and
  keystore/secret loader to be independently scalable. PRD's single-binary
  constraint means we cannot deploy multiple processes today.
- **Decision:** Define the three components as distinct internal packages in
  the app, talking only through interfaces declared in a shared `libs/signer-api`
  module. Each one is testable in isolation with mocks.
- **Alternatives Considered:**
  1. **One package, file-level split** — saves a few imports, but makes the
     "what does the keystore expose" question hard to answer by grep; doesn't
     pre-commit us to the extraction path. Rejected.
  2. **Three separate apps from day one** — over-engineered for v1; violates
     PRD "small auditable binary"; operator UX would suffer (three processes
     to launch). Rejected.
- **Consequences:** A modest amount of interface plumbing in v1 (one extra
  module, four interfaces). The win is that the v2 extraction is a
  bootstrap-only change, not a code reshape.

### ADR-002: Sealed `SigningCapability` instead of returning `*ecdsa.PrivateKey`

- **Status:** Accepted.
- **Context:** The signer core has to sign with the keystore's key. Two
  obvious shapes: (a) keystore returns the `*Key` and core calls
  `types.SignTx(tx, signer, key.PrivateKey)`; (b) keystore exposes a method
  that takes a `(tx, signer)` and returns a signed tx, with the key never
  leaving the keystore module's lexical scope.
- **Decision:** (b). Keystore exposes `Keystore.WithSigningCapability(ctx,
  fn)` where `fn` receives a `SigningCapability` whose `SignTx(tx, signer)`
  is the only operation. Keystore zeroes the key on `fn` return.
- **Alternatives Considered:**
  - (a) — simpler in v1, but defeats the seam: every call site that holds a
    `*Key` is a place secret material can leak by inattentive coding.
    Rejected.
  - **`SignerCore` does the decryption itself** — collapses the seam entirely
    and is incompatible with a remote keystore. Rejected.
- **Consequences:** Slightly more code in the keystore module; slightly more
  ceremony at call sites in core. Pays for itself the day we want to swap
  in an HSM-backed keystore: the interface and call shape are already
  exactly what an HSM API exposes ("sign this with key id X; you do not get
  the key").

### ADR-003: `SignJob` is JSON-clean even in-process

- **Status:** Accepted.
- **Context:** v1 is one process; the transport-to-core call is an in-process
  Go interface call. We could pass anything we want through it, including
  pointers to closures, `io.Reader`s, etc.
- **Decision:** `SignJob` is a struct that JSON-marshals to a self-contained
  request. No callbacks, no readers, no module-private types. The type lives
  in `libs/signer-api`.
- **Alternatives Considered:**
  - **Pass a `func() io.Reader` to lazily fetch the tx body** — overkill, and
    nukes the extraction story.
  - **Pass go-ethereum `*types.Transaction` directly** — drags every caller
    into go-ethereum-shaped types; rejected.
- **Consequences:** A small amount of upfront work to define field types.
  The job/result shape will not need to change to support RPC or queue
  boundaries in v2.

### ADR-004: Stay on stdlib for logging (`slog`), don't adopt `memguard` / `mlock`

- **Status:** Accepted.
- **Context:** Research §03 surveys `memguard` and `mlock` for stronger
  memory hygiene; concludes they're operational complexity not justified by
  v1's threat model (short-lived key, no swap-capture adversary in scope).
- **Decision:** Stdlib `slog` for structured logging; `libs/secret` provides
  redaction. Memory hygiene is `clear` + `runtime.KeepAlive` + scope
  discipline; no `memguard`, no `mlock`.
- **Alternatives Considered:** `memguard` enclaves; `mlock` syscall.
- **Consequences:** Best-effort, not absolute, memory erasure. Documented
  caveat. Easy to bolt on later if the threat model widens.

### ADR-005: HTTP bearer token compared as SHA-256 hashes

- **Status:** Accepted.
- **Context:** `subtle.ConstantTimeCompare` is constant-time **in contents**
  but short-circuits on length mismatch (research §03).
- **Decision:** Pre-hash both the loaded token and the supplied bearer header
  to SHA-256 once each; compare the fixed-length hashes with
  `subtle.ConstantTimeCompare`.
- **Alternatives Considered:**
  - Compare raw bytes — leaks length.
  - HMAC tokens — overkill for a single-user signer.
  - mTLS — operator UX tax disproportionate to threat model; PRD specifies
    bearer tokens.
- **Consequences:** Tiny CPU cost per request (one SHA-256); eliminates the
  length oracle.

### ADR-006: Permission check is in keystore module, refusal is `--strict-perms`-gated

- **Status:** Accepted.
- **Context:** PRD P0-SEC-4 warns on world-/group-readable keystore or
  password files; P1-SEC-1 promotes to refusal with `--strict-perms`.
- **Decision:** `internal/keystore.New` runs the check, logs a warning at
  `warn` level, and refuses startup if `StrictPerms` is true. Windows is a
  no-op with a logged note (FileMode bits aren't meaningful there).
- **Alternatives Considered:** Put the check in `bootstrap` directly — would
  bypass the keystore module's data-ownership claim. Rejected.
- **Consequences:** Bootstrap wires `StrictPerms` from the CLI flag into
  `keystore.Options`. One file owns "what does it mean for keystore files
  to be acceptable."

### ADR-007: No in-process cache of decrypted key across calls

- **Status:** Accepted.
- **Context:** PRD P0-SEC-2: decrypted key lives in memory only for one
  signing operation. A cache would amortize the scrypt KDF (~50 ms per first
  call) across many calls.
- **Decision:** No cache. Every call to `Submit` triggers a fresh
  `WithSigningCapability` → fresh decrypt → fresh zero. The PRD's latency
  budget allows this.
- **Alternatives Considered:** Time-bounded unlock (geth's `TimedUnlock`
  pattern) — explicitly out of scope per PRD and threat model.
- **Consequences:** First signing call is dominated by scrypt KDF time; this
  matches the PRD's "p50 <50 ms after first use" envelope only if you read
  "after first use" as "after this server has been re-used by something
  warming the OS page cache." A cleaner reading is "every call pays scrypt,
  and that's fine because scrypt is *also* the security." Documented.

### ADR-008: Build-time enforcement that the signing path does not import HTTP/RPC clients

- **Status:** Accepted.
- **Context:** PRD insists on "no outbound network calls of any kind"; the
  cleanest enforcement is structural.
- **Decision:** `libs/ethsign`, `libs/signer-api`, and `internal/core` and
  `internal/keystore` import none of `net/http` (client side),
  `net/rpc`, or any RPC client packages. A make target runs `go list -deps`
  and asserts the import graph does not include disallowed packages. Servers
  (`net/http.Server`) are allowed *only* in `internal/transport`.
- **Alternatives Considered:** Runtime check (block egress in a sandbox) —
  not portable; harder to test; rejected.
- **Consequences:** One extra make target; one CI step. Zero ambiguity about
  whether a code change reintroduces network egress.

---

## Open Questions

These are deferred until after the user's review of this candidate. None
block starting v1 implementation.

- **Q1.** Should `libs/signer-api` live as its own Go module (per the monorepo
  convention) from day one, or as an internal package under
  `apps/eth-signer-mcp/internal/signerapi`? The current proposal is **its own
  module under `libs/`** to make the extraction story trivial later — but
  this means a slightly bigger `go.work` and one extra `go.mod`. If we never
  extract, the small overhead is wasted. **Recommendation: do it.**
- **Q2.** Should `libs/secret` and `libs/obs` be one module or two? They're
  tightly cohesive but separate concerns. **Recommendation: keep them
  separate** so a future service that needs `obs` without the redaction
  guardrails (unlikely but possible) can drop in only what it needs.
- **Q3.** Do we want a v1 `make extract-keystore` target that *demonstrates*
  splitting `internal/keystore` into a standalone `apps/eth-keystore-svc/`
  by rewiring `internal/core` to a stub RPC client? Useful as a forcing
  function for the seam, but not in scope for the PRD's milestones.
- **Q4.** Is the `request_id` propagation tied to MCP's `CallToolRequest`
  metadata, or generated transport-side? **Recommendation:** prefer the
  SDK's request id where available; fall back to generated UUID. Defer the
  exact mechanic to implementation review.
- **Q5.** Does the bootstrap layer hold the bearer-token hash in a `[32]byte`
  (no `Secret` wrapper because a SHA-256 is not itself the secret) or in a
  `Secret[[32]byte]`? **Recommendation:** `[32]byte`. The hash is a verifier,
  not a secret per se; the original token file is what's sensitive.

## Risks

- **R1: PRD scope explicitly forbids HD/multi-account/typed-data signing in
  v1.** The scale-first split doesn't change v1 scope but it does make
  adding P2 items (`sign_message`, `sign_typed_data`, multi-account) trivial
  — each is a new `ethsign` function and a new `mcp-tools` registration.
  **Mitigation:** noted in ADR-001's consequences.
- **R2: Module count.** Five libraries + four app packages is on the higher
  end. **Mitigation:** every module passes the "describe its purpose in one
  sentence" test; ADRs justify each seam. If a reviewer wants to consolidate
  `libs/signer-api` + `libs/secret`, that's a defensible call — but it
  reintroduces the transport↔keystore dep we deliberately broke.
- **R3: `runtime.KeepAlive` is not a formal guarantee.** Research §03 is
  clear: best-effort. **Mitigation:** documented; log-scanning test enforces
  the *observable* requirement (no secret bytes in logs).
- **R4: Reference signer drift.** Foundry stdout has shifted across nightlies
  (research §04). **Mitigation:** pin Foundry tag in
  `regen-vectors.sh`; CI never invokes `cast`; fixture metadata records the
  tool version.
- **R5: go-ethereum DoS advisories on v1.17.0–v1.17.3.** Not exploitable in
  our offline path; **Mitigation:** record in `README` risk section; watch for
  a patched v1.17.x to bump.
- **R6: Over-engineering risk vs. simplicity-first sibling candidate.** This
  is the candidate the brief *asked for*. The simplicity-first variant would
  collapse `transport`/`core`/`keystore` into one package and drop
  `signer-api` + `mcp-tools`. **Mitigation:** the user is choosing this
  trade-off explicitly; the simplicity-first sibling is a separate file.

---

## Assumptions

These are the assumptions this candidate makes without asking the user.
Override any of them at review.

- **A1.** Repo placement: `apps/eth-signer-mcp/`, library modules under
  `libs/signer-api`, `libs/secret`, `libs/ethsign`, `libs/mcp-tools`,
  `libs/obs`, each its own Go module per existing monorepo conventions and
  `scripts/new-module.sh` / `make new-lib` workflow.
- **A2.** Library package names follow the convention "drop separators from
  the directory name" (per the repo's `CLAUDE.md`): `libs/signer-api` →
  package `signerapi`; `libs/mcp-tools` → package `mcptools`.
- **A3.** Go 1.26 toolchain (per `go.work`). MCP SDK declares `go 1.25.0`;
  compatible.
- **A4.** All PRD P0 features land in v1. P1 features (`get_address`,
  `--strict-perms`, structured logging, `hash`+`from` in output) are
  implemented at the architectural level even when their handlers are
  no-ops in earlier phases (e.g. `get_address` is registered behind a
  feature flag for the Phase 1 skeleton). This keeps the module boundaries
  honest across phases.
- **A5.** No additional MCP tools in v1 beyond `sign_transaction` (P0) and
  `get_address` (P1). P2 items (`sign_message`, `sign_typed_data`,
  multi-account, etc.) are deferred.
- **A6.** Test oracles are out-of-CI: `cast mktx` (Foundry) and ethers v6
  generate fixture vectors via `make regen-vectors`; CI runs only the
  Go parity tests against the committed fixtures.
- **A7.** No telemetry, no auto-update, no analytics — at any level, in any
  module. Enforced by ADR-008's import-graph rule.
- **A8.** `request_id` propagation uses Go `context.Context` plus a typed key
  defined in `libs/obs`; if the MCP SDK exposes a request id in
  `CallToolRequest`, we adopt it; otherwise transport generates a UUID per
  request.
- **A9.** No build-time feature flags for v1 beyond `go build` defaults. The
  go-ethereum cgo libsecp256k1 vs. nocgo decred fallback choice is left at
  go-ethereum's default for the developer's build environment; parity tests
  cover both outcomes implicitly because the underlying lib yields low-s on
  both.
- **A10.** The `Secret[T]` type uses Go generics, requiring Go 1.18+; we are
  on 1.26.
- **A11.** Bootstrap is the only module that imports `urfave/cli`; no other
  module knows the CLI parsing library exists. (Keeps the door open for a
  hypothetical alt entrypoint, e.g. a library import of `bootstrap.RunWithConfig`.)
- **A12.** "Modular monolith today, distributed-ready tomorrow" is the
  load-bearing trade-off. The simplicity-first sibling explicitly does not
  carry this load.
