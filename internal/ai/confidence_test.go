package ai

import (
	"fmt"
	"strings"
	"testing"

	"github.com/omniviewdev/wkperf/internal/audit"
	"github.com/omniviewdev/wkperf/internal/compare"
	"github.com/omniviewdev/wkperf/internal/insights"
	"github.com/omniviewdev/wkperf/internal/model"
)

func TestConfidencePromptsCarryEvidenceAndActualReplication(t *testing.T) {
	rec := &model.Recording{EndTime: 12, Records: []model.Record{{Type: model.TypeScript, EventType: "function-call", StartTime: 1, EndTime: 2}}}
	var runs []audit.Run
	for i := range 4 {
		runs = append(runs, audit.Run{Report: insights.Analyze(rec, fmt.Sprintf("run-%d.json", i), insights.Options{})})
	}
	profile := runs[0].Report
	report := audit.Build([]audit.Scene{{Name: "scene", Runs: runs, MissingRuns: 1}}, audit.Options{})
	for _, tc := range []struct {
		prompt                  Prompt
		confidence, replication string
	}{
		{BuildAnalyzePrompt([]*insights.Report{profile}, Context{}), "medium", "1 of 1 runs (1 parsed)"},
		{BuildComparePrompt(&compare.Result{}, nil, profile, Context{}), "medium", "1 of 1 runs (1 parsed)"},
		{BuildAuditPrompt(report, Context{}), "high", "4 of 5 runs (4 parsed)"},
	} {
		for _, want := range []string{"confidence=" + tc.confidence, "evidenceBasis=measured", tc.replication} {
			if !strings.Contains(tc.prompt.User, want) {
				t.Errorf("missing %q", want)
			}
		}
		if EstimateTokens(tc.prompt.System+tc.prompt.User) > MaxPromptTokens {
			t.Error("confidence detail exceeded prompt cap")
		}
	}
	prompt := BuildAuditPrompt(report, Context{})
	for _, want := range []string{"4 parsed run(s) of 5 attempted (1 missing)", "Evidence is incomplete"} {
		if !strings.Contains(prompt.User, want) {
			t.Errorf("missing %q", want)
		}
	}
}
