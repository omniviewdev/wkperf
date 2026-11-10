package cmd

import (
	"bytes"
	"encoding/json"
	"errors"
	"math"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"github.com/spf13/viper"

	"github.com/omniviewdev/wkperf/internal/insights"
	"github.com/omniviewdev/wkperf/internal/query"
	"github.com/omniviewdev/wkperf/internal/testgen"
)

func runPipeline(t *testing.T, args ...string) (string, string, error) {
	t.Helper()
	viper.Reset()
	root := newRootCmd()
	var out, stderr bytes.Buffer
	root.SetOut(&out)
	root.SetErr(&stderr)
	err := executeCommand(root, args)
	return out.String(), stderr.String(), err
}

func decodeDocument(t *testing.T, out string) map[string]json.RawMessage {
	t.Helper()
	var doc map[string]json.RawMessage
	if err := json.Unmarshal([]byte(out), &doc); err != nil {
		t.Fatalf("stdout must contain one JSON object: %v\n%.600s", err, out)
	}
	return doc
}

func TestPipelineWrappedExportsAllCommands(t *testing.T) {
	dir := t.TempDir()
	if _, _, err := runPipeline(t, "sample", "--wrapped", "--out", dir); err != nil {
		t.Fatal(err)
	}
	base := filepath.Join(dir, "baseline-1.json")
	cand := filepath.Join(dir, "candidate-1.json")
	data, err := os.ReadFile(base)
	if err != nil {
		t.Fatal(err)
	}
	var export struct {
		Version   int             `json:"version"`
		Recording json.RawMessage `json:"recording"`
	}
	if err := json.Unmarshal(data, &export); err != nil || export.Version != 1 || len(export.Recording) == 0 {
		t.Fatalf("sample is not a Safari wrapped export: %v %.200s", err, data)
	}
	for _, args := range [][]string{
		{"analyze", base, "--format", "json"},
		{"audit", filepath.Join(dir, "scenes"), "--format", "json"},
		{"compare", base, cand, "--format", "json"},
		{"summary", base, "--format", "json"},
	} {
		t.Run(args[0], func(t *testing.T) {
			out, _, err := runPipeline(t, args...)
			if err != nil {
				t.Fatal(err)
			}
			decodeDocument(t, out)
		})
	}
}

func TestPipelineKeepsValidFiles(t *testing.T) {
	dir := t.TempDir()
	good := writeRec(t, dir, "good.json", 4, testgen.Baseline())
	bad := filepath.Join(dir, "broken.json")
	missing := filepath.Join(dir, "missing.json")
	if err := os.WriteFile(bad, []byte("{broken"), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, command := range []string{"analyze", "audit"} {
		t.Run(command, func(t *testing.T) {
			out, stderr, err := runPipeline(t, command, missing, bad, good, "--format", "json")
			if err == nil {
				t.Fatal("partial failure must produce a nonzero exit")
			}
			doc := decodeDocument(t, out)
			var failures []struct{ File, Message, Code string }
			if err := json.Unmarshal(doc["errors"], &failures); err != nil || len(failures) != 2 {
				t.Fatalf("want two per-file JSON errors: %v %s", err, out)
			}
			for _, failure := range failures {
				if failure.File == "" || failure.Message == "" || failure.Code == "" || !strings.Contains(stderr, failure.File) {
					t.Fatalf("missing error details or stderr: %+v %q", failure, stderr)
				}
			}
			if !strings.Contains(out, good) {
				t.Fatalf("valid recording was lost: %.600s", out)
			}
			if command == "analyze" {
				var reports []json.RawMessage
				if err := json.Unmarshal(doc["reports"], &reports); err != nil || len(reports) != 1 {
					t.Fatalf("want one valid report: %v", err)
				}
			}
		})
	}
}

func TestPipelineJSONErrors(t *testing.T) {
	dir := t.TempDir()
	good := writeRec(t, dir, "good.json", 4, testgen.Baseline())
	bad := filepath.Join(dir, "missing.json")
	for _, args := range [][]string{
		{"analyze", "--format", "json"},
		{"analyze", bad, "--format=json"},
		{"audit", bad, "--format", "json"},
		{"summary", bad, "--format", "json"},
		{"compare", good, bad, "--format", "json"},
		{"compare", "--format", "json"},
		{"summary", good, "--format", "json", "--unknown"},
		{"audit", good, "--format", "json", "--min-score", "100"},
		{"analyze", good, "--format", "json", "--out", filepath.Join(dir, "out.json"), "--ai"},
		{"analyze", good, "--format", "json", "--out", dir},
		{"audit", good, "--format", "json", "--out", dir},
		{"compare", good, good, "--format", "json", "--out", dir},
		{"sample", "--format", "json"},
		{"schema", "--format", "json", "--unknown"},
	} {
		t.Run(strings.Join(args[:1], ""), func(t *testing.T) {
			t.Setenv("ANTHROPIC_API_KEY", "")
			out, stderr, err := runPipeline(t, args...)
			if err == nil || stderr == "" {
				t.Fatalf("want nonzero exit and stderr: %v %q", err, stderr)
			}
			doc := decodeDocument(t, out)
			var failures []struct{ Message, Code string }
			if err := json.Unmarshal(doc["errors"], &failures); err != nil || len(failures) == 0 || failures[0].Message == "" || failures[0].Code == "" {
				t.Fatalf("structured command error missing: %v %s", err, out)
			}
		})
	}
}

func TestPipelineFoldedStacks(t *testing.T) {
	p := testgen.Baseline()
	p.DurationSec = 0.3
	f := writeRec(t, t.TempDir(), "profile.json", 8, p)
	out, _, err := runPipeline(t, "analyze", f, "--format", "folded")
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(out), "\n")
	linePattern := regexp.MustCompile(`^\S.* [0-9]+(?:\.[0-9]+)?$`)
	if len(lines) == 0 || !strings.Contains(out, ";") {
		t.Fatalf("collapsed stacks absent: %q", out)
	}
	for _, line := range lines {
		if !linePattern.MatchString(line) {
			t.Fatalf("not standard collapsed stack: %q", line)
		}
	}
}

