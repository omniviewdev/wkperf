package audit

import (
	"sort"

	"github.com/omniviewdev/wkperf/internal/insights"
	"github.com/omniviewdev/wkperf/internal/metrics"
	"github.com/omniviewdev/wkperf/internal/scorecard"
)

const defaultTop = 10

// severityWeight scales a finding's ImpactPct when ranking priorities.
var severityWeight = map[insights.Severity]float64{
	insights.SevHigh:   3,
	insights.SevMedium: 2,
	insights.SevLow:    1,
	insights.SevInfo:   0.25,
}

func sevWeight(s insights.Severity) float64 {
	if w, ok := severityWeight[s]; ok {
		return w
	}
	return 0
}

// sevRank orders severities for "max across scenes".
func sevRank(s insights.Severity) int {
	switch s {
	case insights.SevHigh:
		return 3
	case insights.SevMedium:
		return 2
	case insights.SevLow:
		return 1
	}
	return 0
}

// CardValue returns the value of standard id in c, reporting false when the
// card is nil, lacks the standard or rated it Unavailable.
func CardValue(c *scorecard.Scorecard, id string) (float64, bool) {
	if c == nil {
		return 0, false
	}
	for _, r := range c.Results {
		if r.ID == id && r.Rating != scorecard.Unavailable {
			return r.Value, true
		}
	}
	return 0, false
}

func median(v []float64) float64 {
	if len(v) == 0 {
		return 0
	}
	s := append([]float64(nil), v...)
	sort.Float64s(s)
	n := len(s)
	if n%2 == 1 {
		return s[n/2]
	}
	return (s[n/2-1] + s[n/2]) / 2
}

// cardValues is the available id->value map of c (nil-safe).
func cardValues(c *scorecard.Scorecard) map[string]float64 {
	if c == nil {
		return nil
	}
	return scorecard.Values(c)
}

// Build aggregates scenes into one Report. Scenes without runs are dropped.
// Scenes are ordered worst score first; Top (default 10) caps the priority,
// hotspot and module lists.
//
// Priority.Rank = sum over scenes of ImpactPct * severityWeight, with weights
// high=3, medium=2, low=1, info=0.25. Summing (rather than averaging over the
// scenes where the finding fired) makes a finding that hurts many scenes
// outrank one that is severe in only one scene of similar size.
func Build(scenes []Scene, o Options) *Report {
	top := o.Top
	if top <= 0 {
		top = defaultTop
	}

	results := make([]SceneResult, 0, len(scenes))
	for _, s := range scenes {
		if len(s.Runs) == 0 {
			continue
		}
		results = append(results, buildScene(s))
	}
	sort.SliceStable(results, func(i, j int) bool {
		a, b := results[i], results[j]
		if a.Card.Score != b.Card.Score {
			return a.Card.Score < b.Card.Score
		}
		return a.Name < b.Name
	})

	rep := &Report{Scenes: results, WorstByStd: map[string]string{}}
	rep.Overall, rep.WorstByStd = overall(results)
	rep.Breakdown = mergeBreakdown(results)
	rep.Priorities = buildPriorities(results, top)
	rep.Hotspots, rep.Modules = mergeHotspots(results, top)
	return rep
}

func buildScene(s Scene) SceneResult {
	sr := SceneResult{Name: s.Name, Runs: len(s.Runs)}

	perStd := map[string][]float64{}
	var durations []float64
	for _, r := range s.Runs {
		if r.Source != "" {
			sr.Sources = append(sr.Sources, r.Source)
		} else if r.Report != nil && r.Report.Source != "" {
			sr.Sources = append(sr.Sources, r.Report.Source)
		}
		for id, v := range cardValues(r.Card) {
			perStd[id] = append(perStd[id], v)
		}
		if r.Report != nil {
			durations = append(durations, r.Report.DurationSec)
		}
	}
	meds := make(map[string]float64, len(perStd))
	for id, vs := range perStd {
		meds[id] = median(vs)
	}
	sr.Card = scorecard.FromValues(meds)
	sr.DurationSec = median(durations)
	sr.Breakdown = medianBreakdown(s.Runs)
	sr.Rep = pickRep(s.Runs)
	if sr.Rep != nil {
		best := -1.0
		for _, f := range sr.Rep.Findings {
			if f.ImpactMs > best {
				best = f.ImpactMs
				sr.TopFinding = f.Title
			}
		}
	}
	return sr
}

