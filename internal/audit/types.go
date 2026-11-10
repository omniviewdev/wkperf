// Package audit aggregates many recordings of the SAME build (different
// scenes, optionally several runs per scene) into one non-comparison report:
// where time goes overall, what to fix first, how far from a performant UI.
package audit

import (
	"github.com/omniviewdev/wkperf/internal/insights"
	"github.com/omniviewdev/wkperf/internal/metrics"
	"github.com/omniviewdev/wkperf/internal/rootcause"
	"github.com/omniviewdev/wkperf/internal/scorecard"
)

// Run is one analyzed recording.
type Run struct {
	Source string
	Report *insights.Report
	Card   *scorecard.Scorecard
}

// Scene is one scenario. Runs contains parsed captures; MissingRuns counts
// requested captures that could not be loaded or observed in the window.
type Scene struct {
	Name        string
	Runs        []Run
	MissingRuns int
}

// SceneResult is a scene after aggregating its runs.
type SceneResult struct {
	Name          string               `json:"name"`
	Sources       []string             `json:"sources"`
	Runs          int                  `json:"runs"`
	AttemptedRuns int                  `json:"attemptedRuns"`
	MissingRuns   int                  `json:"missingRuns"`
	DurationSec   float64              `json:"durationSec"`        // median
	Card          *scorecard.Scorecard `json:"card"`               // scorecard of per-metric medians across runs
	Breakdown     []insights.Slice     `json:"breakdown"`          // median share per slice
	TopFinding    string               `json:"topFinding"`         // title of highest-impact finding
	Rep           *insights.Report     `json:"analysis,omitempty"` // representative run, closest to median frame p95
	runs          []Run
}

// Priority is a cross-scene finding rollup, the "fix this first" list.
type Priority struct {
	ID                 string                     `json:"id"`
	Title              string                     `json:"title"`
	Category           string                     `json:"category"`
	Severity           insights.Severity          `json:"severity"` // max across scenes
	Scenes             []string                   `json:"scenes"`   // scenes where it fired
	TotalImpactMs      float64                    `json:"totalImpactMs"`
	AvgImpactPct       float64                    `json:"avgImpactPct"` // mean ImpactPct over ALL scenes (0 where absent)
	Rank               float64                    `json:"rank"`         // sort key: breadth-weighted impact; documented in implementation
	Evidence           []string                   `json:"evidence"`     // from the scene with highest impact, prefixed by scene name
	Suggestion         string                     `json:"suggestion"`
	EvidenceScene      string                     `json:"evidenceScene,omitempty"`
	EvidenceSceneIndex int                        `json:"evidenceSceneIndex"`
	NumericEvidence    []insights.Evidence        `json:"numericEvidence,omitempty"`
	MetricKeys         []string                   `json:"metricKeys,omitempty"`
	Verify             []insights.Verification    `json:"verify,omitempty"`
	EvidenceBasis      insights.EvidenceBasis     `json:"evidenceBasis"`
	Replication        insights.Replication       `json:"replication"`
	Confidence         insights.Confidence        `json:"confidence,omitempty"`
	ConfidenceReasons  []string                   `json:"confidenceReasons,omitempty"`
	SuggestedWindows   []insights.SuggestedWindow `json:"suggestedWindows,omitempty"`
	Functions          []string                   `json:"functions,omitempty"`
	Triggers           []string                   `json:"triggers,omitempty"`
	Nodes              []insights.NodeClue        `json:"nodes,omitempty"`
	Stacks             []rootcause.Stack          `json:"-"`
	StackIDs           []string                   `json:"stackIds"`
	StacksTotal        int                        `json:"stacksTotal"`
	Command            string                     `json:"command,omitempty"`
	RelatedFindings    []string                   `json:"relatedFindings,omitempty"`
	NativeReads        []rootcause.NativeRead     `json:"nativeReads,omitempty"`
	NativeReadsTotal   int                        `json:"nativeReadsTotal"`
	Depth              *rootcause.DepthFacets     `json:"depth,omitempty"`
}

// HotFunction is a function merged across scenes by FuncStat.Key.
type HotFunction struct {
	ID                 string                  `json:"id,omitempty"`
	Name               string                  `json:"-"`
	URL                string                  `json:"-"`
	Line               int                     `json:"-"`
	Column             int                     `json:"-"`
	SourceID           string                  `json:"-"`
	Original           *metrics.SourceLocation `json:"-"`
	CommonStack        []metrics.FunctionFrame `json:"-"`
	CommonStackID      string                  `json:"stackId,omitempty"`
	CommonStackScene   string                  `json:"commonStackScene,omitempty"`
	EvidenceSceneIndex int                     `json:"evidenceSceneIndex"`
	SelfMs             float64                 `json:"selfMs"` // sum of per-scene median self ms
	Pct                float64                 `json:"pct"`    // of total sampled self time across scenes
	SceneCount         int                     `json:"sceneCount"`
	Scenes             []string                `json:"scenes"`
}

type Report struct {
	Scenes     []SceneResult         `json:"scenes"`          // worst score first
	Overall    *scorecard.Scorecard  `json:"overall"`         // across scenes: per standard take the WORST scene median? NO: use the median across scenes; the per-scene worst is in WorstByStandard
	WorstByStd map[string]string     `json:"worstByStandard"` // standard ID -> scene name with the worst value
	Breakdown  []insights.Slice      `json:"breakdown"`       // duration-weighted across scenes
	Priorities []Priority            `json:"priorities"`
	Hotspots   []HotFunction         `json:"hotspots"` // top N
	Modules    []insights.ModuleStat `json:"modules"`  // merged across scenes, top N
}

type Options struct{ Top int } // default 10

// Implemented in audit.go:    func Build(scenes []Scene, o Options) *Report
// Implemented in render.go:   func RenderTerm(w io.Writer, r *Report, color bool) error
//                             func RenderMarkdown(w io.Writer, r *Report) error
// (HTML is rendered by internal/report/audithtml.)
