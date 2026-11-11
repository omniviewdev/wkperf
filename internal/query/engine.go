package query

import (
	"context"
	"errors"
	"fmt"
	"math"
	"strconv"

	"github.com/omniviewdev/wkperf/internal/diagnostic"
	"github.com/omniviewdev/wkperf/internal/flame"
	"github.com/omniviewdev/wkperf/internal/profile"
)

func normalize(req Request) (Request, error) {
	if req.Operation == "window" {
		if req.To <= req.From {
			return req, diagnostic.Wrap(diagnostic.WindowError, req.File, fmt.Errorf("window end must be after window start"))
		}
		req.Operation = req.QueryOperation
		if req.Operation == "" {
			req.Operation = "top"
		}
		if req.Operation == "window" {
			return req, fmt.Errorf("nested window operation is not supported")
		}
	}
	known := false
	for _, op := range Operations {
		if op == req.Operation {
			known = true
			break
		}
	}
	if !known {
		return req, fmt.Errorf("unknown query operation %q", req.Operation)
	}
	if req.Limit < 0 || req.Limit > MaxLimit {
		return req, fmt.Errorf("limit must be between 1 and %d (0 uses %d)", MaxLimit, DefaultLimit)
	}
	if req.Limit == 0 {
		req.Limit = DefaultLimit
	}
	if req.Offset < 0 {
		return req, fmt.Errorf("offset must not be negative")
	}
	if req.MaxBytes < 0 || req.MaxBytes > MaxBytes {
		return req, fmt.Errorf("max_bytes must be between 1 and %d (0 uses %d)", MaxBytes, DefaultMaxBytes)
	}
	if req.MaxBytes == 0 {
		req.MaxBytes = DefaultMaxBytes
	}
	if req.MaxStackFrames < 0 {
		return req, fmt.Errorf("max_stack_frames must not be negative")
	}
	if req.MaxStackFrames == 0 && !req.MaxStackFramesSet {
		req.MaxStackFrames = DefaultMaxStackFrames
	}
	mode, err := flame.ParseDiffMode(req.Normalize)
	if err != nil {
		return req, err
	}
	req.Normalize = string(mode)
	if req.RootCauseTop < 0 {
		return req, fmt.Errorf("top must not be negative (0 uses 10)")
	}
	if req.RootCauseTop == 0 {
		req.RootCauseTop = 10
	}
	if req.Depth < 0 {
		return req, fmt.Errorf("depth must not be negative")
	}
	if req.Depth == 0 && !req.DepthSet {
		req.Depth = 8
	}
	if req.By == "" {
		req.By = "self"
	}
	if req.By != "self" && req.By != "total" {
		return req, fmt.Errorf("by must be self or total")
	}
	switch req.Category {
	case "", "app", "framework", "native", "gc", "unknown-bundled", "injected":
	default:
		return req, fmt.Errorf("category must be app, framework, native, gc, unknown-bundled, or injected")
	}
	if err := req.ownership().Validate(); err != nil {
		return req, fmt.Errorf("ownership URL patterns: %w", err)
	}
	for _, bound := range []struct {
		name  string
		value float64
	}{{"from", req.From}, {"to", req.To}, {"trim_start", req.TrimStart}, {"trim_end", req.TrimEnd}, {"min_ms", req.MinMS}, {"min_share", req.MinShare}, {"long_task_ms", req.LongTaskMS}, {"jank_ms", req.JankMS}} {
		if math.IsNaN(bound.value) || math.IsInf(bound.value, 0) || bound.value < 0 {
			return req, fmt.Errorf("%s must be finite and nonnegative", bound.name)
		}
	}
	if req.MinShare > 1 {
		return req, fmt.Errorf("min_share must be between zero and one")
	}
	if req.MinShare == 0 && !req.MinShareSet {
		req.MinShare = .05
	}
	if req.MinSamples < 0 {
		return req, fmt.Errorf("min_samples must not be negative")
	}
	if req.MinSamples == 0 && !req.MinSamplesSet {
		req.MinSamples = 5
	}
	if math.IsNaN(req.TieRatio) || math.IsInf(req.TieRatio, 0) || req.TieRatio < 0 || req.TieRatio > 1 {
		return req, fmt.Errorf("tie_ratio must be between zero and one")
	}
	if req.TieRatio == 0 && !req.TieRatioSet {
		req.TieRatio = .8
	}
	if req.LongTaskMS == 0 {
		req.LongTaskMS = 50
	}
	if req.JankMS == 0 {
		req.JankMS = 16.7
	}
	if req.To > 0 && req.To <= math.Max(req.From, req.TrimStart) {
		return req, diagnostic.Wrap(diagnostic.WindowError, req.File, fmt.Errorf("window end must be after window start"))
	}
	if req.Operation == "diff" {
		if req.Baseline == "" {
			req.Baseline = req.File
		}
		if req.Baseline == "" || req.Candidate == "" {
			return req, fmt.Errorf("diff requires baseline and candidate files")
		}
	} else if req.File == "" {
		return req, fmt.Errorf("recording file is required")
	}
	return req, nil
}

