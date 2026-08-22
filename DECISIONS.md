# jeep — Decisions & Learning Log

**Append-only.** Committed. Newest entries at the top. Agents READ this before
working and APPEND an entry after every task (`/implement`, `/implement-all`).
This is the *committed* home for reasoning that would otherwise die in local,
gitignored `.claude/plans/` files.

**How to use:**
- One entry per meaningful decision or learning. Never edit or delete past
  entries — supersede them with a new dated entry instead.
- Keep it concrete: what changed, why, and what bit us. Future agents rely on
  the Gotchas to avoid repeating mistakes.
- Architectural decisions that need standalone detail also get an ADR in
  `design/decisions/` (see `design-adr-template.md`); link it here.

---

## 2026-08-22 — Port `pipeline` (section-driven, resumable, loudness-normalized) (S1-ab-8)

**Task:** S1-ab-8 · **Area:** infra (new packages `internal/audio` + `internal/pipeline`)

**Decision:** Ported audi's `internal/pipeline` (+ the TTS-adjacent segment/concat
helpers from audi `internal/tts`) into two jeep packages, rewired to (1) drive off
the `ingest` Section tree, not PDF pages; (2) synthesize through the shared
`gemini.Client` (`GenerateSpeech` → PCM, then `gemini.PCMToMP3`) instead of Cloud
TTS gRPC; while keeping the resumable 3-level content-addressed cache, per-segment
EBU R128 loudness normalize, adaptive re-split on Vertex 400, and per-book manifest.

Split into `internal/audio` (segmenter + `FFmpegConcatenator` + the `Synthesizer`
seam with `GeminiSynthesizer`/`StubSynthesizer`) and `internal/pipeline` (the
`Cache`/`DiskCache`, `Prober`/`FFprobeProber`, and the `Runner`). The synth seam
uses the same package-local narrow-interface idiom as describe/mapreduce
(`speechClient` that `*gemini.Client` satisfies structurally), so nothing under the
CRITICAL `internal/gemini` path was touched.

**Rationale:** Section-tree flattening (`chapterToPageContents`: one
`describe.PageContent` per Block — figure→ImageRef, prose→text, pre-order DFS)
preserves reading order and lets Weave splice image descriptions inline, while
decoupling the pipeline from pagination so ePub/markdown/HTML work uniformly. The
narration cache stabilizes non-deterministic Gemini output across restarts, which
is the prerequisite for the segment cache to hit on a fresh resume. Re-split keys
only on the 400 classification (`audio.IsOversizedInput` → `gemini.IsInvalidInput`)
so oversized segments no longer crash a full-book run.

**Gotchas:**
- **Loudness needs enough audio.** EBU R128 integrated loudness gates over the
  whole file; the integration test synthesizes ~4s of sine per segment so the
  measurement is stable, and asserts within ±2 LU of -16 LUFS (the one-pass
  `loudnorm` dynamic-mode residual). A sub-second clip measures unreliably.
- **`grpc` is a pre-existing indirect dep** (ships with the genai SDK; already in
  `go.mod`). The "no gRPC" constraint means "don't re-add the Cloud TTS gRPC stack
  (`cloud.google.com/go/texttospeech`, `gax-go`)" — this port adds no dependency.
- **No TTS usage/cost tracking:** `gemini.SpeechResult` carries only `PCMData` +
  `ModelVersion` (no token metadata), so only describe/prepare feed `usage.Tracker`.
