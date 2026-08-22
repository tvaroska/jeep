package audio

import (
	"context"
	"encoding/binary"
	"fmt"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"google.golang.org/genai"

	"github.com/tvaroska/jeep/internal/gemini"
)

// fakeSpeechClient is a scripted speechClient for offline tests. It returns pcm
// as SpeechResult.PCMData on success, or err (returned as-is) when set.
type fakeSpeechClient struct {
	calls int
	pcm   []byte
	err   error
	last  struct{ model, voice, style, prompt string }
}

func (f *fakeSpeechClient) GenerateSpeech(_ context.Context, model, voice, style, prompt string, _ int) (*gemini.SpeechResult, error) {
	f.calls++
	f.last.model, f.last.voice, f.last.style, f.last.prompt = model, voice, style, prompt
	if f.err != nil {
		return nil, f.err
	}
	return &gemini.SpeechResult{PCMData: f.pcm, ModelVersion: "fake"}, nil
}

// sinePCM returns seconds of signed 16-bit little-endian mono PCM at 24kHz,
// a 440Hz tone — real, decodable audio for the PCM->MP3 encode path.
func sinePCM(seconds float64) []byte {
	const rate = 24000
	n := int(float64(rate) * seconds)
	buf := make([]byte, n*2)
	for i := 0; i < n; i++ {
		v := int16(8000 * math.Sin(2*math.Pi*440*float64(i)/float64(rate)))
		binary.LittleEndian.PutUint16(buf[i*2:], uint16(v))
	}
	return buf
}

func TestGeminiSynthesizer_ReturnsDecodableMP3(t *testing.T) {
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		t.Skip("ffmpeg not installed")
	}
	fake := &fakeSpeechClient{pcm: sinePCM(1.0)}
	g := &GeminiSynthesizer{Client: fake, Model: "m", Voice: "Kore", Style: "calm"}

	mp3, err := g.Synthesize(context.Background(), "hello world")
	if err != nil {
		t.Fatalf("Synthesize: %v", err)
	}
	if len(mp3) == 0 {
		t.Fatal("Synthesize returned empty MP3")
	}
	if fake.calls != 1 {
		t.Errorf("calls = %d, want 1", fake.calls)
	}
	if fake.last.style != "calm" || fake.last.voice != "Kore" || fake.last.prompt != "hello world" {
		t.Errorf("request shape wrong: %+v", fake.last)
	}

	// The bytes must be a decodable MP3 (ffprobe reports a positive duration).
	out := filepath.Join(t.TempDir(), "seg.mp3")
	if err := os.WriteFile(out, mp3, 0o644); err != nil {
		t.Fatalf("write mp3: %v", err)
	}
	if d := probeDuration(t, out); d <= 0 {
		t.Errorf("synthesized MP3 has non-positive duration %.3fs", d)
	}
}

func TestGeminiSynthesizer_OversizedInputClassification(t *testing.T) {
	// A wrapped Vertex 400 (as GenerateSpeech wraps it) must propagate and be
	// classified as oversized so the pipeline re-splits.
	fake400 := &fakeSpeechClient{err: fmt.Errorf("generating speech: %w", genai.APIError{Code: 400, Message: "invalid"})}
	g := &GeminiSynthesizer{Client: fake400, Model: "m", Voice: "Kore"}
	_, err := g.Synthesize(context.Background(), "text")
	if err == nil {
		t.Fatal("expected error for 400, got nil")
	}
	if !IsOversizedInput(err) {
		t.Errorf("IsOversizedInput(400) = false, want true")
	}
	if fake400.calls != 1 {
		t.Errorf("calls = %d, want 1", fake400.calls)
	}

	// A 503 is transient, NOT an oversized rejection — must not trigger re-split.
	fake503 := &fakeSpeechClient{err: fmt.Errorf("generating speech: %w", genai.APIError{Code: 503, Message: "unavailable"})}
	g503 := &GeminiSynthesizer{Client: fake503, Model: "m", Voice: "Kore"}
	_, err = g503.Synthesize(context.Background(), "text")
	if err == nil {
		t.Fatal("expected error for 503, got nil")
	}
	if IsOversizedInput(err) {
		t.Errorf("IsOversizedInput(503) = true, want false")
	}
	if fake503.calls != 1 {
		t.Errorf("calls = %d, want 1", fake503.calls)
	}
}

func TestGeminiSynthesizer_EmptyTextGuard(t *testing.T) {
	fake := &fakeSpeechClient{pcm: sinePCM(0.1)}
	g := &GeminiSynthesizer{Client: fake, Model: "m", Voice: "Kore"}
	if _, err := g.Synthesize(context.Background(), ""); err == nil {
		t.Fatal("expected error for empty text")
	}
	if fake.calls != 0 {
		t.Errorf("calls = %d, want 0 (no API call for empty input)", fake.calls)
	}
}

func TestGeminiSynthesizer_EmptyAudioContent(t *testing.T) {
	fake := &fakeSpeechClient{pcm: nil}
	g := &GeminiSynthesizer{Client: fake, Model: "m", Voice: "Kore"}
	if _, err := g.Synthesize(context.Background(), "text"); err == nil {
		t.Fatal("expected error for empty audio content")
	}
}

func TestStubSynthesizer(t *testing.T) {
	var s StubSynthesizer
	got1, err := s.Synthesize(context.Background(), "seg")
	if err != nil {
		t.Fatalf("Synthesize: %v", err)
	}
	if len(got1) == 0 {
		t.Fatal("stub returned empty bytes")
	}
	got2, _ := s.Synthesize(context.Background(), "seg")
	if string(got1) != string(got2) {
		t.Errorf("stub not deterministic: %q != %q", got1, got2)
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := s.Synthesize(ctx, "seg"); err == nil {
		t.Error("cancelled ctx should error")
	}
}
