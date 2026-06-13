# Running over Streamable HTTP

This guide walks an operator or AI-agent integrator through standing up the
`eth-signer-mcp` **Streamable HTTP** transport for a *local* MCP client. You'll
generate a throwaway bearer token outside the repo, launch the server bound to a
loopback ephemeral port, read the bound port from stderr, then drive a full MCP
session — `initialize` → `tools/list` → `get_address` → `sign_transaction` — with
`curl`, using the byte-reproducible golden-vector payloads. Finally you'll exercise
the hardening surfaces (`401` on bad bearer, `403` on a forged `Host`) so you can
see exactly what each rejection looks like.

The server still **never broadcasts** and makes **no outbound network calls**
(offline by construction, ADR-007/008). HTTP is just a second transport over the
same signer; it returns broadcast-ready signed RLP that *you* submit elsewhere.

> If you're wiring a Claude Desktop-style client instead, prefer stdio — see the
> stdio quick-start in [`../../README.md`](../../README.md) §3. HTTP is for local
> programmatic clients that speak JSON-RPC over `POST /mcp`.

---

## 1. When HTTP makes sense — and the hard constraint

Use the HTTP transport when your MCP client connects over a socket rather than by
spawning the binary on stdio: a long-lived local agent process, a test harness, or
a `curl`-driven integration check. Both transports register the same two tools and
return **byte-identical** results (ADR-002 transport parity; see
[`../demo.md`](../demo.md) §Transport parity).

**The hard constraint: LOOPBACK ONLY.** Off-localhost exposure is *unsupported*.
`--http-addr` accepts only loopback addresses (`127.0.0.1` or `[::1]`); any
non-loopback value is **rejected at startup** before any listener binds:

```
--http-addr must be a loopback address (127.0.0.1 or [::1]); non-loopback binds are rejected (ADR-006)
```

(source: `cmd/eth-signer-mcp/config.go`, `validate()`). There is a second,
defense-in-depth check inside `RunHTTP`: even if validation were bypassed, a
non-loopback *bound* address is closed and rejected
(`RunHTTP: bound address is not loopback (ADR-006)`; source:
`internal/server/http.go`). Off-localhost network callers are an **explicitly
excluded adversary in v1** — do not put this behind a reverse proxy or `0.0.0.0`
bind and expose it. If you need remote access, that's out of scope; terminate it
locally and tunnel at a layer this tool doesn't manage.

---

## 2. Step 1 — Generate a throwaway bearer token (outside the repo tree)

Every HTTP request must carry `Authorization: Bearer <token>`. Generate a random
token into a `chmod 600` file **outside your clone** so it can never be committed:

```sh
# Create the file outside the repo tree (mktemp picks /tmp), lock it down, fill it:
TOKEN_FILE=$(mktemp /tmp/eth-signer-mcp-token.XXXXXX)
chmod 600 "$TOKEN_FILE"
openssl rand -hex 32 > "$TOKEN_FILE"
```

This produces a 32-byte (64 hex char) random token. Notes:

- **Never commit a token, and never inline it on the command line** — pass the
  *path*, not the value. The server reads the file once and stores only
  `sha256(token)` inside a redacting wrapper; the raw bytes are zeroed right after
  hashing (best-effort, ADR-009; source: `internal/server/auth.go`).
- `chmod 600` matters: at startup the binary checks the mode bits of every secret
  file (keystore, password, **and** token). Group/world-readable files trigger a
  warning by default, or a hard refusal (exit 2) under `--strict-perms`.
- The token may contain inner spaces if you want; the constructor strips only a
  single trailing `\n` (and a `\r` for CRLF), not all whitespace. A random hex
  token has none, so this is moot here.

---

## 3. Step 2 — Launch loopback-bound and read the port from stderr

Start the server with `--http`, an ephemeral loopback port, and the token file.
We use the committed **test-only** light-scrypt fixture for this walkthrough
(~50 ms per signing call):

```sh
# From the monorepo root:
./bin/eth-signer-mcp \
  --keystore      apps/eth-signer-mcp/internal/signing/testdata/keystore-light.json \
  --password-file apps/eth-signer-mcp/internal/signing/testdata/password.txt \
  --http \
  --http-addr     127.0.0.1:0 \
  --http-auth-token-file "$TOKEN_FILE"
```

