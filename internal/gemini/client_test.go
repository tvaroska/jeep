package gemini

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"google.golang.org/genai"
)

// fakeGenerator is a test double for the contentGenerator seam. It records the
// contents/config of the most recent call, counts calls, and returns scripted
// responses: it serves errs[i] (or, once errs is exhausted, resp) for call i.
type fakeGenerator struct {
	calls    int
	contents []*genai.Content
	config   *genai.GenerateContentConfig
	errs     []error
	resp     *genai.GenerateContentResponse
}

func (f *fakeGenerator) GenerateContent(ctx context.Context, model string, contents []*genai.Content, config *genai.GenerateContentConfig) (*genai.GenerateContentResponse, error) {
	f.contents = contents
	f.config = config
	i := f.calls
	f.calls++
	if i < len(f.errs) {
		return nil, f.errs[i]
	}
	return f.resp, nil
}

// audioResponse builds a minimal successful TTS response carrying PCM bytes.
func audioResponse() *genai.GenerateContentResponse {
	return &genai.GenerateContentResponse{
		ModelVersion: "fake-tts",
		Candidates: []*genai.Candidate{{
			Content: &genai.Content{
				Role: "model",
				Parts: []*genai.Part{{
					InlineData: &genai.Blob{
						MIMEType: "audio/L16",
						Data:     []byte{0x01, 0x02, 0x03, 0x04},
					},
				}},
			},
		}},
	}
}

// firstText returns the text of the first part of the first content, or "".
func firstText(contents []*genai.Content) string {
	if len(contents) == 0 || contents[0] == nil || len(contents[0].Parts) == 0 {
		return ""
	}
	return contents[0].Parts[0].Text
}

func TestGenerateSpeechStylePrefixReachesRequest(t *testing.T) {
	fake := &fakeGenerator{resp: audioResponse()}
	c := &Client{models: fake}

	style := "Read slowly and warmly"
	prompt := "Hello world"
	if _, err := c.GenerateSpeech(context.Background(), "m", "Kore", style, prompt, 0); err != nil {
		t.Fatalf("GenerateSpeech: %v", err)
	}

	got := firstText(fake.contents)
	if !strings.Contains(got, style) {
		t.Errorf("request text %q does not contain style %q", got, style)
	}
	if !strings.Contains(got, prompt) {
		t.Errorf("request text %q does not contain prompt %q", got, prompt)
	}
}

func TestGenerateSpeechEmptyStyleLeavesPromptUnchanged(t *testing.T) {
	fake := &fakeGenerator{resp: audioResponse()}
	c := &Client{models: fake}

	prompt := "Hello world"
	if _, err := c.GenerateSpeech(context.Background(), "m", "Kore", "", prompt, 0); err != nil {
		t.Fatalf("GenerateSpeech: %v", err)
	}

	if got := firstText(fake.contents); got != prompt {
		t.Errorf("request text = %q, want exactly %q (no prefix)", got, prompt)
	}
}

func TestGenerateSpeechInvalidInputNotRetried(t *testing.T) {
	fake := &fakeGenerator{errs: []error{genai.APIError{Code: 400, Message: "invalid argument"}}}
	c := &Client{models: fake}

	_, err := c.GenerateSpeech(context.Background(), "m", "Kore", "", "too dense", 3)
	if err == nil {
		t.Fatal("expected an error for a 400 response")
	}
	if !IsInvalidInput(err) {
		t.Errorf("IsInvalidInput(%v) = false, want true", err)
	}
	if fake.calls != 1 {
		t.Errorf("fake called %d times, want exactly 1 (400 must not retry)", fake.calls)
	}
}

func TestGenerateSpeechRetriesOn503(t *testing.T) {
	fake := &fakeGenerator{
		errs: []error{genai.APIError{Code: 503, Message: "unavailable"}},
		resp: audioResponse(),
	}
	c := &Client{models: fake}

	res, err := c.GenerateSpeech(context.Background(), "m", "Kore", "", "hi", 3)
	if err != nil {
		t.Fatalf("GenerateSpeech after 503 retry: %v", err)
	}
	if res == nil || len(res.PCMData) == 0 {
		t.Fatal("expected non-empty PCM after successful retry")
	}
	if fake.calls != 2 {
		t.Errorf("fake called %d times, want 2 (one 503 then success)", fake.calls)
	}
}

func TestIsInvalidInput(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want bool
	}{
		{"nil", nil, false},
		{"400", genai.APIError{Code: 400}, true},
		{"wrapped 400", fmt.Errorf("generating speech: %w", genai.APIError{Code: 400}), true},
		{"429", genai.APIError{Code: 429}, false},
		{"503", genai.APIError{Code: 503}, false},
		{"plain error", errors.New("boom"), false},
	}
	for _, tt := range tests {
		if got := IsInvalidInput(tt.err); got != tt.want {
			t.Errorf("%s: IsInvalidInput = %v, want %v", tt.name, got, tt.want)
		}
	}
}
