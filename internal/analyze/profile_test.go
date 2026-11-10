package analyze

import (
	"bytes"
	"encoding/json"
	"testing"

	"github.com/omniviewdev/wkperf/internal/flame"
	"github.com/omniviewdev/wkperf/internal/model"
	"github.com/omniviewdev/wkperf/internal/profile"
	"github.com/omniviewdev/wkperf/internal/testgen"
)

func TestLeafFirstWeightedCallersAndStacks(t *testing.T) {
	root := model.StackFrame{Name: "root", URL: "bundle.js", Line: 1, Column: 1}
	a := model.StackFrame{Name: "a", URL: "bundle.js", Line: 1, Column: 20}
	b := model.StackFrame{Name: "a", URL: "bundle.js", Line: 1, Column: 40}
	rec := &model.Recording{StartTime: 0, EndTime: 1, Samples: []model.TargetSamples{{StackTraces: []model.StackTrace{
		{Timestamp: .1, StackFrames: []model.StackFrame{a, a, root}},
		{Timestamp: .2, StackFrames: []model.StackFrame{b, root}},
		{Timestamp: .3, StackFrames: []model.StackFrame{a, a, root}},
	}, Durations: []float64{.002, .003, .004}}}}
	s := Summarize(rec, "x")
	if len(s.Functions) != 3 || s.Functions[0].Column != 20 || !approx(s.Functions[0].SelfMs, 6) || !approx(s.Functions[0].TotalMs, 6) {
		t.Fatalf("identity/self/recursion: %+v", s.Functions)
	}
	if s.InclusiveFunctions[0].Name != "root" || !approx(s.InclusiveFunctions[0].TotalMs, 9) {
		t.Fatalf("inclusive ranking: %+v", s.InclusiveFunctions)
	}
	var found bool
	for _, edge := range s.Functions[0].Callers {
		if edge.Function.Name == "root" && approx(edge.WeightMs, 6) && edge.Samples == 2 {
			found = true
		}
	}
	if !found || len(s.CommonStacks) != 2 || s.CommonStacks[0].Samples != 2 || len(s.CommonStacks[0].Frames) != 3 || s.CommonStacks[0].Frames[0].Name != "root" {
		t.Fatalf("weighted callers/full stacks: %+v %+v", s.Functions[0], s.CommonStacks)
	}
}

func TestSummaryPausesAndIdleWakeups(t *testing.T) {
	rec := &model.Recording{StartTime: 0, EndTime: 4, Discontinuities: []model.Discontinuity{{StartTime: 1, EndTime: 3}}, Records: []model.Record{
		frame(0, 1), frame(1, 2), frame(3, .016),
		{Type: model.TypeScript, EventType: "timer-fired", StartTime: .99, EndTime: .995},
		{Type: model.TypeScript, EventType: "timer-fired", StartTime: 2, EndTime: 2.5},
		{Type: model.TypeScript, EventType: "timer-fired", StartTime: 3, EndTime: 3.004},
	}}
	s := Summarize(rec, "x")
	if !approx(s.DurationSec, 2) || !approx(val(t, s, "script.total_ms"), 9) || val(t, s, "frames.count") != 1 || val(t, s, "frames.idle_wakeups") != 1 {
		t.Fatalf("paused/idle statistics: %+v", s)
	}
	if !approx(val(t, s, "frames.p95_ms"), 16) || !approx(val(t, s, "frames.fps"), 62.5) || val(t, s, "frames.jank_16ms") != 0 {
		t.Fatal("idle gap inflated frame metrics")
	}
}

func TestDeepStackKeepsAllFrames(t *testing.T) {
	frames := make([]model.StackFrame, 366)
	for i := range frames {
		frames[i] = model.StackFrame{Name: "a", URL: "bundle.js", Line: 1, Column: i + 1}
	}
	rec := &model.Recording{Samples: []model.TargetSamples{{StackTraces: []model.StackTrace{{StackFrames: frames}}, Durations: []float64{.007}}}}
	s := Summarize(rec, "x")
	if len(s.Functions) != 366 || len(s.CommonStacks) != 1 || len(s.CommonStacks[0].Frames) != 366 {
		t.Fatal("deep stack lost frames")
	}
	for _, f := range s.Functions {
		if !approx(f.TotalMs, 7) || len(f.Callers) > 1 || len(f.Callees) > 1 {
			t.Fatalf("not adjacent weighted edges: %+v", f)
		}
	}
}

