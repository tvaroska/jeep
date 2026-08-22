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

## 2026-08-22 — audi "archive" resolved: no standalone repo; deprecated in monorepo (S0-infra-1)

**Task:** S0-infra-1 · **Area:** ops/docs (external repo deprecation + bookkeeping; NO jeep code changes)

**Finding:** The task premise "archive the audi GitHub repo (`github.com/tvaroska/learning/audi`)" was false. Verified 2026-08-22 with `gh` (authenticated as `tvaroska`):
- **No standalone `tvaroska/audi` repository exists** (`gh repo view tvaroska/audi` → GraphQL error: "Could not resolve to a Repository").
- `audi` is a **plain subdirectory** of the private monorepo `tvaroska/learning` (`git -C /home/boris/learning/audi rev-parse --show-toplevel` → `/home/boris/learning`; no `audi/.git`).
- `tvaroska/learning` is a **PRIVATE monorepo** containing ~30 unrelated projects (agents, coffee, loans, robotics, vertex, insurance, k8s, ma, ...).
- The literal task "archive the audi repo" is **not executable as written** because the object it names does not exist as a repo.

**Decision:** Reject the catastrophically wrong `gh repo archive tvaroska/learning` (would flip the ENTIRE monorepo to read-only, affecting 30 unrelated projects). Instead, **retire `audi` in-place** by adding a deprecation banner to `/home/boris/learning/audi/README.md` and `PLAN.md` pointing readers to jeep `cmd/jeep-audiobook`, committed surgically in the learning repo.

**Executed:** Lane A (external deprecation, human-gated): Added deprecation banners to `audi/README.md` and `audi/PLAN.md` stating the project is archived/deprecated (2026-08-22) and ported to jeep as `jeep-audiobook`, supporting PDF, ePub, Markdown/plain-text, and HTML/web-article inputs. Staged ONLY `audi/README.md` and `audi/PLAN.md` explicitly (not `git add -A`), avoiding the dirty files (`ma/pyproject.toml`, `ma/uv.lock`, `robotics/`, `audi/.claude/plans/*.md`, `ma/Environments.ipynb`). Committed as "audi: mark deprecated — ported to jeep (jeep-audiobook)" (commit `6d6ae58` in `tvaroska/learning`). **Push deferred pending human sign-off** — modifying the shared private monorepo is a high-blast-radius action; the implementer must NOT push autonomously (per plan + DECISIONS.md S1-ab-13 guidance).

Lane B (jeep bookkeeping, always done): Appended this DECISIONS.md entry recording the finding, the rejected whole-repo archive, the chosen resolution (deprecation banner), the surgical-staging gotcha, and the human-gate status. TODO.md flipped to `- [!]` (deprecation prepared locally, awaiting human push).

**Rationale:** The deprecation notice is the faithful, non-destructive realization of the task intent ("mark audi archived; development continues in jeep"). It preserves history, is reversible, and carries zero blast radius for the 30 unrelated projects sharing the monorepo. A deletion (`git rm audi/`) was explicitly out of scope unless the human asks. If the human later wants the whole `learning` repo archived, that is their call to make in the GitHub UI — this task does not execute it.

**Gotchas:**
- **There is no `tvaroska/audi` repo.** The module path `github.com/tvaroska/learning/audi` is a Go module convention pointing at a subdirectory, NOT a GitHub repo URL. `gh repo view tvaroska/learning/audi` errors (not a valid repo).
- **`tvaroska/learning` is shared and dirty.** The working tree has unrelated modified/untracked paths (`ma/`, `robotics/`, `audi/.claude/plans/*.md`) that MUST NOT be swept into the commit. Always stage paths explicitly (`git -C /home/boris/learning add audi/README.md audi/PLAN.md`), NEVER `git add -A` / `git add .` there.
- **Push is human-gated.** Modifying and pushing to the private `tvaroska/learning` monorepo is a high-blast-radius action. The implementer prepared the commit locally and STOPPED; a human must review and approve the push. The task closes when the finding is recorded (Lane B), regardless of whether Lane A's push happens now or later.
- **This is NOT a jeep CRITICAL path.** The task touches only external (`tvaroska/learning`) and jeep docs (DECISIONS.md, TODO.md); no files under jeep's CRITICAL.md are modified.

