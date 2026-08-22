# jeep — Contracts

The stable interfaces the system exposes or depends on: APIs, schemas, message
formats, CLI surfaces, file formats. Part of `spec/` — forkable, **status-free**,
protected. Changing a contract is a breaking change; propose via
`spec/open-questions.md` or a Sprint 0 task, don't edit silently during `/implement`.

---

## Contract: `jeep` CLI (text)

**Kind:** CLI
**Consumers:** coding agents (Claude Code, etc.), shell scripts
**Stability:** experimental

**Shape:**
```
jeep [flags] "<prompt>"
  -f <file>      read prompt from file
  -m <model>     override model (else config default)
  --format json  structured output (where supported)
```
Result is printed to stdout; errors to stderr; exit 0 on success, non-zero on failure.

**Invariants:**
- Single request/response; full response before exit; no streaming to stdout.
- stdout carries only the result payload (agent-consumable).

**Versioning / compatibility:**
- Flag removals/renames are breaking; introduce new flags additively.

---

## Contract: `jeep-image` CLI

**Kind:** CLI
**Consumers:** coding agents, scripts
**Stability:** experimental

**Shape:**
```
jeep-image [flags] "<prompt>" -o <out.png>
```

**Invariants:**
- Writes a single image artifact to `-o`; no partial file on failure.

---

## Contract: `jeep-tts` CLI

**Kind:** CLI
**Consumers:** coding agents, scripts
**Stability:** experimental

**Shape:**
```
jeep-tts [flags] "<text>" -o <out.wav>
```

**Invariants:**
- Writes a single audio artifact to `-o`; no partial file on failure.

---

## Contract: Vertex AI backend (dependency)

**Kind:** external API (`google.golang.org/genai`, Vertex AI)
**Consumers:** `internal/gemini`
**Stability:** stable (external)

**Invariants:**
- Requires a resolvable GCP project + credentials; region defaults to `global`.
