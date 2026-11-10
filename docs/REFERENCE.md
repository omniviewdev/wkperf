# wkperf reference

`wkperf` compares two groups of WebKit Web Inspector **Timelines** exports (a baseline and a candidate) and reports what got faster, what got slower, and by how much. It writes a terminal report, a single self-contained HTML report, and a JSON report.

It can also **analyze a single recording** (`wkperf analyze`): where wall time goes, hot functions and modules, slow frames, long tasks, and findings with optimization hints, optionally interpreted by an LLM (`--ai`).

It analyzes rendering frames (frame time, FPS, jank), script and layout events, GC, CPU, memory, network, and sampling-profiler function self time.

## Install

The repository is private, so Go needs git access to the `omniviewdev` org:

```bash
GOPRIVATE=github.com/omniviewdev go install github.com/omniviewdev/wkperf@latest
```

Or build from a checkout:

```bash
go build -o wkperf .
```

## Exporting a recording

1. In Safari (or any WebKit view with Web Inspector enabled), open Web Inspector and go to the **Timelines** tab.
2. Record the scenario you want to measure, then stop the recording.
3. Click the **Export** button in the timeline view and save the `.json` file.

### Recommended workflow

- Record the **same scenario** on both sides, with the same steps and duration.
- Capture **at least 5 runs per side**. With several runs wkperf compares medians and marks changes inside run-to-run noise as `noisy`.
- Keep runs in one directory per side, e.g. `baseline/` and `candidate/`.

### Fixture handling: comparing like with like

A comparison only means something if both sides measured the same scenario. wkperf checks this and tells you when they differ:

- **Normalization** (`--normalize auto|per-second|none`, default `auto`). Totals and counts (script ms, layout ms, GC count, network bytes, ...) grow with recording length, so a 15s capture looks "worse" than a 10s one. With `auto`, when durations differ by more than 10%, these metrics are rescaled per second. `per-second` always rescales; `none` never does.
- **Windowing** (`--trim-start SEC`, `--trim-end SEC`, `--from SEC`, `--to SEC`) on `compare`, `summary` and `analyze`: cut warm-up or tail noise, or compare only the same slice of each recording. Times are seconds from the recording start; records are clipped to the window.
- **Comparability score and warnings**. The comparison carries a 0..1 score (1 = same fixture) and warnings: `duration-mismatch`, `frame-count-mismatch`, `url-mismatch`, `instrument-mismatch`, `workload-mismatch` (different hot functions), `sample-sparse`, `run-variance`. They are shown in the terminal and HTML reports and included in the JSON.
- **Gate in CI**: `--strict-fixture` exits with code 3 when the score is below `--min-comparability` (default `0.6`).

Recommendation: capture the same scenario, with the same steps and the same duration, on both sides. Use windowing and normalization as a safety net, not a substitute.

`wkperf sample` writes a deliberately mismatched pair (`different-length-baseline.json` 10s, `different-length-candidate.json` 15s) to try this.

## Usage

```bash
# Try it with synthetic data
wkperf sample --out demo
wkperf compare demo/baseline-*.json --vs demo/candidate-*.json

# Groups: base files are positional, candidates follow --vs
wkperf compare base-1.json base-2.json base-3.json --vs cand-1.json cand-2.json cand-3.json

# Directories (all *.json inside)
wkperf compare ./baseline --vs ./candidate

# Exactly two files, no --vs: 1v1
wkperf compare before.json after.json

# Reports
wkperf compare ./baseline --vs ./candidate --format html --out report.html --open
wkperf compare ./baseline --vs ./candidate --format json > result.json
wkperf compare ./baseline --vs ./candidate --format all --out reports/

# One group on its own
wkperf summary ./baseline
wkperf summary ./baseline --format json

# Profile one recording (each file analyzed separately)
wkperf analyze recording.json
wkperf analyze recording.json --format md
wkperf analyze recording.json --format html --out analysis.html --open
wkperf analyze ./baseline --format json

# Ask an LLM, or just get the prompt
ANTHROPIC_API_KEY=... wkperf analyze recording.json --ai
wkperf analyze recording.json --emit-prompt=prompt.md
wkperf compare ./baseline --vs ./candidate --emit-prompt -

wkperf version
```

`--vs` may be repeated, comma separated, or followed by several files. Put baseline files before `--vs`.

