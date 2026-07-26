package version

import "testing"

func TestString_Explicit(t *testing.T) {
	orig := Version
	defer func() { Version = orig }()

	Version = "v1.2.3"
	if got := String(); got != "v1.2.3" {
		t.Errorf("String() = %q, want v1.2.3", got)
	}
}

func TestString_Fallback(t *testing.T) {
	orig := Version
	defer func() { Version = orig }()

	Version = ""
	// Under `go test` the main module version is typically empty or "(devel)",
	// so this exercises the build-info fallback and its default.
	if got := String(); got == "" {
		t.Error("String() returned empty; want a non-empty fallback")
	}
}
