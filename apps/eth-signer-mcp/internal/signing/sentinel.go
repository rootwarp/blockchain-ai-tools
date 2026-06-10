package signing

import (
	"bytes"
	"encoding/base64"
	"encoding/hex"
	"math/big"
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
// constructing a Sentinel with the appropriate raw bytes. The six forms
// derived here (raw, hex-lower, hex-upper, base64-std, base64-raw, decimal)
// cover the most common encoding paths; if a new type introduces additional
// representations (e.g. checksummed address strings), add them manually to
// Sentinel.Forms after construction.
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
//   - "raw"        — the raw bytes verbatim
//   - "hex-lower"  — lowercase hex encoding
//   - "hex-upper"  — uppercase hex encoding
//   - "base64-std" — standard base64 (padded)
//   - "base64-raw" — raw (unpadded) base64
//   - "decimal"    — decimal string rendering of new(big.Int).SetBytes(raw)
//
// These cover the most likely leak paths in log output. The decimal form
// catches the scenario where a private-key scalar is printed via %d or
// big.Int.String() — the Phase 2 usage that motivates including it now.
func NewSentinel(name string, raw []byte) Sentinel {
	type namedForm struct {
		name  string
		bytes []byte
	}

	forms := []namedForm{
		{"raw", raw},
		{"hex-lower", []byte(hex.EncodeToString(raw))},
		{"hex-upper", []byte(strings.ToUpper(hex.EncodeToString(raw)))},
		{"base64-std", []byte(base64.StdEncoding.EncodeToString(raw))},
		{"base64-raw", []byte(base64.RawStdEncoding.EncodeToString(raw))},
		{"decimal", []byte(new(big.Int).SetBytes(raw).String())},
	}

	s := Sentinel{Name: name}
	for _, f := range forms {
		// Deduplicate: skip forms that are empty or already present.
		if len(f.bytes) == 0 {
			continue
		}
		already := false
		for _, existing := range s.Forms {
			if bytes.Equal(existing, f.bytes) {
				already = true
				break
			}
		}
		if !already {
			s.Forms = append(s.Forms, f.bytes)
			s.names = append(s.names, f.name)
		}
	}
	return s
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
		if bytes.Contains(output, form) {
			found = append(found, s.names[i])
		}
	}
	return found
}