- **Per-chapter cover-art / ID3 streaming NOT ported** (out of scope → S1-ab-11).
- **`--from-page`/`--to-page` dropped from `Options`** (meaningless over a Section
  tree). **PROPOSAL for S1-ab-9:** if PDF page-window selection parity is still
  wanted, resolve a page window → chapter indices in the CLI layer (using each
  chapter's `[StartPage,EndPage]` span from the manifest/`chapterPageSpan`) and pass
  the resulting `Options.Chapters` — do not reintroduce page fields into the pipeline.

---

## 2026-08-22 — Generic map-reduce helper on `gemini.Generate` (S1-ab-7)

**Task:** S1-ab-7 · **Area:** infra (new package `internal/mapreduce`)

**Decision:** Implemented a generic, provider-agnostic chunk→map→reduce[+refine] orchestrator in `internal/mapreduce` that routes workflows through `gemini.Generate` with structured JSON schemas. The package is the concurrency primitive for the audiobook pipeline's `--mode summary|abstract` feature (S1-ab-10), which processes over-window inputs by chunking, parallel mapping, and reducing.

Key design choices:
1. **Two-file split:** `mapreduce.go` (generic orchestrator, no GCP/genai deps) + `gemini.go` (thin Gemini binding).
2. **Generics `[E, P, R any]`** (Element → Partial → Result) for reusability across any chunk→map→reduce workflow.
3. **Concurrency via `golang.org/x/sync/errgroup`:** Bounded worker pool + fail-fast semantics. First Map error cancels remaining work and propagates; Reduce/Refine never run on partial failure. `SetLimit(concurrency)` where concurrency defaults to 4 when `≤0` (never pass 0/negative — blocks forever).
4. **Order preservation:** Map results written to `partials[i]` by index, never appended from goroutines, so Reduce receives partials in element order.
5. **Empty Elements edge case:** When `Elements` is empty/nil, still calls `Reduce(ctx, nil)` so callers get a defined result (e.g., "No content to summarize") instead of a zero value.
6. **Local `generator` interface** (following S1-ab-6 precedent): package-local interface matching `gemini.Client.Generate`; `*gemini.Client` satisfies it structurally; tests inject a `fakeGenerator`. This is why the task does NOT touch CRITICAL `internal/gemini` — the seam is the same "fake client returns canned responses" pattern as describe.
7. **`Step[T]` is minimal:** Calls `gemini.Generate` with schema, unmarshals JSON into `T`. Does NOT record usage (deferred to S1-ab-10 to avoid gold-plating). Retries driven by `GenerateOptions.Retries`; this function adds none.

**Spec/plan discrepancy resolved:** The task scope said "refit `jeep-research`'s hand-rolled synthesis onto the helper." Investigation revealed the current `jeep-research` performs NO local synthesis — it delegates to the Vertex AI interactions deep-research agent (`internal/interactions`), which synthesizes server-side. There is no hand-rolled map-reduce to unify. The task left `cmd/jeep-research` and `internal/interactions` completely untouched (acceptance: still builds + tests green, which holds trivially). The helper's real consumer is S1-ab-10 (`--mode summary|abstract`), which is where "one implementation" is realized.

**Verified:** Tests (chunk→map→reduce producing expected reduced result over a fake, order preservation, fail-fast Map error, empty-Elements calls Reduce with nil, Refine application, context cancellation, missing Map/Reduce errors, Step unmarshals JSON / forwards errors / rejects bad JSON) all PASS; `go build ./... && go vet ./... && go test ./...` all green; `go test -race ./internal/mapreduce/` exits 0 (no DATA RACE); `CGO_ENABLED=0 go build ./...` exits 0 (pure-Go); `go mod tidy` introduces NO new dependency (`golang.org/x/sync` and `google.golang.org/genai` already direct).

**Gotchas:**
- **Do NOT touch `internal/gemini`** — the local `generator` interface is mandatory. If you think you must change `internal/gemini`, STOP: that auto-escalates (stronger model + review).
- **JSON schema required** — test fakes MUST return valid JSON matching the target struct (e.g., `{"summary":"text"}`, NOT just `"text"`), or `json.Unmarshal` fails (same bite as S1-ab-6).
- **`SetLimit(n)` with n<1 blocks forever** — default concurrency to 4 when `spec.Concurrency ≤ 0`.
- **Reduce/Refine run on outer `ctx`**, not the errgroup's `gctx` (cancelled after Wait).
- **Order preservation via indexed writes** — `partials := make([]P, len(spec.Elements))`; each goroutine writes `partials[i] = p`, never appends.
- **No usage tracking in this task** — defer to S1-ab-10 to avoid gold-plating. If Step later needs to expose tokens, return `*gemini.GenerateResult` alongside `T` or accept a `*usage.Tracker` param.

---

## 2026-08-22 — Port `describe` onto `gemini.Generate` + structured JSON (S1-ab-6)

**Task:** S1-ab-6 · **Area:** infra (new package `internal/describe`)

**Decision:** Ported audi's `internal/describe` (figure description + inline weaving +
listening adaptation) into jeep, routing every LLM call through `gemini.Generate` with
structured-JSON response schemas and deleting audi's hand-rolled genai plumbing.

