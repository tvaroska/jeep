package cli

import (
	"errors"
	"testing"
)

func TestExitError_ErrorAndUnwrap(t *testing.T) {
	base := errors.New("boom")
	e := &ExitError{Code: ExitAPI, Err: base}

	if e.Error() != "boom" {
		t.Errorf("Error() = %q, want boom", e.Error())
	}
	if !errors.Is(e, base) {
		t.Error("errors.Is should unwrap to the base error")
	}
}

func TestExitf(t *testing.T) {
	err := Exitf(ExitUsage, "bad %s", "input")
	var exitErr *ExitError
	if !errors.As(err, &exitErr) {
		t.Fatal("Exitf should return an *ExitError")
	}
	if exitErr.Code != ExitUsage {
		t.Errorf("code = %d, want %d", exitErr.Code, ExitUsage)
	}
	if exitErr.Error() != "bad input" {
		t.Errorf("Error() = %q, want 'bad input'", exitErr.Error())
	}
}
