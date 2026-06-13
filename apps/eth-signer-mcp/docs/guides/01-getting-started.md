# Getting Started

This guide takes you from a fresh clone of the monorepo to a **verified Ethereum
signature** in five steps: build the binary, point it at a keystore, start an MCP
session over stdio, call `get_address`, and sign the legacy-mainnet golden
transaction. At the end you confirm the signed RLP is **byte-identical** to the
committed golden vector — the fastest proof that your build signs correctly.

`eth-signer-mcp` is **strictly offline**: it signs fully-specified transactions
and returns broadcast-ready signed RLP, but it **never broadcasts** and makes no
outbound network calls by construction (`internal/signing` imports no HTTP/RPC
client — ADR-007 / ADR-008). Submitting the signed transaction is the caller's
job. (source: ../../README.md §1)

> This guide covers the **stdio** transport, which is the default. For the
> Streamable HTTP transport (bearer auth, loopback-only bind, 401/403 surfaces),
> see [HTTP transport](03-http-transport.md).

---

## 1. Prerequisites

- **Go 1.26 toolchain.** The workspace pins `1.26` in `go.work`; the app is a
  single statically-linked binary with no runtime dependencies. (source: CLAUDE.md, ../../README.md §11)
- **The repo cloned**, with this guide living under
  `apps/eth-signer-mcp/docs/guides/`. Run all build commands from the **monorepo
  root** (`.../blockchain-ai-tools`).
- **An MCP client** for Step 4. Any MCP client works; this guide shows a Claude
  Desktop-style `mcpServers` config block.

`curl` and `openssl` are **not** needed for this guide — they are only used by the
HTTP guide for token generation and request crafting.

---

## 2. Step 1 — Build the binary

From the monorepo root:

```sh
make build
```

`make build` compiles every module with `-trimpath -buildvcs=true` and drops app
binaries into `bin/` (which is gitignored). The signer lands at
`bin/eth-signer-mcp`. (source: Makefile `build` target, ../../README.md §2)

Verify the build:

```sh
./bin/eth-signer-mcp --version
./bin/eth-signer-mcp --help
```

`--version` prints a single line in this shape:

```
eth-signer-mcp version <Version> (commit <Commit>, built <Date>, <GoVersion>)
```

All four fields come from `runtime/debug.ReadBuildInfo`. A **tagged release**
build populates all four. A plain `go build` from source without a VCS tag shows
`<Version>` as `(devel)` and `<Commit>` / `<Date>` as `<unknown>` — this is
expected for local development builds and does not affect signing behaviour.
(source: ../../README.md §2, cmd/eth-signer-mcp/main.go)

`--help` lists all 10 flags. Only two are required to run:

| Flag | Required | Default | Purpose |
|------|----------|---------|---------|
| `--keystore` | **yes** | — | Web3 Secret Storage JSON keystore path |
| `--password-file` | **yes** | — | File holding the keystore password (never inline a password) |
| `--http` | no | `false` | Enable Streamable HTTP transport (requires `--http-auth-token-file`) — see the HTTP guide |
| `--http-addr` | no | `127.0.0.1:0` | Loopback bind address; only meaningful with `--http` |
| `--http-auth-token-file` | no | — | Bearer token file; required when `--http` is set |
| `--chain-id` | no | unset (no guard) | Optional chain-id guard; mismatches are refused; `0` is rejected at startup |
| `--strict-perms` | no | `false` | Refuse startup (exit 2) if a secret file is group/world-readable; default only warns |
| `--log-level` | no | `info` | `debug` \| `info` \| `warn` \| `error` (case-insensitive) |
| `--help` / `-h` | — | — | Show help |
| `--version` / `-v` | — | — | Print the version line |

(source: cmd/eth-signer-mcp/config.go, cmd/eth-signer-mcp/main.go, ../../README.md §5)

For a field-by-field tool contract, see [Tool reference](04-tool-reference.md).

---

## 3. Step 2 — Pick a keystore and password file

For a first run, the repo ships a **committed test fixture** so you can produce a
signature without creating any key material:

```
apps/eth-signer-mcp/internal/signing/testdata/keystore-light.json
apps/eth-signer-mcp/internal/signing/testdata/password.txt
```

- The keystore is a **light-scrypt** keystore (N=4096), so each signing call costs
  ~50 ms — fast enough for interactive demos. (source: ../demo.md "Fixture keystore")
- The password file contains exactly `test-only-password-do-not-reuse\n`
  (32 bytes). (source: internal/signing/testdata/password.txt)
- The fixture address is `0x9858EfFD232B4033E47d90003D41EC34EcaEda94`.

> **⚠ TEST-ONLY KEY MATERIAL — DO NOT SEND REAL FUNDS.**
> This keystore and password are committed to the repository for demos and CI.
> The private key is public to anyone who clones the repo. Never send funds to
> `0x9858EfFD232B4033E47d90003D41EC34EcaEda94`, and never reuse this keystore or
> password for anything real. See `internal/signing/testdata/README.md` for the
> full disclosure.

