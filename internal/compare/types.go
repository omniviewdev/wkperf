// Package compare diffs two groups of Summaries (baseline vs candidate).
// A group is one or more runs of the same scenario; values are aggregated by
// median and noise-checked. This file is the contract with report packages.
package compare

import "github.com/omniviewdev/wkperf/internal/metrics"

type Verdict string

const (
	Improved  Verdict = "improved"
	Regressed Verdict = "regressed"
	Unchanged Verdict = "unchanged"
	Noisy     Verdict = "noisy"   // change within run-to-run noise (multi-run only)
	Info      Verdict = "info"    // Neutral direction metric
	NewMetric Verdict = "new"     // only in candidate
	Removed   Verdict = "removed" // only in baseline
)

// Options control verdict thresholds.
type Options struct {
	// RelThreshold: |pct change| below this is Unchanged (e.g. 0.05 = 5%).
	RelThreshold float64
	// AbsFloor: per-unit minimum |delta| to matter, keyed by metrics.Unit
	// (e.g. ms: 0.5). Deltas below floor are Unchanged.
	AbsFloor map[metrics.Unit]float64
	// TopFunctions: how many functions to keep in each of the
	// regressed / improved lists.
	TopFunctions int
	// Normalize: "auto" (default) rescales Extensive metrics per second when
	// durations differ by more than 10%; "per-second" always; "none" never.
	Normalize string
}

// Side describes one group.
type Side struct {
	Label   string   `json:"label"`   // e.g. "baseline"
	Sources []string `json:"sources"` // file paths
	Runs    int      `json:"runs"`
}

// MetricDiff is the comparison of one metric key.
type MetricDiff struct {
	Metric     metrics.Metric `json:"metric"` // Value = candidate median
	Base       float64        `json:"base"`   // baseline median
	Cand       float64        `json:"cand"`   // candidate median
	BaseRuns   []float64      `json:"baseRuns"`
	CandRuns   []float64      `json:"candRuns"`
	Delta      float64        `json:"delta"` // Cand-Base
	Pct        float64        `json:"pct"`   // Delta/Base; 0 if Base==0
	Verdict    Verdict        `json:"verdict"`
	Confidence string         `json:"confidence"` // "single-run" | "low" | "high"
}

// FuncDiff compares one function's self time.
type FuncDiff struct {
	ID                string                  `json:"id,omitempty"`
	Name              string                  `json:"name"`
	URL               string                  `json:"url"`
	Line              int                     `json:"line"`
	Column            int                     `json:"column"`
	SourceID          string                  `json:"sourceID,omitempty"`
	Original          *metrics.SourceLocation `json:"original,omitempty"`
	CommonStack       []metrics.FunctionFrame `json:"commonStack,omitempty"`
	CommonStackID     string                  `json:"commonStackID,omitempty"`
	CommonStackSource string                  `json:"commonStackSource,omitempty"`
	BaseSelf          float64                 `json:"baseSelfMs"`
	CandSelf          float64                 `json:"candSelfMs"`
	BaseTotal         float64                 `json:"baseTotalMs"`
	CandTotal         float64                 `json:"candTotalMs"`
	Delta             float64                 `json:"deltaSelfMs"`
	Pct               float64                 `json:"pct"`
	Verdict           Verdict                 `json:"verdict"`
}

// Counts is the tally used for the headline.
type Counts struct {
	Improved, Regressed, Unchanged, Noisy int
}

// Result is the full comparison.
type Result struct {
	Baseline Side         `json:"baseline"`
	Cand     Side         `json:"candidate"`
	Metrics  []MetricDiff `json:"metrics"` // ordered by Group then Key
	// FuncRegressed sorted by Delta desc; FuncImproved by Delta asc.
	FuncRegressed []FuncDiff    `json:"funcRegressed"`
	FuncImproved  []FuncDiff    `json:"funcImproved"`
	Counts        Counts        `json:"counts"`
	Comparability Comparability `json:"comparability"`

	// Representative summaries (the run closest to the median frame p95, or
	// the only run) for chart rendering.
	BaseRep *metrics.Summary `json:"-"`
	CandRep *metrics.Summary `json:"-"`
}

// Compare is implemented in compare.go:
//   func Compare(base, cand []*metrics.Summary, opts Options) *Result
// DefaultOptions is implemented in compare.go:
//   func DefaultOptions() Options

// Warning is a comparability problem between the two groups.
type Warning struct {
	Level   string `json:"level"` // "warn" | "info"
	Code    string `json:"code"`  // duration-mismatch, frame-count-mismatch, url-mismatch, instrument-mismatch, workload-mismatch, sample-sparse, run-variance
	Message string `json:"message"`
}

// Comparability reports how apples-to-apples the groups are.
type Comparability struct {
	Score      float64   `json:"score"`      // 0..1; 1 = same fixture
	Normalized bool      `json:"normalized"` // Extensive metrics were rescaled per second
	Warnings   []Warning `json:"warnings"`
}
