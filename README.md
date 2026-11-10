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
