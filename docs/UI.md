# Local recording investigator

Run `wkperf ui --open`. If the browser does not open, paste the printed URL into your browser. Keep the terminal running while you use the UI; press Ctrl-C to stop it.

For a reproducible investigation with real sampled stacks and browser screenshots, follow the [flame workbench tour](FLAME-TOUR.md).

1. In Web Inspector's [Timelines tab](https://webkit.org/web-inspector/timelines-tab/#sharing), export a recording as JSON. Include the instruments you want to investigate, such as **JavaScript & Events** for call stacks.
2. Choose that file under **Baseline recording**. To compare two runs, choose the second file under **Candidate recording**. Use recordings of the same action and page when possible.
3. Select **Analyze recordings**. Open a recording's **Flame graph** for calling contexts, **Attribution** for sampled self weight by category, and **Hot functions** for a function ranking. **Metrics** and **Findings** show other evidence independently of the sampled stacks.
4. With two files, check the warnings and **Metric changes** before treating a difference as a regression. The UI uses the same analysis and comparison engine as the CLI. It can normalize duration-dependent values when the runs differ in length. A single recording per side does not establish run-to-run variability; use `wkperf compare` with multiple runs for that.

## Reading the flame graph

- Width shows inclusive sample weight for one calling context, not elapsed time or call count. Callers are above callees. Siblings are ordered by weight. Space under a caller can be its own self weight or omitted branches.
- Hover or focus a frame to preview its category, self and inclusive weight, count, source locations, and call path. Click or press Enter or Space to pin the frame. **Zoom to selected** rescales its displayed subtree; **Reset zoom** returns to the recording. Detail percentages always use the full recording's sample weight.
- Tab enters or leaves the graph. Use Up and Down for caller and heaviest visible callee, Left and Right for adjacent frames on the same level, and Home and End for the edges of that level. Escape returns the readout to the pinned frame. Open **Calling-context table** for text equivalents and larger **Inspect** controls. With JavaScript off, the plot and tables still render; interactive previews and zoom are unavailable.
- The UI displays up to 320 contexts and 24 levels. It normally omits branches below 0.1% of total weight and retains the largest root if all roots fall below that cutoff. It preserves the remaining widths and does not change totals. Use `wkperf flame` to inspect omitted contexts.
- Each recording uses its own denominator. Compare absolute self and inclusive weights as well as shares. An untimed sample still contributes to whole-recording aggregates but cannot be placed on a timeline.

The category describes the executing frame, not the owner of all its descendants. **Sample attribution** counts each sample once by its executing category. Its caller-context view associates framework or native work with eligible captured callers; it overlaps the category totals and does not prove ownership or causation. An unresolved bundle can mix application and library code. The UI displays original locations already present in a recording, but does not fetch or resolve source maps.

### Profiler design references

| Profiler | Pattern used here | Difference |
| --- | --- | --- |
| [speedscope](https://github.com/jlfwong/speedscope#views) | Left Heavy groups identical caller paths and puts heavier siblings first. Sandwich separates self and inclusive weight. | wkperf pairs the aggregate graph with context and function tables, not a chronological view. |
| [Firefox Profiler](https://github.com/firefox-devtools/profiler/blob/main/docs-user/bunny.md#problems-in-the-content-process-main-thread) | Flame width is share of sampled work, and subtree focus helps isolate a hot path. | Zoom changes graph scale, not the full-recording share shown in the details. |
| [Chrome DevTools](https://developer.chrome.com/docs/devtools/performance/reference#read-the-flame-chart) | Caller-above-callee layout, selection details, and separate [table views](https://developer.chrome.com/docs/devtools/performance/reference#view-activities-in-a-table). | Chrome's flame chart has an elapsed-time axis. wkperf never presents sample weight as elapsed time. |
| [Safari Web Inspector](https://webkit.org/web-inspector/timelines-tab/#events-view) | Its JavaScript & Events instrument separates the combined call tree from chronological script entries. | wkperf reads its exports and keeps measured timeline metrics separate from sampled estimates. |

## Visual verification

From the repository root, run `go run . ui --open`. Upload [`flame-baseline.json`](../internal/webui/testdata/flame-baseline.json) and optionally [`flame-candidate.json`](../internal/webui/testdata/flame-candidate.json). These synthetic fixtures contain no private content.

1. Verify baseline shows **100 ms** and **8 samples**. `onResize` spans **70%**; `measureRows` and `flushSync` cover **50%** and **10%** below it. The gap below `onResize` represents its **10 ms** of self weight.
2. Hover `onResize`. Verify its generated `m` location (`dashboard.js:1:240`), original `src/dashboard.tsx:21:5`, and **70 ms** inclusive / **10 ms** self. Select `measureRows`, hover another frame, and move outside the graph and readout. The readout returns to `measureRows`. Zoom to it, then reset. Only the displayed subtree rescales; the selected frame's share stays **50%**.
3. Check **Sample attribution**: baseline native self weight is **40 ms**. The caller-context table shows **35 ms** of that native weight under `measureRows`, and **5 ms** with no eligible caller. `decodePayload` is an unresolved bundle. Its one untimed sample contributes **15 ms**.
4. Verify candidate shows **80 ms** and **8 samples**, with `onResize` at **62.5%**. The baseline graph keeps its own selection and scale. Expand **Metric changes** and check it reports the shorter candidate script record.
5. Repeat with a keyboard, touch or narrow viewport, and light and dark browser themes. Check focus remains visible; open the context table and inspect a frame there. At a narrow width, only individual tables should scroll sideways.
6. Disable JavaScript, re-submit the fixtures, and verify the static graph and tables still work. Upload [`metrics-only.json`](../internal/webui/testdata/metrics-only.json) as the candidate and verify it shows no weighted call stacks or function comparison, while its recording metrics remain available.

Run `go test ./internal/webui ./cmd` for server, rendering, and CLI integration checks.

## Listener and file limits

```sh
wkperf ui                           # Print an available IPv4 loopback URL
wkperf ui --listen 127.0.0.1:8123   # Use a fixed loopback port
wkperf ui --listen '[::1]:0' --open # Use IPv6 loopback and open the browser
wkperf ui --max-upload-mib 96      # Allow up to 96 MiB per recording
```

`--listen` accepts numeric loopback addresses or `localhost`, which uses `127.0.0.1`. Other interfaces, wildcard addresses, and DNS hostnames are rejected. Always use the exact printed URL. Do not expose the listener through a reverse proxy or tunnel.

The default limit is 32 MiB per file. `--max-upload-mib` sets a limit between 1 and 256 MiB per file. The full multipart request is also bounded, and the server analyzes one upload at a time. Each file may have up to 250,000 entries in a single JSON array, 1,000,000 entries in total (array entries plus object fields), and 128 levels of nested objects and arrays. Large captures can require substantially more memory to decode and analyze than their size on disk. Use the CLI directly for larger recordings or workflows that need source maps, capture, custom windows, or repeat runs.

The browser uploads only files you select. The server analyzes them in memory, does not save or retain them after returning the report, and does not resolve local paths or fetch source maps. To clear the displayed report, navigate to the printed URL again or close the tab. This UI does not start captures, run commands, or send reports to other services.
