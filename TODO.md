# jeep — TODO & Live Status

**Last Updated:** 2026-08-22
**Current Focus:** Sprint 1 — `jeep-audiobook` port (S1-ab-*)
**Cadence:** 2-week sprints

> This file is the ONLY place live status and counts live. Keep status/counts
> OUT of `spec/` and `design/` — those stay status-free.
> See [docs/roadmap.md](docs/roadmap.md) for strategic priorities.
> See [docs/features/](docs/features/) for the completed-work archive.
> See [DECISIONS.md](DECISIONS.md) for the decision/learning log.
> See [CRITICAL.md](CRITICAL.md) for protected/sensitive paths (auto-escalate).

<!-- Counters: sec=0 bug=0 infra=0 perf=0 debt=0 rel=0 -->

**Checkbox states:** `- [ ]` open · `- [x]` done · `- [!]` attempted-but-failed
(awaiting follow-up). `/open` and `/status` surface `- [!]`; `/implement` may
pick one up to retry.

---

## Sprint 0: Critical Issues (always-active triage lane)

No open issues. Add with `/new-task` (categories: sec, bug, infra, perf, debt, rel).

<!-- Example rows:
- [ ] S0-sec-1 (P0) Rotate leaked API key and add secret scanning
- [!] S0-bug-2 (P1) Fix race in upload finalize — retry failed, see DECISIONS.md
-->

---

## Sprint 1: `jeep-audiobook` — port `audi` into jeep

Feature: fold the PDF/ePub→audiobook CLI into jeep as `cmd/jeep-audiobook`, maximizing
reuse of `internal/{gemini,cli,config,gcp,retry}`. Locked decisions + phase-0 spike
results are in **DECISIONS.md (2026-08-22)** — READ IT FIRST. Source to port from:
`/home/boris/learning/audi` (different repo; rewrite imports to `github.com/tvaroska/jeep/...`).
Strictly sequential — respect the `Deps:` on each task; `/implement-all` re-reads this file
between tasks so dependents unblock as their prerequisites flip to `- [x]`.

`S1-ab-0` (phase-0 spike: pure-Go PDF fidelity + genai-TTS parity) is **done** — see DECISIONS.md.

