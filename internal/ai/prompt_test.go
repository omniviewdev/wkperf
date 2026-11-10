package ai

import (
	"fmt"
	"strings"
	"testing"

	"github.com/omniviewdev/wkperf/internal/compare"
	"github.com/omniviewdev/wkperf/internal/insights"
	"github.com/omniviewdev/wkperf/internal/metrics"
)

func sampleReport(name string) *insights.Report {
	return &insights.Report{
		Source:      "/tmp/" + name + ".json",
		Name:        name,
		DurationSec: 12.5,
		Breakdown: []insights.Slice{
			{Name: "Script", Ms: 4000, Pct: 32},
			{Name: "Layout", Ms: 1500.5, Pct: 12},
			{Name: "Idle", Ms: 7000, Pct: 56},
		},
		Hotspots: []metrics.FuncStat{
			{Name: "renderList", URL: "https://app/src/List.tsx", Line: 42, SelfMs: 900, TotalMs: 1500},
			{Name: "", URL: "", SelfMs: 10, TotalMs: 10},
		},
		Modules: []insights.ModuleStat{{Name: "react-dom", SelfMs: 700, Pct: 20}},
		SlowFrames: []insights.SlowFrame{
			{T: 3.2, Ms: 88.4, ScriptMs: 60, LayoutMs: 20, TopEvent: "event-dispatched click", TopFunc: "onClick"},
		},
		LongTasks: []insights.LongTask{
			{T: 3.2, Ms: 75, EventType: "event-dispatched", Detail: "click", TopFunc: "onClick"},
		},
		Findings: []insights.Finding{{
			ID: "forced-layout", Title: "Forced synchronous layout", Severity: insights.SevHigh,
			Category: "layout", ImpactMs: 300, ImpactPct: 2.4,
			Evidence:   []string{"layout.forced_count=42", "renderList reads offsetHeight in a loop"},
			Suggestion: "Batch DOM reads before writes.",
		}},
	}
}

func sampleResult() *compare.Result {
	return &compare.Result{
		Baseline: compare.Side{Label: "baseline", Sources: []string{"a.json"}, Runs: 1},
		Cand:     compare.Side{Label: "candidate", Sources: []string{"b.json"}, Runs: 3},
		Metrics: []compare.MetricDiff{
			{Metric: metrics.Metric{Key: "frames.p95_ms", Label: "Frame p95", Unit: metrics.UnitMs},
				Base: 20, Cand: 30, Delta: 10, Pct: 0.5, Verdict: compare.Regressed, Confidence: "high"},
			{Metric: metrics.Metric{Key: "script.total_ms", Label: "Script total", Unit: metrics.UnitMs},
				Base: 100, Cand: 60, Delta: -40, Pct: -0.4, Verdict: compare.Improved, Confidence: "low"},
			{Metric: metrics.Metric{Key: "gc.count", Label: "GC count", Unit: metrics.UnitCount},
				Base: 10, Cand: 11, Delta: 1, Pct: 0.1, Verdict: compare.Noisy, Confidence: "low"},
		},
		FuncRegressed: []compare.FuncDiff{
			{Name: "renderRow", URL: "https://app/Row.tsx", Line: 7, BaseSelf: 10, CandSelf: 50, Delta: 40, Pct: 4},
		},
		FuncImproved: []compare.FuncDiff{
			{Name: "parseAll", URL: "https://app/parse.ts", Line: 3, BaseSelf: 80, CandSelf: 20, Delta: -60, Pct: -0.75},
		},
		Counts: compare.Counts{Regressed: 1, Improved: 1, Noisy: 1},
		Comparability: compare.Comparability{
			Score: 0.4, Normalized: true,
			Warnings: []compare.Warning{
				{Level: "warn", Code: "duration-mismatch", Message: "baseline 10.0s vs candidate 30.0s"},
			},
		},
	}
}

var testCtx = Context{AppName: "Omniview", Platform: "a Wails/WKWebView desktop app", Framework: "React", Notes: "cold start"}

func TestSystemPromptCoversRequirements(t *testing.T) {
	p := BuildAnalyzePrompt([]*insights.Report{sampleReport("r")}, testCtx)
	for _, want := range []string{
		"WebKit", "ONLY", "hypothes", "impact", "effort", "virtualization", "layout thrash",
		"requestAnimationFrame", "Web Workers", "CSS containment", "comparability", "wkperf",
		"## Summary", "## Top Issues (ranked)", "## Recommendations", "## Verification Plan", "## Caveats",
	} {
		if !strings.Contains(p.System, want) {
			t.Errorf("system prompt missing %q", want)
		}
	}
}

