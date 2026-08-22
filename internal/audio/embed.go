package audio

import (
	"bytes"
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
)

// ArtEmbedder embeds a cover image into an existing MP3 as an ID3v2 APIC
// ("attached picture") frame. Like Concatenator it is a LOCAL tool boundary
// (ffmpeg via os/exec), not a managed service, so it is a seam with a local
// adapter only (no GCP adapter). It is injectable so the pipeline can be tested
// with a real FFmpegArtEmbedder under an ffmpeg guard.
type ArtEmbedder interface {
	Embed(ctx context.Context, mp3Path string, image []byte, mimeType string) error
}

// FFmpegArtEmbedder muxes a cover image into an MP3 as an attached picture via
// ffmpeg. ffmpeg cannot rewrite a file in place, so it writes to a temp file in
// the same directory and renames it over the original (an atomic same-dir move).
// It mirrors FFmpegConcatenator's exec idiom: -hide_banner -nostdin -y, captured
// stderr, tail on error, nil-safe logger.
type FFmpegArtEmbedder struct {
	FFmpegPath string       // "" => "ffmpeg" (resolved on PATH)
	Log        *slog.Logger // nil-safe => slog.Default()
}

// imageExt maps a cover image MIME type to a file extension ffmpeg's demuxer can
// recognize from the path. Unknown types fall back to .png (the gemini image
// model returns PNG).
func imageExt(mimeType string) string {
	switch mimeType {
	case "image/jpeg", "image/jpg":
		return ".jpg"
	case "image/webp":
		return ".webp"
	default:
		return ".png"
	}
}

// Embed writes image into mp3Path as an ID3v2 APIC frame. It is a no-op error if
// image is empty. The MP3 audio stream is copied verbatim (-c:a copy), so only
// the container is rewritten; -disposition:v attached_pic is what makes ffprobe
// report the image as an attached picture.
func (f *FFmpegArtEmbedder) Embed(ctx context.Context, mp3Path string, image []byte, mimeType string) error {
	if len(image) == 0 {
		return fmt.Errorf("embed: no image data")
	}

	bin := f.FFmpegPath
	if bin == "" {
		bin = "ffmpeg"
	}

	dir := filepath.Dir(mp3Path)

	// Write the cover image to a temp file: ffmpeg needs a real, demuxable path
	// for the second input and -map 1:v.
	imgFile, err := os.CreateTemp(dir, "jeep-art-*"+imageExt(mimeType))
	if err != nil {
		return fmt.Errorf("embed: create temp image: %w", err)
	}
	imgPath := imgFile.Name()
	defer os.Remove(imgPath)
	if _, err := imgFile.Write(image); err != nil {
		imgFile.Close()
		return fmt.Errorf("embed: write temp image: %w", err)
	}
	if err := imgFile.Close(); err != nil {
		return fmt.Errorf("embed: close temp image: %w", err)
	}

	// Output to a temp mp3 in the same dir, then rename over the original.
	outFile, err := os.CreateTemp(dir, "jeep-embed-*.mp3")
	if err != nil {
		return fmt.Errorf("embed: create temp mp3: %w", err)
	}
	outPath := outFile.Name()
	outFile.Close()
	defer os.Remove(outPath) // no-op after a successful rename

	args := []string{
		"-hide_banner", "-nostdin",
		"-i", mp3Path,
		"-i", imgPath,
		"-map", "0:a", "-map", "1:v",
		"-c:a", "copy", "-c:v", "copy",
		"-id3v2_version", "3",
		"-metadata:s:v", "title=Album cover",
		"-metadata:s:v", "comment=Cover (front)",
		"-disposition:v", "attached_pic",
		"-y", outPath,
	}

	cmd := exec.CommandContext(ctx, bin, args...)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		tail := tailString(stderr.String(), 500)
		return fmt.Errorf("embed: ffmpeg failed: %w: %s", err, tail)
	}

	if err := os.Rename(outPath, mp3Path); err != nil {
		return fmt.Errorf("embed: rename %s -> %s: %w", outPath, mp3Path, err)
	}

	f.logger().Info("embedded cover art", "mp3", mp3Path, "bytes", len(image))
	return nil
}

// logger returns f.Log if non-nil, else slog.Default().
func (f *FFmpegArtEmbedder) logger() *slog.Logger {
	if f.Log != nil {
		return f.Log
	}
	return slog.Default()
}

// compile-time check that the local adapter satisfies the seam.
var _ ArtEmbedder = (*FFmpegArtEmbedder)(nil)
