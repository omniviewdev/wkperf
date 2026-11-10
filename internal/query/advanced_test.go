package query

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/omniviewdev/wkperf/internal/flame"
	"github.com/omniviewdev/wkperf/internal/model"
	"github.com/omniviewdev/wkperf/internal/sourcemap"
)

func TestOriginalSelectorsAndContextIdentity(t *testing.T) {
	rec := fixture()
	for i := range rec.Samples[0].StackTraces {
		for j := range rec.Samples[0].StackTraces[i].StackFrames {
			frame := &rec.Samples[0].StackTraces[i].StackFrames[j]
			if frame.Name == "work" {
				frame.Original = &model.SourceLocation{Name: "doWork", URL: "/src/work.ts", Line: 80, Column: 4}
			}
		}
	}
	engine := engineFor(rec)
	for _, selector := range []string{"doWork", "/src/work.ts:80:4"} {
		result := run(t, engine, Request{Operation: "top", Selector: selector})
		if len(result.Rows) != 1 || result.Rows[0].Name != "work" || result.Rows[0].SelfMS != 50 || result.Rows[0].Original.Name != "doWork" {
			t.Fatalf("mapping changed identity: %+v", result)
		}
		for _, format := range []string{"md", "tsv"} {
			var out strings.Builder
			if err := Render(&out, result, format); err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(out.String(), "src/work.ts:80:4") || !strings.Contains(out.String(), "app.js:20:1") {
				t.Fatalf("lost source: %s", out.String())
			}
		}
	}
	result := run(t, engine, Request{Operation: "subtree", Selector: "work"})
	if len(result.Rows) != 2 || result.Rows[0].NodeID == result.Rows[1].NodeID {
		t.Fatalf("contexts collapsed: %+v", result.Rows)
	}
	for _, row := range result.Rows {
		if len(row.Stack) < 2 || row.Stack[len(row.Stack)-1].ID != row.ID {
			t.Fatalf("subtree lost context: %+v", row)
		}
	}
	for _, row := range result.Rows {
		exact := run(t, engine, Request{Operation: "subtree", Selector: row.NodeID})
		if len(exact.Rows) != 1 || exact.Rows[0].NodeID != row.NodeID {
			t.Fatalf("node selector guessed: %+v", exact)
		}
	}
	tree := flame.Build(rec)
	if _, err := ResolveNodes(tree, "absent"); !errors.Is(err, ErrNoMatches) {
		t.Fatalf("missing sentinel: %v", err)
	}
	if _, err := ResolveNodes(tree, "f_"+strings.Repeat("0", 64)); !errors.Is(err, ErrNoMatches) {
		t.Fatalf("missing ID sentinel: %v", err)
	}
	if _, err := ResolveNodes(tree, "f_broken"); err == nil || errors.Is(err, ErrNoMatches) {
		t.Fatalf("malformed ID accepted: %v", err)
	}
}

func TestExactStacksMetricsEventsAndRootcause(t *testing.T) {
	rec := fixture()
	rec.Records = []model.Record{{Type: model.TypeScript, EventType: "timer-fired", StartTime: 10.05, EndTime: 10.35, Details: json.RawMessage(`"poll"`)}}
	engine := engineFor(rec)
	stacks := run(t, engine, Request{Operation: "stacks"})
	for _, row := range stacks.Rows {
		exact := run(t, engine, Request{Operation: "stacks", Selector: row.NodeID})
		if len(exact.Rows) != 1 || !reflect.DeepEqual(exact.Rows[0], row) {
			t.Fatalf("not exact stack: %+v", exact)
		}
	}
	metrics := run(t, engine, Request{Operation: "metrics", Selector: "samples.count"})
	if len(metrics.Rows) != 1 || metrics.Rows[0].Value != 4 {
		t.Fatalf("derived metric missing: %+v", metrics)
	}
	events := run(t, engine, Request{Operation: "events", Selector: "poll", Limit: 1})
	if len(events.Rows) != 1 || events.Rows[0].Count != 1 || events.Rows[0].TotalMS != 300 || events.Rows[0].Kind != "timer" || len(events.Rows[0].TopSelf) != 1 || !events.Rows[0].FunctionsTruncated {
		t.Fatalf("event mismatch: %+v", events)
	}
	for _, format := range []string{"md", "tsv"} {
		var out strings.Builder
		if err := Render(&out, events, format); err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(out.String(), "TRUNCATED") {
			t.Fatalf("missing nested truncation marker: %s", out.String())
		}
	}
	all := run(t, engine, Request{Operation: "rootcause", Limit: MaxLimit})
	if len(all.Rows) == 0 {
		t.Fatal("no rootcause measurements")
	}
	exact := run(t, engine, Request{Operation: "rootcause", Selector: all.Rows[0].Key})
	if len(exact.Rows) != 1 || exact.Rows[0].Key != all.Rows[0].Key {
		t.Fatalf("rootcause key missing: %+v", exact)
	}
}

func TestWindowOperationAndDefaultBounds(t *testing.T) {
	engine := engineFor(fixture())
	window := run(t, engine, Request{Operation: "window", QueryOperation: "top", From: .15, To: .5})
	plain := run(t, engine, Request{Operation: "top", From: .15, To: .5})
	if !reflect.DeepEqual(window, plain) {
		t.Fatalf("window differs: %+v / %+v", window, plain)
	}
	if window.Limit != DefaultLimit {
		t.Fatalf("unbounded default: %+v", window)
	}
	if _, err := engine.Run(context.Background(), Request{File: "sample.json", Operation: "window", From: 0, To: 0}); err == nil {
		t.Fatal("empty explicit window accepted")
	}
	if _, err := engine.Run(context.Background(), Request{File: "sample.json", Operation: "stacks", Selector: "missing"}); err == nil {
		t.Fatal("missing exact stack accepted")
	}
}

