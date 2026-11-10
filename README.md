# wkperf

`wkperf` compares two groups of WebKit Web Inspector **Timelines** exports (a baseline and a candidate) and reports what got faster, what got slower, and by how much. It writes a terminal report, a single self-contained HTML report, and a JSON report.

It analyzes rendering frames (frame time, FPS, jank), script and layout events, GC, CPU, memory, network, and sampling-profiler function self time.

## Install

The repository is private, so Go needs git access to the `omniviewdev` org:

```bash
GOPRIVATE=github.com/omniviewdev go install github.com/omniviewdev/wkperf@latest
```

Or build from a checkout:

```bash
go build -o wkperf .
```

## Exporting a recording

1. In Safari (or any WebKit view with Web Inspector enabled), open Web Inspector and go to the **Timelines** tab.
2. Record the scenario you want to measure, then stop the recording.
3. Click the **Export** button in the timeline view (or press `Cmd+S`) and save the `.json` file.

### Recommended workflow

- Record the **same scenario** on both sides, with the same steps and duration.
- Capture **at least 5 runs per side**. With several runs wkperf compares medians and marks changes inside run-to-run noise as `noisy`.
- Keep runs in one directory per side, e.g. `baseline/` and `candidate/`.

## Usage

```bash
# Try it with synthetic data
wkperf sample --out demo
wkperf compare demo/baseline-*.json --vs demo/candidate-*.json

# Groups: base files are positional, candidates follow --vs
wkperf compare base-1.json base-2.json base-3.json --vs cand-1.json cand-2.json cand-3.json

# Directories (all *.json inside)
wkperf compare ./baseline --vs ./candidate

# Exactly two files, no --vs: 1v1
wkperf compare before.json after.json

# Reports
wkperf compare ./baseline --vs ./candidate --format html --out report.html --open
wkperf compare ./baseline --vs ./candidate --format json > result.json
wkperf compare ./baseline --vs ./candidate --format all --out reports/

# One group on its own
wkperf summary ./baseline
wkperf summary ./baseline --format json

wkperf version
```

`--vs` may be repeated, comma separated, or followed by several files. Put baseline files before `--vs`.

### compare flags

| Flag | Default | Meaning |
| --- | --- | --- |
| `--vs` | | candidate files or directories |
| `--format` | `term` | `term`, `html`, `json`, or `all` |
| `--out` | `wkperf-report.html` | output file for html/json; directory or prefix with `all`. JSON with no `--out` prints to stdout |
| `--threshold` | `0.05` | relative change below which a metric is unchanged |
| `--top` | `10` | functions listed per direction |
| `--verbose` | off | show unchanged metrics too |
| `--no-color` | off | disable color (`NO_COLOR` is respected; color is off when stdout is not a TTY) |
| `--fail-on-regression` | off | exit 2 on a confident regression |
| `--open` | off | open the HTML report |
| `--title` | | HTML report title |

## Configuration

Precedence: flags > environment (`WKPERF_<FLAG>`, dashes become underscores, e.g. `WKPERF_FAIL_ON_REGRESSION=true`) > config file. The config file is `./.wkperf.yaml`, else `$XDG_CONFIG_HOME/wkperf/config.yaml` (default `~/.config/wkperf/config.yaml`).

```yaml
format: all
out: reports/
threshold: 0.03
top: 15
no-color: false
fail-on-regression: true
```

## Metric glossary

| Group | Metrics |
| --- | --- |
| overview | recording duration, record counts |
| frames | frame count; mean/p50/p95/p99/max frame time (ms); FPS; jank counts and ratio for frames over 16.7 / 33.4 / 50 ms |
| script | per event type (event-dispatched, timer-fired, animation-frame-fired, ...) count, total, mean, p95, max ms; total script time and busy ratio |
| layout | per event type (layout, recalculate-styles, paint, composite, forced-layout, ...) count, total, mean, p95, max ms |
| gc | garbage collection count and total ms |
| cpu | total CPU mean/p95/max %; main-thread mean/p95 % |
| memory | peak, final, mean MB; per-category peak |
| network | request count, transferred bytes, mean/p95 request time |
| samples | per-function self and total time from the sampling profiler |

Verdicts: `improved`, `regressed`, `unchanged`, `noisy` (change within run-to-run noise), `info` (informational metric), `new`, `removed`. Confidence is `single-run`, `low`, or `high`.

## Exit codes

| Code | Meaning |
| --- | --- |
| 0 | success |
| 1 | error (bad input, unreadable file, write failure) |
| 2 | `--fail-on-regression` and a regressed metric with confidence other than `low` in the frames, script, layout, or cpu groups |

## CI usage

```yaml
- run: |
    wkperf compare baseline/ --vs candidate/ \
      --format all --out wkperf-reports/ --no-color --fail-on-regression
- uses: actions/upload-artifact@v4
  if: always()
  with:
    name: wkperf-reports
    path: wkperf-reports/
```
