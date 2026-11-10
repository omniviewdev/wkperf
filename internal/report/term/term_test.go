package term

import (
	"bytes"
	"strings"
	"testing"

	"github.com/omniviewdev/wkperf/internal/compare"
	"github.com/omniviewdev/wkperf/internal/metrics"
)

func md(group, key, label string, u metrics.Unit, base, cand, pct float64, v compare.Verdict, conf string) compare.MetricDiff {
	return compare.MetricDiff{
		Metric: metrics.Metric{Key: key, Group: group, Label: label, Unit: u, Value: cand},
		Base:   base, Cand: cand, Delta: cand - base, Pct: pct, Verdict: v, Confidence: conf,
	}
}

func sample() *compare.Result {
	return &compare.Result{
		Baseline: compare.Side{Label: "baseline", Sources: []string{"/tmp/a/base1.json"}, Runs: 1},
		Cand:     compare.Side{Label: "candidate", Sources: []string{"/tmp/b/c1.json", "c2.json"}, Runs: 2},
		Metrics: []compare.MetricDiff{
			md("frames", "frames.p95_ms", "Frame p95", metrics.UnitMs, 16, 33.5, 1.09, compare.Regressed, "high"),
			md("frames", "frames.count", "Frame count", metrics.UnitCount, 1000, 1000, 0, compare.Unchanged, "high"),
			md("script", "script.total_ms", "Script total", metrics.UnitMs, 200, 150, -0.25, compare.Improved, "high"),
			md("memory", "mem.peak", "Peak memory", metrics.UnitBytes, 1<<20, 1<<20, 0, compare.Unchanged, "high"),
			md("network", "net.reqs", "Requests", metrics.UnitCount, 10, 11, 0.1, compare.Noisy, "low"),
		},
		FuncRegressed: []compare.FuncDiff{
			{Name: "slow", URL: "https://x.com/a/b/app.js", Line: 12, BaseSelf: 1, CandSelf: 9, Delta: 8, Pct: 8},
			{Name: "meh", BaseSelf: 1, CandSelf: 2, Delta: 1, Pct: 1},
		},
		FuncImproved: []compare.FuncDiff{{Name: "fast", BaseSelf: 5, CandSelf: 1, Delta: -4, Pct: -0.8}},
		Counts:       compare.Counts{Improved: 1, Regressed: 1, Unchanged: 2, Noisy: 1},
	}
}

func render(t *testing.T, r *compare.Result, o Options) string {
	t.Helper()
	var b bytes.Buffer
	if err := Render(&b, r, o); err != nil {
		t.Fatal(err)
	}
	return b.String()
}

func TestRenderContent(t *testing.T) {
	out := render(t, sample(), Options{})
	for _, want := range []string{
		"1 improved · 1 regressed · 1 noisy · 2 unchanged",
		"Overall: REGRESSED",
		"base1.json", "c1.json, c2.json", "2 runs", "1 run",
		"33.50 ms", "+17.50 ms", "+109.0%", "▲", "▼",
		"~Requests",
		"Top regressed functions", "Top improved functions",
		"b/app.js:12",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in:\n%s", want, out)
		}
	}
	if strings.Contains(out, "Frame count") || strings.Contains(out, "Peak memory") {
		t.Errorf("unchanged rows should be hidden:\n%s", out)
	}
	if !strings.Contains(out, "(1 unchanged)") {
		t.Errorf("missing hidden footer:\n%s", out)
	}
	// Group with only hidden rows still gets a footer.
	if i := strings.Index(out, "MEMORY"); i < 0 || !strings.Contains(out[i:], "(1 unchanged)") {
		t.Errorf("memory group footer missing:\n%s", out)
	}
	if strings.Index(out, "slow") > strings.Index(out, "meh") {
		t.Errorf("function ordering not preserved")
	}
	if strings.Index(out, "FRAMES") > strings.Index(out, "SCRIPT") {
		t.Errorf("group ordering wrong")
	}
}

