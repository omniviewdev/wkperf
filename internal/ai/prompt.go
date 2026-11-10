// Package ai builds LLM prompts from wkperf analysis/comparison data and runs
// them against a model provider (Anthropic, or any OpenAI-compatible API).
package ai

import (
	"fmt"
	"math"
	"sort"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/omniviewdev/wkperf/internal/compare"
	"github.com/omniviewdev/wkperf/internal/insights"
)

const (
	// MaxPromptTokens is the estimated budget for System+User.
	MaxPromptTokens = 12000
	charsPerToken   = 4

	defaultTopN        = 10
	defaultMaxFindings = 10
	capTopN            = 50
	capMaxFindings     = 30

	maxNotesChars    = 2000
	maxEvidenceChars = 300
	maxSuggestChars  = 400
	maxLocChars      = 90
)

// Context describes the application under test so the model can tailor advice.
type Context struct {
	AppName     string // e.g. "Omniview"
	Platform    string // e.g. "a Wails/WKWebView desktop app"
	Framework   string // e.g. "React"
	Notes       string // free-form extra context
	MaxFindings int    // findings rendered per report (default 10)
	TopN        int    // list length for hotspots/functions/metrics (default 10)
}

// Prompt is a system + user message pair.
type Prompt struct {
	System, User string
}

// Markdown renders the prompt as a single pasteable document.
func (p Prompt) Markdown() string {
	var b strings.Builder
	b.WriteString("# Instructions (system prompt)\n\n")
	b.WriteString(strings.TrimSpace(p.System))
	b.WriteString("\n\n---\n\n# Data (user prompt)\n\n")
	b.WriteString(strings.TrimSpace(p.User))
	b.WriteString("\n")
	return b.String()
}

// EstimateTokens estimates the token count at 4 characters per token.
func EstimateTokens(s string) int {
	n := utf8.RuneCountInString(s)
	return (n + charsPerToken - 1) / charsPerToken
}

const systemPrompt = `You are a senior WebKit/WKWebView and React performance engineer. You are given measurements from the WebKit Web Inspector timeline (wkperf output) and must explain where time goes and how to make the application faster.

Rules:
- Base every conclusion ONLY on the data provided. Do not invent numbers, functions, files or metrics that are not in the data.
- Cite the evidence for each claim: metric keys (e.g. frames.p95_ms), function names with their location, finding IDs, and the numbers.
- Clearly distinguish measured facts from hypotheses. Label hypotheses as "Hypothesis" and say what data would confirm them.
- Rank recommendations by expected impact and by effort (e.g. high impact / low effort first).
- Give concrete, code-level changes rather than generic advice. Consider: React memoization (React.memo, useMemo, useCallback, stable props), list virtualization, avoiding layout thrash (batching DOM reads before writes, no forced synchronous layout), throttling/debouncing handlers, requestAnimationFrame batching, reducing allocations and garbage-collection pressure, moving work to Web Workers, CSS containment (contain, content-visibility), and lazy loading / code splitting.
- If comparability warnings are listed, or results are marked noisy, low-confidence or single-run, say so explicitly and do not over-claim. A metric that moved within noise is not a finding.
- For each recommendation, say how to verify it with wkperf: which metric key (or function) should move, in which direction, and roughly by how much; capture again with the same scenario and compare.
- If the data was truncated, say which conclusions may be affected.

Output Markdown with exactly these top-level sections, in this order:
## Summary
## Top Issues (ranked)
## Recommendations
## Verification Plan
## Caveats

Be concise and specific. No preamble before the first section.`

// BuildAnalyzePrompt builds a prompt asking for an explanation of where time
// goes in one or more profiled recordings, and how to optimize.
func BuildAnalyzePrompt(reps []*insights.Report, c Context) Prompt {
	c = normalizeContext(c)
	var secs []section
	secs = append(secs, headerSection(c, "Task: analyze the performance profile below. Explain where time goes, identify the biggest problems, and recommend optimizations.\n"))

	var recs []*insights.Report
	for _, r := range reps {
		if r != nil {
			recs = append(recs, r)
		}
	}
	multi := len(recs) > 1
	for i, r := range recs {
		label := "Recording"
		if multi {
			label = fmt.Sprintf("Recording %d", i+1)
		}
		secs = append(secs, reportSections(label, r, c, i, 0)...)
	}
	if len(recs) == 0 {
		secs = append(secs, section{title: "no-data", value: math.MaxInt, body: "No recordings were provided.\n"})
	}
	return Prompt{System: systemPrompt, User: assemble(secs, userBudget())}
}

