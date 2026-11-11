package insights

import (
	"math"
	"sort"
	"strings"

	"github.com/omniviewdev/wkperf/internal/metrics"
	"github.com/omniviewdev/wkperf/internal/rootcause"
)

func rootSuggestion(id string) string {
	switch id {
	case "forced-style-or-layout-read":
		return "Inspect the nearest captured non-framework frame around the named native API. Batch reads before DOM writes, cache repeated animation/style queries, and confirm the separate style or layout timeline evidence. API semantics and temporal overlap do not prove that a flush occurred."
	case "react-sync-in-trigger":
		return "Inspect the measured trigger and its exact synchronous React stack. Remove unnecessary flushSync/synchronous root work, coalesce state updates, and defer nonurgent updates. Compare disjoint sampled render and commit costs; temporal overlap does not prove the trigger caused the update."
	case "react-sync":
		return "Inspect the sampled synchronous React path without assigning it to an unobserved user trigger. Capture scheduling context before deciding which event or application update to change."
	case "deep-stack":
		return "Inspect the quantitative depth facets and exact outlier stack. Ordinary React fiber traversal is not application recursion; check a reported non-framework repeated cycle before changing traversal or re-entry."
	case "periodic-root":
		return "Inspect the recurring sampled root and its measured fires. Preserve required interaction and animation work; remove redundant polling or repeated reads only after checking whether the loop runs during input, rAF callbacks, or idle background time."
	}
	return "Inspect the exact sampled stack and its surrounding measured trigger, then repeat the same scene to confirm that the suspected cost falls."
}

