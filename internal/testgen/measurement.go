package testgen

import (
	"encoding/json"

	"github.com/omniviewdev/wkperf/internal/model"
)

// NestedRecords contains three script tasks with nested microtask checkpoints,
// plus a composite whose paint and style children own most of its work.
func NestedRecords() *model.Recording {
	rec := Empty()
	rec.DisplayName = "Nested task and rendering records"
	rec.StartTime, rec.EndTime = 100, 102
	dispatch := scriptRecord("event-dispatched", 100.4, 100.564)
	dispatch.Details = json.RawMessage(`"scroll"`)
	rec.Records = []model.Record{
		scriptRecord("timer-fired", 100.1, 100.312),
		scriptRecord("microtask-dispatched", 100.102, 100.3116),
		dispatch,
		scriptRecord("microtask-dispatched", 100.401, 100.5631),
		scriptRecord("observer-callback", 100.7, 100.78),
		{Type: model.TypeLayout, EventType: "composite", StartTime: 101, EndTime: 101.010},
		{Type: model.TypeLayout, EventType: "paint", StartTime: 101.001, EndTime: 101.007},
		{Type: model.TypeLayout, EventType: "recalculate-styles", StartTime: 101.0075, EndTime: 101.0095},
	}
	return rec
}

// InputStallFrames separates a 1.2-second input stall with 0.35 ms work from
// a 299.5 ms active interval containing a 200 ms scroll handler.
func InputStallFrames() *model.Recording {
	rec := Empty()
	rec.DisplayName = "Input stall versus main-thread work"
	rec.StartTime, rec.EndTime = 100, 101.5
	wheel := scriptRecord("event-dispatched", 100.1, 100.10005)
	wheel.Details = json.RawMessage(`"wheel"`)
	scroll := scriptRecord("event-dispatched", 101.21, 101.41)
	scroll.Details = json.RawMessage(`"scroll"`)
	rec.Records = []model.Record{
		frameRecord(100, 101.2), wheel,
		{Type: model.TypeLayout, EventType: "paint", StartTime: 100.2, EndTime: 100.2003},
		frameRecord(101.2005, 101.5), scroll,
	}
	return rec
}

// GCOverlay contains a collector interval with seven milliseconds of concurrent
// instrumented script. The remaining rendering interval is an idle gap.
func GCOverlay() *model.Recording {
	rec := Empty()
	rec.DisplayName = "Collector interval overlay"
	rec.StartTime, rec.EndTime = 100, 100.853
	rec.Records = []model.Record{
		frameRecord(100, 100.853),
		scriptRecord("garbage-collected", 100.1, 100.2316),
		scriptRecord("timer-fired", 100.15, 100.157),
	}
	return rec
}

// ThresholdFrames has two work-bound active intervals of 350 and 300 ms.
// A 325 ms finding threshold differs from the fixed 16.7 ms frame standards.
func ThresholdFrames() *model.Recording {
	rec := NestedRecords()
	rec.DisplayName = "Configurable findings versus fixed standards"
	rec.Records = append(rec.Records, frameRecord(100, 100.35), frameRecord(100.35, 100.65))
	return rec
}

// ExclusiveFindingCosts separates inclusive rendering records from work owned
// by nested layout and script records.
func ExclusiveFindingCosts() *model.Recording {
	rec := Empty()
	rec.DisplayName = "Exclusive rendering finding costs"
	rec.EndTime = 1
	rec.Records = []model.Record{
		{Type: model.TypeLayout, EventType: "composite", StartTime: .1, EndTime: .5},
		{Type: model.TypeLayout, EventType: "recalculate-styles", StartTime: .11, EndTime: .31},
		{Type: model.TypeLayout, EventType: "layout", StartTime: .15, EndTime: .25},
		{Type: model.TypeLayout, EventType: "paint", StartTime: .32, EndTime: .42},
		scriptRecord("timer-fired", .33, .38),
	}
	return rec
}

func RepeatedMeasurementWorkload(repeats int) *model.Recording {
	rec := NestedRecords()
	input := scriptRecord("event-dispatched", 101.61, 101.61005)
	input.Details = json.RawMessage(`"wheel"`)
	records := append(rec.Records,
		frameRecord(100.09, 100.32),
		frameRecord(101.6, 101.9),
		input,
		scriptRecord("garbage-collected", 100.15, 100.25),
		model.Record{Type: model.TypeLayout, EventType: "layout", StartTime: 101.0015, EndTime: 101.003},
	)
	rec.Records = make([]model.Record, 0, len(records)*max(repeats, 0))
	for i := range repeats {
		for _, original := range records {
			record := original
			record.StartTime += float64(i) * 2
			record.EndTime += float64(i) * 2
			rec.Records = append(rec.Records, record)
		}
	}
	rec.EndTime = rec.StartTime + float64(repeats)*2
	return rec
}
