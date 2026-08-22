package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"

	flag "github.com/spf13/pflag"
	"github.com/tvaroska/jeep/internal/audio"
	"github.com/tvaroska/jeep/internal/cli"
	"github.com/tvaroska/jeep/internal/config"
	"github.com/tvaroska/jeep/internal/describe"
	"github.com/tvaroska/jeep/internal/epubsource"
	"github.com/tvaroska/jeep/internal/gemini"
	"github.com/tvaroska/jeep/internal/ingest"
	"github.com/tvaroska/jeep/internal/log"
	"github.com/tvaroska/jeep/internal/pdfsource"
	"github.com/tvaroska/jeep/internal/pipeline"
	"github.com/tvaroska/jeep/internal/usage"
)

func main() {
	cli.RunMain("jeep-audiobook", run)
}

func run(args []string, stdout, stderr io.Writer) error {
	var voice, style, output, model, ttsModel, chaptersSpec, mode string
	var resume bool
	var fromPage, toPage, concurrency int
	var common cli.CommonFlags

	fs := cli.NewFlagSet("jeep-audiobook", stderr)
	common.Register(fs, 30*time.Minute)
	fs.StringVarP(&voice, "voice", "v", "Kore", "Voice name")
	fs.StringVar(&style, "style", "", "Speaking style")
	fs.StringVar(&mode, "mode", "full", "Narration mode: full, summary, or abstract")
	fs.BoolVar(&resume, "resume", false, "Resume from cached work")
	fs.StringVar(&chaptersSpec, "chapters", "", "Comma-separated chapter indices/ranges (1-based), e.g. 1,3-5")
	fs.IntVar(&fromPage, "from-page", 0, "Start page (PDF only, 0 = from start)")
	fs.IntVar(&toPage, "to-page", 0, "End page (PDF only, 0 = to end)")
	fs.IntVar(&concurrency, "concurrency", 0, "Concurrent TTS workers (0 = default)")
	fs.StringVarP(&output, "output", "o", "audiobook", "Output directory")
	fs.StringVar(&model, "model", "", "Text model name (for describe/prepare)")
	fs.StringVar(&ttsModel, "tts-model", "", "TTS model name")
	fs.Usage = func() { printUsage(fs, stderr) }

	if done, err := cli.Parse(fs, args, "jeep-audiobook", stdout, &common); err != nil || done {
		return err
	}

	if fs.NArg() != 1 {
		return cli.Exitf(cli.ExitUsage, "expected exactly one input file; got %d", fs.NArg())
	}
	input := fs.Arg(0)

	// Validate mode early (so dry-run also rejects bad modes).
	parsedMode, err := describe.ParseMode(mode)
	if err != nil {
		return cli.Exitf(cli.ExitUsage, "invalid --mode: %w", err)
	}

	cfg, err := cli.ResolveCommon(fs, &common.Project, &common.Region, &common.Quiet)
	if err != nil {
		return err
	}

	// Resolve models (offline, safe in dry-run).
	textModel := model
	if textModel == "" {
		textModel = config.ResolveModelWithConfig(cfg, "TEXT")
	}
	resolvedTTSModel := ttsModel
	if resolvedTTSModel == "" {
		resolvedTTSModel = config.ResolveModelWithConfig(cfg, "TTS")
	}

	// Build logger.
	level := "info"
	if common.Quiet {
		level = "warn"
	}
	logger := log.New(level)

	// Select source by extension.
	src, err := selectSource(input, logger)
	if err != nil {
		return err
	}

	// Load document and structure it into chapters.
	allChapters, err := planChapters(src, input)
	if err != nil {
		return cli.Exitf(cli.ExitIO, "loading document: %w", err)
	}
	if len(allChapters) == 0 {
		return cli.Exitf(cli.ExitUsage, "no chapters found in %q", input)
	}

	// Apply chapter selection.
	selectedIndices, err := selectIndices(chaptersSpec, fromPage, toPage, allChapters)
	if err != nil {
		return err
	}

	// Filter chapters.
	var selectedChapters []chapterInfo
	if len(selectedIndices) > 0 {
		for _, idx := range selectedIndices {
			if idx < 1 || idx > len(allChapters) {
				return cli.Exitf(cli.ExitUsage, "chapter index %d out of range [1, %d]", idx, len(allChapters))
			}
			selectedChapters = append(selectedChapters, allChapters[idx-1])
		}
	} else {
		selectedChapters = allChapters
	}

	if len(selectedChapters) == 0 {
		return cli.Exitf(cli.ExitUsage, "no chapters selected")
	}

	// Dry-run: print planned chapters without building client.
	if common.DryRun {
		return printDryRun(stdout, common.Format, input, output, textModel, resolvedTTSModel, mode, selectedChapters)
	}

	// Real run: build client and execute pipeline.
	ctx, cancel := context.WithTimeout(context.Background(), common.Timeout)
	defer cancel()

	client, err := gemini.NewClient(ctx, common.Project, common.Region)
	if err != nil {
		return cli.Exitf(cli.ExitAPI, "creating client: %w", err)
	}

	tracker := usage.New()

	// Wire seams.
	describer := &describe.GeminiDescriber{
		Client: client,
		Model:  textModel,
		Usage:  tracker,
	}
	weaver := &describe.Weaver{
		Describer:   describer,
		Concurrency: concurrency,
	}

	// Select preparer based on mode.
	var preparer describe.Preparer
	if parsedMode == describe.ModeFull {
		preparer = &describe.GeminiPreparer{
			Client:      client,
			Model:       textModel,
			Concurrency: concurrency,
			Usage:       tracker,
		}
	} else {
		preparer = &describe.Summarizer{
			Client:      client,
			Model:       textModel,
			Mode:        parsedMode,
			Concurrency: concurrency,
			Usage:       tracker,
			Log:         logger,
		}
	}
	synthesizer := &audio.GeminiSynthesizer{
		Client: client,
		Model:  resolvedTTSModel,
		Voice:  voice,
		Style:  style,
	}
	concatenator := &audio.FFmpegConcatenator{
		Normalize: true,
	}

	var cache pipeline.Cache
	if resume {
		cache = &pipeline.DiskCache{Dir: filepath.Join(output, ".cache")}
	}

	ttsFingerprint := fmt.Sprintf("%s|%s|%s", resolvedTTSModel, voice, style)

	runner := &pipeline.Runner{
		Source:         src,
		Weaver:         weaver,
		Preparer:       preparer,
		Synthesizer:    synthesizer,
		Concatenator:   concatenator,
		Concurrency:    concurrency,
		Cache:          cache,
		GeminiModel:    textModel,
		TTSFingerprint: ttsFingerprint,
		Usage:          tracker,
		Log:            logger,
	}
	if !common.Quiet {
		runner.Progress = func(msg string) {
			logger.Info(msg)
		}
	}

	if err := os.MkdirAll(output, 0755); err != nil {
		return cli.Exitf(cli.ExitIO, "creating output directory: %w", err)
	}

	opts := pipeline.Options{
		InputPath: input,
		OutputDir: output,
		Chapters:  selectedIndices,
	}

	manifestPath, err := runner.Run(ctx, opts)
	if err != nil {
		return cli.Exitf(cli.ExitAPI, "pipeline: %w", err)
	}

	// Print success.
	switch common.Format {
	case "json":
		out := struct {
			Manifest string `json:"manifest"`
			Output   string `json:"output_dir"`
			Chapters int    `json:"chapters"`
		}{
			Manifest: manifestPath,
			Output:   output,
			Chapters: len(selectedChapters),
		}
		enc := json.NewEncoder(stdout)
		enc.SetIndent("", "  ")
		if err := enc.Encode(out); err != nil {
			return fmt.Errorf("encoding output: %w", err)
		}
	default:
		if !common.Quiet {
			fmt.Fprintf(stderr, "Audiobook complete: %d chapters → %s\n", len(selectedChapters), output)
			fmt.Fprintf(stderr, "Manifest: %s\n", manifestPath)
			if tracker != nil {
				tracker.Log(logger)
			}
		}
	}
	return nil
}

