package insights

import (
	"fmt"
	"io"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/omniviewdev/wkperf/internal/flame"
	"github.com/omniviewdev/wkperf/internal/metrics"
	"github.com/omniviewdev/wkperf/internal/rootcause"
)

const MarkdownTruncationMarker = "[truncated: use JSON for complete report]"

type MarkdownOptions struct {
	Brief    bool
	MaxBytes int
}

var markdownEscaper = strings.NewReplacer("|", "\\|", "\n", " ")
var terminalEscaper = strings.NewReplacer("`", "", "**", "", "\\|", "|")

func mdEscape(s string) string { return markdownEscaper.Replace(s) }
func code(s string) string     { return "`" + mdEscape(strings.ReplaceAll(s, "`", "\\`")) + "`" }

func bar(pct float64, width int) string {
	if pct < 0 {
		pct = 0
	}
	if pct > 100 {
		pct = 100
	}
	n := int(pct/100*float64(width) + 0.5)
	return strings.Repeat("█", n) + strings.Repeat(" ", width-n)
}

func frameLabel(f flame.Frame) string {
	name := f.Name
	if name == "" {
		name = "(anonymous)"
	}
	generated := fmt.Sprintf("%s:%d:%d", f.URL, f.Line, f.Column)
	if f.URL == "" {
		generated = fmt.Sprintf("native[%s]:%d:%d", f.SourceID, f.Line, f.Column)
	}
	if f.Original != nil {
		o := f.Original
		mappedName := o.Name
		if mappedName == "" {
			mappedName = name
		}
		return fmt.Sprintf("%s (%s:%d:%d) [generated %s (%s)]", mappedName, o.URL, o.Line, o.Column, name, generated)
	}
	return name + " (" + generated + ")"
}

func functionFrameLabel(f metrics.FunctionFrame) string {
	return functionLabel(metrics.FuncStat{Name: f.Name, URL: f.URL, Line: f.Line, Column: f.Column, SourceID: f.SourceID, Original: f.Original})
}

func renderRootStats(b *strings.Builder, stats []rootcause.Stat) {
	for _, stat := range stats {
		fmt.Fprintf(b, "- %s: %.6g %s (%s). Reproduce: %s\n", mdEscape(stat.Label), stat.Value, stat.Unit, stat.Basis, code(stat.Command))
	}
}

func renderStack(b *strings.Builder, stack rootcause.Stack, brief bool) {
	frames := stack.Frames
	if brief && len(frames) > 12 {
		frames = frames[:12]
	}
	for _, frame := range frames {
		fmt.Fprintf(b, "  - %s\n", code(frameLabel(frame)))
	}
	if len(frames) < len(stack.Frames) {
		b.WriteString("  - _Path abbreviated; use the reproduction query for the complete stack._\n")
	}
	renderRootStats(b, stack.Stats)
	fmt.Fprintf(b, "  Reproduce exact stack: %s\n", code(stack.Command))
}

func renderFindings(b *strings.Builder, r *Report, brief bool) {
	if brief {
		b.WriteString("\n## Findings\n\n")
	} else {
		b.WriteString("\n## Finding details\n\n")
	}
	if len(r.Findings) == 0 {
		b.WriteString("_No issues detected by the available instruments._\n")
	}
	findings := r.Findings
	if brief && len(findings) > 3 {
		findings = findings[:3]
	}
	for _, f := range findings {
		fmt.Fprintf(b, "### [%s] %s\n\n", strings.ToUpper(string(f.Severity)), mdEscape(f.Title))
		fmt.Fprintf(b, "%s · confidence: %s · attributed impact %.6gms (%.6g%%). Reproduce: %s; %s\n\n", code(f.ID), f.Confidence, f.ImpactMs, f.ImpactPct, code(NumericCommand(r, "findings."+f.ID+".impact_ms")), code(NumericCommand(r, "findings."+f.ID+".impact_pct")))
		if !brief {
			for _, e := range f.NumericEvidence {
				fmt.Fprintf(b, "- %s: %.6g %s (%s). Reproduce: %s\n", mdEscape(e.Description), e.Value, e.Unit, e.Basis, code(e.Command))
			}
			for _, reason := range f.ConfidenceReasons {
				fmt.Fprintf(b, "- Confidence: %s\n", mdEscape(reason))
			}
			for _, stack := range f.Stacks {
				renderStack(b, stack, false)
			}
			if f.Command != "" {
				fmt.Fprintf(b, "Evidence query: %s\n", code(f.Command))
			}
		}
		if len(f.Functions) > 0 {
			fmt.Fprintf(b, "- Measured sampled functions: %s\n", code(strings.Join(f.Functions, "; ")))
		}
		if len(f.Triggers) > 0 {
			fmt.Fprintf(b, "- Measured triggers: %s\n", code(strings.Join(f.Triggers, "; ")))
		}
		for _, node := range f.Nodes {
			fmt.Fprintf(b, "- Recorded %s node: %s %s\n", node.EventType, code(node.CSSPath), code(node.DisplayName))
		}
		fmt.Fprintf(b, "\n**Suggestion:** %s\n\n", f.Suggestion)
	}
}

