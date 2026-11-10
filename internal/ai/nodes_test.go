package ai

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"github.com/omniviewdev/wkperf/internal/audit"
	"github.com/omniviewdev/wkperf/internal/insights"
	"github.com/omniviewdev/wkperf/internal/report/audithtml"
	"github.com/omniviewdev/wkperf/internal/report/insightshtml"
)

func TestFindingNodeCluesSurviveReports(t *testing.T) {
	r := &insights.Report{Name: "layout", Findings: []insights.Finding{{ID: "layout-cost", Title: "Layout cost", Category: "layout", Severity: insights.SevMedium, Nodes: []insights.NodeClue{{CSSPath: "#tree-node<unsafe>", DisplayName: "tree<unsafe>", EventType: "layout"}}}}}
	a := audit.Build([]audit.Scene{{Name: "edit", Runs: []audit.Run{{Report: r}}}}, audit.Options{})
	for _, p := range []Prompt{BuildAnalyzePrompt([]*insights.Report{r}, Context{}), BuildAuditPrompt(a, Context{})} {
		if !strings.Contains(p.User, "#tree-node<unsafe>") || !strings.Contains(p.User, "recorded node:") {
			t.Error("prompt lost measured node clue")
		}
	}
	for _, render := range []func(*bytes.Buffer) error{
		func(b *bytes.Buffer) error { return insightshtml.Render(b, []*insights.Report{r}) },
		func(b *bytes.Buffer) error { return audithtml.Render(b, a, "") },
	} {
		var b bytes.Buffer
		if err := render(&b); err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(b.String(), "#tree-node&lt;unsafe&gt;") || strings.Contains(b.String(), "<unsafe>") {
			t.Error("HTML lost or failed to escape node clue")
		}
	}
	for _, render := range []func(*bytes.Buffer) error{
		func(b *bytes.Buffer) error { return audit.RenderTerm(b, a, false) },
		func(b *bytes.Buffer) error { return audit.RenderMarkdown(b, a) },
	} {
		var b bytes.Buffer
		if err := render(&b); err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(b.String(), "#tree-node<unsafe>") {
			t.Error("audit text lost node clue")
		}
	}
	payload, err := json.Marshal(a)
	if err != nil {
		t.Fatal(err)
	}
	var doc struct {
		Priorities []struct {
			Nodes []insights.NodeClue `json:"nodes"`
		} `json:"priorities"`
	}
	if err := json.Unmarshal(payload, &doc); err != nil {
		t.Fatal(err)
	}
	if len(doc.Priorities) != 1 || len(doc.Priorities[0].Nodes) != 1 {
		t.Fatal("audit priority JSON lost node clue")
	}
}
