package ingest

import (
	"reflect"
	"testing"
)

func TestSectionsFromHeadings_Empty(t *testing.T) {
	got := SectionsFromHeadings(nil)
	if got != nil {
		t.Errorf("SectionsFromHeadings(nil) = %v, want nil", got)
	}

	got = SectionsFromHeadings([]HeadingItem{})
	if got != nil {
		t.Errorf("SectionsFromHeadings([]) = %v, want nil", got)
	}
}

func TestSectionsFromHeadings_SingleH1(t *testing.T) {
	items := []HeadingItem{
		{Level: 1, Title: "Chapter One", Blocks: []Block{{Kind: BlockProse, Text: "intro"}}},
	}
	got := SectionsFromHeadings(items)
	if len(got) != 1 {
		t.Fatalf("got %d roots, want 1", len(got))
	}
	if got[0].Title != "Chapter One" || got[0].Level != 1 {
		t.Errorf("root = %+v, want {Title:Chapter One, Level:1}", got[0])
	}
	if len(got[0].Blocks) != 1 || got[0].Blocks[0].Text != "intro" {
		t.Errorf("blocks = %+v, want [{Kind:prose Text:intro}]", got[0].Blocks)
	}
	if len(got[0].Children) != 0 {
		t.Errorf("children = %v, want empty", got[0].Children)
	}
}

func TestSectionsFromHeadings_H1WithTwoH2(t *testing.T) {
	items := []HeadingItem{
		{Level: 1, Title: "Chapter", Blocks: []Block{{Kind: BlockProse, Text: "intro"}}},
		{Level: 2, Title: "Section A", Blocks: []Block{{Kind: BlockProse, Text: "a"}}},
		{Level: 2, Title: "Section B", Blocks: []Block{{Kind: BlockProse, Text: "b"}}},
	}
	got := SectionsFromHeadings(items)
	if len(got) != 1 {
		t.Fatalf("got %d roots, want 1", len(got))
	}
	root := got[0]
	if root.Title != "Chapter" || root.Level != 1 {
		t.Errorf("root = %+v, want {Title:Chapter, Level:1}", root)
	}
	if len(root.Children) != 2 {
		t.Fatalf("root.Children = %d, want 2", len(root.Children))
	}
	if root.Children[0].Title != "Section A" || root.Children[0].Level != 2 {
		t.Errorf("child[0] = %+v, want {Title:Section A, Level:2}", root.Children[0])
	}
	if root.Children[1].Title != "Section B" || root.Children[1].Level != 2 {
		t.Errorf("child[1] = %+v, want {Title:Section B, Level:2}", root.Children[1])
	}
}

func TestSectionsFromHeadings_SkippedLevel(t *testing.T) {
	// h1 → h3 (skipping h2): h3 should nest under h1.
	items := []HeadingItem{
		{Level: 1, Title: "Top", Blocks: []Block{{Kind: BlockProse, Text: "intro"}}},
		{Level: 3, Title: "Subsection", Blocks: []Block{{Kind: BlockProse, Text: "sub"}}},
	}
	got := SectionsFromHeadings(items)
	if len(got) != 1 {
		t.Fatalf("got %d roots, want 1", len(got))
	}
	root := got[0]
	if root.Title != "Top" {
		t.Errorf("root.Title = %q, want Top", root.Title)
	}
	if len(root.Children) != 1 {
		t.Fatalf("root.Children = %d, want 1", len(root.Children))
	}
	child := root.Children[0]
	if child.Title != "Subsection" || child.Level != 3 {
		t.Errorf("child = %+v, want {Title:Subsection, Level:3}", child)
	}
}

func TestSectionsFromHeadings_TwoSiblingRoots(t *testing.T) {
	items := []HeadingItem{
		{Level: 1, Title: "First", Blocks: []Block{{Kind: BlockProse, Text: "a"}}},
		{Level: 1, Title: "Second", Blocks: []Block{{Kind: BlockProse, Text: "b"}}},
	}
	got := SectionsFromHeadings(items)
	if len(got) != 2 {
		t.Fatalf("got %d roots, want 2", len(got))
	}
	if got[0].Title != "First" || got[1].Title != "Second" {
		t.Errorf("roots = [%q, %q], want [First, Second]", got[0].Title, got[1].Title)
	}
}

func TestSectionsFromHeadings_DeepNesting(t *testing.T) {
	// h1 → h2 → h3, then another h2 (sibling of first h2).
	items := []HeadingItem{
		{Level: 1, Title: "Chapter", Blocks: nil},
		{Level: 2, Title: "Section", Blocks: nil},
		{Level: 3, Title: "Subsection", Blocks: nil},
		{Level: 2, Title: "Another Section", Blocks: nil},
	}
	got := SectionsFromHeadings(items)
	if len(got) != 1 {
		t.Fatalf("got %d roots, want 1", len(got))
	}
	root := got[0]
	if len(root.Children) != 2 {
		t.Fatalf("root.Children = %d, want 2", len(root.Children))
	}
	sec1 := root.Children[0]
	if sec1.Title != "Section" || len(sec1.Children) != 1 {
		t.Errorf("first h2 = %+v, want {Title:Section, 1 child}", sec1)
	}
	if sec1.Children[0].Title != "Subsection" {
		t.Errorf("h3 = %q, want Subsection", sec1.Children[0].Title)
	}
	sec2 := root.Children[1]
	if sec2.Title != "Another Section" || len(sec2.Children) != 0 {
		t.Errorf("second h2 = %+v, want {Title:Another Section, 0 children}", sec2)
	}
}

func TestSectionsFromHeadings_PreservesBlocks(t *testing.T) {
	// Verify that Blocks are copied intact to each Section.
	items := []HeadingItem{
		{Level: 1, Title: "One", Blocks: []Block{
			{Kind: BlockProse, Text: "prose"},
			{Kind: BlockCode, Text: "code"},
		}},
		{Level: 1, Title: "Two", Blocks: []Block{
			{Kind: BlockFigure, Data: []byte("img"), MimeType: "image/png"},
		}},
	}
	got := SectionsFromHeadings(items)
	if len(got) != 2 {
		t.Fatalf("got %d roots, want 2", len(got))
	}
	if !reflect.DeepEqual(got[0].Blocks, items[0].Blocks) {
		t.Errorf("first root blocks mismatch")
	}
	if !reflect.DeepEqual(got[1].Blocks, items[1].Blocks) {
		t.Errorf("second root blocks mismatch")
	}
}
