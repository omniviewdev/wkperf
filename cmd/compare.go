package cmd

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/spf13/cobra"
	"github.com/spf13/viper"

	"github.com/omniviewdev/wkperf/internal/ai"
	"github.com/omniviewdev/wkperf/internal/analyze"
	"github.com/omniviewdev/wkperf/internal/compare"
	"github.com/omniviewdev/wkperf/internal/insights"
	"github.com/omniviewdev/wkperf/internal/metrics"
	"github.com/omniviewdev/wkperf/internal/report/html"
	"github.com/omniviewdev/wkperf/internal/report/insightshtml"
	"github.com/omniviewdev/wkperf/internal/report/term"
	"github.com/omniviewdev/wkperf/internal/schema"
)

const defaultReportBase = "wkperf-report"

func newCompareCmd() *cobra.Command {
	c := &cobra.Command{
		Use:   "compare [BASE_FILES...] --vs CAND_FILES...",
		Short: "Compare a baseline group of recordings against a candidate group",
		Long: `Compare baseline recordings (positional args) against candidate recordings (--vs).
Both sides accept files or directories (all *.json inside). With exactly two
positional args and no --vs, the two files are compared 1v1.`,
		Example: `  wkperf compare base-*.json --vs cand-*.json
  wkperf compare before.json after.json
  wkperf compare ./baseline --vs ./candidate --format all --out reports/`,
		PreRunE: func(cmd *cobra.Command, _ []string) error {
			if err := bindFlags(cmd, "format", "out", "threshold", "top", "verbose", "no-color", "fail-on-regression", "open", "title",
				"normalize", "strict-fixture", "min-comparability", "module-grouping"); err != nil {
				return err
			}
			if err := bindFlags(cmd, windowKeys...); err != nil {
				return err
			}
			if err := bindSourceMapFlags(cmd); err != nil {
				return err
			}
			return bindAIFlags(cmd)
		},
		RunE: runCompare,
	}
	f := c.Flags()
	f.StringSlice("vs", nil, "candidate files or directories (repeatable, comma separated, or several values after one --vs)")
	f.String("format", "term", "output format: term|md|html|json|all")
	f.String("out", "", "output file (md/html/json) or directory/prefix with --format all (default "+defaultReportBase+".html)")
	f.Float64("threshold", 0.05, "relative change below which a metric is unchanged")
	f.Int("top", 10, "number of functions to list per direction")
	f.Bool("verbose", false, "show all metrics, including unchanged ones")
	f.Bool("no-color", false, "disable colored output (NO_COLOR is also respected)")
	f.Bool("fail-on-regression", false, "exit with code 2 on a confident regression in frames/script/layout/cpu")
	f.Bool("open", false, "open the HTML report after writing it")
	f.String("title", "", "title for the HTML report")
	f.String("normalize", "auto", "normalize totals for different recording lengths: auto|per-second|none")
	f.Bool("strict-fixture", false, "exit with code 3 when the comparability score is below --min-comparability")
	f.Float64("min-comparability", 0.6, "minimum comparability score (0..1) for --strict-fixture")
	addSourceMapFlags(c)
	addModuleGroupingFlag(c)
	addWindowFlags(c)
	addAIFlags(c)
	return c
}

// splitGroups resolves the baseline and candidate path lists.
func splitGroups(args, vs []string) (base, cand []string, err error) {
	if len(vs) == 0 {
		if len(args) == 2 {
			return args[:1], args[1:], nil
		}
		return nil, nil, fmt.Errorf("need candidate files: use --vs CAND_FILES (or pass exactly two files for a 1v1 comparison)")
	}
	if len(args) == 0 {
		return nil, nil, fmt.Errorf("need baseline files as positional arguments")
	}
	return args, vs, nil
}

