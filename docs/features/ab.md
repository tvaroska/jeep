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

---

## Format-Agnostic Ingestion Seam

**Built:** 2026-08-22 (S1-ab-2)

### What Was Built

Introduced the `internal/ingest` package with a tree-based document model that replaces
audi's page-centric TOC approach. The package defines the core types (`Block`, `Section`,
`Document`, `Meta`) and the `Source` interface that format adapters (PDF, ePub, markdown,
HTML) will implement to feed the audiobook pipeline.

The section-based chapterization algorithm (`Structure`) walks the document tree and groups
sections into narration chapters, applying the same 30-45 minute granularity strategy as
audi but adapted to work on a section tree instead of page ranges.

### Core Model

**`Source → Document{Sections}`:** Format adapters implement the `Source.Load(path string)
(*Document, error)` interface. A `Document` holds metadata (`Meta`) and a tree of `Section`
nodes in `Sections` (the top-level roots). Each `Section` has:
- A `Title` and hierarchy `Level` (1-based)
- Ordered content `Blocks` (prose/figure/caption/code/table in reading order)
- Ordered child `Sections` (`Children`)

The tree represents the document's logical structure without any format-specific details.
There are no page numbers, byte offsets, or file-format metadata in the model — those
concerns live in the adapters.

**`Block` types:** Content is classified as `BlockProse` (text), `BlockFigure` (raster
image with `Data`/`MimeType`), `BlockCaption`, `BlockCode`, or `BlockTable`. This lets
the pipeline make intelligent decisions about what to narrate, what to describe, and what
to skip. The optional `Page` field (1-based) is for source traceability when the adapter
can supply it; it's not used by chapterization.

### Tree-Based Chapterization

**Key semantic shift from audi:** Chapters are derived by walking the `Section` tree, not
by slicing page ranges. The `Structure(*Document) []Chapter` function:

1. **Iterates top-level sections** (`doc.Sections`) directly — no `minLevel` scanning needed.
2. **Expands sections with ≥2 children** into one chapter per child for finer granularity
   (commute-sized chunks). The parent section's own intro `Blocks` (prose that precedes
   its subsections) are absorbed by the first child via a synthetic **intro-shim** `*Section`
   prepended to the child's chapter.
3. **Keeps sections with <2 children whole** as a single chapter.
4. **Filters boilerplate** at the top level (Cover/Copyright/Table of Contents/Index/Colophon)
   using the same case-insensitive prefix-matching logic as audi. Foreword, Preface, and
   Introduction are treated as real content and are never skipped.

The intro-shim approach ensures no content is lost during expansion: a parent section's
intro prose appears exactly once, ahead of the first expanded child chapter, without
double-counting. The shim is a `&Section{Title: "", Level: parent.Level, Blocks: parent.Blocks}`
that carries only the parent's own blocks, never its children.

### Why This Approach

**Tree vs. pages:** The page-centric model couples chapterization to the PDF format
(page ranges, `StartPage`/`EndPage`, same-page deduplication). Moving to a section tree
decouples structure from pagination, making the pipeline format-agnostic. An ePub or
markdown document has no pages, but it does have sections. The tree model works uniformly
across all formats.

**Intro-shim uniformity:** The alternative to the shim would be giving `Chapter` two
different representations: a simple `[]*Section` for unexpanded chapters and a complex
`ParentIntro + []*Section` for expanded ones. The shim keeps `Chapter.Sections` always
`[]*Section`, simplifying downstream code that flattens chapters into TTS segments.

**Boilerplate filtering:** Front and back matter like copyright pages and indexes produce
useless audio. The same heuristic list from audi (`defaultSkipTitles`) is ported verbatim,
including the `honeycomb` entry (a book-specific artifact from the test corpus, harmless).

**Heading detection deferred:** The old `chaptersFromHeadings` text-based heuristic (regex
matching chapter/part/section patterns) is not ported. For headless formats like plain
markdown or HTML articles, the responsibility for detecting structure and building the
tree lies with the adapter (S1-ab-12), not with chapterization. The `Structure` function
only consumes an already-built tree.

**No external dependencies:** The package imports only stdlib (`strings` for boilerplate
matching, `reflect`/`testing` for tests). No genai, no pdfcpu, no ledongthuc. This keeps
it off the `CRITICAL.md` escalation list and makes it trivially unit-testable without
network or GCP setup.

### Files Created

- `internal/ingest/document.go` — core types, package documentation
- `internal/ingest/structure.go` — `Chapter`, `Structure`, boilerplate classifier
- `internal/ingest/structure_test.go` — table-driven tests over synthetic section trees
  (expansion, intro absorption, boilerplate filtering, edge cases)

### Gotchas

**No page-model artifacts:** Do not reintroduce `minLevel`, page clamping, same-page
deduplication, or `numPages` from audi. These are meaningless in the tree model. If you
find yourself needing them, the abstraction is wrong.

**Intro-shim carries blocks only:** The shim must include the parent's `Blocks` but never
its `Children`. Copying children would double-count them when the pipeline later flattens
`chapter.Sections`.

**`Source.Load` is context-free:** The signature is `Load(path string) (*Document, error)`
with no `context.Context` parameter. This matches audi's `Extract(path)` idiom and reflects
that ingestion is synchronous local I/O. If a reviewer later prefers context support, it's
a trivial signature bump — but the default matches the surrounding codebase conventions.

**Boilerplate at top level only:** Only `doc.Sections` (the roots) are checked against
`defaultSkipTitles`. Children of a boilerplate section are not individually filtered. This
matches audi's behavior.
