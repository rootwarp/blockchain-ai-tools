# Production & Hardening Guide

This guide takes `eth-signer-mcp` from the committed **test fixture** to a real
deployment. By the end you will have a production keystore with the right scrypt
strength, secret files locked down to `chmod 600`, the chain-id guard and
`--strict-perms` startup checks understood and enabled, a secret-rotation
runbook, and an honest picture of exactly what the v1 threat model does and does
not cover. Every command and log line below is copy-pasteable or a faithful
sample of real output; where a fact is verifiable in source, a `(source: …)`
pointer is given.

This is an operator/integrator guide. For the per-tool request/response contract
see [Tool reference](04-tool-reference.md); for the running app reference see
[`../../README.md`](../../README.md); for live captures and golden-vector parity
see [`../demo.md`](../demo.md).

> The signer is **strictly offline** and **never broadcasts**. It returns
> broadcast-ready signed RLP; the caller submits it. No flag changes this —
> there is no outbound network path by construction (ADR-007/008,
> source: `internal/signing/offline_test.go`, `.golangci.yml`).

---

## Step 1 — Create a real keystore

The committed fixture is a *light-scrypt, test-only key*. **Do not send real
funds to it.** For production, mint your own key with `geth`.

```sh
# Standard scrypt (geth default, N=262144) — recommended for production at rest:
geth account new --keystore /etc/eth-signer/keystore

# Light scrypt (N=4096) — faster per-sign, weaker at rest; dev loops / low-value:
geth account new --keystore /etc/eth-signer/keystore --lightkdf
```

`geth` writes a UTC-named Web3 Secret Storage JSON file into the directory and
prompts for a password. You point `--keystore` at that **file** (not the
directory) and supply the password via `--password-file` (Step 2).

### scrypt strength vs. latency

The decrypted key is **never cached** (ADR-010): every `sign_transaction` call
re-reads the password file and re-runs the scrypt KDF inside
`keystore.DecryptKey`. That KDF dominates end-to-end latency; the rest of
signing is sub-millisecond. **You pay the KDF cost on every single call** —
there is no warm path.

| Keystore type | scrypt `N` | Latency per sign call | Use for |
|---|---|---|---|
| Standard (`geth account new`) | 262,144 | ~0.5–1 s | Production keys at rest |
| Light (`geth account new --lightkdf`) | 4,096 | ~50 ms | Dev loops, fast iteration, low-value keys |
| Weak test-only | 2 | ~1 ms | CI fixtures only — never real funds |

(source: README §7; `plan/architecture.md` ADR-010)

The trade-off: higher `N` resists offline brute-force of the keystore file if it
leaks, at the cost of per-call latency and ~256 MiB of memory per decrypt.
Choose standard scrypt for any key holding value; reserve light scrypt for fast
development against testnets or throwaway keys.

> **Test-only fixture warning.** The committed keystore
> `internal/signing/testdata/keystore-light.json` (light scrypt, N=4096) and its
> password `internal/signing/testdata/password.txt` (`test-only-password-do-not-reuse`)
> are low-value demo keys for address `0x9858EfFD232B4033E47d90003D41EC34EcaEda94`.
> Use them only for demos and CI. **Never send real funds to that address.**

---

## Step 2 — Store the password in a file (never inline)

The password is supplied **only** via `--password-file`. There is no
`--password` flag, and inlining a secret on a command line leaks it to the
process table and shell history.

```sh
# Write the password to a file outside any repo tree, then lock it down:
umask 077
printf '%s' 'your-strong-password' > /etc/eth-signer/password.txt
chmod 600 /etc/eth-signer/password.txt
```

A trailing newline (or Windows `\r\n`) in the password file is stripped
automatically before decryption, so an editor-saved file works
(source: `internal/signing/decrypt.go` `readPasswordFile`).

Lock down **every** secret file to `chmod 600` (owner read/write only):

```sh
chmod 600 /etc/eth-signer/keystore/UTC--…   # keystore JSON
chmod 600 /etc/eth-signer/password.txt       # password file
chmod 600 /etc/eth-signer/http-token.txt     # bearer token file (only with --http)
```

### The startup permission check

At startup the binary `os.Stat`s each secret file and inspects its mode bits. A
file with **any** group- or world-accessible bit set (`mode & 0o077 != 0`) is
"too open" (source: `cmd/eth-signer-mcp/fsperm.go` `checkPerms`). The reaction
depends on `--strict-perms`:

- **Default (flag absent):** logs a **WARN** and continues.
- **`--strict-perms`:** logs an **ERROR** and refuses to start with **exit code
  2**.
