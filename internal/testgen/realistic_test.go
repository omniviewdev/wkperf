package testgen

import (
	"bytes"
	"math"
	"testing"

	"github.com/omniviewdev/wkperf/internal/model"
)

func TestWrappedEmissionAndRealisticFixtures(t *testing.T) {
	for _, rec := range []*model.Recording{IdleMinified(), DeepMinified(), GetAnimations(), React(), ReactOwnership(), Periodic(), PausedMinified(), UntimedSamples(), DecimalEnd(), NestedRecords(), InputStallFrames(), GCOverlay(), ThresholdFrames(), ExclusiveFindingCosts()} {
		var out bytes.Buffer
		if err := Write(&out, rec, true); err != nil {
			t.Fatal(err)
		}
		if !bytes.Contains(out.Bytes(), []byte(`"version":1`)) || !bytes.Contains(out.Bytes(), []byte(`"recording":`)) {
			t.Fatalf("not wrapped: %s", out.Bytes())
		}
		got, err := model.Parse(&out)
		if err != nil || len(got.Records) != len(rec.Records) {
			t.Fatalf("round trip: %v", err)
		}
	}
	rec := DeepMinified()
	maxDepth := 0
	for _, st := range rec.Samples[0].StackTraces {
		if len(st.StackFrames) > maxDepth {
			maxDepth = len(st.StackFrames)
		}
	}
	if got := MinifiedSourceMap(); len(got) == 0 {
		t.Fatal("missing reusable source-map fixture")
	}
	if maxDepth != 366 {
		t.Fatalf("deep profile max depth = %d", maxDepth)
	}
}

func TestUntimedSamplesFixture(t *testing.T) {
	rec := UntimedSamples()
	if rec.StartTime != 10 || rec.EndTime != 12 || len(rec.Samples) != 1 {
		t.Fatalf("fixture bounds or targets: %+v", rec)
	}
	target := rec.Samples[0]
	if len(target.StackTraces) != 50 || len(target.Durations) != 50 {
		t.Fatal("fixture must have 50 weighted samples")
	}
	for i, sample := range target.StackTraces {
		leaf, timestamp, weight := "sharedLeaf", 0.0, .001
		if i < 25 {
			leaf = "untimedLeaf"
		}
		if i >= 40 {
			timestamp, weight = 11+float64(i-40)*.004, .004
		}
		if len(sample.StackFrames) != 2 || sample.StackFrames[0].Name != leaf || sample.StackFrames[1].Name != "appRoot" || sample.Timestamp != timestamp || target.Durations[i] != weight {
			t.Fatalf("sample %d: %+v, weight %g", i, sample, target.Durations[i])
		}
	}
	var scriptMs float64
	for _, record := range rec.Records {
		scriptMs += record.Duration() * 1000
	}
	if math.Abs(scriptMs-140) > 1e-9 {
		t.Fatalf("script ms = %g, want 140", scriptMs)
	}
}

func TestReactFixtureCapturesExternalClickAndRenderPhase(t *testing.T) {
	rec := React()
	var click *model.Record
	for i := range rec.Records {
		record := &rec.Records[i]
		if record.EventType == "event-dispatched" && string(record.Details) == `"click"` {
			click = record
		}
	}
	if click == nil {
		t.Fatal("React trigger fixture needs an actual click, not only its scheduling microtask")
	}
	for _, sample := range rec.Samples[0].StackTraces[:2] {
		if sample.Timestamp < click.StartTime || sample.Timestamp >= click.EndTime {
			t.Fatal("click does not cover render and commit evidence")
		}
	}
	if rec.Samples[0].StackTraces[2].Timestamp < click.EndTime {
		t.Fatal("outside sync sample lost its unidentified trigger")
	}
	hasRenderRoot := false
	for _, frame := range rec.Samples[0].StackTraces[0].StackFrames {
		hasRenderRoot = hasRenderRoot || frame.Name == "renderRootSync"
	}
	if !hasRenderRoot {
		t.Fatal("render phase must be identified by its phase ancestor")
	}
}

func TestPeriodicFixtureHasSixIndependentFires(t *testing.T) {
	rec := Periodic()
	fires := 0
	first, last := math.Inf(1), math.Inf(-1)
	for _, record := range rec.Records {
		if record.EventType == "timer-fired" {
			fires++
			first = min(first, record.StartTime)
			last = max(last, record.StartTime)
		}
	}
	if fires != 6 || last-first < 2 {
		t.Fatalf("periodic evidence = %d fires over %gs, want six across at least 2s", fires, last-first)
	}
	samples := rec.Samples[0]
	if len(samples.StackTraces) != 18 || len(samples.Durations) != 18 {
		t.Fatal("periodic fixture must retain three samples per fire")
	}
	for _, duration := range samples.Durations {
		if duration != .002 {
			t.Fatal("periodic sample cost changed")
		}
	}
}

func TestReactOwnershipFixturePreservesIndependentEvidence(t *testing.T) {
	rec := ReactOwnership()
	if len(rec.Samples) != 1 || len(rec.Samples[0].StackTraces) != 7 || len(rec.Samples[0].Durations) != 7 {
		t.Fatal("ownership fixture must retain seven independent weighted stacks")
	}
	var total float64
	for i, sample := range rec.Samples[0].StackTraces {
		if len(sample.StackFrames) == 0 || sample.Timestamp <= 0 || sample.Timestamp >= rec.EndTime {
			t.Fatalf("invalid ownership evidence at sample %d", i)
		}
		total += rec.Samples[0].Durations[i]
	}
	if math.Abs(total-.030) > 1e-12 {
		t.Fatalf("ownership weight = %g seconds, want .030", total)
	}
	library := rec.Samples[0].StackTraces[5].StackFrames[0]
	user := rec.Samples[0].StackTraces[6].StackFrames[0]
	if library.Original == nil || user.Original == nil || library.URL != user.URL || library.Original.URL == user.Original.URL {
		t.Fatal("source-map fixture lost shared generated bundle or distinct ownership paths")
	}
}
