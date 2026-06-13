// Benchmark and overhead-assertion tests — Issue 2.6 (ADR-010); extended in Issue 4.3.
// CI-noise fix in fix/bench-ci-noise: switched overhead estimator from median-based to
// min-based (see WHY-MIN comment blocks inside the overhead tests for the full rationale).
//
// Design: we measure (a) total SignTransaction time and (b) KDF-only time
// (timing keystore.DecryptKey directly on the same fixture). The non-KDF overhead
// is (a − b). Per ADR-010, this delta must be < 10 ms on BOTH standard- and
// light-scrypt fixtures. The assertion uses min(total) − min(KDF) — see WHY-MIN.
//
// Issue 4.3 adds cold-start tests: construct vault + signer from fixture paths and
// assert median construction time < 200 ms. Construction is a file-read + JSON parse
// (no KDF — the keystore ciphertext is decrypted on every WithSigningKey call, not at
// construction); both fixtures therefore meet the 200 ms bound with orders of magnitude
// to spare.
//
// The standard-scrypt fixture (N=262144) is skipped under -short because its
// KDF alone takes ~0.5–1 s per call, making the full measurement very slow.
// Standard cold-start is also guarded by testing.Short() for structural consistency with
// the rest of the standard-scrypt tests, even though construction itself has no KDF cost.
package signing

import (
	"context"
	"os"
	"runtime"
	"sort"
	"testing"
	"time"

	gokeystore "github.com/ethereum/go-ethereum/accounts/keystore"
)

// lightOverheadIterations is the number of iterations for the light-scrypt overhead
// test (each op ~50 ms). 15 gives a well-sampled minimum without being slow.
const lightOverheadIterations = 15

// standardOverheadIterations is the number of iterations for the standard-scrypt
// overhead test. Each op takes ~0.6–1 s; 15 iterations (~15–20 s total in CI)
// gives extra min-sampling margin so the minimum has more chances to catch a clean
// non-KDF window even on a busy shared runner.
const standardOverheadIterations = 15

// coldStartIterations is the number of iterations for the cold-start median.
// Construction is fast (I/O + JSON parse only, no KDF), so 5 iterations is enough.
const coldStartIterations = 5

// medianDuration returns the median of a slice of durations.
func medianDuration(ds []time.Duration) time.Duration {
	if len(ds) == 0 {
		return 0
	}
	sorted := make([]time.Duration, len(ds))
	copy(sorted, ds)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i] < sorted[j] })
	return sorted[len(sorted)/2]
}

// minDuration returns the minimum of a slice of durations.
func minDuration(ds []time.Duration) time.Duration {
	if len(ds) == 0 {
		return 0
	}
	m := ds[0]
	for _, d := range ds[1:] {
		if d < m {
			m = d
		}
	}
	return m
}

// readFileForBench reads a file for use in benchmarks/test helpers.
func readFileForBench(path string) ([]byte, error) {
	return os.ReadFile(path)
}

// measureColdStartTime times one full vault + signer construction from fixture paths.
// Construction reads the keystore file, parses the address field, and creates the
// Signer struct — no KDF decryption occurs (per the lifecycle contract in ADR-010:
// the password file is re-read and the KDF runs only on WithSigningKey, not at
// construction). The 200 ms bound is therefore generous on any developer-class machine.
func measureColdStartTime(t testing.TB, keystorePath, passwordPath string) time.Duration {
	t.Helper()
	start := time.Now()
	vault, err := NewFileKeyVault(VaultOptions{
		KeystorePath: keystorePath,
		PasswordPath: passwordPath,
	})
	if err != nil {
		t.Fatalf("NewFileKeyVault: %v", err)
	}
	_ = NewSigner(vault, SignerOptions{})
	return time.Since(start)
}

// measureKDFTime times a single keystore.DecryptKey call (no vault overhead).
// keystoreJSON must be the already-loaded keystore bytes — passing the same slice
// that the vault snapshot uses (see overheadTimings) avoids any I/O asymmetry
// between the KDF and total measurements.
func measureKDFTime(t testing.TB, keystoreJSON []byte, password string) time.Duration {
	t.Helper()
	start := time.Now()
	key, decErr := gokeystore.DecryptKey(keystoreJSON, password)
	elapsed := time.Since(start)
	if decErr != nil {
		t.Fatalf("DecryptKey: %v", decErr)
	}
	ZeroBigInt(key.PrivateKey.D) //nolint:staticcheck // ADR-009: zeroing after use
	return elapsed
}

