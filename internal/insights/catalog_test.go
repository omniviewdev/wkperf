package insights

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"testing"

	"github.com/omniviewdev/wkperf/internal/flame"
	"github.com/omniviewdev/wkperf/internal/metrics"
	"github.com/omniviewdev/wkperf/internal/model"
	"github.com/omniviewdev/wkperf/internal/rootcause"
	"github.com/omniviewdev/wkperf/internal/testgen"
)

func TestAnalysisJSONStoresCanonicalEvidenceOnce(t *testing.T) {
	report := Analyze(testgen.DeepStacks(400, 80, 200), "deep.json", Options{Top: 10})
	payload, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	var wire map[string]any
	if err := json.Unmarshal(payload, &wire); err != nil {
		t.Fatal(err)
	}
	frames, ok := wire["frames"].([]any)
	if !ok || len(frames) == 0 {
		t.Fatalf("missing canonical frame catalog; JSON bytes=%d", len(payload))
	}
	seen := map[string]bool{}
	for _, raw := range frames {
		id := raw.(map[string]any)["id"].(string)
		if seen[id] {
			t.Fatalf("duplicate canonical frame %s", id)
		}
		seen[id] = true
	}
	stacks, ok := wire["stacks"].(map[string]any)
	if !ok || len(stacks) == 0 {
		t.Fatal("missing exact stack table")
	}
	var check func(any, string)
	check = func(value any, path string) {
		switch value := value.(type) {
		case []any:
			for i, child := range value {
				check(child, fmt.Sprintf("%s.%d", path, i))
			}
		case map[string]any:
			if id, ok := value["stackId"].(string); ok {
				if _, exists := stacks[id]; !exists {
					t.Errorf("dangling stack reference %s at %s", id, path)
				}
			}
			for key, child := range value {
				if key == "commonStack" {
					t.Errorf("embedded exact stack remains: %s", path)
				}
				if key == "frames" && path != "" {
					for _, frame := range child.([]any) {
						if _, ok := frame.(float64); !ok {
							t.Errorf("embedded frame object remains: %s", path)
						}
					}
				}
				check(child, path+"."+key)
			}
		}
	}
	check(wire, "")
	for _, raw := range stacks {
		stack := raw.(map[string]any)
		if len(stack["frames"].([]any)) != int(stack["depth"].(float64)) {
			t.Fatal("exact stack was depth-truncated")
		}
	}
	root := wire["rootCause"].(map[string]any)
	if len(root["contextAttribution"].([]any)) > 10 || root["contextAttributionTotal"].(float64) < 81 {
		t.Fatalf("uncapped attributions or missing total: %v", root["contextAttributionTotal"])
	}
	if len(report.RootCause.ContextAttribution) < 81 || len(report.RootCause.Depth.Outliers) != 80 {
		t.Fatal("wire selection corrupted full numeric evidence")
	}
	for _, raw := range wire["findings"].([]any) {
		if _, exists := raw.(map[string]any)["stacks"]; exists {
			t.Fatal("finding repeats canonical root-cause evidence")
		}
	}
	if len(payload) > 700000 {
		t.Errorf("interned synthetic JSON too large: %d", len(payload))
	}
	for _, function := range report.FlameTree.Functions() {
		if function.Frame.Name == "deepLeaf0" && !seen[flame.FunctionID(function.Frame)] {
			t.Fatal("selected deep leaf missing from catalog")
		}
	}
}

func TestAnalysisJSONCatalogDeterministic(t *testing.T) {
	rec := testgen.DeepStacks(50, 5, 200)
	first, err := json.Marshal(Analyze(rec, "deep.json", Options{}))
	if err != nil {
		t.Fatal(err)
	}
	second, err := json.Marshal(Analyze(rec, "deep.json", Options{}))
	if err != nil {
		t.Fatal(err)
	}
	if string(first) != string(second) || !strings.Contains(string(first), `"stackId":"n_`) {
		t.Fatal("catalog output lacks deterministic canonical stack IDs")
	}
}

func TestCatalogRepresentativeIgnoresEncounterOrder(t *testing.T) {
	frame := flame.Frame{URL: "app.js", Line: 1, Column: 2}
	id := flame.FunctionID(frame)
	a := metrics.FuncStat{ID: id, URL: frame.URL, Line: frame.Line, Column: frame.Column, SourceID: "z", Original: &metrics.SourceLocation{URL: "src/Z.ts", Line: 4}}
	b := a
	b.SourceID = "a"
	b.Original = &metrics.SourceLocation{URL: "src/A.ts", Line: 2}
	left := Catalog(&Report{Hotspots: []metrics.FuncStat{a, b}})
	right := Catalog(&Report{Hotspots: []metrics.FuncStat{b, a}})
	x, _ := json.Marshal(left)
	y, _ := json.Marshal(right)
	if string(x) != string(y) {
		t.Fatal("frame representative depends on first encounter")
	}
	if len(left.Frames) != 1 || left.Frames[0].ID != id || left.Frames[0].Name != "" {
		t.Fatal("catalog changed anonymous generated identity")
	}
}

