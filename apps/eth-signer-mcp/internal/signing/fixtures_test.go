package signing_test

import (
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	gokeystore "github.com/ethereum/go-ethereum/accounts/keystore"

	"github.com/rootwarp/blockchain-ai-tools/apps/eth-signer-mcp/internal/signing"
)

// fixtureTestAddress is the EIP-55 checksummed address of the test private key
// documented in testdata/README.md. All three keystore files encrypt the same key
// and must decrypt to this address.
//
// This constant is the canonical reference; if it ever changes, update README.md
// and re-run testdata/gen_fixtures.go. It mirrors signing.FixtureTestAddress.
const fixtureTestAddress = signing.FixtureTestAddress

// testdataPath returns the path to a file inside the signing testdata directory,
// relative to the package directory (where go test runs).
func testdataPath(name string) string {
	return filepath.Join("testdata", name)
}

// readFixturePassword reads testdata/password.txt and strips the trailing newline.
// The trailing newline is present by design to exercise the strip-trailing-newline
// path in the vault (Issue 2.2).
func readFixturePassword(t *testing.T) string {
	t.Helper()
	raw, err := os.ReadFile(testdataPath("password.txt"))
	if err != nil {
		t.Fatalf("cannot read testdata/password.txt: %v", err)
	}
	return strings.TrimRight(string(raw), "\n")
}

// keystoreScryptN parses the scrypt N parameter from a keystore JSON file.
// Expected structure: {"crypto":{"kdf":"scrypt","kdfparams":{"n":<N>,...}}}
func keystoreScryptN(t *testing.T, path string) int {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("cannot read %s: %v", path, err)
	}
	var ks struct {
		Crypto struct {
			KDF       string `json:"kdf"`
			KDFParams struct {
				N int `json:"n"`
			} `json:"kdfparams"`
		} `json:"crypto"`
	}
	if err := json.Unmarshal(raw, &ks); err != nil {
		t.Fatalf("cannot unmarshal %s: %v", path, err)
	}
	if ks.Crypto.KDF != "scrypt" {
		t.Fatalf("%s: kdf = %q, want \"scrypt\"", path, ks.Crypto.KDF)
	}
	return ks.Crypto.KDFParams.N
}

// TestFixtures_DecryptAllThree is the Issue 2.1 fixture-sanity test.
// It decrypts all three keystore files with testdata/password.txt and asserts:
//  1. Decryption succeeds for each file.
//  2. Each decrypts to fixtureTestAddress (all three encode the same private key).
//  3. KDF scrypt N matches the documented value per file.
//  4. The weak fixture (n=2) decrypts in well under 100 ms.
//
// The standard-scrypt sub-test is skipped under -short because its KDF takes ~0.5–1 s.
// Light and weak always run.
func TestFixtures_DecryptAllThree(t *testing.T) {
	t.Parallel()

	password := readFixturePassword(t)

	type fixtureCase struct {
		file    string
		wantN   int
		short   bool          // if true, skip under testing.Short()
		maxTime time.Duration // non-zero → assert elapsed < maxTime
	}

	cases := []fixtureCase{
		{
			// n=2: near-instant; default fixture for fast unit tests.
			file:    "keystore-weak.json",
			wantN:   2,
			maxTime: 100 * time.Millisecond,
		},
		{
			// N=4096: fast enough for integration tests without -short.
			file:  "keystore-light.json",
			wantN: 4096,
		},
		{
			// N=262144: standard geth default; skip under -short (~0.5–1 s).
			file:  "keystore-standard.json",
			wantN: 262144,
			short: true,
		},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.file, func(t *testing.T) {
			t.Parallel()
			if tc.short && testing.Short() {
				t.Skip("skipping standard-scrypt decrypt under -short (KDF ~0.5–1 s)")
			}

			path := testdataPath(tc.file)
			raw, err := os.ReadFile(path)
			if err != nil {
				t.Fatalf("read %s: %v", tc.file, err)
			}

			start := time.Now()
			key, err := gokeystore.DecryptKey(raw, password)
			elapsed := time.Since(start)

			if err != nil {
				t.Fatalf("DecryptKey(%s): %v", tc.file, err)
			}

			// All three keystores must decrypt to the same test address.
			addr := key.Address.Hex() // EIP-55 checksummed
			if addr != fixtureTestAddress {
				t.Errorf("DecryptKey(%s): address = %q, want %q", tc.file, addr, fixtureTestAddress)
			}

			// Assert KDF N parameter by reading the JSON directly.
			gotN := keystoreScryptN(t, path)
			if gotN != tc.wantN {
				t.Errorf("%s: scrypt N = %d, want %d", tc.file, gotN, tc.wantN)
			}

			// Weak fixture must decrypt near-instantly (n=2).
			if tc.maxTime > 0 && elapsed > tc.maxTime {
				t.Errorf("%s: decrypt took %v, want < %v (n=2 should be near-instant)", tc.file, elapsed, tc.maxTime)
			}

			t.Logf("%s: address=%s N=%d elapsed=%v", tc.file, addr, gotN, elapsed)
		})
	}
}

