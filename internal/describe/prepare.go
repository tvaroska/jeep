package describe

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"sort"
	"strings"

	"golang.org/x/sync/errgroup"
	"google.golang.org/genai"

	"github.com/tvaroska/jeep/internal/gemini"
	"github.com/tvaroska/jeep/internal/usage"
)

// ImageRef locates one image within a page and carries what Weave needs to
// describe it. It is a describe-local, primitives-only type so package describe
// stays independent of pdfx (the pipeline maps pdfx.Image -> ImageRef).
type ImageRef struct {
	Data     []byte  // image bytes (as extracted by pdfx)
	MimeType string  // e.g. "image/png"
	Top      float64 // y-position in page points; orders multiple images on a page
	Context  string  // optional surrounding text/caption to ground the description ("")
}

// PageContent is one page's narration text plus the images that appear on it,
// in the same page order pdfx returns. Weave consumes a slice of these.
type PageContent struct {
	Text   string     // page text in reading order
	Images []ImageRef // images on this page (any order; Weave sorts by Top)
}

// Weaver inserts spoken image descriptions into narration text at each image's
// position in reading order (page-level granularity).
// It delegates description generation to the Describer seam, so the same Weaver
// works offline (StubDescriber) or against Gemini (GeminiDescriber).
type Weaver struct {
	Describer Describer    // required
	Log       *slog.Logger // optional; nil-safe (slog.Default() when nil)

	// Concurrency bounds the image-description worker pool. <=0 => defaultWeaveConcurrency.
	Concurrency int
}

// defaultWeaveConcurrency bounds the image-description worker pool when
// Weaver.Concurrency is unset.
const defaultWeaveConcurrency = 4

// logger returns w.Log if non-nil, else slog.Default().
func (w *Weaver) logger() *slog.Logger {
	if w.Log != nil {
		return w.Log
	}
	return slog.Default()
}

// concurrency returns w.Concurrency if positive, else defaultWeaveConcurrency. It
// never returns a value < 1 (SetLimit(0) would block forever).
func (w *Weaver) concurrency() int {
	if w.Concurrency > 0 {
		return w.Concurrency
	}
	return defaultWeaveConcurrency
}

// Weave returns the pages' narration text with each image's spoken description
// woven in at the image's position in reading order. Pages are joined by a blank
// line ("\n\n"), matching pdfx.Document.Text(); each description is appended as
// its own paragraph after the text of the page it appears on. Within a page,
// images are ordered by Top ascending (stable). It calls Describer once per image
// through a bounded concurrent worker pool, but assembles the output in
// reading order so the result is identical to sequential weaving. Context
// cancellation is honored.
//
// Error handling is fail-fast: the first Describer error stops weaving and returns
// an error naming the page.
func (w *Weaver) Weave(ctx context.Context, pages []PageContent) (string, error) {
	// Cheap early-out on cancellation, so a pre-cancelled context returns
	// context.Canceled even when there are zero images.
	if err := ctx.Err(); err != nil {
		return "", err
	}

	// Phase 1 (sequential, cheap): build an ordered list of items. Each item is
	// either a literal text string or an image to describe. Images within a page
	// are ordered by Top ascending (stable).
	items := make([]weaveItem, 0, len(pages))
	for pageIdx, page := range pages {
		if trimmed := strings.TrimSpace(page.Text); trimmed != "" {
			items = append(items, weaveItem{text: trimmed})
		}

		// Sort images by Top ascending (stable sort, do not mutate caller's slice)
		sortedImages := make([]ImageRef, len(page.Images))
		copy(sortedImages, page.Images)
		sort.SliceStable(sortedImages, func(i, j int) bool {
			return sortedImages[i].Top < sortedImages[j].Top
		})
		for _, img := range sortedImages {
			items = append(items, weaveItem{isImage: true, img: img, pageIdx: pageIdx})
		}
	}

	// Phase 2 (concurrent): describe images through a bounded worker pool. Each
	// worker writes its result into its own item by index, never appending, so
	// ordering is preserved. errgroup cancels gctx on the first error and Wait
	// returns that (real) error, giving fail-fast with the page-numbered wrapping.
	g, gctx := errgroup.WithContext(ctx)
	g.SetLimit(w.concurrency())
	for idx := range items {
		if !items[idx].isImage {
			continue
		}
		it := &items[idx]
		g.Go(func() error {
			if err := gctx.Err(); err != nil {
				return err
			}
			desc, err := w.Describer.Describe(gctx, it.img.Data, it.img.MimeType, it.img.Context)
			if err != nil {
				return fmt.Errorf("weave: describe image on page %d: %w", it.pageIdx+1, err)
			}
			it.result = strings.TrimSpace(desc)
			return nil
		})
	}
	if err := g.Wait(); err != nil {
		return "", err
	}

	// Phase 3 (sequential, cheap): assemble in reading order, skipping empty
	// descriptions to preserve the no-leading/triple-newline behavior.
	var parts []string
	for _, it := range items {
		if it.isImage {
			if it.result != "" {
				parts = append(parts, it.result)
			}
			continue
		}
		parts = append(parts, it.text)
	}

	return strings.Join(parts, "\n\n"), nil
}

