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
