package profile

import (
	"testing"

	"github.com/omniviewdev/wkperf/internal/model"
	"github.com/omniviewdev/wkperf/internal/testgen"
)

func compositeMs(startSec, ms float64) model.Record {
	return model.Record{Type: model.TypeLayout, EventType: EventComposite, StartTime: startSec, EndTime: startSec + ms/1000}
}

func TestStalledCompositeThreshold(t *testing.T) {
	for _, tc := range []struct {
		name    string
		record  model.Record
		stalled bool
	}{
		{"49 ms is main-thread work", compositeMs(10, 49), false},
		{"exactly 50 ms is work despite timestamp rounding", model.Record{Type: model.TypeLayout, EventType: EventComposite, StartTime: .35, EndTime: .4}, false},
		{"51 ms is stalled", compositeMs(10, 51), true},
		{"a span of seconds is stalled", compositeMs(1, 12534.53), true},
		{"a zero-length composite is work", compositeMs(5, 0), false},
		{"a long paint is not a composite", model.Record{Type: model.TypeLayout, EventType: "paint", EndTime: 13}, false},
		{"a long script event is not a layout composite", model.Record{Type: model.TypeScript, EventType: EventComposite, EndTime: 13}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := IsStalledComposite(tc.record); got != tc.stalled {
				t.Fatalf("IsStalledComposite = %v, want %v", got, tc.stalled)
			}
		})
	}
}

func TestStalledCompositesOwnNoMainThreadWork(t *testing.T) {
	rec := testgen.StalledComposites()
	work := Measure(rec, rec.StartTime, rec.EndTime)
	if !closeTo(work.CompositeMs, 55) || !closeTo(work.ScriptMs, 100) || !closeTo(work.WorkMs, 155) {
		t.Fatalf("stalled composites contributed work: %+v", work)
	}
	costs := ExclusiveRecords(rec, rec.StartTime, rec.EndTime)
	for _, cost := range costs {
		if IsStalledComposite(cost.Record) {
			t.Errorf("stalled composite owns exclusive work: %+v", cost)
		}
	}
	if len(costs) != 3 {
		t.Fatalf("exclusive owners = %d, want the timer and both short composites: %+v", len(costs), costs)
	}
}

func TestStalledCompositeKeepsOriginalBoundsWhenClipped(t *testing.T) {
	rec := testgen.StalledComposites()
	clipped := Clip(rec, 5, 5.04)
	var seen bool
	for _, r := range clipped.Records {
		if r.EventType != EventComposite {
			continue
		}
		seen = true
		if r.EndTime-r.StartTime >= CompositeStallThresholdMs/1000 {
			t.Fatalf("fixture must clip the stalled composite below the threshold: %+v", r)
		}
		if !IsStalledComposite(r) {
			t.Errorf("clipping turned a stalled composite into short work: %+v", r)
		}
	}
	if !seen {
		t.Fatal("clip dropped the stalled composite overlapping the window")
	}
	if work := Measure(clipped, clipped.StartTime, clipped.EndTime); work.CompositeMs != 0 || work.WorkMs != 0 {
		t.Fatalf("clipped stalled composite counted as work: %+v", work)
	}
}

func TestStalledCompositeDoesNotMakeAFrameActive(t *testing.T) {
	frameRecord := model.Record{Type: model.TypeRenderingFrame, StartTime: 0, EndTime: 1.2}
	for _, tc := range []struct {
		name      string
		composite model.Record
		class     FrameClass
		workMs    float64
	}{
		{"stalled", compositeMs(0, 1200), FrameIdleGap, 0},
		{"short", compositeMs(0, 49), FrameIdleGap, 49},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rec := &model.Recording{EndTime: 2, Records: []model.Record{frameRecord, tc.composite}}
			frames := Frames(rec, 0, 2)
			if len(frames) != 1 || frames[0].Class != tc.class || !closeTo(frames[0].Occupancy.WorkMs, tc.workMs) {
				t.Fatalf("frame attribution: %+v", frames)
			}
		})
	}
	// A 49 ms composite is real work: with enough of it the frame is active.
	rec := &model.Recording{EndTime: 2, Records: []model.Record{
		{Type: model.TypeRenderingFrame, StartTime: 0, EndTime: .1},
		compositeMs(0, 49),
		compositeMs(.05, 49),
	}}
	frames := Frames(rec, 0, 2)
	if len(frames) != 1 || frames[0].Class != FrameActive || !closeTo(frames[0].Occupancy.CompositeMs, 98) {
		t.Fatalf("short composites must remain frame work: %+v", frames)
	}
}
