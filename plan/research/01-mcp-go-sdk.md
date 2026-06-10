# Research: Building an MCP Server with the Official Go SDK (`github.com/modelcontextprotocol/go-sdk` v1.6.x)

## Summary

The official Go MCP SDK lets you build an MCP server by calling `mcp.NewServer`, registering a tool with the generic `mcp.AddTool` (which derives the JSON schema from a Go struct via the external `google/jsonschema-go` dependency), and running the server over a transport — `&mcp.StdioTransport{}` for the stdio default, or `mcp.NewStreamableHTTPHandler(...)` for HTTP. `StreamableHTTPOptions` ships built-in DNS-rebinding protection (on by default), tool errors are signalled via `CallToolResult.IsError` with a nil Go error, and v1.6.x carries the v1.0.0 backward-compat commitment (frozen 2025-09-30) against MCP spec revisions `2024-11-05` through `2025-11-25` [1][2][3][4].

## Key Concepts

### `mcp.NewServer` and `Implementation`

The server is constructed with the server's identity and optional behavior knobs [1]:

```go
func NewServer(impl *Implementation, options *ServerOptions) *Server

type Implementation struct {
    Name    string
    Version string
}
```

`ServerOptions` exposes optional handlers (`InitializedHandler`, `RootsListChangedHandler`, etc.) and a `PageSize` knob; for a small tool server like `eth-signer-mcp`, passing `nil` is sufficient.

### Typed tool registration: `AddTool` + `jsonschema.For`

Tools are registered with the top-level generic helper, **not** `Server.AddTool` (the lower-level method) [1][5]:

```go
func AddTool[In, Out any](s *Server, t *Tool, h ToolHandlerFor[In, Out])

type ToolHandlerFor[In, Out any] func(
    ctx context.Context,
    req *CallToolRequest,
    args In,
) (*CallToolResult, Out, error)
```

The doc comment for `AddTool` states: *"If the tool's input schema is nil, it is set to the schema inferred from the In type parameter. Types are inferred from Go types, and property descriptions are read from the 'jsonschema' struct tag. Internally, the SDK uses the github.com/google/jsonschema-go package for inference and validation. The In type argument must be a map or a struct, so that its inferred JSON Schema has type 'object', as required by the spec."* [5]

The schema inference is reachable directly via the external `github.com/google/jsonschema-go/jsonschema` package (a direct dependency of the SDK — there is no SDK-embedded jsonschema package): `func For[T any](opts *ForOptions) (*Schema, error)` translates Go types into JSON Schema (strings → "string", ints → "integer", structs → object with `additionalProperties: false`, exported field JSON names, `omitempty` ⇒ optional, everything else required) [6]. In normal use, `mcp.AddTool` calls this for you; import it directly only if you need to publish a tool's schema separately.

### Transports — `StdioTransport` and `StreamableHTTPHandler`

The server is wired to a transport at `Run` time:

```go
func (s *Server) Run(ctx context.Context, t Transport) error
```

Two transports matter for `eth-signer-mcp`:

- **`StdioTransport`** — newline-delimited JSON-RPC over `os.Stdin`/`os.Stdout`. The repo's `docs/protocol.md` describes the wire format as *"newline-delimited JSON over its stdin/stdout"* and confirms `StdioTransport` is the server side of that pair (the client side is `CommandTransport`, which `exec`s the server binary as a subprocess) [4].
- **`StreamableHTTPHandler`** — an `http.Handler` you mount under `net/http`, constructed by `NewStreamableHTTPHandler` with a per-request server factory [1][3].

Both transports are **single-use**. `docs/protocol.md` states explicitly: *"Transports should not be reused for multiple connections: if you need to create multiple connections, use different transports."* [4] The source-level doc on `StreamableServerTransport` reinforces this: *"Each StreamableServerTransport must be connected (via Server.Connect) at most once."* [3] For HTTP, this is handled for you — `StreamableHTTPHandler` creates a fresh transport per session — but if you ever drive `Server.Connect` directly, allocate one transport per connection.

### Tool-level vs protocol-level errors

`CallToolResult.IsError` (a `*bool`) is the signal channel for tool-level errors; a non-nil Go `error` is reserved for protocol/transport failures [1]:

```go
type CallToolResult struct {
    Meta    Meta      `json:"_meta,omitempty"`
    Content []Content `json:"content"`
    IsError *bool     `json:"isError,omitempty"`
}

func (r *CallToolResult) GetError() error
func (r *CallToolResult) SetError(err error)
```

