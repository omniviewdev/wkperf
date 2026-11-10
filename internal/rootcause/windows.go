package rootcause

import (
	"crypto/sha256"
	"encoding/hex"
	"math"
	"sort"
	"strings"

	"github.com/omniviewdev/wkperf/internal/model"
	"github.com/omniviewdev/wkperf/internal/profile"
)

func digest(parts ...string) string {
	h := sha256.Sum256([]byte(strings.Join(parts, "\x00")))
	return hex.EncodeToString(h[:12])
}

func (b builder) window(kind, label string, from, to, durationMs, workMs float64) Window {
	from = seconds(math.Max(from, b.from))
	to = seconds(math.Min(to, b.to))
	w := Window{Kind: kind, Label: label, FromSec: b.exportTime(from), ToSec: b.exportTime(to), DurationMs: number(durationMs), ActiveMs: number(workMs), Command: b.query("hot-path --min-share 0", from, to)}
	key := windowKey(w)
	w.Stats = []Stat{
		b.stat(key+".from_sec", "Window start", w.FromSec, "s", timelineBasis),
		b.stat(key+".to_sec", "Window end", w.ToSec, "s", timelineBasis),
		b.stat(key+".duration_ms", "Active timeline interval duration", w.DurationMs, "ms", timelineBasis),
		b.stat(key+".work_ms", "Measured exclusive work", w.ActiveMs, "ms", timelineBasis),
	}
	return w
}

func windowKey(w Window) string {
	return "triage." + digest(w.Kind, w.Label, decimal(w.FromSec), decimal(w.ToSec))
}

type windowStackWeight struct {
	ms          float64
	samples     int
	first, last float64
}

func (b builder) heaviestWindowStack(from, to float64, prefix string) *Stack {
	samples := b.tree.Samples
	lo := sort.Search(len(samples), func(i int) bool { return samples[i].Timestamp >= from })
	hi := sort.Search(len(samples), func(i int) bool { return samples[i].Timestamp >= to })
	if lo == hi {
		return nil
	}
	pathIDs := b.tree.SamplePathIDs()
	weights := make(map[string]windowStackWeight, min(hi-lo, len(b.paths)))
	for i := lo; i < hi; i++ {
		s := samples[i]
		id := pathIDs[i]
		v := weights[id]
		if v.samples == 0 {
			v.first = s.Timestamp
		}
		v.ms += s.WeightMs
		v.samples++
		v.last = s.Timestamp
		weights[id] = v
	}
	best := ""
	for id, v := range weights {
		ms, bestMs := number(v.ms), number(weights[best].ms)
		if best == "" || ms > bestMs || ms == bestMs && framesLess(b.pathByID[id].Frames, b.pathByID[best].Frames) {
			best = id
		}
	}
	v := weights[best]
	first, last := b.exportTime(v.first), b.exportTime(v.last)
	stack := b.finishStack(Stack{Frames: b.pathByID[best].Frames, SelfMs: v.ms, Samples: v.samples, FirstSec: &first, LastSec: &last}, prefix, b.pathKeys[best])
	stack.Command = b.query("stacks "+quote(best), from, to)
	return &stack
}

func (b builder) completeWindows(windows []Window) {
	totalMs := b.tree.Root.TotalMs
	if !b.tree.UntimedIncluded {
		totalMs += b.tree.UntimedTotal.Ms
	}
	note := UntimedAttributionNote(b.tree.UntimedTotal.Ms, totalMs)
	for i := range windows {
		w := &windows[i]
		from, to := seconds(w.FromSec-b.o.OffsetSec), seconds(w.ToSec-b.o.OffsetSec)
		key := windowKey(*w)
		w.TopStack = b.heaviestWindowStack(from, to, key)
		if w.TopStack == nil {
			w.SampleStatus = UntimedSampleStatus(b.tree.UntimedSpans, from, to, b.o.OffsetSec)
			if w.SampleStatus == "" {
				w.SampleStatus = "No overlapping sampler samples; timeline evidence only."
			}
		} else {
			w.SampleStatus = "Duration-weighted sampler estimates at timestamps inside this interval; overlap is not causal attribution."
		}
		if note != "" {
			w.SampleStatus += " " + note
		}
		if i < b.o.Top {
			tree := b.tree.Window(from, to).TimedOnly()
			path := b.hotPath(tree, key+".hot_path", from, to)
			w.HotPath = &path
		}
	}
}

