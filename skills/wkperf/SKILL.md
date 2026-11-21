---
name: wkperf
description: Capture and investigate real WebKit site performance with wkperf CLI or read-only MCP. Use for Safari Timeline exports, WebKitGTK/WPE Inspector targets, sampled JavaScript, rendering stalls, and controlled before/after experiments.
---

# Investigate WebKit performance

## Pick the evidence source

- Check `wkperf version` and `wkperf --help`. Live capture needs `record` and `targets`; older binaries can still read exports. From this checkout: `GOWORK=off go build -o /absolute/path/to/wkperf .`.
- Prefer an existing real capture if the investigation supplies one. Pass a WebKit Timelines JSON file, not a directory, URL, Chromium trace, or Playwright trace. Do not use `wkperf sample` as proof of real behavior.
- **WebKitGTK/WPE:** start a build with the Inspector HTTP server enabled and an inspectable page. Set `WEBKIT_INSPECTOR_HTTP_SERVER=127.0.0.1:9222` before launch. Run `wkperf targets http://127.0.0.1:9222/` and match the intended title; never pick the first entry blindly. `WEBKIT_INSPECTOR_SERVER` alone uses a different protocol. An empty list means no supported page was discovered; check page readiness, inspectability, port and WebKit build. Close any graphical inspector before attaching.
- **Safari / stock WKWebView on macOS:** open the intended page in Safari or select the inspectable app view from Safari's Develop menu. In Web Inspector → Timelines, select the instruments needed, start recording, perform the workload, stop, then export the recording JSON. `WKWebView.inspectable`, Safari WebDriver, and WebDriver BiDi do not supply a `wkperf record` endpoint. Do not pass a Safari automation URL to `targets` or `record`. Live `record` on macOS needs a separate bridge exposing that page's WebKit Inspector protocol with Timeline and ScriptProfiler.
- **Other explicit WebKit Inspector bridge:** pass the selected page's `ws://` or `wss://` URL directly to `record`. A browser-control WebSocket, CDP endpoint or remote target-list socket is not interchangeable.

Keep inspection endpoints on loopback or a trusted protected transport. Treat recordings and source maps as sensitive. Run only trusted workload scripts in a disposable or authorized page. Do not upload captures or use `--ai` unless asked.

## Repeatable live WebKitGTK site check

This exercises a real browser rendering a local page, not a synthetic recording. On Ubuntu 24.04 with `libwebkitgtk-6.0-4`, `python3`, `xvfb` and `xauth`, use the packaged MiniBrowser. If your distro does not ship it, substitute an inspectable WebKitGTK app. Run as a normal user. Use Xvfb only without a display. On macOS, run the browser in a Linux guest and use a loopback forward, or run wkperf in the guest too. Headless, software-rendered or emulated results describe that environment, not production frame delivery.

In a shell:

```sh
D=$(mktemp -d)
mkdir "$D/site"
cat > "$D/site/index.html" <<'HTML'
<!doctype html><meta charset="utf-8"><title>wkperf sample site</title>
<style>#list div{height:24px;padding:4px;border-bottom:1px solid silver}</style>
<h1>WebKit performance sample</h1><div id="list"></div>
HTML
python3 -m http.server 8120 --bind 127.0.0.1 --directory "$D/site" &
SITE_PID=$!
```

In a second terminal, launch the browser in the foreground:

```sh
MINIBROWSER=$(find /usr/lib -path '*/webkitgtk-6.0/MiniBrowser' -type f -print -quit)
WEBKIT_INSPECTOR_HTTP_SERVER=127.0.0.1:9222 \
  xvfb-run -a "$MINIBROWSER" http://127.0.0.1:8120/
```

After the page appears in `wkperf targets`, return to the first shell:

