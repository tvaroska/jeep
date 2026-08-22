package mapreduce

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/tvaroska/jeep/internal/gemini"
	"github.com/tvaroska/jeep/internal/usage"
	"google.golang.org/genai"
)

// fakeGenerator implements the local generator interface, returning canned
// *gemini.GenerateResult for tests.
type fakeGenerator struct {
	result *gemini.GenerateResult
	err    error
}

func (f *fakeGenerator) Generate(ctx context.Context, model, system string, contents []*genai.Content, search bool, schema *genai.Schema, opts *gemini.GenerateOptions) (*gemini.GenerateResult, error) {
	if f.err != nil {
		return nil, f.err
	}
	return f.result, nil
}

// TestStep_UnmarshalsJSON verifies that Step unmarshals the JSON response into
// the target struct.
func TestStep_UnmarshalsJSON(t *testing.T) {
	fake := &fakeGenerator{
		result: &gemini.GenerateResult{Text: `{"summary":"hello"}`},
	}

	type response struct {
		Summary string `json:"summary"`
	}

	result, err := Step[response](
		context.Background(),
		fake,
		"gemini-2.0-flash-001",
		"system prompt",
		"user text",
		&genai.Schema{},
		nil,
		nil,
	)
	if err != nil {
		t.Fatalf("Step() error = %v", err)
	}
	if result.Summary != "hello" {
		t.Errorf("Step().Summary = %q, want 'hello'", result.Summary)
	}
}

// TestStep_ForwardsError verifies that Step propagates generator errors.
func TestStep_ForwardsError(t *testing.T) {
	fakeErr := errors.New("fake generate error")
	fake := &fakeGenerator{
		err: fakeErr,
	}

	type response struct {
		Summary string `json:"summary"`
	}

	_, err := Step[response](
		context.Background(),
		fake,
		"gemini-2.0-flash-001",
		"system prompt",
		"user text",
		&genai.Schema{},
		nil,
		nil,
	)
	if !errors.Is(err, fakeErr) {
		t.Errorf("Step() error = %v, want %v", err, fakeErr)
	}
}

// TestStep_BadJSON_Errors verifies that Step returns a parse error when the
// response is not valid JSON.
func TestStep_BadJSON_Errors(t *testing.T) {
	fake := &fakeGenerator{
		result: &gemini.GenerateResult{Text: `not json`},
	}

	type response struct {
		Summary string `json:"summary"`
	}

	_, err := Step[response](
		context.Background(),
		fake,
		"gemini-2.0-flash-001",
		"system prompt",
		"user text",
		&genai.Schema{},
		nil,
		nil,
	)
	if err == nil {
		t.Fatal("Step() expected parse error, got nil")
	}
	if !strings.Contains(err.Error(), "parse step response") {
		t.Errorf("Step() error = %v, want parse error", err)
	}
}

// TestRun_WithGeminiStep is an optional integration test that wires Map/Reduce
// closures calling Step against a fake, proving the two files compose.
func TestRun_WithGeminiStep(t *testing.T) {
	fake := &fakeGenerator{
		result: &gemini.GenerateResult{Text: `{"value":10}`},
	}

	type partial struct {
		Value int `json:"value"`
	}
	type result struct {
		Total int `json:"total"`
	}

	spec := Spec[int, partial, result]{
		Elements: []int{1, 2, 3},
		Map: func(ctx context.Context, i int, e int) (partial, error) {
			// In a real scenario, this would call Step with a prompt containing e.
			// Here, we just call Step to prove it composes.
			return Step[partial](
				ctx,
				fake,
				"gemini-2.0-flash-001",
				"map system",
				"user text",
				&genai.Schema{},
				nil,
				nil,
			)
		},
		Reduce: func(ctx context.Context, partials []partial) (result, error) {
			sum := 0
			for _, p := range partials {
				sum += p.Value
			}
			return result{Total: sum}, nil
		},
	}

	res, err := Run(context.Background(), spec)
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	// Each of the 3 Map calls gets {"value":10} from the fake, so Total = 30.
	if res.Total != 30 {
		t.Errorf("Run() Total = %d, want 30", res.Total)
	}
}

// TestStep_RecordsUsage verifies that Step records usage when a tracker is provided.
func TestStep_RecordsUsage(t *testing.T) {
	fake := &fakeGenerator{
		result: &gemini.GenerateResult{
			Text:         `{"summary":"x"}`,
			PromptTokens: 10,
			OutputTokens: 5,
		},
	}

	type response struct {
		Summary string `json:"summary"`
	}

	tracker := usage.New()
	_, err := Step[response](
		context.Background(),
		fake,
		"gemini-2.0-flash-001",
		"system prompt",
		"user text",
		&genai.Schema{},
		nil,
		tracker,
	)
	if err != nil {
		t.Fatalf("Step() error = %v", err)
	}

	report := tracker.Report()
	if report.TotalTokens != 15 {
		t.Errorf("tracker.Report().TotalTokens = %d, want 15", report.TotalTokens)
	}
	if report.PromptTokens != 10 {
		t.Errorf("tracker.Report().PromptTokens = %d, want 10", report.PromptTokens)
	}
	if report.OutputTokens != 5 {
		t.Errorf("tracker.Report().OutputTokens = %d, want 5", report.OutputTokens)
	}
	if report.Calls != 1 {
		t.Errorf("tracker.Report().Calls = %d, want 1", report.Calls)
	}
}
