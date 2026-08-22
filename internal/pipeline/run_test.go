package pipeline

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"math/rand"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"google.golang.org/genai"

	"github.com/tvaroska/jeep/internal/audio"
	"github.com/tvaroska/jeep/internal/describe"
	"github.com/tvaroska/jeep/internal/ingest"
)

// fakeSource implements ingest.Source, returning a canned Document.
type fakeSource struct {
	doc *ingest.Document
	err error
}

func (f *fakeSource) Load(path string) (*ingest.Document, error) {
	if f.err != nil {
		return nil, f.err
	}
	return f.doc, nil
}

// fakeCache is an in-memory Cache for testing, with call counters and optional
// forced errors to exercise the non-fatal read/write paths.
type fakeCache struct {
	mu     sync.Mutex // guards store/gets/puts under the concurrent segment pool
	store  map[string][]byte
	gets   int
	puts   int
	getErr error
	putErr error
}

func newFakeCache() *fakeCache {
	return &fakeCache{store: map[string][]byte{}}
}

func (f *fakeCache) Get(ns, key string) ([]byte, bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.gets++
	if f.getErr != nil {
		return nil, false, f.getErr
	}
	data, ok := f.store[ns+"/"+key]
	if !ok {
		return nil, false, nil
	}
	return data, true, nil
}

func (f *fakeCache) Put(ns, key string, data []byte) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.puts++
	if f.putErr != nil {
		return f.putErr
	}
	f.store[ns+"/"+key] = data
	return nil
}

func (f *fakeCache) putCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.puts
}

// countingDescriber implements describe.Describer, returning a fixed non-empty
// string and counting calls (so a resume can prove Weave was skipped).
type countingDescriber struct {
	mu    sync.Mutex // guards calls under the concurrent Weave pool
	calls int
}

func (c *countingDescriber) Describe(ctx context.Context, img []byte, mimeType, _ string) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	c.mu.Lock()
	c.calls++
	c.mu.Unlock()
	return "desc", nil
}

// countingPreparer implements describe.Preparer, returning the trimmed input and
// counting calls (so a resume can prove Prepare was skipped).
type countingPreparer struct{ calls int }

func (c *countingPreparer) Prepare(ctx context.Context, text string) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	c.calls++
	return strings.TrimSpace(text), nil
}

// concatCall records one Concat invocation.
type concatCall struct {
	out      string
	segments [][]byte
}

// fakeConcat implements audio.Concatenator, recording every call.
type fakeConcat struct {
	calls []concatCall
	err   error
}

func (f *fakeConcat) Concat(ctx context.Context, segments [][]byte, outPath string) error {
	f.calls = append(f.calls, concatCall{out: outPath, segments: segments})
	if f.err != nil {
		return f.err
	}
	// Write a sentinel file so the path is real.
	if err := os.MkdirAll(filepath.Dir(outPath), 0o755); err != nil {
		return err
	}
	return os.WriteFile(outPath, []byte("fake-audio"), 0o644)
}

// joined returns all segment bytes of the given recorded call concatenated.
func (c concatCall) joined() string {
	var b []byte
	for _, s := range c.segments {
		b = append(b, s...)
	}
	return string(b)
}

// fakeSynth implements audio.Synthesizer for testing.
type fakeSynth struct {
	mu        sync.Mutex // guards callCount under the concurrent segment pool
	callCount int
	errOn     int // return error on Nth call (1-based), 0 = never
}

func (f *fakeSynth) Synthesize(ctx context.Context, text string) ([]byte, error) {
	f.mu.Lock()
	f.callCount++
	n := f.callCount
	f.mu.Unlock()
	if f.errOn > 0 && n == f.errOn {
		return nil, fmt.Errorf("fake synth error on call %d", n)
	}
	return []byte("fake-audio-" + text), nil
}

// fakeProber implements pipeline.Prober for testing.
type fakeProber struct {
	dur time.Duration
	err error
}

func (f *fakeProber) Duration(ctx context.Context, path string) (time.Duration, error) {
	return f.dur, f.err
}

// newTestRunner builds a Runner with offline stubs for the describe seams.
func newTestRunner(src ingest.Source, synth audio.Synthesizer, cat audio.Concatenator, prober Prober) *Runner {
	return &Runner{
		Source:       src,
		Weaver:       &describe.Weaver{Describer: describe.StubDescriber{}},
		Preparer:     describe.StubPreparer{},
		Synthesizer:  synth,
		Concatenator: cat,
		Prober:       prober,
	}
}

