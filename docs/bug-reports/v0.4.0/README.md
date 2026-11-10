# wkperf v0.4.0 bug reports

Four analysts verified the v0.4.0 behaviour claims against `~/Scratch/profiles` (real Safari captures of a React 19 / Vite / Wails app) and the source at tag `v0.4.0` (commit `392facf`). Every bug has status, severity, reproduce commands, root cause with file and line, proposed fix, regression test and notes. Each report ends with its own fix plan and a list of things checked that are fine.

| File | Area | Bugs |
|---|---|---|
| `01-sample-timestamps.md` | Samples with `timestamp == 0` are dropped | S1–S3 |
| `02-output-contract.md` | Output size, `--max-bytes`, reproduce commands, metric keys, schema, errors, confidence | O1–O11 |
| `03-analysis-correctness.md` | Scorecard, frame classification, GC, nesting, breakdown, ownership, detectors | A1–A16 |
| `04-flame-exports-mcp.md` | `flame` outputs, source maps, query/MCP bounds, diff, HTML | F1–F15 |

`fixtures/` holds everything the reproduce commands need (small real captures, synthetic generators, a source-map bundle). `a-real-rapid-scrolling-deployment.json` and `o-real-wrapped-rapid-scrolling-deployment.json` are byte-identical; keep either one and update the references. A16 also cites `~/Scratch/profiles/pods-not-moving.json`, which is not in the repo; any fixture with at least two active frames reproduces it. Some real-capture figures come only from the big profiles, and each bug says which.

## Corrections to earlier claims in this session
- The "1444 ms top function" quoted for v0.3.0 was the root `(anonymous)` microtask frame, not `getAnimations` (v0.3.0 read stacks in reverse order). With all samples kept, the cost is synchronous React rendering: `performSyncWorkOnRoot` 1441 ms inclusive, ending in `MenuRootInternal2` (S1).
- `getFiniteAnimations` and the other frames on the `getAnimations` path are Base UI library code (`@base-ui/react` v1.9.0), not app code. `getAnimations` flushes style; it is not a layout read (A8, A11).
- `samples.total_ms` does not exist; the key is `samples.sampled_ms`. Four `samples.*` keys exist (O6).
- The hot-path calculation itself is correct (A14).
- The "idle 1Hz re-render" lead for the app was a measurement artifact of idle gaps counted as frames (A4).

## Severity summary
- **Blocker:** S1 (whole-recording output drops every untimed sample, 28–85% of sampled weight), O1 (default output unbounded).
- **High:** S2, O2, O8, A1, A3, A4, A5, A8, F1, F2.
- **Medium:** S3 (latent, high once S1 is fixed), O3–O6, O10, A2, A6, A7, A9, A11, A13, F3–F9.
- **Low:** the rest.

## Fix order
Dependencies come from the reports' own fix plans.

1. **S1 together with the S3 tolerance check.** Keep untimed samples in whole-recording trees, rankings and flame graphs; exclude them from time-based output; always report their count and weight. This changes most sampler numbers, so do it first and re-baseline. It raises distinct stacks about 9× on the large profile, so schedule O1 straight after.
2. **O1 (stack interning), then O2–O4.** Store each stack once and refer to it by ID; make `--brief` and `--max-bytes` honour priority order, apply to `term`, and keep hot-path leaves.
3. **Metric correctness (A6, A5, A3 + A7, A4).** Start with the event-name fix (A6), then nested records (A5), GC and breakdown precedence (A3 + A7), then frame work vs interval length (A4).
4. **Scorecard and gates (A1, A2, A16, O8).** Minimum-sample policy, partial-weight grades, `--jank-ms` consistency, empty windows in `compare`/`summary`.
5. **Attribution quality (A8, A9, A10–A13, A14, A15, S2).** Ownership for bundled React/Base UI code, React 19 sync-render detector, duplicate findings, single-sample findings, severity consistency, windows that exclude untimed samples.
6. **Query/MCP bounds and discovery (F1, O5, O6, O7, O9–O11).** Byte caps for query and MCP, valid reproduce commands, metric-key discovery, sparse rows, error codes, confidence, schema descriptions.
7. **Flame and export (F2, F4, F5, F6, F7, F3, F8–F15).** Source-map names, speedscope-compatible folded output, `--focus` with multiple contexts, normalized diffs, source-map diagnostics, then the rest.

After each step run: `fixtures/o-repro-commands.py`, the `o-deep-stacks.jq` size loop, a `wkperf schema` validation of `analyze`, `query` and `audit` JSON, and `go test ./...`.

## Brief for the coding agent

```markdown
You are fixing bugs in wkperf (github.com/omniviewdev/wkperf, Go) at tag v0.4.0. The bugs are documented in docs/bug-reports/v0.4.0/ (README.md plus four reports). Read README.md first, then the report for the bug you work on. Each bug has reproduce commands that use files under docs/bug-reports/v0.4.0/fixtures/, a root cause with file and line, a proposed fix and a regression test to add.

Rules:
- Work in the fix order in README.md. Land each bug (or tightly coupled group) with a regression test that fails before the fix and passes after, using the fixtures named in the report or generators in internal/testgen. Do not delete or rewrite the reports.
- Reproduce each bug first with the commands in its report and confirm the "actual" output. If it no longer reproduces, say so and skip it.
- Where a report lists alternatives it rejected, follow its recommendation unless you find a concrete reason not to; if you deviate, note why in the commit message.
- Keep output deterministic: stable ordering, ties broken by name, then url, line, column; no map-iteration order in output.
- Update docs/REFERENCE.md, the README and `wkperf schema` for every user-visible change. Bump schema versions on breaking changes. Keep comments short.
- Verify after each phase: `go test ./...`, `docs/bug-reports/v0.4.0/fixtures/o-repro-commands.py` (every printed reproduce command must succeed and reproduce its value), schema validation of analyze/query/audit JSON, and a before/after table of the numbers the bug report names.
- Do not use the large real profiles in tests; they are not in the repo.
```
