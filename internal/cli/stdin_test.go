package cli

import (
	"os"
	"testing"
)

// withStdin replaces os.Stdin with a pipe carrying content for the duration of fn.
func withStdin(t *testing.T, content string, fn func()) {
	t.Helper()
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := w.WriteString(content); err != nil {
		t.Fatal(err)
	}
	w.Close()
	orig := os.Stdin
	os.Stdin = r
	defer func() { os.Stdin = orig }()
	fn()
}

func TestResolvePrompt_FromStdin(t *testing.T) {
	withStdin(t, "  piped prompt \n", func() {
		got, err := ResolvePrompt(nil)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if got != "piped prompt" {
			t.Errorf("got %q, want %q", got, "piped prompt")
		}
	})
}

func TestResolvePrompt_EmptyStdin(t *testing.T) {
	withStdin(t, "   \n", func() {
		if _, err := ResolvePrompt(nil); err == nil {
			t.Fatal("expected error when stdin is only whitespace and no args")
		}
	})
}
