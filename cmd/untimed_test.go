package cmd

import (
	"bytes"
	"encoding/json"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/omniviewdev/wkperf/internal/analyze"
	"github.com/omniviewdev/wkperf/internal/insights"
	"github.com/omniviewdev/wkperf/internal/model"
	"github.com/omniviewdev/wkperf/internal/query"
	"github.com/omniviewdev/wkperf/internal/testgen"
)

func writeSampleTimestampInput(t *testing.T, rec *model.Recording, name string) string {
	t.Helper()
	var data bytes.Buffer
	if err := testgen.Write(&data, rec, true); err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(file, data.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}
	return file
}

func TestUntimedPipelineRankingsAndMetrics(t *testing.T) {
	file := writeSampleTimestampInput(t, testgen.UntimedSamples(), "wrapped.json")
	for _, bounds := range [][]string{nil, {"--from", "0", "--to", "2"}} {
		args := append([]string{"query", file, "top", "--format", "json"}, bounds...)
		out, _, err := runPipeline(t, args...)
		if err != nil {
			t.Fatal(err)
		}
		var result query.Result
		if err := json.Unmarshal([]byte(out), &result); err != nil || result.TotalMS != 80 {
			t.Errorf("whole top lost sample weight: %v %s", err, out)
		}
		want := map[string]float64{"appRoot": 80, "sharedLeaf": 55, "untimedLeaf": 25}
		wantSamples := map[string]int{"appRoot": 50, "sharedLeaf": 25, "untimedLeaf": 25}
		for _, row := range result.Rows {
			if row.TotalMS != want[row.Name] {
				t.Errorf("%s total_ms=%v, want %v", row.Name, row.TotalMS, want[row.Name])
			}
			wantSelf := want[row.Name]
			if row.Name == "appRoot" {
				wantSelf = 0
			}
			if row.SelfMS != wantSelf || row.Samples != wantSamples[row.Name] {
				t.Errorf("%s self_ms=%v samples=%d, want %v / %d", row.Name, row.SelfMS, row.Samples, wantSelf, wantSamples[row.Name])
			}
			delete(want, row.Name)
		}
		if len(want) != 0 {
			t.Errorf("missing rankings: %v", want)
		}
	}
	for key, want := range map[string]float64{"samples.count": 50, "samples.sampled_ms": 80, "samples.coverage_ratio": 80.0 / 140, "samples.untimed_count": 40} {
		out, _, err := runPipeline(t, "query", file, "metrics", key, "--format", "json")
		if err != nil {
			t.Errorf("%s: %v", key, err)
			continue
		}
		var result query.Result
		if err := json.Unmarshal([]byte(out), &result); err != nil || len(result.Rows) != 1 || math.Abs(result.Rows[0].Value-want) > 1e-9 {
			t.Errorf("%s did not reproduce %v: %v %s", key, want, err, out)
		}
	}
	out, _, err := runPipeline(t, "analyze", file, "--format", "json")
	if err != nil {
		t.Fatal(err)
	}
	var doc struct {
		Reports []insights.WireReport `json:"reports"`
	}
	if err := json.Unmarshal([]byte(out), &doc); err != nil || len(doc.Reports) != 1 {
		t.Fatalf("analysis: %v", err)
	}
	for _, finding := range doc.Reports[0].Findings {
		if finding.ID == "low-sample-coverage" {
			t.Error("whole recording falsely reports low sample coverage")
		}
	}
	out, _, err = runPipeline(t, "flame", file, "--format", "folded")
	if err != nil {
		t.Fatal(err)
	}
	var total float64
	lines := strings.Split(strings.TrimSpace(out), "\n")
	for _, line := range lines {
		fields := strings.Fields(line)
		weight, err := strconv.ParseFloat(fields[len(fields)-1], 64)
		if err != nil {
			t.Fatal(err)
		}
		total += weight
	}
	if len(lines) != 2 || total != 80 {
		t.Errorf("folded total=%v: %q", total, out)
	}
}

