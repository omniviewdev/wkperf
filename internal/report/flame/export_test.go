package flame

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	core "github.com/omniviewdev/wkperf/internal/flame"
	"github.com/omniviewdev/wkperf/internal/model"
)

func TestExportsPreserveColumnsNativeAndAnonymous(t *testing.T) {
	frames := []model.StackFrame{
		{Name: "same", URL: "app.js", Line: 1, Column: 1},
		{Name: "same", URL: "app.js", Line: 1, Column: 2},
		{Name: "native", SourceID: "native-source"},
		{SourceID: "anonymous-source"},
	}
	samples := model.TargetSamples{}
	for i, frame := range frames {
		samples.StackTraces = append(samples.StackTraces, model.StackTrace{Timestamp: float64(i) * .001, StackFrames: []model.StackFrame{frame}})
		samples.Durations = append(samples.Durations, .001)
	}
	tree := core.Build(&model.Recording{EndTime: 1, Samples: []model.TargetSamples{samples}})
	var out bytes.Buffer
	if err := Render(&out, "speedscope", []Chart{{Label: "identity", Tree: tree}}, Options{}); err != nil {
		t.Fatal(err)
	}
	var doc Speedscope
	if err := json.Unmarshal(out.Bytes(), &doc); err != nil {
		t.Fatal(err)
	}
	if len(doc.Shared.Frames) != 4 {
		t.Fatalf("frame identities merged: %+v", doc.Shared.Frames)
	}
	for _, suffix := range []string{"app.js:1:1", "app.js:1:2", "native-source", "anonymous-source", "(anonymous)"} {
		if !strings.Contains(out.String(), suffix) {
			t.Errorf("speedscope lacks %q", suffix)
		}
	}
	out.Reset()
	if err := Render(&out, "folded", []Chart{{Label: "identity", Tree: tree}}, Options{}); err != nil {
		t.Fatal(err)
	}
	if strings.Count(strings.TrimSpace(out.String()), "\n") != 3 {
		t.Fatalf("folded frames merged: %s", out.String())
	}
}

func TestDifferentialFoldedAndSpeedscopeWeights(t *testing.T) {
	makeTree := func(name string, weight float64) *core.Tree {
		return core.Build(&model.Recording{EndTime: 1, Samples: []model.TargetSamples{{StackTraces: []model.StackTrace{{StackFrames: []model.StackFrame{{Name: name, URL: "app.js", Line: 1, Column: 1}}}}, Durations: []float64{weight}}}})
	}
	before, after := makeTree("removed", .004), makeTree("added", .006)
	chart := Chart{Label: "delta", Before: before, After: after, Comparison: core.Diff(before, after)}
	var out bytes.Buffer
	if err := Render(&out, "folded", []Chart{chart}, Options{}); err != nil {
		t.Fatal(err)
	}
	for _, line := range []string{"added@app.js:1:1 0 6\n", "removed@app.js:1:1 4 0\n"} {
		if !strings.Contains(out.String(), line) {
			t.Errorf("differential folded lacks %q: %s", line, out.String())
		}
	}
	out.Reset()
	if err := Render(&out, "speedscope", []Chart{chart}, Options{}); err != nil {
		t.Fatal(err)
	}
	var doc Speedscope
	if err := json.Unmarshal(out.Bytes(), &doc); err != nil {
		t.Fatal(err)
	}
	if len(doc.Profiles) != 2 || doc.Profiles[0].EndValue != 4 || doc.Profiles[1].EndValue != 6 {
		t.Fatalf("differential speedscope must retain both runs: %+v", doc.Profiles)
	}
	out.Reset()
	if err := RenderSVG(&out, []Chart{chart}, Options{Invert: true, MinMS: 5}); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out.String(), "Generated: removed") || !strings.Contains(out.String(), "Generated: added") {
		t.Fatalf("diff inversion/threshold lost delta nodes: %s", out.String())
	}
}

func TestFragmentsStayScopedAndEmptyChartsStayValid(t *testing.T) {
	var out bytes.Buffer
	charts := []Chart{{Label: "empty", Tree: core.Build(&model.Recording{})}}
	if err := HTMLFragment(&out, charts, Options{}); err != nil {
		t.Fatal(err)
	}
	if err := HTMLFragment(&out, charts, Options{}); err != nil {
		t.Fatal(err)
	}
	if strings.Count(out.String(), "<section class=\"wk-flame\"") != 2 || strings.Contains(out.String(), " id=") || !strings.Contains(out.String(), "No weighted samples") {
		t.Fatal("fragment IDs collide or empty state missing")
	}
	for _, format := range []string{"svg", "json", "speedscope", "folded"} {
		out.Reset()
		if err := Render(&out, format, charts, Options{}); err != nil {
			t.Errorf("empty %s: %v", format, err)
		}
	}
}

func TestFoldedMultipleRecordingLabelsEscapeSeparators(t *testing.T) {
	var out bytes.Buffer
	charts := []Chart{{Label: "one; run\n", Tree: fixtureTree()}, {Label: "two", Tree: fixtureTree()}}
	if err := Render(&out, "folded", charts, Options{}); err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(out.String(), `one\;\ run\n;`) || strings.Contains(out.String(), "one; run") {
		t.Fatalf("recording label broke folded syntax: %s", out.String())
	}
}

func TestInvertedDiffDoesNotDoubleCountExecutingSelf(t *testing.T) {
	makeTree := func(weight float64) *core.Tree {
		return core.Build(&model.Recording{EndTime: 1, Samples: []model.TargetSamples{{StackTraces: []model.StackTrace{{StackFrames: []model.StackFrame{{Name: "work", URL: "app.js"}, {Name: "caller", URL: "app.js"}}}}, Durations: []float64{weight}}}})
	}
	before, after := makeTree(.004), makeTree(.006)
	root := chartView(Chart{Before: before, After: after, Comparison: core.Diff(before, after)}, true)
	if root.LayoutMS != 6 || len(root.Children) != 1 || root.Children[0].LayoutMS != 6 {
		t.Fatalf("inverted self ownership doubled layout width: %+v", root)
	}
}
