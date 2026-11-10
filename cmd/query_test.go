package cmd

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/omniviewdev/wkperf/internal/model"
	"github.com/omniviewdev/wkperf/internal/query"
)

func queryFile(t *testing.T) string {
	t.Helper()
	rec := &model.Recording{StartTime: 10, EndTime: 12, Samples: []model.TargetSamples{{StackTraces: []model.StackTrace{{Timestamp: 10.1, StackFrames: []model.StackFrame{{Name: "leaf", URL: "app.js", Line: 2}, {Name: "root", URL: "app.js", Line: 1}}}, {Timestamp: 11.1, StackFrames: []model.StackFrame{{Name: "late", URL: "app.js", Line: 3}}}}, Durations: []float64{.03, .01}}}}
	data, err := json.Marshal(rec)
	if err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(t.TempDir(), "recording.json")
	if err := os.WriteFile(file, data, 0600); err != nil {
		t.Fatal(err)
	}
	return file
}

func TestQueryCommandFlagsAnywhereAndWindow(t *testing.T) {
	file := queryFile(t)
	for _, args := range [][]string{
		{"--limit", "1", file, "top", "--by", "self"},
		{file, "window", "0", "0.5", "top", "--limit", "1"},
		{file, "top", "--from", "0", "--to", "0.5", "--limit", "1"},
	} {
		command := newQueryCmd()
		command.SetArgs(args)
		var out, stderr bytes.Buffer
		command.SetOut(&out)
		command.SetErr(&stderr)
		if err := command.Execute(); err != nil {
			t.Fatalf("%v: %v (%s)", args, err, stderr.String())
		}
		var result query.Result
		if err := json.Unmarshal(out.Bytes(), &result); err != nil {
			t.Fatal(err)
		}
		if result.Schema != query.Schema || len(result.Rows) != 1 || result.Rows[0].Name != "leaf" {
			t.Fatalf("bad result: %s", out.String())
		}
	}
}

func TestQueryCommandSelectorsFormatsAndValidation(t *testing.T) {
	file := queryFile(t)
	for _, args := range [][]string{{file, "callers", "leaf", "--format", "md"}, {file, "stacks", "--format", "tsv"}, {file, "depth"}} {
		command := newQueryCmd()
		command.SetArgs(args)
		var out bytes.Buffer
		command.SetOut(&out)
		command.SetErr(&bytes.Buffer{})
		if err := command.Execute(); err != nil {
			t.Fatal(err)
		}
		if out.Len() == 0 {
			t.Fatal("empty result")
		}
	}
	for _, args := range [][]string{{file, "top", "--format", "xml"}, {file, "callers"}, {file, "window", "bad", "1", "top"}, {file, "top", "--limit", "1001"}, {file, "depth", "extra"}} {
		command := newQueryCmd()
		command.SetArgs(args)
		command.SetOut(&bytes.Buffer{})
		command.SetErr(&bytes.Buffer{})
		if err := command.Execute(); err == nil {
			t.Fatalf("accepted %s", strings.Join(args, " "))
		}
	}
}

func TestQueryCommandSourceMapsDiffAndAmbiguity(t *testing.T) {
	dir := t.TempDir()
	bundle := filepath.Join(dir, "app.js")
	file, mapFile := filepath.Join(dir, "capture.json"), filepath.Join(dir, "explicit.map")
	rec := &model.Recording{StartTime: 0, EndTime: 1, Samples: []model.TargetSamples{{StackTraces: []model.StackTrace{{Timestamp: .1, StackFrames: []model.StackFrame{{Name: "a", URL: bundle, Line: 1, Column: 1}}}}, Durations: []float64{.01}}}}
	data, err := json.Marshal(rec)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(file, data, 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(mapFile, []byte(`{"version":3,"file":"app.js","sources":["src/source.ts"],"names":["restored"],"mappings":"AAAAA"}`), 0600); err != nil {
		t.Fatal(err)
	}
	command := newQueryCmd()
	command.SetArgs([]string{file, "top", "restored", "--sourcemap", mapFile})
	var out bytes.Buffer
	command.SetOut(&out)
	command.SetErr(&bytes.Buffer{})
	if err := command.Execute(); err != nil {
		t.Fatal(err)
	}
	var mapped query.Result
	if err := json.Unmarshal(out.Bytes(), &mapped); err != nil {
		t.Fatal(err)
	}
	if len(mapped.Rows) != 1 || mapped.Rows[0].Name != "a" || mapped.Rows[0].Original == nil || !strings.Contains(strings.Join(mapped.Command, " "), mapFile) {
		t.Fatalf("source map/reproduction lost: %s", out.String())
	}
	command = newQueryCmd()
	command.SetArgs([]string{"diff", file, file})
	out.Reset()
	command.SetOut(&out)
	command.SetErr(&bytes.Buffer{})
	if err := command.Execute(); err != nil {
		t.Fatal(err)
	}
	var diff query.Result
	if err := json.Unmarshal(out.Bytes(), &diff); err != nil {
		t.Fatal(err)
	}
	if diff.Operation != "diff" || len(diff.Sources) != 2 {
		t.Fatalf("diff syntax failed: %s", out.String())
	}
	rec.Samples[0].StackTraces = append(rec.Samples[0].StackTraces, model.StackTrace{Timestamp: .2, StackFrames: []model.StackFrame{{Name: "a", URL: "other.js", Line: 2, Column: 1}}})
	rec.Samples[0].Durations = append(rec.Samples[0].Durations, .02)
	data, err = json.Marshal(rec)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(file, data, 0600); err != nil {
		t.Fatal(err)
	}
	command = newQueryCmd()
	command.SetArgs([]string{file, "paths", "a"})
	out.Reset()
	command.SetOut(&out)
	command.SetErr(&bytes.Buffer{})
	if err := command.Execute(); err == nil {
		t.Fatal("ambiguous selector exited zero")
	}
	var ambiguous query.Result
	if err := json.Unmarshal(out.Bytes(), &ambiguous); err != nil {
		t.Fatal(err)
	}
	if ambiguous.Status != "ambiguous" || len(ambiguous.Candidates) != 2 {
		t.Fatalf("candidate list lost: %s", out.String())
	}
}
