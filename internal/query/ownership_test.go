package query

import (
	"context"
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/omniviewdev/wkperf/internal/model"
	"github.com/omniviewdev/wkperf/internal/sourcemap"
)

func TestOwnershipRulesSurviveQueryOperations(t *testing.T) {
	rec := fixture()
	rec.Records = []model.Record{{Type: model.TypeScript, EventType: "event-dispatched", Details: json.RawMessage(`"click"`), StartTime: 10, EndTime: 11.5}}
	for _, op := range []string{"top", "find", "callers", "callees", "paths", "stacks", "subtree", "hot-path", "by-root", "depth", "events", "diff"} {
		t.Run(op, func(t *testing.T) {
			var req Request
			if err := json.Unmarshal([]byte(`{"file":"sample.json","app_url":["app.js"],"library_url":["vendor/**"]}`), &req); err != nil {
				t.Fatal(err)
			}
			req.Operation = op
			if op == "find" || op == "callers" || op == "callees" || op == "paths" || op == "subtree" {
				req.Selector = "render"
			}
			if op == "diff" {
				req.Baseline, req.Candidate = "sample.json", "after.json"
			}
			result := run(t, engineFor(rec), req)
			check := func(f Function) {
				if f.URL != "" && f.Category != "app" {
					t.Errorf("configured owner lost: %+v", f)
				}
			}
			for _, row := range result.Rows {
				check(row.Function)
				for _, frame := range row.Stack {
					check(frame)
				}
				for _, cost := range append(row.TopSelf, row.TopInclusive...) {
					check(cost.Function)
				}
			}
			command := strings.Join(result.Command, " ")
			if !strings.Contains(command, "--app-url app.js") || !strings.Contains(command, "--library-url vendor/**") {
				t.Fatalf("ownership absent from reproduction: %s", command)
			}
		})
	}
}

func TestOwnershipCategoriesAndPatternValidation(t *testing.T) {
	for _, category := range []string{"unknown-bundled", "injected"} {
		if _, err := engineFor(fixture()).Run(context.Background(), Request{File: "sample.json", Operation: "top", Category: category}); err != nil {
			t.Fatalf("category %s: %v", category, err)
		}
	}
	var req Request
	if err := json.Unmarshal([]byte(`{"file":"sample.json","operation":"top","app_url":["["]}`), &req); err != nil {
		t.Fatal(err)
	}
	loaded := false
	engine := &Engine{Loader: func(context.Context, string, SourceOptions) (*model.Recording, sourcemap.Diagnostics, error) {
		loaded = true
		return fixture(), sourcemap.Diagnostics{}, nil
	}}
	if _, err := engine.Run(context.Background(), req); err == nil || loaded {
		t.Fatalf("invalid rules must fail before loading: loaded=%t err=%v", loaded, err)
	}
	properties := RequestSchema("top")["properties"].(map[string]any)
	for _, key := range []string{"app_url", "library_url"} {
		if _, ok := properties[key]; !ok {
			t.Errorf("missing MCP argument %s", key)
		}
	}
	if !reflect.DeepEqual(properties["category"].(map[string]any)["enum"], []string{"", "app", "framework", "native", "gc", "unknown-bundled", "injected"}) {
		t.Fatal("MCP categories omit neutral ownership")
	}
}
