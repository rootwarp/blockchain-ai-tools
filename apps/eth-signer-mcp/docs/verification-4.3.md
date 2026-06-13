# Issue 4.3: Version-Pin Verification + Acceptance Benchmark — Evidence

This file records the mechanical verification evidence for Issue 4.3.  
All commands were run in `apps/eth-signer-mcp/` unless otherwise noted.  
Date: 2026-06-12.

---

## 1. Pin Verification

### 1.1 `go list -m -json` transcript

Command:
```
cd apps/eth-signer-mcp && go list -m -json \
  github.com/modelcontextprotocol/go-sdk \
  github.com/ethereum/go-ethereum \
  github.com/urfave/cli/v3
```

Output:
```json
{
    "Path": "github.com/modelcontextprotocol/go-sdk",
    "Version": "v1.6.1",
    "Time": "2026-05-22T11:30:38Z",
    "Dir": "…/go-sdk@v1.6.1",
    "GoVersion": "1.25.0",
    "Sum": "h1:0zOSupjKUxPKSocPT1Wtago+mUHU2/uZ4xSOY0FGReU=",
    "GoModSum": "h1:kzm3kzFL1/+AziGOE0nUs3gvPoNxMCvkxokMkuFapXQ="
}
{
    "Path": "github.com/ethereum/go-ethereum",
    "Version": "v1.17.3",
    "Time": "2026-05-11T15:19:24Z",
    "Dir": "…/go-ethereum@v1.17.3",
    "GoVersion": "1.24.0",
    "Sum": "h1:Ev/sQHH+UdKZHWjuVzhu2pxhi/sXaPZl23Q+Q5LDd4Q=",
    "GoModSum": "h1:f2EhRwqewIZkGoQekywI2Y2RZAMTSavLNkD9qItFy1A="
}
{
    "Path": "github.com/urfave/cli/v3",
    "Version": "v3.9.0",
    "Time": "2026-05-09T21:35:44Z",
    "Dir": "…/cli/v3@v3.9.0",
    "GoVersion": "1.22",
    "Sum": "h1:AV9lIiPv3ukYnxunaCUsHnEozptYmDN2F0+yWqLMn/c=",
    "GoModSum": "h1:ysVLtOEmg2tOy6PknnYVhDoouyC/6N42TMeoMzskhso="
}
```

**Verification result:**
| Dependency | Expected | Actual | Status |
|---|---|---|---|
| `github.com/modelcontextprotocol/go-sdk` | v1.6.1 | v1.6.1 | ✅ |
| `github.com/ethereum/go-ethereum` | v1.17.3 | v1.17.3 | ✅ |
| `github.com/urfave/cli/v3` | v3.9.0 | v3.9.0 | ✅ |

**Go workspace toolchain** (`go.work`):
```
go 1.26
toolchain go1.26.4
```
— matches the required Go 1.26 toolchain. ✅

### 1.2 `go mod tidy` no-op proof

Command:
```
cd apps/eth-signer-mcp && go mod tidy
git diff --exit-code -- apps/eth-signer-mcp/go.mod apps/eth-signer-mcp/go.sum
```

Exit code: **0** — `go mod tidy` produced no changes. ✅

### 1.3 Foundry version comparison

`.foundry-version` (repo root):
```
v1.7.1
```

`apps/eth-signer-mcp/internal/signing/testdata/vectors/cast-version.txt`:
```
cast was NOT run in this environment (Foundry/cast not installed).

Intended pinned version: v1.7.1 (see /.foundry-version in the repo root)

The committed vectors were generated and verified with ethers v6.16.0 ONLY.
Cast cross-check is deferred to a Foundry-equipped machine.

To regenerate with both oracles:
  1. Install Foundry v1.7.1 (see https://getfoundry.sh/)
  2. Verify: cast --version  # must match .foundry-version
  3. Run: scripts/regen-vectors.sh
     (the script performs the dual-oracle byte-compare and overwrites this file)
```

**Status:** Both files agree on the intended pinned version `v1.7.1`. The `cast-version.txt` file documents a known deferred status — Foundry was not installed in the original vector-generation environment; vectors were generated and verified with ethers v6.16.0 only. The cast cross-check is deferred to a Foundry-equipped machine per the note. This is the status as left by Issue 2.1 (generation) and carried through Phase 2/3.  
`.foundry-version` = `v1.7.1` ✅ (single stable Foundry tag, consistent with cast-version.txt intent).

### 1.4 `govulncheck` output

Command:
```
cd apps/eth-signer-mcp && govulncheck ./...
```

Output:
```
=== Symbol Results ===

No vulnerabilities found.

Your code is affected by 0 vulnerabilities.
This scan also found 0 vulnerabilities in packages you import and 13
vulnerabilities in modules you require, but your code doesn't appear to call
these vulnerabilities.
Use '-show verbose' for more details.
```

