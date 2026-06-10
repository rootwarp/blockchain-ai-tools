# Software Architecture: `eth-signer-mcp`

> Final architecture (rev 2 — full simplification). The implementation
> contract for the first app in the `blockchain-ai-tools` monorepo: a
> **deliberately small modular monolith** — one Go binary, four packages
> (a `cmd` composition root plus three `internal/` packages) — with the
> security-critical boundary (nothing in the signing package may reach
> the network) enforced at build time. Where this document and the
> superseded 11-package revision disagree, this document wins.

## Overview

`eth-signer-mcp` is a single Go binary that exposes an offline Ethereum
signer as a Model Context Protocol (MCP) server. It loads a Web3 Secret
Storage keystore (single account) referenced by CLI flags, accepts a
fully-specified transaction over MCP, decrypts the key at the moment of
signing, returns the broadcast-ready RLP plus `{r, s, v}`, `hash`, and
`from`, and zeroes secret material immediately (best-effort; ADR-009).
It ships **two transports** (stdio default, Streamable HTTP on `--http`)
over **one tool surface** (`sign_transaction`, `get_address`); both
transports register the exact same tools, schemas, and handlers.

The package layout is the **full simplification** of the earlier
11-package design: `cmd/eth-signer-mcp` (flags, permission checks,
composition root), `internal/signing` (everything that touches key
material), `internal/server` (MCP tools + transports + auth),
`internal/obs` (JSON `slog`, redaction rules, build info). The SDK's
typed tool structs **are** the wire contract — there is no DTO/adapter
layer. Responsibilities and allowed imports: §Module Overview.

