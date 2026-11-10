package schema

import (
	"reflect"
	"sort"
	"strings"

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
		{query.Result{}, query.Schema},
	}
	branches := make([]any, 0, len(outputs))
	for _, output := range outputs {
		t := reflect.TypeOf(output.value)
		ref := b.describe(t)
		definition := b.definitions[definitionName(t)].(map[string]any)
		definition["properties"].(map[string]any)["schema"] = map[string]any{"const": output.version, "type": "string"}
		properties := definition["properties"].(map[string]any)
		if _, exists := properties["errors"]; !exists {
			properties["errors"] = b.describe(reflect.TypeOf([]Error{}))
		}
		branches = append(branches, ref)
	}
	speedscopeType := reflect.TypeOf(flamereport.Speedscope{})
	speedscopeReference := b.describe(speedscopeType)
	speedscopeDefinition := b.definitions[definitionName(speedscopeType)].(map[string]any)
	speedscopeDefinition["properties"].(map[string]any)["$schema"] = map[string]any{"type": "string", "const": "https://www.speedscope.app/file-format-schema.json"}
	branches = append(branches, speedscopeReference)
	if definition, ok := b.definitions["insights_Report"].(map[string]any); ok {
		definition["properties"].(map[string]any)["schemaVersion"] = map[string]any{"type": "string", "const": InsightsVersion}
	}
	if definition, ok := b.definitions["insights_WireRootReport"].(map[string]any); ok {
		definition["properties"].(map[string]any)["schemaVersion"] = map[string]any{"type": "string", "const": "2"}
	}
	errorDefinition := b.definitions[definitionName(reflect.TypeOf(ErrorDocument{}))].(map[string]any)
	errorDefinition["properties"].(map[string]any)["errors"] = map[string]any{"type": "array", "minItems": 1, "items": b.describe(reflect.TypeOf(Error{}))}
	flameDefinition := b.definitions[definitionName(reflect.TypeOf(flamereport.Document{}))].(map[string]any)
	flameDefinition["properties"].(map[string]any)["mode"] = map[string]any{"type": "string", "enum": []string{"profile", "diff"}}
	for name, fields := range map[string]map[string]string{
		"insights_Report": {
			"frames":              "Canonical generated function catalog, ordered by name, URL, line and column. Frame indices are zero-based within this report.",
			"stacks":              "Exact root-to-leaf stacks stored once, keyed by canonical n_ path selectors. Detailed lists are top-N; their Total fields count all measured rows.",
			"moreEvidenceCommand": "Explicit analyze rerun with a larger top limit that reveals omitted structured evidence; source maps, selected window and analysis options are preserved.",
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
			"frames": "Zero-based frame catalog indices for the complete hot-path prefix, in root-to-leaf order.",
		},
		"insights_WireAttribution": {
			"app": "Zero-based frame catalog index of the nearest application ancestor.",
		},
		"insights_WirePeriodic": {
			"root":         "Zero-based frame catalog index of the periodic sampled root.",
			"sampleStatus": "Limit on periodic attribution when substantial sampled weight has no timestamp.",
		},
		"insights_WireRootFinding": {
			"stackIds": "Selected exact evidence selectors in this report's stacks table; evidenceTotal retains the complete count.",
		},
		"audit_Priority": {
			"evidenceSceneIndex": "Zero-based index into audit scenes. Stack selectors resolve in that scene's analysis.stacks table.",
			"stackIds":           "Selected exact evidence selectors scoped by evidenceSceneIndex; stacksTotal retains the complete count.",
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
			"sampleStatus": "Limit on timed attribution when substantial sampled weight has no timestamp.",
		},
		"insights_LongTask": {
			"sampleStatus": "Limit on timed attribution when substantial sampled weight has no timestamp.",
		},
		"flame_Tree": {
			"untimed":         "Included samples without usable timestamps; timestamp values in this separate array must not be interpreted as times.",
			"untimedTotal":    "Untimed sample count and weight for the whole original recording, including excluded samples.",
			"untimedIncluded": "True when untimed sample weight contributes to this aggregate.",
			"untimed_spans":   "Selected-recording-relative export-order spans, inferred only for labels.",
		},
		"query_Source": {
			"untimed_samples":  "Whole-recording untimed sample count, preserved for partial windows.",
			"untimed_ms":       "Whole-recording untimed sample weight in milliseconds.",
			"untimed_excluded": "True when this selection excludes untimed sample weight.",
			"untimed_spans":    "Recording-relative export-order spans, inferred only for labels.",
		},
		"rootcause_Stack": {
			"stackId":        "Canonical exact path selector in the enclosing analysis report's stacks table.",
			"firstSec":       "First timed sample in recording-relative seconds, or null for an untimed-only stack.",
			"lastSec":        "Last timed sample in recording-relative seconds, or null for an untimed-only stack.",
			"untimedSamples": "Included untimed samples contributing to this exact stack.",
		},
		"report_flame_Chart": {
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
		switch t.PkgPath() + "." + t.Name() {
		case "github.com/omniviewdev/wkperf/internal/insights.Severity":
			spec["enum"] = []string{"high", "medium", "low", "info"}
		case "github.com/omniviewdev/wkperf/internal/scorecard.Rating":
			spec["enum"] = []string{"good", "needs-improvement", "poor", "n/a"}
		case "github.com/omniviewdev/wkperf/internal/compare.Verdict":
			spec["enum"] = []string{"improved", "regressed", "unchanged", "noisy", "info", "new", "removed"}
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