// weaveItem is one ordered element of a woven chapter: either literal text or an
// image whose description is filled in concurrently (into result) during Weave.
type weaveItem struct {
	text    string   // literal narration text (when !isImage)
	isImage bool     // true => describe img and use result
	img     ImageRef // image to describe (when isImage)
	pageIdx int      // 0-based page index, for error messages (when isImage)
	result  string   // trimmed description, filled by a worker (when isImage)
}

// Defaults for the GeminiPreparer GCP adapter, resolved lazily inside Prepare.
const (
	defaultPrepareMaxInputChars = 6000
	defaultPrepareConcurrency   = 4
)

// defaultPrepareSystemPrompt steers Gemini to edit lightly for the ear without
// changing meaning. It includes artifact-repair guidance for PDF extraction issues.
const defaultPrepareSystemPrompt = "You are an editor preparing written text to be read " +
	"aloud as an audiobook. Rewrite it lightly so it flows naturally for a listener, while " +
	"preserving all meaning and every fact. Never summarize, omit, or invent content. " +
	"Additionally, repair common PDF extraction artifacts: re-join words split across line " +
	"wraps (such as hyphenated words or mid-word breaks), restore dropped leading letters " +
	"and ligatures at word starts (for example, 'unied' should become 'unified', 'ree pillars' " +
	"should be 'three pillars', and restore dropped 'The', 'Th', 'fi', etc.). Make these repairs " +
	"without changing the meaning or inventing content."

// defaultPrepareInstruction is the per-call user instruction accompanying the text.
const defaultPrepareInstruction = "Rewrite the following text for audiobook narration. " +
	"Turn tables and bulleted lists into flowing spoken prose. Remove or naturalize inline " +
	"citation markers such as \"[12]\" or \"(Smith et al., 2020)\". Smooth cross-references " +
	"like \"see Figure 3\" into natural phrasing, since figures are already described aloud. " +
	"Do not summarize, drop, or add information. Do not use markdown, headings, or bullet " +
	"characters. Return only the rewritten narration text.\n\nText:\n"

// Preparer lightly rewrites narration text so it reads well aloud (tables -> prose,
// citation cleanup, figure-reference smoothing). It is a GCP boundary, so it is a seam
// with a GCP adapter (GeminiPreparer) and a local adapter (StubPreparer), mirroring
// Describer. Implementations must be safe for concurrent use.
type Preparer interface {
	Prepare(ctx context.Context, text string) (string, error)
}

