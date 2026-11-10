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
	case "suspected-layout-read":
		return "Inspect the named application caller around native layout-related reads. Cache getAnimations/layout queries, avoid polling while idle, and batch reads before DOM writes. Confirm actual layout with timeline records; a native sampler frame alone does not prove forced layout."
	case "react-sync-in-trigger":
		return "Inspect the measured event or microtask and its exact React stack. Remove unnecessary flushSync/synchronous root work, coalesce state updates, and defer nonurgent updates. Compare distinct render and commit self costs; inclusive ancestor times are not additive."
	case "deep-recursion":
		return "Inspect repeated generated frames in the exact outlier stack. Check recursion termination and repeated synchronous re-entry; replace unbounded traversal with bounded iterative work where appropriate. A deep sample alone does not prove an infinite loop."
	case "deep-commit":
		return "Inspect the exact deep commit stack and its nearest application ancestor. Reduce unnecessary subtree updates, stabilize props, and virtualize large rendered trees. Compare commit depth and self cost in repeated equivalent interactions."
	case "deep-stack":
		return "Inspect the exact unusually deep stack and the repeated application call path. Reduce avoidable synchronous traversal or re-entry only after checking the surrounding trigger; depth alone does not establish a bottleneck."
	case "periodic-root":
		return "Inspect the measured periodic root and its event/timer fires. Stop background polling when the view is idle, replace polling with event-driven updates, or coalesce repeated work. Preserve required animation or refresh behavior and compare sampled cost per fire."
	}
	return "Inspect the exact measured stack and its surrounding trigger, then repeat the same scene to confirm that the suspected cost falls."
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
	if raw.ID == "suspected-layout-read" {
		f.Category = "layout"
	}
	if impact < 20 || c.pct(impact) < 0.1 {
		f.Severity = SevInfo
	} else {
		f.Severity = sampledSeverity(c, impact, 100)
	}
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
	var verification *rootcause.Stat
	for i := range raw.Stats {
		stat := &raw.Stats[i]
		f.NumericEvidence = append(f.NumericEvidence, Evidence{MetricKey: stat.Key, Value: stat.Value, Unit: metrics.Unit(stat.Unit), Description: stat.Label, Basis: stat.Basis, Command: stat.Command})
		f.MetricKeys = appendUnique(f.MetricKeys, stat.Key)
		if stat.Unit == "ms" && verification == nil && (strings.HasSuffix(stat.Key, ".self_ms") || strings.HasSuffix(stat.Key, ".total_ms")) {
			verification = stat
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
	raw := rootcause.Finding{ID: "periodic-root", Title: "Repeated periodic sampled background work", Severity: "low", Explanation: "Sampled roots recur at regular cadences. Fires link to instrumented events or distinct timestamp bursts; this is not evidence that every rendered frame was busy."}
	var total float64
	for _, periodic := range c.rep.RootCause.PeriodicRoots {
		if periodic.MeanCostMs < 2 || periodic.TotalMs < 20 || c.pct(periodic.TotalMs) < 0.1 {
			continue
		}
		total += periodic.TotalMs
		raw.Stats = append(raw.Stats, periodic.Stats...)
		raw.Evidence = append(raw.Evidence, periodic.Evidence...)
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
		command := NumericCommand(c.rep, "periodic.total_ms")
		raw.Stats = append([]rootcause.Stat{{Key: "periodic.total_ms", Label: "Sampled cost across qualifying periodic roots", Value: total, Unit: "ms", Basis: "statistical sample-weight estimate", Command: command}}, raw.Stats...)
		raw.Command = command
		out = append(out, bridgeRootFinding(c, raw))
	}
	return out
}

func sortFindings(findings []Finding) {
	rank := map[Severity]int{SevHigh: 0, SevMedium: 1, SevLow: 2, SevInfo: 3}
	sort.Slice(findings, func(i, j int) bool {
		a, b := findings[i], findings[j]
		if rank[a.Severity] != rank[b.Severity] {
			return rank[a.Severity] < rank[b.Severity]
		}
		if a.ImpactMs != b.ImpactMs {
			return a.ImpactMs > b.ImpactMs
		}
		return a.ID < b.ID
	})
}
