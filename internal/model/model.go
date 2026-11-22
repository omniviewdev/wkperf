// Package model decodes WebKit Web Inspector Timelines recording exports
// (WI.TimelineRecording.exportData()). All timestamps are seconds.
package model

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"
	"unicode/utf8"

	"github.com/omniviewdev/wkperf/internal/diagnostic"
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

	// CaptureCapabilities reports optional instrument status for wkperf captures.
	CaptureCapabilities map[string]string `json:"captureCapabilities,omitzero"`

	// Partial clips retain untimed evidence but exclude its weight.
	UntimedExcluded bool            `json:"-"`
	SampleEvidence  *SampleEvidence `json:"-"`

	// SourceHadRenderingFrames survives window clipping, which can drop every
	// rendering-frame record from Records. Read it through HasRenderingFrames.
	SourceHadRenderingFrames bool `json:"-"`

	// Legacy (pre-`samples`) fields; Load folds them into Samples.
	SampleStackTraces []StackTrace `json:"sampleStackTraces,omitempty"`
	SampleDurations   []float64    `json:"sampleDurations,omitempty"`
}

// SampleEvidence preserves original export order for inference-only span labels.
// Clipped recordings share these read-only samples with their source recording.
type SampleEvidence struct {
	StartTime float64
	EndTime   float64
	Samples   []TargetSamples
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

	// Original bounds retain record ownership when an analysis window clips a
	// positive-duration span. A positive original span indicates provenance.
	OriginalStartTime float64 `json:"-"`
	OriginalEndTime   float64 `json:"-"`

	Target  *Target         `json:"target,omitempty"`
	Details json.RawMessage `json:"details,omitempty"` // script: string or object
	Quad    json.RawMessage `json:"quad,omitempty"`    // layout: four x/y pairs

	DOMNodeDisplayName string `json:"domNodeDisplayName,omitempty"` // layout
	DOMNodeCSSPath     string `json:"domNodeCSSPath,omitempty"`

	Usage      float64          `json:"usage"` // cpu, percent
	Threads    []Thread         `json:"threads,omitempty"`
	Categories []MemoryCategory `json:"categories,omitempty"` // memory

	// Network: HAR entry and recording-clock origin as exported by WebKit.
	Entry            json.RawMessage `json:"entry,omitempty"`
	ArchiveStartTime float64         `json:"archiveStartTime,omitempty"`
}

// Duration returns EndTime-StartTime in seconds, or 0 when not a span.
func (r Record) Duration() float64 {
	if r.EndTime > r.StartTime {
		return r.EndTime - r.StartTime
	}
	return 0
}

// HasRenderingFrames reports whether the capture holds rendering-frame records,
// including any a window clip removed from Records.
func (r *Recording) HasRenderingFrames() bool {
	if r == nil {
		return false
	}
	if r.SourceHadRenderingFrames {
		return true
	}
	for i := range r.Records {
		if r.Records[i].Type == TypeRenderingFrame {
			return true
		}
	}
	return false
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
		SourceID frameSourceID `json:"sourceID"`
	}
	v.plain = (*plain)(f)
	if err := json.Unmarshal(data, &v); err != nil {
		return err
	}
	if v.SourceID.err != nil {
		return v.SourceID.err
	}
	if v.SourceID.present {
		f.SourceID = v.SourceID.value
	}
	return nil
}

// Keep each result until the enclosing frame is decoded: the last sourceID
// wins even if an earlier duplicate had the wrong type.
type frameSourceID struct {
	value   string
	present bool
	err     error
}

func (id *frameSourceID) UnmarshalJSON(data []byte) error {
	*id = frameSourceID{}
	switch data[0] {
	case 'n':
		return nil
	case '"':
		value := data[1 : len(data)-1]
		if bytes.IndexByte(value, '\\') < 0 && utf8.Valid(value) {
			id.value = string(value)
		} else {
			id.err = json.Unmarshal(data, &id.value)
		}
	case '-', '0', '1', '2', '3', '4', '5', '6', '7', '8', '9':
		id.value = string(data)
	default:
		var n json.Number
		err := json.Unmarshal(data, &n)
		id.err = fmt.Errorf("sourceID must be a string or number: %w", err)
	}
	id.present = true
	return nil
}

const (
	recordingDecodeContext = "decode recording: expected an object containing records or samples, or version/recording/overview"
	recordingTrailingError = "decode recording: expected exactly one JSON object"
)

// Parse decodes a recording from r.
func Parse(r io.Reader) (*Recording, error) {
	dec := json.NewDecoder(r)
	// Reject a malformed first value without waiting for EOF. Validate trailing
	// input before the typed decode, even when the payload is large.
	var root json.RawMessage
	if err := dec.Decode(&root); err != nil {
		return nil, fmt.Errorf("%s: %w", recordingDecodeContext, err)
	}
	var extra struct{}
	if err := dec.Decode(&extra); err != io.EOF {
		return nil, errors.New(recordingTrailingError)
	}
	return parseRecordingExport(root)
}