func selectSource(path string, logger *slog.Logger) (ingest.Source, error) {
	ext := strings.ToLower(filepath.Ext(path))
	switch ext {
	case ".pdf":
		return &pdfsource.Source{Log: logger}, nil
	case ".epub":
		return &epubsource.Source{Log: logger}, nil
	default:
		return nil, cli.Exitf(cli.ExitUsage, "unsupported input format %q (supported: .pdf, .epub)", ext)
	}
}

type chapterInfo struct {
	Index     int
	Title     string
	StartPage int
	EndPage   int
}

func planChapters(src ingest.Source, input string) ([]chapterInfo, error) {
	doc, err := src.Load(input)
	if err != nil {
		return nil, err
	}
	chapters := ingest.Structure(doc)
	var result []chapterInfo
	for i, ch := range chapters {
		start, end := chapterPageSpan(ch.Sections)
		result = append(result, chapterInfo{
			Index:     i + 1,
			Title:     ch.Title,
			StartPage: start,
			EndPage:   end,
		})
	}
	return result, nil
}

// chapterPageSpan computes the [start, end] page span of a chapter by DFS over
// its sections' blocks. Returns (0, 0) for page-less formats.
func chapterPageSpan(sections []*ingest.Section) (int, int) {
	start, end := 0, 0
	var visit func(*ingest.Section)
	visit = func(s *ingest.Section) {
		if s == nil {
			return
		}
		for _, b := range s.Blocks {
			if b.Page > 0 {
				if start == 0 || b.Page < start {
					start = b.Page
				}
				if b.Page > end {
					end = b.Page
				}
			}
		}
		for _, child := range s.Children {
			visit(child)
		}
	}
	for _, s := range sections {
		visit(s)
	}
	return start, end
}