// BuildComparePrompt builds a prompt asking for an explanation of what
// changed between a baseline and a candidate. base and cand profile reports
// are optional (either may be nil).
func BuildComparePrompt(res *compare.Result, base, cand *insights.Report, c Context) Prompt {
	c = normalizeContext(c)
	var secs []section
	secs = append(secs, headerSection(c, "Task: compare the baseline and candidate recordings below. Explain what regressed and what improved, why (using the profile data), and what to change.\n"))

	if res != nil {
		secs = append(secs, compareSections(res, c)...)
		secs = append(secs, compareStackSections(res, base, cand, c)...)
	}
	// Candidate before baseline: baseline detail is dropped first.
	if cand != nil {
		secs = append(secs, reportSections("Candidate profile", cand, c, 0, 15)...)
	}
	if base != nil {
		secs = append(secs, reportSections("Baseline profile", base, c, 1, 15)...)
	}
	if res == nil && base == nil && cand == nil {
		secs = append(secs, section{title: "no-data", value: math.MaxInt, body: "No comparison data was provided.\n"})
	}
	return Prompt{System: systemPrompt, User: assemble(secs, userBudget())}
}

func normalizeContext(c Context) Context {
	if c.TopN <= 0 {
		c.TopN = defaultTopN
	}
	if c.TopN > capTopN {
		c.TopN = capTopN
	}
	if c.MaxFindings <= 0 {
		c.MaxFindings = defaultMaxFindings
	}
	if c.MaxFindings > capMaxFindings {
		c.MaxFindings = capMaxFindings
	}
	return c
}

func userBudget() int {
	return MaxPromptTokens*charsPerToken - utf8.RuneCountInString(systemPrompt) - 1
}

// ---- section assembly & truncation ----

type section struct {
	title string
	value int // higher = more valuable; lowest value is dropped first
	body  string
}

const truncNoteReserve = 400

// assemble joins sections in order. While over budget it drops the lowest
// value section (ties: the later one) and appends a truncation note.
func assemble(secs []section, budget int) string {
	size := func(list []section, extra int) int {
		n := extra
		for _, s := range list {
			n += utf8.RuneCountInString(s.body) + 1
		}
		return n
	}
	keep := make([]bool, len(secs))
	for i := range keep {
		keep[i] = true
	}
	var dropped []string
	current := func() []section {
		var out []section
		for i, s := range secs {
			if keep[i] {
				out = append(out, s)
			}
		}
		return out
	}
	for {
		extra := 0
		if len(dropped) > 0 {
			extra = truncNoteReserve
		}
		if size(current(), extra) <= budget {
			break
		}
		victim := -1
		for i, s := range secs {
			if !keep[i] || s.value == math.MaxInt {
				continue
			}
			if victim == -1 || s.value <= secs[victim].value {
				victim = i
			}
		}
		if victim == -1 {
			break
		}
		keep[victim] = false
		dropped = append(dropped, secs[victim].title)
	}

	var b strings.Builder
	for _, s := range current() {
		b.WriteString(s.body)
		b.WriteByte('\n')
	}
	if len(dropped) > 0 {
		sort.Strings(dropped)
		note := "> NOTE: input truncated to fit the size budget. Omitted sections: " + strings.Join(dropped, "; ") + ". Conclusions about omitted areas are not supported by the data.\n"
		if utf8.RuneCountInString(note) > truncNoteReserve {
			note = clip(note, truncNoteReserve-2) + "\n"
		}
		b.WriteString(note)
	}
	out := b.String()
	// Last resort for pathological input: critical sections alone too large.
	if r := []rune(out); len(r) > budget {
		out = string(r[:budget])
	}
	return out
}

func headerSection(c Context, task string) section {
	var b strings.Builder
	b.WriteString("## Context\n")
	if c.AppName != "" {
		fmt.Fprintf(&b, "- Application: %s\n", clip(oneLine(c.AppName), 200))
	}
	if c.Platform != "" {
		fmt.Fprintf(&b, "- Platform: %s\n", clip(oneLine(c.Platform), 200))
	}
	if c.Framework != "" {
		fmt.Fprintf(&b, "- Frontend framework: %s\n", clip(oneLine(c.Framework), 200))
	}
	if c.Notes != "" {
		fmt.Fprintf(&b, "- Notes: %s\n", clip(strings.TrimSpace(c.Notes), maxNotesChars))
	}
	b.WriteString("- Times are milliseconds unless stated. Frame budget at 60fps is 16.7ms.\n\n")
	b.WriteString(task)
	return section{title: "context", value: math.MaxInt, body: b.String()}
}

