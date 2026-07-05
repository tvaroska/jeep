# jeep

[![Test](https://github.com/tvaroska/jeep/actions/workflows/test.yml/badge.svg)](https://github.com/tvaroska/jeep/actions/workflows/test.yml)
[![Release](https://img.shields.io/github/v/release/tvaroska/jeep)](https://github.com/tvaroska/jeep/releases/latest)
[![codecov](https://codecov.io/gh/tvaroska/jeep/graph/badge.svg)](https://codecov.io/gh/tvaroska/jeep)
[![Go Report Card](https://goreportcard.com/badge/github.com/tvaroska/jeep)](https://goreportcard.com/report/github.com/tvaroska/jeep)

CLI tools for Google Gemini via Vertex AI.

| Tool | Purpose |
|------|---------|
| `jeep` | Text prompts with file/URL attachments |
| `jeep-image` | Image generation |
| `jeep-tts` | Text-to-speech |
| `jeep-research` | Deep research reports |

## Install

```sh
go install github.com/tvaroska/jeep/cmd/jeep@latest
go install github.com/tvaroska/jeep/cmd/jeep-image@latest
go install github.com/tvaroska/jeep/cmd/jeep-tts@latest
go install github.com/tvaroska/jeep/cmd/jeep-research@latest
```

## Setup

Requires a GCP project with the Vertex AI API enabled and valid credentials (e.g. `gcloud auth application-default login`).

The GCP project is auto-detected from `GOOGLE_CLOUD_PROJECT`, `GCLOUD_PROJECT`, or `gcloud config get-value project` — or pass `--project` explicitly.

## Model configuration

Each tool resolves its model in order:

1. `--model` flag
2. `JEEP_MODEL_<USE>` env var (`JEEP_MODEL_TEXT`, `JEEP_MODEL_IMAGE`, `JEEP_MODEL_TTS`, `JEEP_MODEL_RESEARCH`)
3. Per-tool default: `gemini-3.5-flash` (text), `gemini-3.1-flash-image` (image), `gemini-3.1-flash-tts-preview` (tts), `deep-research-preview-04-2026` (research)

## jeep

Send text prompts to Gemini, optionally with file attachments.

```
jeep [flags] "prompt" [-f [name=]file ...]
```

### Flags

| Flag | Description |
|------|-------------|
| `-f value` | Attach a file, YouTube URL, or `gs://` URI (repeatable) |
| `--json file` | Load all parameters from a JSON file |
| `--model string` | Model name |
| `--system string` | System instruction |
| `--project string` | GCP project (default: auto-detect) |
| `--region string` | Vertex AI region (default: `global`) |
| `--schema file` | JSON schema file for structured output |
| `--format string` | Output format: `text` (default) or `json` |
| `--search` | Ground responses with Google Search |

### Examples

```sh
jeep "hello world"
jeep "describe this" -f photo.jpg
jeep "compare a vs b" -f a=img1.jpg -f b=img2.jpg
jeep "summarize" -f https://youtube.com/watch?v=xxx
jeep "analyze" -f gs://bucket/doc.pdf
echo "explain this code" | jeep -f main.go
jeep --system "Be concise" "what is this?" -f image.png
jeep --search "latest news about Go programming language"
jeep --json request.json
jeep --schema person.json "Extract name and age" -f doc.txt
jeep --format json "hello world"
jeep --format json --search "latest Go news"
```

### JSON request format

```json
{
  "prompt": "Compare these images",
  "model": "gemini-2.5-flash",
  "system": "Be concise",
  "project": "my-project",
  "region": "global",
  "search": true,
  "schema": {"type": "object", "properties": {"name": {"type": "string"}}},
  "files": [
    {"name": "a", "path": "img1.jpg"},
    {"path": "gs://bucket/doc.pdf"}
  ]
}
```

### JSON output format (`--format json`)

```json
{
  "text": "response text",
  "model": "gemini-3.5-flash-001",
  "finish_reason": "STOP",
  "prompt_tokens": 10,
  "output_tokens": 42,
  "sources": [
    {"title": "Page Title", "uri": "https://example.com", "domain": "example.com"}
  ]
}
```

The `sources` field is populated when `--search` is used. In text mode, sources are printed to stderr.

## jeep-image

Generate images using Gemini.

```
jeep-image [flags] "prompt" [-f [name=]file ...]
```

### Flags

| Flag | Description |
|------|-------------|
| `-o, --output string` | Output file path (default: `output.png`) |
| `-f value` | Reference image (repeatable) |
| `--model string` | Model name |
| `--project string` | GCP project (default: auto-detect) |
| `--region string` | Vertex AI region (default: `global`) |

### Examples

```sh
jeep-image "a cat astronaut floating in space"
jeep-image "a cat astronaut" -o cat.png
jeep-image "make this sketch realistic" -f sketch.jpg
jeep-image "combine these styles" -f a=style.jpg -f b=content.jpg
echo "a sunset over mountains" | jeep-image -o sunset.png
```

## jeep-tts

Generate speech audio from text.

```
jeep-tts [flags] "text to speak"
```

### Flags

| Flag | Description |
|------|-------------|
| `-o, --output string` | Output WAV file (default: `output.wav`) |
| `-v, --voice string` | Voice name (default: `Kore`) |
| `--model string` | Model name |
| `--project string` | GCP project (default: auto-detect) |
| `--region string` | Vertex AI region (default: `global`) |

30 voices available: Zephyr, Puck, Charon, Kore, Fenrir, Leda, Orus, Aoede, Callirrhoe, Autonoe, Enceladus, Iapetus, Umbriel, Algieba, Despina, Erinome, Algenib, Rasalgethi, Laomedeia, Achernar, Alnilam, Schedar, Gacrux, Pulcherrima, Achird, Zubenelgenubi, Vindemiatrix, Sadachbia, Sadaltager, Sulafat.

### Examples

```sh
jeep-tts "Hello, how are you today?"
jeep-tts -v Puck "Welcome to the show!" -o intro.wav
echo "Say cheerfully: Have a wonderful day!" | jeep-tts -o greeting.wav
```

## jeep-research

Run deep research queries using Gemini's Interactions API.

```
jeep-research [flags] "research query"
```

### Flags

| Flag | Description |
|------|-------------|
| `--agent string` | Agent name (default: `deep-research-preview-04-2026`) |
| `--project string` | GCP project (default: auto-detect) |
| `--region string` | Vertex AI region (default: `global`) |
| `--format string` | Output format: `text` (default) or `json` |
| `--timeout duration` | Request timeout (default: `30m`) |
| `-q, --quiet` | Suppress stderr status messages |
| `--dry-run` | Show config without making the API call |

### Examples

```sh
jeep-research "what is the current state of solid-state batteries?"
jeep-research --format json "explain CRISPR gene editing"
jeep-research -q "history of quantum computing" > report.md
echo "analyze recent advances in fusion energy" | jeep-research
```

In text mode, sources are appended as a numbered list. In JSON mode, sources are included in the `sources` array.

## Supported file types

Images (jpg, png, gif, webp), video (mp4, webm, mov, avi, mkv), audio (mp3, wav, flac, ogg), documents (pdf, txt, csv, json). Other file types are sent with auto-detected MIME types.