func renderHotPath(b *strings.Builder, r *Report, brief bool) {
	b.WriteString("\n## Hot path\n\n")
	if r.RootCause == nil || len(r.RootCause.HotPath.Frames) == 0 {
		b.WriteString("_No sampled hot path available._\n")
		return
	}
	root := r.RootCause
	b.WriteString("Heaviest root-to-leaf prefix path; sampled weight is an estimate, not measured script wall time.\n\n")
	frames := root.HotPath.Frames
	if brief && len(frames) > 12 {
		frames = frames[:12]
	}
	for _, frame := range frames {
		fmt.Fprintf(b, "- %s\n", code(frameLabel(frame)))
	}
	if len(frames) < len(root.HotPath.Frames) {
		b.WriteString("- _Path abbreviated; query the complete path._\n")
	}
	renderRootStats(b, root.HotPath.Stats)
	fmt.Fprintf(b, "Reproduce hot path: %s\n", code(root.HotPath.Command))
	b.WriteString("\n### Owners and nearest application ancestors\n\n")
	for _, owner := range root.Owners {
		fmt.Fprintf(b, "- Owner: %s\n", owner.Category)
		renderRootStats(b, owner.Stats)
	}
	attributions := root.AppAttribution
	if brief && len(attributions) > 3 {
		attributions = attributions[:3]
	}
	for _, attribution := range attributions {
		fmt.Fprintf(b, "- Nearest application ancestor: %s\n", code(frameLabel(attribution.App)))
		renderRootStats(b, attribution.Stats)
		if !brief {
			for _, stack := range attribution.Stacks {
				renderStack(b, stack, false)
			}
		}
		fmt.Fprintf(b, "  Reproduce attribution: %s\n", code(attribution.Command))
	}
}

func renderFunctions(b *strings.Builder, r *Report, brief bool) {
	b.WriteString("\n## Hot functions\n\n")
	for _, ranking := range []struct {
		title     string
		functions []metrics.FuncStat
	}{{"Top hotspots (self time)", r.Hotspots}, {"Inclusive ranking", r.InclusiveHotspots}} {
		fmt.Fprintf(b, "### %s\n\n", ranking.title)
		if len(ranking.functions) == 0 {
			b.WriteString("_No samples._\n")
			continue
		}
		b.WriteString("| Function | Self ms | Inclusive ms | Reproduce self / inclusive |\n|---|---:|---:|---|\n")
		functions := ranking.functions
		if brief && len(functions) > 5 {
			functions = functions[:5]
		}
		for _, f := range functions {
			prefix := functionMetricPrefix(f)
			fmt.Fprintf(b, "| %s | %.6g | %.6g | %s; %s |\n", code(functionLabel(f)), f.SelfMs, f.TotalMs, code(NumericCommand(r, prefix+".self_ms")), code(NumericCommand(r, prefix+".total_ms")))
			if !brief {
				for _, relation := range []struct {
					name  string
					edges []metrics.CallEdge
				}{{"caller", f.Callers}, {"callee", f.Callees}} {
					for _, edge := range relation.edges {
						key := prefix + "." + relation.name + "s." + url.PathEscape(edge.Function.Key()) + ".weight_ms"
						fmt.Fprintf(b, "| ↳ %s: %s | | %.6g | %s |\n", relation.name, code(functionFrameLabel(edge.Function)), edge.WeightMs, code(NumericCommand(r, key)))
					}
				}
			}
		}
		if !brief {
			for _, f := range functions {
				if len(f.CommonStack) == 0 {
					continue
				}
				fmt.Fprintf(b, "\n#### Common full stack for %s\n\n", mdEscape(f.Name))
				for _, frame := range f.CommonStack {
					fmt.Fprintf(b, "- %s\n", code(functionFrameLabel(frame)))
				}
				fmt.Fprintf(b, "Reproduce exact function stack: %s\n", code(FunctionStackCommand(r, f)))
			}
		}
	}
}

