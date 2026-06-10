# Software Architecture: `eth-signer-mcp` — Extraction-First Candidate

> Optimization target: **extraction-first**. Every module is designed so it can
> become its own standalone Go module, package, or service with **no rewrite**
> — only an import-path change and a new composition root. The architecture
> uses ports-and-adapters (hexagonal) style: a small, dependency-free **signer
> core** defines interfaces (ports); concrete adapters (keystore loader, secret
> loader, MCP transports, CLI, config) live in their own modules and implement
> those interfaces. The composition root (`apps/eth-signer-mcp/cmd`) is the
> only place where concrete adapters are wired together.

---

## Overview

`eth-signer-mcp` is an offline, local Model Context Protocol (MCP) server that
signs Ethereum transactions using a Web3 Secret Storage keystore. The system
exposes a single MCP tool (`sign_transaction`, plus P1 `get_address`) over two
interchangeable transports (stdio, HTTP+SSE), produces broadcast-ready RLP, and
makes zero network calls.

This candidate optimizes for **extraction readiness**. The hard architectural
question — "what does it take to lift the signer core into a separate service
six months from now?" — answers to "rewire the composition root, swap an
adapter, ship." Concretely:

- The **signer core** is a pure-Go module with one dependency: go-ethereum's
  `crypto` / `core/types`. It defines its own ports (`KeyProvider`,
  `SecretProvider`, `Clock`) and consumes them via constructor injection. It
  never imports an MCP package, an HTTP package, or a CLI package.
- The **keystore loader**, **secret/password loader**, **MCP transport
  adapters**, and **CLI/config** each live in their own Go module under
  `libs/`. Each implements one or more ports defined either in the signer core
  or in `libs/contracts`. None of them imports each other.
- A separate **`libs/contracts`** module holds the stable cross-module
  interfaces and DTOs (TxRequest, SignedTx, error codes). Anything that
  crosses a module boundary is defined there; everything else is internal to
  its owning module.
- The **app binary** (`apps/eth-signer-mcp/cmd`) is the only place that
  imports concrete adapters. It is ~150 LoC of wiring.

Guiding principles: ports-and-adapters everywhere, dependency inversion on
every cross-module edge, "extract by moving the directory" as the design
litmus.

## Architecture Principles

- **Hexagonal core, adapters at the edges.** The signer core is the
  domain; everything else is an adapter implementing a port the core owns.
  Rationale: lets us replace any adapter (e.g. swap keystore for KMS, swap MCP
  for gRPC) without touching the core.
- **Dependency inversion across every module boundary.** A high-level module
  never depends on a low-level module's concrete type. It depends on an
  interface that the low-level module satisfies. Interfaces live with the
  consumer or in `libs/contracts`. Rationale: extraction never requires
  changing the importer.
- **Each module is its own Go module.** No internal "package" boundaries that
  only matter inside one `go.mod`. If the boundary is real, it's a separate
  `go.mod` from day one. Rationale: extraction is `mv` + import-path rewrite,
  not a refactor.
- **Contracts module is the only cross-module shared code.** All
  inter-module types and interfaces live in `libs/contracts`. No other module
  imports another's types. Rationale: there is exactly one place where a
  breaking change to a cross-module type can originate, so versioning the
  surface is tractable.
- **Composition root pattern.** Only `apps/eth-signer-mcp/cmd` knows the
  concrete adapter set. Libraries never import each other. Rationale: swapping
  an adapter is a one-file change.
- **Offline by structural enforcement.** The signer core's module has no
  import of any HTTP/RPC client package. A CI test asserts this. Rationale:
  prevents accidental network egress drift in v1.
- **No backdoor coupling.** No globals, no init-time registries, no shared
  in-memory state. Adapters communicate with the core only through
  constructor-injected interfaces and per-call arguments. Rationale: extracted
  modules can't rely on shared process state because there isn't any.

## System Context Diagram

```text
              ┌─────────────────────────────┐
              │   MCP Client (Claude        │
              │   Desktop / agent / script) │
              └──────────────┬──────────────┘
                             │ stdio (default) or HTTP+SSE (127.0.0.1)
                             │ Bearer token (HTTP only)
                             ▼
              ┌─────────────────────────────┐
              │     eth-signer-mcp          │
              │  (offline, single binary)   │
              │                             │
              │  ┌───────────────────────┐  │
              │  │   Signer Core         │  │
              │  └───────────────────────┘  │
              └──────────────┬──────────────┘
                             │ filesystem reads only
                             ▼
              ┌─────────────────────────────┐
              │  Local filesystem           │
              │   - keystore.json (encrypted)│
              │   - password.txt            │
              │   - http-auth-token.txt     │
              └─────────────────────────────┘

   (No outbound network. No RPC. No telemetry.)
```

## Module Overview

| Module | Path | Responsibility | Owns Data | Depends On | Communication |
|--------|------|---------------|-----------|------------|---------------|
| **contracts** | `libs/contracts` | Stable cross-module DTOs, ports (`Signer`, `KeyProvider`, `SecretProvider`), error codes | DTOs/types only (no state) | — | sync (Go interfaces) |
| **signer-core** | `libs/signer-core` | Pure Ethereum signing domain: validates tx requests, signs, emits RLP | In-flight tx + signature; zero persistent state | `contracts`, `go-ethereum` | sync (port calls) |
| **keystore-provider** | `libs/keystore-provider` | Loads & decrypts a Web3 Secret Storage keystore on demand | Path to keystore file + cached address | `contracts`, `go-ethereum/accounts/keystore` | sync (port impl) |
| **secret-loader** | `libs/secret-loader` | Reads, trims, zeroes file-backed secrets (password, bearer token) | Path to secret file | `contracts` | sync (port impl) |
| **redact** | `libs/redact` | `Secret[T]` wrapper type with full redaction over `fmt`/`json`/`slog` | None | std lib only | sync (value type) |
| **fsperms** | `libs/fsperms` | POSIX permission checks for sensitive files; Windows-aware no-op | None | std lib only | sync (function call) |
| **mcp-tooling** | `libs/mcp-tooling` | Generic MCP tool-registration helpers, `ToolError` ↔ `CallToolResult.SetError` mapping | None | `contracts`, `modelcontextprotocol/go-sdk` | sync (helper funcs) |
| **mcp-transport-stdio** | `libs/mcp-transport-stdio` | Constructs an MCP server bound to stdio | None | `contracts`, `mcp-tooling`, go-sdk | sync (constructor) |
| **mcp-transport-http** | `libs/mcp-transport-http` | Constructs an MCP server bound to HTTP+SSE with localhost protection + bearer auth | None | `contracts`, `mcp-tooling`, `redact`, go-sdk | sync (constructor) |
| **mcp-tool-sign** | `libs/mcp-tool-sign` | Adapts a `contracts.Signer` to the MCP `sign_transaction` tool surface | None | `contracts`, `mcp-tooling` | sync (registration) |
| **mcp-tool-address** | `libs/mcp-tool-address` | Adapts a `contracts.AddressProvider` to the MCP `get_address` tool | None | `contracts`, `mcp-tooling` | sync (registration) |
| **cli-config** | `libs/cli-config` | Parses CLI flags (urfave/cli) into a strongly-typed `Config` struct | None | `urfave/cli` | sync (constructor) |
| **obs** | `libs/obs` | Structured logging (slog) + version/build info; redaction-aware | None | `redact`, std lib | sync (logger handle) |
| **eth-signer-mcp** | `apps/eth-signer-mcp` | Composition root: parses config, wires adapters into the core, starts the chosen transport | None | every lib above | sync (DI wiring) |

