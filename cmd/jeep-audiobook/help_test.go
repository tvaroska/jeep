package main

import (
	"bytes"
	"strings"
	"testing"
)

func TestHelp(t *testing.T) {
	var stdout, stderr bytes.Buffer
	err := run([]string{"--help"}, &stdout, &stderr)
	if err == nil {
		t.Fatal("expected help error (pflag.ErrHelp)")
	}
	usage := stderr.String()
	if !strings.Contains(usage, "Usage:") {
		t.Errorf("help output missing usage:\n%s", usage)
	}

	// Verify all eight audiobook-specific flags are documented.
	for _, flag := range []string{"--voice", "--style", "--resume", "--chapters", "--from-page", "--to-page", "--concurrency", "--output"} {
		if !strings.Contains(usage, flag) {
			t.Errorf("help output missing %s:\n%s", flag, usage)
		}
	}
}
