package signing_test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"testing"

	"github.com/rootwarp/blockchain-ai-tools/apps/eth-signer-mcp/internal/signing"
)

// fixtureRaw is the sentinel used across all leak-scan tests in this file.
// It is long and distinctive so that its encoded forms can't collide with
// innocent output.
var fixtureRaw = []byte("SENTINEL-7f3a9c-DO-NOT-LOG")

// newFixtureSentinel builds the shared Sentinel for all tests in this file.
func newFixtureSentinel() signing.Sentinel {
	return signing.NewSentinel("fixture-secret", fixtureRaw)
}

// reportRedactFailure reports a redaction failure WITHOUT leaking the secret.
// The redacting APIs are fed the fixture sentinel, so a broken implementation
// would return the secret itself — printing `got` directly would re-leak it into
// the test log. Instead we scan `got` for sentinel forms and name only the
// leaked form(s) (the sanitized-failure-message rule). desc names the API.
func reportRedactFailure(t *testing.T, desc, got string) {
	t.Helper()
	sent := newFixtureSentinel()
	if leaked := sent.Scan([]byte(got)); len(leaked) > 0 {
		t.Errorf("%s leaked sentinel forms: %v (sentinel: %q)", desc, leaked, sent.Name)
	} else {
		t.Errorf("%s did not return \"[REDACTED]\" (no sentinel forms; got length=%d)", desc, len(got))
	}
}

// TestSecret_Stringer verifies that fmt.Stringer returns "[REDACTED]".
func TestSecret_Stringer(t *testing.T) {
	t.Parallel()
	s := signing.NewSecret(string(fixtureRaw))
	got := s.String()
	if got != "[REDACTED]" {
		reportRedactFailure(t, "String()", got)
	}
}

// TestSecret_GoStringer verifies that fmt.GoStringer (%#v) returns "[REDACTED]".
func TestSecret_GoStringer(t *testing.T) {
	t.Parallel()
	s := signing.NewSecret(string(fixtureRaw))
	got := fmt.Sprintf("%#v", s)
	if got != "[REDACTED]" {
		reportRedactFailure(t, "%#v", got)
	}
}

// TestSecret_Formatter verifies that fmt.Formatter writes "[REDACTED]" for every verb.
func TestSecret_Formatter(t *testing.T) {
	t.Parallel()
	s := signing.NewSecret(string(fixtureRaw))
	verbs := []string{"%v", "%+v", "%#v", "%s", "%q", "%x", "%X", "%d"}
	for _, verb := range verbs {
		verb := verb
		t.Run(verb, func(t *testing.T) {
			t.Parallel()
			got := fmt.Sprintf(verb, s)
			if got != "[REDACTED]" {
				reportRedactFailure(t, "Sprintf("+verb+")", got)
			}
		})
	}
}

// TestSecret_Format_KnownVerbGaps documents the %T / %p gap: Go's fmt does NOT
// route those two verbs through fmt.Formatter, so Secret cannot redact them.
// This test pins that known behaviour (so a future Go change that altered it
// would be noticed) and proves %T is safe (type name only). It deliberately
// wraps a HARMLESS marker — never a real secret — because %p prints fmt's
// bad-verb error containing the struct's field values.
func TestSecret_Format_KnownVerbGaps(t *testing.T) {
	t.Parallel()
	const harmless = "harmless-marker"
	s := signing.NewSecret(harmless)

	// %T: type name only, no value bytes — safe, and not "[REDACTED]".
	gotT := fmt.Sprintf("%T", s)
	if !strings.Contains(gotT, "Secret") {
		t.Errorf("%%T = %q, want a string containing the type name \"Secret\"", gotT)
	}
	if gotT == "[REDACTED]" {
		t.Errorf("%%T = %q; fmt is not expected to route %%T through Formatter", gotT)
	}

	// %p: KNOWN GAP — fmt does not call Formatter; output is NOT redacted.
	// (Using a harmless marker so this documented leak path exposes nothing real.)
	gotP := fmt.Sprintf("%p", s)
	if gotP == "[REDACTED]" {
		t.Errorf("%%p unexpectedly returned [REDACTED]; the documented gap (fmt skips " +
			"Formatter for %%p) no longer holds — update secret.go's KNOWN GAP note")
	}
}

// TestSecret_JSONMarshaler verifies that json.Marshal returns `"[REDACTED]"`.
func TestSecret_JSONMarshaler(t *testing.T) {
	t.Parallel()
	s := signing.NewSecret(string(fixtureRaw))
	b, err := json.Marshal(s)
	if err != nil {
		t.Fatalf("json.Marshal error: %v", err)
	}
	if string(b) != `"[REDACTED]"` {
		reportRedactFailure(t, "json.Marshal", string(b))
	}
}

