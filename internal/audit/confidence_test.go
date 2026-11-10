package audit

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/omniviewdev/wkperf/internal/insights"
	"github.com/omniviewdev/wkperf/internal/model"
)

func confidenceRuns(present ...bool) []Run {
	runs := make([]Run, 0, len(present))
	for i, finding := range present {
		rec := &model.Recording{EndTime: 12}
		if finding {
			rec.Records = []model.Record{{Type: model.TypeScript, EventType: "function-call", StartTime: 1, EndTime: 1.2 + float64(i)*0.1}}
		}
		source := fmt.Sprintf("run-%d.json", i)
		runs = append(runs, Run{Source: source, Report: insights.Analyze(rec, source, insights.Options{}), Card: card(map[string]float64{"frame-p95": float64(i)*10 + 10})})
	}
	return runs
}

func priorityByID(t *testing.T, r *Report, id string) Priority {
	t.Helper()
	for _, p := range r.Priorities {
		if p.ID == id {
			return p
		}
	}
	t.Fatalf("missing priority %q: %+v", id, r.Priorities)
	return Priority{}
}

func TestAuditConfidenceCountsActualFindingRuns(t *testing.T) {
	for _, tc := range []struct {
		name       string
		present    []bool
		missing    int
		confidence string
	}{
		{"four-measured", []bool{true, true, true, true}, 0, "high"},
		{"one-of-three", []bool{false, true, false}, 0, "low"},
		{"three-of-four-with-failure", []bool{true, true, true}, 1, "high"},
		{"three-of-five-with-failures", []bool{true, true, true}, 2, "low"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			runs := confidenceRuns(tc.present...)
			r := Build([]Scene{{Name: "scroll", Runs: runs, MissingRuns: tc.missing}}, Options{})
			p := priorityByID(t, r, "long-tasks")
			if string(p.Confidence) != tc.confidence {
				t.Errorf("confidence=%s, want %s", p.Confidence, tc.confidence)
			}
			present := 0
			for _, yes := range tc.present {
				if yes {
					present++
				}
			}
			payload, err := json.Marshal(p)
			if err != nil {
				t.Fatal(err)
			}
			var wire struct {
				EvidenceBasis string                                          `json:"evidenceBasis"`
				Replication   struct{ Runs, ParsedRuns, RunsWithFinding int } `json:"replication"`
			}
			if err := json.Unmarshal(payload, &wire); err != nil {
				t.Fatal(err)
			}
			if wire.EvidenceBasis != "measured" || wire.Replication.Runs != len(runs)+tc.missing || wire.Replication.ParsedRuns != len(runs) || wire.Replication.RunsWithFinding != present {
				t.Errorf("replication must count actual attempts and findings: %s", payload)
			}
			reasons := strings.Join(p.ConfidenceReasons, " ")
			if !strings.Contains(reasons, fmt.Sprintf("%d of %d runs", present, len(runs)+tc.missing)) || strings.Contains(reasons, "Single-run") {
				t.Errorf("stale run basis: %s", reasons)
			}
			if tc.missing > 0 && (!strings.Contains(reasons, "incomplete") || !strings.Contains(reasons, "parsed")) {
				t.Errorf("missing evidence caveat: %s", reasons)
			}
			if r.Scenes[0].Runs != len(runs) {
				t.Error("failed inputs inflated parsed run count")
			}
			if tc.missing > 0 && (!r.Scenes[0].Card.Partial || !r.Overall.Partial) {
				t.Error("confidence inflated scorecard coverage")
			}
		})
	}
}

func TestAuditConfidenceKeepsRepresentativeEvidenceAndInputOrder(t *testing.T) {
	runs := confidenceRuns(true, true, true, true)
	first := Build([]Scene{{Name: "scene", Runs: runs}}, Options{})
	p := priorityByID(t, first, "long-tasks")
	if !strings.Contains(p.NumericEvidence[0].Command, "run-1.json") {
		t.Fatalf("numeric evidence not from selected representative: %+v", p.NumericEvidence)
	}
	for left, right := 0, len(runs)-1; left < right; left, right = left+1, right-1 {
		runs[left], runs[right] = runs[right], runs[left]
	}
	second := Build([]Scene{{Name: "scene", Runs: runs}}, Options{})
	a, err := json.Marshal(first)
	if err != nil {
		t.Fatal(err)
	}
	b, err := json.Marshal(second)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(a, b) {
		t.Fatal("input order changed audit evidence, replication, or representative")
	}
}

func TestAuditConfidenceDoesNotInventRelatedFindingPresence(t *testing.T) {
	runs := confidenceRuns(false, true, false)
	for _, i := range []int{0, 2} {
		runs[i].Report.Findings = []insights.Finding{{ID: "forced-style-or-layout-read", RelatedFindings: []string{"long-tasks"}, Confidence: "low"}}
	}
	r := Build([]Scene{{Name: "scene", Runs: runs}}, Options{})
	p := priorityByID(t, r, "long-tasks")
	if p.Confidence != "low" || !strings.Contains(strings.Join(p.ConfidenceReasons, " "), "1 of 3 runs") {
		t.Fatalf("related facets fabricated measured cause replication: %+v", p)
	}
}

func TestAuditConfidenceDoesNotPoolWeakSampleSupport(t *testing.T) {
	for _, tc := range []struct {
		name   string
		counts []int
		want   string
	}{
		{"four-strong", []int{120, 120, 120, 120}, "medium"},
		{"four-sparse", []int{30, 30, 30, 30}, "low"},
		{"strong-representative-weak-others", []int{30, 120, 30, 30}, "low"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var runs []Run
			for i, count := range tc.counts {
				rec := &model.Recording{EndTime: 12, Records: []model.Record{{Type: model.TypeScript, EventType: "function-call", StartTime: 1, EndTime: 9}}}
				ts := model.TargetSamples{}
				for n := range count {
					ts.StackTraces = append(ts.StackTraces, model.StackTrace{Timestamp: 1.1 + float64(n)*7/float64(count), StackFrames: []model.StackFrame{{Name: "work", URL: "src/work.js", Line: 1}}})
					ts.Durations = append(ts.Durations, 6/float64(count))
				}
				rec.Samples = []model.TargetSamples{ts}
				source := fmt.Sprintf("sample-%d.json", i)
				runs = append(runs, Run{Source: source, Report: insights.Analyze(rec, source, insights.Options{}), Card: card(map[string]float64{"frame-p95": float64(i)*10 + 10})})
			}
			p := priorityByID(t, Build([]Scene{{Name: "scene", Runs: runs}}, Options{}), "hot-function")
			if string(p.Confidence) != tc.want || p.Replication.RunsWithFinding != 4 || p.EvidenceBasis != insights.EvidenceSampled {
				t.Fatalf("sample support must remain per capture: %+v", p)
			}
		})
	}
}

func TestAuditConfidenceFormatsKeepBasisAndReplication(t *testing.T) {
	report := Build([]Scene{{Name: "scene", Runs: confidenceRuns(true, true, true, true)}}, Options{})
	for _, render := range []func(*bytes.Buffer) error{
		func(b *bytes.Buffer) error { return RenderTerm(b, report, false) },
		func(b *bytes.Buffer) error { return RenderMarkdown(b, report) },
	} {
		var out bytes.Buffer
		if err := render(&out); err != nil {
			t.Fatal(err)
		}
		for _, want := range []string{"evidence basis: measured", "4 of 4 runs (4 parsed)", "4 parsed of 4 attempted"} {
			if !strings.Contains(out.String(), want) {
				t.Errorf("missing %q", want)
			}
		}
	}
}
