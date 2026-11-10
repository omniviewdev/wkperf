package flame

import (
	"encoding/json"
	"fmt"
	"math"
	"math/rand/v2"
	"reflect"
	"strings"
	"testing"

	"github.com/omniviewdev/wkperf/internal/model"
)

func frame(name string, line int) model.StackFrame {
	return model.StackFrame{Name: name, URL: "app.ts", Line: line, Column: 1}
}

func recording(stacks [][]model.StackFrame, weights []float64) *model.Recording {
	traces := make([]model.StackTrace, len(stacks))
	for i, stack := range stacks {
		traces[i] = model.StackTrace{Timestamp: .01 + float64(i)*.01, StackFrames: stack}
	}
	return &model.Recording{EndTime: 1, Samples: []model.TargetSamples{{StackTraces: traces, Durations: weights}}}
}

func goldenRecording() *model.Recording {
	main, work, render := frame("main", 1), frame("work", 2), frame("render", 3)
	return recording([][]model.StackFrame{{work, main}, {render, work, main}, {work, main}, {main}}, []float64{.004, .002, .003, .001})
}

func names(nodes []*Node) string {
	out := make([]string, len(nodes))
	for i, node := range nodes {
		out[i] = node.Frame.Name
	}
	return strings.Join(out, ";")
}

func findFunction(t *testing.T, tree *Tree, name string) Function {
	t.Helper()
	for _, function := range tree.Functions() {
		if function.Frame.Name == name {
			return function
		}
	}
	t.Fatalf("missing function %q", name)
	return Function{}
}

func near(t *testing.T, got, want float64) {
	t.Helper()
	if math.Abs(got-want) > 1e-9 {
		t.Fatalf("got %.15g, want %.15g", got, want)
	}
}

func TestWeightedTreeGolden(t *testing.T) {
	tree := Build(goldenRecording())
	near(t, tree.Root.TotalMs, 10)
	if tree.Root.Samples != 4 || len(tree.Root.Children) != 1 {
		t.Fatalf("root: %+v", tree.Root)
	}
	main := tree.Root.Children[0]
	work := main.Children[0]
	render := work.Children[0]
	if main.Parent != tree.Root || work.Parent != main || render.Parent != work {
		t.Fatal("broken parents")
	}
	near(t, main.SelfMs, 1)
	near(t, work.SelfMs, 7)
	near(t, render.SelfMs, 2)
	if got, want := tree.Folded(), "main@app.ts:1:1 1\nmain@app.ts:1:1;work@app.ts:2:1 7\nmain@app.ts:1:1;work@app.ts:2:1;render@app.ts:3:1 2\n"; got != want {
		t.Fatalf("folded:\n%s\nwant:\n%s", got, want)
	}
	if got := names(tree.HotPath(0)); got != "main;work;render" {
		t.Fatalf("hot path: %s", got)
	}
	if got := names(tree.HotPath(.3)); got != "main;work" {
		t.Fatalf("threshold hot path: %s", got)
	}
	if len(tree.HotPath(math.NaN())) != 0 {
		t.Fatal("nonfinite threshold must select nothing")
	}
	paths := tree.Paths()
	if len(paths) != 3 {
		t.Fatalf("paths: %+v", paths)
	}
	if paths[1].Samples != 2 || paths[1].SelfMs != 7 || paths[1].TotalMs != 7 {
		t.Fatalf("path: %+v", paths[1])
	}
	if paths[1].ID != work.ID {
		t.Fatal("path ID does not match call-tree context")
	}
	if findFunction(t, tree, "work").Samples != 3 {
		t.Fatal("function samples wrong")
	}
	workFn := findFunction(t, tree, "work")
	callers, callees := tree.Callers(workFn.ID), tree.Callees(workFn.ID)
	if len(callers) != 1 || callers[0].Caller.Name != "main" || callers[0].Samples != 3 {
		t.Fatalf("callers: %+v", callers)
	}
	near(t, callers[0].TotalMs, 9)
	if len(callees) != 1 || callees[0].Callee.Name != "render" {
		t.Fatalf("callees: %+v", callees)
	}
	near(t, callees[0].TotalMs, 2)
	if NearestAppAncestor(render) != work {
		t.Fatal("nearest app ancestor must exclude the frame itself")
	}
	encoded, err := json.Marshal(tree)
	if err != nil || strings.Contains(string(encoded), "\"parent\"") {
		t.Fatalf("tree JSON: %s, %v", encoded, err)
	}
}

