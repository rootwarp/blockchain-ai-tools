# Adversarial (Red-Team) Security Audit: eth-signer-mcp addr-opt-1 (post-fix, commit 1885e75 + feedback)

**Date**: 2026-06-13  
**Branch**: `feature/make-keystore-address-optional`  
**Commit**: `1885e7527f3aa1f8aaf1e6cbfd68265afd6f22ed`  
**Context**: This is the output of an adversarial (red-team) review performed after the `/proceed-dev-plan` implementation + initial reviewer + security-auditor pass for making the top-level `"address"` field in Web3 Secret Storage v3 keystores optional (to better align with the spec, which treats it as optional/unnecessary for privacy reasons — see `ethereum.org` documentation and the changes in the referenced commit).

The review was executed by spawning adversarial reviewer and security-auditor subagents with explicit instructions to be hostile: assume previous "fixes" (unconditional cache, best-effort docs, added tests, doc notes) were too lenient; hunt for remaining flaws, races, client-visible lies, insufficient coverage, and side-channels.

---

**Audit scope**: Hostile review of the *optional top-level "address" field* feature after the initial implementation + security review feedback round (changes in 1885e7527f3aa1f8aaf1e6cbfd68265afd6f22ed and follow-ups). Focus on real, exploitable or operationally damaging issues *not fully closed* by the prior review + fixes (unconditional `v.address = key.Address`, best-effort docs, two new tests in decrypt_test.go, doc updates for zero case and stale phrasing).

**Process followed (adversarial lens)**:
- Read the previous security audit (/tmp/grok-plan-review-8a45d882.md) in full, the commit message + full patch diff (via `git show 1885e75`), all post-fix source in `apps/eth-signer-mcp/internal/signing/{file_vault.go,decrypt.go,vault.go,signer.go,decrypt_test.go,...}`, `internal/server/{handlers.go,server.go,handlers_test.go,concurrent_test.go,...}`, `cmd/eth-signer-mcp/{main.go,config_test.go,main_test.go}`, README.md, testdata (keystore-no-address.json, keystore-empty-address.json, fixtures), and related e2e.
- Full data-flow tracing (boot no-pw parse → Address()/get_address/log; first WithSigningKey (pw+KDF+DecryptKey) → unconditional write under sem; sender check inside callback; MCP handlers; startup log).
- Adversarial traces per query: boot permissive HexToAddress(any non-empty) or zero; concurrent get_address/Address() from handlers while discovery write occurs; pre-sign get_address on no-addr (zero) and wrong-present (non-zero wrong); "best effort" visibility; format distinguishability (zero vs real) via unauth paths + logs; sender mismatch post-heal; whether new tests actually validate claims under realistic concurrency/adversarial input.
- Concrete reproduction: direct execution of `common.HexToAddress` probe on adversarial inputs (empty/partial/garbage/short/wrong); temporary mutation of the "concurrent readers" test + `go test -race -count=5 -run ...` (produced real DATA RACE report); inspection of test code vs. claims in comments/godocs/prior review; full package `go test -race` runs; grep for call sites, docs, and stale strings.
- Assumed attacker/careless operator: supplies or edits keystore JSON with missing/""/wrong/"garbage" top-level address (common per spec privacy note); issues concurrent unauth get_address + first sign; observes startup logs / MCP responses pre-sign; relies on get_address for "which account does this control?" before any passworded op.
- No source changes left in tree (temporary test edit for race repro was reverted via search_replace after capture; all runs used post-fix committed state except the probe exercise).
- Verified: `make lint` (0 issues), focused `go test -race` (signing/server/cmd), hex behavior, race repro.

Review file location: /tmp/grok-adversarial-review-addr-opt-1.md (this file; created for the adversarial pass as the prior was the plan-review file).

## Summary of Post-Fix State (Adversarial View)

The prior review identified 1 high (permanent wrong-address lock-in + DoS on sign via sender mismatch), 2 medium (visibility race on cache, keystore format side-channel), 1 low (operator confusion from zero), 1 informational (coverage gaps). Feedback applied "smallest change" mitigations: unconditional cache (self-heals wrong-present + absent), best-effort docs + minimal concurrent test, doc notes only for zero/side-channel (no behavior or log changes), no new mutex/atomics/RWMutex (to keep hot paths small).

