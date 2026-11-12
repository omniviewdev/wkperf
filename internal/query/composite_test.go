package query

import (
	"testing"

	"github.com/omniviewdev/wkperf/internal/testgen"
)

func TestStalledCompositeMetricsAreQueryable(t *testing.T) {
	engine := engineFor(testgen.StalledComposites())
	for key, want := range map[string]float64{
		"layout.composite.stalled_count":      2,
		"layout.composite.stalled_ms":         26000,
		"layout.composite.stall_threshold_ms": 50,
		"layout.composite.count":              2,
		"layout.composite.total_ms":           55,
		"breakdown.Composite.ms":              55,
	} {
		result := run(t, engine, Request{Operation: "metrics", Selector: key})
		if len(result.Rows) != 1 || result.Rows[0].Value != want || result.Rows[0].Basis != "measured" {
			t.Errorf("%s = %+v; want measured %g", key, result.Rows, want)
		}
	}
}

func TestStalledCompositeMetricsAreDiscoverable(t *testing.T) {
	result := run(t, engineFor(testgen.StalledComposites()), Request{Operation: "keys", Selector: "layout.composite."})
	have := map[string]string{}
	for _, row := range result.Rows {
		have[row.Key] = row.Unit
	}
	for key, unit := range map[string]string{
		"layout.composite.stalled_count":      "count",
		"layout.composite.stalled_ms":         "ms",
		"layout.composite.stall_threshold_ms": "ms",
	} {
		if have[key] != unit {
			t.Errorf("keys omit %s (%s): %v", key, unit, have)
		}
	}
}