func bridgeRootFinding(c *ruleCtx, raw rootcause.Finding) Finding {
	impact := 0.0
	for _, stat := range raw.Stats {
		if stat.Unit == "ms" && (strings.HasSuffix(stat.Key, ".self_ms") || strings.HasSuffix(stat.Key, ".total_ms")) {
			impact = stat.Value
			break
		}
	}
	if impact == 0 {
		for _, stack := range raw.Evidence {
			impact += stack.SelfMs
		}
	}
	f := c.finding(raw.ID, raw.Title, "script", impact, []string{raw.Explanation}, rootSuggestion(raw.ID))
	if raw.ID == "forced-style-or-layout-read" {
		f.Category = "layout"
	}
	share := 0.0
	if sampledMs := c.rep.SampleCoverage.SampledMs; sampledMs > 0 {
		share = impact / sampledMs * 100
	}
	if raw.ID == "react-sync" || impact < 20 || c.pct(impact) < 0.1 {
		f.Severity = SevInfo
	} else {
		f.Severity = sampledSeverity(c, impact, share)
	}
	f.SampleIDs = raw.SampleIDs
	f.RelatedFindings = append([]string{}, raw.RelatedFindings...)
	f.NativeReads = raw.NativeReads
	f.NativeReadsTotal = max(raw.NativeReadsTotal, len(raw.NativeReads))
	f.Depth = raw.Depth
	items := []Finding{f}
	enrichFindings(c, items)
	f = items[0]
	f.Evidence = []string{raw.Explanation}
	f.Suggestion = rootSuggestion(raw.ID)
	f.Nodes = []NodeClue{}
	f.Command = raw.Command
	f.Stacks = raw.Evidence
	f.StackIDs = make([]string, 0, len(raw.Evidence))
	for _, stack := range raw.Evidence {
		f.StackIDs = append(f.StackIDs, stack.StackID)
	}
	f.StacksTotal = len(raw.Evidence)
	f.Functions = []string{}
	f.Triggers = []string{}
	for i := range f.NumericEvidence {
		if strings.HasSuffix(f.NumericEvidence[i].MetricKey, ".impact_ms") {
			f.NumericEvidence[i].Basis = "estimated"
		}
	}
	if c.rep.SampleCoverage.SampledMs > 0 {
		key := "findings." + raw.ID + ".sampled_pct"
		f.NumericEvidence = append(f.NumericEvidence, Evidence{MetricKey: key, Value: share, Unit: metrics.UnitPct, Description: "Detector sample-weight share of all retained sampled self time; overlapping facets are not additive", Basis: "estimated", Command: metricCommand(c, key)})
		f.MetricKeys = appendUnique(f.MetricKeys, key)
	}
	var verification *rootcause.Stat
	for i := range raw.Stats {
		stat := &raw.Stats[i]
		f.NumericEvidence = append(f.NumericEvidence, Evidence{MetricKey: stat.Key, Value: stat.Value, Unit: metrics.Unit(stat.Unit), Description: stat.Label, Basis: stat.Basis, Command: stat.Command})
		f.MetricKeys = appendUnique(f.MetricKeys, stat.Key)
		if stat.Unit == "ms" && verification == nil && (strings.HasSuffix(stat.Key, ".self_ms") || strings.HasSuffix(stat.Key, ".total_ms")) {
			verification = stat
		}
	}
	for _, api := range raw.NativeReads {
		for _, stat := range api.Stats {
			f.NumericEvidence = append(f.NumericEvidence, Evidence{MetricKey: stat.Key, Value: stat.Value, Unit: metrics.Unit(stat.Unit), Description: stat.Label, Basis: stat.Basis, Command: stat.Command})
			f.MetricKeys = appendUnique(f.MetricKeys, stat.Key)
		}
	}
	if verification != nil {
		f.Verify = []Verification{{MetricKey: verification.Key, Direction: "decrease", Target: verification.Value * 0.8, Unit: metrics.Unit(verification.Unit), Description: "Rough target: reduce this specific sampled cost by about 20% in repeated equivalent captures; do not sum inclusive costs.", Command: verification.Command}}
	}
	for _, stack := range raw.Evidence {
		if len(stack.Frames) == 0 {
			continue
		}
		f.Functions = appendUnique(f.Functions, frameLabel(stack.Frames[0]))
		f.Functions = appendUnique(f.Functions, frameLabel(stack.Frames[len(stack.Frames)-1]))
		if len(f.Functions) >= 4 {
			break
		}
	}
	windows := raw.Windows
	if len(windows) == 0 {
		for _, stack := range raw.Evidence {
			if stack.FirstSec == nil || stack.LastSec == nil {
				continue
			}
			from, to := *stack.FirstSec, *stack.LastSec
			if to <= from {
				to = from + 0.001
			} else {
				to += 0.001
			}
			windows = append(windows, rootcause.WindowRef{FromSec: from, ToSec: to, Command: stack.Command})
			if len(windows) >= 3 {
				break
			}
		}
	}
	f.SuggestedWindows = []SuggestedWindow{}
	for _, window := range windows {
		from := math.Max(c.rep.WindowStartSec, window.FromSec)
		to := math.Min(c.rep.WindowEndSec, window.ToSec)
		if to <= from {
			continue
		}
		context := findingContext{span{c.rec.StartTime + from - c.opt.WindowStartSec, c.rec.StartTime + to - c.opt.WindowStartSec}, "Inspect exact sampled detector evidence and overlapping measured triggers; temporal overlap is not proof of cause."}
		f.SuggestedWindows = append(f.SuggestedWindows, suggestedWindow(c, context))
		for _, trigger := range contextTriggers(c, context) {
			f.Triggers = appendUnique(f.Triggers, trigger.Kind+" "+trigger.Name)
		}
		if len(f.SuggestedWindows) >= 3 {
			break
		}
	}
	if len(f.SuggestedWindows) == 0 && c.rec.EndTime > c.rec.StartTime {
		f.SuggestedWindows = append(f.SuggestedWindows, suggestedWindow(c, contextForFinding(c, f)))
	}
	f.ConfidenceReasons = append(f.ConfidenceReasons, "Sampler patterns support a hypothesis, not proof of the suspected timeline cause.")
	sort.Strings(f.Functions)
	sort.Strings(f.Triggers)
	return f
}

