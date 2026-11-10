package compare

import (
	"math"
	"sort"

	"github.com/omniviewdev/wkperf/internal/metrics"
)

const funcAbsFloorMs = 0.5

// DefaultOptions returns the standard thresholds.
func DefaultOptions() Options {
	return Options{
		RelThreshold: 0.05,
		AbsFloor: map[metrics.Unit]float64{
			metrics.UnitMs:    0.5,
			metrics.UnitCount: 1,
			metrics.UnitPct:   1,
			metrics.UnitBytes: 1024,
			metrics.UnitMB:    0.5,
			metrics.UnitFPS:   1,
			metrics.UnitRatio: 0.01,
		},
		TopFunctions: 15,
	}
}

var groupOrder = map[string]int{
	"overview": 0, "frames": 1, "script": 2, "layout": 3, "gc": 4,
	"cpu": 5, "memory": 6, "network": 7, "pressure": 8,
}

func groupRank(g string) int {
	if r, ok := groupOrder[g]; ok {
		return r
	}
	return len(groupOrder)
}

// Compare diffs a baseline group against a candidate group.
func Compare(base, cand []*metrics.Summary, opts Options) *Result {
	res := &Result{
		Baseline: side("baseline", base),
		Cand:     side("candidate", cand),
	}
	res.Metrics = compareMetrics(base, cand, opts)
	for _, m := range res.Metrics {
		switch m.Verdict {
		case Improved:
			res.Counts.Improved++
		case Regressed:
			res.Counts.Regressed++
		case Unchanged:
			res.Counts.Unchanged++
		case Noisy:
			res.Counts.Noisy++
		}
	}
	res.FuncRegressed, res.FuncImproved = compareFuncs(base, cand, opts)
	res.BaseRep = representative(base)
	res.CandRep = representative(cand)
	return res
}

func side(label string, group []*metrics.Summary) Side {
	s := Side{Label: label, Sources: []string{}, Runs: len(group)}
	for _, g := range group {
		s.Sources = append(s.Sources, g.Source)
	}
	return s
}

// collect gathers per-run values for each metric key, plus the first-seen
// metric definition.
func collect(group []*metrics.Summary) (map[string][]float64, map[string]metrics.Metric) {
	vals := map[string][]float64{}
	defs := map[string]metrics.Metric{}
	for _, s := range group {
		for _, m := range s.Metrics {
			vals[m.Key] = append(vals[m.Key], m.Value)
			if _, ok := defs[m.Key]; !ok {
				defs[m.Key] = m
			}
		}
	}
	return vals, defs
}

func compareMetrics(base, cand []*metrics.Summary, opts Options) []MetricDiff {
	bv, bd := collect(base)
	cv, cd := collect(cand)

	keys := map[string]struct{}{}
	for k := range bv {
		keys[k] = struct{}{}
	}
	for k := range cv {
		keys[k] = struct{}{}
	}

	out := make([]MetricDiff, 0, len(keys))
	for k := range keys {
		d := MetricDiff{BaseRuns: bv[k], CandRuns: cv[k]}
		if m, ok := cd[k]; ok {
			d.Metric = m
		} else {
			d.Metric = bd[k]
		}
		d.Base = median(d.BaseRuns)
		d.Cand = median(d.CandRuns)
		d.Metric.Value = d.Cand
		d.Delta = d.Cand - d.Base
		if d.Base != 0 {
			d.Pct = d.Delta / d.Base
		}
		d.Verdict, d.Confidence = verdict(d, opts)
		out = append(out, d)
	}

	sort.Slice(out, func(i, j int) bool {
		gi, gj := groupRank(out[i].Metric.Group), groupRank(out[j].Metric.Group)
		if gi != gj {
			return gi < gj
		}
		if out[i].Metric.Group != out[j].Metric.Group {
			return out[i].Metric.Group < out[j].Metric.Group
		}
		return out[i].Metric.Key < out[j].Metric.Key
	})
	return out
}

func verdict(d MetricDiff, opts Options) (Verdict, string) {
	nb, nc := len(d.BaseRuns), len(d.CandRuns)
	multi := nb >= 2 || nc >= 2

	confidence := "single-run"
	noisy := false
	if multi && nb > 0 && nc > 0 {
		blo, bhi := minMax(d.BaseRuns)
		clo, chi := minMax(d.CandRuns)
		if rangesOverlap(blo, bhi, clo, chi) {
			confidence = "low"
		} else {
			confidence = "high"
		}
		noisy = inRange(d.Cand, blo, bhi) && inRange(d.Base, clo, chi)
	} else if multi {
		confidence = "low"
	}

	switch {
	case d.Metric.Dir == metrics.Neutral:
		return Info, confidence
	case nb == 0:
		return NewMetric, confidence
	case nc == 0:
		return Removed, confidence
	}

	if math.Abs(d.Delta) < opts.AbsFloor[d.Metric.Unit] ||
		(d.Base != 0 && math.Abs(d.Pct) < opts.RelThreshold) {
		return Unchanged, confidence
	}
	if d.Delta == 0 {
		return Unchanged, confidence
	}
	if noisy {
		return Noisy, confidence
	}
	better := d.Delta < 0
	if d.Metric.Dir == metrics.HigherBetter {
		better = d.Delta > 0
	}
	if better {
		return Improved, confidence
	}
	return Regressed, confidence
}

