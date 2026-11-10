package flame

import (
	"testing"

	"github.com/omniviewdev/wkperf/internal/model"
)

func TestHotPathDoesNotChooseTiedLeaf(t *testing.T) {
	var stacks [][]model.StackFrame
	var weights []float64
	for i := range 10 {
		leaf := "a"
		if i >= 5 {
			leaf = "b"
		}
		stacks = append(stacks, []model.StackFrame{frame(leaf, 2), frame("root", 1)})
		weights = append(weights, .001)
	}
	tree := Build(recording(stacks, weights))
	result := tree.SupportedHotPath(.05, 5, .8)
	if got := names(result.Nodes); got != "root" {
		t.Fatalf("unsupported tie resolution picked %q, want supported root", got)
	}
	if result.StopReason != "near-tie" || names(result.Branches) != "a;b" {
		t.Fatalf("tie alternatives: %+v", result)
	}
	if names(tree.HotPath(.05)) != "root;a" {
		t.Fatal("supported presentation changed the raw greedy API")
	}
}

func TestSupportedHotPathSupportAndShareOrder(t *testing.T) {
	stacks := [][]model.StackFrame{{frame("noise", 2), frame("root", 1)}}
	weights := []float64{.050}
	for range 9 {
		stacks = append(stacks, []model.StackFrame{frame("root", 1)})
		weights = append(weights, .001)
	}
	tree := Build(recording(stacks, weights))
	result := tree.SupportedHotPath(.05, 5, .8)
	if names(result.Nodes) != "root" || result.StopReason != "sample-support" || names(result.Branches) != "noise" {
		t.Fatalf("single-sample terminal accepted: %+v", result)
	}
	near(t, result.Nodes[0].TotalMs, 59)
	if result.Nodes[0].Samples != 10 || result.Branches[0].Samples != 1 {
		t.Fatalf("context support changed: %+v", result)
	}
	result = tree.SupportedHotPath(.9, 5, .8)
	if names(result.Nodes) != "root" || result.StopReason != "min-share" {
		t.Fatalf("sample check took precedence over share: %+v", result)
	}
	sparse := Build(goldenRecording()).SupportedHotPath(0, 5, .8)
	if len(sparse.Nodes) != 0 || sparse.StopReason != "sample-support" || names(sparse.Branches) != "main" {
		t.Fatalf("insufficient root support accepted: %+v", sparse)
	}
	for _, minShare := range []float64{0, .05, .9} {
		got := tree.SupportedHotPath(minShare, 0, 0)
		if names(got.Nodes) != names(tree.HotPath(minShare)) {
			t.Fatalf("disabled gates changed greedy path: %+v", got)
		}
	}
}

func TestSupportedHotPathKeepsThresholdEquality(t *testing.T) {
	tree := Build(goldenRecording())
	equal := tree.SupportedHotPath(.2, 0, 0)
	if names(equal.Nodes) != "main;work;render" || equal.StopReason != "leaf" || len(equal.Branches) != 0 {
		t.Fatalf("threshold equality stopped early: %+v", equal)
	}
	above := tree.SupportedHotPath(.200001, 0, 0)
	if names(above.Nodes) != "main;work" || above.StopReason != "min-share" || names(above.Branches) != "render" {
		t.Fatalf("threshold stop lost branches: %+v", above)
	}
}

func TestSupportedHotPathListsAllCompetingBranches(t *testing.T) {
	var stacks [][]model.StackFrame
	var weights []float64
	for _, branch := range []struct {
		name   string
		weight float64
	}{{"z", .001}, {"a", .0008}, {"m", .0002}} {
		for range 5 {
			stacks = append(stacks, []model.StackFrame{frame(branch.name, 2), frame("root", 1)})
			weights = append(weights, branch.weight)
		}
	}
	tree := Build(recording(stacks, weights))
	childOrder := names(tree.Root.Children[0].Children)
	result := tree.SupportedHotPath(.05, 5, .8)
	if names(result.Nodes) != "root" || result.StopReason != "near-tie" || names(result.Branches) != "z;a;m" {
		t.Fatalf("near-tie or competing branch order wrong: %+v", result)
	}
	if names(tree.Root.Children[0].Children) != childOrder {
		t.Fatal("branch presentation mutated canonical child order")
	}
	result = tree.SupportedHotPath(.05, 5, .800001)
	if names(result.Nodes) != "root;z" || result.StopReason != "leaf" {
		t.Fatalf("below-ratio sibling blocked descent: %+v", result)
	}
}

func TestSupportedHotPathEmptyAndViewEvidence(t *testing.T) {
	for _, tree := range []*Tree{nil, Build(nil)} {
		result := tree.SupportedHotPath(.05, 5, .8)
		if result.StopReason != "no-samples" || len(result.Nodes) != 0 || len(result.Branches) != 0 {
			t.Fatalf("empty path metadata: %+v", result)
		}
	}
	tree := Build(goldenRecording())
	for _, view := range []*Tree{tree, tree.Window(0, 1), tree.Inverted(), tree.Inverted().Canonical()} {
		result := view.SupportedHotPath(0, 0, 0)
		if names(result.Nodes) != names(view.HotPath(0)) {
			t.Fatal("shared helper disagrees with raw context selection")
		}
		for _, node := range result.Nodes {
			if node.TotalMs <= 0 || node.Samples <= 0 || view.byID[node.ID] != node {
				t.Fatalf("path lost exact per-context evidence: %+v", node)
			}
		}
	}
}