### compare flags

| Flag | Default | Meaning |
| --- | --- | --- |
| `--vs` | | candidate files or directories |
| `--format` | `term` | `term`, `html`, `json`, or `all` |
| `--out` | `wkperf-report.html` | output file for html/json; directory or prefix with `all`. JSON with no `--out` prints to stdout |
| `--threshold` | `0.05` | relative change below which a metric is unchanged |
| `--top` | `10` | functions listed per direction |
| `--verbose` | off | show unchanged metrics too |
| `--no-color` | off | disable color (`NO_COLOR` is respected; color is off when stdout is not a TTY) |
| `--fail-on-regression` | off | exit 2 on a confident regression |
| `--open` | off | open the HTML report |
| `--title` | | HTML report title |
| `--normalize` | `auto` | `auto`, `per-second`, or `none` (see Fixture handling) |
| `--trim-start`, `--trim-end`, `--from`, `--to` | `0` | window in seconds (also on `summary` and `analyze`) |
| `--strict-fixture` | off | exit 3 when comparability score < `--min-comparability` |
| `--min-comparability` | `0.6` | threshold for `--strict-fixture` |
| `--ai`, `--emit-prompt`, `--ai-*`, `--app-*` | | see AI workflow |

## analyze

`wkperf analyze FILE...` takes files or directories and analyzes each recording on its own; nothing is aggregated.

| Flag | Default | Meaning |
| --- | --- | --- |
| `--format` | `term` | `term`, `md`, `json`, `html`, or `all` |
| `--out` | `wkperf-analysis.html` | output file; directory or prefix with `all` (writes `.md`, `.json`, `.html`). `md` and `json` print to stdout without `--out` |
| `--top` | `10` | list length for hotspots, modules, slow frames, long tasks |
| `--jank-ms` | `16.7` | slow-frame threshold |
| `--long-task-ms` | `50` | long-task threshold |
| `--no-scorecard` | off | skip the per-recording scorecard |
| `--open`, `--no-color` | | as in `compare` |

Each recording is followed by its scorecard (see Scorecard). JSON output is `{"schema": "wkperf-insights/v1", "reports": [...]}`; each report carries a `scorecard` field unless `--no-scorecard`. The HTML report is self-contained (inline SVG, light/dark): a stacked wall-time bar with legend, hotspot and module tables, slow frames, long tasks, and finding cards with severity, evidence and suggestion.

### Findings catalogue

| ID | Looks for |
| --- | --- |
| `long-tasks` | script records over `--long-task-ms` blocking the main thread |
| `forced-layout` | layout/style running after script in the same frame (layout thrashing) |
| `layout-heavy`, `style-recalc-heavy`, `paint-heavy` | large share of wall time in layout, style recalculation, paint |
| `jank` | frames over the jank budget, with the dominant cause |
| `raf-load` | expensive requestAnimationFrame callbacks |
| `timer-storm` | very many timer firings |
| `event-handler-heavy` | costly event dispatches |
| `microtask-heavy`, `observer-callback-heavy` | promise/microtask and observer callback cost |
| `gc-pressure` | garbage collection share and frequency |
| `memory-growth` | steady memory growth (possible leak) |
| `cpu-high` | sustained high CPU usage |
| `hot-function`, `hot-module` | one function or module dominating sampled self time |
| `network` | heavy or slow network activity |
| `low-sample-coverage` | sampling profiler covers little of script time (hotspots may mislead) |

Severity is `high`, `medium`, `low`, or `info`, derived from the share of wall time affected. Findings are heuristics: treat them as leads and verify with a second capture.

## audit

`wkperf audit PATH...` audits one build across many scenes (no baseline). Scene discovery: each subdirectory of a given directory is a scene and its `*.json` files are the runs; loose `*.json` files (and files given directly) are one scene each, named after the file. A directory with both is handled both ways. Each recording is windowed, analyzed, rated against the scorecard, and then aggregated: median per scene, scenes ranked worst first, overall scorecard across scenes, a cross-scene priority list, merged hot functions/modules and a duration-weighted breakdown. A file that fails to parse aborts the run with its name.

