# Tool Reference & Request Recipes

This guide is the complete, transport-agnostic request/response contract for the
two tools that `eth-signer-mcp` exposes over MCP: **`sign_transaction`** and
**`get_address`**. Whether you drive the server over stdio (a Claude Desktop-style
MCP client) or Streamable HTTP (`--http`), the tool input and output shapes are
identical — only the framing around them differs. By the end you will know every
input field (and its applicability, encoding, and validation rule), the exact
output schema, two byte-for-byte worked examples, and the error wire shape.

The server is **strictly offline and never broadcasts**: `sign_transaction`
returns broadcast-ready signed RLP, and *you* are responsible for submitting it
via `eth_sendRawTransaction`. (source: `internal/server/server.go`,
`internal/signing/result.go`)

For setup and transport mechanics, see the install/run and HTTP guides
(`01`–`03` in this directory) and the app reference at
[`../../README.md`](../../README.md). For live captures with golden-vector
parity, see [`../demo.md`](../demo.md). For fixing specific errors, see the
troubleshooting guide ([07-troubleshooting.md](07-troubleshooting.md)).

---

## 1. `sign_transaction` overview

`sign_transaction` signs a **fully-specified** Ethereum transaction with the
loaded keystore account and returns the signed RLP plus signature components and
the transaction hash. It supports two transaction types: **type 0 (legacy,
EIP-155)** and **type 2 (EIP-1559)**. The result is **not** broadcast.

The tool is registered with this exact description string (source:
`internal/server/server.go`):

> Sign a fully-specified Ethereum transaction (type 0 / legacy or type 2 /
> EIP-1559) with the loaded keystore. Supported types: 0x0 (legacy, EIP-155) and
> 0x2 (EIP-1559). The signed transaction is returned as a hex-encoded RLP string
> (rawTransaction) ready for eth_sendRawTransaction, along with signature
> components and the transaction hash. The result is NOT broadcast — the caller
> is responsible for submission.

The input schema is **inferred from the typed Go struct `signing.TxRequest`**
(source: `internal/signing/request.go`) via `mcp.AddTool`. Two consequences
matter for callers:

- **All fields are JSON strings**, including every numeric field. There are no
  JSON numbers in this contract.
- **`additionalProperties: false`** — unknown fields are rejected at the SDK
  schema layer. Send only the fields documented below. (source:
  `testdata/schema/sign_transaction.golden.json`)

Each call re-reads the password file and runs the keystore KDF (scrypt); the key
is never cached. Expect ~0.5–1 s per call with a standard-scrypt keystore, or
~50 ms with a light-scrypt keystore. (See the latency notes in
[`../../README.md`](../../README.md).)

---

## 2. Input field reference

Field names, JSON types, the required set (from the inferred schema), per-type
applicability, accepted encodings, and rules. Every JSON value is a **string**.

| Field | JSON type | Required (schema) | Applies to | Accepted encoding | Rules |
|---|---|---|---|---|---|
| `type` | string | **yes** | both | exact literal | One of `"0x0"`, `"legacy"` (type 0) or `"0x2"`, `"eip1559"` (type 2). Anything else → `unsupported_type`. Case-sensitive. |
| `chainId` | string | **yes** | both | decimal or `0x`-hex | Must parse as a non-negative integer; **must not be zero**; must fit in `uint64`. |
| `nonce` | string | **yes** | both | decimal or `0x`-hex | Sender transaction count; must fit in `uint64`. |
| `value` | string | **yes** | both | decimal or `0x`-hex | Wei to transfer; zero allowed; ≤ 256 bits. |
| `data` | string | **yes** | both | `0x`-prefixed even-length hex | Call data / init code. `"0x"` = empty. Decoded length ≤ 256 KiB. See §3. |
| `gas` | string | **yes** | both | decimal or `0x`-hex | Gas limit; must fit in `uint64`. |
| `to` | string | no | both | `0x`-prefixed 20-byte hex | Recipient. **Omit for contract creation.** Mixed-case must pass EIP-55; all-lower / all-upper accepted as-is. |
| `gasPrice` | string | no | **type 0 only** | decimal or `0x`-hex | Required for type 0; **rejected on type 2**. Wei per gas; ≤ 256 bits. |
| `maxFeePerGas` | string | no | **type 2 only** | decimal or `0x`-hex | Required for type 2; **rejected on type 0**. Wei; ≤ 256 bits. |
| `maxPriorityFeePerGas` | string | no | **type 2 only** | decimal or `0x`-hex | Required for type 2; **rejected on type 0**. Wei; ≤ 256 bits. |
| `accessList` | array (or null) | no | both | array of objects | EIP-2930 list. **Must be empty in v1**; any non-empty list → `invalid_input`. Present only to satisfy strict schema inference. |

