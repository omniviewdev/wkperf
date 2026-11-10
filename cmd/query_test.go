package cmd

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/omniviewdev/wkperf/internal/model"
	"github.com/omniviewdev/wkperf/internal/query"
)

type queryOutput[Row any] struct {
	query.Document
	Rows         []Row             `json:"rows"`
	DeepestStack []json.RawMessage `json:"deepest_stack"`
}

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
		var result queryOutput[query.FunctionRow]
		if err := json.Unmarshal(out.Bytes(), &result); err != nil {
			t.Fatal(err)
		}
		if result.Schema != query.Schema || len(result.Rows) != 1 || result.Functions[result.Rows[0].FunctionID].Name != "leaf" {
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
	rec := &model.Recording{StartTime: 0, EndTime: 1, Samples: []model.TargetSamples{{StackTraces: []model.StackTrace{{Timestamp: .1, StackFrames: []model.StackFrame{{Name: "a", URL: bundle, Line: 1, Column: 2}}}}, Durations: []float64{.01}}}}
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
	var mapped queryOutput[query.FunctionRow]
	if err := json.Unmarshal(out.Bytes(), &mapped); err != nil {
		t.Fatal(err)
	}
	if len(mapped.Rows) != 1 || mapped.Functions[mapped.Rows[0].FunctionID].Name != "a" || mapped.Functions[mapped.Rows[0].FunctionID].Original == nil || len(mapped.Sources) != 1 || mapped.Sources[0].SourceMaps.MappedFrames != 1 || !strings.Contains(strings.Join(mapped.Command, " "), mapFile) {
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
	var diff queryOutput[query.DiffRow]
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
	var ambiguous queryOutput[json.RawMessage]
	if err := json.Unmarshal(out.Bytes(), &ambiguous); err != nil {
		t.Fatal(err)
	}
	if ambiguous.Status != "ambiguous" || len(ambiguous.Candidates) != 2 {
		t.Fatalf("candidate list lost: %s", out.String())
	}
}

func TestQueryCommandV2FlagsAndKeys(t *testing.T) {
	file := queryFile(t)
	out, _, err := runPipeline(t, "query", file, "keys", "samples.", "--offset", "1", "--limit", "2")
	var keys queryOutput[query.KeyRow]
	if err != nil || json.Unmarshal([]byte(out), &keys) != nil || keys.Offset != 1 || len(keys.Rows) != 2 || keys.NextOffset == nil || *keys.NextOffset != 3 {
		t.Fatalf("key discovery must page through CLI: %v %.1000s", err, out)
	}
	for _, key := range keys.Rows {
		if !strings.HasPrefix(key.Key, "samples.") || key.Description == "" || key.Basis == "" || key.Operation == "" {
			t.Fatalf("key metadata lost: %+v", key)
		}
	}
	out, _, err = runPipeline(t, "query", file, "metrics", "samples.*", "--max-bytes", "4096")
	var values queryOutput[query.ValueRow]
	if err != nil || json.Unmarshal([]byte(out), &values) != nil || len(values.Rows) == 0 || len(out) > 4096 {
		t.Fatalf("CLI wildcard or byte budget lost: %v %.1000s", err, out)
	}
	for _, format := range []string{"json", "md", "tsv"} {
		out, _, err = runPipeline(t, "query", file, "top", "--format", format, "--max-bytes", "4096", "--offset", "1", "--limit", "1")
		if err != nil || len(out) > 4096 {
			t.Fatalf("%s did not bind query budget/offset: %v bytes=%d", format, err, len(out))
		}
	}
	out, _, err = runPipeline(t, "query", "diff", file, file, "--normalize", "share")
	var diff queryOutput[query.DiffRow]
	if err != nil || json.Unmarshal([]byte(out), &diff) != nil || diff.DiffNormalization == nil || diff.DiffNormalization.Mode != "share" {
		t.Fatalf("CLI diff normalization lost: %v %.1000s", err, out)
	}
}

func TestQueryCommandExactStackFlag(t *testing.T) {
	rec := &model.Recording{StartTime: 0, EndTime: 1}
	frames := make([]model.StackFrame, 60)
	for i := range frames {
		frames[i] = model.StackFrame{Name: strconv.Itoa(i), URL: "app.js", Line: i + 1, Column: 1}
	}
	rec.Samples = []model.TargetSamples{{StackTraces: []model.StackTrace{{Timestamp: .1, StackFrames: frames}}, Durations: []float64{.01}}}
	file := filepath.Join(t.TempDir(), "deep.json")
	writeFlameJSON(t, file, rec)
	for _, exact := range []bool{false, true} {
		args := []string{"query", file, "stacks", "--max-bytes", "65536"}
		if exact {
			args = append(args, "--max-stack-frames", "0")
		}
		out, _, err := runPipeline(t, args...)
		var result queryOutput[struct {
			Stack      []json.RawMessage `json:"stack"`
			StackDepth int               `json:"stack_depth"`
		}]
		if err != nil || json.Unmarshal([]byte(out), &result) != nil || len(result.Rows) != 1 || result.Rows[0].StackDepth != 60 {
			t.Fatalf("CLI stack query lost exact depth: %v %.1000s", err, out)
		}
		want := query.DefaultMaxStackFrames + 1
		if exact {
			want = 60
		}
		if len(result.Rows[0].Stack) != want {
			t.Fatalf("CLI explicit zero stack limit lost: exact=%t got=%d want=%d", exact, len(result.Rows[0].Stack), want)
		}
	}
}
