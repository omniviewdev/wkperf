// Package scorecard rates a recording against published performance
// standards (Core Web Vitals / Lighthouse thresholds where they apply,
// RAIL and frame-budget targets for a long-lived desktop webview app).
// Contract between scorecard, audit, ai and cmd packages.
package scorecard

import "github.com/omniviewdev/wkperf/internal/metrics"

type Rating string

const (
	Good             Rating = "good"
	NeedsImprovement Rating = "needs-improvement"
	Poor             Rating = "poor"
	Unavailable      Rating = "n/a" // recording lacks the data
)

// Standard is one rated metric. Thresholds follow the Web Vitals convention:
// value <= Good is good, > Poor is poor, between is needs-improvement
// (reversed when HigherBetter: >= Good is good, < Poor is poor).
type Standard struct {
	ID           string       `json:"id"`     // e.g. "frame-p95", "jank-ratio", "tbt", "inp", "fcp", "lcp", "fps", "main-busy", "gc-share", "memory-growth", "forced-layout-rate", "long-task-rate"
	Name         string       `json:"name"`   // human name
	Source       string       `json:"source"` // "Core Web Vitals", "Lighthouse", "RAIL", "frame budget (60Hz)", "wkperf heuristic"
	Unit         metrics.Unit `json:"unit"`
	Good         float64      `json:"good"`
	Poor         float64      `json:"poor"`
	HigherBetter bool         `json:"higherBetter"`
	Weight       float64      `json:"weight"` // contribution to overall score
	Note         string       `json:"note"`   // how it is derived here / caveats vs the original standard
}

// Result is a Standard evaluated on one recording (or one aggregated value).
type Result struct {
	Standard
	Value  float64 `json:"value"`
	Rating Rating  `json:"rating"`
	Score  float64 `json:"score"` // 0..100, piecewise-linear: Good->90..100, Poor->0..50 style; 100 at/better than Good*, 0 at/worse than Poor*; documented in implementation
	Detail string  `json:"detail,omitempty"`
}

// Scorecard is the full rating of one recording.
type Scorecard struct {
	Results []Result `json:"results"`
	Score   float64  `json:"score"` // weighted mean of available Results, 0..100
	Grade   string   `json:"grade"` // A (>=90) B (>=75) C (>=60) D (>=40) F
	// Gap lists the available results not rated Good, worst first, with the
	// distance to the Good threshold in the Standard's unit: "how far from a
	// very performant UI".
	Gap []GapItem `json:"gap"`
}

type GapItem struct {
	ID     string  `json:"id"`
	Name   string  `json:"name"`
	Value  float64 `json:"value"`
	Target float64 `json:"target"` // the Good threshold
	Rating Rating  `json:"rating"`
	Factor float64 `json:"factor"` // how many times over target (value/target; target/value if HigherBetter)
}

// Implemented in scorecard.go:
//   func Standards() []Standard
//   func Evaluate(rec *model.Recording, rep *insights.Report) *Scorecard   // derives every metric from the recording + analysis
//   func FromValues(values map[string]float64) *Scorecard                  // builds a Scorecard from standard-ID -> value (used to aggregate medians across runs/scenes); missing IDs => Unavailable
//   func Values(c *Scorecard) map[string]float64                           // available id -> value
// Implemented in render.go:
//   func RenderTerm(w io.Writer, c *Scorecard, color bool) error
//   func RenderMarkdown(w io.Writer, c *Scorecard) error
