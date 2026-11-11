package rootcause_test

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/omniviewdev/wkperf/internal/flame"
	"github.com/omniviewdev/wkperf/internal/model"
	"github.com/omniviewdev/wkperf/internal/rootcause"
)

func bundled(name string) model.StackFrame {
	return model.StackFrame{Name: name, URL: "wails://localhost/assets/index-TEST.js", Line: 12, Column: 4}
}

func TestA8NearestNonFrameworkContextKeepsUnknownAndMissingEvidence(t *testing.T) {
	r := recording()
	sample(r, .1, 6, bundled("tick"), bundled("getFiniteAnimations"), model.StackFrame{Name: "getAnimations"})
	sample(r, .2, 9, bundled("processRootScheduleInMicrotask"), bundled("performSyncWorkOnRoot"), model.StackFrame{Name: "cell", URL: "wails://localhost/_/plugins/kubernetes/assets/entry2.js", Line: 248, Column: 31307})
	sample(r, .3, 15, bundled("commitRoot"), bundled("commitDeletionEffectsOnFiber"), model.StackFrame{Name: "removeChild"})
	sample(r, .4, 2, model.StackFrame{Name: "bridge", URL: "user-script:2"}, model.StackFrame{Name: "nativeBridge"})
	report := build(r)
	attrs := map[string]rootcause.Attribution{}
	for _, attr := range report.ContextAttribution {
		attrs[attr.Frame.Name] = attr
		if attr.Frame.Category == flame.CategoryFramework {
			t.Fatalf("framework attributed as context: %+v", attr)
		}
	}
	if attrs["getFiniteAnimations"].Frame.Category != flame.Category("unknown-bundled") || attrs["cell"].Frame.Category != flame.CategoryApp || attrs["bridge"].Frame.Category != flame.Category("injected") {
		t.Fatalf("ownership evidence lost: %+v", attrs)
	}
	near(t, attrs["getFiniteAnimations"].NativeMs, 6)
	near(t, attrs["cell"].SelfMs, 9)
	near(t, report.ContextUnavailable.SelfMs, 15)
	if report.ContextUnavailable.Samples != 1 || !strings.Contains(report.ContextUnavailable.Reason, "scheduling") {
		t.Fatalf("missing framework-only scheduling evidence: %+v", report.ContextUnavailable)
	}
	payload, _ := json.Marshal(report)
	if strings.Contains(string(payload), "appAttribution") || !strings.Contains(string(payload), "contextAttribution") {
		t.Fatalf("stale attribution JSON: %s", payload)
	}
	row, _ := json.Marshal(attrs["cell"])
	if strings.Contains(string(row), `"app":`) || !strings.Contains(string(row), `"frame":`) {
		t.Fatalf("stale context frame JSON: %s", row)
	}
	var total float64
	for _, owner := range report.Owners {
		total += owner.SelfMs
	}
	near(t, total, 32)
}

func TestA9ReactSchedulingIsNotUserTriggerAndPhaseUsesAncestors(t *testing.T) {
	r := recording()
	event(r, "microtask-dispatched", .09, .2)
	root := []model.StackFrame{bundled(""), bundled("processRootScheduleInMicrotask"), bundled("performSyncWorkOnRoot")}
	sample(r, .1, 9, append(append([]model.StackFrame{}, root...), bundled("renderRootSync"), bundled("renderWithHooks"), app("cell"))...)
	sample(r, .15, 15, append(append([]model.StackFrame{}, root...), bundled("commitRoot"), bundled("commitDeletionEffectsOnFiber"), model.StackFrame{Name: "removeChild"})...)
	report := build(r)
	if finding(report, "react-sync-in-trigger") != nil {
		t.Fatal("React's own scheduling microtask blamed as user trigger")
	}
	f := finding(report, "react-sync")
	if f == nil || !strings.Contains(f.Explanation, "not identifiable") {
		t.Fatalf("unidentified sync work missing: %+v", report.Findings)
	}
	near(t, stat(f.Stats, "react.render.self_ms"), 9)
	near(t, stat(f.Stats, "react.commit.self_ms"), 15)
	event(r, "event-dispatched", .08, .21)
	r.Records[len(r.Records)-1].Details = json.RawMessage(`"click"`)
	f = finding(build(r), "react-sync-in-trigger")
	if f == nil || len(f.Windows) != 1 || !strings.Contains(f.Windows[0].Event, "click") {
		t.Fatalf("enclosing user event missing: %+v", f)
	}
	near(t, stat(f.Stats, "react.render.self_ms"), 9)
	near(t, stat(f.Stats, "react.commit.self_ms"), 15)
	r.Records[len(r.Records)-1].Details = json.RawMessage(`"message"`)
	if finding(build(r), "react-sync-in-trigger") != nil {
		t.Fatal("scheduler message counted as user trigger")
	}
	r = recording()
	event(r, "timer-fired", .09, .2)
	sample(r, .1, 5, bundled("performWorkUntilDeadline"), bundled("renderRootSync"), app("cell"))
	if finding(build(r), "react-sync-in-trigger") != nil || finding(build(r), "react-sync") != nil {
		t.Fatal("renderRootSync alone mistaken for SyncLane evidence")
	}
}

