# Adversarial Review: `feature/make-keystore-address-optional` (8b5f9f5)

**Base:** `develop` (7c38707) · **Head:** `feature/make-keystore-address-optional` (8b5f9f5)
**Date:** 2026-06-13
**Method:** Multi-agent adversarial workflow — empirical ground-truth (build/vet/test/`-race`/lint)
running concurrently with 6 dimension finders (concurrency, security, correctness, tests,
spec-compat, simplicity-docs); each finding refuted by 3 diverse-lens skeptics (code-reality,
exploitability, prior-art); a finding survives only with ≥2 confirms and confirms > refutes.
**Stats:** 104 agents · 32 findings raised · 26 confirmed · 6 refuted.
**Manual cross-check:** H1 and H2 (the merge-blocking findings) were independently verified by
hand against HEAD — the write at `decrypt.go:150` is unconditional and precedes `fn` (called at
`decrypt.go:180`); `Address()` (`file_vault.go:94`) is a bare unsynchronized read; the guard at
`signer.go:185-186` reads the just-written value.

## 1. Verdict

**[BLOCK MERGE]** — This commit makes the top-level keystore `address` optional (a sound goal),
but does so by making the address cache *unconditionally mutable on every decrypt*, which (a)
silently kills the sender-mismatch integrity guard for a swapped/wrong keystore and (b) introduces
a real, CI-invisible data race on the address returned to clients and logs. Both are fixable with a
one-line zero-guard plus field synchronization.

## 2. Empirical status

| Check | Result |
|---|---|
| `go build ./...` | PASS |
| `go vet ./...` | PASS |
| `go test ./...` | PASS |
| `golangci-lint run ./...` (v2) | PASS — 0 issues |
| `go test -race ./...` (committed tree) | PASS — 0 races |

**Critical caveat:** the green `-race` run proves almost nothing. `concurrent_addr_test_exists =
false` — **no committed test ever calls `Address()`/`get_address` concurrently with an in-flight
`WithSigningKey`/`sign_transaction`.** The one test that claims to
(`TestWithSigningKey_NoAddress_ConcurrentReaders`, decrypt_test.go:850) discovers synchronously at
line 862, *then* spawns readers at 873 — goroutine creation is a happens-before edge, so there is
no overlap. `server/concurrent_test.go` bursts 10 signs but issues no `get_address` and uses a
has-address fixture. The `v.address` race is therefore **latent but real**: a probe spinning
`Address()` readers against `WithSigningKey` reproduces `WARNING: DATA RACE` at decrypt.go:150
(write) vs file_vault.go:93 `Address()` (read), with no happens-before. It ships invisibly.
(Working tree note: an untracked review doc `full-codebase-adversarial-review-2026-06-13.md` is
present; no source changes are uncommitted.)

## 3. Confirmed findings (ranked; duplicates merged)

### H1 — Defensive sender-mismatch integrity guard is now dead (security regression)
**Severity: HIGH** · `internal/signing/decrypt.go:150` (write) + `internal/signing/signer.go:185-196` (defeated guard)

The unconditional `v.address = key.Address` (decrypt.go:150) executes **before** `fn(sk)`
(decrypt.go:180). Inside `fn`, the guard reads `keystoreAddr := s.vault.Address()` (signer.go:185)
— which is now exactly `key.Address` — and compares it to `sender`, which is recovered from a
signature produced by that *same* key. Both operands are `key.Address`, so `if sender !=
keystoreAddr` (signer.go:186) **can never fire for any keystore**. The guard's own prose
(signer.go:169-172: "must never silently produce a result attributed to the wrong address") is dead
documentation.

