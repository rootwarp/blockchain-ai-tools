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
- `.claude/skills/` — Claude Code skills that drive the apps from natural-language
  requests. See [Skills](#skills) below.

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

## Skills

[Claude Code skills](https://docs.claude.com/en/docs/claude-code/skills) under
`.claude/skills/` turn natural-language requests into the right calls against the
apps. They split the transaction lifecycle so the signer can stay strictly offline:
the skills make the outbound RPC calls, the signer only signs.

- [`eth-tx-builder`](./.claude/skills/eth-tx-builder/README.md) — **build** a
  ready-to-sign `TxRequest` JSON for the signer's `sign_transaction` tool from a
  network, destination, and amount (v1: send-ETH, EIP-1559). Queries the sender's
  nonce and fees over RPC; does not sign.
- [`eth-jsonrpc`](./.claude/skills/eth-jsonrpc/README.md) — **balance** (`eth_getBalance`
  of an EOA) and **broadcast** (`eth_sendRawTransaction` for an already-signed raw
  tx, optionally waiting for the receipt). Does not sign and does not build.

Together with the signer they cover the full path — **build → sign → broadcast** —
plus balance queries, on `mainnet` and `hoodi`:

```
eth-tx-builder        eth-signer-mcp          eth-jsonrpc
   (build)      →    (sign, offline)    →    (broadcast)
```

Each skill is stdlib-only Python with its own `SKILL.md`, helper script, unit
tests (mocked RPC — no live network), and `README.md`.

## License

See [`LICENSE`](./LICENSE) for terms.
