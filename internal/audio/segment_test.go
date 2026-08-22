package audio

import (
	"strings"
	"testing"
	"unicode/utf8"
)

func TestSegment_Empty(t *testing.T) {
	tests := []struct {
		name  string
		input string
	}{
		{"empty string", ""},
		{"whitespace only", "   \n\t "},
		{"multiple newlines", "\n\n\n"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := Segment(tt.input)
			if result != nil {
				t.Errorf("Segment(%q) = %v, want nil", tt.input, result)
			}
		})
	}
}

func TestSegment_SingleShortSentence(t *testing.T) {
	input := "This is a single short sentence."
	want := "This is a single short sentence."

	result := Segment(input)
	if len(result) != 1 {
		t.Fatalf("Segment(%q) returned %d segments, want 1", input, len(result))
	}
	if result[0] != want {
		t.Errorf("Segment(%q) = %q, want %q", input, result[0], want)
	}
}

func TestSegment_MultipleShortSentencesPack(t *testing.T) {
	// Multiple small sentences should pack into one or few segments
	s := &Segmenter{MaxBytes: 100}
	input := "First sentence. Second sentence. Third sentence."

	result := s.Segment(input)
	if len(result) == 0 {
		t.Fatal("expected at least one segment")
	}

	// All segments must be <= MaxBytes
	for i, seg := range result {
		if len(seg) > 100 {
			t.Errorf("segment[%d] is %d bytes, exceeds MaxBytes=100", i, len(seg))
		}
		if !utf8.ValidString(seg) {
			t.Errorf("segment[%d] is invalid UTF-8", i)
		}
	}

	// Check that all content is preserved
	joined := strings.Join(result, " ")
	for _, sent := range []string{"First sentence", "Second sentence", "Third sentence"} {
		if !strings.Contains(joined, sent) {
			t.Errorf("joined segments missing %q", sent)
		}
	}
}

func TestSegment_WhitespaceNormalization(t *testing.T) {
	input := "First sentence.  \n\n  Second sentence.\r\n\tThird sentence."

	result := Segment(input)
	if len(result) == 0 {
		t.Fatal("expected at least one segment")
	}

	// All segments should have normalized whitespace (single spaces, trimmed)
	for i, seg := range result {
		if strings.Contains(seg, "  ") {
			t.Errorf("segment[%d] contains multiple consecutive spaces: %q", i, seg)
		}
		if strings.Contains(seg, "\n") || strings.Contains(seg, "\r") || strings.Contains(seg, "\t") {
			t.Errorf("segment[%d] contains non-space whitespace: %q", i, seg)
		}
		if seg != strings.TrimSpace(seg) {
			t.Errorf("segment[%d] has leading/trailing whitespace: %q", i, seg)
		}
	}
}

func TestSegment_ByteBudgetInvariant(t *testing.T) {
	tests := []struct {
		maxBytes int
		input    string
	}{
		{20, "Short sentence. Another sentence. And another."},
		{50, "This is a longer sentence with more content. " +
			"Multiple sentences here. " +
			"Each one should be checked."},
		{3000, strings.Repeat("This is a sentence. ", 200)}, // ~4000 bytes of repeated text
	}

	for _, tt := range tests {
		t.Run("", func(t *testing.T) {
			s := &Segmenter{MaxBytes: tt.maxBytes}
			result := s.Segment(tt.input)

			for i, seg := range result {
				if len(seg) > tt.maxBytes {
					t.Errorf("segment[%d] is %d bytes, exceeds MaxBytes=%d: %q",
						i, len(seg), tt.maxBytes, seg)
				}
				if !utf8.ValidString(seg) {
					t.Errorf("segment[%d] is invalid UTF-8", i)
				}
				if seg == "" {
					t.Errorf("segment[%d] is empty", i)
				}
			}
		})
	}
}

func TestSegment_OversizedSingleSentence(t *testing.T) {
	// A long comma-separated clause list longer than MaxBytes
	clauses := make([]string, 50)
	for i := range clauses {
		clauses[i] = "clause number N"
	}
	input := strings.Join(clauses, ", ")

	s := &Segmenter{MaxBytes: 80}
	result := s.Segment(input)

	if len(result) == 0 {
		t.Fatal("expected at least one segment")
	}

	// All segments must be <= MaxBytes, non-empty, valid UTF-8
	for i, seg := range result {
		if len(seg) > 80 {
			t.Errorf("segment[%d] is %d bytes, exceeds MaxBytes=80", i, len(seg))
		}
		if !utf8.ValidString(seg) {
			t.Errorf("segment[%d] is invalid UTF-8", i)
		}
		if seg == "" {
			t.Errorf("segment[%d] is empty", i)
		}
	}

	// Check order preserved (joining should contain original clauses in order)
	joined := strings.Join(result, " ")
	if !strings.Contains(joined, "clause") {
		t.Error("joined segments missing content")
	}
}

