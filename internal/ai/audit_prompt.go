package ai

import (
	"fmt"
	"math"
	"sort"
	"strings"
	"unicode/utf8"

	"github.com/omniviewdev/wkperf/internal/audit"
	"github.com/omniviewdev/wkperf/internal/insights"
	"github.com/omniviewdev/wkperf/internal/metrics"
	"github.com/omniviewdev/wkperf/internal/scorecard"
)

const auditSystemPrompt = `You are a senior WebKit/WKWebView and React performance engineer. You are given an AUDIT of ONE build of an application across several scenes (scenarios), measured with wkperf from the WebKit Web Inspector timeline. This is not a before/after comparison: judge how far the build is from a performant UI and what to fix first.

Rules:
- Base every conclusion ONLY on the data provided. Do not invent numbers, functions, files, scenes or metrics that are not in the data.
- Cite the evidence for each claim: scorecard standard IDs (e.g. frame-p95, tbt), metric keys (e.g. frames.p95_ms), function names with their location, finding IDs, scene names, and the numbers.
- Clearly distinguish measured facts from hypotheses. Label hypotheses as "Hypothesis" and say what data would confirm them.
- Prioritize by cross-scene breadth x impact: a problem that fires in many scenes with a large share of time outranks a larger problem in a single scene. Then weigh effort (high impact / low effort first).
- Explain the distance to the scorecard targets: for each standard not rated good, say how far the value is from the Good threshold and what must change to reach it.
- The scorecard standards are adapted from Core Web Vitals, Lighthouse and RAIL, and from frame-budget targets. They are approximations for a long-lived webview app, not page-load measurements. Say so where it affects a conclusion, and do not over-claim.
- Give concrete, code-level changes rather than generic advice. Consider: React memoization (React.memo, useMemo, useCallback, stable props), list virtualization, avoiding layout thrash (batching DOM reads before writes, no forced synchronous layout), throttling/debouncing handlers, requestAnimationFrame batching, reducing allocations and garbage-collection pressure, moving work to Web Workers, CSS containment (contain, content-visibility), and lazy loading / code splitting.
- If results are marked noisy, low-confidence or single-run, say so explicitly. A scene with one run has no variance estimate.
- Verification plan: for each recommendation name the scorecard standard IDs and metric keys that must move, in which direction and roughly by how much, and which scenes to re-capture (name them) before re-running the audit.
- If the data was truncated, say which conclusions may be affected.

Output Markdown with exactly these top-level sections, in this order:
## Summary
## Top Issues (ranked)
## Recommendations
## Verification Plan
## Caveats

Be concise and specific. No preamble before the first section.`

// Audit section values; lowest is dropped first when over budget. Overall
// and priorities are the last to go.
const (
	vAuditOverall    = 100
	vAuditPriorities = 95
	vAuditScenes     = 80
	vAuditBreakdown  = 70
	vAuditHotspots   = 60
	vAuditWorst      = 50
	vAuditModules    = 40
)

// BuildAuditPrompt builds a prompt asking for a prioritized, non-comparison
// assessment of one build across many scenes. a may be nil.
func BuildAuditPrompt(a *audit.Report, c Context) Prompt {
	budget := MaxPromptTokens*charsPerToken - utf8.RuneCountInString(auditSystemPrompt) - 1
	return Prompt{System: auditSystemPrompt, User: buildAuditUser(a, c, budget)}
}

func buildAuditUser(a *audit.Report, c Context, budget int) string {
	c = normalizeContext(c)
	secs := []section{headerSection(c, "Task: audit this build across the scenes below. Explain how far it is from a performant UI, rank what to fix first by cross-scene breadth x impact, and say how to verify each fix.\n")}
	if a == nil || (len(a.Scenes) == 0 && a.Overall == nil && len(a.Priorities) == 0) {
		secs = append(secs, section{title: "no-data", value: math.MaxInt, body: "No audit data was provided.\n"})
		return assemble(secs, budget)
	}
	a = audit.SelectEvidence(a)
	secs = append(secs, auditSections(a, c)...)
	return assemble(secs, budget)
}

