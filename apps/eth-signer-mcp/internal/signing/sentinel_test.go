package signing_test

import (
	"encoding/base64"
	"encoding/hex"
	"math/big"
	"strings"
	"testing"

	"github.com/rootwarp/blockchain-ai-tools/apps/eth-signer-mcp/internal/signing"
)

// upperHex returns the uppercase hex encoding of b.
func upperHex(b []byte) string {
	return strings.ToUpper(hex.EncodeToString(b))
}

// TestNewSentinel_DerivesAllForms verifies that NewSentinel derives at minimum:
// raw, hex-lower, hex-upper, base64-std, base64-raw, and decimal forms.
// Each form is planted in isolation and Scan must report its name.
func TestNewSentinel_DerivesAllForms(t *testing.T) {
	t.Parallel()
	raw := []byte{0xDE, 0xAD, 0xBE, 0xEF}
	sent := signing.NewSentinel("test", raw)

	// Map of form name → bytes that should trigger detection.
	// SAFETY: do NOT include these bytes in any t.Errorf call.
	wantForms := map[string][]byte{
		"raw":        raw,
		"hex-lower":  []byte(hex.EncodeToString(raw)),
		"hex-upper":  []byte(upperHex(raw)),
		"base64-std": []byte(base64.StdEncoding.EncodeToString(raw)),
		"base64-raw": []byte(base64.RawStdEncoding.EncodeToString(raw)),
		"decimal":    []byte(new(big.Int).SetBytes(raw).String()),
	}

	for name, formBytes := range wantForms {
		name, formBytes := name, formBytes
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			// Plant the form in a surrounding context so the match is
			// unambiguous — the scanner must find the encoded form.
			output := append([]byte("prefix "), append(formBytes, []byte(" suffix")...)...)
			leaked := sent.Scan(output)
			found := false
			for _, leakedName := range leaked {
				if leakedName == name {
					found = true
					break
				}
			}
			if !found {
				// SAFETY: report form name only, never the bytes.
				t.Errorf("NewSentinel: Scan did not find form %q when planted (sentinel: %q)", name, sent.Name)
			}
		})
	}
}

// TestSentinel_ScanClean verifies Scan returns empty on output that contains no sentinel forms.
func TestSentinel_ScanClean(t *testing.T) {
	t.Parallel()
	sent := signing.NewSentinel("test", []byte{0xDE, 0xAD, 0xBE, 0xEF})
	clean := []byte("this output is completely unrelated and contains no secret material")
	leaked := sent.Scan(clean)
	if len(leaked) > 0 {
		t.Errorf("Scan on clean output returned leaked forms: %v (sentinel: %q)", leaked, sent.Name)
	}
}

// TestSentinel_ScanMultipleForms verifies Scan returns all form names when multiple are present.
func TestSentinel_ScanMultipleForms(t *testing.T) {
	t.Parallel()
	raw := []byte{0x01, 0x02, 0x03}
	sent := signing.NewSentinel("multi", raw)

	// Plant hex-lower and base64-std forms in the output.
	hexLower := hex.EncodeToString(raw)
	b64Std := base64.StdEncoding.EncodeToString(raw)
	// SAFETY: output intentionally constructed from known forms; do NOT log it on failure.
	output := []byte("output contains " + hexLower + " and also " + b64Std)

	leaked := sent.Scan(output)
	foundHex := false
	foundB64 := false
	for _, name := range leaked {
		if name == "hex-lower" {
			foundHex = true
		}
		if name == "base64-std" {
			foundB64 = true
		}
	}
	if !foundHex {
		t.Errorf("Scan did not detect hex-lower form (sentinel: %q)", sent.Name)
	}
	if !foundB64 {
		t.Errorf("Scan did not detect base64-std form (sentinel: %q)", sent.Name)
	}
}

// TestSentinel_Name verifies the Sentinel.Name field is set correctly.
func TestSentinel_Name(t *testing.T) {
	t.Parallel()
	sent := signing.NewSentinel("my-sentinel", []byte{0x01})
	if sent.Name != "my-sentinel" {
		t.Errorf("Sentinel.Name = %q, want %q", sent.Name, "my-sentinel")
	}
}

// TestNewSentinel_DeduplicatesForms verifies that NewSentinel deduplicates
// forms that encode to the same bytes. For inputs like []byte{0x00}, the
// lowercase-hex and uppercase-hex forms are both "00" — they should appear
// only once in Sentinel.Forms.
func TestNewSentinel_DeduplicatesForms(t *testing.T) {
	t.Parallel()
	// []byte{0x00} produces hex-lower "00" and hex-upper "00" (identical).
	raw := []byte{0x00}
	sent := signing.NewSentinel("dup-test", raw)

	// Verify there are no duplicate form bytes in the Forms slice.
	seen := make(map[string]int)
	for _, form := range sent.Forms {
		key := string(form)
		seen[key]++
		if seen[key] > 1 {
			// SAFETY: do not print form bytes; use hex length as an identifier.
			t.Errorf("NewSentinel: duplicate form found (length %d, seen %d times)", len(form), seen[key])
		}
	}
}

