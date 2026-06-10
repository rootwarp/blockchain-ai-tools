# Project Plan: `eth-signer-mcp`

## Summary

`eth-signer-mcp` is the first app in the `blockchain-ai-tools` monorepo: a
single Go binary that exposes an offline Ethereum signer over the Model
Context Protocol (MCP). The architecture is locked as a deliberately small
modular monolith — **a `cmd` composition root plus three `internal/`
packages** (`internal/signing`, `internal/server`, `internal/obs`; see
[`architecture.md`](./architecture.md)). The implementation is split into
**four sequential phases**, ~38 working days total, one developer:

1. **Foundations (~7 days)** — scaffold, CI, CLI/config, complete
   observability, secret hygiene, fsperm checks, stdio boot, MCP SDK spike.
2. **Signing core (~14 days)** — keystore vault, tx validate/build, signer
   orchestration, both tools, byte-identical parity vs reference signers.
3. **HTTP transport (~9 days)** — Streamable HTTP, bearer auth, hardening
   matrix, resource bounds, concurrent-calls test, graceful shutdown.
4. **Release (~5 days)** — demos, operator README, final sweep, v1.0.0 tag.

The plan is **single-stream**: one implementer takes the phases in order.
Every build-time gate (depguard, offline-import test, parity goldens, leak
scan with encoded forms) lands in the phase where the rule it enforces
first applies. The dependency story is linear: Phase 2 builds on Phase 1's
skeleton and guard rails; Phase 3 wraps Phase 2's tool surface in the HTTP
transport; Phase 4 closes out v1.

## Planning Principles

Stated up front because they shape every phase below:

- **No separate polish phase.** The old plan's "P1 polish" phase is
  dissolved. Each former polish item is built in the phase where the
  relevant code is first written — complete, once: JSON `slog` +
  `--log-level` + redaction + build info / rich `--version` and the
  fsperm warn/`--strict-perms` refusal in Phase 1; `get_address`, the
  `hash`/`from` output fields, and the per-signing audit line in Phase 2
  as part of the tools from day one; HTTP request logging + `request_id`
  in Phase 3. No text-handler-then-swap step, no `omitempty`-then-enable
  step, no fsperm re-wiring step.
- **Every phase ends with an explicit polish task** (refactor/simplify,
  lint sweep, docs touch-up), so code stays clean continuously instead
  of accruing a cleanup backlog. Tasks 1.10, 2.12, 3.9; in Phase 4 the
  final sweep (4.4) plays that role.
- **De-risk first.** The top schedule risk is MCP SDK API drift
  (typed-tool registration, `StreamableHTTPOptions`, middleware order).
  The SDK spike is therefore a **Phase 1** task — before any signing code
  depends on the SDK — and its findings are committed as a note in-repo.
- **CI is real from Phase 1.** A GitHub Actions workflow (`make lint`,
  `make test`, `make build`, `govulncheck`, `GOOS=windows` compile check)
  is created immediately after scaffolding. Every later "enforced on every
  commit" claim in this plan refers to that workflow.
- **Build only what the architecture contains.** Four packages, no DTO
  adapters, no noop placeholder implementations, no interface-only
  skeleton tasks. Tasks map 1:1 onto `cmd`, `signing`, `server`, `obs`.

## Prerequisites

Things that must be in place before Phase 1 begins:

- **Tooling on the developer machine.** Go 1.26 toolchain, `make`,
  `golangci-lint` v2 on `$PATH`, `git`. For Phase 2 fixture generation
  only: Foundry (`cast`, pinned via `.foundry-version`) and Node.js +
  ethers v6 — used by `scripts/regen-vectors.sh` only; CI never invokes
  them.
- **Repo access.** Push/merge access to `rootwarp/blockchain-ai-tools`,
  including GitHub Actions workflow permissions.
- **Approved artifacts.** PRD ([`prd.md`](./prd.md)), the research docs
  under `research/`, and the architecture
  ([`architecture.md`](./architecture.md), rev 2 — full simplification)
  are accepted and not under active revision.

## Conventions & Locked Decisions

Do not re-litigate during execution:

- **Module & layout.** Single Go module at `apps/eth-signer-mcp/`, import
  path `github.com/rootwarp/blockchain-ai-tools/apps/eth-signer-mcp`,
  Go 1.26. Binary `main` at `cmd/eth-signer-mcp/main.go`; three internal
  packages: `internal/signing`, `internal/server`, `internal/obs`
  (ADR-001).
- **Version pins.** `github.com/modelcontextprotocol/go-sdk` **v1.6.1**
  (server and test client), `github.com/ethereum/go-ethereum` **v1.17.3**,
  `urfave/cli` **v3**, golangci-lint **v2**. go-ethereum v1.17.3 is NOT
  affected by GO-2026-4314/-4315/-4507/-4508/-4511 (all fixed at or
  before v1.17.0); future advisories are tracked by `govulncheck` in CI,
  not by manual claims.