func renderVerification(b *strings.Builder, r *Report) {
	b.WriteString("\n## Verification\n\n")
	for _, finding := range r.Findings {
		for _, verification := range finding.Verify {
			fmt.Fprintf(b, "- %s: %s %s toward roughly %.6g %s. %s Reproduce baseline: %s\n", code(finding.ID), verification.Direction, code(verification.MetricKey), verification.Target, verification.Unit, verification.Description, code(verification.Command))
		}
		for _, window := range finding.SuggestedWindows {
			fmt.Fprintf(b, "  - %s Re-run: %s\n", window.Reason, code("wkperf analyze "+shellQuote(r.Source)+" "+formatFlags(window.Flags)+" --format json"))
		}
	}
	if r.RootCause != nil {
		for _, window := range r.RootCause.Windows {
			fmt.Fprintf(b, "- Inspect %s: %s\n", mdEscape(window.Label), code(window.Command))
		}
	}
	b.WriteString("\nTargets are rough hypotheses. Repeat equivalent captures; a single recording does not establish a performance regression.\n")
}

func renderRootDetails(b *strings.Builder, r *Report) {
	if r.RootCause == nil {
		return
	}
	root := r.RootCause
	b.WriteString("\n## Periodic roots\n\n")
	for _, periodic := range root.PeriodicRoots {
		fmt.Fprintf(b, "### %s\n\n", mdEscape(frameLabel(periodic.Root)))
		renderRootStats(b, periodic.Stats)
		for _, stack := range periodic.Evidence {
			renderStack(b, stack, false)
		}
		for _, fire := range periodic.Fires {
			fmt.Fprintf(b, "- Fire %s: %s\n", mdEscape(fire.Event), code(fire.Command))
		}
	}
	b.WriteString("\n## GC overlap\n\n")
	for _, gc := range root.GCOverlaps {
		fmt.Fprintf(b, "- %s\n", gc.Attribution)
		renderRootStats(b, gc.Stats)
		for _, event := range gc.Events {
			fmt.Fprintf(b, "  - %s temporal overlap %.6gms. Reproduce: %s\n", mdEscape(event.Event), event.OverlapMs, code(event.Command))
		}
	}
	b.WriteString("\n## Stack depth\n\n")
	renderRootStats(b, root.Depth.Stats)
	for _, outlier := range root.Depth.Outliers {
		renderRootStats(b, outlier.Stats)
		renderStack(b, outlier.Stack, false)
		fmt.Fprintf(b, "Reproduce depth outlier: %s\n", code(outlier.Command))
	}
	b.WriteString("\n## Timeline windows and exact sampled stacks\n\n")
	for _, window := range allRootWindows(root) {
		fmt.Fprintf(b, "### %s\n\n", mdEscape(window.Label))
		renderRootStats(b, window.Stats)
		if window.TopStack != nil {
			renderStack(b, *window.TopStack, false)
		} else {
			fmt.Fprintf(b, "%s\n", window.SampleStatus)
		}
		fmt.Fprintf(b, "Reproduce window: %s\n", code(window.Command))
	}
}

func renderTriggers(b *strings.Builder, r *Report, title, prefix string, triggers []TriggerStat) {
	fmt.Fprintf(b, "\n## %s\n\n", title)
	if len(triggers) == 0 {
		b.WriteString("_No matching trigger records._\n")
		return
	}
	for _, t := range triggers {
		base := prefix + triggerMetricPrefix(t)
		fmt.Fprintf(b, "### %s %s\n\n", t.Kind, mdEscape(t.Name))
		b.WriteString("| Metric | Value | Reproduce |\n|---|---:|---|\n")
		for _, item := range []struct {
			key, label string
			value      float64
		}{{"count", "Count", float64(t.Count)}, {"total_ms", "Total ms", t.TotalMs}, {"mean_ms", "Mean ms", t.MeanMs}, {"p95_ms", "p95 ms", t.P95Ms}, {"max_ms", "Max ms", t.MaxMs}} {
			fmt.Fprintf(b, "| %s | %.6g | %s |\n", item.label, item.value, code(NumericCommand(r, base+"."+item.key)))
		}
		if t.SampleMessage != "" {
			fmt.Fprintf(b, "%s\n", t.SampleMessage)
		}
		for _, ranking := range []struct {
			title, key string
			functions  []metrics.FuncStat
		}{{"Top self", "self_ms", t.TopSelf}, {"Top inclusive", "total_ms", t.TopInclusive}} {
			for _, f := range ranking.functions {
				value := f.SelfMs
				if ranking.key == "total_ms" {
					value = f.TotalMs
				}
				fmt.Fprintf(b, "- %s: %s, %.6gms. Reproduce: %s\n", ranking.title, code(functionLabel(f)), value, code(NumericCommand(r, base+"."+functionMetricPrefix(f)+"."+ranking.key)))
			}
		}
	}
}

