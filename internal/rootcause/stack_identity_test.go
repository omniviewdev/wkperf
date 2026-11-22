package rootcause

import (
	"crypto/sha256"
	"encoding/hex"
	"reflect"
	"strconv"
	"testing"

	"github.com/omniviewdev/wkperf/internal/flame"
	"github.com/omniviewdev/wkperf/internal/testgen"
)

func TestCachedStackIdentityMatchesOriginal(t *testing.T) {
	rec := testgen.FlameTiny()
	target := &rec.Samples[0]
	duplicate := target.StackTraces[2]
	target.StackTraces = append(target.StackTraces, duplicate)
	target.Durations = append(target.Durations, target.Durations[2])
	duplicate.Timestamp = 0
	target.StackTraces = append(target.StackTraces, duplicate)
	target.Durations = append(target.Durations, target.Durations[2])
	tree := flame.Build(rec)
	if len(tree.Untimed) != 1 {
		t.Fatalf("fixture has %d untimed samples, want 1", len(tree.Untimed))
	}
	focus := flame.FunctionID(tree.Paths()[0].Frames[0])
	views := []*flame.Tree{tree, tree.Window(1, 4), tree.Inverted(), tree.FocusFunction(focus), tree.Inverted().FocusFunction(focus)}
	for _, view := range views {
		if view.SampleCount() == 0 {
			t.Fatal("fixture view has no samples")
		}
		b := builder{tree: view, pathKeys: map[string]string{}, pathIDs: map[string]string{}}
		for _, path := range view.Paths() {
			want := originalStackKey(path.Frames)
			if got := stackKey(path.Frames); got != want {
				t.Errorf("stack %s: key = %q, want %q", path.ID, got, want)
			}
			b.pathKeys[path.ID], b.pathIDs[want] = want, path.ID
		}
		keep := func(s flame.Sample) bool { return !s.Untimed && s.WeightMs >= 5 }
		var all, filtered []string
		var samples, selected []flame.Sample
		view.EachSample(func(s flame.Sample) {
			key := originalStackKey(s.Frames)
			signature := digest(s.TargetID, decimal(s.Timestamp), strconv.FormatFloat(s.WeightMs, 'g', -1, 64), strconv.FormatBool(s.Untimed), key)
			id := digest(signature, strconv.Itoa(len(all)))
			all = append(all, id)
			samples = append(samples, s)
			if keep(s) {
				filtered = append(filtered, id)
				selected = append(selected, s)
			}
		})
		if got := SampleIDs(view, nil); !reflect.DeepEqual(got, all) {
			t.Fatalf("sample IDs = %v, want %v", got, all)
		}
		if got := SampleIDs(view, keep); !reflect.DeepEqual(got, filtered) {
			t.Fatalf("filtered sample IDs = %v, want %v", got, filtered)
		}
		uncached := b.stacks(samples, "test")
		b.indexSamples()
		if got := b.sampleIDs(selected); !reflect.DeepEqual(got, filtered) {
			t.Fatalf("indexed sample IDs = %v, want %v", got, filtered)
		}
		if got := b.stacks(samples, "test"); !reflect.DeepEqual(got, uncached) {
			t.Fatalf("cached stack aggregation differs from uncached aggregation: got %v, want %v", got, uncached)
		}
	}
}

func originalStackKey(frames []flame.Frame) string {
	h := sha256.New()
	for _, f := range frames {
		h.Write([]byte(flame.FunctionID(f)))
		h.Write([]byte{0})
	}
	return hex.EncodeToString(h.Sum(nil)[:12])
}