func TestAnalyzePromptContent(t *testing.T) {
	p := BuildAnalyzePrompt([]*insights.Report{sampleReport("r"), nil}, testCtx)
	for _, want := range []string{
		"Omniview", "WKWebView", "React", "cold start",
		"Script 4000ms (32%)", "Layout 1500.5ms (12%)",
		"renderList", "https://app/src/List.tsx:42", "(anonymous)", "sourceID::0:0",
		"react-dom", "t=3.2s 88.4ms", "event=event-dispatched click", "topFunc=onClick",
		"[high] Forced synchronous layout", "id=forced-layout", "layout.forced_count=42",
		"Batch DOM reads before writes.",
	} {
		if !strings.Contains(p.User, want) {
			t.Errorf("user prompt missing %q\n%s", want, p.User)
		}
	}
	if strings.Contains(p.User, "truncated") {
		t.Error("small prompt must not be marked truncated")
	}
	if strings.Contains(p.User, "Recording 1") {
		t.Error("single recording should not be numbered")
	}
}

func TestComparePromptContent(t *testing.T) {
	p := BuildComparePrompt(sampleResult(), sampleReport("base"), sampleReport("cand"), testCtx)
	for _, want := range []string{
		"duration-mismatch", "baseline 10.0s vs candidate 30.0s", "Comparability score: 0.4",
		"1 regressed, 1 improved, 0 unchanged, 1 noisy",
		"frames.p95_ms (Frame p95): 20 → 30 ms (+50%) [regressed, high]",
		"script.total_ms", "(-40%)", "gc.count", "Noisy metrics",
		"renderRow", "self 10 → 50ms (+40ms, +400%)",
		"parseAll", "Candidate profile", "Baseline profile",
	} {
		if !strings.Contains(p.User, want) {
			t.Errorf("compare prompt missing %q\n%s", want, p.User)
		}
	}
}

func TestComparePromptNilReports(t *testing.T) {
	p := BuildComparePrompt(sampleResult(), nil, nil, Context{})
	if !strings.Contains(p.User, "frames.p95_ms") || strings.Contains(p.User, "Baseline profile") {
		t.Errorf("unexpected user prompt:\n%s", p.User)
	}
	empty := BuildComparePrompt(nil, nil, nil, Context{})
	if !strings.Contains(empty.User, "No comparison data") {
		t.Errorf("empty compare = %q", empty.User)
	}
}

func TestDeterministic(t *testing.T) {
	a := BuildComparePrompt(sampleResult(), sampleReport("b"), sampleReport("c"), testCtx)
	b := BuildComparePrompt(sampleResult(), sampleReport("b"), sampleReport("c"), testCtx)
	if a != b || a.Markdown() != b.Markdown() {
		t.Error("compare prompt not deterministic")
	}
	x := BuildAnalyzePrompt([]*insights.Report{sampleReport("r")}, testCtx)
	y := BuildAnalyzePrompt([]*insights.Report{sampleReport("r")}, testCtx)
	if x != y {
		t.Error("analyze prompt not deterministic")
	}
}

func TestMetricOrderingByMagnitude(t *testing.T) {
	res := sampleResult()
	res.Metrics = nil
	for i, pct := range []float64{0.1, 0.9, 0.5} {
		res.Metrics = append(res.Metrics, compare.MetricDiff{
			Metric: metrics.Metric{Key: fmt.Sprintf("m.k%d", i), Unit: metrics.UnitMs},
			Base:   10, Cand: 10 * (1 + pct), Delta: 10 * pct, Pct: pct, Verdict: compare.Regressed,
		})
	}
	u := BuildComparePrompt(res, nil, nil, testCtx).User
	i1, i2, i0 := strings.Index(u, "m.k1"), strings.Index(u, "m.k2"), strings.Index(u, "m.k0")
	if !(i1 >= 0 && i1 < i2 && i2 < i0) {
		t.Errorf("regressed metrics not worst-first: k1=%d k2=%d k0=%d", i1, i2, i0)
	}
}

