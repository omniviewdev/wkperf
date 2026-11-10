package query

import (
	"context"
	"encoding/json"
	"math/rand/v2"
	"reflect"
	"strings"
	"testing"

	"github.com/omniviewdev/wkperf/internal/model"
)

func fixture() *model.Recording {
	f := func(name string, line int) model.StackFrame {
		return model.StackFrame{Name: name, URL: "app.js", Line: line, Column: 1}
	}
	root, render, work, leaf := f("event", 1), f("render", 10), f("work", 20), f("leaf", 30)
	return &model.Recording{StartTime: 10, EndTime: 12, Samples: []model.TargetSamples{{
		StackTraces: []model.StackTrace{
			{Timestamp: 10.1, StackFrames: []model.StackFrame{leaf, render, root}},
			{Timestamp: 10.2, StackFrames: []model.StackFrame{work, render, root}},
			{Timestamp: 10.3, StackFrames: []model.StackFrame{render, root}},
			{Timestamp: 11.1, StackFrames: []model.StackFrame{work, root}},
		},
		Durations: []float64{.04, .03, .01, .02},
	}}}
}

func engineFor(rec *model.Recording) *Engine {
	return &Engine{Loader: func(_ context.Context, _ string, _ SourceOptions) (*model.Recording, error) { return rec, nil }}
}

