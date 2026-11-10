package insights

import (
	"fmt"
	"io"
	"net/url"
	"sort"
	"strconv"
	"strings"

	"github.com/omniviewdev/wkperf/internal/flame"
	"github.com/omniviewdev/wkperf/internal/metrics"
	"github.com/omniviewdev/wkperf/internal/rootcause"
)

const MarkdownTruncationMarker = "[truncated: use JSON for complete report]"

type MarkdownOptions struct {
	Brief    bool
	MaxBytes int
}

// MarkdownDocument keeps optional scorecard text inside the report's byte cap.
type MarkdownDocument struct {
	Report *Report
	Suffix string
}

func mdEscape(s string) string {
	return strings.NewReplacer("|", "\\|", "\n", " ", "\r", " ").Replace(s)
}

// CommonMark code spans use a delimiter longer than any content backtick run.
// Backslashes and pipes are literal outside table cells and must not be escaped.
func code(s string) string {
	s = strings.NewReplacer("\n", " ", "\r", " ").Replace(s)
	longest, run := 0, 0
	for _, c := range s {
		if c == '`' {
			run++
			longest = max(longest, run)
		} else {
			run = 0
		}
	}
	fence := strings.Repeat("`", longest+1)
	if strings.HasPrefix(s, "`") || strings.HasSuffix(s, "`") || (strings.HasPrefix(s, " ") && strings.HasSuffix(s, " ") && strings.Trim(s, " ") != "") {
		return fence + " " + s + " " + fence
	}
	return fence + s + fence
}

func codeCell(s string) string { return code(strings.ReplaceAll(s, "|", "\\|")) }

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

type markdownRenderer struct {
	r          *Report
	brief      bool
	stacks     []StackPreview
	rootPrefix string
	stackMap   map[string]StackPreview
}

func newMarkdownRenderer(r *Report, brief bool) *markdownRenderer {
	r = SelectEvidence(r)
	v := &markdownRenderer{r: r, brief: brief, stacks: StackPreviews(r, brief), stackMap: make(map[string]StackPreview)}
	v.rootPrefix = "wkperf query " + shellQuote(r.Source) + " rootcause "
	for _, stack := range v.stacks {
		v.stackMap[stack.ID] = stack
	}
	return v
}

func numericRef(op, key string) string { return "[" + op + ": " + code(key) + "]" }

func (v *markdownRenderer) valueRef(command, key string) string {
	if strings.HasPrefix(command, v.rootPrefix) {
		return numericRef("rootcause", key)
	}
	return numericRef("metrics", key)
}

func renderRootStats(b *strings.Builder, stats []rootcause.Stat) {
	for _, stat := range stats {
		fmt.Fprintf(b, "- %s: %.6g %s (%s). %s\n", mdEscape(stat.Label), stat.Value, stat.Unit, mdEscape(stat.Basis), numericRef("rootcause", stat.Key))
	}
}

func (v *markdownRenderer) stackReference(id string) string {
	if stack, ok := v.stackMap[id]; ok {
		if v.brief {
			return stack.Reference() + "; " + stack.ID
		}
		return stack.Reference()
	}
	return "stack " + id
}

func (v *markdownRenderer) renderStack(b *strings.Builder, stack rootcause.Stack) {
	id := stack.StackID
	if id == "" {
		id = flame.StackID(stack.Frames)
	}
	fmt.Fprintf(b, "- %s: %.6gms sampled self weight, %d samples.\n", code(v.stackReference(id)), stack.SelfMs, stack.Samples)
}

func (v *markdownRenderer) header() string {
	r := v.r
	var b strings.Builder
	fmt.Fprintf(&b, "# wkperf profile: %s\n\n- Source: %s\n- Active duration: %.6gs. %s\n", mdEscape(r.Name), code(r.Source), r.DurationSec, numericRef("metrics", "recording.duration_sec"))
	if coverage := r.SampleCoverage; coverage.UntimedSampleCount > 0 {
		accounting := "included in rankings, excluded from frame, task, trigger, window and periodic attribution"
		if coverage.UntimedExcluded {
			accounting = "excluded from this window and all time-based attribution"
		}
		fmt.Fprintf(&b, "- Untimed samples: %d (%.1f ms): %s.\n", coverage.UntimedSampleCount, coverage.UntimedMs, accounting)
	}
	return b.String()
}

