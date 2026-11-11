package flame

import (
	"testing"

	core "github.com/omniviewdev/wkperf/internal/flame"
	"github.com/omniviewdev/wkperf/internal/model"
)

func TestNormalizedDiffViewPreservesSelectedModeAndRawValues(t *testing.T) {
	frame := model.StackFrame{Name: "work", URL: "app.js", Line: 1, Column: 1}
	before := core.Build(&model.Recording{EndTime: 1, Samples: []model.TargetSamples{{StackTraces: []model.StackTrace{{Timestamp: .5, StackFrames: []model.StackFrame{frame}}}, Durations: []float64{.001}}}})
	after := core.Build(&model.Recording{EndTime: 2, Samples: []model.TargetSamples{{StackTraces: []model.StackTrace{{Timestamp: .5, StackFrames: []model.StackFrame{frame}}, {Timestamp: 1.5, StackFrames: []model.StackFrame{frame}}}, Durations: []float64{.001, .001}}}})
	for _, mode := range []core.DiffMode{core.DiffAuto, core.DiffNone, core.DiffPerSecond, core.DiffShare} {
		comparison, err := core.DiffNormalized(before, after, mode)
		if err != nil {
			t.Fatal(err)
		}
		chart := Chart{Label: "diff", Before: before, After: after, Comparison: comparison}
		want := 0.0
		if mode == core.DiffNone {
			want = 1
		}
		for _, invert := range []bool{false, true} {
			root := chartView(chart, invert)
			if root.DeltaMS != want || root.Children[0].DeltaMS != want || root.BaselineMS != 1 || root.CurrentMS != 2 {
				t.Errorf("%s invert=%t changed normalized color delta or raw values: %+v", mode, invert, root)
			}
		}
	}
}
