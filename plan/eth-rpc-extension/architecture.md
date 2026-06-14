# Software Architecture: eth-rpc `call` Extension

## Overview

This architecture extends the existing single-file Claude Code skill at
`.claude/skills/eth-rpc/eth_rpc.py` with a generic JSON-RPC passthrough
operation (`call`) that covers Ethereum's documented `eth_*` read surface in
one command. The design is a **modular monolith inside one Python file**: the
"modules" are named *sections* of `eth_rpc.py` (functions + module-level
constants) bracketed by sentinel comments and matched 1:1 by test classes in
`test_eth_rpc.py`. There is no package split, no new file in the skill, no
new third-party dependency, and no rewrite of the existing curated
`balance` / `broadcast` ops.

The guiding principles are: **smallest diff that satisfies P0**;
**argparse stops at `main`** (so every domain function is library-callable);
**dependency-injected I/O everywhere** (`rpc=`, `stdin=`) so every code
path is mockable without network; **error-and-stop in `main`**
(`ValueError` / `RPCError` print `error: ...` to stderr and return exit
code 1); and **module boundaries are extraction seams** — when the file
ever outgrows itself or a second consumer materializes, sections become
files with no signature changes.

## Architecture Principles

- **Single-file cohesion with sentinel-bounded sections** — `eth_rpc.py`
  stays one file; "module" = a named section bracketed by
  `# === MODULE: <name> ===` / `# === END MODULE: <name> ===` comments.
  Boundaries are explicit, greppable, and reviewable without a package
  split (PRD Open Q7 recommendation).
- **Argparse stops at `main`** — no domain helper accepts an
  `argparse.Namespace`. `main` decomposes args into plain kwargs before
  calling `do_call`. This is the single non-negotiable extraction-readiness
  property: a future `libs/eth-rpc-core/` consumer (second skill, Go
  re-implementation, MCP wrapper) can call `do_call` with no argparse
  dependency.
- **Inline over factor when the call site is the only call site** — new
  helpers (`_validate_rpc_url`, `_resolve_endpoint`, `_parse_params`)
  exist *only* because each is testable in isolation and used by the new
  op; trivial one-liners stay inline. Boundaries are drawn at testable
  units, not at arbitrary line counts.
- **Reuse, don't re-derive** — `rpc_call`, `RPCError`, `NETWORKS`,
  `network_config`, the `argparse` subparser pattern, and the `main`
  error-and-stop block are reused verbatim; `do_call` is the only new
  domain function.
- **Verbatim passthrough is the contract** — P0 does `json.loads` →
  `json.dumps` and forwards `--params` unchanged. No shape interpretation,
  no wrapping dict. Stdout stays pipe-friendly for `jq` / `tee`.
- **Read-oriented by default; loud opt-out** — the denylist is a name
  guard on by default; `--allow-write` removes it with a stderr warning.
  Matches the project's offline-signing posture.
- **Dependency-injected, mockable everywhere** — `do_call` takes
  `rpc=rpc_call` exactly like `do_balance` / `do_broadcast`;
  `_parse_params` takes `stdin=sys.stdin`. Tests never hit the network
  and never patch globals.
- **No circular dependencies inside the file** — section dependency is a
  strict DAG: `constants → validators/formatting/rpc_transport → network_config
  → endpoint_resolution → param_ingest → do_call → main`. Verified
  by the dependency graph below.

## System Context Diagram

```text
                                                           publicnode (or
                                                           operator-supplied
                                                           --rpc-url)
                                                                 ^
                                                                 | HTTPS
                                                                 | (loopback
                                                                 |  HTTP OK)
                                                                 |
 +------------------+    argv     +----------------------------+ |
 | Claude Code      |------------>|  eth_rpc.py (single file)  |-+
 | operator / CI    |             |                            |
 | (terminal)       |<------------|  stdout JSON / stderr err  |
 +------------------+   exit 0/1  +----------------------------+
                                                  ^
                                                  | import eth_rpc as r
                                                  |
                                       +------------------------+
                                       | test_eth_rpc.py        |
                                       | (stdlib unittest +     |
                                       |  mocked rpc / urlopen) |
                                       +------------------------+
```

No persistent state, no database, no on-disk artifacts produced by the op,
no sibling services. The skill is a stateless CLI; the test file is its
only sibling consumer today. A hypothetical future consumer
(`eth-tx-builder` reads, a second skill, a Go re-implementation) would
import `do_call` directly — `cli` would not be in their import path.

## Module Overview

The "modules" are sentinel-bounded sections of `eth_rpc.py` plus matching
test classes. Existing sections are unchanged in identity (some get one or
two new entries — noted in the Diff column). New sections are bold.

| Module (section in `eth_rpc.py`) | Responsibility | Owns | Depends On | Diff |
|---|---|---|---|---|
| `constants` | Provide module-level constants the rest of the file reads | `NETWORKS`, `USER_AGENT`, `WEI_PER_ETH`, broadcast tuning | - | + `_DENY_METHODS`, `_DENY_PREFIXES`, `_LOOPBACK_HOSTS` |
| `network_config` | Look up `name -> (chain_id, url)` | the `NETWORKS` registry surface | `constants` | unchanged |
| `validators` | Format checks for inputs (address, raw tx, hex int) | `_ADDR_RE`, `_HEX_BODY_RE`, `validate_*`, `parse_hex_int` | `constants` | unchanged |
| `formatting` | Exact wei -> ETH decimal string | `wei_to_eth_str` | `constants` | unchanged |
| `rpc_transport` | `rpc_call` (POST JSON-RPC, raise `RPCError`) + `RPCError` | nothing persisted | `constants`, stdlib `urllib`/`json` | unchanged |
| `do_balance` | Curated `eth_getBalance` op | output schema | `network_config`, `validators`, `formatting`, `rpc_transport` | unchanged |
| `do_broadcast` | Curated `eth_sendRawTransaction` op + receipt poll | output schema, `_receipt_summary` | `network_config`, `validators`, `rpc_transport` | unchanged |
| **`endpoint_resolution`** | Resolve `(chain_id, url)` from named network *or* explicit `--rpc-url`+`--chain-id`; validate URL scheme/loopback | `_resolve_endpoint`, `_validate_rpc_url` | `constants`, `network_config` | **new** |
| **`param_ingest`** | Parse `--params` (inline JSON or `-` stdin) into a Python `list` | `_parse_params` | - | **new** |
| **`do_call`** | Generic JSON-RPC passthrough orchestrator; gates by method-name denylist; forwards verbatim | nothing persisted; returns raw JSON-RPC `result` | `constants` (`_DENY_*`), `rpc_transport` | **new** |
| `main` | Argparse wiring, dispatch, stdout/stderr/exit-code conventions | exit code | every `do_*`, `endpoint_resolution`, `param_ingest` | + `call` subparser, + `call` branch |

Matching test classes in `test_eth_rpc.py`:

| Test class | Tests | Diff |
|---|---|---|
| `TestNetworkConfig` | `network_config` | unchanged |
| `TestValidateHexAddress` / `TestValidateRawTx` / `TestParseHexInt` | validators | unchanged |
| `TestRpcCall` | `rpc_call` + UA regression | unchanged |
| `TestDoBalance` | `do_balance` | unchanged |
| `TestDoBroadcastSubmit` / `TestDoBroadcastWait` / `TestReceiptSummary` | `do_broadcast` | unchanged |
| `TestWeiToEthStr` | `wei_to_eth_str` | unchanged |
| `TestMain` | argparse, dispatch, error-and-stop | + assertions for `call` dispatch |
| `TestCliSmoke` | `--help`, real-process smoke | + assert `call` in `--help` output |
| **`TestValidateRpcUrl`** | `_validate_rpc_url`: scheme, loopback HTTP, IPv6 `::1`, non-loopback HTTP rejected | **new** |
| **`TestResolveEndpoint`** | `_resolve_endpoint`: network path, custom-URL path, mutual-exclusion, required-pair | **new** |
| **`TestParseParams`** | `_parse_params`: inline array OK, object rejected, malformed JSON rejected, `-` reads from injected stdin, non-array top-level rejected | **new** |
| **`TestDoCall`** | `do_call`: happy path, denylist explicit + prefix, `allow_write` bypass, `RPCError` propagation, custom timeout forwarded | **new** |
| **`TestCallCli`** | `main` for `call`: dispatch, mutual exclusion validation, `--allow-write` stderr warning, error-and-stop | **new** |
| **`TestDenylistContents`** | drift protection: `_DENY_METHODS` equals PRD-enumerated set; `_DENY_PREFIXES` equals PRD-enumerated tuple | **new** |

