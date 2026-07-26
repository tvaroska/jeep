package gemini

import (
	"errors"
	"testing"

	"google.golang.org/genai"
)

func TestIsRetryable(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want bool
	}{
		{"429", genai.APIError{Code: 429}, true},
		{"500", genai.APIError{Code: 500}, true},
		{"503", genai.APIError{Code: 503}, true},
		{"400 not retryable", genai.APIError{Code: 400}, false},
		{"non-api error", errors.New("boom"), false},
	}
	for _, tt := range tests {
		if got := isRetryable(tt.err); got != tt.want {
			t.Errorf("%s: isRetryable = %v, want %v", tt.name, got, tt.want)
		}
	}
}

func TestResolveRedirect_Passthrough(t *testing.T) {
	// Non-matching host is returned unchanged without any network call.
	in := "https://example.com/page"
	if got := ResolveRedirect(in); got != in {
		t.Errorf("ResolveRedirect(%q) = %q, want unchanged", in, got)
	}
	// Unparseable URL is also returned unchanged.
	bad := "://not a url"
	if got := ResolveRedirect(bad); got != bad {
		t.Errorf("ResolveRedirect(%q) = %q, want unchanged", bad, got)
	}
}
