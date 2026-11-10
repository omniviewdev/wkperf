package insights

import (
	"bytes"
	"encoding/json"
	"math"
	"strings"
	"testing"

	"github.com/omniviewdev/wkperf/internal/model"
)

func idleRecording() *model.Recording {
	r := &model.Recording{DisplayName: "idle", StartTime: 0, EndTime: 10}
	for i := 0; i < 10; i++ {
		s := float64(i)
		r.Records = append(r.Records, frame(s, s+1), script("timer-fired", s+0.001, s+0.006, "poll"), layout("layout", s+0.006, s+0.008))
	}
	return r
}

func TestIdleGapsAreNotJankOrUnattributedWork(t *testing.T) {
	r := Analyze(idleRecording(), "idle.json", Options{})
	if len(r.SlowFrames) != 0 || find(r, "jank") != nil || find(r, "forced-layout") != nil {
		t.Fatalf("idle recording reported as jank or forced layout: %+v", r.Findings)
	}
	idle, unattributed := 0.0, 0.0
	for _, s := range r.Breakdown {
		if s.Name == "Idle" {
			idle = s.Ms
		}
		if s.Name == "Frame time not attributed to work" {
			unattributed = s.Ms
		}
	}
	if math.Abs(idle-9930) > 0.01 || unattributed > 0.01 {
		t.Fatalf("idle=%v unattributed=%v; idle gaps must count as unused time", idle, unattributed)
	}
}

func TestDefaultModuleGroupingKeepsPluginFilesDistinct(t *testing.T) {
	a := "http://localhost/plugins/plugin-a.js?cache=1#source"
	b := "http://localhost/plugins/plugin-b.js?cache=2"
	if moduleOf(a) != "http://localhost/plugins/plugin-a.js" || moduleOf(b) != "http://localhost/plugins/plugin-b.js" {
		t.Fatalf("file URL grouping collapsed plugin bundles: %q %q", moduleOf(a), moduleOf(b))
	}
}

func TestTinySampledHotspotDoesNotImplyHighImpact(t *testing.T) {
	r := &model.Recording{EndTime: 180, Samples: []model.TargetSamples{{StackTraces: []model.StackTrace{sample(1, "tiny", "http://x/plugin.js")}}}}
	rep := Analyze(r, "tiny.json", Options{})
	if find(rep, "hot-function") != nil || find(rep, "hot-module") != nil {
		t.Fatalf("one millisecond across three minutes must not be a hotspot finding: %+v", rep.Findings)
	}
}

func TestPostScriptRenderingIsNotForcedLayout(t *testing.T) {
	r := &model.Recording{EndTime: 2}
	for i := 0; i < 10; i++ {
		s := float64(i) * 0.1
		r.Records = append(r.Records, frame(s, s+0.025), script("event-dispatched", s, s+0.005, "scroll"), layout("layout", s+0.006, s+0.020))
	}
	rep := Analyze(r, "normal.json", Options{})
	if find(rep, "forced-layout") != nil {
		t.Fatal("normal rendering after script is not forced synchronous layout")
	}
	if f := find(rep, "layout-after-script"); f == nil || !strings.Contains(f.Suggestion, "invalidation") {
		t.Fatalf("normal rendering needs separate style-invalidation advice: %+v", f)
	}
}

func TestSamplesAttributeFramesWithoutScriptRecords(t *testing.T) {
	r := &model.Recording{EndTime: 1, Records: []model.Record{frame(0.1, 0.14)}, Samples: []model.TargetSamples{{StackTraces: []model.StackTrace{{Timestamp: 0.11, StackFrames: []model.StackFrame{{Name: "minified", URL: "http://x/bundle.js", Line: 1, Column: 917}}}}}}}
	rep := Analyze(r, "sample-only.json", Options{})
	if len(rep.SlowFrames) != 1 || !strings.Contains(rep.SlowFrames[0].TopFunc, "bundle.js:1:917") {
		t.Fatalf("sample timestamps must attribute frame without script records, preserving columns: %+v", rep.SlowFrames)
	}
}

func TestIntervalsWithoutSamplesSaySo(t *testing.T) {
	r := Analyze(&model.Recording{EndTime: 1, Records: []model.Record{frame(0.1, 0.2), script("timer-fired", 0.1, 0.2, "42")}}, "no-samples.json", Options{})
	if len(r.SlowFrames) != 1 || !strings.Contains(strings.ToLower(r.SlowFrames[0].TopFunc), "no samples") || !strings.Contains(strings.ToLower(r.LongTasks[0].TopFunc), "no samples") {
		t.Fatalf("missing interval samples need explicit message: frames=%+v tasks=%+v", r.SlowFrames, r.LongTasks)
	}
	var b bytes.Buffer
	if err := RenderMarkdown(&b, r); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(strings.ToLower(b.String()), "no samples") {
		t.Fatal("report must state absent samples")
	}
}

