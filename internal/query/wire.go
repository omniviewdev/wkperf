package query

import (
	"bytes"
	"encoding/json"

	"github.com/omniviewdev/wkperf/internal/flame"
)

// Document is the v2 transport contract. Domain rows remain convenient for
// aggregation; only the operation's concrete row type crosses the wire.
// Functions use the same captured-frame metadata as analysis and flame exports.
type Document struct {
	Schema               string                   `json:"schema"`
	Operation            string                   `json:"operation"`
	Status               string                   `json:"status"`
	Sources              []Source                 `json:"sources"`
	Command              []string                 `json:"command"`
	Selector             string                   `json:"selector,omitempty"`
	By                   string                   `json:"by,omitempty"`
	SampledTotalMS       float64                  `json:"sampled_total_ms"`
	TotalCount           int                      `json:"total_count"`
	ReturnedCount        int                      `json:"returned_count"`
	Offset               int                      `json:"offset"`
	NextOffset           *int                     `json:"next_offset,omitempty"`
	Limit                int                      `json:"limit"`
	MaxBytes             int                      `json:"max_bytes"`
	Truncated            bool                     `json:"truncated"`
	TruncatedBy          []string                 `json:"truncated_by,omitempty"`
	Rows                 []any                    `json:"rows"`
	Functions            map[string]flame.Frame   `json:"functions,omitempty"`
	Candidates           []string                 `json:"candidates,omitempty"`
	DeepestStack         []StackRef               `json:"deepest_stack,omitempty"`
	DeepestStackDepth    int                      `json:"deepest_stack_depth,omitempty"`
	HotPathBranches      []FunctionRow            `json:"hot_path_branches,omitempty"`
	HotPathBranchesTotal int                      `json:"hot_path_branches_total,omitempty"`
	HotPathStopReason    string                   `json:"hot_path_stop_reason,omitempty"`
	DiffNormalization    *flame.DiffNormalization `json:"diff_normalization,omitempty"`
	Errors               []Error                  `json:"errors,omitempty"`
}

// StackRef encodes either a dictionary ID or an explicit middle-frame omission.
// The omitted count never includes the retained root and leaf frames.
type StackRef struct {
	FunctionID string
	Elided     int
}

func (ref StackRef) MarshalJSON() ([]byte, error) {
	if ref.Elided > 0 {
		return json.Marshal(struct {
			Elided int `json:"elided"`
		}{ref.Elided})
	}
	return json.Marshal(ref.FunctionID)
}

type FunctionRow struct {
	FunctionID string  `json:"function_id"`
	NodeID     string  `json:"node_id,omitempty"`
	SelfMS     float64 `json:"self_ms"`
	TotalMS    float64 `json:"total_ms"`
	Samples    int     `json:"samples"`
	Percent    float64 `json:"percent"`
}

type EdgeRow struct {
	FunctionID string  `json:"function_id"`
	NodeID     string  `json:"node_id,omitempty"`
	TotalMS    float64 `json:"total_ms"`
	Samples    int     `json:"samples"`
	Percent    float64 `json:"percent"`
}

type PathRow struct {
	FunctionRow
	Depth      int        `json:"depth"`
	Stack      []StackRef `json:"stack"`
	StackDepth int        `json:"stack_depth"`
}

type HotPathRow struct {
	FunctionRow
	Depth int `json:"depth"`
}

type DepthRow struct {
	Depth   int     `json:"depth"`
	SelfMS  float64 `json:"self_ms"`
	TotalMS float64 `json:"total_ms"`
	Samples int     `json:"samples"`
	Percent float64 `json:"percent"`
}

type DiffRow struct {
	FunctionID       string  `json:"function_id"`
	BaselineSelfMS   float64 `json:"baseline_self_ms"`
	CandidateSelfMS  float64 `json:"candidate_self_ms"`
	DeltaSelfMS      float64 `json:"delta_self_ms"`
	BaselineTotalMS  float64 `json:"baseline_total_ms"`
	CandidateTotalMS float64 `json:"candidate_total_ms"`
	DeltaTotalMS     float64 `json:"delta_total_ms"`
	DeltaSamples     int     `json:"delta_samples"`
}

