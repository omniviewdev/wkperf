package insights

import (
	"fmt"
	"math"
	"sort"

	"github.com/omniviewdev/wkperf/internal/model"
	"github.com/omniviewdev/wkperf/internal/profile"
)

type findingContext struct {
	span
	reason string
}

// LongTaskRecords returns shared task unions with their outermost record labels.
func LongTaskRecords(rec *model.Recording, thresholdMs float64) []model.Record {
	tasks := longTaskSpans(rec, thresholdMs)
	out := make([]model.Record, len(tasks))
	for i, task := range tasks {
		out[i] = task.Record
	}
	return out
}

func longTaskSpans(rec *model.Recording, thresholdMs float64) []profile.Task {
	if thresholdMs <= 0 {
		thresholdMs = 50
	}
	tasks := profile.Tasks(rec)
	out := tasks[:0]
	for _, task := range tasks {
		if (task.EndTime-task.StartTime)*1000 > thresholdMs {
			out = append(out, task)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		a, b := out[i], out[j]
		aMs, bMs := a.EndTime-a.StartTime, b.EndTime-b.StartTime
		if aMs != bMs {
			return aMs > bMs
		}
		if a.StartTime != b.StartTime {
			return a.StartTime < b.StartTime
		}
		if a.EndTime != b.EndTime {
			return a.EndTime < b.EndTime
		}
		if a.EventType != b.EventType {
			return a.EventType < b.EventType
		}
		return detailString(&a.Record) < detailString(&b.Record)
	})
	return out
}

func matchesFinding(r model.Record, id string) bool {
	if profile.IsGC(r) {
		return id == "gc-pressure"
	}
	switch id {
	case "long-tasks":
		return r.Type == model.TypeScript && !profile.CountOnly(r.EventType)
	case "raf-load":
		return r.Type == model.TypeScript && r.EventType == "animation-frame-fired"
	case "timer-storm":
		return r.Type == model.TypeScript && (r.EventType == "timer-fired" || r.EventType == "timer-installed")
	case "event-handler-heavy":
		return r.Type == model.TypeScript && r.EventType == "event-dispatched"
	case "microtask-heavy":
		return r.Type == model.TypeScript && r.EventType == profile.EventMicrotaskDispatched
	case "observer-callback-heavy":
		return r.Type == model.TypeScript && r.EventType == profile.EventObserverCallback
	case "style-recalc-heavy":
		return r.Type == model.TypeLayout && isStyleEvent(r.EventType)
	case "layout-heavy":
		return r.Type == model.TypeLayout && r.EventType == "layout"
	case "layout-after-script":
		return r.Type == model.TypeLayout && (r.EventType == "layout" || isStyleEvent(r.EventType))
	case "paint-heavy":
		return r.Type == model.TypeLayout && r.EventType == "paint"
	}
	return false
}

func contextForFinding(c *ruleCtx, f Finding) findingContext {
	context := findingContext{span{c.rec.StartTime, c.rec.EndTime}, "Repeat the same interaction and compare with this single-run baseline."}
	if f.Category == "memory" {
		context.reason = "Repeat the complete measured memory interval."
		return context
	}
	if f.ID == "jank" && len(c.slow) > 0 {
		frame := c.slow[0]
		return findingContext{span{c.rec.StartTime + frame.T, c.rec.StartTime + frame.T + frame.Ms/1000}, "Inspect the worst active frame; idle gaps are excluded."}
	}
	if f.ID == "input-stall" && len(c.rep.InputStalls) > 0 {
		frame := c.rep.InputStalls[0]
		return findingContext{span{c.rec.StartTime + frame.T, c.rec.StartTime + frame.T + frame.Ms/1000}, "Inspect the longest input-driven rendering stall; measured main-thread work is at most one frame budget."}
	}
	if f.ID == "long-tasks" && len(c.long) > 0 {
		task := c.long[0]
		return findingContext{span{c.rec.StartTime + task.T, c.rec.StartTime + task.T + task.Ms/1000}, "Inspect the worst overlapping script-task union; nested records count once."}
	}
	candidates := c.rec.Records
	if f.ID == "forced-layout" {
		candidates = profile.ForcedLayoutRecords(c.rec)
	}
	var best model.Record
	bestMs := -1.0
	for _, r := range candidates {
		if f.ID != "forced-layout" && !matchesFinding(r, f.ID) {
			continue
		}
		if f.ID == "layout-after-script" && !normalPostScriptLayout(c.rec, r) {
			continue
		}
		ms := profile.ActiveDuration(c.rec, r.StartTime, r.EndTime) * 1000
		if ms > bestMs || (ms == bestMs && (r.StartTime < best.StartTime || (r.StartTime == best.StartTime && eventLabel(&r) < eventLabel(&best)))) {
			best, bestMs = r, ms
		}
	}
	if bestMs >= 0 {
		context = findingContext{span{math.Max(c.rec.StartTime, best.StartTime), math.Min(c.rec.EndTime, best.EndTime)}, "Inspect the worst measured " + eventLabel(&best) + " interval."}
		if context.e <= context.s {
			context.e = math.Min(c.rec.EndTime, context.s+0.001)
		}
		if f.Category == "layout" || f.Category == "paint" {
			for _, r := range c.rec.Records {
				if r.Type != model.TypeScript || profile.IsGC(r) || triggerKind(r.EventType) == "" {
					continue
				}
				if r.StartTime <= best.StartTime && best.StartTime < r.EndTime {
					context.s = math.Min(context.s, r.StartTime)
					context.e = math.Max(context.e, r.EndTime)
					context.reason = "Inspect the worst measured rendering interval and its overlapping " + eventLabel(&r) + " trigger; overlap is not proof of cause."
				}
			}
			if f.ID == "layout-after-script" {
				for _, frame := range profile.Frames(c.rec, c.rec.StartTime, c.rec.EndTime) {
					if best.StartTime < frame.Start || best.StartTime >= frame.End {
						continue
					}
					for _, r := range c.rec.Records {
						if r.Type == model.TypeScript && triggerKind(r.EventType) != "" && r.StartTime >= frame.Start && r.EndTime <= best.StartTime {
							context.s = math.Min(context.s, r.StartTime)
						}
					}
				}
			}
		}
		context.s = math.Max(context.s, c.rec.StartTime)
		context.e = math.Min(context.e, c.rec.EndTime)
		return context
	}
	if f.ID == "hot-function" || f.ID == "hot-module" {
		bestWeight := -1.0
		for _, sample := range c.rep.FlameTree.Samples {
			if len(sample.Frames) == 0 {
				continue
			}
			leaf := sample.Frames[len(sample.Frames)-1]
			match := f.ID == "hot-module" && len(c.mods) > 0 && moduleGroup(leaf.URL, c.opt.ModuleGrouping) == c.mods[0].Name
			if f.ID == "hot-function" && len(c.rep.Hotspots) > 0 {
				h := c.rep.Hotspots[0]
				match = h.Name == leaf.Name && h.URL == leaf.URL && h.Line == leaf.Line && h.Column == leaf.Column
			}
			at := c.rec.StartTime + sample.Timestamp
			if match && (sample.WeightMs > bestWeight || (sample.WeightMs == bestWeight && at < context.s)) {
				bestWeight = sample.WeightMs
				context = findingContext{span{at, math.Min(c.rec.EndTime, at+0.001)}, "Inspect a timestamp sample of the implicated function/module; its weight is not an occupied time span."}
			}
		}
		if bestWeight < 0 && c.rep.SampleCoverage.UntimedSampleCount > 0 {
			context.reason = "implicated function has only untimed samples; no time window available"
		}
	}
	return context
}

