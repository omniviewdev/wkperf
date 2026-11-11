package audit

import (
	"math"
	"testing"

	"github.com/omniviewdev/wkperf/internal/insights"
	"github.com/omniviewdev/wkperf/internal/model"
)

func TestAuditIncludesUntimedSceneHotspots(t *testing.T) {
	rec := &model.Recording{StartTime: 10, EndTime: 12, Samples: []model.TargetSamples{{
		StackTraces: []model.StackTrace{
			{Timestamp: 0, StackFrames: []model.StackFrame{{Name: "unknownClock", URL: "app.js", Line: 1}}},
			{Timestamp: 11.1, StackFrames: []model.StackFrame{{Name: "shared", URL: "app.js", Line: 2}}},
		}, Durations: []float64{0.025, 0.04},
	}}}
	profile := insights.Analyze(rec, "untimed.json", insights.Options{})
	r := Build([]Scene{{Name: "scene", Runs: []Run{{Report: profile}}}}, Options{})
	if len(r.Scenes) != 1 || r.Scenes[0].Rep.SampleCoverage.SampleCount != 2 || math.Abs(r.Scenes[0].Rep.SampleCoverage.SampledMs-65) > 1e-9 {
		t.Fatalf("audit scene dropped untimed samples: %+v", r.Scenes)
	}
	found := false
	for _, hotspot := range r.Hotspots {
		if hotspot.Name == "unknownClock" {
			found = true
		}
	}
	if !found {
		t.Fatalf("audit aggregate dropped untimed hotspot: %+v", r.Hotspots)
	}
}
