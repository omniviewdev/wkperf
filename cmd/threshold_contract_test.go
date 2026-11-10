package cmd

import (
	"encoding/json"
	"math"
	"path/filepath"
	"strings"
	"testing"

	"github.com/omniviewdev/wkperf/internal/insights"
	"github.com/omniviewdev/wkperf/internal/query"
	"github.com/omniviewdev/wkperf/internal/testgen"
)

func TestAnalyzeThresholdEvidenceReproducesThroughQuery(t *testing.T) {
	file := filepath.Join(t.TempDir(), "threshold '$.json")
	rec := testgen.ThresholdFrames()
	writeFlameJSON(t, file, rec)
	out, _, err := runPipeline(t, "analyze", file, "--format", "json", "--jank-ms", "325", "--long-task-ms", "100")
	if err != nil {
		t.Fatal(err)
	}
	var document struct {
		Reports []struct {
			Findings []insights.Finding `json:"findings"`
		} `json:"reports"`
	}
	if err := json.Unmarshal([]byte(out), &document); err != nil || len(document.Reports) != 1 {
		t.Fatalf("invalid analysis output: %v %.400s", err, out)
	}
	want := map[string]float64{"findings.jank.ratio": .5, "findings.jank.threshold_ms": 325}
	for _, finding := range document.Reports[0].Findings {
		for _, evidence := range finding.NumericEvidence {
			expected, ok := want[evidence.MetricKey]
			if !ok {
				continue
			}
			if math.Abs(evidence.Value-expected) > 1e-6 || !strings.Contains(evidence.Command, "--jank-ms 325") || !strings.Contains(evidence.Command, "--long-task-ms 100") {
				t.Errorf("threshold evidence lost its configuration: %+v", evidence)
			}
			args := outputCommandArgs(t, evidence.Command)
			reproduced, stderr, err := runPipeline(t, args[1:]...)
			var result query.Result
			if err != nil || json.Unmarshal([]byte(reproduced), &result) != nil || len(result.Rows) != 1 || math.Abs(result.Rows[0].Value-evidence.Value) > 1e-6 {
				t.Errorf("threshold query does not reproduce: %s: %v %s %.400s", evidence.Command, err, stderr, reproduced)
			}
			delete(want, evidence.MetricKey)
		}
	}
	if len(want) != 0 {
		t.Fatalf("missing explicit threshold evidence: %v", want)
	}
	fixed, _, err := runPipeline(t, "query", file, "metrics", "frames.jank_ratio", "--format", "json", "--jank-ms", "325")
	var result query.Result
	if err != nil || json.Unmarshal([]byte(fixed), &result) != nil || len(result.Rows) != 1 || result.Rows[0].Value != 1 {
		t.Fatalf("custom finding threshold changed fixed60Hz standard: %v %s", err, fixed)
	}
}