`SetError` sets `IsError=true` and attaches an error message as content; the handler then returns `(result, zeroOut, nil)` — a **nil** Go error. This is the mechanism by which the PRD's stable error codes (`invalid_input`, `chain_id_mismatch`, `password_error`, …) are surfaced to the client without aborting the JSON-RPC session.

### HTTP hardening built into `StreamableHTTPOptions`

Verified verbatim from the v1.6.1 source [3]:

```go
type StreamableHTTPOptions struct {
    Stateless                  bool
    JSONResponse               bool
    Logger                     *slog.Logger
    EventStore                 EventStore
    SessionTimeout             time.Duration
    DisableLocalhostProtection bool
    CrossOriginProtection      *http.CrossOriginProtection
}
```

The `DisableLocalhostProtection` doc comment is what matters for `eth-signer-mcp` [3]:

> *"disables automatic DNS rebinding protection. By default, requests arriving via a localhost address (127.0.0.1, [::1]) that have a non-localhost Host header are rejected with 403 Forbidden. This protects against DNS rebinding attacks regardless of whether the server is listening on localhost specifically or on 0.0.0.0. Only disable this if you understand the security implications."*

In other words: **bind to 127.0.0.1, leave `DisableLocalhostProtection` at its zero value (`false`), and the SDK rejects rebound DNS attacks for you with HTTP 403** — exactly the posture P0-SEC-5 wants.

`CrossOriginProtection` is **deprecated**: its doc comment says *"Deprecated: wrap the handler with cross-origin protection middleware instead."* [3] Don't set it — apply CORS / origin checks (and the bearer-token check) as ordinary HTTP middleware in front of the handler.

### Version, maturity, and spec compatibility

- v1.0.0 was released **2025-09-30** with the commitment *"going forward we won't make breaking API changes"* [7].
- Latest verified release: **v1.6.1** on **2026-05-22** (v1.6.0 on 2026-05-08) [8]. v1.6.0 introduced `ClientCredentialsHandler` (OAuth client-credentials grant) and an `MCPGODEBUG=disablecontenttypecheck=1` escape hatch for the unconditional `Content-Type: application/json` check on POST [8] — neither of which `eth-signer-mcp` needs.
- `go.mod` declares `go 1.25.0`, no `toolchain` directive — fully compatible with the monorepo's Go 1.26 toolchain [9]. Direct deps include `google/jsonschema-go`, `golang-jwt/jwt/v5`, `golang.org/x/oauth2`, `segmentio/encoding` [9].
- Spec-compat (verbatim from the README) [2]:

  | SDK Version | Latest MCP Spec | All Supported MCP Specs |
  |---|---|---|
  | v1.4.0+ | 2025-11-25* | 2025-11-25*, 2025-06-18, 2025-03-26, 2024-11-05 |
  | v1.2.0 – v1.3.1 | 2025-11-25** | 2025-11-25**, 2025-06-18, 2025-03-26, 2024-11-05 |
  | v1.0.0 – v1.1.0 | 2025-06-18 | 2025-06-18, 2025-03-26, 2024-11-05 |

  *Client side OAuth has experimental support. **Partial support for 2025-11-25.* (Pinning v1.6.1 puts us in the v1.4.0+ row — full 2025-11-25 server support, multi-revision back-compat.)

- **Maintainer attribution:** the README opens *"This repository contains an implementation of the official Go software development kit (SDK) for the Model Context Protocol (MCP)"*, and the GitHub repo description explicitly states *"Maintained in collaboration with Google"* under the community `modelcontextprotocol` org [2]. (Per the overview §3 caveat: do **not** call this an Anthropic co-maintained project — it is not.)

## How It Works

### End-to-end flow (stdio)

1. The MCP client (e.g. Claude Desktop) launches the server binary as a subprocess with stdio piped.
2. `NewServer(&Implementation{Name:"eth-signer-mcp", Version:"…"}, nil)` constructs the server.
3. `mcp.AddTool(server, &mcp.Tool{Name:"sign_transaction", …}, handler)` registers a typed handler. The SDK reads `In`'s exported fields and `jsonschema:"…"` tags, calls into `google/jsonschema-go`, and publishes the input schema in the tool catalog. `additionalProperties: false` falls out of struct inference — that's exactly the PRD's "unknown fields are rejected (strict schema)" rule.
4. `server.Run(ctx, &mcp.StdioTransport{})` blocks: it reads newline-delimited JSON-RPC frames from stdin, dispatches `initialize`, `tools/list`, `tools/call`, etc., and writes responses to stdout [4].
5. When a `tools/call` arrives for `sign_transaction`, the SDK validates the incoming JSON against the inferred schema, unmarshals it into the `In` struct, and invokes the handler. The handler returns either a successful `*CallToolResult` (with content) or a tool-level error result (`SetError`) — both with a nil Go error.

