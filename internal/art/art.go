// Package art produces a per-chapter cover image for the audiobook pipeline. It
// is a GCP boundary (Vertex AI Gemini image generation), so — mirroring
// describe.GeminiPreparer and audio.GeminiSynthesizer — it reaches the shared
// gemini client only through a package-local narrow interface (imageClient) that
// *gemini.Client satisfies structurally; tests inject a fake. The pipeline owns
// the ffmpeg APIC embed and PNG sidecar; this package only yields image bytes.
package art

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"sync"

	"github.com/tvaroska/jeep/internal/gemini"
)

// defaultRetries mirrors describe's default retry budget for a paid API call.
const defaultRetries = 4

// defaultStylePrompt is the fixed house-style prefix applied to generated cover
// art when Provider.StylePrompt is empty. It steers toward a clean, evocative,
// text-free chapter cover suitable for an audiobook player thumbnail.
const defaultStylePrompt = "A tasteful, evocative audiobook chapter cover " +
	"illustration in a clean modern editorial style, rich but restrained color, " +
	"no text, no words, no lettering, no logos."

// Mode selects how a chapter's cover image is produced.
type Mode string

const (
	// ModeGenerate creates a fresh image per chapter from its title + summary.
	ModeGenerate Mode = "generate"
	// ModeExtract reuses the chapter's largest embedded figure (no API call).
	ModeExtract Mode = "extract"
	// ModeBookCover generates one image from the book title and reuses it for
	// every chapter.
	ModeBookCover Mode = "book-cover"
	// ModeNone disables cover art entirely (default; zero extra cost).
	ModeNone Mode = "none"
)

// ParseMode validates a --art value (case-insensitive, trimmed). It mirrors
// describe.ParseMode.
func ParseMode(s string) (Mode, error) {
	switch Mode(strings.ToLower(strings.TrimSpace(s))) {
	case ModeGenerate:
		return ModeGenerate, nil
	case ModeExtract:
		return ModeExtract, nil
	case ModeBookCover:
		return ModeBookCover, nil
	case ModeNone:
		return ModeNone, nil
	default:
		return "", fmt.Errorf("invalid art mode %q (want generate, extract, book-cover, or none)", s)
	}
}

// Image is a cover image's bytes plus its MIME type.
type Image struct {
	Data     []byte
	MIMEType string
}

// imageClient is the minimal subset of *gemini.Client this adapter needs.
// *gemini.Client satisfies it structurally; tests supply a fake.
type imageClient interface {
	GenerateImage(ctx context.Context, model, prompt string, inputs []gemini.FileInput, retries int) (*gemini.ImageResult, error)
}

// compile-time check that the real client satisfies the narrow interface.
var _ imageClient = (*gemini.Client)(nil)

// Provider yields a cover Image for a chapter according to Mode. It mirrors the
// concrete-adapter idiom of describe.GeminiPreparer: an injected client field, a
// nil-safe logger, and no factory function (the CLI constructs it directly).
//
// A single *Provider is safe for concurrent use: ForChapter reads only receiver
// fields for generate/extract, and book-cover memoization is guarded by sync.Once.
type Provider struct {
	Client      imageClient  // nil ok for extract/none
	Model       string       // IMAGE model (resolved by the CLI)
	Mode        Mode         // one of the Mode constants
	BookTitle   string       // for book-cover and generate prompt context
	StylePrompt string       // fixed house-style prefix ("" => defaultStylePrompt)
	Retries     int          // <=0 => defaultRetries
	Log         *slog.Logger // nil-safe

	// book-cover memoization: the pipeline is sequential today, but guard anyway.
	once     sync.Once
	cover    Image
	coverErr error
}

// logger returns p.Log if non-nil, else slog.Default().
func (p *Provider) logger() *slog.Logger {
	if p.Log != nil {
		return p.Log
	}
	return slog.Default()
}

// retries returns p.Retries if positive, else defaultRetries.
func (p *Provider) retries() int {
	if p.Retries > 0 {
		return p.Retries
	}
	return defaultRetries
}

