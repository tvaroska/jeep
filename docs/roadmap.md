# jeep — Roadmap

**Last Updated:** 2026-08-22

Strategic priorities by feature area and the release plan. Planning doc (`docs/`).
Roadmap is intent, not live status — per-task status/counts live in `TODO.md`.
Map releases to the CUJs (`spec/cujs.md`) they deliver.

---

## Feature Areas

### infra

**Priority:** P0
**Delivers CUJs:** (foundation for all)

Goals:
- Keep CI, release automation, and the verification ladder green.

### text (jeep)

**Priority:** P0
**Delivers CUJs:** CUJ-1

Goals:
- Robust single-shot text prompting with clean stdout + retry.

### image (jeep-image)

**Priority:** P1
**Delivers CUJs:** CUJ-2

Goals:
- Reliable image generation to file; no partial artifacts.

### tts (jeep-tts)

**Priority:** P1
**Delivers CUJs:** CUJ-3

Goals:
- Reliable speech synthesis to file.

---

## Release Plan

### v1.0 — MVP

- Three working CLIs (text, image, tts) over Vertex AI.
- Delivers: CUJ-1, CUJ-2, CUJ-3
- Verification: T3 CUJ suite green via `/verify`

### vNext

- TBD (additional Gemini capabilities as new `cmd/jeep-*` binaries).

---

## CUJ → Release map

| CUJ | Release / Sprint | Notes |
|-----|------------------|-------|
| CUJ-1 | v1.0 / S1 | text prompt |
| CUJ-2 | v1.0 / S1 | image generation |
| CUJ-3 | v1.0 / S1 | text-to-speech |

---

**Updated by:** /replan
