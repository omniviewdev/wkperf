package compare

import (
	"testing"

	"github.com/omniviewdev/wkperf/internal/metrics"
)

func sum(src string, ms ...metrics.Metric) *metrics.Summary {
	return &metrics.Summary{Source: src, Metrics: ms}
}

func met(key, group string, u metrics.Unit, d metrics.Direction, v float64) metrics.Metric {
	return metrics.Metric{Key: key, Group: group, Unit: u, Dir: d, Value: v}
}

func find(t *testing.T, r *Result, key string) MetricDiff {
	t.Helper()
	for _, m := range r.Metrics {
		if m.Metric.Key == key {
			return m
		}
	}
	t.Fatalf("metric %s not found", key)
	return MetricDiff{}
}

func TestMedianEven(t *testing.T) {
	if got := median([]float64{4, 1, 3, 2}); got != 2.5 {
		t.Fatalf("got %v", got)
	}
	if got := median([]float64{5, 1, 3}); got != 3 {
		t.Fatalf("got %v", got)
	}
	if got := median(nil); got != 0 {
		t.Fatalf("got %v", got)
	}
}

func TestVerdictDirections(t *testing.T) {
	b := []*metrics.Summary{sum("b",
		met("frames.fps", "frames", metrics.UnitFPS, metrics.HigherBetter, 50),
		met("frames.p95_ms", "frames", metrics.UnitMs, metrics.LowerBetter, 20),
		met("overview.dur", "overview", metrics.UnitMs, metrics.Neutral, 100),
	)}
	c := []*metrics.Summary{sum("c",
		met("frames.fps", "frames", metrics.UnitFPS, metrics.HigherBetter, 58),
		met("frames.p95_ms", "frames", metrics.UnitMs, metrics.LowerBetter, 30),
		met("overview.dur", "overview", metrics.UnitMs, metrics.Neutral, 500),
	)}
	r := Compare(b, c, DefaultOptions())
	if v := find(t, r, "frames.fps").Verdict; v != Improved {
		t.Errorf("fps up: %v", v)
	}
	if v := find(t, r, "frames.p95_ms").Verdict; v != Regressed {
		t.Errorf("p95 up: %v", v)
	}
	if v := find(t, r, "overview.dur").Verdict; v != Info {
		t.Errorf("neutral: %v", v)
	}
	if find(t, r, "frames.fps").Confidence != "single-run" {
		t.Errorf("confidence")
	}
	if r.Counts.Improved != 1 || r.Counts.Regressed != 1 || r.Counts.Unchanged != 0 {
		t.Errorf("counts %+v", r.Counts)
	}
	if r.Metrics[0].Metric.Group != "overview" {
		t.Errorf("ordering: %v", r.Metrics[0].Metric.Group)
	}
}

func TestThresholds(t *testing.T) {
	mk := func(v float64, u metrics.Unit) []*metrics.Summary {
		return []*metrics.Summary{sum("x", met("k", "script", u, metrics.LowerBetter, v))}
	}
	cases := []struct {
		name       string
		b, c       float64
		u          metrics.Unit
		want       Verdict
		wantPctNil bool
	}{
		{"below abs floor", 0.1, 0.4, metrics.UnitMs, Unchanged, false},
		{"below rel", 100, 103, metrics.UnitMs, Unchanged, false},
		{"over both", 100, 110, metrics.UnitMs, Regressed, false},
		{"base zero", 0, 5, metrics.UnitMs, Regressed, true},
		{"base zero small", 0, 0.2, metrics.UnitMs, Unchanged, true},
		{"bytes floor", 1000000, 1000500, metrics.UnitBytes, Unchanged, false},
	}
	for _, tc := range cases {
		r := Compare(mk(tc.b, tc.u), mk(tc.c, tc.u), DefaultOptions())
		d := find(t, r, "k")
		if d.Verdict != tc.want {
			t.Errorf("%s: got %v", tc.name, d.Verdict)
		}
		if tc.wantPctNil && d.Pct != 0 {
			t.Errorf("%s: pct %v", tc.name, d.Pct)
		}
	}
}

func TestNoisyMultiRun(t *testing.T) {
	mk := func(vals ...float64) []*metrics.Summary {
		var out []*metrics.Summary
		for _, v := range vals {
			out = append(out, sum("r", met("k", "frames", metrics.UnitMs, metrics.LowerBetter, v)))
		}
		return out
	}
	// medians 100 vs 110, each inside the other's range -> noisy
	r := Compare(mk(80, 100, 130), mk(90, 110, 140), DefaultOptions())
	d := find(t, r, "k")
	if d.Verdict != Noisy || d.Confidence != "low" {
		t.Errorf("got %v/%v", d.Verdict, d.Confidence)
	}
	if r.Counts.Noisy != 1 {
		t.Errorf("counts %+v", r.Counts)
	}
	// disjoint -> regressed, high
	r = Compare(mk(100, 102), mk(150, 152), DefaultOptions())
	d = find(t, r, "k")
	if d.Verdict != Regressed || d.Confidence != "high" {
		t.Errorf("got %v/%v", d.Verdict, d.Confidence)
	}
	if d.Base != 101 || d.Cand != 151 {
		t.Errorf("medians %v %v", d.Base, d.Cand)
	}
}