// readManifest reads and parses a manifest.json at path.
func readManifest(t *testing.T, path string) Manifest {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read manifest: %v", err)
	}
	var m Manifest
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatalf("unmarshal manifest: %v", err)
	}
	return m
}

// twoChapterDoc returns a Document that Structure splits into exactly 2 chapters,
// with a prose block on each and one figure on chapter 1.
func twoChapterDoc(t *testing.T) *ingest.Document {
	t.Helper()
	doc := &ingest.Document{
		Meta: ingest.Meta{Path: "test.pdf"},
		Sections: []*ingest.Section{
			{Title: "Chapter 1", Level: 1, Blocks: []ingest.Block{
				{Kind: ingest.BlockProse, Text: "This is the first chapter. It has content.", Page: 1},
				{Kind: ingest.BlockFigure, Data: []byte("PNGDATA"), MimeType: "image/png", Page: 1},
			}},
			{Title: "Chapter 2", Level: 1, Blocks: []ingest.Block{
				{Kind: ingest.BlockProse, Text: "This is the second chapter. More content here.", Page: 2},
			}},
		},
	}
	if chapters := ingest.Structure(doc); len(chapters) != 2 {
		t.Fatalf("fixture setup: expected 2 chapters, got %d", len(chapters))
	}
	return doc
}

func TestRunner_HappyPath_MultiChapterWithImage(t *testing.T) {
	doc := twoChapterDoc(t)
	cat := &fakeConcat{}
	synth := audio.StubSynthesizer{}
	prober := &fakeProber{dur: 42 * time.Second}

	runner := newTestRunner(&fakeSource{doc: doc}, synth, cat, prober)

	outDir := t.TempDir()
	manifestPath, err := runner.Run(context.Background(), Options{InputPath: "test.pdf", OutputDir: outDir})
	if err != nil {
		t.Fatalf("Run failed: %v", err)
	}

	// (a) returns .../manifest.json and the file exists.
	wantManifest := filepath.Join(outDir, "manifest.json")
	if manifestPath != wantManifest {
		t.Errorf("manifest path: got %q, want %q", manifestPath, wantManifest)
	}
	if _, err := os.Stat(manifestPath); err != nil {
		t.Errorf("manifest file missing: %v", err)
	}

	// (b) two chapter files requested via fakeConcat.
	if len(cat.calls) != 2 {
		t.Fatalf("concat calls: got %d, want 2", len(cat.calls))
	}
	if got := filepath.Base(cat.calls[0].out); got != "chapter_01.mp3" {
		t.Errorf("first concat file: got %q, want chapter_01.mp3", got)
	}
	if got := filepath.Base(cat.calls[1].out); got != "chapter_02.mp3" {
		t.Errorf("second concat file: got %q, want chapter_02.mp3", got)
	}

	// (c) manifest has 2 entries with correct fields.
	m := readManifest(t, manifestPath)
	if m.Source != "test.pdf" {
		t.Errorf("manifest source: got %q, want test.pdf", m.Source)
	}
	if len(m.Chapters) != 2 {
		t.Fatalf("manifest chapters: got %d, want 2", len(m.Chapters))
	}
	for i, e := range m.Chapters {
		if e.Title == "" {
			t.Errorf("chapter %d has empty title", i)
		}
		if e.DurationSeconds != 42 {
			t.Errorf("chapter %d duration: got %v, want 42", i, e.DurationSeconds)
		}
	}
	if m.Chapters[0].StartPage != 1 || m.Chapters[0].EndPage != 1 {
		t.Errorf("chapter 0 pages: got [%d,%d], want [1,1]", m.Chapters[0].StartPage, m.Chapters[0].EndPage)
	}
	if m.Chapters[1].StartPage != 2 || m.Chapters[1].EndPage != 2 {
		t.Errorf("chapter 1 pages: got [%d,%d], want [2,2]", m.Chapters[1].StartPage, m.Chapters[1].EndPage)
	}
	if m.Chapters[0].File != "chapter_01.mp3" || m.Chapters[1].File != "chapter_02.mp3" {
		t.Errorf("manifest File basenames wrong: %q, %q", m.Chapters[0].File, m.Chapters[1].File)
	}

	// (d) chapter 1 bytes contain the stub image-description marker (weaving ran);
	// chapter 2 bytes do not.
	if !contains(cat.calls[0].joined(), "[image description:") {
		t.Errorf("chapter 1 concat bytes missing image marker: %q", cat.calls[0].joined())
	}
	if contains(cat.calls[1].joined(), "[image description:") {
		t.Errorf("chapter 2 concat bytes unexpectedly contain image marker: %q", cat.calls[1].joined())
	}
}

