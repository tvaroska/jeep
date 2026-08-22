# Audiobook Features

Status-free archive of completed audiobook-related work. This area covers the
`jeep-audiobook` command and the infrastructure required to convert PDF/ePub
documents into narrated audiobooks via Gemini TTS.

---

## Reusable Gemini Client + TTS Enhancements

**Built:** 2026-08-22 (S1-ab-1)

### What Was Built

Introduced a long-lived `gemini.Client` struct to `internal/gemini` that holds a
constructed genai client (via an injectable `contentGenerator` seam) plus project
and region configuration. The client exposes `Generate`, `GenerateImage`, and
`GenerateSpeech` as methods, enabling callers to reuse a single client across
thousands of requests — critical for the audiobook pipeline's performance.

The existing package-level functions (`gemini.Generate`, `gemini.GenerateImage`,
`gemini.GenerateSpeech`) were preserved as thin wrappers that construct a client
and delegate to its methods. This keeps all existing command-line tools (`jeep`,
`jeep-image`, `jeep-research`, `jeep-tts`) unchanged at their call sites, except
for `jeep-tts` which gained a `style` parameter (passed as `""`).

### TTS Upgrades

`GenerateSpeech` received three enhancements specifically for the audiobook pipeline:

1. **Style-prompt prefix parameter:** Gemini TTS has no separate style field in its
   API, so non-empty style text is prepended to the prompt (`style + "\n\n" + prompt`).
   Empty style leaves the prompt unchanged. This allows audiobook narration to carry
   voice-direction instructions (e.g., "Read slowly and warmly") without polluting
   the text content.

2. **Invalid-input classifier:** Exported `IsInvalidInput(err)` to detect Vertex
   `APIError{Code:400}` (oversized or malformed input). The function unwraps errors
   via `errors.As`, so it works through `fmt.Errorf("%w", ...)` chains. The audiobook
   pipeline uses this signal to trigger adaptive re-splitting when a segment exceeds
   Gemini's input limits. Note that 400 is NOT in the retry classifier — it propagates
   on the first attempt without retry, unlike 429/500/503.

3. **PCM to MP3 encoding:** Added `PCMToMP3(ctx, pcm, sampleRate, channels)` to convert
   the raw `audio/L16` PCM that Gemini TTS returns into MP3 format via ffmpeg
   (`libmp3lame -q:a 4`). The function pipes PCM bytes through stdin and captures MP3
   bytes from stdout. This eliminates the need for intermediate WAV files in the
   audiobook pipeline.

### Injectable Seam

The new `contentGenerator` interface (matching `Models.GenerateContent`'s signature)
makes the Gemini API boundary fully testable. The client stores `genai.Client.Models`
as a `contentGenerator`, allowing tests to inject a fake that records requests and
returns scripted responses. This seam enables unit testing of retry logic,
400-classification, and request construction (like style-prefix injection) without
making network calls.

### Why This Approach

**Client reuse:** The audiobook pipeline generates thousands of TTS segments per book.
Creating a fresh genai client for each call (the old behavior) incurs repeated
authentication and setup overhead. A single long-lived client amortizes this cost.

**Backward compatibility:** By preserving the package-level functions as wrappers,
no existing tool code needed changes (except the one-line `jeep-tts` update to pass
the new style parameter). This reduces implementation risk in a sensitive package
that all tools depend on.

**Style via prompt prepending:** Gemini TTS lacks a dedicated style parameter. The
alternative — embedding style instructions within the narration text itself — would
require post-hoc editing of prepared content and risk leaking the instructions into
the audio. Prepending the style as a separate block keeps it cleanly separated from
the content and allows the same prepared text to be narrated with different styles.

**400 vs retry logic:** The existing `isRetryable` function treats 429/500/503 as
transient failures worth retrying. A 400 signals a request problem (too large,
malformed) that won't resolve with retries. By exposing `IsInvalidInput` as a
separate classifier, the pipeline can distinguish "retry this" from "adapt and re-split".

**ffmpeg for encoding:** Gemini TTS returns raw PCM; audiobooks need compressed audio.
ffmpeg is already a runtime dependency for concatenation and loudness normalization
(inherited from the audi codebase being ported). Reusing it for MP3 encoding avoids
introducing another dependency. Tests skip gracefully when ffmpeg is absent, keeping
the suite green in minimal environments.

### Files Touched

- `internal/gemini/gemini.go` — added `Client` struct, `NewClient` constructor,
  converted generation functions to methods, added `IsInvalidInput` and `PCMToMP3`
- `cmd/jeep-tts/main.go` — updated `GenerateSpeech` call to pass empty style parameter
- `internal/gemini/client_test.go` (new) — tests for style prefix, 400 classification,
  retry behavior, all against a fake `contentGenerator`
- `internal/gemini/mp3_test.go` (new) — PCM to MP3 encoding validation using ffprobe

### Gotchas

**Interface receiver subtlety:** `Models.GenerateContent` has a value receiver, but
`genai.Client.Models` is of type `*Models`. Both satisfy the `contentGenerator`
interface. Store `client.Models` (the pointer) directly; dereferencing causes a
type mismatch.

**Retry test timing:** Tests that verify 503 retry behavior wait for real exponential
backoff delays (`time.Second << attempt`). Even with retry counts limited to 1 or 2,
these tests add ~1.3 seconds of wall-clock time to the suite. This is acceptable for
a sensitive package but means the test should not be run in tight loops or with high
retry counts.

**ffmpeg test portability:** The MP3 encoding test skips (`t.Skip`) when ffmpeg or
ffprobe are not on PATH. This keeps the suite green in CI or minimal Docker containers
that lack multimedia tools. In the production environment, both tools are present at
`/usr/bin/ffmpeg` (version 6.1.1).

**jeep-tts still writes WAV:** The tool's existing `pcmToWAV` function and its test
(`pcm_test.go`) remain unchanged. Only the audiobook pipeline will consume MP3-encoded
output; jeep-tts continues to produce WAV files for single-prompt use cases.