- [x] S1-ab-1 Reusable `internal/gemini` client + upgrade `GenerateSpeech` (SENSITIVE: touches `internal/gemini`, auto-escalates) _(done 2026-08-22; reviewed; see docs/features/*)_
  - Deps: none.
  - Scope: add a constructed long-lived `Client` (genai client + project/region) with `Generate`/`GenerateImage`/`GenerateSpeech` methods; keep package funcs as thin wrappers. Add to `GenerateSpeech`: (a) style-prompt prefix param, (b) PCM→MP3 encode (ffmpeg), (c) expose the oversized-input error so callers can classify `genai.APIError{Code:400}`.
  - Accept: `go build ./... && go vet ./... && go test ./...` green; existing jeep/jeep-image/jeep-research/jeep-tts still build+test green; unit test proves PCM→MP3 output is a valid MP3 (ffprobe positive duration), the style prefix reaches the request (fake client), and a 400 surfaces as a classifiable error while 429/500/503 still retry.

- [x] S1-ab-2 Format-agnostic ingestion seam: `Source → Document{Sections}` + section-based chapterization _(done 2026-08-22; see docs/features/*)_
  - Deps: none.
  - Scope: define `Section` (title, level, ordered content blocks: prose/figure/caption/code/table, children) and `Document{Meta, Sections}`. Port audi's 30–45-min chapter grouping + boilerplate-skip (`internal/pdfx/structure.go`) to walk the section tree instead of TOC+pages.
  - Accept: unit tests over a synthetic section tree reproduce audi's `structure_test.go` expectations (chapter expansion when ≥2 children; boilerplate titles — cover/copyright/toc/index/colophon — dropped); `go test ./...` green.

- [x] S1-ab-3 PDF `Source` on the pure-Go stack (pdfcpu outline+images, ledongthuc text) _(done 2026-08-22; see docs/features/*)_
  - Deps: S1-ab-2.
  - Scope: implement the `Source` seam for PDF — `pdfcpu api.Bookmarks()` → sections, `pdfcpu api.ExtractImages()` → figure blocks with page association, `ledongthuc GetPlainText` → prose. CGO-free (`CGO_ENABLED=0` must build). No MuPDF, no unidoc.
  - Accept: a test/example over `/home/boris/learning/audi/testdata/observability-engineering.pdf` yields 559 outline entries at 4 levels, extracts images with a page number, and returns non-empty prose per section; `CGO_ENABLED=0 go build ./...` succeeds.

- [x] S1-ab-4 ePub `Source` (pure-Go: OPF spine + nav/NCX → sections + images) _(done 2026-08-22; see docs/features/*)_
  - Deps: S1-ab-2.
  - Scope: unzip → parse OPF spine + `nav`/NCX TOC → XHTML per chapter → `Section` tree; extract embedded images as figure blocks.
  - Accept: a unit test parses a small fixture `.epub` into a section tree with correct titles, per-section content, and at least one extracted image; `go test ./...` green.

- [x] S1-ab-5 Port `usage` (genai token metadata) + `log` _(done 2026-08-22; see docs/features/*)_
  - Deps: none.
  - Scope: move audi `internal/usage` + `internal/log`; usage is token-based (one currency) off genai metadata.
  - Accept: unit test shows the tracker accumulates prompt/output/total tokens across calls; `go build ./... && go test ./...` green.

- [x] S1-ab-6 Port `describe` onto `gemini.Generate` + structured JSON _(done 2026-08-22; see docs/features/*)_
  - Deps: S1-ab-1.
  - Scope: move the audiobook prompts (figure description, inline weaving, listening adaptation); delete genai plumbing, call `gemini.Generate` with schemas. The prepare/"read for listening" system prompt MUST include artifact-repair guidance (re-join line-wrap-split words; restore dropped ligatures — leading `Th`/`fi`/dropped `The`) — see DECISIONS.md.
  - Accept: `describe` unit tests pass against a fake gemini client; a test asserts the prepare system prompt contains the artifact-repair instruction; `go test ./...` green.

- [ ] S1-ab-7 Generic map-reduce helper on `gemini.Generate`; refit `jeep-research` (SENSITIVE: touches `internal/gemini` + jeep-research)
  - Deps: S1-ab-1.
  - Scope: `chunk→map→reduce[+refine]` helper (generic over element/partial/result; caller supplies map/reduce prompts+schema+chunker; concurrency+retry via `errgroup`/`retry.Do`). Refit `jeep-research`'s hand-rolled synthesis onto it so there's one implementation.
  - Accept: helper unit test (chunk→map→reduce over a fake produces the expected reduced result); `jeep-research` still builds + tests green; `go test ./...` green.

- [ ] S1-ab-8 Port `pipeline` (section-driven; loudness normalize + re-split + 3-level cache + manifest)
  - Deps: S1-ab-1, S1-ab-2, S1-ab-3, S1-ab-5, S1-ab-6.
  - Scope: move audi `internal/pipeline` — resumable content-addressed cache (narration/segments/chapters), ffmpeg concat + per-segment EBU R128 loudnorm, adaptive re-split on the 400 classification, per-chapter manifest. Swap the Synthesizer to the shared `gemini` client; drive off `Sections` not pages.
  - Accept: an integration test on a tiny fixture (few short sections, fake gemini + real ffmpeg) produces a manifest + a normalized MP3 whose integrated loudness is within tolerance of -16 LUFS (ffprobe/loudnorm); a fake 400 triggers one re-split; re-run reuses cache; `go test ./...` green.

- [ ] S1-ab-9 `cmd/jeep-audiobook` on `internal/cli` + `internal/config`
  - Deps: S1-ab-8.
  - Scope: wire the CLI on jeep's pflag `internal/cli` (RunMain/CommonFlags/Exitf) + `internal/config`. Flag parity: voice, style, resume, chapters, from/to-page, concurrency, output.
  - Accept: `go run ./cmd/jeep-audiobook --help` lists the flags; a dry-run on the test PDF prints the planned chapters without calling the model; binary builds; `go test ./...` green.

- [ ] S1-ab-10 `--mode full|summary|abstract` via the map-reduce helper
  - Deps: S1-ab-7, S1-ab-9.
  - Scope: add the mode flag; summary/abstract use the map-reduce helper for over-window inputs.
  - Accept: `--mode abstract` on the test PDF (fake gemini) produces a short-form manifest; an invalid mode value errors clearly; `go test ./...` green.

- [ ] S1-ab-11 Chapter cover art via `gemini.GenerateImage` (ID3 embed + sidecar)
  - Deps: S1-ab-9.
  - Scope: `internal/art` seam → generate per-chapter art from title+summary; embed as ID3 APIC in each chapter MP3 (one ffmpeg mux) + write `out/chapter_NN.png` sidecar + `image` field in manifest. `--art generate|extract|book-cover|none`. See audi `docs/CHAPTER_IMAGES_PLAN.md`.
  - Accept: a test shows a chapter MP3 gains an embedded cover (ffprobe reports a video/attached_pic stream) and a sidecar PNG is written; `go test ./...` green.

- [ ] S1-ab-12 Markdown/plain-text + HTML/web-article `Source` adapters
  - Deps: S1-ab-2.
  - Scope: markdown/plain-text (`#`/`##` headings → section levels; fenced code → code blocks) and HTML/web-articles (readability-style main-content extraction → headings → sections; strip nav/boilerplate).
  - Accept: unit tests convert a markdown fixture and an HTML-article fixture into correct section trees; `go test ./...` green.

- [ ] S1-ab-13 Archive audi; update jeep README/docs
  - Deps: S1-ab-9 (and remaining features as landed).
  - Scope: document `jeep-audiobook` in README + `docs/features/`; note audi is archived (the GitHub archive itself is a manual step — PROPOSE it, don't attempt repo-admin).
  - Accept: README documents the new tool + supported formats; `docs/features/` has the archive entry; a Sprint 0 note proposes archiving the audi repo.

---

**Workflow:** /open | /implement | /new-task | /new-feature | /replan | /verify | /status