Key design choices:
1. **Local `generator` interface is mandatory.** `gemini.Client.models` is unexported;
   an external package cannot inject a fake into a real `*gemini.Client`. describe
   defines its own narrow interface matching `gemini.Client.Generate`; `*gemini.Client`
   satisfies it structurally, and tests supply a `fakeGenerator`.
2. **Structured JSON wrapping.** `gemini.Generate` sets `ResponseMIMEType="application/json"`
   + `ResponseSchema` when `schema != nil`; the returned `GenerateResult.Text` is the
   JSON string. You MUST `json.Unmarshal` it into the `{description}` / `{narration}`
   struct — do not return `res.Text` raw (it would be a JSON blob, not narration).
3. **`GenerateResult` has no `TotalTokens`.** It exposes only `PromptTokens` and
   `OutputTokens` (int32). Compute total = prompt+output for `usage.Record`.
4. **No ThinkingConfig in jeep.** audi disabled thinking (`ThinkingBudget:0`) so
   reasoning tokens wouldn't eat `MaxOutputTokens` and truncate the rewrite. jeep's
   `GenerateOptions` has no such field, and adding one would modify CRITICAL
   `internal/gemini`. Instead, **leave prepare's `MaxTokens` unset (0)** so no output
   cap is imposed and the model's default budget applies; the existing ~6000-char
   chunking keeps each rewrite small. If real runs later show truncation, PROPOSE a
   follow-up task to add a thinking-budget knob to `GenerateOptions` — do not sneak
   it into this task.
5. **Retry semantics changed.** jeep retries only 429/500/503 (see `isRetryable` in
   gemini.go); audi also retried 502/504 and `context.DeadlineExceeded`. This is the
   accepted jeep convention — do not re-introduce a describe-local retry loop. Drive
   retries via `GenerateOptions.Retries` (default 4). Tests verify a 400
   (`genai.APIError{Code:400}`) is NOT retried (call count == 1), while 503 would be
   (handled by gemini layer, not tested at describe level).
6. **Artifact-repair prompt guidance is load-bearing.** The prepare system prompt now
   explicitly instructs the model to repair extraction artifacts: re-join words split
   across line wraps (hyphenation / mid-word breaks), restore dropped leading letters
   and ligatures at word starts (e.g. `unied`→`unified`, `ree pillars`→`three pillars`,
   dropped `The`/`Th`/`fi` restored). This is the phase-0 spike's validated solution to
   PDF text quality — do NOT remove or weaken this guidance.

**Verified:** Tests (Describe with JSON `{"description":"..."}`, Prepare with JSON
`{"narration":"..."}`, retry test showing 503 propagates vs 400 is fatal, usage-recording,
Weave concurrency, chunk logic) all PASS; artifact-repair test asserts the real exported
constant contains "ligature" or "line" guidance; `go build ./... && go vet ./... && go test
./...` all green; `CGO_ENABLED=0 go build ./...` exits 0 (pure-Go); `go mod tidy` promotes
only `golang.org/x/sync` to direct require; `git diff --stat` shows no files under
`internal/gemini/`.

**Gotchas:**
- **Local `generator` interface is a pattern** — reuse it in other packages that need to
  fake `gemini.Client` (e.g., pipeline, map-reduce).
- **JSON schema required** — tests MUST return valid JSON matching the schema (e.g.,
  `{"description":"text"}`, NOT just `"text"`), or unmarshal will fail.
- **Retries tested indirectly** — the describe package does NOT test retry counts; that's
  gemini's responsibility. Tests verify a 400 error propagates (no retry) by checking
  `fake.calls == 1`.
- **Artifact-repair guidance is in the PROMPT** — it's a string constant, not a runtime
  flag. Tests assert its presence. Do not dilute or remove it; the phase-0 spike validated
  it as the solution to PDF text quality.
- **No `NewGeminiDescriber` / `NewGeminiPreparer`** — CLI wiring (S1-ab-9) constructs
  adapters directly with a `*gemini.Client` and sets fields. There's no factory function.

---

## 2026-08-22 — Token-only usage tracking + structured logging (S1-ab-5)

