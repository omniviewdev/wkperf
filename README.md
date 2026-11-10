# wkperf

**Did that change actually make the UI faster?** `wkperf` answers that from WebKit Web Inspector timeline recordings.

Safari's Web Inspector (and any WKWebView app, like Omniview) can export a Timelines recording as JSON. The inspector can open that file, but it can't compare two of them or tell you what to fix. `wkperf` does both:

- **`compare`**: put a *before* and an *after* side by side. See what got faster, what got slower, and what is just noise.
- **`analyze`**: look at one recording and see where the time goes, which functions are hot, which frames were slow and why, plus concrete suggestions.
- **`--ai`**: hand the findings to an LLM (Claude, OpenAI, or a local model) for a ranked fix list, or print the prompt and paste it into your own agent.

Output is a terminal report, a single self-contained HTML file you can share, or JSON for CI.

## Install

The repo is private, so you need git access to the `omniviewdev` org:

```bash
GOPRIVATE=github.com/omniviewdev go install github.com/omniviewdev/wkperf@latest
```

Needs Go 1.26+. Check it worked with `wkperf version`.

## Try it in 30 seconds

No recording needed. Generate some fake ones:

```bash
wkperf sample --out demo
wkperf compare demo/baseline-*.json --vs demo/candidate-*.json
wkperf analyze demo/baseline-1.json
```

A comparison looks like this:

```
35 improved · 1 regressed · 3 noisy · 25 unchanged
Overall: IMPROVED

FRAMES
  Metric                Baseline  Candidate          Δ      Δ%
  Average FPS           56.1 fps   71.6 fps  +15.5 fps  +27.7%  ▲ improved
  Frame time p95        24.09 ms   17.31 ms   -6.78 ms  -28.1%  ▼ improved
  Jank ratio (>16.7ms)     61.9%       8.3%     -53.6%  -86.6%  ▼ improved
```

Add `--format html --out report.html --open` for the shareable version with charts.

## The real workflow

### 1. Record

In Web Inspector, open the **Timelines** tab, make sure **JavaScript & Events**, **Layout & Rendering**, **CPU** and **Memory** are enabled, record the scenario, stop, and use the **Export** button to save the `.json`.

A few habits make results trustworthy:

- **Do exactly the same thing every time**: same steps, same data, about the same length.
- **Record 5 runs per side** and put them in `baseline/` and `candidate/`. `wkperf` compares medians and calls out changes that are within normal run-to-run wobble.
- Use a warm app (do a throwaway run first) unless startup is what you're measuring.

### 2. Find out what's slow

```bash
wkperf analyze baseline/run-1.json
```

You get:

- **Where the time goes**: script, style, layout, paint, GC, as a share of the recording.
- **Hot functions and modules**: what the sampling profiler spent time in.
- **Slow frames**: each one broken down into script / layout / paint / GC, with the likely culprit.
- **Findings**: plain-English problems with evidence and a suggested fix, e.g. layout thrashing, an un-throttled `mousemove` handler, steady memory growth, or one library eating a quarter of your CPU time.

### 3. Get help fixing it

```bash
export ANTHROPIC_API_KEY=...
wkperf analyze baseline/run-1.json --ai
```

Or skip the API and take the prompt with you (no network, no key):

```bash
wkperf analyze baseline/run-1.json --emit-prompt=prompt.md
```

The prompt tells the model to stick to the measured data, rank fixes by impact and effort, and say which metric should move to prove each fix worked. Local models work too (Ollama, LM Studio) through `--ai-provider openai --ai-base-url ...`. API keys are read from the environment only.

### 4. Check that it worked

Make the change, record again, then:

```bash
wkperf compare baseline/ --vs candidate/
```

### 5. Keep it from regressing (optional)

```bash
wkperf compare baseline/ --vs candidate/ --fail-on-regression --strict-fixture
```

Exit code `2` means a real regression, `3` means the two captures weren't comparable enough to trust. Details in the [reference](docs/REFERENCE.md#exit-codes).

## Audit one build across many scenes

`compare` answers "did my change help?". `audit` answers "how healthy is this build overall, and what should I fix first?". Record several scenes of the *same* build (home, a big table, a log view, settings...), one folder per scene:

```
scenes/
  home/           run-1.json  run-2.json  run-3.json
  table-scroll/   run-1.json  run-2.json  run-3.json
  log-stream/     run-1.json  run-2.json
  settings.json                      # a loose file is its own scene
```

```bash
wkperf sample --out /tmp/demo          # includes a demo scenes/ tree
wkperf audit /tmp/demo/scenes
wkperf audit scenes/ --format html --out audit.html --open
wkperf audit scenes/ --min-score 70    # exit code 4 below 70: gate CI on the whole app
wkperf audit captures/ --group-by-prefix   # home-1.json, home-2.json -> scene "home"
```

