package insights

import (
	"encoding/json"
	"fmt"
	"math"
	"sort"
	"strings"

	"github.com/omniviewdev/wkperf/internal/analyze"
	"github.com/omniviewdev/wkperf/internal/metrics"
	"github.com/omniviewdev/wkperf/internal/model"
	"github.com/omniviewdev/wkperf/internal/profile"
)

type ruleCtx struct {
	rec    *model.Recording
	sum    *metrics.Summary
	rep    *Report
	opt    Options
	mods   []ModuleStat
	slow   []SlowFrame
	long   []LongTask
	wallMs float64
	costs  []profile.RecordCost
}

// evStat aggregates records of one kind.
type evStat struct {
	n           int
	ms          float64 // inclusive record time
	exclusiveMs float64
}

type recStats struct {
	script         map[string]*evStat // eventType -> stat (non-GC)
	layout         map[string]*evStat
	gc             evStat
	eventNames     map[string]*evStat // event-dispatched details -> stat
	paintQuads     int                // paint records with a large quad
	maxQuad        float64
	forced         evStat
	forcedExplicit int
	afterScript    evStat
	selfTotalMs    float64
}

const largeQuadPx = 2_000_000

func quadArea(raw json.RawMessage) float64 {
	if len(raw) == 0 {
		return 0
	}
	var d struct {
		Quad []float64 `json:"quad"`
	}
	if json.Unmarshal(raw, &d) != nil || len(d.Quad) < 8 {
		return 0
	}
	minX, maxX, minY, maxY := math.Inf(1), math.Inf(-1), math.Inf(1), math.Inf(-1)
	for i := 0; i+1 < len(d.Quad); i += 2 {
		minX, maxX = math.Min(minX, d.Quad[i]), math.Max(maxX, d.Quad[i])
		minY, maxY = math.Min(minY, d.Quad[i+1]), math.Max(maxY, d.Quad[i+1])
	}
	return (maxX - minX) * (maxY - minY)
}

func (c *ruleCtx) collect() *recStats {
	st := &recStats{script: map[string]*evStat{}, layout: map[string]*evStat{}, eventNames: map[string]*evStat{}}
	get := func(m map[string]*evStat, k string) *evStat {
		if m[k] == nil {
			m[k] = &evStat{}
		}
		return m[k]
	}
	var scripts []span
	for i := range c.rec.Records {
		r := &c.rec.Records[i]
		if !profile.Observed(c.rec, *r) {
			continue
		}
		ms := profile.ActiveDuration(c.rec, r.StartTime, r.EndTime) * 1000
		switch r.Type {
		case model.TypeScript:
			if profile.IsGC(*r) {
				st.gc.n++
				st.gc.ms += ms
				continue
			}
			s := get(st.script, r.EventType)
			s.n++
			s.ms += ms
			if r.EventType == "event-dispatched" {
				name := stringDetail(r)
				if name == "" {
					name = "(unknown)"
				}
				e := get(st.eventNames, name)
				e.n++
				e.ms += ms
			}
			if ms > 0 {
				scripts = append(scripts, span{r.StartTime, r.EndTime})
			}
		case model.TypeLayout:
			s := get(st.layout, r.EventType)
			s.n++
			s.ms += ms
			if r.EventType == "paint" {
				if a := quadArea(r.Details); a > st.maxQuad {
					st.maxQuad = a
				}
				if quadArea(r.Details) >= largeQuadPx {
					st.paintQuads++
				}
			}
		}
	}
	type recordSpan struct {
		event      string
		start, end float64
	}
	forced := map[recordSpan]bool{}
	postScript := map[recordSpan]bool{}
	for _, r := range profile.ForcedLayoutRecords(c.rec) {
		forced[recordSpan{r.EventType, r.StartTime, r.EndTime}] = true
		st.forced.n++
		st.forced.ms += profile.ActiveDuration(c.rec, r.StartTime, r.EndTime) * 1000
		if r.EventType == "forced-layout" {
			st.forcedExplicit++
		}
	}
	for _, f := range profile.Frames(c.rec, c.rec.StartTime, c.rec.EndTime) {
		for i := range c.rec.Records {
			r := &c.rec.Records[i]
			if !profile.Observed(c.rec, *r) {
				continue
			}
			if r.Type != model.TypeLayout || (r.EventType != "layout" && !isStyleEvent(r.EventType)) || r.StartTime < f.Start || r.StartTime >= f.End {
				continue
			}
			inside, follows := false, false
			for _, s := range scripts {
				if s.s <= r.StartTime && r.StartTime < s.e {
					inside = true
				}
				if s.s >= f.Start && s.e <= r.StartTime {
					follows = true
				}
			}
			if follows && !inside {
				st.afterScript.n++
				postScript[recordSpan{r.EventType, r.StartTime, r.EndTime}] = true
				st.afterScript.ms += profile.ActiveDuration(c.rec, r.StartTime, r.EndTime) * 1000
			}
		}
	}
	for _, f := range c.sum.Functions {
		st.selfTotalMs += f.SelfMs
	}
	for _, cost := range c.costs {
		r := cost.Record
		switch r.Type {
		case model.TypeScript:
			get(st.script, r.EventType).exclusiveMs += cost.ExclusiveMs
			if r.EventType == "event-dispatched" {
				name := stringDetail(&r)
				if name == "" {
					name = "(unknown)"
				}
				get(st.eventNames, name).exclusiveMs += cost.ExclusiveMs
			}
		case model.TypeLayout:
			get(st.layout, r.EventType).exclusiveMs += cost.ExclusiveMs
			if forced[recordSpan{r.EventType, r.StartTime, r.EndTime}] {
				st.forced.exclusiveMs += cost.ExclusiveMs
			}
			if postScript[recordSpan{r.EventType, r.StartTime, r.EndTime}] {
				st.afterScript.exclusiveMs += cost.ExclusiveMs
			}
		}
	}
	st.gc.ms = c.rep.GCMs
	return st
}

