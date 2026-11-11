package testgen

import (
	"encoding/json"
	"fmt"
	"io"

	"github.com/omniviewdev/wkperf/internal/model"
)

// Write emits either a flat payload or Safari's versioned recording wrapper.
func Write(w io.Writer, rec *model.Recording, wrapped bool) error {
	encoder := json.NewEncoder(w)
	if !wrapped {
		return encoder.Encode(rec)
	}
	return encoder.Encode(struct {
		Version   int              `json:"version"`
		Recording *model.Recording `json:"recording"`
		Overview  struct{}         `json:"overview"`
	}{Version: 1, Recording: rec})
}

// Empty is a reusable deterministic ten-second capture with work instruments.
func Empty() *model.Recording {
	return &model.Recording{DisplayName: "Deterministic WebKit fixture", StartTime: 0, EndTime: 10,
		InstrumentTypes: []string{model.TypeScript, model.TypeLayout, model.TypeRenderingFrame},
		Records:         []model.Record{}, Markers: []model.Marker{}, MemoryPressureEvents: []model.MemoryPressure{}, Samples: []model.TargetSamples{}}
}

func target() model.Target {
	return model.Target{Identifier: "page", Type: "page", Name: "Fixture", URL: "https://app.test/"}
}
func frameRecord(start, end float64) model.Record {
	return model.Record{Type: model.TypeRenderingFrame, StartTime: start, EndTime: end}
}
func scriptRecord(kind string, start, end float64) model.Record {
	return model.Record{Type: model.TypeScript, EventType: kind, StartTime: start, EndTime: end}
}

// IdleMinified models sparse one-second wakeups with 3–7 ms JavaScript and less
// than 10 ms rendering, plus two genuine interactive frames.
func IdleMinified() *model.Recording {
	rec := Empty()
	rec.DisplayName = "Idle minified wakeups"
	rec.EndTime = 6.032
	root := model.StackFrame{Name: "(program)", URL: "https://app.test/assets/bundle.js", SourceID: "17", Line: 1, Column: 1}
	samples := model.TargetSamples{Target: target()}
	for i := range 6 {
		start, end := float64(i), float64(i+1)
		scriptMs := float64(3 + i%5)
		workStart := end - .012
		rec.Records = append(rec.Records, frameRecord(start, end), scriptRecord("timer-fired", workStart, workStart+scriptMs/1000),
			model.Record{Type: model.TypeLayout, EventType: "recalculate-styles", StartTime: end - .005, EndTime: end - .004},
			model.Record{Type: model.TypeLayout, EventType: "layout", StartTime: end - .004, EndTime: end - .002},
			model.Record{Type: model.TypeLayout, EventType: "paint", StartTime: end - .002, EndTime: end})
		leaf := model.StackFrame{Name: "a", URL: root.URL, SourceID: "17", Line: 1, Column: 120 + i*250}
		samples.StackTraces = append(samples.StackTraces, model.StackTrace{Timestamp: workStart, StackFrames: []model.StackFrame{leaf, root}})
		samples.Durations = append(samples.Durations, scriptMs/1000)
	}
	for i := range 2 {
		start := 6 + float64(i)*.016
		dispatch := scriptRecord("event-dispatched", start, start+.006)
		dispatch.Details = json.RawMessage(`"pointermove"`)
		rec.Records = append(rec.Records, frameRecord(start, start+.016), dispatch)
	}
	rec.Samples = []model.TargetSamples{samples}
	return rec
}

// DeepMinified has 100 samples averaging exactly 67 frames, maximum 366.
func DeepMinified() *model.Recording {
	rec := Empty()
	rec.DisplayName = "Deep minified application stacks"
	rec.EndTime = 1
	samples := model.TargetSamples{Target: target()}
	for i := range 100 {
		depth := 64
		if i == 0 {
			depth = 366
		} else if i <= 2 {
			depth = 63
		}
		frames := make([]model.StackFrame, depth)
		for j := range frames {
			frames[j] = model.StackFrame{Name: fmt.Sprintf("%c", 'a'+j%26), URL: "https://app.test/assets/bundle.js", SourceID: "17", Line: 1, Column: 100 + j*137}
		}
		samples.StackTraces = append(samples.StackTraces, model.StackTrace{Timestamp: float64(i) * .01, StackFrames: frames})
		samples.Durations = append(samples.Durations, .005)
	}
	rec.Records = append(rec.Records, frameRecord(0, .08), scriptRecord("timer-fired", 0, .07))
	rec.Samples = []model.TargetSamples{samples}
	return rec
}

// GetAnimations separates native self cost from framework and app ancestry.
func GetAnimations() *model.Recording {
	rec := Empty()
	rec.DisplayName = "Native getAnimations attribution"
	app := model.StackFrame{Name: "onResize", URL: "https://app.test/src/app.js", Line: 21, Column: 3}
	measure := model.StackFrame{Name: "measureRows", URL: "https://app.test/src/rows.js", Line: 40, Column: 7}
	commit := model.StackFrame{Name: "commitOnFiber", URL: "https://app.test/node_modules/react-dom/index.js", Line: 80, Column: 2}
	native := model.StackFrame{Name: "getAnimations", SourceID: "native", Line: -1, Column: -1}
	rec.Records = append(rec.Records, frameRecord(1, 1.08), scriptRecord("observer-callback", 1, 1.08))
	rec.Samples = []model.TargetSamples{{Target: target(), StackTraces: []model.StackTrace{
		{Timestamp: 1.01, StackFrames: []model.StackFrame{native, commit, measure, app}},
		{Timestamp: 1.03, StackFrames: []model.StackFrame{commit, measure, app}},
		{Timestamp: 1.05, StackFrames: []model.StackFrame{measure, app}},
	}, Durations: []float64{.006, .002, .003}}}
	return rec
}

