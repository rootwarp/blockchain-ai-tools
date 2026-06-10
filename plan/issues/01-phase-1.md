# Phase 1: Foundations — scaffold, CI, CLI, obs, secret hygiene, stdio boot, SDK spike

## Phase Overview

- **Goal:** A runnable `eth-signer-mcp` binary that parses all CLI flags via
  urfave/cli v3, performs file-permission checks (warn / `--strict-perms`
  refuse), logs structured JSON, reports rich build info on `--version`, and
  answers MCP `initialize` + an empty `tools/list` over stdio. CI enforces
  lint/test/build/govulncheck/Windows-compile from this phase onward. The MCP
  SDK surface is de-risked by a spike whose findings are committed as a note.
  `internal/obs` is **complete** in this phase — JSON handler from day one,
  no later swap.
- **Issue count:** 10 issues (one per plan task 1.1–1.10), **14 total points**.
- **Estimated duration:** ~7 working days, single implementer,
  single-stream sequential execution.
- **Packages touched:** `cmd/eth-signer-mcp` (full), `internal/obs` (full),
  `internal/signing` (secret hygiene + Sentinel only), `internal/server`
  (stdio shell, no tools), repo root (CI workflow, depguard block).
- **ADRs:** ADR-001, ADR-002 (stdio half), ADR-007 (scaffold), ADR-008,
  ADR-009 (helpers).
- **Entry criteria** (from `plan/project-plan.md`):
  - Prerequisites met: Go 1.26 toolchain, `make`, `golangci-lint` v2 on
    `$PATH`, `git`; push/merge access to `rootwarp/blockchain-ai-tools`
    including GitHub Actions workflow permissions.
  - Plan, PRD ([`prd.md`](../prd.md)), and architecture
    ([`architecture.md`](../architecture.md), rev 2 — full simplification)
    approved and not under active revision.
- **Exit criteria** (from `plan/project-plan.md`, all testable):
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

### Phase Conventions (locked; do not re-litigate during execution)

- **Module & layout.** Single Go module at `apps/eth-signer-mcp/`, import
  path `github.com/rootwarp/blockchain-ai-tools/apps/eth-signer-mcp`,
  Go 1.26. Binary `main` at `cmd/eth-signer-mcp/main.go`; three internal
  packages: `internal/signing`, `internal/server`, `internal/obs` (ADR-001).
- **Version pins.** `github.com/modelcontextprotocol/go-sdk` **v1.6.1**
  (server and test client), `github.com/ethereum/go-ethereum` **v1.17.3**,
  `urfave/cli` **v3** (latest stable patch, pinned exactly),
  `github.com/google/jsonschema-go`, golangci-lint **v2**. go-ethereum
  v1.17.3 is NOT affected by GO-2026-4314/-4315/-4507/-4508/-4511; future
  advisories are tracked by `govulncheck` in CI (issue 1.2), never by manual
  claims.
- **Transport naming.** The second transport (Phase 3) is MCP **Streamable
  HTTP**. It is never called "HTTP/SSE" anywhere — code, docs, or notes.
- **`config` is a `cmd`-local struct, not a package.** Parsed once,
  validated, fields passed to constructors; no internal package imports a
  config type.
- **`internal/obs` is built complete in this phase.** JSON handler from day
  one; there is no later text→JSON swap, no `--log-level` enable-later step.
- **fsperm is wired once,** in `cmd`, with `cfg.StrictPerms`, in this phase.
  No later re-wiring issue exists in any phase. The check is startup-only
  per the PRD (paths checked before serving; not re-checked per call).
- **Sanitized-failure-message rule.** Any test that captures output
  containing the leak-scan sentinel must NEVER include that captured buffer
  in a failure message (`t.Logf`/`t.Errorf("got: %s", buf)` would re-leak
  the sentinel into CI logs). Failure messages name the leaked *form*
  (e.g. "base64"), never the bytes. This rule applies to every leak test in
  every phase and is restated in the Sentinel's doc comment (issue 1.5).

## Phase Summary

| Issue | Title                                                              | Points | Days | Blocked by |
|-------|--------------------------------------------------------------------|--------|------|------------|
| 1.1   | Scaffold app module + pin dependencies                             | 1      | 0.5  | —          |
| 1.2   | CI workflow (lint/test/build/govulncheck/GOOS=windows)             | 2      | 1.0  | 1.1        |
| 1.3   | CLI flags + `cmd`-local config struct + validation                 | 2      | 1.0  | 1.1        |
| 1.4   | `internal/obs` complete: JSON slog, `--log-level`, build info      | 1      | 0.5  | 1.3        |
| 1.5   | `internal/signing` secret hygiene: `Secret[T]`, zeroing, Sentinel  | 2      | 1.0  | 1.1        |
| 1.6   | fsperm checks in `cmd`: warn + `--strict-perms` refusal            | 1      | 0.5  | 1.3        |
| 1.7   | MCP SDK spike (in-memory transport, HTTP options, middleware order)| 2      | 1.0  | 1.1        |
| 1.8   | Stdio MCP server boots (`initialize` + empty `tools/list`)         | 1      | 0.5  | 1.4, 1.7   |
| 1.9   | Slim depguard config + offline-import test scaffold                | 1      | 0.5  | 1.5        |
| 1.10  | Phase polish pass (refactor/simplify, lint sweep, docs)            | 1      | 0.5  | 1.1–1.9    |

**Total: 10 issues, 14 points, 7.0 task days.**

## Phase Execution Plan

| Day | Work                                                                     |
|-----|--------------------------------------------------------------------------|
| 1   | 1.1 Scaffold + pins (0.5d) · 1.2 CI workflow start (0.5d)                |
| 2   | 1.2 CI workflow finish (0.5d) · 1.3 CLI flags + config start (0.5d)      |
| 3   | 1.3 CLI flags + config finish (0.5d) · 1.4 `internal/obs` complete (0.5d)|
| 4   | 1.5 Secret hygiene: `Secret[T]`, zeroing, Sentinel + leak tests (1.0d)   |
| 5   | 1.6 fsperm in `cmd` (0.5d) · 1.7 MCP SDK spike start (0.5d)              |
| 6   | 1.7 MCP SDK spike finish + note (0.5d) · 1.8 stdio boot (0.5d)           |
| 7   | 1.9 depguard + offline-import scaffold (0.5d) · 1.10 polish pass (0.5d)  |

---

## Issues

### Issue 1.1: Scaffold app module + pin dependencies

- **Points:** 1
- **Type:** chore
- **Priority:** P0
- **Blocked by:** none
- **Blocks:** 1.2, 1.3, 1.5, 1.7 (direct); everything else transitively.
- **Scope:** ~0.5 day

**Description:**
Create the `eth-signer-mcp` app module with `make new-app`, relocate the
scaffolder's starter `main.go` to the architecture's `cmd/` layout, and pin
every Phase 1+ dependency at its locked version so subsequent issues compile
against a frozen surface.

