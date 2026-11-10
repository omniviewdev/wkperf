package html

import (
	"bytes"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/omniviewdev/wkperf/internal/compare"
	"github.com/omniviewdev/wkperf/internal/metrics"
)

func sample() *compare.Result {
	return &compare.Result{
		Baseline: compare.Side{Label: "baseline", Sources: []string{"base1.json", "base2.json"}, Runs: 2},
		Cand:     compare.Side{Label: "candidate", Sources: []string{"cand.json"}, Runs: 3},
		Metrics: []compare.MetricDiff{
			{
				Metric: metrics.Metric{Key: "frames.p95_ms", Group: "frames", Label: "Frame p95", Unit: metrics.UnitMs},
				Base:   20, Cand: 30, Pct: 0.5, Verdict: compare.Regressed, Confidence: "high",
				BaseRuns: []float64{19, 21}, CandRuns: []float64{28.5, 30, 31.25},
			},
			{
				Metric: metrics.Metric{Key: "script.total_ms", Group: "script", Label: "Script total", Unit: metrics.UnitMs},
				Base:   100, Cand: 50, Pct: -0.5, Verdict: compare.Improved, Confidence: "single-run",
			},
		},
		FuncRegressed: []compare.FuncDiff{{Name: "<script>alert(1)</script>", URL: "http://x.test/a.js", Line: 7,
			BaseSelf: 1, CandSelf: 5, Delta: 4, Pct: 4, Verdict: compare.Regressed}},
		FuncImproved: []compare.FuncDiff{{Name: "fine", BaseSelf: 9, CandSelf: 2, Delta: -7, Pct: -0.77, Verdict: compare.Improved}},
		Counts:       compare.Counts{Improved: 1, Regressed: 1},
	}
}

func render(t *testing.T, r *compare.Result) string {
	t.Helper()
	var buf bytes.Buffer
	if err := Render(&buf, r, Options{Title: "T", Generated: time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)}); err != nil {
		t.Fatal(err)
	}
	return buf.String()
}

func TestRenderContentAndRanges(t *testing.T) {
	out := render(t, sample())
	for _, want := range []string{"Frame p95", "▼ Regressed", "+50.0%", "-50.0%", "baseline runs: 19.00 ms – 21.00 ms", "candidate runs: 28.50 ms – 31.25 ms", "base1.json", "2026-01-02"} {
		if !strings.Contains(out, want) {
			t.Errorf("output missing %q", want)
		}
	}
}

func TestEscapesFunctionNames(t *testing.T) {
	out := render(t, sample())
	if strings.Contains(out, "<script>alert(1)") {
		t.Fatal("function name not escaped")
	}
	if !strings.Contains(out, "&lt;script&gt;alert(1)&lt;/script&gt;") {
		t.Fatal("escaped function name missing")
	}
}

func TestChartsOnlyWhenSeriesPresent(t *testing.T) {
	r := sample()
	if strings.Contains(render(t, r), `class="chart"`) {
		t.Fatal("chart rendered without representatives")
	}
	r.BaseRep = &metrics.Summary{FrameMs: []float64{16, 17, 50}}
	r.CandRep = &metrics.Summary{FrameMs: []float64{16, 16}}
	out := render(t, r)
	if n := strings.Count(out, `class="chart"`); n != 1 {
		t.Fatalf("want 1 chart (histogram only), got %d", n)
	}
	if !strings.Contains(out, "16.7ms") || !strings.Contains(out, "33ms") {
		t.Error("guide lines missing")
	}
	r.BaseRep.CPU = []metrics.Point{{T: 0, V: 10}, {T: 1, V: 20}}
	r.CandRep.MemoryMB = []metrics.Point{{T: 0, V: 100}, {T: 2, V: 120}}
	if n := strings.Count(render(t, r), `class="chart"`); n != 3 {
		t.Fatalf("want 3 charts, got %d", n)
	}
}

func TestSelfContained(t *testing.T) {
	r := sample()
	r.BaseRep = &metrics.Summary{FrameMs: []float64{16}, CPU: []metrics.Point{{T: 0, V: 1}}}
	out := render(t, r)
	re := regexp.MustCompile(`(?i)(src|href|action|xlink:href)\s*=\s*["']?\s*(https?:)?//`)
	if m := re.FindString(out); m != "" {
		t.Fatalf("external reference: %q", m)
	}
	if strings.Contains(out, "@import") || strings.Contains(out, "url(") {
		t.Fatal("external CSS reference")
	}
}

func TestEmptyResult(t *testing.T) {
	var buf bytes.Buffer
	if err := Render(&buf, &compare.Result{}, Options{}); err != nil {
		t.Fatal(err)
	}
	if err := Render(&buf, nil, Options{}); err != nil {
		t.Fatal(err)
	}
}
