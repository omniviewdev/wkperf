# wkperf v0.4.0 — output contract bugs (area O)

Scope: output size and duplication, `--brief`/`--max-bytes`, reproduce commands, metric-key discovery, JSON Schema conformance, exit codes and error JSON, and finding confidence (claims 3, 5, 7 of the previous session, plus the agent-facing contract). Zero-timestamp sample loss is BUG-S1 (separate report); where it changes a number below, this is noted and not investigated.

All commands run from the repo root (`~/Repos/omniviewdev/wkperf`, HEAD `392facf` = `v0.4.0`) with the installed `wkperf` v0.4.0 (`go run .` gives the same results). Fixtures used:

- `docs/bug-reports/v0.4.0/fixtures/real-wrapped-fresh-record.json` (388 KB, byte-identical to `~/Scratch/profiles/fresh-record.json`; referred to as `$F`).
- `docs/bug-reports/v0.4.0/fixtures/o-real-wrapped-rapid-scrolling-deployment.json` (33 KB, byte-identical copy of `~/Scratch/profiles/rapid-scrolling-deployment.json`; `$R`).
- `docs/bug-reports/v0.4.0/fixtures/o-deep-stacks.jq`: jq generator of a flat recording with `shallow` depth-5 samples plus `deep` distinct stacks of depth `depth`.
- `docs/bug-reports/v0.4.0/fixtures/o-repro-commands.py`: extracts every `wkperf …` command printed by `analyze` (Markdown and/or JSON), runs it, and checks exit status, non-empty rows and the printed value.
- `wkperf sample --out /tmp/demo` (synthetic data from `internal/testgen`, used for compare/audit).

```bash
F=docs/bug-reports/v0.4.0/fixtures/real-wrapped-fresh-record.json
R=docs/bug-reports/v0.4.0/fixtures/o-real-wrapped-rapid-scrolling-deployment.json
```

Numbers for the three large captures (`pods-scrolling-to-failure.json` 10 MB, `pods-not-moving.json` 37 MB, `rapid-scroll-deployment.json` 50 MB) were measured on `~/Scratch/profiles/` and are quoted for scale; every bug is reproducible with the fixtures above.

## BUG-O1: Default `analyze` output is unbounded; exact stacks are repeated up to 26× (fixture) / 34× (pods-scrolling) and most lists ignore `--top`

- **Status**: CONFIRMED (claim 3: sizes confirmed; "full 366-frame stacks repeated many times" confirmed and quantified).
- **Severity**: blocker. The default Markdown/terminal report for a 10 MB capture is 7.6 MB (≈2M tokens) and JSON is 52 MB, so an agent cannot read the default output at all; JSON consumers have to parse 5× the input size to find a handful of stacks.
- **Version**: v0.4.0. v0.3.0 produced 7,511 B md / 15,730 B JSON for fresh-record and 10,253 B md / 21,219 B JSON for pods-scrolling-to-failure (measured on `.recording`-unwrapped copies, since v0.3.0 cannot read wrapped exports). The growth comes from the v0.4.0 root-cause/evidence sections.
- **Summary**: Every exact sampled stack is embedded verbatim, with all frames, in each place that references it: `commonStacks`, `hotspots[].commonStack`, `inclusiveHotspots[].commonStack`, `rootCause.appAttribution[].stacks`, `rootCause.depth.outliers[].stack`, `rootCause.findings[].evidence`, `findings[].stacks` (a copy of the previous one), window `topStack`/`hotPath`. Several of those lists are uncapped (they scale with the number of distinct functions, outlier stacks or timeline windows, not with `--top`). Output size is therefore O(stack depth × number of referencing sites) and is dominated by the same few hundred frame objects repeated tens of thousands of times.
- **Reproduce**:

  ```bash
  for fmt in md json term; do printf "%s " $fmt; wkperf analyze $F --format $fmt --no-color | wc -c; done
  wkperf analyze $F --format md --brief | wc -c
  wkperf analyze $F --format html --out /tmp/o1.html && wc -c < /tmp/o1.html
  wkperf analyze $F --format md --top 3 | wc -c          # --top barely helps
  wkperf analyze $F --format json | jq -c '[.. | objects | (.frames?, .commonStack?) | arrays | select(length > 0 and (.[0] | type) == "object")] | {stack_occurrences: length, distinct_stacks: (map(map([.name, .url, .line, .column])) | unique | length), frame_objects: (map(length) | add)}'
  wkperf analyze $F --format json | jq -r '.reports[0].findings[] | "\(.id) stacks=\(.stacks|length)"'
  wkperf analyze $F --format json | jq -r '.reports[0].rootCause | "appAttribution=\(.appAttribution|length) depthOutliers=\(.depth.outliers|length) rcFindings=\([.findings[]|"\(.id):\(.evidence|length)"]|join(","))"'
  ```

  Observed (fixture, 388 KB input): md 717,045 B; json 3,558,498 B (1,904,338 B compact); term 703,921 B; html 2,036,940 B; brief 28,485 B; `--top 3` md 633,099 B / json 3,176,567 B. JSON: `{"stack_occurrences":224,"distinct_stacks":37,"frame_objects":7142}`; stack arrays are 69.5% of compact bytes. The most repeated stack (10 frames) occurs 26 times, a 100-frame stack 16 times, and the deepest stack (109 frames) 12 times. `rootCause.findings[].evidence` (451,539 B) and `findings[].stacks` (451,489 B) are the same data twice. `deep-stack`, `deep-commit` and `deep-recursion` carry 7, 7 and 3 stacks that are all members of `depth.outliers` (7). `appAttribution` has 35 entries, each with its full stack(s).

  Markdown composition (fixture): 4,489 of 5,949 lines are stack-frame bullet lines (55% of bytes) but only 146 of those lines are distinct; 1,131 reproduce commands are 30% of bytes. Per `##` section: Finding details 257,255 B, Hot path (incl. "Owners and nearest application ancestors") 181,675 B, Hot functions 72,997 B, Stack depth 71,787 B, Common sampled stacks 48,059 B, Timeline windows 44,013 B.

  Large captures (same commands on `~/Scratch/profiles`):

  | capture | input | md | json | term | html | brief |
  |---|---:|---:|---:|---:|---:|---:|
  | rapid-scrolling-deployment | 33 KB | 90,471 | 289,540 | 88,130 | 207,164 | 18,809 |
  | fresh-record | 388 KB | 698,933 | 3,536,338 | 685,809 | 2,011,516 | 26,869 |
  | pods-not-moving | 37 MB | 1,023,737 | 6,245,143 | 1,003,904 | 2,981,379 | 26,214 |
  | rapid-scroll-deployment | 50 MB | 4,322,270 | 28,100,461 | 4,235,540 | 11,456,586 | 32,913 |
  | pods-scrolling-to-failure | 10 MB | 7,578,245 | 51,980,095 | 7,424,026 | 20,081,971 | 32,382 |

  The five JSON outputs sum to 90.2 MB (claim 3 confirmed). pods-scrolling-to-failure JSON: 697 stack occurrences, 113 distinct stacks, 127,237 frame objects but only 316 distinct frames; 93.1% of compact bytes are stack arrays. Its md has 72,984 frame lines (89% of bytes) with 294 distinct lines, and 158 rendered stack blocks of ≥300 frames (25 distinct stacks of depth ≥300 occur 271 times in JSON). In rapid-scroll-deployment, the deepest retained stack (360 frames) occurs 10 times in JSON; 16 distinct stacks of depth ≥300 are emitted 195 times (65,214 frame objects). Its `rootCause` keeps 66 slow frames and 78 event bursts although `--top` is 10. `--top 3` on pods-scrolling-to-failure still gives 7,269,308 B md / 48,370,898 B JSON.

  Scaling on synthetic input (200 shallow samples plus K distinct deep stacks of depth D):

  ```bash
  for D in 50 200 400; do for K in 5 80; do jq -nc --argjson depth $D --argjson deep $K --argjson shallow 200 -f docs/bug-reports/v0.4.0/fixtures/o-deep-stacks.jq > /tmp/syn.json; echo "D=$D K=$K in=$(wc -c </tmp/syn.json) md=$(wkperf analyze /tmp/syn.json --format md | wc -c) json=$(wkperf analyze /tmp/syn.json --format json | wc -c) brief=$(wkperf analyze /tmp/syn.json --format md --brief | wc -c)"; done; done
  ```

  | D | K | input | md | json | brief |
  |---:|---:|---:|---:|---:|---:|
  | 50 | 5 | 90,296 | 93,599 | 656,384 | 12,796 |
  | 50 | 80 | 340,184 | 730,033 | 6,622,775 | 12,799 |
  | 200 | 5 | 140,791 | 204,670 | 2,227,896 | 12,796 |
  | 200 | 80 | 1,148,104 | 1,725,163 | 23,195,140 | 12,799 |
  | 400 | 5 | 208,791 | 356,670 | 4,327,896 | 12,796 |
  | 400 | 80 | 2,236,104 | 3,087,163 | 45,337,540 | 12,799 |

  Expected: default output bounded by `--top` and independent of stack depth beyond a fixed rendering cap; each distinct stack stored once. Actual: linear in D × K (JSON 20× the input at D=400, K=80). Even tiny inputs carry a large fixed cost: `wkperf sample` writes `regressions/get-animations.json` (1,602 B), which produces 45,935 B md and 113,482 B JSON.