func (v *markdownRenderer) findings() []string {
	rows := []string{"\n## Findings\n\n"}
	if len(v.r.Findings) == 0 {
		return append(rows, "_No issues detected by the available instruments._\n")
	}
	for _, f := range v.r.Findings {
		rows = append(rows, fmt.Sprintf("- [%s] %s: %s; impact %.6gms (%.6g%%). %s\n", strings.ToUpper(string(f.Severity)), code(f.ID), mdEscape(f.Title), f.ImpactMs, f.ImpactPct, numericRef("metrics", "findings."+f.ID+".impact_ms")))
	}
	return rows
}

func (v *markdownRenderer) findingFacets() []string {
	var rows []string
	for _, f := range v.r.Findings {
		var b strings.Builder
		for _, facet := range FacetLabels(f.NativeReads, f.Depth, f.RelatedFindings) {
			fmt.Fprintf(&b, "- %s\n", mdEscape(facet))
		}
		for _, evidence := range f.NumericEvidence {
			if strings.Contains(evidence.MetricKey, "react.render.") || strings.Contains(evidence.MetricKey, "react.commit.") || strings.HasSuffix(evidence.MetricKey, ".samples") {
				fmt.Fprintf(&b, "- %s: %.6g %s. %s\n", mdEscape(evidence.Description), evidence.Value, evidence.Unit, v.valueRef(evidence.Command, evidence.MetricKey))
			}
		}
		if b.Len() > 0 {
			rows = append(rows, "\n### Evidence facets: "+code(f.ID)+"\n\n"+b.String())
		}
	}
	return rows
}

func (v *markdownRenderer) context() string {
	metric := NumericCommand(v.r, "")
	metric = strings.Replace(metric, " metrics ''", " metrics", 1)
	root := "wkperf query " + shellQuote(v.r.Source) + " rootcause --format json" + queryFlags(v.r)
	return "\nReproduce values using the metric key after its operation; source, window, source maps and ownership URL rules apply throughout this report.\n\n- Metrics: " + code(metric) + "\n- Root cause: " + code(root) + "\n"
}

func renderPath(b *strings.Builder, path rootcause.Path, brief bool) {
	root, leaf, elided := PreviewFrames(path.Frames, brief)
	printFrame := func(i int, frame flame.Frame) {
		if i < len(path.Nodes) {
			node := path.Nodes[i]
			fmt.Fprintf(b, "- %s: %.6gms inclusive, %.6g%% share, %d samples.\n", code(frameLabel(frame)), node.TotalMs, node.Share*100, node.Samples)
			if len(node.Stats) > 0 {
				fmt.Fprintf(b, "  Inclusive value: %s\n", numericRef("rootcause", node.Stats[0].Key))
			}
		} else {
			fmt.Fprintf(b, "- %s\n", code(frameLabel(frame)))
		}
	}
	for i, frame := range root {
		printFrame(i, frame)
	}
	if elided > 0 {
		fmt.Fprintf(b, "- _… %d frames elided; exact evidence remains available through the query._\n", elided)
	}
	for i, frame := range leaf {
		printFrame(len(path.Frames)-len(leaf)+i, frame)
	}
	if path.StopReason != "" {
		fmt.Fprintf(b, "- Hot path stop: %s.\n", mdEscape(path.StopReason))
	}
	if path.BranchesTotal > 0 {
		fmt.Fprintf(b, "- Competing branches: %d of %d shown.\n", len(path.Branches), path.BranchesTotal)
		for _, node := range path.Branches {
			fmt.Fprintf(b, "  - %s: %.6gms inclusive, %.6g%% share, %d samples.\n", code(frameLabel(node.Frame)), node.TotalMs, node.Share*100, node.Samples)
			if len(node.Stats) > 0 {
				fmt.Fprintf(b, "    Inclusive value: %s\n", numericRef("rootcause", node.Stats[0].Key))
			}
		}
	}
}