func run(t *testing.T, engine *Engine, req Request) *Result {
	t.Helper()
	if req.File == "" {
		req.File = "sample.json"
	}
	result, err := engine.Run(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	return result
}

func TestWeightedQueryGoldens(t *testing.T) {
	engine := engineFor(fixture())
	for _, tt := range []struct{ op, selector, golden string }{
		{"callers", "work", `[{"name":"render","ms":30,"percent":60,"samples":1},{"name":"event","ms":20,"percent":40,"samples":1}]`},
		{"callees", "render", `[{"name":"leaf","ms":40,"percent":50,"samples":1},{"name":"work","ms":30,"percent":37.5,"samples":1}]`},
		{"paths", "work", `[{"stack":["event","render","work"],"ms":30,"samples":1},{"stack":["event","work"],"ms":20,"samples":1}]`},
		{"hot-path", "", `[{"name":"event","ms":100,"percent":100,"samples":4},{"name":"render","ms":80,"percent":80,"samples":3},{"name":"leaf","ms":40,"percent":40,"samples":1}]`},
	} {
		t.Run(tt.op, func(t *testing.T) {
			result := run(t, engine, Request{Operation: tt.op, Selector: tt.selector})
			var rows []map[string]any
			for _, row := range result.Rows {
				if tt.op == "paths" {
					stack := make([]string, len(row.Stack))
					for i, f := range row.Stack {
						stack[i] = f.Name
					}
					rows = append(rows, map[string]any{"stack": stack, "ms": row.TotalMS, "samples": row.Samples})
				} else {
					rows = append(rows, map[string]any{"name": row.Name, "ms": row.TotalMS, "percent": row.Percent, "samples": row.Samples})
				}
			}
			var want any
			if err := json.Unmarshal([]byte(tt.golden), &want); err != nil {
				t.Fatal(err)
			}
			gotJSON, _ := json.Marshal(rows)
			var got any
			_ = json.Unmarshal(gotJSON, &got)
			if !reflect.DeepEqual(got, want) {
				t.Fatalf("got %s; want %s", gotJSON, tt.golden)
			}
		})
	}
}

func TestDiffAbsoluteDeltaAndLimit(t *testing.T) {
	a, b := fixture(), fixture()
	b.Samples[0].Durations = []float64{.005, .06, .01, .02}
	engine := &Engine{Loader: func(_ context.Context, file string, _ SourceOptions) (*model.Recording, error) {
		if file == "a.json" {
			return a, nil
		}
		return b, nil
	}}
	result := run(t, engine, Request{Operation: "diff", Baseline: "a.json", Candidate: "b.json", By: "self", Limit: 1})
	if result.TotalCount != 4 || !result.Truncated || len(result.Rows) != 1 || result.Rows[0].Name != "leaf" || result.Rows[0].DeltaSelfMS != -35 {
		t.Fatalf("unexpected diff: %+v", result)
	}
	if result.Schema != Schema || len(result.Command) == 0 || len(result.Sources) != 2 {
		t.Fatalf("missing provenance: %+v", result)
	}
}

func TestQueryDeterminismShuffledSamples(t *testing.T) {
	base := fixture()
	for _, op := range []string{"top", "callers", "callees", "paths", "subtree", "hot-path", "by-root", "find", "depth"} {
		t.Run(op, func(t *testing.T) {
			req := Request{File: "sample.json", Operation: op}
			if op == "callers" || op == "callees" || op == "paths" || op == "subtree" {
				req.Selector = "render"
			}
			if op == "find" {
				req.Selector = ".*"
			}
			want, _ := json.Marshal(run(t, engineFor(base), req))
			for seed := int64(1); seed <= 10; seed++ {
				rec := fixture()
				samples := &rec.Samples[0]
				perm := rand.New(rand.NewPCG(uint64(seed), 0)).Perm(len(samples.StackTraces))
				stacks := append([]model.StackTrace(nil), samples.StackTraces...)
				weights := append([]float64(nil), samples.Durations...)
				for i, j := range perm {
					samples.StackTraces[i], samples.Durations[i] = stacks[j], weights[j]
				}
				got, _ := json.Marshal(run(t, engineFor(rec), req))
				if string(got) != string(want) {
					t.Fatalf("seed %d differs\n%s\n%s", seed, got, want)
				}
			}
		})
	}
}

func TestSelectorsAmbiguityNativeAnonymousAndMalformed(t *testing.T) {
	rec := fixture()
	rec.Samples[0].StackTraces = append(rec.Samples[0].StackTraces,
		model.StackTrace{Timestamp: 10.5, StackFrames: []model.StackFrame{{Name: "work", URL: "other.js", Line: 9}}},
		model.StackTrace{Timestamp: 10.6, StackFrames: []model.StackFrame{{Name: "nativeCall"}}},
		model.StackTrace{Timestamp: 10.7, StackFrames: []model.StackFrame{{URL: "anonymous.js", Line: 7, Column: 2}}})
	rec.Samples[0].Durations = append(rec.Samples[0].Durations, .001, .002, .003)
	engine := engineFor(rec)
	ambiguous := run(t, engine, Request{Operation: "callers", Selector: "work"})
	if ambiguous.Status != "ambiguous" || len(ambiguous.Candidates) != 2 || ambiguous.Candidates[0].ID == "" {
		t.Fatalf("not explicit ambiguity: %+v", ambiguous)
	}
	for _, selector := range []string{"app.js:20:1", "nativeCall", "anonymous.js:7:2", ambiguous.Candidates[0].ID, "/^work$/"} {
		result := run(t, engine, Request{Operation: "paths", Selector: selector})
		if result.Status != "ok" || len(result.Rows) == 0 {
			t.Fatalf("selector %q: %+v", selector, result)
		}
	}
	for _, selector := range []string{"", "/[/", "/unterminated", "app.js:wat:1", "fn:not-an-id", "node:not-an-id"} {
		if _, err := engine.Run(context.Background(), Request{File: "sample.json", Operation: "paths", Selector: selector}); err == nil {
			t.Errorf("accepted malformed selector %q", selector)
		}
	}
}

func TestWindowDepthSubtreeFiltersAndRendering(t *testing.T) {
	engine := engineFor(fixture())
	result := run(t, engine, Request{Operation: "top", From: .15, To: .5, By: "self"})
	if result.Rows[0].Name != "work" || result.Rows[0].SelfMS != 30 {
		t.Fatalf("window mismatch: %+v", result.Rows)
	}
	depth := run(t, engine, Request{Operation: "depth"})
	if len(depth.DeepestStack) != 3 || len(depth.Rows) != 2 || depth.Rows[0].Depth != 2 || depth.Rows[0].TotalMS != 30 {
		t.Fatalf("depth mismatch: %+v", depth)
	}
	subtree := run(t, engine, Request{Operation: "subtree", Selector: "render", Depth: 1, MinMS: 35})
	if len(subtree.Rows) != 2 || subtree.Rows[1].Name != "leaf" {
		t.Fatalf("subtree mismatch: %+v", subtree.Rows)
	}
	bounded := run(t, engine, Request{Operation: "top", Limit: 1})
	for _, format := range []string{"md", "tsv"} {
		var out strings.Builder
		if err := Render(&out, bounded, format); err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(out.String(), "TRUNCATED") || !strings.Contains(out.String(), "sample.json") {
			t.Fatalf("missing bounds/provenance: %s", out.String())
		}
	}
	for _, req := range []Request{{Operation: "top", Limit: -1}, {Operation: "hot-path", MinShare: 2}, {Operation: "subtree", Selector: "render", Depth: -1}, {Operation: "top", From: 1, To: .5}} {
		if _, err := engine.Run(context.Background(), req); err == nil {
			t.Fatalf("accepted invalid request: %+v", req)
		}
	}
}
