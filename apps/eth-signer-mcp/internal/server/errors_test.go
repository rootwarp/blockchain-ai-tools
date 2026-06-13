// Package server — errors_test.go — Issue 2.7.
//
// Wire-encoding contract tests for toolResult. These are the canonical
// contract tests that Phase 3 HTTP e2e tests will mirror over real HTTP.
//
// For each of the six ToolError codes (plus the non-ToolError path), this file
// asserts:
//   - IsError == true (ToolError path) OR non-nil Go error (non-ToolError path)
//   - Exactly one TextContent in Content
//   - Text parses as JSON with EXACTLY the keys "code" and "message"
//   - code matches the expected code constant
//   - handler returned nil Go error (ToolError path)
//   - Cause is NEVER serialised (not present in Content[0] text)
package server

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/rootwarp/blockchain-ai-tools/apps/eth-signer-mcp/internal/signing"
)

// TestToolResult_SixCodes is the table-driven contract test for all six
// ToolError code constants. Each row asserts the complete wire contract:
// IsError=true, one TextContent, JSON with exactly "code"+"message", code matches.
func TestToolResult_SixCodes(t *testing.T) {
	t.Parallel()

	const sentinelCause = "SENTINEL_CAUSE_SHOULD_NOT_APPEAR"

	cases := []struct {
		code    string
		message string
	}{
		{signing.CodeInvalidInput, "field X is required"},
		{signing.CodeUnsupportedType, "transaction type 0x3 is not supported"},
		{signing.CodeChainIDMismatch, "request chain-id 5 does not match guard 1"},
		{signing.CodeKeystoreError, "keystore file could not be read"},
		{signing.CodePasswordError, "password file is unreadable"},
		{signing.CodeInternalError, "recovered sender does not match keystore address"},
	}

	for _, tc := range cases {
		tc := tc // capture for parallel sub-tests
		t.Run(tc.code, func(t *testing.T) {
			t.Parallel()

			// Construct ToolError with a sentinel-bearing Cause.
			te := &signing.ToolError{
				Code:    tc.code,
				Message: tc.message,
				Cause:   errors.New(sentinelCause),
			}

			result, err := toolResult(te)

			// Contract 1: handler returns nil Go error (session stays alive).
			if err != nil {
				t.Fatalf("toolResult(%q): got non-nil Go error %v; want nil", tc.code, err)
			}

			// Contract 2: result is non-nil with IsError == true.
			if result == nil {
				t.Fatalf("toolResult(%q): result is nil; want non-nil", tc.code)
			}
			if !result.IsError {
				t.Errorf("toolResult(%q): IsError = false; want true", tc.code)
			}

			// Contract 3: exactly one TextContent item.
			if len(result.Content) != 1 {
				t.Fatalf("toolResult(%q): len(Content) = %d; want 1", tc.code, len(result.Content))
			}
			tc0, ok := result.Content[0].(*mcp.TextContent)
			if !ok {
				t.Fatalf("toolResult(%q): Content[0] is %T; want *mcp.TextContent", tc.code, result.Content[0])
			}

			// Contract 4: text parses as JSON with EXACTLY keys "code" and "message".
			var decoded map[string]json.RawMessage
			if err := json.Unmarshal([]byte(tc0.Text), &decoded); err != nil {
				t.Fatalf("toolResult(%q): Content[0].Text is not valid JSON: %v\ntext: %s", tc.code, err, tc0.Text)
			}
			wantKeys := []string{"code", "message"}
			if len(decoded) != len(wantKeys) {
				t.Errorf("toolResult(%q): JSON has %d keys (%v); want exactly %v",
					tc.code, len(decoded), mapKeys(decoded), wantKeys)
			}
			for _, k := range wantKeys {
				if _, ok := decoded[k]; !ok {
					t.Errorf("toolResult(%q): JSON missing key %q", tc.code, k)
				}
			}

			// Contract 5: "code" matches the expected code constant.
			var gotCode string
			if err := json.Unmarshal(decoded["code"], &gotCode); err != nil {
				t.Fatalf("toolResult(%q): cannot unmarshal code field: %v", tc.code, err)
			}
			if gotCode != tc.code {
				t.Errorf("toolResult(%q): code = %q; want %q", tc.code, gotCode, tc.code)
			}

			// Contract 6: "message" matches the expected message.
			var gotMsg string
			if err := json.Unmarshal(decoded["message"], &gotMsg); err != nil {
				t.Fatalf("toolResult(%q): cannot unmarshal message field: %v", tc.code, err)
			}
			if gotMsg != tc.message {
				t.Errorf("toolResult(%q): message = %q; want %q", tc.code, gotMsg, tc.message)
			}

			// Contract 7: Cause NEVER appears in Content[0] text.
			if strings.Contains(tc0.Text, sentinelCause) {
				t.Errorf("toolResult(%q): Cause sentinel found in Content[0].Text; must not be serialised", tc.code)
			}
		})
	}
}

