#!/bin/sh
set -eu

if [ "$#" -ne 1 ] || [ ! -x "$1" ]; then
    printf 'usage: sh examples/wailsprobe/smoke.sh /absolute/path/to/wailsprobe-smoke\n' >&2
    exit 2
fi

umask 077
capture=$(mktemp -d "${TMPDIR:-/tmp}/wailsprobe.XXXXXXXX")
ready="$capture/ready.json"
events="$capture/events.ndjson"
log="$capture/app.log"
sampler=wailsprobe-smoke
printf 'Capture: %s\n' "$capture" >&2

OMNIVIEW_WKPERF_TRACE="$events" OMNIVIEW_WKPERF_READY="$ready" \
    "$1" -autorun >"$log" 2>&1 &
app=$!
cleanup() {
    kill "$app" 2>/dev/null || :
    wait "$app" 2>/dev/null || :
    rm -f "$ready"
}
trap cleanup EXIT
trap 'exit 130' INT
trap 'exit 143' TERM

fail() {
    printf '%s; see %s\n' "$1" "$capture" >&2
    exit 1
}

i=0
until [ -s "$ready" ] && grep -Fq 'wailsprobe smoke ready' "$log"; do
    [ "$i" -lt 300 ] && kill -0 "$app" 2>/dev/null || fail 'Native Wails frontend did not become ready'
    i=$((i + 1))
    sleep 0.05
done

url=$(jq -er --argjson pid "$app" 'select(.pid == $pid) | .url | select(test("^http://127[.]0[.]0[.]1:[0-9]+$"))' "$ready")
token=$(jq -er '.token | select(test("^[A-Za-z0-9]+$"))' "$ready")
rm "$ready"

profile() {
    printf 'Authorization: Bearer %s\n' "$token" |
        curl --fail --silent --show-error --max-time 15 --noproxy '*' --header @- \
            --header "X-WKPerf-Capture-ID: $sampler" "$url$1"
}

profile '/debug/pprof/trace?seconds=5' >"$capture/trace.out" &
trace_job=$!
i=0
until profile "/debug/wkperf/capture?id=$sampler" | jq -e '.active == true' >/dev/null; do
    [ "$i" -lt 50 ] && kill -0 "$trace_job" 2>/dev/null || fail 'Go trace did not start'
    i=$((i + 1))
    sleep 0.05
done
profile '/debug/wkperf/clock' >"$capture/host-clock.json"
wait "$trace_job" || fail 'Go trace download failed'
profile "/debug/wkperf/capture?id=$sampler" >"$capture/sampler.json"
unset token

grep -Fq 'wailsprobe smoke digest: ba7816bf8f01cfea414140de5dae2223b00361a396177a9cb410ff61f20015ad' "$log" ||
    fail 'Frontend did not receive and flush the expected SHA-256 result'

inspector_bounds=$(awk '/ wailsprobe smoke Inspector app-side bounds: [02]$/ { print $NF }' "$log")
case "$inspector_bounds" in
    0|2) ;;
    *) fail 'Frontend did not report consistent Inspector marker support' ;;
esac

