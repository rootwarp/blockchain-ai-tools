# eth-signer-mcp

A small, auditable, **strictly offline** Model Context Protocol (MCP) server
that signs Ethereum transactions using a locally-stored Web3 Secret Storage
keystore.  An AI agent — or any MCP client — sends a fully-specified
transaction and receives a broadcast-ready signed transaction back.

---

## 1. What it is / what it is not

**In scope (v1):**

- **Legacy transactions (type 0, EIP-155)** and **EIP-1559 transactions (type
  2)** over MCP; stdio (default) or Streamable HTTP (`--http`).
- Single-account keystore; one instance signs for one address.

**Out of scope (v1 — P2 backlog):**

- Transaction types 1 (EIP-2930), 3 (EIP-4844), 4 (EIP-7702): not supported.
- EIP-191 `personal_sign` and EIP-712 typed-data signing: not supported.
- Broadcasting / `eth_sendRawTransaction`: the caller submits the signed RLP.
- Wallet management (key generation, HD derivation, multi-account): not supported.

**No outbound network calls — by construction.** `internal/signing` imports
no HTTP or RPC client — directly or transitively.  Enforced by the ADR-007
offline-import test (`internal/signing/offline_test.go`) and ADR-008 `depguard`
rules in `.golangci.yml`.  Both guards are mutation-verified in issue 4.4.

---

## 2. Install

```sh
# From the monorepo root:
make build          # -trimpath -buildvcs=true → bin/eth-signer-mcp
./bin/eth-signer-mcp --version
./bin/eth-signer-mcp --help
```

`--version` output format:
`eth-signer-mcp version <Version> (commit <Commit>, built <Date>, <GoVersion>)`

All four fields come from `internal/obs.Build()` via `runtime/debug.ReadBuildInfo`.
In `go build` from source without a VCS tag, `<Version>` shows `(devel)` and
`<Commit>` / `<Date>` show `<unknown>`; a tagged release build populates all four.

---

## 3. Quick-start: stdio (Claude Desktop-style clients)

Add to `claude_desktop_config.json` (or equivalent client config):

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

Replace `/ABSOLUTE/PATH/TO/` with the absolute path to your monorepo clone.
After restarting the client the `sign_transaction` and `get_address` tools
appear in the tool-approval dialog.

> **stdout** is reserved for MCP JSON-RPC frames — never redirect it.
> **All logs go to stderr** as structured JSON.

> ⚠️  The paths above reference the committed **test fixture** — low-value test
> keys for demo / CI use only.  **Do not send real funds to the test keystore.**
> Point `--keystore` / `--password-file` at your own chmod-600 files in
> production.

Full walkthrough (live captures, golden-vector parity proof):
[`docs/demo.md`](docs/demo.md)

---

## 4. Quick-start: Streamable HTTP

```sh
# Generate a throwaway bearer token (outside the repo tree; never commit):
TOKEN_FILE=$(mktemp /tmp/eth-signer-mcp-token.XXXXXX)
chmod 600 "$TOKEN_FILE"
openssl rand -hex 32 > "$TOKEN_FILE"

# Start the server — bound address printed to stderr:
./bin/eth-signer-mcp \
  --keystore    apps/eth-signer-mcp/internal/signing/testdata/keystore-light.json \
  --password-file apps/eth-signer-mcp/internal/signing/testdata/password.txt \
  --http \
  --http-addr   127.0.0.1:0 \
  --http-auth-token-file "$TOKEN_FILE"
# stderr: eth-signer-mcp listening on 127.0.0.1:<PORT>
```

Every request must carry `Authorization: Bearer <token>`.
Missing / wrong bearer → `401 Unauthorized`; non-loopback `Host` header → `403 Forbidden`.

**Off-localhost exposure is unsupported.** `--http-addr` accepts only loopback
addresses (`127.0.0.1` or `[::1]`); non-loopback values are rejected at startup.

Demo script, 401/403 curl one-liners, live captures: [`docs/demo.md`](docs/demo.md).

---

## 5. Flag reference

| Flag | Type | Default | Description |
|------|------|---------|-------------|
| `--keystore` | string | — (required) | Web3 Secret Storage JSON keystore path |
| `--password-file` | string | — (required) | Password file path; never inline |
| `--http` | bool | `false` | Streamable HTTP transport; requires `--http-auth-token-file` |
| `--http-addr` | string | `127.0.0.1:0` | Loopback bind address; ephemeral port when `:0`; non-loopback rejected (ADR-006) |
| `--http-auth-token-file` | string | — | Bearer auth token file (required with `--http`); chmod 600 recommended |
| `--chain-id` | uint | unset (no guard) | Optional chain-id guard; signs refused on mismatch; `0` rejected (replay-unprotected). urfave/cli renders the underlying `Uint64Flag` as `uint` in `--help`; omitting the flag disables the guard entirely |
| `--strict-perms` | bool | `false` | Refuse startup (exit 2) if any secret file is group/world-readable; default warns only |
| `--log-level` | string | `info` | `debug` \| `info` \| `warn` \| `error` (case-insensitive) |
| `--help` / `-h` | — | — | Show help (urfave/cli v3 built-in) |
| `--version` / `-v` | — | — | Print version / commit / date / Go version |