func (c *ruleCtx) pct(ms float64) float64 {
	if c.wallMs <= 0 {
		return 0
	}
	return ms / c.wallMs * 100
}

func sevPct(p float64) Severity {
	switch {
	case p >= 15:
		return SevHigh
	case p >= 5:
		return SevMedium
	}
	return SevLow
}

func (c *ruleCtx) metric(key string) (float64, bool) {
	m, ok := c.sum.Lookup(key)
	return m.Value, ok
}

func (c *ruleCtx) finding(id, title, cat string, ms float64, ev []string, sug string) Finding {
	p := c.pct(ms)
	return Finding{ID: id, Title: title, Severity: sevPct(p), Category: cat, ImpactMs: ms, ImpactPct: p, Evidence: ev, Suggestion: sug}
}

func f1(v float64) string { return fmt.Sprintf("%.1f", v) }

func sortedByMs(m map[string]*evStat) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool {
		if m[keys[i]].exclusiveMs != m[keys[j]].exclusiveMs {
			return m[keys[i]].exclusiveMs > m[keys[j]].exclusiveMs
		}
		return keys[i] < keys[j]
	})
	return keys
}

func runRules(c *ruleCtx) []Finding {
	st := c.collect()
	var out []Finding
	add := func(f *Finding) {
		if f != nil {
			out = append(out, *f)
		}
	}
	add(ruleLongTasks(c))
	add(ruleForcedLayout(c, st))
	add(ruleLayoutAfterScript(c, st))
	add(ruleLayoutKind(c, st, "style-recalc-heavy", "Style recalculation is heavy", "recalculate-styles",
		"Reduce the number of elements invalidated per change: avoid toggling classes on high-level ancestors, simplify deep or broad selectors (descendant/universal), batch DOM mutations, and use contain: style/content on isolated widgets."))
	add(ruleLayoutKind(c, st, "layout-heavy", "Layout is heavy", "layout",
		"Limit the subtree affected by layout: use contain: layout/strict on independent regions, avoid animating geometry (width/height/top/left; use transform), keep the DOM small and virtualize long lists."))
	add(rulePaint(c, st))
	add(ruleJank(c))
	add(ruleInputStall(c))
	add(ruleRAF(c, st))
	add(ruleTimers(c, st))
	add(ruleEvents(c, st))
	add(ruleScriptKind(c, st, "microtask-heavy", "Microtask / promise callbacks are heavy", []string{profile.EventMicrotaskDispatched},
		"Promise/microtask chains run before rendering and cannot be interrupted. Break up large async loops, yield to the event loop (setTimeout/scheduler.yield) between chunks, and avoid cascading awaits over big collections."))
	add(ruleScriptKind(c, st, "observer-callback-heavy", "Observer callbacks are heavy", []string{profile.EventObserverCallback},
		"Observer callbacks (Resize/Intersection/Mutation) run on the main thread each frame. Make them cheap: batch the entries, avoid reading/writing layout inside them, debounce, and disconnect observers that are no longer needed."))
	add(ruleGC(c, st))
	add(ruleMemory(c))
	add(ruleCPU(c))
	add(ruleHotFunction(c, st))
	add(ruleHotModule(c))
	add(ruleNetwork(c))
	add(ruleCoverage(c, st))

	sortFindings(out)
	if out == nil {
		out = []Finding{}
	}
	enrichFindings(c, out)
	return out
}

