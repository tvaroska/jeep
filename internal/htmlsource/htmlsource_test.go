package htmlsource

import (
	"strings"
	"testing"

	"github.com/tvaroska/jeep/internal/ingest"
)

func TestLoad_ArticleFixture(t *testing.T) {
	doc, err := Load("testdata/article.html")
	if err != nil {
		t.Fatalf("Load failed: %v", err)
	}

	if doc.Meta.Title != "Test Article Title" {
		t.Errorf("Meta.Title = %q, want Test Article Title", doc.Meta.Title)
	}
	if !strings.HasSuffix(doc.Meta.Path, "article.html") {
		t.Errorf("Meta.Path = %q, want *article.html", doc.Meta.Path)
	}

	// Verify we have sections (Introduction + Conclusion as top-level).
	if len(doc.Sections) < 2 {
		t.Fatalf("got %d sections, want at least 2", len(doc.Sections))
	}

	// Find the "Introduction" section.
	var intro *ingest.Section
	for _, sec := range doc.Sections {
		if sec.Title == "Introduction" {
			intro = sec
			break
		}
	}
	if intro == nil {
		t.Fatal("Introduction section not found")
	}
	if intro.Level != 1 {
		t.Errorf("Introduction.Level = %d, want 1", intro.Level)
	}

	// Check that intro has children (Background, Code Examples, Inline Code).
	if len(intro.Children) < 2 {
		t.Fatalf("Introduction.Children = %d, want at least 2", len(intro.Children))
	}

	// Find the "Code Examples" section (should have a BlockCode).
	var codeExamples *ingest.Section
	var findCodeExamples func(*ingest.Section)
	findCodeExamples = func(s *ingest.Section) {
		if codeExamples != nil {
			return
		}
		if s.Title == "Code Examples" {
			codeExamples = s
			return
		}
		for _, ch := range s.Children {
			findCodeExamples(ch)
		}
	}
	findCodeExamples(intro)
	if codeExamples == nil {
		t.Fatal("Code Examples section not found")
	}

	// Check for a BlockCode.
	var hasCode bool
	for _, blk := range codeExamples.Blocks {
		if blk.Kind == ingest.BlockCode {
			hasCode = true
			if !strings.Contains(blk.Text, "function hello") {
				t.Errorf("BlockCode.Text missing function hello: %q", blk.Text)
			}
		}
	}
	if !hasCode {
		t.Error("Code Examples section should have a BlockCode")
	}

	// Find the "Conclusion" section.
	var conclusion *ingest.Section
	for _, sec := range doc.Sections {
		if sec.Title == "Conclusion" {
			conclusion = sec
			break
		}
	}
	if conclusion == nil {
		t.Fatal("Conclusion section not found")
	}
	if conclusion.Level != 1 {
		t.Errorf("Conclusion.Level = %d, want 1", conclusion.Level)
	}
}

func TestLoad_BoilerplateStripped(t *testing.T) {
	// Verify that nav/header/footer/aside text does NOT appear in any blocks.
	doc, err := Load("testdata/article.html")
	if err != nil {
		t.Fatalf("Load failed: %v", err)
	}

	// Collect all text from all blocks.
	var allText strings.Builder
	var collectText func([]*ingest.Section)
	collectText = func(sections []*ingest.Section) {
		for _, sec := range sections {
			for _, blk := range sec.Blocks {
				allText.WriteString(blk.Text)
				allText.WriteString(" ")
			}
			collectText(sec.Children)
		}
	}
	collectText(doc.Sections)
	text := strings.ToLower(allText.String())

	// Check that boilerplate phrases are absent.
	boilerplate := []string{
		"navigation text that should be stripped",
		"site header - should be stripped",
		"header content to remove",
		"sidebar - should be stripped",
		"sidebar content that should not appear",
		"footer text - copyright 2026 - should be stripped",
	}
	for _, phrase := range boilerplate {
		if strings.Contains(text, strings.ToLower(phrase)) {
			t.Errorf("boilerplate phrase %q found in extracted text", phrase)
		}
	}
}

func TestLoad_SectionTreeMatchesHeadings(t *testing.T) {
	// Verify that the section tree structure matches the <article> headings.
	doc, err := Load("testdata/article.html")
	if err != nil {
		t.Fatalf("Load failed: %v", err)
	}

	// We expect: Introduction (h1) with children Background (h2), Code Examples (h2),
	// Inline Code (h2), and Conclusion (h1).
	if len(doc.Sections) < 2 {
		t.Fatalf("got %d top-level sections, want at least 2", len(doc.Sections))
	}

	// Introduction should have Background as a child.
	var intro *ingest.Section
	for _, sec := range doc.Sections {
		if sec.Title == "Introduction" {
			intro = sec
			break
		}
	}
	if intro == nil {
		t.Fatal("Introduction section not found")
	}

	var background *ingest.Section
	for _, ch := range intro.Children {
		if ch.Title == "Background" {
			background = ch
			break
		}
	}
	if background == nil {
		t.Error("Background section not found as a child of Introduction")
	}
	if background != nil && background.Level != 2 {
		t.Errorf("Background.Level = %d, want 2", background.Level)
	}

	// Background should have History as a child.
	if background != nil {
		var history *ingest.Section
		for _, ch := range background.Children {
			if ch.Title == "History" {
				history = ch
				break
			}
		}
		if history == nil {
			t.Error("History section not found as a child of Background")
		}
		if history != nil && history.Level != 3 {
			t.Errorf("History.Level = %d, want 3", history.Level)
		}
	}
}

func TestLoad_PreservesNewlinesInPre(t *testing.T) {
	// Verify that <pre> content preserves newlines (rawTextContent).
	doc, err := Load("testdata/article.html")
	if err != nil {
		t.Fatalf("Load failed: %v", err)
	}

	// Find the BlockCode.
	var codeText string
	var findCode func([]*ingest.Section)
	findCode = func(sections []*ingest.Section) {
		for _, sec := range sections {
			for _, blk := range sec.Blocks {
				if blk.Kind == ingest.BlockCode && strings.Contains(blk.Text, "function hello") {
					codeText = blk.Text
					return
				}
			}
			findCode(sec.Children)
		}
	}
	findCode(doc.Sections)

	if codeText == "" {
		t.Fatal("BlockCode with 'function hello' not found")
	}

	// The <pre> block should contain newlines (not collapsed to spaces).
	if !strings.Contains(codeText, "\n") {
		t.Errorf("BlockCode should preserve newlines, got: %q", codeText)
	}
}

func TestLoad_InlineCodeStandalone(t *testing.T) {
	// Verify that standalone <code> (not inside <pre>) becomes a BlockCode.
	doc, err := Load("testdata/article.html")
	if err != nil {
		t.Fatalf("Load failed: %v", err)
	}

	// Find the "Inline Code" section.
	var inlineCode *ingest.Section
	var findInlineCode func([]*ingest.Section)
	findInlineCode = func(sections []*ingest.Section) {
		for _, sec := range sections {
			if sec.Title == "Inline Code" {
				inlineCode = sec
				return
			}
			findInlineCode(sec.Children)
		}
	}
	findInlineCode(doc.Sections)

	if inlineCode == nil {
		t.Fatal("Inline Code section not found")
	}

	// Should have at least one BlockCode for the standalone <code> snippet.
	var hasCode bool
	for _, blk := range inlineCode.Blocks {
		if blk.Kind == ingest.BlockCode && strings.Contains(blk.Text, "inline code snippets") {
			hasCode = true
		}
	}
	if !hasCode {
		t.Error("Inline Code section should have a BlockCode for standalone <code>")
	}
}
