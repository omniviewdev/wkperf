package rootcause_test

import (
	"encoding/json"
	"math"
	"math/rand/v2"
	"strings"
	"testing"

	"github.com/omniviewdev/wkperf/internal/flame"
	"github.com/omniviewdev/wkperf/internal/model"
	"github.com/omniviewdev/wkperf/internal/rootcause"
	"github.com/omniviewdev/wkperf/internal/testgen"
)

func recording() *model.Recording {
	r := testgen.Empty()
	r.StartTime, r.EndTime = 10, 12
	r.Records = nil
	r.Samples = []model.TargetSamples{{Target: model.Target{Identifier: "page"}}}
	return r
}

func app(name string) model.StackFrame {
	return model.StackFrame{Name: name, URL: "https://app.test/src/main.js", Line: 8, Column: 2}
}

func framework(name string) model.StackFrame {
	return model.StackFrame{Name: name, URL: "https://app.test/vendor/react.js", Line: 12, Column: 4}
}

// Test input follows WebKit's self-first export, not the canonical flame order.
func sample(r *model.Recording, at, ms float64, frames ...model.StackFrame) {
	raw := make([]model.StackFrame, len(frames))
	for i := range frames {
		raw[len(frames)-1-i] = frames[i]
	}
	r.Samples[0].StackTraces = append(r.Samples[0].StackTraces, model.StackTrace{Timestamp: r.StartTime + at, StackFrames: raw})
	r.Samples[0].Durations = append(r.Samples[0].Durations, ms/1000)
}

func event(r *model.Recording, typ string, from, to float64) {
	r.Records = append(r.Records, model.Record{Type: model.TypeScript, EventType: typ, StartTime: r.StartTime + from, EndTime: r.StartTime + to})
}

func build(r *model.Recording) *rootcause.Report {
	return rootcause.Build(r, nil, rootcause.Options{Source: "capture one.json"})
}

func finding(r *rootcause.Report, id string) *rootcause.Finding {
	for i := range r.Findings {
		if r.Findings[i].ID == id {
			return &r.Findings[i]
		}
	}
	return nil
}

func stat(stats []rootcause.Stat, key string) float64 {
	for _, s := range stats {
		if s.Key == key {
			return s.Value
		}
	}
	return -1
}

func near(t *testing.T, got, want float64) {
	t.Helper()
	if math.Abs(got-want) > 0.00001 {
		t.Fatalf("got %.9f want %.9f", got, want)
	}
}

func TestLayoutChainOwnersAndClosestAppAttribution(t *testing.T) {
	r := recording()
	chain := []model.StackFrame{app("onResize"), app("measureRows"), framework("commitOnFiber"), {Name: "getAnimations"}}
	for _, at := range []float64{.1, .2, .3} {
		sample(r, at, 5, chain...)
	}
	sample(r, .4, 3, app("onResize"))
	sample(r, .5, 2, app("onResize"), framework("renderWithHooks"))
	sample(r, .6, 1, model.StackFrame{Name: "unknownNative"})
	report := build(r)
	if len(report.HotPath.Frames) != 4 || report.HotPath.Frames[3].Name != "getAnimations" {
		t.Fatalf("hot path: %+v", report.HotPath)
	}
	f := finding(report, "suspected-layout-read")
	if f == nil || len(f.Evidence) != 1 || len(f.Evidence[0].Frames) != 4 {
		t.Fatalf("missing exact stack evidence: %+v", f)
	}
	if !strings.Contains(strings.ToLower(f.Explanation), "not proof") {
		t.Fatal("layout reads presented as proof")
	}
	near(t, f.Evidence[0].SelfMs, 15)
	owners := map[string]float64{}
	for _, o := range report.Owners {
		owners[o.Category] = o.SelfMs
		if o.Command == "" || len(o.Stats) == 0 {
			t.Fatal("owner lacks query/stat")
		}
	}
	near(t, owners["native"], 16)
	near(t, owners["framework-library"], 2)
	near(t, owners["app"], 3)
	var total float64
	for _, v := range owners {
		total += v
	}
	near(t, total, 21)
	attribution := map[string]rootcause.Attribution{}
	for _, a := range report.AppAttribution {
		attribution[a.App.Name] = a
	}
	near(t, attribution["measureRows"].NativeMs, 15)
	near(t, attribution["measureRows"].TotalMs, 15)
	near(t, attribution["onResize"].TotalMs, 5)
	if f.Command == "" || !strings.Contains(f.Evidence[0].Command, "stacks") || !strings.Contains(report.Commands.Depth, "depth") || !strings.Contains(report.Commands.Paths, "paths") {
		t.Fatal("missing reproducible queries")
	}
}

