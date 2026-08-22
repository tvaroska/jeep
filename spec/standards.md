# jeep — Standards

Cross-cutting rules the system must uphold. Part of `spec/` — forkable,
**status-free**, protected. This is THE WHAT (quality bars), not THE HOW.
Agents propose changes here; they do not edit it during `/implement`.

**Stack:** Go, `google.golang.org/genai` SDK (Vertex AI backend)
**Test command:** `go build ./... && go vet ./... && go test ./...` (must include lint/type-check, not just unit tests)

---

## Coding standards

- Standard Go style: `gofmt`-clean, `go vet` must pass in T1.
- Package boundaries follow the layout in `CLAUDE.md`: `cmd/*` are thin entry
  points; all reusable logic lives under `internal/`.
- Errors are wrapped with context (`fmt.Errorf("...: %w", err)`) and surfaced to
  stderr with a non-zero exit — never a bare panic to the user.

## Quality bars

- **Verification:** every task passes the ladder — T1 (test + build + lint/type-check),
  T2 (acceptance criteria exercised), T3 (CUJ sim at sprint close).
- Single-shot semantics: commands send one request, wait for the full response,
  print to stdout — no streaming, no interactive sessions.
- No partial artifacts: on failure, do not leave zero-byte or corrupt output files.

## Security & data

- Never print credentials, access tokens, or resolved project ids to stdout/stderr.
- GCP project is auto-detected from env or gcloud config; detection code
  (`internal/gcp/`) is listed in `CRITICAL.md`.
- Region defaults to `global`.

## Observability

- Errors go to stderr with actionable messages; stdout carries only the result
  payload so downstream agents can consume it cleanly.

## Dependencies & licensing

- Prefer the official `google.golang.org/genai` SDK; pin module versions in
  `go.mod`. Review new dependencies for permissive licensing before adding.
