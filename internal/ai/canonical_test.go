package ai

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"github.com/omniviewdev/wkperf/internal/audit"
	"github.com/omniviewdev/wkperf/internal/flame"
	"github.com/omniviewdev/wkperf/internal/insights"
	"github.com/omniviewdev/wkperf/internal/report/audithtml"
	"github.com/omniviewdev/wkperf/internal/report/insightshtml"
	"github.com/omniviewdev/wkperf/internal/rootcause"
)

func TestCanonicalFindingStacksAndCommandsSurviveReports(t *testing.T) {
	stack := rootcause.Stack{Frames: []flame.Frame{{Name: "bridge<unsafe>", URL: "bundle.js", Line: 1, Column: 90, Original: &flame.Location{URL: "src/App.tsx", Line: 4, Column: 2}}}, SelfMs: 12, Samples: 6, Command: "wkperf query 'canonical.json' stacks 'stack-stable' --format json"}
	command := "wkperf query 'canonical.json' --from 0.25 --to 0.5 stacks 'stack-stable' --format json"
	r := &insights.Report{Name: "canonical", Findings: []insights.Finding{{ID: "native-reads", Title: "Native reads", Category: "layout", Severity: insights.SevMedium, Stacks: []rootcause.Stack{stack}, Command: command}}}
	a := audit.Build([]audit.Scene{{Name: "edit", Runs: []audit.Run{{Report: r}}}}, audit.Options{})
	for _, p := range []Prompt{BuildAnalyzePrompt([]*insights.Report{r}, Context{}), BuildAuditPrompt(a, Context{})} {
		for _, want := range []string{command, "Exact sampled stack", "bridge<unsafe>", "bundle.js:1:90", "src/App.tsx:4:2"} {
			if !strings.Contains(p.User, want) {
				t.Errorf("canonical prompt missing %q", want)
			}
		}
	}
	for _, render := range []func(*bytes.Buffer) error{func(b *bytes.Buffer) error { return insightshtml.Render(b, []*insights.Report{r}) }, func(b *bytes.Buffer) error { return audithtml.Render(b, a, "") }} {
		var b bytes.Buffer
		if err := render(&b); err != nil {
			t.Fatal(err)
		}
		for _, want := range []string{"--from 0.25 --to 0.5", "bridge&lt;unsafe&gt;", "bundle.js:1:90", "src/App.tsx:4:2"} {
			if !strings.Contains(b.String(), want) {
				t.Errorf("canonical HTML missing %q", want)
			}
		}
		if strings.Contains(b.String(), "bridge<unsafe>") {
			t.Fatal("unescaped canonical stack")
		}
	}
	payload, err := json.Marshal(a)
	if err != nil {
		t.Fatal(err)
	}
	var doc struct {
		Priorities []struct {
			Stacks  []rootcause.Stack
			Command string
		}
	}
	if err := json.Unmarshal(payload, &doc); err != nil {
		t.Fatal(err)
	}
	if len(doc.Priorities) != 1 || len(doc.Priorities[0].Stacks) != 1 || doc.Priorities[0].Command != command {
		t.Fatal("audit priority lost canonical stack metadata")
	}
}