func (v *markdownRenderer) hotPath() string {
	var b strings.Builder
	b.WriteString("\n## Hot path\n\n")
	root := v.r.RootCause
	if root == nil {
		b.WriteString("_No sampled hot path available._\n")
		return b.String()
	}
	if len(root.HotPath.Frames) == 0 {
		b.WriteString("_No supported sampled hot path available._\n")
	}
	b.WriteString("Heaviest root-to-leaf prefix; sampled weight estimates cost, not measured script wall time.\n\n")
	renderPath(&b, root.HotPath, v.brief)
	renderRootStats(&b, root.HotPath.Stats)
	if !v.brief && root.HotPath.Command != "" {
		fmt.Fprintf(&b, "Reproduce hot path: %s\n", code(root.HotPath.Command))
	}
	b.WriteString("\n### Owners and nearest non-framework frames\n\nOwnership heuristic: name table + URL rules; bundled code without source maps may belong to a library. Attribution is context, not proof of causation.\n\n")
	for _, owner := range root.Owners {
		fmt.Fprintf(&b, "- Owner %s: %.6gms, %.6g%%, %d samples.\n", owner.Category, owner.SelfMs, owner.Pct, owner.Samples)
		if !v.brief {
			renderRootStats(&b, owner.Stats)
		}
	}
	attributions := root.ContextAttribution
	if v.brief {
		attributions = attributions[:min(3, len(attributions))]
	}
	for _, attribution := range attributions {
		fmt.Fprintf(&b, "- Nearest non-framework frame %s [%s]: %.6gms total (%.6gms self, %.6gms framework, %.6gms native).\n", code(frameLabel(attribution.Frame)), attribution.Frame.Category, attribution.TotalMs, attribution.SelfMs, attribution.FrameworkMs, attribution.NativeMs)
		if !v.brief {
			renderRootStats(&b, attribution.Stats)
			for _, stack := range attribution.Stacks {
				v.renderStack(&b, stack)
			}
			fmt.Fprintf(&b, "  Reproduce attribution: %s\n", code(attribution.Command))
		}
	}
	if root.ContextAttributionTotal > len(attributions) {
		fmt.Fprintf(&b, "- Context attributions: %d of %d shown; inspect rootcause for remaining evidence.\n", len(attributions), root.ContextAttributionTotal)
	}
	if unavailable := root.ContextUnavailable; unavailable.Samples > 0 {
		fmt.Fprintf(&b, "- No non-framework frame on this stack: %.6gms, %d samples. %s\n", unavailable.SelfMs, unavailable.Samples, mdEscape(unavailable.Reason))
		if !v.brief {
			renderRootStats(&b, unavailable.Stats)
		}
	}
	return b.String()
}

func (v *markdownRenderer) functions() string {
	var b strings.Builder
	b.WriteString("\n## Hot functions\n\n")
	for _, ranking := range []struct {
		title     string
		functions []metrics.FuncStat
	}{{"Top hotspots (self time)", v.r.Hotspots}, {"Inclusive ranking (overlapping; do not sum)", v.r.InclusiveHotspots}} {
		fmt.Fprintf(&b, "### %s\n\n", ranking.title)
		if len(ranking.functions) == 0 {
			b.WriteString("_No samples._\n")
			continue
		}
		b.WriteString("| Function | Self ms | Inclusive ms | Self samples | Metric keys |\n|---|---:|---:|---:|---|\n")
		functions := ranking.functions
		if v.brief {
			functions = functions[:min(3, len(functions))]
		}
		for _, f := range functions {
			prefix := functionMetricPrefix(f)
			fmt.Fprintf(&b, "| %s | %.6g | %.6g | %d | %s / %s |\n", codeCell(functionLabel(f)), f.SelfMs, f.TotalMs, f.SelfSamples, codeCell(prefix+".self_ms"), codeCell(prefix+".total_ms"))
			if v.brief {
				continue
			}
			for _, relation := range []struct {
				name  string
				edges []metrics.CallEdge
			}{{"caller", f.Callers}, {"callee", f.Callees}} {
				for _, edge := range relation.edges {
					key := prefix + "." + relation.name + "s." + url.PathEscape(edge.Function.Key()) + ".weight_ms"
					fmt.Fprintf(&b, "| ↳ %s: %s | | %.6g | %d | %s |\n", relation.name, codeCell(functionFrameLabel(edge.Function)), edge.WeightMs, edge.Samples, codeCell(key))
				}
			}
		}
		if !v.brief {
			for _, f := range functions {
				if len(f.CommonStack) > 0 {
					id := f.CommonStackID
					if id == "" {
						id = MetricStackID(f.CommonStack)
					}
					fmt.Fprintf(&b, "- Common sampled stack for %s: %s.\n", code(functionLabel(f)), code(v.stackReference(id)))
				}
			}
		}
		if len(functions) < len(ranking.functions) {
			fmt.Fprintf(&b, "\n%d of %d ranked functions shown.\n", len(functions), len(ranking.functions))
		}
	}
	return b.String()
}