**Core remaining problems (adversarial)**:
- The data race on the lazily-mutable `address` field ([20]byte plain read/write) is *real and reproducible under -race* during the exact transition the feature introduced. The "concurrent test" added does not exercise overlapping readers vs. the write.
- "Best effort" + "may briefly see zero or stale" is now *documented* but the visibility window enables concrete client-visible inconsistencies/lies between get_address (pre-sign or concurrent) and sign results / post-heal state, for both absent and (crafted) wrong-present cases.
- Distinguishability of "privacy-preserving keystore export" (no top-level address, per the ethereum.org / spec commentary already in code) remains a fully working, low-effort side-channel via unauthenticated (wrt pw) get_address and the mandatory startup "keystore loaded" log line. Docs note the zero contract but do not close the observability.
- Pre-sign get_address can still report wrong non-zero values (from permissive HexToAddress on any non-empty garbage/partial/short value in the JSON field) for "wrong-present" keystores until the *first* passworded sign. This is distinct from the zero/absent case documented in README.
- New tests + prior coverage do not validate the security claims (concurrency during transition, adversarial keystore content + full handler paths, -race on the racy window, pre-sign lying for wrong-present).
- Sender mismatch is now almost unreachable for address-field issues (good), but the inconsistency between what get_address says and what a successful sign result reports ("from") creates new trust/UX attack surface.
- Stale commentary in server.go tool registration ("boot-time keystore snapshot") and godocs for get_address/Address.

No new injection/auth bypass/crypto weakening/offline violations (ADR-007/depguard still hold; zeroing/ADR-009 patterns untouched). The change surface remains narrow. Risk is concentrated in the *new lazy-mutation + permissive-boot-parse* paths for a value that is returned over unauthenticated tool and in startup logs.

**Severity counts (this adversarial report)**:
- critical: 0
- high: 2
- medium: 3
- low: 2
- informational: 1

(The high items are the newly-proven data race + the pre-sign misinformation for wrong-present keystores, both with concrete repros and direct impact on correctness of address reported to callers and in logs.)

---

### Finding 1: Real Go data race on unsynchronized `fileKeyVault.address` read/write during lazy discovery for optional-address keystores

- **Severity**: high
- **Category**: Concurrency / Data race (Go memory model violation on cached public value); insufficient test coverage of claimed "best effort" fix
- **Location**: apps/eth-signer-mcp/internal/signing/file_vault.go:26 (field + struct comment), :93 (`return v.address`), apps/eth-signer-mcp/internal/signing/decrypt.go:150 (`v.address = key.Address` inside WithSigningKey after DecryptKey, under sem but visible to unsync readers), apps/eth-signer-mcp/internal/signing/vault.go:26 (godoc "best-effort visibility to concurrent readers"), apps/eth-signer-mcp/internal/signing/decrypt_test.go:846 (TestWithSigningKey_NoAddress_ConcurrentReaders and comment claiming "overlapping the first decrypt"), apps/eth-signer-mcp/internal/server/handlers.go:154 (get_address), signer.go:75/185 (Address + inside sign closure), cmd/main.go:252 (startup log), server/concurrent_test.go (uses only has-address light fixture)
- **Description**: `address` ([20]byte) is written exactly once (unconditionally, post first successful DecryptKey on optional-addr or wrong-present cases) inside the sem-protected writer path. All readers (plain `Address()` method, called by get_address handler, Signer.Address, sender check read (safe only because inside same goroutine post-write), startup log) are unsynchronized plain loads. The sem only serializes *KDF callers* (WithSigningKey entry); password-free get_address never acquires it. Go's memory model treats concurrent read + write of the struct as a data race (no happens-before). The prior "fixed" concurrent test launches readers *after* a synchronous discovery WithSigningKey, so the write and reads never overlap in the test; it only exercises post-write readers. The struct comment and godoc explicitly call the visibility "best-effort" (analogous to ADR-009) but do not prevent or detect the racy access.
- **Impact**:
  - `go test -race` (and TSAN in prod-like runs) reports a genuine DATA RACE on the field during the first sign on a no-address keystore while concurrent Address()/get_address calls are active. (Reproduced in this audit via targeted test mutation + 5x -race runs; stack: write in decrypt.go:150 by the discoverer goroutine vs. read in Address() by reader goroutines.)
  - Potential for torn/partial [20]byte reads in theory (on some arches/schedulers the 20-byte copy is not single-instruction); address bytes could mix pre- and post-discovery values, producing a bogus third address never present in the keystore or key.
  - Even without visible corruption, the race is a correctness and "undefined behavior" violation in Go for a security/audit-sensitive cached value exposed to MCP clients and logs.
  - The "added concurrent test" in the feedback round does not validate the visibility or race claims under the exact conditions the feature + docs describe.
