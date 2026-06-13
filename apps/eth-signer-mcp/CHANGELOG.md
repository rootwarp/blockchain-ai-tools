# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/).

## [Unreleased]

## [v1.0.0] - 2026-06-13

### Added

- **`sign_transaction` tool** — accepts a fully-specified legacy (type 0,
  EIP-155) or EIP-1559 (type 2) transaction in JSON and returns a
  broadcast-ready RLP-encoded signed transaction. Response includes
  `rawTransaction` (0x-prefixed hex), `signature {r, s, v}`, `hash`
  (canonical transaction hash), and `from` (EIP-55-checksummed keystore
  address, cross-checked against the recovered sender on every call).

- **`get_address` tool** — returns the checksummed Ethereum address from
  the loaded keystore. No password is required; uses the keystore's stored
  address field.

- **Stdio transport** (default) — launched from an MCP client's
  `mcpServers` config with no client code changes. Stdout carries MCP
  JSON-RPC frames; all logs go to stderr as structured JSON.

- **Streamable HTTP transport** (`--http`) — loopback-only bind
  (`--http-addr`, default `127.0.0.1:0`); bearer auth via
  `--http-auth-token-file` (SHA-256 constant-time compare); 401 on
  missing/wrong bearer; 403 on non-loopback `Host` header (DNS-rebinding
  guard); 1 MiB request body cap; signing requests serialized via a
  semaphore of 1. Both transports expose the identical tool surface
  (ADR-002).

- **`--chain-id` guard** (optional) — rejects signing requests whose JSON
  `chainId` differs from the configured value before any key material is
  touched. `chainId = 0` always rejected (replay-unprotected).

- **`--strict-perms` flag** — refuses startup (exit 2) if any secret file
  (keystore, password, or token) is group- or world-readable. Default
  behaviour is a warning; exit 2 requires the flag.

- **Structured JSON logging** on stderr (`log/slog`); configurable via
  `--log-level` (`debug | info | warn | error`, case-insensitive,
  default `info`).

- **Per-signing audit line** (info level, successful calls only): emits
  `request_id`, `tx_hash`, `chain_id`, and `nonce`. Transaction body
  (`to`, `value`, calldata) is never logged.

- **HTTP request log** (info level, every request including 401/403):
  emits `request_id`, `remote_addr`, `status`, and `latency_ms`.

- **Rich `--version`** — prints version, commit, date, and Go version via
  `internal/obs.Build()` / `runtime/debug.ReadBuildInfo`. A VCS-tagged
  build populates all four fields; `go build` from source without a tag
  shows `(devel)`.

- **Byte-identical parity suite** — RLP output verified byte-identical
  against ethers v6.16.0 for both legacy (type 0) and EIP-1559 (type 2)
  transaction types on all committed golden vectors. `cast` (Foundry
  v1.7.1) cross-check is deferred; the deferred status is documented in
  `internal/signing/testdata/vectors/cast-version.txt`.

- **File permission checks** at startup on the keystore, password, and
  token files: warns on group- or world-readable files; `--strict-perms`
  upgrades to startup refusal (exit 2).

### Security

- **ADR-007 offline-import guard** — `internal/signing/offline_test.go`
  fails the test suite if `internal/signing` transitively imports any
  HTTP/RPC client package. Verified load-bearing by issue 4.4 mutation
  re-check: injecting `import _ "github.com/ethereum/go-ethereum/ethclient"`
  caused the test to fail naming the forbidden import (see
  `docs/verification-4.4.md` §2a).

- **ADR-008 depguard rule** — `golangci-lint` depguard configuration in
  `.golangci.yml` enforces package import edges on every `make lint` run.
  Verified load-bearing by the same 4.4 mutation: depguard failed naming
  the forbidden import via the `signing-offline-and-leaf` rule (see
  `docs/verification-4.4.md` §2a).

- **Sentinel-based leak scans** — `signing.FixtureKeySentinel` scans for
  key material in eight encoded forms (raw bytes, hex-lower, hex-upper,
  base64-std, base64-raw, base64-url, base64-rawurl, decimal) across
  every captured byte on both stdio and HTTP transports: happy path, all
  six error-code paths (`invalid_input`, `unsupported_type`,
  `chain_id_mismatch`, `keystore_error`, `password_error`,
  `internal_error`), and the `--strict-perms` refusal path. Verified
  load-bearing by issue 4.4 mutation re-check: injecting a struct with an
  exported field containing the fixture private key caused the scan to
  fail identifying the `hex-lower` form (see `docs/verification-4.4.md`
  §2b). Scans are committed tests that run under `make test` (CI-gated).

- **Sender-recovery defence** — every `sign_transaction` response
  cross-checks the recovered sender against the keystore-derived address.
  A mismatch returns `internal_error` and is logged server-side.

- **Decrypt-sign-zero per call** (ADR-003/009) — the decrypted ECDSA key
  and password bytes are zeroed via deferred callbacks after each signing
  operation. Best-effort caveat: Go may retain transient copies in
  registers or GC-managed memory; stated in the README, not hidden.

---

> **Tag scheme:** this app is released under the monorepo-prefixed tag
> `eth-signer-mcp/v1.0.0`. Go module path:
> `github.com/rootwarp/blockchain-ai-tools/apps/eth-signer-mcp`.

[v1.0.0]: https://github.com/rootwarp/blockchain-ai-tools/releases/tag/eth-signer-mcp%2Fv1.0.0