Before this commit, `v.address` held the operator-*declared* address (set once at construction,
never mutated), so the guard validated declared-vs-actual. **Failure scenario:** an operator points
`--keystore` at a swapped account (JSON declares A, key decrypts to B). It boots clean;
`get_address` returns A; the first `sign_transaction` silently overwrites the cache to B and signs
successfully, attributing the tx to B with **no error**. Previously this fail-fasted with
`internal_error: recovered sender …B does not match keystore address …A`. (Not key compromise or
wrong-key signing — the file's own key always signs — hence high, not critical.)

The added `TestWithSigningKey_WrongPresentAddress_SelfHeals` (decrypt_test.go:805) **codifies this
defeat as required passing behavior**, so any future restoration of the guard will look like a
regression. The only test exercising the guard (`TestSigner_SenderMismatch`, signer_test.go:418)
uses a stub `mismatchedAddressVault` that hardcodes a wrong `Address()`, so it passes regardless and
masks the real-vault regression.

**Fix:** guard the write — `if v.address == (common.Address{}) { v.address = key.Address }`. This
heals only the absent/zero case (the feature's actual goal) and keeps `keystoreAddr` = declared A
for a present field, so B != A fires `internal_error`. Then rewrite `WrongPresentAddress_SelfHeals`
to assert `internal_error` for a present-but-disagreeing address, and add a test that drives
`Signer.SignTransaction` over a *real* `fileKeyVault` with a swapped address asserting rejection.

### H2 — Data race on `v.address`: per-call write races unsynchronized `Address()` reads, invisible to CI
**Severity: HIGH** · `internal/signing/decrypt.go:150` (write) + `internal/signing/file_vault.go:93-95` (read)

`v.address = key.Address` runs inside `WithSigningKey` under the capacity-1 `sem`, but `sem` only
serializes *writers* against each other. `Address()` (file_vault.go:93: `return v.address`) is a
bare read with no sem/mutex/atomic, reached from the password-free `get_address` handler
(handlers.go:154 `sp.Address()` → signer.go:76 → vault) and the startup log (main.go:252).
`common.Address` is `[20]byte` (not word-sized). The HTTP transport dispatches tool calls
per-goroutine over one shared vault, so a `get_address` overlapping any `sign_transaction` is a
genuine Go-memory-model data race (UB) on a value returned to clients and logs — reproduced under
`-race` (see §2).

The write is **unconditional**, so it fires on every sign for *every* keystore — including normal
has-address keystores whose address was write-once-immutable at base 7c38707. **This commit
therefore introduces the race for the common case that did not exist before.** The struct comment
(file_vault.go:26) and `Address()` godoc rationalize this as "best-effort visibility … the sem
serializes writers. Analogous to ADR-009" — but `sem` provides zero happens-before to the reader,
and ADR-009 is about best-effort *secret zeroing*, not unsynchronized shared-memory access. The
analogy launders a fixable race into an "accepted limitation."

**Fix:** route both sides through one primitive — `atomic.Pointer[common.Address]` (store in the
writer, load in `Address()`) or a `sync.RWMutex`. Combined with the H1 zero-guard, the write becomes
a one-time store and the race window collapses. Add `go test -race` to CI (`make test-race` or
`-race` on the existing run) and make `TestWithSigningKey_NoAddress_ConcurrentReaders` actually
overlap readers with the discovery write (block in the callback until readers are spinning). Delete
the ADR-009 / "best-effort visibility" framing.

### H3 — `get_address` returns the zero (burn) address while the wire contract asserts it is the real account
**Severity: HIGH** · `internal/server/handlers.go:154-157`, `internal/signing/result.go:50-55`, `internal/server/server.go:92-96`

For an address-less keystore (now a spec-endorsed, supported form), `Address()` returns
`common.Address{}` until the first decrypt, so `get_address` returns
`0x0000000000000000000000000000000000000000` with **no readiness signal** — no `discovered` flag, no
`isError`, no `omitempty` (handlers.go:155-157 returns `AddressResult{Address: addr.Hex()}`
unconditionally). Meanwhile the typed-result doc (result.go:54: "the EIP-55 checksummed Ethereum
address of the loaded keystore account") and the `get_address` tool `Description` returned to
clients via `tools/list` (server.go:92-96: "served from the boot-time keystore snapshot") both
assert it is the real account. **Financial footgun:** an agent that probes `get_address` first ("who
am I?") gets the burn address and may use it as a `from`/funding address; funds sent there are
irrecoverable. (No wrong-key signing — the sign path independently returns the true `From` — hence
high, not critical.)

**Fix:** when the address is unknown (zero, undiscovered), return an explicit tool error or a typed
`discovered:false` result instead of a valid-looking `0x000…000`. Update `AddressResult.Address`
doc, the `get_address` tool `Description`, and the `signerPort.Address` godoc (handlers.go:42-43) to
state the optional-address / zero-until-first-decrypt semantics.

### M1 — Permissive `HexToAddress` silently coerces a malformed present address into plausible garbage
**Severity: MEDIUM** · `internal/signing/file_vault.go:67-73`

`if ks.Address != "" { addr = common.HexToAddress(ks.Address) }` performs no length/format
validation. `HexToAddress` never errors: `"0x123"` → `0x…0123`, over-length is left-truncated,
non-hex → zero. A typo like `"address":"0x123"` boots clean and is served by `get_address` and the
startup log as a valid-looking wrong address until the first sign silently replaces it (with H1's
guard dead, nothing ever flags the disagreement). The project already validates the user `to` field
with `len==42 && common.IsHexAddress(s)` (validate.go:455) — the identity-critical keystore field is
inconsistently left unchecked.

**Fix:** when `ks.Address != ""`, require `common.IsHexAddress(ks.Address)` (and length 42) and
return `CodeKeystoreError` otherwise. Restores fail-fast on operator typo/tampering while keeping
the field optional when truly absent.

### M2 — Release artifacts and wire docs describe the old fail-fast / always-real-address contract
**Severity: MEDIUM** · `docs/release-notes-v1.0.0.md:16,107`; `server.go:88-96`; `result.go:50-55`; `handlers.go:42-43`

The commit updated the internal docs (`doc.go`, `errors.go`) and the README (140, 215) for the new
optional-address behavior, but left the **client-facing and release surfaces stale and
self-contradictory**: release-notes line 107 still says "Keystore JSON read; Ethereum address
extracted — boot-time snapshot, fail fast. Missing / malformed keystore → `keystore_error`" (the
exact line the README was edited away from in this same commit), line 16 omits the zero-window; and
the `tools/list` `Description` + `AddressResult` godoc still claim the value is always the real
account. `CHANGELOG.md` `[Unreleased]` is empty and the v1.0.0 `get_address` entry ("uses the
keystore's stored address field") is stale. No `eth-signer-mcp` git tag exists yet (v1.0.0 not
frozen).

**Fix:** record the change under CHANGELOG `[Unreleased] → Changed`; sync the release-notes
lifecycle row + `get_address` bullet, the tool `Description`, and the `AddressResult` doc to the new
semantics. Do **not** retroactively rewrite the released v1.0.0 bullet beyond the `[Unreleased]`
entry.

### L1 — Doc/contract drift: discovery is on first successful *decrypt*, not first successful *sign*; "first/one-time" comments are false
**Severity: LOW** · `README.md:140,215`; `vault.go:16-17,25-26`; `file_vault.go:26,91`

The write at decrypt.go:150 fires after `DecryptKey` and **before** `fn` — so a sign that decrypts
then fails inside `fn` (e.g. a panic) still mutates `v.address`, and `get_address` returns non-zero
though no sign *succeeded*. README/vault.go say "first successful `sign_transaction`/`WithSigningKey`";
the accurate trigger is "first successful decrypt" (which `file_vault.go:26`/`decrypt.go:146`
themselves use — the codebase contradicts itself). Separately, file_vault.go:26 ("overwritten on
first successful decrypt") and :91 ("one-time lazy write") describe a write-once field, but the
write is unconditional on every decrypt.

**Fix:** standardize on "first successful decrypt" everywhere; the H1 zero-guard additionally makes
the "first/one-time" wording literally true.

### Info — Design simplicity & verified spec citation
- The unconditional rewrite is **not** the simplest correct design. The one-line zero-guard (`if
  v.address == (common.Address{})`) is strictly simpler in consequences: it makes the field
  write-once (fixing L1 comments), removes the write for the common has-address case (shrinking H2),
  and preserves the H1 guard. Construction-time decrypt is correctly *not* viable (the locked
  lifecycle, vault.go:69, forbids reading the password at construction). **This single change
  resolves H1 and shrinks H2/M1/L1.**
- The spec claim that the top-level `address` is optional/"unnecessary and compromises privacy"
  (file_vault.go:63-64) is **accurate** per the Web3 Secret Storage v3 spec and the in-repo
  `keystore-no-address.json` fixture. Not a finding.

## 4. Notable refuted claims (process transparency)

- **"Torn-read produces a bogus third address" (medium):** technically true that `[20]byte` is
  multi-word, but it is one manifestation of the H2 race with the identical fix — not a separate
  defect. Folded into H2.
- **"Stray uncommitted `zztmp_race_test.go` left in the tree" (low):** refuted — the file does not
  exist and never appears in git history; it was a transient probe a reviewer created and deleted.
  Tree is clean.
- **"`WrongPresentAddress_SelfHeals` brittly string-replaces the fixture, silently no-ops" (low):**
  refuted — the assertion at decrypt_test.go:827 (`want wrong value 0x…0001`) fails loudly if the
  replace no-ops, empirically verified.
- **"CHANGELOG entry is HIGH severity / v1.0.0 entry factually wrong":** downgraded — the README
  *was* updated in this commit, so operators are informed; the residual is a low-severity
  `[Unreleased]` gap (folded into M2).
- **"No test for decrypt-but-fn-fails discovery" (medium):** downgraded to low — the value written
  is always the *correct* key address (reported early, never wrong); benign doc/test-precision gap
  (folded into L1).

## 5. Bottom line

**One change unblocks merge:** make the discovery write conditional and synchronized.

```go
// decrypt.go:150 — discover only when unknown
if v.address == (common.Address{}) {
    v.address = key.Address   // via atomic store / under the field's RWMutex
}
```

Pair it with: (1) `Address()` reading through the same atomic/RWMutex, (2) reverting
`WrongPresentAddress_SelfHeals` to assert `internal_error` and adding a real-vault swapped-keystore
rejection test, (3) a `get_address` readiness signal for the undiscovered/zero case (H3), (4)
`IsHexAddress` validation on a present field (M1), and (5) `go test -race` in CI plus a
genuinely-overlapping concurrency test. This restores the dead integrity guard (H1), eliminates the
data race (H2), and reduces H3/M1/M2/L1 to doc/test cleanups.
