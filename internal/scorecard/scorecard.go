package scorecard

import (
	"encoding/json"
	"fmt"
	"math"
	"sort"
	"strings"

	"github.com/omniviewdev/wkperf/internal/insights"
	"github.com/omniviewdev/wkperf/internal/metrics"
	"github.com/omniviewdev/wkperf/internal/model"
	"github.com/omniviewdev/wkperf/internal/profile"
)

// Scoring model
//
// Each Result gets a 0..100 score that is piecewise linear in the value:
//
//	at or better than Good        -> 100
//	Good .. Poor (needs-improvement band) -> 100 down to 50
//	Poor .. Poor+(Poor-Good)      -> 50 down to 0 (same slope as the band above)
//	at or worse than that         -> 0
//
// For HigherBetter standards the axis is mirrored. The score is therefore
// monotonic in the value and always clamped to 0..100, and a Poor rating
// always scores below 50. The overall score is the Weight-weighted mean of
// the available (non-Unavailable) results. Weights: frame-budget metrics
// (frame-p95, jank-ratio, fps) weigh 3, responsiveness metrics (tbt, inp,
// long-task-rate) weigh 2, everything else weighs 1.

const (
	longTaskMs    = 50.0
	tbtWindowSec  = 10.0
	inpMinForP98  = 50
	maxFactor     = 1000.0
	secondsPerMin = 60.0
	minMemSamples = 5
)

var interactionEvents = map[string]bool{
	"click": true, "pointerdown": true, "pointerup": true, "keydown": true, "keyup": true,
	"mousedown": true, "mouseup": true, "input": true, "change": true, "submit": true,
	"touchstart": true, "touchend": true,
}

// Standards returns the rated metrics in display order.
func Standards() []Standard {
	return []Standard{
		{ID: "frame-p95", Name: "Frame time p95", Source: "frame budget (60Hz)", Unit: metrics.UnitMs, Good: 16.7, Poor: 33.4, Weight: 3,
			Note: "95th percentile of active rendering-frame durations, excluding idle gaps and paused spans; 16.7ms is one 60Hz frame."},
		{ID: "jank-ratio", Name: "Jank ratio", Source: "frame budget (60Hz)", Unit: metrics.UnitRatio, Good: 0.05, Poor: 0.25, Weight: 3,
			Note: "Share of active rendering frames longer than 16.7ms; idle-gap frames and paused spans are excluded. Thresholds are wkperf choices."},
		{ID: "fps", Name: "Average FPS", Source: "frame budget (60Hz)", Unit: metrics.UnitFPS, Good: 55, Poor: 30, HigherBetter: true, Weight: 3,
			Note: "1000 / mean active frame duration, not display refresh rate. Idle-gap frames are excluded; an all-idle capture has no FPS grade."},
		{ID: "tbt", Name: "Total Blocking Time", Source: "Lighthouse", Unit: metrics.UnitMs, Good: 200, Poor: 600, Weight: 2,
			Note: "Adapted: Lighthouse measures TBT between FCP and TTI of a page load. Here it is the sum of (duration-50ms) over script records longer than 50ms, normalized to ms per 10 seconds of recording so captures of different lengths compare."},
		{ID: "inp", Name: "Interaction latency (INP approx.)", Source: "Core Web Vitals", Unit: metrics.UnitMs, Good: 200, Poor: 500, Weight: 2,
			Note: "Approximation of INP: p98 (max when fewer than 50 events) of event-dispatched script durations for click/pointer/key/input/touch events. Measures handler time only; excludes input delay and presentation delay, so real INP is higher."},
		{ID: "long-task-rate", Name: "Long tasks per minute", Source: "wkperf heuristic", Unit: metrics.UnitCount, Good: 3, Poor: 12, Weight: 2,
			Note: "Script records longer than 50ms (garbage collection excluded) per minute of recording."},
		{ID: "fcp", Name: "First Contentful Paint", Source: "Lighthouse", Unit: metrics.UnitMs, Good: 1800, Poor: 3000, Weight: 1,
			Note: "From the first-contentful-paint layout record relative to recording start. Page-load metric: only meaningful if the recording covers a load."},
		{ID: "lcp", Name: "Largest Contentful Paint", Source: "Core Web Vitals", Unit: metrics.UnitMs, Good: 2500, Poor: 4000, Weight: 1,
			Note: "From the largest-contentful-paint layout record relative to recording start. Page-load metric: only meaningful if the recording covers a load."},
		{ID: "main-busy", Name: "Main thread script busy", Source: "wkperf heuristic", Unit: metrics.UnitRatio, Good: 0.25, Poor: 0.5, Weight: 1,
			Note: "Script time as a share of wall time. Thresholds are wkperf choices: a main thread busy more than half the time leaves little headroom for input and rendering."},
		{ID: "gc-share", Name: "GC share of wall time", Source: "wkperf heuristic", Unit: metrics.UnitRatio, Good: 0.03, Poor: 0.10, Weight: 1,
			Note: "Total garbage-collection time over wall time. Thresholds are wkperf choices."},
		{ID: "memory-growth", Name: "Memory growth", Source: "wkperf heuristic", Unit: metrics.UnitMB, Good: 1, Poor: 10, Weight: 1,
			Note: "MB per minute, slope of a linear regression over total memory samples; needs at least 5 samples. A heuristic for leaks in long sessions."},
		{ID: "forced-layout-rate", Name: "Forced layouts per second", Source: "wkperf heuristic", Unit: metrics.UnitCount, Good: 0.5, Poor: 5, Weight: 1,
			Note: "Explicit forced-layout records plus layout records starting inside script spans, per active second. Rendering that merely follows script is not forced layout."},
	}
}

