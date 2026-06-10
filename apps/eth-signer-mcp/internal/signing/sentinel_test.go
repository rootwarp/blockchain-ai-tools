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
