package ingest

import (
	"reflect"
	"testing"
)

// Helper to build sections compactly in tests.
func sec(title string, level int, children ...*Section) *Section {
	return &Section{
		Title:    title,
		Level:    level,
		Children: children,
	}
}

// Helper to build prose blocks compactly in tests.
func prose(t string) Block {
	return Block{Kind: BlockProse, Text: t}
}

func TestStructure_TopLevelSectionsNoChildren_OneChapterEach(t *testing.T) {
	doc := &Document{
		Sections: []*Section{
			sec("A", 1),
			sec("B", 1),
			sec("C", 1),
		},
	}
	chapters := Structure(doc)
	if len(chapters) != 3 {
		t.Fatalf("expected 3 chapters, got %d", len(chapters))
	}
	want := []struct {
		title string
		level int
	}{
		{"A", 1},
		{"B", 1},
		{"C", 1},
	}
	for i, ch := range chapters {
		if ch.Title != want[i].title {
			t.Errorf("chapter[%d] Title: got %q, want %q", i, ch.Title, want[i].title)
		}
		if ch.Level != want[i].level {
			t.Errorf("chapter[%d] Level: got %d, want %d", i, ch.Level, want[i].level)
		}
		if len(ch.Sections) != 1 {
			t.Errorf("chapter[%d] Sections length: got %d, want 1", i, len(ch.Sections))
		}
		if ch.Sections[0] != doc.Sections[i] {
			t.Errorf("chapter[%d] Sections[0]: got different pointer, want doc.Sections[%d]", i, i)
		}
	}
}

func TestStructure_TwoOrMoreChildren_ExpandsAndAbsorbsIntro(t *testing.T) {
	introBlocks := []Block{prose("intro")}
	doc := &Document{
		Sections: []*Section{
			{
				Title:  "Chapter 1",
				Level:  1,
				Blocks: introBlocks,
				Children: []*Section{
					sec("Section 1.1", 2),
					sec("Section 1.2", 2),
				},
			},
			sec("Chapter 2", 1),
		},
	}
	chapters := Structure(doc)
	if len(chapters) != 3 {
		t.Fatalf("expected 3 chapters, got %d", len(chapters))
	}

	// Chapter 0: Section 1.1 with intro absorbed
	if chapters[0].Title != "Section 1.1" {
		t.Errorf("chapter[0] Title: got %q, want %q", chapters[0].Title, "Section 1.1")
	}
	if chapters[0].Level != 2 {
		t.Errorf("chapter[0] Level: got %d, want 2", chapters[0].Level)
	}
	if len(chapters[0].Sections) != 2 {
		t.Fatalf("chapter[0] Sections length: got %d, want 2", len(chapters[0].Sections))
	}
	// First section is the intro shim
	if chapters[0].Sections[0].Title != "" {
		t.Errorf("chapter[0] Sections[0].Title: got %q, want empty", chapters[0].Sections[0].Title)
	}
	if !reflect.DeepEqual(chapters[0].Sections[0].Blocks, introBlocks) {
		t.Errorf("chapter[0] Sections[0].Blocks: got %+v, want %+v", chapters[0].Sections[0].Blocks, introBlocks)
	}
	// Second section is the actual child
	if chapters[0].Sections[1].Title != "Section 1.1" {
		t.Errorf("chapter[0] Sections[1].Title: got %q, want %q", chapters[0].Sections[1].Title, "Section 1.1")
	}

	// Chapter 1: Section 1.2 (no intro)
	if chapters[1].Title != "Section 1.2" {
		t.Errorf("chapter[1] Title: got %q, want %q", chapters[1].Title, "Section 1.2")
	}
	if chapters[1].Level != 2 {
		t.Errorf("chapter[1] Level: got %d, want 2", chapters[1].Level)
	}
	if len(chapters[1].Sections) != 1 {
		t.Fatalf("chapter[1] Sections length: got %d, want 1", len(chapters[1].Sections))
	}
	if chapters[1].Sections[0].Title != "Section 1.2" {
		t.Errorf("chapter[1] Sections[0].Title: got %q, want %q", chapters[1].Sections[0].Title, "Section 1.2")
	}

	// Chapter 2: kept whole
	if chapters[2].Title != "Chapter 2" {
		t.Errorf("chapter[2] Title: got %q, want %q", chapters[2].Title, "Chapter 2")
	}
	if chapters[2].Level != 1 {
		t.Errorf("chapter[2] Level: got %d, want 1", chapters[2].Level)
	}
	if len(chapters[2].Sections) != 1 {
		t.Fatalf("chapter[2] Sections length: got %d, want 1", len(chapters[2].Sections))
	}
}

