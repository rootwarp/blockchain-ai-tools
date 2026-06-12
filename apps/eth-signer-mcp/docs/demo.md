# eth-signer-mcp Demos — Stdio + Streamable HTTP

> **Issue 4.1 — adoption-metric evidence.**
> Both transports return the same byte-identical `rawTransaction`, equal to the
> committed golden vector.  Decoded transaction recovers to the fixture keystore address.
> **The transaction is never broadcast**; the binary has no outbound RPC capability
> (ADR-007).  **Off-localhost HTTP exposure is unsupported** (loopback-only bind
> enforced, ADR-006).

This document is verbatim-reproducible from a clean clone.  To re-run either demo,
follow the steps below exactly.  The 4.5 smoke test re-proves this from a fresh clone
of the `eth-signer-mcp/v1.0.0` tag.

---

## Prerequisites

```sh
# From the monorepo root:
make build           # produces bin/eth-signer-mcp

# For the HTTP demo script:
which curl           # required
which python3        # required (JSON extraction in http-demo.sh)
which openssl        # required (token generation)

# For transaction decoding (read-only — transaction is never broadcast):
# Option A: cast (Foundry, pinned via .foundry-version)
#   cast decode-tx <raw_tx>
# Option B: node + ethers v6 (used on this machine — see Decoder section)
#   node -e "const {ethers}=require('ethers'); const tx=ethers.Transaction.from('<raw>'); ..."
```

---

## Fixture keystore

Both demos use the **committed light-scrypt fixture** (`keystore-light.json`, N=4096).
This gives ~50 ms per decrypt — fast enough for interactive demos.  Standard-scrypt
keystores (N=262144, geth default) cost ~0.5–1 s per call; see the README §Latency.

> ⚠️  **TEST-ONLY KEY MATERIAL — DO NOT SEND REAL FUNDS.**
> Address: `0x9858EfFD232B4033E47d90003D41EC34EcaEda94`
> See `internal/signing/testdata/README.md` for the full disclosure.

Keystore paths (relative to the monorepo root):

```
apps/eth-signer-mcp/internal/signing/testdata/keystore-light.json
apps/eth-signer-mcp/internal/signing/testdata/password.txt   # "test-only-password-do-not-reuse\n"
```

---

## Demo 1: Stdio transport

### Canonical client: Claude Desktop (mcpServers config)

The canonical way to use eth-signer-mcp is via a **Claude Desktop-style MCP client**
that launches the binary from its `mcpServers` JSON configuration — no client code
changes, config only (PRD adoption metric).

Add the following block to your `claude_desktop_config.json` (or equivalent):

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
After restarting the client, the `sign_transaction` and `get_address` tools appear in
the tool-approval dialog (evidencing the PRD's "rely on the MCP client's approval flow"
threat-model stance).

### Fallback client: `scripts/demo/cmd/stdio-client` (used on this machine)

> **Troubleshooting note:** Claude Desktop was not available on this demo machine
> (no GUI MCP client installed).  The documented fallback is the **go-sdk v1.6.1
> example CLI client** or an equivalent minimal Go-SDK-based client.  The
> `scripts/demo/cmd/stdio-client` program below satisfies the adoption metric: it uses
> `mcp.CommandTransport` from go-sdk v1.6.1, launches the binary unmodified from its
> `mcpServers` equivalent config, and requires zero changes to the binary.

The fallback client is at `scripts/demo/cmd/stdio-client/main.go`.  Run it:

```sh
# From the monorepo root:
go run ./scripts/demo/cmd/stdio-client \
  -binary        ./bin/eth-signer-mcp \
  -keystore      ./apps/eth-signer-mcp/internal/signing/testdata/keystore-light.json \
  -password-file ./apps/eth-signer-mcp/internal/signing/testdata/password.txt \
  -want-raw-tx   0xf871808504a817c800830186a0949858effd232b4033e47d90003d41ec34ecaeda94880de0b6b3a764000084deadbeef26a082dc9bb5c5916b7728febf0a7269cef831012cf86eb0f2a4c69aa77ba92755dea073b452e7edc29e44a5f2b6887e90d59aec9ca1b81c858f592362e45a4e9ffcac
```

### Stdio demo output (live capture from this machine)

The server logs go to **stderr**; stdout carries MCP JSON-RPC frames only.