func TestRunner_TitleFallback(t *testing.T) {
	// A single untitled section yields one chapter with an empty title; the
	// manifest entry must fall back to "Chapter 1".
	doc := &ingest.Document{
		Meta: ingest.Meta{Path: "plain.pdf"},
		Sections: []*ingest.Section{
			{Title: "", Level: 1, Blocks: []ingest.Block{
				{Kind: ingest.BlockProse, Text: "Some plain narration text here. It has sentences."},
			}},
		},
	}
	chapters := ingest.Structure(doc)
	if len(chapters) != 1 || chapters[0].Title != "" {
		t.Fatalf("fixture setup: expected 1 untitled chapter, got %+v", chapters)
	}

	runner := newTestRunner(&fakeSource{doc: doc}, audio.StubSynthesizer{}, &fakeConcat{}, &fakeProber{dur: time.Second})
	manifestPath, err := runner.Run(context.Background(), Options{InputPath: "plain.pdf", OutputDir: t.TempDir()})
	if err != nil {
		t.Fatalf("Run failed: %v", err)
	}
	m := readManifest(t, manifestPath)
	if len(m.Chapters) != 1 {
		t.Fatalf("chapters: got %d, want 1", len(m.Chapters))
	}
	if m.Chapters[0].Title != "Chapter 1" {
		t.Errorf("title fallback: got %q, want %q", m.Chapters[0].Title, "Chapter 1")
	}
	// Page-less content emits 0 page span.
	if m.Chapters[0].StartPage != 0 || m.Chapters[0].EndPage != 0 {
		t.Errorf("page-less span: got [%d,%d], want [0,0]", m.Chapters[0].StartPage, m.Chapters[0].EndPage)
	}
}

func TestRunner_EmptyChapterSkip(t *testing.T) {
	// Section 1 is blank front matter (no usable text, no images); the first real
	// chapter is section 2. Only one file + one manifest entry should be produced.
	doc := &ingest.Document{
		Meta: ingest.Meta{Path: "test.pdf"},
		Sections: []*ingest.Section{
			{Title: "Front Matter", Level: 1, Blocks: []ingest.Block{
				{Kind: ingest.BlockProse, Text: "   \n\n  "},
			}},
			{Title: "Chapter 1", Level: 1, Blocks: []ingest.Block{
				{Kind: ingest.BlockProse, Text: "Real content in this chapter. More sentences here.", Page: 2},
			}},
		},
	}
	chapters := ingest.Structure(doc)
	if len(chapters) != 2 {
		t.Fatalf("fixture setup: expected 2 chapters, got %+v", chapters)
	}

	cat := &fakeConcat{}
	runner := newTestRunner(&fakeSource{doc: doc}, audio.StubSynthesizer{}, cat, &fakeProber{dur: time.Second})
	manifestPath, err := runner.Run(context.Background(), Options{InputPath: "test.pdf", OutputDir: t.TempDir()})
	if err != nil {
		t.Fatalf("Run failed: %v", err)
	}
	if len(cat.calls) != 1 {
		t.Fatalf("concat calls: got %d, want 1 (empty chapter skipped)", len(cat.calls))
	}
	if got := filepath.Base(cat.calls[0].out); got != "chapter_01.mp3" {
		t.Errorf("concat file: got %q, want chapter_01.mp3", got)
	}
	m := readManifest(t, manifestPath)
	if len(m.Chapters) != 1 {
		t.Fatalf("manifest chapters: got %d, want 1", len(m.Chapters))
	}
	// The produced entry is the real (second) chapter, spanning page 2.
	if m.Chapters[0].StartPage != 2 || m.Chapters[0].EndPage != 2 {
		t.Errorf("entry pages: got [%d,%d], want [2,2]", m.Chapters[0].StartPage, m.Chapters[0].EndPage)
	}
}

func TestRunner_AllChaptersEmpty(t *testing.T) {
	doc := &ingest.Document{
		Meta: ingest.Meta{Path: "blank.pdf"},
		Sections: []*ingest.Section{
			{Title: "A", Level: 1, Blocks: []ingest.Block{{Kind: ingest.BlockProse, Text: "   \n\n   "}}},
			{Title: "B", Level: 1, Blocks: []ingest.Block{{Kind: ingest.BlockProse, Text: "\t\t\n"}}},
		},
	}
	cat := &fakeConcat{}
	runner := newTestRunner(&fakeSource{doc: doc}, audio.StubSynthesizer{}, cat, &fakeProber{})
	outDir := t.TempDir()
	_, err := runner.Run(context.Background(), Options{InputPath: "blank.pdf", OutputDir: outDir})
	if err == nil {
		t.Fatal("expected error for blank document, got nil")
	}
	if !contains(err.Error(), "no extractable text") {
		t.Errorf("expected 'no extractable text' error, got: %v", err)
	}
	if len(cat.calls) != 0 {
		t.Errorf("concat should not have been called, got %d calls", len(cat.calls))
	}
	if _, err := os.Stat(filepath.Join(outDir, "manifest.json")); err == nil {
		t.Error("manifest should not have been written for blank document")
	}
}

