package flame

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	core "github.com/omniviewdev/wkperf/internal/flame"
	"github.com/omniviewdev/wkperf/internal/model"
)

func untimedExportTree() *core.Tree {
	return core.Build(&model.Recording{StartTime: 10, EndTime: 12, Samples: []model.TargetSamples{{
		StackTraces: []model.StackTrace{
			{StackFrames: []model.StackFrame{{Name: "untimed", URL: "app.js"}}},
			{Timestamp: 11, StackFrames: []model.StackFrame{{Name: "timed", URL: "app.js"}}},
		}, Durations: []float64{.025, .004},
	}}})
}

func TestUntimedSpeedscopeWeightsAndOrder(t *testing.T) {
	for _, tc := range []struct {
		name  string
		tree  *core.Tree
		count int
		total float64
	}{
		{"whole", untimedExportTree(), 2, 29},
		{"window", untimedExportTree().Window(.9, 1.1), 1, 4},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var out bytes.Buffer
			if err := Render(&out, "speedscope", []Chart{{Label: tc.name, Tree: tc.tree}}, Options{}); err != nil {
				t.Fatal(err)
			}
			var doc Speedscope
			if err := json.Unmarshal(out.Bytes(), &doc); err != nil {
				t.Fatal(err)
			}
			p := doc.Profiles[0]
			if p.EndValue != tc.total || len(p.Samples) != tc.count || len(p.Weights) != tc.count {
				t.Fatalf("weights omitted or duplicated: %+v", p)
			}
			if tc.name == "whole" && (p.Weights[0] != 25 || !strings.HasPrefix(doc.Shared.Frames[p.Samples[0][0]].Name, "untimed")) {
				t.Fatalf("untimed samples must precede timed samples: %+v", doc)
			}
		})
	}
}

func TestUntimedChartMetadataAndHTMLNote(t *testing.T) {
	tree := untimedExportTree().Window(.1, .4)
	var out bytes.Buffer
	if err := Render(&out, "json", []Chart{{Label: "partial", Tree: tree}}, Options{}); err != nil {
		t.Fatal(err)
	}
	var doc struct {
		Charts []map[string]any `json:"charts"`
	}
	if err := json.Unmarshal(out.Bytes(), &doc); err != nil {
		t.Fatal(err)
	}
	chart := doc.Charts[0]
	if chart["untimedSamples"] != float64(1) || chart["untimedMs"] != float64(25) || chart["untimedExcluded"] != true || chart["empty"] != true || chart["emptyReason"] == "" || chart["emptyReason"] == nil {
		t.Fatalf("missing exclusion/empty metadata: %v", chart)
	}
	out.Reset()
	if err := Render(&out, "html", []Chart{{Label: "partial", Tree: tree}}, Options{}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "Untimed samples: 1") || !strings.Contains(out.String(), "excluded") {
		t.Fatal("HTML omits untimed sample exclusion note")
	}
}
