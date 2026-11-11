package schema

import (
	"context"
	"encoding/json"
	"reflect"
	"sort"
	"strings"
	"testing"

	"github.com/omniviewdev/wkperf/internal/analyze"
	"github.com/omniviewdev/wkperf/internal/compare"
	"github.com/omniviewdev/wkperf/internal/flame"
	"github.com/omniviewdev/wkperf/internal/insights"
	"github.com/omniviewdev/wkperf/internal/metrics"
	"github.com/omniviewdev/wkperf/internal/model"
	"github.com/omniviewdev/wkperf/internal/query"
	flamereport "github.com/omniviewdev/wkperf/internal/report/flame"
	"github.com/omniviewdev/wkperf/internal/sourcemap"
	"github.com/omniviewdev/wkperf/internal/testgen"
)

func TestEveryPrimitiveSchemaLeafExplainsMeaningAndNumericUnit(t *testing.T) {
	var missing []string
	var visit func(map[string]any, string)
	visit = func(spec map[string]any, path string) {
		kind, _ := spec["type"].(string)
		if kind == "string" || kind == "number" || kind == "integer" || kind == "boolean" {
			description, _ := spec["description"].(string)
			if len(strings.Fields(description)) < 4 || strings.Contains(strings.ToLower(description), "field value") {
				missing = append(missing, path+": semantic description")
			}
			if kind == "number" || kind == "integer" {
				if unit, _ := spec["x-unit"].(string); unit == "" {
					missing = append(missing, path+": numeric unit")
				}
			}
		}
		for _, key := range []string{"$defs", "properties"} {
			if children, ok := spec[key].(map[string]any); ok {
				for name, raw := range children {
					visit(raw.(map[string]any), path+"/"+name)
				}
			}
		}
		for _, key := range []string{"items", "additionalProperties"} {
			if child, ok := spec[key].(map[string]any); ok {
				visit(child, path+"/"+key)
			}
		}
		for _, key := range []string{"oneOf", "anyOf", "allOf"} {
			if children, ok := spec[key].([]any); ok {
				for _, raw := range children {
					visit(raw.(map[string]any), path+"/"+key)
				}
			}
		}
	}
	visit(Document(), "output")
	sort.Strings(missing)
	if len(missing) != 0 {
		t.Fatalf("undocumented primitive leaves:\n%s", strings.Join(missing, "\n"))
	}
}

func TestSchemaUnitsDistinguishClockWeightsSharesAndDynamicMetrics(t *testing.T) {
	definitions := Document()["$defs"].(map[string]any)
	for _, test := range []struct{ definition, field, unit, meaning string }{
		{"insights_Report", "durationSec", "s", "active"},
		{"insights_SampleCoverage", "sampledMs", "ms", "weight"},
		{"insights_SampleCoverage", "ratio", "ratio", "exceed"},
		{"insights_Slice", "pct", "%", "active"},
		{"rootcause_Stack", "firstSec", "s", "null"},
		{"report_flame_WireNodeDelta", "selfMs", "comparison.normalization.unit", "normalized"},
		{"metrics_Metric", "value", "unit", "unit"},
		{"compare_MetricDiff", "confidence", "", "overlap"},
	} {
		t.Run(test.definition+"."+test.field, func(t *testing.T) {
			property := definitions[test.definition].(map[string]any)["properties"].(map[string]any)[test.field].(map[string]any)
			if test.unit != "" && property["x-unit"] != test.unit {
				t.Errorf("unit = %v, want %s", property["x-unit"], test.unit)
			}
			description, _ := property["description"].(string)
			if !strings.Contains(strings.ToLower(description), test.meaning) {
				t.Errorf("missing %q semantics: %q", test.meaning, description)
			}
		})
	}
}

