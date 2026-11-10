// Package rootcause links sampled hot paths to timeline evidence and reproducible queries.
// Sampled costs are estimates, not proof that a caller caused a timeline event.
package rootcause

import "github.com/omniviewdev/wkperf/internal/flame"

// Options use seconds relative to the recording start, like the query command.
type Options struct {
	Source       string
	Sourcemaps   []string
	OffsetSec    float64 // Current recording start relative to the original source.
	FromSec      float64
	ToSec        float64
	TrimStartSec float64
	TrimEndSec   float64
	Top          int
	LongTaskMs   float64
	JankMs       float64
}

// Stat keeps a numeric figure beside its reproduction command and measurement basis.
type Stat struct {
	Key     string  `json:"key"`
	Label   string  `json:"label"`
	Value   float64 `json:"value"`
	Unit    string  `json:"unit"`
	Basis   string  `json:"basis"`
	Command string  `json:"command"`
}

// Commands expose the entry points needed to reproduce each report section.
type Commands struct {
	HotPath string `json:"hotPath"`
	Window  string `json:"window"`
	Paths   string `json:"paths"`
	Depth   string `json:"depth"`
	Owners  string `json:"owners"`
	Stacks  string `json:"stacks"`
	GC      string `json:"gc"`
}

// Stack is one exact root-to-leaf stack, never an aggregation of unrelated paths.
// Frame locations retain generated and mapped identities from the recording.
type Stack struct {
	StackID        string        `json:"stackId"`
	Frames         []flame.Frame `json:"-"`
	SelfMs         float64       `json:"selfMs"`
	Samples        int           `json:"samples"`
	UntimedSamples int           `json:"untimedSamples"`
	FirstSec       *float64      `json:"firstSec"`
	LastSec        *float64      `json:"lastSec"`
	Stats          []Stat        `json:"stats"`
	Command        string        `json:"command"`
}

// Path follows the heaviest child at every level of the weighted prefix tree.
type Path struct {
	Frames    []flame.Frame `json:"frames"`
	SampledMs float64       `json:"sampledMs"`
	Samples   int           `json:"samples"`
	Stats     []Stat        `json:"stats"`
	Command   string        `json:"command"`
}

// Owner partitions sampled self time; categories never count inclusive time.
type Owner struct {
	Category string  `json:"category"`
	SelfMs   float64 `json:"selfMs"`
	Pct      float64 `json:"pct"`
	Samples  int     `json:"samples"`
	Stats    []Stat  `json:"stats"`
	Command  string  `json:"command"`
}

// Attribution charges each framework/native self sample to its closest preceding
// application frame. TotalMs includes application self and charged self only.
type Attribution struct {
	App         flame.Frame `json:"app"`
	SelfMs      float64     `json:"selfMs"`
	FrameworkMs float64     `json:"frameworkMs"`
	NativeMs    float64     `json:"nativeMs"`
	TotalMs     float64     `json:"totalMs"`
	Stacks      []Stack     `json:"stacks"`
	StacksTotal int         `json:"stacksTotal"`
	Stats       []Stat      `json:"stats"`
	Command     string      `json:"command"`
}

// Finding separates measured stack evidence from a suspected explanation.
type Finding struct {
	ID            string      `json:"id"`
	Title         string      `json:"title"`
	Severity      string      `json:"severity"`
	Explanation   string      `json:"explanation"`
	Stats         []Stat      `json:"stats"`
	Evidence      []Stack     `json:"-"`
	EvidenceTotal int         `json:"evidenceTotal"`
	Windows       []WindowRef `json:"windows"`
	WindowsTotal  int         `json:"windowsTotal"`
	Command       string      `json:"command"`
}

// WindowRef uses recording-relative seconds, directly reusable as query flags.
type WindowRef struct {
	FromSec float64 `json:"fromSec"`
	ToSec   float64 `json:"toSec"`
	Command string  `json:"command"`
}

// Fire is a deduplicated event or contiguous sample burst for a periodic root.
type Fire struct {
	FromSec float64 `json:"fromSec"`
	ToSec   float64 `json:"toSec"`
	SelfMs  float64 `json:"selfMs"`
	Samples int     `json:"samples"`
	Event   string  `json:"event"`
	Stats   []Stat  `json:"stats"`
	Command string  `json:"command"`
}