func TestStructure_ExpandsWithNoParentIntro_NoShim(t *testing.T) {
	doc := &Document{
		Sections: []*Section{
			{
				Title:  "Parent",
				Level:  1,
				Blocks: []Block{}, // empty blocks
				Children: []*Section{
					sec("Child 1", 2),
					sec("Child 2", 2),
				},
			},
		},
	}
	chapters := Structure(doc)
	if len(chapters) != 2 {
		t.Fatalf("expected 2 chapters, got %d", len(chapters))
	}
	// First child should NOT have an intro shim
	if len(chapters[0].Sections) != 1 {
		t.Errorf("chapter[0] Sections length: got %d, want 1 (no shim)", len(chapters[0].Sections))
	}
	if chapters[0].Sections[0].Title != "Child 1" {
		t.Errorf("chapter[0] Sections[0].Title: got %q, want %q", chapters[0].Sections[0].Title, "Child 1")
	}
}

func TestStructure_SkipsBoilerplate(t *testing.T) {
	doc := &Document{
		Sections: []*Section{
			sec("Cover", 1),
			sec("Table of Contents", 1),
			sec("Preface", 1),
			sec("Chapter 1", 1),
			sec("Index", 1),
		},
	}
	chapters := Structure(doc)
	if len(chapters) != 2 {
		t.Fatalf("expected 2 chapters (Preface, Chapter 1), got %d", len(chapters))
	}
	if chapters[0].Title != "Preface" {
		t.Errorf("chapter[0] Title: got %q, want %q", chapters[0].Title, "Preface")
	}
	if chapters[1].Title != "Chapter 1" {
		t.Errorf("chapter[1] Title: got %q, want %q", chapters[1].Title, "Chapter 1")
	}
}

func TestStructure_SingleChild_KeptWhole(t *testing.T) {
	doc := &Document{
		Sections: []*Section{
			{
				Title: "Parent",
				Level: 1,
				Children: []*Section{
					sec("Only Child", 2),
				},
			},
		},
	}
	chapters := Structure(doc)
	if len(chapters) != 1 {
		t.Fatalf("expected 1 chapter, got %d", len(chapters))
	}
	// Title should be from the parent (not expanded)
	if chapters[0].Title != "Parent" {
		t.Errorf("chapter Title: got %q, want %q", chapters[0].Title, "Parent")
	}
	if chapters[0].Level != 1 {
		t.Errorf("chapter Level: got %d, want 1", chapters[0].Level)
	}
	if len(chapters[0].Sections) != 1 {
		t.Fatalf("chapter Sections length: got %d, want 1", len(chapters[0].Sections))
	}
	// Sections should contain the parent section
	if chapters[0].Sections[0].Title != "Parent" {
		t.Errorf("chapter Sections[0].Title: got %q, want %q", chapters[0].Sections[0].Title, "Parent")
	}
}

func TestStructure_RootLevelNotOne_PreservesLevel(t *testing.T) {
	doc := &Document{
		Sections: []*Section{
			sec("First", 2),
			sec("Second", 2),
		},
	}
	chapters := Structure(doc)
	if len(chapters) != 2 {
		t.Fatalf("expected 2 chapters, got %d", len(chapters))
	}
	for i, ch := range chapters {
		if ch.Level != 2 {
			t.Errorf("chapter[%d] Level: got %d, want 2", i, ch.Level)
		}
	}
}

func TestStructure_NilOrEmpty_ReturnsNil(t *testing.T) {
	tests := []struct {
		name string
		doc  *Document
	}{
		{"nil document", nil},
		{"empty document", &Document{}},
		{"empty sections", &Document{Sections: []*Section{}}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			chapters := Structure(tt.doc)
			if chapters != nil {
				t.Errorf("expected nil, got %v", chapters)
			}
		})
	}
}

func TestIsBoilerplate(t *testing.T) {
	tests := []struct {
		title string
		want  bool
	}{
		// Exact matches
		{"cover", true},
		{"index", true},
		{"Table of Contents", true},
		{"copyright", true},
		{"colophon", true},
		{"contents", true},
		{"honeycomb", true},
		// Prefix matches
		{"Copyright © 2020", true},
		{"Table of Contents - Detailed", true},
		{"Index of Terms", true},
		// Case insensitivity
		{"INDEX", true},
		{"COVER", true},
		{"Table Of Contents", true},
		// Keep real content
		{"Preface", false},
		{"Foreword", false},
		{"About the Authors", false},
		{"Introduction", false},
		{"Chapter 1", false},
		{"Appendix", false},
	}
	for _, tt := range tests {
		t.Run(tt.title, func(t *testing.T) {
			got := isBoilerplate(tt.title)
			if got != tt.want {
				t.Errorf("isBoilerplate(%q) = %v, want %v", tt.title, got, tt.want)
			}
		})
	}
}
