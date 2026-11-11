package insights

import (
	"bytes"
	"encoding/json"
	"math"
	"strings"
	"testing"

	"github.com/omniviewdev/wkperf/internal/model"
	"github.com/omniviewdev/wkperf/internal/testgen"
)

func TestMeasurementNestedTasksAndRealCallbackTypes(t *testing.T) {
	rec := testgen.NestedRecords()
	r := Analyze(rec, "nested.json", Options{})
	if len(r.LongTasks) != 3 {
		t.Errorf("nested records must form three task unions: %+v", r.LongTasks)
	}
	if task := r.LongTasks[0]; task.EventType != "timer-fired" || len(task.Children) != 1 || task.Children[0] != "microtask-dispatched" || task.ChildrenTotal != 1 {
		t.Errorf("task union must retain outermost label and nested event types: %+v", task)
	}
	f := find(r, "long-tasks")
	if f == nil || math.Abs(f.ImpactMs-456) > 1e-6 {
		t.Errorf("long-task impact must count each union once: %+v", f)
	}
	for _, id := range []string{"microtask-heavy", "observer-callback-heavy"} {
		if find(r, id) == nil {
			t.Errorf("real WebKit callback type did not produce %s", id)
		}
	}
	kinds := map[string]bool{}
	for _, trigger := range r.Triggers {
		kinds[trigger.Kind] = true
	}
	if !kinds["microtask"] || !kinds["observer"] {
		t.Errorf("real callback triggers missing: %+v", r.Triggers)
	}
	if f := find(r, "microtask-heavy"); f != nil && !strings.Contains(strings.Join(f.Evidence, " "), "overlaps event/timer time") {
		t.Errorf("nested microtask evidence must disclose overlap: %+v", f)
	}
}

func TestMeasurementGCIsOverlayNotWork(t *testing.T) {
	rec := testgen.GCOverlay()
	r := Analyze(rec, "gc.json", Options{})
	if len(r.SlowFrames) != 0 || find(r, "jank") != nil || len(r.RootCause.SlowFrames) != 0 {
		t.Errorf("concurrent collector interval became work-bound jank: %+v", r.SlowFrames)
	}
	for _, slice := range r.Breakdown {
		if slice.Name == "GC" {
			t.Errorf("GC must not be an exclusive wall-time slice: %+v", slice)
		}
	}
	f := find(r, "gc-pressure")
	if f == nil || !strings.Contains(strings.Join(f.Evidence, " "), "collections") || strings.Contains(strings.Join(f.Evidence, " "), "pauses") {
		t.Errorf("GC evidence must name collector intervals, not pauses: %+v", f)
	}
	values := NumericValues(r)
	if math.Abs(values["gc.total_ms"].Value-131.6) > 1e-6 || math.Abs(values["gc.overlap_with_work_ms"].Value-7) > 1e-6 {
		t.Errorf("collector overlay scalars missing: %+v", values)
	}
}

func TestMeasurementInputStallSeparateFromWorkJank(t *testing.T) {
	rec := testgen.InputStallFrames()
	r := Analyze(rec, "stall.json", Options{})
	if len(r.SlowFrames) != 1 || math.Abs(r.SlowFrames[0].Ms-299.5) > 1e-6 {
		t.Errorf("input stall must not appear among work-bound slow frames: %+v", r.SlowFrames)
	}
	f := find(r, "jank")
	if f == nil || math.Abs(f.ImpactMs-(200-16.7)) > 1e-6 {
		t.Errorf("jank impact must use exclusive work excess: %+v", f)
	}
	stall := find(r, "input-stall")
	if stall == nil || math.Abs(stall.ImpactMs-1200) > 1e-6 || !strings.Contains(strings.Join(stall.Evidence, " "), "0.35ms") {
		t.Errorf("input stall must retain interval and measured work: %+v", stall)
	}
	for _, slice := range r.Breakdown {
		if slice.Name == "Frame time not attributed to work" {
			t.Errorf("untruthful unattributed label retained")
		}
		if slice.Name == "Active-frame time without instrumented main-thread work" && math.Abs(slice.Ms-99.5) > 1e-6 {
			t.Errorf("input stall leaked into active-frame unattributed time: %+v", slice)
		}
	}
	if len(r.RootCause.Windows) == 0 || r.RootCause.Windows[0].ActiveMs < 200 {
		t.Errorf("triage must rank actual work first: %+v", r.RootCause.Windows)
	}
	var md bytes.Buffer
	if err := RenderMarkdown(&md, r); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(md.String(), "## Input stalls") {
		t.Error("input-stall details missing from report")
	}
}

func TestMeasurementLayoutFindingsUseExclusiveCost(t *testing.T) {
	rec := testgen.ExclusiveFindingCosts()
	r := Analyze(rec, "rendering.json", Options{})
	for _, tc := range []struct {
		id, slice string
		ms        float64
	}{
		{"style-recalc-heavy", "Style", 100}, {"layout-heavy", "Layout", 100}, {"paint-heavy", "Paint", 50},
	} {
		f := find(r, tc.id)
		if f == nil || math.Abs(f.ImpactMs-tc.ms) > 1e-6 {
			t.Errorf("%s must use exclusive cost %.1fms: %+v", tc.id, tc.ms, f)
			continue
		}
		if !strings.Contains(strings.Join(f.Evidence, " "), "inclusive record time") {
			t.Errorf("%s must label secondary inclusive cost: %+v", tc.id, f)
		}
		if len(f.NumericEvidence) < 2 || !strings.HasSuffix(f.NumericEvidence[0].MetricKey, ".exclusive_ms") || math.Abs(f.NumericEvidence[0].Value-tc.ms) > 1e-6 || !strings.Contains(f.NumericEvidence[1].Description, "inclusive") {
			t.Errorf("%s numeric evidence must put exclusive cost before labeled inclusive cost: %+v", tc.id, f.NumericEvidence)
		}
		for _, slice := range r.Breakdown {
			if slice.Name == tc.slice && math.Abs(slice.Ms-f.ImpactMs) > 1e-6 {
				t.Errorf("%s contradicts exclusive breakdown: %+v", tc.id, slice)
			}
		}
	}
}

