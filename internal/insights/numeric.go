package insights

import (
	"net/url"
	"strconv"

	"github.com/omniviewdev/wkperf/internal/metrics"
)

type NumericValue struct {
	Value       float64      `json:"value"`
	Unit        metrics.Unit `json:"unit"`
	Description string       `json:"description"`
}

// NumericCommand reproduces one exact scalar, including the original file window.
func NumericCommand(r *Report, key string) string {
	command := "wkperf query " + shellQuote(r.Source) + " metrics " + shellQuote(key) + " --format json" + queryFlags(r)
	if r.JankMs > 0 && r.JankMs != 16.7 {
		command += " --jank-ms " + seconds(r.JankMs)
	}
	if r.LongTaskMs > 0 && r.LongTaskMs != 50 {
		command += " --long-task-ms " + seconds(r.LongTaskMs)
	}
	return command
}

func queryFlags(r *Report) string {
	flags := ""
	if !r.WholeRecording && r.WindowEndSec > r.WindowStartSec {
		flags += " --from " + seconds(r.WindowStartSec) + " --to " + seconds(r.WindowEndSec)
	}
	for _, path := range r.Sourcemaps {
		flags += " --sourcemap " + shellQuote(path)
	}
	return flags
}

func functionMetricPrefix(f metrics.FuncStat) string { return "functions." + url.PathEscape(f.Key()) }
func triggerMetricPrefix(t TriggerStat) string {
	return "triggers." + t.Kind + "." + url.PathEscape(t.Name)
}

// NumericValues exposes derived report figures to the query metrics command.
func NumericValues(r *Report) map[string]NumericValue {
	values := map[string]NumericValue{}
	if r == nil {
		return values
	}
	add := func(key string, value float64, unit metrics.Unit, description string) {
		values[key] = NumericValue{Value: value, Unit: unit, Description: description}
	}
	add("recording.duration_sec", r.DurationSec, "s", "Active recording duration")
	for _, s := range r.Breakdown {
		prefix := "breakdown." + url.PathEscape(s.Name)
		add(prefix+".ms", s.Ms, metrics.UnitMs, s.Name+" wall time")
		add(prefix+".pct", s.Pct, metrics.UnitPct, s.Name+" wall-time share")
	}
	coverage := r.SampleCoverage
	add("samples.count", float64(coverage.SampleCount), metrics.UnitCount, "Weighted sample count")
	add("samples.sampled_ms", coverage.SampledMs, metrics.UnitMs, "Sampled self-time weight")
	add("samples.untimed_count", float64(coverage.UntimedSampleCount), metrics.UnitCount, "Profiler samples without timestamps")
	add("samples.untimed_ms", coverage.UntimedMs, metrics.UnitMs, "Sample weight without timestamps")
	excludedMs := 0.0
	if coverage.UntimedExcluded {
		excludedMs = coverage.UntimedMs
	}
	add("samples.untimed_excluded_ms", excludedMs, metrics.UnitMs, "Untimed sample weight excluded from this window")
	add("samples.script_ms", coverage.ScriptMs, metrics.UnitMs, "Exclusive measured script time")
	if coverage.Available {
		add("samples.coverage_ratio", coverage.Ratio, metrics.UnitRatio, "Sample weight / measured script time")
	}
	add("wakeups.count", float64(r.Wakeups.Count), metrics.UnitCount, "Active idle-gap frame wakeups")
	add("wakeups.per_second", r.Wakeups.PerSecond, "count/s", "Idle-gap frame wakeups per active second")
	addFunctions := func(prefix string, functions []metrics.FuncStat) {
		for _, f := range functions {
			base := prefix + functionMetricPrefix(f)
			add(base+".self_ms", f.SelfMs, metrics.UnitMs, "Sampled self-time weight")
			add(base+".total_ms", f.TotalMs, metrics.UnitMs, "Sampled inclusive-time weight")
			add(base+".self_samples", float64(f.SelfSamples), metrics.UnitCount, "Self sample count")
			for _, edge := range f.Callers {
				key := base + ".callers." + url.PathEscape(edge.Function.Key())
				add(key+".weight_ms", edge.WeightMs, metrics.UnitMs, "Immediate caller edge sample weight")
				add(key+".samples", float64(edge.Samples), metrics.UnitCount, "Immediate caller edge sample count")
			}
			for _, edge := range f.Callees {
				key := base + ".callees." + url.PathEscape(edge.Function.Key())
				add(key+".weight_ms", edge.WeightMs, metrics.UnitMs, "Immediate callee edge sample weight")
				add(key+".samples", float64(edge.Samples), metrics.UnitCount, "Immediate callee edge sample count")
			}
		}
	}
	if r.Summary != nil {
		addFunctions("", r.Summary.Functions)
		for _, grouping := range []string{"file", "parent-directory"} {
			for _, m := range computeModules(r.Summary.Functions, grouping) {
				prefix := "modules." + grouping + "." + url.PathEscape(m.Name)
				add(prefix+".self_ms", m.SelfMs, metrics.UnitMs, "Grouped sampled self time")
				add(prefix+".pct", m.Pct, metrics.UnitPct, "Share of total sampled self time")
			}
		}
	} else {
		addFunctions("", r.Hotspots)
		addFunctions("", r.InclusiveHotspots)
	}
	addTriggers := func(prefix string, triggers []TriggerStat) {
		for _, t := range triggers {
			base := prefix + triggerMetricPrefix(t)
			add(base+".count", float64(t.Count), metrics.UnitCount, "Trigger count")
			add(base+".total_ms", t.TotalMs, metrics.UnitMs, "Total recorded trigger duration")
			add(base+".mean_ms", t.MeanMs, metrics.UnitMs, "Mean recorded trigger duration")
			add(base+".p95_ms", t.P95Ms, metrics.UnitMs, "p95 recorded trigger duration")
			add(base+".max_ms", t.MaxMs, metrics.UnitMs, "Maximum recorded trigger duration")
			addFunctions(base+".", t.TopSelf)
			addFunctions(base+".", t.TopInclusive)
		}
	}
	addTriggers("", r.Triggers)
	addTriggers("wakeups.", r.Wakeups.Causes)
	for i, stack := range r.CommonStacks {
		base := "commonStacks." + strconv.Itoa(i)
		add(base+".weight_ms", stack.WeightMs, metrics.UnitMs, "Exact common stack sample weight")
		add(base+".samples", float64(stack.Samples), metrics.UnitCount, "Exact common stack sample count")
	}
	for i, f := range r.SlowFrames {
		base := "slowFrames." + strconv.Itoa(i)
		add(base+".t", f.T, "s", "Frame time from analyzed recording start")
		add(base+".ms", f.Ms, metrics.UnitMs, "Active frame duration")
		add(base+".script_ms", f.ScriptMs, metrics.UnitMs, "Exclusive script overlap")
		add(base+".layout_ms", f.LayoutMs, metrics.UnitMs, "Exclusive layout/style overlap")
		add(base+".paint_ms", f.PaintMs, metrics.UnitMs, "Exclusive paint/composite overlap")
		add(base+".gc_ms", f.GCMs, metrics.UnitMs, "Exclusive GC overlap")
	}
	for i, t := range r.LongTasks {
		base := "longTasks." + strconv.Itoa(i)
		add(base+".t", t.T, "s", "Task time from analyzed recording start")
		add(base+".ms", t.Ms, metrics.UnitMs, "Measured long-task duration")
	}
	for _, f := range r.Findings {
		base := "findings." + f.ID
		add(base+".impact_ms", f.ImpactMs, metrics.UnitMs, "Attributed finding impact; findings are not additive")
		add(base+".impact_pct", f.ImpactPct, metrics.UnitPct, "Attributed share of active wall time")
		for _, e := range f.NumericEvidence {
			if _, exists := values[e.MetricKey]; !exists {
				add(e.MetricKey, e.Value, e.Unit, e.Description)
			}
		}
	}
	return values
}

