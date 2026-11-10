package profile

import (
	"encoding/json"
	"math"
	"testing"

	"github.com/omniviewdev/wkperf/internal/model"
	"github.com/omniviewdev/wkperf/internal/testgen"
)

func closeTo(a, b float64) bool { return math.Abs(a-b) < 1e-6 }

func TestExclusiveOccupancyAndPausedIntervals(t *testing.T) {
	rec := &model.Recording{StartTime: 0, EndTime: 1, Discontinuities: []model.Discontinuity{{StartTime: .4, EndTime: .6}}, Records: []model.Record{
		{Type: model.TypeScript, EventType: "timer-fired", StartTime: 0, EndTime: 1},
		{Type: model.TypeScript, EventType: "function-call", StartTime: .05, EndTime: .95},
		{Type: model.TypeLayout, EventType: "recalculate-styles", StartTime: .1, EndTime: .2},
		{Type: model.TypeLayout, EventType: "layout", StartTime: .2, EndTime: .3},
		{Type: model.TypeLayout, EventType: "paint", StartTime: .3, EndTime: .35},
		{Type: model.TypeLayout, EventType: "composite", StartTime: .35, EndTime: .4},
		{Type: model.TypeScript, EventType: "garbage-collected", StartTime: .7, EndTime: .75},
	}}
	o := Measure(rec, 0, 1)
	if !closeTo(ActiveDuration(rec, 0, 1), .8) || !closeTo(o.DurationMs, 800) || !closeTo(o.WorkMs, 800) {
		t.Fatalf("paused or duplicate work counted: %+v", o)
	}
	if !closeTo(o.ScriptMs, 450) || !closeTo(o.StyleMs, 100) || !closeTo(o.LayoutMs, 100) || !closeTo(o.PaintMs, 50) || !closeTo(o.CompositeMs, 50) || !closeTo(o.GCMs, 50) {
		t.Fatalf("exclusive categories: %+v", o)
	}
}

func TestIdleGapInputAndPauseFrames(t *testing.T) {
	rec := &model.Recording{StartTime: 0, EndTime: 4, Discontinuities: []model.Discontinuity{{StartTime: 2.5, EndTime: 3.5}}, Records: []model.Record{
		{Type: model.TypeRenderingFrame, StartTime: 0, EndTime: 1},
		{Type: model.TypeScript, EventType: "timer-fired", StartTime: .99, EndTime: .995},
		{Type: model.TypeLayout, EventType: "paint", StartTime: .995, EndTime: 1},
		{Type: model.TypeRenderingFrame, StartTime: 1, EndTime: 2},
		{Type: model.TypeScript, EventType: "event-dispatched", Details: json.RawMessage(`"click"`), StartTime: 1.99, EndTime: 1.995},
		{Type: model.TypeRenderingFrame, StartTime: 2, EndTime: 4},
	}}
	fs := Frames(rec, 0, 4)
	if len(fs) != 2 || !fs[0].IdleGap || fs[0].DominantTrigger != "timer-fired" || fs[1].IdleGap {
		t.Fatalf("idle/input/pause classification: %+v", fs)
	}
	if !closeTo(fs[0].Occupancy.WorkMs, 10) {
		t.Fatalf("work = %+v", fs[0].Occupancy)
	}
}

func TestForcedLayoutUsesStartContainment(t *testing.T) {
	rec := &model.Recording{Records: []model.Record{
		{Type: model.TypeScript, EventType: "timer-fired", StartTime: 1, EndTime: 2},
		{Type: model.TypeLayout, EventType: "layout", StartTime: .9, EndTime: 1.1},
		{Type: model.TypeLayout, EventType: "layout", StartTime: 1.9, EndTime: 2.1},
		{Type: model.TypeLayout, EventType: "layout", StartTime: 2, EndTime: 2.1},
		{Type: model.TypeLayout, EventType: "forced-layout", StartTime: 3, EndTime: 3.1},
	}}
	got := ForcedLayoutRecords(rec)
	if len(got) != 2 || got[0].StartTime != 1.9 || got[1].EventType != "forced-layout" {
		t.Fatalf("forced layouts: %+v", got)
	}
}

