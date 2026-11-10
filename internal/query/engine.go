package query

import (
	"context"
	"errors"
	"fmt"
	"math"
	"sort"
	"strconv"

	"github.com/omniviewdev/wkperf/internal/analyze"
	"github.com/omniviewdev/wkperf/internal/flame"
	"github.com/omniviewdev/wkperf/internal/insights"
	"github.com/omniviewdev/wkperf/internal/model"
	"github.com/omniviewdev/wkperf/internal/profile"
	"github.com/omniviewdev/wkperf/internal/rootcause"
	"github.com/omniviewdev/wkperf/internal/sourcemap"
)

func normalize(req Request) (Request, error) {
	if req.Operation == "window" {
		if req.To <= req.From {
			return req, fmt.Errorf("window end must be after window start")
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
	if req.Category != "" && req.Category != "app" && req.Category != "framework" && req.Category != "native" && req.Category != "gc" {
		return req, fmt.Errorf("category must be app, framework, native, or gc")
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
	if req.LongTaskMS == 0 {
		req.LongTaskMS = 50
	}
	if req.JankMS == 0 {
		req.JankMS = 16.7
	}
	if req.To > 0 && req.To <= math.Max(req.From, req.TrimStart) {
		return req, fmt.Errorf("window end must be after window start")
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

func defaultLoad(ctx context.Context, file string, opts SourceOptions) (*model.Recording, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	rec, err := model.Load(file)
	if err != nil {
		return nil, err
	}
	if _, err := sourcemap.Resolve(rec, opts.Sourcemaps, file); err != nil {
		return nil, err
	}
	return rec, nil
}

func (e *Engine) load(ctx context.Context, file string, req Request) (*model.Recording, *flame.Tree, Source, error) {
	loader := e.Loader
	if loader == nil {
		loader = defaultLoad
	}
	rec, err := loader(ctx, file, SourceOptions{Sourcemaps: req.Sourcemaps})
	if err != nil {
		return nil, nil, Source{}, fmt.Errorf("%s: %w", file, err)
	}
	if rec == nil {
		return nil, nil, Source{}, fmt.Errorf("%s: loader returned no recording", file)
	}
	from, to := math.Max(req.From, req.TrimStart), rec.EndTime-rec.StartTime-req.TrimEnd
	if req.To > 0 {
		to = math.Min(to, req.To)
	}
	if math.IsNaN(to) || math.IsInf(to, 0) || to < from || (to == from && (from > 0 || req.To > 0 || req.TrimEnd > 0)) {
		return nil, nil, Source{}, fmt.Errorf("%s: selected window is empty", file)
	}
	tree := flame.Build(rec)
	if from > 0 || to < rec.EndTime-rec.StartTime {
		tree = tree.Window(from, to)
	}
	return rec, tree, Source{File: file, From: from, To: to}, nil
}

// Run executes the same bounded query used by CLI and MCP. Input paths are
// opened locally through model.Load; no shell or subprocess is involved.
func (e *Engine) Run(ctx context.Context, request Request) (*Result, error) {
	req, err := normalize(request)
	if err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	result := &Result{Schema: Schema, Operation: req.Operation, Status: "ok", Sources: []Source{}, Command: command(req), Selector: req.Selector, By: req.By, Limit: req.Limit, Rows: []Row{}}
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
		result.Rows = diffRows(a, b, req)
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
					err = fmt.Errorf("unknown stack ID %q", req.Selector)
				}
			}
		case "hot-path":
			for depth, n := range tree.HotPath(req.MinShare) {
				row := nodeRow(n, tree.Root.TotalMs)
				row.Depth = depth
				result.Rows = append(result.Rows, row)
			}
		case "by-root":
			for _, n := range tree.Root.Children {
				result.Rows = append(result.Rows, nodeRow(n, tree.Root.TotalMs))
			}
			sortRows(result.Rows, "total")
		case "depth":
			result.Rows, result.DeepestStack = depthRows(tree)
		case "metrics":
			options := analyze.Options{FromSec: req.From, ToSec: req.To, TrimStartSec: req.TrimStart, TrimEndSec: req.TrimEnd}
			for _, m := range analyze.SummarizeWith(rec, req.File, options).Metrics {
				if req.Selector == "" || req.Selector == m.Key {
					result.Rows = append(result.Rows, Row{Key: m.Key, Value: clean(m.Value), Unit: string(m.Unit)})
				}
			}
			seen := map[string]bool{}
			for _, row := range result.Rows {
				seen[row.Key] = true
			}
			clipped := profile.Clip(rec, rec.StartTime+source.From, rec.StartTime+source.To)
			report := insights.Analyze(clipped, req.File, insights.Options{Top: MaxLimit, JankMs: req.JankMS, LongTaskMs: req.LongTaskMS, WindowStartSec: source.From, Sourcemaps: req.Sourcemaps})
			for key, value := range insights.NumericValues(report) {
				if !seen[key] && (req.Selector == "" || req.Selector == key) {
					result.Rows = append(result.Rows, Row{Key: key, Value: clean(value.Value), Unit: string(value.Unit)})
				}
			}
			sort.Slice(result.Rows, func(i, j int) bool { return result.Rows[i].Key < result.Rows[j].Key })
			if req.Selector != "" && len(result.Rows) == 0 {
				err = fmt.Errorf("unknown metric key %q", req.Selector)
			}
		case "events":
			clipped := profile.Clip(rec, rec.StartTime+source.From, rec.StartTime+source.To)
			result.Rows = triggerRows(clipped, len(tree.Functions()), req)
		case "rootcause":
			report := rootcause.Build(rec, tree, rootcause.Options{Source: req.File, FromSec: source.From, ToSec: source.To, Top: req.RootCauseTop, LongTaskMs: req.LongTaskMS, JankMs: req.JankMS, Sourcemaps: req.Sourcemaps})
			for _, value := range rootcause.Stats(report) {
				if req.Selector == "" || req.Selector == value.Key {
					result.Rows = append(result.Rows, Row{Key: value.Key, Value: clean(value.Value), Unit: value.Unit, Basis: value.Basis})
				}
			}
			sort.Slice(result.Rows, func(i, j int) bool { return result.Rows[i].Key < result.Rows[j].Key })
			if req.Selector != "" && len(result.Rows) == 0 {
				err = fmt.Errorf("unknown rootcause statistic key %q", req.Selector)
			}
		}
		if err != nil {
			var ambiguous *AmbiguousError
			if !errors.As(err, &ambiguous) {
				return nil, err
			}
			result.Status = "ambiguous"
			result.Candidates = ambiguous.Candidates
			result.Rows = []Row{}
		}
	}
	if result.Status == "ambiguous" {
		result.TotalCount = len(result.Candidates)
		result.Truncated = result.TotalCount > req.Limit
		if result.Truncated {
			result.Candidates = result.Candidates[:req.Limit]
		}
	} else {
		result.TotalCount = len(result.Rows)
		result.Truncated = result.TotalCount > req.Limit
		if result.Truncated {
			result.Rows = result.Rows[:req.Limit]
		}
	}
	return result, nil
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
	}
	if req.Operation == "rootcause" && req.RootCauseTop != 10 {
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
	return out
}