func renderDetails(b *strings.Builder, r *Report) {
	renderFindings(b, r, false)
	renderRootDetails(b, r)
	b.WriteString("\n## Common sampled stacks\n\n")
	for i, stack := range r.CommonStacks {
		fmt.Fprintf(b, "- Exact stack: %.6gms sampled weight, %d samples. Reproduce: %s; %s\n", stack.WeightMs, stack.Samples, code(StackCommand(r, i, "weight_ms")), code(StackCommand(r, i, "samples")))
		for _, frame := range stack.Frames {
			fmt.Fprintf(b, "  - %s\n", code(functionFrameLabel(frame)))
		}
	}
	b.WriteString("\n## Wall-time breakdown\n\n| Category | ms | % | Activity | Reproduce ms / percent |\n|---|---:|---:|---|---|\n")
	for _, s := range r.Breakdown {
		prefix := "breakdown." + url.PathEscape(s.Name)
		fmt.Fprintf(b, "| %s | %.6g | %.6g | %s | %s; %s |\n", s.Name, s.Ms, s.Pct, bar(s.Pct, 20), code(NumericCommand(r, prefix+".ms")), code(NumericCommand(r, prefix+".pct")))
	}
	b.WriteString("\n## Top modules\n\n| File / module | Self ms | Sample share % | Reproduce self / share |\n|---|---:|---:|---|\n")
	grouping := r.ModuleGrouping
	if grouping == "" {
		grouping = "file"
	}
	for _, m := range r.Modules {
		prefix := "modules." + grouping + "." + url.PathEscape(m.Name)
		fmt.Fprintf(b, "| %s | %.6g | %.6g | %s; %s |\n", code(m.Name), m.SelfMs, m.Pct, code(NumericCommand(r, prefix+".self_ms")), code(NumericCommand(r, prefix+".pct")))
	}
	fmt.Fprintf(b, "\n## Idle wakeups\n\nMeasured idle-gap frame wakeups: %d; rate %.6g/s. Reproduce: %s; %s\n", r.Wakeups.Count, r.Wakeups.PerSecond, code(NumericCommand(r, "wakeups.count")), code(NumericCommand(r, "wakeups.per_second")))
	b.WriteString("\nCause labels identify overlapping measured trigger work, not proof that a callback woke the process.\n")
	renderTriggers(b, r, "Wakeup causes", "wakeups.", r.Wakeups.Causes)
	renderTriggers(b, r, "Event and callback triggers", "", r.Triggers)
	b.WriteString("\n## Slow frames\n\n")
	if len(r.SlowFrames) == 0 {
		b.WriteString("_None over threshold; idle-gap frames are excluded._\n")
	}
	for i, f := range r.SlowFrames {
		prefix := "slowFrames." + strconv.Itoa(i)
		fmt.Fprintf(b, "- %s — %s\n", mdEscape(f.TopEvent), code(f.TopFunc))
		for _, item := range []struct {
			key, label string
			value      float64
		}{{"t", "Time from window start (s)", f.T}, {"ms", "Frame ms", f.Ms}, {"script_ms", "Script ms", f.ScriptMs}, {"layout_ms", "Layout/style ms", f.LayoutMs}, {"paint_ms", "Paint/composite ms", f.PaintMs}, {"gc_ms", "GC ms", f.GCMs}} {
			fmt.Fprintf(b, "  - %s: %.6g. Reproduce: %s\n", item.label, item.value, code(NumericCommand(r, prefix+"."+item.key)))
		}
	}
	b.WriteString("\n## Long tasks\n\n")
	if len(r.LongTasks) == 0 {
		b.WriteString("_None over threshold._\n")
	}
	for i, t := range r.LongTasks {
		prefix := "longTasks." + strconv.Itoa(i)
		fmt.Fprintf(b, "- %s %s — %s; time %.6gs, duration %.6gms. Reproduce: %s; %s\n", mdEscape(t.EventType), mdEscape(t.Detail), code(t.TopFunc), t.T, t.Ms, code(NumericCommand(r, prefix+".t")), code(NumericCommand(r, prefix+".ms")))
	}
}

