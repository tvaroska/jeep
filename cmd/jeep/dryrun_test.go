package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tvaroska/jeep/internal/cli"
)

func TestDryRun_TextWithExtras(t *testing.T) {
	t.Setenv("GOOGLE_CLOUD_PROJECT", "test-project")
	dir := t.TempDir()
	f := filepath.Join(dir, "doc.txt")
	os.WriteFile(f, []byte("data"), 0644)

	var stdout, stderr bytes.Buffer
	err := run([]string{
		"--dry-run", "--search", "--system", "be nice",
		"--temperature", "0.5", "--top-p", "0.9", "--max-tokens", "128",
		"-f", f, "explain this",
	}, &stdout, &stderr)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	out := stdout.String()
	for _, want := range []string{"jeep", "search:", "temperature:", "top_p:", "max_tokens:", "system:", "doc.txt"} {
		if !strings.Contains(out, want) {
			t.Errorf("dry-run output missing %q:\n%s", want, out)
		}
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
	if got["tool"] != "jeep" {
		t.Errorf("tool = %v, want jeep", got["tool"])
	}
}

func TestDryRun_WithSchemaFile(t *testing.T) {
	t.Setenv("GOOGLE_CLOUD_PROJECT", "test-project")
	dir := t.TempDir()
	schema := filepath.Join(dir, "s.json")
	os.WriteFile(schema, []byte(`{"type":"OBJECT","properties":{"name":{"type":"STRING"}}}`), 0644)

	var stdout, stderr bytes.Buffer
	err := run([]string{"--dry-run", "--schema", schema, "extract"}, &stdout, &stderr)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(stdout.String(), "schema:") {
		t.Errorf("dry-run output missing schema flag:\n%s", stdout.String())
	}
}

func TestBadSchemaFile(t *testing.T) {
	t.Setenv("GOOGLE_CLOUD_PROJECT", "test-project")
	dir := t.TempDir()
	schema := filepath.Join(dir, "bad.json")
	os.WriteFile(schema, []byte(`{not json`), 0644)

	var stdout, stderr bytes.Buffer
	err := run([]string{"--schema", schema, "extract"}, &stdout, &stderr)
	if err == nil {
		t.Fatal("expected error for invalid schema file")
	}
	var exitErr *cli.ExitError
	if !errors.As(err, &exitErr) || exitErr.Code != cli.ExitUsage {
		t.Errorf("error = %v, want ExitUsage", err)
	}
}

func TestJSONInput_DryRun(t *testing.T) {
	t.Setenv("GOOGLE_CLOUD_PROJECT", "test-project")
	dir := t.TempDir()
	reqFile := filepath.Join(dir, "req.json")
	os.WriteFile(reqFile, []byte(`{"prompt":"hi","model":"custom-model","system":"s","search":true}`), 0644)

	var stdout, stderr bytes.Buffer
	err := run([]string{"--json", reqFile, "--dry-run"}, &stdout, &stderr)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(stdout.String(), "custom-model") {
		t.Errorf("dry-run should reflect model from JSON file:\n%s", stdout.String())
	}
}

func TestJSONInput_Empty(t *testing.T) {
	t.Setenv("GOOGLE_CLOUD_PROJECT", "test-project")
	dir := t.TempDir()
	reqFile := filepath.Join(dir, "empty.json")
	os.WriteFile(reqFile, []byte(`{}`), 0644)

	var stdout, stderr bytes.Buffer
	err := run([]string{"--json", reqFile}, &stdout, &stderr)
	if err == nil {
		t.Fatal("expected error when JSON file has no prompt or messages")
	}
}

func TestJSONInput_Missing(t *testing.T) {
	t.Setenv("GOOGLE_CLOUD_PROJECT", "test-project")
	var stdout, stderr bytes.Buffer
	err := run([]string{"--json", "/no/such/file.json"}, &stdout, &stderr)
	if err == nil {
		t.Fatal("expected error for missing JSON file")
	}
}

func TestLoadSchema(t *testing.T) {
	dir := t.TempDir()
	good := filepath.Join(dir, "good.json")
	os.WriteFile(good, []byte(`{"type":"OBJECT"}`), 0644)
	if _, err := loadSchema(good); err != nil {
		t.Fatalf("loadSchema(good): %v", err)
	}

	if _, err := loadSchema("/no/such/schema.json"); err == nil {
		t.Fatal("expected error for missing schema file")
	}

	bad := filepath.Join(dir, "bad.json")
	os.WriteFile(bad, []byte(`{bad`), 0644)
	if _, err := loadSchema(bad); err == nil {
		t.Fatal("expected error for invalid schema JSON")
	}
}
