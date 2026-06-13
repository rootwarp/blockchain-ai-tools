# Adversarial review — `feature/make-keystore-address-optional`

| | |
|---|---|
| **Branch** | `feature/make-keystore-address-optional` |
| **Base** | `develop` (merge-base `7c38707`) |
| **Commit** | `1885e75` — *proceed: addr-opt-1 Make top-level "address" field optional in keystore JSON* |
| **Diff** | `git diff develop...HEAD` — 17 files, +279 / −119 |
| **Reviewed** | 2026-06-13 |
| **Method** | High-effort adversarial review: line-by-line + removed-behavior + cross-file angles, findings independently verified by refutation-biased agents. |

## Scope

The change makes the keystore JSON top-level `"address"` field **optional**: instead
of failing startup when it's absent/empty, the vault stores the zero address and
"discovers" the real one from the decrypted key on the first sign (unconditional
`v.address = key.Address` in `internal/signing/decrypt.go`).

Findings #1 and #2 were independently confirmed by adversarial verifiers; the rest
survived a refutation sweep. Ranked most severe first.

---

## 1. 🔴 Data race on `v.address` (CONFIRMED) — and CI won't catch it

`internal/signing/decrypt.go:150` · `internal/signing/file_vault.go:93`

The new write `v.address = key.Address` runs inside `WithSigningKey` under the `sem`
semaphore. But `Address()` is a bare field read with **no** `sem`/mutex/atomic:

```go
func (v *fileKeyVault) Address() common.Address { return v.address }
```

`common.Address` is `[20]byte` — not word-sized, so reads can tear. The HTTP transport
dispatches tool calls concurrently (`jsonrpc2.Async` → goroutine per call; one shared
vault), so a `get_address` call (→ `Address()`) overlapping a `sign_transaction`
(→ the write) is an unsynchronized read/write = a genuine Go data race, **reachable in
production**.

Worse than a stale value: the write now fires on **every** sign, even for normal
keystores that previously had an immutable, write-once-at-construction address. So this
diff *introduces* a race for the common case that didn't exist before.

`make test` / CI run `go test ./...` with **no `-race`** (`Makefile:46`, `ci.yml:44`) —
the race ships silently. The struct/`Address()` comments rationalize it as *"best-effort
visibility… the sem serializes writers,"* which is misleading: `sem` serializes writers
against each other but does nothing for the unsynchronized reader. The ADR-009 analogy
launders a real shared-memory bug into an "accepted limitation."

**Fix:** make both sides go through one primitive — `atomic.Pointer[common.Address]`, or
a `sync.RWMutex`, or keep the address immutable (see #2's fix, which also removes the
need to write on every call).

---

## 2. 🔴 The defensive sender-mismatch guard is now dead; declared address silently overwritten (CONFIRMED)

`internal/signing/decrypt.go:150` · `internal/signing/signer.go:185-196`

`v.address = key.Address` executes **before** the callback `fn` runs. Inside `fn`, the
integrity check reads the value it just wrote:

```go
keystoreAddr := s.vault.Address()      // == key.Address (just written at decrypt.go:150)
if sender != keystoreAddr { /* internal_error */ }   // sender == key.Address ⇒ always false
```