### End-to-end flow (HTTP)

1. CLI parses `--http`, `--http-addr`, `--http-auth-token-file`.
2. Construct the server exactly as in the stdio case (same `AddTool` registrations).
3. Build `handler := mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return server }, opts)` — a single shared server is fine for this app since the signing-side state (loaded keystore JSON, optional `--chain-id` guard) is read-only across sessions.
4. Leave `opts.DisableLocalhostProtection` at `false` and `opts.CrossOriginProtection` at `nil` — the built-in localhost/Host-header DNS-rebinding check fires automatically; cross-origin protection comes from the bearer-token middleware below [3].
5. Wrap the handler in a tiny middleware that constant-time-compares `Authorization: Bearer <token>` against the file-loaded token; reject with 401 before the MCP handler ever sees the request body. Bind `http.Server.Addr` to `127.0.0.1:<port>`.
6. The SDK spins up a fresh `StreamableServerTransport` per session inside the handler [3][4]; concurrent sessions are isolated.

### Tool-error vs protocol-error decision tree

- Bad input JSON (schema fails), `chainId` guard mismatch, keystore decrypt failure, malformed tx fields → **tool error**: build a `*CallToolResult`, call `SetError`, return `(result, zero, nil)`. Client sees a normal `tools/call` response with `isError: true`.
- Transport blew up, JSON-RPC framing broke, panic in the handler that we recovered → **protocol error**: return a non-nil Go error. Client sees a JSON-RPC error response.

## Code Examples

A minimal runnable skeleton showing stdio + HTTP selectable at runtime, with the security middleware sketched in. This is the shape Phase 1 of the PRD's milestones lands.

