package describe

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"google.golang.org/genai"

	"github.com/tvaroska/jeep/internal/usage"
)

// TestWeave_TextOnlyNoImages confirms weaving is a no-op page-join when no images.
func TestWeave_TextOnlyNoImages(t *testing.T) {
	w := &Weaver{Describer: StubDescriber{}}
	pages := []PageContent{
		{Text: "Page one."},
		{Text: "Page two."},
	}

	got, err := w.Weave(context.Background(), pages)
	if err != nil {
		t.Fatalf("Weave returned error: %v", err)
	}

	want := "Page one.\n\nPage two."
	if got != want {
		t.Errorf("Weave() = %q, want %q", got, want)
	}
}

// TestWeave_SingleImageAfterPageText confirms a single image description is
// appended after the page text.
func TestWeave_SingleImageAfterPageText(t *testing.T) {
	w := &Weaver{Describer: StubDescriber{}}
	pages := []PageContent{
		{
			Text: "Intro.",
			Images: []ImageRef{
				{Data: testImg, MimeType: testMime},
			},
		},
	}

	got, err := w.Weave(context.Background(), pages)
	if err != nil {
		t.Fatalf("Weave returned error: %v", err)
	}

	introIdx := strings.Index(got, "Intro.")
	descIdx := strings.Index(got, "[image description")
	if introIdx < 0 {
		t.Error("result does not contain page text 'Intro.'")
	}
	if descIdx < 0 {
		t.Error("result does not contain stub description")
	}
	if introIdx >= descIdx {
		t.Errorf("page text at %d should come before description at %d", introIdx, descIdx)
	}
	if !strings.Contains(got, "\n\n") {
		t.Error("page text and description should be joined by \\n\\n")
	}
}

// TestWeave_MultipleImagesOrderedByTop confirms multiple images on one page are
// ordered by Top ascending and the caller's input slice is not mutated.
func TestWeave_MultipleImagesOrderedByTop(t *testing.T) {
	w := &Weaver{Describer: StubDescriber{}}

	// Input in NON-Top order with distinct Data lengths (so stub markers differ)
	inputImages := []ImageRef{
		{Data: make([]byte, 5), MimeType: testMime, Top: 200.0}, // middle
		{Data: make([]byte, 3), MimeType: testMime, Top: 100.0}, // first
		{Data: make([]byte, 7), MimeType: testMime, Top: 300.0}, // last
	}

	// Keep a copy of the original order to verify no mutation
	origOrder := make([]ImageRef, len(inputImages))
	copy(origOrder, inputImages)

	pages := []PageContent{
		{
			Text:   "One page, three images.",
			Images: inputImages,
		},
	}

	got, err := w.Weave(context.Background(), pages)
	if err != nil {
		t.Fatalf("Weave returned error: %v", err)
	}

	// Stub description markers: "3 bytes" (Top 100), "5 bytes" (Top 200), "7 bytes" (Top 300)
	idx3 := strings.Index(got, "3 bytes")
	idx5 := strings.Index(got, "5 bytes")
	idx7 := strings.Index(got, "7 bytes")

	if idx3 < 0 || idx5 < 0 || idx7 < 0 {
		t.Fatalf("result = %q, want all three stub markers", got)
	}
	if !(idx3 < idx5 && idx5 < idx7) {
		t.Errorf("descriptions not in Top-ascending order: 3 bytes at %d, 5 bytes at %d, 7 bytes at %d", idx3, idx5, idx7)
	}

	// Verify input was not mutated
	for i := range inputImages {
		if inputImages[i].Top != origOrder[i].Top || len(inputImages[i].Data) != len(origOrder[i].Data) {
			t.Errorf("input slice mutated at index %d", i)
		}
	}
}

