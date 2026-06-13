# Troubleshooting & Operations

This guide is a symptom-to-fix reference for running **eth-signer-mcp** in
anger. It covers how to read what the server is telling you (all logs are JSON
on stderr), the full error-code table with causes and remedies, every startup
failure mode, the permission warning and the `--strict-perms` refusal, HTTP
401/403/400, the `chain_id_mismatch` guard, why signing can feel slow, the
difference between `password_error` and `keystore_error`, and how to read
`--version` output. When you finish, you should be able to take any line the
server prints and turn it into a fix.

This is a companion to the [app reference README](../../README.md) (sections 9,
10, 12 are the canonical short forms of what follows) and the
[demo walkthrough](../demo.md) (live captures of these exact paths). Where a
fact is verifiable in source, a `(source: <file>)` pointer is given so you can
confirm it yourself.

> The commands below reference the committed **test fixture** keystore at
> `apps/eth-signer-mcp/internal/signing/testdata/keystore-light.json` (light
> scrypt, address `0x9858EfFD232B4033E47d90003D41EC34EcaEda94`, password file
> `testdata/password.txt`). These are **low-value test keys for demo/CI only —
> never send real funds to this address.** In production, point `--keystore` /
> `--password-file` at your own chmod-600 files.

---

## 1. How to read what the server is telling you

The binary uses two output streams with strictly separate jobs:

- **stdout** carries MCP JSON-RPC frames *only* (on the default stdio
  transport). **Never** redirect it, print to it, or try to parse logs from it.
  If you see what looks like a log line on stdout, something is wrong — there
  are no log writers pointed at stdout.
- **stderr** carries **all logs**, as newline-delimited JSON produced by the
  stdlib `log/slog` JSON handler (source: `internal/obs/log.go` — `NewLogger`
  builds `slog.NewJSONHandler(os.Stderr, …)`).

So the first move in any investigation is: **capture stderr.** For the HTTP
transport you can simply read the terminal; for stdio under an MCP client, find
the client's server-log file (Claude Desktop writes per-server logs to disk).

### 1.1 `--log-level` controls verbosity

`--log-level` takes one of `debug | info | warn | error`, parsed
case-insensitively (source: `internal/obs/log.go` — `parseLevel`). The default
is `info`. Note one subtlety: the *logger constructor* silently falls back to
`info` for an unrecognized level, but the *CLI* rejects a bad value at startup
before the logger is built (see §3.5) — so in practice you never get a silent
fallback through the real binary.

Set `--log-level debug` when you need to see everything; `info` is enough for
the two audit lines below.

### 1.2 The signing audit line

Emitted at `info` on a **successful** `sign_transaction` only (a failed sign
emits an error-level line instead — see §1.4):

```json
{"level":"INFO","msg":"signing: transaction signed successfully","request_id":"3f2a…","tx_hash":"0xfa41dd66…","chain_id":1,"nonce":0}
```

Field meanings:

| Field | Meaning |
|-------|---------|
| `request_id` | UUIDv4 correlating this sign to its HTTP request log line (HTTP path), or a freshly generated id (stdio path). Source: `internal/server/handlers.go` — `generateRequestID`. |
| `tx_hash` | The keccak-256 hash of the signed transaction — the same `hash` returned in the tool result. For the legacy golden vector this is `0xfa41dd66cc50da67d7e59d1b7277e794cbe69d5b10deb88a9ca2930fae65a239`. |
| `chain_id` | The transaction's chain id (`1` = mainnet in the golden vectors). |
| `nonce` | The transaction nonce. |

What is **deliberately absent** is the whole transaction body: `to`, `value`,
and calldata are **never logged** (source: README §10). The audit line is for
correlation and accountability, not for reconstructing payloads.

### 1.3 The HTTP request line

Emitted at `info` for **every** HTTP request — including ones that get rejected
with 401 or 403 — because the request-log middleware sits *outside* auth in the
pipeline (source: `internal/server/http.go` — `newRequestLogMiddleware` wraps
the auth layer):

```json
{"level":"INFO","msg":"http request","request_id":"3f2a…","remote_addr":"127.0.0.1:54321","status":200,"latency_ms":512}
```

| Field | Meaning |
|-------|---------|
| `request_id` | The id minted for this request, propagated into the signing audit line so you can join the two. |
| `remote_addr` | The client's `ip:port` (always loopback in a correct deployment). |
| `status` | The HTTP status actually returned (200, 401, 403, 400, …). |
| `latency_ms` | Wall-clock time for the request. A ~500 ms value on a `status:200` is normal — that is the scrypt KDF (see §7), not a network stall. |

