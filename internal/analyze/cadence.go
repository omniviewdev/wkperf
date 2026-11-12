package analyze

import (
	"sort"

	"github.com/omniviewdev/wkperf/internal/metrics"
	"github.com/omniviewdev/wkperf/internal/profile"
)

// compositeCadence summarizes composite start spacing as an estimate. It
// returns nil when the selection has no composite start: nothing to estimate.
// Percentiles exist only when at least one interval does.
func compositeCadence(sample profile.CadenceSample) *metrics.CompositeCadence {
	if sample.Starts == 0 {
		return nil
	}
	c := &metrics.CompositeCadence{Basis: metrics.BasisEstimated, Starts: sample.Starts, Runs: sample.Runs, Count: len(sample.IntervalsMs)}
	if c.Count == 0 {
		return c
	}
	sorted := append([]float64(nil), sample.IntervalsMs...)
	sort.Float64s(sorted)
	c.P50Ms, c.P95Ms, c.MaxMs = percentileSorted(sorted, 50), percentileSorted(sorted, 95), sorted[len(sorted)-1]
	var total float64
	for _, ms := range sorted {
		total += ms
		if ms > profile.CadenceMissedVsyncMs {
			c.MissedVsyncCount++
		}
		if ms > profile.CadenceSlowIntervalMs {
			c.Over50MsCount++
		}
	}
	c.MissedVsyncRatio = float64(c.MissedVsyncCount) / float64(c.Count)
	c.ActiveRunSec = total / 1000
	return c
}

// addCompositeCadence registers the estimate as cadence.composite.* scalars.
// Every one carries the estimated basis, so no consumer can mistake it for a
// rendering-frame measurement.
func (b *builder) addCompositeCadence(c *metrics.CompositeCadence) {
	if c == nil {
		return
	}
	add := func(key, label string, unit metrics.Unit, dir metrics.Direction, extensive bool, v float64) {
		b.m = append(b.m, metrics.Metric{Key: "cadence.composite." + key, Group: "cadence", Label: label + " (estimate)", Unit: unit, Dir: dir, Value: v, Extensive: extensive, Basis: c.Basis})
	}
	add("starts", "Composite record starts", metrics.UnitCount, metrics.Neutral, true, float64(c.Starts))
	add("runs", "Active composite runs", metrics.UnitCount, metrics.Neutral, true, float64(c.Runs))
	add("count", "Composite start intervals", metrics.UnitCount, metrics.Neutral, true, float64(c.Count))
	if c.Count > 0 {
		add("p50_ms", "Composite interval p50", metrics.UnitMs, metrics.LowerBetter, false, c.P50Ms)
		add("p95_ms", "Composite interval p95", metrics.UnitMs, metrics.LowerBetter, false, c.P95Ms)
		add("max_ms", "Composite interval max", metrics.UnitMs, metrics.LowerBetter, false, c.MaxMs)
	}
	add("missed_vsync_count", "Composite intervals > 25.05ms (missed vsync)", metrics.UnitCount, metrics.LowerBetter, true, float64(c.MissedVsyncCount))
	if c.Count > 0 {
		add("missed_vsync_ratio", "Composite intervals missing a vsync", metrics.UnitRatio, metrics.LowerBetter, false, c.MissedVsyncRatio)
	}
	add("over_50ms_count", "Composite intervals > 50ms", metrics.UnitCount, metrics.LowerBetter, true, float64(c.Over50MsCount))
	add("active_run_s", "Active composite run time", "s", metrics.Neutral, true, c.ActiveRunSec)
}
