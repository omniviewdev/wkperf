package testgen

import "github.com/omniviewdev/wkperf/internal/model"

// FlameTiny contains seven samples and six exact paths. Inclusive function
// weights are main=80, A=50, B=51, C=21 and getAnimations=30 milliseconds.
// A recurs, and B has two callers. These values count recursion once per sample.
func FlameTiny() *model.Recording {
	main := model.StackFrame{Name: "main", URL: "file:///app/app.js", Line: 1, Column: 1}
	a := model.StackFrame{Name: "A", URL: "file:///app/app.js", Line: 10, Column: 5}
	b := model.StackFrame{Name: "B", URL: "file:///app/app.js", Line: 20, Column: 5}
	c := model.StackFrame{Name: "C", URL: "file:///app/lib.js", Line: 5, Column: 3}
	native := model.StackFrame{Name: "getAnimations", SourceID: "4294967295", Line: -1, Column: -1}
	stacks := [][]model.StackFrame{{a, main}, {a, a, main}, {b, a, main}, {b, a, main}, {c, a, main}, {c, b, a, main}, {native, b, main}}
	target := model.TargetSamples{Target: model.Target{Identifier: "main"}, Durations: []float64{.005, .004, .010, .010, .020, .001, .030}}
	for i, stack := range stacks {
		target.StackTraces = append(target.StackTraces, model.StackTrace{Timestamp: 10.5 + float64(i), StackFrames: stack})
	}
	return &model.Recording{StartTime: 10, EndTime: 20, Samples: []model.TargetSamples{target}}
}

// ReactOwnership separates framework-only commit work, unresolved library
// context, first-party plugins, injected scripts, and mapped user/library code.
// Seven samples carry exactly 30 milliseconds of statistical weight.
func ReactOwnership() *model.Recording {
	rec := Empty()
	rec.DisplayName = "Bundled React ownership evidence"
	bundle := "wails://localhost/assets/index-SYNTHETIC.js"
	bundled := func(name string, line int) model.StackFrame {
		return model.StackFrame{Name: name, URL: bundle, Line: line, Column: 1}
	}
	native := model.StackFrame{Name: "removeChild", SourceID: "native", Line: -1, Column: -1}
	commit := []model.StackFrame{
		native, bundled("commitDeletionEffectsOnFiber", 17), bundled("recursivelyTraverseMutationEffects", 16),
		bundled("commitMutationEffectsOnFiber", 15), bundled("commitRoot", 14),
		bundled("performSyncWorkOnRoot", 13), bundled("processRootScheduleInMicrotask", 12),
	}
	animations := []model.StackFrame{
		{Name: "getAnimations", SourceID: "native", Line: -1, Column: -1},
		bundled("getFiniteAnimations", 23), bundled("exec2", 22), bundled("", 21), bundled("tick", 20),
	}
	plugin := model.StackFrame{Name: "cell", URL: "wails://localhost/_/plugins/kubernetes/assets/entry2.js", Line: 30, Column: 7}
	library := bundled("getFiniteAnimations", 40)
	library.Original = &model.SourceLocation{Name: "getFiniteAnimations", URL: "/node_modules/@base-ui/react/utils/getFiniteAnimations.ts", Line: 4, Column: 2}
	mappedUser := bundled("setProp", 50)
	mappedUser.Original = &model.SourceLocation{Name: "commitRows", URL: "/src/rows.ts", Line: 9, Column: 3}
	stacks := [][]model.StackFrame{
		commit, animations, {plugin, bundled("renderWithHooks", 11), bundled("performSyncWorkOnRoot", 13)},
		{{Name: "commitRoot", URL: "/src/store.ts", Line: 8, Column: 1}},
		{{Name: "bridge", URL: "user-script:2", Line: 1, Column: 1}},
		{library}, {mappedUser},
	}
	samples := model.TargetSamples{Target: target(), Durations: []float64{.006, .004, .009, .003, .002, .005, .001}}
	for i, stack := range stacks {
		samples.StackTraces = append(samples.StackTraces, model.StackTrace{Timestamp: 1 + float64(i)*.01, StackFrames: stack})
	}
	rec.Samples = []model.TargetSamples{samples}
	return rec
}