The middleware does **not** echo the URL, headers, or body — so the bearer token
never lands in a log even on a 401 (source: `internal/server/auth.go` header
comment; README §8 layer 2).

### 1.4 Reading an error

A failed `sign_transaction` produces two things you can observe:

1. **On the wire**, a tool result with `IsError: true` and a single text item
   that is compact JSON `{"code":"…","message":"…"}` (the contract in §2).
2. **In the logs (server side only)**, if the error carried an internal
   `Cause`, a separate error line:
   ```json
   {"level":"ERROR","msg":"sign_transaction: tool error with cause","request_id":"3f2a…","code":"password_error","cause":"could not decrypt key with given password"}
   ```
   The `cause` is logged but is **never serialized to the caller** (source:
   `internal/signing/errors.go` — `Cause` has `json:"-"`; `handlers.go` logs it
   as a separate slog field). When a wire error feels opaque, the matching
   `cause` line on stderr is where the real detail lives.

---

## 2. Error-code reference table

Tool errors all share one wire shape (ADR-004; source:
`internal/server/errors.go` — `toolResult` / `toolErrorPayload`):

```json
{"isError":true,"content":[{"type":"text","text":"{\"code\":\"password_error\",\"message\":\"keystore decryption failed; check the password\"}"}]}
```

The inner text is **exactly two fields, `code` then `message`**, with no
indentation and no third field. There are exactly six codes (source:
`internal/signing/errors.go`).

| Code | Likely cause | Concrete fix |
|------|--------------|--------------|
| `invalid_input` | A missing or malformed field; a hex parse failure; a bad EIP-55 checksum on a mixed-case `to`; `chainId` = `0`; a non-empty `accessList`; or `data` larger than 256 KiB decoded. | Fix the offending field. The `message` names it, e.g. `"nonce: field is required"`, `"maxFeePerGas: not applicable for legacy (type 0) transactions"` (source: `internal/signing/validate.go`). |
| `unsupported_type` | The `type` field is not `0x0`/`legacy` or `0x2`/`eip1559` — e.g. you sent an EIP-2930 (type 1), EIP-4844 (type 3), or EIP-7702 (type 4) transaction. | Use a type-0 or type-2 transaction. Types 1/3/4 are out of scope in v1 (source: `internal/signing/validate.go` — `parseTxType`; README §1). |
| `chain_id_mismatch` | The request's `chainId` differs from the `--chain-id` guard you started the server with. | Align the transaction's `chainId`, or drop `--chain-id` to disable the guard. See §6. |
| `keystore_error` | The keystore file is missing/unreadable, the JSON is malformed, or it has no usable `"address"` field. This is a **boot-time** failure. | Fix the keystore path/file and **restart** the server. See §3.6 and §8. |
| `password_error` | The password file is unreadable, **or** the password is wrong (keystore MAC failure, `keystore.ErrDecrypt`). The server **stays running**. | Fix the password file and retry — no restart needed. See §8. |
| `internal_error` | A recovered panic, a post-sign sender mismatch, or a non-`ErrDecrypt` decrypt failure (unknown cipher, corrupted ciphertext, unsupported KDF). | The `Cause` is logged server-side, never sent to the caller. Read the matching `cause` line on stderr (§1.4); a non-`ErrDecrypt` decrypt failure usually means a damaged keystore. |

Exact `message` strings worth recognizing (all from `internal/signing/`):

- `password_error`, unreadable file → `"password file could not be read"`
  (`decrypt.go`).
- `password_error`, wrong password → `"keystore decryption failed; check the
  password"` (`decrypt.go`).
- `internal_error`, bad keystore internals →
  `"keystore decryption failed due to an internal error"` (`decrypt.go`).
- `keystore_error` variants → `"keystore file could not be read"`,
  `"keystore JSON is malformed"`,
  `` `keystore JSON has no usable "address" field; re-export the keystore` ``
  (`file_vault.go`).

> Protocol- vs tool-level errors: only the six codes above ride the
> `IsError: true` tool-result path. A genuine *system* failure (e.g. a cancelled
> context) is instead returned as a JSON-RPC protocol error with the generic
> wire message `"internal server error"` and is logged server-side as
> `"sign_transaction: system failure"` (source: `internal/server/handlers.go`).

---

## 3. Startup failures

These are caught by CLI validation **before** the server binds or signs
anything. They print an error and exit non-zero (source:
`cmd/eth-signer-mcp/config.go` — `validate`, unless noted). Fix the flag and
re-run.