func TestStableIDsAndShuffledSamples(t *testing.T) {
	main, alpha, beta := frame("main", 1), frame("same", 2), frame("same", 3)
	var stacks [][]model.StackFrame
	var weights []float64
	for i := range 96 {
		leaf := alpha
		if i%2 != 0 {
			leaf = beta
		}
		stacks = append(stacks, []model.StackFrame{leaf, main})
		weights = append(weights, []float64{.000001, .1, .0002, .003}[i%4])
	}
	rec := recording(stacks, weights)
	for i := range rec.Samples[0].StackTraces {
		rec.Samples[0].StackTraces[i].Timestamp = .25
	}
	baseline := Build(rec)
	want, _ := json.Marshal(baseline)
	wantHot, _ := json.Marshal(baseline.HotPath(0))
	rng := rand.New(rand.NewPCG(19, 23))
	for range 12 {
		rng.Shuffle(len(rec.Samples[0].StackTraces), func(i, j int) {
			rec.Samples[0].StackTraces[i], rec.Samples[0].StackTraces[j] = rec.Samples[0].StackTraces[j], rec.Samples[0].StackTraces[i]
			rec.Samples[0].Durations[i], rec.Samples[0].Durations[j] = rec.Samples[0].Durations[j], rec.Samples[0].Durations[i]
		})
		got := Build(rec)
		encoded, _ := json.Marshal(got)
		hot, _ := json.Marshal(got.HotPath(0))
		if string(encoded) != string(want) || got.Folded() != baseline.Folded() || string(hot) != string(wantHot) {
			t.Fatal("sample shuffle changed deterministic output")
		}
	}
	changed := goldenRecording()
	changed.Samples[0].Durations[0] = .1
	if Build(changed).Root.Children[0].ID != Build(goldenRecording()).Root.Children[0].ID {
		t.Fatal("weights changed context IDs")
	}
	functions := baseline.Functions()
	if functions[1].Frame.Line != 2 || functions[2].Frame.Line != 3 {
		t.Fatalf("function sort: %+v", functions)
	}
}

func TestMissingZeroInvalidAndEmptySamples(t *testing.T) {
	main := frame("main", 1)
	rec := recording([][]model.StackFrame{{main}, {main}, {main}, {main}, {main}, {main}, nil, {main}, {main}}, []float64{0, -1, math.NaN(), math.Inf(1), math.Inf(-1), .002, .1})
	rec.Samples[0].StackTraces[7].Timestamp = math.NaN()
	tree := Build(rec)
	near(t, tree.Root.TotalMs, 3)
	if tree.Root.Samples != 2 {
		t.Fatalf("got %d samples, want 2", tree.Root.Samples)
	}
	if _, err := json.Marshal(tree); err != nil {
		t.Fatalf("nonfinite data escaped filtering: %v", err)
	}
	if Build(nil).Root.TotalMs != 0 || len(Build(nil).Samples) != 0 {
		t.Fatal("nil input is not empty")
	}
}

func TestFrameIdentityMetadataAndEscaping(t *testing.T) {
	urlFrame := Frame{Name: "a", URL: "app.js", Line: 1, Column: 2, SourceID: "one"}
	other := urlFrame
	other.SourceID = "two"
	if FunctionID(urlFrame) != FunctionID(other) {
		t.Fatal("source ID split URL-backed identity")
	}
	other.Column++
	if FunctionID(urlFrame) == FunctionID(other) {
		t.Fatal("column omitted from identity")
	}
	urlFrame.URL, other.URL, other.Column = "", "", urlFrame.Column
	if FunctionID(urlFrame) == FunctionID(other) {
		t.Fatal("native source ID fallback missing")
	}
	anonymous := model.StackFrame{URL: "app.js", Line: 2}
	native := model.StackFrame{Name: "native;slash\\line\n", SourceID: "native"}
	mapped := frame("generated", 4)
	mapped.Original = &model.SourceLocation{Name: "Original", URL: "src/App.tsx", Line: 12, Column: 3}
	tree := Build(recording([][]model.StackFrame{{native, anonymous, mapped}}, []float64{.002}))
	if got := tree.Folded(); got != "Original@src/App.tsx:12:3\\ [generated\\ generated@app.ts:4:1];(anonymous)@app.js:2:0;native\\;slash\\\\line\\n@native:native:0:0 2\n" {
		t.Fatalf("escaped folded: %q", got)
	}
	if !tree.Samples[0].Frames[1].Anonymous || !tree.Samples[0].Frames[2].Native {
		t.Fatal("missing flags")
	}
	if tree.Samples[0].Frames[0].Original.URL != "src/App.tsx" {
		t.Fatal("lost original location")
	}
	mappedTree := Build(recording([][]model.StackFrame{{mapped}}, []float64{.001}), Options{Mapper: func(model.StackFrame) *Location { return &Location{URL: "node_modules/react/index.js", Line: 1} }})
	if mappedTree.Samples[0].Frames[0].Category != CategoryFramework {
		t.Fatal("mapped category ignored")
	}
	if FunctionID(mappedTree.Samples[0].Frames[0]) != FunctionID(tree.Samples[0].Frames[0]) {
		t.Fatal("source map changed ID")
	}
}

