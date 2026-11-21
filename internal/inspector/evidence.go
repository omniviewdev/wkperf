package inspector

import (
	"cmp"
	"encoding/json"
	"fmt"
	"slices"
	"sort"
	"strings"

	"github.com/omniviewdev/wkperf/internal/model"
	"github.com/omniviewdev/wkperf/internal/profile"
)

type evidence struct {
	timelineStart, timelineEnd *float64
	scriptStart, scriptEnd     *float64
	records                    []model.Record
	script                     []model.Record
	stacks                     []model.StackTrace
	resources                  [2]resourceTracker
	resourceRecords            []model.Record
	markers                    []model.Marker
	pressure                   []model.MemoryPressure
}

type timelineEvent struct {
	Type       string                     `json:"type"`
	Start      *float64                   `json:"startTime"`
	End        *float64                   `json:"endTime"`
	Data       map[string]json.RawMessage `json:"data"`
	StackTrace json.RawMessage            `json:"stackTrace"`
	Children   []timelineEvent            `json:"children"`
}

type scriptSamples []model.StackTrace

// WebKit can omit samples, but a present buffer must contain a stack array.
func (s *scriptSamples) UnmarshalJSON(data []byte) error {
	var p struct {
		StackTraces []model.StackTrace `json:"stackTraces"`
	}
	if err := json.Unmarshal(data, &p); err != nil {
		return err
	}
	if p.StackTraces == nil {
		return fmt.Errorf("ScriptProfiler samples must contain a stackTraces array")
	}
	for i := range p.StackTraces {
		trace := &p.StackTraces[i]
		if trace.StackFrames == nil || trace.Timestamp < 0 {
			return fmt.Errorf("invalid ScriptProfiler stack trace at index %d", i)
		}
	}
	*s = p.StackTraces
	return nil
}

func (e *evidence) event(m message) error {
	switch m.Method {
	case "Timeline.recordingStarted", "Timeline.recordingStopped", "ScriptProfiler.trackingStart", "ScriptProfiler.trackingComplete":
		var p struct {
			Start     *float64      `json:"startTime"`
			End       *float64      `json:"endTime"`
			Timestamp *float64      `json:"timestamp"`
			Samples   scriptSamples `json:"samples"`
		}
		if err := json.Unmarshal(m.Params, &p); err != nil {
			return fmt.Errorf("decode %s: %w", m.Method, err)
		}
		var dest **float64
		var value *float64
		switch m.Method {
		case "Timeline.recordingStarted":
			dest, value = &e.timelineStart, p.Start
		case "Timeline.recordingStopped":
			dest, value = &e.timelineEnd, p.End
		case "ScriptProfiler.trackingStart":
			dest, value = &e.scriptStart, p.Timestamp
		case "ScriptProfiler.trackingComplete":
			dest, value = &e.scriptEnd, p.Timestamp
			e.stacks = p.Samples
		}
		if value == nil || *value < 0 || *dest != nil {
			return fmt.Errorf("unexpected %s timestamp", m.Method)
		}
		*dest = value
	case "Timeline.eventRecorded":
		var p struct {
			Record timelineEvent `json:"record"`
		}
		if err := json.Unmarshal(m.Params, &p); err != nil {
			return fmt.Errorf("decode Timeline.eventRecorded: %w", err)
		}
		e.addTimeline(p.Record, nil)
	case "ScriptProfiler.trackingUpdate":
		var p struct {
			Event struct {
				Start float64 `json:"startTime"`
				End   float64 `json:"endTime"`
				Type  string  `json:"type"`
			} `json:"event"`
		}
		if err := json.Unmarshal(m.Params, &p); err != nil {
			return fmt.Errorf("decode ScriptProfiler.trackingUpdate: %w", err)
		}
		typ := "script-evaluated"
		switch p.Event.Type {
		case "API":
			typ = "api-script-evaluated"
		case "Microtask":
			typ = "microtask-dispatched"
		}
		if p.Event.End > p.Event.Start {
			e.script = append(e.script, model.Record{Type: model.TypeScript, EventType: typ, StartTime: p.Event.Start, EndTime: p.Event.End})
		}
	default:
		return e.resourceEvent(m)
	}
	return nil
}

var layoutEvents = map[string]string{
	"ScheduleStyleRecalculation": "invalidate-styles",
	"RecalculateStyles":          "recalculate-styles",
	"InvalidateLayout":           "invalidate-layout",
	"ScheduleLayout":             "schedule-layout",
	"Layout":                     "layout",
	"Paint":                      "paint",
	"Composite":                  "composite",
	"FirstContentfulPaint":       "first-contentful-paint",
	"LargestContentfulPaint":     "largest-contentful-paint",
}

