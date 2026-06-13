package signing

import (
	"bytes"
	"encoding/base64"
	"encoding/hex"
	"math/big"
	"strconv"
	"strings"
)

// Sentinel is the leak-scan helper used in tests to verify that a fixture
// secret does not appear in captured log output or serialised values — in any
// of its encoded forms.
//
// # How it works
//
// NewSentinel derives multiple encodings (forms) of the raw secret bytes:
// raw bytes, lowercase hex, uppercase hex, standard base64, raw/unpadded
// base64, and the decimal rendering of the bytes as a big-endian integer
// scalar. Scan walks a captured-output buffer and reports the NAMES of every
// form that appears — never the bytes themselves.
//
// # Sanitised-failure-message rule
//
// Any test that uses Sentinel.Scan MUST report only Sentinel.Name and the
// leaked form names in failure messages. Never include the captured buffer,
// nor any raw or encoded form of the secret bytes, in a t.Errorf / t.Logf
// call. Scan returns names for exactly this reason: a well-written failure
// message says "hex-lower form of fixture-secret leaked", not the hex string.
//
// # New secret types
//
// When Phase 2 introduces new secret types (e.g. key scalars, password bytes
// in their various encodings), register their expected encoded forms by
// constructing a Sentinel with the appropriate raw bytes. The forms derived
// here (raw, hex-lower, hex-upper, base64-std, base64-raw, base64-url,
// base64-rawurl, decimal) cover the most common encoding paths; if a new type
// introduces additional representations (e.g. checksummed address strings),
// register them with the RegisterForm method (which keeps Forms and the
// internal name slice in sync — never append to Forms directly).
type Sentinel struct {
	// Name is a human-readable label for this sentinel instance.
	// Used in failure messages — never the bytes.
	Name string
	// Forms holds the derived encodings that Scan searches for in output.
	// Each element has a corresponding name in names (parallel slices).
	Forms [][]byte

	// names holds the name for each entry in Forms (parallel to Forms).
	names []string
}

// NewSentinel constructs a Sentinel for the given raw secret bytes, deriving
// the following encoded forms:
//
//   - "raw"          — the raw bytes verbatim
//   - "hex-lower"    — lowercase hex encoding
//   - "hex-upper"    — uppercase hex encoding
//   - "base64-std"   — standard base64 (padded, +/ alphabet)
//   - "base64-raw"   — raw (unpadded) standard base64
//   - "base64-url"   — URL-safe base64 (padded, -_ alphabet)
//   - "base64-rawurl"— raw (unpadded) URL-safe base64
//   - "decimal"      — decimal string rendering of new(big.Int).SetBytes(raw)
//
// These cover the most likely leak paths in log output. The decimal form
// catches the scenario where a private-key scalar is printed via %d or
// big.Int.String() — the Phase 2 usage that motivates including it now. The
// URL-safe base64 forms catch leaks through JWT/OAuth/token paths (Phase 3).
//
// An empty raw input yields a Sentinel with NO forms (it would otherwise derive
// the decimal form "0", which matches innocuous output and produces false
// positives in Scan).
func NewSentinel(name string, raw []byte) Sentinel {
	if len(raw) == 0 {
		return Sentinel{Name: name}
	}

	hexLower := hex.EncodeToString(raw)
	type namedForm struct {
		name  string
		bytes []byte
	}
	// Defensive copy of raw: the caller may ZeroBytes(raw) for cleanup after
	// constructing the Sentinel; without the copy that would silently wipe the
	// "raw" form (W-1).
	rawCopy := append([]byte(nil), raw...)

	forms := []namedForm{
		{"raw", rawCopy},
		{"hex-lower", []byte(hexLower)},
		{"hex-upper", []byte(strings.ToUpper(hexLower))},
		{"base64-std", []byte(base64.StdEncoding.EncodeToString(raw))},
		{"base64-raw", []byte(base64.RawStdEncoding.EncodeToString(raw))},
		{"base64-url", []byte(base64.URLEncoding.EncodeToString(raw))},
		{"base64-rawurl", []byte(base64.RawURLEncoding.EncodeToString(raw))},
		{"decimal", []byte(new(big.Int).SetBytes(raw).String())},
	}

	s := Sentinel{Name: name}
	for _, f := range forms {
		s.RegisterForm(f.name, f.bytes)
	}
	return s
}

// RegisterForm adds an encoded form (with its name) to the Sentinel, keeping the
// Forms slice and the internal name slice in sync. Empty forms and forms that
// duplicate an already-registered one are skipped. Use this — never append to
// Forms directly — when registering additional encodings for a new secret type.
func (s *Sentinel) RegisterForm(name string, b []byte) {
	if len(b) == 0 {
		return
	}
	for _, existing := range s.Forms {
		if bytes.Equal(existing, b) {
			return
		}
	}
	s.Forms = append(s.Forms, b)
	s.names = append(s.names, name)
}

// Scan searches output for every derived form of the sentinel secret.
// It returns the NAMES of all forms found — never the bytes.
//
// Callers MUST report only these names in failure messages, never the buffer
// contents or the raw/encoded secret bytes. This is the sanitised-failure-
// message rule: scan failures name the form ("hex-lower"), not the value.
func (s Sentinel) Scan(output []byte) []string {
	var found []string
	for i, form := range s.Forms {
		name := "form-" + strconv.Itoa(i) // fallback if names got out of sync
		if i < len(s.names) {
			name = s.names[i]
		}
		if bytes.Contains(output, form) {
			found = append(found, name)
		}
	}
	return found
}