**Task:** S1-ab-5 · **Area:** infra (new packages `internal/usage`, `internal/log`)

**Decision:** Ported audi's `internal/log` verbatim and `internal/usage` with a
simplification to one currency (tokens only). Both packages are stdlib-only
(`log/slog`, `sync`), nil-safe, and concurrency-safe.

Key design choices:
1. **`log` is a verbatim port** from audi: pure stdlib (`io`, `log/slog`, `os`,
   `strings`), no API changes. The only edit: dropped the audi-specific `(S3-5)`
   task reference from the package doc comment.
2. **`usage` tracks tokens only** (one currency). Dropped from audi: `Pricing`,
   `DefaultPricing`, `RecordTTS`, `ttsCounters`, `TTSChars`, `TTSCalls`,
   `EstimatedCostUSD`, and all cost math. The token metadata comes from
   `genai.GenerateContentResponse.UsageMetadata` (read by callers in later tasks;
   this package does NOT import genai).
3. **Renamed `RecordLLM`→`Record`** (single currency means no "LLM" qualifier
   needed) and `llmCounters`→`counters`.
4. **Simplified `Report`** to hold only `PromptTokens`, `OutputTokens`,
   `TotalTokens`, `Calls` (aggregated across all models).
5. **Simplified `Log`** to take only a logger (no pricing parameter); emits
   "run token usage" with four keys (`prompt_tokens`, `output_tokens`,
   `total_tokens`, `calls`).
6. **Preserved the nil-safe + mutex-guarded contract** — all methods no-op on a
   nil `*Tracker` (so later adapters can call unconditionally), and lock `mu` on
   every mutation/read (load-bearing for the S1-ab-8 concurrent pipeline).

**Verified:** Tests pass showing two `Record` calls (same model + across two models)
sum into `Report{PromptTokens, OutputTokens, TotalTokens}` and increment `Calls` per
call; nil `*Tracker` no-ops and returns zero `Report`; `Log` does not panic on nil
receiver or nil logger; `go test -race -run TestTracker_Concurrent ./internal/usage/`
exits 0 with no DATA RACE; log tests show Warn-level logger suppresses Info, Debug
logger emits all levels, `parseLevel` is case-insensitive+trimmed. `go build ./... &&
go vet ./... && go test ./...` all green. Smoke test (New→Record x2→Report totals
300/100/400/2→Log emits "run token usage" line) passes. The usage package imports only
`log/slog` and `sync` (no `google.golang.org/genai`); no Pricing/DefaultPricing/
RecordTTS/EstimatedCostUSD symbols exist.

**Gotchas:**
- **Stdlib-only is load-bearing** — do NOT import `google.golang.org/genai` into
  `internal/usage`. Callers convert `UsageMetadata` fields at the call site.
- **Nil receiver no-op is a contract** — later adapters may hold a possibly-nil
  `*Tracker` ("recording disabled") and call it unconditionally.
- **Lock on `Report`/`Log` too**, not just `Record` — reads race with concurrent
  writes otherwise; `-race` will catch a missing lock.

---

## 2026-08-22 — ePub `Source` adapter on the pure-Go stack (S1-ab-4)

**Task:** S1-ab-4 · **Area:** infra (new package `internal/epubsource`; `go.mod`/`go.sum`)

**Decision:** Implemented `ingest.Source` for EPUB in a new `internal/epubsource`
package on the pure-Go stack: `archive/zip` for the container, `encoding/xml` for
OPF/NCX parsing, and `golang.org/x/net/html` for XHTML/nav parsing (CGO-free).
Output is an `*ingest.Document` that `ingest.Structure` consumes unchanged;
`internal/ingest` stays dependency-free (heavy deps live only in the adapter).

Key design choices:
1. **Spine-index-span DFS reuse:** The TOC→section mapping mirrors pdfsource's
   page-span algorithm, substituting 0-based spine index for page number. Each TOC
   node's href resolves to a spine doc; the node's own span = `[start, nextStart-1]`
   where `nextStart` is the next node's start in pre-order (first child if any,
   else next sibling, else nearest ancestor's next sibling); the last node runs to
   `len(spine)-1`. Two TOC entries pointing at the same file get an empty span for
   the earlier node, so content is never double-counted.
