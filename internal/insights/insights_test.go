package insights

import (
	"bytes"
	"encoding/json"
	"math"
	"strings"
	"testing"

	"github.com/omniviewdev/wkperf/internal/model"
)

func script(et string, s, e float64, detail string) model.Record {
	r := model.Record{Type: model.TypeScript, EventType: et, StartTime: s, EndTime: e}
	if detail != "" {
		r.Details, _ = json.Marshal(detail)
	}
	return r
}
func layout(et string, s, e float64) model.Record {
	return model.Record{Type: model.TypeLayout, EventType: et, StartTime: s, EndTime: e}
}
func frame(s, e float64) model.Record {
	return model.Record{Type: model.TypeRenderingFrame, StartTime: s, EndTime: e}
}
func gcRec(s, e float64) model.Record {
	return model.Record{Type: model.TypeScript, StartTime: s, EndTime: e, Details: json.RawMessage(`{"__type":"GarbageCollection"}`)}
}
func sample(t float64, name, url string) model.StackTrace {
	return model.StackTrace{Timestamp: t, StackFrames: []model.StackFrame{{Name: name, URL: url, Line: 1}, {Name: "root", URL: "app.js"}}}
}

func find(r *Report, id string) *Finding {
	for i := range r.Findings {
		if r.Findings[i].ID == id {
			return &r.Findings[i]
		}
	}
	return nil
}

func healthy() *model.Recording {
	rec := &model.Recording{DisplayName: "ok", StartTime: 0, EndTime: 10}
	for i := 0; i < 600; i++ {
		t := float64(i) * 0.016
		rec.Records = append(rec.Records, frame(t, t+0.0166), layout("layout", t+0.001, t+0.0015), script("animation-frame-fired", t+0.002, t+0.003, ""))
	}
	var st []model.StackTrace
	for i := 0; i < 600; i++ {
		n := string(rune('a' + i%20))
		st = append(st, sample(float64(i)*0.016, "f"+n, "http://x/node_modules/p"+n+"/i.js"))
	}
	rec.Samples = []model.TargetSamples{{StackTraces: st}}
	return rec
}

func TestBreakdownNoDoubleCount(t *testing.T) {
	rec := &model.Recording{StartTime: 0, EndTime: 0.2, Records: []model.Record{
		script("function-call", 0, 0.1, ""),
		script("function-call", 0.02, 0.06, ""), // nested, must merge
		layout("layout", 0.05, 0.15),
		gcRec(0.08, 0.12),
	}}
	got := map[string]float64{}
	sum := 0.0
	for _, s := range computeBreakdown(rec) {
		got[s.Name] = s.Ms
		sum += s.Pct
	}
	want := map[string]float64{"Script": 50, "Layout": 100, "Idle": 50}
	for k, v := range want {
		if math.Abs(got[k]-v) > 1e-6 {
			t.Errorf("%s = %v, want %v", k, got[k], v)
		}
	}
	if math.Abs(sum-100) > 1e-6 {
		t.Errorf("pct sum = %v", sum)
	}
}

func TestBreakdownOtherIsUnclaimedFrameTime(t *testing.T) {
	rec := &model.Recording{StartTime: 0, EndTime: 1, Records: []model.Record{
		frame(0, 0.5), script("function-call", 0, 0.2, ""),
	}}
	got := map[string]float64{}
	for _, s := range computeBreakdown(rec) {
		got[s.Name] = s.Ms
	}
	if math.Abs(got["Active-frame time without instrumented main-thread work"]-300) > 1e-6 || math.Abs(got["Idle"]-500) > 1e-6 {
		t.Errorf("got %v", got)
	}
}

func TestModuleOf(t *testing.T) {
	cases := map[string]string{
		"":              "(native)",
		"[native code]": "(native)",
		"http://x/node_modules/react-dom/cjs/a.js?v=1":     "http://x/node_modules/react-dom/cjs/a.js",
		"http://x/a/node_modules/@mui/material/Box.js#h":   "http://x/a/node_modules/@mui/material/Box.js",
		"http://x/node_modules/a/node_modules/lodash/x.js": "http://x/node_modules/a/node_modules/lodash/x.js",
		"http://localhost:5173/src/app/main.js?t=3":        "http://localhost:5173/src/app/main.js",
	}
	for in, want := range cases {
		if got := moduleOf(in); got != want {
			t.Errorf("moduleOf(%q)=%q want %q", in, got, want)
		}
	}
}

