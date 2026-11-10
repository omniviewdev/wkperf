package schema

import (
	"strings"
	"testing"
)

func TestMeasurementSchemaDocumentsUnavailableGradesAndOverlays(t *testing.T) {
	definitions := Document()["$defs"].(map[string]any)
	for name, fields := range map[string][]string{
		"insights_Report":         {"gcMs", "gcOverlapWithWorkMs", "inputStalls", "inputStallsTotal"},
		"insights_SlowFrame":      {"workMs", "gcMs", "gcOverlapWithWorkMs"},
		"insights_LongTask":       {"children", "childrenTotal"},
		"scorecard_Scorecard":     {"coverage", "partial", "score", "grade"},
		"insights_WireRootReport": {"inputStalls", "inputStallsTotal"},
	} {
		definition, ok := definitions[name].(map[string]any)
		if !ok {
			t.Errorf("missing measurement schema %s", name)
			continue
		}
		properties := definition["properties"].(map[string]any)
		for _, field := range fields {
			property, ok := properties[field].(map[string]any)
			if !ok {
				t.Errorf("missing measurement property %s.%s", name, field)
				continue
			}
			if _, ok := property["description"].(string); !ok {
				t.Errorf("missing semantics for %s.%s", name, field)
			}
		}
	}
	card, ok := definitions["scorecard_Scorecard"].(map[string]any)
	if ok {
		properties := card["properties"].(map[string]any)
		if coverage, ok := properties["coverage"].(map[string]any); ok && (coverage["minimum"] != float64(0) || coverage["maximum"] != float64(1)) {
			t.Error("coverage is not bounded to [0,1]")
		}
		if score, ok := properties["score"].(map[string]any); ok {
			description, _ := score["description"].(string)
			if !strings.Contains(description, "partial") {
				t.Error("numeric score does not disclose partial semantics")
			}
		}
	}
}
