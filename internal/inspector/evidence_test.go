package inspector

import (
	"encoding/json"
	"slices"
	"testing"

	"github.com/omniviewdev/wkperf/internal/model"
)

func timelineEvidence(t *testing.T, e *evidence, record string) {
	t.Helper()
	if err := e.event(message{Method: "Timeline.eventRecorded", Params: json.RawMessage(`{"record":` + record + `}`)}); err != nil {
		t.Fatal(err)
	}
}

func testEvidence(scriptStart, timelineStart, timelineEnd, scriptEnd float64) evidence {
	return evidence{scriptStart: &scriptStart, timelineStart: &timelineStart, timelineEnd: &timelineEnd, scriptEnd: &scriptEnd}
}

func TestLayoutAttribution(t *testing.T) {
	for _, tc := range []struct {
		name, stack, kind string
	}{
		{"empty legacy", `[]`, "layout"},
		{"empty modern", `{"callFrames":[]}`, "layout"},
		{"native only", `{"callFrames":[{"url":"[native code]","scriptId":"0"}]}`, "layout"},
		{"no source", `{"callFrames":[{"functionName":"unknown"}]}`, "layout"},
		{"async parent only", `{"callFrames":[],"parentStackTrace":{"callFrames":[{"scriptId":"42"}]}}`, "layout"},
		{"legacy URL", `[{"url":"https://example.test/app.js"}]`, "forced-layout"},
		{"modern script ID", `{"callFrames":[{"url":"[native code]"},{"scriptId":"42"}]}`, "forced-layout"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var e evidence
			timelineEvidence(t, &e, `{"type":"Layout","startTime":1,"endTime":2,"stackTrace":`+tc.stack+`}`)
			if len(e.records) != 1 || e.records[0].EventType != tc.kind {
				t.Fatalf("want %q, got %+v", tc.kind, e.records)
			}
		})
	}
}

func TestScriptParentAttribution(t *testing.T) {
	for _, child := range []string{"FunctionCall", "EvaluateScript"} {
		for _, tc := range []struct {
			parent, data, kind, detail string
		}{
			{"TimerFire", `{"timerId":42}`, "timer-fired", `42`},
			{"ObserverCallback", `{"type":"resize"}`, "observer-callback", `"resize"`},
			{"FireAnimationFrame", `{"id":7}`, "animation-frame-fired", `7`},
			{"EventDispatch", `{"type":"click"}`, "event-dispatched", `"click"`},
		} {
			t.Run(child+"/"+tc.parent, func(t *testing.T) {
				var e evidence
				timelineEvidence(t, &e, `{"type":"`+tc.parent+`","data":`+tc.data+`,"children":[{"type":"`+child+`","startTime":1,"endTime":2}]}`)
				kind, detail := tc.kind, tc.detail
				if child == "EvaluateScript" && tc.parent == "EventDispatch" {
					kind, detail = "script-evaluated", ""
				}
				if len(e.records) != 1 || e.records[0].EventType != kind || string(e.records[0].Details) != detail {
					t.Fatalf("want %q with %q, got %+v", kind, detail, e.records)
				}
			})
		}
	}
}

func TestConsoleProfileAnnotatesWithoutSuppressingScript(t *testing.T) {
	e := testEvidence(1, 1, 5, 5)
	timelineEvidence(t, &e, `{"type":"ConsoleProfile","startTime":1,"endTime":5,"data":{"title":"fixture"},"children":[{"type":"EvaluateScript","startTime":2,"endTime":2.25}]}`)
	if err := e.event(message{Method: "ScriptProfiler.trackingUpdate", Params: json.RawMessage(`{"event":{"type":"Other","startTime":3,"endTime":3.5}}`)}); err != nil {
		t.Fatal(err)
	}
	r, err := e.recording("")
	if err != nil {
		t.Fatal(err)
	}
	if len(r.Records) != 2 || r.Records[0].StartTime != 2 || r.Records[1].StartTime != 3 {
		t.Fatalf("want just child and profiler execution, got %+v", r.Records)
	}
	if len(r.Markers) != 1 || r.Markers[0].Time != 1 || r.Markers[0].Type != "console-profile-recorded" || string(r.Markers[0].Details) != `{"title":"fixture","endTime":5}` {
		t.Fatalf("want console profile interval annotation, got %+v", r.Markers)
	}
}