// stylePrompt returns p.StylePrompt if set, else defaultStylePrompt.
func (p *Provider) stylePrompt() string {
	if s := strings.TrimSpace(p.StylePrompt); s != "" {
		return s
	}
	return defaultStylePrompt
}

// ForChapter returns the cover image for one chapter.
//
//   - ModeNone: (Image{}, false, nil) — no art.
//   - ModeGenerate: generate a fresh image from the book/chapter title + summary.
//   - ModeBookCover: generate one image from the book title, memoized and reused
//     for every chapter.
//   - ModeExtract: pick the largest supplied figure (by byte length); if there
//     are none, return ok=false (that chapter simply gets no art), never an error.
//
// ok is false when there is deliberately no image (none, or extract with no
// figures). err is non-nil only on a real generation failure; the pipeline
// treats every art error as non-fatal (Warn + continue).
func (p *Provider) ForChapter(ctx context.Context, title, summary string, figures []Image) (Image, bool, error) {
	switch p.Mode {
	case ModeNone, "":
		return Image{}, false, nil

	case ModeExtract:
		best, ok := largestFigure(figures)
		if !ok {
			p.logger().Debug("no figure to extract for chapter", "title", title)
			return Image{}, false, nil
		}
		return best, true, nil

	case ModeBookCover:
		p.once.Do(func() {
			prompt := p.bookCoverPrompt()
			p.cover, p.coverErr = p.generate(ctx, prompt)
		})
		if p.coverErr != nil {
			return Image{}, false, p.coverErr
		}
		return p.cover, true, nil

	case ModeGenerate:
		img, err := p.generate(ctx, p.chapterPrompt(title, summary))
		if err != nil {
			return Image{}, false, err
		}
		return img, true, nil

	default:
		return Image{}, false, fmt.Errorf("art: unknown mode %q", p.Mode)
	}
}

// generate calls the image client and returns the resulting Image. It errors if
// no client is configured or the model returns no image bytes.
func (p *Provider) generate(ctx context.Context, prompt string) (Image, error) {
	if p.Client == nil {
		return Image{}, fmt.Errorf("art: no image client configured for mode %q", p.Mode)
	}
	res, err := p.Client.GenerateImage(ctx, p.Model, prompt, nil, p.retries())
	if err != nil {
		return Image{}, fmt.Errorf("art: generate image: %w", err)
	}
	if res == nil || len(res.Data) == 0 {
		return Image{}, fmt.Errorf("art: model returned no image data")
	}
	mime := res.MIMEType
	if mime == "" {
		mime = "image/png"
	}
	return Image{Data: res.Data, MIMEType: mime}, nil
}

// chapterPrompt builds the generation prompt for a single chapter.
func (p *Provider) chapterPrompt(title, summary string) string {
	var b strings.Builder
	b.WriteString(p.stylePrompt())
	if t := strings.TrimSpace(p.BookTitle); t != "" {
		fmt.Fprintf(&b, " From the audiobook %q.", t)
	}
	if t := strings.TrimSpace(title); t != "" {
		fmt.Fprintf(&b, " Chapter: %q.", t)
	}
	if s := strings.TrimSpace(summary); s != "" {
		fmt.Fprintf(&b, " The chapter is about: %s", s)
	}
	return b.String()
}

// bookCoverPrompt builds the single generation prompt used for book-cover mode.
func (p *Provider) bookCoverPrompt() string {
	var b strings.Builder
	b.WriteString(p.stylePrompt())
	if t := strings.TrimSpace(p.BookTitle); t != "" {
		fmt.Fprintf(&b, " Cover art for the audiobook %q.", t)
	} else {
		b.WriteString(" Cover art for an audiobook.")
	}
	return b.String()
}

// largestFigure returns the figure with the most bytes (a proxy for the most
// detailed/prominent image), and false if there are no non-empty figures.
func largestFigure(figures []Image) (Image, bool) {
	best := -1
	for i, f := range figures {
		if len(f.Data) == 0 {
			continue
		}
		if best < 0 || len(f.Data) > len(figures[best].Data) {
			best = i
		}
	}
	if best < 0 {
		return Image{}, false
	}
	return figures[best], true
}
