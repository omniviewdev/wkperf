package cmd

import (
	"errors"
	"fmt"
	"io"
	"math"
	"sort"
	"strings"

	"github.com/spf13/cobra"
	"github.com/spf13/viper"

	"github.com/omniviewdev/wkperf/internal/analyze"
	core "github.com/omniviewdev/wkperf/internal/flame"
	"github.com/omniviewdev/wkperf/internal/model"
	"github.com/omniviewdev/wkperf/internal/profile"
	"github.com/omniviewdev/wkperf/internal/query"
	flamereport "github.com/omniviewdev/wkperf/internal/report/flame"
)

func newFlameCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "flame FILE...",
		Short: "Render duration-weighted flame graphs and profile exports",
		Long:  "Render each WebKit recording separately. HTML is self-contained and interactive. --diff A B compares B minus A by generated calling-context identity. Folded uses integer microseconds; spaces and backslashes stay literal, while percent, separators and ASCII controls use percent encoding. Folded and speedscope stacks stay canonical root-to-leaf; --invert and --min-ms affect visual layouts only.",
		Args:  cobra.MinimumNArgs(1),
		PreRunE: func(cmd *cobra.Command, args []string) error {
			if err := bindFlags(cmd, "format", "out", "focus", "invert", "min-ms", "around-long-task", "diff", "normalize"); err != nil {
				return err
			}
			if err := bindFlags(cmd, windowKeys...); err != nil {
				return err
			}
			return bindSourceMapFlags(cmd)
		},
		RunE: runFlameCommand,
	}
	f := cmd.Flags()
	f.String("format", "html", "output format: html|svg|folded|speedscope|json")
	f.String("out", "", "output path (- = stdout; HTML default wkperf-flame.html, other formats stdout)")
	f.String("focus", "", "re-root at one function/node selector: ID, exact name, file:line:column, or /regex/")
	f.Bool("invert", false, "show self-to-callers visual tree instead of root-to-leaf")
	f.Float64("min-ms", 0, "hide visual contexts below MS without changing sample weights")
	f.Int("around-long-task", 0, "restrict to Nth longest script task over 50 ms (1-based; 0 = disabled)")
	f.Bool("diff", false, "compare exactly two recordings: current minus baseline")
	f.String("normalize", "auto", "diff normalization: auto|none|per-second|share (auto uses ms/s when active durations differ by more than 10%)")
	addWindowFlags(cmd)
	addSourceMapFlags(cmd)
	return cmd
}

func runFlameCommand(cmd *cobra.Command, args []string) error {
	format := formatName(viper.GetString("format"))
	switch format {
	case "html", "svg", "folded", "speedscope", "json":
	default:
		return fmt.Errorf("unknown --format %q (want html|svg|folded|speedscope|json)", format)
	}
	opts := flamereport.Options{Invert: viper.GetBool("invert"), MinMS: viper.GetFloat64("min-ms")}
	if !flameFinite(opts.MinMS) || opts.MinMS < 0 {
		return fmt.Errorf("--min-ms must be finite and nonnegative")
	}
	normalization, err := core.ParseDiffMode(viper.GetString("normalize"))
	if err != nil {
		return err
	}
	wo := windowOpts()
	for _, value := range []struct {
		name   string
		number float64
	}{{"from", wo.FromSec}, {"to", wo.ToSec}, {"trim-start", wo.TrimStartSec}, {"trim-end", wo.TrimEndSec}} {
		if !flameFinite(value.number) || value.number < 0 {
			return fmt.Errorf("--%s must be finite and nonnegative", value.name)
		}
	}
	around := viper.GetInt("around-long-task")
	if around < 0 {
		return fmt.Errorf("--around-long-task must be nonnegative")
	}
	files, err := expandPaths(args)
	if err != nil {
		return err
	}
	if len(files) == 0 {
		return fmt.Errorf("no input files")
	}
	differential := viper.GetBool("diff")
	if differential && len(files) != 2 {
		return fmt.Errorf("--diff requires exactly two recording files: baseline and current")
	}
	if !differential {
		sort.Strings(files)
	}
	trees := make([]*core.Tree, 0, len(files))
	for _, path := range files {
		rec, _, err := loadRecording(cmd, path)
		if err != nil {
			return err
		}
		tree, err := buildFlameTree(rec, wo, around)
		if err != nil {
			return &inputError{File: path, Code: "window_error", Err: err}
		}
		trees = append(trees, tree)
	}
	focus := viper.GetString("focus")
	charts := make([]flamereport.Chart, 0, len(trees))
	if differential {
		before, after := trees[0], trees[1]
		if focus != "" {
			before, after, err = focusFlameDiff(before, after, focus)
			if err != nil {
				return err
			}
		}
		opts.Title = "Differential flame graph"
		comparison, err := core.DiffNormalized(before, after, normalization)
		if err != nil {
			return err
		}
		charts = append(charts, flamereport.Chart{Label: files[1] + " − " + files[0], Before: before, After: after, Comparison: comparison})
	} else {
		for i, tree := range trees {
			if focus != "" {
				tree, err = focusFlameTree(tree, focus)
				if err != nil {
					return &inputError{File: files[i], Code: "focus_error", Err: err}
				}
			}
			charts = append(charts, flamereport.Chart{Label: files[i], Tree: tree})
		}
	}
	if err := flamereport.WriteNotes(cmd.ErrOrStderr(), charts); err != nil {
		return err
	}
	out := viper.GetString("out")
	if out == "" && format == "html" {
		out = "wkperf-flame.html"
	}
	render := func(w io.Writer) error { return flamereport.Render(w, format, charts, opts) }
	if out == "" || out == "-" {
		return render(cmd.OutOrStdout())
	}
	return writeFileNote(cmd, out, render)
}

