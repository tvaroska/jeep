// Package describe uses Gemini multimodal to generate spoken descriptions of
// images and to adapt text for listening. Image description is a GCP boundary
// (Vertex AI Gemini), so it is built as a seam with a GCP adapter
// (GeminiDescriber) and a local, offline adapter (StubDescriber).
package describe

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"

	"google.golang.org/genai"

	"github.com/tvaroska/jeep/internal/gemini"
	"github.com/tvaroska/jeep/internal/usage"
)

// Defaults for the GCP adapter, resolved lazily inside Describe.
const (
	defaultMaxOutputTokens = 400
	defaultTemperature     = 0.2
	defaultRetries         = 4
	defaultMimeType        = "image/png"
)

// defaultSystemPrompt steers Gemini toward concise, ear-friendly descriptions.
const defaultSystemPrompt = "You are a concise audiobook narrator describing figures for a listener who cannot see them. " +
	"Speak plainly, never mention images, markdown, or formatting, and keep to a few sentences."

// defaultInstruction is the per-call user instruction accompanying the image.
const defaultInstruction = "Describe this figure for an audiobook listener in 1-3 spoken sentences. " +
	"State what kind of figure it is and its key takeaway. Do not mention that it is an image or use markdown."

// Describer turns one image (bytes + mime) into a concise, spoken-style
// description. context is optional surrounding text/caption to ground the
// description ("" if none). Implementations must be safe for concurrent use.
type Describer interface {
	Describe(ctx context.Context, img []byte, mimeType, context string) (string, error)
}

// generator is the minimal subset of *gemini.Client the GCP adapter needs.
// *gemini.Client satisfies it structurally; tests supply a fake.
type generator interface {
	Generate(ctx context.Context, model, system string, contents []*genai.Content, search bool, schema *genai.Schema, opts *gemini.GenerateOptions) (*gemini.GenerateResult, error)
}

// GeminiDescriber is the GCP adapter: it calls Vertex AI Gemini multimodal to
// turn one image into a spoken-style description, retrying transient HTTP
// failures via gemini.Generate.
//
// Each Describe call reads only the receiver's fields and builds a fresh request,
// so a single *GeminiDescriber is safe to call from many goroutines.
type GeminiDescriber struct {
	Client          generator      // Gemini client (or a fake in tests)
	Model           string         // e.g. "gemini-2.5-flash"
	SystemPrompt    string         // "" => defaultSystemPrompt
	MaxOutputTokens int32          // <=0 => defaultMaxOutputTokens
	Retries         int            // <=0 => defaultRetries
	Log             *slog.Logger   // optional; nil-safe (uses slog.Default() when nil)
	Usage           *usage.Tracker // optional; nil => no cost tracking
}

// logger returns g.Log if non-nil, else slog.Default().
func (g *GeminiDescriber) logger() *slog.Logger {
	if g.Log != nil {
		return g.Log
	}
	return slog.Default()
}

// retries returns g.Retries if positive, else defaultRetries.
func (g *GeminiDescriber) retries() int {
	if g.Retries > 0 {
		return g.Retries
	}
	return defaultRetries
}

// Describe sends one image to Gemini multimodal and returns a concise, spoken
// description. Empty image bytes or an empty mime type fail fatally without an
// API call.
func (g *GeminiDescriber) Describe(ctx context.Context, img []byte, mimeType, surrounding string) (string, error) {
	if len(img) == 0 {
		return "", fmt.Errorf("describe: empty image bytes")
	}
	if mimeType == "" {
		return "", fmt.Errorf("describe: empty mime type")
	}

	instruction := defaultInstruction
	if surrounding != "" {
		instruction = "Surrounding text: " + surrounding + "\n\n" + instruction
	}

	textPart := genai.NewPartFromText(instruction)
	imgPart := genai.NewPartFromBytes(img, mimeType)
	contents := []*genai.Content{
		genai.NewContentFromParts([]*genai.Part{textPart, imgPart}, genai.RoleUser),
	}

	sys := g.SystemPrompt
	if sys == "" {
		sys = defaultSystemPrompt
	}
	maxTokens := g.MaxOutputTokens
	if maxTokens <= 0 {
		maxTokens = defaultMaxOutputTokens
	}
	temp := defaultTemperature
	schema := &genai.Schema{
		Type: genai.TypeObject,
		Properties: map[string]*genai.Schema{
			"description": {Type: genai.TypeString},
		},
		Required: []string{"description"},
	}
	opts := &gemini.GenerateOptions{
		Temperature: &temp,
		MaxTokens:   maxTokens,
		Retries:     g.retries(),
	}

	resp, err := g.Client.Generate(ctx, g.Model, sys, contents, false, schema, opts)
	if err != nil {
		return "", err
	}

	// Parse the JSON response.
	var result struct {
		Description string `json:"description"`
	}
	if err := json.Unmarshal([]byte(resp.Text), &result); err != nil {
		return "", fmt.Errorf("describe: failed to parse JSON response: %w", err)
	}

	// Record token usage if tracking is enabled.
	// GenerateResult has no TotalTokens field; compute prompt + output.
	g.Usage.Record(g.Model, int64(resp.PromptTokens), int64(resp.OutputTokens), int64(resp.PromptTokens+resp.OutputTokens))

	desc := strings.TrimSpace(result.Description)
	if desc == "" {
		return "", fmt.Errorf("describe: empty response from model")
	}
	return desc, nil
}

// StubDescriber is the local adapter: a deterministic, offline stand-in for the
// GCP adapter, used for pipeline wiring and unit tests. Its output is a
// placeholder, not a real description.
type StubDescriber struct{}

// Describe returns a deterministic, non-empty placeholder derived from the
// inputs. It honors context cancellation.
func (StubDescriber) Describe(ctx context.Context, img []byte, mimeType, _ string) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	if mimeType == "" {
		mimeType = defaultMimeType
	}
	return fmt.Sprintf("[image description: %s, %d bytes]", mimeType, len(img)), nil
}

// compile-time checks that both adapters satisfy the seam.
var (
	_ Describer = (*GeminiDescriber)(nil)
	_ Describer = StubDescriber{}
)