// Run executes the same bounded query used by CLI and MCP. Input paths are
// opened locally through model.Load; no shell or subprocess is involved.
func (e *Engine) Run(ctx context.Context, request Request) (*Result, error) {
	req, err := normalize(request)
	if err != nil {
		return nil, diagnostic.Wrap(diagnostic.UsageError, "", err)
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	result := &Result{Schema: Schema, Operation: req.Operation, Status: "ok", Sources: []Source{}, Command: command(req), Selector: req.Selector, By: req.By, Limit: req.Limit, Offset: req.Offset, Rows: []Row{}}
	if req.Operation == "diff" {
		_, a, sourceA, err := e.load(ctx, req.Baseline, req)
		if err != nil {
			return nil, err
		}
		_, b, sourceB, err := e.load(ctx, req.Candidate, req)
		if err != nil {
			return nil, err
		}
		result.Sources = []Source{sourceA, sourceB}
		result.TotalMS = clean(b.Root.TotalMs)
		normalization, err := flame.ResolveDiffNormalization(a, b, flame.DiffMode(req.Normalize))
		if err != nil {
			return nil, diagnostic.Wrap(diagnostic.UsageError, "", err)
		}
		result.DiffNormalization = &normalization
		result.Rows = diffRows(a, b, req, normalization)
	} else {
		rec, tree, source, err := e.load(ctx, req.File, req)
		if err != nil {
			return nil, err
		}
		result.Sources = []Source{source}
		result.TotalMS = clean(tree.Root.TotalMs)
		switch req.Operation {
		case "top", "find":
			result.Rows, err = functionRows(tree, req)
		case "callers", "callees", "paths", "subtree":
			result.Rows, err = selectedRows(tree, req)
		case "stacks":
			result.Rows = pathRows(tree, nil)
			if req.Selector != "" {
				filtered := []Row{}
				for _, row := range result.Rows {
					if row.NodeID == req.Selector {
						filtered = append(filtered, row)
					}
				}
				result.Rows = filtered
				if len(result.Rows) == 0 {
					err = fmt.Errorf("unknown stack ID %q: stacks requires a node_id from paths or stacks output; use paths FUNCTION to search by function name", req.Selector)
				}
			}
		case "hot-path":
			path := tree.SupportedHotPath(req.MinShare, req.MinSamples, req.TieRatio)
			for depth, n := range path.Nodes {
				row := nodeRow(n, tree.Root.TotalMs)
				row.Depth = depth
				result.Rows = append(result.Rows, row)
			}
			result.HotPathStopReason = path.StopReason
			result.HotPathBranchesTotal = len(path.Branches)
			for _, branch := range path.Branches[:min(len(path.Branches), req.Limit)] {
				result.HotPathBranches = append(result.HotPathBranches, nodeRow(branch, tree.Root.TotalMs))
			}
		case "by-root":
			for _, n := range tree.Root.Children {
				result.Rows = append(result.Rows, nodeRow(n, tree.Root.TotalMs))
			}
			sortRows(result.Rows, "total")
		case "depth":
			result.Rows, result.DeepestStack = depthRows(tree)
		case "metrics", "rootcause", "keys":
			result.Rows, err = keyRows(rec, tree, source, req)
		case "events":
			clipped := profile.Clip(rec, rec.StartTime+source.From, rec.StartTime+source.To)
			result.Rows = triggerRows(clipped, len(tree.Functions()), req)
		}
		if err != nil {
			var ambiguous *AmbiguousError
			if !errors.As(err, &ambiguous) {
				return nil, diagnostic.Wrap(diagnostic.SelectorError, req.File, err)
			}
			result.Status = "ambiguous"
			result.Candidates = ambiguous.Candidates
			result.Rows = []Row{}
			result.Errors = []Error{{File: req.File, Code: diagnostic.AmbiguousSelector, Message: "ambiguous selector; choose an ID from returned candidates"}}
		}
	}
	for i := range result.Rows {
		if result.Rows[i].Operation == "" {
			result.Rows[i].Operation = req.Operation
		}
	}
	if result.Status == "ambiguous" {
		result.TotalCount = len(result.Candidates)
		start := min(req.Offset, result.TotalCount)
		end := start + min(req.Limit, result.TotalCount-start)
		result.Candidates = result.Candidates[start:end]
		result.ReturnedCount = len(result.Candidates)
	} else {
		result.TotalCount = len(result.Rows)
		start := min(req.Offset, result.TotalCount)
		end := start + min(req.Limit, result.TotalCount-start)
		result.Rows = result.Rows[start:end]
		result.ReturnedCount = len(result.Rows)
	}
	if req.Offset+result.ReturnedCount < result.TotalCount {
		next := req.Offset + result.ReturnedCount
		result.NextOffset = &next
		result.Truncated = true
		result.TruncatedBy = []string{"limit"}
	}
	return Finalize(result, req)
}

func clean(value float64) float64 {
	if math.IsNaN(value) || math.IsInf(value, 0) {
		return 0
	}
	if math.Abs(value) > math.MaxFloat64/1e9 {
		return value
	}
	return math.Round(value*1e9) / 1e9
}
func percent(value, total float64) float64 {
	if total <= 0 {
		return 0
	}
	return clean(100 * value / total)
}

func command(req Request) []string {
	file := req.File
	if req.Operation == "diff" {
		file = req.Baseline
	}
	out := []string{"wkperf", "query", file, req.Operation}
	if req.Operation == "diff" {
		out = append(out, req.Candidate)
	} else if req.Selector != "" {
		out = append(out, req.Selector)
	}
	out = append(out, "--format", "json", "--limit", strconv.Itoa(req.Limit))
	if req.Offset != 0 {
		out = append(out, "--offset", strconv.Itoa(req.Offset))
	}
	if req.MaxBytes != DefaultMaxBytes {
		out = append(out, "--max-bytes", strconv.Itoa(req.MaxBytes))
	}
	if req.MaxStackFramesSet || req.MaxStackFrames != DefaultMaxStackFrames {
		out = append(out, "--max-stack-frames", strconv.Itoa(req.MaxStackFrames))
	}
	if req.Operation == "diff" {
		out = append(out, "--normalize", req.Normalize)
	}
	if req.By != "self" {
		out = append(out, "--by", req.By)
	}
	if req.Module != "" {
		out = append(out, "--module", req.Module)
	}
	if req.Category != "" {
		out = append(out, "--category", req.Category)
	}
	for _, flag := range []struct {
		name  string
		value float64
	}{{"from", req.From}, {"to", req.To}, {"trim-start", req.TrimStart}, {"trim-end", req.TrimEnd}, {"min-ms", req.MinMS}} {
		if flag.value != 0 {
			out = append(out, "--"+flag.name, strconv.FormatFloat(flag.value, 'g', -1, 64))
		}
	}
	if req.Operation == "subtree" {
		out = append(out, "--depth", strconv.Itoa(req.Depth))
	}
	if req.Operation == "hot-path" {
		out = append(out, "--min-share", strconv.FormatFloat(req.MinShare, 'g', -1, 64))
		out = append(out, "--min-samples", strconv.Itoa(req.MinSamples), "--tie-ratio", strconv.FormatFloat(req.TieRatio, 'g', -1, 64))
	}
	if (req.Operation == "rootcause" || req.Operation == "keys") && req.RootCauseTop != 10 {
		out = append(out, "--top", strconv.Itoa(req.RootCauseTop))
	}
	if req.LongTaskMS != 50 {
		out = append(out, "--long-task-ms", strconv.FormatFloat(req.LongTaskMS, 'g', -1, 64))
	}
	if req.JankMS != 16.7 {
		out = append(out, "--jank-ms", strconv.FormatFloat(req.JankMS, 'g', -1, 64))
	}
	for _, sourceMap := range req.Sourcemaps {
		out = append(out, "--sourcemap", sourceMap)
	}
	for _, pattern := range req.AppURLs {
		out = append(out, "--app-url", pattern)
	}
	for _, pattern := range req.LibraryURLs {
		out = append(out, "--library-url", pattern)
	}
	return out
}

func (req Request) ownership() flame.OwnershipRules {
	return flame.OwnershipRules{AppURLs: req.AppURLs, LibraryURLs: req.LibraryURLs}
}
