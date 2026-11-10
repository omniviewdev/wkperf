# wkperf reference

`wkperf` compares two groups of WebKit Web Inspector **Timelines** exports (a baseline and a candidate) and reports what got faster, what got slower, and by how much. It writes a terminal report, a single self-contained HTML report, and a JSON report.

It can also **analyze a single recording** (`wkperf analyze`): where wall time goes, hot functions and modules, slow frames, long tasks, and findings with optimization hints, optionally interpreted by an LLM (`--ai`).

It analyzes rendering frames (frame time, FPS, jank), script and layout events, GC, CPU, memory, network, and sampling-profiler function self time.

## Install

Install with Go:

```bash
go install github.com/omniviewdev/wkperf@latest
```

Or build from a checkout:

```bash
go build -o wkperf .
```

## Exporting a recording

1. In Safari (or any WebKit view with Web Inspector enabled), open Web Inspector and go to the **Timelines** tab.
2. Record the scenario you want to measure, then stop the recording.
3. Click the **Export** button in the timeline view and save the `.json` file.

### Capture formats and timing

Both flat `WI.TimelineRecording.exportData()` objects and Safari's `{ "version": 1, "recording": {...}, "overview": {...} }` exports are accepted. `overview` is UI state, not profiling evidence. `wkperf sample --wrapped --out demo` generates wrapped fixtures; omit `--wrapped` for flat fixtures.

The sample set also includes `regressions/idle-gap.json`, `stack-evidence.json` (deep minified stacks), `get-animations.json`, `react-microtask.json`, `periodic-background.json`, `paused-minified.json`, and `regressions/maps/bundle.js.map`. These are synthetic reproductions, not captured application benchmarks.

Paused intervals in `recording.discontinuities` use `{startTime,endTime}` in recording timestamps. Their union is excluded from active wall time and rate denominators. Work overlapping pauses is clipped; paused frame intervals do not enter rendering statistics. Window flags remain seconds from the original recording start, not a compressed paused-time clock.

`compare` and `summary` reject empty or zero-active-time selections with `window_error` and exit 1 before producing summaries or evaluating CI gates. A comparison cannot pass strict-fixture checks on an empty window.

Rendering-frame spans alone do not measure CPU work. An interval longer than 50 ms with less than 10% exclusive instrumented work and no input-driven event is an inferred idle gap, reported as an idle wakeup rather than jank. An input-driven interval over budget with at most one budget of measured work is an input stall, not evidence that JavaScript occupied the whole interval. Idle gaps and input stalls do not enter active-frame percentiles, work-bound jank ratios, or FPS. Slow active frames retain their observed duration; work over budget and unexplained interval excess remain separate.

Exclusive main-thread work follows the innermost record: latest original start, then shortest duration, then a deterministic category tie-break. Script, style, layout, paint, and composite partition that measured work. GC collector intervals are a separate overlay, with overlap against work; their elapsed duration is not a main-thread pause measurement. Uninstrumented active-frame time is labeled accordingly rather than attributed to JavaScript or GC.

Tasks are maximal unions of overlapping non-GC, non-count-only script records. Touching intervals remain separate tasks. Long-task counts, TBT, triage, and `--around-long-task` share those boundaries, so nested callbacks and microtasks are not counted repeatedly. WebKit event names include `microtask-dispatched` and `observer-callback`; per-trigger inclusive durations can overlap and are labeled separately from exclusive work.

Analysis exposes `gcMs` and `gcOverlapWithWorkMs` outside the exclusive breakdown, and keeps input stalls in their own bounded `inputStalls` list with totals. Jank findings cite `findings.jank.ratio` and `findings.jank.threshold_ms`; fixed-standard `frames.*` measurements remain separate. `findings.jank.interval_excess_ms` reports observed interval excess independently of measured work excess.

Sampling weights come from `durations[i]`, in seconds, paired with `stackTraces[i]`. Missing weights use a 1 ms default. WebKit's actual [CallingContextTree consumer](https://github.com/WebKit/WebKit/blob/main/Source/WebInspectorUI/UserInterface/Models/CallingContextTree.js) visits frames last-to-first and identifies index zero as the leaf. wkperf normalizes these raw stacks to root-to-leaf paths. This follows the consumer rather than the protocol's ambiguous top/bottom wording.

Sample durations are statistical weights, not measured execution intervals. A time window selects samples by timestamp in `[from,to)` and retains each selected sample's full weight; it does not spread or proportionally clip the weight over wall time. Sampling totals can therefore differ from instrumented script totals and should not be presented as an exclusive elapsed-time breakdown.