- **Either mode**, if a path can't be stat'd or is not a regular file (e.g. a
  typo'd path): logs ERROR and exits 2 immediately — fail fast, so a broken
  path never boots a server that can never sign.

The WARN line emitted by default for a too-open file:

```json
{"level":"WARN","msg":"file is group/world accessible; consider chmod 600 (use --strict-perms to refuse)","path":"/etc/eth-signer/password.txt"}
```

With `--strict-perms`, the same condition produces an ERROR and a non-zero exit:

```json
{"level":"ERROR","msg":"refusing to start: file is group/world accessible; chmod 600","path":"/etc/eth-signer/password.txt"}
```

(source: `cmd/eth-signer-mcp/fsperm.go` `applyPermChecks`)

> **TOCTOU advisory.** The check uses `os.Stat` at startup; the actual file
> reads happen later, per call. The permission check is an operator-facing
> advisory, not a hard guarantee against a process that races to widen
> permissions between startup and a sign call (source:
> `cmd/eth-signer-mcp/main.go`). Filesystem permissions remain your real control.

Paths are logged; **file contents are never logged**.

---

## Step 3 — Pin the network with `--chain-id`

`--chain-id` is an optional guard. When set, any `sign_transaction` whose
`chainId` does not equal the guard value is refused with `chain_id_mismatch`
**before any key material is touched** — the vault is never even invoked
(source: `internal/signing/signer.go`, `internal/signing/validate.go`;
`plan/architecture.md` Flow C).

```sh
# Restrict this instance to Ethereum mainnet (chain-id 1):
./bin/eth-signer-mcp \
  --keystore      /etc/eth-signer/keystore/UTC--… \
  --password-file /etc/eth-signer/password.txt \
  --chain-id      1
```

How the guard behaves:

- **Guard set, request `chainId` matches:** proceeds normally.
- **Guard set, request `chainId` differs:** `chain_id_mismatch`, returned as the
  standard two-field tool error. The on-the-wire message is
  `"chainId: does not match the chain-id guard configured on the signer"`
  (source: `internal/signing/validate.go`; the README §12 example string is
  illustrative).
- **Guard omitted:** no guard — the request's own `chainId` is used as-is. (The
  request `chainId` is always required and must still be non-zero; see below.)

```json
{"isError":true,"content":[{"type":"text","text":"{\"code\":\"chain_id_mismatch\",\"message\":\"chainId: does not match the chain-id guard configured on the signer\"}"}]}
```

**Why `--chain-id 0` is rejected at startup.** chain-id 0 selects the
replay-unprotected (pre-EIP-155) signer, so the binary fails fast rather than
silently weakening replay protection (source: `cmd/eth-signer-mcp/config.go`):

```
--chain-id 0 is rejected: chain-id 0 is replay-unprotected; use a non-zero chain-id
```

The request `chainId` field is likewise forbidden from being zero, independent
of the guard: `"chainId: must not be zero (would select the replay-unprotected
Homestead signer)"` (source: `internal/signing/validate.go`).

**When to use it.** The guard is a backstop against sign-on-the-wrong-network
mistakes (e.g. an agent confusing mainnet and a testnet). The signer cannot
judge transaction *intent* — it signs what it is told. Pinning the chain-id is a
cheap, high-value guardrail for any instance that should only ever sign for one
network. Run a separate instance per chain if you sign for several.

---

## Step 4 — Choose a transport for production posture

Two transports; pick by who launches the process and who needs to reach it.

### stdio (default) — launched by a local MCP client

The binary speaks MCP JSON-RPC over stdin/stdout, launched as a subprocess by a
local client (e.g. a Claude Desktop-style `mcpServers` config block of
`command` + `args`). This is the simplest secure posture: no network listener
exists at all, and access is exactly "whoever can launch this subprocess."

> **stdout is reserved for MCP frames.** Never redirect or print to it. All logs
> go to **stderr** as newline-delimited JSON (source: README §3, §10).

After (re)starting the client, `sign_transaction` and `get_address` appear in
its tool-approval dialog. See the stdio quick-start in
[`../../README.md`](../../README.md) §3.

### Loopback HTTP (`--http`) — local daemon with bearer auth

```sh
# Generate a throwaway bearer token outside any repo tree, then lock it down:
TOKEN_FILE=$(mktemp /tmp/eth-signer-mcp-token.XXXXXX)
chmod 600 "$TOKEN_FILE"
openssl rand -hex 32 > "$TOKEN_FILE"

./bin/eth-signer-mcp \
  --keystore             /etc/eth-signer/keystore/UTC--… \
  --password-file        /etc/eth-signer/password.txt \
  --chain-id             1 \
  --http \
  --http-addr            127.0.0.1:0 \
  --http-auth-token-file "$TOKEN_FILE" \
  --strict-perms
# stderr: eth-signer-mcp listening on 127.0.0.1:<PORT>
# stderr: {"level":"INFO","msg":"http server listening","addr":"127.0.0.1:<PORT>"}
```