func ruleLongTasks(c *ruleCtx) *Finding {
	if len(c.long) == 0 {
		return nil
	}
	total, worst := 0.0, c.long[0]
	for _, l := range c.long {
		total += l.Ms
	}
	ev := []string{
		fmt.Sprintf("%d tasks > %sms, %sms total (%s%% of wall time)", len(c.long), f1(c.opt.LongTaskMs), f1(total), f1(c.pct(total))),
		fmt.Sprintf("worst: %sms %s at t=%ss", f1(worst.Ms), strings.TrimSpace(worst.EventType+" "+worst.Detail), f1(worst.T)),
	}
	if worst.TopFunc != "" {
		ev = append(ev, "hottest function in worst task: "+worst.TopFunc)
	}
	f := c.finding("long-tasks", "Long tasks block the main thread", "script", total, ev,
		"Split long-running work into chunks <50ms and yield between them (scheduler.yield(), setTimeout, requestIdleCallback), move pure computation to a Web Worker, and defer non-critical work until after first interaction.")
	return &f
}

func ruleForcedLayout(c *ruleCtx, st *recStats) *Finding {
	if st.forced.n == 0 {
		return nil
	}
	ev := []string{
		fmt.Sprintf("%d explicit forced-layout records; %d layout records start inside script spans", st.forcedExplicit, st.forced.n-st.forcedExplicit),
		fmt.Sprintf("%sms exclusive layout cost; %sms inclusive record time; overlap suggests synchronous work, not proof of repeated DOM read/write thrashing", f1(st.forced.exclusiveMs), f1(st.forced.ms)),
	}
	f := c.finding("forced-layout", "Synchronous layout evidence", "layout", st.forced.exclusiveMs, ev,
		"Inspect the implicated script spans for layout reads after DOM writes. Batch layout reads before writes, reduce style invalidation, and verify with an explicit forced-layout trace before claiming layout thrashing.")
	return &f
}

func ruleLayoutAfterScript(c *ruleCtx, st *recStats) *Finding {
	if st.afterScript.n == 0 || c.pct(st.afterScript.exclusiveMs) < 5 {
		return nil
	}
	ev := []string{fmt.Sprintf("%d layout/style records begin after script ends within a frame; %sms exclusive rendering cost; %sms inclusive record time", st.afterScript.n, f1(st.afterScript.exclusiveMs), f1(st.afterScript.ms)),
		"Rendering after script is a normal browser phase, not evidence of forced synchronous layout."}
	f := c.finding("layout-after-script", "Rendering after script is costly", "layout", st.afterScript.exclusiveMs, ev,
		"Inspect style invalidation from the preceding trigger: reduce the changed subtree, batch DOM mutations, and use containment where appropriate. Do not treat normal post-script rendering as forced layout.")
	return &f
}

