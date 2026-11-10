package rootcause_test

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/omniviewdev/wkperf/internal/flame"
	"github.com/omniviewdev/wkperf/internal/model"
	"github.com/omniviewdev/wkperf/internal/rootcause"
)

func TestOutputCommandsUseStableFunctionSelectors(t *testing.T) {
	rec := recording()
	rec.EndTime = rec.StartTime + 4
	for i, name := range []string{"", "heartbeat", "heartbeat"} {
		f := app(name)
		f.Column += i
		for fire := range 6 {
			sample(rec, .1+float64(fire)*.5+float64(i)*.01, 1, f)
		}
	}
	report := build(rec)
	for _, attribution := range report.ContextAttribution {
		id := flame.FunctionID(attribution.Frame)
		if !strings.Contains(attribution.Command, "paths '"+id+"'") {
			t.Errorf("ambiguous attribution command: %s", attribution.Command)
		}
		payload, err := json.Marshal(attribution.Frame)
		if err != nil {
			t.Fatal(err)
		}
		var frame map[string]any
		if err := json.Unmarshal(payload, &frame); err != nil {
			t.Fatal(err)
		}
		if frame["id"] != id {
			t.Errorf("frame lacks stable selector: %s", payload)
		}
	}
	if !strings.Contains(report.Commands.Paths, "paths 'f_") {
		t.Errorf("entry command uses display selector: %s", report.Commands.Paths)
	}
	if len(report.PeriodicRoots) != 3 {
		t.Fatalf("periodic fixture: %d roots", len(report.PeriodicRoots))
	}
	for _, periodic := range report.PeriodicRoots {
		if !strings.Contains(periodic.Command, "paths '"+flame.FunctionID(periodic.Root)+"'") {
			t.Errorf("ambiguous periodic command: %s", periodic.Command)
		}
	}
}

func TestOutputTimelineOnlyCommandsReproduceScalars(t *testing.T) {
	rec := recording()
	event(rec, "event-dispatched", .1, .2)
	rec.Records = append(rec.Records, model.Record{Type: model.TypeScript, EventType: "garbage-collected", StartTime: rec.StartTime + .12, EndTime: rec.StartTime + .15})
	report := build(rec)
	if len(report.GCOverlaps) != 1 || len(report.LongTasks) != 1 {
		t.Fatalf("timeline fixture: %+v", report)
	}
	stats := rootcause.Stats(report)
	for _, command := range []string{report.GCOverlaps[0].Command, report.LongTasks[0].Command, report.Windows[0].Command} {
		if !strings.Contains(command, " rootcause '") || !strings.Contains(command, ".duration_ms'") {
			t.Errorf("timeline-only command queries missing rows: %s", command)
		}
		found := false
		for _, stat := range stats {
			found = found || stat.Command == command && stat.Value > 0
		}
		if !found {
			t.Errorf("timeline-only scalar command does not name retained stat: %s", command)
		}
	}
}

func TestOutputUnsampledEntryCommandsReturnScalarEvidence(t *testing.T) {
	rec := recording()
	event(rec, "event-dispatched", .1, .2)
	rec.Records = append(rec.Records, model.Record{Type: model.TypeScript, EventType: "garbage-collected", StartTime: rec.StartTime + .12, EndTime: rec.StartTime + .15})
	report := build(rec)
	for _, command := range []string{report.Commands.HotPath, report.Commands.Window, report.Commands.Paths, report.Commands.Depth, report.Commands.Stacks, report.HotPath.Command, report.Depth.Command} {
		found := false
		for _, stat := range rootcause.Stats(report) {
			found = found || stat.Command == command
		}
		if !found {
			t.Errorf("unsampled entry command cannot return evidence: %s", command)
		}
	}
	for _, overlap := range report.GCOverlaps {
		for _, event := range overlap.Events {
			if !strings.Contains(event.Command, " rootcause '") || !strings.Contains(event.Command, ".overlap_ms'") {
				t.Errorf("GC event overlap queries trigger rows instead of measured scalar: %s", event.Command)
			}
		}
	}
}

func TestSparseWindowCommandsKeepRetrievableEvidence(t *testing.T) {
	rec := recording()
	event(rec, "event-dispatched", .1, .2)
	sample(rec, .15, 2, app("rare"))
	report := build(rec)
	if len(report.LongTasks) != 1 || report.LongTasks[0].TopStack == nil || report.LongTasks[0].HotPath == nil {
		t.Fatal("sparse fixture must retain both timeline and sampled evidence")
	}
	window := report.LongTasks[0]
	if len(window.HotPath.Nodes) != 0 || window.HotPath.StopReason != "sample-support" {
		t.Fatal("one observation must not establish a supported hot path")
	}
	if !strings.Contains(window.Command, " stacks '"+window.TopStack.StackID+"'") {
		t.Errorf("sparse window must retrieve its exact observed stack: %s", window.Command)
	}
	for _, command := range []string{report.HotPath.Command, window.HotPath.Command} {
		found := false
		for _, stat := range rootcause.Stats(report) {
			found = found || command == stat.Command
		}
		if !found {
			t.Errorf("unsupported hot path must retrieve retained scalar evidence: %s", command)
		}
	}
}
