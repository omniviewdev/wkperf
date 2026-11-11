package rootcause

import (
	"fmt"
	"math"
	"sort"
	"strings"

	"github.com/omniviewdev/wkperf/internal/flame"
	"github.com/omniviewdev/wkperf/internal/model"
	"github.com/omniviewdev/wkperf/internal/profile"
)

func frameNames(f flame.Frame) []string {
	if f.Original != nil && f.Original.Name != "" && f.Original.Name != f.Name {
		return []string{f.Name, f.Original.Name}
	}
	return []string{f.Name}
}

func syncReact(frames []flame.Frame) bool {
	for _, f := range frames {
		if f.Category != flame.CategoryFramework {
			continue
		}
		for _, name := range frameNames(f) {
			if name == "performSyncWorkOnRoot" || strings.HasPrefix(name, "flushSync") {
				return true
			}
		}
	}
	return false
}

func commitFrame(f flame.Frame) bool {
	if f.Category != flame.CategoryFramework {
		return false
	}
	for _, name := range frameNames(f) {
		if strings.HasPrefix(name, "commit") || strings.HasPrefix(name, "completeRoot") || strings.HasPrefix(name, "flush") && strings.HasSuffix(name, "Effects") || strings.HasPrefix(name, "recursivelyTraverse") && strings.Contains(name, "Effect") {
			return true
		}
	}
	return false
}

func renderFrame(f flame.Frame) bool {
	if f.Category != flame.CategoryFramework {
		return false
	}
	for _, name := range frameNames(f) {
		if strings.HasPrefix(name, "renderRoot") {
			return true
		}
	}
	return false
}

func isInteraction(r model.Record) bool {
	if r.Type != model.TypeScript || profile.IsGC(r) || profile.CountOnly(r.EventType) {
		return false
	}
	return profile.IsInputDriven(r) || r.EventType == "timer-fired" || r.EventType == "animation-frame-fired"
}

func (b builder) interactionWindows() []WindowRef {
	var out []WindowRef
	seen := map[string]bool{}
	for _, r := range b.rec.Records {
		if !isInteraction(r) {
			continue
		}
		for _, part := range b.active(r.StartTime-b.rec.StartTime, r.EndTime-b.rec.StartTime) {
			name := eventName(r)
			key := digest(name, decimal(part.Start), decimal(part.End))
			if !seen[key] {
				seen[key] = true
				out = append(out, WindowRef{FromSec: b.exportTime(part.Start), ToSec: b.exportTime(part.End), Event: name, Command: b.query("stacks", part.Start, part.End)})
			}
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].FromSec != out[j].FromSec {
			return out[i].FromSec < out[j].FromSec
		}
		if out[i].ToSec != out[j].ToSec {
			return out[i].ToSec < out[j].ToSec
		}
		return out[i].Event < out[j].Event
	})
	return out
}

func union(in []flame.Interval) []flame.Interval {
	sort.Slice(in, func(i, j int) bool {
		if in[i].Start != in[j].Start {
			return in[i].Start < in[j].Start
		}
		return in[i].End < in[j].End
	})
	out := make([]flame.Interval, 0, len(in))
	for _, v := range in {
		if v.End <= v.Start {
			continue
		}
		if len(out) > 0 && v.Start <= out[len(out)-1].End {
			out[len(out)-1].End = math.Max(out[len(out)-1].End, v.End)
		} else {
			out = append(out, v)
		}
	}
	return out
}

func nativeFlush(f flame.Frame) (api, flush string) {
	if f.Category != flame.CategoryNative {
		return "", ""
	}
	for _, name := range frameNames(f) {
		name = strings.TrimPrefix(name, "get ")
		if i := strings.LastIndexAny(name, ".:"); i >= 0 {
			name = name[i+1:]
		}
		switch name {
		case "getAnimations":
			return name, "style"
		case "getComputedStyle":
			// The queried property is not present in a sampler frame. Some
			// properties need only style, while geometry can also force layout.
			return name, "unknown"
		case "getBoundingClientRect", "getClientRects", "offsetTop", "offsetLeft", "offsetWidth", "offsetHeight", "offsetParent", "clientTop", "clientLeft", "clientWidth", "clientHeight", "scrollTop", "scrollLeft", "scrollWidth", "scrollHeight":
			return name, "layout"
		}
	}
	return "", ""
}

