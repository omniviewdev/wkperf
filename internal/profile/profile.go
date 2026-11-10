// Package profile shares capture-time, exclusive-work and frame semantics.
// Exported timeline records have no hierarchy, so occupancy uses interval unions,
// not sums of potentially nested durations.
package profile

import (
	"encoding/json"
	"math"
	"sort"
	"strings"

	"github.com/omniviewdev/wkperf/internal/model"
)

// Interval is an absolute, half-open span, in seconds.
type Interval struct {
	Start float64 `json:"start"`
	End   float64 `json:"end"`
}

// CoversRecording accepts nanosecond-rounded bounds for a whole recording.
func CoversRecording(rec *model.Recording, fromRel, toRel float64) bool {
	return rec != nil && fromRel <= 1e-9 && toRel >= rec.EndTime-rec.StartTime-1e-9
}

// UntimedTimestamp distinguishes missing sampler clocks from a real zero origin.
func UntimedTimestamp(rec *model.Recording, timestamp float64) bool {
	start := rec.StartTime
	if rec.SampleEvidence != nil {
		start = rec.SampleEvidence.StartTime
	}
	return !finite(timestamp) || timestamp == 0 && start > 0
}

// ActiveIntervals subtracts the union of stopped/resumed capture intervals.
// WebKit TimelineRecording exports discontinuities as {startTime,endTime}.
func ActiveIntervals(rec *model.Recording, start, end float64) []Interval {
	out := make([]Interval, 0)
	if rec == nil || !finite(start) || !finite(end) || end <= start {
		return out
	}
	if rec.EndTime > rec.StartTime {
		start = math.Max(start, rec.StartTime)
		end = math.Min(end, rec.EndTime)
	}
	if end <= start {
		return out
	}
	pauses := make([]Interval, 0, len(rec.Discontinuities))
	for _, d := range rec.Discontinuities {
		lo, hi := math.Max(start, d.StartTime), math.Min(end, d.EndTime)
		if finite(lo) && finite(hi) && hi > lo {
			pauses = append(pauses, Interval{lo, hi})
		}
	}
	sort.Slice(pauses, func(i, j int) bool {
		if pauses[i].Start != pauses[j].Start {
			return pauses[i].Start < pauses[j].Start
		}
		return pauses[i].End < pauses[j].End
	})
	cursor := start
	for _, p := range pauses {
		if p.Start > cursor {
			out = append(out, Interval{cursor, p.Start})
		}
		cursor = math.Max(cursor, p.End)
	}
	if cursor < end {
		out = append(out, Interval{cursor, end})
	}
	return out
}

func ActiveDuration(rec *model.Recording, start, end float64) float64 {
	var duration float64
	for _, span := range ActiveIntervals(rec, start, end) {
		duration += span.End - span.Start
	}
	return duration
}

// Contains reports whether timestamp lies in sorted, disjoint active intervals.
func Contains(intervals []Interval, timestamp float64) bool {
	i := sort.Search(len(intervals), func(i int) bool { return intervals[i].End > timestamp })
	return i < len(intervals) && intervals[i].Start <= timestamp
}

// Occupancy partitions measured interval coverage into exclusive categories.
// More specialized records replace enclosing script coverage. Where exported
// overlaps lose nesting, precedence is GC, composite, paint, layout, style,
// script. The partition never exceeds observed active wall time.
type Occupancy struct {
	ScriptMs    float64 `json:"scriptMs"`
	StyleMs     float64 `json:"styleMs"`
	LayoutMs    float64 `json:"layoutMs"`
	PaintMs     float64 `json:"paintMs"`
	CompositeMs float64 `json:"compositeMs"`
	GCMs        float64 `json:"gcMs"`
	WorkMs      float64 `json:"workMs"`
	DurationMs  float64 `json:"durationMs"`
}

const (
	kindScript = iota
	kindStyle
	kindLayout
	kindPaint
	kindComposite
	kindGC
	kindCount
)

type endpoint struct {
	time        float64
	kind, delta int
}
type workSpan struct {
	start, end float64
	kind       int
}

