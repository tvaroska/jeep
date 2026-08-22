// Package pipeline orchestrates the end-to-end document-to-audiobook workflow,
// with resumable content-addressed caching, per-segment loudness normalization,
// adaptive re-split, and manifest generation. It drives off the format-agnostic
// ingest Section tree, not PDF pages, and synthesizes through the shared gemini
// client (via the audio.Synthesizer seam).
package pipeline

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"time"

	"golang.org/x/sync/errgroup"

	"github.com/tvaroska/jeep/internal/audio"
	"github.com/tvaroska/jeep/internal/describe"
	"github.com/tvaroska/jeep/internal/ingest"
	"github.com/tvaroska/jeep/internal/usage"
)

// Runner orchestrates the per-chapter pipeline: load the document, split it into
// chapters off the Section tree, then per chapter run Weave (image descriptions)
// → Prepare (listening adaptation) → Segment → Synthesize → Concat, and finally
// write a manifest. Injected dependencies allow offline testing with fakes.
type Runner struct {
	Source       ingest.Source     // input file -> Document (fake in tests)
	Weaver       *describe.Weaver  // holds a Describer; concrete struct (not a seam); required
	Preparer     describe.Preparer // listening adaptation seam
	Segmenter    *audio.Segmenter  // pure/local; nil-safe via r.segmenter()
	Synthesizer  audio.Synthesizer
	Concatenator audio.Concatenator
	Prober       Prober       // nil-safe => &FFprobeProber{}
	Log          *slog.Logger // nil-safe => slog.Default()

	// Concurrency bounds the per-chapter TTS worker pool (segments synthesized in
	// parallel within a chapter; chapters stay sequential). <=0 => defaultConcurrency.
	Concurrency int

	// Cache makes re-runs resumable and cheap by skipping already-completed
	// expensive work (Gemini describe/prepare, TTS synth). A nil Cache falls back
	// to nopCache{} (no caching).
	Cache          Cache  // nil-safe => nopCache{} (no caching)
	GeminiModel    string // identifies Weave/Prepare output for the narration cache key ("" ok)
	TTSFingerprint string // "model|voice|style" for the segment/chapter cache keys ("" ok)

	// Progress, if non-nil, is called with a human-readable status line at each
	// chapter boundary (synthesizing / resumed-from-cache). nil => no progress output.
	Progress func(string)

	// Usage, if non-nil, accumulates per-run token usage recorded by the
	// describe/prepare adapters; its totals are logged at the end of Run.
	// nil => no usage reporting.
	Usage *usage.Tracker
}

// Options configures a pipeline run. It is format-agnostic: InputPath may be any
// supported format (PDF/ePub/markdown/HTML), and chapter selection is by 1-based
// index into the Structure output (page windows have no meaning over a Section
// tree — see the S1-ab-9 proposal in DECISIONS.md).
type Options struct {
	InputPath string // any supported format; replaces audi's PDFPath
	OutputDir string
	Chapters  []int // 1-based indices into ingest.Structure output; nil = all
}

