package analyze

import (
	"encoding/json"
	"math"
	"testing"

	"github.com/omniviewdev/wkperf/internal/metrics"
	"github.com/omniviewdev/wkperf/internal/model"
)

func approx(a, b float64) bool { return math.Abs(a-b) < 1e-6 }

func val(t *testing.T, s *metrics.Summary, key string) float64 {
	t.Helper()
	m, ok := s.Lookup(key)
	if !ok {
		t.Fatalf("metric %q missing", key)
	}
	return m.Value
}

func TestPercentile(t *testing.T) {
	v := []float64{40, 10, 30, 20}
	cases := []struct{ p, want float64 }{
		{0, 10}, {50, 25}, {100, 40}, {95, 38.5}, {25, 17.5},
	}
	for _, c := range cases {
		if got := Percentile(v, c.p); !approx(got, c.want) {
			t.Errorf("p%v = %v, want %v", c.p, got, c.want)
		}
	}
	if v[0] != 40 {
		t.Error("Percentile mutated input")
	}
	if Percentile(nil, 50) != 0 || Percentile([]float64{7}, 99) != 7 {
		t.Error("edge cases wrong")
	}
}

func frame(start, dur float64) model.Record {
	return model.Record{Type: model.TypeRenderingFrame, StartTime: start, EndTime: start + dur}
}

func TestFramesAndJank(t *testing.T) {
	rec := &model.Recording{StartTime: 0, EndTime: 2, Records: []model.Record{
		frame(1.0, 0.060), frame(0, 0.010), frame(0.5, 0.020), frame(0.8, 0.040),
	}}
	s := Summarize(rec, "x")
	if val(t, s, "frames.count") != 4 {
		t.Error("count")
	}
	if val(t, s, "frames.jank_16ms") != 3 || val(t, s, "frames.jank_33ms") != 2 || val(t, s, "frames.jank_50ms") != 1 {
		t.Errorf("jank wrong: %v %v %v", val(t, s, "frames.jank_16ms"), val(t, s, "frames.jank_33ms"), val(t, s, "frames.jank_50ms"))
	}
	if !approx(val(t, s, "frames.jank_ratio"), 0.75) {
		t.Error("ratio")
	}
	if !approx(val(t, s, "frames.mean_ms"), 32.5) || !approx(val(t, s, "frames.fps"), 1000/32.5) {
		t.Error("mean/fps")
	}
	if !approx(val(t, s, "frames.p50_ms"), 30) || !approx(val(t, s, "frames.max_ms"), 60) {
		t.Error("p50/max")
	}
	want := []float64{10, 20, 40, 60}
	for i, w := range want {
		if !approx(s.FrameMs[i], w) {
			t.Fatalf("FrameMs not chronological: %v", s.FrameMs)
		}
	}
	if m, _ := s.Lookup("frames.fps"); m.Dir != metrics.HigherBetter {
		t.Error("fps dir")
	}
}

func TestScriptLayoutGC(t *testing.T) {
	gc := json.RawMessage(`{"__type":"GarbageCollection"}`)
	rec := &model.Recording{StartTime: 10, EndTime: 12, Records: []model.Record{
		{Type: model.TypeScript, EventType: "event-dispatched", StartTime: 10, EndTime: 10.1},
		{Type: model.TypeScript, EventType: "event-dispatched", StartTime: 11, EndTime: 11.3},
		{Type: model.TypeScript, EventType: "timer-installed", StartTime: 11, EndTime: 11},
		{Type: model.TypeScript, EventType: "script-evaluated", Details: gc, StartTime: 11.5, EndTime: 11.52},
		{Type: model.TypeLayout, EventType: "layout", StartTime: 10, EndTime: 10.01},
		{Type: model.TypeLayout, EventType: "paint", StartTime: 10, EndTime: 10.03},
	}}
	s := Summarize(rec, "x")
	if val(t, s, "script.event-dispatched.count") != 2 || !approx(val(t, s, "script.event-dispatched.total_ms"), 400) ||
		!approx(val(t, s, "script.event-dispatched.max_ms"), 300) || !approx(val(t, s, "script.event-dispatched.mean_ms"), 200) {
		t.Error("event-dispatched aggregate")
	}
	if !approx(val(t, s, "script.total_ms"), 400) || !approx(val(t, s, "script.busy_ratio"), 0.2) {
		t.Error("script total / busy ratio (GC and timer-installed must not count)")
	}
	if val(t, s, "script.timer-installed.count") != 1 {
		t.Error("timer-installed count")
	}
	if _, ok := s.Lookup("script.timer-installed.total_ms"); ok {
		t.Error("count-only event emitted time stats")
	}
	if val(t, s, "gc.count") != 1 || !approx(val(t, s, "gc.total_ms"), 20) {
		t.Error("gc")
	}
	if !approx(val(t, s, "layout.total_ms"), 40) || !approx(val(t, s, "layout.paint.total_ms"), 30) {
		t.Error("layout")
	}
	if _, ok := s.Lookup("cpu.mean_pct"); ok {
		t.Error("absent instrument emitted")
	}
}

