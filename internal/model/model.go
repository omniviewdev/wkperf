// Package model decodes WebKit Web Inspector Timelines recording exports
// (WI.TimelineRecording.exportData()). All timestamps are seconds.
package model

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
)

// Record type identifiers as emitted in Record.Type.
const (
	TypeNetwork        = "timeline-record-type-network"
	TypeLayout         = "timeline-record-type-layout"
	TypeScript         = "timeline-record-type-script"
	TypeRenderingFrame = "timeline-record-type-rendering-frame"
	TypeCPU            = "timeline-record-type-cpu"
	TypeMemory         = "timeline-record-type-memory"
	TypeHeapAlloc      = "timeline-record-type-heap-allocations"
	TypeMedia          = "timeline-record-type-media"
	TypeScreenshots    = "timeline-record-type-screenshots"
)

// Recording is the top-level export object.
type Recording struct {
	DisplayName          string           `json:"displayName"`
	StartTime            float64          `json:"startTime"`
	EndTime              float64          `json:"endTime"`
	InstrumentTypes      []string         `json:"instrumentTypes"`
	Records              []Record         `json:"records"`
	Markers              []Marker         `json:"markers"`
	MemoryPressureEvents []MemoryPressure `json:"memoryPressureEvents"`
	Samples              []TargetSamples  `json:"samples"`

	// Legacy (pre-`samples`) fields; Load folds them into Samples.
	SampleStackTraces []StackTrace `json:"sampleStackTraces,omitempty"`
	SampleDurations   []float64    `json:"sampleDurations,omitempty"`
}

// Record is a union of every record kind; unused fields are zero.
type Record struct {
	Type      string  `json:"type"`
	EventType string  `json:"eventType,omitempty"` // script / layout records
	StartTime float64 `json:"startTime"`           // script, layout, rendering-frame
	EndTime   float64 `json:"endTime"`
	Timestamp float64 `json:"timestamp"` // cpu, memory

	Target  *Target         `json:"target,omitempty"`
	Details json.RawMessage `json:"details,omitempty"` // script: string or object

	DOMNodeDisplayName string `json:"domNodeDisplayName,omitempty"` // layout
	DOMNodeCSSPath     string `json:"domNodeCSSPath,omitempty"`

	Usage      float64          `json:"usage"` // cpu, percent
	Threads    []Thread         `json:"threads,omitempty"`
	Categories []MemoryCategory `json:"categories,omitempty"` // memory

	// network: HAR entry as produced by WI.HARBuilder.entry
	Entry json.RawMessage `json:"entry,omitempty"`
}

// Duration returns EndTime-StartTime in seconds, or 0 when not a span.
func (r Record) Duration() float64 {
	if r.EndTime > r.StartTime {
		return r.EndTime - r.StartTime
	}
	return 0
}

type Target struct {
	Identifier string `json:"identifier"`
	Type       string `json:"type"`
	Name       string `json:"name"`
	URL        string `json:"url"`
}

type Thread struct {
	Name     string  `json:"name"`
	Usage    float64 `json:"usage"`
	Type     string  `json:"type,omitempty"`
	TargetID string  `json:"targetId,omitempty"`
}

type MemoryCategory struct {
	Type string  `json:"type"`
	Size float64 `json:"size"` // bytes
}

type Marker struct {
	Time    float64         `json:"time"`
	Type    string          `json:"type"`
	Details json.RawMessage `json:"details,omitempty"`
}

type MemoryPressure struct {
	Timestamp float64 `json:"timestamp"`
	Severity  string  `json:"severity"`
}

// TargetSamples holds sampling-profiler data for one target.
// Durations[i] (seconds) is the weight of StackTraces[i].
type TargetSamples struct {
	Target      Target       `json:"target"`
	StackTraces []StackTrace `json:"stackTraces"`
	Durations   []float64    `json:"durations"`
}

// StackTrace: StackFrames[0] is the BOTTOM of the stack, last is the TOP
// (the currently executing function => self time).
type StackTrace struct {
	Timestamp   float64      `json:"timestamp"`
	StackFrames []StackFrame `json:"stackFrames"`
}

type StackFrame struct {
	SourceID string `json:"sourceID"`
	Name     string `json:"name"`
	Line     int    `json:"line"`
	Column   int    `json:"column"`
	URL      string `json:"url"`
}

// Parse decodes a recording from r.
func Parse(r io.Reader) (*Recording, error) {
	var rec Recording
	if err := json.NewDecoder(r).Decode(&rec); err != nil {
		return nil, fmt.Errorf("decode recording: %w", err)
	}
	if rec.Records == nil && rec.Samples == nil && rec.SampleStackTraces == nil {
		return nil, fmt.Errorf("not a WebKit timeline recording: no records or samples")
	}
	if len(rec.Samples) == 0 && len(rec.SampleStackTraces) > 0 {
		rec.Samples = []TargetSamples{{StackTraces: rec.SampleStackTraces, Durations: rec.SampleDurations}}
	}
	rec.SampleStackTraces, rec.SampleDurations = nil, nil
	return &rec, nil
}

// Load reads and decodes the recording at path.
func Load(path string) (*Recording, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	rec, err := Parse(f)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	return rec, nil
}