**Module count rationale.** Thirteen library modules sounds high for a single-
binary signer, but each maps to *exactly one extraction target* called out by
the optimization brief (signer core, keystore/secret loader, MCP transport
adapters, CLI/config) plus the cross-cutting primitives (contracts, redact,
fsperms, obs) those targets need to remain extractable in isolation. If two
modules are always extracted together, they could be merged — but each split
here corresponds to a future "swap this for X" scenario explicitly in scope
(swap keystore for KMS, add a new transport, replace CLI with config-file
launcher, etc.).

## Module Dependency Graph

```text
                              ┌─────────────────┐
                              │  contracts      │  (DTOs + interfaces only)
                              └────────┬────────┘
                                       │
            ┌──────────────────────────┼──────────────────────────┐
            ▼                          ▼                          ▼
    ┌───────────────┐         ┌─────────────────┐         ┌─────────────────┐
    │ signer-core   │         │ keystore-provider│         │ secret-loader   │
    │ (depends on   │         │ (impl KeyProvider│         │ (impl Secret    │
    │  go-ethereum  │         │  uses go-ethereum│         │  Provider, uses │
    │  only)        │         │  keystore)       │         │  fsperms+redact)│
    └───────┬───────┘         └─────────┬───────┘         └────────┬────────┘
            │                           │                          │
            │                           ▼                          ▼
            │                   ┌──────────────┐           ┌──────────────┐
            │                   │  fsperms     │           │   redact     │
            │                   └──────────────┘           └──────────────┘
            │
            │ (consumed by, not depended on)
            │
    ┌───────┴────────────────────────────────────────────────┐
    │                                                         │
    ▼                                                         ▼
┌────────────────────┐                                ┌────────────────────┐
│ mcp-tool-sign      │                                │ mcp-tool-address   │
│ (adapts Signer to  │                                │ (adapts Address    │
│  MCP tool)         │                                │  Provider)         │
└────────┬───────────┘                                └────────┬───────────┘
         │                                                     │
         │           ┌────────────────────────┐                │
         └──────────▶│   mcp-tooling          │◀───────────────┘
                     │ (typed AddTool helpers)│
                     └───────────┬────────────┘
                                 │
              ┌──────────────────┼──────────────────┐
              ▼                                     ▼
    ┌──────────────────────┐              ┌──────────────────────┐
    │ mcp-transport-stdio  │              │ mcp-transport-http   │
    └──────────────────────┘              └──────────┬───────────┘
                                                     │
                                                     ▼
                                              ┌──────────────┐
                                              │  redact      │
                                              │  fsperms     │
                                              └──────────────┘

                          ┌──────────────┐         ┌──────────────┐
                          │  cli-config  │         │     obs      │
                          └──────────────┘         └──────────────┘
                          (independent; consumed only by apps/cmd)

                          ┌─────────────────────────────────────┐
                          │     apps/eth-signer-mcp (cmd)       │
                          │     — wires everything above —      │
                          └─────────────────────────────────────┘
                                          ▲
                                          │ imports every lib above
                                          │ (composition root only)
```

**Verification: no circular dependencies.**

- `contracts` is a sink: it depends only on the standard library.
- `redact` and `fsperms` are sinks: standard library only.
- `signer-core` depends on `contracts` (+ go-ethereum). It does **not** depend
  on `keystore-provider` or `secret-loader`; it consumes them through
  `contracts.KeyProvider` and `contracts.SecretProvider` interfaces.
- `keystore-provider` depends on `contracts` + `fsperms` + go-ethereum.
- `secret-loader` depends on `contracts` + `fsperms` + `redact`.
- `mcp-tooling` depends on `contracts` + go-sdk.
- `mcp-tool-sign` / `mcp-tool-address` depend on `contracts` + `mcp-tooling`.
  They do **not** depend on `signer-core`; they receive a `contracts.Signer`
  by constructor injection.
- `mcp-transport-{stdio,http}` depend on `contracts` + `mcp-tooling` + go-sdk
  (HTTP also pulls `redact` + `fsperms`). They do **not** depend on tool
  packages; tools are registered by the cmd against the constructed server.
- `cli-config` and `obs` depend only on their own narrow deps.
- `apps/eth-signer-mcp` is the only module that imports everything else; it is
  imported by nothing.

All arrows point downward (or sideways into utility sinks). No cycles.

---

## Module Details

### Module: `contracts`

**Path:** `libs/contracts` ⇒ Go module
`github.com/rootwarp/blockchain-ai-tools/libs/contracts`

**Responsibility:** Define the stable cross-module DTOs and ports. Nothing
else.

**Domain Entities (as types):**

- `TxRequest` — the validated transaction request (chain id, nonce, to, value,
  data, gas, type, optional fee fields). Has no go-ethereum imports.
- `SignedTx` — the signed output (`RawTransaction`, `Signature {R, S, V}`,
  `Hash`, `From`).
- `Address` — a thin 20-byte type. Avoids leaking `common.Address` across
  module boundaries (keeps go-ethereum a leaf-only dep).
- `ErrorCode` — enum-like string type (`invalid_input`, `unsupported_type`,
  `chain_id_mismatch`, `keystore_error`, `password_error`, `internal_error`).
- `Error` — a `ToolError` shape with `Code`, `Message`, optional underlying
  error.

**Ports (interfaces):**

```go
type Signer interface {
    Sign(ctx context.Context, req TxRequest) (SignedTx, error)
}

type AddressProvider interface {
    Address(ctx context.Context) (Address, error)
}

type KeyProvider interface {
    // WithDecryptedKey calls fn with a decrypted *ecdsa.PrivateKey for the
    // duration of fn only. Implementations MUST zero the key after fn returns
    // or panics. The pointer MUST NOT escape fn.
    WithDecryptedKey(ctx context.Context, fn func(key DecryptedKey) error) error
    Address(ctx context.Context) (Address, error) // cached
}

type DecryptedKey interface {
    // Opaque key handle. Implementations expose go-ethereum specifics via a
    // separate concrete adapter type kept inside signer-core, not here.
    Bytes() []byte // 32-byte secret scalar; caller MUST NOT retain
    Address() Address
}

type SecretProvider interface {
    // WithSecret invokes fn with the secret bytes; the implementation
    // zeroes the bytes after fn returns. Caller MUST NOT retain.
    WithSecret(ctx context.Context, fn func([]byte) error) error
}

type Clock interface { Now() time.Time } // for log timestamps, future audit
```

**Data Store:** None. Pure types and interfaces.

**Public API:** the types above. There is no function-level API at all.

**Events Published / Consumed:** None (no eventing in v1).

**Internal Structure:**
```
libs/contracts/
├── go.mod
├── types.go          # TxRequest, SignedTx, Address, ErrorCode, Error
├── ports.go          # Signer, AddressProvider, KeyProvider, SecretProvider, Clock
└── types_test.go     # JSON round-trip & validation invariants (no behaviour)
```

**Key Design Decisions:**

- **No go-ethereum in the types.** `Address` is `[20]byte` with helpers, not
  `common.Address`. Reason: when we extract `signer-core` into a service or
  swap go-ethereum for a different library, `contracts` should not need a
  go.mod bump.
- **`DecryptedKey` is an interface, not a struct.** The concrete struct lives
  in `signer-core` (where go-ethereum is allowed). The interface exposes only
  what the caller needs.
- **`WithSecret` / `WithDecryptedKey` callback shape.** Forces the lifetime of
  secret material to be scoped — the contract literally encodes the
  "zero-after-use" rule.

**Failure Modes:** N/A — no behaviour.

---

### Module: `signer-core`

