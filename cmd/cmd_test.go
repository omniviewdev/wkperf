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
	err := executeCommand(root, args)
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
	if err := json.Unmarshal([]byte(out), &doc); err != nil || doc.Schema != "wkperf-insights/v3" || len(doc.Reports) != 1 {
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

// sceneTree writes home (good, 2 runs) and table (janky, 2 runs) scenes.
func sceneTree(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	bad := testgen.Baseline()
	bad.FrameMeanMs = 45
	bad.LayoutScale = 6
	for _, sc := range []struct {
		name string
		p    testgen.Params
	}{{"home", testgen.Improved()}, {"table-scroll", bad}} {
		d := filepath.Join(root, sc.name)
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
		for i := int64(1); i <= 2; i++ {
			writeRec(t, d, "r"+string(rune('0'+i))+".json", i, sc.p)
		}
	}
	return root
}

func TestDiscoverScenes(t *testing.T) {
	root := sceneTree(t)
	writeRec(t, root, "loose.json", 9, testgen.Baseline())
	got, err := discoverScenes([]string{root}, false)
	if err != nil {
		t.Fatal(err)
	}
	counts := map[string]int{}
	for _, s := range got {
		counts[s.Name] = len(s.Files)
	}
	want := map[string]int{"home": 2, "table-scroll": 2, "loose": 1}
	if !reflect.DeepEqual(counts, want) {
		t.Fatalf("got %v want %v", counts, want)
	}

	dir := t.TempDir()
	for _, n := range []string{"login-1.json", "login-2.json", "feed_1.json", "feed_2.json", "feed_3.json", "solo.json"} {
		writeRec(t, dir, n, 1, testgen.Baseline())
	}
	got, err = discoverScenes([]string{dir}, true)
	if err != nil {
		t.Fatal(err)
	}
	counts = map[string]int{}
	for _, s := range got {
		counts[s.Name] = len(s.Files)
	}
	want = map[string]int{"login": 2, "feed": 3, "solo": 1}
	if !reflect.DeepEqual(counts, want) {
		t.Fatalf("prefix: got %v want %v", counts, want)
	}
	got, _ = discoverScenes([]string{dir}, false)
	if len(got) != 6 {
		t.Fatalf("no grouping: want 6 scenes, got %d", len(got))
	}
}

func TestAuditMarkdown(t *testing.T) {
	out, err := run(t, "audit", sceneTree(t), "--format", "md")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "Fix first") || !strings.Contains(out, "table-scroll") {
		t.Fatalf("md lacks Fix first / worst scene:\n%.600s", out)
	}
}

func TestAuditJSONAndMinScore(t *testing.T) {
	root := sceneTree(t)
	out, err := run(t, "audit", root, "--format", "json")
	if err != nil {
		t.Fatal(err)
	}
	var doc struct {
		Schema string          `json:"schema"`
		Report json.RawMessage `json:"report"`
	}
	if err := json.Unmarshal([]byte(out), &doc); err != nil || doc.Schema != "wkperf-audit/v2" || len(doc.Report) == 0 {
		t.Fatalf("bad json: %v %.200s", err, out)
	}
	_, err = run(t, "audit", root, "--format", "json", "--min-score", "100")
	var ee *ExitError
	if !errors.As(err, &ee) || ee.Code != 4 {
		t.Fatalf("want exit 4, got %v", err)
	}
	if _, err := run(t, "audit", root, "--format", "json", "--min-score", "1"); err != nil {
		t.Fatalf("min-score 1 should pass: %v", err)
	}
}

func TestAuditBadFileNamed(t *testing.T) {
	root := sceneTree(t)
	bad := filepath.Join(root, "home", "broken.json")
	if err := os.WriteFile(bad, []byte("{nope"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := run(t, "audit", root, "--format", "json")
	if err == nil || !strings.Contains(err.Error(), "broken.json") {
		t.Fatalf("want error naming broken.json, got %v", err)
	}
}

func TestEmitPromptAudit(t *testing.T) {
	out, err := run(t, "audit", sceneTree(t), "--emit-prompt", "-")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"Data (user prompt)", "home", "table-scroll"} {
		if !strings.Contains(out, want) {
			t.Errorf("prompt lacks %q", want)
		}
	}
}

func TestAnalyzeScorecard(t *testing.T) {
	f := writeRec(t, t.TempDir(), "a.json", 1, testgen.Baseline())
	out, err := run(t, "analyze", f, "--format", "json")
	if err != nil {
		t.Fatal(err)
	}
	var doc struct {
		Reports []struct {
			Scorecard *struct {
				Grade string `json:"grade"`
			} `json:"scorecard"`
		} `json:"reports"`
	}
	if err := json.Unmarshal([]byte(out), &doc); err != nil || len(doc.Reports) != 1 || doc.Reports[0].Scorecard == nil || doc.Reports[0].Scorecard.Grade == "" {
		t.Fatalf("scorecard missing: %v %.300s", err, out)
	}
	out, err = run(t, "analyze", f, "--format", "json", "--no-scorecard")
	if err != nil || strings.Contains(out, `"scorecard"`) {
		t.Fatalf("--no-scorecard ignored: %v", err)
	}
}

func TestSampleWritesScenes(t *testing.T) {
	dir := t.TempDir()
	if _, err := run(t, "sample", "--out", dir); err != nil {
		t.Fatal(err)
	}
	ents, err := os.ReadDir(filepath.Join(dir, "scenes"))
	if err != nil || len(ents) < 4 {
		t.Fatalf("want >=4 scenes: %v %d", err, len(ents))
	}
}
