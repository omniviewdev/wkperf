package flame

import (
	"reflect"
	"testing"

	"github.com/omniviewdev/wkperf/internal/model"
	"github.com/omniviewdev/wkperf/internal/testgen"
)

func doubleTiny() *model.Recording {
	rec := testgen.FlameTiny()
	rec.EndTime += 10
	target := &rec.Samples[0]
	for _, trace := range append([]model.StackTrace(nil), target.StackTraces...) {
		trace.Timestamp += 10
		target.StackTraces = append(target.StackTraces, trace)
	}
	target.Durations = append(target.Durations, target.Durations...)
	return rec
}

func TestDiffNormalizationKeepsRawEvidence(t *testing.T) {
	before, after := Build(testgen.FlameTiny()), Build(doubleTiny())
	for _, tc := range []struct {
		requested DiffMode
		resolved  DiffMode
		unit      string
		delta     float64
	}{
		{DiffAuto, DiffPerSecond, "ms/s", 0},
		{DiffNone, DiffNone, "ms", 80},
		{DiffPerSecond, DiffPerSecond, "ms/s", 0},
		{DiffShare, DiffShare, "percentage-points", 0},
	} {
		t.Run(string(tc.requested), func(t *testing.T) {
			diff, err := DiffNormalized(before, after, tc.requested)
			if err != nil {
				t.Fatal(err)
			}
			normalization := diff.Normalization
			if normalization.RequestedMode != tc.requested || normalization.Mode != tc.resolved || diff.DeltaUnit() != tc.unit || normalization.BeforeActiveMs != 10000 || normalization.AfterActiveMs != 20000 {
				t.Fatalf("normalization contract: %+v", normalization)
			}
			near(t, diff.BeforeTotalMs, 80)
			near(t, diff.AfterTotalMs, 160)
			near(t, diff.TotalMs, tc.delta)
			near(t, diff.Root.TotalMs, tc.delta)
			warnings := map[string]int{}
			for _, warning := range normalization.Warnings {
				warnings[warning.Code]++
			}
			if warnings["duration-mismatch"] != 1 || warnings["sample-sparse"] != 2 {
				t.Fatalf("missing comparison warnings: %+v", normalization.Warnings)
			}
			for _, function := range diff.Functions {
				near(t, function.AfterSelfMs, function.BeforeSelfMs*2)
				near(t, function.AfterTotalMs, function.BeforeTotalMs*2)
				wantSelf, wantTotal := 0.0, 0.0
				if tc.requested == DiffNone {
					wantSelf, wantTotal = function.BeforeSelfMs, function.BeforeTotalMs
				}
				near(t, function.SelfMs, wantSelf)
				near(t, function.TotalMs, wantTotal)
				near(t, normalization.Delta(function.BeforeTotalMs, function.AfterTotalMs), wantTotal)
			}
			pending := []*NodeDelta{diff.Root}
			for len(pending) != 0 {
				node := pending[len(pending)-1]
				pending = pending[:len(pending)-1]
				near(t, node.TotalMs, normalization.Delta(node.BeforeTotalMs, node.AfterTotalMs))
				near(t, node.SelfMs, normalization.Delta(node.BeforeSelfMs, node.AfterSelfMs))
				pending = append(pending, node.Children...)
			}
		})
	}
	if before.Root.TotalMs != 80 || after.Root.TotalMs != 160 || before.ActiveDurationMs != 10000 {
		t.Fatal("normalization mutated input evidence")
	}
	if Diff(before, after).TotalMs != 0 {
		t.Fatal("default Diff did not resolve auto")
	}
	if _, err := DiffNormalized(before, after, "invalid"); err == nil {
		t.Fatal("invalid mode accepted")
	}
}

func TestDiffNormalizationThresholdAndActiveClock(t *testing.T) {
	before := Build(testgen.FlameTiny())
	for _, tc := range []struct {
		end  float64
		want DiffMode
	}{{21, DiffNone}, {21.000001, DiffPerSecond}, {19, DiffNone}, {18.999999, DiffPerSecond}} {
		rec := testgen.FlameTiny()
		rec.EndTime = tc.end
		normalization, err := ResolveDiffNormalization(before, Build(rec), DiffAuto)
		if err != nil || normalization.Mode != tc.want {
			t.Fatalf("end %g: %+v %v", tc.end, normalization, err)
		}
	}
	rec := testgen.FlameTiny()
	rec.EndTime = 30
	rec.Discontinuities = []model.Discontinuity{{StartTime: 20, EndTime: 30}}
	normalization, err := ResolveDiffNormalization(before, Build(rec), DiffAuto)
	if err != nil || normalization.Mode != DiffNone || normalization.AfterActiveMs != 10000 {
		t.Fatalf("used wall rather than active clock: %+v %v", normalization, err)
	}
	for _, mode := range []DiffMode{DiffPerSecond, DiffShare} {
		normalization, err := ResolveDiffNormalization(nil, afterTreeWithNoClock(), mode)
		if err != nil || normalization.Mode != DiffNone || normalization.Unit != "ms" {
			t.Fatalf("missing denominators silently normalized: %+v %v", normalization, err)
		}
		found := false
		for _, warning := range normalization.Warnings {
			found = found || warning.Code == "normalization-unavailable"
		}
		if !found {
			t.Fatal("unavailable denominator lacks explicit warning")
		}
		near(t, normalization.Delta(0, 1), 1)
	}
}

