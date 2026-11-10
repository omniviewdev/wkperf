package cmd

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/spf13/cobra"
	"github.com/spf13/viper"

	"github.com/omniviewdev/wkperf/internal/compare"
	"github.com/omniviewdev/wkperf/internal/report/html"
	"github.com/omniviewdev/wkperf/internal/report/jsonout"
	"github.com/omniviewdev/wkperf/internal/report/term"
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
			for _, k := range []string{"format", "out", "threshold", "top", "verbose", "no-color", "fail-on-regression", "open", "title"} {
				if err := viper.BindPFlag(k, cmd.Flags().Lookup(k)); err != nil {
					return err
				}
			}
			return nil
		},
		RunE: runCompare,
	}
	f := c.Flags()
	f.StringSlice("vs", nil, "candidate files or directories (repeatable, comma separated, or several values after one --vs)")
	f.String("format", "term", "output format: term|html|json|all")
	f.String("out", "", "output file (html/json) or directory/prefix with --format all (default "+defaultReportBase+".html)")
	f.Float64("threshold", 0.05, "relative change below which a metric is unchanged")
	f.Int("top", 10, "number of functions to list per direction")
	f.Bool("verbose", false, "show all metrics, including unchanged ones")
	f.Bool("no-color", false, "disable colored output (NO_COLOR is also respected)")
	f.Bool("fail-on-regression", false, "exit with code 2 on a confident regression in frames/script/layout/cpu")
	f.Bool("open", false, "open the HTML report after writing it")
	f.String("title", "", "title for the HTML report")
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

	format := strings.ToLower(viper.GetString("format"))
	switch format {
	case "term", "html", "json", "all":
	default:
		return fmt.Errorf("unknown --format %q (want term|html|json|all)", format)
	}

	base, baseFiles, err := loadGroup(baseP)
	if err != nil {
		return fmt.Errorf("baseline: %w", err)
	}
	cand, candFiles, err := loadGroup(candP)
	if err != nil {
		return fmt.Errorf("candidate: %w", err)
	}

	opts := compare.DefaultOptions()
	opts.RelThreshold = viper.GetFloat64("threshold")
	opts.TopFunctions = viper.GetInt("top")
	res := compare.Compare(base, cand, opts)
	if res.Baseline.Sources == nil {
		res.Baseline.Sources = baseFiles
	}
	if res.Cand.Sources == nil {
		res.Cand.Sources = candFiles
	}

	out := viper.GetString("out")
	stdout := cmd.OutOrStdout()
	var htmlPath string

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
	if format == "html" || format == "all" {
		htmlPath = reportPath(out, format, ".html")
		o := html.Options{Title: viper.GetString("title"), Generated: time.Now()}
		if err := writeFile(htmlPath, func(w io.Writer) error { return html.Render(w, res, o) }); err != nil {
			return err
		}
		fmt.Fprintln(cmd.ErrOrStderr(), "wrote", htmlPath)
	}
	if format == "json" || format == "all" {
		p := reportPath(out, format, ".json")
		if format == "json" && out == "" {
			if err := jsonout.Render(stdout, res); err != nil {
				return err
			}
		} else {
			if err := writeFile(p, func(w io.Writer) error { return jsonout.Render(w, res) }); err != nil {
				return err
			}
			fmt.Fprintln(cmd.ErrOrStderr(), "wrote", p)
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
	if format != "all" {
		if out == "" {
			return defaultReportBase + ext
		}
		return out
	}
	if out == "" {
		return defaultReportBase + ext
	}
	if st, err := os.Stat(out); (err == nil && st.IsDir()) || strings.HasSuffix(out, string(filepath.Separator)) {
		return filepath.Join(out, defaultReportBase+ext)
	}
	prefix := strings.TrimSuffix(strings.TrimSuffix(out, ".html"), ".json")
	return prefix + ext
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
