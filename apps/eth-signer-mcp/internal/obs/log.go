package obs

import (
	"io"
	"log/slog"
	"os"
	"strings"
)

// NewLogger returns a JSON-handler *slog.Logger writing to stderr.
// The level string is parsed case-insensitively (debug|info|warn|error);
// any unrecognised value silently falls back to slog.LevelInfo.
// The constructor is infallible — it never returns an error or panics.
func NewLogger(level string) *slog.Logger {
	return newLoggerTo(os.Stderr, level)
}

// newLoggerTo constructs a JSON-handler logger writing to w at the given level.
// It is unexported so the public API remains exactly NewLogger(level string);
// tests use it to capture output without directing real logs to stderr.
func newLoggerTo(w io.Writer, level string) *slog.Logger {
	lvl := parseLevel(level)
	return slog.New(slog.NewJSONHandler(w, &slog.HandlerOptions{Level: lvl}))
}

// parseLevel converts a case-insensitive level string to a slog.Level.
// Unrecognised strings fall back to slog.LevelInfo.
func parseLevel(level string) slog.Level {
	switch strings.ToLower(level) {
	case "debug":
		return slog.LevelDebug
	case "info":
		return slog.LevelInfo
	case "warn":
		return slog.LevelWarn
	case "error":
		return slog.LevelError
	default:
		return slog.LevelInfo
	}
}
