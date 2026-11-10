package rootcause_test

import (
	"encoding/json"
	"math"
	"strings"
	"testing"

	"github.com/omniviewdev/wkperf/internal/flame"
	"github.com/omniviewdev/wkperf/internal/model"
	"github.com/omniviewdev/wkperf/internal/profile"
	"github.com/omniviewdev/wkperf/internal/rootcause"
	"github.com/omniviewdev/wkperf/internal/testgen"
)

func TestSharedSyntheticScenarios(t *testing.T) {
	t.Run("getAnimations", func(t *testing.T) {
		r := build(testgen.GetAnimations())
		if f := finding(r, "suspected-layout-read"); f == nil || len(f.Evidence) != 1 || len(f.Evidence[0].Frames) != 4 {
			t.Fatalf("missing native chain: %+v", r.Findings)
		}
		if len(r.AppAttribution) != 1 || r.AppAttribution[0].App.Name != "measureRows" {
			t.Fatalf("closest app ancestor lost: %+v", r.AppAttribution)
		}
		near(t, r.AppAttribution[0].NativeMs, 6)
		near(t, r.AppAttribution[0].FrameworkMs, 2)
		near(t, r.AppAttribution[0].SelfMs, 3)
		near(t, r.AppAttribution[0].TotalMs, 11)
	})
	t.Run("react", func(t *testing.T) {
		r := build(testgen.React())
		f := finding(r, "react-sync-in-trigger")
		if f == nil {
			t.Fatal("missing synchronous React trigger evidence")
		}
		near(t, stat(f.Stats, "react.sync.self_ms"), 35)
		near(t, stat(f.Stats, "react.render.self_ms"), 20)
		near(t, stat(f.Stats, "react.commit.self_ms"), 15)
	})
	t.Run("periodic", func(t *testing.T) {
		r := build(testgen.Periodic())
		if len(r.PeriodicRoots) != 1 || r.PeriodicRoots[0].FireCount != 5 {
			t.Fatalf("fire dedup: %+v", r.PeriodicRoots)
		}
		near(t, r.PeriodicRoots[0].PeriodMs, 1000)
		near(t, r.PeriodicRoots[0].MeanCostMs, 6)
		if len(r.SlowFrames) != 0 {
			t.Fatal("idle gaps counted as active slow frames")
		}
	})
	t.Run("deep", func(t *testing.T) {
		r := build(testgen.DeepMinified())
		if r.Depth.Max != 366 || len(r.Depth.Outliers) != 1 || finding(r, "deep-stack") == nil {
			t.Fatalf("deep minified outlier: %+v", r.Depth)
		}
		if finding(r, "deep-recursion") != nil {
			t.Fatal("distinct minified columns mistaken for recursion")
		}
	})
}

func TestNumericFiguresHaveUniqueExactReproductionCommands(t *testing.T) {
	r := rootcause.Build(testgen.GetAnimations(), nil, rootcause.Options{Source: "capture'; echo unsafe.json", Sourcemaps: []string{"source map's.js.map"}})
	stats := rootcause.Stats(r)
	if len(stats) < 30 {
		t.Fatalf("too few numeric figures exposed: %d", len(stats))
	}
	seen := map[string]bool{}
	for _, s := range stats {
		if seen[s.Key] || s.Key == "" || math.IsNaN(s.Value) || math.IsInf(s.Value, 0) {
			t.Fatalf("invalid stat: %+v", s)
		}
		seen[s.Key] = true
		if !strings.Contains(s.Command, "rootcause '"+s.Key+"'") || !strings.Contains(s.Command, "--from 0 --to 10") || !strings.Contains(s.Command, `'capture'"'"'; echo unsafe.json'`) || !strings.Contains(s.Command, `--sourcemap 'source map'"'"'s.js.map'`) {
			t.Fatalf("not exact or shell-safe: %+v", s)
		}
	}
	paths := flame.Build(testgen.GetAnimations()).Paths()
	for _, a := range r.AppAttribution {
		for _, s := range a.Stacks {
			found := false
			for _, p := range paths {
				found = found || strings.Contains(s.Command, " stacks '"+p.ID+"'")
			}
			if !found {
				t.Fatalf("stack command lacks exact shared path ID: %s", s.Command)
			}
		}
	}
}

func TestOriginalWindowOffsetMatchesFullRecordingWindow(t *testing.T) {
	r := testgen.GetAnimations()
	options := rootcause.Options{Source: "capture.json", FromSec: 1, ToSec: 1.08, Sourcemaps: []string{"capture.js.map"}}
	full := rootcause.Build(r, nil, options)
	clipped := profile.Clip(r, 1, 1.08)
	options.FromSec, options.ToSec, options.OffsetSec = 0, 0, 1
	windowed := rootcause.Build(clipped, nil, options)
	a, _ := json.Marshal(full)
	b, _ := json.Marshal(windowed)
	if string(a) != string(b) {
		t.Fatalf("clipped report changed original query coordinates\nfull %s\nclipped %s", a, b)
	}
}

