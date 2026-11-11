package insights

import (
	"encoding/json"
	"math"
	"sort"
	"strings"

	"github.com/omniviewdev/wkperf/internal/analyze"
	"github.com/omniviewdev/wkperf/internal/flame"
	"github.com/omniviewdev/wkperf/internal/metrics"
	"github.com/omniviewdev/wkperf/internal/model"
	"github.com/omniviewdev/wkperf/internal/profile"
	"github.com/omniviewdev/wkperf/internal/rootcause"
)

func (o Options) withDefaults() Options {
	if o.Top <= 0 {
		o.Top = 10
	}
	if o.JankMs <= 0 {
		o.JankMs = profile.FrameBudgetMs
	}
	if o.LongTaskMs <= 0 {
		o.LongTaskMs = 50
	}
	if o.ModuleGrouping == "" {
		o.ModuleGrouping = "file"
	}
	return o
}

// Analyze profiles one recording. It never panics on empty or nil input.
func Analyze(rec *model.Recording, source string, o Options) *Report {
	o = o.withDefaults()
	if rec == nil {
		rec = &model.Recording{}
	}
	if rec.SampleEvidence != nil {
		o.WindowStartSec = rec.StartTime - rec.SampleEvidence.StartTime
	}
	tree := flame.Build(rec, flame.Options{Ownership: o.Ownership})
	o.Ownership = tree.Ownership
	sum := analyze.SummarizeWithTree(rec, source, tree)
	work, costs := profile.MeasureRecords(rec, rec.StartTime, rec.EndTime)
	r := &Report{
		SchemaVersion:       "wkperf-insights/v3",
		Top:                 o.Top,
		FlameTree:           tree,
		Source:              source,
		WindowStartSec:      o.WindowStartSec,
		WindowEndSec:        o.WindowStartSec + rec.EndTime - rec.StartTime,
		WholeRecording:      !rec.UntimedExcluded && o.WindowStartSec <= 1e-9,
		ModuleGrouping:      o.ModuleGrouping,
		JankMs:              o.JankMs,
		LongTaskMs:          o.LongTaskMs,
		SourceMapPaths:      append([]string{}, o.Sourcemaps...),
		Ownership:           tree.Ownership,
		Name:                rec.DisplayName,
		DurationSec:         sum.DurationSec,
		Summary:             sum,
		Breakdown:           breakdownWithWork(rec, work),
		GCMs:                work.GCMs,
		GCOverlapWithWorkMs: work.GCOverlapWithWorkMs,
		Hotspots:            []metrics.FuncStat{},
		InclusiveHotspots:   []metrics.FuncStat{},
		CommonStacks:        []metrics.StackStat{},
		Triggers:            []TriggerStat{},
		Wakeups:             WakeupStats{Causes: []TriggerStat{}},
		Modules:             []ModuleStat{},
		SlowFrames:          []SlowFrame{},
		InputStalls:         []InputStall{},
		LongTasks:           []LongTask{},
		Findings:            []Finding{},
	}
	for _, f := range sum.Functions {
		if len(r.Hotspots) >= o.Top || f.SelfMs <= 0 {
			break
		}
		r.Hotspots = append(r.Hotspots, f)
	}
	for _, f := range sum.InclusiveFunctions {
		if len(r.InclusiveHotspots) >= o.Top || f.TotalMs <= 0 {
			break
		}
		r.InclusiveHotspots = append(r.InclusiveHotspots, f)
	}
	r.CommonStacks = sum.CommonStacks
	if len(r.CommonStacks) > o.Top {
		r.CommonStacks = r.CommonStacks[:o.Top]
	}
	mods := computeModules(sum.Functions, o.ModuleGrouping)
	if len(mods) > o.Top {
		r.Modules = mods[:o.Top]
	} else if mods != nil {
		r.Modules = mods
	}

	idx := newSampleIndex(tree, rec.StartTime, o.WindowStartSec)
	r.Triggers = triggerStatsWithTree(rec, tree, o.Top)
	r.Wakeups = idleWakeups(rec, tree, o.Top)
	r.SampleCoverage = sampleCoverage(sum, work.ScriptMs)
	r.SampleCoverage.SampleCount = tree.SampleCount()
	r.SampleCoverage.TimedSampleCount = len(tree.Samples)
	r.SampleCoverage.UntimedSampleCount = tree.UntimedTotal.Count
	r.SampleCoverage.UntimedMs = tree.UntimedTotal.Ms
	r.SampleCoverage.UntimedExcluded = tree.UntimedTotal.Count > 0 && !tree.UntimedIncluded
	r.SampleCoverage.UntimedSpans = append([]flame.UntimedSpan{}, tree.UntimedSpans...)
	for i := range r.SampleCoverage.UntimedSpans {
		span := &r.SampleCoverage.UntimedSpans[i]
		span.FromSec = math.Round((span.FromSec+o.WindowStartSec)*1e9) / 1e9
		span.ToSec = math.Round((span.ToSec+o.WindowStartSec)*1e9) / 1e9
		if span.FromSec == 0 {
			span.FromSec = 0
		}
	}
	sampledMs := r.SampleCoverage.SampledMs
	if r.SampleCoverage.UntimedExcluded {
		sampledMs += r.SampleCoverage.UntimedMs
	}
	note := rootcause.UntimedAttributionNote(r.SampleCoverage.UntimedMs, sampledMs)
	allSlow := slowFrames(rec, idx, o.JankMs)
	allStalls := inputStalls(rec, idx)
	allLong := longTasks(rec, idx, o.LongTaskMs)
	for i := range allSlow {
		allSlow[i].SampleStatus = note
	}
	for i := range allStalls {
		allStalls[i].SampleStatus = note
	}
	for i := range allLong {
		allLong[i].SampleStatus = note
	}
	r.SlowFrames, r.SlowFramesTotal = allSlow, len(allSlow)
	r.InputStalls, r.InputStallsTotal = allStalls, len(allStalls)
	r.LongTasks, r.LongTasksTotal = allLong, len(allLong)

	r.RootCause = rootcause.Build(rec, tree, rootcause.Options{
		Source: source, OffsetSec: o.WindowStartSec, Sourcemaps: o.Sourcemaps,
		Top: o.Top, JankMs: o.JankMs, LongTaskMs: o.LongTaskMs, Ownership: o.Ownership,
	})
	ctx := &ruleCtx{rec: rec, sum: sum, rep: r, opt: o, mods: mods, slow: allSlow, long: allLong, costs: costs, wallMs: sum.DurationSec * 1000}
	r.Findings = runRules(ctx)
	r.Findings = mergeSampleFindings(append(r.Findings, rootFindings(ctx)...))
	sortFindings(r.Findings)
	syncRootFindingSeverities(r)
	return r
}

