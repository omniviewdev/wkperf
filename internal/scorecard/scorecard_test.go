package scorecard

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"github.com/omniviewdev/wkperf/internal/insights"
	"github.com/omniviewdev/wkperf/internal/metrics"
	"github.com/omniviewdev/wkperf/internal/model"
)

func get(t *testing.T, c *Scorecard, id string) Result {
	t.Helper()
	for _, r := range c.Results {
		if r.ID == id {
			return r
		}
	}
	t.Fatalf("no result %s", id)
	return Result{}
}

func script(et string, s, e float64, detail string) model.Record {
	r := model.Record{Type: model.TypeScript, EventType: et, StartTime: s, EndTime: e}
	if detail != "" {
		r.Details, _ = json.Marshal(detail)
	}
	return r
}

func layout(et string, s float64) model.Record {
	return model.Record{Type: model.TypeLayout, EventType: et, StartTime: s, EndTime: s + 0.001}
}

func report(dur float64, ms ...metrics.Metric) *insights.Report {
	return &insights.Report{DurationSec: dur, Summary: &metrics.Summary{Metrics: ms}}
}

func TestRatingBoundaries(t *testing.T) {
	for _, s := range Standards() {
		var g, n, p float64
		if s.HigherBetter {
			g, n, p = s.Good, (s.Good+s.Poor)/2, s.Poor-0.001
		} else {
			g, n, p = s.Good, (s.Good+s.Poor)/2, s.Poor+0.001
		}
		if rate(s, g) != Good || rate(s, n) != NeedsImprovement || rate(s, p) != Poor {
			t.Errorf("%s: edges %v/%v/%v rated %s/%s/%s", s.ID, g, n, p, rate(s, g), rate(s, n), rate(s, p))
		}
		if rate(s, s.Poor) != NeedsImprovement {
			t.Errorf("%s: value at Poor threshold must be needs-improvement", s.ID)
		}
	}
	fps := stdByID("fps")
	if rate(fps, 54.9) != NeedsImprovement || rate(fps, 29.9) != Poor || rate(fps, 60) != Good {
		t.Error("fps HigherBetter ratings wrong")
	}
}

func TestScoreMonotonicAndClamped(t *testing.T) {
	for _, s := range Standards() {
		span := s.Poor - s.Good
		prev := 101.0
		for i := -2; i <= 40; i++ {
			v := s.Good + span*float64(i)/10
			sc := score(s, v)
			if sc < 0 || sc > 100 {
				t.Fatalf("%s: score %v out of range at %v", s.ID, sc, v)
			}
			if sc > prev {
				t.Fatalf("%s: score rose from %v to %v at %v", s.ID, prev, sc, v)
			}
			prev = sc
		}
		if score(s, s.Good) != 100 || score(s, s.Poor) != 50 {
			t.Errorf("%s: good=%v poor=%v", s.ID, score(s, s.Good), score(s, s.Poor))
		}
		if score(s, s.Good+span*5) != 0 {
			t.Errorf("%s: far-worse value must score 0", s.ID)
		}
	}
}

func TestGradeCutoffs(t *testing.T) {
	cases := map[float64]string{100: "A", 90: "A", 89.9: "B", 75: "B", 74.9: "C", 60: "C", 59.9: "D", 40: "D", 39.9: "F", 0: "F"}
	for s, want := range cases {
		if got := grade(s); got != want {
			t.Errorf("grade(%v)=%s want %s", s, got, want)
		}
	}
}

func TestTBTAndLongTasks(t *testing.T) {
	rec := &model.Recording{StartTime: 0, EndTime: 20, Records: []model.Record{
		script("timer-fired", 1, 1.1, ""),         // 100ms -> 50
		script("event-dispatched", 2, 2.2, "foo"), // 200ms -> 150, worst
		script("timer-fired", 3, 3.05, ""),        // exactly 50ms: not long
		script("timer-fired", 4, 4.04, ""),
		{Type: model.TypeScript, EventType: "garbage-collected", StartTime: 5, EndTime: 5.5},
	}}
	c := Evaluate(rec, report(20))
	tbt := get(t, c, "tbt")
	if tbt.Value < 99.99 || tbt.Value > 100.01 { // 200ms over 20s => 100 per 10s
		t.Fatalf("tbt %v", tbt.Value)
	}
	if tbt.Detail != "2 long tasks, worst 200ms" {
		t.Errorf("detail %q", tbt.Detail)
	}
	if lt := get(t, c, "long-task-rate"); lt.Value < 5.99 || lt.Value > 6.01 {
		t.Errorf("long-task-rate %v", lt.Value)
	}
}