- **Root cause** [VERIFIED]:
  - `internal/insights/rootcause.go:59` `bridgeRootFinding` copies `raw.Evidence` into `f.Stacks`, so every stack-detector finding is serialized twice (`findings[].stacks` and `rootCause.findings[].evidence`).
  - `internal/rootcause/depth.go:96-124` `depthFindings` passes the same outlier stacks to `deep-stack` (all outliers), `deep-recursion` and `deep-commit`; each depth outlier is then stored in `depth.outliers` plus up to three root-cause findings plus three insights findings (7 copies).
  - `internal/rootcause/ownership.go:9-…` `ownership` returns one `Attribution` per distinct app ancestor with its stacks; nothing truncates it to `Top` (fresh-record 35, pods-scrolling 83, rapid-scroll 52 entries).
  - `internal/rootcause/windows.go:112-161` `windows`: only `worst` is cut to `b.o.Top` (lines 156-158). `longTasks`, `slowFrames` and `bursts` are returned complete, each with `topStack` and stats; `completeWindows` (77-93) builds hot paths for the first `Top` only.
  - `internal/rootcause` `Stack`/`Window` types embed `[]flame.Frame` by value, and `metrics.FuncStat.CommonStack` embeds `[]metrics.FunctionFrame`; there is no stack table or reference type.
  - Markdown mirrors the data: `internal/insights/render.go:154-167` (`renderHotPath` renders every attribution and all its stacks when not brief), `109-111` (`renderFindings` renders all `f.Stacks`), `201-211` (`renderFunctions` renders every common stack), `258-275` (`renderRootDetails` renders every depth outlier and `allRootWindows`, i.e. all long tasks + slow frames + bursts, 459-484), `315-321` (common stacks). `RenderTerm` (441-457) renders the same text, and HTML embeds the same data.
  - The reproduce strings add a fixed per-number cost: each embeds the full source path plus window flags (`internal/insights/numeric.go:17-37`, `internal/rootcause/build.go:147-167`).
- **Impact**: the default `analyze` (term), `--format md`, `--format json`, `--format html` and `--format all` are unusable by an LLM agent on real scroll captures. `audit` embeds full per-scene analyses (`report.scenes[].analysis` is 98.6% of a 7.05 MB audit JSON for the 12-recording `wkperf sample` scenes; `rootCause.slowFrames` alone is 2.5 MB because 57–285 slow frames per scene are kept). The suggested "Re-run" commands in Verification (`wkperf analyze FILE --from A --to B --format json`) emit 26 KB–3.4 MB each on the fixture (a 37 ms window produced 1,002,794 B).
- **Proposed fix** (design decisions explicit):
  1. JSON (bump to `wkperf-insights/v3`, same for `wkperf-audit/v2`, because the shape changes): add a per-report `frames` table (`[{id, name, url, line, column, sourceID, category, native, anonymous, original}]`, `id` = existing `f_…` function ID) and a `stacks` table keyed by the existing canonical path ID `n_…` (already accepted by `wkperf query FILE stacks ID`) with `{frames: [frameIndex…], depth}`. Every site that embeds `frames`/`commonStack` instead carries `stackId` plus its occurrence-specific numbers (`selfMs`, `samples`, `firstSec`, `lastSec`, `stats`). Hot paths (prefixes, not complete stacks) carry `frames: [frameIndex…]`. Measured effect of interning alone: fixture compact JSON 1,904,338 → ~584,819 B; pods-scrolling 25,889,288 → ~1,623,577 B; synthetic D=200 K=20 2,729,011 → ~244,629 B. Rejected: depth-truncating JSON stacks (breaks the "exact stack" promise), gzip (consumers still decode the inflated document), dropping `rootCause` from JSON (loses evidence).
  2. Remove `findings[].stacks`; give `findings[]` a `stackIds` array, and have deep-* findings reference `depth.outliers` by `stackId` rather than copying.
  3. Cap at `Top` (default 10), keeping the existing sort order, and add `…Total` counts plus a query command that lists the rest: `rootCause.appAttribution`, each attribution's `stacks`, `rootCause.depth.outliers`, each finding's evidence, `rootCause.longTasks`, `rootCause.slowFrames`, `rootCause.eventBursts`, `periodicRoots[].fires`. The `triage.*` counts already exist in `rootCause.stats`. Update REFERENCE.md: JSON lists are top-N with explicit totals and stacks are exact; it is no longer "never abbreviated".
  4. Markdown/term/HTML: render each distinct stack once in a final `## Stacks` section (label `S1…Sn`, `n_` ID, depth, `wkperf query FILE stacks n_…`). Everywhere else print a one-line reference (`stack S3 (361 frames; leaf getAnimations …)`). In the appendix, show the 6 root-most and 16 leaf-most frames with an explicit `… 339 frames elided; full stack: <command>` line.
  5. Print the source path and window once per report (header line `Reproduce base: wkperf query 'FILE' {metrics|rootcause} KEY --from A --to B`) and print only the key next to each number (see BUG-O4).
- **Regression test**: in `internal/insights` (or `cmd/pipeline_test.go`), generate the `o-deep-stacks.jq` shape with `internal/testgen` (depth 400, 80 deep stacks, 200 shallow). Assert: default md ≤ 150 KB; JSON contains each distinct frame once (`len(frames) == distinct`) and each stack once; no `frames` array outside the tables; `len(rootCause.appAttribution) ≤ Top` with `appAttributionTotal` set. Add a golden-size test on the fixture: default md ≤ 100 KB and JSON ≤ 700 KB. Doubling K must not change the md size by more than 5%.
- **Notes/uncertainty**: BUG-S1 currently discards 121/159 fixture samples, so a fixed BUG-S1 will make these outputs larger (more distinct stacks and attributions). The thresholds above are chosen to hold after that fix [INFERENCE]. The indented JSON is about 1.9× the compact size; I did not propose changing indentation.

## BUG-O2: `--max-bytes` breaks its documented contract (priority order, completeness, identity, multi-file)

