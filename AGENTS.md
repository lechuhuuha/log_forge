# Project Instructions

## Unit Test Style
- Write unit tests using table-driven style by default.
- Use `service/aggregation_test.go` as the canonical reference for structure.
- Prefer a `cases := []struct { ... }` pattern with `t.Run(tc.name, func(t *testing.T) { ... })`.
- For benchmarks, prefer case-based sub-benchmarks with `b.Run(...)` when there are multiple scenarios.
- Keep helper setup functions small and reusable, but assertions should remain close to each table case.

## Commit Message Style
- Follow concise commit subjects in the existing repository style.
- Prefer conventional prefixes with lowercase type and colon, for example:
  - `feat: ...`
  - `fix: ...`
  - `refactor: ...`
  - `docs: ...`
  - `test: ...`
  - `chore: ...`
- Keep subject lines short, imperative, and focused on the primary change.
