package describe

import (
	"context"
	"fmt"
	"log/slog"
	"strings"

	"github.com/tvaroska/jeep/internal/gemini"
	"github.com/tvaroska/jeep/internal/mapreduce"
	"github.com/tvaroska/jeep/internal/usage"
	"google.golang.org/genai"
)

// Mode identifies the narration style.
type Mode string

const (
	ModeFull     Mode = "full"
	ModeSummary  Mode = "summary"
	ModeAbstract Mode = "abstract"
)

// ParseMode validates a --mode value (case-insensitive, trimmed).
func ParseMode(s string) (Mode, error) {
	switch Mode(strings.ToLower(strings.TrimSpace(s))) {
	case ModeFull:
		return ModeFull, nil
	case ModeSummary:
		return ModeSummary, nil
	case ModeAbstract:
		return ModeAbstract, nil
	default:
		return "", fmt.Errorf("invalid mode %q (want full, summary, or abstract)", s)
	}
}

// Summarizer is a short-form Preparer: it condenses each chapter's text via
// map-reduce (chunk → map each chunk to a partial summary → reduce partials
// into one listening-ready narration). It implements describe.Preparer and
// mirrors GeminiPreparer field-for-field, swapping full narration for
// summary/abstract narration without changing the pipeline.
type Summarizer struct {
	Client        generator      // reuse the generator interface (same as GeminiPreparer)
	Model         string         // TEXT model
	Mode          Mode           // ModeSummary or ModeAbstract (NOT ModeFull)
	MaxInputChars int            // <=0 => defaultPrepareMaxInputChars
	Concurrency   int            // <=0 => defaultPrepareConcurrency
	Retries       int            // <=0 => defaultRetries
	Log           *slog.Logger   // optional; nil-safe
	Usage         *usage.Tracker // optional; nil-safe
}

// logger returns s.Log if non-nil, else slog.Default().
func (s *Summarizer) logger() *slog.Logger {
	if s.Log != nil {
		return s.Log
	}
	return slog.Default()
}

// maxInputChars returns s.MaxInputChars if positive, else defaultPrepareMaxInputChars.
func (s *Summarizer) maxInputChars() int {
	if s.MaxInputChars > 0 {
		return s.MaxInputChars
	}
	return defaultPrepareMaxInputChars
}

// concurrency returns s.Concurrency if positive, else defaultPrepareConcurrency.
func (s *Summarizer) concurrency() int {
	if s.Concurrency > 0 {
		return s.Concurrency
	}
	return defaultPrepareConcurrency
}

// retries returns s.Retries if positive, else defaultRetries.
func (s *Summarizer) retries() int {
	if s.Retries > 0 {
		return s.Retries
	}
	return defaultRetries
}

// opts returns GenerateOptions for map/reduce Step calls.
func (s *Summarizer) opts() *gemini.GenerateOptions {
	temp := defaultTemperature
	return &gemini.GenerateOptions{
		Temperature: &temp,
		Retries:     s.retries(),
	}
}

// Prepare condenses text into a short-form audiobook narration via map-reduce.
// Empty input is a no-op (returns "", nil without an API call).
func (s *Summarizer) Prepare(ctx context.Context, text string) (string, error) {
	text = strings.TrimSpace(text)
	if text == "" {
		return "", nil
	}

	chunks := chunkForPrepare(text, s.maxInputChars())

	// Build map-reduce spec.
	spec := mapreduce.Spec[string, chunkSummary, finalNarration]{
		Elements:    chunks,
		Concurrency: s.concurrency(),
		Map: func(ctx context.Context, i int, chunk string) (chunkSummary, error) {
			return mapreduce.Step[chunkSummary](
				ctx,
				s.Client,
				s.Model,
				mapSystem(s.Mode),
				chunk,
				summarySchema(),
				s.opts(),
				s.Usage,
			)
		},
		Reduce: func(ctx context.Context, partials []chunkSummary) (finalNarration, error) {
			// Belt-and-suspenders: empty Elements calls Reduce(nil), but empty text
			// already returned above, so this branch should never run.
			if len(partials) == 0 {
				return finalNarration{}, nil
			}

			// Join partials into one string.
			var parts []string
			for _, p := range partials {
				if s := strings.TrimSpace(p.Summary); s != "" {
					parts = append(parts, s)
				}
			}
			joined := strings.Join(parts, "\n\n")

			return mapreduce.Step[finalNarration](
				ctx,
				s.Client,
				s.Model,
				reduceSystem(s.Mode),
				joined,
				narrationSchema(),
				s.opts(),
				s.Usage,
			)
		},
	}

	res, err := mapreduce.Run(ctx, spec)
	if err != nil {
		return "", fmt.Errorf("summarize (%s): %w", s.Mode, err)
	}

	out := strings.TrimSpace(res.Narration)
	if out == "" {
		return "", fmt.Errorf("summarize: empty response from model")
	}
	return out, nil
}

// chunkSummary is the Map output: one partial summary.
type chunkSummary struct {
	Summary string `json:"summary"`
}

// finalNarration is the Reduce output: the condensed listening-ready narration.
type finalNarration struct {
	Narration string `json:"narration"`
}

// summarySchema is the JSON schema for chunkSummary (Map output).
func summarySchema() *genai.Schema {
	return &genai.Schema{
		Type: genai.TypeObject,
		Properties: map[string]*genai.Schema{
			"summary": {Type: genai.TypeString},
		},
		Required: []string{"summary"},
	}
}

// narrationSchema is the JSON schema for finalNarration (Reduce output).
func narrationSchema() *genai.Schema {
	return &genai.Schema{
		Type: genai.TypeObject,
		Properties: map[string]*genai.Schema{
			"narration": {Type: genai.TypeString},
		},
		Required: []string{"narration"},
	}
}

// mapSystem returns the system prompt for the Map phase based on mode.
func mapSystem(mode Mode) string {
	switch mode {
	case ModeSummary:
		return "You are an editor condensing written text into a concise spoken " +
			"summary for an audiobook. Write a summary that preserves the key points " +
			"and main ideas of the passage as flowing spoken prose. Use complete " +
			"sentences, no markdown, no headings, no bullet points. Do not invent " +
			"content."
	case ModeAbstract:
		return "You are an editor condensing written text into a very brief spoken " +
			"abstract for an audiobook. State, in one or two spoken sentences, only " +
			"the single most important idea of the passage. Use complete sentences, " +
			"no markdown, no headings, no bullet points. Do not invent content."
	default:
		return ""
	}
}

// reduceSystem returns the system prompt for the Reduce phase based on mode.
func reduceSystem(mode Mode) string {
	switch mode {
	case ModeSummary:
		return "You are an editor combining partial summaries into one coherent " +
			"audiobook narration. Combine the following partial summaries into a " +
			"single flowing narration that reads well aloud, removing any redundancy " +
			"and preserving the key points. Use spoken prose only, no markdown, no " +
			"headings, no bullet points. Return the field 'narration' with the " +
			"combined text."
	case ModeAbstract:
		return "You are an editor combining brief notes into a single short abstract " +
			"for an audiobook. Combine the following notes into a single short " +
			"abstract (a few spoken sentences) capturing the whole chapter's essence. " +
			"Use spoken prose only, no markdown, no headings, no bullet points. " +
			"Return the field 'narration' with the abstract text."
	default:
		return ""
	}
}

// Compile-time check that Summarizer satisfies Preparer.
var _ Preparer = (*Summarizer)(nil)