type PeriodicRoot struct {
	Root          flame.Frame `json:"root"`
	TargetID      string      `json:"targetID,omitempty"`
	FireCount     int         `json:"fireCount"`
	PeriodMs      float64     `json:"periodMs"`
	JitterMs      float64     `json:"jitterMs"`
	Regularity    float64     `json:"regularity"`
	MeanCostMs    float64     `json:"meanCostMs"`
	TotalMs       float64     `json:"totalMs"`
	Fires         []Fire      `json:"fires"`
	FiresTotal    int         `json:"firesTotal"`
	Evidence      []Stack     `json:"evidence"`
	EvidenceTotal int         `json:"evidenceTotal"`
	Stats         []Stat      `json:"stats"`
	Command       string      `json:"command"`
	SampleStatus  string      `json:"sampleStatus,omitempty"`
}

// EventOverlap measures temporal intersection with a collector interval, not a main-thread pause.
type EventOverlap struct {
	Event     string  `json:"event"`
	FromSec   float64 `json:"fromSec"`
	ToSec     float64 `json:"toSec"`
	OverlapMs float64 `json:"overlapMs"`
	Stats     []Stat  `json:"stats"`
	Command   string  `json:"command"`
}

type GCOverlap struct {
	FromSec     float64        `json:"fromSec"`
	ToSec       float64        `json:"toSec"`
	DurationMs  float64        `json:"durationMs"`
	Events      []EventOverlap `json:"events"`
	EventsTotal int            `json:"eventsTotal"`
	Attribution string         `json:"attribution"`
	Stats       []Stat         `json:"stats"`
	Command     string         `json:"command"`
}

// Window links every long task and active slow frame to its heaviest overlapping
// exact sampler stack. A missing TopStack is accompanied by SampleStatus.
type Window struct {
	Kind         string  `json:"kind"`
	Label        string  `json:"label"`
	FromSec      float64 `json:"fromSec"`
	ToSec        float64 `json:"toSec"`
	DurationMs   float64 `json:"durationMs"`
	ActiveMs     float64 `json:"activeMs"`
	HotPath      *Path   `json:"hotPath"` // Full path detail is bounded by Options.Top.
	TopStack     *Stack  `json:"topStack"`
	SampleStatus string  `json:"sampleStatus"`
	Stats        []Stat  `json:"stats"`
	Command      string  `json:"command"`
}

type DepthOutlier struct {
	Depth              int    `json:"depth"`
	RepeatedFrameDepth int    `json:"repeatedFrameDepth"`
	CommitDepth        int    `json:"commitDepth"`
	Stack              Stack  `json:"stack"`
	Stats              []Stat `json:"stats"`
	Command            string `json:"command"`
}

type DepthStats struct {
	Median                float64        `json:"median"`
	P95                   float64        `json:"p95"`
	P99                   float64        `json:"p99"`
	Max                   int            `json:"max"`
	OutlierThreshold      float64        `json:"outlierThreshold"`
	MaxRepeatedFrameDepth int            `json:"maxRepeatedFrameDepth"`
	MaxCommitDepth        int            `json:"maxCommitDepth"`
	Outliers              []DepthOutlier `json:"outliers"`
	OutliersTotal         int            `json:"outliersTotal"`
	Stats                 []Stat         `json:"stats"`
	Command               string         `json:"command"`
}

// Report places the hot path first and keeps timeline and sampled evidence distinct.
type Report struct {
	Top                 int            `json:"-"`
	SchemaVersion       string         `json:"schemaVersion"`
	Source              string         `json:"source"`
	Sourcemaps          []string       `json:"sourcemaps"`
	FromSec             float64        `json:"fromSec"`
	ToSec               float64        `json:"toSec"`
	HotPath             Path           `json:"hotPath"`
	Owners              []Owner        `json:"owners"`
	AppAttribution      []Attribution  `json:"appAttribution"`
	AppAttributionTotal int            `json:"appAttributionTotal"`
	Findings            []Finding      `json:"findings"`
	PeriodicRoots       []PeriodicRoot `json:"periodicRoots"`
	PeriodicRootsTotal  int            `json:"periodicRootsTotal"`
	GCOverlaps          []GCOverlap    `json:"gcOverlaps"`
	GCOverlapsTotal     int            `json:"gcOverlapsTotal"`
	Windows             []Window       `json:"windows"`
	WindowsTotal        int            `json:"windowsTotal"`
	LongTasks           []Window       `json:"longTasks"`
	LongTasksTotal      int            `json:"longTasksTotal"`
	SlowFrames          []Window       `json:"slowFrames"`
	SlowFramesTotal     int            `json:"slowFramesTotal"`
	InputStalls         []Window       `json:"inputStalls"`
	InputStallsTotal    int            `json:"inputStallsTotal"`
	EventBursts         []Window       `json:"eventBursts"`
	EventBurstsTotal    int            `json:"eventBurstsTotal"`
	Depth               DepthStats     `json:"depth"`
	Stats               []Stat         `json:"stats"`
	Commands            Commands       `json:"commands"`
}