### Samples without usable timestamps

When the original recording starts after zero, samples with `timestamp: 0` have an unknown position in time, not a position at the start of the recording. WebKit can export these when the sampler's stopwatch has not accumulated time. Their weights are still useful for whole-recording rankings, call trees, ownership, folded stacks, and coverage. A genuine zero-time sample in a recording starting at zero remains timed.

Whole-recording views include both timed and untimed weights. Partial windows, task/frame attribution, trigger tables, and periodic detection use timed samples only. Output reports untimed counts, weights, and exclusions explicitly; it never reconstructs timestamps from sample weights. Coverage can exceed 1 because weights include default estimates and event averages while script time is an exclusive measurement.

Export order can bound a run of untimed samples between neighboring timed samples when those timed timestamps are monotonic. These spans are labeled inferences used only to explain missing temporal attribution; they never move sampled weight into a window. Stack first/last times are nullable when no timed observation exists.

Window coverage uses a shared 1 ns tolerance. An explicitly selected full recording includes the same samples as the default view, even when a printed endpoint is rounded. Reproduction commands omit `--from` and `--to` for whole-recording measurements.

| Metric key | Unit | Meaning |
| --- | --- | --- |
| `samples.untimed_count` | count | untimed observations in the original recording |
| `samples.untimed_ms` | ms | their total statistical weight |
| `samples.untimed_excluded_ms` | ms | weight excluded from this selection because its position is unknown; zero for a whole-recording view |

Query source metadata uses `untimed_samples`, `untimed_ms`, `untimed_excluded`, and `untimed_spans`. These describe missing placement in the original recording, not a claim that every unknown sample occurred inside the requested window.

See WebKit's [TimelineRecording implementation](https://github.com/WebKit/WebKit/blob/main/Source/WebInspectorUI/UserInterface/Models/TimelineRecording.js) and [RenderingFrameTimelineRecord implementation](https://github.com/WebKit/WebKit/blob/main/Source/WebInspectorUI/UserInterface/Models/RenderingFrameTimelineRecord.js) for export semantics. Real recordings are not bundled; regression fixtures reproduce reported shapes and edge cases.

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
| `--format` | `term` | `term`, `md`, `html`, `json`, or `all` |
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
| `--format` | `term` | `term`, `md`, `html`, `json`, `folded`, or `all` |
| `--out` | `wkperf-analysis.html` | output file; `-` writes to stdout; directory or prefix with `all` (writes `.md`, `.json`, `.html`). `md` and `json` print to stdout without `--out` |
| `--top` | `10` | list length for hotspots, modules, slow frames, long tasks |
| `--jank-ms` | `16.7` | slow-frame threshold |
| `--long-task-ms` | `50` | long-task threshold |
| `--no-scorecard` | off | skip the per-recording scorecard |
| `--sourcemap` | auto-discovery only | repeatable source-map file or directory; show original and generated locations |
| `--module-grouping` | `file` | `file` or `parent-directory`; also supported by audit and comparison's representative analyses |
| `--brief` | off | compact agent-oriented Markdown or terminal output |
| `--max-bytes` | `0` (unlimited) | cap Markdown or terminal bytes, including the final truncation marker; positive limits below 64 are rejected |
| `--open`, `--no-color` | | as in `compare` |

Each recording is followed by its scorecard (see Scorecard). The unreleased fixes use `wkperf-insights/v3`; v0.4.0 emits v2. Each report carries a `scorecard` field unless `--no-scorecard`. Consumers must check the `schema` discriminator, including nullable stack times and untimed-sample metadata. HTML includes time breakdowns, self and inclusive hotspots, trigger attribution, idle wakeups, and findings with confidence and verification guidance.

Bounded text cannot be combined with a live `--ai` completion; emit the bounded local report or a prompt separately. The limit applies to the complete text artifact, including scorecards and truncation markers. JSON keeps exact selected evidence and explicit retrieval metadata rather than inheriting text limits.

`analyze --out -` writes a single-format report to stdout. It cannot be combined with `--format all` or `--open`.

### Findings catalogue

| ID | Looks for |
| --- | --- |
| `long-tasks` | script records over `--long-task-ms` blocking the main thread |
| `forced-layout` | explicit forced-layout records or layout beginning inside a script interval; not ordinary rendering after script |
| `layout-after-script` | layout/style follows script within a frame; a rendering-order signal, not evidence of forced synchronous layout |
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