type ValueRow struct {
	Key   string  `json:"key"`
	Value float64 `json:"value"`
	Unit  string  `json:"unit"`
}

type RootCauseRow struct {
	ValueRow
	Basis string `json:"basis,omitempty"`
}

type KeyRow struct {
	Key         string `json:"key"`
	Operation   string `json:"op"`
	Unit        string `json:"unit"`
	Description string `json:"description"`
	Basis       string `json:"basis"`
	AliasOf     string `json:"alias_of,omitempty"`
}

type CostRow struct {
	FunctionID  string  `json:"function_id"`
	SelfMS      float64 `json:"self_ms"`
	TotalMS     float64 `json:"total_ms"`
	SelfSamples int     `json:"self_samples"`
}

type EventRow struct {
	Kind                   string    `json:"kind"`
	Name                   string    `json:"name"`
	Count                  int       `json:"count"`
	TotalMS                float64   `json:"total_ms"`
	MeanMS                 float64   `json:"mean_ms"`
	P95MS                  float64   `json:"p95_ms"`
	MaxMS                  float64   `json:"max_ms"`
	TopSelf                []CostRow `json:"top_self"`
	TopInclusive           []CostRow `json:"top_inclusive"`
	TopSelfTotalCount      int       `json:"top_self_total_count"`
	TopInclusiveTotalCount int       `json:"top_inclusive_total_count"`
	FunctionsTruncated     bool      `json:"functions_truncated"`
}

