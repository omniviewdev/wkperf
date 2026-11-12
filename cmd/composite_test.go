package cmd

import (
	"encoding/json"
	"math"
	"path/filepath"
	"strings"
	"testing"

	"github.com/omniviewdev/wkperf/internal/insights"
	"github.com/omniviewdev/wkperf/internal/metrics"
	"github.com/omniviewdev/wkperf/internal/query"
	"github.com/omniviewdev/wkperf/internal/testgen"
)

func TestStalledCompositesReachAnalyzeAndQueryOutputs(t *testing.T) {
	file := filepath.Join(t.TempDir(), "stalled.json")
	writeFlameJSON(t, file, testgen.StalledComposites())

	out, _, err := runPipeline(t, "analyze", file, "--format", "json")
	if err != nil {
		t.Fatal(err)
	}
	var document struct {
		Reports []struct {
			Breakdown []insights.Slice          `json:"breakdown"`
			Stalled   metrics.StalledComposites `json:"stalledComposites"`
		} `json:"reports"`
	}
	if err := json.Unmarshal([]byte(out), &document); err != nil || len(document.Reports) != 1 {
		t.Fatalf("invalid analysis output: %v %.400s", err, out)
	}
	report := document.Reports[0]
	if want := (metrics.StalledComposites{ThresholdMs: 50, Count: 2, TotalMs: 26000}); report.Stalled != want {
		t.Errorf("stalledComposites = %+v, want %+v", report.Stalled, want)
	}
	for _, slice := range report.Breakdown {
		if slice.Name == "Composite" && math.Abs(slice.Ms-55) > 1e-6 {
			t.Errorf("Composite slice = %gms; stalled spans must not count", slice.Ms)
		}
	}

	for _, format := range []string{"term", "md"} {
		text, _, err := runPipeline(t, "analyze", file, "--format", format, "--no-color")
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(text, "Stalled composite records: 2 longer than 50ms (26000ms of reported spans, summed)") {
			t.Errorf("%s analysis lacks the stalled composite line:\n%.1500s", format, text)
		}
	}

	for key, want := range map[string]float64{"layout.composite.stalled_count": 2, "layout.composite.stalled_ms": 26000} {
		raw, _, err := runPipeline(t, "query", file, "metrics", key, "--format", "json")
		var result queryOutput[query.ValueRow]
		if err != nil || json.Unmarshal([]byte(raw), &result) != nil || len(result.Rows) != 1 || result.Rows[0].Value != want {
			t.Errorf("query metrics %s = %v (%v) %.300s", key, result.Rows, err, raw)
		}
	}
}
