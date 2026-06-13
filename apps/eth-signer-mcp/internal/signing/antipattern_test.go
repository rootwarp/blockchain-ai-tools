package signing_test

import (
	"bytes"
	"log/slog"
	"testing"

	"github.com/rootwarp/blockchain-ai-tools/apps/eth-signer-mcp/internal/signing"
)

// naiveSecretWrapper simulates what a developer might write WITHOUT knowing to
// implement redacting interfaces. It wraps a secret value in an exported field,
// which means any reflection-based encoder (json.Marshal, slog, etc.) will
// read and encode the raw value directly.
//
// DO NOT USE THIS TYPE IN PRODUCTION CODE. It exists only to demonstrate the
// anti-pattern.
type naiveSecretWrapper struct {
	// ExposedValue is an exported field — json.Marshal, slog's JSON handler,
	// and any reflection-based tool will read and encode it directly.
	ExposedValue string
}

// structWithNaiveSecret simulates the dangerous pattern: embedding a
// naiveSecretWrapper (which has no redaction interfaces) in a struct that
// is then passed to slog. The slog JSON handler reflects over the struct's
// exported fields, encodes ExposedValue as a raw JSON string, and the secret
// leaks into the log output.
type structWithNaiveSecret struct {
	Name   string
	Secret naiveSecretWrapper
}

// TestKnownLeak_SecretEmbeddedInStruct documents and asserts the KNOWN LEAK
// that occurs when secret material is held in a struct with EXPORTED fields
// and that struct is embedded in a larger struct passed to slog.Info.
//
// # WHY THIS TEST EXISTS
//
// This test makes the "never embed a secret-holding struct in a logged struct"
// rule machine-checked and visible. It demonstrates the concrete failure mode
// that signing.Secret[T] is designed to prevent.
//
// # THE ANTI-PATTERN DEMONSTRATED HERE
//
// If a developer wraps secret material in a struct with exported fields
// (naiveSecretWrapper.ExposedValue) and then passes that outer struct to slog:
//
//  1. slog processes "payload", payload as a KindAny attribute.
//  2. The slog JSON handler calls json.Marshal(payload) for the outer struct
//     (since the outer struct doesn't implement json.Marshaler).
//  3. json.Marshal reflects over exported fields:
//     - payload.Name    → "example"
//     - payload.Secret  → json.Marshal(naiveSecretWrapper{ExposedValue:"..."})
//  4. naiveSecretWrapper doesn't implement json.Marshaler, so json.Marshal
//     reflects further and encodes ExposedValue directly → SECRET LEAKS.
//
// # WHY signing.Secret[T] PREVENTS THIS
//
// signing.Secret[T] implements ALL FIVE redacting interfaces:
//   - fmt.Stringer    → "[REDACTED]"
//   - fmt.GoStringer  → "[REDACTED]"
//   - fmt.Formatter   → "[REDACTED]" for every verb
//   - json.Marshaler  → `"[REDACTED]"` (prevents the json.Marshal reflection path)
//   - slog.LogValuer  → slog.StringValue("[REDACTED]")
//
// Because signing.Secret[T] implements json.Marshaler, when json.Marshal
// encounters a Secret field in a struct, it calls MarshalJSON() → "[REDACTED]"
// instead of reflecting through the inner value. The leak is prevented.
//
// # THE USAGE RULE
//
// Even with the full interface implementation, prefer passing secrets as
// explicit slog key-value pairs:
//
//	slog.Info("msg", "key", mySecret)       // correct: LogValue IS called
//	slog.Info("msg", "s", structWithS{...}) // RISKY: depends on json.Marshaler
//
// The rule "never embed a Secret in a logged struct" is the SAFEST posture.
//
// # SAFETY NOTE ON FAILURE MESSAGES
//
// Even though we are asserting a LEAK occurs in this test, we must NOT
// include the raw buffer in any failure message. We report only form names
// via Sentinel.Scan — never the bytes.
func TestKnownLeak_SecretEmbeddedInStruct(t *testing.T) {
	t.Parallel()

	sent := newFixtureSentinel()

	// Build the anti-pattern: naiveSecretWrapper holds secret bytes in an
	// EXPORTED field. This simulates a developer who wraps secrets without
	// implementing the required redaction interfaces.
	payload := structWithNaiveSecret{
		Name:   "example-request",
		Secret: naiveSecretWrapper{ExposedValue: string(fixtureRaw)},
	}

	var buf bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug}))

	// THIS IS THE ANTI-PATTERN: the outer struct containing a naive (non-redacting)
	// wrapper is passed directly to slog. The JSON handler calls json.Marshal on the
	// outer struct, which reflects into naiveSecretWrapper.ExposedValue and encodes
	// it as a raw string — leaking the secret into the log output.
	logger.Info("processing", "payload", payload)

	output := buf.Bytes()

	// SAFETY: do NOT include `output` in any failure message — report form names only.
	// The Sentinel.Scan function exists precisely to let us make this assertion without
	// echoing the leaked bytes.
	leaked := sent.Scan(output)

	if len(leaked) == 0 {
		// The leak did not occur. This would mean either:
		// (a) slog's JSON handler no longer uses json.Marshal for struct values, OR
		// (b) json.Marshal no longer reflects over exported fields.
		// Either change would be significant and warrants review of the anti-pattern.
		t.Logf("UNEXPECTED: naiveSecretWrapper did not leak via slog JSON handler.")
		t.Logf("Check slog/json.Marshal behaviour for struct values with exported fields.")
		t.Fatal("expected sentinel to leak via naiveSecretWrapper.ExposedValue, but no forms were found")
	}

	// The leak occurred as expected. Log the form names only (sanitized message).
	t.Logf("EXPECTED LEAK via naiveSecretWrapper.ExposedValue: forms leaked = %v (sentinel: %q)",
		leaked, sent.Name)
	t.Logf("This demonstrates WHY signing.Secret[T] implements json.Marshaler to prevent this path.")

	// Confirm that signing.Secret[T] does NOT leak under the same struct-embedding
	// scenario (because it implements json.Marshaler, which json.Marshal calls
	// instead of reflecting over the inner field).
	type structWithProperSecret struct {
		Name   string
		Secret signing.Secret[string]
	}
	properPayload := structWithProperSecret{
		Name:   "example-request",
		Secret: signing.NewSecret(string(fixtureRaw)),
	}
	var buf2 bytes.Buffer
	logger2 := slog.New(slog.NewJSONHandler(&buf2, &slog.HandlerOptions{Level: slog.LevelDebug}))
	logger2.Info("processing", "payload", properPayload)

	// SAFETY: do NOT include buf2 in failure messages.
	properLeaked := sent.Scan(buf2.Bytes())
	if len(properLeaked) > 0 {
		t.Errorf("signing.Secret[T] leaked forms via struct embedding: %v (sentinel: %q) — "+
			"MarshalJSON may not be called for generic type instantiation on this Go version",
			properLeaked, sent.Name)
	}
}