// The file reader reserves capacity for its EOF probe. Use that space for a
// sentinel so syntax errors on EOF have an offset past the original input.
func parseRecordingFile(data []byte) (*Recording, error) {
	var export recordingExport
	if err := json.Unmarshal(append(data, ' '), &export); err != nil {
		if syntax, ok := err.(*json.SyntaxError); ok {
			switch {
			case syntax.Offset > int64(len(data)):
				err = io.ErrUnexpectedEOF
				if len(bytes.Trim(data, " \t\r\n")) == 0 {
					err = io.EOF
				}
			case syntax.Offset > 0 && json.Valid(data[:syntax.Offset-1]):
				return nil, errors.New(recordingTrailingError)
			}
		}
		return nil, fmt.Errorf("%s: %w", recordingDecodeContext, err)
	}
	return export.recording, export.err
}

// Unmarshal validates the entire file before calling UnmarshalJSON. Borrow
// the buffer only during the call; neither root nor wrapped payload is copied.
type recordingExport struct {
	recording *Recording
	err       error
}

func (e *recordingExport) UnmarshalJSON(root []byte) error {
	e.recording, e.err = parseRecordingExport(root)
	// Only errors from validating the entire file need stream normalization.
	return nil
}

func parseRecordingExport(root []byte) (*Recording, error) {
	payload := root
	switch root[0] {
	case '{':
		jsonObjectFields(root, func(key string, value []byte) {
			if strings.EqualFold(key, "recording") {
				payload = value
			}
		})
		if bytes.Equal(payload, []byte("null")) {
			return nil, fmt.Errorf("invalid recording payload; actual top-level keys: %s; expected recording object with records or samples", objectKeys(root))
		}
	case 'n':
		// A top-level null has no fields, like an empty object.
	default:
		var envelope struct {
			Recording json.RawMessage `json:"recording"`
		}
		err := json.Unmarshal(root, &envelope)
		return nil, fmt.Errorf("decode recording: expected an object with records or samples, or version/recording/overview: %w", err)
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
	if bytes.Equal(data, []byte("null")) {
		return "[]"
	}
	if data[0] != '{' {
		return "(not an object)"
	}
	unique := make(map[string]struct{})
	jsonObjectFields(data, func(key string, _ []byte) { unique[key] = struct{}{} })
	keys := make([]string, 0, len(unique))
	for key := range unique {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return "[" + strings.Join(keys, ", ") + "]"
}

// jsonObjectFields walks only the members of an already validated JSON object.
// Values are slices of the input, not copies. The scanner only tracks nesting
// and strings; encoding/json remains responsible for syntax and type checks.
func jsonObjectFields(data []byte, visit func(string, []byte)) {
	i := 1
	for {
		i = skipJSONSpace(data, i)
		if data[i] == '}' {
			return
		}
		end := jsonStringEnd(data, i)
		rawKey := data[i+1 : end-1]
		key := string(rawKey)
		if bytes.IndexByte(rawKey, '\\') >= 0 || !utf8.Valid(rawKey) {
			_ = json.Unmarshal(data[i:end], &key)
		}
		start := skipJSONSpace(data, skipJSONSpace(data, end)+1)
		i = start
		depth := 0
	value:
		for {
			switch data[i] {
			case '"':
				i = jsonStringEnd(data, i)
				continue
			case '{', '[':
				depth++
			case '}', ']', ',':
				if depth == 0 {
					break value
				}
				if data[i] != ',' {
					depth--
				}
			}
			i++
		}
		visit(key, bytes.TrimRight(data[start:i], " \t\r\n"))
		if data[i] == '}' {
			return
		}
		i++
	}
}

func skipJSONSpace(data []byte, i int) int {
	for {
		switch data[i] {
		case ' ', '\t', '\r', '\n':
			i++
		default:
			return i
		}
	}
}

func jsonStringEnd(data []byte, start int) int {
	for i := start + 1; ; i++ {
		switch data[i] {
		case '\\':
			i++
		case '"':
			return i + 1
		}
	}
}

// Load reads and decodes the regular recording file at path.
func Load(path string) (*Recording, error) {
	info, err := os.Stat(path)
	if err != nil {
		return nil, diagnostic.Wrap(diagnostic.LoadError, path, err)
	}
	if !info.Mode().IsRegular() {
		return nil, diagnostic.Wrap(diagnostic.InputError, path, fmt.Errorf("not a regular file"))
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, diagnostic.Wrap(diagnostic.LoadError, path, err)
	}
	defer f.Close()
	rec, err := loadRecordingFile(f, info.Size())
	if err != nil {
		return nil, diagnostic.Wrap(diagnostic.LoadError, path, err)
	}
	return rec, nil
}

// Bound allocations based on file size alone. Larger files use Parse so an
// invalid prefix in a large or sparse file fails before reading the rest.
const maxBufferedRecordingSize = 128 << 20

func loadRecordingFile(f *os.File, size int64) (*Recording, error) {
	if size > maxBufferedRecordingSize {
		return Parse(f)
	}
	// The extra byte probes for growth and makes room for the JSON sentinel
	// without a second allocation. Parse any bytes read if the file shrank.
	data := make([]byte, int(size)+1)
	n, err := io.ReadFull(f, data)
	switch err {
	case io.EOF, io.ErrUnexpectedEOF:
		return parseRecordingFile(data[:n])
	case nil:
		return Parse(io.MultiReader(bytes.NewReader(data), f))
	default:
		return nil, fmt.Errorf("%s: %w", recordingDecodeContext, err)
	}
}