// Timeline used a bare frame array before macOS 13 and iOS 16.
func hasLayoutSource(raw json.RawMessage) bool {
	type frame struct {
		URL      string `json:"url"`
		ScriptID string `json:"scriptId"`
	}
	var stack struct {
		CallFrames []frame `json:"callFrames"`
	}
	if len(raw) == 0 {
		return false
	}
	if raw[0] == '[' {
		if json.Unmarshal(raw, &stack.CallFrames) != nil {
			return false
		}
	} else if json.Unmarshal(raw, &stack) != nil {
		return false
	}
	for _, f := range stack.CallFrames {
		if f.URL == "[native code]" || f.URL == "" && f.ScriptID == "0" {
			continue
		}
		// Without the frontend's script registry, use the frame's source identity.
		return f.URL != "" || f.ScriptID != ""
	}
	return false
}

func (e *evidence) addTimeline(ev timelineEvent, parent *timelineEvent) {
	if ev.Type == "TimeStamp" && ev.Start != nil {
		raw := ev.Data["message"]
		var label string
		if json.Unmarshal(raw, &label) == nil && strings.HasPrefix(label, "wkperf.clock.") && label != "wkperf.clock." {
			e.markers = append(e.markers, model.Marker{Time: *ev.Start, Type: "timestamp", Details: raw})
		}
	}
	var record model.Record
	var detailsKey string
	if kind, ok := layoutEvents[ev.Type]; ok {
		if ev.Type == "Layout" && hasLayoutSource(ev.StackTrace) {
			kind = "forced-layout"
		}
		record.Type, record.EventType = model.TypeLayout, kind
	} else {
		record.Type = model.TypeScript
		switch ev.Type {
		case "RenderingFrame":
			record.Type = model.TypeRenderingFrame
		case "FunctionCall", "EvaluateScript":
			record.EventType = "script-evaluated"
			if parent != nil {
				switch parent.Type {
				case "EventDispatch":
					if ev.Type == "FunctionCall" {
						record.EventType, record.Details = "event-dispatched", parent.Data["type"]
					}
				case "TimerFire":
					record.EventType, record.Details = "timer-fired", parent.Data["timerId"]
				case "FireAnimationFrame":
					record.EventType, record.Details = "animation-frame-fired", parent.Data["id"]
				case "ObserverCallback":
					record.EventType, record.Details = "observer-callback", parent.Data["type"]
				}
			}
		case "TimerInstall":
			record.EventType, detailsKey = "timer-installed", "timerId"
		case "TimerRemove":
			record.EventType, detailsKey = "timer-removed", "timerId"
		case "RequestAnimationFrame":
			record.EventType, detailsKey = "animation-frame-requested", "id"
		case "CancelAnimationFrame":
			record.EventType, detailsKey = "animation-frame-canceled", "id"
		case "ConsoleProfile":
			record.Type = ""
			if ev.Start != nil {
				// A console profile measures a user-defined interval, not continuous work.
				details, _ := json.Marshal(struct {
					Title json.RawMessage `json:"title,omitempty"`
					End   *float64        `json:"endTime,omitempty"`
				}{Title: ev.Data["title"], End: ev.End})
				e.markers = append(e.markers, model.Marker{Time: *ev.Start, Type: "console-profile-recorded", Details: details})
			}
		default:
			record.Type = ""
		}
	}
	if record.Type != "" && ev.Start != nil {
		record.StartTime, record.EndTime = *ev.Start, *ev.Start
		if ev.End != nil {
			record.EndTime = max(*ev.End, *ev.Start)
		}
		if detailsKey != "" {
			record.Details = ev.Data[detailsKey]
		}
		e.records = append(e.records, record)
	}
	for _, child := range ev.Children {
		e.addTimeline(child, &ev)
	}
}