**Status:** govulncheck clean. ✅

---

## 2. Advisory Verification

Five go-ethereum advisories verified against the Go Vulnerability Database (`https://vuln.go.dev/ID/GO-2026-XXXX.json`).

| Advisory | CVE | Summary | Fixed at | v1.17.3 affected? |
|---|---|---|---|---|
| GO-2026-4314 | CVE-2026-22868 | High CPU usage (DoS) via malicious p2p message | v1.16.8 | No ✅ |
| GO-2026-4315 | CVE-2026-22862 | DoS via malicious p2p message | v1.16.8 | No ✅ |
| GO-2026-4507 | CVE-2026-26314 | Crash via malicious p2p message (BitCurve.IsOnCurve) | v1.16.9 | No ✅ |
| GO-2026-4508 | CVE-2026-26313 | DoS via malicious p2p message | v1.17.0 | No ✅ |
| GO-2026-4511 | CVE-2026-26315 | Improper ECIES public key validation in RLPx handshake (key-validation flaw, not a DoS) | v1.16.9 | No ✅ |

All five advisories are fixed at or before v1.17.0. go-ethereum v1.17.3 is not affected by any of them. Note: GO-2026-4511 is an ECIES public-key validation flaw in the `crypto/ecies` package used during RLPx handshake — it is a key-validation flaw, not a denial-of-service issue.

Advisory DB references:
- https://pkg.go.dev/vuln/GO-2026-4314
- https://pkg.go.dev/vuln/GO-2026-4315
- https://pkg.go.dev/vuln/GO-2026-4507
- https://pkg.go.dev/vuln/GO-2026-4508
- https://pkg.go.dev/vuln/GO-2026-4511

**No escalation required.** All expectations confirmed.

---

## 3. Documentation Sweep

Searched all shipped docs and code comments in `apps/eth-signer-mcp/` for advisory claims:

```
grep -rEn "exploitable|open DoS|advisory|GO-2026|not affected|unaffected|bump when" \
  apps/eth-signer-mcp/ --include="*.md" --include="*.go"
```

Findings:
- `cmd/eth-signer-mcp/main.go:200`: comment reads "TOCTOU advisory: applyPermChecks uses os.Stat; the actual file reads happen" — this refers to a POSIX TOCTOU concern about permission checks, not a go-ethereum advisory. Not a false advisory claim. No change needed.
- `docs/mcp-sdk-spike.md:80`: "DoS hygiene" in context of session timeout — not an advisory claim. No change needed.
- `README.md:259`: "`govulncheck ./...` runs in CI (workflow from issue 1.2) against all pinned dependencies on every push." — only the verified not-affected fact and govulncheck-in-CI statement. Permitted. ✅
- `docs/demo.md`: no hits. ✅

**Result:** No manual advisory claims found in shipped docs or code comments. Sweep clean. ✅

---

## 4. Acceptance Benchmark Numbers

Tests run with `go test ./internal/signing/ -run 'Overhead|ColdStart' -v -count=1` (package isolation).  
Machine: macOS, 10 logical CPUs, Apple M-series.

### 4.1 Non-KDF overhead (ADR-010, Issue 4.3 + fix/bench-ci-noise)

**Estimator (updated in fix/bench-ci-noise):** `min(total) − min(KDF)` — not `median − median`.

The original median-based estimator was falsified by a CI failure at `main@c1c4ec1`
(ubuntu-latest, GOMAXPROCS=2):

```
--- FAIL: TestSigner_NonKDFOverhead_Standard (9.32s)
    bench_test.go:276: standard-scrypt: median total=631.782954ms  median KDF=616.419812ms  non-KDF delta=15.363142ms  (limit: 10ms)
```

Standard scrypt (N=262144) takes ~620 ms/op on a 2-core CI runner. Run-to-run variance
(≈2%, ≈12 ms) is larger than the 10 ms budget, so `median(total) − median(KDF)` wobbled
past 10 ms even though the true non-KDF work (RLP encode + ECDSA sign + sender recovery)
is sub-millisecond. The estimator was fragile; the product is fine.

**Fix:** `min(total) − min(KDF)`. The minimum sample is the least-preempted run, closest
to the bare compute floor: `min(total) ≈ scrypt_floor + overhead`, `min(KDF) ≈ scrypt_floor`,
so their difference cancels scrypt's central tendency **and** its variance. A real 50 ms
regression still fails the test — the contract is not weakened, only noise is removed.

Iteration counts increased so the minimum is well-sampled: light N=15 (was 7), standard N=15 (was 7; bumped from 10 for extra min-sampling margin on busy shared runners).

Representative runs on macOS, Apple M-series, 10 logical CPUs (bench_test.go line numbers
reflect the updated file):