**Path:** `libs/signer-core` ⇒
`github.com/rootwarp/blockchain-ai-tools/libs/signer-core`

**Responsibility:** Implement `contracts.Signer`. Take a validated
`TxRequest`, sign it using the injected `KeyProvider`, return a `SignedTx`.

**Domain Entities:**

- `EthSigner` — concrete implementation of `contracts.Signer`. Stateless
  besides the injected dependencies.
- `txValidator` — internal: enforces strict schema, chain-id guard, type
  appropriateness of fields.
- `decryptedKey` — concrete `contracts.DecryptedKey` wrapping
  `*ecdsa.PrivateKey`. Lives here because go-ethereum lives here.

**Data Store:** None. No persistent state.

**Public API:**

| Symbol | Kind | Signature | Description |
|--------|------|-----------|-------------|
| `New` | constructor | `func New(opts Options) (*EthSigner, error)` | Build a signer wired to a `KeyProvider` |
| `(*EthSigner).Sign` | method | `func (Sign request → SignedTx)` | Implements `contracts.Signer` |
| `Options` | struct | `{Key KeyProvider, ChainIDGuard *uint64, Clock Clock}` | Constructor inputs |

**Events Published:** None.
**Events Consumed:** None.

**Internal Structure:**
```
libs/signer-core/
├── go.mod
├── signer.go              # New, EthSigner, Sign
├── validate.go            # strict-schema validation per tx type
├── encode_legacy.go       # LegacyTx build path
├── encode_eip1559.go      # DynamicFeeTx build path
├── decrypted_key.go       # concrete DecryptedKey + zeroKey helper
├── errors.go              # contracts.Error builders (no secrets in messages)
└── *_test.go              # parity vectors (testdata/), validation tests
```

**Key Design Decisions:**

- **Owns the geth dependency entirely.** Every other module is geth-free. If
  we ever swap geth for an alternative, the blast radius is this module.
- **Validation happens here, not in the MCP layer.** The MCP tool adapter is a
  thin marshaling shell; structural validation is core domain logic.
- **`chainIDGuard` is wired at construction.** Not a per-request option,
  because it is an operator policy, not a caller choice.
- **`WithDecryptedKey` callback wraps the entire sign operation.** The
  decrypted key never escapes the callback; zeroing happens in the deferred
  cleanup inside `keystore-provider`. The core never calls `clear` itself —
  that's the `KeyProvider`'s contract.
- **No `net/http` import.** A test asserts the module's transitive imports
  don't include any HTTP/RPC client. Structural offline guarantee.

**Failure Modes:**

- Invalid tx request → returns `contracts.Error{Code: invalid_input | unsupported_type | chain_id_mismatch}`. No partial signing, no key touched.
- `KeyProvider.WithDecryptedKey` returns an error → wrapped as
  `keystore_error` or `password_error`, propagated up.
- go-ethereum `SignTx` returns an error → wrapped as `internal_error`. Message
  never includes secret material (test enforces).
- Sender recovery mismatch (recovered address ≠ keystore address) → fail loud
  with `internal_error`; do not return the signed tx.

**Extraction Path:** Ready now. To extract as a standalone service: keep
`libs/contracts` + `libs/signer-core`, add a thin gRPC/HTTP transport adapter
on the *consumer* side, point it at `contracts.Signer`. No code change in
`signer-core`.

---

### Module: `keystore-provider`

**Path:** `libs/keystore-provider`

**Responsibility:** Implement `contracts.KeyProvider` backed by a single Web3
Secret Storage JSON file. Caches the keystore-derived address; decrypts on
demand per `WithDecryptedKey` call, zeroes after.

**Domain Entities:**

- `FileKeystore` — concrete implementation.
- `decryptedKeyAdapter` — bridges go-ethereum `*keystore.Key` to
  `contracts.DecryptedKey`.

**Data Store:** Read-only filesystem (the keystore JSON file path). Caches
nothing across calls except the address parsed at construction time.

**Public API:**

| Symbol | Kind | Signature | Description |
|--------|------|-----------|-------------|
| `New` | constructor | `func New(path string, secrets SecretProvider, opts ...Option) (*FileKeystore, error)` | Construct from a file path and the secret provider that backs the password |
| `WithStrictPerms` | option | `func() Option` | Refuse if file is group/world readable |
| `(*FileKeystore).Address` | method | (impl `AddressProvider`) | Returns cached EIP-55 address |
| `(*FileKeystore).WithDecryptedKey` | method | (impl `KeyProvider`) | Decrypt → callback → zero |

**Events:** None.

**Internal Structure:**
```
libs/keystore-provider/
├── go.mod
├── filekeystore.go        # New, Address, WithDecryptedKey
├── decrypt.go             # wraps keystore.DecryptKey + defer zeroKey
├── decrypted_key_adapter.go
└── *_test.go
```

**Key Design Decisions:**

- **Takes `SecretProvider` for the password, not a path or raw bytes.** This
  is the dependency-inversion edge: the keystore provider doesn't know how
  passwords are sourced (file today, KMS tomorrow). It just consumes the
  port.
- **Permission check uses `libs/fsperms`** — a sink utility, no other deps.
- **`zeroKey` re-implemented locally as `clear(k.D.Bits())`.** Lives in
  `decrypt.go`. The `keystore.Key.PrivateKey` is zeroed inside the `defer`
  before the callback returns.
- **Address is parsed once at construction time, then memoized.** Avoids
  re-reading the file for every `get_address` call.

**Failure Modes:**

- File not found / unreadable → `keystore_error`.
- Malformed JSON → `keystore_error`.
- Decryption failure → `password_error`.
- Strict perms violation → `keystore_error` at construction.

**Extraction Path:** Ready now. Swap for `kms-provider` (e.g. AWS KMS, GCP
KMS, HashiCorp Vault) by implementing the same two interfaces. The signer
core never changes.

---

### Module: `secret-loader`

**Path:** `libs/secret-loader`

**Responsibility:** Implement `contracts.SecretProvider` for file-backed
secrets (password file, HTTP bearer-token file). Read, trim trailing newline,
hand to callback, zero on return.

**Domain Entities:**

- `FileSecret` — concrete `SecretProvider` reading a single file path.

**Data Store:** Read-only filesystem; no in-memory caching by design (the
secret is read at the moment of use).

**Public API:**

