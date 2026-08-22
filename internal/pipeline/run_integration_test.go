package pipeline

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"

	"google.golang.org/genai"

	"github.com/tvaroska/jeep/internal/audio"
	"github.com/tvaroska/jeep/internal/describe"
	"github.com/tvaroska/jeep/internal/gemini"
	"github.com/tvaroska/jeep/internal/ingest"
)

// requireFFmpeg skips the test unless both ffmpeg and ffprobe are on PATH; these
// integration tests exercise the real audio toolchain, not fakes.
func requireFFmpeg(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		t.Skip("ffmpeg not installed; skipping audio integration test")
	}
	if _, err := exec.LookPath("ffprobe"); err != nil {
		t.Skip("ffprobe not installed; skipping audio integration test")
	}
}

// intgSinePCM returns seconds of signed 16-bit little-endian mono PCM at 24kHz,
// a 440Hz tone at roughly -6 dBFS so loudnorm has real signal to bring to target.
func intgSinePCM(seconds float64) []byte {
	const rate = 24000
	n := int(float64(rate) * seconds)
	buf := make([]byte, n*2)
	for i := 0; i < n; i++ {
		v := int16(16000 * math.Sin(2*math.Pi*440*float64(i)/float64(rate)))
		binary.LittleEndian.PutUint16(buf[i*2:], uint16(v))
	}
	return buf
}

// realAudioSynth is a fake Synthesizer that returns genuine, decodable MP3 bytes
// (a fixed-duration sine encoded via the real PCM->MP3 path), so integration
// tests can concat + normalize + probe against actual audio. It counts calls so
// resume behavior can be asserted.
type realAudioSynth struct {
	mu      sync.Mutex
	seconds float64
	calls   int
}

func (s *realAudioSynth) Synthesize(ctx context.Context, text string) ([]byte, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	s.mu.Lock()
	s.calls++
	s.mu.Unlock()
	secs := s.seconds
	if secs <= 0 {
		secs = 4.0
	}
	return gemini.PCMToMP3(ctx, intgSinePCM(secs), 24000, 1)
}

func (s *realAudioSynth) callCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.calls
}

// resplitAudioSynth rejects any text longer than maxOK with a Vertex 400 (as the
// TTS model does for token-dense segments) and otherwise returns real MP3 audio.
// It records rejections so the re-split path can be asserted.
type resplitAudioSynth struct {
	mu       sync.Mutex
	maxOK    int
	seconds  float64
	rejected int
}

func (s *resplitAudioSynth) Synthesize(ctx context.Context, text string) ([]byte, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	s.mu.Lock()
	over := len(text) > s.maxOK
	if over {
		s.rejected++
	}
	s.mu.Unlock()
	if over {
		return nil, fmt.Errorf("generating speech: %w", genai.APIError{Code: 400, Message: "oversized"})
	}
	secs := s.seconds
	if secs <= 0 {
		secs = 4.0
	}
	return gemini.PCMToMP3(ctx, intgSinePCM(secs), 24000, 1)
}

// intgDoc returns a Document that Structure splits into two chapters with real
// prose, no images (images require Gemini describe, out of scope for an offline
// integration test).
func intgDoc() *ingest.Document {
	return &ingest.Document{
		Meta: ingest.Meta{Path: "book.pdf"},
		Sections: []*ingest.Section{
			{Title: "Chapter One", Level: 1, Blocks: []ingest.Block{
				{Kind: ingest.BlockProse, Text: "The opening chapter sets the scene with calm narration.", Page: 1},
			}},
			{Title: "Chapter Two", Level: 1, Blocks: []ingest.Block{
				{Kind: ingest.BlockProse, Text: "The second chapter continues the story with more detail.", Page: 2},
			}},
		},
	}
}

// measureIntegratedLoudness runs ffmpeg's loudnorm analysis (print_format=json)
// on path and returns the measured integrated loudness (input_i) in LUFS.
func measureIntegratedLoudness(t *testing.T, path string) float64 {
	t.Helper()
	cmd := exec.Command("ffmpeg",
		"-hide_banner", "-nostdin",
		"-i", path,
		"-af", "loudnorm=I=-16:TP=-1.5:LRA=11:print_format=json",
		"-f", "null", "-",
	)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		t.Fatalf("loudnorm analysis failed: %v\n%s", err, stderr.String())
	}
	out := stderr.String()
	// loudnorm prints a single JSON object near the end of stderr.
	start := strings.LastIndex(out, "{")
	end := strings.LastIndex(out, "}")
	if start < 0 || end < 0 || end <= start {
		t.Fatalf("no loudnorm JSON in output:\n%s", out)
	}
	var parsed struct {
		InputI string `json:"input_i"`
	}
	if err := json.Unmarshal([]byte(out[start:end+1]), &parsed); err != nil {
		t.Fatalf("parse loudnorm JSON %q: %v", out[start:end+1], err)
	}
	li, err := strconv.ParseFloat(parsed.InputI, 64)
	if err != nil {
		t.Fatalf("parse input_i %q: %v", parsed.InputI, err)
	}
	return li
}

