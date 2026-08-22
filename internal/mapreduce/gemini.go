package mapreduce

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/tvaroska/jeep/internal/gemini"
	"google.golang.org/genai"
)

// generator is the minimal subset of *gemini.Client this package needs. It is a
// LOCAL interface (gemini.Client.models is unexported, so a fake cannot be injected
// into a real *gemini.Client). *gemini.Client satisfies it structurally; tests use
// a fake. Mirrors internal/describe. Do NOT modify internal/gemini.
type generator interface {
	Generate(ctx context.Context, model, system string, contents []*genai.Content, search bool, schema *genai.Schema, opts *gemini.GenerateOptions) (*gemini.GenerateResult, error)
}

// Step calls gemini.Generate with a structured-JSON schema and unmarshals the
// response into T. It is the building block for Map/Reduce closures: the caller
// supplies the system+user prompt and a *genai.Schema whose shape matches T.
// Retries are driven by opts.Retries (the gemini layer); this function adds none.
func Step[T any](ctx context.Context, c generator, model, system, userText string, schema *genai.Schema, opts *gemini.GenerateOptions) (T, error) {
	var zero T

	// Build user content.
	contents := []*genai.Content{
		genai.NewContentFromParts(
			[]*genai.Part{genai.NewPartFromText(userText)},
			genai.RoleUser,
		),
	}

	// Call gemini.Generate with structured JSON.
	res, err := c.Generate(ctx, model, system, contents, false, schema, opts)
	if err != nil {
		return zero, err
	}

	// Unmarshal the JSON response.
	var out T
	if err := json.Unmarshal([]byte(res.Text), &out); err != nil {
		return zero, fmt.Errorf("mapreduce: parse step response: %w", err)
	}

	return out, nil
}