func TestSharedTreeIntervalsAndMostFrequentStack(t *testing.T) {
	rec := testgen.Empty()
	rec.Samples = []model.TargetSamples{{StackTraces: []model.StackTrace{
		{Timestamp: 1, StackFrames: []model.StackFrame{{Name: "common", URL: "app.js", Line: 1, Column: 1}}},
		{Timestamp: 2, StackFrames: []model.StackFrame{{Name: "common", URL: "app.js", Line: 1, Column: 1}}},
		{Timestamp: 3, StackFrames: []model.StackFrame{{Name: "heavy", URL: "app.js", Line: 1, Column: 50}}},
	}, Durations: []float64{.002, .003, .020}}}
	tree := flame.Build(rec)
	s := SummarizeWithTree(rec, "x", tree)
	if len(s.CommonStacks) != 2 || s.CommonStacks[0].Frames[0].Name != "common" || s.CommonStacks[0].Samples != 2 {
		t.Fatalf("common stack ranked by weight, not frequency: %+v", s.CommonStacks)
	}
	functions := FunctionsInIntervalsWithTree(rec, tree, []profile.Interval{{Start: 1, End: 3}})
	if len(functions) != 1 || functions[0].Name != "common" || !approx(functions[0].SelfMs, 5) {
		t.Fatalf("shared tree interval view: %+v", functions)
	}
}

func TestSummaryDeterministicAcrossPairedSampleShuffle(t *testing.T) {
	a, b := testgen.DeepMinified(), testgen.DeepMinified()
	for _, rec := range []*model.Recording{a, b} {
		for i := range rec.Samples[0].Durations {
			rec.Samples[0].Durations[i] = []float64{.0001, .001, .1}[i%3]
		}
	}
	target := &b.Samples[0]
	for i := range len(target.StackTraces) / 2 {
		j := len(target.StackTraces) - 1 - i
		target.StackTraces[i], target.StackTraces[j] = target.StackTraces[j], target.StackTraces[i]
		target.Durations[i], target.Durations[j] = target.Durations[j], target.Durations[i]
	}
	left, err := json.Marshal(Summarize(a, "fixture"))
	if err != nil {
		t.Fatal(err)
	}
	right, err := json.Marshal(Summarize(b, "fixture"))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(left, right) {
		t.Fatal("summary changed after paired sample shuffle")
	}
}


func TestEachHotspotHasStableIDAndFrequentContainingStack(t *testing.T) {
	rec := testgen.GetAnimations()
	target := &rec.Samples[0]
	repeated := target.StackTraces[0]
	repeated.Timestamp = 1.02
	target.StackTraces = append(target.StackTraces, repeated)
	target.Durations = append(target.Durations, .001)
	target.Durations[2] = .050
	tree := flame.Build(rec)
	s := SummarizeWithTree(rec, "fixture", tree)
	ids := map[string]string{}
	for _, f := range tree.Functions() {
		ids[f.Frame.Name] = f.ID
	}
	for _, f := range s.Functions {
		if f.ID == "" || f.ID != ids[f.Name] || len(f.CommonStack) == 0 {
			t.Fatalf("hotspot lacks stable ID or full common stack: %+v", f)
		}
		if len(f.CommonStack) != 4 || f.CommonStack[0].Name != "onResize" || f.CommonStack[3].Name != "getAnimations" {
			t.Fatalf("hotspot common stack used heaviest weight instead of most frequent containing path: %+v", f)
		}
		for _, frame := range f.CommonStack {
			if frame.ID != ids[frame.Name] {
				t.Fatalf("common-stack frame ID differs from shared tree: %+v", frame)
			}
		}
		for _, edge := range f.Callers {
			if edge.Function.ID != ids[edge.Function.Name] {
				t.Fatalf("caller cannot select stable function ID: %+v", edge)
			}
		}
	}
	data, err := json.Marshal(s.Functions)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(data, []byte(`"commonStack":[`)) || !bytes.Contains(data, []byte(`"id":`)) {
		t.Fatalf("hotspot JSON lacks identity/stack fields: %s", data)
	}
}

func TestCommonStackIDMatchesCanonicalPath(t *testing.T) {
	rec := testgen.Periodic()
	tree := flame.Build(rec)
	s := SummarizeWithTree(rec, "fixture", tree)
	want := tree.Paths()[0].ID
	if len(s.CommonStacks) != 1 || s.CommonStacks[0].ID != want {
		t.Fatalf("global stack ID differs from shared path: %+v", s.CommonStacks)
	}
	for _, f := range s.Functions {
		if f.CommonStackID != want {
			t.Fatalf("hotspot lacks exact common-stack query selector: %+v", f)
		}
	}
}

func TestUntimedWholeRecordingUsesNanosecondTolerance(t *testing.T) {
	rec := &model.Recording{StartTime: 2.611872458306607, EndTime: 20.24081204162212, Samples: []model.TargetSamples{{
		StackTraces: []model.StackTrace{
			{Timestamp: 0, StackFrames: []model.StackFrame{{Name: "untimed", URL: "app.js"}}},
			{Timestamp: 3, StackFrames: []model.StackFrame{{Name: "timed", URL: "app.js"}}},
		},
		Durations: []float64{0.025, 0.04},
	}}}
	s := SummarizeWith(rec, "decimal.json", Options{ToSec: 17.628939583})
	if s.Fixture.SampleCount != 2 || s.Fixture.Window != "" || len(s.Functions) != 2 {
		t.Fatalf("rounded whole bounds excluded unknown clocks: %+v %+v", s.Fixture, s.Functions)
	}
}
