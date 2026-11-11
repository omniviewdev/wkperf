package ai

import (
	"strings"
	"testing"

	"github.com/omniviewdev/wkperf/internal/insights"
	"github.com/omniviewdev/wkperf/internal/testgen"
)

func TestOutputPromptSharesSelectedStackAppendix(t *testing.T) {
	r := insights.Analyze(testgen.DeepMinified(), "capture.json", insights.Options{Top: 3})
	p := BuildAnalyzePrompt([]*insights.Report{r}, Context{})
	if !strings.Contains(p.User, "Stack appendix") || !strings.Contains(p.User, "frames elided") {
		t.Fatal("prompt must render each selected exact stack in one bounded appendix")
	}
	for _, stack := range insights.StackPreviews(insights.SelectEvidence(r), false) {
		if strings.Count(p.User, "Exact stack "+stack.ID+" (") != 1 {
			t.Errorf("selected stack %s must occur once in appendix", stack.ID)
		}
	}
}