For real keys, point `--keystore` and `--password-file` at your own chmod-600
files — and prefer a standard-scrypt keystore created by `geth account new`.
Production setup, file permissions, the `--strict-perms` gate, and chain-id
guarding are covered in [Production hardening](05-production-hardening.md).

A note on scrypt latency you will feel later: the KDF inside `keystore.DecryptKey`
runs on **every** `sign_transaction` call — the decrypted key is never cached
(ADR-010, no warm path). Standard-scrypt keystores (N=262144, geth default) cost
~0.5–1 s per call; the light fixture costs ~50 ms. (source: ../../README.md §7)

---

## 4. Step 3 — Run the server over stdio

stdio is the **default** transport — no `--http` flag. You can launch the binary
directly to confirm it starts (it will then wait on stdin for MCP frames):

```sh
./bin/eth-signer-mcp \
  --keystore      apps/eth-signer-mcp/internal/signing/testdata/keystore-light.json \
  --password-file apps/eth-signer-mcp/internal/signing/testdata/password.txt
```

### Two output channels — keep them separate

- **stdout is reserved for MCP JSON-RPC frames.** Never redirect it, never print
  to it. (source: cmd/eth-signer-mcp/main.go "STDOUT DISCIPLINE")
- **All logs go to stderr** as newline-delimited JSON (`log/slog`). `--log-level`
  controls verbosity. (source: ../../README.md §10)

On startup the binary takes a **boot-time keystore snapshot**: it reads the
keystore JSON and extracts the Ethereum address, failing fast on a missing or
malformed keystore (`keystore_error`, non-zero exit). The **password file is not
read at startup** — it is re-read on every signing call, so password rotation
takes effect immediately without a restart. (source: cmd/eth-signer-mcp/main.go
steps 4-5, ../../README.md §6)

Expected stderr at startup (timestamps omitted; the two `WARN` lines appear
because the committed fixtures are group/world-readable — see Step 2):

```json
{"level":"INFO","msg":"eth-signer-mcp starting","log_level":"info"}
{"level":"WARN","msg":"file is group/world accessible; consider chmod 600 (use --strict-perms to refuse)","path":"apps/eth-signer-mcp/internal/signing/testdata/keystore-light.json"}
{"level":"WARN","msg":"file is group/world accessible; consider chmod 600 (use --strict-perms to refuse)","path":"apps/eth-signer-mcp/internal/signing/testdata/password.txt"}
{"level":"INFO","msg":"keystore loaded","address":"0x9858EfFD232B4033E47d90003D41EC34EcaEda94"}
```

The `keystore loaded` line confirms the boot-time snapshot succeeded and shows the
EIP-55 checksummed address. Press Ctrl-C (SIGINT) to stop; the server shuts down
gracefully.

In practice you do **not** run the binary by hand — an MCP client launches it for
you, which is the next step.

---

## 5. Step 4 — Connect a minimal client

The canonical way to use `eth-signer-mcp` is to have an MCP client **launch the
binary** from a config block (command + args). No client code changes are needed —
configuration only. **Any MCP client works**; the example below is the Claude
Desktop `mcpServers` shape.