// GeminiPreparer is the GCP adapter: it calls Vertex AI Gemini to rewrite narration
// text for listening, retrying transient HTTP failures via gemini.Generate.
// Each Prepare call reads only the receiver's fields and builds a fresh request, so a
// single *GeminiPreparer is safe to call from many goroutines.
type GeminiPreparer struct {
	Client        generator      // reuse the generator interface from images.go
	Model         string         // e.g. "gemini-2.5-flash"
	SystemPrompt  string         // "" => defaultPrepareSystemPrompt
	MaxOutputTokens int32        // <=0 => leave unset (use model's default budget)
	MaxInputChars int            // <=0 => defaultPrepareMaxInputChars (chunk size)
	Concurrency   int            // <=0 => defaultPrepareConcurrency (chunk worker pool)
	Retries       int            // <=0 => defaultRetries
	Log           *slog.Logger   // optional; nil-safe
	Usage         *usage.Tracker // optional; nil => no cost tracking
}

// logger returns g.Log if non-nil, else slog.Default().
func (g *GeminiPreparer) logger() *slog.Logger {
	if g.Log != nil {
		return g.Log
	}
	return slog.Default()
}

// concurrency returns g.Concurrency if positive, else defaultPrepareConcurrency.
// It never returns a value < 1 (SetLimit(0) would block forever).
func (g *GeminiPreparer) concurrency() int {
	if g.Concurrency > 0 {
		return g.Concurrency
	}
	return defaultPrepareConcurrency
}

// maxInputChars returns g.MaxInputChars if positive, else defaultPrepareMaxInputChars.
func (g *GeminiPreparer) maxInputChars() int {
	if g.MaxInputChars > 0 {
		return g.MaxInputChars
	}
	return defaultPrepareMaxInputChars
}

// retries returns g.Retries if positive, else defaultRetries.
func (g *GeminiPreparer) retries() int {
	if g.Retries > 0 {
		return g.Retries
	}
	return defaultRetries
}

// Prepare returns a version of text lightly rewritten for audiobook narration.
// Empty input is a no-op (returns "", nil without an API call).
//
// A chapter's text routinely exceeds the model's output-token budget, which would
// silently truncate/summarize a single call. Prepare therefore splits long text into
// paragraph-aligned chunks of at most maxInputChars, prepares each concurrently
// through a bounded worker pool, and rejoins them in reading order with a blank line.
func (g *GeminiPreparer) Prepare(ctx context.Context, text string) (string, error) {
	text = strings.TrimSpace(text)
	if text == "" {
		return "", nil
	}

	chunks := chunkForPrepare(text, g.maxInputChars())
	if len(chunks) <= 1 {
		return g.prepareOne(ctx, text)
	}

	// Prepare chunks concurrently, writing each result into its own slot by index
	// so the output rejoins in reading order regardless of completion order.
	// Fail-fast: the first chunk error cancels the rest and is returned.
	outs := make([]string, len(chunks))
	grp, gctx := errgroup.WithContext(ctx)
	grp.SetLimit(g.concurrency())
	for i, chunk := range chunks {
		i, chunk := i, chunk
		grp.Go(func() error {
			if err := gctx.Err(); err != nil {
				return err
			}
			out, err := g.prepareOne(gctx, chunk)
			if err != nil {
				return fmt.Errorf("prepare: chunk %d/%d: %w", i+1, len(chunks), err)
			}
			outs[i] = out
			return nil
		})
	}
	if err := grp.Wait(); err != nil {
		return "", err
	}

	// Rejoin non-empty chunk outputs in reading order.
	var parts []string
	for _, out := range outs {
		if out = strings.TrimSpace(out); out != "" {
			parts = append(parts, out)
		}
	}
	return strings.Join(parts, "\n\n"), nil
}

