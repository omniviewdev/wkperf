# wkperf v0.4.0 bug report — flame exports, source maps, determinism, MCP, query operations (area F)

Analyst report for the fixing agent. Nothing in `cmd/`, `internal/`, tests or docs was modified. Only this file and the `f-*` fixtures listed below were added.

## Scope, environment and method

- Binary: installed `wkperf` (`wkperf version` → `wkperf v0.4.0`), same output as `go build` of HEAD `392facf` (byte-identical folded output checked). macOS 25.6 arm64, Go 1.27.3.
- v0.3.0 comparison: built `v0.3.0` (`d9bdc33`) in a throwaway worktree. v0.3.0 has only `analyze`, `audit`, `compare`, `sample`, `summary`, `version`; it has no `flame`, `query`, `mcp`, `--sourcemap` or `--format folded`. Every bug below is therefore **new in v0.4.0**; "v0.3.0 behaved differently" only in the sense that the feature did not exist.
- Tools used for independent validation: `jq`, Python 3 with `jsonschema` (Draft 2020-12 and Draft 7), `xmllint`, the official speedscope file-format schema (`https://www.speedscope.app/file-format-schema.json`), speedscope 1.25.0 web app (npm) served locally, esbuild 0.24.0 (`npx`), headless Chromium via the eval `browser` global, a hand-written newline-framed JSON-RPC client for `wkperf mcp`, and an independent Python re-implementation of the call-tree aggregates (`top`, `callers`, `callees`, `paths`, `stacks`, `depth`, `by-root`, `hot-path`).
- **BUG-S1 caveat (out of scope, known).** v0.4.0 drops samples whose timestamp is outside `[startTime,endTime)` (WebKit writes `timestamp: 0` for most samples). Independently re-counted: `fresh-record` keeps 38/159 samples (313.009 ms of 434.009 ms), `pods-scrolling-to-failure` 185/591 (261.498 ms of 667.498 ms), `rapid-scroll-deployment` 229/2282 (357.151 ms of 2410.151 ms), `pods-not-moving` 45/166 (101.183 ms). All real-profile numbers below refer to the kept subset; wkperf matched my independent computation of that subset exactly. Where S1 changes a conclusion it is stated. To estimate post-S1 sizes, I re-timed the untimed samples of two real profiles in a /tmp copy (`*-alltimed.json`); those numbers are labelled "simulated".

### Fixtures added for this report (`docs/bug-reports/v0.4.0/fixtures/`)

| File | What it is |
| --- | --- |
| `f-tiny.json` | Hand-written flat recording, 7 samples, 3 script records, known answers (below). Regenerate: `python3 docs/bug-reports/v0.4.0/fixtures/f-gen-tiny.py /tmp/t.json` (byte-identical). |
| `f-tiny-b2.json` | Variant for diffs: samples whose leaf is `B` weigh ×2, samples whose leaf is `C` removed, new function `D` (7 ms). `f-gen-tiny.py OUT --scale-b 2 --drop-c --add-d`. |
| `f-tiny-2x.json` | Same per-second workload, recording twice as long (samples duplicated at +10 s). `jq '.endTime += 10 \| .samples[0].stackTraces += (.samples[0].stackTraces \| map(.timestamp += 10)) \| .samples[0].durations += .samples[0].durations' f-tiny.json` |
| `f-gen-tiny.py` | Generator for the three files above (`--shuffle SEED`, `--wrapped` also available). |
| `f-shuffle.py` | `python3 f-shuffle.py IN OUT SEED` shuffles `(stackTraces[i], durations[i])` pairs together and shuffles `records`. |
| `f-gen-deep.py` | `python3 f-gen-deep.py OUT [SAMPLES=120] [DEPTH=400]`: 120 timestamped samples, 400-frame React-like recursion, distinct leaves (7 MB output, not committed). |
| `f-sourcemap/` | `src/app.js` (original), `dist/assets/index-abc.js` + `.map` built with `npx -y esbuild@0.24.0 src/app.js --bundle --minify --sourcemap --outfile=dist/assets/index-abc.js --format=iife`, and `f-sourcemap-wails.json`, a wrapped export whose frames point at `wails://wails.localhost/assets/index-abc.js` with WebKit-style 1-based function-start columns. |

Known answers for `f-tiny.json` (computed by hand, all confirmed against wkperf): total 80 ms; self ms `getAnimations 30, C 21, B 20, A 9, main 0`; inclusive ms `main 80, B 51, A 50, getAnimations 30, C 21`; `callers B` = `main 30, A 21`; `callees A` = `B 21, C 20, A 4` (recursion); `callees B` = `getAnimations 30, C 1`; full paths `main>B>getAnimations 30, main>A>B 20, main>A>C 20, main>A 5, main>A>A 4, main>A>B>C 1`; depth `2:5, 3:74, 4:1`; `hot-path` (5%) = `main>A>B`; `window 2.5 6` total 45 ms; events: timer `1` 80 ms (C self 20), dispatch `click` ×2 72 ms (getAnimations self 30, B self 10); longest tasks 80 ms timer then 60 ms click.

---

## BUG-F1: Query and MCP results are not usefully bounded; MCP drops every row above 4 MiB instead of truncating

- **Status**: NEW
- **Severity**: high — default `stacks`/`paths`/`depth` calls on real React profiles return 0.1–3.5 MB per call (≈ tens of thousands to ~900k tokens), far beyond what an LLM agent can ingest; above 4 MiB the MCP agent gets no rows at all, contradicting the documented truncation behaviour.
- **Version**: v0.4.0 (feature absent in v0.3.0).
- **Summary**: The only bound is a row count (`limit`, default 50). Rows embed full 300–400-frame stacks with one ~200-byte object per frame, every row carries ~20 unrelated zero fields, and MCP sends each result twice (text + `structuredContent`). When the encoded MCP response reaches 4 MiB the server replaces the entire result with an error, so no partial evidence and no truncation metadata reach the agent.
- **Reproduce** (repo root):

```bash
FX=docs/bug-reports/v0.4.0/fixtures
wkperf query $FX/real-wrapped-fresh-record.json stacks | wc -c          # 388939 bytes for 37 rows, longest stack 109 frames
mcp() { printf '%s\n' '{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18"}}' '{"jsonrpc":"2.0","method":"notifications/initialized"}' "{\"jsonrpc\":\"2.0\",\"id\":2,\"method\":\"tools/call\",\"params\":{\"name\":\"$1\",\"arguments\":$2}}" | wkperf mcp | tail -1; }
mcp stacks "{\"file\":\"$FX/real-wrapped-fresh-record.json\"}" | wc -c   # 581223 bytes (38 samples in the recording!)
mcp depth  "{\"file\":\"$FX/real-wrapped-fresh-record.json\"}" | wc -c   # 81963 bytes
python3 $FX/f-gen-deep.py /tmp/f-deep.json
mcp stacks '{"file":"/tmp/f-deep.json"}'
mcp stacks '{"file":"/tmp/f-deep.json","limit":10}' | wc -c           # 2506467 bytes for 10 rows
mcp paths '{"file":"/tmp/f-deep.json","selector":"leaf000"}' | jq -c '{bytes:(tostring|length), rows:(.result.structuredContent.rows|length)}'
```

Observed for the default deep `stacks` call:

```
{"id":2,"jsonrpc":"2.0","result":{"content":[{"type":"text","text":"Query response exceeds the 4 MiB protocol bound; reduce limit or narrow the window/selector. No rows were returned."}],"isError":true}}
```

and `{"bytes":251380,"rows":1}` for a single path. On the real 10 MB profile (`pods-scrolling-to-failure.json`, kept subset) default MCP sizes were: `stacks` 3,470,158 B, `depth` 263,743 B, `events` 142,823 B, `top` 65,146 B, `metrics` 49,341 B; `stacks --limit 100` → the "No rows were returned" error. CLI `query … subtree /./ --depth 400 --limit 1000` on `rapid-scroll-deployment.json` printed 61,567,543 bytes. Expected: every default call fits an agent budget (tens of KB), over-budget results are trimmed with explicit metadata (as `docs/REFERENCE.md:262` promises: "explicit truncation metadata for cut result detail"), never replaced by an empty error.