All 10 flags above are present in `./bin/eth-signer-mcp --help` with matching
descriptions and defaults.  (urfave/cli v3 renders `Uint64Flag` as `uint` in
the help output; the underlying Go type is `uint64`.)

---

## 6. Keystore lifecycle

| Event | Behaviour |
|-------|-----------|
| **Binary starts** | Keystore JSON read (boot-time snapshot, fail fast on I/O or parse error). Top-level `"address"` is optional per spec; if absent `get_address` returns `IsError: true` with `code: address_unknown` until the first successful decrypt (after `DecryptKey` succeeds, before the sign callback returns) discovers it. A malformed `"address"` field (wrong length, non-hex, bad EIP-55 mixed-case) → `keystore_error` at boot. Missing/malformed keystore → `keystore_error`, non-zero exit. |
| **`sign_transaction` call** | **Password file re-read on every call.** Password rotation takes effect immediately; no restart needed. |
| **Keystore file replaced on disk** | Address snapshot unchanged — **restart required** to pick up the new key. |
| **Wrong password / unreadable file** | `password_error` returned; server stays running. Fix the file and retry. |

---

## 7. Latency expectations

Signing computation **excluding the KDF is sub-millisecond**.  The dominant
cost is the scrypt key-derivation function inside `keystore.DecryptKey`:

| Keystore type | scrypt N | Latency per call |
|--------------|----------|-----------------|
| Standard (geth default) | 262,144 | ~0.5–1 s |
| Light (`geth --lightkdf`) | 4,096 | ~50 ms |
| Weak test-only (N=2) | 2 | ~1 ms (CI only) |

**This cost is paid on every `sign_transaction` call — there is no warm path.**
The decrypted key is never cached (ADR-010).

This statement also appears in `--help` (see `NAME` / `USAGE` line output from
`./bin/eth-signer-mcp --help`).

For dev loops, use a **light-scrypt keystore** (`geth account new --lightkdf`).

---

## 8. Security posture & threat model

**Offline by construction (ADR-007 / ADR-008).** Structurally enforced; see §1.

**Decrypt-sign-zero per call (ADR-003 / ADR-009).** The decrypted ECDSA key and
password bytes are zeroed via deferred callbacks after each signing operation.
*Best-effort caveat:* Go may retain transient copies in registers or GC-managed
memory — stated honestly, not hidden.  Concurrent calls serialised by a semaphore
of 1 (ADR-006): at most one live key scalar at a time.

**No secrets in logs.** Raw key bytes, password bytes, and their hex / base64 /
decimal encodings are never emitted.  Sentinel-based leak scans cover the happy
path and all seven error codes (`invalid_input`, `unsupported_type`,
`chain_id_mismatch`, `keystore_error`, `password_error`, `internal_error`,
`address_unknown`) across both stdio and HTTP transports (CI-gated).
`internal_error` is not force-able through the unmodified binary; it is covered
at the wire-contract level in `internal/server/handlers_test.go`.

**File permission checks.** At startup the binary checks mode bits of every
secret file.  Group- or world-readable files trigger a warning (default) or
startup refusal with exit 2 (`--strict-perms`).

**HTTP hardening layers** (outermost → innermost, ADR-006):

1. `MaxBytesHandler` — 1 MiB request body cap (413 on oversize).
2. `reqlog` middleware — request-id + structured log; no URL / header / body echo.
3. Bearer auth — SHA-256 constant-time compare → 401 on mismatch.
4. SDK `StreamableHTTPHandler` — DNS-rebinding guard (non-loopback `Host` → 403).

**Excluded adversaries (v1):** root / kernel-level attackers; swap-capture
attacks; off-localhost network callers.

Full threat model: [`../../plan/prd.md` §Security & Threat Model](../../plan/prd.md).

---

## 9. Error codes

Tool errors are returned with `IsError: true`; `Content[0]` is a text item whose
value is compact JSON with exactly two fields: `{"code":"…","message":"…"}`.

