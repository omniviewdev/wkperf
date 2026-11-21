# Paired WebKit experiments

`wkperf experiment plan.json --out experiment-01` runs trusted commands on the local machine. The output directory must not exist. Each pair runs sequentially. Odd pairs run baseline then candidate; even pairs reverse order. The command inherits the working directory and environment. No shell is implied.

```json
{
  "pairs": 6,
  "metrics": ["layout.paint.p95_ms", "overview.duration_s"],
  "conditions": {"scenario": "scroll-fixed-pod-list", "cache": "warm", "viewport": "1280x800"},
  "baseline": ["./capture-build", "baseline", "{recording}"],
  "candidate": ["./capture-build", "candidate", "{recording}"]
}
```

Provide the `capture-build` executable for your application. It must select the specified build, establish the same fixture, run the same scenario, and write a WKPerf-readable WebKit export at the supplied path. Alternatively, give each variant a `wkperf record` argument vector directly. `{dir}` expands to a fresh per-run directory for sidecar evidence. Store exported identifiers, revision, display mode, input dataset hash, power state and warmup policy under `conditions`. Optional `environment` entries override inherited variables for both variants. Plans are copied verbatim: **do not put secrets in plan arguments, conditions or environment overrides**.

When a producer writes a directory, set `"recording": "capture/webkit.json"` and pass `--out {dir}/capture` to that producer. The path is relative to each run.

Pilot both commands before spending time on repeated captures. Use `wkperf summary CAPTURE.json --format json | jq -r '.metrics[].key'` with a single untrimmed capture and default configuration to find exact experiment keys. `query keys` and MCP also advertise query-only keys that experiment plans cannot use. Pick unique keys present in both pilots. Prefer at least three pairs; an odd number of pairs runs baseline first once more than candidate.

`experiment.json` records host architecture, OS, logical CPUs, available build/hardware details, timestamps, command durations, paths and scalar values. Each signed difference is candidate minus baseline in the metric's original unit. The report takes the median **of paired differences**, not the difference of group medians. An unavailable metric or a change in its unit or evidence basis fails the experiment. An interrupted run leaves completed recordings and no final report. Inspect individual recordings before attributing a difference to a patch. The runner does not establish significance, normalize unequal work, control the thermal state, or prove a cold filesystem cache.

For comparable startup runs, terminate the owned instance after every run and explicitly choose whether to clear its disposable profile. Never delete a user profile. For interaction runs, warm both variants and pin the workload completion event. Compare an uninstrumented control against the instrumented build before interpreting small effects.

On Unix, interruption sends `SIGTERM` to the active capture process group and follows with `SIGKILL` if it does not exit. A producer that launches a new process group or a detached process must terminate it itself. On other platforms, the runner only terminates the direct child.