### Schema-enforced vs validate.go-only

The library that infers the schema (`google/jsonschema-go` v0.4.3) supports only
*description* text in struct tags, not constraint keywords. As a result the
inferred schema enforces a **narrow** contract and `validate.go` is the **sole
authoritative enforcer** of the rest (source: `request.go` doc comment,
`validate.go` doc comment).

**Enforced by the inferred schema** (`testdata/schema/sign_transaction.golden.json`):

- Every property is `type: "string"` (except `accessList`, typed
  `["null","array"]` of `additionalProperties:false` objects).
- The **required set** is exactly: `type`, `chainId`, `nonce`, `value`, `data`,
  `gas`.
- `additionalProperties: false` — unknown fields are rejected.

**Enforced only in `validate.go`** (not expressible in the schema):

- The hex / decimal **encoding patterns** on every numeric field and on `to`.
- The `data` **max length** (524,290 hex chars including `0x` = 262,144 decoded
  bytes).
- The **EIP-55 checksum** rule on `to`.
- The **per-type required/forbidden** field rules: `gasPrice` is required on
  type 0 and forbidden on type 2; `maxFeePerGas` and `maxPriorityFeePerGas` are
  required on type 2 and forbidden on type 0.
- The **non-empty `accessList` rejection** and the **`chainId != 0`** rule.

Note that `to`, `gasPrice`, `maxFeePerGas`, `maxPriorityFeePerGas`, and
`accessList` are **not** in the schema `required` set, yet the fee fields become
*conditionally* required by `validate.go` depending on `type`. Schema rejection,
when it fires, is a bonus UX layer; `validate.go` is the contract.

---

## 3. Numeric and data encoding rules

(source: `validate.go` — `parseBigInt`, `parseUint64`, `parseData`)

**Integer fields** (`chainId`, `nonce`, `value`, `gas`, `gasPrice`,
`maxFeePerGas`, `maxPriorityFeePerGas`) accept **either** form:

- **Decimal string**: `"1"`, `"21000"`, `"0"`. Leading zeros are **rejected**
  (`"007"` → error); the single character `"0"` is the only valid zero.
- **`0x`-hex string**: `"0x1"`, `"0x5208"`. `0X` uppercase prefix is also
  accepted. Leading zeros in hex are **normalised** (`"0x0009"` == `9`).

Additional integer rules:

- A sign prefix (`+` or `-`), anywhere, is rejected. Negative values are
  rejected.
- `"0x"` with no digits after the prefix is rejected.
- `chainId` **must not be zero** — a zero chain id would select the
  replay-unprotected Homestead signer, so it is rejected with `invalid_input`.
  `chainId` must also fit in `uint64`.
- `nonce` and `gas` must fit in `uint64`.
- `value` and the fee fields must be ≤ 256 bits.

**`data` field** rules:

- Must be `0x`-prefixed (`0X` accepted) with an **even** number of hex digits
  (each byte is two hex chars).
- `"0x"` means **empty** call data — it decodes to a non-nil empty byte slice,
  which RLP-encodes as `0x80` (empty string), the correct wire form for empty
  calldata. (source: `parseData`)
