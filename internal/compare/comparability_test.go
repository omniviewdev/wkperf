package compare

import (
	"math"
	"strings"
	"testing"

	"github.com/omniviewdev/wkperf/internal/metrics"
)

func ext(key, group string, u metrics.Unit, v float64) metrics.Metric {
	m := met(key, group, u, metrics.LowerBetter, v)
	m.Extensive = true
	return m
}

func run(dur float64, ms ...metrics.Metric) *metrics.Summary {
	return &metrics.Summary{Source: "r", DurationSec: dur, Metrics: ms}
}

func codes(c Comparability) map[string]string {
	out := map[string]string{}
	for _, w := range c.Warnings {
		out[w.Code] = w.Level
	}
	return out
}

func TestNormalizeAutoRescalesExtensiveOnly(t *testing.T) {
	b := []*metrics.Summary{run(10,
		ext("script.total_ms", "script", metrics.UnitMs, 1000),
		ext("gc.count", "gc", metrics.UnitCount, 20),
		met("frames.p95_ms", "frames", metrics.UnitMs, metrics.LowerBetter, 20))}
	c := []*metrics.Summary{run(20,
		ext("script.total_ms", "script", metrics.UnitMs, 2000),
		ext("gc.count", "gc", metrics.UnitCount, 80),
		met("frames.p95_ms", "frames", metrics.UnitMs, metrics.LowerBetter, 40))}
	r := Compare(b, c, DefaultOptions())
	if !r.Comparability.Normalized {
		t.Fatal("expected normalization for 10s vs 20s")
	}
	st := find(t, r, "script.total_ms")
	if st.Base != 100 || st.Cand != 100 || st.Verdict != Unchanged || st.Metric.Label != " (ms per s)" { // fixture label is empty: only the suffix remains
		t.Errorf("script.total_ms: %+v", st)
	}
	gc := find(t, r, "gc.count")
	if gc.Base != 2 || gc.Cand != 4 || gc.Verdict != Regressed {
		t.Errorf("gc.count per second: %+v", gc)
	}
	if gc.Metric.Unit != metrics.UnitCount || gc.Metric.Label != " /s" {
		t.Errorf("gc label/unit: %+v", gc.Metric)
	}
	p := find(t, r, "frames.p95_ms")
	if p.Base != 20 || p.Cand != 40 || p.Verdict != Regressed {
		t.Errorf("intensive metric must not be rescaled: %+v", p)
	}
	if find(t, r, "frames.p95_ms").Metric.Label != "" {
		t.Error("intensive label changed")
	}
	if codes(r.Comparability)[CodeDurationMismatch] != LevelInfo {
		t.Errorf("want info duration-mismatch when normalized: %+v", r.Comparability.Warnings)
	}
}

func TestNormalizeLabels(t *testing.T) {
	m := ext("script.total_ms", "script", metrics.UnitMs, 100)
	m.Label = "Script total"
	n := ext("gc.count", "gc", metrics.UnitCount, 3)
	n.Label = "GC count"
	r := Compare([]*metrics.Summary{run(10, m, n)}, []*metrics.Summary{run(30, m, n)}, DefaultOptions())
	if got := find(t, r, "script.total_ms").Metric.Label; got != "Script total (ms per s)" {
		t.Errorf("ms label %q", got)
	}
	if got := find(t, r, "gc.count").Metric.Label; got != "GC count /s" {
		t.Errorf("count label %q", got)
	}
}

func TestNormalizeModes(t *testing.T) {
	mk := func(dur, v float64) []*metrics.Summary {
		return []*metrics.Summary{run(dur, ext("script.total_ms", "script", metrics.UnitMs, v))}
	}
	o := DefaultOptions()

	// Within 10%: auto leaves values alone.
	r := Compare(mk(10, 100), mk(10.5, 130), o)
	if r.Comparability.Normalized || find(t, r, "script.total_ms").Cand != 130 {
		t.Errorf("auto normalized within tolerance: %+v", r.Comparability)
	}
	if len(r.Comparability.Warnings) != 0 || r.Comparability.Score != 1 {
		t.Errorf("unexpected warnings: %+v", r.Comparability)
	}

	// per-second forces it even for equal durations.
	o.Normalize = NormalizePerSecond
	r = Compare(mk(10, 100), mk(10, 130), o)
	if !r.Comparability.Normalized || find(t, r, "script.total_ms").Cand != 13 {
		t.Errorf("per-second not applied: %+v %+v", r.Comparability, find(t, r, "script.total_ms"))
	}

	// none: values raw, mismatch is a warn and costs more.
	o.Normalize = NormalizeNone
	r = Compare(mk(10, 100), mk(20, 200), o)
	if r.Comparability.Normalized || find(t, r, "script.total_ms").Cand != 200 {
		t.Errorf("none normalized: %+v", r.Comparability)
	}
	if codes(r.Comparability)[CodeDurationMismatch] != LevelWarn || !approx(r.Comparability.Score, 0.75) {
		t.Errorf("want warn mismatch score 0.75: %+v", r.Comparability)
	}

	// Empty mode behaves like auto.
	o.Normalize = ""
	if r = Compare(mk(10, 100), mk(20, 200), o); !r.Comparability.Normalized {
		t.Error("empty Normalize must mean auto")
	}

	// Unknown durations can never be rescaled.
	o.Normalize = NormalizePerSecond
	r = Compare(mk(0, 100), mk(10, 100), o)
	if r.Comparability.Normalized || find(t, r, "script.total_ms").Cand != 100 {
		t.Errorf("zero duration must disable normalization: %+v", r.Comparability)
	}
}

