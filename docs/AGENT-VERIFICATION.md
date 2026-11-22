# Blind WebKit investigation check

An agent received eight anonymized WebKitGTK 2.54.0 Inspector recordings of a real page, the `wkperf` binary, and the [agent skill](../skills/wkperf/SKILL.md). It did not receive the page source, workload scripts, timings, or variant assignments. The harness alternated order across four pairs. Both variants updated the same elements; one interleaved geometry reads with writes, while the other batched them.

Through the stdio MCP server, the agent independently identified the batched variant in all four pairs. `metrics` showed 12 versus 1,152 forced layouts, despite identical script task and layout invalidation counts. Total forced-layout time was 26.9–27.5 ms versus 120.2–126.8 ms. `window` located a burst with one versus 96 forced layouts; `top` identified the sampled workload function. Per-layout p95 alone pointed in the wrong direction because the slower variant performed many shorter layouts. The agent recommended batching geometry access without seeing the implementation.

A subsequent capture with the stricter protocol lifecycle and clock validation reproduced the counts across four more pairs. This verifies evidence navigation and a specific code-level hypothesis for this workload, not production frame delivery, visual equivalence, or a general speedup. Raw captures stay local. Follow [the skill](../skills/wkperf/SKILL.md) to run your own controlled experiment.

## Analysis overhead

`BenchmarkTriggerStatsLongRecording` exercises attribution across 6,000 callbacks, 512 timer IDs and 30,000 samples. On darwin/arm64 (Apple M5 Max, Go 1.27.4), the median of five single-iteration measurements was 107.46 ms and 1,003,991,080 allocated bytes before indexing, versus 24.70 ms and 21,061,576 bytes after indexing. The same benchmark file ran against both implementations; these are process allocations during analysis, not application heap usage or a browser speedup.

```sh
GOWORK=off go test ./internal/insights -run '^$' -bench '^BenchmarkTriggerStatsLongRecording$' -benchmem -benchtime=1x -count=5
```

### Profiling the analyzer itself

The v0.7 audit compared release binaries against v0.6.0, not instrumented browser timings. Ten existing wrapped WebKit exports ranged from 335,390 to 728,766,611 bytes. Both binaries received identical paths, `--ai=false`, explicit ownership rules and `--format json`. The complete JSON reports matched byte for byte. A clipped metrics query, a clipped hotspot query and a folded-stack export also matched. This checks stability against an earlier implementation, not the absolute accuracy of the underlying browser clocks.

On one 72,922,023-byte recording, 1,025 samples contained 159,069 frame occurrences but only 536 distinct generated frames. [Go CPU and allocation profiles](https://go.dev/doc/diagnostics#profiling) identified repeated frame hashing, GC-detail decoding, per-window occupancy sweeps and whole-recording JSON copies. Reusing frame identities and measured intervals preserves the original IDs and weights. Common non-GC records no longer require a JSON decode. Regular files up to 128 MiB use a size-aware input buffer; larger files do not preallocate their advertised size. This last bound is not a limit on total memory use.

To reproduce the component measurements with your own recording:

```sh
export GOWORK=off WKPERF_BENCH_RECORDING=/absolute/path/to/recording.json
go test ./internal/model -run '^$' -bench 'Benchmark(Load|Parse)Recording$' -benchmem -benchtime=1x -count=5
go test ./internal/rootcause -run '^$' -bench '^BenchmarkRealProfile$' -benchmem -benchtime=1x -count=5
```

For end-to-end measurements, run `wkperf analyze "$WKPERF_BENCH_RECORDING" --ai=false --format json` as a fresh process. Alternate binary order within successive pairs, use the same output destination, and check report equality separately. Report wall time and process peak RSS independently of cumulative allocated bytes. Peaks and timings depend on the build toolchain, data and operating system.