func selectIndices(chaptersSpec string, fromPage, toPage int, chapters []chapterInfo) ([]int, error) {
	var indices []int

	// Parse explicit --chapters.
	if chaptersSpec != "" {
		parsed, err := parseChapters(chaptersSpec, len(chapters))
		if err != nil {
			return nil, cli.Exitf(cli.ExitUsage, "invalid --chapters: %w", err)
		}
		indices = append(indices, parsed...)
	}

	// Add page-window indices.
	if fromPage > 0 || toPage > 0 {
		// Check if source has page info.
		hasPages := false
		for _, ch := range chapters {
			if ch.StartPage > 0 || ch.EndPage > 0 {
				hasPages = true
				break
			}
		}
		if !hasPages {
			return nil, cli.Exitf(cli.ExitUsage, "--from-page/--to-page require a PDF (this source has no page numbers)")
		}

		for _, ch := range chapters {
			// A chapter is in the window if end >= from (when from>0) AND start <= to (when to>0).
			inWindow := true
			if fromPage > 0 && (ch.EndPage == 0 || ch.EndPage < fromPage) {
				inWindow = false
			}
			if toPage > 0 && (ch.StartPage == 0 || ch.StartPage > toPage) {
				inWindow = false
			}
			if inWindow {
				indices = append(indices, ch.Index)
			}
		}
	}

	// Dedup and sort.
	if len(indices) > 0 {
		slices.Sort(indices)
		indices = slices.Compact(indices)
	}

	return indices, nil
}