```sh
WS=$(wkperf targets http://127.0.0.1:9222/ | jq -er \
  'map(select(.name == "wkperf sample site")) | if length == 1 then .[0].endpoint else error("expected one sample page") end')
for rows in 300 80; do
  cat > "$D/render-$rows.js" <<JS
const list = document.querySelector('#list');
if (!list || document.title !== 'wkperf sample site') throw Error('wrong inspector page');
function renderRows(count) {
  const fragment = document.createDocumentFragment();
  for (let i = 0; i < count; i++) {
    const row = document.createElement('div');
    row.textContent = 'Visible row ' + i;
    fragment.append(row);
  }
  list.replaceChildren(fragment);
  return list.getBoundingClientRect().height;
}
list.replaceChildren();
for (let batch = 0; batch < 12; batch++) {
  await new Promise(requestAnimationFrame);
  renderRows($rows);
}
await new Promise(requestAnimationFrame);
list.replaceChildren();
await new Promise(requestAnimationFrame);
JS
  wkperf record --endpoint "$WS" --script "$D/render-$rows.js" --out "$D/rows-$rows.json"
done
wkperf compare "$D/rows-300.json" "$D/rows-80.json"
wkperf query "$D/rows-300.json" keys 'layout.*' --limit 30
wkperf query "$D/rows-300.json" metrics layout.recalculate-styles.p95_ms
```

For a manual interaction use `record --duration 15s`; for external automation use `record --out FILE --endpoint "$WS" -- ./exercise-ui` and wait for work to finish in that executable. Use only one of duration, script or executable. If no samples appear, check instrument support, page readiness, selected time window and whether the interaction ran. Stop the foreground MiniBrowser with Ctrl-C and stop the site server (`kill "$SITE_PID"`). Do not close unrelated browser sessions.

## Narrow one recording

```sh
CAPTURE=/absolute/path/to/capture.json
wkperf analyze "$CAPTURE" --brief --max-bytes 8000
wkperf query "$CAPTURE" keys 'layout.*' --limit 20
wkperf query "$CAPTURE" metrics 'layout.composite.*' --limit 10
wkperf query "$CAPTURE" events --limit 10
wkperf query "$CAPTURE" top --by self --limit 10
wkperf query "$CAPTURE" window 0 0.25 top --limit 10
```

1. Check `.sources[].from`, `.sources[].to`, sample counts, and instrument coverage. Window arguments use seconds relative to recording start. Raw record timestamps use the export clock; subtract `startTime` before using an export timestamp as a window bound.
2. Pick the metric or event that actually changed. `keys` gives the key's `op`, `unit`, description and evidence `basis`. Query that exact key with `metrics` or `rootcause`. Follow `next_offset` while `truncated` is true. If byte-limited, shrink the request first.
3. Find sampled call paths with `top`, `find`, `paths`, `callers` or `callees`. Resolve `rows[].function_id` through `functions[id]`. If a name is ambiguous, use a returned stable function ID. Use the exact `node_id` from `paths` or `stacks` for one calling context. Use `--sourcemap FILE` or MCP `sourcemaps` only with maps matching the deployed bundle. Missing maps limit attribution; they do not invalidate a measured layout span.
4. Inspect raw evidence when needed. `record` writes top-level `.records`; many Safari exports wrap them under `.recording`. For example, show the ten longest top-level layout records with their original zero-based indices:

```sh
jq '(.recording // .).records | to_entries | map(select((.value.type == "timeline-record-type-layout" or .value.type == "layout") and (.value.endTime // 0) > (.value.startTime // 0)) | {index: .key, event: .value.eventType, start: .value.startTime, end: .value.endTime, ms: ((.value.endTime - .value.startTime) * 1000)}) | sort_by(-.ms) | .[:10]' "$CAPTURE"
```

Measured spans are not sampled function time. Unclocked samples might be excluded by a window; inspect `untimed_samples` and `untimed_excluded`. An empty frame series or unsupported optional instrument is missing evidence, not zero work. Long `composite` spans do not alone prove missed display presents. Generic WebKit evidence cannot isolate native host, Go, transport, or scheduling time. Use an independent clock mapping before joining separate profilers.

