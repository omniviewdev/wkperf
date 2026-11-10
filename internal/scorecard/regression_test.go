package scorecard

import (
	"testing"

	"github.com/omniviewdev/wkperf/internal/insights"
	"github.com/omniviewdev/wkperf/internal/model"
)

func TestIdleSafeFrameStandards(t *testing.T) {
	r := &model.Recording{EndTime: 10}
	for i := range 10 {
		s := float64(i)
		r.Records = append(r.Records, model.Record{Type: model.TypeRenderingFrame, StartTime: s, EndTime: s + 1}, script("timer-fired", s+0.001, s+0.006, "poll"))
	}
	c := Evaluate(r, insights.Analyze(r, "idle.json", insights.Options{}))
	for _, id := range []string{"frame-p95", "jank-ratio", "fps"} {
		if result := get(t, c, id); result.Rating != Unavailable {
			t.Errorf("%s must not grade idle-gap cadence: %+v", id, result)
		}
	}
}

func TestAbsentInstrumentsCannotEarnHealthyScores(t *testing.T) {
	r := &model.Recording{EndTime: 10, Records: []model.Record{{Type: model.TypeMemory, Timestamp: 1}}}
	c := Evaluate(r, insights.Analyze(r, "memory-only.json", insights.Options{}))
	for _, id := range []string{"tbt", "long-task-rate", "gc-share", "forced-layout-rate", "main-busy"} {
		if result := get(t, c, id); result.Rating != Unavailable {
			t.Errorf("%s absent instrument must be unavailable, got %+v", id, result)
		}
	}
}

func TestForcedLayoutScoreMatchesInsideScriptEvidence(t *testing.T) {
	r := &model.Recording{EndTime: 2, Records: []model.Record{
		script("event-dispatched", 0.1, 0.2, "click"),
		{Type: model.TypeLayout, EventType: "layout", StartTime: 0.15, EndTime: 0.16},
		{Type: model.TypeLayout, EventType: "layout", StartTime: 0.2, EndTime: 0.21},
		{Type: model.TypeLayout, EventType: "forced-layout", StartTime: 1, EndTime: 1.01},
	}}
	c := Evaluate(r, insights.Analyze(r, "forced.json", insights.Options{}))
	if result := get(t, c, "forced-layout-rate"); result.Value != 1 {
		t.Fatalf("inside-script and explicit forced count once; post-script layout not forced: %+v", result)
	}
}

func TestPausedRecordsDoNotEarnGradesOrCreateBlocking(t *testing.T) {
	r := &model.Recording{EndTime: 10, Discontinuities: []model.Discontinuity{{StartTime: 2, EndTime: 8}}, Records: []model.Record{script("event-dispatched", 3, 7, "click"), {Type: model.TypeLayout, EventType: "forced-layout", StartTime: 3.5, EndTime: 3.6}}}
	c := Evaluate(r, insights.Analyze(r, "paused.json", insights.Options{}))
	for _, id := range []string{"tbt", "long-task-rate", "inp", "gc-share", "forced-layout-rate"} {
		if result := get(t, c, id); result.Rating != Unavailable {
			t.Errorf("%s paused-only records must not earn a grade: %+v", id, result)
		}
	}
}