// Run executes the per-chapter pipeline and returns the path of the written
// manifest.json. One MP3 is produced per non-empty chapter; blank chapters are
// skipped (a fully blank/scanned document still errors). Context cancellation is
// honored at chapter and segment boundaries.
func (r *Runner) Run(ctx context.Context, opts Options) (string, error) {
	// Load the document (Section tree) for the whole book, once.
	doc, err := r.Source.Load(opts.InputPath)
	if err != nil {
		return "", fmt.Errorf("run: load: %w", err)
	}

	// Split the book into chapters (>=1 for a non-empty doc).
	chapters := ingest.Structure(doc)

	// Apply chapter selection (1-based index).
	chapters, err = selectChapters(chapters, opts)
	if err != nil {
		return "", fmt.Errorf("run: %w", err)
	}
	if len(chapters) == 0 {
		return "", fmt.Errorf("run: no chapters selected (check --chapters)")
	}

	seg := r.segmenter()
	log := r.logger()
	c := r.cache()

	var manifest Manifest
	manifest.Source = opts.InputPath
	manifest.GeneratedAt = time.Now().UTC()

	produced := 0 // number of chapter files written (drives chapter_NN numbering)
	total := len(chapters)
	for i, ch := range chapters {
		// Cheap early-out on cancellation before doing chapter work.
		if err := ctx.Err(); err != nil {
			return "", err
		}

		// Flatten this chapter's Section subtree into per-block PageContent units
		// in reading order (figure blocks -> one ImageRef each; prose -> text).
		pageContents := chapterToPageContents(ch)
		startPage, endPage := chapterPageSpan(ch)

		// Narration cache (level 1): key on the Weave/Prepare inputs and store the
		// final adapted narration. This guards both Gemini stages AND makes adapted
		// stable across process restarts — the prerequisite for the segment cache
		// (level 2) to hit after a fresh run, since Gemini output is non-deterministic.
		// All cache read/write errors are NON-FATAL: log at Warn and treat as a
		// miss/no-op so a broken cache degrades to "recompute", never aborts a run.
		narrKey := narrationCacheKey(r.GeminiModel, pageContents)
		var adapted string
		if data, ok, err := c.Get("narration", narrKey); err != nil {
			log.Warn("narration cache read failed; recomputing", "err", err) // NON-FATAL: fall through
		} else if ok {
			adapted = string(data)
			log.Debug("narration cache hit", "chapter", produced+1)
		}
		if adapted == "" { // miss, empty cached value, or read error
			// Weave image descriptions into the narration BEFORE listening adaptation,
			// so descriptions are part of the text the preparer smooths.
			woven, err := r.Weaver.Weave(ctx, pageContents)
			if err != nil {
				return "", fmt.Errorf("run: chapter %d weave: %w", produced+1, err)
			}

			// Prepare (listening adaptation) BEFORE segmentation. GeminiPreparer
			// chunks long chapters internally so output scales with input (no
			// whole-chapter single-call truncation).
			adapted, err = r.Preparer.Prepare(ctx, woven)
			if err != nil {
				return "", fmt.Errorf("run: chapter %d prepare: %w", produced+1, err)
			}
			if err := c.Put("narration", narrKey, []byte(adapted)); err != nil {
				log.Warn("narration cache write failed", "err", err) // NON-FATAL
			}
		}

		// Segment the final narration.
		segments := seg.Segment(adapted)

		// Skip empty chapters (blank front matter, no text, no images): do not
		// call Concat (it errors on zero segments) and do not add a manifest entry.
		if len(segments) == 0 {
			log.Warn("skipping empty chapter", "title", ch.Title)
			continue
		}

		n := produced + 1
		title := ch.Title
		if title == "" {
			title = fmt.Sprintf("Chapter %d", n)
		}

		// Chapter output path, numbered by produced-file counter so there are no
		// gaps even when early chapters are skipped. Computed before synth so the
		// completion short-circuit can check whether the mp3 already exists.
		filename := fmt.Sprintf("chapter_%02d.mp3", n)
		chapterPath := filepath.Join(opts.OutputDir, filename)

		// Chapter-completion short-circuit (level 3): if a completion record exists
		// AND the output mp3 still exists on disk, skip synth+concat+probe entirely
		// and rebuild the manifest entry from the record. Any error, missing record,
		// or missing mp3 falls through and rebuilds — correct after a crash between
		// concat and the record write, or if the mp3 was deleted.
		chapKey := chapterCacheKey(r.TTSFingerprint, adapted)
		if data, ok, err := c.Get("chapters", chapKey); err == nil && ok {
			var rec cachedChapter
			if json.Unmarshal(data, &rec) == nil {
				if _, statErr := os.Stat(chapterPath); statErr == nil {
					manifest.Chapters = append(manifest.Chapters, ChapterEntry{
						Title:           title,
						File:            filename,
						DurationSeconds: rec.DurationSeconds,
						StartPage:       startPage,
						EndPage:         endPage,
					})
					log.Info("resumed chapter from cache", "chapter", n, "title", title)
					r.progress("[%d/%d] %s (resumed from cache)", i+1, total, title)
					produced++
					continue
				}
			}
		}

		log.Info("synthesizing chapter", "chapter", n, "title", title, "segments", len(segments))
		r.progress("[%d/%d] %s: synthesizing %d segments", i+1, total, title, len(segments))

		// Synthesize this chapter's segments concurrently through a bounded worker
		// pool, fail-fast. Results are written into a pre-sized slice by index so
		// audio concatenates in reading order regardless of completion order —
		// never append from a goroutine. Segment-audio cache (level 2): key on the
		// segment text + TTS fingerprint, storing the raw audio bytes so a resume
		// skips the (many, expensive) TTS calls. Concurrent cache access is safe
		// here: distinct segments have distinct keys → distinct files in the
		// segments/ namespace, and DiskCache.Put writes atomically (temp+rename).
		// One or more audio blobs per segment: a segment gemini-tts rejects for
		// being too token-dense is re-split at runtime into smaller pieces, each
		// producing its own blob (see synthSegment). Blobs are flattened in reading
		// order before concat.
		audios := make([][][]byte, len(segments))
		g, gctx := errgroup.WithContext(ctx)
		g.SetLimit(r.concurrency())
		for i, segText := range segments {
			i, segText := i, segText // capture per-iteration
			g.Go(func() error {
				if err := gctx.Err(); err != nil {
					return err
				}
				blobs, err := r.synthSegment(gctx, c, log, segText, 0)
				if err != nil {
					return fmt.Errorf("run: chapter %d (%q) segment %d/%d (%d bytes; %q): %w",
						n, title, i+1, len(segments), len(segText), snippet(segText), err)
				}
				audios[i] = blobs
				log.Debug("synthesized segment", "chapter", n, "index", i+1, "total", len(segments))
				return nil
			})
		}
		if err := g.Wait(); err != nil {
			return "", err
		}

		// Flatten per-segment blobs into a single ordered list for concat.
		var flat [][]byte
		for _, blobs := range audios {
			flat = append(flat, blobs...)
		}

		// Concat to the per-chapter file.
		if err := r.Concatenator.Concat(ctx, flat, chapterPath); err != nil {
			return "", fmt.Errorf("run: chapter %d concat: %w", n, err)
		}

		// Probe duration — best-effort metadata: the audio is already written, so a
		// probe error is a warning + duration 0, never a run failure.
		var durSeconds float64
		if dur, err := r.prober().Duration(ctx, chapterPath); err != nil {
			log.Warn("probe chapter duration failed; using 0", "chapter", n, "path", chapterPath, "err", err)
		} else {
			durSeconds = dur.Seconds()
		}

		// Write the completion record only AFTER concat+probe succeed, so a resume
		// never short-circuits a chapter whose mp3 was not actually written. NON-FATAL.
		if b, err := json.Marshal(cachedChapter{File: filename, DurationSeconds: durSeconds}); err == nil {
			if err := c.Put("chapters", chapKey, b); err != nil {
				log.Warn("chapter cache write failed", "chapter", n, "err", err) // NON-FATAL
			}
		}

		manifest.Chapters = append(manifest.Chapters, ChapterEntry{
			Title:           title,
			File:            filename, // basename, so the manifest is relocatable
			DurationSeconds: durSeconds,
			StartPage:       startPage,
			EndPage:         endPage,
		})
		produced++
	}

	// All-chapters-empty guard: preserve behavior for scanned/blank documents.
	if produced == 0 {
		return "", fmt.Errorf("run: no extractable text in %q (scanned document? OCR is out of scope)", opts.InputPath)
	}

	manifestPath := filepath.Join(opts.OutputDir, "manifest.json")
	if err := WriteManifest(manifestPath, manifest); err != nil {
		return "", fmt.Errorf("run: %w", err)
	}

	log.Info("wrote manifest", "path", manifestPath, "chapters", produced)

	// Report token usage if tracking is enabled (jeep usage is token-only; TTS
	// carries no token metadata, so only describe/prepare contribute).
	if r.Usage != nil {
		rep := r.Usage.Report()
		r.Usage.Log(log) // structured slog line (stderr)
		r.progress("usage: %d tokens (%d calls)", rep.TotalTokens, rep.Calls)
	}

	return manifestPath, nil
}

