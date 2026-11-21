# Blind WebKit investigation check

An agent received eight anonymized WebKitGTK 2.54.0 Inspector recordings of a real page, the `wkperf` binary, and the [agent skill](../skills/wkperf/SKILL.md). It did not receive the page source, workload scripts, timings, or variant assignments. The harness alternated order across four pairs. Both variants updated the same elements; one interleaved geometry reads with writes, while the other batched them.

Through the stdio MCP server, the agent independently identified the batched variant in all four pairs. `metrics` showed 12 versus 1,152 forced layouts, despite identical script task and layout invalidation counts. Total forced-layout time was 26.9–27.5 ms versus 120.2–126.8 ms. `window` located a burst with one versus 96 forced layouts; `top` identified the sampled workload function. Per-layout p95 alone pointed in the wrong direction because the slower variant performed many shorter layouts. The agent recommended batching geometry access without seeing the implementation.

A subsequent capture with the stricter protocol lifecycle and clock validation reproduced the counts across four more pairs. This verifies evidence navigation and a specific code-level hypothesis for this workload, not production frame delivery, visual equivalence, or a general speedup. Raw captures stay local. Follow [the skill](../skills/wkperf/SKILL.md) to run your own controlled experiment.

## Analysis overhead

`BenchmarkTriggerStatsLongRecording` exercises attribution across 6,000 callbacks, 512 timer IDs and 30,000 samples. On darwin/arm64 (Apple M5 Max, Go 1.27.4), the median of five single-iteration measurements was 107.46 ms and 1,003,991,080 allocated bytes before indexing, versus 24.70 ms and 21,061,576 bytes after indexing. The same benchmark file ran against both implementations; these are process allocations during analysis, not application heap usage or a browser speedup.

```sh
GOWORK=off go test ./internal/insights -run '^$' -bench '^BenchmarkTriggerStatsLongRecording$' -benchmem -benchtime=1x -count=5
```