func approx(a, b float64) bool { return math.Abs(a-b) < 1e-9 }

func TestNormalizedFloorsAreScaled(t *testing.T) {
	// 1.0 ms/s -> 1.3 ms/s is a +30% change of 0.3: below the 0.5 raw floor
	// but above the scaled 0.05 floor.
	b := []*metrics.Summary{run(10, ext("script.total_ms", "script", metrics.UnitMs, 10))}
	c := []*metrics.Summary{run(20, ext("script.total_ms", "script", metrics.UnitMs, 26))}
	if v := find(t, Compare(b, c, DefaultOptions()), "script.total_ms").Verdict; v != Regressed {
		t.Errorf("normalized verdict %v, want regressed", v)
	}
	// The same 0.3 ms delta on an intensive metric stays under the raw 0.5 floor.
	b2 := []*metrics.Summary{run(10, met("x", "script", metrics.UnitMs, metrics.LowerBetter, 1))}
	c2 := []*metrics.Summary{run(10, met("x", "script", metrics.UnitMs, metrics.LowerBetter, 1.3))}
	if v := find(t, Compare(b2, c2, DefaultOptions()), "x").Verdict; v != Unchanged {
		t.Errorf("intensive metric keeps raw floor, got %v", v)
	}
}

func TestFunctionsNormalizedPerSecond(t *testing.T) {
	b := &metrics.Summary{DurationSec: 10, Functions: []metrics.FuncStat{{Name: "f", SelfMs: 100, TotalMs: 200}}}
	c := &metrics.Summary{DurationSec: 20, Functions: []metrics.FuncStat{{Name: "f", SelfMs: 400, TotalMs: 800}}}
	r := Compare([]*metrics.Summary{b}, []*metrics.Summary{c}, DefaultOptions())
	if len(r.FuncRegressed) != 1 {
		t.Fatalf("funcs %+v", r.FuncRegressed)
	}
	f := r.FuncRegressed[0]
	if f.BaseSelf != 10 || f.CandSelf != 20 || f.Delta != 10 || f.BaseTotal != 20 || f.CandTotal != 40 {
		t.Errorf("per-second function values wrong: %+v", f)
	}
	// Equal per-second self time but 4x the raw totals: no regression.
	c.Functions[0].SelfMs = 200
	r = Compare([]*metrics.Summary{b}, []*metrics.Summary{c}, DefaultOptions())
	if len(r.FuncRegressed) != 0 || len(r.FuncImproved) != 0 {
		t.Errorf("same rate must not diff: %+v %+v", r.FuncRegressed, r.FuncImproved)
	}
}

func fixtureSum(p95 float64, f metrics.Fixture) *metrics.Summary {
	fps := 60.0
	return &metrics.Summary{
		DurationSec: f.DurationSec, Fixture: f,
		Metrics: []metrics.Metric{
			ext("frames.count", "frames", metrics.UnitCount, float64(f.Frames)),
			met("frames.p95_ms", "frames", metrics.UnitMs, metrics.LowerBetter, p95),
			met("frames.fps", "frames", metrics.UnitFPS, metrics.HigherBetter, fps),
		},
	}
}

func goodFixture() metrics.Fixture {
	return metrics.Fixture{
		DurationSec: 10, Frames: 600, Records: 1000, SampleCount: 5000,
		URLs: []string{"https://app/"}, Instruments: []string{"cpu", "script"},
		TopFunctionKeys: []string{"a", "b", "c", "d"},
	}
}

func TestComparabilityIdentical(t *testing.T) {
	c := Compare([]*metrics.Summary{fixtureSum(16, goodFixture())}, []*metrics.Summary{fixtureSum(17, goodFixture())}, DefaultOptions()).Comparability
	if c.Score != 1 || len(c.Warnings) != 0 || c.Normalized {
		t.Errorf("identical fixtures: %+v", c)
	}
	if c.Warnings == nil {
		t.Error("Warnings must serialize as [], not null")
	}
}