func TestSlowFrameAttribution(t *testing.T) {
	rec := &model.Recording{StartTime: 1, EndTime: 3, Records: []model.Record{
		frame(1, 1.1), // 100ms
		script("event-dispatched", 0.95, 1.05, "mousemove"), // clipped to 50ms in frame
		layout("recalculate-styles", 1.05, 1.07),
		layout("paint", 1.07, 1.08),
		gcRec(1.08, 1.09),
		frame(2, 2.01), // fast
	}, Samples: []model.TargetSamples{{
		StackTraces: []model.StackTrace{sample(1.01, "hot", "http://x/a.js"), sample(1.02, "hot", "http://x/a.js"), sample(1.03, "cold", "http://x/a.js"), sample(2.0, "other", "http://x/a.js")},
		Durations:   []float64{0.001, 0.001, 0.001, 0.001},
	}}}
	r := Analyze(rec, "s", Options{})
	if len(r.SlowFrames) != 1 {
		t.Fatalf("slow frames %d", len(r.SlowFrames))
	}
	f := r.SlowFrames[0]
	if math.Abs(f.T) > 1e-9 || math.Abs(f.Ms-100) > 1e-6 {
		t.Errorf("frame %+v", f)
	}
	if math.Abs(f.ScriptMs-50) > 1e-6 || math.Abs(f.LayoutMs-20) > 1e-6 || math.Abs(f.PaintMs-10) > 1e-6 || math.Abs(f.GCMs-10) > 1e-6 {
		t.Errorf("attribution %+v", f)
	}
	if f.TopEvent != "event-dispatched mousemove" || !strings.HasPrefix(f.TopFunc, "hot ") {
		t.Errorf("top %q / %q", f.TopEvent, f.TopFunc)
	}
}

func TestLongTasks(t *testing.T) {
	rec := &model.Recording{StartTime: 0, EndTime: 1, Records: []model.Record{
		script("timer-fired", 0.5, 0.7, "42"), script("function-call", 1, 1.01, ""),
	}}
	r := Analyze(rec, "s", Options{})
	if len(r.LongTasks) != 1 || r.LongTasks[0].Detail != "42" || math.Abs(r.LongTasks[0].Ms-200) > 1e-6 {
		t.Fatalf("%+v", r.LongTasks)
	}
	f := find(r, "long-tasks")
	if f == nil || f.Severity != SevHigh || len(f.Evidence) == 0 || f.Suggestion == "" {
		t.Errorf("finding %+v", f)
	}
}

func TestHealthyHasNoFindings(t *testing.T) {
	r := Analyze(healthy(), "h", Options{})
	for _, f := range r.Findings {
		t.Errorf("unexpected finding %s", f.ID)
	}
	if len(r.SlowFrames) != 0 || len(r.LongTasks) != 0 {
		t.Error("slow frames/long tasks on healthy data")
	}
}

func TestEmptyAndNil(t *testing.T) {
	for _, rec := range []*model.Recording{nil, {}} {
		r := Analyze(rec, "e", Options{})
		var b bytes.Buffer
		if err := RenderTerm(&b, r, true); err != nil {
			t.Fatal(err)
		}
		if err := RenderMarkdown(&b, r); err != nil {
			t.Fatal(err)
		}
	}
}

func TestForcedLayoutAndJank(t *testing.T) {
	rec := &model.Recording{StartTime: 0, EndTime: 2}
	for i := 0; i < 20; i++ {
		s := float64(i) * 0.1
		rec.Records = append(rec.Records, frame(s, s+0.04),
			script("event-dispatched", s, s+0.01, "scroll"),
			layout("forced-layout", s+0.015, s+0.03))
	}
	r := Analyze(rec, "x", Options{})
	fl := find(r, "forced-layout")
	if fl == nil || fl.ImpactMs < 299 {
		t.Fatalf("forced-layout %+v", fl)
	}
	j := find(r, "jank")
	if j == nil || !strings.Contains(strings.Join(j.Evidence, " "), "layout") {
		t.Fatalf("jank %+v", j)
	}
	if find(r, "event-handler-heavy") == nil { // 200ms / 2000ms = 10%
		t.Error("event-handler-heavy expected")
	}
	if find(r, "layout-heavy") != nil {
		t.Error("forced layout must not also be reported as layout-heavy")
	}
	lay := &model.Recording{StartTime: 0, EndTime: 1, Records: []model.Record{layout("layout", 0, 0.1)}}
	if find(Analyze(lay, "x", Options{}), "layout-heavy") == nil {
		t.Error("layout-heavy expected at 10% layout")
	}
}

func TestGCTimerRAFRules(t *testing.T) {
	rec := &model.Recording{StartTime: 0, EndTime: 1}
	for i := 0; i < 100; i++ {
		s := float64(i) * 0.01
		rec.Records = append(rec.Records, script("timer-fired", s, s+0.0001, "1"), script("timer-installed", s, s, "1"))
	}
	for i := 0; i < 5; i++ {
		s := float64(i) * 0.2
		rec.Records = append(rec.Records, gcRec(s, s+0.01))
	}
	rec.Records = append(rec.Records, script("animation-frame-fired", 0.5, 0.6, ""))
	r := Analyze(rec, "x", Options{})
	for _, id := range []string{"timer-storm", "gc-pressure", "raf-load"} {
		if find(r, id) == nil {
			t.Errorf("%s missing", id)
		}
	}
	for _, f := range r.Findings {
		if f.Evidence == nil || f.Suggestion == "" {
			t.Errorf("%s lacks evidence/suggestion", f.ID)
		}
	}
}