func windowLess(a, b Window) bool {
	if a.DurationMs != b.DurationMs {
		return a.DurationMs > b.DurationMs
	}
	if a.FromSec != b.FromSec {
		return a.FromSec < b.FromSec
	}
	if a.ToSec != b.ToSec {
		return a.ToSec < b.ToSec
	}
	if a.Kind != b.Kind {
		return a.Kind < b.Kind
	}
	return a.Label < b.Label
}

func (b builder) windows() (longTasks, slowFrames, bursts, worst []Window) {
	seen := map[string]bool{}
	for _, r := range b.rec.Records {
		if r.Type != model.TypeScript || profile.IsGC(r) || profile.CountOnly(r.EventType) {
			continue
		}
		from, to := math.Max(b.from, r.StartTime-b.rec.StartTime), math.Min(b.to, r.EndTime-b.rec.StartTime)
		if to <= from {
			continue
		}
		ms := profile.ActiveDuration(b.rec, b.rec.StartTime+from, b.rec.StartTime+to) * 1000
		if number(ms) <= b.o.LongTaskMs {
			continue
		}
		label := eventName(r)
		key := label + "\x00" + decimal(from) + "\x00" + decimal(to)
		if seen[key] {
			continue
		}
		seen[key] = true
		longTasks = append(longTasks, b.window("long-task", label, from, to, ms, ms))
	}
	for _, f := range profile.Frames(b.rec, b.rec.StartTime+b.from, b.rec.StartTime+b.to) {
		if f.IdleGap || number(f.DurationMs) <= b.o.JankMs {
			continue
		}
		label := f.DominantTrigger
		if label == "" {
			label = "active rendering frame"
		}
		from, to := f.Start-b.rec.StartTime, f.End-b.rec.StartTime
		key := "frame\x00" + decimal(from) + "\x00" + decimal(to)
		if seen[key] {
			continue
		}
		seen[key] = true
		slowFrames = append(slowFrames, b.window("slow-frame", label, from, to, f.DurationMs, f.Occupancy.WorkMs))
	}
	sort.Slice(longTasks, func(i, j int) bool { return windowLess(longTasks[i], longTasks[j]) })
	sort.Slice(slowFrames, func(i, j int) bool { return windowLess(slowFrames[i], slowFrames[j]) })
	bursts = b.eventBursts()
	b.completeWindows(longTasks)
	b.completeWindows(slowFrames)
	b.completeWindows(bursts)
	worst = append(worst, longTasks[:min(len(longTasks), b.o.Top)]...)
	worst = append(worst, slowFrames[:min(len(slowFrames), b.o.Top)]...)
	worst = append(worst, bursts[:min(len(bursts), b.o.Top)]...)
	sort.Slice(worst, func(i, j int) bool { return windowLess(worst[i], worst[j]) })
	return
}

func (b builder) eventBursts() []Window {
	events := b.rootEvents()
	var out []Window
	for i := 0; i < len(events); {
		j := i + 1
		end := events[i].to
		for j < len(events) && events[j].from-events[j-1].from <= .050000001 && events[j].from-events[i].from <= .250000001 {
			end = math.Max(end, events[j].to)
			j++
		}
		if j-i >= 3 {
			from := events[i].from
			duration := profile.ActiveDuration(b.rec, b.rec.StartTime+from, b.rec.StartTime+end) * 1000
			work := profile.Measure(b.rec, b.rec.StartTime+from, b.rec.StartTime+end).WorkMs
			w := b.window("event-burst", "clustered script-event burst", from, end, duration, work)
			key := windowKey(w)
			w.Stats = append(w.Stats, b.stat(key+".events", "Distinct script events in burst", float64(j-i), "count", timelineBasis))
			out = append(out, w)
		}
		i = j
	}
	sort.Slice(out, func(i, j int) bool { return windowLess(out[i], out[j]) })
	return out
}