---

## 2026-08-22 — Documentation complete: jeep-audiobook is live, audi archive proposed (S1-ab-13)

**Task:** S1-ab-13 · **Area:** docs/ops (pure documentation + backlog note; NO production code changes)

**Decision:** With all twelve technical tasks (S1-ab-1 through S1-ab-12) complete, this final task makes `jeep-audiobook` discoverable and formally proposes archiving the source `audi` repository. Delivered README updates (tool table, install line, ffmpeg prereq, model-reuse note, and a full `## jeep-audiobook` section with flag table + examples), a completed-work archive entry in `docs/features/ab.md`, and a Sprint 0 task proposing the GitHub archive of `github.com/tvaroska/learning/audi` (the actual repo-admin action is manual, not executed here).

**Rationale:** Flag documentation in the README is cross-checked against `--help` output (the T2 acceptance grep) to prevent drift as the tool evolves. The README explicitly notes that `-o/--output` is a DIRECTORY (not a file like `jeep-tts`), that `--from-page`/`--to-page` are PDF-only, and that the tool requires `ffmpeg`/`ffprobe` on PATH. These are the load-bearing gotchas from prior tasks (DECISIONS.md S1-ab-9, S1-ab-8, S1-ab-1). The audi archive is PROPOSED (Sprint 0 task `S0-infra-1`) rather than executed — GitHub repo archival is a manual repo-admin step, not scripted.

**Verified:** T1 ladder (`go build ./... && go vet ./... && go test ./...` all green; `git diff --name-only -- internal cmd spec .github .goreleaser.yml CRITICAL.md` prints nothing, proving no code/CRITICAL change) and T2 acceptance (flag-parity between README and `--help`; `go build ./cmd/jeep-audiobook` succeeds; offline dry-run examples run; README grep checks pass; archive entry exists; Sprint 0 proposal filed with counter bump `infra=0`→`infra=1`; S1-ab-13 flipped to `- [x]` with done-suffix). No new dependencies, no CRITICAL paths touched. The port from audi to jeep as `cmd/jeep-audiobook` is complete.

**Gotchas:**
- **Flag-parity is the anti-drift guard.** The T2 check greps `--help` for every flag the README documents and vice-versa. If you add/change/remove a flag in `cmd/jeep-audiobook/main.go`, update the README flag table to match — or the next docs verification will fail.
- **audi archive is PROPOSED, not executed.** Do NOT run `gh repo archive` or edit `/home/boris/learning/audi`. The Sprint 0 task is the proposal; a human performs the GitHub action.
- **README section mirrors existing tools.** The `## jeep-audiobook` section follows the same template as `## jeep-research` (description, synopsis, supported-formats line, flags table, examples). Common flags (project/region/retry/timeout/quiet/dry-run/version/format) stay in the shared table; only tool-specific flags are listed.
- **The `-o` is-a-directory note is mandatory.** Users coming from `jeep-tts` (where `-o output.wav` is a file) will expect `-o` to be a file. The README must state "Output DIRECTORY (not a file)" to prevent confusion.
- **Runnable examples are load-bearing.** The dry-run examples (`internal/mdsource/testdata/article.md`, `internal/htmlsource/testdata/article.html`) are real files that exist in the repo and run offline without GCP credentials — acceptance depends on them.

---

## 2026-08-22 — Markdown/plain-text + HTML/web-article `Source` adapters (S1-ab-12)

**Task:** S1-ab-12 · **Area:** feature (new packages `internal/mdsource`, `internal/htmlsource`; shared helper in `internal/ingest`)