// Wire prepares the sparse document without changing domain data. Its frame
// dictionary contains only IDs referenced by retained rows and detail previews.
func Wire(result *Result) Document {
	out := Document{
		Schema: Schema, Operation: result.Operation, Status: result.Status,
		Sources: result.Sources, Command: result.Command, Selector: result.Selector,
		SampledTotalMS: result.TotalMS, TotalCount: result.TotalCount,
		ReturnedCount: result.ReturnedCount, Offset: result.Offset, NextOffset: result.NextOffset,
		Limit: result.Limit, MaxBytes: result.MaxBytes, Truncated: result.Truncated,
		TruncatedBy: result.TruncatedBy, Rows: make([]any, 0, len(result.Rows)),
		HotPathBranchesTotal: result.HotPathBranchesTotal,
		HotPathStopReason:    result.HotPathStopReason, DiffNormalization: result.DiffNormalization,
		Errors: result.Errors,
	}
	if out.Sources == nil {
		out.Sources = []Source{}
	}
	if out.Command == nil {
		out.Command = []string{}
	}
	if result.Operation == "top" || result.Operation == "diff" {
		out.By = result.By
	}
	add := func(f Function) string {
		if f.ID == "" {
			return ""
		}
		if _, exists := out.Functions[f.ID]; !exists {
			if out.Functions == nil {
				out.Functions = make(map[string]flame.Frame)
			}
			out.Functions[f.ID] = flame.Frame{ID: f.ID, Name: f.Name, URL: f.URL, Line: f.Line, Column: f.Column, SourceID: f.SourceID, Native: f.Native, Anonymous: f.Anonymous, Category: flame.Category(f.Category), Original: f.Original}
		}
		return f.ID
	}
	stack := func(frames []Function) []StackRef {
		head, tail, omitted := previewFrames(frames, stackLimit(result))
		size := len(head) + len(tail)
		if omitted > 0 {
			size++
		}
		refs := make([]StackRef, 0, size)
		for _, f := range head {
			refs = append(refs, StackRef{FunctionID: add(f)})
		}
		if omitted > 0 {
			refs = append(refs, StackRef{Elided: omitted})
		}
		for _, f := range tail {
			refs = append(refs, StackRef{FunctionID: add(f)})
		}
		return refs
	}
	costs := func(values []FunctionCost) []CostRow {
		rows := make([]CostRow, len(values))
		for i, value := range values {
			rows[i] = CostRow{add(value.Function), value.SelfMS, value.TotalMS, value.SelfSamples}
		}
		return rows
	}
	for _, row := range result.Rows {
		functionRow := func() FunctionRow {
			return FunctionRow{add(row.Function), row.NodeID, row.SelfMS, row.TotalMS, row.Samples, row.Percent}
		}
		switch result.Operation {
		case "metrics":
			out.Rows = append(out.Rows, ValueRow{row.Key, row.Value, row.Unit})
		case "rootcause":
			out.Rows = append(out.Rows, RootCauseRow{ValueRow{row.Key, row.Value, row.Unit}, row.Basis})
		case "keys":
			out.Rows = append(out.Rows, KeyRow{row.Key, row.Operation, row.Unit, row.Description, row.Basis, row.AliasOf})
		case "events":
			out.Rows = append(out.Rows, EventRow{row.Kind, row.Name, row.Count, row.TotalMS, row.MeanMS, row.P95MS, row.MaxMS, costs(row.TopSelf), costs(row.TopInclusive), row.TopSelfTotalCount, row.TopInclusiveTotalCount, row.FunctionsTruncated})
		case "diff":
			out.Rows = append(out.Rows, DiffRow{add(row.Function), row.BaselineSelfMS, row.CandidateSelfMS, row.DeltaSelfMS, row.BaselineTotalMS, row.CandidateTotalMS, row.DeltaTotalMS, row.Samples})
		case "depth":
			out.Rows = append(out.Rows, DepthRow{row.Depth, row.SelfMS, row.TotalMS, row.Samples, row.Percent})
		case "callers", "callees":
			out.Rows = append(out.Rows, EdgeRow{add(row.Function), row.NodeID, row.TotalMS, row.Samples, row.Percent})
		case "paths", "stacks", "subtree":
			out.Rows = append(out.Rows, PathRow{functionRow(), row.Depth, stack(row.Stack), len(row.Stack)})
		case "hot-path":
			out.Rows = append(out.Rows, HotPathRow{functionRow(), row.Depth})
		default:
			out.Rows = append(out.Rows, functionRow())
		}
	}
	for _, f := range result.Candidates {
		out.Candidates = append(out.Candidates, add(f))
	}
	if len(result.DeepestStack) > 0 {
		out.DeepestStack, out.DeepestStackDepth = stack(result.DeepestStack), len(result.DeepestStack)
	}
	for _, row := range result.HotPathBranches {
		out.HotPathBranches = append(out.HotPathBranches, FunctionRow{add(row.Function), row.NodeID, row.SelfMS, row.TotalMS, row.Samples, row.Percent})
	}
	return out
}

// MarshalJSON deliberately has no inverse compatibility decoder. Consumers of
// the breaking v2 contract must use its operation-specific rows and dictionary.
func (result *Result) MarshalJSON() ([]byte, error) {
	var out bytes.Buffer
	encoder := json.NewEncoder(&out)
	encoder.SetEscapeHTML(false)
	if err := encoder.Encode(Wire(result)); err != nil {
		return nil, err
	}
	return bytes.TrimSuffix(out.Bytes(), []byte{'\n'}), nil
}

func stackLimit(result *Result) int {
	if result.MaxStackFrames == 0 && !result.MaxStackFramesSet {
		return DefaultMaxStackFrames
	}
	return result.MaxStackFrames
}

// previewFrames retains eight root-most and forty leaf-most frames at the
// default limit. Smaller byte-driven previews still retain the terminal frame.
func previewFrames(frames []Function, limit int) ([]Function, []Function, int) {
	if limit == 0 || len(frames) <= limit {
		return frames, nil, 0
	}
	root := min(8, limit/6)
	if limit > 1 && root == 0 {
		root = 1
	}
	return frames[:root], frames[len(frames)-(limit-root):], len(frames) - limit
}
