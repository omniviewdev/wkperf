# Profile a stock Wails app

Import [`github.com/omniviewdev/wkperf/pkg/wailsprobe`](../pkg/wailsprobe) in a development build. The probe records selected application calls across their JavaScript and Go boundaries. It uses stock Wails v3 bindings, not a patched runtime or a second transport. The [native example](../examples/wailsprobe) pins Wails v3.1.0 and uses the same public API.

## Integrate the host and page

Start the probe before creating the Wails application, then register its service alongside your own. Keep it in development builds with trusted, app-owned frontend content only. Do not expose the service to arbitrary WebView navigation or production users.

```go
probe, err := wailsprobe.StartFromEnv()
if err != nil {
    return err
}
closeProbe := func() {
    if err := probe.Close(); err != nil {
        log.Printf("close profiling session: %v", err)
    }
}
defer closeProbe()

app := application.New(application.Options{
    Services: []application.Service{
        application.NewService(probe.Service()),
        application.NewService(&TextService{probe: probe}),
    },
    OnShutdown: closeProbe,
    // Add your usual asset and window configuration.
})
```

`OnShutdown` handles native shutdown even if `app.Run()` does not return; the deferred close handles setup errors. `Close` is idempotent. With no capture environment, the probe is disabled and creates no listener or event file.

