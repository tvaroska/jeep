package main

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
)

func TestDryRun_Text(t *testing.T) {
	t.Setenv("GOOGLE_CLOUD_PROJECT", "test-project")
	var stdout, stderr bytes.Buffer
	err := run([]string{"--dry-run", "-v", "Puck", "hello there"}, &stdout, &stderr)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	out := stdout.String()
	if !strings.Contains(out, "jeep-tts") || !strings.Contains(out, "voice:") {
		t.Errorf("dry-run output unexpected:\n%s", out)
	}
}

func TestDryRun_JSON(t *testing.T) {
	t.Setenv("GOOGLE_CLOUD_PROJECT", "test-project")
	var stdout, stderr bytes.Buffer
	err := run([]string{"--dry-run", "--format", "json", "hello"}, &stdout, &stderr)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	var got map[string]any
	if err := json.Unmarshal(stdout.Bytes(), &got); err != nil {
		t.Fatalf("output not valid JSON: %v", err)
	}
	if got["tool"] != "jeep-tts" {
		t.Errorf("tool = %v, want jeep-tts", got["tool"])
	}
}

func TestListVoices_Text(t *testing.T) {
	var stdout, stderr bytes.Buffer
	err := run([]string{"--list-voices"}, &stdout, &stderr)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(stdout.String(), "Kore") {
		t.Errorf("voice list missing Kore:\n%s", stdout.String())
	}
}

func TestListVoices_JSON(t *testing.T) {
	var stdout, stderr bytes.Buffer
	err := run([]string{"--list-voices", "--format", "json"}, &stdout, &stderr)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	var got []map[string]any
	if err := json.Unmarshal(stdout.Bytes(), &got); err != nil {
		t.Fatalf("output not valid JSON: %v", err)
	}
	if len(got) != 30 {
		t.Errorf("got %d voices, want 30", len(got))
	}
}
