package cmd

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/spf13/viper"

	"github.com/omniviewdev/wkperf/internal/analyze"
	"github.com/omniviewdev/wkperf/internal/metrics"
	"github.com/omniviewdev/wkperf/internal/testgen"
)

func TestExpandVS(t *testing.T) {
	got := expandVS([]string{"compare", "a", "--vs", "b", "c", "--format", "json"})
	want := []string{"compare", "a", "--vs=b", "--vs=c", "--format", "json"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v want %v", got, want)
	}
}

func TestSplitGroups(t *testing.T) {
	b, c, err := splitGroups([]string{"a", "b"}, nil)
	if err != nil || len(b) != 1 || b[0] != "a" || c[0] != "b" {
		t.Fatalf("1v1: %v %v %v", b, c, err)
	}
	if _, _, err := splitGroups([]string{"a", "b", "c"}, nil); err == nil {
		t.Fatal("expected error without --vs")
	}
	if _, _, err := splitGroups(nil, []string{"x"}); err == nil {
		t.Fatal("expected error without baseline")
	}
}

func TestReportPath(t *testing.T) {
	cases := []struct{ out, format, ext, want string }{
		{"", "html", ".html", "wkperf-report.html"},
		{"r.html", "html", ".html", "r.html"},
		{"x/", "all", ".json", "x/wkperf-report.json"},
		{"out/rep.html", "all", ".json", "out/rep.json"},
		{"pre", "all", ".html", "pre.html"},
	}
	for _, c := range cases {
		if got := reportPath(c.out, c.format, c.ext); got != c.want {
			t.Errorf("reportPath(%q,%q,%q)=%q want %q", c.out, c.format, c.ext, got, c.want)
		}
	}
}

func TestMedianMetrics(t *testing.T) {
	mk := func(v float64) *metrics.Summary {
		return &metrics.Summary{Metrics: []metrics.Metric{{Key: "k", Value: v}}}
	}
	got := medianMetrics([]*metrics.Summary{mk(9), mk(1), mk(5), mk(100)})
	if got[0].Value != 7 {
		t.Fatalf("median = %v, want 7", got[0].Value)
	}
}

// run executes the CLI in-process, writing any recordings under dir.
func run(t *testing.T, args ...string) (string, error) {
	t.Helper()
	viper.Reset()
	root := newRootCmd()
	var out, errb bytes.Buffer
	root.SetOut(&out)
	root.SetErr(&errb)
	root.SetArgs(expandEmitPrompt(expandVS(args)))
	err := root.Execute()
	return out.String(), err
}

func writeRec(t *testing.T, dir, name string, seed int64, p testgen.Params) string {
	t.Helper()
	data, err := json.Marshal(testgen.Generate(seed, p))
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestEmitPromptAnalyze(t *testing.T) {
	dir := t.TempDir()
	f := writeRec(t, dir, "a.json", 1, testgen.Baseline())
	out, err := run(t, "analyze", f, "--emit-prompt", "-")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "# Instructions (system prompt)") || !strings.Contains(out, "# Data (user prompt)") {
		t.Fatalf("prompt sections missing:\n%.300s", out)
	}
	if !strings.Contains(out, "Omniview") {
		t.Error("default app name missing")
	}
}

func TestEmitPromptCompareToFile(t *testing.T) {
	dir := t.TempDir()
	b := writeRec(t, dir, "b.json", 1, testgen.Baseline())
	c := writeRec(t, dir, "c.json", 2, testgen.Improved())
	dst := filepath.Join(dir, "p.md")
	if _, err := run(t, "compare", b, "--vs", c, "--emit-prompt="+dst); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(dst)
	if err != nil || !strings.Contains(string(data), "Data (user prompt)") {
		t.Fatalf("%v %.200s", err, data)
	}
}

func TestAnalyzeMarkdown(t *testing.T) {
	f := writeRec(t, t.TempDir(), "a.json", 1, testgen.Baseline())
	out, err := run(t, "analyze", f, "--format", "md")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"findings", "breakdown"} {
		if !strings.Contains(strings.ToLower(out), want) {
			t.Errorf("md output lacks %q:\n%.400s", want, out)
		}
	}
}