Severity is `high`, `medium`, `low`, or `info`. Function and module findings consider absolute sampled impact, wall-time share, and sample coverage—not just share of samples. Sparse samples reduce confidence. Findings are leads, not causal proof; confirm with source inspection and repeat captures.

Sampled hotspot findings require at least 20 ms and 0.1% of active wall time. A hot function must exceed 10% of sampled self time; a hot module must exceed 25%. High severity also requires at least 15% wall impact and 30% sampled share; medium requires at least 5% wall impact and 10% sampled share. Fewer than 100 samples, coverage below 0.5, or unavailable coverage downgrades sampled severity. Single-capture findings carry low confidence; short captures under 10 active seconds and sparse samples add explicit reasons.

### Function identity, source maps, and attribution

Function identity is `(url,line,column,name)`, with `sourceID` in place of the URL when the URL is empty. Distinct anonymous functions on one minified line stay distinct. Reports print generated `file:line:column` and original locations when a map resolves them. Native frames retain their source identity even without a URL.

Pass `--sourcemap path/to/bundle.js.map` or `--sourcemap path/to/maps` repeatedly. Standard v3 mappings resolve original file, line, and column; function names use conservative declaration-oriented lookup rather than the parameter token at the profiler position. Anonymous frames do not inherit a nearby parameter name. Local bundle-adjacent maps and recording-adjacent bundle-name sidecars can be discovered without network requests. A directory scan warns and continues past unrelated unsupported maps; explicitly supplied unsupported map files still fail. Missing relocated sources retain their cleaned map-relative path with a warning rather than inventing a local absolute path. Ambiguous matches retain the generated frame with a warning.

Hot functions retain a stable query `id`, self and inclusive time, weighted callers and callees, and `commonStack`. The common stack is the most frequent complete containing path by sample count, with weight and lexical tie-breaks; it is distinct from the heaviest window stack. Module grouping defaults to individual file URLs without query/hash. Use `--module-grouping parent-directory` for roll-up. Inclusive time overlaps between callers and callees; never add inclusive rows as a wall-time breakdown.

Trigger tables separate event names and timer/rAF/microtask dispatches. Each row has count, total, mean, p95, max, and sampled self/inclusive hotspots within its intervals. Sampling-based attribution uses timestamp overlap; missing samples are stated explicitly. A native layout-related API in a stack is a lead, not proof that it forced layout.

## audit

`wkperf audit PATH...` audits one build across many scenes (no baseline). Each subdirectory is a scene and its `*.json` files are repeat runs; loose files are individual scenes. A mixed directory includes both. Each recording is windowed, analyzed, and rated before per-scene aggregation. Bad files are reported individually; valid recordings still produce results. Any file failure makes the final exit status nonzero.

| Flag | Default | Meaning |
| --- | --- | --- |
| `--format` | `term` | `term`, `md`, `json`, `html`, or `all` |
| `--out` | `wkperf-audit.html` | output file; directory or prefix with `all` |
| `--group-by-prefix` | off | group loose files `<scene>-<n>.json` / `<scene>_<n>.json` into scene `<scene>` |
| `--min-score` | `0` (off) | exit 4 when the overall score is below N or an overall grade is unavailable |
| `--top` | `10` | list length for priorities, hotspots, modules |
| `--jank-ms`, `--long-task-ms` | `16.7`, `50` | thresholds as in `analyze` |
| `--trim-start`, `--trim-end`, `--from`, `--to` | `0` | window applied to every recording |
| `--open`, `--no-color` | | as in `compare` |
| `--ai`, `--emit-prompt`, `--ai-*`, `--app-*` | | see AI workflow (audit prompt) |

The unreleased audit envelope is `{"schema": "wkperf-audit/v2", "report": {...}}`; v0.4.0 emits v1. Its embedded analysis follows the updated analysis contract.


## Flame graphs

`wkperf flame FILE...` builds weighted root-to-leaf call trees from sampler stacks. Width represents inclusive sampled milliseconds; it does not represent elapsed frame duration or event latency. Native and anonymous frames are retained.

```bash
wkperf flame recording.json --format html --out flame.html
wkperf flame recording.json --format svg --out flame.svg
wkperf flame recording.json --format folded --out stacks.folded
wkperf flame recording.json --format speedscope --out profile.speedscope.json
wkperf flame recording.json --format json --out tree.json
wkperf flame recording.json --focus performSyncWorkOnRoot --from 2 --to 8
wkperf flame --diff before.json after.json --format html --out diff.html
```