The relocation is deliberate and must be documented: `make new-app` writes
`apps/eth-signer-mcp/main.go` at the module root, but the architecture
(§Module Details: `cmd/eth-signer-mcp`) places the entry point at
`apps/eth-signer-mcp/cmd/eth-signer-mcp/main.go` — the module root stays
package-free so the binary name, the `cmd/` convention, and the three
`internal/` packages mirror the four-package layout exactly, and `go build
./cmd/eth-signer-mcp` names the binary correctly. Record this rationale in a
short comment at the top of `main.go` (and in the README stub touched by
1.10) so future scaffolding doesn't reintroduce a root-level `main.go`.

**Implementation Notes:**
- Files to create/modify:
  - `apps/eth-signer-mcp/go.mod`, `go.sum` (created by scaffolder; pins added).
  - `apps/eth-signer-mcp/cmd/eth-signer-mcp/main.go` (moved from
    `apps/eth-signer-mcp/main.go`; root placeholder deleted).
  - `go.work` (auto-updated by the scaffolder via `go work use`; do not
    hand-edit per `CLAUDE.md`).
- Steps:
  1. `make new-app name=eth-signer-mcp` from the repo root; verify `go.work`
     gains `./apps/eth-signer-mcp`.
  2. `mkdir -p apps/eth-signer-mcp/cmd/eth-signer-mcp` and `git mv` the
     starter `main.go` into it; ensure no `package main` file remains at the
     module root (an empty module root is fine — the Makefile discovers
     modules by `go.mod`, not by packages).
  3. From `apps/eth-signer-mcp/`, pin dependencies:
     - `go get github.com/modelcontextprotocol/go-sdk@v1.6.1`
     - `go get github.com/ethereum/go-ethereum@v1.17.3`
     - `go get github.com/urfave/cli/v3@latest` — resolve to the latest
       stable v3 patch and record the exact version in the commit message;
       this is the pin every later "urfave/cli v3" reference means.
     - `go get github.com/google/jsonschema-go@latest` (record exact version;
       backs `mcp.AddTool` schema inference — there is no SDK-embedded
       jsonschema package).
     - `go mod tidy`
  4. `go mod tidy` will drop pins nothing imports yet. Keep them with a
     single `tools.go`-style file at the module root:
     `//go:build tools` + blank imports of the four module paths (e.g.
     `_ "github.com/modelcontextprotocol/go-sdk/mcp"`). Issues 1.3/1.7/1.8
     add real imports; the tools file can shrink in 1.10 once real imports
     hold each pin.
- Watch out for: go-ethereum pulls a large transitive tree; a big `go.sum`
  diff is expected. Do not prune manually.

**Acceptance Criteria:**
- [ ] `apps/eth-signer-mcp/go.mod` exists with module path
      `github.com/rootwarp/blockchain-ai-tools/apps/eth-signer-mcp` and
      `go 1.26`.
- [ ] `apps/eth-signer-mcp/cmd/eth-signer-mcp/main.go` is the only
      `package main` file in the module; the scaffolder's root-level
      `main.go` is gone; a comment in `main.go` records why the entry point
      lives under `cmd/`.
- [ ] `go.mod` pins `github.com/modelcontextprotocol/go-sdk v1.6.1`,
      `github.com/ethereum/go-ethereum v1.17.3`, an exact
      `github.com/urfave/cli/v3` patch, and `github.com/google/jsonschema-go`.
- [ ] `go mod tidy` is idempotent (second run changes nothing).
- [ ] `go.work` lists `./apps/eth-signer-mcp`.
- [ ] `make build` produces `bin/eth-signer-mcp` and exits 0; `make test`
      exits 0.

**Testing Notes:**
- No unit tests; `make build` / `make test` are the smoke. Real tests land
  from 1.3 onward.

---

### Issue 1.2: CI workflow (lint/test/build/govulncheck/GOOS=windows)

- **Points:** 2
- **Type:** chore
- **Priority:** P0
- **Blocked by:** 1.1
- **Blocks:** nothing structurally, but every later "in CI" / "enforced on
  every commit" claim in the plan refers to this workflow — land it before
  feature work piles up.
- **Scope:** ~1 day

**Description:**
Create `.github/workflows/ci.yml` running on every PR and every push to
`main`: `make lint` (golangci-lint v2 — this automatically picks up the
depguard block when 1.9 lands), `make test`, `make build`,
`govulncheck ./...` per module, and a `GOOS=windows go build ./...` compile
check (exercises the `fsperm_windows.go` build tag from 1.6 once it exists,
and catches accidental POSIX-only code from day one). This workflow is the
referent of every later "in CI" claim; vulnerability tracking for
go-ethereum and all other deps is automated here — no manual advisory
claims anywhere in the repo.

**Implementation Notes:**
- Files to create: `.github/workflows/ci.yml`.
- Workflow shape (single job is fine at this repo size; split later if slow):
  - Trigger: `pull_request` + `push: branches: [main]`.
  - `actions/checkout`, `actions/setup-go` with `go-version: '1.26'` (or
    `go-version-file: go.work`) and module caching.
  - Install golangci-lint v2 via its official action
    (`golangci/golangci-lint-action`) or the install script, pinned to a
    v2.x version; then `make lint`. Note: `make lint` iterates modules and
    expects `golangci-lint` on PATH — if the action's built-in run mode
    fights the Makefile loop, use `install-mode: none`-style installation
    and call `make lint` directly so local and CI lint are identical.
  - `make test`, then `make build`.
  - `go install golang.org/x/vuln/cmd/govulncheck@latest`, then run
    `govulncheck ./...` inside `apps/eth-signer-mcp/` (workspace mode is
    active; run per module dir to match the Makefile convention).
  - Windows compile check: `cd apps/eth-signer-mcp && GOOS=windows
    GOARCH=amd64 go build ./...` — compile only, no Windows runner.
- Watch out for: `make lint` is the only step with an external tool version
  to drift; pin the golangci-lint version in the workflow and note it in
  the workflow file comment so bumps are deliberate.
- Watch out for: `govulncheck` failures on *future* advisories are the
  point — do not mark the step `continue-on-error`.

**Acceptance Criteria:**
- [ ] `.github/workflows/ci.yml` exists and triggers on PRs and pushes to
      `main`.
- [ ] The workflow runs, in order or in parallel: `make lint`, `make test`,
      `make build`, `govulncheck ./...` (in `apps/eth-signer-mcp/`), and
      `GOOS=windows go build ./...`.
- [ ] golangci-lint v2 is version-pinned in the workflow; Go version matches
      the workspace toolchain (1.26).
- [ ] CI is green on `main` with the Phase 1 codebase as of this issue.
- [ ] No step is `continue-on-error`; a govulncheck finding fails the build.

**Testing Notes:**
- Verify by pushing a branch and opening a PR; confirm all five checks run
  and pass. Optionally verify the failure path by temporarily breaking a
  test in the PR (then fixing it) so the team has seen CI actually fail.

---

