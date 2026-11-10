package query

import (
	"strings"

	"github.com/omniviewdev/wkperf/internal/flame"
	"github.com/omniviewdev/wkperf/internal/model"
	"github.com/omniviewdev/wkperf/internal/profile"
)

// scalarRows serves basic recording and sampling facts without building findings,
// ownership attribution, detector windows, or other full-analysis evidence.
func scalarRows(rec *model.Recording, tree *flame.Tree, source Source, operation string) []Row {
	rows := make([]Row, 0, 14)
	add := func(key string, value float64, unit, description, basis, alias string) {
		rows = append(rows, Row{Key: key, Value: clean(value), Unit: unit, Description: description, Basis: basis, AliasOf: alias, Operation: operation})
	}
	if operation == "metrics" {
		active := 0.0
		wall := max(0, source.To-source.From)
		recordCount := 0
		start, end := rec.StartTime+source.From, rec.StartTime+source.To
		activeIntervals := profile.ActiveIntervals(rec, start, end)
		for _, interval := range activeIntervals {
			active += interval.End - interval.Start
		}
		unbounded := rec.EndTime <= rec.StartTime && len(rec.Discontinuities) == 0 && source.From == 0 && source.To == 0
		for _, record := range rec.Records {
			keep := unbounded
			if !unbounded {
				switch record.Type {
				case model.TypeCPU, model.TypeMemory:
					keep = profile.Contains(activeIntervals, record.Timestamp)
				case model.TypeNetwork:
					keep = record.StartTime == 0 && record.EndTime == 0
				}
			}
			if record.Type != model.TypeCPU && record.Type != model.TypeMemory && !keep {
				if record.EndTime <= record.StartTime {
					keep = profile.Contains(activeIntervals, record.StartTime)
				} else {
					for _, interval := range activeIntervals {
						if record.EndTime > interval.Start && record.StartTime < interval.End {
							keep = true
							break
						}
					}
				}
			}
			if keep {
				recordCount++
			}
		}
		add("overview.duration_s", active, "s", "Selected active recording duration, excluding stopped capture intervals", "measured", "")
		add("recording.duration_sec", active, "s", "Exact alias of metrics:overview.duration_s; seconds rounded to nine decimal places", "measured", "metrics:overview.duration_s")
		add("overview.wall_duration_s", wall, "s", "Selected elapsed recording duration, including stopped capture intervals", "measured", "")
		add("overview.records", float64(recordCount), "count", "Timeline records retained by the selected recording window", "measured", "")
		add("overview.paused_s", max(0, wall-active), "s", "Stopped capture duration within selected recording window", "measured", "")
	}
	sampledKey, alias := "samples.sampled_ms", ""
	if operation == "rootcause" {
		sampledKey, alias = "samples.self_ms", "metrics:samples.sampled_ms"
	}
	add(sampledKey, tree.Root.TotalMs, "ms", "Retained sampled self-time weight; milliseconds rounded to nine decimal places", "sampled", alias)
	countAlias, untimedCountAlias, untimedMsAlias := "", "", ""
	if operation == "rootcause" {
		countAlias = "metrics:samples.count"
		untimedCountAlias = "metrics:samples.untimed_count"
		untimedMsAlias = "metrics:samples.untimed_ms"
	}
	add("samples.count", float64(tree.SampleCount()), "count", "Retained positive-weight profiler samples; weights do not increase sample count", "sampled", countAlias)
	add("samples.untimed_count", float64(tree.UntimedTotal.Count), "count", "Recording-wide profiler samples without timestamps, including samples excluded by a selected window", "sampled", untimedCountAlias)
	add("samples.untimed_ms", tree.UntimedTotal.Ms, "ms", "Recording-wide profiler sample weight without timestamps", "sampled", untimedMsAlias)
	excludedMs := 0.0
	if !tree.UntimedIncluded {
		excludedMs = tree.UntimedTotal.Ms
	}
	excludedAlias := ""
	if operation == "rootcause" {
		excludedAlias = "metrics:samples.untimed_excluded_ms"
	}
	add("samples.untimed_excluded_ms", excludedMs, "ms", "Untimed sample weight excluded from the selected window", "sampled", excludedAlias)
	if operation == "metrics" {
		scriptMs := profile.Measure(rec, rec.StartTime+source.From, rec.StartTime+source.To).ScriptMs
		add("samples.script_ms", scriptMs, "ms", "Exclusive measured script time in the selected active recording window", "measured", "")
		if scriptMs > 0 {
			add("samples.coverage_ratio", tree.Root.TotalMs/scriptMs, "ratio", "Retained sampled weight divided by exclusive measured script time; may exceed one", "sampled", "")
		}
	}
	return rows
}

func isScalarSelector(operation, selector string) bool {
	if selector == "" || strings.HasPrefix(selector, "/") {
		return false
	}
	if operation == "metrics" && (strings.HasPrefix(selector, "overview.") || strings.HasPrefix(selector, "recording.")) {
		return true
	}
	if !strings.HasPrefix(selector, "samples.") {
		return false
	}
	if operation == "metrics" {
		return true
	}
	if operation == "rootcause" {
		switch selector {
		case "samples.self_ms", "samples.count", "samples.untimed_count", "samples.untimed_ms", "samples.untimed_excluded_ms", "samples.*":
			return true
		}
	}
	return false
}