func TestTriggerTablesAndIdleWakeups(t *testing.T) {
	r := idleRecording()
	r.Samples = []model.TargetSamples{{StackTraces: []model.StackTrace{
		{Timestamp: 0.003, StackFrames: []model.StackFrame{{Name: "poll", URL: "plugin.js", Line: 1, Column: 14}, {Name: "schedule", URL: "host.js", Line: 2}}},
		{Timestamp: 1.003, StackFrames: []model.StackFrame{{Name: "poll", URL: "plugin.js", Line: 1, Column: 14}, {Name: "schedule", URL: "host.js", Line: 2}}},
	}}}
	rep := Analyze(r, "idle.json", Options{})
	if rep.Wakeups.Count != 10 || math.Abs(rep.Wakeups.PerSecond-1) > 0.001 || len(rep.Wakeups.Causes) != 1 {
		t.Fatalf("idle wakeup counts/causes: %+v", rep.Wakeups)
	}
	if len(rep.Triggers) != 1 {
		t.Fatalf("trigger table: %+v", rep.Triggers)
	}
	tr := rep.Triggers[0]
	if tr.Kind != "timer" || tr.Name != "poll" || tr.Count != 10 || math.Abs(tr.TotalMs-50) > 0.001 || math.Abs(tr.MeanMs-5) > 0.001 || math.Abs(tr.P95Ms-5) > 0.001 || math.Abs(tr.MaxMs-5) > 0.001 {
		t.Fatalf("trigger aggregate: %+v", tr)
	}
	if len(tr.TopSelf) == 0 || tr.TopSelf[0].Name != "poll" || len(tr.TopInclusive) < 2 {
		t.Fatalf("trigger interval function ranking: %+v", tr)
	}
}

func TestTriggerSamplesStayInsideIntervals(t *testing.T) {
	r := &model.Recording{EndTime: 2, Records: []model.Record{
		script("event-dispatched", 0.1, 0.2, "click"),
		script("animation-frame-fired", 0.3, 0.31, ""),
		script("microtask-fired", 0.4, 0.42, ""),
	}, Samples: []model.TargetSamples{{StackTraces: []model.StackTrace{
		{Timestamp: 0.15, StackFrames: []model.StackFrame{{Name: "clickWork", URL: "app.js"}}},
		{Timestamp: 0.2, StackFrames: []model.StackFrame{{Name: "outside", URL: "app.js"}}},
		{Timestamp: 0.305, StackFrames: []model.StackFrame{{Name: "draw", URL: "app.js"}}},
	}}}}
	rep := Analyze(r, "triggers.json", Options{})
	if len(rep.Triggers) != 3 {
		t.Fatalf("all callback trigger kinds required: %+v", rep.Triggers)
	}
	for _, tr := range rep.Triggers {
		for _, f := range tr.TopInclusive {
			if f.Name == "outside" {
				t.Fatal("end boundary sample leaked into trigger")
			}
		}
		if tr.Kind == "microtask" && !strings.Contains(strings.ToLower(tr.SampleMessage), "no samples") {
			t.Fatalf("missing no-sample message: %+v", tr)
		}
	}
}

func TestFindingHasNumericEvidenceAndRerunContract(t *testing.T) {
	r := Analyze(&model.Recording{EndTime: 2, Records: []model.Record{script("event-dispatched", 0.1, 0.4, "click")}}, "capture.json", Options{})
	for _, f := range r.Findings {
		if len(f.NumericEvidence) == 0 || len(f.MetricKeys) == 0 || len(f.Verify) == 0 || f.Confidence != "low" || len(f.ConfidenceReasons) == 0 || len(f.SuggestedWindows) == 0 {
			t.Errorf("%s incomplete structured finding: %+v", f.ID, f)
		}
		for _, v := range f.Verify {
			if v.Direction == "" || v.Unit == "" || v.Description == "" {
				t.Errorf("invalid verification %+v", v)
			}
		}
		for _, w := range f.SuggestedWindows {
			whole := w.FromSec == 0 && w.ToSec == 2
			if w.FromSec < 0 || w.ToSec <= w.FromSec || w.ToSec > 2 || (!whole && (len(w.Flags) < 4 || w.Flags[0] != "--from" || w.Flags[2] != "--to")) {
				t.Errorf("invalid rerun window %+v", w)
			}
		}
	}
	if f := find(r, "long-tasks"); f == nil || len(f.Triggers) == 0 {
		t.Fatalf("must name measured trigger: %+v", f)
	}
}