- Decoded length must be **≤ 256 KiB (262,144 bytes)**. The equivalent hex-string
  limit is **524,290 chars** including the `0x` prefix. This cap lives in
  `validate.go` (`maxDataBytes`), not the schema.

---

## 4. Legacy vs EIP-1559 applicability matrix

The transaction `type` decides which fee fields are required, which are
forbidden, and how the signature `v` is interpreted. Omitting `to` makes any
transaction a contract creation.

| Field | Type 0 (`"0x0"` / `"legacy"`) | Type 2 (`"0x2"` / `"eip1559"`) |
|---|---|---|
| `gasPrice` | **required** | forbidden → `invalid_input` |
| `maxFeePerGas` | forbidden → `invalid_input` | **required** |
| `maxPriorityFeePerGas` | forbidden → `invalid_input` | **required** |
| `to` | optional (omit = contract creation) | optional (omit = contract creation) |
| `chainId`, `nonce`, `value`, `data`, `gas` | required | required |

**Contract creation:** omit `to` entirely (an empty/absent `to` parses to a nil
address → contract creation). Put your init code in `data`. This works for both
transaction types. (source: `parseToAddress`)

**`to` checksum behaviour** (source: `parseToAddress`, `hasMixedCase`):

- Must be exactly 42 chars (`0x` + 40 hex).
- **All-lowercase** and **all-uppercase** addresses are accepted without a
  checksum check.
- A **mixed-case** address (contains both upper- and lower-case hex letters)
  **must** match its EIP-55 checksum, or you get
  `invalid_input` ("EIP-55 checksum mismatch").

---

## 5. Output: `SignResult`

On success `sign_transaction` returns a `signing.SignResult`. **All fields are
always present** (no `omitempty`), regardless of value. (source: `result.go`)

| Field | JSON type | Description |
|---|---|---|
| `rawTransaction` | string (`0x`-hex) | RLP of the signed transaction, ready for `eth_sendRawTransaction`. **The caller submits it; the server does not.** |
| `signature.r` | string (`0x`-hex) | ECDSA `r` component (32-byte quantity). |
| `signature.s` | string (`0x`-hex) | ECDSA `s` component (32-byte quantity). |
| `signature.v` | string (`0x`-hex) | Recovery value — see below. |
| `hash` | string (`0x`-hex) | Keccak-256 hash of the signed transaction (the tx id). |
| `from` | string (EIP-55) | Checksummed sender recovered from the signature; always equals the keystore address. |

**`v` semantics by type** (source: `result.go` `SignatureValues`):

- **Type 0 (legacy, EIP-155):** `v = chainId*2 + 35` or `chainId*2 + 36`. For
  mainnet (`chainId=1`) this is `37` (`0x25`) or `38` (`0x26`).
- **Type 2 (EIP-1559):** `v` is the `yParity` value, either `0` (`0x0`) or
  `1` (`0x1`).

> The signed result is **not broadcast anywhere**. To submit it, send
> `rawTransaction` to an Ethereum node yourself (e.g.
> `cast publish <rawTransaction>` or an `eth_sendRawTransaction` RPC call).

---

## 6. Worked example A — legacy (type 0)

These are the byte-reproducible golden values from
`internal/signing/testdata/vectors/legacy-mainnet.json`, signed by the committed
**test-only** keystore.

> **TEST-ONLY FIXTURE — DO NOT SEND REAL FUNDS.** The committed keystore
> (`internal/signing/testdata/keystore-light.json`, light scrypt) and password
> (`internal/signing/testdata/password.txt`) exist solely for tests and demos.
> Account address `0x9858EfFD232B4033E47d90003D41EC34EcaEda94`. Never reuse
> these for funds.

**Request** — the `arguments` object you pass to `sign_transaction`:

```json
{
  "type": "0x0",
  "chainId": "1",
  "nonce": "0",
  "to": "0x9858EfFD232B4033E47d90003D41EC34EcaEda94",
  "value": "1000000000000000000",
  "data": "0xdeadbeef",
  "gas": "100000",
  "gasPrice": "20000000000"
}
```

