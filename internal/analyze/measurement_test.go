package analyze

import (
	"bytes"
	"encoding/json"
	"math/rand/v2"
	"testing"

	"github.com/omniviewdev/wkperf/internal/testgen"
)

func TestNestedTasksScalarMetrics(t *testing.T) {
	s := Summarize(testgen.NestedRecords(), "nested.json")
	for key, want := range map[string]float64{
		"script.tasks.count":                       3,
		"script.tasks.total_ms":                    456,
		"script.long_tasks.count":                  3,
		"script.long_tasks.total_ms":               456,
		"script.long_tasks.tbt_ms":                 306,
		"script.long_tasks.tbt_per_10s":            1530,
		"script.long_tasks.rate_per_min":           90,
		"layout.composite.total_ms":                10,
		"layout.composite.exclusive_ms":            2,
		"layout.paint.exclusive_ms":                6,
		"layout.recalculate-styles.exclusive_ms":   2,
		"script.microtask-dispatched.exclusive_ms": 371.7,
	} {
		if got := val(t, s, key); !approx(got, want) {
			t.Errorf("%s = %g, want %g", key, got, want)
		}
	}
}

func TestInputStallsExcludedFromFrameStandards(t *testing.T) {
	s := Summarize(testgen.InputStallFrames(), "input.json")
	for key, want := range map[string]float64{
		"frames.count":                   1,
		"frames.active_ms":               299.5,
		"frames.p95_ms":                  299.5,
		"frames.jank_ratio":              1,
		"frames.input_stalls":            1,
		"frames.input_stall_max_ms":      1200,
		"frames.input_stall_ms":          1200,
		"frames.observed_count":          2,
		"frames.observed_ms":             1499.5,
		"frames.jank_work_excess_ms":     183.3,
		"frames.jank_interval_excess_ms": 282.8,
	} {
		if got := val(t, s, key); !approx(got, want) {
			t.Errorf("%s = %g, want %g", key, got, want)
		}
	}
	if len(s.FrameMs) != 1 || !approx(s.FrameMs[0], 299.5) {
		t.Fatalf("chart series included input stall: %v", s.FrameMs)
	}
}

func TestCollectorMetricsAreSeparateOverlay(t *testing.T) {
	s := Summarize(testgen.GCOverlay(), "gc.json")
	for key, want := range map[string]float64{
		"frames.count":            0,
		"frames.active_ms":        0,
		"frames.idle_wakeups":     1,
		"gc.total_ms":             131.6,
		"gc.overlap_with_work_ms": 7,
		"script.total_ms":         7,
	} {
		if got := val(t, s, key); !approx(got, want) {
			t.Errorf("%s = %g, want %g", key, got, want)
		}
	}
}

func TestTaskMetricsClipUnionsBeforeApplyingFixedThreshold(t *testing.T) {
	rec := testgen.NestedRecords()
	s := SummarizeWith(rec, "nested.json", Options{FromSec: .11, ToSec: .31})
	for key, want := range map[string]float64{
		"script.tasks.count":             1,
		"script.tasks.total_ms":          200,
		"script.long_tasks.count":        1,
		"script.long_tasks.tbt_ms":       150,
		"script.long_tasks.tbt_per_10s":  7500,
		"script.long_tasks.rate_per_min": 300,
	} {
		if got := val(t, s, key); !approx(got, want) {
			t.Errorf("%s = %g, want %g", key, got, want)
		}
	}
}

func TestFrameAndTaskStandardsExposeFixedBudgets(t *testing.T) {
	s := Summarize(testgen.ThresholdFrames(), "threshold.json")
	for key, want := range map[string]float64{
		"frames.budget_ms":               16.7,
		"frames.count":                   2,
		"frames.active_ms":               650,
		"frames.jank_ratio":              1,
		"script.long_tasks.threshold_ms": 50,
		"script.long_tasks.count":        3,
	} {
		if got := val(t, s, key); !approx(got, want) {
			t.Errorf("%s = %g, want %g", key, got, want)
		}
	}
}

func TestScalarMeasurementDeterministicAcrossRecordShuffle(t *testing.T) {
	rec := testgen.ThresholdFrames()
	encode := func() []byte {
		t.Helper()
		data, err := json.Marshal(Summarize(rec, "shuffle.json"))
		if err != nil {
			t.Fatal(err)
		}
		return data
	}
	want := encode()
	random := rand.New(rand.NewPCG(12, 13))
	for range 20 {
		random.Shuffle(len(rec.Records), func(i, j int) { rec.Records[i], rec.Records[j] = rec.Records[j], rec.Records[i] })
		if got := encode(); !bytes.Equal(got, want) {
			t.Fatal("record shuffle changed scalar measurement")
		}
	}
}

func TestExclusiveFindingScalarsRetainInclusiveRecordTime(t *testing.T) {
	s := Summarize(testgen.ExclusiveFindingCosts(), "exclusive.json")
	for key, want := range map[string]float64{
		"layout.recalculate-styles.total_ms":     200,
		"layout.recalculate-styles.exclusive_ms": 100,
		"layout.layout.total_ms":                 100,
		"layout.layout.exclusive_ms":             100,
		"layout.paint.total_ms":                  100,
		"layout.paint.exclusive_ms":              50,
		"layout.composite.total_ms":              400,
		"layout.composite.exclusive_ms":          100,
		"script.timer-fired.exclusive_ms":        50,
	} {
		if got := val(t, s, key); !approx(got, want) {
			t.Errorf("%s = %g, want %g", key, got, want)
		}
	}
}
