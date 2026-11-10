package compare

import (
	"testing"

	"github.com/omniviewdev/wkperf/internal/analyze"
	"github.com/omniviewdev/wkperf/internal/metrics"
	"github.com/omniviewdev/wkperf/internal/model"
)

func TestUntimedClockDoesNotCreateFunctionImprovements(t *testing.T) {
	recording := func(untimed bool) *model.Recording {
		at := 11.0
		if untimed {
			at = 0
		}
		return &model.Recording{StartTime: 10, EndTime: 12, Samples: []model.TargetSamples{{
			StackTraces: []model.StackTrace{
				{Timestamp: at, StackFrames: []model.StackFrame{{Name: "unknownClock", URL: "app.js", Line: 1}}},
				{Timestamp: 11.1, StackFrames: []model.StackFrame{{Name: "shared", URL: "app.js", Line: 2}}},
			}, Durations: []float64{0.025, 0.04},
		}}}
	}
	base := analyze.Summarize(recording(false), "timed.json")
	candidate := analyze.Summarize(recording(true), "untimed.json")
	r := Compare([]*metrics.Summary{base}, []*metrics.Summary{candidate}, DefaultOptions())
	if len(r.FuncImproved) != 0 || len(r.FuncRegressed) != 0 || candidate.Fixture.SampleCount != 2 {
		t.Fatalf("clock availability created phantom improvement: improved=%+v regressed=%+v fixture=%+v", r.FuncImproved, r.FuncRegressed, candidate.Fixture)
	}
}