### 3.1 Missing required flags

```
--keystore is required
--password-file is required
--http-auth-token-file is required when --http is set
```

`--keystore` and `--password-file` are always required. `--http-auth-token-file`
becomes required the moment you pass `--http`.

### 3.2 `--chain-id 0`

```
--chain-id 0 is rejected: chain-id 0 is replay-unprotected; use a non-zero chain-id
```

Chain id `0` offers no EIP-155 replay protection, so it is refused. Either pass a
real chain id or **omit `--chain-id` entirely** to disable the guard.

### 3.3 Non-loopback `--http-addr`

```
--http-addr must be a loopback address (127.0.0.1 or [::1]); non-loopback binds are rejected (ADR-006)
```

(Or, if the value does not even parse as `host:port`:
`--http-addr: invalid host:port — must be a loopback address (e.g. 127.0.0.1:0 or [::1]:0)`.)

Off-localhost exposure is unsupported by design (ADR-006). There is also a
**defense-in-depth** check at bind time: even if validation were bypassed,
`RunHTTP` re-rejects a non-loopback bound address with
`"RunHTTP: bound address is not loopback (ADR-006); check --http-addr"` (source:
`internal/server/http.go`). Use `127.0.0.1:0` (ephemeral port) or `[::1]:0`.

### 3.4 `--http` without a token file

Covered by the message in §3.1. The token file is read, stripped of one trailing
`\n` (then one `\r`), and rejected if empty — an empty token file fails startup
with `token file "<path>": empty after stripping trailing newline` (source:
`internal/server/auth.go` — `NewBearerVerifierFromFile`). The listener never
binds when token setup fails (fail-fast; source: `internal/server/http.go`
step 1).

### 3.5 Invalid `--log-level`

```
--log-level must be one of debug|info|warn|error
```

The CLI rejects anything outside that set (case-insensitive) before the logger
is built.

### 3.6 Bad or missing keystore (`keystore_error` + non-zero exit)

The keystore is read once at startup (boot-time snapshot, fail fast). If it is
missing, malformed, or has no usable `"address"` field, the server emits a
`keystore_error` and **exits non-zero** — it never reaches a listening state.
This is the one error code you cannot fix by retrying a tool call: fix the file
and restart (source: README §6; `internal/signing/file_vault.go`). See §8 for
how this differs from `password_error`.

---

## 4. Permission warning vs. `--strict-perms` exit 2

At startup the binary stats every secret file (`--keystore`, `--password-file`,
and the token file under `--http`) and checks its POSIX mode bits (source:
`cmd/eth-signer-mcp/fsperm.go` — `applyPermChecks`).

**Default behavior (flag absent): WARN and continue.** A group- or
world-readable file logs:

```json
{"level":"WARN","msg":"file is group/world accessible; consider chmod 600 (use --strict-perms to refuse)","path":"/path/to/keystore.json"}
```

The server still starts. The fix is to tighten the file:

```sh
chmod 600 /path/to/keystore.json /path/to/password.txt
# and, when using --http:
chmod 600 /path/to/token-file
```

**With `--strict-perms`: REFUSE and exit 2.** The same condition instead logs an
ERROR and aborts:

```json
{"level":"ERROR","msg":"refusing to start: file is group/world accessible; chmod 600","path":"/path/to/keystore.json"}
```

and the process exits with code **2** and stderr line
`startup aborted: refusing to start: file is group/world accessible; chmod 600`.
So `--strict-perms` does not change *what* is detected — it turns the warning
into a hard refusal. Use it in production. (A stat failure or non-regular file is
always fatal with exit 2 regardless of the flag.)

> On Windows, mode-bit checks do not apply (access is governed by ACLs);
> `--strict-perms` has no effect there (source:
> `cmd/eth-signer-mcp/fsperm_windows.go`).

---

## 5. HTTP 401, 403, and 400

These only apply to the Streamable HTTP transport (`--http`). All three are
request-logged (status field in the `http request` line, §1.3). The pipeline
order that produces them, outermost → innermost (source:
`internal/server/http.go` step 5):

1. `MaxBytesHandler` — 1 MiB body cap.
2. `reqlog` — request-id + log line.
3. Bearer auth — 401.
4. SDK `StreamableHTTPHandler` — DNS-rebinding guard (403), then dispatch.

### 5.1 401 Unauthorized — bearer auth

A **401 with an empty body and a `WWW-Authenticate: Bearer` response header**
fires when (source: `internal/server/auth.go` — `Middleware`):

