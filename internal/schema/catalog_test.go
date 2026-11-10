package schema

import (
	"encoding/json"
	"testing"

	"github.com/omniviewdev/wkperf/internal/insights"
	"github.com/omniviewdev/wkperf/internal/scorecard"
	"github.com/omniviewdev/wkperf/internal/sourcemap"
	"github.com/omniviewdev/wkperf/internal/testgen"
)

func TestDocumentValidatesInternedEvidenceAndMetadata(t *testing.T) {
	rec := testgen.DeepStacks(400, 80, 200)
	r := insights.Analyze(rec, "deep.json", insights.Options{Top: 3})
	output := InsightsDocument{Schema: InsightsVersion, Reports: []Analysis{{Report: r, Scorecard: scorecard.Evaluate(rec, r), SourceMap: &sourcemap.Diagnostics{}}}}
	payload, err := json.Marshal(output)
	if err != nil {
		t.Fatal(err)
	}
	var value map[string]any
	if err := json.Unmarshal(payload, &value); err != nil {
		t.Fatal(err)
	}
	report := value["reports"].([]any)[0].(map[string]any)
	for _, name := range []string{"scorecard", "sourceMap", "frames", "stacks"} {
		if report[name] == nil {
			t.Fatalf("interned marshaler swallowed sibling %s", name)
		}
	}
	if err := Validate(Document(), value); err != nil {
		t.Fatal(err)
	}
	for _, raw := range report["stacks"].(map[string]any) {
		stack := raw.(map[string]any)
		stack["frames"] = []any{"not-an-index"}
		break
	}
	if err := Validate(Document(), value); err == nil {
		t.Fatal("wire schema accepts frame objects/strings instead of integer references")
	}
}

func TestDocumentOmitsReplacedEvidenceShapes(t *testing.T) {
	definitions := Document()["$defs"].(map[string]any)
	for _, name := range []string{"rootcause_Report", "rootcause_Path", "rootcause_Attribution", "rootcause_PeriodicRoot"} {
		if _, exists := definitions[name]; exists {
			t.Errorf("schema still exposes replaced embedded-frame shape %s", name)
		}
	}
}