### Issue 1.3: CLI flags + `cmd`-local config struct + validation

- **Points:** 2
- **Type:** feature
- **Priority:** P0
- **Blocked by:** 1.1
- **Blocks:** 1.4, 1.6, 1.8
- **Scope:** ~1 day

**Description:**
Implement the urfave/cli v3 root `*cli.Command` with context-aware
`Run(ctx, os.Args)` and the complete v1 flag set, parsing into the
`cmd`-local `config` struct. **All flags land now** — including the
HTTP-transport flags whose behavior arrives in Phase 3 — so the CLI
contract is frozen once and `--help` is truthful for the life of v1.
`config` is a struct in `package main`, not a package; no internal package
ever imports a config type (architecture §Module Overview).

Flag set (PRD P0-CLI-1..6, P1-SEC-1, P1-OBS-1):

| Flag                      | Type     | Default       | Notes                                   |
|---------------------------|----------|---------------|-----------------------------------------|
| `--keystore`              | string   | — (required)  | Web3 Secret Storage JSON path           |
| `--password-file`         | string   | — (required)  | password file path; never inline        |
| `--http`                  | bool     | false         | selects Streamable HTTP (Phase 3)       |
| `--http-addr`             | string   | `127.0.0.1:0` | bind address; ephemeral port default    |
| `--http-auth-token-file`  | string   | —             | required when `--http` is set           |
| `--chain-id`              | uint64   | unset (nil)   | optional guard; nilable                 |
| `--strict-perms`          | bool     | false         | refuse (exit 2) on open perms (1.6)     |
| `--log-level`             | string   | `info`        | debug/info/warn/error                   |
| `--help` / `--version`    | —        | —             | urfave/cli defaults; version via obs    |

**Implementation Notes:**
- Files to create/modify:
  - `apps/eth-signer-mcp/cmd/eth-signer-mcp/main.go` — `*cli.Command`
    construction, `Run(ctx, os.Args)`; replaces the scaffold placeholder.
  - `apps/eth-signer-mcp/cmd/eth-signer-mcp/config.go` — the `config`
    struct + `validate()` cross-field rules.
  - `apps/eth-signer-mcp/cmd/eth-signer-mcp/config_test.go` — golden
    flag-set tests + validation-rule tests.
- The `config` struct (architecture §`cmd/eth-signer-mcp`):
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
- urfave/cli v3 idioms (verified patterns; the package reshaped its API
  from v2 — `*cli.Command` root, no `cli.App`):
  - `cli.StringFlag{Name: ..., Required: true}`, `cli.BoolFlag`,
    `cli.Uint64Flag`; read values inside `Action: func(ctx context.Context,
    cmd *cli.Command) error` via `cmd.String(...)`/`cmd.Bool(...)`/
    `cmd.Uint64(...)`.
  - `--chain-id` nilability: urfave/cli has no nilable uint64 flag. Use
    `cmd.IsSet("chain-id")` in the Action to decide between nil and
    `&value`. Document the idiom inline.
  - `cmd.Version` is set from `obs.Build()` (wired in 1.4); v3 prints it on
    `--version` automatically.
  - For tests, drive `Run(ctx, []string{...})` with explicit arg slices —
    never mutate `os.Args` in tests.
- Cross-field validation in `validate()`:
  - `--http` set ⇒ `--http-auth-token-file` set (P0-CLI-4: no token, no
    HTTP).
  - keystore + password file flags both required (also enforced by
    `Required: true`; validate defensively anyway).
  - `--chain-id 0` rejected at parse time: chainId 0 transactions are
    rejected by decision (no replay-unprotected signatures), so a 0 guard
    can never match a valid request — fail fast with a clear message.
  - `--log-level` must be one of `debug|info|warn|error` (case-insensitive
    accept is fine; document the choice). Note: `obs.NewLogger` itself
    falls back to `info` on garbage — the flag validation gives the
    operator an explicit error instead of a silent fallback.
- Bad flags / failed validation: sanitized one-line stderr message,
  non-zero exit. Error messages never echo file *contents*, only paths.

**Acceptance Criteria:**
- [ ] `--help` exits 0 and lists every flag in the table above with its
      default.
- [ ] Golden flag-set test: parsing
      `--keystore /k --password-file /p` yields `config{KeystorePath:"/k",
      PasswordPath:"/p", HTTP:false, HTTPAddr:"127.0.0.1:0",
      ChainIDGuard:nil, StrictPerms:false, LogLevel:"info"}`.
- [ ] Missing `--keystore` or `--password-file` → non-zero exit with an
      error naming the missing flag.
- [ ] `--http` without `--http-auth-token-file` → validation error naming
      `--http-auth-token-file`.
- [ ] `--chain-id 1` → `*ChainIDGuard == 1`; flag absent → `ChainIDGuard ==
      nil`; `--chain-id 0` → validation error.
- [ ] `--log-level garbage` → validation error; each of
      `debug|info|warn|error` accepted.
- [ ] Unknown flag → non-zero exit, non-empty stderr (binary-level smoke
      test).
- [ ] No internal package imports any config type (`config` is unexported,
      in `package main`).

**Testing Notes:**
- Table-driven tests over arg slices; assert stable error-message
  substrings so messages don't silently lose information.
- The `--help`/bad-flag smoke tests may run the Action in-process; full
  binary-level `--version` output is asserted in 1.4 once `obs.Build()` is
  wired.

---

### Issue 1.4: `internal/obs` complete: JSON slog, `--log-level`, build info

- **Points:** 1
- **Type:** feature
- **Priority:** P0
- **Blocked by:** 1.3
- **Blocks:** 1.8
- **Scope:** ~0.5 day

**Description:**
Build `internal/obs` **complete, once** — there is no later text→JSON swap
anywhere in the plan. `NewLogger(level string)` returns a JSON-handler
`*slog.Logger` writing to **stderr** (stdout stays pristine for MCP JSON-RPC
frames on stdio); an unparseable level falls back to `info` (constructor is
infallible). `Build()` reads `runtime/debug.ReadBuildInfo` and feeds a rich
`--version`: version, commit, build date, Go version — with explicit
`<unknown>` fallbacks for fields that are absent (e.g. under `go test`,
or builds without VCS metadata). The package doc documents the project's
redaction rules; enforcement lives in `signing` (`Secret[T]`, Sentinel) and
the leak-scan tests.