func ruleLayoutKind(c *ruleCtx, st *recStats, id, title, kind, sug string) *Finding {
	s := st.layout[kind]
	if s == nil || c.pct(s.exclusiveMs)+1e-9 < 5 {
		return nil
	}
	ev := []string{
		fmt.Sprintf("%d %s records, %sms exclusive cost (%s%% of wall time)", s.n, kind, f1(s.exclusiveMs), f1(c.pct(s.exclusiveMs))),
		fmt.Sprintf("%sms inclusive record time (nested work overlaps other categories)", f1(s.ms)),
	}
	f := c.finding(id, title, "layout", s.exclusiveMs, ev, sug)
	return &f
}

func rulePaint(c *ruleCtx, st *recStats) *Finding {
	p := st.layout["paint"]
	if p == nil {
		return nil
	}
	if c.pct(p.exclusiveMs)+1e-9 < 5 && st.paintQuads < 3 {
		return nil
	}
	ev := []string{
		fmt.Sprintf("%d paints, %sms exclusive cost (%s%% of wall time)", p.n, f1(p.exclusiveMs), f1(c.pct(p.exclusiveMs))),
		fmt.Sprintf("%sms inclusive record time (nested work overlaps other categories)", f1(p.ms)),
	}
	if st.maxQuad > 0 {
		ev = append(ev, fmt.Sprintf("largest painted area %.0f px² (%d paints over %d px²)", st.maxQuad, st.paintQuads, largeQuadPx))
	}
	f := c.finding("paint-heavy", "Painting is heavy", "paint", p.exclusiveMs, ev,
		"Reduce repaint area: avoid full-surface invalidation, replace expensive effects (box-shadow, filter, blur, gradients) with cheaper ones, and use contain: paint to scope repaints. Promote only truly animating elements to their own layer with will-change: transform, sparingly, since every layer costs memory.")
	return &f
}

func ruleJank(c *ruleCtx) *Finding {
	var frames []float64
	janky, workExcess, intervalExcess := 0, 0.0, 0.0
	for _, frame := range profile.Frames(c.rec, c.rec.StartTime, c.rec.EndTime) {
		if frame.Class != profile.FrameActive {
			continue
		}
		frames = append(frames, frame.DurationMs)
		if frame.DurationMs > c.opt.JankMs {
			janky++
			workExcess += math.Max(0, frame.Occupancy.WorkMs-c.opt.JankMs)
			intervalExcess += frame.DurationMs - c.opt.JankMs
		}
	}
	if len(frames) == 0 {
		return nil
	}
	p95 := analyze.Percentile(frames, 95)
	ratio := float64(janky) / float64(len(frames))
	if p95 <= c.opt.JankMs || ratio < 0.05 {
		return nil
	}
	var sc, ly, pt float64
	for _, f := range c.slow {
		sc += f.ScriptMs
		ly += f.LayoutMs
		pt += f.PaintMs
	}
	cause, cm, sug := "script", sc, "Measured work in slow active frames is dominated by JavaScript: shorten the listed handlers and callbacks, defer non-visual work, and move computation off the main thread."
	if ly > cm {
		cause, cm, sug = "layout/style", ly, "Measured work in slow active frames is dominated by layout/style: reduce style invalidation, shrink the affected DOM subtree, and animate transform/opacity instead of geometry. Only synchronous layout evidence supports a forced-layout claim."
	}
	if pt > cm {
		cause, cm, sug = "paint/composite", pt, "Measured work in slow active frames is dominated by paint/composite: reduce repaint area, simplify effects, and use layers sparingly via will-change/contain."
	}
	if math.Max(cm, pt) == 0 {
		cause, sug = "unestablished", "No instrumented main-thread cost explains these intervals. Inspect CPU and rendering instrumentation before attributing them to JavaScript."
	}
	fixedRatio, _ := c.metric("frames.jank_ratio")
	ev := []string{
		fmt.Sprintf("active rendering interval p95 %sms vs configured %sms threshold", f1(p95), f1(c.opt.JankMs)),
		fmt.Sprintf("%d of %d active frames over threshold (%s%%); idle gaps and input stalls excluded", janky, len(frames), f1(ratio*100)),
		fmt.Sprintf("dominant measured cost across slow frames: %s (script %sms, layout/style %sms, paint/composite %sms)", cause, f1(sc), f1(ly), f1(pt)),
		fmt.Sprintf("%sms exclusive work excess; %sms observed interval excess (not main-thread work)", f1(workExcess), f1(intervalExcess)),
		fmt.Sprintf("scorecard and frames.jank_ratio use fixed 16.7ms budget: %s%% over budget", f1(fixedRatio*100)),
	}
	f := c.finding("jank", "Work-bound janky frames", "frames", workExcess, ev, sug)
	for _, metric := range []struct {
		key         string
		value       float64
		unit        metrics.Unit
		description string
	}{
		{"findings.jank.ratio", ratio, metrics.UnitRatio, fmt.Sprintf("Active-frame interval ratio over configured %.6gms threshold", c.opt.JankMs)},
		{"findings.jank.threshold_ms", c.opt.JankMs, metrics.UnitMs, "Configured finding threshold; fixed scorecard standards remain 16.7ms"},
		{"findings.jank.interval_excess_ms", intervalExcess, metrics.UnitMs, "Observed active-frame interval excess; not measured main-thread work"},
	} {
		f.NumericEvidence = append(f.NumericEvidence, Evidence{MetricKey: metric.key, Value: metric.value, Unit: metric.unit, Description: metric.description, Basis: "measured", Command: metricCommand(c, metric.key)})
	}
	return &f
}

