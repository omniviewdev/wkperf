package audithtml

import (
	"bytes"
	"strings"
	"testing"

	"github.com/omniviewdev/wkperf/internal/audit"
	"github.com/omniviewdev/wkperf/internal/insights"
	"github.com/omniviewdev/wkperf/internal/scorecard"
	"github.com/omniviewdev/wkperf/internal/testgen"
)

func TestAuditHTMLSceneDetailsAndDeterminism(t *testing.T) {
	profile := insights.Analyze(testgen.Generate(3, testgen.Baseline()), "scene.json", insights.Options{})
	r := audit.Build([]audit.Scene{{Name: "edit <unsafe>", Runs: []audit.Run{{Report: profile, Card: scorecard.FromValues(map[string]float64{"frame-p95": 25})}}}}, audit.Options{})
	var first, second bytes.Buffer
	if err := Render(&first, r, ""); err != nil {
		t.Fatal(err)
	}
	if err := Render(&second, r, ""); err != nil {
		t.Fatal(err)
	}
	if first.String() != second.String() {
		t.Fatal("audit output changed for identical input")
	}
	for _, want := range []string{"Scene details", `class="wk-flame"`, "Hot path", "Owner breakdown", "edit &lt;unsafe&gt;", "Trigger timing"} {
		if !strings.Contains(first.String(), want) {
			t.Errorf("missing %q", want)
		}
	}
	if strings.Contains(first.String(), "<unsafe>") || strings.Contains(first.String(), "<div class=\"mut\">Generated ") {
		t.Fatal("unsafe or nondeterministic scene detail")
	}
}
