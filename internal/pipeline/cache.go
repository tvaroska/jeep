package pipeline

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"

	"github.com/tvaroska/jeep/internal/art"
	"github.com/tvaroska/jeep/internal/describe"
)

// Cache is a content-addressed blob store for resumable pipeline state. Keys are
// opaque strings (the pipeline uses hex SHA-256 digests); ns groups keys into a
// namespace (a subdirectory on disk). A miss returns (nil, false, nil).
//
// It is a LOCAL persistence boundary (disk), so — like audio.Concatenator and
// pipeline.Prober — it is a seam with a local adapter (DiskCache) only, no GCP
// adapter. A future GCS-backed adapter for cloud runs could satisfy this same
// interface; that is out of scope here.
//
// Implementations MUST be crash-safe on write: a process killed mid-Put must
// never leave a value that a later Get reads as complete-but-truncated.
type Cache interface {
	Get(ns, key string) ([]byte, bool, error)
	Put(ns, key string, data []byte) error
}

// DiskCache is the local adapter: it stores each value as a single file at
// <Dir>/<ns>/<key>. Writes are atomic (temp file in the same dir + fsync +
// rename) so a crash mid-write never leaves a truncated value readable.
type DiskCache struct {
	Dir string       // cache root, e.g. "out/.cache"
	Log *slog.Logger // nil-safe => slog.Default()
}

// Get reads the value stored under ns/key. A missing entry is a clean miss
// (nil, false, nil); any other read error is returned.
func (d *DiskCache) Get(ns, key string) ([]byte, bool, error) {
	data, err := os.ReadFile(filepath.Join(d.Dir, ns, key))
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, false, nil
		}
		return nil, false, fmt.Errorf("cache: read %s/%s: %w", ns, key, err)
	}
	return data, true, nil
}

// Put atomically stores data under ns/key. It writes to a temp file in the same
// namespace directory (so os.Rename stays on one filesystem and is atomic),
// fsyncs it, then renames it into place.
func (d *DiskCache) Put(ns, key string, data []byte) error {
	dir := filepath.Join(d.Dir, ns)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("cache: create dir %s: %w", dir, err)
	}

	tmp, err := os.CreateTemp(dir, key+".tmp-*")
	if err != nil {
		return fmt.Errorf("cache: create temp in %s: %w", dir, err)
	}
	tmpName := tmp.Name()

	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		os.Remove(tmpName)
		return fmt.Errorf("cache: write temp %s: %w", tmpName, err)
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		os.Remove(tmpName)
		return fmt.Errorf("cache: sync temp %s: %w", tmpName, err)
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmpName)
		return fmt.Errorf("cache: close temp %s: %w", tmpName, err)
	}

	if err := os.Rename(tmpName, filepath.Join(dir, key)); err != nil {
		os.Remove(tmpName)
		return fmt.Errorf("cache: rename %s: %w", tmpName, err)
	}
	return nil
}

// compile-time check that the local adapter satisfies the seam.
var _ Cache = (*DiskCache)(nil)

// nopCache is the nil-safe fallback: it always misses and never stores, so a
// Runner with no Cache behaves exactly as it would with caching disabled.
type nopCache struct{}

func (nopCache) Get(ns, key string) ([]byte, bool, error) { return nil, false, nil }
func (nopCache) Put(ns, key string, data []byte) error    { return nil }

var _ Cache = nopCache{}

// hashKey returns a hex SHA-256 over the given parts, LENGTH-PREFIXING each part
// (8-byte big-endian length + bytes) so that concatenation is unambiguous
// (hashKey("a","bc") != hashKey("ab","c")).
func hashKey(parts ...[]byte) string {
	h := sha256.New()
	var lenBuf [8]byte
	for _, p := range parts {
		binary.BigEndian.PutUint64(lenBuf[:], uint64(len(p)))
		h.Write(lenBuf[:])
		h.Write(p)
	}
	return hex.EncodeToString(h.Sum(nil))
}

// narrationCacheKey keys the narration cache on the Weave/Prepare inputs: the
// Gemini model plus every page's text and image bytes/mimes (in order). It is
// independent of TTS config, since Weave/Prepare do not depend on the voice.
func narrationCacheKey(geminiModel string, pages []describe.PageContent) string {
	// narration-v2: bumped when chunked preparation landed. v1 entries hold
	// truncated/summarized narration from the single-call Prepare bug and must
	// miss so a resume regenerates the full text.
	parts := [][]byte{[]byte("narration-v2"), []byte(geminiModel)}
	for _, page := range pages {
		parts = append(parts, []byte(page.Text))
		for _, img := range page.Images {
			parts = append(parts, img.Data, []byte(img.MimeType))
		}
	}
	return hashKey(parts...)
}

// segmentCacheKey keys the segment-audio cache on the segment text plus the TTS
// fingerprint (model|voice|style). It is independent of the Gemini model, which
// is already encoded transitively through the segment text via adapted.
func segmentCacheKey(ttsFingerprint, segText string) string {
	return hashKey([]byte("segment-v1"), []byte(ttsFingerprint), []byte(segText))
}

// chapterCacheKey keys the chapter-completion record on the adapted narration
// plus the TTS fingerprint.
func chapterCacheKey(ttsFingerprint, adapted string) string {
	return hashKey([]byte("chapter-v1"), []byte(ttsFingerprint), []byte(adapted))
}

// artCacheKey keys the cover-art cache on the art mode, IMAGE model, chapter
// title, and one-line summary — the inputs that determine a generated image.
// Generation is non-deterministic AND paid, so caching (like narration) lets a
// resume reuse the exact image.
func artCacheKey(mode, model, title, summary string) string {
	return hashKey([]byte("art-v1"), []byte(mode), []byte(model), []byte(title), []byte(summary))
}

// cachedArt is the cover image as stored in the "art" cache namespace. Data is
// base64-encoded by encoding/json, which is fine for small cover images.
type cachedArt struct {
	MIMEType string `json:"mime"`
	Data     []byte `json:"data"`
}

// encodeCachedArt serializes an art.Image for the cache.
func encodeCachedArt(img art.Image) ([]byte, error) {
	return json.Marshal(cachedArt{MIMEType: img.MIMEType, Data: img.Data})
}

// decodeCachedArt deserializes a cached cover image. It errors on malformed data
// or empty bytes so the caller falls back to regeneration.
func decodeCachedArt(data []byte) (art.Image, error) {
	var ca cachedArt
	if err := json.Unmarshal(data, &ca); err != nil {
		return art.Image{}, err
	}
	if len(ca.Data) == 0 {
		return art.Image{}, fmt.Errorf("cache: empty cover image")
	}
	return art.Image{Data: ca.Data, MIMEType: ca.MIMEType}, nil
}

// cachedChapter is the tiny completion record stored under the "chapters"
// namespace so a resume can rebuild a chapter's manifest entry without
// re-synthesizing, re-concatenating, or re-probing.
type cachedChapter struct {
	File            string  `json:"file"`             // basename, e.g. "chapter_01.mp3"
	DurationSeconds float64 `json:"duration_seconds"` // so resume skips re-probing
	Image           string  `json:"image,omitempty"`  // cover-art sidecar basename, e.g. "chapter_01.png"; empty when no art
}
