# eth-signer-mcp

A small, auditable, **strictly offline** Model Context Protocol (MCP) server that
signs Ethereum transactions using a locally-stored Web3 Secret Storage keystore.
An AI agent (or any MCP client) sends a fully-specified transaction and receives a
broadcast-ready signed transaction back. The server performs **no network calls**
and **never broadcasts** — it signs exactly what it is told to sign.

See [`../../plan/prd.md`](../../plan/prd.md) for the full product requirements and
[`../../plan/architecture.md`](../../plan/architecture.md) for the design.

## Status — Phase 1 (Foundations) complete

The binary boots as a real MCP server over **stdio** and answers `initialize` and
an (empty) `tools/list`. The signing tools (`sign_transaction`, `get_address`)
arrive in **Phase 2**; the Streamable HTTP transport arrives in **Phase 3**.

What works today:

- Full CLI flag set (frozen for v1) parsed via `urfave/cli` v3.
- Structured **JSON logging** to stderr with `--log-level`; rich `--version`
  (version, commit, build date, Go version).
- **File-permission startup check**: warns on group/world-accessible keystore or
  password files; refuses (exit code 2) with `--strict-perms`.
- Secret-hygiene primitives (`Secret[T]`, best-effort zeroing, leak-scan Sentinel)
  ready for the Phase 2 signing core.
- Stdio MCP server (`initialize` + empty `tools/list`). `--http` currently exits
  with a clear "arrives in Phase 3" message.

## Build & run

All commands run from the repo root.

```sh
make build                 # produces bin/eth-signer-mcp (-trimpath -buildvcs=true)
./bin/eth-signer-mcp --version
./bin/eth-signer-mcp --help

# boot the stdio MCP server (keystore/password files should be chmod 600):
./bin/eth-signer-mcp --keystore ./key.json --password-file ./pass.txt
```

Per-module test / lint (workspace mode is active, so plain `go` works inside the
module dir too):

```sh
make test                  # all modules
make lint                  # golangci-lint v2 (incl. the depguard import-edge rules)
cd apps/eth-signer-mcp && go test ./...
```

> `TestDepguardRuleFires` (in `internal/signing`) shells out to `golangci-lint`;
> it `t.Skip`s when the binary is not on `$PATH`. CI always has it.

## Flags

| Flag | Type | Default | Notes |
|------|------|---------|-------|
| `--keystore` | string | — (required) | Web3 Secret Storage JSON keystore path |
| `--password-file` | string | — (required) | password file path; never inline |
| `--http` | bool | `false` | selects Streamable HTTP transport (Phase 3) |
| `--http-addr` | string | `127.0.0.1:0` | bind address; ephemeral port by default |
| `--http-auth-token-file` | string | — | required when `--http` is set |
| `--chain-id` | uint64 | unset | optional guard; `0` is rejected (replay-unprotected) |
| `--strict-perms` | bool | `false` | refuse (exit 2) on group/world-accessible secret files |
| `--log-level` | string | `info` | `debug` \| `info` \| `warn` \| `error` |
| `--help` / `--version` | — | — | `urfave/cli` defaults; version via `internal/obs` |

## Layout

```
apps/eth-signer-mcp/
├── cmd/eth-signer-mcp/    # composition root: flags, config, fsperm, wiring, run loop
├── internal/signing/      # everything that touches key material (offline; leaf node)
├── internal/server/       # MCP server + transports (stdio now; Streamable HTTP in Phase 3)
├── internal/obs/          # JSON slog logger + build info (stdlib-only; leaf node)
├── docs/mcp-sdk-spike.md  # verified MCP Go SDK v1.6.1 findings (issue 1.7)
└── tools.go               # //go:build tools — holds dependency pins not yet imported
```

**Entry point under `cmd/`:** the module root is deliberately package-free so the
binary name, the `cmd/` convention, and the three `internal/` packages mirror the
four-package architecture; `go build ./cmd/eth-signer-mcp` names the binary
correctly. Do not reintroduce a root-level `main.go`.

The one load-bearing boundary — **`internal/signing` must never reach the
network** — is enforced at build time by `internal/signing/offline_test.go`
(ADR-007) and the `depguard` import-edge rules in the root `.golangci.yml`
(ADR-008). depguard checks package import *paths*, not symbols; interface-vs-
concrete discipline is code-review enforced.
