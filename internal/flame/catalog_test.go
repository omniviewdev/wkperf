package flame

import "testing"

func TestCanonicalFrameAndStackIDs(t *testing.T) {
	tree := Build(goldenRecording())
	paths := tree.SamplePathIDs()
	for i, sample := range tree.Samples {
		if StackID(sample.Frames) != paths[i] {
			t.Fatalf("stack selector differs from query path: %s != %s", StackID(sample.Frames), paths[i])
		}
		for _, frame := range sample.Frames {
			if frame.ID == "" || frame.ID != FunctionID(frame) {
				t.Fatalf("generated frame lacks stable function selector: %+v", frame)
			}
		}
	}
	if StackID(nil) != "" {
		t.Fatal("empty stack should not claim a sampled context selector")
	}
}
