// Package insights turns ONE recording into a performance profile: where wall
// time goes, hotspots, slow-frame attribution and heuristic findings with
// optimization hints. Contract between the analyzer, AI and CLI packages.
package insights

import (
	"github.com/omniviewdev/wkperf/internal/flame"
	"github.com/omniviewdev/wkperf/internal/metrics"
	"github.com/omniviewdev/wkperf/internal/rootcause"
)

type Severity string

const (
	SevHigh   Severity = "high"
	SevMedium Severity = "medium"
	SevLow    Severity = "low"
	SevInfo   Severity = "info"
)

// MinFunctionSamples is the minimum self-sample support for a concentration lead.
const MinFunctionSamples = 5

// Slice is one exclusive part of active wall time. Idle includes unused
// portions of idle-gap frames; unattributed frame time is not measured work.
type Slice struct {
	Name string  `json:"name"`
	Ms   float64 `json:"ms"`
	Pct  float64 `json:"pct"` // of recording wall time
}

// ModuleStat groups sampled self time by normalized file URL by default.
// Parent-directory grouping is available explicitly through Options.
type ModuleStat struct {
	Name        string  `json:"name"`
	SelfMs      float64 `json:"selfMs"`
	Pct         float64 `json:"pct"` // of total sampled self time
	SelfSamples int     `json:"selfSamples"`
}

// SlowFrame is a work-bound active rendering interval above the jank threshold.
// GC is a collector overlay, not part of WorkMs.
type SlowFrame struct {
	T                   float64 `json:"t"`  // seconds from recording start
	Ms                  float64 `json:"ms"` // frame duration
	WorkMs              float64 `json:"workMs"`
	ScriptMs            float64 `json:"scriptMs"`
	LayoutMs            float64 `json:"layoutMs"` // layout+style
	PaintMs             float64 `json:"paintMs"`  // paint+composite
	GCMs                float64 `json:"gcMs"`
	GCOverlapWithWorkMs float64 `json:"gcOverlapWithWorkMs"`
	TopEvent            string  `json:"topEvent"`          // dominant script eventType/details, e.g. "event-dispatched click"
	TopFunc             string  `json:"topFunc,omitempty"` // hottest sampled function in the frame, if samples cover it
	SampleStatus        string  `json:"sampleStatus,omitempty"`
}

// LongTask is a maximal union of overlapping script records above the task threshold.
type LongTask struct {
	T             float64  `json:"t"`
	Ms            float64  `json:"ms"`
	EventType     string   `json:"eventType"`
	Children      []string `json:"children"`
	ChildrenTotal int      `json:"childrenTotal"`
	Detail        string   `json:"detail,omitempty"` // e.g. event name, timer id, URL
	TopFunc       string   `json:"topFunc,omitempty"`
	SampleStatus  string   `json:"sampleStatus,omitempty"`
}

// InputStall retains input-driven rendering intervals with at most one budget of work.
// Interval length is an observation, not measured main-thread blocking time.
type InputStall struct {
	T            float64 `json:"t"`
	Ms           float64 `json:"ms"`
	WorkMs       float64 `json:"workMs"`
	TopEvent     string  `json:"topEvent"`
	TopFunc      string  `json:"topFunc,omitempty"`
	SampleStatus string  `json:"sampleStatus,omitempty"`
}

// Evidence is one numeric observation. Basis distinguishes measurements from
// inference; sample-weighted timings are estimates, not instrumented durations.
type Evidence struct {
	MetricKey   string       `json:"metricKey"`
	Value       float64      `json:"value"`
	Unit        metrics.Unit `json:"unit"`
	Description string       `json:"description"`
	Command     string       `json:"command"`
	Basis       string       `json:"basis"`
}

type Verification struct {
	MetricKey   string       `json:"metricKey"`
	Direction   string       `json:"direction"`
	Target      float64      `json:"target"`
	Unit        metrics.Unit `json:"unit"`
	Description string       `json:"description"`
	Command     string       `json:"command"`
}

type SuggestedWindow struct {
	FromSec float64  `json:"fromSec"`
	ToSec   float64  `json:"toSec"`
	Reason  string   `json:"reason"`
	Flags   []string `json:"flags"`
}

// TriggerStat aggregates a dispatch event name or callback trigger. Function
// timings use samples whose timestamps fall within the merged trigger spans.
type TriggerStat struct {
	Kind          string             `json:"kind"`
	Name          string             `json:"name"`
	Count         int                `json:"count"`
	TotalMs       float64            `json:"totalMs"`
	MeanMs        float64            `json:"meanMs"`
	P95Ms         float64            `json:"p95Ms"`
	MaxMs         float64            `json:"maxMs"`
	TopSelf       []metrics.FuncStat `json:"topSelf"`
	TopInclusive  []metrics.FuncStat `json:"topInclusive"`
	SampleMessage string             `json:"sampleMessage,omitempty"`
}

type WakeupStats struct {
	Count     int           `json:"count"`
	PerSecond float64       `json:"perSecond"`
	Causes    []TriggerStat `json:"causes"`
}

type SampleCoverage struct {
	SampleCount        int                 `json:"sampleCount"`
	TimedSampleCount   int                 `json:"timedSampleCount"`
	UntimedSampleCount int                 `json:"untimedSampleCount"`
	UntimedMs          float64             `json:"untimedMs"`
	UntimedExcluded    bool                `json:"untimedExcluded"`
	UntimedSpans       []flame.UntimedSpan `json:"untimedSpans"`
	SampledMs          float64             `json:"sampledMs"`
	ScriptMs           float64             `json:"scriptMs"`
	Ratio              float64             `json:"ratio"`
	Available          bool                `json:"available"`
	Interpretation     string              `json:"interpretation"`
}

