# jeep — Critical User Journeys

End-to-end flows that matter most. Every feature should support at least one CUJ.
This file is part of `spec/` — the forkable, **status-free** source of truth.
Keep counts/status out of here; live status lives in `TODO.md`.

Each CUJ carries, on top of Description / Steps / Success Criteria / Supported By,
two fields the verification ladder's T3 tier consumes:
- **Driver:** how the journey is played end-to-end — a code e2e harness command
  OR an LLM-agent-sim scenario id. `/verify` and `/replan` use whatever the repo
  provides; no forced rewrite.
- **Judge:** how a run is scored — the deterministic must-pass assertions +
  **hard-fail traps**, plus the rubric criteria handed to the external LLM judge
  (`jeep`) against the Success Criteria. BOTH judges must pass.

---

## CUJ-1: Single-shot text prompt from a coding agent

**Description:** A coding agent invokes `jeep` as a one-shot command to get a
text completion from Gemini via Vertex AI, capturing the full response on stdout.

**Steps:**
1. Agent runs `jeep "<prompt>"` (or `jeep -f prompt.txt`).
2. jeep resolves the GCP project + model, sends the request to Vertex AI.
3. jeep waits for the full response and prints it to stdout, exit 0.

**Success Criteria:**
- Response text is printed to stdout with no interactive/streaming artifacts.
- Non-zero exit with a clear stderr message on auth/model/quota failure.
- Model and region resolve per config defaults when unspecified.

**Supported By:** `cmd/jeep`, `internal/gemini`, `internal/cli`, `internal/config`, `internal/gcp`

**Driver:** code e2e harness — `go test ./cmd/jeep/... -run TestE2E`

**Judge:**
- *Deterministic (must-pass):* process exits 0; stdout non-empty; no partial-write on error paths.
- *Hard-fail traps:* any panic; credentials or project id echoed to stderr/stdout; wrote to a protected path.
- *LLM-judge rubric:* output is a complete, well-formed answer to the prompt with no truncation.

---

## CUJ-2: Image generation to a file

**Description:** An agent generates an image from a text prompt and receives a
usable image artifact on disk.

**Steps:**
1. Agent runs `jeep-image "<prompt>" -o out.png`.
2. jeep-image sends the request to Vertex AI and receives image bytes.
3. The artifact is written to the requested path, exit 0.

**Success Criteria:**
- A valid image file is produced at the requested path.
- Clear stderr + non-zero exit on failure; no zero-byte artifact left behind.

**Supported By:** `cmd/jeep-image`, `internal/gemini`

**Driver:** code e2e harness — `go test ./cmd/jeep-image/... -run TestE2E`

**Judge:**
- *Deterministic (must-pass):* output file exists and is a non-empty, decodable image; exit 0.
- *Hard-fail traps:* any panic; partial/corrupt file left on error; secret leaked in trace.
- *LLM-judge rubric:* image plausibly matches the prompt (when a vision judge is available).

---

## CUJ-3: Text-to-speech to an audio file

**Description:** An agent synthesizes speech from text and receives a playable
audio artifact.

**Steps:**
1. Agent runs `jeep-tts "<text>" -o out.wav`.
2. jeep-tts sends the request to Vertex AI and receives audio bytes.
3. The artifact is written to the requested path, exit 0.

**Success Criteria:**
- A valid audio file is produced at the requested path.
- Clear stderr + non-zero exit on failure; no zero-byte artifact left behind.

**Supported By:** `cmd/jeep-tts`, `internal/gemini`

**Driver:** code e2e harness — `go test ./cmd/jeep-tts/... -run TestE2E`

**Judge:**
- *Deterministic (must-pass):* output file exists, is non-empty, and has a valid WAV header; exit 0.
- *Hard-fail traps:* any panic; partial/corrupt file left on error; secret leaked in trace.
- *LLM-judge rubric:* N/A (deterministic tier is authoritative for TTS).