## Module Dependency Graph

```text
constants ----------> (leaf; provides NETWORKS, USER_AGENT, WEI_PER_ETH,
                       _DENY_METHODS, _DENY_PREFIXES, _LOOPBACK_HOSTS)

validators ---------> constants            (regex pattern strings)

formatting ---------> constants            (WEI_PER_ETH)

rpc_transport ------> constants            (USER_AGENT header)
                      [stdlib urllib, json]

network_config -----> constants            (NETWORKS lookup)

endpoint_resolution > constants            (_LOOPBACK_HOSTS)
                      network_config       (named-network path)

param_ingest -------> (leaf; stdlib json, injectable stdin)

do_balance ---------> network_config
                      validators
                      formatting
                      rpc_transport

do_broadcast -------> network_config
                      validators
                      rpc_transport

do_call ------------> constants            (_DENY_METHODS, _DENY_PREFIXES)
                      rpc_transport        (rpc=rpc_call default)

main ---------------> endpoint_resolution  (resolve url before do_call)
                      param_ingest         (parse --params before do_call)
                      do_balance
                      do_broadcast
                      do_call
```

**Acyclicity verification:**

- Leaves (`constants`, `validators`, `formatting`, `param_ingest`) depend
  only on stdlib (and `constants` for shared values). No leaf imports
  another leaf except via the constants module.
- `endpoint_resolution` calls `network_config` for the named-network
  branch; `network_config` does not call back. Verified one-direction.
- `do_call` does **not** import `do_balance` / `do_broadcast` and
  vice-versa. They are siblings whose only common parent is `main`.
- `endpoint_resolution` does not call into `do_call`; `param_ingest`
  does not call into `do_call`. `main` is the sole fan-in to all three
  `do_*` functions plus the two new helpers.
- No section imports `main`.

The graph is a strict DAG. No circular dependency exists or can be
introduced by P0.

**Design choice — endpoint resolution lives in `main`, not in `do_call`.**
`do_call` accepts the *resolved* `url` (and a kept-for-symmetry
`chain_id`) rather than the raw network-selection trio. `main` calls
`_resolve_endpoint(args.network, args.rpc_url, args.chain_id)` and
passes the result. Rationale: `do_call`'s signature becomes
`do_call(url, method, params, allow_write, timeout, rpc=rpc_call)` —
the smallest library-shaped contract, with no triple-argument network
selector burden on a function that does not own that policy. Resolving
in `main` keeps `do_call` testable with a single URL string without
having to thread network names through every test case.

---

## Module Details

### Module: `constants`

**Responsibility:** Provide module-level constants the rest of the file
reads. Sole owner of the passthrough-safety registry.

**Section sentinel:**

```python
# === MODULE: constants ===
# Existing:  NETWORKS, USER_AGENT, WEI_PER_ETH, DEFAULT_WAIT_TIMEOUT,
#            DEFAULT_POLL_INTERVAL
# New:       _DENY_METHODS, _DENY_PREFIXES, _LOOPBACK_HOSTS
# === END MODULE: constants ===
```

**Domain Entities:**
- Network registry (`NETWORKS`)
- HTTP UA workaround (`USER_AGENT`)
- Unit conversion factor (`WEI_PER_ETH`)
- Broadcast tuning (`DEFAULT_WAIT_TIMEOUT`, `DEFAULT_POLL_INTERVAL`)
- **New:**
  - `_DENY_METHODS: frozenset[str]` — explicit denied method names
  - `_DENY_PREFIXES: tuple[str, ...]` — denied namespace prefixes
  - `_LOOPBACK_HOSTS: frozenset[str]` — hosts allowed with `http://`

**Data Store:** none; in-memory Python literals only.

**Public API:** read-only attribute access on the `eth_rpc` module. Tests
import these symbols by name (the leading underscore is the documented
convention for "internal, but importable by tests in the same package").

**Proposed addition:**

```python
# --- passthrough safety (call op) ---------------------------------------
_DENY_METHODS = frozenset({
    "eth_sendRawTransaction", "eth_sendTransaction",
    "eth_sign", "eth_signTransaction",
    "eth_signTypedData", "eth_signTypedData_v3", "eth_signTypedData_v4",
})
_DENY_PREFIXES = ("personal_", "admin_", "miner_", "engine_", "clique_")
_LOOPBACK_HOSTS = frozenset({"127.0.0.1", "localhost", "::1"})
```

**Key Design Decisions:**
- `frozenset` for membership tests (O(1), immutable).
- `tuple` of prefixes (small, ordered, used with `str.startswith` which
  accepts a tuple natively).
- All three values are sized in source code (no env-var override). Bypass
  is via `--allow-write`, not via per-deployment config. Keeps audit
  surface tiny and reviewable.
- New entries are added via PR review only; `TestDenylistContents` is the
  drift-protection assertion (one assert per constant equal to the
  PRD-enumerated literal).

**Failure Modes:** none — constants don't fail.

---

### Module: `network_config`, `validators`, `formatting`, `rpc_transport`, `do_balance`, `do_broadcast`

**Status:** unchanged in P0. Reused verbatim by the new helpers and
`do_call`. Their tests stay as-is.

The relevant property for P0 is that `rpc_transport.rpc_call` already
accepts an arbitrary `(url, method, params, timeout)` and returns the
JSON-RPC `result` opaquely — exactly the contract `do_call` needs. No
refactor required.

A *future* P1+ refactor (out of scope here) could route `do_balance` /
`do_broadcast` through `_resolve_endpoint` for symmetry. ADR-008 records
the deliberate deferral.

---

### Module: `endpoint_resolution` (new)

**Section sentinel:**

```python
# === MODULE: endpoint_resolution ===
# Public:   _resolve_endpoint(network, rpc_url, chain_id) -> (int, str)
# Public:   _validate_rpc_url(url) -> str
# === END MODULE: endpoint_resolution ===
```

**Responsibility:** Translate a user's network choice into a concrete
`(chain_id, url)` pair, with URL safety validation on the custom-endpoint
branch.

**Domain Entities:**
- A `Network` (named, from `NETWORKS`) or a `CustomEndpoint`
  (`--rpc-url` + `--chain-id`).

**Data Store:** none; pure function over inputs and `NETWORKS`.

**Public API:**

| Symbol | Signature | Description |
|---|---|---|
| `_validate_rpc_url(url: str) -> str` | url -> url, raises `ValueError` | Returns the URL unchanged if scheme in `{http, https}` and `http://` host is in `_LOOPBACK_HOSTS`. Otherwise raises with a clear message. |
| `_resolve_endpoint(network, rpc_url, chain_id) -> (int, str)` | at most one of `network` OR (`rpc_url` + `chain_id`) -> `(chain_id, url)` | Mutual-exclusion + completeness check; calls `network_config(network)` on the named path; calls `_validate_rpc_url(rpc_url)` on the custom path. |

Both helpers are leading-underscored — they are internal to the
`call` op but exported for testability (Python has no private symbol;
the underscore is the convention).

**Proposed internal structure:**

```python
import urllib.parse  # new import line; stdlib only.

def _validate_rpc_url(url):
    """Return url unchanged or raise ValueError.

    Scheme must be http or https. http:// is allowed only for loopback
    hosts. The loopback check uses urllib.parse.SplitResult.hostname which
    is documented to strip IPv6 brackets and lowercase the host.
    """
    parts = urllib.parse.urlsplit(url)
    if parts.scheme not in ("http", "https"):
        raise ValueError("rpc url must be http(s): %r" % (url,))
    if parts.scheme == "http" and parts.hostname not in _LOOPBACK_HOSTS:
        raise ValueError(
            "non-loopback http:// rpc url refused (use https): %r" % (url,)
        )
    return url


def _resolve_endpoint(network=None, rpc_url=None, chain_id=None):
    """Return (chain_id: int, url: str). Mutual-exclusion is enforced here."""
    if network is not None and (rpc_url is not None or chain_id is not None):
        raise ValueError("use --network OR (--rpc-url + --chain-id), not both")
    if network is not None:
        return network_config(network)
    if rpc_url is None or chain_id is None:
        raise ValueError("--rpc-url and --chain-id are required together")
    return int(chain_id), _validate_rpc_url(rpc_url)
```

**Key Design Decisions:**
- One entry-point function with three optional kwargs, returning the
  smallest sufficient value (a tuple). No dataclass; the function is the
  interesting bit.