func TestUntimedPipelineWindowAndEmptyNotes(t *testing.T) {
	file := writeSampleTimestampInput(t, testgen.UntimedSamples(), "wrapped.json")
	for _, format := range []string{"folded", "speedscope", "json", "svg"} {
		out, stderr, err := runPipeline(t, "flame", file, "--around-long-task", "1", "--format", format)
		if err != nil || !strings.Contains(stderr, "40 samples (40 ms) have no timestamp") || !strings.Contains(stderr, "no weighted samples") {
			t.Errorf("%s missing valid empty-window note: %v %q", format, err, stderr)
		}
		if format == "folded" && out != "" {
			t.Errorf("empty folded output contains data: %q", out)
		}
	}
	for _, format := range []string{"folded", "speedscope", "json", "svg"} {
		timed := &model.Recording{StartTime: 10, EndTime: 12, Samples: []model.TargetSamples{{StackTraces: []model.StackTrace{{Timestamp: 10.1, StackFrames: []model.StackFrame{{Name: "timed"}}}}, Durations: []float64{.001}}}}
		path := writeSampleTimestampInput(t, timed, "timed.json")
		_, stderr, err := runPipeline(t, "flame", path, "--from", "1", "--format", format)
		if err != nil || !strings.Contains(stderr, "no weighted samples") {
			t.Errorf("%s silently empty: %v %q", format, err, stderr)
		}
	}
}

func TestUntimedClipKeepsRecordingMetadata(t *testing.T) {
	rec := testgen.UntimedSamples()
	full, err := clipRecording(rec, analyze.Options{ToSec: 2 - 3e-10})
	if err != nil || full != rec {
		t.Fatalf("rounded whole window must preserve original recording: %v", err)
	}
	partial, err := clipRecording(rec, analyze.Options{FromSec: .1, ToSec: .4})
	if err != nil {
		t.Fatal(err)
	}
	samples := 0
	for _, target := range partial.Samples {
		samples += len(target.StackTraces)
	}
	if samples != 40 {
		t.Fatalf("partial clipping discarded %d untimed samples", 40-samples)
	}
}

func TestUntimedPipelineSampleCommandsReproduce(t *testing.T) {
	file := writeSampleTimestampInput(t, testgen.DecimalEnd(), "decimal end's profile.json")
	out, _, err := runPipeline(t, "analyze", file, "--format", "json")
	if err != nil {
		t.Fatal(err)
	}
	var document any
	if err := json.Unmarshal([]byte(out), &document); err != nil {
		t.Fatal(err)
	}
	seen := map[string]bool{}
	wholeWeight, wholeCount := false, false
	var walk func(any)
	walk = func(value any) {
		switch node := value.(type) {
		case []any:
			for _, child := range node {
				walk(child)
			}
		case map[string]any:
			command, _ := node["command"].(string)
			expected, numeric := node["value"].(float64)
			if numeric && strings.HasPrefix(command, "wkperf query ") && strings.Contains(command, "samples.") && !seen[command] {
				seen[command] = true
				// Let a POSIX shell parse the exact emitted command without building
				// a second binary. The function returns arguments to the CLI helper.
				data, err := exec.Command("/bin/sh", "-c", "wkperf() { printf '%s\\000' \"$@\"; }; "+command).Output()
				if err != nil {
					t.Fatalf("parse exact command %q: %v", command, err)
				}
				args := strings.Split(strings.TrimSuffix(string(data), "\x00"), "\x00")
				resultJSON, _, err := runPipeline(t, args...)
				if err != nil {
					t.Fatalf("exact command %s: %v", command, err)
				}
				var result query.Result
				if err := json.Unmarshal([]byte(resultJSON), &result); err != nil || len(result.Rows) != 1 || math.Abs(result.Rows[0].Value-expected) > 1e-9 {
					t.Fatalf("exact command %s: want %v, got %s (%v)", command, expected, resultJSON, err)
				}
				if len(args) > 3 && !strings.Contains(command, "--from") && !strings.Contains(command, "--to") {
					if args[3] == "samples.self_ms" || args[3] == "samples.sampled_ms" {
						wholeWeight = result.Rows[0].Value == 80
					}
					if args[3] == "samples.count" {
						wholeCount = result.Rows[0].Value == 50
					}
				}
			}
			for _, child := range node {
				walk(child)
			}
		}
	}
	walk(document)
	if len(seen) == 0 || !wholeWeight || !wholeCount {
		t.Fatalf("whole-recording commands did not reproduce 80 ms / 50 samples: commands=%d weight=%v count=%v", len(seen), wholeWeight, wholeCount)
	}
}

