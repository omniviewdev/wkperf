package schema

import (
	"reflect"
	"sort"
	"strings"

	"github.com/omniviewdev/wkperf/internal/audit"
	"github.com/omniviewdev/wkperf/internal/insights"
	"github.com/omniviewdev/wkperf/internal/query"
	flamereport "github.com/omniviewdev/wkperf/internal/report/flame"
)

type definitionBuilder struct {
	definitions map[string]any
}

// Document returns a Draft 2020-12 JSON Schema for each public JSON output.
// Definitions come from the serialization types, including embedded fields,
// so nested profile contracts cannot silently drift from their Go producers.
func Document() map[string]any {
	b := &definitionBuilder{definitions: map[string]any{}}
	outputs := []struct {
		value   any
		version string
	}{
		{InsightsDocument{}, InsightsVersion},
		{AuditDocument{}, AuditVersion},
		{ComparisonDocument{}, ComparisonVersion},
		{SummaryDocument{}, SummaryVersion},
		{ErrorDocument{}, ErrorVersion},
		{flamereport.Document{}, flamereport.Schema},
	}
	branches := make([]any, 0, len(outputs))
	for _, output := range outputs {
		t := reflect.TypeOf(output.value)
		ref := b.describe(t)
		definition := b.definitions[definitionName(t)].(map[string]any)
		definition["properties"].(map[string]any)["schema"] = map[string]any{"const": output.version, "type": "string", "description": "Versioned discriminator selecting this command output contract."}
		properties := definition["properties"].(map[string]any)
		if _, exists := properties["errors"]; !exists {
			properties["errors"] = b.describe(reflect.TypeOf([]Error{}))
		}
		branches = append(branches, ref)
	}
	branches = append(branches, query.ResultSchema())
	speedscopeType := reflect.TypeOf(flamereport.Speedscope{})
	speedscopeReference := b.describe(speedscopeType)
	speedscopeDefinition := b.definitions[definitionName(speedscopeType)].(map[string]any)
	speedscopeDefinition["properties"].(map[string]any)["$schema"] = map[string]any{"type": "string", "const": "https://www.speedscope.app/file-format-schema.json", "description": "Canonical schema URL identifying the speedscope file format."}
	branches = append(branches, speedscopeReference)
	for _, name := range []string{"insights_Report", "schema_Analysis"} {
		definition := b.definitions[name].(map[string]any)
		definition["properties"].(map[string]any)["schemaVersion"] = map[string]any{"type": "string", "const": InsightsVersion, "description": "Versioned discriminator for the interned single-recording analysis contract."}
	}
	if definition, ok := b.definitions["insights_WireRootReport"].(map[string]any); ok {
		definition["properties"].(map[string]any)["schemaVersion"] = map[string]any{"type": "string", "const": "2", "description": "Version of root-cause evidence embedded in the enclosing analysis."}
	}
	errorDefinition := b.definitions[definitionName(reflect.TypeOf(ErrorDocument{}))].(map[string]any)
	errorDefinition["properties"].(map[string]any)["errors"] = map[string]any{"type": "array", "minItems": 1, "items": b.describe(reflect.TypeOf(Error{}))}
	flameDefinition := b.definitions[definitionName(reflect.TypeOf(flamereport.Document{}))].(map[string]any)
	flameDefinition["properties"].(map[string]any)["mode"] = map[string]any{"type": "string", "enum": []string{"profile", "diff"}}
	normalization := b.definitions["flame_DiffNormalization"].(map[string]any)["properties"].(map[string]any)
	normalization["mode"] = map[string]any{"type": "string", "enum": []string{"none", "per-second", "share"}}
	normalization["unit"] = map[string]any{"type": "string", "enum": []string{"ms", "ms/s", "percentage-points"}}
	for name, fields := range map[string]map[string]string{
		"insights_Report": {
			"frames":              "Canonical generated function catalog, ordered by name, URL, line and column. Frame indices are zero-based within this report.",
			"stacks":              "Exact root-to-leaf stacks stored once, keyed by canonical n_ path selectors. Detailed lists are top-N; their Total fields count all measured rows.",
			"moreEvidenceCommand": "Explicit analyze rerun with a larger top limit that reveals omitted structured evidence; source maps, selected window and analysis options are preserved.",
			"gcMs":                "Union of observed GC collector intervals, in milliseconds. Concurrent collector time is an overlay, not an exclusive main-thread work slice or a measured pause.",
			"gcOverlapWithWorkMs": "Intersection of the GC collector union with instrumented main-thread work, in milliseconds. This overlap does not establish blocking.",
			"inputStalls":         "Selected input-driven rendering intervals above the fixed 16.7 ms budget with at most one budget of instrumented work. Excluded from work-bound frame statistics and jank.",
			"inputStallsTotal":    "Complete count of observed input stalls before the shared evidence limit.",
			"slowFramesTotal":     "Complete count of work-bound slow frames before the shared evidence limit.",
			"longTasksTotal":      "Complete count of maximal overlapping script tasks above longTaskMs before the shared evidence limit.",
			"jankMs":              "Configurable threshold for slow-frame selection and finding evidence. Fixed scorecard frame standards and frames.jank_ratio remain at 16.7 ms.",
			"longTaskMs":          "Configurable threshold for selected long tasks and finding evidence. Fixed long-task scorecard standards remain at 50 ms.",
		},
		"insights_CatalogStack": {
			"frames": "Zero-based indices into this report's frames catalog, in exact root-to-leaf order. No stack depth truncation is applied.",
			"depth":  "Full number of frames in this exact sampled stack.",
		},
		"insights_WireFunction": {
			"frame":   "Zero-based index of this generated function in this report's frames catalog.",
			"stackId": "Canonical exact common-stack selector in this report's stacks table.",
		},
		"insights_WirePath": {
			"frames":     "Zero-based frame catalog indices for the complete hot-path prefix, in root-to-leaf order.",
			"nodes":      "Per-prefix inclusive/self sampled weight, share and sample support, with interned frame references. Report and query use the same supported, tie-aware path.",
			"branches":   "Selected competing children at the stopping point. branchesTotal counts every alternative before the shared evidence cap.",
			"stopReason": "Reason descent stopped: no-samples, min-share, sample-support, near-tie, or leaf.",
		},
		"insights_WireAttribution": {
			"frame": "Zero-based frame catalog index of the nearest non-framework context. Its category may be unknown-bundled or injected; this is not an application-ownership claim.",
		},
		"insights_WirePeriodic": {
			"root":         "Zero-based frame catalog index of the periodic sampled root.",
			"sampleStatus": "Limit on periodic attribution when substantial sampled weight has no timestamp.",
		},
		"insights_WireRootFinding": {
			"stackIds": "Selected exact evidence selectors in this report's stacks table; evidenceTotal retains the complete count.",
		},
		"insights_WirePathNode": {
			"metricKey": "Inclusive value key for query rootcause using this report's source/window/maps/ownership context. Complete per-prefix stats remain queryable; commands are not repeated for every node.",
			"frame":     "Zero-based frame catalog index for this exact calling-context prefix.",
		},
		"insights_WireRepeatedFrame": {
			"frame": "Zero-based frame catalog index of the repeated generated function; identity remains unchanged by ownership rules.",
		},
		"rootcause_NativeRead": {
			"flush":    "Per-API style or layout flush hypothesis, not proof of a forced flush.",
			"samples":  "Timed API sample count available for timeline corroboration.",
			"styleMs":  "Union duration of recalculate-styles records covering these samples, not sampler self weight.",
			"layoutMs": "Union duration of layout records covering these samples, not sampler self weight.",
		},
		"audit_Priority": {
			"evidenceSceneIndex": "Zero-based index into audit scenes. Stack selectors resolve in that scene's analysis.stacks table.",
			"stackIds":           "Selected exact evidence selectors scoped by evidenceSceneIndex; stacksTotal retains the complete count.",
			"depth":              "Consolidated depth facets from evidenceSceneIndex. Repeated functions refer by stable ID to that scene's analysis.frames catalog.",
		},
		"audit_WireRepeatedFrame": {
			"id": "Stable generated function ID scoped by the priority's evidenceSceneIndex; no frame object is repeated.",
		},
		"audit_HotFunction": {
			"evidenceSceneIndex": "Zero-based index into audit scenes. Function identity and exact stack resolve in that scene's analysis catalogs.",
			"stackId":            "Exact common stack selector in scenes[evidenceSceneIndex].analysis.stacks.",
		},
		"insights_SampleCoverage": {
			"timedSampleCount":   "Included samples with usable timestamps.",
			"untimedSampleCount": "Untimed sample count from the whole recording, including samples excluded by this window.",
			"untimedMs":          "Whole-recording untimed sample weight in milliseconds; weights are estimates, not elapsed time.",
			"untimedExcluded":    "True when untimed samples are excluded from the selected aggregate.",
			"untimedSpans":       "Export-order inferred spans for labels only; never used for time-based attribution.",
		},
		"insights_SlowFrame": {
			"workMs":              "Exclusive instrumented main-thread work within the active frame; excludes the GC collector overlay.",
			"gcMs":                "Observed GC collector interval union overlapping the frame, not a measured pause or an exclusive work category.",
			"gcOverlapWithWorkMs": "GC collector interval overlap with instrumented main-thread work within the frame.",
			"sampleStatus":        "Limit on timed attribution when substantial sampled weight has no timestamp.",
		},
		"insights_InputStall": {
			"t":            "Observed interval start, in seconds from the analyzed recording start.",
			"ms":           "Observed input-driven rendering interval length; not measured main-thread blocking time.",
			"workMs":       "Instrumented main-thread work in this interval, at most the fixed 16.7 ms frame budget and excluding GC collector intervals.",
			"sampleStatus": "Limits on sampled attribution. Unknown-clock samples never imply time-based causality.",
		},
		"insights_WireRootReport": {
			"inputStalls":        "Selected input-stall windows, kept separately from work-bound slow frames and ranked worst-work windows.",
			"inputStallsTotal":   "Complete input-stall window count before the shared evidence limit.",
			"contextAttribution": "Selected nearest non-framework contexts. Categories distinguish configured or source-resolved app ownership from unknown bundled or injected code.",
			"contextUnavailable": "Framework/native sampled cost with no captured non-framework context; no scheduling component is inferred.",
			"ownership":          "Effective appURLs/libraryURLs whole-URL globs. Library rules win overlap; commands preserve these settings.",
		},
		"scorecard_Scorecard": {
			"coverage": "Available standard weight divided by total weight. Aggregates retain the mean coverage of their child runs or scenes.",
			"partial":  "True when data is insufficient for an overall grade, including any ungraded child run or scene in an aggregate.",
			"score":    "Available-weight numeric score. This is a partial score, not an overall health grade, whenever partial is true.",
			"grade":    "Overall letter grade or n/a. A grade is unavailable below 50% standard coverage or when all three frame standards are unavailable; partial aggregates also remain n/a.",
		},
		"insights_LongTask": {
			"sampleStatus":  "Limit on timed attribution when substantial sampled weight has no timestamp.",
			"children":      "Selected sorted unique nested event types within this maximal overlapping script task. The shared evidence limit applies; nested records do not count as separate tasks.",
			"childrenTotal": "Complete count of unique nested event types before the shared evidence limit.",
		},
		"report_flame_Document": {
			"frames": "Generated functions interned once across all charts, ordered by generated identity. Every frame reference is a zero-based index into this array. IDs remain stable f_ selectors.",
			"view":   "Visual layout options only; exported samples and nodes always retain exact canonical root-to-leaf evidence.",
		},
		"report_flame_WireTree": {
			"root":             "Zero-based index of the synthetic root in nodes.",
			"nodes":            "Flat canonical root-to-leaf calling-context tree in deterministic preorder. Reconstruct paths using parent indices; no children or frame objects are repeated.",
			"samples":          "Timed samples with recording-relative seconds (t), original statistical milliseconds (w), exact terminal node ID (path), and optional target ID. No depth truncation or weight rounding.",
			"untimed":          "Included samples without usable timestamps, represented by exact path and weight with no t field. Never interpret array order as clock time.",
			"untimedTotal":     "Untimed sample count and weight for the whole original recording, including excluded samples.",
			"untimedIncluded":  "True when untimed sample weight contributes to this aggregate.",
			"untimed_spans":    "Recording-relative export-order spans, inferred only for labels.",
			"startTime":        "Selected wall-clock window start in recording-relative seconds.",
			"endTime":          "Selected half-open wall-clock window end in recording-relative seconds.",
			"activeDurationMs": "Selected active clock duration in milliseconds, not the sum of statistical sample weights.",
			"activeIntervals":  "Exact selected half-open active clock intervals in recording-relative seconds, including any interval selection.",
			"discontinuities":  "Recording-relative clock intervals excluded by recorder pauses.",
		},
		"report_flame_WireNode": {
			"id":      "Stable n_ calling-context selector, or root for the synthetic root.",
			"parent":  "Zero-based parent index in this tree's nodes array; -1 only for the synthetic root.",
			"frame":   "Zero-based document frames index; -1 only for the synthetic root.",
			"selfMs":  "Raw milliseconds from samples ending at this exact context.",
			"totalMs": "Raw milliseconds from samples traversing this context.",
		},
		"report_flame_WireSampleRef": {
			"path": "Terminal n_ node ID in this tree, referencing a complete exact canonical stack.",
			"w":    "Original statistical sample weight in milliseconds; not an elapsed wall-clock span.",
		},
		"report_flame_WireSample": {
			"path": "Terminal n_ node ID in this tree, referencing a complete exact canonical stack.",
			"w":    "Original statistical sample weight in milliseconds; not an elapsed wall-clock span.",
			"t":    "Usable recording-relative sample timestamp in seconds, preserved without inventing times for untimed evidence.",
		},
		"report_flame_WireNodeDelta": {
			"parent":  "Zero-based parent index in this comparison's nodes array; -1 only for the synthetic root.",
			"frame":   "Zero-based document frames index; -1 only for the synthetic root.",
			"selfMs":  "Normalized after-minus-before self delta in comparison.normalization.unit; raw beforeSelfMs and afterSelfMs remain milliseconds.",
			"totalMs": "Normalized after-minus-before inclusive delta in comparison.normalization.unit; raw beforeTotalMs and afterTotalMs remain milliseconds.",
		},
		"report_flame_WireFunctionDelta": {
			"frame":   "Zero-based document frames index for this stable function ID.",
			"selfMs":  "Normalized after-minus-before self delta in comparison.normalization.unit.",
			"totalMs": "Normalized inclusive function delta in comparison.normalization.unit, counting recursion once per sample.",
		},
		"report_flame_WireComparison": {
			"root":          "Zero-based synthetic root index in this comparison's flat nodes array.",
			"beforeTotalMs": "Unmodified baseline sampled weight in milliseconds.",
			"afterTotalMs":  "Unmodified candidate sampled weight in milliseconds.",
			"totalMs":       "Normalized after-minus-before root delta in normalization.unit.",
			"normalization": "Requested and resolved modes, delta unit, raw totals, active durations and explicit comparability warnings.",
		},
		"flame_DiffNormalization": {
			"requestedMode":  "Requested auto, none, per-second or share mode.",
			"mode":           "Resolved mode. Auto selects per-second only when both positive active durations differ by more than 10% of baseline. Missing denominators retain none with an explicit warning.",
			"unit":           "Delta unit only: ms for none, ms/s for per-second, percentage-points for share. Raw values always remain milliseconds.",
			"beforeTotalMs":  "Raw baseline selected statistical sample weight in milliseconds.",
			"afterTotalMs":   "Raw candidate selected statistical sample weight in milliseconds.",
			"beforeActiveMs": "Baseline selected active clock duration in milliseconds.",
			"afterActiveMs":  "Candidate selected active clock duration in milliseconds.",
			"warnings":       "Explicit duration-mismatch (>10%), sample-sparse (<200), and unavailable-normalization evidence warnings.",
		},
		"rootcause_Stack": {
			"stackId":        "Canonical exact path selector in the enclosing analysis report's stacks table.",
			"firstSec":       "First timed sample in recording-relative seconds, or null for an untimed-only stack.",
			"lastSec":        "Last timed sample in recording-relative seconds, or null for an untimed-only stack.",
			"untimedSamples": "Included untimed samples contributing to this exact stack.",
		},
		"report_flame_WireChart": {
			"untimedSamples":  "Untimed sample count across the chart inputs, including excluded samples.",
			"untimedMs":       "Untimed sample weight across the chart inputs in milliseconds.",
			"untimedExcluded": "True when a chart input excludes untimed sample weight.",
			"empty":           "True when the chart inputs contain no included weighted samples.",
			"emptyReason":     "Explanation for a valid selection with no weighted samples.",
		},
	} {
		definition := b.definitions[name].(map[string]any)
		properties := definition["properties"].(map[string]any)
		for field, description := range fields {
			properties[field].(map[string]any)["description"] = description
		}
	}
	cardProperties := b.definitions["scorecard_Scorecard"].(map[string]any)["properties"].(map[string]any)
	coverage := cardProperties["coverage"].(map[string]any)
	coverage["minimum"], coverage["maximum"] = float64(0), float64(1)
	applySemantics(b.definitions)
	return map[string]any{
		"$schema":     "https://json-schema.org/draft/2020-12/schema",
		"$id":         "https://wkperf.dev/schema/output.json",
		"title":       "wkperf JSON outputs",
		"description": "Complete command output contracts. Profile findings distinguish measured observations from inference; generated source locations remain available after mapping.",
		"oneOf":       branches,
		"$defs":       b.definitions,
	}
}

