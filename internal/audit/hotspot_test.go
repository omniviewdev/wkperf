package audit

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"github.com/omniviewdev/wkperf/internal/insights"
	"github.com/omniviewdev/wkperf/internal/model"
)

func TestAuditHotspotIdentityAndExactStack(t *testing.T) {
	rec := &model.Recording{EndTime: 1, Samples: []model.TargetSamples{{StackTraces: []model.StackTrace{{Timestamp: .1, StackFrames: []model.StackFrame{{Name: "leaf", URL: "bundle.js", Line: 1, Column: 90}, {Name: "owner", URL: "bundle.js", Line: 1, Column: 30}}}}, Durations: []float64{.020}}}}
	profile := insights.Analyze(rec, "edit.json", insights.Options{})
	id := profile.Hotspots[0].ID
	r := Build([]Scene{{Name: "edit", Runs: []Run{{Report: profile}}}}, Options{})
	payload, err := json.Marshal(r)
	if err != nil {
		t.Fatal(err)
	}
	var doc struct {
		Hotspots []struct {
			ID               string
			StackID          string `json:"stackId"`
			CommonStackScene string
		}
		Scenes []struct {
			Analysis *insights.WireReport `json:"analysis"`
		}
	}
	if err := json.Unmarshal(payload, &doc); err != nil {
		t.Fatal(err)
	}
	if len(doc.Hotspots) != 1 || doc.Hotspots[0].ID != id || doc.Hotspots[0].StackID == "" || doc.Hotspots[0].CommonStackScene != "edit" {
		t.Fatal("audit lost hotspot evidence")
	}
	stack, exists := doc.Scenes[0].Analysis.Stacks[doc.Hotspots[0].StackID]
	if !exists || stack.Depth != 2 || len(stack.Frames) != 2 || doc.Scenes[0].Analysis.Frames[stack.Frames[0]].Name != "owner" {
		t.Fatal("audit hotspot cannot resolve exact scene stack")
	}
	for _, render := range []func(*bytes.Buffer) error{func(b *bytes.Buffer) error { return RenderTerm(b, r, false) }, func(b *bytes.Buffer) error { return RenderMarkdown(b, r) }} {
		var b bytes.Buffer
		if err := render(&b); err != nil {
			t.Fatal(err)
		}
		for _, want := range []string{id, "Common stack", "edit", "owner", "bundle.js:1:30"} {
			if !strings.Contains(b.String(), want) {
				t.Errorf("audit text missing %q", want)
			}
		}
	}
}