Before this branch, `keystoreAddr` was the operator-**declared** address from the
keystore JSON, so the check validated *declared address vs actual key*. Now both operands
derive from the same key, so the guard **can never fire for any keystore** — its prose at
`signer.go:169-196` ("must never silently produce a result attributed to the wrong
address") is now dead documentation.

**Failure scenario:** operator points `--keystore` at the wrong/swapped account —
declares address A, key is actually B. Boots clean. `get_address` returns A. First
`sign_transaction` overwrites to B, **signs successfully** (no error), and returns a tx
`from` B. `get_address` afterward silently returns B. Previously this failed fast with
`internal_error: recovered sender …B does not match keystore address …A`.

The branch's actual goal (optional/absent address) only needs the *zero* case healed.
Making the write unconditional also silenced the *present-but-disagreeing* case — a
different, security-relevant signal.

**Fix:** only discover when unknown, preserving the guard for the present case:

```go
if v.address == (common.Address{}) { v.address = key.Address }   // discover only
// and keep the sender != declared check firing when declared != zero
```

---

## 3. 🟠 `get_address` can return the zero/burn address with no "unknown" signal — financial footgun

`internal/server/handlers.go:154` · `internal/signing/result.go:54` · `internal/server/server.go:92`

For an address-less keystore (now the *recommended*, privacy-friendly form per the new
docs), `get_address` returns `{"address":"0x0000000000000000000000000000000000000000"}`
until the first sign. Nothing on the wire flags it as a placeholder — and both the
`AddressResult.Address` doc and the tool `Description` still assert it is "the EIP-55
checksummed Ethereum address of the loaded keystore account." They now lie during the
pre-discovery window.

**Failure scenario:** an agent/client probes `get_address` first ("who am I?") before
building a tx, gets `0x0000…0000`, and uses it as the funding/`from`/display address.
Funds sent to the burn address are irrecoverable. There's no `discovered:false`, no
error, no omitempty.

**Fix:** if the address is unknown, return an explicit error/`isError` (or a typed "not
yet discovered" signal) rather than the zero address; and update the contract/description
text to state the optional-address semantics.

---

## 4. 🟠 Permissive `HexToAddress` turns a malformed present address into a plausible-looking garbage snapshot

`internal/signing/file_vault.go:67-73`

`common.HexToAddress(ks.Address)` silently right-aligns/zero-pads or truncates malformed
hex instead of erroring. The parse is pre-existing, but its blast radius is **new**:
previously a wrong address was caught at first sign by the mismatch guard (now defeated,
#2), so the garbage value is what `get_address` serves until first sign, then silently
replaced.

**Failure scenario:** `"address":"0x123"` (typo/short) → boots clean, `get_address`
returns `0x0000…0000000123`, a valid-looking EIP-55 string indistinguishable from a real
address or the zero placeholder (#3). No `keystore_error`, no warning.

**Fix:** if `ks.Address != ""`, validate it is a well-formed 20-byte hex address and
`keystore_error` otherwise — don't accept silently-coerced garbage.

---

## 5. 🟡 Fail-fast narrowed: broken keystore boots clean and fails later as `internal_error`; README error table misdirects

`internal/signing/file_vault.go` (removed `if ks.Address == ""` guard) · `internal/signing/decrypt.go:135-143` · `README.md:215`

The deleted guard was a weak boot-time fail-fast. With it gone — and full keystore JSON
validity still not checked at boot (only `"address"` is parsed) — a structurally-broken-
but-undecryptable keystore (corrupt ciphertext, unknown cipher/KDF) now boots
successfully and only fails on the **first sign**, mapped to `internal_error`/
`password_error` mid-session. The operator-facing README error table still files keystore
problems under `keystore_error`, so they're misdirected when debugging.

**Fix:** note the runtime-detection cases in the error table, or validate
decryptability/JSON shape at boot if fail-fast is still desired.

---

## 6. 🟡 Doc contract wrong: address is discovered on successful *decrypt*, not "first successful `sign_transaction`"

`README.md:140,215` · `internal/signing/decrypt.go:146-150`

The write at `decrypt.go:150` happens right after `DecryptKey`, **before** the
sender-mismatch check, RLP encode, and `fn` return. So a `sign_transaction` that decrypts
then fails (e.g. an `internal_error` or panic in `fn`) still updates `v.address` —
`get_address` starts returning a non-zero address even though no sign ever *succeeded*.
The README's "returns the zero address until first successful `sign_transaction`" is a
contract a client might rely on to detect readiness, and it's inaccurate.

**Fix:** move the discovery write to after `fn` returns nil, or correct the docs to say
"first successful decrypt."

---

## 7. 🟡 `TestWithSigningKey_NoAddress_ConcurrentReaders` gives false race coverage

`internal/signing/decrypt_test.go:428-465`

The test calls `WithSigningKey(...)` to completion **first** (line 440), *then* spawns the
reader goroutines (line 451). Goroutine creation establishes happens-before, so there is
no concurrent read-during-write — the test cannot observe the race in #1, yet its comment
claims "concurrent `Address()` calls overlapping the first decrypt" and "Finding 2/5
coverage." It will pass under `-race` and falsely signal the concern is tested.

**Fix:** to actually cover it, spawn readers that run *while* `WithSigningKey` is inside
`fn` (e.g. block inside the callback until readers have fired). Better: fix #1 so the
question is moot.

---

## 8. 🟡 `TestWithSigningKey_WrongPresentAddress_SelfHeals` codifies the #2 regression as required behavior

`internal/signing/decrypt_test.go:383-422`

This test asserts that a keystore declaring address `…0001` but resolving to a different
key **signs successfully** and "self-heals." That is exactly the integrity-guard defeat in
#2, now encoded as a passing requirement. Any future fix restoring the guard will fail
this test and look like a regression — actively entrenching the bug.

**Fix:** if #2 is fixed, this test should assert `internal_error` for a present-but-
disagreeing address; reserve "self-heal" for the absent/zero case only.

---

## Bottom line

The smallest defensible version of this feature is: **discover only when the address is
unknown (zero), keep it immutable once known, and synchronize the field.** That single
change neutralizes #1 (no per-call write to race on), restores #2 (guard fires for
present-but-wrong), and shrinks #3–#8 to doc/test cleanups. As written, the unconditional
always-overwrite is the root cause behind the two confirmed high-severity findings.

The headline risk for an offline *signer holding keys*: **#1** (a shipping, CI-invisible
data race) and **#3** (`get_address` can hand a client the burn address with the wire
contract insisting it's the real account).
