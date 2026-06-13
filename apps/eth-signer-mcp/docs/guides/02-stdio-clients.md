# Integrating with MCP Clients over stdio

This guide shows how to wire **eth-signer-mcp** into a Claude Desktop-style MCP
client — or any MCP client that launches servers over **stdio** — using an
`mcpServers` configuration block. By the end you will have the binary launched
on demand by your client, the `sign_transaction` and `get_address` tools showing
up in the tool-approval dialog, and a clear picture of where logs go, when a
restart is required, and how to verify the whole thing headlessly without a GUI.

eth-signer-mcp is a **strictly offline** signer: it signs fully-specified
Ethereum transactions and returns broadcast-ready signed RLP. It **never
broadcasts** and makes no outbound network calls by construction (source:
`internal/signing/offline_test.go`, ADR-007). You — or your agent — submit the
returned `rawTransaction` yourself.

---

## 1. When to use stdio (and when not to)

The binary speaks the same MCP protocol over two transports; pick the one that
matches how the client reaches the server.

**Use stdio when:**

- The server runs **locally**, on the same machine as the client.
- It is **single-user** — one human or agent driving one keystore.
- The client itself **launches and owns the process** (Claude Desktop and most
  desktop MCP clients spawn each server as a child process and pipe stdin/stdout
  to it). No port, no token, no listener.

This is the **default transport**: with no `--http` flag the binary speaks MCP
JSON-RPC over stdin/stdout (source: `internal/server/stdio.go`).

**Use Streamable HTTP instead when** the client is a separate process that
connects over a socket (a shared local daemon, a sidecar, a test harness POSTing
to `/mcp`). HTTP adds bearer auth and a DNS-rebinding guard, and binds
**loopback-only** — off-localhost exposure is unsupported (source: `README.md`
§4, ADR-006). See the HTTP integration guide:
[03-http-transport.md](03-http-transport.md).

Both transports return **byte-identical** signed transactions for the same input
(transport parity, ADR-002; see [`../demo.md`](../demo.md)), so the choice is
purely about how the client connects, not about what you get back.

---

## 2. The `mcpServers` configuration block

A stdio MCP client launches the server from a JSON config entry. The two pieces
that matter are:

- **`command`** — the **absolute path** to the `eth-signer-mcp` binary.
- **`args`** — the CLI flags, at minimum `--keystore` and `--password-file`,
  each as its own array element (flag and value are separate strings).

First build the binary from the monorepo root:

```sh
make build                      # -trimpath -buildvcs=true → bin/eth-signer-mcp
./bin/eth-signer-mcp --version  # confirms it runs
```

Then add the following to `claude_desktop_config.json` (or your client's
equivalent config file):

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

Replace every `/ABSOLUTE/PATH/TO/` with the absolute path to your monorepo clone
(source: `README.md` §3, `../demo.md` Demo 1).

> ⚠️ **TEST-ONLY KEY MATERIAL — DO NOT SEND REAL FUNDS.** The paths above point
> at the committed light-scrypt **test fixture**
> (`internal/signing/testdata/keystore-light.json`, address
> `0x9858EfFD232B4033E47d90003D41EC34EcaEda94`, password file contents
> `test-only-password-do-not-reuse`). These are low-value demo/CI keys. In
> production, point `--keystore` / `--password-file` at **your own** files,
> `chmod 600`, and outside any repo tree.

### Absolute paths are mandatory

