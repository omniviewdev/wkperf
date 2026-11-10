package rootcause_test

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/omniviewdev/wkperf/internal/rootcause"
)

func TestUntimedSamplesKeepRootCauseAggregates(t *testing.T) {
	r := recording()
	for range 40 {
		sample(r, -10, 1, app("appRoot"), app("untimedLeaf"))
	}
	for i := range 10 {
		sample(r, 1+float64(i)*0.004, 4, app("appRoot"), app("sharedLeaf"))
	}
	event(r, "timer-fired", .2, .3)
	report := build(r)
	near(t, stat(report.Stats, "samples.self_ms"), 80)
	near(t, stat(report.Stats, "samples.count"), 50)
	near(t, stat(report.Stats, "samples.untimed_count"), 40)
	near(t, stat(report.Stats, "samples.untimed_ms"), 40)
	var owned float64
	var unknown json.RawMessage
	for _, owner := range report.Owners {
		owned += owner.SelfMs
	}
	for _, a := range report.ContextAttribution {
		if a.Frame.Name == "untimedLeaf" && len(a.Stacks) == 1 {
			unknown, _ = json.Marshal(a.Stacks[0])
		}
	}
	near(t, owned, 80)
	var stack struct {
		FirstSec       *float64
		LastSec        *float64
		UntimedSamples int
	}
	if len(unknown) == 0 {
		t.Fatal("untimed application stack missing")
	}
	if err := json.Unmarshal(unknown, &stack); err != nil {
		t.Fatal(err)
	}
	if stack.FirstSec != nil || stack.LastSec != nil || stack.UntimedSamples != 40 || !strings.Contains(string(unknown), `"firstSec":null`) || !strings.Contains(string(unknown), `"lastSec":null`) {
		t.Fatalf("unknown clock fabricated temporal bounds: %s", unknown)
	}
	if len(report.LongTasks) != 1 || !strings.Contains(report.LongTasks[0].SampleStatus, "40 untimed samples") || !strings.Contains(report.LongTasks[0].SampleStatus, "cannot be placed") {
		t.Fatalf("window lacks untimed label: %+v", report.LongTasks)
	}
	if strings.Contains(report.Commands.HotPath, "--from") || strings.Contains(report.Commands.HotPath, "--to") {
		t.Fatalf("whole-recording command adds temporal filter: %s", report.Commands.HotPath)
	}
}

func TestUntimedDepthOutlierHasNoTemporalBounds(t *testing.T) {
	r := recording()
	for range 20 {
		sample(r, 1, 1, app("normal"))
	}
	sample(r, -10, 1, app("deep"))
	for range 63 {
		r.Samples[0].StackTraces[20].StackFrames = append(r.Samples[0].StackTraces[20].StackFrames, app("deep"))
	}
	report := build(r)
	if report.Depth.Max != 64 || len(report.Depth.Outliers) != 1 {
		t.Fatalf("untimed depth excluded: %+v", report.Depth)
	}
	payload, err := json.Marshal(report.Depth.Outliers[0].Stack)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(payload), `"firstSec":null`) || !strings.Contains(string(payload), `"untimedSamples":1`) {
		t.Fatalf("outlier fabricated timestamps: %s", payload)
	}
}

func TestWholeRecordingRoundedBoundsKeepUntimedWeight(t *testing.T) {
	r := recording()
	r.StartTime, r.EndTime = 2.611872458306607, 20.24081204162212
	sample(r, -r.StartTime, 9, app("unknownClock"))
	report := rootcause.Build(r, nil, rootcause.Options{Source: "decimal.json", ToSec: 17.628939583})
	near(t, stat(report.Stats, "samples.self_ms"), 9)
	if strings.Contains(report.Commands.HotPath, "--to") {
		t.Fatalf("rounded whole recording command became partial: %s", report.Commands.HotPath)
	}
}