func runCompare(cmd *cobra.Command, args []string) error {
	vs, _ := cmd.Flags().GetStringSlice("vs")
	baseP, candP, err := splitGroups(args, vs)
	if err != nil {
		return err
	}
	cmd.SilenceUsage = true

	format := formatName(viper.GetString("format"))
	switch format {
	case "term", "md", "html", "json", "all":
	default:
		return fmt.Errorf("unknown --format %q (want term|md|html|json|all)", format)
	}
	grouping, err := moduleGrouping()
	if err != nil {
		return err
	}

	norm := strings.ToLower(viper.GetString("normalize"))
	switch norm {
	case "auto", "per-second", "none":
	default:
		return fmt.Errorf("unknown --normalize %q (want auto|per-second|none)", norm)
	}

	wo := windowOpts()
	baseL, baseFiles, err := loadGroup(cmd, baseP, wo)
	if err != nil {
		return fmt.Errorf("baseline: %w", err)
	}
	candL, candFiles, err := loadGroup(cmd, candP, wo)
	if err != nil {
		return fmt.Errorf("candidate: %w", err)
	}

	opts := compare.DefaultOptions()
	opts.RelThreshold = viper.GetFloat64("threshold")
	opts.TopFunctions = viper.GetInt("top")
	opts.Normalize = norm
	res := compare.Compare(summaries(baseL), summaries(candL), opts)
	if res.Baseline.Sources == nil {
		res.Baseline.Sources = baseFiles
	}
	if res.Cand.Sources == nil {
		res.Cand.Sources = candFiles
	}

	out := viper.GetString("out")
	stdout := cmd.OutOrStdout()
	var htmlPath string

	emit := viper.GetString("emit-prompt")
	wantAI := viper.GetBool("ai")
	var prompt ai.Prompt
	if emit != "" || wantAI {
		top := viper.GetInt("top")
		bRep, err := repReport(baseL, res.BaseRep, wo, top, grouping)
		if err != nil {
			return err
		}
		cRep, err := repReport(candL, res.CandRep, wo, top, grouping)
		if err != nil {
			return err
		}
		prompt = ai.BuildComparePrompt(res, bRep, cRep, aiContext(top))
	}
	if emit != "" {
		return emitPrompt(stdout, cmd.ErrOrStderr(), emit, prompt)
	}

	if format == "term" || format == "all" {
		o := term.Options{
			Color:   useColor(),
			Width:   terminalWidth(),
			Verbose: viper.GetBool("verbose"),
			Top:     viper.GetInt("top"),
		}
		if err := term.Render(stdout, res, o); err != nil {
			return err
		}
	}
	var aiText string
	if wantAI {
		var err error
		if aiText, err = runAI(cmd.Context(), prompt); err != nil {
			return err
		}
		printAI(cmd, format, aiText)
	}
	if format == "md" || format == "all" {
		render := func(w io.Writer) error { return compare.RenderMarkdown(w, res) }
		if format == "md" && out == "" {
			if err := render(stdout); err != nil {
				return err
			}
		} else if err := writeFileNote(cmd, reportPath(out, format, ".md"), render); err != nil {
			return err
		}
	}
	if format == "html" || format == "all" {
		htmlPath = reportPath(out, format, ".html")
		o := html.Options{Title: viper.GetString("title")}
		if err := writeFile(htmlPath, func(w io.Writer) error {
			if aiText == "" {
				return html.Render(w, res, o)
			}
			var buf bytes.Buffer
			if err := html.Render(&buf, res, o); err != nil {
				return err
			}
			_, err := io.WriteString(w, injectAI(buf.String(), aiText))
			return err
		}); err != nil {
			return err
		}
		fmt.Fprintln(cmd.ErrOrStderr(), "wrote", htmlPath)
	}
	if format == "json" || format == "all" {
		document := schema.ComparisonDocument{Schema: schema.ComparisonVersion, Result: res, SourceMaps: append(sourceMapMetadata(baseL), sourceMapMetadata(candL)...)}
		render := func(w io.Writer) error {
			enc := json.NewEncoder(w)
			enc.SetIndent("", "  ")
			return enc.Encode(document)
		}
		if format == "json" && out == "" {
			if err := render(stdout); err != nil {
				return err
			}
		} else if err := writeFileNote(cmd, reportPath(out, format, ".json"), render); err != nil {
			return err
		}
	}

	if viper.GetBool("open") {
		if htmlPath == "" {
			return fmt.Errorf("--open requires --format html or all")
		}
		if err := openFile(htmlPath); err != nil {
			return fmt.Errorf("open %s: %w", htmlPath, err)
		}
	}

	if viper.GetBool("strict-fixture") {
		min := viper.GetFloat64("min-comparability")
		if sc := res.Comparability.Score; sc < min {
			return &ExitError{Code: 3, Msg: fmt.Sprintf("fixtures not comparable: score %.2f < --min-comparability %.2f (capture the same scenario and duration, or use --trim-*/--from/--to)", sc, min)}
		}
	}
	if viper.GetBool("fail-on-regression") {
		if n := confidentRegressions(res); n > 0 {
			return &ExitError{Code: 2, Msg: fmt.Sprintf("%d regressed metric(s) in frames/script/layout/cpu", n)}
		}
	}
	return nil
}

