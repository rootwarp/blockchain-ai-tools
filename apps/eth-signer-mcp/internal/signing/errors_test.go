// Tests for ToolError — Issue 2.2.
// Internal tests (package signing).
package signing

import (
	"errors"
	"testing"
)

// TestToolError_Error verifies the Error() string format for both the
// with-cause and without-cause cases.
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

	t.Run("with-cause", func(t *testing.T) {
		t.Parallel()
		cause := errors.New("underlying error")
		te := &ToolError{Code: CodePasswordError, Message: "bad password", Cause: cause}
		got := te.Error()
		want := "password_error: bad password: underlying error"
		if got != want {
			t.Errorf("Error() = %q, want %q", got, want)
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