- the `Authorization` header is missing or empty;
- it does not start with the exact, case-sensitive string `Bearer ` (note the
  trailing space — `bearer`, `BEARER`, or `Bearer` without a space are all
  rejected);
- the `Bearer ` prefix is present but the token is empty;
- the token is present but `sha256(supplied)` does not match the stored
  `sha256(expected)` (constant-time compared).

Reproduce and confirm:

```sh
# No header → 401
curl -i -s -X POST http://127.0.0.1:<PORT>/mcp -d '{}' | head -1
# HTTP/1.1 401 Unauthorized   (and a `WWW-Authenticate: Bearer` header)
```

Fixes:

- Send the correct header: `Authorization: Bearer $(cat "$TOKEN_FILE")`.
- **Token file replaced after startup?** The token hash is read **once at
  startup** and never re-read (source: `internal/server/http.go` step 1). If you
  rotate the token file, every request keeps getting 401 until you **restart the
  server**. This is the opposite of the password file, which is re-read every
  sign call (§8).

### 5.2 403 Forbidden — DNS-rebinding guard

The SDK's `StreamableHTTPHandler` runs with DNS-rebinding protection **on**
(`DisableLocalhostProtection` left at its zero value `false`, by design; source:
`internal/server/http.go` step 4). A request whose `Host` header is **not a
loopback host** is answered with **403 Forbidden** before any tool dispatch.

This is why a correct client always targets `127.0.0.1` (or `[::1]`) by literal
address. If you front the server with a reverse proxy that rewrites `Host` to a
public name, you will get 403s — that is the guard working as intended, and
exposing the server off-loopback is unsupported in v1.

### 5.3 400 — body over 1 MiB

`MaxBytesHandler` caps the request body at 1 MiB (`1 << 20`) as the outermost
layer (source: `internal/server/http.go` step 5). It wraps the body in
`http.MaxBytesReader`, so an oversized body never reaches auth, body-content
logging, or tool dispatch. When the cap is exceeded the body read fails with
`*http.MaxBytesError`, which makes the SDK's `json.Decoder` fail — and the SDK's
`StreamableHTTPHandler` answers a JSON-decode failure with **HTTP 400**, not 413.
It does **not** translate `*http.MaxBytesError` to a 413 (pinned in
`internal/server/bounds_test.go` — `const pinnedStatus = 400`; see also
`internal/server/http.go` line 174). So watch for a **400** in the `http request`
log line, never a 413. Note that the practical cause is almost always an
oversized `data` field — but `data` has its own, smaller, 256 KiB *decoded* cap
enforced in validation (which surfaces as `invalid_input`, not 400). You only hit
the 1 MiB transport cap with a genuinely huge or malformed request envelope;
shrink the payload.

---

## 6. `chain_id_mismatch`

If you started the server with `--chain-id <N>`, every `sign_transaction` whose
`chainId` differs from `<N>` is refused **before any decryption happens**
(source: `internal/signing/validate.go` — rule 2 runs before type and
field checks; the vault is never touched, per
`signer_test.go:TestSigner_VaultNeverTouchedOnChainIDMismatch`).

The wire message is:

```json
{"code":"chain_id_mismatch","message":"chainId: does not match the chain-id guard configured on the signer"}
```

(The README §9 example phrases this as `chain ID 1 does not match guard
11155111` — that is illustrative; the verbatim source string is the one above.)

Two fixes:

- **Align the transaction.** Set the request's `chainId` to match the guard. For
  example, the golden-vector legacy request uses `chainId:"1"`, so it signs
  cleanly only under `--chain-id 1` (or with no guard at all).
- **Remove the guard.** Omit `--chain-id` entirely to disable the check. (You
  cannot set it to `0` to "turn it off" — `0` is rejected at startup, §3.2.)

---

## 7. "Signing feels slow"

This is expected and is **not a bug, a hang, or a network call** (the server
makes no outbound calls by construction). The cost is the scrypt
key-derivation function inside go-ethereum's `keystore.DecryptKey`, and it is
paid on **every** `sign_transaction` because the decrypted key is **never
cached** (ADR-010; source: README §7).

| Keystore type | scrypt `N` | Per-call latency |
|---------------|-----------:|------------------|
| Standard (geth default) | 262,144 | ~0.5–1 s |
| Light (`geth account new --lightkdf`) | 4,096 | ~50 ms |
| Weak test-only | 2 | ~1 ms (CI fixtures only) |

Everything else — RLP encoding, the actual ECDSA signature — is
sub-millisecond. So a `latency_ms` around 500–1000 on a successful sign with a
standard keystore is the KDF, full stop.