// TestWeave_ImagesAcrossMultiplePages confirms images stay in page order.
func TestWeave_ImagesAcrossMultiplePages(t *testing.T) {
	w := &Weaver{Describer: StubDescriber{}}
	pages := []PageContent{
		{
			Text:   "Page one text.",
			Images: []ImageRef{{Data: make([]byte, 10), MimeType: testMime}},
		},
		{
			Text:   "Page two text.",
			Images: []ImageRef{{Data: make([]byte, 20), MimeType: testMime}},
		},
	}

	got, err := w.Weave(context.Background(), pages)
	if err != nil {
		t.Fatalf("Weave returned error: %v", err)
	}

	// Expected order: page 1 text, page 1 desc (10 bytes), page 2 text, page 2 desc (20 bytes)
	p1TextIdx := strings.Index(got, "Page one text.")
	p1DescIdx := strings.Index(got, "10 bytes")
	p2TextIdx := strings.Index(got, "Page two text.")
	p2DescIdx := strings.Index(got, "20 bytes")

	if !(p1TextIdx < p1DescIdx && p1DescIdx < p2TextIdx && p2TextIdx < p2DescIdx) {
		t.Errorf("wrong order: p1 text at %d, p1 desc at %d, p2 text at %d, p2 desc at %d",
			p1TextIdx, p1DescIdx, p2TextIdx, p2DescIdx)
	}
}

// TestWeave_EmptyInput confirms nil and empty slice return "" without error.
func TestWeave_EmptyInput(t *testing.T) {
	w := &Weaver{Describer: StubDescriber{}}

	tests := []struct {
		name  string
		pages []PageContent
	}{
		{"nil", nil},
		{"empty slice", []PageContent{}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := w.Weave(context.Background(), tt.pages)
			if err != nil {
				t.Fatalf("Weave returned error: %v", err)
			}
			if got != "" {
				t.Errorf("Weave() = %q, want empty string", got)
			}
		})
	}
}

// TestWeave_EmptyPageTextSkipped confirms empty page text is skipped but its
// image is still described, with no leading blank part.
func TestWeave_EmptyPageTextSkipped(t *testing.T) {
	w := &Weaver{Describer: StubDescriber{}}
	pages := []PageContent{
		{
			Text:   "  ",
			Images: []ImageRef{{Data: testImg, MimeType: testMime}},
		},
	}

	got, err := w.Weave(context.Background(), pages)
	if err != nil {
		t.Fatalf("Weave returned error: %v", err)
	}

	if !strings.Contains(got, "[image description") {
		t.Error("result should contain image description")
	}
	if strings.HasPrefix(got, "\n") {
		t.Error("result should not have leading newline from skipped empty text")
	}
	if strings.Contains(got, "\n\n\n") {
		t.Error("result should not have stray blank parts (triple newline)")
	}
}

// TestWeave_DescriberErrorFailsFast confirms Describer error is fail-fast and
// mentions the page number.
func TestWeave_DescriberErrorFailsFast(t *testing.T) {
	// Use a GeminiDescriber with fakeGenerator that returns a non-retryable error
	fake := &fakeGenerator{errs: []error{genai.APIError{Code: 400, Message: "bad request"}}}
	g := &GeminiDescriber{
		Client:  fake,
		Model:   testModel,
		Retries: 1,
	}
	w := &Weaver{Describer: g}

	pages := []PageContent{
		{
			Text:   "Some text.",
			Images: []ImageRef{{Data: testImg, MimeType: testMime}},
		},
	}

	got, err := w.Weave(context.Background(), pages)
	if err == nil {
		t.Fatal("Weave should return error when Describer fails")
	}
	if got != "" {
		t.Errorf("result should be empty on error, got %q", got)
	}
	if !strings.Contains(err.Error(), "page 1") {
		t.Errorf("error = %v, want it to mention page number", err)
	}
}

