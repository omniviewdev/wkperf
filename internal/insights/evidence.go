package insights

import (
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/omniviewdev/wkperf/internal/metrics"
)

func functionLabel(f metrics.FuncStat) string {
	generated := fmt.Sprintf("%s:%d:%d", f.URL, f.Line, f.Column)
	name := f.Name
	if name == "" {
		name = "(anonymous)"
	}
	if f.URL == "" {
		generated = fmt.Sprintf("native[%s]:%d:%d", f.SourceID, f.Line, f.Column)
	}
	if f.Original != nil {
		o := f.Original
		originalName := o.Name
		if originalName == "" {
			originalName = name
		}
		return fmt.Sprintf("%s (%s:%d:%d) [generated %s (%s)]", originalName, o.URL, o.Line, o.Column, name, generated)
	}
	return fmt.Sprintf("%s (%s)", name, generated)
}

func sampledSeverity(c *ruleCtx, ms, share float64) Severity {
	severity := SevLow
	if c.pct(ms) >= 15 && share >= 30 {
		severity = SevHigh
	} else if c.pct(ms) >= 5 && share >= 10 {
		severity = SevMedium
	}
	coverage := c.rep.SampleCoverage
	if !coverage.Available || coverage.Ratio < 0.5 || coverage.SampleCount < 100 {
		if severity == SevHigh {
			severity = SevMedium
		} else if severity == SevMedium {
			severity = SevLow
		}
	}
	return severity
}

func shellQuote(s string) string { return "'" + strings.ReplaceAll(s, "'", "'\\''") + "'" }
func seconds(s float64) string   { return strconv.FormatFloat(s, 'f', -1, 64) }

func metricCommand(c *ruleCtx, key string) string {
	return NumericCommand(c.rep, key)
}

func primaryMetricKeys(id string) []string {
	switch id {
	case "jank":
		return []string{"frames.p95_ms", "frames.jank_ratio"}
	case "forced-layout":
		return []string{"layout.forced-layout.total_ms", "layout.layout.total_ms"}
	case "layout-after-script", "layout-heavy":
		return []string{"layout.layout.total_ms", "layout.recalculate-styles.total_ms"}
	case "style-recalc-heavy":
		return []string{"layout.recalculate-styles.total_ms"}
	case "paint-heavy":
		return []string{"layout.paint.total_ms"}
	case "raf-load":
		return []string{"script.animation-frame-fired.total_ms", "script.animation-frame-fired.max_ms"}
	case "timer-storm":
		return []string{"script.timer-fired.count", "script.timer-installed.count"}
	case "event-handler-heavy":
		return []string{"script.event-dispatched.total_ms", "script.event-dispatched.max_ms"}
	case "microtask-heavy":
		return []string{"script.microtask-fired.total_ms"}
	case "observer-callback-heavy":
		return []string{"script.observer-callback-fired.total_ms"}
	case "gc-pressure":
		return []string{"gc.total_ms", "gc.count"}
	case "memory-growth":
		return []string{"memory.peak_mb", "memory.final_mb"}
	case "cpu-high":
		return []string{"cpu.mean_pct", "cpu.main.mean_pct"}
	case "network":
		return []string{"network.requests", "network.p95_ms", "network.transfer_bytes"}
	case "long-tasks", "low-sample-coverage":
		return []string{"script.total_ms"}
	}
	return nil
}

func verificationTarget(id string, metric metrics.Metric) float64 {
	switch metric.Key {
	case "frames.p95_ms":
		return 16.7
	case "frames.jank_ratio":
		return 0.05
	case "cpu.mean_pct", "cpu.main.mean_pct":
		return 50
	case "script.animation-frame-fired.max_ms":
		return 8
	case "script.event-dispatched.max_ms":
		return 50
	}
	if id == "forced-layout" {
		return 0
	}
	return metric.Value * 0.8
}

func appendUnique(items []string, value string) []string {
	if value == "" || strings.HasPrefix(value, "No samples") {
		return items
	}
	for _, item := range items {
		if item == value {
			return items
		}
	}
	return append(items, value)
}

func implicatedTriggers(id string, triggers []TriggerStat) []TriggerStat {
	var out []TriggerStat
	for _, trigger := range triggers {
		include := false
		switch id {
		case "raf-load":
			include = trigger.Kind == "rAF"
		case "timer-storm":
			include = trigger.Kind == "timer"
		case "event-handler-heavy":
			include = trigger.Kind == "dispatch"
		case "microtask-heavy":
			include = trigger.Kind == "microtask"
		case "long-tasks", "forced-layout", "layout-after-script", "jank", "hot-function", "hot-module", "cpu-high":
			include = true
		}
		if include {
			out = append(out, trigger)
		}
		if len(out) == 3 {
			break
		}
	}
	return out
}

func suggestedWindow(c *ruleCtx, context findingContext) SuggestedWindow {
	from := context.s - c.rec.StartTime + c.opt.WindowStartSec
	to := context.e - c.rec.StartTime + c.opt.WindowStartSec
	flags := []string{"--from", seconds(from), "--to", seconds(to)}
	if c.opt.JankMs != 16.7 {
		flags = append(flags, "--jank-ms", seconds(c.opt.JankMs))
	}
	if c.opt.LongTaskMs != 50 {
		flags = append(flags, "--long-task-ms", seconds(c.opt.LongTaskMs))
	}
	if c.opt.ModuleGrouping != "file" {
		flags = append(flags, "--module-grouping", c.opt.ModuleGrouping)
	}
	for _, path := range c.opt.Sourcemaps {
		flags = append(flags, "--sourcemap", path)
	}
	return SuggestedWindow{FromSec: from, ToSec: to, Reason: context.reason, Flags: flags}
}