**Implementation Notes:**
- Files to create:
  - `apps/eth-signer-mcp/internal/obs/log.go` —
    `NewLogger(level string) *slog.Logger`: parse `level`
    (case-insensitive `debug|info|warn|error`) to `slog.Level`, fall back
    to `slog.LevelInfo` on anything unparseable;
    `slog.New(slog.NewJSONHandler(os.Stderr, &slog.HandlerOptions{Level: lvl}))`.
  - `apps/eth-signer-mcp/internal/obs/buildinfo.go` —
    `type Info struct{ Version, Commit, Date, GoVersion string }` and
    `Build() Info` from `debug.ReadBuildInfo()`: `Version` from
    `info.Main.Version`, `Commit` from the `vcs.revision` setting, `Date`
    from `vcs.time`, `GoVersion` from `info.GoVersion`. Every field that
    cannot be determined (`ok == false`, missing settings, `(devel)`
    placeholders treated as-is) is set to the literal string `<unknown>` —
    never empty, never a panic. This matters because `go test` binaries
    have no VCS stamping; tests rely on the fallback.
  - `apps/eth-signer-mcp/internal/obs/log_test.go`,
    `buildinfo_test.go`.
  - Package doc (`doc.go` or top of `log.go`): the redaction rules —
    (1) no secret material at any log level, raw or encoded; (2) secrets
    are only ever held in `signing.Secret[T]`, which redacts on every
    print/serialize path; (3) **never embed a `Secret` inside a struct
    passed to slog** — reflection bypasses `LogValue` (the known-leak
    anti-pattern test in 1.5 demonstrates this); (4) the transaction body
    (calldata/to/value) is never logged (Phase 2 enforces).
- `cmd` wiring (same issue): `logger := obs.NewLogger(cfg.LogLevel)`
  immediately after parse; set `cmd.Version` from `obs.Build()` so
  `--version` prints all four fields on one line, e.g.
  `eth-signer-mcp <version> (commit <commit>, built <date>, <goversion>)`.
