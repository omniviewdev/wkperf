# wkperf v0.4.0: analysis correctness (area A)

Scope: idle-gap frame handling and the scorecard (claim 4), ownership and "nearest application ancestor" (claim 6), precision of every detector, and hot-path semantics. Analyst-only: no source, test or git change was made. The only files added are this report and the fixtures listed below.

## Method and conventions

- Version under test: `wkperf v0.4.0` on PATH (HEAD `392facf`). v0.3.0 was built from the existing throwaway worktree `/tmp/wkperf-v030` (`d9bdc33`, tag v0.3.0) into `/tmp/a3/wkperf-v030`. v0.3.0 cannot load wrapped exports, so it was run on `jq '.recording'` copies.
- Independent checks: I re-implemented `profile.Frames`, the exclusive-work sweep, `IsInputDriven`, `Categorize`, nearest-app attribution, the call tree and `HotPath` in Python and ran them on the raw JSON of all five profiles in `~/Scratch/profiles/`. Wherever the output says "matches", the Python value equals wkperf to the printed precision.
- Restriction because of BUG-S1 (see `01-sample-timestamps.md`): every sampler number in this report uses only the samples wkperf keeps (timestamp inside the active window: 38/159, 4/4, 185/591, 45/166, 229/2282). This separates logic bugs from the dropping of untimed samples. Sampler-based numbers will change after S1 is fixed. The logic bugs below do not depend on that.
- Times are seconds from recording start. `work` means wkperf's exclusive instrumented work (`profile.Measure`).
- All commands run from the repo root with `FX=docs/bug-reports/v0.4.0/fixtures`.

Fixtures (all in `$FX`):

| File | Size | Origin | Used by |
| --- | --- | --- | --- |
| `real-wrapped-fresh-record.json` | 388 KB | real (pre-existing copy of `fresh-record.json`) | A1, A5, A9, A10, A13, A14 |
| `a-real-pods-not-moving-idle-slice.json` | 20 KB | real: `pods-not-moving.json` records with start in frames 5–10 (t = 2.005–4.997 s), CPU threads with usage > 0 kept, screenshots/network dropped, no samples in that window | A1, A3 |
| `a-real-rapid-scrolling-deployment.json` | 33 KB | real: byte-identical copy of `rapid-scrolling-deployment.json`. It is also identical to `o-real-wrapped-rapid-scrolling-deployment.json`; keep one. | A2, A3, A5, A13 |
| `a-synthetic-nested-records.json` | 1.5 KB | synthetic: nested script records, paint/style inside composite | A5, A6, A7 |
| `a-synthetic-frames.json` | 1.5 KB | synthetic: a 1.2 s frame whose only work is a 0.05 ms `wheel` dispatch | A4 |
| `a-synthetic-react-ownership.json` | 67 KB | synthetic: React 19 commit/render stacks, Base UI `getAnimations` chain, Kubernetes plugin URL, 33-level commit recursion | A8–A11 |
| `a-synthetic-periodic-raf.json` | 18 KB | synthetic: 4 rAF callbacks 394 ms apart during `wheel` input, `getAnimations` inside `recalculate-styles` | A11, A12 |

Claim verdicts:

| Claim | Verdict | Where |
| --- | --- | --- |
| 4: `pods-not-moving` idle wakeups 66, Idle 93.6 %, but jank 100 %, 2.8 fps, grade D | CONFIRMED exactly. It rests on 5 active frames, with no minimum-sample policy. One of the 5 frames is active only because of a concurrent GC. | A1, A3, Appendix A |
| 6: React internal `commitDeletionEffectsOnFiber` and `getFiniteAnimations` listed as application code | CONFIRMED. `getFiniteAnimations` is Base UI library code. About 140 React-DOM internals are "app". The first-party Kubernetes plugin is "framework-library". The `paths ''` part is also confirmed, and is filed as BUG-O5 in `02-output-contract.md`. | A8 |
| 7: everything `confidence: low` | CONFIRMED (`internal/insights/evidence.go:183`), filed as BUG-O10. A15 adds a related severity contradiction. | A15 |

## BUG-A1: Scorecard grades frame standards from 1–5 active frames; no minimum-sample policy

- **Status**: CONFIRMED (claim 4)
- **Severity**: high. Frame standards carry 9 of 21 weight. An idle capture gets grade D or F from 1–5 intervals, so an agent chases "jank" that does not exist. The grade then moves randomly between captures and breaks before/after verification.
- **Version**: v0.4.0. v0.3.0 also graded, but from all frames: the slice fixture scored D with p95 872.6 ms and 2.01 fps over 6 frames, because v0.3.0 had no idle-gap logic. v0.4.0 removes the idle frames but grades the 1 that remains.
- **Summary**: `pods-not-moving.json` has 71 rendering-frame intervals. 66 are idle gaps; the remaining 5 active frames (1.76 s of 32.6 s) are all > 150 ms. jank-ratio 1.0, fps 2.84 and p95 761.5 ms are rated "poor" and the grade is D (43.75). Without the frame standards, every other available standard scores 100. The frame results carry no `detail`, so the agent cannot see that N = 5.
- **Reproduce**:

  ```bash
  wkperf analyze $FX/a-real-pods-not-moving-idle-slice.json --format json | jq -c '.reports[0].scorecard | {score, grade, frames: [.results[] | select(.id=="frame-p95" or .id=="jank-ratio" or .id=="fps") | {id, value, rating, detail}]}'
  wkperf query $FX/a-real-pods-not-moving-idle-slice.json metrics --format tsv | grep -E '^frames\.(count|idle_wakeups|jank_ratio|fps)'
  wkperf query $FX/real-wrapped-fresh-record.json metrics --limit 1000 --format tsv | grep -E '^frames\.(count|max_ms|jank_ratio|fps)'
  ```

  Observed:

  ```text
  {"score":43.12587273777502,"grade":"D","frames":[{"id":"frame-p95","value":853.4609166672453,"rating":"poor","detail":null},{"id":"jank-ratio","value":1,"rating":"poor","detail":null},{"id":"fps","value":1.171699817145685,"rating":"poor","detail":null}]}
  frames.count	1	count
  frames.fps	1.171699817	fps
  frames.idle_wakeups	5	count
  frames.jank_ratio	1	ratio
  frames.count	2	count
  frames.fps	0.144357158	fps
  frames.jank_ratio	0.5	ratio
  frames.max_ms	13841.896083322	ms
  ```

  fresh-record gets grade F (30.98) from 2 frames, one of which is 12.6 ms. On the full `pods-not-moving.json`: `frames.count 5`, `frames.idle_wakeups 66`, `frames.idle_gap_ms 30776.8`, `frames.mean_ms 351.69`, `frames.fps 2.8434`, `frames.p95_ms 761.51`, `jank_ratio 1`, score 43.75, grade D. My Python re-implementation gives the same values for all five profiles (Appendix A).
  Expected: with fewer than a documented minimum of active frames, `frame-p95`, `jank-ratio` and `fps` are `n/a` with a detail such as `insufficient data: 1 active frame (minimum 30), 5 idle gaps`.
- **Root cause** [VERIFIED]: `internal/analyze/analyze.go:379-411` emits `frames.*` whenever `len(frames) > 0`. `internal/scorecard/scorecard.go:105-107` copies them, and `build` (`scorecard.go:281-309`) rates any present value. There is no frame-count gate, although the same file already has minimum-sample policies (`minMemSamples = 5`, `inpMinForP98 = 50`, `scorecard.go:35-38`). Frame results get no `detail` (`scorecard.go:97-104` sets only `v`).
- **Impact**: `scorecard` in every format, `audit` scene ratings (it uses `frames.p95_ms`, `internal/audit/audit.go:154`), the `compare` representative choice (`frames.p95_ms`) and AI prompts all present 1–5-frame statistics as grades.
- **Proposed fix**: Add `MinActiveFrames = 30` and `MinActiveFrameMs = 1000` to `scorecard`. If `frames.count < 30` or total active-frame time is < 1 s, the three frame standards are `Unavailable` with detail `insufficient data: N active frames (min 30), M idle gaps`. When rated, they get detail `N active frames, M idle gaps`. Expose `frames.active_ms` from `Summarize`. Document both constants in REFERENCE "Scorecard". Thirty is the usual small-sample floor and makes p95 distinct from max. The exact value is a product choice, but it must be a documented constant. Rejected: weighting frame standards by frame count (opaque, still grades 1 frame), and keeping the grade with a warning (agents read the grade).
- **Regression test**: `internal/scorecard`: `Evaluate` on `a-real-pods-not-moving-idle-slice.json` (decode via `cmd` loader, or inline the 6 frames) makes all three frame standards `n/a` with detail containing `insufficient data`. A synthetic 40 × 40 ms frame capture is still rated. `internal/analyze`: `frames.active_ms` is present.
- **Notes/uncertainty**: A3 and A4 change which frames count as active. Apply this gate after the active-frame definition is fixed. After A3, `pods-not-moving` has 4 active frames.

## BUG-A2: A letter grade is computed from one third of the weight; a broken "scrolling" capture gets grade A

- **Status**: NEW
- **Severity**: medium. `rapid-scrolling-deployment.json` contains no layout, paint or composite records. Its only rendering frame is a 26.6 s idle gap, yet it scores A (94.0). An agent concludes the scene is healthy.
- **Version**: v0.4.0. v0.3.0 gave F (37.2) by grading the 26.6 s frame as a frame. Both versions are wrong.
- **Summary**: The overall score is the weighted mean over available standards only. No coverage figure is reported, and no grade is withheld. Here 7 of 21 weight is available (tbt, long-task-rate, main-busy, gc-share, forced-layout-rate). `forced-layout-rate` is rated "good, 0 synchronous layouts" in a capture that has zero layout records, because the layout instrument is listed.
- **Reproduce**:

  ```bash
  wkperf analyze $FX/a-real-rapid-scrolling-deployment.json --format json | jq -c '.reports[0] | {score: .scorecard.score, grade: .scorecard.grade, rated: [.scorecard.results[] | select(.rating!="n/a") | "\(.id)=\(.rating)"], availableWeight: ([.scorecard.results[] | select(.rating!="n/a") | .weight] | add), totalWeight: ([.scorecard.results[].weight] | add), wakeups: .wakeups.count}'
  jq '[.recording.records[] | select(.type=="timeline-record-type-layout")] | length' $FX/a-real-rapid-scrolling-deployment.json
  ```

  Observed: `{"score":94.01375631738323,"grade":"A","rated":["tbt=good","long-task-rate=needs-improvement","main-busy=good","gc-share=good","forced-layout-rate=good"],"availableWeight":7,"totalWeight":21,"wakeups":1}` and `0`.
  Expected: `grade: "n/a"` (insufficient data) with `coverage: 0.33`, and frame standards `n/a`.
