# jeep

CLI tools for Google Gemini via Vertex AI, designed for use by coding agents.

## Usage context

These tools are meant to be called by coding agents (Claude Code, etc.) as single-shot commands — no streaming, no interactive sessions. Each invocation sends a request, waits for the full response, and prints the result to stdout.

## Project structure

- `cmd/jeep/` — text prompt CLI
- `cmd/jeep-image/` — image generation CLI
- `cmd/jeep-tts/` — text-to-speech CLI
- `internal/gemini/` — Gemini API client (Vertex AI)
- `internal/cli/` — shared CLI parsing
- `internal/config/` — model resolution
- `internal/gcp/` — GCP project detection

## Build & test

```sh
go build ./...
go test ./...
```

## Key details

- Go module: `github.com/tvaroska/jeep`
- Uses `google.golang.org/genai` SDK with Vertex AI backend
- Region defaults to `global`
- GCP project auto-detected from env or gcloud config
