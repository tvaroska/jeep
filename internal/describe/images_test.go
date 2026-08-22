package describe

import (
	"context"
	"errors"
	"strings"
	"testing"

	"google.golang.org/genai"

	"github.com/tvaroska/jeep/internal/gemini"
	"github.com/tvaroska/jeep/internal/usage"
)

const (
	testModel = "gemini-2.5-flash"
	testMime  = "image/png"
)

var testImg = []byte{0x89, 0x50, 0x4e, 0x47} // arbitrary non-empty bytes

// fakeGenerator is a scripted generator for offline tests. It returns
// *gemini.GenerateResult with JSON text matching the schema.
type fakeGenerator struct {
	calls        int
	text         string              // JSON text of the result
	errs         []error             // errs[i] returned on call i; nil => success with text
	lastModel    string
	lastSystem   string
	lastContents []*genai.Content
	lastSchema   *genai.Schema
	lastOpts     *gemini.GenerateOptions
}

func (f *fakeGenerator) Generate(_ context.Context, model, system string, contents []*genai.Content, _ bool, schema *genai.Schema, opts *gemini.GenerateOptions) (*gemini.GenerateResult, error) {
	f.calls++
	f.lastModel = model
	f.lastSystem = system
	f.lastContents = contents
	f.lastSchema = schema
	f.lastOpts = opts
	var err error
	if f.calls-1 < len(f.errs) {
		err = f.errs[f.calls-1]
	}
	if err != nil {
		return nil, err
	}
	return &gemini.GenerateResult{
		Text:         f.text,
		PromptTokens: 100,
		OutputTokens: 40,
	}, nil
}

func newTestDescriber(fake generator) *GeminiDescriber {
	return &GeminiDescriber{
		Client:  fake,
		Model:   testModel,
		Retries: 2,
	}
}

func TestDescribe_SuccessRequestShape(t *testing.T) {
	fake := &fakeGenerator{text: `{"description":"A bar chart showing rising revenue."}`}
	g := newTestDescriber(fake)

	got, err := g.Describe(context.Background(), testImg, testMime, "")
	if err != nil {
		t.Fatalf("Describe returned error: %v", err)
	}
	if got != "A bar chart showing rising revenue." {
		t.Errorf("description = %q", got)
	}
	if fake.calls != 1 {
		t.Errorf("calls = %d, want 1", fake.calls)
	}
	if fake.lastModel != testModel {
		t.Errorf("model = %q, want %q", fake.lastModel, testModel)
	}
	if len(fake.lastContents) != 1 {
		t.Fatalf("contents len = %d, want 1", len(fake.lastContents))
	}
	parts := fake.lastContents[0].Parts
	if len(parts) != 2 {
		t.Fatalf("parts len = %d, want 2", len(parts))
	}
	var sawText, sawImg bool
	for _, p := range parts {
		if p.Text != "" {
			sawText = true
		}
		if p.InlineData != nil {
			sawImg = true
			if string(p.InlineData.Data) != string(testImg) {
				t.Errorf("inline data = %v, want %v", p.InlineData.Data, testImg)
			}
			if p.InlineData.MIMEType != testMime {
				t.Errorf("inline mime = %q, want %q", p.InlineData.MIMEType, testMime)
			}
		}
	}
	if !sawText {
		t.Error("no text part in request")
	}
	if !sawImg {
		t.Error("no image part in request")
	}
	if fake.lastSystem == "" {
		t.Error("system prompt not set")
	}
	if fake.lastSchema == nil {
		t.Error("schema not set")
	}
}

func TestDescribe_ContextPrepended(t *testing.T) {
	fake := &fakeGenerator{text: `{"description":"ok"}`}
	g := newTestDescriber(fake)

	if _, err := g.Describe(context.Background(), testImg, testMime, "Figure 3: revenue"); err != nil {
		t.Fatalf("Describe returned error: %v", err)
	}
	var textPart string
	for _, p := range fake.lastContents[0].Parts {
		if p.Text != "" {
			textPart = p.Text
		}
	}
	if !strings.Contains(textPart, "Figure 3: revenue") {
		t.Errorf("text part = %q, want it to include surrounding context", textPart)
	}
}