func TestNewRemoved(t *testing.T) {
	b := []*metrics.Summary{sum("b", met("old", "gc", metrics.UnitMs, metrics.LowerBetter, 5))}
	c := []*metrics.Summary{sum("c", met("new", "gc", metrics.UnitMs, metrics.LowerBetter, 5))}
	r := Compare(b, c, DefaultOptions())
	if v := find(t, r, "old").Verdict; v != Removed {
		t.Errorf("old: %v", v)
	}
	if v := find(t, r, "new").Verdict; v != NewMetric {
		t.Errorf("new: %v", v)
	}
	if r.Counts != (Counts{}) {
		t.Errorf("counts %+v", r.Counts)
	}
}

func fs(name string, self, total float64) metrics.FuncStat {
	return metrics.FuncStat{Name: name, URL: "u.js", Line: 1, SelfMs: self, TotalMs: total}
}

func TestFunctionDiff(t *testing.T) {
	b := []*metrics.Summary{
		{Source: "b1", Functions: []metrics.FuncStat{fs("a", 10, 10), fs("b", 50, 60), fs("gone", 20, 20), fs("tiny", 1, 1)}},
		{Source: "b2", Functions: []metrics.FuncStat{fs("a", 10, 10), fs("b", 50, 60), fs("gone", 20, 20), fs("tiny", 1, 1)}},
	}
	c := []*metrics.Summary{
		{Source: "c1", Functions: []metrics.FuncStat{fs("a", 30, 30), fs("b", 20, 30), fs("fresh", 40, 40), fs("tiny", 1.2, 1.2)}},
		{Source: "c2", Functions: []metrics.FuncStat{fs("a", 30, 30), fs("b", 20, 30), fs("fresh", 40, 40), fs("tiny", 1.2, 1.2)}},
	}
	o := DefaultOptions()
	r := Compare(b, c, o)
	if len(r.FuncRegressed) != 2 || r.FuncRegressed[0].Name != "fresh" || r.FuncRegressed[1].Name != "a" {
		t.Fatalf("regressed: %+v", r.FuncRegressed)
	}
	if r.FuncRegressed[0].Verdict != NewMetric || r.FuncRegressed[1].Verdict != Regressed {
		t.Errorf("verdicts %+v", r.FuncRegressed)
	}
	if r.FuncRegressed[1].Delta != 20 || r.FuncRegressed[1].Pct != 2 {
		t.Errorf("a: %+v", r.FuncRegressed[1])
	}
	if len(r.FuncImproved) != 2 || r.FuncImproved[0].Name != "b" || r.FuncImproved[1].Name != "gone" {
		t.Fatalf("improved: %+v", r.FuncImproved)
	}
	if r.FuncImproved[1].Verdict != Removed {
		t.Errorf("gone verdict")
	}

	o.TopFunctions = 1
	r = Compare(b, c, o)
	if len(r.FuncRegressed) != 1 || len(r.FuncImproved) != 1 {
		t.Errorf("truncate: %d %d", len(r.FuncRegressed), len(r.FuncImproved))
	}
}

func TestFunctionMissingRunCountsZero(t *testing.T) {
	b := []*metrics.Summary{{Functions: []metrics.FuncStat{fs("f", 10, 10)}}, {}, {}}
	c := []*metrics.Summary{{}}
	r := Compare(b, c, DefaultOptions())
	// base median over [10,0,0] is 0, candidate absent: no change.
	if len(r.FuncRegressed)+len(r.FuncImproved) != 0 {
		t.Errorf("unexpected %+v %+v", r.FuncRegressed, r.FuncImproved)
	}
}

func TestRepresentative(t *testing.T) {
	mk := func(src string, v float64) *metrics.Summary {
		return sum(src, met("frames.p95_ms", "frames", metrics.UnitMs, metrics.LowerBetter, v))
	}
	g := []*metrics.Summary{mk("a", 10), mk("b", 30), mk("c", 31)}
	r := Compare(g, g, DefaultOptions())
	if r.BaseRep.Source != "b" {
		t.Errorf("rep %s", r.BaseRep.Source)
	}
	if r.Baseline.Label != "baseline" || r.Cand.Label != "candidate" || r.Baseline.Runs != 3 {
		t.Errorf("side %+v", r.Baseline)
	}
	g2 := []*metrics.Summary{sum("x"), sum("y")}
	if Compare(g2, g2, DefaultOptions()).BaseRep.Source != "x" {
		t.Errorf("fallback")
	}
}

func TestEmptyGroups(t *testing.T) {
	r := Compare(nil, nil, DefaultOptions())
	if r == nil || len(r.Metrics) != 0 || r.BaseRep != nil || r.CandRep != nil {
		t.Errorf("%+v", r)
	}
}
