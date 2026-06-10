package obs

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"strings"
	"testing"

	"github.com/rootwarp/blockchain-ai-tools/apps/eth-signer-mcp/internal/signing"
)

// TestNewLogger_NonNil verifies that NewLogger returns a non-nil logger.
func TestNewLogger_NonNil(t *testing.T) {
	t.Parallel()
	logger := NewLogger("info")
	if logger == nil {
		t.Fatal("NewLogger returned nil")
	}
}

// TestNewLogger_JSONOutput verifies that log output is valid JSON with the
// expected slog default keys (time/level/msg).
func TestNewLogger_JSONOutput(t *testing.T) {
	t.Parallel()
	var buf bytes.Buffer
	logger := newLoggerTo(&buf, "info")
	logger.Info("hello world")

	line := strings.TrimSpace(buf.String())
	if line == "" {
		t.Fatal("expected log output, got empty string")
	}

	var m map[string]any
	if err := json.Unmarshal([]byte(line), &m); err != nil {
		t.Fatalf("log output is not valid JSON: %v", err)
	}
	for _, key := range []string{"time", "level", "msg"} {
		if _, ok := m[key]; !ok {
			t.Errorf("missing expected JSON key %q in log output", key)
		}
	}
	if got := m["msg"]; got != "hello world" {
		t.Errorf("msg = %q, want %q", got, "hello world")
	}
}

// TestNewLogger_LevelFiltering verifies level filtering is honoured.
// At each configured level, only messages at that level or above appear.
func TestNewLogger_LevelFiltering(t *testing.T) {
	t.Parallel()

	type levelCase struct {
		configLevel string
		// wantVisible: slog level name → should output appear?
		wantVisible map[string]bool
	}

	cases := []levelCase{
		{
			configLevel: "debug",
			wantVisible: map[string]bool{"DEBUG": true, "INFO": true, "WARN": true, "ERROR": true},
		},
		{
			configLevel: "info",
			wantVisible: map[string]bool{"DEBUG": false, "INFO": true, "WARN": true, "ERROR": true},
		},
		{
			configLevel: "warn",
			wantVisible: map[string]bool{"DEBUG": false, "INFO": false, "WARN": true, "ERROR": true},
		},
		{
			configLevel: "error",
			wantVisible: map[string]bool{"DEBUG": false, "INFO": false, "WARN": false, "ERROR": true},
		},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.configLevel, func(t *testing.T) {
			t.Parallel()
			var buf bytes.Buffer
			logger := newLoggerTo(&buf, tc.configLevel)

			logger.Debug("debug message")
			logger.Info("info message")
			logger.Warn("warn message")
			logger.Error("error message")

			output := buf.String()
			for levelStr, wantPresent := range tc.wantVisible {
				found := false
				for _, rawLine := range strings.Split(strings.TrimSpace(output), "\n") {
					if rawLine == "" {
						continue
					}
					var m map[string]any
					if err := json.Unmarshal([]byte(rawLine), &m); err != nil {
						t.Errorf("invalid JSON line: %v", err)
						continue
					}
					if lvl, _ := m["level"].(string); strings.EqualFold(lvl, levelStr) {
						found = true
						break
					}
				}
				if wantPresent && !found {
					t.Errorf("configLevel=%q: expected %q message to appear, but it did not", tc.configLevel, levelStr)
				}
				if !wantPresent && found {
					t.Errorf("configLevel=%q: expected %q message to be suppressed, but it appeared", tc.configLevel, levelStr)
				}
			}
		})
	}
}

