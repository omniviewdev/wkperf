package mcp

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/omniviewdev/wkperf/internal/model"
	"github.com/omniviewdev/wkperf/internal/query"
)

func TestUntimedMCPWindowProvenance(t *testing.T) {
	rec := &model.Recording{StartTime: 10, EndTime: 12, Samples: []model.TargetSamples{{StackTraces: []model.StackTrace{
		{StackFrames: []model.StackFrame{{Name: "untimed"}}},
		{Timestamp: 11, StackFrames: []model.StackFrame{{Name: "timed"}}},
	}, Durations: []float64{.025, .004}}}}
	engine := &query.Engine{Loader: func(context.Context, string, query.SourceOptions) (*model.Recording, error) { return rec, nil }}
	input := strings.Join([]string{
		`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18"}}`,
		`{"jsonrpc":"2.0","method":"notifications/initialized"}`,
		`{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"window","arguments":{"file":"fixture.json","from":0.1,"to":0.4}}}`,
	}, "\n") + "\n"
	var out bytes.Buffer
	if err := (&Server{Engine: engine}).Serve(context.Background(), strings.NewReader(input), &out); err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(out.String()), "\n")
	var response struct {
		Result struct {
			Structured map[string]any `json:"structuredContent"`
		} `json:"result"`
	}
	if err := json.Unmarshal([]byte(lines[len(lines)-1]), &response); err != nil {
		t.Fatal(err)
	}
	result := response.Result.Structured
	if result["total_ms"] != float64(0) {
		t.Fatalf("window received invented untimed weight: %v", result)
	}
	source := result["sources"].([]any)[0].(map[string]any)
	if source["untimed_samples"] != float64(1) || source["untimed_ms"] != float64(25) || source["untimed_excluded"] != true {
		t.Fatalf("MCP lost untimed provenance: %v", source)
	}
	spans, ok := source["untimed_spans"].([]any)
	if !ok || len(spans) != 1 || spans[0].(map[string]any)["to_sec"] != float64(1) {
		t.Fatalf("MCP lost inferred recording span: %v", source)
	}
}
