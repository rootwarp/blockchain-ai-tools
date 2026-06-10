# Research: Handling Secret Material in `eth-signer-mcp` — Best Practices

## Recommended Approach

For v1, treat secret hygiene as a **best-effort defense in depth** anchored on
six concrete practices:

1. Wrap the keystore password and any other shallow secret in a single
   `Secret` value type that implements `fmt.Stringer`, `fmt.GoStringer`,
   `fmt.Formatter`, `json.Marshaler`, and `slog.LogValuer` — and *never embed
   it in a logged struct* because `slog` will reflect through nested fields and
   bypass `LogValue` [1][2].
2. Read the password file with `os.ReadFile`, strip the trailing newline with
   `bytes.TrimRight` [3], use the bytes, then call `clear(passwd)` on the
   slice — the `clear` builtin zeroes every element of a slice to its
   element-type zero value [4].
3. Re-implement go-ethereum's unexported `zeroKey` locally as
   `clear(k.D.Bits())`. `big.Int.Bits()` returns the underlying limb array
   shared with the `*big.Int`, so clearing the returned slice zeroes the actual
   storage [5][6].
4. Use `runtime.KeepAlive(secret)` immediately *after* the last legitimate use
   to guard against the compiler / GC freeing or rearranging the backing
   memory before the `clear` runs [7].
5. Compare the HTTP bearer token in constant time on **fixed-length hashes**
   (SHA-256 of supplied token vs. SHA-256 of expected token) — never on raw
   variable-length strings — because `crypto/subtle.ConstantTimeCompare`
   short-circuits on length mismatch and therefore leaks length [8][9].
6. Check keystore/password file permissions on startup using
   `FileInfo.Mode().Perm() & 0077 != 0` to detect group- or world-readable
   files; warn by default and refuse with `--strict-perms` (matches the PRD's
   `P0-SEC-4` / `P1-SEC-1`). Document the Windows caveat explicitly:
   `os.FileMode` permission bits are a Unix concept, and Go on Windows only
   surfaces the read-only attribute [10][11].

These satisfy `P0-SEC-1` through `P0-SEC-5` in the PRD without pulling in
`memguard` or `mlock`. Full guaranteed memory erasure is *not* achievable in
pure Go today — the compiler is permitted to eliminate dead stores and the GC
is permitted to copy backing arrays during stack growth or compaction — but the
practices above shrink the window meaningfully and are the documented Go
idioms [4][12].

## Approach Overview

### Option 1: [Recommended] — Stdlib hygiene (`clear` + redacting wrapper + perms check)

**How it works:** No third-party dependencies. A small `Secret` type guards
formatting and JSON paths; `clear` zeroes password bytes and key limbs; a
permission check on startup rejects (or warns on) world-/group-readable files;
the bearer-token comparison uses `subtle.ConstantTimeCompare` on hashed inputs.

**Why this one:** Matches the PRD's "small, auditable Go binary" footprint
constraint. Every primitive is in the standard library or in
`github.com/ethereum/go-ethereum`. Auditable in a single afternoon. No new
attack surface from a memory-management library.

**Trade-offs:** Provides *best-effort* erasure only. A sufficiently aggressive
future Go compiler could in principle eliminate the `clear` if it can prove the
slice is never read again — the existing precaution is `runtime.KeepAlive`
plus the empirical fact that today's Go compiler does not eliminate calls to
the `clear` builtin. There is no formal language-level guarantee [12].

### Option 2: [Alternative] — `awnumar/memguard` enclaves

**How it works:** Allocate password and key bytes inside a `memguard.LockedBuffer`,
which `mlock`s pages, fences them between guard pages, encrypts at rest, and
provides a `Destroy()` that zeroes before freeing.

**When to prefer this:** A long-lived signer that holds an unlocked key in
memory for many minutes; a multi-tenant signer; or a target where the
operational threat model includes core-dump / swap-file capture by a local
non-root attacker. None of these apply to v1 of `eth-signer-mcp`, which
decrypts at the moment of signing and zeroes immediately after.