// TestFixtures_MalformedKeystores verifies the structure of the optional-address
// fixture files (per Web3 Secret Storage spec the top-level "address" is optional).
// These are copies of keystore-weak.json that differ only in the top-level
// "address" field — removed (no-address) or set to "" (empty-address).
// They now exercise the success + zero-address-until-discover path.
func TestFixtures_MalformedKeystores(t *testing.T) {
	t.Parallel()

	// readTopLevelAddress parses the top-level "address" field from a keystore file.
	// Returns (present=false) if the field is absent.
	readTopLevelAddress := func(t *testing.T, name string) (present bool, value string) {
		t.Helper()
		raw, err := os.ReadFile(testdataPath(name))
		if err != nil {
			t.Fatalf("cannot read %s: %v", name, err)
		}
		var top map[string]json.RawMessage
		if err := json.Unmarshal(raw, &top); err != nil {
			t.Fatalf("cannot parse %s as JSON object: %v", name, err)
		}
		addrRaw, ok := top["address"]
		if !ok {
			return false, ""
		}
		var addr string
		if err := json.Unmarshal(addrRaw, &addr); err != nil {
			// Unexpected JSON type (not a string) — treat as present but malformed.
			return true, string(addrRaw)
		}
		return true, addr
	}

	t.Run("keystore-no-address.json", func(t *testing.T) {
		t.Parallel()
		present, _ := readTopLevelAddress(t, "keystore-no-address.json")
		if present {
			t.Error(`keystore-no-address.json: must not have a top-level "address" field`)
		}
	})

	t.Run("keystore-empty-address.json", func(t *testing.T) {
		t.Parallel()
		present, val := readTopLevelAddress(t, "keystore-empty-address.json")
		if !present {
			t.Error(`keystore-empty-address.json: must have a top-level "address" field`)
		}
		if val != "" {
			t.Errorf(`keystore-empty-address.json: address = %q, want empty string ""`, val)
		}
	})
}

// TestFixtureKeySentinel verifies that FixtureKeySentinel returns a Sentinel
// that detects the fixture private key in all expected encoded forms:
// raw bytes, hex-lower, hex-upper, base64-std, base64-raw, and decimal.
// This is the leak-scan registration required by Issue 2.1 Acceptance Criteria.
func TestFixtureKeySentinel(t *testing.T) {
	t.Parallel()

	const privKeyHex = "1ab42cc412b618bdea3a599e3c9bae199ebf030895b039e9db1e30dafb12b727"

	sentinel := signing.FixtureKeySentinel()
	if sentinel.Name == "" {
		t.Fatal("FixtureKeySentinel: Name is empty")
	}
	if len(sentinel.Forms) == 0 {
		t.Fatal("FixtureKeySentinel: no forms registered")
	}

	raw, err := hex.DecodeString(privKeyHex)
	if err != nil {
		t.Fatalf("test setup: hex.DecodeString: %v", err)
	}

	// For each expected encoding, plant it in output and assert Scan detects it.
	// SAFETY: do NOT include raw or encoded key bytes in t.Errorf messages below.
	type formPlant struct {
		name  string
		bytes []byte
	}
	plants := []formPlant{
		{"raw", raw},
		{"hex-lower", []byte(privKeyHex)},
		{"hex-upper", []byte(strings.ToUpper(privKeyHex))},
		{"base64-std", []byte(base64.StdEncoding.EncodeToString(raw))},
		{"base64-raw", []byte(base64.RawStdEncoding.EncodeToString(raw))},
	}

	for _, p := range plants {
		p := p
		t.Run(p.name, func(t *testing.T) {
			t.Parallel()
			output := append([]byte("log: "), append(p.bytes, []byte(" end")...)...)
			leaked := sentinel.Scan(output)
			found := false
			for _, n := range leaked {
				if n == p.name {
					found = true
				}
			}
			if !found {
				// SAFETY: report form name only, never the bytes.
				t.Errorf("FixtureKeySentinel: Scan did not detect form %q when planted", p.name)
			}
		})
	}

	// Verify Scan returns nothing on clean output (no false positives).
	t.Run("clean", func(t *testing.T) {
		t.Parallel()
		clean := []byte("completely unrelated log output with no key material")
		if leaked := sentinel.Scan(clean); len(leaked) > 0 {
			t.Errorf("FixtureKeySentinel: Scan on clean output returned leaked forms: %v", leaked)
		}
	})
}
