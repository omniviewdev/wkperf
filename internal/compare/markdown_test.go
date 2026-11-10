package compare

import (
	"bytes"
	"errors"
	"strings"
	"testing"

	"github.com/omniviewdev/wkperf/internal/metrics"
)

func TestRenderMarkdownVerdictAndComparability(t *testing.T) {
	r := &Result{
		Baseline:      Side{Runs: 2, Sources: []string{"before.json"}},
		Cand:          Side{Runs: 2, Sources: []string{"after.json"}},
		Counts:        Counts{Regressed: 1},
		Comparability: Comparability{Score: 0.6, Normalized: true, Warnings: []Warning{{Level: "warn", Code: "url-mismatch", Message: "different | scenes <unsafe>"}}},
		Metrics:       []MetricDiff{{Metric: metrics.Metric{Key: "script.total_ms", Label: "Script | work", Group: "script", Unit: metrics.UnitMs}, Base: 10, Cand: 20, Delta: 10, Pct: 1, Verdict: Regressed, Confidence: "high"}},
		FuncRegressed: []FuncDiff{{Name: "render", URL: "bundle.js", Line: 1, Column: 222, Original: &metrics.SourceLocation{URL: "src/App.tsx", Line: 17, Column: 4}, BaseSelf: 2, CandSelf: 5, Delta: 3, Verdict: Regressed}},
	}
	var b bytes.Buffer
	if err := RenderMarkdown(&b, r); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"Verdict: **regressed**", "Comparability: **0.60**", "normalized per second", "url-mismatch", "different \\| scenes &lt;unsafe&gt;", "script.total_ms", "regressed", "high", "bundle.js:1:222", "src/App.tsx:17:4"} {
		if !strings.Contains(b.String(), want) {
			t.Errorf("missing %q in %s", want, b.String())
		}
	}
}

type brokenMarkdownWriter struct{}

func (brokenMarkdownWriter) Write([]byte) (int, error) { return 0, errors.New("write failed") }

func TestRenderMarkdownEmptyAndWriteError(t *testing.T) {
	var b bytes.Buffer
	if err := RenderMarkdown(&b, nil); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(b.String(), "No comparison metrics") {
		t.Fatal(b.String())
	}
	if err := RenderMarkdown(brokenMarkdownWriter{}, &Result{}); err == nil {
		t.Fatal("missing write error")
	}
}
