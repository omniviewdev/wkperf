package ai

import (
	"fmt"
	"strings"

	"github.com/omniviewdev/wkperf/internal/audit"
	"github.com/omniviewdev/wkperf/internal/compare"
	"github.com/omniviewdev/wkperf/internal/insights"
	"github.com/omniviewdev/wkperf/internal/metrics"
)

func functionLocation(f metrics.FunctionFrame) string {
	f.URL = tailLocation(oneLine(f.URL))
	f.SourceID = clip(oneLine(f.SourceID), maxLocChars)
	if f.Original != nil {
		original := *f.Original
		original.URL = tailLocation(oneLine(original.URL))
		f.Original = &original
	}
	return f.Location()
}

func tailLocation(s string) string {
	runes := []rune(s)
	if len(runes) > maxLocChars {
		return "…" + string(runes[len(runes)-maxLocChars+1:])
	}
	return s
}

func diffLocation(f compare.FuncDiff) string {
	return functionLocation(metrics.FunctionFrame{Name: f.Name, URL: f.URL, Line: f.Line, Column: f.Column, SourceID: f.SourceID, Original: f.Original})
}

func auditLocation(f audit.HotFunction) string {
	return functionLocation(metrics.FunctionFrame{Name: f.Name, URL: f.URL, Line: f.Line, Column: f.Column, SourceID: f.SourceID, Original: f.Original})
}

func priorityFinding(p audit.Priority) insights.Finding {
	return insights.Finding{NumericEvidence: p.NumericEvidence, MetricKeys: p.MetricKeys, Verify: p.Verify, EvidenceBasis: p.EvidenceBasis, Replication: p.Replication, Confidence: p.Confidence, ConfidenceReasons: p.ConfidenceReasons, SuggestedWindows: p.SuggestedWindows, Functions: p.Functions, Triggers: p.Triggers, Nodes: p.Nodes, Stacks: p.Stacks, StackIDs: p.StackIDs, StacksTotal: p.StacksTotal, Command: p.Command, RelatedFindings: p.RelatedFindings, NativeReads: p.NativeReads, NativeReadsTotal: p.NativeReadsTotal, Depth: p.Depth}
}

func writeFindingDetails(b *strings.Builder, f insights.Finding) {
	for _, facet := range insights.FacetLabels(f.NativeReads, f.Depth, f.RelatedFindings) {
		fmt.Fprintf(b, "   - %s\n", oneLine(facet))
	}
	if f.Command != "" {
		fmt.Fprintf(b, "   - reproduce: %s\n", f.Command)
	}
	for _, stack := range limitSlice(f.Stacks, 2) {
		rootStack(b, stack)
	}
	if len(f.MetricKeys) > 0 {
		fmt.Fprintf(b, "   - metric keys: %s\n", clip(oneLine(strings.Join(f.MetricKeys, ", ")), 400))
	}
	if f.Confidence != "" {
		fmt.Fprintf(b, "   - confidence=%s; evidenceBasis=%s; replication: %d of %d runs (%d parsed); reasons=%s\n", f.Confidence, f.EvidenceBasis, f.Replication.RunsWithFinding, f.Replication.Runs, f.Replication.ParsedRuns, clip(oneLine(strings.Join(f.ConfidenceReasons, "; ")), 400))
	}
	for _, e := range limitSlice(f.NumericEvidence, 6) {
		fmt.Fprintf(b, "   - numeric evidence: %s=%s %s [%s]; %s; reproduce: %s\n", e.MetricKey, num(e.Value), e.Unit, clip(oneLine(e.Basis), 60), clip(oneLine(e.Description), maxEvidenceChars), e.Command)
	}
	for _, v := range limitSlice(f.Verify, 6) {
		fmt.Fprintf(b, "   - verify: %s %s; target=%s %s; %s; reproduce: %s\n", v.MetricKey, clip(oneLine(v.Direction), 40), num(v.Target), v.Unit, clip(oneLine(v.Description), maxEvidenceChars), v.Command)
	}
	for _, window := range limitSlice(f.SuggestedWindows, 4) {
		fmt.Fprintf(b, "   - window: %s–%ss; %s; flags: %s\n", num(window.FromSec), num(window.ToSec), clip(oneLine(window.Reason), 200), strings.Join(window.Flags, " "))
	}
	for _, name := range limitSlice(f.Functions, 3) {
		fmt.Fprintf(b, "   - function: %s\n", clip(oneLine(name), 250))
	}
	for _, trigger := range limitSlice(f.Triggers, 3) {
		fmt.Fprintf(b, "   - trigger: %s\n", clip(oneLine(trigger), 150))
	}
	for _, node := range limitSlice(f.Nodes, 3) {
		fmt.Fprintf(b, "   - recorded node: %s; selector=%s; display=%s\n", clip(oneLine(node.EventType), 80), node.CSSPath, clip(oneLine(node.DisplayName), 250))
	}
	if len(f.NumericEvidence) > 6 || len(f.Verify) > 6 || len(f.SuggestedWindows) > 4 || len(f.Functions) > 3 || len(f.Triggers) > 3 || len(f.Nodes) > 3 || len(f.Stacks) > 2 {
		b.WriteString("   - Additional finding detail omitted to bound prompt size.\n")
	}
}

func writeMetricStack(b *strings.Builder, frames []metrics.FunctionFrame) {
	if len(frames) == 0 {
		return
	}
	leaf := frames[len(frames)-1]
	fmt.Fprintf(b, "   Common stack reference: %s (%d frames; leaf %s @ %s)\n", insights.MetricStackID(frames), len(frames), funcName(leaf.Name), functionLocation(leaf))
}

func writeProfileFunction(b *strings.Builder, r *insights.Report, f metrics.FuncStat) {
	fmt.Fprintf(b, "   samples=%d (self)\n", f.SelfSamples)
	if f.ID != "" {
		fmt.Fprintf(b, "   id=%s\n", f.ID)
	}
	if len(f.CommonStack) > 0 {
		writeMetricStack(b, f.CommonStack)
		fmt.Fprintf(b, "   full stack reproduce: %s\n", insights.FunctionStackCommand(r, f))
	}
}

func compareStackSections(res *compare.Result, base, cand *insights.Report, c Context) []section {
	reports := make(map[string]*insights.Report)
	var sources []string
	for _, group := range [][]compare.FuncDiff{res.FuncRegressed, res.FuncImproved} {
		for _, f := range limitSlice(group, c.TopN) {
			if len(f.CommonStack) == 0 {
				continue
			}
			source := f.CommonStackSource
			if (base != nil && base.Source == source) || (cand != nil && cand.Source == source) {
				continue
			}
			report := reports[source]
			if report == nil {
				report = &insights.Report{Source: source, Top: c.TopN}
				reports[source] = report
				sources = append(sources, source)
			}
			report.Hotspots = append(report.Hotspots, metrics.FuncStat{
				ID: f.ID, Name: f.Name, URL: f.URL, Line: f.Line, Column: f.Column,
				SourceID: f.SourceID, Original: f.Original, CommonStack: f.CommonStack,
				CommonStackID: f.CommonStackID,
			})
		}
	}
	sections := make([]section, 0, len(sources))
	for _, source := range sources {
		sections = append(sections, stackAppendixSection("Comparison "+oneLine(source), reports[source], (vHotspots+1)*10))
	}
	return sections
}