func TestRunner_LoadError(t *testing.T) {
	runner := newTestRunner(&fakeSource{err: fmt.Errorf("fake load error")}, audio.StubSynthesizer{}, &fakeConcat{}, &fakeProber{})
	_, err := runner.Run(context.Background(), Options{InputPath: "x.pdf", OutputDir: t.TempDir()})
	if err == nil {
		t.Fatal("expected error from load, got nil")
	}
	if !contains(err.Error(), "load") {
		t.Errorf("expected 'load' error, got: %v", err)
	}
}

func longTextDoc(path string) *ingest.Document {
	longText := ""
	for i := 0; i < 50; i++ {
		longText += fmt.Sprintf("This is sentence number %d with some additional content to make it longer. ", i)
	}
	return &ingest.Document{
		Meta:     ingest.Meta{Path: path},
		Sections: []*ingest.Section{{Title: "Ch", Level: 1, Blocks: []ingest.Block{{Kind: ingest.BlockProse, Text: longText}}}},
	}
}

func TestRunner_SynthesizeError(t *testing.T) {
	doc := longTextDoc("test.pdf")
	cat := &fakeConcat{}
	synth := &fakeSynth{errOn: 1}
	runner := newTestRunner(&fakeSource{doc: doc}, synth, cat, &fakeProber{})
	outDir := t.TempDir()
	_, err := runner.Run(context.Background(), Options{InputPath: "test.pdf", OutputDir: outDir})
	if err == nil {
		t.Fatal("expected error from synthesize, got nil")
	}
	if !contains(err.Error(), "chapter") || !contains(err.Error(), "segment") {
		t.Errorf("expected error mentioning chapter and segment, got: %v", err)
	}
	if len(cat.calls) != 0 {
		t.Error("concat should not have been called after synth error")
	}
	if _, err := os.Stat(filepath.Join(outDir, "manifest.json")); err == nil {
		t.Error("manifest should not have been written after synth error")
	}
}

func TestRunner_ConcatError(t *testing.T) {
	doc := &ingest.Document{
		Meta:     ingest.Meta{Path: "test.pdf"},
		Sections: []*ingest.Section{{Title: "Ch", Level: 1, Blocks: []ingest.Block{{Kind: ingest.BlockProse, Text: "Some text here."}}}},
	}
	cat := &fakeConcat{err: fmt.Errorf("fake concat error")}
	runner := newTestRunner(&fakeSource{doc: doc}, audio.StubSynthesizer{}, cat, &fakeProber{})
	_, err := runner.Run(context.Background(), Options{InputPath: "test.pdf", OutputDir: t.TempDir()})
	if err == nil {
		t.Fatal("expected error from concat, got nil")
	}
	if !contains(err.Error(), "concat") {
		t.Errorf("expected concat error, got: %v", err)
	}
}

func TestRunner_ProberErrorNonFatal(t *testing.T) {
	doc := &ingest.Document{
		Meta:     ingest.Meta{Path: "test.pdf"},
		Sections: []*ingest.Section{{Title: "Ch", Level: 1, Blocks: []ingest.Block{{Kind: ingest.BlockProse, Text: "Some narration text. It has content."}}}},
	}
	cat := &fakeConcat{}
	prober := &fakeProber{err: fmt.Errorf("fake probe error")}
	runner := newTestRunner(&fakeSource{doc: doc}, audio.StubSynthesizer{}, cat, prober)
	manifestPath, err := runner.Run(context.Background(), Options{InputPath: "test.pdf", OutputDir: t.TempDir()})
	if err != nil {
		t.Fatalf("Run should succeed despite probe error, got: %v", err)
	}
	m := readManifest(t, manifestPath)
	if len(m.Chapters) != 1 {
		t.Fatalf("manifest chapters: got %d, want 1", len(m.Chapters))
	}
	if m.Chapters[0].DurationSeconds != 0 {
		t.Errorf("duration on probe error: got %v, want 0", m.Chapters[0].DurationSeconds)
	}
}