- Build flags: the Makefile already builds with defaults; confirm
  `-trimpath -buildvcs=true` (add to the app's build flags if the root
  Makefile doesn't pass them) so `ReadBuildInfo` carries VCS data in real
  builds. No ldflags plumbing — `ReadBuildInfo` is the single source.
- `internal/obs` imports **nothing internal** (stdlib only). Its leak-scan
  test will import `signing`'s Sentinel in 1.5 — a test-only edge,
  explicitly permitted by ADR-008.

**Acceptance Criteria:**
- [ ] `NewLogger("info")` returns a non-nil logger; output captured through
      a JSON handler is valid JSON per line with `ts`/`level`/`msg` keys
      (slog defaults).
- [ ] Level filtering honored: at `warn`, a `Debug(...)` and `Info(...)`
      produce no output; at `debug`, all levels appear. Table-driven over
      all four levels.
- [ ] `NewLogger("garbage")` falls back to `info` (asserted: debug line
      suppressed, info line emitted) and does not error or panic.
- [ ] `Build()` under `go test` returns `<unknown>` (not empty, not a
      panic) for any undeterminable field, and a non-empty `GoVersion`.
- [ ] Binary-level: `./bin/eth-signer-mcp --version` exits 0 and prints
      version, commit, date, and Go version (real build ⇒ VCS fields
      populated, no `<unknown>` expected; assert at least the Go-version
      substring `go1.`).
- [ ] Package doc states the four redaction rules.
- [ ] `internal/obs` production code imports stdlib only.

**Testing Notes:**
- Capture logger output by constructing the handler over a `bytes.Buffer`
  in tests (export a small `newLoggerTo(w io.Writer, level string)` helper
  or accept an option — keep the public constructor signature
  `NewLogger(level string)` per the architecture).
- The sentinel-based leak scan over `obs` output is added in 1.5 (it needs
  the Sentinel helper); this issue's tests cover shape, level, fallback,
  and build info.

---

### Issue 1.5: `internal/signing` secret hygiene: `Secret[T]`, zeroing, Sentinel

- **Points:** 2
- **Type:** feature
- **Priority:** P0
- **Blocked by:** 1.1
- **Blocks:** 1.9; all Phase 2 signing work builds on these primitives.
- **Scope:** ~1 day

**Description:**
Land the secret-hygiene primitives in `internal/signing` (the home of
everything that touches key material — these are files in that package, not
a separate package): the generic redacting wrapper `Secret[T]`, the
best-effort zeroing helpers `ZeroBytes`/`ZeroBigInt` (ADR-009), and the
**Sentinel** leak-scan helper that derives **encoded forms** of a fixture
secret — lowercase hex, uppercase hex, base64, and the decimal rendering of
the fixture key's scalar — so a hex- or base64-rendered leak cannot evade a
raw-bytes scan. New secret types added in later phases must register their
encoded forms with the Sentinel; the doc comment states this contract.
Includes the known-leak anti-pattern test (a `Secret` embedded in a struct
passed to slog **does** leak via reflection — asserted, to make the usage
rule visible) and the shared leak scan over `obs` output (test-only edge).

**Implementation Notes:**
- Files to create (all in `apps/eth-signer-mcp/internal/signing/`):
  - `secret.go` — `Secret[T any]` with one unexported field;
    `NewSecret[T any](v T) Secret[T]`; `Expose() T` as the **only** read
    path (no `Unwrap`/`Value` aliases reflection might trip). Implements
    all five redacting interfaces:
    - `fmt.Stringer` → `"[REDACTED]"`
    - `fmt.GoStringer` → `"[REDACTED]"` (catches `%#v`)
    - `fmt.Formatter` → writes `"[REDACTED]"` for every verb
      (`%v %+v %#v %s %q %x %X %d`)
    - `json.Marshaler` → `[]byte("\"[REDACTED]\"")`
    - `slog.LogValuer` → `slog.StringValue("[REDACTED]")`
  - `zero.go` — `ZeroBytes(b []byte)` = `clear(b)` +
    `runtime.KeepAlive(b)`; `ZeroBigInt(n *big.Int)` = `clear(n.Bits())` +
    `runtime.KeepAlive(n)` (geth pattern). Doc comments state the ADR-009
    limitation honestly: zeroing is **best-effort** — Go's runtime may
    retain transient copies (GC moves, stack copies); the test-enforced
    requirement is "no secrets in logs/outputs, raw or encoded".
  - `sentinel.go` — per the architecture's public API:
    ```go
    type Sentinel struct{ Name string; Forms [][]byte }
    func NewSentinel(name string, raw []byte) Sentinel
    func (s Sentinel) Scan(output []byte) []string // names of leaked forms
    ```
    `NewSentinel` derives forms: raw bytes, lowercase hex, uppercase hex,
    std base64 (and raw/unpadded base64 — cheap, closes a gap), and the
    decimal rendering `new(big.Int).SetBytes(raw).String()` of the scalar.
    `Scan` returns the names of every form found (e.g.
    `["hex-lower","base64-std"]`) so failure messages can name the *form*
    without reprinting the bytes — this is how the sanitized-failure-message
    rule (Phase Conventions) is made easy to follow. Doc comment: new
    secret types must register their encoded forms here.
  - `secret_test.go` — leak scan over every redacting path: build a
    Sentinel from a fixture byte string, wrap it in `Secret`, render via
    `fmt.Sprintf` (all verbs above), `json.Marshal`, and
    `slog.Info("e","k",s)` into a buffer-backed JSON handler; assert
    `Scan` reports zero leaked forms on each. Plus `Expose()` round-trip.
  - `zero_test.go` — after `ZeroBytes(b)`, every byte of `b` is 0; after
    `ZeroBigInt(n)`, `n.BitLen() == 0`.
  - `antipattern_test.go` — `TestKnownLeak_SecretEmbeddedInStruct`: a
    struct with a `Secret` field passed to `slog.Info` **does** leak the
    sentinel (slog reflects through fields, bypassing `LogValue`).
    Asserted true, heavily commented: this test exists to make the
    "never embed a Secret in a logged struct" rule visible and to detect
    if a future slog change alters the behavior.
- File to modify: `apps/eth-signer-mcp/internal/obs/log_test.go` — add the
  shared leak scan: log a `Secret`-wrapped sentinel through
  `obs`-constructed loggers at every level; `Scan` the captured output;
  assert no form leaks. `obs`'s test importing `signing` is the explicitly
  permitted test-only edge (ADR-008).
- Watch out for (sanitized failures): on any scan failure, report only
  `Sentinel.Name` + the leaked form names — never the buffer, never the
  raw/encoded bytes. Put this warning in a comment above every scan
  assertion.
- `internal/signing` stays stdlib-only in this issue (no go-ethereum import
  needed for these files; `math/big` is stdlib).

**Acceptance Criteria:**
- [ ] `Secret[T]` implements all five interfaces; one test per interface
      proves the redaction fires on the relevant API call.
- [ ] `Expose()` returns the wrapped value bitwise-equal to the input.
- [ ] `NewSentinel` derives at minimum: raw, lowercase hex, uppercase hex,
      std base64, and decimal-scalar forms; `Scan` finds each form when
      planted and returns its name; `Scan` returns empty on clean input.
- [ ] Leak-scan tests green in `signing` (fmt/json/slog paths) and in `obs`
      (logger output at all four levels) — covering the sentinel **and**
      all encoded forms.
- [ ] Known-leak anti-pattern test green (asserts the leak DOES occur) with
      a doc comment explaining the slog-reflection limitation and the
      usage rule.
- [ ] `ZeroBytes`/`ZeroBigInt` tests green; doc comments state the ADR-009
      best-effort limitation.
- [ ] No test failure message can contain sentinel bytes in any form
      (verified by review: all assertions report form names only).

**Testing Notes:**
- Use a distinctive fixture like `[]byte("SENTINEL-7f3a9c-DO-NOT-LOG")` —
  long enough that encoded forms can't collide with innocent output.
- The decimal-scalar form is the rendering Phase 2's key-scalar
  (`big.Int`) would take if leaked via `%d`/`.String()` — that's why it's
  in the form set now.

---

### Issue 1.6: fsperm checks in `cmd`: warn + `--strict-perms` refusal

- **Points:** 1
- **Type:** feature
- **Priority:** P0
- **Blocked by:** 1.3
- **Blocks:** nothing (wired once here; no later re-wiring task exists in
  any phase).
- **Scope:** ~0.5 day

**Description:**
Implement the file-permission startup check as a small file in `cmd` (not a
package): POSIX `Mode().Perm() & 0o077` on the keystore and password-file
paths, with a Windows no-op behind a build tag. Wire it **once**, with
`cfg.StrictPerms`: a world-/group-accessible file produces a WARN log line
by default, and a refusal (exit code 2) when `--strict-perms` is set. The
check runs at startup only, before any transport starts, per the PRD —
it is not re-checked per signing call. When 1.8 lands, the `main` sequence
is: parse → logger → **fsperm** → server → run.

**Implementation Notes:**
- Files to create (all in `apps/eth-signer-mcp/cmd/eth-signer-mcp/`):
  - `fsperm.go` — `//go:build !windows`.
    `checkPerms(path string) (tooOpen bool, err error)`:
    `os.Stat` (follow symlinks — a symlinked keystore checks the target's
    perms; document in a comment); missing file or not
    `Mode().IsRegular()` → error; `Mode().Perm()&0o077 != 0` → tooOpen.
  - `fsperm_windows.go` — `//go:build windows`; always `(false, nil)`
    (Windows ACL semantics differ; documented no-op). This file is what
    the CI `GOOS=windows` compile check (1.2) exercises.
  - `fsperm_test.go` — `//go:build !windows`; temp-file + chmod tests.
- Wiring in `main` (this issue, final form — no later re-wiring):
  ```go
  for _, p := range []string{cfg.KeystorePath, cfg.PasswordPath} {
      tooOpen, err := checkPerms(p)
      if err != nil  { logger.Error(...); exit 2 }   // missing/not regular
      if tooOpen && cfg.StrictPerms {
          logger.Error("refusing to start: file is group/world accessible; chmod 600", "path", p)
          exit 2
      }
      if tooOpen {
          logger.Warn("file is group/world accessible; consider chmod 600 (use --strict-perms to refuse)", "path", p)
      }
  }
  ```
  (Exit through a single error-return path in `main` rather than scattered
  `os.Exit` calls, so deferred cleanup runs; the *process* exit code is 2.)
- Log the path, never file contents.
- Keep the helper unexported; it is `cmd`-local by design (architecture
  puts fsperm logic in `cmd`, not a package).

**Acceptance Criteria:**
- [ ] Mode `0600` file → no warning, process proceeds.
- [ ] Mode `0644` file, no `--strict-perms` → WARN line naming the path and
      suggesting `chmod 600`; process proceeds.
- [ ] Mode `0644` file, `--strict-perms` → process refuses with **exit
      code 2** and an ERROR line; binary-level test asserts the exit code.
- [ ] Missing path or directory-as-path → error + exit 2 (fail fast; a
      typo'd keystore path must not boot a server that can never sign).
- [ ] `GOOS=windows GOARCH=amd64 go build ./...` compiles (no-op variant);
      green in CI's Windows compile step.
- [ ] Both keystore and password-file paths are checked; both warn/refuse
      paths covered by tests.

**Testing Notes:**
- `t.TempDir()` + `os.WriteFile` + explicit `os.Chmod` (umask-independent).
- Warn-path assertion: run the check helper with a buffer-backed logger and
  assert the WARN line's `path` attr. Refuse-path assertion: drive the real
  binary (or the `main` run function in-process) and assert exit code 2.
- Skip chmod-based tests when running as root (`os.Geteuid() == 0`), where
  perms don't bite — guard with `t.Skip`.

---

### Issue 1.7: MCP SDK spike (in-memory transport, HTTP options, middleware order)

- **Points:** 2
- **Type:** spike
- **Priority:** P0
- **Blocked by:** 1.1
- **Blocks:** 1.8 (in-memory transport pattern); Phases 2 and 3 consume the
  findings (tool registration, StreamableHTTPOptions, pipeline order).
- **Scope:** ~1 day

**Description:**
De-risk the top schedule risk — MCP SDK API drift — against the pinned
go-sdk v1.6.1, **before any signing code depends on the SDK**. Deliverables
are (a) a committed rationale note answering the architecture's open
questions, and (b) a **passing `initialize` smoke test** over the SDK's
in-memory transport, committed as a real test (1.8 builds its boot test on
this pattern). This is not a throwaway branch: the note and the smoke test
land on `main`.

Questions the note MUST answer (architecture §Open Questions + plan 1.7):

1. **In-memory transport for tests.** The exact v1.6.1 pattern for
   connecting an `*mcp.Server` to a test client without stdio/network
   (e.g. `mcp.NewInMemoryTransports()` or equivalent — confirm the real
   symbol; if none exists, the `io.Pipe`-based fallback, written out).
2. **`StreamableHTTPOptions` field survey.** Enumerate the actual exported
   fields/knobs on the v1.6.1 Streamable HTTP server handler — DNS-rebinding
   / localhost-protection switches, session behavior (stateful/stateless),
   anything relevant to ADR-006 — with the field names Phase 3 will use.
3. **Middleware pipeline order.** How standard `http.Handler` middleware
   composes *around* the SDK's StreamableHTTPHandler; confirm nothing in
   the SDK prevents the locked nesting `MaxBytesHandler → request-id/logging
   → bearer auth → SDK handler`; record where the SDK's rebinding check
   fires relative to wrappers.
4. **Request-id source.** Whether v1.6.1 exposes a usable request/call id
   on `CallToolRequest` (or server context); decision: SDK id if present,
   else UUIDv4 generated in the handler — record which one Phase 2/3 will
   use.
5. **jsonschema-go tag surface.** What `mcp.AddTool` inference via
   `github.com/google/jsonschema-go/jsonschema`
   (`For[T any](opts *ForOptions) (*Schema, error)`) supports in struct
   tags: confirm `additionalProperties: false` behavior, `pattern`,
   `maxLength` — and list what can't be expressed in tags (those rules go
   to `validate.go` in Phase 2).

**Implementation Notes:**
- Files to create:
  - `apps/eth-signer-mcp/docs/mcp-sdk-spike.md` — the note: one section per
    question, each with the verified v1.6.1 symbol names and a minimal code
    excerpt; a final "decisions" list (request-id source; tag-expressible vs
    validate.go-enforced rules). Date it and pin it to `v1.6.1`.
  - `apps/eth-signer-mcp/internal/server/spike_smoke_test.go` (placed in
    `internal/server` so 1.8 grows it in place) — the smoke test: construct
    a bare `*mcp.Server`, connect a v1.6.1 test client over the in-memory
    transport, complete `initialize`, assert server name/version round-trip.
- Spike scope discipline: time-box exploration of each question; the
  deliverable is the note + the one committed test, not productionized
  code. HTTP findings are *recorded*, not implemented (Phase 3 implements).
- Sketch the `mcp.AddTool` typed-registration call shape (with a throwaway
  struct) far enough to confirm the generic signature and schema inference
  work as the architecture assumes — paste the working snippet into the
  note; do not commit throwaway tools.
- If any finding contradicts the architecture's assumptions (e.g. a
  missing localhost-protection knob), flag it in the note's header AND
  raise it immediately — Phase 3's hardening matrix depends on it.