func finite(v float64) bool { return !math.IsNaN(v) && !math.IsInf(v, 0) }

func IsGC(r model.Record) bool {
	if r.EventType == "garbage-collected" {
		return true
	}
	var details struct {
		Type string `json:"__type"`
	}
	return len(r.Details) > 0 && json.Unmarshal(r.Details, &details) == nil && details.Type == "GarbageCollection"
}

func CountOnly(eventType string) bool {
	switch eventType {
	case "timer-installed", "timer-removed", "animation-frame-requested", "animation-frame-canceled":
		return true
	}
	return false
}

func category(r model.Record) (int, bool) {
	if r.Type == model.TypeScript {
		if IsGC(r) {
			return kindGC, true
		}
		return kindScript, !CountOnly(r.EventType)
	}
	if r.Type != model.TypeLayout {
		return 0, false
	}
	switch r.EventType {
	case "recalculate-styles", "invalidate-styles", "style", "styles":
		return kindStyle, true
	case "paint":
		return kindPaint, true
	case "composite":
		return kindComposite, true
	default:
		return kindLayout, true
	}
}

func exclusiveSpans(rec *model.Recording, active []Interval) []workSpan {
	points := make([]endpoint, 0, len(rec.Records)*2)
	for _, r := range rec.Records {
		kind, ok := category(r)
		if !ok || !finite(r.StartTime) || !finite(r.EndTime) || r.EndTime <= r.StartTime {
			continue
		}
		i := sort.Search(len(active), func(i int) bool { return active[i].End > r.StartTime })
		for ; i < len(active) && active[i].Start < r.EndTime; i++ {
			lo, hi := math.Max(active[i].Start, r.StartTime), math.Min(active[i].End, r.EndTime)
			if hi > lo {
				points = append(points, endpoint{lo, kind, 1}, endpoint{hi, kind, -1})
			}
		}
	}
	sort.Slice(points, func(i, j int) bool {
		if points[i].time != points[j].time {
			return points[i].time < points[j].time
		}
		if points[i].kind != points[j].kind {
			return points[i].kind < points[j].kind
		}
		return points[i].delta < points[j].delta
	})
	var counts [kindCount]int
	spans := make([]workSpan, 0, len(points)/2)
	for i := 0; i < len(points); {
		t := points[i].time
		for i < len(points) && points[i].time == t {
			counts[points[i].kind] += points[i].delta
			i++
		}
		if i == len(points) {
			break
		}
		for kind := kindCount - 1; kind >= 0; kind-- {
			if counts[kind] <= 0 {
				continue
			}
			end := points[i].time
			if len(spans) > 0 && spans[len(spans)-1].kind == kind && spans[len(spans)-1].end == t {
				spans[len(spans)-1].end = end
			} else {
				spans = append(spans, workSpan{t, end, kind})
			}
			break
		}
	}
	return spans
}

func occupancy(spans []workSpan, active []Interval, start, end float64) Occupancy {
	var ms [kindCount]float64
	var o Occupancy
	for _, a := range active {
		if hi, lo := math.Min(end, a.End), math.Max(start, a.Start); hi > lo {
			o.DurationMs += (hi - lo) * 1000
		}
	}
	i := sort.Search(len(spans), func(i int) bool { return spans[i].end > start })
	for ; i < len(spans) && spans[i].start < end; i++ {
		span := spans[i]
		ms[span.kind] += (math.Min(end, span.end) - math.Max(start, span.start)) * 1000
	}
	o.ScriptMs, o.StyleMs, o.LayoutMs = ms[kindScript], ms[kindStyle], ms[kindLayout]
	o.PaintMs, o.CompositeMs, o.GCMs = ms[kindPaint], ms[kindComposite], ms[kindGC]
	for _, value := range ms {
		o.WorkMs += value
	}
	return o
}

func Measure(rec *model.Recording, start, end float64) Occupancy {
	if rec == nil {
		return Occupancy{}
	}
	active := ActiveIntervals(rec, start, end)
	return occupancy(exclusiveSpans(rec, active), active, start, end)
}

