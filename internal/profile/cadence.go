package profile

import (
	"math"
	"sort"

	"github.com/omniviewdev/wkperf/internal/model"
)

// Composite cadence is an estimate from composite record start times. It is not
// frame latency and never stands in for rendering-frame statistics.
const (
	// CadenceRunGapMs groups nearby composite starts into one active run.
	CadenceRunGapMs = 100.0
	// CadenceMissedVsyncMs is a spacing threshold of 1.5 nominal frame
	// budgets, not evidence of a missed display refresh.
	CadenceMissedVsyncMs = FrameBudgetMs * 1.5
	// CadenceSlowIntervalMs matches the 50 ms boundary of slow frames and long tasks.
	CadenceSlowIntervalMs = LongTaskBudgetMs
)

// CadenceSample is the raw spacing evidence behind a composite cadence estimate.
type CadenceSample struct {
	Starts      int       // composite record starts inside the selection
	Runs        int       // active runs holding at least two starts
	IntervalsMs []float64 // spacing of consecutive starts within one run, in start order
}

// CompositeCadence collects the spacing between consecutive composite record
// START times inside [start,end). Every composite record contributes its start,
// stalled ones included: only their reported end is unreliable. Starts closer
// than CadenceRunGapMs form one active run; the gap between runs is not an
// interval. A capture pause also ends a run, since the composites inside it
// were never observed. Original bounds supply each start, so a window clip
// neither invents a start at the window edge nor keeps one that began before it.
func CompositeCadence(rec *model.Recording, start, end float64) CadenceSample {
	var sample CadenceSample
	if rec == nil {
		return sample
	}
	active := ActiveIntervals(rec, start, end)
	type observed struct {
		at      float64
		segment int // index of the active interval holding the start
	}
	var starts []observed
	for _, r := range rec.Records {
		if r.Type != model.TypeLayout || r.EventType != EventComposite {
			continue
		}
		at, _ := recordBounds(r)
		if !finite(at) {
			continue
		}
		i := sort.Search(len(active), func(i int) bool { return active[i].End > at })
		if i == len(active) || active[i].Start > at {
			continue
		}
		starts = append(starts, observed{at, i})
	}
	sort.Slice(starts, func(i, j int) bool { return starts[i].at < starts[j].at })
	sample.Starts = len(starts)
	inRun := false
	for i := 1; i < len(starts); i++ {
		gap := starts[i].at - starts[i-1].at
		// Whole nanoseconds, as for stalled composites, keep timestamp rounding from
		// moving a gap across the run boundary.
		if starts[i].segment != starts[i-1].segment || math.Round(gap*1e9) >= CadenceRunGapMs*1e6 {
			inRun = false
			continue
		}
		if !inRun {
			sample.Runs++
			inRun = true
		}
		sample.IntervalsMs = append(sample.IntervalsMs, gap*1000)
	}
	return sample
}
