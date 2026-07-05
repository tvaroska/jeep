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
	if !strings.HasPrefix(stdout.String(), "jeep-tts ") {
		t.Errorf("version output = %q, want prefix 'jeep-tts '", stdout.String())
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