// prepareOne sends a single (already size-bounded) chunk to Gemini and returns the
// rewritten text. MaxOutputTokens is left unset (0) so the model's default (large)
// budget applies, avoiding truncation (jeep's GenerateOptions has no ThinkingConfig).
func (g *GeminiPreparer) prepareOne(ctx context.Context, text string) (string, error) {
	textPart := genai.NewPartFromText(defaultPrepareInstruction + text)
	contents := []*genai.Content{
		genai.NewContentFromParts([]*genai.Part{textPart}, genai.RoleUser),
	}

	sys := g.SystemPrompt
	if sys == "" {
		sys = defaultPrepareSystemPrompt
	}
	temp := defaultTemperature
	schema := &genai.Schema{
		Type: genai.TypeObject,
		Properties: map[string]*genai.Schema{
			"narration": {Type: genai.TypeString},
		},
		Required: []string{"narration"},
	}

	// MaxTokens left at 0 (unset) so model default applies; see the ThinkingConfig gotcha.
	maxTokens := g.MaxOutputTokens
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
		Narration string `json:"narration"`
	}
	if err := json.Unmarshal([]byte(resp.Text), &result); err != nil {
		return "", fmt.Errorf("prepare: failed to parse JSON response: %w", err)
	}

	// Record token usage if tracking is enabled.
	// GenerateResult has no TotalTokens field; compute prompt + output.
	g.Usage.Record(g.Model, int64(resp.PromptTokens), int64(resp.OutputTokens), int64(resp.PromptTokens+resp.OutputTokens))

	out := strings.TrimSpace(result.Narration)
	if out == "" {
		return "", fmt.Errorf("prepare: empty response from model")
	}
	return out, nil
}

// chunkForPrepare splits text into chunks of at most maxChars characters, breaking
// on paragraph boundaries ("\n\n") to keep semantic units intact. A single paragraph
// longer than maxChars is hard-split on word boundaries (splitLong) so no chunk grows
// unbounded. Empty/whitespace paragraphs are dropped. The concatenation of the
// returned chunks preserves the original reading order.
func chunkForPrepare(text string, maxChars int) []string {
	if maxChars <= 0 {
		maxChars = defaultPrepareMaxInputChars
	}

	var chunks []string
	var b strings.Builder
	flush := func() {
		if b.Len() > 0 {
			chunks = append(chunks, strings.TrimSpace(b.String()))
			b.Reset()
		}
	}

	for _, para := range strings.Split(text, "\n\n") {
		para = strings.TrimSpace(para)
		if para == "" {
			continue
		}

		// A single oversized paragraph is flushed on its own after word-splitting.
		if len(para) > maxChars {
			flush()
			chunks = append(chunks, splitLong(para, maxChars)...)
			continue
		}

		// Start a new chunk if appending this paragraph (plus the "\n\n" join) would
		// exceed the limit.
		if b.Len() > 0 && b.Len()+2+len(para) > maxChars {
			flush()
		}
		if b.Len() > 0 {
			b.WriteString("\n\n")
		}
		b.WriteString(para)
	}
	flush()
	return chunks
}

// splitLong splits a single long string into pieces of at most maxChars, breaking on
// word boundaries. A lone word longer than maxChars becomes its own (over-limit) piece
// rather than being cut mid-word.
func splitLong(s string, maxChars int) []string {
	var pieces []string
	var b strings.Builder
	for _, w := range strings.Fields(s) {
		if b.Len() > 0 && b.Len()+1+len(w) > maxChars {
			pieces = append(pieces, b.String())
			b.Reset()
		}
		if b.Len() > 0 {
			b.WriteByte(' ')
		}
		b.WriteString(w)
	}
	if b.Len() > 0 {
		pieces = append(pieces, b.String())
	}
	return pieces
}

// StubPreparer is the local adapter: a deterministic, offline passthrough used for
// pipeline wiring and unit tests. It returns the input unchanged (trimmed).
type StubPreparer struct{}

// Prepare returns the trimmed input unchanged. It honors context cancellation.
func (StubPreparer) Prepare(ctx context.Context, text string) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	return strings.TrimSpace(text), nil
}

// compile-time checks that both adapters satisfy the seam.
var (
	_ Preparer = (*GeminiPreparer)(nil)
	_ Preparer = StubPreparer{}
)
