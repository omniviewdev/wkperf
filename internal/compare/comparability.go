package compare

import (
	"fmt"
	"math"
	"sort"
	"strings"

	"github.com/omniviewdev/wkperf/internal/metrics"
)

// Warning codes (Warning.Code).
const (
	CodeWindowEmpty        = "window-empty"
	CodeDurationMismatch   = "duration-mismatch"
	CodeFrameCountMismatch = "frame-count-mismatch"
	CodeURLMismatch        = "url-mismatch"
	CodeInstrumentMismatch = "instrument-mismatch"
	CodeWorkloadMismatch   = "workload-mismatch"
	CodeSampleSparse       = "sample-sparse"
	CodeRunVariance        = "run-variance"
)

// Warning levels (Warning.Level).
const (
	LevelWarn = "warn"
	LevelInfo = "info"
)

// Score penalties. Comparability.Score starts at 1 and each warning subtracts
// its weight; the result is clamped at 0. Purely informational warnings cost
// little, warnings that make deltas hard to attribute to performance cost more.
const (
	weightDurationWarn   = 0.25 // durations differ and nothing was normalized
	weightDurationInfo   = 0.05 // durations differ but metrics were rescaled per second
	weightFrameCadence   = 0.05
	weightURL            = 0.15
	weightInstrument     = 0.15
	weightWorkload       = 0.25
	weightSampleSparse   = 0.10
	weightRunVariance    = 0.10 // per group
	cadenceTolerance     = 0.25 // relative frames-per-second difference
	workloadSimilarity   = 0.5  // minimum Jaccard similarity of top-function sets
	minSamples           = 200
	runVarianceMaxCV     = 0.15
	varianceMetricKey    = "frames.p95_ms"
	framesCountMetricKey = "frames.count"
)

// assess judges how comparable two groups are. Checks lacking data are
// skipped; it never panics on empty groups.
func assess(base, cand []*metrics.Summary, normalized bool) Comparability {
	c := Comparability{Score: 1, Normalized: normalized, Warnings: []Warning{}}
	add := func(level, code, msg string, weight float64) {
		c.Warnings = append(c.Warnings, Warning{Level: level, Code: code, Message: msg})
		c.Score -= weight
	}
	windowEmpty := false
	for _, g := range []struct {
		name  string
		group []*metrics.Summary
	}{{"baseline", base}, {"candidate", cand}} {
		empty := len(g.group) == 0
		for _, s := range g.group {
			if s.DurationSec <= 0 {
				empty = true
				break
			}
		}
		if empty {
			add(LevelWarn, CodeWindowEmpty,
				fmt.Sprintf("%s has no runs or a window with zero active duration; select an observed active window", g.name), 1)
			windowEmpty = true
		}
	}
	if windowEmpty {
		c.Score = 0
		return c
	}

	// duration-mismatch
	if bd, ok1 := durations(base); ok1 {
		if cd, ok2 := durations(cand); ok2 {
			b, cn := median(bd), median(cd)
			if durationsDiffer(b, cn) {
				msg := fmt.Sprintf("capture durations differ: baseline %.1fs vs candidate %.1fs (%s)", b, cn, signedPct((cn-b)/b))
				if normalized {
					add(LevelInfo, CodeDurationMismatch, msg+"; totals and counts were compared per second", weightDurationInfo)
				} else {
					add(LevelWarn, CodeDurationMismatch, msg+"; totals and counts are not comparable (use per-second normalization)", weightDurationWarn)
				}
			}
		}
	}

	// frame-count-mismatch: frame cadence, i.e. frames per second of capture.
	if bf, ok1 := framesPerSecond(base); ok1 {
		if cf, ok2 := framesPerSecond(cand); ok2 && bf > 0 {
			if rel := (cf - bf) / bf; math.Abs(rel) > cadenceTolerance {
				add(LevelInfo, CodeFrameCountMismatch,
					fmt.Sprintf("frame cadence differs: %.1f vs %.1f frames/s (%s); the scenario may render at a different rate", bf, cf, signedPct(rel)),
					weightFrameCadence)
			}
		}
	}

	// url-mismatch
	bu, cu := union(base, func(s *metrics.Summary) []string { return s.Fixture.URLs }), union(cand, func(s *metrics.Summary) []string { return s.Fixture.URLs })
	if len(bu) > 0 && len(cu) > 0 && !sameSet(bu, cu) {
		add(LevelWarn, CodeURLMismatch,
			fmt.Sprintf("captures target different URLs (%s)", setDiff(bu, cu)), weightURL)
	}

	// instrument-mismatch
	bi, ci := union(base, func(s *metrics.Summary) []string { return s.Fixture.Instruments }), union(cand, func(s *metrics.Summary) []string { return s.Fixture.Instruments })
	if len(bi) > 0 && len(ci) > 0 && !sameSet(bi, ci) {
		add(LevelWarn, CodeInstrumentMismatch,
			fmt.Sprintf("different instruments were recorded (%s); metrics from the missing instruments are absent on one side", setDiff(bi, ci)),
			weightInstrument)
	}

	// workload-mismatch
	bk, ck := majority(base), majority(cand)
	if len(bk) > 0 && len(ck) > 0 {
		if j := jaccard(bk, ck); j < workloadSimilarity {
			add(LevelWarn, CodeWorkloadMismatch,
				fmt.Sprintf("different code paths exercised (top-function overlap %.0f%%), deltas may reflect workload not performance", j*100),
				weightWorkload)
		}
	}

	// sample-sparse
	for _, g := range []struct {
		name  string
		group []*metrics.Summary
	}{{"baseline", base}, {"candidate", cand}} {
		if n, ok := medianSamples(g.group); ok && n < minSamples {
			add(LevelWarn, CodeSampleSparse,
				fmt.Sprintf("%s has only %.0f profiler samples (<%d); function-level deltas are unreliable", g.name, n, minSamples),
				weightSampleSparse)
		}
	}

	// run-variance
	for _, g := range []struct {
		name  string
		group []*metrics.Summary
	}{{"baseline", base}, {"candidate", cand}} {
		if cv, ok := runCV(g.group, varianceMetricKey); ok && cv > runVarianceMaxCV {
			add(LevelWarn, CodeRunVariance,
				fmt.Sprintf("%s runs are noisy: %s varies %.0f%% between runs (>%.0f%%)", g.name, varianceMetricKey, cv*100, runVarianceMaxCV*100),
				weightRunVariance)
		}
	}

	c.Score = math.Max(0, math.Round(c.Score*1e6)/1e6)
	return c
}