func TestA9InnermostPhaseWinsAndStoreSourceIsNamed(t *testing.T) {
	r := recording()
	event(r, "timer-fired", .09, .2)
	sample(r, .1, 3, app("refresh"), framework("flushSync"), framework("renderRootSync"), framework("commitRoot"), model.StackFrame{Name: "removeChild"})
	sample(r, .15, 4, app("refresh"), framework("flushSync"), framework("commitRoot"), framework("renderRootSync"), framework("updateSyncExternalStore"), app("cell"))
	f := finding(build(r), "react-sync-in-trigger")
	if f == nil || !strings.Contains(f.Explanation, "useSyncExternalStore") {
		t.Fatalf("missing store SyncLane lead: %+v", f)
	}
	near(t, stat(f.Stats, "react.render.self_ms"), 4)
	near(t, stat(f.Stats, "react.commit.self_ms"), 3)
}

func TestA10ReactDepthConsolidatesWithoutRecursionClaim(t *testing.T) {
	r := recording()
	for i := range 30 {
		sample(r, float64(i)*.01, 1, app("root"), app("leaf"))
	}
	frames := []model.StackFrame{bundled("commitRoot")}
	for range 33 {
		frames = append(frames, bundled("commitDeletionEffectsOnFiber"), bundled("recursivelyTraverseDeletionEffects"))
	}
	sample(r, .8, 4.5, append(frames, model.StackFrame{Name: "removeChild"})...)
	report := build(r)
	var depth []rootcause.Finding
	for _, f := range report.Findings {
		if strings.HasPrefix(f.ID, "deep") {
			depth = append(depth, f)
		}
	}
	if len(depth) != 1 || depth[0].ID != "deep-stack" || depth[0].Depth == nil || depth[0].Depth.Recursion != nil {
		t.Fatalf("depth triplicated or traversal mistaken for recursion: %+v", depth)
	}
	if strings.Contains(strings.ToLower(depth[0].Title+depth[0].Explanation), "recursion") || depth[0].Depth.ReactFiberLevels != 33 {
		t.Fatalf("ordinary fiber depth misdescribed: %+v", depth[0])
	}
	near(t, stat(depth[0].Stats, "deep-stack.self_ms"), 4.5)
	if depth[0].Depth.RepeatedFrame == nil || depth[0].Depth.RepeatedFrame.Count != 33 {
		t.Fatalf("quantitative repeat facet missing: %+v", depth[0].Depth)
	}
}

func TestA10OnlyShortNonFrameworkCyclesSupportRecursion(t *testing.T) {
	for _, cycle := range []int{1, 2, 3} {
		t.Run(string(rune('0'+cycle)), func(t *testing.T) {
			r := recording()
			for i := range 30 {
				sample(r, float64(i)*.01, 1, app("root"))
			}
			var frames []model.StackFrame
			for range 40 {
				frames = append(frames, app("walk"))
				if cycle >= 2 {
					frames = append(frames, app("visit"))
				}
				if cycle >= 3 {
					frames = append(frames, app("third"))
				}
			}
			sample(r, .8, 4, frames...)
			f := finding(build(r), "deep-stack")
			if f == nil || f.Depth == nil || (f.Depth.Recursion != nil) != (cycle <= 2) {
				t.Fatalf("cycle %d recursion facet wrong: %+v", cycle, f)
			}
		})
	}
}

func TestA11NativeFlushSemanticsUseTimedTimelineCorroboration(t *testing.T) {
	r := recording()
	r.Records = append(r.Records, model.Record{Type: model.TypeLayout, EventType: "recalculate-styles", StartTime: r.StartTime + .09, EndTime: r.StartTime + .2})
	for i := range 20 {
		sample(r, .1+float64(i)*.003, 1.5, app("tick"), model.StackFrame{Name: "getAnimations"})
	}
	f := finding(build(r), "forced-style-or-layout-read")
	if f == nil || len(f.NativeReads) != 1 || f.NativeReads[0].API != "getAnimations" || f.NativeReads[0].Flush != "style" {
		t.Fatalf("style API misclassified: %+v", f)
	}
	api := f.NativeReads[0]
	if api.Samples != 20 || api.StyleSamples != 20 || api.LayoutSamples != 0 || strings.Contains(f.Title, "layout-forcing") {
		t.Fatalf("timed style evidence wrong: %+v", f)
	}
	near(t, api.SelfMs, 30)
	near(t, api.StyleMs, 110)
	near(t, api.LayoutMs, 0)
	if len(f.Windows) != 1 || !strings.Contains(f.Windows[0].Event, "recalculate-styles") || len(f.RelatedFindings) != 1 || f.RelatedFindings[0] != "style-recalc-heavy" {
		t.Fatalf("linked style evidence missing: %+v", f)
	}
	if !strings.Contains(f.Explanation, "20/20") || !strings.Contains(f.Explanation, "not proof") {
		t.Fatalf("missing conservative corroboration: %s", f.Explanation)
	}
	r = recording()
	r.Records = append(r.Records, model.Record{Type: model.TypeLayout, EventType: "layout", StartTime: r.StartTime + .09, EndTime: r.StartTime + .2})
	sample(r, .1, 3, app("measure"), model.StackFrame{Name: "getBoundingClientRect"})
	f = finding(build(r), "forced-style-or-layout-read")
	if f == nil || f.NativeReads[0].Flush != "layout" || f.NativeReads[0].LayoutSamples != 1 {
		t.Fatalf("geometry layout corroboration missing: %+v", f)
	}
	r = recording()
	sample(r, .1, 3, app("measure"), model.StackFrame{Name: "getComputedStyle"})
	f = finding(build(r), "forced-style-or-layout-read")
	if f == nil || f.NativeReads[0].Flush != "unknown" {
		t.Fatalf("property-dependent flush overstated: %+v", f)
	}
}