func enrichFindings(c *ruleCtx, findings []Finding) {
	for i := range findings {
		f := &findings[i]
		f.NumericEvidence = []Evidence{}
		f.MetricKeys = []string{}
		f.Verify = []Verification{}
		f.SuggestedWindows = []SuggestedWindow{}
		if f.Functions == nil {
			f.Functions = []string{}
		}
		f.Triggers = []string{}
		f.Nodes = []NodeClue{}
		context := contextForFinding(c, *f)
		addNodeClues(c, f, context)
		f.Confidence = "low"
		f.ConfidenceReasons = []string{"Single-run observation; repeat the same scene to estimate variance."}
		if c.rep.DurationSec < 10 {
			f.ConfidenceReasons = append(f.ConfidenceReasons, "Short capture: fewer than 10 active seconds.")
		}
		coverage := c.rep.SampleCoverage
		if coverage.SampleCount < 100 {
			f.ConfidenceReasons = append(f.ConfidenceReasons, "Sparse JavaScript samples: fewer than 100 weighted samples.")
		}
		if coverage.Available && coverage.Ratio < 0.5 {
			f.ConfidenceReasons = append(f.ConfidenceReasons, "Sample weights cover less than half the measured script time.")
		}
		for _, key := range primaryMetricKeys(f.ID) {
			metric, ok := c.sum.Lookup(key)
			if !ok {
				continue
			}
			f.NumericEvidence = append(f.NumericEvidence, Evidence{MetricKey: key, Value: metric.Value, Unit: metric.Unit, Description: metric.Label, Basis: "measured", Command: metricCommand(c, key)})
			f.MetricKeys = appendUnique(f.MetricKeys, key)
			if len(f.Verify) == 0 {
				f.Verify = append(f.Verify, Verification{MetricKey: key, Direction: "decrease", Target: verificationTarget(f.ID, metric), Unit: metric.Unit, Description: "Rough target; compare repeated captures of the same scene, not a guaranteed outcome.", Command: metricCommand(c, key)})
			}
		}
		impactKey := "findings." + f.ID + ".impact_ms"
		basis := "measured"
		if f.ID == "hot-function" || f.ID == "hot-module" || f.ID == "cpu-high" {
			basis = "estimated"
		}
		f.NumericEvidence = append(f.NumericEvidence, Evidence{MetricKey: impactKey, Value: f.ImpactMs, Unit: metrics.UnitMs, Description: "Attributed impact; overlapping findings are not additive.", Basis: basis, Command: metricCommand(c, impactKey)})
		f.MetricKeys = appendUnique(f.MetricKeys, impactKey)
		if len(f.Verify) == 0 {
			f.Verify = append(f.Verify, Verification{MetricKey: impactKey, Direction: "decrease", Target: f.ImpactMs * 0.8, Unit: metrics.UnitMs, Description: "Aim for roughly 20% less measured or sampled cost in repeated equivalent captures.", Command: metricCommand(c, impactKey)})
		}
		if f.ID == "hot-function" || f.ID == "hot-module" || f.ID == "low-sample-coverage" {
			f.NumericEvidence = append(f.NumericEvidence,
				Evidence{MetricKey: "samples.count", Value: float64(coverage.SampleCount), Unit: metrics.UnitCount, Description: "Weighted samples supporting function rankings", Basis: "measured", Command: metricCommand(c, "samples.count")},
				Evidence{MetricKey: "samples.coverage_ratio", Value: coverage.Ratio, Unit: metrics.UnitRatio, Description: "Sampled weight / exclusive measured script time; unavailable without script intervals", Basis: "estimated", Command: metricCommand(c, "samples.coverage_ratio")})
			f.MetricKeys = appendUnique(f.MetricKeys, "samples.count")
			f.MetricKeys = appendUnique(f.MetricKeys, "samples.coverage_ratio")
			f.Evidence = append(f.Evidence, fmt.Sprintf("sample coverage ratio %.3f (%d samples; measured script %.1fms; available=%t)", coverage.Ratio, coverage.SampleCount, coverage.ScriptMs, coverage.Available))
			if f.ID == "low-sample-coverage" {
				f.Verify = []Verification{{MetricKey: "samples.coverage_ratio", Direction: "increase", Target: 0.5, Unit: metrics.UnitRatio, Description: "Capture at least half the measured script time as sample weight before trusting sampled rankings.", Command: metricCommand(c, "samples.coverage_ratio")}}
			}
		}
		for _, trigger := range implicatedTriggers(f.ID, contextTriggers(c, context)) {
			f.Triggers = appendUnique(f.Triggers, trigger.Kind+" "+trigger.Name)
			for _, function := range trigger.TopSelf {
				if len(f.Functions) >= 3 {
					break
				}
				f.Functions = appendUnique(f.Functions, functionLabel(function))
			}
		}
		if f.ID == "long-tasks" && len(c.long) > 0 {
			task := c.long[0]
			f.Functions = appendUnique(f.Functions, task.TopFunc)
			f.Triggers = appendUnique(f.Triggers, strings.TrimSpace(task.EventType+" "+task.Detail))
		}
		if context.e > context.s {
			f.SuggestedWindows = append(f.SuggestedWindows, suggestedWindow(c, context))
		}
		sort.Strings(f.Functions)
		sort.Strings(f.Triggers)
	}
}