func TestMarkdownBudgetOrderAndJSONCompleteness(t *testing.T) {
	r := Analyze(&model.Recording{EndTime: 1, Records: []model.Record{script("timer-fired", 0.1, 0.4, "42")}}, "capture.json", Options{})
	var b bytes.Buffer
	if err := RenderMarkdownWithOptions(&b, r, MarkdownOptions{Brief: true, MaxBytes: 500}); err != nil {
		t.Fatal(err)
	}
	if b.Len() > 500 || !strings.Contains(b.String(), MarkdownTruncationMarker) {
		t.Fatalf("bounded Markdown bytes=%d: %s", b.Len(), b.String())
	}
	b.Reset()
	if err := RenderMarkdownWithOptions(&b, r, MarkdownOptions{Brief: true}); err != nil {
		t.Fatal(err)
	}
	s := b.String()
	if !(strings.Index(s, "## Findings") < strings.Index(s, "## Hot functions") && strings.Index(s, "## Hot functions") < strings.Index(s, "## Verification")) {
		t.Fatalf("brief sections out of order: %s", s)
	}
	if strings.Contains(s, "## Wall-time") {
		t.Fatal("brief output includes full-detail sections")
	}
	if len(r.Findings[0].Evidence) == 0 || len(r.Triggers) != 1 {
		t.Fatal("Markdown budget mutated complete report")
	}
}

func TestModuleParentRollupIsOptIn(t *testing.T) {
	r := &model.Recording{EndTime: 1, Samples: []model.TargetSamples{{StackTraces: []model.StackTrace{
		{Timestamp: 0.1, StackFrames: []model.StackFrame{{Name: "a", URL: "http://x/plugins/a.js?v=1"}}},
		{Timestamp: 0.2, StackFrames: []model.StackFrame{{Name: "b", URL: "http://x/plugins/b.js#s"}}},
	}}}}
	if rep := Analyze(r, "", Options{}); len(rep.Modules) != 2 {
		t.Fatalf("default file identities: %+v", rep.Modules)
	}
	if rep := Analyze(r, "", Options{ModuleGrouping: "parent-directory"}); len(rep.Modules) != 1 || rep.Modules[0].Name != "http://x/plugins" {
		t.Fatalf("parent rollup: %+v", rep.Modules)
	}
}

func TestLayoutHintsNameMeasuredNodeAndDominantTrigger(t *testing.T) {
	target := layout("layout", 0.8, 0.85)
	target.DOMNodeCSSPath = "#plugin-panel > .items"
	target.DOMNodeDisplayName = "div.items"
	r := &model.Recording{EndTime: 3, Records: []model.Record{
		script("timer-fired", 0.1, 0.5, "unrelated"),
		script("timer-fired", 0.6, 0.9, "panel-update"),
		target,
	}, Samples: []model.TargetSamples{{StackTraces: []model.StackTrace{
		{Timestamp: 0.2, StackFrames: []model.StackFrame{{Name: "unrelatedWork", URL: "other.js"}}},
		{Timestamp: 0.81, StackFrames: []model.StackFrame{{Name: "measurePanel", URL: "panel.js"}}},
	}}}}
	rep := Analyze(r, "nodes.json", Options{})
	f := find(rep, "forced-layout")
	if f == nil {
		t.Fatal("layout beginning inside script must retain synchronous evidence")
	}
	if !strings.Contains(strings.Join(f.Evidence, " "), target.DOMNodeCSSPath) || !strings.Contains(f.Suggestion, target.DOMNodeCSSPath) {
		t.Fatalf("missing measured selector/node clue: %+v", f)
	}
	if strings.Contains(strings.Join(f.Triggers, " "), "unrelated") || !strings.Contains(strings.Join(f.Triggers, " "), "panel-update") || strings.Contains(strings.Join(f.Functions, " "), "unrelatedWork") {
		t.Fatalf("function/trigger clues must overlap actual worst layout window: %+v", f)
	}
	w := f.SuggestedWindows[0]
	if math.Abs(w.FromSec-0.6) > 1e-9 || math.Abs(w.ToSec-0.9) > 1e-9 {
		t.Fatalf("window must cover implicated measured script, not unrelated worst task: %+v", w)
	}
}