// chapterToPageContents flattens a chapter's Section subtree (pre-order DFS: a
// section's own Blocks, then each child recursively) into describe.PageContent
// units in reading order. Each figure Block becomes its own PageContent carrying
// one ImageRef (so Weave inserts its spoken description at exactly that position);
// each non-figure Block with text becomes a prose PageContent. Empty blocks drop.
func chapterToPageContents(ch ingest.Chapter) []describe.PageContent {
	var out []describe.PageContent
	var walk func(s *ingest.Section)
	walk = func(s *ingest.Section) {
		if s == nil {
			return
		}
		for _, b := range s.Blocks {
			switch b.Kind {
			case ingest.BlockFigure:
				if len(b.Data) > 0 {
					out = append(out, describe.PageContent{
						Images: []describe.ImageRef{{Data: b.Data, MimeType: b.MimeType}},
					})
				}
			default:
				if strings.TrimSpace(b.Text) != "" {
					out = append(out, describe.PageContent{Text: b.Text})
				}
			}
		}
		for _, c := range s.Children {
			walk(c)
		}
	}
	for _, s := range ch.Sections {
		walk(s)
	}
	return out
}

// chapterPageSpan derives a chapter's [StartPage, EndPage] from its blocks'
// Page values for PDF fidelity: StartPage is the smallest non-zero Page, EndPage
// the largest. Page-less formats (ePub/markdown/HTML) carry Page==0 on every
// block, so both come back 0. Walks the same Section subtree as
// chapterToPageContents (pre-order DFS).
func chapterPageSpan(ch ingest.Chapter) (start, end int) {
	var walk func(s *ingest.Section)
	walk = func(s *ingest.Section) {
		if s == nil {
			return
		}
		for _, b := range s.Blocks {
			if b.Page <= 0 {
				continue
			}
			if start == 0 || b.Page < start {
				start = b.Page
			}
			if b.Page > end {
				end = b.Page
			}
		}
		for _, c := range s.Children {
			walk(c)
		}
	}
	for _, s := range ch.Sections {
		walk(s)
	}
	return start, end
}