func TestClipPreservesStatisticalSampleWeights(t *testing.T) {
	rec := testgen.Empty()
	rec.Discontinuities = []model.Discontinuity{{StartTime: 2, EndTime: 4}}
	rec.Records = []model.Record{
		{Type: model.TypeScript, EventType: "timer-fired", StartTime: 1, EndTime: 5},
		{Type: model.TypeScript, EventType: "timer-fired", StartTime: 3, EndTime: 3.1},
		{Type: model.TypeCPU, Timestamp: 3, Usage: 50},
	}
	rec.Samples = []model.TargetSamples{{StackTraces: []model.StackTrace{
		{Timestamp: 1.5, StackFrames: []model.StackFrame{{Name: "inside"}}},
		{Timestamp: 3, StackFrames: []model.StackFrame{{Name: "paused"}}},
		{Timestamp: 4.5, StackFrames: []model.StackFrame{{Name: "after"}}},
		{Timestamp: 5, StackFrames: []model.StackFrame{{Name: "boundary"}}},
	}, Durations: []float64{.7, .2, .3, .1}}}
	got := Clip(rec, 1.5, 5)
	if got.StartTime != 1.5 || got.EndTime != 5 || len(got.Records) != 1 || got.Records[0].StartTime != 1.5 {
		t.Fatalf("clip records/bounds: %+v", got)
	}
	if len(got.Samples) != 1 || len(got.Samples[0].Durations) != 2 || got.Samples[0].Durations[0] != .7 || got.Samples[0].Durations[1] != .3 {
		t.Fatalf("statistical weights changed or pause included: %+v", got.Samples)
	}
	if rec.Records[0].StartTime != 1 || len(rec.Samples[0].StackTraces) != 4 {
		t.Fatal("clip mutated source")
	}
}

func TestObservedRecordsExcludePauseAndHalfOpenBoundaries(t *testing.T) {
	rec := testgen.Empty()
	rec.Discontinuities = []model.Discontinuity{{StartTime: 2, EndTime: 4}}
	for _, tc := range []struct {
		name   string
		record model.Record
		want   bool
	}{
		{"paused span", model.Record{Type: model.TypeScript, StartTime: 3, EndTime: 3.1}, false},
		{"partially captured span", model.Record{Type: model.TypeScript, StartTime: 1, EndTime: 5}, true},
		{"pause start instant", model.Record{Type: model.TypeScript, StartTime: 2, EndTime: 2}, false},
		{"resume instant", model.Record{Type: model.TypeScript, StartTime: 4, EndTime: 4}, true},
		{"capture end instant", model.Record{Type: model.TypeScript, StartTime: 10, EndTime: 10}, false},
		{"paused CPU", model.Record{Type: model.TypeCPU, Timestamp: 3}, false},
		{"resumed memory", model.Record{Type: model.TypeMemory, Timestamp: 4}, true},
		{"untimed network", model.Record{Type: model.TypeNetwork}, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := Observed(rec, tc.record); got != tc.want {
				t.Fatalf("Observed=%v want %v", got, tc.want)
			}
		})
	}
}

func TestForcedLayoutEvidenceExcludesPausedRecords(t *testing.T) {
	rec := testgen.Empty()
	rec.Discontinuities = []model.Discontinuity{{StartTime: 2, EndTime: 4}}
	rec.Records = []model.Record{
		{Type: model.TypeScript, EventType: "timer-fired", StartTime: 3, EndTime: 3.1},
		{Type: model.TypeLayout, EventType: "layout", StartTime: 3.01, EndTime: 3.02},
		{Type: model.TypeLayout, EventType: "forced-layout", StartTime: 3.03, EndTime: 3.04},
		{Type: model.TypeLayout, EventType: "forced-layout", StartTime: 4.1, EndTime: 4.2},
	}
	got := ForcedLayoutRecords(rec)
	if len(got) != 1 || got[0].StartTime != 4.1 {
		t.Fatalf("paused forced-layout evidence retained: %+v", got)
	}
}
