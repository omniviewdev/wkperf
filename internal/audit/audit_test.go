package audit

import (
	"bytes"
	"math"
	"strings"
	"testing"

	"github.com/omniviewdev/wkperf/internal/insights"
	"github.com/omniviewdev/wkperf/internal/metrics"
	"github.com/omniviewdev/wkperf/internal/scorecard"
)

// card builds a Scorecard literal exposing exactly the given values.
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

func summary(p95 float64) *metrics.Summary {
	return &metrics.Summary{Metrics: []metrics.Metric{{Key: "frames.p95_ms", Value: p95}}}
}

func near(a, b float64) bool { return math.Abs(a-b) < 1e-6 }

func fixture() []Scene {
	list := func(p95 float64, fps float64, finding insights.Finding) Run {
		return Run{
			Source: "list.json",
			Card:   card(map[string]float64{"frame-p95": p95, "fps": fps}),
			Report: &insights.Report{
				Source:      "list.json",
				DurationSec: 10,
				Breakdown:   []insights.Slice{{Name: "Script", Ms: 5000, Pct: 50}, {Name: "Idle", Ms: 5000, Pct: 50}},
				Summary:     summary(p95),
				Findings:    []insights.Finding{finding},
				Hotspots:    []metrics.FuncStat{{Name: "f", URL: "a.js", Line: 1, SelfMs: 50}},
				Modules:     []insights.ModuleStat{{Name: "react", SelfMs: 60}},
			},
		}
	}
	fl := insights.Finding{ID: "forced-layout", Title: "Forced layout", Severity: insights.SevMedium, Category: "layout", ImpactMs: 100, ImpactPct: 10,
		Evidence: []string{"list evidence"}, Suggestion: "list suggestion"}
	other := insights.Finding{ID: "other-run", Title: "Not the representative run", Severity: insights.SevHigh, ImpactMs: 999, ImpactPct: 99}
	return []Scene{
		{Name: "list", Runs: []Run{
			list(20, 50, other), // not representative: p95 20 vs median 30
			list(30, 40, fl),    // representative
			list(100, 10, other),
		}},
		{Name: "settings", Runs: []Run{{
			Source: "settings.json",
			Card:   card(map[string]float64{"frame-p95": 10, "fps": 60}),
			Report: &insights.Report{
				DurationSec: 30,
				Breakdown:   []insights.Slice{{Name: "Script", Ms: 3000, Pct: 10}, {Name: "Idle", Ms: 21000, Pct: 70}},
				Summary:     summary(10),
				Findings:    []insights.Finding{{ID: "gc", Title: "GC pressure", Severity: insights.SevLow, Category: "gc", ImpactMs: 20, ImpactPct: 2, Suggestion: "gc suggestion"}},
				Hotspots:    []metrics.FuncStat{{Name: "g", URL: "b.js", Line: 2, SelfMs: 40}},
				Modules:     []insights.ModuleStat{{Name: "lodash", SelfMs: 30}},
			},
		}}},
		{Name: "editor", Runs: []Run{{
			Source: "editor.json",
			Card:   card(map[string]float64{"frame-p95": 80, "fps": 12}),
			Report: &insights.Report{
				DurationSec: 10,
				Breakdown:   []insights.Slice{{Name: "Script", Ms: 4000, Pct: 40}, {Name: "Idle", Ms: 6000, Pct: 60}},
				Summary:     summary(80),
				Findings: []insights.Finding{
					{ID: "forced-layout", Title: "Forced layout", Severity: insights.SevHigh, Category: "layout", ImpactMs: 200, ImpactPct: 20,
						Evidence: []string{"editor evidence"}, Suggestion: "editor suggestion"},
					{ID: "info-thing", Title: "Small note", Severity: insights.SevInfo, ImpactMs: 5, ImpactPct: 0.5},
				},
				Hotspots: []metrics.FuncStat{{Name: "f", URL: "a.js", Line: 1, SelfMs: 30}, {Name: "h", URL: "c.js", Line: 3, SelfMs: 10}},
				Modules:  []insights.ModuleStat{{Name: "react", SelfMs: 20}},
			},
		}}},
	}
}

func TestBuildSceneAggregation(t *testing.T) {
	r := Build(fixture(), Options{})
	if len(r.Scenes) != 3 {
		t.Fatalf("scenes = %d", len(r.Scenes))
	}
	var list *SceneResult
	for i := range r.Scenes {
		if r.Scenes[i].Name == "list" {
			list = &r.Scenes[i]
		}
	}
	if list == nil {
		t.Fatal("no list scene")
	}
	if list.Runs != 3 || len(list.Sources) != 3 {
		t.Fatalf("runs/sources = %d/%d", list.Runs, len(list.Sources))
	}
	if v, _ := CardValue(list.Card, "frame-p95"); v != 30 {
		t.Errorf("median frame-p95 = %v, want 30", v)
	}
	if v, _ := CardValue(list.Card, "fps"); v != 40 {
		t.Errorf("median fps = %v, want 40", v)
	}
	if _, ok := CardValue(list.Card, "tbt"); ok {
		t.Error("tbt should be unavailable")
	}
	if list.DurationSec != 10 {
		t.Errorf("duration = %v", list.DurationSec)
	}
	if list.Rep == nil || list.Rep.Summary.Metrics[0].Value != 30 {
		t.Errorf("rep should be the run with p95 closest to median (30)")
	}
	if list.TopFinding != "Forced layout" {
		t.Errorf("top finding = %q", list.TopFinding)
	}
	if len(list.Breakdown) != 2 || list.Breakdown[0].Name != "Script" || list.Breakdown[0].Pct != 50 {
		t.Errorf("breakdown = %+v", list.Breakdown)
	}
}