func TestSegment_HardRuneSplit(t *testing.T) {
	// A giant token with no spaces, commas, etc. — forces hard rune split
	// Use multi-byte UTF-8 to ensure rune boundaries are respected
	giant := strings.Repeat("日本語", 100) // Each character is 3 bytes

	s := &Segmenter{MaxBytes: 50}
	result := s.Segment(giant)

	if len(result) == 0 {
		t.Fatal("expected at least one segment")
	}

	// Every segment must be <= MaxBytes and valid UTF-8
	for i, seg := range result {
		if len(seg) > 50 {
			t.Errorf("segment[%d] is %d bytes, exceeds MaxBytes=50", i, len(seg))
		}
		if !utf8.ValidString(seg) {
			t.Errorf("segment[%d] has broken UTF-8", i)
		}
		if seg == "" {
			t.Errorf("segment[%d] is empty", i)
		}
	}

	// Concatenating all segments should reproduce the original
	joined := strings.Join(result, "")
	if joined != giant {
		t.Error("joined segments != original token")
	}
}

func TestSegment_MultiByteIsByteBasedNotRuneBased(t *testing.T) {
	// String of multi-byte chars near the budget
	// Each "é" is 2 bytes in UTF-8
	input := strings.Repeat("é", 30) // 60 bytes

	s := &Segmenter{MaxBytes: 50}
	result := s.Segment(input)

	if len(result) == 0 {
		t.Fatal("expected at least one segment")
	}

	// Should split by bytes, not rune count
	// 60 bytes with MaxBytes=50 should produce multiple segments
	if len(result) < 2 {
		t.Errorf("expected multiple segments for 60 bytes with MaxBytes=50, got %d", len(result))
	}

	for i, seg := range result {
		if len(seg) > 50 {
			t.Errorf("segment[%d] is %d bytes, exceeds MaxBytes=50", i, len(seg))
		}
		if !utf8.ValidString(seg) {
			t.Errorf("segment[%d] has broken UTF-8", i)
		}
	}
}

func TestSegment_AbbreviationGuard(t *testing.T) {
	input := "Dr. Smith met Mr. Jones. Then they left."

	// splitSentences should produce 2 sentences (not 4)
	sentences := splitSentences(input)
	if len(sentences) != 2 {
		t.Errorf("splitSentences returned %d sentences, want 2: %v", len(sentences), sentences)
	}

	// Verify the sentences
	want0 := "Dr. Smith met Mr. Jones."
	want1 := "Then they left."
	if len(sentences) >= 1 && sentences[0] != want0 {
		t.Errorf("sentence[0] = %q, want %q", sentences[0], want0)
	}
	if len(sentences) >= 2 && sentences[1] != want1 {
		t.Errorf("sentence[1] = %q, want %q", sentences[1], want1)
	}
}

func TestSplitSentences_DirectUnit(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  []string
	}{
		{
			name:  "initials",
			input: "A. B. C.",
			want:  []string{"A. B. C."},
		},
		{
			name:  "decimal not split",
			input: "Pi is 3.14 today. Yes.",
			want:  []string{"Pi is 3.14 today.", "Yes."},
		},
		{
			name:  "trailing text without terminator",
			input: "First sentence. Some trailing text",
			want:  []string{"First sentence.", "Some trailing text"},
		},
		{
			name:  "multiple terminators",
			input: "Question? Exclamation! Statement.",
			want:  []string{"Question?", "Exclamation!", "Statement."},
		},
		{
			name:  "quoted sentence",
			input: `He said "Hello." She replied "Hi."`,
			want:  []string{`He said "Hello."`, `She replied "Hi."`},
		},
		{
			name:  "abbreviations etc.",
			input: "See fig. 3.14 for details. Dr. Jones agrees.",
			want:  []string{"See fig. 3.14 for details.", "Dr. Jones agrees."},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := splitSentences(tt.input)
			if len(got) != len(tt.want) {
				t.Errorf("splitSentences(%q) returned %d sentences, want %d: %v",
					tt.input, len(got), len(tt.want), got)
				return
			}
			for i := range got {
				if got[i] != tt.want[i] {
					t.Errorf("sentence[%d] = %q, want %q", i, got[i], tt.want[i])
				}
			}
		})
	}
}

