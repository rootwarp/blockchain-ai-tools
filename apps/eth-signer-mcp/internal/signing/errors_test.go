// Tests for ToolError — Issue 2.2 / 2.6.
// Internal tests (package signing).
package signing

import (
	"errors"
	"log/slog"
	"testing"
)

// TestToolError_Error verifies the Error() string format. Error() returns
// "Code: Message" in all cases — Cause is intentionally excluded to prevent
// accidental leakage of internal error details through the error string. Use
// Unwrap() or errors.Is/As to access Cause programmatically.
func TestToolError_Error(t *testing.T) {
	t.Parallel()

	t.Run("without-cause", func(t *testing.T) {
		t.Parallel()
		te := &ToolError{Code: CodeKeystoreError, Message: "test message"}
		got := te.Error()
		want := "keystore_error: test message"
		if got != want {
			t.Errorf("Error() = %q, want %q", got, want)
		}
	})

	// Even with a Cause set, Error() must NOT include Cause.Error() to prevent
	// accidental leakage of internal error details into caller-visible strings.
	// Cause is accessible via Unwrap() / errors.Is / errors.As.
	t.Run("with-cause-cause-not-in-error-string", func(t *testing.T) {
		t.Parallel()
		cause := errors.New("underlying-secret-cause")
		te := &ToolError{Code: CodePasswordError, Message: "bad password", Cause: cause}
		got := te.Error()
		want := "password_error: bad password"
		if got != want {
			t.Errorf("Error() = %q, want %q", got, want)
		}
		// Cause must not bleed into Error().
		if contains(got, "underlying-secret-cause") {
			t.Errorf("Error() leaks Cause text: %q", got)
		}
	})
}

// TestToolError_Unwrap verifies that errors.Is/As work through the ToolError chain.
func TestToolError_Unwrap(t *testing.T) {
	t.Parallel()

	cause := errors.New("cause-sentinel")
	te := &ToolError{Code: CodeInternalError, Message: "internal failure", Cause: cause}

	// errors.Is should find the cause via Unwrap.
	if !errors.Is(te, cause) {
		t.Errorf("errors.Is(*ToolError, cause): expected true")
	}

	// Unwrap nil cause returns nil.
	teNoCause := &ToolError{Code: CodeKeystoreError, Message: "no cause"}
	if teNoCause.Unwrap() != nil {
		t.Errorf("Unwrap() on no-cause ToolError: expected nil, got %v", teNoCause.Unwrap())
	}
}

// TestToolError_NilGuard verifies that (*ToolError)(nil).Error() does not panic.
func TestToolError_NilGuard(t *testing.T) {
	t.Parallel()

	var te *ToolError
	// Must not panic.
	got := te.Error()
	if got == "" {
		t.Error("nil ToolError.Error() should return a non-empty string")
	}
}

// TestToolError_LogValue verifies that LogValue never exposes the Cause field.
func TestToolError_LogValue(t *testing.T) {
	t.Parallel()

	cause := errors.New("super-secret-cause-content")
	te := &ToolError{
		Code:    CodeInternalError,
		Message: "public message",
		Cause:   cause,
	}

	// LogValue must return only Code and Message — never the cause string.
	lv := te.LogValue()
	str := lv.String()
	if str == "" {
		t.Fatal("LogValue().String() is empty")
	}
	if contains(str, "super-secret-cause-content") {
		t.Errorf("LogValue() leaked Cause: %q", str)
	}
	if !contains(str, "internal_error") {
		t.Errorf("LogValue() missing Code: %q", str)
	}
	if !contains(str, "public message") {
		t.Errorf("LogValue() missing Message: %q", str)
	}

	// Also verify via the slog handler path — slog.AnyValue should call LogValue.
	av := slog.AnyValue(te)
	if av.Kind() == slog.KindGroup {
		attrs := av.Group()
		for _, a := range attrs {
			if a.Key == "cause" {
				t.Errorf("LogValue() group contains 'cause' key")
			}
			if v := a.Value.String(); contains(v, "super-secret-cause-content") {
				t.Errorf("LogValue() group attr leaked Cause in key=%q", a.Key)
			}
		}
	}
}

// contains is a simple substring helper for test use.
func contains(s, sub string) bool {
	return len(s) >= len(sub) && (s == sub || len(sub) == 0 || indexSubstr(s, sub) >= 0)
}

func indexSubstr(s, sub string) int {
	for i := 0; i <= len(s)-len(sub); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}

// TestToolError_CodeConstants verifies that all six code constants are distinct
// non-empty strings, preventing accidental collisions.
func TestToolError_CodeConstants(t *testing.T) {
	t.Parallel()

	codes := []string{
		CodeKeystoreError,
		CodePasswordError,
		CodeInvalidInput,
		CodeUnsupportedType,
		CodeChainIDMismatch,
		CodeInternalError,
	}
	seen := make(map[string]bool)
	for _, c := range codes {
		if c == "" {
			t.Errorf("code constant is empty string")
			continue
		}
		if seen[c] {
			t.Errorf("duplicate code constant: %q", c)
		}
		seen[c] = true
	}
}
