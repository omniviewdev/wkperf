package query

import (
	"encoding/json"
	"math"
	"testing"

	"github.com/omniviewdev/wkperf/internal/testgen"
)

func TestUntimedQueryWholeAndWindowMetadata(t *testing.T) {
	for _, tc := range []struct {
		name            string
		from, to, total float64
		excluded        bool
	}{
		{"whole", 0, 0, 80, false},
		{"explicit-whole", 0, 2, 80, false},
		{"partial", .1, .4, 0, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			result := run(t, engineFor(testgen.UntimedSamples()), Request{Operation: "top", From: tc.from, To: tc.to})
			if result.TotalMS != tc.total {
				t.Errorf("total_ms=%v, want %v", result.TotalMS, tc.total)
			}
			data, _ := json.Marshal(result.Sources[0])
			var source map[string]any
			_ = json.Unmarshal(data, &source)
			if source["untimed_samples"] != float64(40) || source["untimed_ms"] != float64(40) || source["untimed_excluded"] != tc.excluded {
				t.Fatalf("untimed source metadata: %s", data)
			}
			spans, ok := source["untimed_spans"].([]any)
			if !ok || len(spans) != 1 {
				t.Fatalf("missing inferred span: %s", data)
			}
			span := spans[0].(map[string]any)
			if span["from_sec"] != float64(0) || span["to_sec"] != float64(1) || span["samples"] != float64(40) || span["ms"] != float64(40) {
				t.Fatalf("span changed or clipped: %v", span)
			}
		})
	}
}

func TestUntimedQueryRoundedWholeWindow(t *testing.T) {
	rec := testgen.DecimalEnd()
	for _, to := range []float64{0, 17.628939583} {
		for _, request := range []Request{
			{Operation: "top", To: to},
			{Operation: "metrics", Selector: "samples.sampled_ms", To: to},
			{Operation: "rootcause", Selector: "samples.self_ms", To: to},
		} {
			result := run(t, engineFor(rec), request)
			if result.TotalMS != 80 || result.Sources[0].UntimedExcluded {
				t.Fatalf("rounded full window to=%v lost untimed weight: %+v", to, result)
			}
			if request.Selector != "" && (len(result.Rows) != 1 || result.Rows[0].Value != 80) {
				t.Fatalf("rounded %s did not reproduce 80 ms: %+v", request.Selector, result.Rows)
			}
		}
	}
}

func TestUntimedQueryMetrics(t *testing.T) {
	for key, want := range map[string]float64{
		"samples.count":               50,
		"samples.sampled_ms":          80,
		"samples.coverage_ratio":      80.0 / 140,
		"samples.untimed_count":       40,
		"samples.untimed_ms":          40,
		"samples.untimed_excluded_ms": 0,
	} {
		t.Run(key, func(t *testing.T) {
			result := run(t, engineFor(testgen.UntimedSamples()), Request{Operation: "metrics", Selector: key})
			if len(result.Rows) != 1 || math.Abs(result.Rows[0].Value-want) > 1e-9 {
				t.Fatalf("%s: %+v, want %v", key, result.Rows, want)
			}
		})
	}
}

func TestUntimedQueryPartialMetricExclusions(t *testing.T) {
	for key, want := range map[string]float64{
		"samples.count":               0,
		"samples.sampled_ms":          0,
		"samples.untimed_count":       40,
		"samples.untimed_ms":          40,
		"samples.untimed_excluded_ms": 40,
	} {
		t.Run(key, func(t *testing.T) {
			result := run(t, engineFor(testgen.UntimedSamples()), Request{Operation: "metrics", Selector: key, From: .1, To: .4})
			if len(result.Rows) != 1 || result.Rows[0].Value != want {
				t.Fatalf("%s: %+v, want %v", key, result.Rows, want)
			}
		})
	}
}

func TestUntimedQueryAggregateOrderStable(t *testing.T) {
	before, after := testgen.UntimedSamples(), testgen.UntimedSamples()
	target := &after.Samples[0]
	for i, j := 0, 39; i < j; i, j = i+1, j-1 {
		target.StackTraces[i], target.StackTraces[j] = target.StackTraces[j], target.StackTraces[i]
		target.Durations[i], target.Durations[j] = target.Durations[j], target.Durations[i]
	}
	for _, operation := range []string{"top", "paths", "stacks", "callers", "callees", "hot-path", "by-root", "depth"} {
		t.Run(operation, func(t *testing.T) {
			request := Request{Operation: operation}
			if operation == "paths" || operation == "callers" || operation == "callees" {
				request.Selector = "sharedLeaf"
			}
			a, _ := json.Marshal(run(t, engineFor(before), request))
			b, _ := json.Marshal(run(t, engineFor(after), request))
			if string(a) != string(b) {
				t.Fatalf("untimed sample order changes aggregate output: %s\n%s", a, b)
			}
		})
	}
}
