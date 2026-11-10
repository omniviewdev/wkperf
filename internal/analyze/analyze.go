// Package analyze turns one decoded recording into a metrics.Summary.
package analyze

import (
	"encoding/json"
	"math"
	"sort"

	"github.com/omniviewdev/wkperf/internal/metrics"
	"github.com/omniviewdev/wkperf/internal/model"
)

const (
	mb                   = 1 << 20
	defaultSampleSeconds = 0.001
)

// countOnlyEvents are instantaneous script events: only their count is meaningful.
var countOnlyEvents = map[string]bool{
	"timer-installed":           true,
	"timer-removed":             true,
	"animation-frame-requested": true,
	"animation-frame-canceled":  true,
}

// Percentile returns the p-th percentile (0..100) of vals using linear
// interpolation between closest ranks: rank = p/100*(n-1). The input is not
// modified. It returns 0 for an empty slice.
func Percentile(vals []float64, p float64) float64 {
	if len(vals) == 0 {
		return 0
	}
	s := append([]float64(nil), vals...)
	sort.Float64s(s)
	return percentileSorted(s, p)
}

func percentileSorted(s []float64, p float64) float64 {
	n := len(s)
	if n == 1 {
		return s[0]
	}
	if p <= 0 {
		return s[0]
	}
	if p >= 100 {
		return s[n-1]
	}
	rank := p / 100 * float64(n-1)
	lo := int(math.Floor(rank))
	frac := rank - float64(lo)
	if lo+1 >= n {
		return s[n-1]
	}
	return s[lo] + (s[lo+1]-s[lo])*frac
}

func mean(v []float64) float64 {
	if len(v) == 0 {
		return 0
	}
	return sum(v) / float64(len(v))
}

func sum(v []float64) float64 {
	t := 0.0
	for _, x := range v {
		t += x
	}
	return t
}

func maxOf(v []float64) float64 {
	m := math.Inf(-1)
	for _, x := range v {
		if x > m {
			m = x
		}
	}
	return m
}

type builder struct {
	m []metrics.Metric
}

func (b *builder) add(key, group, label string, unit metrics.Unit, dir metrics.Direction, v float64) {
	b.m = append(b.m, metrics.Metric{Key: key, Group: group, Label: label, Unit: unit, Dir: dir, Value: v})
}

// timeStats emits count/total/mean/p95/max for durations in ms under prefix.
func (b *builder) timeStats(prefix, group, label string, ms []float64, countOnly bool) {
	b.add(prefix+".count", group, label+" count", metrics.UnitCount, metrics.LowerBetter, float64(len(ms)))
	if countOnly || len(ms) == 0 {
		return
	}
	b.add(prefix+".total_ms", group, label+" total", metrics.UnitMs, metrics.LowerBetter, sum(ms))
	b.add(prefix+".mean_ms", group, label+" mean", metrics.UnitMs, metrics.LowerBetter, mean(ms))
	b.add(prefix+".p95_ms", group, label+" p95", metrics.UnitMs, metrics.LowerBetter, Percentile(ms, 95))
	b.add(prefix+".max_ms", group, label+" max", metrics.UnitMs, metrics.LowerBetter, maxOf(ms))
}

type timedPoint struct {
	t, v float64
}

