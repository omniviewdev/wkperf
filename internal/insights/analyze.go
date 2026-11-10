package insights

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/omniviewdev/wkperf/internal/analyze"
	"github.com/omniviewdev/wkperf/internal/metrics"
	"github.com/omniviewdev/wkperf/internal/model"
)

const defaultSampleSeconds = 0.001

func (o Options) withDefaults() Options {
	if o.Top <= 0 {
		o.Top = 10
	}
	if o.JankMs <= 0 {
		o.JankMs = 16.7
	}
	if o.LongTaskMs <= 0 {
		o.LongTaskMs = 50
	}
	return o
}

// Analyze profiles one recording. It never panics on empty or nil input.
func Analyze(rec *model.Recording, source string, o Options) *Report {
	o = o.withDefaults()
	if rec == nil {
		rec = &model.Recording{}
	}
	sum := analyze.Summarize(rec, source)
	r := &Report{
		Source:      source,
		Name:        rec.DisplayName,
		DurationSec: sum.DurationSec,
		Summary:     sum,
		Breakdown:   computeBreakdown(rec),
		Hotspots:    []metrics.FuncStat{},
		Modules:     []ModuleStat{},
		SlowFrames:  []SlowFrame{},
		LongTasks:   []LongTask{},
		Findings:    []Finding{},
	}
	for _, f := range sum.Functions {
		if len(r.Hotspots) >= o.Top || f.SelfMs <= 0 {
			break
		}
		r.Hotspots = append(r.Hotspots, f)
	}
	mods := computeModules(sum.Functions)
	if len(mods) > o.Top {
		r.Modules = mods[:o.Top]
	} else if mods != nil {
		r.Modules = mods
	}

	idx := newSampleIndex(rec)
	allSlow := slowFrames(rec, idx, o.JankMs)
	allLong := longTasks(rec, idx, o.LongTaskMs)
	r.SlowFrames = topN(allSlow, o.Top)
	r.LongTasks = topNLong(allLong, o.Top)

	ctx := &ruleCtx{rec: rec, sum: sum, rep: r, opt: o, mods: mods, slow: allSlow, long: allLong, wallMs: sum.DurationSec * 1000}
	r.Findings = runRules(ctx)
	return r
}

func topN(in []SlowFrame, n int) []SlowFrame {
	if len(in) > n {
		in = in[:n]
	}
	if in == nil {
		return []SlowFrame{}
	}
	return in
}

func topNLong(in []LongTask, n int) []LongTask {
	if len(in) > n {
		in = in[:n]
	}
	if in == nil {
		return []LongTask{}
	}
	return in
}

