package analyze

import (
	"testing"

	"github.com/omniviewdev/wkperf/internal/metrics"
	"github.com/omniviewdev/wkperf/internal/model"
	"github.com/omniviewdev/wkperf/internal/testgen"
)

func TestStalledCompositesLeaveLayoutStatisticsAndAreReportedApart(t *testing.T) {
	s := Summarize(testgen.StalledComposites(), "stalled.json")
	for key, want := range map[string]float64{
		"layout.composite.stalled_count":      2,
		"layout.composite.stalled_ms":         26000,
		"layout.composite.stall_threshold_ms": 50,
		"layout.composite.count":              2,
		"layout.composite.total_ms":           55,
		"layout.composite.max_ms":             49,
		"layout.composite.exclusive_ms":       55,
		"layout.total_ms":                     55,
		"script.total_ms":                     100,
	} {
		if got := val(t, s, key); !approx(got, want) {
			t.Errorf("%s = %g, want %g", key, got, want)
		}
	}
	if want := (metrics.StalledComposites{ThresholdMs: 50, Count: 2, TotalMs: 26000}); s.StalledComposites != want {
		t.Errorf("summary stalled composites = %+v, want %+v", s.StalledComposites, want)
	}
	for _, key := range []string{"layout.composite.stalled_count", "layout.composite.stalled_ms", "layout.composite.stall_threshold_ms"} {
		m, _ := s.Lookup(key)
		if m.Group != "layout" || m.Dir != metrics.Neutral {
			t.Errorf("%s must be informational in the layout group: %+v", key, m)
		}
	}
	if m, _ := s.Lookup("layout.composite.stalled_ms"); !m.Extensive {
		t.Error("stalled composite time scales with capture length and must normalize per second")
	}
}

func TestCompositeOfFortyNineMillisecondsStillCounts(t *testing.T) {
	rec := testgen.Empty()
	rec.Records = []model.Record{{Type: model.TypeLayout, EventType: "composite", StartTime: 1, EndTime: 1.049}}
	s := Summarize(rec, "short.json")
	for key, want := range map[string]float64{
		"layout.composite.stalled_count": 0,
		"layout.composite.stalled_ms":    0,
		"layout.composite.count":         1,
		"layout.composite.total_ms":      49,
		"layout.composite.exclusive_ms":  49,
		"layout.total_ms":                49,
	} {
		if got := val(t, s, key); !approx(got, want) {
			t.Errorf("%s = %g, want %g", key, got, want)
		}
	}
}

func TestStalledCompositeWindowReportsClippedSpan(t *testing.T) {
	rec := testgen.StalledComposites()
	// The window overlaps only the first stalled composite, for 1 s of its span.
	s := SummarizeWith(rec, "window.json", Options{FromSec: 5, ToSec: 6})
	for key, want := range map[string]float64{
		"layout.composite.stalled_count": 1,
		"layout.composite.stalled_ms":    1000,
		"layout.total_ms":                0,
	} {
		if got := val(t, s, key); !approx(got, want) {
			t.Errorf("%s = %g, want %g", key, got, want)
		}
	}
	if _, ok := s.Lookup("layout.composite.count"); ok {
		t.Error("a window holding only a stalled composite has no ordinary composite statistics")
	}
}

func TestStalledCompositeMetricsAbsentWithoutLayoutRecords(t *testing.T) {
	rec := testgen.Empty()
	rec.Records = []model.Record{{Type: model.TypeScript, EventType: "timer-fired", StartTime: 1, EndTime: 1.01}}
	s := Summarize(rec, "script-only.json")
	if _, ok := s.Lookup("layout.composite.stalled_count"); ok {
		t.Error("a capture without layout records must not invent layout metrics")
	}
	if s.StalledComposites.Count != 0 || s.StalledComposites.ThresholdMs != 50 {
		t.Errorf("summary field should still report the fixed threshold: %+v", s.StalledComposites)
	}
}