func TestPipeline_Integration(t *testing.T) {
	requireFFmpeg(t)

	t.Run("EndToEndWithLoudnessAndResume", func(t *testing.T) {
		outDir := t.TempDir()
		cache := &DiskCache{Dir: filepath.Join(outDir, ".cache")}
		synth := &realAudioSynth{seconds: 4.0}
		runner := &Runner{
			Source:         &fakeSource{doc: intgDoc()},
			Weaver:         &describe.Weaver{Describer: describe.StubDescriber{}},
			Preparer:       describe.StubPreparer{},
			Synthesizer:    synth,
			Concatenator:   &audio.FFmpegConcatenator{Normalize: true},
			Prober:         &FFprobeProber{},
			Cache:          cache,
			GeminiModel:    "stub",
			TTSFingerprint: "m|v|s",
		}

		manifestPath, err := runner.Run(context.Background(), Options{InputPath: "book.pdf", OutputDir: outDir})
		if err != nil {
			t.Fatalf("Run failed: %v", err)
		}

		// (a) manifest.json parses and has both chapters with positive durations.
		m := readManifest(t, manifestPath)
		if len(m.Chapters) != 2 {
			t.Fatalf("manifest chapters: got %d, want 2", len(m.Chapters))
		}
		for i, e := range m.Chapters {
			if e.DurationSeconds <= 0 {
				t.Errorf("chapter %d duration: got %v, want > 0", i, e.DurationSeconds)
			}
		}

		// (b) chapter_01.mp3 exists with a positive ffprobe duration.
		ch1 := filepath.Join(outDir, "chapter_01.mp3")
		dur, err := (&FFprobeProber{}).Duration(context.Background(), ch1)
		if err != nil {
			t.Fatalf("probe chapter_01.mp3: %v", err)
		}
		if dur.Seconds() <= 0 {
			t.Errorf("chapter_01.mp3 duration: got %v, want > 0", dur)
		}

		// (c) measured integrated loudness within +/-2 LU of the -16 LUFS target.
		li := measureIntegratedLoudness(t, ch1)
		if math.Abs(li-(-16.0)) > 2.0 {
			t.Errorf("integrated loudness = %.2f LUFS, want within 2 LU of -16", li)
		}

		// (d) re-run over the same cache does no new synthesis work.
		callsAfterFirst := synth.callCount()
		if callsAfterFirst == 0 {
			t.Fatal("first run should have synthesized at least one segment")
		}
		if _, err := runner.Run(context.Background(), Options{InputPath: "book.pdf", OutputDir: outDir}); err != nil {
			t.Fatalf("resume Run failed: %v", err)
		}
		if grew := synth.callCount() - callsAfterFirst; grew != 0 {
			t.Errorf("resume synthesized %d new segments, want 0 (cache reuse)", grew)
		}
	})

	t.Run("ResplitProducesValidNormalizedMP3", func(t *testing.T) {
		outDir := t.TempDir()
		doc := &ingest.Document{
			Meta: ingest.Meta{Path: "book.pdf"},
			Sections: []*ingest.Section{
				{Title: "Chapter One", Level: 1, Blocks: []ingest.Block{
					{Kind: ingest.BlockProse, Text: "This chapter has a single long sentence that the model rejects as too dense until it is re-split into smaller pieces.", Page: 1},
				}},
			},
		}
		full := doc.Sections[0].Blocks[0].Text
		// Reject the whole segment once (forcing exactly one re-split), accept halves.
		synth := &resplitAudioSynth{maxOK: len(full) / 2, seconds: 4.0}
		runner := &Runner{
			Source:         &fakeSource{doc: doc},
			Weaver:         &describe.Weaver{Describer: describe.StubDescriber{}},
			Preparer:       describe.StubPreparer{},
			Synthesizer:    synth,
			Concatenator:   &audio.FFmpegConcatenator{Normalize: true},
			Prober:         &FFprobeProber{},
			Cache:          &DiskCache{Dir: filepath.Join(outDir, ".cache")},
			TTSFingerprint: "m|v|s",
		}

		manifestPath, err := runner.Run(context.Background(), Options{InputPath: "book.pdf", OutputDir: outDir})
		if err != nil {
			t.Fatalf("Run failed: %v", err)
		}
		if synth.rejected != 1 {
			t.Errorf("expected exactly one oversized rejection driving a re-split, got %d", synth.rejected)
		}

		m := readManifest(t, manifestPath)
		if len(m.Chapters) != 1 {
			t.Fatalf("manifest chapters: got %d, want 1", len(m.Chapters))
		}

		ch1 := filepath.Join(outDir, "chapter_01.mp3")
		if _, err := os.Stat(ch1); err != nil {
			t.Fatalf("chapter_01.mp3 missing: %v", err)
		}
		dur, err := (&FFprobeProber{}).Duration(context.Background(), ch1)
		if err != nil || dur.Seconds() <= 0 {
			t.Fatalf("chapter_01.mp3 not a valid audio file: dur=%v err=%v", dur, err)
		}
		li := measureIntegratedLoudness(t, ch1)
		if math.Abs(li-(-16.0)) > 2.0 {
			t.Errorf("re-split output integrated loudness = %.2f LUFS, want within 2 LU of -16", li)
		}
	})
}
