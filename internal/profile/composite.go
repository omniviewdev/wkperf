package profile

import (
	"math"

	"github.com/omniviewdev/wkperf/internal/model"
)

// EventComposite is the layout record event type of one compositing pass.
const EventComposite = "composite"

// CompositeStallThresholdMs is the longest composite record that still counts
// as main-thread work. WebKit can report the end of a composite seconds after
// it started, so a longer span measures late reporting, not occupancy.
const CompositeStallThresholdMs = 50.0

// IsStalledComposite reports whether r is a composite record longer than
// CompositeStallThresholdMs. This is the single definition of the rule:
// exclusive work, the time breakdown, frame and finding attribution and the
// per-event scalar metrics all call it, so no consumer repeats the threshold.
//
// Original bounds decide, so clipping a stalled record to an analysis window
// cannot turn it into short work. Spans are compared at nanosecond resolution:
// a 50 ms record must not be tipped over the threshold by floating-point noise
// in its timestamps.
func IsStalledComposite(r model.Record) bool {
	if r.Type != model.TypeLayout || r.EventType != EventComposite {
		return false
	}
	start, end := recordBounds(r)
	return finite(start) && finite(end) && math.Round((end-start)*1e9) > CompositeStallThresholdMs*1e6
}