// ---- profile report rendering ----

// Report section base values (scaled by 10, minus index for multi-report
// ordering, minus offset when the report supports a compare).
const (
	vFindings   = 90
	vBreakdown  = 85
	vHotspots   = 70
	vSlowFrames = 60
	vLongTasks  = 55
	vModules    = 40
)

func reportSections(label string, r *insights.Report, c Context, idx, offset int) []section {
	r = insights.SelectEvidence(r)
	val := func(v int) int { return (v-offset)*10 - idx }
	head := fmt.Sprintf("## %s: %s", label, clip(oneLine(firstNonEmpty(r.Name, r.Source)), 120))
	if r.Source != "" && r.Name != "" {
		head += fmt.Sprintf(" (%s)", clip(oneLine(r.Source), 120))
	}
	head += fmt.Sprintf(" — %ss\n", num(r.DurationSec))

	var out []section
	tag := strings.ToLower(label)

	// The header outlives every section of its own report so surviving
	// sections are never orphaned.
	out = append(out, section{title: tag + " header", value: val(vFindings) + 1, body: head})
	out = append(out, rootCauseSections(tag, r.RootCause, c, val(vFindings)+2)...)
	out = append(out, stackAppendixSection(tag, r, val(vFindings)+1))

	if len(r.Breakdown) > 0 {
		bd := "### Wall-time breakdown\n"
		parts := make([]string, 0, len(r.Breakdown))
		for _, s := range r.Breakdown {
			parts = append(parts, fmt.Sprintf("%s %sms (%s%%)", s.Name, num(s.Ms), num(s.Pct)))
		}
		bd += strings.Join(parts, ", ") + "\n"
		out = append(out, section{title: tag + " breakdown", value: val(vBreakdown), body: bd})
	}

	if len(r.Findings) > 0 {
		var b strings.Builder
		b.WriteString("### Findings (heuristic, sorted by severity then impact)\n")
		n := min(len(r.Findings), c.MaxFindings)
		for i, f := range r.Findings[:n] {
			fmt.Fprintf(&b, "%d. [%s] %s — id=%s category=%s impact=%sms (%s%%)\n",
				i+1, f.Severity, clip(oneLine(f.Title), 160), f.ID, f.Category, num(f.ImpactMs), num(f.ImpactPct))
			for _, e := range f.Evidence {
				fmt.Fprintf(&b, "   - evidence: %s\n", clip(oneLine(e), maxEvidenceChars))
			}
			writeFindingDetails(&b, f)
			if f.Suggestion != "" {
				fmt.Fprintf(&b, "   - heuristic hint: %s\n", clip(oneLine(f.Suggestion), maxSuggestChars))
			}
		}
		if len(r.Findings) > n {
			fmt.Fprintf(&b, "(%d more findings omitted)\n", len(r.Findings)-n)
		}
		out = append(out, section{title: tag + " findings", value: val(vFindings), body: b.String()})
	}

	if len(r.Hotspots) > 0 {
		var b strings.Builder
		b.WriteString("### Top functions by self time\n")
		for i, h := range limitSlice(r.Hotspots, c.TopN) {
			fmt.Fprintf(&b, "%d. %s — self %sms, total %sms, %s\n",
				i+1, funcName(h.Name), num(h.SelfMs), num(h.TotalMs), functionLocation(h.Frame()))
			fmt.Fprintf(&b, "   self reproduce: %s\n   inclusive reproduce: %s\n", insights.FunctionCommand(r, h, "self_ms"), insights.FunctionCommand(r, h, "total_ms"))
			writeProfileFunction(&b, r, h)
		}
		out = append(out, section{title: tag + " hotspots", value: val(vHotspots), body: b.String()})
	}
	if len(r.InclusiveHotspots) > 0 {
		var b strings.Builder
		b.WriteString("### Top functions by inclusive sampled time (overlapping; do not sum)\n")
		for i, h := range limitSlice(r.InclusiveHotspots, c.TopN) {
			fmt.Fprintf(&b, "%d. %s — inclusive %sms, self %sms, %s\n", i+1, funcName(h.Name), num(h.TotalMs), num(h.SelfMs), functionLocation(h.Frame()))
			fmt.Fprintf(&b, "   inclusive reproduce: %s\n   self reproduce: %s\n", insights.FunctionCommand(r, h, "total_ms"), insights.FunctionCommand(r, h, "self_ms"))
			writeProfileFunction(&b, r, h)
			for _, edge := range limitSlice(h.Callers, 3) {
				fmt.Fprintf(&b, "   caller: %s @ %s (%sms, %d samples); weight reproduce: %s; samples reproduce: %s\n", funcName(edge.Function.Name), functionLocation(edge.Function), num(edge.WeightMs), edge.Samples, insights.EdgeCommand(r, h, edge, "callers", "weight_ms"), insights.EdgeCommand(r, h, edge, "callers", "samples"))
			}
			for _, edge := range limitSlice(h.Callees, 3) {
				fmt.Fprintf(&b, "   callee: %s @ %s (%sms, %d samples); weight reproduce: %s; samples reproduce: %s\n", funcName(edge.Function.Name), functionLocation(edge.Function), num(edge.WeightMs), edge.Samples, insights.EdgeCommand(r, h, edge, "callees", "weight_ms"), insights.EdgeCommand(r, h, edge, "callees", "samples"))
			}
		}
		out = append(out, section{title: tag + " inclusive hotspots", value: val(vHotspots), body: b.String()})
	}
	if len(r.Triggers) > 0 {
		var b strings.Builder
		b.WriteString("### Trigger timing and sampled code attribution (overlap does not establish causation)\n")
		for _, trigger := range limitSlice(r.Triggers, c.TopN) {
			fmt.Fprintf(&b, "- %s %s; %s\n", clip(oneLine(trigger.Kind), 60), clip(oneLine(trigger.Name), 100), clip(oneLine(trigger.SampleMessage), 200))
			for _, figure := range []struct {
				key   string
				value float64
				unit  string
			}{
				{"count", float64(trigger.Count), "count"}, {"total_ms", trigger.TotalMs, "ms"}, {"mean_ms", trigger.MeanMs, "ms"}, {"p95_ms", trigger.P95Ms, "ms"}, {"max_ms", trigger.MaxMs, "ms"},
			} {
				fmt.Fprintf(&b, "  %s=%s %s; reproduce: %s\n", figure.key, num(figure.value), figure.unit, insights.TriggerCommand(r, trigger, figure.key))
			}
			for _, h := range limitSlice(trigger.TopSelf, 3) {
				fmt.Fprintf(&b, "  self: %s @ %s %sms; reproduce: %s\n", funcName(h.Name), functionLocation(h.Frame()), num(h.SelfMs), insights.TriggerFunctionCommand(r, trigger, h, "self_ms"))
			}
			for _, h := range limitSlice(trigger.TopInclusive, 3) {
				fmt.Fprintf(&b, "  inclusive: %s @ %s %sms; reproduce: %s\n", funcName(h.Name), functionLocation(h.Frame()), num(h.TotalMs), insights.TriggerFunctionCommand(r, trigger, h, "total_ms"))
			}
		}
		fmt.Fprintf(&b, "Wakeups: %d callbacks (reproduce: %s), %s/s (reproduce: %s); sample coverage: %s\n", r.Wakeups.Count, insights.NumericCommand(r, "wakeups.count"), num(r.Wakeups.PerSecond), insights.NumericCommand(r, "wakeups.per_second"), clip(oneLine(r.SampleCoverage.Interpretation), 300))
		out = append(out, section{title: tag + " triggers", value: val(vHotspots) + 1, body: b.String()})
	}

	if len(r.Modules) > 0 {
		var b strings.Builder
		b.WriteString("### Self time by module\n")
		for i, m := range limitSlice(r.Modules, c.TopN) {
			fmt.Fprintf(&b, "%d. %s — %sms (%s%%)\n", i+1, clip(oneLine(m.Name), maxLocChars), num(m.SelfMs), num(m.Pct))
		}
		out = append(out, section{title: tag + " modules", value: val(vModules), body: b.String()})
	}

	if len(r.SlowFrames) > 0 {
		var b strings.Builder
		b.WriteString("### Slowest frames\n")
		for i, f := range limitSlice(r.SlowFrames, c.TopN) {
			fmt.Fprintf(&b, "%d. t=%ss %sms (script %s, layout+style %s, paint+composite %s, gc %s)",
				i+1, num(f.T), num(f.Ms), num(f.ScriptMs), num(f.LayoutMs), num(f.PaintMs), num(f.GCMs))
			if f.TopEvent != "" {
				fmt.Fprintf(&b, " event=%s", clip(oneLine(f.TopEvent), 80))
			}
			if f.TopFunc != "" {
				fmt.Fprintf(&b, " topFunc=%s", clip(oneLine(f.TopFunc), 80))
			}
			b.WriteByte('\n')
		}
		out = append(out, section{title: tag + " slow frames", value: val(vSlowFrames), body: b.String()})
	}

	if len(r.LongTasks) > 0 {
		var b strings.Builder
		b.WriteString("### Long tasks (>50ms)\n")
		for i, t := range limitSlice(r.LongTasks, c.TopN) {
			fmt.Fprintf(&b, "%d. t=%ss %sms %s", i+1, num(t.T), num(t.Ms), clip(oneLine(t.EventType), 60))
			if t.Detail != "" {
				fmt.Fprintf(&b, " detail=%s", clip(oneLine(t.Detail), 80))
			}
			if t.TopFunc != "" {
				fmt.Fprintf(&b, " topFunc=%s", clip(oneLine(t.TopFunc), 80))
			}
			b.WriteByte('\n')
		}
		out = append(out, section{title: tag + " long tasks", value: val(vLongTasks), body: b.String()})
	}
	return out
}

