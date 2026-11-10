package rootcause

import (
	"sort"

	"github.com/omniviewdev/wkperf/internal/flame"
)

func (b builder) ownership() ([]Owner, []Attribution) {
	owners := map[flame.Category]*Owner{}
	for _, c := range []flame.Category{flame.CategoryApp, flame.CategoryFramework, flame.CategoryNative, flame.CategoryGC} {
		name := string(c)
		if c == flame.CategoryFramework {
			name = "framework-library"
		}
		owners[c] = &Owner{Category: name, Command: b.query("rootcause", b.from, b.to)}
	}
	attrs := map[string]*Attribution{}
	attrSamples := map[string][]flame.Sample{}
	var total float64
	for _, s := range b.tree.Samples {
		if len(s.Frames) == 0 {
			continue
		}
		leaf := s.Frames[len(s.Frames)-1]
		owner := owners[leaf.Category]
		if owner == nil {
			continue
		}
		owner.SelfMs += s.WeightMs
		owner.Samples++
		total += s.WeightMs
		var app *flame.Frame
		if leaf.Category == flame.CategoryApp {
			app = &leaf
		} else if leaf.Category == flame.CategoryFramework || leaf.Category == flame.CategoryNative {
			for i := len(s.Frames) - 2; i >= 0; i-- {
				if s.Frames[i].Category == flame.CategoryApp {
					app = &s.Frames[i]
					break
				}
			}
		}
		if app == nil {
			continue
		}
		key := stackKey([]flame.Frame{*app})
		a := attrs[key]
		if a == nil {
			a = &Attribution{App: *app, Command: b.query("paths "+quote(app.Name), b.from, b.to)}
			attrs[key] = a
		}
		switch leaf.Category {
		case flame.CategoryApp:
			a.SelfMs += s.WeightMs
		case flame.CategoryFramework:
			a.FrameworkMs += s.WeightMs
		case flame.CategoryNative:
			a.NativeMs += s.WeightMs
		}
		a.TotalMs += s.WeightMs
		attrSamples[key] = append(attrSamples[key], s)
	}
	out := make([]Owner, 0, len(owners))
	for _, c := range []flame.Category{flame.CategoryApp, flame.CategoryFramework, flame.CategoryNative, flame.CategoryGC} {
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
	attributions := make([]Attribution, 0, len(attrs))
	for key, a := range attrs {
		a.SelfMs = number(a.SelfMs)
		a.FrameworkMs = number(a.FrameworkMs)
		a.NativeMs = number(a.NativeMs)
		a.TotalMs = number(a.TotalMs)
		id := "app." + key
		a.Stats = []Stat{
			b.stat(id+".self_ms", "Application own self time", a.SelfMs, "ms", sampledBasis),
			b.stat(id+".framework_ms", "Closest application ancestor framework self time", a.FrameworkMs, "ms", sampledBasis),
			b.stat(id+".native_ms", "Closest application ancestor native self time", a.NativeMs, "ms", sampledBasis),
			b.stat(id+".total_ms", "Application own and charged self time", a.TotalMs, "ms", sampledBasis),
		}
		a.Stacks = b.stacks(attrSamples[key], id)
		attributions = append(attributions, *a)
	}
	sort.Slice(attributions, func(i, j int) bool {
		if attributions[i].TotalMs != attributions[j].TotalMs {
			return attributions[i].TotalMs > attributions[j].TotalMs
		}
		return frameLess(attributions[i].App, attributions[j].App)
	})
	return out, attributions
}