With `--http-addr 127.0.0.1:0` the OS assigns an ephemeral port; read it from
the `listening on` line on stderr (source: `internal/server/http.go`). `--http`
**requires** `--http-auth-token-file`. Every request must carry
`Authorization: Bearer <token>`.

The HTTP pipeline enforces four layers, outermost → innermost (ADR-006):

1. **`MaxBytesHandler`** — 1 MiB request-body cap; oversize → `400` (the SDK
   returns `400 Bad Request` when the capped body fails to decode; it does not
   emit `413`; source: `internal/server/http.go`, `bounds_test.go`
   `pinnedStatus = 400`).
2. **`reqlog` middleware** — attaches a request-id and emits one structured log
   line. It does **not** echo URL, headers, or body.
3. **Bearer auth** — SHA-256 + constant-time compare; missing/wrong token →
   `401` with header `Www-Authenticate: Bearer`.
4. **SDK `StreamableHTTPHandler`** — DNS-rebinding guard: a non-loopback `Host`
   header → `403 Forbidden`; then tool dispatch.

### Off-localhost is unsupported — read this before fronting it

`--http-addr` accepts **only** loopback addresses (`127.0.0.1` or `[::1]`);
non-loopback values are **rejected at startup** (ADR-006, source: README §4).
Off-localhost network callers are an **explicitly out-of-scope adversary** in
v1.

If you must reach the signer from another host, that is **your** responsibility
and **your** risk: run your own localhost-only tunnel or reverse proxy (e.g. an
SSH tunnel terminating at `127.0.0.1`, or a proxy you operate and authenticate)
in front of the loopback listener. The signer offers **no** support, hardening,
or guarantees for that path — it only ever sees loopback traffic, and its bearer
auth assumes the network boundary is the local host. Do not treat the tunnel as
endorsed; treat it as a thing you own end-to-end.

---

## Step 5 — Secret rotation runbook

What can change live versus what needs a restart, per the keystore lifecycle
(source: README §6; SHARED FACT BRIEF "Keystore lifecycle"):

| Secret | Rotation method | Restart? |
|---|---|---|
| **Password** | Re-encrypt the keystore to the new password (or replace the password file to match), then overwrite `--password-file`. The password file is **re-read on every sign call**. | **No restart.** Takes effect on the next `sign_transaction`. |
| **Keystore (key)** | Replace the keystore JSON on disk. The address is a **boot-time snapshot**; the running process keeps the old key. | **Restart required** to pick up the new key. |
| **HTTP bearer token** | Replace the token file and update clients. The token hash is read **once at startup**. | **Restart required.** |

Operational notes:

- **Password rotation is live.** Because the password file is re-read every call
  (source: `internal/signing/decrypt.go` `WithSigningKey` step 3), an atomic
  overwrite of `--password-file` is picked up on the next sign with no downtime.
- A **wrong password** (or unreadable file) returns `password_error` and the
  **server keeps running** — fix the file and retry; no restart needed
  (source: README §6).
- `get_address` is served from the boot-time snapshot and **never reads the
  password file or runs the KDF**, so it keeps working even mid-rotation when
  the password file is briefly unreadable.
- To rotate a token with minimal disruption on HTTP, start a second instance on
  a fresh ephemeral port with the new token, cut clients over, then stop the old
  one.

---

## Step 6 — What is protected vs. what is not

Be precise with yourself here; an overstated threat model is worse than an
honest one.

### Protected (enforced)

- **Decrypt-sign-zero per call (ADR-003/009).** The password bytes and the
  decrypted ECDSA key scalar are zeroed via deferred callbacks after each
  signing operation — including on panic paths (source:
  `internal/signing/decrypt.go`, `internal/signing/zero.go`). The decrypted key
  is never cached (ADR-010): its in-memory lifetime is one stack frame.
- **Best-effort, stated honestly.** Zeroing is `clear()` + `runtime.KeepAlive`.
  Go's runtime **may retain transient copies** in registers, stack copies, or
  GC-managed memory. In particular, `keystore.DecryptKey` takes a **string**
  password; the `string(passwordBytes)` conversion allocates an immutable copy
  that our deferred zeroing does **not** own and that persists until GC reclaims
  it (source: `internal/signing/decrypt.go`, ADR-009). The *observable*,
  test-enforced guarantee is "no secrets in logs or outputs, raw or encoded" —
  **not** guaranteed in-memory erasure.
- **Semaphore of 1 (ADR-006).** Signing serializes through a semaphore of one,
  so at most one live key scalar (and one ~256 MiB scrypt decrypt) exists at any
  moment (source: `internal/signing/decrypt.go` step 1).
- **No secrets in logs.** Raw key/password bytes and their hex/base64/decimal
  encodings are never emitted (`Secret[T]` redaction; sentinel leak-scan tests;
  source: `internal/signing/secret.go`, README §8).