func (v *markdownRenderer) verification() string {
	var b strings.Builder
	b.WriteString("\n## Verification\n\n")
	windows, shown := 0, 0
	for _, finding := range v.r.Findings {
		checks := finding.Verify
		if v.brief {
			checks = checks[:min(1, len(checks))]
		}
		for _, check := range checks {
			fmt.Fprintf(&b, "- %s: %s %s toward roughly %.6g %s. %s\n", code(finding.ID), mdEscape(check.Direction), v.valueRef(check.Command, check.MetricKey), check.Target, mdEscape(string(check.Unit)), mdEscape(check.Description))
		}
		for _, window := range finding.SuggestedWindows {
			windows++
			if v.brief && shown >= 5 {
				continue
			}
			fmt.Fprintf(&b, "- Verify window %.6g–%.6gs: %s. Window flags: %s\n", window.FromSec, window.ToSec, mdEscape(window.Reason), code(formatFlags(window.Flags)))
			shown++
		}
	}
	if v.r.RootCause != nil {
		for _, window := range v.r.RootCause.Windows {
			windows++
			if v.brief && shown >= 5 {
				continue
			}
			if window.TopStack == nil {
				key := ""
				for _, stat := range window.Stats {
					if strings.HasSuffix(stat.Key, ".duration_ms") {
						key = stat.Key
						break
					}
				}
				fmt.Fprintf(&b, "- Inspect %s (no samples; timeline-only): %s\n", mdEscape(window.Label), numericRef("rootcause", key))
			} else if v.brief {
				fmt.Fprintf(&b, "- Inspect %s, %.6g–%.6gs: %s.\n", mdEscape(window.Label), window.FromSec, window.ToSec, code(v.stackReference(window.TopStack.StackID)))
			} else {
				fmt.Fprintf(&b, "- Inspect %s: %s\n", mdEscape(window.Label), code(window.Command))
			}
			shown++
		}
	}
	if windows > shown {
		fmt.Fprintf(&b, "- Verification windows: %d of %d shown; %d omitted.\n", shown, windows, windows-shown)
	}
	b.WriteString("\nTargets are hypotheses. Repeat equivalent captures; one recording does not establish a regression.\n")
	return b.String()
}

func (v *markdownRenderer) findingDetails() []string {
	var blocks []string
	findings := v.r.Findings
	if v.brief {
		findings = findings[:min(3, len(findings))]
	}
	for i, f := range findings {
		var b strings.Builder
		if i == 0 {
			b.WriteString("\n## Finding details\n\n")
		}
		fmt.Fprintf(&b, "### [%s] %s — %s\n\nConfidence: %s. **Suggestion:** %s\n", strings.ToUpper(string(f.Severity)), mdEscape(f.Title), code(f.ID), mdEscape(f.Confidence), mdEscape(f.Suggestion))
		if !v.brief {
			for _, e := range f.NumericEvidence {
				fmt.Fprintf(&b, "- %s: %.6g %s (%s). %s\n", mdEscape(e.Description), e.Value, e.Unit, mdEscape(e.Basis), numericRef("metrics", e.MetricKey))
			}
			for _, reason := range f.ConfidenceReasons {
				fmt.Fprintf(&b, "- Confidence: %s\n", mdEscape(reason))
			}
			for _, stack := range f.Stacks {
				v.renderStack(&b, stack)
			}
			fmt.Fprintf(&b, "Stack evidence: %d of %d shown.\n", len(f.Stacks), f.StacksTotal)
			if len(f.Functions) > 0 {
				fmt.Fprintf(&b, "- Measured sampled functions: %s\n", code(strings.Join(f.Functions, "; ")))
			}
			if len(f.Triggers) > 0 {
				fmt.Fprintf(&b, "- Measured triggers: %s\n", code(strings.Join(f.Triggers, "; ")))
			}
			if f.Command != "" {
				fmt.Fprintf(&b, "Evidence query: %s\n", code(f.Command))
			}
			for _, node := range f.Nodes {
				fmt.Fprintf(&b, "- Recorded %s node: %s %s\n", mdEscape(node.EventType), code(node.CSSPath), code(node.DisplayName))
			}
		}
		blocks = append(blocks, b.String())
	}
	if v.brief && len(v.r.Findings) > len(findings) {
		blocks = append(blocks, fmt.Sprintf("\nFinding details: %d of %d shown; all finding identities appear above.\n", len(findings), len(v.r.Findings)))
	}
	return blocks
}

