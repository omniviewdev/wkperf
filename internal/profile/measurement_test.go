package profile

import (
	"bytes"
	"encoding/json"
	"math"
	"math/rand/v2"
	"reflect"
	"testing"

	"github.com/omniviewdev/wkperf/internal/model"
	"github.com/omniviewdev/wkperf/internal/testgen"
)

func TestNestedRenderingUsesInnermostRecord(t *testing.T) {
	rec := testgen.NestedRecords()
	work := Measure(rec, rec.StartTime, rec.EndTime)
	if !closeTo(work.PaintMs, 6) || !closeTo(work.StyleMs, 2) || !closeTo(work.CompositeMs, 2) || !closeTo(work.ScriptMs, 456) {
		t.Fatalf("nested rendering ownership: %+v", work)
	}
}

func TestCollectorIntervalsDoNotOwnMainThreadWork(t *testing.T) {
	rec := testgen.GCOverlay()
	work := Measure(rec, rec.StartTime, rec.EndTime)
	if !closeTo(work.GCMs, 131.6) || !closeTo(work.WorkMs, 7) || !closeTo(work.ScriptMs, 7) || !closeTo(work.GCOverlapWithWorkMs, 7) {
		t.Fatalf("collector interval counted as main-thread work: %+v", work)
	}
	frames := Frames(rec, rec.StartTime, rec.EndTime)
	if len(frames) != 1 || frames[0].Class != FrameIdleGap {
		t.Fatalf("collector interval created an active frame: %+v", frames)
	}
	rec.Records = rec.Records[:2]
	rec.InstrumentTypes = nil
	work = Measure(rec, rec.StartTime, rec.EndTime)
	frames = Frames(rec, rec.StartTime, rec.EndTime)
	if work.WorkMs != 0 || work.GCOverlapWithWorkMs != 0 || len(frames) != 1 || frames[0].Class != FrameIdleGap || len(Tasks(rec)) != 0 {
		t.Fatalf("GC-only capture manufactured main-thread work: %+v %+v", work, frames)
	}
}

func TestInputStallAndActiveFrameClasses(t *testing.T) {
	rec := testgen.InputStallFrames()
	frames := Frames(rec, rec.StartTime, rec.EndTime)
	if len(frames) != 2 {
		t.Fatalf("frames: %+v", frames)
	}
	if frames[0].Class != FrameInputStall || frames[1].Class != FrameActive {
		t.Fatalf("input frame classification: %+v", frames)
	}
	if !closeTo(frames[0].Occupancy.WorkMs, .35) || !closeTo(frames[1].Occupancy.WorkMs, 200) {
		t.Fatalf("input frame work: %+v", frames)
	}
}

func TestOwnershipUsesOriginalRecordBounds(t *testing.T) {
	rec := &model.Recording{EndTime: .03, Records: []model.Record{
		{Type: model.TypeLayout, EventType: "composite", StartTime: 0, EndTime: .01},
		{Type: model.TypeScript, EventType: "timer-fired", StartTime: .002, EndTime: .02},
	}}
	work := Measure(rec, .005, .009)
	if !closeTo(work.ScriptMs, 4) || work.CompositeMs != 0 {
		t.Fatalf("clipped bounds replaced original start ordering: %+v", work)
	}
	clipped := Clip(rec, .005, .009)
	work = Measure(clipped, clipped.StartTime, clipped.EndTime)
	if !closeTo(work.ScriptMs, 4) || work.CompositeMs != 0 {
		t.Fatalf("Clip lost original record ownership: %+v", work)
	}
	nested := Clip(clipped, .006, .008)
	costs := ExclusiveRecords(nested, nested.StartTime, nested.EndTime)
	if len(costs) != 1 || !closeTo(costs[0].ExclusiveMs, 2) || costs[0].Record.StartTime != .002 || costs[0].Record.EndTime != .02 {
		t.Fatalf("repeated clipping lost original owner bounds: %+v", costs)
	}
}