- **Reproduction** (concrete, exercised in this audit):
  1. `cd apps/eth-signer-mcp/internal/signing`
  2. Temporarily restructure TestWithSigningKey_NoAddress_ConcurrentReaders (or equivalent) to launch N reader goroutines doing tight-loop `v.Address().Hex()` *before* firing the first `WithSigningKey` (the discovering writer), using wg + done chan (exact edit captured the race).
  3. `go test -race -count=5 -run '^TestWithSigningKey_NoAddress_ConcurrentReaders$' ./...` → produces "WARNING: DATA RACE", "Write at ... decrypt.go:150", "Previous read at ... Address() ... decrypt_test.go", goroutine creation stacks from the test.
  4. Revert shows the committed version of the test does not trigger (no overlap) → passes cleanly under -race, hiding the issue.
  5. Real usage: start signer on keystore-no-address.json; from parallel MCP sessions (or in server/concurrent_test style with a no-addr vault) issue get_address while the very first sign_transaction is in flight → timing-dependent race window (KDF for weak fixture is fast but still spans the write).
  6. Confirmed on the module's go-ethereum v1.17.3 + Go 1.26 toolchain.
- **Remediation**: Protect the single-writer / multi-reader field with a sync primitive that gives happens-before without new hot-path cost: e.g. `sync/atomic` (atomic.Pointer[common.Address] or store/load of the [20]byte via unsafe or a wrapper), or `sync.RWMutex` (RLock on Address(), Lock only for the one writer). Or accept a once-only write + document + add a build-tagged or race-only stress test that forces overlap (e.g. runtime.Gosched() around the assignment + spinning readers). At minimum, fix the test comment + implementation so the "concurrent readers" test actually launches readers overlapping the first WithSigningKey write (and asserts final consistency). Re-run full `go test -race` + server concurrent paths with no-addr fixture. Consider making initial boot address always zero for *all* optional/wrong cases and only advertise post-discovery (or always do a background decrypt for address only, but that changes pw-free + no-KDF-at-boot contract).
- **Status**: open (prior review documented "best effort" + added a non-overlapping test; the data race and test insufficiency were not detected/closed).

### Finding 2: Pre-first-sign get_address (and startup log) reports wrong non-zero address for keystores with a present but incorrect top-level "address" field (permissive HexToAddress + lazy heal only on first sign)

- **Severity**: high
- **Category**: Input validation / Trust of unauthenticated boot snapshot / Client-visible misinformation (pre-use)
- **Location**: apps/eth-signer-mcp/internal/signing/file_vault.go:68 (`if ks.Address != "" { addr = common.HexToAddress(ks.Address) }`), :93 (Address()), decrypt.go:146 (comment "self-heals any present-but-wrong"), 150 (unconditional write), vault.go:16 and :24 (godoc), signer.go:185 (keystoreAddr read inside sign fn, now post-write), handlers.go:154, cmd/main.go:252 (log), README.md:140/215 (only documents absent→zero case), testdata fixtures + TestWithSigningKey_WrongPresentAddress_SelfHeals (covers only the self-heal path, not pre-sign get_address lying with wrong non-zero)
- **Description**: Boot always does permissive `common.HexToAddress` on any non-empty top-level "address" string (no length/checksum/validation beyond what go-ethereum does). For absent/"" → zero (documented). For present-but-wrong (operator edit, bad export, supply-chain tampered keystore JSON, or even "0x1234", 19-byte hex, etc.) → a non-zero wrong value is stored in the initial snapshot. The unconditional heal only occurs *inside the first successful WithSigningKey*. Thus get_address, Signer.Address, and the "keystore loaded" log all report the wrong value until (and unless) a passworded sign occurs. The prior high-severity "permanent lock-in" is closed for *signing correctness* (sender check now sees healed value; sign succeeds), but the *pre-sign reporting* of wrong address over the pw-free path remains.
- **Impact**:
  - An operator (or monitoring) calling get_address before any sign_transaction sees a completely wrong account address (e.g. 0x...0001 or 0x...1234) and may believe the signer instance controls the wrong key.
  - Startup logs (always emitted) contain the wrong address.
  - Distinguishes "keystores that included a (stale/wrong) address field" from clean absent ones.
  - In adversarial supply-chain or careless edit scenarios, this is a client-visible lie that only self-corrects on first *use* of the password. No error or warning at boot for a present field that will be ignored/overwritten.
  - README and error tables only call out the zero/absent contract; wrong-present non-zero wrong values are not surfaced to operators.
  - Sender mismatch (internal_error) no longer fires for this case post-fix (improvement), but the inconsistency between get_address and successful sign result "from" field still exists during/around the first sign.