// Frame is an observed rendering interval, not necessarily a busy refresh.
type Frame struct {
	Start           float64   `json:"start"`
	End             float64   `json:"end"`
	DurationMs      float64   `json:"durationMs"`
	Occupancy       Occupancy `json:"occupancy"`
	IdleGap         bool      `json:"idleGap"`
	DominantTrigger string    `json:"dominantTrigger,omitempty"`
}

// Idle-gap thresholds are deliberately conservative: an interval must exceed
// the 50 ms long-task boundary, contain less than 10% measured exclusive work,
// and contain no input-driven dispatch. Without work instrumentation, unknown
// occupancy cannot establish idleness. A gap is an inference, not dropped FPS.
const IdleGapMinMs = 50.0
const IdleGapMaxWorkRatio = 0.10

// IsInputDriven conservatively treats unlabelled dispatched events as possible
// input. Named background dispatches do not turn idle wakeups into slow frames.
func IsInputDriven(r model.Record) bool {
	if r.Type != model.TypeScript {
		return false
	}
	if r.EventType == "input" || r.EventType == "input-dispatched" {
		return true
	}
	if r.EventType != "event-dispatched" {
		return false
	}
	var event string
	if json.Unmarshal(r.Details, &event) != nil {
		var details struct {
			Type      string `json:"type"`
			EventType string `json:"eventType"`
			Name      string `json:"name"`
		}
		if json.Unmarshal(r.Details, &details) == nil {
			event = details.Type
			if event == "" {
				event = details.EventType
			}
			if event == "" {
				event = details.Name
			}
		}
	}
	event = strings.ToLower(event)
	if event == "" {
		return true
	}
	for _, prefix := range []string{"mouse", "pointer", "touch", "key", "drag"} {
		if strings.HasPrefix(event, prefix) {
			return true
		}
	}
	switch event {
	case "click", "dblclick", "auxclick", "wheel", "scroll", "input", "beforeinput", "change", "submit", "focus", "blur", "contextmenu", "select":
		return true
	}
	return false
}

type triggerSpan struct {
	start, end float64
	event      string
	input      bool
}

// Frames discards records overlapping a capture pause: the unobserved portion
// cannot establish frame latency. Window clipping retains existing semantics.
func Frames(rec *model.Recording, start, end float64) []Frame {
	if rec == nil || end <= start {
		return nil
	}
	active := ActiveIntervals(rec, start, end)
	spans := exclusiveSpans(rec, active)
	haveWork := false
	for _, instrument := range rec.InstrumentTypes {
		if instrument == model.TypeScript || instrument == model.TypeLayout {
			haveWork = true
		}
	}
	var triggers []triggerSpan
	for _, r := range rec.Records {
		if _, ok := category(r); ok {
			haveWork = true
		}
		if r.Type != model.TypeScript || IsGC(r) || CountOnly(r.EventType) {
			continue
		}
		triggers = append(triggers, triggerSpan{r.StartTime, math.Max(r.StartTime, r.EndTime), r.EventType, IsInputDriven(r)})
	}
	sort.Slice(triggers, func(i, j int) bool {
		if triggers[i].start != triggers[j].start {
			return triggers[i].start < triggers[j].start
		}
		if triggers[i].end != triggers[j].end {
			return triggers[i].end < triggers[j].end
		}
		return triggers[i].event < triggers[j].event
	})
	maxEnd := make([]float64, len(triggers))
	for i, r := range triggers {
		maxEnd[i] = r.end
		if i > 0 {
			maxEnd[i] = math.Max(maxEnd[i], maxEnd[i-1])
		}
	}
	var out []Frame
	for _, r := range rec.Records {
		if r.Type != model.TypeRenderingFrame || !finite(r.StartTime) || !finite(r.EndTime) {
			continue
		}
		lo, hi := math.Max(start, r.StartTime), math.Min(end, r.EndTime)
		if hi <= lo {
			continue
		}
		duration := hi - lo
		o := occupancy(spans, active, lo, hi)
		if o.DurationMs+1e-6 < duration*1000 {
			continue
		}
		f := Frame{Start: lo, End: hi, DurationMs: duration * 1000, Occupancy: o}
		weights := map[string]float64{}
		input := false
		i := sort.Search(len(maxEnd), func(i int) bool { return maxEnd[i] >= lo })
		for ; i < len(triggers) && triggers[i].start < hi; i++ {
			t := triggers[i]
			if t.end < lo {
				continue
			}
			input = input || t.input
			weights[t.event] += math.Max(0, math.Min(hi, t.end)-math.Max(lo, t.start))
		}
		var best float64 = -1
		for event, weight := range weights {
			if weight > best || (weight == best && event < f.DominantTrigger) {
				best = weight
				f.DominantTrigger = event
			}
		}
		f.IdleGap = haveWork && f.DurationMs > IdleGapMinMs && o.WorkMs/f.DurationMs < IdleGapMaxWorkRatio && !input
		out = append(out, f)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Start != out[j].Start {
			return out[i].Start < out[j].Start
		}
		return out[i].End < out[j].End
	})
	return out
}