func (v *markdownRenderer) rootDetails() string {
	root := v.r.RootCause
	if root == nil {
		return ""
	}
	var b strings.Builder
	b.WriteString("\n## Periodic roots\n\n")
	for _, periodic := range root.PeriodicRoots {
		fmt.Fprintf(&b, "### %s [%s]\n\n%s\n", mdEscape(frameLabel(periodic.Root)), mdEscape(periodic.Kind), mdEscape(periodic.SampleStatus))
		renderRootStats(&b, periodic.Stats)
		for _, stack := range periodic.Evidence {
			v.renderStack(&b, stack)
		}
		for _, fire := range periodic.Fires {
			fmt.Fprintf(&b, "- Fire %s: %s\n", mdEscape(fire.Event), code(fire.Command))
		}
		fmt.Fprintf(&b, "Fires: %d of %d shown; stack evidence: %d of %d shown.\n", len(periodic.Fires), periodic.FiresTotal, len(periodic.Evidence), periodic.EvidenceTotal)
	}
	b.WriteString("\n## GC overlap\n\n")
	for _, gc := range root.GCOverlaps {
		fmt.Fprintf(&b, "- %s\n", mdEscape(gc.Attribution))
		renderRootStats(&b, gc.Stats)
		for _, event := range gc.Events {
			fmt.Fprintf(&b, "- %s temporal overlap %.6gms. Reproduce: %s\n", mdEscape(event.Event), event.OverlapMs, code(event.Command))
		}
		fmt.Fprintf(&b, "Overlapping events: %d of %d shown.\n", len(gc.Events), gc.EventsTotal)
	}
	b.WriteString("\n## Stack depth\n\n")
	renderRootStats(&b, root.Depth.Stats)
	for _, outlier := range root.Depth.Outliers {
		renderRootStats(&b, outlier.Stats)
		v.renderStack(&b, outlier.Stack)
	}
	fmt.Fprintf(&b, "Outliers: %d of %d shown.\n", len(root.Depth.Outliers), root.Depth.OutliersTotal)
	b.WriteString("\n## Timeline windows and sampled stack references\n\n")
	for _, window := range allRootWindows(root) {
		fmt.Fprintf(&b, "### %s — %s\n\n", mdEscape(window.Kind), mdEscape(window.Label))
		renderRootStats(&b, window.Stats)
		if window.HotPath != nil {
			renderPath(&b, *window.HotPath, false)
		}
		if window.TopStack != nil {
			v.renderStack(&b, *window.TopStack)
		}
		fmt.Fprintf(&b, "%s\nReproduce window: %s\n", mdEscape(window.SampleStatus), code(window.Command))
	}
	fmt.Fprintf(&b, "Timeline lists shown/total: long tasks %d/%d; slow frames %d/%d; input stalls %d/%d; event bursts %d/%d.\n", len(root.LongTasks), root.LongTasksTotal, len(root.SlowFrames), root.SlowFramesTotal, len(root.InputStalls), root.InputStallsTotal, len(root.EventBursts), root.EventBurstsTotal)
	return b.String()
}

