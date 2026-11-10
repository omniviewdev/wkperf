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