func TestDescribe_Success(t *testing.T) {
	fake := &fakeGenerator{text: `{"description":"ok"}`}
	g := newTestDescriber(fake)

	got, err := g.Describe(context.Background(), testImg, testMime, "")
	if err != nil {
		t.Fatalf("Describe returned error: %v", err)
	}
	if got != "ok" {
		t.Errorf("description = %q, want ok", got)
	}
	if fake.calls != 1 {
		t.Errorf("calls = %d, want 1", fake.calls)
	}
}

func TestDescribe_FatalNoRetry(t *testing.T) {
	fake := &fakeGenerator{errs: []error{genai.APIError{Code: 400, Message: "bad request"}}}
	g := newTestDescriber(fake)

	if _, err := g.Describe(context.Background(), testImg, testMime, ""); err == nil {
		t.Fatal("expected error, got nil")
	}
	if fake.calls != 1 {
		t.Errorf("calls = %d, want 1 (no retry on fatal)", fake.calls)
	}
}

func TestDescribe_EmptyResponse(t *testing.T) {
	fake := &fakeGenerator{text: `{"description":""}`}
	g := newTestDescriber(fake)

	if _, err := g.Describe(context.Background(), testImg, testMime, ""); err == nil {
		t.Fatal("expected error for empty response")
	}
}

func TestDescribe_EmptyImageGuard(t *testing.T) {
	fake := &fakeGenerator{text: `{"description":"x"}`}
	g := newTestDescriber(fake)

	if _, err := g.Describe(context.Background(), nil, testMime, ""); err == nil {
		t.Fatal("expected error for empty image bytes")
	}
	if fake.calls != 0 {
		t.Errorf("calls = %d, want 0 (no API call for empty image)", fake.calls)
	}
}

func TestDescribe_EmptyMimeGuard(t *testing.T) {
	fake := &fakeGenerator{text: `{"description":"x"}`}
	g := newTestDescriber(fake)

	if _, err := g.Describe(context.Background(), testImg, "", ""); err == nil {
		t.Fatal("expected error for empty mime type")
	}
	if fake.calls != 0 {
		t.Errorf("calls = %d, want 0 (no API call for empty mime)", fake.calls)
	}
}

func TestDescribe_ContextCancelled(t *testing.T) {
	fake := &fakeGenerator{
		errs: []error{context.Canceled},
	}
	g := newTestDescriber(fake)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := g.Describe(ctx, testImg, testMime, "")
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("error = %v, want context.Canceled", err)
	}
}

func TestStubDescriber(t *testing.T) {
	var s StubDescriber

	got1, err := s.Describe(context.Background(), testImg, testMime, "")
	if err != nil {
		t.Fatalf("Describe returned error: %v", err)
	}
	if got1 == "" {
		t.Fatal("stub returned empty string")
	}
	got2, err := s.Describe(context.Background(), testImg, testMime, "")
	if err != nil {
		t.Fatalf("Describe returned error: %v", err)
	}
	if got1 != got2 {
		t.Errorf("stub not deterministic: %q != %q", got1, got2)
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := s.Describe(ctx, testImg, testMime, ""); !errors.Is(err, context.Canceled) {
		t.Errorf("cancelled ctx: err = %v, want context.Canceled", err)
	}
}

func TestDescribe_RecordsUsage(t *testing.T) {
	fake := &fakeGenerator{text: `{"description":"A bar chart."}`}
	tracker := usage.New()
	g := newTestDescriber(fake)
	g.Usage = tracker

	_, err := g.Describe(context.Background(), testImg, testMime, "")
	if err != nil {
		t.Fatalf("Describe returned error: %v", err)
	}

	rep := tracker.Report()
	if rep.PromptTokens != 100 {
		t.Errorf("PromptTokens = %d, want 100", rep.PromptTokens)
	}
	if rep.OutputTokens != 40 {
		t.Errorf("OutputTokens = %d, want 40", rep.OutputTokens)
	}
	if rep.TotalTokens != 140 {
		t.Errorf("TotalTokens = %d, want 140", rep.TotalTokens)
	}
	if rep.Calls != 1 {
		t.Errorf("Calls = %d, want 1", rep.Calls)
	}
}