func TestA12PeriodicNeedsSixFiresAndTwoSecondsAndClassifiesRAF(t *testing.T) {
	makeRAF := func(count int, period float64) *model.Recording {
		r := recording()
		r.EndTime = r.StartTime + 4
		for i := range count {
			at := .1 + float64(i)*period
			event(r, "animation-frame-fired", at, at+.02)
			event(r, "event-dispatched", at-.005, at+.025)
			r.Records[len(r.Records)-1].Details = json.RawMessage(`"wheel"`)
			sample(r, at+.01, 5, app("tick"), model.StackFrame{Name: "getAnimations"})
		}
		return r
	}
	for _, r := range []*model.Recording{makeRAF(4, .394), makeRAF(6, .1)} {
		if got := build(r).PeriodicRoots; len(got) != 0 {
			t.Fatalf("short repetition called periodic: %+v", got)
		}
	}
	report := build(makeRAF(6, .4))
	if len(report.PeriodicRoots) != 1 || report.PeriodicRoots[0].Kind != "raf" || report.PeriodicRoots[0].RAFFires != 6 || report.PeriodicRoots[0].InputFires != 6 {
		t.Fatalf("rAF/input context lost: %+v", report.PeriodicRoots)
	}
	if len(report.PeriodicRoots[0].SampleIDs) != 6 {
		t.Fatal("exact periodic sample membership missing")
	}
}

func TestA14SupportedHotPathStopsBeforeNearTieAndKeepsNodeCosts(t *testing.T) {
	r := recording()
	for i := range 5 {
		sample(r, .1+float64(i)*.01, 10, app("root"), app("left"))
		sample(r, .2+float64(i)*.01, 9, app("root"), app("right"))
	}
	report := build(r)
	p := report.HotPath
	if len(p.Frames) != 1 || p.Frames[0].Name != "root" || len(p.Nodes) != 1 || p.StopReason != "near-tie" || len(p.Branches) != 2 {
		t.Fatalf("arbitrary tie descent: %+v", p)
	}
	near(t, p.Nodes[0].TotalMs, 95)
	near(t, p.Nodes[0].SelfMs, 0)
	near(t, p.Nodes[0].Share, 1)
	near(t, stat(p.Stats, "hot_path.total_ms"), 95)
	if stat(p.Stats, "hot_path.self_ms") != -1 || !strings.Contains(p.Command, "--min-share 0.05") {
		t.Fatalf("inclusive cost mislabeled self or unreproducible threshold: %+v", p)
	}
	selected := rootcause.SelectEvidence(report, 1)
	if len(selected.HotPath.Branches) != 1 || selected.HotPath.BranchesTotal != 2 || len(report.HotPath.Branches) != 2 {
		t.Fatalf("branch selection changed measurement or lacks total: %+v", selected.HotPath)
	}
}

func TestA14HotPathRejectsSingleSampleAndBelowShareLeaves(t *testing.T) {
	r := recording()
	for i := range 5 {
		sample(r, .1+float64(i)*.01, 10, app("root"))
	}
	sample(r, .2, 1, app("root"), app("noise"))
	p := build(r).HotPath
	if len(p.Frames) != 1 || p.StopReason != "min-share" || p.Samples != 6 {
		t.Fatalf("unsupported single-sample tail retained: %+v", p)
	}
	r = recording()
	for i := range 5 {
		sample(r, .1+float64(i)*.01, 10, app("root"))
	}
	sample(r, .2, 20, app("root"), app("heavyNoise"))
	p = build(r).HotPath
	if len(p.Frames) != 1 || p.StopReason != "sample-support" {
		t.Fatalf("single weighted sample treated as supported path: %+v", p)
	}
}
