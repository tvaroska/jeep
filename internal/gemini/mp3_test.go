package gemini

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

func TestPCMToMP3ProducesValidMP3(t *testing.T) {
	if _, err := exec.LookPath(ffmpegBin); err != nil {
		t.Skip("ffmpeg not on PATH; skipping MP3 encode test")
	}
	if _, err := exec.LookPath("ffprobe"); err != nil {
		t.Skip("ffprobe not on PATH; skipping MP3 encode test")
	}

	// ~1s of silence: 24000 mono 16-bit samples = 24000*2 zero bytes.
	pcm := make([]byte, 24000*2)

	mp3, err := PCMToMP3(context.Background(), pcm, 24000, 1)
	if err != nil {
		t.Fatalf("PCMToMP3: %v", err)
	}
	if len(mp3) == 0 {
		t.Fatal("PCMToMP3 returned no bytes")
	}
	// MP3 frame sync (0xFF 0xFB/0xF3/0xF2...) or an ID3 tag.
	if !(mp3[0] == 0xFF || strings.HasPrefix(string(mp3[:min(3, len(mp3))]), "ID3")) {
		t.Errorf("output does not start with a plausible MP3/ID3 marker: % x", mp3[:min(4, len(mp3))])
	}

	dir := t.TempDir()
	path := filepath.Join(dir, "out.mp3")
	if err := os.WriteFile(path, mp3, 0644); err != nil {
		t.Fatalf("writing mp3: %v", err)
	}

	out, err := exec.Command("ffprobe", "-v", "error",
		"-show_entries", "format=duration",
		"-of", "default=nw=1:nk=1", path).Output()
	if err != nil {
		t.Fatalf("ffprobe: %v", err)
	}
	dur, err := strconv.ParseFloat(strings.TrimSpace(string(out)), 64)
	if err != nil {
		t.Fatalf("parsing ffprobe duration %q: %v", string(out), err)
	}
	if dur <= 0 {
		t.Errorf("ffprobe duration = %v, want > 0", dur)
	}
}

func TestPCMToMP3EmptyInput(t *testing.T) {
	if _, err := PCMToMP3(context.Background(), nil, 24000, 1); err == nil {
		t.Fatal("expected an error for empty PCM input")
	}
}