// TestSecret_SlogLogValuer verifies that slog.LogValuer returns slog.StringValue("[REDACTED]").
func TestSecret_SlogLogValuer(t *testing.T) {
	t.Parallel()
	s := signing.NewSecret(string(fixtureRaw))
	var buf bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug}))
	logger.Info("test", "key", s)

	output := buf.String()
	// SAFETY: do NOT include raw buffer in failure messages — report form names only.
	// Check that the output contains [REDACTED] and not any form of the sentinel.
	sent := newFixtureSentinel()
	leaked := sent.Scan([]byte(output))
	if len(leaked) > 0 {
		// Sanitized: report form names, never the buffer.
		t.Errorf("slog.LogValuer leaked sentinel forms: %v (sentinel name: %q)", leaked, sent.Name)
	}
	if !strings.Contains(output, "[REDACTED]") {
		t.Error("slog output does not contain [REDACTED]")
	}
}

// TestSecret_Expose verifies that Expose returns the wrapped value bitwise-equal to the input.
func TestSecret_Expose(t *testing.T) {
	t.Parallel()
	input := "some-secret-value"
	s := signing.NewSecret(input)
	got := s.Expose()
	if got != input {
		t.Errorf("Expose() = %q, want %q", got, input)
	}
}

// TestSecret_ExposeBytes verifies Expose round-trip for []byte type.
func TestSecret_ExposeBytes(t *testing.T) {
	t.Parallel()
	input := []byte{0x01, 0x02, 0xAB, 0xFF}
	s := signing.NewSecret(input)
	got := s.Expose()
	if !bytes.Equal(got, input) {
		t.Errorf("Expose() bytes not equal to input")
	}
}

// TestSecret_GoStringDirect verifies GoString() itself returns "[REDACTED]".
// Note: when Secret implements fmt.Formatter, fmt.Sprintf("%#v", s) calls Format
// rather than GoString — the fmt.Formatter takes precedence. GoString is still
// provided as belt-and-suspenders for any code that calls .GoString() directly
// (e.g. text/template's %#v-equivalent, or direct interface calls).
func TestSecret_GoStringDirect(t *testing.T) {
	t.Parallel()
	s := signing.NewSecret(string(fixtureRaw))
	if got := s.GoString(); got != "[REDACTED]" {
		reportRedactFailure(t, "GoString()", got)
	}
}

// TestLeakScan_FmtVerbs scans all fmt.Sprintf renderings for sentinel leaks.
func TestLeakScan_FmtVerbs(t *testing.T) {
	t.Parallel()
	sent := newFixtureSentinel()
	s := signing.NewSecret(string(fixtureRaw))

	verbs := []string{"%v", "%+v", "%#v", "%s", "%q", "%x", "%X", "%d"}
	for _, verb := range verbs {
		verb := verb
		t.Run(verb, func(t *testing.T) {
			t.Parallel()
			output := []byte(fmt.Sprintf(verb, s))
			// SAFETY: do NOT log `output` in failure messages — report form names only.
			leaked := sent.Scan(output)
			if len(leaked) > 0 {
				t.Errorf("fmt.Sprintf(%q, Secret) leaked sentinel forms: %v (sentinel name: %q)",
					verb, leaked, sent.Name)
			}
		})
	}
}

// TestLeakScan_JSONMarshal scans json.Marshal output for sentinel leaks.
func TestLeakScan_JSONMarshal(t *testing.T) {
	t.Parallel()
	sent := newFixtureSentinel()
	s := signing.NewSecret(string(fixtureRaw))
	b, err := json.Marshal(s)
	if err != nil {
		t.Fatalf("json.Marshal error: %v", err)
	}
	// SAFETY: do NOT include b in failure messages — report form names only.
	leaked := sent.Scan(b)
	if len(leaked) > 0 {
		t.Errorf("json.Marshal leaked sentinel forms: %v (sentinel name: %q)", leaked, sent.Name)
	}
}

// TestLeakScan_SlogJSONHandler scans slog JSON handler output for sentinel leaks.
func TestLeakScan_SlogJSONHandler(t *testing.T) {
	t.Parallel()
	sent := newFixtureSentinel()
	s := signing.NewSecret(string(fixtureRaw))

	var buf bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug}))
	logger.Info("event", "key", s)

	output := buf.Bytes()
	// SAFETY: do NOT include output in failure messages — report form names only.
	leaked := sent.Scan(output)
	if len(leaked) > 0 {
		t.Errorf("slog JSON handler leaked sentinel forms: %v (sentinel name: %q)", leaked, sent.Name)
	}
}