func afterTreeWithNoClock() *Tree {
	return Build(&model.Recording{Samples: []model.TargetSamples{{StackTraces: []model.StackTrace{{Timestamp: 0, StackFrames: []model.StackFrame{{Name: "untimed"}}}}}}})
}

func TestDiffNormalizationShareUsesPercentagePoints(t *testing.T) {
	before := Build(recording([][]model.StackFrame{{frame("A", 1)}, {frame("B", 2)}}, []float64{.001, .003}))
	after := Build(recording([][]model.StackFrame{{frame("A", 1)}, {frame("B", 2)}}, []float64{.003, .001}))
	diff, err := DiffNormalized(before, after, DiffShare)
	if err != nil {
		t.Fatal(err)
	}
	near(t, diff.Functions[0].SelfMs, 50)
	near(t, diff.Functions[1].SelfMs, -50)
	near(t, diff.BeforeTotalMs, 4)
	near(t, diff.AfterTotalMs, 4)
}

func TestDiffAutoPreservesEqualDurationTinyChanges(t *testing.T) {
	rec := testgen.FlameTiny()
	before := Build(rec)
	selected := model.TargetSamples{Target: rec.Samples[0].Target}
	for i, trace := range rec.Samples[0].StackTraces {
		if trace.StackFrames[0].Name == "C" {
			continue
		}
		weight := rec.Samples[0].Durations[i]
		if trace.StackFrames[0].Name == "B" {
			weight *= 2
		}
		selected.StackTraces = append(selected.StackTraces, trace)
		selected.Durations = append(selected.Durations, weight)
	}
	main := rec.Samples[0].StackTraces[0].StackFrames[1]
	selected.StackTraces = append(selected.StackTraces, model.StackTrace{Timestamp: 17.5,
		StackFrames: []model.StackFrame{{Name: "D", URL: "file:///app/app.js", Line: 30, Column: 5}, main}})
	selected.Durations = append(selected.Durations, .007)
	rec.Samples = []model.TargetSamples{selected}
	diff := Diff(before, Build(rec))
	if diff.Normalization.Mode != DiffNone || diff.DeltaUnit() != "ms" {
		t.Fatalf("equal active durations changed default units: %+v", diff.Normalization)
	}
	want := map[string]float64{"A": 0, "B": 20, "C": -21, "D": 7, "getAnimations": 0, "main": 0}
	for _, function := range diff.Functions {
		near(t, function.SelfMs, want[function.Frame.Name])
		delete(want, function.Frame.Name)
	}
	if len(want) != 0 {
		t.Fatalf("diff omitted functions: %v", want)
	}
	near(t, diff.AfterTotalMs, 86)
	near(t, diff.TotalMs, 6)
}

func TestFocusFunctionCountsRecursiveSamplesOnce(t *testing.T) {
	tree := Build(testgen.FlameTiny())
	a := findFunction(t, tree, "A")
	focused := tree.FocusFunction(a.ID)
	near(t, focused.Root.TotalMs, 50)
	if focused.SampleCount() != 6 || len(focused.Paths()) != 5 {
		t.Fatal("recursive sample duplicated or path lost")
	}
	if len(focused.Samples[1].Frames) != 2 {
		t.Fatal("forward focus did not keep outermost recursion")
	}
	inverted := tree.Inverted().FocusFunction(a.ID)
	near(t, inverted.Root.TotalMs, 50)
	if !inverted.inverted || inverted.Samples[1].Frames[0].Name != "main" || len(inverted.Samples[1].Frames) != 3 {
		t.Fatal("inverted focus did not retain prefix through last occurrence")
	}
	if inverted.Root.Children[0].Frame.Name != "A" {
		t.Fatal("inverted focus root is not selected function")
	}
	if !reflect.DeepEqual(inverted.Canonical().Samples, inverted.Samples) || inverted.Canonical().inverted {
		t.Fatal("canonical presentation altered samples")
	}
	if tree.FocusFunction("missing").Root.TotalMs != 0 || (*Tree)(nil).FocusFunction(a.ID).Root.TotalMs != 0 {
		t.Fatal("unknown/nil function focus is not empty")
	}
	untimed := Build(testgen.UntimedSamples())
	function := findFunction(t, untimed, "sharedLeaf")
	selected := untimed.FocusFunction(function.ID)
	near(t, selected.Root.TotalMs, 55)
	if selected.UntimedTotal != untimed.UntimedTotal || !selected.UntimedIncluded || len(selected.UntimedPathIDs()) != len(selected.Untimed) {
		t.Fatal("function focus lost untimed evidence metadata")
	}
}

func TestFrameCatalogKeepsCanonicalMetadata(t *testing.T) {
	raw := frame("work", 1)
	mapped := raw
	mapped.SourceID = "mapped"
	mapped.Original = &model.SourceLocation{Name: "original", URL: "src/work.ts", Line: 8, Column: 3}
	before, after := Build(recording([][]model.StackFrame{{raw}}, nil)), Build(recording([][]model.StackFrame{{mapped}}, nil))
	frames := FrameCatalog(before, after)
	if len(frames) != 1 || frames[0].Original == nil || frames[0].ID != before.Functions()[0].ID {
		t.Fatal("catalog split identity or discarded available mapping")
	}
	if !reflect.DeepEqual(frames, FrameCatalog(after, before)) {
		t.Fatal("catalog depends on input order")
	}
}
