package profile

import (
	"bytes"
	"container/heap"
	"math"
	"sort"

	"github.com/omniviewdev/wkperf/internal/model"
)

// RecordCost retains the original owner of a positive exclusive work duration.
// GC and count-only records cannot own main-thread work.
type RecordCost struct {
	Record      model.Record `json:"record"`
	ExclusiveMs float64      `json:"exclusiveMs"`
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
	ms := make([]float64, len(rec.Records))
	for _, span := range spans {
		ms[span.owner] += (span.end - span.start) * 1000
	}
	var out []RecordCost
	for i, value := range ms {
		if value > 0 {
			r := rec.Records[i]
			r.StartTime, r.EndTime = recordBounds(r)
			out = append(out, RecordCost{Record: r, ExclusiveMs: value})
		}
	}
	sort.Slice(out, func(i, j int) bool { return recordLess(out[i].Record, out[j].Record) })
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
	active          bool
}

type endpoint struct {
	time   float64
	record *workRecord
	enter  bool
}

type workHeap []*workRecord

func (h workHeap) Len() int { return len(h) }
func (h workHeap) Less(i, j int) bool {
	a, b := h[i], h[j]
	if a.start != b.start {
		return a.start > b.start
	}
	if a.duration != b.duration {
		return a.duration < b.duration
	}
	if a.kind != b.kind {
		return a.kind > b.kind
	}
	return recordLess(*a.record, *b.record)
}
func (h workHeap) Swap(i, j int) { h[i], h[j] = h[j], h[i] }
func (h *workHeap) Push(v any)   { *h = append(*h, v.(*workRecord)) }
func (h *workHeap) Pop() any {
	last := len(*h) - 1
	v := (*h)[last]
	(*h)[last] = nil
	*h = (*h)[:last]
	return v
}

func exclusiveSpans(rec *model.Recording, active []Interval) []workSpan {
	if len(active) == 0 {
		return nil
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
	pending := make(workHeap, 0, len(rec.Records))
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
		for len(pending) > 0 && !pending[0].active {
			heap.Pop(&pending)
		}
		if i == len(points) || len(pending) == 0 {
			continue
		}
		owner := pending[0]
		end := points[i].time
		if n := len(spans); n > 0 && spans[n-1].owner == owner.owner && spans[n-1].end == t {
			spans[n-1].end = end
		} else {
			spans = append(spans, workSpan{start: t, end: end, kind: owner.kind, owner: owner.owner})
		}
	}
	return spans
}

func collectorIntervals(rec *model.Recording, active []Interval) []Interval {
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
