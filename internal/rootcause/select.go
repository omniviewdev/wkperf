package rootcause

// SelectEvidence caps presentation lists without changing the full measured report.
// Totals retain the complete list sizes, including when selection is applied twice.
func SelectEvidence(report *Report, top int) *Report {
	if report == nil {
		return nil
	}
	if top <= 0 {
		top = report.Top
	}
	if top <= 0 {
		top = 10
	}
	r := *report
	r.Top = top
	r.AppAttributionTotal = max(r.AppAttributionTotal, len(r.AppAttribution))
	r.AppAttribution = append([]Attribution{}, head(r.AppAttribution, top)...)
	for i := range r.AppAttribution {
		a := &r.AppAttribution[i]
		a.StacksTotal = max(a.StacksTotal, len(a.Stacks))
		a.Stacks = head(a.Stacks, top)
	}
	r.Findings = append([]Finding{}, r.Findings...)
	for i := range r.Findings {
		f := &r.Findings[i]
		f.EvidenceTotal = max(f.EvidenceTotal, len(f.Evidence))
		f.Evidence = head(f.Evidence, top)
		f.WindowsTotal = max(f.WindowsTotal, len(f.Windows))
		f.Windows = head(f.Windows, top)
	}
	r.PeriodicRootsTotal = max(r.PeriodicRootsTotal, len(r.PeriodicRoots))
	r.PeriodicRoots = append([]PeriodicRoot{}, head(r.PeriodicRoots, top)...)
	for i := range r.PeriodicRoots {
		p := &r.PeriodicRoots[i]
		p.FiresTotal = max(p.FiresTotal, len(p.Fires))
		p.EvidenceTotal = max(p.EvidenceTotal, len(p.Evidence))
		p.Fires = head(p.Fires, top)
		p.Evidence = head(p.Evidence, top)
	}
	r.GCOverlapsTotal = max(r.GCOverlapsTotal, len(r.GCOverlaps))
	r.GCOverlaps = append([]GCOverlap{}, head(r.GCOverlaps, top)...)
	for i := range r.GCOverlaps {
		g := &r.GCOverlaps[i]
		g.EventsTotal = max(g.EventsTotal, len(g.Events))
		g.Events = head(g.Events, top)
	}
	r.Depth.OutliersTotal = max(r.Depth.OutliersTotal, len(r.Depth.Outliers))
	r.Depth.Outliers = head(r.Depth.Outliers, top)
	r.LongTasksTotal = max(r.LongTasksTotal, len(r.LongTasks))
	r.SlowFramesTotal = max(r.SlowFramesTotal, len(r.SlowFrames))
	r.EventBurstsTotal = max(r.EventBurstsTotal, len(r.EventBursts))
	r.WindowsTotal = max(r.WindowsTotal, len(r.Windows), r.LongTasksTotal+r.SlowFramesTotal+r.EventBurstsTotal)
	r.LongTasks = head(r.LongTasks, top)
	r.SlowFrames = head(r.SlowFrames, top)
	r.EventBursts = head(r.EventBursts, top)
	r.Windows = head(r.Windows, top)
	return &r
}

func head[T any](values []T, top int) []T {
	return values[:min(len(values), top)]
}