func TestMeasurementCustomJankRatioCarriesThreshold(t *testing.T) {
	rec := testgen.ThresholdFrames()
	r := Analyze(rec, "custom.json", Options{JankMs: 325, LongTaskMs: 80})
	f := find(r, "jank")
	if f == nil {
		t.Fatal("custom-threshold finding missing")
	}
	values := NumericValues(r)
	if math.Abs(values["findings.jank.ratio"].Value-.5) > 1e-9 || values["findings.jank.threshold_ms"].Value != 325 {
		t.Errorf("finding ratio must carry configured threshold: %+v", f.NumericEvidence)
	}
	if !strings.Contains(strings.Join(f.Evidence, " "), "fixed 16.7ms") {
		t.Errorf("finding must disclose fixed scorecard budget: %+v", f.Evidence)
	}
	for _, ev := range f.NumericEvidence {
		if !strings.Contains(ev.Command, "--jank-ms 325") || !strings.Contains(ev.Command, "--long-task-ms 80") {
			t.Errorf("custom-threshold scalar command cannot reproduce value: %+v", ev)
		}
	}
	if len(f.Verify) == 0 || f.Verify[0].MetricKey != "findings.jank.ratio" {
		t.Errorf("verification must target configured finding ratio: %+v", f.Verify)
	}
}

func TestMeasurementInputStallEvidenceSelection(t *testing.T) {
	rec := &model.Recording{EndTime: 4, Samples: []model.TargetSamples{{}}}
	for i := range 4 {
		at := float64(i)
		rec.Records = append(rec.Records, frame(at, at+1), script("event-dispatched", at+.1, at+.1001, "wheel"))
		rec.Samples[0].StackTraces = append(rec.Samples[0].StackTraces, sample(at+.10005, "renderWait", "app.js"))
		rec.Samples[0].Durations = append(rec.Samples[0].Durations, .001)
	}
	r := Analyze(rec, "stalls.json", Options{Top: 2})
	data, err := json.Marshal(r)
	if err != nil {
		t.Fatal(err)
	}
	var wire struct {
		Frames              []json.RawMessage       `json:"frames"`
		Stacks              map[string]CatalogStack `json:"stacks"`
		InputStalls         []json.RawMessage       `json:"inputStalls"`
		InputStallsTotal    int                     `json:"inputStallsTotal"`
		MoreEvidenceCommand string                  `json:"moreEvidenceCommand"`
		RootCause           struct {
			InputStalls []struct {
				TopStack *struct {
					StackID string `json:"stackId"`
				} `json:"topStack"`
				HotPath *struct {
					Frames []int `json:"frames"`
				} `json:"hotPath"`
			} `json:"inputStalls"`
			InputStallsTotal int `json:"inputStallsTotal"`
		} `json:"rootCause"`
	}
	if err := json.Unmarshal(data, &wire); err != nil {
		t.Fatal(err)
	}
	if len(wire.InputStalls) != 2 || wire.InputStallsTotal != 4 || len(wire.RootCause.InputStalls) != 2 || wire.RootCause.InputStallsTotal != 4 || !strings.Contains(wire.MoreEvidenceCommand, "--top 4") {
		t.Errorf("input-stall evidence must use shared bounded selection: %s", data)
	}
	for _, stall := range wire.RootCause.InputStalls {
		if stall.TopStack == nil || stall.HotPath == nil {
			t.Fatal("selected input-stall window lacks timed stack evidence")
		}
		if _, ok := wire.Stacks[stall.TopStack.StackID]; !ok {
			t.Errorf("input-stall stack reference missing from shared catalog: %s", stall.TopStack.StackID)
		}
		for _, index := range stall.HotPath.Frames {
			if index < 0 || index >= len(wire.Frames) {
				t.Errorf("input-stall hot path contains non-interned frame index %d", index)
			}
		}
	}
}

func TestMeasurementInputStallPreservesUntimedDisclosure(t *testing.T) {
	rec := testgen.InputStallFrames()
	rec.Samples = []model.TargetSamples{{StackTraces: []model.StackTrace{sample(0, "awaitRender", "app.js")}, Durations: []float64{.2}}}
	r := Analyze(rec, "untimed-stall.json", Options{})
	if len(r.InputStalls) != 1 || !strings.Contains(r.InputStalls[0].TopFunc, "untimed") || !strings.Contains(r.InputStalls[0].SampleStatus, "has no timestamp") {
		t.Fatalf("input-stall attribution must disclose unknown sampler clocks: %+v", r.InputStalls)
	}
	f := find(r, "input-stall")
	if f == nil || !strings.Contains(strings.Join(f.ConfidenceReasons, " "), "has no timestamp") {
		t.Fatalf("input-stall finding lacks untimed confidence disclosure: %+v", f)
	}
	if len(r.RootCause.InputStalls) != 1 || r.RootCause.InputStalls[0].TopStack != nil || !strings.Contains(r.RootCause.InputStalls[0].SampleStatus, "untimed") {
		t.Fatalf("input-stall root window imputed missing sample clocks: %+v", r.RootCause.InputStalls)
	}
}
