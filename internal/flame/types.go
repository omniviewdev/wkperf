// Package flame builds deterministic, duration-weighted calling-context trees.
// It performs no I/O. Durations are milliseconds; relative timestamps and window
// bounds are seconds rounded to nanoseconds to avoid absolute-origin drift.
package flame

import "github.com/omniviewdev/wkperf/internal/model"

// Category describes the frame's owner, not its callers' owners.
type Category string

const (
	CategoryApp       Category = "app"
	CategoryFramework Category = "framework"
	CategoryNative    Category = "native"
	CategoryGC        Category = "gc"
)

// Location is an original source location supplied by a source-map resolver.
type Location struct {
	Name   string `json:"name,omitempty"`
	URL    string `json:"url"`
	Line   int    `json:"line"`
	Column int    `json:"column"`
}

// Frame identifies a generated function. SourceID distinguishes URL-less frames
// only; a source ID never changes an identity that already has a URL. Original
// enriches the generated identity without making IDs depend on source maps.
type Frame struct {
	ID        string    `json:"id"`
	Name      string    `json:"name"`
	URL       string    `json:"url"`
	Line      int       `json:"line"`
	Column    int       `json:"column"`
	SourceID  string    `json:"sourceID,omitempty"`
	Native    bool      `json:"native"`
	Anonymous bool      `json:"anonymous"`
	Category  Category  `json:"category"`
	Original  *Location `json:"original,omitempty"`
}

// FrameMapper resolves original sources. It must be deterministic and must not
// mutate the input recording. Returning nil means no original location exists.
type FrameMapper func(model.StackFrame) *Location

// Options supplies source-map metadata and recording-relative window bounds.
// Nil Intervals leaves active time unrestricted; a nonnil empty slice selects
// no time. Timestamp membership uses half-open intervals and preserves each
// sample's statistical weight. Missing durations receive one millisecond.
// Zero, negative, and nonfinite durations and empty stacks are skipped.
// Missing/nonfinite timestamps contribute only to whole-recording aggregates.
type Options struct {
	Mapper    FrameMapper
	From      *float64
	To        *float64
	Intervals []Interval
}

// Node is one calling context. Recursive appearances have distinct path IDs.
// Samples counts positive-weight samples traversing this context.
type Node struct {
	ID       string  `json:"id"`
	Frame    Frame   `json:"frame"`
	SelfMs   float64 `json:"selfMs"`
	TotalMs  float64 `json:"totalMs"`
	Samples  int     `json:"samples"`
	Children []*Node `json:"children"`
	Parent   *Node   `json:"-"`
}

// Sample retains one canonical root-to-leaf stack and statistical weight.
// Timestamp is recording-relative; WeightMs is not an observed occupied span.
type Sample struct {
	Frames    []Frame `json:"frames"`
	Timestamp float64 `json:"timestamp"`
	WeightMs  float64 `json:"weightMs"`
	TargetID  string  `json:"targetID,omitempty"`
	Untimed   bool    `json:"-"`
}

// Interval is a half-open recording-relative time range, in seconds.
type Interval struct {
	Start float64 `json:"start"`
	End   float64 `json:"end"`
}

// SampleTotal describes recording-wide evidence, even when a view excludes it.
type SampleTotal struct {
	Count int     `json:"count"`
	Ms    float64 `json:"ms"`
}

// UntimedSpan bounds one target's untimed run using its original export order.
// Bounds are labels, not timestamps or a basis for attributing sample weight.
// Nonmonotonic timed exports have no inferred spans.
type UntimedSpan struct {
	FromSec float64 `json:"from_sec"`
	ToSec   float64 `json:"to_sec"`
	Samples int     `json:"samples"`
	Ms      float64 `json:"ms"`
}

