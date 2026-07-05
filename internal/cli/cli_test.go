package cli

import (
	"testing"
)

func TestResolvePrompt_FromArgs(t *testing.T) {
	got, err := ResolvePrompt([]string{"hello", "world"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "hello world" {
		t.Errorf("got %q, want %q", got, "hello world")
	}
}

func TestResolvePrompt_SingleArg(t *testing.T) {
	got, err := ResolvePrompt([]string{"hello"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "hello" {
		t.Errorf("got %q, want %q", got, "hello")
	}
}

func TestResolvePrompt_NoArgs(t *testing.T) {
	_, err := ResolvePrompt(nil)
	if err == nil {
		t.Fatal("expected error when no args and no stdin")
	}
}