| Code | Operator meaning |
|------|-----------------|
| `invalid_input` | Missing / malformed field; bad EIP-55 checksum; `chainId = 0` |
| `unsupported_type` | Transaction type is not `0x0` or `0x2` |
| `chain_id_mismatch` | Request `chainId` ≠ `--chain-id` guard value |
| `keystore_error` | Keystore fails a boot-time structural check — covers: (a) missing or unreadable file, (b) malformed JSON, (c) present-but-malformed top-level `"address"` (wrong length, non-hex, bad EIP-55 mixed-case), (d) structurally-broken `crypto.*` v3 shape (cipher / KDF / mac / ciphertext / version). All four are detected at boot; the process exits non-zero. See §6 for the optional-address / `address_unknown` semantics. |
| `address_unknown` | `get_address` called before the optional-address keystore has discovered its real account (via the first successful decrypt inside `sign_transaction`); call `sign_transaction` first or use a keystore with a declared address |
| `password_error` | Password file unreadable or wrong password (keystore MAC failure detected at first sign; structural keystore shape is validated at boot and surfaces as `keystore_error`) |
| `internal_error` | Recovered panic, sender mismatch, or runtime ciphertext/MAC corruption detected by `DecryptKey` (structural shape is validated at boot — see `keystore_error`); `Cause` logged server-side, never sent to caller |

Wire shape example:
```json
{"isError":true,"content":[{"type":"text","text":"{\"code\":\"password_error\",\"message\":\"keystore decryption failed; check the password\"}"}]}
```

---

## 10. Observability

All logs go to **stderr** as newline-delimited JSON (`log/slog`).  Never parse
stdout — it carries MCP frames.

`--log-level` controls verbosity (`debug` | `info` | `warn` | `error`).

**Per-signing audit line** (`info`, successful `sign_transaction` calls only):
```json
{"level":"INFO","msg":"signing: transaction signed successfully","request_id":"<uuid>","tx_hash":"0x…","chain_id":1,"nonce":0}
```
Fields: `request_id`, `tx_hash`, `chain_id`, `nonce`.  Transaction body (`to`,
`value`, calldata) is **never logged**.

**HTTP request log** (`info`, every request including 401 / 403):
```json
{"level":"INFO","msg":"http request","request_id":"<uuid>","remote_addr":"127.0.0.1:<port>","status":200,"latency_ms":512}
```
Fields: `request_id`, `remote_addr`, `status`, `latency_ms`.

---

## 11. Pinned versions & MCP protocol revision

| Component | Version |
|-----------|---------|
| MCP Go SDK (`github.com/modelcontextprotocol/go-sdk`) | `v1.6.1` |
| MCP protocol revision | **`2025-11-25`** — read verbatim from `latestProtocolVersion = protocolVersion20251125 = "2025-11-25"` in `mcp/shared.go` of the SDK source (`$(go env GOMODCACHE)/github.com/modelcontextprotocol/go-sdk@v1.6.1/mcp/shared.go`, line 38) |
| go-ethereum (`github.com/ethereum/go-ethereum`) | `v1.17.3` |
| urfave/cli (`github.com/urfave/cli/v3`) | `v3.9.0` |
| Go toolchain | `1.26` (see `go.work` / `apps/eth-signer-mcp/go.mod`) |
| Foundry (cast) | `v1.7.1` (`.foundry-version`) — **vector regeneration only**; CI and the binary never invoke Foundry |

`govulncheck ./...` runs in CI (workflow from issue 1.2) against all pinned
dependencies on every push.

Golden vector regeneration (requires Foundry v1.7.1):
```sh
scripts/regen-vectors.sh   # dual-oracle: cast + ethers v6
```

See [`scripts/regen-vectors.sh`](../../scripts/regen-vectors.sh).

---

## 12. Troubleshooting

**Permission warning at startup (or exit 2 with `--strict-perms`)**

```
{"level":"WARN","msg":"file is group/world accessible; consider chmod 600 (use --strict-perms to refuse)","path":"/path/to/keystore.json"}
```

Fix: `chmod 600 <keystore> <password-file>` (and `<token-file>` for `--http`).
With `--strict-perms`, the binary refuses to start; fix permissions and retry.

---

**401 from the HTTP transport**

Causes: missing `Authorization: Bearer` header; wrong token value; token file
replaced after startup (token hash read once at startup — restart to pick up a
new file).

Fix: send the correct bearer header.  Restart if the token file was replaced.

---

**`chain_id_mismatch` error**

```json
{"code":"chain_id_mismatch","message":"chain ID 1 does not match guard 11155111"}
```

The transaction's `chainId` does not match `--chain-id`.  Either remove
`--chain-id` (disables the guard) or align the transaction's `chainId`.

---

**"Signing feels slow" → §Latency expectations**

Each call decrypts the keystore from scratch.  Use a light-scrypt keystore
(`geth account new --lightkdf`, N=4096, ~50 ms) for dev loops instead of the
geth default (N=262144, ~0.5–1 s).

---

*Architecture:* [`../../plan/architecture.md`](../../plan/architecture.md) — ADR citations, dependency graph.  
*Demo:* [`docs/demo.md`](docs/demo.md) — live stdio + HTTP sessions, golden-vector proof.
