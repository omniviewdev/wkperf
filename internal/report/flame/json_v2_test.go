package flame

import (
	"bytes"
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	core "github.com/omniviewdev/wkperf/internal/flame"
	"github.com/omniviewdev/wkperf/internal/model"
	"github.com/omniviewdev/wkperf/internal/testgen"
)

func jsonObject(t *testing.T, tree *core.Tree) (map[string]any, []byte) {
	t.Helper()
	var out bytes.Buffer
	if err := Render(&out, "json", []Chart{{Label: "synthetic", Tree: tree}}, Options{}); err != nil {
		t.Fatal(err)
	}
	var value map[string]any
	if err := json.Unmarshal(out.Bytes(), &value); err != nil {
		t.Fatal(err)
	}
	return value, out.Bytes()
}

func TestFlatFlameJSONInternsFramesNodesAndSamples(t *testing.T) {
	tree := core.Build(testgen.DeepStacks(400, 120, 0))
	document, payload := jsonObject(t, tree)
	if document["schema"] != "wkperf-flame/v2" {
		t.Fatalf("flame JSON still uses recursive v1: %v", document["schema"])
	}
	if bytes.Count(payload, []byte{'\n'}) != 1 || len(payload) > 400000 {
		t.Fatalf("flat compact JSON has depth/duplicate-frame blowup: %d bytes", len(payload))
	}
	frames := document["frames"].([]any)
	if len(frames) != 519 {
		t.Fatalf("frame table = %d, want 519 unique functions", len(frames))
	}
	wire := document["charts"].([]any)[0].(map[string]any)["tree"].(map[string]any)
	nodes := wire["nodes"].([]any)
	if len(nodes) != 520 {
		t.Fatalf("node table = %d, want synthetic root plus 519 contexts", len(nodes))
	}
	byID := map[string]map[string]any{}
	for _, raw := range nodes {
		node := raw.(map[string]any)
		byID[node["id"].(string)] = node
		if _, nested := node["children"]; nested {
			t.Fatal("node retains recursive children")
		}
		if frame, ok := node["frame"].(float64); ok && (frame < -1 || int(frame) >= len(frames)) {
			t.Fatal("frame ref outside catalog")
		}
	}
	samples := wire["samples"].([]any)
	if len(samples) != len(tree.Samples) {
		t.Fatal("sample evidence dropped")
	}
	for i, raw := range samples {
		sample := raw.(map[string]any)
		if _, embedded := sample["frames"]; embedded {
			t.Fatal("sample repeats frame metadata")
		}
		if sample["t"] != tree.Samples[i].Timestamp || sample["w"] != tree.Samples[i].WeightMs || sample["targetID"] != tree.Samples[i].TargetID {
			t.Fatal("sample clock/weight/target changed")
		}
		node := byID[sample["path"].(string)]
		var stack []string
		for int(node["frame"].(float64)) >= 0 {
			frame := frames[int(node["frame"].(float64))].(map[string]any)
			stack = append(stack, frame["id"].(string))
			node = nodes[int(node["parent"].(float64))].(map[string]any)
		}
		if len(stack) != 400 {
			t.Fatalf("sample path lost depth: %d", len(stack))
		}
		for j, id := range stack {
			if id != tree.Samples[i].Frames[len(stack)-1-j].ID {
				t.Fatal("sample path no longer reconstructs exact stack")
			}
		}
	}
	_, smaller := jsonObject(t, core.Build(testgen.DeepStacks(200, 120, 0)))
	if len(payload) > 2*len(smaller)+2048 {
		t.Fatal("JSON size grows faster than frames and nodes")
	}
}

func TestFlatFlameJSONDiffSharesOneFrameCatalog(t *testing.T) {
	tree := core.Build(testgen.DeepStacks(400, 120, 0))
	var out bytes.Buffer
	if err := Render(&out, "json", []Chart{{Label: "diff", Before: tree, After: tree, Comparison: core.Diff(tree, tree)}}, Options{}); err != nil {
		t.Fatal(err)
	}
	var doc Document
	if err := json.Unmarshal(out.Bytes(), &doc); err != nil {
		t.Fatal(err)
	}
	chart := doc.Charts[0]
	if len(doc.Frames) != 519 || len(chart.Before.Nodes) != 520 || len(chart.After.Nodes) != 520 || len(chart.Comparison.Nodes) != 520 {
		t.Fatal("diff duplicated frame metadata or lost context nodes")
	}
	if bytes.Count(out.Bytes(), []byte(`"name":"f0"`)) != 1 || bytes.Contains(out.Bytes(), []byte(`"children"`)) {
		t.Fatal("diff repeated or nested frame evidence")
	}
	if chart.Comparison.TotalMs != 0 || chart.Comparison.BeforeTotalMs != 120 || chart.Comparison.AfterTotalMs != 120 {
		t.Fatal("diff flattening changed weights")
	}
}

func TestFlatFlameJSONPreservesClockAndUntimedMetadata(t *testing.T) {
	rec := testgen.UntimedSamples()
	rec.Discontinuities = []model.Discontinuity{{StartTime: 10.4, EndTime: 10.6}}
	tree := core.Build(rec)
	document, payload := jsonObject(t, tree)
	wire := document["charts"].([]any)[0].(map[string]any)["tree"].(map[string]any)
	if wire["startTime"] != tree.StartTime || wire["endTime"] != tree.EndTime || wire["activeDurationMs"] != tree.ActiveDurationMs || wire["untimedIncluded"] != true {
		t.Fatal("tree clock metadata lost")
	}
	if len(wire["untimed"].([]any)) != 40 || len(wire["samples"].([]any)) != 10 {
		t.Fatal("timing classes collapsed")
	}
	for _, raw := range wire["untimed"].([]any) {
		if raw.(map[string]any)["t"] != nil {
			t.Fatal("untimed sample gained synthetic timestamp")
		}
	}
	for _, key := range []string{"untimedTotal", "untimed_spans", "discontinuities"} {
		var coreFields map[string]any
		encoded, err := json.Marshal(tree)
		if err != nil {
			t.Fatal(err)
		}
		if err := json.Unmarshal(encoded, &coreFields); err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(wire[key], coreFields[key]) {
			t.Errorf("metadata %s changed", key)
		}
	}
	if strings.Contains(string(payload), `"children"`) {
		t.Fatal("recursive tree leaked into flat JSON")
	}
}