// TestWeave_ContextCancellation confirms pre-cancelled context returns error
// without panicking.
func TestWeave_ContextCancellation(t *testing.T) {
	w := &Weaver{Describer: StubDescriber{}}

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // pre-cancel

	pages := []PageContent{
		{
			Text:   "Some text.",
			Images: []ImageRef{{Data: testImg, MimeType: testMime}},
		},
	}

	_, err := w.Weave(ctx, pages)
	if !errors.Is(err, context.Canceled) {
		t.Errorf("Weave with cancelled context: err = %v, want context.Canceled", err)
	}
}

// TestWeave_AllEmptyPagesAndImages confirms a document with all-empty text and
// no images returns "" without error.
func TestWeave_AllEmptyPagesAndImages(t *testing.T) {
	w := &Weaver{Describer: StubDescriber{}}
	pages := []PageContent{
		{Text: "   "},
		{Text: ""},
		{Text: "\n\n"},
	}

	got, err := w.Weave(context.Background(), pages)
	if err != nil {
		t.Fatalf("Weave returned error: %v", err)
	}
	if got != "" {
		t.Errorf("Weave() = %q, want empty string for all-empty pages", got)
	}
}

// orderDescriber returns a description encoding the input length and sleeps a
// small random duration so completion order is shuffled.
type orderDescriber struct{}

func (orderDescriber) Describe(ctx context.Context, img []byte, _, _ string) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	time.Sleep(time.Millisecond)
	return fmt.Sprintf("desc-%d", len(img)), nil
}

// TestWeave_ConcurrentPreservesOrder confirms the worker pool reassembles image
// descriptions in reading (page, then Top-ascending) order.
func TestWeave_ConcurrentPreservesOrder(t *testing.T) {
	w := &Weaver{Describer: orderDescriber{}}

	// Distinct data lengths give unique "desc-N" markers. Reading order is:
	// page 1 (Top 1,2,3), page 2 (Top 1,2), page 3 (Top 1).
	pages := []PageContent{
		{Text: "Page one.", Images: []ImageRef{
			{Data: make([]byte, 3), Top: 30},
			{Data: make([]byte, 1), Top: 10},
			{Data: make([]byte, 2), Top: 20},
		}},
		{Text: "Page two.", Images: []ImageRef{
			{Data: make([]byte, 5), Top: 20},
			{Data: make([]byte, 4), Top: 10},
		}},
		{Text: "Page three.", Images: []ImageRef{
			{Data: make([]byte, 6), Top: 10},
		}},
	}

	got, err := w.Weave(context.Background(), pages)
	if err != nil {
		t.Fatalf("Weave returned error: %v", err)
	}

	// Expected marker order after Top sort within each page.
	wantOrder := []string{"desc-1", "desc-2", "desc-3", "desc-4", "desc-5", "desc-6"}
	prev := -1
	for _, marker := range wantOrder {
		idx := strings.Index(got, marker)
		if idx < 0 {
			t.Fatalf("result missing marker %q: %q", marker, got)
		}
		if idx <= prev {
			t.Errorf("marker %q out of order (idx %d <= prev %d): %q", marker, idx, prev, got)
		}
		prev = idx
	}
}

// boundedDescriber tracks concurrent in-flight calls and records the max seen.
type boundedDescriber struct {
	inFlight int32
	maxSeen  int32
}

func (b *boundedDescriber) Describe(ctx context.Context, img []byte, _, _ string) (string, error) {
	n := atomic.AddInt32(&b.inFlight, 1)
	for {
		old := atomic.LoadInt32(&b.maxSeen)
		if n <= old || atomic.CompareAndSwapInt32(&b.maxSeen, old, n) {
			break
		}
	}
	time.Sleep(5 * time.Millisecond)
	atomic.AddInt32(&b.inFlight, -1)
	return "desc", nil
}

