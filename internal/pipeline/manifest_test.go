package pipeline

import (
	"encoding/json"
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestWriteManifest_RoundTrip(t *testing.T) {
	m := Manifest{
		Source:      "book.pdf",
		GeneratedAt: time.Date(2026, 8, 15, 12, 0, 0, 0, time.UTC),
		Chapters: []ChapterEntry{
			{Title: "Intro", File: "chapter_01.mp3", DurationSeconds: 42.5, StartPage: 1, EndPage: 3},
			{Title: "Chapter 2", File: "chapter_02.mp3", DurationSeconds: 90, StartPage: 4, EndPage: 10},
		},
	}

	path := filepath.Join(t.TempDir(), "manifest.json")
	if err := WriteManifest(path, m); err != nil {
		t.Fatalf("WriteManifest: %v", err)
	}

	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read manifest: %v", err)
	}

	// Indented JSON with a chapters key and newlines.
	if !strings.Contains(string(raw), "\"chapters\"") {
		t.Errorf("manifest missing chapters key: %s", raw)
	}
	if !strings.Contains(string(raw), "\n") {
		t.Errorf("manifest is not indented (no newlines): %s", raw)
	}
	if !strings.HasSuffix(string(raw), "\n") {
		t.Errorf("manifest should end with a trailing newline")
	}

	var got Manifest
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatalf("unmarshal manifest: %v", err)
	}
	if got.Source != m.Source {
		t.Errorf("source mismatch: got %q, want %q", got.Source, m.Source)
	}
	if len(got.Chapters) != 2 {
		t.Fatalf("chapter count: got %d, want 2", len(got.Chapters))
	}
	for i, want := range m.Chapters {
		g := got.Chapters[i]
		if g.Title != want.Title || g.File != want.File ||
			g.StartPage != want.StartPage || g.EndPage != want.EndPage ||
			math.Abs(g.DurationSeconds-want.DurationSeconds) > 1e-9 {
			t.Errorf("chapter %d mismatch: got %+v, want %+v", i, g, want)
		}
		// File must be a basename (relocatable manifest).
		if strings.ContainsRune(g.File, '/') {
			t.Errorf("chapter %d File should be a basename, got %q", i, g.File)
		}
	}
}

func TestWriteManifest_CreatesParentDir(t *testing.T) {
	// Path in a not-yet-existing subdirectory should succeed.
	path := filepath.Join(t.TempDir(), "nested", "deeper", "manifest.json")
	if err := WriteManifest(path, Manifest{Source: "x.pdf"}); err != nil {
		t.Fatalf("WriteManifest into new dir: %v", err)
	}
	if _, err := os.Stat(path); err != nil {
		t.Errorf("manifest not written: %v", err)
	}
}

func TestParseFFprobeDuration(t *testing.T) {
	tests := []struct {
		name    string
		in      string
		want    time.Duration
		wantErr bool
	}{
		{"typical with newline", "12.34\n", time.Duration(12.34 * float64(time.Second)), false},
		{"zero", "0", 0, false},
		{"empty", "", 0, true},
		{"non-numeric", "abc", 0, true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := parseFFprobeDuration(tc.in)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("expected error for %q, got %v", tc.in, got)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if math.Abs(float64(got-tc.want)) > float64(time.Millisecond) {
				t.Errorf("got %v, want %v", got, tc.want)
			}
		})
	}
}
