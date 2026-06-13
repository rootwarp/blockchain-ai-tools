package signing

import (
	"encoding/hex"
)

// fixturePrivKeyHex is the raw hex scalar of the test private key committed in
// testdata/. It is the SINGLE non-README disclosure of this value within the
// signing package — used only to derive the leak-scan sentinel.
//
// WARNING: TEST-ONLY key. Never use for real funds. Documented in
// testdata/README.md (the canonical human-readable disclosure).
const fixturePrivKeyHex = "1ab42cc412b618bdea3a599e3c9bae199ebf030895b039e9db1e30dafb12b727"

// FixtureTestAddress is the EIP-55 checksummed Ethereum address derived from the
// test private key. It is exported so downstream test files (vault tests, signer
// tests, parity tests) can assert the expected address without re-deriving it or
// duplicating the raw key scalar.
const FixtureTestAddress = "0x9858EfFD232B4033E47d90003D41EC34EcaEda94"

// FixtureKeySentinel returns a Sentinel pre-loaded with all encoded forms of the
// test private key scalar committed in testdata/. Downstream test packages call
// this to build their leak-scan assertions so the sentinel is derived from one
// source of truth: the raw private key hex above.
//
// The Sentinel covers: raw bytes, hex-lower, hex-upper, base64-std, base64-raw,
// base64-url, base64-rawurl, and the decimal big-int form (all via NewSentinel),
// plus the EIP-55 checksummed address registered as an additional form. This
// ensures that any accidental log/output of the key scalar — in any common
// encoding — is detected by Scan.
//
// Usage in tests:
//
//	sentinel := signing.FixtureKeySentinel()
//	if leaked := sentinel.Scan(capturedOutput); len(leaked) > 0 {
//	    t.Errorf("fixture key leaked in form(s): %v", leaked)
//	}
func FixtureKeySentinel() Sentinel {
	raw, err := hex.DecodeString(fixturePrivKeyHex)
	if err != nil {
		// This cannot happen at runtime — the constant is a valid hex string.
		// Panic here (not an error return) because this is always a programming error.
		panic("signing: fixturePrivKeyHex is not valid hex: " + err.Error())
	}
	s := NewSentinel("fixture-private-key", raw)
	// Register the EIP-55 checksummed address as an additional form:
	// the address is derived from the key and would identify the key owner
	// if it appeared alongside other context.
	s.RegisterForm("address-checksummed", []byte(FixtureTestAddress))
	// Also register lowercase address (without 0x prefix, as geth typically emits).
	lowerAddr := "9858effd232b4033e47d90003d41ec34ecaeda94"
	s.RegisterForm("address-lower-nox", []byte(lowerAddr))
	return s
}
