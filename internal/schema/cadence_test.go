package schema

import (
	"strings"
	"testing"

	"github.com/omniviewdev/wkperf/internal/testgen"
)

func TestSchemaDocumentsCompositeCadence(t *testing.T) {
	definitions := Document()["$defs"].(map[string]any)
	cadence := definitions["metrics_CompositeCadence"].(map[string]any)
	properties := cadence["properties"].(map[string]any)
	for field, unit := range map[string]string{
		"starts": "count", "runs": "count", "count": "count", "p50Ms": "ms", "p95Ms": "ms", "maxMs": "ms",
		"missedVsyncCount": "count", "missedVsyncRatio": "ratio", "over50MsCount": "count", "activeRunSec": "s",
	} {
		property, ok := properties[field].(map[string]any)
		if !ok {
			t.Errorf("composite cadence schema lacks %s", field)
			continue
		}
		if property["x-unit"] != unit {
			t.Errorf("%s unit = %v, want %s", field, property["x-unit"], unit)
		}
	}
	basis := properties["basis"].(map[string]any)
	if values, _ := basis["enum"].([]string); len(values) != 1 || values[0] != "estimated" {
		t.Errorf("cadence basis must always be estimated: %v", basis["enum"])
	}
	if description, _ := basis["description"].(string); !strings.Contains(description, "estimated") {
		t.Errorf("cadence basis description: %q", description)
	}

	report := definitions["insights_Report"].(map[string]any)
	field, ok := report["properties"].(map[string]any)["compositeCadence"].(map[string]any)
	if !ok {
		t.Fatal("analysis report schema lacks compositeCadence")
	}
	if description, _ := field["description"].(string); !strings.Contains(description, "never fills frame standards") {
		t.Errorf("compositeCadence must say it never feeds frame standards or grades: %q", description)
	}
	required, _ := report["required"].([]string)
	for _, name := range required {
		if name == "compositeCadence" {
			t.Error("compositeCadence exists only for captures without rendering-frame records")
		}
	}

	metric := definitions["metrics_Metric"].(map[string]any)
	metricBasis := metric["properties"].(map[string]any)["basis"].(map[string]any)
	if values, _ := metricBasis["enum"].([]string); len(values) != 2 || values[1] != "estimated" {
		t.Errorf("metric basis vocabulary: %v", metricBasis["enum"])
	}
	if required, _ := metric["required"].([]string); strings.Contains(strings.Join(required, ","), "basis") {
		t.Error("measured metrics omit basis")
	}
}

func TestSchemaValidatesCompositeCadenceAnalysis(t *testing.T) {
	value := analysisValue(t, testgen.CompositeCadenceRuns())
	report := value["reports"].([]any)[0].(map[string]any)
	cadence, ok := report["compositeCadence"].(map[string]any)
	if !ok || cadence["basis"] != "estimated" {
		t.Fatalf("compositeCadence = %v", report["compositeCadence"])
	}
	if err := Validate(Document(), value); err != nil {
		t.Fatal(err)
	}
	cadence["basis"] = "measured"
	if err := Validate(Document(), value); err == nil {
		t.Fatal("schema accepts a measured basis for the composite cadence estimate")
	}
	cadence["basis"] = "estimated"
	cadence["count"] = "twenty"
	if err := Validate(Document(), value); err == nil {
		t.Fatal("schema accepts a non-numeric interval count")
	}
}
