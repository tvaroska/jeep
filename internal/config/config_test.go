package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoad_Missing(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	cfg := Load()
	if cfg.Project != "" {
		t.Errorf("project = %q, want empty", cfg.Project)
	}
}

func TestLoad_Valid(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)

	jeepDir := filepath.Join(dir, "jeep")
	os.MkdirAll(jeepDir, 0755)
	os.WriteFile(filepath.Join(jeepDir, "config.json"), []byte(`{
		"project": "my-project",
		"region": "us-central1",
		"models": {"text": "gemini-2.5-pro"},
		"quiet": true
	}`), 0644)

	cfg := Load()
	if cfg.Project != "my-project" {
		t.Errorf("project = %q", cfg.Project)
	}
	if cfg.Region != "us-central1" {
		t.Errorf("region = %q", cfg.Region)
	}
	if cfg.Models["text"] != "gemini-2.5-pro" {
		t.Errorf("models[text] = %q", cfg.Models["text"])
	}
	if !cfg.Quiet {
		t.Error("quiet should be true")
	}
}

func TestLoad_InvalidJSON(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)

	jeepDir := filepath.Join(dir, "jeep")
	os.MkdirAll(jeepDir, 0755)
	os.WriteFile(filepath.Join(jeepDir, "config.json"), []byte(`{invalid`), 0644)

	cfg := Load()
	if cfg.Project != "" {
		t.Errorf("expected empty config on invalid JSON, got project=%q", cfg.Project)
	}
}

func TestResolveModel_FromConfig(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)
	t.Setenv("JEEP_MODEL_TEXT", "")

	jeepDir := filepath.Join(dir, "jeep")
	os.MkdirAll(jeepDir, 0755)
	os.WriteFile(filepath.Join(jeepDir, "config.json"), []byte(`{"models": {"text": "custom-model"}}`), 0644)

	got := ResolveModel("TEXT")
	if got != "custom-model" {
		t.Errorf("ResolveModel(TEXT) = %q, want custom-model", got)
	}
}

func TestResolveModelWithConfig(t *testing.T) {
	t.Setenv("JEEP_MODEL_TEXT", "")
	cfg := &Config{Models: map[string]string{"text": "cfg-model"}}
	got := ResolveModelWithConfig(cfg, "TEXT")
	if got != "cfg-model" {
		t.Errorf("ResolveModelWithConfig(TEXT) = %q, want cfg-model", got)
	}
}

func TestResolveModelWithConfig_NilConfig(t *testing.T) {
	t.Setenv("JEEP_MODEL_TEXT", "")
	got := ResolveModelWithConfig(nil, "TEXT")
	if got != "gemini-3.5-flash" {
		t.Errorf("ResolveModelWithConfig(nil, TEXT) = %q, want gemini-3.5-flash", got)
	}
}

func TestResolveModel_EnvOverridesConfig(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)
	t.Setenv("JEEP_MODEL_TEXT", "env-model")

	jeepDir := filepath.Join(dir, "jeep")
	os.MkdirAll(jeepDir, 0755)
	os.WriteFile(filepath.Join(jeepDir, "config.json"), []byte(`{"models": {"text": "config-model"}}`), 0644)

	got := ResolveModel("TEXT")
	if got != "env-model" {
		t.Errorf("ResolveModel(TEXT) = %q, want env-model", got)
	}
}
