package testgen

import (
	"testing"

	"github.com/omniviewdev/wkperf/internal/flame"
)

func TestDeepStacksScalingShape(t *testing.T) {
	rec := DeepStacks(400, 80, 200)
	traces := rec.Samples[0].StackTraces
	if len(traces) != 280 || len(traces[0].StackFrames) != 5 || len(traces[200].StackFrames) != 400 {
		t.Fatal("deep generator must keep D400/K80/shallow200 regression shape")
	}
	if traces[200].StackFrames[0].Name != "deepLeaf0" || traces[200].StackFrames[399].Name != "f0" {
		t.Fatal("generator must use WebKit's self-first frame order")
	}
	tree := flame.Build(rec)
	if len(tree.Paths()) != 81 || tree.SampleCount() != 280 || len(tree.Functions()) != 480 {
		t.Fatalf("wrong structural scaling: stacks=%d samples=%d functions=%d", len(tree.Paths()), tree.SampleCount(), len(tree.Functions()))
	}
	for _, sample := range tree.Samples {
		if sample.WeightMs != 1 || sample.Untimed {
			t.Fatal("scaling sample must have a usable timestamp and one millisecond weight")
		}
	}
}