// newBenchSigner constructs a Signer from fixture paths for use in overhead
// and benchmark measurements. The vault is constructed once; the same signer
// is reused across all iterations to match the production usage pattern (vault
// created at startup, sign many times). Vault construction cost is measured
// separately by the cold-start tests.
func newBenchSigner(t testing.TB, keystorePath, passwordPath string) *Signer {
	t.Helper()
	vault, err := NewFileKeyVault(VaultOptions{
		KeystorePath: keystorePath,
		PasswordPath: passwordPath,
	})
	if err != nil {
		t.Fatalf("NewFileKeyVault: %v", err)
	}
	return NewSigner(vault, SignerOptions{}) // nil logger → slog.Default()
}

// measureSignTime times one full end-to-end SignTransaction call on a pre-built
// Signer. The Signer must be created once before the timing loop (see
// newBenchSigner); reusing it across iterations matches the real production
// pattern and avoids counting vault-construction I/O in the per-call delta.
func measureSignTime(t testing.TB, s *Signer) time.Duration {
	t.Helper()

	req := TxRequest{
		Type:     "legacy",
		ChainID:  "1",
		Nonce:    "0",
		To:       "0x9858EfFD232B4033E47d90003D41EC34EcaEda94",
		Value:    "0",
		Data:     "0x",
		Gas:      "21000",
		GasPrice: "1000000000",
	}

	start := time.Now()
	_, signErr := s.SignTransaction(context.Background(), req)
	elapsed := time.Since(start)
	if signErr != nil {
		t.Fatalf("SignTransaction: %v", signErr)
	}
	return elapsed
}

// ── Guarded overhead tests ────────────────────────────────────────────────────

// TestSigner_NonKDFOverhead_Light asserts that the non-KDF overhead of a complete
// SignTransaction call is < 10 ms when measured against the light-scrypt fixture.
//
// ADR-010: the bound is on the DELTA (total − KDF), not on absolute total time.
// This makes the test pass even when the KDF itself is slow (e.g. on a slow CI runner).
//
// Estimator: min(total) − min(KDF). See WHY-MIN comment below.
//
// Measurement design: in each iteration, total SignTransaction time is measured
// FIRST, then bare KDF time is measured on the same pre-loaded keystoreJSON. This
// eliminates two sources of systematic bias identified in review:
//  1. Ordering bias: measuring KDF first warms the CPU for the subsequent total
//     measurement, making total appear faster than KDF (negative delta).
//  2. I/O asymmetry: measuring KDF with a fresh os.ReadFile call each time inflates
//     kdfTimes relative to the vault-internal KDF that uses a snapshot byte slice.
//
// Pre-loading keystoreJSON once and passing it to measureKDFTime removes the I/O
// asymmetry; measuring total before KDF in each pair removes the ordering bias.
func TestSigner_NonKDFOverhead_Light(t *testing.T) {
	t.Parallel()

	keystorePath := testdataFile(t, "keystore-light.json")
	passwordPath := testdataFile(t, "password.txt")
	password := "test-only-password-do-not-reuse"

	keystoreJSON, err := readFileForBench(keystorePath)
	if err != nil {
		t.Fatalf("readFileForBench: %v", err)
	}

	// Create the signer once before the timing loop (production pattern: vault
	// constructed at startup, signed many times). This avoids counting keystore
	// file-read + JSON-parse cost in the per-call total measurement, which would
	// inflate the delta under concurrent-package load.
	s := newBenchSigner(t, keystorePath, passwordPath)

	// Guard against a phantom pass if the iteration constant is accidentally zero.
	if lightOverheadIterations == 0 {
		t.Fatal("lightOverheadIterations is 0: timing loop would be a phantom pass")
	}

	// Force a GC collection before the timing loop to reduce GC-pause variance
	// from allocations made during test setup (a known source of delta spikes).
	runtime.GC()

	var totalTimes, kdfTimes []time.Duration
	for i := 0; i < lightOverheadIterations; i++ {
		// Measure total FIRST so the KDF measurement runs on a comparably warm CPU.
		totalTimes = append(totalTimes, measureSignTime(t, s))
		kdfTimes = append(kdfTimes, measureKDFTime(t, keystoreJSON, password))
	}

	// WHY-MIN: We assert on min(total) − min(KDF) rather than median − median.
	//
	// The non-KDF work (RLP encode + ECDSA sign + sender recovery) is constant and
	// sub-millisecond. scrypt's own run-to-run variance — caused by OS scheduler
	// preemption, cache evictions, and power-management jitter on shared CI runners —
	// can be several milliseconds even for the light fixture (~50 ms/op, ≈2–5% noise).
	// When total and KDF are measured in SEPARATE loops, their medians can drift apart
	// by more than the 10 ms budget purely from scrypt variance; the median−median
	// estimator is fragile on noisy machines.
	//
	// The minimum sample is the least-preempted run, closest to the bare compute floor:
	//   min(total) ≈ scrypt_floor + non_KDF_overhead
	//   min(KDF)   ≈ scrypt_floor
	//   min(total) − min(KDF) ≈ non_KDF_overhead   (scrypt variance cancels)
	//
	// This correctly catches a real regression: if someone adds 50 ms of non-KDF work,
	// min(total) − min(KDF) ≈ 50 ms > 10 ms, and the test fails. A small negative
	// delta means the non-KDF overhead is below the noise floor, which is a pass.
	minTotal := minDuration(totalTimes)
	minKDF := minDuration(kdfTimes)
	delta := minTotal - minKDF

	// Log medians too for human-readable context alongside the asserting delta.
	t.Logf("light-scrypt: median total=%v  median KDF=%v  min total=%v  min KDF=%v  non-KDF delta (min-based)=%v  (limit: 10ms)",
		medianDuration(totalTimes), medianDuration(kdfTimes), minTotal, minKDF, delta)

	if delta < 0 {
		// Negative delta: non-KDF overhead is below the measurement noise floor.
		// With the min-based estimator this is expected occasionally and is not a failure.
		t.Logf("measurement noise: negative delta (%v) — min(KDF) landed slightly below min(total); not a failure", delta)
	}

	const limit = 10 * time.Millisecond
	if delta > limit {
		t.Errorf("non-KDF overhead (min total − min KDF) = %v; ADR-010 requires < %v", delta, limit)
	}
}