func TestRecordingValidatesProfilerClockOrder(t *testing.T) {
	for _, tc := range []struct {
		name                       string
		timelineStart, timelineEnd float64
		wantError                  bool
	}{
		{"contained", 2, 3, false},
		{"equal bounds", 1, 4, false},
		{"timeline starts first", 0, 3, true},
		{"timeline stops last", 2, 5, true},
		{"disjoint", 5, 6, true},
		{"inverted", 3, 2, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e := testEvidence(1, tc.timelineStart, tc.timelineEnd, 4)
			r, err := e.recording("")
			if (err != nil) != tc.wantError {
				t.Fatalf("want error=%t, got %v", tc.wantError, err)
			}
			if err == nil && (r.StartTime != 1 || r.EndTime != 4) {
				t.Fatalf("want ScriptProfiler bounds, got [%v, %v)", r.StartTime, r.EndTime)
			}
		})
	}
}

func TestResourceEvidenceUsesHalfOpenBounds(t *testing.T) {
	e := testEvidence(1, 1, 2, 2)
	e.resources = [2]resourceTracker{
		{kind: model.TypeCPU, domain: "CPUProfiler", enabled: true},
		{kind: model.TypeMemory, domain: "Memory", enabled: true},
	}
	e.resourceRecords = []model.Record{
		{Type: model.TypeCPU, Timestamp: 2},
		{Type: model.TypeMemory, Timestamp: 1},
		{Type: model.TypeMemory, Timestamp: 2},
	}
	e.pressure = []model.MemoryPressure{{Timestamp: 1}, {Timestamp: 2}}
	r, err := e.recording("")
	if err != nil {
		t.Fatal(err)
	}
	if len(r.Records) != 1 || r.Records[0].Timestamp != 1 || len(r.MemoryPressureEvents) != 1 || r.MemoryPressureEvents[0].Timestamp != 1 {
		t.Fatalf("want only in-range resource evidence, got %+v", r)
	}
	if got := e.resourceCapabilities(); got["CPUProfiler"] != "enabled-no-samples" || got["Memory"] != "captured" {
		t.Fatalf("unexpected capabilities: %v", got)
	}
}

func TestSampleWeightsPreserveWireOrderAndClockOrigin(t *testing.T) {
	for _, tc := range []struct {
		name       string
		start      float64
		timestamps []float64
		weights    []float64
	}{
		{"untimed between timed samples", 1, []float64{1.25, 0, 1.75, 2}, []float64{0.5, 0.001, 0.5, 0.001}},
		{"real zero with out-of-order samples", 0, []float64{0.75, 0, 1, 0.25}, []float64{1.0 / 3, 1.0 / 3, 0.001, 1.0 / 3}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e := testEvidence(tc.start, tc.start, tc.start+2, tc.start+2)
			e.script = []model.Record{{Type: model.TypeScript, StartTime: tc.start, EndTime: tc.start + 1}}
			for _, ts := range tc.timestamps {
				e.stacks = append(e.stacks, model.StackTrace{Timestamp: ts})
			}
			r, err := e.recording("")
			if err != nil {
				t.Fatal(err)
			}
			var timestamps []float64
			for _, stack := range r.Samples[0].StackTraces {
				timestamps = append(timestamps, stack.Timestamp)
			}
			if !slices.Equal(timestamps, tc.timestamps) || !slices.Equal(r.Samples[0].Durations, tc.weights) {
				t.Fatalf("want timestamps %v with weights %v, got %v with %v", tc.timestamps, tc.weights, timestamps, r.Samples[0].Durations)
			}
		})
	}
}
