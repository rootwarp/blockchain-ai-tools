# Phase 3: HTTP Transport — Streamable HTTP, auth, hardening, resource bounds, shutdown

## Phase Overview

- **Goal:** Serve the exact same tool surface built in Phase 2 over MCP
  **Streamable HTTP** on `127.0.0.1`, hardened per ADR-006: bearer auth
  (SHA-256 both sides + constant-time compare) returning 401 **before any
  signing logic runs**, SDK DNS-rebinding protection returning 403,
  `http.MaxBytesHandler` at 1 MiB, the Phase 2 decrypt semaphore proven
  under concurrent load, HTTP request logging with `request_id`
  correlated to the per-signing audit line, graceful signal shutdown, and
  a pinned middleware pipeline order. The **concurrent-calls integration
  test is required and may not be waived** — the earlier plan revision's
  waiver of that test is revoked by ADR-006 and the project plan.
- **Phase length:** 9 issues, 17 total points.
- **Estimated duration:** ~9 working days (single-stream, one
  code-writer, sequential).
- **Packages touched:** `internal/server` (`http.go`, `auth.go`,
  `reqlog.go`, hardening + concurrency + e2e tests), `cmd/eth-signer-mcp`
  (HTTP branch wiring, token-file permission check, shutdown plumbing
  finalized). All file paths below are relative to the app module root
  `apps/eth-signer-mcp/` unless stated otherwise.
- **ADRs:** ADR-002 (second transport), ADR-006 (full — hardening +
  resource bounds).