// detailString returns Details when it is a JSON string, or the "url" field
// of an object; otherwise "".
func detailString(r *model.Record) string {
	if len(r.Details) == 0 {
		return ""
	}
	var s string
	if json.Unmarshal(r.Details, &s) == nil {
		return s
	}
	var o struct {
		URL string `json:"url"`
	}
	if json.Unmarshal(r.Details, &o) == nil {
		return o.URL
	}
	return ""
}

// moduleOf preserves file identity. Parent-directory rollup is opt-in.
func moduleOf(url string) string {
	if i := strings.IndexAny(url, "?#"); i >= 0 {
		url = url[:i]
	}
	if url == "" || strings.HasPrefix(url, "[") || strings.Contains(url, "native code") {
		return "(native)"
	}
	return url
}

func moduleGroup(url, grouping string) string {
	url = moduleOf(url)
	if grouping == "parent-directory" && url != "(native)" {
		if i := strings.LastIndex(url, "/"); i > 0 && !strings.HasSuffix(url[:i], ":/") && !strings.HasSuffix(url[:i], ":") {
			return url[:i]
		}
	}
	return url
}

func computeModules(fns []metrics.FuncStat, grouping string) []ModuleStat {
	agg := map[string]ModuleStat{}
	total := 0.0
	for _, f := range fns {
		if f.SelfMs <= 0 {
			continue
		}
		name := moduleGroup(f.URL, grouping)
		module := agg[name]
		module.Name = name
		module.SelfMs += f.SelfMs
		module.SelfSamples += f.SelfSamples
		agg[name] = module
		total += f.SelfMs
	}
	var out []ModuleStat
	for _, module := range agg {
		module.Pct = module.SelfMs / total * 100
		out = append(out, module)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].SelfMs != out[j].SelfMs {
			return out[i].SelfMs > out[j].SelfMs
		}
		return out[i].Name < out[j].Name
	})
	return out
}

// sampleIndex is a time-sorted flattening of all samples for range queries.
type sampleIndex struct {
	rows         []indexedSample
	untimedSpans []flame.UntimedSpan
	origin       float64
	offset       float64
}

func newSampleIndex(tree *flame.Tree, origin, offset float64) *sampleIndex {
	x := &sampleIndex{rows: make([]indexedSample, 0, len(tree.Samples)), untimedSpans: tree.UntimedSpans, origin: origin, offset: offset}
	for _, sample := range tree.Samples {
		if len(sample.Frames) == 0 {
			continue
		}
		leaf := sample.Frames[len(sample.Frames)-1]
		x.rows = append(x.rows, indexedSample{sample.Timestamp + origin, sample.WeightMs, frameLabel(leaf)})
	}
	sort.Slice(x.rows, func(i, j int) bool {
		a, b := x.rows[i], x.rows[j]
		if a.t != b.t {
			return a.t < b.t
		}
		if a.label != b.label {
			return a.label < b.label
		}
		return a.w < b.w
	})
	return x
}

