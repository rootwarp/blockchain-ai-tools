# eth-signer-mcp — Usage Guides

These are **task-oriented walkthroughs** for setting up, integrating, and
operating `eth-signer-mcp` — the strictly-offline Ethereum-transaction signing
MCP server. They complement, rather than duplicate, the reference material: the
[app README](../../README.md) is the canonical flag/error/architecture reference,
and [`../demo.md`](../demo.md) holds the live captures (stdio + HTTP sessions,
raw SSE responses, and golden-vector / transport-parity proof). Each guide here
takes you end to end through one job — build and verify a signature, wire the
server into a client, deploy it for real — using copy-pasteable commands and the
byte-reproducible golden-vector payloads.

---

## Which guide do I need?

| Your goal | Guide |
|---|---|
| Get from a fresh clone to a verified, byte-identical signature in five steps | [01-getting-started.md](01-getting-started.md) |
| Wire the server into a Claude Desktop-style (or any) **stdio** MCP client via an `mcpServers` block | [02-stdio-clients.md](02-stdio-clients.md) |
| Stand up the **Streamable HTTP** transport, drive it with `curl`, and exercise the 401/403 surfaces | [03-http-transport.md](03-http-transport.md) |
| Look up the exact request/response contract for `sign_transaction` and `get_address` (fields, encodings, error codes) | [04-tool-reference.md](04-tool-reference.md) |
| Take it from the test fixture to a real deployment: production keystore, permissions, `--strict-perms`, `--chain-id`, threat model | [05-production-hardening.md](05-production-hardening.md) |
| Wire the signer into an **AI agent**: tool discovery, building a fully-specified tx, broadcasting the result, handling errors/approvals | [06-agent-integration.md](06-agent-integration.md) |
| Diagnose a problem: error codes, startup failures, the permission warning, 401/403, `chain_id_mismatch`, slow signing, reading the logs | [07-troubleshooting.md](07-troubleshooting.md) |

---

## Recommended reading order

A newcomer should read in roughly this sequence:

1. **[01-getting-started.md](01-getting-started.md)** — build the binary and
   produce one verified signature. Start here.
2. **[02-stdio-clients.md](02-stdio-clients.md)** *or*
   **[03-http-transport.md](03-http-transport.md)** — pick the transport that
   matches how your client reaches the server (stdio if the client launches the
   process; Streamable HTTP if it connects over a loopback socket).
3. **[04-tool-reference.md](04-tool-reference.md)** — the full, transport-agnostic
   field-and-error contract for both tools.
4. **[06-agent-integration.md](06-agent-integration.md)** — if you are wiring the
   server into an AI agent rather than driving it by hand.
5. **[05-production-hardening.md](05-production-hardening.md)** — before you point
   it at a real key: keystore strength, permissions, the chain-id guard, and an
   honest threat model.

Keep **[07-troubleshooting.md](07-troubleshooting.md)** as the
reference-when-stuck — reach for it the moment something prints an error code, a
warning, or an unexpected status, at any point above.

---

## Conventions

A few invariants hold across every guide; internalize them once:

- **All logs go to stderr** as newline-delimited JSON (`log/slog`). **stdout is
  reserved for MCP JSON-RPC frames** on the stdio transport — never redirect,
  print to, or parse stdout.
- **The binary never broadcasts.** `sign_transaction` returns broadcast-ready
  signed RLP (`rawTransaction`); submitting it via `eth_sendRawTransaction` is the
  caller's job. There are no outbound network calls by construction (ADR-007/008).
- **HTTP is loopback-only.** `--http-addr` accepts only `127.0.0.1` or `[::1]`;
  non-loopback binds are rejected at startup (ADR-006). Off-localhost exposure is
  unsupported in v1.
- **The committed keystore is TEST-ONLY.**
  `internal/signing/testdata/keystore-light.json` (light scrypt, address
  `0x9858EfFD232B4033E47d90003D41EC34EcaEda94`) and
  `internal/signing/testdata/password.txt` are low-value demo/CI keys whose
  private key is public to anyone who clones the repo. **Never send real funds to
  that address**, and point `--keystore` / `--password-file` at your own
  `chmod 600` files for anything real.

---

*Reference docs:* [app README](../../README.md) ·
[live demo + golden-vector parity](../demo.md) ·
[PRD](../../../../plan/prd.md) ·
[architecture](../../../../plan/architecture.md).
