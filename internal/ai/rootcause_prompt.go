package ai

import (
	"fmt"
	"strings"

	"github.com/omniviewdev/wkperf/internal/flame"
	"github.com/omniviewdev/wkperf/internal/insights"
	"github.com/omniviewdev/wkperf/internal/metrics"
	"github.com/omniviewdev/wkperf/internal/rootcause"
)

func rootFrame(f flame.Frame) string {
	frame := metrics.FunctionFrame{Name: f.Name, URL: f.URL, Line: f.Line, Column: f.Column, SourceID: f.SourceID}
	if f.Original != nil {
		frame.Original = &metrics.SourceLocation{Name: f.Original.Name, URL: f.Original.URL, Line: f.Original.Line, Column: f.Original.Column}
	}
	return funcName(f.Name) + " @ " + functionLocation(frame)
}

func rootStats(b *strings.Builder, stats []rootcause.Stat, n int) {
	for _, stat := range limitSlice(stats, n) {
		fmt.Fprintf(b, "  - %s=%s %s [%s]; reproduce: %s\n", stat.Key, num(stat.Value), clip(oneLine(stat.Unit), 30), clip(oneLine(stat.Basis), 120), stat.Command)
	}
	if len(stats) > n {
		fmt.Fprintf(b, "  - %d additional numeric rows omitted.\n", len(stats)-n)
	}
}

func rootFrames(b *strings.Builder, frames []flame.Frame) {
	root, leaf, elided := insights.PreviewFrames(frames, false)
	for _, frame := range root {
		fmt.Fprintf(b, "  → %s\n", rootFrame(frame))
	}
	if elided > 0 {
		fmt.Fprintf(b, "  → [%d frames elided; use reproduction command for exact stack]\n", elided)
	}
	for _, frame := range leaf {
		fmt.Fprintf(b, "  → %s\n", rootFrame(frame))
	}
}

func rootStack(b *strings.Builder, stack rootcause.Stack) {
	window := "unknown clock"
	if stack.FirstSec != nil && stack.LastSec != nil {
		window = num(*stack.FirstSec) + "–" + num(*stack.LastSec) + "s"
	}
	id := stack.StackID
	if id == "" {
		id = flame.StackID(stack.Frames)
	}
	leaf := ""
	if len(stack.Frames) > 0 {
		leaf = rootFrame(stack.Frames[len(stack.Frames)-1])
	}
	fmt.Fprintf(b, "Exact sampled stack reference: %s (%d frames; leaf %s): self=%sms samples=%d window=%s; reproduce: %s\n", id, len(stack.Frames), leaf, num(stack.SelfMs), stack.Samples, window, stack.Command)
	rootStats(b, stack.Stats, 4)
}

func rootWindow(b *strings.Builder, window rootcause.Window) {
	fmt.Fprintf(b, "- %s %s, %s–%ss: duration=%sms active=%sms; %s; reproduce: %s\n", clip(oneLine(window.Kind), 60), clip(oneLine(window.Label), 100), num(window.FromSec), num(window.ToSec), num(window.DurationMs), num(window.ActiveMs), clip(oneLine(window.SampleStatus), 150), window.Command)
	rootStats(b, window.Stats, 6)
	if window.TopStack != nil {
		rootStack(b, *window.TopStack)
	}
}

