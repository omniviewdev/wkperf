// Package testgen builds deterministic synthetic WebKit timeline recordings
// for tests and for the `wkperf sample` command.
package testgen

import (
	"encoding/json"
	"fmt"
	"math/rand"

	"github.com/omniviewdev/wkperf/internal/model"
)

// FuncWeight describes one function in the sampling profile. Weight is the
// relative share of profiler samples whose top frame is this function.
type FuncWeight struct {
	Name   string
	URL    string
	Line   int
	Weight float64
}

// Params control the shape of the generated recording.
type Params struct {
	DurationSec          float64
	FrameMeanMs          float64
	FrameJitterMs        float64 // standard deviation of frame duration
	LayoutScale          float64 // multiplies layout/paint/style durations
	ScriptScale          float64 // multiplies script event durations
	CPUMean              float64 // mean total CPU usage, percent
	MemoryStartMB        float64
	MemoryGrowthMBPerSec float64
	Functions            []FuncWeight
}

// sampleIntervalSec is the spacing (and weight) of profiler samples.
const sampleIntervalSec = 0.01

// Baseline returns parameters for a typical, slightly janky page.
func Baseline() Params {
	return Params{
		DurationSec:          10,
		FrameMeanMs:          18,
		FrameJitterMs:        4,
		LayoutScale:          1,
		ScriptScale:          1,
		CPUMean:              45,
		MemoryStartMB:        120,
		MemoryGrowthMBPerSec: 2,
		Functions: []FuncWeight{
			{Name: "renderList", URL: "https://app.test/main.js", Line: 120, Weight: 40},
			{Name: "computeLayout", URL: "https://app.test/layout.js", Line: 33, Weight: 25},
			{Name: "parseJSON", URL: "https://app.test/data.js", Line: 8, Weight: 15},
			{Name: "handleScroll", URL: "https://app.test/main.js", Line: 210, Weight: 12},
			{Name: "idleTick", URL: "https://app.test/util.js", Line: 5, Weight: 8},
		},
	}
}

// Improved returns parameters for a faster build of the Baseline scenario.
func Improved() Params {
	p := Baseline()
	p.FrameMeanMs = 14
	p.FrameJitterMs = 2
	p.LayoutScale = 0.7
	p.ScriptScale = 0.75
	p.CPUMean = 35
	p.MemoryGrowthMBPerSec = 1
	p.Functions = []FuncWeight{
		{Name: "renderList", URL: "https://app.test/main.js", Line: 120, Weight: 20},
		{Name: "computeLayout", URL: "https://app.test/layout.js", Line: 33, Weight: 20},
		{Name: "parseJSON", URL: "https://app.test/data.js", Line: 8, Weight: 15},
		{Name: "handleScroll", URL: "https://app.test/main.js", Line: 210, Weight: 12},
		{Name: "idleTick", URL: "https://app.test/util.js", Line: 5, Weight: 8},
	}
	return p
}

// Generate builds a recording from p. The same seed and params always yield
// the same recording.
func Generate(seed int64, p Params) *model.Recording {
	rng := rand.New(rand.NewSource(seed))
	rec := &model.Recording{
		DisplayName:     fmt.Sprintf("Synthetic Recording %d", seed),
		StartTime:       0,
		EndTime:         p.DurationSec,
		InstrumentTypes: []string{"timeline-record-type-script", "timeline-record-type-layout", "timeline-record-type-rendering-frame"},
		Records:         []model.Record{},
		Markers:         []model.Marker{},
	}

	genFrames(rng, p, rec)
	genTimeline(rng, p, rec, 0.5)
	genNetwork(rng, p, rec)
	rec.Samples = []model.TargetSamples{genSamples(rng, p)}
	return rec
}

