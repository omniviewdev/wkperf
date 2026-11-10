package rootcause_test

import (
	"strings"
	"testing"

	"github.com/omniviewdev/wkperf/internal/rootcause"
	"github.com/omniviewdev/wkperf/internal/testgen"
)

func TestMeasurementTaskUnionsAndWorkFirstWindows(t *testing.T) {
	rec := testgen.NestedRecords()
	report := build(rec)
	if len(report.LongTasks) != 3 {
		t.Errorf("nested script records must form three task windows: %+v", report.LongTasks)
	}
	var ms float64
	for _, task := range report.LongTasks {
		ms += task.DurationMs
	}
	near(t, ms, 456)
}

func TestMeasurementStallWindowRanksBehindActualWork(t *testing.T) {
	rec := testgen.InputStallFrames()
	report := build(rec)
	if len(report.SlowFrames) != 1 || len(report.Windows) == 0 || report.Windows[0].ActiveMs < 200 {
		t.Errorf("input stall outranks work-bound window: %+v", report.Windows)
	}
	found := false
	for _, window := range report.Windows {
		if window.Kind == "input-stall" {
			found = true
			near(t, window.DurationMs, 1200)
			near(t, window.ActiveMs, .35)
		}
	}
	if !found {
		t.Error("observed input stall missing from separate triage kind")
	}
}

func TestMeasurementGCLabelsDoNotAssertPauses(t *testing.T) {
	rec := recording()
	event(rec, "garbage-collected", .1, .3)
	event(rec, "timer-fired", .15, .16)
	report := rootcause.Build(rec, nil, rootcause.Options{Source: "capture.json"})
	if len(report.GCOverlaps) != 1 {
		t.Fatal("collector interval missing")
	}
	gc := report.GCOverlaps[0]
	if strings.Contains(strings.ToLower(gc.Attribution), "pause") || !strings.Contains(gc.Attribution, "not necessarily main-thread") {
		t.Errorf("collector interval must not assert main-thread pause: %+v", gc)
	}
	for _, stat := range gc.Stats {
		if strings.Contains(strings.ToLower(stat.Label), "pause") {
			t.Errorf("GC scalar label asserts pause: %+v", stat)
		}
	}
}