**Trade-offs:** Adds a dependency with cgo-adjacent semantics; `mlock` requires
elevated limits and silently fails or partially locks on small
`RLIMIT_MEMLOCK`; the encrypted-at-rest enclave does not protect bytes during
the brief moment they are unlocked for use (which is when the geth signing path
needs them as a plain `*ecdsa.PrivateKey`). Recommendation: defer for v1 and
revisit if the threat model widens. *(This recommendation is editorial — not a
sourced verdict against memguard.)*

### Option 3: [Alternative] — Hand off to `keystore.SignTx` and never touch the `*Key`

**How it works:** Use `keystore.KeyStore.SignTxWithPassphrase(account, passphrase, tx, chainID)`
so the password and the decrypted key are confined to a single go-ethereum
function call. That call performs the same `defer zeroKey(...)` pattern
internally [13].

**When to prefer this:** When the caller does not need fine-grained control
over the inner txdata before signing.

**Trade-offs:** `keystore.KeyStore` is directory-oriented (loads accounts from
a directory at construction time) and presumes an account-manager UX that the
PRD explicitly avoids (`P2-KEY-1` is the only place a directory could appear).
For a single-file keystore + offline signer, `keystore.DecryptKey(jsonBytes, password)`
is the right primitive, and the responsibility for zeroing the returned
`*Key.PrivateKey` falls to us — exactly what this document codifies. The
go-ethereum function we are mimicking is:

```go
// from go-ethereum/accounts/keystore/keystore.go
func zeroKey(k *ecdsa.PrivateKey) {
    b := k.D.Bits()
    clear(b)
}
```

`zeroKey` is unexported, so we re-implement it locally [13][14].

---

## Implementation Guidelines

### (a) Why truly zeroing `*ecdsa.PrivateKey` is hard, and what is achievable

The decrypted scalar `D` is a `*big.Int`. `big.Int` stores its magnitude as a
little-endian slice of machine words returned by `Bits() []Word`. The
documentation states this slice **shares the same underlying array** as the
`Int`, so writing to it writes to the actual storage [6]:

> "Bits provides raw (unchecked but fast) access to x by returning its absolute
> value as a little-endian Word slice. The result and x share the same
> underlying array."

This is what makes the geth one-liner work, and what makes the same one-liner
correct for us:

```go
// re-implementation of geth's unexported zeroKey; copy locally rather than
// reaching for an unexported symbol.
func zeroKey(k *ecdsa.PrivateKey) {
    if k == nil || k.D == nil {
        return
    }
    clear(k.D.Bits())
}
```

What is *not* achievable:

- **Intermediate buffers inside `keystore.DecryptKey`.** The decryption path
  copies scalar bytes into several short-lived buffers before constructing the
  `*ecdsa.PrivateKey`. We have no handle on those; they become garbage
  immediately after the call. The Go GC will eventually reclaim them, but does
  not zero before reclaim, and a copying collector may have already produced
  ghost copies during stack growth. This is a generic property of GC'd
  languages, not a go-ethereum bug.
- **Stack copies of `*big.Int.D` across goroutine growth.** Go's GC is a
  non-moving collector for heap objects, but stacks grow by copying. Any
  `*big.Int` that lived on a goroutine stack at the moment of a stack-growth
  event may leave behind a copy in the old, freed stack page. Mitigation:
  perform the entire decrypt/sign/zero sequence inside a single function with
  modest stack usage so the chance of a stack-growth event during that window
  is small.
- **Compiler-level dead-store elimination.** Go's `dse` pass can in principle
  remove a "write whose value is never read." The `clear` builtin is currently
  not eliminated in practice, and `runtime.KeepAlive` is the documented escape
  hatch to mark the argument "as currently reachable" — preventing finalization
  before the call point [7]. There is no language-level guarantee that future
  compilers will not be more aggressive; this is an open issue in the Go
  compiler-backed-by-formal-correctness sense [12].