```go
package main

import (
    "context"
    "crypto/subtle"
    "errors"
    "flag"
    "log"
    "log/slog"
    "net/http"
    "os"
    "time"

    "github.com/modelcontextprotocol/go-sdk/mcp"
)

// SignTxInput is the typed argument struct AddTool will derive a JSON schema
// from. Field-level `jsonschema:"..."` tags become property descriptions.
type SignTxInput struct {
    Type    string `json:"type"    jsonschema:"\"0x0\"|\"legacy\" or \"0x2\"|\"eip1559\""`
    ChainID string `json:"chainId" jsonschema:"decimal or 0x-hex chain id"`
    Nonce   string `json:"nonce"   jsonschema:"decimal or 0x-hex nonce"`
    To      string `json:"to,omitempty" jsonschema:"recipient address; omit for contract creation"`
    Value   string `json:"value"   jsonschema:"decimal or 0x-hex wei"`
    Data    string `json:"data"    jsonschema:"0x-prefixed calldata; \"0x\" allowed"`
    Gas     string `json:"gas"     jsonschema:"gas limit"`

    // Legacy-only
    GasPrice string `json:"gasPrice,omitempty"`

    // EIP-1559-only
    MaxFeePerGas         string `json:"maxFeePerGas,omitempty"`
    MaxPriorityFeePerGas string `json:"maxPriorityFeePerGas,omitempty"`
}

type SignTxOutput struct {
    RawTransaction string    `json:"rawTransaction"`
    Signature      Signature `json:"signature"`
    Hash           string    `json:"hash,omitempty"` // P1
    From           string    `json:"from,omitempty"` // P1
}

type Signature struct {
    R string `json:"r"`
    S string `json:"s"`
    V string `json:"v"`
}

func main() {
    var (
        httpMode      = flag.Bool("http", false, "run HTTP transport instead of stdio")
        httpAddr      = flag.String("http-addr", "127.0.0.1:0", "HTTP bind address")
        httpTokenFile = flag.String("http-auth-token-file", "", "bearer token file (HTTP)")
        // ... --keystore, --password-file, --chain-id elided
    )
    flag.Parse()

    server := mcp.NewServer(
        &mcp.Implementation{Name: "eth-signer-mcp", Version: buildVersion},
        nil,
    )

    mcp.AddTool(server,
        &mcp.Tool{
            Name:        "sign_transaction",
            Description: "Sign a fully-specified Ethereum transaction with the loaded keystore. Offline only.",
        },
        signTransactionHandler, // see below
    )

    ctx := context.Background()

    if !*httpMode {
        if err := server.Run(ctx, &mcp.StdioTransport{}); err != nil {
            log.Fatalf("stdio server: %v", err)
        }
        return
    }

    // HTTP path
    if *httpTokenFile == "" {
        log.Fatal("--http-auth-token-file is required with --http")
    }
    expectedToken := loadBearerToken(*httpTokenFile) // []byte, fixed-length-friendly

    mcpHandler := mcp.NewStreamableHTTPHandler(
        func(*http.Request) *mcp.Server { return server },
        &mcp.StreamableHTTPOptions{
            // DisableLocalhostProtection left at false: SDK rejects requests
            // with non-localhost Host headers arriving on 127.0.0.1 / [::1]
            // with HTTP 403 automatically.
            Logger:         slog.Default(),
            SessionTimeout: 5 * time.Minute,
        },
    )

    httpServer := &http.Server{
        Addr:    *httpAddr,
        Handler: bearerAuth(expectedToken, mcpHandler),
    }
    log.Printf("eth-signer-mcp listening on %s", *httpAddr)
    if err := httpServer.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
        log.Fatalf("http server: %v", err)
    }
}

// signTransactionHandler is the typed ToolHandlerFor[SignTxInput, SignTxOutput].
// Tool-level errors (bad input, chainId mismatch, decrypt failure) set IsError
// on the result and return a nil Go error. Only transport-level failures return
// a non-nil Go error.
func signTransactionHandler(
    ctx context.Context,
    _ *mcp.CallToolRequest,
    in SignTxInput,
) (*mcp.CallToolResult, SignTxOutput, error) {
    if err := validateInput(in); err != nil {
        // PRD error code: invalid_input / unsupported_type / chain_id_mismatch ...
        res := &mcp.CallToolResult{
            Content: []mcp.Content{
                &mcp.TextContent{Text: err.Error()},
            },
        }
        res.SetError(err) // sets IsError=true
        return res, SignTxOutput{}, nil
    }

    out, err := signWithKeystore(in) // owns password-file read + zeroing
    if err != nil {
        res := &mcp.CallToolResult{
            Content: []mcp.Content{
                &mcp.TextContent{Text: "signing failed: " + sanitize(err)},
            },
        }
        res.SetError(err)
        return res, SignTxOutput{}, nil
    }
    return &mcp.CallToolResult{
        Content: []mcp.Content{
            &mcp.TextContent{Text: out.RawTransaction},
        },
    }, out, nil
}

// bearerAuth constant-time-compares the Authorization header bearer token.
// crypto/subtle.ConstantTimeCompare leaks length, so compare fixed-length
// tokens (or HMACs of the token).
func bearerAuth(expected []byte, next http.Handler) http.Handler {
    return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
        const prefix = "Bearer "
        h := r.Header.Get("Authorization")
        if len(h) <= len(prefix) || h[:len(prefix)] != prefix {
            http.Error(w, "unauthorized", http.StatusUnauthorized)
            return
        }
        got := []byte(h[len(prefix):])
        if len(got) != len(expected) || subtle.ConstantTimeCompare(got, expected) != 1 {
            http.Error(w, "unauthorized", http.StatusUnauthorized)
            return
        }
        next.ServeHTTP(w, r)
    })
}

var buildVersion = "0.0.0-dev"

func validateInput(SignTxInput) error    { return nil /* fill in */ }
func sanitize(err error) string          { return "internal_error" /* never leak secrets */ }
func loadBearerToken(string) []byte      { return nil /* read, trim trailing \n, mlock optional */ }

type signResult = SignTxOutput
func signWithKeystore(SignTxInput) (signResult, error) { return signResult{}, nil }
```

Key points the skeleton illustrates:

- Same `server` object, same `AddTool` registrations, two transports — matches PRD P0-MCP-2 ("Both transports expose the same tools and the same JSON schemas").
- The handler returns *typed* `SignTxOutput`; the SDK marshals it. With a typed `Out`, the SDK also infers the **output** schema [5], which clients can use to render the response.
- Tool errors use `SetError(err)` and return a nil Go error. The Go error return is reserved for genuine protocol/system breakage.
- The HTTP path leaves the SDK's localhost DNS-rebinding guard armed and adds the bearer-token check as a thin middleware, **not** via the deprecated `StreamableHTTPOptions.CrossOriginProtection` field.

## Common Pitfalls

