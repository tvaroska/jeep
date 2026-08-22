package pdfsource

import (
	"os"
	"strings"
	"testing"

	"github.com/tvaroska/jeep/internal/ingest"
)

// countSections returns the recursive count of all sections in the tree.
func countSections(secs []*ingest.Section) int {
	n := 0
	for _, s := range secs {
		n++
		n += countSections(s.Children)
	}
	return n
}

// maxLevel returns the deepest Level in the tree.
func maxLevel(secs []*ingest.Section) int {
	m := 0
	for _, s := range secs {
		if s.Level > m {
			m = s.Level
		}
		if c := maxLevel(s.Children); c > m {
			m = c
		}
	}
	return m
}

// walkSections calls fn for every section in pre-order.
func walkSections(secs []*ingest.Section, fn func(*ingest.Section)) {
	for _, s := range secs {
		fn(s)
		walkSections(s.Children, fn)
	}
}

func TestLoad_TOCFixture_YieldsSectionsWithTitles(t *testing.T) {
	doc, err := Load("testdata/toc.pdf")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if doc.Meta.Path != "testdata/toc.pdf" {
		t.Errorf("Meta.Path = %q, want testdata/toc.pdf", doc.Meta.Path)
	}
	if got := len(doc.Sections); got != 3 {
		t.Fatalf("top-level sections = %d, want 3", got)
	}
	if got := countSections(doc.Sections); got != 3 {
		t.Errorf("recursive section count = %d, want 3", got)
	}
	wantTitles := []string{"Chapter One", "Chapter Two", "Chapter Three"}
	for i, want := range wantTitles {
		if got := doc.Sections[i].Title; got != want {
			t.Errorf("section[%d].Title = %q, want %q", i, got, want)
		}
		if doc.Sections[i].Level != 1 {
			t.Errorf("section[%d].Level = %d, want 1", i, doc.Sections[i].Level)
		}
	}
	// Each chapter carries its own prose block from its page span.
	var proseSeen bool
	for _, b := range doc.Sections[0].Blocks {
		if b.Kind == ingest.BlockProse && strings.Contains(b.Text, "Chapter One body text") {
			proseSeen = true
			if b.Page < 1 {
				t.Errorf("prose block Page = %d, want >= 1", b.Page)
			}
		}
	}
	if !proseSeen {
		t.Errorf("expected 'Chapter One body text' prose in first section, blocks: %+v", doc.Sections[0].Blocks)
	}
}

func TestLoad_ImageFixture_YieldsFigureBlock(t *testing.T) {
	doc, err := Load("testdata/image.pdf")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	var figs int
	walkSections(doc.Sections, func(s *ingest.Section) {
		for _, b := range s.Blocks {
			if b.Kind != ingest.BlockFigure {
				continue
			}
			figs++
			if b.Page <= 0 {
				t.Errorf("figure block Page = %d, want > 0", b.Page)
			}
			if len(b.Data) == 0 {
				t.Errorf("figure block has empty Data")
			}
			if !strings.HasPrefix(b.MimeType, "image/") {
				t.Errorf("figure block MimeType = %q, want image/*", b.MimeType)
			}
		}
	})
	if figs == 0 {
		t.Fatalf("expected at least one BlockFigure, got none")
	}
}

func TestLoad_HelloFixture_YieldsProse(t *testing.T) {
	// hello.pdf has no outline, so it exercises the whole-document fallback:
	// a single top-level section holding every page's prose.
	doc, err := Load("testdata/hello.pdf")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if got := len(doc.Sections); got != 1 {
		t.Fatalf("fallback should yield 1 top-level section, got %d", got)
	}
	if doc.Sections[0].Level != 1 {
		t.Errorf("fallback section Level = %d, want 1", doc.Sections[0].Level)
	}
	var all strings.Builder
	var page1 bool
	for _, b := range doc.Sections[0].Blocks {
		if b.Kind == ingest.BlockProse {
			all.WriteString(b.Text)
			if b.Page >= 1 {
				page1 = true
			}
		}
	}
	if !strings.Contains(all.String(), "Hello, audiobook world") {
		t.Errorf("expected known prose 'Hello, audiobook world', got %q", all.String())
	}
	if !page1 {
		t.Errorf("expected prose blocks with Page >= 1")
	}
}

// TestLoad_ObservabilityBook_MatchesSpike is the gated T2 acceptance test: it
// runs against the real 633-page book and asserts the phase-0 spike numbers.
// It t.Skips when the external PDF is absent.
func TestLoad_ObservabilityBook_MatchesSpike(t *testing.T) {
	const bookPath = "/home/boris/learning/audi/testdata/observability-engineering.pdf"
	if _, err := os.Stat(bookPath); err != nil {
		t.Skip("big-book fixture absent")
	}
	doc, err := Load(bookPath)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	if got := countSections(doc.Sections); got != 559 {
		t.Errorf("recursive section count = %d, want 559", got)
	}
	if got := maxLevel(doc.Sections); got != 4 {
		t.Errorf("max section Level = %d, want 4", got)
	}

	var figures, totalProse int
	var bodyChapterProse int
	walkSections(doc.Sections, func(s *ingest.Section) {
		for _, b := range s.Blocks {
			switch b.Kind {
			case ingest.BlockFigure:
				if b.Page > 0 {
					figures++
				}
			case ingest.BlockProse:
				totalProse += len(b.Text)
			}
		}
		if strings.Contains(s.Title, "What Is Observability") {
			for _, b := range s.Blocks {
				if b.Kind == ingest.BlockProse {
					bodyChapterProse += len(b.Text)
				}
			}
		}
	})

	if figures == 0 {
		t.Errorf("expected at least one BlockFigure with Page > 0, got none")
	}
	if totalProse < 100_000 {
		t.Errorf("aggregate prose length = %d, want > 100000", totalProse)
	}
	if bodyChapterProse == 0 {
		t.Errorf("sampled body chapter 'What Is Observability' has no prose")
	}
}