Add this to `claude_desktop_config.json` (or your client's equivalent), replacing
`/ABSOLUTE/PATH/TO/` with the absolute path to your clone:

```json
{
  "mcpServers": {
    "eth-signer-mcp": {
      "command": "/ABSOLUTE/PATH/TO/bin/eth-signer-mcp",
      "args": [
        "--keystore",
        "/ABSOLUTE/PATH/TO/apps/eth-signer-mcp/internal/signing/testdata/keystore-light.json",
        "--password-file",
        "/ABSOLUTE/PATH/TO/apps/eth-signer-mcp/internal/signing/testdata/password.txt"
      ]
    }
  }
}
```

> The paths above point at the **TEST-ONLY** fixture. Do not send real funds to
> `0x9858EfFD232B4033E47d90003D41EC34EcaEda94`. Swap in your own chmod-600
> keystore and password file for anything real.

After restarting the client, the `sign_transaction` and `get_address` tools appear
in the client's tool-approval dialog. (source: ../demo.md "Demo 1: Stdio")

For a hands-on, runnable client (a minimal Go program using the go-sdk
`CommandTransport`, plus deeper integration patterns), see
[stdio clients](02-stdio-clients.md).

---

## 6. Step 5 — Call `get_address` and sign the golden transaction

With the client connected, exercise both tools.

### 6a. `get_address` (read-only, no KDF)

`get_address` takes **no input** (an empty object `{}`). It is served from the
boot-time keystore snapshot — the password file is **not** read and **no KDF
runs**, so it is fast and safe to call even if the password file is later rotated
or made unreadable. (source: shared fact brief; ../../README.md §6)

Expected result:

```json
{"address":"0x9858EfFD232B4033E47d90003D41EC34EcaEda94"}
```

This is the same EIP-55 checksummed address printed in the `keystore loaded` log
line at startup.

### 6b. `sign_transaction` (legacy-mainnet golden vector)

Call `sign_transaction` with the **legacy-mainnet golden payload**. Note that all
numeric fields are **strings** — either decimal integer strings or `0x`-hex; both
are accepted (`type` is the EIP-155 legacy type `0x0`):

```json
{
  "type": "0x0",
  "chainId": "1",
  "nonce": "0",
  "to": "0x9858EfFD232B4033E47d90003D41EC34EcaEda94",
  "value": "1000000000000000000",
  "data": "0xdeadbeef",
  "gas": "100000",
  "gasPrice": "20000000000"
}
```

This signs a 1 ETH transfer (`value` = 1e18 wei) on mainnet (`chainId` 1) with a
20 Gwei `gasPrice` and a 4-byte calldata payload `0xdeadbeef`. `gasPrice` is
legacy/type-0 only; an EIP-1559 (type `0x2`) transaction would use `maxFeePerGas`
/ `maxPriorityFeePerGas` instead. (source: internal/signing/testdata/vectors/legacy-mainnet.json)

Expected result (all fields always present; numeric outputs are `0x`-hex):

```json
{
  "rawTransaction": "0xf871808504a817c800830186a0949858effd232b4033e47d90003d41ec34ecaeda94880de0b6b3a764000084deadbeef26a082dc9bb5c5916b7728febf0a7269cef831012cf86eb0f2a4c69aa77ba92755dea073b452e7edc29e44a5f2b6887e90d59aec9ca1b81c858f592362e45a4e9ffcac",
  "signature": {
    "r": "0x82dc9bb5c5916b7728febf0a7269cef831012cf86eb0f2a4c69aa77ba92755de",
    "s": "0x73b452e7edc29e44a5f2b6887e90d59aec9ca1b81c858f592362e45a4e9ffcac",
    "v": "0x26"
  },
  "hash": "0xfa41dd66cc50da67d7e59d1b7277e794cbe69d5b10deb88a9ca2930fae65a239",
  "from": "0x9858EfFD232B4033E47d90003D41EC34EcaEda94"
}
```

What each field means:

- **`rawTransaction`** — the broadcast-ready signed RLP, ready for
  `eth_sendRawTransaction`. **The caller submits it; the server does not.**
- **`signature.v`** — `0x26` (decimal 38) for this transaction. For legacy
  (EIP-155) transactions `v = chainID*2 + {35,36}`; for EIP-1559 it is the yParity
  bit (`0x0` or `0x1`).
- **`hash`** — the keccak-256 transaction hash.
- **`from`** — the EIP-55 checksummed sender **recovered from the signature**,
  which equals the keystore address from `get_address`. This is your proof the
  signature is valid and was produced by the expected key.

On a successful sign, the server emits one audit line to **stderr** (the
transaction body — `to`, `value`, calldata — is never logged):

```json
{"level":"INFO","msg":"signing: transaction signed successfully","request_id":"<uuid>","tx_hash":"0xfa41dd66cc50da67d7e59d1b7277e794cbe69d5b10deb88a9ca2930fae65a239","chain_id":1,"nonce":0}
```

### 6c. Confirm byte-identical to the golden vector

The `rawTransaction` above is **byte-identical** to the committed golden vector at
`internal/signing/testdata/vectors/legacy-mainnet.json` (`expected.raw_tx`), and
the `hash` matches `expected.tx_hash`. If your client produces these exact
strings, your build signs correctly. (source:
internal/signing/testdata/vectors/legacy-mainnet.json, ../demo.md "Transport parity")

A quick way to confirm from a shell:

```sh
EXPECTED=0xf871808504a817c800830186a0949858effd232b4033e47d90003d41ec34ecaeda94880de0b6b3a764000084deadbeef26a082dc9bb5c5916b7728febf0a7269cef831012cf86eb0f2a4c69aa77ba92755dea073b452e7edc29e44a5f2b6887e90d59aec9ca1b81c858f592362e45a4e9ffcac
[ "$YOUR_RAW_TX" = "$EXPECTED" ] && echo "MATCH" || echo "MISMATCH"
```

> **This transaction is NOT broadcast.** The binary has no RPC capability
> (ADR-007): there is no Ethereum client import anywhere in `internal/signing`,
> enforced by the offline-import test and depguard rules. Producing the signed RLP
> is the end of the server's job; submitting it (or not) is entirely up to you.

If you want the same flow without wiring up a GUI client, `docs/demo.md` ships a
runnable Go fallback client (`scripts/demo/cmd/stdio-client`) that performs exactly
these calls and asserts byte-equality for you.

---

## 7. What next

- **[stdio clients](02-stdio-clients.md)** — a runnable minimal client, the
  `CommandTransport` launch pattern, and deeper MCP integration.
- **[HTTP transport](03-http-transport.md)** — Streamable HTTP, bearer auth,
  loopback-only bind, and the 401/403 hardening surfaces.
- **[Tool reference](04-tool-reference.md)** — every `sign_transaction` field
  (legacy vs EIP-1559), the output schema, and the full error-code contract.
- **[Production hardening](05-production-hardening.md)** — real keystores, file
  permissions and `--strict-perms`, the chain-id guard, and the threat model.

Reference docs that complement these guides:
[app README](../../README.md) · [live demo + golden-vector parity](../demo.md) ·
[PRD](../../../../plan/prd.md) · [architecture](../../../../plan/architecture.md).