func TestUntimedPipelineCompareTimedControl(t *testing.T) {
	untimed := testgen.UntimedSamples()
	timed := testgen.UntimedSamples()
	for i := range timed.Samples[0].StackTraces {
		trace := &timed.Samples[0].StackTraces[i]
		if trace.Timestamp == 0 {
			trace.Timestamp = 10.2 + float64(i)*.0025
		}
	}
	a := writeSampleTimestampInput(t, timed, "timed.json")
	b := writeSampleTimestampInput(t, untimed, "untimed.json")
	out, _, err := runPipeline(t, "compare", a, b, "--format", "json")
	if err != nil {
		t.Fatal(err)
	}
	var comparison struct {
		Improved  []json.RawMessage `json:"funcImproved"`
		Regressed []json.RawMessage `json:"funcRegressed"`
	}
	if err := json.Unmarshal([]byte(out), &comparison); err != nil || len(comparison.Improved) != 0 || len(comparison.Regressed) != 0 {
		t.Fatalf("missing timestamps create phantom function changes: %v %s", err, out)
	}
}

func TestUntimedPipelinePartialMetadata(t *testing.T) {
	file := writeSampleTimestampInput(t, testgen.UntimedSamples(), "window.json")
	for _, operation := range []string{"analyze", "audit"} {
		out, _, err := runPipeline(t, operation, file, "--from", ".1", "--to", ".4", "--format", "json")
		if err != nil {
			t.Fatal(err)
		}
		var document any
		if err := json.Unmarshal([]byte(out), &document); err != nil {
			t.Fatal(err)
		}
		found := false
		var walk func(any)
		walk = func(value any) {
			switch node := value.(type) {
			case []any:
				for _, child := range node {
					walk(child)
				}
			case map[string]any:
				if coverage, ok := node["sampleCoverage"].(map[string]any); ok {
					found = true
					if coverage["untimedSampleCount"] != float64(40) || coverage["untimedMs"] != float64(40) || coverage["untimedExcluded"] != true {
						t.Errorf("%s discarded untimed capture metadata: %v", operation, coverage)
					}
				}
				for _, child := range node {
					walk(child)
				}
			}
		}
		walk(document)
		if !found {
			t.Fatalf("%s has no sample coverage", operation)
		}
	}
	out, stderr, err := runPipeline(t, "analyze", file, "--from", ".1", "--to", ".4", "--format", "folded")
	if err != nil || out != "" || !strings.Contains(stderr, "40 samples (40 ms) have no timestamp") {
		t.Fatalf("analyze folded hides exclusion: %v stdout=%q stderr=%q", err, out, stderr)
	}
}

func TestUntimedPipelineTextCoverageNote(t *testing.T) {
	file := writeSampleTimestampInput(t, testgen.UntimedSamples(), "profile.json")
	for _, format := range []string{"term", "md"} {
		out, _, err := runPipeline(t, "analyze", file, "--format", format, "--no-color")
		if err != nil {
			t.Fatal(err)
		}
		for _, text := range []string{"Untimed samples: 40", "excluded from frame, task, trigger, window and periodic attribution"} {
			if !strings.Contains(out, text) {
				t.Errorf("%s lacks %q", format, text)
			}
		}
	}
}