func TestRunner_ContextCancel(t *testing.T) {
	doc := &ingest.Document{
		Meta:     ingest.Meta{Path: "test.pdf"},
		Sections: []*ingest.Section{{Title: "Ch", Level: 1, Blocks: []ingest.Block{{Kind: ingest.BlockProse, Text: "First. Second. Third. Fourth. Fifth."}}}},
	}
	synth := &fakeSynth{}
	runner := newTestRunner(&fakeSource{doc: doc}, synth, &fakeConcat{}, &fakeProber{})

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := runner.Run(ctx, Options{InputPath: "test.pdf", OutputDir: t.TempDir()})
	if err == nil {
		t.Fatal("expected context error, got nil")
	}
	if err != context.Canceled {
		t.Errorf("expected context.Canceled, got: %v", err)
	}
	if synth.callCount > 1 {
		t.Errorf("synth should not have been called many times with cancelled context, got %d calls", synth.callCount)
	}
}

// newCachingRunner builds a Runner wired with counting describe/prepare fakes and
// the given cache, so resume tests can assert which expensive stages ran.
func newCachingRunner(doc *ingest.Document, cd *countingDescriber, cp *countingPreparer, synth audio.Synthesizer, cat audio.Concatenator, cache Cache) *Runner {
	return &Runner{
		Source:         &fakeSource{doc: doc},
		Weaver:         &describe.Weaver{Describer: cd},
		Preparer:       cp,
		Synthesizer:    synth,
		Concatenator:   cat,
		Prober:         &fakeProber{dur: 42 * time.Second},
		Cache:          cache,
		GeminiModel:    "g",
		TTSFingerprint: "m|v|s",
	}
}

func TestRunner_ResumeSkipsAllWork(t *testing.T) {
	doc := twoChapterDoc(t)
	cache := newFakeCache()
	outDir := t.TempDir()

	// Run #1: everything is a miss; expensive stages run and the cache fills.
	cd1, cp1 := &countingDescriber{}, &countingPreparer{}
	synth1 := &fakeSynth{}
	r1 := newCachingRunner(doc, cd1, cp1, synth1, &fakeConcat{}, cache)
	if _, err := r1.Run(context.Background(), Options{InputPath: "test.pdf", OutputDir: outDir}); err != nil {
		t.Fatalf("run #1 failed: %v", err)
	}
	if synth1.callCount == 0 || cd1.calls == 0 || cp1.calls == 0 {
		t.Fatalf("run #1 should exercise all stages: synth=%d desc=%d prep=%d", synth1.callCount, cd1.calls, cp1.calls)
	}
	if cache.putCount() == 0 {
		t.Fatal("run #1 should have populated the cache")
	}
	putsAfterRun1 := cache.putCount()

	// Run #2: fresh fakes, same cache + outDir (chapter mp3 sentinels still exist).
	// The chapter-completion short-circuit must skip synth+concat+probe entirely,
	// and no new puts should occur.
	cd2, cp2 := &countingDescriber{}, &countingPreparer{}
	synth2 := &fakeSynth{}
	cat2 := &fakeConcat{}
	r2 := newCachingRunner(doc, cd2, cp2, synth2, cat2, cache)
	manifestPath, err := r2.Run(context.Background(), Options{InputPath: "test.pdf", OutputDir: outDir})
	if err != nil {
		t.Fatalf("run #2 failed: %v", err)
	}
	if synth2.callCount != 0 {
		t.Errorf("run #2 synth calls: got %d, want 0 (chapter short-circuit)", synth2.callCount)
	}
	if cd2.calls != 0 || cp2.calls != 0 {
		t.Errorf("run #2 describe/prepare calls: got desc=%d prep=%d, want 0 (narration cached)", cd2.calls, cp2.calls)
	}
	if len(cat2.calls) != 0 {
		t.Errorf("run #2 concat calls: got %d, want 0 (chapter short-circuit)", len(cat2.calls))
	}
	if grew := cache.putCount() - putsAfterRun1; grew != 0 {
		t.Errorf("run #2 cache puts grew by %d, want 0 (everything cached)", grew)
	}
	m := readManifest(t, manifestPath)
	if len(m.Chapters) != 2 {
		t.Fatalf("run #2 manifest chapters: got %d, want 2", len(m.Chapters))
	}
	for i, e := range m.Chapters {
		if e.DurationSeconds != 42 {
			t.Errorf("run #2 chapter %d duration: got %v, want 42 (from record)", i, e.DurationSeconds)
		}
	}
}