// confidentRegressions counts regressed metrics with non-low confidence in
// the gated groups.
func confidentRegressions(r *compare.Result) int {
	n := 0
	for _, m := range r.Metrics {
		switch m.Metric.Group {
		case "frames", "script", "layout", "cpu":
		default:
			continue
		}
		if m.Verdict == compare.Regressed && m.Confidence != "low" {
			n++
		}
	}
	return n
}

// reportPath computes where a report of the given extension is written.
// With --format all, out is a directory or a file prefix.
func reportPath(out, format, ext string) string {
	return reportPathBase(out, format, ext, defaultReportBase)
}

func reportPathBase(out, format, ext, base string) string {
	if format != "all" {
		if out == "" {
			return base + ext
		}
		return out
	}
	if out == "" {
		return base + ext
	}
	if st, err := os.Stat(out); (err == nil && st.IsDir()) || strings.HasSuffix(out, string(filepath.Separator)) {
		return filepath.Join(out, base+ext)
	}
	prefix := strings.TrimSuffix(strings.TrimSuffix(strings.TrimSuffix(out, ".html"), ".json"), ".md")
	return prefix + ext
}

// repReport builds the insights report for the loaded recording whose
// summary is rep (the representative run), honoring the window.
func repReport(group []*loaded, rep *metrics.Summary, wo analyze.Options, top int, grouping string) (*insights.Report, error) {
	if rep == nil {
		return nil, nil
	}
	for _, l := range group {
		if l.Sum.Source == rep.Source {
			rec, err := clipRecording(l.Rec, wo)
			if err != nil {
				return nil, fmt.Errorf("%s: %w", rep.Source, err)
			}
			return insights.Analyze(rec, rep.Source, insights.Options{Top: top, WindowStartSec: rec.StartTime - l.Rec.StartTime, Sourcemaps: viper.GetStringSlice("sourcemap"), ModuleGrouping: grouping}), nil
		}
	}
	return nil, nil
}

// printAI shows the model answer: stdout for text formats, stderr when stdout
// carries JSON, nowhere extra for html-only (it lands in the report).
func printAI(cmd *cobra.Command, format, text string) {
	if text == "" {
		return
	}
	switch format {
	case "term", "md", "all":
		fmt.Fprintf(cmd.OutOrStdout(), "\n## AI analysis\n\n%s\n", text)
	case "json":
		fmt.Fprintf(cmd.ErrOrStderr(), "\n## AI analysis\n\n%s\n", text)
	}
}

// injectAI inserts the AI section before the closing </body> of page.
func injectAI(page, text string) string {
	sec := `<div style="max-width:1000px;margin:0 auto;padding:0 16px 48px;font:14px/1.5 -apple-system,BlinkMacSystemFont,'Segoe UI',sans-serif"><h2>AI analysis</h2><div class="ai" style="border:1px solid rgba(128,128,128,.35);border-radius:8px;padding:12px 16px"><style>.ai pre.code{background:rgba(128,128,128,.15);padding:10px;border-radius:6px;overflow:auto}.ai code{background:rgba(128,128,128,.15);padding:0 4px;border-radius:3px}</style>` +
		string(insightshtml.AISection(text)) + `</div></div>`
	if i := strings.LastIndex(page, "</body>"); i >= 0 {
		return page[:i] + sec + page[i:]
	}
	return page + sec
}

func writeFile(path string, render func(io.Writer) error) error {
	if dir := filepath.Dir(path); dir != "." {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return err
		}
	}
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	if err := render(f); err != nil {
		f.Close()
		return fmt.Errorf("render %s: %w", path, err)
	}
	return f.Close()
}

// useColor reports whether colored output should be used.
func useColor() bool {
	if viper.GetBool("no-color") {
		return false
	}
	if _, ok := os.LookupEnv("NO_COLOR"); ok {
		return false
	}
	return isTTY(os.Stdout)
}

func isTTY(f *os.File) bool {
	st, err := f.Stat()
	return err == nil && st.Mode()&os.ModeCharDevice != 0
}

func terminalWidth() int {
	if isTTY(os.Stdout) {
		if w := os.Getenv("COLUMNS"); w != "" {
			var n int
			if _, err := fmt.Sscanf(w, "%d", &n); err == nil && n > 0 {
				return n
			}
		}
	}
	return 100
}

func openFile(path string) error {
	var c *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		c = exec.Command("open", path)
	case "windows":
		c = exec.Command("cmd", "/c", "start", "", path)
	default:
		c = exec.Command("xdg-open", path)
	}
	return c.Start()
}