func TestSchemasConcreteAndComplete(t *testing.T) {
	schema := ResultSchema()
	properties := schema["properties"].(map[string]any)
	for _, key := range []string{"schema", "sources", "command", "sampled_total_ms", "total_count", "returned_count", "offset", "truncated", "truncated_by", "functions", "candidates", "deepest_stack", "errors"} {
		if properties[key] == nil {
			t.Fatalf("missing result schema %s", key)
		}
	}
	rowsByOperation := map[string]map[string]any{}
	for _, item := range schema["oneOf"].([]any) {
		branch := item.(map[string]any)["properties"].(map[string]any)
		operation, ok := branch["operation"].(map[string]any)
		if !ok {
			continue
		}
		rowsByOperation[operation["const"].(string)] = branch["rows"].(map[string]any)["items"].(map[string]any)["properties"].(map[string]any)
	}
	for operation, fields := range map[string][]string{
		"top":     {"function_id", "self_ms", "total_ms", "samples", "percent"},
		"stacks":  {"function_id", "node_id", "depth", "stack", "stack_depth"},
		"metrics": {"key", "value", "unit"},
		"keys":    {"key", "op", "unit", "description", "basis"},
		"events":  {"kind", "count", "total_ms", "top_self", "top_inclusive"},
		"diff":    {"function_id", "baseline_self_ms", "candidate_self_ms", "delta_self_ms", "delta_samples"},
	} {
		for _, field := range fields {
			if rowsByOperation[operation][field] == nil {
				t.Errorf("missing %s row field %s", operation, field)
			}
		}
	}
	if len(rowsByOperation["metrics"]) != 3 {
		t.Fatalf("metric row includes unrelated fields: %v", rowsByOperation["metrics"])
	}
	for _, op := range Operations {
		if op != "window" && rowsByOperation[op] == nil {
			t.Errorf("missing operation-specific row schema for %s", op)
		}
		input := RequestSchema(op)
		if input["additionalProperties"] != false {
			t.Fatalf("loose schema for %s", op)
		}
	}
}

func TestDiffGoldenAndShuffledDeterminism(t *testing.T) {
	build := func(reverse bool) *Engine {
		a, b := fixture(), fixture()
		b.Samples[0].Durations = []float64{.005, .06, .01, .02}
		if reverse {
			for _, rec := range []*model.Recording{a, b} {
				samples := &rec.Samples[0]
				for i, j := 0, len(samples.StackTraces)-1; i < j; i, j = i+1, j-1 {
					samples.StackTraces[i], samples.StackTraces[j] = samples.StackTraces[j], samples.StackTraces[i]
					samples.Durations[i], samples.Durations[j] = samples.Durations[j], samples.Durations[i]
				}
			}
		}
		return &Engine{Loader: func(_ context.Context, file string, _ SourceOptions) (*model.Recording, sourcemap.Diagnostics, error) {
			if file == "a.json" {
				return a, sourcemap.Diagnostics{}, nil
			}
			return b, sourcemap.Diagnostics{}, nil
		}}
	}
	req := Request{Operation: "diff", Baseline: "a.json", Candidate: "b.json", By: "total"}
	result := run(t, build(false), req)
	type delta struct {
		Name  string  `json:"name"`
		Self  float64 `json:"self"`
		Total float64 `json:"total"`
	}
	rows := make([]delta, len(result.Rows))
	for i, row := range result.Rows {
		rows[i] = delta{row.Name, row.DeltaSelfMS, row.DeltaTotalMS}
	}
	got, _ := json.Marshal(rows)
	const golden = `[{"name":"leaf","self":-35,"total":-35},{"name":"work","self":30,"total":30},{"name":"event","self":0,"total":-5},{"name":"render","self":0,"total":-5}]`
	if string(got) != golden {
		t.Fatalf("diff: %s; want %s", got, golden)
	}
	first, _ := json.Marshal(result)
	shuffled, _ := json.Marshal(run(t, build(true), req))
	if string(first) != string(shuffled) {
		t.Fatalf("shuffled diff changed: %s / %s", first, shuffled)
	}
}

func TestAnonymousNativeAmbiguity(t *testing.T) {
	rec := &model.Recording{StartTime: 0, EndTime: 1, Samples: []model.TargetSamples{{StackTraces: []model.StackTrace{{Timestamp: .1, StackFrames: []model.StackFrame{{SourceID: "alpha"}}}, {Timestamp: .2, StackFrames: []model.StackFrame{{SourceID: "beta"}}}}, Durations: []float64{.001, .002}}}}
	engine := engineFor(rec)
	result := run(t, engine, Request{Operation: "paths", Selector: "(anonymous)"})
	if result.Status != "ambiguous" || len(result.Candidates) != 2 || result.Candidates[0].ID == result.Candidates[1].ID {
		t.Fatalf("native anonymous functions collapsed: %+v", result)
	}
	for _, candidate := range result.Candidates {
		exact := run(t, engine, Request{Operation: "top", Selector: candidate.ID})
		if len(exact.Rows) != 1 || exact.Rows[0].ID != candidate.ID {
			t.Fatalf("native ID did not resolve: %+v", exact)
		}
	}
}
