package compare

import (
	"testing"

	"github.com/omniviewdev/wkperf/internal/analyze"
	"github.com/omniviewdev/wkperf/internal/metrics"
	"github.com/omniviewdev/wkperf/internal/model"
	"github.com/omniviewdev/wkperf/internal/testgen"
)

func TestStalledCompositeMetricsAreInformationalInComparisons(t *testing.T) {
	candidate := testgen.StalledComposites()
	candidate.Records = append(candidate.Records, model.Record{Type: model.TypeLayout, EventType: "composite", StartTime: 20, EndTime: 25})
	res := Compare(
		[]*metrics.Summary{analyze.Summarize(testgen.StalledComposites(), "baseline.json")},
		[]*metrics.Summary{analyze.Summarize(candidate, "candidate.json")},
		DefaultOptions(),
	)
	byKey := map[string]MetricDiff{}
	for _, m := range res.Metrics {
		byKey[m.Metric.Key] = m
	}
	for key, want := range map[string]struct{ base, cand float64 }{
		"layout.composite.stalled_count": {2, 3},
		"layout.composite.stalled_ms":    {26000, 31000},
	} {
		m, ok := byKey[key]
		if !ok {
			t.Errorf("comparison omits %s", key)
			continue
		}
		if m.Base != want.base || m.Cand != want.cand {
			t.Errorf("%s base/candidate = %g/%g, want %g/%g", key, m.Base, m.Cand, want.base, want.cand)
		}
		if m.Verdict != Info || m.Metric.Dir != metrics.Neutral {
			t.Errorf("%s must be informational, never a regression: %+v", key, m)
		}
	}
	// The stalled span no longer reads as composite work, so the work metric
	// does not move when only stalled records are added.
	if m := byKey["layout.total_ms"]; m.Delta != 0 {
		t.Errorf("a stalled composite changed layout work: %+v", m)
	}
}
