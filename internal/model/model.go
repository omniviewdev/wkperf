// Package model decodes WebKit Web Inspector Timelines recording exports
// (WI.TimelineRecording.exportData()). All timestamps are seconds.
package model

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"
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

// Recording is the recording payload, either flat or inside a versioned export.
type Recording struct {
	DisplayName          string           `json:"displayName"`
	StartTime            float64          `json:"startTime"`
	EndTime              float64          `json:"endTime"`
	Discontinuities      []Discontinuity  `json:"discontinuities,omitempty"`
	InstrumentTypes      []string         `json:"instrumentTypes"`
	Records              []Record         `json:"records"`
	Markers              []Marker         `json:"markers"`
	MemoryPressureEvents []MemoryPressure `json:"memoryPressureEvents"`
	Samples              []TargetSamples  `json:"samples"`

	// Legacy (pre-`samples`) fields; Load folds them into Samples.
	SampleStackTraces []StackTrace `json:"sampleStackTraces,omitempty"`
	SampleDurations   []float64    `json:"sampleDurations,omitempty"`
}

// Discontinuity is a stopped/resumed capture interval, not observed page activity.
// WebKit's TimelineRecording.capturingStarted exports these absolute timestamps.
type Discontinuity struct {
	StartTime float64 `json:"startTime"`
	EndTime   float64 `json:"endTime"`
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

// StackTrace follows WebKit's actual CallingContextTree implementation:
// StackFrames[0] is the executing leaf; the last frame is the root caller.
// The ScriptProfiler protocol's historical prose describes the reverse order.
type StackTrace struct {
	Timestamp   float64      `json:"timestamp"`
	StackFrames []StackFrame `json:"stackFrames"`
}

type StackFrame struct {
	SourceID string          `json:"sourceID"`
	Name     string          `json:"name"`
	Line     int             `json:"line"`
	Column   int             `json:"column"`
	URL      string          `json:"url"`
	Original *SourceLocation `json:"original,omitempty"`
}

// SourceLocation stores a resolved source-map location without replacing the
// generated identity. Like ScriptProfiler frames, line and column are 1-based.
type SourceLocation struct {
	Name   string `json:"name,omitempty"`
	URL    string `json:"url"`
	Line   int    `json:"line"`
	Column int    `json:"column"`
}

// UnmarshalJSON accepts the upstream string sourceID and numeric IDs found in
// older or third-party exports, retaining their exact decimal representation.
func (f *StackFrame) UnmarshalJSON(data []byte) error {
	type plain StackFrame
	var v struct {
		*plain
		SourceID json.RawMessage `json:"sourceID"`
	}
	v.plain = (*plain)(f)
	if err := json.Unmarshal(data, &v); err != nil {
		return err
	}
	if len(v.SourceID) == 0 || string(v.SourceID) == "null" {
		return nil
	}
	if err := json.Unmarshal(v.SourceID, &f.SourceID); err == nil {
		return nil
	}
	var n json.Number
	if err := json.Unmarshal(v.SourceID, &n); err != nil {
		return fmt.Errorf("sourceID must be a string or number: %w", err)
	}
	f.SourceID = n.String()
	return nil
}

// Parse decodes a recording from r.
func Parse(r io.Reader) (*Recording, error) {
	dec := json.NewDecoder(r)
	var root json.RawMessage
	if err := dec.Decode(&root); err != nil {
		return nil, fmt.Errorf("decode recording: expected an object containing records or samples, or version/recording/overview: %w", err)
	}
	var extra json.RawMessage
	if err := dec.Decode(&extra); err != io.EOF {
		return nil, fmt.Errorf("decode recording: expected exactly one JSON object")
	}
	var envelope struct {
		Recording json.RawMessage `json:"recording"`
	}
	if err := json.Unmarshal(root, &envelope); err != nil {
		return nil, fmt.Errorf("decode recording: expected an object with records or samples, or version/recording/overview: %w", err)
	}
	payload := root
	if len(envelope.Recording) > 0 {
		payload = envelope.Recording
		if string(payload) == "null" {
			return nil, fmt.Errorf("invalid recording payload; actual top-level keys: %s; expected recording object with records or samples", objectKeys(root))
		}
	}
	var rec Recording
	if err := json.Unmarshal(payload, &rec); err != nil {
		return nil, fmt.Errorf("decode recording payload (actual keys: %s; expected records, samples, or sampleStackTraces): %w", objectKeys(payload), err)
	}
	if rec.Records == nil && rec.Samples == nil && rec.SampleStackTraces == nil {
		return nil, fmt.Errorf("not a WebKit timeline recording: actual keys: %s; expected records, samples, or sampleStackTraces (wrapped exports use version, recording, overview)", objectKeys(payload))
	}
	if len(rec.Samples) == 0 && len(rec.SampleStackTraces) > 0 {
		rec.Samples = []TargetSamples{{StackTraces: rec.SampleStackTraces, Durations: rec.SampleDurations}}
	}
	rec.SampleStackTraces, rec.SampleDurations = nil, nil
	return &rec, nil
}

func objectKeys(data []byte) string {
	var object map[string]json.RawMessage
	if err := json.Unmarshal(data, &object); err != nil {
		return "(not an object)"
	}
	keys := make([]string, 0, len(object))
	for key := range object {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return "[" + strings.Join(keys, ", ") + "]"
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