Use **absolute paths** for `command`, `--keystore`, and `--password-file`. A
GUI client spawns the server with an unpredictable working directory (often
`/` or the client's app bundle), so relative paths resolve against the wrong
place and the launch fails — typically with the server exiting immediately
(see §8). This is the single most common stdio misconfiguration.

### One account per instance

A single binary instance signs for exactly **one** keystore address. To serve a
second account, add a second entry under `mcpServers` with a different name and
its own `--keystore` / `--password-file`.

---

## 3. Adding flags inside `args`

Any of the binary's CLI flags can be appended to the `args` array — same rule:
flag and value are separate strings. The flags most useful for a stdio
deployment:

```json
{
  "mcpServers": {
    "eth-signer-mcp": {
      "command": "/ABSOLUTE/PATH/TO/bin/eth-signer-mcp",
      "args": [
        "--keystore",      "/ABSOLUTE/PATH/TO/keystore.json",
        "--password-file", "/ABSOLUTE/PATH/TO/password.txt",
        "--chain-id",      "1",
        "--strict-perms",
        "--log-level",     "debug"
      ]
    }
  }
}
```

- **`--chain-id <uint>`** — optional replay-safety guard. When set, any
  `sign_transaction` whose `chainId` differs from this value is refused with
  `chain_id_mismatch`. Value `0` is **rejected at startup** (replay-unprotected);
  omit the flag entirely to disable the guard (source: `README.md` §5,
  `cmd/eth-signer-mcp/config.go`). `--help` renders the type as `uint`.
- **`--strict-perms`** — refuse to start (exit 2) if any secret file is
  group- or world-readable. Without it the binary only **warns**. Recommended
  for production once your files are `chmod 600`.
- **`--log-level <level>`** — `debug | info | warn | error` (case-insensitive,
  default `info`). `debug` is handy while wiring up the client; an invalid value
  is a startup error.

**Production hardening (forward reference).** A real deployment swaps the test
fixture for a `chmod 600` keystore/password under your control, adds
`--strict-perms`, and usually pins `--chain-id`. The full hardening checklist —
file permissions, light- vs standard-scrypt latency tradeoffs, the threat model
— lives in the production hardening guide
([05-production-hardening.md](05-production-hardening.md)) and `README.md` §8.

The complete flag list is in `README.md` §5 and `./bin/eth-signer-mcp --help`.
The HTTP-only flags (`--http`, `--http-addr`, `--http-auth-token-file`) have no
effect on a stdio launch; if you set `--http` here the binary becomes an HTTP
server instead — see [03-http-transport.md](03-http-transport.md).

---

## 4. The tool-approval flow

After you save the config, **restart the client** so it re-reads `mcpServers`
and (re)launches the server. On the next session the client discovers two tools
and they appear in its tool-approval dialog:

- **`sign_transaction`** — "Sign a fully-specified Ethereum transaction (type 0
  / legacy or type 2 / EIP-1559) with the loaded keystore … The result is NOT
  broadcast — the caller is responsible for submission." (source:
  `internal/server/server.go`).
- **`get_address`** — "Return the EIP-55 checksummed Ethereum address of the
  loaded keystore account … read-only … the password file is NOT read and no KDF
  runs on this path." (source: `internal/server/server.go`).

**The client gates each call.** The server does not implement its own
allow/deny prompt; by design it **relies on the MCP client's approval flow** to
authorize each tool invocation — this is the deliberate threat-model stance for
stdio (source: `../demo.md` Demo 1; PRD threat model). When your agent (or you)
trigger a `sign_transaction`, the client shows the approval dialog; the server
only signs after the client forwards the approved call. Treat that dialog as the
authorization boundary — anyone able to approve calls in the client can sign
with the loaded key.

A successful sign emits one audit line to **stderr** (see §5):

```json
{"level":"INFO","msg":"signing: transaction signed successfully","request_id":"<uuid>","tx_hash":"0x…","chain_id":1,"nonce":0}
```

The transaction body (`to`, `value`, calldata) is **never logged** (source:
`README.md` §10).

---

## 5. stdout vs stderr discipline

This separation is load-bearing for stdio and you must respect it:

- **stdout is reserved for MCP JSON-RPC frames.** The SDK's `StdioTransport`
  writes newline-delimited JSON-RPC to `os.Stdout`; nothing else in the server
  ever writes there (source: `internal/server/stdio.go`). **Never redirect
  stdout, never wrap the binary in a script that prints to stdout, never enable
  a shell that emits a banner on stdout** — any stray byte corrupts the frame
  stream and the client's connection breaks.
- **All logs go to stderr** as newline-delimited JSON (`log/slog`); `--log-level`
  sets the verbosity (source: `README.md` §10).

### Reading stderr when the client hides it

Desktop clients capture the child process's stderr but do not always surface it.
To see the logs:

- **Claude Desktop**: the per-server stderr is written to the client's MCP log
  files (look for an `mcp-server-eth-signer-mcp` log under the app's logs
  directory; on macOS that is typically `~/Library/Logs/Claude/`). Consult your
  client's own docs for the exact path.
- **Any client**: run the exact `command` + `args` from your config **manually**
  in a terminal to watch stderr live. Because the binary waits on stdin for MCP
  frames, you will see only the startup logs and then it blocks (that is
  correct — there is no client driving it):

