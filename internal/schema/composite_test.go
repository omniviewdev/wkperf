package schema

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/omniviewdev/wkperf/internal/insights"
	"github.com/omniviewdev/wkperf/internal/model"
	"github.com/omniviewdev/wkperf/internal/scorecard"
	"github.com/omniviewdev/wkperf/internal/testgen"
)

func analysisValue(t *testing.T, rec *model.Recording) map[string]any {
	t.Helper()
	report := insights.Analyze(rec, "fixture.json", insights.Options{})
	data, err := json.Marshal(InsightsDocument{Schema: InsightsVersion, Reports: []Analysis{{Report: report, Scorecard: scorecard.Evaluate(rec, report)}}})
	if err != nil {
		t.Fatal(err)
	}
	var value map[string]any
	if err := json.Unmarshal(data, &value); err != nil {
		t.Fatal(err)
	}
	return value
}

func TestSchemaDocumentsStalledComposites(t *testing.T) {
	definitions := Document()["$defs"].(map[string]any)
	properties := definitions["metrics_StalledComposites"].(map[string]any)["properties"].(map[string]any)
	for field, unit := range map[string]string{"thresholdMs": "ms", "count": "count", "totalMs": "ms"} {
		property, ok := properties[field].(map[string]any)
		if !ok {
			t.Errorf("stalled composite schema lacks %s", field)
			continue
		}
		if property["x-unit"] != unit {
			t.Errorf("%s unit = %v, want %s", field, property["x-unit"], unit)
		}
		description, _ := property["description"].(string)
		if strings.Contains(description, "statistical") || !strings.Contains(description, "composite") {
			t.Errorf("%s inherited an unrelated shared meaning: %q", field, description)
		}
	}
	report := definitions["insights_Report"].(map[string]any)
	field, ok := report["properties"].(map[string]any)["stalledComposites"].(map[string]any)
	if !ok {
		t.Fatal("analysis report schema lacks stalledComposites")
	}
	if description, _ := field["description"].(string); !strings.Contains(description, "excluded") {
		t.Errorf("stalledComposites must say what happens to the spans: %q", description)
	}
	required, _ := report["required"].([]string)
	found := false
	for _, name := range required {
		found = found || name == "stalledComposites"
	}
	if !found {
		t.Error("stalledComposites must always be present so consumers can rely on it")
	}
}

func TestSchemaValidatesStalledCompositeAnalysis(t *testing.T) {
	value := analysisValue(t, testgen.StalledComposites())
	report := value["reports"].([]any)[0].(map[string]any)
	stalled, ok := report["stalledComposites"].(map[string]any)
	if !ok || stalled["count"] != float64(2) || stalled["totalMs"] != float64(26000) || stalled["thresholdMs"] != float64(50) {
		t.Fatalf("stalledComposites = %v", report["stalledComposites"])
	}
	if err := Validate(Document(), value); err != nil {
		t.Fatal(err)
	}
	stalled["count"] = "two"
	if err := Validate(Document(), value); err == nil {
		t.Fatal("schema accepts a non-numeric stalled composite count")
	}
	delete(report, "stalledComposites")
	if err := Validate(Document(), value); err == nil {
		t.Fatal("schema accepts an analysis without stalledComposites")
	}
}