func definitionName(t reflect.Type) string {
	pkg := strings.TrimPrefix(t.PkgPath(), "github.com/omniviewdev/wkperf/internal/")
	pkg = strings.ReplaceAll(pkg, "/", "_")
	return pkg + "_" + t.Name()
}

func nullable(spec map[string]any) map[string]any {
	return map[string]any{"anyOf": []any{spec, map[string]any{"type": "null"}}}
}

func (b *definitionBuilder) describe(t reflect.Type) map[string]any {
	switch t.Kind() {
	case reflect.Pointer:
		return nullable(b.describe(t.Elem()))
	case reflect.Struct:
		name := definitionName(t)
		if _, exists := b.definitions[name]; !exists {
			b.definitions[name] = map[string]any{}
			properties, required := b.properties(t)
			definition := map[string]any{"type": "object", "properties": properties, "additionalProperties": false}
			if len(required) != 0 {
				definition["required"] = required
			}
			b.definitions[name] = definition
		}
		return map[string]any{"$ref": "#/$defs/" + name}
	case reflect.Slice:
		return nullable(map[string]any{"type": "array", "items": b.describe(t.Elem())})
	case reflect.Array:
		return map[string]any{"type": "array", "items": b.describe(t.Elem()), "minItems": t.Len(), "maxItems": t.Len()}
	case reflect.Map:
		return nullable(map[string]any{"type": "object", "additionalProperties": b.describe(t.Elem())})
	case reflect.String:
		spec := map[string]any{"type": "string"}
		if vocabulary, ok := stringVocabularies[t.PkgPath()+"."+t.Name()]; ok {
			spec["enum"] = vocabulary
		}
		return spec
	case reflect.Bool:
		return map[string]any{"type": "boolean"}
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64,
		reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		return map[string]any{"type": "integer"}
	case reflect.Float32, reflect.Float64:
		return map[string]any{"type": "number"}
	default:
		panic("JSON Schema needs a concrete serialized type: " + t.String())
	}
}