func markdown(r *Report, o MarkdownOptions) string {
	var b strings.Builder
	fmt.Fprintf(&b, "# wkperf profile: %s\n\n- Source: %s\n- Active duration: %.6gs. Reproduce: %s\n", mdEscape(r.Name), code(r.Source), r.DurationSec, code(NumericCommand(r, "recording.duration_sec")))
	renderFindings(&b, r, true)
	renderHotPath(&b, r, o.Brief)
	renderFunctions(&b, r, o.Brief)
	renderVerification(&b, r)
	if !o.Brief && (o.MaxBytes <= 0 || b.Len() < o.MaxBytes) {
		renderDetails(&b, r)
	}
	return b.String()
}

func boundedMarkdown(s string, maxBytes int) string {
	if maxBytes <= 0 || len(s) <= maxBytes {
		return s
	}
	marker := MarkdownTruncationMarker + "\n"
	if maxBytes < len(marker) {
		return marker[:maxBytes]
	}
	sections := strings.Split(s, "\n## ")
	if len(sections) >= 5 {
		title := strings.SplitN(sections[0], "\n", 2)[0] + "\n"
		headings := make([]string, 4)
		bodies := make([]string, 4)
		reserved := len(title) + len(marker)
		for i := range 4 {
			parts := strings.SplitN(sections[i+1], "\n", 2)
			headings[i] = "\n## " + parts[0] + "\n"
			if len(parts) == 2 {
				bodies[i] = parts[1]
			}
			reserved += len(headings[i])
		}
		if reserved <= maxBytes {
			var b strings.Builder
			b.WriteString(title)
			remaining := maxBytes - reserved
			for i := range 4 {
				b.WriteString(headings[i])
				allowance := remaining / (4 - i)
				body := markdownPrefix(bodies[i], allowance)
				b.WriteString(body)
				remaining -= len(body)
			}
			b.WriteString(marker)
			return b.String()
		}
	}
	return markdownPrefix(s, maxBytes-len(marker)) + marker
}

func markdownPrefix(s string, maxBytes int) string {
	if len(s) <= maxBytes {
		return s
	}
	end := maxBytes
	for end > 0 && !utf8.RuneStart(s[end]) {
		end--
	}
	if newline := strings.LastIndexByte(s[:end], '\n'); newline >= 0 {
		end = newline + 1
	}
	return s[:end]
}

func RenderMarkdownWithOptions(w io.Writer, r *Report, o MarkdownOptions) error {
	_, err := io.WriteString(w, boundedMarkdown(markdown(r, o), o.MaxBytes))
	return err
}

func RenderMarkdown(w io.Writer, r *Report) error {
	return RenderMarkdownWithOptions(w, r, MarkdownOptions{})
}

// RenderTerm shares report order and full source locations with Markdown.
func RenderTerm(w io.Writer, r *Report, color bool) error {
	text := markdown(r, MarkdownOptions{})
	var b strings.Builder
	for _, line := range strings.Split(text, "\n") {
		if strings.HasPrefix(line, "#") {
			line = strings.TrimLeft(line, "# ")
			if color {
				line = "\x1b[1m" + line + "\x1b[0m"
			}
		}
		line = terminalEscaper.Replace(line)
		b.WriteString(line)
		b.WriteByte('\n')
	}
	_, err := io.WriteString(w, b.String())
	return err
}

func allRootWindows(r *rootcause.Report) []rootcause.Window {
	windows := make([]rootcause.Window, 0, len(r.LongTasks)+len(r.SlowFrames)+len(r.EventBursts))
	windows = append(windows, r.LongTasks...)
	windows = append(windows, r.SlowFrames...)
	windows = append(windows, r.EventBursts...)
	if len(windows) == 0 {
		windows = append(windows, r.Windows...)
	}
	sort.Slice(windows, func(i, j int) bool {
		a, b := windows[i], windows[j]
		if a.DurationMs != b.DurationMs {
			return a.DurationMs > b.DurationMs
		}
		if a.FromSec != b.FromSec {
			return a.FromSec < b.FromSec
		}
		if a.ToSec != b.ToSec {
			return a.ToSec < b.ToSec
		}
		if a.Kind != b.Kind {
			return a.Kind < b.Kind
		}
		return a.Label < b.Label
	})
	return windows
}

func formatFlags(flags []string) string {
	var b strings.Builder
	for i, flag := range flags {
		if i > 0 {
			b.WriteByte(' ')
		}
		if strings.ContainsAny(flag, " \t\r\n'\"`$;&|<>()*?![]{}\\") {
			flag = shellQuote(flag)
		}
		b.WriteString(flag)
	}
	return b.String()
}