func TriggerCommand(r *Report, trigger TriggerStat, field string) string {
	return NumericCommand(r, triggerMetricPrefix(trigger)+"."+field)
}

func FunctionCommand(r *Report, function metrics.FuncStat, field string) string {
	return NumericCommand(r, functionMetricPrefix(function)+"."+field)
}

func TriggerFunctionCommand(r *Report, trigger TriggerStat, function metrics.FuncStat, field string) string {
	return NumericCommand(r, triggerMetricPrefix(trigger)+"."+functionMetricPrefix(function)+"."+field)
}

func EdgeCommand(r *Report, function metrics.FuncStat, edge metrics.CallEdge, relation, field string) string {
	return NumericCommand(r, functionMetricPrefix(function)+"."+relation+"."+url.PathEscape(edge.Function.Key())+"."+field)
}

func StackCommand(r *Report, index int, field string) string {
	return NumericCommand(r, "commonStacks."+strconv.Itoa(index)+"."+field)
}

func FunctionStackCommand(r *Report, function metrics.FuncStat) string {
	if function.CommonStackID != "" {
		return "wkperf query " + shellQuote(r.Source) + " stacks " + shellQuote(function.CommonStackID) + " --format json" + queryFlags(r)
	}
	selector := function.ID
	if selector == "" {
		selector = function.URL + ":" + strconv.Itoa(function.Line) + ":" + strconv.Itoa(function.Column)
	}
	return "wkperf query " + shellQuote(r.Source) + " paths " + shellQuote(selector) + " --format json" + queryFlags(r)
}