func TestWindowSelectsTimestampsAndExcludesDiscontinuities(t *testing.T) {
	work := frame("work", 1)
	rec := &model.Recording{StartTime: 10, EndTime: 11,
		Discontinuities: []model.Discontinuity{{StartTime: 10.4, EndTime: 10.6}},
		Samples: []model.TargetSamples{{StackTraces: []model.StackTrace{
			{Timestamp: 10.25, StackFrames: []model.StackFrame{work}},
			{Timestamp: 10.5, StackFrames: []model.StackFrame{work}},
			{Timestamp: 10.625, StackFrames: []model.StackFrame{work}},
			{Timestamp: 10.75, StackFrames: []model.StackFrame{work}},
		}, Durations: []float64{.4, .9, .2, .1}}},
	}
	tree := Build(rec)
	near(t, tree.ActiveDurationMs, 800)
	near(t, tree.Root.TotalMs, 700)
	if tree.Root.Samples != 3 {
		t.Fatalf("paused timestamp included: %+v", tree.Samples)
	}
	window := tree.Window(.25, .75)
	near(t, window.ActiveDurationMs, 300)
	near(t, window.Root.TotalMs, 600)
	if window.Root.Samples != 2 {
		t.Fatal("half-open window selected wrong timestamps")
	}
	nested := tree.Window(.2, .8).Window(.25, .75)
	if !reflect.DeepEqual(window, nested) {
		t.Fatal("nested windows differ from direct window")
	}
	if tree.Window(.45, .55).Root.TotalMs != 0 {
		t.Fatal("paused time included")
	}
	if tree.Window(.8, .2).Root.TotalMs != 0 || tree.Window(math.NaN(), .5).Root.TotalMs != 0 {
		t.Fatal("invalid bounds selected samples")
	}
	selected := Build(rec, Options{Intervals: []Interval{{Start: .2, End: .3}, {Start: .25, End: .35}}})
	near(t, selected.Root.TotalMs, 400)
	if selected.Root.Samples != 1 {
		t.Fatal("overlap duplicated sample")
	}
	if Build(rec, Options{Intervals: []Interval{}}).Root.TotalMs != 0 {
		t.Fatal("empty interval selection not empty")
	}
}

func TestRecursiveDeepStacksFocusAndInversion(t *testing.T) {
	main, loop := frame("main", 1), frame("loop", 2)
	stack := make([]model.StackFrame, 367)
	for i := range stack {
		stack[i] = loop
	}
	stack[len(stack)-1] = main
	tree := Build(recording([][]model.StackFrame{stack}, []float64{.004}))
	function := findFunction(t, tree, "loop")
	if function.Samples != 1 || function.TotalMs != 4 || function.SelfMs != 4 {
		t.Fatalf("recursive function: %+v", function)
	}
	edges := tree.Callees(function.ID)
	if len(edges) != 1 || edges[0].Samples != 1 || edges[0].TotalMs != 4 {
		t.Fatalf("recursive edge: %+v", edges)
	}
	path := tree.HotPath(0)
	if len(path) != 367 || path[1].ID == path[2].ID {
		t.Fatal("deep recursive path collapsed")
	}
	focused := tree.FocusNode(path[100].ID)
	if len(focused.Samples[0].Frames) != 267 || focused.Root.TotalMs != 4 {
		t.Fatal("context focus wrong")
	}
	if tree.FocusNode("missing").Root.TotalMs != 0 {
		t.Fatal("unknown focus did not select empty tree")
	}
	inverted := tree.Inverted()
	if inverted.Root.Children[0].Frame.Name != "loop" || inverted.Root.Children[0].SelfMs != 4 {
		t.Fatal("inverted leaf lost self weight")
	}
	if len(inverted.HotPath(0)) != 367 || inverted.HotPath(0)[366].SelfMs != 0 {
		t.Fatal("inverted callers claim self weight")
	}
	if !reflect.DeepEqual(tree.Functions(), inverted.Functions()) || tree.Folded() != inverted.Folded() {
		t.Fatal("inversion changed canonical aggregate")
	}
	if _, err := json.Marshal(tree); err != nil {
		t.Fatal(err)
	}
}