func TestCapturePauseUsesSharedActiveTimeAndExcludesSamplerPoints(t *testing.T) {
	r := recording()
	r.Discontinuities = []model.Discontinuity{{StartTime: r.StartTime + .12, EndTime: r.StartTime + .18}}
	event(r, "event-dispatched", .1, .3)
	event(r, "garbage-collected", .14, .19)
	r.Records = append(r.Records, model.Record{Type: model.TypeRenderingFrame, StartTime: r.StartTime + .1, EndTime: r.StartTime + .3})
	sample(r, .11, 5, app("retained"))
	sample(r, .15, 100, app("unobserved"))
	sample(r, .19, 5, app("retained"))
	report := build(r)
	near(t, stat(report.Stats, "samples.self_ms"), 10)
	if len(report.LongTasks) != 1 {
		t.Fatalf("active long task missing: %+v", report.LongTasks)
	}
	near(t, report.LongTasks[0].DurationMs, 140)
	if len(report.SlowFrames) != 0 {
		t.Fatal("pause-spanning frame used as latency evidence")
	}
	if len(report.GCOverlaps) != 1 {
		t.Fatal("active GC pause absent")
	}
	near(t, report.GCOverlaps[0].DurationMs, 10)
}

func TestEventBurstsHaveWindowAndExactSampleEvidence(t *testing.T) {
	r := recording()
	for i := range 4 {
		at := .1 + float64(i)*.01
		event(r, "microtask-fired", at, at+.004)
		sample(r, at+.001, 2, app("burstRoot"), app("flush"))
	}
	report := build(r)
	if len(report.EventBursts) != 1 {
		t.Fatalf("event burst missing: %+v", report.EventBursts)
	}
	w := report.EventBursts[0]
	if w.Kind != "event-burst" || w.TopStack == nil || len(w.HotPath.Frames) != 2 {
		t.Fatalf("burst triage missing stack/path: %+v", w)
	}
	near(t, w.TopStack.SelfMs, 8)
	if !strings.Contains(w.Command, "--from 0.1 --to 0.134") {
		t.Fatalf("wrong burst window: %s", w.Command)
	}
	var count float64
	for _, s := range w.Stats {
		if strings.HasSuffix(s.Key, ".events") {
			count = s.Value
		}
	}
	near(t, count, 4)
}

func TestNestedNativeReadsCountEachSampleOnlyOnce(t *testing.T) {
	r := recording()
	sample(r, .1, 7, app("measure"), model.StackFrame{Name: "getAnimations"}, model.StackFrame{Name: "getBoundingClientRect"}, model.StackFrame{Name: "engineWork"})
	f := finding(build(r), "suspected-layout-read")
	if f == nil || len(f.Evidence) != 1 || len(f.Evidence[0].Frames) != 4 {
		t.Fatalf("nested native read evidence: %+v", f)
	}
	near(t, stat(f.Stats, "layout_reads.self_ms"), 7)
	near(t, stat(f.Stats, "layout_reads.samples"), 1)
}

func TestKnownReactOwnersAndGCDoNotDoubleChargeApp(t *testing.T) {
	r := recording()
	names := []string{"performSyncWorkOnRoot", "performWorkOnRoot", "renderRootSync", "workLoopSync", "performUnitOfWork", "renderWithHooks", "flushSyncWorkAcrossRoots", "processRootScheduleInMicrotask", "commitOnFiber", "recursivelyTraverseEffects", "performWorkUntilDeadline"}
	for i, name := range names {
		sample(r, .1+float64(i)*.01, 1, app("update"), app(name))
	}
	sample(r, .5, 2, app("update"), model.StackFrame{Name: "unknownNative"})
	sample(r, .6, 3, app("update"), model.StackFrame{Name: "GC"})
	report := build(r)
	owners := map[string]float64{}
	for _, o := range report.Owners {
		owners[o.Category] = o.SelfMs
	}
	near(t, owners["framework-library"], float64(len(names)))
	near(t, owners["native"], 2)
	near(t, owners["gc"], 3)
	if len(report.AppAttribution) != 1 {
		t.Fatalf("unexpected app charges: %+v", report.AppAttribution)
	}
	near(t, report.AppAttribution[0].TotalMs, float64(len(names))+2)
}