func TestBuildScenesWorstFirst(t *testing.T) {
	r := Build(fixture(), Options{})
	got := []string{r.Scenes[0].Name, r.Scenes[1].Name, r.Scenes[2].Name}
	want := []string{"editor", "list", "settings"}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("order = %v, want %v", got, want)
		}
	}
	for i := 1; i < len(r.Scenes); i++ {
		if r.Scenes[i-1].Card.Score > r.Scenes[i].Card.Score {
			t.Errorf("scores not ascending: %v then %v", r.Scenes[i-1].Card.Score, r.Scenes[i].Card.Score)
		}
	}
}

func TestBuildOverallAndWorst(t *testing.T) {
	r := Build(fixture(), Options{})
	// medians across scenes: frame-p95 {30,10,80} -> 30; fps {40,60,12} -> 40
	if v, _ := CardValue(r.Overall, "frame-p95"); v != 30 {
		t.Errorf("overall frame-p95 = %v, want 30", v)
	}
	if v, _ := CardValue(r.Overall, "fps"); v != 40 {
		t.Errorf("overall fps = %v, want 40", v)
	}
	if r.WorstByStd["frame-p95"] != "editor" {
		t.Errorf("worst frame-p95 = %q, want editor (highest value)", r.WorstByStd["frame-p95"])
	}
	if r.WorstByStd["fps"] != "editor" {
		t.Errorf("worst fps = %q, want editor (lowest value, higher-better)", r.WorstByStd["fps"])
	}
	if _, ok := r.WorstByStd["tbt"]; ok {
		t.Error("tbt has no data; should have no worst scene")
	}

	// HigherBetter must flip direction: make "list" the slowest by fps only.
	scenes := fixture()
	scenes[0].Runs = []Run{{Card: card(map[string]float64{"frame-p95": 15, "fps": 5}), Report: scenes[0].Runs[1].Report}}
	r = Build(scenes, Options{})
	if r.WorstByStd["fps"] != "list" || r.WorstByStd["frame-p95"] != "editor" {
		t.Errorf("worst = %v", r.WorstByStd)
	}
}

func TestBuildBreakdownDurationWeighted(t *testing.T) {
	r := Build(fixture(), Options{})
	// weights 10/50, 30/50, 10/50. Script = .2*50+.6*10+.2*40 = 24; Idle = .2*50+.6*70+.2*60 = 64; sum 88 -> normalized.
	got := map[string]float64{}
	sum := 0.0
	for _, s := range r.Breakdown {
		got[s.Name] = s.Pct
		sum += s.Pct
	}
	if !near(sum, 100) {
		t.Errorf("breakdown sums to %v", sum)
	}
	if !near(got["Script"], 24.0/88*100) || !near(got["Idle"], 64.0/88*100) {
		t.Errorf("breakdown = %v", got)
	}
}

func TestBuildPriorities(t *testing.T) {
	r := Build(fixture(), Options{})
	if len(r.Priorities) != 3 {
		t.Fatalf("priorities = %d: %+v", len(r.Priorities), r.Priorities)
	}
	p := r.Priorities[0]
	if p.ID != "forced-layout" {
		t.Fatalf("first priority = %q", p.ID)
	}
	if p.Severity != insights.SevHigh {
		t.Errorf("severity = %q, want max (high)", p.Severity)
	}
	if !near(p.TotalImpactMs, 300) {
		t.Errorf("total impact = %v", p.TotalImpactMs)
	}
	if !near(p.AvgImpactPct, 10) { // (10+20+0)/3
		t.Errorf("avg impact pct = %v", p.AvgImpactPct)
	}
	if !near(p.Rank, 10*2+20*3) {
		t.Errorf("rank = %v, want 80", p.Rank)
	}
	if len(p.Scenes) != 2 {
		t.Errorf("scenes = %v", p.Scenes)
	}
	if len(p.Evidence) != 1 || p.Evidence[0] != "editor: editor evidence" || p.Suggestion != "editor suggestion" {
		t.Errorf("evidence/suggestion from highest-impact scene: %v / %q", p.Evidence, p.Suggestion)
	}
	if r.Priorities[1].ID != "gc" || !near(r.Priorities[1].Rank, 2) {
		t.Errorf("second = %+v", r.Priorities[1])
	}
	if r.Priorities[2].ID != "info-thing" || !near(r.Priorities[2].Rank, 0.125) {
		t.Errorf("third = %+v", r.Priorities[2])
	}
	for _, pr := range r.Priorities {
		if pr.ID == "other-run" {
			t.Error("finding from a non-representative run leaked in")
		}
	}

	capped := Build(fixture(), Options{Top: 1})
	if len(capped.Priorities) != 1 || capped.Priorities[0].ID != "forced-layout" {
		t.Errorf("capped priorities = %+v", capped.Priorities)
	}
}

