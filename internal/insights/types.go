// Package insights turns ONE recording into a performance profile: where wall
// time goes, hotspots, slow-frame attribution and heuristic findings with
// optimization hints. Contract between the analyzer, AI and CLI packages.
package insights

import "github.com/omniviewdev/wkperf/internal/metrics"

type Severity string

const (
	SevHigh   Severity = "high"
	SevMedium Severity = "medium"
	SevLow    Severity = "low"
	SevInfo   Severity = "info"
)

// Slice is one part of the wall-time breakdown. Slices are exclusive:
// Script, Style, Layout, Paint, Composite, GC, Other (non-frame work), Idle.
type Slice struct {
	Name string  `json:"name"`
	Ms   float64 `json:"ms"`
	Pct  float64 `json:"pct"` // of recording wall time
}

// ModuleStat groups function self-time by module: npm package name parsed
// from ".../node_modules/<pkg>/", else the URL path directory, else "(native)".
type ModuleStat struct {
	Name   string  `json:"name"`
	SelfMs float64 `json:"selfMs"`
	Pct    float64 `json:"pct"` // of total sampled self time
}

// SlowFrame is a rendering frame longer than the jank threshold, with what
// overlapped it.
type SlowFrame struct {
	T        float64 `json:"t"`  // seconds from recording start
	Ms       float64 `json:"ms"` // frame duration
	ScriptMs float64 `json:"scriptMs"`
	LayoutMs float64 `json:"layoutMs"` // layout+style
	PaintMs  float64 `json:"paintMs"`  // paint+composite
	GCMs     float64 `json:"gcMs"`
	TopEvent string  `json:"topEvent"`          // dominant script eventType/details, e.g. "event-dispatched click"
	TopFunc  string  `json:"topFunc,omitempty"` // hottest sampled function in the frame, if samples cover it
}

// LongTask is a single script/event record longer than the long-task threshold (50ms).
type LongTask struct {
	T         float64 `json:"t"`
	Ms        float64 `json:"ms"`
	EventType string  `json:"eventType"`
	Detail    string  `json:"detail,omitempty"` // e.g. event name, timer id, URL
	TopFunc   string  `json:"topFunc,omitempty"`
}

// Finding is one heuristic conclusion.
type Finding struct {
	ID         string   `json:"id"` // stable, e.g. "forced-layout", "long-tasks", "gc-pressure", "memory-growth"
	Title      string   `json:"title"`
	Severity   Severity `json:"severity"`
	Category   string   `json:"category"` // script|layout|paint|gc|memory|cpu|network|frames
	ImpactMs   float64  `json:"impactMs"` // estimated wall time attributable
	ImpactPct  float64  `json:"impactPct"`
	Evidence   []string `json:"evidence"`   // concrete numbers/names
	Suggestion string   `json:"suggestion"` // actionable optimization hint
}

// Report is the analysis of one recording.
type Report struct {
	Source      string             `json:"source"`
	Name        string             `json:"name"`
	DurationSec float64            `json:"durationSec"`
	Breakdown   []Slice            `json:"breakdown"`
	Hotspots    []metrics.FuncStat `json:"hotspots"`   // top N by self time
	Modules     []ModuleStat       `json:"modules"`    // top N
	SlowFrames  []SlowFrame        `json:"slowFrames"` // worst N by Ms
	LongTasks   []LongTask         `json:"longTasks"`  // worst N by Ms
	Findings    []Finding          `json:"findings"`   // sorted by severity then ImpactMs desc
	Summary     *metrics.Summary   `json:"-"`
}

// Options tune the analyzer.
type Options struct {
	Top        int     // list length for hotspots/modules/slow frames/long tasks (default 10)
	JankMs     float64 // slow frame threshold (default 16.7)
	LongTaskMs float64 // default 50
}