func (e *evidence) recording(targetID string) (*model.Recording, error) {
	if *e.scriptEnd < *e.scriptStart || *e.timelineEnd < *e.timelineStart {
		return nil, fmt.Errorf("WebKit returned inverted recording bounds")
	}
	if *e.scriptStart > *e.timelineStart || *e.timelineEnd > *e.scriptEnd {
		return nil, fmt.Errorf("WebKit Timeline bounds fall outside ScriptProfiler bounds")
	}
	start, end := *e.scriptStart, *e.scriptEnd
	if end <= start {
		return nil, fmt.Errorf("WebKit returned an empty recording interval; the target must expose an advancing inspector clock")
	}
	if targetID == "" {
		targetID = "page"
	}
	target := model.Target{Identifier: targetID, Type: "page", Name: "WebKit page"}
	if e.records == nil {
		e.records = []model.Record{}
	}
	sortRecords(e.records)
	sortRecords(e.script)
	withProfiler := e.records
	var i int
	var lastTimelineScriptEnd float64
	for _, rec := range e.script {
		for i < len(e.records) && e.records[i].StartTime <= rec.StartTime {
			if e.records[i].Type == model.TypeScript && e.records[i].Duration() > 0 {
				lastTimelineScriptEnd = max(lastTimelineScriptEnd, e.records[i].EndTime)
			}
			i++
		}
		// The Timeline record already describes the containing Other event.
		if rec.EventType != "script-evaluated" || rec.EndTime > lastTimelineScriptEnd {
			withProfiler = append(withProfiler, rec)
		}
	}
	instruments := []string{model.TypeScript, model.TypeLayout, model.TypeRenderingFrame}
	for i := range e.resources {
		if e.resources[i].enabled {
			instruments = append(instruments, e.resources[i].kind)
		}
	}
	for _, rec := range e.resourceRecords {
		if rec.Timestamp >= start && rec.Timestamp < end {
			withProfiler = append(withProfiler, rec)
			for i := range e.resources {
				if rec.Type == e.resources[i].kind {
					e.resources[i].samples++
					break
				}
			}
		}
	}
	pressure := []model.MemoryPressure{}
	for _, event := range e.pressure {
		if event.Timestamp >= start && event.Timestamp < end {
			pressure = append(pressure, event)
		}
	}
	sortRecords(withProfiler)
	for i := range withProfiler {
		if withProfiler[i].Type == model.TypeScript {
			withProfiler[i].Target = &target
		}
	}
	markers := []model.Marker{}
	for _, marker := range e.markers {
		if marker.Time >= start && marker.Time <= end {
			markers = append(markers, marker)
		}
	}
	sort.SliceStable(markers, func(i, j int) bool { return markers[i].Time < markers[j].Time })
	recording := &model.Recording{
		DisplayName:          "wkperf WebKit recording",
		StartTime:            start,
		EndTime:              end,
		InstrumentTypes:      instruments,
		Records:              withProfiler,
		Markers:              markers,
		MemoryPressureEvents: pressure,
		Samples:              []model.TargetSamples{},
	}
	if e.stacks != nil {
		recording.Samples = []model.TargetSamples{{
			Target:      target,
			StackTraces: e.stacks,
			Durations:   sampleDurations(recording, e.stacks, e.script),
		}}
	}
	return recording, nil
}

func sortRecords(records []model.Record) {
	sort.SliceStable(records, func(i, j int) bool {
		return recordTime(&records[i]) < recordTime(&records[j])
	})
}

func recordTime(record *model.Record) float64 {
	if record.Type == model.TypeCPU || record.Type == model.TypeMemory {
		return record.Timestamp
	}
	return record.StartTime
}

func (e *evidence) resourceCapabilities() map[string]string {
	capabilities := make(map[string]string, len(e.resources))
	for _, r := range e.resources {
		capabilities[r.domain] = r.status()
	}
	return capabilities
}

// Match the Web Inspector frontend: average an evaluation interval over its
// samples, and assign 1 ms to samples outside intervals (including untimed ones).
func sampleDurations(recording *model.Recording, stacks []model.StackTrace, records []model.Record) []float64 {
	durations := make([]float64, len(stacks))
	for i := range durations {
		durations[i] = 0.001
	}
	if len(records) == 0 {
		return durations
	}
	// Sort only timed sample indices. The exported neighbors must stay intact
	// for consumers that infer spans for untimed samples.
	timed := make([]int, 0, len(stacks))
	for i := range stacks {
		if !profile.UntimedTimestamp(recording, stacks[i].Timestamp) {
			timed = append(timed, i)
		}
	}
	slices.SortFunc(timed, func(i, j int) int {
		return cmp.Compare(stacks[i].Timestamp, stacks[j].Timestamp)
	})
	var i int
	for _, rec := range records {
		for i < len(timed) && stacks[timed[i]].Timestamp < rec.StartTime {
			i++
		}
		first := i
		for i < len(timed) && stacks[timed[i]].Timestamp < rec.EndTime {
			i++
		}
		if first != i {
			weight := rec.Duration() / float64(i-first)
			for j := first; j < i; j++ {
				durations[timed[j]] = weight
			}
		}
	}
	return durations
}
