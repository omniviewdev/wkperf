package profile

import (
	"math"
	"sort"

	"github.com/omniviewdev/wkperf/internal/model"
)

// WebKit exports these script event names; invented *-fired aliases are not used.
const (
	EventMicrotaskDispatched = "microtask-dispatched"
	EventObserverCallback    = "observer-callback"
)

// Task is a maximal overlapping script union within one observed capture span.
// Record carries the longest original member's label and the union's bounds.
// Children lists other member event types once, in lexical order.
type Task struct {
	model.Record
	Children []string `json:"children,omitempty"`
}

// Tasks excludes GC and count-only events. Touching endpoints stay separate,
// and capture pauses split work rather than joining unobserved task time.
func Tasks(rec *model.Recording) []Task {
	if rec == nil {
		return nil
	}
	active := ActiveIntervals(rec, rec.StartTime, rec.EndTime)
	type part struct {
		start, end float64
		owner      int
	}
	var parts []part
	for i, r := range rec.Records {
		if r.Type != model.TypeScript || IsGC(r) || CountOnly(r.EventType) || !finite(r.StartTime) || !finite(r.EndTime) || r.EndTime <= r.StartTime {
			continue
		}
		for j := sort.Search(len(active), func(j int) bool { return active[j].End > r.StartTime }); j < len(active) && active[j].Start < r.EndTime; j++ {
			lo, hi := math.Max(active[j].Start, r.StartTime), math.Min(active[j].End, r.EndTime)
			if hi > lo {
				parts = append(parts, part{lo, hi, i})
			}
		}
	}
	sort.Slice(parts, func(i, j int) bool {
		a, b := parts[i], parts[j]
		if a.start != b.start {
			return a.start < b.start
		}
		if a.end != b.end {
			return a.end > b.end
		}
		return recordLess(rec.Records[a.owner], rec.Records[b.owner])
	})
	var out []Task
	label := -1
	var members []string
	finish := func() {
		if len(out) == 0 {
			return
		}
		task := &out[len(out)-1]
		sort.Strings(members)
		for _, event := range members {
			if event == task.EventType || (len(task.Children) > 0 && task.Children[len(task.Children)-1] == event) {
				continue
			}
			task.Children = append(task.Children, event)
		}
		members = members[:0]
	}
	for _, part := range parts {
		r := rec.Records[part.owner]
		if len(out) == 0 || part.start >= out[len(out)-1].EndTime {
			finish()
			label = part.owner
			r.StartTime, r.EndTime = part.start, part.end
			out = append(out, Task{Record: r})
		} else {
			task := &out[len(out)-1]
			task.EndTime = math.Max(task.EndTime, part.end)
			if taskLabelLess(r, rec.Records[label]) {
				start, end := task.StartTime, task.EndTime
				task.Record = r
				task.StartTime, task.EndTime = start, end
				label = part.owner
			}
		}
		members = append(members, r.EventType)
	}
	finish()
	return out
}

func taskLabelLess(a, b model.Record) bool {
	as, ae := recordBounds(a)
	bs, be := recordBounds(b)
	if ae-as != be-bs {
		return ae-as > be-bs
	}
	return recordLess(a, b)
}