## Use read-only MCP

Configure an stdio MCP server with `command: /absolute/path/to/wkperf` and `args: [mcp]`. The server only queries local recording files. Capture and experiment orchestration stay in the CLI. Send newline-delimited JSON-RPC; wait for `initialize`, send `notifications/initialized`, then call `tools/list`. The server provides input and output schemas and read-only tool annotations. Use absolute paths the server process can read.

Minimal direct check (`jq` substitutes the path safely):

```sh
{
  printf '%s\n' \
    '{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18","capabilities":{},"clientInfo":{"name":"wkperf-check","version":"1"}}}' \
    '{"jsonrpc":"2.0","method":"notifications/initialized"}' \
    '{"jsonrpc":"2.0","id":2,"method":"tools/list"}'
  jq -cn --arg file "$CAPTURE" '{jsonrpc:"2.0",id:3,method:"tools/call",params:{name:"keys",arguments:{file:$file,selector:"layout.*",limit:8}}}'
} | wkperf mcp | jq -c 'if .id == 2 then [.result.tools[].name] elif .id == 3 then (.result.structuredContent // (.result.content[0].text | fromjson)) else . end'
```

- Read `result.structuredContent` first; `content[0].text` encodes the same document. Check `result.isError`, `status`, `errors[].code`, and returned `candidates` before drawing conclusions. A top-level JSON-RPC `error` instead indicates a protocol or dispatch failure, such as unknown tool or a call sent before initialization.
- Regular calls take `file`; selector-based calls add `selector`. `window` requires `from` and `to`, with optional `query_operation`. Its result names the executed operation. `diff` takes baseline `file` and `candidate`. Prefer `limit` 5–20 and exact selectors after discovery. MCP names are snake_case even where CLI flags use hyphens.
- `wkperf schema` prints the CLI JSON Schema; discover MCP tool schemas through `tools/list`. Use `limit`, `offset`, `max_bytes` and `max_stack_frames` to keep evidence bounded. Source maps and ownership globs are optional inputs, not sources of profile samples.

## Run and report a controlled repeat

- Record the browser/engine, version, build, viewport, fixture, input, warmup, capture window, and instrumentation. Change one factor. Fix work by action count or readiness, not just elapsed time. Keep raw recordings and never overwrite the input artifact.
- Run at least three pairs and alternate order. `wkperf experiment PLAN.json --out NEW_DIR` runs trusted argument vectors and substitutes `{recording}`. Set `pairs`, `metrics`, `baseline`, `candidate` and `conditions`; discover available keys before writing the plan. See [paired experiments](../../docs/EXPERIMENT.md). The report gives individual candidate-minus-baseline differences and their median. A single pair is exploratory; paired medians are not causal or statistical proof.
- Report the measured symptom, units, baseline/candidate values or paired deltas, file plus record index or query key/window, sample coverage, and the strongest remaining uncertainty. Separate candidate explanations from observations. Suggest one falsifiable follow-up, not an unqualified fix.

See [capture flags and protocol limits](../../docs/RECORD.md).

## Verified coverage and gaps

- The local site ran in WebKitGTK 2.52.6 and was discovered over the Inspector HTTP server. Six live scripted captures completed in alternating order. For 80 versus 300 rows, the median paired differences were −7.230 ms for `layout.recalculate-styles.p95_ms` and −0.292 s for capture duration. This was an Xvfb Linux guest, not a production benchmark.
- Read-only MCP exercised all 16 tools with real WebKitGTK exports. It also queried sampled functions, trigger events, and composite metrics from an existing wrapped Safari Web Inspector export. Discovery advertised valid JSON Schemas; structured and text results agreed. Negative cases covered premature calls, unknown tools and keys, missing files, bad arguments and reversed windows.
- Safari 27.0.1 exposed a WebDriver status endpoint but no Inspector HTTP target listing there. WebDriver session creation reported that remote automation was disabled. No Safari settings changed; no fresh Safari GUI export or WPE capture was verified.