| Flag | Default | Meaning |
| --- | --- | --- |
| `--format` | `term` | `term`, `md`, `json`, `html`, or `all` |
| `--out` | `wkperf-audit.html` | output file; directory or prefix with `all` |
| `--group-by-prefix` | off | group loose files `<scene>-<n>.json` / `<scene>_<n>.json` into scene `<scene>` |
| `--min-score` | `0` (off) | exit 4 when the overall score is below N |
| `--top` | `10` | list length for priorities, hotspots, modules |
| `--jank-ms`, `--long-task-ms` | `16.7`, `50` | thresholds as in `analyze` |
| `--trim-start`, `--trim-end`, `--from`, `--to` | `0` | window applied to every recording |
| `--open`, `--no-color` | | as in `compare` |
| `--ai`, `--emit-prompt`, `--ai-*`, `--app-*` | | see AI workflow (audit prompt) |

JSON output is `{"schema": "wkperf-audit/v1", "report": {...}}`.

## Scorecard

Each recording is rated per standard as `good` / `needs-improvement` / `poor` (or `n/a` when the recording lacks the data), scored 0 to 100 (weighted mean), and graded A (90+), B (75+), C (60+), D (40+), F. "Gap" lists what is not good, with how many times over target. Targets are not Lighthouse or Core Web Vitals scores: TBT is normalized per 10 s of recording, INP is a handler-time approximation, FCP/LCP need load records, CLS is not measured, and wkperf-heuristic thresholds are our choices.

| Standard | Source | ID | Good | Poor | Weight |
| --- | --- | --- | --- | --- | --- |
| Frame time p95 | frame budget (60Hz) | `frame-p95` | ≤ 16.7 ms | > 33.4 ms | 3 |
| Jank ratio | frame budget (60Hz) | `jank-ratio` | ≤ 5% | > 25% | 3 |
| Average FPS | frame budget (60Hz) | `fps` | ≥ 55 | < 30 | 3 |
| Total Blocking Time | Lighthouse | `tbt` | ≤ 200 ms per 10 s | > 600 ms per 10 s | 2 |
| Interaction latency (INP approx.) | Core Web Vitals | `inp` | ≤ 200 ms | > 500 ms | 2 |
| Long tasks per minute | wkperf heuristic | `long-task-rate` | ≤ 3 | > 12 | 2 |
| First Contentful Paint | Lighthouse | `fcp` | ≤ 1800 ms | > 3000 ms | 1 |
| Largest Contentful Paint | Core Web Vitals | `lcp` | ≤ 2500 ms | > 4000 ms | 1 |
| Main thread script busy | wkperf heuristic | `main-busy` | ≤ 25% | > 50% | 1 |
| GC share of wall time | wkperf heuristic | `gc-share` | ≤ 3% | > 10% | 1 |
| Memory growth | wkperf heuristic | `memory-growth` | ≤ 1 MB/min | > 10 MB/min | 1 |
| Forced layouts per second | wkperf heuristic | `forced-layout-rate` | ≤ 0.5 /s | > 5 /s | 1 |

### Findings → scorecard

| Finding | Standards it typically moves |
| --- | --- |
| `jank`, `layout-heavy`, `style-recalc-heavy`, `paint-heavy`, `raf-load` | `frame-p95`, `jank-ratio`, `fps` |
| `forced-layout` | `forced-layout-rate`, `frame-p95` |
| `long-tasks` | `tbt`, `long-task-rate`, `main-busy` |
| `event-handler-heavy` | `inp`, `main-busy` |
| `timer-storm`, `microtask-heavy`, `observer-callback-heavy`, `hot-function`, `hot-module` | `main-busy` |
| `gc-pressure` | `gc-share` |
| `memory-growth` | `memory-growth` |

## AI workflow

`--ai` on `analyze` and `compare` sends the profile (or comparison plus the median-run profiles) to an LLM and prints its Markdown answer after the report. With `--format html` or `all` it is also added to the HTML report; with `--format json` on `compare` it goes to stderr, on `analyze` it is added as an `ai` field.

`--emit-prompt` needs no network and no key: it writes the exact prompt as Markdown and exits, so you can paste it into Claude Code or another agent. Use `--emit-prompt` for stdout (`--emit-prompt -`) or `--emit-prompt=FILE`.

