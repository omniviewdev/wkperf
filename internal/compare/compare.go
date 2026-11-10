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
		Normalize:    NormalizeAuto,
	}
}

// Normalize modes for Options.Normalize.
const (
	NormalizeAuto      = "auto"       // per second only when durations differ by more than durationTolerance
	NormalizePerSecond = "per-second" // always
	NormalizeNone      = "none"       // never
)

// durationTolerance is the relative median-duration difference (against the
// baseline) above which auto mode normalizes and a mismatch is reported.
const durationTolerance = 0.10

// normalizedFloorScale shrinks absolute floors for per-second values, which
// are about 10x smaller than the totals they were derived from on typical
// 5-30 s captures.
const normalizedFloorScale = 0.1

// normalizeMode resolves the effective mode; an empty value means auto.
func normalizeMode(o Options) string {
	switch o.Normalize {
	case NormalizePerSecond, NormalizeNone:
		return o.Normalize
	}
	return NormalizeAuto
}

// durations returns every run's DurationSec, and false if any is not positive
// (per-second rescaling would be meaningless).
func durations(group []*metrics.Summary) ([]float64, bool) {
	out := make([]float64, 0, len(group))
	for _, s := range group {
		if s.DurationSec <= 0 {
			return nil, false
		}
		out = append(out, s.DurationSec)
	}
	return out, len(out) > 0
}

// durationsDiffer reports whether the median durations differ by more than
// durationTolerance, relative to the baseline.
func durationsDiffer(bd, cd float64) bool {
	return bd > 0 && math.Abs(cd-bd)/bd > durationTolerance
}

// shouldNormalize decides whether Extensive metrics and function times are
// rescaled per second.
func shouldNormalize(base, cand []*metrics.Summary, opts Options) bool {
	mode := normalizeMode(opts)
	if mode == NormalizeNone {
		return false
	}
	bd, ok1 := durations(base)
	cd, ok2 := durations(cand)
	if !ok1 || !ok2 {
		return false
	}
	return mode == NormalizePerSecond || durationsDiffer(median(bd), median(cd))
}

// perSecond returns v scaled by the run duration when norm is set.
func perSecond(v float64, s *metrics.Summary, norm bool) float64 {
	if norm {
		return v / s.DurationSec
	}
	return v
}

// normalizedLabel appends the per-second suffix to a metric label.
func normalizedLabel(label string, u metrics.Unit) string {
	if u == metrics.UnitMs {
		return label + " (ms per s)"
	}
	return label + " /s"
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
	norm := shouldNormalize(base, cand, opts)
	res.Comparability = assess(base, cand, norm)
	res.Metrics = compareMetrics(base, cand, opts, norm)
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
	res.FuncRegressed, res.FuncImproved = compareFuncs(base, cand, opts, norm)
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
// metric definition. With norm, Extensive values are divided by the run's
// DurationSec.
func collect(group []*metrics.Summary, norm bool) (map[string][]float64, map[string]metrics.Metric) {
	vals := map[string][]float64{}
	defs := map[string]metrics.Metric{}
	for _, s := range group {
		for _, m := range s.Metrics {
			v := m.Value
			if m.Extensive {
				v = perSecond(v, s, norm)
			}
			vals[m.Key] = append(vals[m.Key], v)
			if _, ok := defs[m.Key]; !ok {
				defs[m.Key] = m
			}
		}
	}
	return vals, defs
}

func compareMetrics(base, cand []*metrics.Summary, opts Options, norm bool) []MetricDiff {
	bv, bd := collect(base, norm)
	cv, cd := collect(cand, norm)

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
		scaled := norm && d.Metric.Extensive
		if scaled {
			d.Metric.Label = normalizedLabel(d.Metric.Label, d.Metric.Unit)
		}
		d.Base = median(d.BaseRuns)
		d.Cand = median(d.CandRuns)
		d.Metric.Value = d.Cand
		d.Delta = d.Cand - d.Base
		if d.Base != 0 {
			d.Pct = d.Delta / d.Base
		}
		d.Verdict, d.Confidence = verdict(d, opts, scaled)
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

// verdict classifies d. scaled marks per-second values, which use shrunken
// absolute floors.
func verdict(d MetricDiff, opts Options, scaled bool) (Verdict, string) {
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

	floor := opts.AbsFloor[d.Metric.Unit]
	if scaled {
		floor *= normalizedFloorScale
	}
	if math.Abs(d.Delta) < floor ||
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
	function    metrics.FuncStat
	source      string
	self, total []float64
}

// collectFuncs returns per-function per-run values; a run lacking the
// function contributes 0. With norm, times are ms per second of the run.
func collectFuncs(group []*metrics.Summary, norm bool) map[string]*funcAgg {
	out := map[string]*funcAgg{}
	for i, s := range group {
		for _, f := range s.Functions {
			k := f.Key()
			a, ok := out[k]
			if !ok {
				a = &funcAgg{
					function: f,
					source:   s.Source,
					self:     make([]float64, len(group)),
					total:    make([]float64, len(group)),
				}
				out[k] = a
			}
			a.self[i] += perSecond(f.SelfMs, s, norm)
			a.total[i] += perSecond(f.TotalMs, s, norm)
		}
	}
	return out
}

func compareFuncs(base, cand []*metrics.Summary, opts Options, norm bool) (regressed, improved []FuncDiff) {
	bf, cf := collectFuncs(base, norm), collectFuncs(cand, norm)
	floor := funcAbsFloorMs
	if norm {
		floor *= normalizedFloorScale
	}
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
		case c != nil:
			d.Name, d.URL, d.Line = c.function.Name, c.function.URL, c.function.Line
			d.Column, d.SourceID, d.Original = c.function.Column, c.function.SourceID, c.function.Original
			d.ID, d.CommonStack = c.function.ID, c.function.CommonStack
			d.CommonStackID, d.CommonStackSource = c.function.CommonStackID, c.source
		default:
			d.Name, d.URL, d.Line = b.function.Name, b.function.URL, b.function.Line
			d.Column, d.SourceID, d.Original = b.function.Column, b.function.SourceID, b.function.Original
			d.ID, d.CommonStack = b.function.ID, b.function.CommonStack
			d.CommonStackID, d.CommonStackSource = b.function.CommonStackID, b.source
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
		significant := math.Abs(d.Delta) > floor &&
			(d.BaseSelf == 0 || math.Abs(d.Pct) >= opts.RelThreshold)
		switch {
		case d.Delta > 0 && (significant || (isNew && d.Delta > floor)):
			d.Verdict = Regressed
			if isNew {
				d.Verdict = NewMetric
			}
			regressed = append(regressed, d)
		case d.Delta < 0 && (significant || (isGone && -d.Delta > floor)):
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
		if a.Line != b.Line {
			return a.Line < b.Line
		}
		if a.Column != b.Column {
			return a.Column < b.Column
		}
		return a.SourceID < b.SourceID
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