// ---- comparison rendering ----

func compareSections(res *compare.Result, c Context) []section {
	var out []section

	var h strings.Builder
	h.WriteString("## Comparison\n")
	fmt.Fprintf(&h, "- Baseline: %d run(s) %s\n", res.Baseline.Runs, joinSources(res.Baseline.Sources))
	fmt.Fprintf(&h, "- Candidate: %d run(s) %s\n", res.Cand.Runs, joinSources(res.Cand.Sources))
	fmt.Fprintf(&h, "- Headline: %d regressed, %d improved, %d unchanged, %d noisy\n",
		res.Counts.Regressed, res.Counts.Improved, res.Counts.Unchanged, res.Counts.Noisy)
	fmt.Fprintf(&h, "- Comparability score: %s (1 = identical fixture); extensive metrics normalized per second: %t\n",
		num(res.Comparability.Score), res.Comparability.Normalized)
	h.WriteString("- Metric line format: key (label): base → candidate unit (Δ%) [verdict, confidence]. Values are medians over runs.\n")
	out = append(out, section{title: "comparison header", value: math.MaxInt, body: h.String()})

	if len(res.Comparability.Warnings) > 0 {
		var b strings.Builder
		b.WriteString("### Comparability warnings (treat conclusions with care)\n")
		for _, w := range res.Comparability.Warnings {
			fmt.Fprintf(&b, "- [%s] %s: %s\n", w.Level, w.Code, clip(oneLine(w.Message), 300))
		}
		out = append(out, section{title: "comparability warnings", value: math.MaxInt, body: b.String()})
	}

	var regressed, improved, noisy, added, removed []compare.MetricDiff
	for _, m := range res.Metrics {
		switch m.Verdict {
		case compare.Regressed:
			regressed = append(regressed, m)
		case compare.Improved:
			improved = append(improved, m)
		case compare.Noisy:
			noisy = append(noisy, m)
		case compare.NewMetric:
			added = append(added, m)
		case compare.Removed:
			removed = append(removed, m)
		}
	}
	byMagnitude := func(l []compare.MetricDiff) {
		sort.SliceStable(l, func(i, j int) bool {
			ai, aj := math.Abs(l[i].Pct), math.Abs(l[j].Pct)
			if ai != aj {
				return ai > aj
			}
			return l[i].Metric.Key < l[j].Metric.Key
		})
	}
	for _, l := range [][]compare.MetricDiff{regressed, improved, noisy} {
		byMagnitude(l)
	}

	if len(regressed) > 0 {
		out = append(out, section{title: "regressed metrics", value: 950,
			body: metricList("### Regressed metrics (worst first)", regressed, c.TopN*2)})
	}
	if len(res.FuncRegressed) > 0 {
		out = append(out, section{title: "regressed functions", value: 800,
			body: funcList("### Functions with more self time (candidate vs baseline)", res.FuncRegressed, c.TopN)})
	}
	if len(improved) > 0 {
		out = append(out, section{title: "improved metrics", value: 650,
			body: metricList("### Improved metrics (best first)", improved, c.TopN)})
	}
	if len(res.FuncImproved) > 0 {
		out = append(out, section{title: "improved functions", value: 500,
			body: funcList("### Functions with less self time (candidate vs baseline)", res.FuncImproved, c.TopN)})
	}
	if len(noisy) > 0 {
		out = append(out, section{title: "noisy metrics", value: 450,
			body: metricList("### Noisy metrics (change within run-to-run variance; do not treat as real)", noisy, c.TopN)})
	}
	if len(added)+len(removed) > 0 {
		var b strings.Builder
		b.WriteString("### Metrics present on only one side\n")
		for _, m := range limitSlice(added, c.TopN) {
			fmt.Fprintf(&b, "- %s: only in candidate (%s %s)\n", m.Metric.Key, num(m.Cand), m.Metric.Unit)
		}
		for _, m := range limitSlice(removed, c.TopN) {
			fmt.Fprintf(&b, "- %s: only in baseline (%s %s)\n", m.Metric.Key, num(m.Base), m.Metric.Unit)
		}
		out = append(out, section{title: "one-sided metrics", value: 300, body: b.String()})
	}
	return out
}

