# Software Architecture: `eth-signer-mcp` (Modular Monolith — Candidate)

> Candidate: **modular monolith**. Optimized for simplicity and the fewest moving
> parts that still satisfy the PRD. One Go binary, one process, two MCP
> transports (stdio + HTTP), strictly offline.

## Overview

`eth-signer-mcp` is a single Go binary that exposes an offline Ethereum signer
as a Model Context Protocol (MCP) server. It loads a Web3 Secret Storage
keystore (single account) from a file referenced by CLI flags, accepts a
fully-specified transaction over MCP, decrypts the key at the moment of
signing, returns the broadcast-ready RLP plus `{r, s, v}`, and zeroes secret
material immediately. The binary ships **two transports** (stdio default,
HTTP/SSE on `--http`) over **one tool surface** (`sign_transaction` at P0,
`get_address` at P1); both transports register the exact same tools, schemas,
and handler.

The architecture is a **modular monolith inside a single Go module**
(`apps/eth-signer-mcp`, per the monorepo conventions in `/CLAUDE.md`). Modules
are Go *packages* under `internal/`, each owning one responsibility and
exposing a small interface. There is no inter-module persistence, no inter-
process boundary, and no shared mutable state — the only "data store" is the
keystore JSON file and the password file on disk, both touched briefly inside
one module and immediately released. This is deliberately the smallest design
that satisfies all P0 functional, security, and parity requirements in the
PRD and the four research angles in `plan/research/`.

## Architecture Principles

- **Offline by construction.** The signing module never imports an HTTP/RPC
  client; a build-time test enforces it. The only outward-facing
  `net/http` use is the MCP HTTP *server* (transport module). This is the
  structural backstop for PRD `P0-SIGN-5` and `P0-SEC-6`.
- **Secrets confined to one package and one stack frame.** The keystore JSON,
  the password bytes, and the decrypted `*ecdsa.PrivateKey` exist only inside
  `internal/signer` for the duration of one signing call. They never appear
  in another module's API, log line, error string, or struct field. PRD
  `P0-SEC-1..3`.