Net effect: full guaranteed memory erasure of secret material is **best-effort
in Go, not absolute**. The PRD's hardening rules are tightened by the
practices above; treat them as defense in depth, not a proof of erasure.

### (b) `memguard` / `mlock` trade-offs

`mlock(2)` pins pages so they cannot be swapped to disk. The kernel imposes a
per-process limit (`RLIMIT_MEMLOCK`); the default on Linux is **commonly
cited** at 64 KB on older kernels and 8 MB on newer ones, though we did not
re-verify that against a primary man-page section. The signer's working set is
tiny (a 32-byte scalar, a sub-100-byte password), so the limit is not the
problem — the operational complexity is: requiring users to raise limits or
run with `CAP_IPC_LOCK` is a non-trivial UX tax on a local developer tool. The
PRD explicitly does **not** target a threat model that includes swap capture
(out-of-scope adversaries in `Security & Threat Model` § "out-of-scope").

For `eth-signer-mcp` v1: best-effort `clear` suffices and `memguard` is not
adopted. If the threat model widens in v2 (long-lived unlocked key, multi-user
host), revisit.

### (c) `runtime.KeepAlive` and dead-store elimination

Two correctness concerns motivate `runtime.KeepAlive`:

1. **Finalizer races.** If a future maintainer attaches a finalizer to a wrapper
   struct, `KeepAlive` prevents that finalizer from running while the secret is
   still in use. The official documentation states [7]:

   > "KeepAlive marks its argument as currently reachable. This ensures that
   > the object is not freed, and its finalizer is not run, before the point in
   > the program where KeepAlive is called."

2. **Optimizer pressure.** While `KeepAlive` is specified narrowly around
   finalizer/freeing semantics, it has the *practical* side effect of preventing
   the compiler from treating the underlying memory as dead before that point.
   This is widely-relied-upon background, not a quoted spec guarantee, and
   community discussion in the Go issue tracker recognizes that secure
   zeroization in user code "ultimately rel[ies] only on the lack of
   optimizations in current Go implementations, instead of on formal
   correctness" [12].

Pattern:

```go
func signOnce(keystoreJSON, password []byte, tx *types.Transaction, chainID *big.Int) (signed *types.Transaction, err error) {
    // Zero the password slice when we leave this function, no matter what.
    defer clear(password)

    key, err := keystore.DecryptKey(keystoreJSON, string(password))
    if err != nil {
        return nil, fmt.Errorf("password_error: decrypt: %w", err)
    }
    // Zero the decrypted scalar's limbs when we leave this function.
    defer func() {
        zeroKey(key.PrivateKey)
        // Hint to the compiler/GC that key was still live up to this point.
        runtime.KeepAlive(key)
    }()

    signer := types.LatestSignerForChainID(chainID)
    signed, err = types.SignTx(tx, signer, key.PrivateKey)
    return signed, err
}
```

Notes:

- `defer clear(password)` runs *after* `DecryptKey` consumes it. The `string(password)`
  conversion makes a copy that lives inside go-ethereum's call stack and is
  not under our control — this is part of the unavoidable best-effort caveat.
- `runtime.KeepAlive(key)` is placed in the deferred function *after* the
  `zeroKey` call so that the compiler cannot decide the `key` value is dead
  earlier in the function body.

### (d) Constant-time bearer-token compare

`crypto/subtle.ConstantTimeCompare` is constant-time *in the contents* of equal-length
slices, but it short-circuits on length mismatch [9]:

> "ConstantTimeCompare returns 1 if the two slices, x and y, have equal
> contents and 0 otherwise. The time taken is a function of the length of the
> slices and is independent of the contents. If the lengths of x and y do not
> match it returns 0 immediately."

For a bearer token of unbounded user-supplied length, this is a length
oracle [8]. The recommended pattern is to compare **SHA-256 hashes** of the
supplied and expected tokens, which forces both inputs to a fixed 32 bytes:

```go
import (
    "crypto/sha256"
    "crypto/subtle"
    "net/http"
    "strings"
)

func bearerAuthMiddleware(expectedToken string, next http.Handler) http.Handler {
    expectedHash := sha256.Sum256([]byte(expectedToken))
    return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
        h := r.Header.Get("Authorization")
        const prefix = "Bearer "
        if !strings.HasPrefix(h, prefix) {
            http.Error(w, "unauthorized", http.StatusUnauthorized)
            return
        }
        supplied := strings.TrimPrefix(h, prefix)
        suppliedHash := sha256.Sum256([]byte(supplied))
        if subtle.ConstantTimeCompare(suppliedHash[:], expectedHash[:]) != 1 {
            http.Error(w, "unauthorized", http.StatusUnauthorized)
            return
        }
        next.ServeHTTP(w, r)
    })
}
```

The expected token is loaded once at startup from `--http-auth-token-file` and
hashed once. The hash sits in process memory (it is not a secret — a hash of a
bearer token is still the verifier; document `chmod 600` on the token file
and treat the in-memory hash as still sensitive enough to keep out of logs).

### (e) Reading the password file safely

```go
import (
    "bytes"
    "fmt"
    "io/fs"
    "os"
)

// readPasswordFile reads the password file, strips a trailing "\n" or "\r\n",
// and returns a fresh byte slice the caller is expected to `clear` after use.
// It also performs a sanity permission check.
func readPasswordFile(path string, strict bool) ([]byte, error) {
    info, err := os.Stat(path)
    if err != nil {
        return nil, fmt.Errorf("password_error: stat: %w", err)
    }
    if !info.Mode().IsRegular() {
        return nil, fmt.Errorf("password_error: not a regular file: %s", path)
    }
    if err := checkPerms(path, info, strict); err != nil {
        return nil, err
    }

    raw, err := os.ReadFile(path)
    if err != nil {
        return nil, fmt.Errorf("password_error: read: %w", err)
    }
    // Strip a single trailing newline (CRLF or LF). Do not strip arbitrary
    // whitespace — a password legitimately might contain trailing spaces.
    pw := bytes.TrimRight(raw, "\r\n")
    // If TrimRight returned the same backing array, we're fine — the bytes
    // beyond `len(pw)` are unreferenced and will be GC'd. For safety, copy into
    // a fresh slice so a future `clear(pw)` zeros exactly the buffer we
    // intend, and so the trailing bytes of `raw` don't survive indefinitely.
    out := make([]byte, len(pw))
    copy(out, pw)
    // Zero the original read buffer; it may have held the password plus a
    // newline.
    clear(raw)
    return out, nil
}

// checkPerms enforces P0-SEC-4: warn (or, with --strict-perms, refuse) when a
// file is group- or world-readable on POSIX. Windows: the FileMode perm bits
// are not meaningful and this check is a no-op. The PRD allows Windows to be
// best-effort.
func checkPerms(path string, info fs.FileInfo, strict bool) error {
    if runtimeIsWindows() {
        // Per the Go FileMode docs, on Windows the permission bits are derived
        // from the FILE_ATTRIBUTE_READONLY attribute only and do not
        // distinguish user/group/world. Document and move on.
        return nil
    }
    mode := info.Mode().Perm()
    if mode&0o077 == 0 {
        return nil // user-only.
    }
    if strict {
        return fmt.Errorf("password_error: %s is group- or world-accessible (mode %#o); refuse with --strict-perms", path, mode)
    }
    // caller emits the warning via the structured logger
    return nil
}

func runtimeIsWindows() bool { return runtime.GOOS == "windows" }
```

Why `bytes.TrimRight(raw, "\r\n")` rather than `strings.TrimSpace`:
TrimSpace would also strip trailing spaces and tabs, which a user may have
deliberately included in their password. TrimRight on `"\r\n"` only removes
the trailing line terminator the editor wrote [3].