type measure struct {
	v      float64
	detail string
}

// Evaluate rates one recording. rep supplies the frame, GC, memory and
// script-busy numbers already computed by the analyzer; everything else is
// derived from rec. rep may be nil, in which case summary-based standards
// are unavailable.
func Evaluate(rec *model.Recording, rep *insights.Report) *Scorecard {
	var sum *metrics.Summary
	dur := 0.0
	if rep != nil {
		sum = rep.Summary
		dur = rep.DurationSec
	}
	if dur <= 0 && rec != nil {
		dur = profile.ActiveDuration(rec, rec.StartTime, rec.EndTime)
	}
	m := map[string]measure{}
	lookup := func(id, key string) {
		if sum == nil {
			return
		}
		if x, ok := sum.Lookup(key); ok {
			m[id] = measure{v: x.Value}
		}
	}
	lookup("frame-p95", "frames.p95_ms")
	lookup("fps", "frames.fps")
	lookup("jank-ratio", "frames.jank_ratio")
	lookup("main-busy", "script.busy_ratio")
	if sum != nil && dur > 0 && instrumentAvailable(rec, model.TypeScript) {
		if x, ok := sum.Lookup("gc.total_ms"); ok {
			m["gc-share"] = measure{v: x.Value / 1000 / dur, detail: fmt.Sprintf("%.0fms GC", x.Value)}
		} else {
			m["gc-share"] = measure{v: 0, detail: "no GC recorded"}
		}
	}
	if sum != nil {
		if v, n, ok := memoryGrowth(sum.MemoryMB); ok {
			m["memory-growth"] = measure{v: v, detail: fmt.Sprintf("%d memory samples", n)}
		}
	}
	if rec != nil {
		deriveFromRecords(rec, dur, m)
	}
	return build(m)
}

func deriveFromRecords(rec *model.Recording, dur float64, m map[string]measure) {
	var tbt, worst float64
	long := 0
	var inp []float64
	names := map[string]bool{}
	layouts := 0
	forced := len(profile.ForcedLayoutRecords(rec))
	fcp, lcp := math.Inf(1), math.Inf(1)
	for i := range rec.Records {
		r := &rec.Records[i]
		if !profile.Observed(rec, *r) {
			continue
		}
		switch r.Type {
		case model.TypeScript:
			if profile.IsGC(*r) {
				continue
			}
			d := profile.ActiveDuration(rec, r.StartTime, r.EndTime) * 1000
			if d > longTaskMs {
				long++
				tbt += d - longTaskMs
				worst = math.Max(worst, d)
			}
			if r.EventType == "event-dispatched" {
				if name := detailName(r); interactionEvents[name] {
					inp = append(inp, d)
					names[name] = true
				}
			}
		case model.TypeLayout:
			layouts++
			t := (r.StartTime - rec.StartTime) * 1000
			switch r.EventType {
			case "first-contentful-paint":
				fcp = math.Min(fcp, t)
			case "largest-contentful-paint":
				lcp = math.Min(lcp, t)
			}
		}
	}
	if !math.IsInf(fcp, 1) {
		m["fcp"] = measure{v: fcp}
	}
	if !math.IsInf(lcp, 1) {
		m["lcp"] = measure{v: lcp}
	}
	if len(inp) > 0 {
		sort.Float64s(inp)
		label := strings.Join(sortedKeys(names), "/")
		if len(inp) < inpMinForP98 {
			m["inp"] = measure{v: inp[len(inp)-1], detail: fmt.Sprintf("max of %d %s handlers", len(inp), label)}
		} else {
			idx := int(math.Ceil(0.98*float64(len(inp)))) - 1
			m["inp"] = measure{v: inp[idx], detail: fmt.Sprintf("p98 of %d %s handlers", len(inp), label)}
		}
	}
	if dur > 0 {
		if instrumentAvailable(rec, model.TypeScript) {
			m["tbt"] = measure{v: tbt / dur * tbtWindowSec, detail: fmt.Sprintf("%d long tasks, worst %.0fms", long, worst)}
			m["long-task-rate"] = measure{v: float64(long) / dur * secondsPerMin, detail: fmt.Sprintf("%d long tasks in %.1fs", long, dur)}
		}
		if layouts > 0 || instrumentAvailable(rec, model.TypeLayout) {
			m["forced-layout-rate"] = measure{v: float64(forced) / dur, detail: fmt.Sprintf("%d synchronous layouts (explicit or starting inside script)", forced)}
		}
	}
}

