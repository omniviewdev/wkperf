package insightshtml

import (
	"bytes"
	"regexp"
	"strings"
	"testing"

	"github.com/omniviewdev/wkperf/internal/insights"
	"github.com/omniviewdev/wkperf/internal/testgen"
)

func TestOutputHTMLUsesDistinctBoundedStacks(t *testing.T) {
	r := insights.Analyze(testgen.DeepMinified(), "capture.json", insights.Options{})
	// Flame charts have their own exact tree export contract. This regression
	// bounds the repeated report evidence, not that separate visualization.
	r.FlameTree = nil
	var b bytes.Buffer
	if err := Render(&b, []*insights.Report{r}); err != nil {
		t.Fatal(err)
	}
	out := b.String()
	if !strings.Contains(out, "Stack appendix") || !strings.Contains(out, "frames elided") {
		t.Fatal("HTML must use distinct bounded stack appendix")
	}
	if strings.Count(out, "assets/bundle.js:1:17910") > 2 {
		t.Error("interior deep frame repeated outside capped previews")
	}
	for _, stack := range insights.StackPreviews(insights.SelectEvidence(r), false) {
		target := regexp.MustCompile(`id="stack-[^"]*` + regexp.QuoteMeta(stack.ID) + `"`)
		if len(target.FindAllStringIndex(out, -1)) != 1 {
			t.Errorf("stack %s must occur once in appendix", stack.ID)
		}
	}
	if b.Len() > 500*1024 {
		t.Errorf("HTML deep report bytes=%d exceeds 500 KiB", b.Len())
	}
}
