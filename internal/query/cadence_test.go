package query

import (
	"math"
	"strings"
	"testing"

	"github.com/omniviewdev/wkperf/internal/model"
	"github.com/omniviewdev/wkperf/internal/testgen"
)

func TestCompositeCadenceMetricsAreQueryable(t *testing.T) {
	engine := engineFor(testgen.CompositeCadenceRuns())
	for key, want := range map[string]float64{
		"cadence.composite.starts":             23,
		"cadence.composite.runs":               2,
		"cadence.composite.count":              20,
		"cadence.composite.p50_ms":             17,
		"cadence.composite.p95_ms":             61.5,
		"cadence.composite.max_ms":             90,
		"cadence.composite.missed_vsync_count": 5,
		"cadence.composite.missed_vsync_ratio": .25,
		"cadence.composite.over_50ms_count":    2,
		"cadence.composite.active_run_s":       .5,
	} {
		result := run(t, engine, Request{Operation: "metrics", Selector: key})
		if len(result.Rows) != 1 || math.Abs(result.Rows[0].Value-want) > 1e-6 {
			t.Errorf("%s = %+v; want %g", key, result.Rows, want)
		}
	}
	if rows := run(t, engine, Request{Operation: "metrics", Selector: "cadence.composite.*"}).Rows; len(rows) != 10 {
		t.Errorf("cadence.composite.* returned %d rows, want 10", len(rows))
	}
}

func TestCompositeCadenceKeysAreLabeledEstimated(t *testing.T) {
	result := run(t, engineFor(testgen.CompositeCadenceRuns()), Request{Operation: "keys", Selector: "cadence."})
	if len(result.Rows) != 10 {
		t.Fatalf("cadence keys = %d, want 10: %+v", len(result.Rows), result.Rows)
	}
	for _, row := range result.Rows {
		if row.Basis != "estimated" || row.Operation != "metrics" || row.Unit == "" || !strings.HasSuffix(row.Description, "(estimate)") {
			t.Errorf("cadence key must be an estimate: %+v", row)
		}
	}
	// Measured scalars keep their basis.
	other := run(t, engineFor(testgen.CompositeCadenceRuns()), Request{Operation: "keys", Selector: "layout.composite.count"})
	if len(other.Rows) != 1 || other.Rows[0].Basis != "measured" {
		t.Errorf("layout keys must stay measured: %+v", other.Rows)
	}
}

func TestCompositeCadenceKeysAbsentWhenRenderingFramesExist(t *testing.T) {
	rec := testgen.CompositeCadenceRuns()
	rec.Records = append(rec.Records, model.Record{Type: model.TypeRenderingFrame, StartTime: 5, EndTime: 5.016})
	_, err := engineFor(rec).Run(t.Context(), Request{File: "framed.json", Operation: "keys", Selector: "cadence."})
	if err == nil {
		t.Fatal("a capture with rendering-frame records has no cadence keys")
	}
}
