package insightshtml

import (
	"bytes"
	"strings"
	"testing"

	"github.com/omniviewdev/wkperf/internal/insights"
	"github.com/omniviewdev/wkperf/internal/testgen"
)

func TestAnalyzeHTMLIncludesDeterministicFlameGraph(t *testing.T) {
	r := insights.Analyze(testgen.Generate(7, testgen.Baseline()), "profile.json", insights.Options{})
	var first, second bytes.Buffer
	if err := Render(&first, []*insights.Report{r}); err != nil {
		t.Fatal(err)
	}
	if err := Render(&second, []*insights.Report{r}); err != nil {
		t.Fatal(err)
	}
	if first.String() != second.String() {
		t.Fatal("HTML changed for identical input")
	}
	for _, want := range []string{`class="wk-flame"`, `class="wk-flame-node"`, `data-action="search"`, `data-action="invert"`, "Sampled flame graph", "Hot path"} {
		if !strings.Contains(first.String(), want) {
			t.Errorf("missing embedded graph %q", want)
		}
	}
	if strings.Contains(first.String(), "<div class=\"mut\">Generated ") {
		t.Fatal("unexpected wall-clock header")
	}
}