- **Returning a non-nil Go error for a bad-input case.** That aborts JSON-RPC semantics instead of giving the client a structured `isError: true` result. Use `result.SetError(err)` + nil Go error for all PRD error codes (`invalid_input`, `chain_id_mismatch`, `password_error`, `unsupported_type`, `keystore_error`). Reserve non-nil Go error for transport-layer failures.
- **Reusing a transport across connections.** `docs/protocol.md` and the source doc on `StreamableServerTransport` both say transports are single-use [3][4]. If you ever drive `Server.Connect` directly (rare), construct a fresh transport per call. The `NewStreamableHTTPHandler` path handles this for you.
- **Disabling localhost protection because you "moved to 0.0.0.0" for a docker test.** The default protection works *regardless* of bind address — it inspects the Host header of localhost-originated requests. Don't set `DisableLocalhostProtection = true` unless you've replaced it with an equivalent middleware [3]. For `eth-signer-mcp` we bind to 127.0.0.1 and leave it alone.
- **Using the deprecated `CrossOriginProtection` field.** The field doc says *"Deprecated: wrap the handler with cross-origin protection middleware instead."* [3] Apply CORS/origin checks (if you ever need them) as ordinary middleware, like the bearer-token gate.
- **Calling `Server.AddTool` instead of the top-level `mcp.AddTool`.** The method form requires you to provide the JSON schema yourself; the generic `mcp.AddTool` derives it from the `In` struct via `jsonschema.For` and enforces MCP spec conformance [5]. For typed Go handlers, the generic form is what you want.
- **Trusting `jsonschema:` tag on a non-exported field.** Schema inference walks **exported** struct fields keyed by their JSON name [6]. Unexported fields are invisible.
- **Putting OAuth/client-OAuth scaffolding on the path.** The historical `mcp_go_client_oauth` build tag was dropped before v1.6 (client OAuth went GA around v1.5); it is not a knob to flip [8]. (Not relevant to a local stdio/bearer server; flagged so README/docs don't reintroduce the historical claim.)
- **Pinning to a Go toolchain older than 1.25.** `go.mod` declares `go 1.25.0` [9]; the monorepo's Go 1.26 toolchain is compatible. Pinning lower will fail to build.
- **Quoting `additionalProperties: false` for `In` and then sending extra fields from the client.** The struct-inferred schema disallows additional properties [6], which is exactly what the PRD's "strict schema, unknown fields rejected" requires — but if you also accept `accessList` (PRD says it must be empty in v1), include the field in the struct and validate emptiness in handler code, don't try to fake it via tag tricks.

## Further Reading

- [`pkg.go.dev/github.com/modelcontextprotocol/go-sdk/mcp`](https://pkg.go.dev/github.com/modelcontextprotocol/go-sdk/mcp) — authoritative type/function reference for `NewServer`, `AddTool`, `StdioTransport`, `NewStreamableHTTPHandler`, `StreamableHTTPOptions`, `CallToolResult.SetError` [1].
- [`github.com/modelcontextprotocol/go-sdk` README](https://github.com/modelcontextprotocol/go-sdk) — quickstart, the SDK↔spec compat table, maintainer language [2].
- [`mcp/streamable.go` at v1.6.1](https://raw.githubusercontent.com/modelcontextprotocol/go-sdk/v1.6.1/mcp/streamable.go) — verbatim `StreamableHTTPOptions` struct and the DNS-rebinding / deprecation doc comments [3].
- [`docs/protocol.md`](https://github.com/modelcontextprotocol/go-sdk/blob/main/docs/protocol.md) — transport reference, single-use-transport rule, newline-delimited JSON over stdio [4].
- [`pkg.go.dev/.../mcp#AddTool`](https://pkg.go.dev/github.com/modelcontextprotocol/go-sdk/mcp#AddTool) — typed `AddTool` doc comment confirming schema inference + `jsonschema` tag behavior [5].
- [`pkg.go.dev/github.com/google/jsonschema-go/jsonschema#For`](https://pkg.go.dev/github.com/google/jsonschema-go/jsonschema#For) — type-to-schema rules: structs become objects with `additionalProperties: false`, `omitempty` ⇒ optional [6].
- [v1.0.0 release notes](https://github.com/modelcontextprotocol/go-sdk/releases/tag/v1.0.0) — backward-compatibility commitment, 2025-09-30 [7].
- [Releases page](https://github.com/modelcontextprotocol/go-sdk/releases) — v1.6.0 (2026-05-08) and v1.6.1 (2026-05-22) dates; v1.6.0 release notes for `ClientCredentialsHandler` and `MCPGODEBUG=disablecontenttypecheck=1` [8].
- [`go.mod` on `main`](https://raw.githubusercontent.com/modelcontextprotocol/go-sdk/main/go.mod) — `go 1.25.0`, no toolchain directive, direct dependency list [9].

## Sources

[1] [mcp package — pkg.go.dev](https://pkg.go.dev/github.com/modelcontextprotocol/go-sdk/mcp) — Go Packages, accessed 2026-06-10. Type signatures for `NewServer`, `Server.Run`, top-level `AddTool`, `StdioTransport`, `NewStreamableHTTPHandler`, `CallToolResult` (with `IsError`, `SetError`, `GetError`), `Implementation`, `ServerOptions`, and the `ToolHandlerFor[In, Out]` handler type.

[2] [modelcontextprotocol/go-sdk README on GitHub](https://github.com/modelcontextprotocol/go-sdk) — modelcontextprotocol org, accessed 2026-06-10. Repo description ("Maintained in collaboration with Google"), opening README paragraph, MCP spec-compatibility table (verbatim), and minimum-Go-version policy reference.

[3] [`mcp/streamable.go` at tag v1.6.1](https://raw.githubusercontent.com/modelcontextprotocol/go-sdk/v1.6.1/mcp/streamable.go) — modelcontextprotocol/go-sdk, v1.6.1 (2026-05-22). Verbatim `StreamableHTTPOptions` struct definition with all seven fields, `DisableLocalhostProtection` doc comment (default-on DNS rebinding protection; 403 on non-localhost Host headers from 127.0.0.1/[::1]), `CrossOriginProtection` deprecation notice ("wrap the handler with cross-origin protection middleware instead"), and the `StreamableServerTransport` single-use constraint.

[4] [`docs/protocol.md`](https://github.com/modelcontextprotocol/go-sdk/blob/main/docs/protocol.md) — modelcontextprotocol/go-sdk, accessed 2026-06-10. Stdio transport described as newline-delimited JSON over stdin/stdout; `StreamableHTTPHandler` / `StreamableServerTransport` / `StreamableClientTransport` roles; explicit single-use-transport rule ("Transports should not be reused for multiple connections").

[5] [`mcp.AddTool` doc comment on pkg.go.dev](https://pkg.go.dev/github.com/modelcontextprotocol/go-sdk/mcp#AddTool) — Go Packages, accessed 2026-06-10. Confirms automatic JSON Schema inference from the `In` type parameter via `google/jsonschema-go`, `jsonschema:` struct tag for property descriptions, In/Out must be map or struct, and output schema inference behavior.

[6] [`jsonschema.For` doc comment on pkg.go.dev](https://pkg.go.dev/github.com/google/jsonschema-go/jsonschema#For) — `github.com/google/jsonschema-go/jsonschema`, the external package the SDK depends on directly for inference (the SDK itself embeds no jsonschema package). Signature `For[T any](opts *ForOptions) (*Schema, error)`, Go-type-to-JSON-Schema mapping rules, struct fields use JSON names, `omitempty` ⇒ optional, structs disallow `additionalProperties`.

[7] [Release v1.0.0](https://github.com/modelcontextprotocol/go-sdk/releases/tag/v1.0.0) — modelcontextprotocol/go-sdk, 2025-09-30. Stable-release announcement and backward-compatibility commitment ("going forward we won't make breaking API changes").

[8] [Releases page](https://github.com/modelcontextprotocol/go-sdk/releases) — modelcontextprotocol/go-sdk, accessed 2026-06-10. Dates for v1.6.0 (2026-05-08) and v1.6.1 (2026-05-22); v1.6.0 release notes covering `ClientCredentialsHandler` (OAuth client-credentials grant) and the `MCPGODEBUG=disablecontenttypecheck=1` escape hatch. (The build-tag-gated client-OAuth claim — historical only — corresponds to this stabilization timeline.)

[9] [`go.mod` on `main`](https://raw.githubusercontent.com/modelcontextprotocol/go-sdk/main/go.mod) — modelcontextprotocol/go-sdk, accessed 2026-06-10. Declares `go 1.25.0` with no `toolchain` directive; direct deps include `google/jsonschema-go v0.4.3`, `golang-jwt/jwt/v5 v5.3.1`, `golang.org/x/oauth2 v0.35.0`, `segmentio/encoding v0.5.4`, `yosida95/uritemplate/v3 v3.0.2`, `google/go-cmp v0.7.0`, `golang.org/x/time v0.15.0`, `golang.org/x/tools v0.42.0`.