func frameP95(rep *insights.Report) (float64, bool) {
	if rep == nil || rep.Summary == nil {
		return 0, false
	}
	m, ok := rep.Summary.Lookup("frames.p95_ms")
	if !ok {
		return 0, false
	}
	return m.Value, true
}

// pickRep returns the run whose frame p95 is closest to the median frame p95;
// the first run with a report when no run has that metric.
func pickRep(runs []Run) *insights.Report {
	var vals []float64
	for _, r := range runs {
		if v, ok := frameP95(r.Report); ok {
			vals = append(vals, v)
		}
	}
	if len(vals) > 0 {
		med := median(vals)
		var best *insights.Report
		bestD := 0.0
		for _, r := range runs {
			v, ok := frameP95(r.Report)
			if !ok {
				continue
			}
			d := v - med
			if d < 0 {
				d = -d
			}
			if best == nil || d < bestD {
				best, bestD = r.Report, d
			}
		}
		return best
	}
	for _, r := range runs {
		if r.Report != nil {
			return r.Report
		}
	}
	return nil
}

// medianBreakdown takes the per-slice median across runs, in order of first
// appearance. A run lacking a slice does not contribute to that slice.
func medianBreakdown(runs []Run) []insights.Slice {
	var order []string
	ms := map[string][]float64{}
	pct := map[string][]float64{}
	for _, r := range runs {
		if r.Report == nil {
			continue
		}
		for _, s := range r.Report.Breakdown {
			if _, ok := ms[s.Name]; !ok {
				order = append(order, s.Name)
			}
			ms[s.Name] = append(ms[s.Name], s.Ms)
			pct[s.Name] = append(pct[s.Name], s.Pct)
		}
	}
	var out []insights.Slice
	for _, n := range order {
		out = append(out, insights.Slice{Name: n, Ms: median(ms[n]), Pct: median(pct[n])})
	}
	return out
}

// overall builds the typical-scene card (median per standard across scenes)
// and the scene with the worst value per standard.
func overall(scenes []SceneResult) (*scorecard.Scorecard, map[string]string) {
	perStd := map[string][]float64{}
	for _, s := range scenes {
		for id, v := range cardValues(s.Card) {
			perStd[id] = append(perStd[id], v)
		}
	}
	meds := make(map[string]float64, len(perStd))
	for id, vs := range perStd {
		meds[id] = median(vs)
	}
	worst := map[string]string{}
	for _, std := range scorecard.Standards() {
		found := false
		var wv float64
		for _, s := range scenes { // worst score first, so ties go to the worse scene
			v, ok := CardValue(s.Card, std.ID)
			if !ok {
				continue
			}
			if !found || (std.HigherBetter && v < wv) || (!std.HigherBetter && v > wv) {
				found, wv = true, v
				worst[std.ID] = s.Name
			}
		}
	}
	return scorecard.FromValues(meds), worst
}

// mergeBreakdown is the duration-weighted mean share per slice, normalized so
// the shares sum to 100. Equal weights are used if no scene has a duration.
func mergeBreakdown(scenes []SceneResult) []insights.Slice {
	totalDur := 0.0
	for _, s := range scenes {
		totalDur += s.DurationSec
	}
	var order []string
	pct := map[string]float64{}
	ms := map[string]float64{}
	for _, s := range scenes {
		w := 1.0
		if totalDur > 0 {
			w = s.DurationSec / totalDur
		} else if len(scenes) > 0 {
			w = 1 / float64(len(scenes))
		}
		for _, sl := range s.Breakdown {
			if _, ok := pct[sl.Name]; !ok {
				order = append(order, sl.Name)
			}
			pct[sl.Name] += sl.Pct * w
			ms[sl.Name] += sl.Ms
		}
	}
	sum := 0.0
	for _, v := range pct {
		sum += v
	}
	out := make([]insights.Slice, 0, len(order))
	for _, n := range order {
		p := pct[n]
		if sum > 0 {
			p = p / sum * 100
		}
		out = append(out, insights.Slice{Name: n, Ms: ms[n], Pct: p})
	}
	return out
}

