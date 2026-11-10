// Package query provides bounded, deterministic recording queries for the CLI and MCP.
package query

import (
	"context"

	"github.com/omniviewdev/wkperf/internal/flame"
	"github.com/omniviewdev/wkperf/internal/model"
)

const (
	Schema       = "wkperf.query.v1"
	DefaultLimit = 50
	MaxLimit     = 1000
)

// SourceOptions controls source-map resolution before a shared flame tree is built.
type SourceOptions struct {
	Sourcemaps []string             `json:"sourcemaps,omitempty"`
	Ownership  flame.OwnershipRules `json:"ownership,omitempty"`
}

// Request selects one operation. Times are seconds relative to each source start.
// Zero limit means DefaultLimit. Zero to means recording end. DepthSet and
// MinShareSet preserve explicit zero; omitted values use depth eight and 5%.
type Request struct {
	File           string   `json:"file"`
	Operation      string   `json:"operation"`
	QueryOperation string   `json:"query_operation,omitempty"`
	Selector       string   `json:"selector,omitempty"`
	Baseline       string   `json:"baseline,omitempty"`
	Candidate      string   `json:"candidate,omitempty"`
	By             string   `json:"by,omitempty"`
	Module         string   `json:"module,omitempty"`
	Category       string   `json:"category,omitempty"`
	Limit          int      `json:"limit,omitempty"`
	RootCauseTop   int      `json:"top,omitempty"`
	Depth          int      `json:"depth,omitempty"`
	DepthSet       bool     `json:"-"`
	MinMS          float64  `json:"min_ms,omitempty"`
	MinShare       float64  `json:"min_share,omitempty"`
	MinShareSet    bool     `json:"-"`
	MinSamples     int      `json:"min_samples,omitempty"`
	MinSamplesSet  bool     `json:"-"`
	TieRatio       float64  `json:"tie_ratio,omitempty"`
	TieRatioSet    bool     `json:"-"`
	LongTaskMS     float64  `json:"long_task_ms,omitempty"`
	JankMS         float64  `json:"jank_ms,omitempty"`
	From           float64  `json:"from,omitempty"`
	To             float64  `json:"to,omitempty"`
	TrimStart      float64  `json:"trim_start,omitempty"`
	TrimEnd        float64  `json:"trim_end,omitempty"`
	AppURLs        []string `json:"app_url,omitempty"`
	LibraryURLs    []string `json:"library_url,omitempty"`
	Sourcemaps     []string `json:"sourcemaps,omitempty"`
}

// Loader must use model.Load for file inputs. The default loader does so and
// resolves source maps using the same resolver as the CLI.
type Loader func(context.Context, string, SourceOptions) (*model.Recording, error)

type Engine struct{ Loader Loader }

type Source struct {
	File            string              `json:"file"`
	From            float64             `json:"from"`
	To              float64             `json:"to"`
	UntimedSamples  int                 `json:"untimed_samples"`
	UntimedMs       float64             `json:"untimed_ms"`
	UntimedExcluded bool                `json:"untimed_excluded"`
	UntimedSpans    []flame.UntimedSpan `json:"untimed_spans,omitempty"`
}

// Function keeps generated identity and optional original source locations.
// Function IDs and calling-context node IDs remain stable across sample order.
type Function struct {
	ID       string          `json:"id"`
	Name     string          `json:"name"`
	URL      string          `json:"url"`
	Line     int             `json:"line"`
	Column   int             `json:"column"`
	SourceID string          `json:"source_id,omitempty"`
	Module   string          `json:"module"`
	Category string          `json:"category"`
	Original *flame.Location `json:"original,omitempty"`
}

type FunctionCost struct {
	Function
	SelfMS      float64 `json:"self_ms"`
	TotalMS     float64 `json:"total_ms"`
	SelfSamples int     `json:"self_samples"`
}

// Row is the stable union used by all operations. Unused numeric fields are zero.
// percent is a percentage, not a fraction. Call edges divide by selected
// function inclusive time. Other sampled rows divide by total sampled time.
// diff deltas are candidate minus baseline, ordered by absolute selected delta.
type Row struct {
	Function
	NodeID                 string         `json:"node_id,omitempty"`
	SelfMS                 float64        `json:"self_ms"`
	TotalMS                float64        `json:"total_ms"`
	Samples                int            `json:"samples"`
	Percent                float64        `json:"percent"`
	Depth                  int            `json:"depth"`
	Stack                  []Function     `json:"stack,omitempty"`
	BaselineSelfMS         float64        `json:"baseline_self_ms"`
	CandidateSelfMS        float64        `json:"candidate_self_ms"`
	DeltaSelfMS            float64        `json:"delta_self_ms"`
	BaselineTotalMS        float64        `json:"baseline_total_ms"`
	CandidateTotalMS       float64        `json:"candidate_total_ms"`
	DeltaTotalMS           float64        `json:"delta_total_ms"`
	Key                    string         `json:"key,omitempty"`
	Value                  float64        `json:"value"`
	Unit                   string         `json:"unit,omitempty"`
	Kind                   string         `json:"kind,omitempty"`
	Count                  int            `json:"count"`
	MeanMS                 float64        `json:"mean_ms"`
	P95MS                  float64        `json:"p95_ms"`
	MaxMS                  float64        `json:"max_ms"`
	Basis                  string         `json:"basis,omitempty"`
	TopSelf                []FunctionCost `json:"top_self,omitempty"`
	TopInclusive           []FunctionCost `json:"top_inclusive,omitempty"`
	TopSelfTotalCount      int            `json:"top_self_total_count"`
	TopInclusiveTotalCount int            `json:"top_inclusive_total_count"`
	FunctionsTruncated     bool           `json:"functions_truncated"`
}

// Result always reports complete row count before limit, even for ambiguity.
// Ambiguous selectors return candidates, never an arbitrarily selected function.
type Result struct {
	Schema               string     `json:"schema"`
	Operation            string     `json:"operation"`
	Status               string     `json:"status"`
	Sources              []Source   `json:"sources"`
	Command              []string   `json:"command"`
	Selector             string     `json:"selector,omitempty"`
	By                   string     `json:"by"`
	TotalMS              float64    `json:"total_ms"`
	TotalCount           int        `json:"total_count"`
	Limit                int        `json:"limit"`
	Truncated            bool       `json:"truncated"`
	Rows                 []Row      `json:"rows"`
	Candidates           []Function `json:"candidates,omitempty"`
	DeepestStack         []Function `json:"deepest_stack,omitempty"`
	HotPathBranches      []Row      `json:"hot_path_branches,omitempty"`
	HotPathBranchesTotal int        `json:"hot_path_branches_total,omitempty"`
	HotPathStopReason    string     `json:"hot_path_stop_reason,omitempty"`
	Errors               []Error    `json:"errors,omitempty"`
}

type Error struct {
	File    string `json:"file"`
	Message string `json:"message"`
	Code    string `json:"code"`
}

var Operations = []string{"top", "callers", "callees", "paths", "stacks", "subtree", "hot-path", "by-root", "find", "diff", "depth", "metrics", "events", "rootcause", "window"}