Why copy into a fresh `out` and `clear(raw)`: the byte slice returned by
`os.ReadFile` may have additional capacity beyond `len(pw)` (it contained the
newline). Zeroing the read buffer immediately after extracting the trimmed
password limits the window in which the password lives in multiple places.

Windows caveat (`P0-SEC-4` portability): Go's `os.FileMode` permission bits are
Unix bits. On Windows, only the read-only attribute is reflected, so the
`0o077` check is meaningless — we skip it with a logged note and rely on
NTFS ACLs being configured by the operator. The PRD's non-functional
requirements already declare Windows as best-effort [10][11].

### (f) Keeping secrets out of logs/errors: the `Secret` wrapper and the log-scanning test

Single-type design rule (recommendation, not a sourced quote): one `Secret[T]`
type implementing **five** methods so that *every* common path that prints or
serializes a value falls into the redaction trap.

> Note: the samples below use a standalone `secret` package for illustration.
> In the final architecture this type lives in `internal/signing` (see
> architecture.md) — adjust package/import names accordingly.

```go
package secret

import (
    "encoding/json"
    "fmt"
    "log/slog"
)

// Secret wraps a value that must never appear in logs, formatted output,
// JSON, or error strings.
//
// Use it for the keystore password and any other shallow secret. Do NOT embed
// a Secret in a struct that itself gets logged — slog reflects through
// fields and will not call LogValue on nested values. Either log the Secret
// directly (so its LogValue is called) or pass only its non-secret siblings
// to the logger.
type Secret[T any] struct {
    v T
}

func New[T any](v T) Secret[T] { return Secret[T]{v: v} }

// Expose returns the underlying value. This is the only path to the secret.
func (s Secret[T]) Expose() T { return s.v }

// fmt.Stringer — catches %s, %v on the Secret value itself.
func (Secret[T]) String() string { return "[REDACTED]" }

// fmt.GoStringer — catches %#v.
func (Secret[T]) GoString() string { return "[REDACTED]" }

// fmt.Formatter — catches every verb (%q, %x, %+v, etc.).
func (Secret[T]) Format(f fmt.State, verb rune) {
    _, _ = fmt.Fprint(f, "[REDACTED]")
}

// json.Marshaler — catches encoding/json (and, transitively, anything that
// json-marshals before logging).
func (Secret[T]) MarshalJSON() ([]byte, error) {
    return json.Marshal("[REDACTED]")
}

// slog.LogValuer — catches log/slog's structured logger.
func (Secret[T]) LogValue() slog.Value {
    return slog.StringValue("[REDACTED]")
}
```

The five-method shape is researcher synthesis informed by the pattern used in
the community packages `cockroachdb/redact`, `75py/secretstr`, and
`logfusc.Secret` [15][16][17]. It is presented as our recommendation, not as
a verbatim source quote.

**Critical usage rule** — `slog` reflects through nested struct fields and
will **not** call `LogValue` on them [1][2]. This means:

```go
// WRONG — slog will print the inner Secret's stored bytes because it
// reflects through Config to render the field, and the reflection path does
// not invoke LogValue on nested values.
type Config struct { Password secret.Secret[string] }
slog.Info("starting", "config", cfg) // leaks

// RIGHT — log only non-secret siblings, never embed a Secret in a logged struct.
slog.Info("starting", "keystore_path", cfg.KeystorePath)
```

The arcjet write-up calls this out explicitly: the safe path is for the
*parent* struct to implement `LogValue` and to enumerate only the
fields it wants logged via `slog.GroupValue` [1]. We prefer the stronger rule
"never embed a Secret in a logged struct" because it is auditable by grep, and
because forgetting to update a parent's `LogValue` when a new field is added
fails open.

**Log-scanning test** (satisfies `P0-SEC-3`):

