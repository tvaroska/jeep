# jeep — Testing

Planning/ops doc (`docs/`). Documents how the verification ladder is realized in
this repo. The ladder — not "tests pass" — is the gate for commits and for
flipping tasks to done.

## Test command (T1)

```bash
go build ./... && go vet ./... && go test ./...
```

T1 must run **test + build + lint/type-check** — not just the unit runner. Here
that is `go build` + `go vet` + `go test`. A green build with red `go vet` is a
T1 failure.

## Verification ladder

| Tier | What | When | Gates |
|------|------|------|-------|
| **T1** | project test + build + lint/type-check (`go build ./... && go vet ./... && go test ./...`) | every `/implement` | the commit |
| **T2** | exercise the artifact against the task's acceptance criteria — run the CLI, don't just assert files exist | every `/implement` | flipping `- [x]` |
| **T3** | CUJ simulation end-to-end + judged | sprint-close (`/replan`) + on-demand (`/verify`) | sprint transition |

## Strategy

- Unit tests for `internal/*` logic (config resolution, cli parsing, gemini client with fakes).
- Integration tests for the client against a fake genai backend.
- E2E tests for CUJs — the T3 **Drivers** declared in `spec/cujs.md` (`go test
  ./cmd/*/... -run TestE2E`), using a fake client so they run without credentials.

## Conventions

- Test files live alongside source (`*_test.go`), per Go convention.
- Name tests descriptively: `Test{What}_{Condition}_{Expected}`.
- Every new feature includes tests before T1 can pass.

## T3: CUJ simulation (Driver + Judge)

Run via `/verify [cuj-id]` (on demand) or `/replan` (sprint-close gate). For each
CUJ in `spec/cujs.md`:
- **Driver** — the harness command from the CUJ's `Driver:` field.
- **Judge — both must pass:**
  - *Deterministic:* state assertions (exit code, artifact validity) + **hard-fail traps** (panic, leaked secret, partial file).
  - *External LLM judge (`jeep`):* grades the run against the CUJ Success Criteria:
    `jeep --schema rubric.json --system "<grader>" -f criteria.txt -f run_output.txt --format json`.

`jeep` (this repo's own binary) serves as the LLM-judge for the T3 tier.

## How to add tests

1. Identify what to test (happy path, edge cases, error cases).
2. Write the test following existing patterns.
3. Run `go build ./... && go vet ./... && go test ./...`; ensure nothing breaks.
4. For a new CUJ, add its `Driver:` and `Judge:` fields in `spec/cujs.md`.