func rootCauseSections(label string, r *rootcause.Report, c Context, value int) []section {
	if r == nil {
		return nil
	}
	var sections []section
	add := func(name string, priority int, body string) {
		sections = append(sections, section{title: label + " " + name, value: priority, body: body})
	}
	var b strings.Builder
	fmt.Fprintf(&b, "### %s: Hot path (heaviest sampled prefix, root → leaf)\nSampled time is an estimate, not instrumented wall time. Reproduce: %s\n", label, r.HotPath.Command)
	rootFrames(&b, r.HotPath.Frames)
	rootStats(&b, r.HotPath.Stats, 6)
	add("hot path", value, b.String())
	b.Reset()
	b.WriteString("### Owner breakdown and nearest application attribution\nExclusive sampled self time; framework/native charges follow the nearest application ancestor, not proven causation.\n")
	for _, owner := range limitSlice(r.Owners, c.TopN) {
		fmt.Fprintf(&b, "- Owner %s: self=%sms share=%s%% samples=%d; reproduce: %s\n", clip(oneLine(owner.Category), 60), num(owner.SelfMs), num(owner.Pct), owner.Samples, owner.Command)
		rootStats(&b, owner.Stats, 4)
	}
	for _, app := range limitSlice(r.AppAttribution, c.TopN) {
		fmt.Fprintf(&b, "- Nearest app %s: app self=%sms framework=%sms native=%sms total=%sms; reproduce: %s\n", rootFrame(app.App), num(app.SelfMs), num(app.FrameworkMs), num(app.NativeMs), num(app.TotalMs), app.Command)
		rootStats(&b, app.Stats, 6)
		for _, stack := range limitSlice(app.Stacks, 1) {
			rootStack(&b, stack)
		}
	}
	add("owners", value-1, b.String())
	if len(r.Findings) > 0 {
		b.Reset()
		b.WriteString("### Exact-stack root-cause findings (interpretations remain hypotheses)\n")
		for _, finding := range limitSlice(r.Findings, c.MaxFindings) {
			fmt.Fprintf(&b, "- %s [%s] %s: %s; reproduce: %s\n", clip(oneLine(finding.ID), 80), clip(oneLine(finding.Severity), 40), clip(oneLine(finding.Title), 150), clip(oneLine(finding.Explanation), 500), finding.Command)
			rootStats(&b, finding.Stats, 6)
			for _, stack := range limitSlice(finding.Evidence, 2) {
				rootStack(&b, stack)
			}
			for _, window := range limitSlice(finding.Windows, 3) {
				fmt.Fprintf(&b, "  window: %s–%ss; reproduce: %s\n", num(window.FromSec), num(window.ToSec), window.Command)
			}
		}
		add("exact stacks", value-2, b.String())
	}
	if len(r.PeriodicRoots) > 0 {
		b.Reset()
		b.WriteString("### Periodic roots\n")
		for _, periodic := range limitSlice(r.PeriodicRoots, c.TopN) {
			fmt.Fprintf(&b, "- %s: fires=%d period=%sms jitter=%sms regularity=%s mean cost=%sms total=%sms; reproduce: %s\n", rootFrame(periodic.Root), periodic.FireCount, num(periodic.PeriodMs), num(periodic.JitterMs), num(periodic.Regularity), num(periodic.MeanCostMs), num(periodic.TotalMs), periodic.Command)
			rootStats(&b, periodic.Stats, 7)
			for _, fire := range limitSlice(periodic.Fires, 3) {
				fmt.Fprintf(&b, "  fire %s–%ss self=%sms samples=%d event=%s; reproduce: %s\n", num(fire.FromSec), num(fire.ToSec), num(fire.SelfMs), fire.Samples, clip(oneLine(fire.Event), 100), fire.Command)
				rootStats(&b, fire.Stats, 4)
			}
			for _, stack := range limitSlice(periodic.Evidence, 1) {
				rootStack(&b, stack)
			}
		}
		add("periodic roots", value-5, b.String())
	}
	if len(r.GCOverlaps) > 0 {
		b.Reset()
		b.WriteString("### GC timeline overlap (not allocation causality)\n")
		for _, gc := range limitSlice(r.GCOverlaps, c.TopN) {
			fmt.Fprintf(&b, "- %s–%ss duration=%sms: %s; reproduce: %s\n", num(gc.FromSec), num(gc.ToSec), num(gc.DurationMs), clip(oneLine(gc.Attribution), 400), gc.Command)
			rootStats(&b, gc.Stats, 4)
			for _, event := range limitSlice(gc.Events, 3) {
				fmt.Fprintf(&b, "  overlap %s %s–%ss: %sms; reproduce: %s\n", clip(oneLine(event.Event), 100), num(event.FromSec), num(event.ToSec), num(event.OverlapMs), event.Command)
				rootStats(&b, event.Stats, 4)
			}
		}
		add("GC overlaps", value-6, b.String())
	}
	b.Reset()
	b.WriteString("### Stack depth and outliers\n")
	rootStats(&b, r.Depth.Stats, 8)
	for _, outlier := range limitSlice(r.Depth.Outliers, 2) {
		fmt.Fprintf(&b, "- depth=%d repeated-frame=%d commit=%d; reproduce: %s\n", outlier.Depth, outlier.RepeatedFrameDepth, outlier.CommitDepth, outlier.Command)
		rootStats(&b, outlier.Stats, 3)
		rootStack(&b, outlier.Stack)
	}
	add("depth", value-10, b.String())
	if len(r.Windows)+len(r.LongTasks)+len(r.SlowFrames)+len(r.EventBursts) > 0 {
		b.Reset()
		b.WriteString("### Worst-window triage\n")
		for _, window := range limitSlice(r.Windows, c.TopN) {
			rootWindow(&b, window)
		}
		for _, window := range limitSlice(r.LongTasks, 3) {
			rootWindow(&b, window)
		}
		for _, window := range limitSlice(r.SlowFrames, 3) {
			rootWindow(&b, window)
		}
		for _, window := range limitSlice(r.EventBursts, 3) {
			rootWindow(&b, window)
		}
		add("worst-window triage", value-3, b.String())
	}
	return sections
}

func stackAppendixSection(label string, report *insights.Report, value int) section {
	var b strings.Builder
	fmt.Fprintf(&b, "### %s: Stack appendix\nEach distinct selected stack appears once; queries return every exact frame.\n", label)
	for _, stack := range insights.StackPreviews(report, false) {
		fmt.Fprintf(&b, "Exact stack %s (%s; %d frames); reproduce: %s\n", stack.ID, stack.Label, stack.Depth, stack.Command)
		for _, frame := range stack.Root {
			fmt.Fprintf(&b, "  → %s\n", rootFrame(frame))
		}
		if stack.Elided > 0 {
			fmt.Fprintf(&b, "  → [%d frames elided]\n", stack.Elided)
		}
		for _, frame := range stack.Leaf {
			fmt.Fprintf(&b, "  → %s\n", rootFrame(frame))
		}
	}
	return section{title: label + " stack appendix", value: value, body: b.String()}
}
