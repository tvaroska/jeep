package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDryRun_Text(t *testing.T) {
	t.Setenv("GOOGLE_CLOUD_PROJECT", "test-project")
	dir := t.TempDir()
	ref := filepath.Join(dir, "ref.png")
	os.WriteFile(ref, []byte("img"), 0644)

	var stdout, stderr bytes.Buffer
	err := run([]string{"--dry-run", "-f", ref, "a cat"}, &stdout, &stderr)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	out := stdout.String()
	if !strings.Contains(out, "jeep-image") || !strings.Contains(out, "ref.png") {
		t.Errorf("dry-run output unexpected:\n%s", out)
	}
}

func TestDryRun_JSON(t *testing.T) {
	t.Setenv("GOOGLE_CLOUD_PROJECT", "test-project")
	var stdout, stderr bytes.Buffer
	err := run([]string{"--dry-run", "--format", "json", "a cat"}, &stdout, &stderr)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	var got map[string]any
	if err := json.Unmarshal(stdout.Bytes(), &got); err != nil {
		t.Fatalf("output not valid JSON: %v", err)
	}
	if got["tool"] != "jeep-image" {
		t.Errorf("tool = %v, want jeep-image", got["tool"])
	}
}