**Response** — the `SignResult` (note `v = 0x26`, i.e. 38 = `1*2 + 36`):

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

---

## 7. Worked example B — EIP-1559 (type 2)

Golden values from `internal/signing/testdata/vectors/1559-mainnet.json`, same
test-only keystore.

> **TEST-ONLY FIXTURE — DO NOT SEND REAL FUNDS.** As above; signed by
> `0x9858EfFD232B4033E47d90003D41EC34EcaEda94` from the committed test keystore.

**Request:**

```json
{
  "type": "0x2",
  "chainId": "1",
  "nonce": "42",
  "to": "0x9858EfFD232B4033E47d90003D41EC34EcaEda94",
  "value": "1000000000000000000",
  "data": "0xcafebabe",
  "gas": "100000",
  "maxFeePerGas": "30000000000",
  "maxPriorityFeePerGas": "2000000000"
}
```

**Response** — note `v = 0x0` (yParity 0), and `rawTransaction` carries the
`0x02` type-2 envelope prefix:

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

---

## 8. `get_address`

`get_address` takes **no arguments** — its input is an empty object `{}`. It is
registered with this exact description (source: `internal/server/server.go`):

> Return the EIP-55 checksummed Ethereum address of the loaded keystore account.
> This is a read-only operation served from the boot-time keystore snapshot; the
> password file is NOT read and no KDF runs on this path. Safe to call even if
> the password file has been rotated or made unreadable.

**Input:**

```json
{}
```

**Output** — `signing.AddressResult` (source: `result.go`,
`testdata/schema/get_address_result.golden.json`):

```json
{
  "address": "0x9858EfFD232B4033E47d90003D41EC34EcaEda94"
}
```

Key properties:

- **Read-only.** The address is served from the boot-time keystore snapshot
  captured at startup.
- **No password read, no KDF.** Unlike `sign_transaction`, this path does not
  read the password file and runs no scrypt — it is fast and cannot fail on a
  wrong/rotated password.
- Because the snapshot is fixed at boot, **replacing the keystore file requires a
  restart** to change the reported address.

---

## 9. Error responses

Tool-level errors are returned as an MCP tool result with `IsError: true`, and
`Content[0]` is a text item whose value is **compact JSON with exactly two
fields** (source: `internal/server/errors.go`, ADR-004):

```json
{"code":"invalid_input","message":"chainId: must not be zero (would select the replay-unprotected Homestead signer)"}
```

The `message` is a static, human-readable string and **never echoes raw input
values** (so a secret embedded in calldata cannot leak into the response or
logs). Any internal `Cause` is logged server-side only and never crosses the
wire.

> Distinction: a *tool-level* error (the six codes below) keeps the JSON-RPC
> session alive and is delivered as the result above. A *protocol-level* failure
> (e.g. a cancelled context) instead surfaces as a JSON-RPC error, not this
> payload. (source: `internal/server/errors.go`)

The six codes (source: `internal/signing/errors.go`):

| `code` | When it fires |
|---|---|
| `invalid_input` | Missing/malformed field, bad hex/decimal, EIP-55 checksum mismatch, `chainId=0`, type-inappropriate fee field, non-empty `accessList`, or oversize `data`. |
| `unsupported_type` | `type` is not one of `0x0` / `legacy` / `0x2` / `eip1559` (e.g. types 1/3/4). |
| `chain_id_mismatch` | Request `chainId` differs from the `--chain-id` guard configured at startup. |
| `keystore_error` | Keystore missing, unreadable, malformed, or lacking a usable `address` (boot-time failure). |
| `password_error` | Password file unreadable, or wrong password (keystore MAC failure). The server keeps running — fix and retry. |
| `internal_error` | Recovered panic, sender mismatch after signing, or a non-`ErrDecrypt` decrypt failure. The cause is logged, never sent. |

For step-by-step fixes for each code, see the troubleshooting guide
([07-troubleshooting.md](07-troubleshooting.md)).
