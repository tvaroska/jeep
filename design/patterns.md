# jeep — Patterns & Conventions

THE HOW, repeated. Reusable implementation patterns and idioms this codebase
uses, so new work stays consistent with existing work. Part of `design/`.
Status-free. Complements `spec/standards.md` (which sets the rules) by showing
the concrete, in-repo way those rules are met.

---

## Pattern: Thin `cmd/*` over shared `internal/*`

**Problem:** three CLIs share request/config/project logic and must stay consistent.

**Solution:** each `cmd/jeep-*` is a thin entry point (parse → call → output);
all reusable logic lives in `internal/cli`, `internal/config`, `internal/gemini`.

**Example:** `cmd/jeep/main.go` alongside `cmd/jeep-image`, `cmd/jeep-tts`.

**When to use / avoid:** use for every new capability CLI; avoid putting business
logic in `cmd/*`.

---

## Pattern: Single-shot request/response

**Problem:** agents want a complete result in one call, not a stream.

**Solution:** send one request, block for the full response, print/write, exit.

**When to use / avoid:** all commands. Avoid streaming or interactive loops.

---

## Pattern: Shared run / dry-run helper

**Problem:** commands need consistent execution + a dry-run path for tests.

**Solution:** the `internal/cli` run/dry-run helpers centralize invocation so each
command wires the same flow and is testable without network.

**Example:** `internal/cli` (see run/dry-run helpers).

---

## Anti-patterns

- Writing partial/zero-byte artifacts on error — always write only on success.
- Leaking credentials or resolved project id to stdout/stderr (cross-link
  `DECISIONS.md` gotchas as they arise).
