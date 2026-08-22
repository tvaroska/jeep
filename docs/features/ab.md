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

---

## Generic Map-Reduce Helper on gemini.Generate

**Built:** 2026-08-22 (S1-ab-7)

### What Was Built

Built a generic, provider-agnostic map-reduce orchestrator in `internal/mapreduce` that routes chunk→map→reduce[+refine] workflows through `gemini.Generate` with structured JSON schemas. The package provides the core concurrency primitive for the audiobook pipeline's `--mode summary|abstract` feature (S1-ab-10), which needs to process over-window inputs by chunking, parallel mapping, and reducing.

The implementation has two files:
1. **`mapreduce.go`:** The generic orchestrator (`Spec[E,P,R]`, `Run`) with no GCP or genai dependencies
2. **`gemini.go`:** The thin Gemini binding (`Step[T]`) that wraps `gemini.Generate` + structured JSON unmarshaling

### Core API

**`Spec[E any, P any, R any]`** describes one map-reduce run:
- `Elements []E` — input chunks
- `Map func(ctx, i int, e E) (P, error)` — required; transforms one chunk into a partial result
- `Reduce func(ctx, partials []P) (R, error)` — required; combines all partials into final result
- `Refine func(ctx, r R) (R, error)` — optional; post-processes the reduced result (e.g., polish/trim)
- `Concurrency int` — worker pool size (defaults to 4 when ≤0)

**`Run[E,P,R](ctx, spec) (R, error)`** orchestrates the workflow:
- Maps every element concurrently through a bounded, fail-fast worker pool (`golang.org/x/sync/errgroup`)
- Collects partials in input order (writes to `partials[i]`, never appends from goroutines)
- Reduces them into a single result
- Refines if `Refine != nil`
- First Map error cancels remaining work and is returned (fail-fast); Reduce/Refine never run on partial failure
- Empty `Elements` still calls `Reduce(ctx, nil)` so callers get a defined result (e.g., empty-summary string) instead of a zero value

**`Step[T any](ctx, generator, model, system, userText, schema, opts) (T, error)`** is the Gemini building block for Map/Reduce closures:
- Calls `gemini.Generate` with the system/user prompt and a `*genai.Schema`
- Unmarshals the returned JSON into `T`
- Retries are driven by `opts.Retries` (gemini layer); this function adds none
- Does NOT record usage (deferred to S1-ab-10 to avoid gold-plating)

### The Generator Seam

Following the S1-ab-6 `describe` precedent exactly, `gemini.go` defines a package-LOCAL `generator` interface matching `gemini.Client.Generate`'s signature. `*gemini.Client` satisfies it structurally; tests inject a `fakeGenerator`. This is why the task does NOT actually touch the CRITICAL `internal/gemini` path — the seam is the same "fake client returns canned responses" pattern documented in `design/seams.md`.

### Design Decisions

**New package `internal/mapreduce` (not modify `internal/gemini`):** The task scope explicitly forbids touching `internal/gemini` (SENSITIVE/CRITICAL). All code lives in a new, standalone package. Only the thin `gemini.go` binding imports `internal/gemini` to call its `Generate` method; the core orchestrator (`mapreduce.go`) has zero genai or GCP dependencies.

**Generics `[E, P, R any]`:** Element type, Partial type, Result type are all generic. This makes the helper reusable for any chunk→map→reduce workflow, not just text summarization. The audiobook pipeline will use `E=string` (text chunk), `P=struct{Summary string}`, `R=struct{Summary string}` for summary mode.

**Concurrency via errgroup:** `golang.org/x/sync/errgroup.WithContext` provides bounded worker pool + fail-fast semantics. `SetLimit(concurrency)` bounds parallelism; any Map error cancels the group context and propagates immediately. Retry logic is NOT duplicated here — it lives in `gemini.Generate` via `GenerateOptions.Retries`.

**Order preservation:** Map results are written to `partials[i]` by index, not appended. This guarantees the Reduce function receives partials in the same order as `Elements`, even when goroutines complete out of order.

**Empty Elements edge case:** When `Elements` is empty or nil, `Run` still calls `Reduce(ctx, nil)` so the caller gets a meaningful result (e.g., "No content to summarize") instead of a zero value. This matches the "defined result for all inputs" principle.

**No usage tracking in Step:** The function signature is deliberately minimal — it does NOT accept a `*usage.Tracker` or return `*gemini.GenerateResult`. S1-ab-10 will decide usage wiring (likely the same pattern as `describe`: the caller holds a tracker and records after each `Step` call). Adding it here would be gold-plating.

### Spec/Plan Discrepancy — jeep-research Unchanged

The Sprint 1 plan and the task scope said "refit `jeep-research`'s hand-rolled synthesis onto the helper." Investigation revealed this assumption is stale: the current `jeep-research` performs NO local synthesis — it delegates to the Vertex AI interactions deep-research agent (`internal/interactions`), which synthesizes server-side. There is no hand-rolled map-reduce to unify.

The task left `cmd/jeep-research` and `internal/interactions` completely untouched. The acceptance criterion for them ("still builds + tests green") holds trivially. The helper's real consumer is S1-ab-10 (`--mode summary|abstract`), which is where "one implementation" is realized. This discrepancy is noted in DECISIONS.md.

### Verification

- `TestRun_ChunkMapReduce_ProducesExpectedResult`: `Elements=[1,2,3]`, Map returns `e*10`, Reduce sums → asserts 60 (the core acceptance)
- `TestRun_PreservesInputOrder`: Map completion order randomized → Reduce receives partials in element order
- `TestRun_MapError_FailFast`: one Map errors → Run returns that error, Reduce never called (atomic flag assertion)
- `TestRun_EmptyElements_CallsReduceWithNil`: `Elements=nil` → Reduce receives `len(partials)==0`, returns sentinel
- `TestRun_Refine_Applied`: Refine transforms result; nil Refine leaves it unchanged
- `TestRun_ContextCancelled`: pre-cancelled ctx → returns `context.Canceled`
- `TestRun_MissingMapOrReduce_Errors`: nil Map or nil Reduce → error
- `TestStep_UnmarshalsJSON`: fakeGenerator returns `{"summary":"hello"}` → Step extracts "hello"
- `TestStep_ForwardsError`: fake errors → Step propagates
- `TestStep_BadJSON_Errors`: fake returns non-JSON → parse error
- Full suite: `go build ./... && go vet ./... && go test ./...` all green
- Race-free: `go test -race ./internal/mapreduce/` exits 0
- Pure-Go: `CGO_ENABLED=0 go build ./...` exits 0
- `go mod tidy` introduces NO new dependency (`golang.org/x/sync` and `google.golang.org/genai` already direct)