// TestWeave_ConcurrencyIsBounded confirms the worker pool honors Weaver.Concurrency.
func TestWeave_ConcurrencyIsBounded(t *testing.T) {
	bd := &boundedDescriber{}
	w := &Weaver{Describer: bd, Concurrency: 2}

	var images []ImageRef
	for i := 0; i < 10; i++ {
		images = append(images, ImageRef{Data: make([]byte, i+1), Top: float64(i)})
	}
	pages := []PageContent{{Text: "Page.", Images: images}}

	if _, err := w.Weave(context.Background(), pages); err != nil {
		t.Fatalf("Weave returned error: %v", err)
	}
	if max := atomic.LoadInt32(&bd.maxSeen); max > 2 {
		t.Errorf("max in-flight describe calls: got %d, want <= 2", max)
	}
	if max := atomic.LoadInt32(&bd.maxSeen); max < 2 {
		t.Errorf("max in-flight describe calls: got %d, want the pool to reach its limit of 2", max)
	}
}

// newTestPreparer builds a GeminiPreparer with fast retry settings for tests.
func newTestPreparer(fake generator) *GeminiPreparer {
	return &GeminiPreparer{
		Client:  fake,
		Model:   testModel,
		Retries: 2,
	}
}

func TestPrepare_SuccessRequestShape(t *testing.T) {
	fake := &fakeGenerator{text: `{"narration":"rewritten prose."}`}
	g := newTestPreparer(fake)

	got, err := g.Prepare(context.Background(), "Table: A|B")
	if err != nil {
		t.Fatalf("Prepare returned error: %v", err)
	}
	if got != "rewritten prose." {
		t.Errorf("prepared = %q", got)
	}
	if fake.calls != 1 {
		t.Errorf("calls = %d, want 1", fake.calls)
	}
	if fake.lastModel != testModel {
		t.Errorf("model = %q, want %q", fake.lastModel, testModel)
	}
	if len(fake.lastContents) != 1 {
		t.Fatalf("contents len = %d, want 1", len(fake.lastContents))
	}
	parts := fake.lastContents[0].Parts
	if len(parts) != 1 {
		t.Fatalf("parts len = %d, want 1 (text-only, no image)", len(parts))
	}
	if parts[0].Text == "" {
		t.Error("text part is empty")
	}
	if !strings.Contains(parts[0].Text, "Table: A|B") {
		t.Errorf("text part = %q, want it to include input text", parts[0].Text)
	}
	if parts[0].InlineData != nil {
		t.Error("request should have NO image part (text-only)")
	}
	if fake.lastSystem == "" {
		t.Error("system prompt not set")
	}
	if fake.lastSchema == nil {
		t.Error("schema not set")
	}
}

func TestPrepare_EmptyInputNoOp(t *testing.T) {
	fake := &fakeGenerator{text: `{"narration":"should not be called"}`}
	g := newTestPreparer(fake)

	tests := []struct {
		name  string
		input string
	}{
		{"empty", ""},
		{"whitespace only", "   "},
		{"tabs and newlines", "\t\n\n"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fake.calls = 0 // reset

			got, err := g.Prepare(context.Background(), tt.input)
			if err != nil {
				t.Fatalf("Prepare returned error: %v", err)
			}
			if got != "" {
				t.Errorf("result = %q, want empty string", got)
			}
			if fake.calls != 0 {
				t.Errorf("calls = %d, want 0 (no API call for empty input)", fake.calls)
			}
		})
	}
}

func TestPrepare_OutputTrimmed(t *testing.T) {
	fake := &fakeGenerator{text: `{"narration":"  spaced.  "}`}
	g := newTestPreparer(fake)

	got, err := g.Prepare(context.Background(), "input")
	if err != nil {
		t.Fatalf("Prepare returned error: %v", err)
	}
	if got != "spaced." {
		t.Errorf("result = %q, want trimmed %q", got, "spaced.")
	}
}

func TestPrepare_Success(t *testing.T) {
	fake := &fakeGenerator{text: `{"narration":"ok"}`}
	g := newTestPreparer(fake)

	got, err := g.Prepare(context.Background(), "test input")
	if err != nil {
		t.Fatalf("Prepare returned error: %v", err)
	}
	if got != "ok" {
		t.Errorf("result = %q, want ok", got)
	}
	if fake.calls != 1 {
		t.Errorf("calls = %d, want 1", fake.calls)
	}
}

