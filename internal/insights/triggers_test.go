package insights

import (
	"math"
	"reflect"
	"sort"
	"strconv"
	"strings"
	"testing"

	"github.com/omniviewdev/wkperf/internal/analyze"
	"github.com/omniviewdev/wkperf/internal/flame"
	"github.com/omniviewdev/wkperf/internal/metrics"
	"github.com/omniviewdev/wkperf/internal/model"
)

func TestTriggerAttributionAcrossOverlappingIntervals(t *testing.T) {
	root := model.StackFrame{Name: "root", URL: "app.js", Line: 10}
	parse := model.StackFrame{Name: "parse", URL: "app.js", Line: 25, Column: 5}
	write := model.StackFrame{Name: "write", URL: "app.js", Line: 40}
	outside := []model.StackFrame{{Name: "outside", URL: "app.js"}}
	recursive := []model.StackFrame{parse, parse, parse, root}
	flush := []model.StackFrame{write, root}
	target := model.TargetSamples{
		StackTraces: []model.StackTrace{
			{Timestamp: 102.5, StackFrames: outside},
			{Timestamp: 101.375, StackFrames: recursive},
			{Timestamp: 101.875, StackFrames: outside},
			{Timestamp: 100.25, StackFrames: []model.StackFrame{parse}},
			{Timestamp: 101.5, StackFrames: recursive},
			{Timestamp: 102.75, StackFrames: flush},
			{Timestamp: 102.125, StackFrames: flush},
			{Timestamp: 101.375, StackFrames: recursive},
			{Timestamp: 100.249, StackFrames: outside},
			{Timestamp: 102.625, StackFrames: outside},
			{Timestamp: 103, StackFrames: outside},
		},
		Durations: []float64{.001, .002, .003, .004, .005, .006, .007, .008, .009, .010, .011},
	}
	rec := &model.Recording{
		StartTime:       100,
		EndTime:         103,
		Discontinuities: []model.Discontinuity{{StartTime: 101.75, EndTime: 102}},
		Records: []model.Record{
			script("timer-fired", 102.75, 103, "first"),
			script("timer-fired", 101.375, 102.5, "first"),
			script("event-dispatched", 101.375, 101.5, "second"),
			script("timer-fired", 100.25, 101.5, "first"),
		},
		Samples: []model.TargetSamples{target},
	}
	got := TriggerStats(rec, 10)
	if len(got) != 2 {
		t.Fatalf("got %d trigger buckets, want 2", len(got))
	}
	for name, indices := range map[string][]int{"first": {1, 3, 4, 5, 6, 7}, "second": {1, 7}} {
		t.Run(name, func(t *testing.T) {
			var trigger TriggerStat
			for _, stat := range got {
				if stat.Name == name {
					trigger = stat
					break
				}
			}
			selected := *rec
			selected.Samples = []model.TargetSamples{{}}
			for _, i := range indices {
				selected.Samples[0].StackTraces = append(selected.Samples[0].StackTraces, target.StackTraces[i])
				selected.Samples[0].Durations = append(selected.Samples[0].Durations, target.Durations[i])
			}
			// Derive exact weights, recursive edges, and full-stack IDs from only
			// the known members, without invoking interval selection.
			functions := analyze.SummarizeWithTree(&selected, "", flame.Build(&selected)).Functions
			var wantSelf []metrics.FuncStat
			for _, f := range functions {
				if f.SelfMs > 0 {
					wantSelf = append(wantSelf, f)
				}
			}
			sort.Slice(functions, func(i, j int) bool {
				if functions[i].TotalMs != functions[j].TotalMs {
					return functions[i].TotalMs > functions[j].TotalMs
				}
				return functions[i].Key() < functions[j].Key()
			})
			if !reflect.DeepEqual(trigger.TopSelf, wantSelf) {
				t.Errorf("self = %+v, want %+v", trigger.TopSelf, wantSelf)
			}
			if !reflect.DeepEqual(trigger.TopInclusive, functions) {
				t.Errorf("inclusive = %+v, want %+v", trigger.TopInclusive, functions)
			}
		})
	}
}