### Files Created

- `internal/mapreduce/mapreduce.go` — `Spec[E,P,R]`, `Run`, generic orchestrator
- `internal/mapreduce/gemini.go` — local `generator` interface, `Step[T]`
- `internal/mapreduce/mapreduce_test.go` — core orchestrator tests
- `internal/mapreduce/gemini_test.go` — `fakeGenerator`, Step tests, composition test

### Gotchas

**Do NOT touch `internal/gemini`:** The local `generator` interface is mandatory. If you think you must change `internal/gemini`, STOP — that auto-escalates (stronger model + review). Flag it, don't proceed.

**JSON schema required for Step:** Test fakes MUST return valid JSON matching the target struct (e.g., `{"summary":"text"}`, NOT just `"text"`), or `json.Unmarshal` fails. Same bite as S1-ab-6.

**SetLimit(n) with n<1 blocks forever:** Default concurrency to 4 when `spec.Concurrency ≤ 0`. Never pass 0 or negative to `errgroup.SetLimit`.

**Reduce/Refine run on outer ctx, not gctx:** After `g.Wait()`, use the original `ctx` for Reduce/Refine calls, not the errgroup's cancelled `gctx`.

**Order preservation:** Write map results to `partials[i]`, never append from goroutines. The slice is pre-allocated with `len(spec.Elements)`; each goroutine owns its index.

**No usage tracking yet:** Defer to S1-ab-10 to avoid gold-plating. If Step later needs to expose tokens, return `*gemini.GenerateResult` alongside `T` or accept a `*usage.Tracker` param.

---

## Pipeline Orchestrator (section-driven, resumable, loudness-normalized)

**Built:** 2026-08-22 (S1-ab-8)

### What Was Built

Ported audi's `internal/pipeline` (plus the TTS-adjacent segmentation/concat helpers from audi `internal/tts`) into jeep as two packages — `internal/audio` and `internal/pipeline` — rewired off the format-agnostic `ingest` Section tree instead of PDF pages, and synthesizing through the shared `gemini.Client` (PCM → MP3) instead of Cloud TTS gRPC. The orchestrator drives, per chapter: Weave (image descriptions) → Prepare (listening adaptation) → Segment → Synthesize (bounded worker pool) → Concat (per-segment EBU R128 loudness normalize) → Probe → manifest entry. It keeps the resumable 3-level content-addressed cache, adaptive re-split on Vertex HTTP 400, and a per-book `manifest.json`.

### The `internal/audio` Package

Three pieces, all pure/local with a Gemini seam:
- **`segment.go`** — verbatim port of audi's sentence/word-aware `Segmenter` (`MaxBytes`, `DefaultMaxSegmentBytes=3000`, greedy pack + hard-rune-split fallback). Package renamed `tts`→`audio`.
- **`concat.go`** — verbatim port of `FFmpegConcatenator`. `Normalize:true` decodes each segment as a separate input, applies `loudnorm=I=-16:TP=-1.5:LRA=11,aformat=sample_rates=44100:channel_layouts=mono`, and joins via the concat filter in one encode. With `Normalize:true` even a single segment goes through ffmpeg so lone-segment chapters hit the same loudness target as multi-segment ones.
- **`synthesize.go`** — NEW. The `Synthesizer` seam plus `GeminiSynthesizer` (GCP adapter) and `StubSynthesizer` (offline). `GeminiSynthesizer.Synthesize` calls `speechClient.GenerateSpeech` (a package-local narrow interface `*gemini.Client` satisfies structurally — the same seam idiom as describe/mapreduce, since `gemini.Client.models` is unexported) and encodes the returned PCM with `gemini.PCMToMP3`. `IsOversizedInput(err)` re-exports `gemini.IsInvalidInput` so the pipeline needn't import gemini for the 400 classification.

### The `internal/pipeline` Package