```sh
/ABSOLUTE/PATH/TO/bin/eth-signer-mcp \
  --keystore      /ABSOLUTE/PATH/TO/apps/eth-signer-mcp/internal/signing/testdata/keystore-light.json \
  --password-file /ABSOLUTE/PATH/TO/apps/eth-signer-mcp/internal/signing/testdata/password.txt
# stderr, in order:
# {"level":"INFO","msg":"eth-signer-mcp starting","log_level":"info"}
# {"level":"WARN","msg":"file is group/world accessible; consider chmod 600 (use --strict-perms to refuse)","path":".../keystore-light.json"}
# {"level":"WARN","msg":"file is group/world accessible; consider chmod 600 (use --strict-perms to refuse)","path":".../password.txt"}
# {"level":"INFO","msg":"keystore loaded","address":"0x9858EfFD232B4033E47d90003D41EC34EcaEda94"}
# {"level":"INFO","msg":"server run start"}
# ... then it blocks on stdin (Ctrl-C to exit) ...
```

(Log shapes from `../demo.md` Demo 1 stderr capture.) Seeing the `keystore
loaded` line with the expected address confirms the launch is healthy.

---

## 6. Restart semantics

Two pieces of secret state behave differently, and the difference matters
operationally (source: `README.md` §6, `cmd/eth-signer-mcp/...` keystore
lifecycle):

| Change you make | Takes effect | Action required |
|---|---|---|
| **Replace the keystore file** (new key) | Not until restart | The address is a **boot-time snapshot**, read once at startup. **Restart** the client (or the server process) to pick up the new key. |
| **Rotate the password** | Immediately | The **password file is re-read on every `sign_transaction` call**. No restart needed — the next sign uses the new password. |

Consequences:

- After swapping a keystore, the running instance keeps signing for the **old**
  address until you restart. `get_address` will also keep reporting the old
  address (it is served from the same boot snapshot).
- If you rotate the password but the file is wrong or unreadable, the next sign
  returns `password_error` and the **server stays running** — fix the file and
  retry, no restart (source: `README.md` §6, §9).
- A missing or malformed keystore at startup is fatal: `keystore_error` and a
  non-zero exit. In a desktop client this shows up as the server exiting
  immediately (see §8).

Because every sign re-reads the password and re-decrypts the keystore from
scratch (the key is never cached, ADR-010), each call pays the scrypt KDF cost:
~50 ms for the light-scrypt fixture, ~0.5–1 s for a standard geth keystore. This
is per call, not amortized (source: `README.md` §7). For dev loops, prefer a
light-scrypt keystore (`geth account new --lightkdf`).

---

## 7. Verifying it works without a GUI

You do not need a desktop client to prove the integration. The repo ships a
**headless equivalent** that exercises the exact same stdio launch path: a small
Go program that uses the go-sdk v1.6.1 `mcp.CommandTransport` to spawn the
binary unmodified — the programmatic analogue of an `mcpServers` block — then
calls `get_address` and `sign_transaction` (source: `../demo.md` Demo 1
"Fallback client", `scripts/demo/cmd/stdio-client/main.go`).

Run it from the monorepo root:

```sh
go run ./scripts/demo/cmd/stdio-client \
  -binary        ./bin/eth-signer-mcp \
  -keystore      ./apps/eth-signer-mcp/internal/signing/testdata/keystore-light.json \
  -password-file ./apps/eth-signer-mcp/internal/signing/testdata/password.txt \
  -want-raw-tx   0xf871808504a817c800830186a0949858effd232b4033e47d90003d41ec34ecaeda94880de0b6b3a764000084deadbeef26a082dc9bb5c5916b7728febf0a7269cef831012cf86eb0f2a4c69aa77ba92755dea073b452e7edc29e44a5f2b6887e90d59aec9ca1b81c858f592362e45a4e9ffcac
```

> ⚠️ Same **TEST-ONLY** fixture warning as §2 — do not send real funds to
> `0x9858EfFD232B4033E47d90003D41EC34EcaEda94`.

Expected result (live capture, `../demo.md` Demo 1):

