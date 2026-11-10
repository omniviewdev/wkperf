package jsonout

import (
	"bytes"
	"encoding/json"
	"testing"

	"github.com/omniviewdev/wkperf/internal/compare"
	"github.com/omniviewdev/wkperf/internal/metrics"
)

func TestRenderRoundTrip(t *testing.T) {
	r := &compare.Result{
		Baseline: compare.Side{Label: "baseline", Sources: []string{"a.json"}, Runs: 1},
		Cand:     compare.Side{Label: "candidate", Sources: []string{"b.json"}, Runs: 2},
		Metrics: []compare.MetricDiff{{
			Metric: metrics.Metric{Key: "frames.p95_ms", Group: "frames"},
			Base:   10, Cand: 12, Pct: 0.2, Verdict: compare.Regressed,
			CandRuns: []float64{11, 13},
		}},
		BaseRep: &metrics.Summary{Source: "must-not-appear"},
	}
	var buf bytes.Buffer
	if err := Render(&buf, r); err != nil {
		t.Fatal(err)
	}
	var got map[string]any
	if err := json.Unmarshal(buf.Bytes(), &got); err != nil {
		t.Fatalf("invalid json: %v", err)
	}
	if got["schema"] != "wkperf/v1" {
		t.Fatalf("schema = %v", got["schema"])
	}
	ms, ok := got["metrics"].([]any)
	if !ok || len(ms) != 1 {
		t.Fatalf("metrics = %v", got["metrics"])
	}
	if got["candidate"].(map[string]any)["runs"].(float64) != 2 {
		t.Fatalf("candidate runs wrong: %v", got["candidate"])
	}
	if bytes.Contains(buf.Bytes(), []byte("must-not-appear")) {
		t.Fatal("representative summary leaked into output")
	}
}

func TestRenderEmptyAndNil(t *testing.T) {
	for _, r := range []*compare.Result{nil, {}} {
		var buf bytes.Buffer
		if err := Render(&buf, r); err != nil {
			t.Fatal(err)
		}
		if !json.Valid(buf.Bytes()) {
			t.Fatalf("invalid json: %s", buf.String())
		}
	}
}