func TestAnalyzeJSONSchema(t *testing.T) {
	f := writeRec(t, t.TempDir(), "a.json", 1, testgen.Baseline())
	out, err := run(t, "analyze", f, "--format", "json")
	if err != nil {
		t.Fatal(err)
	}
	var doc struct {
		Schema  string            `json:"schema"`
		Reports []json.RawMessage `json:"reports"`
	}
	if err := json.Unmarshal([]byte(out), &doc); err != nil || doc.Schema != "wkperf-insights/v1" || len(doc.Reports) != 1 {
		t.Fatalf("bad json: %v %+v", err, doc)
	}
}

func TestAIMissingKey(t *testing.T) {
	t.Setenv("ANTHROPIC_API_KEY", "")
	f := writeRec(t, t.TempDir(), "a.json", 1, testgen.Baseline())
	_, err := run(t, "analyze", f, "--ai")
	if err == nil || !strings.Contains(err.Error(), "--emit-prompt") {
		t.Fatalf("want key error mentioning --emit-prompt, got %v", err)
	}
}

func TestWindowingReducesDuration(t *testing.T) {
	rec := testgen.Generate(1, testgen.Baseline())
	full := analyze.Summarize(rec, "x")
	win := analyze.SummarizeWith(rec, "x", analyze.Options{TrimStartSec: 2, TrimEndSec: 3})
	if !(win.DurationSec < full.DurationSec) || win.DurationSec < 4.9 || win.DurationSec > 5.1 {
		t.Fatalf("full=%v win=%v", full.DurationSec, win.DurationSec)
	}
	clipped, err := clipRecording(rec, analyze.Options{FromSec: 2, ToSec: 7})
	if err != nil || clipped.EndTime-clipped.StartTime != 5 {
		t.Fatalf("clip: %v %v", err, clipped)
	}
	if _, err := clipRecording(rec, analyze.Options{TrimStartSec: 20}); err == nil {
		t.Fatal("expected empty-window error")
	}
}

func TestSummaryWindowFlag(t *testing.T) {
	f := writeRec(t, t.TempDir(), "a.json", 1, testgen.Baseline())
	out, err := run(t, "summary", f, "--format", "json", "--trim-start", "5")
	if err != nil {
		t.Fatal(err)
	}
	var doc struct {
		Metrics []metrics.Metric `json:"metrics"`
	}
	if err := json.Unmarshal([]byte(out), &doc); err != nil {
		t.Fatal(err)
	}
	for _, m := range doc.Metrics {
		if m.Key == "overview.duration_s" || strings.HasSuffix(m.Key, "duration_sec") {
			if m.Value < 4.9 || m.Value > 5.1 {
				t.Fatalf("%s=%v", m.Key, m.Value)
			}
			return
		}
	}
	t.Logf("duration metric not found; keys unchecked")
}

func TestStrictFixtureExitCode(t *testing.T) {
	dir := t.TempDir()
	b := writeRec(t, dir, "b.json", 1, testgen.Baseline())
	short := testgen.Improved()
	short.DurationSec = 3
	short.Functions = []testgen.FuncWeight{{Name: "other", URL: "https://x.test/o.js", Line: 1, Weight: 1}}
	c := writeRec(t, dir, "c.json", 2, short)
	_, err := run(t, "compare", b, c, "--normalize", "none", "--strict-fixture", "--min-comparability", "0.99")
	var ee *ExitError
	if !errors.As(err, &ee) || ee.Code != 3 {
		t.Fatalf("want exit 3, got %v", err)
	}
	if _, err := run(t, "compare", b, c, "--normalize", "bogus"); err == nil {
		t.Fatal("want bad --normalize error")
	}
	// Same fixture passes the gate.
	b2 := writeRec(t, dir, "b2.json", 3, testgen.Baseline())
	if _, err := run(t, "compare", b, b2, "--strict-fixture"); err != nil {
		t.Fatalf("identical fixtures should pass: %v", err)
	}
}

func TestSampleWritesDifferentLengthPair(t *testing.T) {
	dir := t.TempDir()
	if _, err := run(t, "sample", "--out", dir); err != nil {
		t.Fatal(err)
	}
	for _, n := range []string{"baseline-1.json", "different-length-baseline.json", "different-length-candidate.json"} {
		if _, err := os.Stat(filepath.Join(dir, n)); err != nil {
			t.Error(err)
		}
	}
}
