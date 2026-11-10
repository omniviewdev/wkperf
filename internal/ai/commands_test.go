package ai

import (
	"strings"
	"testing"

	"github.com/omniviewdev/wkperf/internal/insights"
	"github.com/omniviewdev/wkperf/internal/metrics"
	"github.com/omniviewdev/wkperf/internal/rootcause"
)

func TestPromptReproductionCommandsStayExact(t *testing.T) {
	h := metrics.FuncStat{Name: "f", URL: "https://example.test/" + strings.Repeat("long-path-", 90) + "bundle.js", Line: 1, Column: 123, SelfMs: 8, TotalMs: 12}
	r := &insights.Report{Source: "profile  spaced.json", Hotspots: []metrics.FuncStat{h}}
	command := insights.FunctionCommand(r, h, "self_ms")
	rootCommand := "wkperf query 'profile  spaced.json' rootcause '" + strings.Repeat("long-key-", 90) + "'"
	r.RootCause = &rootcause.Report{HotPath: rootcause.Path{Command: rootCommand}}
	p := BuildAnalyzePrompt([]*insights.Report{r}, Context{})
	for _, want := range []string{command, rootCommand} {
		if !strings.Contains(p.User, want) {
			t.Errorf("reproduction command truncated or altered: %s", want)
		}
	}
	if EstimateTokens(p.System+p.User) > MaxPromptTokens {
		t.Fatal("prompt budget exceeded")
	}
}
