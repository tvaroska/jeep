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
	if !strings.Contains(stderr.String(), "Usage:") {
		t.Errorf("help output missing usage:\n%s", stderr.String())
	}
}