// TestToolResult_NonToolError verifies that a non-ToolError returns (nil, err) —
// the protocol-level error path. The caller is responsible for wrapping this in
// a JSON-RPC error to signal a system failure.
func TestToolResult_NonToolError(t *testing.T) {
	t.Parallel()

	plainErr := fmt.Errorf("context deadline exceeded")
	result, err := toolResult(plainErr)

	// Must return nil result — NOT an IsError tool result.
	if result != nil {
		t.Errorf("toolResult(non-ToolError): result = %v; want nil", result)
	}

	// Must return the original error.
	if err == nil {
		t.Error("toolResult(non-ToolError): err is nil; want non-nil")
	}
	if err != plainErr {
		t.Errorf("toolResult(non-ToolError): err = %v; want %v", err, plainErr)
	}
}

// TestToolResult_WrappedToolError verifies that errors.As-based detection works
// when the *signing.ToolError is wrapped inside another error.
func TestToolResult_WrappedToolError(t *testing.T) {
	t.Parallel()

	te := &signing.ToolError{Code: signing.CodeInvalidInput, Message: "wrapped error test"}
	wrapped := fmt.Errorf("wrapper: %w", te)

	result, err := toolResult(wrapped)
	if err != nil {
		t.Fatalf("toolResult(wrapped ToolError): got non-nil Go error: %v", err)
	}
	if result == nil || !result.IsError {
		t.Error("toolResult(wrapped ToolError): expected IsError=true result")
	}
}

// TestToolResult_CompactJSON verifies that the JSON in Content[0] has no
// whitespace (compact, not pretty-printed) and that key order is "code" then
// "message" (stable for machine-parseable wire encoding).
func TestToolResult_CompactJSON(t *testing.T) {
	t.Parallel()

	// Use a message without spaces so the whitespace check can be strict about structure.
	te := &signing.ToolError{Code: signing.CodeInvalidInput, Message: "compact-test-no-spaces"}
	result, err := toolResult(te)
	if err != nil || result == nil {
		t.Fatalf("toolResult unexpectedly failed: result=%v, err=%v", result, err)
	}

	text := result.Content[0].(*mcp.TextContent).Text

	// No leading/trailing whitespace or newlines.
	if text != strings.TrimSpace(text) {
		t.Errorf("Content[0].Text has surrounding whitespace: %q", text)
	}
	// No internal whitespace between JSON tokens (the message has no spaces so
	// any space found is structural — produced by pretty-printing).
	if strings.ContainsAny(text, " \t\n\r") {
		t.Errorf("Content[0].Text contains structural whitespace (not compact): %q", text)
	}
	// Key "code" comes before key "message" (stable order).
	codeIdx := strings.Index(text, `"code"`)
	msgIdx := strings.Index(text, `"message"`)
	if codeIdx < 0 || msgIdx < 0 {
		t.Fatalf("Content[0].Text missing expected keys: %q", text)
	}
	if codeIdx > msgIdx {
		t.Errorf("Key order: 'code' appears after 'message'; want code before message: %q", text)
	}
}

// mapKeys returns the keys of a map, for use in test error messages.
func mapKeys(m map[string]json.RawMessage) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	return keys
}
