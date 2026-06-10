# MCP SDK Spike — `github.com/modelcontextprotocol/go-sdk@v1.6.1`

**Date:** 2026-06-10  
**SDK pin:** `github.com/modelcontextprotocol/go-sdk v1.6.1`  
**Status:** Complete — all five questions answered; no architecture contradictions found.

> **⚠ CONTRADICTION / RED-FLAG SUMMARY (none)**  
> All five architecture assumptions validated against the real v1.6.1 API.  
> The locked middleware nesting is confirmed unobstructed.  
> Localhost/DNS-rebinding protection (`DisableLocalhostProtection`) is present and enabled by default.  
> No blocking issues for Phase 2 or Phase 3.

---

## Question 1 — In-Memory Transport for Tests

**Symbol:** `mcp.NewInMemoryTransports()`  
**Package:** `github.com/modelcontextprotocol/go-sdk/mcp`

### Finding

`mcp.NewInMemoryTransports()` is the exact v1.6.1 symbol. It returns two
`*mcp.InMemoryTransport` values connected to each other via `net.Pipe()`.

**Critical ordering rule:** the server must call `server.Connect` on t1 **before**
the client calls `client.Connect` on t2, because `client.Connect` immediately
sends the MCP `initialize` request. If the server is not yet listening, the
handshake stalls.

### Verified Code Excerpt

```go
t1, t2 := mcp.NewInMemoryTransports()

// Server connects first — it must be ready to receive 'initialize'.
serverSession, err := server.Connect(ctx, t1, nil)

// client.Connect sends 'initialize' + 'notifications/initialized' synchronously.
clientSession, err := client.Connect(ctx, t2, nil)
// After client.Connect returns, clientSession.InitializeResult() is populated.
```

### Type Signatures

```go
// mcp/transport.go
func NewInMemoryTransports() (*InMemoryTransport, *InMemoryTransport)

// InMemoryTransport implements mcp.Transport
func (t *InMemoryTransport) Connect(context.Context) (Connection, error)

// Server.Connect — returns a live *ServerSession
func (s *Server) Connect(ctx context.Context, t Transport, opts *ServerSessionOptions) (*ServerSession, error)

// Client.Connect — sends initialize, returns initialized *ClientSession
func (c *Client) Connect(ctx context.Context, t Transport, opts *ClientSessionOptions) (*ClientSession, error)
```

### io.Pipe Fallback

The `io.Pipe` fallback (via `mcp.IOTransport`) is unnecessary in v1.6.1 because
`mcp.NewInMemoryTransports()` exists and is the recommended approach. It is
implemented using `net.Pipe()` internally.

---

## Question 2 — `StreamableHTTPOptions` Field Survey

**Constructor:** `mcp.NewStreamableHTTPHandler(getServer func(*http.Request) *Server, opts *StreamableHTTPOptions) *StreamableHTTPHandler`  
**Options type:** `mcp.StreamableHTTPOptions`

### Exported Fields (v1.6.1)

| Field | Type | Default | ADR-006 relevance |
|---|---|---|---|
| `Stateless` | `bool` | `false` | Stateless = no session ID validation; suitable for simple tool servers. For eth-signer-mcp we want stateful (default). |
| `JSONResponse` | `bool` | `false` | `true` → responses are `application/json` instead of `text/event-stream`. Stateless single-response mode. |
| `Logger` | `*slog.Logger` | `nil` (disabled) | Wire our injected `*slog.Logger` here. |
| `EventStore` | `EventStore` | `nil` | Stream-resumption persistence. Not needed for Phase 3. |
| `SessionTimeout` | `time.Duration` | 0 (no timeout) | Idle-session cleanup. Optional but recommended for DoS hygiene. |
| **`DisableLocalhostProtection`** | `bool` | **`false`** | **KEY for ADR-006.** When `false` (the default), requests arriving on a loopback address (`127.0.0.1`, `[::1]`) with a non-loopback `Host` header are rejected with `403 Forbidden`. This is the DNS-rebinding guard. **Never set to `true` in production.** |
| `CrossOriginProtection` | `*http.CrossOriginProtection` | `nil` | **Deprecated.** The SDK's comment says: "wrap the handler with cross-origin protection middleware instead." Do not use; our pipeline approach (Phase 3) supersedes this. |

