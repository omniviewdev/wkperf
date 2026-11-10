package rootcause

import "sort"

// Stats returns every named numeric figure once, sorted by stable key.
func Stats(r *Report) []Stat {
	if r == nil {
		return nil
	}
	all := map[string]Stat{}
	add := func(stats []Stat) {
		for _, s := range stats {
			all[s.Key] = s
		}
	}
	addStacks := func(stacks []Stack) {
		for _, s := range stacks {
			add(s.Stats)
		}
	}
	addPath := func(path Path) {
		add(path.Stats)
		for _, node := range path.Nodes {
			add(node.Stats)
		}
		for _, branch := range path.Branches {
			add(branch.Stats)
		}
	}
	addWindow := func(w Window) {
		add(w.Stats)
		if w.HotPath != nil {
			addPath(*w.HotPath)
		}
		if w.TopStack != nil {
			add(w.TopStack.Stats)
		}
	}
	add(r.Stats)
	addPath(r.HotPath)
	for _, o := range r.Owners {
		add(o.Stats)
	}
	add(r.ContextUnavailable.Stats)
	for _, a := range r.ContextAttribution {
		add(a.Stats)
		addStacks(a.Stacks)
	}
	for _, f := range r.Findings {
		add(f.Stats)
		addStacks(f.Evidence)
		for _, api := range f.NativeReads {
			add(api.Stats)
		}
	}
	for _, p := range r.PeriodicRoots {
		add(p.Stats)
		addStacks(p.Evidence)
		for _, f := range p.Fires {
			add(f.Stats)
		}
	}
	for _, g := range r.GCOverlaps {
		add(g.Stats)
		for _, e := range g.Events {
			add(e.Stats)
		}
	}
	for _, w := range r.Windows {
		addWindow(w)
	}
	for _, w := range r.LongTasks {
		addWindow(w)
	}
	for _, w := range r.SlowFrames {
		addWindow(w)
	}
	for _, w := range r.InputStalls {
		addWindow(w)
	}
	for _, w := range r.EventBursts {
		addWindow(w)
	}
	add(r.Depth.Stats)
	for _, o := range r.Depth.Outliers {
		add(o.Stats)
		add(o.Stack.Stats)
	}
	out := make([]Stat, 0, len(all))
	for _, s := range all {
		out = append(out, s)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Key < out[j].Key })
	return out
}