| Symbol | Kind | Signature | Description |
|--------|------|-----------|-------------|
| `New` | constructor | `func New(path string, opts ...Option) (*FileSecret, error)` | Validate path/permissions at construction |
| `WithStrictPerms` | option | enables strict refusal | |
| `(*FileSecret).WithSecret` | method | impl `SecretProvider` | Read → callback → clear |
| `LoadOnce` | helper | `func LoadOnce(path string) (redact.Secret[[]byte], error)` | Single-shot loader for HTTP bearer token (loaded once at startup; can't read per-request because the HTTP middleware needs a hashed comparator) |

**Events:** None.

**Internal Structure:**
```
libs/secret-loader/
├── go.mod
├── filesecret.go          # New, WithSecret
├── loadonce.go            # LoadOnce for bearer-token case
└── *_test.go              # log-scanning + zeroing tests
```

**Key Design Decisions:**

- **No path inspection of the file content.** Trim `\r\n` only (not
  `TrimSpace`) — passwords legitimately can have trailing spaces; only the
  editor newline should be trimmed.
- **Zeroing happens unconditionally on callback return** (deferred), including
  on panic. A test asserts this.
- **`LoadOnce` returns a `redact.Secret[[]byte]`**, not raw bytes. Forces the
  caller to either expose explicitly or carry it through redaction-aware
  paths.

**Failure Modes:**

- File missing/unreadable → `password_error` (returned as `contracts.Error`).
- Empty file → `password_error` (zero-length passwords are almost certainly a
  config bug).
- Permission failure (strict mode) → `password_error` at construction.

**Extraction Path:** Ready now. Add `env-secret-loader` or `vault-secret-loader`
as drop-in alternatives; the API is the `SecretProvider` interface from
`contracts`. No consumer change.

---

### Module: `redact`

**Path:** `libs/redact`

**Responsibility:** A `Secret[T]` generic wrapper that renders as
`[REDACTED]` across `fmt.Stringer`, `fmt.GoStringer`, `fmt.Formatter`,
`json.Marshaler`, and `slog.LogValuer`.

**Data Store:** None — pure value type.

**Public API:**

| Symbol | Description |
|--------|-------------|
| `Secret[T any]` | Generic wrapper struct |
| `New[T](v T) Secret[T]` | Construct |
| `(s Secret[T]).Expose() T` | Unwrap (only path to the value) |

**Internal Structure:**
```
libs/redact/
├── go.mod
├── secret.go
└── secret_test.go   # the log-scanning test from research/03
```

**Key Design Decisions:**

- Stays standalone — depends only on `fmt`, `encoding/json`, `log/slog`.
- Five-method redaction surface (Stringer, GoStringer, Formatter,
  json.Marshaler, slog.LogValuer) per research/03 recommendation.
- Documented usage rule: never embed a `Secret` in a struct that gets logged;
  always log siblings directly. A linter check in `obs` can be added later.

**Failure Modes:** None.

**Extraction Path:** Already trivially extractable; this is a candidate to
publish as a standalone OSS module the moment its surface stabilizes.

---

### Module: `fsperms`

**Path:** `libs/fsperms`

**Responsibility:** POSIX file-permission checks for sensitive files
(`mode & 0o077 == 0`). Windows-aware no-op with a structured log warning.

**Data Store:** None.

**Public API:**

| Symbol | Signature |
|--------|-----------|
| `Check(path string, strict bool) (warning string, err error)` | Returns warning text for non-strict mode, or error in strict mode |
| `IsRestricted(info fs.FileInfo) bool` | Predicate for testing |

**Failure Modes:**

- Stat failure → wrapped error.
- World/group readable in strict mode → error with mode in octal.

**Extraction Path:** Already standalone; std-lib only.

---

### Module: `mcp-tooling`

**Path:** `libs/mcp-tooling`

**Responsibility:** Generic MCP server helpers: typed tool registration,
`contracts.Error` → `CallToolResult.SetError` mapping, structured error
response builders. Nothing application-specific lives here.

**Domain Entities:**

- `ToolRegistrar` — wraps `mcp.Server` and exposes a typed `RegisterTool`
  helper that takes a description, schema-inferring `In/Out` types, and a
  handler returning `(Out, *contracts.Error)`.
- `ResultBuilder` — converts a `contracts.Error` to the proper `CallToolResult`
  with `IsError=true` and a sanitized text-content payload.

**Data Store:** None.

**Public API:**

| Symbol | Description |
|--------|-------------|
| `NewServer(impl mcp.Implementation, opts ...Option) *Server` | Wraps `mcp.NewServer` |
| `RegisterTool[In, Out any](s *Server, t Tool, h Handler[In, Out])` | Wraps typed `mcp.AddTool` |
| `Handler[In, Out any]` | Type alias `func(ctx, In) (Out, *contracts.Error)` |
| `Tool` | Mirror of `mcp.Tool` (`Name`, `Description`) — keeps go-sdk out of consumers' import sets when possible |

**Internal Structure:**
```
libs/mcp-tooling/
├── go.mod
├── server.go              # NewServer wrapper
├── register.go            # RegisterTool, Handler
├── error_mapping.go       # contracts.Error → CallToolResult.SetError
└── *_test.go
```

**Key Design Decisions:**

- **Tool packages depend on `mcp-tooling`, not on go-sdk directly.** This
  centralizes the error-mapping convention and gives us a single seam if we
  ever swap the MCP SDK.
- **Handler signature returns `*contracts.Error`**, not Go `error`. Enforces
  the "tool error vs protocol error" distinction at the type level — a tool
  cannot accidentally signal a protocol failure for a domain error.
- **Transports do not live here.** Only the cross-cutting tool-registration
  semantics. Transports (`mcp-transport-stdio`, `mcp-transport-http`) are
  their own modules so each can be extracted or swapped independently.

**Failure Modes:** Wrong tool-error mapping is the main risk; covered by a
table-driven test asserting `IsError=true` for every code.

**Extraction Path:** Ready now. If we move to a different MCP SDK or a custom
RPC, only this module changes. Tools and transports keep their seams.

---

### Module: `mcp-transport-stdio`

**Path:** `libs/mcp-transport-stdio`

**Responsibility:** Build and run an MCP server over stdio.

**Public API:**

| Symbol | Description |
|--------|-------------|
| `Serve(ctx context.Context, srv *mcptooling.Server) error` | Run the server bound to `&mcp.StdioTransport{}` |

**Internal Structure:**
```
libs/mcp-transport-stdio/
├── go.mod
├── stdio.go
└── *_test.go
```

**Failure Modes:** Stdio framing errors → returned as Go errors (these *are*
protocol-level). The cmd treats them as fatal.

**Extraction Path:** Trivial — the entire module is ~40 LoC.

---

### Module: `mcp-transport-http`

**Path:** `libs/mcp-transport-http`

**Responsibility:** Build and run an MCP server over HTTP+SSE with localhost
DNS-rebinding protection and bearer-token middleware.

**Domain Entities:**

- `Server` — wraps `http.Server` + `mcp.StreamableHTTPHandler` + bearer auth
  middleware.

**Public API:**

| Symbol | Description |
|--------|-------------|
| `Serve(ctx context.Context, srv *mcptooling.Server, opts Options) error` | Bind, layer middleware, run |
| `Options` | `Addr string`, `BearerToken redact.Secret[[]byte]`, `Logger *slog.Logger`, `SessionTimeout time.Duration` |

**Internal Structure:**
```
libs/mcp-transport-http/
├── go.mod
├── http.go
├── bearer_middleware.go   # SHA-256 hashed constant-time compare
└── *_test.go              # localhost-only enforcement; 401 on bad token
```

**Key Design Decisions:**

- **Bearer token is a `redact.Secret[[]byte]`** at the type level. Cannot end
  up in logs accidentally.
- **Constant-time compare on SHA-256 hashes**, never raw token bytes (per
  research/03). Avoids the length-leak oracle in `crypto/subtle`.
- **Leaves the SDK's `DisableLocalhostProtection` at `false`** by default;
  bind address default is `127.0.0.1`. Both are operator-overridable but
  warned-on.
- **Does not import `secret-loader`.** The cmd loads the token via
  `secret-loader.LoadOnce` and passes the resulting `redact.Secret` here.

**Failure Modes:**

- Bad bearer → 401.
- Bind error → returned to cmd, fatal.
- SDK localhost protection rejection → 403 (handled by SDK).

**Extraction Path:** Ready. The transport is a pure adapter; the only thing
unique to this app is the bearer-auth flavor. Swapping for mTLS is a new
sibling module, not a rewrite.

---

### Module: `mcp-tool-sign`

**Path:** `libs/mcp-tool-sign`

**Responsibility:** Register the `sign_transaction` MCP tool against a
provided `contracts.Signer`.

**Public API:**

| Symbol | Description |
|--------|-------------|
| `Register(srv *mcptooling.Server, signer contracts.Signer) error` | Attach the tool to the server |
| `Input` | The typed JSON input struct (drives schema inference) |
| `Output` | The typed JSON output struct |

**Internal Structure:**
```
libs/mcp-tool-sign/
├── go.mod
├── tool.go             # Register
├── input.go            # Input struct with `jsonschema:"…"` tags
├── output.go           # Output struct (RawTransaction, Signature, Hash, From)
├── marshal.go          # Input → contracts.TxRequest; SignedTx → Output
└── *_test.go
```

**Key Design Decisions:**

- **Receives a `contracts.Signer`** by injection — does **not** import
  `signer-core`. The cmd wires the concrete `*signer.EthSigner` here.
- **Input/Output structs live with the tool**, not in `contracts`. They are
  MCP-surface concerns (JSON schema tags); changing them only ripples through
  this module. The cross-module DTO is `TxRequest` / `SignedTx`.

**Failure Modes:** All domain errors are `*contracts.Error`, surfaced via
`mcp-tooling`'s `SetError` mapping.

**Extraction Path:** Trivial.

---

### Module: `mcp-tool-address`

**Path:** `libs/mcp-tool-address`

**Responsibility:** Register the `get_address` (P1) MCP tool.

**Public API:**

| Symbol | Description |
|--------|-------------|
| `Register(srv *mcptooling.Server, p contracts.AddressProvider) error` | Attach |
| `Output` | `{ Address string }` (EIP-55 checksummed) |

**Failure Modes:** Underlying `AddressProvider` error → `keystore_error`.

**Extraction Path:** Trivial.

---

### Module: `cli-config`

**Path:** `libs/cli-config`

**Responsibility:** Parse CLI flags via `urfave/cli` into a strongly-typed
`Config` struct. No business logic, no side effects beyond flag parsing and
filepath canonicalization.

**Domain Entities:**

- `Config` — the fully-validated, immutable result of CLI parsing.
- `App` — the `urfave/cli.App` wrapper exposing a `Parse(argv []string) (Config, error)` method.

**Public API:**

| Symbol | Description |
|--------|-------------|
| `Parse(args []string) (Config, error)` | Parse argv; returns Config or error |
| `Config` | Strongly-typed result: `KeystorePath`, `PasswordFilePath`, `HTTPMode bool`, `HTTPAddr string`, `HTTPAuthTokenFilePath string`, `ChainIDGuard *uint64`, `StrictPerms bool`, `LogLevel string`, `Version Build` |

**Internal Structure:**
```
libs/cli-config/
├── go.mod
├── parse.go
├── config.go              # Config struct, validators
├── flags.go               # urfave/cli flag definitions
└── *_test.go
```

**Key Design Decisions:**

- **No coupling to any other domain module.** Returns a value object the cmd
  consumes. Could be reused by a non-MCP app or replaced by a YAML loader
  module that produces the same `Config`.
- **`Config` is the single contract** between CLI parsing and composition. If
  a future module wants to be wired by the cmd, it takes its inputs from
  `Config` (or directly from constructor injection).

**Failure Modes:** Missing required flag, malformed value → returned as
errors; the cmd prints help and exits non-zero.

**Extraction Path:** Already a self-contained module. Could be replaced by
`yaml-config` or `envvar-config` without any consumer change as long as the
output is `Config`.

---

### Module: `obs`

**Path:** `libs/obs`

**Responsibility:** Construct the application's `slog.Logger` and `BuildInfo`.
Provides the structured logger handle the cmd passes to other modules.

**Public API:**

| Symbol | Description |
|--------|-------------|
| `NewLogger(level string, w io.Writer) (*slog.Logger, error)` | JSON handler, level from CLI |
| `BuildInfo` | `{ Version, Commit, Date, GoVersion }` |
| `Build()` | Read build info via `runtime/debug` |

**Internal Structure:**
```
libs/obs/
├── go.mod
├── logger.go
├── build.go
└── *_test.go              # log-scanning test infrastructure (helper)
```

**Key Design Decisions:**

- **Logger is constructor-injected everywhere**, never read from a package
  global. This is essential for extraction: a module never depends on a global
  logger.
- **JSON handler by default.** Stderr by default. Configurable level.
- **No initialization of metrics/tracing** in v1 (PRD scope). When added,
  this module is the natural home; consumers don't change.

**Failure Modes:** Invalid log level → returned error.

---

### Module: `apps/eth-signer-mcp` (composition root)

**Path:** `apps/eth-signer-mcp`

**Responsibility:** Wire concrete adapters into the signer core, choose
transport, start the server. The *only* module that imports every other
module.

**Public API:** `main()`.

**Internal Structure:**
```
apps/eth-signer-mcp/
├── go.mod
├── main.go               # ~150 LoC: parse → build deps → wire → run
├── wire_stdio.go         # stdio composition
├── wire_http.go          # HTTP composition
└── main_test.go          # end-to-end smoke (stdio echo, HTTP 401)
```

**Composition flow (pseudo-Go):**

```go
cfg, err := cliconfig.Parse(os.Args)
logger, _ := obs.NewLogger(cfg.LogLevel, os.Stderr)

passwordProvider, _ := secretloader.New(cfg.PasswordFilePath, secretloader.WithStrictPerms(cfg.StrictPerms))
keyProvider,      _ := keystoreprovider.New(cfg.KeystorePath, passwordProvider, keystoreprovider.WithStrictPerms(cfg.StrictPerms))

ethSigner, _ := signercore.New(signercore.Options{
    Key:          keyProvider,
    ChainIDGuard: cfg.ChainIDGuard,
    Clock:        systemClock{},
})

server := mcptooling.NewServer(mcp.Implementation{Name: "eth-signer-mcp", Version: obs.Build().Version})
_ = mcptoolsign.Register(server, ethSigner)
_ = mcptooladdress.Register(server, keyProvider) // KeyProvider also implements AddressProvider

switch {
case !cfg.HTTPMode:
    return mcptransportstdio.Serve(ctx, server)
case cfg.HTTPMode:
    tok, _ := secretloader.LoadOnce(cfg.HTTPAuthTokenFilePath)
    return mcptransporthttp.Serve(ctx, server, mcptransporthttp.Options{
        Addr:        cfg.HTTPAddr,
        BearerToken: tok,
        Logger:      logger,
    })
}
```

**Key Design Decisions:**

- **No business logic.** If you find a conditional in `main.go` that depends
  on a chain id or a tx field, it belongs in `signer-core`.
- **Build info is read via `runtime/debug.ReadBuildInfo` in `obs.Build()`**, not
  via ldflags. Avoids a coupling to the Makefile.

**Failure Modes:**

- Any constructor returns an error → logged via `obs` (without secrets) and
  the process exits non-zero.
- Transport returns an error mid-run → logged and exits non-zero. No
  recovery.

**Extraction Path:** This is the only module that does **not** extract — it's
the assembly. Extracting `signer-core` to a service replaces this binary with
a new composition root that boots a gRPC adapter instead of MCP tools, but
the signer-core, keystore-provider, secret-loader, redact, fsperms, obs, and
contracts modules move verbatim.

---

## Cross-Cutting Concerns

### Authentication & Authorization

- **stdio:** authentication is the parent process. The MCP client spawns the
  binary; the host OS process boundary is the trust boundary.
- **HTTP:** bearer token, loaded once at startup as a `redact.Secret[[]byte]`,
  compared via SHA-256 + `crypto/subtle.ConstantTimeCompare` (see
  `mcp-transport-http`). 401 fires before any handler logic runs. The MCP
  SDK's localhost DNS-rebinding protection layers on top (403 on non-localhost
  Host headers from `127.0.0.1`/`[::1]`).