jq -es --argjson pid "$app" --argjson inspectorBounds "$inspector_bounds" --slurpfile sampler "$capture/sampler.json" '
    def pair($tx; $stage; $clock; $unit):
        [ .[] | select(.tx == $tx and .stage == $stage) ]
        | if map(.phase) == ["begin", "end"]
            and all(.[]; .pid == $pid and .clock == $clock and .unit == $unit and (.ts | type) == "number")
            and .[0].ts <= .[1].ts
          then . else error("invalid " + $stage + " pair for " + $tx) end;
    def web($tx): pair($tx; "bridge.js.call"; "js.performance_now"; "ms");
    def host($tx): pair($tx; "bridge.go.handler"; "go.process_monotonic"; "ns");
    [ .[] | select(.stage == "bridge.js.call" and .phase == "begin") ] as $starts
    | if ($starts | length) == 2 then . else error("expected two browser calls") end
    | ($starts | map(select(.detail.calibration == 1)) | .[0].tx // error("missing calibration call")) as $cal
    | ($starts | map(select(.detail.calibration != 1)) | .[0].tx // error("missing fingerprint call")) as $tx
    | if ($cal | length) > 0 and ($tx | length) > 0 and $cal != $tx then . else error("nonunique transaction IDs") end
    | web($cal) as $calWeb | host($cal) as $calGo
    | web($tx) as $appWeb | host($tx) as $appGo
    | [ .[] | select(.stage == "clock.anchor" and .tx == $cal) ] as $marks
    | $marks[0] as $anchor
    | $anchor.detail as $anchorDetail
    | $sampler[0] as $trace
    | [ .[] | select(.stage == "webkit.clock.anchor") ] as $inspector
    | if ($marks | length) == 1 and $anchor.phase == "mark" and $anchor.pid == $pid
        and $anchor.clock == "js.performance_now" and $anchor.unit == "ms"
        and ($anchor.document | type) == "string" and ($anchor.document | length) > 0
        and $anchor.timeOrigin > 0
        and all(($calWeb + $appWeb)[]; .document == $anchor.document and .timeOrigin == $anchor.timeOrigin)
        and ($calWeb[1].detail.failed // 0) == 0 and ($appWeb[1].detail.failed // 0) == 0
        and $anchorDetail.timeOrigin == $anchor.timeOrigin
        and $anchorDetail.jsSend == $calWeb[0].ts and $anchorDetail.jsReceive == $calWeb[1].ts
        and $anchor.ts == $anchorDetail.jsReceive
        and $anchorDetail.goClock == "go.process_monotonic" and $anchorDetail.goUnit == "ns"
        and $anchorDetail.goTs >= $calGo[0].ts and $anchorDetail.goTs <= $calGo[1].ts
        and $appGo[0].detail.requestBytes == 3 and $appGo[0].detail.responseBytes == 64
        and ($inspector | length) == $inspectorBounds
        and all($inspector[];
            .phase == "mark" and .pid == $pid and .clock == "js.performance_now" and .unit == "ms"
            and .document == $anchor.document and .timeOrigin == $anchor.timeOrigin
            and (.tx | type) == "string" and (.tx | length) > 0 and .tx != $tx and .tx != $cal
            and (.detail.jsBefore | type) == "number" and (.detail.jsAfter | type) == "number"
            and .detail.jsBefore >= 0 and .detail.jsBefore <= .detail.jsAfter and .ts == .detail.jsAfter)
        and ($inspectorBounds == 0 or (
            $inspector[0].tx != $inspector[1].tx and $inspector[0].ts <= $appWeb[0].ts
            and $appWeb[1].ts <= $inspector[1].detail.jsBefore))
        and $trace.id == "wailsprobe-smoke" and $trace.kind == "trace" and $trace.active == false
        and $trace.start.pid == $pid and $trace.stop.pid == $pid
        and $trace.start.ts <= $calGo[0].ts and $trace.stop.ts >= $appGo[1].ts
      then {
        tx: $tx, document: $anchor.document, calibration_tx: $cal,
        js_round_trip_ms: ($appWeb[1].ts - $appWeb[0].ts),
        js_round_trip_uncertainty_ms: 1,
        inspector_app_bounds: $inspectorBounds,
        go_handler_ns: ($appGo[1].ts - $appGo[0].ts),
        # This macOS WKWebView smoke assumes performance.now() floors to 1 ms.
        go_minus_js_ms: [($anchorDetail.goTs / 1e6 - ($anchorDetail.jsReceive + 1)), ($anchorDetail.goTs / 1e6 - $anchorDetail.jsSend)]
      } else error("missing clock bracket, Inspector app-side bounds, fingerprint payload evidence or Go trace coverage") end
' "$events" >"$capture/smoke.json"

go tool trace -d=parsed "$capture/trace.out" >"$capture/trace.txt"
tx=$(jq -er '.tx' "$capture/smoke.json")
awk -v tx="$tx" '
    function field(prefix, i) {
        for (i = 1; i <= NF; i++) if (index($i, prefix) == 1) return substr($i, length(prefix) + 1)
        return ""
    }
    / Log / && / Category="wkperf.smoke" Message="frontend released"/ { released = NR }
    / Log / && / Category="wkperf.smoke" Message="frontend completed"/ { completed = NR }
    / TaskBegin / && / Type="fingerprint"/ { begin[field("ID=")] = NR }
    / TaskEnd / && / Type="fingerprint"/ { finish[field("ID=")] = NR }
    / Log / && index($0, " Category=\"wkperf.tx\" Message=\"" tx "\"") {
        count++
        task = field("Task=")
        logged = NR
    }
    END {
        exit !(released && completed && count == 1 && task != "" &&
            task in begin && task in finish &&
            released < begin[task] && begin[task] < logged && logged < finish[task] && finish[task] < completed)
    }
' "$capture/trace.txt" || fail 'Exact fingerprint task was not enclosed by the frontend release and completion in the Go trace'
cat "$capture/smoke.json"