2. **EPUB3 nav → EPUB2 NCX fallback:** Prefer the nav document (manifest item with
   `properties~="nav"`), parse its `<nav epub:type="toc"><ol>` structure. If absent
   or unparseable, fall back to the NCX (`application/x-dtbncx+xml`, referenced by
   `spine toc=` or manifest media-type), parsing its `<navMap>` nested `<navPoint>`s.
3. **Per-spine-doc fallback when no TOC:** When neither nav nor NCX is present or
   parseable, emit one top-level section per spine document (Level 1), with title
   from XHTML `<title>` element, else first `<h1>`/`<h2>`, else `"Section N"`. This
   differs from PDF's single-section fallback — the spine gives natural chapter
   boundaries, so it is a more useful fallback (call this out as the ePub-specific
   advantage).
4. **Fragment-level splitting deferred:** TOC hrefs like `ch1.xhtml#part2` have the
   fragment stripped for content mapping — the entire spine doc's content is
   assigned to the section. Fragment-level splitting (splitting a spine doc into
   sub-sections based on `#id` anchors) is OUT OF SCOPE for v1; document this
   choice.
5. **golang.org/x/net/html for XHTML/nav parsing:** Parsing XHTML with
   `encoding/xml` is fragile (HTML entities like `&nbsp;`, non-strict markup);
   `x/net/html` is the robust, pure-Go, CGO-free choice. It was already present as
   an indirect dep (`v0.57.0` in go.sum with an `h1:` hash), so using it promotes
   it to a direct require (via `go mod tidy`) — no new download, no network fetch.
6. **Front matter before first TOC entry DROPPED:** Spine indices `< order[0].start`
   are discarded (mirrors PDF; `ingest.Structure` would drop cover/copyright/ToC
   boilerplate anyway; acceptance does not need it).

**Verified:** Tests (EPUB3Nav, EmbeddedImage, NCXFallback, NoTOC,
HrefWithFragmentAndSubdir) all PASS; the image test shows a `BlockFigure` with
non-empty `Data` and `image/png` MimeType preceded by a `BlockProse` (reading-order
interleave); the nav test exercises `ingest.Structure(doc)` and gets ≥1 `Chapter`
whose `Sections` carry non-empty prose Blocks (proves the tree flows through the
downstream chapterizer unchanged); `CGO_ENABLED=0 go build ./...` exits 0 (pure-Go).

**Gotchas:**
- **Relative href bases differ:** OPF hrefs are relative to the OPF dir; nav/NCX
  hrefs are relative to the NAV/NCX file's own dir; `<img src>` is relative to its
  XHTML's dir. Do NOT use one base for all three — always
  `path.Clean(path.Join(dir, url.PathUnescape(href)))`.
- **Strip the fragment** (`strings.SplitN(href, "#", 2)[0]`) before resolving to a
  zip path / spine index. Otherwise TOC entries with `#part2` won't map to their
  spine doc.
- **zip paths use `/`**; `zip.File.Name` is already slash-separated — do NOT
  `filepath.Join` (would break on Windows); use the `path` package.
- **encoding/xml namespace matching:** A struct tag WITHOUT a namespace (e.g.
  `xml:"item"`) matches that local name in ANY namespace, so you generally do NOT
  need to spell out the OPF/NCX namespaces. For Dublin Core use
  `xml:"metadata>title"` / `xml:"metadata>creator"` (direct child path).
- **`epub:type` attribute:** In `x/net/html`, a namespaced attribute surfaces with
  `Attr.Key == "epub:type"` (HTML parser does not split the prefix). Match on the
  raw key containing `toc` as a token (substring match is liberal and works).
- **Best-effort, never panic:** A book with an unreadable image, a TOC href that
  doesn't resolve, or a missing nav must degrade (warn + continue / fallback), never
  error out — mirror pdfsource. The ONLY hard errors are: cannot open zip, no
  container.xml, no OPF, or empty spine.
- **Do not touch `internal/ingest`** — it must stay stdlib-only (DECISIONS.md
  2026-08-22 S1-ab-2). All ePub deps live in `internal/epubsource`.
- **RAW text** — no ligature/de-glue repair (DECISIONS.md). Whitespace collapse from
  HTML layout (runs of `\n` / `\t` / ` ` → single space) is allowed and expected;
  word-level rewriting is not.
