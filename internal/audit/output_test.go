package audit

import (
	"bytes"
	"strings"
	"testing"

	"github.com/omniviewdev/wkperf/internal/insights"
	"github.com/omniviewdev/wkperf/internal/testgen"
)

func TestOutputAuditUsesStackReferences(t *testing.T) {
	r := insights.Analyze(testgen.DeepMinified(), "capture.json", insights.Options{})
	a := Build([]Scene{{Name: "deep", Runs: []Run{{Report: r}}}}, Options{})
	var b bytes.Buffer
	if err := RenderMarkdown(&b, a); err != nil {
		t.Fatal(err)
	}
	out := b.String()
	if !strings.Contains(out, "Exact stack reference:") || !strings.Contains(out, "## Stacks") {
		t.Fatal("audit must use scene catalog references and one scene appendix")
	}
	if strings.Contains(out, "https://app.test/assets/bundle.js:1:17910 →") {
		t.Error("audit repeats unbounded full stack outside scene appendix")
	}
}
