package insightshtml

import (
	"bytes"
	"strings"
	"testing"

	"github.com/omniviewdev/wkperf/internal/insights"
	"github.com/omniviewdev/wkperf/internal/testgen"
)

func TestRender(t *testing.T) {
	rec := testgen.Generate(1, testgen.Baseline())
	r := insights.Analyze(rec, "x<script>.json", insights.Options{})
	var buf bytes.Buffer
	if err := RenderWithAI(&buf, []*insights.Report{r}, "## Summary\n- **bold** <script>alert(1)</script>\n```\ncode <b>\n```"); err != nil {
		t.Fatal(err)
	}
	out := buf.String()
	for _, want := range []string{"<svg", "Where time goes", "Hotspot functions", "Findings", "AI analysis", "<li><b>bold</b>"} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q", want)
		}
	}
	if strings.Contains(out, "<script") {
		t.Error("unescaped script tag")
	}
}

func TestRenderEmpty(t *testing.T) {
	var buf bytes.Buffer
	if err := Render(&buf, nil); err != nil || !strings.Contains(buf.String(), "No recordings") {
		t.Fatalf("%v", err)
	}
}