type NodeClue struct {
	CSSPath     string `json:"cssPath,omitempty"`
	DisplayName string `json:"displayName,omitempty"`
	EventType   string `json:"eventType"`
}

// EvidenceBasis identifies how the finding itself is supported, not its severity.
type EvidenceBasis string

const (
	EvidenceMeasured   EvidenceBasis = "measured"
	EvidenceSampled    EvidenceBasis = "sampled"
	EvidenceHypothesis EvidenceBasis = "hypothesis"
)

// Confidence rates support for a finding, not significance of a comparison delta.
type Confidence string

const (
	ConfidenceLow    Confidence = "low"
	ConfidenceMedium Confidence = "medium"
	ConfidenceHigh   Confidence = "high"
)

// Replication counts actual findings against all requested captures. ParsedRuns
// excludes failed or unavailable analyses; these remain in the Runs denominator.
type Replication struct {
	Runs            int `json:"runs"`
	ParsedRuns      int `json:"parsedRuns"`
	RunsWithFinding int `json:"runsWithFinding"`
}

// Finding retains numeric evidence and testable optimization hypotheses.
type Finding struct {
	ID                string                 `json:"id"`
	Title             string                 `json:"title"`
	Severity          Severity               `json:"severity"`
	Category          string                 `json:"category"`
	ImpactMs          float64                `json:"impactMs"`
	ImpactPct         float64                `json:"impactPct"`
	Evidence          []string               `json:"evidence"`
	NumericEvidence   []Evidence             `json:"numericEvidence"`
	MetricKeys        []string               `json:"metricKeys"`
	Verify            []Verification         `json:"verify"`
	EvidenceBasis     EvidenceBasis          `json:"evidenceBasis"`
	Replication       Replication            `json:"replication"`
	Confidence        Confidence             `json:"confidence"`
	ConfidenceReasons []string               `json:"confidenceReasons"`
	SuggestedWindows  []SuggestedWindow      `json:"suggestedWindows"`
	Functions         []string               `json:"functions"`
	Triggers          []string               `json:"triggers"`
	Nodes             []NodeClue             `json:"nodes"`
	Suggestion        string                 `json:"suggestion"`
	Stacks            []rootcause.Stack      `json:"-"`
	StackIDs          []string               `json:"stackIds"`
	StacksTotal       int                    `json:"stacksTotal"`
	Command           string                 `json:"command"`
	SampleIDs         []string               `json:"-"`
	RelatedFindings   []string               `json:"relatedFindings,omitempty"`
	NativeReads       []rootcause.NativeRead `json:"nativeReads,omitempty"`
	NativeReadsTotal  int                    `json:"nativeReadsTotal"`
	Depth             *rootcause.DepthFacets `json:"depth,omitempty"`
}

// Report is the analysis of one recording.
type Report struct {
	Top                 int                  `json:"-"`
	RootCause           *rootcause.Report    `json:"rootCause"`
	FlameTree           *flame.Tree          `json:"-"`
	SchemaVersion       string               `json:"schemaVersion"`
	Source              string               `json:"source"`
	MoreEvidenceCommand string               `json:"moreEvidenceCommand,omitempty"`
	WindowStartSec      float64              `json:"windowStartSec"`
	WindowEndSec        float64              `json:"windowEndSec"`
	WholeRecording      bool                 `json:"-"`
	ModuleGrouping      string               `json:"moduleGrouping"`
	JankMs              float64              `json:"jankMs"`
	LongTaskMs          float64              `json:"longTaskMs"`
	SourceMapPaths      []string             `json:"sourceMapPaths"`
	Ownership           flame.OwnershipRules `json:"ownership"`
	Name                string               `json:"name"`
	DurationSec         float64              `json:"durationSec"`
	Breakdown           []Slice              `json:"breakdown"`
	GCMs                float64              `json:"gcMs"`
	GCOverlapWithWorkMs float64              `json:"gcOverlapWithWorkMs"`
	Hotspots            []metrics.FuncStat   `json:"hotspots"` // top N by self time
	InclusiveHotspots   []metrics.FuncStat   `json:"inclusiveHotspots"`
	CommonStacks        []metrics.StackStat  `json:"commonStacks"`
	Triggers            []TriggerStat        `json:"triggers"`
	Wakeups             WakeupStats          `json:"wakeups"`
	SampleCoverage      SampleCoverage       `json:"sampleCoverage"`
	Modules             []ModuleStat         `json:"modules"`    // top N
	SlowFrames          []SlowFrame          `json:"slowFrames"` // worst N active frames by measured work
	SlowFramesTotal     int                  `json:"slowFramesTotal"`
	InputStalls         []InputStall         `json:"inputStalls"` // longest N observed input stalls
	InputStallsTotal    int                  `json:"inputStallsTotal"`
	LongTasks           []LongTask           `json:"longTasks"` // worst N by duration
	LongTasksTotal      int                  `json:"longTasksTotal"`
	Findings            []Finding            `json:"findings"` // sorted by severity then ImpactMs desc
	Summary             *metrics.Summary     `json:"-"`
}

// Options tune the analyzer.
type Options struct {
	Top            int     // list length for hotspots/modules/slow frames/long tasks (default 10)
	JankMs         float64 // slow frame threshold (default 16.7)
	LongTaskMs     float64 // default 50
	ModuleGrouping string  // "file" (default) or "parent-directory"
	WindowStartSec float64 // original recording-relative start when rec was trimmed
	Sourcemaps     []string
	Ownership      flame.OwnershipRules
}