func TestDiffGolden(t *testing.T) {
	before := Build(goldenRecording())
	after := Build(recording([][]model.StackFrame{{frame("new", 4), frame("main", 1)}, {frame("work", 2), frame("main", 1)}}, []float64{.005, .002}))
	diff := Diff(before, after)
	near(t, diff.BeforeTotalMs, 10)
	near(t, diff.AfterTotalMs, 7)
	near(t, diff.TotalMs, -3)
	var got strings.Builder
	for _, function := range diff.Functions {
		fmt.Fprintf(&got, "%s:%g/%g/%d\n", function.Frame.Name, function.SelfMs, function.TotalMs, function.Samples)
	}
	want := "main:-1/-3/-2\nnew:5/5/1\nrender:-2/-2/-1\nwork:-5/-7/-2\n"
	if got.String() != want {
		t.Fatalf("diff:\n%s\nwant:\n%s", got.String(), want)
	}
	wantFolded := "main@app.ts:1:1 1 0\nmain@app.ts:1:1;new@app.ts:4:1 0 5\nmain@app.ts:1:1;work@app.ts:2:1 7 2\nmain@app.ts:1:1;work@app.ts:2:1;render@app.ts:3:1 2 0\n"
	if got := FoldedDiff(before, after); got != wantFolded {
		t.Fatalf("folded diff: %q", got)
	}
	if Diff(nil, nil).Root.TotalMs != 0 || FoldedDiff(nil, nil) != "" {
		t.Fatal("nil diff not empty")
	}
}

func TestCategoryAndHotPathTie(t *testing.T) {
	for _, name := range []string{"performSyncWorkOnRoot", "performWorkOnRoot", "renderRootSync", "workLoopSync", "performUnitOfWork", "renderWithHooks", "flushSync", "flushSyncFromReconciler", "flushSyncWorkAcrossRoots", "processRootScheduleInMicrotask", "commitOnFiberMutationEffects", "recursivelyTraversePassiveMountEffects", "performWorkUntilDeadline"} {
		for _, url := range []string{"assets/vendor-bundle.js", "assets/app.js", ""} {
			if got := Categorize(Frame{Name: name, URL: url}); got != CategoryFramework {
				t.Errorf("%s at %q: %s", name, url, got)
			}
		}
	}
	for _, path := range []string{"/node_modules/x/index.js", "/vendor/react.js", "/plugins/aws/plugin.js", "/assets/plugin-foo.js", "plugin-foo/index.js", "plugin.js"} {
		if got := Categorize(Frame{Name: "run", URL: path}); got != CategoryFramework {
			t.Errorf("%s: %s", path, got)
		}
	}
	if Categorize(Frame{Name: "Garbage Collection"}) != CategoryGC || Categorize(Frame{Name: "native"}) != CategoryNative || Categorize(Frame{Name: "app", URL: "/src/App.tsx"}) != CategoryApp {
		t.Fatal("basic category wrong")
	}
	tree := Build(recording([][]model.StackFrame{{frame("z", 1)}, {frame("a", 1)}}, []float64{.001, .001}))
	if got := names(tree.HotPath(0)); got != "a" {
		t.Fatalf("tie: %q", got)
	}
}

func TestCanonicalMetadataKeepsAvailableOriginal(t *testing.T) {
	raw := frame("mapped", 1)
	mapped := raw
	mapped.Original = &model.SourceLocation{Name: "original", URL: "src/Original.ts", Line: 7, Column: 2}
	rec := recording([][]model.StackFrame{{raw}, {mapped}}, []float64{.001, .002})
	tree := Build(rec)
	for _, sample := range tree.Samples {
		if sample.Frames[0].Original == nil {
			t.Fatal("available original metadata lost during canonicalization")
		}
	}
	if tree.Root.Children[0].Frame.Original == nil {
		t.Fatal("tree lost original metadata")
	}
}