func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// Summarize analyzes rec and returns flat metrics, function stats and chart series.
func Summarize(rec *model.Recording, source string) *metrics.Summary {
	s := &metrics.Summary{Source: source}
	if rec == nil {
		return s
	}
	s.Name = rec.DisplayName
	duration := rec.EndTime - rec.StartTime
	if duration < 0 {
		duration = 0
	}
	s.DurationSec = duration

	var (
		frames       []timedPoint
		scriptMs     = map[string][]float64{}
		scriptCount  = map[string]int{}
		layoutMs     = map[string][]float64{}
		layoutCount  = map[string]int{}
		gcMs         []float64
		cpuTotal     []timedPoint
		cpuMain      []float64
		memTotal     []timedPoint
		memCatPeak   = map[string]float64{}
		netMs        []float64
		netBytes     float64
		haveNetwork  bool
		haveScript   bool
		haveLayout   bool
		scriptTimeMs float64
		layoutTimeMs float64
	)

	for i := range rec.Records {
		r := &rec.Records[i]
		switch r.Type {
		case model.TypeRenderingFrame:
			if d := r.Duration(); d > 0 {
				frames = append(frames, timedPoint{r.StartTime, d * 1000})
			}
		case model.TypeScript:
			if isGC(r) {
				gcMs = append(gcMs, r.Duration()*1000)
				continue
			}
			haveScript = true
			scriptCount[r.EventType]++
			if !countOnlyEvents[r.EventType] {
				ms := r.Duration() * 1000
				scriptMs[r.EventType] = append(scriptMs[r.EventType], ms)
				scriptTimeMs += ms
			}
		case model.TypeLayout:
			haveLayout = true
			layoutCount[r.EventType]++
			ms := r.Duration() * 1000
			layoutMs[r.EventType] = append(layoutMs[r.EventType], ms)
			layoutTimeMs += ms
		case model.TypeCPU:
			cpuTotal = append(cpuTotal, timedPoint{r.Timestamp, r.Usage})
			for _, th := range r.Threads {
				if th.Type == "main" {
					cpuMain = append(cpuMain, th.Usage)
					break
				}
			}
		case model.TypeMemory:
			total := 0.0
			for _, c := range r.Categories {
				total += c.Size
				if v, ok := memCatPeak[c.Type]; !ok || c.Size > v {
					memCatPeak[c.Type] = c.Size
				}
			}
			memTotal = append(memTotal, timedPoint{r.Timestamp, total})
		case model.TypeNetwork:
			if t, bytes, ok := parseHAR(r.Entry); ok {
				haveNetwork = true
				netMs = append(netMs, t)
				netBytes += bytes
			}
		}
	}

	sort.SliceStable(frames, func(i, j int) bool { return frames[i].t < frames[j].t })
	sort.SliceStable(cpuTotal, func(i, j int) bool { return cpuTotal[i].t < cpuTotal[j].t })
	sort.SliceStable(memTotal, func(i, j int) bool { return memTotal[i].t < memTotal[j].t })

	b := &builder{}
	b.add("overview.duration_s", "overview", "Recording duration", "s", metrics.Neutral, duration)
	b.add("overview.records", "overview", "Records", metrics.UnitCount, metrics.Neutral, float64(len(rec.Records)))

	if len(frames) > 0 {
		ms := make([]float64, len(frames))
		for i, f := range frames {
			ms[i] = f.v
		}
		s.FrameMs = ms
		sorted := append([]float64(nil), ms...)
		sort.Float64s(sorted)
		meanMs := mean(ms)
		b.add("frames.count", "frames", "Frames", metrics.UnitCount, metrics.Neutral, float64(len(ms)))
		b.add("frames.mean_ms", "frames", "Frame time mean", metrics.UnitMs, metrics.LowerBetter, meanMs)
		b.add("frames.p50_ms", "frames", "Frame time p50", metrics.UnitMs, metrics.LowerBetter, percentileSorted(sorted, 50))
		b.add("frames.p95_ms", "frames", "Frame time p95", metrics.UnitMs, metrics.LowerBetter, percentileSorted(sorted, 95))
		b.add("frames.p99_ms", "frames", "Frame time p99", metrics.UnitMs, metrics.LowerBetter, percentileSorted(sorted, 99))
		b.add("frames.max_ms", "frames", "Frame time max", metrics.UnitMs, metrics.LowerBetter, sorted[len(sorted)-1])
		b.add("frames.fps", "frames", "Average FPS", metrics.UnitFPS, metrics.HigherBetter, 1000/meanMs)
		j16, j33, j50 := 0, 0, 0
		for _, v := range ms {
			if v > 16.7 {
				j16++
			}
			if v > 33.4 {
				j33++
			}
			if v > 50 {
				j50++
			}
		}
		b.add("frames.jank_16ms", "frames", "Frames > 16.7ms", metrics.UnitCount, metrics.LowerBetter, float64(j16))
		b.add("frames.jank_33ms", "frames", "Frames > 33.4ms", metrics.UnitCount, metrics.LowerBetter, float64(j33))
		b.add("frames.jank_50ms", "frames", "Frames > 50ms", metrics.UnitCount, metrics.LowerBetter, float64(j50))
		b.add("frames.jank_ratio", "frames", "Jank ratio (>16.7ms)", metrics.UnitRatio, metrics.LowerBetter, float64(j16)/float64(len(ms)))
	}

	if haveScript {
		b.add("script.total_ms", "script", "Script total", metrics.UnitMs, metrics.LowerBetter, scriptTimeMs)
		if duration > 0 {
			b.add("script.busy_ratio", "script", "Script busy ratio", metrics.UnitRatio, metrics.LowerBetter, scriptTimeMs/1000/duration)
		}
		for _, et := range sortedKeys(scriptCount) {
			if countOnlyEvents[et] {
				b.add("script."+et+".count", "script", et+" count", metrics.UnitCount, metrics.LowerBetter, float64(scriptCount[et]))
				continue
			}
			b.timeStats("script."+et, "script", et, scriptMs[et], false)
		}
	}

	if haveLayout {
		b.add("layout.total_ms", "layout", "Layout total", metrics.UnitMs, metrics.LowerBetter, layoutTimeMs)
		for _, et := range sortedKeys(layoutCount) {
			b.timeStats("layout."+et, "layout", et, layoutMs[et], false)
		}
	}

	if len(gcMs) > 0 {
		b.add("gc.count", "gc", "GC count", metrics.UnitCount, metrics.LowerBetter, float64(len(gcMs)))
		b.add("gc.total_ms", "gc", "GC total", metrics.UnitMs, metrics.LowerBetter, sum(gcMs))
	}

	if len(cpuTotal) > 0 {
		vals := make([]float64, len(cpuTotal))
		for i, p := range cpuTotal {
			vals[i] = p.v
			s.CPU = append(s.CPU, metrics.Point{T: p.t - rec.StartTime, V: p.v})
		}
		b.add("cpu.mean_pct", "cpu", "CPU mean", metrics.UnitPct, metrics.LowerBetter, mean(vals))
		b.add("cpu.p95_pct", "cpu", "CPU p95", metrics.UnitPct, metrics.LowerBetter, Percentile(vals, 95))
		b.add("cpu.max_pct", "cpu", "CPU max", metrics.UnitPct, metrics.LowerBetter, maxOf(vals))
		if len(cpuMain) > 0 {
			b.add("cpu.main.mean_pct", "cpu", "Main thread CPU mean", metrics.UnitPct, metrics.LowerBetter, mean(cpuMain))
			b.add("cpu.main.p95_pct", "cpu", "Main thread CPU p95", metrics.UnitPct, metrics.LowerBetter, Percentile(cpuMain, 95))
		}
	}

	if len(memTotal) > 0 {
		vals := make([]float64, len(memTotal))
		for i, p := range memTotal {
			vals[i] = p.v / mb
			s.MemoryMB = append(s.MemoryMB, metrics.Point{T: p.t - rec.StartTime, V: vals[i]})
		}
		b.add("memory.peak_mb", "memory", "Memory peak", "MB", metrics.LowerBetter, maxOf(vals))
		b.add("memory.final_mb", "memory", "Memory final", "MB", metrics.LowerBetter, vals[len(vals)-1])
		b.add("memory.mean_mb", "memory", "Memory mean", "MB", metrics.LowerBetter, mean(vals))
		for _, c := range sortedKeys(memCatPeak) {
			b.add("memory."+c+".peak_mb", "memory", c+" peak", "MB", metrics.LowerBetter, memCatPeak[c]/mb)
		}
	}

	if haveNetwork {
		b.add("network.requests", "network", "Requests", metrics.UnitCount, metrics.Neutral, float64(len(netMs)))
		b.add("network.transfer_bytes", "network", "Transferred", metrics.UnitBytes, metrics.LowerBetter, netBytes)
		b.add("network.mean_ms", "network", "Request time mean", metrics.UnitMs, metrics.LowerBetter, mean(netMs))
		b.add("network.p95_ms", "network", "Request time p95", metrics.UnitMs, metrics.LowerBetter, Percentile(netMs, 95))
	}

	if n := len(rec.MemoryPressureEvents); n > 0 {
		b.add("pressure.events", "memory", "Memory pressure events", metrics.UnitCount, metrics.LowerBetter, float64(n))
	}

	s.Metrics = b.m
	s.Functions = aggregateFunctions(rec.Samples)
	return s
}