// TestSigner_NonKDFOverhead_Standard asserts the same < 10 ms bound against the
// standard-scrypt fixture (N=262144, ~0.5–1 s KDF). Skipped under -short.
// In CI's full run this test is the Phase 4 ADR-010 acceptance benchmark.
//
// Estimator: min(total) − min(KDF). See WHY-MIN comment below. The min-based
// estimator was introduced to fix a CI flake (ubuntu-latest, GOMAXPROCS=2):
// standard scrypt's ~620 ms/op has ≈2% run-to-run variance (~12 ms), which is
// larger than the 10 ms budget; median−median therefore wobbled past 10 ms even
// though the true non-KDF work is sub-millisecond. The min-based estimator
// cancels scrypt's central tendency AND its variance, making the test robust
// without weakening the contract (a real 50 ms regression still fails the test).
// standardOverheadIterations=15 gives extra min-sampling margin so the minimum
// has more chances to catch a clean non-KDF window on a busy shared runner.
//
// NOTE: This test is NOT t.Parallel(). The standard-scrypt KDF is CPU-bound at
// ~0.5–1 s; running it in parallel with other tests creates goroutine scheduling
// contention. LockOSThread further reduces preemption noise in the narrow non-KDF
// window — this complements but does not replace the min-based estimator.
//
// The KDF (golang.org/x/crypto/scrypt) is pure Go and does not use CGo, so
// LockOSThread has no adverse effect on it.
func TestSigner_NonKDFOverhead_Standard(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping standard-scrypt overhead test under -short (each call ~0.5–1 s)")
	}
	// NOT t.Parallel() — see comment above.

	keystorePath := testdataFile(t, "keystore-standard.json")
	passwordPath := testdataFile(t, "password.txt")
	password := "test-only-password-do-not-reuse"

	keystoreJSON, err := readFileForBench(keystorePath)
	if err != nil {
		t.Fatalf("readFileForBench: %v", err)
	}

	// Create the signer once before the timing loop (production pattern: vault
	// constructed at startup, signed many times). This avoids counting keystore
	// file-read + JSON-parse cost in the per-call total measurement.
	s := newBenchSigner(t, keystorePath, passwordPath)

	// Force a GC collection before the timing loop to reduce GC-pause variance
	// from allocations made during test setup.
	runtime.GC()

	// Guard against a phantom pass if the iteration constant is accidentally zero.
	if standardOverheadIterations == 0 {
		t.Fatal("standardOverheadIterations is 0: timing loop would be a phantom pass")
	}

	// Pin the goroutine to one OS thread for the duration of the timing loop.
	// This prevents other goroutines (e.g. from concurrently running test packages)
	// from stealing CPU time between the KDF completion and the end of SignTransaction,
	// which is the narrow non-KDF window where preemption inflates the delta.
	runtime.LockOSThread()
	defer runtime.UnlockOSThread() // ensures unlock on all paths, including t.Fatalf inside the loop

	var totalTimes, kdfTimes []time.Duration
	for i := 0; i < standardOverheadIterations; i++ {
		// Measure total FIRST, then KDF, to avoid ordering bias (see Light test comment).
		totalTimes = append(totalTimes, measureSignTime(t, s))
		kdfTimes = append(kdfTimes, measureKDFTime(t, keystoreJSON, password))
	}

	// WHY-MIN: We assert on min(total) − min(KDF) rather than median − median.
	//
	// Standard scrypt (N=262144) takes ~620 ms/op in CI. Run-to-run variance on a
	// shared 2-core runner (ubuntu-latest, GOMAXPROCS=2) is ≈2% ≈ 12 ms. Because
	// the total and KDF are measured in SEPARATE loops, their medians can drift
	// independently by that 12 ms, causing median(total) − median(KDF) to exceed
	// the 10 ms budget even when the true non-KDF work (RLP encode + ECDSA sign +
	// sender recovery) is sub-millisecond.
	//
	// The minimum sample is the least-preempted run, closest to the bare compute floor:
	//   min(total) ≈ scrypt_floor + non_KDF_overhead
	//   min(KDF)   ≈ scrypt_floor
	//   min(total) − min(KDF) ≈ non_KDF_overhead   (scrypt variance cancels)
	//
	// This correctly catches a real regression: if someone adds 50 ms of non-KDF work,
	// min(total) − min(KDF) ≈ 50 ms > 10 ms, and the test fails. A small negative
	// delta means the non-KDF overhead is below the noise floor, which is a pass.
	// This is the standard microbenchmark technique for noisy machines.
	minTotal := minDuration(totalTimes)
	minKDF := minDuration(kdfTimes)
	delta := minTotal - minKDF

	// Log medians too for human-readable context alongside the asserting delta.
	t.Logf("standard-scrypt: median total=%v  median KDF=%v  min total=%v  min KDF=%v  non-KDF delta (min-based)=%v  (limit: 10ms)",
		medianDuration(totalTimes), medianDuration(kdfTimes), minTotal, minKDF, delta)

	if delta < 0 {
		// Negative delta: non-KDF overhead is below the measurement noise floor.
		// With the min-based estimator this is expected occasionally and is not a failure.
		t.Logf("measurement noise: negative delta (%v) — min(KDF) landed slightly below min(total); not a failure", delta)
	}

	const limit = 10 * time.Millisecond
	if delta > limit {
		t.Errorf("non-KDF overhead (min total − min KDF) = %v; ADR-010 requires < %v", delta, limit)
	}
}