func signedPct(f float64) string {
	return fmt.Sprintf("%+.0f%%", f*100)
}

// framesPerSecond is the median over runs of frames.count / DurationSec;
// false if no run has both.
func framesPerSecond(group []*metrics.Summary) (float64, bool) {
	var vals []float64
	for _, s := range group {
		m, ok := s.Lookup(framesCountMetricKey)
		if !ok || s.DurationSec <= 0 {
			continue
		}
		vals = append(vals, m.Value/s.DurationSec)
	}
	if len(vals) == 0 {
		return 0, false
	}
	return median(vals), true
}

// union collects the distinct non-empty strings returned by get for every run.
func union(group []*metrics.Summary, get func(*metrics.Summary) []string) map[string]bool {
	out := map[string]bool{}
	for _, s := range group {
		for _, v := range get(s) {
			if v != "" {
				out[v] = true
			}
		}
	}
	return out
}

func sameSet(a, b map[string]bool) bool {
	if len(a) != len(b) {
		return false
	}
	for k := range a {
		if !b[k] {
			return false
		}
	}
	return true
}

// setDiff describes the elements present on only one side.
func setDiff(base, cand map[string]bool) string {
	var onlyB, onlyC []string
	for k := range base {
		if !cand[k] {
			onlyB = append(onlyB, k)
		}
	}
	for k := range cand {
		if !base[k] {
			onlyC = append(onlyC, k)
		}
	}
	sort.Strings(onlyB)
	sort.Strings(onlyC)
	var parts []string
	if len(onlyB) > 0 {
		parts = append(parts, "only baseline: "+strings.Join(onlyB, ", "))
	}
	if len(onlyC) > 0 {
		parts = append(parts, "only candidate: "+strings.Join(onlyC, ", "))
	}
	return strings.Join(parts, "; ")
}

// majority returns the top-function keys present in at least half of a
// group's runs, so a single noisy run does not widen the fingerprint.
func majority(group []*metrics.Summary) map[string]bool {
	count := map[string]int{}
	for _, s := range group {
		seen := map[string]bool{}
		for _, k := range s.Fixture.TopFunctionKeys {
			if !seen[k] {
				seen[k] = true
				count[k]++
			}
		}
	}
	out := map[string]bool{}
	for k, n := range count {
		if 2*n >= len(group) {
			out[k] = true
		}
	}
	return out
}

// jaccard returns |a∩b| / |a∪b|; both sets must be non-empty.
func jaccard(a, b map[string]bool) float64 {
	inter := 0
	for k := range a {
		if b[k] {
			inter++
		}
	}
	return float64(inter) / float64(len(a)+len(b)-inter)
}

// medianSamples is the median profiler sample count over runs that carry
// fixture data. Runs with an all-zero fixture (summaries not produced by the
// analyzer) are ignored.
func medianSamples(group []*metrics.Summary) (float64, bool) {
	var vals []float64
	for _, s := range group {
		f := s.Fixture
		if f.SampleCount == 0 && f.Records == 0 && f.Frames == 0 {
			continue
		}
		vals = append(vals, float64(f.SampleCount))
	}
	if len(vals) == 0 {
		return 0, false
	}
	return median(vals), true
}

// runCV is the coefficient of variation (sample stddev / mean) of a metric
// across a group's runs; false with fewer than two runs or a non-positive mean.
func runCV(group []*metrics.Summary, key string) (float64, bool) {
	var vals []float64
	for _, s := range group {
		if m, ok := s.Lookup(key); ok {
			vals = append(vals, m.Value)
		}
	}
	if len(vals) < 2 {
		return 0, false
	}
	var sum float64
	for _, v := range vals {
		sum += v
	}
	mean := sum / float64(len(vals))
	if mean <= 0 {
		return 0, false
	}
	var ss float64
	for _, v := range vals {
		ss += (v - mean) * (v - mean)
	}
	return math.Sqrt(ss/float64(len(vals)-1)) / mean, true
}