func TestRunner_ResumeHitsSegmentAndNarrationCacheWhenMP3Gone(t *testing.T) {
	doc := twoChapterDoc(t)
	cache := newFakeCache()
	outDir := t.TempDir()

	cd1, cp1 := &countingDescriber{}, &countingPreparer{}
	synth1 := &fakeSynth{}
	r1 := newCachingRunner(doc, cd1, cp1, synth1, &fakeConcat{}, cache)
	if _, err := r1.Run(context.Background(), Options{InputPath: "test.pdf", OutputDir: outDir}); err != nil {
		t.Fatalf("run #1 failed: %v", err)
	}

	// Delete the chapter mp3s so the completion short-circuit misses (mp3 gone),
	// but narration + segment caches still hit.
	for _, name := range []string{"chapter_01.mp3", "chapter_02.mp3"} {
		if err := os.Remove(filepath.Join(outDir, name)); err != nil {
			t.Fatalf("remove %s: %v", name, err)
		}
	}

	cd2, cp2 := &countingDescriber{}, &countingPreparer{}
	synth2 := &fakeSynth{}
	cat2 := &fakeConcat{}
	r2 := newCachingRunner(doc, cd2, cp2, synth2, cat2, cache)
	if _, err := r2.Run(context.Background(), Options{InputPath: "test.pdf", OutputDir: outDir}); err != nil {
		t.Fatalf("run #2 failed: %v", err)
	}
	if cd2.calls != 0 || cp2.calls != 0 {
		t.Errorf("run #2 describe/prepare: got desc=%d prep=%d, want 0 (narration cached)", cd2.calls, cp2.calls)
	}
	if synth2.callCount != 0 {
		t.Errorf("run #2 synth calls: got %d, want 0 (segment cache)", synth2.callCount)
	}
	if len(cat2.calls) != 2 {
		t.Errorf("run #2 concat calls: got %d, want 2 (mp3 rebuilt)", len(cat2.calls))
	}
}

func TestRunner_CacheReadErrorNonFatal(t *testing.T) {
	doc := twoChapterDoc(t)
	cache := newFakeCache()
	cache.getErr = fmt.Errorf("boom read")

	cd, cp := &countingDescriber{}, &countingPreparer{}
	synth := &fakeSynth{}
	r := newCachingRunner(doc, cd, cp, synth, &fakeConcat{}, cache)
	if _, err := r.Run(context.Background(), Options{InputPath: "test.pdf", OutputDir: t.TempDir()}); err != nil {
		t.Fatalf("Run should succeed despite cache read error, got: %v", err)
	}
	// A read error is treated as a miss, so the real stages must run.
	if synth.callCount == 0 || cp.calls == 0 {
		t.Errorf("stages should run on cache read error: synth=%d prep=%d", synth.callCount, cp.calls)
	}
}

func TestRunner_CacheWriteErrorNonFatal(t *testing.T) {
	doc := twoChapterDoc(t)
	cache := newFakeCache()
	cache.putErr = fmt.Errorf("boom write")

	cd, cp := &countingDescriber{}, &countingPreparer{}
	synth := &fakeSynth{}
	r := newCachingRunner(doc, cd, cp, synth, &fakeConcat{}, cache)
	if _, err := r.Run(context.Background(), Options{InputPath: "test.pdf", OutputDir: t.TempDir()}); err != nil {
		t.Fatalf("Run should succeed despite cache write error, got: %v", err)
	}
}

// orderSynth returns audio deterministically derived from the input text and
// sleeps a small random duration so completion order is shuffled — proving the
// worker pool reassembles segments in reading order, not completion order.
type orderSynth struct{}

func (orderSynth) Synthesize(ctx context.Context, text string) ([]byte, error) {
	time.Sleep(time.Duration(rand.Intn(3)) * time.Millisecond)
	return []byte("audio:" + text), nil
}

func TestRunner_ConcurrentSegmentsPreserveOrder(t *testing.T) {
	doc := longTextDoc("test.pdf")
	longText := doc.Sections[0].Blocks[0].Text
	cat := &fakeConcat{}
	runner := newTestRunner(&fakeSource{doc: doc}, orderSynth{}, cat, &fakeProber{})

	if _, err := runner.Run(context.Background(), Options{InputPath: "test.pdf", OutputDir: t.TempDir()}); err != nil {
		t.Fatalf("Run failed: %v", err)
	}
	if len(cat.calls) != 1 {
		t.Fatalf("concat calls: got %d, want 1", len(cat.calls))
	}

	// The stub Weaver/Preparer pass the text through unchanged, so the segments the
	// runner synthesized are exactly Segmenter.Segment(longText). The recorded audio
	// bytes must map back to that same order.
	want := (&audio.Segmenter{}).Segment(longText)
	if len(want) < 2 {
		t.Fatalf("fixture setup: expected many segments, got %d", len(want))
	}
	got := cat.calls[0].segments
	if len(got) != len(want) {
		t.Fatalf("segment count: got %d, want %d", len(got), len(want))
	}
	for i := range want {
		if string(got[i]) != "audio:"+want[i] {
			t.Errorf("segment %d out of order: got %q, want %q", i, got[i], "audio:"+want[i])
		}
	}
}

