package cli

import (
	"bytes"
	"errors"
	"strings"
	"testing"
	"time"
)

func TestCommonFlagsRegisterDefaults(t *testing.T) {
	var c CommonFlags
	fs := NewFlagSet("test", &bytes.Buffer{})
	c.Register(fs, 7*time.Minute)
	if err := fs.Parse(nil); err != nil {
		t.Fatalf("parse: %v", err)
	}
	if c.Region != "global" {
		t.Errorf("Region = %q, want global", c.Region)
	}
	if c.Format != "text" {
		t.Errorf("Format = %q, want text", c.Format)
	}
	if c.Timeout != 7*time.Minute {
		t.Errorf("Timeout = %v, want 7m", c.Timeout)
	}
	if c.Quiet || c.DryRun || c.Version || c.Retries != 0 || c.Project != "" {
		t.Errorf("unexpected non-zero default: %+v", c)
	}
}

func TestCommonFlagsRegisterParsesValues(t *testing.T) {
	var c CommonFlags
	fs := NewFlagSet("test", &bytes.Buffer{})
	c.Register(fs, time.Minute)
	args := []string{"--project", "p", "--region", "us", "--format", "json", "-q", "--dry-run", "--retry", "3", "--timeout", "2m"}
	if err := fs.Parse(args); err != nil {
		t.Fatalf("parse: %v", err)
	}
	if c.Project != "p" || c.Region != "us" || c.Format != "json" || !c.Quiet || !c.DryRun || c.Retries != 3 || c.Timeout != 2*time.Minute {
		t.Errorf("parsed flags mismatch: %+v", c)
	}
}

func TestParseVersion(t *testing.T) {
	var c CommonFlags
	fs := NewFlagSet("jeep", &bytes.Buffer{})
	c.Register(fs, time.Minute)
	var stdout bytes.Buffer
	done, err := Parse(fs, []string{"--version"}, "jeep", &stdout, &c)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if !done {
		t.Fatal("done = false, want true for --version")
	}
	if !strings.HasPrefix(stdout.String(), "jeep ") {
		t.Errorf("version output = %q, want prefix %q", stdout.String(), "jeep ")
	}
}

func TestParseNormal(t *testing.T) {
	var c CommonFlags
	fs := NewFlagSet("jeep", &bytes.Buffer{})
	c.Register(fs, time.Minute)
	done, err := Parse(fs, []string{"--region", "eu"}, "jeep", &bytes.Buffer{}, &c)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if done {
		t.Fatal("done = true, want false")
	}
	if c.Region != "eu" {
		t.Errorf("Region = %q, want eu", c.Region)
	}
}

func TestParseError(t *testing.T) {
	var c CommonFlags
	fs := NewFlagSet("jeep", &bytes.Buffer{})
	c.Register(fs, time.Minute)
	done, err := Parse(fs, []string{"--nonexistent-flag"}, "jeep", &bytes.Buffer{}, &c)
	if err == nil {
		t.Fatal("expected error for unknown flag")
	}
	if done {
		t.Fatal("done = true on error, want false")
	}
	var exitErr *ExitError
	if !errors.As(err, &exitErr) || exitErr.Code != ExitUsage {
		t.Errorf("want ExitUsage error, got %v", err)
	}
}