```
connecting to eth-signer-mcp via stdio (CommandTransport) ...

>>> calling get_address ...
<<< get_address result: {"address":"0x9858EfFD232B4033E47d90003D41EC34EcaEda94"}

>>> calling sign_transaction (legacy-mainnet vector) ...
    input: map[chainId:1 data:0xdeadbeef gas:100000 gasPrice:20000000000 nonce:0 to:0x9858EfFD232B4033E47d90003D41EC34EcaEda94 type:0x0 value:1000000000000000000]
<<< sign_transaction result:
    rawTransaction: 0xf871808504a817c800830186a0949858effd232b4033e47d90003d41ec34ecaeda94880de0b6b3a764000084deadbeef26a082dc9bb5c5916b7728febf0a7269cef831012cf86eb0f2a4c69aa77ba92755dea073b452e7edc29e44a5f2b6887e90d59aec9ca1b81c858f592362e45a4e9ffcac
    hash:           0xfa41dd66cc50da67d7e59d1b7277e794cbe69d5b10deb88a9ca2930fae65a239
    from:           0x9858EfFD232B4033E47d90003D41EC34EcaEda94
    signature.r:    0x82dc9bb5c5916b7728febf0a7269cef831012cf86eb0f2a4c69aa77ba92755de
    signature.s:    0x73b452e7edc29e44a5f2b6887e90d59aec9ca1b81c858f592362e45a4e9ffcac
    signature.v:    0x26

✓ rawTransaction MATCHES golden vector (byte-identical)

✓ stdio demo complete — server signs from the committed keystore fixture
  NOTE: this transaction is NOT broadcast — the binary has no RPC capability (ADR-007)
  NOTE: off-localhost exposure is unsupported
```

Server stderr (JSON structured logs):

```json
{"level":"INFO","msg":"eth-signer-mcp starting","log_level":"info"}
{"level":"WARN","msg":"file is group/world accessible; consider chmod 600 (use --strict-perms to refuse)","path":"./apps/eth-signer-mcp/internal/signing/testdata/keystore-light.json"}
{"level":"WARN","msg":"file is group/world accessible; consider chmod 600 (use --strict-perms to refuse)","path":"./apps/eth-signer-mcp/internal/signing/testdata/password.txt"}
{"level":"INFO","msg":"keystore loaded","address":"0x9858EfFD232B4033E47d90003D41EC34EcaEda94"}
{"level":"INFO","msg":"server run start"}
{"level":"INFO","msg":"server connecting"}
{"level":"INFO","msg":"server session connected","session_id":""}
{"level":"INFO","msg":"session initialized"}
{"level":"INFO","msg":"signing: transaction signed successfully","request_id":"3d66b78c-42ba-4f82-a164-7ebb6b6c5b63","tx_hash":"0xfa41dd66cc50da67d7e59d1b7277e794cbe69d5b10deb88a9ca2930fae65a239","chain_id":1,"nonce":0}
{"level":"INFO","msg":"server session disconnected","session_id":""}
{"level":"INFO","msg":"server session ended"}
```

> Full transcript (with timestamps) in `docs/demo-assets/stdio-session.txt`.

---

## Demo 2: Streamable HTTP transport

### Launch command

```sh
# Generate a throwaway bearer token (outside the repo tree; never commit):
TOKEN_FILE=$(mktemp /tmp/eth-signer-mcp-token.XXXXXX)
chmod 600 "$TOKEN_FILE"
openssl rand -hex 32 > "$TOKEN_FILE"

# Start the server (bound address printed to stderr):
./bin/eth-signer-mcp \
  --keystore    apps/eth-signer-mcp/internal/signing/testdata/keystore-light.json \
  --password-file apps/eth-signer-mcp/internal/signing/testdata/password.txt \
  --http \
  --http-addr   127.0.0.1:0 \
  --http-auth-token-file "$TOKEN_FILE"
```

Server startup stderr:

```
eth-signer-mcp listening on 127.0.0.1:<PORT>
{"level":"INFO","msg":"http server listening","addr":"127.0.0.1:<PORT>"}
```

Read the bound `host:port` from the `listening on` line.  The server is ready for
requests immediately after that line appears (no sleep required).

### Demo script

The `scripts/demo/http-demo.sh` script automates the entire HTTP demo:

```sh
# From the monorepo root:
scripts/demo/http-demo.sh           # builds binary then runs the demo
scripts/demo/http-demo.sh --no-build  # skip build (use existing bin/eth-signer-mcp)
```