func TestPrewindowedMappedTreeRetainsSelectedBounds(t *testing.T) {
	r := testgen.GetAnimations()
	tree := flame.Build(r).Window(1, 1.08)
	report := rootcause.Build(r, tree, rootcause.Options{Source: "capture.json"})
	near(t, report.FromSec, 1)
	near(t, report.ToSec, 1.08)
	near(t, stat(report.Stats, "window.duration_ms"), 80)
	if !strings.Contains(report.Commands.HotPath, "--min-share 0 --from 1 --to 1.08") {
		t.Fatalf("wrong selected path command: %s", report.Commands.HotPath)
	}
}

func TestEveryLongTaskAndActiveSlowFrameKeepsStackBeyondTopLimit(t *testing.T) {
	r := recording()
	for i := range 12 {
		at := float64(i) * .1
		event(r, "event-dispatched", at, at+.06)
		r.Records = append(r.Records, model.Record{Type: model.TypeRenderingFrame, StartTime: r.StartTime + at, EndTime: r.StartTime + at + .06})
		sample(r, at+.01, 5, app("root"), app(string(rune('a'+i))))
	}
	report := rootcause.Build(r, nil, rootcause.Options{Source: "capture.json", Top: 2})
	if len(report.LongTasks) != 12 || len(report.SlowFrames) != 12 || len(report.Windows) != 4 {
		t.Fatalf("triage top limit removed linked records: tasks=%d frames=%d windows=%d", len(report.LongTasks), len(report.SlowFrames), len(report.Windows))
	}
	for _, group := range [][]rootcause.Window{report.LongTasks, report.SlowFrames} {
		detailed := 0
		for _, w := range group {
			if w.TopStack == nil || len(w.TopStack.Frames) != 2 || w.TopStack.Samples != 1 {
				t.Fatalf("record lost heaviest stack: %+v", w)
			}
			if !strings.Contains(w.Command, "--from") || !strings.Contains(w.Command, "--to") {
				t.Fatalf("window lacks exact query: %+v", w)
			}
			if w.HotPath != nil && len(w.HotPath.Frames) > 0 {
				detailed++
			}
		}
		if detailed != 2 {
			t.Fatalf("per-window tree detail not bounded by Top: got %d want 2", detailed)
		}
		for _, w := range group {
			for _, s := range w.Stats {
				if !strings.Contains(s.Command, "--top 2") {
					t.Fatalf("numeric query lost detail bound: %s", s.Command)
				}
			}
		}
	}
}

// Parse only the literal quoting form emitted by rootcause, without invoking a shell.
func emittedArgs(t *testing.T, command string) []string {
	t.Helper()
	var args []string
	var value strings.Builder
	var mode rune
	word := false
	for _, ch := range command {
		if mode == '\'' {
			if ch == '\'' {
				mode = 0
			} else {
				value.WriteRune(ch)
			}
			continue
		}
		if mode == '"' {
			if ch == '"' {
				mode = 0
			} else {
				if ch == '$' || ch == '`' || ch == '\\' {
					t.Fatalf("expanding double-quoted character in %q", command)
				}
				value.WriteRune(ch)
			}
			continue
		}
		switch ch {
		case '\'', '"':
			mode = ch
			word = true
		case ' ', '\t', '\n':
			if word {
				args = append(args, value.String())
				value.Reset()
				word = false
			}
		case '$', '`', ';', '&', '|', '>', '<', '(', ')', '\\':
			t.Fatalf("unquoted shell control character in %q", command)
		default:
			value.WriteRune(ch)
			word = true
		}
	}
	if mode != 0 {
		t.Fatalf("unterminated quote in %q", command)
	}
	if word {
		args = append(args, value.String())
	}
	return args
}

func TestReproductionPreservesUntrustedLiteralArgumentBoundaries(t *testing.T) {
	source := "capture space's $HOME `id`\nname.json"
	mapPath := "map space's $PATH `touch file`\nname.js.map"
	name := "function space's $SHELL `id`\nnext"
	r := recording()
	sample(r, .1, 2, app(name))
	report := rootcause.Build(r, nil, rootcause.Options{Source: source, Sourcemaps: []string{mapPath}})
	check := func(command string) []string {
		args := emittedArgs(t, command)
		if len(args) < 8 || args[0] != "wkperf" || args[1] != "query" || args[2] != source {
			t.Fatalf("source argument split: %#v", args)
		}
		found := false
		for i, arg := range args {
			if arg == "--sourcemap" {
				if i+1 >= len(args) || args[i+1] != mapPath {
					t.Fatalf("map argument split: %#v", args)
				}
				found = true
			}
		}
		if !found {
			t.Fatalf("missing map argument: %#v", args)
		}
		return args
	}
	for _, s := range rootcause.Stats(report) {
		args := check(s.Command)
		if args[3] != "rootcause" || args[4] != s.Key {
			t.Fatalf("stat key split: %#v", args)
		}
	}
	args := check(report.Commands.Paths)
	if args[3] != "paths" || args[4] != name {
		t.Fatalf("selector argument split: %#v", args)
	}
}