func TestPipelineSourceMapsAndMissingHint(t *testing.T) {
	dir := t.TempDir()
	p := testgen.Baseline()
	p.Functions = []testgen.FuncWeight{
		{Name: "a", URL: "https://app.test/assets/bundle.js", Line: 1, Column: 120, Weight: 1},
		{Name: "b", URL: "https://app.test/assets/bundle.js", Line: 1, Column: 450, Weight: 1},
	}
	f := writeRec(t, dir, "profile.json", 6, p)
	out, stderr, err := runPipeline(t, "analyze", f, "--format", "json")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(strings.ToLower(stderr), "sourcemap") || !strings.Contains(strings.ToLower(out), "sourcemap") {
		t.Fatalf("minified missing-map hint absent from stderr or metadata: %q %.500s", stderr, out)
	}
	mapData := `{"version":3,"file":"bundle.js","sources":["src/render.ts"],"names":["renderRows"],"mappings":"AAAAA"}`
	mapPath := filepath.Join(dir, "bundle.js.map")
	if err := os.WriteFile(mapPath, []byte(mapData), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, extra := range [][]string{
		nil,
		{"--sourcemap", mapPath, "--sourcemap", mapPath},
	} {
		args := append([]string{"analyze", f, "--format", "json"}, extra...)
		out, _, err := runPipeline(t, args...)
		if err != nil || !strings.Contains(out, "renderRows") || !strings.Contains(out, "render.ts") {
			t.Fatalf("source resolution must precede analysis: %v %.600s", err, out)
		}
	}
	for _, args := range [][]string{
		{"summary", f, "--format", "json", "--sourcemap", mapPath},
		{"compare", f, f, "--format", "json", "--sourcemap", mapPath},
		{"audit", f, "--format", "json", "--sourcemap", mapPath},
	} {
		out, _, err := runPipeline(t, args...)
		if err != nil {
			t.Fatal(err)
		}
		decodeDocument(t, out)
	}
}

func TestPipelineBoundedMarkdown(t *testing.T) {
	f := writeRec(t, t.TempDir(), "profile.json", 9, testgen.Baseline())
	args := []string{"analyze", f, "--format", "md", "--brief", "--max-bytes", "1800"}
	one, _, err := runPipeline(t, args...)
	if err != nil {
		t.Fatal(err)
	}
	two, _, err := runPipeline(t, args...)
	if err != nil || one != two || len(one) > 1800 {
		t.Fatalf("markdown must be deterministic and bounded: %v %d/%d", err, len(one), len(two))
	}
	if !strings.Contains(one, "- Source:") || !strings.Contains(one, f) {
		t.Fatalf("bounded Markdown must retain source identity: %s", one)
	}
	lines := strings.Split(strings.TrimSpace(one), "\n")
	if !strings.Contains(lines[len(lines)-1], "[truncated:") {
		t.Fatalf("bounded output must end with omitted-detail marker: %s", one)
	}
	jsonOut, _, err := runPipeline(t, "analyze", f, "--format", "json", "--brief", "--max-bytes", "1800")
	if err != nil || len(jsonOut) <= 1800 {
		t.Fatalf("JSON must not be truncated: %v %d", err, len(jsonOut))
	}
	decodeDocument(t, jsonOut)
}

func TestPipelineCompareMarkdownAndAuditRatings(t *testing.T) {
	dir := t.TempDir()
	base := writeRec(t, dir, "base.json", 1, testgen.Baseline())
	cand := writeRec(t, dir, "candidate.json", 2, testgen.Improved())
	out, _, err := runPipeline(t, "compare", base, cand, "--format", "md")
	if err != nil || !strings.Contains(strings.ToLower(out), "verdict") || strings.Contains(out, "\x1b[") {
		t.Fatalf("concise Markdown comparison absent: %v %.500s", err, out)
	}
	out, _, err = runPipeline(t, "audit", base, "--format", "json")
	if err != nil || !strings.Contains(out, `"rating"`) {
		t.Fatalf("audit JSON lacks per-standard ratings: %v %.500s", err, out)
	}
}

func TestPipelineSchemaContract(t *testing.T) {
	out, _, err := runPipeline(t, "schema")
	if err != nil {
		t.Fatal(err)
	}
	doc := decodeDocument(t, out)
	if !strings.Contains(string(doc["$schema"]), "2020-12") || !strings.Contains(out, "wkperf-insights/v3") {
		t.Fatalf("schema missing version: %.300s", out)
	}
	for _, field := range []string{"findings", "hotspots", "inclusiveHotspots", "triggers", "wakeups", "suggestedWindows", "scorecard", "column", "errors"} {
		if !strings.Contains(out, strconv.Quote(field)) {
			t.Errorf("schema lacks %q", field)
		}
	}
	f := writeRec(t, t.TempDir(), "profile.json", 1, testgen.Baseline())
	analysis, _, err := runPipeline(t, "analyze", f, "--format", "json")
	if err != nil {
		t.Fatal(err)
	}
	var result struct {
		Schema string `json:"schema"`
	}
	if err := json.Unmarshal([]byte(analysis), &result); err != nil || result.Schema != "wkperf-insights/v3" {
		t.Fatalf("actual schema differs: %v %s", err, result.Schema)
	}
}

func TestPipelineGateExitCodePreserved(t *testing.T) {
	f := writeRec(t, t.TempDir(), "profile.json", 1, testgen.Baseline())
	out, _, err := runPipeline(t, "audit", f, "--format", "json", "--min-score", "100")
	var exit *ExitError
	if !errors.As(err, &exit) || exit.Code != 4 {
		t.Fatalf("want exit 4: %v", err)
	}
	decodeDocument(t, out)
}

func TestPipelineDeterministicOutputs(t *testing.T) {
	dir := t.TempDir()
	p := testgen.Baseline()
	p.DurationSec = 0.8
	f := writeRec(t, dir, "profile.json", 7, p)
	for _, command := range []string{"analyze", "audit", "compare", "summary"} {
		formats := []string{"term", "json"}
		if command != "summary" {
			formats = append(formats, "md", "html", "all")
		}
		if command == "analyze" {
			formats = append(formats, "folded")
		}
		for _, format := range formats {
			t.Run(command+"/"+format, func(t *testing.T) {
				args := []string{command, f}
				if command == "compare" {
					args = append(args, f)
				}
				args = append(args, "--format", format)
				var files []string
				if format == "html" {
					dst := filepath.Join(dir, command+".html")
					args = append(args, "--out", dst)
					files = []string{dst}
				}
				if format == "all" {
					prefix := filepath.Join(dir, command+"-all")
					args = append(args, "--out", prefix)
					files = []string{prefix + ".json", prefix + ".md", prefix + ".html"}
				}
				if command != "summary" && (format == "term" || format == "all") {
					args = append(args, "--no-color")
				}
				one, _, err := runPipeline(t, args...)
				if err != nil {
					t.Fatal(err)
				}
				first := make([]string, len(files))
				for i, path := range files {
					data, err := os.ReadFile(path)
					if err != nil {
						t.Fatal(err)
					}
					first[i] = string(data)
				}
				two, _, err := runPipeline(t, args...)
				if err != nil || one != two {
					t.Fatalf("stdout changed: %v", err)
				}
				for i, path := range files {
					data, err := os.ReadFile(path)
					if err != nil || first[i] != string(data) {
						t.Fatalf("%s changed: %v", path, err)
					}
				}
			})
		}
	}
}

func TestPipelineShuffledSamplesStable(t *testing.T) {
	dir := t.TempDir()
	rec := testgen.Generate(12, testgen.Baseline())
	path := filepath.Join(dir, "shuffled.json")
	write := func() {
		t.Helper()
		var data bytes.Buffer
		if err := testgen.Write(&data, rec, true); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, data.Bytes(), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write()
	one, _, err := runPipeline(t, "analyze", path, "--format", "json")
	if err != nil {
		t.Fatal(err)
	}
	for target := range rec.Samples {
		samples := &rec.Samples[target]
		for i, j := 0, len(samples.StackTraces)-1; i < j; i, j = i+1, j-1 {
			samples.StackTraces[i], samples.StackTraces[j] = samples.StackTraces[j], samples.StackTraces[i]
			samples.Durations[i], samples.Durations[j] = samples.Durations[j], samples.Durations[i]
		}
	}
	write()
	two, _, err := runPipeline(t, "analyze", path, "--format", "json")
	if err != nil || one != two {
		t.Fatalf("paired sample order changed JSON: %v", err)
	}
}

func TestPipelineSampleRegressionProfiles(t *testing.T) {
	dir := t.TempDir()
	if _, _, err := runPipeline(t, "sample", "--out", dir, "--wrapped"); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"idle-gap", "stack-evidence", "get-animations", "react-microtask", "periodic-background"} {
		path := filepath.Join(dir, "regressions", name+".json")
		out, _, err := runPipeline(t, "analyze", path, "--format", "json")
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		decodeDocument(t, out)
	}
}

func TestPipelineBoundedMarkdownIncludesScorecardsInBudget(t *testing.T) {
	dir := t.TempDir()
	one := writeRec(t, dir, "one.json", 1, testgen.Baseline())
	two := writeRec(t, dir, "two.json", 2, testgen.Baseline())
	out, _, err := runPipeline(t, "analyze", one, two, "--format", "md", "--max-bytes", "1400")
	if err != nil || len(out) > 1400 || !strings.Contains(out, "[truncated: use JSON for complete report]") {
		t.Fatalf("aggregate Markdown budget failed: %v %d", err, len(out))
	}
	t.Setenv("ANTHROPIC_API_KEY", "")
	if _, _, err := runPipeline(t, "analyze", one, "--format", "md", "--max-bytes", "1400", "--ai"); err == nil {
		t.Fatal("bounded AI output must be rejected before network access")
	}
}

func TestPipelineWindowCommandsKeepOriginalClock(t *testing.T) {
	rec := testgen.React()
	for i := range rec.Records {
		rec.Records[i].StartTime += 2
		rec.Records[i].EndTime += 2
	}
	for target := range rec.Samples {
		for i := range rec.Samples[target].StackTraces {
			rec.Samples[target].StackTraces[i].Timestamp += 2
		}
	}
	dir := t.TempDir()
	file := filepath.Join(dir, "window.json")
	var data bytes.Buffer
	if err := testgen.Write(&data, rec, true); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(file, data.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}
	out, _, err := runPipeline(t, "analyze", file, "--format", "json", "--from", "2", "--to", "5")
	if err != nil {
		t.Fatal(err)
	}
	var doc struct {
		Reports []insights.WireReport `json:"reports"`
	}
	if err := json.Unmarshal([]byte(out), &doc); err != nil || len(doc.Reports) != 1 {
		t.Fatalf("report: %v", err)
	}
	report := doc.Reports[0]
	found := false
	for _, finding := range report.Findings {
		for _, window := range finding.SuggestedWindows {
			if window.FromSec >= 2 && window.FromSec <= 3.05 && window.ToSec >= 3.05 {
				found = true
			}
		}
	}
	if !found {
		t.Fatal("suggested windows lost original 3s event after --from 2")
	}
	if report.RootCause == nil || report.RootCause.FromSec != 2 || report.RootCause.ToSec != 5 {
		t.Fatalf("root cause window lost origin: %+v", report.RootCause)
	}
	for _, window := range report.RootCause.LongTasks {
		if window.FromSec < 3 || window.FromSec > 3.01 {
			t.Fatalf("root cause task shifted: %+v", window)
		}
	}
	verified := false
	for _, finding := range report.Findings {
		for _, evidence := range finding.NumericEvidence {
			if evidence.Command == "" {
				continue
			}
			result, _, err := runPipeline(t, "query", file, "metrics", evidence.MetricKey, "--from", "2", "--to", "5", "--format", "json")
			if err != nil {
				t.Fatalf("reproduce %s: %v", evidence.MetricKey, err)
			}
			var q query.Result
			if err := json.Unmarshal([]byte(result), &q); err != nil || len(q.Rows) != 1 || math.Abs(q.Rows[0].Value-evidence.Value) > 1e-9 {
				t.Fatalf("windowed evidence %s did not reproduce: %v want %.12g result %s", evidence.MetricKey, err, evidence.Value, result)
			}
			if !strings.Contains(evidence.Command, "--from 2") || !strings.Contains(evidence.Command, "--to 5") {
				t.Fatalf("command lost original window: %s", evidence.Command)
			}
			verified = true
			break
		}
		if verified {
			break
		}
	}
	if !verified {
		t.Fatal("no reproducible numeric evidence")
	}
}

func TestPipelineDefaultJSONQueryErrorsKeepCandidates(t *testing.T) {
	dir := t.TempDir()
	p := testgen.Baseline()
	p.Functions = []testgen.FuncWeight{
		{Name: "a", URL: "https://app.test/bundle.js", Line: 1, Column: 120, Weight: 1},
		{Name: "a", URL: "https://app.test/bundle.js", Line: 1, Column: 450, Weight: 1},
	}
	file := writeRec(t, dir, "ambiguous.json", 1, p)
	out, stderr, err := runPipeline(t, "query", file, "callers", "a")
	if err == nil || stderr == "" {
		t.Fatalf("ambiguous selector must fail with stderr: %v %q", err, stderr)
	}
	document := decodeDocument(t, out)
	var candidates []json.RawMessage
	if err := json.Unmarshal(document["candidates"], &candidates); err != nil || len(candidates) != 2 {
		t.Fatalf("candidate result lost: %v %s", err, out)
	}
	if len(document["errors"]) == 0 {
		t.Fatalf("structured query error missing: %s", out)
	}
	out, stderr, err = runPipeline(t, "query")
	if err == nil || stderr == "" {
		t.Fatalf("missing query args must fail: %v", err)
	}
	decodeDocument(t, out)
}

func TestPipelineModuleGrouping(t *testing.T) {
	p := testgen.Baseline()
	p.DurationSec = 0.5
	p.Functions = []testgen.FuncWeight{
		{Name: "render", URL: "https://app.test/src/render.js", Line: 10, Weight: 1},
		{Name: "measure", URL: "https://app.test/src/measure.js", Line: 20, Weight: 1},
	}
	file := writeRec(t, t.TempDir(), "modules.json", 2, p)
	for _, command := range []string{"analyze", "audit"} {
		for _, grouping := range []string{"", "file", "parent-directory"} {
			t.Run(command+"/"+grouping, func(t *testing.T) {
				args := []string{command, file, "--format", "json"}
				expectedGrouping := grouping
				if grouping == "" {
					expectedGrouping = "file"
				} else {
					args = append(args, "--module-grouping", grouping)
				}
				out, _, err := runPipeline(t, args...)
				if err != nil {
					t.Fatal(err)
				}
				var doc struct {
					Reports []insights.WireReport `json:"reports"`
					Report  struct {
						Modules []insights.ModuleStat `json:"modules"`
					} `json:"report"`
				}
				if err := json.Unmarshal([]byte(out), &doc); err != nil {
					t.Fatal(err)
				}
				modules := doc.Report.Modules
				if command == "analyze" {
					if len(doc.Reports) != 1 || doc.Reports[0].ModuleGrouping != expectedGrouping {
						t.Fatalf("module grouping not recorded: %s", out)
					}
					modules = doc.Reports[0].Modules
				}
				want := 2
				if grouping == "parent-directory" {
					want = 1
				}
				if len(modules) != want {
					t.Fatalf("%s grouping produced %d modules, want %d: %+v", grouping, len(modules), want, modules)
				}
			})
		}
	}
	for _, command := range []string{"analyze", "audit", "compare"} {
		args := []string{command, file}
		if command == "compare" {
			args = append(args, file)
		}
		args = append(args, "--format", "json", "--module-grouping", "invalid")
		out, stderr, err := runPipeline(t, args...)
		if err == nil || !strings.Contains(stderr, "--module-grouping") {
			t.Fatalf("invalid grouping must fail: %v %q", err, stderr)
		}
		decodeDocument(t, out)
	}
}
