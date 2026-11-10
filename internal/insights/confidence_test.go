package insights

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"github.com/omniviewdev/wkperf/internal/model"
)

func confidenceRecording(seconds float64, samples int, weight float64) *model.Recording {
	rec := &model.Recording{EndTime: seconds, Records: []model.Record{
		frame(0.1, seconds-0.1), script("function-call", 0.1, seconds-0.1, ""),
	}}
	ts := model.TargetSamples{}
	for i := range samples {
		ts.StackTraces = append(ts.StackTraces, sample(0.2+float64(i)*(seconds-0.4)/float64(samples), "work", "src/work.js"))
		ts.Durations = append(ts.Durations, weight)
	}
	rec.Samples = []model.TargetSamples{ts}
	return rec
}

func TestFindingConfidenceUsesMeasuredAndSampledEvidence(t *testing.T) {
	for _, tc := range []struct {
		name              string
		seconds           float64
		samples           int
		weight            float64
		measured, sampled string
	}{
		{"long-strong", 10, 128, 0.06, "medium", "medium"},
		{"short-strong", 2, 128, 0.01, "low", "low"},
		{"long-sparse", 10, 20, 0.3, "medium", "low"},
		{"long-low-coverage", 10, 128, 0.001, "medium", "low"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := Analyze(confidenceRecording(tc.seconds, tc.samples, tc.weight), "confidence.json", Options{})
			for _, want := range []struct{ id, basis, confidence string }{
				{"jank", "measured", tc.measured},
				{"long-tasks", "measured", tc.measured},
				{"hot-function", "sampled", tc.sampled},
			} {
				f := find(r, want.id)
				if f == nil || string(f.Confidence) != want.confidence {
					t.Fatalf("%s confidence = %+v, want %s", want.id, f, want.confidence)
				}
				payload, err := json.Marshal(f)
				if err != nil {
					t.Fatal(err)
				}
				var wire struct {
					EvidenceBasis string `json:"evidenceBasis"`
					Replication   struct {
						Runs, ParsedRuns, RunsWithFinding int
					} `json:"replication"`
				}
				if err := json.Unmarshal(payload, &wire); err != nil {
					t.Fatal(err)
				}
				if wire.EvidenceBasis != want.basis || wire.Replication.Runs != 1 || wire.Replication.ParsedRuns != 1 || wire.Replication.RunsWithFinding != 1 {
					t.Errorf("missing evidence/replication: %s", payload)
				}
				if tc.seconds < 10 && !strings.Contains(strings.Join(f.ConfidenceReasons, " "), "Short capture") {
					t.Error("short-capture caveat disappeared")
				}
			}
		})
	}
}

func TestFindingConfidenceKeepsHypothesesLow(t *testing.T) {
	rec := &model.Recording{EndTime: 12}
	ts := model.TargetSamples{}
	for i := range 120 {
		at := 0.1 + float64(i)*0.09
		rec.Records = append(rec.Records, script("timer-fired", at, at+0.003, "poll"))
		ts.StackTraces = append(ts.StackTraces, model.StackTrace{Timestamp: at + 0.001, StackFrames: []model.StackFrame{{Name: "getAnimations"}, {Name: "pollAnimations", URL: "plugin.js", Line: 1, Column: 900}}})
		ts.Durations = append(ts.Durations, 0.003)
	}
	rec.Samples = []model.TargetSamples{ts}
	r := Analyze(rec, "hypothesis.json", Options{})
	f := find(r, "forced-style-or-layout-read")
	if f == nil || f.Confidence != "low" {
		t.Fatalf("hypothesis confidence = %+v", f)
	}
	payload, err := json.Marshal(f)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(payload), `"evidenceBasis":"hypothesis"`) {
		t.Fatalf("hypothesis basis missing: %s", payload)
	}
}

func TestFindingConfidenceFormatsKeepBasisAndReplication(t *testing.T) {
	report := Analyze(confidenceRecording(10, 128, 0.06), "confidence.json", Options{})
	for _, render := range []func(*bytes.Buffer) error{
		func(b *bytes.Buffer) error { return RenderTerm(b, report, false) },
		func(b *bytes.Buffer) error { return RenderMarkdown(b, report) },
		func(b *bytes.Buffer) error { return RenderMarkdownWithOptions(b, report, MarkdownOptions{Brief: true}) },
	} {
		var out bytes.Buffer
		if err := render(&out); err != nil {
			t.Fatal(err)
		}
		for _, want := range []string{"basis measured", "1 of 1 runs (1 parsed)"} {
			if !strings.Contains(out.String(), want) {
				t.Errorf("missing %q", want)
			}
		}
	}
}
