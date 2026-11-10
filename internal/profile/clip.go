package profile

import (
	"math"
	"sort"

	"github.com/omniviewdev/wkperf/internal/model"
)

// Clip selects an absolute recording window without changing statistical sample
// weights. Read-only frame/source metadata is shared with the source recording.
func Clip(rec *model.Recording, start, end float64) *model.Recording {
	if rec == nil {
		return nil
	}
	if CoversRecording(rec, start-rec.StartTime, end-rec.StartTime) {
		return rec
	}
	out := *rec
	out.UntimedExcluded = true
	if out.SampleEvidence == nil {
		out.SampleEvidence = &model.SampleEvidence{StartTime: rec.StartTime, EndTime: rec.EndTime, Samples: rec.Samples}
	}
	start = math.Max(start, rec.StartTime)
	end = math.Min(end, rec.EndTime)
	if end < start {
		end = start
	}
	out.StartTime, out.EndTime = start, end
	active := ActiveIntervals(rec, start, end)
	out.Records = make([]model.Record, 0)
	for _, r := range rec.Records {
		keep := false
		switch r.Type {
		case model.TypeCPU, model.TypeMemory:
			keep = Contains(active, r.Timestamp)
		case model.TypeNetwork:
			if r.StartTime == 0 && r.EndTime == 0 {
				keep = true
			} else {
				keep = overlaps(active, r.StartTime, r.EndTime)
			}
		default:
			if r.EndTime <= r.StartTime {
				keep = Contains(active, r.StartTime)
			} else {
				keep = overlaps(active, r.StartTime, r.EndTime)
			}
		}
		if !keep {
			continue
		}
		if r.EndTime > r.StartTime {
			r.StartTime = math.Max(start, r.StartTime)
			r.EndTime = math.Min(end, r.EndTime)
		}
		out.Records = append(out.Records, r)
	}
	out.Discontinuities = make([]model.Discontinuity, 0)
	for _, d := range rec.Discontinuities {
		d.StartTime, d.EndTime = math.Max(start, d.StartTime), math.Min(end, d.EndTime)
		if d.EndTime > d.StartTime {
			out.Discontinuities = append(out.Discontinuities, d)
		}
	}
	out.Markers = make([]model.Marker, 0)
	for _, m := range rec.Markers {
		if Contains(active, m.Time) {
			out.Markers = append(out.Markers, m)
		}
	}
	out.MemoryPressureEvents = make([]model.MemoryPressure, 0)
	for _, e := range rec.MemoryPressureEvents {
		if Contains(active, e.Timestamp) {
			out.MemoryPressureEvents = append(out.MemoryPressureEvents, e)
		}
	}
	out.Samples = make([]model.TargetSamples, 0, len(rec.Samples))
	for _, t := range rec.Samples {
		selected := model.TargetSamples{Target: t.Target, StackTraces: []model.StackTrace{}, Durations: []float64{}}
		for i, stack := range t.StackTraces {
			if !UntimedTimestamp(rec, stack.Timestamp) && !Contains(active, stack.Timestamp) {
				continue
			}
			weight := .001
			if i < len(t.Durations) {
				weight = t.Durations[i]
			}
			selected.StackTraces = append(selected.StackTraces, stack)
			selected.Durations = append(selected.Durations, weight)
		}
		out.Samples = append(out.Samples, selected)
	}
	out.SampleStackTraces, out.SampleDurations = nil, nil
	return &out
}

func overlaps(intervals []Interval, start, end float64) bool {
	i := sort.Search(len(intervals), func(i int) bool { return intervals[i].End > start })
	return i < len(intervals) && intervals[i].Start < end
}