type nativeReadEvidence struct {
	read       NativeRead
	lastSample int
	style      []flame.Interval
	layout     []flame.Interval
	styleSeen  map[flame.Interval]bool
	layoutSeen map[flame.Interval]bool
}

func (b builder) flushEvents() (style, layout []rootEvent) {
	for _, r := range b.rec.Records {
		if r.Type != model.TypeLayout || r.EventType != "recalculate-styles" && r.EventType != "layout" {
			continue
		}
		for _, part := range b.active(r.StartTime-b.rec.StartTime, r.EndTime-b.rec.StartTime) {
			e := rootEvent{from: part.Start, to: part.End}
			if r.EventType == "layout" {
				layout = append(layout, e)
			} else {
				style = append(style, e)
			}
		}
	}
	for _, events := range [][]rootEvent{style, layout} {
		sort.Slice(events, func(i, j int) bool {
			if events[i].from != events[j].from {
				return events[i].from < events[j].from
			}
			return events[i].to < events[j].to
		})
		for i := range events {
			events[i].maxEnd = events[i].to
			if i > 0 {
				events[i].maxEnd = math.Max(events[i].maxEnd, events[i-1].maxEnd)
			}
		}
	}
	return style, layout
}

func coveringFlushes(at float64, events []rootEvent) []flame.Interval {
	var intervals []flame.Interval
	end := sort.Search(len(events), func(i int) bool { return events[i].from > at })
	for i := end - 1; i >= 0 && events[i].maxEnd > at; i-- {
		if at < events[i].to {
			intervals = append(intervals, flame.Interval{Start: events[i].from, End: events[i].to})
		}
	}
	return intervals
}

func intervalMs(intervals []flame.Interval) float64 {
	var ms float64
	for _, w := range intervals {
		ms += (w.End - w.Start) * 1000
	}
	return number(ms)
}

