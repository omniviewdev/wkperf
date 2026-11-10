package insights

import (
	"sort"

	"github.com/omniviewdev/wkperf/internal/model"
	"github.com/omniviewdev/wkperf/internal/profile"
)

type span struct{ s, e float64 }

// mergeSpans returns the sorted union of in as disjoint spans.
func mergeSpans(in []span) []span {
	if len(in) == 0 {
		return nil
	}
	s := append([]span(nil), in...)
	sort.Slice(s, func(i, j int) bool { return s[i].s < s[j].s })
	out := []span{s[0]}
	for _, x := range s[1:] {
		last := &out[len(out)-1]
		if x.s <= last.e {
			if x.e > last.e {
				last.e = x.e
			}
			continue
		}
		out = append(out, x)
	}
	return out
}

func isStyleEvent(t string) bool { return t == "recalculate-styles" }

// computeBreakdown partitions active recording time without treating sparse
// rendering-frame cadence as work. Idle-gap frames contribute only their
// measured work; their unused time remains Idle.
func computeBreakdown(rec *model.Recording) []Slice {
	work := profile.Measure(rec, rec.StartTime, rec.EndTime)
	wallMs := profile.ActiveDuration(rec, rec.StartTime, rec.EndTime) * 1000
	const unattributed = "Frame time not attributed to work"
	values := map[string]float64{
		"Script": work.ScriptMs, "Style": work.StyleMs, "Layout": work.LayoutMs,
		"Paint": work.PaintMs, "Composite": work.CompositeMs, "GC": work.GCMs,
	}
	var activeFrames []span
	for _, f := range profile.Frames(rec, rec.StartTime, rec.EndTime) {
		if !f.IdleGap {
			activeFrames = append(activeFrames, span{f.Start, f.End})
		}
	}
	for _, f := range mergeSpans(activeFrames) {
		occupancy := profile.Measure(rec, f.s, f.e)
		unused := profile.ActiveDuration(rec, f.s, f.e)*1000 - occupancy.WorkMs
		if unused > 0 {
			values[unattributed] += unused
		}
	}
	idle := wallMs - work.WorkMs - values[unattributed]
	if idle < 0 {
		idle = 0
	}
	values["Idle"] = idle
	out := make([]Slice, 0, 8)
	for _, name := range []string{"Script", "Style", "Layout", "Paint", "Composite", "GC", unattributed, "Idle"} {
		pct := 0.0
		if wallMs > 0 {
			pct = values[name] / wallMs * 100
		}
		out = append(out, Slice{Name: name, Ms: values[name], Pct: pct})
	}
	return out
}
