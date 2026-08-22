package log

import (
	"bytes"
	"log/slog"
	"strings"
	"testing"
)

func TestNew_NoPanic(t *testing.T) {
	tests := []struct {
		name  string
		level string
	}{
		{"debug", "debug"},
		{"info", "info"},
		{"warn", "warn"},
		{"empty", ""},
		{"garbage", "garbage"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			logger := New(tt.level)
			if logger == nil {
				t.Errorf("New(%q) returned nil", tt.level)
			}
		})
	}
}

func TestLog_LevelGating(t *testing.T) {
	var buf bytes.Buffer

	// Create a logger at Warn level
	logger := newWithWriter(&buf, "warn")

	// Log at Info level (should be filtered out)
	logger.Info("info message should not appear")

	// Log at Warn level (should appear)
	logger.Warn("warn message should appear")

	output := buf.String()

	// Assert that only the Warn message appears
	if strings.Contains(output, "info message") {
		t.Errorf("Info message appeared in output when logger is at Warn level:\n%s", output)
	}
	if !strings.Contains(output, "warn message") {
		t.Errorf("Warn message did not appear in output:\n%s", output)
	}
}

func TestLog_LevelGatingDebug(t *testing.T) {
	var buf bytes.Buffer

	// Create a logger at Debug level
	logger := newWithWriter(&buf, "debug")

	// Log at all levels
	logger.Debug("debug message")
	logger.Info("info message")
	logger.Warn("warn message")
	logger.Error("error message")

	output := buf.String()

	// All messages should appear
	if !strings.Contains(output, "debug message") {
		t.Errorf("Debug message did not appear:\n%s", output)
	}
	if !strings.Contains(output, "info message") {
		t.Errorf("Info message did not appear:\n%s", output)
	}
	if !strings.Contains(output, "warn message") {
		t.Errorf("Warn message did not appear:\n%s", output)
	}
	if !strings.Contains(output, "error message") {
		t.Errorf("Error message did not appear:\n%s", output)
	}
}

func TestParseLevel(t *testing.T) {
	tests := []struct {
		input string
		want  slog.Level
	}{
		{"debug", slog.LevelDebug},
		{"DEBUG", slog.LevelDebug},
		{"info", slog.LevelInfo},
		{"INFO", slog.LevelInfo},
		{"warn", slog.LevelWarn},
		{"WARN", slog.LevelWarn},
		{"warning", slog.LevelWarn},
		{"error", slog.LevelError},
		{"ERROR", slog.LevelError},
		{"", slog.LevelInfo},
		{"garbage", slog.LevelInfo},
		{"  info  ", slog.LevelInfo}, // test trimming
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got := parseLevel(tt.input)
			if got != tt.want {
				t.Errorf("parseLevel(%q) = %v, want %v", tt.input, got, tt.want)
			}
		})
	}
}