- **Authorization on signing:** the only operator policy is the optional
  `--chain-id` guard, enforced in `signer-core` before any key material is
  touched.
- **No multi-tenancy.** Single keystore, single account, single token. Out of
  scope.

### Logging & Observability

- `obs.NewLogger` builds the single `*slog.Logger` for the process. JSON
  handler, level from `--log-level` (P1).
- The logger is **injected**, never read from a global. Every module that logs
  takes a `*slog.Logger` as a constructor field.
- **Redaction:** all secret values are wrapped in `redact.Secret[T]` at the
  type level. The five-method coverage stops accidental logging through
  `fmt.*`, `encoding/json`, and `slog`. Documented rule: never embed a
  `Secret` in a struct that gets logged.
- **No tracing in v1.** When added, opentelemetry shim lives in `obs`.

### Error Handling

- **Domain errors** are `*contracts.Error` with a stable `Code` from a small
  enum (`invalid_input`, `unsupported_type`, `chain_id_mismatch`,
  `keystore_error`, `password_error`, `internal_error`). They flow through
  every layer untransformed and surface to the MCP client via
  `mcp-tooling.SetError`. The Go `error` channel is reserved for genuine
  protocol/transport failure.
- **No secret material in error messages.** A regex sentinel test
  (research/03) scans serialized error output for the password & key
  fragments.
