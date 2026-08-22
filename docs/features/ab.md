# Audiobook Features

Status-free archive of completed audiobook-related work. This area covers the
`jeep-audiobook` command and the infrastructure required to convert PDF/ePub
documents into narrated audiobooks via Gemini TTS.

---

## Token Usage Tracking + Structured Logging

**Built:** 2026-08-22 (S1-ab-5)

### What Was Built

Ported two stdlib-only infrastructure packages from the audi codebase: `internal/log` (verbatim) and `internal/usage` (simplified to track tokens only). Both packages are nil-safe, concurrency-safe, and designed for use by the audiobook pipeline and all other jeep tools.

### The Log Package

`internal/log` is a verbatim port from audi with a single cosmetic change (dropped the audi-specific task reference from the package doc comment). It wraps `log/slog` with level-based filtering and provides a clean `New(level string, w io.Writer) *slog.Logger` constructor. The package is pure stdlib (`io`, `log/slog`, `os`, `strings`) with no external dependencies.

### The Usage Package

`internal/usage` tracks Gemini API token consumption across a run. Unlike the audi original, it uses a **single currency model** — tokens only — eliminating the old pricing estimates, TTS character counts, and cost calculations that were flagged as stale and unreliable in the source code.

**Core API:**
- `Tracker` — holds per-model token counters, mutex-guarded for concurrent writes
- `Record(model string, prompt, output, total int64)` — accumulates one API call's token usage (renamed from audi's `RecordLLM` since there's only one currency now)
- `Report()` — snapshots aggregate totals: `PromptTokens`, `OutputTokens`, `TotalTokens`, `Calls`
- `Log(logger *slog.Logger)` — emits a structured log line with the current report

All methods are **nil-safe**: a nil `*Tracker` no-ops on `Record`/`Log` and returns a zero `Report`. This allows callers to pass a possibly-nil tracker (recording disabled) without branching.

### Why This Approach

**Stdlib-only:** Both packages have zero external dependencies. This keeps them off the `CRITICAL.md` escalation list and makes them trivially unit-testable without GCP credentials or network access. Token metadata originates from `genai.GenerateContentResponse.UsageMetadata`, but `internal/usage` does not import the genai SDK — callers read the metadata and pass the values to `Record`.

**One currency:** The decision to consolidate TTS onto Gemini's `GenerateSpeech` API (DECISIONS.md 2026-08-22) means everything flows through genai, which returns token counts for both text generation and TTS. The old Cloud TTS character-based tracking and stale dollar estimates are no longer relevant. Dropping them simplifies the API and removes misleading cost figures.

**Concurrency-safe by design:** The audiobook pipeline (S1-ab-8) writes usage from a worker pool. The mutex-guarded `Tracker` and nil-safe contract eliminate the need for callers to manage locks or nil checks.

### Verification

Unit tests demonstrate that `Record` accumulates tokens correctly across multiple calls (same model and across different models), that the nil-receiver no-ops return a zero `Report` without panicking, and that concurrent writes from multiple goroutines complete without data races (`go test -race` passes). The log package tests verify level-based filtering (a Warn-level logger suppresses Info messages) and case-insensitive level parsing.

### Files Touched

- `internal/usage/usage.go` — `Tracker`, `Record`, `Report`, `Log`
- `internal/usage/usage_test.go` — table-driven accumulation tests, nil-safety, concurrency
- `internal/log/log.go` — `New`, `parseLevel`, level filtering
- `internal/log/log_test.go` — level gating tests

### Gotchas

**Do NOT import genai into `internal/usage`:** The package is intentionally stdlib-only. Callers convert `UsageMetadata` fields (int32 → int64) at the call site. Adding the genai dependency would drag this leaf package onto the `internal/gemini` blast radius.

**Nil receiver is a contract, not an optimization:** Later adapters may hold a nil `*Tracker` to disable recording. All methods must handle nil gracefully without panicking.

**Lock on reads, not just writes:** Both `Report` and `Log` acquire the mutex. Reading the per-model map while a concurrent goroutine is writing races without the lock. The `-race` detector will catch a missing lock.

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

---

## PDF Source Adapter (pure-Go stack)

**Built:** 2026-08-22 (S1-ab-3)

### What Was Built

`internal/pdfsource` implements the `ingest.Source` seam for PDF files on a
CGO-free stack, producing an `*ingest.Document` that `ingest.Structure` consumes
unchanged. Three pure-Go primitives are combined:

- **Outline → section tree:** `pdfcpu api.Bookmarks()` yields a `[]pdfcpu.Bookmark`
  tree (`Title`, `PageFrom`, `Kids`), converted to `ingest.Section` with 1-based
  `Level` = nesting depth.
- **Text → prose blocks:** `ledongthuc/pdf` `Page.GetPlainText(nil)` per page,
  stored RAW (no de-glue/ligature repair — that belongs to the S1-ab-6 LLM stage).
- **Images → figure blocks:** `pdfcpu api.ExtractImages()`, page-associated via
  `img.PageNr`; bytes read inside the digest callback (the reader is invalid after
  it returns), MIME mapped from `img.FileType`.

The heavy PDF dependencies live ONLY in this adapter; `internal/ingest` stays
dependency-free.

### Page-span assignment (the novel part)

pdfcpu gives one start page per bookmark, but `ingest.Section` needs each node's
OWN intro prose plus `Children`. Spans are reconstructed via a pre-order DFS: a
node's own span is `[startPage, nextStart-1]`, where `nextStart` is the start page
of the NEXT node in DFS order (first child, else next sibling, else nearest
ancestor's next sibling); the last node runs to the total page count. Because DFS
pre-order visits a node then its children, the gap between a node and its first
child is exactly that node's intro pages. Two bookmarks sharing a start page yield
an empty span `[p, p-1]` for the earlier node, so prose is never double-counted.
Each span's pages are emitted in order as a prose block (when non-empty) followed
by that page's figure blocks, interleaving prose + figures by page.

### Design decisions

- **Front matter before the first bookmark is DROPPED** (`[1, firstStart-1]`):
  almost always cover/copyright/ToC boilerplate that `ingest.Structure` would drop
  anyway; acceptance does not require it. Simpler than a synthetic root.
- **Empty-outline fallback:** a PDF with no bookmarks yields a single synthetic
  top-level section (`Level 1`, empty title) holding every page's prose + figures,
  mirroring audi's whole-document fallback.
- **Page count comes from pdfcpu, not ledongthuc.** pdfcpu's parser is more lenient
  than ledongthuc's header check, so it is authoritative for span math while text
  extraction is best-effort.

### Robustness

- **pdfcpu config dir disabled:** `model.ConfigPath = "disable"` in `init()` avoids
  config-dir side effects / noise in sandboxed environments.
- **ledongthuc is best-effort:** if `ledpdf.Open` fails (it rejects some headers
  pdfcpu accepts, e.g. `%PDF-1.3 ` with a trailing space in the `image.pdf`
  fixture) the whole book still ingests with empty prose — outline + images survive.
- **Per-page panic/error guard:** `GetPlainText` can panic on malformed content; a
  `recover()` converts a bad page into empty prose (logged) instead of aborting.
- **Clean stdout:** all diagnostics route through `slog` (stderr); nothing is
  printed to stdout, per the agent-consumption constraint.

### Verification

- Fast unit tests over copied `toc.pdf`/`image.pdf`/`hello.pdf` fixtures: sections
  + titles from `toc.pdf`; a `BlockFigure` with `image/png` MIME + `Page>0` from
  `image.pdf`; fallback prose with a known word ("Hello, audiobook world") from
  `hello.pdf`.
- Gated T2 acceptance (`TestLoad_ObservabilityBook_MatchesSpike`, `t.Skip` when the
  external 633-page PDF is absent): recursive section count == 559, max `Level` ==
  4, ≥1 figure with `Page>0`, aggregate prose ~1.38M chars (>100k), and the sampled
  "What Is Observability" chapter has non-empty prose.
- `CGO_ENABLED=0 go build ./...` exits 0 (pure-Go requirement confirmed).

### Files Created

- `internal/pdfsource/pdfsource.go` — `Source`/`Load`, three extraction primitives,
  DFS span-assignment tree builder, empty-outline fallback.
- `internal/pdfsource/pdfsource_test.go` — fixture unit tests + gated big-book test.
- `internal/pdfsource/testdata/{toc,image,hello}.pdf` — committed small fixtures.
- `go.mod`/`go.sum` — added `pdfcpu v0.15.0` + `ledongthuc/pdf`.

### Gotchas

- **Do NOT add a deterministic de-glue/ligature pass.** ledongthuc glues
  line-wrapped words and drops some heading ligatures; a geometry-based repair was
  tried and failed (DECISIONS.md). Repair is the S1-ab-6 LLM prepare stage's job.
- **`model.Image` embeds `io.Reader`** — read bytes with `io.ReadAll(img)` inside
  the digest callback. `Width/Height` can report 0×0 for some PNG codec paths; the
  emitted bytes are still valid, so dimensions are ignored here.
- **Package name is `pdfsource`, not `pdf`** — `pdf` collides with the ledongthuc
  import. It lives outside `internal/ingest` to keep that package dependency-free.

---

## ePub Source Adapter (pure-Go stack)

**Built:** 2026-08-22 (S1-ab-4)

### What Was Built

`internal/epubsource` implements the `ingest.Source` seam for ePub files on a CGO-free stack, producing an `*ingest.Document` that `ingest.Structure` consumes unchanged. The adapter combines pure-Go primitives to extract structure and content:

- **Container + OPF parsing:** `archive/zip` to open the `.epub`, `encoding/xml` to parse the OPF (metadata, manifest, spine)
- **TOC → section tree:** EPUB3 nav document (XHTML with `<nav epub:type="toc">`) preferred; EPUB2 NCX fallback (`application/x-dtbncx+xml`); per-spine-doc fallback when both absent
- **XHTML content → blocks:** `golang.org/x/net/html` to parse each spine document, extracting prose and embedded images as `ingest.Block`s in reading order

All ePub-specific dependencies live ONLY in this adapter; `internal/ingest` stays dependency-free.

### Spine-Index-Span Mapping (Reuses PDF Algorithm)

The TOC→section tree-building mirrors `pdfsource`'s page-span DFS algorithm, substituting **0-based spine index** for page number. Each TOC node's href resolves to a spine document index. The node's content span is `[start, nextStart-1]` where `nextStart` is the next node's start in pre-order DFS (first child, else next sibling, else nearest ancestor's next sibling). The last node runs to `len(spine)-1`. Two TOC entries pointing at the same file yield an empty span for the earlier node, so content is never double-counted.

Per spine document, XHTML is parsed to extract visible text (collapsed whitespace from HTML layout) and `<img>` elements. Images are resolved relative to the XHTML's directory, read from the zip, and emitted as `BlockFigure` with MIME type from the OPF manifest. Prose and figures interleave in reading order.

### Design Decisions

- **EPUB3 nav → NCX → per-spine fallback:** The nav document (identified by manifest `properties~="nav"`) is parsed first for its `<ol>/<li>` tree. If absent or unparseable, the NCX (referenced by `spine toc=` or manifest media-type) is tried. If neither works, the adapter emits one top-level section per spine document with title from XHTML `<title>`, first `<h1>`/`<h2>`, or `"Section N"`. This differs from PDF's single-section fallback — the spine provides natural chapter boundaries, making it a more useful fallback.
  
- **Fragment-level splitting deferred:** TOC hrefs like `ch1.xhtml#part2` have the fragment stripped before mapping to a spine document. The entire document's content is assigned to the section. Fragment-level splitting (using `#id` anchors to split a single spine doc into subsections) is out of scope for v1.

- **golang.org/x/net/html for XHTML parsing:** `encoding/xml` is fragile with HTML entities (`&nbsp;`) and non-strict markup. `x/net/html` is the robust, pure-Go, CGO-free choice. It was already present as an indirect dependency (`v0.57.0`), so using it promoted it to a direct require via `go mod tidy` — no new download.

- **Front matter before first TOC entry dropped:** Spine indices before the first TOC node's start are discarded (mirrors PDF behavior). These are typically cover/copyright/ToC pages that `ingest.Structure` would filter anyway.

### Verification

- Five test cases cover the adapter: EPUB3 nav tree building, embedded image extraction (with reading-order interleave), NCX fallback, no-TOC per-spine fallback, and href-with-fragment-and-subdir resolution.
- The nav test exercises `ingest.Structure(doc)` to prove the produced tree flows through the downstream chapterizer unchanged, yielding chapters with non-empty prose blocks.
- `CGO_ENABLED=0 go build ./...` confirms pure-Go requirement.

### Files Created

- `internal/epubsource/epubsource.go` — `Source`/`Load`, container/OPF/nav/NCX parsers, DFS spine-span tree builder, per-document XHTML extractor
- `internal/epubsource/epubsource_test.go` — programmatically-built fixtures (no committed `.epub` binaries), five test cases
- `go.mod`/`go.sum` — promoted `golang.org/x/net` to direct dependency

### Gotchas

- **Relative href bases differ per context:** OPF hrefs are relative to the OPF's directory; nav/NCX hrefs are relative to the nav/NCX file's directory; `<img src>` is relative to the containing XHTML's directory. Do NOT use one base for all three — always `path.Clean(path.Join(dir, url.PathUnescape(href)))`.
- **Strip fragments before resolving:** `strings.SplitN(href, "#", 2)[0]` before mapping to a spine index. Otherwise `ch1.xhtml#part2` won't match the spine entry `ch1.xhtml`.
- **zip paths use forward slashes:** `zip.File.Name` is already slash-separated. Use the `path` package (not `filepath`) to avoid platform-specific separators.
- **encoding/xml namespace matching:** A struct tag without a namespace (e.g., `xml:"item"`) matches that local name in ANY namespace. For Dublin Core metadata use `xml:"metadata>title"` and `xml:"metadata>creator"`.
- **epub:type attribute in x/net/html:** Appears as `Attr.Key == "epub:type"` (the HTML parser doesn't split the namespace prefix). Match on the raw key containing `toc` as a substring.
- **Best-effort degradation:** Missing images, unresolvable TOC hrefs, or absent nav/NCX must log a warning and continue, never error out. Hard errors are limited to: cannot open zip, no container.xml, no OPF, or empty spine.

---

## Figure Description + Listening Adaptation (describe package)

**Built:** 2026-08-22 (S1-ab-6)

### What Was Built

Ported audi's `internal/describe` package (figure description, inline weaving, and listening adaptation) into jeep, routing every LLM call through `gemini.Generate` with structured-JSON response schemas. This replaces audi's hand-rolled genai plumbing (`callWithRetry`, `isRetryableGenAI`, `NewGemini*` factories) with jeep's reusable client infrastructure.

The package provides three capabilities:
1. **Figure description:** `Describer` seam converts image bytes + surrounding context into spoken descriptions via LLM
2. **Listening adaptation:** `Preparer` seam rewrites extracted text to flow naturally for audio narration, with critical **artifact-repair guidance** for fixing PDF extraction issues
3. **Inline weaving:** `Weaver` orchestrates concurrent figure description across a page's images and splices results into the prose at correct reference points

### The Generator Seam

`internal/describe` cannot import `internal/gemini` and inject a fake into a real `*gemini.Client` because `gemini.Client.models` (the `contentGenerator` field) is unexported. The solution is a describe-local `generator` interface matching `gemini.Client.Generate`'s exact signature:

```go
type generator interface {
    Generate(ctx context.Context, model, system string, contents []*genai.Content,
        search bool, schema *genai.Schema, opts *gemini.GenerateOptions) (*gemini.GenerateResult, error)
}
```

`*gemini.Client` satisfies this structurally. Tests supply a `fakeGenerator` that returns scripted `GenerateResult`s with JSON-formatted `Text` fields, enabling full unit test coverage without network calls.

### Structured JSON Wrapping

Both `Describer` and `Preparer` pass a `genai.Schema` to `gemini.Generate`:
- **Describer:** `{Type: TypeObject, Properties: {"description": {Type: TypeString}}, Required: ["description"]}`
- **Preparer:** `{Type: TypeObject, Properties: {"narration": {Type: TypeString}}, Required: ["narration"]}`

`gemini.Generate` sets `ResponseMIMEType="application/json"` + `ResponseSchema` when `schema != nil`. The returned `GenerateResult.Text` is the JSON string, which must be `json.Unmarshal`ed into a struct to extract the actual text. Do NOT return `res.Text` raw — it would be a JSON blob, not narration.

### Artifact-Repair Prompt (Load-Bearing)

The `defaultPrepareSystemPrompt` was extended with explicit instructions to repair PDF extraction artifacts:
- Re-join words split across line wraps (hyphenation/mid-word breaks)
- Restore dropped leading letters and ligatures at word starts (e.g., `unied`→`unified`, `ree pillars`→`three pillars`, dropped `The`/`Th`/`fi` restored)
- Do this WITHOUT changing meaning, summarizing, or inventing content

This guidance is the validated solution from the phase-0 spike for fixing ledongthuc text quality issues. A unit test asserts the exported constant contains "ligature" or "line" to prevent accidental deletion.

### Key Design Decisions

**Local interface is mandatory:** The `generator` seam is not an optimization — it's the only way to fake `gemini.Client` from an external package. This pattern should be reused in other packages that need to test against Gemini (e.g., pipeline, map-reduce).

**No ThinkingConfig in jeep:** audi disabled thinking (`ThinkingBudget:0`) so reasoning tokens wouldn't eat `MaxOutputTokens` and truncate the rewrite. jeep's `GenerateOptions` has no such field, and adding one would modify CRITICAL `internal/gemini`. Instead, prepare's `MaxTokens` is left unset (0) so no output cap is imposed and the model's default budget applies. The existing ~6000-char chunking keeps each rewrite small. If real runs later show truncation, a follow-up task should PROPOSE adding a thinking-budget knob — do not sneak it in.

**Changed retry semantics:** jeep retries only 429/500/503 via `isRetryable` in `gemini.go`; audi also retried 502/504 and `context.DeadlineExceeded`. This is the accepted jeep convention. Retries are driven via `GenerateOptions.Retries` (default 4). Tests verify a 400 (`genai.APIError{Code:400}`) is NOT retried (call count == 1).

**No factory functions:** Unlike audi's `NewGeminiDescriber(cfg, gcp.Clients, ...)`, jeep has no constructors. CLI wiring (S1-ab-9) constructs adapters directly with a `*gemini.Client` and sets fields. This eliminates the `config`/`gcp` dependency cycle and simplifies the package.

**Weave + chunk logic ported byte-for-byte:** The pure-Go orchestrator (`Weaver`, `weaveItem`, `chunkForPrepare`, `splitLong`) is unchanged from audi except for import path rewrites. It is already correct, unit-tested for concurrency and reading order, and has no genai dependencies. Re-deriving risks regression.

### Token Usage Recording

Both `GeminiDescriber` and `GeminiPreparer` hold an optional `Usage *usage.Tracker` field. After each `Generate` call, they record:

```go
g.Usage.Record(g.Model, int64(res.PromptTokens), int64(res.OutputTokens), int64(res.PromptTokens+res.OutputTokens))
```

`GenerateResult` has NO `TotalTokens` field — only `PromptTokens` and `OutputTokens` (int32). Total is computed as prompt+output. `usage.Record` is nil-safe, so an unset `Usage` field is fine.

### Verification

- Describe unit test feeds `fakeGenerator` returning `{"description":"a chart"}`, asserts parsed string
- Prepare unit test feeds `{"narration":"adapted text"}`, asserts parsed/rejoined string across chunks
- Retry test scripts one 503 then success, asserts `fake.calls == 2`
- Fatal test scripts `genai.APIError{Code:400}`, asserts `fake.calls == 1` (no retry)
- Artifact-repair test asserts the real exported `defaultPrepareSystemPrompt` constant contains "ligature" or "line"
- Full suite: `go build ./... && go vet ./... && go test ./...` all green
- Pure-Go: `CGO_ENABLED=0 go build ./...` exits 0
- `go mod tidy` promotes only `golang.org/x/sync` to direct require (already in go.sum as indirect)
- `git diff --stat` shows no files under `internal/gemini/` (kept off CRITICAL)

### Files Created

- `internal/describe/images.go` — `Describer` interface, `GeminiDescriber` + `StubDescriber` adapters, `Weaver` orchestrator
- `internal/describe/prepare.go` — `Preparer` interface, `GeminiPreparer` + `StubPreparer` adapters, chunking logic
- `internal/describe/images_test.go` — Describe/Weave tests, retry/fatal, usage recording
- `internal/describe/prepare_test.go` — Prepare tests, concurrency, artifact-repair assertion
- `go.mod`/`go.sum` — promoted `golang.org/x/sync/errgroup` from indirect to direct

### Gotchas

**JSON schema required in tests:** Test fakes MUST return valid JSON matching the schema (e.g., `{"description":"text"}`, NOT just `"text"`), or unmarshal will fail. This is different from audi's `fakeModels` which returned raw `genai.GenerateContentResponse` with text parts.

**Retries tested indirectly:** The describe package does NOT test retry counts (that's `gemini`'s responsibility). Tests verify a 400 error propagates without retry by checking `fake.calls == 1`. The 503 retry path is tested by scripting one 503 then success and asserting `calls == 2`.

**Artifact-repair guidance is in the prompt:** It's a string constant, not a runtime flag. Tests assert its presence. Do not dilute or remove this guidance — the phase-0 spike validated it as the solution to PDF text quality issues.

**ImageRef stays primitives-only:** The `ImageRef` struct (used by Weaver) imports no other jeep packages. The S1-ab-8 pipeline maps `ingest.Block(Figure)` → `ImageRef` at the call site; describe must not depend on `ingest`.