func TestTasksMergeOverlapButNotTouchingOrCollectorIntervals(t *testing.T) {
	rec := &model.Recording{EndTime: 1, Records: []model.Record{
		{Type: model.TypeScript, EventType: "timer-fired", StartTime: 0, EndTime: .25},
		{Type: model.TypeScript, EventType: EventMicrotaskDispatched, StartTime: .125, EndTime: .375},
		{Type: model.TypeScript, EventType: EventObserverCallback, StartTime: .375, EndTime: .5},
		{Type: model.TypeScript, EventType: EventMicrotaskDispatched, StartTime: .75, EndTime: .875},
		{Type: model.TypeScript, EventType: "garbage-collected", StartTime: 0, EndTime: 1},
		{Type: model.TypeScript, EventType: "timer-installed", StartTime: 0, EndTime: 1},
	}}
	tasks := Tasks(rec)
	if len(tasks) != 3 || tasks[0].StartTime != 0 || tasks[0].EndTime != .375 || tasks[0].EventType != "timer-fired" {
		t.Fatalf("maximal overlapping tasks: %+v", tasks)
	}
	if !reflect.DeepEqual(tasks[0].Children, []string{EventMicrotaskDispatched}) || tasks[1].StartTime != .375 || tasks[2].EventType != EventMicrotaskDispatched {
		t.Fatalf("touching/microtask-only task or member labels lost: %+v", tasks)
	}
}

func TestTaskLabelsUseLongestOriginalMember(t *testing.T) {
	rec := &model.Recording{EndTime: 1, Records: []model.Record{
		{Type: model.TypeScript, EventType: "timer-fired", StartTime: 0, EndTime: .125},
		{Type: model.TypeScript, EventType: EventMicrotaskDispatched, StartTime: .0625, EndTime: .25},
	}}
	tasks := Tasks(Clip(rec, .1, .12))
	if len(tasks) != 1 || tasks[0].EventType != EventMicrotaskDispatched || !reflect.DeepEqual(tasks[0].Children, []string{"timer-fired"}) {
		t.Fatalf("window clipping changed longest original label: %+v", tasks)
	}
}

func TestTaskUnionAndGCOverlayRespectCapturePauses(t *testing.T) {
	rec := &model.Recording{EndTime: 1, Discontinuities: []model.Discontinuity{{StartTime: .4, EndTime: .6}}, Records: []model.Record{
		{Type: model.TypeScript, EventType: "timer-fired", StartTime: .05, EndTime: .95},
		{Type: model.TypeScript, EventType: "garbage-collected", StartTime: .1, EndTime: .5},
		{Type: model.TypeScript, EventType: "garbage-collected", StartTime: .3, EndTime: .8},
		{Type: model.TypeScript, EventType: "garbage-collected", StartTime: .75, EndTime: .9},
	}}
	work := Measure(rec, 0, 1)
	if !closeTo(work.WorkMs, 700) || !closeTo(work.GCMs, 600) || !closeTo(work.GCOverlapWithWorkMs, 600) {
		t.Fatalf("collector union/pause/overlap: %+v", work)
	}
	tasks := Tasks(rec)
	if len(tasks) != 2 || !closeTo(tasks[0].Duration()*1000, 350) || !closeTo(tasks[1].Duration()*1000, 350) {
		t.Fatalf("capture pause joined task work: %+v", tasks)
	}
}

func TestExclusiveOwnershipDurationAndKindTies(t *testing.T) {
	for _, tc := range []struct {
		name              string
		records           []model.Record
		script, composite float64
	}{
		{"shortest duration before kind", []model.Record{
			{Type: model.TypeLayout, EventType: "composite", EndTime: .01},
			{Type: model.TypeScript, EventType: "timer-fired", EndTime: .006},
		}, 6, 4},
		{"kind tie after identical original bounds", []model.Record{
			{Type: model.TypeLayout, EventType: "paint", EndTime: .01},
			{Type: model.TypeLayout, EventType: "composite", EndTime: .01},
		}, 0, 10},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rec := &model.Recording{EndTime: .01, Records: tc.records}
			work := Measure(rec, 0, .01)
			if !closeTo(work.ScriptMs, tc.script) || !closeTo(work.CompositeMs, tc.composite) {
				t.Fatalf("ownership tie: %+v", work)
			}
		})
	}
}

func TestInputAtPreviousFrameEndDoesNotLeakIntoNextFrame(t *testing.T) {
	rec := &model.Recording{EndTime: 2, Records: []model.Record{
		{Type: model.TypeRenderingFrame, StartTime: 1, EndTime: 2},
		{Type: model.TypeScript, EventType: "event-dispatched", Details: json.RawMessage(`"wheel"`), StartTime: .99, EndTime: 1},
	}}
	frames := Frames(rec, 0, 2)
	if len(frames) != 1 || frames[0].Class != FrameIdleGap || frames[0].DominantTrigger != "" {
		t.Fatalf("half-open input boundary: %+v", frames)
	}
}