- **Panics are recovered at the tool handler boundary** in `mcp-tooling`,
  converted to `internal_error`, and the process keeps serving. The signer
  core's deferred zeroing runs first.

### Configuration

- A single `cliconfig.Config` struct, produced by `libs/cli-config`. Every
  module receives the subset of `Config` it needs via constructor args. No
  module reads environment variables or files directly except where the PRD
  mandates it (keystore, password file, bearer token file — all paths come in
  through `Config`).
- **Feature flags:** none in v1. Future flags belong in `Config`.

### Concurrency & Lifecycle

- Stdio: a single connection, single-shot. No concurrency.
- HTTP: SDK manages per-session transports; the signer is stateless across
  calls (each call decrypts, signs, zeros, returns). No per-call state shared
  between requests.
- The `*ecdsa.PrivateKey` lifetime is scoped to a single
  `KeyProvider.WithDecryptedKey` callback — never escapes, never cached.

---

## Data Flow Diagrams

### `sign_transaction` end-to-end (stdio)

```text
Client                  cmd                signer-core          keystore-provider   secret-loader   fs
  │                      │                      │                      │                  │           │
  ├─ tools/call ────────▶│                      │                      │                  │           │
  │  sign_transaction    │                      │                      │                  │           │
  │                      │                      │                      │                  │           │
  │                  mcp-tooling                │                      │                  │           │
  │                  decode Input              │                      │                  │           │
  │                  → contracts.TxRequest      │                      │                  │           │
  │                      │── Sign(req) ────────▶│                      │                  │           │
  │                      │                      │── validate ──────────│                  │           │
  │                      │                      │── WithDecryptedKey ─▶│                  │           │
  │                      │                      │                      │── WithSecret ───▶│           │
  │                      │                      │                      │                  │── read ──▶│
  │                      │                      │                      │                  │◀─ bytes ──│
  │                      │                      │                      │◀─ password ──────│           │
  │                      │                      │                      │ DecryptKey       │           │
  │                      │                      │◀─ DecryptedKey ──────│ (defer zeroKey)  │           │
  │                      │                      │ SignTx                                              │
  │                      │                      │ MarshalBinary                                       │
  │                      │                      │ verify Sender                                       │
  │                      │                      │ → SignedTx                                          │
  │                      │◀─ SignedTx ──────────│                                                     │
  │                  encode Output              │                                                     │
  │◀─ result ────────────│                      │                                                     │
```

Every secret has a tightly bounded lifetime. Password bytes: read, used,
`clear()` on callback return. Private-key limbs: derived, used,
`clear(k.D.Bits())` on callback return. Nothing persists between requests.

### `get_address` (P1)

```text
Client → cmd → mcp-tool-address.Register'd handler
       → KeyProvider.Address(ctx)  (cached at construction — no decrypt)
       → contracts.Address  → EIP-55 string → result
```

### HTTP auth + signing

```text
Client ── HTTP POST ──▶ bearer-middleware (SHA-256 constant-time compare)
                  │           │
                  │   401 ────┘  (mismatch)
                  │
                  ▼
              SDK localhost-Host check (403 on rebound DNS)
                  │
                  ▼
              streamable handler → MCP dispatch → handler → signer-core → (as above)
```

---

## Infrastructure & Deployment

### Deployment Model

- **Single static Go binary** built from `apps/eth-signer-mcp` to `bin/`.
- **Modular monorepo today.** `go.work` lists every `libs/*` and the app
  module. Every library is its own `go.mod` from day one — they are not
  internal packages — which is the central enabling decision for extraction.
- **Targets:** Linux + macOS, amd64 + arm64. Windows best-effort.
- Distribution: built via `make build`; consumer copies binary, writes their
  keystore + password file, wires their MCP client.

### Scaling Strategy

- v1: single-process, single-connection (stdio) or a handful of local sessions
  (HTTP). No horizontal scaling needed.
- Future: extracting `signer-core` to a service lets multiple agents share a
  signer. At that point, scaling is "more replicas behind the new transport"
  — `signer-core` is already stateless except for the per-call lifetime of
  key material.

### Service Extraction Readiness

| Module | Extraction Readiness | Notes |
|--------|---------------------|-------|
| `contracts` | **Ready now** | Pure types. Already publishable as a standalone `go get` package. |
| `signer-core` | **Ready now** | Stateless, port-driven, geth is the only direct dep. Drop into a service repo, add a gRPC adapter that calls `contracts.Signer.Sign`, done. |
| `keystore-provider` | **Ready now** | Implements only `contracts` ports. Swap target for KMS/HSM is straightforward: implement the same two interfaces in a new module. |
| `secret-loader` | **Ready now** | Same shape as keystore-provider. Drop-in alternatives possible. |
| `mcp-tooling` | **Ready now** | Generic helper; not app-specific. |
| `mcp-transport-stdio` | **Ready now** | ~40 LoC; reusable for any MCP tool server. |
| `mcp-transport-http` | **Ready now** | Reusable for any localhost-bound MCP tool server with bearer auth. |
| `mcp-tool-sign` / `mcp-tool-address` | **Ready now** | The tool adapters are reusable wherever a `contracts.Signer` / `AddressProvider` exists. |
| `cli-config` | **Ready now** | Already standalone. |
| `redact` / `fsperms` / `obs` | **Ready now** | All std-lib only or near-it. Publishable separately. |
| `apps/eth-signer-mcp` | **N/A — assembly** | This is the composition root; "extraction" of it means writing a new composition root. |

**Service extraction example: signer-as-a-service.**

1. Create a new repo or new app `apps/eth-signer-svc`.
2. Copy a new composition root that wires `keystore-provider` (or a KMS
   provider) into `signer-core` and exposes it via gRPC.
3. The signer core, keystore provider, secret loader, contracts, redact,
   fsperms, obs modules move **verbatim** (no source change) into the new
   build. `go.work` (or replace directives) handles in-repo path.
4. Replace `mcp-tool-sign` with the gRPC service definition (different surface
   adapter, same `contracts.Signer` consumer).

The MCP transport adapters and CLI/config remain in this repo for the local
signer use case; they are independent.

---

## Technology Choices

