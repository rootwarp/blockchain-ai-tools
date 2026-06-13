# blockchain-ai-tools

Go monorepo for tools combining blockchain / crypto primitives with AI workflows.

## Layout

- `apps/` — runnable Go programs; each is its own Go module under
  `github.com/rootwarp/blockchain-ai-tools/apps/<name>`. The first app is
  `apps/eth-signer-mcp` — a strictly-offline Ethereum-signing MCP server.
  See its [README](./apps/eth-signer-mcp/README.md) for full documentation.
- `libs/` — shared libraries; each is its own Go module. Currently empty
  (`.gitkeep` only). A library appears when a second consumer needs it.
- `go.work` — workspace tying the modules together. Managed automatically by
  `scripts/new-module.sh`; do not hand-edit unless necessary.
- `scripts/` — `new-module.sh` (module scaffolding), demo clients, and vector
  regeneration helpers.

## First-time usage

All commands run from the repo root.

```sh
make help              # list all available targets
make new-app name=foo  # scaffold a new app module
make new-lib name=foo  # scaffold a new library module
make build             # build all modules; binaries go to bin/
make test              # run tests in all modules
make test-race         # run tests with the race detector
make lint              # run golangci-lint per module
```

## Conventions

- Go toolchain: 1.26 (see `go.work`).
- Library package names drop separators (e.g. `libs/chain-client`
  becomes package `chainclient`), per Go convention.
- Prefer `make new-app` / `make new-lib` over hand-creating modules so
  `go.work` stays in sync.

## Apps

- [`apps/eth-signer-mcp`](./apps/eth-signer-mcp/README.md) — strictly-offline
  Ethereum-signing MCP server (stdio and Streamable HTTP transports).

## License

See [`LICENSE`](./LICENSE) for terms.
