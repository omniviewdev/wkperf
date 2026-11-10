package cmd

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/omniviewdev/wkperf/internal/model"
	flamereport "github.com/omniviewdev/wkperf/internal/report/flame"
)

func writeFlameJSON(t *testing.T, path string, rec *model.Recording) {
	t.Helper()
	data, err := json.Marshal(rec)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestFlameCLIByteIdenticalRepeatAndSampleShuffle(t *testing.T) {
	path := flameInput(t, t.TempDir(), "input.json", false)
	for _, format := range []string{"html", "svg", "folded", "speedscope", "json"} {
		t.Run(format, func(t *testing.T) {
			args := []string{path, "--format", format, "--out", "-"}
			first, err := runFlame(t, args...)
			if err != nil {
				t.Fatal(err)
			}
			repeated, err := runFlame(t, args...)
			if err != nil {
				t.Fatal(err)
			}
			if first != repeated {
				t.Fatal("same CLI input changed bytes")
			}
			rec, err := model.Load(path)
			if err != nil {
				t.Fatal(err)
			}
			samples := &rec.Samples[0]
			samples.StackTraces[0], samples.StackTraces[1] = samples.StackTraces[1], samples.StackTraces[0]
			samples.Durations[0], samples.Durations[1] = samples.Durations[1], samples.Durations[0]
			writeFlameJSON(t, path, rec)
			shuffled, err := runFlame(t, args...)
			if err != nil {
				t.Fatal(err)
			}
			if first != shuffled {
				t.Fatal("paired sample shuffle changed CLI bytes")
			}
		})
	}
}

func TestFlameProfileChartsSortByPath(t *testing.T) {
	dir := t.TempDir()
	a, b := flameInput(t, dir, "a.json", false), flameInput(t, dir, "b.json", false)
	for _, format := range []string{"html", "svg", "folded", "speedscope", "json"} {
		first, err := runFlame(t, a, b, "--format", format, "--out", "-")
		if err != nil {
			t.Fatal(err)
		}
		reversed, err := runFlame(t, b, a, "--format", format, "--out", "-")
		if err != nil {
			t.Fatal(err)
		}
		if first != reversed {
			t.Errorf("%s chart order follows argument order, not path", format)
		}
	}
}

func TestFlameAroundLongTaskSharesAnalyzeClassificationAndWindow(t *testing.T) {
	path := flameInput(t, t.TempDir(), "input.json", false)
	rec, err := model.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	rec.Records = append(rec.Records,
		model.Record{Type: model.TypeScript, EventType: "garbage-collected", StartTime: 10, EndTime: 11},
		model.Record{Type: model.TypeScript, EventType: "other", Details: json.RawMessage(`{"__type":"GarbageCollection"}`), StartTime: 10.01, EndTime: 10.99},
		model.Record{Type: model.TypeScript, EventType: "timer-installed", StartTime: 10.02, EndTime: 10.98})
	writeFlameJSON(t, path, rec)
	out, err := runFlame(t, path, "--format", "json", "--around-long-task", "1")
	if err != nil {
		t.Fatal(err)
	}
	var doc flamereport.Document
	if err := json.Unmarshal([]byte(out), &doc); err != nil {
		t.Fatal(err)
	}
	if doc.Charts[0].Tree.Root.TotalMs != 6 {
		t.Fatalf("GC/count-only events displaced longest script task: %s", out)
	}
	out, err = runFlame(t, path, "--format", "json", "--from", "0.1", "--to", "0.25", "--around-long-task", "1")
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal([]byte(out), &doc); err != nil {
		t.Fatal(err)
	}
	if doc.Charts[0].Tree.Root.TotalMs != 4 {
		t.Fatalf("selected-window task rank differs from analyze: %s", out)
	}
	if _, err := runFlame(t, path, "--format", "json", "--from", "0.1", "--to", "0.25", "--around-long-task", "2"); err == nil {
		t.Fatal("selected window has only one long task; second index must fail")
	}
}

func TestFlameAroundLongTaskUnionsNestedRecords(t *testing.T) {
	path := flameInput(t, t.TempDir(), "nested.json", false)
	rec, err := model.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	rec.Records = append(rec.Records, model.Record{Type: model.TypeScript, EventType: "microtask-dispatched", StartTime: 10.51, EndTime: 10.69})
	writeFlameJSON(t, path, rec)
	out, err := runFlame(t, path, "--format", "json", "--around-long-task", "2")
	if err != nil {
		t.Fatal(err)
	}
	var doc flamereport.Document
	if err := json.Unmarshal([]byte(out), &doc); err != nil {
		t.Fatal(err)
	}
	if doc.Charts[0].Tree.Root.TotalMs != 4 {
		t.Fatalf("nested microtask displaced second maximal task: %s", out)
	}
	if _, err := runFlame(t, path, "--format", "json", "--around-long-task", "3"); err == nil {
		t.Fatal("nested microtask must not create a third task")
	}
}

func TestFlameFocusGeneratedMappedSelectorsAndCandidates(t *testing.T) {
	dir := t.TempDir()
	path := flameInput(t, dir, "input.json", false)
	capture, loadErr := model.Load(path)
	if loadErr != nil {
		t.Fatal(loadErr)
	}
	for i := range capture.Samples[0].StackTraces {
		capture.Samples[0].StackTraces[i].StackFrames[0].Column = 2
	}
	writeFlameJSON(t, path, capture)
	mapPath := filepath.Join(dir, "bundle.js.map")
	if err := os.WriteFile(mapPath, []byte(`{"version":3,"file":"bundle.js","sources":["src/original.ts"],"names":["originalWork"],"mappings":"AAAAA"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	out, err := runFlame(t, path, "--format", "json", "--sourcemap", mapPath)
	if err != nil {
		t.Fatal(err)
	}
	var doc flamereport.Document
	if err := json.Unmarshal([]byte(out), &doc); err != nil {
		t.Fatal(err)
	}
	var mapped *model.SourceLocation
	for _, child := range doc.Charts[0].Tree.Root.Children {
		for _, leaf := range child.Children {
			if leaf.Frame.Original != nil {
				loc := leaf.Frame.Original
				mapped = &model.SourceLocation{Name: loc.Name, URL: loc.URL, Line: loc.Line, Column: loc.Column}
			}
		}
	}
	if mapped == nil {
		t.Fatal("mapped source metadata missing")
	}
	for _, selector := range []string{"work", "originalWork", "bundle.js:1:2", fmt.Sprintf("%s:%d:%d", mapped.URL, mapped.Line, mapped.Column)} {
		out, err := runFlame(t, path, "--format", "json", "--sourcemap", mapPath, "--focus", selector)
		if err != nil {
			t.Fatalf("%s: %v", selector, err)
		}
		if err := json.Unmarshal([]byte(out), &doc); err != nil {
			t.Fatal(err)
		}
		if doc.Charts[0].Tree.Root.TotalMs != 4 {
			t.Fatalf("%s focused wrong samples", selector)
		}
	}
	rec, err := model.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	for i := range rec.Samples[0].StackTraces {
		leaf := &rec.Samples[0].StackTraces[i].StackFrames[0]
		leaf.Name, leaf.URL, leaf.Line, leaf.Column = "same", "app.js", 1, i+1
	}
	writeFlameJSON(t, path, rec)
	if _, err := runFlame(t, path, "--format", "json", "--focus", "same"); err == nil || !strings.Contains(err.Error(), "ambiguous selector") || !strings.Contains(err.Error(), "app.js:1:1") || !strings.Contains(err.Error(), "app.js:1:2") {
		t.Fatalf("ambiguous selector must expose both candidates: %v", err)
	}
}

func TestFlameDiffPreservesArgumentOrderAndAbsentFocusSide(t *testing.T) {
	dir := t.TempDir()
	a, b := flameInput(t, dir, "a.json", false), flameInput(t, dir, "b.json", false)
	rec, err := model.Load(b)
	if err != nil {
		t.Fatal(err)
	}
	rec.Samples[0].Durations[0] = .008
	writeFlameJSON(t, b, rec)
	for _, tc := range []struct {
		before, after string
		delta         float64
	}{{a, b, 4}, {b, a, -4}} {
		out, err := runFlame(t, "--diff", tc.before, tc.after, "--format", "json", "--focus", "work", "--invert", "--min-ms", "1")
		if err != nil {
			t.Fatal(err)
		}
		var doc flamereport.Document
		if err := json.Unmarshal([]byte(out), &doc); err != nil {
			t.Fatal(err)
		}
		if doc.Charts[0].Comparison.Root.TotalMs != tc.delta {
			t.Fatalf("diff argument order changed sign: %s", out)
		}
	}
	rec.Samples[0].StackTraces[0].StackFrames[0].Name = "added"
	writeFlameJSON(t, b, rec)
	out, err := runFlame(t, "--diff", a, b, "--format", "json", "--focus", "added")
	if err != nil {
		t.Fatal(err)
	}
	var doc flamereport.Document
	if err := json.Unmarshal([]byte(out), &doc); err != nil {
		t.Fatal(err)
	}
	chart := doc.Charts[0]
	if chart.Before.Root.TotalMs != 0 || chart.After.Root.TotalMs != 8 || chart.Comparison.Root.TotalMs != 8 {
		t.Fatalf("new context focus must preserve empty baseline: %s", out)
	}
}