func (b *definitionBuilder) properties(t reflect.Type) (map[string]any, []string) {
	properties := map[string]any{}
	required := []string{}
	for name, field := range serializationFields(t) {
		properties[name] = b.describe(field.Type)
		optional := false
		for _, option := range strings.Split(field.Tag.Get("json"), ",")[1:] {
			if option == "omitempty" || option == "omitzero" {
				optional = true
			}
		}
		if !optional {
			required = append(required, name)
		}
	}
	sort.Strings(required)
	return properties, required
}

// Resolve promoted fields before describing their types. Otherwise an overridden
// in-memory field can leak obsolete evidence shapes into the published schema.
func serializationFields(t reflect.Type) map[string]reflect.StructField {
	type candidate struct {
		field     reflect.StructField
		depth     int
		tagged    bool
		ambiguous bool
	}
	candidates := map[string]candidate{}
	var collect func(reflect.Type, int)
	collect = func(t reflect.Type, depth int) {
		if t == reflect.TypeOf(insights.Report{}) {
			t = reflect.TypeOf(insights.WireReport{})
		}
		if t == reflect.TypeOf(audit.Priority{}) {
			t = reflect.TypeOf(audit.WirePriority{})
		}
		for i := range t.NumField() {
			field := t.Field(i)
			if field.PkgPath != "" {
				continue
			}
			name := strings.Split(field.Tag.Get("json"), ",")[0]
			if name == "-" {
				continue
			}
			if field.Anonymous && name == "" {
				embedded := field.Type
				if embedded.Kind() == reflect.Pointer {
					embedded = embedded.Elem()
				}
				if embedded.Kind() == reflect.Struct {
					collect(embedded, depth+1)
					continue
				}
			}
			tagged := name != ""
			if name == "" {
				name = field.Name
			}
			current, exists := candidates[name]
			if !exists || depth < current.depth || depth == current.depth && tagged && !current.tagged {
				candidates[name] = candidate{field: field, depth: depth, tagged: tagged}
			} else if depth == current.depth && tagged == current.tagged {
				current.ambiguous = true
				candidates[name] = current
			}
		}
	}
	collect(t, 0)
	fields := make(map[string]reflect.StructField, len(candidates))
	for name, candidate := range candidates {
		if !candidate.ambiguous {
			fields[name] = candidate.field
		}
	}
	return fields
}