func metricList(title string, l []compare.MetricDiff, n int) string {
	var b strings.Builder
	b.WriteString(title + "\n")
	for _, m := range limitSlice(l, n) {
		fmt.Fprintf(&b, "- %s (%s): %s → %s %s (%s) [%s, %s]\n",
			m.Metric.Key, clip(oneLine(m.Metric.Label), 60), num(m.Base), num(m.Cand), m.Metric.Unit,
			pctStr(m), m.Verdict, m.Confidence)
	}
	if len(l) > n && n > 0 {
		fmt.Fprintf(&b, "(%d more omitted)\n", len(l)-n)
	}
	return b.String()
}

func funcList(title string, l []compare.FuncDiff, n int) string {
	var b strings.Builder
	b.WriteString(title + "\n")
	for i, f := range limitSlice(l, n) {
		fmt.Fprintf(&b, "%d. %s — self %s → %sms (%sms, %s), total %s → %sms, %s\n",
			i+1, funcName(f.Name), num(f.BaseSelf), num(f.CandSelf), signed(f.Delta), pctFrac(f.Pct, f.BaseSelf),
			num(f.BaseTotal), num(f.CandTotal), diffLocation(f))
		if f.ID != "" {
			fmt.Fprintf(&b, "   id=%s\n", f.ID)
		}
		writeMetricStack(&b, f.CommonStack)
		if command := f.StackCommand(); command != "" {
			fmt.Fprintf(&b, "   full stack reproduce: %s\n", command)
		}
	}
	return b.String()
}