The script:
1. Generates a throwaway token with `mktemp` **outside the repo tree**.
2. Starts the binary and reads the bound address from stderr (no sleeps).
3. Calls `initialize`, `tools/list`, `get_address`, `sign_transaction` with the bearer.
4. **Asserts** the returned `rawTransaction` is byte-equal to the committed golden vector.
5. Demonstrates the 401 and 403 hardening surfaces.
6. Kills the server and removes the token file.
7. Exits **non-zero on any failure** — the golden-vector assertion is a hard check.

The token is **never printed** or committed.

### HTTP demo output (live capture)

```
>>> repo root: <REPO_ROOT>
>>> --no-build: skipping build; using <REPO_ROOT>/bin/eth-signer-mcp
✓  server bound at http://127.0.0.1:58526
>>> Step 1: initialize
✓  initialized; session=T36SQD3MNIDWOGLSR5ZNFSTZBV
>>> Step 2: tools/list
✓  tools/list: sign_transaction + get_address present
>>> Step 3: get_address
✓  get_address: 0x9858EfFD232B4033E47d90003D41EC34EcaEda94
>>> Step 4: sign_transaction (legacy-mainnet golden vector)
>>>   rawTransaction: 0xf871808504a817c800830186a0949858effd232b4033e47d90003d41ec34ecaeda94880de0b6b3a764000084deadbeef26a082dc9bb5c5916b7728febf0a7269cef831012cf86eb0f2a4c69aa77ba92755dea073b452e7edc29e44a5f2b6887e90d59aec9ca1b81c858f592362e45a4e9ffcac
>>>   from:           0x9858EfFD232B4033E47d90003D41EC34EcaEda94
>>>   hash:           0xfa41dd66cc50da67d7e59d1b7277e794cbe69d5b10deb88a9ca2930fae65a239
>>> Asserting rawTransaction == golden vector ...
✓  rawTransaction is byte-identical to the committed golden vector
✓  recovered from == fixture address (0x9858EfFD232B4033E47d90003D41EC34EcaEda94)
>>> Security demo 1: missing bearer token → 401
✓  missing bearer → 401 Unauthorized
>>> Security demo 2: wrong bearer token → 401
✓  wrong bearer → 401 Unauthorized
>>> Security demo 3: forged Host header (DNS-rebinding guard) → 403
✓  forged Host header → 403 Forbidden (DNS-rebinding guard)

✓ HTTP demo complete
  NOTE: the signed transaction was NOT broadcast — the binary has no RPC capability (ADR-007)
  NOTE: off-localhost exposure is unsupported (loopback-only bind enforced)
```

### 401 and 403 curl one-liners (hardening surface)

Assuming the server is bound at `http://127.0.0.1:<PORT>`:

```sh
# 401: missing bearer token
curl -si -X POST http://127.0.0.1:<PORT>/mcp \
  -H "Content-Type: application/json" \
  -H "Accept: application/json, text/event-stream" \
  -d '{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-03-26","capabilities":{},"clientInfo":{"name":"test","version":"1.0"}}}'
# → HTTP/1.1 401 Unauthorized
#   Www-Authenticate: Bearer
#   Content-Length: 0

# 401: wrong bearer token
curl -si -X POST http://127.0.0.1:<PORT>/mcp \
  -H "Content-Type: application/json" \
  -H "Accept: application/json, text/event-stream" \
  -H "Authorization: Bearer wrong-token-value" \
  -d '{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-03-26","capabilities":{},"clientInfo":{"name":"test","version":"1.0"}}}'
# → HTTP/1.1 401 Unauthorized
#   Www-Authenticate: Bearer
#   Content-Length: 0

# 403: forged Host header (DNS-rebinding guard)
curl -si -X POST http://127.0.0.1:<PORT>/mcp \
  -H "Content-Type: application/json" \
  -H "Accept: application/json, text/event-stream" \
  -H "Authorization: Bearer <YOUR_TOKEN>" \
  -H "Host: evil.example.com" \
  -d '{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-03-26","capabilities":{},"clientInfo":{"name":"test","version":"1.0"}}}'
# → HTTP/1.1 403 Forbidden
#   Content-Type: text/plain; charset=utf-8
#   Content-Length: 50
```

> Full transcript and raw SSE responses in `docs/demo-assets/http-session.txt`.

---

## Transport parity (ADR-002)

Both transports return **byte-identical** `rawTransaction`:

```
0xf871808504a817c800830186a0949858effd232b4033e47d90003d41ec34ecaeda94880de0b6b3a764000084deadbeef26a082dc9bb5c5916b7728febf0a7269cef831012cf86eb0f2a4c69aa77ba92755dea073b452e7edc29e44a5f2b6887e90d59aec9ca1b81c858f592362e45a4e9ffcac
```

This is the committed golden vector from
`internal/signing/testdata/vectors/legacy-mainnet.json` (`expected.raw_tx`).
The automated parity test (Issue 3.8 / `internal/server/parity_transport_test.go`)
asserts this byte-equality for every test run.

---

## Transaction decoder verification

**Decoder used:** ethers v6 (`cast` / Foundry not installed on this machine; see
`internal/signing/testdata/vectors/cast-version.txt` for the cast deferred status).

```sh
# Install ethers v6 (one-off; any directory outside the repo tree):
cd /tmp && mkdir ethers-decode && cd ethers-decode
npm init -y && npm install ethers@6

# Decode the raw transaction (read-only — no broadcast):
node -e "
  const { ethers } = require('ethers');
  const raw = '0xf871808504a817c800830186a0949858effd232b4033e47d90003d41ec34ecaeda94880de0b6b3a764000084deadbeef26a082dc9bb5c5916b7728febf0a7269cef831012cf86eb0f2a4c69aa77ba92755dea073b452e7edc29e44a5f2b6887e90d59aec9ca1b81c858f592362e45a4e9ffcac';
  const tx = ethers.Transaction.from(raw);
  console.log('type:    ', tx.type, '(legacy EIP-155)');
  console.log('chainId: ', tx.chainId.toString());
  console.log('nonce:   ', tx.nonce);
  console.log('to:      ', tx.to);
  console.log('value:   ', tx.value.toString(), 'wei (1 ETH)');
  console.log('gasLimit:', tx.gasLimit.toString());
  console.log('gasPrice:', tx.gasPrice.toString(), 'wei (20 Gwei)');
  console.log('data:    ', tx.data);
  console.log('from:    ', tx.from, '(recovered sender)');
  console.log('hash:    ', tx.hash);
"
```

Output (live capture on this machine):

```
type:     0 (legacy EIP-155)
chainId:  1
nonce:    0
to:       0x9858EfFD232B4033E47d90003D41EC34EcaEda94
value:    1000000000000000000 wei (1 ETH)
gasLimit: 100000
gasPrice: 20000000000 wei (20 Gwei)
data:     0xdeadbeef
from:     0x9858EfFD232B4033E47d90003D41EC34EcaEda94  (recovered sender)
hash:     0xfa41dd66cc50da67d7e59d1b7277e794cbe69d5b10deb88a9ca2930fae65a239
```

`from` matches the fixture address and `hash` matches the golden vector — **the
recovered sender is the committed keystore address**.

> **This transaction is NOT broadcast.**  The `ethers.Transaction.from()` call
> is read-only decoding.  The binary itself has no RPC capability (ADR-007): there is
> no `ethclient` import anywhere in `internal/signing/`, enforced by the offline-import
> test (`internal/signing/offline_test.go`) and depguard rules (ADR-008).

---

## Latency note

These demos use the **light-scrypt fixture** (N=4096, ~50 ms per signing call) for fast
feedback.  Production keystores created with `geth account new` (standard scrypt, N=262144)
cost ~0.5–1 s per call.  This is by design: scrypt KDF cost is paid on **every** signing
call because the decrypted key material is never cached (ADR-010 / no warm path).
See the README §Latency for the full statement.

---

## Security notes

- **Transaction not broadcast:** The binary makes no outbound network calls.
  The `internal/signing` package transitively imports no HTTP/RPC client
  (ADR-007 offline-import test; ADR-008 depguard).
- **Off-localhost exposure unsupported:** `--http` binds exclusively on loopback.
  Non-loopback `--http-addr` values are rejected at startup (ADR-006).
- **Bearer auth required for HTTP:** Every request must carry `Authorization: Bearer <token>`.
  Missing or incorrect bearer → 401 (constant-time compare, ADR-005).
- **DNS-rebinding protection:** The SDK's `StreamableHTTPHandler` rejects requests with
  a non-loopback `Host` header even when bearer auth passes → 403.
- **Key material:** The private key is decrypted once per signing call and zeroed
  immediately after use (best-effort; Go may retain transient copies, stated in the README).