func auditSections(a *audit.Report, c Context) []section {
	var secs []section

	// Overall + gap.
	{
		var b strings.Builder
		b.WriteString("## Overall scorecard\n")
		if a.Overall == nil {
			b.WriteString("Not available.\n")
		} else {
			fmt.Fprintf(&b, "Grade %s, score %s/100 (median across %d scene(s)).\n", a.Overall.Grade, num(a.Overall.Score), len(a.Scenes))
			if len(a.Overall.Gap) == 0 {
				b.WriteString("All available standards are rated good.\n")
			} else {
				b.WriteString("Gap to targets (worst first; factor = times over target):\n")
				for _, g := range limitSlice(a.Overall.Gap, c.TopN) {
					fmt.Fprintf(&b, "- %s (%s): value %s vs target %s, %s, %sx\n", g.ID, clip(oneLine(g.Name), 60), num(g.Value), num(g.Target), g.Rating, num(g.Factor))
				}
			}
			b.WriteString("Standards are adapted from Core Web Vitals/Lighthouse/RAIL; approximations for a long-lived webview app.\n")
			var notes []string
			for _, r := range a.Overall.Results {
				if r.Rating == scorecard.Unavailable {
					notes = append(notes, r.ID)
				}
			}
			if len(notes) > 0 {
				fmt.Fprintf(&b, "Unavailable (no data): %s\n", strings.Join(notes, ", "))
			}
		}
		secs = append(secs, section{title: "overall", value: vAuditOverall, body: b.String()})
	}

	// Priorities.
	if len(a.Priorities) > 0 {
		var b strings.Builder
		b.WriteString("## Priorities (fix first; ranked by breadth x impact)\n")
		for i, p := range limitSlice(a.Priorities, c.MaxFindings) {
			fmt.Fprintf(&b, "%d. [%s] %s (%s, severity %s): %d scene(s) [%s], total impact %sms, avg impact %s%% over all scenes, rank %s\n",
				i+1, p.ID, clip(oneLine(p.Title), 120), p.Category, p.Severity, len(p.Scenes),
				clip(strings.Join(p.Scenes, ", "), 200), num(p.TotalImpactMs), num(p.AvgImpactPct), num(p.Rank))
			for _, e := range limitSlice(p.Evidence, 3) {
				fmt.Fprintf(&b, "   - evidence: %s\n", clip(oneLine(e), maxEvidenceChars))
			}
			if p.EvidenceScene != "" {
				fmt.Fprintf(&b, "   - evidence scene: %s\n", clip(oneLine(p.EvidenceScene), 100))
			}
			writeFindingDetails(&b, priorityFinding(p))
			if p.Suggestion != "" {
				fmt.Fprintf(&b, "   - suggestion: %s\n", clip(oneLine(p.Suggestion), maxSuggestChars))
			}
		}
		secs = append(secs, section{title: "priorities", value: vAuditPriorities, body: b.String()})
	}

	// Scene leaderboard (worst first, as supplied).
	if len(a.Scenes) > 0 {
		var b strings.Builder
		b.WriteString("## Scene leaderboard (worst first)\n")
		for _, s := range limitSlice(a.Scenes, capTopN) {
			grade, score := "n/a", "n/a"
			var gaps []string
			if s.Card != nil {
				grade, score = s.Card.Grade, num(s.Card.Score)
				for _, g := range limitSlice(s.Card.Gap, 3) {
					gaps = append(gaps, fmt.Sprintf("%s=%s(%s)", g.ID, num(g.Value), g.Rating))
				}
			}
			fmt.Fprintf(&b, "- %s: grade %s, score %s, %d run(s), %ss", clip(oneLine(s.Name), 80), grade, score, s.Runs, num(s.DurationSec))
			if s.TopFinding != "" {
				fmt.Fprintf(&b, ", top finding: %s", clip(oneLine(s.TopFinding), 120))
			}
			if len(gaps) > 0 {
				fmt.Fprintf(&b, ", worst standards: %s", strings.Join(gaps, ", "))
			}
			if s.Runs == 1 {
				b.WriteString(" [single run]")
			}
			b.WriteByte('\n')
		}
		secs = append(secs, section{title: "scene leaderboard", value: vAuditScenes, body: b.String()})
	}

	// Breakdown.
	if len(a.Breakdown) > 0 {
		var b strings.Builder
		b.WriteString("## Time breakdown (duration-weighted across scenes)\n")
		for _, s := range a.Breakdown {
			fmt.Fprintf(&b, "- %s: %sms (%s%%)\n", s.Name, num(s.Ms), num(s.Pct))
		}
		secs = append(secs, section{title: "breakdown", value: vAuditBreakdown, body: b.String()})
	}

	// Hot functions.
	if len(a.Hotspots) > 0 {
		var b strings.Builder
		b.WriteString("## Hot functions (merged across scenes)\n")
		for i, h := range limitSlice(a.Hotspots, c.TopN) {
			fmt.Fprintf(&b, "%d. %s @ %s: self %sms (%s%%), %d scene(s) [%s]\n", i+1, funcName(h.Name),
				auditLocation(h), num(h.SelfMs), num(h.Pct), h.SceneCount, clip(strings.Join(h.Scenes, ", "), 150))
			if h.ID != "" {
				fmt.Fprintf(&b, "   id=%s\n", h.ID)
			}
			writeMetricStack(&b, h.CommonStack)
			if len(h.CommonStack) > 0 {
				fmt.Fprintf(&b, "   Common stack scene: %s\n", clip(oneLine(h.CommonStackScene), 100))
				if h.EvidenceSceneIndex >= 0 && h.EvidenceSceneIndex < len(a.Scenes) {
					scene := a.Scenes[h.EvidenceSceneIndex]
					if scene.Rep != nil {
						fmt.Fprintf(&b, "   full stack reproduce: %s\n", insights.FunctionStackCommand(scene.Rep, metrics.FuncStat{ID: h.ID, Name: h.Name, URL: h.URL, Line: h.Line, Column: h.Column, SourceID: h.SourceID, CommonStackID: h.CommonStackID}))
					}
				}
			}
		}
		secs = append(secs, section{title: "hot functions", value: vAuditHotspots, body: b.String()})
	}

	// Worst scene per standard, sorted by ID for determinism.
	if len(a.WorstByStd) > 0 {
		ids := make([]string, 0, len(a.WorstByStd))
		for id := range a.WorstByStd {
			ids = append(ids, id)
		}
		sort.Strings(ids)
		var b strings.Builder
		b.WriteString("## Worst scene per standard\n")
		for _, id := range ids {
			fmt.Fprintf(&b, "- %s: %s\n", id, clip(oneLine(a.WorstByStd[id]), 80))
		}
		secs = append(secs, section{title: "worst scene per standard", value: vAuditWorst, body: b.String()})
	}

	// Modules.
	if len(a.Modules) > 0 {
		var b strings.Builder
		b.WriteString("## Modules by self time (merged across scenes)\n")
		for _, m := range limitSlice(a.Modules, c.TopN) {
			fmt.Fprintf(&b, "- %s: %sms (%s%%)\n", clip(oneLine(m.Name), maxLocChars), num(m.SelfMs), num(m.Pct))
		}
		secs = append(secs, section{title: "modules", value: vAuditModules, body: b.String()})
	}
	for i, scene := range limitSlice(a.Scenes, c.TopN) {
		if scene.Rep != nil {
			secs = append(secs, rootCauseSections("scene "+clip(oneLine(scene.Name), 80), scene.Rep.RootCause, c, vAuditPriorities-i)...)
			secs = append(secs, stackAppendixSection("scene "+clip(oneLine(scene.Name), 80), scene.Rep, vAuditPriorities-i+1))
		}
	}
	return secs
}