func TestINPSelectionAndPercentile(t *testing.T) {
	var recs []model.Record
	// 60 clicks of 10..69ms; non-interaction and non-dispatched records must be ignored.
	for i := 0; i < 60; i++ {
		recs = append(recs, script("event-dispatched", float64(i), float64(i)+float64(10+i)/1000, "click"))
	}
	recs = append(recs, script("event-dispatched", 70, 71, "mousemove"), script("timer-fired", 72, 73, "click"))
	c := Evaluate(&model.Recording{EndTime: 80, Records: recs}, report(80))
	inp := get(t, c, "inp")
	// n=60, ceil(.98*60)=59 -> index 58 -> 68ms
	if inp.Value < 67.99 || inp.Value > 68.01 || inp.Detail != "p98 of 60 click handlers" {
		t.Errorf("inp %v %q", inp.Value, inp.Detail)
	}

	few := []model.Record{
		script("event-dispatched", 1, 1.03, "keydown"),
		script("event-dispatched", 2, 2.25, "click"),
		script("event-dispatched", 3, 3.01, "keydown"),
	}
	c = Evaluate(&model.Recording{EndTime: 10, Records: few}, report(10))
	inp = get(t, c, "inp")
	if inp.Value < 249.99 || inp.Value > 250.01 || inp.Detail != "max of 3 click/keydown handlers" {
		t.Errorf("few inp %v %q", inp.Value, inp.Detail)
	}
	if inp.Rating != NeedsImprovement {
		t.Errorf("250ms rating %s", inp.Rating)
	}

	c = Evaluate(&model.Recording{EndTime: 10, Records: []model.Record{script("event-dispatched", 1, 2, "mousemove")}}, report(10))
	if get(t, c, "inp").Rating != Unavailable {
		t.Error("inp must be unavailable without interaction events")
	}
}

func TestFCPLCP(t *testing.T) {
	rec := &model.Recording{StartTime: 100, EndTime: 110, Records: []model.Record{
		layout("first-contentful-paint", 101.2),
		layout("largest-contentful-paint", 103.5),
		layout("forced-layout", 105),
		layout("forced-layout", 106),
	}}
	c := Evaluate(rec, report(10))
	fcp, lcp := get(t, c, "fcp"), get(t, c, "lcp")
	if fcp.Value < 1199 || fcp.Value > 1201 || fcp.Rating != Good {
		t.Errorf("fcp %v %s", fcp.Value, fcp.Rating)
	}
	if lcp.Value < 3499 || lcp.Value > 3501 || lcp.Rating != NeedsImprovement {
		t.Errorf("lcp %v %s", lcp.Value, lcp.Rating)
	}
	if fl := get(t, c, "forced-layout-rate"); fl.Value < 0.199 || fl.Value > 0.201 {
		t.Errorf("forced rate %v", fl.Value)
	}

	c = Evaluate(&model.Recording{EndTime: 10, Records: []model.Record{script("timer-fired", 1, 1.01, "")}}, report(10))
	for _, id := range []string{"fcp", "lcp", "forced-layout-rate"} {
		if get(t, c, id).Rating != Unavailable {
			t.Errorf("%s must be unavailable", id)
		}
	}
}