// defaultConcurrency bounds the per-chapter TTS worker pool when Runner.Concurrency
// is unset.
const defaultConcurrency = 4

// concurrency returns r.Concurrency if positive, else defaultConcurrency. It never
// returns a value < 1 (SetLimit(0) would block forever).
func (r *Runner) concurrency() int {
	if r.Concurrency > 0 {
		return r.Concurrency
	}
	return defaultConcurrency
}

// cache returns r.Cache if non-nil, else a no-op cache (no caching).
func (r *Runner) cache() Cache {
	if r.Cache != nil {
		return r.Cache
	}
	return nopCache{}
}

// maxSplitDepth bounds adaptive re-split recursion. Each level roughly halves
// the byte budget, so 4 levels take a 3000-byte segment down to ~190 bytes —
// far below any plausible token limit. The bound also guarantees termination on
// pathological input that never satisfies the API.
const maxSplitDepth = 4

// synthSegment synthesizes text to one or more ordered audio blobs, consulting
// and populating the segment cache per piece. If gemini-tts rejects the text
// with a Vertex 400 (too token-dense — see audio.IsOversizedInput), it splits
// the text into smaller pieces and recurses, so a single oversized segment no
// longer aborts the whole run. Every other error (transient, exhausted retries,
// context cancellation) is surfaced unchanged. Pieces are cached individually,
// so a resume reuses them.
func (r *Runner) synthSegment(ctx context.Context, c Cache, log *slog.Logger, text string, depth int) ([][]byte, error) {
	segKey := segmentCacheKey(r.TTSFingerprint, text)
	if data, ok, err := c.Get("segments", segKey); err != nil {
		log.Warn("segment cache read failed; synthesizing", "err", err) // NON-FATAL
	} else if ok {
		return [][]byte{data}, nil
	}

	audioBlob, err := r.Synthesizer.Synthesize(ctx, text)
	if err == nil {
		if err := c.Put("segments", segKey, audioBlob); err != nil {
			log.Warn("segment cache write failed", "err", err) // NON-FATAL
		}
		return [][]byte{audioBlob}, nil
	}

	// A cancelled/expired parent context is fatal, not a re-split trigger.
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}

	// Only an oversized-input (Vertex 400) rejection is worth re-splitting;
	// anything else won't be fixed by smaller pieces.
	pieces := splitSegmentText(text)
	if !audio.IsOversizedInput(err) || depth >= maxSplitDepth || len(pieces) < 2 {
		return nil, err
	}

	log.Warn("tts rejected segment as oversized; re-splitting",
		"bytes", len(text), "pieces", len(pieces), "depth", depth)

	var out [][]byte
	for _, p := range pieces {
		blobs, perr := r.synthSegment(ctx, c, log, p, depth+1)
		if perr != nil {
			return nil, perr
		}
		out = append(out, blobs...)
	}
	return out, nil
}