| Flag | Meaning |
| --- | --- |
| `--format html\|svg\|folded\|speedscope\|json` | interactive HTML by default; static SVG, integer-microsecond collapsed stacks, Speedscope sampled profile, or compact flat JSON |
| `--focus FUNC` | merge all contexts of one function; an exact `n_` selector retains one calling context |
| `--invert` | invert the call view |
| `--min-ms N` | suppress nodes below a sampled-time threshold |
| `--from SEC`, `--to SEC` | restrict samples to recording-relative bounds |
| `--around-long-task N` | select the Nth longest active long task, starting at 1; `0` disables; GC and count-only records are excluded |
| `--sourcemap PATH` | repeatable source-map files or directories |
| `--normalize auto\|none\|per-second\|share` | differential display normalization; auto uses per-second when active durations differ by more than 10% |
| `--diff A B` | show changes between two profiles with a differential color scale |

HTML is self-contained: zoom/focus by clicking frames, search with a regular expression, switch call direction, and inspect generated/original source locations and self/inclusive time in tooltips. SVG works without JavaScript. Analysis and audit HTML embed the same graph implementation. No external scripts, fonts, or CDN are required.

HTML defaults to `wkperf-flame.html`; `--out -` writes it to stdout. Other formats default to stdout. Multiple inputs remain separate charts/profiles. Folded counts are rounded integer microseconds, including raw baseline/candidate columns for differential output. Spaces and backslashes remain literal; `%`, semicolons, and ASCII controls are percent-encoded to keep stack boundaries unambiguous. Speedscope diff output retains separate raw profiles. Visual inversion and minimum-size filtering never truncate canonical exported stacks.

Function focus includes every matching calling context without counting a recursive sample twice. Generated and original names, locations, stable IDs, and `/regex/` selectors use exact selection rules; ambiguous distinct functions return candidates instead of guessing. Interactive focus follows the same semantics, with regex highlighting kept separate. Deep graphs place roots at the top; tooltips and static labels retain readable contrast.

Differential JSON retains raw totals, active durations, per-function/context before/after weights, resolved mode, unit, and comparability warnings. Displayed deltas use milliseconds, milliseconds per active second, or percentage points of selected sampled weight. Folded and Speedscope exports retain raw weights even when a visual graph is normalized.

`wkperf-flame/v2` has one document-level `frames` table shared across charts. Profile trees use flat preorder `nodes`, zero-based parent/frame indices, a root index, and sample references to terminal path IDs. The synthetic root uses `-1` parent/frame indices. Timed sample `t` values are recording-relative seconds; `w` is exact statistical weight in milliseconds. Untimed samples have no invented timestamp. This replaces v1's repeated nested tree/frame representation.

## Queries for agents

`wkperf query FILE OP [ARGS]` returns targeted evidence instead of a full profile dump. JSON is the default; select `--format md|tsv` for text. `--limit` defaults to 50 and is capped at 1000. Subtree depth defaults to 8; hot-path minimum share defaults to 0.05. JSON includes schema, sources, total count, the limit, and an explicit truncation flag. Text output marks truncation. Tie-breaking is stable.

| Operation | Meaning |
| --- | --- |
| `top --by self\|total` | hottest functions; optional module/category filters |
| `callers FUNC`, `callees FUNC` | weighted immediate edges, samples, and share of function time |
| `paths FUNC` | heaviest complete stacks containing a selected function |
| `stacks PATH_ID` | retrieve an exact canonical stack, including a hotspot's `commonStackID` |
| `subtree FUNC --depth D --min-ms N` | bounded call subtree |
| `hot-path --min-share 0.05` | greedily follow the heaviest child from the roots |
| `by-root` | group sampled paths by root/trigger |
| `find REGEX` | matching functions with inclusive and self time |
| `window FROM TO OP [ARGS]` | restrict another operation to a recording-relative window |
| `diff A B` | per-function self/inclusive deltas, largest absolute change first; invoke as `wkperf query diff A B` or `wkperf query A diff B` |
| `depth` | stack-depth distribution and deepest paths |
| `metrics` | scalar recording metrics and their units |
| `events` | trigger counts and timing aggregates |
| `rootcause KEY` | reproduce a named root-cause measurement |

Selectors accept plain names, stable IDs, `url:line:col`, or `/regex/`. If a selector is ambiguous, the command returns candidates and IDs rather than guessing. Source-mapped originals and generated locations are preserved. Native frames without URLs and anonymous frames are valid selectors through their stable IDs.