func TestSharedMeasurementDeterministicAcrossRecordShuffle(t *testing.T) {
	rec := testgen.ThresholdFrames()
	rec.Records = append(rec.Records,
		model.Record{Type: model.TypeLayout, EventType: "paint", StartTime: 101, EndTime: 101.01},
		model.Record{Type: model.TypeScript, EventType: "script-evaluated", StartTime: 101.4, EndTime: 101.5, Details: json.RawMessage(`"b"`)},
		model.Record{Type: model.TypeScript, EventType: "script-evaluated", StartTime: 101.4, EndTime: 101.5, Details: json.RawMessage(`"a"`)},
		model.Record{Type: model.TypeScript, EventType: "garbage-collected", StartTime: 100.1, EndTime: 100.3},
	)
	snapshot := func() []byte {
		t.Helper()
		work, costs := MeasureRecords(rec, rec.StartTime, rec.EndTime)
		data, err := json.Marshal(struct {
			Work   Occupancy
			Costs  []RecordCost
			Tasks  []Task
			Frames []Frame
		}{work, costs, Tasks(rec), Frames(rec, rec.StartTime, rec.EndTime)})
		if err != nil {
			t.Fatal(err)
		}
		return data
	}
	want := snapshot()
	random := rand.New(rand.NewPCG(10, 11))
	for range 20 {
		random.Shuffle(len(rec.Records), func(i, j int) { rec.Records[i], rec.Records[j] = rec.Records[j], rec.Records[i] })
		if got := snapshot(); !bytes.Equal(got, want) {
			t.Fatalf("shuffle changed shared measurement:\n%s\n%s", want, got)
		}
	}
}

func TestWorkIndexMatchesWindowSweeps(t *testing.T) {
	rec := &model.Recording{
		StartTime: 100, EndTime: 101,
		Discontinuities: []model.Discontinuity{
			{StartTime: 100.44, EndTime: 100.53},
			{StartTime: 100.14, EndTime: 100.18},
			{StartTime: 100.51, EndTime: 100.62},
			{StartTime: 100.48, EndTime: 100.49},
			{StartTime: 100.62, EndTime: 100.65},
			{StartTime: math.Inf(-1), EndTime: 100.01},
			{StartTime: 100.94, EndTime: math.Inf(1)},
			{StartTime: 100.90, EndTime: 100.89},
			{StartTime: math.NaN(), EndTime: 100.92},
		},
		Records: []model.Record{
			{Type: model.TypeScript, EventType: "timer-fired", StartTime: 99.9, EndTime: 101.1},
			{Type: model.TypeScript, EventType: "event-dispatched", StartTime: 100.11, EndTime: 100.44},
			{Type: model.TypeScript, EventType: "timer-installed", StartTime: 100.13, EndTime: 100.41},
			{Type: model.TypeLayout, EventType: "paint", StartTime: 100.12, EndTime: 100.38},
			{Type: model.TypeLayout, EventType: "layout", StartTime: 100.28, EndTime: 100.38},
			{Type: model.TypeLayout, EventType: "recalculate-styles", StartTime: 100.35, EndTime: 100.58},
			{Type: model.TypeLayout, EventType: "paint", StartTime: 100.67, EndTime: 100.81},
			{Type: model.TypeLayout, EventType: "layout", StartTime: 100.72, EndTime: 100.85},
			{Type: model.TypeScript, EventType: "garbage-collected", StartTime: 100.09, EndTime: 100.34},
			{Type: model.TypeScript, Details: json.RawMessage(`{"__type":"GarbageCollection"}`), StartTime: 100.33, EndTime: 100.84},
			{Type: model.TypeScript, EventType: "garbage-collected", StartTime: 100.82, EndTime: 100.90},
			{Type: model.TypeScript, EventType: "garbage-collected", StartTime: 100.92, EndTime: math.Inf(1)},
			{Type: model.TypeScript, StartTime: math.NaN(), EndTime: 100.93},
			{Type: model.TypeRenderingFrame, StartTime: 100.04, EndTime: 100.13},
			{Type: model.TypeRenderingFrame, StartTime: 100.13, EndTime: 100.40},
			{Type: model.TypeRenderingFrame, StartTime: 100.23, EndTime: 100.40},
			{Type: model.TypeRenderingFrame, StartTime: 100.66, EndTime: 100.75},
			{Type: model.TypeRenderingFrame, StartTime: 100.75, EndTime: 100.94},
			{Type: model.TypeRenderingFrame, StartTime: 100.77, EndTime: math.Inf(1)},
		},
	}
	check := func(rec *model.Recording) {
		t.Helper()
		index := NewWorkIndex(rec, rec.StartTime, rec.EndTime)
		windows := [][2]float64{
			{99, 102}, {100, 101}, {100.14, 100.18}, {100.18, 100.44}, {100.43, 100.66},
			{100.51, 100.60}, {100.65, 100.94}, {100.7, 100.7}, {100.9, 100.8},
			{math.Inf(-1), 100.8}, {100.2, math.Inf(1)}, {math.NaN(), 100.8}, {100.2, math.NaN()},
		}
		random := rand.New(rand.NewPCG(20, 21))
		for range 100 {
			lo, hi := 99.9+random.Float64()*1.2, 99.9+random.Float64()*1.2
			windows = append(windows, [2]float64{min(lo, hi), max(lo, hi)})
		}
		for _, bounds := range windows {
			lo, hi := bounds[0], bounds[1]
			wantWork, wantCosts := MeasureRecords(rec, lo, hi)
			gotWork, gotCosts := index.MeasureRecords(lo, hi)
			if gotWork != wantWork || index.Measure(lo, hi) != wantWork || !reflect.DeepEqual(gotCosts, wantCosts) {
				t.Fatalf("[%g,%g]: work %+v (%+v); costs %+v (%+v)", lo, hi, gotWork, wantWork, gotCosts, wantCosts)
			}
			if got, want := index.ActiveDuration(lo, hi), ActiveDuration(rec, lo, hi); got != want {
				t.Fatalf("[%g,%g]: active duration %g, want %g", lo, hi, got, want)
			}
			if !math.IsNaN(lo) && !math.IsNaN(hi) {
				if got, want := index.Frames(lo, hi), Frames(rec, lo, hi); !reflect.DeepEqual(got, want) {
					t.Fatalf("[%g,%g]: frames %+v, want %+v", lo, hi, got, want)
				}
			}
		}
	}
	check(rec)
	check(Clip(rec, 100.08, 100.91))
}