func TestNativeAnimationSamplingDoesNotProveForcedLayout(t *testing.T) {
	r := &model.Recording{EndTime: 2, Samples: []model.TargetSamples{{StackTraces: []model.StackTrace{
		{Timestamp: 0.1, StackFrames: []model.StackFrame{{Name: "getAnimations", URL: "[native code]"}}},
	}}}}
	if f := find(Analyze(r, "native.json", Options{}), "forced-layout"); f != nil {
		t.Fatalf("native sample alone is not forced-layout evidence: %+v", f)
	}
}

func TestBoundedBriefReservesFindingsBeforeDetailHeadings(t *testing.T) {
	r := Analyze(&model.Recording{EndTime: 2, Records: []model.Record{script("timer-fired", 0.1, 0.4, "42")}}, "capture.json", Options{})
	var b bytes.Buffer
	if err := RenderMarkdownWithOptions(&b, r, MarkdownOptions{Brief: true, MaxBytes: 1800}); err != nil {
		t.Fatal(err)
	}
	for _, finding := range r.Findings {
		if !strings.Contains(b.String(), finding.ID) {
			t.Errorf("missing reserved finding %q: %s", finding.ID, b.String())
		}
	}
	if !strings.Contains(b.String(), "- Source:") {
		t.Error("bounded report dropped source identity")
	}
	if b.Len() > 1800 || !strings.Contains(b.String(), MarkdownTruncationMarker) {
		t.Fatalf("invalid hard budget bytes=%d", b.Len())
	}
}

func TestNumericCommandsKeepSourceMapsAndOriginalWindow(t *testing.T) {
	r := Analyze(&model.Recording{StartTime: 102, EndTime: 104, Records: []model.Record{script("timer-fired", 102.1, 102.4, "42")}}, "capture file.json", Options{WindowStartSec: 2, Sourcemaps: []string{"plugin file.js.map"}})
	values := NumericValues(r)
	for _, f := range r.Findings {
		for _, e := range f.NumericEvidence {
			value, ok := values[e.MetricKey]
			if !ok && r.Summary != nil {
				_, ok = r.Summary.Lookup(e.MetricKey)
			}
			if !ok {
				t.Errorf("missing exact numeric key %q", e.MetricKey)
			}
			if e.MetricKey == "findings."+f.ID+".impact_ms" && math.Abs(value.Value-f.ImpactMs) > 1e-9 {
				t.Error("numeric query impact differs")
			}
			if !strings.Contains(e.Command, "--from 2 --to 4") || !strings.Contains(e.Command, "--sourcemap 'plugin file.js.map'") {
				t.Errorf("irreproducible command %q", e.Command)
			}
		}
	}
}

func TestSparseSampleSeverityUsesAbsoluteWallImpact(t *testing.T) {
	r := &model.Recording{EndTime: 1, Records: []model.Record{script("timer-fired", 0, 0.9, "render")}}
	for i := range 50 {
		r.Samples = append(r.Samples, model.TargetSamples{StackTraces: []model.StackTrace{{Timestamp: float64(i) * 0.001, StackFrames: []model.StackFrame{{Name: "render", URL: "plugin.js", Line: 1, Column: 700}}}}})
	}
	rep := Analyze(r, "sparse.json", Options{})
	for _, id := range []string{"hot-function", "hot-module"} {
		f := find(rep, id)
		if f == nil || f.Severity == SevHigh || f.Severity == SevMedium || !strings.Contains(strings.Join(f.Evidence, " "), "coverage ratio") {
			t.Fatalf("sparse coverage must downgrade sampled concentration: %+v", f)
		}
	}
}

func TestGeneratedAndOriginalLocationsRemainVisible(t *testing.T) {
	r := &model.Recording{EndTime: 1, Samples: []model.TargetSamples{{StackTraces: []model.StackTrace{{Timestamp: 0.1, StackFrames: []model.StackFrame{{Name: "a", URL: "http://x/plugin.js", Line: 1, Column: 917, Original: &model.SourceLocation{Name: "renderPanel", URL: "src/Panel.tsx", Line: 42, Column: 9}}}}}}}}
	rep := Analyze(r, "mapped.json", Options{})
	for _, render := range []func(*bytes.Buffer) error{func(b *bytes.Buffer) error { return RenderMarkdown(b, rep) }, func(b *bytes.Buffer) error { return RenderTerm(b, rep, false) }} {
		var b bytes.Buffer
		if err := render(&b); err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(b.String(), "src/Panel.tsx:42:9") || !strings.Contains(b.String(), "http://x/plugin.js:1:917") {
			t.Fatalf("mapped and generated identity must both survive: %s", b.String())
		}
	}
}