```bash
wkperf query recording.json top --by total --limit 8 --format json
wkperf query recording.json callers getAnimations --format md
wkperf query recording.json paths performSyncWorkOnRoot --limit 3
wkperf query recording.json window 2 4 hot-path
wkperf query recording.json depth --format json
```

### MCP

`wkperf mcp` starts a stdio MCP server with tools backed by the same query engine. Connect it as a command-based server; stdout is reserved for JSON-RPC protocol messages and diagnostics go to stderr. Tools accept local recording paths, query arguments, and window bounds. Tool results use the same bounded structured query schema as the CLI. Requests and responses are bounded at 4 MiB, with explicit truncation metadata for cut result detail.

Request IDs must be strings or integer literals; integers retain their exact value. Null, fractional, boolean, array, and object IDs receive `-32600` with a null response ID. An oversized line is discarded through its newline, rejected, and followed by normal processing of later requests. `initialize.serverInfo.version` uses the same installed-module build-info fallback as the version command. Recording and source-map inputs must be regular files; directories, devices, and named pipes are rejected before reading.

```json
{
  "mcpServers": {
    "wkperf": {
      "command": "wkperf",
      "args": ["mcp"]
    }
  }
}
```

The server reads files that the local process can access. Do not expose it to untrusted remote clients or pass proprietary recordings to a remote model without approval.

## Deterministic root-cause reports

The hot path is a greedy inclusive-time chain, not a self-time ranking. Time-by-owner splits sampled self time into app, framework/library, native, and GC. Known framework internals can be recognized even in bundled code; source maps improve classification. Nearest-app attribution identifies the closest app ancestor preceding expensive framework/native work in each sampled path.

Stack-based findings retain their exact paths and reproducing query commands: native layout-related APIs, synchronous React work inside event/microtask intervals, unusual recursion/commit depth, periodic roots, and GC/event overlap. Layout-related API names and temporal overlap do not prove causation. Render/commit estimates are sampled contributions, not React Profiler measurements.

Window triage ranks long tasks, active slow frames, and event bursts. Detailed hot paths are built for the highest-ranked windows; lower-priority rows may have `hotPath: null` to avoid repeated tree construction. Every task/frame still retains its complete heaviest sampled stack, numeric evidence, and exact query command. Windows without sampler evidence say so explicitly.

Stable ordering applies to tree nodes, functions, paths, findings, triggers, selectors, and exports. Equal-weight functions sort by name, URL, line, then column. Reports do not insert the current clock time. Aggregate weights and identities are invariant under paired sample reordering. Inferred untimed spans depend on meaningful export order and are unavailable when chronological ordering cannot be established.

Local reports and emitted prompts are deterministic. Responses from external AI providers are not guaranteed deterministic.

## JSON contracts and errors

Use the `schema` discriminator before decoding a result. `wkperf schema` prints a Draft 2020-12 JSON Schema covering the versioned output envelopes; breaking consumer changes get a new discriminator. JSON is never abbreviated by `--brief` or `--max-bytes`.

| Output | Contract |
| --- | --- |
| Analysis | `wkperf-insights/v3` (unreleased): reports, untimed accounting, per-file errors, optional AI response |
| Query/MCP | `wkperf.query.v1`: `operation`, `status`, `sources`, `command`, `rows`, `total_count`, `limit`, `truncated` |
| Comparison | `wkperf/v1`: baseline/candidate groups, metric/function deltas, verdicts, comparability warnings |
| Audit | `wkperf-audit/v2` (unreleased): scene scorecards, priorities, overall scorecard, per-file errors |
| Flame | `wkperf-flame/v2` (unreleased): shared frames, flat profile/delta nodes, exact sample/path references, normalization metadata |
| Summary | `wkperf-summary/v1`: scalar summary output |
| Error-only result | `wkperf-error/v1`: machine-readable command errors |

An analysis report retains recording identity and active duration, exclusive time breakdown, self/inclusive hotspots, file modules, slow frames, long tasks, idle wakeups, trigger tables, findings, and root-cause evidence. Its `frames` array stores generated identity, URL/line/column, category, native/anonymous flags, and optional original location once. Hotspots use a zero-based `frame` index, retain weighted self/inclusive times and sample counts, and link caller/callee frame indices. Source-map enrichment never changes generated identity.