// parseChapters parses a comma-separated spec like "1,3-5,7" into sorted []int.
func parseChapters(spec string, max int) ([]int, error) {
	var result []int
	parts := strings.Split(spec, ",")
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		if strings.Contains(p, "-") {
			// Range: A-B
			rangeParts := strings.SplitN(p, "-", 2)
			if len(rangeParts) != 2 {
				return nil, fmt.Errorf("invalid range %q", p)
			}
			a, err := strconv.Atoi(strings.TrimSpace(rangeParts[0]))
			if err != nil {
				return nil, fmt.Errorf("invalid range start %q", rangeParts[0])
			}
			b, err := strconv.Atoi(strings.TrimSpace(rangeParts[1]))
			if err != nil {
				return nil, fmt.Errorf("invalid range end %q", rangeParts[1])
			}
			if a < 1 || b > max || a > b {
				return nil, fmt.Errorf("range %d-%d out of bounds [1, %d]", a, b, max)
			}
			for i := a; i <= b; i++ {
				result = append(result, i)
			}
		} else {
			// Single index.
			n, err := strconv.Atoi(p)
			if err != nil {
				return nil, fmt.Errorf("invalid chapter number %q", p)
			}
			if n < 1 || n > max {
				return nil, fmt.Errorf("chapter %d out of range [1, %d]", n, max)
			}
			result = append(result, n)
		}
	}
	slices.Sort(result)
	result = slices.Compact(result)
	return result, nil
}

func printDryRun(w io.Writer, format, input, output, textModel, ttsModel, mode string, chapters []chapterInfo) error {
	if format == "json" {
		type chapterJSON struct {
			Index     int    `json:"index"`
			Title     string `json:"title"`
			StartPage int    `json:"start_page,omitempty"`
			EndPage   int    `json:"end_page,omitempty"`
		}
		var chaps []chapterJSON
		for _, ch := range chapters {
			c := chapterJSON{Index: ch.Index, Title: ch.Title}
			if ch.StartPage > 0 {
				c.StartPage = ch.StartPage
			}
			if ch.EndPage > 0 {
				c.EndPage = ch.EndPage
			}
			chaps = append(chaps, c)
		}
		out := struct {
			Tool      string        `json:"tool"`
			Input     string        `json:"input"`
			Output    string        `json:"output"`
			Mode      string        `json:"mode"`
			TextModel string        `json:"text_model"`
			TTSModel  string        `json:"tts_model"`
			Chapters  []chapterJSON `json:"chapters"`
		}{
			Tool:      "jeep-audiobook",
			Input:     input,
			Output:    output,
			Mode:      mode,
			TextModel: textModel,
			TTSModel:  ttsModel,
			Chapters:  chaps,
		}
		enc := json.NewEncoder(w)
		enc.SetIndent("", "  ")
		return enc.Encode(out)
	}

	// Text format.
	for _, ch := range chapters {
		if ch.StartPage > 0 && ch.EndPage > 0 {
			fmt.Fprintf(w, "Chapter %d: %s (pages %d-%d)\n", ch.Index, ch.Title, ch.StartPage, ch.EndPage)
		} else {
			fmt.Fprintf(w, "Chapter %d: %s\n", ch.Index, ch.Title)
		}
	}
	fmt.Fprintf(w, "Planned %d chapter(s) from %s\n", len(chapters), input)
	fmt.Fprintf(w, "Mode:         %s\n", mode)
	fmt.Fprintf(w, "Text model:   %s\n", textModel)
	fmt.Fprintf(w, "TTS model:    %s\n", ttsModel)
	fmt.Fprintf(w, "Output dir:   %s\n", output)
	return nil
}

func printUsage(fs *flag.FlagSet, w io.Writer) {
	fmt.Fprint(w, `Usage: jeep-audiobook [flags] <input-file>

Generate an audiobook from a PDF or ePub using Gemini TTS via Vertex AI.

Flags:
`)
	fs.PrintDefaults()
	fmt.Fprint(w, `
Examples:
  jeep-audiobook book.pdf
  jeep-audiobook -v Puck --chapters 1,3-5 book.epub -o output
  jeep-audiobook --dry-run --from-page 10 --to-page 50 book.pdf
  jeep-audiobook --resume book.pdf
`)
}