func TestWorkIndexRetainsOriginalSweepTieBehavior(t *testing.T) {
	rec := &model.Recording{
		EndTime: 2,
		Discontinuities: []model.Discontinuity{
			{StartTime: 1.5, EndTime: 1.52},
		},
		Records: []model.Record{
			{Type: model.TypeScript, StartTime: .2, EndTime: .65, OriginalEndTime: 1, Timestamp: 1},
			{Type: model.TypeScript, StartTime: .1, EndTime: .9, OriginalEndTime: 1, Timestamp: 2},
			{Type: model.TypeScript, StartTime: .93, EndTime: 1.13},
			{Type: model.TypeScript, StartTime: 1.4, EndTime: 1.7, OriginalStartTime: 1.17, OriginalEndTime: 2},
			{Type: model.TypeScript, StartTime: 1.2, EndTime: 1.8, OriginalStartTime: 1.17, OriginalEndTime: 2},
			{Type: model.TypeScript, EventType: "garbage-collected", StartTime: .05, EndTime: 1.8},
		},
	}
	index := NewWorkIndex(rec, 0, 2)
	want := ExclusiveRecords(rec, .3, .8)
	if len(want) != 2 || want[0].Record.Timestamp != 1 || want[1].Record.Timestamp != 2 {
		t.Fatalf("fixture did not produce two distinct tied owners: %+v", want)
	}
	if got := index.ExclusiveRecords(.3, .8); !reflect.DeepEqual(got, want) {
		t.Fatalf("indexed costs changed tied ownership: %+v, want %+v", got, want)
	}
	wantWork := Measure(rec, .3, .8)
	if uncut := occupancy(index.spans, index.collections, index.active, .3, .8); uncut.WorkMs == wantWork.WorkMs {
		t.Fatal("fixture did not expose changed rounding from a tie split")
	}
	if got := index.Measure(.3, .8); got != wantWork {
		t.Fatalf("indexed occupancy changed tie rounding: %+v, want %+v", got, wantWork)
	}

	for _, window := range []Interval{{.65, .8}, {1, 1.1}, {1.5, 1.52}} {
		want := Measure(rec, window.Start, window.End)
		var got Occupancy
		allocs := testing.AllocsPerRun(20, func() { got = index.Measure(window.Start, window.End) })
		if got != want || allocs != 0 {
			t.Fatalf("disjoint window %+v: occupancy %+v (want %+v), allocations %g (want 0)", window, got, want, allocs)
		}
	}
}