- **Root cause** [VERIFIED]: `internal/scorecard/scorecard.go:298-301`: `c.Score = ssum / wsum` over available weight, and `grade(c.Score)` is applied regardless of `wsum`. `scorecard.go:189` rates `forced-layout-rate` when `layouts > 0 || instrumentAvailable(rec, layout)`.
- **Impact**: `analyze` scorecards, `audit` per-scene and overall scores, and `--min-score` gating. A capture missing whole instrument classes can pass a CI gate.
- **Proposed fix**: Add `coverage = wsum / totalWeight` to `Scorecard` (JSON + render). Set `Grade = "n/a"` and omit `Score` (or mark it `partial`) when `coverage < 0.5` or when all three frame standards are unavailable. Keep the per-standard ratings. Rejected: renormalizing silently (today's behavior), and treating n/a as 0 (punishes captures that legitimately lack page-load events).
- **Regression test**: `internal/scorecard`: `Evaluate` on `a-real-rapid-scrolling-deployment.json` gives `Grade == "n/a"`, `Coverage ≈ 7/21`. Existing `TestIdleSafeFrameStandards` (`regression_test.go:10`) must also assert a grade of `n/a`.
- **Notes/uncertainty**: The 0.5 threshold is a choice. The point is that a grade from 7/21 weight must be visibly partial. Whether `forced-layout-rate` should require at least one layout record is debatable, because an idle page can have zero layouts legitimately. I only recommend that coverage reflect it.

## BUG-A3: Garbage-collection intervals are treated as exclusive main-thread work

- **Status**: NEW
- **Severity**: high. A GC-only interval becomes the #1 slow frame and the top triage window. GC gets the highest precedence in the 100 % breakdown and can hide script time. `gc-pressure` calls collections "pauses". CPU data in the same captures shows the main thread mostly idle (4–5 %) during full collections.
- **Version**: v0.4.0. v0.3.0 also gave GC first priority in the breakdown (`/tmp/wkperf-v030/internal/insights/breakdown.go:96`). v0.3.0 had no idle-gap logic, so frames were affected differently.
- **Summary**: WebKit `garbage-collected` records (`details.__type == "GarbageCollection"`, `type` `full`/`partial`) span a collector cycle. wkperf counts them as main-thread work: `profile.category` maps them to `kindGC`, which the sweep ranks above all other kinds. In the `pods-not-moving` frame at t = 3.002 s (853.5 ms), 131.6 ms of the 138.6 ms work is one `full` GC. Without GC the frame has 7.0 ms of work (0.8 %) and would be an idle gap. The CPU record covering that GC shows main thread 4.8 % and four collector threads at about 12 % each. The jank finding then names "layout/style" as the dominant cause, because GC is not one of its buckets.
- **Reproduce**:

  ```bash
  wkperf analyze $FX/a-real-pods-not-moving-idle-slice.json --format json | jq -c '.reports[0] | {slowFrames: [.slowFrames[] | {t, ms, scriptMs, layoutMs, paintMs, gcMs}], jank: (.findings[] | select(.id=="jank") | .evidence[2]), gc: (.findings[] | select(.id=="gc-pressure") | .evidence[0])}'
  jq -r '.recording as $r | ($r.records[] | select(.eventType=="garbage-collected") | "GC t=\(.startTime-$r.startTime|.*1000|round/1000)s..\(.endTime-$r.startTime|.*1000|round/1000)s dur=\((.endTime-.startTime)*1000|.*10|round/10)ms type=\(.details.type)"), ($r.records[] | select(.type=="timeline-record-type-cpu") | "cpu t=\(.timestamp-$r.startTime|.*1000|round/1000)s total=\(.usage|.*10|round/10)% " + ([.threads[]|"\(.name):\(.usage|.*10|round/10)"]|join(" ")))' $FX/a-real-pods-not-moving-idle-slice.json
  ```

  Observed (excerpt; CPU lines for t = 0.021, 0.524 and 2.534 s omitted):

  ```text
  {"slowFrames":[{"t":0.9978331250022165,"ms":853.4609166672453,"scriptMs":3.0379583477042615,"layoutMs":3.436999919358641,"paintMs":0.5440416862256825,"gcMs":131.62383332382888}],"jank":"dominant cause across slow frames: layout/style (script 3.0ms, layout 3.4ms, paint 0.5ms)","gc":"1 GC pauses, 131.6ms total (4.4% of wall time), 0.3/s"}
  GC t=1.414s..1.545s dur=131.6ms type=full
  cpu t=1.024s total=13.3% Main Thread:12.9 JavaScriptCore libpas scavenger:0.1 JIT Worklist Helper Thread:0.3
  cpu t=1.529s total=54.2% Main Thread:4.8 JavaScriptCore libpas scavenger:0.1 Heap Helper Thread:12.0 Heap Helper Thread:12.6 Heap Helper Thread:12.4 JSC Heap Collector Thread:12.3
  cpu t=2.032s total=41.1% Main Thread:14.0 ... Heap Helper Thread:6.0 Heap Helper Thread:6.1 Heap Helper Thread:6.0 JSC Heap Collector Thread:6.1
  ```

  The CPU window (1.024, 1.529] contains 115 ms of the GC. If the GC blocked the main thread, main-thread usage would be at least 23 %; it is 4.8 %, lower than the neighbouring windows (12.9 %, 14.0 %). The same pattern holds in `$FX/a-real-rapid-scrolling-deployment.json`: a 525.7 ms `full` GC at t = 9.075–9.601 s, and the CPU record at t = 9.682 shows `Main Thread:4.3 Heap Helper Thread:4.2 … JSC Heap Collector Thread:3.0` (about 26 ms of main-thread time in that 0.6 s window).
  Expected: the frame is an idle gap (work 7.0 ms excluding GC). Jank, if reported at all, does not cite this frame. `gc-pressure` says "collections", not "pauses".
- **Root cause** [VERIFIED]:
  - `internal/profile/profile.go:129-135`: `category` maps GC to `kindGC`. `profile.go:186-197` picks the highest active kind, and `kindGC` is the highest (`profile.go:90-97`).
  - `profile.go:372`: `IdleGap` uses `o.WorkMs`, which includes GC.
  - `internal/insights/rules.go:333-345`: the jank cause buckets omit `GCMs`.
  - `rules.go:441`: the text says "GC pauses".
  - `internal/rootcause/windows.go:205` labels GC overlaps "Instrumented GC pause".
  - Script hiding, measured on `rapid-scroll-deployment.json` with my union computation: a 256.7 ms `partial` GC lies entirely inside the 281 ms `scroll` handler. That time is credited to GC, not Script. Its CPU window shows main thread 38.5 % vs 49–59 % in the neighbouring windows, so whether a partial collection blocks the mutator is not established.
- **Impact**: frame classification (idle vs active), `slowFrames`, `jank`, the scorecard frame standards, `rootCause.windows` ranking (the GC frame is the top window in `pods-not-moving`), the breakdown `GC` and `Script` slices, and `gc-pressure` wording. An agent is sent to "reduce allocation churn" for a concurrent collection, and to layout/style work that is 3.4 ms.
- **Proposed fix**: Treat GC as an overlay, not as main-thread work:
  1. Remove `kindGC` from the exclusive sweep used for `Occupancy.WorkMs`, idle-gap classification, slow-frame work and window `activeMs`.
  2. Report GC beside the breakdown as `gcMs` and `gcOverlapWithWorkMs`, not as a slice of the 100 %.
  3. Rename to "GC collections (collector interval; not necessarily main-thread time)".
  4. Optional cross-check: when CPU records exist, report main-thread CPU during each GC interval.

  Rejected: keeping GC at top precedence (contradicted by per-thread CPU), dropping GC entirely (loses the allocation-pressure lead), and giving GC the lowest precedence inside the 100 % (it would still add concurrent time to a main-thread partition).
- **Regression test**: `internal/profile`: a frame of 853 ms containing a 131.6 ms GC record and 7 ms of script is `IdleGap == true`. `internal/insights` on the slice fixture: `slowFrames` is empty and no `jank` finding exists. `gc-pressure` evidence contains "collections", not "pauses". The breakdown has no `GC` slice inside the 100 %.
- **Notes/uncertainty**: The per-thread CPU evidence shows full collections are not main-thread time in these captures. I did not trace JavaScriptCore source, and partial (eden) collections during allocation may block the mutator. That is why I propose an overlay with an explicit overlap value rather than a guess.

## BUG-A4: "Slow frames" and jank are measured on interval length; near-empty input frames dominate p95, impact, window ranking and the "not attributed" slice

- **Status**: NEW
- **Severity**: high. The worst-ranked frames and windows contain about 1 % work, and the jank finding still says "Frames are dominated by JavaScript" with 79–98 % "impact". An agent looks for main-thread work where there is none.
- **Version**: v0.4.0. v0.3.0 had no idle-gap or input logic and graded all frames by interval as well, so it had the same metric with worse classification.
- **Summary**: In these WKWebView captures, rendering-frame records tile the whole timeline. In the three captures with many composites, every frame that contains a composite ends 44–183 ms after its last instrumented work: median 125.3 ms over 70 frames in `pods-not-moving`, 111.9 ms over 67 frames in `rapid-scroll-deployment`, 54.3 ms over 4 frames in `pods-scrolling-to-failure`. In `fresh-record` the tail is 10–35 ms. A frame is active if any input-driven dispatch overlaps it, however little work it contains. Then its full interval counts toward p95/jank/fps, the jank impact (`Σ (duration − 16.7)`) and the breakdown slice "Frame time not attributed to work". `rootCause.windows` ranks windows by interval duration.
  - `pods-scrolling-to-failure`: frame 5 (t = 1.861, 2189 ms) has 25.2 ms of work and 792 `wheel` dispatches (21 ms), with no composite. It is slow frame #1 and window #1 (top stack `fetch`), and it contributes 2172 ms of the 3962 ms jank impact (97.8 % of wall, severity high).
  - `rapid-scroll-deployment`: 8 active frames are "input-only" (work < 10 %), for example frame 1 (1655.6 ms, 39.8 ms work, 12 `wheel` + 4 `scroll`, window #1) and frames 67–68 (≈120 ms, 0.4–2.8 ms work). The jank impact is 14 009 ms (79.5 %), while all instrumented work inside the 66 slow frames, including 374.8 ms of GC, is 6164 ms.
  - "Frame time not attributed to work" is 70.3 % of `pods-scrolling-to-failure`. Over that capture, the CPU records give 1028 ms of main-thread CPU, against 1009 ms of instrumented non-GC work. That 2848 ms slice therefore had no main-thread CPU: it is idle, not unknown work.
- **Reproduce**:

  ```bash
  wkperf analyze $FX/a-synthetic-frames.json --format json | jq -c '.reports[0] | {slowFrames: [.slowFrames[] | {t, ms, scriptMs, paintMs, topEvent}], notAttributed: (.breakdown[] | select(.name=="Frame time not attributed to work") | .ms), jank: (.findings[] | select(.id=="jank") | {severity, impactMs, e: .evidence}), windows: [.rootCause.windows[] | {kind, label, durationMs, activeMs}]}'
  ```

  Observed (abridged): `slowFrames` = `{"t":0,"ms":1200.0,"scriptMs":0.05,"paintMs":0.3,"topEvent":"event-dispatched wheel"}`, `{"t":1.2005,"ms":299.5,"scriptMs":200.0,…}`; `notAttributed` 1291.15; jank `{"severity":"high","impactMs":1466.1,"e":["frame time p95 1155.0ms vs 16.7ms budget","2 of 2 frames over budget (100.0%)","dominant cause across slow frames: script (script 200.1ms, layout 0.0ms, paint 8.3ms)"]}`; `windows[0]` = `{"kind":"slow-frame","label":"event-dispatched","durationMs":1200,"activeMs":0.35}`, and the 200 ms `scroll` long task is third.
  Expected: the 1.2 s frame (0.35 ms work) is reported as an input stall ("input received, no rendering update for 1.2 s, main thread did 0.35 ms"), not as a work-bound slow frame. It is excluded from jank p95/impact and from "not attributed to work". The top window is the 200 ms scroll handler.
- **Root cause** [VERIFIED]:
  - `internal/profile/profile.go:372`: `IdleGap = … && !input`. Any overlapping input-driven record (`IsInputDriven`, `profile.go:250-291`) forces "active", whatever the work.
  - `internal/analyze/analyze.go:257` collects `f.DurationMs` for every non-idle frame.
  - `internal/insights/rules.go:322-327`: impact is the interval excess.
  - `internal/insights/breakdown.go:52-58`: every active-frame millisecond without work becomes "Frame time not attributed to work".
  - `internal/rootcause/windows.go:96-110`: `windowLess` sorts by `DurationMs`, and `windows.go:148` passes interval duration with work as a separate `ActiveMs`.
  - Frame tiling and tails were measured from the raw records: for each frame I took the end of the last exclusive work span and compared it with the frame end (Appendix A, column `comp#`; tails are in the summary above).
- **Impact**: `slowFrames`, `jank` (severity, impact, suggestion), the scorecard frame standards, `rootCause.slowFrames` and `windows` ordering, `--brief` "worst windows", and the breakdown. Whether the input frames are real user-visible stalls is a separate question: the 2.19 s period with 792 wheel events and no composite in `pods-scrolling-to-failure` may well be a real freeze. Either way, its cause is not main-thread work, and the suggestion text sends the agent the wrong way.
- **Proposed fix**: Classify frames into three kinds in `profile.Frames`, using work without GC (A3):
  - `idle-gap`: unchanged rule.
  - `input-stall`: input present, `duration > budget`, `workMs ≤ budget` (16.7 ms).
  - `active`: everything else.

  Then:
  1. Frame statistics and jank use `active` only.
  2. Input stalls get their own metrics (`frames.input_stalls`, `frames.input_stall_max_ms`) and their own finding ("rendering did not update for X ms during input; main thread did Y ms of work; look for awaited async work, IPC or suppressed rendering"), not the JS-cost suggestion.
  3. Jank impact becomes `Σ max(0, workMs − budget)` over active slow frames; the interval excess becomes a separately named value.
  4. `windowLess` sorts by `ActiveMs` first, then `DurationMs`.
  5. Rename the slice to "Active-frame time without instrumented main-thread work". When CPU records exist, add the estimated main-thread CPU for those spans so idle and uninstrumented time can be told apart.

  Rejected:
  - Dropping the input override: it would hide real stalls such as the pods-scrolling freeze.
  - Trimming frames at the last composite: the frame boundary semantics are not established, and idle gaps between tasks remain.
  - Using the work ratio alone: a 120 ms frame with 0.4 ms work and an input event is not a work-bound frame at any ratio.
- **Regression test**: `internal/profile`: in `a-synthetic-frames.json` frame 1 is `input-stall` and frame 2 is `active`. `internal/insights`: `slowFrames` has 1 entry (299.5 ms), jank impact ≤ 200 − 16.7 ms, a new input-stall finding exists with 1200 ms, and "not attributed" excludes the stall. `internal/rootcause`: `windows[0]` has `activeMs ≥ 200`.
- **Notes/uncertainty**: I do not claim why WebKit frames end about 120 ms after the composite. The report should not depend on it. The proposal measures work, which is defined, and keeps the interval as a separately labeled observation.

## BUG-A5: Nested script records are counted as separate long tasks: TBT, long-task rate, the long-task list and the finding total are inflated ~1.7–2×

- **Status**: NEW
- **Severity**: high. TBT and long-task rate are the main responsiveness numbers an agent uses to verify a fix. Here they double-count. The long-task list also shows each task twice (event, then the microtask checkpoint inside it).
- **Version**: v0.4.0 and v0.3.0 (the nested fixture gives 5 long tasks, TBT 2888 in both).
- **Summary**: WebKit nests the microtask checkpoint inside the dispatching task. Examples: `event-dispatched scroll` 164.0 ms contains `microtask-dispatched` 162.1 ms, and `timer-fired` 212.2 ms contains `microtask-dispatched` 209.6 ms. wkperf treats every script record > 50 ms as its own task.
  - `pods-scrolling-to-failure`: 8 long tasks, TBT 1937.3 ms/10 s and "1184.7 ms total (29.2 % of wall time)". There are 5 top-level tasks: TBT 1035.3 ms/10 s, union 669.3 ms (16.5 %), rate 74.1/min instead of 118.5/min.
  - `rapid-scroll-deployment`: 10 tasks become 6, TBT 479.1 becomes 279.5, total 1344.7 ms becomes a union of 792.7 ms.
  - `fresh-record`: 10 tasks become 6, TBT 624.9 ("poor") becomes 364.3 ("needs-improvement").
  - `rapid-scrolling-deployment`: 3 tasks become 2, TBT 60.6 becomes 50.6.
- **Reproduce**:

  ```bash
  wkperf analyze $FX/a-synthetic-nested-records.json --format json | jq -c '.reports[0] | {longTasks: [.longTasks[] | "\(.eventType) \(.ms|round)ms"], tbt: (.scorecard.results[] | select(.id=="tbt") | {value, detail}), rate: (.scorecard.results[] | select(.id=="long-task-rate") | .value), finding: (.findings[] | select(.id=="long-tasks") | .evidence[0])}'
  jq -r '.recording as $r | [$r.records[] | select(.type=="timeline-record-type-script" and .eventType!="garbage-collected" and ((.endTime-.startTime)>0.05))] as $L | $L[] | . as $x | "\(.eventType) t=\(.startTime-$r.startTime|.*1000|round/1000) \((.endTime-.startTime)*1000|.*10|round/10)ms nestedIn=\([$L[] | select(.startTime<=$x.startTime and .endTime>=$x.endTime and ((.endTime-.startTime)>($x.endTime-$x.startTime))) | .eventType] | join(","))"' $FX/real-wrapped-fresh-record.json
  ```

  Observed:

  ```text
  {"longTasks":["timer-fired 212ms","microtask-dispatched 210ms","event-dispatched 164ms","microtask-dispatched 162ms","observer-callback 80ms"],"tbt":{"value":2888.0000000000196,"detail":"5 long tasks, worst 212ms"},"rate":150,"finding":"5 tasks > 50.0ms, 827.6ms total (41.4% of wall time)"}
  microtask-dispatched t=1.741 477.4ms nestedIn=timer-fired
  microtask-dispatched t=2.221 336.4ms nestedIn=timer-fired
  microtask-dispatched t=2.557 633.6ms nestedIn=timer-fired
  microtask-dispatched t=3.198 74.7ms nestedIn=timer-fired
  microtask-dispatched t=3.485 90.7ms nestedIn=
  timer-fired t=1.72 1553.2ms nestedIn=
  script-evaluated t=13.21 78.0ms nestedIn=
  event-dispatched t=13.531 110.8ms nestedIn=
  event-dispatched t=13.644 148.9ms nestedIn=
  timer-fired t=221.578 166.8ms nestedIn=
  ```

  Expected for the synthetic fixture: 3 tasks (212, 164, 80 ms), TBT (162 + 114 + 30) / 2 s × 10 = 1530 ms, rate 90/min, total 456 ms (22.8 %).
- **Root cause** [VERIFIED]:
  - `internal/insights/windows.go:17-62` (`LongTaskRecords`) iterates over records without merging nesting.
  - `internal/scorecard/scorecard.go:135-150` adds `d − 50` per record.
  - `internal/insights/rules.go:241-244` sums all `c.long`.
  - `internal/rootcause/windows.go:114-133` dedupes only identical `label+from+to`.
- **Impact**: the scorecard `tbt` and `long-task-rate` (rating flips in fresh-record), `longTasks[]`, the `long-tasks` finding (count, total, severity), `rootCause.longTasks`, `windows`, and `compare` deltas, which change if nesting differs between runs.
- **Proposed fix**: Add one shared helper, `profile.Tasks(rec)`. It returns maximal unions of overlapping non-GC, non-count-only script records. Each task carries the outermost (longest) record as its label and the nested event types as `children`. Use it in `LongTaskRecords`, the scorecard TBT/rate, `ruleLongTasks` and the root-cause long-task windows. Design decision: overlap means `b.start < a.end`; touching endpoints stay separate tasks; a microtask-only long task (fresh-record t = 3.485) stays a task. Rejected: dropping `microtask-dispatched` records from long-task detection, which would lose microtask-only tasks, and deduping by identical span, which is today's behavior.
- **Regression test**: `internal/insights` + `internal/scorecard` on `a-synthetic-nested-records.json`: 3 long tasks; TBT ≈ 1530; `long-task-rate` 90; finding total 456 ms. On the fresh-record fixture: 6 long tasks, TBT ≈ 364.3.
- **Notes/uncertainty**: none. The nesting is visible in raw records in all five captures.

## BUG-A6: `microtask-heavy`, `observer-callback-heavy` and microtask trigger rows never fire on real exports (wrong event-type names)

- **Status**: NEW
- **Severity**: medium. Microtask work is 11.6 % of `rapid-scroll-deployment` and 14.2 % of `pods-scrolling-to-failure`, yet it never appears as a finding or trigger row. The bug is hidden because the test generators use the same wrong names.
- **Version**: v0.4.0 and v0.3.0 (same strings in `/tmp/wkperf-v030/internal/insights/rules.go:203-205`).
- **Summary**: Real Safari exports use `eventType` `microtask-dispatched` and `observer-callback`. All five profiles have `microtask-dispatched` (68/39/990/237/1650 records), and three have `observer-callback`. wkperf matches `microtask-fired` and `observer-callback-fired`, which do not occur. REFERENCE promises "Trigger tables separate event names and timer/rAF/microtask dispatches".
- **Reproduce**:

  ```bash
  wkperf analyze $FX/a-synthetic-nested-records.json --format json | jq -c '.reports[0] | {findings: [.findings[].id], triggers: [.triggers[] | "\(.kind)/\(.name)"]}'
  wkperf query $FX/a-synthetic-nested-records.json metrics --limit 1000 --format tsv | grep -E 'script\.(microtask-dispatched|observer-callback)\.total_ms'
  jq -r '[.recording.records[] | select(.type=="timeline-record-type-script") | .eventType] | group_by(.) | map("\(.[0])=\(length)") | join(" ")' $FX/real-wrapped-fresh-record.json
  ```

  Observed: `{"findings":["long-tasks","event-handler-heavy","low-sample-coverage"],"triggers":["timer/timer-fired","dispatch/scroll"]}`; `script.microtask-dispatched.total_ms 371.6`, `script.observer-callback.total_ms 80`. The real fixture contains `microtask-dispatched=68` and no `microtask-fired`.
  Expected: `microtask-heavy` (371.6 ms, 18.6 % ≥ 3 %) and `observer-callback-heavy` (80 ms, 4 %) fire, and triggers include `microtask` and `observer` rows.
- **Root cause** [VERIFIED]:
  - `internal/insights/rules.go:217` and `219`.
  - `internal/insights/evidence.go:76-78` (metric keys `script.microtask-fired.total_ms`, `script.observer-callback-fired.total_ms`).
  - `internal/insights/windows.go:78-80`.
  - `internal/insights/triggers.go:28` (no observer kind at all).
  - Tests and generators use the wrong names, so they pass: `internal/testgen/realistic.go:104,122`, `internal/insights/regression_test.go:120`, `internal/rootcause/evidence_test.go:134`.
- **Impact**: two catalogue findings are dead, trigger tables omit microtasks, and AI prompts never see microtask cost.
- **Proposed fix**: Replace the names with `microtask-dispatched` and `observer-callback` in all the places above, and add `observer-callback` → kind `observer` in `triggerKind`. Fix `testgen` and the tests to emit real names. Clean cutover: no alias for the never-exported names. Because microtask checkpoints nest inside events and timers (A5), the `microtask-heavy` evidence must say "overlaps event/timer time", or use time not covered by an enclosing record. Rejected: accepting both spellings, which would keep an invented name alive.
- **Regression test**: `internal/insights` on `a-synthetic-nested-records.json`: `microtask-heavy` and `observer-callback-heavy` are present, and `triggers` has kinds `microtask` and `observer`. Add a test that every event type used by rules appears in a real-fixture allowlist taken from `real-wrapped-fresh-record.json`.
- **Notes/uncertainty**: none.

## BUG-A7: Exclusive breakdown gives a fixed precedence to composite over paint, style and script nested inside it; Paint is always 0 on real captures

- **Status**: NEW (regression vs v0.3.0 for paint and style)
- **Severity**: medium. "Paint 0 %, Composite 14 %" sends an agent away from paint cost. The `style-recalc-heavy` finding contradicts the breakdown.
- **Version**: v0.4.0. v0.3.0 gives Paint 6, Style 2, Composite 2 on the fixture. Its order GC > Script > Style > Layout > Paint > Composite was right for composite but wrong for layout inside script.
- **Summary**: In the four captures that have paint records, every paint lies inside a `composite` record (fresh 3/3, pods-scrolling 233/233, pods-not-moving 140/140, rapid-scroll 4936/4936). Some `recalculate-styles`, `layout` and script records do too. The sweep picks the highest kind in the order GC > composite > paint > layout > style > script. So the composite parent swallows its children, and Paint is always 0.
  - `rapid-scroll-deployment`: an innermost-record partition gives Paint 143.8 ms (wkperf 0), Style 1882.6 (1735.4), Script 1972.1 (1826.7), Layout 370.3 (353.0), Composite 2025.6 (2479.3).
  - `pods-not-moving`: Composite 84.7 instead of 157.0, Script 276.1 instead of 213.5.
  - `style-recalc-heavy` says "131 recalculate-styles records, 1882.6 ms total (10.7 %)" while the breakdown says Style 1735.4 ms (9.84 %).
- **Reproduce**:

  ```bash
  wkperf analyze $FX/a-synthetic-nested-records.json --format json | jq -c '[.reports[0].breakdown[] | select(.name|test("Script|Style|Paint|Composite")) | {(.name): .ms}] | add'
  jq '.recording.records[] | select(.type=="timeline-record-type-layout") | {eventType, startTime, endTime}' $FX/a-synthetic-nested-records.json
  ```

  Observed: `{"Script":455.99999999998886,"Style":0,"Paint":0,"Composite":10.000000000005116}` for `composite [101.000,101.010]` ⊃ `paint [101.001,101.007]` and `recalculate-styles [101.0075,101.0095]`. v0.3.0 on the unwrapped copy: `Style=2, Paint=6, Composite=2`.
  Expected: Paint 6, Style 2, Composite 2.
- **Root cause** [VERIFIED]: `internal/profile/profile.go:186-197` chooses `for kind := kindCount-1; kind >= 0` (the fixed order of `profile.go:90-97`). The comment at `profile.go:75-78` says "More specialized records replace enclosing script coverage", but a fixed order cannot satisfy both script ⊃ layout and composite ⊃ paint/style/script. `internal/insights/rules.go:281-292` reports raw record sums, while the breakdown is exclusive.
- **Impact**: the breakdown in all formats, `slowFrames.paintMs` (which is paint + composite, `internal/insights/analyze.go:276-277`, so it hides this), `layout.total_ms`, scorecard `main-busy` (script share), A4/A3 frame work splits, and the `layout-heavy`/`style-recalc-heavy`/`paint-heavy` vs breakdown consistency.
- **Proposed fix**: Innermost record wins. In the sweep, attribute each elementary segment to the active record with the latest start; on ties, the shorter one; then the current kind order. For properly nested WebKit records this is the deepest record. GC is handled as in A3. Make the layout-kind findings report exclusive time with the raw sum as a secondary value, or label the raw sum "inclusive record time". Rejected: another fixed order (paint > composite fixes paint but breaks script nested in composite), and v0.3.0's order (credits forced layout inside script to Script).
- **Regression test**: `internal/profile`: `Measure` on the nested fixture gives Paint 6, Style 2, Composite 2. A script ⊃ layout case still credits the layout to Layout. `internal/insights`: the `style-recalc-heavy` ms equals the breakdown Style ms on a fixture where they should be equal.
- **Notes/uncertainty**: I did not check whether WebKit always nests style updates inside composite. The data shows 38/131 `recalculate-styles` records inside composite in `rapid-scroll-deployment`. Innermost-wins does not depend on the nesting rule.

## BUG-A8: Ownership classification marks React-DOM and Base UI internals as "app" and the first-party plugin as "framework-library"; "nearest application ancestor" points at library code

- **Status**: CONFIRMED (claim 6, classification part; the `paths ''` part is BUG-O5)
- **Severity**: high. This is the output an agent uses to decide which source to change. On the real captures, the top "application" ancestors are `getFiniteAnimations` (Base UI), `commitDeletionEffectsOnFiber`, `setValueForStyle`, `commitHookEffectListUnmount`, `insertOrAppendPlacementNode` (React DOM), React's anonymous microtask callback and `pop` (React's fiber stack). The real component code is classified framework.
- **Version**: v0.4.0 (ownership is new in v0.4.0; v0.3.0 has no `rootcause` package).
- **Summary**: `flame.Categorize` recognizes React by 36 exact names plus 5 prefix rules. Any other frame with a URL is `app`, unless the URL contains `node_modules/`, `vendor` or `plugin(s)`. In these Vite bundles React, Base UI, the Wails runtime and the app share `wails://localhost/assets/index-GgPenX8q.js`. As a result:
  - About 140 distinct React-DOM/reconciler function names seen in the samples are `app`. I identified them by name and by position in the bundle's React-DOM region, lines 446 188–459 202. Their self time is 153.7 of 269.6 "app" ms in fresh-record, 39.8/77.0 in pods-not-moving, 36.9/112.9 in pods-scrolling and 32.2/69.8 in rapid-scroll. The rest of "app" is mostly Base UI, the Wails runtime and `user-script:N` (WKWebView-injected scripts).
  - `getFiniteAnimations` is Base UI's helper (`@base-ui/react` v1.9.0 `packages/react/src/utils/getFiniteAnimations.ts`). It is called from `exec` in `internals/useAnimationsFinished.ts` and scheduled by `Scheduler.tick` in `@base-ui/utils/useAnimationFrame`. That is exactly the sampled chain `tick → (anonymous) → exec2 → getFiniteAnimations → getAnimations`.
  - The 60 distinct frames in `wails://localhost/_/plugins/kubernetes/assets/entry2.js` are `framework-library`. That bundle holds the code under test: `ResourceTableContainer`, `ActionsCell`, `cell`, … In pods-scrolling, 12.4 ms of the plugin's 15.4 ms self time is charged as `frameworkMs` to the "application ancestor" `(anonymous) index:456097:31`, together with 3.0 ms of React `completeWork` (row `fw=15.42`). That frame is the caller of `processRootScheduleInMicrotask`, i.e. React's `scheduleImmediateRootScheduleTask` microtask callback (`react-reconciler/src/ReactFiberRootScheduler.js:598` in React v19.1.0).
- **Reproduce**:

  ```bash
  wkperf analyze $FX/a-synthetic-react-ownership.json --format json | jq -c '.reports[0].rootCause | {owners: [.owners[] | "\(.category)=\(.selfMs)ms/\(.samples)"], nearestApp: [.appAttribution[] | "\(.app.name // "") @\(.app.url|split("/")|last):\(.app.line) total=\(.totalMs) fw=\(.frameworkMs) native=\(.nativeMs)"]}'
  wkperf analyze $FX/real-wrapped-fresh-record.json --format json | jq -c '[.reports[0].rootCause.appAttribution[:8][] | "\(.app.name)@\(.app.line) total=\(.totalMs)"]'
  ```

  Observed: `{"owners":["app=15ms/30","framework-library=9ms/6","native=49ms/30","gc=0ms/0"],"nearestApp":["getFiniteAnimations @index-TEST.js:182750 total=34 fw=0 native=34","commitDeletionEffectsOnFiber @index-TEST.js:453671 total=15 fw=0 native=15","heartbeat @index-TEST.js:304368 total=15 fw=0 native=0"," @index-TEST.js:456097 total=9 fw=9 native=0"]}`. The 9 ms charged to the anonymous React callback is the plugin component `cell` (`/_/plugins/kubernetes/assets/entry2.js:248:31307`). The real fresh-record fixture gives `["pop@446188 total=110.533208","@290602 total=18.352979","setOptions@289189 total=18.352979","@70 total=13.578792","@175238 total=9.598683","@195659 total=9.598683","FiberNode@448302 total=9.598683","@181888 total=4.799341"]`. `pop` and `FiberNode` are React internals, and `@70` is `user-script:2`. On its hot path (`.reports[0].rootCause.hotPath.frames[].category`), `performWorkOnRootViaSchedulerTask`, `updateForwardRef` (React) and Base UI `useRenderElement`, `useRenderElementProps`, `mergePropsN` and `mergeProps` are all `app`, while `bound performWorkOnRootViaSchedulerTask` is `native`.
  On the big captures (not in the repo): rapid-scroll nearest-app rows `getFiniteAnimations 229.1`, `(anonymous)@456097 18.0`, `commitDeletionEffectsOnFiber 16.5`, `setValueForStyle 10.6`, `commitHookEffectListUnmount 6.8`, `insertOrAppendPlacementNode 4.4`. My Python re-implementation of the rule reproduces these rows.
  Expected: React internals are `framework`. `getFiniteAnimations`/`tick`/`exec2` are `unknown (bundled)`, or library if a library URL pattern or source map says so. `ResourceTableContainer`/`cell` are `app`, or a first-party `plugin` category. Commit-phase stacks report "no application frame on this stack".
- **Root cause** [VERIFIED]:
  - `internal/flame/frame.go:150-164` (`reactFunction`): the list misses e.g. `commitDeletionEffectsOnFiber`, `commitHookEffectListMount/Unmount`, `commitPassiveUnmountOnFiber`, `commitReconciliationEffects`, `commitHostUpdate`, `insertOrAppendPlacementNode`, `setValueForStyle(s)`, `setValueForAttribute`, `setProp`, `setInitialProperties`, `updateProperties`, `reconcileChildFibersImpl`, `reconcileChildrenArray`, `createChild`, `updateSlot`, `bailoutOnAlreadyFinishedWork`, `propagateParentContextChanges`, `updateSyncExternalStore`, `mountWorkInProgressHook`, `updateWorkInProgressHook`, `pop`, `push*`, `FiberNode`, `createFiberFromTypeAndProps`, `flushMutationEffects`, `flushLayoutEffects`, `flushPendingEffects`, `flushSpawnedWork`, `completeRoot(WhenReady)`, `performWorkOnRootViaSchedulerTask`, `dispatchEvent*`, `batchedUpdates$1`, `jsxProd`.
  - `frame.go:135-142` treats `/plugins/` as framework; this is intentional per `flame_test.go:332`.
  - `frame.go:144-147`: every other non-empty URL is `app`, including `user-script:N`.
  - `internal/rootcause/ownership.go:33-43` takes the first `CategoryApp` frame walking up from the leaf.
  - REFERENCE.md:279 claims "Known framework internals can be recognized even in bundled code".
- **Impact**: `rootCause.owners` (app/framework/native split), `appAttribution`, the `--brief` and md "Owners and nearest application ancestors" section, and AI prompts. The words "Nearest application ancestor" assert ownership that wkperf cannot know for single-bundle builds without source maps.
- **Proposed fix**:
  1. Name table: replace `reactFunction` with a table generated from React 19 `react-dom`/`react-reconciler`/`scheduler` (exact names). Add safe prefixes: `commit*`, `recursivelyTraverse*`, `*OnFiber`, `reconcileChild*`, `bailoutOn*`, `setValueFor*`, `mount*Hook*`, `update*Hook*`, `flush*Effects`. Apply the name table only to bundled code, not to `/src/` URLs.
  2. Unknown instead of app: a frame in a bundle without a resolving source map, not in the table and not matching a user app pattern, is `unknown-bundled`. `app` requires a source map to a non-`node_modules` path, or a configured `app_url` / `library_url` glob (new flags, also in config).
  3. Plugins: drop the built-in `/plugins/` → framework rule. Classify `/_/plugins/<name>/` as `app` (first-party extension) unless configured as library. `user-script:N` becomes `injected`.
  4. Rename "nearest application ancestor" to "nearest non-framework frame". When none exists (React commit stacks consist only of React + native frames), print "no non-framework frame on this stack (framework-only work, e.g. React commit); the scheduling component is not visible in sampler stacks".
  5. Wording in md/brief/JSON labels: "ownership heuristic: name table + URL rules; bundled code without source maps may belong to a library".

  Optional: a "bundle region" inference that marks anonymous frames lying between table-matched React frames in the same file as `framework (inferred)`, for example `:456097` between `performSyncWorkOnRoot :456092` and `processRootScheduleInMicrotask :456012`. It must be labeled "inferred".

  Rejected: a growing ad-hoc list of library names such as Base UI (unbounded), and URL heuristics alone (cannot separate a single bundle).
- **Regression test**: `internal/flame`: every React name in the table → `framework`; `getFiniteAnimations` in a bundle without a map → `unknown-bundled`; `ResourceTableContainer` at `/_/plugins/kubernetes/assets/entry2.js` → `app`; `user-script:2` → `injected`. `internal/rootcause` on `a-synthetic-react-ownership.json`: no attribution row names `commitDeletionEffectsOnFiber` or `getFiniteAnimations` as application; `cell` is a non-framework frame with 9 ms; commit samples report "no non-framework frame".
- **Notes/uncertainty**: `getFiniteAnimations` exists in Base UI v1.9.0; master renamed it to `isFiniteAnimation` (`/tmp/uaf-*.ts` downloads). Which Base UI version the app bundles is inferred from these names. Whether Omniview considers plugin code first-party is a product decision. The current default hides it, and for this app it is the code under test.

## BUG-A9: `react-sync-in-trigger` is true by construction for React 19, and its render/commit split uses the leaf frame (render 3 ms vs 108 ms actual)

- **Status**: NEW
- **Severity**: medium. The finding fires on every React 19 sync-lane render with a "high" raw severity, so it carries no information. Its render/commit stats are wrong by 30×, which misleads an agent deciding whether to optimize render or commit.
- **Version**: v0.4.0 (new).
- **Summary**: React 19 flushes SyncLane work in its own microtask (`scheduleImmediateRootScheduleTask` → `scheduleMicrotask(() => processRootScheduleInMicrotask())`, which ends in `flushSyncWorkAcrossRoots_impl`; `ReactFiberRootScheduler.js:582-606, 242-313`, v19.1.0). Scheduler tasks run inside a MessageChannel `message` event. The trigger windows are any `*microtask*` or `event-dispatched` record, so they always contain React's own work. In all four captures with React samples, 100 % of sync-React samples fall inside trigger windows: fresh 30/30, pods-scrolling 120/120, pods-not-moving 24/24, rapid-scroll 90/90. The stack roots are React's anonymous microtask callback or `performWorkUntilDeadline`. `renderRootSync` is not evidence of SyncLane: React 19 uses it for every blocking lane, including DefaultLane (`ReactFiberWorkLoop.js:1031-1044`, `ReactFiberLane.js:640-648`). Render/commit is decided from the leaf frame only, so time whose leaf is a component, a native DOM call or a plugin frame goes to `other`.
- **Reproduce**:

  ```bash
  wkperf analyze $FX/a-synthetic-react-ownership.json --format json | jq -c '.reports[0].rootCause.findings[] | select(.id=="react-sync-in-trigger") | {severity, stats: [.stats[] | "\(.key)=\(.value)"]}'
  wkperf analyze $FX/real-wrapped-fresh-record.json --format json | jq -c '.reports[0].rootCause.findings[] | select(.id=="react-sync-in-trigger") | [.stats[] | "\(.key)=\(.value)"]'
  ```

  Observed: `{"severity":"high","stats":["react.sync.self_ms=24","react.render.self_ms=0","react.commit.self_ms=0","react.other.self_ms=24","react.sync.samples=16","react.sync.trigger_windows=1"]}`. The fixture's only trigger is the `microtask-dispatched` that contains React's own `processRootScheduleInMicrotask` stacks. Its phase ancestors give render 9 ms (`renderRootSync`) and commit 15 ms (`commitRoot`). Fresh-record: `render 4.799341, commit 0, other 166.288176`, with roots `performWorkUntilDeadline` (Scheduler) for 28 of 30 samples. By innermost phase ancestor, the real captures give fresh render 134.4 / commit 36.7, pods-scrolling 108.4/74.3 (wkperf 3.0/3.0), pods-not-moving 52.1/30.1 (wkperf 2.6/3.2), rapid-scroll 80.8/44.4 (wkperf 4.2/2.6).
  Expected: no "inside trigger" finding when the only enclosing trigger is React's own scheduling microtask or Scheduler message. Render/commit are split by phase ancestor.
- **Root cause** [VERIFIED]: `internal/rootcause/detectors.go:75-78` (`isInteraction` matches any microtask and every `event-dispatched`, including `message`), `:37-49` (`syncReact` accepts `renderRootSync`), `:138-144` (`commitFrame(leaf)` / `renderFrame(leaf)` on the leaf only), `:162` (`Severity: "high"`).
- **Impact**: `react-sync-in-trigger` in findings, root-cause JSON, brief and AI prompt. Its "Distinct React render self time" and "commit" stats.
- **Proposed fix**:
  1. SyncLane evidence = `performSyncWorkOnRoot` or `flushSync*` on the stack. Drop `renderRootSync`.
  2. A trigger counts only if it is not React's own scheduling entry. Exclude windows whose samples are rooted at React's microtask callback (`processRootScheduleInMicrotask` as the second frame) or `performWorkUntilDeadline`. Look for a user event, timer or rAF enclosing that microtask (`microtask-dispatched` nested in `event-dispatched click` is a real trigger). If none exists, report "React sync render; trigger not identifiable from timeline", info severity.
  3. Phase = innermost ancestor among `renderRoot*` (render) and `commitRoot`/`completeRoot*`/`flush*Effects`/`commit*` (commit); otherwise `other`.
  4. Mention `useSyncExternalStore`/`forceStoreRerender` on the stack as the likely SyncLane source when present (seen in pods-not-moving: `updateSyncExternalStore`).

  Rejected: keeping the detector and lowering its severity (still no information).
- **Regression test**: `internal/rootcause` on `a-synthetic-react-ownership.json`: no `react-sync-in-trigger` (only React's own microtask), or an info finding with render 9 / commit 15. A new case with `event-dispatched click` enclosing the microtask fires with that event as trigger.
- **Notes/uncertainty**: React behavior is verified against React v19.1.0 source; the app's exact React 19 minor is not known. The phase sums above come from my Python implementation over kept samples.

## BUG-A10: `deep-stack`, `deep-recursion` and `deep-commit` report the same outlier stacks three times; React 19's recursive commit traversal is flagged as "possible runaway recursion"

- **Status**: CONFIRMED (the previous session noted the triplication)
- **Severity**: low. Three findings for one observation fill the bounded output. The "runaway recursion" wording sends an agent looking for an infinite-recursion bug that does not exist.
- **Version**: v0.4.0 (new).
- **Summary**: All outliers are React 19 commit traversals: alternating `commit<Phase>OnFiber` / `recursivelyTraverse<Phase>Effects` pairs repeated 125× (pods-not-moving), 176× (rapid-scroll) and 49× (fresh). This mirrors the fiber-tree depth; React 19 commits recursively by design. Numbers:
  - pods-scrolling: all three 74.304967 ms / 34 stacks / 48 samples.
  - rapid-scroll: all three 44.358934 / 16 / 32.
  - pods-not-moving: commit and recursion 26.99 / 3, stack 28.12 / 4.
  - fresh: commit = stack 53.18 / 7, recursion 41.51 / 3.

  The explanation says "at least 32 distinct commit-frame appearances", but the counter counts all appearances, not distinct ones.
- **Reproduce**:

  ```bash
  wkperf analyze $FX/a-synthetic-react-ownership.json --format json | jq -c '[.reports[0].rootCause.findings[] | select(.id|startswith("deep")) | {id, stats: [.stats[] | "\(.key)=\(.value)"], stacks: (.evidence|length)}]'
  wkperf analyze $FX/real-wrapped-fresh-record.json --format json | jq -c '.reports[0].rootCause | {deep: [.findings[] | select(.id|startswith("deep")) | "\(.id): \(.stats[0].value)ms/\(.stats[1].value) stacks"], depth: (.depth | {median, outlierThreshold, maxRepeatedFrameDepth, maxCommitDepth})}'
  ```

  Observed: three entries `deep-commit|deep-recursion|deep-stack … self_ms=4.5 … stacks=1` (the fixture's 33 commit pairs); fresh `["deep-commit: 53.180065ms/7 stacks","deep-recursion: 41.505299ms/3 stacks","deep-stack: 53.180065ms/7 stacks"]`, `{"median":11,"outlierThreshold":32,"maxRepeatedFrameDepth":49,"maxCommitDepth":98}`.
  Expected: one finding, "deep React fiber tree (~N levels; commit traversal depth M)", severity info, with no recursion claim when the repeated frames are a known framework traversal pair.
- **Root cause** [VERIFIED]: `internal/rootcause/depth.go:96-124` builds three findings from the same `d.Outliers`. `depth.go:100,103` uses fixed thresholds of 32. `depth.go:20-33` counts every frame identity and `commitFrame`. `depth.go:58` sets the outlier threshold to `max(32, median + max(16, 6·1.4826·MAD))`.
- **Impact**: three findings in md/brief/JSON, wasted bounded-output space, and a false "runaway recursion" lead.
- **Proposed fix**: Emit one `deep-stack` finding with facets `maxDepth`, `repeatedFrame{name,count}` and `commitFrames`. Compute recursion after removing known framework traversal frames: the table from A8 plus the `*OnFiber`/`recursivelyTraverse*` pair rule. Claim "possible recursion" only for a non-framework function that repeats ≥ 32 times, consecutively or in a cycle of ≤ 2. Rejected: raising the thresholds, because deep trees grow with the app and the duplicate output would remain.
- **Regression test**: `internal/rootcause` on `a-synthetic-react-ownership.json`: exactly one depth finding; no "recursion" wording. A synthetic stack with 40 consecutive `walk` app frames gives the recursion facet.
- **Notes/uncertainty**: Whether a 125–176-level fiber tree deserves an info finding at all is a product choice; the self time of those stacks is leaf work (e.g. `removeChild`), not caused by depth.

## BUG-A11: `suspected-layout-read` calls `getAnimations` a layout-forcing read; the timeline shows a style flush, and the finding contradicts `forced-layout`

- **Status**: NEW
- **Severity**: medium. In both scrolling captures `getAnimations` is the largest sampled hotspot (rapid-scroll 229 ms / 64 %, pods-scrolling 69 ms / 26 % of kept samples). The mechanism wkperf names (forced layout) is wrong, so an agent looks for layout thrash instead of style invalidation from Base UI's rAF loop.
- **Version**: v0.4.0 (new).
- **Summary**: The Web Animations spec says `getAnimations()` "triggers a style change event" (style flush, not layout). In `rapid-scroll-deployment`, 133/134 `getAnimations` samples lie inside `recalculate-styles` records (87 inside one 163.0 ms recalc at t = 14.858 within rAF 493) and 0 inside `layout` records. In `pods-scrolling-to-failure`, 50/50 lie inside one 69.1 ms recalc (t = 1.554, the 73 ms rAF long task), 0 inside layout. The finding says "Suspected layout-forcing native reads", while `forced-layout` and the scorecard say 0 synchronous layouts for rapid-scroll. Nothing links the finding to the `style-recalc-heavy` finding that measures the same cost.
- **Reproduce**:

  ```bash
  wkperf analyze $FX/a-synthetic-periodic-raf.json --format json | jq -c '.reports[0] | [.findings[] | "\(.id) \(.severity) \(.impactMs)ms: \(.title)"]'
  jq -r '.recording as $r | [$r.records[] | select(.eventType=="recalculate-styles") | [.startTime,.endTime]] as $rs | [$r.samples[0].stackTraces[] | select(.stackFrames[0].name=="getAnimations") | .timestamp as $t | ($rs | map(select(.[0] <= $t and $t < .[1])) | length)] | "getAnimations samples=\(length) insideRecalc=\(map(select(.>0))|length)"' $FX/a-synthetic-periodic-raf.json
  ```

  Observed: `["raf-load low 99.99999999999432ms: requestAnimationFrame callbacks are expensive","hot-function low 30ms: Sampled function concentration: getAnimations","periodic-root low 30ms: Repeated periodic sampled background work","suspected-layout-read low 30ms: Suspected layout-forcing native reads"]` and `getAnimations samples=20 insideRecalc=20`.
  Expected: a finding named "native API forcing style recalculation (getAnimations)", with evidence "20/20 samples inside recalculate-styles records (… ms)", linked to `style-recalc-heavy` when that finding exists.
- **Root cause** [VERIFIED]: `internal/rootcause/detectors.go:20-35` puts `getAnimations` in one "layout read" list with `getBoundingClientRect`, `offset*` and `scroll*`, and does not consult timeline records. `detectors.go:150` hard-codes the title and explanation. Spec: https://www.w3.org/TR/web-animations-1/ (`getAnimations()`: "Calling this method triggers a style change event").
- **Impact**: wrong mechanism in findings, brief and AI prompt; an apparent contradiction with `forced-layout`/`forced-layout-rate`.
- **Proposed fix**: Use a per-API flush table: `getAnimations` → style; `getComputedStyle` → style (layout for geometry properties, unknown from the stack); `getBoundingClientRect`, `getClientRects`, `offset*`, `client*`, `scroll*` → layout. Rename the finding `forced-style-or-layout-read` with a per-API `flush` field. Add timeline corroboration: count samples whose timestamp lies inside `recalculate-styles` vs `layout` records and report both, plus the covering records' total ms. Rejected: dropping `getAnimations` from the list (it is the real cost here).
- **Regression test**: `internal/rootcause` on `a-synthetic-periodic-raf.json`: finding flush = `style`, evidence `20/20 inside recalculate-styles`, and the title has no "layout-forcing". A `getBoundingClientRect` sample inside a `layout` record gives flush = `layout`.
- **Notes/uncertainty**: The style-vs-layout split is from the spec and the timeline overlap; I did not read WebKit's `Element::getAnimations`.

## BUG-A12: One set of samples is reported by up to four findings; `periodic-root` labels per-frame rAF work during input as "background"

- **Status**: NEW
- **Severity**: low. Duplicates overstate the number of independent leads and use bounded output; the "background" label is wrong for interaction-time rAF callbacks.
- **Version**: v0.4.0 (new).
- **Summary**: In `rapid-scroll-deployment` the same 134 samples (229.0995 ms) appear as `hot-function getAnimations`, `suspected-layout-read` (`layout_reads.self_ms=229.0995`), `periodic-root` (`periodic.total_ms=229.0995`), the top nearest-app row `getFiniteAnimations 229.1` and the hot-path terminal (229.1). The periodic root `tick` has 4 fires (the minimum), with period 393.8 ms. The fires are `animation-frame-fired 493/494/496/497` inside active, input-overlapped frames (61, 62, 64, 66 in Appendix A), i.e. per-frame rAF work during scrolling, not background work.
- **Reproduce**:

  ```bash
  wkperf analyze $FX/a-synthetic-periodic-raf.json --format json | jq -c '.reports[0] | {periodic: [.rootCause.periodicRoots[] | {root: .root.name, fireCount, periodMs, totalMs, events: [.fires[].event]}], nearestApp: [.rootCause.appAttribution[] | "\(.app.name) \(.totalMs)"]}'
  ```

  Observed: `{"periodic":[{"root":"tick","fireCount":4,"periodMs":394,"totalMs":30,"events":["animation-frame-fired 490","animation-frame-fired 491","animation-frame-fired 492","animation-frame-fired 493"]}],"nearestApp":["getFiniteAnimations 30"]}`. Together with the A11 output, the same 30 ms appears in `hot-function`, `periodic-root`, `suspected-layout-read` and the nearest-app row; 40 `wheel` dispatches overlap the whole period.
  Expected: one lead (the style-forcing `getAnimations` in a rAF loop) with facets, and `periodic-root` not labeled "background" when its fires are rAF callbacks during input.
- **Root cause** [VERIFIED]: `internal/insights/rootcause.go:137-162` builds `periodic-root` with the fixed title "Repeated periodic sampled background work". `internal/rootcause/periodic.go:131,153` accepts ≥ 4 fires with regularity ≥ 0.9. There is no cross-finding dedupe: `internal/insights/rules.go:224` hot-function plus `rootFindings` are appended independently.
- **Impact**: finding lists and brief priorities; an agent counts four problems where there is one.
- **Proposed fix**:
  1. Add `relatedFindings` and merge findings whose sample sets overlap by Jaccard ≥ 0.8 into the strongest one, keeping the others as facets (stats remain in `rootCause`).
  2. In `periodic-root`, classify fires: if most fires are `animation-frame-fired` or lie in input-driven frames, title "Per-frame rAF work (period ≈ frame interval)" instead of "background".
  3. Require ≥ 6 fires spanning ≥ 2 s for "periodic".

  Rejected: suppressing `periodic-root` entirely (it is the right detector for idle timers and polling loops).
- **Regression test**: `internal/insights` on `a-synthetic-periodic-raf.json`: at most one finding carries the 30 ms, with `relatedFindings` listing the others, and no finding title contains "background".
- **Notes/uncertainty**: The Jaccard threshold and fire minimum are choices.

## BUG-A13: Function-level findings are built from a single sample; coverage is judged by weight only; anonymous functions give empty titles

- **Status**: NEW
- **Severity**: medium. `hot-function` names React's `pop` (110.5 ms) or an anonymous closure (127.3 ms) from one sample each. A single sample's weight equals the duration of the enclosing event, so one stack stands for a whole 110 ms event.
- **Version**: v0.4.0. v0.3.0 also reported single-sample hot functions, at high severity (`(anonymous) … 64.2%` on rapid-scrolling-deployment).
- **Summary**: Observed weight pattern: fresh `pop` is 1 sample of 110.53 ms inside a 110.8 ms `event-dispatched message`. rapid-scrolling `(anonymous):304554:36` is 1 sample of 127.27 ms inside a 134.2 ms timer. pods-not-moving `setProp` is 1 sample of 24.38 ms inside a 48.8 ms microtask, and it is also the hot-path terminal. `low-sample-coverage` does not fire for a 4-sample capture, because its ratio is computed from weights (0.897). The hot-function title for an anonymous function is `"Sampled function concentration: "` (empty).
- **Reproduce**:

  ```bash
  wkperf analyze $FX/a-real-rapid-scrolling-deployment.json --format json | jq -c '.reports[0] | {sampleCoverage: {sampleCount: .sampleCoverage.sampleCount, ratio: .sampleCoverage.ratio}, hot: [.findings[] | select(.id=="hot-function" or .id=="hot-module" or .id=="low-sample-coverage") | {id, severity, title, e0: .evidence[0]}], hotspots: [.hotspots[:3][] | {name, selfMs, selfSamples}]}'
  wkperf analyze $FX/real-wrapped-fresh-record.json --format json | jq -c '[.reports[0].hotspots[:1][] | {name, selfMs, selfSamples}]'
  ```

  Observed: `{"sampleCoverage":{"sampleCount":4,"ratio":0.8970944948924743},"hot":[{"id":"hot-module",…},{"id":"hot-function","severity":"low","title":"Sampled function concentration: ","e0":"(anonymous) (wails://localhost/assets/index-GgPenX8q.js:304554:36) is 56.9% of sampled self time (127.3ms; 0.5% of wall time)"}],"hotspots":[{"name":"","selfMs":127.26874998770654,"selfSamples":1},{"name":"fetch","selfMs":80.08950005751103,"selfSamples":2},{"name":"heartbeat","selfMs":16.409375006332994,"selfSamples":1}]}` and `[{"name":"pop","selfMs":110.53320829523727,"selfSamples":1}]`.
  Expected: no `hot-function` from fewer than N samples (proposed 5); evidence shows the sample count; `low-sample-coverage` fires below N samples whatever the weight ratio; the title shows `(anonymous) (url:line:col)`.
- **Root cause** [VERIFIED]:
  - `internal/insights/rules.go:536-554`: no sample-count gate; the title is `"…: " + h.Name`.
  - `internal/insights/evidence.go:32-48`: fewer than 100 samples only lowers severity by one step.
  - `internal/insights/rules.go:596-600`: coverage fires only on `Ratio < 0.5`.
  - REFERENCE.md:169 defines ms and share thresholds but no sample minimum.
- **Impact**: `hot-function`, `hot-module`, the hot-path terminal (A14), nearest-app rows, and AI prompts that treat these as leads.
- **Proposed fix**: `MinFunctionSamples = 5` for function/module findings. Include `samples=N` in evidence and titles. When a function's only samples fall inside one timeline record and its weight is ≥ 80 % of that record, say so ("one sample stands for the 110.8 ms message event"). `low-sample-coverage` fires when `SampleCount < 20 || Ratio < 0.5`. Titles use `functionLabel`. Rejected: keeping severity-only downgrades (the finding still appears as a lead).
- **Regression test**: `internal/insights` on `a-real-rapid-scrolling-deployment.json`: no `hot-function`; `low-sample-coverage` present; no title ending in `": "`. On the fresh-record fixture: no `hot-function pop`.
- **Notes/uncertainty**: I did not read WebKit's sample-duration algorithm; the "weight ≈ enclosing record" pattern is observed, not traced. After BUG-S1 more samples are kept, but the gate is still needed for sparse captures.

## BUG-A14: Hot path is computed correctly, but the report descends to a single-sample leaf and hides per-node weight and tie resolution

- **Status**: NEW (the claim that hot-path logic is wrong is NOT REPRODUCED: the chain, the threshold and "terminal sampled time" match an independent computation on all five profiles)
- **Severity**: low. The chain is right, but the report ends on noise (`terminal`, 1 sample, 0.6 % of sampled time) and does not show where the weight drops. That makes it look as if the terminal is the hot spot.
- **Version**: v0.4.0 (the root-cause hot path is new).
- **Summary**: `rootCause.hotPath` and every window hot path use `HotPath(0)`, which always descends to a leaf. In pods-scrolling the chain goes `(anonymous) 183.6 ms → … renderRootSync 108.4 → … renderWithHooks 50.9 → iH 11.6 → useSourceStates 3.3 → filter 1.66 → terminal 1.66 (1 sample)`. Markdown and brief print only the frame names plus "Hot path terminal sampled time: 1.65876 ms". The default `query hot-path` (min-share 0.05) stops at `renderWithHooks` (50.9 ms). Equal-weight children are resolved lexically without notice: fresh `mergePropsN` has two 4.80 ms children, pods-scrolling `filter` two 1.66 ms children. Getting a terminal from a 50/50 split by name sort is arbitrary.
- **Reproduce**:

  ```bash
  wkperf query $FX/real-wrapped-fresh-record.json hot-path --min-share 0 --format json | jq -c '[.rows[] | "\(.name)=\(.total_ms|.*100|round/100)ms/\(.samples)"]'
  wkperf query $FX/real-wrapped-fresh-record.json hot-path --format json | jq -c '[.rows[-1] | "\(.name)=\(.total_ms|.*100|round/100)ms/\(.samples)"], (.rows|length)'
  wkperf analyze $FX/real-wrapped-fresh-record.json --format json | jq -c '.reports[0].rootCause.hotPath | {terminal: .frames[-1].name, sampledMs, samples}'
  ```

  Observed: `["performWorkUntilDeadline=261.39ms/34","bound performWorkOnRootViaSchedulerTask=148.78ms/31",…,"useRenderElementProps=24.0ms/5","mergePropsN=9.6ms/2","mergeProps=4.8ms/1"]`; default `["useRenderElementProps=24.0ms/5"] 11`; report `{"terminal":"mergeProps","sampledMs":4.799341,"samples":1}`. My Python tree gives the same 13-node chain and totals, and for all five profiles the same terminal, depth and totals (fresh `mergeProps` 4.799, rapid-scrolling `heartbeat` 16.409, pods-scrolling `terminal` 1.659, pods-not-moving `setProp` 24.379, rapid-scroll `getAnimations` 229.100).
  Expected: the report shows per-node `total_ms`/`samples`/share. It stops where the heaviest child is below a documented share, or is a near-tie with its sibling, and lists the competing branches.
- **Root cause** [VERIFIED]: `internal/rootcause/build.go:260-273` (`HotPath(0)`); `internal/rootcause/windows.go:90`; `internal/flame/tree.go:203-225` (tie → `compareFrame`); `internal/insights/render.go:129+` prints names without per-node values. The stat key `hot_path.self_ms` holds `node.TotalMs`. That is correct only because the terminal of `HotPath(0)` is a leaf, where total equals self.
- **Impact**: hot path in md/brief/JSON and window hot paths; agents may open the terminal function.
- **Proposed fix**: Use the same default as `query` (`--min-share 0.05`) for the report and window hot paths. Print per node `name total_ms (share, samples)`. Stop and print "branches: A x ms, B y ms" when `second ≥ 0.8 × best`. Rename the stat label to "Hot path terminal inclusive time". Rejected: changing the greedy algorithm (it is documented and correct).
- **Regression test**: `internal/rootcause` on the fresh-record fixture: the hot path ends at `useRenderElementProps` (24.0 ms, 5 samples), and md contains per-node ms. A synthetic 50/50 tie produces a `branches` note.
- **Notes/uncertainty**: none on correctness; the thresholds are choices.

## BUG-A15: Raw root-cause severities contradict canonical finding severities in the same report; bridged severity ignores share

- **Status**: NEW (the constant `confidence: "low"` itself is BUG-O10, confirmed: `internal/insights/evidence.go:183`)
- **Severity**: low. A JSON consumer reading `rootCause.findings[]` sees `react-sync-in-trigger` as "high", while `findings[]` says "low". Agents that read both get contradictory priorities.
- **Version**: v0.4.0 (new).
- **Summary**: The pods-scrolling raw detectors are `react-sync-in-trigger` high, `deep-*` medium and `suspected-layout-read` medium. The canonical copies are low, low and low. `bridgeRootFinding` passes `share = 100` to `sampledSeverity`, so severity depends only on the wall-time share.
- **Reproduce**:

  ```bash
  wkperf analyze $FX/real-wrapped-fresh-record.json --format json | jq -c '.reports[0] | {raw: [.rootCause.findings[] | "\(.id)=\(.severity)"], canonical: [.findings[] | select(.id=="react-sync-in-trigger" or (.id|startswith("deep"))) | "\(.id)=\(.severity)"]}'
  ```

  Observed: `{"raw":["react-sync-in-trigger=high","deep-commit=medium","deep-recursion=medium","deep-stack=medium"],"canonical":["react-sync-in-trigger=low","deep-commit=low","deep-stack=low","deep-recursion=info"]}`.
  Expected: one severity per finding ID.
- **Root cause** [VERIFIED]: `internal/rootcause/detectors.go:150,162` and `internal/rootcause/depth.go:116` hard-code raw severities. `internal/insights/rootcause.go:47-51` recomputes with `sampledSeverity(c, impact, 100)`.
- **Impact**: analysis JSON consumers and the AI prompt, if it includes raw root-cause data.
- **Proposed fix**: Drop `Severity` from `rootcause.Finding`, which is raw evidence, or set it from the bridged value after bridging. Pass the real sampled share to `sampledSeverity`. Rejected: keeping two severity vocabularies.
- **Regression test**: `internal/insights`: for every bridged finding, `rootCause.findings[id].severity` is absent or equal to `findings[id].severity`.
- **Notes/uncertainty**: none.

## BUG-A16: `--jank-ms` changes the jank finding and slow frames but not `frames.jank_ratio` or the scorecard

- **Status**: NEW
- **Severity**: low. With `--jank-ms 200` the finding says 40 % jank and the scorecard says 100 % for the same frames.
- **Version**: v0.4.0 and v0.3.0 (slice: finding "3 of 6 frames over budget (50.0%)", scorecard 1).
- **Summary**: The finding and `slowFrames` use `--jank-ms`. `frames.jank_ratio` and the scorecard use a hard-coded 16.7 ms.
- **Reproduce**:

  ```bash
  wkperf analyze ~/Scratch/profiles/pods-not-moving.json --jank-ms 200 --format json | jq -r '.reports[0] | (.findings[]|select(.id=="jank")|.evidence[0:2]|join(" | ")), (.scorecard.results[]|select(.id=="jank-ratio")|"jank-ratio=\(.value) good=\(.good)"), "slowFrames=\(.slowFrames|length)"'
  ```

  Observed: `frame time p95 761.5ms vs 200.0ms budget | 2 of 5 frames over budget (40.0%)`, `jank-ratio=1 good=0.05`, `slowFrames=2`. Any fixture with 2+ active frames shows the same split.
  Expected: either the finding states that the scorecard uses a fixed 60 Hz budget, or `frames.jank_ratio` follows `--jank-ms` with its label showing the threshold.
- **Root cause** [VERIFIED]: `internal/analyze/analyze.go:397,410` hard-codes 16.7; `internal/insights/rules.go:324` uses `c.opt.JankMs`.
- **Impact**: confusing evidence when users tune `--jank-ms`.
- **Proposed fix**: Keep the scorecard at 60 Hz (documented as a standard). Add `frames.jank_ratio_at_threshold` with the `--jank-ms` value, and have the finding cite both. Rejected: making the scorecard follow `--jank-ms` (scores would stop being comparable).
- **Regression test**: `internal/insights`: with `JankMs: 200` the finding evidence includes the fixed-budget ratio, and the metric `frames.jank_ratio_at_threshold` equals the finding ratio.
- **Notes/uncertainty**: none.

## Fix plan for the coding agent

Order and dependencies (BUG-S1 from `01-sample-timestamps.md` is independent. Land it first or in parallel, and rebaseline every sampler expectation afterwards):

1. A6: rename the event types; fix testgen and tests to use real WebKit names. Trivial; it unblocks honest tests for everything below.
2. A5: add `profile.Tasks` (union of nested script records) and use it in long tasks, TBT, the long-task rate, findings and root-cause windows.
3. A3 + A7 together, both in `profile.exclusiveSpans`/`Measure`: GC becomes an overlay and the partition becomes innermost-wins. Then update the breakdown slice list and the layout-kind findings.
4. A4: three-way frame classification (idle gap / input stall / active) on GC-free work, work-based jank impact, the window ranking by `ActiveMs`, and the slice rename. Depends on 3.
5. A1 + A2 + A16: scorecard minimum active frames, `coverage`, grade `n/a`, frame details, and the jank threshold metric. Depends on 4 for the definition of an active frame.
6. A8: ownership table, `unknown-bundled`, plugin/injected categories, and "nearest non-framework frame" wording. Coordinate with BUG-O5 (ID selectors in commands, `id` on `flame.Frame`).
7. A9, A10, A11: React sync detector, single depth finding, style/layout flush table. All use the A8 framework table.
8. A12, A13: cross-finding merge and sample-count gates. Run after 7, because they merge the findings 7 produces.
9. A14, A15: hot-path presentation and the severity single source. Cosmetic; last.

After each step, re-run the fixtures listed in each bug, and the five big captures from `~/Scratch/profiles` if available. Appendix A shows which frames should change class.

## Things checked that are fine

- Frame classification arithmetic: for all five profiles my Python `Frames`/`Measure` gives the same counts, idle gaps, idle-gap ms, mean, p95, fps and jank counts as `wkperf query … metrics`:
  - fresh: 2 frames, fps 0.1444
  - rapid-scrolling: 0 active, 1 idle 26582.7 ms
  - pods-scrolling: 5, mean 809.16
  - pods-not-moving: 5, 66 idle, 30776.8 ms, mean 351.69
  - rapid-scroll: 68 active, 3 idle, 1202.5 ms, mean 222.55

  The idle-gap thresholds (50 ms, 10 %, no input) are the documented ones (REFERENCE.md:37, `profile.go:241-246`).
- The 66 idle gaps in `pods-not-moving` are correct: all have < 10 % work (max 6.1 %, 64 of 66 at ≤ 2.2 %) and no input, and CPU main-thread usage in those windows is 0.8–23 %.
- The breakdown sums to 100 % in all five profiles (maximum deviation 1e-14). There is no double counting: the sweep is an exclusive partition. The problems are attribution order (A3, A7) and slice labels (A4), not arithmetic.
- Paused frames: the fresh-record frame that overlaps the pause (42.9–236.5 s) is dropped, and the active duration is 50.73 s of 244.32 s wall, as documented.
- `IsInputDriven` parses string `details`: `message` and `animationstart` are not input; `wheel`, `scroll`, `pointerout` and `pointerover` are.
- Stack orientation: `stackFrames[0]` is the leaf (e.g. `getAnimations`), and wkperf's root-to-leaf normalization is correct.
- Hot path: greedy descent, the min-share threshold (relative to root total, `<` stops), deterministic Neumaier sums over timestamp-sorted samples, the lexical tie order, and "terminal sampled time = terminal inclusive" all match an independent computation on all five profiles (A14 is presentation only).
- Recomputed sums (kept samples; wkperf matches my values to 0.01 ms):
  - owner self totals: rapid-scroll native 277.86, app 69.76, framework 9.53; pods-scrolling 127.17/112.85/21.47
  - module totals: pods-scrolling `index` 118.9 ms, 45.5 %
  - hot-function `getAnimations` 229.10 ms, 64.1 % of 357.15
  - sample coverage 357.2/1826.7 = 0.196
- Timeline-derived finding numbers match raw-JSON recomputation:
  - `event-handler-heavy`: rapid-scroll 3903 events, 1450.2 ms; scroll 272/1366.4; wheel 2796/50.9; pods-scrolling 1042/362.4 and scroll 12/310.1
  - `raf-load`: 57/1078.8 and 2/107.9
  - `layout-after-script`: rapid-scroll 148 records, 960.9 ms
  - `forced-layout`: pods-scrolling 1 explicit, 15.2 ms, equal to the scorecard's "1 synchronous layouts"; 0 in the other captures
  - `gc-pressure` ms: 188.9 and 131.6 (wording aside, A3)
  - jank cause sums: rapid-scroll script 1624.4, layout 2025.4, paint 2139.1; `c.slow` is not truncated by `--top`
- The `periodicRoots: null` and `events: null` values are allowed by `wkperf schema` (`anyOf [array, null]`).
- Not investigated here: claims 1, 2, 3 and 5 (other areas); WebKit's own definition of rendering-frame boundaries and sample durations (I avoided depending on them).

## Appendix A: every rendering frame in the five real profiles

Columns: work and its split come from wkperf's own exclusive sweep (so `paint` is 0 because of A7). `input` = any `IsInputDriven` record overlaps the frame. `comp#` = composite records starting inside the frame. Class rule (v0.4.0, `profile.go:372`): idle gap iff duration > 50 ms, work/duration < 10 % and no input. Flags:

- `input-only`: active only because of input, work < 10 %.
- `GC-only`: active only because of GC (work without GC < 10 %).
- `slow by interval only`: > 16.7 ms long, but work without GC < 16.7 ms.
- `idle≥16.7ms work`: an idle gap that still contains at least one frame budget of work.

Summary: fresh 2 frames (2 active); rapid-scrolling 1 (idle, 795.6 ms work including a 546 ms GC and 3 long tasks); pods-scrolling 5 (5 active: 1 input-only, 1 slow by interval only); pods-not-moving 71 (5 active, 66 idle: 1 GC-only, 2 slow by interval only, 2 idle gaps with ≥ 16.7 ms work); rapid-scroll 71 (68 active, 3 idle: 8 input-only, 8 slow by interval only).

### fresh-record.json (2 frames kept by `profile.Frames`)

One more rendering-frame record (29.8 s) overlaps the capture pause and is dropped by `profile.Frames` (profile.go:350); it is not listed.

| # | start s | dur ms | work ms | script | style | layout | paint | comp | GC | work % | input | comp# | class | flags |
|---|---|---|---|---|---|---|---|---|---|---|---|---|---|---|
| 1 | 0.000 | 13841.9 | 2054.7 | 2043.0 | 5.6 | 0.6 | 0.0 | 5.5 | 0.0 | 14.8 | no | 1 | active |  |
| 2 | 13.855 | 12.6 | 0.6 | 0.0 | 0.0 | 0.0 | 0.0 | 0.6 | 0.0 | 4.8 | no | 1 | active |  |

### rapid-scrolling-deployment.json (1 frames kept by `profile.Frames`)

| # | start s | dur ms | work ms | script | style | layout | paint | comp | GC | work % | input | comp# | class | flags |
|---|---|---|---|---|---|---|---|---|---|---|---|---|---|---|
| 1 | 0.000 | 26582.7 | 795.6 | 249.4 | 0.0 | 0.0 | 0.0 | 0.0 | 546.2 | 3.0 | no | 0 | idle gap | idle≥16.7ms work |

### pods-scrolling-to-failure.json (5 frames kept by `profile.Frames`)

| # | start s | dur ms | work ms | script | style | layout | paint | comp | GC | work % | input | comp# | class | flags |
|---|---|---|---|---|---|---|---|---|---|---|---|---|---|---|
| 1 | 0.000 | 750.8 | 279.3 | 82.9 | 0.1 | 3.2 | 0.0 | 4.2 | 188.9 | 37.2 | yes | 1 | active |  |
| 2 | 0.752 | 347.6 | 278.9 | 168.8 | 31.0 | 9.6 | 0.0 | 69.5 | 0.0 | 80.2 | yes | 1 | active |  |
| 3 | 1.101 | 696.3 | 602.2 | 361.9 | 100.6 | 38.1 | 0.0 | 101.6 | 0.0 | 86.5 | yes | 1 | active |  |
| 4 | 1.799 | 62.1 | 12.1 | 0.4 | 0.0 | 0.0 | 0.0 | 11.7 | 0.0 | 19.5 | yes | 1 | active | slow by interval only |
| 5 | 1.861 | 2189.0 | 25.2 | 25.2 | 0.0 | 0.0 | 0.0 | 0.0 | 0.0 | 1.2 | yes | 0 | active | input-only |

### pods-not-moving.json (71 frames kept by `profile.Frames`)

| # | start s | dur ms | work ms | script | style | layout | paint | comp | GC | work % | input | comp# | class | flags |
|---|---|---|---|---|---|---|---|---|---|---|---|---|---|---|
| 1 | 0.000 | 879.9 | 17.4 | 14.5 | 0.0 | 2.7 | 0.0 | 0.2 | 0.0 | 2.0 | no | 1 | idle gap | idle≥16.7ms work |
| 2 | 0.881 | 126.2 | 0.2 | 0.0 | 0.0 | 0.0 | 0.0 | 0.2 | 0.0 | 0.2 | no | 1 | idle gap |  |
| 3 | 1.008 | 860.6 | 7.6 | 3.7 | 0.1 | 3.6 | 0.0 | 0.3 | 0.0 | 0.9 | no | 1 | idle gap |  |
| 4 | 1.870 | 134.6 | 0.3 | 0.0 | 0.0 | 0.0 | 0.0 | 0.3 | 0.0 | 0.2 | no | 1 | idle gap |  |
| 5 | 2.005 | 867.5 | 12.4 | 5.4 | 0.1 | 6.7 | 0.0 | 0.3 | 0.0 | 1.4 | no | 1 | idle gap |  |
| 6 | 2.873 | 129.0 | 0.3 | 0.0 | 0.0 | 0.0 | 0.0 | 0.3 | 0.0 | 0.2 | no | 1 | idle gap |  |
| 7 | 3.002 | 853.5 | 138.6 | 3.0 | 0.1 | 3.4 | 0.0 | 0.5 | 131.6 | 16.2 | no | 1 | active | GC-only; slow by interval only |
| 8 | 3.868 | 128.8 | 0.2 | 0.0 | 0.0 | 0.0 | 0.0 | 0.2 | 0.0 | 0.2 | no | 1 | idle gap |  |
| 9 | 3.997 | 874.3 | 16.0 | 12.5 | 0.1 | 3.1 | 0.0 | 0.3 | 0.0 | 1.8 | no | 1 | idle gap |  |
| 10 | 4.872 | 125.1 | 0.2 | 0.0 | 0.0 | 0.0 | 0.0 | 0.2 | 0.0 | 0.2 | no | 1 | idle gap |  |
| 11 | 4.998 | 862.2 | 10.1 | 1.8 | 0.1 | 8.1 | 0.0 | 0.2 | 0.0 | 1.2 | no | 1 | idle gap |  |
| 12 | 5.861 | 132.6 | 0.2 | 0.0 | 0.0 | 0.0 | 0.0 | 0.2 | 0.0 | 0.2 | no | 1 | idle gap |  |
| 13 | 5.994 | 875.4 | 11.8 | 3.1 | 0.1 | 8.3 | 0.0 | 0.3 | 0.0 | 1.3 | no | 1 | idle gap |  |
| 14 | 6.870 | 130.9 | 0.3 | 0.0 | 0.0 | 0.0 | 0.0 | 0.3 | 0.0 | 0.2 | no | 1 | idle gap |  |
| 15 | 7.002 | 869.5 | 9.2 | 5.2 | 0.1 | 3.6 | 0.0 | 0.4 | 0.0 | 1.1 | no | 1 | idle gap |  |
| 16 | 7.872 | 131.6 | 0.2 | 0.0 | 0.0 | 0.0 | 0.0 | 0.2 | 0.0 | 0.2 | no | 1 | idle gap |  |
| 17 | 8.004 | 867.6 | 7.2 | 3.0 | 0.1 | 3.8 | 0.0 | 0.3 | 0.0 | 0.8 | no | 1 | idle gap |  |
| 18 | 8.873 | 132.1 | 0.2 | 0.0 | 0.0 | 0.0 | 0.0 | 0.2 | 0.0 | 0.2 | no | 1 | idle gap |  |
| 19 | 9.005 | 855.7 | 10.2 | 5.6 | 0.1 | 4.3 | 0.0 | 0.3 | 0.0 | 1.2 | no | 1 | idle gap |  |
| 20 | 9.862 | 123.0 | 0.3 | 0.0 | 0.0 | 0.0 | 0.0 | 0.3 | 0.0 | 0.2 | no | 1 | idle gap |  |
| 21 | 9.985 | 393.7 | 123.2 | 57.4 | 0.7 | 13.8 | 0.0 | 51.3 | 0.0 | 31.3 | no | 1 | active |  |
| 22 | 10.380 | 137.5 | 8.3 | 0.0 | 0.0 | 0.0 | 0.0 | 8.3 | 0.0 | 6.1 | no | 1 | idle gap |  |
| 23 | 10.518 | 335.7 | 4.0 | 1.0 | 0.0 | 2.7 | 0.0 | 0.2 | 0.0 | 1.2 | no | 1 | idle gap |  |
| 24 | 10.854 | 132.3 | 0.2 | 0.0 | 0.0 | 0.0 | 0.0 | 0.2 | 0.0 | 0.2 | no | 1 | idle gap |  |
| 25 | 10.987 | 868.2 | 7.0 | 1.3 | 0.1 | 5.4 | 0.0 | 0.3 | 0.0 | 0.8 | no | 1 | idle gap |  |
| 26 | 11.856 | 121.0 | 0.2 | 0.0 | 0.0 | 0.0 | 0.0 | 0.2 | 0.0 | 0.2 | no | 1 | idle gap |  |
| 27 | 11.978 | 880.7 | 7.8 | 1.8 | 0.1 | 5.7 | 0.0 | 0.3 | 0.0 | 0.9 | no | 1 | idle gap |  |
| 28 | 12.860 | 124.6 | 0.3 | 0.0 | 0.0 | 0.0 | 0.0 | 0.3 | 0.0 | 0.2 | no | 1 | idle gap |  |
| 29 | 12.985 | 885.3 | 8.2 | 2.1 | 0.1 | 5.7 | 0.0 | 0.3 | 0.0 | 0.9 | no | 1 | idle gap |  |
| 30 | 13.871 | 122.3 | 0.2 | 0.0 | 0.0 | 0.0 | 0.0 | 0.2 | 0.0 | 0.2 | no | 1 | idle gap |  |
| 31 | 13.993 | 869.9 | 9.6 | 6.2 | 0.1 | 3.1 | 0.0 | 0.2 | 0.0 | 1.1 | no | 1 | idle gap |  |
| 32 | 14.864 | 133.0 | 0.2 | 0.0 | 0.0 | 0.0 | 0.0 | 0.2 | 0.0 | 0.2 | no | 1 | idle gap |  |
| 33 | 14.997 | 875.4 | 4.5 | 1.0 | 0.0 | 3.0 | 0.0 | 0.4 | 0.0 | 0.5 | no | 1 | idle gap |  |
| 34 | 15.874 | 130.2 | 0.2 | 0.0 | 0.0 | 0.0 | 0.0 | 0.2 | 0.0 | 0.2 | no | 1 | idle gap |  |
| 35 | 16.004 | 850.3 | 7.0 | 2.1 | 0.1 | 4.6 | 0.0 | 0.3 | 0.0 | 0.8 | no | 1 | idle gap |  |
| 36 | 16.856 | 185.4 | 56.4 | 5.3 | 0.2 | 1.8 | 0.0 | 49.1 | 0.0 | 30.4 | no | 1 | active |  |
| 37 | 17.042 | 156.1 | 16.5 | 2.5 | 0.0 | 0.0 | 0.0 | 14.0 | 0.0 | 10.6 | no | 1 | active | slow by interval only |
| 38 | 17.198 | 113.6 | 0.3 | 0.0 | 0.0 | 0.0 | 0.0 | 0.3 | 0.0 | 0.3 | no | 1 | idle gap |  |
| 39 | 17.312 | 560.6 | 12.5 | 4.9 | 0.2 | 7.1 | 0.0 | 0.2 | 0.0 | 2.2 | no | 1 | idle gap |  |
| 40 | 17.873 | 113.2 | 0.4 | 0.0 | 0.0 | 0.0 | 0.0 | 0.4 | 0.0 | 0.4 | no | 1 | idle gap |  |
| 41 | 17.987 | 872.8 | 7.4 | 2.5 | 0.1 | 4.5 | 0.0 | 0.2 | 0.0 | 0.8 | no | 1 | idle gap |  |
| 42 | 18.861 | 127.9 | 0.3 | 0.0 | 0.0 | 0.0 | 0.0 | 0.3 | 0.0 | 0.2 | no | 1 | idle gap |  |
| 43 | 18.989 | 870.0 | 14.1 | 10.2 | 0.1 | 3.4 | 0.0 | 0.5 | 0.0 | 1.6 | no | 1 | idle gap |  |
| 44 | 19.860 | 137.8 | 0.2 | 0.0 | 0.0 | 0.0 | 0.0 | 0.2 | 0.0 | 0.2 | no | 1 | idle gap |  |
| 45 | 19.998 | 874.6 | 8.7 | 2.1 | 0.1 | 6.1 | 0.0 | 0.4 | 0.0 | 1.0 | no | 1 | idle gap |  |
| 46 | 20.874 | 116.3 | 0.2 | 0.0 | 0.0 | 0.0 | 0.0 | 0.2 | 0.0 | 0.2 | no | 1 | idle gap |  |
| 47 | 20.991 | 866.6 | 9.4 | 3.7 | 0.0 | 5.4 | 0.0 | 0.2 | 0.0 | 1.1 | no | 1 | idle gap |  |
| 48 | 21.858 | 133.7 | 0.3 | 0.0 | 0.0 | 0.0 | 0.0 | 0.3 | 0.0 | 0.2 | no | 1 | idle gap |  |
| 49 | 21.993 | 864.7 | 11.5 | 3.3 | 0.1 | 7.9 | 0.0 | 0.3 | 0.0 | 1.3 | no | 1 | idle gap |  |
| 50 | 22.858 | 128.7 | 0.3 | 0.0 | 0.0 | 0.0 | 0.0 | 0.3 | 0.0 | 0.2 | no | 1 | idle gap |  |
| 51 | 22.987 | 169.8 | 26.4 | 17.9 | 0.6 | 0.7 | 0.0 | 7.3 | 0.0 | 15.5 | no | 1 | active |  |
| 52 | 23.157 | 116.4 | 0.2 | 0.0 | 0.0 | 0.0 | 0.0 | 0.2 | 0.0 | 0.2 | no | 1 | idle gap |  |
| 53 | 23.274 | 585.5 | 6.6 | 1.6 | 0.1 | 4.7 | 0.0 | 0.2 | 0.0 | 1.1 | no | 1 | idle gap |  |
| 54 | 23.860 | 126.4 | 0.2 | 0.0 | 0.0 | 0.0 | 0.0 | 0.2 | 0.0 | 0.2 | no | 1 | idle gap |  |
| 55 | 23.987 | 880.4 | 9.4 | 4.7 | 0.1 | 4.4 | 0.0 | 0.3 | 0.0 | 1.1 | no | 1 | idle gap |  |
| 56 | 24.869 | 126.8 | 0.2 | 0.0 | 0.0 | 0.0 | 0.0 | 0.2 | 0.0 | 0.2 | no | 1 | idle gap |  |
| 57 | 24.996 | 873.6 | 10.6 | 4.5 | 0.1 | 5.7 | 0.0 | 0.3 | 0.0 | 1.2 | no | 1 | idle gap |  |
| 58 | 25.871 | 121.1 | 0.5 | 0.0 | 0.0 | 0.0 | 0.0 | 0.5 | 0.0 | 0.4 | no | 1 | idle gap |  |
| 59 | 25.992 | 880.2 | 6.1 | 1.6 | 0.1 | 4.2 | 0.0 | 0.3 | 0.0 | 0.7 | no | 1 | idle gap |  |
| 60 | 26.873 | 118.6 | 0.2 | 0.0 | 0.0 | 0.0 | 0.0 | 0.2 | 0.0 | 0.2 | no | 1 | idle gap |  |
| 61 | 26.992 | 869.1 | 7.2 | 2.7 | 0.1 | 4.2 | 0.0 | 0.3 | 0.0 | 0.8 | no | 1 | idle gap |  |
| 62 | 27.863 | 129.2 | 0.2 | 0.0 | 0.0 | 0.0 | 0.0 | 0.2 | 0.0 | 0.2 | no | 1 | idle gap |  |
| 63 | 27.992 | 859.9 | 6.5 | 2.2 | 0.1 | 3.9 | 0.0 | 0.2 | 0.0 | 0.8 | no | 1 | idle gap |  |
| 64 | 28.853 | 125.5 | 0.2 | 0.0 | 0.0 | 0.0 | 0.0 | 0.2 | 0.0 | 0.2 | no | 1 | idle gap |  |
| 65 | 28.980 | 881.3 | 13.9 | 6.1 | 0.2 | 7.4 | 0.0 | 0.3 | 0.0 | 1.6 | no | 1 | idle gap |  |
| 66 | 29.863 | 130.1 | 0.2 | 0.0 | 0.0 | 0.0 | 0.0 | 0.2 | 0.0 | 0.2 | no | 1 | idle gap |  |
| 67 | 29.993 | 879.0 | 8.4 | 3.0 | 0.4 | 4.6 | 0.0 | 0.4 | 0.0 | 1.0 | no | 1 | idle gap |  |
| 68 | 30.873 | 126.4 | 0.2 | 0.0 | 0.0 | 0.0 | 0.0 | 0.2 | 0.0 | 0.2 | no | 1 | idle gap |  |
| 69 | 31.000 | 884.0 | 18.7 | 2.0 | 0.1 | 7.3 | 0.0 | 9.2 | 0.0 | 2.1 | no | 1 | idle gap | idle≥16.7ms work |
| 70 | 31.884 | 127.9 | 0.2 | 0.0 | 0.0 | 0.0 | 0.0 | 0.2 | 0.0 | 0.2 | no | 1 | idle gap |  |
| 71 | 32.013 | 582.1 | 0.9 | 0.9 | 0.0 | 0.0 | 0.0 | 0.0 | 0.0 | 0.2 | no | 0 | idle gap |  |

### rapid-scroll-deployment.json (71 frames kept by `profile.Frames`)

| # | start s | dur ms | work ms | script | style | layout | paint | comp | GC | work % | input | comp# | class | flags |
|---|---|---|---|---|---|---|---|---|---|---|---|---|---|---|
| 1 | 0.000 | 1655.6 | 39.8 | 36.1 | 0.0 | 0.0 | 0.0 | 3.6 | 0.0 | 2.4 | yes | 1 | active | input-only |
| 2 | 1.656 | 115.5 | 65.8 | 47.8 | 3.3 | 4.1 | 0.0 | 10.5 | 0.0 | 57.0 | yes | 1 | active |  |
| 3 | 1.772 | 368.5 | 252.4 | 142.2 | 39.1 | 7.8 | 0.0 | 63.3 | 0.0 | 68.5 | yes | 1 | active |  |
| 4 | 2.141 | 282.1 | 157.3 | 70.7 | 34.0 | 6.0 | 0.0 | 46.5 | 0.0 | 55.8 | yes | 1 | active |  |
| 5 | 2.424 | 281.0 | 138.2 | 0.9 | 0.0 | 0.0 | 0.0 | 19.2 | 118.0 | 49.2 | yes | 1 | active |  |
| 6 | 2.709 | 353.3 | 207.1 | 81.4 | 35.1 | 6.8 | 0.0 | 83.7 | 0.0 | 58.6 | yes | 1 | active |  |
| 7 | 3.063 | 245.4 | 128.9 | 37.8 | 34.9 | 7.9 | 0.0 | 48.3 | 0.0 | 52.5 | yes | 1 | active |  |
| 8 | 3.363 | 211.8 | 99.6 | 11.8 | 46.0 | 9.3 | 0.0 | 32.6 | 0.0 | 47.0 | yes | 1 | active |  |
| 9 | 3.609 | 171.0 | 70.2 | 5.1 | 19.1 | 7.3 | 0.0 | 38.8 | 0.0 | 41.1 | yes | 1 | active |  |
| 10 | 3.781 | 315.8 | 180.3 | 45.2 | 57.0 | 6.5 | 0.0 | 71.6 | 0.0 | 57.1 | yes | 1 | active |  |
| 11 | 4.177 | 16.5 | 16.5 | 0.0 | 0.0 | 0.0 | 0.0 | 16.5 | 0.0 | 100.0 | yes | 0 | active |  |
| 12 | 4.329 | 216.8 | 73.2 | 12.7 | 44.3 | 7.6 | 0.0 | 8.6 | 0.0 | 33.8 | yes | 1 | active |  |
| 13 | 4.546 | 268.2 | 121.6 | 38.6 | 46.7 | 8.4 | 0.0 | 28.0 | 0.0 | 45.3 | yes | 1 | active |  |
| 14 | 4.815 | 177.9 | 45.8 | 6.5 | 19.6 | 3.6 | 0.0 | 16.1 | 0.0 | 25.7 | yes | 1 | active |  |
| 15 | 4.994 | 177.5 | 47.1 | 3.6 | 23.3 | 3.3 | 0.0 | 16.9 | 0.0 | 26.5 | yes | 1 | active |  |
| 16 | 5.171 | 146.5 | 50.9 | 9.1 | 22.9 | 4.1 | 0.0 | 14.9 | 0.0 | 34.8 | yes | 1 | active |  |
| 17 | 5.318 | 178.7 | 89.3 | 19.8 | 17.7 | 3.5 | 0.0 | 48.3 | 0.0 | 50.0 | yes | 1 | active |  |
| 18 | 5.498 | 325.0 | 201.4 | 49.9 | 99.0 | 8.7 | 0.0 | 43.8 | 0.0 | 62.0 | yes | 1 | active |  |
| 19 | 5.823 | 227.1 | 139.5 | 38.6 | 40.3 | 10.8 | 0.0 | 49.8 | 0.0 | 61.4 | yes | 1 | active |  |
| 20 | 6.051 | 234.4 | 100.8 | 37.1 | 18.7 | 7.3 | 0.0 | 37.8 | 0.0 | 43.0 | yes | 1 | active |  |
| 21 | 6.286 | 134.4 | 37.1 | 11.3 | 5.4 | 4.1 | 0.0 | 16.3 | 0.0 | 27.6 | yes | 1 | active |  |
| 22 | 6.421 | 239.4 | 111.9 | 34.6 | 27.1 | 5.2 | 0.0 | 45.0 | 0.0 | 46.7 | yes | 1 | active |  |
| 23 | 6.661 | 247.6 | 112.0 | 35.3 | 22.6 | 7.4 | 0.0 | 46.7 | 0.0 | 45.2 | yes | 1 | active |  |
| 24 | 6.909 | 290.6 | 152.8 | 49.4 | 49.0 | 10.5 | 0.0 | 44.0 | 0.0 | 52.6 | yes | 1 | active |  |
| 25 | 7.200 | 125.5 | 32.5 | 4.2 | 18.3 | 3.3 | 0.0 | 6.7 | 0.0 | 25.9 | yes | 1 | active |  |
| 26 | 7.326 | 211.2 | 103.3 | 30.9 | 23.2 | 5.2 | 0.0 | 44.0 | 0.0 | 48.9 | yes | 1 | active |  |
| 27 | 7.538 | 241.8 | 139.8 | 39.0 | 47.3 | 9.9 | 0.0 | 43.5 | 0.0 | 57.8 | yes | 1 | active |  |
| 28 | 7.780 | 259.9 | 125.2 | 47.7 | 29.0 | 8.2 | 0.0 | 40.3 | 0.0 | 48.2 | yes | 1 | active |  |
| 29 | 8.041 | 166.3 | 29.4 | 8.7 | 4.3 | 3.3 | 0.0 | 13.1 | 0.0 | 17.7 | yes | 1 | active |  |
| 30 | 8.207 | 143.9 | 13.9 | 1.7 | 0.0 | 2.3 | 0.0 | 9.9 | 0.0 | 9.7 | yes | 1 | active | input-only; slow by interval only |
| 31 | 8.945 | 278.7 | 146.1 | 7.4 | 55.8 | 16.0 | 0.0 | 66.8 | 0.0 | 52.4 | yes | 1 | active |  |
| 32 | 9.224 | 170.6 | 42.8 | 21.6 | 9.2 | 6.1 | 0.0 | 5.9 | 0.0 | 25.1 | yes | 1 | active |  |
| 33 | 9.395 | 117.6 | 8.7 | 1.4 | 0.0 | 2.3 | 0.0 | 5.0 | 0.0 | 7.4 | yes | 1 | active | input-only; slow by interval only |
| 34 | 9.513 | 138.0 | 12.2 | 1.9 | 1.9 | 2.0 | 0.0 | 6.5 | 0.0 | 8.9 | yes | 1 | active | input-only; slow by interval only |
| 35 | 9.664 | 5.3 | 5.3 | 0.0 | 0.0 | 0.0 | 0.0 | 5.3 | 0.0 | 100.0 | yes | 0 | active |  |
| 36 | 9.793 | 121.3 | 45.4 | 11.4 | 9.9 | 4.4 | 0.0 | 19.8 | 0.0 | 37.5 | yes | 1 | active |  |
| 37 | 9.914 | 221.3 | 101.6 | 29.2 | 19.0 | 5.9 | 0.0 | 47.4 | 0.0 | 45.9 | yes | 1 | active |  |
| 38 | 10.136 | 120.1 | 3.3 | 0.8 | 0.0 | 0.0 | 0.0 | 2.5 | 0.0 | 2.8 | yes | 1 | active | input-only; slow by interval only |
| 39 | 10.257 | 150.9 | 26.9 | 3.0 | 17.3 | 2.2 | 0.0 | 4.5 | 0.0 | 17.8 | yes | 1 | active |  |
| 40 | 10.458 | 17.2 | 17.2 | 0.0 | 0.0 | 0.0 | 0.0 | 17.2 | 0.0 | 100.0 | yes | 0 | active |  |
| 41 | 10.533 | 227.7 | 116.6 | 39.4 | 24.9 | 5.3 | 0.0 | 47.1 | 0.0 | 51.2 | yes | 1 | active |  |
| 42 | 10.761 | 215.7 | 137.1 | 35.5 | 45.4 | 8.2 | 0.0 | 48.1 | 0.0 | 63.6 | yes | 1 | active |  |
| 43 | 10.978 | 187.7 | 106.4 | 31.2 | 20.4 | 4.9 | 0.0 | 49.9 | 0.0 | 56.7 | yes | 1 | active |  |
| 44 | 11.166 | 239.2 | 116.0 | 33.1 | 35.2 | 7.7 | 0.0 | 40.0 | 0.0 | 48.5 | yes | 1 | active |  |
| 45 | 11.406 | 155.2 | 61.6 | 14.9 | 19.3 | 4.1 | 0.0 | 23.3 | 0.0 | 39.7 | yes | 1 | active |  |
| 46 | 11.561 | 203.3 | 104.0 | 18.5 | 23.1 | 4.7 | 0.0 | 57.8 | 0.0 | 51.1 | yes | 1 | active |  |
| 47 | 11.765 | 214.3 | 138.4 | 36.8 | 37.4 | 6.7 | 0.0 | 57.5 | 0.0 | 64.6 | yes | 1 | active |  |
| 48 | 11.980 | 260.8 | 127.8 | 48.7 | 25.8 | 7.1 | 0.0 | 46.2 | 0.0 | 49.0 | yes | 1 | active |  |
| 49 | 12.241 | 147.5 | 67.0 | 13.5 | 19.3 | 4.1 | 0.0 | 30.0 | 0.0 | 45.4 | yes | 1 | active |  |
| 50 | 12.389 | 180.3 | 94.2 | 23.4 | 20.2 | 3.9 | 0.0 | 46.7 | 0.0 | 52.2 | yes | 1 | active |  |
| 51 | 12.570 | 203.6 | 102.6 | 35.6 | 21.6 | 7.5 | 0.0 | 38.0 | 0.0 | 50.4 | yes | 1 | active |  |
| 52 | 12.774 | 231.7 | 103.5 | 29.6 | 26.4 | 6.1 | 0.0 | 41.3 | 0.0 | 44.7 | yes | 1 | active |  |
| 53 | 13.011 | 205.2 | 98.9 | 16.9 | 42.3 | 7.5 | 0.0 | 32.2 | 0.0 | 48.2 | yes | 1 | active |  |
| 54 | 13.216 | 174.5 | 58.0 | 9.6 | 13.7 | 2.7 | 0.0 | 32.0 | 0.0 | 33.3 | yes | 1 | active |  |
| 55 | 13.392 | 130.2 | 10.5 | 3.2 | 1.1 | 2.2 | 0.0 | 4.1 | 0.0 | 8.1 | yes | 1 | active | input-only; slow by interval only |
| 56 | 13.523 | 413.3 | 295.0 | 24.8 | 3.3 | 3.3 | 0.0 | 7.0 | 256.7 | 71.4 | yes | 1 | active |  |
| 57 | 13.994 | 265.7 | 133.1 | 29.3 | 39.0 | 9.2 | 0.0 | 55.7 | 0.0 | 50.1 | yes | 1 | active |  |
| 58 | 14.260 | 140.8 | 16.2 | 0.7 | 1.7 | 0.0 | 0.0 | 13.8 | 0.0 | 11.5 | yes | 1 | active | slow by interval only |
| 59 | 14.403 | 124.6 | 38.3 | 5.5 | 14.9 | 2.4 | 0.0 | 15.5 | 0.0 | 30.7 | yes | 1 | active |  |
| 60 | 14.530 | 238.8 | 126.9 | 43.4 | 29.2 | 5.8 | 0.0 | 48.5 | 0.0 | 53.2 | yes | 1 | active |  |
| 61 | 14.817 | 439.5 | 293.1 | 7.8 | 183.2 | 8.2 | 0.0 | 93.8 | 0.0 | 66.7 | yes | 1 | active |  |
| 62 | 15.258 | 187.2 | 83.4 | 19.9 | 24.8 | 4.6 | 0.0 | 34.1 | 0.0 | 44.6 | yes | 1 | active |  |
| 63 | 15.445 | 152.7 | 81.6 | 21.6 | 15.2 | 5.4 | 0.0 | 39.3 | 0.0 | 53.4 | yes | 1 | active |  |
| 64 | 15.599 | 258.0 | 125.3 | 40.0 | 25.1 | 5.3 | 0.0 | 54.9 | 0.0 | 48.6 | yes | 1 | active |  |
| 65 | 15.857 | 170.0 | 29.7 | 3.3 | 13.6 | 2.1 | 0.0 | 10.6 | 0.0 | 17.5 | yes | 1 | active |  |
| 66 | 16.028 | 148.9 | 23.8 | 3.5 | 15.5 | 2.3 | 0.0 | 2.5 | 0.0 | 16.0 | yes | 1 | active |  |
| 67 | 16.178 | 127.0 | 2.8 | 0.2 | 1.1 | 0.0 | 0.0 | 1.4 | 0.0 | 2.2 | yes | 1 | active | input-only; slow by interval only |
| 68 | 16.306 | 119.6 | 0.4 | 0.2 | 0.0 | 0.0 | 0.0 | 0.2 | 0.0 | 0.4 | yes | 1 | active | input-only; slow by interval only |
| 69 | 16.426 | 111.9 | 0.8 | 0.0 | 0.0 | 0.0 | 0.0 | 0.8 | 0.0 | 0.7 | no | 1 | idle gap |  |
| 70 | 16.538 | 117.5 | 0.5 | 0.3 | 0.0 | 0.0 | 0.0 | 0.3 | 0.0 | 0.5 | no | 1 | idle gap |  |
| 71 | 16.656 | 973.1 | 0.5 | 0.5 | 0.0 | 0.0 | 0.0 | 0.0 | 0.0 | 0.1 | no | 0 | idle gap |  |

## Appendix B: independent recomputations (≥ 10 numbers)

| # | Quantity | wkperf v0.4.0 | Independent (raw JSON) | Verdict |
| --- | --- | --- | --- | --- |
| 1 | pods-not-moving active frames / idle / idle ms / fps | 5 / 66 / 30776.8 / 2.8434 | 5 / 66 / 30776.8 / 2.843 | match |
| 2 | rapid-scroll active frames / mean ms / jank16 | 68 / 222.55 / 66 | 68 / 222.55 / 66 | match |
| 3 | pods-scrolling long tasks / TBT per 10 s | 8 / 1937.3 | 8 records, but 5 top-level tasks / 1035.3 | discrepancy (A5) |
| 4 | fresh-record long tasks / TBT | 10 / 624.9 | 6 top-level / 364.3 | discrepancy (A5) |
| 5 | rapid-scroll long-task total | 1344.7 ms (7.6 %) | union 792.7 ms (4.5 %) | discrepancy (A5) |
| 6 | rapid-scroll Paint / Style / Composite (breakdown) | 0 / 1735.4 / 2479.3 | innermost: 143.8 / 1882.6 / 2025.6 | discrepancy (A7) |
| 7 | rapid-scroll `style-recalc-heavy` ms vs breakdown Style | 1882.6 vs 1735.4 | raw sum 1882.6 = union 1882.6 | internal contradiction (A7) |
| 8 | rapid-scroll microtask time / finding | 2042.5 ms (11.6 %) / none | 1650 records, 2042.5 ms | discrepancy (A6) |
| 9 | pods-scrolling React render / commit self | 3.03 / 3.03 | 108.4 / 74.3 by phase ancestor | discrepancy (A9) |
| 10 | pods-not-moving jank dominant cause | paint/composite 122.3 (GC ignored) | script 86.1, layout 21.2, paint 122.3, GC 131.6 | discrepancy (A3) |
| 11 | pods-scrolling "Frame time not attributed to work" | 2848.1 ms | main-thread CPU 1028 ms ≈ instrumented non-GC work 1009 ms | mislabel (A4) |
| 12 | rapid-scroll hot-function `getAnimations` | 229.10 ms, 64.1 % | 229.10 / 357.15 = 64.15 % | match |
| 13 | owners (all five) | e.g. rapid-scroll native 277.86 / app 69.76 / fw 9.53 | same | match (classification wrong, A8) |
| 14 | hot path terminal / ms (all five) | see A14 | same chain and totals | match |
| 15 | rapid-scroll events / scroll / wheel | 3903, 1450.2 / 272, 1366.4 / 2796, 50.9 | same | match |
| 16 | rapid-scroll layout-after-script | 148, 960.9 ms | 148, 960.9 ms | match |

Method for row 11: CPU busy = Σ over consecutive CPU records of main-thread `usage`/100 × Δt (Δt ≤ 1 s). Instrumented work = the exclusive sweep over the same intervals, excluding GC.