- **Offline by construction.** No outbound network calls; structurally enforced
  (ADR-007/008).
- **Filesystem permission advisory** (Step 2) and the **chain-id guard**
  (Step 3).

### Excluded adversaries (v1) — NOT protected against

- **Root / kernel-level attackers** on the same host. No mitigation is possible
  at this layer; out of scope.
- **Swap-capture attacks** (secret material paged to disk). Out of scope —
  best-effort zeroing does not defend against this.
- **Off-localhost network callers.** The HTTP transport is loopback-only;
  anything reaching it from off-host is the operator's responsibility
  (Step 4).
- **Side-channel attacks** against scrypt or ECDSA (trusting go-ethereum's
  implementations).
- **Semantic intent.** A malicious or prompt-injected agent can craft any
  syntactically valid transaction; the server signs what it is told and cannot
  tell a "drain wallet" tx from a normal one. The **MCP client's own approval
  flow is the primary gate** (source: `plan/prd.md` §Security & Threat Model).

### Out of scope as features (v1)

No broadcasting (`eth_sendRawTransaction`); no EIP-191 `personal_sign`; no
EIP-712 typed-data signing; no transaction types 1 (EIP-2930), 3 (EIP-4844), or
4 (EIP-7702); no wallet management (key gen, HD derivation, multi-account).
**One account per instance** (source: README §1).

---

## Step 7 — Logging & audit in production

All logs go to **stderr** as newline-delimited JSON (`log/slog`). Never parse
stdout — it carries MCP frames. `--log-level` sets verbosity
(`debug` | `info` | `warn` | `error`, case-insensitive; an invalid value is a
startup error). For production, `info` (the default) is the right baseline; it
emits the audit and request lines below without debug noise.

**Per-signing audit line** (`info`; successful `sign_transaction` only)
— fields exactly `request_id`, `tx_hash`, `chain_id`, `nonce`
(source: `internal/signing/signer.go`):

```json
{"level":"INFO","msg":"signing: transaction signed successfully","request_id":"<uuid>","tx_hash":"0x…","chain_id":1,"nonce":0}
```

**HTTP request line** (`info`; **every** request including 401/403)
— fields exactly `request_id`, `remote_addr`, `status`, `latency_ms`
(source: `internal/server/reqlog.go`):

```json
{"level":"INFO","msg":"http request","request_id":"<uuid>","remote_addr":"127.0.0.1:<port>","status":200,"latency_ms":512}
```

**The transaction body is never logged.** `to`, `value`, and calldata do not
appear in any log line at any level — only the hash, chain-id, and nonce do. The
audit line lets you reconcile a signed tx with what the agent requested
(by `tx_hash`) without exposing the recipient or amount in your log pipeline.

For deeper observability details and the troubleshooting recipes (permission
warnings, 401s, `chain_id_mismatch`, "signing feels slow"), see the
[Troubleshooting guide](07-troubleshooting.md) and README §10/§12.

---

## Pre-flight checklist

Before exposing the signer to an agent:

- [ ] Keystore minted with **standard scrypt** (`geth account new`, N=262144)
      for any key holding real value; light scrypt reserved for dev/low-value.
- [ ] Password stored in a **file** (`--password-file`), never inline; trailing
      newline is fine.
- [ ] Keystore, password file, and (if `--http`) bearer token file are all
      **`chmod 600`**, owned by the service user.
- [ ] **`--strict-perms`** set so a too-open secret refuses startup (exit 2)
      instead of merely warning.
- [ ] **`--chain-id`** pinned to the one network this instance should sign for
      (non-zero); a separate instance per chain.
- [ ] Transport chosen deliberately: **stdio** (no listener) or **loopback
      HTTP** with a strong bearer token; off-localhost is unsupported and, if
      tunneled, owned end-to-end by you.
- [ ] For HTTP: token generated with `openssl rand -hex 32` outside any repo
      tree; bound to a **loopback** address (`127.0.0.1` / `[::1]`).
- [ ] You are **not** using the committed test fixture keystore/password/address
      for real funds.
- [ ] Latency budget understood: ~0.5–1 s per sign on standard scrypt, paid on
      **every** call (no key cache).
- [ ] Log destination (stderr) captured by your supervisor/journal; stdout left
      untouched for stdio MCP frames.
- [ ] Threat-model limits accepted: best-effort zeroing, no defense against
      root/kernel/swap, no broadcast, the MCP client's approval flow is the
      primary intent gate.

---

*See also:* [Tool reference](04-tool-reference.md) ·
[Troubleshooting](07-troubleshooting.md) · [`../../README.md`](../../README.md) ·
[`../demo.md`](../demo.md) · [`../../../../plan/prd.md`](../../../../plan/prd.md) ·
[`../../../../plan/architecture.md`](../../../../plan/architecture.md)