func bigReport(name string) *insights.Report {
	r := sampleReport(name)
	r.Findings = nil
	for i := 0; i < 30; i++ {
		r.Findings = append(r.Findings, insights.Finding{
			ID: fmt.Sprintf("%s-f%d", name, i), Title: "finding " + strings.Repeat("t", 100),
			Severity: insights.SevMedium, Category: "script", ImpactMs: float64(i),
			Evidence: []string{
				strings.Repeat("a", 290), strings.Repeat("b", 290), strings.Repeat("c", 290),
			},
			Suggestion: strings.Repeat("s", 390),
		})
	}
	return r
}

func TestComparePromptTruncationKeepsBudgetAndKeyData(t *testing.T) {
	res := sampleResult()
	for i := 0; i < 300; i++ {
		res.Metrics = append(res.Metrics, compare.MetricDiff{
			Metric: metrics.Metric{Key: fmt.Sprintf("script.evt%03d.total_ms", i), Label: "evt", Unit: metrics.UnitMs},
			Base:   10, Cand: 12, Delta: 2, Pct: 0.2, Verdict: compare.Regressed, Confidence: "high",
		})
	}
	p := BuildComparePrompt(res, bigReport("base"), bigReport("cand"), Context{TopN: 50, MaxFindings: 15})
	if tok := EstimateTokens(p.System + p.User); tok > MaxPromptTokens {
		t.Errorf("prompt is %d tokens, budget %d", tok, MaxPromptTokens)
	}
	if tok := EstimateTokens(p.Markdown()); tok > MaxPromptTokens+50 {
		t.Errorf("markdown is %d tokens", tok)
	}
	if !strings.Contains(p.User, "NOTE: input truncated") {
		t.Error("truncation not noted")
	}
	// Lowest-value (baseline profile detail) is dropped before the data that matters.
	if !strings.Contains(p.User, "baseline profile findings") {
		t.Error("omitted baseline section should be named in the truncation note")
	}
	for _, want := range []string{"frames.p95_ms", "script.evt000.total_ms", "duration-mismatch", "renderRow"} {
		if !strings.Contains(p.User, want) {
			t.Errorf("truncated prompt lost %q", want)
		}
	}
	if !strings.Contains(p.User, "Candidate profile") {
		t.Error("candidate profile should survive baseline's drop")
	}
}

func TestAnalyzeTruncationManyReports(t *testing.T) {
	var reps []*insights.Report
	for i := 0; i < 4; i++ {
		reps = append(reps, bigReport(fmt.Sprintf("r%d", i)))
	}
	p := BuildAnalyzePrompt(reps, Context{TopN: 50, MaxFindings: 30})
	if tok := EstimateTokens(p.System + p.User); tok > MaxPromptTokens {
		t.Errorf("prompt is %d tokens, budget %d", tok, MaxPromptTokens)
	}
	if !strings.Contains(p.User, "NOTE: input truncated") {
		t.Error("truncation not noted")
	}
	if !strings.Contains(p.User, "Recording 1") || !strings.Contains(p.User, "r0-f0") {
		t.Error("first recording's findings should be kept")
	}
	if strings.Contains(p.User, "r3-f0") {
		t.Error("last recording's findings should be dropped first")
	}
}

func TestMarkdownAndEstimateTokens(t *testing.T) {
	p := Prompt{System: "be brief", User: "data here"}
	md := p.Markdown()
	if !strings.Contains(md, "be brief") || !strings.Contains(md, "data here") ||
		strings.Index(md, "be brief") > strings.Index(md, "data here") {
		t.Errorf("markdown = %q", md)
	}
	if got := EstimateTokens(strings.Repeat("a", 400)); got != 100 {
		t.Errorf("EstimateTokens(400 chars) = %d", got)
	}
	if got := EstimateTokens(""); got != 0 {
		t.Errorf("EstimateTokens(\"\") = %d", got)
	}
	if got := EstimateTokens("abcde"); got != 2 {
		t.Errorf("EstimateTokens(5 chars) = %d", got)
	}
}

func TestContextCaps(t *testing.T) {
	r := sampleReport("r")
	r.Hotspots = nil
	for i := 0; i < 20; i++ {
		r.Hotspots = append(r.Hotspots, metrics.FuncStat{Name: fmt.Sprintf("fn%02d", i), SelfMs: float64(100 - i)})
	}
	u := BuildAnalyzePrompt([]*insights.Report{r}, Context{TopN: 3}).User
	if !strings.Contains(u, "fn02") || strings.Contains(u, "fn03") {
		t.Errorf("TopN=3 not honoured:\n%s", u)
	}
}