> **TEST-ONLY KEY MATERIAL — DO NOT SEND REAL FUNDS.** The keystore/password above
> are committed low-value demo keys for address
> `0x9858EfFD232B4033E47d90003D41EC34EcaEda94`. For real use, point `--keystore`
> and `--password-file` at your own `chmod 600` files. See
> `internal/signing/testdata/README.md` for the full disclosure.

On startup the bound address is printed to **stderr** in two forms (source:
`internal/server/http.go`):

```
eth-signer-mcp listening on 127.0.0.1:<PORT>
{"level":"INFO","msg":"http server listening","addr":"127.0.0.1:<PORT>"}
```

Read the bound `host:port` from the `listening on` line. The server is ready for
requests the instant that line appears — no sleep needed.

**`:0` ephemeral vs a fixed port.** `--http-addr 127.0.0.1:0` (the default) asks
the OS to assign a free ephemeral port, which is the safest choice: no port-in-use
conflicts, and the port is unguessable until printed. The trade-off is that the
port changes on every launch, so you must scrape it from stderr. If you need a
**stable** port (e.g. a fixed client config), pin one explicitly, still on
loopback:

```sh
--http-addr 127.0.0.1:8849
# stderr → eth-signer-mcp listening on 127.0.0.1:8849
```

A scripted launch can capture the port without sleeping (this is exactly what the
demo script does — poll the stderr file for the announce line):

```sh
STDERR_FILE=$(mktemp /tmp/eth-signer-mcp-stderr.XXXXXX)
./bin/eth-signer-mcp \
  --keystore      apps/eth-signer-mcp/internal/signing/testdata/keystore-light.json \
  --password-file apps/eth-signer-mcp/internal/signing/testdata/password.txt \
  --http --http-addr 127.0.0.1:0 \
  --http-auth-token-file "$TOKEN_FILE" \
  2>"$STDERR_FILE" &
SERVER_PID=$!

# Poll for the announce line, then extract host:port:
until grep -q "listening on" "$STDERR_FILE"; do sleep 0.1; done
ADDR=$(grep -m1 "listening on" "$STDERR_FILE" | sed 's/.*listening on //' | tr -d '[:space:]')
MCP_URL="http://$ADDR/mcp"
echo "bound at $MCP_URL"
```

> **Never read or parse stdout** — on the HTTP transport stdout carries nothing of
> interest, and all logs (including the announce JSON above) go to stderr as
> newline-delimited JSON. The `host:port` lives only on the stderr announce line.

---

## 4. Step 3 — The request contract and a full session

### Request contract

Every request is a `POST` to the `/mcp` endpoint with three headers:

| Header | Value |
|--------|-------|
| `Content-Type` | `application/json` |
| `Accept` | `application/json, text/event-stream` |
| `Authorization` | `Bearer <token>` |

The `Accept` header **must** include `text/event-stream`: the SDK's
`StreamableHTTPHandler` replies with Server-Sent Events (`data: {...}` lines), not
plain JSON. Responses come back as SSE; extract the JSON from the `data:` line.

Streamable HTTP is **stateful**: `initialize` returns an `Mcp-Session-Id` response
header, you send a `notifications/initialized` notification, and every subsequent
call carries `Mcp-Session-Id: <id>`.

For the worked examples, export your bound URL and token once:

```sh
export MCP_URL="http://127.0.0.1:<PORT>/mcp"      # from the announce line
export TOKEN="$(cat "$TOKEN_FILE")"               # the value, for the header
```

### 4a. `initialize` — open a session, capture the session id

```sh
curl -si -X POST "$MCP_URL" \
  -H "Content-Type: application/json" \
  -H "Accept: application/json, text/event-stream" \
  -H "Authorization: Bearer $TOKEN" \
  -d '{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-03-26","capabilities":{},"clientInfo":{"name":"curl-guide","version":"1.0"}}}'
```

`-si` prints headers + body. Look for `HTTP/1.1 200 OK` and an
`Mcp-Session-Id:` response header — that value is your session id. Grab it:

