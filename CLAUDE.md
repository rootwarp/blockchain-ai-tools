# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Project status

A Go monorepo for tools combining blockchain/crypto with AI. The first app,
**`apps/eth-signer-mcp`**, has completed Phase 1 (Foundations): a strictly-offline
Ethereum-signer MCP server that boots over stdio (`initialize` + empty `tools/list`),
with full CLI flags, JSON logging, build-info `--version`, file-permission startup
checks, and secret-hygiene primitives. The signing tools land in Phase 2; the
Streamable HTTP transport in Phase 3. See
[`apps/eth-signer-mcp/README.md`](apps/eth-signer-mcp/README.md) and the planning
set under [`plan/`](plan/) (PRD, architecture, phased issues).

`libs/` is still empty (`.gitkeep` only) — shared libraries appear when a second
consumer needs them.

## Architecture

Multi-module Go workspace tied together by a top-level `go.work`:

- **`apps/<name>/`** — runnable programs. Each is its own Go module
  (`github.com/rootwarp/blockchain-ai-tools/apps/<name>`) with a `main` package.
- **`libs/<name>/`** — shared libraries. Each is its own Go module
  (`github.com/rootwarp/blockchain-ai-tools/libs/<name>`).
- **`go.work`** — the workspace. Lists each module under `use (...)`. Managed
  automatically by the scaffolding script; don't hand-edit unless necessary.
- **`scripts/new-module.sh`** — backs `make new-app` / `make new-lib`; creates a
  module (`go.mod` + a starter file) and runs `go work use` to wire it in.

Every module is independent: it has its own `go.mod`, its own dependency set, and can
be built/tested on its own. The Makefile discovers modules dynamically by finding
`go.mod` files under `apps/` and `libs/`, so all targets work whether there are zero
modules or many.

## Commands

All commands run from the repo root. `make help` lists everything.

- **Add a module:** `make new-app name=foo` or `make new-lib name=foo`
  (names: lowercase, start with a letter; `-`/`_` allowed).
- **Build:** `make build` — app binaries go to `bin/` (gitignored); libs are compile-checked.
- **Test (all):** `make test`
- **Run a single test:** `cd apps/foo && go test -run '^TestName$' ./...`
  (workspace mode is active, so plain `go test`/`go build` work inside any module dir).
- **Lint:** `make lint` — runs `golangci-lint` (v2 config in `.golangci.yml`) per module.
  Requires `golangci-lint` on PATH.
- **Format:** `make fmt` (gofmt `-s` over the whole tree). **Vet:** `make vet`.
  **Tidy deps:** `make tidy` (`go mod tidy` per module). **Clean:** `make clean`.

## Conventions

- Go toolchain: 1.26 (see `go.work`). Lint via golangci-lint v2.
- Library package names drop separators from the dir name (e.g. `libs/chain-client`
  → package `chainclient`), per Go convention.
- Prefer `make new-app`/`make new-lib` over hand-creating modules so `go.work` stays in sync.

## App: eth-signer-mcp

- Full docs: [`apps/eth-signer-mcp/README.md`](apps/eth-signer-mcp/README.md).
  Four-package layout: `cmd/eth-signer-mcp` (composition root) + `internal/signing`
  (key material; offline leaf), `internal/server` (MCP/transports),
  `internal/obs` (logging; stdlib-only leaf).
- **Build-time invariants** enforced on `make lint` / `make test`:
  - `internal/signing/offline_test.go` (ADR-007) fails if the signing package
    transitively imports any HTTP/RPC client — the "offline" guarantee.
  - `depguard` rules in `.golangci.yml` (ADR-008) pin the package import edges
    (paths only, not symbols).
  - `TestDepguardRuleFires` (in `internal/signing`) shells out to `golangci-lint`
    and `t.Skip`s if it is not on `$PATH` — so **run `make lint`/CI with
    `golangci-lint` installed** to exercise it. CI installs a pinned v2.x.
- Dependency pins live in `go.mod`; `tools.go` (`//go:build tools`) holds pins not
  yet imported by real code (currently just go-ethereum, used from Phase 2).

## Maintaining this file

Keep the commands and architecture above accurate as the repo grows. When the first real
modules land, document any cross-module architecture (shared interfaces, how apps consume
libs) that can't be understood from a single file.
