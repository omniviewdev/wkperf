package cmd

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/spf13/viper"

	core "github.com/omniviewdev/wkperf/internal/flame"
	"github.com/omniviewdev/wkperf/internal/model"
	flamereport "github.com/omniviewdev/wkperf/internal/report/flame"
)

func flameInput(t *testing.T, dir, name string, wrapped bool) string {
	t.Helper()
	rec := &model.Recording{StartTime: 10, EndTime: 11, Records: []model.Record{
		{Type: model.TypeScript, StartTime: 10.1, EndTime: 10.2, EventType: "task"},
		{Type: model.TypeScript, StartTime: 10.5, EndTime: 10.7, EventType: "larger task"},
	}, Samples: []model.TargetSamples{{StackTraces: []model.StackTrace{
		{Timestamp: 10.15, StackFrames: []model.StackFrame{{Name: "work", URL: "bundle.js", Line: 1, Column: 1}, {Name: "main", URL: "app.js", Line: 1, Column: 1}}},
		{Timestamp: 10.6, StackFrames: []model.StackFrame{{Name: "idle", URL: "app.js", Line: 2, Column: 1}, {Name: "main", URL: "app.js", Line: 1, Column: 1}}},
	}, Durations: []float64{.004, .006}}}}
	var value any = rec
	if wrapped {
		value = map[string]any{"recording": rec}
	}
	data, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func runFlame(t *testing.T, args ...string) (string, error) {
	t.Helper()
	viper.Reset()
	cmd := newFlameCmd()
	var out, stderr bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&stderr)
	cmd.SetArgs(args)
	err := cmd.Execute()
	return out.String(), err
}

func TestFlameFormatsAcceptWrappedRecordings(t *testing.T) {
	path := flameInput(t, t.TempDir(), "wrapped.json", true)
	for _, format := range []string{"html", "svg", "folded", "speedscope", "json"} {
		t.Run(format, func(t *testing.T) {
			out, err := runFlame(t, path, "--format", format, "--out", "-")
			if err != nil {
				t.Fatal(err)
			}
			if out == "" {
				t.Fatal("empty flame output")
			}
			if format == "json" {
				var doc flamereport.Document
				if err := json.Unmarshal([]byte(out), &doc); err != nil || doc.Schema != flamereport.Schema || doc.Charts[0].Tree.Root.TotalMs != 10 {
					t.Fatalf("bad tree: %v %s", err, out)
				}
			}
		})
	}
	out, err := runFlame(t, path, "--out", "-")
	if err != nil || !strings.HasPrefix(out, "<!doctype html>") {
		t.Fatalf("HTML must be default: %v %.100s", err, out)
	}
}

func TestFlameWindowFocusAndTask(t *testing.T) {
	path := flameInput(t, t.TempDir(), "input.json", false)
	cases := []struct {
		args  []string
		total float64
		leaf  string
	}{
		{[]string{"--from", "0.1", "--to", "0.2"}, 4, "work"},
		{[]string{"--focus", "work"}, 4, "work"},
		{[]string{"--around-long-task", "1"}, 6, "idle"},
		{[]string{"--around-long-task", "2"}, 4, "work"},
	}
	for _, tc := range cases {
		args := append([]string{path, "--format", "json"}, tc.args...)
		out, err := runFlame(t, args...)
		if err != nil {
			t.Fatal(err)
		}
		var doc flamereport.Document
		if err := json.Unmarshal([]byte(out), &doc); err != nil {
			t.Fatal(err)
		}
		tree := doc.Charts[0].Tree
		if tree.Root.TotalMs != tc.total {
			t.Fatalf("%v total=%v want %v", tc.args, tree.Root.TotalMs, tc.total)
		}
		if !strings.Contains(out, tc.leaf) {
			t.Fatalf("focus leaf missing: %s", out)
		}
	}
}

func TestFlameMultipleAndDifferentialInputs(t *testing.T) {
	dir := t.TempDir()
	a, b := flameInput(t, dir, "a.json", false), flameInput(t, dir, "b.json", true)
	out, err := runFlame(t, a, b, "--format", "json")
	if err != nil {
		t.Fatal(err)
	}
	var doc flamereport.Document
	if err := json.Unmarshal([]byte(out), &doc); err != nil || len(doc.Charts) != 2 || doc.Mode != "profile" {
		t.Fatalf("separate chart document: %v %s", err, out)
	}
	out, err = runFlame(t, "--diff", a, b, "--format", "json", "--focus", "work", "--invert", "--min-ms", "1")
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal([]byte(out), &doc); err != nil || doc.Mode != "diff" || len(doc.Charts) != 1 || doc.Charts[0].Comparison.Root.TotalMs != 0 {
		t.Fatalf("invalid delta: %v %s", err, out)
	}
	if doc.Charts[0].Before.Root.TotalMs != 4 || doc.Charts[0].After.Root.TotalMs != 4 {
		t.Fatal("diff ignored focus")
	}
	if !doc.View.Invert || doc.View.MinMS != 1 {
		t.Fatal("view flags lost")
	}
}

func TestFlameSourceMapAcrossFormats(t *testing.T) {
	dir := t.TempDir()
	path := flameInput(t, dir, "input.json", false)
	mapPath := filepath.Join(dir, "bundle.js.map")
	data := `{"version":3,"file":"bundle.js","sources":["src/original.ts"],"names":["originalWork"],"mappings":"AAAAA"}`
	if err := os.WriteFile(mapPath, []byte(data), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, format := range []string{"html", "svg", "folded", "speedscope", "json"} {
		out, err := runFlame(t, path, "--format", format, "--out", "-", "--sourcemap", mapPath)
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(out, "original.ts") || !strings.Contains(out, "bundle.js") {
			t.Fatalf("%s lost mapped or generated location: %.500s", format, out)
		}
	}
}

func TestFlameRejectsInvalidInputs(t *testing.T) {
	path := flameInput(t, t.TempDir(), "input.json", false)
	for _, flags := range [][]string{{"--format", "png"}, {"--min-ms", "-1"}, {"--min-ms", "NaN"}, {"--from", "-1"}, {"--from", "0.8", "--to", "0.2"}, {"--around-long-task", "3"}, {"--around-long-task", "-1"}, {"--focus", "missing"}, {"--diff"}} {
		if _, err := runFlame(t, append([]string{path}, flags...)...); err == nil {
			t.Errorf("accepted invalid flags %v", flags)
		}
	}
}

func TestFlameWritesRequestedFile(t *testing.T) {
	dir := t.TempDir()
	path := flameInput(t, dir, "input.json", false)
	outPath := filepath.Join(dir, "flame.html")
	out, err := runFlame(t, path, "--out", outPath)
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(outPath)
	if err != nil || !strings.Contains(string(data), "wk-flame-canvas") || out != "" {
		t.Fatalf("file output failed: %v %.80s %q", err, data, out)
	}
}

func TestFlameFocusAmbiguousContextsReportsNodeIDs(t *testing.T) {
	rec := &model.Recording{EndTime: 1, Samples: []model.TargetSamples{{StackTraces: []model.StackTrace{
		{StackFrames: []model.StackFrame{{Name: "work"}, {Name: "a"}}},
		{StackFrames: []model.StackFrame{{Name: "work"}, {Name: "b"}}},
	}, Durations: []float64{.001, .002}}}}
	tree := core.Build(rec)
	if _, err := focusFlameTree(tree, "work"); err == nil || !strings.Contains(err.Error(), "node") {
		t.Fatalf("must report context ambiguity: %v", err)
	}
}