**Acceptance Criteria:**
- [ ] `docs/mcp-sdk-spike.md` committed, answering all five questions with
      v1.6.1 symbol names and code excerpts; includes the two recorded
      decisions (request-id source; tag surface split).
- [ ] In-memory `initialize` smoke test committed under `internal/server`
      and green in `make test` / CI.
- [ ] The note explicitly confirms (or red-flags) the locked middleware
      nesting and the availability of DNS-rebinding/localhost protection.
- [ ] No throwaway/spike code outside the note and the smoke test reaches
      `main`.

**Testing Notes:**
- The smoke test is the only executable artifact; bound it with a timeout
  context so a hung handshake can't wedge CI.
- Keep the test free of tools — `tools/list` emptiness is asserted in 1.8.

---

### Issue 1.8: Stdio MCP server boots (`initialize` + empty `tools/list`)

- **Points:** 1
- **Type:** feature
- **Priority:** P0
- **Blocked by:** 1.4, 1.7
- **Blocks:** 1.10; Phase 2 registers tools against this server.
- **Scope:** ~0.5 day

**Description:**
Stand up `internal/server` far enough that the binary is a real MCP server
over stdio with **no tools registered**: `server.New` constructs the
`*mcp.Server` (name + version from `obs.Build()`), `RunStdio(ctx)` serves
one session and returns nil on clean EOF. `cmd` wires the full `main`
sequence — parse → logger → fsperm → server → `RunStdio` — with
`signal.NotifyContext` for SIGINT/SIGTERM. The smoke test (grown from 1.7's
spike test) drives `initialize` and asserts `tools/list` returns an empty
list over the in-memory transport.