```sh
SESSION_ID=$(curl -si -X POST "$MCP_URL" \
  -H "Content-Type: application/json" \
  -H "Accept: application/json, text/event-stream" \
  -H "Authorization: Bearer $TOKEN" \
  -d '{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-03-26","capabilities":{},"clientInfo":{"name":"curl-guide","version":"1.0"}}}' \
  | grep -i '^mcp-session-id:' | sed 's/^[^:]*:[[:space:]]*//' | tr -d '\r')
echo "session: $SESSION_ID"
```

Then send the required `initialized` notification (returns `202`, no body):

```sh
curl -s -X POST "$MCP_URL" \
  -H "Content-Type: application/json" \
  -H "Accept: application/json, text/event-stream" \
  -H "Authorization: Bearer $TOKEN" \
  -H "Mcp-Session-Id: $SESSION_ID" \
  -d '{"jsonrpc":"2.0","method":"notifications/initialized","params":{}}'
```

### 4b. `tools/list` — confirm the two tools

```sh
curl -s -X POST "$MCP_URL" \
  -H "Content-Type: application/json" \
  -H "Accept: application/json, text/event-stream" \
  -H "Authorization: Bearer $TOKEN" \
  -H "Mcp-Session-Id: $SESSION_ID" \
  -d '{"jsonrpc":"2.0","id":2,"method":"tools/list","params":{}}'
```

The SSE `data:` payload lists both `sign_transaction` and `get_address`.

### 4c. `get_address` — read-only, no KDF

`get_address` takes an empty argument object and is served from the boot-time
keystore snapshot — the password file is **not** read and no scrypt runs, so it's
instant and safe even if the password file was rotated:

```sh
curl -s -X POST "$MCP_URL" \
  -H "Content-Type: application/json" \
  -H "Accept: application/json, text/event-stream" \
  -H "Authorization: Bearer $TOKEN" \
  -H "Mcp-Session-Id: $SESSION_ID" \
  -d '{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"get_address","arguments":{}}}'
```

The `structuredContent` in the SSE `data:` line is:

```json
{"address":"0x9858EfFD232B4033E47d90003D41EC34EcaEda94"}
```

### 4d. `sign_transaction` — legacy-mainnet golden vector

This is the byte-reproducible legacy (type-0, EIP-155) vector. All numeric fields
are **strings** (decimal or `0x`-hex); `gasPrice` is legacy-only:

```sh
curl -s -X POST "$MCP_URL" \
  -H "Content-Type: application/json" \
  -H "Accept: application/json, text/event-stream" \
  -H "Authorization: Bearer $TOKEN" \
  -H "Mcp-Session-Id: $SESSION_ID" \
  -d '{"jsonrpc":"2.0","id":4,"method":"tools/call","params":{"name":"sign_transaction","arguments":{"type":"0x0","chainId":"1","nonce":"0","to":"0x9858EfFD232B4033E47d90003D41EC34EcaEda94","value":"1000000000000000000","data":"0xdeadbeef","gas":"100000","gasPrice":"20000000000"}}}'
```

The `structuredContent` (all fields always present) is byte-identical to the
committed golden vector:

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

The `rawTransaction` is ready for `eth_sendRawTransaction` — but **the caller
submits it; this server does not.** `from` is the recovered sender and equals the
keystore address.

### 4e. `sign_transaction` — EIP-1559 variant

EIP-1559 (type-2) uses `maxFeePerGas` / `maxPriorityFeePerGas` instead of
`gasPrice` (omit `gasPrice` for type 2):

```sh
curl -s -X POST "$MCP_URL" \
  -H "Content-Type: application/json" \
  -H "Accept: application/json, text/event-stream" \
  -H "Authorization: Bearer $TOKEN" \
  -H "Mcp-Session-Id: $SESSION_ID" \
  -d '{"jsonrpc":"2.0","id":5,"method":"tools/call","params":{"name":"sign_transaction","arguments":{"type":"0x2","chainId":"1","nonce":"42","to":"0x9858EfFD232B4033E47d90003D41EC34EcaEda94","value":"1000000000000000000","data":"0xcafebabe","gas":"100000","maxFeePerGas":"30000000000","maxPriorityFeePerGas":"2000000000"}}}'
```

Expected `structuredContent` (golden vector `1559-mainnet.json`):