- **Version pins:** `github.com/modelcontextprotocol/go-sdk` **v1.6.1**
  for both the server and every test client in this phase. The transport
  is always called **Streamable HTTP** (the SDK's StreamableHTTP server)
  — never "HTTP/SSE".

### Entry criteria (from the project plan)

- Phase 2 exit criteria — the parity gate — all green on `main`:
  byte-identical RLP vs `cast`/ethers v6 on every golden vector, all six
  error codes observable as `{"code","message"}` JSON in `Content[0]`,
  validation never touches the vault, zeroing + leak scans green,
  offline-import test load-bearing, the stdio e2e (task 2.11) passing,
  one audit line per successful signing carrying `request_id`/`tx_hash`/
  `chain_id`/`nonce`.
- The Phase 1 SDK spike note (task 1.7) is committed and answers: the
  `StreamableHTTPOptions` surface actually available in v1.6.1
  (DNS-rebinding/localhost protection knobs, session behavior), how
  `http.Handler` middleware composes around the SDK's
  StreamableHTTPHandler, whether a request id is exposed on
  `CallToolRequest`, and the in-memory transport pattern.
- `cmd` already parses `--http`, `--http-addr` (default `127.0.0.1:0`),
  and `--http-auth-token-file` into the `cmd`-local `config` struct, and
  cross-field validation rejects `--http` without
  `--http-auth-token-file` (Phase 1, task 1.3). The fsperm check
  machinery (`fsperm.go`, warn / `--strict-perms` refuse) exists in `cmd`
  from task 1.6.
- `signing.WithRequestID` / `signing.RequestIDFromContext` exist (task
  2.6); the decrypt semaphore of 1 with the ctx-before-KDF check exists
  in the vault (task 2.2); the light-scrypt fixture keystore exists
  (task 2.1); golden vectors exist under
  `internal/signing/testdata/vectors/` (task 2.9).

### Exit criteria (from the project plan)

- [ ] `--http` serves Streamable HTTP on `127.0.0.1` (ephemeral port
      printed); no token file → startup refusal; bad/missing bearer →
      401; rebound Host → 403; >1 MiB body rejected. All in the
      hardening matrix, green in CI.
- [ ] Pipeline-order regression test pins MaxBytes → reqlog → auth → SDK.
- [ ] Concurrent-calls integration test green (correct signatures,
      serialized decrypts, no leakage) — present and not skipped.
- [ ] Every HTTP request logs `request_id`/`remote_addr`/`status`/
      `latency_ms`; the signing audit line carries the same `request_id`.
- [ ] stdio/HTTP parity test green (identical schemas + results).
- [ ] SIGINT/SIGTERM drains and exits cleanly on both transports;
      stdio EOF exits 0.
- [ ] Leak scan (raw + encoded forms) green over all HTTP-path logs.

## Phase Summary

| Issue | Title | Points | ~Days | Blocked by |
|-------|-------|--------|-------|------------|
| 3.1 | Streamable HTTP server: `RunHTTP`, localhost bind, token-file startup | 2 | 1.5 | Phase 2 exit |
| 3.2 | Bearer auth middleware (SHA-256 + constant-time) | 2 | 1.0 | 3.1 |
| 3.3 | Request-id + HTTP request-logging middleware | 2 | 1.0 | 3.1 |
| 3.4 | Resource bounds: `MaxBytesHandler` 1 MiB; semaphore under HTTP | 2 | 1.0 | 3.1 |
| 3.5 | Hardening matrix tests (bind / 403 / 401 / pipeline order) | 3 | 1.5 | 3.2, 3.3, 3.4 |
| 3.6 | Concurrent-calls integration test (REQUIRED) | 2 | 1.0 | 3.4 |
| 3.7 | Signal shutdown: drain on SIGINT/SIGTERM; stdio EOF | 1 | 0.5 | 3.1 |
| 3.8 | HTTP e2e + stdio/HTTP parity test | 2 | 1.0 | 3.5 |
| 3.9 | Phase polish pass | 1 | 0.5 | 3.1–3.8 |

Total: 9 issues, 17 points, 9.0 task days.

## Phase Execution Plan

Single-stream, sequential; under-budget issues roll the next one forward
within the same day.

| Day | Work | Notes |
|-----|------|-------|
| 1 | 3.1 | `RunHTTP`: bind, token-file startup check, SDK handler wiring, bound-address announce. |
| 2 | 3.1 finish; 3.2 start | Smoke test + shutdown skeleton; start `BearerVerifier`. |
| 3 | 3.2 finish; 3.3 start | 401 middleware + token-file perms in `cmd`; start request-id/reqlog middleware. |
| 4 | 3.3 finish; 3.4 start | Audit-line correlation test; `MaxBytesHandler` + semaphore verification under HTTP. |
| 5 | 3.4 finish; 3.5 start | Bounds composition tests; begin hardening matrix. |
| 6 | 3.5 finish | Pipeline-order regression assertions pinned to SDK v1.6.1. |
| 7 | 3.6 | Concurrent-calls integration test (instrumented vault, memory bound, `-race`). |
| 8 | 3.7; 3.8 start | Signal-shutdown tests on both transports; start HTTP e2e harness. |
| 9 | 3.8 finish; 3.9 | Transport parity assertions; phase polish pass + exit review. |

---

## Issues

### Issue 3.1: Streamable HTTP server: `RunHTTP`, localhost bind, token-file startup

- **Points:** 2
- **Type:** feature
- **Priority:** P0
- **Blocked by:** Phase 2 exit criteria; spike note from task 1.7
  (StreamableHTTPOptions surface, middleware composition)
- **Blocks:** 3.2, 3.3, 3.4, 3.7
- **Scope:** 1.5 days

**Description:**

Implement `(*Server).RunHTTP(ctx, HTTPOptions)` in `internal/server` —
the second transport over the same `*mcp.Server` built in `server.New`
(ADR-002: one server, two transports; tools are already registered
once). `RunHTTP` reads the bearer-token file at startup (unreadable or
empty → startup error returned **before the listener binds**; token
contents never logged), binds `Addr` (default `127.0.0.1:0`), prints the
resolved bound `host:port` to stderr (P0-CLI-3), wraps the SDK's
StreamableHTTPHandler with DNS-rebinding/localhost protection **on** per
the 1.7 spike note's recorded option names, and serves until `ctx`
cancels. `cmd` routes `--http` to `RunHTTP` and exits non-zero on
startup error with a sanitized one-line message.

The auth, request-logging, and body-cap layers land in 3.2–3.4; this
issue establishes the server, the option surface, the bind/announce
behavior, and the fail-fast token-file startup contract. Full pipeline
assembly order is `http.MaxBytesHandler` → request-id/logging → bearer
auth → SDK handler (architecture, Flow B); build `RunHTTP` so each
middleware slots in as a `func(http.Handler) http.Handler` without
re-plumbing.

**Implementation Notes:**

- Files:
  - New: `internal/server/http.go` — `HTTPOptions{Addr, TokenFilePath}`,
    `RunHTTP`, listener bind + announce, `http.Server` construction,
    shutdown skeleton (completed in 3.7).
  - Edit: `internal/server/server.go` — only if `RunHTTP` needs access
    to shared server state; no change to tool registration.
  - Edit: `cmd/eth-signer-mcp/main.go` — route `cfg.HTTP` to
    `RunHTTP(ctx, HTTPOptions{Addr: cfg.HTTPAddr, TokenFilePath:
    cfg.TokenFilePath})`; startup error → sanitized stderr line,
    non-zero exit.
  - New: `internal/server/http_test.go` — bind/announce/startup-failure
    tests (extended by 3.5).
- Approach:
  1. Validate the token file first: `os.ReadFile`, strip exactly one
     trailing `\n`, empty after strip → error. Hand the bytes to the
     3.2 verifier constructor once it exists; until then hold the
     SHA-256 wrapped in `signing.Secret` and zero the raw bytes via
     `signing.ZeroBytes`. **The listener must not bind if this step
     fails** — operators get a clean failure, not a half-broken
     listening process.
  2. `ln, err := net.Listen("tcp", opts.Addr)`; on success print
     `eth-signer-mcp listening on <ln.Addr()>` to stderr (and log it).
     With the default `127.0.0.1:0` the printed line carries the
     ephemeral port; tests and the 3.8 e2e harness parse this line.
  3. Construct the SDK handler per the spike note:
     `mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server {
     return s.mcp }, &mcp.StreamableHTTPOptions{...})` with
     DNS-rebinding/localhost protection left **enabled** (the v1.6.1
     default; do not set any knob that disables it). Record in a code
     comment which fields are deliberately zero-valued, citing the 1.7
     note.
  4. `&http.Server{Handler: pipeline, ReadHeaderTimeout: 5 *
     time.Second}` — the only non-default `http.Server` knob; defensive
     against slow-header stalls even on loopback.
  5. Serve in a goroutine; on `ctx.Done()` call `Shutdown` with a grace
     period (skeleton here; drain semantics tested in 3.7).
- Watch out for:
  - **Bound-address verification:** tests must assert on
    `ln.Addr().(*net.TCPAddr).IP.IsLoopback()` — not just the printed
    string — so a mis-bind is caught structurally (the full matrix
    assertion is 3.5(a); the unit test here covers the default).
  - Announce only **after** `Listen` succeeds; bind failure must return
    an error without printing the line.
  - Never log or echo token-file contents in any error path (sanitized:
    path + error class only).

**Acceptance Criteria:**

- [x] `RunHTTP(ctx, opts)` with a valid token file binds
      `127.0.0.1:0` by default, prints `listening on 127.0.0.1:<port>`
      to stderr after `Listen` succeeds, and serves MCP Streamable HTTP
      until `ctx` cancels.
- [x] The bound listener address is loopback — asserted via the
      listener's resolved `*net.TCPAddr`, not by string parsing.
- [x] Missing, unreadable, or empty (after one-`\n` strip) token file →
      `RunHTTP` returns an error **before any listener is bound**
      (asserted: no port opened); `cmd` exits non-zero with a sanitized
      one-line stderr message naming the path, never its contents.
- [x] `--http-addr` override is honored; bind failure (address in use)
      returns an error without printing the announce line.
- [x] The SDK handler is constructed with DNS-rebinding/localhost
      protection enabled; the deliberate zero-value option fields are
      documented in a comment citing the 1.7 spike note.
- [x] `http.Server.ReadHeaderTimeout` is set (5 s).
- [x] A smoke test drives one `initialize` round-trip over real
      Streamable HTTP with the SDK v1.6.1 test client (auth not yet
      enforced in this issue's pipeline; the test is updated in 3.2).
- [x] `go test -race ./internal/server/...` green; depguard green
      (`server` imports only `signing` + `obs` internally).

**Testing Notes:**

- Use `Addr: "127.0.0.1:0"` everywhere in tests; read the port from the
  returned/announced address, never hardcode.
- Gate the first client request on the announce signal (channel or
  captured writer), not on sleeps.
- Pin the SDK test client to v1.6.1 — same module version as the server.

---

### Issue 3.2: Bearer auth middleware (SHA-256 + constant-time)

- **Points:** 2
- **Type:** feature
- **Priority:** P0
- **Blocked by:** 3.1
- **Blocks:** 3.5
- **Scope:** 1.0 day

**Description:**

Implement `internal/server/auth.go`: `NewBearerVerifierFromFile(path)`
reads the token file, strips exactly one trailing `\n`, rejects an empty
token, stores `sha256(expected)` wrapped in `signing.Secret` (even the
hash stays out of logs), and zeroes the raw token bytes before
returning. `(*BearerVerifier).Middleware(next)` extracts the
`Authorization: Bearer <token>` header, hashes the supplied token with
SHA-256, compares with `subtle.ConstantTimeCompare` — hashing both sides
first neutralizes the length-leak short-circuit — and on any failure
writes **401 before the SDK handler sees the body and before any signing
logic can run**. `RunHTTP` (3.1) slots this middleware inside the
request-logging layer per the pinned pipeline order.

This issue also lands the **bearer-token file permission check in
`cmd`**: extend the task 1.6 `checkPerms` call to cover
`cfg.TokenFilePath` whenever `--http` is set, with the exact same
semantics as the keystore and password files — world-/group-readable →
warn by default, refuse (exit 2) with `--strict-perms`. It ships here,
with the token wiring, so the check exists from the first commit where
the token file is security-load-bearing.

**Implementation Notes:**

- Files:
  - New: `internal/server/auth.go` — `BearerVerifier`,
    `NewBearerVerifierFromFile`, `Middleware`.
  - New: `internal/server/auth_test.go`.
  - Edit: `internal/server/http.go` — wire the verifier into the
    pipeline; the 3.1 token-file pre-read becomes the verifier
    constructor call (single read path, still before bind).
  - Edit: `cmd/eth-signer-mcp/fsperm.go` / `main.go` — include the
    token path in the startup permission check when `cfg.HTTP`.
  - Edit: `cmd/eth-signer-mcp/main_test.go` — warn/refuse cases for the
    token file.
- Approach:
  1. Constructor: read file → strip one `\n` (do **not** `TrimSpace`;
     tokens may legitimately contain inner spaces) → empty → error →
     `sha256.Sum256` → wrap in `signing.Secret[[32]byte]` →
     `signing.ZeroBytes(raw)`.
  2. Middleware: `strings.CutPrefix(header, "Bearer ")`
     (case-sensitive per RFC 6750); missing header, wrong scheme,
     prefix-only, wrong-case `bearer`, or hash mismatch → set
     `WWW-Authenticate: Bearer`, `WriteHeader(401)`, return — `next`
     never invoked, no response body. Success → `next.ServeHTTP`.
  3. 401 responses carry no body and nothing derived from the token
     file; misconfiguration is reported via structured logs only.
- Watch out for:
  - The middleware sees every request, including SDK
    session-establishment requests — the 401 short-circuit must not
    create any MCP transport/session state (automatic: `next` is the
    SDK handler and is never called).
  - Headers must be set before `WriteHeader`.
  - No timing assertions in tests — the structural property (SHA-256
    both sides + `ConstantTimeCompare`) is the contract; timing tests
    are flaky and prove nothing.

**Acceptance Criteria:**

- [x] `NewBearerVerifierFromFile`: valid file → verifier holding only
      the SHA-256 of the token inside `signing.Secret`; raw token bytes
      zeroed before return; empty/missing/unreadable file → error.
- [x] Correct `Authorization: Bearer <token>` → request reaches `next`.
- [x] Missing header, `Bearer` with empty token, non-Bearer scheme,
      lowercase `bearer `, and wrong tokens of assorted lengths (1, 16,
      32, 64, 128 bytes) → 401, empty body, `WWW-Authenticate: Bearer`
      set, and `next` **never invoked** (recording stub fails the test
      if hit) — i.e. 401 fires before any signing logic.
- [x] The compare path is `sha256(supplied)` vs stored
      `sha256(expected)` via `subtle.ConstantTimeCompare` — no raw-byte
      or `==` compare anywhere (code-review item, named here so the
      reviewer checks it).
- [x] Leak scan: with the leak-scan sentinel used as the token, no
      captured log line at any level contains the token or its encoded
      forms (Sentinel from task 1.5, encoded forms registered).
- [x] `cmd`: world-/group-readable token file + `--http` → warning by
      default; with `--strict-perms` → refusal, exit 2 — identical
      semantics and shared code path with the keystore/password checks;
      both paths covered in `main_test.go` via temp files + chmod.
- [x] `go test -race ./internal/server/... ./cmd/...` green.

**Testing Notes:**

- `t.TempDir()` + `os.WriteFile` for token fixtures; rotate a random
  token per test run.
- Use `httptest.NewRecorder` for unit-level middleware tests; the
  full-pipeline behavior is locked in by 3.5.
- Register the bearer sentinel's encoded forms with the leak scan so
  hex/base64 renderings of the token are also caught.

---

### Issue 3.3: Request-id + HTTP request-logging middleware

- **Points:** 2
- **Type:** feature
- **Priority:** P0
- **Blocked by:** 3.1
- **Blocks:** 3.5
- **Scope:** 1.0 day

**Description:**

Implement `internal/server/reqlog.go`: middleware that generates the
`request_id` (the SDK-provided id if the 1.7 spike found one exposed on
the request; otherwise a UUIDv4), attaches it to the request context via
`signing.WithRequestID`, and logs exactly one structured line per HTTP
request on completion: `request_id`, `remote_addr`, `status`,
`latency_ms`. The same `request_id` must appear in the Phase 2
per-signing audit line (task 2.6: `request_id`, `tx_hash`, `chain_id`,
`nonce`) — proven by a correlation test. Bodies and headers are never
logged; the leak scan runs over all captured HTTP logs.

This middleware sits **outside** bearer auth in the pipeline, so even
rejected (401) requests are request-logged — that property is pinned by
the 3.5 pipeline-order regression test.

**Implementation Notes:**

- Files:
  - New: `internal/server/reqlog.go` — request-id generation,
    status-capturing `ResponseWriter` wrapper, logging middleware.
  - New: `internal/server/reqlog_test.go`.
  - Edit: `internal/server/http.go` — slot reqlog between
    `MaxBytesHandler` (3.4) and bearer auth (3.2).
  - Edit (if needed): `internal/server/handlers.go` — ensure tool
    handlers propagate `request_id` from the HTTP middleware context
    when present, generating one themselves only on the stdio path
    (Phase 2 behavior, task 2.7, unchanged).
- Approach:
  1. Wrap `http.ResponseWriter` to capture the status code (default 200
     if a handler writes a body without `WriteHeader`).
  2. `ctx = signing.WithRequestID(r.Context(), id)`;
     `next.ServeHTTP(w, r.WithContext(ctx))`; on return emit one
     info-level line via the injected `*slog.Logger`.
  3. Log fields exactly: `request_id`, `remote_addr`, `status`,
     `latency_ms` (plus standard `ts`/`level`/`msg` from `obs`). No
     URL query echo, no headers, no body bytes.
- Watch out for:
  - One line per request, emitted on completion — not one on entry plus
    one on exit.
  - Streamable HTTP responses may stream; latency is measured to
    handler return, which is fine for this tool's request shapes —
    note it in a comment.
  - Do not add a logging dependency: stdlib `slog` only, injected from
    `cmd` via `server.Options`.

**Acceptance Criteria:**

- [x] Every HTTP request — including 401s and 403s — produces exactly
      one log line with `request_id`, `remote_addr`, `status`,
      `latency_ms`; asserted by parsing captured JSON stderr.
- [x] `request_id` is propagated via `signing.WithRequestID`; a
      successful `sign_transaction` over HTTP yields an audit line
      (task 2.6) whose `request_id` equals the HTTP request log line's
      `request_id` — the correlation test.
- [x] When the SDK exposes no request id (per the 1.7 spike note's
      finding), ids are UUIDv4 and unique across concurrent requests.
- [x] No request body bytes, no `Authorization` header value, and no
      other header values appear in any captured log at any level —
      leak scan (raw + encoded forms) green over captured HTTP logs.
- [x] Status capture is correct for: handler 200, middleware-written
      401, SDK-written 403, and a handler that never calls
      `WriteHeader`.
- [x] `go test -race ./internal/server/...` green.

**Testing Notes:**

- Capture logs by constructing the logger over a `bytes.Buffer` (same
  pattern as the `obs` leak-scan tests); parse lines as JSON.
- The correlation test can run over the in-memory pipeline with the
  real signer + light-scrypt fixture (task 2.1) to get a real audit
  line, or stub the signer and assert the context id — prefer the real
  signer so the test also covers the handler context plumbing.

---

### Issue 3.4: Resource bounds: `MaxBytesHandler` 1 MiB; semaphore under HTTP

- **Points:** 2
- **Type:** feature
- **Priority:** P0
- **Blocked by:** 3.1
- **Blocks:** 3.5, 3.6
- **Scope:** 1.0 day

**Description:**

Make ADR-006's resource bounds real on the HTTP path. Wrap the entire
pipeline in `http.MaxBytesHandler(pipeline, 1<<20)` as the outermost
layer: an oversized body is rejected without reaching the SDK handler —
and therefore without touching auth state, the signer, or the vault.
Verify the bound composes sanely with the Phase 2 schema-level `data`
cap (256 KiB of bytes / 512 KiB hex chars, task 2.4): a maximal-`data`
valid request fits under 1 MiB; an over-cap `data` inside an under-1 MiB
body still fails schema validation with `invalid_input`.

Then verify the **decrypt semaphore is wired through the HTTP path**:
the Phase 2 vault semaphore of 1 (task 2.2) is the **only** signing
concurrency gate under HTTP — no extra pooling, no per-transport
serialization — and the **per-request ctx (deadline/cancellation) is
checked before scrypt starts**, so a request cancelled while queued on
the semaphore never pays the KDF. The full N-way concurrent proof is
3.6; this issue proves the plumbing (HTTP request ctx reaches the vault
intact) with a two-request test.

**Implementation Notes:**

- Files:
  - Edit: `internal/server/http.go` — `MaxBytesHandler` as the
    outermost wrapper; pipeline assembly finalized: MaxBytes → reqlog →
    auth → SDK handler.
  - New: `internal/server/bounds_test.go` — body-cap tests, cap
    composition tests, ctx-propagation + semaphore plumbing tests.
- Approach:
  1. `http.MaxBytesHandler` sets `r.Body = http.MaxBytesReader(...)`;
     the SDK handler's JSON decode of an oversized body surfaces as a
     request error — assert the client-visible rejection (413 or the
     SDK's decode failure; pin whichever v1.6.1 produces, with a
     comment) and assert via a recording seam that the MCP handler
     never received a complete request.
  2. Cap composition: build a `sign_transaction` request with `data` at
     exactly 256 KiB of bytes (512 KiB hex + `0x`) — total body
     comfortably < 1 MiB → passes the body cap, signs successfully
     (light fixture). One byte of `data` over the cap → `invalid_input`
     from schema/validation, not a body-cap rejection.
  3. Ctx + semaphore plumbing: instrument the vault (test seam or
     wrapper `KeyVault` recording `WithSigningKey` entry/exit). Request
     A holds the semaphore (slow `fn` via the wrapper); request B
     arrives over HTTP and is cancelled client-side while queued →
     B returns without the KDF starting (vault wrapper asserts
     `WithSigningKey` for B observed `ctx.Err()` before decrypt);
     A completes normally.
- Watch out for:
  - `MaxBytesHandler` must be outermost — an oversized body must be
    rejected even with a bad token (that ordering is pinned in 3.5; the
    wiring happens here).
  - Do not add any new semaphore/pool in `server` — the vault owns
    concurrency (architecture: "no shared mutable signing state").
  - The wrapper-vault test seam must live in `_test.go` files; no
    production test hooks.

**Acceptance Criteria:**

- [ ] A >1 MiB request body is rejected without the SDK handler
      receiving a complete request; the rejection status/behavior
      observed under SDK v1.6.1 is asserted and documented in the test.
- [ ] A valid request with `data` at the 256 KiB-bytes cap passes the
      body cap and signs; `data` over the cap but body under 1 MiB →
      `invalid_input` (schema/validation layer), vault never invoked.
- [ ] The HTTP request context reaches `vault.WithSigningKey`
      unmodified: a client-cancelled queued request returns with
      `ctx.Err()` **before scrypt starts** (instrumented vault
      assertion, not wall-clock).
- [ ] Exactly one concurrency gate exists on the signing path: the
      Phase 2 vault semaphore — asserted by code inspection note in the
      PR plus the instrumented two-request test (A in-flight, B queued,
      never concurrent).
- [ ] Pipeline assembly in `http.go` reads, outermost first:
      `MaxBytesHandler` → reqlog → bearer auth → SDK handler.
- [ ] `go test -race ./internal/server/...` green.

**Testing Notes:**

- Generate the >1 MiB body as a syntactically valid JSON-RPC frame with
  an oversized padding field so the rejection is attributable to the
  byte cap, not earlier JSON syntax failure.
- Use the light-scrypt fixture for any real decrypt; the instrumented
  wrapper-vault keeps semaphore tests fast and deterministic.

---

### Issue 3.5: Hardening matrix tests (bind / 403 / 401 / pipeline order)

- **Points:** 3
- **Type:** test
- **Priority:** P0
- **Blocked by:** 3.2, 3.3, 3.4
- **Blocks:** 3.8
- **Scope:** 1.5 days

**Description:**

The production-equivalence test matrix for ADR-006: **each hardening
layer asserted independently**, then the **pipeline order pinned by
regression assertions** against the behavior observed in SDK v1.6.1.
Layers: (a) the listener is actually bound to a loopback address —
asserted on the resolved bound address, not the flag value; (b) a
forged/rebound `Host` header → 403 from the SDK's DNS-rebinding
protection, even with a valid bearer; (c) missing/wrong bearer → 401
before the SDK handler runs; (d) oversized body rejected (3.4's layer,
re-asserted inside the matrix so the matrix is complete on its own).

The pipeline-order regression test pins MaxBytes → reqlog → auth → SDK
with **both-fail** cases: an oversized body **with a bad token** must
fail on size (the body cap wins — proving auth never buffers the body);
an unauthorized request must still be **request-logged with status
401** (reqlog outside auth); a request with a bad token **and** a
rebound `Host` returns 401 (our auth wraps the SDK handler, so auth
wins over the SDK's 403 in this composition) — each assertion's failure
message points at the 1.7 spike note so a future SDK bump that changes
v1.6.1's observed behavior fails loudly with the right diagnostic.

**Implementation Notes:**

- Files:
  - New: `internal/server/hardening_test.go` — the full matrix +
    pipeline-order regression tests.
  - Read-only: the 1.7 spike note (option/ordering findings),
    `internal/server/http.go`, `auth.go`, `reqlog.go`.
- Approach:
  1. Drive the **real** `RunHTTP` listener (`127.0.0.1:0`), not
     `httptest.NewServer` — the DNS-rebinding check must see a real
     `Host` header against a real bound address, and the bind assertion
     needs the production `net.Listen` path.
  2. (a) Bind: resolve the announced address; assert
     `IP.IsLoopback()`; additionally assert a non-loopback `--http-addr`
     is possible only when explicitly configured (the default never
     leaves loopback).
  3. (b) Rebind 403: valid bearer, `req.Host = "evil.example.com"` →
     403; status-only assertion (SDK body content is not our contract).
  4. (c) 401: no header / malformed / wrong token → 401; a recording
     seam (stub inner handler or instrumented panicking vault) proves
     neither the SDK handler nor any signing logic ran.
  5. (d) Both-fail order pins: oversized-body+bad-token → size
     rejection, auth never consulted; wrong-token request appears in
     the captured reqlog with `status=401`; bad-token+rebound-Host →
     401 (auth outside SDK). Each with a failure message citing the
     spike note path.
  6. One fresh `RunHTTP` instance per sub-test where state matters; all
     sessions closed cleanly so no cross-test bleed.
- Watch out for:
  - If v1.6.1's observed both-fail behavior differs from the spike
    note's prediction, update the note **and** the assertion together
    in this issue — the test pins reality, not the prediction.
  - No timing assertions anywhere in the matrix.

**Acceptance Criteria:**

- [ ] Bind layer: the default configuration's resolved listener address
      is loopback, asserted structurally on `*net.TCPAddr`.
- [ ] Rebind layer: `Host: evil.example.com` with a **valid** bearer →
      403 from the SDK handler.
- [ ] Auth layer: missing / malformed / wrong bearer → 401; the SDK
      handler and all signing logic provably never ran (recording
      seam).
- [ ] Body-cap layer: >1 MiB body rejected inside the matrix.
- [ ] Pipeline-order regression: (i) oversized body + bad token fails
      on size, auth untouched; (ii) 401 requests are request-logged
      with `status=401`; (iii) bad token + rebound Host → 401. All
      three pinned to SDK v1.6.1 observed behavior with failure
      messages pointing at the 1.7 spike note.
- [ ] Every sub-test runs against the real `RunHTTP` pipeline with the
      SDK v1.6.1 server; matrix is flake-free across 10 consecutive
      local `-race` runs (development smoke, not a CI loop).
- [ ] Leak scan green over all logs captured during the matrix
      (including 401/403 paths).
- [ ] `go test -race ./internal/server/...` green; matrix runs in CI.

**Testing Notes:**

- Set the forged host via `req.Host` on `*http.Request` (Go sends it as
  the `Host` header).
- For "signing logic never ran", wire the panicking fake vault pattern
  from task 2.6 behind the real handlers — a panic would fail the test
  loudly.
- Keep each layer's test independent: one layer's failure must not
  cascade into the others' diagnostics.

---

### Issue 3.6: Concurrent-calls integration test (REQUIRED)

- **Points:** 2
- **Type:** test
- **Priority:** P0
- **Blocked by:** 3.4
- **Blocks:** 3.9
- **Scope:** 1.0 day

**Description:**

ADR-006's named acceptance test, **required and non-waivable** — the
earlier plan revision waived concurrent-call coverage on the grounds
that per-call independence was already proven; that waiver is revoked.
N concurrent `tools/call sign_transaction` requests (N ≥ 8) over **real
Streamable HTTP** against the light-scrypt fixture must show:

1. **All N succeed with correct signatures** — every returned
   `rawTransaction` is independently verified (decode via
   `UnmarshalBinary`, recovered sender == fixture address, and for
   vector-matching inputs byte-equality against the task 2.9 goldens).
2. **Decrypts are observably serialized** — an instrumented vault
   records entry/exit of the decrypt section; max observed concurrency
   is exactly 1. Serialization is asserted on instrumentation ordering,
   never wall-clock sleeps (flake-proof by design).
3. **Memory stays bounded** — the instrumented max-concurrent-decrypts
   == 1 is the structural memory bound (at most one KDF allocation
   alive); a `runtime.ReadMemStats` sanity check asserts heap growth
   during the burst stays under a generous fixed bound for the light
   fixture.
4. **No race detector findings** — the whole test runs under `-race`
   in CI and must be clean.
5. **No cross-call state bleed** — distinct nonces/inputs per request;
   each response matches its own request (request/response pairing
   asserted); all N `request_id`s distinct in the HTTP log and each
   audit line correlated to its request.
6. **Leak scan green** over the full captured output of the burst
   (logs + responses, raw + encoded forms).

**Implementation Notes:**

- Files:
  - New: `internal/server/concurrent_test.go` — the integration test +
    instrumented-vault wrapper helper (test-only).
- Approach:
  1. Stand up `RunHTTP` with the real signer over a wrapper `KeyVault`
     that delegates to the real `FileKeyVault` (light fixture) while
     counting concurrent `WithSigningKey` bodies with an atomic
     gauge + max tracker.
  2. Fire N goroutines, each an SDK v1.6.1 client session issuing one
     `sign_transaction` with a unique nonce; collect all results.
  3. Assert (1)–(6). For pairing, derive the expected `hash`/`from` per
     input and match against each response.
  4. This test is a CI gate: it must not be guarded by
     `testing.Short()`, build tags, or `t.Skip` under any condition.
- Watch out for:
  - Light fixture only (~50 ms/KDF) keeps N×serialized runtime ~1 s.
  - The memory sanity bound must be generous (e.g. tens of MiB) — it
    guards against unbounded parallel KDF allocation, not allocator
    noise.
  - Sessions must be closed cleanly so the burst leaves no stale SDK
    session state.

**Acceptance Criteria:**

- [ ] N ≥ 8 parallel `sign_transaction` calls over real Streamable
      HTTP all succeed; every signature independently verified
      (decode, recovered sender == fixture address; golden
      byte-equality where the input matches a committed vector).
- [ ] Instrumented vault proves max concurrent decrypts == 1 —
      serialization asserted on instrumentation, not timing.
- [ ] Memory bounded: structural bound via the ==1 gauge plus a
      `ReadMemStats` sanity assertion on heap growth during the burst.
- [ ] Test runs under `go test -race` in CI with zero race findings.
- [ ] No cross-call bleed: responses pair with their requests; N
      distinct `request_id`s in HTTP logs; each success has its own
      correlated audit line.
- [ ] Leak scan (raw + encoded forms) green over all captured logs and
      response bytes from the burst.
- [ ] The test is present, unconditioned, and green in CI — no skip
      path exists.

**Testing Notes:**

- Use `errgroup` or a `sync.WaitGroup` + result channel; seed each
  request from a table of distinct inputs.
- The instrumented wrapper lives in `_test.go`; production `server`
  code gains no test hooks.
- If flakes appear, fix by strengthening instrumentation-based
  ordering — never by adding sleeps or loosening assertions, and never
  by skipping.

---

### Issue 3.7: Signal shutdown: drain on SIGINT/SIGTERM; stdio EOF

- **Points:** 1
- **Type:** feature
- **Priority:** P0
- **Blocked by:** 3.1
- **Blocks:** 3.8
- **Scope:** 0.5 day

**Description:**

Finalize graceful shutdown on both transports. `cmd`'s
`signal.NotifyContext(SIGINT, SIGTERM)` (wired since task 1.8) cancels
the root ctx; on cancellation the HTTP server **stops accepting new
requests and drains in-flight ones** via `http.Server.Shutdown` with a
grace timeout (3 s), then exits 0. In-flight signing calls complete
inside the grace window and their **key material is zeroed by the
vault's deferred zeroing as usual** — scrypt is non-cancellable, so the
worst case is one KDF completing after cancel with its output discarded
and zeroed (task 2.2 semantics); a request still **queued** on the
semaphore at cancel observes `ctx.Err()` and never starts the KDF. No
in-flight secret material survives shutdown beyond the vault's existing
zeroing guarantees — nothing new to zero exists at the transport layer
(asserted by review note: `server` holds no key material, only the
token hash inside `signing.Secret`). stdio exits 0 on clean EOF.

**Implementation Notes:**

- Files:
  - Edit: `internal/server/http.go` — complete the 3.1 shutdown
    skeleton: `<-ctx.Done()` → `srv.Shutdown(graceCtx)` (3 s) →
    return nil on clean drain; `Shutdown` error propagated.
  - Edit: `cmd/eth-signer-mcp/main.go` — exit 0 on clean shutdown
    (both transports), non-zero on transport error; no parallel cancel
    path beside the signal context.
  - New: `internal/server/shutdown_test.go`; edit
    `cmd/eth-signer-mcp/main_test.go` for the subprocess signal test.
- Approach:
  1. In-process test: start `RunHTTP`, put one slow signing call
     in-flight (instrumented vault), cancel ctx → new request refused,
     in-flight call completes and its response is delivered, `RunHTTP`
     returns nil within the grace window; vault zeroing ran (Phase 2
     zeroing test hooks).
  2. Semaphore-waiter-on-cancel: a queued request at cancel time gets
     `ctx.Err()` without starting the KDF (shares the 3.4 instrumented
     seam).
  3. Subprocess tests: spawn the real binary (`--http`, fixtures, token
     file), wait for the announce line, send SIGTERM → exit code 0
     within 5 s (3 s grace + buffer); repeat with SIGINT. Stdio: spawn,
     `initialize`, close stdin → exit 0.
- Watch out for:
  - `t.Cleanup(cmd.Process.Kill)` so a failed assertion never leaves a
    listener or zombie behind.
  - POSIX-only signal tests are fine (CI's Windows job is compile-only,
    task 1.2).

**Acceptance Criteria:**

- [ ] SIGTERM and SIGINT each cause the real HTTP binary to stop
      accepting, drain, and exit 0 within 5 s (subprocess test, both
      signals).
- [ ] In-process: cancellation with one in-flight signing call → the
      call completes and responds, new requests are refused, `RunHTTP`
      returns within the 3 s grace, and the vault's deferred zeroing
      ran for the in-flight call.
- [ ] A request queued on the decrypt semaphore at cancel returns
      `ctx.Err()` before scrypt starts.
- [ ] stdio: clean EOF on stdin → process exits 0 (subprocess test).
- [ ] No goroutine leaks across the shutdown tests (`-race` runs
      clean; goleak only if already a dependency — do not add one).
- [ ] Review note recorded in the PR: `internal/server` holds no key
      material at shutdown; the only secret-adjacent state is the
      token hash inside `signing.Secret`.

**Testing Notes:**

- Use `os/exec` with captured stderr; gate on the announce line before
  signaling.
- Reuse the Phase 2 zeroing assertion helpers; this issue adds the
  shutdown-timing dimension, not new zeroing machinery.

---

### Issue 3.8: HTTP e2e + stdio/HTTP parity test

- **Points:** 2
- **Type:** test
- **Priority:** P0
- **Blocked by:** 3.5 (hardened pipeline final), 3.7 (shutdown wiring
  exercised by the harness teardown)
- **Blocks:** 3.9
- **Scope:** 1.0 day

**Description:**

Full-surface end-to-end test over real Streamable HTTP, plus the
transport-parity proof of ADR-002. The harness builds the binary,
launches it with `--http` + token file + fixture keystore, scrapes the
bound port from the announce line, and connects with the SDK v1.6.1
client through a bearer-injecting `http.RoundTripper`. It then runs:
`initialize` → `tools/list` (both tools, strict schemas) →
`get_address` (checksummed fixture address) → `sign_transaction` happy
path → one error path per code asserted by JSON-parsing `Content[0]`
(ADR-004 wire contract, same assertions as the stdio e2e, task 2.11).

**Parity:** using the **same golden fixture inputs** as the stdio e2e
(task 2.11; vectors from task 2.9), assert the `sign_transaction`
output is **byte-identical across transports** — `rawTransaction`,
`signature{r,s,v}`, `hash`, `from` all equal between the stdio run and
the HTTP run (and equal to the committed golden) — and that the
`tools/list` schemas are deep-equal across stdio and HTTP. Run at least
one legacy and one EIP-1559 vector. Captured stderr from both runs
passes the leak scan and contains correlated reqlog + audit lines on
the HTTP side.

**Implementation Notes:**

- Files:
  - New: `internal/server/http_e2e_test.go` — harness + e2e sub-tests.
  - New: `internal/server/parity_transport_test.go` — stdio-vs-HTTP
    deep-equal assertions (may share the harness helpers).
  - Read-only: `internal/signing/testdata/vectors/` (task 2.9),
    fixtures (task 2.1).
- Approach:
  1. `TestMain`/helper builds `./cmd/eth-signer-mcp` into a temp dir
     with `go build` (self-contained; no `make` dependency); launch
     helpers spawn it in HTTP or stdio mode.
  2. `bearerRoundTripper{base, token}` clones each request and sets
     `Authorization`; plug into the SDK client's HTTP transport.
  3. Port scrape: parse the `listening on 127.0.0.1:<port>` stderr line
     within a 5 s deadline (retry loop, no sleeps in the happy path).
  4. Parity: drive the same vector input through (a) the stdio binary
     (the task 2.11 client pattern) and (b) the HTTP binary; deep-equal
     the two `SignResult` payloads and byte-compare both against the
     golden `rawTransaction`. Deep-equal the two `tools/list` schema
     documents.
  5. Teardown via context-cancel/SIGTERM (3.7 wiring) — clean exits
     double as a shutdown regression check.
- Watch out for:
  - Resolve fixture/vector paths from `runtime.Caller(0)` walked to the
    module root, not `os.Getwd()`.
  - Rotate a fresh random token per run; never commit a token fixture.
  - The error-path sub-tests must JSON-parse `Content[0]` — never
    substring-match — same discipline as 2.11.

**Acceptance Criteria:**

- [ ] HTTP e2e green against the real binary over Streamable HTTP with
      SDK v1.6.1 client + bearer round-tripper: `initialize`,
      `tools/list` (exactly `sign_transaction` + `get_address`, strict
      schemas), `get_address` (EIP-55 fixture address),
      `sign_transaction` happy path.
- [ ] One error path per stable code (`invalid_input`,
      `unsupported_type`, `chain_id_mismatch`, `keystore_error` or
      `password_error` as reachable, `internal_error` via seam if
      practical) asserted over HTTP by JSON-parsing `Content[0]`.
- [ ] Parity: for at least one legacy and one EIP-1559 golden vector,
      the stdio and HTTP `SignResult`s are byte-identical to each other
      and to the committed golden (`rawTransaction`, `r`, `s`, `v`,
      `hash`, `from`).
- [ ] Parity: `tools/list` schema documents are deep-equal across
      stdio and HTTP.
- [ ] HTTP-side stderr shows a reqlog line and an audit line sharing
      one `request_id` per signing; all captured output (both
      transports) passes the leak scan, raw + encoded forms.
- [ ] Harness leaves no zombie processes on pass or fail
      (`t.Cleanup` kill); teardown exits are clean (exit 0).
- [ ] Green in CI under `-race`; no external network access; no
      Foundry/Node invocation.

**Testing Notes:**

- The golden vectors are the single source of expected outputs — the
  parity test must not re-derive expectations with go-ethereum calls.
- Keep harness helpers in `_test.go` files within `internal/server`;
  no new packages, no exported test utilities.

---

### Issue 3.9: Phase polish pass

- **Points:** 1
- **Type:** chore
- **Priority:** P0
- **Blocked by:** 3.1–3.8
- **Blocks:** Phase 4 entry
- **Scope:** 0.5 day

**Description:**

The in-phase polish task that closes every phase (planning principle:
no separate polish phase — each phase leaves its code clean). Now that
the full HTTP stack exists, simplify the middleware assembly and the
test helpers that accreted across 3.1–3.8, run the full lint/test
sweep, and touch up docs: package docs for `internal/server`'s HTTP
surface, the `--help` text for the HTTP flags (final wording:
`--http`, `--http-addr`, `--http-auth-token-file`, token-file
permission expectations), and the repo `CLAUDE.md` command notes if
anything changed. Verify every Phase 3 exit criterion and leave the
tree ready for Phase 4 entry.

**Implementation Notes:**

- Files:
  - Edit (as needed): `internal/server/http.go`, `auth.go`,
    `reqlog.go`, `server.go` — consolidate duplicated middleware
    plumbing; one canonical pipeline-assembly function.
  - Edit (as needed): `internal/server/*_test.go` — extract shared
    harness helpers (launch, port-scrape, bearer round-tripper,
    instrumented vault) into one `_test.go` helper file; delete
    duplicates.
  - Edit: `cmd/eth-signer-mcp/main.go` — final `--help` flag
    descriptions for the HTTP flags.
  - Edit (if needed): repo `CLAUDE.md`, `internal/server` package doc.
- Approach: refactor only — no behavior changes; every change covered
  by the existing Phase 3 test suite. Re-run the full suite after each
  consolidation step.

**Acceptance Criteria:**

- [ ] Exactly one pipeline-assembly path exists in
      `internal/server/http.go` (MaxBytes → reqlog → auth → SDK),
      used by production and by every matrix/e2e test — no test
      assembles its own divergent pipeline.
- [ ] Shared test helpers (binary launch, announce scrape, bearer
      round-tripper, instrumented vault) exist once each across
      `internal/server/*_test.go`; duplicated copies removed.
- [ ] `make lint` clean with zero suppressions added during Phase 3;
      depguard green (`server` → `signing`+`obs` only; `signing` still
      imports nothing internal); the ADR-007 offline-import test still
      green (no HTTP client code crept toward `signing`).
- [ ] `make test && make build` green; full CI green on `main`,
      including the 3.6 concurrent-calls test and the 3.5 matrix.
- [ ] `--help` documents all three HTTP flags with the token-file
      permission guidance (chmod 600 / `--strict-perms`); package doc
      for `internal/server` describes the pipeline order and the
      hardening layers.
- [ ] Every Phase 3 exit criterion at the top of this file re-checked
      and ticked, with test names cited in the PR description for: the
      hardening matrix, the pipeline-order pins, the concurrent-calls
      test, the parity test, and the shutdown tests.
- [ ] No behavior changes: the diff is refactor/docs only, and the
      golden vectors, schemas, and wire encodings are byte-identical
      before and after.

**Testing Notes:**

- Run the hardening matrix and the concurrent-calls test 10×
  consecutively under `-race` after the refactor as a local flake
  check (not a CI loop).
- If a refactor wants to change behavior, it is out of scope — file it
  for Phase 4 entry discussion instead.

---

## Phase Exit Review Checklist

When all nine issues land, the reviewer confirms:

- [ ] All Phase 3 exit criteria (top of this file) are satisfied.
- [ ] ADR-006 fully implemented: loopback bind (verified bound
      address), SDK DNS-rebinding 403, bearer 401 before any signing
      logic, 1 MiB body cap, serialized decrypts proven under the
      required (never-waived) concurrent-calls test.
- [ ] Pipeline order MaxBytes → reqlog → auth → SDK pinned by
      regression tests tied to SDK v1.6.1 observed behavior.
- [ ] Bearer-token file is permission-checked with the same
      warn/`--strict-perms` semantics as the keystore/password files.
- [ ] ADR-007 offline-import test and ADR-008 depguard both still
      green — Phase 3 added HTTP code only to `internal/server`.
- [ ] Leak scan (raw + encoded forms) green over every captured byte
      of HTTP-path logs and responses, including 401/403 paths and the
      concurrent burst.
- [ ] stdio/HTTP parity (schemas + signed results, byte-identical on
      golden vectors) green; graceful shutdown green on both
      transports.
- [ ] `make lint && make test && make build` green on a clean
      checkout; CI green on `main` with go-sdk pinned at v1.6.1 for
      server and test client.
