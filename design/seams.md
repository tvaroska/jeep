# jeep — Seams & Boundaries

THE HOW, at the joints. Documents the seams where the system can be tested,
faked, or extended — the boundaries between zones, and the injection points the
verification ladder relies on. Part of `design/`. Status-free.

A **seam** is a place where you can alter behavior without editing the code on
either side — the basis for fakes, e2e harnesses (T3 Drivers), and safe extension.

---

## Zones / trust boundaries

| Boundary | Inside | Outside | What crosses |
|----------|--------|---------|--------------|
| Process ↔ Vertex AI | jeep CLI + client | Google Vertex AI | requests/responses over authed HTTPS |
| Process ↔ environment | jeep CLI | env vars, gcloud config | GCP project id, credentials |
| stdout / stderr | jeep CLI | calling agent / shell | result payload (stdout), diagnostics (stderr) |

## Test seams

### Gemini API client (`internal/gemini`)

**Where:** the client interface used by `cmd/*`.
**Real implementation:** genai/Vertex AI calls.
**Fake / harness:** inject a fake client returning canned responses so `cmd/*`
e2e tests run without network/credentials.
**Used by CUJs:** CUJ-1, CUJ-2, CUJ-3

### GCP project detection (`internal/gcp`)

**Where:** project-resolution function.
**Real implementation:** reads env / gcloud config.
**Fake / harness:** override via env in tests to pin a project id.

### Output sink

**Where:** stdout writer / `-o` file path in `cmd/*`.
**Real implementation:** os.Stdout / file on disk.
**Fake / harness:** buffer / temp dir in tests to assert exact bytes and no
partial-write on error.

## Extension points

- New Gemini capabilities become new `cmd/jeep-*` binaries sharing `internal/cli`,
  `internal/config`, and `internal/gemini`.