func ruleInputStall(c *ruleCtx) *Finding {
	stalls := c.rep.InputStalls
	if len(stalls) == 0 {
		return nil
	}
	total, work := 0.0, 0.0
	for _, stall := range stalls {
		total += stall.Ms
		work += stall.WorkMs
	}
	worst := stalls[0]
	ev := []string{
		fmt.Sprintf("%d input-driven rendering stalls, %sms observed intervals; %sms measured main-thread work", len(stalls), f1(total), f1(work)),
		fmt.Sprintf("input received, no rendering update for %sms; main thread did %.6gms of instrumented work in the longest interval", f1(worst.Ms), worst.WorkMs),
		fmt.Sprintf("each interval exceeds the fixed %sms frame budget with at most one budget of work; excluded from work-bound jank and frame statistics", f1(profile.FrameBudgetMs)),
		"Observed stall duration is not measured main-thread blocking time; collector intervals do not count as main-thread work.",
	}
	f := c.finding("input-stall", "Rendering stalls during input with little measured work", "frames", total, ev,
		"Inspect awaited async work, IPC, and suppressed rendering. Check main-thread CPU before attributing the stall to uninstrumented work; this evidence does not show expensive JavaScript.")
	return &f
}

func ruleRAF(c *ruleCtx, st *recStats) *Finding {
	s := st.script["animation-frame-fired"]
	if s == nil || s.n == 0 {
		return nil
	}
	mean := s.exclusiveMs / float64(s.n)
	if c.pct(s.exclusiveMs) < 10 && mean <= 8 {
		return nil
	}
	ev := []string{fmt.Sprintf("%d animation-frame callbacks, %sms exclusive cost, mean %sms (%s%% of wall time); %sms inclusive record time", s.n, f1(s.exclusiveMs), f1(mean), f1(c.pct(s.exclusiveMs)), f1(s.ms))}
	f := c.finding("raf-load", "requestAnimationFrame callbacks are expensive", "script", s.exclusiveMs, ev,
		"Keep rAF callbacks under ~8ms: do only visual updates, precompute outside the callback, avoid allocating per frame, cancel loops when content is offscreen or idle, and merge multiple rAF loops into one.")
	return &f
}