func normalPostScriptLayout(rec *model.Recording, r model.Record) bool {
	for _, script := range rec.Records {
		if script.Type == model.TypeScript && !profile.IsGC(script) && script.StartTime <= r.StartTime && r.StartTime < script.EndTime {
			return false
		}
	}
	for _, frame := range profile.Frames(rec, rec.StartTime, rec.EndTime) {
		if r.StartTime < frame.Start || r.StartTime >= frame.End {
			continue
		}
		for _, script := range rec.Records {
			if script.Type == model.TypeScript && !profile.IsGC(script) && script.StartTime >= frame.Start && script.EndTime <= r.StartTime {
				return true
			}
		}
	}
	return false
}

func contextTriggers(c *ruleCtx, context findingContext) []TriggerStat {
	buckets := map[string]*triggerBucket{}
	for i := range c.rec.Records {
		r := &c.rec.Records[i]
		kind := triggerKind(r.EventType)
		if r.Type != model.TypeScript || kind == "" || profile.IsGC(*r) {
			continue
		}
		s, e := math.Max(context.s, r.StartTime), math.Min(context.e, r.EndTime)
		if e <= s {
			continue
		}
		addTrigger(buckets, kind, triggerName(r), profile.ActiveDuration(c.rec, s, e)*1000, profile.Interval{Start: s, End: e})
	}
	return finishTriggers(c.rec, c.rep.FlameTree, buckets, c.opt.Top)
}

func addNodeClues(c *ruleCtx, f *Finding, context findingContext) {
	if f.Category != "layout" && f.Category != "paint" {
		return
	}
	seen := map[string]bool{}
	f.Nodes = []NodeClue{}
	for _, r := range c.rec.Records {
		if r.Type != model.TypeLayout || overlap(context.s, context.e, r.StartTime, r.EndTime) <= 0 || (r.DOMNodeCSSPath == "" && r.DOMNodeDisplayName == "") {
			continue
		}
		key := r.EventType + "\x00" + r.DOMNodeCSSPath + "\x00" + r.DOMNodeDisplayName
		if seen[key] {
			continue
		}
		seen[key] = true
		f.Nodes = append(f.Nodes, NodeClue{CSSPath: r.DOMNodeCSSPath, DisplayName: r.DOMNodeDisplayName, EventType: r.EventType})
	}
	sort.Slice(f.Nodes, func(i, j int) bool {
		a, b := f.Nodes[i], f.Nodes[j]
		if a.CSSPath != b.CSSPath {
			return a.CSSPath < b.CSSPath
		}
		if a.DisplayName != b.DisplayName {
			return a.DisplayName < b.DisplayName
		}
		return a.EventType < b.EventType
	})
	for _, node := range f.Nodes {
		label := node.CSSPath
		if label == "" {
			label = node.DisplayName
		}
		f.Evidence = append(f.Evidence, fmt.Sprintf("Recorded %s node: %s (%s)", node.EventType, node.CSSPath, node.DisplayName))
		f.Suggestion += " Inspect recorded node " + label + " and changes invalidating that subtree; no stylesheet location is supplied by this record."
	}
}
