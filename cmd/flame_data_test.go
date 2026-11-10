package cmd

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	core "github.com/omniviewdev/wkperf/internal/flame"
	"github.com/omniviewdev/wkperf/internal/model"
	"github.com/omniviewdev/wkperf/internal/testgen"
)

func TestFlameFocusMergesFunctionContexts(t *testing.T) {
	tree := core.Build(testgen.FlameTiny())
	for _, tc := range []struct {
		name  string
		total float64
		paths int
	}{{"A", 50, 5}, {"B", 51, 3}} {
		focused, err := focusFlameTree(tree, tc.name)
		if err != nil {
			t.Errorf("focus %s rejects contexts of one function: %v", tc.name, err)
			continue
		}
		if focused.Root.TotalMs != tc.total || len(focused.Paths()) != tc.paths {
			t.Errorf("focus %s = %g ms, %d paths", tc.name, focused.Root.TotalMs, len(focused.Paths()))
		}
	}
	var id string
	for _, function := range tree.Functions() {
		if function.Frame.Name == "B" {
			id = function.ID
		}
	}
	focused, err := focusFlameTree(tree, id)
	if err != nil || focused.Root.TotalMs != 51 {
		t.Errorf("function ID focus did not merge: %v", err)
	}
	for _, node := range tree.Root.Children[0].Children {
		if node.Frame.Name != "B" {
			continue
		}
		context, err := focusFlameTree(tree, node.ID)
		if err != nil || context.Root.TotalMs != 30 {
			t.Fatalf("exact node focus changed its calling context: %v", err)
		}
	}
	_, err = focusFlameTree(tree, "/^[AB]$/")
	if err == nil || !strings.Contains(err.Error(), "2 functions") || strings.Count(err.Error(), "f_") != 2 || strings.Contains(err.Error(), "n_") {
		t.Errorf("ambiguity must list two distinct function candidates: %v", err)
	}
}

func TestFlameDiffFocusMergesBothSides(t *testing.T) {
	before, after, err := focusFlameDiff(core.Build(testgen.FlameTiny()), core.Build(testgen.FlameTiny()), "A")
	if err != nil || before.Root.TotalMs != 50 || after.Root.TotalMs != 50 {
		t.Fatalf("diff focus rejected repeated function: %v", err)
	}
}

func writeFlameRecording(t *testing.T, dir, name string, rec *model.Recording) string {
	t.Helper()
	payload, err := json.Marshal(rec)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, payload, 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestFlameCLIAutoAndNoneNormalization(t *testing.T) {
	dir := t.TempDir()
	rec := testgen.FlameTiny()
	baseline := writeFlameRecording(t, dir, "base.json", rec)
	rec.EndTime += 10
	target := &rec.Samples[0]
	for _, trace := range append([]model.StackTrace(nil), target.StackTraces...) {
		trace.Timestamp += 10
		target.StackTraces = append(target.StackTraces, trace)
	}
	target.Durations = append(target.Durations, target.Durations...)
	candidate := writeFlameRecording(t, dir, "candidate.json", rec)
	for _, mode := range []string{"auto", "none", "per-second", "share"} {
		out, err := runFlame(t, "--diff", baseline, candidate, "--format", "json", "--normalize", mode)
		if err != nil {
			t.Errorf("normalization %s: %v", mode, err)
			continue
		}
		var document struct {
			Charts []struct {
				Comparison struct {
					BeforeTotalMs float64 `json:"beforeTotalMs"`
					AfterTotalMs  float64 `json:"afterTotalMs"`
					TotalMs       float64 `json:"totalMs"`
					Normalization struct {
						Mode           string  `json:"mode"`
						Unit           string  `json:"unit"`
						BeforeActiveMs float64 `json:"beforeActiveMs"`
						AfterActiveMs  float64 `json:"afterActiveMs"`
					} `json:"normalization"`
				} `json:"comparison"`
			} `json:"charts"`
		}
		if err := json.Unmarshal([]byte(out), &document); err != nil {
			t.Fatal(err)
		}
		comparison := document.Charts[0].Comparison
		want := 0.0
		if mode == "none" {
			want = 80
		}
		if comparison.TotalMs != want || comparison.BeforeTotalMs != 80 || comparison.AfterTotalMs != 160 || comparison.Normalization.BeforeActiveMs != 10000 || comparison.Normalization.AfterActiveMs != 20000 {
			t.Errorf("normalization %s lost raw context or delta: %+v", mode, comparison)
		}
		if !strings.Contains(out, "duration-mismatch") {
			t.Errorf("%s lacks duration warning", mode)
		}
	}
	if _, err := runFlame(t, "--diff", baseline, candidate, "--normalize", "invalid", "--out", "-"); err == nil {
		t.Fatal("accepted unknown normalization mode")
	}
}