func TestNativeLayoutReadNamesAndAppNameNegative(t *testing.T) {
	for _, name := range []string{"getAnimations", "getBoundingClientRect", "getClientRects", "offsetHeight", "offsetWidth", "getComputedStyle", "scrollTop", "scrollHeight"} {
		t.Run(name, func(t *testing.T) {
			r := recording()
			sample(r, .1, 2, app("measure"), model.StackFrame{Name: name})
			if finding(build(r), "suspected-layout-read") == nil {
				t.Fatalf("native %s not detected", name)
			}
			r = recording()
			sample(r, .1, 2, app(name))
			if finding(build(r), "suspected-layout-read") != nil {
				t.Fatal("app name mistaken for native read")
			}
		})
	}
}

func TestSynchronousReactRequiresTriggerAndSplitsDistinctSelfFrames(t *testing.T) {
	r := recording()
	event(r, "microtask-dispatched", .09, .14)
	sample(r, .1, 4, app("scheduleUpdate"), framework("performSyncWorkOnRoot"), framework("renderWithHooks"))
	sample(r, .106, 3, app("scheduleUpdate"), framework("performSyncWorkOnRoot"), framework("commitOnFiber"))
	sample(r, .5, 10, app("scheduleUpdate"), framework("performSyncWorkOnRoot"), framework("renderWithHooks"))
	f := finding(build(r), "react-sync-in-trigger")
	if f == nil {
		t.Fatal("missing React microtask finding")
	}
	near(t, stat(f.Stats, "react.render.self_ms"), 4)
	near(t, stat(f.Stats, "react.commit.self_ms"), 3)
	near(t, stat(f.Stats, "react.sync.self_ms"), 7)
	if len(f.Evidence) != 2 || len(f.Windows) != 1 || !strings.Contains(f.Windows[0].Command, "--from") {
		t.Fatalf("trigger evidence: %+v", f)
	}
	r.Records = nil
	if finding(build(r), "react-sync-in-trigger") != nil {
		t.Fatal("React root outside event falsely blamed")
	}
}

func TestPeriodicRootsDeduplicateBurstsAndRejectJitter(t *testing.T) {
	makePeriodic := func(times []float64) *model.Recording {
		r := recording()
		for _, at := range times {
			event(r, "timer-fired", at, at+.004)
			for j := range 3 {
				sample(r, at+float64(j)*.001, 1, app("pollRoot"), app("pollBody"))
			}
		}
		return r
	}
	report := build(makePeriodic([]float64{.1, .2, .3, .4, .5}))
	if len(report.PeriodicRoots) != 1 {
		t.Fatalf("periodic roots: %+v", report.PeriodicRoots)
	}
	p := report.PeriodicRoots[0]
	if p.FireCount != 5 {
		t.Fatalf("samples counted as fires: %+v", p)
	}
	near(t, p.PeriodMs, 100)
	near(t, p.MeanCostMs, 3)
	if p.Regularity < .95 || len(p.Evidence) != 1 || p.Command == "" {
		t.Fatalf("period evidence: %+v", p)
	}
	if len(build(makePeriodic([]float64{.1, .2, .385, .43, .9})).PeriodicRoots) != 0 {
		t.Fatal("jitter accepted as periodic")
	}
	r := recording()
	for i := range 40 {
		sample(r, .1+float64(i)*.001, 1, app("continuous"))
	}
	if len(build(r).PeriodicRoots) != 0 {
		t.Fatal("one sampled burst mistaken for repeated fires")
	}
}

func TestDeepRecursionOutlierAndHealthyDepth(t *testing.T) {
	r := recording()
	for i := range 30 {
		sample(r, float64(i)*.01, 1, app("root"), app("middle"), app("leaf"))
	}
	deep := []model.StackFrame{app("updateTree")}
	for range 100 {
		deep = append(deep, framework("recursivelyTraverseEffects"))
	}
	sample(r, .8, 4, deep...)
	report := build(r)
	if report.Depth.Max != 101 || report.Depth.MaxCommitDepth != 100 || len(report.Depth.Outliers) != 1 {
		t.Fatalf("depth distribution: %+v", report.Depth)
	}
	if finding(report, "deep-recursion") == nil || finding(report, "deep-commit") == nil {
		t.Fatal("missing recursion/commit findings")
	}
	if report.Depth.Outliers[0].Command == "" || len(report.Depth.Outliers[0].Stack.Frames) != 101 {
		t.Fatal("depth evidence not exact")
	}
	r = recording()
	for i := range 30 {
		sample(r, float64(i)*.01, 1, app("root"), app("healthy"), app("healthy"), app("healthy"))
	}
	if len(build(r).Depth.Outliers) != 0 || finding(build(r), "deep-recursion") != nil {
		t.Fatal("healthy recursion flagged")
	}
}

