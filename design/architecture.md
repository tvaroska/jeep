# jeep — Architecture (as built)

THE HOW. Describes the system as it is actually built. Part of `design/`.
Keep status/counts OUT (F6) — live status lives in `TODO.md`. When the build
diverges from `spec/`, update this doc and raise the gap in `spec/open-questions.md`.

**Stack:** Go, `google.golang.org/genai` SDK (Vertex AI backend)

---

## System overview

jeep is a set of single-shot CLI tools that wrap the Gemini API (via Vertex AI)
for use by coding agents. Each CLI is a thin entry point over a shared client and
config layer.

```
cmd/jeep       ─┐
cmd/jeep-image ─┼─▶ internal/cli ─▶ internal/config ─▶ internal/gemini ─▶ Vertex AI
cmd/jeep-tts   ─┘                         │
                                   internal/gcp (project detection)
```

## Components

### CLI entry points (`cmd/jeep`, `cmd/jeep-image`, `cmd/jeep-tts`)

**Responsibility:** parse args, invoke the shared client, print result / write artifact, set exit code.
**Location:** `cmd/*`
**Dependencies:** `internal/cli`, `internal/config`, `internal/gemini`
**Implements CUJs:** CUJ-1 (jeep), CUJ-2 (jeep-image), CUJ-3 (jeep-tts)

### Gemini client (`internal/gemini`)

**Responsibility:** construct the genai/Vertex client, send requests, return full responses; retry logic.
**Location:** `internal/gemini`
**Dependencies:** `google.golang.org/genai`, `internal/gcp`
**Implements CUJs:** CUJ-1, CUJ-2, CUJ-3

### CLI parsing (`internal/cli`)

**Responsibility:** shared flag parsing and run/dry-run helpers across the three commands.
**Location:** `internal/cli`

### Config / model resolution (`internal/config`)

**Responsibility:** resolve model + region defaults (region defaults to `global`).
**Location:** `internal/config`

### GCP project detection (`internal/gcp`)

**Responsibility:** auto-detect the GCP project from env or gcloud config. Sensitive (see `CRITICAL.md`).
**Location:** `internal/gcp`

## Data flow

Invocation → CLI parses flags → config resolves model/region → gcp resolves
project → gemini builds request and calls Vertex AI → full response returned →
CLI prints to stdout or writes the artifact → exit code set.

## Deployment topology

Distributed as standalone binaries (goreleaser). No server; each run is a
short-lived process making one outbound call to Vertex AI.

## Constraints

- Single-shot only: no streaming, no interactive sessions.
- Requires resolvable GCP credentials + project.
- stdout must stay clean (result payload only) for agent consumption.

## Cross-references

- Boundaries & test seams: `design/seams.md`
- Reusable patterns: `design/patterns.md`
- Decision records: `design/decisions/`
