// Benchmark and overhead-assertion tests — Issue 2.6 (ADR-010).
//
// Design: we measure (a) total SignTransaction time and (b) KDF-only time
// (timing keystore.DecryptKey directly on the same fixture). The non-KDF overhead
// is (a − b). Per ADR-010, median(a − b) < 10 ms on BOTH standard- and
// light-scrypt fixtures.
//
// The standard-scrypt fixture (N=262144) is skipped under -short because its
// KDF alone takes ~0.5–1 s per call, making the full median measurement very slow.
package signing

import (
	"context"
	"os"
	"sort"
	"testing"
	"time"

	gokeystore "github.com/ethereum/go-ethereum/accounts/keystore"
)

// overheadIterations is the number of iterations used for the median calculation
// in the TestSigner_NonKDFOverhead_* tests. Enough to be statistically robust
// without being prohibitively slow even on CI runners (each light-scrypt ~50 ms).
const overheadIterations = 7

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

// readFileForBench reads a file for use in benchmarks/test helpers.
func readFileForBench(path string) ([]byte, error) {
	return os.ReadFile(path)
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

// measureSignTime times one full end-to-end SignTransaction call.
func measureSignTime(t testing.TB, keystorePath, passwordPath string) time.Duration {
	t.Helper()
	vault, err := NewFileKeyVault(VaultOptions{
		KeystorePath: keystorePath,
		PasswordPath: passwordPath,
	})
	if err != nil {
		t.Fatalf("NewFileKeyVault: %v", err)
	}
	s := NewSigner(vault, SignerOptions{}) // nil logger → slog.Default()

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
// Uses medians over overheadIterations iterations to be robust against machine noise.
//
// ADR-010: the bound is on the DELTA (total − KDF), not on absolute total time.
// This makes the test pass even when the KDF itself is slow (e.g. on a slow CI runner).
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

	var totalTimes, kdfTimes []time.Duration
	for i := 0; i < overheadIterations; i++ {
		// Measure total FIRST so the KDF measurement runs on a comparably warm CPU.
		totalTimes = append(totalTimes, measureSignTime(t, keystorePath, passwordPath))
		kdfTimes = append(kdfTimes, measureKDFTime(t, keystoreJSON, password))
	}

	medTotal := medianDuration(totalTimes)
	medKDF := medianDuration(kdfTimes)
	delta := medTotal - medKDF

	t.Logf("light-scrypt: median total=%v  median KDF=%v  non-KDF delta=%v  (limit: 10ms)",
		medTotal, medKDF, delta)

	if delta < 0 {
		t.Logf("WARNING: negative delta (%v) — unexpected; KDF measurement may be slower than total", delta)
	}

	const limit = 10 * time.Millisecond
	if delta > limit {
		t.Errorf("non-KDF overhead (median total − median KDF) = %v; ADR-010 requires < %v", delta, limit)
	}
}

// TestSigner_NonKDFOverhead_Standard asserts the same < 10 ms bound against the
// standard-scrypt fixture (N=262144, ~0.5–1 s KDF). Skipped under -short.
// In CI's full run this test is the Phase 4 ADR-010 acceptance benchmark.
//
// NOTE: This test is NOT t.Parallel(). The standard-scrypt KDF is CPU-bound at
// ~0.5–1 s; running concurrently with other CPU-bound tests inflates contention
// variance by ±10–15 ms, which is enough to exceed the 10 ms delta budget.
// Running sequentially makes the delta measurement reliably reflect the actual
// non-KDF overhead rather than scheduler noise.
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

	var totalTimes, kdfTimes []time.Duration
	for i := 0; i < overheadIterations; i++ {
		// Measure total FIRST, then KDF, to avoid ordering bias (see Light test comment).
		totalTimes = append(totalTimes, measureSignTime(t, keystorePath, passwordPath))
		kdfTimes = append(kdfTimes, measureKDFTime(t, keystoreJSON, password))
	}

	medTotal := medianDuration(totalTimes)
	medKDF := medianDuration(kdfTimes)
	delta := medTotal - medKDF

	t.Logf("standard-scrypt: median total=%v  median KDF=%v  non-KDF delta=%v  (limit: 10ms)",
		medTotal, medKDF, delta)

	if delta < 0 {
		t.Logf("WARNING: negative delta (%v) — unexpected; KDF measurement may be slower than total", delta)
	}

	const limit = 10 * time.Millisecond
	if delta > limit {
		t.Errorf("non-KDF overhead (median total − median KDF) = %v; ADR-010 requires < %v", delta, limit)
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
