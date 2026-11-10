package scorecard

import (
	"bytes"
	"encoding/json"
	"math"
	"strings"
	"testing"

	"github.com/omniviewdev/wkperf/internal/insights"
	"github.com/omniviewdev/wkperf/internal/metrics"
	"github.com/omniviewdev/wkperf/internal/testgen"
)

func TestFrameStandardsRequireCountAndObservation(t *testing.T) {
	for _, tc := range []struct {
		name            string
		count, activeMs float64
		available       bool
	}{
		{"few-long-frames", 5, 2000, false},
		{"below-count", 29, 2000, false},
		{"below-observation", 60, 999, false},
		{"minimum", 30, 1000, true},
		{"healthy-60hz", 120, 2000, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rep := report(10,
				metrics.Metric{Key: "frames.count", Value: tc.count},
				metrics.Metric{Key: "frames.active_ms", Value: tc.activeMs},
				metrics.Metric{Key: "frames.idle_wakeups", Value: 7},
				metrics.Metric{Key: "frames.p95_ms", Value: 16},
				metrics.Metric{Key: "frames.fps", Value: 60},
				metrics.Metric{Key: "frames.jank_ratio", Value: 0},
			)
			c := Evaluate(nil, rep)
			for _, id := range []string{"frame-p95", "fps", "jank-ratio"} {
				r := get(t, c, id)
				if (r.Rating != Unavailable) != tc.available {
					t.Errorf("%s availability: %+v", id, r)
				}
				if !strings.Contains(r.Detail, "active frames") || !strings.Contains(r.Detail, "7 idle gaps") {
					t.Errorf("%s lacks observation detail: %q", id, r.Detail)
				}
				if !tc.available && !strings.Contains(r.Detail, "insufficient data") {
					t.Errorf("%s lacks unavailable reason: %q", id, r.Detail)
				}
			}
		})
	}
}

func TestMissingFrameObservationIsUnavailable(t *testing.T) {
	for _, key := range []string{"frames.count", "frames.active_ms"} {
		rep := report(10,
			metrics.Metric{Key: key, Value: 1000},
			metrics.Metric{Key: "frames.p95_ms", Value: 16},
			metrics.Metric{Key: "frames.fps", Value: 60},
			metrics.Metric{Key: "frames.jank_ratio", Value: 0},
		)
		for _, r := range Evaluate(nil, rep).Results {
			if r.ID == "frame-p95" || r.ID == "fps" || r.ID == "jank-ratio" {
				if r.Rating != Unavailable || !strings.Contains(r.Detail, "insufficient data") {
					t.Errorf("missing frame observation must withhold standard: %+v", r)
				}
			}
		}
	}
}

func TestCoverageWithholdsUnsupportedGrade(t *testing.T) {
	for _, tc := range []struct {
		name    string
		values  map[string]float64
		weight  float64
		partial bool
	}{
		{"empty", nil, 0, true},
		{"one-standard", map[string]float64{"frame-p95": 16}, 3, true},
		{"non-frame-majority", map[string]float64{"tbt": 0, "inp": 0, "long-task-rate": 0, "fcp": 0, "lcp": 0, "main-busy": 0, "gc-share": 0, "memory-growth": 0, "forced-layout-rate": 0}, 12, true},
		{"sufficient", map[string]float64{"frame-p95": 16, "jank-ratio": 0, "fps": 60, "tbt": 0}, 11, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c := FromValues(tc.values)
			if (c.Grade == "n/a") != tc.partial {
				t.Errorf("grade %q, want partial=%v", c.Grade, tc.partial)
			}
			var wire struct {
				Coverage *float64 `json:"coverage"`
				Partial  *bool    `json:"partial"`
			}
			data, err := json.Marshal(c)
			if err != nil {
				t.Fatal(err)
			}
			if err := json.Unmarshal(data, &wire); err != nil {
				t.Fatal(err)
			}
			if wire.Coverage == nil || math.Abs(*wire.Coverage-tc.weight/21) > 1e-9 {
				t.Errorf("coverage must reflect available/total weight: %s", data)
			}
			if wire.Partial == nil || *wire.Partial != tc.partial {
				t.Errorf("numeric score must be explicitly partial: %s", data)
			}
		})
	}
}

func TestCollectorIntervalsCannotGradeMainThreadPauses(t *testing.T) {
	rec := testgen.Generate(2, testgen.Baseline())
	c := Evaluate(rec, report(rec.EndTime, metrics.Metric{Key: "gc.total_ms", Value: 5000}))
	r := get(t, c, "gc-share")
	if r.Rating != Unavailable || !strings.Contains(r.Detail, "collector intervals") || !strings.Contains(r.Detail, "main-thread") {
		t.Fatalf("collector interval cannot grade pause time: %+v", r)
	}
}

func TestFrameStandardsKeepFixedBudget(t *testing.T) {
	p := testgen.Baseline()
	p.DurationSec, p.FrameMeanMs, p.FrameJitterMs = 2, 40, 0
	p.ScriptScale = 5
	rec := testgen.Generate(1, p)
	fixed := Evaluate(rec, insights.Analyze(rec, "fixed.json", insights.Options{JankMs: 16.7}))
	custom := Evaluate(rec, insights.Analyze(rec, "fixed.json", insights.Options{JankMs: 200}))
	for _, id := range []string{"frame-p95", "jank-ratio", "fps"} {
		a, b := get(t, fixed, id), get(t, custom, id)
		if a.Rating == Unavailable || a.Value != b.Value || a.Rating != b.Rating {
			t.Errorf("%s must use fixed 16.7ms standard: fixed=%+v custom=%+v", id, a, b)
		}
	}
}

func TestHealthySixtyHzCaptureRemainsGradeable(t *testing.T) {
	p := testgen.Baseline()
	p.DurationSec, p.FrameMeanMs, p.FrameJitterMs = 2, 1000.0/60, 0
	p.LongTaskRate, p.ForcedLayoutRate = 0, 0
	rec := testgen.Generate(1, p)
	c := Evaluate(rec, insights.Analyze(rec, "healthy.json", insights.Options{}))
	if c.Grade != "A" {
		t.Fatalf("healthy observed 60Hz capture must remain gradeable: %+v", c)
	}
}

func TestUnavailableScoreAndReasonsRender(t *testing.T) {
	c := Evaluate(nil, report(10, metrics.Metric{Key: "frames.count", Value: 2}))
	for _, render := range []func(*bytes.Buffer) error{
		func(b *bytes.Buffer) error { return RenderTerm(b, c, false) },
		func(b *bytes.Buffer) error { return RenderMarkdown(b, c) },
	} {
		var out bytes.Buffer
		if err := render(&out); err != nil {
			t.Fatal(err)
		}
		for _, want := range []string{"grade n/a", "partial score", "coverage", "insufficient data"} {
			if !strings.Contains(out.String(), want) {
				t.Errorf("missing %q: %s", want, out.String())
			}
		}
	}
}

func TestNestedTasksMatchFixedBlockingStandards(t *testing.T) {
	rec := testgen.NestedRecords()
	c := Evaluate(rec, insights.Analyze(rec, "nested.json", insights.Options{LongTaskMs: 200}))
	if r := get(t, c, "tbt"); math.Abs(r.Value-1530) > 1e-6 || r.Detail != "3 long tasks, worst 212ms" {
		t.Errorf("nested records must share one fixed-50ms task model: %+v", r)
	}
	if r := get(t, c, "long-task-rate"); math.Abs(r.Value-90) > 1e-6 {
		t.Errorf("three tasks over two seconds: %+v", r)
	}
}
