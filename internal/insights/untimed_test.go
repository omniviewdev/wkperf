package insights

import (
	"bytes"
	"encoding/json"
	"math"
	"strings"
	"testing"

	"github.com/omniviewdev/wkperf/internal/model"
	"github.com/omniviewdev/wkperf/internal/profile"
)

func untimedRecording() *model.Recording {
	r := &model.Recording{StartTime: 10, EndTime: 12, Records: []model.Record{
		{Type: model.TypeScript, EventType: "timer-fired", StartTime: 10.2, EndTime: 10.3},
		{Type: model.TypeScript, EventType: "timer-fired", StartTime: 11, EndTime: 11.04},
		{Type: model.TypeRenderingFrame, StartTime: 10.2, EndTime: 10.3},
	}, Samples: []model.TargetSamples{{}}}
	for i := range 50 {
		name, line, at, weight := "untimedLeaf", 10, 0.0, 0.001
		if i >= 25 {
			name, line = "sharedLeaf", 20
		}
		if i >= 40 {
			at, weight = 11+float64(i-40)*0.004, 0.004
		}
		r.Samples[0].StackTraces = append(r.Samples[0].StackTraces, model.StackTrace{Timestamp: at, StackFrames: []model.StackFrame{
			{Name: name, URL: "https://a.test/app.js", Line: line, Column: 1},
			{Name: "appRoot", URL: "https://a.test/app.js", Line: 1, Column: 1},
		}})
		r.Samples[0].Durations = append(r.Samples[0].Durations, weight)
	}
	return r
}

func TestUntimedSamplesAggregateCoverageAndMetrics(t *testing.T) {
	r := Analyze(untimedRecording(), "untimed.json", Options{})
	if r.SampleCoverage.SampleCount != 50 || math.Abs(r.SampleCoverage.SampledMs-80) > 1e-9 || math.Abs(r.SampleCoverage.Ratio-80.0/140) > 1e-9 {
		t.Fatalf("whole-recording coverage dropped untimed samples: %+v", r.SampleCoverage)
	}
	if find(r, "low-sample-coverage") != nil {
		t.Fatal("included untimed weight must prevent false low coverage")
	}
	values := NumericValues(r)
	for key, want := range map[string]float64{"samples.count": 50, "samples.sampled_ms": 80, "samples.untimed_count": 40, "samples.untimed_ms": 40, "samples.untimed_excluded_ms": 0} {
		got, ok := values[key]
		if !ok || math.Abs(got.Value-want) > 1e-9 {
			t.Errorf("%s = %+v, want %v", key, got, want)
		}
		if command := NumericCommand(r, key); strings.Contains(command, "--from") || strings.Contains(command, "--to") {
			t.Errorf("whole-recording command adds window: %s", command)
		}
	}
	payload, err := json.Marshal(r.SampleCoverage)
	if err != nil {
		t.Fatal(err)
	}
	var coverage struct {
		TimedSampleCount   int
		UntimedSampleCount int
		UntimedMs          float64
		UntimedExcluded    bool
		UntimedSpans       []struct {
			FromSec float64 `json:"from_sec"`
			ToSec   float64 `json:"to_sec"`
			Samples int
			Ms      float64
		}
	}
	if err := json.Unmarshal(payload, &coverage); err != nil {
		t.Fatal(err)
	}
	if coverage.TimedSampleCount != 10 || coverage.UntimedSampleCount != 40 || coverage.UntimedMs != 40 || coverage.UntimedExcluded || len(coverage.UntimedSpans) != 1 || coverage.UntimedSpans[0].FromSec != 0 || coverage.UntimedSpans[0].ToSec != 1 {
		t.Fatalf("missing clock accounting: %s", payload)
	}
	var b bytes.Buffer
	if err := RenderMarkdown(&b, r); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(b.String(), "Untimed samples: 40") || !strings.Contains(b.String(), "excluded from frame, task, trigger, window and periodic attribution") {
		t.Fatal("Markdown lacks untimed accounting")
	}
}

func TestUntimedIntervalsExplainMissingClocks(t *testing.T) {
	r := Analyze(untimedRecording(), "untimed.json", Options{})
	if len(r.LongTasks) != 1 || !strings.Contains(r.LongTasks[0].TopFunc, "40 untimed samples") || !strings.Contains(r.LongTasks[0].TopFunc, "cannot be placed") {
		t.Fatalf("task must not imply absent JavaScript: %+v", r.LongTasks)
	}
	if len(r.SlowFrames) != 1 || !strings.Contains(r.SlowFrames[0].TopFunc, "40 untimed samples") {
		t.Fatalf("frame lacks untimed status: %+v", r.SlowFrames)
	}
	f := find(r, "long-tasks")
	if f == nil || !strings.Contains(strings.Join(f.ConfidenceReasons, " "), "50% of sampled weight has no timestamp") {
		t.Fatalf("temporal confidence lacks missing clocks: %+v", f)
	}
}