- **Reproduction** (concrete, using fixtures + probe):
  1. From audit hex probe: `common.HexToAddress("0x1234").Hex()` == "0x0000000000000000000000000000000000001234" (non-zero); `common.HexToAddress("0x" + strings.Repeat("11",19)).Hex()` yields leading-00 + 19 bytes (non-zero); "garbage!!!" or "0xZZZZ" → zero (treated like absent).
  2. Take keystore-weak.json, replace the address value with `"0000000000000000000000000000000000000001"` (or "0x1234"), write temp file (as done in TestWithSigningKey_WrongPresentAddress_SelfHeals).
  3. `NewFileKeyVault(...)`; call `vault.Address().Hex()` → reports the wrong non-zero (e.g. "...0001").
  4. Call get_address via real signer + in-memory MCP session (or just vault) pre-sign → wrong value in AddressResult.
  5. `logger.Info` at main.go:252 shows the wrong value.
  6. Only after `sign_transaction` (successful decrypt) does subsequent get_address + Address() report the real fixture address. The sign result "from" is always the real recovered sender.
  7. With no-address fixture: zero (as documented). Wrong-present is a distinct (and more misleading) non-zero lie.
- **Remediation**: At boot, for a *present* non-empty "address" field, optionally parse + compare against a no-KDF fast path if possible (but spec intentionally makes the field redundant), or *always initialize the cache to zero* for the optional-address feature and treat *any* pre-sign value as best-effort/possibly-stale (document uniformly). Emit a one-time info log (or warning) on discovery if the boot snapshot differed from the decrypted key.Address. Update README + tool description + godoc to explicitly call out "may report zero or a stale/wrong value from the top-level field until first successful sign_transaction". Add coverage: pre-sign get_address assertions on a wrong-present temp keystore in handler tests or decrypt tests using real vault. Consider failing loud at boot on present-but-non-matching if the privacy note is not taken seriously (but this would regress the "optional per spec" goal).
- **Status**: open (prior fix closed the *permanent* + *sign-DoS* half of Finding 1 in the plan-review; pre-sign misinformation + lack of docs/warnings for the wrong-present non-zero case was not addressed).

### Finding 3: The added "concurrent readers" test (and overall new test surface) does not validate the security/visibility claims under realistic concurrency or with full MCP handler paths + adversarial keystores

- **Severity**: medium
- **Category**: Insufficient test coverage / Validation gap for concurrency and new feature paths
- **Location**: apps/eth-signer-mcp/internal/signing/decrypt_test.go:846 (the test + its comment "overlapping the first decrypt on a no-addr vault"), 800 (WrongPresent test is sequential only, exercises heal but not pre-sign get_address or handler), server/handlers_test.go (get_address tests use stubs or standard has-address real vault; TestGetAddress_UnreadablePasswordFile etc. never use no-addr or wrong-present), server/concurrent_test.go (always keystore-light.json with address at boot; no no-addr variant), no -race specific stress in server paths for the new mutable state.
- **Description**: The feedback-round test for Finding 2/5 in the plan-review claims to exercise "concurrent Address() calls overlapping the first decrypt". The actual code (post-revert) does a synchronous discovery first, *then* spawns readers. The wrong-present self-heal test is single-threaded and only checks post-heal state (not that get_address lied with the wrong value pre-sign, or concurrent views). No tests combine real FileKeyVault (no-addr or crafted wrong-present) + MCP handler (makeGetAddressHandler + makeSignTransactionHandler) + concurrent CallTool(get_address) overlapping the first sign_transaction. The ADR-006 concurrent test (server/concurrent_test.go) and leak audits use only has-address fixtures. `go test -race` on the packages passes for existing tests (as they avoid the window), but the data race (Finding 1) and visibility inconsistencies are unexercised in the committed tree.
- **Impact**: Regressions in the lazy-discovery logic, race introduction on the address field, or accidental re-locking of wrong address would not be caught by `make test` / CI. The "new tests actually validate the security claims" question (per query) has the answer "no" for the concurrency and adversarial-input dimensions. Operators relying on get_address for address discovery in no-addr or edited-keystore deployments have no end-to-end assurance.
- **Reproduction**:
  1. Inspect decrypt_test.go:861 (the "discover synchronously" line before spawning readers) vs. the godoc/comment claiming overlap.
  2. `go test -race ...` (focused on the three new tests) passes cleanly in the committed state.
  3. Modify (as in this audit) to force overlap → race is immediately reported.
  4. No handler-level test calls get_address on a no-addr real vault before/ during sign (grep confirms).
  5. server/concurrent_test.go hardcodes keystore-light.json (has address).
