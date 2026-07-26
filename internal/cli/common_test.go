package cli

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	flag "github.com/spf13/pflag"
)

func newFlagSet() (*flag.FlagSet, *string) {
	fs := flag.NewFlagSet("test", flag.ContinueOnError)
	region := fs.String("region", "global", "")
	return fs, region
}

func TestResolveCommon_FromConfigFile(t *testing.T) {
	dir := t.TempDir()
	cfgDir := filepath.Join(dir, "jeep")
	if err := os.MkdirAll(cfgDir, 0755); err != nil {
		t.Fatal(err)
	}
	os.WriteFile(filepath.Join(cfgDir, "config.json"),
		[]byte(`{"project":"cfg-proj","region":"us-central1","quiet":true}`), 0644)
	t.Setenv("XDG_CONFIG_HOME", dir)

	fs, region := newFlagSet()
	project := ""
	quiet := false

	cfg, err := ResolveCommon(fs, &project, region, &quiet)
	if err != nil {
		t.Fatalf("ResolveCommon: %v", err)
	}
	if project != "cfg-proj" {
		t.Errorf("project = %q, want cfg-proj", project)
	}
	if *region != "us-central1" {
		t.Errorf("region = %q, want us-central1", *region)
	}
	if !quiet {
		t.Error("quiet should be enabled from config")
	}
	if cfg == nil {
		t.Error("expected non-nil config")
	}
}

func TestResolveCommon_FlagsWinOverConfig(t *testing.T) {
	dir := t.TempDir()
	cfgDir := filepath.Join(dir, "jeep")
	os.MkdirAll(cfgDir, 0755)
	os.WriteFile(filepath.Join(cfgDir, "config.json"),
		[]byte(`{"project":"cfg-proj","region":"us-central1"}`), 0644)
	t.Setenv("XDG_CONFIG_HOME", dir)

	fs, region := newFlagSet()
	// Simulate an explicit --region flag and an explicit project value.
	fs.Parse([]string{"--region", "europe-west1"})
	project := "flag-proj"
	quiet := false

	if _, err := ResolveCommon(fs, &project, region, &quiet); err != nil {
		t.Fatalf("ResolveCommon: %v", err)
	}
	if project != "flag-proj" {
		t.Errorf("project = %q, want flag-proj (flag wins)", project)
	}
	if *region != "europe-west1" {
		t.Errorf("region = %q, want europe-west1 (flag wins)", *region)
	}
}

func TestResolveCommon_NoProject(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir()) // no config file
	t.Setenv("GOOGLE_CLOUD_PROJECT", "")
	t.Setenv("GCLOUD_PROJECT", "")
	// Ensure gcloud fallback can't find a project by breaking PATH lookup.
	t.Setenv("PATH", "")

	fs, region := newFlagSet()
	project := ""
	quiet := false

	_, err := ResolveCommon(fs, &project, region, &quiet)
	if err == nil {
		t.Fatal("expected error when no project can be resolved")
	}
	var exitErr *ExitError
	if !errors.As(err, &exitErr) || exitErr.Code != ExitConfig {
		t.Errorf("error = %v, want ExitConfig", err)
	}
}