// ── Cold-start tests (Issue 4.3) ─────────────────────────────────────────────

// TestSigner_ColdStart_Light asserts that constructing a FileKeyVault + Signer
// from the light-scrypt fixture paths completes in < 200 ms (median of
// coldStartIterations iterations). Construction reads the keystore file and
// parses the address field; the KDF is NOT run at construction time (per
// ADR-010 and the KeyVault lifecycle contract — see vault.go, file_vault.go).
// The 200 ms bound is therefore generous on any developer-class machine.
func TestSigner_ColdStart_Light(t *testing.T) {
	t.Parallel()

	// Guard against a phantom pass if the iteration constant is accidentally zero.
	if coldStartIterations == 0 {
		t.Fatal("coldStartIterations is 0: timing loop would be a phantom pass")
	}

	keystorePath := testdataFile(t, "keystore-light.json")
	passwordPath := testdataFile(t, "password.txt")

	// GC before the loop to reduce GC-pause variance.
	runtime.GC()

	var times []time.Duration
	for i := 0; i < coldStartIterations; i++ {
		times = append(times, measureColdStartTime(t, keystorePath, passwordPath))
	}

	med := medianDuration(times)
	t.Logf("light-scrypt cold start: median=%v  (limit: 200ms)", med)

	const limit = 200 * time.Millisecond
	if med > limit {
		t.Errorf("cold start (median) = %v; ADR-010 acceptance criterion requires < %v", med, limit)
	}
}