func (v *markdownRenderer) otherDetails() string {
	r := v.r
	var b strings.Builder
	b.WriteString("\n## Common sampled stacks\n\n")
	for _, stack := range r.CommonStacks {
		id := stack.ID
		if id == "" {
			id = MetricStackID(stack.Frames)
		}
		fmt.Fprintf(&b, "- %s: %.6gms sampled weight, %d samples.\n", code(v.stackReference(id)), stack.WeightMs, stack.Samples)
	}
	b.WriteString("\n## Wall-time breakdown\n\n| Category | ms | % | Metric keys |\n|---|---:|---:|---|\n")
	for _, s := range r.Breakdown {
		prefix := "breakdown." + url.PathEscape(s.Name)
		fmt.Fprintf(&b, "| %s | %.6g | %.6g | %s / %s |\n", mdEscape(s.Name), s.Ms, s.Pct, codeCell(prefix+".ms"), codeCell(prefix+".pct"))
	}
	fmt.Fprintf(&b, "\nCollector overlay: %.6gms; %.6gms overlaps instrumented main-thread work. Collector intervals are not necessarily main-thread time and are excluded from the exclusive breakdown. %s; %s\n", r.GCMs, r.GCOverlapWithWorkMs, numericRef("metrics", "gc.total_ms"), numericRef("metrics", "gc.overlap_with_work_ms"))
	b.WriteString("\n## Top modules\n\n| File / module | Self ms | Sample share % | Metric keys |\n|---|---:|---:|---|\n")
	grouping := r.ModuleGrouping
	if grouping == "" {
		grouping = "file"
	}
	for _, m := range r.Modules {
		prefix := "modules." + grouping + "." + url.PathEscape(m.Name)
		fmt.Fprintf(&b, "| %s | %.6g | %.6g | %s / %s |\n", codeCell(m.Name), m.SelfMs, m.Pct, codeCell(prefix+".self_ms"), codeCell(prefix+".pct"))
	}
	fmt.Fprintf(&b, "\n## Idle wakeups\n\nMeasured idle-gap frame wakeups: %d; rate %.6g/s. %s; %s\n", r.Wakeups.Count, r.Wakeups.PerSecond, numericRef("metrics", "wakeups.count"), numericRef("metrics", "wakeups.per_second"))
	for _, group := range []struct {
		title, prefix string
		rows          []TriggerStat
	}{{"Wakeup causes", "wakeups.", r.Wakeups.Causes}, {"Event and callback triggers", "", r.Triggers}} {
		fmt.Fprintf(&b, "\n## %s\n\n", group.title)
		for _, trigger := range group.rows {
			base := group.prefix + triggerMetricPrefix(trigger)
			fmt.Fprintf(&b, "### %s %s\n\n%s\n", mdEscape(trigger.Kind), code(trigger.Name), mdEscape(trigger.SampleMessage))
			for _, value := range []struct {
				key, label string
				value      float64
			}{{"count", "Count", float64(trigger.Count)}, {"total_ms", "Total ms", trigger.TotalMs}, {"mean_ms", "Mean ms", trigger.MeanMs}, {"p95_ms", "p95 ms", trigger.P95Ms}, {"max_ms", "Max ms", trigger.MaxMs}} {
				fmt.Fprintf(&b, "- %s: %.6g. %s\n", value.label, value.value, numericRef("metrics", base+"."+value.key))
			}
			for _, ranking := range []struct {
				title, field string
				functions    []metrics.FuncStat
			}{{"Self", "self_ms", trigger.TopSelf}, {"Inclusive", "total_ms", trigger.TopInclusive}} {
				for _, function := range ranking.functions {
					value := function.SelfMs
					if ranking.field == "total_ms" {
						value = function.TotalMs
					}
					fmt.Fprintf(&b, "- %s %s: %.6gms. %s\n", ranking.title, code(functionLabel(function)), value, numericRef("metrics", base+"."+functionMetricPrefix(function)+"."+ranking.field))
				}
			}
		}
	}
	b.WriteString("\n## Slow frames\n\n")
	for i, frame := range r.SlowFrames {
		prefix := "slowFrames." + strconv.Itoa(i)
		fmt.Fprintf(&b, "- %s — %s. %s\n", mdEscape(frame.TopEvent), code(frame.TopFunc), mdEscape(frame.SampleStatus))
		for _, value := range []struct {
			key, label string
			value      float64
		}{{"t", "Time from window start (s)", frame.T}, {"ms", "Observed interval ms", frame.Ms}, {"work_ms", "Exclusive main-thread work ms", frame.WorkMs}, {"script_ms", "Script ms", frame.ScriptMs}, {"layout_ms", "Layout/style ms", frame.LayoutMs}, {"paint_ms", "Paint/composite ms", frame.PaintMs}, {"gc_ms", "Collector overlay ms", frame.GCMs}, {"gc_overlap_with_work_ms", "Collector overlap with work ms", frame.GCOverlapWithWorkMs}} {
			fmt.Fprintf(&b, "  - %s: %.6g. %s\n", value.label, value.value, numericRef("metrics", prefix+"."+value.key))
		}
	}
	fmt.Fprintf(&b, "Slow frames: %d of %d shown; configured finding threshold %.6gms. Fixed scorecard standards use 16.7ms.\n", len(r.SlowFrames), r.SlowFramesTotal, r.JankMs)
	b.WriteString("\n## Input stalls\n\nInput-driven rendering intervals with at most one 16.7ms budget of measured work; excluded from work-bound jank. Observed interval length is not measured main-thread blocking time.\n\n")
	for i, stall := range r.InputStalls {
		prefix := "inputStalls." + strconv.Itoa(i)
		fmt.Fprintf(&b, "- %s — %s. %s\n", mdEscape(stall.TopEvent), code(stall.TopFunc), mdEscape(stall.SampleStatus))
		for _, value := range []struct {
			key, label string
			value      float64
		}{{"t", "Time from window start (s)", stall.T}, {"ms", "Observed interval ms", stall.Ms}, {"work_ms", "Exclusive main-thread work ms", stall.WorkMs}} {
			fmt.Fprintf(&b, "  - %s: %.6g. %s\n", value.label, value.value, numericRef("metrics", prefix+"."+value.key))
		}
	}
	fmt.Fprintf(&b, "Input stalls: %d of %d shown.\n", len(r.InputStalls), r.InputStallsTotal)
	b.WriteString("\n## Long tasks\n\n")
	for i, task := range r.LongTasks {
		fmt.Fprintf(&b, "- At %.6gs: %.6gms; %s %s — %s. %s %s\n", task.T, task.Ms, mdEscape(task.EventType), mdEscape(task.Detail), code(task.TopFunc), mdEscape(task.SampleStatus), numericRef("metrics", "longTasks."+strconv.Itoa(i)+".ms"))
		if len(task.Children) > 0 {
			fmt.Fprintf(&b, "  - Nested event types (%d of %d shown): %s.\n", len(task.Children), task.ChildrenTotal, code(strings.Join(task.Children, ", ")))
		}
	}
	fmt.Fprintf(&b, "Long tasks: %d of %d shown; overlapping script records count once.\n", len(r.LongTasks), r.LongTasksTotal)
	return b.String()
}

