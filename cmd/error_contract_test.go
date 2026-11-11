package cmd

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/omniviewdev/wkperf/internal/schema"
	"github.com/omniviewdev/wkperf/internal/testgen"
)

func TestErrorContractCodesAndSingleFilePath(t *testing.T) {
	dir := t.TempDir()
	good := queryFile(t)
	missing := filepath.Join(dir, "missing.json")
	bad := filepath.Join(dir, "broken.json")
	badMap := filepath.Join(dir, "broken.js.map")
	for _, file := range []string{bad, badMap} {
		if err := os.WriteFile(file, []byte("{broken"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	for _, tc := range []struct {
		name string
		args []string
		code string
		file string
	}{
		{"query-missing", []string{"query", missing, "top"}, "load_error", missing},
		{"analyze-missing", []string{"analyze", missing, "--format", "json"}, "load_error", missing},
		{"audit-missing", []string{"audit", missing, "--format", "json"}, "load_error", missing},
		{"summary-missing", []string{"summary", missing, "--format", "json"}, "load_error", missing},
		{"compare-missing", []string{"compare", good, missing, "--format", "json"}, "load_error", missing},
		{"flame-missing", []string{"flame", missing, "--format", "json"}, "load_error", missing},
		{"query-malformed", []string{"query", bad, "top"}, "load_error", bad},
		{"query-window", []string{"query", good, "top", "--from", "1000", "--format", "json"}, "window_error", good},
		{"query-selector", []string{"query", good, "paths", "absent"}, "selector_error", good},
		{"query-empty-selector", []string{"query", good, "paths", ""}, "selector_error", good},
		{"query-key", []string{"query", good, "metrics", "samples.total_ms"}, "unknown_key", good},
		{"query-usage", []string{"query", good, "top", "--limit", "1001"}, "usage_error", ""},
		{"query-source-map", []string{"query", good, "top", "--sourcemap", badMap}, "source_map_error", badMap},
		{"flame-selector", []string{"flame", good, "--focus", "absent", "--format", "json"}, "selector_error", good},
		{"unknown-flag", []string{"summary", good, "--format", "json", "--absent"}, "usage_error", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			out, stderr, err := runPipeline(t, tc.args...)
			if err == nil {
				t.Fatal("failed command returned success")
			}
			doc := decodeDocument(t, out)
			var failures []schema.Error
			if err := json.Unmarshal(doc["errors"], &failures); err != nil || len(failures) != 1 {
				t.Fatalf("want exactly one failure: %v %.1000s", err, out)
			}
			failure := failures[0]
			if string(failure.Code) != tc.code || failure.File != tc.file {
				t.Fatalf("want code=%s file=%q; got %+v", tc.code, tc.file, failure)
			}
			if tc.file != "" && (strings.Count(stderr, tc.file) != 1 || strings.Contains(failure.Message, tc.file)) {
				t.Fatalf("file must occur once on stderr and only in JSON file field: stderr=%q failure=%+v", stderr, failure)
			}
		})
	}
}

func TestErrorContractPartialFileOnlyAndKeyOrder(t *testing.T) {
	dir := t.TempDir()
	good := writeRec(t, dir, "good.json", 4, testgen.Baseline())
	bad := filepath.Join(dir, "broken.json")
	if err := os.WriteFile(bad, []byte("{broken"), 0600); err != nil {
		t.Fatal(err)
	}
	for _, command := range []string{"analyze", "audit"} {
		t.Run(command, func(t *testing.T) {
			path := filepath.Join(dir, command+".json")
			out, stderr, err := runPipeline(t, command, good, bad, "--format", "json", "--out", path)
			if err == nil || out != "" {
				t.Fatalf("partial file output must not emit another stdout document: err=%v stdout=%.500s", err, out)
			}
			if strings.Count(stderr, bad) != 1 {
				t.Fatalf("failed file repeated or absent in stderr: %q", stderr)
			}
			data, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			doc := decodeDocument(t, string(data))
			if len(doc["errors"]) == 0 || !strings.HasPrefix(strings.TrimSpace(string(data)), "{\n  \"schema\":") {
				t.Fatalf("file must retain ordered partial report: %.500s", data)
			}
			stdout, _, err := runPipeline(t, command, good, bad, "--format", "json")
			if err == nil || !strings.HasPrefix(strings.TrimSpace(stdout), "{\n  \"schema\":") {
				t.Fatalf("stdout partial report lost key order: err=%v %.300s", err, stdout)
			}
		})
	}
}

func TestErrorContractTextMandatoryAndBounded(t *testing.T) {
	dir := t.TempDir()
	good := writeRec(t, dir, "good.json", 4, testgen.Baseline())
	bad := filepath.Join(dir, "broken.json")
	if err := os.WriteFile(bad, []byte("{broken"), 0600); err != nil {
		t.Fatal(err)
	}
	for _, format := range []string{"md", "term"} {
		out, _, err := runPipeline(t, "analyze", good, bad, "--format", format, "--max-bytes", "512", "--no-color")
		if err == nil || !strings.HasPrefix(strings.TrimPrefix(out, "## "), "Errors\n") || !strings.Contains(out, bad) || len(out) > 512 {
			t.Fatalf("mandatory error must lead bounded %s report: err=%v bytes=%d %.700s", format, err, len(out), out)
		}
	}
	for _, format := range []string{"md", "term"} {
		out, _, err := runPipeline(t, "audit", good, bad, "--format", format, "--no-color")
		if err == nil || !strings.HasPrefix(out, "## Errors\n") || !strings.Contains(out, bad) {
			t.Fatalf("mandatory error must lead %s audit: err=%v %.700s", format, err, out)
		}
	}
}

func TestErrorContractFileGateHasStructuredFailure(t *testing.T) {
	dir := t.TempDir()
	good := writeRec(t, dir, "profile.json", 4, testgen.Baseline())
	path := filepath.Join(dir, "gate.json")
	out, _, err := runPipeline(t, "audit", good, "--format", "json", "--out", path, "--min-score", "100")
	var exit *ExitError
	if !errors.As(err, &exit) || exit.Code != 4 || out != "" {
		t.Fatalf("minimum-score file gate must preserve exit 4 without stdout: err=%v stdout=%.500s", err, out)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	doc := decodeDocument(t, string(data))
	var failures []schema.Error
	if err := json.Unmarshal(doc["errors"], &failures); err != nil || len(failures) != 1 || failures[0].Code != "score_below_minimum" {
		t.Fatalf("file gate failure missing from structured report: %v %.800s", err, data)
	}
}

func TestErrorContractTinyBudgetKeepsFailure(t *testing.T) {
	good := queryFile(t)
	bad := filepath.Join(t.TempDir(), "broken.json")
	if err := os.WriteFile(bad, []byte("{broken"), 0600); err != nil {
		t.Fatal(err)
	}
	for _, format := range []string{"md", "term"} {
		out, _, err := runPipeline(t, "analyze", good, bad, "--format", format, "--max-bytes", "64", "--no-color")
		if err == nil || len(out) > 64 || !strings.Contains(out, "Errors") || !strings.Contains(out, "load_error") {
			t.Fatalf("tiny budget must keep mandatory failure before report detail: %s err=%v bytes=%d %q", format, err, len(out), out)
		}
	}
}

func TestErrorContractComparisonGateFiles(t *testing.T) {
	dir := t.TempDir()
	fast := writeRec(t, dir, "fast.json", 1, testgen.Improved())
	slow := writeRec(t, dir, "slow.json", 1, testgen.Baseline())
	short := testgen.Improved()
	short.DurationSec = 3
	short.Functions = []testgen.FuncWeight{{Name: "other", URL: "https://x.test/o.js", Line: 1, Weight: 1}}
	mismatch := writeRec(t, dir, "mismatch.json", 2, short)
	for _, tc := range []struct {
		name string
		args []string
		exit int
		code string
	}{
		{"regression", []string{"compare", fast, slow, "--fail-on-regression"}, 2, "regression"},
		{"fixture", []string{"compare", slow, mismatch, "--normalize", "none", "--strict-fixture", "--min-comparability", "0.99"}, 3, "fixture_mismatch"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			for _, fileOutput := range []bool{false, true} {
				args := append(append([]string(nil), tc.args...), "--format", "json")
				path := filepath.Join(dir, tc.name+".json")
				if fileOutput {
					args = append(args, "--out", path)
				}
				out, stderr, err := runPipeline(t, args...)
				var exit *ExitError
				if !errors.As(err, &exit) || exit.Code != tc.exit || stderr == "" {
					t.Fatalf("gate exit changed: want=%d err=%v stderr=%q", tc.exit, err, stderr)
				}
				if fileOutput {
					if out != "" {
						t.Fatalf("gate file must not emit second stdout document: %.500s", out)
					}
					data, err := os.ReadFile(path)
					if err != nil {
						t.Fatal(err)
					}
					out = string(data)
				}
				doc := decodeDocument(t, out)
				var failures []schema.Error
				if err := json.Unmarshal(doc["errors"], &failures); err != nil || len(failures) != 1 || string(failures[0].Code) != tc.code || len(doc["metrics"]) == 0 || !strings.HasPrefix(strings.TrimSpace(out), "{\n  \"schema\":") {
					t.Fatalf("gate must keep ordered comparison plus typed failure: %v %.1000s", err, out)
				}
			}
		})
	}
}
