package mdsource

import (
	"os"
	"strings"
	"testing"

	"github.com/tvaroska/jeep/internal/ingest"
)

func TestLoad_ArticleFixture(t *testing.T) {
	doc, err := Load("testdata/article.md")
	if err != nil {
		t.Fatalf("Load failed: %v", err)
	}

	if doc.Meta.Title != "Introduction" {
		t.Errorf("Meta.Title = %q, want Introduction", doc.Meta.Title)
	}
	if !strings.HasSuffix(doc.Meta.Path, "article.md") {
		t.Errorf("Meta.Path = %q, want *article.md", doc.Meta.Path)
	}

	// Verify we have sections (preamble + Introduction + Conclusion as top-level).
	if len(doc.Sections) < 2 {
		t.Fatalf("got %d sections, want at least 2", len(doc.Sections))
	}

	// First section should be the preamble (Level 1, untitled).
	preamble := doc.Sections[0]
	if preamble.Level != 1 || preamble.Title != "" {
		t.Errorf("preamble = {Level:%d, Title:%q}, want {Level:1, Title:\"\"}", preamble.Level, preamble.Title)
	}
	if len(preamble.Blocks) == 0 {
		t.Error("preamble should have prose blocks")
	}

	// Find the "Introduction" section (should have children: Background, Implementation).
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
	if len(intro.Children) < 2 {
		t.Fatalf("Introduction.Children = %d, want at least 2", len(intro.Children))
	}

	// Find the "Implementation" section (should have a BlockCode).
	var impl *ingest.Section
	var findImpl func(*ingest.Section)
	findImpl = func(s *ingest.Section) {
		if impl != nil {
			return
		}
		if s.Title == "Implementation" {
			impl = s
			return
		}
		for _, ch := range s.Children {
			findImpl(ch)
		}
	}
	findImpl(intro)
	if impl == nil {
		t.Fatal("Implementation section not found")
	}

	// Check for a BlockCode.
	var hasCode bool
	for _, blk := range impl.Blocks {
		if blk.Kind == ingest.BlockCode {
			hasCode = true
			if !strings.Contains(blk.Text, "func main") {
				t.Errorf("BlockCode.Text missing func main: %q", blk.Text)
			}
		}
	}
	if !hasCode {
		t.Error("Implementation section should have a BlockCode")
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

func TestLoad_NoHeadings(t *testing.T) {
	// A plain-text file with no headings should yield one Level-1 untitled section.
	tmpFile, err := os.CreateTemp("", "plaintext-*.txt")
	if err != nil {
		t.Fatal(err)
	}
	defer os.Remove(tmpFile.Name())
	defer tmpFile.Close()

	content := "This is a plain text file.\nNo headings at all.\nJust prose."
	if _, err := tmpFile.WriteString(content); err != nil {
		t.Fatal(err)
	}
	tmpFile.Close()

	doc, err := Load(tmpFile.Name())
	if err != nil {
		t.Fatalf("Load failed: %v", err)
	}

	if len(doc.Sections) != 1 {
		t.Fatalf("got %d sections, want 1", len(doc.Sections))
	}
	sec := doc.Sections[0]
	if sec.Level != 1 || sec.Title != "" {
		t.Errorf("section = {Level:%d, Title:%q}, want {Level:1, Title:\"\"}", sec.Level, sec.Title)
	}
	if len(sec.Blocks) != 1 || sec.Blocks[0].Kind != ingest.BlockProse {
		t.Errorf("section should have exactly 1 BlockProse, got %+v", sec.Blocks)
	}
	if !strings.Contains(sec.Blocks[0].Text, "plain text file") {
		t.Errorf("prose missing expected text: %q", sec.Blocks[0].Text)
	}
}

func TestParse_FencedCode(t *testing.T) {
	md := "# Test\n\n```\n# not a heading\ncode line\n```\n\nProse after."
	items := parse(md)
	if len(items) != 1 {
		t.Fatalf("got %d items, want 1", len(items))
	}
	item := items[0]
	if item.Title != "Test" || item.Level != 1 {
		t.Errorf("item = {Title:%q, Level:%d}, want {Test, 1}", item.Title, item.Level)
	}
	// Should have code + prose blocks.
	var hasCode, hasProse bool
	for _, blk := range item.Blocks {
		if blk.Kind == ingest.BlockCode && strings.Contains(blk.Text, "# not a heading") {
			hasCode = true
		}
		if blk.Kind == ingest.BlockProse && strings.Contains(blk.Text, "Prose after") {
			hasProse = true
		}
	}
	if !hasCode {
		t.Error("expected a BlockCode with '# not a heading'")
	}
	if !hasProse {
		t.Error("expected a BlockProse with 'Prose after'")
	}
}

func TestParse_TildeFence(t *testing.T) {
	md := "~~~\ncode\n~~~"
	items := parse(md)
	// Should produce one untitled section with a BlockCode.
	if len(items) != 1 {
		t.Fatalf("got %d items, want 1", len(items))
	}
	item := items[0]
	if item.Level != 1 || item.Title != "" {
		t.Errorf("preamble item = {Level:%d, Title:%q}, want {1, \"\"}", item.Level, item.Title)
	}
	if len(item.Blocks) != 1 || item.Blocks[0].Kind != ingest.BlockCode {
		t.Fatalf("blocks = %+v, want 1 BlockCode", item.Blocks)
	}
	if item.Blocks[0].Text != "code" {
		t.Errorf("code text = %q, want code", item.Blocks[0].Text)
	}
}

func TestParse_TrailingHash(t *testing.T) {
	md := "# Title #####\n\nContent."
	items := parse(md)
	if len(items) != 1 {
		t.Fatalf("got %d items, want 1", len(items))
	}
	if items[0].Title != "Title" {
		t.Errorf("title = %q, want Title (trailing # stripped)", items[0].Title)
	}
}
