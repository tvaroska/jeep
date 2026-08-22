// Package log provides structured logging using log/slog.
// Cost/usage reporting lives in internal/usage; this package stays log-only.
package log

import (
	"io"
	"log/slog"
	"os"
	"strings"
)

// New builds a slog.Logger writing to stderr at the given level
// ("debug"|"info"|"warn"|"error"; unknown => info). Text handler by default.
func New(level string) *slog.Logger {
	return newWithWriter(os.Stderr, level)
}

// newWithWriter is an unexported helper for testing that allows specifying
// the output writer. New delegates to this.
func newWithWriter(w io.Writer, level string) *slog.Logger {
	lvl := parseLevel(level)
	handler := slog.NewTextHandler(w, &slog.HandlerOptions{
		Level: lvl,
	})
	return slog.New(handler)
}

// parseLevel converts a level string to slog.Level, case-insensitively.
// Returns slog.LevelInfo for empty or unknown strings.
func parseLevel(level string) slog.Level {
	switch strings.ToLower(strings.TrimSpace(level)) {
	case "debug":
		return slog.LevelDebug
	case "info":
		return slog.LevelInfo
	case "warn", "warning":
		return slog.LevelWarn
	case "error":
		return slog.LevelError
	default:
		return slog.LevelInfo
	}
}
