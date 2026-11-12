package compare

import (
	"testing"

	"github.com/omniviewdev/wkperf/internal/analyze"
	"github.com/omniviewdev/wkperf/internal/metrics"
	"github.com/omniviewdev/wkperf/internal/model"
	"github.com/omniviewdev/wkperf/internal/testgen"
)

// stretchedCadence slows every start interval by 10%: p95 grows from 61.5 to 67.65 ms.
func stretchedCadence() *model.Recording {
	rec := testgen.CompositeCadenceRuns()
	for i := range rec.Records {
		r := &rec.Records[i]
		length := r.EndTime - r.StartTime
		r.StartTime = 1 + (r.StartTime-1)*1.1
		r.EndTime = r.StartTime + length
	}
	return rec
}

func TestCompositeCadenceIsComparedLikeAnyScalar(t *testing.T) {
	res := Compare(
		[]*metrics.Summary{analyze.Summarize(testgen.CompositeCadenceRuns(), "baseline.json")},
		[]*metrics.Summary{analyze.Summarize(stretchedCadence(), "candidate.json")},
		DefaultOptions(),
	)
	byKey := map[string]MetricDiff{}
	firstOf := map[string]int{}
	for i, m := range res.Metrics {
		byKey[m.Metric.Key] = m
		if _, seen := firstOf[m.Metric.Group]; !seen {
			firstOf[m.Metric.Group] = i
		}
	}
	p95, ok := byKey["cadence.composite.p95_ms"]
	if !ok {
		t.Fatal("comparison omits the composite cadence estimate")
	}
	if p95.Metric.Group != "cadence" || p95.Metric.Basis != metrics.BasisEstimated {
		t.Errorf("comparison must keep the estimate label: %+v", p95.Metric)
	}
	if !near(p95.Base, 61.5) || !near(p95.Cand, 67.65) || p95.Verdict != Regressed {
		t.Errorf("p95 comparison = base %g cand %g verdict %s", p95.Base, p95.Cand, p95.Verdict)
	}
	if starts := byKey["cadence.composite.starts"]; starts.Verdict != Info || starts.Base != 23 || starts.Cand != 23 {
		t.Errorf("start count is context, not a verdict: %+v", starts)
	}
	for _, key := range []string{"cadence.composite.count", "cadence.composite.runs", "cadence.composite.active_run_s"} {
		if byKey[key].Verdict != Info {
			t.Errorf("%s should be informational: %+v", key, byKey[key])
		}
	}
	// The group sits beside frames, ahead of script and layout.
	if _, ok := firstOf["layout"]; !ok || firstOf["overview"] > firstOf["cadence"] || firstOf["cadence"] > firstOf["layout"] {
		t.Errorf("cadence group order (first index per group): %v", firstOf)
	}
}

func near(a, b float64) bool {
	d := a - b
	return d < 1e-6 && d > -1e-6
}
