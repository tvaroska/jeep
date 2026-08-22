package audio

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/tvaroska/jeep/internal/gemini"
)

// Defaults for the Gemini synthesis adapter, resolved lazily inside Synthesize.
const (
	defaultTTSSampleRate = 24000 // genai TTS returns audio/L16 @ 24kHz mono
	defaultTTSChannels   = 1
	defaultTTSRetries    = 4
)

// speechClient is the minimal subset of *gemini.Client this adapter needs.
// *gemini.Client satisfies it structurally; tests inject a fake. This mirrors
// the local-interface-at-the-gemini-boundary pattern used by describe/mapreduce
// (gemini.Client.models is unexported, so an external package cannot fake a real
// *gemini.Client — depend on a narrow interface instead).
type speechClient interface {
	GenerateSpeech(ctx context.Context, model, voice, style, prompt string, retries int) (*gemini.SpeechResult, error)
}

// Synthesizer renders one TTS-ready text segment to encoded audio bytes (MP3).
// Implementations must be safe to call concurrently.
type Synthesizer interface {
	Synthesize(ctx context.Context, text string) ([]byte, error)
}

// GeminiSynthesizer is the GCP adapter: it calls Vertex AI Gemini TTS via the
// shared gemini.Client to render one segment, then encodes the raw PCM the model
// returns to MP3. genai TTS has no dedicated style field, so a non-empty Style is
// carried by GenerateSpeech as the leading text of the prompt.
//
// Each Synthesize call reads only the receiver's fields and builds a fresh
// request, so a single *GeminiSynthesizer is safe to call from many goroutines
// (the pipeline's per-chapter worker pool relies on this).
type GeminiSynthesizer struct {
	Client     speechClient // Gemini client (or a fake in tests)
	Model      string       // e.g. "gemini-2.5-flash-preview-tts" (config resolves in S1-ab-9)
	Voice      string       // prebuilt voice name, e.g. "Kore"
	Style      string       // style-prompt prefix ("" => none)
	SampleRate int          // <=0 => defaultTTSSampleRate (24000)
	Channels   int          // <=0 => defaultTTSChannels (1)
	Retries    int          // <=0 => defaultTTSRetries (4)
	Log        *slog.Logger // optional; nil-safe (uses slog.Default() when nil)
}

// logger returns g.Log if non-nil, else slog.Default().
func (g *GeminiSynthesizer) logger() *slog.Logger {
	if g.Log != nil {
		return g.Log
	}
	return slog.Default()
}

func (g *GeminiSynthesizer) sampleRate() int {
	if g.SampleRate > 0 {
		return g.SampleRate
	}
	return defaultTTSSampleRate
}

func (g *GeminiSynthesizer) channels() int {
	if g.Channels > 0 {
		return g.Channels
	}
	return defaultTTSChannels
}

func (g *GeminiSynthesizer) retries() int {
	if g.Retries > 0 {
		return g.Retries
	}
	return defaultTTSRetries
}

// Synthesize renders one already-segmented text to MP3 bytes. It is a blocking
// call. Empty input fails fatally without an API call. Unlike audi's Cloud-TTS
// adapter there is NO local byte-limit guard: the segmenter already bounds
// segments, and the only oversize path we want is the Vertex 400, which the
// pipeline re-splits on (see IsOversizedInput). A wrapped Vertex 400 stays
// classifiable through the returned error.
func (g *GeminiSynthesizer) Synthesize(ctx context.Context, text string) ([]byte, error) {
	if text == "" {
		return nil, fmt.Errorf("synthesize: empty segment")
	}

	res, err := g.Client.GenerateSpeech(ctx, g.Model, g.Voice, g.Style, text, g.retries())
	if err != nil {
		return nil, err // a wrapped Vertex 400 stays classifiable via IsOversizedInput
	}
	if res == nil || len(res.PCMData) == 0 {
		return nil, fmt.Errorf("synthesize: empty audio content")
	}

	return gemini.PCMToMP3(ctx, res.PCMData, g.sampleRate(), g.channels())
}

// IsOversizedInput reports whether err is the Vertex 400 the TTS model returns
// for a segment too token-dense to synthesize; the pipeline re-splits on it. It
// unwraps, so GenerateSpeech's "generating speech: %w" wrapping is still
// recognized. Exposed here so the pipeline need not import gemini for this.
func IsOversizedInput(err error) bool { return gemini.IsInvalidInput(err) }

// StubSynthesizer is the local adapter: a deterministic, offline stand-in for the
// GCP adapter, used for pipeline wiring and unit tests. Its output is NOT
// decodable audio — real audio comes from GeminiSynthesizer, and the loudness
// integration test must supply its own real-audio fake.
type StubSynthesizer struct{}

// stubAudioPrefix marks StubSynthesizer output so it is never mistaken for real
// MP3 bytes.
const stubAudioPrefix = "JEEP-STUB-MP3:"

// Synthesize returns deterministic, non-empty (non-decodable) bytes derived from
// text. It honors context cancellation.
func (StubSynthesizer) Synthesize(ctx context.Context, text string) ([]byte, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return []byte(stubAudioPrefix + text), nil
}

// compile-time checks that both adapters satisfy the seam.
var (
	_ Synthesizer = (*GeminiSynthesizer)(nil)
	_ Synthesizer = StubSynthesizer{}
)
