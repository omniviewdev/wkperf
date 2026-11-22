package profile

import (
	"bytes"
	"container/heap"
	"math"
	"slices"
	"sort"

	"github.com/omniviewdev/wkperf/internal/model"
)

// RecordCost retains the original owner of a positive exclusive work duration.
// GC and count-only records cannot own main-thread work.
type RecordCost struct {
	Record      model.Record `json:"record"`
	ExclusiveMs float64      `json:"exclusiveMs"`
}

// WorkIndex reuses one exclusive sweep for subwindows of a recording. It
// retains the original record bounds and visits intersecting spans in time order.
type WorkIndex struct {
	rec         *model.Recording
	active      []Interval
	spans       []workSpan
	collections []Interval
	ambiguous   []Interval
}

// NewWorkIndex indexes activity within the supplied absolute bounds.
func NewWorkIndex(rec *model.Recording, start, end float64) *WorkIndex {
	return newWorkIndex(rec, start, end, true)
}

func newWorkIndex(rec *model.Recording, start, end float64, trackTies bool) *WorkIndex {
	active := ActiveIntervals(rec, start, end)
	index := &WorkIndex{rec: rec, active: active}
	if len(active) != 0 {
		index.spans, index.ambiguous = sweepExclusiveSpans(rec, active, trackTies)
		index.collections = collectorIntervals(rec, active)
	}
	return index
}

// spansFor preserves historical heap tie behavior. Clipping endpoints can
// change which otherwise equal owner wins, or where a duration sum is split.
// The active intervals and collector union do not depend on those owners.
func (w *WorkIndex) spansFor(start, end float64) []workSpan {
	if len(w.ambiguous) == 0 || start <= w.active[0].Start && end >= w.active[len(w.active)-1].End {
		return w.spans
	}
	i := sort.Search(len(w.ambiguous), func(i int) bool { return w.ambiguous[i].End > start })
	if i == len(w.ambiguous) || w.ambiguous[i].Start >= end {
		return w.spans
	}
	var clipped []Interval
	for i := sort.Search(len(w.active), func(i int) bool { return w.active[i].End > start }); i < len(w.active) && w.active[i].Start < end; i++ {
		a := w.active[i]
		clipped = append(clipped, Interval{math.Max(start, a.Start), math.Min(end, a.End)})
	}
	return exclusiveSpans(w.rec, clipped)
}

// Measure returns clipped occupancy without reassociating floating-point sums.
func (w *WorkIndex) Measure(start, end float64) Occupancy {
	if !finite(start) || !finite(end) || end <= start {
		return Occupancy{}
	}
	return occupancy(w.spansFor(start, end), w.collections, w.active, start, end)
}

// ActiveDuration returns observed seconds, summed in the same order as
// ActiveDuration. Multiplying that sum differs from summing milliseconds.
func (w *WorkIndex) ActiveDuration(start, end float64) float64 {
	var duration float64
	if !finite(start) || !finite(end) || end <= start {
		return duration
	}
	first := sort.Search(len(w.active), func(i int) bool { return w.active[i].End > start })
	for i := first; i < len(w.active) && w.active[i].Start < end; i++ {
		a := w.active[i]
		duration += math.Min(end, a.End) - math.Max(start, a.Start)
	}
	return duration
}

// MeasureRecords returns occupancy and costs of original records intersecting
// the window. The index must cover the requested window.
func (w *WorkIndex) MeasureRecords(start, end float64) (Occupancy, []RecordCost) {
	if !finite(start) || !finite(end) || end <= start {
		return Occupancy{}, nil
	}
	spans := w.spansFor(start, end)
	return occupancy(spans, w.collections, w.active, start, end), w.recordCosts(spans, start, end)
}

// ExclusiveRecords returns costs of original records intersecting the window.
func (w *WorkIndex) ExclusiveRecords(start, end float64) []RecordCost {
	if !finite(start) || !finite(end) || end <= start {
		return nil
	}
	return w.recordCosts(w.spansFor(start, end), start, end)
}

func (w *WorkIndex) recordCosts(spans []workSpan, start, end float64) []RecordCost {
	if len(spans) == 0 {
		return nil
	}
	if start <= w.active[0].Start && w.active[len(w.active)-1].End <= end {
		return recordCosts(w.rec, spans)
	}
	first := sort.Search(len(spans), func(i int) bool { return spans[i].end > start })
	ms := map[int]float64{}
	for i := first; i < len(spans) && spans[i].start < end; i++ {
		s := spans[i]
		ms[s.owner] += (math.Min(end, s.end) - math.Max(start, s.start)) * 1000
	}
	if len(ms) == 0 {
		return nil
	}
	owners := make([]int, 0, len(ms))
	for owner := range ms {
		owners = append(owners, owner)
	}
	// Match the dense path's order before sorting by record identity.
	slices.Sort(owners)
	out := make([]RecordCost, 0, len(owners))
	for _, owner := range owners {
		out = appendRecordCost(out, w.rec, owner, ms[owner])
	}
	sort.Slice(out, func(i, j int) bool { return recordLess(out[i].Record, out[j].Record) })
	return out
}

