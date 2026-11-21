package insights

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"github.com/omniviewdev/wkperf/internal/model"
	"github.com/omniviewdev/wkperf/internal/testgen"
)

func renderBoth(t *testing.T, r *Report) (markdown, terminal string) {
	t.Helper()
	var md, term bytes.Buffer
	if err := RenderMarkdown(&md, r); err != nil {
		t.Fatal(err)
	}
	if err := RenderTerm(&term, r, false); err != nil {
		t.Fatal(err)
	}
	return md.String(), term.String()
}

func TestCompositeCadenceSectionLabelsTheEstimate(t *testing.T) {
	r := Analyze(testgen.CompositeCadenceRuns(), "cadence.json", Options{})
	if r.CompositeCadence == nil {
		t.Fatal("report lacks the estimate")
	}
	md, term := renderBoth(t, r)
	for name, text := range map[string]string{"markdown": md, "terminal": term} {
		for _, want := range []string{
			"Composite cadence (estimate)",
			"Basis: estimated. This capture has no rendering-frame records, so frame p95, jank and the grade are unavailable.",
			"does not prove missed display vsyncs",
			"never fills the frame standards or the grade",
			"Composite starts: 23; 20 intervals within 2 active runs spanning 0.5s.",
			"Interval p50 17ms, p95 61.5ms, max 90ms.",
			"Intervals over 25.05ms (1.5 nominal 60 Hz frame budgets): 5 of 20 (25%).",
			"Intervals over 50ms: 2.",
		} {
			if !strings.Contains(text, want) {
				t.Errorf("%s report lacks %q:\n%s", name, want, text)
			}
		}
	}
	if !strings.Contains(md, "## Composite cadence (estimate)") {
		t.Error("markdown needs its own section heading")
	}
	for _, key := range []string{"cadence.composite.starts", "cadence.composite.count", "cadence.composite.p50_ms", "cadence.composite.p95_ms", "cadence.composite.max_ms", "cadence.composite.missed_vsync_count", "cadence.composite.over_50ms_count"} {
		if !strings.Contains(md, key) {
			t.Errorf("section must cite %s so the value can be reproduced", key)
		}
	}
	if brief := briefMarkdown(t, r); strings.Contains(brief, "Composite cadence (estimate)") {
		t.Error("brief output keeps to findings, hot path, functions and verification")
	}
}

func briefMarkdown(t *testing.T, r *Report) string {
	t.Helper()
	var md bytes.Buffer
	if err := RenderMarkdownWithOptions(&md, r, MarkdownOptions{Brief: true}); err != nil {
		t.Fatal(err)
	}
	return md.String()
}

func TestCompositeCadenceSectionForIdleCapture(t *testing.T) {
	r := Analyze(testgen.StalledComposites(), "idle.json", Options{})
	md, _ := renderBoth(t, r)
	if !strings.Contains(md, "No two composite starts were less than 100ms apart, so there is no cadence to estimate.") ||
		!strings.Contains(md, "Composite starts: 4; 0 intervals within 0 active runs spanning 0s.") ||
		strings.Contains(md, "Interval p50") {
		t.Errorf("idle capture must say there is no cadence rather than print zero percentiles:\n%s", md)
	}
}

func TestCompositeCadenceSectionAbsentWhenNotEstimated(t *testing.T) {
	framed := testgen.CompositeCadenceRuns()
	framed.Records = append(framed.Records, model.Record{Type: model.TypeRenderingFrame, StartTime: 5, EndTime: 5.016})
	scriptOnly := testgen.Empty()
	scriptOnly.Records = []model.Record{{Type: model.TypeScript, EventType: "timer-fired", StartTime: 1, EndTime: 1.01}}
	for name, rec := range map[string]*model.Recording{"rendering frames exist": framed, "no composite records": scriptOnly} {
		r := Analyze(rec, name+".json", Options{})
		if r.CompositeCadence != nil {
			t.Errorf("%s: unexpected estimate %+v", name, r.CompositeCadence)
		}
		if md, term := renderBoth(t, r); strings.Contains(md, "Composite cadence (estimate)") || strings.Contains(term, "Composite cadence (estimate)") {
			t.Errorf("%s: unexpected cadence section", name)
		}
	}
}

func TestCompositeCadenceIsAJSONFieldOnlyWhenEstimated(t *testing.T) {
	wire := func(rec *model.Recording) map[string]any {
		data, err := json.Marshal(ToWire(Analyze(rec, "fixture.json", Options{})))
		if err != nil {
			t.Fatal(err)
		}
		var out map[string]any
		if err := json.Unmarshal(data, &out); err != nil {
			t.Fatal(err)
		}
		return out
	}
	cadence, ok := wire(testgen.CompositeCadenceRuns())["compositeCadence"].(map[string]any)
	if !ok {
		t.Fatal("analysis JSON lacks compositeCadence")
	}
	for key, want := range map[string]any{
		"basis": "estimated", "starts": 23.0, "runs": 2.0, "count": 20.0, "missedVsyncCount": 5.0, "missedVsyncRatio": .25, "over50MsCount": 2.0,
	} {
		if cadence[key] != want {
			t.Errorf("compositeCadence.%s = %v, want %v", key, cadence[key], want)
		}
	}
	for _, key := range []string{"p50Ms", "p95Ms", "maxMs", "activeRunSec"} {
		if _, ok := cadence[key].(float64); !ok {
			t.Errorf("compositeCadence.%s missing: %v", key, cadence)
		}
	}
	framed := testgen.CompositeCadenceRuns()
	framed.Records = append(framed.Records, model.Record{Type: model.TypeRenderingFrame, StartTime: 5, EndTime: 5.016})
	if _, present := wire(framed)["compositeCadence"]; present {
		t.Error("compositeCadence must be absent when rendering-frame records exist")
	}
}