### Phase 3 Usage Pattern

```go
handler := mcp.NewStreamableHTTPHandler(
    func(*http.Request) *mcp.Server { return srv.mcpServer },
    &mcp.StreamableHTTPOptions{
        Logger:                    srv.logger,
        SessionTimeout:            30 * time.Minute, // tunable
        // DisableLocalhostProtection: false (default — rebinding guard ON)
    },
)
```

### DNS-Rebinding Protection Detail

The `DisableLocalhostProtection` flag controls the check in
`StreamableHTTPHandler.ServeHTTP` (see source:
`mcp/streamable.go:ServeHTTP`):

```go
// Fires at the TOP of ServeHTTP, before anything else.
if !h.opts.DisableLocalhostProtection {
    if util.IsLoopback(localAddr) && !util.IsLoopback(req.Host) {
        http.Error(w, fmt.Sprintf("Forbidden: invalid Host header %q", req.Host), http.StatusForbidden)
        return
    }
}
```

This check is INSIDE the SDK handler. Our Phase 3 pipeline wraps the SDK handler
from outside, so the order is (outermost first):

```
[MaxBytesHandler] → [request-id/logging] → [bearer auth] → [SDK ServeHTTP]
                                                               ↑ rebinding check (403) fires here
```

See Question 3 for the complete pipeline analysis.

---

## Question 3 — Middleware Pipeline Order

### Confirmed: No SDK Obstruction

`StreamableHTTPHandler` implements `http.Handler` via a single `ServeHTTP`
method. Standard `http.Handler` middleware can wrap it exactly as any other
handler:

```go
// Outermost to innermost — Phase 3 will wire this in server.go / http.go:
var h http.Handler = sdkHandler                          // SDK's StreamableHTTPHandler
h = bearerMiddleware.Middleware(h)                       // 401 before SDK sees body
h = requestIDAndLoggingMiddleware(h)                     // req-id attached, latency logged
h = http.MaxBytesHandler(h, 1<<20)                       // 1 MiB body limit
```

This is the architecture's locked nesting:
**`MaxBytesHandler → request-id/logging → bearer auth → SDK handler`**

### Where Each Layer Fires

| Layer | Status code | Fires when |
|---|---|---|
| `http.MaxBytesHandler` | 413 (implicit) | Body > 1 MiB |
| request-id/logging | — (wraps, logs on return) | Always |
| bearer auth (401) | 401 | Missing or wrong `Authorization: Bearer …` |
| SDK `StreamableHTTPHandler` | 403 | DNS-rebinding: loopback-address server, non-loopback `Host` header |
| SDK normal processing | 200/204 | All guards passed |

### Security Consequence

A request with a wrong bearer token is rejected **before** the SDK ever reads
the body (body is available but the SDK handler is never reached). This is
correct: bearer auth runs at position 3, SDK runs at position 4.