func (b builder) gcOverlaps() []GCOverlap {
	seen := map[string]bool{}
	var out []GCOverlap
	for _, gc := range b.rec.Records {
		if !profile.IsGC(gc) {
			continue
		}
		parts := b.active(gc.StartTime-b.rec.StartTime, gc.EndTime-b.rec.StartTime)
		if len(parts) == 0 {
			continue
		}
		from, to := parts[0].Start, parts[len(parts)-1].End
		key := "gc." + digest(decimal(b.exportTime(from)), decimal(b.exportTime(to)))
		if seen[key] {
			continue
		}
		seen[key] = true
		g := GCOverlap{FromSec: b.exportTime(from), ToSec: b.exportTime(to), Attribution: "Instrumented GC pause and temporal event overlap only; sampled allocation or application causes are not established.", Command: b.query("events "+quote("garbage-collected"), from, to)}
		for _, p := range parts {
			g.DurationMs += (p.End - p.Start) * 1000
		}
		g.DurationMs = number(g.DurationMs)
		eventsSeen := map[string]bool{}
		for _, r := range b.rec.Records {
			if r.Type != model.TypeScript || profile.IsGC(r) || profile.CountOnly(r.EventType) {
				continue
			}
			var ms float64
			for _, p := range parts {
				lo, hi := math.Max(p.Start, r.StartTime-b.rec.StartTime), math.Min(p.End, r.EndTime-b.rec.StartTime)
				if hi > lo {
					ms += (hi - lo) * 1000
				}
			}
			if number(ms) <= 0 {
				continue
			}
			lo, hi := math.Max(from, r.StartTime-b.rec.StartTime), math.Min(to, r.EndTime-b.rec.StartTime)
			name := eventName(r)
			eventID := digest(name, decimal(b.exportTime(lo)), decimal(b.exportTime(hi)))
			if eventsSeen[eventID] {
				continue
			}
			eventsSeen[eventID] = true
			e := EventOverlap{Event: name, FromSec: b.exportTime(lo), ToSec: b.exportTime(hi), OverlapMs: number(ms), Command: b.query("events", lo, hi)}
			e.Stats = []Stat{
				b.stat(key+".event."+eventID+".from_sec", "GC/event overlap start", e.FromSec, "s", timelineBasis),
				b.stat(key+".event."+eventID+".to_sec", "GC/event overlap end", e.ToSec, "s", timelineBasis),
				b.stat(key+".event."+eventID+".overlap_ms", "Instrumented GC/event overlap", e.OverlapMs, "ms", timelineBasis),
			}
			g.Events = append(g.Events, e)
		}
		sort.Slice(g.Events, func(i, j int) bool {
			if g.Events[i].OverlapMs != g.Events[j].OverlapMs {
				return g.Events[i].OverlapMs > g.Events[j].OverlapMs
			}
			if g.Events[i].FromSec != g.Events[j].FromSec {
				return g.Events[i].FromSec < g.Events[j].FromSec
			}
			if g.Events[i].ToSec != g.Events[j].ToSec {
				return g.Events[i].ToSec < g.Events[j].ToSec
			}
			return g.Events[i].Event < g.Events[j].Event
		})
		g.Stats = []Stat{
			b.stat(key+".from_sec", "GC pause start", g.FromSec, "s", timelineBasis),
			b.stat(key+".to_sec", "GC pause end", g.ToSec, "s", timelineBasis),
			b.stat(key+".duration_ms", "Instrumented active GC pause", g.DurationMs, "ms", timelineBasis),
			b.stat(key+".events", "Distinct overlapping script events", float64(len(g.Events)), "count", timelineBasis),
		}
		out = append(out, g)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].FromSec != out[j].FromSec {
			return out[i].FromSec < out[j].FromSec
		}
		if out[i].ToSec != out[j].ToSec {
			return out[i].ToSec < out[j].ToSec
		}
		return out[i].DurationMs > out[j].DurationMs
	})
	return out
}
