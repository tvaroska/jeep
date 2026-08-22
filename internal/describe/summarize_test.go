package describe

import (
	"context"
	"strings"
	"testing"

	"github.com/tvaroska/jeep/internal/gemini"
	"github.com/tvaroska/jeep/internal/usage"
	"google.golang.org/genai"
)

// TestParseMode verifies mode parsing (case-insensitive, valid/invalid).
func TestParseMode(t *testing.T) {
	tests := []struct {
		input   string
		want    Mode
		wantErr bool
	}{
		{"full", ModeFull, false},
		{"summary", ModeSummary, false},
		{"abstract", ModeAbstract, false},
		{"FULL", ModeFull, false},
		{"  Summary  ", ModeSummary, false},
		{"", "", true},
		{"bogus", "", true},
		{"abstrac", "", true},
	}

	for _, tt := range tests {
		got, err := ParseMode(tt.input)
		if tt.wantErr {
			if err == nil {
				t.Errorf("ParseMode(%q) expected error, got nil", tt.input)
			}
		} else {
			if err != nil {
				t.Errorf("ParseMode(%q) unexpected error: %v", tt.input, err)
			}
			if got != tt.want {
				t.Errorf("ParseMode(%q) = %q, want %q", tt.input, got, tt.want)
			}
		}
	}
}

// TestSummarizer_Prepare_Abstract verifies that multi-chunk input produces
// the reduce output (proves map-reduce ran).
func TestSummarizer_Prepare_Abstract(t *testing.T) {
	// Build a long input (>12000 chars) to force ≥2 chunks at 6000 maxInputChars.
	var b strings.Builder
	for i := 0; i < 250; i++ {
		b.WriteString("This is a paragraph of text that will be chunked. ")
	}
	input := b.String()

	fake := &fakeSummarizerGenerator{
		result: &gemini.GenerateResult{
			Text:         `{"summary":"short bit","narration":"final short narration"}`,
			PromptTokens: 10,
			OutputTokens: 5,
		},
	}

	s := &Summarizer{
		Client: fake,
		Model:  "gemini-2.0-flash-001",
		Mode:   ModeAbstract,
	}

	got, err := s.Prepare(context.Background(), input)
	if err != nil {
		t.Fatalf("Prepare() error = %v", err)
	}
	if got != "final short narration" {
		t.Errorf("Prepare() = %q, want %q", got, "final short narration")
	}

	// Optionally assert the fake was called ≥3 times (≥2 maps + 1 reduce).
	if fake.calls < 3 {
		t.Errorf("fake.calls = %d, want ≥3 (≥2 maps + 1 reduce)", fake.calls)
	}
}

// TestSummarizer_Prepare_Empty verifies empty input returns "", nil with no API call.
func TestSummarizer_Prepare_Empty(t *testing.T) {
	fake := &fakeSummarizerGenerator{}

	s := &Summarizer{
		Client: fake,
		Model:  "gemini-2.0-flash-001",
		Mode:   ModeAbstract,
	}

	tests := []string{"", "   ", "\n\n"}
	for _, input := range tests {
		got, err := s.Prepare(context.Background(), input)
		if err != nil {
			t.Errorf("Prepare(%q) error = %v, want nil", input, err)
		}
		if got != "" {
			t.Errorf("Prepare(%q) = %q, want empty", input, got)
		}
		if fake.calls != 0 {
			t.Errorf("Prepare(%q) called fake %d times, want 0", input, fake.calls)
		}
	}
}

// TestSummarizer_Prepare_Usage verifies usage tracking accumulates across N+1 calls.
func TestSummarizer_Prepare_Usage(t *testing.T) {
	// Build a long input to force ≥2 chunks.
	var b strings.Builder
	for i := 0; i < 250; i++ {
		b.WriteString("This is a paragraph of text that will be chunked. ")
	}
	input := b.String()

	fake := &fakeSummarizerGenerator{
		result: &gemini.GenerateResult{
			Text:         `{"summary":"s","narration":"n"}`,
			PromptTokens: 10,
			OutputTokens: 5,
		},
	}

	tracker := usage.New()
	s := &Summarizer{
		Client: fake,
		Model:  "gemini-2.0-flash-001",
		Mode:   ModeAbstract,
		Usage:  tracker,
	}

	_, err := s.Prepare(context.Background(), input)
	if err != nil {
		t.Fatalf("Prepare() error = %v", err)
	}

	report := tracker.Report()
	// Expect N chunks + 1 reduce, each contributing 15 tokens.
	expectedCalls := fake.calls
	expectedTotal := int64(expectedCalls) * 15
	if report.TotalTokens != expectedTotal {
		t.Errorf("tracker.TotalTokens = %d, want %d (calls=%d)", report.TotalTokens, expectedTotal, expectedCalls)
	}
	if report.PromptTokens != int64(expectedCalls)*10 {
		t.Errorf("tracker.PromptTokens = %d, want %d", report.PromptTokens, expectedCalls*10)
	}
	if report.OutputTokens != int64(expectedCalls)*5 {
		t.Errorf("tracker.OutputTokens = %d, want %d", report.OutputTokens, expectedCalls*5)
	}
}

// TestSummarizer_Prepare_Summary verifies summary mode also works.
func TestSummarizer_Prepare_Summary(t *testing.T) {
	// Build a long input to force ≥2 chunks.
	var b strings.Builder
	for i := 0; i < 250; i++ {
		b.WriteString("This is a paragraph of text that will be chunked. ")
	}
	input := b.String()

	fake := &fakeSummarizerGenerator{
		result: &gemini.GenerateResult{
			Text:         `{"summary":"short summary","narration":"final summary narration"}`,
			PromptTokens: 10,
			OutputTokens: 5,
		},
	}

	s := &Summarizer{
		Client: fake,
		Model:  "gemini-2.0-flash-001",
		Mode:   ModeSummary,
	}

	got, err := s.Prepare(context.Background(), input)
	if err != nil {
		t.Fatalf("Prepare() error = %v", err)
	}
	if got != "final summary narration" {
		t.Errorf("Prepare() = %q, want %q", got, "final summary narration")
	}
}

// fakeSummarizerGenerator implements the describe.generator interface for tests.
type fakeSummarizerGenerator struct {
	result *gemini.GenerateResult
	err    error
	calls  int
}

func (f *fakeSummarizerGenerator) Generate(ctx context.Context, model, system string, contents []*genai.Content, search bool, schema *genai.Schema, opts *gemini.GenerateOptions) (*gemini.GenerateResult, error) {
	f.calls++
	if f.err != nil {
		return nil, f.err
	}
	return f.result, nil
}