func rootFindings(c *ruleCtx) []Finding {
	out := []Finding{}
	if c.rep.RootCause == nil {
		return out
	}
	for _, raw := range c.rep.RootCause.Findings {
		out = append(out, bridgeRootFinding(c, raw))
	}
	raw := rootcause.Finding{ID: "periodic-root", Title: "Repeated periodic sampled work", Explanation: "Sampled roots recur at regular cadences. Fires link to instrumented events or distinct timestamp bursts; recurrence does not establish background work or a busy rendering frame."}
	var total float64
	rafFires, inputFires, fireCount := 0, 0, 0
	allBackground := true
	for _, periodic := range c.rep.RootCause.PeriodicRoots {
		if periodic.MeanCostMs < 2 || periodic.TotalMs < 20 || c.pct(periodic.TotalMs) < 0.1 {
			continue
		}
		total += periodic.TotalMs
		raw.Stats = append(raw.Stats, periodic.Stats...)
		raw.Evidence = append(raw.Evidence, periodic.Evidence...)
		raw.SampleIDs = append(raw.SampleIDs, periodic.SampleIDs...)
		rafFires += periodic.RAFFires
		inputFires += periodic.InputFires
		fireCount += periodic.FireCount
		allBackground = allBackground && periodic.Kind == "background"
		fires := append([]rootcause.Fire{}, periodic.Fires...)
		sort.Slice(fires, func(i, j int) bool {
			if fires[i].SelfMs != fires[j].SelfMs {
				return fires[i].SelfMs > fires[j].SelfMs
			}
			return fires[i].FromSec < fires[j].FromSec
		})
		for _, fire := range fires[:min(3, len(fires))] {
			raw.Windows = append(raw.Windows, rootcause.WindowRef{FromSec: fire.FromSec, ToSec: fire.ToSec, Command: fire.Command})
		}
	}
	if total > 0 {
		if rafFires*2 > fireCount {
			raw.Title = "Repeated sampled rAF work"
		} else if inputFires*2 > fireCount {
			raw.Title = "Repeated sampled work during input"
		} else if allBackground {
			raw.Title = "Repeated periodic sampled background work"
		}
		command := NumericCommand(c.rep, "periodic.total_ms")
		raw.Stats = append([]rootcause.Stat{{Key: "periodic.total_ms", Label: "Sampled cost across qualifying periodic roots", Value: total, Unit: "ms", Basis: "statistical sample-weight estimate", Command: command}}, raw.Stats...)
		raw.Command = command
		out = append(out, bridgeRootFinding(c, raw))
	}
	return out
}

func sortedSampleSet(ids []string) []string {
	sort.Strings(ids)
	n := 0
	for _, id := range ids {
		if n == 0 || ids[n-1] != id {
			ids[n] = id
			n++
		}
	}
	return ids[:n]
}

func overlappingSampleSets(a, b []string) bool {
	if len(a) == 0 || len(b) == 0 {
		return false
	}
	i, j, intersection := 0, 0, 0
	for i < len(a) && j < len(b) {
		switch {
		case a[i] == b[j]:
			intersection++
			i++
			j++
		case a[i] < b[j]:
			i++
		default:
			j++
		}
	}
	return intersection*5 >= (len(a)+len(b)-intersection)*4
}

func sampledLeadPriority(id string) int {
	switch id {
	case "forced-style-or-layout-read":
		return 0
	case "react-sync-in-trigger":
		return 1
	case "deep-stack":
		return 2
	case "hot-function":
		return 3
	case "periodic-root":
		return 4
	case "hot-module":
		return 5
	}
	return 6
}

func mergeSampleFindings(findings []Finding) []Finding {
	emitted := make(map[string]bool, len(findings))
	for _, finding := range findings {
		emitted[finding.ID] = true
	}
	for i := range findings {
		findings[i].SampleIDs = sortedSampleSet(findings[i].SampleIDs)
		related := findings[i].RelatedFindings[:0]
		for _, id := range findings[i].RelatedFindings {
			if emitted[id] && id != findings[i].ID {
				related = appendUnique(related, id)
			}
		}
		findings[i].RelatedFindings = related
	}
	sort.SliceStable(findings, func(i, j int) bool {
		a, b := findings[i], findings[j]
		if severityRank(a.Severity) != severityRank(b.Severity) {
			return severityRank(a.Severity) < severityRank(b.Severity)
		}
		if sampledLeadPriority(a.ID) != sampledLeadPriority(b.ID) {
			return sampledLeadPriority(a.ID) < sampledLeadPriority(b.ID)
		}
		if a.ImpactMs != b.ImpactMs {
			return a.ImpactMs > b.ImpactMs
		}
		return a.ID < b.ID
	})
	out := findings[:0]
	for _, candidate := range findings {
		merged := false
		for i := range out {
			if overlappingSampleSets(out[i].SampleIDs, candidate.SampleIDs) {
				mergeFindingFacet(&out[i], candidate)
				merged = true
				break
			}
		}
		if !merged {
			out = append(out, candidate)
		}
	}
	return out
}

