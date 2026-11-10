// Package analyze turns one decoded recording into a metrics.Summary.
package analyze

import (
	"encoding/json"
	"fmt"
	"math"
	"sort"
	"strings"

	"github.com/omniviewdev/wkperf/internal/flame"
	"github.com/omniviewdev/wkperf/internal/metrics"
	"github.com/omniviewdev/wkperf/internal/model"
	"github.com/omniviewdev/wkperf/internal/profile"
)

const (
	mb = 1 << 20
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

func (b *builder) addExt(key, group, label string, unit metrics.Unit, dir metrics.Direction, v float64) {
	b.m = append(b.m, metrics.Metric{Key: key, Group: group, Label: label, Unit: unit, Dir: dir, Value: v, Extensive: true})
}

// timeStats emits count/total/mean/p95/max for durations in ms under prefix.
func (b *builder) timeStats(prefix, group, label string, ms []float64, countOnly bool) {
	sort.Float64s(ms)
	b.addExt(prefix+".count", group, label+" count", metrics.UnitCount, metrics.LowerBetter, float64(len(ms)))
	if countOnly || len(ms) == 0 {
		return
	}
	b.addExt(prefix+".total_ms", group, label+" total", metrics.UnitMs, metrics.LowerBetter, sum(ms))
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

// Options select the part of a recording to analyze. All values are seconds
// relative to the recording start. The zero value analyzes the whole recording.
type Options struct {
	TrimStartSec float64 // drop the first N seconds (e.g. warm-up)
	TrimEndSec   float64 // drop the last N seconds
	FromSec      float64 // explicit window start
	ToSec        float64 // explicit window end; 0 = recording end
}

// window is an absolute [lo,hi] time range; active is false when it covers the
// whole recording unchanged.
type window struct {
	lo, hi    float64
	active    bool
	bounded   bool
	intervals []profile.Interval
}

func (o Options) window(rec *model.Recording) window {
	lo := rec.StartTime + math.Max(0, math.Max(o.TrimStartSec, o.FromSec))
	hi := rec.EndTime - math.Max(0, o.TrimEndSec)
	if o.ToSec > 0 {
		hi = math.Min(hi, rec.StartTime+o.ToSec)
	}
	active := lo != rec.StartTime || hi != rec.EndTime
	return window{lo: lo, hi: hi, active: active, bounded: active || rec.EndTime > rec.StartTime || len(rec.Discontinuities) > 0, intervals: profile.ActiveIntervals(rec, lo, hi)}
}

func (w window) dur() float64 {
	var total float64
	for _, span := range w.intervals {
		total += span.End - span.Start
	}
	return total
}

// at reports whether instant t lies in the window.
func (w window) at(t float64) bool {
	return !w.bounded || profile.Contains(w.intervals, t)
}

// span clips [start,end] to the window and reports whether anything remains.
// Zero-length spans count when their start lies in the window.
func (w window) span(start, end float64) (float64, float64, bool) {
	if !w.bounded {
		return start, end, true
	}
	if end <= start {
		return start, end, w.at(start)
	}
	if end <= w.lo || start >= w.hi {
		return 0, 0, false
	}
	start, end = math.Max(start, w.lo), math.Min(end, w.hi)
	return start, end, w.duration(start, end) > 0
}

func (w window) duration(start, end float64) float64 {
	if !w.bounded {
		return math.Max(0, end-start)
	}
	var total float64
	i := sort.Search(len(w.intervals), func(i int) bool { return w.intervals[i].End > start })
	for ; i < len(w.intervals) && w.intervals[i].Start < end; i++ {
		total += math.Min(end, w.intervals[i].End) - math.Max(start, w.intervals[i].Start)
	}
	return total
}

// Summarize analyzes the whole of rec and returns flat metrics, function
// stats and chart series.
func Summarize(rec *model.Recording, source string) *metrics.Summary {
	return SummarizeWith(rec, source, Options{})
}

// SummarizeWith is Summarize restricted to the window selected by o. Records
// are kept when they overlap the window and their spans clipped to it;
// samples, CPU, memory and pressure events are kept by timestamp. DurationSec
// and chart series offsets are relative to the window start.
func SummarizeWith(rec *model.Recording, source string, o Options) *metrics.Summary {
	return summarize(rec, source, o, nil)
}

// SummarizeWithTree reuses one full-recording tree across reports and analysis.
func SummarizeWithTree(rec *model.Recording, source string, tree *flame.Tree) *metrics.Summary {
	return summarize(rec, source, Options{}, tree)
}

func summarize(rec *model.Recording, source string, o Options, tree *flame.Tree) *metrics.Summary {
	s := &metrics.Summary{Source: source}
	if rec == nil {
		return s
	}
	s.Name = rec.DisplayName
	win := o.window(rec)
	duration := win.dur()
	s.WallDurationSec = math.Max(0, win.hi-win.lo)
	s.PausedDurationSec = math.Max(0, s.WallDurationSec-duration)
	s.DurationSec = duration
	origin := rec.StartTime
	if win.active {
		origin = win.lo
	}

	var (
		frames       []timedPoint
		scriptMs     = map[string][]float64{}
		scriptCount  = map[string]int{}
		layoutMs     = map[string][]float64{}
		layoutCount  = map[string]int{}
		gcCount      int
		cpuTotal     []timedPoint
		cpuMain      []float64
		memTotal     []timedPoint
		memCatPeak   = map[string]float64{}
		netMs        []float64
		netBytes     float64
		netTransfers []float64
		haveNetwork  bool
		haveScript   bool
		haveLayout   bool
		nRecords     int
		urls         = map[string]bool{}
	)

	frameData := profile.Frames(rec, win.lo, win.hi)
	idleGapMs := 0.0
	for _, f := range frameData {
		if f.IdleGap {
			s.IdleWakeups = append(s.IdleWakeups, metrics.IdleWakeup{StartSec: f.Start - origin, DurationMs: f.DurationMs, WorkMs: f.Occupancy.WorkMs, DominantTrigger: f.DominantTrigger})
			idleGapMs += f.DurationMs
			continue
		}
		frames = append(frames, timedPoint{f.Start, f.DurationMs})
	}
	for i := range rec.Records {
		r := &rec.Records[i]
		var start, end float64
		var keep bool
		switch r.Type {
		case model.TypeCPU, model.TypeMemory:
			keep = win.at(r.Timestamp)
		case model.TypeNetwork:
			// Network records may lack timing; keep those rather than guess.
			if r.StartTime == 0 && r.EndTime == 0 {
				keep = true
			} else {
				start, end, keep = win.span(r.StartTime, r.EndTime)
			}
		default:
			start, end, keep = win.span(r.StartTime, r.EndTime)
		}
		if !keep {
			continue
		}
		nRecords++
		if r.Target != nil && r.Target.URL != "" {
			urls[r.Target.URL] = true
		}
		clipped := win.duration(start, end)
		switch r.Type {
		case model.TypeScript:
			if profile.IsGC(*r) {
				gcCount++
				continue
			}
			haveScript = true
			scriptCount[r.EventType]++
			if !countOnlyEvents[r.EventType] {
				ms := clipped * 1000
				scriptMs[r.EventType] = append(scriptMs[r.EventType], ms)
			}
		case model.TypeLayout:
			haveLayout = true
			layoutCount[r.EventType]++
			ms := clipped * 1000
			layoutMs[r.EventType] = append(layoutMs[r.EventType], ms)
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
				netTransfers = append(netTransfers, bytes)
			}
		}
	}
	nPressure := 0
	for _, e := range rec.MemoryPressureEvents {
		if win.at(e.Timestamp) {
			nPressure++
		}
	}
	for _, mk := range rec.Markers {
		if win.at(mk.Time) {
			if u := markerURL(mk.Details); u != "" {
				urls[u] = true
			}
		}
	}

	sort.Slice(frames, func(i, j int) bool {
		if frames[i].t != frames[j].t {
			return frames[i].t < frames[j].t
		}
		return frames[i].v < frames[j].v
	})
	sort.Slice(cpuTotal, func(i, j int) bool {
		if cpuTotal[i].t != cpuTotal[j].t {
			return cpuTotal[i].t < cpuTotal[j].t
		}
		return cpuTotal[i].v < cpuTotal[j].v
	})
	sort.Slice(memTotal, func(i, j int) bool {
		if memTotal[i].t != memTotal[j].t {
			return memTotal[i].t < memTotal[j].t
		}
		return memTotal[i].v < memTotal[j].v
	})
	sort.Float64s(cpuMain)
	sort.Float64s(netMs)
	sort.Float64s(netTransfers)
	netBytes = sum(netTransfers)
	work := profile.Measure(rec, win.lo, win.hi)
	scriptTimeMs := work.ScriptMs
	layoutTimeMs := work.StyleMs + work.LayoutMs + work.PaintMs + work.CompositeMs

	b := &builder{}
	b.add("overview.duration_s", "overview", "Recording duration", "s", metrics.Neutral, duration)
	b.addExt("overview.records", "overview", "Records", metrics.UnitCount, metrics.Neutral, float64(nRecords))
	b.add("overview.wall_duration_s", "overview", "Capture wall duration", "s", metrics.Neutral, s.WallDurationSec)
	b.add("overview.paused_s", "overview", "Paused capture duration", "s", metrics.Neutral, s.PausedDurationSec)
	if len(frameData) > 0 {
		b.addExt("frames.idle_wakeups", "frames", "Idle wakeups", metrics.UnitCount, metrics.LowerBetter, float64(len(s.IdleWakeups)))
		b.addExt("frames.idle_gap_ms", "frames", "Idle rendering intervals", metrics.UnitMs, metrics.Neutral, idleGapMs)
		if len(frames) == 0 {
			b.addExt("frames.count", "frames", "Active frames", metrics.UnitCount, metrics.Neutral, 0)
		}
	}

	if len(frames) > 0 {
		ms := make([]float64, len(frames))
		for i, f := range frames {
			ms[i] = f.v
		}
		s.FrameMs = ms
		sorted := append([]float64(nil), ms...)
		sort.Float64s(sorted)
		meanMs := mean(sorted)
		b.addExt("frames.count", "frames", "Frames", metrics.UnitCount, metrics.Neutral, float64(len(ms)))
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
		b.addExt("frames.jank_16ms", "frames", "Frames > 16.7ms", metrics.UnitCount, metrics.LowerBetter, float64(j16))
		b.addExt("frames.jank_33ms", "frames", "Frames > 33.4ms", metrics.UnitCount, metrics.LowerBetter, float64(j33))
		b.addExt("frames.jank_50ms", "frames", "Frames > 50ms", metrics.UnitCount, metrics.LowerBetter, float64(j50))
		b.add("frames.jank_ratio", "frames", "Jank ratio (>16.7ms)", metrics.UnitRatio, metrics.LowerBetter, float64(j16)/float64(len(ms)))
	}

	var eventSet []string
	if haveScript {
		b.addExt("script.total_ms", "script", "Script total", metrics.UnitMs, metrics.LowerBetter, scriptTimeMs)
		if duration > 0 {
			b.add("script.busy_ratio", "script", "Script busy ratio", metrics.UnitRatio, metrics.LowerBetter, scriptTimeMs/1000/duration)
		}
		for _, et := range sortedKeys(scriptCount) {
			eventSet = append(eventSet, et)
			if countOnlyEvents[et] {
				b.addExt("script."+et+".count", "script", et+" count", metrics.UnitCount, metrics.LowerBetter, float64(scriptCount[et]))
				continue
			}
			b.timeStats("script."+et, "script", et, scriptMs[et], false)
		}
	}

	if haveLayout {
		b.addExt("layout.total_ms", "layout", "Layout total", metrics.UnitMs, metrics.LowerBetter, layoutTimeMs)
		for _, et := range sortedKeys(layoutCount) {
			b.timeStats("layout."+et, "layout", et, layoutMs[et], false)
		}
	}

	if gcCount > 0 {
		b.addExt("gc.count", "gc", "GC count", metrics.UnitCount, metrics.LowerBetter, float64(gcCount))
		b.addExt("gc.total_ms", "gc", "GC total", metrics.UnitMs, metrics.LowerBetter, work.GCMs)
	}

	if len(cpuTotal) > 0 {
		vals := make([]float64, len(cpuTotal))
		for i, p := range cpuTotal {
			vals[i] = p.v
			s.CPU = append(s.CPU, metrics.Point{T: p.t - origin, V: p.v})
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
			s.MemoryMB = append(s.MemoryMB, metrics.Point{T: p.t - origin, V: vals[i]})
		}
		b.add("memory.peak_mb", "memory", "Memory peak", "MB", metrics.LowerBetter, maxOf(vals))
		b.add("memory.final_mb", "memory", "Memory final", "MB", metrics.LowerBetter, vals[len(vals)-1])
		b.add("memory.mean_mb", "memory", "Memory mean", "MB", metrics.LowerBetter, mean(vals))
		for _, c := range sortedKeys(memCatPeak) {
			b.add("memory."+c+".peak_mb", "memory", c+" peak", "MB", metrics.LowerBetter, memCatPeak[c]/mb)
		}
	}

	if haveNetwork {
		b.addExt("network.requests", "network", "Requests", metrics.UnitCount, metrics.Neutral, float64(len(netMs)))
		b.addExt("network.transfer_bytes", "network", "Transferred", metrics.UnitBytes, metrics.LowerBetter, netBytes)
		b.add("network.mean_ms", "network", "Request time mean", metrics.UnitMs, metrics.LowerBetter, mean(netMs))
		b.add("network.p95_ms", "network", "Request time p95", metrics.UnitMs, metrics.LowerBetter, Percentile(netMs, 95))
	}

	if nPressure > 0 {
		b.addExt("pressure.events", "memory", "Memory pressure events", metrics.UnitCount, metrics.LowerBetter, float64(nPressure))
	}

	s.Metrics = b.m
	if tree == nil {
		if win.active {
			tree = flame.Build(rec, flame.Options{Intervals: relativeIntervals(rec, win.intervals)})
		} else {
			tree = flame.Build(rec)
		}
	}
	applyTree(s, tree)
	nSamples := len(tree.Samples)

	instruments := append([]string(nil), rec.InstrumentTypes...)
	sort.Strings(instruments)
	s.Fixture = metrics.Fixture{
		DurationSec:     duration,
		Frames:          len(frames),
		Records:         nRecords,
		SampleCount:     nSamples,
		URLs:            sortedKeys(urls),
		Instruments:     instruments,
		ScriptEventSet:  eventSet,
		TopFunctionKeys: topFunctionKeys(s.Functions, topFunctionCount),
	}
	if win.active {
		s.Fixture.Window = fmt.Sprintf("%.1fs-%.1fs", win.lo-rec.StartTime, math.Max(win.lo, win.hi)-rec.StartTime)
	}
	return s
}

// topFunctionCount is the size of the workload fingerprint in Fixture.
const topFunctionCount = 20

// topFunctionKeys returns the Key of the first n functions with self time;
// fs must be sorted by self time descending.
func topFunctionKeys(fs []metrics.FuncStat, n int) []string {
	var out []string
	for _, f := range fs {
		if len(out) == n || f.SelfMs <= 0 {
			break
		}
		out = append(out, f.Key())
	}
	return out
}

// markerURL extracts a URL from a marker's details: either a JSON string that
// looks like a URL or an object with a "url" field.
func markerURL(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	var str string
	if json.Unmarshal(raw, &str) == nil {
		if strings.HasPrefix(str, "http://") || strings.HasPrefix(str, "https://") {
			return str
		}
		return ""
	}
	var obj struct {
		URL string `json:"url"`
	}
	if json.Unmarshal(raw, &obj) == nil {
		return obj.URL
	}
	return ""
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
