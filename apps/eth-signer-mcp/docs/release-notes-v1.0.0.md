# eth-signer-mcp v1.0.0 — Release Notes

**Release date:** 2026-06-13  
**Tag:** `eth-signer-mcp/v1.0.0`  
**Go module:** `github.com/rootwarp/blockchain-ai-tools/apps/eth-signer-mcp`

---

## Scope

### In scope (v1.0.0)

- `sign_transaction` MCP tool: **legacy (type 0, EIP-155)** and **EIP-1559
  (type 2)** transactions. Response includes `rawTransaction`,
  `signature {r, s, v}`, `hash`, and `from`.
- `get_address` MCP tool: returns the EIP-55-checksummed address from the
  loaded keystore.
- **Stdio transport** (default) and **Streamable HTTP transport** (`--http`)
  with bearer auth; identical tool surface on both transports.
- **`--strict-perms`** startup refusal (exit 2) on group/world-readable
  secret files.
- Structured JSON logging, per-signing audit line, rich `--version`.
- Byte-identical parity verified against ethers v6.16.0 on all committed
  golden vectors (see `internal/signing/testdata/vectors/`).

### Out of scope (P2 backlog — excluded by decision)

The following are not implemented in v1 and are tracked as P2 items:

| Feature | PRD item |
|---------|----------|
| EIP-2930 access-list transactions (type 1) | P2-SIGN-3 |
| EIP-4844 blob transactions (type 3) | P2-SIGN-4 |
| EIP-7702 setCode transactions (type 4) | P2-SIGN-5 |
| EIP-191 `personal_sign` message signing | P2-SIGN-1 |
| EIP-712 typed-data signing | P2-SIGN-2 |
| Audit log of signed hashes to a configurable file | P2-OBS-1 |
| Keystore directory / multi-account selection | P2-KEY-1 |

---

## Pinned versions

| Component | Version |
|-----------|---------|
| MCP Go SDK (`github.com/modelcontextprotocol/go-sdk`) | `v1.6.1` |
| MCP protocol revision | **`2025-11-25`** |
| go-ethereum (`github.com/ethereum/go-ethereum`) | `v1.17.3` |
| urfave/cli (`github.com/urfave/cli/v3`) | `v3.9.0` |
| Go toolchain | `1.26` (see `go.work` / `apps/eth-signer-mcp/go.mod`) |
| Foundry (cast) | `v1.7.1` (`.foundry-version`) — vector regeneration only; CI and the binary never invoke Foundry |

The MCP protocol revision `2025-11-25` is read verbatim from
`latestProtocolVersion = protocolVersion20251125 = "2025-11-25"` in
`mcp/shared.go` of the go-sdk v1.6.1 source.

The `cast` cross-check is deferred; vectors were generated and verified
with ethers v6.16.0 only. The deferred status is documented in
`internal/signing/testdata/vectors/cast-version.txt`.

---

## Latency

Signing computation **excluding the KDF is sub-millisecond**. The
dominant cost is the scrypt key-derivation function inside
`keystore.DecryptKey`. This cost is paid on **every** `sign_transaction`
call — **there is no warm path**. The decrypted key is never cached
(ADR-010).

| Keystore type | scrypt N | Approx. per-call latency |
|---------------|----------|--------------------------|
| Standard (geth default) | 262,144 | ~0.5–1 s |
| Light (`geth --lightkdf`) | 4,096 | ~50 ms |

### Measured benchmark numbers (issue 4.3, `internal/signing/bench_test.go`)

Machine: macOS, Apple M-series, 10 logical CPUs.

| Fixture | Median total | Median KDF | Non-KDF delta | Limit |
|---------|-------------|------------|---------------|-------|
| light-scrypt (N=4096) | ~35.5 ms | ~35.0 ms | ~470 µs | < 10 ms ✅ |
| standard-scrypt (N=262144) | ~416 ms | ~411 ms | ~4.9 ms | < 10 ms ✅ |

Cold start (vault construction + signer allocation — no KDF):

| Fixture | Median cold start | Limit |
|---------|-----------------|-------|
| light-scrypt (N=4096) | ~20–70 µs | < 200 ms ✅ |
| standard-scrypt (N=262144) | ~17–70 µs | < 200 ms ✅ |

Full benchmark evidence in `docs/verification-4.3.md` §4.

For dev loops, use a **light-scrypt keystore** (`geth account new
--lightkdf`, N=4096, ~50 ms). See also `README.md` §7 Latency.

---

## Keystore lifecycle

| Event | Behaviour |
|-------|-----------|
| **Binary starts** | Keystore JSON read; Ethereum address extracted — **boot-time snapshot, fail fast**. Missing / malformed keystore → `keystore_error`, non-zero exit. |
| **`sign_transaction` call** | **Password file re-read on every call.** Password rotation takes effect immediately; no restart needed. |
| **Keystore file replaced on disk** | Address snapshot unchanged — **restart required** to pick up the new key. |
| **Wrong password / unreadable file** | `password_error` returned; server stays running. Fix the file and retry. |

See also `README.md` §6 Keystore lifecycle.

---

## Security verification

`govulncheck ./...` run locally at the candidate commit (`a358bef`) is
clean (0 vulnerabilities). `govulncheck` is also enforced in CI (workflow
from issue 1.2) on every push; the team lead will confirm CI green at the
exact tagged SHA.

go-ethereum v1.17.3 is not affected by GO-2026-4314, GO-2026-4315,
GO-2026-4507, GO-2026-4508, or GO-2026-4511:

- GO-2026-4508 is fixed exactly at go-ethereum v1.17.0.
- GO-2026-4314, GO-2026-4315, GO-2026-4507, and GO-2026-4511 are each
  fixed at or before go-ethereum v1.16.9.

All five were verified against the Go vulnerability database in issue 4.3
(see `docs/verification-4.3.md` §2).

The two build-time guards (ADR-007 offline-import test, ADR-008 depguard)
and the sentinel-based leak scans were each verified load-bearing by
mutation re-checks in issue 4.4 (see `docs/verification-4.4.md` §2).

---

## Threat model

See `README.md` §8 Security posture & threat model and
`plan/prd.md` §Security & Threat Model for the full statement, including
in-scope adversaries, excluded adversaries (root/kernel, swap capture,
off-localhost callers), and hardening rules.

---

## Adoption metric evidence

Live stdio and HTTP sessions — including golden-vector byte-equality
proof, decoder verification, and 401/403 hardening surface captures —
are documented verbatim in [`docs/demo.md`](demo.md).

---

## Post-release smoke

The v1.0.0 smoke test (fresh clone of the tag, `make build`, `make test`,
both demos from `docs/demo.md` verbatim) is performed by the team lead at
release time per issue 4.5. The smoke transcript is recorded in the issue
4.5 PR description. It is not pre-claimed here.
