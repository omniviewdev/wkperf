package scorecard

import (
	"reflect"
	"strings"
	"testing"

	"github.com/omniviewdev/wkperf/internal/insights"
	"github.com/omniviewdev/wkperf/internal/metrics"
	"github.com/omniviewdev/wkperf/internal/testgen"
)

func TestCompositeCadenceNeverFillsFrameStandardsOrTheGrade(t *testing.T) {
	rec := testgen.CompositeCadenceRuns()
	rep := insights.Analyze(rec, "cadence.json", insights.Options{})
	if rep.CompositeCadence == nil {
		t.Fatal("fixture must carry the estimate")
	}
	card := Evaluate(rec, rep)
	for _, id := range []string{"frame-p95", "jank-ratio", "fps"} {
		if r := get(t, card, id); r.Rating != Unavailable {
			t.Errorf("%s was rated from an estimate: %+v", id, r)
		}
	}
	if card.Grade != "n/a" || !card.Partial {
		t.Errorf("a capture without rendering frames has no grade: %+v", card)
	}

	// Removing every trace of the estimate must leave the scorecard unchanged.
	var kept []metrics.Metric
	for _, m := range rep.Summary.Metrics {
		if !strings.HasPrefix(m.Key, "cadence.") {
			kept = append(kept, m)
		}
	}
	if len(kept) == len(rep.Summary.Metrics) {
		t.Fatal("fixture carries no cadence metrics to remove")
	}
	rep.CompositeCadence, rep.Summary.CompositeCadence, rep.Summary.Metrics = nil, nil, kept
	if without := Evaluate(rec, rep); !reflect.DeepEqual(card, without) {
		t.Errorf("the estimate changed the scorecard:\nwith    %+v\nwithout %+v", card, without)
	}
}
