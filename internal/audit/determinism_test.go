package audit

import (
	"encoding/json"
	"testing"

	"github.com/omniviewdev/wkperf/internal/insights"
)

func TestAuditBreakdownDeterministic(t *testing.T) {
	parts := []insights.Slice{{Name: "Script", Pct: 50}, {Name: "Style", Pct: 3e-15}, {Name: "Layout", Pct: 3e-15}, {Name: "Paint", Pct: 3e-15}, {Name: "Composite", Pct: 3e-15}, {Name: "GC", Pct: 3e-15}, {Name: "Other", Pct: 3e-15}, {Name: "Idle", Pct: 50}}
	scenes := []SceneResult{{Name: "edit", DurationSec: 1, Breakdown: parts}}
	var first []byte
	for range 200 {
		payload, err := json.Marshal(mergeBreakdown(scenes))
		if err != nil {
			t.Fatal(err)
		}
		if first == nil {
			first = payload
		}
		if string(payload) != string(first) {
			t.Fatalf("identical scenes produced different breakdowns: %s != %s", payload, first)
		}
	}
}