func genFrames(rng *rand.Rand, p Params, rec *model.Recording) {
	layoutEvents := []struct {
		name string
		ms   float64
	}{
		{"recalculate-styles", 0.8},
		{"layout", 1.5},
		{"paint", 1.2},
		{"composite", 0.4},
	}
	scriptEvents := []struct {
		name string
		ms   float64
	}{
		{"event-dispatched", 1.2},
		{"timer-fired", 0.9},
		{"animation-frame-fired", 2.0},
	}

	t := 0.0
	for t < p.DurationSec {
		ms := p.FrameMeanMs + rng.NormFloat64()*p.FrameJitterMs
		if ms < 1 {
			ms = 1
		}
		end := t + ms/1000
		rec.Records = append(rec.Records, model.Record{
			Type: model.TypeRenderingFrame, StartTime: t, EndTime: end,
		})
		t = end
	}

	// Script and layout work is emitted on a fixed tick, independent of the
	// frame rate, so event counts stay comparable between parameter sets.
	const tick = 0.016
	for t = 0; t < p.DurationSec; t += tick {
		cursor := t
		for _, e := range scriptEvents {
			d := e.ms * p.ScriptScale * (0.5 + rng.Float64()) / 1000
			rec.Records = append(rec.Records, model.Record{
				Type: model.TypeScript, EventType: e.name, StartTime: cursor, EndTime: cursor + d,
			})
			cursor += d
		}
		for _, e := range layoutEvents {
			d := e.ms * p.LayoutScale * (0.5 + rng.Float64()) / 1000
			rec.Records = append(rec.Records, model.Record{
				Type: model.TypeLayout, EventType: e.name, StartTime: cursor, EndTime: cursor + d,
			})
			cursor += d
		}
		if rng.Float64() < 0.03 {
			d := (2 + rng.Float64()*3) * p.ScriptScale / 1000
			rec.Records = append(rec.Records, model.Record{
				Type: model.TypeScript, EventType: "garbage-collected",
				StartTime: cursor, EndTime: cursor + d,
				Details: json.RawMessage(`{"__type":"GarbageCollection","type":"partial"}`),
			})
		}
	}
}

// genTimeline emits CPU and memory records every step seconds.
func genTimeline(rng *rand.Rand, p Params, rec *model.Recording, step float64) {
	for ts := step; ts <= p.DurationSec; ts += step {
		usage := clamp(p.CPUMean+rng.NormFloat64()*5, 1, 100)
		main := clamp(usage*0.7, 0, 100)
		rec.Records = append(rec.Records, model.Record{
			Type: model.TypeCPU, Timestamp: ts, Usage: usage,
			Threads: []model.Thread{
				{Name: "Main Thread", Usage: main, Type: "main"},
				{Name: "WebKit Worker", Usage: usage - main, Type: "webkit"},
			},
		})

		total := (p.MemoryStartMB + p.MemoryGrowthMBPerSec*ts + rng.NormFloat64()*0.5) * 1024 * 1024
		if total < 0 {
			total = 0
		}
		rec.Records = append(rec.Records, model.Record{
			Type: model.TypeMemory, Timestamp: ts,
			Categories: []model.MemoryCategory{
				{Type: "javascript", Size: total * 0.5},
				{Type: "images", Size: total * 0.3},
				{Type: "layers", Size: total * 0.2},
			},
		})
	}
}

func genNetwork(rng *rand.Rand, p Params, rec *model.Recording) {
	n := int(p.DurationSec / 2)
	for i := 0; i < n; i++ {
		start := float64(i) * 2
		body := 2000 + rng.Intn(50000)
		entry := fmt.Sprintf(`{"time":%.2f,"request":{"url":"https://app.test/api/item/%d"},"response":{"bodySize":%d,"_transferSize":%d}}`,
			20+rng.Float64()*80, i, body, body+300)
		rec.Records = append(rec.Records, model.Record{
			Type: model.TypeNetwork, StartTime: start, EndTime: start + 0.05,
			Entry: json.RawMessage(entry),
		})
	}
}

func genSamples(rng *rand.Rand, p Params) model.TargetSamples {
	ts := model.TargetSamples{
		Target:      model.Target{Identifier: "page", Type: "page", Name: "Synthetic Page", URL: "https://app.test/"},
		StackTraces: []model.StackTrace{},
		Durations:   []float64{},
	}
	var total float64
	for _, f := range p.Functions {
		total += f.Weight
	}
	if total <= 0 {
		return ts
	}
	root := model.StackFrame{Name: "(program)", URL: "https://app.test/main.js", Line: 1}
	n := int(p.DurationSec / sampleIntervalSec)
	for i := 0; i < n; i++ {
		x := rng.Float64() * total
		fn := p.Functions[len(p.Functions)-1]
		for _, f := range p.Functions {
			if x < f.Weight {
				fn = f
				break
			}
			x -= f.Weight
		}
		ts.StackTraces = append(ts.StackTraces, model.StackTrace{
			Timestamp: float64(i) * sampleIntervalSec,
			StackFrames: []model.StackFrame{
				root,
				{Name: fn.Name, URL: fn.URL, Line: fn.Line},
			},
		})
		ts.Durations = append(ts.Durations, sampleIntervalSec)
	}
	return ts
}

func clamp(v, lo, hi float64) float64 {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}