func ruleTimers(c *ruleCtx, st *recStats) *Finding {
	dur := c.sum.DurationSec
	if dur <= 0 {
		return nil
	}
	fired, inst := 0, 0
	ms := 0.0
	if s := st.script["timer-fired"]; s != nil {
		fired, ms = s.n, s.exclusiveMs
	}
	if s := st.script["timer-installed"]; s != nil {
		inst = s.n
	}
	fr, ir := float64(fired)/dur, float64(inst)/dur
	if fr < 50 && ir < 50 {
		return nil
	}
	ev := []string{fmt.Sprintf("%d timers fired (%s/s), %sms total", fired, f1(fr), f1(ms)), fmt.Sprintf("%d timers installed (%s/s)", inst, f1(ir))}
	f := c.finding("timer-storm", "Timer storm", "script", ms, ev,
		"Replace polling and short setTimeout/setInterval chains with event-driven updates, coalesce timers into a single scheduler tick, raise intervals, and clear timers on teardown. Repeated setTimeout(0) installs usually signal a loop that should be batched.")
	return &f
}

func ruleEvents(c *ruleCtx, st *recStats) *Finding {
	s := st.script["event-dispatched"]
	if s == nil || c.pct(s.exclusiveMs) < 5 {
		return nil
	}
	var ev []string
	ev = append(ev, fmt.Sprintf("%d events, %sms exclusive cost (%s%% of wall time); %sms inclusive record time", s.n, f1(s.exclusiveMs), f1(c.pct(s.exclusiveMs)), f1(s.ms)))
	for i, k := range sortedByMs(st.eventNames) {
		if i >= 3 {
			break
		}
		e := st.eventNames[k]
		ev = append(ev, fmt.Sprintf("%s: %d dispatches, %sms exclusive cost; %sms inclusive record time", k, e.n, f1(e.exclusiveMs), f1(e.ms)))
	}
	f := c.finding("event-handler-heavy", "Event handlers are heavy", "script", s.exclusiveMs, ev,
		"Throttle or debounce high-frequency handlers (mousemove, pointermove, scroll, resize, input) to once per animation frame, mark scroll/touch listeners passive, use event delegation instead of per-element listeners, and keep handlers short by deferring heavy work.")
	return &f
}

func ruleScriptKind(c *ruleCtx, st *recStats, id, title string, kinds []string, sug string) *Finding {
	ms, inclusiveMs, n := 0.0, 0.0, 0
	for _, k := range kinds {
		if s := st.script[k]; s != nil {
			ms += s.exclusiveMs
			inclusiveMs += s.ms
			n += s.n
		}
	}
	if n == 0 || c.pct(ms) < 3 {
		return nil
	}
	ev := []string{fmt.Sprintf("%d %s callbacks, %sms exclusive cost (%s%% of wall time); %sms inclusive record time", n, strings.Join(kinds, "/"), f1(ms), f1(c.pct(ms)), f1(inclusiveMs))}
	if id == "microtask-heavy" {
		ev = append(ev, "Microtask checkpoints can nest inside dispatches and timers; inclusive record time overlaps event/timer time.")
	}
	f := c.finding(id, title, "script", ms, ev, sug)
	return &f
}

func ruleGC(c *ruleCtx, st *recStats) *Finding {
	g := st.gc
	if g.n == 0 {
		return nil
	}
	dur := c.sum.DurationSec
	rate := 0.0
	if dur > 0 {
		rate = float64(g.n) / dur
	}
	if c.pct(g.ms) < 3 && !(rate >= 2 && g.n >= 3) {
		return nil
	}
	ev := []string{
		fmt.Sprintf("%d GC collections, %sms collector-interval union (%s%% of wall time), %s/s", g.n, f1(g.ms), f1(c.pct(g.ms)), f1(rate)),
		fmt.Sprintf("%sms overlaps instrumented main-thread work; collector intervals are not necessarily main-thread time", f1(c.rep.GCOverlapWithWorkMs)),
	}
	if n := len(c.rec.MemoryPressureEvents); n > 0 {
		ev = append(ev, fmt.Sprintf("%d memory pressure events", n))
	}
	ms := g.ms
	f := c.finding("gc-pressure", "Garbage collection pressure", "gc", ms, ev,
		"Reduce allocation churn: avoid creating objects, arrays, closures and strings per frame or per event; reuse buffers and pool short-lived objects; prefer typed arrays; hoist constants out of hot loops; avoid chained map/filter/spread on large arrays in hot paths.")
	return &f
}