func TestPrepare_FatalNoRetry(t *testing.T) {
	fake := &fakeGenerator{errs: []error{genai.APIError{Code: 400, Message: "bad request"}}}
	g := newTestPreparer(fake)

	if _, err := g.Prepare(context.Background(), "input"); err == nil {
		t.Fatal("expected error, got nil")
	}
	if fake.calls != 1 {
		t.Errorf("calls = %d, want 1 (no retry on fatal)", fake.calls)
	}
}

func TestPrepare_EmptyResponse(t *testing.T) {
	fake := &fakeGenerator{text: `{"narration":""}`}
	g := newTestPreparer(fake)

	if _, err := g.Prepare(context.Background(), "non-empty input"); err == nil {
		t.Fatal("expected error for empty response")
	}
}

func TestPrepare_ContextCancelled(t *testing.T) {
	fake := &fakeGenerator{
		errs: []error{context.Canceled},
	}
	g := newTestPreparer(fake)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := g.Prepare(ctx, "input")
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("error = %v, want context.Canceled", err)
	}
}

// TestPrepare_ChunksLongInput confirms a chapter far larger than one output-token
// budget must be split into multiple prepare calls.
func TestPrepare_ChunksLongInput(t *testing.T) {
	fake := &fakeGenerator{text: `{"narration":"PREPARED"}`}
	g := newTestPreparer(fake)
	g.MaxInputChars = 100
	g.Concurrency = 1 // serialize: fakeGenerator is not concurrency-safe

	// 20 paragraphs of ~100 chars each => ~2000 chars, well over one 100-char chunk.
	var sb strings.Builder
	for i := 0; i < 20; i++ {
		sb.WriteString(fmt.Sprintf("Paragraph %02d ", i))
		sb.WriteString(strings.Repeat("word ", 17))
		sb.WriteString("\n\n")
	}

	got, err := g.Prepare(context.Background(), sb.String())
	if err != nil {
		t.Fatalf("Prepare returned error: %v", err)
	}
	if fake.calls < 2 {
		t.Fatalf("calls = %d, want >= 2 (long input must be chunked)", fake.calls)
	}
	// Output must contain one "PREPARED" per chunk call, joined in order.
	if n := strings.Count(got, "PREPARED"); n != fake.calls {
		t.Errorf("output has %d chunk results, want %d (one per call)", n, fake.calls)
	}
}

// TestPrepare_ShortInputSingleCall confirms text that fits in one chunk
// costs exactly one API call.
func TestPrepare_ShortInputSingleCall(t *testing.T) {
	fake := &fakeGenerator{text: `{"narration":"ok"}`}
	g := newTestPreparer(fake)
	g.MaxInputChars = 6000

	if _, err := g.Prepare(context.Background(), "A short paragraph that fits in one chunk."); err != nil {
		t.Fatalf("Prepare returned error: %v", err)
	}
	if fake.calls != 1 {
		t.Errorf("calls = %d, want 1 for short input", fake.calls)
	}
}

// TestPrepare_PromptContainsArtifactRepairGuidance asserts the exported prepare
// system prompt constant contains the artifact-repair instruction.
func TestPrepare_PromptContainsArtifactRepairGuidance(t *testing.T) {
	lower := strings.ToLower(defaultPrepareSystemPrompt)
	if !strings.Contains(lower, "ligature") && !strings.Contains(lower, "line") {
		t.Fatal("prepare system prompt is missing artifact-repair guidance (ligature/line-wrap language)")
	}
}