- **Remediation**: Make the concurrent test actually overlap (launch readers first, discover in parallel goroutine, sample many times, assert final consistency + that at least the post-sign state is correct for all). Add table-driven or explicit coverage in handlers_test.go (or a new no-addr variant of concurrent_test) using real no-addr + wrong-present temp keystores + in-memory sessions + concurrent get_address + sign bursts. Gate or run the address-transition concurrency under `-race` explicitly in CI if needed. Assert in tests that pre-sign get_address on wrong-present returns the boot wrong value (not just post-heal).
- **Status**: open (prior review listed this as informational Finding 5 and claimed "closed" by the two tests + make test under race; adversarial execution shows the tests are too weak to support the claims).

### Finding 4: Keystore format choice (presence/absence of top-level "address" field) remains distinguishable via unauthenticated get_address responses and the mandatory startup log line (side-channel on privacy choice)

- **Severity**: medium
- **Category**: Information disclosure / Side-channel (observable state distinguishing "privacy-preserving" vs. standard keystore export)
- **Location**: apps/eth-signer-mcp/cmd/eth-signer-mcp/main.go:252 (`logger.Info("keystore loaded", "address", vault.Address().Hex())`), internal/server/handlers.go:154 (`addr := sp.Address()` → AddressResult), file_vault.go + decrypt.go (zero-until-discovery contract), vault.go:24 (godoc), README.md:140/215 (public contract), server.go:88/94 (tool description "boot-time keystore snapshot"), e2e tests and demo logs that capture the line.
- **Description**: Pre-change, every successful boot for a valid keystore produced a non-zero Address() at construction (field was mandatory). Post-change (and post "fix"), a valid keystore omitting the field (or "") legitimately yields exactly the zero address in:
  - The first structured log after startup ("keystore loaded").
  - Every get_address response (pw-free, bearer-auth for HTTP or direct for stdio) until the *first* successful sign_transaction.
  - After discovery the in-memory cache holds the real value (subsequent restarts still start at zero).
  The prior review called this a medium side-channel (the field "compromises privacy" per comments citing ethereum.org / Web3 spec; official vectors omit it). Feedback "fixed" via doc notes only (vault godoc, lifecycle, README contract, internal doc.go/errors.go stale phrasing); no change to logging, no redaction, no "presence only" indicator, no redesign of pw-free get_address.
- **Impact**: An observer with access to process stdout/stderr (startup), or the ability to call get_address (via MCP, which for HTTP still goes through bearer but the zero reveals the on-disk choice), can reliably fingerprint whether the operator exported/created the keystore with or without the redundant top-level address field. This was not observable before. No key material is leaked, but metadata about the operator's keystore hygiene/privacy choice is now remotely (or locally via logs) distinguishable without reading the file or knowing the password. In a fleet or shared-log environment this is a persistent signal.
- **Reproduction** (straightforward, matches prior review Finding 3):
  1. Start binary once with keystore-weak.json (has address) → "keystore loaded" line + get_address contain the real EIP-55 fixture addr.
  2. Start with keystore-no-address.json (or empty-address) → both report exactly "0x0000000000000000000000000000000000000000".
  3. Pre-sign get_address on no-addr instance returns zero in the JSON result; post one sign_transaction it returns real (and stays real until restart).
  4. The README now advertises the zero-until-sign contract, but this makes the distinction *official and expected* rather than closing it.
- **Remediation**: Do not log the raw address at startup (log only a presence indicator, a truncated hash, or omit the field for the optional case). For get_address, document the zero case more prominently in the tool description (already partially done). If the side-channel is unacceptable, fall back to always caching the decrypted address at boot (changes "no pw at construction" and "get_address never triggers KDF" properties, which were locked earlier). Or expose an explicit "addressSource": "boot" | "discovered" in the AddressResult (but this is a new feature). Accept as inherent to the "optional + pw-free get_address + no KDF at boot" design and ensure operator docs call out the privacy implication of the observable zero.
- **Status**: open (prior review treated as "fixed" by docs; behavior and log exposure unchanged, so the channel remains fully functional and now explicitly part of the supported contract).

