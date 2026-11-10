package audithtml

import (
	"bytes"
	"strings"
	"testing"

	"github.com/omniviewdev/wkperf/internal/audit"
	"github.com/omniviewdev/wkperf/internal/insights"
	"github.com/omniviewdev/wkperf/internal/metrics"
	"github.com/omniviewdev/wkperf/internal/scorecard"
)

func card(vals map[string]float64) *scorecard.Scorecard {
	c := &scorecard.Scorecard{}
	for _, std := range scorecard.Standards() {
		r := scorecard.Result{Standard: std, Rating: scorecard.Unavailable}
		if v, ok := vals[std.ID]; ok {
			r.Value, r.Rating = v, scorecard.Good
		}
		c.Results = append(c.Results, r)
	}
	return c
}

func report() *audit.Report {
	run := func(src string, p95 float64, title string) audit.Run {
		return audit.Run{
			Source: src,
			Card:   card(map[string]float64{"frame-p95": p95, "fps": 20}),
			Report: &insights.Report{
				Source:      src,
				DurationSec: 12,
				Breakdown:   []insights.Slice{{Name: "Script", Ms: 6000, Pct: 50}, {Name: "Idle", Ms: 6000, Pct: 50}},
				Summary:     &metrics.Summary{Metrics: []metrics.Metric{{Key: "frames.p95_ms", Value: p95}}},
				Findings: []insights.Finding{{
					ID: "long-tasks", Title: title, Severity: insights.SevHigh, Category: "script",
					ImpactMs: 400, ImpactPct: 12, Evidence: []string{"<b>ev</b>"}, Suggestion: "split <work>",
				}},
				Hotspots: []metrics.FuncStat{{Name: "render<T>", URL: "app.js", Line: 7, SelfMs: 90}},
				Modules:  []insights.ModuleStat{{Name: "@scope/pkg", SelfMs: 90}},
			},
		}
	}
	return audit.Build([]audit.Scene{
		{Name: `<script>alert(1)</script>`, Runs: []audit.Run{run("a.json", 70, "Long <i>tasks</i>")}},
		{Name: "calm", Runs: []audit.Run{run("b.json", 10, "Long tasks")}},
	}, audit.Options{})
}

func TestRenderContentAndEscaping(t *testing.T) {
	var buf bytes.Buffer
	if err := Render(&buf, report(), ""); err != nil {
		t.Fatal(err)
	}
	out := buf.String()
	for _, want := range []string{
		"<!DOCTYPE html>", "prefers-color-scheme:dark", "<svg", "Scene leaderboard", "Where the time goes", "Fix first",
		"Hot functions across scenes", "Hot modules", "Worst scene per standard", "Distance from a performant UI",
		"calm", "class=\"grade g", "@scope/pkg", "render&lt;T&gt;", "split &lt;work&gt;", "&lt;b&gt;ev&lt;/b&gt;", "Long &lt;i&gt;tasks&lt;/i&gt;",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("output missing %q", want)
		}
	}
	for _, bad := range []string{"<script>alert", "<b>ev</b>", "<i>tasks</i>", "render<T>"} {
		if strings.Contains(out, bad) {
			t.Errorf("output contains unescaped %q", bad)
		}
	}
	if strings.Contains(out, "AI analysis") {
		t.Error("AI section rendered with empty markdown")
	}
	if strings.Contains(out, "<script") {
		t.Error("page must contain no script elements")
	}
}

func TestRenderAIBlockEscaped(t *testing.T) {
	var buf bytes.Buffer
	if err := Render(&buf, report(), "## Plan\n<img src=x onerror=alert(1)> & more"); err != nil {
		t.Fatal(err)
	}
	out := buf.String()
	if !strings.Contains(out, "AI analysis") || !strings.Contains(out, `<pre class="ai">`) {
		t.Fatal("AI block missing")
	}
	if !strings.Contains(out, "&lt;img src=x onerror=alert(1)&gt; &amp; more") {
		t.Error("AI markdown not escaped verbatim")
	}
	if strings.Contains(out, "<img") {
		t.Error("raw <img> leaked")
	}
}

func TestRenderEmptyAndNil(t *testing.T) {
	for _, r := range []*audit.Report{nil, audit.Build(nil, audit.Options{})} {
		var buf bytes.Buffer
		if err := Render(&buf, r, ""); err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(buf.String(), "No scenes audited") {
			t.Error("empty report should say no scenes")
		}
	}
}