| Flag | Default | Meaning |
| --- | --- | --- |
| `--ai` | off | call the model |
| `--ai-provider` | `anthropic` | `anthropic` or `openai` (any OpenAI-compatible server) |
| `--ai-model` | provider default | model name |
| `--ai-base-url` | provider default | API base URL |
| `--ai-max-tokens` | `4096` | answer limit |
| `--ai-timeout` | `180s` | request timeout |
| `--app-name` | `Omniview` | application name in the prompt |
| `--app-context` | | extra context; platform defaults to "macOS WKWebView (Wails)", framework to React |
| `--ai-notes` | | free-form notes for the model |
| `--emit-prompt [FILE\|-]` | | write the prompt and exit |

API keys come only from the environment: `ANTHROPIC_API_KEY` or `OPENAI_API_KEY`. They are never read from flags or config files. Without a key, `--ai` fails with an error pointing at `--emit-prompt`. Ctrl-C cancels the request.

Local models: run any OpenAI-compatible server and point wkperf at it, e.g. Ollama:

```bash
wkperf analyze recording.json --ai --ai-provider openai \
  --ai-base-url http://localhost:11434/v1 --ai-model llama3.1
```

(`OPENAI_API_KEY` may be unset for local servers.)

Config file keys live under `ai:` and env vars use `WKPERF_AI_*`:

```yaml
ai:
  provider: openai
  model: llama3.1
  base_url: http://localhost:11434/v1
  max_tokens: 4096
  timeout: 180s
```

## Continuous improvement loop

1. **Capture a baseline**: record the scenario 5 times and export into `baseline/`.
2. **Analyze**: `wkperf analyze baseline/baseline-1.json --ai` (or `--emit-prompt=prompt.md` and hand it to your coding agent) to find what to fix.
3. **Change** the code.
4. **Capture a candidate** of the *same scenario and duration* into `candidate/`.
5. **Compare**: `wkperf compare baseline/ --vs candidate/ --ai`; the verification plan in the answer names the metrics that should move.
6. **Gate in CI**: `wkperf compare baseline/ --vs candidate/ --strict-fixture --fail-on-regression` (exit 3 for non-comparable captures, 2 for regressions). Promote the candidate to the new baseline and repeat.

## Configuration

Precedence: flags > environment (`WKPERF_<FLAG>`, dashes become underscores, e.g. `WKPERF_FAIL_ON_REGRESSION=true`) > config file. The config file is `./.wkperf.yaml`, else `$XDG_CONFIG_HOME/wkperf/config.yaml` (default `~/.config/wkperf/config.yaml`).

```yaml
format: all
out: reports/
threshold: 0.03
top: 15
no-color: false
fail-on-regression: true
ai:
  provider: anthropic
```

## Metric glossary

| Group | Metrics |
| --- | --- |
| overview | recording duration, record counts |
| frames | frame count; mean/p50/p95/p99/max frame time (ms); FPS; jank counts and ratio for frames over 16.7 / 33.4 / 50 ms |
| script | per event type (event-dispatched, timer-fired, animation-frame-fired, ...) count, total, mean, p95, max ms; total script time and busy ratio |
| layout | per event type (layout, recalculate-styles, paint, composite, forced-layout, ...) count, total, mean, p95, max ms |
| gc | garbage collection count and total ms |
| cpu | total CPU mean/p95/max %; main-thread mean/p95 % |
| memory | peak, final, mean MB; per-category peak |
| network | request count, transferred bytes, mean/p95 request time |
| samples | per-function self and total time from the sampling profiler |

Verdicts: `improved`, `regressed`, `unchanged`, `noisy` (change within run-to-run noise), `info` (informational metric), `new`, `removed`. Confidence is `single-run`, `low`, or `high`.

## Exit codes

| Code | Meaning |
| --- | --- |
| 0 | success |
| 1 | error (bad input, unreadable file, write failure) |
| 2 | `--fail-on-regression` and a regressed metric with confidence other than `low` in the frames, script, layout, or cpu groups |
| 3 | `--strict-fixture` and the comparability score is below `--min-comparability` |
| 4 | `audit --min-score N` and the overall score is below N |

## CI usage

```yaml
- run: |
    wkperf compare baseline/ --vs candidate/ \
      --format all --out wkperf-reports/ --no-color --fail-on-regression
- uses: actions/upload-artifact@v4
  if: always()
  with:
    name: wkperf-reports
    path: wkperf-reports/
```
