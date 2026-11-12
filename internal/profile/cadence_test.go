package profile

import (
	"testing"

	"github.com/omniviewdev/wkperf/internal/model"
	"github.com/omniviewdev/wkperf/internal/testgen"
)

// startsAt is a ten-second capture with a 2 ms composite beginning at each time.
func startsAt(secs ...float64) *model.Recording {
	rec := &model.Recording{EndTime: 10}
	for _, s := range secs {
		rec.Records = append(rec.Records, compositeMs(s, 2))
	}
	return rec
}

func TestCompositeCadenceIntervalsAndRuns(t *testing.T) {
	rec := testgen.CompositeCadenceRuns()
	sample := CompositeCadence(rec, rec.StartTime, rec.EndTime)
	if sample.Starts != 23 || sample.Runs != 2 || len(sample.IntervalsMs) != 20 {
		t.Fatalf("starts/runs/intervals = %d/%d/%d, want 23/2/20", sample.Starts, sample.Runs, len(sample.IntervalsMs))
	}
	// The 250 ms gap between runs and the 300 ms gap before the lone start are not intervals.
	want := []float64{16, 16, 16, 16, 16, 18, 18, 18, 30, 60, 16, 16, 16, 16, 16, 18, 18, 30, 40, 90}
	for i, ms := range sample.IntervalsMs {
		if !closeTo(ms, want[i]) {
			t.Errorf("interval %d = %g ms, want %g (%v)", i, ms, want[i], sample.IntervalsMs)
		}
	}
}

func TestCompositeCadenceRunBoundary(t *testing.T) {
	for _, tc := range []struct {
		name      string
		gapSec    float64
		intervals int
	}{
		{"99.9 ms stays in one run", .0999, 1},
		{"exactly 100 ms starts a new run", .1, 0},
		{"250 ms starts a new run", .25, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			sample := CompositeCadence(startsAt(1, 1+tc.gapSec), 0, 10)
			if sample.Starts != 2 || len(sample.IntervalsMs) != tc.intervals || sample.Runs != tc.intervals {
				t.Fatalf("sample = %+v", sample)
			}
		})
	}
}

func TestCompositeCadenceNeedsTwoStartsPerRun(t *testing.T) {
	sample := CompositeCadence(startsAt(1, 2, 3.5, 5), 0, 10)
	if sample.Starts != 4 || sample.Runs != 0 || len(sample.IntervalsMs) != 0 {
		t.Fatalf("isolated starts must not form a run: %+v", sample)
	}
	if sample := CompositeCadence(startsAt(1, 1.03, 1.06, 1.09, 3, 3.02), 0, 10); sample.Runs != 2 || len(sample.IntervalsMs) != 4 {
		t.Fatalf("a run of any length counts once: %+v", sample)
	}
}

func TestCompositeCadenceUsesOnlyCompositeStartsAndKeepsStalledOnes(t *testing.T) {
	rec := &model.Recording{EndTime: 10, Records: []model.Record{
		{Type: model.TypeLayout, EventType: "paint", StartTime: 1, EndTime: 1.002},
		{Type: model.TypeLayout, EventType: "paint", StartTime: 1.02, EndTime: 1.022},
		{Type: model.TypeScript, EventType: EventComposite, StartTime: 1.04, EndTime: 1.042},
		{Type: model.TypeRenderingFrame, StartTime: 1.06, EndTime: 1.07},
	}}
	if sample := CompositeCadence(rec, 0, 10); sample.Starts != 0 {
		t.Fatalf("only layout composite records give cadence starts: %+v", sample)
	}
	stalled := testgen.StalledComposites()
	if sample := CompositeCadence(stalled, stalled.StartTime, stalled.EndTime); sample.Starts != 4 {
		t.Fatalf("stalled composites still start at a known time: %+v", sample)
	}
	if sample := CompositeCadence(nil, 0, 10); sample.Starts != 0 {
		t.Fatalf("nil recording: %+v", sample)
	}
}

func TestCompositeCadenceBreaksRunsAtCapturePauses(t *testing.T) {
	rec := startsAt(.39, .41, .46)
	rec.Discontinuities = []model.Discontinuity{{StartTime: .4, EndTime: .45}}
	sample := CompositeCadence(rec, 0, 10)
	// The start inside the pause was never observed. The two others are 70 ms
	// apart, but a pause between them hides composites, so they share no run.
	if sample.Starts != 2 || sample.Runs != 0 || len(sample.IntervalsMs) != 0 {
		t.Fatalf("pause must end an active run: %+v", sample)
	}
}

func TestCompositeCadenceWindowUsesOriginalStarts(t *testing.T) {
	rec := &model.Recording{EndTime: 10, Records: []model.Record{
		compositeMs(.99, 20),
		compositeMs(1.02, 2),
		compositeMs(1.04, 2),
		compositeMs(1.5, 2),
	}}
	whole := CompositeCadence(rec, 0, 10)
	if whole.Starts != 4 || len(whole.IntervalsMs) != 2 {
		t.Fatalf("whole recording: %+v", whole)
	}
	window := CompositeCadence(rec, 1.0, 1.1)
	if window.Starts != 2 || len(window.IntervalsMs) != 1 || !closeTo(window.IntervalsMs[0], 20) {
		t.Fatalf("a start before the window must not count: %+v", window)
	}
	clipped := Clip(rec, 1.0, 1.1)
	got := CompositeCadence(clipped, clipped.StartTime, clipped.EndTime)
	if got.Starts != window.Starts || len(got.IntervalsMs) != len(window.IntervalsMs) {
		t.Fatalf("clipping invented a start at the window edge: %+v vs %+v", got, window)
	}
}

func TestClipRetainsRenderingFrameProvenance(t *testing.T) {
	rec := &model.Recording{EndTime: 10, Records: []model.Record{
		{Type: model.TypeRenderingFrame, StartTime: 0, EndTime: .016},
		compositeMs(5, 2),
	}}
	if !rec.HasRenderingFrames() {
		t.Fatal("recording with a frame record")
	}
	clipped := Clip(rec, 5, 6)
	if len(clipped.Records) != 1 || !clipped.HasRenderingFrames() {
		t.Fatalf("the clip dropped every frame record but must remember the source had them: %+v", clipped.Records)
	}
	if again := Clip(clipped, 5, 5.5); !again.HasRenderingFrames() {
		t.Fatal("provenance must survive repeated clipping")
	}
	if frameless := Clip(testgen.CompositeCadenceRuns(), 1, 2); frameless.HasRenderingFrames() {
		t.Fatal("a capture without frame records must stay frame-less after clipping")
	}
}