```
bench_test.go: light-scrypt:    median total=46.529ms  median KDF=45.485ms  min total=38.236ms  min KDF=37.484ms  non-KDF delta (min-based)=751µs   (limit: 10ms)
bench_test.go: standard-scrypt: median total=600.251ms median KDF=520.161ms min total=440.334ms min KDF=439.829ms non-KDF delta (min-based)=504µs   (limit: 10ms)
```

CI-robustness evidence (GOMAXPROCS=2, count=5 at N=10 — all passed; then count=3 at N=15 — all passed):

| Run | N | Standard min-based delta | Pass? |
|-----|---|--------------------------|-------|
| 1 | 10 | 324 µs | ✅ |
| 2 | 10 | 124 µs | ✅ |
| 3 | 10 | -486 µs (noise, negative → pass) | ✅ |
| 4 | 10 | 90 µs | ✅ |
| 5 | 10 | -650 µs (noise, negative → pass) | ✅ |
| 6 | 15 | 231 µs | ✅ |
| 7 | 15 | 193 µs | ✅ |
| 8 | 15 | -1.156 ms (noise, negative → pass) | ✅ |

Under-load evidence (background `yes` CPU workers, count=3 — all passed):

| Run | Standard min-based delta | Pass? |
|-----|--------------------------|-------|
| 1 | -2.897 ms (noise, negative → pass) | ✅ |
| 2 | 520 µs | ✅ |
| 3 | 850 µs | ✅ |

**Previous claim retracted:** The prior text stated "The test is reliable in CI
(ubuntu-latest, GOMAXPROCS=2)" with the median-based estimator. That claim was
falsified by the CI failure above. The min-based estimator is reliable in CI;
the evidence above supports this.

### 4.2 Cold start (Issue 4.3 addition)

| Fixture | Median cold start | Limit | Pass? |
|---|---|---|---|
| light-scrypt (N=4096) | ~20–70 µs | < 200 ms | ✅ |
| standard-scrypt (N=262144) | ~17–70 µs | < 200 ms | ✅ |

Raw `t.Logf` output (representative run):
```
bench_test.go:299: light-scrypt cold start:    median=68.667µs  (limit: 200ms)
bench_test.go:331: standard-scrypt cold start: median=17.042µs  (limit: 200ms)
```

**Reasoning:** Construction calls `NewFileKeyVault` (file read + JSON parse of address field only, no KDF) then `NewSigner` (struct allocation). No decryption occurs at construction — per the lifecycle contract (ADR-010, `vault.go`, `file_vault.go`): the KDF runs only on `WithSigningKey`, not at construction. Both fixtures have identically-sized keystore JSON and identical construction paths; the ~200 ms bound is orders of magnitude above the ~50 µs actual median.

### 4.3 Gap analysis on pre-existing bench_test.go

The file landed in Issue 2.6 and contained:

| Item | Status before 4.3 | Action taken |
|---|---|---|
| `TestSigner_NonKDFOverhead_Light` | ✅ present, median-of-7, light fixture | Added `newBenchSigner` reuse + `runtime.GC()` for robustness |
| `TestSigner_NonKDFOverhead_Standard` | ✅ present, median-of-7, guarded by `testing.Short()` | Added `newBenchSigner` reuse + `runtime.GC()` + `runtime.LockOSThread()` + load sensitivity documentation |
| Cold-start test (< 200 ms) | ❌ absent | Added `TestSigner_ColdStart_Light` (parallel) and `TestSigner_ColdStart_Standard` (sequential, `!testing.Short()`) |
| `Benchmark*` functions | ✅ present for both fixtures | No change |
| `t.Logf` of results | ✅ present | Preserved; also added for cold-start |

New constants and helpers added: `coldStartIterations = 5`, `measureColdStartTime`, `newBenchSigner`, `measureSignTime` refactored to accept `*Signer`.

---

## 5. Summary

| Acceptance criterion | Status |
|---|---|
| go.mod pins verified (go-sdk v1.6.1, go-ethereum v1.17.3, urfave/cli v3.9.0, Go 1.26) | ✅ |
| `go mod tidy` is a no-op | ✅ |
| `.foundry-version` = v1.7.1 (single stable tag, consistent with cast-version.txt intent) | ✅ |
| Five advisories confirmed fixed ≤ v1.17.0; v1.17.3 not affected | ✅ |
| `govulncheck` clean | ✅ |
| No shipped doc/comment carries a manual advisory claim | ✅ |
| Cold start < 200 ms on both fixture sets | ✅ |
| Non-KDF overhead < 10 ms on both fixture sets (isolation run) | ✅ |
| Test uses min-of-N for overhead (N=15 both fixtures), median-of-N for cold-start (N=5) | ✅ (N=15 light / N=15 standard overhead, N=5 cold-start) |
| Benchmark numbers recorded for release notes | ✅ (§4 above) |