Findings retain `id`, `severity`, `confidence`, human-readable `evidence`, `numericEvidence`, `metricKeys`, `verify`, and `suggestedWindows`. Numeric evidence contains `metricKey`, `value`, `unit`, `description`, `basis`, and a reproducing `command`. Verification entries carry metric key, direction, rough target, unit, explanation, and command. Windows contain `fromSec`, `toSec`, `reason`, and reusable flags. Supporting `functions`, `triggers`, and layout `nodes` preserve measured names and CSS paths when available. `rootCause` includes hot path, sampled owners, nearest-app attribution, exact-stack findings, periodic roots, GC overlaps, depth outliers, and ranked windows. Windows retain their heaviest full sampled stack or an explicit no-samples status.

Exact selected paths live once in the report's `stacks` map, keyed by stable `n_` selectors. Each entry has root-to-leaf frame indices and full `depth`; occurrences use `stackId` or `stackIds` plus their own numeric observations. Canonical and raw findings reference the same paths. Detailed lists honor `--top` and carry explicit totals and retrieval commands, so a bounded default report does not pretend omitted evidence is absent. Text and HTML use shared stack references and root/leaf previews; query retrieves full stacks.

When structured lists are capped, `moreEvidenceCommand` reruns `analyze` with a sufficient `--top` value and preserves selection, maps, thresholds, and module grouping. Use it for the remaining structured rows; existing `query stacks`, `query paths`, and scalar queries retrieve exact selected evidence without inventing a list operation.

Query rows use explicit millisecond fields (`self_ms`, `total_ms`, deltas), `samples`, and `percent` in percentage points. `stack` is root-to-leaf. Ambiguous selectors include `candidates`; they do not return a guessed result. Caller/callee percentages use the selected function's inclusive sampled time as denominator. Sampled owner costs are self time, so their sum does not double-count callers.

Errors contain `message`, `file`, and `code`. With JSON output, failures appear in the single stdout JSON document as well as human-readable stderr diagnostics. Multi-file `analyze` and `audit` keep successful results, list failed files, and exit 1 after processing the group. `compare` and `summary` may stop at the first bad input. Treat a nonempty `errors` array as incomplete evidence even when useful reports exist.

For bounded agent context, use `--brief` or `--max-bytes N` with Markdown or terminal output. Priority is recording identities and errors, all finding summaries, hot path, top functions, verification, then details. The final truncation marker counts toward the limit. Repeated stacks are referenced rather than expanded at every occurrence; use their query commands to retrieve full context.

## Scorecard

Each available standard is rated `good`, `needs-improvement`, or `poor`; missing evidence is `n/a`. The numeric score is a weighted mean of available standards. Overall grade is unavailable when measured standards cover less than half the total weight, or all three frame standards are unavailable. A retained numeric value is then a partial score, not a claim of a healthy UI. Otherwise grades are A (90+), B (75+), C (60+), D (40+), and F. Targets are not Lighthouse or Core Web Vitals scores: TBT is normalized per 10 s, INP is a handler-time approximation, FCP/LCP need load records, and CLS is not measured.

Frame p95, jank ratio, and FPS require at least 30 active frames and 1,000 ms of active-frame observation. `frames.active_ms` exposes the latter. Fixed 60 Hz standards always use 16.7 ms; `--jank-ms` changes finding thresholds, not those standards. Audit score gates cannot pass an unavailable overall grade.

Scorecards expose `coverage` and `partial`. A recording's coverage is available standard weight divided by total standard weight. Audit aggregation uses mean child coverage rather than combining disjoint measurements into fictional complete coverage; any unavailable or missing child keeps the aggregate partial. Failed requested inputs also prevent a conclusive overall grade.

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
| Main-thread GC pause share | wkperf heuristic | `gc-share` | ≤ 3% | > 10% | 1 |
| Memory growth | wkperf heuristic | `memory-growth` | ≤ 1 MB/min | > 10 MB/min | 1 |
| Forced layouts per second | wkperf heuristic | `forced-layout-rate` | ≤ 0.5 /s | > 5 /s | 1 |

Collector intervals alone leave `gc-share` unavailable. Their coverage and overlap remain visible as diagnostics, not evidence of equivalent main-thread blocking.

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
| 1 | input or runtime error, including partial success when some analyze/audit files fail; valid results are still emitted |
| 2 | `--fail-on-regression` and a regressed metric with confidence other than `low` in the frames, script, layout, or cpu groups |
| 3 | `--strict-fixture` and the comparability score is below `--min-comparability` |
| 4 | `audit --min-score N` and the overall score is below N or cannot be graded with available evidence |

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