type funcAgg struct {
	name, url   string
	line        int
	self, total []float64
	present     bool
}

// collectFuncs returns per-function per-run values; a run lacking the
// function contributes 0.
func collectFuncs(group []*metrics.Summary) map[string]*funcAgg {
	out := map[string]*funcAgg{}
	for i, s := range group {
		for _, f := range s.Functions {
			k := f.Key()
			a, ok := out[k]
			if !ok {
				a = &funcAgg{
					name: f.Name, url: f.URL, line: f.Line,
					self:  make([]float64, len(group)),
					total: make([]float64, len(group)),
				}
				out[k] = a
			}
			a.self[i] += f.SelfMs
			a.total[i] += f.TotalMs
			a.present = true
		}
	}
	return out
}

func compareFuncs(base, cand []*metrics.Summary, opts Options) (regressed, improved []FuncDiff) {
	bf, cf := collectFuncs(base), collectFuncs(cand)
	keys := map[string]struct{}{}
	for k := range bf {
		keys[k] = struct{}{}
	}
	for k := range cf {
		keys[k] = struct{}{}
	}

	for k := range keys {
		b, c := bf[k], cf[k]
		var d FuncDiff
		switch {
		case b != nil && c != nil:
			d.Name, d.URL, d.Line = c.name, c.url, c.line
		case c != nil:
			d.Name, d.URL, d.Line = c.name, c.url, c.line
		default:
			d.Name, d.URL, d.Line = b.name, b.url, b.line
		}
		if b != nil {
			d.BaseSelf, d.BaseTotal = median(b.self), median(b.total)
		}
		if c != nil {
			d.CandSelf, d.CandTotal = median(c.self), median(c.total)
		}
		d.Delta = d.CandSelf - d.BaseSelf
		if d.BaseSelf != 0 {
			d.Pct = d.Delta / d.BaseSelf
		}

		isNew := b == nil
		isGone := c == nil
		significant := math.Abs(d.Delta) > funcAbsFloorMs &&
			(d.BaseSelf == 0 || math.Abs(d.Pct) >= opts.RelThreshold)
		switch {
		case d.Delta > 0 && (significant || (isNew && d.Delta > funcAbsFloorMs)):
			d.Verdict = Regressed
			if isNew {
				d.Verdict = NewMetric
			}
			regressed = append(regressed, d)
		case d.Delta < 0 && (significant || (isGone && -d.Delta > funcAbsFloorMs)):
			d.Verdict = Improved
			if isGone {
				d.Verdict = Removed
			}
			improved = append(improved, d)
		}
	}

	tie := func(a, b FuncDiff) bool {
		if a.Name != b.Name {
			return a.Name < b.Name
		}
		if a.URL != b.URL {
			return a.URL < b.URL
		}
		return a.Line < b.Line
	}
	sort.Slice(regressed, func(i, j int) bool {
		if regressed[i].Delta != regressed[j].Delta {
			return regressed[i].Delta > regressed[j].Delta
		}
		return tie(regressed[i], regressed[j])
	})
	sort.Slice(improved, func(i, j int) bool {
		if improved[i].Delta != improved[j].Delta {
			return improved[i].Delta < improved[j].Delta
		}
		return tie(improved[i], improved[j])
	})
	if n := opts.TopFunctions; n >= 0 {
		if len(regressed) > n {
			regressed = regressed[:n]
		}
		if len(improved) > n {
			improved = improved[:n]
		}
	}
	return regressed, improved
}

// representative picks the run whose frames.p95_ms is closest to the median
// across runs, falling back to the first run.
func representative(group []*metrics.Summary) *metrics.Summary {
	if len(group) == 0 {
		return nil
	}
	var vals []float64
	for _, s := range group {
		if m, ok := s.Lookup("frames.p95_ms"); ok {
			vals = append(vals, m.Value)
		}
	}
	if len(vals) == 0 {
		return group[0]
	}
	med := median(vals)
	best := group[0]
	bestDist := math.Inf(1)
	for _, s := range group {
		m, ok := s.Lookup("frames.p95_ms")
		if !ok {
			continue
		}
		if d := math.Abs(m.Value - med); d < bestDist {
			best, bestDist = s, d
		}
	}
	return best
}