- **`cache.go`** — near-verbatim `Cache` seam + `DiskCache` local adapter (atomic temp+fsync+rename), `nopCache` nil-safe fallback, length-prefixed SHA-256 `hashKey`, and the three key builders: `narrationCacheKey` (`narration-v2`, keyed on Gemini model + every PageContent's text/image bytes), `segmentCacheKey` (`segment-v1`, keyed on segment text + TTS fingerprint), `chapterCacheKey` (`chapter-v1`).
- **`manifest.go`** — near-verbatim `Manifest`/`ChapterEntry`, `WriteManifest`, and the `Prober` seam + `FFprobeProber` local adapter.
- **`run.go`** — rewritten `Runner`/`Options`/`Run` for the ingest model.

### Key Rewrites From the Page Model

**Drives off `ingest.Source` + `ingest.Structure`, not PDF pages.** `Run` does `doc := r.Source.Load(opts.InputPath)`, `chapters := ingest.Structure(doc)`, then `selectChapters`. `Options` dropped audi's `FromPage`/`ToPage`; chapter selection is now 1-based indices into `Structure`'s output (`Options.Chapters`).

**Section-tree → PageContent flattening.** `chapterToPageContents` walks a chapter's Section subtree pre-order (own Blocks then Children) emitting ONE `describe.PageContent` per Block: a `BlockFigure` (with data) becomes a PageContent carrying one `ImageRef` so Weave inserts its spoken description at exactly that position; a prose Block with non-blank text becomes a text PageContent. This preserves reading order and lets Weave splice image descriptions inline.

**Page span best-effort.** `chapterPageSpan` derives `[StartPage,EndPage]` from the min-non-zero/max `Block.Page`; page-less formats (ePub/markdown/HTML) carry `Page==0` so both come back 0.

### 3-Level Resumable Cache (unchanged semantics)

1. **narration** — guards both Gemini stages (Weave+Prepare) AND stabilizes the adapted narration across process restarts, which is the prerequisite for the segment cache to hit on a fresh run (Gemini output is non-deterministic).
2. **segments** — guards the (many, expensive) TTS calls; keyed per segment so concurrent writes go to distinct atomic files.
3. **chapters** — a tiny completion record (`{file, duration_seconds}`) written only AFTER concat+probe succeed. The resume short-circuit fires only when the record exists AND the mp3 still exists on disk, so a crash between concat and record-write, or a deleted mp3, correctly falls through and rebuilds.

All cache read/write errors are NON-FATAL: log at Warn and treat as miss/no-op, so a broken cache degrades to "recompute", never aborts a run.

### Adaptive Re-split

`synthSegment` recurses: on a Vertex 400 (`audio.IsOversizedInput`) it re-splits the text at half the byte budget via the segmenter and synthesizes the pieces, flattening blobs in reading order (`maxSplitDepth=4`). A cancelled context is fatal (not a re-split trigger); any non-400 error (e.g. a transient 503) is surfaced unchanged on the first attempt — smaller pieces won't fix it.

### Verification

- **T1:** `go build ./... && go vet ./... && go test ./...` green; `CGO_ENABLED=0 go build ./...` exits 0; `go mod tidy` adds no dependency (no `texttospeech`/`gax-go`/`grpc` re-added — `grpc` was already an indirect dep of genai); `git diff --stat` shows no changes under `internal/gemini/` or `internal/gcp/`.
- **T2 (integration, real ffmpeg/ffprobe):** `TestPipeline_Integration` produces a `manifest.json` that parses, `chapter_01.mp3` with a positive ffprobe duration, and a measured integrated loudness within ±2 LU of -16 LUFS (via a `loudnorm ... print_format=json` analysis pass parsing `input_i`); a re-run over the same `DiskCache` synthesizes 0 new segments; and a fake 400-then-success variant re-splits (exactly one rejection) and still yields a valid normalized mp3.
- **T2 (unit):** `-run Resplit` proves one 400 → segment→halves re-split completes and a 503 is NOT re-split (`calls==1`); `-run Resume` proves a second Run does no new Weave/Prepare/Synthesize work and grows cache puts by 0; `internal/audio -run Oversized` covers the segmenter oversize path and the 400/503 classification.

### Files Created

- `internal/audio/segment.go`, `concat.go`, `synthesize.go` (+ `_test.go` for each)
- `internal/pipeline/cache.go`, `manifest.go`, `run.go`
- `internal/pipeline/cache_test.go`, `manifest_test.go`, `run_test.go`, `run_integration_test.go`

### Gotchas

**Loudness needs enough audio.** EBU R128 integrated loudness gates over the whole file; the integration test synthesizes a ~4s sine per segment so the measurement is stable. A sub-second clip can measure unreliably. The ±2 LU tolerance absorbs the one-pass `loudnorm` dynamic-mode residual.

**`grpc` is a pre-existing indirect dep.** It ships transitively with the genai SDK and was already in `go.mod` before this task. The "no gRPC" constraint means "do not re-add the Cloud TTS gRPC stack (`cloud.google.com/go/texttospeech`, `gax-go`)", which this port does not.

**No TTS usage/cost tracking.** `gemini.SpeechResult` carries only `PCMData` + `ModelVersion` (no token metadata), so only the describe/prepare stages contribute to `usage.Tracker`. audi's TTS character/cost accounting was intentionally dropped (single-currency model, see S1-ab-5).

**Per-chapter image streaming was NOT ported.** audi streamed cover art / per-chapter ID3 art; that is out of scope here and lands in S1-ab-11. The manifest carries text + duration + page span only.

**`--from-page`/`--to-page` has no meaning over a Section tree** and was dropped from `Options`. If page-window selection parity is still wanted for PDFs, it belongs in the S1-ab-9 CLI as a page→chapter-index resolution step — PROPOSED, not implemented here.

---

## CLI Integration (jeep-audiobook command)

**Built:** 2026-08-22 (S1-ab-9)

### What Was Built

Added the fifth jeep CLI tool, `cmd/jeep-audiobook`, that wires the finished audiobook pipeline (`internal/pipeline`, `internal/audio`, `internal/describe`, ingestion sources) onto jeep's shared CLI plumbing (`internal/cli` for RunMain/CommonFlags/Parse/Exitf and `internal/config` for model resolution). This is the final integration seam — all internal packages were already built and tested; this task is almost entirely wiring in a single new `main.go` package.

The tool takes a PDF or ePub input file and produces a directory of narrated MP3 chapters plus a manifest. It supports dry-run mode (no API calls, just prints planned chapters), chapter selection (by index or page window), resumable runs, voice/style customization, and both text and JSON output formats.

### Core Flags and Arguments

**Positional argument:** The input file path (PDF or ePub). NOT a prompt — no `cli.ResolvePrompt` is used.

**Common flags** (via `cli.CommonFlags` with 30-minute default timeout):
`--project`, `--region`, `--format` (text/json), `--quiet`, `--dry-run`, `--version`, `--timeout`, `--retry`

**Audiobook-specific flags:**
- `--voice` / `-v` — TTS voice name (default `"Kore"`)
- `--style` — narration style instructions (default `""`)
- `--resume` — enable DiskCache under `<output>/.cache` (default `false`)
- `--chapters` — comma-separated 1-based indices and ranges, e.g. `"1,3-5"` (default `""` = all chapters)
- `--from-page` / `--to-page` — PDF page window (0 = unbounded); resolved to chapter indices in the CLI layer
- `--concurrency` — worker pool size (default `0` = pipeline/describe defaults of 4)
- `--output` / `-o` — OUTPUT DIRECTORY (default `"audiobook"`)
- `--model` — override text model for describe/prepare (default `""` = resolve TEXT)
- `--tts-model` — override TTS model for synthesis (default `""` = resolve TTS)

### Page-Window to Chapter-Index Resolution

One of the non-trivial design decisions: `--from-page` and `--to-page` flags provide parity with audi's page-window selection, but `pipeline.Options` dropped page fields (meaningless over a Section tree). The resolution happens **in the CLI layer** to avoid reintroducing page fields into the pipeline:

1. Load the document with the selected Source and run `ingest.Structure` (via helper `planChapters`)
2. Compute each chapter's `(start, end)` page span by DFS over its `Section` blocks' `Page` fields (min non-zero → start, max → end)
3. A chapter is IN the window if `end >= fromPage` (when fromPage>0) AND `start <= toPage` (when toPage>0)
4. Page-less formats (ePub) yield 0/0 spans; using page flags with a page-less source errors clearly
5. Union the page-derived indices with any explicit `--chapters` indices and pass the final sorted `[]int` as `pipeline.Options.Chapters`

This keeps the change confined to `cmd/jeep-audiobook` without modifying pipeline internals. The tiny span-walk logic (~15 lines) is replicated locally rather than exporting `pipeline.chapterPageSpan`.

### Source Selection by Extension

`switch strings.ToLower(filepath.Ext(input))`:
- `.pdf` → `&pdfsource.Source{Log: logger}`
- `.epub` → `&epubsource.Source{Log: logger}`
- default → `cli.Exitf(cli.ExitUsage, "unsupported input format %q (supported: .pdf, .epub)", ext)`

Markdown and HTML adapters land in S1-ab-12 and are not included here.

### Dry-Run Behavior (Acceptance-Critical)

When `--dry-run`: load the document, apply the same chapter/page selection logic, and print the planned chapters WITHOUT constructing a `*gemini.Client` or calling `Runner.Run`. This mirrors how other tools print before any API call, but lists chapters instead of showing prompt length:

- **text format:** one line per selected chapter: `Chapter %d: %s (pages %d-%d)` (omit page suffix when both are 0), then a trailing summary showing planned count, input, output dir, and resolved models
- **json format:** `{"tool":"jeep-audiobook","input":...,"output":...,"text_model":...,"tts_model":...,"chapters":[{"index":...,"title":...,"start_page":...,"end_page":...}]}` via `json.NewEncoder(stdout)` with two-space indent

Model resolution (`config.ResolveModelWithConfig`) is offline/pure, so models can be shown in dry-run output even though no client is built.

### Model and Seam Wiring

**Model resolution:**
- describe (`GeminiDescriber.Model`) and prepare (`GeminiPreparer.Model`) use `config.ResolveModelWithConfig(cfg, "TEXT")`
- TTS (`GeminiSynthesizer.Model`) uses `config.ResolveModelWithConfig(cfg, "TTS")`
- Overridden by `--model` (text) and `--tts-model` flags when non-empty

**Seam construction** (all consumed, none modified):
- `*gemini.Client` — constructed via `gemini.NewClient(ctx, project, region)` ONLY on a real run, AFTER the dry-run short-circuit
- `describe.GeminiDescriber` — `{Client: client, Model: textModel, Usage: tracker}`, wrapped in `&describe.Weaver{Describer: ..., Concurrency: conc}`
- `describe.GeminiPreparer` — `{Client: client, Model: textModel, Concurrency: conc, Usage: tracker}` (pointer; satisfies `describe.Preparer`)
- `audio.GeminiSynthesizer` — `{Client: client, Model: ttsModel, Voice: voice, Style: style}` (pointer; satisfies `audio.Synthesizer`)
- `audio.FFmpegConcatenator` — `{Normalize: true}` (loudnorm on by default for podcast target)
- `pipeline.DiskCache` — `{Dir: filepath.Join(outputDir, ".cache")}` ONLY when `--resume`; else leave `Runner.Cache` nil (nopCache)
- `usage.Tracker` — `usage.New()`, passed to describe/prepare adapters AND `Runner.Usage`

**`Runner` field wiring:**
`Source`, `Weaver`, `Preparer`, `Synthesizer`, `Concatenator`, `Concurrency`, `Cache`, `GeminiModel: textModel`, `TTSFingerprint: fmt.Sprintf("%s|%s|%s", ttsModel, voice, style)`, `Usage: tracker`, `Log`, and optionally `Progress` (only when `!quiet`, writes to stderr).

### Why This Approach

**Mirrors jeep-tts, not audi's cobra/viper CLI:** The binding decision (DECISIONS.md S1-ab-9) is to use jeep's existing CLI infrastructure for consistency across all five tools. This means `flag.FlagSet` + `cli.CommonFlags` + `cli.ResolveCommon`, not introducing a second CLI framework.

**Page-window in CLI, not pipeline:** Resolving page flags to chapter indices in the CLI layer keeps the pipeline format-agnostic. The alternative — reintroducing `FromPage`/`ToPage` into `pipeline.Options` — would make ePub/markdown/HTML support awkward. The CLI-side mapping uses each chapter's `[StartPage,EndPage]` span (already computed for the manifest) to filter, then passes a simple `[]int` to the pipeline.

**Output is a DIRECTORY:** Unlike `jeep-tts` which writes a single output WAV file, `jeep-audiobook` writes `chapter_01.mp3`, `chapter_02.mp3`, ..., `manifest.json` into the output directory. The cache lives at `<output>/.cache` when `--resume`. This matches the audi behavior and reflects the multi-chapter nature of audiobook runs.

**TTSFingerprint format:** `fmt.Sprintf("%s|%s|%s", ttsModel, voice, style)` ensures the segment/chapter cache keys change when voice, style, or TTS model change. Without this, a `--resume` run with different settings would replay stale audio from the first run. This format was established in S1-ab-8.

**Stdout cleanliness:** All diagnostics and progress go to stderr via `slog`; only the dry-run listing and the final success/JSON summary go to stdout. This keeps `--format json` output parseable, matching the other tools.

### Verification

**Unit tests** (all offline, no gemini, no ffmpeg):
- Help test: `--help` returns `pflag.ErrHelp`; stderr lists all eight audiobook flags
- Dry-run text: bogus project + `--dry-run` + fixture PDF prints chapter titles and count with no network error (proves no client was built)
- Dry-run JSON: same with `--format json`; unmarshal stdout and assert `tool=="jeep-audiobook"` and `len(chapters)==3`
- Chapter selection: `--chapters 1,3` yields exactly two chapters
- Unsupported extension: `foo.txt` errors with usage message
- Missing input: no positional arg errors clearly

**T2 acceptance:**
1. `go run ./cmd/jeep-audiobook --help 2>&1 | grep -E -- '--voice|--style|--resume|--chapters|--from-page|--to-page|--concurrency|--output'` shows all eight flags
2. `GOOGLE_CLOUD_PROJECT=does-not-matter go run ./cmd/jeep-audiobook --dry-run ./internal/pdfsource/testdata/toc.pdf` lists three chapters and exits 0 with no API error
3. Dry-run JSON is well-formed with 3 chapters
4. `--chapters 2` narrows to exactly one chapter
5. Binary builds successfully
6. Full suite green: `go build ./... && go vet ./... && go test ./...`

A full real run against Vertex is out of scope for T2 — it needs GCP credentials + ffmpeg and is covered by the pipeline's integration test from S1-ab-8.

### Files Created

- `cmd/jeep-audiobook/main.go` — `main()`, `run()`, flag registration, usage printer, helpers (`parseChapters`, `planChapters`, `chapterPageSpan`, `selectIndices`, dry-run printer)
- `cmd/jeep-audiobook/help_test.go` — help flag test
- `cmd/jeep-audiobook/dryrun_test.go` — dry-run text/json, chapter selection, error cases

### Gotchas

**Do NOT touch CRITICAL paths:** `internal/gemini` is imported and its `NewClient` used, but not edited. This keeps the task off auto-escalation.

**Order matters in `run`:** parse → `ResolveCommon` → (dry-run branch returns before any client) → real branch builds client. Never call `gemini.NewClient` in the dry-run path.

**Output is a DIRECTORY, not a file:** The pipeline writes `chapter_NN.mp3` + `manifest.json` into it. The cache lives at `<output>/.cache` when `--resume`.

**TTSFingerprint must be `"model|voice|style"`:** This format (DECISIONS.md S1-ab-8) ensures the segment/chapter cache keys change when voice/style/model change.

**GeminiModel = the TEXT model:** This identifies Weave/Prepare output for the narration cache key, NOT the TTS model.

**Concurrency=0 is correct as a default:** The pipeline and describe adapters treat `<=0` as "use 4". Do not pass a negative.

**Replicated `chapterPageSpan` in cmd is intentional:** The ~15-line span walk is duplicated locally to avoid modifying `internal/pipeline`. If you would rather export it, that's a pipeline change (allowed, non-critical) but is beyond "wire the CLI".

**ePub + page flags is an error, not a silent no-op:** Page-less source with page flags errors clearly.

**Stdout clean:** All diagnostics/progress go to stderr via `slog`; only the dry-run listing and the final success/JSON summary go to stdout (so `--format json` output stays parseable).

---

## Short-Form Audiobooks via Map-Reduce Condenser

**Built:** 2026-08-22 (S1-ab-10)

### What Was Built

Added a `--mode full|summary|abstract` flag to `cmd/jeep-audiobook` that enables short-form audiobook generation. The `full` mode (default) preserves the existing behavior exactly, producing complete narration via `describe.GeminiPreparer`. The new `summary` and `abstract` modes produce condensed audiobooks by routing each chapter's woven text through a map-reduce condenser that chunks the content, maps each chunk to a partial summary via Gemini, and reduces the partials into a single listening-ready narration.

### The Preparer Swap Pattern

The entire feature is realized by introducing a new `describe.Summarizer` type that also implements the `describe.Preparer` interface. The pipeline orchestration remains completely unchanged — it runs `Weave → Preparer.Prepare(text) → segment → synthesize → concat → manifest` per chapter. The CLI selects which `Preparer` implementation to inject based on the mode flag: `GeminiPreparer` for full mode, or `Summarizer` (configured with `ModeSummary` or `ModeAbstract`) for short-form modes.

This design keeps the pipeline format-agnostic and means `internal/pipeline` required zero modifications. The Summarizer receives the woven chapter text (with image descriptions already inline) and returns the condensed narration; everything downstream — segmentation, TTS, loudnorm, caching, manifest generation — is unchanged.

### Map-Reduce Condensation

`Summarizer.Prepare` chunks the incoming text (reusing the same `chunkForPrepare` helper as `GeminiPreparer`), then orchestrates a two-stage map-reduce workflow via `internal/mapreduce`:

1. **Map stage:** Each chunk is sent to `mapreduce.Step` with a mode-specific prompt that instructs the model to produce a concise summary (for `summary` mode) or the single most important idea (for `abstract` mode). The response is structured JSON (`{"summary":"..."}`) that is unmarshaled into a `chunkSummary` struct.

2. **Reduce stage:** All partial summaries are joined and sent to another `mapreduce.Step` call with a reduce prompt that combines them into a single coherent audiobook narration. The response is structured JSON (`{"narration":"..."}`) that becomes the final prepared text.

Mode-specific prompts ensure the output flows naturally for audio narration: plain spoken prose, no markdown or bullets, no invented content. The `summary` mode preserves key points and main ideas across the whole chapter; `abstract` mode distills the chapter's essence into a few spoken sentences.

### Usage Tracking Extension

Extended `mapreduce.Step` to accept a trailing `*usage.Tracker` parameter (nil-safe). After each successful `gemini.Generate` call, the function records the prompt/output/total token counts. This lets summary and abstract runs report token consumption the same way full-mode runs do. All existing test call sites were updated to pass a trailing `nil` for the tracker parameter.

### Files Touched

- `internal/mapreduce/gemini.go` — extended `Step` signature to accept `*usage.Tracker`
- `internal/mapreduce/gemini_test.go` — updated test call sites, added usage accumulation test
- `internal/describe/summarize.go` (new) — `Mode` type, `ParseMode`, `Summarizer` struct, `Prepare` method, mode-specific prompts
- `internal/describe/summarize_test.go` (new) — mode parsing, multi-chunk condenser, empty input, usage tracking, error propagation
- `cmd/jeep-audiobook/main.go` — `--mode` flag, early validation, Preparer selection switch

### Verification

Unit tests demonstrate that `ParseMode` validates mode strings (case-insensitive), `Summarizer.Prepare` condenses multi-paragraph input through the map-reduce path and returns the final narration, empty input short-circuits with no API call, and usage accumulates across all map and reduce calls. An integration test in `internal/pipeline` proves that an abstract-mode run produces a valid manifest with real audio (via a fake generator and real ffmpeg).

The acceptance criteria were executed offline (dry-run with invalid mode errors clearly; valid mode accepted) and online (summarizer reduces a multi-chunk input via fakes; abstract mode drives the pipeline end-to-end with real ffmpeg; full suite green with no new dependencies).

### Known Limitations

The narration cache key is computed from the Gemini text model and the page contents (pre-Prepare inputs), but does NOT include the mode. A resume run that changes mode over the same output directory could serve a stale full narration for a now-summary run. In practice, different modes typically write to different output directories, and `--resume` is opt-in, so this is a known limitation rather than a blocker. Folding mode into the cache key would require modifying `internal/pipeline`, which was out of scope for this task.

The alternative mitigation — having the CLI append the mode to `Runner.GeminiModel` (e.g., `textModel + "|" + mode`) so the cache key naturally differs per mode — was considered but not implemented, to avoid muddying the model-resolution contract for a corner case.

---

## Chapter Cover Art (ID3 APIC embed + PNG sidecar)

**Built:** 2026-08-22 (S1-ab-11)

### What Was Built

Per-chapter cover art for the audiobook pipeline, driven by a new `--art
generate|extract|book-cover|none` flag (default `none`, so existing behavior and
cost are unchanged). When active, each `chapter_NN.mp3` gains an embedded cover
image (an ID3v2 APIC "attached picture" frame) and a matching `chapter_NN.png`
sidecar is written alongside it, with an `image` field recorded in each
manifest chapter entry.

### The `internal/art` Seam

A new package `internal/art` owns image production. Following the mandatory
package-local-narrow-interface pattern (DECISIONS.md S1-ab-1/6/8), it reaches the
shared gemini client only through a local `imageClient` interface that
`*gemini.Client` satisfies structurally; tests inject a fake. `internal/gemini`
is untouched.

- `Mode` + `ParseMode` mirror `describe.ParseMode` (values: generate, extract,
  book-cover, none; invalid values yield a clear error).
- `Provider.ForChapter(ctx, title, summary, figures)` returns `(Image, ok, err)`:
  - **generate** builds a prompt from a house-style prefix + book/chapter title +
    a one-line summary and calls the IMAGE model.
  - **book-cover** generates one image (memoized via `sync.Once`) reused for all
    chapters.
  - **extract** picks the largest embedded figure (by byte length), no API call;
    a chapter with no figure simply gets no art (`ok=false`, not an error).
  - **none** yields `ok=false`.

### The `internal/audio` Embed Seam

`FFmpegArtEmbedder.Embed` muxes the cover into the mp3 via one ffmpeg call
(`-map 0:a -map 1:v -c:a copy -c:v copy -disposition:v attached_pic`), writing to
a temp file in the same directory and renaming over the original (ffmpeg cannot
rewrite in place). The `-disposition:v attached_pic` flag is what makes ffprobe
report the stream as an attached picture. It mirrors `FFmpegConcatenator`'s exec
idiom (captured stderr, tail-on-error, nil-safe logger) and is injectable behind
the `ArtEmbedder` interface.

### Pipeline Wiring

`Runner` gained `Art *art.Provider` (nil => disabled) and `ArtEmbedder
audio.ArtEmbedder` (nil-safe default `&audio.FFmpegArtEmbedder{}`). After
concat+probe and BEFORE the completion record is written, the pipeline produces
the cover, writes the PNG sidecar, and embeds it. Embedding before the record
means a resume that hits the chapter short-circuit can trust the mp3 already
carries the APIC frame. Every art step is NON-FATAL (Warn + continue): the audio
is the product, art is a nicety.

- Generated art is cached under a new `"art"` namespace keyed on
  mode+model+title+summary, so a resume reuses the exact (paid, non-deterministic)
  image. `cachedChapter` and `ChapterEntry` gained an `Image` field; the resume
  short-circuit restores it only when the sidecar file still exists on disk.
- The one-line summary is a deterministic whitespace-collapsed ~200-char prefix
  of the adapted narration (cheapest faithful proxy for "a one-line summary";
  avoids an extra LLM call).
- The PNG sidecar is always a valid PNG: bytes are decoded (`image/jpeg` +
  `image/png` stdlib decoders) and re-encoded as PNG so extract-mode JPEG figures
  still land in an honest `.png` file; on decode failure the raw bytes are written
  as a fallback. Pure-Go, no ffmpeg needed for the sidecar.

### Files Touched

- `internal/art/art.go`, `internal/art/art_test.go` (new)
- `internal/audio/embed.go`, `internal/audio/embed_test.go` (new)
- `internal/pipeline/run.go` — Art/ArtEmbedder fields, art hook, helpers
  (`artEmbedder`, `chapterArt`, `chapterFigures`, `oneLineSummary`,
  `writePNGSidecar`)
- `internal/pipeline/cache.go` — `art` cache key + `cachedArt` codec + `Image`
  on `cachedChapter`
- `internal/pipeline/manifest.go` — `Image` on `ChapterEntry`
- `internal/pipeline/run_art_integration_test.go` (new)
- `cmd/jeep-audiobook/main.go` — `--art` + `--image-model` flags, early
  validation, IMAGE model resolve, dry-run plan, real-branch provider+embedder
  wiring

### Verification

Unit tests (`internal/art`) prove generate calls the fake client and returns
bytes, extract picks the largest figure, extract-with-no-figures and none return
`ok=false`, `ParseMode` rejects a bogus value, and book-cover calls the client
once across two chapters. An `internal/audio` test embeds a real PNG into a real
MP3 and asserts (via ffprobe) an attached-picture video stream. A pipeline
integration test over the two-chapter fixture (fake image client + real
`FFmpegArtEmbedder`) asserts the sidecar is written, the manifest records
`chapter_01.png`, and the mp3 gains an attached-picture stream. CLI checks
confirm `--help` lists `--art`, a bogus mode errors clearly and offline, and a
valid generate dry-run prints the plan (including art mode + image model) with no
API call. Full suite green; `CGO_ENABLED=0` build succeeds; `go mod tidy` adds no
dependency; `internal/gemini/` untouched.

### Notes / Deviations

- The book title used for prompt context is derived from the input filename stem
  (rather than `ingest.Meta.Title`) to avoid a second document load in the CLI
  layer — a pragmatic v1 choice; `ingest.Meta` does carry a `Title` field if
  richer context is wanted later.

---

## Markdown/Plain-Text + HTML/Web-Article Source Adapters

**Built:** 2026-08-22 (S1-ab-12)

### What Was Built

Added two new format adapters to `jeep-audiobook` so it can now ingest Markdown/plain-text and HTML/web-article inputs, producing the same `*ingest.Document` Section tree that PDF and ePub already emit. Each adapter implements the existing `ingest.Source` seam and requires no changes to `internal/gemini`, the pipeline, or any CRITICAL path.

The implementation consists of three new components:
1. **`internal/ingest/headings.go`** — A shared stdlib-only helper that converts flat heading-delimited content into a nested Section tree using a level-stack algorithm
2. **`internal/mdsource`** — Pure stdlib line scanner for ATX markdown headings, fenced code blocks, and prose
3. **`internal/htmlsource`** — `golang.org/x/net/html` walk with readability-style main-content selection and boilerplate stripping

### The Shared Nesting Helper

Both adapters reduce their format to a flat, ordered list of `HeadingItem` (a heading level + title + the content blocks under it) and call `ingest.SectionsFromHeadings` to build the nested tree. This keeps the nesting logic tested in one place rather than duplicated across adapters.

The level-stack algorithm handles skipped levels gracefully: an h1 followed by an h3 nests the h3 under the h1, not as a sibling. When a document has content before its first heading (or no headings at all), the adapter prepends a single top-level `HeadingItem{Level:1, Title:"", Blocks: <preamble>}` so a headingless plain-text file becomes one untitled section holding all prose.

### Markdown Parser

The `internal/mdsource` adapter uses a pure stdlib line scanner (no goldmark or markdown library) to parse:
- **ATX headings:** Lines matching `^(#{1,6})\s+(.*)` open a new heading; level = count of `#`, title = trimmed remainder (trailing `#` stripped)
- **Fenced code:** Lines starting with ` ``` ` or `~~~` toggle a code fence; everything between the fences is one `BlockCode` (verbatim, including any `#` inside)
- **Prose:** Consecutive non-heading, non-fence lines accumulate into one `BlockProse` (joined with `\n`, trimmed, dropped if empty)

Setext headings (`===`/`---` underlines) are out of scope for v1. Images (`![alt](url)`) are not extracted since they're remote URLs, not local bytes. `Meta.Title` is set to the first level-1 heading if present.

### HTML Parser

The `internal/htmlsource` adapter uses `golang.org/x/net/html` (already a direct dependency, promoted from indirect) to parse web articles with readability-style heuristics:
- **Main-content selection:** Find the first `<article>`; else first `<main>`; else `<body>`; else document root
- **Boilerplate strip:** Skip entire subtrees of `<script>`, `<style>`, `<nav>`, `<header>`, `<footer>`, `<aside>`, `<form>`, and elements with `role="navigation"` (making `<body>` fallback usable)
- **Headings:** `<h1>`..`<h6>` become heading items, Level = the digit
- **Code:** `<pre>` subtrees become `BlockCode` with text content (preserving newlines; whitespace NOT collapsed inside pre)
- **Prose:** Visible text outside headings/pre accumulates, whitespace-collapsed via `strings.Fields`, flushed into `BlockProse` on the next heading/pre/end

Remote `<img>` (http/https/`//`) are skipped — no network fetch. `data:` URI images are also skipped for v1. `Meta.Title` comes from the `<title>` element, else the first heading.

### CLI Integration

Extended `cmd/jeep-audiobook/main.go` so the adapters are reachable:
- `.md`, `.markdown`, `.txt`, `.text` → `&mdsource.Source{Log: logger}`
- `.html`, `.htm` → `&htmlsource.Source{Log: logger}`
- Updated help text from "PDF or ePub" to "PDF, ePub, Markdown, or HTML file"
- Updated the unsupported-format error to list `.pdf, .epub, .md, .txt, .html`

The existing `cmd/jeep-audiobook/dryrun_test.go` test used `foo.txt` as its "unsupported" example. Once `.txt` mapped to `mdsource`, that input was no longer unsupported and the test broke. The fix: changed the test argument to `foo.docx` (genuinely unsupported).

### Files Created

- `internal/ingest/headings.go`, `internal/ingest/headings_test.go`
- `internal/mdsource/mdsource.go`, `internal/mdsource/mdsource_test.go`, `internal/mdsource/testdata/article.md`
- `internal/htmlsource/htmlsource.go`, `internal/htmlsource/htmlsource_test.go`, `internal/htmlsource/testdata/article.html`

### Files Modified

- `cmd/jeep-audiobook/main.go` — `selectSource` switch, imports, `printUsage`
- `cmd/jeep-audiobook/dryrun_test.go` — changed `foo.txt` to `foo.docx`

### Why This Approach

**Two packages, not one:** Following the existing pdfsource/epubsource sibling convention keeps format-specific parsing isolated. The shared tree-building logic was factored into `internal/ingest/headings.go` rather than duplicated.

**Shared helper in `internal/ingest`:** Both adapters need identical nesting; one tested implementation beats two copies. The helper is pure stdlib so `ingest` stays dependency-free and off CRITICAL. The alternative — a private copy in each adapter — was rejected to avoid divergence.

**Stdlib-only markdown parser:** A line scanner covers the acceptance scope (ATX headings + fenced code + prose) with no new dependency. `goldmark` or a full markdown library would be overkill and add weight.

**Readability-style HTML selection:** Main-content detection (prefer `<article>`, else `<main>`, else `<body>`) plus boilerplate strip makes the adapter usable on real web pages without ingesting menus and footers. `golang.org/x/net/html` was the robust, CGO-free choice and was already present as an indirect dependency.

**No network fetch:** Remote images are skipped. This keeps the tool offline-capable and avoids network errors/latency/security concerns. A local PDF with embedded images works; a web article referencing CDN images does not (documented v1 limitation).

### Verification

Unit tests cover the happy path (correct section titles/levels, code blocks, prose blocks present) and edge cases (preamble handling, no-heading files, boilerplate stripped). The CLI end-to-end dry-run acceptance proved the adapters are reachable: running `--dry-run` on `article.md` and `article.html` prints the planned chapters without contacting Vertex AI, and an unsupported `.docx` errors clearly. Full suite green; `CGO_ENABLED=0 go build ./...` exits 0 (pure-Go); `go mod tidy` shows no new dependency added (`golang.org/x/net` was already direct); `git diff --stat` shows no files under CRITICAL paths.

### Gotchas

**The sibling test break was mandatory:** `cmd/jeep-audiobook/dryrun_test.go:71` used `foo.txt` as its "unsupported" input. Once `.txt` became supported, the test's assertion broke. Changing it to `foo.docx` was required to keep T1 green.

**ATX only for v1:** Setext headings (`===`/`---` underlines) are not parsed. This is documented in the mdsource package comment as a known limitation.

**No images from markdown/HTML:** Markdown images are remote references (no local bytes); HTML images require network fetch or data-URI decoding (deferred). Acceptance does not require them. Narration is prose + code.

**Boilerplate strip is load-bearing:** Without skipping `<nav>`/`<header>`/`<footer>`, the `<body>` fallback would ingest entire menus and site chrome. The strip list makes web articles usable.

**Whitespace collapse in HTML:** Text from HTML layout has whitespace collapsed (`strings.Fields`) per the browser rendering model. This is distinct from "no word-level rewriting" — layout whitespace is part of the format, not content.

---

## Documentation Complete: README + Archive + audi Supersession Proposal

**Built:** 2026-08-22 (S1-ab-13)

### What Was Built

Pure documentation update to make `jeep-audiobook` discoverable now that all technical tasks (S1-ab-1 through S1-ab-12) are complete. No production code or CRITICAL paths were touched. This task delivered:

1. **README.md updates** — Added `jeep-audiobook` to the tool table and install block, documented the ffmpeg/ffprobe runtime dependency in Setup, noted that audiobook reuses TEXT/TTS/IMAGE model uses, and added a complete `## jeep-audiobook` section (flags table + examples).

2. **docs/features/ab.md archive entry** — This S1-ab-13 entry documenting the port completion and the audi-supersession proposal.

3. **TODO.md Sprint 0 task** — Added a new `S0-infra-1` task proposing the GitHub archive of the `audi` repo (`github.com/tvaroska/learning/audi`) now that the port to jeep is complete, and bumped the `infra` counter from 0 to 1. The task is a PROPOSAL — the GitHub archive action itself is a manual repo-admin step, not performed by this task.

4. **DECISIONS.md entry** — Recorded the docs-only nature of the task, the flag-parity verification as the anti-drift guard, and the audi-archive proposal (not execution).

### Why This Approach

**README flag docs match `--help` exactly:** The acceptance criteria grep `--help` output for every flag documented in the README. This anti-drift guard ensures the documentation stays truthful as the tool evolves. The flag table lists ONLY the tool-specific flags (voice, style, mode, art, resume, chapters, from-page, to-page, concurrency, output, model, tts-model, image-model); the common flags (project, region, retry, timeout, quiet, dry-run, version, format) are documented once in the shared "Common flags" section.

**Output is a DIRECTORY, not a file:** The table explicitly notes that `-o/--output` specifies a directory (where `chapter_NN.mp3` + `manifest.json` are written), unlike `jeep-image`/`jeep-tts` where `-o` is a single output file. This is the load-bearing gotcha from DECISIONS.md S1-ab-9.

**ffmpeg/ffprobe runtime dependency documented:** `jeep-audiobook` is the only jeep tool that requires these binaries on PATH (for PCM→MP3 encoding, per-segment loudness normalization, concat, and ID3 cover-art mux). The README Setup section now calls this out explicitly.

**Supported formats listed:** PDF, ePub, Markdown (`.md`/`.markdown`/`.txt`/`.text`), HTML (`.html`/`.htm`). The `--from-page`/`--to-page` flags are documented as PDF-only; using them with page-less sources (ePub/Markdown/HTML) errors clearly.

**Model reuse, no new key:** The Model configuration section was updated with a one-liner noting that `jeep-audiobook` reuses existing model uses (TEXT for describe/prepare, TTS for synthesis, IMAGE for `--art` generation) without introducing a new `JEEP_MODEL_*` environment variable. This aligns with the design decision from the plan (S1-ab-13, step 4).

**Runnable examples:** The examples block includes both simple invocations (`jeep-audiobook book.pdf`) and complex ones showing flag combinations (`-v Puck --chapters 1,3-5`, `--art generate`, `--resume`, `--mode summary`). Two in-repo dry-run examples (`internal/mdsource/testdata/article.md`, `internal/htmlsource/testdata/article.html`) are provided as acceptance-check commands that run offline without GCP credentials or network access.

**audi archive is a PROPOSAL, not an action:** The Sprint 0 task explicitly states "repo-admin is manual" so implementers understand this is a planning item, not executable automation. The audi repo (`/home/boris/learning/audi`) is left untouched — no `gh repo` command, no file edits in the audi codebase.

### Verification

**T1 (gates the commit):**
- `go build ./... && go vet ./... && go test ./...` — all green
- `git diff --name-only -- internal cmd spec .github .goreleaser.yml CRITICAL.md` — prints nothing (proves no code or CRITICAL path was touched)

**T2 (gates `- [x]`):**
1. **Flag-parity/no-drift:** `go run ./cmd/jeep-audiobook --help 2>&1` — every flag the README documents (voice, style, mode, art, resume, chapters, from-page, to-page, concurrency, output, model, tts-model, image-model) appears in `--help` and vice-versa, same defaults.

2. **Install path is real:** `go build ./cmd/jeep-audiobook` exits 0, proving the documented `go install github.com/tvaroska/jeep/cmd/jeep-audiobook@latest` path is valid.

3. **Documented example runs offline:** `GOOGLE_CLOUD_PROJECT=test-project go run ./cmd/jeep-audiobook --dry-run internal/mdsource/testdata/article.md` prints planned chapters and exits 0 without contacting Vertex AI, proving the Markdown-format claim and a documented example are accurate.

4. **README grep checks:** All four assertions pass:
   - `grep -q '## jeep-audiobook' README.md` — section present
   - `grep -qi 'ffmpeg' README.md` — prereq documented
   - `grep -qi 'jeep-audiobook@latest' README.md` — install line present
   - `grep -Eqi 'pdf.*epub.*(markdown|md).*html|epub.*markdown' README.md` — formats listed

5. **Archive entry exists:** `grep -q 'S1-ab-13' docs/features/ab.md` confirms the entry.

6. **Sprint 0 proposal + counter:** `grep 'S0-infra-1' TODO.md` finds the open `- [ ]` task proposing the audi repo be archived, and `grep 'infra=1' TODO.md` confirms the counter was bumped from 0 to 1.

7. **S1-ab-13 flipped to done:** (After the `/implement` archive step) `grep '\- \[x\] S1-ab-13' TODO.md` confirms it was marked done with the standard `_(done 2026-08-22; see docs/features/ab.md)_` suffix.

### Files Touched

- `README.md` — tool table, install block, Setup section (ffmpeg note), Model configuration section (audiobook model-reuse note), new `## jeep-audiobook` section
- `docs/features/ab.md` — this S1-ab-13 archive entry
- `TODO.md` — Sprint 0 `S0-infra-1` task, counter bump (`infra=0` → `infra=1`), and at close: S1-ab-13 flipped to `- [x]`
- `DECISIONS.md` — S1-ab-13 append entry

### Gotchas

**Do NOT touch code or CRITICAL paths:** This is a docs-only task. `internal/`, `cmd/`, `spec/`, `.github/`, `.goreleaser.yml`, and `CRITICAL.md` must remain unchanged. The T1 `git diff` check enforces this.

**Flag docs MUST match `--help` exactly:** The T2 anti-drift check verifies one-for-one parity. If a flag exists in `--help` but is not documented in the README, or vice-versa, the check fails. Defaults must also match.

**audi archive is PROPOSED, not executed:** Do NOT run `gh repo archive`, do NOT edit files in `/home/boris/learning/audi`. The Sprint 0 task is the proposal; a human will execute the GitHub archive action later.

**The `-o` is-a-directory note is load-bearing:** Users coming from `jeep-tts` (where `-o output.wav` is a file) will expect `-o` to be a file. The README must explicitly state "Output DIRECTORY (not a file)" to prevent confusion.

**README structure mirrors existing tools:** The `## jeep-audiobook` section follows the same template as `## jeep-research` (one-line description, synopsis, flags table, examples block). The common flags stay in the shared table; only tool-specific flags are listed in the audiobook section.

**Examples must be runnable:** The dry-run examples (`internal/mdsource/testdata/article.md`, `internal/htmlsource/testdata/article.html`) are real files that exist in the repo and can be executed offline without GCP credentials.
