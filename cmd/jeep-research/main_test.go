package main

import (
	"bytes"
	"strings"
	"testing"
)

func TestVersion(t *testing.T) {
	var stdout, stderr bytes.Buffer
	err := run([]string{"--version"}, &stdout, &stderr)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.HasPrefix(stdout.String(), "jeep-research ") {
		t.Errorf("version output = %q, want prefix 'jeep-research '", stdout.String())
	}
}

func TestNoPrompt(t *testing.T) {
	var stdout, stderr bytes.Buffer
	err := run([]string{}, &stdout, &stderr)
	if err == nil {
		t.Fatal("expected error when no prompt provided")
	}
}

func TestMissingProject(t *testing.T) {
	t.Setenv("GOOGLE_CLOUD_PROJECT", "")
	t.Setenv("GCLOUD_PROJECT", "")
	t.Setenv("PATH", "")
	var stdout, stderr bytes.Buffer
	err := run([]string{"hello"}, &stdout, &stderr)
	if err == nil {
		t.Fatal("expected error when no project available")
	}
	if !strings.Contains(err.Error(), "GCP project") {
		t.Errorf("error = %q, want mention of GCP project", err.Error())
	}
}

func TestUnknownFlag(t *testing.T) {
	var stdout, stderr bytes.Buffer
	err := run([]string{"--bogus"}, &stdout, &stderr)
	if err == nil {
		t.Fatal("expected error for unknown flag")
	}
}

func TestDryRun(t *testing.T) {
	t.Setenv("GOOGLE_CLOUD_PROJECT", "test-project")
	var stdout, stderr bytes.Buffer
	err := run([]string{"--dry-run", "test query"}, &stdout, &stderr)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	out := stdout.String()
	if !strings.Contains(out, "jeep-research") {
		t.Errorf("dry-run output missing tool name: %q", out)
	}
	if !strings.Contains(out, "deep-research") {
		t.Errorf("dry-run output missing agent: %q", out)
	}
}

func TestDryRunJSON(t *testing.T) {
	t.Setenv("GOOGLE_CLOUD_PROJECT", "test-project")
	var stdout, stderr bytes.Buffer
	err := run([]string{"--dry-run", "--format", "json", "test query"}, &stdout, &stderr)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	out := stdout.String()
	if !strings.Contains(out, `"tool"`) {
		t.Errorf("dry-run JSON missing tool field: %q", out)
	}
}