**Implementation Notes:**
- Files to create/modify:
  - `apps/eth-signer-mcp/internal/server/server.go` —
    ```go
    type Options struct {
        Name, Version string // advertised on MCP initialize
        Logger        *slog.Logger
    }
    func New(opts Options) *Server
    ```
    Phase 1 signature takes no signer — there is nothing to sign yet.
    Phase 2 extends `New` to `New(signer *signing.Signer, opts Options)`
    when tools register (architecture's final signature); note this in a
    comment so the change is expected, not drift.
  - `apps/eth-signer-mcp/internal/server/stdio.go` —
    `func (s *Server) RunStdio(ctx context.Context) error`: run the
    `*mcp.Server` over the SDK's stdio transport; one session; **nil on
    clean EOF**; ctx cancellation returns promptly (error or nil per
    observed SDK behavior — record which in a comment).
  - `apps/eth-signer-mcp/internal/server/server_test.go` — grow 1.7's
    smoke test: `initialize` round-trips (name/version from Options match
    what the client sees), then `tools/list` returns an **empty** list.
    Add a ctx-cancel test: cancel the context, assert the serve call
    returns within 1s (catches the hung-loop regression class).
  - `apps/eth-signer-mcp/cmd/eth-signer-mcp/main.go` — final Phase 1
    wiring inside the cli Action:
    ```go
    ctx, stop := signal.NotifyContext(ctx, os.Interrupt, syscall.SIGTERM)
    defer stop()
    // parse (done) → logger := obs.NewLogger(cfg.LogLevel)
    // → fsperm checks (1.6) → srv := server.New(server.Options{
    //       Name: "eth-signer-mcp", Version: obs.Build().Version,
    //       Logger: logger})
    // → if cfg.HTTP { return error: "Streamable HTTP transport lands in
    //       Phase 3" } else { return srv.RunStdio(ctx) }
    ```
    `--http` in Phase 1: clean error message + non-zero exit (flags exist
    per 1.3; the transport lands in Phase 3). Never the word "SSE".
  - `apps/eth-signer-mcp/cmd/eth-signer-mcp/main_test.go` — binary-level
    smoke: with temp keystore/password files (contents irrelevant — no
    vault exists yet; fsperm only needs them to exist with mode 0600), the
    binary starts on stdio, completes `initialize` driven over the child
    process's stdin/stdout, and exits 0 on stdin EOF; `--http` with a token
    file exits non-zero with the stable not-yet-available message.
- `internal/server` imports: the MCP SDK, `signing`+`obs` allowed by
  ADR-008 (only `obs`-typed values arrive injected in practice; no
  `signing` import needed until Phase 2).
- Stdout discipline: nothing in the server or cmd may print to stdout —
  stdout carries MCP frames; logs are stderr-only via `obs` (already
  guaranteed by 1.4; keep `fmt.Println` out of `cmd`).

**Acceptance Criteria:**
- [ ] In-memory smoke test: `initialize` completes; advertised server
      name/version equal `Options.Name`/`Options.Version`; `tools/list`
      returns an empty list.
- [ ] Ctx-cancel test: cancelling the serve context returns within 1s.
- [ ] `RunStdio` returns nil on clean EOF (binary-level: closing stdin
      after a completed session ⇒ exit 0).
- [ ] Binary-level smoke: real `initialize` over child-process stdio
      succeeds with `--keystore`/`--password-file` pointing at 0600 temp
      files.
- [ ] `--http` (with token file) exits non-zero with a stable message that
      the Streamable HTTP transport arrives in Phase 3.
- [ ] SIGINT/SIGTERM wired via `signal.NotifyContext`; SIGINT during an
      idle stdio session exits cleanly (binary-level assertion).
- [ ] No stdout writes from `cmd`/`server` outside MCP frames.

**Testing Notes:**
- Bound all child-process tests with ≤10s timeout contexts.
- Version under `go test` is `<unknown>` per 1.4 — assert presence, not a
  concrete value, in the in-memory test; the binary-level `--version`
  assertion already lives in 1.4.

---

### Issue 1.9: Slim depguard config + offline-import test scaffold

- **Points:** 1
- **Type:** chore
- **Priority:** P0
- **Blocked by:** 1.5 (the `internal/signing` package must exist for rules
  and test to bind to)
- **Blocks:** 1.10. Phase 2 (task 2.8) makes the offline gate load-bearing.
- **Scope:** ~0.5 day

**Description:**
Land both build-time guard rails in their Phase 1 form. (a) A **slim
depguard block** in the root `.golangci.yml` enforcing **package-level
import edges only** (ADR-008): `internal/signing` may not import
`internal/server`, `internal/obs`, or any HTTP/RPC client package
(`net/http`, `net/rpc`, go-ethereum `ethclient`/`rpc`); `internal/obs`
imports nothing internal; `internal/server` imports only `signing` + `obs`;
only `cmd/eth-signer-mcp` imports all; test files may additionally import
`signing` (the `obs` leak test uses the Sentinel). Include a
**deliberate-violation test** that proves the rule actually fires — a
config that silently never matches is worse than no config.
(b) The ADR-007 **offline-import test scaffold** in
`internal/signing/offline_test.go`: walk the package's transitive imports
via `golang.org/x/tools/go/packages` and fail on any forbidden client
package — **vacuous in Phase 1** (signing has no go-ethereum imports yet),
marked as such with an explicit in-code comment, load-bearing from Phase 2.

State honestly in both artifacts: depguard sees package import paths, not
symbols — interface-vs-concrete discipline is code-review enforced.

**Implementation Notes:**
- Files to create/modify:
  - `.golangci.yml` (repo root) — add `depguard` to `linters.enable`
    (preserving `revive`, `misspell`, `unconvert`) and a
    `linters.settings.depguard.rules` block. golangci-lint **v2 syntax
    gotchas** (budget a 15-minute syntax check against the v2 docs before
    writing the full block): the schema is `version: "2"`; rules live under
    `linters.settings.depguard.rules.<name>` with `files:` (path globs —
    `$test` suffix convention for test files differs between versions;
    verify), `deny:` lists of `{pkg, desc}`, and `list-mode: lax` (deny
    only what's listed). Every `deny` entry carries a `desc:` citing
    ADR-007/ADR-008 so a failing lint points at the rationale. Rule
    sketch (verify globs fire before committing):
    - `signing-offline-and-leaf`: files
      `**/apps/eth-signer-mcp/internal/signing/**`; deny `net/http`,
      `net/rpc`, `github.com/ethereum/go-ethereum/ethclient`,
      `github.com/ethereum/go-ethereum/rpc`,
      `.../apps/eth-signer-mcp/internal/server`,
      `.../apps/eth-signer-mcp/internal/obs`.
    - `obs-leaf`: files `**/apps/eth-signer-mcp/internal/obs/**`,
      excluding `**/*_test.go` (the leak test's `signing` import is the
      permitted test-only edge); deny all
      `.../apps/eth-signer-mcp/internal/*` paths.
    - `server-edges`: files `**/apps/eth-signer-mcp/internal/server/**`;
      deny `.../cmd/**` (nothing imports cmd anyway — `package main` — but
      the deny documents the edge; `signing` and `obs` are allowed by
      omission under lax mode).
  - `apps/eth-signer-mcp/internal/signing/offline_test.go` — the ADR-007
    scaffold:
    - `go get golang.org/x/tools@latest` (test-only dep; record version).
    - `packages.Load` with `NeedImports|NeedDeps|NeedName` on
      `github.com/rootwarp/blockchain-ai-tools/apps/eth-signer-mcp/internal/signing`;
      walk `pkg.Imports` recursively with a visited set (a ~20-line helper
      inside the test file — no sub-package ceremony for one walker); fail
      if `net/http`, `net/rpc`,
      `github.com/ethereum/go-ethereum/ethclient`, or
      `github.com/ethereum/go-ethereum/rpc` is reachable.
    - The mandatory in-code comment, verbatim intent: *"This test is the
      structural enforcement of ADR-007 (PRD P0-SIGN-5/P0-SEC-6). It
      passes VACUOUSLY in Phase 1 — internal/signing imports neither
      go-ethereum nor any network package yet, so an empty result proves
      nothing about offline-ness. It becomes load-bearing in Phase 2 when
      accounts/keystore, core/types, and crypto land; do not treat a green
      run before then as an offline guarantee."*
  - `apps/eth-signer-mcp/lint_test.go` (module root, or alongside
    `offline_test.go`) — the **deliberate-violation test**
    `TestDepguardRuleFires`:
    1. `t.Skip` if `golangci-lint` is not on `$PATH` (always present in CI
       via 1.2).
    2. Write `internal/signing/zz_depguard_violation.go` guarded by
       `//go:build depguard_violation` containing `import _ "net/http"`;
       remove it via `t.Cleanup` (runs even on failure — the tree stays
       clean).
    3. Run `golangci-lint run --build-tags depguard_violation
       ./internal/signing/...` from the module dir (the root config is
       found by upward search).
    4. Assert non-zero exit AND output containing a depguard diagnostic
       naming `net/http`. This proves the glob actually matches — the
       known failure mode of depguard configs is a path pattern that never
       fires.
- Watch out for: `make lint` must stay green on the real tree (positive
  case) — run it after adding the block, before the violation test.
- Watch out for: x/tools stays a test-only dependency; `make build` must
  not pull it into the binary (it can't — `_test.go` only — but assert via
  `go list -deps ./cmd/...` once if paranoid).

**Acceptance Criteria:**
- [ ] `.golangci.yml` contains the depguard block with the three rules
      above; every `deny` entry has a `desc:` citing ADR-007 or ADR-008;
      a comment states the honest scope (package edges only;
      interface-vs-concrete is code-review enforced).
- [ ] `make lint` exits 0 on the real tree (depguard positive case).
- [ ] `TestDepguardRuleFires` is green: the deliberate violation makes
      `golangci-lint` fail with a depguard message naming `net/http`, and
      the violation file is removed afterward even when assertions fail.
- [ ] `offline_test.go` compiles, runs in `make test`, and passes
      (vacuously); the explicit vacuous-in-Phase-1 comment is present.
- [ ] `golang.org/x/tools` is pinned in `go.mod`; `make build` output is
      unaffected.
- [ ] CI (1.2) is green with both gates active.

**Testing Notes:**
- The violation test doubles as the recurring mutation check; Phase 4's
  final sweep re-runs the same mutation against the then-load-bearing
  offline test (its named re-check), so keep the helper that writes/removes
  the violation file easy to point at other forbidden imports.
- `packages.Load` must run from inside the module; `make test` cds per
  module, so CI is fine — note it for anyone running `go test` from the
  repo root.

---

### Issue 1.10: Phase polish pass (refactor/simplify, lint sweep, docs)

- **Points:** 1
- **Type:** chore
- **Priority:** P0
- **Blocked by:** 1.1–1.9 (all phase work merged)
- **Blocks:** Phase 2 entry.
- **Scope:** ~0.5 day

**Description:**
Close the phase with the in-phase polish task the planning principles
mandate: refactor/simplify what the phase produced now that all pieces
exist, run a full lint sweep with zero unjustified suppressions, and bring
the docs in line with what was actually built. This is a quality gate with
concrete, checkable outcomes — not a free-form cleanup day.

**Implementation Notes:**
- Refactor/simplify targets (review each; act where it pays):
  - `cmd/eth-signer-mcp`: one coherent error-return path through the cli
    Action (no scattered `os.Exit`); `config` validation and fsperm checks
    read as a single startup sequence matching architecture Flow D.
  - `internal/server`: the spike smoke test (1.7) and the boot test (1.8)
    should now be one coherent test file with shared in-memory-transport
    helpers, not two overlapping setups.
  - `internal/signing`: consistent doc comments across `secret.go` /
    `zero.go` / `sentinel.go`; the sanitized-failure-message rule stated
    once in the Sentinel doc and referenced (not re-pasted) elsewhere.
  - Shrink 1.1's `tools.go` blank-import file to only the pins not yet
    held by real imports (the SDK and urfave/cli are now genuinely
    imported; jsonschema-go likely still needs the tools file until
    Phase 2).
- Lint sweep: `make lint && make vet && make fmt` clean. Audit every
  `//nolint` in the module.
- Docs touch-up:
  - `apps/eth-signer-mcp/README.md` — create/update the app README stub:
    what the binary is (one paragraph), Phase 1 status (boots stdio MCP,
    no tools yet — signing lands in Phase 2), build/run instructions
    (`make build`, flags table from 1.3), the `cmd/` layout note from 1.1,
    and a pointer to `docs/mcp-sdk-spike.md`.
  - Root `CLAUDE.md` — per its "Maintaining this file" section: the first
    real module now exists; update the project-status paragraph and add
    the app's command notes (e.g. running its tests, the golangci-lint
    requirement for `TestDepguardRuleFires`).
  - Package docs present and accurate on all four packages.
- Do NOT add features, rename public API, or restructure packages — this
  pass polishes Phase 1's surface so Phase 2 starts clean.

**Acceptance Criteria:**
- [ ] `make lint`, `make vet`, `make test`, `make build` all green; CI
      green on the polish commit.
- [ ] `gofmt -s -l` over the module reports nothing (`make fmt` produces
      no diff).
- [ ] **Zero lint suppressions without justification:** every `//nolint`
      directive (if any remain) carries a specific linter name and a
      same-line justification comment; `grep -rn "//nolint" apps/` output
      reviewed and each hit defensible.
- [ ] **No dead code:** no unused exported identifiers, no leftover spike
      scaffolding, no orphan helpers (verified by `staticcheck`'s unused
      pass in `make lint` plus a manual read of each file); the `tools.go`
      pin file contains only pins not held by real imports.
- [ ] `apps/eth-signer-mcp/README.md` exists with the stub content listed
      above; root `CLAUDE.md` project-status section reflects the first
      real module.
- [ ] All four packages (`cmd/eth-signer-mcp`, `internal/signing`,
      `internal/server`, `internal/obs`) have package doc comments
      consistent with the architecture's one-sentence responsibilities.
- [ ] Phase 1 exit criteria (top of this file) all check off — this issue
      is the gate that walks the list and ticks each box.

**Testing Notes:**
- No new tests required; the gate is that everything existing stays green
  after refactoring. If a refactor breaks a test, the refactor is wrong —
  Phase 1's tests encode the phase contract.

---

## Risk Flags

On top of the project-plan risk register, the Phase-1-local risks:

- **SDK v1.6.1 surprises.** The spike (1.7) exists precisely to surface
  them now, while the blast radius is one task, not the signing core. If a
  finding contradicts the architecture (e.g. no localhost-protection knob),
  escalate from the spike note immediately — Phase 3 depends on it.
- **urfave/cli v3 idiom drift between patch releases.** Pinned exactly in
  1.1; `--help`/`--version`/bad-flag smoke-tested at the binary level
  (1.3/1.4) and run in CI (1.2) from this phase on.
- **depguard glob that never fires.** A path pattern that silently matches
  nothing passes lint forever. Mitigation: 1.9's deliberate-violation test
  is a permanent, automated proof the rule bites — not a one-off manual
  mutation in a PR description.
- **Vacuous offline test mistaken for a guarantee.** The ADR-007 scaffold
  is green by emptiness in Phase 1. Mitigation: the mandatory in-code
  comment (1.9) says so in exactly those terms; Phase 2 task 2.8 is the
  explicit load-bearing switchover.
- **Sentinel re-leak via test failure output.** A failing leak test invites
  `t.Logf("got: %s", buf)`. Mitigation: the sanitized-failure-message rule
  is a phase convention, `Sentinel.Scan` returns form *names* so good
  failure messages are the easy path, and 1.10's review sweeps for
  violations.
- **`ReadBuildInfo` emptiness under `go test`.** Rich `--version` fields
  are absent in test binaries. Mitigation: `<unknown>` fallbacks are part
  of `obs.Build()`'s contract (1.4) and tested; binary-level assertions
  pin the real-build behavior.
