package schema

import (
	"bytes"
	"context"
	"encoding/json"
	"testing"

	"github.com/omniviewdev/wkperf/internal/flame"
	"github.com/omniviewdev/wkperf/internal/insights"
	"github.com/omniviewdev/wkperf/internal/model"
	"github.com/omniviewdev/wkperf/internal/query"
	flamereport "github.com/omniviewdev/wkperf/internal/report/flame"
	"github.com/omniviewdev/wkperf/internal/rootcause"
	"github.com/omniviewdev/wkperf/internal/testgen"
)

func TestUntimedSchemaFieldsAndNullTimes(t *testing.T) {
	doc := Document()
	defs := doc["$defs"].(map[string]any)
	for name, fields := range map[string][]string{
		"insights_SampleCoverage": {"timedSampleCount", "untimedSampleCount", "untimedMs", "untimedExcluded", "untimedSpans"},
		"query_Source":            {"untimed_samples", "untimed_ms", "untimed_excluded", "untimed_spans"},
		"report_flame_Chart":      {"untimedSamples", "untimedMs", "untimedExcluded", "empty", "emptyReason"},
		"rootcause_Stack":         {"firstSec", "lastSec", "untimedSamples"},
	} {
		properties := defs[name].(map[string]any)["properties"].(map[string]any)
		for _, field := range fields {
			if properties[field] == nil {
				t.Errorf("%s missing %s", name, field)
			}
		}
	}
	stackSchema := map[string]any{"$defs": defs, "$ref": "#/$defs/rootcause_Stack"}
	data, err := json.Marshal(rootcause.Stack{UntimedSamples: 1})
	if err != nil {
		t.Fatal(err)
	}
	var stack map[string]any
	if err := json.Unmarshal(data, &stack); err != nil {
		t.Fatal(err)
	}
	if stack["firstSec"] != nil || stack["lastSec"] != nil {
		t.Fatalf("untimed-only stack fabricates timestamps: %s", data)
	}
	if err := Validate(stackSchema, stack); err != nil {
		t.Fatalf("nullable stack times do not validate: %v", err)
	}
	stack["firstSec"] = "not a timestamp"
	if err := Validate(stackSchema, stack); err == nil {
		t.Fatal("stack timestamp schema accepts strings")
	}
}

func TestUntimedSchemaValidatesSurfaceOutputs(t *testing.T) {
	rec := testgen.UntimedSamples()
	engine := &query.Engine{Loader: func(context.Context, string, query.SourceOptions) (*model.Recording, error) { return rec, nil }}
	result, err := engine.Run(context.Background(), query.Request{File: "fixture.json", Operation: "top", From: .1, To: .4})
	if err != nil {
		t.Fatal(err)
	}
	report := insights.Analyze(rec, "fixture.json", insights.Options{})
	outputs := []any{InsightsDocument{Schema: InsightsVersion, Reports: []Analysis{{Report: report}}}, result}
	var chart bytes.Buffer
	if err := flamereport.Render(&chart, "json", []flamereport.Chart{{Label: "window", Tree: flame.Build(rec).Window(.1, .4)}}, flamereport.Options{}); err != nil {
		t.Fatal(err)
	}
	outputs = append(outputs, json.RawMessage(chart.Bytes()))
	for _, output := range outputs {
		data, err := json.Marshal(output)
		if err != nil {
			t.Fatal(err)
		}
		var value any
		if err := json.Unmarshal(data, &value); err != nil {
			t.Fatal(err)
		}
		if err := Validate(Document(), value); err != nil {
			t.Fatalf("%T: %v", output, err)
		}
	}
}