- **Root cause** [VERIFIED by code reading and the measurements above]:
  - `internal/mcp/server.go:258-259` `writeResult`: if the whole JSON-RPC payload is ≥ `MaxResponseBytes` (4 MiB) it is replaced by `failedCall(...)`; nothing trims rows. `internal/mcp/bounds_test.go` `TestResponseByteBound` codifies this all-or-nothing behaviour.
  - `internal/mcp/server.go:206`: the same result is serialized into `content[0].text` and again as `structuredContent` (measured text == structured size, response ≈ 2.1× the result).
  - `internal/query/operations.go:156` (`pathRows`, used by `stacks` and `paths`) and `:190` (`subtreeRows`) attach `stack(...)` — one full `Function` object per frame (`id` 66 chars, `url`, `module`, `category`, `source_id`, ~200 B) for every frame of every row; `engine.go:207` adds the full deepest stack to `depth`.
  - `internal/query/types.go:88-116` `Row` is a union whose op-specific fields (`baseline_*`, `candidate_*`, `delta_*`, `value`, `count`, `mean_ms`, `p95_ms`, `max_ms`, `top_*_total_count`, `functions_truncated`) have no `omitempty`, so a `top` row is ~617 B of which most are zeros. (The semantic problem of this union is reported separately as BUG-O7 in `02-output-contract.md`; here it matters only as a size contributor.)
  - `internal/query/render.go:16` indents CLI JSON (+40% vs compact on these outputs).
  - `DefaultLimit = 50` (`types.go:13`) bounds rows, not bytes; row size varies from ~0.6 KB to ~125 KB.
- **Impact**: MCP tools `stacks`, `paths`, `depth`, `subtree`, `events`, `window(paths|stacks)` and the matching `wkperf query` CLI operations; analysis "Reproduce" commands that point at `paths`/`stacks`. On real deep React profiles an MCP client either receives megabytes it cannot use or an error with no rows. After BUG-S1 is fixed, real profiles keep 5–10× more samples and distinct paths, so these sizes grow further (simulated all-timed `rapid-scroll-deployment`: 2975 tree nodes vs 1569 today).
- **Proposed fix** (design decision: one byte-budget contract shared by CLI and MCP, trimming instead of failing):
  1. Add `max_bytes` to `query.Request` (CLI `--max-bytes`, MCP argument), default 131072 for both, hard cap 4 MiB. After sorting and the row limit, `Engine.Run` encodes compact JSON and drops rows from the end until the result fits; set `truncated: true`, add `truncated_by: "limit"|"bytes"` and `returned_count`. MCP budgets the result (the text copy doubles it; account for that rather than removing the text block, because MCP 2025-06-18 says tools returning structured content SHOULD also return the serialized JSON as text).
  2. Never return an empty error for size: if a single row exceeds the budget, elide its stack (below) and mark it.
  3. Bound stacks independently: `max_stack_frames` (default 48: first 8 root frames + last 40 leaf-side frames, with one `{"elided": N}` marker); full stacks remain available through `stacks PATH_ID --max-stack-frames 0`.
  4. In a new `wkperf.query.v2` schema: stacks become arrays of function IDs plus one result-level `functions` dictionary, and op-specific `Row` fields become `omitempty` (today they are `required` in `ResultSchema`, so this is a breaking change and needs the discriminator bump; coordinate with BUG-O7 and the frame-shape items of BUG-O11). Steps 1–3 are non-breaking and should land first.
  - Rejected: only raising/lowering the 4 MiB constant (still all-or-nothing); relying on `limit` (row size varies by 200×); dropping the text block (spec SHOULD).
- **Regression test**: `internal/mcp`: build an in-memory recording equivalent to `f-gen-deep.py` (120 samples × 400 frames) through `query.Engine{Loader: …}`, call `stacks` with default arguments, assert `isError == false`, `len(rows) >= 1`, `truncated == true`, `truncated_by == "bytes"`, and encoded response ≤ 2×budget + 1 KiB. `internal/query`: `paths leaf000` returns ≤ budget with an elided stack; `stacks <id> --max-stack-frames 0` returns all 400 frames. Replace `TestResponseByteBound`'s "explicit error" expectation with the trimming expectation.
- **Notes/uncertainty**: token estimates assume ~4 bytes/token [INFERENCE]. MCP clients commonly cap tool output far below 1 MB [INFERENCE; not tested against a real client].

## BUG-F2: Source-map names for arrow and anonymous functions are parameter names (`props`, `item`, `r`)

- **Status**: NEW
- **Severity**: high — for minified builds the "original" name shown in flame graphs, query rows and analysis is often the first parameter's name, so an agent searches the source for `props` instead of `PodRow`; `query find PodRow` returns nothing.
- **Version**: v0.4.0 (no `--sourcemap` in v0.3.0).
- **Summary**: `sourcemap.Lookup` takes the `name` of the greatest-lower-bound segment at the frame's generated column. WebKit frame columns are the 1-based start of the parameter list, so for `x=>…` (the minified form of `const PodRow = (props) => …`) the segment at that column is the parameter, whose `names` entry is `props`. Anonymous generated frames also receive parameter names. Function declarations (`function t(`) resolve correctly because the name token precedes `(`.
- **Reproduce**:

```bash
FX=docs/bug-reports/v0.4.0/fixtures/f-sourcemap
M=$FX/dist/assets/index-abc.js.map
wkperf query $FX/f-sourcemap-wails.json top --sourcemap $M --format json | jq -r '.rows[] | "\(.name)@\(.line):\(.column) -> \(.original.name // "<none>") \(.original.line // "-"):\(.original.column // "-")"'
wkperf query $FX/f-sourcemap-wails.json find PodRow --sourcemap $M | jq -c '{status,total_count}'
wkperf flame $FX/f-sourcemap-wails.json --sourcemap $M --format svg | grep -o 'class="frame-label"[^>]*>[^<]*' | sed 's/.*>//' | sort -u
```

Actual:

```
(anonymous)@1:28 -> item 2:27
n@1:13 -> props 1:24
t@1:59 -> Table 4:17
(anonymous)@1:83 -> r 5:20
map@1:11 -> <none> -:-
{"status":"ok","total_count":0}
(root) Table item map props r
```

Expected: `n` → `PodRow` (`src/app.js:1:14`), `t` → `Table`, both anonymous arrows → `(anonymous)` with their original location, `find PodRow` → 1 row. Generated code (`dist/assets/index-abc.js`): `var n=e=>e.items.map(o=>o.name)…;function t({rows:e}){return e.map(o=>n({items:o})).length}`. Decoded mappings: generated col 11 (1-based) `n` → name `PodRow`; col 13 `e` → name `props`; col 28 `o` → `item`; col 58 `t` → `Table`; col 83 `o` → `r`.