- **Status**: NEW (claim 3 only mentioned `--brief`).
- **Severity**: high. `--max-bytes` is the documented way to get bounded agent context, but it hides most findings, drops recording identity, can silently omit whole recordings, and wastes up to half the budget.
- **Version**: v0.4.0 (flag new in v0.4.0; v0.3.0 has no `--brief`/`--max-bytes`).
- **Summary**: With any budget smaller than the full report, the bounded renderer keeps only the first four `##` sections (Findings top-3 summary, Hot path, Hot functions, Verification). It splits the budget equally between them (fair share, not the documented "fixed priority … findings, hot path, top functions, then verification"). It drops the `- Source:` and `- Active duration:` lines and never renders findings 4..N. In multi-file runs the first report takes the whole budget and later recordings disappear. The marker is not the last line when a scorecard follows.
- **Reproduce**:

  ```bash
  for n in 4000 100000 500000; do o=$(wkperf analyze $F --format md --max-bytes $n); echo "n=$n bytes=$(printf %s "$o" | wc -c) sections=$(printf '%s\n' "$o" | grep '^## ' | cut -c4-24 | tr '\n' '|') marker_line=$(printf '%s\n' "$o" | grep -n 'truncated:' | cut -d: -f1)/$(printf '%s\n' "$o" | wc -l) source_lines=$(printf '%s\n' "$o" | grep -c '^- Source:')"; done
  wkperf analyze $F --format md --max-bytes 4000 | grep -c '^### \['    # findings shown
  wkperf analyze $R $F --format md --max-bytes 3000 | grep -n '^# \|^- Source\|^---\|truncated'
  wkperf analyze $F --format md --max-bytes 200
  ```

  Observed:

  ```text
  n=4000 bytes=3952 sections=Findings|Hot path|Hot functions|Verification|Performance scorecard: grad| marker_line=52/52 source_lines=0
  n=100000 bytes=81730 sections=Findings|Hot path|Hot functions|Verification|Performance scorecard: grad| marker_line=612/652 source_lines=0
  n=500000 bytes=255691 sections=Findings|Hot path|Hot functions|Verification|Performance scorecard: grad| marker_line=1815/1855 source_lines=0
  1
  1:# wkperf profile: Timeline Recording 1
  43:[truncated: use JSON for complete report]
  ```

  - With 500,000 B allowed and a 717,045 B full report, 255,691 B are emitted: 244 KB of budget is unused and all detail sections are gone.
  - At 4,000 B, only 1 of the 9 findings has a heading; the per-section split was Verification 1,068 / Hot path 968 / Findings 949 / Hot functions 687 B.
  - The fixture has 9 findings (`jank, long-tasks, hot-module, react-sync-in-trigger, hot-function, deep-commit, deep-stack, deep-recursion, low-sample-coverage`). Under any truncation only the first 3 get a heading; the other 6 appear only as `Verification` bullets, without severity, title or impact.
  - In the two-file run, the second recording (`$F`) is absent; nothing says it was dropped, and both reports carry the same title `Timeline Recording 1`.
  - `--max-bytes 200` emits four empty headings followed by a scorecard heading.
  - pods-scrolling-to-failure with `--max-bytes 1000000`: 686,073 B emitted from a 7.58 MB report, marker mid-document.

  Expected: priority-ordered fill that uses the budget, all findings listed (at least one line each), source identity kept, every recording represented, marker last and counted.
- **Root cause** [VERIFIED]:
  - `internal/insights/render.go:364-375` `markdown`: when `MaxBytes > 0`, details are rendered only if the summary prefix is shorter than `MaxBytes`.
  - `render.go:377-415` `boundedMarkdown`: splits on `"\n## "`, keeps `sections[1..4]` only and discards the rest even when budget remains. `allowance := remaining / (4 - i)` gives each section an equal share. `title := strings.SplitN(sections[0], "\n", 2)[0]` keeps only the `# …` line, which drops `- Source:`/`- Active duration:`.
  - `render.go:86-98` `renderFindings(…, brief=true)` is always used for the leading summary and cuts to 3 findings without an omission note.
  - `cmd/analyze.go:272-300` `renderMarkdownAll`: each report is bounded to the whole `MaxBytes`, the scorecard is appended after the already-bounded report (so it follows the marker), and then `cmd/markdown.go:11-35` `writeBoundedMarkdown` strips inner markers and cuts the concatenation from the front, which drops later recordings.
- **Impact**: `analyze --format md --max-bytes N` (and `--format all`, which writes the bounded md file) gives an agent the scheduler-side hot path but not the findings list. The agent cannot tell which file it is reading and may miss whole recordings.
- **Proposed fix**: Replace the section splitter with a block packer.
  1. Build each report as an ordered list of blocks `{priority, mandatory, text}`. Mandatory: header (title + Source + Active duration + per-file error lines). Priority order: one line per finding for all findings (severity, id, title, impact, key); hot path; top functions; verification; finding details (in finding order); scorecard; other details.
  2. Fill greedily in priority order. Include whole blocks; a block that does not fit is cut at a line boundary and followed by `[truncated: <section>, N of M bytes shown]`. Lower-priority blocks are still tried if they fit (no fair share).
  3. Multi-file: reserve `header + findings-list` for every recording first, then split the remaining budget equally between recordings. If even the reserved part does not fit, emit the headers plus `[truncated: K recordings omitted: file1, file2]`.
  4. The final marker is always the last line and counted.

  Rejected: a per-section byte quota table, because it is still fair share and is fragile when a section is empty.
- **Regression test**: `internal/insights/render_test.go`, using a report built from the fixture (or `testgen`):
  - for N in {64, 200, 1000, 4000, 20000, len(full)-1}: `len(out) ≤ N`, the output ends with the marker line, and `- Source:` is present for N ≥ 300;
  - for N ≥ 4000, every finding ID appears in a heading or list line;
  - bytes used ≥ 0.9 N whenever the full report exceeds N;
  - two-report input: both `- Source:` lines are present for N ≥ 1000.

  Keep the existing "<64 rejected" test.
- **Notes/uncertainty**: Truncation always landed on a line boundary, and backtick counts were balanced on every line, in all 44 accepted runs (N from 64 to 1,000,000, brief and full, fresh-record and pods-scrolling-to-failure). The output is therefore syntactically valid Markdown. Tables can end after the header row.

## BUG-O3: `--brief` and `--max-bytes` are silently ignored for the default `term` format

- **Status**: NEW.
- **Severity**: medium. `wkperf analyze FILE --brief` (no `--format`) is the natural invocation, and it prints the full 0.7–7.4 MB report with exit 0 and no warning.
- **Version**: v0.4.0 (flags new).
- **Summary**: `RenderTerm` always renders the full report and ignores both flags; `validateMarkdownOptions` validates only md/all. With `--format all`, the bounded md is written to a file, but the full term report still goes to stdout.
- **Reproduce**:

  ```bash
  wkperf analyze $F --brief --no-color | wc -c              # 703921 (same as without --brief)
  wkperf analyze $F --max-bytes 20000 --no-color | wc -c    # 703921
  wkperf analyze $R --max-bytes 100 --format all --out /tmp/o3/ | wc -c   # 93260 B on stdout; /tmp/o3/wkperf-analysis.md is 81 B
  ```

  Expected: term honours `--brief`/`--max-bytes`, or the command fails with `--brief/--max-bytes require --format md`. Actual: both flags are silently ignored. On pods-scrolling-to-failure: `--brief` → 7,424,026 B.
- **Root cause** [VERIFIED]: `cmd/analyze.go:184-204` calls `insights.RenderTerm(stdout, r, color)` without `markdownOptions`; `internal/insights/render.go:441-442` `RenderTerm` uses `markdown(r, MarkdownOptions{})`. `cmd/input.go:106-109` returns early for formats other than md/all.
- **Impact**: agents and humans who omit `--format md` get unbounded output.
- **Proposed fix**: Add `RenderTermWithOptions(w, r, color, MarkdownOptions)` and use `boundedMarkdown(markdown(r, o), o.MaxBytes)` before terminal conversion. This is minimal because term is already a re-styling of the md text. Apply the ≥64 validation to term too. Under `--format all`, apply the options to the stdout term output as well. Rejected: erroring out (it breaks the obvious invocation for no benefit).
- **Regression test**: `cmd/cmd_test.go`: `analyze FIXTURE --brief --no-color` output equals the terminal conversion of `--format md --brief`; `--max-bytes 5000` gives `len ≤ 5000`.
- **Notes/uncertainty**: `analyze --format html --out -` writes the HTML report to a file literally named `-` in the current directory, whereas `flame` documents `--out -` as stdout. Either make `-` mean stdout for analyze too, or document the difference.

## BUG-O4: `--brief` is ~60% repeated reproduce commands, cuts the hot path at the wrong end, and hides findings without saying so

