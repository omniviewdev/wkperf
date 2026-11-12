// Package metrics defines the analysis output for ONE recording. It is the
// contract between analyze (producer) and compare (consumer).
package metrics

import "github.com/omniviewdev/wkperf/internal/model"

type Unit string

const (
	UnitMs    Unit = "ms"
	UnitCount Unit = "count"
	UnitPct   Unit = "%"
	UnitBytes Unit = "bytes"
	UnitMB    Unit = "MB"
	UnitFPS   Unit = "fps"
	UnitRatio Unit = "ratio" // dimensionless; estimated coverage may exceed 1
)

// Direction says which way is an improvement.
type Direction int

const (
	LowerBetter Direction = iota
	HigherBetter
	Neutral // informational; never flagged as regression/improvement
)

// Evidence bases of a scalar. An empty Metric.Basis means BasisMeasured; the
// query registry, finding evidence and the composite cadence share the labels.
const (
	BasisMeasured  = "measured"
	BasisEstimated = "estimated"
)

// Metric is one flat scalar. Key is stable and dotted, e.g.
// "frames.p95_ms", "script.event-dispatched.total_ms", "cpu.main.mean_pct".
type Metric struct {
	Key   string    `json:"key"`
	Group string    `json:"group"` // "overview","frames","cadence","script","layout","cpu","memory","network","gc"
	Label string    `json:"label"` // human label
	Unit  Unit      `json:"unit"`
	Dir   Direction `json:"dir"`
	Value float64   `json:"value"`
	// Extensive marks metrics that scale with capture length/activity (counts
	// and total_ms sums). Compare normalizes these per second when fixtures
	// differ in duration. Percentiles, means, maxima, ratios are intensive.
	Extensive bool `json:"extensive,omitempty"`
	// Basis is empty for an instrumented measurement. BasisEstimated marks a
	// value inferred from other records, such as the composite cadence.
	Basis string `json:"basis,omitempty"`
}

// FuncStat aggregates weighted samples. Generated URL, line, column and name
// define identity; sourceID distinguishes URL-less functions. Recursion counts
// once per sample for inclusive time, not once per recursive occurrence.
type FuncStat struct {
	ID            string          `json:"id"`
	Name          string          `json:"name"`
	URL           string          `json:"url"`
	Line          int             `json:"line"`
	Column        int             `json:"column"`
	SourceID      string          `json:"sourceID,omitempty"`
	Original      *SourceLocation `json:"original,omitempty"`
	SelfMs        float64         `json:"selfMs"`
	TotalMs       float64         `json:"totalMs"`
	SelfSamples   int             `json:"selfSamples"`
	Callers       []CallEdge      `json:"callers,omitempty"`
	Callees       []CallEdge      `json:"callees,omitempty"`
	CommonStack   []FunctionFrame `json:"commonStack"`
	CommonStackID string          `json:"commonStackID"`
}

// Key is the identity used to match functions across recordings.
func (f FuncStat) Key() string {
	return f.Frame().Key()
}

// SourceLocation retains the original source-map name and 1-based coordinates.
type SourceLocation = model.SourceLocation

// FunctionFrame is a location-only function reference, without recursive stats.
type FunctionFrame struct {
	ID       string          `json:"id"`
	Name     string          `json:"name"`
	URL      string          `json:"url"`
	Line     int             `json:"line"`
	Column   int             `json:"column"`
	SourceID string          `json:"sourceID,omitempty"`
	Original *SourceLocation `json:"original,omitempty"`
}

func (f FuncStat) Frame() FunctionFrame {
	return FunctionFrame{ID: f.ID, Name: f.Name, URL: f.URL, Line: f.Line, Column: f.Column, SourceID: f.SourceID, Original: f.Original}
}

// Key always uses generated coordinates, even when a source map is available.
func (f FunctionFrame) Key() string {
	source := "url:" + f.URL
	if f.URL == "" {
		source = "sourceID:" + f.SourceID
	}
	return f.Name + "\x00" + source + "\x00" + itoa(f.Line) + "\x00" + itoa(f.Column)
}

func (f FuncStat) Location() string { return f.Frame().Location() }

// Location displays generated and resolved coordinates without hiding either.
func (f FunctionFrame) Location() string {
	source := f.URL
	if source == "" {
		source = "sourceID:" + f.SourceID
	}
	location := source + ":" + itoa(f.Line) + ":" + itoa(f.Column)
	if f.Original != nil {
		location += " → " + f.Original.URL + ":" + itoa(f.Original.Line) + ":" + itoa(f.Original.Column)
	}
	return location
}

// CallEdge counts each adjacent caller/callee relationship once per sample.
type CallEdge struct {
	Function FunctionFrame `json:"function"`
	WeightMs float64       `json:"weightMs"`
	Samples  int           `json:"samples"`
}

// StackStat aggregates one full canonical root-to-leaf sampled stack.
type StackStat struct {
	ID       string          `json:"id"`
	Frames   []FunctionFrame `json:"frames"`
	WeightMs float64         `json:"weightMs"`
	Samples  int             `json:"samples"`
}