// TestSentinel_ScanReturnsEmpty_NilInput verifies Scan returns nil on empty output.
func TestSentinel_ScanReturnsEmpty_NilInput(t *testing.T) {
	t.Parallel()
	sent := signing.NewSentinel("test", []byte{0x01, 0x02})
	leaked := sent.Scan(nil)
	if len(leaked) > 0 {
		t.Errorf("Scan(nil) returned non-empty: %v", leaked)
	}
}

// TestNewSentinel_RawFormDefensiveCopy verifies that zeroing the caller's input
// slice after construction does NOT wipe the Sentinel's "raw" form (W-1). The
// natural hygiene pattern is: derive a Sentinel from fixture bytes, then
// ZeroBytes(fixture) for cleanup — which must leave the Sentinel intact.
func TestNewSentinel_RawFormDefensiveCopy(t *testing.T) {
	t.Parallel()
	raw := []byte{0xDE, 0xAD, 0xBE, 0xEF}
	orig := append([]byte(nil), raw...) // keep an independent copy to plant
	sent := signing.NewSentinel("copytest", raw)

	signing.ZeroBytes(raw) // caller cleanup — must not affect the Sentinel

	output := append([]byte("prefix "), append(orig, []byte(" suffix")...)...)
	leaked := sent.Scan(output)
	found := false
	for _, name := range leaked {
		if name == "raw" {
			found = true
		}
	}
	if !found {
		t.Errorf("raw form was wiped by zeroing the caller's input slice (sentinel: %q)", sent.Name)
	}
}

// TestNewSentinel_EmptyRaw verifies that an empty/nil raw input yields a Sentinel
// with no forms — guarding against the decimal form "0" (from
// big.Int.SetBytes(nil)) producing false positives on innocuous output (W-3).
func TestNewSentinel_EmptyRaw(t *testing.T) {
	t.Parallel()
	for _, raw := range [][]byte{{}, nil} {
		sent := signing.NewSentinel("empty", raw)
		if len(sent.Forms) != 0 {
			t.Errorf("NewSentinel(empty): derived %d forms, want 0", len(sent.Forms))
		}
		// Output full of zeros / counts must NOT trip a false positive.
		if leaked := sent.Scan([]byte("count=0 port=8080 status=0")); len(leaked) > 0 {
			t.Errorf("NewSentinel(empty): Scan false-positive forms: %v", leaked)
		}
	}
}

// TestNewSentinel_DerivesURLBase64Forms verifies the URL-safe base64 forms are
// derived and detected (W-4) — a secret leaking through a JWT/OAuth/token path
// (URL-safe alphabet, -_ instead of +/) must not evade the scan.
func TestNewSentinel_DerivesURLBase64Forms(t *testing.T) {
	t.Parallel()
	// 0xFB,0xFF,0xBF encodes to "+/+/"-ish under std and "-_-_"-ish under url —
	// the two alphabets differ, so the url form is a distinct detectable string.
	raw := []byte{0xFB, 0xFF, 0xBF, 0xFE}
	sent := signing.NewSentinel("urltest", raw)

	for _, tc := range []struct {
		name string
		form []byte
	}{
		{"base64-url", []byte(base64.URLEncoding.EncodeToString(raw))},
		{"base64-rawurl", []byte(base64.RawURLEncoding.EncodeToString(raw))},
	} {
		output := append([]byte("token="), tc.form...)
		found := false
		for _, n := range sent.Scan(output) {
			if n == tc.name {
				found = true
			}
		}
		if !found {
			t.Errorf("Scan did not detect %s form when planted (sentinel: %q)", tc.name, sent.Name)
		}
	}
}

// TestSentinel_RegisterForm verifies RegisterForm adds a custom form (keeping
// Forms/names in sync so Scan reports it by name) and skips empty/duplicate forms.
func TestSentinel_RegisterForm(t *testing.T) {
	t.Parallel()
	sent := signing.NewSentinel("reg", []byte{0x01, 0x02})
	before := len(sent.Forms)

	sent.RegisterForm("custom", []byte("CHECKSUMMED-FORM-XYZ"))
	if len(sent.Forms) != before+1 {
		t.Fatalf("RegisterForm: Forms len = %d, want %d", len(sent.Forms), before+1)
	}

	// Empty and duplicate registrations are skipped.
	sent.RegisterForm("empty", nil)
	sent.RegisterForm("dup", []byte("CHECKSUMMED-FORM-XYZ"))
	if len(sent.Forms) != before+1 {
		t.Errorf("RegisterForm: empty/dup not skipped; Forms len = %d, want %d", len(sent.Forms), before+1)
	}

	// Scan reports the custom form by its registered name (names stayed in sync).
	leaked := sent.Scan([]byte("x CHECKSUMMED-FORM-XYZ y"))
	found := false
	for _, n := range leaked {
		if n == "custom" {
			found = true
		}
	}
	if !found {
		t.Errorf("Scan did not report the registered custom form by name (got %v)", leaked)
	}
}