func TestComparabilityWarnings(t *testing.T) {
	base := fixtureSum(16, goodFixture())
	cf := goodFixture()
	cf.DurationSec = 20
	cf.Frames = 600 // 30 frames/s vs 60: cadence differs
	cf.URLs = []string{"https://other/"}
	cf.Instruments = []string{"cpu"}
	cf.TopFunctionKeys = []string{"a", "x", "y", "z"} // jaccard 1/7
	cf.SampleCount = 50
	cand := fixtureSum(16, cf)
	cr := Compare([]*metrics.Summary{base}, []*metrics.Summary{cand}, DefaultOptions()).Comparability
	want := map[string]string{
		CodeDurationMismatch: LevelInfo, CodeFrameCountMismatch: LevelInfo, CodeURLMismatch: LevelWarn,
		CodeInstrumentMismatch: LevelWarn, CodeWorkloadMismatch: LevelWarn, CodeSampleSparse: LevelWarn,
	}
	got := codes(cr)
	if len(got) != len(want) {
		t.Fatalf("warnings %+v", cr.Warnings)
	}
	for k, v := range want {
		if got[k] != v {
			t.Errorf("%s: level %q want %q", k, got[k], v)
		}
	}
	// 1 - (0.05+0.05+0.15+0.15+0.25+0.10)
	if !approx(cr.Score, 0.25) || !cr.Normalized {
		t.Errorf("score %v normalized %v", cr.Score, cr.Normalized)
	}
	for _, w := range cr.Warnings {
		if w.Message == "" {
			t.Errorf("empty message for %s", w.Code)
		}
	}
}

func TestComparabilityWorkloadThreshold(t *testing.T) {
	mk := func(keys ...string) []*metrics.Summary {
		f := goodFixture()
		f.TopFunctionKeys = keys
		return []*metrics.Summary{fixtureSum(16, f)}
	}
	// Jaccard 2/4 = 0.5 is not below the threshold.
	r := Compare(mk("a", "b", "c"), mk("a", "b", "d"), DefaultOptions())
	if _, ok := codes(r.Comparability)[CodeWorkloadMismatch]; ok {
		t.Errorf("jaccard 0.5 flagged: %+v", r.Comparability.Warnings)
	}
	r = Compare(mk("a", "b", "c", "d"), mk("a", "x", "y", "z"), DefaultOptions())
	if codes(r.Comparability)[CodeWorkloadMismatch] != LevelWarn {
		t.Errorf("jaccard 1/7 not flagged: %+v", r.Comparability.Warnings)
	}
}

func TestComparabilityRunVariance(t *testing.T) {
	f := goodFixture()
	stable := []*metrics.Summary{fixtureSum(16, f), fixtureSum(16.5, f)}
	noisy := []*metrics.Summary{fixtureSum(10, f), fixtureSum(20, f)} // CV 47%
	cr := Compare(stable, noisy, DefaultOptions()).Comparability
	if codes(cr)[CodeRunVariance] != LevelWarn || len(cr.Warnings) != 1 || !approx(cr.Score, 0.9) {
		t.Errorf("candidate-only variance: %+v", cr)
	}
	if cr.Warnings[0].Message == "" || !strings.Contains(cr.Warnings[0].Message, "candidate") {
		t.Errorf("message should name the group: %q", cr.Warnings[0].Message)
	}
	// A single run has no variance to judge.
	if len(Compare(stable[:1], noisy[:1], DefaultOptions()).Comparability.Warnings) != 0 {
		t.Error("variance flagged for single runs")
	}
}

func TestComparabilitySkipsMissingData(t *testing.T) {
	// Missing fixture metadata does not warn when active windows are observed.
	b := []*metrics.Summary{sum("b", met("k", "script", metrics.UnitMs, metrics.LowerBetter, 1))}
	c := []*metrics.Summary{sum("c", met("k", "script", metrics.UnitMs, metrics.LowerBetter, 1))}
	b[0].DurationSec, c[0].DurationSec = 10, 10
	r := Compare(b, c, DefaultOptions())
	if r.Comparability.Score != 1 || len(r.Comparability.Warnings) != 0 {
		t.Errorf("no data must not warn: %+v", r.Comparability)
	}
	r = Compare(nil, c, DefaultOptions())
	if r.Comparability.Score != 0 || codes(r.Comparability)[CodeWindowEmpty] != LevelWarn {
		t.Errorf("empty baseline: %+v", r.Comparability)
	}
	if r = Compare(nil, nil, DefaultOptions()); r.Comparability.Score != 0 || codes(r.Comparability)[CodeWindowEmpty] != LevelWarn {
		t.Errorf("both empty: %+v", r.Comparability)
	}
}

func TestComparabilityScoreClamped(t *testing.T) {
	bf := goodFixture()
	cf := metrics.Fixture{DurationSec: 40, Frames: 100, Records: 10, SampleCount: 1,
		URLs: []string{"https://other/"}, Instruments: []string{"x"}, TopFunctionKeys: []string{"q"}}
	o := DefaultOptions()
	o.Normalize = NormalizeNone
	b := []*metrics.Summary{fixtureSum(10, bf), fixtureSum(20, bf)}
	c := []*metrics.Summary{fixtureSum(10, cf), fixtureSum(20, cf)}
	cr := Compare(b, c, o).Comparability
	if cr.Score != 0 {
		t.Errorf("score must clamp at 0, got %v (%d warnings)", cr.Score, len(cr.Warnings))
	}
}