```go
package secret_test

import (
    "bytes"
    "encoding/json"
    "fmt"
    "log/slog"
    "strings"
    "testing"

    "github.com/rootwarp/blockchain-ai-tools/apps/eth-signer-mcp/internal/secret"
)

func TestSecret_NeverLeaks(t *testing.T) {
    const sentinel = "hunter2-NEVER-LEAK"
    s := secret.New(sentinel)

    cases := []struct {
        name string
        got  string
    }{
        {"Stringer", fmt.Sprint(s)},
        {"Verb_v", fmt.Sprintf("%v", s)},
        {"Verb_s", fmt.Sprintf("%s", s)},
        {"Verb_q", fmt.Sprintf("%q", s)},
        {"Verb_x", fmt.Sprintf("%x", s)},
        {"Verb_GoString", fmt.Sprintf("%#v", s)},
        {"Verb_PlusV", fmt.Sprintf("%+v", s)},
        {"Println", func() string {
            var b bytes.Buffer
            fmt.Fprintln(&b, s)
            return b.String()
        }()},
    }
    for _, tc := range cases {
        t.Run(tc.name, func(t *testing.T) {
            if strings.Contains(tc.got, sentinel) {
                t.Fatalf("sentinel leaked via %s: %q", tc.name, tc.got)
            }
        })
    }

    // JSON
    j, err := json.Marshal(s)
    if err != nil {
        t.Fatalf("MarshalJSON: %v", err)
    }
    if bytes.Contains(j, []byte(sentinel)) {
        t.Fatalf("sentinel leaked via JSON: %s", j)
    }

    // slog (both handlers)
    for _, name := range []string{"text", "json"} {
        var buf bytes.Buffer
        var h slog.Handler
        if name == "text" {
            h = slog.NewTextHandler(&buf, nil)
        } else {
            h = slog.NewJSONHandler(&buf, nil)
        }
        logger := slog.New(h)
        logger.Info("event", "secret", s)
        if bytes.Contains(buf.Bytes(), []byte(sentinel)) {
            t.Fatalf("sentinel leaked via slog (%s): %s", name, buf.String())
        }
    }
}
```

Extend this test in the signer package to also write captured server logs at
every level (debug through error) and assert the password sentinel and a
synthetic private-key sentinel never appear. That is the test the PRD's
`P0-SEC-3` calls for, and it doubles as a regression guard the day someone
"helpfully" adds `logger.Debug("decrypted", "key", key)`.

---

## Common Pitfalls

- **Pitfall: Embedding a `Secret` in a struct that gets logged.**
  `slog` will reflect through the struct and emit the embedded value's actual
  field, bypassing `LogValue` [1][2]. Mitigation: never put a `Secret` inside
  a struct that ends up in a `slog` call; pass it as a top-level attribute or
  log the parent struct via an explicit `LogValue` that enumerates only safe
  fields.

- **Pitfall: Using `subtle.ConstantTimeCompare` on raw variable-length tokens.**
  Leaks length [8][9]. Mitigation: SHA-256 both inputs and compare the
  fixed-length hashes.

- **Pitfall: `strings.TrimSpace` on the password.**
  Silently strips trailing spaces / tabs the user may have intended as
  password characters. Mitigation: `bytes.TrimRight(raw, "\r\n")` only [3].

- **Pitfall: Trusting `os.FileMode` permission bits on Windows.**
  Windows derives the bits from `FILE_ATTRIBUTE_READONLY` only and does not
  distinguish user/group/world [10][11]. Mitigation: skip the perm check
  with a clear log note on Windows; rely on operator-set NTFS ACLs; this is
  why the PRD calls Windows "best-effort."

- **Pitfall: Reaching for go-ethereum's `crypto.zeroKey` / `keystore.zeroKey`.**
  Both are unexported [14]. Mitigation: re-implement locally — exactly
  `clear(k.D.Bits())`. Test that the limb slice is in fact zero after the call.

