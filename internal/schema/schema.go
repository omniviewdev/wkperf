package schema

import (
	"reflect"
	"sort"
	"strings"

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
	errorDefinition := b.definitions[definitionName(reflect.TypeOf(ErrorDocument{}))].(map[string]any)
	errorDefinition["properties"].(map[string]any)["errors"] = map[string]any{"type": "array", "minItems": 1, "items": b.describe(reflect.TypeOf(Error{}))}
	flameDefinition := b.definitions[definitionName(reflect.TypeOf(flamereport.Document{}))].(map[string]any)
	flameDefinition["properties"].(map[string]any)["mode"] = map[string]any{"type": "string", "enum": []string{"profile", "diff"}}
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
	requiredSet := map[string]bool{}
	for i := range t.NumField() {
		field := t.Field(i)
		if field.PkgPath != "" {
			continue
		}
		tag := strings.Split(field.Tag.Get("json"), ",")
		if tag[0] == "-" {
			continue
		}
		if field.Anonymous && tag[0] == "" {
			embedded := field.Type
			if embedded.Kind() == reflect.Pointer {
				embedded = embedded.Elem()
			}
			if embedded.Kind() == reflect.Struct {
				nested, required := b.properties(embedded)
				for name, spec := range nested {
					properties[name] = spec
				}
				for _, name := range required {
					requiredSet[name] = true
				}
				continue
			}
		}
		name := tag[0]
		if name == "" {
			name = field.Name
		}
		properties[name] = b.describe(field.Type)
		optional := false
		for _, option := range tag[1:] {
			if option == "omitempty" || option == "omitzero" {
				optional = true
			}
		}
		if !optional {
			requiredSet[name] = true
		}
	}
	required := make([]string, 0, len(requiredSet))
	for name := range requiredSet {
		required = append(required, name)
	}
	sort.Strings(required)
	return properties, required
}