func TestBuildHotspotsAndModules(t *testing.T) {
	r := Build(fixture(), Options{})
	if len(r.Hotspots) != 3 {
		t.Fatalf("hotspots = %+v", r.Hotspots)
	}
	f := r.Hotspots[0]
	if f.Name != "f" || f.SelfMs != 80 || f.SceneCount != 2 || len(f.Scenes) != 2 {
		t.Errorf("f = %+v", f)
	}
	if !near(f.Pct, 80.0/130*100) {
		t.Errorf("f pct = %v", f.Pct)
	}
	if r.Hotspots[1].Name != "g" || r.Hotspots[2].Name != "h" {
		t.Errorf("order = %v %v", r.Hotspots[1].Name, r.Hotspots[2].Name)
	}
	if len(r.Modules) != 2 || r.Modules[0].Name != "react" || r.Modules[0].SelfMs != 80 || !near(r.Modules[0].Pct, 80.0/110*100) {
		t.Errorf("modules = %+v", r.Modules)
	}
	if got := Build(fixture(), Options{Top: 2}); len(got.Hotspots) != 2 || len(got.Modules) != 2 {
		t.Errorf("top cap: %d hotspots %d modules", len(got.Hotspots), len(got.Modules))
	}
}

func TestBuildSingleAndEmpty(t *testing.T) {
	one := Build(fixture()[2:], Options{})
	if len(one.Scenes) != 1 || one.WorstByStd["frame-p95"] != "editor" {
		t.Fatalf("single scene: %+v", one.WorstByStd)
	}
	if v, _ := CardValue(one.Overall, "frame-p95"); v != 80 {
		t.Errorf("single overall = %v", v)
	}

	for _, in := range [][]Scene{nil, {{Name: "norun"}}, {{Name: "bare", Runs: []Run{{}}}}} {
		r := Build(in, Options{})
		if r.Overall == nil {
			t.Fatal("overall must not be nil")
		}
		var term, md bytes.Buffer
		if err := RenderTerm(&term, r, true); err != nil {
			t.Fatal(err)
		}
		if err := RenderMarkdown(&md, r); err != nil {
			t.Fatal(err)
		}
	}
	if r := Build([]Scene{{Name: "norun"}}, Options{}); len(r.Scenes) != 0 {
		t.Errorf("scene without runs should be dropped")
	}
}

func TestRenderTermAndMarkdown(t *testing.T) {
	r := Build(fixture(), Options{})

	var plain, colored bytes.Buffer
	if err := RenderTerm(&plain, r, false); err != nil {
		t.Fatal(err)
	}
	if err := RenderTerm(&colored, r, true); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(plain.String(), "\x1b[") {
		t.Error("color=false emitted ANSI")
	}
	if !strings.Contains(colored.String(), "\x1b[") {
		t.Error("color=true emitted no ANSI")
	}
	for _, want := range []string{
		"3 scenes", "5 runs", "Scene leaderboard", "Where the time goes", "Fix first", "Hot functions across scenes",
		"Hot modules", "Worst scene per standard", "Distance from a performant UI",
		"Forced layout", "editor suggestion", "react", "Frame time p95",
	} {
		if !strings.Contains(plain.String(), want) {
			t.Errorf("term output missing %q", want)
		}
	}
	if i, j := strings.Index(plain.String(), "editor "), strings.Index(plain.String(), "settings "); i < 0 || j < 0 || i > j {
		t.Error("leaderboard should list editor before settings")
	}

	var md bytes.Buffer
	if err := RenderMarkdown(&md, r); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"# wkperf audit", "## Scene leaderboard", "## Where the time goes", "## Fix first", "## Hot functions across scenes",
		"## Hot modules", "## Worst scene per standard", "## Distance from a performant UI",
		"| editor |", "Scenes affected (2): editor, list", "80.0 ms",
	} {
		if !strings.Contains(md.String(), want) {
			t.Errorf("markdown missing %q\n%s", want, md.String())
		}
	}
}

func TestMedianEvenCount(t *testing.T) {
	if got := median([]float64{4, 1, 3, 2}); got != 2.5 {
		t.Errorf("median = %v", got)
	}
	if got := median(nil); got != 0 {
		t.Errorf("median(nil) = %v", got)
	}
}