// Point is a (seconds-from-recording-start, value) sample for charts.
type Point struct {
	T float64 `json:"t"`
	V float64 `json:"v"`
}

// Summary is the full analysis of one recording.
type Summary struct {
	Source             string       `json:"source"` // file path
	Name               string       `json:"name"`   // recording displayName
	DurationSec        float64      `json:"durationSec"`
	Metrics            []Metric     `json:"metrics"`
	Functions          []FuncStat   `json:"functions"`
	Fixture            Fixture      `json:"fixture"`
	InclusiveFunctions []FuncStat   `json:"inclusiveFunctions,omitempty"`
	CommonStacks       []StackStat  `json:"commonStacks,omitempty"`
	WallDurationSec    float64      `json:"wallDurationSec"`
	PausedDurationSec  float64      `json:"pausedDurationSec"`
	IdleWakeups        []IdleWakeup `json:"idleWakeups,omitempty"`

	// StalledComposites are composite records excluded from main-thread work.
	StalledComposites StalledComposites `json:"stalledComposites"`

	// CompositeCadence estimates cadence when the capture has no rendering-frame
	// records; nil otherwise, and nil when there is no composite start to use.
	CompositeCadence *CompositeCadence `json:"compositeCadence,omitempty"`

	// Series for HTML charts (not compared statistically).
	FrameMs  []float64 `json:"frameMs,omitempty"`  // active rendering intervals, excluding idle gaps and input stalls
	CPU      []Point   `json:"cpu,omitempty"`      // total cpu usage %
	MemoryMB []Point   `json:"memoryMB,omitempty"` // total memory MB
}

// IdleWakeup describes a low-occupancy rendering interval, not a slow frame.
type IdleWakeup struct {
	StartSec        float64 `json:"startSec"`
	DurationMs      float64 `json:"durationMs"`
	WorkMs          float64 `json:"workMs"`
	DominantTrigger string  `json:"dominantTrigger"`
}

// StalledComposites counts composite records whose reported span exceeds the
// stall threshold. WebKit can report a composite's end seconds late, so such a
// span is not main-thread work: it is excluded from the time breakdown, from
// frame and finding attribution and from the layout.composite.* statistics.
type StalledComposites struct {
	ThresholdMs float64 `json:"thresholdMs"` // longest composite span still counted as work
	Count       int     `json:"count"`
	// TotalMs sums the reported spans clipped to the selection. Stalled spans
	// can overlap and each adds separately, so it may exceed the capture length.
	TotalMs float64 `json:"totalMs"`
}

// CompositeCadence is a fallback estimate of rendering cadence for captures
// without rendering-frame records. It describes the spacing of composite record
// START times within active runs: starts less than 100 ms apart form one run,
// and the gap between runs is not an interval. It is not frame latency and
// never feeds the real frame statistics, jank ratio or the grade.
type CompositeCadence struct {
	Basis            string  `json:"basis"`            // always BasisEstimated
	Starts           int     `json:"starts"`           // composite record starts in the selection
	Runs             int     `json:"runs"`             // active runs holding at least two starts
	Count            int     `json:"count"`            // intervals between consecutive starts of one run
	P50Ms            float64 `json:"p50Ms"`            // zero when Count is zero
	P95Ms            float64 `json:"p95Ms"`            // zero when Count is zero
	MaxMs            float64 `json:"maxMs"`            // zero when Count is zero
	MissedVsyncCount int     `json:"missedVsyncCount"` // intervals over 1.5 frame budgets (25.05 ms)
	MissedVsyncRatio float64 `json:"missedVsyncRatio"` // MissedVsyncCount / Count; zero when Count is zero
	Over50MsCount    int     `json:"over50MsCount"`    // intervals over 50 ms
	ActiveRunSec     float64 `json:"activeRunSec"`     // sum of the intervals: time spanned by active runs
}

// Lookup returns the metric with key, if present.
func (s *Summary) Lookup(key string) (Metric, bool) {
	for _, m := range s.Metrics {
		if m.Key == key {
			return m, true
		}
	}
	return Metric{}, false
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var b [20]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		b[i] = '-'
	}
	return string(b[i:])
}

// Fixture describes what was captured, to judge comparability of two recordings.
type Fixture struct {
	DurationSec     float64  `json:"durationSec"` // after any trimming/windowing
	Frames          int      `json:"frames"`
	Records         int      `json:"records"`
	SampleCount     int      `json:"sampleCount"`
	URLs            []string `json:"urls"`             // distinct target URLs
	Instruments     []string `json:"instruments"`      // recording instrument types
	ScriptEventSet  []string `json:"scriptEventSet"`   // distinct script eventTypes seen
	TopFunctionKeys []string `json:"topFunctionKeys"`  // FuncStat.Key of top 20 by self time (workload fingerprint)
	Window          string   `json:"window,omitempty"` // e.g. "2.0s-12.0s" when trimmed
}