func TestUntimedOnlyHotspotKeepsWholeRecordingContext(t *testing.T) {
	rec := untimedRecording()
	for i := range 40 {
		rec.Samples[0].StackTraces[i].StackFrames[0].Name = "untimedLeaf"
	}
	for i := 40; i < 50; i++ {
		rec.Samples[0].Durations[i] = 0.002
	}
	r := Analyze(rec, "untimed.json", Options{})
	f := find(r, "hot-function")
	if f == nil || len(f.SuggestedWindows) != 1 || f.SuggestedWindows[0].FromSec != 0 || f.SuggestedWindows[0].ToSec != 2 || !strings.Contains(f.SuggestedWindows[0].Reason, "only untimed samples; no time window available") {
		t.Fatalf("untimed-only function got fabricated time context: %+v", f)
	}
}

func TestUntimedPartialCoverageExplainsExclusion(t *testing.T) {
	rec := profile.Clip(untimedRecording(), 10.1, 10.4)
	r := Analyze(rec, "untimed.json", Options{})
	c := r.SampleCoverage
	if c.SampleCount != 0 || c.TimedSampleCount != 0 || c.SampledMs != 0 || c.UntimedSampleCount != 40 || c.UntimedMs != 40 || !c.UntimedExcluded {
		t.Fatalf("partial-window clock accounting: %+v", c)
	}
	if len(c.UntimedSpans) != 1 || c.UntimedSpans[0].FromSec != 0 || c.UntimedSpans[0].ToSec != 1 {
		t.Fatalf("spans must stay original-recording relative: %+v", c.UntimedSpans)
	}
	if got := NumericValues(r)["samples.untimed_excluded_ms"].Value; got != 40 {
		t.Fatalf("excluded weight = %v, want 40", got)
	}
	f := find(r, "low-sample-coverage")
	if f == nil || !strings.Contains(f.Suggestion, "Analyze the whole recording") || strings.Contains(f.Suggestion, "sampling enabled") || !strings.Contains(strings.Join(f.Evidence, " "), "40 untimed samples") {
		t.Fatalf("partial coverage gives wrong capture advice: %+v", f)
	}
	if command := NumericCommand(r, "samples.count"); !strings.Contains(command, "--from") || !strings.Contains(command, "--to") {
		t.Fatalf("partial command omitted exact window: %s", command)
	}
	if len(r.LongTasks) != 1 || !strings.Contains(r.LongTasks[0].TopFunc, "between +0 s and +1 s") {
		t.Fatalf("interval label rebased original export span: %+v", r.LongTasks)
	}
}

func TestUntimedCoverageRatioCanExceedOne(t *testing.T) {
	rec := untimedRecording()
	rec.Records[0].EndTime = 10.21
	rec.Records[1].EndTime = 11.01
	r := Analyze(rec, "untimed.json", Options{})
	if math.Abs(r.SampleCoverage.Ratio-4) > 1e-9 || math.Abs(NumericValues(r)["samples.coverage_ratio"].Value-4) > 1e-9 {
		t.Fatalf("estimated coverage was clamped: %+v", r.SampleCoverage)
	}
}

func TestUntimedNeverEntersWholeSizedTriggerAttribution(t *testing.T) {
	rec := untimedRecording()
	rec.Records = []model.Record{{Type: model.TypeScript, EventType: "timer-fired", StartTime: 10, EndTime: 12}}
	r := Analyze(rec, "untimed.json", Options{})
	if len(r.Triggers) != 1 || len(r.Triggers[0].TopSelf) != 1 || r.Triggers[0].TopSelf[0].Name != "sharedLeaf" || r.Triggers[0].TopSelf[0].SelfMs != 40 {
		t.Fatalf("whole-sized trigger attributed untimed samples: %+v", r.Triggers)
	}
	if len(r.RootCause.LongTasks) != 1 || r.RootCause.LongTasks[0].HotPath == nil || r.RootCause.LongTasks[0].HotPath.SampledMs != 40 {
		t.Fatalf("whole-sized task attributed untimed samples: %+v", r.RootCause.LongTasks)
	}
	idx := newSampleIndex(r.FlameTree, rec.StartTime, 0)
	if got := idx.top(11.5, 11.6); got != "No samples in this interval" {
		t.Fatalf("untimed span labeled unrelated interval: %s", got)
	}
}
