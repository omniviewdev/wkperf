package testgen

import (
	"fmt"

	"github.com/omniviewdev/wkperf/internal/model"
)

// DeepStacks makes shallow depth-five samples and distinct exact deep stacks.
// Deep paths share their ancestors but have separate leaves, as minified bundles do.
func DeepStacks(depth, deep, shallow int) *model.Recording {
	rec := Empty()
	rec.DisplayName = "Deep stack output scaling"
	rec.EndTime = 20
	depth = max(1, depth)
	deep, shallow = max(0, deep), max(0, shallow)
	chain := func(n int, leaf string, line int) []model.StackFrame {
		frames := make([]model.StackFrame, n)
		frames[0] = model.StackFrame{Name: leaf, URL: "app.js", SourceID: "2", Line: line, Column: 1}
		for j := 1; j < n; j++ {
			level := n - j - 1
			frames[j] = model.StackFrame{Name: fmt.Sprintf("f%d", level), URL: "app.js", SourceID: "2", Line: level + 1, Column: 1}
		}
		return frames
	}
	samples := model.TargetSamples{Target: target(), StackTraces: make([]model.StackTrace, 0, shallow+deep), Durations: make([]float64, 0, shallow+deep)}
	shallowFrames := chain(5, "leaf", 901)
	for i := range shallow {
		at := 1 + float64(i)*.01
		samples.StackTraces = append(samples.StackTraces, model.StackTrace{Timestamp: at, StackFrames: shallowFrames})
		samples.Durations = append(samples.Durations, .001)
		rec.EndTime = max(rec.EndTime, at+.01)
	}
	for i := range deep {
		at := 10 + float64(i)*.01
		samples.StackTraces = append(samples.StackTraces, model.StackTrace{Timestamp: at, StackFrames: chain(depth, fmt.Sprintf("deepLeaf%d", i), 1001+i)})
		samples.Durations = append(samples.Durations, .001)
		rec.EndTime = max(rec.EndTime, at+.01)
	}
	rec.Samples = []model.TargetSamples{samples}
	return rec
}