// linreg returns slope and r² of y over x.
func linreg(pts []metrics.Point) (slope, r2 float64) {
	n := float64(len(pts))
	if n < 2 {
		return 0, 0
	}
	var sx, sy, sxx, sxy, syy float64
	for _, p := range pts {
		sx += p.T
		sy += p.V
		sxx += p.T * p.T
		sxy += p.T * p.V
		syy += p.V * p.V
	}
	dx := n*sxx - sx*sx
	dy := n*syy - sy*sy
	if dx == 0 || dy == 0 {
		return 0, 0
	}
	cov := n*sxy - sx*sy
	return cov / dx, cov * cov / (dx * dy)
}

func ruleMemory(c *ruleCtx) *Finding {
	pts := c.sum.MemoryMB
	if len(pts) < 5 {
		return nil
	}
	slope, r2 := linreg(pts) // MB per second
	perMin := slope * 60
	if perMin < 5 || r2 < 0.8 {
		return nil
	}
	mean := 0.0
	for _, p := range pts {
		mean += p.V
	}
	mean /= float64(len(pts))
	growth := slope * (pts[len(pts)-1].T - pts[0].T)
	rel := 0.0
	if mean > 0 {
		rel = growth / mean
	}
	sev := SevLow
	if rel >= 0.5 {
		sev = SevHigh
	} else if rel >= 0.2 {
		sev = SevMedium
	}
	ev := []string{
		fmt.Sprintf("memory grows %s MB/min (linear fit r²=%.2f)", f1(perMin), r2),
		fmt.Sprintf("%s MB → %s MB over %ss", f1(pts[0].V), f1(pts[len(pts)-1].V), f1(pts[len(pts)-1].T-pts[0].T)),
	}
	return &Finding{ID: "memory-growth", Title: "Memory grows steadily (possible leak)", Severity: sev, Category: "memory",
		Evidence:   ev,
		Suggestion: "Take heap snapshots at two points and diff them. Typical culprits: event listeners or timers never removed, detached DOM nodes held by closures, unbounded caches/arrays/maps, and subscriptions not unsubscribed on unmount. Use WeakMap/WeakRef for caches keyed by objects and cap collection sizes."}
}

func ruleCPU(c *ruleCtx) *Finding {
	mean, ok := c.metric("cpu.mean_pct")
	if !ok {
		return nil
	}
	main, haveMain := c.metric("cpu.main.mean_pct")
	if mean < 70 && !(haveMain && main >= 80) {
		return nil
	}
	ev := []string{fmt.Sprintf("mean CPU %s%%", f1(mean))}
	val := mean
	if haveMain {
		ev = append(ev, fmt.Sprintf("main thread mean %s%%", f1(main)))
		val = math.Max(mean, main)
	}
	if p, ok := c.metric("cpu.p95_pct"); ok {
		ev = append(ev, fmt.Sprintf("CPU p95 %s%%", f1(p)))
	}
	sev := SevMedium
	if val >= 90 {
		sev = SevHigh
	}
	return &Finding{ID: "cpu-high", Title: "High CPU usage", Severity: sev, Category: "cpu",
		ImpactMs: val / 100 * c.wallMs, ImpactPct: val, Evidence: ev,
		Suggestion: "The main thread is near saturation. Use the hotspots and modules above to find the dominant work, remove redundant work (memoize, cache, skip unchanged renders), move computation to workers and avoid continuous animation or polling while idle."}
}

