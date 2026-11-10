package audit

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"github.com/omniviewdev/wkperf/internal/flame"
	"github.com/omniviewdev/wkperf/internal/insights"
	"github.com/omniviewdev/wkperf/internal/rootcause"
)

func TestPriorityPreservesSelectedSceneFacets(t *testing.T) {
	frame := flame.Frame{Name: "walk", URL: "src/walk.ts", Line: 1}
	finding := insights.Finding{ID: "deep-stack", Title: "Depth", Severity: insights.SevInfo, ImpactMs: 20, Depth: &rootcause.DepthFacets{MaxDepth: 40, RepeatedFrame: &rootcause.RepeatedFrame{Frame: frame, Count: 40}}, NativeReads: []rootcause.NativeRead{{API: "getAnimations", Flush: "style", Samples: 8, StyleSamples: 8}, {API: "getBoundingClientRect", Flush: "layout", Samples: 5, LayoutSamples: 5}}, RelatedFindings: []string{"hot-function"}}
	report := Build([]Scene{{Name: "edit", Runs: []Run{{Report: &insights.Report{Top: 1, Source: "edit.json", Findings: []insights.Finding{finding}}}}}}, Options{})
	selected := SelectEvidence(report)
	if len(selected.Priorities) != 1 {
		t.Fatal("missing priority")
	}
	payload, err := json.Marshal(selected)
	if err != nil {
		t.Fatal(err)
	}
	var wire map[string]any
	if err := json.Unmarshal(payload, &wire); err != nil {
		t.Fatal(err)
	}
	priority := wire["priorities"].([]any)[0].(map[string]any)
	if priority["nativeReadsTotal"] != float64(2) || len(priority["nativeReads"].([]any)) != 1 {
		t.Fatalf("facet caps lost: %v", priority)
	}
	depth, ok := priority["depth"].(map[string]any)
	if !ok || depth["maxDepth"] != float64(40) {
		t.Fatalf("depth lost: %v", priority)
	}
	repeated := depth["repeatedFrame"].(map[string]any)
	if repeated["id"] != flame.FunctionID(frame) || repeated["frame"] != nil {
		t.Fatalf("priority repeats frame instead of scene-scoped identity: %v", repeated)
	}
	if priority["evidenceSceneIndex"] != float64(0) || len(priority["relatedFindings"].([]any)) != 1 {
		t.Fatalf("facet scene/link lost: %v", priority)
	}
	for _, render := range []func(*bytes.Buffer) error{func(b *bytes.Buffer) error { return RenderMarkdown(b, report) }, func(b *bytes.Buffer) error { return RenderTerm(b, report, false) }} {
		var out bytes.Buffer
		if err := render(&out); err != nil {
			t.Fatal(err)
		}
		for _, want := range []string{"getAnimations", "style flush", "Depth facets", "hot-function"} {
			if !strings.Contains(out.String(), want) {
				t.Errorf("missing %s", want)
			}
		}
	}
}