- **Root cause** [VERIFIED for the lookup mechanism; column convention VERIFIED for function declarations, INFERENCE for methods and unparenthesized arrows]: `internal/sourcemap/sourcemap.go:201-209` (`Map.Lookup`): `i := sort.Search(...) - 1` picks the last segment with `generated <= column-1` and copies `m.Names[s.name]`. Evidence for the column convention from the real export `rapid-scroll-deployment.json`: for 254 of the named functions in `index-GgPenX8q.js` (the bundle itself is not in the export), `column - len(name)` is exactly 10 or 12, which matches `function NAME(` at indentation 0 or 2 with the column on the `(` that opens the parameter list (`commitRoot` col 22, `performWorkOnRoot` col 29); the "method" reading of the remaining values (3, 5, 7, …) and the line 1 col 11 of JS builtins (`map`, `forEach`; consistent with JSC builtin sources starting `(function (`) are [INFERENCE]. For unparenthesized arrows the parameter list starts at the parameter token [INFERENCE from the same convention; not observed directly because the captured Omniview bundle is not minified]. In every JS function form the token at the parameter-list start is `(` or the first parameter, never the function name. Secondary: `internal/model/model.go:127-134` drops `expressionLocation`, so call-site based naming is impossible today.
- **Impact**: every output that displays `Original.Name`: flame HTML/SVG/folded/speedscope labels (`report/flame/flame.go` `frameName`, `internal/flame/frame.go` `FrameLabel`), query rows (`original.name`), `find`/name selectors (`query/selector.go` matches `Original.Name`), analysis hotspots and root-cause text. Function IDs are unaffected (generated identity).
- **Proposed fix** (minimal, never-wrong rule, simulated on three esbuild bundles): in `Resolve`, for a frame with generated column `c`:
  1. Location: keep the current greatest-lower-bound location.
  2. Name: only if the generated frame name is non-empty, take the name of the nearest segment with `generated < c-1` (strictly before the parameter-list start); if that segment has no name, set no original name. Never take the name of a segment at exactly `c-1`. When a name is taken, also use that segment's original location (it points at the declaration name, e.g. `1:14`).
  - Simulation of this rule against the three test bundles (`computeLayout/renderRows/main`, `handleScroll/measureRow/layoutRows`, `PodRow/Table`) gave the correct name or no name in all 12 frames, versus 5 wrong parameter names with the current rule (`event`, `props`, `item`, and `r` twice).
  - Rejected: naming via the caller's `expressionLocation` (Sentry-style) — attractive, but JSC's call divot column is not verified here; scanning original `sourcesContent` for the declaring identifier — more code and needs content. Both can be added later; keep `expressionLocation` in the model for that and for hot-line reporting.
- **Regression test**: `internal/sourcemap` table test using `f-sourcemap/dist/assets/index-abc.js.map` through `Resolve` on `f-sourcemap-wails.json`: `n@1:13 → PodRow`, `t@1:59 → Table`, `(anonymous)@1:83` and `@1:28` → no `Name`, and `query find PodRow` (in `internal/query`) → 1 row.
- **Notes/uncertainty**: The current Omniview capture uses an unminified bundle (names intact), so today's real profiles are unaffected; the bug matters as soon as a minified build plus `--sourcemap` is used, which is the documented purpose of the flag.

## BUG-F3: `flame --format json` is 6–9× larger than its content (indentation of deep trees, frames repeated per sample)

- **Status**: NEW
- **Severity**: medium — the versioned machine contract `wkperf-flame/v1` is 1.77 MB for a 388 KB input with 38 samples and 24–30 MB for the real 10/50 MB profiles (87 MB simulated after BUG-S1); agents and tools cannot load it cheaply.
- **Version**: v0.4.0 (new).
- **Summary**: JSON is pretty-printed with two-space indentation, which for 110–373-level-deep nested `children` makes whitespace the dominant content. Each sample also repeats its full frame objects, and the tree repeats them again per node.
- **Reproduce**:

```bash
R=docs/bug-reports/v0.4.0/fixtures/real-wrapped-fresh-record.json
wkperf flame $R --format json | wc -c              # 1767848
wkperf flame $R --format json | jq -c . | wc -c    # 307582 (same document, compact)
wkperf flame $R --format speedscope | wc -c        # 34781 (same samples/weights/frames)
```

Real profiles (kept subset): `pods-scrolling-to-failure` 30,374,924 B indented vs 3,661,046 B compact (samples alone 3,051,955 B for 185 samples); `rapid-scroll-deployment` 24,067,213 B vs 2,794,324 B. `flame --diff pods-scrolling-to-failure.json rapid-scroll-deployment.json --format json` = 89,461,144 B. Simulated all-timed `rapid-scroll-deployment`: 87,405,416 B. Expected: size proportional to distinct frames + nodes + samples (speedscope shows ~35 KB is achievable for the small fixture).
- **Root cause** [VERIFIED]: `internal/report/flame/flame.go:63-65` uses `enc.SetIndent("", "  ")` on `Document`; `internal/flame/types.go` `Sample.Frames []Frame` and `Node.Frame Frame` embed complete frame objects (`name,url,line,column,sourceID,native,anonymous,category,original`) in every sample and node; diff documents embed `before`, `after` and `comparison` trees.
- **Impact**: `wkperf flame --format json` (profile and diff); any consumer of `wkperf-flame/v1`.
- **Proposed fix**: (1) non-breaking, now: encode compact JSON (drop `SetIndent`) — 5.7× smaller on the small fixture, 8.3× on the 10 MB profile. (2) In `wkperf-flame/v2`: a `frames` table (indexed by function ID), nodes as `{id, parent, frame, selfMs, totalMs, samples}` in a flat array, samples as `{t, w, path}` referencing path/leaf node IDs. Rejected: gzip-only (still unusable to text consumers), dropping samples (needed for re-windowing).
- **Regression test**: `internal/report/flame`: render JSON for `real-wrapped-fresh-record.json` and assert size < 400 KB and that it still validates against `wkperf schema` (the current output validates: 0 errors).
- **Notes/uncertainty**: none.

## BUG-F4: Folded output uses fractional millisecond counts and non-standard backslash escapes; speedscope cannot import it

- **Status**: NEW
- **Severity**: medium — the collapsed-stack export does not interoperate with speedscope at all, and names containing spaces are altered in every standard consumer; agents piping folded stacks into common tooling get nothing or corrupted labels.
- **Version**: v0.4.0 (new; `analyze --format folded` shares the code and output).
- **Summary**: Weights are printed as `strconv.FormatFloat(ms,'f',-1)` (e.g. `18.35297918296419`). speedscope's collapsed-stack importer only accepts integer counts (`/^(.*) (\d+)$/`), so the file is rejected. Labels escape spaces, `;`, tabs and newlines with backslashes; no folded consumer unescapes them, so `bound performWorkOnRootViaSchedulerTask` is shown as `bound\ performWorkOnRootViaSchedulerTask`, and mapped labels become `Table@…/app.js:4:17\ [generated\ t@…]`.
- **Reproduce**:

```bash
R=docs/bug-reports/v0.4.0/fixtures/real-wrapped-fresh-record.json
wkperf flame $R --format folded | wc -l                    # 37
wkperf flame $R --format folded | grep -cE ' [0-9]+$'      # 0  (no line has an integer count)
wkperf flame $R --format folded | awk '{print $NF}' | head -1   # 18.35297918296419
wkperf flame $R --format folded | grep -o 'bound\\ [A-Za-z]*@native' | head -1   # bound\ performWorkOnRootViaSchedulerTask@native
```

