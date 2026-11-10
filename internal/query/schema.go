package query

import (
	"reflect"
	"strings"
)

// ResultSchema describes every concrete field shared by CLI and MCP results.
func ResultSchema() map[string]any {
	out := objectSchema(reflect.TypeFor[Result]())
	out["$schema"] = "https://json-schema.org/draft/2020-12/schema"
	out["title"] = Schema
	properties := out["properties"].(map[string]any)
	properties["schema"] = map[string]any{"type": "string", "const": Schema}
	properties["operation"] = map[string]any{"type": "string", "enum": Operations}
	properties["status"] = map[string]any{"type": "string", "enum": []string{"ok", "ambiguous"}}
	properties["by"] = map[string]any{"type": "string", "enum": []string{"self", "total"}}
	properties["limit"] = map[string]any{"type": "integer", "minimum": 1, "maximum": MaxLimit}
	properties["total_count"] = map[string]any{"type": "integer", "minimum": 0}
	source := properties["sources"].(map[string]any)["items"].(map[string]any)["properties"].(map[string]any)
	for field, description := range map[string]string{
		"untimed_samples":  "Whole-recording untimed sample count, preserved for partial windows.",
		"untimed_ms":       "Whole-recording untimed sample weight in milliseconds.",
		"untimed_excluded": "True when the selected window excludes untimed sample weight.",
		"untimed_spans":    "Recording-relative export-order spans, inferred only for labels; never used for attribution.",
	} {
		source[field].(map[string]any)["description"] = description
	}
	return out
}

// RequestSchema is the tool-input schema. Operation comes from the tool name.
func RequestSchema(operation string) map[string]any {
	out := objectSchema(reflect.TypeFor[Request]())
	properties := out["properties"].(map[string]any)
	delete(properties, "operation")
	out["required"] = []string{"file"}
	if operation == "diff" {
		out["required"] = []string{"file", "candidate"}
	}
	if operation == "callers" || operation == "callees" || operation == "paths" || operation == "subtree" || operation == "find" {
		out["required"] = []string{"file", "selector"}
	}
	if operation == "window" {
		operations := make([]string, 0, len(Operations)-1)
		for _, op := range Operations {
			if op != "window" {
				operations = append(operations, op)
			}
		}
		out["required"] = []string{"file", "from", "to"}
		properties["query_operation"] = map[string]any{"type": "string", "enum": operations, "default": "top"}
	} else {
		delete(properties, "query_operation")
	}
	properties["file"] = map[string]any{"type": "string", "minLength": 1, "description": "Local WebKit recording path; never executed as a command."}
	properties["limit"] = map[string]any{"type": "integer", "minimum": 0, "maximum": MaxLimit, "default": DefaultLimit, "description": "Maximum rows; zero selects the bounded default."}
	properties["by"] = map[string]any{"type": "string", "enum": []string{"self", "total"}, "default": "self"}
	properties["category"] = map[string]any{"type": "string", "enum": []string{"", "app", "framework", "native", "gc"}}
	properties["depth"] = map[string]any{"type": "integer", "minimum": 0, "default": 8}
	properties["top"] = map[string]any{"type": "integer", "minimum": 0, "default": 10, "description": "Root-cause detail size, independent of returned row limit; zero selects ten."}
	for _, name := range []string{"from", "to", "trim_start", "trim_end", "min_ms", "min_share", "long_task_ms", "jank_ms"} {
		if _, ok := properties[name]; ok {
			properties[name] = map[string]any{"type": "number", "minimum": 0}
		}
	}
	properties["min_share"] = map[string]any{"type": "number", "minimum": 0, "maximum": 1, "default": .05}
	properties["jank_ms"] = map[string]any{"type": "number", "minimum": 0, "default": 16.7, "description": "Slow-frame and findings.jank.* threshold in milliseconds; zero selects 16.7. Fixed frames.* scorecard standards remain at 16.7 ms."}
	properties["long_task_ms"] = map[string]any{"type": "number", "minimum": 0, "default": float64(50), "description": "Maximal script-task threshold for derived finding and root-cause evidence in milliseconds; zero selects 50. Fixed long-task scorecard standards remain at 50 ms."}
	return out
}

func objectSchema(t reflect.Type) map[string]any {
	properties := map[string]any{}
	required := []string{}
	for i := range t.NumField() {
		field := t.Field(i)
		if field.Anonymous {
			nested := objectSchema(field.Type)
			for name, schema := range nested["properties"].(map[string]any) {
				properties[name] = schema
			}
			required = append(required, nested["required"].([]string)...)
			continue
		}
		tag := strings.Split(field.Tag.Get("json"), ",")
		name := tag[0]
		if name == "-" {
			continue
		}
		if name == "" {
			name = field.Name
		}
		properties[name] = typeSchema(field.Type)
		optional := false
		for _, option := range tag[1:] {
			if option == "omitempty" {
				optional = true
			}
		}
		if !optional {
			required = append(required, name)
		}
	}
	return map[string]any{"type": "object", "properties": properties, "required": required, "additionalProperties": false}
}

func typeSchema(t reflect.Type) map[string]any {
	if t.Kind() == reflect.Pointer {
		return typeSchema(t.Elem())
	}
	switch t.Kind() {
	case reflect.Struct:
		return objectSchema(t)
	case reflect.Slice, reflect.Array:
		return map[string]any{"type": "array", "items": typeSchema(t.Elem())}
	case reflect.String:
		return map[string]any{"type": "string"}
	case reflect.Bool:
		return map[string]any{"type": "boolean"}
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64, reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		return map[string]any{"type": "integer"}
	case reflect.Float32, reflect.Float64:
		return map[string]any{"type": "number"}
	default:
		panic("query schema contains unsupported type: " + t.String())
	}
}