func TestCPUMemoryNetworkPressure(t *testing.T) {
	rec := &model.Recording{StartTime: 100, EndTime: 110,
		MemoryPressureEvents: []model.MemoryPressure{{Timestamp: 101}},
		Records: []model.Record{
			{Type: model.TypeCPU, Timestamp: 102, Usage: 50, Threads: []model.Thread{{Type: "main", Usage: 30}, {Type: "webkit", Usage: 20}}},
			{Type: model.TypeCPU, Timestamp: 101, Usage: 10},
			{Type: model.TypeMemory, Timestamp: 105, Categories: []model.MemoryCategory{{Type: "JS", Size: 4 << 20}, {Type: "Images", Size: 2 << 20}}},
			{Type: model.TypeMemory, Timestamp: 103, Categories: []model.MemoryCategory{{Type: "JS", Size: 10 << 20}}},
			{Type: model.TypeNetwork, Entry: json.RawMessage(`{"time":100,"response":{"bodySize":50,"_transferSize":200}}`)},
			{Type: model.TypeNetwork, Entry: json.RawMessage(`{"time":300,"response":{"bodySize":70,"_transferSize":-1}}`)},
		}}
	s := Summarize(rec, "x")
	if !approx(val(t, s, "cpu.mean_pct"), 30) || !approx(val(t, s, "cpu.max_pct"), 50) {
		t.Error("cpu")
	}
	if !approx(val(t, s, "cpu.main.mean_pct"), 30) {
		t.Error("cpu main")
	}
	if s.CPU[0].T != 1 || s.CPU[1].T != 2 {
		t.Errorf("cpu series %v", s.CPU)
	}
	if !approx(val(t, s, "memory.peak_mb"), 10) || !approx(val(t, s, "memory.final_mb"), 6) || !approx(val(t, s, "memory.mean_mb"), 8) {
		t.Error("memory")
	}
	if !approx(val(t, s, "memory.JS.peak_mb"), 10) || !approx(val(t, s, "memory.Images.peak_mb"), 2) {
		t.Error("memory category")
	}
	if val(t, s, "network.requests") != 2 || val(t, s, "network.transfer_bytes") != 270 || !approx(val(t, s, "network.mean_ms"), 200) {
		t.Error("network")
	}
	if val(t, s, "pressure.events") != 1 {
		t.Error("pressure")
	}
}

func TestFunctionsRecursion(t *testing.T) {
	f := func(n string) model.StackFrame { return model.StackFrame{Name: n, URL: "u", Line: 1} }
	rec := &model.Recording{Samples: []model.TargetSamples{{
		StackTraces: []model.StackTrace{
			{StackFrames: []model.StackFrame{f("a"), f("b"), f("b"), f("b")}},
			{StackFrames: []model.StackFrame{f("a"), {Name: "", URL: "u", Line: 1}}},
			{StackFrames: []model.StackFrame{f("a")}},
		},
		Durations: []float64{0.010, 0.020}, // third sample defaults to 1ms
	}}}
	s := Summarize(rec, "x")
	by := map[string]metrics.FuncStat{}
	for _, fs := range s.Functions {
		by[fs.Name] = fs
	}
	b := by["b"]
	if !approx(b.SelfMs, 10) || !approx(b.TotalMs, 10) || b.SelfSamples != 1 {
		t.Errorf("b = %+v (recursion must count once)", b)
	}
	a := by["a"]
	if !approx(a.TotalMs, 31) || !approx(a.SelfMs, 1) {
		t.Errorf("a = %+v", a)
	}
	anon := by["(anonymous)"]
	if !approx(anon.SelfMs, 20) {
		t.Errorf("anon = %+v", anon)
	}
	if s.Functions[0].Name != "(anonymous)" || s.Functions[1].Name != "b" {
		t.Errorf("not sorted by self desc: %+v", s.Functions)
	}
}

func TestEmptyAndNil(t *testing.T) {
	s := Summarize(&model.Recording{}, "e")
	if val(t, s, "overview.records") != 0 {
		t.Error("records")
	}
	if _, ok := s.Lookup("frames.count"); ok {
		t.Error("frames emitted for empty recording")
	}
	if Summarize(nil, "n") == nil {
		t.Error("nil rec")
	}
}