func TestTriggerAttributionKeepsUntimedSpansAsLabels(t *testing.T) {
	stack := []model.StackFrame{{Name: "callback", URL: "app.js"}}
	rec := &model.Recording{
		StartTime: 40,
		EndTime:   44,
		Records: []model.Record{
			script("timer-fired", 40.5, 40.75, "gap"),
			script("timer-fired", 40.875, 41.25, "gap"),
			script("timer-fired", 42, 42.25, "gap"),
			script("timer-fired", 40, 40.25, "boundary"),
			script("timer-fired", 43.1, 43.3, "unordered"),
			script("timer-fired", 40, 44, "whole"),
		},
		Samples: []model.TargetSamples{
			{StackTraces: []model.StackTrace{
				{Timestamp: 40.25, StackFrames: stack},
				{Timestamp: 0, StackFrames: stack},
				{Timestamp: 41.75, StackFrames: stack},
				{Timestamp: math.NaN(), StackFrames: stack},
				{Timestamp: 42.75, StackFrames: stack},
			}},
			{StackTraces: []model.StackTrace{
				{Timestamp: 43.5, StackFrames: stack},
				{Timestamp: 0, StackFrames: stack},
				{Timestamp: 43, StackFrames: stack},
			}},
		},
	}
	byName := map[string]TriggerStat{}
	for _, stat := range TriggerStats(rec, 10) {
		byName[stat.Name] = stat
	}
	gap := byName["gap"]
	if len(gap.TopInclusive) != 0 || strings.Count(gap.SampleMessage, "1 untimed samples") != 2 ||
		!strings.Contains(gap.SampleMessage, "+0.25 s and +1.75 s") || !strings.Contains(gap.SampleMessage, "+1.75 s and +2.75 s") {
		t.Errorf("gap = %+v, want two untimed spans without function weight", gap)
	}
	for _, name := range []string{"boundary", "unordered"} {
		if stat := byName[name]; len(stat.TopInclusive) != 0 || stat.SampleMessage != "No samples in this trigger's intervals" {
			t.Errorf("%s = %+v, want no timed samples or untimed labels", name, stat)
		}
	}
	whole := byName["whole"]
	if len(whole.TopSelf) != 1 || whole.TopSelf[0].SelfMs != 5 || whole.TopSelf[0].SelfSamples != 5 || whole.SampleMessage != "" {
		t.Errorf("whole = %+v, want five timed samples only", whole)
	}
}

func BenchmarkTriggerStatsLongRecording(b *testing.B) {
	const (
		seconds      = 120
		sampleRate   = 250
		callbackRate = 50
		timerCount   = 512
	)
	rec := &model.Recording{StartTime: 100, EndTime: 100 + seconds}
	ids := make([][]byte, timerCount)
	for i := range ids {
		ids[i] = strconv.AppendInt(nil, int64(i), 10)
	}
	for i := range seconds * callbackRate {
		start := rec.StartTime + float64(i)/callbackRate
		rec.Records = append(rec.Records, model.Record{
			Type: model.TypeScript, EventType: "timer-fired", StartTime: start, EndTime: start + .012, Details: ids[i%timerCount],
		})
	}
	stacks := make([][]model.StackFrame, 64)
	for i := range stacks {
		stacks[i] = []model.StackFrame{
			{Name: "update", URL: "app.js", Line: i + 1},
			{Name: "poll", URL: "app.js"},
			{Name: "runCallback", URL: "runtime.js"},
			{Name: "dispatch", URL: "runtime.js"},
		}
	}
	target := model.TargetSamples{}
	for i := range seconds * sampleRate {
		target.StackTraces = append(target.StackTraces, model.StackTrace{
			Timestamp: rec.StartTime + (float64(i)+.5)/sampleRate, StackFrames: stacks[i%len(stacks)],
		})
	}
	rec.Samples = []model.TargetSamples{target}
	// Analyze already builds the tree before computing trigger stats.
	tree := flame.Build(rec)
	b.ReportAllocs()
	for b.Loop() {
		if got := triggerStatsWithTree(rec, tree, 10); len(got) != timerCount {
			b.Fatalf("got %d trigger buckets, want %d", len(got), timerCount)
		}
	}
}