// TestSigner_ColdStart_Standard asserts the same < 200 ms cold-start bound against
// the standard-scrypt fixture (N=262144).
//
// No testing.Short() guard: construction has no KDF cost (I/O + JSON parse only,
// ~20–70 µs per ADR-010 measurements). The "standard-scrypt" label refers to the
// fixture file's KDF parameters, not the construction operation being measured here.
//
// NOTE: This test is NOT t.Parallel() — consistent with TestSigner_NonKDFOverhead_Standard.
func TestSigner_ColdStart_Standard(t *testing.T) {
	// NOT t.Parallel() — consistent with NonKDFOverhead_Standard.

	keystorePath := testdataFile(t, "keystore-standard.json")
	passwordPath := testdataFile(t, "password.txt")

	// Guard against a phantom pass if the iteration constant is accidentally zero.
	if coldStartIterations == 0 {
		t.Fatal("coldStartIterations is 0: timing loop would be a phantom pass")
	}

	// GC before the loop to reduce GC-pause variance.
	runtime.GC()

	var times []time.Duration
	for i := 0; i < coldStartIterations; i++ {
		times = append(times, measureColdStartTime(t, keystorePath, passwordPath))
	}

	med := medianDuration(times)
	t.Logf("standard-scrypt cold start: median=%v  (limit: 200ms)", med)

	const limit = 200 * time.Millisecond
	if med > limit {
		t.Errorf("cold start (median) = %v; ADR-010 acceptance criterion requires < %v", med, limit)
	}
}

// ── Go benchmarks (for -bench runs and phase comparison) ─────────────────────

// BenchmarkSignTransaction_Light benchmarks the full SignTransaction flow using
// the light-scrypt fixture. Throughput is dominated by the scrypt KDF (~50 ms/op).
func BenchmarkSignTransaction_Light(b *testing.B) {
	vault, err := NewFileKeyVault(VaultOptions{
		KeystorePath: "testdata/keystore-light.json",
		PasswordPath: "testdata/password.txt",
	})
	if err != nil {
		b.Fatalf("NewFileKeyVault: %v", err)
	}
	s := NewSigner(vault, SignerOptions{})
	req := TxRequest{
		Type:     "legacy",
		ChainID:  "1",
		Nonce:    "0",
		To:       "0x9858EfFD232B4033E47d90003D41EC34EcaEda94",
		Value:    "0",
		Data:     "0x",
		Gas:      "21000",
		GasPrice: "1000000000",
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, signErr := s.SignTransaction(context.Background(), req); signErr != nil {
			b.Fatalf("SignTransaction: %v", signErr)
		}
	}
}

// BenchmarkKDFOnly_Light benchmarks keystore.DecryptKey alone for the light fixture.
// Run alongside BenchmarkSignTransaction_Light to compute non-KDF overhead.
func BenchmarkKDFOnly_Light(b *testing.B) {
	keystoreJSON, err := readFileForBench("testdata/keystore-light.json")
	if err != nil {
		b.Fatalf("ReadFile: %v", err)
	}
	password := "test-only-password-do-not-reuse"
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		key, decErr := gokeystore.DecryptKey(keystoreJSON, password)
		if decErr != nil {
			b.Fatalf("DecryptKey: %v", decErr)
		}
		ZeroBigInt(key.PrivateKey.D) //nolint:staticcheck // ADR-009
	}
}

// BenchmarkSignTransaction_Standard benchmarks the full flow with standard-scrypt
// fixture. Each call ~0.5–1 s; use -benchtime=1x or -benchcount=1 to limit runs.
func BenchmarkSignTransaction_Standard(b *testing.B) {
	if testing.Short() {
		b.Skip("skipping standard-scrypt benchmark under -short")
	}
	vault, err := NewFileKeyVault(VaultOptions{
		KeystorePath: "testdata/keystore-standard.json",
		PasswordPath: "testdata/password.txt",
	})
	if err != nil {
		b.Fatalf("NewFileKeyVault: %v", err)
	}
	s := NewSigner(vault, SignerOptions{})
	req := TxRequest{
		Type:     "legacy",
		ChainID:  "1",
		Nonce:    "0",
		To:       "0x9858EfFD232B4033E47d90003D41EC34EcaEda94",
		Value:    "0",
		Data:     "0x",
		Gas:      "21000",
		GasPrice: "1000000000",
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, signErr := s.SignTransaction(context.Background(), req); signErr != nil {
			b.Fatalf("SignTransaction: %v", signErr)
		}
	}
}

// BenchmarkKDFOnly_Standard benchmarks keystore.DecryptKey alone for the standard fixture.
func BenchmarkKDFOnly_Standard(b *testing.B) {
	if testing.Short() {
		b.Skip("skipping standard-scrypt KDF benchmark under -short")
	}
	keystoreJSON, err := readFileForBench("testdata/keystore-standard.json")
	if err != nil {
		b.Fatalf("ReadFile: %v", err)
	}
	password := "test-only-password-do-not-reuse"
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		key, decErr := gokeystore.DecryptKey(keystoreJSON, password)
		if decErr != nil {
			b.Fatalf("DecryptKey: %v", decErr)
		}
		ZeroBigInt(key.PrivateKey.D) //nolint:staticcheck // ADR-009
	}
}