func TestPairedSampleShuffleKeepsCompleteReportDeterministic(t *testing.T) {
	r := &model.Recording{EndTime: 2, Records: []model.Record{frame(0, 0.1), script("timer-fired", 0, 0.1, "poll")}}
	samples := model.TargetSamples{}
	for i := range 120 {
		frames := []model.StackFrame{{Name: "a", URL: "http://x/plugin.js", Line: 1, Column: 100 + i%2}}
		depth := 67
		if i == 0 {
			depth = 366
		}
		for d := 1; d < depth; d++ {
			frames = append(frames, model.StackFrame{Name: "caller" + string(rune('a'+d%24)), URL: "http://x/host.js", Line: 1, Column: d})
		}
		samples.StackTraces = append(samples.StackTraces, model.StackTrace{Timestamp: float64(i%20) * 0.005, StackFrames: frames})
		samples.Durations = append(samples.Durations, 0.001+float64(i%5)*0.0001)
	}
	r.Samples = []model.TargetSamples{samples}
	first := Analyze(r, "deterministic.json", Options{})
	for left, right := 0, len(samples.StackTraces)-1; left < right; left, right = left+1, right-1 {
		samples.StackTraces[left], samples.StackTraces[right] = samples.StackTraces[right], samples.StackTraces[left]
		samples.Durations[left], samples.Durations[right] = samples.Durations[right], samples.Durations[left]
	}
	second := Analyze(r, "deterministic.json", Options{})
	a, err := json.Marshal(first)
	if err != nil {
		t.Fatal(err)
	}
	b, err := json.Marshal(second)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(a, b) {
		t.Fatal("paired sample order changed complete report JSON")
	}
}

func TestNumericCommandsKeepNondefaultThresholds(t *testing.T) {
	r := Analyze(&model.Recording{EndTime: 1, Records: []model.Record{script("timer-fired", 0.1, 0.13, "42")}}, "custom.json", Options{JankMs: 25, LongTaskMs: 20})
	command := NumericCommand(r, "longTasks.0.ms")
	if !strings.Contains(command, "--jank-ms 25") || !strings.Contains(command, "--long-task-ms 20") {
		t.Fatalf("custom thresholds lost: %s", command)
	}
}

func TestRootDetectorsHaveCanonicalActionableFindingMetadata(t *testing.T) {
	r := &model.Recording{EndTime: 3}
	ts := model.TargetSamples{}
	for i := range 20 {
		at := float64(i) * 0.1
		r.Records = append(r.Records, script("timer-fired", at, at+0.003, "poll"))
		ts.StackTraces = append(ts.StackTraces, model.StackTrace{Timestamp: at + 0.001, StackFrames: []model.StackFrame{{Name: "getAnimations"}, {Name: "pollAnimations", URL: "plugin.js", Line: 1, Column: 900}}})
		ts.Durations = append(ts.Durations, 0.003)
	}
	r.Samples = []model.TargetSamples{ts}
	rep := Analyze(r, "polling.json", Options{})
	for _, id := range []string{"suspected-layout-read", "periodic-root"} {
		f := find(rep, id)
		if f == nil || len(f.NumericEvidence) == 0 || len(f.MetricKeys) == 0 || len(f.Verify) == 0 || f.Confidence != "low" || len(f.SuggestedWindows) == 0 || f.Suggestion == "" {
			t.Fatalf("root detector lacks canonical actionable metadata: %s %+v", id, f)
		}
	}
	if find(rep, "forced-layout") != nil {
		t.Fatal("native sampling must remain suspected, never explicit forced layout")
	}
}

func TestPausedRecordsDoNotCreateHeavyWorkFindings(t *testing.T) {
	r := &model.Recording{EndTime: 10, Discontinuities: []model.Discontinuity{{StartTime: 2, EndTime: 8}}, Records: []model.Record{script("event-dispatched", 3, 7, "click"), layout("forced-layout", 3.5, 3.6)}}
	rep := Analyze(r, "paused.json", Options{})
	if len(rep.LongTasks) != 0 || find(rep, "event-handler-heavy") != nil || find(rep, "forced-layout") != nil {
		t.Fatalf("paused records are not observed work: %+v", rep.Findings)
	}
}
