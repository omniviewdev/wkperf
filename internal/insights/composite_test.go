package insights

import (
	"bytes"
	"encoding/json"
	"math"
	"strings"
	"testing"

	"github.com/omniviewdev/wkperf/internal/metrics"
	"github.com/omniviewdev/wkperf/internal/model"
	"github.com/omniviewdev/wkperf/internal/testgen"
)

func TestStalledCompositesDoNotInflateTheBreakdown(t *testing.T) {
	r := Analyze(testgen.StalledComposites(), "stalled.json", Options{})
	slices := map[string]Slice{}
	for _, s := range r.Breakdown {
		slices[s.Name] = s
	}
	for name, want := range map[string]float64{"Composite": 55, "Script": 100, "Idle": 29845} {
		if got := slices[name].Ms; math.Abs(got-want) > 1e-6 {
			t.Errorf("%s = %gms, want %gms (%+v)", name, got, want, r.Breakdown)
		}
	}
	if pct := slices["Composite"].Pct; pct > 0.2 {
		t.Errorf("stalled composites leaked into the Composite share: %g%%", pct)
	}
	if want := (metrics.StalledComposites{ThresholdMs: 50, Count: 2, TotalMs: 26000}); r.StalledComposites != want {
		t.Errorf("report stalled composites = %+v, want %+v", r.StalledComposites, want)
	}
}

func TestStalledCompositeIsNotJankOrFrameWork(t *testing.T) {
	for _, tc := range []struct {
		name    string
		records []model.Record
		jank    bool
	}{
		// One 100 ms rendering interval wholly inside a stalled composite.
		{"stalled", []model.Record{frame(0, .1), layout("composite", 0, .1)}, false},
		// The same interval filled by two composites of 49 ms: real paint/composite work.
		{"short", []model.Record{frame(0, .1), layout("composite", 0, .049), layout("composite", .05, .099)}, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rec := testgen.Empty()
			rec.EndTime = 2
			rec.Records = tc.records
			r := Analyze(rec, tc.name+".json", Options{})
			f := find(r, "jank")
			if tc.jank {
				if f == nil || !strings.Contains(strings.Join(f.Evidence, " "), "paint/composite") {
					t.Fatalf("short composites are frame work and must be attributed: %+v", f)
				}
				return
			}
			if f != nil || len(r.SlowFrames) != 0 {
				t.Fatalf("stalled composite became frame work: finding %+v slow frames %+v", f, r.SlowFrames)
			}
			active, _ := r.Summary.Lookup("frames.count")
			idle, _ := r.Summary.Lookup("frames.idle_wakeups")
			if active.Value != 0 || idle.Value != 1 {
				t.Errorf("an interval without measured work is an idle gap, not an active frame: active %v idle %v", active.Value, idle.Value)
			}
		})
	}
}

func TestStalledCompositeLineInReports(t *testing.T) {
	r := Analyze(testgen.StalledComposites(), "stalled.json", Options{})
	var md, term bytes.Buffer
	if err := RenderMarkdown(&md, r); err != nil {
		t.Fatal(err)
	}
	if err := RenderTerm(&term, r, false); err != nil {
		t.Fatal(err)
	}
	for name, text := range map[string]string{"markdown": md.String(), "terminal": term.String()} {
		if !strings.Contains(text, "Stalled composite records: 2 longer than 50ms (26000ms of reported spans, summed)") {
			t.Errorf("%s report lacks the stalled composite line:\n%s", name, text)
		}
		if strings.Count(text, "Stalled composite records:") != 1 {
			t.Errorf("%s report must have exactly one stalled composite line", name)
		}
	}
	if !strings.Contains(md.String(), "layout.composite.stalled_count") || !strings.Contains(md.String(), "layout.composite.stalled_ms") {
		t.Errorf("stalled line must cite its metric keys:\n%s", md.String())
	}

	md.Reset()
	if err := RenderMarkdown(&md, Analyze(testgen.NestedRecords(), "nested.json", Options{})); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(md.String(), "Stalled composite records") {
		t.Error("a capture without stalled composites must not print the line")
	}
}

func TestStalledCompositesAreAJSONField(t *testing.T) {
	data, err := json.Marshal(ToWire(Analyze(testgen.StalledComposites(), "stalled.json", Options{})))
	if err != nil {
		t.Fatal(err)
	}
	var wire struct {
		Stalled map[string]float64 `json:"stalledComposites"`
	}
	if err := json.Unmarshal(data, &wire); err != nil {
		t.Fatal(err)
	}
	if wire.Stalled["thresholdMs"] != 50 || wire.Stalled["count"] != 2 || wire.Stalled["totalMs"] != 26000 {
		t.Fatalf("stalledComposites JSON = %v", wire.Stalled)
	}
}