### Finding 5: Stale/outdated godoc and tool descriptions still refer to a pure "boot-time keystore snapshot" for get_address / Address, despite the lazy mutation on optional-addr keystores

- **Severity**: low
- **Category**: Documentation drift / Misleading contract description
- **Location**: apps/eth-signer-mcp/internal/server/server.go:88 (comment), :94 (Description string for the get_address tool), apps/eth-signer-mcp/internal/signing/vault.go:23 (KeyVault.Address godoc), file_vault.go:89 (Address godoc), main.go comments, various e2e/demo text that copy the "snapshot" language.
- **Description**: Multiple places still say "Served from the boot-time keystore snapshot" or "the account address from the boot-time keystore snapshot" for get_address and Address(). This was accurate pre-addr-opt (immutable after construction). Post-change, for absent/empty/wrong-present cases the value is mutated (best-effort) on first successful WithSigningKey; subsequent get_address returns the discovered value. The vault.go and file_vault.go godocs were lightly updated to mention "or the value discovered...", but the server registration (the public tool surface) and some handler comments were not fully refreshed. README does a better job of describing the zero-until-sign behaviour.
- **Impact**: Minor confusion for code readers and (via the MCP tool description returned on tools/list) for client implementors. The description claims a stronger "snapshot" immutability than the implementation + optional-addr contract provides. Not directly exploitable but contributes to incorrect mental model of when the address is authoritative.
- **Reproduction**: Start server, call tools/list, inspect the "get_address" tool Description text (contains "boot-time keystore snapshot"); grep the source for the phrase in server.go vs. the mutation logic in decrypt.go + file_vault.
- **Remediation**: Update the tool Description and registration comments in server.go (and any handler docs) to match the vault godoc: "Return the EIP-55 ... (from boot snapshot or discovered on first successful sign for keystores omitting the optional top-level address field). This is a read-only ...". Keep changes minimal and consistent with "smallest diff" style.
- **Status**: open (minor doc debt left after the feedback round).

### Finding 6: Inconsistency between get_address responses and successful sign_transaction "from" results around the first discovery (client-visible "lie" even when signing succeeds)

- **Severity**: low
- **Category**: Usability / Trust / Output inconsistency for optional-addr and wrong-present keystores
- **Location**: signer.go:211 (`From: sender.Hex()` — always the real recovered sender from inside the callback), 185 (mismatch check uses post-write vault.Address()), handlers.go (get_address always current vault.Address()), result.go (SignResult doc), e2e tests, README contract.
- **Description**: Inside the sign fn (after the unconditional `v.address = ...` write), the code recovers `sender` from the just-signed tx and puts the *real* address into SignResult.From (and the audit log). The mismatch check also sees the healed value. However, any concurrent (or prior) get_address call sees whatever the unsynchronized / best-effort cache held at that instant (zero, wrong-present boot value, or the new value). A client that calls get_address, then sign_transaction, may receive a result whose "from" differs from the prior get_address response. Even sequential pre-sign get_address + successful sign produces this (pre = zero/wrong, sign "from" = real).
- **Impact**: Clients and operators can observe the signer "lying" about its own address until the first use. This is by design for the optional case (and now also wrong-present), but increases the chance of confusion, incorrect assumptions in client code ("the address I got from get_address is the one that will appear in sign results"), or false "the signer controls address X" beliefs. The SignResult is authoritative for the actual signature produced; get_address is the one that can be stale.
- **Reproduction**: Use no-addr or wrong-present temp keystore + real vault + in-memory session:
  - Call get_address → zero (or wrong non-zero).
  - Call sign_transaction (valid tx) → succeeds, SignResult.From == real fixture, "from" in result differs from the get_address value just obtained.
  - Post-sign get_address now matches the "from".
  - Concurrent: two sessions, one get_address + one sign overlapping → non-deterministic which address the get_address client sees.