func buildFlameTree(rec *model.Recording, wo analyze.Options, around int) (*core.Tree, error) {
	duration := rec.EndTime - rec.StartTime
	rules := ownershipRules()
	var tree *core.Tree
	if !flameFinite(duration) || duration <= 0 {
		tree = core.Build(rec, core.Options{Ownership: rules})
		duration = tree.EndTime
	}
	opts, err := flameBuildOptions(rec, wo, around, duration)
	if err != nil {
		return nil, err
	}
	opts.Ownership = rules
	if tree == nil {
		return core.Build(rec, opts), nil
	}
	if opts.From == nil || profile.CoversRecording(rec, *opts.From, *opts.To) {
		return tree, nil
	}
	return tree.Window(*opts.From, *opts.To), nil
}

func flameBuildOptions(rec *model.Recording, wo analyze.Options, around int, duration float64) (core.Options, error) {
	var opts core.Options
	from := math.Max(wo.FromSec, wo.TrimStartSec)
	to := duration - wo.TrimEndSec
	if duration == 0 && from == 0 && wo.ToSec == 0 && wo.TrimEndSec == 0 && around == 0 {
		return opts, nil
	}
	if wo.ToSec > 0 {
		to = math.Min(to, wo.ToSec)
	}
	if !flameFinite(from) || !flameFinite(to) || to <= from {
		return opts, fmt.Errorf("window is empty: %.3fs..%.3fs", from, to)
	}
	if around > 0 {
		windowRec := *rec
		windowRec.StartTime, windowRec.EndTime = rec.StartTime+from, rec.StartTime+to
		tasks := profile.Tasks(&windowRec)
		long := tasks[:0]
		for _, task := range tasks {
			if task.Duration()*1000 > profile.LongTaskBudgetMs {
				long = append(long, task)
			}
		}
		tasks = long
		sort.Slice(tasks, func(i, j int) bool {
			if tasks[i].Duration() != tasks[j].Duration() {
				return tasks[i].Duration() > tasks[j].Duration()
			}
			return tasks[i].StartTime < tasks[j].StartTime
		})
		if around > len(tasks) {
			return opts, fmt.Errorf("--around-long-task %d exceeds %d script tasks over 50 ms", around, len(tasks))
		}
		task := tasks[around-1]
		from, to = math.Max(from, task.StartTime-rec.StartTime), math.Min(to, task.EndTime-rec.StartTime)
		if to <= from {
			return opts, fmt.Errorf("long task %d does not intersect selected window", around)
		}
	}
	opts.From, opts.To = &from, &to
	return opts, nil
}

func focusFlameTree(tree *core.Tree, selector string) (*core.Tree, error) {
	id, context, err := resolveFlameFocus([]*core.Tree{tree}, selector)
	if err != nil {
		return nil, err
	}
	if context {
		return tree.FocusNode(id), nil
	}
	return tree.FocusFunction(id), nil
}

type flameFocusCandidate struct {
	frame  core.Frame
	totals []float64
}

// Resolve function identity before considering calling contexts. Exact n_ IDs
// retain their contextual meaning; function selectors merge all their contexts.
func resolveFlameFocus(trees []*core.Tree, selector string) (string, bool, error) {
	candidates := make(map[string]*flameFocusCandidate)
	exactContext := false
	for i, tree := range trees {
		nodes, err := query.ResolveNodes(tree, selector)
		var ambiguous *query.AmbiguousError
		if err != nil && !errors.Is(err, query.ErrNoMatches) && !errors.As(err, &ambiguous) {
			return "", false, err
		}
		matched := make(map[string]bool)
		if ambiguous != nil {
			for _, function := range ambiguous.Candidates {
				matched[function.ID] = true
			}
		}
		for _, node := range nodes {
			exactContext = exactContext || node.ID == selector
			matched[node.Frame.ID] = true
		}
		for _, function := range tree.Functions() {
			if !matched[function.ID] {
				continue
			}
			candidate := candidates[function.ID]
			if candidate == nil {
				candidate = &flameFocusCandidate{frame: function.Frame, totals: make([]float64, len(trees))}
				candidates[function.ID] = candidate
			}
			candidate.totals[i] = function.TotalMs
		}
	}
	if exactContext {
		return selector, true, nil
	}
	if len(candidates) == 0 {
		return "", false, fmt.Errorf("%w: %q", query.ErrNoMatches, selector)
	}
	if len(candidates) == 1 {
		for id := range candidates {
			return id, false, nil
		}
	}
	labels := make([]string, 0, len(candidates))
	for id, candidate := range candidates {
		total := fmt.Sprintf("%g ms total", candidate.totals[0])
		if len(trees) == 2 {
			total = fmt.Sprintf("baseline %g ms; current %g ms", candidate.totals[0], candidate.totals[1])
		}
		labels = append(labels, fmt.Sprintf("%s %s (%s)", id, core.FrameLabel(candidate.frame), total))
	}
	sort.Strings(labels)
	return "", false, fmt.Errorf("focus selector %q matches %d functions; choose a function or node ID: %s", selector, len(candidates), strings.Join(labels, "; "))
}

func focusFlameDiff(before, after *core.Tree, selector string) (*core.Tree, *core.Tree, error) {
	id, context, err := resolveFlameFocus([]*core.Tree{before, after}, selector)
	if err != nil {
		return nil, nil, err
	}
	if context {
		return before.FocusNode(id), after.FocusNode(id), nil
	}
	return before.FocusFunction(id), after.FocusFunction(id), nil
}

func flameFinite(value float64) bool { return !math.IsNaN(value) && !math.IsInf(value, 0) }
