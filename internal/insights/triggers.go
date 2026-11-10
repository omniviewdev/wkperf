package insights

import (
	"math"
	"sort"

	"github.com/omniviewdev/wkperf/internal/analyze"
	"github.com/omniviewdev/wkperf/internal/flame"
	"github.com/omniviewdev/wkperf/internal/metrics"
	"github.com/omniviewdev/wkperf/internal/model"
	"github.com/omniviewdev/wkperf/internal/profile"
)

type triggerBucket struct {
	kind, name string
	durations  []float64
	intervals  []profile.Interval
}

func triggerKind(event string) string {
	switch event {
	case "event-dispatched":
		return "dispatch"
	case "timer-fired":
		return "timer"
	case "animation-frame-fired":
		return "rAF"
	case "microtask-fired":
		return "microtask"
	}
	return ""
}

func triggerName(r *model.Record) string {
	if name := detailString(r); name != "" {
		return name
	}
	return r.EventType
}

func addTrigger(buckets map[string]*triggerBucket, kind, name string, ms float64, interval profile.Interval) {
	key := kind + "\x00" + name
	b := buckets[key]
	if b == nil {
		b = &triggerBucket{kind: kind, name: name}
		buckets[key] = b
	}
	b.durations = append(b.durations, ms)
	if interval.End > interval.Start {
		b.intervals = append(b.intervals, interval)
	}
}

func finishTriggers(rec *model.Recording, tree *flame.Tree, buckets map[string]*triggerBucket, top int) []TriggerStat {
	if top <= 0 {
		top = 10
	}
	out := make([]TriggerStat, 0, len(buckets))
	for _, b := range buckets {
		sort.Float64s(b.durations)
		t := TriggerStat{Kind: b.kind, Name: b.name, Count: len(b.durations), TopSelf: []metrics.FuncStat{}, TopInclusive: []metrics.FuncStat{}}
		for _, ms := range b.durations {
			t.TotalMs += ms
			t.MaxMs = math.Max(t.MaxMs, ms)
		}
		if t.Count > 0 {
			t.MeanMs = t.TotalMs / float64(t.Count)
			t.P95Ms = analyze.Percentile(b.durations, 95)
		}
		functions := analyze.FunctionsInIntervalsWithTree(rec, tree, b.intervals)
		for _, f := range functions {
			if f.SelfMs > 0 && len(t.TopSelf) < top {
				t.TopSelf = append(t.TopSelf, f)
			}
		}
		sort.Slice(functions, func(i, j int) bool {
			if functions[i].TotalMs != functions[j].TotalMs {
				return functions[i].TotalMs > functions[j].TotalMs
			}
			return functions[i].Key() < functions[j].Key()
		})
		for _, f := range functions {
			if f.TotalMs > 0 && len(t.TopInclusive) < top {
				t.TopInclusive = append(t.TopInclusive, f)
			}
		}
		if len(t.TopInclusive) == 0 {
			t.SampleMessage = "No samples in this trigger's intervals"
		}
		out = append(out, t)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].TotalMs != out[j].TotalMs {
			return out[i].TotalMs > out[j].TotalMs
		}
		if out[i].Kind != out[j].Kind {
			return out[i].Kind < out[j].Kind
		}
		return out[i].Name < out[j].Name
	})
	return out
}

// TriggerStats reports every dispatch name and timer/rAF/microtask trigger.
// Sampling attribution depends only on timestamps within trigger intervals.
func TriggerStats(rec *model.Recording, top int) []TriggerStat {
	return triggerStatsWithTree(rec, flame.Build(rec), top)
}

func triggerStatsWithTree(rec *model.Recording, tree *flame.Tree, top int) []TriggerStat {
	buckets := map[string]*triggerBucket{}
	if rec == nil {
		return []TriggerStat{}
	}
	for i := range rec.Records {
		r := &rec.Records[i]
		if !profile.Observed(rec, *r) {
			continue
		}
		kind := triggerKind(r.EventType)
		if r.Type != model.TypeScript || kind == "" || profile.IsGC(*r) {
			continue
		}
		s, e := math.Max(r.StartTime, rec.StartTime), math.Min(r.EndTime, rec.EndTime)
		if e < s || (e > s && profile.ActiveDuration(rec, s, e) <= 0) {
			continue
		}
		addTrigger(buckets, kind, triggerName(r), profile.ActiveDuration(rec, s, e)*1000, profile.Interval{Start: s, End: e})
	}
	return finishTriggers(rec, tree, buckets, top)
}

func idleWakeups(rec *model.Recording, tree *flame.Tree, top int) WakeupStats {
	out := WakeupStats{Causes: []TriggerStat{}}
	buckets := map[string]*triggerBucket{}
	for _, f := range profile.Frames(rec, rec.StartTime, rec.EndTime) {
		if !f.IdleGap || f.Occupancy.WorkMs <= 0 {
			continue
		}
		out.Count++
		var best *model.Record
		bestMs := -1.0
		for i := range rec.Records {
			r := &rec.Records[i]
			if !profile.Observed(rec, *r) {
				continue
			}
			if r.Type != model.TypeScript || profile.IsGC(*r) || triggerKind(r.EventType) == "" || r.StartTime < f.Start || r.StartTime >= f.End {
				continue
			}
			ms := overlap(f.Start, f.End, r.StartTime, r.EndTime) * 1000
			if ms > bestMs || (ms == bestMs && (best == nil || eventLabel(r) < eventLabel(best))) {
				best, bestMs = r, ms
			}
		}
		kind, name := "rendering", "rendering work; no script trigger recorded"
		interval := profile.Interval{Start: f.Start, End: f.End}
		if best != nil {
			kind, name = triggerKind(best.EventType), triggerName(best)
			interval = profile.Interval{Start: math.Max(f.Start, best.StartTime), End: math.Min(f.End, best.EndTime)}
		}
		addTrigger(buckets, kind, name, f.Occupancy.WorkMs, interval)
	}
	if duration := profile.ActiveDuration(rec, rec.StartTime, rec.EndTime); duration > 0 {
		out.PerSecond = float64(out.Count) / duration
	}
	out.Causes = finishTriggers(rec, tree, buckets, top)
	return out
}

func sampleCoverage(rec *model.Recording, sum *metrics.Summary) SampleCoverage {
	c := SampleCoverage{SampleCount: sum.Fixture.SampleCount, Interpretation: "Sample weights estimate execution time; coverage is not a causal attribution."}
	for _, f := range sum.Functions {
		c.SampledMs += f.SelfMs
	}
	c.ScriptMs = profile.Measure(rec, rec.StartTime, rec.EndTime).ScriptMs
	if c.ScriptMs > 0 {
		c.Available = true
		c.Ratio = c.SampledMs / c.ScriptMs
	} else {
		c.Interpretation = "No measured script intervals; sample-to-script coverage is unavailable. Timestamp samples still attribute frames and triggers."
	}
	return c
}