func TestWindowClampsToRecordingBounds(t *testing.T) {
	tree := Build(goldenRecording())
	window := tree.Window(2, 3)
	if window.StartTime != 1 || window.EndTime != 1 {
		t.Fatalf("out-of-range window: %g..%g", window.StartTime, window.EndTime)
	}
	from := 2.0
	built := Build(goldenRecording(), Options{From: &from})
	if built.StartTime != 1 || built.EndTime != 1 {
		t.Fatalf("out-of-range options: %g..%g", built.StartTime, built.EndTime)
	}
}

func TestMapperReusesRepeatedRawFrames(t *testing.T) {
	calls := 0
	mapper := func(model.StackFrame) *Location { calls++; return &Location{URL: "src/Original.ts", Line: 1} }
	tree := Build(goldenRecording(), Options{Mapper: mapper})
	if calls != 3 {
		t.Fatalf("resolved %d frames, want 3 distinct frames", calls)
	}
	if len(tree.Functions()) != 3 {
		t.Fatal("mapper changed function identity")
	}
}

func TestSignedZeroTimestampDeterminism(t *testing.T) {
	main := frame("main", 1)
	rec := recording([][]model.StackFrame{{main}, {main}}, []float64{.001, .001})
	rec.Samples[0].StackTraces[0].Timestamp = 0
	rec.Samples[0].StackTraces[1].Timestamp = math.Copysign(0, -1)
	before, _ := json.Marshal(Build(rec))
	rec.Samples[0].StackTraces[0], rec.Samples[0].StackTraces[1] = rec.Samples[0].StackTraces[1], rec.Samples[0].StackTraces[0]
	after, _ := json.Marshal(Build(rec))
	if string(before) != string(after) {
		t.Fatal("signed zero timestamps changed shuffled JSON")
	}
}

func TestShiftedRecordingWindowBoundaries(t *testing.T) {
	makeRecording := func(origin float64) *model.Recording {
		main := frame("main", 1)
		return &model.Recording{StartTime: origin, EndTime: origin + 1,
			Discontinuities: []model.Discontinuity{{StartTime: origin + .4, EndTime: origin + .6}},
			Samples: []model.TargetSamples{{StackTraces: []model.StackTrace{
				{Timestamp: origin + .1, StackFrames: []model.StackFrame{main}},
				{Timestamp: origin + .2, StackFrames: []model.StackFrame{main}},
			}, Durations: []float64{.00123456789, .003}}},
		}
	}
	baseline := Build(makeRecording(0))
	want, _ := json.Marshal(baseline)
	for _, origin := range []float64{10, 1000} {
		rec := makeRecording(origin)
		tree := Build(rec)
		encoded, _ := json.Marshal(tree)
		if string(encoded) != string(want) {
			t.Errorf("origin %g changed relative tree JSON", origin)
		}
		window := tree.Window(.1, .2)
		if window.Root.Samples != 1 {
			t.Errorf("origin %g selected %d boundary samples, want 1", origin, window.Root.Samples)
		}
		near(t, window.Root.TotalMs, 1.23456789)
		from, to := .1, .2
		built := Build(rec, Options{From: &from, To: &to})
		if built.Root.Samples != 1 {
			t.Errorf("origin %g options selected %d boundary samples, want 1", origin, built.Root.Samples)
		}
	}
}

func TestSamplePathIDsStayAligned(t *testing.T) {
	tree := Build(goldenRecording())
	main := tree.Root.Children[0]
	work := main.Children[0]
	render := work.Children[0]
	want := []string{work.ID, render.ID, work.ID, main.ID}
	if got := tree.SamplePathIDs(); !reflect.DeepEqual(got, want) {
		t.Fatalf("sample paths: %v, want %v", got, want)
	}
	if got := tree.Inverted().SamplePathIDs(); !reflect.DeepEqual(got, want) {
		t.Fatal("inversion changed canonical path IDs")
	}
	if got := tree.Window(.015, .035).SamplePathIDs(); !reflect.DeepEqual(got, want[1:3]) {
		t.Fatal("window misaligned sample IDs")
	}
	focused := tree.FocusNode(work.ID)
	if focused.SamplePathIDs()[0] != focused.Root.Children[0].ID {
		t.Fatal("focus retained stale context ID")
	}
	var empty *Tree
	if len(empty.SamplePathIDs()) != 0 {
		t.Fatal("nil tree sample paths not empty")
	}
}
