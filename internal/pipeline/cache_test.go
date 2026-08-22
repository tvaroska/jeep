package pipeline

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tvaroska/jeep/internal/describe"
)

func TestDiskCache_RoundTrip(t *testing.T) {
	c := &DiskCache{Dir: t.TempDir()}
	want := []byte("hello world")
	if err := c.Put("segments", "k1", want); err != nil {
		t.Fatalf("Put: %v", err)
	}
	got, ok, err := c.Get("segments", "k1")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if !ok {
		t.Fatal("Get: want hit, got miss")
	}
	if string(got) != string(want) {
		t.Errorf("Get: got %q, want %q", got, want)
	}
}

func TestDiskCache_Miss(t *testing.T) {
	c := &DiskCache{Dir: t.TempDir()}
	got, ok, err := c.Get("segments", "never")
	if err != nil {
		t.Fatalf("Get miss should not error, got: %v", err)
	}
	if ok {
		t.Error("Get: want miss, got hit")
	}
	if got != nil {
		t.Errorf("Get miss: want nil data, got %q", got)
	}
}

func TestDiskCache_Overwrite(t *testing.T) {
	c := &DiskCache{Dir: t.TempDir()}
	if err := c.Put("narration", "k", []byte("first")); err != nil {
		t.Fatalf("Put first: %v", err)
	}
	if err := c.Put("narration", "k", []byte("second")); err != nil {
		t.Fatalf("Put second: %v", err)
	}
	got, ok, err := c.Get("narration", "k")
	if err != nil || !ok {
		t.Fatalf("Get: ok=%v err=%v", ok, err)
	}
	if string(got) != "second" {
		t.Errorf("overwrite: got %q, want %q", got, "second")
	}
}

func TestDiskCache_NamespaceIsolation(t *testing.T) {
	c := &DiskCache{Dir: t.TempDir()}
	if err := c.Put("segments", "k", []byte("seg")); err != nil {
		t.Fatalf("Put segments: %v", err)
	}
	if err := c.Put("narration", "k", []byte("narr")); err != nil {
		t.Fatalf("Put narration: %v", err)
	}
	s, _, _ := c.Get("segments", "k")
	n, _, _ := c.Get("narration", "k")
	if string(s) != "seg" || string(n) != "narr" {
		t.Errorf("namespace isolation broken: segments=%q narration=%q", s, n)
	}
}

func TestDiskCache_CreatesNamespaceDir(t *testing.T) {
	dir := t.TempDir()
	c := &DiskCache{Dir: dir}
	if err := c.Put("chapters", "k", []byte("x")); err != nil {
		t.Fatalf("Put: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "chapters")); err != nil {
		t.Errorf("namespace dir not created: %v", err)
	}
}

func TestDiskCache_AtomicWriteLeavesNoTemp(t *testing.T) {
	dir := t.TempDir()
	c := &DiskCache{Dir: dir}
	if err := c.Put("segments", "thekey", []byte("data")); err != nil {
		t.Fatalf("Put: %v", err)
	}
	entries, err := os.ReadDir(filepath.Join(dir, "segments"))
	if err != nil {
		t.Fatalf("ReadDir: %v", err)
	}
	if len(entries) != 1 {
		var names []string
		for _, e := range entries {
			names = append(names, e.Name())
		}
		t.Fatalf("expected exactly 1 entry, got %d: %v", len(entries), names)
	}
	if entries[0].Name() != "thekey" {
		t.Errorf("entry name: got %q, want %q", entries[0].Name(), "thekey")
	}
	if strings.Contains(entries[0].Name(), ".tmp-") {
		t.Errorf("temp turd left behind: %q", entries[0].Name())
	}
}

func TestHashKey_DeterminismAndCollisionSafety(t *testing.T) {
	a := hashKey([]byte("a"), []byte("bc"))
	b := hashKey([]byte("a"), []byte("bc"))
	if a != b {
		t.Errorf("hashKey not deterministic: %q != %q", a, b)
	}
	if len(a) != 64 {
		t.Errorf("hashKey length: got %d, want 64", len(a))
	}
	// Length-prefixing must make these distinct.
	if hashKey([]byte("a"), []byte("bc")) == hashKey([]byte("ab"), []byte("c")) {
		t.Error("hashKey collision across ambiguous concatenation")
	}
}

func TestSegmentCacheKey_DiffersByField(t *testing.T) {
	base := segmentCacheKey("m|v|s", "x")
	if base == segmentCacheKey("m|v|s2", "x") {
		t.Error("segmentCacheKey should differ by fingerprint")
	}
	if base == segmentCacheKey("m|v|s", "y") {
		t.Error("segmentCacheKey should differ by segment text")
	}
	if base != segmentCacheKey("m|v|s", "x") {
		t.Error("segmentCacheKey should be stable for equal inputs")
	}
}

func TestChapterCacheKey_DiffersByField(t *testing.T) {
	base := chapterCacheKey("m|v|s", "adapted text")
	if base == chapterCacheKey("m|v|s2", "adapted text") {
		t.Error("chapterCacheKey should differ by fingerprint")
	}
	if base == chapterCacheKey("m|v|s", "other text") {
		t.Error("chapterCacheKey should differ by adapted text")
	}
}

func TestNarrationCacheKey_DiffersByContent(t *testing.T) {
	pages := []describe.PageContent{
		{Text: "page one", Images: []describe.ImageRef{{Data: []byte("IMG"), MimeType: "image/png"}}},
	}
	base := narrationCacheKey("gemini", pages)

	// Model change.
	if base == narrationCacheKey("gemini2", pages) {
		t.Error("narrationCacheKey should differ by gemini model")
	}

	// Page text change.
	pagesText := []describe.PageContent{
		{Text: "page two", Images: []describe.ImageRef{{Data: []byte("IMG"), MimeType: "image/png"}}},
	}
	if base == narrationCacheKey("gemini", pagesText) {
		t.Error("narrationCacheKey should differ when page text changes")
	}

	// Image bytes change.
	pagesImg := []describe.PageContent{
		{Text: "page one", Images: []describe.ImageRef{{Data: []byte("IMG2"), MimeType: "image/png"}}},
	}
	if base == narrationCacheKey("gemini", pagesImg) {
		t.Error("narrationCacheKey should differ when image bytes change")
	}

	// Stable for equal input.
	if base != narrationCacheKey("gemini", pages) {
		t.Error("narrationCacheKey should be stable for equal inputs")
	}
}