```
>>> calling get_address ...
<<< get_address result: {"address":"0x9858EfFD232B4033E47d90003D41EC34EcaEda94"}

>>> calling sign_transaction (legacy-mainnet vector) ...
<<< sign_transaction result:
    rawTransaction: 0xf871808504a817c800830186a0949858effd232b4033e47d90003d41ec34ecaeda94880de0b6b3a764000084deadbeef26a082dc9bb5c5916b7728febf0a7269cef831012cf86eb0f2a4c69aa77ba92755dea073b452e7edc29e44a5f2b6887e90d59aec9ca1b81c858f592362e45a4e9ffcac
    hash:           0xfa41dd66cc50da67d7e59d1b7277e794cbe69d5b10deb88a9ca2930fae65a239
    from:           0x9858EfFD232B4033E47d90003D41EC34EcaEda94
    signature.r:    0x82dc9bb5c5916b7728febf0a7269cef831012cf86eb0f2a4c69aa77ba92755de
    signature.s:    0x73b452e7edc29e44a5f2b6887e90d59aec9ca1b81c858f592362e45a4e9ffcac
    signature.v:    0x26

✓ rawTransaction MATCHES golden vector (byte-identical)
```

This is the **`legacy-mainnet` golden vector**
(`type:"0x0", chainId:"1", nonce:"0", to:"0x9858…da94",
value:"1000000000000000000", data:"0xdeadbeef", gas:"100000",
gasPrice:"20000000000"`). The `-want-raw-tx` flag asserts byte-equality and the
program exits non-zero on mismatch — so a clean exit is proof the stdio launch,
the keystore, and the password file all work end to end. The program also
accepts an optional `-chain-id` flag that it forwards to the server as
`--chain-id`, mirroring §3.

If you prefer to drive an EIP-1559 transaction, the matching golden vector is
`type:"0x2", chainId:"1", nonce:"42", to:"0x9858…da94",
value:"1000000000000000000", data:"0xcafebabe", gas:"100000",
maxFeePerGas:"30000000000", maxPriorityFeePerGas:"2000000000"` →
`rawTransaction 0x02f878012a84773594008506fc23ac00830186a0949858effd232b4033e47d90003d41ec34ecaeda94880de0b6b3a764000084cafebabec080a09c4861a936548597508b2582117dc2603d11b53d7da9db676204df34dca5ee49a048349fb03c9991300fdb9dff1569609d0bf2e4ad94a256b4624627219b262b99`,
`hash 0x8490c945e27a90c756b574fcb1d3ef42ab4522423ad0e6e3c4c25407d18ca78a`.
For the full input/output field reference, see the tool reference guide
([04-tool-reference.md](04-tool-reference.md)).

---

## 8. Troubleshooting pointers

A few stdio-specific failure modes and where to look:

- **Tools don't appear in the approval dialog.** The client did not connect to a
  healthy server. Restart the client (config changes are only read on restart,
  §6). Then run the binary manually (§5) and confirm you see the `keystore
  loaded` line — if startup fails, the client never gets a tool list.

- **Server "exits immediately" / connection drops at launch.** Almost always one
  of: a **relative path** that doesn't resolve in the client's working directory
  (§2 — use absolute paths); a missing/malformed keystore (`keystore_error`,
  non-zero exit); or `--strict-perms` set against a group/world-readable file
  (exit 2). Reproduce by running the exact `command` + `args` in a terminal and
  reading stderr.

- **Every sign fails with `password_error`.** The password file is unreadable or
  the password is wrong (keystore MAC failure). The server keeps running — fix
  the file and retry (no restart). Note the password is re-read each call (§6).

- **`chain_id_mismatch` on otherwise-valid transactions.** The transaction's
  `chainId` doesn't match your `--chain-id` guard (§3). Align the transaction's
  `chainId` or drop the flag.

- **Signing feels slow.** Each call re-runs the scrypt KDF (§6). Use a
  light-scrypt keystore for dev.

For the full diagnostic matrix — log lines to grep for, the complete error-code
table, and step-by-step fixes — see the troubleshooting guide
([07-troubleshooting.md](07-troubleshooting.md)), `README.md` §9 (error codes)
and §12 (troubleshooting).

---

*Related:* [03-http-transport.md](03-http-transport.md) (the HTTP transport) ·
[04-tool-reference.md](04-tool-reference.md) (full tool I/O) ·
[07-troubleshooting.md](07-troubleshooting.md) · [`../demo.md`](../demo.md)
(live captures + golden-vector parity) · [`../../README.md`](../../README.md)
(app reference).
