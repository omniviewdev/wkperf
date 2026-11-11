package ai

import (
	"strings"
	"testing"

	"github.com/omniviewdev/wkperf/internal/audit"
	"github.com/omniviewdev/wkperf/internal/insights"
	"github.com/omniviewdev/wkperf/internal/scorecard"
)

func sampleAudit() *audit.Report {
	card := &scorecard.Scorecard{
		Score: 58.5, Grade: "D",
		Gap: []scorecard.GapItem{
			{ID: "frame-p95", Name: "Frame time p95", Value: 50, Target: 16.7, Rating: scorecard.Poor, Factor: 3},
			{ID: "tbt", Name: "Total blocking time", Value: 400, Target: 200, Rating: scorecard.NeedsImprovement, Factor: 2},
		},
		Results: []scorecard.Result{{Standard: scorecard.Standard{ID: "lcp"}, Rating: scorecard.Unavailable}},
	}
	return &audit.Report{
		Overall: card,
		Scenes: []audit.SceneResult{
			{Name: "scroll-list", Runs: 3, DurationSec: 20, Card: card, TopFinding: "Long scripts"},
			{Name: "open-settings", Runs: 1, DurationSec: 8, Card: &scorecard.Scorecard{Grade: "B", Score: 80}},
		},
		WorstByStd: map[string]string{"tbt": "scroll-list", "frame-p95": "open-settings"},
		Breakdown:  []insights.Slice{{Name: "Script", Ms: 9000, Pct: 40}},
		Priorities: []audit.Priority{{
			ID: "long-scripts", Title: "Long scripts block the main thread", Category: "script",
			Severity: insights.SevHigh, Scenes: []string{"scroll-list", "open-settings"},
			TotalImpactMs: 3000, AvgImpactPct: 25, Rank: 7.5,
			Evidence: []string{"scroll-list: 12 tasks over 50ms"}, Suggestion: "Virtualize the list.",
		}},
		Modules: []insights.ModuleStat{{Name: "react-dom", SelfMs: 700, Pct: 20}},
	}
}

func TestAuditPromptDeterministicAndContent(t *testing.T) {
	a := BuildAuditPrompt(sampleAudit(), Context{AppName: "Omniview"})
	b := BuildAuditPrompt(sampleAudit(), Context{AppName: "Omniview"})
	if a != b {
		t.Fatal("prompt not deterministic")
	}
}

func TestAuditPromptTruncationKeepsOverallAndPriorities(t *testing.T) {
	a := sampleAudit()
	a.Modules = manyModules()
	full := buildAuditUser(a, Context{}, 100000)
	budget := len([]rune(full)) - 100
	out := buildAuditUser(a, Context{}, budget)
	if len([]rune(out)) > budget {
		t.Fatalf("over budget: %d > %d", len([]rune(out)), budget)
	}
	if !strings.Contains(out, "NOTE: input truncated") {
		t.Fatal("no truncation note")
	}
	if !strings.Contains(out, "Grade D") || !strings.Contains(out, "long-scripts") {
		t.Fatalf("overall/priorities dropped:\n%s", out)
	}
	if strings.Contains(out, "## Modules by self time") {
		t.Error("lowest-value section should go first")
	}
}

func TestAuditPromptNilAndEmpty(t *testing.T) {
	for _, a := range []*audit.Report{nil, {}} {
		p := BuildAuditPrompt(a, Context{})
		if p.System == "" || !strings.Contains(p.User, "No audit data") {
			t.Errorf("bad empty prompt: %q", p.User)
		}
	}
}

func manyModules() []insights.ModuleStat {
	var m []insights.ModuleStat
	for i := 0; i < 10; i++ {
		m = append(m, insights.ModuleStat{Name: "node_modules/some-long-package-name-" + strings.Repeat("x", 60), SelfMs: 10, Pct: 1})
	}
	return m
}