Two more facts that shape what you'll observe:

- **No warm path.** The first call and the thousandth call cost the same; there
  is nothing to pre-warm.
- **Calls are serialized.** A semaphore of 1 (ADR-006) ensures at most one live
  key scalar at a time, so concurrent sign requests **queue** rather than run in
  parallel — their latencies add up. The committed test fixture
  `keystore-light.json` is already light-scrypt (`N=4096`).

**Remedy for dev loops:** use a light-scrypt keystore
(`geth account new --lightkdf`). Do **not** use the weak `N=2` profile outside
CI fixtures — it provides no meaningful key protection.

---

## 8. `password_error` vs. `keystore_error`

These two codes look similar but sit at opposite ends of the lifecycle table
(README §6). The distinction tells you whether to **retry** or **restart**.

| Aspect | `password_error` | `keystore_error` |
|--------|------------------|------------------|
| When it fires | On a `sign_transaction` call. | At **startup** (boot-time snapshot). |
| Trigger | Password file unreadable, or wrong password (keystore MAC failure / `keystore.ErrDecrypt`). | Keystore missing/unreadable, malformed JSON, or no usable `"address"`. |
| Server state | **Keeps running.** | Process **exits non-zero**; it never reaches a serving state. |
| Operator action | **Transient fix, no restart.** The password file is re-read on every call, so fix the file (or fix the password) and just **retry** the tool call. | Fix the keystore file, then **restart** the binary. The address snapshot is frozen at boot. |
| Source | `internal/signing/decrypt.go` | `internal/signing/file_vault.go` |

Key consequence of the snapshot model: **replacing the keystore file on disk
does nothing until you restart** — the boot-time address is unchanged and the
binary keeps decrypting the old snapshot. By contrast, **rotating the password
file takes effect on the very next call** with no restart. (A useful corollary:
`get_address` is served from the boot snapshot and reads neither the password
file nor runs the KDF, so it stays answerable even after the password file is
rotated or made unreadable — source: `internal/server/handlers.go`
`makeGetAddressHandler`.)

One more nuance: not every decrypt failure is a `password_error`. A wrong
password (`keystore.ErrDecrypt`) is `password_error`; an *unknown cipher,
corrupted ciphertext, or unsupported KDF* is mapped to `internal_error` instead,
because changing the password won't help — the file itself needs attention
(source: `internal/signing/decrypt.go`).

---

## 9. Interpreting `--version`

```sh
./bin/eth-signer-mcp --version
# eth-signer-mcp version v1.0.0 (commit a358bef, built 2026-06-12T10:00:00Z, go1.26.0)
```

The line after `eth-signer-mcp version ` is four fields, populated from
`runtime/debug.ReadBuildInfo` (source: `internal/obs/buildinfo.go` — `Build` and
`Info.String`):

| Field | Source | `<unknown>` / `(devel)` case |
|-------|--------|------------------------------|
| **Version** | module version (`Main.Version`). | A `go build` from source with no VCS tag shows the Go toolchain placeholder **`(devel)`** — passed through unchanged because it is informative, not undeterminable. `<unknown>` only if build info is entirely absent. |
| **commit** | `vcs.revision` build setting. | **`<unknown>`** when absent — e.g. `go test` binaries and plain `go build` without VCS stamping. |
| **built** | `vcs.time` build setting. | **`<unknown>`** when absent (same conditions as commit). |
| **Go version** | `GoVersion` from build info. | Always present from the toolchain; `<unknown>` only if build info is entirely absent. |

How to read it:

- A **`make build`** binary (built with `-trimpath -buildvcs=true`) populates all
  four — that is a properly stamped build (source: README §2).
- A **`go build`** straight from source typically shows
  `(devel)` for Version and `<unknown>` for commit/built — this is a local,
  untagged dev binary, not a release.
- A **tagged release** populates Version with the tag (e.g. `v1.0.0`) and fills
  in commit and built.

So if `--version` shows `(devel)` or `<unknown>` fields, you are not running a
release artifact — rebuild with `make build` from a tagged checkout for a fully
stamped binary.

---

## See also

- [App reference README](../../README.md) — §9 error codes, §10 observability,
  §12 the short-form troubleshooting these sections expand on.
- [Demo walkthrough](../demo.md) — live stdio + HTTP captures, golden-vector
  parity, and curl one-liners for the 401/403 paths.
- [PRD](../../../../plan/prd.md) and
  [Architecture](../../../../plan/architecture.md) — the ADRs (003/004/006/007/
  008/009/010) cited throughout this guide.