// ExclusiveRecords returns original records with positive exclusive costs,
// ordered by original bounds and deterministic record identity.
func ExclusiveRecords(rec *model.Recording, start, end float64) []RecordCost {
	if rec == nil {
		return nil
	}
	active := ActiveIntervals(rec, start, end)
	return recordCosts(rec, exclusiveSpans(rec, active))
}

// MeasureRecords returns occupancy and original record costs from one sweep.
func MeasureRecords(rec *model.Recording, start, end float64) (Occupancy, []RecordCost) {
	if rec == nil {
		return Occupancy{}, nil
	}
	active := ActiveIntervals(rec, start, end)
	spans := exclusiveSpans(rec, active)
	return occupancy(spans, collectorIntervals(rec, active), active, start, end), recordCosts(rec, spans)
}

func recordCosts(rec *model.Recording, spans []workSpan) []RecordCost {
	if len(spans) == 0 {
		return nil
	}
	ms := make([]float64, len(rec.Records))
	for _, span := range spans {
		ms[span.owner] += (span.end - span.start) * 1000
	}
	var out []RecordCost
	for i, value := range ms {
		out = appendRecordCost(out, rec, i, value)
	}
	sort.Slice(out, func(i, j int) bool { return recordLess(out[i].Record, out[j].Record) })
	return out
}

func appendRecordCost(out []RecordCost, rec *model.Recording, owner int, value float64) []RecordCost {
	if value > 0 {
		r := rec.Records[owner]
		r.StartTime, r.EndTime = recordBounds(r)
		out = append(out, RecordCost{Record: r, ExclusiveMs: value})
	}
	return out
}

func recordBounds(r model.Record) (float64, float64) {
	if r.OriginalEndTime > r.OriginalStartTime {
		return r.OriginalStartTime, r.OriginalEndTime
	}
	return r.StartTime, r.EndTime
}

// recordLess orders meaningful script/layout identity without allocating keys.
func recordLess(a, b model.Record) bool {
	as, ae := recordBounds(a)
	bs, be := recordBounds(b)
	if as != bs {
		return as < bs
	}
	if ae != be {
		return ae < be
	}
	if a.Type != b.Type {
		return a.Type < b.Type
	}
	if a.EventType != b.EventType {
		return a.EventType < b.EventType
	}
	if d := bytes.Compare(a.Details, b.Details); d != 0 {
		return d < 0
	}
	if a.DOMNodeDisplayName != b.DOMNodeDisplayName {
		return a.DOMNodeDisplayName < b.DOMNodeDisplayName
	}
	if a.DOMNodeCSSPath != b.DOMNodeCSSPath {
		return a.DOMNodeCSSPath < b.DOMNodeCSSPath
	}
	if a.Target == nil || b.Target == nil {
		return a.Target == nil && b.Target != nil
	}
	if a.Target.Identifier != b.Target.Identifier {
		return a.Target.Identifier < b.Target.Identifier
	}
	if a.Target.Type != b.Target.Type {
		return a.Target.Type < b.Target.Type
	}
	if a.Target.Name != b.Target.Name {
		return a.Target.Name < b.Target.Name
	}
	return a.Target.URL < b.Target.URL
}

type workRecord struct {
	record          *model.Record
	start, duration float64
	owner, kind     int
	active, tied    bool
}

type endpoint struct {
	time   float64
	record *workRecord
	enter  bool
}

type workHeap struct {
	records   []*workRecord
	trackTies bool
	ties      bool
}

func (h *workHeap) Len() int { return len(h.records) }
func (h *workHeap) Less(i, j int) bool {
	a, b := h.records[i], h.records[j]
	if a.start != b.start {
		return a.start > b.start
	}
	if a.duration != b.duration {
		return a.duration < b.duration
	}
	if a.kind != b.kind {
		return a.kind > b.kind
	}
	if recordLess(*a.record, *b.record) {
		return true
	}
	if h.trackTies && a.owner != b.owner && (!a.tied || !b.tied) && !recordLess(*b.record, *a.record) {
		a.tied, b.tied, h.ties = true, true, true
	}
	return false
}
func (h *workHeap) Swap(i, j int) { h.records[i], h.records[j] = h.records[j], h.records[i] }
func (h *workHeap) Push(v any)    { h.records = append(h.records, v.(*workRecord)) }
func (h *workHeap) Pop() any {
	last := len(h.records) - 1
	v := h.records[last]
	h.records[last] = nil
	h.records = h.records[:last]
	return v
}

func exclusiveSpans(rec *model.Recording, active []Interval) []workSpan {
	spans, _ := sweepExclusiveSpans(rec, active, false)
	return spans
}