// TestNewLogger_GarbageLevelFallsBackToInfo verifies that an unknown level
// string falls back to info (debug suppressed, info emitted) without error or panic.
func TestNewLogger_GarbageLevelFallsBackToInfo(t *testing.T) {
	t.Parallel()
	var buf bytes.Buffer
	logger := newLoggerTo(&buf, "garbage")

	logger.Debug("should not appear")
	logger.Info("should appear")

	output := buf.String()

	// Debug should be suppressed.
	for _, line := range strings.Split(strings.TrimSpace(output), "\n") {
		if line == "" {
			continue
		}
		var m map[string]any
		if err := json.Unmarshal([]byte(line), &m); err != nil {
			t.Errorf("invalid JSON line: %v", err)
			continue
		}
		if lvl, _ := m["level"].(string); strings.EqualFold(lvl, "DEBUG") {
			t.Error("debug message appeared with garbage level; expected info fallback to suppress it")
		}
	}

	// Info should appear.
	if !strings.Contains(output, "should appear") {
		t.Error("info message did not appear; expected info fallback to allow it")
	}
}

// TestLeakScan_ObsLogger verifies that a Secret-wrapped sentinel does not appear
// in any form in obs logger output, at every log level.
//
// This test imports internal/signing solely for its Sentinel and Secret[T]
// helpers — an explicitly permitted test-only edge per ADR-008. The obs package
// production code does NOT import signing.
//
// SAFETY: on any scan failure, only Sentinel.Name and leaked form names are
// reported — never the captured buffer or its contents.
func TestLeakScan_ObsLogger(t *testing.T) {
	t.Parallel()

	// fixtureRaw is the same sentinel used in internal/signing/secret_test.go.
	const fixtureName = "obs-fixture-secret"
	fixtureRaw := []byte("SENTINEL-7f3a9c-DO-NOT-LOG")
	sent := signing.NewSentinel(fixtureName, fixtureRaw)
	secret := signing.NewSecret(string(fixtureRaw))

	levels := []struct {
		name  string
		logFn func(l *slog.Logger)
	}{
		{
			"debug",
			func(l *slog.Logger) { l.Debug("obs-test-event", "secret", secret) },
		},
		{
			"info",
			func(l *slog.Logger) { l.Info("obs-test-event", "secret", secret) },
		},
		{
			"warn",
			func(l *slog.Logger) { l.Warn("obs-test-event", "secret", secret) },
		},
		{
			"error",
			func(l *slog.Logger) { l.Error("obs-test-event", "secret", secret) },
		},
	}

	for _, tc := range levels {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			var buf bytes.Buffer
			// newLoggerTo is the unexported helper in obs/log.go; since this
			// test is in package obs (same package), it can call it directly.
			logger := newLoggerTo(&buf, tc.name)
			tc.logFn(logger)

			output := buf.Bytes()
			// SAFETY: do NOT include `output` in failure messages — report form names only.
			leaked := sent.Scan(output)
			if len(leaked) > 0 {
				t.Errorf("obs logger at level %q leaked sentinel forms: %v (sentinel: %q)",
					tc.name, leaked, sent.Name)
			}
		})
	}
}

// TestNewLogger_CaseInsensitiveLevel verifies that level strings are accepted
// case-insensitively.
func TestNewLogger_CaseInsensitiveLevel(t *testing.T) {
	t.Parallel()
	for _, levelStr := range []string{"WARN", "Warn", "wArN"} {
		levelStr := levelStr
		t.Run(levelStr, func(t *testing.T) {
			t.Parallel()
			var buf bytes.Buffer
			logger := newLoggerTo(&buf, levelStr)
			logger.Info("should be suppressed at warn")
			logger.Warn("should appear")

			output := buf.String()

			infoSuppressed := true
			warnPresent := false
			for _, line := range strings.Split(strings.TrimSpace(output), "\n") {
				if line == "" {
					continue
				}
				var m map[string]any
				if err := json.Unmarshal([]byte(line), &m); err != nil {
					t.Errorf("invalid JSON line: %v", err)
					continue
				}
				lvl, _ := m["level"].(string)
				if strings.EqualFold(lvl, "INFO") {
					infoSuppressed = false
				}
				if strings.EqualFold(lvl, "WARN") {
					warnPresent = true
				}
			}
			if !infoSuppressed {
				t.Errorf("level=%q: info should be suppressed at warn level", levelStr)
			}
			if !warnPresent {
				t.Errorf("level=%q: warn message should appear", levelStr)
			}
		})
	}
}