- **Remediation**: Document more explicitly in the get_address tool description and SignResult godoc that "get_address may return the zero address (or a stale value from a present top-level field) until the first successful sign_transaction; the 'from' in a SignResult is always the address that actually signed". Consider adding a note or "discovered":true flag, but avoid new surface. In practice the sign path is the one that "proves" the address via a real signature.
- **Status**: open (inherent to the lazy-discovery contract; prior docs updates covered the zero case in README but not the cross-tool inconsistency or wrong-present non-zero case in all surfaces).

### Finding 7 (informational): Permissive boot parse + zero-vs-real distinguishability + "best effort" creates operational gotchas for monitoring, clients, and no-sign deployments even for fully valid keystores

- **Severity**: informational (compounds the above)
- **Category**: Operational / Monitoring / Edge-case contract
- **Location**: All the above + testdata/README.md (documents the fixtures), cmd/main_test.go + config_test.go (assert zero only in specific no-addr tests), e2e http/stdio that snapshot logs.
- **Description**: A production keystore legitimately created with `geth account new` (or any exporter that follows the "optional/unnecessary for privacy" guidance) will cause the signer to emit the zero address in its very first log line and in get_address responses until its first real signing call. Servers that are only used for get_address (query the address, never sign) stay at zero forever. Monitoring expecting a non-zero post-"keystore loaded" will false-positive. The prior low Finding 4 was "fixed" by internal docs; the behavior and lack of a discovery log event remain.
- **Impact**: Operational friction, alert fatigue, client code that treats the first get_address as authoritative. No direct exploit beyond the side-channel and misinformation already noted.
- **Reproduction**: Start with a real no-address production-style keystore (copy of weak with field removed) → observe zero in stderr JSON log and get_address.
- **Remediation**: As in prior low item: prominent operator README call-out (beyond the current table); optional one-time "address discovered" info log on the first heal (only when boot value was zero or differed); or accept and live with it.
- **Status**: open (accepted consequence of the design per prior round; still worth calling out in adversarial summary).

## Positive / Closed Items Noted (for completeness)

- Unconditional cache closes the original high "permanent wrong address + sign DoS" (sender mismatch no longer triggers for wrong-present; post-sign everything authoritative).
- Offline guarantee, depguard, zeroing (ADR-009), semaphore serialization of KDF, pw re-read, fail-fast on real I/O/malformed JSON, bearer/loopback/MaxBytes, etc. all intact.
- make lint / build / basic tests green.
- README and some internal docs now correctly surface the zero-until-sign contract for absent case.
- Wrong-present self-heals on use (the test exercises this).

## Final Summary

- review_file: /tmp/grok-adversarial-review-addr-opt-1.md
- Overall risk assessment: **Moderate, trending high on the concurrency + pre-use misinformation surfaces**. The feature correctly implements "optional per spec + lazy discovery" with smallest changes and heals the worst signing breakage, but the post-fix state leaves a *reproducible data race*, *insufficient test validation of the exact new paths*, *client- and log-visible address lies for both absent and wrong-present keystores before first use*, and an *unclosed side-channel on keystore privacy choice*. These have concrete reproductions (race detector output, hex probe, code inspection, timing-dependent MCP calls) and real impact on "signing correctness" (via reports), "client-visible lies", and new side-channels — exactly the priorities for this adversarial pass. The "best effort" documentation acknowledges but does not mitigate the timing-dependent and pre-sign problems. No criticals (no key escape, no auth bypass, no crypto weakening).

**Top 2-3 worst remaining issues**:
1. **Data race on the address cache (Finding 1)** — proven under -race with the exact no-addr + overlapping get_address pattern the feature + docs describe. The test added to "close" visibility does not exercise it. Direct violation of Go memory model on a value returned to unauthenticated callers.
2. **Pre-sign address misinformation for wrong-present + (documented) zero cases (Findings 2 + 6)** — get_address and startup logs can (and do) report values that are not the real key address, for valid or adversarially-crafted keystores, until the first passworded operation. Clients see inconsistency between get_address and sign "from". README only partially covers it.
3. **Unclosed format side-channel + weak test coverage (Findings 3 + 4)** — zero vs. real reliably distinguishes privacy choice in logs and pw-free tool; the new tests do not stress the transition under concurrency or full handler + adversarial input, so claims are not validated.

All findings include file:line citations, reproduction steps exercised in the audit, and concrete remediation aligned with the existing architecture (sem, best-effort zeroing, no new surface where possible). The prior review + fixes addressed the most obvious "permanent DoS" case but left the timing, visibility, testing, and observability problems open under an adversarial lens.

(End of adversarial review.)
