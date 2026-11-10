package ai

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/omniviewdev/wkperf/internal/audit"
	"github.com/omniviewdev/wkperf/internal/compare"
	"github.com/omniviewdev/wkperf/internal/insights"
	"github.com/omniviewdev/wkperf/internal/metrics"
)

func TestPromptHotspotIDAndCommonStack(t *testing.T) {
	var r insights.Report
	if err := json.Unmarshal([]byte(`{"source":"profile.json","hotspots":[{"id":"function-stable","name":"leaf","url":"bundle.js","line":1,"column":90,"selfMs":20,"commonStack":[{"id":"parent-stable","name":"owner","url":"bundle.js","line":1,"column":30},{"id":"function-stable","name":"leaf","url":"bundle.js","line":1,"column":90}]}]}`), &r); err != nil {
		t.Fatal(err)
	}
	comparison := compare.Compare([]*metrics.Summary{{}}, []*metrics.Summary{{Source: "profile.json", Functions: r.Hotspots}}, compare.DefaultOptions())
	a := audit.Build([]audit.Scene{{Name: "edit", Runs: []audit.Run{{Report: &r}}}}, audit.Options{})
	for _, p := range []Prompt{BuildAnalyzePrompt([]*insights.Report{&r}, Context{}), BuildComparePrompt(comparison, nil, nil, Context{}), BuildAuditPrompt(a, Context{})} {
		for _, want := range []string{"function-stable", "Common stack", "owner", "bundle.js:1:30"} {
			if !strings.Contains(p.User, want) {
				t.Errorf("prompt missing %q", want)
			}
		}
	}
}
