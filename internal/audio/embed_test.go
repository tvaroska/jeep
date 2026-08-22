package audio

import (
	"bytes"
	"context"
	"encoding/binary"
	"image"
	"image/color"
	"image/png"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tvaroska/jeep/internal/gemini"
)

// embedSinePCM returns seconds of signed 16-bit LE mono PCM at 24kHz (a 440Hz
// tone), enough for the real PCM->MP3 path to produce a decodable file.
func embedSinePCM(seconds float64) []byte {
	const rate = 24000
	n := int(float64(rate) * seconds)
	buf := make([]byte, n*2)
	for i := 0; i < n; i++ {
		v := int16(16000 * math.Sin(2*math.Pi*440*float64(i)/float64(rate)))
		binary.LittleEndian.PutUint16(buf[i*2:], uint16(v))
	}
	return buf
}

// embedTinyPNG returns a valid small PNG for the cover image.
func embedTinyPNG(t *testing.T) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, 16, 16))
	for y := 0; y < 16; y++ {
		for x := 0; x < 16; x++ {
			img.Set(x, y, color.RGBA{R: uint8(x * 16), G: uint8(y * 16), B: 100, A: 255})
		}
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatalf("encode png: %v", err)
	}
	return buf.Bytes()
}

func TestFFmpegArtEmbedder(t *testing.T) {
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		t.Skip("ffmpeg not installed")
	}
	if _, err := exec.LookPath("ffprobe"); err != nil {
		t.Skip("ffprobe not installed")
	}

	ctx := context.Background()

	// Build a real MP3 via the shared PCM->MP3 path.
	mp3, err := gemini.PCMToMP3(ctx, embedSinePCM(1.0), 24000, 1)
	if err != nil {
		t.Fatalf("PCMToMP3: %v", err)
	}

	dir := t.TempDir()
	mp3Path := filepath.Join(dir, "chapter_01.mp3")
	if err := os.WriteFile(mp3Path, mp3, 0o644); err != nil {
		t.Fatalf("write mp3: %v", err)
	}

	embedder := &FFmpegArtEmbedder{}
	if err := embedder.Embed(ctx, mp3Path, embedTinyPNG(t), "image/png"); err != nil {
		t.Fatalf("Embed: %v", err)
	}

	// ffprobe should now report a video stream (the attached picture).
	cmd := exec.Command("ffprobe",
		"-v", "error",
		"-select_streams", "v",
		"-show_entries", "stream=codec_type",
		"-of", "default=noprint_wrappers=1:nokey=1",
		mp3Path,
	)
	var out bytes.Buffer
	cmd.Stdout = &out
	if err := cmd.Run(); err != nil {
		t.Fatalf("ffprobe: %v", err)
	}
	if !strings.Contains(out.String(), "video") {
		t.Errorf("ffprobe video streams = %q, want a video stream (attached_pic)", out.String())
	}
}

func TestFFmpegArtEmbedder_EmptyImage(t *testing.T) {
	err := (&FFmpegArtEmbedder{}).Embed(context.Background(), "nope.mp3", nil, "image/png")
	if err == nil {
		t.Error("Embed with empty image: want error, got nil")
	}
}