func recIsGC(r *model.Record) bool {
	if r.EventType == "garbage-collected" {
		return true
	}
	if len(r.Details) == 0 {
		return false
	}
	var d struct {
		Type string `json:"__type"`
	}
	return json.Unmarshal(r.Details, &d) == nil && d.Type == "GarbageCollection"
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

// moduleOf maps a script URL to a module name: npm package from the last
// node_modules segment (scoped packages keep @scope/pkg), else the URL
// directory, else "(native)". Query strings and hashes are dropped.
func moduleOf(url string) string {
	if i := strings.IndexAny(url, "?#"); i >= 0 {
		url = url[:i]
	}
	if url == "" || strings.HasPrefix(url, "[") || strings.Contains(url, "native code") {
		return "(native)"
	}
	if i := strings.LastIndex(url, "node_modules/"); i >= 0 {
		parts := strings.Split(url[i+len("node_modules/"):], "/")
		if len(parts) > 0 && parts[0] != "" {
			if strings.HasPrefix(parts[0], "@") && len(parts) > 1 {
				return parts[0] + "/" + parts[1]
			}
			return parts[0]
		}
	}
	if i := strings.LastIndex(url, "/"); i > 0 && !strings.HasSuffix(url[:i], ":/") && !strings.HasSuffix(url[:i], ":") {
		return url[:i]
	}
	return url
}

func computeModules(fns []metrics.FuncStat) []ModuleStat {
	agg := map[string]float64{}
	total := 0.0
	for _, f := range fns {
		if f.SelfMs <= 0 {
			continue
		}
		agg[moduleOf(f.URL)] += f.SelfMs
		total += f.SelfMs
	}
	var out []ModuleStat
	for n, ms := range agg {
		out = append(out, ModuleStat{Name: n, SelfMs: ms, Pct: ms / total * 100})
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
	t     []float64
	w     []float64
	label []string
}

func funcLabel(f model.StackFrame) string {
	name := f.Name
	if name == "" {
		name = "(anonymous)"
	}
	u := f.URL
	if i := strings.IndexAny(u, "?#"); i >= 0 {
		u = u[:i]
	}
	if i := strings.LastIndex(u, "/"); i >= 0 {
		u = u[i+1:]
	}
	if u == "" {
		return name
	}
	return fmt.Sprintf("%s (%s:%d)", name, u, f.Line)
}

func newSampleIndex(rec *model.Recording) *sampleIndex {
	type row struct {
		t, w  float64
		label string
	}
	var rows []row
	for _, ts := range rec.Samples {
		for i, st := range ts.StackTraces {
			if len(st.StackFrames) == 0 {
				continue
			}
			w := defaultSampleSeconds
			if i < len(ts.Durations) {
				w = ts.Durations[i]
			}
			rows = append(rows, row{st.Timestamp, w, funcLabel(st.StackFrames[len(st.StackFrames)-1])})
		}
	}
	sort.SliceStable(rows, func(i, j int) bool { return rows[i].t < rows[j].t })
	x := &sampleIndex{}
	for _, r := range rows {
		x.t = append(x.t, r.t)
		x.w = append(x.w, r.w)
		x.label = append(x.label, r.label)
	}
	return x
}

// top returns the hottest self function among samples with ts in [s,e).
func (x *sampleIndex) top(s, e float64) string {
	i := sort.SearchFloat64s(x.t, s)
	agg := map[string]float64{}
	for ; i < len(x.t) && x.t[i] < e; i++ {
		agg[x.label[i]] += x.w[i]
	}
	best, bw := "", 0.0
	for l, w := range agg {
		if w > bw || (w == bw && l < best) {
			best, bw = l, w
		}
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
	var out []SlowFrame
	for i := range rec.Records {
		f := &rec.Records[i]
		if f.Type != model.TypeRenderingFrame || f.Duration()*1000 <= jankMs {
			continue
		}
		sf := SlowFrame{T: f.StartTime - rec.StartTime, Ms: f.Duration() * 1000}
		byEvent := map[string]float64{}
		for j := range rec.Records {
			r := &rec.Records[j]
			ov := overlap(f.StartTime, f.EndTime, r.StartTime, r.EndTime) * 1000
			if ov == 0 {
				continue
			}
			switch r.Type {
			case model.TypeScript:
				if recIsGC(r) {
					sf.GCMs += ov
				} else {
					sf.ScriptMs += ov
					byEvent[eventLabel(r)] += ov
				}
			case model.TypeLayout:
				switch {
				case isStyleEvent(r.EventType) || isLayoutEvent(r.EventType):
					sf.LayoutMs += ov
				case isPaintEvent(r.EventType) || r.EventType == "composite":
					sf.PaintMs += ov
				}
			}
		}
		best, bm := "", 0.0
		for l, ms := range byEvent {
			if ms > bm || (ms == bm && l < best) {
				best, bm = l, ms
			}
		}
		sf.TopEvent = best
		sf.TopFunc = idx.top(f.StartTime, f.EndTime)
		out = append(out, sf)
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Ms > out[j].Ms })
	return out
}

func longTasks(rec *model.Recording, idx *sampleIndex, thr float64) []LongTask {
	var out []LongTask
	for i := range rec.Records {
		r := &rec.Records[i]
		if r.Type != model.TypeScript || recIsGC(r) || r.Duration()*1000 <= thr {
			continue
		}
		out = append(out, LongTask{
			T: r.StartTime - rec.StartTime, Ms: r.Duration() * 1000,
			EventType: r.EventType, Detail: detailString(r),
			TopFunc: idx.top(r.StartTime, r.EndTime),
		})
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Ms > out[j].Ms })
	return out
}

func modelFrame(name, url string, line int) model.StackFrame {
	return model.StackFrame{Name: name, URL: url, Line: line}
}