**Decision:** Added two new format adapters behind the existing `ingest.Source` seam so `jeep-audiobook` can ingest Markdown/plain-text and HTML/web-article inputs, producing the same Section tree that PDF and ePub already emit. Both adapters are pure-Go (stdlib + `golang.org/x/net/html` for HTML, which was already a direct dependency), and neither touches `internal/gemini`, the pipeline, or any CRITICAL path.

Key design choices:
1. **Two packages, not one:** `internal/mdsource` and `internal/htmlsource` match the existing pdfsource/epubsource sibling convention, keeping format-specific parsing isolated.
2. **Shared nesting helper in `internal/ingest`:** Both adapters reduce their format to a flat `[]HeadingItem` (level + title + blocks) and call a new `ingest.SectionsFromHeadings` function that builds the nested tree via a level-stack algorithm. One tested implementation beats two copies, and it's pure stdlib so `ingest` stays dependency-free.
3. **Markdown parser = pure stdlib line scanner:** ATX headings (`^#{1,6}\s+...`), fenced code (` ``` `/`~~~` toggle), and prose runs. No goldmark or markdown library needed. Setext headings (`===`/`---` underlines) are out of scope for v1.
4. **HTML parser = readability-style main-content selection:** Find first `<article>`, else `<main>`, else `<body>`; skip entire subtrees of `<script>`, `<style>`, `<nav>`, `<header>`, `<footer>`, `<aside>`, `<form>`, and `role="navigation"`. This makes the `<body>` fallback usable on real web pages.
5. **No images:** Markdown images are remote URLs (no local bytes); HTML `<img>` require network fetch or data-URI decoding (deferred). Narration is prose + code.

**Verified:** Unit tests cover section tree building (headings, nesting, preamble, code blocks), boilerplate stripping (HTML nav/header/footer text absent), and edge cases (no-heading input → one Level-1 section); CLI dry-run proves both adapters are reachable via the CLI (`.md`/`.html` inputs print planned chapters offline); full suite green; `CGO_ENABLED=0 go build ./...` exits 0; `go mod tidy` adds NO new dependency (`golang.org/x/net` promoted from indirect to direct, already in go.sum); `git diff --stat` shows no CRITICAL paths touched.

**Gotchas:**
- **The sibling test break was mandatory:** `cmd/jeep-audiobook/dryrun_test.go:71` used `foo.txt` as its "unsupported" example. Once `.txt` mapped to `mdsource`, the test broke (file-not-found, not "unsupported"). Changed it to `foo.docx` (genuinely unsupported) to keep T1 green.
- **ATX only for v1:** Setext headings (`===`/`---` underlines) are NOT parsed. Documented in the mdsource package comment as a known limitation.
- **Boilerplate strip is load-bearing for HTML:** Without skipping `<nav>`/`<header>`/`<footer>`, the `<body>` fallback would ingest entire menus/site chrome.
- **Whitespace collapse in HTML is allowed:** Layout whitespace (runs of `\n`/`\t`/` ` → single space via `strings.Fields`) is part of the format, not content. Word-level rewriting is still forbidden (DECISIONS.md S1-ab-3/4/6).
- **Preamble handling is in the adapter, not the helper:** When a document has content before its first heading (or no headings at all), the adapter prepends a `HeadingItem{Level:1, Title:"", Blocks: <preamble>}` so the helper sees a well-formed input.

---

## 2026-08-22 — Chapter cover art: `internal/art` seam + ID3 APIC embed + PNG sidecar (S1-ab-11)

**Task:** S1-ab-11 · **Area:** feature (new packages `internal/art` + `internal/audio` embed seam)

**Decision:** Added `--art generate|extract|book-cover|none` (default `none`, so
existing behavior/cost/tests are unchanged). A new `internal/art` package produces
a per-chapter cover `Image` via `Provider.ForChapter(ctx, title, summary,
figures)`; a new `audio.FFmpegArtEmbedder` embeds it into each `chapter_NN.mp3` as
an ID3v2 APIC frame (one ffmpeg mux); the pipeline writes a `chapter_NN.png`
sidecar and records an `image` field in each manifest chapter entry.

`internal/art` reaches the shared gemini client only through a package-local
`imageClient` interface that `*gemini.Client` satisfies structurally (mirrors
`describe.generator` / `audio.speechClient`) — `internal/gemini` untouched. The
embed seam co-locates with `FFmpegConcatenator` and reuses its exec idiom.
`Provider` mirrors `describe.GeminiPreparer` (concrete struct, injected client,
nil-safe logger, no factory); book-cover mode memoizes one image via `sync.Once`.

**Gotchas:**
- **Never fail a run on art.** Every art step (generate, cache, sidecar, embed) is
  Warn + continue; only audio-path errors stay fatal. The audio is the product.
- **`-disposition:v attached_pic` is required** for ffprobe to report the image as
  an attached picture (the acceptance signal). ffmpeg cannot rewrite in place — mux
  to a temp file in the SAME dir, then `os.Rename` over the mp3 (atomic same-dir).
- **Embed BEFORE the completion record** so a resume that hits the chapter
  short-circuit can trust the mp3 already carries the APIC frame. The resume path
  restores `ChapterEntry.Image` from the record only when the sidecar still exists
  on disk (mirror the mp3 stat guard).
- **Cache generated art** under an `"art"` namespace keyed on
  mode+model+title+summary (generation is non-deterministic AND paid — same
  reasoning as the narration cache). Cache errors are non-fatal.
- **Sidecar must be real PNG.** extract figures can be JPEG; decode via stdlib
  (`image` + blank `image/jpeg`/`image/png` decoders) and re-encode as PNG; fall
  back to raw bytes only if decode fails. Pure-Go — no ffmpeg for the sidecar.
- **One-line summary = deterministic narration prefix** (~200 chars, word-boundary
  cut) — avoids an extra LLM call; documented v1 proxy for "a one-line summary".
- **Validate `--art` BEFORE the dry-run branch** so `--dry-run --art bogus` errors
  offline; dry-run never builds a client or resolves art.
- **Book title = input filename stem** (not `ingest.Meta.Title`) to avoid a second
  document load in the CLI; `ingest.Meta.Title` is available if richer context is
  wanted later.

---

## 2026-08-22 — `--mode full|summary|abstract` via map-reduce condenser (S1-ab-10)

**Task:** S1-ab-10 · **Area:** feature (new flag + `describe.Summarizer`)

**Decision:** Added a `--mode full|summary|abstract` flag to `cmd/jeep-audiobook`.
`full` (default) keeps today's behavior exactly (full narration via
`describe.GeminiPreparer`). `summary` and `abstract` produce a short-form audiobook:
each chapter's narration is condensed via the generic map-reduce helper
(`internal/mapreduce`) — chunk the (already-woven) chapter text, map each chunk to
a partial summary, reduce the partials into a single listening-ready narration.

Key design: swap the `Preparer` implementation, don't touch the pipeline. The
pipeline runs `Weave → Preparer.Prepare(text) → segment → synthesize → concat`.
Introduced a new `describe.Summarizer` type that ALSO implements `describe.Preparer`,
so `main.go` injects either `GeminiPreparer` (full) or `Summarizer` (summary/abstract)
based on the flag, and `internal/pipeline` needs ZERO changes.

Extended `mapreduce.Step` to accept a trailing `*usage.Tracker` param (nil-safe);
after a successful `Generate`, it calls `tracker.Record(...)`. This lets
summary/abstract runs report tokens like `full` does. Updated all existing
`Step` call sites in tests to pass a trailing `nil`.

**Gotchas:**
- **Do NOT touch `internal/gemini`.** The package-local `generator` interface
  (identical single-method `Generate(...)` in describe and mapreduce) is mandatory.
- **Validate `--mode` BEFORE the dry-run branch** so `--dry-run --mode bogus`
  errors clearly (acceptance #1 uses dry-run to stay offline).
- **One canned JSON serves both map and reduce fakes** — `{"summary":..,"narration":..}` —
  because `encoding/json` ignores fields absent from the target struct.
- **Map-reduce always runs** even for a single chunk (1 map + 1 reduce); that's
  intended and uniform. Don't add a single-chunk bypass.
- **Cache correctness caveat:** The narration cache key
  (`narrationCacheKey(GeminiModel, pageContents)`) does NOT include the mode. A
  resume that CHANGES mode over the same output dir could serve a stale full
  narration for a now-summary run. In practice modes go to different output dirs,
  and `--resume` is opt-in, so this is out of scope for this task. Known limitation
  flagged for future consideration.

---

## 2026-08-22 — CLI integration: `cmd/jeep-audiobook` on shared plumbing (S1-ab-9)

**Task:** S1-ab-9 · **Area:** feature (new `cmd/jeep-audiobook`)

**Decision:** Added the fifth jeep CLI, `cmd/jeep-audiobook`, wiring the finished
audiobook pipeline onto jeep's shared `internal/cli` + `internal/config` plumbing
(matching the existing four tools: `jeep`, `jeep-image`, `jeep-tts`, `jeep-research`).
Mirrors `cmd/jeep-tts/main.go` closely — same `flag.FlagSet` + `cli.CommonFlags` +
`cli.RunMain` idiom, not audi's cobra/viper CLI.

Supports PDF/ePub input, chapter selection by 1-based index (`--chapters 1,3-5`) or
page window (`--from-page N --to-page M`, resolved to chapter indices in the CLI layer
using each chapter's `[StartPage,EndPage]` span), resumable runs (`--resume` enables
`DiskCache` under `<output>/.cache`), voice/style customization, and dry-run mode
(prints planned chapters WITHOUT building a `*gemini.Client` or making API calls).

**Rationale:** Page-window → chapter-index resolution in the CLI layer (replicated
`chapterPageSpan` DFS, ~15 lines) avoids reintroducing page fields into
`pipeline.Options` (meaningless over a Section tree; ePub/markdown/HTML have no pages).
The alternative — exporting `pipeline.chapterPageSpan` — is allowed (non-critical) but
beyond "wire the CLI" scope; prefer the local copy to minimize cross-package coupling.
CLI-side mapping uses each chapter's page span (already computed for the manifest) to
filter, then passes a simple `[]int` to the pipeline.

**Gotchas:**
- **Do NOT copy audi's cobra/viper CLI.** Use `internal/cli` + `internal/config` only.
- **`--output` is a DIRECTORY**, not a file (unlike `jeep-tts` `-o output.wav`). The
  pipeline writes `chapter_NN.mp3` + `manifest.json` into it; the cache lives at
  `<output>/.cache` when `--resume`.
- **Order matters in `run`:** parse → `ResolveCommon` → (dry-run branch returns before
  any client) → real branch builds client. Never call `gemini.NewClient` in the
  dry-run path.
- **`TTSFingerprint` must be `"model|voice|style"`** (DECISIONS.md S1-ab-8) so the
  segment/chapter cache keys change when voice/style/model change — otherwise a resume
  would replay stale audio for new settings.
- **`GeminiModel` = the TEXT model** (identifies Weave/Prepare output for the narration
  cache key), NOT the TTS model.
- **Concurrency=0 is correct as a default** — the pipeline and describe adapters treat
  `<=0` as "use 4". Do not pass a negative.
- **ePub + page flags is an error**, not a silent no-op (page-less source).
- Keep **stdout clean**: all diagnostics/progress go to stderr via `slog`; only the
  dry-run listing and the final success/JSON summary go to stdout (so `--format json`
  output stays parseable, matching the other tools).
- **Dry-run is OFFLINE** (bogus project still succeeds; model resolution is pure).

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