- **Status**: CONFIRMED (claim 3, second half), plus NEW sub-issues.
- **Severity**: medium. The brief is bounded (13–33 KB), but most of it is boilerplate. The hot-path leaf (the frame that carries the cost) is removed, and 6 of 9 findings are invisible.
- **Version**: v0.4.0 (new).
- **Summary**: Every number in brief output carries a full `wkperf query '<path>' {metrics|rootcause} '<key>' --format json --from … --to …` string. The path is repeated once per command. Hot path and stack abbreviation keep the first 12 root-side frames and drop the leaf. Only 3 findings are rendered, and nothing says more exist.
- **Reproduce**:

  ```bash
  wkperf analyze $F --format md --brief > /tmp/b.md
  python3 - <<'EOF'
  import re; t=open('/tmp/b.md').read(); c=re.findall(r'`(wkperf (?:[^`\\]|\\.)*)`',t)
  print(len(t.encode()), len(c), sum(len(x)+2 for x in c))
  EOF
  sed -n '/^## Hot path/,/^### Owners/p' /tmp/b.md | grep -E '^- `|abbreviated|depth'
  grep -c '^### \[' /tmp/b.md; wkperf analyze $F --format json | jq '.reports[0].findings|length'
  ```

  Observed: 28,485 B, 100 commands, 17,086 B of command text (60%); the fixture path is repeated 100 times (6,500 B). Same on the large captures: rapid-scroll-deployment 20,162/32,913 B (61%), pods-scrolling 19,167/32,382 B (59%). The hot path lists `performWorkUntilDeadline … mergePropsN`, then `_Path abbreviated; query the complete path._`, then `Hot path depth: 13 frames`. The terminal frame `mergeProps (…:181141:20)`, to which the "terminal sampled time 4.79934 ms" line refers, is not shown. 3 finding headings are shown out of 9 findings. Brief size also grows with `--top`: on rapid-scroll-deployment, `--top 3/10/30/100` gives 27,150 / 32,913 / 40,558 / 56,063 B, from the Verification "Inspect" lines.
- **Root cause** [VERIFIED]: `internal/insights/render.go:65-69` `renderRootStats` and `101` print a full command per value. `137-146` (`renderHotPath`) and `72-75` (`renderStack`) use `frames[:12]`, which keeps the root end (frames are root-to-leaf). `95-98` cuts findings to 3 silently. `216-231` `renderVerification` lists every finding's verify/windows and every `RootCause.Windows` entry (up to 3×Top).
- **Impact**: agents receive mostly repeated paths and lose the hot-path leaf, which is the only frame that points at the costly source.
- **Proposed fix**:
  1. Print once per report: `Reproduce any value: wkperf query '<FILE>' metrics <KEY> --from A --to B --format json` and the same for `rootcause`. After each number, print only `[metrics: findings.jank.impact_ms]` or `[rootcause: hot_path.self_ms]`. Keep full commands only for non-scalar queries (stacks, hot-path).
  2. Abbreviate paths by keeping the first 3 and the last 9 frames, with `… K frames elided …` between them.
  3. List all findings as one line each (`[SEV] id: title, impact X ms`) and give the details only for the top 3.
  4. Cap brief Verification windows at 5, with an omission note.

  Rejected: dropping reproduce info from brief (the key is the contract an agent needs).
- **Regression test**: `internal/insights` brief test: the leaf frame of the hot path appears in brief output; every finding ID appears; the number of occurrences of the source path ≤ 3; brief size on the fixture ≤ 12 KB.
- **Notes/uncertainty**: the "Re-run" analyze commands printed in Verification have float noise (`--to 13.841896083322354`, `--from 1.720246999990195`) and emit multi-MB JSON (BUG-O1).

## BUG-O5: Printed reproduce commands that fail or cannot return evidence (empty selector, ambiguous names, GC events, no-sample windows, `\|` escaping)

- **Status**: CONFIRMED (claim 6, reproduce part: `paths ''`). The application-ownership classification part of claim 6 is out of scope here and not investigated.
- **Severity**: medium. All printed numeric values reproduce exactly (see "Things checked"), but whole classes of printed commands error or return 0 rows, and the agent loses a turn and the attribution path each time.
- **Version**: v0.4.0 (`query` is new in v0.4.0).
- **Summary**: The checker extracted 2,507 commands (1,401 unique) from the fixture's full md and JSON, 561 (345 unique) from `$R`, and sampled 60 brief + 40 JSON commands from each large capture. Failure categories:
  1. `paths ''` for every nearest-app ancestor with an empty function name: 9/35 attributions in fresh-record, 2/3 in rapid-scrolling, 22/83 in pods-scrolling, 14/52 in rapid-scroll, 6/28 in pods-not-moving. Exit 1, `function selector is required`.
  2. Ambiguous name selectors: `paths 'heartbeat'` in `$R` matches two functions (`:304368:25` and `:304368:28`). Exit 1, status `ambiguous`.
  3. Every GC-overlap command `events 'garbage-collected' --from … --to …` returns 0 rows with status `ok`, because GC records are never trigger rows.
  4. "Inspect"/"Reproduce window" `hot-path` commands for windows that have no samples return 0 rows: 10 in fresh-record, 2 in `$R`, and 5–14 among the 60 sampled brief commands of each large capture. JSON and full md say "No overlapping sampler samples", but the brief "Inspect" line does not.
  5. Commands whose path contains `|` are corrupted in Markdown list items, because `code()` escapes `|` as `\|` everywhere, and in a CommonMark code span the backslash is literal. All 58 unique brief commands failed when copied verbatim for a source under `/tmp/we ird'$x|dir/`; the JSON copy of the same command worked.
- **Reproduce**:

  ```bash
  python3 docs/bug-reports/v0.4.0/fixtures/o-repro-commands.py --md <(wkperf analyze $F --format md) --json <(wkperf analyze $F --format json) | head -8
  wkperf analyze $F --format json | jq -r '.reports[0].rootCause | "attributions=\(.appAttribution|length) emptySelector=\([.appAttribution[]|select(.command|contains(" paths '"''"' "))]|length)"'
  wkperf query $F paths '' --from 0 --to 244.320199458 --format json; echo rc=$?
  wkperf query $R paths 'heartbeat' --from 0 --to 26.582651625 --format json | jq -c '{status,total_count,candidates:[.candidates[]|{id,line,column}]}'
  wkperf query $F events 'garbage-collected' --from 214.528493167 --to 214.6695605 --format json | jq -c '{status,total_count}'
  wkperf query $F events --format json | jq -c '[.rows[]|[.kind,.name]]'
  wkperf query $F hot-path --min-share 0 --from 1.720247 --to 3.273419833 --format json | jq -c '{status,total_count}'
  d="/tmp/we ird'\$x|dir"; mkdir -p "$d"; cp $R "$d/rec.json"; wkperf analyze "$d/rec.json" --format md --brief | grep -m1 -o '`wkperf [^`]*`'
  ```

  Observed:

  ```text
  commands extracted=2507 unique=1401 run=1401
    exit_1 1 / no_rows 11 / ok 9 / ok_match 1220 / ok_unchecked 160
  attributions=35 emptySelector=9
  wkperf: function selector is required   {"schema":"wkperf-error/v1","errors":[{"file":"","message":"function selector is required","code":"command_error"}]}  rc=1
  {"status":"ambiguous","total_count":2,"candidates":[{"id":"f_144ee6bd…","line":304368,"column":25},{"id":"f_b975202f…","line":304368,"column":28}]}
  {"status":"ok","total_count":0}
  [["timer","timer-fired"],["dispatch","message"]]
  {"status":"ok","total_count":0}
  `wkperf query '/tmp/we ird'\''$x\|dir/rec.json' metrics 'recording.duration_sec' …`   (copy-paste → "no such file or directory")
  ```

  The stable ID works for the same anonymous function: `wkperf query $F paths f_59127dc3a9c587b485394d006b711aafdfc476c5b69835ea7941393a5f767066` → status ok, `(anonymous) …:290602:26`. The GC overlap's own stat command works: `wkperf query $F rootcause 'gc.de844d7845ee7d67e684ebbe.duration_ms' --from 0 --to 244.320199458` → 141.067333 ms.
- **Root cause** [VERIFIED]:
  - `internal/rootcause/ownership.go:50` builds `Command: b.query("paths "+quote(app.Name), …)` from the display name. `internal/rootcause/periodic.go:157` does the same for periodic roots. `internal/rootcause/build.go:64-68` builds `Commands.Paths` from the first sample's leaf name, which is arbitrary.
  - `flame.Frame` (`internal/flame/types.go:29`), used for every root-cause frame, has no `id` field in JSON. An agent therefore cannot recover the selector from `rootCause.appAttribution[].app` either: these frames have `name,url,line,column,sourceID,native,anonymous,category`, whereas `metrics.FunctionFrame` has `id`.
  - `internal/rootcause/windows.go:205` sets `GCOverlap.Command` to `events 'garbage-collected'`; `internal/query/events.go:13` matches only trigger names and kinds.
  - `internal/insights/render.go:226-229` prints `- Inspect <label>: <command>` without `window.SampleStatus`.
  - `internal/insights/render.go:24,28` `markdownEscaper` replaces `|` with `\|` in every code span, not only in table cells.
- **Impact**: "Reproduce attribution" for anonymous or duplicate-named app code, which is common in minified React bundles, cannot be run. GC "reproduce" commands never reproduce anything. Brief "Inspect" lines send agents to empty windows. Sources with `|` in the path break every md command.
- **Proposed fix**:
  1. Use `flame.FunctionID(app)` (the `f_…` ID, already accepted by all selector ops) in `ownership.go:50`, `periodic.go:157` and `build.go:68`, and add `ID string json:"id"` to `flame.Frame` (filled at build time).
  2. Point `GCOverlap.Command` at `rootcause 'gc.<id>.duration_ms'`, the existing stat. Rejected: adding GC to `events`, which changes the trigger-table semantics.
  3. In brief Verification, append `(no samples; timeline-only)` and use `rootcause 'triage.<id>.duration_ms'` for windows without `TopStack`.
  4. Escape `|` only inside table cells. For code spans, choose a backtick fence longer than the longest backtick run in the content (CommonMark), instead of `\``.
  5. Optional: unify the window formatting on one formatter. `NumericCommand` uses `strconv.FormatFloat(s,'f',-1,64)` (`--to 244.32019945827778`, `internal/insights/evidence.go:51`), while root-cause uses 1e-9 rounding (`--to 244.320199458`, `internal/rootcause/build.go:142-143`). No value mismatch was observed, so this is cosmetic.
- **Regression test**: `cmd` test using `real-wrapped-fresh-record.json` and `o-real-wrapped-rapid-scrolling-deployment.json`: run `o-repro-commands.py` logic in Go (or keep the script in CI). Assert that no printed command exits non-zero, that every `metrics`/`rootcause` value matches, and that no GC command returns 0 rows. Unit test: a recording with two same-named functions and one anonymous app frame produces attribution commands with `f_` selectors that each return status `ok`.
- **Notes/uncertainty**: windows without samples may be caused partly by BUG-S1 (fresh-record keeps only 38 of 159 samples); the md wording problem is independent of that.

## BUG-O6: Metric keys cannot be discovered; `samples.total_ms` does not exist; two overlapping namespaces; function keys do not match function IDs

- **Status**: PARTIALLY CONFIRMED (claim 5). `samples.total_ms` is not a key (correct), but the claim that only `samples.coverage_ratio` exists is wrong: `samples.count`, `samples.sampled_ms`, `samples.script_ms` and `samples.coverage_ratio` (the last only when coverage is available) all exist. On large captures they are simply not listable.
- **Severity**: medium. Agents verifying a fix need to find the right key and re-query it; today discovery requires reading Go source.
- **Version**: v0.4.0 (new).
- **Summary**: `query FILE metrics` without a selector lists keys, but `--limit` is capped at 1000 and there is no offset, prefix or regex. Real captures have 1,613–4,190 keys, sorted alphabetically, so whole families are unreachable. Keys are spread over two ops with overlapping names and different rounding. Per-function keys use an opaque `%00`-joined encoding unrelated to the `f_…` IDs in JSON. Each single-key lookup re-runs the full analysis with `Top=1000`.
- **Reproduce**:

  ```bash
  wkperf query $F metrics --limit 1000 | jq -c '{total_count, truncated, last:.rows[-1].key[0:50]}'
  wkperf query $F metrics --limit 1000 | jq '[.rows[].key|select(startswith("wakeups."))]|length'; wkperf query $F metrics wakeups.count | jq -c '.rows[0]|{key,value}'
  wkperf query $F metrics --limit 1001 2>&1 | head -1
  wkperf query $F metrics 'samples.total_ms' 2>&1 | head -1; wkperf query $F metrics 'samples.*' 2>&1 | head -1
  for k in samples.count samples.sampled_ms recording.duration_sec overview.duration_s; do echo "$k $(wkperf query $F metrics $k | jq -c .rows[0].value)"; done
  for k in samples.count samples.self_ms; do echo "rootcause $k $(wkperf query $F rootcause $k | jq -c .rows[0].value)"; done
  wkperf analyze $F --format json | jq -r '.reports[0].hotspots[0] | "\(.id) \(.name)"'
  wkperf analyze $F --format md | grep -o "metrics 'functions\.pop[^']*self_ms'" | head -1
  ```

  Observed:

  ```text
  {"total_count":1613,"truncated":true,"last":"triggers.dispatch.message.functions.%00url:wails:"}
  0                                   # no wakeups.* key in the listing …
  {"key":"wakeups.count","value":0}   # … yet the key exists
  wkperf: limit must be between 1 and 1000 (0 uses 50)
  wkperf: unknown metric key "samples.total_ms"
  wkperf: unknown metric key "samples.*"
  samples.count 38 / samples.sampled_ms 313.009458245 / recording.duration_sec 50.730864708 / overview.duration_s 50.730864708
  rootcause samples.count 38 / rootcause samples.self_ms 313.009458
  f_9ce07923d8ec1780497354a563a8b31fede7d54d69ec9b3a00f55cad40db4d32 pop
  metrics 'functions.pop%00url:wails:%2F%2Flocalhost%2Fassets%2Findex-GgPenX8q.js%00446188%0017.self_ms'
  ```

  Large captures: `metrics` total_count 4,190 (pods-scrolling) and 3,831 (rapid-scroll), with no `samples.*` key in the first 1000. `rootcause` total_count 1,724 and 1,762. A single key lookup on rapid-scroll-deployment takes 3.61 s (`metrics recording.duration_sec`), versus 3.78 s for the whole `analyze --format json` and 0.76 s for `query top`.
- **Root cause** [VERIFIED]:
  - `internal/query/engine.go:43-48` caps the limit at `MaxLimit` (1000), and there is no offset.
  - `engine.go:208-229`: `metrics` matches only `req.Selector == key`, merges `analyze.SummarizeWith` metrics with `insights.NumericValues` of a fresh `insights.Analyze(…, Top: MaxLimit)` run, and errors with no suggestions.
  - `engine.go:233-243`: `rootcause` is a separate key space from `rootcause.Stats`. `samples.count` exists in both; `samples.sampled_ms` (metrics, 1e-9 rounding by `clean`) and `samples.self_ms` (rootcause, 1e-6 rounding by `rootcause.number`) are the same quantity under two names. `recording.duration_sec` (`internal/insights/numeric.go:53`) duplicates `overview.duration_s`. Root-cause keys such as `deep-commit.self_ms` and `react.sync.self_ms` also leak into `metrics` through finding `NumericEvidence` (`numeric.go:136-140`).
  - `numeric.go:39` builds `functions.<url.PathEscape(f.Key())>`, where `metrics.FunctionFrame.Key` (`internal/metrics/metrics.go:87-93`) is `name\x00url:…\x00line\x00col`. This differs from `FuncStat.ID` (`f_…`).
- **Impact**: claim 5 follows from this: an agent guessed `samples.total_ms` (the query envelope even has a top-level `total_ms` field) and could not list keys to find the right one. On a 50 MB capture, each `metrics` reproduce command printed by a report costs ~3.6 s; each `rootcause` command costs ~0.9 s.
- **Proposed fix**:
  1. Add `wkperf query FILE keys [PREFIX|/REGEX/] [--offset N]`, returning rows `{key, op: metrics|rootcause, unit, description, basis}` plus a `next_offset`. Expose the same in MCP.
  2. Make `metrics` and `rootcause` accept a trailing `.*` prefix selector and `/regex/`, and add `--offset` to all row ops.
  3. Unknown-key errors list up to 10 nearest keys (shared prefix, then edit distance) in `errors[].candidates`.
  4. Accept `functions.f_<id>.…` as an alias and print that form in reports (keep the `%00` form readable for one release only if needed).
  5. Remove the alias pairs (`recording.duration_sec`/`overview.duration_s`; `samples.sampled_ms`/`samples.self_ms`), or document one name as canonical and make the other key resolve to it, with the same rounding.
  6. Performance: compute only the requested key family (e.g. skip `rootcause.Build` for `overview.*`/`script.*` keys).

  Rejected: raising `MaxLimit` (it only moves the cliff).
- **Regression test**: `internal/query`: `keys` on the fixture returns `total_count` equal to the union of both namespaces, and paging with `--offset` reaches `wakeups.count`. `metrics 'samples.*'` returns exactly the 4 sample keys. An unknown key returns candidates that include `samples.sampled_ms` for `samples.total_ms`. `metrics functions.f_<id>.self_ms` equals the hotspot `selfMs`.
- **Notes/uncertainty**: `samples.count` = 38 on the fixture (159 raw samples) is BUG-S1.

## BUG-O7: Query rows are a zero-filled union; inapplicable fields look like measured zeros

- **Status**: NEW.
- **Severity**: low. The output is bloated and ambiguous, but parseable.
- **Version**: v0.4.0 (new).
- **Summary**: Every query row serializes all 27–29 union fields. `metrics`/`rootcause` rows carry `self_ms: 0, samples: 0, percent: 0, baseline_*: 0, count: 0, …`, and `top` rows carry `value: 0, count: 0, mean_ms: 0`. The envelope also carries `by: "self"` for ops where ranking does not apply, and a top-level `total_ms` (sampled total) that is easy to confuse with a metric.
- **Reproduce**:

  ```bash
  wkperf query $F metrics --limit 50 | jq '.rows[0]'
  wkperf query $F metrics --limit 50 | python3 -c 'import json,sys; r=json.load(sys.stdin)["rows"]; e=lambda v:v in (0,"",False,None,[],{}); print(sum(len(json.dumps({k:v}))-2 for x in r for k,v in x.items() if e(v)), len(json.dumps(r)))'
  ```

  Observed: 19,220 of 25,204 row bytes (76%) are default-valued fields for `metrics`; `rootcause` 67%; `top` 43%. Example `metrics` row: `{"id":"","name":"","url":"","line":0,…,"self_ms":0,"total_ms":0,"samples":0,…,"key":"findings.jank.impact_ms","value":13825.196083322,"unit":"ms",…}`.
- **Root cause** [VERIFIED]: `internal/query/types.go:84-117` `Row` declares numeric fields without `omitempty` ("Unused numeric fields are zero"). `query_Row` in `wkperf schema` marks 26 of 36 properties as required.
- **Impact**: the bytes are wasted in MCP and agent contexts, and `self_ms: 0` on a value row can be misread as a measurement.
- **Proposed fix**: Introduce `wkperf.query.v2`, with per-operation row shapes expressed as a `oneOf` discriminated by `operation` in the schema. Implement this with `omitempty` plus pointer fields for values where 0 is meaningful (`value`, `self_ms`, …), and set them only for applicable ops. Drop `by` unless the op is `top`/`diff`, and rename the top-level `total_ms` to `sampled_total_ms`. Rejected: keeping v1 and adding `omitempty` silently, which breaks consumers that rely on required fields without a discriminator change.
- **Regression test**: `internal/query/schema` test: for each op, rows contain only that op's fields; `metrics` rows contain exactly `key, value, unit`; the schema validates the output.
- **Notes/uncertainty**: coordinate with BUG-O6 so the `keys` rows use the v2 shape.

## BUG-O8: `compare` and `summary` accept an empty window, report comparability 1.00 and pass `--strict-fixture`

- **Status**: NEW.
- **Severity**: high. A CI gate (`--strict-fixture --fail-on-regression`) passes with exit 0 when the window is outside both recordings, for example after a typo or a unit mix-up. `analyze`, `audit`, `flame` and `query` correctly fail on the same input.
- **Version**: v0.4.0. v0.3.0 behaves the same: the same command prints `Comparability: 1.00` and exits 0. The bug is pre-existing.
- **Summary**: `loadGroup` summarizes each file with the window and never checks that the window intersects the recording. Every metric is 0, both sides are equal, and comparability is perfect.
- **Reproduce**:

  ```bash
  wkperf sample --out /tmp/demo >/dev/null
  wkperf compare /tmp/demo/baseline-1.json --vs /tmp/demo/candidate-1.json --from 1000 --to 2000 --strict-fixture --min-comparability 0.99 --no-color; echo rc=$?
  wkperf compare /tmp/demo/baseline-1.json --vs /tmp/demo/candidate-1.json --from 1000 --to 2000 --format json | jq -c '{comparability, counts, n:(.metrics|length)}'
  wkperf summary /tmp/demo/baseline-1.json --from 1000 --to 2000 --format json | jq -c '[.metrics[]|[.key,.value]]'; echo rc=$?
  wkperf analyze /tmp/demo/baseline-1.json --from 1000 --to 2000 --format json | jq -c .errors
  ```

  Observed:

  ```text
    Comparability: 1.00
  0 improved · 0 regressed · 0 noisy · 0 unchanged
  Overall: no significant change
  rc=0
  {"comparability":{"score":1,"normalized":false,"warnings":[]},"counts":{"Improved":0,"Regressed":0,"Unchanged":0,"Noisy":0},"n":4}
  [["overview.duration_s",0],["overview.records",0],["overview.wall_duration_s",0],["overview.paused_s",0]]   rc=0
  [{"file":"/tmp/demo/baseline-1.json","message":"…window is empty: 1000.00s..10.00s of a 10.00s recording","code":"window_error"}]
  ```

  Expected: exit 1 with `window_error` per file, as for `analyze`/`audit`/`flame`/`query`. Actual: success.
- **Root cause** [VERIFIED]: `cmd/load.go:60-77` `loadGroup` calls `analyze.SummarizeWith(rec, f, wo)` without the emptiness check that `clipRecording` (`cmd/load.go:100-102`) performs for `analyze`/`audit`.
- **Impact**: `compare`, `summary` and CI gating (`--strict-fixture`, `--fail-on-regression`).
- **Proposed fix**: Factor the `lo/hi` computation of `clipRecording` into `windowBounds(rec, wo) (lo, hi float64, err error)` and call it in `loadGroup` before `SummarizeWith`. Return `&inputError{File: f, Code: "window_error", Err: err}`. Also make `computeComparability` emit a `window-empty` warning with score 0 if the summarized active duration is 0, as a second line of defence.
- **Regression test**: `cmd/cmd_test.go`: `compare a b --from 1000 --to 2000 --format json` exits 1, and `errors[0].code == "window_error"`; the same for `summary`.
- **Notes/uncertainty**: `summary` of a real capture with an empty window reported `overview.records = 3` (rapid-scrolling, `--from 100 --to 200`), so a few untimed records survive the window [not investigated].

## BUG-O9: Error JSON and exit-code details are inconsistent across commands and undocumented

- **Status**: NEW (claim 1 itself is confirmed; see "Things checked").
- **Severity**: low/medium. Exit codes are correct, but agents cannot branch on `code` reliably, and two output modes mislead.
- **Version**: v0.4.0 (error envelope new).
- **Summary**: The same condition gets different codes depending on the command. The code vocabulary is not documented, and the schema has no enum for it. `query` doubles the file prefix in messages. With `--out FILE`, a partial failure prints a `wkperf-error/v1` document to stdout although FILE holds a valid partial report. Markdown/term output never lists failed files. Key order changes when errors exist.
- **Reproduce** (exit code / stdout schema / `errors[].code`; run with `--format json`, `wkperf sample --out /tmp/demo`, `echo '{not json' > /tmp/bad.json`):

  | case | analyze | audit | compare | summary | flame | query |
  |---|---|---|---|---|---|---|
  | good + bad file | 1 / insights / `load_error` (good report kept) | 1 / audit / `load_error` | 1 / error / `load_error` (stops) | 1 / error / `load_error` (stops) | 1 / error / `load_error` (stops) | n/a |
  | missing file | 1 / insights / `input_error` | 1 / audit / `input_error` | 1 / error / `input_error` | 1 / error / `input_error` | 1 / error / `input_error` | 1 / error / **`load_error`** |
  | empty window | 1 / insights / `window_error` | 1 / audit / `window_error` ×12 | **0** (BUG-O8) | **0** (BUG-O8) | 1 / error / `window_error` | 1 / error / **`command_error`**, `file: ""` |
  | bad / unknown selector | n/a | n/a | n/a | n/a | 1 / error / `focus_error` | 1 / error / `command_error` |
  | ambiguous selector | n/a | n/a | n/a | n/a | n/a | 1 / query (`status: ambiguous`, `candidates`) + `errors[code=command_error]` |

  ```bash
  wkperf query /tmp/bad.json top 2>&1 >/dev/null              # wkperf: /tmp/bad.json: /tmp/bad.json: decode recording: …
  wkperf analyze $R /tmp/bad.json --format json --out /tmp/o9.json | jq -c '{schema, n:(.errors|length)}'; jq -c '{schema, reports:(.reports|length), errors:(.errors|length)}' /tmp/o9.json
  wkperf analyze $R --format json | jq -c 'keys_unsorted'; wkperf analyze $R /tmp/bad.json --format json | jq -c 'keys_unsorted'
  wkperf analyze $R /tmp/bad.json --format md --brief 2>/dev/null | grep -ci 'error\|failed'
  ```

  Observed: the double prefix shown above; stdout `{"schema":"wkperf-error/v1","n":1}` while `/tmp/o9.json` is `{"schema":"wkperf-insights/v2","reports":1,"errors":1}`; key order `["schema","reports"]` vs `["errors","reports","schema"]`; `0` error mentions in the md report (the failure is only on stderr and in the exit code).
- **Root cause** [VERIFIED]:
  - `cmd/errors.go:43-68` `failuresFor` uses an `*inputError`'s own code. It maps a bare `*os.PathError` to `input_error` and everything else to `command_error`. `query` errors come from `internal/query/engine.go:127` (`fmt.Errorf("%s: %w", file, err)`), which wraps a loader error that already contains the path (hence the duplicate prefix), and from `136-138` (empty window), a plain error that becomes `command_error`.
  - `query` skips `expandPaths` (which `stat`s and yields `input_error`), and `loadRecording` labels every open or parse failure `load_error` (`cmd/input.go:36-37`). A missing file is therefore `load_error` only in `query`.
  - `cmd/query.go:73-75` returns `ExitError{Code:1}` for ambiguity, which maps to `command_error`.
  - `cmd/errors.go:123-131`: when stdout holds no JSON (because of `--out`), a fresh error-only document is written. `133-157` re-encodes through `map[string]json.RawMessage`, which sorts keys.
  - `cmd/analyze.go:205-220`: the md path never renders `failures`.
  - REFERENCE.md §"JSON contracts and errors" names `message`, `file`, `code` but not the codes, and `schema_Error.code` is `{"type":"string"}`.
- **Impact**: an agent cannot use `code` to decide between "fix path", "fix window" and "choose candidate". With `--out` it may conclude that the run failed completely. A md-only consumer never learns that a file was skipped.
- **Proposed fix**:
  1. Fix the vocabulary: `input_error` (path missing or unreadable), `load_error` (unparseable), `source_map_error`, `window_error`, `selector_error` (unknown or empty), `ambiguous_selector`, `unknown_key`, `usage_error` (flags, ops, limits), `regression`, `fixture_mismatch`, `score_below_minimum`. Make it an `enum` in the schema and add a table to REFERENCE.md.
  2. `query`: wrap engine errors in typed errors with `File` set, and stop re-prefixing.
  3. With `--out`, write nothing to stdout on partial failure (stderr already says it); the error-only document is only for failures before rendering.
  4. Preserve key order by decoding into an ordered struct per schema (or append `errors` to the buffered document textually).
  5. md/term: add `## Errors` (one line per failed file) at the top when `failures` is non-empty, counted inside `--max-bytes`.
- **Regression test**: `cmd/cmd_test.go` table test over the matrix above, asserting exit code, schema and code. `query` missing file → `input_error`; empty window → `window_error` with `file`; ambiguous → `ambiguous_selector`. The md output for good+bad contains `## Errors`.
- **Notes/uncertainty**: `compare`/`summary`/`flame` stopping at the first bad input is documented ("may stop at the first bad input") and was not treated as a bug.

## BUG-O10: Finding `confidence` is the constant `"low"`; audit with repeat runs still says "Single-run observation"

- **Status**: CONFIRMED (claim 7).
- **Severity**: medium. The field carries no information, so agents cannot rank leads by it. In audit it is factually wrong, and the same word means something different in `compare`.
- **Version**: v0.4.0. v0.3.0 findings had no `confidence` field (`null` in v0.3.0 JSON).
- **Summary**: `enrichFindings` assigns `Confidence = "low"` unconditionally. Only the `confidenceReasons` strings vary (single run always; plus short capture, <100 samples, coverage <0.5; root-cause findings add "hypothesis"). A 1000-sample, 10 s synthetic capture with coverage 3.9 still gets `low` on HIGH-severity findings. Audit copies the single-recording finding, so a priority seen in 4 scenes × 3 runs is still "Single-run observation". In `compare`, `low` means "base/candidate run ranges overlap" (`single-run|low|high`).
- **Reproduce**:

  ```bash
  wkperf analyze $F --format json | jq -c '[.reports[0].findings[].confidence]|unique'
  wkperf sample --out /tmp/demo >/dev/null
  wkperf analyze /tmp/demo/baseline-1.json --format json | jq -c '.reports[0] | {dur:.durationSec, samples:.sampleCoverage.sampleCount, ratio:.sampleCoverage.ratio, f:[.findings[]|{id,severity,confidence,confidenceReasons}][0:2]}'
  wkperf audit /tmp/demo/scenes --format json | jq -c '[.report.scenes[]|{name,runs}], [.report.priorities[]|{id,confidence,confidenceReasons,scenes:(.scenes|length)}][0:2]'
  ```

  Observed: `["low"]`; `{"dur":10,"samples":1000,"ratio":3.92…,"f":[{"id":"hot-module","severity":"high","confidence":"low","confidenceReasons":["Single-run observation; repeat the same scene to estimate variance."]},…]}`; audit scenes each have `runs: 3`, and priorities `hot-module`/`hot-function` (4 scenes) are `confidence: low` with `["Single-run observation; repeat the same scene to estimate variance."]`. All 9 findings on each of the five real captures are `low`.
- **Root cause** [VERIFIED]: `internal/insights/evidence.go:183-194` hard-codes `"low"` and the single-run reason, and `internal/insights/rootcause.go:53,123` reuses it. In audit, `internal/audit/audit.go:137` (`pickRep`, 161-…) keeps one representative run per scene (frame p95 closest to the median). `buildPriorities` (293-348) then copies `Confidence`/`ConfidenceReasons` from the highest-impact scene's representative finding (line 330). The other runs' reports (`Scene.Runs[].Report`) are never consulted for the finding. REFERENCE.md:169 documents "Single-capture findings carry low confidence" but defines no other level for findings.
- **Impact**: `analyze`/`audit` JSON/md/html `confidence` fields are noise; the audit reason text is false.
- **Proposed fix**: Make the dimensions explicit, then derive the label.
  1. Add `evidenceBasis`: `measured` (instrumented timeline records: jank, long-tasks, layout, gc), `sampled` (hot-function/module, react-*, depth), or `hypothesis` (pattern detectors, suspected-layout-read).
  2. Add `replication: {runs, runsWithFinding}`: 1/1 for `analyze`. In `audit`, count, per scene, the runs whose analysis contains the finding ID.
  3. Derive `confidence`:
     - `high` = measured ∧ runs ≥ 3 ∧ runsWithFinding/runs ≥ 2/3;
     - `medium` = measured single run with ≥10 active s, or sampled with ≥100 samples ∧ coverage ≥ 0.5 ∧ (runs ≥ 3 ∧ ratio ≥ 2/3);
     - `low` otherwise.
  4. Reasons list each failed criterion, and audit reasons state "seen in X of N runs".
  5. Rename the `compare` value or document both vocabularies in the schema `description`.

  Rejected: deleting the field (the reasons are useful), or keeping a constant (no information).
- **Regression test**: `internal/insights`: a synthetic long, well-sampled single capture gives `jank`/`long-tasks` = `medium` and `hot-function` = `medium`; a short sparse capture gives `low` with reasons. `internal/audit`: 3 runs with the finding → `high` and a reason containing `3 of 3 runs`; 1 of 3 → `low`.
- **Notes/uncertainty**: the exact thresholds are a product decision; the bug is that the current value is constant and the audit reason is wrong.

## BUG-O11: JSON Schema has no descriptions, units or enums; naming and frame shapes are inconsistent

- **Status**: NEW.
- **Severity**: low. All outputs conform, but the schema does not tell an agent what a field means or which values to expect.
- **Version**: v0.4.0 (`wkperf schema` is new).
- **Summary**: The `wkperf schema` output (93,075 B, 79 `$defs`, `additionalProperties: false` everywhere) has 606 properties, and none of them has a `description` keyword; only the schema root has one. String vocabularies are free `string`: `schema_Error.code`, `query_Result.status` (`ok`/`ambiguous`), `insights_Finding.confidence`, window `kind`, frame `category`. Enums exist only for `insights.Severity`, `scorecard.Rating`, `compare.Verdict` and flame `mode`, hand-coded in `internal/schema/schema.go:56,101-108`.
- **Reproduce**:

  ```bash
  wkperf schema > /tmp/s.json
  jq -c '[."$defs"[]|.properties|length?]|add' /tmp/s.json; jq '[.. | objects | select((.description? | type) == "string")] | length' /tmp/s.json
  jq -c '."$defs".schema_Error.properties.code, ."$defs".query_Result.properties.status, ."$defs".insights_Finding.properties.confidence' /tmp/s.json
  wkperf compare /tmp/demo/baseline-1.json --vs /tmp/demo/candidate-1.json --format json | jq -c .counts
  wkperf analyze $F --format json | jq -c '.reports[0] | {sourceMap: (has("sourceMap")), sourcemaps: (has("sourcemaps")), hotspotFrame: (.hotspots[0].commonStack[0]|keys), rootFrame: (.rootCause.hotPath.frames[0]|keys)}'
  ```

  Observed: `606` and `1`; the single `description` keyword is the schema root's (REFERENCE.md also lists only envelope field names). `{"type":"string"}` ×3. `{"Improved":…,"Regressed":…,"Unchanged":…,"Noisy":…}` uses PascalCase, next to camelCase analysis fields and snake_case query fields. `sourceMap` and `sourcemaps` are sibling keys with different meanings. Hotspot frames have `id` but no `category`, and root-cause frames have `category`/`native`/`anonymous` but no `id` (see BUG-O5).
- **Root cause** [VERIFIED]: `internal/schema/schema.go` reflects Go types, and its `reflect.String` case (`99-109`) adds enums for exactly three named string types. No field documentation is emitted. `compare.Counts` (`internal/compare/types.go:77-79`) has no JSON tags. There are two frame types (`metrics.FunctionFrame`, `flame.Frame`).
- **Impact**: agents must infer semantics and units from names; that cannot be validated.
- **Proposed fix**: Generate `description` (with the unit) from struct field doc comments or a tag (`jsonschema:"description=…,enum=…"`) in `internal/schema`. Replace the hard-coded type switch with typed string constants plus a registry, and add enums for `code`, `status`, `confidence`, window `kind` and frame `category`. Use one frame shape (`id` + location + `category`/`native`/`anonymous` + `original`) everywhere; the BUG-O1 frame table makes this natural. Tag `compare.Counts` in camelCase in the next schema version. Rename `sourcemaps` to `sourceMapPaths`.
- **Regression test**: `internal/schema/schema_test.go`: every leaf property has a non-empty `description`, and the listed string fields have `enum`s. The existing conformance tests keep validating real outputs.
- **Notes/uncertainty**: none.

## Fix plan for the coding agent

1. **BUG-O8** (empty window in compare/summary): small, independent, high value for CI. Do first.
2. **BUG-O5** part 1 (add `id` to `flame.Frame`; use `f_` IDs in attribution/periodic/paths commands; GC command → rootcause stat; Markdown escaping). This is a prerequisite for O1's frame table and makes every printed command runnable. Add the reproduce-command test (port `o-repro-commands.py`) now, so later steps cannot regress it.
3. **BUG-O1** (stack/frame tables, caps, `wkperf-insights/v3`, `wkperf-audit/v2`): the biggest change. It depends on step 2 for frame IDs. Update `internal/schema`, REFERENCE.md ("JSON lists are top-N with totals"), the HTML/insightshtml/audithtml consumers and `internal/ai` prompts, which read the same structs.
4. **BUG-O2 + BUG-O3 + BUG-O4** (Markdown/term rendering: block packer, term honours options, brief keys instead of commands, leaf-side abbreviation, all findings listed). These depend on O1's stack references for the `## Stacks` appendix. Do them in one pass over `internal/insights/render.go` and `cmd/analyze.go`/`cmd/markdown.go`.
5. **BUG-O6** (`query keys`, prefix/regex, `--offset`, `functions.f_<id>` keys, alias cleanup). It touches `internal/query`, `internal/insights/numeric.go` and MCP. Do it together with **BUG-O7** (`wkperf.query.v2` row shapes), since both change the query contract and should bump the version once.
6. **BUG-O9** (error-code vocabulary, query typed errors, `--out` stdout behaviour, md `## Errors`). Its schema enum work overlaps with O11.
7. **BUG-O10** (confidence model): independent of the above. The audit part needs per-run finding presence, which is computable from `Scene.Runs[].Report` (`internal/audit/types.go:14-18`) but currently unused by `buildPriorities`.
8. **BUG-O11** (schema descriptions/enums, naming): last, after the shapes have settled in steps 3–7.

Re-run after each step: `python3 docs/bug-reports/v0.4.0/fixtures/o-repro-commands.py --md <(wkperf analyze $F --format md) --json <(wkperf analyze $F --format json)` (expect 0 non-`ok` statuses after step 2), the `o-deep-stacks.jq` scaling loop (expect flat md size in K after steps 3–4), and schema validation of real outputs (below).

## Things checked that are fine

- **Printed values reproduce exactly.** Every `query … metrics KEY` / `query … rootcause KEY` command printed next to a number returned that number (relative tolerance 2e-5 for md `%.6g`; JSON values matched to rounding): 1,220/1,220 on the fixture's full md + JSON, 285/285 on `$R`, and 1,065/1,065 on a windowed report (`analyze $F --from 3 --to 20`). On the large captures, sampled checks matched 30+35, 37+35 and 24+34 values (brief + JSON) with 0 mismatches. The two `--from/--to` formats (`244.32019945827778` vs `244.320199458`) produce identical results.
- **`%00`-encoded function metric keys work** when copied verbatim (they are only opaque; see BUG-O6).
- **Schema conformance.** `wkperf schema` is valid Draft 2020-12. With python `jsonschema` 4.26.0, these validate against it, both via the root `oneOf` and via the matching `$defs` entry:
  - all five real `analyze --format json` outputs (up to 52 MB);
  - `audit --format json` (7 MB);
  - every error and partial-failure document in the matrix: bad, empty, non-recording and array JSON; missing file; empty and reversed window; good+bad; directory with a bad file; `--min-score`; `--strict-fixture`; unknown flag;
  - `compare`, `summary`, and `flame` json/diff/speedscope (flame diff needs a raised Python recursion limit, nesting depth 224);
  - all 15 query operations plus the ambiguous and error cases.

  No undeclared fields were found (`additionalProperties: false` throughout).
- **Claim 1 confirmed.** Wrapped exports load. Multi-file `analyze`/`audit` with a bad file keep valid reports, list per-file `errors[]`, print one JSON document on stdout and diagnostics on stderr, and exit 1. Bad, missing and empty inputs each produce a precise message.
- **Exit codes 2/3/4 behave as documented.**
  - `compare --strict-fixture --min-comparability 0.99` on the different-length pair exits 3 with `fixture_mismatch` merged into the comparison document.
  - `audit --min-score 99` exits 4 with `score_below_minimum`.
  - `--fail-on-regression` exits 0 when the only regression is in `network` with confidence `low`.
  - Unknown flags under `--format json` produce `wkperf-error/v1`.
  - `--format bogus` writes nothing to stdout and an error to stderr (exit 1).
- **`--max-bytes` basics.** JSON ignores `--max-bytes` as documented, including values below 64. For md, values below 64 are rejected with exit 1. Output never exceeded the budget in the 44 accepted runs, always ended on a line boundary with balanced backticks, and the marker was always present when truncating (its position is BUG-O2).
- **Determinism.** Three runs each of `analyze $F --format json` and `--format md --brief`, and two runs of `query $F metrics --limit 1000`, were byte-identical.
- **Query bounds.** `--limit` 0 → 50, values above 1000 are rejected, `truncated` and `total_count` are correct, and the ambiguous response lists candidates with `f_` IDs rather than guessing.