func buildPriorities(scenes []SceneResult, top int) []Priority {
	type acc struct {
		p       Priority
		bestImp float64
		seen    map[string]bool
		pctSum  float64
		hasBest bool
	}
	byID := map[string]*acc{}
	var order []string
	for _, s := range scenes {
		if s.Rep == nil {
			continue
		}
		for _, f := range s.Rep.Findings {
			a := byID[f.ID]
			if a == nil {
				a = &acc{seen: map[string]bool{}}
				a.p.ID = f.ID
				byID[f.ID] = a
				order = append(order, f.ID)
			}
			if !a.seen[s.Name] {
				a.seen[s.Name] = true
				a.p.Scenes = append(a.p.Scenes, s.Name)
			}
			a.p.TotalImpactMs += f.ImpactMs
			a.pctSum += f.ImpactPct
			a.p.Rank += f.ImpactPct * sevWeight(f.Severity)
			if sevRank(f.Severity) > sevRank(a.p.Severity) || a.p.Severity == "" {
				a.p.Severity = f.Severity
			}
			if !a.hasBest || f.ImpactMs > a.bestImp {
				a.hasBest, a.bestImp = true, f.ImpactMs
				a.p.Title, a.p.Category, a.p.Suggestion = f.Title, f.Category, f.Suggestion
				a.p.Evidence = nil
				for _, e := range f.Evidence {
					a.p.Evidence = append(a.p.Evidence, s.Name+": "+e)
				}
			}
		}
	}
	out := make([]Priority, 0, len(order))
	for _, id := range order {
		a := byID[id]
		if len(scenes) > 0 {
			a.p.AvgImpactPct = a.pctSum / float64(len(scenes))
		}
		out = append(out, a.p)
	}
	sort.SliceStable(out, func(i, j int) bool {
		a, b := out[i], out[j]
		if a.Rank != b.Rank {
			return a.Rank > b.Rank
		}
		if a.TotalImpactMs != b.TotalImpactMs {
			return a.TotalImpactMs > b.TotalImpactMs
		}
		return a.ID < b.ID
	})
	if len(out) > top {
		out = out[:top]
	}
	return out
}

func mergeHotspots(scenes []SceneResult, top int) ([]HotFunction, []insights.ModuleStat) {
	fn := map[string]*HotFunction{}
	mod := map[string]*insights.ModuleStat{}
	totalSelf := 0.0
	totalMod := 0.0
	for _, s := range scenes {
		if s.Rep == nil {
			continue
		}
		seenFn := map[string]bool{}
		for _, h := range s.Rep.Hotspots {
			k := metrics.FuncStat{Name: h.Name, URL: h.URL, Line: h.Line}.Key()
			hf := fn[k]
			if hf == nil {
				hf = &HotFunction{Name: h.Name, URL: h.URL, Line: h.Line}
				fn[k] = hf
			}
			hf.SelfMs += h.SelfMs
			totalSelf += h.SelfMs
			if !seenFn[k] {
				seenFn[k] = true
				hf.SceneCount++
				hf.Scenes = append(hf.Scenes, s.Name)
			}
		}
		for _, m := range s.Rep.Modules {
			ms := mod[m.Name]
			if ms == nil {
				ms = &insights.ModuleStat{Name: m.Name}
				mod[m.Name] = ms
			}
			ms.SelfMs += m.SelfMs
			totalMod += m.SelfMs
		}
	}

	hot := make([]HotFunction, 0, len(fn))
	for _, h := range fn {
		if totalSelf > 0 {
			h.Pct = h.SelfMs / totalSelf * 100
		}
		hot = append(hot, *h)
	}
	sort.Slice(hot, func(i, j int) bool {
		a, b := hot[i], hot[j]
		if a.SelfMs != b.SelfMs {
			return a.SelfMs > b.SelfMs
		}
		if a.Name != b.Name {
			return a.Name < b.Name
		}
		if a.URL != b.URL {
			return a.URL < b.URL
		}
		return a.Line < b.Line
	})
	if len(hot) > top {
		hot = hot[:top]
	}

	mods := make([]insights.ModuleStat, 0, len(mod))
	for _, m := range mod {
		if totalMod > 0 {
			m.Pct = m.SelfMs / totalMod * 100
		}
		mods = append(mods, *m)
	}
	sort.Slice(mods, func(i, j int) bool {
		if mods[i].SelfMs != mods[j].SelfMs {
			return mods[i].SelfMs > mods[j].SelfMs
		}
		return mods[i].Name < mods[j].Name
	})
	if len(mods) > top {
		mods = mods[:top]
	}
	return hot, mods
}