func (b builder) nativeReadFinding() *Finding {
	apis := map[string]*nativeReadEvidence{}
	var samples []flame.Sample
	var ms float64
	// Untimed samples cannot corroborate a timeline flush and remain visible
	// only in recording-wide owner/function/stack aggregates.
	styleEvents, layoutEvents := b.flushEvents()
	for index, s := range b.tree.Samples {
		found := false
		var style, layout []flame.Interval
		for _, frame := range s.Frames {
			api, flush := nativeFlush(frame)
			if api == "" {
				continue
			}
			e := apis[api]
			if e == nil {
				e = &nativeReadEvidence{read: NativeRead{API: api, Flush: flush}, styleSeen: map[flame.Interval]bool{}, layoutSeen: map[flame.Interval]bool{}}
				apis[api] = e
			}
			if e.lastSample == index+1 {
				continue
			}
			e.lastSample = index + 1
			if !found {
				style, layout = coveringFlushes(s.Timestamp, styleEvents), coveringFlushes(s.Timestamp, layoutEvents)
				found = true
			}
			e.read.Samples++
			e.read.SelfMs += s.WeightMs
			if len(style) > 0 {
				e.read.StyleSamples++
				for _, w := range style {
					if !e.styleSeen[w] {
						e.styleSeen[w] = true
						e.style = append(e.style, w)
					}
				}
			}
			if len(layout) > 0 {
				e.read.LayoutSamples++
				for _, w := range layout {
					if !e.layoutSeen[w] {
						e.layoutSeen[w] = true
						e.layout = append(e.layout, w)
					}
				}
			}
		}
		if found {
			samples = append(samples, s)
			ms += s.WeightMs
		}
	}
	if len(samples) == 0 {
		return nil
	}
	f := &Finding{ID: "forced-style-or-layout-read", Title: "Native API reads with possible style or layout flushes", Stats: []Stat{
		b.stat("native_reads.self_ms", "Self time of stacks containing native flush-capable APIs", ms, "ms", sampledBasis),
		b.stat("native_reads.samples", "Timed samples containing native flush-capable APIs", float64(len(samples)), "count", sampledBasis),
	}, Evidence: b.stacks(samples, "native_reads"), SampleIDs: b.sampleIDs(samples), Command: b.query("rootcause", b.from, b.to)}
	var explanations []string
	var allStyle, allLayout []flame.Interval
	for _, e := range apis {
		e.style, e.layout = union(e.style), union(e.layout)
		e.read.SelfMs = number(e.read.SelfMs)
		e.read.StyleMs, e.read.LayoutMs = intervalMs(e.style), intervalMs(e.layout)
		key := "native_reads." + e.read.API
		e.read.Stats = []Stat{
			b.stat(key+".self_ms", "Sampled self time on stacks containing "+e.read.API, e.read.SelfMs, "ms", sampledBasis),
			b.stat(key+".samples", "Timed samples containing "+e.read.API, float64(e.read.Samples), "count", sampledBasis),
			b.stat(key+".style_samples", "Samples inside recalculate-styles records", float64(e.read.StyleSamples), "count", timelineBasis),
			b.stat(key+".layout_samples", "Samples inside layout records", float64(e.read.LayoutSamples), "count", timelineBasis),
			b.stat(key+".style_ms", "Union of covering recalculate-styles record intervals", e.read.StyleMs, "ms", timelineBasis),
			b.stat(key+".layout_ms", "Union of covering layout record intervals", e.read.LayoutMs, "ms", timelineBasis),
		}
		f.NativeReads = append(f.NativeReads, e.read)
		allStyle, allLayout = append(allStyle, e.style...), append(allLayout, e.layout...)
		explanations = append(explanations, fmt.Sprintf("%s (flush: %s): %d/%d samples inside recalculate-styles records (%.6g ms); %d/%d inside layout records (%.6g ms).", e.read.API, e.read.Flush, e.read.StyleSamples, e.read.Samples, e.read.StyleMs, e.read.LayoutSamples, e.read.Samples, e.read.LayoutMs))
	}
	sort.Slice(f.NativeReads, func(i, j int) bool { return f.NativeReads[i].API < f.NativeReads[j].API })
	sort.Strings(explanations)
	f.NativeReadsTotal = len(f.NativeReads)
	if len(f.NativeReads) == 1 {
		api := f.NativeReads[0]
		switch api.Flush {
		case "style":
			f.Title = "Native API with possible style recalculation (" + api.API + ")"
		case "layout":
			f.Title = "Native geometry read with possible layout flush (" + api.API + ")"
		default:
			f.Title = "Native read with property-dependent style or layout flush (" + api.API + ")"
		}
	}
	for _, item := range []struct {
		intervals      []flame.Interval
		event, related string
	}{
		{union(allStyle), "recalculate-styles", "style-recalc-heavy"},
		{union(allLayout), "layout", "layout-heavy"},
	} {
		if len(item.intervals) > 0 {
			f.RelatedFindings = append(f.RelatedFindings, item.related)
		}
		for _, w := range item.intervals {
			f.Windows = append(f.Windows, WindowRef{FromSec: b.exportTime(w.Start), ToSec: b.exportTime(w.End), Event: item.event, Command: b.query("rootcause", w.Start, w.End)})
		}
	}
	f.Explanation = strings.Join(explanations, " ") + " API semantics and temporal overlap are not proof that every call forced a flush. getAnimations requires a style change event, not proven layout; getComputedStyle depends on the unobserved property. Each executing sample counts once in the aggregate; per-API facets can overlap."
	return f
}

func reactPhase(frames []flame.Frame) string {
	for i := len(frames) - 1; i >= 0; i-- {
		if commitFrame(frames[i]) {
			return "commit"
		}
		if renderFrame(frames[i]) {
			return "render"
		}
	}
	return "other"
}

