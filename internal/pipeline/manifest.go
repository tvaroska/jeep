package pipeline

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// Manifest is the machine-readable index of a generated audiobook.
type Manifest struct {
	Source      string         `json:"source"`       // input path (opts.InputPath)
	GeneratedAt time.Time      `json:"generated_at"` // time.Now().UTC()
	Chapters    []ChapterEntry `json:"chapters"`
}

// ChapterEntry describes one produced per-chapter audio file.
type ChapterEntry struct {
	Title           string  `json:"title"`            // chapter title; fallback "Chapter N"
	File            string  `json:"file"`             // basename, e.g. "chapter_01.mp3"
	DurationSeconds float64 `json:"duration_seconds"` // 0 if probing failed
	StartPage       int     `json:"start_page"`       // 1-based inclusive; 0 for page-less formats
	EndPage         int     `json:"end_page"`         // 1-based inclusive; 0 for page-less formats
}

// WriteManifest serializes m to path as indented JSON (with a trailing newline),
// creating the parent directory if needed. It is a dumb serializer: title
// fallbacks and page ranges are decided by the caller.
func WriteManifest(path string, m Manifest) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("manifest: create output dir: %w", err)
	}
	b, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return fmt.Errorf("manifest: marshal: %w", err)
	}
	b = append(b, '\n')
	if err := os.WriteFile(path, b, 0o644); err != nil {
		return fmt.Errorf("manifest: write %q: %w", path, err)
	}
	return nil
}

// Prober reports the playback duration of an encoded audio file. It is a LOCAL
// tool boundary (ffprobe), not a managed service, so — mirroring
// audio.Concatenator/FFmpegConcatenator — it is a seam with a local adapter only
// (no GCP adapter). It is injectable so tests can avoid running ffprobe on the
// non-decodable stub audio the fakes produce.
type Prober interface {
	Duration(ctx context.Context, path string) (time.Duration, error)
}

// FFprobeProber reports audio duration by shelling out to ffprobe. It mirrors
// the exec idiom in audio.FFmpegConcatenator (CommandContext, captured stderr,
// tail on error).
type FFprobeProber struct {
	FFprobePath string       // "" => "ffprobe" (resolved on PATH)
	Log         *slog.Logger // nil-safe => slog.Default()
}

// Duration runs ffprobe on path and returns the file's playback duration.
func (p *FFprobeProber) Duration(ctx context.Context, path string) (time.Duration, error) {
	bin := p.FFprobePath
	if bin == "" {
		bin = "ffprobe"
	}

	args := []string{
		"-v", "error",
		"-show_entries", "format=duration",
		"-of", "default=noprint_wrappers=1:nokey=1",
		path,
	}

	cmd := exec.CommandContext(ctx, bin, args...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		tail := tailString(stderr.String(), 500)
		return 0, fmt.Errorf("probe: ffprobe failed: %w: %s", err, tail)
	}

	return parseFFprobeDuration(stdout.String())
}

// parseFFprobeDuration parses ffprobe's "format=duration" stdout (a single
// float in seconds) into a time.Duration. Empty or non-numeric output is an
// error. Factored out so it can be unit-tested without running ffprobe.
func parseFFprobeDuration(out string) (time.Duration, error) {
	s := strings.TrimSpace(out)
	if s == "" {
		return 0, fmt.Errorf("probe: empty ffprobe output")
	}
	secs, err := strconv.ParseFloat(s, 64)
	if err != nil {
		return 0, fmt.Errorf("probe: parse duration %q: %w", s, err)
	}
	return time.Duration(secs * float64(time.Second)), nil
}

// tailString is defined in package audio (concat.go) but pipeline cannot import
// an unexported helper, so a local copy lives here.
func tailString(s string, maxBytes int) string {
	if len(s) <= maxBytes {
		return s
	}
	return s[len(s)-maxBytes:]
}

// compile-time check that the local adapter satisfies the seam.
var _ Prober = (*FFprobeProber)(nil)