- **One tool surface, two transports.** Tools are registered against an MCP
  `*Server` once; the transport module picks stdio or HTTP at runtime. This
  guarantees PRD `P0-MCP-2` ("Both transports expose the same tools and the
  same JSON schemas") without per-transport code duplication.
- **Strict input schema, structured tool errors.** Tool input structs are
  inferred to JSON Schema with `additionalProperties: false`; tool errors set
  `CallToolResult.IsError = true` with a stable `code` and never return a
  non-nil Go error for input-level failures. PRD `P0-MCP-3`/`P0-MCP-4`,
  research `01-mcp-go-sdk.md`.
- **Interface-first between packages.** Every module exposes a small Go
  interface; the composition root (`cmd/`) wires concrete implementations.
  Modules depend on interfaces, not on each other's internals.
- **Microservice-aware, not microservice-mandatory.** Module boundaries are
  drawn so that any of the four "leaf" modules (`signer`, `keystore`, `tx`,
  `authz`) could be extracted behind a thin RPC if the threat model ever
  widens. v1 does not extract.
- **No circular dependencies.** Verified below in §Module Dependency Graph.

## Assumptions

The user explicitly asked for assumptions to be recorded inline rather than
clarified up front. The following are the architecture's working assumptions;
each is either lifted from the PRD or supported by the research notes
(`plan/research/00-overview.md`).

- **App location & name.** The binary lives at `apps/eth-signer-mcp/` as a Go
  module `github.com/rootwarp/blockchain-ai-tools/apps/eth-signer-mcp`, Go
  toolchain 1.26 — verbatim from the PRD repo placement note and the
  monorepo's `go.work`.
- **Single Go module, no shared `libs/`.** This v1 introduces no
  reusable cross-app libraries; the architecture deliberately keeps everything
  in this one app's `internal/` tree because the PRD scope is single-app and
  the research overview recommends "small, auditable, single afternoon" hygiene.
  The first time a second app needs (e.g.) the `Secret[T]` wrapper or the
  permission-check helpers, they can be lifted into `libs/`. The path is
  documented in §Service Extraction Path.
- **Versions pinned per research overview §1.** MCP Go SDK `v1.6.x`
  (latest verified `v1.6.1`), go-ethereum `v1.17.3`, urfave/cli `v2.x` (PRD
  choice; not separately researched).
- **MCP error model.** Tool-level errors set `CallToolResult.IsError=true` via
  `SetError` and the handler returns a nil Go `error`. Non-nil Go `error` is
  reserved for transport/system breakage. (Research `01-mcp-go-sdk.md`.)
- **HTTP hardening posture.** Bind to `127.0.0.1`, leave the SDK's
  `DisableLocalhostProtection` at false (DNS-rebinding protection on by
  default), and gate every request behind a bearer-token middleware that
  compares SHA-256 hashes of the supplied and expected tokens in constant
  time. (Research `01` + `03`.)
- **Secret hygiene.** Use the `Secret[T]` wrapper + `clear`/`runtime.KeepAlive`
  pattern from research `03`; no `memguard` in v1.
- **EIP-2930 (type 1) and EIP-4844 (type 3) are out of scope.** v1 ships
  legacy (type 0, EIP-155) and EIP-1559 (type 2) only, per PRD §Non-Goals.
- **Empty `accessList` accepted, non-empty rejected.** In line with PRD
  validation rule (`accessList` if provided must be empty in v1).
- **`get_address` ships at P1, not P0.** The first PR (Phase 1 in PRD §Milestones)
  may register only `sign_transaction`; the architecture supports
  `get_address` from day one in the `tools` module — adding it is a one-line
  registration.
- **Output extras (`hash`, `from`) ship at P1.** The signer's internal output
  type carries them from day one; the JSON schema may omit them under P0 via
  an `omitempty` tag.
- **`--strict-perms` ships at P1.** The permission check is implemented at
  P0 (warn-only); the strict-refusal upgrade is a flag flip.

---

## System Context Diagram

```text
                        (filesystem, read briefly, never written)
                         ┌────────────────────────────────────┐
                         │  keystore.json  (Web3 Secret Storage)
                         │  password.txt   (trailing-\n stripped)
                         │  token.txt      (HTTP transport only)
                         └────────────────────────────────────┘
                                          │
                                          ▼ (file reads only at signing time)
   ┌────────────┐   stdio (default)   ┌────────────────────────┐
   │ MCP Client │ ◀────────────────▶ │                        │
   │ e.g. Claude│                     │    eth-signer-mcp      │
   │   Desktop  │                     │  (single Go binary)    │
   └────────────┘                     │                        │
                                      │  • MCP server          │
   ┌────────────┐   HTTP/SSE          │  • stdio + HTTP        │
   │ Local      │   bearer-token,     │  • offline signer      │
   │ Automation │◀───127.0.0.1───────▶│  • no outbound network │
   │ (script)   │                     │                        │
   └────────────┘                     └────────────────────────┘
                                          │
                                          ▼ (stderr only)
                                      ┌────────────┐
                                      │ Structured │
                                      │ JSON logs  │
                                      └────────────┘

   No external API calls.  No DB.  No broadcaster.  No RPC.
```

---

## Module Overview

Modules here are Go **packages** inside the single
`apps/eth-signer-mcp` Go module. The "internal" prefix is dropped from names
in the table for brevity (`signer` = `internal/signer`, etc.).

| Module      | Responsibility                                                                                | Owns Data / State                                  | Depends On                | Communication                |
|-------------|-----------------------------------------------------------------------------------------------|----------------------------------------------------|---------------------------|------------------------------|
| `cmd`       | Composition root: parse CLI, wire modules, run the server (stdio or HTTP).                    | none                                               | every other module        | sync (Go function calls)     |
| `config`    | CLI flag parsing and runtime configuration value object.                                      | parsed `Config{}` value (immutable after Parse)    | —                         | sync (constructor)           |
| `secret`    | `Secret[T]` redacting wrapper + `clear`-based zeroing helpers.                                | none (just types/utilities)                        | —                         | sync (types only)            |
| `fsperm`    | Permission checks on the keystore & password files (warn / refuse / no-op on Windows).        | none                                               | —                         | sync (function call)         |
| `keystore`  | Load the keystore JSON file and the password file at signing time; zero both after use.       | the bytes it just read (zeroed before return)      | `fsperm`, `secret`        | sync (Loader interface)      |
| `tx`        | Parse and validate the input JSON; build the `*types.Transaction` (legacy / EIP-1559).        | none                                               | —                         | sync (Builder interface)     |
| `signer`    | Decrypt the keystore, sign the tx, emit RLP + `{r, s, v}` + hash + from, zero the key.        | the decrypted key (lives one stack frame)          | `keystore`, `tx`          | sync (Signer interface)      |
| `tools`     | MCP tool registration & handlers (`sign_transaction`, `get_address`); maps errors to codes.   | none                                               | `signer`, `tx`, `config`  | sync (handler calls)         |
| `transport` | Build and run the MCP server over stdio or HTTP; HTTP wraps SDK handler with bearer + binder. | stateless per request (HTTP); stdio is process-life | `tools`, `authz`, `config`| sync (Run blocks); HTTP    |
| `authz`     | Constant-time bearer-token verification middleware (HTTP only).                               | the SHA-256 of the expected token (loaded once)    | `secret`                  | sync (HTTP middleware)       |
| `obs`       | Structured logging (`slog`) bootstrap; redaction rules; build-info.                           | the global `*slog.Logger`                          | `secret`                  | sync (used by everyone)      |

A module rule: **every module is testable in isolation with mocked
dependencies** because each one consumes interfaces, not concrete types.

---

## Module Dependency Graph

The arrows go *toward dependencies*. No cycles.

```text
                          ┌────────┐
                          │ config │
                          └───┬────┘
                              │
            ┌─────────────────┼─────────────────────────────┐
            ▼                 ▼                             ▼
       ┌────────┐         ┌────────┐                  ┌──────────┐
       │ fsperm │◀──┐     │  obs   │◀──┐              │ transport│
       └────────┘   │     └────────┘   │              └────┬─────┘
                    │                  │                   │
       ┌────────┐   │                  │      ┌────────────┼───────────┐
       │ secret │◀──┼──────────────────┘      ▼            ▼           ▼
       └───▲────┘   │                   ┌─────────┐  ┌────────┐  ┌────────┐
           │        │                   │  tools  │  │ authz  │  │ config │
           │        │                   └────┬────┘  └────────┘  └────────┘
           │        │                        │
           │        │                ┌───────┼────────┐
           │        │                ▼       ▼        ▼
           │        │           ┌────────┐ ┌────┐ ┌────────┐
           │        │           │ signer │ │ tx │ │ config │
           │        │           └───┬────┘ └────┘ └────────┘
           │        │               │
           │        │       ┌───────┘
           │        │       ▼
           │     ┌──────────┐
           └─────│ keystore │
                 └──────────┘

   cmd ──▶ everything (composition root)
```

Verification:

- **No backward arrow exists** from `signer` → `tools`, `tools` → `transport`,
  or `keystore` → `signer`. Lower-level modules never depend on higher-level
  ones.
- `obs` and `secret` are leaf utilities; nothing depends *on them being
  initialised* — they are pure helpers / types.
- `config` is the only module everyone may read from; it depends on nothing.
- `cmd` is the only place that imports concrete implementations of every
  interface — by design.

If `apps/eth-signer-mcp` ever grows a second binary or shared lib, the same
graph holds because module boundaries are interface-based, not file-layout-based.

---

## Module Details

### Module: `config`

**Responsibility:** Parse CLI flags via `urfave/cli` and produce an immutable
`Config` value used by the rest of the program.

**Domain Entities:**
- `Config` — flat value type containing keystore path, password file path,
  optional chain-id guard, transport choice (stdio vs HTTP), HTTP bind
  address, HTTP auth-token file path, strict-perms flag, log level.

**Data Store:** none — values held in memory in the immutable `Config`.

**Public API (interface to other modules):**

| Method | Function | Input | Output | Description |
|--------|----------|-------|--------|-------------|
| —      | `Parse(args []string) (Config, error)` | argv          | `Config`     | Parses CLI into Config. |
| —      | `(Config) Validate() error`            | —             | error        | Cross-field validation (e.g. `--http` requires `--http-auth-token-file`). |

The `Config` type is plain data; consumers read fields directly (no
getters). Internal coupling is acceptable because `Config` is intentionally
the one shared value type — and it carries no secrets, only paths.

**Events Published / Consumed:** none.

**Internal Structure:**
```
internal/config/
├── config.go      # Config struct + field tags
├── parse.go       # urfave/cli wiring + Parse()
└── parse_test.go  # flag-set golden tests
```

**Key Design Decisions:**
- Paths are values, not opened handles — `keystore` reopens them at signing
  time. This keeps `config` free of `io` concerns and means restarting
  with a rotated password file Just Works.
- The `--chain-id` guard is `*uint64` (nilable) because the PRD makes it
  optional; the signer checks `if cfg.ChainIDGuard != nil`.

**Failure Modes:**
- Invalid flags → `Parse` returns an error; `cmd` exits non-zero with a
  one-line stderr message. Never logs flag *values* — paths are not secrets
  but contents might be.

---

### Module: `secret`

**Responsibility:** Provide the `Secret[T]` redacting wrapper and the
`clear`-based zeroing utilities used by every secret-bearing path.

**Domain Entities:**
- `Secret[T any]` — generic redacting wrapper implementing `fmt.Stringer`,
  `fmt.GoStringer`, `fmt.Formatter`, `json.Marshaler`, `slog.LogValuer`.
- `ZeroBytes(b []byte)` — wrapper over `clear(b)` for clarity at call sites.
- `ZeroBigInt(n *big.Int)` — calls `clear(n.Bits())`; the canonical re-
  implementation of geth's unexported `zeroKey`.

**Data Store:** none.

**Public API:**

| Method | Function                                  | Input  | Output | Description                                 |
|--------|-------------------------------------------|--------|--------|---------------------------------------------|
| —      | `New[T any](v T) Secret[T]`               | value  | wrapper| Wrap a value in a redacting envelope.       |
| —      | `(Secret[T]) Expose() T`                  | —      | value  | The only path to read the inner value.      |
| —      | `ZeroBytes(b []byte)`                     | slice  | —      | In-place zero; thin wrapper over `clear`.   |
| —      | `ZeroBigInt(n *big.Int)`                  | bigint | —      | Zero the limb array of a `*big.Int`.        |

**Events Published / Consumed:** none.

**Internal Structure:**
```
internal/secret/
├── secret.go         # Secret[T], the 5 redacting interfaces, Expose()
├── zero.go           # ZeroBytes, ZeroBigInt + runtime.KeepAlive helper
└── secret_test.go    # leak-scan test (P0-SEC-3 enforcement core)
```

**Key Design Decisions:**
- All five formatting interfaces are implemented on the same generic type so
  *every* common path that prints or serializes the wrapper redacts. Justified
  by research `03-secure-secret-handling.md` §(f).
- **Critical usage rule** (documented in code & test): never embed a `Secret`
  inside a logged struct — `slog` reflects through nested fields and bypasses
  `LogValue`. The package's own test exercises this anti-pattern.

**Failure Modes:**
- None; this is a pure data wrapper. Future maintainer adding a logger to the
  type *fails the leak test*, which is the point.

---

### Module: `fsperm`

**Responsibility:** Check that the keystore and password files are not group-
or world-readable; behavior is platform-aware (POSIX vs Windows).

**Public API:**

| Method | Function                                                       | Input            | Output | Description                                              |
|--------|----------------------------------------------------------------|------------------|--------|----------------------------------------------------------|
| —      | `Check(path string, strict bool) (warn bool, err error)`       | file path + mode | bool,err | POSIX: examines `Mode().Perm() & 0o077`. Windows: no-op. |

Returns `(true, nil)` when the file is readable beyond owner *and* `strict`
is false — the caller (`cmd` / `keystore`) logs the warning. Returns
`(false, err)` when `strict=true` and the file is too open.

**Events Published / Consumed:** none.

**Internal Structure:**
```
internal/fsperm/
├── check.go         # POSIX/Unix implementation
├── check_windows.go # Windows no-op (build tag)
└── check_test.go    # uses os.CreateTemp + Chmod
```

**Key Design Decisions:**
- A separate package (not buried inside `keystore`) so it can be unit tested
  without touching real keystore JSON, and so the Windows no-op is a clean
  build-tag swap rather than a branch inside `keystore`.
- The function returns `warn bool` rather than logging itself, because `obs`
  may not be initialised yet at the call point (startup ordering).

**Failure Modes:**
- File missing or not regular → `err != nil` regardless of `strict`. `cmd`
  exits non-zero.

---

### Module: `keystore`

**Responsibility:** At signing time, open the keystore JSON file and the
password file, hand the bytes to `signer`, and zero both buffers
immediately afterwards.

**Domain Entities:**
- `Material` — opaque value type holding the keystore JSON bytes and the
  password bytes. Carries a `Close()` method that zeroes both slices. **Never**
  serialised, never logged; only `signer` ever calls `Close()` (immediately,
  via defer).

**Data Store:** transient — file bytes held for the duration of one signing
call inside one goroutine.

**Public API (the `Loader` interface):**

```go
type Loader interface {
    // Load opens the keystore JSON and password files,
    // strips a trailing "\r\n" from the password, and returns
    // a Material the caller MUST Close (zeroes both buffers).
    Load(ctx context.Context) (*Material, error)
}

func (*Material) KeystoreJSON() []byte    // read by signer, never elsewhere
func (*Material) Password() []byte        // read by signer, never elsewhere
func (*Material) Close()                  // ZeroBytes both; idempotent
```

The interface lets `signer` accept a mock in tests; the production
implementation is `NewFileLoader(cfg.KeystorePath, cfg.PasswordPath)`.

**Events Published / Consumed:** none.

**Internal Structure:**
```
internal/keystore/
├── loader.go        # Loader interface + Material
├── file_loader.go   # FileLoader concrete impl
└── file_loader_test.go
```

**Key Design Decisions:**
- `Loader.Load` re-reads both files **every signing call**. The PRD requires
  decrypt-only-at-signing-time (`P0-SEC-1`); not caching is the cheapest way
  to enforce that.
- `Material.Close` is idempotent and always called via `defer` by `signer`,
  even on early-return errors.
- A file-locking variant is **not** introduced — out of scope for v1.

**Failure Modes:**
- File read errors → `keystore_error` code surfaced by `tools`.
- Password file empty after newline strip → `password_error`.

---

### Module: `tx`

**Responsibility:** Validate the MCP-supplied transaction JSON, normalise its
fields (hex parsing, leading-zero trimming, normalising `data: "0x"` to
`[]byte{}`), and build a `*types.Transaction` of the right type — without
touching any key material.

**Domain Entities:**
- `InputJSON` — typed Go struct used by `tools` for MCP schema inference;
  the package's public input type.
- `Built` — internal struct holding the `*types.Transaction`, the
  `*big.Int` chainID, and the chosen `types.Signer`.

**Data Store:** none.

**Public API (the `Builder` interface):**

```go
type Builder interface {
    // Build validates the input against the configured chain-id guard,
    // parses every field, and returns a Built tx ready to hand to signer.
    // Returns a *ValidationError with a stable code for tool errors.
    Build(in InputJSON, chainIDGuard *uint64) (*Built, error)
}

type ValidationError struct {
    Code    string // "invalid_input", "unsupported_type",
                  // "chain_id_mismatch"
    Message string // safe, never includes the field's value
}

func (*ValidationError) Error() string
```

**Events Published / Consumed:** none.

**Internal Structure:**
```
internal/tx/
├── input.go        # InputJSON + jsonschema tags
├── parse.go        # hex parsing, normalisation
├── validate.go     # chain-id guard, required-field checks
├── build_legacy.go # types.LegacyTx construction
├── build_1559.go   # types.DynamicFeeTx construction
└── *_test.go       # input fuzz + edge-case unit tests
```

**Key Design Decisions:**
- `tx` is the **only** module besides `signer` that imports
  `go-ethereum/core/types`. Splitting it out from `signer` lets us unit-test
  every parity-breaking edge case (zero-value, empty data, contract-creation,
  padded-nonce input — research `04`) with no key material in the test.
- A `ValidationError` returns a stable `code` string that `tools` maps to
  MCP error codes. This is the contract that bridges PRD's stable error codes
  to the MCP SDK's `IsError` channel.
- A separate `validate.go` runs the `--chain-id` guard **before** any
  `signer` call, so a mismatch never touches key material (PRD `P0-CLI-5`).

**Failure Modes:**
- Any invalid input → `*ValidationError` (tool error). Signer is never called.

---

### Module: `signer`

**Responsibility:** Take a `*Built` tx and a `Material`, decrypt the keystore,
sign, emit the RLP and `{r, s, v}` and hash and recovered-`from`, zero the
key and the password.

**Domain Entities:**
- `Output` — `{RawTransaction, Signature{R,S,V}, Hash, From}` — the wire
  shape the `tools` module returns to the MCP client.

**Data Store:** transient — the decrypted `*ecdsa.PrivateKey` lives in one
stack frame and is zeroed via `defer secret.ZeroBigInt(key.D)` plus
`runtime.KeepAlive(key)` (research `03` §(c)).

**Public API (the `Signer` interface):**

```go
type Signer interface {
    Sign(ctx context.Context, built *tx.Built, mat *keystore.Material) (Output, error)
}
```

**Events Published / Consumed:** none.

**Internal Structure:**
```
internal/signer/
├── signer.go        # Signer interface + GethSigner concrete impl
├── decrypt.go       # keystore.DecryptKey wrapper + zeroing
├── sign.go          # types.SignTx + RawSignatureValues + Sender cross-check
├── emit.go          # MarshalBinary + hex render + Output assembly
├── round_trip.go    # acceptance helper: UnmarshalBinary self-check
├── offline_test.go  # build-time forbid: imports of HTTP/RPC clients
└── parity_test.go   # golden vectors from research/04
```

**Key Design Decisions:**
- **One function owns the whole secret lifecycle.** Decrypt → sign → emit →
  zero all live in `signer.Sign`. No goroutines, no channels, no caching.
  The decrypted key never leaves the call frame.
- **Sender mismatch is an internal-error.** `types.Sender(signer, signedTx)`
  must equal `key.Address`. Mismatch returns `internal_error` and is logged
  *without secrets*. Pre-empts the silent-sender-mismatch trap (research `02`).
- **A package-level test** scans the signer package's import set and fails
  the build if `net/http`, `net/rpc`, or any go-ethereum `ethclient`/`rpc`
  is imported (PRD §Security — "The server makes no outbound network calls
  … structurally enforced by not importing").
- The `*Built` is type-agnostic at the signer level: it just signs whatever
  `types.Transaction` it was given with `LatestSignerForChainID`. All
  type-specific construction lives in `tx`.

**Failure Modes:**
- Bad password / keystore JSON → `password_error` or `keystore_error` (tool
  error).
- Sender mismatch → `internal_error` (tool error); kills the response; logs
  a redacted summary.
- Panic anywhere in the path → `cmd`-level recover wipes any in-flight
  Material via `defer` (which already runs during stack unwinding) then
  exits (PRD §NFR — "must result in immediate zeroing of any in-flight
  key material before the process exits").

---

### Module: `tools`

**Responsibility:** Register MCP tools with the SDK and route `tools/call`
requests into `tx` + `signer`. Map errors to PRD-stable codes.

**Domain Entities:**
- `signTransactionInput` — exported struct with `jsonschema` tags, alias of
  `tx.InputJSON` (or imported directly; one struct, one schema).
- `signTransactionOutput` — alias of `signer.Output`.
- `getAddressOutput` — `{Address string}`.

**Public API (the `Registrar` interface):**

```go
type Registrar interface {
    Register(server *mcp.Server)
}
```

**Events Published / Consumed:** none. (No event bus — modular monolith;
function calls only.)

**Internal Structure:**
```
internal/tools/
├── registrar.go     # Registrar interface + NewRegistrar(signer, tx.Builder, cfg)
├── sign_tx.go       # sign_transaction handler
├── get_address.go   # get_address handler (P1)
├── errors.go        # ValidationError -> CallToolResult.SetError mapper
└── *_test.go        # handler-level tests using stub Signer + Builder
```

**Key Design Decisions:**
- `mcp.AddTool[In, Out]` is used so the SDK infers the JSON schemas from the
  typed structs; `additionalProperties: false` falls out of struct inference
  (research `01`).
- Tool errors set `result.SetError(err)` and return `(result, zero, nil)` —
  never a non-nil Go error for input-level issues. (Research `01` end-to-end
  flow.)
- `tools` knows about `tx.ValidationError` codes but not about
  `types.Transaction` — it is the boundary between MCP wire shapes and
  domain logic.

**Failure Modes:**
- Handler-internal panic → recovered by SDK; surfaced as a generic protocol
  error. The signer's defer chain already zeroed material.

---

### Module: `authz`

**Responsibility:** Verify the HTTP transport's bearer token in constant
time. Reject unauthorized requests with 401 before any MCP handler runs.

**Domain Entities:**
- `Verifier` — wraps the SHA-256 of the expected token, exposes a single
  `Authorize(authHeader string) bool`.

**Public API:**

```go
type Verifier interface {
    Authorize(authHeader string) bool
}

func NewVerifierFromFile(path string) (Verifier, error)
func Middleware(v Verifier, next http.Handler) http.Handler
```

**Events Published / Consumed:** none.

**Internal Structure:**
```
internal/authz/
├── verifier.go      # SHA-256 hash compare via crypto/subtle.ConstantTimeCompare
├── middleware.go    # net/http Middleware
└── *_test.go        # length-leak smoke, prefix-stripping, 401 path
```

**Key Design Decisions:**
- Tokens are SHA-256-hashed before compare so `ConstantTimeCompare`'s length-
  leak short-circuit is moot (research `03` §(d)).
- The expected hash is held in a `Secret[[32]byte]` — even a hash leak is
  unwanted.
- The verifier does **not** know about MCP — it is a plain `net/http`
  middleware that can be composed with any handler.

**Failure Modes:**
- Token file unreadable or empty → `cmd` exits non-zero at startup, **before**
  binding the listener.
- Wrong / missing header → 401, before SDK handler runs.

---

### Module: `transport`

**Responsibility:** Stand up an `*mcp.Server` populated by `tools`, then
either run it on `&mcp.StdioTransport{}` (default) or behind
`mcp.NewStreamableHTTPHandler` wrapped by `authz.Middleware` and bound
to `127.0.0.1`.

**Domain Entities:**
- `Runner` — `Run(ctx) error` blocks until the transport returns.

**Public API:**

```go
type Runner interface {
    Run(ctx context.Context) error
}

func NewStdio(server *mcp.Server) Runner
func NewHTTP(server *mcp.Server, mw http.Handler, addr string) Runner
```

**Events Published / Consumed:** none.

**Internal Structure:**
```
internal/transport/
├── runner.go         # Runner interface
├── stdio.go          # &mcp.StdioTransport{} + server.Run
├── http.go           # StreamableHTTPHandler + authz middleware
└── http_test.go      # bearer 401 + DNS-rebinding 403 smoke
```

**Key Design Decisions:**
- A single `*mcp.Server` instance is shared across HTTP sessions; the
  signing-side state (`Config`, the file paths) is read-only. The SDK spins
  up a fresh `StreamableServerTransport` per session (research `01`).
- `StreamableHTTPOptions.DisableLocalhostProtection` stays at its zero value
  (false) so DNS-rebinding 403s come for free.
- `StreamableHTTPOptions.CrossOriginProtection` is **not** set
  (deprecated — research `01`). Bearer-token middleware sits in front
  instead.
- HTTP startup prints the bound `host:port` to stderr (PRD `--http-addr`
  ephemeral port behaviour).

**Failure Modes:**
- Bind error → exit non-zero. `obs` logs a generic line; no token-file
  contents.
- Stdio transport EOF → clean exit 0.

---

### Module: `obs`

**Responsibility:** Initialise the global `*slog.Logger`, declare the
redaction rules in package docs, expose a build-info helper for
`--version`.

**Domain Entities:**
- `BuildInfo` — `{Version, Commit, Date, GoVersion}` from `runtime/debug`.

**Public API:**

```go
func Init(level slog.Level) *slog.Logger // wires JSON handler to stderr
func Build() BuildInfo                    // reads runtime/debug.ReadBuildInfo
```

**Events Published / Consumed:** none.

**Internal Structure:**
```
internal/obs/
├── log.go         # slog bootstrap
├── buildinfo.go   # runtime/debug.ReadBuildInfo
└── log_test.go    # secret-leak test using the same sentinel as secret_test
```

**Key Design Decisions:**
- All structured fields go to **stderr** so stdio remains pristine for MCP
  JSON-RPC frames on the default transport.
- The `obs` package's tests share the leak sentinel with `secret` tests
  to enforce the no-secret-in-logs rule at the integration level too.

**Failure Modes:**
- None. Initialisation is infallible; level parse errors map to `info`.

---

### Module: `cmd`

**Responsibility:** Composition root — parse, wire, run.

**Internal Structure:**
```
cmd/eth-signer-mcp/
├── main.go        # urfave/cli App, sub-zero wiring, signal handling
└── main_test.go   # smoke: --help, --version, bad-flag exits
```

`main.go` does:

1. `cfg, err := config.Parse(os.Args)` → exit on error.
2. `logger := obs.Init(cfg.LogLevel)`.
3. Run `fsperm.Check` on `cfg.KeystorePath` and `cfg.PasswordPath`; log
   warnings or exit per `cfg.StrictPerms`.
4. Construct `keystore.NewFileLoader(...)`, `tx.NewBuilder()`,
   `signer.NewGethSigner(...)`, and `tools.NewRegistrar(...)`.
5. `server := mcp.NewServer(...)`, then `registrar.Register(server)`.
6. If stdio → `transport.NewStdio(server).Run(ctx)`.
7. If HTTP → load token via `authz.NewVerifierFromFile`, wrap with
   `authz.Middleware`, then `transport.NewHTTP(...).Run(ctx)`.
8. Signal handling cancels `ctx` for clean shutdown; defer-driven zeroing
   runs during shutdown.

`cmd` is the **only** package allowed to import concrete implementations
of every other module's interface. No other module imports `cmd`.

---

## Cross-Cutting Concerns

### Authentication & Authorization

- **stdio transport:** none in-server. The OS-level process boundary (the
  parent MCP client launched this subprocess) is the trust boundary.
  Approval is delegated to the client's tool-call approval UI (PRD §UX).
- **HTTP transport:** `authz.Middleware` runs in front of every request.
  401 on missing/wrong token *before* the MCP SDK handler sees the body.
  Bearer-token compare uses SHA-256 + constant-time. SDK-provided DNS-rebind
  protection adds a second 403 layer.
- **No authorisation hierarchy.** The single account in the keystore is the
  single principal; if the caller is authenticated, they may sign.

### Logging & Observability

- Structured `slog` JSON to stderr, level configurable (`--log-level`, P1).
- Standard fields: `ts`, `level`, `msg`, `module` (`signer`, `tools`,
  `transport`, …), and for HTTP: `remote_addr`, `status`, `latency_ms`.
- **Hard rule:** no secret material at any level. Enforced by:
  - `secret.Secret[T]` wrapper with five redacting interfaces;
  - never embedding `Secret` in a logged struct;
  - a leak-scanning test (the PRD's `P0-SEC-3` test) that pipes
    captured logs through a search for known sentinels (password, raw
    private-key bytes, keystore JSON fragments) at every level.
- **Audit log (P2-OBS-1)** of signed transaction hashes is *not* implemented
  in v1; the `obs` module is the natural future home.

### Error Handling

- **Tool-level errors** (PRD codes `invalid_input`, `unsupported_type`,
  `chain_id_mismatch`, `keystore_error`, `password_error`, `internal_error`)
  → `tools.errors.go` maps `tx.ValidationError` + `signer` error types into
  `CallToolResult.SetError(err)` and returns a nil Go error.
- **Protocol/system errors** (transport blew up, panic in handler) →
  non-nil Go error propagated to the SDK.
- **Error message rule:** the human-readable `message` is short, never
  contains a field value from the input that might be sensitive, and never
  contains keystore-derived material. `internal_error` is the catch-all
  with a fixed string.
- **Panic safety:** every handler is wrapped by the SDK's recover; `signer`
  adds its own `defer` chain so material zeroing happens even on panic.

### Configuration

- Sources: CLI flags only. **No** env vars in v1 (the PRD wants paths-on-disk,
  not secrets-in-env).
- All flags listed in PRD §Functional Requirements P0-CLI-1..6 + P1-CLI-1
  + P1-OBS-1; ownership lives in `internal/config`.
- Feature flags: none in v1.

---

## Data Flow Diagrams

### Flow A — stdio `sign_transaction` (happy path)

```text
1.  MCP Client (stdio)
       │  tools/call sign_transaction { ...JSON... }
       ▼
2.  transport.stdio  ──hand to──▶ mcp.Server
                                    │  (SDK validates schema, decodes into struct)
                                    ▼
3.  tools.signTxHandler(in InputJSON)
       │
       ▼  Build(in, cfg.ChainIDGuard)
4.  tx.Builder ───────────────▶ tx.ValidationError? ──no──▶ *Built (tx, signer, chainID)
       │                                  │
       │                                yes ──▶ result.SetError(tool error code) → return
       ▼
5.  signer.Sign(ctx, built, mat)
       │
       │  defer mat.Close()       (zero password + keystore JSON)
       │  defer secret.ZeroBigInt(key.D); runtime.KeepAlive(key)
       │
       │  mat := keystore.FileLoader.Load(ctx)        ← re-reads files
       │  key := keystore.DecryptKey(mat.KeystoreJSON, mat.Password)
       │  signedTx := types.SignTx(built.Tx, built.Signer, key.PrivateKey)
       │  raw := signedTx.MarshalBinary()
       │  v,r,s := signedTx.RawSignatureValues()
       │  recovered := types.Sender(built.Signer, signedTx)
       │  if recovered != key.Address ⇒ internal_error
       ▼
6.  Output {RawTransaction, Signature, Hash, From} ───▶ tools wraps in CallToolResult
       ▼
7.  mcp.Server writes JSON-RPC response to stdout
       ▼
8.  MCP Client receives result
```

### Flow B — HTTP `sign_transaction` (auth path)

```text
1.  POST / from script  (Authorization: Bearer XXXX)
       │
       ▼
2.  http.Server
       │  SDK DNS-rebind check (Host header vs localhost) → 403 if mismatch
       ▼
3.  authz.Middleware
       │  sha256(supplied) cmp sha256(expected) constant-time
       │   ├─ fail → 401, request ends
       │   └─ ok   → continue
       ▼
4.  StreamableHTTPHandler
       │  (per-session StreamableServerTransport; SDK spins up fresh)
       ▼
5..8. Same as Flow A steps 3–8 (tools → tx → signer → response).
```

### Flow C — startup permission check (warn / refuse)

```text
1.  cmd.main → config.Parse(os.Args) → Config
2.  obs.Init(cfg.LogLevel)
3.  warn1, err1 := fsperm.Check(cfg.KeystorePath, cfg.StrictPerms)
    warn2, err2 := fsperm.Check(cfg.PasswordPath, cfg.StrictPerms)
       │
       ├─ err != nil  → log fatal, exit 2
       └─ warn == true (non-strict) → log warning, continue
4.  Continue to step §cmd.4 (wiring)
```

---

## Infrastructure & Deployment

### Deployment Model

- **Single Go binary**, produced by `go build` in `apps/eth-signer-mcp` —
  output to `bin/eth-signer-mcp` per the monorepo Makefile.
- **No containerization required for v1.** A developer drops the binary into
  their MCP client config; the client launches it as a stdio subprocess.
  Optional containerisation is fine — it just needs the keystore+password
  files mounted read-only and a port published if running HTTP, neither of
  which adds module boundaries.
- **One process, two transports, picked at launch.** No daemon manager
  needed.
- **Static binary by default.** `go build -trimpath` and `-buildvcs=true`
  expose `runtime/debug.ReadBuildInfo` for the `--version` output. (Build
  flags are a `cmd/`-level concern; they do not affect module boundaries.)

### Scaling Strategy

- **Vertical scale only.** This is a developer tool; the unit of work is one
  `sign_transaction` call. There is no fan-out, no cache, no job queue.
- **Concurrency:** the MCP SDK serializes per-session; multiple HTTP sessions
  are isolated via per-session transports (SDK). Inside one session, signing
  calls are sequential — `signer.Sign` is goroutine-safe per call because it
  holds no shared mutable state.
- **Per-request resource ceiling:** one file read of each keystore + password
  (≤ a few KB) and one scrypt decrypt. The PRD's p50 < 50 ms after first call
  is achieved by *not* caching — every call is fresh by design.

### Service Extraction Path

Even though the v1 binary is a modular monolith, every module is drawn so
that extraction to a standalone service is a refactor, not a rewrite.

| Module      | Extraction readiness | Notes                                                                                                                                                                                  |
|-------------|----------------------|----------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------|
| `config`    | Keep in process      | Not a service candidate; it's bootstrap glue.                                                                                                                                          |
| `secret`    | Promote to `libs/`   | First time a second app needs the `Secret[T]` wrapper, lift to `libs/secret`. No service.                                                                                              |
| `fsperm`    | Promote to `libs/`   | Same as `secret`.                                                                                                                                                                      |
| `keystore`  | **Ready** for extraction | The `Loader` interface already abstracts file access; replacing it with an HSM/KMS-backed `Loader` becomes the natural v2/v3 path without touching `signer`. The interface is the seam. |
| `tx`        | Promote to `libs/`   | The tx-build logic is reusable across other Ethereum tools (broadcaster, simulator). Lift to `libs/ethtx` whenever a second consumer appears.                                          |
| `signer`    | **Ready** to be the leaf service when keystore extraction lands. Until then keep in-process.                                                                                                              |
| `tools`     | Keep in process      | This is the MCP wire layer; lives with whichever process exposes the tool.                                                                                                             |
| `authz`     | Keep in process      | Bearer-token compare is per-process.                                                                                                                                                   |
| `transport` | Keep in process      | Same as `tools`.                                                                                                                                                                       |
| `obs`       | Promote to `libs/`   | Once a second app exists.                                                                                                                                                              |
| `cmd`       | Keep in process      | By definition.                                                                                                                                                                         |

**The path is unambiguous:** extract `signer + keystore` behind a localhost
gRPC if/when a v2 grows multiple front-ends; promote `secret`, `fsperm`,
`tx`, `obs` to `libs/` the first time a second app appears in the monorepo.

---

## Technology Choices

| Concern             | Choice                                                                       | Rationale                                                                                                                                                       |
|---------------------|------------------------------------------------------------------------------|-----------------------------------------------------------------------------------------------------------------------------------------------------------------|
| Language            | Go 1.26                                                                      | Matches the monorepo's `go.work` toolchain (`go.work` declares Go 1.26).                                                                                        |
| CLI framework       | `urfave/cli` (v2)                                                            | PRD explicit choice; not separately researched (no objections).                                                                                                 |
| MCP framework       | `github.com/modelcontextprotocol/go-sdk` pinned to `v1.6.x` (latest `v1.6.1`)| Official SDK; backward-compat commitment from v1.0.0; supports stdio + Streamable HTTP with built-in DNS-rebinding protection. (Research `01`.)                  |
| Signing primitives  | `github.com/ethereum/go-ethereum` pinned to `v1.17.3`                        | Per research overview §1; planned bump when DoS-only advisories are patched (not exploitable in our offline path). (Research `00` §3 + `02`.)                   |
| Schema inference    | SDK-embedded `jsonschema-go` via `mcp.AddTool[In, Out]`                       | Gives `additionalProperties: false` for free → "strict schema, unknown fields rejected." (Research `01`.)                                                       |
| Logging             | `log/slog` (stdlib)                                                          | No third-party logger; PRD `P1-OBS-1` says structured JSON.                                                                                                     |
| Constant-time cmp   | `crypto/subtle` + `crypto/sha256`                                            | SHA-256 both inputs before constant-time compare to dodge the length leak. (Research `03`.)                                                                     |
| Secret zeroing      | `clear` builtin + `runtime.KeepAlive`; geth-pattern `clear(k.D.Bits())`      | Documented Go idiom; best-effort, with caveats acknowledged. No `memguard`. (Research `02`/`03`.)                                                               |
| Storage             | None — files on disk only.                                                   | PRD: no databases.                                                                                                                                              |
| Test oracle         | `cast mktx` (Foundry, version-pinned) + ethers v6 (one-off fixture gen)      | Per research `04`; fixtures committed under `internal/signer/testdata/vectors/`. CI never invokes external tools.                                                |
| Container / package | None for v1.                                                                  | Single static binary suits the developer-tool use case; containerisation is opt-in.                                                                              |

---

## ADRs (Architecture Decision Records)

### ADR-001: Single Go module, modular packages under `internal/`

- **Status:** Accepted.
- **Context:** PRD scopes one binary with a tight surface; monorepo
  conventions favour adding cross-cutting code to `libs/` only when a
  second consumer needs it. The research overview asks for "small,
  auditable, single afternoon."
- **Decision:** Implement the whole app as one Go module
  (`apps/eth-signer-mcp`) with module boundaries enforced via package
  layout under `internal/`. Defer extracting anything to `libs/` until the
  second app exists.
- **Alternatives Considered:**
  1. Multi-module split inside `apps/eth-signer-mcp` (`apps/eth-signer-mcp/keystore`,
     etc., each its own `go.mod`). Rejected: heavyweight for a single app;
     `go.work` clutter; no shared consumer yet.
  2. Pre-emptive lift to `libs/secret`, `libs/ethtx`, etc. Rejected: YAGNI —
     no second app exists yet; lifting later is a one-PR move because
     packages already have clean interfaces.
- **Consequences:** Faster iteration in v1; clear extraction path documented
  above. Risk: future contributors might cross-import between `internal/`
  packages illegally. Mitigation: lint check + module-dependency graph
  documented here.

### ADR-002: One MCP server, two transports, picked at runtime

- **Status:** Accepted.
- **Context:** PRD `P0-MCP-2` requires identical tools/schemas across stdio
  and HTTP. Research `01` confirms the SDK supports building the server
  once and running it under different transports.
- **Decision:** Construct `*mcp.Server` once in `cmd`, call
  `tools.Register(server)` once, then dispatch to `transport.NewStdio` or
  `transport.NewHTTP` based on `cfg.HTTP`.
- **Alternatives Considered:** Two separate servers (one per transport) —
  rejected; doubles the risk of schema/handler drift.
- **Consequences:** A single registration code path; trivially proven that
  the tool surface is identical.

### ADR-003: Tool-level errors use `CallToolResult.SetError`, never non-nil Go error

- **Status:** Accepted.
- **Context:** Returning a non-nil Go error from the handler aborts JSON-RPC
  semantics instead of giving the client a structured `isError:true` result.
  (Research `01` §Common Pitfalls.)
- **Decision:** All PRD error codes (`invalid_input`, `chain_id_mismatch`,
  `unsupported_type`, `keystore_error`, `password_error`, `internal_error`)
  flow through `CallToolResult.SetError(err)` with a nil Go error. Non-nil
  Go error is reserved for true protocol/system failures.
- **Alternatives Considered:** Mapping every error into a Go error and
  letting the SDK render it — rejected for the wrong-error-class reason
  above.
- **Consequences:** `tools/errors.go` is the single place errors cross the
  domain → MCP wire boundary; the boundary is easy to audit and
  unit-test.

### ADR-004: `--chain-id` guard runs in `tx`, not `signer`

- **Status:** Accepted.
- **Context:** PRD `P0-CLI-5`: a mismatched chainId must reject before any
  key material is touched.
- **Decision:** `tx.Builder.Build(in, chainIDGuard)` enforces the guard;
  `signer.Sign` is only called when validation has already passed.
- **Alternatives Considered:** Guard inside `signer` — rejected: violates
  the "no key touch on bad input" rule and forces `signer` to know about
  CLI flags.
- **Consequences:** A `chain_id_mismatch` tool error returns without
  loading the password file. Compliant with the PRD letter.

### ADR-005: Bind HTTP to 127.0.0.1 + bearer-token middleware in front of SDK handler

- **Status:** Accepted.
- **Context:** PRD `P0-SEC-5`. Research `01` flags that the SDK's
  `StreamableHTTPOptions.CrossOriginProtection` is deprecated and recommends
  ordinary middleware instead.
- **Decision:** `transport.NewHTTP` binds to `127.0.0.1`, leaves
  `DisableLocalhostProtection=false` (default; SDK's DNS-rebinding 403),
  and wraps `StreamableHTTPHandler` with `authz.Middleware`. Bearer-token
  compare uses SHA-256 + constant-time.
- **Alternatives Considered:** mTLS — rejected as overkill for local
  automation; no auth — rejected as a footgun even on localhost; SDK
  deprecated CrossOriginProtection — rejected per SDK guidance.
- **Consequences:** Three layers of HTTP hardening (bind, DNS-rebind,
  bearer); each independently auditable.

### ADR-006: No `memguard` in v1; best-effort `clear` + `runtime.KeepAlive`

- **Status:** Accepted.
- **Context:** PRD-stated threat model excludes swap-file capture and
  root-level adversaries on the same host. Research `03` documents the
  limits of Go's memory-erasure model.
- **Decision:** Use the `clear` builtin and the `Secret[T]` wrapper; do not
  pull in `memguard` / `mlock`. Document the best-effort caveat.
- **Alternatives Considered:** `memguard` — rejected for footprint and
  cgo-adjacent operational complexity; would only matter if the threat
  model widens.
- **Consequences:** A single-afternoon-auditable hygiene story; the gap is
  documented and not hidden.

### ADR-007: A package-level test forbids HTTP/RPC client imports inside `signer`

- **Status:** Accepted.
- **Context:** PRD §Security: "structurally enforced by not importing
  any HTTP/RPC client packages in the signing module."
- **Decision:** `internal/signer/offline_test.go` runs `go list -deps`
  (or walks `go/build.Import`) for `internal/signer/...` and fails the
  build if any of `net/http` (client side), `net/rpc`, or geth's
  `ethclient`/`rpc` are reachable.
- **Alternatives Considered:** Linter rule — rejected: a go-build-time
  test is harder to silently disable than a lint config.
- **Consequences:** A future contributor cannot accidentally re-introduce
  a network round trip into the signer; the offline guarantee is a
  build-time property, not a code review hope.

### ADR-008: Inter-module communication is plain Go function calls (no event bus, no queue)

- **Status:** Accepted.
- **Context:** All work happens inside one synchronous
  `tools/call` → `tx.Build` → `signer.Sign` → response path; there is no
  side effect to fan out asynchronously.
- **Decision:** No message bus, no goroutine workers, no channels at module
  boundaries. Modules expose Go interfaces; `cmd` wires them.
- **Alternatives Considered:** Embedding an in-process event bus to be
  "microservice-ready" — rejected as premature; the interface seams are
  already the extraction path.
- **Consequences:** Easy to reason about; no concurrency hazards inside the
  signer's secret-handling window; tracing is a single stack frame.

---

## Architecture Quality Checklist

- [x] **No circular dependencies between modules** — verified by the
      dependency graph above; `obs` and `secret` are leaves; `cmd` is the
      only cross-importer.
- [x] **Each module has a single, clear responsibility describable in one
      sentence** — see §Module Overview.
- [x] **No shared databases** — there are no databases at all in v1.
- [x] **All inter-module communication goes through defined interfaces** —
      every module exposes a small Go interface; concrete types live behind
      the interface.
- [x] **Every module can be tested in isolation with mocked dependencies** —
      e.g. `tools` tested with stub `Signer`/`Builder`; `transport` tested
      with a stub MCP server; `keystore` tested with `os.CreateTemp`.
- [x] **Cross-cutting concerns are standardized, not reimplemented per
      module** — `secret`, `obs`, `fsperm`, `authz` are the four cross-
      cutting modules.
- [x] **Failure modes are defined** for every module — see each §Failure
      Modes subsection.
- [x] **Service extraction path is clear** — see §Service Extraction Path
      table.
- [x] **Data flow is traceable** — three diagrams in §Data Flow Diagrams.
- [x] **Module count is justified** — 11 packages including `cmd`. Each
      maps to one PRD requirement cluster (signing, schema validation,
      tool registration, transport, secret hygiene, file permission, HTTP
      auth, observability, configuration). Splitting further would create
      tiny single-function packages; merging further would reintroduce
      shared mutable state across responsibilities (e.g. merging `signer`
      and `tx` would make the parity-vector tests touch key material
      unnecessarily).

---

## Open Questions

These are architectural questions that the PRD already flags as open or
that surface naturally from the module breakdown. None block this
candidate from being implementable as written.

- **`urfave/cli` v2 vs v3.** The PRD says `urfave/cli`; v3 is the current
  major. Default: pin v2 because the API surface in v3 is still
  stabilising; revisit at implementation start.
- **Go module name for `apps/eth-signer-mcp`.** PRD-suggested
  `github.com/rootwarp/blockchain-ai-tools/apps/eth-signer-mcp`. Adopt
  verbatim unless renamed.
- **`get_address` register at P0 or P1?** Architecture supports both; PRD
  classes it P1. Default: register at P1 (Phase 4) per PRD §Milestones.
- **Build-info for `--version`.** Use `runtime/debug.ReadBuildInfo` (no
  ldflag plumbing) — fine for `go build` developer flow. Revisit if we
  ever publish reproducible builds.

## Risks

- **R1 — go-ethereum v1.17.3 open DoS advisories.** Not exploitable in this
  signer (no p2p, no RPC). Mitigation: pin v1.17.3 now; bump to a patched
  v1.17.x as soon as one ships; the ADR-007 import-forbid test
  structurally prevents accidental re-introduction of the affected paths.
- **R2 — best-effort memory erasure.** Documented limit (research `03`);
  not a v1 mitigation gap because the threat model excludes the
  adversaries who'd exploit it (root, kernel, side channel).
- **R3 — Foundry stdout drift breaks fixture regen.** Mitigated by pinning
  the Foundry tag in `tools/regen-vectors.sh` and committing the captured
  `cast --version` next to the fixtures. CI never invokes Foundry.
- **R4 — MCP SDK upgrade breaks tool registration shape.** Mitigated by
  the v1.0.0 backward-compat commitment and a pin to `v1.6.x`. The
  `tools` module's tests would catch a breaking change at upgrade time.
- **R5 — A future contributor adds an HTTP/RPC client to `signer`.**
  Mitigated by the ADR-007 build-time test.
- **R6 — `slog` reflection leaks via a nested-struct embed of `Secret`.**
  Mitigated by the documented usage rule + the leak-scanning test in
  `secret` and `obs`. A code review reviewer should still flag any
  `slog.Info("event", "thing", structWithSecretField)` pattern at PR time.
