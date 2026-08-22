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
	err := run([]string{"--dry-run", "../../internal/pdfsource/testdata/toc.pdf"}, &stdout, &stderr)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	out := stdout.String()
	if !strings.Contains(out, "Chapter One") {
		t.Errorf("dry-run missing 'Chapter One':\n%s", out)
	}
	if !strings.Contains(out, "Chapter Two") {
		t.Errorf("dry-run missing 'Chapter Two':\n%s", out)
	}
	if !strings.Contains(out, "Chapter Three") {
		t.Errorf("dry-run missing 'Chapter Three':\n%s", out)
	}
	if !strings.Contains(out, "Planned 3 chapter(s)") {
		t.Errorf("dry-run missing planned count:\n%s", out)
	}
}

func TestDryRun_JSON(t *testing.T) {
	t.Setenv("GOOGLE_CLOUD_PROJECT", "test-project")
	var stdout, stderr bytes.Buffer
	err := run([]string{"--dry-run", "--format", "json", "../../internal/pdfsource/testdata/toc.pdf"}, &stdout, &stderr)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	var got map[string]any
	if err := json.Unmarshal(stdout.Bytes(), &got); err != nil {
		t.Fatalf("output not valid JSON: %v", err)
	}
	if got["tool"] != "jeep-audiobook" {
		t.Errorf("tool = %v, want jeep-audiobook", got["tool"])
	}
	chapters, ok := got["chapters"].([]any)
	if !ok || len(chapters) != 3 {
		t.Errorf("chapters = %v, want length 3", got["chapters"])
	}
}

func TestDryRun_ChapterSelection(t *testing.T) {
	t.Setenv("GOOGLE_CLOUD_PROJECT", "test-project")
	var stdout, stderr bytes.Buffer
	err := run([]string{"--dry-run", "--chapters", "2", "../../internal/pdfsource/testdata/toc.pdf"}, &stdout, &stderr)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	out := stdout.String()
	if !strings.Contains(out, "Chapter Two") {
		t.Errorf("dry-run missing 'Chapter Two':\n%s", out)
	}
	if !strings.Contains(out, "Planned 1 chapter(s)") {
		t.Errorf("dry-run should show 1 chapter:\n%s", out)
	}
}

func TestUnsupportedExtension(t *testing.T) {
	t.Setenv("GOOGLE_CLOUD_PROJECT", "test-project")
	var stdout, stderr bytes.Buffer
	err := run([]string{"--dry-run", "foo.txt"}, &stdout, &stderr)
	if err == nil {
		t.Fatal("expected error for unsupported extension")
	}
	if !strings.Contains(err.Error(), "unsupported") {
		t.Errorf("error should mention unsupported format: %v", err)
	}
}

func TestMissingInputArg(t *testing.T) {
	t.Setenv("GOOGLE_CLOUD_PROJECT", "test-project")
	var stdout, stderr bytes.Buffer
	err := run([]string{"--dry-run"}, &stdout, &stderr)
	if err == nil {
		t.Fatal("expected error when no input file provided")
	}
	if !strings.Contains(err.Error(), "expected exactly one input file") {
		t.Errorf("error should mention missing input: %v", err)
	}
}

func TestDryRun_InvalidMode(t *testing.T) {
	t.Setenv("GOOGLE_CLOUD_PROJECT", "test-project")
	var stdout, stderr bytes.Buffer
	err := run([]string{"--dry-run", "--mode", "bogus", "../../internal/pdfsource/testdata/toc.pdf"}, &stdout, &stderr)
	if err == nil {
		t.Fatal("expected error for invalid mode")
	}
	if !strings.Contains(err.Error(), "invalid --mode") {
		t.Errorf("error should mention invalid --mode: %v", err)
	}
	if !strings.Contains(err.Error(), "full, summary, or abstract") {
		t.Errorf("error should list valid modes: %v", err)
	}
}

func TestDryRun_ValidMode(t *testing.T) {
	t.Setenv("GOOGLE_CLOUD_PROJECT", "test-project")
	var stdout, stderr bytes.Buffer
	err := run([]string{"--dry-run", "--mode", "abstract", "../../internal/pdfsource/testdata/toc.pdf"}, &stdout, &stderr)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	out := stdout.String()
	// Should still list the 3 chapters (dry-run is mode-agnostic).
	if !strings.Contains(out, "Chapter One") {
		t.Errorf("dry-run missing 'Chapter One':\n%s", out)
	}
	if !strings.Contains(out, "Planned 3 chapter(s)") {
		t.Errorf("dry-run missing planned count:\n%s", out)
	}
	// Should mention the mode.
	if !strings.Contains(out, "Mode:") || !strings.Contains(out, "abstract") {
		t.Errorf("dry-run should mention mode=abstract:\n%s", out)
	}
}