func isGC(r *model.Record) bool {
	if r.EventType == "garbage-collected" {
		return true
	}
	if len(r.Details) == 0 {
		return false
	}
	var d struct {
		Type string `json:"__type"`
	}
	if json.Unmarshal(r.Details, &d) != nil {
		return false
	}
	return d.Type == "GarbageCollection"
}

// parseHAR extracts time (ms) and transferred bytes from a HAR entry.
func parseHAR(raw json.RawMessage) (timeMs, bytes float64, ok bool) {
	if len(raw) == 0 {
		return 0, 0, false
	}
	var e struct {
		Time     float64 `json:"time"`
		Response struct {
			BodySize     float64 `json:"bodySize"`
			TransferSize float64 `json:"_transferSize"`
		} `json:"response"`
	}
	if json.Unmarshal(raw, &e) != nil {
		return 0, 0, false
	}
	b := e.Response.TransferSize
	if b <= 0 {
		b = e.Response.BodySize
	}
	if b < 0 {
		b = 0
	}
	t := e.Time
	if t < 0 {
		t = 0
	}
	return t, b, true
}

func aggregateFunctions(targets []model.TargetSamples) []metrics.FuncStat {
	type key struct {
		name, url string
		line      int
	}
	agg := map[key]*metrics.FuncStat{}
	get := func(f model.StackFrame) (*metrics.FuncStat, key) {
		name := f.Name
		if name == "" {
			name = "(anonymous)"
		}
		k := key{name, f.URL, f.Line}
		st := agg[k]
		if st == nil {
			st = &metrics.FuncStat{Name: name, URL: f.URL, Line: f.Line}
			agg[k] = st
		}
		return st, k
	}
	for _, t := range targets {
		for i, st := range t.StackTraces {
			if len(st.StackFrames) == 0 {
				continue
			}
			w := defaultSampleSeconds
			if i < len(t.Durations) {
				w = t.Durations[i]
			}
			ms := w * 1000
			seen := map[key]bool{}
			for _, f := range st.StackFrames {
				fs, k := get(f)
				if !seen[k] {
					seen[k] = true
					fs.TotalMs += ms
				}
			}
			top, _ := get(st.StackFrames[len(st.StackFrames)-1])
			top.SelfMs += ms
			top.SelfSamples++
		}
	}
	out := make([]metrics.FuncStat, 0, len(agg))
	for _, v := range agg {
		out = append(out, *v)
	}
	sort.Slice(out, func(i, j int) bool {
		a, b := out[i], out[j]
		if a.SelfMs != b.SelfMs {
			return a.SelfMs > b.SelfMs
		}
		if a.TotalMs != b.TotalMs {
			return a.TotalMs > b.TotalMs
		}
		if a.Name != b.Name {
			return a.Name < b.Name
		}
		if a.URL != b.URL {
			return a.URL < b.URL
		}
		return a.Line < b.Line
	})
	return out
}