Secret-bearing types use a **callback-shaped port** (`WithSigningKey(fn)`)
so that the zero-after-use rule is encoded in the type system, not
delegated to caller discipline. Two structural invariants are enforced
**at build time**: (1) `internal/signing` does not import — directly or
transitively — any HTTP/RPC client package (`net/http`, `net/rpc`,
go-ethereum's `ethclient`/`rpc`), and never imports `internal/server`;
(2) package-level import edges respect §Module Dependency Graph — only
`cmd` imports all packages. Both are enforced via a slim `depguard`
config (ADR-008) plus a Go test that walks the import graph (ADR-007).
The depguard rules are package-level edges only; interface-vs-concrete
discipline is code-review enforced, because depguard cannot see symbols.

## Architecture Principles

Project-specific principles, on top of the defaults (small modules,
single responsibility, loose coupling, clear data ownership):

- **Offline by construction.** No code in `internal/signing` imports an
  HTTP/RPC client; the only `net/http` use anywhere is the MCP HTTP
  *server* in `internal/server`. Enforced by a build-time import test
  (ADR-007) plus depguard (ADR-008); PRD `P0-SIGN-5`, `P0-SEC-6`.
- **Secrets confined by type, not by discipline.** Password bytes and
  the decrypted key are exposed only through the callback-shaped
  `WithSigningKey(fn)` port; the vault owns the lifetime and zeroes when
  `fn` returns — including on panic. Raw key bytes never cross a package
  boundary (ADR-003, ADR-009; PRD `P0-SEC-1/2`).
- **One tool surface, two transports.** Tools are registered against
  one `*mcp.Server` once; `cmd` picks stdio or Streamable HTTP at
  runtime (ADR-002; PRD `P0-MCP-2`).
- **The typed tool structs are the wire contract.** `signing.TxRequest`
  and `signing.SignResult` carry the `json`/`jsonschema` tags that drive
  schema inference via `mcp.AddTool`. No DTO layer, no adapter file (old
  ADR-011 superseded); struct tags are plain strings, so `signing`
  carries the wire shape without importing the MCP SDK.
- **Strict input schema, structured tool errors.** Schema inference
  yields `additionalProperties: false`; tool errors flow through
  `CallToolResult.SetError` — `IsError = true` with compact
  `{"code","message"}` JSON in `Content[0]` (ADR-004); non-nil Go error
  is reserved for protocol/system failures.
- **Package count earns its keep.** Four packages, each with a
  one-sentence responsibility. Speculative seams (DTO adapters,
  JSON-clean job envelopes, extraction-path scaffolds) were removed
  because no second consumer exists.
- **Every phase ends with an in-phase polish pass.** No separate polish
  phase: each phase closes with an explicit refactor/simplify, lint, and
  docs touch-up task, so the code stays clean continuously instead of
  accruing a cleanup backlog.
- **No circular dependencies** — verified in §Module Dependency Graph,
  enforced by ADR-008's depguard config.

## Assumptions

Recorded inline per the PRD's instruction; each is lifted from the PRD,
the research overview (`plan/research/00-overview.md`), or the locked
rewrite brief.

- **App location & name.** Binary at `apps/eth-signer-mcp/`, Go module
  `github.com/rootwarp/blockchain-ai-tools/apps/eth-signer-mcp`, Go
  toolchain 1.26. Single Go module, no shared `libs/` yet — everything
  lives in this app's `internal/` tree until a second consumer appears.
- **Versions pinned.** MCP Go SDK `v1.6.1` (server and test client),
  go-ethereum `v1.17.3`, urfave/cli v3, golangci-lint v2. go-ethereum
  v1.17.3 is **not** affected by GO-2026-4314/-4315/-4507/-4508/-4511
  (all fixed ≤ v1.17.0); `govulncheck` runs in CI for future advisories.
- **CI exists from Phase 1:** a GitHub Actions workflow running
  `make lint`, `make test`, `make build`, `govulncheck`, and a
  `GOOS=windows` compile check. Every "in CI" claim refers to it.
- **MCP error model** per ADR-004; **HTTP hardening + resource bounds**
  per ADR-006.
- **Latency.** Signing computation excluding the keystore KDF is
  sub-millisecond; end-to-end latency is dominated by scrypt and paid on
  **every** call because decrypted key material is never cached: ~0.5–1 s
  (standard scrypt, N=262144) / ~50 ms (light scrypt, N=4096). Full
  statement and benchmark contract in ADR-010.
- **Keystore lifecycle.** The keystore JSON and its address are a
  boot-time snapshot (read eagerly, fail fast); the password file is
  re-read on every signing call, so password rotation works without
  restart; rotating the keystore file requires a restart; a mid-run
  decrypt failure returns `password_error`.
- **Transaction scope.** Legacy (type 0, EIP-155) and EIP-1559 (type 2)
  only. EIP-2930 (type 1), EIP-4844 (type 3), and EIP-7702 (type 4) are
  excluded **by decision**, listed in the P2 backlog. Empty `accessList`
  accepted, non-empty rejected. `chainId = 0` rejected (`invalid_input`)
  — no replay-unprotected Homestead signatures.
- **EIP-55 rule.** A mixed-case `to` address must pass checksum
  validation (failure → `invalid_input`); all-lowercase / all-uppercase
  inputs are accepted checksum-agnostic; outputs always checksummed.
- **Tool surface.** `sign_transaction` and `get_address` both land in
  Phase 2; output includes `hash` and `from` from day one.
- **`--strict-perms` wired once,** in `cmd`, with `cfg.StrictPerms`,
  from Phase 1 (warn by default, refuse with the flag). No re-wiring.
- **Per-call decryption only:** no cache of decrypted keys across calls
  (ADR-010); decrypts serialized by a semaphore of 1 (ADR-006).

---

## System Context Diagram

```text
                        (filesystem, read briefly, never written)
                         ┌─────────────────────────────────────┐
                         │  keystore.json  (Web3 Secret Storage)
                         │  password.txt   (trailing-\n stripped)
                         │  token.txt      (HTTP transport only)
                         └─────────────────────────────────────┘
                                          │ keystore read at boot;
                                          ▼ password re-read per signing call
   ┌────────────┐   stdio (default)   ┌────────────────────────┐
   │ MCP Client │ ◀──────────────────▶│    eth-signer-mcp      │
   │ e.g. Claude│                     │  (single Go binary)    │
   │   Desktop  │                     │                        │
   └────────────┘                     │  - MCP server          │
   ┌────────────┐  Streamable HTTP    │  - stdio + HTTP        │
   │ Local      │  bearer-token,      │  - offline signer      │
   │ Automation │◀───127.0.0.1───────▶│  - no outbound network │
   │ (script)   │                     └──────────────┬─────────┘
   └────────────┘                                    ▼ (stderr only)
                                            structured JSON logs

   No external API calls.  No DB.  No broadcaster.  No RPC.
```

---

## Module Overview

Modules are Go **packages** inside the single `apps/eth-signer-mcp` Go
module: a `cmd` composition root plus three `internal/` packages.

| Package              | Responsibility                                                                                                     | Owns Data / State                                          | Allowed internal imports          |
|----------------------|--------------------------------------------------------------------------------------------------------------------|------------------------------------------------------------|-----------------------------------|
| `cmd/eth-signer-mcp` | Composition root: CLI flags → local `config` struct, file-permission checks, wiring, signal handling, run loop.    | the parsed `config` value (frozen after parse)             | `signing`, `server`, `obs` (all)  |
| `internal/signing`   | Everything that touches key material: secret wrapper + zeroing + leak-scan sentinels; keystore vault; tx parse/build/validate; signer orchestration; tool-error taxonomy; signing audit log line. | keystore-JSON snapshot (ciphertext, boot-time); per-call ephemeral state; decrypt semaphore | **none** (stdlib + go-ethereum only) |
| `internal/server`    | MCP integration: `*mcp.Server` construction, tool registration/handlers, error wire-encoding, stdio + Streamable HTTP transports, bearer auth, request logging, resource bounds. | per-session state managed by the SDK; SHA-256 of the expected bearer token | `signing`, `obs`                  |
| `internal/obs`       | `slog` bootstrap (JSON to stderr, `--log-level`), redaction rules, build info for `--version`.                      | the constructed `*slog.Logger`                             | **none**                          |

**Package count justification.** Four packages map to four concern
clusters: composition (`cmd`), key material + transaction correctness
(`signing`), MCP/network surface (`server`), logging (`obs`). The
previous design split `signing` into five packages and `server` into
three; every one of those seams was interface-plumbing between files
that always change together. The one load-bearing boundary — *the
signing path must never reach the network* — is still a package
boundary, and it is the one enforced mechanically. **`config` is a
`cmd`-local struct, not a package:** parsed once, validated, fields
passed to constructors; no internal package imports a config type.

---

## Module Dependency Graph

Arrows go *toward dependencies*; no cycles:

```text
                 ┌─────────────────────┐
                 │ cmd/eth-signer-mcp  │   (composition root)
                 └──────┬──────┬───────┘
            ┌───────────┘      │      └───────────┐
            ▼                  ▼                  ▼
   ┌─────────────────┐  ┌─────────────┐   ┌─────────────┐
   │ internal/server │─▶│  internal/  │   │ internal/   │
   │  (MCP + HTTP)   │  │   signing   │   │    obs      │
   └────────┬────────┘  └─────────────┘   └─────────────┘
            └──────────────────────────────────▲
                    (server → obs: permitted edge)

   internal/signing and internal/obs depend on NOTHING internal
   (stdlib + go-ethereum / stdlib only).
```

### Verified dependency rules

- **`cmd` imports all packages** — the only package allowed to; it
  constructs every concrete type and wires them together.
- **`server` imports `signing` and `obs`** — `signing` for the typed
  tool structs, the `Signer`, and the `ToolError` taxonomy; `obs` is a
  permitted edge (in practice the logger arrives injected).
- **`signing` imports nothing internal** — preferred and achieved: its
  logger is an injected stdlib `*slog.Logger`, and the `request_id` it
  logs comes from context helpers it defines itself. It **never**
  imports `internal/server` or any HTTP/RPC client package — enforced
  by ADR-007's import-graph test and ADR-008's depguard rules.
- **`obs` imports nothing internal.** Its leak-scan *test* imports
  `signing`'s sentinel helper — a test-only edge, no production
  dependency, no cycle.

Result: a DAG with `cmd` as the sole root, `signing` and `obs` as
leaves. The depguard config (ADR-008) encodes exactly these edges.

---

## Module Details

### Package: `internal/signing`

**Responsibility:** Own everything that touches key material and
everything that determines what gets signed: secret-hygiene primitives,
keystore vault, transaction parse/build/validate, signing orchestration.
Never touches the network; never imports another internal package.

**Data store:** read-only filesystem. The keystore JSON and its address
are a boot-time snapshot (read eagerly, fail fast); the password file is
re-read on every signing call, so password rotation works without
restart; rotating the keystore file requires a restart; a mid-run decrypt
failure returns `password_error`. No plaintext secret is cached across
calls.

**Public API:**

```go
package signing

// --- secret hygiene ---

// Secret[T] is a generic redacting wrapper implementing fmt.Stringer,
// fmt.GoStringer, fmt.Formatter, json.Marshaler, slog.LogValuer.
type Secret[T any] struct{ /* unexported */ }
func NewSecret[T any](v T) Secret[T]
func (s Secret[T]) Expose() T // the only read path

func ZeroBytes(b []byte)    // clear(b) + runtime.KeepAlive(b)
func ZeroBigInt(n *big.Int) // clear(n.Bits()) + KeepAlive

// Sentinel is the leak-scan helper: a fixture secret plus its derived
// encodings (lower/upper hex, base64, decimal scalar rendering).
// New secret types must register their encoded forms.
type Sentinel struct{ Name string; Forms [][]byte }
func NewSentinel(name string, raw []byte) Sentinel
func (s Sentinel) Scan(output []byte) []string // names of leaked forms

// --- keystore vault ---

type KeyVault interface {
    // Address: account address from the boot-time keystore snapshot.
    // Safe to log; does NOT require the password.
    Address() common.Address

    // WithSigningKey re-reads the password file, decrypts the keystore
    // snapshot (serialized by an internal semaphore of 1; ctx checked
    // before the KDF starts), hands a sealed SigningKey to fn, and
    // best-effort zeroes all secret material before returning —
    // including on panic. The SigningKey MUST NOT escape fn.
    WithSigningKey(ctx context.Context, fn func(SigningKey) error) error
}

type SigningKey interface {
    Address() common.Address
    SignTx(tx *types.Transaction, signer types.Signer) (*types.Transaction, error)
}

type VaultOptions struct {
    KeystorePath string // snapshot read at construction (fail fast)
    PasswordPath string // re-read inside every WithSigningKey call
}

func NewFileKeyVault(opts VaultOptions) (KeyVault, error)

// --- transaction wire contract (jsonschema tags drive mcp.AddTool) ---

type TxRequest struct {
    Type                 string     `json:"type"`
    ChainID              string     `json:"chainId"`
    Nonce                string     `json:"nonce"`
    To                   string     `json:"to,omitempty"`
    Value                string     `json:"value"`
    Data                 string     `json:"data"` // ≤512 KiB hex chars (256 KiB bytes)
    Gas                  string     `json:"gas"`
    GasPrice             string     `json:"gasPrice,omitempty"`             // legacy only
    MaxFeePerGas         string     `json:"maxFeePerGas,omitempty"`         // 1559 only
    MaxPriorityFeePerGas string     `json:"maxPriorityFeePerGas,omitempty"` // 1559 only
    AccessList           []struct{} `json:"accessList,omitempty"`           // must be empty in v1
}
// (fields also carry jsonschema tags — patterns, maxLength — elided here)

type SignatureValues struct{ R, S, V string } // json r/s/v; V is yParity (type 2) / EIP-155 v (legacy)

type SignResult struct {
    RawTransaction string          `json:"rawTransaction"`
    Signature      SignatureValues `json:"signature"`
    Hash           string          `json:"hash"`
    From           string          `json:"from"` // always EIP-55 checksummed
}

type AddressResult struct {
    Address string `json:"address"` // always EIP-55 checksummed
}

// --- errors ---

// Code constants CodeInvalidInput … CodeInternalError mirror the six
// stable PRD codes (see §Error Handling).

type ToolError struct {
    Code    string // a stable code; crosses the wire
    Message string // short, non-sensitive; crosses the wire
    Cause   error  // logs-only; never serialized
}
func (*ToolError) Error() string

// --- signer orchestration ---

type SignerOptions struct {
    ChainIDGuard *uint64      // the ONLY home of the guard (from cmd)
    Logger       *slog.Logger // injected; stdlib type, no internal import
}

func NewSigner(vault KeyVault, opts SignerOptions) *Signer

// SignTransaction validates req (presence per type, hex parsing, EIP-55
// rule, chainId != 0, data cap, guard), then decrypts/signs/zeroes
// inside vault.WithSigningKey and encodes the result. Tool-level
// failures return *ToolError; anything else is a system error. Success
// emits the audit line {request_id, tx_hash, chain_id, nonce}.
func (s *Signer) SignTransaction(ctx context.Context, req TxRequest) (*SignResult, error)
func (s *Signer) Address() common.Address

// --- request-id plumbing (defined here so signing imports nothing internal)

func WithRequestID(ctx context.Context, id string) context.Context
func RequestIDFromContext(ctx context.Context) string
```

**Internal structure:**

```
internal/signing/
├── secret.go, zero.go      # Secret[T]; ZeroBytes/ZeroBigInt (+ KeepAlive)
├── sentinel.go             # Sentinel + encoded-forms derivation
├── vault.go, file_vault.go # KeyVault/SigningKey; boot snapshot, semaphore
├── decrypt.go              # password re-read + DecryptKey + deferred zeroing
├── request.go, result.go   # TxRequest / SignResult etc. (wire contract)
├── validate.go             # presence/type, EIP-55, chainId≠0, data cap, guard
├── build.go                # types.LegacyTx / types.DynamicFeeTx construction
├── signer.go, errors.go    # SignTransaction, panic recovery, audit; ToolError
├── context.go              # WithRequestID / RequestIDFromContext
├── offline_test.go         # ADR-007: import-graph forbid test
├── leakscan_test.go        # sentinel scan (raw + encoded forms)
└── parity_test.go + testdata/vectors/  # golden vectors vs cast/ethers v6
```

**Key design decisions:**

- **Sealed `SigningKey` (ADR-003).** `SignTx` is the only operation
  available; no raw-key accessor. When `WithSigningKey` returns,
  deferred zeroing has already run. A future HSM/KMS-backed `KeyVault`
  ("sign with key id X; you do not get the key") drops in unchanged.
- **Validation runs entirely before key material is touched.** Guard
  mismatch, EIP-55 failure, `chainId = 0`, oversized `data`,
  type-inappropriate fields — all return `*ToolError` before
  `WithSigningKey` is called. A test wires a fake vault that panics if
  invoked and asserts no panic on every validation-failure path.
- **The chain-id guard has a single owner:** a `Signer` constructor
  parameter, wired by `cmd` from `--chain-id`. No per-request guard
  field exists.
- **Decrypt semaphore of 1.** Each standard-scrypt decrypt costs
  ~256 MiB; `FileKeyVault` serializes decrypts and checks `ctx` before
  the KDF starts; scrypt is non-cancellable, so the worst case is one
  KDF completing after cancel.
- **Best-effort zeroing (ADR-009):** deferred zeroing of password bytes
  and the key scalar + `runtime.KeepAlive`, including on panic paths;
  Go's runtime may retain transient copies (GC moves, stack copies) —
  documented, not hidden.
- **Panic semantics.** `SignTransaction` wraps orchestration in
  `defer`/`recover`: the vault's inner `defer` zeroes key material
  first, then the recover maps the panic to `internal_error` with a
  redacted log line. The server keeps serving.
- **Audit line.** One info-level line per successful signing:
  `request_id`, `tx_hash`, `chain_id`, `nonce`. The transaction body —
  `calldata`, `to`, `value` — is **never** logged (calldata may be
  operator-sensitive).
- **Offline-import test (ADR-007)** walks `internal/signing`'s
  transitive imports; fails on `net/http`, `net/rpc`, go-ethereum
  `ethclient`/`rpc`.

**Failure modes:**

| Condition | Result |
|---|---|
| Keystore JSON missing/malformed at boot | constructor error; `cmd` exits non-zero (fail fast) |
| Keystore JSON rotated mid-run | not detected by design; restart required (lifecycle contract) |
| Password file missing/unreadable, or `DecryptKey` fails | `password_error`; password bytes + partial key state zeroed via `defer` |
| Schema/field validation failure, EIP-55 failure, `chainId=0`, data > 256 KiB | `invalid_input`; vault never invoked |
| `type` not 0x0/0x2 | `unsupported_type`; vault never invoked |
| Guard mismatch | `chain_id_mismatch`; vault never invoked |
| Recovered sender ≠ vault address | `internal_error` (defensive; should never fire) |
| Panic inside the signing path | inner `defer` zeroes key material → recover → `internal_error`; process keeps serving |
| ctx cancelled before / during KDF | before: `ctx.Err()`, KDF never starts; during: KDF completes (non-cancellable), result discarded and zeroed |

### Package: `internal/server`

**Responsibility:** Everything MCP- and network-facing: build the
`*mcp.Server`, register both tools with `mcp.AddTool` using `signing`'s
typed structs, map `*signing.ToolError` to the wire encoding, run the
chosen transport with the hardening and resource bounds of ADR-006.

**Public API:**

```go
package server

type Options struct {
    Name, Version string // advertised on MCP initialize
    Logger        *slog.Logger
}

// New builds the *mcp.Server and registers sign_transaction and
// get_address against the given signer. One instance serves whichever
// transport is selected.
func New(signer *signing.Signer, opts Options) *Server

func (s *Server) RunStdio(ctx context.Context) error // one session; nil on clean EOF

type HTTPOptions struct {
    Addr          string // default "127.0.0.1:0"; bound addr printed to stderr
    TokenFilePath string // bearer token file; required
}

// RunHTTP binds Addr, wraps the SDK's StreamableHTTPHandler in
// MaxBytesHandler(1 MiB) → request-id/logging → bearer auth, serves
// until ctx is cancelled, then drains via http.Server.Shutdown.
func (s *Server) RunHTTP(ctx context.Context, opts HTTPOptions) error

// BearerVerifier holds the SHA-256 of the expected token (wrapped in
// signing.Secret to keep even the hash out of logs).
func NewBearerVerifierFromFile(path string) (*BearerVerifier, error)
func (v *BearerVerifier) Middleware(next http.Handler) http.Handler
```

**Internal structure:**

```
internal/server/
├── server.go, handlers.go  # *mcp.Server + AddTool registrations; handlers
├── errors.go               # *signing.ToolError → CallToolResult encoding
├── stdio.go, http.go       # RunStdio; RunHTTP (bind, pipeline, shutdown)
├── auth.go                 # BearerVerifier: SHA-256 + ConstantTimeCompare
├── reqlog.go               # request_id generation + HTTP request logging
└── *_test.go               # handler tests (stub signer); hardening matrix
                            # (bind/rebind-403/401/pipeline order); error
                            # wire-encoding contract tests; CONCURRENT-CALLS
                            # integration test (required)
```

**Key design decisions:**

- **Typed tool structs are the wire contract.**
  `mcp.AddTool[signing.TxRequest, *signing.SignResult]` (and the
  `get_address` pair) drive schema inference via the external
  `github.com/google/jsonschema-go` package; `additionalProperties:
  false` falls out (PRD "unknown fields rejected"). No DTOs, no
  adapters (ADR-011 superseded).
- **Error wire encoding (ADR-004):** tool-level failure →
  `IsError = true`, `Content[0]` a `TextContent` whose text is compact
  JSON `{"code":"...","message":"..."}`, nil Go error from the handler;
  E2E tests assert by JSON-parsing `Content[0]`; `Cause` never crosses
  the wire.
- **One `*mcp.Server`, two transports (ADR-002):** tools registered
  once; `cmd` calls `RunStdio` or `RunHTTP`; the SDK spins up a fresh
  per-session transport per Streamable HTTP session.
- **HTTP middleware pipeline, outermost first:** `http.MaxBytesHandler`
  (1 MiB) → request-id + logging → bearer auth (401) → SDK
  `StreamableHTTPHandler` (DNS-rebinding 403). A pipeline-order
  regression test pins this nesting.
- **`request_id`:** UUIDv4 per tool call (or the SDK's id if exposed),
  attached via `signing.WithRequestID`, present in the HTTP request log
  and the signing audit line.
- **Startup prints the bound `host:port` to stderr** (default
  `127.0.0.1:0` — ephemeral port; PRD `P0-CLI-3`).

**Failure modes:**

| Condition | Result |
|---|---|
| Token file unreadable/empty | startup error before the listener binds; `cmd` exits non-zero |
| Bind error | startup error; `cmd` exits non-zero; no token contents logged |
| Missing/wrong bearer token | 401 before the SDK handler sees the body |
| DNS-rebinding Host header | 403 from the SDK handler |
| Request body > 1 MiB | rejected by `MaxBytesHandler` |
| `*signing.ToolError` from a handler | `IsError=true` + `{"code","message"}` JSON; nil Go error |
| Non-ToolError from a handler | non-nil Go error → SDK protocol error (system failure) |
| stdio EOF | clean return; exit 0 |
| ctx cancelled (signal) | `http.Server.Shutdown` drain with timeout, then return |

### Package: `internal/obs`

**Responsibility:** Construct the application logger and expose build
info; declare the project's redaction rules in package documentation
(enforcement lives in `signing` — `Secret[T]`, sentinel scan — and tests).

**Public API:**

```go
package obs

// NewLogger returns a JSON-handler *slog.Logger writing to stderr.
// An unparseable level falls back to info.
func NewLogger(level string) *slog.Logger

type Info struct{ Version, Commit, Date, GoVersion string }

func Build() Info // reads runtime/debug.ReadBuildInfo; no ldflags plumbing
```

**Internal structure:** `log.go` (slog bootstrap), `buildinfo.go`
(`runtime/debug.ReadBuildInfo`), `log_test.go` (leak scan over captured
output at every level; imports `signing`'s Sentinel — test-only edge).

**Key design decisions:** stderr only (stdout stays pristine for MCP
JSON-RPC frames on stdio); JSON handler from day one (`--log-level`,
default `info`); redaction rules live here as documentation, enforced
elsewhere — `signing.Secret[T]` redacts on every print/serialize path,
and the rule "never embed a `Secret` inside a logged struct" (slog
reflects through nested fields, bypassing `LogValue`) is exercised by
the leak-scan tests in both `signing` and `obs`.

**Failure modes:** none — initialization is infallible; bad level parses
to `info`.

### Package: `cmd/eth-signer-mcp`

**Responsibility:** Composition root — parse, check, wire, run.

**Internal structure:** `main.go` (urfave/cli v3 root `*cli.Command`;
signal handling), `config.go` (config struct + cross-field validation),
`fsperm.go` (POSIX check, `Mode().Perm() & 0o077`) with
`fsperm_windows.go` (no-op, build tag), `main_test.go`
(`--help`/`--version`/bad-flag smoke; perms warn vs refuse).

**The local `config` struct:**

```go
type config struct {
    KeystorePath, PasswordPath string
    HTTP                       bool
    HTTPAddr                   string  // default "127.0.0.1:0"
    TokenFilePath              string  // required when HTTP is true
    ChainIDGuard               *uint64 // nil when --chain-id unset
    StrictPerms                bool
    LogLevel                   string  // default "info"
}
```

**`main` sequence:**

1. Parse flags via urfave/cli v3 (`*cli.Command`, context-aware
   `Run(ctx, os.Args)`) into `config`; cross-field validation (`--http`
   requires `--http-auth-token-file`); `--version` prints `obs.Build()`.
2. `logger := obs.NewLogger(cfg.LogLevel)`.
3. Permission checks (wired once, from Phase 1): `checkPerms` on keystore
   and password paths. World-/group-readable → warn by default; refuse
   (exit 2) when `cfg.StrictPerms`. Windows: no-op via build tag.
4. `signing.NewFileKeyVault(...)` (boot-time keystore snapshot, fail
   fast), then `signing.NewSigner(vault, SignerOptions{ChainIDGuard,
   Logger})` — the only place the guard enters the system.
5. `server.New(signer, ...)`, then `RunStdio(ctx)` or
   `RunHTTP(ctx, HTTPOptions{...})` per `cfg.HTTP`.
6. `signal.NotifyContext(SIGINT, SIGTERM)` cancels `ctx` for graceful
   shutdown.

**Failure modes:** bad flags / failed construction / refused perms →
sanitized one-line stderr message, non-zero exit; mid-run transport
error → logged, non-zero exit.

---

## Cross-Cutting Concerns

### Authentication & Authorization

- **stdio transport:** none in-server. The OS process boundary (the MCP
  client launched this subprocess) is the trust boundary; tool-call
  approval is delegated to the client's approval UI (PRD §UX).
- **HTTP transport:** three hardening layers (ADR-006): bind
  `127.0.0.1`; SDK DNS-rebinding 403 on Host mismatch; bearer middleware
  401 before the SDK handler (SHA-256 both tokens +
  `subtle.ConstantTimeCompare`; hashing first neutralizes the
  length-leak short-circuit).
- **No authorization hierarchy:** one keystore account, one principal —
  authenticated callers may sign.

### Logging & Observability

- All logging goes through the injected `*slog.Logger` from
  `obs.NewLogger(level)` — JSON, stderr, level from `--log-level`;
  constructor-injected, never a global. Standard fields: `time`, `level`,
  `msg` (slog's JSON-handler defaults); HTTP adds `request_id`,
  `remote_addr`, `status`, `latency_ms`.
- **Signing audit line:** every successful signing emits exactly one
  info-level line with `request_id`, `tx_hash`, `chain_id`, `nonce` —
  all non-secret. The transaction body — `calldata`, `to`, `value` — is
  **never** logged at any level (calldata may be operator-sensitive).
  The file-based audit log remains P2 (P2-OBS-1).
- **Hard rule:** no secret material at any level. Enforced by
  `signing.Secret[T]`, the "never embed a Secret in a logged struct"
  rule, and leak-scan tests that scan captured logs for the sentinel
  **and its encoded forms** (hex upper/lower, base64, decimal scalar)
  in both `signing` and `obs`.

### Error Handling

Three error tiers:

1. **Tool-level errors** — the stable codes `invalid_input`,
   `unsupported_type`, `chain_id_mismatch`, `keystore_error`,
   `password_error`, `internal_error`, carried as `*signing.ToolError`
   and encoded by `server/errors.go` as `IsError = true` with
   `Content[0]` a `TextContent` whose text is the compact JSON
   `{"code":"<stable_code>","message":"<short non-sensitive message>"}`
   (nil Go error from the handler). **Both `code` and `message` cross
   the wire**; `Cause` is logs-only. E2E tests assert by JSON-parsing
   `Content[0]` (ADR-004).
2. **Protocol/system errors** (transport failure, ctx cancellation,
   unrecovered handler fault) → non-nil Go error propagated to the SDK.
3. **Operational/bootstrap errors** (bad flags, missing files, refused
   perms, bad token file) → returned to `cmd`; sanitized one-line stderr
   message; non-zero exit.

Error messages never echo raw input field values.

### Configuration

- **Sources:** CLI flags only — no env vars in v1 (the PRD wants
  paths-on-disk, not secrets-in-env); no feature flags. Flags per PRD
  P0-CLI-1..6, P1-SEC-1, P1-OBS-1, P1-CLI-1. Flags parse into the
  `cmd`-local `config` struct; constructors receive plain parameters.
- File-content lifecycle: the keystore JSON and its address are a
  boot-time snapshot (read eagerly, fail fast); the password file is
  re-read on every signing call, so password rotation works without
  restart; rotating the keystore file requires a restart; a mid-run
  decrypt failure returns `password_error`. The bearer-token file is
  read once at HTTP startup.

### Concurrency & Lifecycle

- **stdio:** one MCP session per process invocation. **HTTP:** the SDK
  manages per-session transports; sessions may issue concurrent
  `tools/call` requests. There is no shared mutable signing state to
  race on — each call re-reads the password, decrypts, signs, and
  zeroes within one stack frame.
- **Decrypt semaphore of 1.** Signing calls serialize at the vault: each
  standard-scrypt decrypt costs ~256 MiB, so at most one KDF runs at a
  time. The request `ctx` is checked before scrypt starts; scrypt is
  non-cancellable, so the worst case after cancel/shutdown is one KDF
  completing, its output discarded and zeroed. A concurrent-calls
  integration test is **required** in the HTTP phase (ADR-006).
- **Resource bounds:** HTTP bodies ≤ 1 MiB (`http.MaxBytesHandler`);
  `data` ≤ 256 KiB of bytes (512 KiB hex chars) in schema validation.
- **Key-material lifetime is one stack frame:** the `fn` invocation
  inside `WithSigningKey`. A panic in `fn` unwinds through the `defer`
  that zeroes the key (best-effort, ADR-009), is recovered in
  `SignTransaction`, and the server keeps serving.
- **Graceful shutdown:** SIGINT/SIGTERM cancel the root ctx; HTTP drains
  via `http.Server.Shutdown` with a timeout; stdio exits on EOF.

---

## Data Flow Diagrams

### Flow A — stdio `sign_transaction` (happy path)

```text
1.  MCP Client (stdio)  ──  tools/call sign_transaction { ...JSON... }
       ▼
2.  server.RunStdio ──▶ mcp.Server  (SDK validates the inferred schema —
       │   unknown fields rejected — and decodes directly into
       │   signing.TxRequest; no DTO layer)
       ▼
3.  server handler:  request_id := uuid (or SDK-provided)
       │             ctx = signing.WithRequestID(ctx, request_id)
       ▼
4.  signing.Signer.SignTransaction(ctx, req)
       │
       │  validate(req, guard)        // presence per type, hex parse,
       │     │                        // EIP-55, chainId != 0, data cap, guard
       │     ├─ *ToolError → return   // vault NEVER invoked (tested)
       │     └─ built tx + types.Signer
       │
       │  vault.WithSigningKey(ctx, func(key SigningKey) error {
       │       // semaphore (1) acquired; ctx checked before scrypt;
       │       // password file re-read; keystore snapshot decrypted
       │       defer …                // password + key scalar zeroed,
       │                              // incl. panic path (best-effort)
       │       signedTx = key.SignTx(built, signer)
       │       raw, (v,r,s) = signedTx.MarshalBinary(), RawSignatureValues()
       │       if types.Sender(signer, signedTx) != key.Address() → internal_error
       │  })
       │
       │  audit line: INFO {request_id, tx_hash, chain_id, nonce}
       ▼
5.  *signing.SignResult {rawTransaction, signature{r,s,v}, hash, from}
       ▼
6.  mcp.Server writes the JSON-RPC response to stdout → MCP Client
```

### Flow B — Streamable HTTP `sign_transaction` (auth path)

```text
1.  POST /  (Authorization: Bearer XXXX)  from a local script
       ▼
2.  http.MaxBytesHandler (1 MiB)          — oversized body rejected
       ▼
3.  request-id + request-logging middleware
       │  request_id generated; remote_addr/status/latency logged on return
       ▼
4.  bearer middleware — sha256(supplied) vs sha256(expected), constant-time
       │   ├─ fail → 401, request ends (SDK handler never sees the body)
       │   └─ ok   → continue
       ▼
5.  SDK StreamableHTTPHandler — DNS-rebinding check
       │  (DisableLocalhostProtection=false) → 403 on Host mismatch;
       │  fresh per-session transport
       ▼
6.  Same as Flow A steps 3–6.   (This nesting is pinned by a
    pipeline-order regression test; the hardening matrix covers
    bind / 403 / 401 independently.)
```

### Flow C — chain-id guard mismatch (no key material touched)

```text
Client → transport → server handler → signing.SignTransaction
                                          │
                              validate(): guard check fails
                                          │
                        *ToolError{Code: "chain_id_mismatch"}
                                          │
                server/errors.go → CallToolResult{IsError: true,
                    Content[0]: TextContent(`{"code":"chain_id_mismatch",
                                              "message":"..."}`)}
                                          │
                              Client (isError: true)

The vault is NEVER invoked — asserted by a test wiring a fake KeyVault
that panics if WithSigningKey is called. The guard value exists in one
place: the Signer constructor argument wired by cmd.
```

### Flow D — startup permission check (warn / refuse)

```text
1.  cmd: parse flags → config (cross-field validation)
2.  logger := obs.NewLogger(cfg.LogLevel)
3.  checkPerms(cfg.KeystorePath / cfg.PasswordPath, cfg.StrictPerms)
       │   (POSIX: Mode().Perm() & 0o077 != 0 ⇒ too open; Windows: no-op)
       ├─ file missing / not regular   → log fatal, exit 2
       ├─ too open + StrictPerms       → log fatal, exit 2  (refusal)
       └─ too open + !StrictPerms      → WARN line, continue
4.  Wire vault → signer → server; run the selected transport.

Wired once, in Phase 1, with cfg.StrictPerms. No later re-wiring step.
```

---

## Infrastructure & Deployment

### Deployment Model

- **Single Go binary**, produced by `go build` in `apps/eth-signer-mcp`
  — output to `bin/eth-signer-mcp` per the monorepo Makefile. One
  process, two transports, picked at launch; no daemon manager.
- **No containerization required.** A developer drops the binary into
  their MCP client config (stdio subprocess) or runs it locally for HTTP
  automation. Optional containerization (keystore + password mounted
  read-only) changes nothing architecturally.
- **Build flags:** `go build -trimpath` with `-buildvcs=true` so
  `runtime/debug.ReadBuildInfo` feeds `--version`.
- **CI (from Phase 1):** GitHub Actions runs `make lint` (golangci-lint
  v2, incl. depguard), `make test`, `make build`, `govulncheck`, and a
  `GOOS=windows` compile check on every PR.

### Scaling

This is a single-user local tool; the honest scaling answer is **"run
another instance"** (each with its own keystore and port). No fan-out,
no cache, no queue, no extraction path to a service topology — that
section from the previous revision is deliberately gone. Within one
instance, signing calls serialize at the decrypt semaphore; per-call
cost is two small file reads plus one scrypt KDF — ~0.5–1 s
standard-scrypt, ~50 ms light-scrypt; non-KDF overhead < 10 ms (ADR-010).

---

## Technology Choices

| Concern             | Choice                                                                        | Rationale                                                                                                                                                          |
|---------------------|-------------------------------------------------------------------------------|---------------------------------------------------------------------------------------------------------------------------------------------------------------------|
| Language            | Go 1.26                                                                       | Matches the monorepo's `go.work` toolchain.                                                                                                                        |
| CLI framework       | `urfave/cli` v3                                                               | PRD choice; v3 confirmed at the architecture gate (`*cli.Command` root, context-aware `Run`).                                                                       |
| MCP framework       | `github.com/modelcontextprotocol/go-sdk` pinned `v1.6.1` (server + test client) | Official SDK; stdio + Streamable HTTP with built-in DNS-rebinding protection. API-drift risk de-risked by the Phase 1 SDK spike (in-memory transport, HTTP options, middleware order). |
| Signing primitives  | `github.com/ethereum/go-ethereum` pinned `v1.17.3`                            | `crypto`, `accounts/keystore`, `core/types`. **Not** affected by GO-2026-4314/-4315/-4507/-4508/-4511 (all fixed ≤ v1.17.0); `govulncheck` runs in CI for future advisories. |
| Schema inference    | `mcp.AddTool` backed by the external `github.com/google/jsonschema-go/jsonschema` package (`For[T any](opts *ForOptions) (*Schema, error)`) | Typed structs → strict schemas (`additionalProperties: false`). There is no SDK-embedded jsonschema package.                                                        |
| Logging             | `log/slog` (stdlib), JSON handler from day one                                | No third-party logger; PRD `P1-OBS-1`.                                                                                                                             |
| Constant-time cmp   | `crypto/subtle` + `crypto/sha256`                                             | Hash both tokens before `ConstantTimeCompare` to neutralize the length-leak short-circuit.                                                                          |
| Secret zeroing      | `clear` builtin + `runtime.KeepAlive`; geth-pattern `clear(k.D.Bits())`       | Best-effort, caveats documented. No `memguard` (ADR-009).                                                                                                          |
| Storage             | None — files on disk only                                                     | PRD: no databases.                                                                                                                                                 |
| Test oracle         | `cast` (Foundry, pinned via `.foundry-version` — v1.7.1 at time of writing; any single stable tag satisfies the design) + ethers v6 one-off fixture gen | Golden vectors committed under `internal/signing/testdata/vectors/`; CI never invokes external tools.                                                              |
| Lint / boundaries   | golangci-lint v2 with a slim `depguard` config (package-level edges only) + the ADR-007 import-graph test | Two independent gates on the offline invariant; depguard additionally pins the package dependency graph.                                                            |
| Vulnerability watch | `govulncheck` in CI (from Phase 1)                                            | Automated advisory tracking instead of manual claims.                                                                                                              |

---

## ADRs (Architecture Decision Records)

ADR numbers are stable across revisions (other documents cite them);
superseded ADRs keep their slots as tombstones.

### ADR-001: Single Go module, four packages (`cmd` + three `internal/`)

- **Status:** Accepted (revised — previously eleven packages).
- **Context:** PRD scopes one small, auditable binary. The previous
  revision's eleven packages bought interface ceremony, DTO adapters,
  and an extraction path no requirement asked for; the indirection
  made the security-critical path harder to audit.
- **Decision:** One Go module (`apps/eth-signer-mcp`); four packages:
  `cmd/eth-signer-mcp`, `internal/signing`, `internal/server`,
  `internal/obs`. The one boundary that matters — key material never
  meets the network — is a package boundary (`signing` vs `server`),
  mechanically enforced (ADR-007, ADR-008).
- **Alternatives considered:** keep the 11-package layout (rejected:
  seven packages were single-concern files wearing package clothing);
  a single flat package (rejected: the offline invariant would have no
  package boundary to hang its enforcement on).
- **Consequences:** Less ceremony, shorter import graph, the whole
  signing path auditable in one package. If a second app needs
  `Secret[T]` or the tx builder, lifting to `libs/` is a one-PR move.

### ADR-002: One MCP server, two transports (stdio + Streamable HTTP)

- **Status:** Accepted.
- **Context:** PRD `P0-MCP-2` requires identical tools/schemas across
  stdio and HTTP; the SDK supports building the server once and running
  it under either transport. The HTTP transport is MCP **Streamable
  HTTP** — not "HTTP/SSE".
- **Decision:** Construct `*mcp.Server` once in `server.New`, register
  tools once, then `cmd` dispatches to `RunStdio` or `RunHTTP`.
- **Alternatives considered:** two separate servers — rejected; doubles
  the risk of schema/handler drift.
- **Consequences:** One registration code path; the identical tool
  surface is true by construction and checked by a stdio/HTTP parity
  test.

### ADR-003: Sealed `SigningKey` via callback-shaped `WithSigningKey` port

- **Status:** Accepted.
- **Context:** PRD `P0-SEC-1/2` require zero-after-use for password
  bytes and decrypted keys. A getter-style API puts lifetime
  responsibility on the caller; one missed `defer` is a leak.
- **Decision:** `KeyVault.WithSigningKey(ctx, fn)` is the only way to
  obtain signing capability. `fn` receives a sealed `SigningKey` whose
  only operation is `SignTx`; no raw-key accessor exists. The vault
  zeroes password and key material in a `defer` before `WithSigningKey`
  returns — including on panic.
- **Alternatives considered:** return `*ecdsa.PrivateKey` (every holder
  is a leak site); getter + explicit `Close()` (caller discipline).
  Both rejected.
- **Consequences:** Decrypted-key lifetime is one stack frame, by type.
  A future HSM/KMS-backed `KeyVault` drops in without touching consumers.

### ADR-004: Tool errors via `CallToolResult.SetError` + `{"code","message"}` JSON encoding

- **Status:** Accepted (revised — wire encoding now specified).
- **Context:** A non-nil Go error from a handler surfaces a protocol
  error instead of a structured tool result. Separately, the previous
  revision left the wire encoding of `code` unspecified while elsewhere
  claiming "only Message crosses the wire" — a contradiction this
  revision resolves.
- **Decision:** Tool-level failures use `CallToolResult.SetError`:
  `IsError = true` with `Content[0]` a `TextContent` whose text is the
  compact JSON
  `{"code":"<stable_code>","message":"<short non-sensitive message>"}`.
  **Both `code` and `message` cross the wire**; `Cause` is logs-only.
  Codes: `invalid_input`, `unsupported_type`, `chain_id_mismatch`,
  `keystore_error`, `password_error`, `internal_error`. Handlers return
  a nil Go error for tool-level failures; non-nil is reserved for
  protocol/system failures. E2E tests assert by JSON-parsing `Content[0]`.
- **Consequences:** `server/errors.go` is the single audited crossing
  point from domain errors to the wire; clients can branch on `code`.

### ADR-005: ~~`signer.Job` is JSON-clean even in-process~~

- **Status:** **Superseded** by ADR-001 (this revision).
- The JSON-clean `Job` envelope existed to pre-pay for a future
  RPC/queue extraction of the signer. That extraction path is gone; the
  envelope was a premature abstraction adding a third request shape
  between the wire structs and go-ethereum types. Removed — the signer's
  input is `signing.TxRequest` directly.

### ADR-006: HTTP hardening AND resource bounds

- **Status:** Accepted (revised — resource bounds added).
- **Context:** PRD `P0-SEC-5`; plus review findings: a standard-scrypt
  decrypt costs ~256 MiB, so unbounded concurrent signing is a
  self-inflicted memory DoS, and unbounded request bodies amplify it.
- **Decision:** Five enforced mechanisms: (1) bind `127.0.0.1` (default
  `127.0.0.1:0`, bound address printed); (2) SDK DNS-rebinding
  protection on → 403 on Host mismatch; (3) bearer middleware → 401
  before the SDK handler, SHA-256 both tokens +
  `subtle.ConstantTimeCompare`; (4) `http.MaxBytesHandler` at 1 MiB,
  plus `data` ≤ 256 KiB bytes (512 KiB hex chars) in schema validation;
  (5) keystore decrypts serialized via a semaphore of 1, ctx checked
  before scrypt starts. A **concurrent-calls integration test is
  required** in the HTTP phase and may not be waived.
- **Alternatives considered:** mTLS (operator UX tax disproportionate to
  the localhost threat model); no auth on loopback (footgun); the SDK's
  deprecated `CrossOriginProtection` (rejected per SDK guidance).
- **Consequences:** Each layer independently auditable; worst-case
  memory under load is one KDF (~256 MiB) plus queued waiters.

### ADR-007: Build-time test forbids HTTP/RPC client imports in `internal/signing`

- **Status:** Accepted (rescoped to the consolidated package).
- **Context:** PRD `P0-SIGN-5` / `P0-SEC-6`: offline must be structural,
  not reviewed-for.
- **Decision:** `internal/signing/offline_test.go` walks the transitive
  imports of `internal/signing` (via `golang.org/x/tools/go/packages`)
  and fails if `net/http`, `net/rpc`, or go-ethereum `ethclient`/`rpc`
  are reachable. The only `net/http` use in the binary is the MCP HTTP
  *server* in `internal/server`. Phase 1 lands the (initially vacuous)
  scaffold; Phase 4's final sweep re-checks by mutation (temporarily add
  a forbidden import; confirm the test fails).
- **Alternatives considered:** lint rule alone — easier to silently
  disable than a failing Go test; kept only as the second gate (ADR-008).
- **Consequences:** A network round trip cannot be reintroduced into
  the signing path without a red build.

### ADR-008: Slim `depguard` enforcing package-level import edges only

- **Status:** Accepted (rewritten — honest scope).
- **Context:** The previous revision claimed depguard enforced "only
  `cmd` imports concrete types". depguard cannot do that: it sees
  package import paths, not symbols.
- **Decision:** A slim depguard config (in `.golangci.yml`) enforcing
  exactly the package-level edges of §Module Dependency Graph:
  `internal/signing` may not import `internal/server`, `internal/obs`,
  or any HTTP/RPC client package; `internal/obs` may not import any
  internal package; `internal/server` may import only `internal/signing`
  and `internal/obs`; only `cmd/eth-signer-mcp` imports all packages.
  Test packages may additionally import `internal/signing` for the
  sentinel helper. **Interface-vs-concrete discipline is code-review
  enforced** — stated plainly, because depguard cannot see symbols.
- **Alternatives considered:** per-package Go submodules (compiler-level
  enforcement; heavyweight); trust review only (fragile). Both rejected.
- **Consequences:** One small config block; boundary changes require a
  visible allowlist diff. The offline invariant has two independent
  gates (this + ADR-007).

### ADR-009: No `memguard` in v1; best-effort `clear` + `runtime.KeepAlive`

- **Status:** Accepted.
- **Context:** The PRD threat model excludes root/kernel adversaries and
  swap capture. Research documents the limits of memory erasure in Go.
- **Decision:** Deferred zeroing of password bytes and the key scalar
  (`clear` + `runtime.KeepAlive`), including on panic paths, via the
  `Secret[T]` wrapper and the vault's `defer` chain. No
  `memguard`/`mlock`. **Limitation stated honestly:** Go's runtime may
  retain transient copies (GC moves, stack copies); zeroing is
  best-effort, and the observable requirement — no secrets in logs or
  outputs, raw or encoded — is what tests enforce.
- **Alternatives considered:** `memguard` — footprint and operational
  complexity unjustified by the threat model. Rejected.
- **Consequences:** A single-afternoon-auditable hygiene story; the
  gap is documented, not hidden.

### ADR-010: No in-process cache of decrypted key material

- **Status:** Accepted (consequences rewritten — honest latency).
- **Context:** PRD `P0-SEC-2`: the decrypted key lives in memory only
  for one signing operation. A cache would amortize the scrypt KDF but
  put a plaintext key in memory for an unbounded window.
- **Decision:** No cache. Every signing call re-reads the password file,
  decrypts the boot-time keystore snapshot, signs, and zeroes.
- **Alternatives considered:** geth-style `TimedUnlock` — explicitly out
  of scope per the PRD threat model.
- **Consequences:** Signing computation excluding the KDF is
  sub-millisecond, but end-to-end latency is dominated by the keystore's
  scrypt parameters and paid on **every** call: ~0.5–1 s for
  standard-scrypt keystores (geth default, N=262144), ~50 ms for
  light-scrypt (N=4096). There is no warm path — caching is exactly what
  this ADR forbids, and OS page-cache warmth does not help scrypt (the
  cost is CPU/memory work, not I/O). The acceptance benchmark runs both
  parameter sets and asserts the non-KDF overhead (total minus KDF time)
  stays under 10 ms. For fast iteration, use a light-scrypt dev keystore.

### ADR-011: ~~Tools own MCP wire DTOs; thin adapter to/from domain types~~

- **Status:** **Superseded** by ADR-001 (this revision).
- The DTO/adapter layer between MCP wire shapes and domain types is
  removed. The SDK's typed tool structs (`signing.TxRequest`,
  `signing.SignResult`, `signing.AddressResult`) **are** the wire
  contract; schema and domain evolution are the same change, reviewed in
  one place. Golden schema tests pin the published JSON schema so
  accidental wire changes still fail loudly.

---

## Open Questions

None block implementation; each has a chosen default.

- **`request_id` source.** Prefer an SDK-provided request id if exposed
  on `CallToolRequest`; otherwise a UUIDv4 from the server handler.
  Either way it propagates via `signing.WithRequestID`. Resolved in the
  Phase 1 SDK spike.
- **`jsonschema` tag surface.** Exact tag vocabulary (hex patterns,
  `maxLength` for `data`) is confirmed against
  `github.com/google/jsonschema-go` in the Phase 1 spike; whatever tags
  cannot express is enforced in `validate.go` and tested.
- **Lift `Secret[T]` / tx building to `libs/`?** Wait for a second
  consumer; until then everything stays in `internal/signing`.

## Risks

- **R1 — MCP SDK v1.6.x API drift** (typed-tool registration,
  StreamableHTTP options, middleware hooks). Mitigation: pin `v1.6.1`
  for server and test client; the Phase 1 SDK spike de-risks the
  integration before signing code depends on it.
- **R2 — Parity edge cases vs reference signers** (v/yParity, empty
  `data`, contract creation, leading-zero inputs). Mitigation: committed
  golden vectors for both scrypt fixture sets and both tx types;
  byte-identical assertion vs `cast` and ethers v6.
- **R3 — Secret leakage through encoded log forms.** A raw-bytes-only
  scan would miss a hex- or base64-rendered key. Mitigation: the
  sentinel scan covers raw bytes **and** lower/upper hex, base64, and
  the decimal scalar rendering; new secret types register their
  encoded forms; the scan runs in `signing` and `obs` tests.
- **R4 — Operator mis-binds HTTP off localhost.** Mitigation: default
  `127.0.0.1:0`; bearer auth still gates every request; non-localhost
  exposure documented as unsupported; startup logs the bound address.
- **R5 — Scrypt latency surprises operators:** ~0.5–1 s per signing
  call with standard-scrypt keystores, every call, by design (ADR-010).
  Mitigation: stated in README and `--help`; light-scrypt for dev loops.
- **R6 — Best-effort memory erasure.** Go may retain transient secret
  copies (ADR-009). Accepted residual risk: the threat model excludes
  the adversaries who could exploit it; the observable requirement
  (no secrets in logs/outputs) is test-enforced.
- **R7 — `slog` reflection leaks via a nested-struct embed of
  `Secret`.** Mitigation: documented usage rule, leak-scan tests, review.
- **R8 — Foundry output drift breaks fixture regen.** Mitigation:
  Foundry pinned via `.foundry-version`; captured `cast --version`
  committed next to fixtures; CI never invokes Foundry.

---

## Architecture Quality Checklist

- [x] **No circular dependencies.** `cmd → {server, signing, obs}`,
  `server → {signing, obs}`, `signing → ∅`, `obs → ∅`. Enforced by
  depguard (ADR-008); table, graph, flows, and ADRs all state the
  same edges.
- [x] **Each package has a one-sentence responsibility** (§Module
  Overview).
- [x] **No shared databases** — none at all; the only filesystem reads
  are the keystore snapshot + per-call password (`signing`) and the
  token file (`server`, startup).
- [x] **The security-critical boundary is mechanically enforced** — two
  independent build-time gates (ADR-007 test, ADR-008 depguard);
  interface-vs-concrete discipline is code-review enforced, honestly
  labeled.
- [x] **Every package is testable in isolation.** `server` with a stub
  signer + in-memory transport; `signing` with fixture keystores, a
  panicking fake vault, golden vectors; `obs`/`cmd` with captured output.
- [x] **Cross-cutting concerns standardized** (secrets, logging, auth —
  one audit point each); **failure modes defined** (per-package tables).
- [x] **Resource bounds are explicit.** 1 MiB bodies, 256 KiB data,
  decrypt semaphore of 1, required concurrent-calls test (ADR-006).
- [x] **Data flow is traceable.** Four diagrams, all against the
  four-package layout.
- [x] **Package count is justified.** Four packages; the removed seven
  existed for an extraction path this revision deliberately deleted; the
  scaling story for a single-user local tool is "run another instance".
