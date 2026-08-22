package pipeline

import (
	"bytes"
	"context"
	"image"
	"image/color"
	"image/png"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tvaroska/jeep/internal/art"
	"github.com/tvaroska/jeep/internal/audio"
	"github.com/tvaroska/jeep/internal/describe"
	"github.com/tvaroska/jeep/internal/gemini"
)

// fakeArtImageClient satisfies art's package-local image client (structurally,
// via the exported GenerateImage signature) and returns a canned PNG.
type fakeArtImageClient struct {
	data  []byte
	calls int
}

func (f *fakeArtImageClient) GenerateImage(ctx context.Context, model, prompt string, inputs []gemini.FileInput, retries int) (*gemini.ImageResult, error) {
	f.calls++
	return &gemini.ImageResult{Data: f.data, MIMEType: "image/png"}, nil
}

func artTestPNG(t *testing.T) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, 8, 8))
	img.Set(0, 0, color.RGBA{R: 200, G: 50, B: 50, A: 255})
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatalf("encode png: %v", err)
	}
	return buf.Bytes()
}

// hasAttachedPicture reports whether path contains a video stream (an MP3's
// attached picture surfaces as a video stream to ffprobe).
func hasAttachedPicture(t *testing.T, path string) bool {
	t.Helper()
	cmd := exec.Command("ffprobe",
		"-v", "error",
		"-select_streams", "v",
		"-show_entries", "stream=codec_type",
		"-of", "default=noprint_wrappers=1:nokey=1",
		path,
	)
	var out bytes.Buffer
	cmd.Stdout = &out
	if err := cmd.Run(); err != nil {
		t.Fatalf("ffprobe: %v", err)
	}
	return strings.Contains(out.String(), "video")
}

func TestPipeline_IntegrationArt(t *testing.T) {
	requireFFmpeg(t)

	outDir := t.TempDir()
	fakeImg := &fakeArtImageClient{data: artTestPNG(t)}

	runner := &Runner{
		Source:         &fakeSource{doc: intgDoc()},
		Weaver:         &describe.Weaver{Describer: describe.StubDescriber{}},
		Preparer:       describe.StubPreparer{},
		Synthesizer:    &realAudioSynth{seconds: 2.0},
		Concatenator:   &audio.FFmpegConcatenator{Normalize: true},
		Prober:         &FFprobeProber{},
		Cache:          &DiskCache{Dir: filepath.Join(outDir, ".cache")},
		GeminiModel:    "stub",
		TTSFingerprint: "m|v|s",
		Art: &art.Provider{
			Client: fakeImg,
			Model:  "stub-image",
			Mode:   art.ModeGenerate,
		},
		ArtEmbedder: &audio.FFmpegArtEmbedder{},
	}

	manifestPath, err := runner.Run(context.Background(), Options{InputPath: "book.pdf", OutputDir: outDir})
	if err != nil {
		t.Fatalf("Run failed: %v", err)
	}

	// chapter_01.png sidecar was written.
	pngPath := filepath.Join(outDir, "chapter_01.png")
	if _, err := os.Stat(pngPath); err != nil {
		t.Fatalf("chapter_01.png sidecar missing: %v", err)
	}
	// And it is a valid PNG.
	b, err := os.ReadFile(pngPath)
	if err != nil {
		t.Fatalf("read sidecar: %v", err)
	}
	if _, _, err := image.Decode(bytes.NewReader(b)); err != nil {
		t.Errorf("sidecar is not a decodable image: %v", err)
	}

	// manifest records the sidecar for chapter 1.
	m := readManifest(t, manifestPath)
	if len(m.Chapters) == 0 {
		t.Fatal("manifest has no chapters")
	}
	if m.Chapters[0].Image != "chapter_01.png" {
		t.Errorf("manifest chapter 1 image = %q, want %q", m.Chapters[0].Image, "chapter_01.png")
	}

	// The mp3 gained an attached-picture (video) stream.
	if !hasAttachedPicture(t, filepath.Join(outDir, "chapter_01.mp3")) {
		t.Error("chapter_01.mp3 has no attached-picture video stream")
	}

	// The fake image client was actually used.
	if fakeImg.calls == 0 {
		t.Error("fake image client never called; art provider not wired")
	}
}