- **Pitfall: Holding the `*Key` across an MCP round-trip.**
  The PRD requires `P0-SEC-2`: the decrypted key lives in memory only for the
  duration of one signing operation. Mitigation: decrypt inside the tool
  handler, defer `zeroKey`, never cache the key on a struct field that
  outlives the handler call.

- **Pitfall: Relying on the GC to zero secrets.**
  It does not. Go's GC frees memory; it does not scrub it. Mitigation: explicit
  `clear` plus `runtime.KeepAlive` to bound the window.

- **Pitfall: Logging the keystore JSON bytes for "debug".**
  The keystore JSON includes ciphertext, kdf params, and an `address`. The
  ciphertext is not the cleartext key but it is still material the threat
  model classifies as a secret asset. Mitigation: never log raw `keystoreJSON`
  bytes; log only the derived `Address` after permission and integrity checks.

- **Pitfall: Forgetting that `string(password)` makes a copy.**
  `keystore.DecryptKey(jsonBytes, auth string)` takes a `string` parameter.
  Converting `[]byte` to `string` produces an immutable copy under go-ethereum's
  control [13]. We cannot zero that copy. Mitigation: accept that one short-lived
  copy escapes our zeroing — this is a fundamental Go-language limit, not a bug
  to fix — and minimize call duration. The mitigation that *is* under our control
  is still useful: zero the original `[]byte` we read from the file as soon as
  `DecryptKey` returns.

---

## Real-World Examples

- **go-ethereum** uses `defer zeroKey(key.PrivateKey)` in
  `SignHashWithPassphrase`, `SignTxWithPassphrase`, `Import`, `Delete`,
  `TimedUnlock`, and `expire` to zero the scalar after each transient unlock.
  The function body is two lines: `b := k.D.Bits(); clear(b)` [14].
  This is the canonical Go-ecosystem pattern and what we mimic.

- **cockroachdb/redact** wraps sensitive substrings in a marker type that
  the redact-aware printer redacts; structured types implementing
  `SafeFormatter` get controlled formatting. The pattern motivates the
  multi-interface approach we adopt for `Secret` [15].

- **`logfusc.Secret[T]`** implements `fmt.Stringer`, `fmt.GoStringer`,
  `json.Marshaler`, and `json.Unmarshaler`, returning a `"{REDACTED}"`
  placeholder for every format verb; an explicit `Expose()` is the only path
  to the inner value [17]. Our `Secret` adopts this shape and adds
  `fmt.Formatter` (to catch verbs like `%x`/`%q` directly without falling back
  to `String`) and `slog.LogValuer` (for the official structured logger).

- **arcjet's `slog` guide** notes that the safe path for nested redaction is
  for the *parent* struct to implement `LogValuer` and enumerate the fields it
  wants logged via `slog.GroupValue`; nested `LogValuer` implementations may
  or may not be invoked depending on what handler-level reflection does [1].
  We take the stronger rule: never embed a `Secret` in a logged struct.

---

## Sources