```json
{
  "rawTransaction": "0x02f878012a84773594008506fc23ac00830186a0949858effd232b4033e47d90003d41ec34ecaeda94880de0b6b3a764000084cafebabec080a09c4861a936548597508b2582117dc2603d11b53d7da9db676204df34dca5ee49a048349fb03c9991300fdb9dff1569609d0bf2e4ad94a256b4624627219b262b99",
  "signature": {
    "r": "0x9c4861a936548597508b2582117dc2603d11b53d7da9db676204df34dca5ee49",
    "s": "0x48349fb03c9991300fdb9dff1569609d0bf2e4ad94a256b4624627219b262b99",
    "v": "0x0"
  },
  "hash": "0x8490c945e27a90c756b574fcb1d3ef42ab4522423ad0e6e3c4c25407d18ca78a",
  "from": "0x9858EfFD232B4033E47d90003D41EC34EcaEda94"
}
```

For the full field grammar (string-numeric rules, optional `to` for contract
creation, the 256 KiB decoded-`data` cap, EIP-55 checksum handling, the empty-only
`accessList`, and the tool-error wire contract), see
[Tool reference](04-tool-reference.md).

> **Latency:** every `sign_transaction` decrypts the keystore from scratch — the
> scrypt KDF dominates and there is no warm path (key never cached, ADR-010). The
> light fixture here is ~50 ms/call; a standard geth keystore (N=262144) is
> ~0.5–1 s/call. `get_address` runs no KDF and is instant.

---

## 5. The middleware pipeline (what each layer rejects)

Every request traverses four layers, **outermost → innermost** (ADR-006; source:
`internal/server/http.go`):

```
[1] MaxBytesHandler (1 MiB body cap)
      → [2] reqlog (request-id + structured log line)
            → [3] bearer auth (SHA-256 + constant-time compare → 401)
                  → [4] SDK StreamableHTTPHandler (DNS-rebinding guard → 403; tool dispatch)
```

1. **`MaxBytesHandler` — 1 MiB request body cap.** Wraps the entire pipeline so an
   oversized body is cut off before auth or the SDK reads it. When the cap is
   exceeded the body read fails; the SDK's JSON decoder then errors and the SDK
   returns **HTTP 400** for that decode failure (observed SDK v1.6.1 behavior,
   pinned in `bounds_test.go`). Note the distinction: the 1 MiB cap is the
   transport-level guard; the *application-level* `sign_transaction` `data` field
   also has its own 256 KiB **decoded** limit enforced in signing validation,
   which surfaces as an `invalid_input` tool error, not a 400.
2. **`reqlog` middleware.** Generates a UUIDv4 `request_id`, then on completion
   emits exactly one structured log line. It sits *outside* auth, so even rejected
   `401`/`403` requests are logged. It logs only `request_id`, `remote_addr`,
   `status`, `latency_ms` — **never** the URL, headers (especially not
   `Authorization`), or body (source: `internal/server/reqlog.go`):
   ```json
   {"level":"INFO","msg":"http request","request_id":"<uuid>","remote_addr":"127.0.0.1:<port>","status":200,"latency_ms":512}
   ```
3. **Bearer auth.** Computes `sha256(supplied)` and compares it against the stored
   `sha256(expected)` with `subtle.ConstantTimeCompare`. A missing or wrong token
   yields **401** with header `Www-Authenticate: Bearer` and an empty body; the
   SDK handler is never reached (source: `internal/server/auth.go`).
4. **SDK `StreamableHTTPHandler`.** With bearer auth passed, the SDK's
   DNS-rebinding guard runs first: a non-loopback `Host` header yields **403
   Forbidden** (`DisableLocalhostProtection` is left at its default `false`). Then
   tool dispatch runs.

---

## 6. Hardening demos (real curl one-liners)

With the server bound at `$MCP_URL` and `$TOKEN` set, these three one-liners
reproduce the rejection surfaces exactly as the demo script asserts them
([`../demo.md`](../demo.md) §401/403). Use `-si` to see status + headers.

### Missing bearer → 401