Each subdirectory is a scene (its files are repeat runs, medians are used); loose files are one scene each. You get a scene leaderboard, an overall grade, where the time goes, a cross-scene **Fix first** list (ranked by how many scenes a problem hits times how much it costs), hot functions, and the worst scene per standard. `--ai` / `--emit-prompt` work as in `analyze`.

```
4 scenes · 12 runs · 120.0s recorded
Overall (typical scene): grade B · score 86/100

Scene leaderboard (worst first)
  scene                    grade score    frame p95      jank        TBT  top finding
  table-scroll             D        49      53.5 ms     98.1%     0.0 ms  One module dominates CPU time: https://…
  settings                 C        72      17.1 ms      8.0%  1317.4 ms  One module dominates CPU time: https://…
  log-stream               C        74      21.8 ms     54.1%     0.0 ms  One module dominates CPU time: https://…
  home                     A        97      17.4 ms      9.0%     0.0 ms  One module dominates CPU time: https://…

Fix first
 3.  HIGH  Layout thrashing (forced synchronous layout)
    forced-layout · layout · 4 scenes · 8304.1ms total · 20.9% avg impact · rank 215.9
    • table-scroll: 241 forced-layout records, 4888.9ms total
    → Fix: Batch DOM reads and writes: read all layout properties first, then apply all writes...
```

## How far from a performant UI?

`analyze` and `audit` rate every recording against a small set of published targets and show the distance to "good" (for example "jank ratio is 12.4x the 5% target"). Scores run 0 to 100, graded A (90+), B (75+), C (60+), D (40+), F. `analyze --no-scorecard` hides it for single recordings.

An honest note: this is **not Lighthouse and not Core Web Vitals scoring**. Those tools measure page loads in a browser; wkperf reads Web Inspector timelines of a long-lived app.

- **Borrowed from Lighthouse / Core Web Vitals**: Total Blocking Time (summed over the whole recording and normalized per 10 s, not FCP-to-TTI), an INP *approximation* (handler time only, no input or presentation delay, so real INP is higher), and FCP/LCP only when the recording actually contains those records.
- **Not used**: CLS and any page-load scoring.
- **wkperf-specific**: frame budget (p95, FPS), jank ratio, GC share, memory growth, forced layouts per second, long tasks per minute, main-thread busy share. Their thresholds are our choices for a desktop webview app, not a standard.

| Standard | Source | ID | Good | Poor | Weight |
| --- | --- | --- | --- | --- | --- |
| Frame time p95 | frame budget (60Hz) | `frame-p95` | ≤ 16.7 ms | > 33.4 ms | 3 |
| Jank ratio | frame budget (60Hz) | `jank-ratio` | ≤ 5% | > 25% | 3 |
| Average FPS | frame budget (60Hz) | `fps` | ≥ 55 | < 30 | 3 |
| Total Blocking Time | Lighthouse | `tbt` | ≤ 200 ms per 10 s | > 600 ms per 10 s | 2 |
| Interaction latency (INP approx.) | Core Web Vitals | `inp` | ≤ 200 ms | > 500 ms | 2 |
| Long tasks per minute | wkperf heuristic | `long-task-rate` | ≤ 3 | > 12 | 2 |
| First Contentful Paint | Lighthouse | `fcp` | ≤ 1800 ms | > 3000 ms | 1 |
| Largest Contentful Paint | Core Web Vitals | `lcp` | ≤ 2500 ms | > 4000 ms | 1 |
| Main thread script busy | wkperf heuristic | `main-busy` | ≤ 25% | > 50% | 1 |
| GC share of wall time | wkperf heuristic | `gc-share` | ≤ 3% | > 10% | 1 |
| Memory growth | wkperf heuristic | `memory-growth` | ≤ 1 MB/min | > 10 MB/min | 1 |
| Forced layouts per second | wkperf heuristic | `forced-layout-rate` | ≤ 0.5 /s | > 5 /s | 1 |

Treat scores as a compass, not a certificate.

## "My two recordings aren't the same length / scenario"

That's common, and `wkperf` deals with it instead of silently lying:

- If lengths differ by more than 10%, totals and counts are compared **per second**, and labeled that way.
- `--trim-start 2 --trim-end 1` drops warm-up and tail; `--from` / `--to` compares the same slice of each recording.
- Every comparison gets a **comparability score** (0 to 1) and warnings: different duration, different frame rate, different URLs, different hot code paths ("you may be measuring a different workload"), too few samples, or noisy runs.

Normalization is a safety net. The best fix is still recording the same scenario.

## What it will and won't tell you

- Findings are **heuristics**, not proof. Treat them as leads and confirm with a second capture.
- A single run has no noise estimate, so `wkperf` flags those comparisons as low confidence. Use several runs.
- AI answers are only as good as the data in the prompt. Check the suggestions before shipping them.

## More

Full flag tables, the findings catalogue, config file, metric glossary and CI examples are in [docs/REFERENCE.md](docs/REFERENCE.md). Design notes are in [PLAN.md](PLAN.md).
