package insights

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/omniviewdev/wkperf/internal/flame"
	"github.com/omniviewdev/wkperf/internal/model"
	"github.com/omniviewdev/wkperf/internal/rootcause"
)

func TestCatalogPreservesConfiguredOwnership(t *testing.T) {
	rec := &model.Recording{StartTime: 0, EndTime: 1, Samples: []model.TargetSamples{{StackTraces: []model.StackTrace{{Timestamp: .1, StackFrames: []model.StackFrame{{Name: "utility", URL: "wails://localhost/assets/index.js", Line: 1}}}}, Durations: []float64{.02}}}}
	var opts Options
	if err := json.Unmarshal([]byte(`{"Ownership":{"libraryURLs":["**/index.js"]}}`), &opts); err != nil {
		t.Fatal(err)
	}
	report := Analyze(rec, "capture.json", opts)
	catalog := Catalog(report)
	for _, frame := range catalog.Frames {
		if frame.Category != "framework" {
			t.Fatalf("catalog overrides configured tree owner: %+v", frame)
		}
	}
	for _, function := range report.FlameTree.Functions() {
		if function.Frame.Category != "framework" {
			t.Fatalf("tree lost configured owner: %+v", function.Frame)
		}
	}
	payload, err := json.Marshal(report)
	if err != nil {
		t.Fatal(err)
	}
	for _, legacy := range []string{`"appAttribution"`, `"appAttributionTotal"`, `"app":`, `"rawSeverity"`} {
		if bytes.Contains(payload, []byte(legacy)) {
			t.Errorf("legacy attribution field remains: %s", legacy)
		}
	}
	if !bytes.Contains(payload, []byte(`"contextAttribution"`)) {
		t.Fatal("context attribution missing")
	}
}

func TestHotPathSurfaceShowsSupportBranchesAndOwnership(t *testing.T) {
	var root rootcause.Report
	if err := json.Unmarshal([]byte(`{"hotPath":{"frames":[{"name":"","url":"bundle.js","line":1,"category":"unknown-bundled"}],"nodes":[{"frame":{"name":"","url":"bundle.js","line":1,"category":"unknown-bundled"},"totalMs":20,"selfMs":0,"samples":8,"share":1}],"branches":[{"frame":{"name":"left","url":"bundle.js","line":2,"category":"unknown-bundled"},"totalMs":10,"samples":4,"share":0.5}],"branchesTotal":1,"stopReason":"near-tie"},"contextAttribution":[{"frame":{"name":"","url":"bundle.js","line":1,"category":"unknown-bundled"},"totalMs":20}],"contextAttributionTotal":1,"contextUnavailable":{"selfMs":3,"samples":2,"reason":"no non-framework frame on this stack; scheduling component is not visible"}}`), &root); err != nil {
		t.Fatal(err)
	}
	report := &Report{Name: "support", Source: "capture.json", RootCause: &root}
	var out bytes.Buffer
	if err := RenderMarkdownWithOptions(&out, report, MarkdownOptions{Brief: true}); err != nil {
		t.Fatal(err)
	}
	text := out.String()
	for _, want := range []string{"20ms", "8 samples", "100%", "branches", "left", "unknown-bundled", "nearest non-framework", "scheduling", "(anonymous)"} {
		if !strings.Contains(strings.ToLower(text), strings.ToLower(want)) {
			t.Errorf("surface omits %q: %s", want, text)
		}
	}
	if strings.Contains(strings.ToLower(text), "application ancestor") || strings.Contains(text, "App self") {
		t.Fatal("unknown bundle described as application code")
	}
}

func TestFindingFacetsSurviveBriefAndTerminal(t *testing.T) {
	report := &Report{Source: "capture.json", Findings: []Finding{{ID: "forced-style-or-layout-read", Title: "Native API flush", Severity: SevInfo, NativeReads: []rootcause.NativeRead{{API: "getAnimations", Flush: "style", Samples: 8, StyleSamples: 8, StyleMs: 12}}, Depth: &rootcause.DepthFacets{MaxDepth: 40, CommitFrames: 32, ReactFiberLevels: 16}, RelatedFindings: []string{"hot-function", "periodic-root"}}}}
	for _, format := range []string{"md", "brief", "term"} {
		var out bytes.Buffer
		var err error
		if format == "term" {
			err = RenderTermWithOptions(&out, report, false, MarkdownOptions{Brief: true})
		} else {
			err = RenderMarkdownWithOptions(&out, report, MarkdownOptions{Brief: format == "brief"})
		}
		if err != nil {
			t.Fatal(err)
		}
		for _, want := range []string{"getAnimations", "style flush hypothesis", "8/8 timed samples", "12ms covering records", "Depth facets", "periodic-root"} {
			if !strings.Contains(out.String(), want) {
				t.Errorf("%s omits %q", format, want)
			}
		}
	}
}

func TestOutputFacetsDoNotDisplaceFindingIdentities(t *testing.T) {
	report := &Report{Source: "capture.json"}
	for _, id := range []string{"lead-one", "lead-two", "lead-three", "lead-four", "lead-five", "lead-six", "lead-seven", "lead-eight", "lead-nine"} {
		report.Findings = append(report.Findings, Finding{ID: id, Title: "Measured lead", Severity: SevInfo, NativeReads: []rootcause.NativeRead{{API: "getAnimations", Flush: "style"}, {API: "getComputedStyle", Flush: "style"}, {API: "getBoundingClientRect", Flush: "layout"}}})
	}
	var out bytes.Buffer
	if err := RenderMarkdownWithOptions(&out, report, MarkdownOptions{Brief: true, MaxBytes: 3000}); err != nil {
		t.Fatal(err)
	}
	for _, finding := range report.Findings {
		if !strings.Contains(out.String(), finding.ID) {
			t.Errorf("facets displaced finding identity %s", finding.ID)
		}
	}
}

func TestPathNodeWireKeepsSourceContextBounded(t *testing.T) {
	source := strings.Repeat("long-capture-path/", 80) + "capture.json"
	root := &rootcause.Report{Source: source}
	for i := range 400 {
		frame := flame.Frame{Name: fmt.Sprintf("frame%d", i), URL: "bundle.js", Line: i + 1}
		node := rootcause.PathNode{Frame: frame, TotalMs: 10, Samples: 8, Share: 1}
		for _, suffix := range []string{"total_ms", "self_ms", "share", "samples"} {
			key := fmt.Sprintf("hot_path.node.n_%064d.%s", i, suffix)
			node.Stats = append(node.Stats, rootcause.Stat{Key: key, Value: 10, Command: "wkperf query " + shellQuote(source) + " rootcause " + key + " --format json"})
		}
		root.HotPath.Frames = append(root.HotPath.Frames, frame)
		root.HotPath.Nodes = append(root.HotPath.Nodes, node)
	}
	payload, err := json.Marshal(&Report{Source: source, RootCause: root})
	if err != nil {
		t.Fatal(err)
	}
	if len(payload) > 300000 || bytes.Count(payload, []byte(source)) > 4 {
		t.Fatalf("per-prefix commands repeat source context: bytes=%d occurrences=%d", len(payload), bytes.Count(payload, []byte(source)))
	}
	var wire map[string]any
	if err := json.Unmarshal(payload, &wire); err != nil {
		t.Fatal(err)
	}
	nodes := wire["rootCause"].(map[string]any)["hotPath"].(map[string]any)["nodes"].([]any)
	if len(nodes) != 400 || nodes[0].(map[string]any)["metricKey"] != root.HotPath.Nodes[0].Stats[0].Key {
		t.Fatal("compact wire loses exact prefix evidence or metric recipe")
	}
}
