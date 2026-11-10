// Package metrics defines the analysis output for ONE recording. It is the
// contract between analyze (producer) and compare (consumer).
package metrics

type Unit string

const (
	UnitMs    Unit = "ms"
	UnitCount Unit = "count"
	UnitPct   Unit = "%"
	UnitBytes Unit = "bytes"
	UnitFPS   Unit = "fps"
	UnitRatio Unit = "ratio" // 0..1
)

// Direction says which way is an improvement.
type Direction int

const (
	LowerBetter Direction = iota
	HigherBetter
	Neutral // informational; never flagged as regression/improvement
)

// Metric is one flat scalar. Key is stable and dotted, e.g.
// "frames.p95_ms", "script.event-dispatched.total_ms", "cpu.main.mean_pct".
type Metric struct {
	Key   string    `json:"key"`
	Group string    `json:"group"` // "overview","frames","script","layout","cpu","memory","network","gc"
	Label string    `json:"label"` // human label
	Unit  Unit      `json:"unit"`
	Dir   Direction `json:"dir"`
	Value float64   `json:"value"`
}

// FuncStat: per-function sampling-profiler aggregate. Identity = Name+URL+Line.
// Self = samples where function is top frame. Total = samples where function
// appears anywhere in the stack (counted once per sample).
type FuncStat struct {
	Name        string  `json:"name"`
	URL         string  `json:"url"`
	Line        int     `json:"line"`
	SelfMs      float64 `json:"selfMs"`
	TotalMs     float64 `json:"totalMs"`
	SelfSamples int     `json:"selfSamples"`
}

// Key is the identity used to match functions across recordings.
func (f FuncStat) Key() string {
	return f.Name + "\x00" + f.URL + "\x00" + itoa(f.Line)
}

// Point is a (seconds-from-recording-start, value) sample for charts.
type Point struct {
	T float64 `json:"t"`
	V float64 `json:"v"`
}

// Summary is the full analysis of one recording.
type Summary struct {
	Source      string     `json:"source"` // file path
	Name        string     `json:"name"`   // recording displayName
	DurationSec float64    `json:"durationSec"`
	Metrics     []Metric   `json:"metrics"`
	Functions   []FuncStat `json:"functions"`

	// Series for HTML charts (not compared statistically).
	FrameMs  []float64 `json:"frameMs,omitempty"`  // rendering frame durations in ms, chronological
	CPU      []Point   `json:"cpu,omitempty"`      // total cpu usage %
	MemoryMB []Point   `json:"memoryMB,omitempty"` // total memory MB
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