- Mutual-exclusion lives here (not in argparse) because argparse cannot
  natively express "either `--network` alone, *or* both `--rpc-url` and
  `--chain-id` together" (PRD §Technical Considerations). Centralising
  the rule keeps it testable without spawning a subprocess.
- URL validation is delegated to a *separate* helper (`_validate_rpc_url`)
  rather than inlined, so that scheme/loopback testing has its own focused
  unit test class. `_resolve_endpoint` is then a thin dispatcher: named
  -> `network_config`, custom -> `_validate_rpc_url`. Two responsibilities
  (dispatch and validation) sit in two functions, not bundled in one.
- The loopback check uses documented `urlsplit(...).hostname` semantics.
  **CVE-2024-11168 is not cited** — it is a separate, unrelated bug
  (research Angle-02 correction).
- Curated ops keep calling `network_config` directly. P0 does **not**
  route them through `_resolve_endpoint` (ADR-008).

**Failure Modes:**
- Unknown network -> `ValueError` from `network_config` (unchanged message).
- Both/neither network mode -> `ValueError` with explicit usage hint.
- Bad URL scheme / non-loopback HTTP -> `ValueError` from `_validate_rpc_url`.

All caught by `main`'s existing `except (ValueError, RPCError)` block —
no new exception types.

---

### Module: `param_ingest` (new)

**Section sentinel:**

```python
# === MODULE: param_ingest ===
# Public:  _parse_params(raw, *, stdin=sys.stdin) -> list
# === END MODULE: param_ingest ===
```

**Responsibility:** Parse the `--params` CLI string (inline JSON or `-`
to read stdin) into a Python `list`. Enforce "top-level is a JSON array".

**Public API:**

| Symbol | Signature | Description |
|---|---|---|
| `_parse_params(raw: str, *, stdin=sys.stdin) -> list` | raw `--params` value; injectable stdin | If `raw == "-"`, read all of `stdin` and parse as JSON. Else parse `raw` as JSON. Reject if top-level is not a list. Raise `ValueError` with a clear message on type mismatch or parse failure. |

**Proposed internal structure:**

```python
import sys as _sys  # already imported at module top; shown for context.

def _parse_params(raw, *, stdin=_sys.stdin):
    """Parse --params (inline or stdin) into a list, or raise ValueError."""
    if raw == "-":
        raw = stdin.read()
    try:
        value = json.loads(raw)
    except ValueError as e:
        raise ValueError("--params must be a JSON array: %s" % e)
    if not isinstance(value, list):
        raise ValueError(
            "--params must be a JSON array (got %s)" % type(value).__name__
        )
    return value
```

**Key Design Decisions:**
- Stdin is **injected** as a keyword argument (default `sys.stdin`). Tests
  pass `io.StringIO("[...]")`. This keeps `_parse_params` pure-function
  testable with no `mock.patch(sys.stdin)`.
- The JSON-array shape rule lives here (the input contract for the
  ingestion layer), not in `do_call` (which receives an already-typed
  `params: list`). `do_call` is a library-callable function and may
  assume its arguments are well-typed. The CLI layer is the contract
  layer.
- Extracting this as a helper (vs. inlining in `main`) is justified by
  test isolation: two failure modes (parse error, non-array type) plus
  stdin behaviour = three focused unit tests, none of which need argparse.
- Forward-shape note: a P1 `--params @file` extension adds one branch
  (`raw.startswith("@") -> open(raw[1:]).read()`) and an `opener=open`
  kwarg. No call-site change. Recorded in Open Questions.

**Failure Modes:**
- Malformed JSON -> `ValueError("--params must be a JSON array: ...")`.
- Non-list top-level (object, string, number, null) -> `ValueError("--params must be a JSON array (got <type>)")`.

---

### Module: `do_call` (new)

**Section sentinel:**

```python
# === MODULE: do_call ===
# Public:  do_call(url, *, method, params, allow_write=False,
#                  timeout=15, rpc=rpc_call) -> Any
# === END MODULE: do_call ===
```

**Responsibility:** Forward a JSON-RPC request to a pre-resolved
endpoint URL and return the raw `result`, subject to the read-only
denylist.

**Domain Entities:**
- A *call* = (url, method, params, allow-write flag, timeout).
- The *result* is any JSON value the node returns; this function does
  not interpret it.

**Data Store:** none.

**Public API:**

| Signature | Description |
|---|---|
| `do_call(url, *, method, params, allow_write=False, timeout=15, rpc=rpc_call)` | Returns the JSON-RPC `result` verbatim. Raises `ValueError` on bad input or denied method; raises `RPCError` on transport / JSON-RPC error. |

**Proposed internal structure:**

```python
def do_call(url, *, method, params, allow_write=False,
            timeout=15, rpc=rpc_call):
    """Generic eth_* read passthrough. Returns raw JSON-RPC result."""
    if not isinstance(method, str) or not method:
        raise ValueError("--method is required")
    if not isinstance(params, list):
        raise ValueError("--params must be a JSON array")
    if not allow_write:
        if method in _DENY_METHODS:
            raise ValueError(
                "method %s refused (use the 'broadcast' op for sends, "
                "or pass --allow-write to override)" % method
            )
        if method.startswith(_DENY_PREFIXES):
            raise ValueError(
                "method %s in a sensitive namespace; pass --allow-write "
                "to override" % method
            )
    return rpc(url, method, params, timeout=timeout)
```

Total: ~14 lines including signatures and messages.

**Key Design Decisions:**
- **Signature accepts URL only, not the network-selection trio.** Endpoint
  resolution is `main`'s job. `do_call`'s public contract is the smallest
  library-shaped surface: one URL, one method, one params list. This
  fixes the "`chain_id` is accepted but never used by `do_call` itself"
  smell in the candidate designs — `chain_id` is consumed by
  `_resolve_endpoint` and discarded; `do_call` never sees it.
- **Returns the raw `result`, not a wrapper dict.** Preserves the P0
  contract ("the shape is whatever the node returned") and keeps stdout
  pipe-friendly for `jq` / `tee`. Stdout formatting
  (`json.dumps(..., indent=2)`) lives in `main`, mirroring the curated ops.
- **Denylist check happens before the network call.** Ordering is:
  validate inputs -> check policy -> `rpc(...)`. The `rpc` invocation is
  the only outbound I/O; if policy rejects, no packet leaves the host.
  ADR-004 (denylist in `do_call`, not `main`) protects library callers
  too.
- **`rpc=rpc_call` injected default** matches `do_balance` /
  `do_broadcast` exactly. Every test runs with a fake `rpc`.
- **Argument validation lives here, not only in `main`.** Library callers
  (`import eth_rpc; eth_rpc.do_call(...)`) get the same guarantee. This
  is the "argparse stops at `main`" principle applied: `do_call` must be
  safe to call with arbitrary Python values.
- **Keyword-only after `url`.** Prevents accidental positional drift if
  the signature grows (e.g. future `strict=False` for `--read-only-strict`).
- **`allow_write` is a single boolean.** No fine-grained per-namespace
  opt-in. PRD-aligned and audit-friendly.

**Failure Modes:**
- Bad inputs -> `ValueError` (caught by `main`).
- Denied method without bypass -> `ValueError` naming the method and
  pointing at `broadcast` / the offline signer / `--allow-write`.
- Transport / JSON-RPC error response -> `RPCError` from `rpc_call`
  (caught by `main`).

**P1 extension shape (not implemented in P0):** an optional
`strict=False` kwarg, plus `allowlist: frozenset[str] | None = None`,
implements `--read-only-strict` without changing existing call sites
(mutual exclusion with `allow_write` enforced at argparse layer per
ADR-005). A `decode=False` kwarg + a `_decode_result(method, result)`
helper lands either in `main` (post-call hook) or as a tiny `formatter`
section. Recorded in Open Questions.

---

### Module: `main` (modified)

**Section sentinel:** `main` already exists; the new `call` branch is
inside the existing dispatch.

**Responsibility:** Parse argv, resolve the endpoint, parse params,
dispatch to `do_*`, format stdout JSON, translate exceptions to exit code
1, narrate stderr (warnings, errors).

**Diff scope:**

