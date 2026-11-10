package insights

import (
	"sort"

	"github.com/omniviewdev/wkperf/internal/model"
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

func spanLen(in []span) float64 {
	t := 0.0
	for _, x := range in {
		t += x.e - x.s
	}
	return t
}

// isStyleEvent etc. classify layout-record event types.
func isStyleEvent(t string) bool  { return t == "recalculate-styles" }
func isLayoutEvent(t string) bool { return t == "layout" || t == "forced-layout" }
func isPaintEvent(t string) bool  { return t == "paint" }

// computeBreakdown builds the exclusive wall-time breakdown.
//
// Algorithm: for each category, collect its record intervals clipped to
// [StartTime, EndTime] and merge overlaps. Categories are then claimed in
// priority order GC > Script > Style > Layout > Paint > Composite > Other:
// each category's exclusive time is |union(claimed, cat)| - |claimed|, so any
// instant is credited to exactly one category (the highest-priority one
// active). Other is time inside rendering frames not claimed by any category.
// Idle is the wall time left unclaimed, so slices sum to 100%.
func computeBreakdown(rec *model.Recording) []Slice {
	start, end := rec.StartTime, rec.EndTime
	wall := end - start
	cats := map[string][]span{}
	add := func(name string, r *model.Record) {
		s, e := r.StartTime, r.EndTime
		if s < start {
			s = start
		}
		if e > end {
			e = end
		}
		if e > s {
			cats[name] = append(cats[name], span{s, e})
		}
	}
	for i := range rec.Records {
		r := &rec.Records[i]
		switch r.Type {
		case model.TypeScript:
			if recIsGC(r) {
				add("GC", r)
			} else {
				add("Script", r)
			}
		case model.TypeLayout:
			switch {
			case isStyleEvent(r.EventType):
				add("Style", r)
			case isLayoutEvent(r.EventType):
				add("Layout", r)
			case isPaintEvent(r.EventType):
				add("Paint", r)
			case r.EventType == "composite":
				add("Composite", r)
			}
		case model.TypeRenderingFrame:
			add("Other", r)
		}
	}
	excl := map[string]float64{}
	var claimed []span
	for _, name := range []string{"GC", "Script", "Style", "Layout", "Paint", "Composite", "Other"} {
		if len(cats[name]) == 0 {
			continue
		}
		u := mergeSpans(append(append([]span(nil), claimed...), cats[name]...))
		excl[name] = spanLen(u) - spanLen(claimed)
		claimed = u
	}
	idle := wall - spanLen(claimed)
	if idle < 0 || wall <= 0 {
		idle = 0
	}
	excl["Idle"] = idle
	var out []Slice
	for _, name := range []string{"Script", "Style", "Layout", "Paint", "Composite", "GC", "Other", "Idle"} {
		ms := excl[name] * 1000
		pct := 0.0
		if wall > 0 {
			pct = excl[name] / wall * 100
		}
		out = append(out, Slice{Name: name, Ms: ms, Pct: pct})
	}
	return out
}
