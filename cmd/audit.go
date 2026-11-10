package cmd

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/spf13/cobra"
	"github.com/spf13/viper"

	"github.com/omniviewdev/wkperf/internal/ai"
	"github.com/omniviewdev/wkperf/internal/audit"
	"github.com/omniviewdev/wkperf/internal/diagnostic"
	"github.com/omniviewdev/wkperf/internal/insights"
	"github.com/omniviewdev/wkperf/internal/report/audithtml"
	"github.com/omniviewdev/wkperf/internal/schema"
	"github.com/omniviewdev/wkperf/internal/scorecard"
)

const defaultAuditBase = "wkperf-audit"

// auditSchema identifies the audit --format json document.
const auditSchema = schema.AuditVersion

// exitMinScore is the exit code when --min-score lacks a supported passing grade.
const exitMinScore = 4

func newAuditCmd() *cobra.Command {
	c := &cobra.Command{
		Use:   "audit PATH...",
		Short: "Audit one build across many scenes: scorecard, where time goes, what to fix first",
		Long: `Audit one build (no baseline) across many scenes. Each subdirectory of a given
directory is a scene whose *.json files are repeat runs of it; loose *.json
files are one scene each (name = file name). With --group-by-prefix, files
named <scene>-<n>.json or <scene>_<n>.json are grouped into one scene.

Every recording is analyzed and rated against the scorecard standards, then
aggregated: per-scene scores, an overall grade, cross-scene priorities
("fix first"), merged hot functions and a time breakdown. A grade requires
sufficient observations in every contributing run and scene. --min-score exits
4 when the overall grade is unavailable or the supported score is below N.`,
		Example: `  wkperf audit scenes/
  wkperf audit scenes/ --format html --out audit.html --open
  wkperf audit scenes/ --min-score 70          # exit 4 below 70 or with no supported grade
  wkperf audit captures/ --group-by-prefix --emit-prompt=prompt.md`,
		Args: cobra.MinimumNArgs(1),
		PreRunE: func(cmd *cobra.Command, _ []string) error {
			if err := bindFlags(cmd, "format", "out", "top", "jank-ms", "long-task-ms", "no-color", "open", "group-by-prefix", "min-score", "module-grouping"); err != nil {
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
		RunE: runAudit,
	}
	f := c.Flags()
	f.String("format", "term", "output format: term|md|json|html|all")
	f.String("out", "", "output file (md/json/html) or directory/prefix with --format all (default "+defaultAuditBase+".html)")
	f.Int("top", 10, "list length for priorities, hotspots and modules")
	f.Float64("jank-ms", 16.7, "finding/slow-frame threshold in ms; scorecard standards stay fixed at 16.7ms")
	f.Float64("long-task-ms", 50, "script duration above which a task is long")
	f.Bool("no-color", false, "disable colored output (NO_COLOR is also respected)")
	f.Bool("open", false, "open the HTML report after writing it")
	f.Bool("group-by-prefix", false, "group loose files named <scene>-<n>.json or <scene>_<n>.json into one scene")
	f.Float64("min-score", 0, "exit with code 4 when the overall grade is unavailable or its score is below N (0-100; 0 = off)")
	addSourceMapFlags(c)
	addModuleGroupingFlag(c)
	addWindowFlags(c)
	addAIFlags(c)
	return c
}

// sceneFiles is one discovered scene: a name and its recording files.
type sceneFiles struct {
	Name  string
	Files []string
}

var prefixRe = regexp.MustCompile(`^(.+?)[-_]\d+$`)

func looseSceneName(file string, groupByPrefix bool) string {
	name := strings.TrimSuffix(filepath.Base(file), filepath.Ext(file))
	if groupByPrefix {
		if m := prefixRe.FindStringSubmatch(name); m != nil {
			return m[1]
		}
	}
	return name
}

// discoverScenes maps paths to scenes. A directory contributes one scene per
// subdirectory (its *.json files are the runs) plus loose *.json files; a
// plain file is a loose file. Scenes with the same name are merged.
func discoverScenes(paths []string, groupByPrefix bool) ([]sceneFiles, error) {
	var order []string
	byName := map[string]*sceneFiles{}
	add := func(name string, files ...string) {
		s := byName[name]
		if s == nil {
			s = &sceneFiles{Name: name}
			byName[name] = s
			order = append(order, name)
		}
		s.Files = append(s.Files, files...)
	}
	jsonIn := func(dir string) ([]string, error) {
		m, err := filepath.Glob(filepath.Join(dir, "*.json"))
		if err != nil {
			return nil, err
		}
		sort.Strings(m)
		var out []string
		for _, f := range m {
			if st, err := os.Stat(f); err == nil && !st.IsDir() {
				out = append(out, f)
			}
		}
		return out, nil
	}
	for _, p := range paths {
		st, err := os.Stat(p)
		if err != nil {
			return nil, diagnostic.Wrap(diagnostic.LoadError, p, err)
		}
		if !st.IsDir() {
			add(looseSceneName(p, groupByPrefix), p)
			continue
		}
		entries, err := os.ReadDir(p)
		if err != nil {
			return nil, diagnostic.Wrap(diagnostic.LoadError, p, err)
		}
		found := false
		for _, e := range entries {
			if !e.IsDir() {
				continue
			}
			sub := filepath.Join(p, e.Name())
			files, err := jsonIn(sub)
			if err != nil {
				return nil, err
			}
			if len(files) > 0 {
				add(e.Name(), files...)
				found = true
			}
		}
		loose, err := jsonIn(p)
		if err != nil {
			return nil, err
		}
		for _, f := range loose {
			add(looseSceneName(f, groupByPrefix), f)
			found = true
		}
		if !found {
			return nil, diagnostic.Wrap(diagnostic.InputError, p, fmt.Errorf("no scenes found (want subdirectories or files containing .json recordings)"))
		}
	}
	out := make([]sceneFiles, 0, len(order))
	for _, n := range order {
		out = append(out, *byName[n])
	}
	return out, nil
}

func runAudit(cmd *cobra.Command, args []string) error {
	format := formatName(viper.GetString("format"))
	switch format {
	case "term", "md", "json", "html", "all":
	default:
		return fmt.Errorf("unknown --format %q (want term|md|json|html|all)", format)
	}
	if viper.GetBool("open") && format != "html" && format != "all" {
		return fmt.Errorf("--open requires --format html or all")
	}
	cmd.SilenceUsage = true
	grouping, err := moduleGrouping()
	if err != nil {
		return err
	}

	sf, failures := discoverScenesContinuing(args, viper.GetBool("group-by-prefix"))
	discoveryIncomplete := len(failures) > 0
	wo := windowOpts()
	top := viper.GetInt("top")
	iopts := insights.Options{
		Top:            top,
		JankMs:         viper.GetFloat64("jank-ms"),
		LongTaskMs:     viper.GetFloat64("long-task-ms"),
		Sourcemaps:     viper.GetStringSlice("sourcemap"),
		Ownership:      ownershipRules(),
		ModuleGrouping: grouping,
	}
	scenes := make([]audit.Scene, 0, len(sf))
	var sourceMaps []schema.SourceMapMetadata
	for _, s := range sf {
		sc := audit.Scene{Name: s.Name}
		for _, f := range s.Files {
			rec, diagnostics, err := loadRecording(cmd, f)
			if err != nil {
				failures = append(failures, failuresFor(err)...)
				sc.MissingRuns++
				continue
			}
			originalStart := rec.StartTime
			rec, err = clipRecording(rec, wo)
			if err != nil {
				failures = append(failures, failuresFor(diagnostic.Wrap(diagnostic.WindowError, f, err))...)
				sc.MissingRuns++
				continue
			}
			options := iopts
			options.WindowStartSec = rec.StartTime - originalStart
			rep := insights.Analyze(rec, f, options)
			sc.Runs = append(sc.Runs, audit.Run{Source: f, Report: rep, Card: scorecard.Evaluate(rec, rep)})
			if hasSourceMapMetadata(diagnostics) {
				sourceMaps = append(sourceMaps, schema.SourceMapMetadata{File: f, Diagnostics: diagnostics})
			}
		}
		scenes = append(scenes, sc)
	}
	rpt := audit.Build(scenes, audit.Options{Top: top})
	if discoveryIncomplete {
		// The failed path may represent an unknown number of runs or scenes.
		// Keep measured coverage, but do not certify only the surviving input.
		rpt.Overall.Partial = true
		rpt.Overall.Grade = "n/a"
	}
	gateErr := partialError(failures)
	documentFailures := failures
	if gateErr == nil {
		gateErr = auditMinimumScore(rpt, viper.GetFloat64("min-score"))
		if gateErr != nil {
			documentFailures = failuresFor(gateErr)
		}
	}

	emit := viper.GetString("emit-prompt")
	wantAI := viper.GetBool("ai")
	var prompt ai.Prompt
	if emit != "" || wantAI {
		prompt = ai.BuildAuditPrompt(rpt, aiContext(top))
	}
	stdout := cmd.OutOrStdout()
	if emit != "" {
		if err := combineFailures(failures, emitPrompt(stdout, cmd.ErrOrStderr(), emit, prompt)); err != nil {
			return err
		}
		return gateErr
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
	renderMD := func(w io.Writer) error {
		if _, err := io.WriteString(w, failureMarkdown(documentFailures)); err != nil {
			return err
		}
		return audit.RenderMarkdown(w, rpt)
	}

	if format == "term" || format == "all" {
		if _, err := io.WriteString(stdout, failureMarkdown(documentFailures)); err != nil {
			return err
		}
		if err := audit.RenderTerm(stdout, rpt, useColor()); err != nil {
			return err
		}
		if err := runModel(); err != nil {
			return err
		}
		printAI(cmd, "term", aiText)
	}
	if format == "md" {
		if out == "" || out == "-" {
			if err := renderMD(stdout); err != nil {
				return err
			}
		} else if err := writeFileNote(cmd, out, renderMD); err != nil {
			return err
		}
		if err := runModel(); err != nil {
			return err
		}
		if out == "" || out == "-" {
			printAI(cmd, "md", aiText)
		}
	}
	if format == "all" {
		if err := writeFileNote(cmd, reportPathBase(out, format, ".md", defaultAuditBase), renderMD); err != nil {
			return err
		}
	}
	if format == "json" || format == "all" {
		render := func(w io.Writer) error { return renderAuditJSON(w, rpt, sourceMaps, documentFailures, aiText) }
		if format == "json" {
			if err := runModel(); err != nil {
				return err
			}
			if out == "" || out == "-" {
				err = render(stdout)
			} else {
				err = writeFileNote(cmd, out, render)
			}
			if err != nil {
				return err
			}
		} else if err := writeFileNote(cmd, reportPathBase(out, format, ".json", defaultAuditBase), render); err != nil {
			return err
		}
	}
	if format == "html" || format == "all" {
		if err := runModel(); err != nil {
			return err
		}
		p := reportPathBase(out, format, ".html", defaultAuditBase)
		if err := writeFileNote(cmd, p, func(w io.Writer) error { return audithtml.Render(w, rpt, aiText) }); err != nil {
			return err
		}
		if viper.GetBool("open") {
			if err := openFile(p); err != nil {
				return fmt.Errorf("open %s: %w", p, err)
			}
		}
	}

	return gateErr
}

func auditMinimumScore(rpt *audit.Report, min float64) error {
	if min <= 0 {
		return nil
	}
	if rpt.Overall == nil || rpt.Overall.Partial || rpt.Overall.Grade == "n/a" || rpt.Overall.Grade == "" {
		return &ExitError{Code: exitMinScore, Msg: fmt.Sprintf("overall grade is unavailable: insufficient observations in one or more runs/scenes; cannot satisfy --min-score %.0f", min)}
	}
	if rpt.Overall.Score < min {
		return &ExitError{Code: exitMinScore, Msg: fmt.Sprintf("overall score %.0f (%s) is below --min-score %.0f", rpt.Overall.Score, rpt.Overall.Grade, min)}
	}
	return nil
}

func renderAuditJSON(w io.Writer, r *audit.Report, sourceMaps []schema.SourceMapMetadata, failures []schema.Error, aiText string) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(schema.AuditDocument{Schema: auditSchema, Report: r, SourceMaps: sourceMaps, Errors: failures, AI: aiText})
}