// React includes render and commit samples inside a click's scheduling
// microtask, and an outside sync sample with no identifiable trigger.
func React() *model.Recording {
	rec := Empty()
	rec.DisplayName = "React microtask attribution"
	app := model.StackFrame{Name: "updateRows", URL: "https://app.test/src/rows.tsx", Line: 30, Column: 5}
	render := model.StackFrame{Name: "renderWithHooks", URL: "https://app.test/node_modules/react-dom/client.js", Line: 200, Column: 1}
	renderRoot := model.StackFrame{Name: "renderRootSync", URL: render.URL, Line: 150, Column: 1}
	commit := model.StackFrame{Name: "commitRoot", URL: render.URL, Line: 400, Column: 1}
	syncRoot := model.StackFrame{Name: "performSyncWorkOnRoot", URL: render.URL, Line: 100, Column: 1}
	click := scriptRecord("event-dispatched", 1, 1.085)
	click.Details = json.RawMessage(`"click"`)
	rec.Records = append(rec.Records, frameRecord(1, 1.09), click, scriptRecord("microtask-dispatched", 1.005, 1.08))
	rec.Samples = []model.TargetSamples{{Target: target(), StackTraces: []model.StackTrace{
		{Timestamp: 1.01, StackFrames: []model.StackFrame{render, renderRoot, syncRoot, app}},
		{Timestamp: 1.04, StackFrames: []model.StackFrame{commit, syncRoot, app}},
		{Timestamp: 2, StackFrames: []model.StackFrame{commit, syncRoot, app}},
	}, Durations: []float64{.020, .015, .050}}}
	return rec
}

// Periodic has six timer firings with three samples per firing. Sampling count
// must not inflate the measured timer-fire count used for cadence evidence.
func Periodic() *model.Recording {
	rec := Empty()
	rec.DisplayName = "Periodic background timer"
	root := model.StackFrame{Name: "pollStatus", URL: "https://app.test/src/poll.js", Line: 10, Column: 1}
	leaf := model.StackFrame{Name: "refreshCache", URL: root.URL, Line: 20, Column: 1}
	samples := model.TargetSamples{Target: target()}
	for i := 1; i <= 6; i++ {
		start := float64(i)
		r := scriptRecord("timer-fired", start, start+.006)
		r.Details = json.RawMessage(`{"timerId":7}`)
		rec.Records = append(rec.Records, frameRecord(start-.994, start+.006), r)
		for j := range 3 {
			samples.StackTraces = append(samples.StackTraces, model.StackTrace{Timestamp: start + float64(j)*.002, StackFrames: []model.StackFrame{leaf, root}})
			samples.Durations = append(samples.Durations, .002)
		}
	}
	rec.Samples = []model.TargetSamples{samples}
	return rec
}

// PausedMinified includes a stopped/resumed interval inside the idle capture.
func PausedMinified() *model.Recording {
	rec := IdleMinified()
	rec.DisplayName = "Paused minified wakeups"
	rec.Discontinuities = []model.Discontinuity{{StartTime: 2, EndTime: 4}}
	return rec
}

// MinifiedSourceMap resolves two same-line generated functions with names.
func MinifiedSourceMap() string {
	return `{"version":3,"file":"bundle.js","sourceRoot":"../src","sources":["view.ts"],"names":["render","flush"],"mappings":"AAAAA,UACIC"}`
}

// UntimedSamples has 40 missing-clock samples and 10 timed samples: 80 ms total.
// Whole-recording aggregates include all 50; partial windows include timed only.
func UntimedSamples() *model.Recording {
	rec := Empty()
	rec.DisplayName = "Untimed samples"
	rec.StartTime, rec.EndTime = 10, 12
	rec.Records = []model.Record{
		scriptRecord("timer-fired", 10.2, 10.3),
		scriptRecord("timer-fired", 11, 11.04),
	}
	root := model.StackFrame{Name: "appRoot", URL: "https://a.test/app.js", Line: 1, Column: 1}
	untimed := model.StackFrame{Name: "untimedLeaf", URL: root.URL, Line: 10, Column: 1}
	shared := model.StackFrame{Name: "sharedLeaf", URL: root.URL, Line: 20, Column: 1}
	target := model.TargetSamples{Target: target(), StackTraces: make([]model.StackTrace, 0, 50), Durations: make([]float64, 0, 50)}
	for i := range 50 {
		leaf, timestamp, weight := shared, 0.0, .001
		if i < 25 {
			leaf = untimed
		}
		if i >= 40 {
			timestamp, weight = 11+float64(i-40)*.004, .004
		}
		target.StackTraces = append(target.StackTraces, model.StackTrace{Timestamp: timestamp, StackFrames: []model.StackFrame{leaf, root}})
		target.Durations = append(target.Durations, weight)
	}
	rec.Samples = []model.TargetSamples{target}
	return rec
}

// DecimalEnd exercises a whole-recording end rounded down to nine decimals.
func DecimalEnd() *model.Recording {
	rec := UntimedSamples()
	rec.DisplayName = "Decimal recording end"
	start := 2.611872458306607
	shift := start - rec.StartTime
	rec.StartTime, rec.EndTime = start, 20.24081204162212
	for i := range rec.Records {
		rec.Records[i].StartTime += shift
		rec.Records[i].EndTime += shift
	}
	for i := range rec.Samples[0].StackTraces {
		if rec.Samples[0].StackTraces[i].Timestamp != 0 {
			rec.Samples[0].StackTraces[i].Timestamp += shift
		}
	}
	return rec
}