1. Add the `call` subparser:
   ```python
   p_call = sub.add_parser(
       "call",
       help="generic eth_* JSON-RPC read passthrough",
   )
   p_call.add_argument("--network", choices=sorted(NETWORKS))
   p_call.add_argument("--rpc-url")
   p_call.add_argument("--chain-id", type=int)
   p_call.add_argument("--method", required=True)
   p_call.add_argument(
       "--params",
       required=True,
       help="JSON array; pass '-' to read from stdin",
   )
   p_call.add_argument("--allow-write", action="store_true")
   p_call.add_argument("--timeout", type=int, default=15)
   ```
   (Note: `--network` is *not* `required=True` here, because the
   custom-endpoint path is the alternative. Mutual-exclusion is enforced
   in `_resolve_endpoint`, not argparse.)

2. Add a `call` branch in the dispatch block:
   ```python
   elif args.command == "call":
       params = _parse_params(args.params)
       chain_id, url = _resolve_endpoint(
           network=args.network,
           rpc_url=args.rpc_url,
           chain_id=args.chain_id,
       )
       if args.allow_write:
           print(
               "warning: --allow-write bypasses the call denylist",
               file=sys.stderr,
           )
       result = do_call(
           url,
           method=args.method,
           params=params,
           allow_write=args.allow_write,
           timeout=args.timeout,
       )
       print(json.dumps(result, indent=2))
   ```
   The existing `except (ValueError, RPCError)` block catches everything,
   so the stderr warning prints before the exception path is taken — its
   contract is "always print on bypass", regardless of whether the call
   later fails. `chain_id` is unpacked but unused inside the `call`
   branch (it is part of the resolver's return contract for symmetry with
   the curated ops; a future P1 `--decode` may need it).

3. Stdout formatting is the existing `print(json.dumps(result, indent=2))`
   call — `result` is the raw JSON-RPC `result`, so `json.dumps` handles
   dict / list / str / int / None / bool uniformly. No change to the
   output path.

**Key Design Decisions:**
- **Three small helpers, not one big inline block.** `_parse_params`,
  `_resolve_endpoint`, and `do_call` are called in sequence. `main`'s
  `call` branch is glue — five lines of dispatch — symmetric across the
  three ops.
- **The `--allow-write` stderr warning lives in `main`.** This narration
  is a CLI concern, not a policy decision (`policy` returns silently;
  `cli` narrates the bypass). Keeps `do_call` side-effect-free.
- Use the existing combined `except (ValueError, RPCError)` block. No
  new exception class, no new branch.
- Keep `balance` / `broadcast` subparsers and dispatch untouched.

**Failure Modes:** unchanged — `error: <msg>` to stderr, exit 1. Now
covers the new `ValueError` cases above (parse failure, mutual-exclusion
failure, URL scheme failure, denylist refusal) and the `RPCError`
propagation case.

---

## Cross-Cutting Concerns

### Authentication & Authorization

N/A at the skill level — the skill is a local CLI; no auth flows.
Out-of-band: `--rpc-url` could carry basic-auth credentials in the
userinfo component — these are **never logged** (the skill prints no
URL on the happy path, and error messages reference the *method*, not
the URL). Unchanged from the existing curated ops.

### Logging & Observability

Stdout is reserved for the JSON-RPC `result` (`json.dumps(..., indent=2)`).
Stderr is reserved for:

- The `error: ...` line on any failure (existing pattern).
- The `warning: --allow-write bypasses the call denylist` line when
  `--allow-write` is passed (new; one line; printed before dispatch).

No structured logging, no log file, no metrics. The skill is a one-shot
CLI; the operator's terminal *is* the observability surface. If the
skill ever grows long-running flows, the existing `eth-signer-mcp`
`internal/obs` package is the reference pattern.

**Discipline:** modules other than `main` must not print to stdout or
stderr. `_parse_params`, `_resolve_endpoint`, `_validate_rpc_url`, and
`do_call` either return values or raise. Lint expectation:
`grep -nE '^\s*print\(' eth_rpc.py` should show prints only in `main`'s
dispatch.

### Error Handling

One unified pattern, unchanged:

1. Domain functions raise `ValueError` (input / policy) or `RPCError`
   (transport / JSON-RPC).
2. `main` catches both, prints `error: <message>` to stderr, returns 1.
3. CLI smoke tests pin the exit code.

The denylist refusal is a `ValueError`. The `--allow-write` warning is a
stderr `print(...)`, **not** an exception — bypass is not an error.

### Configuration

Operator-supplied via CLI flags only. No env vars, no config files. The
`NETWORKS` map is the only source of named-network truth; `--rpc-url` /
`--chain-id` is the documented escape hatch.

### Dependency Injection (testability)

A single convention across modules: any function that touches the
outside world takes its escape hatch as a keyword argument with a
sensible default.

| Seam | Default | Test override |
|---|---|---|
| `do_balance` / `do_broadcast` / `do_call` transport | `rpc=rpc_call` | `rpc=make_fake_rpc({...})` |
| `_parse_params` stdin | `stdin=sys.stdin` | `stdin=io.StringIO("[...]")` |
| `do_broadcast` time | `sleep=time.sleep`, `now=time.time` | injected fakes (existing) |

This is **the** mechanism that keeps the test suite I/O-free.

### Resource bounds

No new bounds in P0. `rpc_call`'s `timeout` is forwarded (default 15s,
overridable with `--timeout`). Large bodies are bounded by the upstream
node; the PRD explicitly defers chunking/pagination to a future iteration
(see Open Questions for the `--max-body-bytes` follow-up).

---

## Data Flow Diagrams

### Flow 1 — happy path (named network)

```text
Operator: python3 eth_rpc.py call --network hoodi \
            --method eth_blockNumber --params '[]'

argv --> main
         |-> argparse builds Namespace
         |-> params = _parse_params('[]')          -> []
         |-> _resolve_endpoint(network='hoodi')    -> (560048, 'https://...')
         |-> do_call('https://...', method='eth_blockNumber',
         |           params=[], allow_write=False, timeout=15)
         |     |-> method type/length OK; params is list OK
         |     |-> method not in _DENY_METHODS, no prefix match
         |     `-> rpc('https://...', 'eth_blockNumber', [], timeout=15)
         |           |-> urllib.request POST  -> JSON-RPC response
         |           `-> body['result'] = '0x4e3a01'
         |-> result = '0x4e3a01'
         `-> print(json.dumps('0x4e3a01', indent=2))  -> stdout
             return 0
```

### Flow 2 — denied method (no bypass)

```text
Operator: python3 eth_rpc.py call --network hoodi \
            --method eth_sendRawTransaction --params '["0x..."]'

main
 |-> params = _parse_params('["0x..."]')      -> ['0x...']
 |-> _resolve_endpoint(network='hoodi')        -> (560048, 'https://...')
 `-> do_call(url, method='eth_sendRawTransaction', allow_write=False, ...)
       `-> method in _DENY_METHODS -> raise ValueError(
            "method eth_sendRawTransaction refused (use the 'broadcast' op ...)")

main caught -> stderr: "error: method eth_sendRawTransaction refused ..."
               return 1
```

The denial happens **before** any network call: `_DENY_METHODS` /
`_DENY_PREFIXES` check is the second step of `do_call`, ordered before
`rpc(...)`. The `rpc` invocation is the only outbound I/O; if policy
rejects, we guarantee no packet leaves the host.

### Flow 3 — custom endpoint with stdin params

```text
Operator: cat filter.json | python3 eth_rpc.py call \
            --rpc-url http://127.0.0.1:8545 --chain-id 31337 \
            --method eth_getLogs --params -

main
 |-> params = _parse_params('-', stdin=sys.stdin)
 |     |-> raw = sys.stdin.read()
 |     `-> json.loads(raw) -> [filter-object]
 |-> _resolve_endpoint(rpc_url='http://127.0.0.1:8545', chain_id=31337)
 |     |-> network is None and both rpc_url/chain_id present -> OK
 |     |-> _validate_rpc_url('http://127.0.0.1:8545')
 |     |     `-> scheme='http', hostname='127.0.0.1' in _LOOPBACK_HOSTS -> OK
 |     `-> return (31337, 'http://127.0.0.1:8545')
 `-> do_call('http://127.0.0.1:8545', method='eth_getLogs',
              params=[filter], ...)
       `-> rpc(...) -> array of logs

stdout: pretty-printed array of logs
return 0
```

### Flow 4 — `--allow-write` bypass

```text
Operator: python3 eth_rpc.py call --network hoodi --allow-write \
            --method eth_sendRawTransaction --params '["0x..."]'

main
 |-> params = _parse_params(...)
 |-> _resolve_endpoint(network='hoodi') -> (560048, 'https://...')
 |-> args.allow_write is True
 |-> stderr: "warning: --allow-write bypasses the call denylist"
 |-> do_call(url, ..., allow_write=True)
 |     |-> denylist skipped (allow_write=True)
 |     `-> rpc('https://...', 'eth_sendRawTransaction', ['0x...'], timeout=15)
 |           -> tx hash '0xabc...'
 `-> stdout: '"0xabc..."'  (json.dumps of the string result)
     return 0
```