func TestVerbose(t *testing.T) {
	out := render(t, sample(), Options{Verbose: true})
	if !strings.Contains(out, "Frame count") || !strings.Contains(out, "Peak memory") {
		t.Errorf("verbose should show unchanged:\n%s", out)
	}
	if strings.Contains(out, "(1 unchanged)") {
		t.Errorf("no footer expected in verbose")
	}
}

func TestTopLimit(t *testing.T) {
	out := render(t, sample(), Options{Top: 1})
	if strings.Contains(out, "meh") {
		t.Errorf("Top=1 should cut second function")
	}
}

func TestColor(t *testing.T) {
	if out := render(t, sample(), Options{Color: false}); strings.Contains(out, "\x1b") {
		t.Errorf("escape codes with Color=false")
	}
	out := render(t, sample(), Options{Color: true})
	if !strings.Contains(out, ansiRed) || !strings.Contains(out, ansiGreen) || !strings.Contains(out, ansiYellow) {
		t.Errorf("expected red, green, yellow in colored output")
	}
}

func TestOverall(t *testing.T) {
	r := sample()
	r.Metrics[0].Confidence = "low"
	if got := overall(r); got != compare.Improved {
		t.Errorf("low-confidence regression: got %v want improved", got)
	}
	r.Metrics[2].Verdict = compare.Unchanged
	r.Counts.Improved = 0
	if got := overall(r); got != compare.Unchanged {
		t.Errorf("got %v want unchanged", got)
	}
	// Regression outside frames/script/layout does not make overall regressed.
	r.Metrics[4].Verdict = compare.Regressed
	r.Metrics[4].Confidence = "high"
	if got := overall(r); got != compare.Unchanged {
		t.Errorf("network regression: got %v", got)
	}
}

func TestWidthTruncation(t *testing.T) {
	r := sample()
	r.Metrics[0].Metric.Label = strings.Repeat("verylonglabel", 10)
	out := render(t, r, Options{Width: 80})
	if !strings.Contains(out, "…") {
		t.Errorf("expected truncation")
	}
}

func TestFormatValue(t *testing.T) {
	tests := []struct {
		v    float64
		u    metrics.Unit
		want string
	}{
		{1234567.891, metrics.UnitMs, "1,234,567.89 ms"},
		{0.5, metrics.UnitMs, "0.50 ms"},
		{1234567, metrics.UnitCount, "1,234,567"},
		{999, metrics.UnitCount, "999"},
		{12.34, metrics.UnitPct, "12.3%"},
		{512, metrics.UnitBytes, "512 B"},
		{2048, metrics.UnitBytes, "2.0 KB"},
		{5 << 20, metrics.UnitBytes, "5.00 MB"},
		{59.95, metrics.UnitFPS, "60.0 fps"},
		{0.25, metrics.UnitRatio, "25.0%"},
	}
	for _, tc := range tests {
		if got := FormatValue(tc.v, tc.u); got != tc.want {
			t.Errorf("FormatValue(%v,%s)=%q want %q", tc.v, tc.u, got, tc.want)
		}
	}
}

func TestFormatDeltaPct(t *testing.T) {
	if got := FormatDelta(-1500, metrics.UnitCount); got != "-1,500" {
		t.Errorf("got %q", got)
	}
	if got := FormatDelta(2, metrics.UnitMs); got != "+2.00 ms" {
		t.Errorf("got %q", got)
	}
	for f, want := range map[float64]string{0.123: "+12.3%", -0.5: "-50.0%", 0: "0.0%"} {
		if got := FormatPct(f); got != want {
			t.Errorf("FormatPct(%v)=%q want %q", f, got, want)
		}
	}
}

func TestBarAndTruncate(t *testing.T) {
	if Bar(0) != "" || Bar(5) != strings.Repeat("█", barLen) || Bar(-0.01) != "█" {
		t.Errorf("bad bar: %q %q %q", Bar(0), Bar(5), Bar(-0.01))
	}
	if got := Truncate("abcdef", 4); got != "abc…" {
		t.Errorf("got %q", got)
	}
	if got := Truncate("abc", 4); got != "abc" {
		t.Errorf("got %q", got)
	}
}