Loading that file in speedscope 1.25.0 (`index.html#profileURL=…/small.folded`) leaves the welcome screen (import failed). The same lines with counts converted to integer microseconds load ("Importing as collapsed stack format") and display the frame as `bound\ performWorkOnRootViaSchedulerTask@native:4294967295:-1:-1`. speedscope source `src/import/bg-flamegraph.ts:14`: `/^(.*) (\d+)$/gm`, then `stack.split(';')`; Brendan Gregg's `flamegraph.pl:651` accepts decimals (`(\d+(?:\.\d*)?)`) but also splits on every `;` and shows backslashes. Expected: integer weights in a documented unit and labels that need no escape decoding.
- **Root cause** [VERIFIED]: `internal/flame/folded.go:9` (`foldedEscapes`, also `"\\"→"\\\\"`, `" "→"\\ "`), `folded.go:22` (`weightString`), `internal/report/flame/flame.go:122` (same replacer for multi-input recording-name roots); `FoldedDiff` uses the same helpers for its two columns.
- **Impact**: `flame --format folded`, `flame --diff --format folded`, `analyze --format folded`, multi-input folded roots.
- **Proposed fix**: emit integer microseconds (`int64(math.Round(ms*1000))`) and document the unit in `docs/REFERENCE.md:216,227` ("collapsed stacks weighted in µs"); for diff output keep two integer columns. Stop escaping spaces and backslashes; replace only characters that break the format — `;` → `%3B`, `\n` → `%0A`, `\r` → `%0D`, `\t` → `%09` — and document that encoding. Rejected: sample counts as weights (loses WebKit's duration weights); keeping ms with a decimal point (speedscope rejects); keeping backslash escapes (no consumer decodes them). The rounding loses < 0.5 µs per line; assert the per-line sum matches `round(total*1000)` within line count.
- **Regression test**: `internal/flame`: for `real-wrapped-fresh-record.json` every folded line matches `^(.*) ([0-9]+)$`, a frame name with a space is emitted verbatim, a name containing `;` is emitted as `%3B`, and the sum of counts equals `round(Root.TotalMs*1000) ± lines`. Same for `FoldedDiff` with two integer columns.
- **Notes/uncertainty**: changing the unit is a visible format change; mention it in the changelog.

## BUG-F5: `flame --focus FUNC` fails whenever the function has more than one calling context

- **Status**: NEW
- **Severity**: medium — the documented "focus a selected function subtree" is unusable for exactly the functions an agent wants to focus in React profiles (recursive commit/render functions have 78–80 contexts), and the error lists dozens of identical labels with 66-char IDs.
- **Version**: v0.4.0 (new).
- **Summary**: A function selector is resolved to all of its calling-context nodes; the command errors unless exactly one node matches. Recursion (`A` inside `A`) or two callers are enough to fail. There is no "merge all contexts of this function" focus.
- **Reproduce**:

```bash
FX=docs/bug-reports/v0.4.0/fixtures
wkperf flame $FX/f-tiny.json --focus A --format folded; echo rc=$?
wkperf flame $FX/real-wrapped-fresh-record.json --focus commitPassiveMountOnFiber --format folded 2>&1 | cut -c1-150
```

Actual:

```
wkperf: docs/bug-reports/v0.4.0/fixtures/f-tiny.json: focus selector "A" matches 2 contexts; choose a node ID: n_91aee0df…2840d A@file:///app/app.js:10:5; n_eaee4083…84c4a A@file:///app/app.js:10:5
rc=1
wkperf: …real-wrapped-fresh-record.json: focus selector "commitPassiveMountOnFiber" matches 80 contexts; choose a node
```

(the second error message is 11,865 bytes; `recursivelyTraversePassiveMountEffects` gives 78 contexts, `commitRoot`, `performWorkOnRoot`, `renderWithHooks` 2 each). Expected for `--focus A` on `f-tiny.json` (A inclusive = 50 ms, recursion counted once):

```
A@file:///app/app.js:10:5 5
A@file:///app/app.js:10:5;A@file:///app/app.js:10:5 4
A@file:///app/app.js:10:5;B@file:///app/app.js:20:5 20
A@file:///app/app.js:10:5;B@file:///app/app.js:20:5;C@file:///app/lib.js:5:3 1
A@file:///app/app.js:10:5;C@file:///app/lib.js:5:3 20
```

(today this exact output is produced only by `--focus n_eaee4083571dc363cd6c6d3422d7baacec10302ff8b6a54ffa4e681ba2184c4a`).
- **Root cause** [VERIFIED]: `cmd/flame.go:186-195` `focusFlameTree` requires `len(nodes) == 1` from `query.ResolveNodes`; `internal/flame/tree.go` `FocusNode` supports one exact context only; `cmd/flame.go:197-204` lists node IDs with identical `FrameLabel`s, so candidates are indistinguishable. `focusFlameDiff` (`cmd/flame.go:206-225`) has the same restriction.
- **Impact**: `wkperf flame --focus` (all formats) and `--diff --focus`; the `docs/REFERENCE.md:210,217` example and description.
- **Proposed fix**: add `Tree.FocusFunction(functionID)`: keep samples containing the function and cut each at its **outermost** occurrence (`Frames[firstIndex:]`; for inverted trees the last index), so recursion is not double counted; the resulting root total equals `Function.TotalMs`. In `focusFlameTree`: node-ID selectors keep `FocusNode`; selectors resolving to exactly one function use `FocusFunction`; only selectors matching several *functions* error, listing function IDs with `url:line:col` and total ms. Apply the same to `--diff` (focus by function ID in both trees). Rejected: picking the heaviest context (silently discards time).
- **Regression test**: `cmd/flame_test.go`: `--focus A` on `f-tiny.json` yields exactly the five folded lines above (sum 50); `--focus B` yields `B;getAnimations 30`, `B 20`, `B;C 1` (sum 51); a regex matching `A` and `B` errors with two function candidates.
- **Notes/uncertainty**: none.

## BUG-F6: `query diff` and `flame --diff` compare raw sampled milliseconds with no normalization or duration context

- **Status**: NEW
- **Severity**: medium — in the "fix, re-record, verify" loop, a candidate recording that is simply longer shows every function as a regression (and a shorter one as an improvement); the query result does not even carry the baseline total, so the agent cannot detect it.
- **Version**: v0.4.0 (new; `compare` already handles this case).
- **Summary**: Deltas are `candidate − baseline` in absolute sampled ms. Recordings of different active duration or sampling coverage are not normalized, no comparability warning is emitted, and `query diff` reports only the candidate total.
- **Reproduce**:

```bash
FX=docs/bug-reports/v0.4.0/fixtures
wkperf query diff $FX/f-tiny.json $FX/f-tiny-2x.json --format tsv | tail -5
wkperf query diff $FX/f-tiny.json $FX/f-tiny-2x.json | jq -c '{total_ms, sources}'
wkperf flame --diff $FX/f-tiny.json $FX/f-tiny-2x.json --format json | jq -c '.charts[0] | {b:.before.activeDurationMs, a:.after.activeDurationMs, c:(.comparison|{beforeTotalMs,afterTotalMs,totalMs})}'
wkperf compare $FX/f-tiny.json $FX/f-tiny-2x.json | grep duration-mismatch
```

Actual (same per-second workload on both sides):

```
…getAnimations …	30	60	30	30	60	30	1
…C …	21	42	21	21	42	21	2
…B …	20	40	20	51	102	51	4
…A …	9	18	9	50	100	50	6
…main …	0	0	0	80	160	80	7
{"total_ms":160,"sources":[{"file":"…/f-tiny.json","from":0,"to":10},{"file":"…/f-tiny-2x.json","from":0,"to":20}]}
{"b":10000,"a":20000,"c":{"beforeTotalMs":80,"afterTotalMs":160,"totalMs":80}}
    · [duration-mismatch] capture durations differ: baseline 10.0s vs candidate 20.0s (+100%); totals and counts were compared per second
```

The flame diff HTML/SVG colours every frame red. Expected: a duration/coverage mismatch warning and normalized deltas (or at least both totals and durations so the agent can normalize).
- **Root cause** [VERIFIED]: `internal/query/engine.go:156-168` (diff branch) sets `result.TotalMS = clean(b.Root.TotalMs)` (candidate only) and calls `diffRows` (`operations.go:209+`), which subtracts raw `SelfMs`/`TotalMs`; `internal/flame/diff.go` `Diff` likewise. The comparability/normalization logic of `internal/compare` (`--normalize auto`) is not reused.
- **Impact**: `wkperf query diff`, MCP `diff` tool, `window … diff`, `flame --diff` (all formats and colours).
- **Proposed fix**: add `baseline_total_ms`, `candidate_total_ms`, `baseline_active_ms`, `candidate_active_ms` to the diff result and `warnings` reusing compare's `duration-mismatch` (>10% active-duration difference) and `sample-sparse` checks; add `--normalize none|per-second|share` (default `auto` like `compare`: per-second when durations differ by >10%) applied to the `delta_*` columns, keeping raw `baseline_*`/`candidate_*`; flame diff colours use the normalized delta and the HTML/SVG legend states the mode. Rejected: silently normalizing without exposing raw values (agents need both).
- **Regression test**: `internal/query`: `diff f-tiny.json f-tiny-2x.json` → warning `duration-mismatch`, `candidate_total_ms` 160, `baseline_total_ms` 80, and with `--normalize per-second` every `delta_self_ms` = 0; `diff f-tiny.json f-tiny-b2.json` (equal durations) keeps today's values (`C −21`, `B +20`, `D +7`, `A 0`).
- **Notes/uncertainty**: real-data diffs are additionally distorted by BUG-S1 (which samples are kept depends on timestamp coverage); fix S1 first.

## BUG-F7: Source-map diagnostics (missing-map hint, mapped/unmapped counts, warnings) never reach query JSON or MCP

- **Status**: NEW
- **Severity**: medium — an MCP agent cannot tell that names are minified and unmapped (or that a map failed to parse), contrary to `docs/REFERENCE.md:175` ("Without a matching map, minified profiles include a source-map hint").
- **Version**: v0.4.0 (new).
- **Summary**: `sourcemap.Resolve` returns `Diagnostics`, but the query engine discards them. The CLI prints warnings to stderr only; `wkperf.query.v1` has no field for them; the MCP server never sees them.
- **Reproduce**:

```bash
FX=docs/bug-reports/v0.4.0/fixtures/f-sourcemap
wkperf query $FX/f-sourcemap-wails.json top 2>&1 >/dev/null
wkperf query $FX/f-sourcemap-wails.json top 2>/dev/null | jq -c keys
printf '%s\n' '{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18"}}' '{"jsonrpc":"2.0","method":"notifications/initialized"}' "{\"jsonrpc\":\"2.0\",\"id\":2,\"method\":\"tools/call\",\"params\":{\"name\":\"top\",\"arguments\":{\"file\":\"$FX/f-sourcemap-wails.json\",\"limit\":1}}}" | wkperf mcp | tail -1 | jq -c '.result.structuredContent|keys'
wkperf analyze $FX/f-sourcemap-wails.json --format json 2>/dev/null | jq -c '.reports[0].sourceMap'
```

Actual: stderr `…: Likely minified JavaScript lacks source-map coverage; use --sourcemap FILE or DIR …`; query JSON and MCP keys `["by","command","limit","operation","rows","schema","sources","status","total_count","total_ms","truncated"]` (no diagnostics); `analyze` JSON does carry them: `{"mappedFrames":0,"unmappedFrames":11,"missingMaps":["wails://wails.localhost/assets/index-abc.js"],"warnings":["Likely minified JavaScript lacks source-map coverage; …"],"minified":true}` (with `--sourcemap …/index-abc.js.map`: `{"mappedFrames":8,"unmappedFrames":3,"minified":true}`). Expected: the same diagnostics in query/MCP results.
- **Root cause** [VERIFIED]: `internal/query/engine.go:106-117` `defaultLoad` calls `sourcemap.Resolve` and discards the `Diagnostics` (`_`); the CLI loader in `cmd/query.go` (`rec, _, err := loadRecording(cmd, file)`) discards them too and `cmd/input.go:44-46` only prints warnings to stderr; `query.Result` (`internal/query/types.go:121`) has no field.
- **Impact**: `wkperf query --format json|md|tsv`, all MCP tools.
- **Proposed fix**: make `query.Loader` return `(rec, sourcemap.Diagnostics, error)`; add `source_maps: [{file, mapped_frames, unmapped_frames, missing_maps, warnings, minified}]` to `Result` (and `ResultSchema`; the outputSchema has `additionalProperties:false`, so the schema must be updated in the same change); render a one-line note in md/tsv. Rejected: printing to stderr in MCP (not visible to the model).
- **Regression test**: `internal/query`: `top` on `f-sourcemap-wails.json` without maps → `source_maps[0].minified == true`, `unmapped_frames == 11`, missing bundle `wails://wails.localhost/assets/index-abc.js` and the hint warning; with `--sourcemap …/index-abc.js.map` → `mapped_frames == 8`, `unmapped_frames == 3` (the three native `map` frames), no warning (same counts `analyze` reports today). `internal/mcp`: the structured result contains the field and validates against `outputSchema`.
- **Notes/uncertainty**: none.

## BUG-F8: Differential HTML flame labels are unreadable (contrast 1.3–1.5:1)

- **Status**: NEW
- **Severity**: medium — in the interactive diff view most frame names cannot be read, which defeats the main use of a diff flame graph (the static SVG is fine).
- **Version**: v0.4.0 (new).
- **Summary**: In diff mode the JS paints frames with translucent red/green (`alpha = 0.25 … 0.9`) over the dark canvas while label text stays near-black `#0b1220`. For typical small deltas the effective background is almost as dark as the text.
- **Reproduce**:

```bash
FX=docs/bug-reports/v0.4.0/fixtures
wkperf flame --diff $FX/f-tiny.json $FX/f-tiny-b2.json --out /tmp/f-diff.html
grep -n 'const alpha' internal/report/flame/flame.js
```

Open `/tmp/f-diff.html` (headless Chromium, 1400×900): computed button backgrounds `rgba(220, 60, 65, 0.286)` (root, main), `rgba(35, 150, 105, 0.255)` (A), `rgba(220, 60, 65, 0.365)` (B); screenshot shows labels `(root)`, `main`, `A`, `B`, `C` barely visible. WCAG contrast of `#0b1220` text on the composited backgrounds: 1.26:1 (red α .25), 1.49:1 (red α .365), 1.39:1 (green α .255), 3.62:1 even at α .9; the SVG's solid `#dc3c41` gives 4.25:1 and the non-diff app colour `#60a5fa` 7.36:1. Expected: ≥ 4.5:1 for labels.
- **Root cause** [VERIFIED]: `internal/report/flame/flame.js:46-52` (`color`) returns `rgba(…, alpha)` with `alpha = 0.25 + 0.65*min(1,|delta|/weight(root))`; label colour `color:#0b1220` and canvas `background:#0b1220` come from the CSS in `internal/report/flame/html.go:14`.
- **Impact**: `flame --diff --format html` (and any analysis/audit HTML that embeds a diff chart).
- **Proposed fix**: use opaque colours: interpolate between neutral `#718096` and the red/green end colours by `|delta|` share (no transparency), and choose the label colour per node from background luminance (`#0b1220` if relative luminance > 0.18, else `#f8fafc`). Rejected: raising the minimum alpha only (still < 4.5:1 below α ≈ 1).
- **Regression test**: a Go unit test over the colour ramp (port the ramp to Go or test the JS with a tiny harness) asserting WCAG contrast ≥ 4.5 for 0%, 5%, 50%, 100% deltas of both signs.
- **Notes/uncertainty**: none.

## BUG-F9: HTML "Focus selector" matches substrings of names and file paths and picks the first DFS context

- **Status**: NEW
- **Severity**: medium — the HTML focus box silently focuses the wrong frame (typing `A` focuses `main`), unlike CLI `--focus`, which uses exact selectors.
- **Version**: v0.4.0 (new).
- **Summary**: The client-side focus lower-cases the input and tests `includes()` against `frameName + " " + url:line:col (+ original)`, taking the first match in alphabetical depth-first order. Short names match unrelated frames, file-path text matches every frame of that file, and the chosen context is neither the heaviest nor a merge of all contexts.
- **Reproduce**:

```bash
wkperf flame docs/bug-reports/v0.4.0/fixtures/f-tiny.json --out /tmp/f-tiny.html
```

In headless Chromium: type `A` in "Focus selector" and click Focus → summary `80 ms · 100% of sampled time`, breadcrumb `(root) > main` (because `main` contains "a"); `B` → `21 ms`, breadcrumb `(root) > main > A > B` (the 30 ms `main > B` context is ignored; B's inclusive time is 51 ms); `lib.js` → the 1 ms `main > A > B > C` context instead of the 20 ms `main > A > C`. Expected: exact-name / `url:line:col` / `/regex/` semantics like the CLI, focusing all contexts of the function (see BUG-F5) or at least the heaviest.
- **Root cause** [VERIFIED]: `internal/report/flame/flame.js:148-163` `focusSelector`: `…toLowerCase().includes(selector)` and `if (!best …) best = path` during an alphabetical DFS.
- **Impact**: interactive HTML from `flame`, `analyze --format html`, `audit --format html`.
- **Proposed fix**: mirror the CLI selector grammar in JS: exact function name (or original name), `url:line:col`, `/regex/`; collect all matching nodes; if they belong to one function, build a merged focused view (same outermost-occurrence rule as BUG-F5) or, minimally, pick the heaviest context and say "N contexts; showing heaviest" in the status line. Keep the regex search box for highlighting.
- **Regression test**: export the focus logic as a pure function (`selectFocus(tree, text)`) and test it in the existing Go-side HTML tests by asserting the embedded behaviour through a small JS test harness, or move selection to Go (embed per-function context lists) and unit-test in `internal/report/flame`.
- **Notes/uncertainty**: none.

## BUG-F10: HTML flame graph layout is hard to use on deep stacks; every frame has two tooltips

- **Status**: NEW
- **Severity**: low — human usability of the interactive page; numbers are correct.
- **Version**: v0.4.0 (new).
- **Summary**: (a) Frames are anchored to the canvas bottom (root at the bottom). With 110–373-frame React stacks the canvas is 2,752–9,352 px tall, so the page opens on the narrow leaf tips and the root/widest frames are far below the fold. (b) Each JS-rendered frame sets both the native `title` attribute and the custom tooltip on `mousemove`/`focus`, so two tooltips with the same text appear. (c) Each button also carries the full tooltip text as `aria-label`, tripling text per node in the DOM.
- **Reproduce**:

```bash
wkperf flame docs/bug-reports/v0.4.0/fixtures/real-wrapped-fresh-record.json --out /tmp/f-small.html
```

Headless Chromium, 1400×900 viewport: `(root)` button top = 3103 px, canvas height 2752 px, 33 of 411 frames visible at load. For `pods-scrolling-to-failure.json` the canvas is 9352 px. Every rendered `.wk-flame-node` has a non-empty `title` and `aria-label` equal to the custom tooltip text.
- **Root cause** [VERIFIED]: `internal/report/flame/flame.js:92` (canvas height), `:100` (`button.style.bottom = depth*25px`), `:104` (`button.title = describe(...)`), `:105` (`aria-label`), `:107`/`:109` (custom tooltip).
- **Impact**: HTML from `flame`, `analyze`, `audit`.
- **Proposed fix**: (a) render as an icicle (root at top, `top = depth*25px`) or scroll the canvas so the root row is visible on load; (b) remove `title` from JS-rendered buttons (keep it only in the static no-JS markup); (c) shorten `aria-label` to `name, total ms, self ms`. Rejected: capping depth (hides evidence).
- **Regression test**: HTML test asserting the JS sets no `title` on rendered nodes (string check on `flame.js`) and, if icicle is chosen, that the root rect is at `top:0`.
- **Notes/uncertainty**: Performance is acceptable: 42,116 rendered nodes in the inverted simulated all-timed `rapid-scroll-deployment` view took 222 ms to toggle and 1,055 ms with min width 0; no console errors.

## BUG-F11: Recording-adjacent source-map sidecars resolve original paths to locations that do not exist

- **Status**: NEW
- **Severity**: low — names still resolve, but `original.url` points to a nonexistent absolute path, so an agent opening the "original file" fails and must guess.
- **Version**: v0.4.0 (new).
- **Summary**: Relative `sources` (Vite/esbuild write `../../src/app.js`) are joined with the directory of the map file. For a sidecar copied next to the recording (the documented `recordings/index-abc.js.map` discovery), that directory is not where the map was generated.
- **Reproduce**:

```bash
FX=docs/bug-reports/v0.4.0/fixtures/f-sourcemap
rm -rf /tmp/f-sidecar && mkdir -p /tmp/f-sidecar/recordings
cp $FX/f-sourcemap-wails.json /tmp/f-sidecar/recordings/rec.json && cp $FX/dist/assets/index-abc.js.map /tmp/f-sidecar/recordings/
wkperf query /tmp/f-sidecar/recordings/rec.json find '^t$' | jq -c '.rows[0].original'   # {"name":"Table","url":"/tmp/src/app.js","line":4,"column":17}
ls /tmp/src/app.js                                                                       # No such file or directory
wkperf query $FX/f-sourcemap-wails.json find '^t$' --sourcemap $FX/dist/assets/index-abc.js.map | jq -r '.rows[0].original.url'   # …/fixtures/f-sourcemap/src/app.js (correct)
```

Expected: a path that exists, or the map's raw relative source (`src/app.js`) flagged as unresolved.
- **Root cause** [VERIFIED]: `internal/sourcemap/sourcemap.go:422-423` joins every relative resolved URL with `filepath.Dir(map path)`, including maps found by the recording-adjacent sidecar rule (`discover(filepath.Join(filepath.Dir(recordingPath), basename+".map"))`); there is no existence check.
- **Impact**: `original.url` in all outputs when sidecar discovery is used; module grouping by original URL.
- **Proposed fix**: keep the joined path only if it exists on disk; otherwise store the cleaned raw source (strip leading `../`, `./`, `webpack://…/`) and add a diagnostics warning "original source paths relative to <map> do not exist; pass --sourcemap from the build directory". Rejected: always using raw sources (loses correct absolute paths for maps used in place).
- **Regression test**: `internal/sourcemap`: copy the fixture map into a temp dir next to the recording and assert `Original.URL == "src/app.js"` plus the warning; in-place map keeps the absolute existing path.
- **Notes/uncertainty**: none.

## BUG-F12: `--sourcemap DIR` aborts the whole command if any unrelated map in the directory is unsupported

- **Status**: NEW
- **Severity**: low — pointing `--sourcemap` at a build output directory fails if one vendor/CSS map uses an unsupported feature, even when the relevant bundle's map is valid.
- **Version**: v0.4.0 (new).
- **Summary**: Every `*.map` found by walking an explicit directory is treated as an explicit argument, so any parse error is fatal. Error messages from `query` also repeat the recording path twice.
- **Reproduce**:

```bash
FX=docs/bug-reports/v0.4.0/fixtures
rm -rf /tmp/f-maps && mkdir -p /tmp/f-maps && cp $FX/f-sourcemap/dist/assets/index-abc.js.map /tmp/f-maps/
printf '{"version":3,"sections":[{"offset":{"line":0,"column":0},"url":"chunk.js.map"}]}' > /tmp/f-maps/vendor.js.map
wkperf flame $FX/f-sourcemap/f-sourcemap-wails.json --format folded --sourcemap /tmp/f-maps; echo rc=$?
wkperf query $FX/f-sourcemap/f-sourcemap-wails.json top --sourcemap /tmp/f-maps 2>&1 | head -1
```

Actual: `…: source map /tmp/f-maps/vendor.js.map: external source-map section URLs are not fetched; supply an embedded section map`, `rc=1`; the query line reads `wkperf: docs/…/f-sourcemap-wails.json: docs/…/f-sourcemap-wails.json: source map …`. Expected: warn about `vendor.js.map`, resolve with `index-abc.js.map`, exit 0.
- **Root cause** [VERIFIED]: `internal/sourcemap/sourcemap.go:329` `add(p, true)` for every walked file; `:373-374` explicit parse failures return an error. Duplicate prefix: `internal/query/engine.go:127` wraps the loader error with the file name again although the CLI loader's `inputError` already names it.
- **Impact**: `--sourcemap DIR` on all commands; MCP `sourcemaps` argument with a directory.
- **Proposed fix**: mark directory-walk results `explicit:false` (warning + continue, as for adjacent maps), keep explicit *file* arguments strict; optionally parse a walked map only if it matches at least one frame URL (`matchScore > 0`). Remove the second file prefix in `engine.load` when the error already carries the file.
- **Regression test**: `internal/sourcemap`: directory with one valid and one unsupported map → no error, one warning, frames mapped. `cmd` query error text contains the path once.
- **Notes/uncertainty**: none.

## BUG-F13: MCP server dies on an oversized request and hangs forever on a non-regular file

- **Status**: NEW
- **Severity**: low — a single bad call ends or freezes the whole MCP session (no further requests are answered, cancellation is ignored).
- **Version**: v0.4.0 (new).
- **Summary**: A request line longer than 4 MiB makes the scanner fail, `Serve` returns, and the process exits 1 without a JSON-RPC error. A `file` argument naming a FIFO (or any blocking special file) blocks `os.Open`/decode inside the single request loop; later requests, including `ping`, are never answered, and `notifications/cancelled` is not handled.
- **Reproduce**:

```bash
FX=docs/bug-reports/v0.4.0/fixtures
python3 -c "
import json
print(json.dumps({'jsonrpc':'2.0','id':1,'method':'initialize','params':{'protocolVersion':'2025-06-18'}}))
print(json.dumps({'jsonrpc':'2.0','method':'notifications/initialized'}))
print(json.dumps({'jsonrpc':'2.0','id':2,'method':'tools/call','params':{'name':'top','arguments':{'file':'$FX/f-tiny.json','module':'x'*(5<<20)}}}))
print(json.dumps({'jsonrpc':'2.0','id':3,'method':'ping'}))" | wkperf mcp > /tmp/f-out.txt; echo rc=$?; wc -l < /tmp/f-out.txt
# stderr: wkperf: read MCP message (maximum 4194304 bytes): bufio.Scanner: token too long ; rc=1 ; 1 line (initialize only)
# (python may also print BrokenPipeError, because the server exits while the request is still being written)
rm -f /tmp/f-fifo && mkfifo /tmp/f-fifo
(printf '%s\n' '{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18"}}' '{"jsonrpc":"2.0","method":"notifications/initialized"}' '{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"top","arguments":{"file":"/tmp/f-fifo"}}}' '{"jsonrpc":"2.0","id":3,"method":"ping"}'; sleep 6) | timeout 5 wkperf mcp | jq -c '{id}'; echo "exit=${PIPESTATUS[1]}"
# {"id":1}  exit=124  (ids 2 and 3 never answered)
```

Expected: `-32600`-style error for the oversized line (id `null`) and the server continues; a tool error "not a regular file" for the FIFO.
- **Root cause** [VERIFIED]: `internal/mcp/server.go:71` `scanner.Buffer(…, MaxMessageBytes)` and `:217-218` return the scanner error from `Serve`; the request loop executes `engine.Run` synchronously (`server.go` `tools/call` branch) and notifications other than `notifications/initialized` are dropped; `internal/model/model.go:224` `os.Open` + decoder on any path; `internal/sourcemap` opens map paths the same way.
- **Impact**: `wkperf mcp` sessions.
- **Proposed fix**: read lines with `bufio.Reader.ReadSlice`/manual accumulation; on overflow discard to the next newline and reply `{"code":-32600,"message":"Request exceeds 4 MiB"}` with `id:null`, then continue. Before opening a recording or map, `os.Stat` and reject non-regular files (`!Mode().IsRegular()`), in `model.Load` and `sourcemap.Resolve`. Optionally run tool calls in a goroutine with a context cancelled by `notifications/cancelled`.
- **Regression test**: `internal/mcp`: feed initialize, a 5 MiB line, then `ping` → two responses (error with null id, then ping result) and `Serve` returns nil at EOF. `internal/model`: `Load` on a FIFO returns an error immediately (skip on Windows).
- **Notes/uncertainty**: The server reads any file the process can read; this is documented (`docs/REFERENCE.md:275`). Parse errors leak only the first offending character (e.g. `/etc/passwd` → `invalid character '#'`).

## BUG-F14: MCP `serverInfo.version` is always `dev`; `null` and fractional request IDs are accepted

- **Status**: NEW
- **Severity**: low — agents cannot tell which wkperf they are talking to (needed to know output contracts); minor JSON-RPC/MCP conformance gaps.
- **Version**: v0.4.0 (new).
- **Summary**: `wkperf version` prints `wkperf v0.4.0` (from build info), but `initialize` returns `"serverInfo":{"name":"wkperf","version":"dev"}`. Requests with `"id": null` or `"id": 1.5` are treated as requests and answered with that id; MCP requires string or integer IDs and forbids null.
- **Reproduce**:

```bash
printf '%s\n' '{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18"}}' '{"jsonrpc":"2.0","id":null,"method":"ping"}' '{"jsonrpc":"2.0","id":1.5,"method":"ping"}' | wkperf mcp | jq -c '{id, v: .result.serverInfo.version}'
wkperf version
```

Actual: `{"id":1,"v":"dev"}`, `{"id":null,"v":null}`, `{"id":1.5,"v":null}`, `wkperf v0.4.0`. Expected: `"version":"v0.4.0"`; `-32600 Invalid Request` for null/non-integer numeric IDs.
- **Root cause** [VERIFIED]: `cmd/mcp.go:17` passes the raw link-time variable `Version` (empty unless `-ldflags`), and `internal/mcp/server.go:67-68` substitutes `"dev"`; `cmd/version.go` `versionString()` has the build-info fallback that MCP does not use. `validID` in `internal/mcp/server.go` accepts `nil` and any `float64`.
- **Impact**: `wkperf mcp` `initialize` result; protocol conformance.
- **Proposed fix**: factor the resolution in `cmd/version.go` into `resolvedVersion()` (without the "wkperf " prefix) and pass it to `mcp.Server`; in `validID` accept strings and integral numbers only.
- **Regression test**: `cmd/mcp_test.go`: `initialize` result version equals `resolvedVersion()`; `internal/mcp`: `id:null` and `id:1.5` → `-32600` with `id:null`.
- **Notes/uncertainty**: The server returns `-32002 Server not initialized` for `tools/list` sent after the `initialize` response but before `notifications/initialized`. The spec only says clients SHOULD NOT send other requests before initialization completes, so this is strict but not wrong; compliant clients are unaffected.

## BUG-F15: Non-HTML flame formats are silently empty when the selected window has no samples

- **Status**: CONFIRMED as a sub-case of BUG-S2 (`01-sample-timestamps.md`), which already proposes the stderr note for windowed `flame`/folded output when untimed samples are excluded. The part not covered there: a window that is empty for any reason (also with fully timed input, e.g. `f-tiny.json --from 9.5`) gives silent empty speedscope/JSON/folded output.
- **Severity**: low — `--around-long-task 1` on the real fixture outputs zero bytes with exit 0 and no message, so an agent cannot distinguish "no CPU work" from "no sampler evidence".
- **Version**: v0.4.0 (new).
- **Summary**: HTML shows "No weighted samples in selected window", but folded prints nothing, speedscope prints an empty profile and JSON an empty tree, all with exit 0 and empty stderr. On `real-wrapped-fresh-record.json` the four longest tasks (1553, 634, 477, 336 ms) contain no timestamped samples because 121/159 samples are untimed (BUG-S1).
- **Reproduce**:

```bash
R=docs/bug-reports/v0.4.0/fixtures/real-wrapped-fresh-record.json
wkperf flame $R --around-long-task 1 --format folded > /tmp/f-o.txt 2>/tmp/f-e.txt; echo "rc=$? out=$(wc -c < /tmp/f-o.txt) err=$(wc -c < /tmp/f-e.txt)"   # rc=0 out=0 err=0
wkperf flame $R --around-long-task 1 --format json | jq -c '.charts[0].tree | {startTime,endTime,activeDurationMs, total: .root.totalMs, n: (.samples|length)}'
# {"startTime":1.720247,"endTime":3.273419833,"activeDurationMs":1553.172833,"total":0,"n":0}
```

Expected: a stderr note such as `no weighted samples in 1.720–3.273 s (1553 ms active); N samples lack timestamps`, and the same in JSON (`emptyReason`).
- **Root cause** [VERIFIED]: `internal/report/flame/flame.go` `renderFolded`/`renderSpeedscope` and the JSON path never check `Root.TotalMs == 0`; only `HTMLFragment` does.
- **Impact**: `flame --format folded|speedscope|json|svg` with `--from/--to/--around-long-task`.
- **Proposed fix**: in `runFlameCommand`, after building trees, print one stderr note per empty chart with the window and the count of untimed/out-of-window samples; add `empty: true` plus that reason to each JSON chart. Keep exit 0 (an empty window is a valid answer).
- **Regression test**: `cmd/flame_test.go`: `--from 9.5` on `f-tiny.json` → empty stdout and a stderr note; JSON chart has `empty: true`.
- **Notes/uncertainty**: after BUG-S1 is fixed, the long tasks above may contain samples; keep the test synthetic. Implement together with BUG-S2's exclusion note (same code path, one message).

---

## Fix plan for the coding agent

1. **BUG-S1 first (other report).** It changes which samples exist and therefore every real-profile number, size and empty-window case below; re-run the size measurements after it lands.
2. **BUG-F1 (byte budget, stack elision; non-breaking steps 1–3)** in `internal/query` + `internal/mcp`. Then **BUG-F7** in the same files (`Result` gains `source_maps`; update `ResultSchema`/`wkperf schema`). Bundle the breaking parts of F1 (ID-referenced stacks, `omitempty` rows) and F7 into one `wkperf.query.v2` bump if you choose to do them now.
3. **BUG-F2** (`internal/sourcemap` name rule), then **BUG-F11** and **BUG-F12** (same file: sidecar path existence, directory-walk leniency, duplicate error prefix).
4. **BUG-F3** (compact flame JSON; v2 layout optional) and **BUG-F4** (integer µs folded, new encoding; touches `internal/flame/folded.go`, `internal/report/flame/flame.go`, docs, `analyze --format folded`).
5. **BUG-F5** (`Tree.FocusFunction`, `cmd/flame.go`), then **BUG-F9** reuses the same semantics client-side.
6. **BUG-F6** (diff totals, warnings, normalization) after S1 — reuse `internal/compare` comparability helpers.
7. **BUG-F8, BUG-F9, BUG-F10** together in `internal/report/flame/flame.js` + `html.go` CSS.
8. **BUG-F13, BUG-F14** in `internal/mcp/server.go`, `cmd/mcp.go`, `cmd/version.go`, `internal/model/model.go`.
9. **BUG-F15** together with BUG-S2 (same stderr note and JSON field).

Related reports by other analysts: BUG-A8 (`03-analysis-correctness.md`) covers the ownership categories that drive flame colours (React internals such as `completeRoot`, `flushSpawnedWork` are coloured "app"); BUG-O5 covers analysis "Reproduce" commands with empty or ambiguous selectors (`query FILE paths ''`); BUG-O7 the zero-filled query row union. They are not repeated here.

Docs to update with the fixes: `docs/REFERENCE.md:175` (source-map naming rule, sidecar paths), `:216/:227` (folded unit/encoding), `:217` (function focus), `:244` (diff normalization/warnings), `:262` (byte budget and truncation fields), schema docs for any v2 bump.

## Things checked that are fine

- **Export totals agree everywhere.** Folded sum = JSON `root.totalMs` = sum of JSON sample weights = speedscope weights/`endValue` = my independent kept-subset sum: 313.009458 ms (fresh-record), 261.497958 (pods-scrolling), 357.151208 (rapid-scroll), 101.1825 (pods-not-moving). Every folded line parses as `stack count` by last space.
- **Schemas/well-formedness.** Flame JSON validates against `wkperf schema` (Draft 2020-12, 0 errors) for all four profiles; speedscope output validates against the official speedscope schema (0 errors, all frame indexes in range) and opens in speedscope 1.25.0 (small, 10 MB, 50 MB, and the two-profile diff); SVG is well-formed (`xmllint`) for all profiles; hostile names (`</script><img onerror>`, quotes, `&`, newline, `javascript:` URL) are escaped in HTML/SVG (no injected elements, no dialogs) and in folded.
- **HTML.** Self-contained (no `http(s)` `src`/`href`; data JSON is HTML-escaped inside `<script type="application/json">`), no console errors for small/10 MB/50 MB/diff/analyze/audit pages; click-to-zoom and breadcrumbs work; regex search highlights matches and reports invalid regexes; invert toggle is correct (inverted first level sums to 100%, inverted root = total, first-level totals = function self times); tooltips show generated/original location, self/total and diff baseline/current/delta; load ≤ 1.1 s and search re-render ≤ 68 ms even for the simulated all-timed profiles (2,975 / 3,132 nodes); no truncation of large trees.
- **Flags.** `--from/--to` select samples by timestamp in `[from,to)` with full weights (tiny `2.5–6` = 45 ms); `--around-long-task N` picks the N-th longest non-GC script record (independently re-derived on the real fixture: 1553.173, 633.592, 477.368, 336.419, 166.767 ms) and errors past the count; `--min-ms` and `--invert` change only visual formats (documented); `--diff` requires exactly two files; multi-input folded/speedscope keep separate roots/profiles.
- **Determinism.** Byte-identical outputs after shuffling `(stackTrace,duration)` pairs and records (seeds 7, 42, 9) and on repeated runs, for all five flame formats with and without `--invert`, `--diff` in all formats, `query` `top`, `top --by total`, `depth`, `by-root`, `hot-path`, `stacks`, `metrics`, `events`, `rootcause`, `window`, `callers/callees/paths/subtree` (selectors `B`,`A`,`commitRoot`,`performWorkOnRoot`,`getAnimations`), `query diff`, and `analyze --format json|md`, on `f-tiny.json`, the small real fixture and the 10 MB real profile. Equal-weight ties sort by name, url, line, column (tested with equal-weight `Alpha`/`Zed`). Function IDs are stable across recordings (`commitRoot` = `f_8fa78125c2e9…` in three different captures), independent of `sourceID` for URL frames, and unchanged by source maps.
- **Query operations** `top`, `find` (RE2, name/url/original), `callers`, `callees`, `paths`, `stacks` (+ unknown ID error), `subtree` (depth/min-ms, recursion visited once), `hot-path`, `by-root`, `depth` (+ deepest stack), `events` (count/total/mean/p95/max, per-trigger self/inclusive), `window FROM TO OP` and `--from/--to` matched the hand-computed answers on `f-tiny.json` and an independent Python re-implementation on the kept subsets of three real profiles (e.g. `getAnimations` self 69.122712 ms with sole caller `getFiniteAnimations` in pods-scrolling; 229.0995 ms in rapid-scroll, matching the previous session's "229 ms" claim). The emitted `command` reproduces the identical JSON for 8 operation/flag combinations (including `window`, `--by total`, `--min-ms`, `--min-share`, `--trim-*`).
- **Diff semantics.** Sign is candidate − baseline everywhere (query rows, flame folded columns, JSON comparison, SVG/HTML colours, speedscope baseline/current profiles); functions only in one side appear with zeros on the other (`D` new +7, `C` removed −21); rows sort by absolute delta then name.
- **Source maps (apart from F2/F7/F11/F12).** Lookup treats WebKit line/column as 1-based (a 0-based off-by-one would have produced `row`/`rows` instead of `computeLayout`/`renderRows` on the esbuild test bundle); function declarations resolve to the right original names and `file:line:col`; `wails://` URLs resolve with an explicit map (match by path suffix) and with a recording-adjacent sidecar; `file://` and recording-relative bundle URLs auto-discover the bundle-adjacent map; malformed/version-2/nonexistent explicit maps fail with `source_map_error`; a malformed adjacent map produces a warning and the run continues; originals flow into `analyze --format md|json`, query `json|md|tsv` (`gen → name file:line:col`), flame folded/SVG/HTML/speedscope; selectors by original name and by original `url:line:col` work. Walking `/usr/share` as a map directory took 0.8 s.
- **MCP.** `initialize` echoes `2024-11-05`, `2025-03-26` and `2025-06-18` and answers `2025-06-18` for an unknown version (`2099-01-01`); capabilities `tools.listChanged:false`; `ping` works before initialization; error codes: parse error `-32700` with `id:null`, invalid request/batch `-32600`, unknown method `-32601`, unknown tool `-32602`, second `initialize` `-32600`, pre-initialization `-32002`; invalid arguments (unknown field, wrong type, missing selector, negative/too-large limit, `operation` field) return `isError:true` results. `tools/list` exposes exactly the 15 `query.Operations` with valid Draft 2020-12 input/output schemas whose required fields match the CLI; for all 15 tools `structuredContent` validates against `outputSchema`, the text block equals it, and results equal CLI `wkperf query` JSON. stdout carries only JSON-RPC lines (stderr empty in normal operation). Relative paths resolve against the server's working directory; nonexistent paths, directories and `/dev/zero` return tool errors; symlinks are followed; `~` is not expanded (expected for a tool that never uses a shell).
- **Cross-checks of previous-session claims within this area.** Claim 2 sample counts are correct (fresh-record 121/159 untimed; pods-scrolling 406/591; rapid-scroll 2053/2282 with 357.151 ms kept; pods-not-moving 121/166 with 101.183 ms kept, all recounted independently) — tracked as BUG-S1. Analysis HTML and audit HTML embed the same flame implementation without errors.