// splitSegmentText breaks text into smaller sentence/word-aligned pieces, each
// at most about half the input's byte length, by reusing the segmenter's tested
// splitting logic. It returns the input unchanged when it cannot be split
// further (a single indivisible token), which the caller treats as "give up".
func splitSegmentText(text string) []string {
	half := len(text) / 2
	if half < 1 {
		return []string{text}
	}
	return (&audio.Segmenter{MaxBytes: half}).Segment(text)
}

// snippet returns a single-line, length-capped preview of s for diagnostics,
// collapsing newlines so a failing segment's text stays on one log line.
func snippet(s string) string {
	const max = 160
	s = strings.ReplaceAll(s, "\n", " ")
	if len(s) > max {
		return s[:max] + "…"
	}
	return s
}

// segmenter returns r.Segmenter if non-nil, else a default Segmenter.
func (r *Runner) segmenter() *audio.Segmenter {
	if r.Segmenter != nil {
		return r.Segmenter
	}
	return &audio.Segmenter{}
}

// prober returns r.Prober if non-nil, else a default FFprobeProber.
func (r *Runner) prober() Prober {
	if r.Prober != nil {
		return r.Prober
	}
	return &FFprobeProber{Log: r.Log}
}

// logger returns r.Log if non-nil, else slog.Default()
func (r *Runner) logger() *slog.Logger {
	if r.Log != nil {
		return r.Log
	}
	return slog.Default()
}

// progress calls r.Progress if non-nil, formatting the message like fmt.Sprintf.
func (r *Runner) progress(format string, args ...any) {
	if r.Progress != nil {
		r.Progress(fmt.Sprintf(format, args...))
	}
}

// selectChapters narrows the Structure output to the 1-based indices in
// opts.Chapters (nil/empty = all). Returns an error on an out-of-range index.
func selectChapters(chapters []ingest.Chapter, opts Options) ([]ingest.Chapter, error) {
	if len(opts.Chapters) == 0 {
		return chapters, nil
	}
	chSet := map[int]bool{}
	for _, idx := range opts.Chapters {
		if idx < 1 || idx > len(chapters) {
			return nil, fmt.Errorf("chapter %d out of range (document has %d chapters)", idx, len(chapters))
		}
		chSet[idx] = true
	}
	var sel []ingest.Chapter
	for i, ch := range chapters {
		if chSet[i+1] { // 1-based
			sel = append(sel, ch)
		}
	}
	return sel, nil
}