func TestComparisonV2UsesCamelCaseCounts(t *testing.T) {
	summary := analyze.Summarize(testgen.GetAnimations(), "fixture.json")
	comparison := compare.Compare([]*metrics.Summary{summary}, []*metrics.Summary{summary}, compare.DefaultOptions())
	data, err := json.Marshal(ComparisonDocument{Schema: ComparisonVersion, Result: comparison})
	if err != nil {
		t.Fatal(err)
	}
	var value map[string]any
	if err := json.Unmarshal(data, &value); err != nil {
		t.Fatal(err)
	}
	if value["schema"] != "wkperf/v2" {
		t.Fatalf("breaking count migration kept old discriminator: %v", value["schema"])
	}
	counts := value["counts"].(map[string]any)
	keys := make([]string, 0, len(counts))
	for key := range counts {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	if !reflect.DeepEqual(keys, []string{"improved", "noisy", "regressed", "unchanged"}) {
		t.Fatalf("comparison count wire keys: %v", keys)
	}
	if err := Validate(Document(), value); err != nil {
		t.Fatal(err)
	}
	value["schema"] = "wkperf/v1"
	if err := Validate(Document(), value); err == nil {
		t.Fatal("schema still accepts silently changed v1 comparison")
	}
}

func TestSchemaGeneratedLocationsAllowUnknownSentinels(t *testing.T) {
	definitions := Document()["$defs"].(map[string]any)
	for _, name := range []string{"flame_Frame", "flame_Location", "model_SourceLocation", "metrics_FunctionFrame"} {
		spec := map[string]any{"$defs": definitions, "$ref": "#/$defs/" + name}
		properties := definitions[name].(map[string]any)["properties"].(map[string]any)
		for _, sentinel := range []float64{0, -1} {
			value := map[string]any{"id": "f_native", "name": "native", "url": "", "line": sentinel, "column": sentinel}
			if name == "flame_Frame" {
				value["native"], value["anonymous"], value["category"] = true, false, "native"
			}
			for key := range value {
				if properties[key] == nil {
					delete(value, key)
				}
			}
			if err := Validate(spec, value); err != nil {
				t.Errorf("%s rejects location sentinel %v: %v", name, sentinel, err)
			}
		}
	}
}

func TestSchemaVocabulariesRejectUnknownTerms(t *testing.T) {
	definitions := Document()["$defs"].(map[string]any)
	for _, test := range []struct{ definition, field, valid, invalid string }{
		{"schema_Error", "code", "source_map_error", "command_error"},
		{"schema_Error", "code", "ambiguous_selector", "focus_error"},
		{"flame_Frame", "category", "unknown-bundled", "app-like"},
		{"rootcause_Owner", "category", "injected", "application"},
		{"insights_WireFinding", "confidence", "medium", "single-run"},
		{"insights_WireFinding", "evidenceBasis", "sampled", "proven"},
		{"audit_Priority", "evidenceBasis", "hypothesis", "measured-cause"},
		{"compare_MetricDiff", "confidence", "single-run", "medium"},
		{"insights_WireWindow", "kind", "input-stall", "busy-frame"},
		{"insights_WirePeriodic", "kind", "background", "timer"},
		{"flame_DiffNormalization", "requestedMode", "auto", "normalized"},
		{"flame_DiffNormalization", "mode", "per-second", "auto"},
		{"flame_DiffNormalization", "unit", "percentage-points", "%"},
		{"compare_Warning", "code", "window-empty", "window_error"},
		{"flame_DiffWarning", "code", "normalization-unavailable", "usage_error"},
	} {
		t.Run(test.definition+"."+test.field+"."+test.valid, func(t *testing.T) {
			properties := definitions[test.definition].(map[string]any)["properties"].(map[string]any)
			property := properties[test.field].(map[string]any)
			if err := Validate(property, test.valid); err != nil {
				t.Fatalf("documented term rejected: %v", err)
			}
			if err := Validate(property, test.invalid); err == nil {
				t.Fatalf("unsupported term %q accepted", test.invalid)
			}
		})
	}
}

func TestSchemaSeparatesSourceMapPathsFromCoverage(t *testing.T) {
	definitions := Document()["$defs"].(map[string]any)
	for _, name := range []string{"schema_Analysis", "insights_Report", "insights_WireRootReport"} {
		properties := definitions[name].(map[string]any)["properties"].(map[string]any)
		if properties["sourcemaps"] != nil {
			t.Errorf("%s still publishes ambiguous legacy sourcemaps field", name)
		}
		path := properties["sourceMapPaths"].(map[string]any)
		if description, _ := path["description"].(string); !strings.Contains(description, "not resolution diagnostics") {
			t.Errorf("%s does not distinguish configured paths from resolution diagnostics", name)
		}
	}
	for _, name := range []string{"query_Result", "query_Row", "query_Function"} {
		if definitions[name] != nil {
			t.Errorf("schema leaks legacy query domain definition %s", name)
		}
	}
}

func TestSchemaValidatesEveryActualQueryWireOperation(t *testing.T) {
	rec := testgen.FlameTiny()
	engine := &query.Engine{Loader: func(context.Context, string, query.SourceOptions) (*model.Recording, sourcemap.Diagnostics, error) {
		return rec, sourcemap.Diagnostics{UnmappedFrames: 7, Minified: true, MissingMaps: []string{"bundle.js"}, Warnings: []string{"Source map unavailable."}}, nil
	}}
	schema := Document()
	for _, operation := range query.Operations {
		t.Run(operation, func(t *testing.T) {
			request := query.Request{File: "before.json", Operation: operation}
			switch operation {
			case "find":
				request.Selector = "/A/"
			case "callers", "callees", "paths", "subtree":
				request.Selector = "A"
			case "diff":
				request.Candidate = "after.json"
			case "window":
				request.QueryOperation, request.From, request.To = "top", 1, 8
			case "metrics":
				request.Selector = "overview.duration_s"
			case "rootcause":
				request.Selector = "samples.count"
			}
			result, err := engine.Run(context.Background(), request)
			if err != nil {
				t.Fatal(err)
			}
			data, err := json.Marshal(result)
			if err != nil {
				t.Fatal(err)
			}
			var value map[string]any
			if err := json.Unmarshal(data, &value); err != nil {
				t.Fatal(err)
			}
			if err := Validate(schema, value); err != nil {
				t.Fatalf("actual %s wire output: %v", operation, err)
			}
			if operation == "metrics" {
				row := value["rows"].([]any)[0].(map[string]any)
				row["self_ms"] = float64(0)
				if err := Validate(schema, value); err == nil {
					t.Fatal("scalar row accepts obsolete union fields")
				}
				delete(row, "self_ms")
				value["by"] = "self"
				if err := Validate(schema, value); err == nil {
					t.Fatal("scalar operation accepts irrelevant by ranking")
				}
			}
		})
	}
}

func TestCanonicalFrameMetadataMatchesAnalysisFlameAndQuery(t *testing.T) {
	rec := testgen.FlameTiny()
	report := insights.ToWire(insights.Analyze(rec, "fixture.json", insights.Options{}))
	export := flamereport.NewDocument([]flamereport.Chart{{Label: "fixture", Tree: flame.Build(rec)}}, flamereport.Options{})
	engine := &query.Engine{Loader: func(context.Context, string, query.SourceOptions) (*model.Recording, sourcemap.Diagnostics, error) {
		return rec, sourcemap.Diagnostics{}, nil
	}}
	result, err := engine.Run(context.Background(), query.Request{File: "fixture.json", Operation: "top", Limit: query.MaxLimit})
	if err != nil {
		t.Fatal(err)
	}
	data, err := json.Marshal(result)
	if err != nil {
		t.Fatal(err)
	}
	var queryWire struct {
		Functions map[string]json.RawMessage `json:"functions"`
	}
	if err := json.Unmarshal(data, &queryWire); err != nil {
		t.Fatal(err)
	}
	if len(report.Frames) != len(export.Frames) || len(report.Frames) != len(queryWire.Functions) {
		t.Fatal("canonical catalogs disagree on generated function count")
	}
	flameFrames := make(map[string]flame.Frame, len(export.Frames))
	for _, frame := range export.Frames {
		flameFrames[frame.ID] = frame
	}
	for _, frame := range report.Frames {
		want, err := json.Marshal(frame)
		if err != nil {
			t.Fatal(err)
		}
		got, err := json.Marshal(flameFrames[frame.ID])
		if err != nil {
			t.Fatal(err)
		}
		if string(want) != string(got) || string(want) != string(queryWire.Functions[frame.ID]) {
			t.Errorf("canonical generated frame metadata differs for %s", frame.ID)
		}
	}
}

func TestSchemaRejectsObsoleteNestedAnalysisVersion(t *testing.T) {
	report := insights.Analyze(testgen.FlameTiny(), "fixture.json", insights.Options{})
	data, err := json.Marshal(InsightsDocument{Schema: InsightsVersion, Reports: []Analysis{{Report: report}}})
	if err != nil {
		t.Fatal(err)
	}
	var value map[string]any
	if err := json.Unmarshal(data, &value); err != nil {
		t.Fatal(err)
	}
	value["reports"].([]any)[0].(map[string]any)["schemaVersion"] = "wkperf-insights/v2"
	if err := Validate(Document(), value); err == nil {
		t.Fatal("interned analysis accepts obsolete nested evidence discriminator")
	}
}