### Flow 5 — existing `balance` / `broadcast` flows (unchanged)

```text
balance:
  main -> do_balance(network, address, rpc=rpc_call)
       -> network_config(network) -> (chain_id, url)
       -> validate_hex_address(address)
       -> rpc(url, 'eth_getBalance', [address, 'latest'])
       -> wei_to_eth_str(parse_hex_int(...))
       -> {network, chainId, address, blockTag, balanceWei, balanceEth}

broadcast:
  main -> do_broadcast(network, raw_tx, wait=..., rpc=rpc_call,
                       sleep=..., now=...)
       -> network_config(network) -> (chain_id, url)
       -> validate_raw_tx(raw_tx)
       -> rpc(url, 'eth_sendRawTransaction', [raw_tx]) -> tx_hash
       -> if wait: poll rpc(url, 'eth_getTransactionReceipt', [tx_hash])
       -> {network, chainId, txHash, status, [blockNumber, gasUsed, ...]}
```

No change to either flow under P0.

---

## Infrastructure & Deployment

### Deployment Model

- The skill is checked into `.claude/skills/eth-rpc/` in this monorepo.
  Single Python file plus its test file, both stdlib-only. No build, no
  package, no entry-point wiring.
- The skill is invoked by Claude Code with `python3 eth_rpc.py <op> ...`.
- The Go monorepo (`apps/` + `libs/`) does **not** consume the skill at
  runtime; the only intersection is documentation (`README` cross-links).
- Tests run as `cd .claude/skills/eth-rpc && python3 -m unittest
  test_eth_rpc -v` (per PRD §Milestones Phase 1 step 6).

### Scaling Strategy

The skill is a one-shot CLI; there is no scaling axis. Each invocation
performs at most one outbound RPC. The `--timeout` flag bounds wall time;
concurrency is the operator's responsibility (`xargs -P` etc.). For load
(CI), the existing 15-second default is sufficient.

### Service Extraction Path

The PRD pins single-file. The extraction question reduces to: *"if
`eth_rpc.py` outgrows a single file, what does the split look like?"*
The sentinel comments make that a mechanical operation:

| Section | Future package path | Extraction readiness |
|---|---|---|
| `constants` | `eth_rpc/constants.py` | **Keep together with consumers** — trivially small; no reason to split. |
| `network_config` + `endpoint_resolution` | `eth_rpc/endpoint.py` | **Ready now** — together they form a "resolve an endpoint" lib; both pure. |
| `validators` | `eth_rpc/validators.py` | **Ready now** — pure regex + string predicates. |
| `formatting` | `eth_rpc/formatting.py` | **Ready now** — pure decimal math. |
| `rpc_transport` | `eth_rpc/rpc.py` | **Ready now** — one function + one exception, stdlib only. |
| `param_ingest` | `eth_rpc/params.py` | **Ready now** — one function; `stdin` is injectable. |
| `do_balance` / `do_broadcast` / `do_call` | `eth_rpc/ops/{balance,broadcast,call}.py` | **Ready now** — depend only on leaves through their declared signatures. |
| `main` | `eth_rpc/cli.py` / `__main__.py` | **Keep here** — argparse + stdio formatting; only meaningful in a CLI context. A future MCP / HTTP consumer skips `main`. |

**Concrete extraction recipe (for reference, not in this PR):**

1. Create `libs/eth-rpc-core/` (Python package or, for cross-language
   reuse, a Go module mirroring the API).
2. For Python: move each sentinel-bounded section into a sibling module
   file. Strip the leading underscore from now-public names
   (`_resolve_endpoint -> resolve_endpoint`, `_parse_params -> parse_params`,
   `_validate_rpc_url -> validate_rpc_url`, `_check_method_policy` if
   factored).
3. The skill's `eth_rpc.py` becomes a thin shim:
   ```python
   from eth_rpc_core import do_balance, do_broadcast, do_call, ...
   def main(argv=None): ...   # CLI stays in the skill
   ```
4. A second consumer (Go app, second skill) imports the same library.

Because every section already takes its inputs as plain values and
returns plain values, **no signature changes are needed during the
extraction**. The argparse-stops-at-`main` principle is what makes this
mechanical.

---

## Technology Choices

| Concern | Choice | Rationale |
|---|---|---|
| Language | Python 3 (>=3.9 for `frozenset` literal syntax + `tuple` of strings) | Matches the existing skill and sibling `eth-tx-builder`. |
| File layout | Single file (P0); sentinel-bounded sections | PRD Open Q7. Sentinels give modularity at zero packaging cost. |
| Runtime deps | **stdlib only** (`argparse`, `json`, `re`, `sys`, `time`, `urllib.request`, **new: `urllib.parse`**) | PRD non-functional requirement; matches house style. |
| HTTP transport | `urllib.request` via existing `rpc_call` | Reused as-is; custom `User-Agent` already in place (research Angle-03). |
| URL parsing | `urllib.parse.urlsplit` | Documented hostname normalization (research Angle-02). The one new import; on the PRD's allowed list. |
| Param parsing | `json.loads` | Stdlib. Matches existing usage. |
| CLI | `argparse` with subparsers | Already in the file. |
| Test framework | `unittest` + `unittest.mock` | Already in use; zero new deps. |
| API style for `call` | JSON-RPC 2.0 passthrough | The whole point of the op. |
| Output format | `json.dumps(result, indent=2)` | Matches existing ops; pipeable into `jq`. |
| Error model | `error: <msg>` on stderr, exit 1 | Matches existing ops; testable via `subprocess`. |

