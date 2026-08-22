package audio

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// Compile-time check: FFmpegConcatenator implements Concatenator
var _ Concatenator = (*FFmpegConcatenator)(nil)

// mkMP3 generates a simple MP3 audio segment of the given duration using ffmpeg.
// Skips the test if ffmpeg is not installed.
func mkMP3(t *testing.T, seconds float64) []byte {
	t.Helper()

	if _, err := exec.LookPath("ffmpeg"); err != nil {
		t.Skip("ffmpeg not installed")
	}

	tmpFile := filepath.Join(t.TempDir(), "test.mp3")
	cmd := exec.Command("ffmpeg",
		"-hide_banner",
		"-f", "lavfi",
		"-i", "sine=frequency=440:duration="+fmt.Sprintf("%.2f", seconds),
		"-c:a", "libmp3lame",
		"-q:a", "9",
		tmpFile,
	)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		t.Fatalf("mkMP3: ffmpeg failed: %v: %s", err, stderr.String())
	}

	data, err := os.ReadFile(tmpFile)
	if err != nil {
		t.Fatalf("mkMP3: read file: %v", err)
	}
	return data
}

func TestFFmpegConcatenator_HappyPath(t *testing.T) {
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		t.Skip("ffmpeg not installed")
	}

	// Generate two small MP3 segments
	seg1 := mkMP3(t, 0.5)
	seg2 := mkMP3(t, 0.5)

	outPath := filepath.Join(t.TempDir(), "out.mp3")
	cat := &FFmpegConcatenator{}

	err := cat.Concat(context.Background(), [][]byte{seg1, seg2}, outPath)
	if err != nil {
		t.Fatalf("Concat failed: %v", err)
	}

	// Verify output file exists and has content
	info, err := os.Stat(outPath)
	if err != nil {
		t.Fatalf("output file not found: %v", err)
	}
	if info.Size() == 0 {
		t.Fatal("output file is empty")
	}
}

func TestFFmpegConcatenator_SingleSegment(t *testing.T) {
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		t.Skip("ffmpeg not installed")
	}

	seg := mkMP3(t, 0.5)
	outPath := filepath.Join(t.TempDir(), "single.mp3")
	cat := &FFmpegConcatenator{}

	err := cat.Concat(context.Background(), [][]byte{seg}, outPath)
	if err != nil {
		t.Fatalf("Concat failed: %v", err)
	}

	// Verify output file exists and has same size as input
	data, err := os.ReadFile(outPath)
	if err != nil {
		t.Fatalf("output file not found: %v", err)
	}
	if len(data) != len(seg) {
		t.Errorf("single-segment output size mismatch: got %d, want %d", len(data), len(seg))
	}
}

func TestFFmpegConcatenator_NormalizeMultiSegment(t *testing.T) {
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		t.Skip("ffmpeg not installed")
	}

	seg1 := mkMP3(t, 0.5)
	seg2 := mkMP3(t, 0.5)
	outPath := filepath.Join(t.TempDir(), "norm.mp3")
	cat := &FFmpegConcatenator{Normalize: true}

	if err := cat.Concat(context.Background(), [][]byte{seg1, seg2}, outPath); err != nil {
		t.Fatalf("Concat (normalize) failed: %v", err)
	}

	// Output must be a decodable MP3 (ffprobe reports a positive duration).
	if d := probeDuration(t, outPath); d <= 0 {
		t.Errorf("normalized output has non-positive duration %.3fs", d)
	}
}

func TestFFmpegConcatenator_NormalizeSingleSegment(t *testing.T) {
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		t.Skip("ffmpeg not installed")
	}

	// With Normalize on, a lone segment must still go through ffmpeg (not the raw
	// byte-copy fast path) so it hits the same loudness target as multi-segment
	// chapters. It should decode cleanly and NOT be a verbatim copy of the input.
	seg := mkMP3(t, 0.5)
	outPath := filepath.Join(t.TempDir(), "norm-single.mp3")
	cat := &FFmpegConcatenator{Normalize: true}

	if err := cat.Concat(context.Background(), [][]byte{seg}, outPath); err != nil {
		t.Fatalf("Concat (normalize single) failed: %v", err)
	}

	data, err := os.ReadFile(outPath)
	if err != nil {
		t.Fatalf("output file not found: %v", err)
	}
	if bytes.Equal(data, seg) {
		t.Error("normalize single-segment output is a verbatim copy; loudnorm was not applied")
	}
	if d := probeDuration(t, outPath); d <= 0 {
		t.Errorf("normalized single output has non-positive duration %.3fs", d)
	}
}

// probeDuration returns the container duration (seconds) of an audio file via
// ffprobe, or fails the test.
func probeDuration(t *testing.T, path string) float64 {
	t.Helper()
	if _, err := exec.LookPath("ffprobe"); err != nil {
		t.Skip("ffprobe not installed")
	}
	out, err := exec.Command("ffprobe",
		"-v", "error",
		"-show_entries", "format=duration",
		"-of", "default=noprint_wrappers=1:nokey=1",
		path,
	).Output()
	if err != nil {
		t.Fatalf("ffprobe failed: %v", err)
	}
	var d float64
	if _, err := fmt.Sscanf(string(bytes.TrimSpace(out)), "%f", &d); err != nil {
		t.Fatalf("parse ffprobe duration %q: %v", string(out), err)
	}
	return d
}

func TestFFmpegConcatenator_EmptySegmentsSlice(t *testing.T) {
	cat := &FFmpegConcatenator{}
	err := cat.Concat(context.Background(), [][]byte{}, "/tmp/out.mp3")
	if err == nil {
		t.Fatal("expected error for empty segments slice, got nil")
	}
	if !contains(err.Error(), "no segments") {
		t.Errorf("unexpected error message: %v", err)
	}
}

func TestFFmpegConcatenator_ZeroLengthSegment(t *testing.T) {
	cat := &FFmpegConcatenator{}
	segments := [][]byte{[]byte("valid"), []byte{}}
	err := cat.Concat(context.Background(), segments, "/tmp/out.mp3")
	if err == nil {
		t.Fatal("expected error for zero-length segment, got nil")
	}
	if !contains(err.Error(), "segment 1 is empty") {
		t.Errorf("unexpected error message: %v", err)
	}
}

func TestFFmpegConcatenator_BadFFmpegPath(t *testing.T) {
	cat := &FFmpegConcatenator{FFmpegPath: "/nonexistent/ffmpeg"}
	// Use multiple segments to avoid the single-segment fast path
	segments := [][]byte{[]byte("fake-mp3-data-1"), []byte("fake-mp3-data-2")}
	err := cat.Concat(context.Background(), segments, filepath.Join(t.TempDir(), "out.mp3"))
	if err == nil {
		t.Fatal("expected error for bad ffmpeg path, got nil")
	}
	if !contains(err.Error(), "ffmpeg") {
		t.Errorf("expected error to mention ffmpeg: %v", err)
	}
}

// contains reports whether s contains substr.
func contains(s, substr string) bool {
	return bytes.Contains([]byte(s), []byte(substr))
}