func sweepExclusiveSpans(rec *model.Recording, active []Interval, trackTies bool) ([]workSpan, []Interval) {
	if len(active) == 0 {
		return nil, nil
	}
	owners := make([]workRecord, len(rec.Records))
	points := make([]endpoint, 0, len(rec.Records)*2)
	for i := range rec.Records {
		r := &rec.Records[i]
		kind, ok := category(*r)
		if !ok || !finite(r.StartTime) || !finite(r.EndTime) || r.EndTime <= r.StartTime {
			continue
		}
		start, end := recordBounds(*r)
		owners[i] = workRecord{record: r, start: start, duration: end - start, owner: i, kind: kind}
		for j := sort.Search(len(active), func(j int) bool { return active[j].End > r.StartTime }); j < len(active) && active[j].Start < r.EndTime; j++ {
			lo, hi := math.Max(active[j].Start, r.StartTime), math.Min(active[j].End, r.EndTime)
			if hi > lo {
				points = append(points, endpoint{lo, &owners[i], true}, endpoint{hi, &owners[i], false})
			}
		}
	}
	sort.Slice(points, func(i, j int) bool {
		if points[i].time != points[j].time {
			return points[i].time < points[j].time
		}
		if points[i].enter != points[j].enter {
			return !points[i].enter
		}
		return recordLess(*points[i].record.record, *points[j].record.record)
	})
	pending := workHeap{records: make([]*workRecord, 0, len(rec.Records)), trackTies: trackTies}
	var spans []workSpan
	for i := 0; i < len(points); {
		t := points[i].time
		for i < len(points) && points[i].time == t {
			point := points[i]
			point.record.active = point.enter
			if point.enter {
				heap.Push(&pending, point.record)
			}
			i++
		}
		for pending.Len() > 0 && !pending.records[0].active {
			heap.Pop(&pending)
		}
		if i == len(points) || pending.Len() == 0 {
			continue
		}
		owner := pending.records[0]
		end := points[i].time
		if n := len(spans); n > 0 && spans[n-1].owner == owner.owner && spans[n-1].end == t {
			spans[n-1].end = end
		} else {
			spans = append(spans, workSpan{start: t, end: end, kind: owner.kind, owner: owner.owner})
		}
	}
	if pending.ties {
		return spans, ambiguousIntervals(owners, active)
	}
	return spans, nil
}

// Heap comparisons can involve expired owners. Group equal owners before
// finding overlaps so an expired owner cannot hide a tie between live ones.
func ambiguousIntervals(owners []workRecord, active []Interval) []Interval {
	var tied []*model.Record
	for i := range owners {
		if owners[i].tied {
			tied = append(tied, owners[i].record)
		}
	}
	sort.Slice(tied, func(i, j int) bool {
		a, b := tied[i], tied[j]
		if recordLess(*a, *b) {
			return true
		}
		return !recordLess(*b, *a) && a.StartTime < b.StartTime
	})
	var out []Interval
	var group *model.Record
	var end float64
	for _, r := range tied {
		if group == nil || recordLess(*group, *r) {
			group, end = r, r.EndTime
			continue
		}
		hi := math.Min(end, r.EndTime)
		if r.StartTime < hi {
			out = append(out, Interval{r.StartTime, hi})
		}
		end = math.Max(end, r.EndTime)
	}
	out = unionIntervals(out)
	var observed []Interval
	for i, j := 0, 0; i < len(out) && j < len(active); {
		a, b := out[i], active[j]
		lo, hi := math.Max(a.Start, b.Start), math.Min(a.End, b.End)
		if lo < hi {
			observed = append(observed, Interval{lo, hi})
		}
		if a.End <= b.End {
			i++
		} else {
			j++
		}
	}
	return observed
}

func collectorIntervals(rec *model.Recording, active []Interval) []Interval {
	if len(active) == 0 {
		return nil
	}
	var out []Interval
	for _, r := range rec.Records {
		if !IsGC(r) || !finite(r.StartTime) || !finite(r.EndTime) || r.EndTime <= r.StartTime {
			continue
		}
		for i := sort.Search(len(active), func(i int) bool { return active[i].End > r.StartTime }); i < len(active) && active[i].Start < r.EndTime; i++ {
			lo, hi := math.Max(active[i].Start, r.StartTime), math.Min(active[i].End, r.EndTime)
			if hi > lo {
				out = append(out, Interval{lo, hi})
			}
		}
	}
	return unionIntervals(out)
}

func unionIntervals(out []Interval) []Interval {
	if len(out) < 2 {
		return out
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Start != out[j].Start {
			return out[i].Start < out[j].Start
		}
		return out[i].End < out[j].End
	})
	n := 0
	for _, interval := range out {
		if n > 0 && interval.Start <= out[n-1].End {
			out[n-1].End = math.Max(out[n-1].End, interval.End)
		} else {
			out[n] = interval
			n++
		}
	}
	return out[:n]
}
