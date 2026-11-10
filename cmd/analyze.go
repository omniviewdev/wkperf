package cmd

import (
	"encoding/json"
	"fmt"
	"io"
	"strings"

	"github.com/spf13/cobra"
	"github.com/spf13/viper"

	"github.com/omniviewdev/wkperf/internal/ai"
	"github.com/omniviewdev/wkperf/internal/insights"
	"github.com/omniviewdev/wkperf/internal/model"
	"github.com/omniviewdev/wkperf/internal/report/insightshtml"
)

const defaultAnalyzeBase = "wkperf-analysis"

// insightsSchema identifies the analyze --format json document.
const insightsSchema = "wkperf-insights/v1"

func newAnalyzeCmd() *cobra.Command {
	c := &cobra.Command{
		Use:   "analyze FILE...",
		Short: "Profile single recordings: time breakdown, hotspots, slow frames and findings",
		Long: `Analyze each recording separately (files or directories of *.json): where wall
time goes, hot functions and modules, slow frames, long tasks and heuristic
findings with optimization hints. Use --ai to have an LLM interpret the result,
or --emit-prompt to get the prompt for pasting into another agent.`,
		Example: `  wkperf analyze recording.json
  wkperf analyze recording.json --format html --out analysis.html --open
  wkperf analyze recording.json --ai
  wkperf analyze recording.json --emit-prompt=prompt.md`,
		Args: cobra.MinimumNArgs(1),
		PreRunE: func(cmd *cobra.Command, _ []string) error {
			if err := bindFlags(cmd, "format", "out", "top", "jank-ms", "long-task-ms", "no-color", "open"); err != nil {
				return err
			}
			if err := bindFlags(cmd, windowKeys...); err != nil {
				return err
			}
			return bindAIFlags(cmd)
		},
		RunE: runAnalyze,
	}
	f := c.Flags()
	f.String("format", "term", "output format: term|md|json|html|all")
	f.String("out", "", "output file (md/json/html) or directory/prefix with --format all (default "+defaultAnalyzeBase+".html)")
	f.Int("top", 10, "list length for hotspots, modules, slow frames and long tasks")
	f.Float64("jank-ms", 16.7, "frame duration above which a frame is slow")
	f.Float64("long-task-ms", 50, "script duration above which a task is long")
	f.Bool("no-color", false, "disable colored output (NO_COLOR is also respected)")
	f.Bool("open", false, "open the HTML report after writing it")
	addWindowFlags(c)
	addAIFlags(c)
	return c
}

func runAnalyze(cmd *cobra.Command, args []string) error {
	format := strings.ToLower(viper.GetString("format"))
	switch format {
	case "term", "md", "json", "html", "all":
	default:
		return fmt.Errorf("unknown --format %q (want term|md|json|html|all)", format)
	}
	cmd.SilenceUsage = true

	files, err := expandPaths(args)
	if err != nil {
		return err
	}
	wo := windowOpts()
	iopts := insights.Options{
		Top:        viper.GetInt("top"),
		JankMs:     viper.GetFloat64("jank-ms"),
		LongTaskMs: viper.GetFloat64("long-task-ms"),
	}
	reps := make([]*insights.Report, 0, len(files))
	for _, f := range files {
		rec, err := model.Load(f)
		if err != nil {
			if !strings.Contains(err.Error(), f) {
				err = fmt.Errorf("%s: %w", f, err)
			}
			return err
		}
		rec, err = clipRecording(rec, wo)
		if err != nil {
			return fmt.Errorf("%s: %w", f, err)
		}
		reps = append(reps, insights.Analyze(rec, f, iopts))
	}

	emit := viper.GetString("emit-prompt")
	wantAI := viper.GetBool("ai")
	var prompt ai.Prompt
	if emit != "" || wantAI {
		prompt = ai.BuildAnalyzePrompt(reps, aiContext(viper.GetInt("top")))
	}
	stdout := cmd.OutOrStdout()
	if emit != "" {
		return emitPrompt(stdout, cmd.ErrOrStderr(), emit, prompt)
	}

	out := viper.GetString("out")
	var aiText string
	runModel := func() error {
		if !wantAI || aiText != "" {
			return nil
		}
		var err error
		aiText, err = runAI(cmd.Context(), prompt)
		return err
	}

	// stdout text formats
	if format == "term" || format == "all" {
		color := useColor()
		for i, r := range reps {
			if i > 0 {
				fmt.Fprintln(stdout, strings.Repeat("=", 60))
			}
			if err := insights.RenderTerm(stdout, r, color); err != nil {
				return err
			}
		}
		if err := runModel(); err != nil {
			return err
		}
		printAI(cmd, "term", aiText)
	}
	if format == "md" {
		render := func(w io.Writer) error { return renderMarkdownAll(w, reps) }
		if out == "" {
			if err := render(stdout); err != nil {
				return err
			}
		} else if err := writeFileNote(cmd, out, render); err != nil {
			return err
		}
		if err := runModel(); err != nil {
			return err
		}
		if out == "" {
			printAI(cmd, "md", aiText)
		}
	}
	if format == "all" {
		p := reportPathBase(out, format, ".md", defaultAnalyzeBase)
		if err := writeFileNote(cmd, p, func(w io.Writer) error { return renderMarkdownAll(w, reps) }); err != nil {
			return err
		}
	}
	if format == "json" || format == "all" {
		render := func(w io.Writer) error { return renderInsightsJSON(w, reps, aiText) }
		if format == "json" {
			if err := runModel(); err != nil {
				return err
			}
			if out == "" {
				err = render(stdout)
			} else {
				err = writeFileNote(cmd, out, render)
			}
			if err != nil {
				return err
			}
		} else if err := writeFileNote(cmd, reportPathBase(out, format, ".json", defaultAnalyzeBase), render); err != nil {
			return err
		}
	}
	if format == "html" || format == "all" {
		if err := runModel(); err != nil {
			return err
		}
		p := reportPathBase(out, format, ".html", defaultAnalyzeBase)
		if err := writeFileNote(cmd, p, func(w io.Writer) error { return insightshtml.RenderWithAI(w, reps, aiText) }); err != nil {
			return err
		}
		if viper.GetBool("open") {
			if err := openFile(p); err != nil {
				return fmt.Errorf("open %s: %w", p, err)
			}
		}
	} else if viper.GetBool("open") {
		return fmt.Errorf("--open requires --format html or all")
	}
	return nil
}

func writeFileNote(cmd *cobra.Command, path string, render func(io.Writer) error) error {
	if err := writeFile(path, render); err != nil {
		return err
	}
	fmt.Fprintln(cmd.ErrOrStderr(), "wrote", path)
	return nil
}

func renderMarkdownAll(w io.Writer, reps []*insights.Report) error {
	for i, r := range reps {
		if i > 0 {
			if _, err := io.WriteString(w, "\n---\n\n"); err != nil {
				return err
			}
		}
		if err := insights.RenderMarkdown(w, r); err != nil {
			return err
		}
	}
	return nil
}

func renderInsightsJSON(w io.Writer, reps []*insights.Report, aiText string) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(struct {
		Schema  string             `json:"schema"`
		Reports []*insights.Report `json:"reports"`
		AI      string             `json:"ai,omitempty"`
	}{insightsSchema, reps, aiText})
}
