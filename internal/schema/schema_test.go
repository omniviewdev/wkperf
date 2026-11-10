package schema

import (
	"bytes"
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/omniviewdev/wkperf/internal/analyze"
	"github.com/omniviewdev/wkperf/internal/audit"
	"github.com/omniviewdev/wkperf/internal/compare"
	"github.com/omniviewdev/wkperf/internal/flame"
	"github.com/omniviewdev/wkperf/internal/insights"
	"github.com/omniviewdev/wkperf/internal/metrics"
	"github.com/omniviewdev/wkperf/internal/model"
	"github.com/omniviewdev/wkperf/internal/query"
	flamereport "github.com/omniviewdev/wkperf/internal/report/flame"
	"github.com/omniviewdev/wkperf/internal/scorecard"
	"github.com/omniviewdev/wkperf/internal/testgen"
)

func TestDocumentDefinesCompleteOutputContracts(t *testing.T) {
	doc := Document()
	if doc["$schema"] != "https://json-schema.org/draft/2020-12/schema" {
		t.Fatalf("wrong JSON Schema dialect: %v", doc["$schema"])
	}
	data, err := json.Marshal(doc)
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"wkperf-insights/v3", "wkperf/v1", "wkperf-audit/v2", "wkperf-error/v1", "hotspots", "inclusiveHotspots", "column", "callers", "callees", "stacks", "triggers", "findings", "evidence", "suggestedWindows", "errors", "rating"} {
		if !strings.Contains(string(data), name) {
			t.Errorf("schema omits %s", name)
		}
	}
	var decoded map[string]any
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatal(err)
	}
	defs, ok := decoded["$defs"].(map[string]any)
	if !ok || len(defs) < 15 {
		t.Fatalf("missing nested type definitions: %d", len(defs))
	}
	for name, value := range defs {
		spec := value.(map[string]any)
		if spec["type"] == "object" && spec["properties"] == nil && spec["additionalProperties"] == nil {
			t.Errorf("broad object placeholder: %s", name)
		}
	}
}

func TestValidateRejectsInvalidNestedOutput(t *testing.T) {
	doc := Document()
	valid := map[string]any{
		"schema": "wkperf-error/v1",
		"errors": []any{map[string]any{"file": "broken.json", "message": "invalid JSON", "code": "load_error"}},
	}
	if err := Validate(doc, valid); err != nil {
		t.Fatal(err)
	}
	invalid := map[string]any{
		"schema": "wkperf-error/v1",
		"errors": []any{map[string]any{"file": "broken.json", "message": 7.0, "code": "load_error"}},
	}
	if err := Validate(doc, invalid); err == nil {
		t.Fatal("validator accepted wrong nested message type")
	}
	if reflect.DeepEqual(doc, map[string]any{}) {
		t.Fatal("empty schema")
	}
}

func TestDocumentValidatesRealPipelineOutputs(t *testing.T) {
	document := Document()
	for _, fixture := range []struct {
		name     string
		generate func() *model.Recording
	}{
		{"idle", testgen.IdleMinified},
		{"deep", testgen.DeepMinified},
		{"native", testgen.GetAnimations},
		{"react", testgen.React},
		{"periodic", testgen.Periodic},
	} {
		t.Run(fixture.name, func(t *testing.T) {
			rec := fixture.generate()
			report := insights.Analyze(rec, "fixture.json", insights.Options{})
			card := scorecard.Evaluate(rec, report)
			summary := analyze.Summarize(rec, "fixture.json")
			outputs := []any{
				InsightsDocument{Schema: InsightsVersion, Reports: []Analysis{{Report: report, Scorecard: card}}},
				AuditDocument{Schema: AuditVersion, Report: audit.Build([]audit.Scene{{Name: "fixture", Runs: []audit.Run{{Source: "fixture.json", Report: report, Card: card}}}}, audit.Options{})},
				ComparisonDocument{Schema: ComparisonVersion, Result: compare.Compare([]*metrics.Summary{summary}, []*metrics.Summary{summary}, compare.DefaultOptions())},
				SummaryDocument{Schema: SummaryVersion, Sources: []string{"fixture.json"}, Runs: 1, Metrics: summary.Metrics},
				flamereport.Document{Schema: flamereport.Schema, Mode: "profile", Charts: []flamereport.Chart{{Label: "fixture", Tree: flame.Build(rec)}}},
				query.Result{Schema: query.Schema, Operation: "top", Status: "ok", Sources: []query.Source{{File: "fixture.json"}}, Command: []string{"wkperf", "query", "fixture.json", "top"}, Rows: []query.Row{}},
			}
			for _, output := range outputs {
				data, err := json.Marshal(output)
				if err != nil {
					t.Fatal(err)
				}
				var value any
				if err := json.Unmarshal(data, &value); err != nil {
					t.Fatal(err)
				}
				if err := Validate(document, value); err != nil {
					t.Fatalf("%T: %v", output, err)
				}
			}
		})
	}
}

