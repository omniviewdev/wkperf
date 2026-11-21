# Record a WebKit target

`wkperf record` attaches to an existing WebKit Inspector Protocol page WebSocket. For WebKitGTK/WPE, `wkperf targets http://127.0.0.1:9222/` lists names and socket URLs without opening a browser. Select the intended page; do not assume the first target belongs to your application.

```sh
wkperf record --endpoint ws://127.0.0.1:9222/socket/1/1/WebPage --duration 15s --out scroll.json
wkperf record --endpoint "$WEBKIT_TARGET_WS" --out loaded.json -- ./exercise-ui --scenario loaded
wkperf record --endpoint "$WEBKIT_TARGET_WS" --script exercise.js --out scripted.json
wkperf analyze loaded.json
wkperf flame loaded.json --format html --out loaded.html
```

Use `--duration` for an interactive or continuously running workload. The default is 10 seconds. To bound the recording by an executable, put its name and arguments after `--` and omit `--duration`. The executable runs directly, not through a shell, after WebKit acknowledges both instruments. Its stdout and stderr go to stderr. Capture ends when the executable exits successfully; have it wait for the UI action to finish if that action is asynchronous. A disconnect, target swap, timeout during protocol setup or flush, failed executable, or cancellation fails the recording and leaves an existing output file unchanged.

Use `--script FILE` to run trusted JavaScript in the inspected page after both instruments start. Do not combine it with a duration or executable. The file is an async function body, so it can use `await` directly. Write it to resolve only when the UI action has finished. The recorder uses WebKit's [Runtime protocol](https://github.com/WebKit/WebKit/blob/main/Source/JavaScriptCore/inspector/protocol/Runtime.json) to await completion for up to 60 seconds. A JavaScript exception or rejection fails the capture and reports the target's exception. The recorder does not embed the source or evaluation result in the profile. Normal profiler evidence can still include names and locations of executed functions.

Only use scripts you trust. They run with the page's privileges. A timeout or cancellation detaches the inspector; it cannot undo page actions or stop JavaScript already running.

## Pick a real endpoint

- **WebKitGTK/WPE with Web Inspector HTTP support:** Start an inspectable page with `WEBKIT_INSPECTOR_HTTP_SERVER=127.0.0.1:9222` in the app environment. Open `http://127.0.0.1:9222/` to identify the correct page. The inspect action points to `Main.html?ws=HOST/socket/CONNECTION/TARGET/WebPage`; pass `ws://HOST/socket/CONNECTION/TARGET/WebPage` with the actual IDs. Close the graphical inspector before running `wkperf`. `WEBKIT_INSPECTOR_SERVER` alone configures WebKit's separate socket protocol, not the WebSocket endpoint.
- **Another embedder or WebKit bridge:** Pass its explicit `ws://` or `wss://` target URL. The endpoint must expose WebKit `Timeline` and `ScriptProfiler` directly or through the WebKit `Target` domain. The latter routes to the current non-provisional page, not to worker or subframe targets.
- **Safari or an inspectable WKWebView on macOS:** a website URL is not an Inspector endpoint. `WKWebView.inspectable` allows Safari's Develop menu to inspect the view but does not open a WebSocket. Record and export a Timelines recording in Safari, then open it in `wkperf ui` or run `wkperf analyze`.

Inspector connections can control the inspected app and recordings may contain sensitive source paths, URLs, and event details. Keep the endpoint on loopback or use a trusted protected transport. Turn off inspection when finished.

## Evidence and limits

The output is wkperf-compatible flat Timelines JSON, not a byte-for-byte Safari export. It contains WebKit recording timestamps, script events and samples, and observed layout, paint, composite, and rendering-frame records. CPU and memory use WebKit's optional [`CPUProfiler`](https://github.com/WebKit/WebKit/blob/main/Source/JavaScriptCore/inspector/protocol/CPUProfiler.json) and [`Memory`](https://github.com/WebKit/WebKit/blob/main/Source/JavaScriptCore/inspector/protocol/Memory.json) instruments. These are WebKit resource observations, not host-wide CPU or memory totals. `jq .captureCapabilities recording.json` reports each optional instrument as `captured`, `enabled-no-samples`, or `unsupported`. The recorder does not collect network, GC, screenshots, or worker/subframe evidence.

WebKit may omit frame records or return zero-timestamp samples; wkperf does not synthesize frames or reconstruct sample timestamps. The sampler's one-millisecond fallback is a sample weight, not a timestamp. The recorder [enables WebKit's Page agent](https://github.com/WebKit/WebKit/blob/main/Source/WebCore/inspector/agents/InspectorPageAgent.cpp) to start the execution clock on older engines and rejects an entirely stalled clock. Setup and flushing each have a 15-second protocol deadline; the WebSocket caps individual incoming messages at 128 MiB, including the final sample buffer.

Primary sources: WebKit's [Inspector HTTP server](https://github.com/WebKit/WebKit/blob/main/Source/WebKit/UIProcess/Inspector/glib/RemoteInspectorHTTPServer.cpp), [target list and URL construction](https://github.com/WebKit/WebKit/blob/main/Source/WebKit/UIProcess/Inspector/glib/RemoteInspectorClient.cpp), [initialization and environment settings](https://github.com/WebKit/WebKit/blob/main/Source/WebKit/UIProcess/API/glib/WebKitInitialize.cpp), [Timeline agent](https://github.com/WebKit/WebKit/blob/main/Source/WebCore/inspector/agents/InspectorTimelineAgent.cpp), [ScriptProfiler protocol](https://github.com/WebKit/WebKit/blob/main/Source/JavaScriptCore/inspector/protocol/ScriptProfiler.json), [Target protocol](https://github.com/WebKit/WebKit/blob/main/Source/JavaScriptCore/inspector/protocol/Target.json), [frontend conversion and sample weighting](https://github.com/WebKit/WebKit/blob/main/Source/WebInspectorUI/UserInterface/Controllers/TimelineManager.js), and [recording export format](https://github.com/WebKit/WebKit/blob/main/Source/WebInspectorUI/UserInterface/Models/TimelineRecording.js).