// top returns the hottest self function among samples with ts in [s,e).
func (x *sampleIndex) top(s, e float64) string {
	i := sort.Search(len(x.rows), func(i int) bool { return x.rows[i].t >= s })
	agg := map[string]float64{}
	for ; i < len(x.rows) && x.rows[i].t < e; i++ {
		agg[x.rows[i].label] += x.rows[i].w
	}
	best, bw := "", 0.0
	for l, w := range agg {
		if w > bw || (w == bw && l < best) {
			best, bw = l, w
		}
	}
	if best == "" {
		if status := rootcause.UntimedSampleStatus(x.untimedSpans, s-x.origin, e-x.origin, x.offset); status != "" {
			return status
		}
		return "No samples in this interval"
	}
	return best
}

func overlap(s1, e1, s2, e2 float64) float64 {
	lo, hi := s1, e1
	if s2 > lo {
		lo = s2
	}
	if e2 < hi {
		hi = e2
	}
	if hi > lo {
		return hi - lo
	}
	return 0
}

func eventLabel(r *model.Record) string {
	if d := stringDetail(r); d != "" {
		return r.EventType + " " + d
	}
	return r.EventType
}

// stringDetail is Details only when it is a plain JSON string.
func stringDetail(r *model.Record) string {
	var s string
	if len(r.Details) > 0 && json.Unmarshal(r.Details, &s) == nil {
		return s
	}
	return ""
}

func slowFrames(rec *model.Recording, idx *sampleIndex, jankMs float64) []SlowFrame {
	out := []SlowFrame{}
	for _, f := range profile.Frames(rec, rec.StartTime, rec.EndTime) {
		if f.Class != profile.FrameActive || f.DurationMs <= jankMs {
			continue
		}
		work := f.Occupancy
		sf := SlowFrame{
			T: f.Start - rec.StartTime, Ms: f.DurationMs, WorkMs: work.WorkMs,
			ScriptMs: work.ScriptMs, LayoutMs: work.LayoutMs + work.StyleMs,
			PaintMs: work.PaintMs + work.CompositeMs, GCMs: work.GCMs,
			GCOverlapWithWorkMs: work.GCOverlapWithWorkMs,
		}
		byEvent := map[string]float64{}
		for _, cost := range profile.ExclusiveRecords(rec, f.Start, f.End) {
			if cost.Record.Type == model.TypeScript {
				byEvent[eventLabel(&cost.Record)] += cost.ExclusiveMs
			}
		}
		best, bm := "", 0.0
		for l, ms := range byEvent {
			if ms > bm || (ms == bm && l < best) {
				best, bm = l, ms
			}
		}
		sf.TopEvent = best
		sf.TopFunc = idx.top(f.Start, f.End)
		out = append(out, sf)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].WorkMs != out[j].WorkMs {
			return out[i].WorkMs > out[j].WorkMs
		}
		if out[i].Ms != out[j].Ms {
			return out[i].Ms > out[j].Ms
		}
		if out[i].T != out[j].T {
			return out[i].T < out[j].T
		}
		if out[i].TopEvent != out[j].TopEvent {
			return out[i].TopEvent < out[j].TopEvent
		}
		return out[i].TopFunc < out[j].TopFunc
	})
	return out
}

func inputStalls(rec *model.Recording, idx *sampleIndex) []InputStall {
	out := []InputStall{}
	for _, f := range profile.Frames(rec, rec.StartTime, rec.EndTime) {
		if f.Class != profile.FrameInputStall {
			continue
		}
		out = append(out, InputStall{
			T: f.Start - rec.StartTime, Ms: f.DurationMs, WorkMs: f.Occupancy.WorkMs,
			TopEvent: f.DominantTrigger, TopFunc: idx.top(f.Start, f.End),
		})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Ms != out[j].Ms {
			return out[i].Ms > out[j].Ms
		}
		return out[i].T < out[j].T
	})
	return out
}

func longTasks(rec *model.Recording, idx *sampleIndex, thr float64) []LongTask {
	out := []LongTask{}
	for _, task := range longTaskSpans(rec, thr) {
		out = append(out, LongTask{
			T:         task.StartTime - rec.StartTime,
			Ms:        (task.EndTime - task.StartTime) * 1000,
			EventType: task.EventType, Children: task.Children, ChildrenTotal: len(task.Children), Detail: detailString(&task.Record),
			TopFunc: idx.top(task.StartTime, task.EndTime),
		})
	}
	return out
}

type indexedSample struct {
	t, w  float64
	label string
}