func TestSummaryDerivedAndMemory(t *testing.T) {
	var mem []metrics.Point
	for i := 0; i < 6; i++ {
		mem = append(mem, metrics.Point{T: float64(i * 10), V: 100 + float64(i)*5}) // 0.5MB/s = 30MB/min
	}
	rep := report(100,
		metrics.Metric{Key: "frames.p95_ms", Value: 40},
		metrics.Metric{Key: "frames.fps", Value: 25},
		metrics.Metric{Key: "frames.jank_ratio", Value: 0.1},
		metrics.Metric{Key: "script.busy_ratio", Value: 0.1},
		metrics.Metric{Key: "gc.total_ms", Value: 5000},
	)
	rep.Summary.MemoryMB = mem
	c := Evaluate(&model.Recording{EndTime: 100, Records: []model.Record{script("timer-fired", 1, 1.01, "")}}, rep)
	if r := get(t, c, "frame-p95"); r.Rating != Poor || r.Value != 40 {
		t.Errorf("frame-p95 %+v", r)
	}
	if r := get(t, c, "fps"); r.Rating != Poor {
		t.Errorf("fps %s", r.Rating)
	}
	if r := get(t, c, "jank-ratio"); r.Rating != NeedsImprovement {
		t.Errorf("jank %s", r.Rating)
	}
	if r := get(t, c, "gc-share"); r.Value < 0.0499 || r.Value > 0.0501 {
		t.Errorf("gc-share %v", r.Value)
	}
	if r := get(t, c, "memory-growth"); r.Value < 29.99 || r.Value > 30.01 || r.Rating != Poor {
		t.Errorf("memory %v %s", r.Value, r.Rating)
	}
	rep.Summary.MemoryMB = mem[:4]
	if get(t, Evaluate(&model.Recording{EndTime: 100}, rep), "memory-growth").Rating != Unavailable {
		t.Error("memory growth needs 5 samples")
	}
	// Without a summary frames are unavailable.
	if get(t, Evaluate(&model.Recording{EndTime: 10}, nil), "frame-p95").Rating != Unavailable {
		t.Error("frame-p95 without summary must be unavailable")
	}
}

func TestGapOrderingAndFactor(t *testing.T) {
	c := FromValues(map[string]float64{
		"frame-p95":  40,   // 2.4x
		"fps":        27.5, // 2x (higher better)
		"jank-ratio": 0.1,  // 2x
		"tbt":        100,  // good: not in gap
		"inp":        1000, // 5x
	})
	var ids []string
	for _, g := range c.Gap {
		ids = append(ids, g.ID)
	}
	if strings.Join(ids, ",") != "inp,frame-p95,fps,jank-ratio" {
		t.Fatalf("gap order %v", ids)
	}
	if f := c.Gap[1].Factor; f < 2.39 || f > 2.4 {
		t.Errorf("factor %v", f)
	}
	if c.Gap[2].Factor != 2 {
		t.Errorf("fps factor %v", c.Gap[2].Factor)
	}
}

func TestFromValuesMissingAndRoundTrip(t *testing.T) {
	in := map[string]float64{"frame-p95": 20, "tbt": 10}
	c := FromValues(in)
	if len(c.Results) != len(Standards()) {
		t.Fatal("all standards must appear")
	}
	if get(t, c, "inp").Rating != Unavailable || get(t, c, "fps").Rating != Unavailable {
		t.Error("missing ids must be unavailable")
	}
	out := Values(c)
	if len(out) != 2 || out["frame-p95"] != 20 || out["tbt"] != 10 {
		t.Errorf("values %v", out)
	}
	// Unavailable results do not dilute the score: frame-p95 20ms scores 100-50*(3.3/16.7), tbt 100.
	want := (3*(100-50*(20-16.7)/(33.4-16.7)) + 2*100) / 5
	if c.Score < want-0.01 || c.Score > want+0.01 {
		t.Errorf("score %v want %v", c.Score, want)
	}
	if empty := FromValues(nil); empty.Score != 0 || empty.Grade != "F" || len(empty.Gap) != 0 {
		t.Errorf("empty %+v", empty)
	}
}

func TestRender(t *testing.T) {
	c := FromValues(map[string]float64{"frame-p95": 40, "fps": 60})
	var term bytes.Buffer
	if err := RenderTerm(&term, c, true); err != nil {
		t.Fatal(err)
	}
	s := term.String()
	for _, want := range []string{"grade", "How far from a performant UI", "Frame time p95 is 2.4x the 16.7ms target", "\x1b[31m", "\x1b[2m"} {
		if !strings.Contains(s, want) {
			t.Errorf("term missing %q:\n%s", want, s)
		}
	}
	term.Reset()
	_ = RenderTerm(&term, c, false)
	if strings.Contains(term.String(), "\x1b") {
		t.Error("no-color output has ANSI")
	}
	var md bytes.Buffer
	if err := RenderMarkdown(&md, c); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(md.String(), "| Frame time p95 |") || !strings.Contains(md.String(), "### How far from a performant UI") {
		t.Errorf("markdown:\n%s", md.String())
	}
}
