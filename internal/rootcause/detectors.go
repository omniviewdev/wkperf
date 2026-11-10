package rootcause

import (
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

func layoutRead(f flame.Frame) bool {
	if f.Category != flame.CategoryNative {
		return false
	}
	for _, name := range frameNames(f) {
		name = strings.TrimPrefix(name, "get ")
		if i := strings.LastIndexAny(name, ".:"); i >= 0 {
			name = name[i+1:]
		}
		switch name {
		case "getAnimations", "getBoundingClientRect", "getClientRects", "offsetHeight", "offsetWidth", "getComputedStyle", "scrollTop", "scrollHeight":
			return true
		}
	}
	return false
}

func syncReact(frames []flame.Frame) bool {
	for _, f := range frames {
		if f.Category != flame.CategoryFramework {
			continue
		}
		for _, name := range frameNames(f) {
			if name == "performSyncWorkOnRoot" || name == "renderRootSync" || name == "flushSync" || strings.HasPrefix(name, "flushSyncWork") {
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
		if strings.HasPrefix(name, "commit") || strings.HasPrefix(name, "recursivelyTraverse") && strings.Contains(name, "Effect") {
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
		if strings.HasPrefix(name, "renderRoot") || strings.HasPrefix(name, "renderWithHooks") || strings.HasPrefix(name, "workLoop") || strings.HasPrefix(name, "performUnitOfWork") || strings.HasPrefix(name, "beginWork") || strings.HasPrefix(name, "completeWork") || strings.HasPrefix(name, "updateFunctionComponent") || strings.HasPrefix(name, "updateClassComponent") {
			return true
		}
	}
	return false
}

func isInteraction(r model.Record) bool {
	t := strings.ToLower(r.EventType)
	return r.Type == model.TypeScript && !profile.IsGC(r) && (strings.Contains(t, "microtask") || t == "event-dispatched" || t == "event-fired" || t == "event-listener")
}

func (b builder) interactionWindows() []flame.Interval {
	var all []flame.Interval
	for _, r := range b.rec.Records {
		if isInteraction(r) {
			all = append(all, b.active(r.StartTime-b.rec.StartTime, r.EndTime-b.rec.StartTime)...)
		}
	}
	return union(all)
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

func (b builder) stackFindings() []Finding {
	var layout, react []flame.Sample
	var layoutMs, reactMs, renderMs, commitMs, otherMs float64
	triggers := b.interactionWindows()
	used := make([]bool, len(triggers))
	for _, s := range b.tree.Samples {
		if len(s.Frames) == 0 {
			continue
		}
		leaf := s.Frames[len(s.Frames)-1]
		for _, frame := range s.Frames {
			if layoutRead(frame) {
				layout = append(layout, s)
				layoutMs += s.WeightMs
				break
			}
		}
		if !syncReact(s.Frames) {
			continue
		}
		for i, w := range triggers {
			if s.Timestamp < w.Start || s.Timestamp >= w.End {
				continue
			}
			react = append(react, s)
			used[i] = true
			reactMs += s.WeightMs
			if commitFrame(leaf) {
				commitMs += s.WeightMs
			} else if renderFrame(leaf) {
				renderMs += s.WeightMs
			} else {
				otherMs += s.WeightMs
			}
			break
		}
	}
	var out []Finding
	if len(layout) > 0 {
		out = append(out, Finding{ID: "suspected-layout-read", Title: "Suspected layout-forcing native reads", Severity: "medium", Explanation: "Native layout-related reads appear in full sampler stacks. Associated costs count each executing self sample once, not inclusive totals. This is not proof that every call forced layout; verify invalidation and instrumented layout timing.", Stats: []Stat{
			b.stat("layout_reads.self_ms", "Self time of stacks containing native layout-related reads", layoutMs, "ms", sampledBasis),
			b.stat("layout_reads.samples", "Samples containing native layout-related reads", float64(len(layout)), "count", sampledBasis),
		}, Evidence: b.stacks(layout, "layout_reads"), Command: b.query("rootcause", b.from, b.to)})
	}
	if len(react) > 0 {
		var windows []WindowRef
		for i, w := range triggers {
			if used[i] {
				windows = append(windows, WindowRef{FromSec: b.exportTime(w.Start), ToSec: b.exportTime(w.End), Command: b.query("stacks", w.Start, w.End)})
			}
		}
		out = append(out, Finding{ID: "react-sync-in-trigger", Title: "Synchronous React root work inside an event or microtask", Severity: "high", Explanation: "A synchronous React root or flushSync frame occurs in sampler stacks timestamped inside instrumented event or microtask intervals. Render and commit costs use distinct executing self frames, not overlapping inclusive totals. Temporal overlap does not prove the event caused an update.", Stats: []Stat{
			b.stat("react.sync.self_ms", "Synchronous React stack self time inside triggers", reactMs, "ms", sampledBasis),
			b.stat("react.render.self_ms", "Distinct React render self time", renderMs, "ms", sampledBasis),
			b.stat("react.commit.self_ms", "Distinct React commit self time", commitMs, "ms", sampledBasis),
			b.stat("react.other.self_ms", "Other self time under synchronous React roots", otherMs, "ms", sampledBasis),
			b.stat("react.sync.samples", "Synchronous React trigger samples", float64(len(react)), "count", sampledBasis),
			b.stat("react.sync.trigger_windows", "Merged triggering intervals with samples", float64(len(windows)), "count", timelineBasis),
		}, Evidence: b.stacks(react, "react.sync"), Windows: windows, Command: b.query("rootcause", b.from, b.to)})
	}
	return out
}