// Validate exercises the generated JSON Schema subset without adding a runtime dependency.
func Validate(root map[string]any, value any) error {
	data, err := json.Marshal(root)
	if err != nil {
		return err
	}
	var normalized map[string]any
	if err := json.Unmarshal(data, &normalized); err != nil {
		return err
	}
	var check func(map[string]any, any) error
	check = func(spec map[string]any, value any) error {
		if ref, ok := spec["$ref"].(string); ok {
			name := strings.TrimPrefix(ref, "#/$defs/")
			def, ok := normalized["$defs"].(map[string]any)[name].(map[string]any)
			if !ok {
				return fmt.Errorf("unresolved ref %s", ref)
			}
			return check(def, value)
		}
		for _, combinator := range []string{"oneOf", "anyOf"} {
			if alternatives, ok := spec[combinator].([]any); ok {
				matches := 0
				for _, alternative := range alternatives {
					if check(alternative.(map[string]any), value) == nil {
						matches++
					}
				}
				if matches == 0 || combinator == "oneOf" && matches != 1 {
					return fmt.Errorf("%s matched %d branches", combinator, matches)
				}
			}
		}
		if constant, ok := spec["const"]; ok && !reflect.DeepEqual(constant, value) {
			return fmt.Errorf("want const %v, got %v", constant, value)
		}
		if values, ok := spec["enum"].([]any); ok {
			found := false
			for _, allowed := range values {
				if reflect.DeepEqual(allowed, value) {
					found = true
				}
			}
			if !found {
				return fmt.Errorf("value %v not in enum", value)
			}
		}
		kind, _ := spec["type"].(string)
		switch kind {
		case "null":
			if value != nil {
				return fmt.Errorf("want null, got %T", value)
			}
		case "string":
			if _, ok := value.(string); !ok {
				return fmt.Errorf("want string, got %T", value)
			}
		case "number", "integer":
			if _, ok := value.(float64); !ok {
				return fmt.Errorf("want number, got %T", value)
			}
		case "boolean":
			if _, ok := value.(bool); !ok {
				return fmt.Errorf("want boolean, got %T", value)
			}
		case "array":
			values, ok := value.([]any)
			if !ok {
				return fmt.Errorf("want array, got %T", value)
			}
			item, _ := spec["items"].(map[string]any)
			for i, entry := range values {
				if err := check(item, entry); err != nil {
					return fmt.Errorf("item %d: %w", i, err)
				}
			}
		case "object":
			object, ok := value.(map[string]any)
			if !ok {
				return fmt.Errorf("want object, got %T", value)
			}
			props, _ := spec["properties"].(map[string]any)
			if required, ok := spec["required"].([]any); ok {
				for _, name := range required {
					if _, ok := object[name.(string)]; !ok {
						return fmt.Errorf("missing %s", name)
					}
				}
			}
			for name, entry := range object {
				if property, ok := props[name].(map[string]any); ok {
					if err := check(property, entry); err != nil {
						return fmt.Errorf("%s: %w", name, err)
					}
				} else if allowed, ok := spec["additionalProperties"].(bool); ok && !allowed {
					return fmt.Errorf("unknown property %s", name)
				} else if additional, ok := spec["additionalProperties"].(map[string]any); ok {
					if err := check(additional, entry); err != nil {
						return err
					}
				}
			}
		}
		return nil
	}
	return check(normalized, value)
}

func TestDocumentValidatesRenderedFlameVariants(t *testing.T) {
	tree := flame.Build(testgen.GetAnimations())
	charts := []flamereport.Chart{{Label: "native", Tree: tree}}
	for _, format := range []string{"json", "speedscope"} {
		var out bytes.Buffer
		if err := flamereport.Render(&out, format, charts, flamereport.Options{}); err != nil {
			t.Fatal(err)
		}
		var value any
		if err := json.Unmarshal(out.Bytes(), &value); err != nil {
			t.Fatal(err)
		}
		if err := Validate(Document(), value); err != nil {
			t.Fatalf("%s: %v", format, err)
		}
	}
}