func hasExternalStore(frames []flame.Frame) bool {
	for _, f := range frames {
		if f.Category != flame.CategoryFramework {
			continue
		}
		for _, name := range frameNames(f) {
			if strings.Contains(name, "SyncExternalStore") || name == "forceStoreRerender" {
				return true
			}
		}
	}
	return false
}

func (b builder) reactFinding(samples []flame.Sample, windows []WindowRef, identified bool, prefix string) Finding {
	var ms, render, commit, other float64
	store := false
	for _, s := range samples {
		ms += s.WeightMs
		switch reactPhase(s.Frames) {
		case "render":
			render += s.WeightMs
		case "commit":
			commit += s.WeightMs
		default:
			other += s.WeightMs
		}
		store = store || hasExternalStore(s.Frames)
	}
	id, title := "react-sync", "Synchronous React root work; trigger not identifiable"
	explanation := "performSyncWorkOnRoot or flushSync* appears on these timed stacks. Trigger not identifiable from the timeline: React's own scheduling microtask or Scheduler message is not a user cause."
	if identified {
		id, title = "react-sync-in-trigger", "Synchronous React root work inside a user event, timer, or rAF"
		explanation = "performSyncWorkOnRoot or flushSync* appears on timed stacks inside a measured user event, timer, or rAF. React's own scheduling microtask or Scheduler message alone does not establish a user trigger."
	}
	if store {
		explanation += " useSyncExternalStore/forceStoreRerender frames suggest an external-store SyncLane source."
	}
	explanation += " Render and commit split each sampled self cost by its innermost phase ancestor; these disjoint costs are not inclusive ancestor totals. Temporal overlap is not proof that the trigger caused the update."
	return Finding{ID: id, Title: title, Explanation: explanation, Stats: []Stat{
		b.stat(prefix+".sync.self_ms", "Synchronous React stack self time", ms, "ms", sampledBasis),
		b.stat(prefix+".render.self_ms", "Sampled self time beneath the innermost React render phase", render, "ms", sampledBasis),
		b.stat(prefix+".commit.self_ms", "Sampled self time beneath the innermost React commit phase", commit, "ms", sampledBasis),
		b.stat(prefix+".other.self_ms", "Other self time under synchronous React roots", other, "ms", sampledBasis),
		b.stat(prefix+".sync.samples", "Synchronous React samples", float64(len(samples)), "count", sampledBasis),
		b.stat(prefix+".sync.trigger_windows", "Distinct identifiable triggers with samples", float64(len(windows)), "count", timelineBasis),
	}, Evidence: b.stacks(samples, prefix+".sync"), SampleIDs: b.sampleIDs(samples), Windows: windows, Command: b.query("rootcause", b.from, b.to)}
}

func (b builder) stackFindings() []Finding {
	var out []Finding
	if f := b.nativeReadFinding(); f != nil {
		out = append(out, *f)
	}
	triggers := b.interactionWindows()
	used := make([]bool, len(triggers))
	var identified, unknown []flame.Sample
	for _, s := range b.tree.Samples {
		if !syncReact(s.Frames) {
			continue
		}
		chosen := -1
		at := b.exportTime(s.Timestamp)
		for i, w := range triggers {
			if at >= w.FromSec && at < w.ToSec && (chosen < 0 || w.ToSec-w.FromSec < triggers[chosen].ToSec-triggers[chosen].FromSec) {
				chosen = i
			}
		}
		if chosen < 0 {
			unknown = append(unknown, s)
		} else {
			identified = append(identified, s)
			used[chosen] = true
		}
	}
	if len(identified) > 0 {
		var windows []WindowRef
		for i, w := range triggers {
			if used[i] {
				windows = append(windows, w)
			}
		}
		out = append(out, b.reactFinding(identified, windows, true, "react"))
	}
	if len(unknown) > 0 {
		prefix := "react"
		if len(identified) > 0 {
			prefix = "react.unidentified"
		}
		out = append(out, b.reactFinding(unknown, nil, false, prefix))
	}
	return out
}
