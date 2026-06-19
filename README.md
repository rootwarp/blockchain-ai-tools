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

### Using `eth-signer-mcp`

Build the binary, then point an MCP client at it. The server signs a
fully-specified transaction and returns a broadcast-ready signed RLP; it never
talks to the network. Two MCP tools are exposed: `sign_transaction` and
`get_address`.

```sh
# 1. Build (from the repo root) → bin/eth-signer-mcp
make build
./bin/eth-signer-mcp --version
```

**stdio transport** (Claude Desktop-style clients) — add to the client's MCP
config (e.g. `claude_desktop_config.json`); restart the client and the
`sign_transaction` / `get_address` tools appear in the tool-approval dialog:

```json
{
  "mcpServers": {
    "eth-signer-mcp": {
      "command": "/ABSOLUTE/PATH/TO/bin/eth-signer-mcp",
      "args": [
        "--keystore",     "/ABSOLUTE/PATH/TO/keystore.json",
        "--password-file","/ABSOLUTE/PATH/TO/password.txt"
      ]
    }
  }
}
```

**Streamable HTTP transport** — loopback-only, bearer-authenticated:

```sh
TOKEN_FILE=$(mktemp /tmp/eth-signer-mcp-token.XXXXXX); chmod 600 "$TOKEN_FILE"
openssl rand -hex 32 > "$TOKEN_FILE"

./bin/eth-signer-mcp \
  --keystore       /ABSOLUTE/PATH/TO/keystore.json \
  --password-file  /ABSOLUTE/PATH/TO/password.txt \
  --http --http-addr 127.0.0.1:0 \
  --http-auth-token-file "$TOKEN_FILE"
# stderr: eth-signer-mcp listening on 127.0.0.1:<PORT>
```

Every HTTP request must carry `Authorization: Bearer <token>` (missing/wrong →
`401`; non-loopback `Host` → `403`). stdout is reserved for MCP frames — all
logs go to stderr as JSON. The repo ships low-value test keystores under
`apps/eth-signer-mcp/internal/signing/testdata/` for demos/CI; **never send real
funds to them** — point `--keystore` / `--password-file` at your own chmod-600
files. Full flag reference, security posture, and troubleshooting:
[`apps/eth-signer-mcp/README.md`](./apps/eth-signer-mcp/README.md).

## Skills

[Claude Code skills](https://docs.claude.com/en/docs/claude-code/skills) under
`.claude/skills/` turn natural-language requests into the right calls against the
apps. They split the transaction lifecycle so the signer can stay strictly offline:
the skills make the outbound RPC calls, the signer only signs.

- [`eth-ops`](./.claude/skills/eth-ops/README.md) — **the front door.** An
  instructions-only orchestrator that classifies a request and drives the others:
  it answers **reads** directly (an account's holdings — native ETH + decoded
  ERC-20 balances — a single balance, any `eth_*` read, or node diagnostics) and
  conducts **writes** (send ETH, ERC-20 transfer/approve/transferFrom, or
  broadcast a signed tx) through a gated `build → sign → broadcast` pipeline with
  two explicit human confirmations (before signing, before broadcasting).
- [`eth-tx-builder`](./.claude/skills/eth-tx-builder/README.md) — **build** a
  ready-to-sign `TxRequest` JSON for the signer's `sign_transaction` tool: a
  native ETH send or an ERC-20 transfer/approve/transferFrom. Queries the sender's
  nonce and fees over RPC; does not sign.
- [`eth-jsonrpc`](./.claude/skills/eth-jsonrpc/README.md) — the RPC companion:
  **balance**, **broadcast** (`eth_sendRawTransaction`, optionally waiting for the
  receipt), generic **call** / **batch** of any `eth_*` read, and **net-version** /
  **client-version** diagnostics. Does not sign and does not build.

Together with the signer they cover the full path — **build → sign → broadcast** —
plus reads, on `mainnet` / `hoodi` / `sepolia` / `holesky`:

```
eth-tx-builder        eth-signer-mcp          eth-jsonrpc
   (build)      →    (sign, offline)    →    (broadcast)
              orchestrated by  eth-ops
```

### Using the skills

In a Claude Code session in this repo, just ask `eth-ops` in plain language — it
routes to the right skill, confirms the network, and gates every fund-moving step:

```text
"What does 0xd8dA…6045 hold on mainnet?"     → holdings read (ETH + ERC-20)
"ETH balance of 0x… on hoodi?"                → single balance
"Send 0.001 ETH to 0x…dEaD on hoodi"          → gated build → sign → broadcast
"Transfer 50 USDC to 0x… on mainnet"          → gated ERC-20 pipeline
"Broadcast this signed raw tx on hoodi: 0x02f8…"  → gated broadcast only
```

Writes require the `eth-signer` MCP server (above) connected for `get_address` /
`sign_transaction`. The helper scripts are also runnable directly (stdlib Python,
no AI needed) — e.g. a read or a build:

```sh
python3 .claude/skills/eth-jsonrpc/eth_rpc.py balance \
  --network mainnet --address 0xd8dA6BF26964aF9D7eEd9e03E53415D37aA96045

python3 .claude/skills/eth-tx-builder/build_send_eth.py \
  --network hoodi --to 0x000000000000000000000000000000000000dEaD \
  --amount-gwei 1000000 --sender 0x<your-address>
```

Each skill is stdlib-only Python with its own `SKILL.md`, helper script, unit
tests (mocked RPC — no live network), and `README.md` (`eth-ops` is
instructions-only — it bundles no code).

## License

See [`LICENSE`](./LICENSE) for terms.
