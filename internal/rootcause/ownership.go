package rootcause

import (
	"sort"

	"github.com/omniviewdev/wkperf/internal/flame"
)

func contextFrame(f flame.Frame) bool {
	return f.Category == flame.CategoryApp || f.Category == flame.CategoryUnknownBundled || f.Category == flame.CategoryInjected
}

func (b builder) ownership() ([]Owner, []Attribution, AttributionUnavailable) {
	categories := [...]flame.Category{flame.CategoryApp, flame.CategoryFramework, flame.CategoryNative, flame.CategoryGC, flame.CategoryUnknownBundled, flame.CategoryInjected}
	owners := make(map[flame.Category]*Owner, len(categories))
	for _, c := range categories {
		owners[c] = &Owner{Category: string(c), Command: b.query("rootcause", b.from, b.to)}
	}
	attrs := map[string]*Attribution{}
	attrSamples := map[string][]flame.Sample{}
	unavailable := AttributionUnavailable{Reason: "No non-framework frame on these stacks; framework-only/native work has no captured scheduling application frame."}
	var total float64
	b.tree.EachSample(func(s flame.Sample) {
		if len(s.Frames) == 0 {
			return
		}
		leaf := s.Frames[len(s.Frames)-1]
		owner := owners[leaf.Category]
		if owner == nil {
			return
		}
		owner.SelfMs += s.WeightMs
		owner.Samples++
		total += s.WeightMs
		context := -1
		if contextFrame(leaf) {
			context = len(s.Frames) - 1
		} else if leaf.Category == flame.CategoryFramework || leaf.Category == flame.CategoryNative {
			for i := len(s.Frames) - 2; i >= 0; i-- {
				if contextFrame(s.Frames[i]) {
					context = i
					break
				}
			}
		}
		if context < 0 {
			switch leaf.Category {
			case flame.CategoryFramework:
				unavailable.FrameworkMs += s.WeightMs
			case flame.CategoryNative:
				unavailable.NativeMs += s.WeightMs
			default:
				return
			}
			unavailable.SelfMs += s.WeightMs
			unavailable.Samples++
			return
		}
		frame := s.Frames[context]
		key := stackKey([]flame.Frame{frame})
		a := attrs[key]
		if a == nil {
			a = &Attribution{Frame: frame, Command: b.query("paths "+quote(flame.FunctionID(frame)), b.from, b.to)}
			attrs[key] = a
		}
		switch leaf.Category {
		case flame.CategoryFramework:
			a.FrameworkMs += s.WeightMs
		case flame.CategoryNative:
			a.NativeMs += s.WeightMs
		default:
			a.SelfMs += s.WeightMs
		}
		a.TotalMs += s.WeightMs
		attrSamples[key] = append(attrSamples[key], s)
	})
	out := make([]Owner, 0, len(owners))
	for _, c := range categories {
		o := owners[c]
		o.SelfMs = number(o.SelfMs)
		if total > 0 {
			o.Pct = number(o.SelfMs / total * 100)
		}
		key := "owner." + o.Category
		o.Stats = []Stat{
			b.stat(key+".self_ms", o.Category+" self time", o.SelfMs, "ms", sampledBasis),
			b.stat(key+".pct", o.Category+" sampled self share", o.Pct, "%", sampledBasis),
			b.stat(key+".samples", o.Category+" self samples", float64(o.Samples), "count", sampledBasis),
		}
		out = append(out, *o)
	}
	unavailable.SelfMs = number(unavailable.SelfMs)
	unavailable.FrameworkMs = number(unavailable.FrameworkMs)
	unavailable.NativeMs = number(unavailable.NativeMs)
	unavailable.Stats = []Stat{
		b.stat("context.unattributed.self_ms", "Self time without a captured non-framework context", unavailable.SelfMs, "ms", sampledBasis),
		b.stat("context.unattributed.framework_ms", "Framework self time without a captured non-framework context", unavailable.FrameworkMs, "ms", sampledBasis),
		b.stat("context.unattributed.native_ms", "Native self time without a captured non-framework context", unavailable.NativeMs, "ms", sampledBasis),
		b.stat("context.unattributed.samples", "Samples without a captured non-framework context", float64(unavailable.Samples), "count", sampledBasis),
	}
	attributions := make([]Attribution, 0, len(attrs))
	for key, a := range attrs {
		a.SelfMs = number(a.SelfMs)
		a.FrameworkMs = number(a.FrameworkMs)
		a.NativeMs = number(a.NativeMs)
		a.TotalMs = number(a.TotalMs)
		id := "context." + key
		a.Stats = []Stat{
			b.stat(id+".self_ms", "Non-framework context own self time", a.SelfMs, "ms", sampledBasis),
			b.stat(id+".framework_ms", "Framework self time under the nearest non-framework context", a.FrameworkMs, "ms", sampledBasis),
			b.stat(id+".native_ms", "Native self time under the nearest non-framework context", a.NativeMs, "ms", sampledBasis),
			b.stat(id+".total_ms", "Non-framework context own and charged self time", a.TotalMs, "ms", sampledBasis),
		}
		a.Stacks = b.stacks(attrSamples[key], id)
		attributions = append(attributions, *a)
	}
	sort.Slice(attributions, func(i, j int) bool {
		if attributions[i].TotalMs != attributions[j].TotalMs {
			return attributions[i].TotalMs > attributions[j].TotalMs
		}
		return frameLess(attributions[i].Frame, attributions[j].Frame)
	})
	return out, attributions, unavailable
}