func TestChunkForPrepare(t *testing.T) {
	t.Run("short input is one chunk", func(t *testing.T) {
		got := chunkForPrepare("one\n\ntwo", 1000)
		if len(got) != 1 {
			t.Fatalf("chunks = %d, want 1: %q", len(got), got)
		}
		if got[0] != "one\n\ntwo" {
			t.Errorf("chunk = %q, want %q", got[0], "one\n\ntwo")
		}
	})

	t.Run("paragraphs split on boundary under limit", func(t *testing.T) {
		// Each paragraph is 10 chars; limit 25 => ~2 paragraphs per chunk.
		text := "aaaaaaaaaa\n\nbbbbbbbbbb\n\ncccccccccc\n\ndddddddddd"
		got := chunkForPrepare(text, 25)
		if len(got) < 2 {
			t.Fatalf("chunks = %d, want >= 2: %q", len(got), got)
		}
		for _, c := range got {
			if len(c) > 25 {
				t.Errorf("chunk exceeds limit (%d > 25): %q", len(c), c)
			}
		}
		// Reading order preserved (concatenation restores paragraph order).
		joined := strings.Join(got, "\n\n")
		for _, marker := range []string{"aaaaaaaaaa", "bbbbbbbbbb", "cccccccccc", "dddddddddd"} {
			if !strings.Contains(joined, marker) {
				t.Errorf("joined chunks missing %q", marker)
			}
		}
	})

	t.Run("oversized paragraph is word-split", func(t *testing.T) {
		para := strings.TrimSpace(strings.Repeat("word ", 50)) // ~250 chars, one paragraph
		got := chunkForPrepare(para, 40)
		if len(got) < 2 {
			t.Fatalf("chunks = %d, want >= 2 for oversized paragraph", len(got))
		}
		for _, c := range got {
			if len(c) > 40 {
				t.Errorf("word-split chunk exceeds limit (%d > 40): %q", len(c), c)
			}
		}
	})

	t.Run("empty and whitespace paragraphs dropped", func(t *testing.T) {
		got := chunkForPrepare("real\n\n   \n\n\n\nalso real", 1000)
		if len(got) != 1 {
			t.Fatalf("chunks = %d, want 1: %q", len(got), got)
		}
		if strings.Contains(got[0], "\n\n\n") {
			t.Errorf("chunk has stray blank paragraphs: %q", got[0])
		}
	})
}

func TestStubPreparer(t *testing.T) {
	var s StubPreparer

	got1, err := s.Prepare(context.Background(), "  hi  ")
	if err != nil {
		t.Fatalf("Prepare returned error: %v", err)
	}
	if got1 != "hi" {
		t.Errorf("result = %q, want trimmed %q", got1, "hi")
	}

	got2, err := s.Prepare(context.Background(), "  hi  ")
	if err != nil {
		t.Fatalf("Prepare returned error: %v", err)
	}
	if got1 != got2 {
		t.Errorf("stub not deterministic: %q != %q", got1, got2)
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := s.Prepare(ctx, "input"); !errors.Is(err, context.Canceled) {
		t.Errorf("cancelled ctx: err = %v, want context.Canceled", err)
	}
}

func TestPrepare_RecordsUsage(t *testing.T) {
	fake := &fakeGenerator{text: `{"narration":"Rewritten text."}`}
	tracker := usage.New()
	g := newTestPreparer(fake)
	g.Usage = tracker

	_, err := g.Prepare(context.Background(), "some input text")
	if err != nil {
		t.Fatalf("Prepare returned error: %v", err)
	}

	rep := tracker.Report()
	if rep.PromptTokens != 100 {
		t.Errorf("PromptTokens = %d, want 100", rep.PromptTokens)
	}
	if rep.OutputTokens != 40 {
		t.Errorf("OutputTokens = %d, want 40", rep.OutputTokens)
	}
	if rep.TotalTokens != 140 {
		t.Errorf("TotalTokens = %d, want 140", rep.TotalTokens)
	}
	if rep.Calls != 1 {
		t.Errorf("Calls = %d, want 1", rep.Calls)
	}
}