func ruleHotFunction(c *ruleCtx, st *recStats) *Finding {
	if len(c.rep.Hotspots) == 0 || st.selfTotalMs <= 0 {
		return nil
	}
	h := c.rep.Hotspots[0]
	share := h.SelfMs / st.selfTotalMs * 100
	if share <= 10 || h.SelfMs < 20 || c.pct(h.SelfMs) < 0.1 {
		return nil
	}
	ev := []string{
		fmt.Sprintf("%s is %s%% of sampled self time (%sms; %s%% of wall time)", functionLabel(h), f1(share), f1(h.SelfMs), f1(c.pct(h.SelfMs))),
		fmt.Sprintf("inclusive time %sms", f1(h.TotalMs)),
	}
	sev := sampledSeverity(c, h.SelfMs, share)
	return &Finding{ID: "hot-function", Title: "Sampled function concentration: " + h.Name, Severity: sev, Category: "script",
		ImpactMs: h.SelfMs, ImpactPct: c.pct(h.SelfMs), Evidence: ev,
		Functions:  []string{functionLabel(h)},
		Suggestion: "Confirm the sampled function's wall-time impact in a repeated capture. Then reduce repeated calls, hoist invariant work, or choose a cheaper algorithm; sample share alone does not establish a bottleneck."}
}

func ruleHotModule(c *ruleCtx) *Finding {
	for _, m := range c.mods {
		if m.Name == "(native)" {
			continue
		}
		if m.Pct <= 25 || m.SelfMs < 20 || c.pct(m.SelfMs) < 0.1 {
			return nil
		}
		ev := []string{fmt.Sprintf("file/module %s is %s%% of sampled self time (%sms; %s%% of wall time)", m.Name, f1(m.Pct), f1(m.SelfMs), f1(c.pct(m.SelfMs)))}
		return &Finding{ID: "hot-module", Title: "Sampled module concentration: " + m.Name, Severity: sampledSeverity(c, m.SelfMs, m.Pct), Category: "script",
			ImpactMs: m.SelfMs, ImpactPct: c.pct(m.SelfMs), Evidence: ev,
			Suggestion: "Confirm this file's absolute wall-time cost before changing it. Use the measured functions and trigger intervals to avoid redundant work rather than treating a large sample share as proof of high impact."}
	}
	return nil
}

func ruleNetwork(c *ruleCtx) *Finding {
	n, ok := c.metric("network.requests")
	if !ok {
		return nil
	}
	p95, _ := c.metric("network.p95_ms")
	bytes, _ := c.metric("network.transfer_bytes")
	mean, _ := c.metric("network.mean_ms")
	if n < 50 && p95 < 1000 && bytes < 5<<20 {
		return nil
	}
	ev := []string{
		fmt.Sprintf("%.0f requests, %s MB transferred", n, f1(bytes/(1<<20))),
		fmt.Sprintf("request time mean %sms, p95 %sms", f1(mean), f1(p95)),
	}
	sev := SevLow
	if p95 >= 3000 || bytes >= 20<<20 {
		sev = SevMedium
	}
	return &Finding{ID: "network", Title: "Network activity is heavy or slow", Severity: sev, Category: "network",
		ImpactMs: p95, Evidence: ev,
		Suggestion: "Cut request count (bundle, batch API calls, HTTP/2 multiplexing), enable compression and caching headers, lazy-load below-the-fold assets, shrink payloads (pagination, field selection, modern image formats) and cancel obsolete in-flight requests."}
}

func ruleCoverage(c *ruleCtx, st *recStats) *Finding {
	coverage := c.rep.SampleCoverage
	if coverage.ScriptMs < 100 || coverage.Ratio >= 0.5 {
		return nil
	}
	ev := []string{fmt.Sprintf("%sms sampled vs %sms measured script; coverage ratio %.3f, %d samples", f1(coverage.SampledMs), f1(coverage.ScriptMs), coverage.Ratio, coverage.SampleCount)}
	suggestion := "Sampled function and module rankings may be unrepresentative. Repeat the same interaction with JavaScript sampling enabled and a longer capture; compare sample weights with measured script time."
	if coverage.UntimedExcluded && coverage.UntimedMs > 0 {
		ev = append(ev, fmt.Sprintf("%d untimed samples (%.6g ms) excluded from this window", coverage.UntimedSampleCount, coverage.UntimedMs))
		suggestion = "Analyze the whole recording to include untimed samples; per-window coverage counts timed samples only."
	}
	return &Finding{ID: "low-sample-coverage", Title: "Sampling profiler covers little of the script time", Severity: SevInfo, Category: "script",
		Evidence:   ev,
		Suggestion: suggestion}
}