func (v *markdownRenderer) appendix() string {
	if len(v.stacks) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("\n## Stacks\n\nEach distinct selected stack appears once. Frames run root to leaf; query returns every exact frame.\n")
	for _, stack := range v.stacks {
		fmt.Fprintf(&b, "\n### %s — %s (%d frames)\n\n", stack.Label, code(stack.ID), stack.Depth)
		for _, frame := range stack.Root {
			fmt.Fprintf(&b, "- %s\n", code(frameLabel(frame)))
		}
		if stack.Elided > 0 {
			fmt.Fprintf(&b, "- _… %d frames elided._\n", stack.Elided)
		}
		for _, frame := range stack.Leaf {
			fmt.Fprintf(&b, "- %s\n", code(frameLabel(frame)))
		}
		fmt.Fprintf(&b, "Full stack: %s\n", code(stack.Command))
	}
	return b.String()
}

func allRootWindows(r *rootcause.Report) []rootcause.Window {
	windows := make([]rootcause.Window, 0, len(r.LongTasks)+len(r.SlowFrames)+len(r.InputStalls)+len(r.EventBursts))
	windows = append(windows, r.LongTasks...)
	windows = append(windows, r.SlowFrames...)
	windows = append(windows, r.InputStalls...)
	windows = append(windows, r.EventBursts...)
	if len(windows) == 0 {
		windows = append(windows, r.Windows...)
	}
	sort.Slice(windows, func(i, j int) bool {
		a, b := windows[i], windows[j]
		if a.ActiveMs != b.ActiveMs {
			return a.ActiveMs > b.ActiveMs
		}
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

func RenderMarkdown(w io.Writer, r *Report) error {
	return RenderMarkdownWithOptions(w, r, MarkdownOptions{})
}

func RenderMarkdownWithOptions(w io.Writer, r *Report, o MarkdownOptions) error {
	return RenderMarkdownDocuments(w, []MarkdownDocument{{Report: r}}, "", o)
}

// RenderTerm shares evidence, priority order and byte limits with Markdown.
func RenderTerm(w io.Writer, r *Report, color bool) error {
	return RenderTermWithOptions(w, r, color, MarkdownOptions{})
}

func RenderTermWithOptions(w io.Writer, r *Report, color bool, o MarkdownOptions) error {
	return RenderTermDocuments(w, []MarkdownDocument{{Report: r}}, "", color, o)
}
