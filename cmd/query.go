package cmd

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"github.com/spf13/cobra"
	"github.com/spf13/viper"

	"github.com/omniviewdev/wkperf/internal/model"
	"github.com/omniviewdev/wkperf/internal/query"
)

func newQueryCmd() *cobra.Command {
	c := &cobra.Command{
		Use:   "query FILE OP [ARGS...]",
		Short: "Query weighted functions, call paths, windows, and scalar measurements",
		Long:  "Query a local recording. Operations: " + strings.Join(query.Operations, ", ") + ". Use window FROM TO [OP [ARGS...]] for a recording-relative interval. Select functions by name, url:line:column, /regex/, or stable function/node ID. Ambiguous names return candidates, never guesses.",
		Args:  cobra.MinimumNArgs(2),
		PreRunE: func(cmd *cobra.Command, _ []string) error {
			if err := bindFlags(cmd, windowKeys...); err != nil {
				return err
			}
			return bindSourceMapFlags(cmd)
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			cmd.SilenceUsage = true
			format, _ := cmd.Flags().GetString("format")
			if format != "json" && format != "md" && format != "tsv" {
				return fmt.Errorf("unknown --format %q (want json|md|tsv)", format)
			}
			req, err := parseQueryArgs(args)
			if err != nil {
				return err
			}
			req.Limit, _ = cmd.Flags().GetInt("limit")
			req.By, _ = cmd.Flags().GetString("by")
			req.RootCauseTop, _ = cmd.Flags().GetInt("top")
			req.Module, _ = cmd.Flags().GetString("module")
			req.Category, _ = cmd.Flags().GetString("category")
			req.Depth, _ = cmd.Flags().GetInt("depth")
			req.MinMS, _ = cmd.Flags().GetFloat64("min-ms")
			req.MinShare, _ = cmd.Flags().GetFloat64("min-share")
			req.DepthSet, req.MinShareSet = cmd.Flags().Changed("depth"), cmd.Flags().Changed("min-share")
			req.MinSamples, _ = cmd.Flags().GetInt("min-samples")
			req.MinSamplesSet = cmd.Flags().Changed("min-samples")
			req.TieRatio, _ = cmd.Flags().GetFloat64("tie-ratio")
			req.TieRatioSet = cmd.Flags().Changed("tie-ratio")
			req.LongTaskMS, _ = cmd.Flags().GetFloat64("long-task-ms")
			req.JankMS, _ = cmd.Flags().GetFloat64("jank-ms")
			window := windowOpts()
			req.TrimStart, req.TrimEnd = window.TrimStartSec, window.TrimEndSec
			if args[1] != "window" {
				req.From, req.To = window.FromSec, window.ToSec
			} else {
				if cmd.Flags().Changed("from") || cmd.Flags().Changed("to") {
					return fmt.Errorf("window FROM TO cannot be combined with --from or --to")
				}
			}
			req.Sourcemaps = viper.GetStringSlice("sourcemap")
			rules := ownershipRules()
			req.AppURLs, req.LibraryURLs = rules.AppURLs, rules.LibraryURLs
			engine := &query.Engine{Loader: func(ctx context.Context, file string, _ query.SourceOptions) (*model.Recording, error) {
				if err := ctx.Err(); err != nil {
					return nil, err
				}
				rec, _, err := loadRecording(cmd, file)
				return rec, err
			}}
			result, err := engine.Run(cmd.Context(), req)
			if err != nil {
				return err
			}
			if err := query.Render(cmd.OutOrStdout(), result, format); err != nil {
				return err
			}
			if result.Status == "ambiguous" {
				return &ExitError{Code: 1, Msg: "ambiguous selector; choose an ID from returned candidates"}
			}
			return nil
		},
	}
	c.Flags().String("format", "json", "output format: json|md|tsv")
	c.Flags().Int("limit", query.DefaultLimit, "maximum rows (1..1000; 0 uses bounded default 50)")
	c.Flags().Int("top", 10, "root-cause detail size, separate from row limit (0 uses 10)")
	c.Flags().String("by", "self", "rank or diff by self|total milliseconds")
	c.Flags().String("module", "", "top/diff module URL substring filter")
	c.Flags().String("category", "", "top/diff owner filter: app|framework|native|gc|unknown-bundled|injected")
	c.Flags().Int("depth", 8, "maximum subtree depth (0 returns only selected contexts)")
	c.Flags().Float64("min-ms", 0, "minimum subtree inclusive milliseconds")
	c.Flags().Float64("min-share", .05, "minimum hot-path fraction of sampled time")
	c.Flags().Int("min-samples", 5, "minimum supported hot-path node samples (0 disables support gate)")
	c.Flags().Float64("tie-ratio", .8, "stop before competing hot-path branches at this second/best ratio (0 disables)")
	c.Flags().Float64("long-task-ms", 50, "maximal script-task threshold for derived evidence (fixed scorecard standards remain 50 ms)")
	c.Flags().Float64("jank-ms", 16.7, "slow active-frame/finding threshold for derived evidence (fixed frame standards remain 16.7 ms)")
	addWindowFlags(c)
	addSourceMapFlags(c)
	return c
}

func parseQueryArgs(args []string) (query.Request, error) {
	var req query.Request
	if len(args) < 2 {
		return req, fmt.Errorf("query requires FILE OP [ARGS...]")
	}
	if args[0] == "diff" && len(args) == 3 {
		return query.Request{Operation: "diff", File: args[1], Baseline: args[1], Candidate: args[2]}, nil
	}
	req.File, req.Operation = args[0], args[1]
	rest := args[2:]
	if req.Operation == "window" {
		if len(rest) < 2 {
			return req, fmt.Errorf("window requires FROM TO [OP [ARGS...]]")
		}
		var err error
		req.From, err = strconv.ParseFloat(rest[0], 64)
		if err != nil {
			return req, fmt.Errorf("invalid window FROM %q: %w", rest[0], err)
		}
		req.To, err = strconv.ParseFloat(rest[1], 64)
		if err != nil {
			return req, fmt.Errorf("invalid window TO %q: %w", rest[1], err)
		}
		if req.To <= req.From {
			return req, fmt.Errorf("window TO must be after FROM")
		}
		rest = rest[2:]
		req.Operation = "top"
		if len(rest) > 0 {
			req.Operation, rest = rest[0], rest[1:]
		}
	}
	if req.Operation == "diff" {
		if len(rest) == 1 {
			req.Baseline, req.Candidate = req.File, rest[0]
			return req, nil
		}
		if len(rest) == 2 {
			req.Baseline, req.Candidate = rest[0], rest[1]
			return req, nil
		}
		return req, fmt.Errorf("diff requires a candidate file: query BASELINE diff CANDIDATE")
	}
	needsSelector := req.Operation == "callers" || req.Operation == "callees" || req.Operation == "paths" || req.Operation == "subtree" || req.Operation == "find"
	allowsSelector := needsSelector || req.Operation == "top" || req.Operation == "stacks" || req.Operation == "metrics" || req.Operation == "events" || req.Operation == "rootcause"
	if needsSelector && len(rest) != 1 {
		return req, fmt.Errorf("%s requires one selector", req.Operation)
	}
	if len(rest) > 1 || (len(rest) > 0 && !allowsSelector) {
		return req, fmt.Errorf("unexpected arguments for %s", req.Operation)
	}
	if len(rest) == 1 {
		req.Selector = rest[0]
	}
	return req, nil
}