// boundedSynth tracks concurrent in-flight calls and records the max observed,
// so the pool's SetLimit can be asserted.
type boundedSynth struct {
	inFlight int32
	maxSeen  int32
}

func (b *boundedSynth) Synthesize(ctx context.Context, text string) ([]byte, error) {
	n := atomic.AddInt32(&b.inFlight, 1)
	for {
		old := atomic.LoadInt32(&b.maxSeen)
		if n <= old || atomic.CompareAndSwapInt32(&b.maxSeen, old, n) {
			break
		}
	}
	time.Sleep(5 * time.Millisecond)
	atomic.AddInt32(&b.inFlight, -1)
	return []byte("fake-audio-" + text), nil
}

func TestRunner_ConcurrencyIsBounded(t *testing.T) {
	doc := longTextDoc("test.pdf")
	synth := &boundedSynth{}
	runner := newTestRunner(&fakeSource{doc: doc}, synth, &fakeConcat{}, &fakeProber{})
	runner.Concurrency = 2

	if _, err := runner.Run(context.Background(), Options{InputPath: "test.pdf", OutputDir: t.TempDir()}); err != nil {
		t.Fatalf("Run failed: %v", err)
	}
	if max := atomic.LoadInt32(&synth.maxSeen); max > 2 {
		t.Errorf("max in-flight synth calls: got %d, want <= 2", max)
	}
	if max := atomic.LoadInt32(&synth.maxSeen); max < 2 {
		t.Errorf("max in-flight synth calls: got %d, want the pool to reach its limit of 2", max)
	}
}