func containsString(values []string, value string) bool {
	for _, item := range values {
		if item == value {
			return true
		}
	}
	return false
}

func mergeFindingFacet(lead *Finding, facet Finding) {
	lead.RelatedFindings = appendUnique(lead.RelatedFindings, facet.ID)
	for _, id := range facet.RelatedFindings {
		if id != lead.ID {
			lead.RelatedFindings = appendUnique(lead.RelatedFindings, id)
		}
	}
	lead.Evidence = appendUnique(lead.Evidence, "Related facet "+facet.ID+": "+facet.Title)
	for _, evidence := range facet.Evidence {
		lead.Evidence = appendUnique(lead.Evidence, evidence)
	}
	for _, evidence := range facet.NumericEvidence {
		if !containsString(lead.MetricKeys, evidence.MetricKey) {
			lead.NumericEvidence = append(lead.NumericEvidence, evidence)
			lead.MetricKeys = append(lead.MetricKeys, evidence.MetricKey)
		}
	}
	for _, function := range facet.Functions {
		lead.Functions = appendUnique(lead.Functions, function)
	}
	for _, trigger := range facet.Triggers {
		lead.Triggers = appendUnique(lead.Triggers, trigger)
	}
	for _, reason := range facet.ConfidenceReasons {
		lead.ConfidenceReasons = appendUnique(lead.ConfidenceReasons, reason)
	}
	for _, stack := range facet.Stacks {
		if !containsString(lead.StackIDs, stack.StackID) {
			lead.Stacks = append(lead.Stacks, stack)
			lead.StackIDs = append(lead.StackIDs, stack.StackID)
		}
	}
	lead.StacksTotal = len(lead.Stacks)
	for _, window := range facet.SuggestedWindows {
		if len(lead.SuggestedWindows) >= 3 {
			break
		}
		exists := false
		for _, retained := range lead.SuggestedWindows {
			exists = exists || (retained.FromSec == window.FromSec && retained.ToSec == window.ToSec)
		}
		if !exists {
			lead.SuggestedWindows = append(lead.SuggestedWindows, window)
		}
	}
	if len(lead.NativeReads) == 0 {
		lead.NativeReads = facet.NativeReads
		lead.NativeReadsTotal = facet.NativeReadsTotal
	}
	if lead.Depth == nil {
		lead.Depth = facet.Depth
	}
	sort.Strings(lead.RelatedFindings)
	sort.Strings(lead.Functions)
	sort.Strings(lead.Triggers)
}

func syncRootFindingSeverities(report *Report) {
	if report.RootCause == nil {
		return
	}
	for i := range report.RootCause.Findings {
		raw := &report.RootCause.Findings[i]
		raw.Severity = ""
		raw.RelatedFindings = nil
		for _, finding := range report.Findings {
			if finding.ID == raw.ID {
				raw.Severity = string(finding.Severity)
				raw.RelatedFindings = finding.RelatedFindings
				break
			}
			if containsString(finding.RelatedFindings, raw.ID) {
				raw.RelatedFindings = appendUnique(raw.RelatedFindings, finding.ID)
			}
		}
	}
}

func severityRank(severity Severity) int {
	switch severity {
	case SevHigh:
		return 0
	case SevMedium:
		return 1
	case SevLow:
		return 2
	}
	return 3
}

func sortFindings(findings []Finding) {
	sort.Slice(findings, func(i, j int) bool {
		a, b := findings[i], findings[j]
		if severityRank(a.Severity) != severityRank(b.Severity) {
			return severityRank(a.Severity) < severityRank(b.Severity)
		}
		if a.ImpactMs != b.ImpactMs {
			return a.ImpactMs > b.ImpactMs
		}
		return a.ID < b.ID
	})
}