func TestNoSamplesWindowTriageAndGCTemporalEvidence(t *testing.T) {
	r := recording()
	event(r, "event-dispatched", .1, .2)
	r.Records = append(r.Records, model.Record{Type: model.TypeRenderingFrame, StartTime: r.StartTime + .1, EndTime: r.StartTime + .2}, model.Record{Type: model.TypeScript, EventType: "garbage-collected", StartTime: r.StartTime + .15, EndTime: r.StartTime + .17})
	report := build(r)
	if len(report.LongTasks) != 1 || len(report.SlowFrames) != 1 || len(report.Windows) < 2 {
		t.Fatalf("missing triage: %+v", report)
	}
	for _, w := range report.Windows {
		if w.TopStack != nil || !strings.Contains(w.SampleStatus, "No overlapping") || !strings.Contains(w.Command, "--from") || !strings.Contains(w.Command, "--to") {
			t.Fatalf("absent sample evidence: %+v", w)
		}
	}
	if len(report.GCOverlaps) != 1 || len(report.GCOverlaps[0].Events) != 1 {
		t.Fatalf("GC overlap evidence: %+v", report.GCOverlaps)
	}
	near(t, report.GCOverlaps[0].DurationMs, 20)
	near(t, report.GCOverlaps[0].Events[0].OverlapMs, 20)
	if !strings.Contains(report.GCOverlaps[0].Attribution, "not") {
		t.Fatal("GC pause claims sampled allocation cause")
	}
}

func TestClippedWindowLinksHeaviestOverlappingStack(t *testing.T) {
	r := recording()
	event(r, "event-dispatched", .1, .2)
	sample(r, .095, 20, app("crossing"))
	sample(r, .12, 4, app("small"))
	sample(r, .13, 10, app("largest"), model.StackFrame{Name: "getBoundingClientRect"})
	report := rootcause.Build(r, nil, rootcause.Options{Source: "capture.json", FromSec: .105, ToSec: .15, LongTaskMs: 20})
	if len(report.LongTasks) != 1 || report.LongTasks[0].TopStack == nil {
		t.Fatalf("clipped longtask missing: %+v", report.LongTasks)
	}
	w := report.LongTasks[0]
	near(t, w.FromSec, .105)
	near(t, w.ToSec, .15)
	near(t, w.DurationMs, 45)
	near(t, w.TopStack.SelfMs, 10)
	if w.TopStack.Frames[len(w.TopStack.Frames)-1].Name != "getBoundingClientRect" {
		t.Fatalf("wrong overlapping stack: %+v", w.TopStack)
	}
	near(t, stat(report.Stats, "samples.self_ms"), 14)
}

func TestMappedFrameRetainsGeneratedIdentityAndOwner(t *testing.T) {
	r := recording()
	sample(r, .1, 5, app("caller"), model.StackFrame{Name: "minified", URL: "https://app.test/chunk.js", Line: 2, Column: 5})
	tree := flame.Build(r, flame.Options{Mapper: func(f model.StackFrame) *flame.Location {
		if f.Name == "minified" {
			return &flame.Location{Name: "libraryFn", URL: "webpack:///node_modules/library/index.js", Line: 55, Column: 6}
		}
		return nil
	}})
	report := rootcause.Build(r, tree, rootcause.Options{Source: "capture.json"})
	near(t, report.AppAttribution[0].FrameworkMs, 5)
	frame := report.HotPath.Frames[1]
	if frame.URL != "https://app.test/chunk.js" || frame.Original == nil || frame.Original.URL != "webpack:///node_modules/library/index.js" {
		t.Fatalf("generated/mapped locations lost: %+v", frame)
	}
}

func TestShiftedShuffledRecordingProducesDeterministicReport(t *testing.T) {
	r := recording()
	for i, name := range []string{"z", "a", "m", "a"} {
		event(r, "event-dispatched", float64(i)*.1, float64(i)*.1+.06)
		sample(r, float64(i)*.1+.01, 2, app("root"), app(name))
	}
	first, _ := json.Marshal(build(r))
	rng := rand.New(rand.NewPCG(71, 29))
	rng.Shuffle(len(r.Records), func(i, j int) { r.Records[i], r.Records[j] = r.Records[j], r.Records[i] })
	target := &r.Samples[0]
	rng.Shuffle(len(target.StackTraces), func(i, j int) {
		target.StackTraces[i], target.StackTraces[j] = target.StackTraces[j], target.StackTraces[i]
		target.Durations[i], target.Durations[j] = target.Durations[j], target.Durations[i]
	})
	shift := 32.0
	r.StartTime += shift
	r.EndTime += shift
	for i := range r.Records {
		r.Records[i].StartTime += shift
		r.Records[i].EndTime += shift
	}
	for i := range target.StackTraces {
		target.StackTraces[i].Timestamp += shift
	}
	second, _ := json.Marshal(build(r))
	if string(first) != string(second) {
		t.Fatalf("non-deterministic relative report\nfirst %s\nsecond %s", first, second)
	}
}

func TestNilRecordingProducesEmptyReport(t *testing.T) {
	r := rootcause.Build(nil, nil, rootcause.Options{})
	if r == nil || len(r.Findings) != 0 || len(r.HotPath.Frames) != 0 {
		t.Fatalf("nil recording: %+v", r)
	}
}
