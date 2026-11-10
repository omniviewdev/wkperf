package ai

import (
	"strings"
	"testing"

	"github.com/omniviewdev/wkperf/internal/audit"
	"github.com/omniviewdev/wkperf/internal/compare"
	"github.com/omniviewdev/wkperf/internal/flame"
	"github.com/omniviewdev/wkperf/internal/insights"
	"github.com/omniviewdev/wkperf/internal/rootcause"
)

func TestRootCausePromptsCarryExactEvidenceAndCommands(t *testing.T) {
	frame := flame.Frame{Name: "App", URL: "bundle.js", Line: 1, Column: 234, Original: &flame.Location{URL: "src/App.tsx", Line: 12, Column: 3}}
	stat := rootcause.Stat{Key: "owner.app.self_ms", Value: 8, Unit: "ms", Basis: "sampled", Command: "wkperf query profile.json rootcause owner.app.self_ms"}
	stack := rootcause.Stack{Frames: []flame.Frame{frame}, SelfMs: 8, Samples: 4, Stats: []rootcause.Stat{stat}, Command: "wkperf query profile.json stacks"}
	rc := &rootcause.Report{HotPath: rootcause.Path{Frames: []flame.Frame{frame}, Stats: []rootcause.Stat{stat}, Command: "wkperf query profile.json hot-path"}, Owners: []rootcause.Owner{{Category: "app", SelfMs: 8, Stats: []rootcause.Stat{stat}}}, AppAttribution: []rootcause.Attribution{{App: frame, TotalMs: 8, Stats: []rootcause.Stat{stat}, Stacks: []rootcause.Stack{stack}}}, Findings: []rootcause.Finding{{Title: "Repeated commit", Explanation: "Hypothesis: repeated scheduling", Evidence: []rootcause.Stack{stack}, Command: "wkperf query profile.json paths"}}, Windows: []rootcause.Window{{Kind: "long-task", FromSec: 0.2, ToSec: 0.3, TopStack: &stack, Command: "wkperf query profile.json --from 0.2 --to 0.3 hot-path"}}}
	r := &insights.Report{Name: "profile", RootCause: rc}
	a := audit.Build([]audit.Scene{{Name: "edit", Runs: []audit.Run{{Report: r}}}}, audit.Options{})
	for _, p := range []Prompt{BuildAnalyzePrompt([]*insights.Report{r}, Context{}), BuildComparePrompt(&compare.Result{}, nil, r, Context{}), BuildAuditPrompt(a, Context{})} {
		for _, want := range []string{"Hot path", "Owner breakdown", "Nearest app", "Exact sampled stack", "Worst-window triage", "bundle.js:1:234", "src/App.tsx:12:3", "owner.app.self_ms=8 ms [sampled]", "rootcause owner.app.self_ms", "--from 0.2 --to 0.3 hot-path"} {
			if !strings.Contains(p.User, want) {
				t.Errorf("prompt missing %q", want)
			}
		}
		if EstimateTokens(p.System+p.User) > MaxPromptTokens {
			t.Fatal("prompt exceeds token budget")
		}
	}
	long := strings.Repeat("wide-界", 400)
	for range 100 {
		rc.Findings = append(rc.Findings, rootcause.Finding{Title: long, Explanation: long, Stats: []rootcause.Stat{stat, stat, stat, stat, stat, stat}, Evidence: []rootcause.Stack{stack, stack}})
	}
	for _, p := range []Prompt{BuildAnalyzePrompt([]*insights.Report{r}, Context{TopN: 50, MaxFindings: 30}), BuildAuditPrompt(a, Context{TopN: 50, MaxFindings: 30})} {
		if EstimateTokens(p.System+p.User) > MaxPromptTokens {
			t.Fatal("rich root cause exceeded budget")
		}
		if !strings.Contains(p.User, "truncated") {
			t.Fatal("missing truncation notice")
		}
	}
}
