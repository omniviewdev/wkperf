package schema

import (
	"bytes"
	"encoding/json"
	"testing"

	core "github.com/omniviewdev/wkperf/internal/flame"
	flamereport "github.com/omniviewdev/wkperf/internal/report/flame"
	"github.com/omniviewdev/wkperf/internal/testgen"
)

func TestFlameV2SchemaMatchesFlatProfileAndDiff(t *testing.T) {
	tree := core.Build(testgen.FlameTiny())
	for _, charts := range [][]flamereport.Chart{
		{{Label: "profile", Tree: tree}},
		{{Label: "diff", Before: tree, After: tree, Comparison: core.Diff(tree, tree)}},
	} {
		var out bytes.Buffer
		if err := flamereport.Render(&out, "json", charts, flamereport.Options{}); err != nil {
			t.Fatal(err)
		}
		var document map[string]any
		if err := json.Unmarshal(out.Bytes(), &document); err != nil {
			t.Fatal(err)
		}
		if err := Validate(Document(), document); err != nil {
			t.Fatalf("rendered flame v2 schema mismatch: %v", err)
		}
		chart := document["charts"].([]any)[0].(map[string]any)
		field := "tree"
		if chart["comparison"] != nil {
			field = "comparison"
		}
		nodes := chart[field].(map[string]any)["nodes"].([]any)
		node := nodes[1].(map[string]any)
		node["frame"] = map[string]any{"id": "embedded-frame"}
		if err := Validate(Document(), document); err == nil {
			t.Fatal("flame schema accepts embedded frame objects instead of integer refs")
		}
	}
	definitions := Document()["$defs"].(map[string]any)
	for _, obsolete := range []string{"flame_Tree", "flame_Node", "flame_Sample", "flame_Comparison", "report_flame_Chart"} {
		if _, exists := definitions[obsolete]; exists {
			t.Errorf("flame schema still publishes recursive private shape %s", obsolete)
		}
	}
	for name, fields := range map[string][]string{
		"report_flame_WireTree":   {"nodes", "samples", "untimed", "activeIntervals", "activeDurationMs"},
		"flame_DiffNormalization": {"mode", "unit", "beforeTotalMs", "afterTotalMs", "beforeActiveMs", "afterActiveMs", "warnings"},
	} {
		properties := definitions[name].(map[string]any)["properties"].(map[string]any)
		for _, field := range fields {
			if properties[field].(map[string]any)["description"] == nil {
				t.Errorf("flame schema does not document %s.%s", name, field)
			}
		}
	}
}
