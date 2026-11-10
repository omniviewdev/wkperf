package ai

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/omniviewdev/wkperf/internal/audit"
	"github.com/omniviewdev/wkperf/internal/compare"
	"github.com/omniviewdev/wkperf/internal/insights"
)

func TestPromptStructuredEvidenceAndLocations(t *testing.T) {
	var r insights.Report
	if err := json.Unmarshal([]byte(`{"name":"profile","durationSec":1,"hotspots":[{"name":"f","url":"bundle.js","line":1,"column":222,"original":{"url":"src/App.tsx","line":18,"column":4},"selfMs":20,"totalMs":30}],"findings":[{"id":"work","title":"Work","severity":"medium","category":"script","confidence":"medium","confidenceReasons":["limited samples"],"metricKeys":["script.timer.total_ms"],"numericEvidence":[{"metricKey":"script.timer.total_ms","value":20,"unit":"ms","description":"measured work"}],"verify":[{"metricKey":"script.timer.total_ms","direction":"decrease","target":10,"unit":"ms","description":"same scenario"}],"suggestedWindows":[{"fromSec":0.2,"toSec":0.4,"reason":"burst","flags":["--from","--to"]}]}]}`), &r); err != nil {
		t.Fatal(err)
	}
	a := audit.Build([]audit.Scene{{Name: "edit", Runs: []audit.Run{{Report: &r}}}}, audit.Options{})
	for _, p := range []Prompt{BuildAnalyzePrompt([]*insights.Report{&r}, Context{}), BuildComparePrompt(&compare.Result{}, nil, &r, Context{}), BuildAuditPrompt(a, Context{})} {
		for _, want := range []string{"bundle.js:1:222", "src/App.tsx:18:4", "script.timer.total_ms", "confidence=medium", "limited samples", "verify:", "decrease", "window:", "0.2", "0.4"} {
			if !strings.Contains(p.User, want) {
				t.Errorf("prompt missing %q in %s", want, p.User)
			}
		}
		if EstimateTokens(p.System+p.User) > MaxPromptTokens {
			t.Fatal("prompt exceeded token budget")
		}
	}
}
