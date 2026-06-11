// Package signing owns everything that touches key material and everything
// that determines what gets signed: secret-hygiene primitives, keystore vault,
// transaction parse/build/validate, signing orchestration, the tool-error
// taxonomy, and the per-signing audit log line.
//
// # Keystore lifecycle contract
//
// The keystore JSON and its Ethereum address are a boot-time snapshot: they
// are read eagerly at vault construction and the constructor fails fast on any
// error. A missing or empty "address" field in the keystore JSON is a startup
// keystore_error with a clear message.
//
// The password file is re-read on every signing call, so password rotation
// works without restarting the process. Rotating the keystore file itself
// requires a restart (the snapshot is not refreshed mid-run). A mid-run
// decrypt failure (wrong password or corrupt ciphertext) returns password_error.
//
// No plaintext secret is cached across calls. Every signing call re-reads the
// password, decrypts the boot-time keystore snapshot, signs, and zeroes secret
// material before returning.
//
// # Best-effort zeroing (ADR-009)
//
// Secret material — password bytes and the key scalar — is zeroed via deferred
// clear() + runtime.KeepAlive on the buffers we own, including on panic paths.
// This is best-effort: Go's runtime may retain transient copies created by GC
// moves or stack copies. Tests assert that the buffers we own are cleared; the
// limitation (transient copies beyond our control) is documented here, not hidden.
// The observable security requirement — no secrets in logs or wire outputs,
// raw or encoded — is what the leak-scan tests enforce.
//
// # Offline invariant (ADR-007)
//
// This package must never import, directly or transitively, any HTTP/RPC client
// package (net/http, net/rpc, go-ethereum ethclient/rpc). The invariant is
// mechanically enforced by offline_test.go (import-graph walk) and golangci-lint
// depguard (ADR-008). Violating it produces a red build.
//
// # Public API
//
// See the architecture document (plan/architecture.md §Package: internal/signing)
// for the complete public API contract.
package signing