func TestAnalysisEvidenceCapsPreserveTotals(t *testing.T) {
	r := Analyze(testgen.DeepStacks(400, 80, 200), "deep.json", Options{Top: 3})
	before, _ := json.Marshal(rootcause.Stats(r.RootCause))
	selected := SelectEvidence(r)
	root := selected.RootCause
	if len(root.ContextAttribution) != 3 || root.ContextAttributionTotal != 81 || len(root.Depth.Outliers) != 3 || root.Depth.OutliersTotal != 80 {
		t.Fatalf("wrong selected list counts: apps=%d/%d outliers=%d/%d", len(root.ContextAttribution), root.ContextAttributionTotal, len(root.Depth.Outliers), root.Depth.OutliersTotal)
	}
	for _, attribution := range root.ContextAttribution {
		if len(attribution.Stacks) > 3 || attribution.StacksTotal < len(attribution.Stacks) {
			t.Fatal("attribution stack cap lacks total")
		}
	}
	for _, finding := range root.Findings {
		if len(finding.Evidence) > 3 || finding.EvidenceTotal < len(finding.Evidence) {
			t.Fatal("root finding evidence cap lacks total")
		}
	}
	for _, finding := range selected.Findings {
		if len(finding.StackIDs) > 3 || finding.StacksTotal < len(finding.StackIDs) {
			t.Fatal("canonical finding references lack total")
		}
	}
	after, _ := json.Marshal(rootcause.Stats(r.RootCause))
	if string(before) != string(after) || len(r.RootCause.ContextAttribution) != 81 {
		t.Fatal("selection mutated full numeric analysis")
	}
	first, _ := json.Marshal(selected)
	second, _ := json.Marshal(SelectEvidence(selected))
	if string(first) != string(second) {
		t.Fatal("selection loses totals when repeated")
	}
	periodic := SelectEvidence(Analyze(testgen.Periodic(), "periodic.json", Options{Top: 2})).RootCause.PeriodicRoots
	if len(periodic) != 1 || len(periodic[0].Fires) != 2 || periodic[0].FiresTotal != 6 || periodic[0].FireCount != 6 {
		t.Fatalf("periodic fires lack full counts: %+v", periodic)
	}
	rec := testgen.Empty()
	for i := range 12 {
		at := float64(i) * .1
		rec.Records = append(rec.Records,
			model.Record{Type: model.TypeScript, EventType: "event-dispatched", StartTime: at, EndTime: at + .06},
			model.Record{Type: model.TypeRenderingFrame, StartTime: at, EndTime: at + .08})
	}
	timeline := SelectEvidence(Analyze(rec, "timeline.json", Options{Top: 2}))
	windows := timeline.RootCause
	if windows.WindowsTotal != 24 || !strings.Contains(timeline.MoreEvidenceCommand, "--top 24") {
		t.Fatalf("combined triage total/rerun remains pre-capped: total=%d command=%s", windows.WindowsTotal, timeline.MoreEvidenceCommand)
	}
	for _, group := range []struct{ shown, total int }{{len(windows.LongTasks), windows.LongTasksTotal}, {len(windows.SlowFrames), windows.SlowFramesTotal}, {len(windows.EventBursts), windows.EventBurstsTotal}, {len(windows.Windows), windows.WindowsTotal}} {
		if group.shown > 2 || group.total < group.shown {
			t.Fatalf("triage list cap lacks total: %+v", group)
		}
	}
}

func TestAnalysisJSONStructuralScaling(t *testing.T) {
	var sizes []int
	for _, deep := range []int{80, 160} {
		report := Analyze(testgen.DeepStacks(400, deep, 200), "deep.json", Options{})
		payload, err := json.MarshalIndent(report, "", "  ")
		if err != nil {
			t.Fatal(err)
		}
		sizes = append(sizes, len(payload))
	}
	if sizes[1] > sizes[0]*105/100 {
		t.Fatalf("default JSON grows with omitted deep stacks: %v", sizes)
	}
}

func TestAnalysisExternalCaptureSize(t *testing.T) {
	source := os.Getenv("WKPERF_REAL_CAPTURE")
	if source == "" {
		t.Skip("set WKPERF_REAL_CAPTURE to check an external real capture without copying it into the repository")
	}
	rec, err := model.Load(source)
	if err != nil {
		t.Fatal(err)
	}
	report := Analyze(rec, source, Options{})
	payload, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("default analysis JSON: %d bytes", len(payload))
	if len(payload) > 700000 {
		t.Fatalf("default real-capture JSON exceeds 700 KB: %d bytes", len(payload))
	}
}

func TestAnalysisMoreEvidenceCommandRevealsCappedLists(t *testing.T) {
	report := Analyze(testgen.DeepStacks(400, 80, 200), "capture space's|name.json", Options{Top: 3, JankMs: 20, LongTaskMs: 70, ModuleGrouping: "parent-directory", Sourcemaps: []string{"source map.js.map"}, Ownership: flame.OwnershipRules{AppURLs: []string{"**/app*.js"}, LibraryURLs: []string{"**/vendor/**"}}})
	payload, err := json.Marshal(report)
	if err != nil {
		t.Fatal(err)
	}
	var wire struct {
		MoreEvidenceCommand string `json:"moreEvidenceCommand"`
	}
	if err := json.Unmarshal(payload, &wire); err != nil {
		t.Fatal(err)
	}
	for _, fragment := range []string{"wkperf analyze " + shellQuote(report.Source), "--top 81", "--format json", "--jank-ms 20", "--long-task-ms 70", "--module-grouping 'parent-directory'", "--sourcemap 'source map.js.map'", "--app-url '**/app*.js'", "--library-url '**/vendor/**'"} {
		if !strings.Contains(wire.MoreEvidenceCommand, fragment) {
			t.Errorf("full evidence rerun omits %q: %s", fragment, wire.MoreEvidenceCommand)
		}
	}
	if strings.Contains(wire.MoreEvidenceCommand, "--from ") || strings.Contains(wire.MoreEvidenceCommand, "--to ") {
		t.Fatal("whole-recording evidence rerun adds a clock filter")
	}
}
