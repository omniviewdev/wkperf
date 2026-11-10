package profile

import (
	"bytes"
	"encoding/json"
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
