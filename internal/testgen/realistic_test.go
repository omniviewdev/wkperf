package testgen

import (
	"bytes"
	"math"
	"testing"

	"github.com/omniviewdev/wkperf/internal/model"
)

func TestWrappedEmissionAndRealisticFixtures(t *testing.T) {
	for _, rec := range []*model.Recording{IdleMinified(), DeepMinified(), GetAnimations(), React(), Periodic(), PausedMinified(), UntimedSamples(), DecimalEnd()} {
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