```sh
curl -si -X POST "$MCP_URL" \
  -H "Content-Type: application/json" \
  -H "Accept: application/json, text/event-stream" \
  -d '{"jsonrpc":"2.0","id":99,"method":"initialize","params":{"protocolVersion":"2025-03-26","capabilities":{},"clientInfo":{"name":"bad","version":"1.0"}}}'
# → HTTP/1.1 401 Unauthorized
#   Www-Authenticate: Bearer
#   Content-Length: 0
```

### Wrong bearer → 401 (with `Www-Authenticate: Bearer`)

```sh
curl -si -X POST "$MCP_URL" \
  -H "Content-Type: application/json" \
  -H "Accept: application/json, text/event-stream" \
  -H "Authorization: Bearer this-is-the-wrong-token" \
  -d '{"jsonrpc":"2.0","id":99,"method":"initialize","params":{"protocolVersion":"2025-03-26","capabilities":{},"clientInfo":{"name":"bad","version":"1.0"}}}'
# → HTTP/1.1 401 Unauthorized
#   Www-Authenticate: Bearer
#   Content-Length: 0
```

Both missing and wrong tokens look identical on the wire — empty body, same header
— by design: nothing derived from the token or its hash is ever returned.

### Forged `Host` header → 403 (DNS-rebinding guard)

This one carries the *correct* bearer, so auth passes; the SDK's rebinding guard
rejects the non-loopback `Host`:

```sh
curl -si -X POST "$MCP_URL" \
  -H "Content-Type: application/json" \
  -H "Accept: application/json, text/event-stream" \
  -H "Authorization: Bearer $TOKEN" \
  -H "Host: evil.example.com" \
  -d '{"jsonrpc":"2.0","id":99,"method":"initialize","params":{"protocolVersion":"2025-03-26","capabilities":{},"clientInfo":{"name":"bad","version":"1.0"}}}'
# → HTTP/1.1 403 Forbidden
#   Content-Type: text/plain; charset=utf-8
```

All three are visible in the server's stderr `http request` log lines with
`status` 401 / 401 / 403 respectively — confirming reqlog sits outside auth.

---

## 7. Token rotation requires a restart

The token hash is read **once, at startup**, and stored as `sha256(token)` for the
lifetime of the process (source: `NewBearerVerifierFromFile` in
`internal/server/auth.go`, invoked once at startup from `RunHTTP` in
`internal/server/http.go`). Replacing the contents of the token file at runtime
has **no effect** — the server keeps validating against the old hash, so requests
with the new token return `401`.

To rotate the bearer token:

```sh
# 1. Write the new token into the file (still chmod 600):
openssl rand -hex 32 > "$TOKEN_FILE"

# 2. Restart the server to pick it up:
#    SIGINT/SIGTERM the running process (graceful 3 s drain), then relaunch with
#    the same --http-auth-token-file path.
```

This is distinct from the **password** file, which is re-read on every
`sign_transaction` call (password rotation is immediate, no restart). Token and
keystore both follow the boot-time-snapshot model and need a restart; see the
keystore lifecycle table in [`../../README.md`](../../README.md) §6.

---

## 8. Automated end-to-end check and where to go next

For a one-command, hands-off version of everything above — token generation,
launch, port scrape, the four-call session, a **hard** byte-equality assertion
against the golden vector, and the 401/401/403 hardening demos — run the bundled
script:

```sh
# From the monorepo root:
scripts/demo/http-demo.sh             # builds the binary, then runs the full demo
scripts/demo/http-demo.sh --no-build  # reuse an existing bin/eth-signer-mcp
```

It creates the throwaway token with `mktemp` outside the repo tree, **never prints
the token**, kills the server and removes the token file on exit, and **exits
non-zero on any failure** (the golden-vector assertion is a hard check). It needs
`curl`, `python3`, and `openssl` on `PATH`.

**Cross-links:**

- [Tool reference](04-tool-reference.md) — full `sign_transaction` /
  `get_address` field grammar, the six error codes, and the tool-error wire
  contract.
- [Troubleshooting](07-troubleshooting.md) — diagnosing `401`s, `chain_id_mismatch`,
  permission warnings, and slow signing.
- [`../demo.md`](../demo.md) — live HTTP captures, the raw SSE responses, and
  golden-vector / transport-parity proof.
- [`../../README.md`](../../README.md) — app reference (flags §5, keystore
  lifecycle §6, security posture §8, observability §10).