func TestRunner_Progress(t *testing.T) {
	doc := twoChapterDoc(t)
	cat := &fakeConcat{}
	runner := newTestRunner(&fakeSource{doc: doc}, audio.StubSynthesizer{}, cat, &fakeProber{dur: time.Second})

	var msgs []string
	runner.Progress = func(msg string) {
		msgs = append(msgs, msg)
	}

	if _, err := runner.Run(context.Background(), Options{InputPath: "test.pdf", OutputDir: t.TempDir()}); err != nil {
		t.Fatalf("Run failed: %v", err)
	}
	if len(msgs) == 0 {
		t.Fatal("expected at least one progress message, got none")
	}
	found := false
	for _, msg := range msgs {
		if contains(msg, "[1/") {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected a progress message containing '[1/', got: %v", msgs)
	}
}

func TestRunner_ChapterSelection(t *testing.T) {
	doc := twoChapterDoc(t)
	cat := &fakeConcat{}
	runner := newTestRunner(&fakeSource{doc: doc}, audio.StubSynthesizer{}, cat, &fakeProber{dur: time.Second})

	manifestPath, err := runner.Run(context.Background(), Options{
		InputPath: "test.pdf",
		OutputDir: t.TempDir(),
		Chapters:  []int{2},
	})
	if err != nil {
		t.Fatalf("Run failed: %v", err)
	}

	// Exactly one concat call (second chapter only), numbered by produced counter.
	if len(cat.calls) != 1 {
		t.Fatalf("concat calls: got %d, want 1", len(cat.calls))
	}
	if got := filepath.Base(cat.calls[0].out); got != "chapter_01.mp3" {
		t.Errorf("concat file: got %q, want chapter_01.mp3 (produced-counter numbering)", got)
	}

	m := readManifest(t, manifestPath)
	if len(m.Chapters) != 1 {
		t.Fatalf("manifest chapters: got %d, want 1", len(m.Chapters))
	}
	if m.Chapters[0].StartPage != 2 || m.Chapters[0].EndPage != 2 {
		t.Errorf("manifest entry pages: got [%d,%d], want [2,2]", m.Chapters[0].StartPage, m.Chapters[0].EndPage)
	}
}

func TestSelectChapters(t *testing.T) {
	chapters := []ingest.Chapter{
		{Title: "Ch 1"},
		{Title: "Ch 2"},
		{Title: "Ch 3"},
	}

	tests := []struct {
		name    string
		opts    Options
		wantN   int
		wantErr bool
	}{
		{"identity when no filters", Options{}, 3, false},
		{"index subset", Options{Chapters: []int{1, 3}}, 2, false},
		{"single index", Options{Chapters: []int{2}}, 1, false},
		{"error on out-of-range index", Options{Chapters: []int{4}}, 0, true},
		{"error on zero index", Options{Chapters: []int{0}}, 0, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := selectChapters(chapters, tt.opts)
			if (err != nil) != tt.wantErr {
				t.Errorf("selectChapters() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if err != nil {
				return
			}
			if len(got) != tt.wantN {
				t.Fatalf("selectChapters() len = %d, want %d", len(got), tt.wantN)
			}
		})
	}
}

func TestRunner_ChapterIndexOutOfRange(t *testing.T) {
	doc := twoChapterDoc(t)
	runner := newTestRunner(&fakeSource{doc: doc}, audio.StubSynthesizer{}, &fakeConcat{}, &fakeProber{})
	_, err := runner.Run(context.Background(), Options{
		InputPath: "test.pdf",
		OutputDir: t.TempDir(),
		Chapters:  []int{99},
	})
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !contains(err.Error(), "out of range") {
		t.Errorf("expected 'out of range' error, got: %v", err)
	}
}

// oversizeSynth rejects any text longer than maxOK bytes with a Vertex 400
// (mirroring gemini-tts refusing token-dense segments), and synthesizes anything
// else. It records total calls and how many were rejected.
type oversizeSynth struct {
	mu       sync.Mutex
	maxOK    int
	calls    int
	rejected int
}

func (s *oversizeSynth) Synthesize(ctx context.Context, text string) ([]byte, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.calls++
	if len(text) > s.maxOK {
		s.rejected++
		return nil, fmt.Errorf("generating speech: %w", genai.APIError{Code: 400, Message: "oversized"})
	}
	return []byte("audio:" + text), nil
}

// TestSynthSegment_ResplitsOnOversizedInput is the regression test for the
// full-book crash: a segment gemini-tts rejects as oversized (Vertex 400) must be
// re-split and synthesized as smaller pieces, not abort the run.
func TestSynthSegment_ResplitsOnOversizedInput(t *testing.T) {
	// Build a multi-sentence text that exceeds maxOK but splits cleanly into
	// halves that each fit — driving exactly one segment->halves re-split.
	text := strings.TrimSpace(strings.Repeat("This is a fairly long sentence with several words. ", 8))
	synth := &oversizeSynth{maxOK: len(text) / 2}
	r := &Runner{Synthesizer: synth, TTSFingerprint: "m|v|s"}
	cache := newFakeCache()

	blobs, err := r.synthSegment(context.Background(), cache, testLogger(), text, 0)
	if err != nil {
		t.Fatalf("synthSegment returned error: %v", err)
	}
	if len(blobs) < 2 {
		t.Fatalf("expected multiple audio blobs from re-split, got %d", len(blobs))
	}
	if synth.rejected != 1 {
		t.Errorf("expected exactly one oversized rejection to drive the re-split, got %d", synth.rejected)
	}
	for i, b := range blobs {
		if len(b) == 0 {
			t.Errorf("blob %d is empty", i)
		}
	}
}

// TestSynthSegment_TransientNotResplit ensures a non-oversized (transient 503)
// error is surfaced immediately (re-split only helps oversized rejections).
func TestSynthSegment_TransientNotResplit(t *testing.T) {
	synth := &failSynth{err: fmt.Errorf("generating speech: %w", genai.APIError{Code: 503, Message: "unavailable"})}
	r := &Runner{Synthesizer: synth, TTSFingerprint: "m|v|s"}
	cache := newFakeCache()

	_, err := r.synthSegment(context.Background(), cache, testLogger(), "some text that is long enough to split into pieces if we wanted", 0)
	if err == nil {
		t.Fatal("expected error to be surfaced, got nil")
	}
	if synth.calls != 1 {
		t.Errorf("calls = %d, want 1 (no re-split on non-oversized error)", synth.calls)
	}
}

// testLogger returns a logger that discards output, keeping test logs clean
// while exercising code paths that log.
func testLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

// failSynth always fails with a fixed error and counts calls.
type failSynth struct {
	mu    sync.Mutex
	err   error
	calls int
}

func (s *failSynth) Synthesize(ctx context.Context, _ string) ([]byte, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.calls++
	return nil, s.err
}

// contains reports whether s contains substr.
func contains(s, substr string) bool {
	return strings.Contains(s, substr)
}
