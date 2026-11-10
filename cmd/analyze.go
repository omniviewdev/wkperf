package cmd

import (
	"bytes"
	"encoding/json"
	"fmt"
	"html"
	"io"
	"strings"

	"github.com/spf13/cobra"
	"github.com/spf13/viper"

	"github.com/omniviewdev/wkperf/internal/ai"
	"github.com/omniviewdev/wkperf/internal/insights"
	"github.com/omniviewdev/wkperf/internal/model"
	"github.com/omniviewdev/wkperf/internal/report/insightshtml"
	"github.com/omniviewdev/wkperf/internal/scorecard"
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
			if err := bindFlags(cmd, "format", "out", "top", "jank-ms", "long-task-ms", "no-color", "open", "no-scorecard"); err != nil {
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
	f.Bool("no-scorecard", false, "skip the performance scorecard for each recording")
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
	cards := make([]*scorecard.Scorecard, 0, len(files))
	withCard := !viper.GetBool("no-scorecard")
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
		rep := insights.Analyze(rec, f, iopts)
		reps = append(reps, rep)
		if withCard {
			cards = append(cards, scorecard.Evaluate(rec, rep))
		} else {
			cards = append(cards, nil)
		}
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
			if cards[i] != nil {
				fmt.Fprintln(stdout)
				if err := scorecard.RenderTerm(stdout, cards[i], color); err != nil {
					return err
				}
			}
		}
		if err := runModel(); err != nil {
			return err
		}
		printAI(cmd, "term", aiText)
	}
	if format == "md" {
		render := func(w io.Writer) error { return renderMarkdownAll(w, reps, cards) }
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
		if err := writeFileNote(cmd, p, func(w io.Writer) error { return renderMarkdownAll(w, reps, cards) }); err != nil {
			return err
		}
	}
	if format == "json" || format == "all" {
		render := func(w io.Writer) error { return renderInsightsJSON(w, reps, cards, aiText) }
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
		if err := writeFileNote(cmd, p, func(w io.Writer) error { return renderInsightsHTML(w, reps, cards, aiText) }); err != nil {
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

func renderMarkdownAll(w io.Writer, reps []*insights.Report, cards []*scorecard.Scorecard) error {
	for i, r := range reps {
		if i > 0 {
			if _, err := io.WriteString(w, "\n---\n\n"); err != nil {
				return err
			}
		}
		if err := insights.RenderMarkdown(w, r); err != nil {
			return err
		}
		if cards[i] != nil {
			if _, err := io.WriteString(w, "\n"); err != nil {
				return err
			}
			if err := scorecard.RenderMarkdown(w, cards[i]); err != nil {
				return err
			}
		}
	}
	return nil
}

func renderInsightsJSON(w io.Writer, reps []*insights.Report, cards []*scorecard.Scorecard, aiText string) error {
	type entry struct {
		*insights.Report
		Scorecard *scorecard.Scorecard `json:"scorecard,omitempty"`
	}
	entries := make([]entry, len(reps))
	for i, r := range reps {
		entries[i] = entry{r, cards[i]}
	}
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(struct {
		Schema  string  `json:"schema"`
		Reports []entry `json:"reports"`
		AI      string  `json:"ai,omitempty"`
	}{insightsSchema, entries, aiText})
}

// renderInsightsHTML renders the insights page and appends each recording's
// scorecard (escaped Markdown) before </body>.
func renderInsightsHTML(w io.Writer, reps []*insights.Report, cards []*scorecard.Scorecard, aiText string) error {
	var buf bytes.Buffer
	if err := insightshtml.RenderWithAI(&buf, reps, aiText); err != nil {
		return err
	}
	page := buf.String()
	var sec strings.Builder
	for i, c := range cards {
		if c == nil {
			continue
		}
		var md bytes.Buffer
		if err := scorecard.RenderMarkdown(&md, c); err != nil {
			return err
		}
		fmt.Fprintf(&sec, `<div style="max-width:1000px;margin:0 auto;padding:0 16px 24px;font:14px/1.5 -apple-system,BlinkMacSystemFont,'Segoe UI',sans-serif"><h2>Scorecard: %s</h2><pre style="background:rgba(128,128,128,.15);padding:10px;border-radius:6px;overflow:auto">%s</pre></div>`,
			html.EscapeString(reps[i].Source), html.EscapeString(md.String()))
	}
	if i := strings.LastIndex(page, "</body>"); i >= 0 {
		page = page[:i] + sec.String() + page[i:]
	} else {
		page += sec.String()
	}
	_, err := io.WriteString(w, page)
	return err
}
