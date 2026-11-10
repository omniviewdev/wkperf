package schema

import (
	"encoding/json"
	"testing"

	"github.com/omniviewdev/wkperf/internal/audit"
	"github.com/omniviewdev/wkperf/internal/flame"
	"github.com/omniviewdev/wkperf/internal/insights"
	"github.com/omniviewdev/wkperf/internal/rootcause"
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

func TestDocumentValidatesNewAttributionFacetsWithoutEmbeddedFrames(t *testing.T) {
	r := insights.Analyze(testgen.DeepStacks(10, 2, 40), "deep.json", insights.Options{Top: 3})
	frame := r.FlameTree.Functions()[0].Frame
	node := rootcause.PathNode{Frame: frame, TotalMs: 10, Samples: 5, Share: 1}
	r.RootCause.HotPath.Frames = []flame.Frame{frame}
	r.RootCause.HotPath.Nodes = []rootcause.PathNode{node}
	r.RootCause.HotPath.Branches = []rootcause.PathNode{node}
	r.RootCause.HotPath.BranchesTotal = 1
	r.RootCause.HotPath.StopReason = "near-tie"
	finding := insights.Finding{ID: "depth-facet", Title: "Depth facet", Severity: insights.SevInfo, Depth: &rootcause.DepthFacets{MaxDepth: 40, RepeatedFrame: &rootcause.RepeatedFrame{Frame: frame, Count: 40}}, NativeReads: []rootcause.NativeRead{{API: "getAnimations", Flush: "style", Samples: 5, StyleSamples: 5, StyleMs: 10}}, RelatedFindings: []string{"style-recalc-heavy"}}
	r.Findings = append(r.Findings, finding)
	reports := []any{
		InsightsDocument{Schema: InsightsVersion, Reports: []Analysis{{Report: r}}},
		AuditDocument{Schema: AuditVersion, Report: audit.Build([]audit.Scene{{Name: "edit", Runs: []audit.Run{{Report: r}}}}, audit.Options{})},
	}
	for _, document := range reports {
		payload, err := json.Marshal(document)
		if err != nil {
			t.Fatal(err)
		}
		var value any
		if err := json.Unmarshal(payload, &value); err != nil {
			t.Fatal(err)
		}
		if err := Validate(Document(), value); err != nil {
			t.Fatal(err)
		}
	}
	wire := insights.ToWire(r)
	if wire.RootCause.HotPath.Nodes[0].Frame < 0 || wire.Findings[len(wire.Findings)-1].Depth.RepeatedFrame.Frame < 0 {
		t.Fatal("facet has invalid frame index")
	}
	definitions := Document()["$defs"].(map[string]any)
	for _, name := range []string{"rootcause_PathNode", "rootcause_DepthFacets", "rootcause_RepeatedFrame"} {
		if _, exists := definitions[name]; exists {
			t.Errorf("schema leaks embedded frame shape %s", name)
		}
	}
}
