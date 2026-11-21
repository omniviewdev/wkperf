package cmd

import (
	"encoding/json"
	"math"
	"path/filepath"
	"strings"
	"testing"

	"github.com/omniviewdev/wkperf/internal/compare"
	"github.com/omniviewdev/wkperf/internal/metrics"
	"github.com/omniviewdev/wkperf/internal/query"
	"github.com/omniviewdev/wkperf/internal/testgen"
)

func TestCompositeCadenceReachesAnalyzeAndQueryOutputs(t *testing.T) {
	file := filepath.Join(t.TempDir(), "cadence.json")
	writeFlameJSON(t, file, testgen.CompositeCadenceRuns())

	out, _, err := runPipeline(t, "analyze", file, "--format", "json")
	if err != nil {
		t.Fatal(err)
	}
	var document struct {
		Reports []struct {
			Cadence   *metrics.CompositeCadence `json:"compositeCadence"`
			Scorecard struct {
				Grade string `json:"grade"`
			} `json:"scorecard"`
		} `json:"reports"`
	}
	if err := json.Unmarshal([]byte(out), &document); err != nil || len(document.Reports) != 1 {
		t.Fatalf("invalid analysis output: %v %.400s", err, out)
	}
	report := document.Reports[0]
	if c := report.Cadence; c == nil || c.Basis != "estimated" || c.Count != 20 || c.Runs != 2 || math.Abs(c.P95Ms-61.5) > 1e-6 {
		t.Errorf("compositeCadence = %+v", report.Cadence)
	}
	if report.Scorecard.Grade != "n/a" {
		t.Errorf("the estimate must not produce a grade: %q", report.Scorecard.Grade)
	}

	for _, format := range []string{"term", "md"} {
		text, _, err := runPipeline(t, "analyze", file, "--format", format, "--no-color")
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(text, "Composite cadence (estimate)") || !strings.Contains(text, "Basis: estimated.") {
			t.Errorf("%s analysis lacks the cadence section:\n%.1500s", format, text)
		}
	}

	raw, _, err := runPipeline(t, "query", file, "metrics", "cadence.composite.p95_ms", "--format", "json")
	var result queryOutput[query.ValueRow]
	if err != nil || json.Unmarshal([]byte(raw), &result) != nil || len(result.Rows) != 1 || math.Abs(result.Rows[0].Value-61.5) > 1e-6 {
		t.Errorf("query metrics cadence.composite.p95_ms = %v (%v) %.300s", result.Rows, err, raw)
	}

	raw, _, err = runPipeline(t, "query", file, "keys", "cadence.", "--format", "json")
	var keys queryOutput[query.KeyRow]
	if err != nil || json.Unmarshal([]byte(raw), &keys) != nil || len(keys.Rows) != 10 {
		t.Fatalf("query keys cadence. = %v (%v) %.300s", keys.Rows, err, raw)
	}
	for _, row := range keys.Rows {
		if row.Basis != "estimated" {
			t.Errorf("cadence key %s has basis %q", row.Key, row.Basis)
		}
	}
}

func TestCompositeCadenceRegressionIsNotGated(t *testing.T) {
	regressed := func(group string) *compare.Result {
		return &compare.Result{Metrics: []compare.MetricDiff{{
			Metric:     metrics.Metric{Key: group + ".metric", Group: group, Dir: metrics.LowerBetter},
			Verdict:    compare.Regressed,
			Confidence: "high",
		}}}
	}
	if n := confidentRegressions(regressed("cadence")); n != 0 {
		t.Errorf("an estimated cadence regression must not fail --fail-on-regression: %d", n)
	}
	if n := confidentRegressions(regressed("frames")); n != 1 {
		t.Errorf("control: a measured frame regression is gated: %d", n)
	}
}