// ForcedLayoutRecords returns explicit forced layouts, plus layout records
// whose START lies in a script span. Mere overlap before/after does not prove a
// synchronous layout. Returned records retain original, unclipped timestamps.
func ForcedLayoutRecords(rec *model.Recording) []model.Record {
	if rec == nil {
		return nil
	}
	var scripts []Interval
	for _, r := range rec.Records {
		if r.Type == model.TypeScript && Observed(rec, r) && !IsGC(r) && !CountOnly(r.EventType) && r.EndTime > r.StartTime {
			scripts = append(scripts, Interval{r.StartTime, r.EndTime})
		}
	}
	sort.Slice(scripts, func(i, j int) bool {
		if scripts[i].Start != scripts[j].Start {
			return scripts[i].Start < scripts[j].Start
		}
		return scripts[i].End < scripts[j].End
	})
	merged := make([]Interval, 0, len(scripts))
	for _, s := range scripts {
		if len(merged) > 0 && s.Start <= merged[len(merged)-1].End {
			merged[len(merged)-1].End = math.Max(merged[len(merged)-1].End, s.End)
		} else {
			merged = append(merged, s)
		}
	}
	var out []model.Record
	for _, r := range rec.Records {
		if r.Type == model.TypeLayout && Observed(rec, r) && (r.EventType == "forced-layout" || (r.EventType == "layout" && Contains(merged, r.StartTime))) {
			out = append(out, r)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].StartTime != out[j].StartTime {
			return out[i].StartTime < out[j].StartTime
		}
		if out[i].EndTime != out[j].EndTime {
			return out[i].EndTime < out[j].EndTime
		}
		return out[i].EventType < out[j].EventType
	})
	return out
}

// Observed excludes stopped capture intervals without inventing event timing.
func Observed(rec *model.Recording, r model.Record) bool {
	if rec == nil {
		return false
	}
	timestamp := r.StartTime
	switch r.Type {
	case model.TypeCPU, model.TypeMemory:
		timestamp = r.Timestamp
	case model.TypeNetwork:
		if r.StartTime == 0 && r.EndTime == 0 {
			return true
		}
	}
	if r.Type != model.TypeCPU && r.Type != model.TypeMemory {
		if !finite(r.StartTime) || !finite(r.EndTime) {
			return false
		}
		if r.EndTime > r.StartTime {
			if len(rec.Discontinuities) == 0 {
				return rec.EndTime <= rec.StartTime || (r.EndTime > rec.StartTime && r.StartTime < rec.EndTime)
			}
			return ActiveDuration(rec, r.StartTime, r.EndTime) > 0
		}
	}
	if !finite(timestamp) || (rec.EndTime > rec.StartTime && (timestamp < rec.StartTime || timestamp >= rec.EndTime)) {
		return false
	}
	for _, d := range rec.Discontinuities {
		if timestamp >= d.StartTime && timestamp < d.EndTime {
			return false
		}
	}
	return true
}
