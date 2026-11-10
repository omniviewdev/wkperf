package audit

import (
	"encoding/json"
	"testing"

	"github.com/omniviewdev/wkperf/internal/insights"
	"github.com/omniviewdev/wkperf/internal/testgen"
)

func TestAuditEvidenceUsesSceneCatalogReferences(t *testing.T) {
	report := insights.Analyze(testgen.DeepStacks(400, 80, 200), "deep.json", insights.Options{})
	r := Build([]Scene{{Name: "deep scene", Runs: []Run{{Source: report.Source, Report: report}}}}, Options{})
	payload, err := json.Marshal(r)
	if err != nil {
		t.Fatal(err)
	}
	var wire map[string]any
	if err := json.Unmarshal(payload, &wire); err != nil {
		t.Fatal(err)
	}
	scenes := wire["scenes"].([]any)
	analysis := scenes[0].(map[string]any)["analysis"].(map[string]any)
	stacks := analysis["stacks"].(map[string]any)
	for _, raw := range wire["priorities"].([]any) {
		priority := raw.(map[string]any)
		if priority["evidenceSceneIndex"] != float64(0) {
			t.Fatal("priority stack reference lacks unambiguous scene index")
		}
		if _, exists := priority["stacks"]; exists {
			t.Fatal("audit priority repeats exact scene evidence")
		}
		ids, ok := priority["stackIds"].([]any)
		if !ok {
			t.Fatalf("audit priority omits stack references: %v", priority["id"])
		}
		for _, rawID := range ids {
			if _, exists := stacks[rawID.(string)]; !exists {
				t.Errorf("priority references missing scene stack: %s", rawID)
			}
		}
	}
	for _, raw := range wire["hotspots"].([]any) {
		hotspot := raw.(map[string]any)
		if hotspot["evidenceSceneIndex"] != float64(0) {
			t.Fatal("hotspot stack reference lacks unambiguous scene index")
		}
		if _, exists := hotspot["commonStack"]; exists {
			t.Fatal("audit hotspot repeats exact scene stack")
		}
		if hotspot["commonStackScene"] != "deep scene" {
			t.Fatal("audit hotspot reference lacks scene scope")
		}
		if _, exists := stacks[hotspot["stackId"].(string)]; !exists {
			t.Fatal("audit hotspot references missing scene stack")
		}
	}
}