// ---- helpers ----

// num formats v with at most 2 decimals, trimming trailing zeros.
func num(v float64) string {
	if math.IsNaN(v) || math.IsInf(v, 0) {
		return "n/a"
	}
	s := strconv.FormatFloat(v, 'f', 2, 64)
	if strings.Contains(s, ".") {
		s = strings.TrimRight(s, "0")
		s = strings.TrimSuffix(s, ".")
	}
	if s == "-0" {
		s = "0"
	}
	return s
}

func signed(v float64) string {
	s := num(v)
	if v > 0 && !strings.HasPrefix(s, "+") {
		return "+" + s
	}
	return s
}

func pctFrac(p, base float64) string {
	if base == 0 {
		return "new/zero baseline"
	}
	return signed(math.Round(p*1000)/10) + "%"
}

func pctStr(m compare.MetricDiff) string {
	if m.Base == 0 {
		return "Δ " + signed(m.Delta)
	}
	return pctFrac(m.Pct, m.Base)
}

func funcName(n string) string {
	if strings.TrimSpace(n) == "" {
		return "(anonymous)"
	}
	return clip(oneLine(n), 80)
}

func joinSources(s []string) string {
	if len(s) == 0 {
		return ""
	}
	parts := make([]string, len(s))
	for i, x := range s {
		parts[i] = clip(oneLine(x), 100)
	}
	return "(" + strings.Join(parts, ", ") + ")"
}

func limitSlice[T any](l []T, n int) []T {
	if n > 0 && len(l) > n {
		return l[:n]
	}
	return l
}

func oneLine(s string) string {
	return strings.Join(strings.Fields(s), " ")
}

func clip(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	if n <= 1 {
		return string(r[:n])
	}
	return string(r[:n-1]) + "…"
}

func firstNonEmpty(a, b string) string {
	if a != "" {
		return a
	}
	return b
}
