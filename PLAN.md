# wkperf — design

Compare WebKit Web Inspector Timelines exports (baseline vs candidate). Go, cobra + viper.
Install: `GOPRIVATE=github.com/omniviewdev go install github.com/omniviewdev/wkperf@latest`

## Pipeline
model.Load (JSON) -> analyze.Summarize -> metrics.Summary (flat metrics + function stats + chart series)
-> compare.Compare(baseGroup, candGroup) -> compare.Result -> report/{term,html,jsonout}

## Packages (contracts already in internal/model, internal/metrics, internal/compare/types.go)
- internal/analyze: `Summarize(rec *model.Recording, source string) *metrics.Summary`
- internal/compare: `Compare(base, cand []*metrics.Summary, opts Options) *Result`, `DefaultOptions()`
- internal/report/term: `Render(w io.Writer, r *compare.Result, o term.Options) error`
- internal/report/html: `Render(w io.Writer, r *compare.Result, o html.Options) error` (single self-contained file)
- internal/report/jsonout: `Render(w io.Writer, r *compare.Result) error`
- internal/testgen: synthetic recording generator for tests/demos
- cmd + main.go: cobra commands `compare`, `summary`, `sample`, `version`; viper config

## Commands
- `wkperf compare BASE... --vs CAND...` (also `wkperf compare a.json b.json`): multi-run groups -> medians
- `wkperf summary FILE...`: metrics for one group
- `wkperf sample --out DIR`: write demo baseline/candidate exports
- flags/config (viper: flags > env WKPERF_* > ~/.config/wkperf/config.yaml / ./.wkperf.yaml): format(term|html|json|all), out, threshold, top, color, fail-on-regression, open

## Metrics (analyze)
overview: duration, record counts. frames: count, mean/p50/p95/p99/max ms, fps, jank>16.7ms/33.4ms/50ms counts+ratio.
script: per eventType count/total/mean/p95/max ms + overall script total/busy ratio. layout: per eventType (layout, recalculate-styles, paint, composite, forced-layout...) count/total/mean/p95/max.
gc: count/total ms. cpu: mean/p95/max total + main-thread mean/p95. memory: peak/final/mean MB + per-category peak. network: requests, total transfer bytes, mean/p95 time.
memory-pressure event count. samples: function self/total ms, sample count, top-N.