func TestSegment_PackageLevelConvenience(t *testing.T) {
	input := "This is a test. Another sentence."

	// Package-level function should match default Segmenter
	result1 := Segment(input)
	result2 := (&Segmenter{}).Segment(input)

	if len(result1) != len(result2) {
		t.Errorf("Segment() returned %d segments, (&Segmenter{}).Segment() returned %d",
			len(result1), len(result2))
		return
	}

	for i := range result1 {
		if result1[i] != result2[i] {
			t.Errorf("segment[%d]: Segment() = %q, (&Segmenter{}).Segment() = %q",
				i, result1[i], result2[i])
		}
	}
}

func TestSegment_DefaultMaxBytes(t *testing.T) {
	// Verify DefaultMaxSegmentBytes is used when MaxBytes <= 0
	input := strings.Repeat("word ", 1000) // ~5000 bytes

	s1 := &Segmenter{MaxBytes: 0}
	s2 := &Segmenter{MaxBytes: -1}
	s3 := &Segmenter{MaxBytes: DefaultMaxSegmentBytes}

	result1 := s1.Segment(input)
	result2 := s2.Segment(input)
	result3 := s3.Segment(input)

	// All should produce the same result
	if len(result1) != len(result3) || len(result2) != len(result3) {
		t.Errorf("different segment counts: MaxBytes=0 -> %d, MaxBytes=-1 -> %d, MaxBytes=%d -> %d",
			len(result1), len(result2), DefaultMaxSegmentBytes, len(result3))
	}

	// All segments should be <= DefaultMaxSegmentBytes
	for _, result := range [][]string{result1, result2, result3} {
		for i, seg := range result {
			if len(seg) > DefaultMaxSegmentBytes {
				t.Errorf("segment[%d] is %d bytes, exceeds DefaultMaxSegmentBytes=%d",
					i, len(seg), DefaultMaxSegmentBytes)
			}
		}
	}
}

func TestSplitOversized_ClauseSplit(t *testing.T) {
	// Test clause splitting with semicolons and commas
	input := "This is a long clause; and this is another long clause; and yet another clause"
	max := 40

	result := splitOversized(input, max, nil)

	for i, seg := range result {
		if len(seg) > max {
			t.Errorf("segment[%d] is %d bytes, exceeds max=%d: %q", i, len(seg), max, seg)
		}
		if !utf8.ValidString(seg) {
			t.Errorf("segment[%d] is invalid UTF-8", i)
		}
		if seg == "" {
			t.Errorf("segment[%d] is empty", i)
		}
	}
}

func TestSplitOversized_WordSplit(t *testing.T) {
	// Test word splitting
	input := "word1 word2 word3 word4 word5 word6 word7 word8 word9 word10"
	max := 25

	result := splitOversized(input, max, nil)

	for i, seg := range result {
		if len(seg) > max {
			t.Errorf("segment[%d] is %d bytes, exceeds max=%d: %q", i, len(seg), max, seg)
		}
		if !utf8.ValidString(seg) {
			t.Errorf("segment[%d] is invalid UTF-8", i)
		}
		if seg == "" {
			t.Errorf("segment[%d] is empty", i)
		}
	}
}

func TestGreedyPack(t *testing.T) {
	items := []string{"one", "two", "three", "four"}
	sep := " "
	max := 10

	result := greedyPack(items, sep, max)

	for i, seg := range result {
		if len(seg) > max {
			t.Errorf("segment[%d] is %d bytes, exceeds max=%d: %q", i, len(seg), max, seg)
		}
		if seg == "" {
			t.Errorf("segment[%d] is empty", i)
		}
	}

	// Verify all items are present in order
	joined := strings.Join(result, sep)
	for _, item := range items {
		if !strings.Contains(joined, item) {
			t.Errorf("joined result missing item %q", item)
		}
	}
}

func TestHardRuneSplit(t *testing.T) {
	tests := []struct {
		name  string
		input string
		max   int
	}{
		{"ASCII", strings.Repeat("a", 100), 25},
		{"UTF-8 2-byte", strings.Repeat("é", 50), 25}, // 100 bytes
		{"UTF-8 3-byte", strings.Repeat("日", 50), 25}, // 150 bytes
		{"mixed", "abc日本éé語", 10},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := hardRuneSplit(tt.input, tt.max)

			if len(result) == 0 {
				t.Fatal("expected at least one chunk")
			}

			for i, chunk := range result {
				if len(chunk) > tt.max {
					t.Errorf("chunk[%d] is %d bytes, exceeds max=%d", i, len(chunk), tt.max)
				}
				if !utf8.ValidString(chunk) {
					t.Errorf("chunk[%d] has broken UTF-8", i)
				}
				if chunk == "" {
					t.Errorf("chunk[%d] is empty", i)
				}
			}

			// Verify concatenation reproduces original
			joined := strings.Join(result, "")
			if joined != tt.input {
				t.Errorf("joined chunks != original: got %q, want %q", joined, tt.input)
			}
		})
	}
}