func TestMemoryGrowth(t *testing.T) {
	mk := func(slopeMBPerSec float64, noise bool) *Report {
		rec := &model.Recording{StartTime: 0, EndTime: 60}
		for i := 0; i < 30; i++ {
			v := 100 + slopeMBPerSec*float64(i*2)
			if noise && i%2 == 0 {
				v += 40
			}
			rec.Records = append(rec.Records, model.Record{Type: model.TypeMemory, Timestamp: float64(i * 2),
				Categories: []model.MemoryCategory{{Type: "JS", Size: v * (1 << 20)}}})
		}
		return Analyze(rec, "m", Options{})
	}
	g := find(mk(0.5, false), "memory-growth") // 30 MB/min
	if g == nil || !strings.Contains(g.Evidence[0], "30.0 MB/min") {
		t.Fatalf("%+v", g)
	}
	if find(mk(0, false), "memory-growth") != nil {
		t.Error("flat memory flagged")
	}
	if find(mk(0, true), "memory-growth") != nil {
		t.Error("noisy flat memory flagged")
	}
	if find(mk(0.01, false), "memory-growth") != nil {
		t.Error("tiny slope flagged")
	}
}

func TestHotFunctionAndModule(t *testing.T) {
	var st []model.StackTrace
	for i := 0; i < 70; i++ {
		st = append(st, sample(float64(i)*0.001, "render", "http://x/node_modules/@scope/lib/i.js?v=1"))
	}
	for i := 0; i < 30; i++ {
		st = append(st, sample(float64(i)*0.001, "mine", "http://x/src/m.js"))
	}
	rec := &model.Recording{StartTime: 0, EndTime: 1, Samples: []model.TargetSamples{{StackTraces: st}}}
	r := Analyze(rec, "x", Options{})
	if r.Modules[0].Name != "http://x/node_modules/@scope/lib/i.js" {
		t.Errorf("modules %+v", r.Modules)
	}
	if f := find(r, "hot-function"); f == nil || !containsString(f.RelatedFindings, "hot-module") || find(r, "hot-module") != nil {
		t.Errorf("same function and module samples need one lead with a module facet: %+v", r.Findings)
	}
	// even spread: no hot function/module
	var even []model.StackTrace
	for i := 0; i < 100; i++ {
		even = append(even, sample(0, "f"+string(rune('a'+i%20)), "http://x/node_modules/p"+string(rune('a'+i%20))+"/i.js"))
	}
	r = Analyze(&model.Recording{EndTime: 1, Samples: []model.TargetSamples{{StackTraces: even}}}, "x", Options{})
	if find(r, "hot-function") != nil || find(r, "hot-module") != nil {
		t.Error("even spread flagged")
	}
}

func TestFindingOrder(t *testing.T) {
	rec := &model.Recording{StartTime: 0, EndTime: 2, Records: []model.Record{
		script("function-call", 0, 0.6, ""), script("function-call", 1, 1.06, ""),
	}}
	r := Analyze(rec, "x", Options{})
	for i := 1; i < len(r.Findings); i++ {
		a, b := r.Findings[i-1], r.Findings[i]
		rank := map[Severity]int{SevHigh: 0, SevMedium: 1, SevLow: 2, SevInfo: 3}
		if rank[a.Severity] > rank[b.Severity] || (a.Severity == b.Severity && a.ImpactMs < b.ImpactMs) {
			t.Errorf("order: %s before %s", a.ID, b.ID)
		}
	}
}

func TestRenderSmoke(t *testing.T) {
	rec := &model.Recording{DisplayName: "demo", StartTime: 0, EndTime: 2, Records: []model.Record{
		script("timer-fired", 0.5, 0.7, "42"), frame(0.5, 0.6),
	}, Samples: []model.TargetSamples{{StackTraces: []model.StackTrace{sample(0.55, "hot", "http://x/a.js")}}}}
	r := Analyze(rec, "demo.json", Options{})
	var plain, col, md bytes.Buffer
	if err := RenderTerm(&plain, r, false); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(plain.String(), "\x1b[") {
		t.Error("ANSI in color=false output")
	}
	for _, want := range []string{"Wall-time breakdown", "Top hotspots", "Slow frames", "Long tasks", "Findings", "long-tasks"} {
		if !strings.Contains(plain.String(), want) {
			t.Errorf("term missing %q", want)
		}
	}
	if err := RenderTerm(&col, r, true); err != nil || !strings.Contains(col.String(), "\x1b[") {
		t.Error("expected ANSI with color=true")
	}
	if err := RenderMarkdown(&md, r); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"# wkperf profile: demo", "## Findings", "**Suggestion:**", "| Category |"} {
		if !strings.Contains(md.String(), want) {
			t.Errorf("md missing %q", want)
		}
	}
}