| Concern | Choice | Rationale |
|---------|--------|-----------|
| Language | Go 1.26 | Repo standard; toolchain pinned in `go.work`; matches `go-sdk` `go 1.25.0` floor. |
| MCP framework | `github.com/modelcontextprotocol/go-sdk` v1.6.x | Official SDK; v1 backward-compat commitment; built-in DNS-rebinding protection. |
| Signing primitives | `github.com/ethereum/go-ethereum` v1.17.3 | Canonical Web3 keystore + signer; low-s parity; pinned (revisit on patched 1.17.x for the p2p DoS advisories, even though they don't affect this binary). |
| CLI | `github.com/urfave/cli` (v2) | PRD-mandated; clean flag model; help/version for free. |
| JSON schema (MCP) | `mcp.AddTool` + `jsonschema.For` | Inferred from typed structs; gives strict schema (`additionalProperties: false`) for free. |
| Structured logging | `log/slog` (stdlib) | No third-party logger; integrates with `redact.Secret.LogValue()`. |
| Constant-time compare | `crypto/subtle` (stdlib) | On SHA-256 hashes, not raw token; per research/03. |
| Build info | `runtime/debug.ReadBuildInfo` | Avoids ldflag coupling; works with `go build` and `make build`. |

---

## ADRs (Architecture Decision Records)

### ADR-001: Multi-module monorepo with per-target `go.mod`

- **Status:** Accepted.
- **Context:** The optimization target is extraction-first. Internal Go
  packages inside a single `go.mod` would be cheap today but make extraction
  a refactor (split the module, move files, rewrite imports across both
  sides). The repo's `Makefile` and `scripts/new-module.sh` already assume
  each `apps/<name>` and `libs/<name>` is its own Go module.
- **Decision:** Every architectural module is its own `go.mod` from day one,
  registered in `go.work`. Cross-module dependencies are real `require`
  edges. Library API is a public surface from the first commit.
- **Alternatives Considered:**
  - Single Go module with internal package boundaries — cheaper now, much
    more expensive to extract; loses compile-time enforcement of module
    boundaries (a file in `internal/keystore` could trivially import `internal/cli`).
  - Subset of modules (e.g. only `signer-core` and `contracts` as separate
    modules) — saves a handful of `go.mod` files but creates asymmetry: some
    modules extractable, others a refactor away.
- **Consequences:**
  - More boilerplate per module (`go.mod`, version negotiation).
  - Compile-time enforcement of every boundary — you can't accidentally
    import an adapter from the core.
  - Extraction = `mv libs/signer-core out/`; rewrite the module path in one
    place; done.

### ADR-002: Ports-and-adapters with `libs/contracts` as the only shared module

- **Status:** Accepted.
- **Context:** Modules that share types or interfaces will couple. We want
  exactly one cross-module shared surface to control coupling and version it
  deliberately.
- **Decision:** All cross-module types and interfaces live in
  `libs/contracts`. No other lib imports another lib's types. `signer-core`
  defines its dependencies (`KeyProvider`, `SecretProvider`, `Clock`) as
  interfaces declared in `contracts`. Adapters implement them.
- **Alternatives Considered:**
  - Per-module ports (interfaces declared in the consuming module) — more
    classic hexagonal style but means adapters import the consumer, which
    creates a perverse extraction dependency (extracting `signer-core` would
    force `keystore-provider` to import it).
  - Shared "common" module with utilities — quickly grows into a kitchen sink.
- **Consequences:**
  - `contracts` has to be conservative about what goes in. Anything that
    isn't *required* to cross a module boundary stays internal.
  - Versioning is tractable: one module's surface is the cross-module
    contract.
  - Adapters depend on `contracts` and on the upstream library they're
    adapting — never on the consumer. Extraction is symmetric.

### ADR-003: Callback-shaped secret/key ports (`WithSecret` / `WithDecryptedKey`)

- **Status:** Accepted.
- **Context:** PRD P0-SEC-1/2 require zero-after-use semantics for passwords
  and decrypted keys. A getter-style API ("give me the password bytes") puts
  the lifetime responsibility on the caller, which is easy to get wrong and
  hard to audit.
- **Decision:** The `SecretProvider` and `KeyProvider` ports expose only
  callback methods (`WithSecret(ctx, fn)` and `WithDecryptedKey(ctx, fn)`).
  The implementation owns the lifetime and zeroes deterministically after
  `fn` returns (including on panic, via deferred cleanup).
- **Alternatives Considered:**
  - Getter + `Close()` — works but every caller has to remember to defer
    close; one missed defer is a leak.
  - Synchronous `Get()` returning a value zeroed by a finalizer — Go
    finalizers are not deterministic.
- **Consequences:**
  - The signer core's signing function is shaped like a closure
    (`keyProvider.WithDecryptedKey(ctx, func(k DecryptedKey) error { ... })`).
  - Replacing the file-keystore with a KMS does not change the call site at
    all; the KMS provider implements `WithDecryptedKey` and does whatever it
    needs to inside.
  - Testability: a mock `KeyProvider` is straightforward; the callback's
    return value is the signing result.

### ADR-004: No go-ethereum types in `contracts`

- **Status:** Accepted.
- **Context:** If `contracts.TxRequest` used `common.Address` and
  `*big.Int`, every consumer would import go-ethereum transitively, defeating
  the extraction goal for the non-signing adapters (CLI, secret loader, etc.).
- **Decision:** `contracts` uses its own thin types (`Address [20]byte`,
  decimal-or-hex strings for big numbers carried as `string`, parsed at the
  signer-core boundary). go-ethereum is only allowed in `signer-core` and
  `keystore-provider`.
- **Alternatives Considered:**
  - Allow go-ethereum types in `contracts` — simplest, but every module pulls
    geth as a transitive dep. Build times and supply-chain surface area blow
    up unnecessarily.
- **Consequences:**
  - A small parse/serialize layer at the `signer-core` boundary.
  - Every non-signing module stays geth-free.
  - Future swap of geth for an alternative signer library touches only
    `signer-core` (and `keystore-provider` if relevant).

### ADR-005: MCP tooling helper module instead of direct `mcp.AddTool` calls

- **Status:** Accepted.
- **Context:** The PRD's structured error contract (`code` + `message`) and
  the SDK's tool-error-vs-protocol-error distinction are easy to misuse.
  Without a wrapper, every tool registration repeats the mapping logic and
  could drift.
- **Decision:** `libs/mcp-tooling` wraps `mcp.AddTool` with a typed
  `RegisterTool[In, Out]` whose handler returns `*contracts.Error`. The
  wrapper performs the `SetError` mapping uniformly.
- **Alternatives Considered:**
  - Call `mcp.AddTool` directly in each tool module — N copies of the same
    boilerplate, easy to drift.
- **Consequences:**
  - One place where tool-level error semantics live.
  - Tool modules become near-trivial (decode input, call port, encode output).
  - Swapping the MCP SDK is a one-module change.

### ADR-006: HTTP and stdio transports as separate modules

- **Status:** Accepted.
- **Context:** The PRD calls out that both transports must expose the same
  tool surface. The naïve approach is one transport module with a switch.
- **Decision:** `mcp-transport-stdio` and `mcp-transport-http` are siblings.
  The composition root chooses which to call. They share `mcp-tooling` for the
  server, but neither imports the other.
- **Alternatives Considered:**
  - One `mcp-transport` module with both. Slightly less ceremony; but couples
    the transports such that a deploy that needs only stdio still pulls the
    HTTP/SSE deps.
- **Consequences:**
  - Adding a new transport (e.g. unix socket, mTLS HTTP) is a new sibling
    module, no edits to existing ones.
  - The stdio-only build path doesn't link the HTTP middleware code.

### ADR-007: `redact.Secret[T]` as the sole secret-carrying type across modules

- **Status:** Accepted.
- **Context:** PRD P0-SEC-3 requires no secrets in any log line. The research
  recommends a five-method redaction wrapper.
- **Decision:** Adopt `redact.Secret[T]` as the only allowed cross-module
  carrier of secret values (bearer token, future signing capabilities). Inside
  a module, raw `[]byte` is allowed as long as it's scoped and zeroed.
- **Alternatives Considered:**
  - Raw `[]byte` everywhere with discipline — fails the "secrets are
    typed-out-of-existence at module boundaries" goal.
- **Consequences:**
  - The bearer token crosses three modules (`secret-loader.LoadOnce` →
    `cmd` → `mcp-transport-http`) without ever existing as raw bytes outside
    a `Secret[[]byte]`.
  - The log-scanning test (P0-SEC-3) becomes a regression-grade safety net,
    not a hope.

### ADR-008: Structural offline guarantee via import-allowlist test

- **Status:** Accepted.
- **Context:** PRD P0-SIGN-5 says strictly offline. A future contributor
  could accidentally `import "net/http"` for "just a small thing." The PRD
  research recommends a test that asserts the signing module imports no HTTP
  client.
- **Decision:** A CI test in `libs/signer-core` reads `go list -m -json all`
  output (or `golang.org/x/tools/go/packages`) and asserts the closure
  contains no HTTP/RPC client packages (`net/http` only allowed in server
  capacity, which signer-core doesn't have at all).
- **Alternatives Considered:**
  - Trust review — fragile.
- **Consequences:**
  - Structural guarantee that no signer-core change can introduce network
    egress.
  - When the test eventually fails, the fix is forced into the right place
    (in an adapter, not in the core).

---

## Assumptions

These were chosen in the absence of explicit confirmation; the user may
override any at the planning gate.

1. **Each architectural module is its own `go.mod`.** The repo's `Makefile`
   already supports this via `make new-lib` / `make new-app`. The thirteen
   library modules will each be scaffolded with the existing tool.
2. **`go.work` lists all of them.** Standard monorepo workflow; no need to
   release-version individual libs internally.
3. **Module path scheme:** `github.com/rootwarp/blockchain-ai-tools/libs/<name>`
   and `.../apps/<name>`, matching `CLAUDE.md` conventions.
4. **`urfave/cli` v2** is the assumed major version (the PRD doesn't pin).
5. **`Address` in `contracts` is a `[20]byte`** with thin EIP-55 helpers. The
   signer-core boundary converts to/from `common.Address`.
6. **MCP SDK pin:** v1.6.x as recommended in research/01.
7. **go-ethereum pin:** v1.17.3 as recommended in research/02, with an action
   item to bump on the next p2p-DoS patch (even though the binary doesn't
   import the p2p code).
8. **The composition root reads the bearer token via
   `secret-loader.LoadOnce`** rather than re-reading on every request,
   because the HTTP middleware needs a hash to compare against. The raw token
   bytes live in process memory for the lifetime of the server, wrapped in
   `redact.Secret[[]byte]`.
9. **`go-sdk` typed `In`/`Out` schema inference** (research/01) is the
   chosen mechanism for tool schemas. Inputs and outputs are Go structs with
   `jsonschema:"…"` tags; the SDK derives `additionalProperties: false`,
   which satisfies the PRD's "unknown fields rejected" requirement.
10. **No persistent state, no audit log in v1.** The research-mentioned
    P2 audit log (P2-OBS-1) would land as a new module `libs/audit-log`
    consuming `obs` and `contracts.SignedTx`; not in v1.
11. **The build info source is `runtime/debug.ReadBuildInfo`**, not `ldflags`.
    `--version` (P1-CLI-1) prints module versions, commit, build date, Go
    version from there.
12. **No concurrency-safety inside `signer-core`.** Each MCP session is
    serial; the signer core's `Sign` is reentrancy-safe by virtue of being
    stateless, but no specific lock is needed.
13. **An "import allowlist" test** lives in `libs/signer-core` and enforces
    the structural offline guarantee. Implementation uses
    `golang.org/x/tools/go/packages` to read the transitive import set.

---

## Open Questions

(These are real architectural choices the user may want to weigh in on at the
planning gate. They are *not* blockers to implementation.)

- **Is the thirteen-module split too granular?** The optimization brief
  pushes toward maximal extraction readiness. A pragmatist might fuse
  `mcp-tool-sign` + `mcp-tool-address` into `mcp-tools`, and
  `mcp-transport-stdio` + `mcp-transport-http` into `mcp-transports`, dropping
  to about ten modules. Either choice can be revised cheaply.
- **Does `contracts` need its own SemVer story?** If the architecture is
  monorepo-only for the foreseeable future, normal `go.work`-driven
  development is fine. If we plan to publish `contracts` for external
  consumers, we need a release policy.
- **Should `redact` and `fsperms` be one module ("`libs/secure`")?** They are
  both single-file utilities. Combining them saves one `go.mod` but couples
  their release cadence.
- **Should the composition root be split into a per-transport binary** (i.e.
  `eth-signer-mcp-stdio` and `eth-signer-mcp-http`)? Marginal benefit; keep
  one binary in v1.

---

## Risks

- **Extraction-first cost: more `go.mod` files.** Mitigation: the scaffolding
  script already exists (`make new-lib`); creating thirteen modules is
  mechanical. The Makefile discovers them dynamically.
- **`contracts` becomes a god-package over time.** Mitigation: ADR-002 makes
  the rule explicit; PR review enforces "only things that cross module
  boundaries belong here."
- **Adapters drift from ports.** Mitigation: every adapter has a test that
  exercises it through the port interface (not the concrete type). When the
  port changes, all adapters fail to compile, forcing alignment.
- **Future contributor reaches for go-ethereum in `contracts`.** Mitigation:
  an import-allowlist test in `contracts` that forbids `github.com/ethereum/*`.
- **Future contributor imports an adapter from `signer-core`.** Mitigation:
  the import-allowlist test in `signer-core` forbids
  `libs/keystore-provider`, `libs/mcp-tooling`, etc.
- **MCP SDK breaking change despite v1 commitment.** Mitigation: the SDK is
  consumed only through `libs/mcp-tooling`; one module to patch.
- **go-ethereum p2p DoS advisories (research/02 §3).** Not exploitable in
  this binary (no p2p code path), but watch for v1.17.x patch and bump.

---

## Architecture Quality Checklist

- [x] **No circular dependencies.** Verified module-by-module above; the
  dependency graph is a DAG with `contracts`, `redact`, `fsperms` as sinks
  and `apps/eth-signer-mcp` as the root.
- [x] **Each module has a single clear responsibility.** All thirteen pass
  the one-sentence test (see Module Overview).
- [x] **No shared databases.** No databases at all. Read-only filesystem
  reads of keystore + password + bearer token, scoped to providers.
- [x] **All inter-module communication goes through defined interfaces.**
  `contracts` is the only shared surface; all cross-module wiring is through
  its interfaces.
- [x] **Every module testable in isolation.** Adapters take only their port
  dependencies; the signer core takes mock `KeyProvider` / `SecretProvider`.
- [x] **Cross-cutting concerns standardized.** `obs` (logging), `redact`
  (secrets), `fsperms` (permissions), `mcp-tooling` (MCP error mapping).
- [x] **Failure modes defined.** Per-module section.
- [x] **Service extraction path clear.** Per-module table; the signer-core +
  contracts + providers move verbatim.
- [x] **Data flow traceable.** Sequence diagram for `sign_transaction`.
- [x] **Module count justified.** Each module corresponds to either an
  explicit extraction target from the optimization brief or a cross-cutting
  primitive the targets need to remain extractable.