// Tree retains canonical samples for repeatable windows and deeper analysis.
// Root is a synthetic frame, never included in function/edge/path aggregates.
// StartTime and EndTime describe the selected recording-relative wall window.
type Tree struct {
	Root             *Node         `json:"root"`
	Samples          []Sample      `json:"samples"`
	Untimed          []Sample      `json:"untimed"`
	UntimedTotal     SampleTotal   `json:"untimedTotal"`
	UntimedIncluded  bool          `json:"untimedIncluded"`
	UntimedSpans     []UntimedSpan `json:"untimed_spans,omitempty"`
	StartTime        float64       `json:"startTime"`
	EndTime          float64       `json:"endTime"`
	ActiveDurationMs float64       `json:"activeDurationMs"`
	Discontinuities  []Interval    `json:"discontinuities,omitempty"`
	active           []Interval
	inverted         bool
	functions        []Function
	edges            []Edge
	paths            []Path
	samplePathIDs    []string
	untimedPathIDs   []string
	byID             map[string]*Node
	recordingEnd     float64
	recordingActive  []Interval
}

// Function aggregates all calling contexts for one function. TotalMs and
// Samples count a function once per sample even when recursion repeats it.
type Function struct {
	ID          string  `json:"id"`
	Frame       Frame   `json:"frame"`
	SelfMs      float64 `json:"selfMs"`
	TotalMs     float64 `json:"totalMs"`
	Samples     int     `json:"samples"`
	SelfSamples int     `json:"selfSamples"`
}

// Edge describes an immediate caller-to-callee relation. A repeated recursive
// edge is counted once per sample. TotalMs is the sample's weighted contribution.
type Edge struct {
	CallerID string  `json:"callerID"`
	CalleeID string  `json:"calleeID"`
	Caller   Frame   `json:"caller"`
	Callee   Frame   `json:"callee"`
	TotalMs  float64 `json:"totalMs"`
	Samples  int     `json:"samples"`
}

// Path is one complete root-to-leaf stack. SelfMs and TotalMs both equal the
// weighted samples ending at this full stack, not samples ending below it.
type Path struct {
	ID      string  `json:"id"`
	Frames  []Frame `json:"frames"`
	Samples int     `json:"samples"`
	SelfMs  float64 `json:"selfMs"`
	TotalMs float64 `json:"totalMs"`
}

// FunctionDelta is an after-minus-before aggregate keyed by stable function ID.
// SelfMs and TotalMs use Comparison.Normalization.Unit. Before/After values
// remain raw milliseconds; sample counts are never normalized.
type FunctionDelta struct {
	ID            string  `json:"id"`
	Frame         Frame   `json:"frame"`
	BeforeSelfMs  float64 `json:"beforeSelfMs"`
	AfterSelfMs   float64 `json:"afterSelfMs"`
	SelfMs        float64 `json:"selfMs"`
	BeforeTotalMs float64 `json:"beforeTotalMs"`
	AfterTotalMs  float64 `json:"afterTotalMs"`
	TotalMs       float64 `json:"totalMs"`
	BeforeSamples int     `json:"beforeSamples"`
	AfterSamples  int     `json:"afterSamples"`
	Samples       int     `json:"samples"`
}

// NodeDelta is an after-minus-before calling-context delta. Its ID matches the
// corresponding Node ID in either tree. Children follow deterministic order.
// SelfMs and TotalMs use Comparison.Normalization.Unit; Before/After retain ms.
type NodeDelta struct {
	ID            string       `json:"id"`
	Frame         Frame        `json:"frame"`
	BeforeSelfMs  float64      `json:"beforeSelfMs"`
	AfterSelfMs   float64      `json:"afterSelfMs"`
	SelfMs        float64      `json:"selfMs"`
	BeforeTotalMs float64      `json:"beforeTotalMs"`
	AfterTotalMs  float64      `json:"afterTotalMs"`
	TotalMs       float64      `json:"totalMs"`
	BeforeSamples int          `json:"beforeSamples"`
	AfterSamples  int          `json:"afterSamples"`
	Samples       int          `json:"samples"`
	Children      []*NodeDelta `json:"children"`
}

type Comparison struct {
	Root          *NodeDelta        `json:"root"`
	Functions     []FunctionDelta   `json:"functions"`
	BeforeTotalMs float64           `json:"beforeTotalMs"`
	AfterTotalMs  float64           `json:"afterTotalMs"`
	TotalMs       float64           `json:"totalMs"`
	Normalization DiffNormalization `json:"normalization"`
}