A DNS-rebinding attack (wrong `Host` header from an attacker-controlled page)
reaches the SDK but is stopped at the very first line of `ServeHTTP`. Even
if bearer auth somehow passed (it wouldn't for an external attacker), the
rebinding guard blocks.

### Confirmed: Locked Nesting is Valid

The architecture's assumption "nothing in the SDK prevents the locked nesting"
is **CONFIRMED**. `StreamableHTTPHandler.ServeHTTP` is an ordinary
`http.Handler`; no internal SDK hook intercepts or reorders outer middleware.

---

## Question 4 — Request-ID Source

### Finding: No Exported Request ID in v1.6.1

The jsonrpc2 request ID **is** threaded through the server-side context
(see `server.go:1451` — `ctx = context.WithValue(ctx, idContextKey{}, req.ID)`),
but `idContextKey{}` is an **unexported type** defined in `mcp/streamable.go`.
User code cannot read it.

The SDK source even notes the gap explicitly (streamable.go, near line 899):

```
// TODO:
//   3. Add a `func ForRequest(context.Context) jsonrpc.ID` accessor that lets
//      any transport access the incoming request ID.
//
// For now, by giving only the StreamableServerTransport access to the request
// ID, we avoid having to make this API decision.
```

This TODO confirms the accessor does not exist in v1.6.1.

### Decision

**Phase 2/3 will use a UUIDv4 per tool call**, generated in the server handler,
propagated via `signing.WithRequestID(ctx, id)`.

```go
// In the server tool handler (internal/server/handlers.go, Phase 2):
id := uuid.NewString() // UUIDv4 via github.com/google/uuid or crypto/rand
ctx = signing.WithRequestID(ctx, id)
```

This decision is robust against future SDK versions that may expose the request
ID: if a future `mcp.ForRequest(ctx)` accessor appears, `signing.WithRequestID`
can be wired to it without changing the consumer interface.

---

## Question 5 — `jsonschema-go` Tag Surface

**Package:** `github.com/google/jsonschema-go/jsonschema` (v0.4.3, used by the SDK)  
**Entry point used by SDK:** `jsonschema.ForType(reflect.Type, *jsonschema.ForOptions)` (called inside `mcp.AddTool`)

### What Struct Tags Support

| Tag | Mechanism | Outcome |
|---|---|---|
| `jsonschema:"description text"` | Sets `schema.Description` on the property | Per-field descriptions in the inferred schema |
| `json:"name,omitempty"` | `omitempty` → field not in `Required` list | Property becomes optional |
| `json:"name,omitzero"` | `omitzero` → field not in `Required` list | Property becomes optional |
| _(no tag / non-omitempty)_ | Field appears in `Required` list | Property is required |

**That is the entire tag vocabulary.** The `jsonschema` tag sets only `Description`.

### `additionalProperties: false` Behavior

**CONFIRMED automatic.** `forType` for `reflect.Struct` sets:

```go
s.AdditionalProperties = falseSchema()  // &Schema{Not: &Schema{}}
```

This happens unconditionally for every struct. `mcp.AddTool[signing.TxRequest, ...]`
will produce a schema with `additionalProperties: false` — unknown fields in
tool arguments are rejected by the SDK before the handler is called.
This satisfies PRD "unknown fields rejected" **by construction**.

### `pattern` and `maxLength` — NOT Expressible in Tags

There is no `jsonschema` tag syntax for `pattern`, `maxLength`, `minimum`,
`maximum`, or any other validation keyword beyond `description`. The
`disallowedPrefixRegexp` in `infer.go` explicitly rejects tag values beginning
`WORD=`, reserving that namespace for future expansion — but no keywords are
defined yet.

Setting a `pattern` like `"^0x[0-9a-fA-F]+$"` (for hex-encoded fields) or a
`maxLength` for the `data` field (≤ 512 KiB hex chars) requires one of:

1. **Post-inference schema mutation** — call `jsonschema.ForType`, then patch
   `schema.Properties["fieldName"].Pattern = "^0x[0-9a-fA-F]+$"` and supply
   the patched schema to `mcp.AddTool` via `Tool.InputSchema`.
2. **`validate.go` enforcement** — parse and validate the constraint in
   `signing.Validate()` after the SDK has decoded the struct. This is where
   hex-prefix checks, `chainId != 0`, data-length cap, and EIP-55 live anyway.

**Architecture decision (confirmed):** use approach 2 for Phase 2. Struct tags
carry only descriptions; all parsing and range constraints live in `validate.go`.
The split is:

| Constraint | Source |
|---|---|
| Field presence (required/optional) | `json:"...,omitempty"` struct tags |
| Field descriptions | `jsonschema:"…"` struct tags |
| `additionalProperties: false` | automatic from struct inference |
| Hex-prefix patterns, `maxLength`, numeric bounds | `signing.validate()` |
| `chainId != 0`, EIP-55 rule, data-cap, guard | `signing.validate()` |

### Confirmed `mcp.AddTool` Typed-Registration Signature

```go
// Generic signature confirmed in v1.6.1:
//   func AddTool[In, Out any](s *Server, t *Tool, h ToolHandlerFor[In, Out])
//
// where ToolHandlerFor[In, Out any] is:
//   func(_ context.Context, request *CallToolRequest, input In) (result *CallToolResult, output Out, _ error)
//
// The snippet below is a THROWAWAY verification excerpt — it is NOT committed
// as production code. The actual TxRequest / SignResult types land in Phase 2.

type verifIn struct {
    Type    string `json:"type"    jsonschema:"transaction type: 0x0 (legacy) or 0x2 (EIP-1559)"`
    ChainID string `json:"chainId" jsonschema:"chain ID as 0x-prefixed hex uint256"`
    Nonce   string `json:"nonce"   jsonschema:"nonce as 0x-prefixed hex uint64"`
    To      string `json:"to,omitempty" jsonschema:"recipient address (EIP-55 checksummed); omit for contract creation"`
}
type verifOut struct {
    RawTransaction string `json:"rawTransaction" jsonschema:"RLP-encoded signed transaction, 0x-prefixed hex"`
}

// Inferred input schema: type=object, additionalProperties=false,
//   required=[type, chainId, nonce], optional=[to].
// Inferred output schema: type=object, additionalProperties=false,
//   required=[rawTransaction].
mcp.AddTool[verifIn, *verifOut](srv, &mcp.Tool{
    Name:        "sign_transaction",
    Description: "Signs a fully-specified Ethereum transaction.",
}, func(ctx context.Context, req *mcp.CallToolRequest, in verifIn) (*mcp.CallToolResult, *verifOut, error) {
    // handler body (Phase 2 will call signing.Signer.SignTransaction)
    return nil, &verifOut{RawTransaction: "0x…"}, nil
})
```

**Verified:** The generic parameters compile. Schema inference produces
`type: object`, `additionalProperties: false`, with `omitempty` fields optional
and all others required. The `jsonschema` description tag appears in the inferred
schema's `properties["fieldName"].description`.

---

## Decisions

1. **Request-ID source:** `signing.WithRequestID(ctx, uuid.NewString())` — UUIDv4
   per tool call in the server handler. Rationale: `idContextKey{}` is unexported
   in v1.6.1; no `ForRequest(ctx)` accessor exists. If a future SDK version
   exposes the request ID, the wiring point (`signing.WithRequestID`) stays stable.

2. **Tag surface split:**
   - Struct tags provide: `description` (via `jsonschema:"…"`) and
     `required`/`optional` (via `json:"…,omitempty"`). `additionalProperties:false`
     is automatic.
   - `signing.validate()` enforces: hex patterns, `maxLength` for `data`,
     `chainId != 0`, EIP-55 rule, access-list must be empty, chain-id guard,
     type allow-list. Nothing from this list can be expressed in struct tags at
     the v1.6.1 jsonschema-go surface.

3. **Localhost protection:** `DisableLocalhostProtection: false` (the default)
   in `StreamableHTTPOptions` for Phase 3. The flag is available and the guard
   is on by default. **Do not disable.**

4. **`CrossOriginProtection` field:** Deprecated; do not use. Wrap the SDK handler
   in standard `http.Handler` middleware instead (as the architecture specifies).

5. **Middleware nesting:** Confirmed compatible with the locked order:
   `http.MaxBytesHandler → request-id/logging → bearer auth → SDK handler`.
   The SDK's DNS-rebinding 403 fires inside `ServeHTTP` (position 4), after
   bearer auth (position 3). No SDK mechanism prevents this nesting.