- **mimetype entry:** Write it first and stored (uncompressed) in the fixture for
  correctness; when reading, you can ignore it (rely on container.xml).

---

## 2026-08-22 — PDF `Source` adapter on the pure-Go stack (S1-ab-3)

**Task:** S1-ab-3 · **Area:** infra (new package `internal/pdfsource`; `go.mod`/`go.sum`)

**Decision:** Implemented `ingest.Source` for PDF in a new `internal/pdfsource`
package on the phase-0 pure-Go stack: `pdfcpu api.Bookmarks()` (outline→section
tree), `ledongthuc/pdf GetPlainText` (per-page prose, stored RAW), and `pdfcpu
api.ExtractImages()` (figure blocks, page-associated via `img.PageNr`). Output is
an `*ingest.Document` that `ingest.Structure` consumes unchanged; `internal/ingest`
stays dependency-free (heavy deps live only in the adapter).

Key design choices:
1. **DFS page-span assignment:** each bookmark node's OWN span = `[startPage,
   nextStart-1]` where `nextStart` is the next node's start in pre-order DFS (last
   node runs to page count). The gap between a node and its first child is exactly
   the node's intro pages; bookmarks sharing a page get an empty span, so prose is
   never double-counted. Per span, emit prose block then that page's figures.
2. **Front matter before the first bookmark is DROPPED** (cover/copyright/ToC;
   `ingest.Structure` would drop it anyway; acceptance doesn't need it).
3. **Empty-outline fallback:** no bookmarks → one synthetic top-level section with
   all pages' prose + figures (mirrors audi's whole-doc fallback).
4. **Page count from pdfcpu, text best-effort from ledongthuc:** pdfcpu's parser is
   more lenient, so it is authoritative for span math; a ledongthuc open failure no
   longer sinks the book (outline + images still ingest with empty prose).
5. **Package `pdfsource` (not `pdf`, which collides with the ledongthuc import),
   flat under `internal/`** like the other siblings; future `epubsource` etc.

**Verified:** big-book test asserts 559 sections / 4 levels / ≥1 figure(Page>0) /
~1.38M chars prose; fixture tests over toc/image/hello; `CGO_ENABLED=0 go build`
exits 0.

**Gotchas:**
- `model.ConfigPath = "disable"` in `init()` to kill pdfcpu's config-dir side
  effects/noise in sandboxes. Keep stdout clean — all diagnostics via `slog`
  (stderr).
- `ledpdf.Open` is stricter than pdfcpu about the header: it rejects `%PDF-1.3 `
  (trailing space) in the `image.pdf` fixture. Text extraction MUST be best-effort.
- `GetPlainText` can panic on malformed content — guard each page with `recover()`,
  convert a bad page to empty prose, continue.
- `model.Image` embeds `io.Reader`; read bytes with `io.ReadAll(img)` INSIDE the
  digest callback (invalid after). `Width/Height` may be 0×0 — ignored here.
- Store RAW ledongthuc text. Do NOT add a de-glue/ligature pass — repair is the
  S1-ab-6 LLM prepare stage's job.

---

## 2026-08-22 — Format-agnostic ingestion seam: page→tree translation (S1-ab-2)

**Task:** S1-ab-2 · **Area:** infra (new package `internal/ingest`)

**Decision:** Introduced the format-agnostic document model (`Source → Document{Sections}`)
and section-tree-based chapterization, replacing audi's page-centric TOC model. The
`internal/ingest` package defines the model (`BlockKind`, `Block`, `Section`, `Meta`,
`Document`) and the `Source` seam interface that format adapters (PDF/ePub/md/html in
later tasks) will implement.

Key design choices:
1. **Page→tree translation:** Sections form a tree (`Section.Children`); top-level roots
   are `doc.Sections`. No `StartPage`/`EndPage`, no page ranges. Chapters are derived by
   walking the tree, not by slicing a flat TOC.
2. **Intro-shim for expansion:** A section with ≥2 children expands into one chapter per
   child. The parent's own intro `Blocks` (prose before subsections) are absorbed by the
   first child via a synthetic intro-shim `&Section{Title:"", Level: sec.Level, Blocks:
   sec.Blocks}` prepended to `Chapter.Sections`. Keeps the `Chapter` type uniform (always
   `[]*Section`), no double-counting.
3. **`Source.Load(path string) (*Document, error)`:** Context-free to match audi's
   `Extract(path)` idiom; ingestion is local I/O. Adapters land in S1-ab-3 (PDF),
   S1-ab-4 (ePub), S1-ab-12 (md/html).
4. **Boilerplate filtering:** Ported audi's `defaultSkipTitles` verbatim (cover/copyright/
   table of contents/contents/index/colophon); matched case-insensitively at prefix or
   exact. Applied at top level only. Preface/Foreword/Introduction are real content and
   are NOT skipped.
5. **Heading detection deferred:** The old `chaptersFromHeadings` text heuristic is NOT
   ported. For headless formats (plain markdown/text/HTML-articles), the adapter (S1-ab-12)
   builds the tree by detecting headings; chapterization only consumes an already-built tree.

**Gotchas:**
- `internal/ingest` has ZERO external deps — stdlib only (`strings`, `reflect`/`testing`).
  No genai, no pdfcpu, no ledongthuc. Keeps it off CRITICAL and trivially unit-testable.
- Do not reintroduce `minLevel`, page dedup, clamp, or `numPages` — they are page-model
  artifacts with no meaning in the tree.
- The intro-shim carries ONLY the parent's own `Blocks`, not its `Children` (would
  double-count). When the pipeline later flattens `chapter.Sections`, the intro appears
  exactly once ahead of the first expanded child.

---

## 2026-08-22 — Reusable `internal/gemini` client + `GenerateSpeech` upgrade (S1-ab-1)

**Task:** S1-ab-1 · **Area:** infra (SENSITIVE — touches `internal/gemini`)

**Decision:** Introduced a long-lived `gemini.Client` (genai models seam + project/region)
with `Generate`/`GenerateImage`/`GenerateSpeech` **methods**; the existing package-level
funcs are now thin wrappers that construct a client and delegate, so `cmd/jeep`,
`cmd/jeep-image`, and `cmd/jeep-research` are byte-for-byte unchanged at the call site.
The genai boundary is now an injectable `contentGenerator` interface (matching
`Models.GenerateContent`'s value-receiver signature), which finally makes the "fake client
returns canned responses" seam in `design/seams.md` real — retry/400-classification/request
building are now unit-tested without a network.

`GenerateSpeech` gained three things the audiobook pipeline needs:
1. **`style` param** — genai TTS has no style field, so a non-empty style is carried as the
   leading text of the prompt (`style + "\n\n" + prompt`); empty style leaves the prompt
   exactly unchanged. Its one caller (`cmd/jeep-tts`) passes `""` — behavior unchanged.
2. **`IsInvalidInput(err)`** — exported classifier for Vertex `APIError{Code:400}` (unwraps
   through `%w`). Mirrors audi's `IsInvalidArgument`; the S1-ab-8 pipeline uses it as the
   adaptive re-split trigger. 400 is NOT in `isRetryable` (429/500/503 only), so it
   propagates on the first attempt intact.
3. **`PCMToMP3(ctx, pcm, sampleRate, channels)`** — ffmpeg (`libmp3lame -q:a 4`) over
   stdin/stdout, encoding the raw `audio/L16` 24kHz mono PCM genai returns.

**Gotchas:**
- `Models.GenerateContent` has a **value** receiver, `client.Models` is `*Models`; both
  satisfy the interface — store `gc.Models` (the pointer) directly, don't dereference.
- The 503-retry unit test costs ~1.3s wall clock (retry base is 1s); kept retry counts small.
- ffmpeg/ffprobe tests `t.Skip` when the binaries are absent so the suite stays green
  off-environment; they are present here (`/usr/bin/ffmpeg` 6.1.1).
- jeep-tts still writes WAV via its own `pcmToWAV`; MP3 consumption is deferred to the
  audiobook pipeline (out of scope for this task).

---

## 2026-08-22 — Fold `audi` (PDF→audiobook) into jeep as `jeep-audiobook`

**Task:** S1-ab-* (planning) · **Area:** infra/feature

**Decision:** Port the `audi` CLI (currently at `/home/boris/learning/audi`, module
`github.com/tvaroska/learning/audi`) into jeep as a 5th tool, `cmd/jeep-audiobook`,
maximizing reuse of jeep's `internal/`. audi is archived after the port; jeep is the
single source of truth. Locked decisions (interview 2026-08-22, full write-up in the
audi repo `docs/CONSOLIDATION_PLAN.md`):

1. **Freely refactor** `internal/gemini` + `internal/gcp` for one shared path.
2. **TTS = jeep genai `GenerateSpeech`** — drop Cloud TTS entirely.
3. **describe/prepare LLM calls route through `gemini.Generate` + structured JSON schemas.**
4. **Add a reusable long-lived client** in `internal/gemini`, shared by all tools.
5. **PDF extraction = pure-Go (CGO-free, permissive).** See spike below.
6. **Archive audi** after merge.
7. Full gen-3 workflow (this repo).
8. **`--mode full|summary|abstract`** on `gemini.Generate`.
9. **Format-agnostic ingestion seam:** `Source → Document{Sections}` — a section tree,
   NOT the old page-centric model. Chapters come from source structure.
10. Formats: PDF + ePub now; Markdown/plain-text + HTML/web-articles as adapters. Not DOCX.
11. **Generic map-reduce helper** (`chunk→map→reduce[+refine]`) on `gemini.Generate` for
    over-window summary/abstract/prepare; refit `jeep-research` synthesis onto it.

**Phase-0 spike results (verified on the real 633-page book, artifacts in `/tmp/pdfspike/`):**
- **Outline — win:** `pdfcpu api.Bookmarks()` → 559 entries, 4 levels, correct titles+pages.
- **Images — win:** `pdfcpu api.ExtractImages()` → PNGs with page association via `img.PageNr`.
- **Text — resolved:** `ledongthuc/pdf` (`GetPlainText`) is the only usable text extractor
  (pdfcpu = raw content streams; dslipak hangs). Raw output has line-wrap word-gluing +
  dropped heading ligatures — but the **Gemini "prepare for listening" stage repairs them**
  (validated: `unied`→`The unified`, `ree pillars`→`The three pillars`, glued words + sentence
  boundaries all fixed, no hallucination). So the prepare prompt MUST include artifact-repair
  guidance.
- **TTS parity — no blockers:** `GenerateSpeech` needs (a) a style-prompt prefix (no style
  param today), (b) a PCM→MP3 encode (returns raw `audio/L16`), (c) re-split trigger on
  `genai.APIError{Code:400}` (retry.Do only retries 429/500/503, so a 400 propagates intact).

**Gotchas for implementers:**
- **Recommended pure-Go stack: pdfcpu (outline+images) + ledongthuc/pdf (text).** Do NOT
  reach for MuPDF/go-fitz (CGO) or unidoc (AGPL). A geometry-based de-glue over
  ledongthuc `GetTextByRow` was tried and FAILED (it buckets wrapped lines) — rely on the
  prepare-stage LLM repair, not deterministic de-gluing.
- pdfcpu `model.Image.Width/Height` can report 0×0 for some PNG codec paths; decode the
  emitted bytes if you need dimensions.
- Source audi code to port lives at `/home/boris/learning/audi/internal/{pdfx,describe,tts,pipeline,usage,log}`
  and `cmd/audi/main.go`. Rewrite every import `github.com/tvaroska/learning/audi/...` →
  `github.com/tvaroska/jeep/...`. Do NOT copy audi's cobra/viper CLI — use jeep `internal/cli` + `internal/config`.
- Runtime deps: `ffmpeg` + `ffprobe` on PATH (concat, loudness normalize, PCM→MP3, ID3 art).
- Loudness: per-segment EBU R128 (`loudnorm=I=-16:TP=-1.5:LRA=11`, mono) applied at concat
  time — already shipped in audi `internal/tts/concat.go`; port it.

---

## 2026-08-22 — Project initialized (gen-3 workflow)

**Task:** setup · **Area:** infra

**Decision:** Adopted the gen-3 planning workflow (Option-A layout: `spec/` +
`design/` at root, `docs/` for planning/ops, `TODO.md` for live status).

**Rationale:** Single file convention, verification ladder, committed decision log.

**Gotchas:** None yet.
