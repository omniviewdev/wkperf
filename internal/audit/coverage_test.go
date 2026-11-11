package audit

import (
	"bytes"
	"encoding/json"
	"math"
	"strings"
	"testing"

	"github.com/omniviewdev/wkperf/internal/insights"
	"github.com/omniviewdev/wkperf/internal/scorecard"
)

func coverageOf(t *testing.T, c *scorecard.Scorecard) float64 {
	t.Helper()
	data, err := json.Marshal(c)
	if err != nil {
		t.Fatal(err)
	}
	var wire struct {
		Coverage *float64 `json:"coverage"`
	}
	if err := json.Unmarshal(data, &wire); err != nil {
		t.Fatal(err)
	}
	if wire.Coverage == nil {
		t.Fatalf("missing coverage: %s", data)
	}
	return *wire.Coverage
}

func TestAggregateCoveragePreservesMissingRunsAndScenes(t *testing.T) {
	good := scorecard.FromValues(map[string]float64{"frame-p95": 16, "jank-ratio": 0, "fps": 60, "tbt": 0})
	empty := scorecard.FromValues(nil)
	for _, scenes := range [][]Scene{
		{{Name: "mixed", Runs: []Run{{Card: good}, {Card: empty}}}},
		{{Name: "observed", Runs: []Run{{Card: good}}}, {Name: "missing", Runs: []Run{{Card: empty}}}},
	} {
		r := Build(scenes, Options{})
		if r.Overall.Grade != "n/a" {
			t.Errorf("missing observations cannot produce healthy aggregate: %+v", r.Overall)
		}
		if got := coverageOf(t, r.Overall); math.Abs(got-11.0/42) > 1e-9 {
			t.Errorf("coverage=%v, want 11/42", got)
		}
		if len(r.Scenes) == 1 && r.Scenes[0].Card.Grade != "n/a" {
			t.Errorf("scene fabricated support from one observed run: %+v", r.Scenes[0].Card)
		}
	}
}

func TestAggregateCannotCombineUnsupportedCardsIntoGrade(t *testing.T) {
	frames := scorecard.FromValues(map[string]float64{"frame-p95": 16, "jank-ratio": 0, "fps": 60})
	other := scorecard.FromValues(map[string]float64{"tbt": 0, "inp": 0, "long-task-rate": 0, "fcp": 0, "lcp": 0, "main-busy": 0, "gc-share": 0, "memory-growth": 0, "forced-layout-rate": 0})
	r := Build([]Scene{{Name: "complementary", Runs: []Run{{Card: frames}, {Card: other}}}}, Options{})
	if r.Overall.Grade != "n/a" || r.Scenes[0].Card.Grade != "n/a" {
		t.Fatal("unsupported runs cannot combine into grade A")
	}
	if got := coverageOf(t, r.Overall); math.Abs(got-.5) > 1e-9 {
		t.Errorf("coverage=%v, want .5", got)
	}
}

func TestRepresentativeIgnoresUnavailableFrameStandards(t *testing.T) {
	runs := []Run{
		{Card: card(map[string]float64{"frame-p95": 20}), Report: &insights.Report{Source: "supported-first", Summary: summary(20)}},
		{Card: card(nil), Report: &insights.Report{Source: "sparse-median", Summary: summary(30)}},
		{Card: card(map[string]float64{"frame-p95": 100}), Report: &insights.Report{Source: "supported-last", Summary: summary(100)}},
	}
	if rep := pickRep(runs); rep == nil || rep.Source != "supported-first" {
		t.Fatalf("representative must not rely on sparse frame p95: %+v", rep)
	}
}

func TestAuditLabelsPartialScores(t *testing.T) {
	r := Build([]Scene{{Name: "unobserved", Runs: []Run{{Card: scorecard.FromValues(map[string]float64{"tbt": 0})}}}}, Options{})
	for _, render := range []func(*bytes.Buffer) error{
		func(b *bytes.Buffer) error { return RenderTerm(b, r, false) },
		func(b *bytes.Buffer) error { return RenderMarkdown(b, r) },
	} {
		var out bytes.Buffer
		if err := render(&out); err != nil {
			t.Fatal(err)
		}
		for _, want := range []string{"n/a", "partial score", "coverage", "100 (partial)"} {
			if !strings.Contains(out.String(), want) {
				t.Errorf("missing %q: %s", want, out.String())
			}
		}
	}
}

func TestAggregateRetainsNilInputAndSupportedGrades(t *testing.T) {
	good := scorecard.FromValues(map[string]float64{"frame-p95": 16, "jank-ratio": 0, "fps": 60, "tbt": 0})
	for _, tc := range []struct {
		name     string
		input    *scorecard.Scorecard
		grade    string
		coverage float64
	}{
		{"missing", nil, "n/a", 11.0 / 42},
		{"supported", good, "A", 11.0 / 21},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := Build([]Scene{{Name: "scene", Runs: []Run{{Card: good}, {Card: tc.input}}}}, Options{})
			if r.Overall.Grade != tc.grade || math.Abs(coverageOf(t, r.Overall)-tc.coverage) > 1e-9 {
				t.Fatalf("aggregate must retain each input's observation support: %+v", r.Overall)
			}
		})
	}
}

func TestMissingRunsReduceEvidenceWithoutImputingParsedRuns(t *testing.T) {
	good := scorecard.FromValues(map[string]float64{"frame-p95": 16, "jank-ratio": 0, "fps": 60, "tbt": 0})
	for _, tc := range []struct {
		name     string
		runs     []Run
		missing  int
		coverage float64
	}{
		{"one-failed", []Run{{Card: good}}, 1, 11.0 / 42},
		{"all-failed", nil, 2, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := Build([]Scene{{Name: tc.name, Runs: tc.runs, MissingRuns: tc.missing}}, Options{})
			if len(r.Scenes) != 1 {
				t.Fatal("failed requested scene cannot disappear")
			}
			s := r.Scenes[0]
			if s.Runs != len(tc.runs) || s.Card.Grade != "n/a" || !s.Card.Partial || math.Abs(s.Card.Coverage-tc.coverage) > 1e-9 {
				t.Fatalf("missing captures affect support, not parsed count: %+v", s)
			}
			if len(tc.runs) > 0 {
				if v, ok := CardValue(s.Card, "frame-p95"); !ok || v != 16 {
					t.Fatalf("missing capture must preserve measured per-standard values: %v, %v", v, ok)
				}
			}
		})
	}
}
