package compare

import (
	"bytes"
	"fmt"
	"strings"
	"testing"

	"github.com/omniviewdev/wkperf/internal/metrics"
)

func TestMarkdownKeepsRequestedFunctionsAndMarksStackPreview(t *testing.T) {
	var stack []metrics.FunctionFrame
	for i := range 16 {
		stack = append(stack, metrics.FunctionFrame{Name: fmt.Sprintf("frame-%02d", i), URL: "bundle.js", Line: 1, Column: i + 1})
	}
	r := &Result{Cand: Side{Sources: []string{"candidate  test.json"}}}
	for i := range 12 {
		r.FuncRegressed = append(r.FuncRegressed, FuncDiff{ID: fmt.Sprintf("fn-%d", i), Name: "leaf", URL: "bundle.js", Line: 1, Column: 16, CandSelf: 20, CommonStack: stack, CommonStackID: "stack-stable"})
	}
	var b bytes.Buffer
	if err := RenderMarkdown(&b, r); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"fn-11", "frame-00", "frame-15", "8 frames omitted", "--format json", "wkperf query", "candidate  test.json", "stacks", "stack-stable"} {
		if !strings.Contains(b.String(), want) {
			t.Errorf("concise Markdown missing %q", want)
		}
	}
	if strings.Contains(b.String(), "frame-08") {
		t.Fatal("full stack leaked into concise preview")
	}
}