func detailName(r *model.Record) string {
	var s string
	if len(r.Details) > 0 && json.Unmarshal(r.Details, &s) == nil {
		return s
	}
	return ""
}

func instrumentAvailable(rec *model.Recording, kind string) bool {
	if rec == nil {
		return false
	}
	for _, r := range rec.Records {
		if r.Type == kind && profile.Observed(rec, r) {
			return true
		}
	}
	for _, instrument := range rec.InstrumentTypes {
		name := strings.ToLower(instrument)
		if name == kind {
			return true
		}
		if kind == model.TypeScript && (strings.Contains(name, "script") || strings.Contains(name, "javascript")) {
			return true
		}
		if kind == model.TypeLayout && (strings.Contains(name, "layout") || strings.Contains(name, "rendering")) {
			return true
		}
	}
	return false
}

func sortedKeys(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// memoryGrowth returns the least-squares slope of the series in MB/minute.
func memoryGrowth(pts []metrics.Point) (slope float64, n int, ok bool) {
	n = len(pts)
	if n < minMemSamples {
		return 0, n, false
	}
	var st, sv, stt, stv float64
	for _, p := range pts {
		st += p.T
		sv += p.V
		stt += p.T * p.T
		stv += p.T * p.V
	}
	fn := float64(n)
	den := fn*stt - st*st
	if den == 0 {
		return 0, n, false
	}
	return (fn*stv - st*sv) / den * secondsPerMin, n, true
}

// FromValues builds a Scorecard from standard-ID -> value. IDs absent from
// values are Unavailable.
func FromValues(values map[string]float64) *Scorecard {
	m := map[string]measure{}
	for id, v := range values {
		m[id] = measure{v: v}
	}
	return build(m)
}

// Values returns available standard-ID -> value.
func Values(c *Scorecard) map[string]float64 {
	out := map[string]float64{}
	if c == nil {
		return out
	}
	for _, r := range c.Results {
		if r.Rating != Unavailable {
			out[r.ID] = r.Value
		}
	}
	return out
}

func build(m map[string]measure) *Scorecard {
	c := &Scorecard{}
	var wsum, ssum float64
	for _, s := range Standards() {
		r := Result{Standard: s, Rating: Unavailable}
		if x, ok := m[s.ID]; ok && !math.IsNaN(x.v) {
			r.Value, r.Detail = x.v, x.detail
			r.Rating = rate(s, x.v)
			r.Score = score(s, x.v)
			wsum += s.Weight
			ssum += s.Weight * r.Score
			if r.Rating != Good {
				c.Gap = append(c.Gap, GapItem{ID: s.ID, Name: s.Name, Value: x.v, Target: s.Good, Rating: r.Rating, Factor: factor(s, x.v)})
			}
		}
		c.Results = append(c.Results, r)
	}
	if wsum > 0 {
		c.Score = ssum / wsum
	}
	c.Grade = grade(c.Score)
	sort.SliceStable(c.Gap, func(i, j int) bool {
		if c.Gap[i].Factor != c.Gap[j].Factor {
			return c.Gap[i].Factor > c.Gap[j].Factor
		}
		return c.Gap[i].ID < c.Gap[j].ID
	})
	return c
}

func rate(s Standard, v float64) Rating {
	if s.HigherBetter {
		switch {
		case v >= s.Good:
			return Good
		case v < s.Poor:
			return Poor
		}
		return NeedsImprovement
	}
	switch {
	case v <= s.Good:
		return Good
	case v > s.Poor:
		return Poor
	}
	return NeedsImprovement
}

// score implements the piecewise-linear model documented at the top.
func score(s Standard, v float64) float64 {
	// d is the distance past Good in the "worse" direction, in units where
	// Good->Poor spans 1.
	span := math.Abs(s.Poor - s.Good)
	var d float64
	if s.HigherBetter {
		d = (s.Good - v) / span
	} else {
		d = (v - s.Good) / span
	}
	if d <= 0 {
		return 100
	}
	return math.Max(0, 100-50*d)
}

func factor(s Standard, v float64) float64 {
	var f float64
	if s.HigherBetter {
		if v <= 0 {
			return maxFactor
		}
		f = s.Good / v
	} else {
		f = v / s.Good
	}
	return math.Min(f, maxFactor)
}

func grade(score float64) string {
	switch {
	case score >= 90:
		return "A"
	case score >= 75:
		return "B"
	case score >= 60:
		return "C"
	case score >= 40:
		return "D"
	}
	return "F"
}
