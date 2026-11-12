package analyze

import (
	"strings"
	"testing"

	"github.com/omniviewdev/wkperf/internal/metrics"
	"github.com/omniviewdev/wkperf/internal/model"
	"github.com/omniviewdev/wkperf/internal/profile"
	"github.com/omniviewdev/wkperf/internal/testgen"
)

func cadenceKeys(s *metrics.Summary) []metrics.Metric {
	var out []metrics.Metric
	for _, m := range s.Metrics {
		if strings.HasPrefix(m.Key, "cadence.") {
			out = append(out, m)
		}
	}
	return out
}

func TestCompositeCadenceEstimateMetrics(t *testing.T) {
	s := Summarize(testgen.CompositeCadenceRuns(), "cadence.json")
	for key, want := range map[string]float64{
		"cadence.composite.starts":             23,
		"cadence.composite.runs":               2,
		"cadence.composite.count":              20,
		"cadence.composite.p50_ms":             17,
		"cadence.composite.p95_ms":             61.5,
		"cadence.composite.max_ms":             90,
		"cadence.composite.missed_vsync_count": 5,
		"cadence.composite.missed_vsync_ratio": .25,
		"cadence.composite.over_50ms_count":    2,
		"cadence.composite.active_run_s":       .5,
	} {
		if got := val(t, s, key); !approx(got, want) {
			t.Errorf("%s = %g, want %g", key, got, want)
		}
	}
	for _, m := range cadenceKeys(s) {
		if m.Basis != metrics.BasisEstimated || m.Group != "cadence" || !strings.HasSuffix(m.Label, "(estimate)") {
			t.Errorf("%s must be labeled an estimate: %+v", m.Key, m)
		}
	}
	if c := s.CompositeCadence; c == nil || c.Basis != metrics.BasisEstimated || c.Starts != 23 || c.Runs != 2 || c.Count != 20 ||
		!approx(c.P50Ms, 17) || !approx(c.P95Ms, 61.5) || !approx(c.MaxMs, 90) || c.MissedVsyncCount != 5 ||
		!approx(c.MissedVsyncRatio, .25) || c.Over50MsCount != 2 || !approx(c.ActiveRunSec, .5) {
		t.Errorf("summary cadence = %+v", c)
	}
	// Run time and counts scale with the capture; percentiles and ratios do not.
	for key, extensive := range map[string]bool{"cadence.composite.count": true, "cadence.composite.active_run_s": true, "cadence.composite.p95_ms": false, "cadence.composite.missed_vsync_ratio": false} {
		if m, _ := s.Lookup(key); m.Extensive != extensive {
			t.Errorf("%s extensive = %v", key, m.Extensive)
		}
	}
}

func TestCompositeCadenceNeverFillsFrameStatistics(t *testing.T) {
	s := Summarize(testgen.CompositeCadenceRuns(), "cadence.json")
	for _, key := range []string{"frames.count", "frames.p50_ms", "frames.p95_ms", "frames.fps", "frames.jank_ratio", "frames.jank_16ms", "frames.active_ms"} {
		if m, ok := s.Lookup(key); ok {
			t.Errorf("estimate leaked into %s = %g", key, m.Value)
		}
	}
	if len(s.FrameMs) != 0 {
		t.Errorf("estimate leaked into the frame series: %v", s.FrameMs)
	}
}

func TestCompositeCadenceOnlyWithoutRenderingFrameRecords(t *testing.T) {
	rec := testgen.CompositeCadenceRuns()
	rec.Records = append(rec.Records, model.Record{Type: model.TypeRenderingFrame, StartTime: 5, EndTime: 5.016})
	s := Summarize(rec, "framed.json")
	if got := cadenceKeys(s); len(got) != 0 || s.CompositeCadence != nil {
		t.Fatalf("rendering-frame records exist, so no estimate is allowed: %+v %+v", got, s.CompositeCadence)
	}
	if val(t, s, "frames.count") != 1 {
		t.Error("measured frame statistics must remain")
	}

	// A window that excludes the frame still belongs to a capture that has frames.
	for name, s := range map[string]*metrics.Summary{
		"window":  SummarizeWith(rec, "window.json", Options{FromSec: 1, ToSec: 2}),
		"clipped": Summarize(profile.Clip(rec, rec.StartTime+1, rec.StartTime+2), "clipped.json"),
	} {
		if got := cadenceKeys(s); len(got) != 0 || s.CompositeCadence != nil {
			t.Errorf("%s: a window without frame records must not turn on the estimate: %+v", name, got)
		}
	}
}

func TestCompositeCadenceWindow(t *testing.T) {
	rec := testgen.CompositeCadenceRuns()
	// Only the second run: 11 starts, ten intervals summing 276 ms.
	s := SummarizeWith(rec, "window.json", Options{FromSec: 1.4})
	for key, want := range map[string]float64{
		"cadence.composite.starts":       12,
		"cadence.composite.runs":         1,
		"cadence.composite.count":        10,
		"cadence.composite.max_ms":       90,
		"cadence.composite.active_run_s": .276,
	} {
		if got := val(t, s, key); !approx(got, want) {
			t.Errorf("%s = %g, want %g", key, got, want)
		}
	}
}

func TestCompositeCadenceWithoutIntervalsHasNoPercentiles(t *testing.T) {
	rec := testgen.StalledComposites()
	s := Summarize(rec, "idle.json")
	for key, want := range map[string]float64{
		"cadence.composite.starts":             4,
		"cadence.composite.runs":               0,
		"cadence.composite.count":              0,
		"cadence.composite.missed_vsync_count": 0,
		"cadence.composite.over_50ms_count":    0,
		"cadence.composite.active_run_s":       0,
	} {
		if got := val(t, s, key); !approx(got, want) {
			t.Errorf("%s = %g, want %g", key, got, want)
		}
	}
	for _, key := range []string{"cadence.composite.p50_ms", "cadence.composite.p95_ms", "cadence.composite.max_ms", "cadence.composite.missed_vsync_ratio"} {
		if _, ok := s.Lookup(key); ok {
			t.Errorf("%s needs at least one interval; a zero would pass as a measurement", key)
		}
	}
	if s.CompositeCadence == nil || s.CompositeCadence.Count != 0 || s.CompositeCadence.Basis != metrics.BasisEstimated {
		t.Errorf("idle capture still reports its starts: %+v", s.CompositeCadence)
	}
}

func TestNoCadenceWithoutCompositeRecords(t *testing.T) {
	rec := testgen.Empty()
	rec.Records = []model.Record{{Type: model.TypeScript, EventType: "timer-fired", StartTime: 1, EndTime: 1.01}}
	s := Summarize(rec, "script.json")
	if got := cadenceKeys(s); len(got) != 0 || s.CompositeCadence != nil {
		t.Fatalf("nothing to estimate: %+v %+v", got, s.CompositeCadence)
	}
}