- **Transport naming.** The second transport is MCP **Streamable HTTP**
  (the SDK's StreamableHTTP server). It is never called "HTTP/SSE".
- **Schema inference.** Tool schemas come from `mcp.AddTool` inference
  backed by the external `github.com/google/jsonschema-go/jsonschema`
  package (`For[T any](opts *ForOptions) (*Schema, error)`); there is no
  SDK-embedded jsonschema package.
- **Error wire encoding (ADR-004).** Tool errors: `IsError: true`,
  `Content[0]` a TextContent whose text is compact JSON
  `{"code":"<stable_code>","message":"<short non-sensitive message>"}`.
  Both `code` and `message` cross the wire; `Cause` is logs-only. Codes:
  `invalid_input`, `unsupported_type`, `chain_id_mismatch`,
  `keystore_error`, `password_error`, `internal_error`. E2E tests assert
  by JSON-parsing `Content[0]`.
- **Keystore lifecycle.** Keystore JSON + address are a boot-time
  snapshot (read eagerly, fail fast); the password file is re-read on
  every signing call, so password rotation works without restart;
  rotating the keystore file requires a restart; mid-run decrypt failure
  returns `password_error`.
- **Latency expectations (ADR-010).** Signing computation excluding the
  keystore KDF is sub-millisecond. End-to-end latency is dominated by
  scrypt and paid on **every** call (no decrypted-key cache): ~0.5–1 s
  standard scrypt (geth default, N=262144), ~50 ms light scrypt (N=4096).
  The acceptance benchmark runs both parameter sets and asserts non-KDF
  overhead (total minus KDF time) stays under 10 ms.
- **v1 scope.** Legacy (type 0, EIP-155) + EIP-1559 (type 2) only; types
  1/3/4 excluded by decision, tracked in the P2 backlog. `chainId = 0`
  rejected. Mixed-case `to` must pass EIP-55 checksum validation; outputs
  always checksummed. Single-file keystore; password from a file; stdio +
  Streamable HTTP; tools `sign_transaction` and `get_address`.
- **Chain-id guard single owner.** The `Signer` is constructed with the
  guard value (wired by `cmd` from `--chain-id`); no per-request guard
  field exists.

## Schedule Overview

Task days per phase sum exactly to the phase budget; the project total
carries a ~3-day contingency float on top (35 task days + 3 days float =
~38 working days).

| Phase | Name           | Task days | Day window (nominal) | Cumulative |
|-------|----------------|-----------|----------------------|------------|
| 1     | Foundations    | 7         | Days 1–7             | 7          |
| 2     | Signing core   | 14        | Days 8–21            | 21         |
| 3     | HTTP transport | 9         | Days 22–30           | 30         |
| 4     | Release        | 5         | Days 31–35           | 35         |
| —     | Contingency    | ~3        | absorbed as needed   | ~38        |

## Phase Dependency Map

```text
Phase 1 ──▶ Phase 2 ──▶ Phase 3 ──▶ Phase 4
(skeleton,   (vault, tx,   (Streamable   (demos, docs,
 CI, obs,     signer,       HTTP, auth,   final sweep,
 secret,      both tools,   bounds,       v1.0.0 tag)
 SDK spike)   parity)       shutdown)

Key inter-phase edges: 2.* depends on 1.5 (Secret/Sentinel), 1.7 (SDK
spike), 1.8 (stdio shell), 1.9 (guard-rail scaffolds). 3.* depends on
2.7 (tools/handlers) and 2.6 (signer + request-id plumbing); 3.6 on 2.2
(decrypt semaphore). 4.* depends on everything; 4.3 on 2.1 fixtures.
```

---

## Phase 1: Foundations — scaffold, CI, CLI, obs, secret hygiene, stdio boot, SDK spike

**Goal:** A runnable `eth-signer-mcp` binary that parses all CLI flags via
urfave/cli v3, performs file-permission checks (warn / `--strict-perms`
refuse), logs structured JSON, reports rich build info on `--version`,
and answers MCP `initialize` + an empty `tools/list` over stdio. CI
enforces lint/test/build/govulncheck/Windows-compile from this phase
onward. The MCP SDK surface is de-risked by a spike whose findings are
committed as a note. `internal/obs` is **complete** in this phase — JSON
handler from day one, no later swap.

**Packages touched:** `cmd/eth-signer-mcp` (full), `internal/obs` (full),
`internal/signing` (secret hygiene + sentinel only), `internal/server`
(stdio shell, no tools), repo root (CI workflow, depguard block).
**ADRs:** ADR-001, ADR-002 (stdio half), ADR-007 (scaffold), ADR-008,
ADR-009 (helpers).

**Entry criteria:** Prerequisites met; plan/PRD/architecture approved.

### Tasks

| ID   | Task                                                        | ~Days | Depends on |
|------|-------------------------------------------------------------|-------|------------|
| 1.1  | Scaffold app module + pin dependencies                      | 0.5   | —          |
| 1.2  | CI workflow (lint/test/build/govulncheck/GOOS=windows)      | 1.0   | 1.1        |
| 1.3  | CLI flags + `cmd`-local config struct + validation          | 1.0   | 1.1        |
| 1.4  | `internal/obs` complete: JSON slog, `--log-level`, build info | 0.5 | 1.3        |
| 1.5  | `internal/signing` secret hygiene: `Secret[T]`, zeroing, Sentinel | 1.0 | 1.1     |
| 1.6  | fsperm checks in `cmd`: warn + `--strict-perms` refusal     | 0.5   | 1.3        |
| 1.7  | MCP SDK spike (in-memory transport, HTTP options, middleware order) | 1.0 | 1.1   |
| 1.8  | Stdio MCP server boots (`initialize` + empty `tools/list`)  | 0.5   | 1.4, 1.7   |
| 1.9  | Slim depguard config + offline-import test scaffold         | 0.5   | 1.5        |
| 1.10 | Phase polish pass (refactor/simplify, lint sweep, docs)     | 0.5   | 1.1–1.9    |

### Task notes

- **1.1 — Scaffold app module + pin dependencies.** Run
  `make new-app name=eth-signer-mcp`; verify `go.work` gains the module.
  Relocate the scaffolder's starter `main.go` into
  `cmd/eth-signer-mcp/main.go`; delete the root-level placeholder.
  `go get` the pinned versions: go-sdk `v1.6.1`, go-ethereum `v1.17.3`,
  `urfave/cli/v3` (latest stable v3 patch, pinned exactly),
  `google/jsonschema-go`; `go mod tidy`. `make build` / `make test` green.
- **1.2 — CI workflow.** `.github/workflows/ci.yml` on every PR and push
  to `main`: `make lint` (golangci-lint v2, incl. the depguard block once
  1.9 lands), `make test`, `make build`, `govulncheck ./...`, and a
  `GOOS=windows go build ./...` compile check (exercises the
  `fsperm_windows.go` build tag). The referent of every later "in CI"
  claim.
- **1.3 — CLI flags + config.** urfave/cli v3 root `*cli.Command` with
  context-aware `Run(ctx, os.Args)`. All flags land now: `--keystore`,
  `--password-file`, `--http`, `--http-addr` (default `127.0.0.1:0`),
  `--http-auth-token-file`, `--chain-id`, `--strict-perms`, `--log-level`
  (default `info`). Flags parse into the `cmd`-local `config` struct —
  not a package; no internal package imports a config type. Cross-field
  validation: `--http` requires `--http-auth-token-file`; keystore +
  password file required. Golden flag-set tests; `--help` / bad-flag
  smoke tests.
- **1.4 — `internal/obs` complete.** `NewLogger(level)` returns a
  JSON-handler `*slog.Logger` on stderr (stdout stays pristine for MCP
  frames); unparseable level falls back to `info`. `Build()` reads
  `runtime/debug.ReadBuildInfo` (build with `-trimpath -buildvcs=true`);
  `--version` prints version/commit/date/Go version. Redaction rules
  documented in the package doc. Built complete, once — there is no
  later text→JSON swap.
- **1.5 — Secret hygiene.** `Secret[T]` implementing all five redacting
  interfaces (`fmt.Stringer`, `fmt.GoStringer`, `fmt.Formatter`,
  `json.Marshaler`, `slog.LogValuer`); `ZeroBytes` / `ZeroBigInt`
  (`clear` + `runtime.KeepAlive`). `Sentinel` leak-scan helper deriving
  **encoded forms** of the fixture secret — lowercase/uppercase hex,
  base64, decimal scalar rendering; new secret types must register their
  encoded forms. Leak-scan tests over `fmt`/`json`/`slog` output, plus
  the known-leak anti-pattern test (embedding a `Secret` in a struct
  passed to `slog` leaks via reflection — asserted to make the rule
  visible). `obs`'s log test reuses the Sentinel (test-only edge).
- **1.6 — fsperm checks.** `fsperm.go` in `cmd`: POSIX
  `Mode().Perm() & 0o077` check on keystore and password paths;
  `fsperm_windows.go` no-op behind a build tag. Wired **once**, with
  `cfg.StrictPerms`: world-/group-readable → warn by default, refuse
  (exit 2) with `--strict-perms`. Temp-file + chmod tests cover warn and
  refuse paths. No later re-wiring task exists.
- **1.7 — MCP SDK spike.** Against go-sdk v1.6.1: (a) the in-memory
  transport pattern for tests; (b) the `StreamableHTTPOptions` surface
  actually available (DNS-rebinding/localhost protection knobs, session
  behavior); (c) how `http.Handler` middleware composes around the SDK's
  StreamableHTTPHandler and whether a request id is exposed on
  `CallToolRequest`; (d) `mcp.AddTool` inference behavior with
  `jsonschema-go` tags (`additionalProperties: false`, pattern/maxLength
  support). Findings committed as an in-repo note; resolves the
  architecture's open questions on `request_id` source and tag surface.
- **1.8 — Stdio boot.** `server.New` constructs the `*mcp.Server` (name +
  version from `obs.Build()`); `RunStdio(ctx)` serves one session, returns
  nil on clean EOF. No tools registered yet. Smoke test over the spike's
  in-memory transport: `initialize` round-trips, `tools/list` returns
  empty. `cmd` wires parse → logger → fsperm → server → `RunStdio` with
  `signal.NotifyContext` for SIGINT/SIGTERM.
- **1.9 — depguard + offline-import scaffold.** Slim depguard block in
  `.golangci.yml` enforcing package-level edges only (ADR-008):
  `internal/signing` may not import `internal/server`, `internal/obs`, or
  any HTTP/RPC client package; `internal/obs` imports nothing internal;
  `internal/server` only `signing` + `obs`; only `cmd` imports all; test
  packages may import `signing` for the Sentinel.
  `internal/signing/offline_test.go` walks transitive imports via
  `golang.org/x/tools/go/packages`, failing on `net/http`, `net/rpc`,
  go-ethereum `ethclient`/`rpc` — vacuous now, load-bearing from
  Phase 2. Interface-vs-concrete discipline is code-review enforced —
  depguard cannot see symbols.
- **1.10 — Phase polish pass.** Refactor/simplify, full lint sweep, touch
  up package docs and the repo CLAUDE.md command notes if needed.

### Key risks

- SDK v1.6.1 surprises (spike exists precisely to surface them now, while
  the blast radius is one task, not the signing core).
- urfave/cli v3 idiom drift between patch releases — pinned exactly;
  `--help`/`--version` smoke-tested in CI.

### Exit criteria (testable)

- [ ] `make build` produces `bin/eth-signer-mcp`; `--help` shows all
      flags; `--version` prints version, commit, build date, Go version.
- [ ] Binary answers MCP `initialize` and an empty `tools/list` over
      stdio (in-memory-transport smoke test).
- [ ] Logs are JSON on stderr; `--log-level` honored; leak-scan tests
      pass in `signing` and `obs`, covering the sentinel **and** its
      encoded forms.
- [ ] World-/group-readable keystore or password file warns; with
      `--strict-perms` the process refuses (exit 2). Both paths tested.
- [ ] CI green on `main`: lint (incl. depguard), test, build,
      govulncheck, `GOOS=windows` compile.
- [ ] SDK spike note committed, answering: in-memory transport,
      StreamableHTTPOptions surface, middleware order, request-id
      source, jsonschema tag capabilities.
- [ ] Offline-import test compiles and runs (vacuously green).

---

## Phase 2: Signing core — vault, tx validate/build, signer, both tools, parity

**Goal:** The complete offline signing path, end to end over stdio:
`sign_transaction` and `get_address` registered with full schemas;
validation running entirely before key material is touched; keystore
decrypt-sign-zero inside `WithSigningKey` with panic-safe deferred zeroing
and a decrypt semaphore of 1; the six-code error taxonomy with the locked
`{"code","message"}` wire encoding; one audit line per successful signing;
and **byte-identical RLP parity** against `cast` and ethers v6 on
committed golden vectors including all edge cases.

**Packages touched:** `internal/signing` (everything except the Phase 1
secret files), `internal/server` (tool registration, handlers,
`errors.go`), `cmd` (vault + signer wiring, `--chain-id` guard).
**ADRs:** ADR-003, ADR-004, ADR-007 (now load-bearing), ADR-009, ADR-010,
ADR-006 partially (decrypt semaphore; HTTP-side bounds in Phase 3).

**Entry criteria:** Phase 1 exit criteria all green; SDK spike note
answers the registration/in-memory-transport questions.

### Tasks

| ID   | Task                                                          | ~Days | Depends on |
|------|---------------------------------------------------------------|-------|------------|
| 2.1  | Test keystore fixtures (standard + light scrypt)              | 0.5   | —          |
| 2.2  | Keystore vault: snapshot, `WithSigningKey`, semaphore, zeroing | 2.5  | 2.1        |
| 2.3  | Wire-contract structs: `TxRequest`/`SignResult`/`AddressResult` | 1.0 | —          |
| 2.4  | `validate.go`: presence/type rules, EIP-55, chainId≠0, data cap, guard | 1.5 | 2.3   |
| 2.5  | `build.go`: `LegacyTx` / `DynamicFeeTx` construction          | 1.0   | 2.4        |
| 2.6  | Signer orchestration + error taxonomy + audit line + panic recovery | 2.0 | 2.2, 2.5 |
| 2.7  | Tool registration: `sign_transaction` + `get_address`; error wire encoding | 1.5 | 2.6 |
| 2.8  | Offline-import test load-bearing + depguard verification      | 0.5   | 2.6        |
| 2.9  | Golden parity vectors + regen tooling (`cast` + ethers v6)    | 1.5   | 2.1        |
| 2.10 | Byte-identical parity suite (all edge cases)                  | 1.0   | 2.6, 2.9   |
| 2.11 | Stdio end-to-end test (full binary surface)                   | 0.5   | 2.7        |
| 2.12 | Phase polish pass                                             | 0.5   | 2.1–2.11   |

### Task notes

- **2.1 — Fixtures.** Two throwaway dev keystores under
  `internal/signing/testdata/`: **standard scrypt** (N=262144) and
  **light scrypt** (N=4096), with password files; documented as low-value
  test keys. The light fixture keeps the unit-test loop fast; the
  standard fixture feeds the Phase 4 benchmark. The fixture key doubles
  as the leak-scan sentinel source (encoded forms registered per 1.5).
- **2.2 — Keystore vault.** `NewFileKeyVault(VaultOptions)`: keystore JSON
  + address read eagerly at construction (boot-time snapshot; fail fast on
  missing/malformed). `WithSigningKey(ctx, fn)`: acquire the internal
  **semaphore of 1**, check `ctx` before the KDF starts, re-read the
  password file (trailing-newline stripped) on **every** call, decrypt the
  snapshot, hand a sealed `SigningKey` (only operation: `SignTx`) to `fn`,
  and best-effort zero password bytes + key scalar in a `defer` —
  including on panic (ADR-003/009). Tests: zeroing after success and on a
  panicking `fn`; ctx cancelled before KDF → `ctx.Err()`, KDF never
  starts; wrong password → `password_error`; password rotation mid-run
  picked up without restart; semaphore serialization (two goroutines,
  second blocks until first completes).
- **2.3 — Wire-contract structs.** `TxRequest`, `SignatureValues`,
  `SignResult` (with `hash` and `from` **from day one**),
  `AddressResult` — `json` + `jsonschema` tags per the architecture's
  public API (hex patterns; `data` maxLength 512 KiB hex chars + `0x`).
  These structs ARE the wire contract (no DTO layer); `signing` carries
  the tags without importing the MCP SDK. A golden schema test pins the
  inferred JSON schema (incl. `additionalProperties: false`) so
  accidental wire changes fail loudly.
- **2.4 — Validation.** Runs entirely before key material is touched:
  required-field presence per `type` (`0x0`/`legacy`, `0x2`/`eip1559`);
  type-inappropriate fields rejected (`gasPrice` on type 2, 1559 fee
  fields on legacy); decimal-or-hex numeric parsing; **EIP-55 rule** —
  mixed-case `to` must pass checksum (failure → `invalid_input`),
  all-lowercase/all-uppercase accepted checksum-agnostic;
  **`chainId = 0` rejected**; `data` > 256 KiB bytes rejected; non-empty
  `accessList` rejected; unsupported `type` → `unsupported_type` (types
  1/3/4 are P2 by decision); guard mismatch → `chain_id_mismatch`.
  Table-driven tests per rule.
- **2.5 — Build.** Validated request → `types.LegacyTx` /
  `types.DynamicFeeTx` + the matching `types.Signer` (EIP-155 / London).
  Contract creation (`to` omitted), empty `data` (`"0x"`), zero `value`
  handled; big-int fields parsed without precision loss.
- **2.6 — Signer orchestration.** `NewSigner(vault, SignerOptions)` — the
  **only** home of the chain-id guard, wired by `cmd` from `--chain-id`.
  `SignTransaction(ctx, req)`: validate → `vault.WithSigningKey` →
  `SignTx` → `MarshalBinary` + `RawSignatureValues` → defensive
  recovered-sender == vault-address check (`internal_error` if not) →
  encode `SignResult` (`v` is yParity for type 2, EIP-155 v for legacy;
  `from` EIP-55 checksummed). `ToolError{Code, Message, Cause}` taxonomy
  in `errors.go`; `Cause` never serialized. Panic recovery: deferred
  vault zeroing runs first, then recover maps to `internal_error` with a
  redacted log line; server keeps serving (tested). **Audit line:** one
  info-level line per successful signing — `request_id`, `tx_hash`,
  `chain_id`, `nonce`; calldata/`to`/`value` never logged.
  `WithRequestID`/`RequestIDFromContext` defined here so `signing`
  imports nothing internal. A fake vault that panics if invoked proves
  the vault is **never** touched on any validation-failure path.
- **2.7 — Tools + wire encoding.** `mcp.AddTool` registrations for
  `sign_transaction` (`TxRequest` → `*SignResult`) and `get_address`
  (no input → `*AddressResult`; reads the boot-time snapshot address, no
  password needed). `server/errors.go` is the single crossing point:
  `*signing.ToolError` → `IsError: true` + compact `{"code","message"}`
  JSON in `Content[0]`, nil Go error; any other error → non-nil Go error
  (protocol-level). Contract tests JSON-parse `Content[0]` for every
  code; handler tests use a stub signer + in-memory transport. Handlers
  generate/propagate `request_id` via `signing.WithRequestID`.
- **2.8 — Offline gate load-bearing.** With go-ethereum
  `accounts/keystore` / `core/types` / `crypto` now imported, the ADR-007
  import-graph test is no longer vacuous: assert it passes on the real
  dependency tree and that depguard still pins the package edges, both
  in CI.
- **2.9 — Golden vectors + regen tooling.** `scripts/regen-vectors.sh`
  generates vectors via `cast wallet sign-tx` (Foundry pinned via
  `.foundry-version`; captured `cast --version` committed beside the
  fixtures) and a one-off ethers v6 script; outputs committed under
  `internal/signing/testdata/vectors/`. Vector matrix: both tx types ×
  {chainId 1, chainId 11155111} × edge cases — EIP-155 `v` vs yParity,
  empty `data` (`"0x"`), zero `value`, contract creation (`to` omitted),
  padded/leading-zero nonce — plus rejection vectors for a
  checksum-failing mixed-case address and `chainId = 0`. CI never invokes
  Foundry or Node.
- **2.10 — Parity suite.** For every golden vector: the signer's
  `rawTransaction` is **byte-identical** to the reference output;
  recovered sender equals the keystore address; the RLP decodes via
  `UnmarshalBinary` and round-trips to the same hash; `r`/`s`/`v` match.
  Rejection vectors assert the right `ToolError` code and that the vault
  was never invoked.
- **2.11 — Stdio e2e.** SDK test client over the stdio/in-memory
  transport: `initialize` → `tools/list` shows both tools with strict
  schemas → `get_address` returns the checksummed fixture address →
  `sign_transaction` happy path → one error path per code asserted by
  JSON-parsing `Content[0]` → captured stderr contains the audit line
  and passes the encoded-form leak scan.
- **2.12 — Phase polish pass.** Refactor the signing package now that all
  pieces exist, lint sweep, docs touch-up (package docs + fixture README).

### Key risks

- **Parity edge cases** (yParity vs EIP-155 `v`, empty data, contract
  creation, leading-zero encodings) — the reason 2.9/2.10 are separate,
  explicitly-budgeted tasks with per-edge-case vectors.
- **Zeroing tests are subtle** (Go may copy buffers); tests assert the
  buffers we own are cleared; the limitation is documented per ADR-009,
  not over-claimed.
- Scrypt fixture slowness in the test loop — light fixture for unit
  tests, standard fixture only where parameters matter.

### Exit criteria (testable) — the parity gate

- [ ] **Byte-identical RLP parity vs `cast` and ethers v6** on every
      golden vector, both tx types, chainId 1 and 11155111, including the
      edge cases: EIP-155 `v` vs yParity, empty `data`, zero `value`,
      contract creation (`to` omitted), padded/leading-zero nonce; plus
      rejection of a checksum-failing mixed-case address and of
      `chainId = 0` with the correct codes.
- [ ] Recovered sender == keystore address on every signed vector; every
      RLP round-trips through `UnmarshalBinary` to the same hash.
- [ ] All six error codes observable over MCP as `IsError: true` +
      `{"code","message"}` JSON in `Content[0]`, asserted by JSON-parsing
      in e2e tests.
- [ ] Validation failures never touch the vault (panicking-fake-vault
      test green for every failure class).
- [ ] Zeroing tests green on success and panic paths; a signing-path
      panic leaves the server serving; leak scan (raw + encoded forms)
      green over all captured logs and outputs.
- [ ] Offline-import test load-bearing and green; depguard green; CI
      green on all of the above.
- [ ] One audit line per successful signing with `request_id`, `tx_hash`,
      `chain_id`, `nonce` — and nothing from the tx body.
- [ ] `get_address` returns the EIP-55 address without reading the
      password file (tested with an unreadable password file).

---

## Phase 3: HTTP transport — Streamable HTTP, auth, hardening, resource bounds, shutdown

**Goal:** The same tool surface served over MCP **Streamable HTTP** on
`127.0.0.1`, hardened per ADR-006: bearer auth (SHA-256 + constant-time)
returning 401 before the SDK handler, SDK DNS-rebinding protection
returning 403, `http.MaxBytesHandler` at 1 MiB, the decrypt semaphore
proven under concurrent load, request logging with `request_id`, graceful
signal shutdown, and a pinned middleware pipeline order. The
**concurrent-calls integration test is required and may not be waived.**

**Packages touched:** `internal/server` (`http.go`, `auth.go`,
`reqlog.go`, hardening tests), `cmd` (HTTP branch wiring, shutdown
plumbing finalized).
**ADRs:** ADR-002 (second transport), ADR-006 (full — hardening +
resource bounds).

**Entry criteria:** Phase 2 parity gate green; spike note's
StreamableHTTPOptions + middleware-order findings available.

### Tasks

| ID  | Task                                                         | ~Days | Depends on |
|-----|--------------------------------------------------------------|-------|------------|
| 3.1 | Streamable HTTP server: `RunHTTP`, localhost bind, token-file startup | 1.5 | —     |
| 3.2 | Bearer auth middleware (SHA-256 + constant-time)             | 1.0   | 3.1        |
| 3.3 | Request-id + HTTP request-logging middleware                 | 1.0   | 3.1        |
| 3.4 | Resource bounds: `MaxBytesHandler` 1 MiB; semaphore under HTTP | 1.0 | 3.1        |
| 3.5 | Hardening matrix tests (bind / 403 / 401 / pipeline order)   | 1.5   | 3.2, 3.3, 3.4 |
| 3.6 | Concurrent-calls integration test (REQUIRED)                 | 1.0   | 3.4        |
| 3.7 | Signal shutdown: drain on SIGINT/SIGTERM; stdio EOF          | 0.5   | 3.1        |
| 3.8 | HTTP e2e + stdio/HTTP parity test                            | 1.0   | 3.5        |
| 3.9 | Phase polish pass                                            | 0.5   | 3.1–3.8    |

### Task notes

- **3.1 — Streamable HTTP server.** `RunHTTP(ctx, HTTPOptions)`: read the
  bearer-token file at startup (unreadable/empty → startup error before
  the listener binds; `cmd` exits non-zero, token contents never logged);
  bind `Addr` (default `127.0.0.1:0`) and print the bound `host:port` to
  stderr (P0-CLI-3); wrap the SDK's StreamableHTTPHandler with
  DNS-rebinding/localhost protection **on** (per the spike's findings);
  serve until `ctx` cancels. `cmd` routes `--http` here.
- **3.2 — Bearer auth.** `NewBearerVerifierFromFile` stores
  `sha256(expected)` wrapped in `signing.Secret` (even the hash stays out
  of logs). Middleware hashes the supplied token, then
  `subtle.ConstantTimeCompare` (hashing first neutralizes the length-leak
  short-circuit); missing/wrong token → 401 **before** the SDK handler
  sees the body. Unit tests: correct, wrong, missing, malformed header,
  empty file at startup.
- **3.3 — Request-id + request logging.** Middleware generates the
  `request_id` (SDK-provided id if the spike found one, else UUIDv4),
  attaches it via `signing.WithRequestID`, and logs one line per request
  on completion: `request_id`, `remote_addr`, `status`, `latency_ms`.
  The same `request_id` appears in the Phase 2 audit line — asserted by a
  correlation test. Bodies and headers are never logged; the leak scan
  runs over captured HTTP logs.
- **3.4 — Resource bounds.** Wrap the pipeline in
  `http.MaxBytesHandler(…, 1<<20)`; oversized body rejected without
  reaching the SDK handler (tested with a >1 MiB body). Verify the
  schema-level `data` cap (256 KiB bytes) and the body cap compose sanely.
  Confirm the Phase 2 decrypt semaphore is the only signing concurrency
  gate under HTTP — no extra pooling, ctx checked before KDF.
- **3.5 — Hardening matrix.** One test per layer, plus the order pin:
  (a) listener actually bound to loopback; (b) forged/rebound `Host`
  header → 403 from the SDK handler; (c) missing/wrong bearer → 401;
  (d) **pipeline-order regression test** pinning `MaxBytesHandler` →
  request-id/logging → bearer auth → SDK handler (e.g. oversized body
  with a bad token must fail on size; an unauthorized request must still
  be request-logged with status 401).
- **3.6 — Concurrent-calls integration test (REQUIRED — never waived).**
  N concurrent `tools/call sign_transaction` requests over real HTTP
  against the light-scrypt fixture: all succeed with correct,
  independently-verified signatures; decrypts observably serialized
  (instrumented vault, not wall-clock sleeps); no cross-call state
  bleed; leak scan green over the full captured output. ADR-006's named
  acceptance test.
- **3.7 — Signal shutdown.** `signal.NotifyContext(SIGINT, SIGTERM)`
  cancels the root ctx; HTTP drains via `http.Server.Shutdown` with a
  timeout (semaphore-waiter behavior on cancel covered); stdio exits 0
  on clean EOF. Tested on both transports.
- **3.8 — HTTP e2e + transport parity.** SDK test client over real
  Streamable HTTP with a valid bearer: `initialize`, `tools/list`,
  `get_address`, `sign_transaction` happy + error paths. **Parity test:**
  `tools/list` schemas and a fixed `sign_transaction` result are
  deep-equal across stdio and HTTP — ADR-002's "identical tool surface"
  made checkable.
- **3.9 — Phase polish pass.** Simplify the middleware stack and test
  helpers, lint sweep, docs touch-up (HTTP usage notes; final help text).

### Key risks

- SDK StreamableHTTP/middleware surface differs from the spike's
  expectations — bounded: the spike already validated v1.6.1; any
  residual gap is contained in 3.1.
- Concurrency test flakiness — design it on serialization observations
  (instrumentation/ordering), not wall-clock sleeps.

### Exit criteria (testable)

- [ ] `--http` serves Streamable HTTP on `127.0.0.1` (ephemeral port
      printed); no token file → startup refusal; bad/missing bearer →
      401; rebound Host → 403; >1 MiB body rejected. All in the hardening
      matrix, green in CI.
- [ ] Pipeline-order regression test pins MaxBytes → reqlog → auth → SDK.
- [ ] Concurrent-calls integration test green (correct signatures,
      serialized decrypts, no leakage) — present and not skipped.
- [ ] Every HTTP request logs `request_id`/`remote_addr`/`status`/
      `latency_ms`; the signing audit line carries the same `request_id`.
- [ ] stdio/HTTP parity test green (identical schemas + results).
- [ ] SIGINT/SIGTERM drains and exits cleanly on both transports;
      stdio EOF exits 0.
- [ ] Leak scan (raw + encoded forms) green over all HTTP-path logs.

---

## Phase 4: Release — demos, docs, final sweep, v1.0.0

**Goal:** Prove the PRD's adoption metric (an unmodified MCP client signs
end-to-end), ship operator documentation that sets honest expectations
(scrypt latency, lifecycle, threat model), verify pins and advisories
mechanically, re-verify every guard by mutation, and tag
`eth-signer-mcp/v1.0.0`.

**Packages touched:** none structurally — docs, scripts, CI verification,
tag.

**Entry criteria:** Phases 1–3 exit criteria all green on `main`.

### Tasks

| ID  | Task                                                        | ~Days | Depends on |
|-----|-------------------------------------------------------------|-------|------------|
| 4.1 | Stdio + HTTP demos (real MCP client, no client code changes) | 1.0  | —          |
| 4.2 | Operator README                                             | 1.0   | 4.1        |
| 4.3 | Version-pin verification + acceptance benchmark             | 1.0   | —          |
| 4.4 | Final sweep: mutation re-checks + end-to-end leak audit     | 1.0   | 4.3        |
| 4.5 | CHANGELOG + release notes + `eth-signer-mcp/v1.0.0` tag + smoke | 1.0 | 4.1–4.4   |

### Task notes

- **4.1 — Demos.** (a) Stdio: launch from a Claude Desktop (or
  equivalent) MCP client config; `get_address` then `sign_transaction`;
  verify the returned RLP decodes and recovers to the fixture address —
  no client code changes (PRD adoption metric). Fallback medium: the
  SDK's example CLI client, in case of client config quirks. (b) HTTP:
  start with `--http` + token file; a small script calls both tools over
  Streamable HTTP with the bearer. Both flows captured in a
  `docs/demo.md` walkthrough with exact commands.
- **4.2 — Operator README.** Flags reference; quick-start for both
  transports; the **keystore lifecycle contract** (boot-time snapshot;
  password re-read per call → rotation works live; keystore rotation
  requires restart; mid-run decrypt failure → `password_error`);
  **latency expectations** (~0.5–1 s standard scrypt per call by design,
  ~50 ms light scrypt; light-scrypt for dev loops — also surfaced in
  `--help`); permissions guidance (chmod 600, `--strict-perms`); threat
  model summary (off-localhost exposure unsupported); error-code table;
  supported MCP protocol revision per go-sdk v1.6.1.
- **4.3 — Pin verification + benchmark.** Assert `go.mod` pins exactly:
  go-sdk v1.6.1, go-ethereum v1.17.3, urfave/cli v3 (exact patch);
  `.foundry-version` matches the committed `cast --version` capture;
  `govulncheck` clean — no manual advisory claims anywhere in the docs.
  Run the **acceptance benchmark** on both fixture sets: cold start
  < 200 ms; non-KDF overhead (total minus measured KDF time) < 10 ms on
  standard and light scrypt. Record results in the release notes.
- **4.4 — Final sweep.** `make lint && make test && make build` green at
  the release commit. **Mutation re-checks:** temporarily add a forbidden
  import to `internal/signing` → offline-import test and depguard both
  fail → revert (ADR-007's named re-check); temporarily log a
  Secret-bearing struct → leak scan fails → revert. **End-to-end leak
  audit:** run the full e2e suites (stdio + HTTP) at `debug` level, scan
  every captured byte of stderr/stdout/responses for the sentinel and
  all encoded forms.
- **4.5 — CHANGELOG, tag, smoke.** CHANGELOG + release notes: scope,
  pins, honest latency statement, threat model pointer, P2 backlog
  (types 1/3/4, EIP-191/712, audit-log file, multi-account) — **no false
  advisory claims**. Tag `eth-signer-mcp/v1.0.0` (monorepo-prefixed).
  Post-release smoke: fresh clone, `make build`, run the stdio demo
  against the tagged binary.

### Key risks

- External MCP client config quirks block the canonical demo — fallback
  CLI client documented in 4.1.
- Benchmark variance on developer hardware — the benchmark asserts the
  **non-KDF overhead** delta (< 10 ms), not absolute KDF time, so it is
  robust to machine speed.

### Exit criteria (testable)

- [ ] Both demos reproduced from the written walkthrough by following
      `docs/demo.md` verbatim.
- [ ] README covers flags, lifecycle contract, latency expectations,
      permissions, threat model, error codes.
- [ ] `govulncheck` clean; pins verified; benchmark green on both scrypt
      parameter sets (cold start < 200 ms; non-KDF overhead < 10 ms).
- [ ] Mutation re-checks performed and documented (offline-import +
      depguard; leak scan).
- [ ] `eth-signer-mcp/v1.0.0` tagged with CI green at the tagged commit;
      post-release smoke passed from a fresh clone.

---

## Risk Register

| Risk | Impact | Likelihood | Mitigation |
|------|--------|------------|------------|
| MCP SDK v1.6.x API drift (typed-tool registration, StreamableHTTPOptions, middleware hooks) | High | Medium | Pin `v1.6.1` for server and test client; **Phase 1 SDK spike (task 1.7)** validates the integration surface and records findings before any signing code depends on it. |
| Parity mismatch on edge cases (EIP-155 `v` vs yParity, empty `data`, contract creation, leading-zero encodings) | High | Medium | Per-edge-case golden vectors (2.9) and byte-identical assertions vs `cast` + ethers v6 (2.10); rejection vectors for checksum failure and `chainId = 0`; rerun on any go-ethereum bump. |
| Secret leakage through **encoded** log forms (hex/base64/decimal would evade a raw-bytes scan) | High | Low | Sentinel scan covers raw bytes plus lower/upper hex, base64, and decimal scalar forms (1.5); new secret types must register encoded forms; scan runs in `signing`, `obs`, e2e suites, and the Phase 4 end-to-end leak audit. |
| Operator mis-binds HTTP off localhost | High | Low | Default `127.0.0.1:0`; bearer auth still gates every request; bound address printed at startup; off-localhost documented as unsupported (README, threat model). |
| Scrypt latency surprises operators (~0.5–1 s per call, standard scrypt, every call, by design — ADR-010) | Medium | Medium | Stated in README and `--help`; light-scrypt fixture documented for dev loops; acceptance benchmark separates KDF cost from our < 10 ms overhead so the cost is attributable. |
| `slog` nested-struct reflection leaks a `Secret` | High | Low | Documented usage rule; known-leak anti-pattern test (1.5); leak scans at every level. |
| Best-effort memory erasure (Go GC may retain transient copies) | Medium | Low | ADR-009 accepted residual risk; threat model excludes the adversaries who could exploit it; the observable requirement (no secrets in logs/outputs, raw or encoded) is test-enforced. |
| Foundry output drift breaks fixture regeneration | Low | Medium | `.foundry-version` pin + committed `cast --version` capture; CI never invokes Foundry; regen is a developer-only script. |
| Concurrent-load memory pressure (each standard-scrypt decrypt ≈ 256 MiB) | Medium | Low | Decrypt semaphore of 1 + ctx check before KDF (2.2); 1 MiB body cap + 256 KiB data cap (3.4); required concurrent-calls test (3.6) proves the bound. |
| urfave/cli v3 idiom drift between patch releases | Low | Low | Exact patch pin; `--help`/`--version` smoke tests in CI from Phase 1. |

Future go-ethereum advisories are tracked mechanically by `govulncheck` in
CI (Phase 1 task 1.2); v1.17.3 has no open advisories at planning time, so
no advisory-driven bump task exists in this plan.

## v1.0.0 Acceptance Criteria

Aligned with the PRD's Success Metrics; all are mechanically checkable:

- **Parity.** Byte-identical RLP output vs `cast wallet sign-tx` and
  ethers v6 on every curated vector, both tx types, chainId 1 and a
  non-mainnet chainId, including all Phase 2 edge cases.
- **Sender recovery.** Recovered sender equals the keystore-derived
  address on every signed transaction (defensive in-signer check + parity
  suite assertion).
- **RLP round-trip.** Every signed RLP decodes via
  `core/types.Transaction.UnmarshalBinary` and re-hashes identically.
- **Latency.** Cold start < 200 ms; non-KDF signing overhead < 10 ms,
  benchmarked on both scrypt fixtures; end-to-end latency dominated by
  scrypt by design (~0.5–1 s standard / ~50 ms light), documented — no
  "warm path" claims.
- **No secrets in logs or outputs** at any level — raw or encoded
  (hex/base64/decimal) — verified by the sentinel scans and the Phase 4
  end-to-end leak audit.
- **Offline structurally enforced.** ADR-007 import-graph test + ADR-008
  depguard both green, proven load-bearing by the mutation re-check.
- **Hardened HTTP.** Localhost bind, 401/403 layers, 1 MiB body cap,
  serialized decrypts under the required concurrent-calls test, pinned
  middleware order.
- **Adoption.** An unmodified MCP client launches the binary from config
  and completes `get_address` + `sign_transaction` in one session; the
  same flow works over Streamable HTTP with a bearer token.
- **Release hygiene.** CI green at the tagged commit; pins verified;
  `govulncheck` clean; README + CHANGELOG shipped;
  `eth-signer-mcp/v1.0.0` tagged.

## Decision Log

Decisions made during this planning pass (architecture-level decisions
live in [`architecture.md`](./architecture.md) §ADRs):

- **Four phases, no separate polish phase** (user directive). Old
  "P1 polish" items moved to where their code is first written; every
  phase ends with an in-phase polish task instead.
- **`internal/obs` is built complete in Phase 1** — JSON handler,
  `--log-level`, build info — no text-handler-then-swap step.
- **Both tools and the full output shape ship in Phase 2.**
  `get_address`, `hash`, `from`, and the audit line are part of the tool
  surface from day one; no enable-later flags or `omitempty` staging.
- **The MCP SDK spike moved from day ~31 to Phase 1** because SDK API
  drift is the top risk and the spike's findings shape Phases 2 and 3.
- **CI is a Phase 1 deliverable**, not an assumption — every later
  "enforced in CI" claim in this plan points at task 1.2's workflow.
- **No interface-skeleton or noop-stub tasks.** The four-package layout
  has no consumers for stubs; each phase lands working code with tests.
- **Resource bounds live in Phase 3 with the transport that exposes
  them**; the concurrent-calls integration test is non-waivable (ADR-006).
- **Task IDs are stable `N.M` identifiers** (no a/b/c suffixes); issue
  files generated from this plan reuse them verbatim. Old plan task IDs
  are dead and must not be referenced.
- **35 task days + ~3 days contingency = ~38 working days.** Day windows
  in §Schedule Overview are nominal task days; the float is drawn down
  explicitly rather than padded into individual tasks.
- **No environment variables for secrets in v1** — CLI flags referencing
  files only, per the PRD; the `config` struct is `cmd`-local.
- **No CI-side Foundry / Node invocation.** Golden vectors are committed;
  regeneration is a developer task via `scripts/regen-vectors.sh`.