The single new import is `import urllib.parse` (for `urlsplit`). This
satisfies PRD §FR P0.10 ("Stdlib only. No new imports beyond what's
already in `eth_rpc.py` ... plus `urllib.parse` for the new URL-scheme
check").

---

## ADRs (Architecture Decision Records)

### ADR-001: Single-file skill layout retained, with sentinel-bounded sections

- **Status:** Accepted (confirms PRD Open Q7).
- **Context:** P0 adds <~150 LOC of code + new tests to the existing
  `eth_rpc.py`. A natural temptation is to split into an `eth_rpc/`
  package with `cli.py`, `call.py`, `transport.py`, etc.
- **Decision:** Stay single-file. Each "module" is a named section
  bracketed by `# === MODULE: <name> ===` /
  `# === END MODULE: <name> ===` sentinels. Treat each section as an
  in-file module with a narrow public surface and underscore-prefixed
  private helpers.
- **Alternatives Considered:**
  - `eth_rpc/` package with per-op modules — costs an `__init__.py`,
    explicit relative imports, and breaks `python3 eth_rpc.py` direct
    invocation (would need a `__main__.py` shim).
  - Leave structure implicit, document conventions in prose only —
    sentinels make boundary violations greppable and reviewable;
    conventions in prose drift.
  - `apps/eth-rpc-cli/` Go module — overkill; the skill is stdlib
    Python by design for fast iteration inside Claude Code.
- **Consequences:** The codebase stays at one file the operator can
  read end-to-end; modules exist physically (as sections) but not as
  packages. Reviewers grep for sentinels to check boundaries. CI can
  later add a one-line lint that no underscore symbol from another
  region is referenced; out of scope for P0.

### ADR-002: Argparse stops at `main` — domain functions take plain kwargs

- **Status:** Accepted.
- **Context:** Many CLI tools pass `args` (an `argparse.Namespace`) into
  helper functions verbatim. This couples helpers to argparse and makes
  them harder to reuse from non-CLI consumers (a future skill, MCP tool,
  Go re-implementation).
- **Decision:** `main` decomposes `args` into named kwargs before
  calling `_resolve_endpoint`, `_parse_params`, and `do_call`. Each
  helper's signature is the contract a `libs/` consumer would see.
- **Alternatives Considered:** Pass `args` through. Rejected — couples
  the "library" core to argparse and to the specific subparser layout.
- **Consequences:** `do_call` is callable from any Python context with
  no argparse import. This is the single non-negotiable
  extraction-readiness property.

### ADR-003: `do_call` returns the raw JSON-RPC `result`, not a wrapper dict

- **Status:** Accepted.
- **Context:** `do_balance` and `do_broadcast` return wrapped dicts with
  derived fields (`balanceEth`, `status`). The new `do_call` could
  similarly wrap (`{network, chainId, method, result}`).
- **Decision:** Return the raw `result` (whatever JSON value the node
  returned).
- **Alternatives Considered:**
  - Wrap with `{network, method, result}` for parity with curated ops.
    Rejected — breaks `jq` / `tee` pipelines; loses scalar/array
    fidelity for `eth_blockNumber` / `eth_getLogs`.
  - Optional `--wrap` flag. Rejected — slippery slope toward `--decode`
    which is a P1 design.
- **Consequences:**
  - Matches the PRD's P0 contract ("the shape is whatever the node
    returned"); pipes cleanly into `jq`.
  - Library callers do not need to unwrap.
  - `ops.call` differs from `ops.balance` / `ops.broadcast` in
    return-shape convention. That's correct: `call` is a passthrough,
    the others are curated UX.

### ADR-004: Denylist lives in `do_call`, not in `main`

- **Status:** Accepted.
- **Context:** The denylist could live in `main` (parse arg -> check ->
  dispatch) or in `do_call`.
- **Decision:** `do_call`. `main` just forwards `allow_write` and the
  parsed `params`.
- **Alternatives Considered:** check in `main` only.
- **Consequences:**
  - Library callers (`import eth_rpc; eth_rpc.do_call(...)`) get the
    same guarantee as CLI callers — no surprise bypass.
  - Denylist tests are pure-Python unit tests against `do_call`, not
    `subprocess` smoke tests against `main`.
  - The cost is the `allow_write` flag on `do_call`'s signature; ~10
    chars in the call sites.

### ADR-005: Mutual-exclusion of `--network` vs `--rpc-url`+`--chain-id` enforced in `_resolve_endpoint`, not argparse

- **Status:** Accepted.
- **Context:** `argparse.add_mutually_exclusive_group()` does not model
  "A alone, OR (B AND C) together". The rule needs hand-written
  validation.
- **Decision:** Validation lives in `_resolve_endpoint`. Argparse marks
  none of the three flags as required; `_resolve_endpoint` raises
  `ValueError` if the combination is invalid.
- **Alternatives Considered:**
  - Two subcommands (`call`, `call-url`). Bad UX; doubles documentation.
  - Validation in `main` post-`parse_args`. Splits the rule between two
    sites and complicates library use.
- **Consequences:** The same `ValueError`-and-stop path in `main`
  surfaces a clean error: `error: --rpc-url and --chain-id are required
  together`. Tested by both unit tests (calling `_resolve_endpoint`
  directly) and CLI tests (driving `main`).

### ADR-006: `_parse_params` is a separate helper with injectable stdin

- **Status:** Accepted.
- **Context:** A first-pass design inlined `--params` parsing in `main`.
  That works but has three failure modes (parse error, non-array,
  stdin-empty) plus a stdin path that requires either patching
  `sys.stdin` in tests or a `subprocess` CLI test for each case.
- **Decision:** Extract `_parse_params(raw, *, stdin=sys.stdin) -> list`
  with stdin injection. `main` calls it once; tests exercise its three
  failure modes directly. The call branch in `main` stays symmetric
  with the other ops: `params = _parse_params(args.params)`.
- **Alternatives Considered:**
  - Inline in `main` (P0 candidate's original choice). Rejected —
    breaks `main`'s symmetry across ops and forces `mock.patch(sys.stdin)`
    or `subprocess` for one of three failure cases.
  - Put the JSON-array shape check in `_parse_params` *and* in
    `do_call`. Accepted as light defense-in-depth: `_parse_params`
    rejects with a CLI-friendly message; `do_call` rejects with a
    library-callable-friendly message. The duplication is one
    `isinstance(value, list)` check at each layer; intentional.
- **Consequences:** One new section, three new tests, symmetric
  composition in `main`. Also future-shapes the P1 `--params @file`
  extension (one branch + an `opener=open` kwarg) and the P2 batch
  variant (returns a list of `{method, params}` objects).

### ADR-007: Loopback IPv6 check via `urllib.parse.SplitResult.hostname`, not via CVE-2024-11168

- **Status:** Accepted (incorporates research Angle-02 correction).
- **Context:** A first-pass design might justify the
  `hostname == "::1"` comparison as "defensive against CVE-2024-11168".
  Angle 02 verified this is a non-sequitur: bracket-stripping and host
  lowercasing in `SplitResult.hostname` are long-standing documented
  `urllib.parse` behaviors, not patches for that CVE.
- **Decision:** Cite the documented `SplitResult.hostname` semantics in
  comments and SKILL.md; do not invoke CVE-2024-11168 anywhere in the
  codebase or docs.
- **Consequences:** The validation logic is unchanged; the rationale is
  factually correct.

### ADR-008: No refactor of curated ops onto `_resolve_endpoint` in P0

- **Status:** Accepted (defers a tempting clean-up).
- **Context:** Once `_resolve_endpoint` exists, `do_balance` and
  `do_broadcast` could route through it for symmetry.
- **Decision:** Do not refactor in P0. Curated ops keep calling
  `network_config` directly.
- **Alternatives Considered:** refactor both curated ops; update their
  existing tests to pass arguments through the resolver.
- **Consequences:**
  - **Pro:** P0 diff stays minimal; existing tests untouched; risk of
    breaking the curated paths is zero.
  - **Con:** Mild duplication of "named-network resolution" between the
    curated ops and the resolver. The resolver itself contains no
    duplication — `_resolve_endpoint` calls `network_config` for the
    named path.
  - The refactor is a clean follow-up issue under PRD Phase 2.

### ADR-009: Stderr warning is `print(..., file=sys.stderr)`, not `warnings.warn`

- **Status:** Accepted.
- **Context:** Python has `warnings.warn` and a logging module; the
  skill could use either.
- **Decision:** `print("warning: ...", file=sys.stderr)`. Matches the
  existing `error: ...` pattern.
- **Alternatives Considered:** `warnings.warn` (configurable filters,
  category subclassing). `logging.warning` (handlers, formatters).
- **Consequences:** Zero new imports. Operators get a stable, scriptable
  stderr line. Tests assert the literal string.

### ADR-010: Future P1 mutual-exclusion of `--allow-write` vs `--read-only-strict` lives in argparse

- **Status:** Accepted (forward-shape; not implemented in P0).
- **Context:** PRD P1.3 explicitly calls `--read-only-strict` and
  `--allow-write` mutually exclusive. Enforcing the combination in
  `do_call` would couple it to two CLI flags.
- **Decision:** Use `argparse.add_mutually_exclusive_group()` for these
  two flags. `do_call` (in P1) will accept both as orthogonal kwargs
  and trust the caller; argparse rejects the invalid combination
  before any helper sees it.
- **Alternatives Considered:** Check in `do_call`. Rejected — couples
  the policy function to CLI semantics; complicates per-flag tests.
- **Consequences:** `do_call` stays a pure decision function (modulo
  the network call); CLI surfaces the invalid combination with a clear
  argparse error before any module sees it. Recorded here so the P1 PR
  is mechanical.

### ADR-011: Drift protection for the denylist via `TestDenylistContents`

- **Status:** Accepted.
- **Context:** `_DENY_METHODS` and `_DENY_PREFIXES` are sized in source
  code. A silent edit (e.g. removing `eth_sign` accidentally during a
  refactor) would weaken the security posture with no test failure.
- **Decision:** Add a one-class test `TestDenylistContents` with two
  assertions: `_DENY_METHODS == frozenset({...})` literal equal to the
  PRD-enumerated set; `_DENY_PREFIXES == ("personal_", "admin_",
  "miner_", "engine_", "clique_")` literal equal to the PRD-enumerated
  tuple. The tests double as documentation.
- **Alternatives Considered:** Trust code review only. Rejected — the
  set is the security boundary; an automated catch costs two asserts.
- **Consequences:** Any PR that modifies the denylist must update both
  the constants and the test, surfacing the change in review.

### ADR-012: Batch passthrough — policy, ids, return shape

- **Status:** Accepted. Date: 2026-06-14.
- **Context:** Issue 3.3 adds a `batch` subcommand that sends a JSON-RPC
  array request. Four design questions must be resolved before code lands
  (project-plan §Phase 3 R7 — "Batch + denylist interaction permits
  hidden writes"):
  (a) When does the denylist fire — per-batched-call or as an up-front
  pre-scan?
  (b) Is `--allow-write` per-call or batch-global?
  (c) What does `do_batch` return when some calls succeed and others
  return JSON-RPC errors from the server?
  (d) How are request ids allocated?
- **Decision:**
  - **Policy application:** per-call denylist check inside `do_batch`,
    implemented by calling the shared `_check_method_policy` helper
    (extracted in Issue 3.2) for each entry before any wire egress.
    A refused entry is recorded as a synthetic error envelope at its
    original positional index; the remaining allowed entries are
    forwarded to the node in a single batch POST.
  - **`--allow-write` scope:** batch-global. One flag at the batch
    level bypasses `_check_method_policy` for every entry uniformly;
    the existing loud stderr warning prints exactly once per invocation.
  - **Return shape on partial failure:** `do_batch` always returns the
    envelope list and never raises mid-batch when transport succeeds.
    Each entry in the list is either `{"id": int, "result": Any}` (node
    success) or `{"id": int, "error": {"code": int, "message": str}}`
    (node error or skill refusal). A transport-level failure (network
    error, malformed response) still raises `RPCError` exactly as
    `rpc_call` does.
  - **Id allocation:** positional index (`0..N-1`). The operator input
    shape is `[{"method": str, "params": list}, ...]` — no `id`, no
    `jsonrpc`; those are injected by `do_batch`. Output envelopes carry
    the same positional id so `jq '.[2].error'` is stable regardless of
    server response order (servers may return the array out of order;
    `do_batch` re-sorts by id before merging).
  - **Worked example:**

    Input to `--calls`:
    ```json
    [
      {"method": "eth_chainId",    "params": []},
      {"method": "eth_blockNumber", "params": []}
    ]
    ```

    Wire payload sent (`[{"jsonrpc":"2.0","id":0,...},{"jsonrpc":"2.0","id":1,...}]`).

    Output printed to stdout:
    ```json
    [
      {"id": 0, "result": "0x88bb0"},
      {"id": 1, "result": "0x2df761"}
    ]
    ```

    If entry 0 were `eth_sendRawTransaction` (denied) without `--allow-write`:
    ```json
    [
      {"id": 0, "error": {"code": -32601, "message": "method eth_sendRawTransaction refused ..."}},
      {"id": 1, "result": "0x2df761"}
    ]
    ```
  - **Cross-references:** ADR-004 (denylist in `do_call`); the
    `_check_method_policy` refactor (Issue 3.2) ensures the same policy
    is enforced in both `do_call` and `do_batch`. ADR-005
    (`_resolve_endpoint`) is reused by the `batch` subparser unchanged.
    Open Q5 (`txpool_*` documentation) applies: batch entries using
    undocumented namespaces pass through silently if not on the denylist.
- **Alternatives Considered:**
  - Up-front pre-scan of all methods, then abort the whole batch if any
    is denied. Rejected — positional integrity is lost and the operator
    cannot distinguish "node refused" from "skill refused" without
    re-running individual calls.
  - Per-call `--allow-write` flag (e.g. `allow_write: true` inside each
    entry object). Rejected — complicates the input shape with a
    security-bypass field, breaks the simple "one flag overrides all"
    mental model, and makes the warning difficult to count correctly.
  - Raise `RPCError` on first denied entry instead of continuing.
    Rejected — breaks partial-success use-cases where the operator mixes
    reads and (explicitly allowed) writes in one batch.
- **Consequences:**
  - `do_batch` receives the full envelope list including synthetic
    refusals; the operator can inspect each by index without extra RPC
    round-trips.
  - The `--allow-write` stderr warning is printed exactly once per
    `main` invocation even if many entries would have been refused.
  - Server responses that return a single JSON-RPC error object for the
    whole batch (e.g. "batch too large") are surfaced as `RPCError`
    (not as a partial-result list), consistent with `rpc_call`.
  - Issue 3.2's `_check_method_policy` extraction is a prerequisite for
    this ADR's enforcement point to be in one place.

### ADR-013: `--max-body-bytes` uses bounded `resp.read(limit + 1)`

- **Status:** Accepted. Date: 2026-06-14.
- **Context:** `rpc_call` calls `resp.read()` with no size cap (Open Q6,
  project-plan §Phase 3 Task 3.3). A wide-range `eth_getLogs` can return
  tens of megabytes and stall or OOM the Python process. Two options:
  - **Option A — bounded `resp.read(limit + 1)`:** Read up to `limit + 1`
    bytes; if the buffer is exactly `limit + 1` bytes raise
    `RPCError("response exceeds --max-body-bytes (limit was N)")`. Simple,
    stdlib-only, six lines.
  - **Option B — chunked reader with running size:** Loop `resp.read(8 <<
    10)` until EOF or the running total exceeds the limit. Slightly more
    code; same memory ceiling; matches `MaxBytesHandler` in eth-signer-mcp.
- **Decision:** Option A. Rationale: `urllib.request.urlopen` / stdlib
  `http.client` buffers the entire response body internally on most TCP
  connections before `resp.read()` is called, so the "early-exit before
  download completes" benefit of chunked reading is largely illusory at
  this layer. Option A is mechanically obvious in code review and six lines.
  Note: `Transfer-Encoding: chunked` is handled transparently by stdlib;
  the behaviour is the same regardless of whether the server uses chunked
  encoding.
- **Behaviour contract:**
  - **Default value:** `None` (unset). When `max_body_bytes is None`,
    `rpc_call` / `rpc_batch` call `resp.read()` unchanged — existing
    behaviour is preserved bit-for-bit (project-plan R8 mitigation).
  - **Kwarg name:** `max_body_bytes` in Python; `--max-body-bytes` on
    the CLI. Propagation path: CLI flag → `args.max_body_bytes` → `main`
    → `do_call(max_body_bytes=...)` / `do_batch(max_body_bytes=...)` →
    `rpc(url, method, params, timeout=..., max_body_bytes=...)`.
  - **Error message:** `"response exceeds --max-body-bytes (limit was N
    bytes)"` where N is the limit value.
  - **Boundary semantics:** a body of exactly N bytes succeeds; a body of
    N+1 bytes raises. The read returns `limit + 1` bytes if available to
    allow the check to fire.
  - **Interaction with `--timeout`:** orthogonal. Both can fire
    independently; whichever limit is hit first raises its error.
- **Alternatives Considered:** Option B (chunked reader). Rejected —
  same memory ceiling in practice; more code; harder to review; see
  context above.
- **Consequences:**
  - Existing `TestRpcCall` tests pass unchanged (default `None` path
    unchanged).
  - `rpc_call` and `rpc_batch` branch explicitly on `max_body_bytes is
    None`; the `None` path keeps one `resp.read()` call.
  - CLI flag is default-off so existing operators are unaffected until
    they opt in.
  - Open Q6 is closed by this ADR + Issue 3.6's implementation.

---

## Open Questions

These are non-blocking for P0; defaults are recorded so review can ratify
or override without re-architecting.

1. **`network_config` migration.** When (if?) the curated ops move to
   `_resolve_endpoint`, do we deprecate `network_config` or keep it as
   a public shim forever?
   *Recommendation:* keep it; it's the cheapest possible
   backward-compatibility hedge and consumers may depend on it.
   Tracked as a P1 follow-up (ADR-008).

2. **Denylist evolution policy.** How does a new sensitive namespace
   (e.g. `txpool_` write methods, a future `validator_*`) get added?
   *Recommendation:* PR review only; update `_DENY_PREFIXES` /
   `_DENY_METHODS` and `TestDenylistContents` in the same commit. No
   env-var override; no config file. Audit surface stays the source
   constant. (Recorded against P0 weakness #7.)

3. **`--decode` (P1) module placement.** Decoded-summary likely lives
   in a new `decode` leaf (per-method dispatch table; the scale-first
   `_DECODERS` registry pattern is a good fit) called by `main`
   *after* `do_call` returns. `do_call` stays raw.
   *Recommendation:* defer the exact shape (registry key names like
   `hex` / `dec`, dispatch vs. inline) to P1 review; the new section
   sits between `do_call` and `main`'s `print(...)` line; no `do_call`
   change.

4. **Custom-endpoint chain-id sanity check.** If the operator passes
   `--rpc-url x --chain-id 99` but the node reports `eth_chainId 1`,
   should `do_call` warn?
   *Recommendation:* no — the operator owns this, and passthrough
   means no extra RPC calls.

5. **`txpool_*` documentation.** The PRD says "allowed but
   undocumented." Should `do_call` log a stderr "this namespace is
   undocumented" warning?
   *Recommendation:* no — keep stdout/stderr clean for piping. The
   silence is the right signal.

6. **Large-body bound for `eth_getLogs`.** P0 forwards verbatim; OOM is
   a pre-existing risk (`rpc_call` loads the full body).
   *Recommendation:* defer a `--max-body-bytes` flag to a P1
   follow-up; document narrow-the-filter guidance in SKILL.md as the
   P0 mitigation. (Recorded against P0 weakness #9.)

7. **`_parse_params` `@file` extension (P1).** Should `--params @-` be
   equivalent to `--params -`?
   *Recommendation:* no — keep them separate to avoid ambiguity; the
   `@` prefix unambiguously means "file path".

8. **`--rpc-url` userinfo handling.** Today the URL is forwarded
   verbatim to `urllib.request` (including any `https://user:pw@host`
   userinfo). The skill never logs the URL on the happy path; error
   messages do not echo it.
   *Recommendation:* keep verbatim; do not strip credentials. Operator
   owns the URL.

---

## Risks

| Risk | Likelihood | Impact | Mitigation |
|---|---|---|---|
| Operator runs a denied write method by accident | Low (denylist default-on) | High (signed tx submitted) | Denylist refusal message names the method; bypass requires explicit `--allow-write`; stderr warning loud on bypass. |
| `--rpc-url` points at a non-loopback HTTP endpoint exposing data in flight | Low | Medium | `_validate_rpc_url` refuses non-loopback HTTP outright. HTTPS only. |
| Operators bypass the denylist accidentally via `--allow-write` in scripts | Low | High | Loud stderr warning on every `--allow-write` invocation (PRD §FR.P0.3). P1 `--read-only-strict` provides the hard guarantee for CI. |
| Large `eth_getLogs` result blocks the terminal | Medium (operator-driven) | Low (slow, not unsafe) | SKILL.md documents narrowing block ranges; no truncation in code (operators may pipe to `jq`). Open Q6 tracks a P1 `--max-body-bytes` follow-up. |
| New denylist rule blocks a method the operator legitimately needs | Low | Low | `--allow-write` is one flag away. No restart, no config change. |
| Denylist silently weakened by accidental constant edit | Low | High | `TestDenylistContents` (ADR-011) asserts literal equality of the constants. |
| Section conventions drift over time (new function lands outside its sentinels) | Medium | Low | Sentinel comments make sections explicit; reviewers grep for boundary violations. CI lint (out of scope for P0) could enforce. |
| Method-policy is a *names* guard only; a node could expose writes behind a non-denied name | Low | Medium | Documented in PRD §Security; `--allow-write` is the explicit bypass. P1 `--read-only-strict` allowlist is the hard guarantee. |
| `urllib.parse.SplitResult.hostname` semantics change between Python versions | Very low | Low | Bracket-stripping and lowercase are documented behavior; the change would surface in `TestValidateRpcUrl`. |
| JSON-RPC response too large for a single in-memory `json.loads` (OOM) | Very low | Medium | `rpc_call` already loads the full body — pre-existing risk, not introduced here. Documented in SKILL.md as "narrow your filter". Open Q6 tracks the follow-up. |
| Stdout pollution by accidental `print` in a helper breaks `jq` pipelines | Low | Medium | Discipline: helpers return values or raise; `print(` outside `main` is a review-blocking smell. |
| Hoodi chainId round-trip not independently verified in research (A5) | Low | Low | Manual e2e (`call --network hoodi --method eth_chainId --params '[]'`) is the confirmation step. PRD Phase 1 step 6. |

---

## Assumptions

The PRD did not need to be supplemented; the research files already
encode the open questions and their recommended defaults. Recorded here
for traceability:

1. **A1 — Single-file layout is final** (PRD Open Q7). The architecture
   commits to this; "module" = sentinel-bounded section of one file.
2. **A2 — `do_call` returns the raw JSON-RPC `result`** (PRD §FR P0.6).
   Confirmed by ADR-003.
3. **A3 — Denylist is a name guard, not a capability guard** (research
   A2). `--allow-write` and `--read-only-strict` (P1) are the two-tier
   guard.
4. **A4 — Loopback HTTP carve-out matches the signer** (research A3).
   Implementation uses documented `SplitResult.hostname` (ADR-007).
5. **A5 — Curated ops are not refactored onto `_resolve_endpoint` in
   P0** (ADR-008). They continue to call `network_config` directly.
6. **A6 — `do_call` accepts the resolved URL, not the network-selection
   trio.** Endpoint resolution is `main`'s job. Keeps `do_call`'s
   library-shaped signature minimal.
7. **A7 — `argparse` for the `call` subparser will not set `--network`
   `required=True`** because the alternative path uses `--rpc-url` +
   `--chain-id`. Mutual-exclusion lives in `_resolve_endpoint` (ADR-005).
8. **A8 — The `--allow-write` stderr warning prints unconditionally on
   bypass**, even if the subsequent `do_call` raises. Matches the
   PRD's "loud one-line stderr warning when bypassed" wording.
9. **A9 — Tests are a privileged consumer of underscore-prefixed
   symbols.** Python has no private symbol; the underscore convention is
   the boundary marker. Tests importing `_resolve_endpoint`,
   `_validate_rpc_url`, `_parse_params`, `_DENY_METHODS`, etc., is a
   deliberate exception to "no backdoor imports".
10. **A10 — Python 3.9+ is the runtime** (matches the existing skill's
    use of `frozenset` literal syntax and `tuple` of strings).
11. **A11 — A future `libs/eth-rpc-core/` consumer imports the same
    sections** with no signature changes. This is the
    extraction-readiness contract this architecture guarantees.
12. **A12 — P1 work (`--decode`, `--read-only-strict`, `--params @file`,
    sepolia/holesky) is forward-shaped, not implemented.** `--decode`
    lands as a new section between `do_call` and `print`;
    `--read-only-strict` adds an `allowlist=None` kwarg to a
    `_check_method_policy(method, allow_write, allowlist=None)`
    extraction or stays inline in `do_call` with a `strict=False` kwarg;
    `--params @file` adds one branch to `_parse_params`;
    sepolia/holesky are two new `NETWORKS` entries.

---

## Architecture Quality Checklist

- [x] **No circular dependencies between modules** — see "Module
      Dependency Graph"; the section graph is a strict DAG verified
      arrow-by-arrow.
- [x] **Each module has a single, clear responsibility describable in
      one sentence** — see the "Module Overview" Responsibility column.
- [x] **No shared databases** — there is no database; module data
      ownership is by code (constants live in `constants`, transport
      state lives in `rpc_transport`, no leakage).
- [x] **All inter-module communication goes through defined
      interfaces** — function calls only; no global mutable state
      introduced. Tests' access to underscore-prefixed symbols is the
      documented exception (A9).
- [x] **Every module can be tested in isolation with mocked
      dependencies** — `rpc=rpc_call` injection covers `do_call`,
      `do_balance`, `do_broadcast`; `_parse_params` injects `stdin`;
      `_validate_rpc_url` and `_resolve_endpoint` are pure;
      validators / formatting are already pure.
- [x] **Cross-cutting concerns are standardized** — one error pattern
      (`error: <msg>` / stderr / exit 1), one warning pattern
      (`warning: <msg>` / stderr), one output pattern
      (`json.dumps(..., indent=2)` / stdout), one config source (CLI
      flags only), one network access pattern (`rpc_call`).
- [x] **Failure modes are defined** — per-section "Failure Modes"
      entries and the "Risks" table.
- [x] **Service extraction path is clear** — per-section
      "extraction readiness" table; mechanical recipe documented;
      argparse-stops-at-`main` is the single non-negotiable property
      that makes it work.
- [x] **Data flow is traceable** — five Data Flow Diagrams cover happy
      path, denied path, custom-endpoint stdin path, bypass path, and
      existing curated flows.
- [x] **Module count is justified** — three new sections
      (`endpoint_resolution`, `param_ingest`, `do_call`) plus three
      constant additions in `constants` plus `main` diffs. Lower bound
      (further inlining `_parse_params` into `main`) would lose unit-test
      isolation for three failure modes. Upper bound (a dedicated
      `_check_method_policy` section, a `transport` wrapper around
      `rpc_call`, a `decode` section) would add files of indirection
      with single call sites and was deliberately rejected:
      `_check_method_policy` is two lines inside `do_call` whose only
      meaningful seam is the `allow_write` bypass; a `transport` shim
      around `rpc_call` exists only as forward-shape for P2 batch and
      was rejected per YAGNI; `decode` belongs to P1, not P0.