Instrument the real Go method you want to measure. Pass the browser transaction ID as an ordinary binding argument and use it unchanged. Give the measured code exactly one `bridge.go.handler` span per application call. Use separate named spans or [Go trace regions](https://pkg.go.dev/runtime/trace#WithRegion) for finer app-owned work.

```go
func (s *TextService) Fingerprint(ctx context.Context, tx, text string) string {
    if tx != "" {
        end := s.probe.Begin("bridge.go.handler", tx, map[string]any{
            "requestBytes":  len(text),
            "responseBytes": sha256.Size * 2,
        })
        defer func() {
            if err := end(); err != nil {
                log.Printf("record fingerprint: %v", err)
            }
        }()
        if trace.IsEnabled() {
            ctx, task := trace.NewTask(ctx, "fingerprint")
            defer task.End()
            trace.Log(ctx, "wkperf.tx", tx)
        }
    }
    digest := sha256.Sum256([]byte(text))
    return hex.EncodeToString(digest[:])
}
```

The optional sizes describe the UTF-8 application input and hex output, not serialized Wails messages. Omit them if unknown. Never put payloads, secrets, or user-derived labels in probe metadata. The trace task and log are optional; they let you find the exact same ID in Go execution trace, independently of timestamp alignment.

Serve [`client.js`](../pkg/wailsprobe/client.js) as a frontend ES module, or serve the same source directly from `wailsprobe.ClientJS()` with `Content-Type: text/javascript; charset=utf-8`. Use `/wails/runtime.js` with Wails' bundled assets, as in the example. An app with a bundled frontend can pass `Call.ByName` from its own stock `@wailsio/runtime` import.

```js
import { Call } from "/wails/runtime.js";
import { createWailsProbe } from "/wailsprobe.js";

const probe = await createWailsProbe({ callByName: Call.ByName });
try {
    await probe.calibrate();
    const digest = await probe.callByName("main.TextService.Fingerprint", "abc");
    console.log(digest);
} finally {
    await probe.flush();
}
```

`callByName` prepends the transaction ID to the application arguments. Use the Wails qualified method name for the bound type (`main` for this example; the Go import path for a named package). For generated bindings, use `probe.call(tx => Fingerprint(tx, text))`; keep exactly one real binding invocation in the callback. You can also initialize with explicit service callbacks: `createWailsProbe({ clock: Clock, recordEvents: RecordEvents })`, then use `probe.call`. The adapter never changes the global runtime or intercepts unrelated calls.

- Initialization reads the host clock once to check enablement. Disabled application calls receive an empty transaction ID and return the original binding promise, including its cancellation API. Calibration and flush send nothing while disabled.
- Enabled application calls return ordinary promises. Rejection is preserved and marked; the wrapper does not expose the stock Wails `.cancel()` method. Use the original binding when that cancellation API is needed.
- Finish your calls before flushing. Call and clock events are buffered and sent in bounded batches outside the measured calls. Flush regularly during large runs (the queue holds up to 8,192 rows). A failed or over-capacity flush rejects. Do not treat it as a complete workload or retry uncertain writes: duplicate boundaries invalidate exact-ID joins. Flush before navigation or close; the adapter does not backfill lost events.

## Run the native smoke with public tools

From the repository root on macOS, with a local GUI session, Go 1.26.1 or newer and `jq`:

```sh
(cd examples/wailsprobe && GOWORK=off go build -o /tmp/wailsprobe-smoke .)
sh examples/wailsprobe/smoke.sh /tmp/wailsprobe-smoke
```

The script launches the actual native executable and uses only `curl`, `jq`, shell tools, and `go tool trace`. The page waits for an active Go trace, then runs its real **Compute SHA-256** action through stock Wails. It checks the result and flushes the call evidence. The script verifies the host PID, exact transaction IDs, matching browser and Go boundaries, a calibration bracket, and matching Go trace task and enclosing action markers. It prints a timing report and keeps the capture in the directory it reports. The fixture's explicit `-autorun` option is only for smoke; run `/tmp/wailsprobe-smoke` without it to use the same action interactively.

If the WebView exposes `console.timeStamp`, the smoke also requires two persisted app-side bounds around the action. It reports their count as `inspector_app_bounds`. Without that API, it requires zero rows. The smoke neither attaches an Inspector nor proves an Inspector clock mapping.

For your own app, create a private state directory. Set `OMNIVIEW_WKPERF_TRACE` to an absolute, nonexistent event file. Set `OMNIVIEW_WKPERF_READY` to a different absolute, nonexistent path to also enable the authenticated profiling listener. Omit the second variable for events only. Launch the actual executable so its PID can be checked. For example, in a POSIX shell:

```sh
umask 077
state=$(mktemp -d "${TMPDIR:-/tmp}/wailsprobe.XXXXXXXX")
OMNIVIEW_WKPERF_TRACE="$state/events.ndjson" \
OMNIVIEW_WKPERF_READY="$state/ready.json" \
  /tmp/wailsprobe-smoke >"$state/app.log" 2>&1 &
app_pid=$!
```

Once `ready.json` appears, read and remove it. It contains `{url,pid,token}`; verify the PID and loopback URL before sending the token. Pass the token on standard input, not in the command line, app, or capture report.

```sh
url=$(jq -er --argjson pid "$app_pid" \
  'select(.pid == $pid) | .url | select(test("^http://127[.]0[.]0[.]1:[0-9]+$"))' "$state/ready.json")
token=$(jq -er '.token' "$state/ready.json")
rm "$state/ready.json"

profile() {
  printf 'Authorization: Bearer %s\n' "$token" |
    curl -fsS --noproxy '*' --header @- "$url$1"
}

# While this runs, use the app or exercise the selected binding in its page.
profile '/debug/pprof/trace?seconds=15' >"$state/trace.out"
unset token
```

Record Go CPU separately with `/debug/pprof/profile?seconds=15`, then use `go tool pprof -top /path/to/executable "$state/cpu.pprof"`. To obtain a clock sample during execution trace, query `/debug/wkperf/clock` concurrently: the host emits a pair of trace logs bracketing the same monotonic reading returned as JSON. For automated work, the smoke also demonstrates `X-WKPerf-Capture-ID` and `/debug/wkperf/capture?id=…` to check a particular sampler's state and bounds. Close the app when finished. Review logs, profiles, and traces before sharing them.

```sh
jq -r 'select(.tx != null) | [.tx, .stage, .phase, .ts, .unit] | @tsv' "$state/events.ndjson"
go tool trace "$state/trace.out"
```

## Interpret the evidence

| Evidence | What it measures |
| --- | --- |
| `bridge.js.call` | Elapsed time from immediately before the app binding invocation to its promise settlement as observed by JS. |
| `bridge.go.handler` | Elapsed time between the app's explicit Go handler boundaries. It excludes Wails work outside those boundaries. |
| `clock.anchor` | One Go monotonic sample, bounded by the browser's observed send and receive times of a real calibration binding. The calibration is not an application timing sample. |
| `webkit.clock.anchor` | Browser `performance.now()` bounds around a real `console.timeStamp` invocation. An Inspector timestamp is separate evidence. |
| `trace.out` | Actual Go execution trace. The optional `wkperf.tx` log attaches the exact transaction ID to an app-defined trace task. |

Join by exact transaction ID in one captured process, not by proximity or call order. Browser rows also carry a document ID and `performance.timeOrigin`; do not align across reloads or processes. Compute durations on their own clocks: JS `performance.now()` in milliseconds, Go process-monotonic time in nanoseconds. Reject incomplete, duplicate, or failed pairs when computing successful call statistics.

Validate each `clock.anchor` against both complete spans for its calibration ID. Its send and receive times must match the browser boundaries and document, and its Go reading must fall within the Go handler span.

For a validated anchor, let `g = goTs / 10^6` in milliseconds. Ignoring browser quantization, `Go − JS` lies in `[g − jsReceive, g − jsSend]`. Widen the bracket for timestamp precision. For a browser clock that floors to buckets of width `q`, use `[g − (jsReceive + q), g − jsSend]`; two such readings give an elapsed-time uncertainty of up to `q`. The macOS smoke uses a 1 ms WKWebView floor assumption and reports that uncertainty; a measured duration of zero is not proof of zero elapsed time. Verify the clock behavior for another WebView or OS. Keep anchors near the calls, retain their full bounds, and report contradictory or missing brackets as unaligned. A midpoint is an estimate, not a one-way measurement.

The difference between JS round trip and Go handler time is residual time, not a measurement of transport, serialization, queueing, or response scheduling individually. The probe and Go execution trace add overhead. Repeat matched workloads with representative input sizes and separate capture modes before drawing performance conclusions.

Stock integration does **not** collect Wails internal stages, WebKit Inspector samples, presentation events, or WebKit worker/GPU activity. The Go execution trace describes the host process only.

## Align a separate WebKit Inspector recording

Attach a real Inspector to the same WebView and start its Timeline recording before the action. See [how to record an available Inspector target or export a Safari recording](RECORD.md). An inspectable macOS WebView does not supply a WebSocket endpoint. The Go profiling listener is not an Inspector transport.

Call `probe.markInspectorClock()` immediately before and after your action, then flush the events. It invokes `console.timeStamp("wkperf.clock." + tx)` between two `performance.now()` reads. The transaction ID is unique per marker. It returns `false` when the probe is disabled or `console.timeStamp` is unavailable. `true` only means the browser invoked the method; it does not mean an Inspector was attached or recorded the marker.

Match each app-side `webkit.clock.anchor` row to an actual timestamp marker with the exact label `wkperf.clock.<tx>` in the Inspector recording. Use at least two distinct, matching markers around the action from one document, with consistent bounds and a validated JS-to-Go anchor. Keep the browser clock precision allowance. Do not align an Inspector recording from app-side bounds alone, extend the mapping outside the bracketing markers, or replace missing markers with assumed times.

Use `wkperf analyze` or `wkperf ui` on the WebKit recording; these commands do not read the SDK event stream or Go trace.