[1] [Redacting sensitive data from logs with Go's log/slog](https://blog.arcjet.com/redacting-sensitive-data-from-logs-with-go-log-slog/) — Arcjet blog. Discusses the parent-struct `LogValuer` pattern and the practical limits of nested-field redaction with `slog`.

[2] [`log/slog` package — `LogValuer`](https://pkg.go.dev/log/slog#LogValuer) — Go standard library docs. Defines `LogValuer.LogValue()` and how handlers consume it for top-level values.

[3] [`bytes.TrimRight`](https://pkg.go.dev/bytes#TrimRight) — Go standard library docs. "TrimRight returns a subslice of s by slicing off all trailing UTF-8-encoded code points that are contained in cutset."

[4] [The `clear` builtin](https://pkg.go.dev/builtin#clear) — Go standard library docs. "For slices, clear sets all elements up to the length of the slice to the zero value of the respective element type."

[5] [go-ethereum `accounts/keystore/keystore.go`](https://github.com/ethereum/go-ethereum/blob/master/accounts/keystore/keystore.go) — Source for the unexported `zeroKey` function and the `defer zeroKey(...)` pattern in `SignHashWithPassphrase`, `SignTxWithPassphrase`, etc.

[6] [`math/big.Int.Bits`](https://pkg.go.dev/math/big#Int.Bits) — Go standard library docs. "The result and x share the same underlying array." This is what makes `clear(k.D.Bits())` actually zero the storage.

[7] [`runtime.KeepAlive`](https://pkg.go.dev/runtime#KeepAlive) — Go standard library docs. "KeepAlive marks its argument as currently reachable. This ensures that the object is not freed, and its finalizer is not run, before the point in the program where KeepAlive is called."

[8] [Go issue #18936 — `ConstantTimeCompare` leaks length](https://github.com/golang/go/issues/18936) — golang/go. Discussion confirming the length-leak short-circuit is documented behavior and recommending pre-hashing variable-length inputs.

[9] [`crypto/subtle.ConstantTimeCompare`](https://pkg.go.dev/crypto/subtle) — Go standard library docs. "If the lengths of x and y do not match it returns 0 immediately."

[10] [`os.FileMode` / `os.FileInfo`](https://pkg.go.dev/os#FileInfo) — Go standard library docs. Documents that the nine least-significant bits are Unix `rwxrwxrwx` and exposes `.Mode().Perm()`.

[11] [Go and file perms on Windows](https://medium.com/@MichalPristas/go-and-file-perms-on-windows-3c944d55dd44) — Michal Pristas, Medium. Documents the Windows-specific behavior where `FileMode` reflects only `FILE_ATTRIBUTE_READONLY` and does not distinguish user/group/world bits.

[12] [Go issue #33325 — proposal: built-in to prevent dead-store elimination](https://github.com/golang/go/issues/33325) — golang/go. Confirms that secure zeroization in current Go user code relies on the absence of optimizations, not on formal correctness; motivates the best-effort caveat in this doc.

[13] [`accounts/keystore` package docs — `DecryptKey`](https://pkg.go.dev/github.com/ethereum/go-ethereum/accounts/keystore) — go-ethereum docs. `DecryptKey(keyjson []byte, auth string) (*Key, error)`; `Key` exposes `PrivateKey *ecdsa.PrivateKey`.

[14] [go-ethereum keystore source — `zeroKey` definition and callers](https://github.com/ethereum/go-ethereum/blob/master/accounts/keystore/keystore.go) — github.com/ethereum/go-ethereum. The unexported `zeroKey(k *ecdsa.PrivateKey)` is two lines (`b := k.D.Bits(); clear(b)`) and is called from `Delete`, `SignHashWithPassphrase`, `SignTxWithPassphrase`, `TimedUnlock`, `Import`, and `expire`.

[15] [`cockroachdb/redact`](https://pkg.go.dev/github.com/cockroachdb/redact) — Package docs. Implements safe-formatter wrappers that pass through to controlled formatting while redacting unmarked values.

[16] [Prevent Logging Secrets in Go by Using Custom Types](https://www.commonfate.io/blog/prevent-logging-secrets-in-go-by-using-custom-types) — Common Fate. Walks through the `String()` + `MarshalJSON()` pattern for a secret wrapper type and explicitly calls out that the wrapper is "not totally foolproof" against deliberate exposure.

[17] [Logfusc: Surefire Secret Redaction for Logs and Traces](https://www.angus-morrison.com/blog/logfusc) — Angus Morrison. Describes the `logfusc.Secret[T]` generic with `fmt.Stringer`, `fmt.GoStringer`, `json.Marshaler`, `json.Unmarshaler`, and an `Expose()` accessor; informs the multi-interface shape recommended here.

[18] [`os.ReadFile`](https://pkg.go.dev/os#ReadFile) — Go standard library docs. Reads a whole file into a `[]byte`; appropriate for short password files when followed by `clear` on the returned buffer.
