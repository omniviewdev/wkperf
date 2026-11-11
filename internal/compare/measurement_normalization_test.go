package compare

import (
	"math"
	"testing"

	"github.com/omniviewdev/wkperf/internal/analyze"
	"github.com/omniviewdev/wkperf/internal/metrics"
	"github.com/omniviewdev/wkperf/internal/testgen"
)

func TestRepeatedMeasurementWorkNormalizesWithoutRegression(t *testing.T) {
	baseline := analyze.Summarize(testgen.RepeatedMeasurementWorkload(1), "baseline.json")
	candidate := analyze.Summarize(testgen.RepeatedMeasurementWorkload(2), "candidate.json")
	wanted := map[string]bool{
		"frames.observed_count":                false,
		"frames.observed_ms":                   false,
		"frames.active_ms":                     false,
		"frames.input_stalls":                  false,
		"frames.input_stall_ms":                false,
		"script.long_tasks.tbt_ms":             false,
		"script.event-dispatched.exclusive_ms": false,
		"layout.layout.exclusive_ms":           false,
		"gc.overlap_with_work_ms":              false,
	}
	for _, metric := range baseline.Metrics {
		if _, ok := wanted[metric.Key]; ok && metric.Value <= 0 {
			t.Fatalf("workload does not exercise %s", metric.Key)
		}
	}
	options := DefaultOptions()
	options.Normalize = "per-second"
	result := Compare([]*metrics.Summary{baseline}, []*metrics.Summary{candidate}, options)
	for _, metric := range result.Metrics {
		if _, ok := wanted[metric.Metric.Key]; !ok {
			continue
		}
		wanted[metric.Metric.Key] = true
		if math.Abs(metric.Cand-metric.Base) > 1e-8*math.Max(1, math.Abs(metric.Base)) || metric.Verdict == Regressed {
			t.Errorf("repeating identical work changed normalized %s: %g → %g (%s)", metric.Metric.Key, metric.Base, metric.Cand, metric.Verdict)
		}
	}
	for key, found := range wanted {
		if !found {
			t.Errorf("comparison omitted measured %s", key)
		}
	}
}
