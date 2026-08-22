package audio

import (
	"bytes"
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// loudnormFilter is the per-segment EBU R128 loudness normalization applied when
// FFmpegConcatenator.Normalize is set. Target -16 LUFS integrated / -1.5 dBTP is
// the common podcast/spoken-word loudness. Each segment is a separate, stateless
// Gemini-TTS call whose loudness the model sets per request, so an isolated
// image-description segment can otherwise be audibly louder/quieter than the body
// prose around it; normalizing each segment to a common target evens this out.
// aformat pins a uniform sample rate / channel layout so the concat filter can
// join loudnorm's (possibly resampled) outputs without a mismatch.
const loudnormFilter = "loudnorm=I=-16:TP=-1.5:LRA=11,aformat=sample_rates=44100:channel_layouts=mono"

// Concatenator stitches ordered, already-encoded audio segments into a single
// output file. ffmpeg is the only implementation today; it is a local tool
// (os/exec), not a managed service, so there is no GCP adapter.
type Concatenator interface {
	Concat(ctx context.Context, segments [][]byte, outPath string) error
}

// FFmpegConcatenator concatenates MP3 segments into one MP3 via the ffmpeg
// concat demuxer, re-encoding with libmp3lame for a clean, seamless stream.
type FFmpegConcatenator struct {
	FFmpegPath string       // "" => "ffmpeg" (resolved on PATH)
	Log        *slog.Logger // nil-safe => slog.Default()

	// Normalize, when true, applies per-segment EBU R128 loudness normalization
	// (see loudnormFilter) before stitching, so segment-to-segment volume is even
	// and the chapter hits a consistent podcast loudness target. When false,
	// segments are concatenated as-is (original behavior; the single-segment fast
	// path copies bytes verbatim).
	Normalize bool
}

// Concat stitches segments into a single MP3 file at outPath. Empty segments
// or zero-length segments are rejected. Creates the output parent directory if
// needed. Uses a temporary directory for intermediate files, cleaned on return.
func (f *FFmpegConcatenator) Concat(ctx context.Context, segments [][]byte, outPath string) error {
	// Guard: no segments
	if len(segments) == 0 {
		return fmt.Errorf("concat: no segments to concatenate")
	}

	// Guard: zero-length segment
	for i, seg := range segments {
		if len(seg) == 0 {
			return fmt.Errorf("concat: segment %d is empty", i)
		}
	}

	// Single-segment fast path: only when not normalizing. With Normalize on, a
	// lone segment still goes through ffmpeg so it hits the same loudness target
	// as multi-segment chapters (otherwise single- and multi-segment chapters
	// would sit at different volumes).
	if len(segments) == 1 && !f.Normalize {
		if err := os.MkdirAll(filepath.Dir(outPath), 0o755); err != nil {
			return fmt.Errorf("concat: create output dir: %w", err)
		}
		if err := os.WriteFile(outPath, segments[0], 0o644); err != nil {
			return fmt.Errorf("concat: write single segment: %w", err)
		}
		f.logger().Info("wrote single segment", "out", outPath)
		return nil
	}

	// Resolve ffmpeg binary
	bin := f.FFmpegPath
	if bin == "" {
		bin = "ffmpeg"
	}

	// Ensure output parent directory exists
	if err := os.MkdirAll(filepath.Dir(outPath), 0o755); err != nil {
		return fmt.Errorf("concat: create output dir: %w", err)
	}

	// Create temp working directory
	tmp, err := os.MkdirTemp("", "jeep-concat-*")
	if err != nil {
		return fmt.Errorf("concat: create temp dir: %w", err)
	}
	defer os.RemoveAll(tmp)

	// Write each segment to temp files and collect absolute paths
	var segPaths []string
	for i, seg := range segments {
		segPath := filepath.Join(tmp, fmt.Sprintf("seg_%04d.mp3", i))
		if err := os.WriteFile(segPath, seg, 0o644); err != nil {
			return fmt.Errorf("concat: write segment %d: %w", i, err)
		}
		absPath, err := filepath.Abs(segPath)
		if err != nil {
			return fmt.Errorf("concat: resolve abs path for segment %d: %w", i, err)
		}
		segPaths = append(segPaths, absPath)
	}

	// Build the ffmpeg args. With normalization on, each segment is decoded as a
	// separate input, run through loudnorm, and joined with the concat filter in a
	// single pass. Without it, the lighter concat demuxer stitches the segments
	// via a list file and only re-encodes.
	var args []string
	if f.Normalize {
		args = normalizeConcatArgs(segPaths, outPath)
	} else {
		listPath := filepath.Join(tmp, "list.txt")
		var listBuf bytes.Buffer
		for _, p := range segPaths {
			fmt.Fprintf(&listBuf, "file '%s'\n", p)
		}
		if err := os.WriteFile(listPath, listBuf.Bytes(), 0o644); err != nil {
			return fmt.Errorf("concat: write list file: %w", err)
		}
		args = []string{
			"-hide_banner",
			"-nostdin",
			"-f", "concat",
			"-safe", "0",
			"-i", listPath,
			"-c:a", "libmp3lame",
			"-q:a", "4",
			"-y",
			outPath,
		}
	}

	cmd := exec.CommandContext(ctx, bin, args...)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		tail := tailString(stderr.String(), 500)
		return fmt.Errorf("concat: ffmpeg failed: %w: %s", err, tail)
	}

	f.logger().Info("concatenated segments", "count", len(segments), "out", outPath)
	return nil
}

// normalizeConcatArgs builds an ffmpeg command that decodes each segment as a
// separate input, normalizes it with loudnormFilter, and joins the results with
// the concat filter in one pass (single encode). Works for any count >= 1, so a
// lone segment is normalized to the same target as a multi-segment chapter.
func normalizeConcatArgs(segPaths []string, outPath string) []string {
	args := []string{"-hide_banner", "-nostdin"}
	for _, p := range segPaths {
		args = append(args, "-i", p)
	}

	var fb strings.Builder
	for i := range segPaths {
		fmt.Fprintf(&fb, "[%d:a]%s[a%d];", i, loudnormFilter, i)
	}
	for i := range segPaths {
		fmt.Fprintf(&fb, "[a%d]", i)
	}
	fmt.Fprintf(&fb, "concat=n=%d:v=0:a=1[out]", len(segPaths))

	args = append(args,
		"-filter_complex", fb.String(),
		"-map", "[out]",
		"-c:a", "libmp3lame",
		"-q:a", "4",
		"-y",
		outPath,
	)
	return args
}

// logger returns f.Log if non-nil, else slog.Default()
func (f *FFmpegConcatenator) logger() *slog.Logger {
	if f.Log != nil {
		return f.Log
	}
	return slog.Default()
}

// tailString returns the last maxBytes bytes of s, or all of s if shorter.
func tailString(s string, maxBytes int) string {
	if len(s) <= maxBytes {
		return s
	}
	return s[len(s)-maxBytes:]
}
