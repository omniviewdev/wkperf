package mcp

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/omniviewdev/wkperf/internal/flame"
	"github.com/omniviewdev/wkperf/internal/model"
	"github.com/omniviewdev/wkperf/internal/query"
)

func TestMCPOwnershipDefaultsOverridesAndValidation(t *testing.T) {
	rec := &model.Recording{StartTime: 0, EndTime: 1, Samples: []model.TargetSamples{{StackTraces: []model.StackTrace{{Timestamp: .1, StackFrames: []model.StackFrame{{Name: "utility", URL: "bundle.js", Line: 1}}}}, Durations: []float64{.02}}}}
	loads := 0
	engine := &query.Engine{Loader: func(context.Context, string, query.SourceOptions) (*model.Recording, error) { loads++; return rec, nil }}
	input := strings.Join([]string{
		`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18"}}`,
		`{"jsonrpc":"2.0","method":"notifications/initialized"}`,
		`{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"top","arguments":{"file":"capture.json"}}}`,
		`{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"top","arguments":{"file":"capture.json","app_url":[],"library_url":["bundle.js"]}}}`,
		`{"jsonrpc":"2.0","id":4,"method":"tools/call","params":{"name":"top","arguments":{"file":"capture.json","app_url":["["]}}}`,
		`{"jsonrpc":"2.0","id":5,"method":"tools/call","params":{"name":"hot-path","arguments":{"file":"capture.json","min_samples":0,"tie_ratio":0}}}`,
	}, "\n") + "\n"
	var out bytes.Buffer
	server := &Server{Engine: engine, Sources: query.SourceOptions{Ownership: flame.OwnershipRules{AppURLs: []string{"bundle.js"}}}}
	if err := server.Serve(context.Background(), strings.NewReader(input), &out); err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(out.String()), "\n")
	if len(lines) != 5 || loads != 3 {
		t.Fatalf("invalid ownership reached loader or call lost: loads=%d %s", loads, out.String())
	}
	for i, want := range []string{"app", "framework"} {
		var response struct {
			Result callResult `json:"result"`
		}
		if err := json.Unmarshal([]byte(lines[i+1]), &response); err != nil {
			t.Fatal(err)
		}
		if response.Result.IsError || response.Result.StructuredContent == nil || len(response.Result.StructuredContent.Rows) != 1 || response.Result.StructuredContent.Rows[0].Category != want {
			t.Fatalf("MCP owner lost: %s", lines[i+1])
		}
		if !strings.Contains(response.Result.Content[0].Text, "--"+map[string]string{"app": "app-url", "framework": "library-url"}[want]) {
			t.Fatal("MCP reproduction loses rules")
		}
	}
	var failed, raw struct {
		Result callResult `json:"result"`
	}
	if err := json.Unmarshal([]byte(lines[3]), &failed); err != nil {
		t.Fatal(err)
	}
	if !failed.Result.IsError || !strings.Contains(failed.Result.Content[0].Text, "pattern") {
		t.Fatalf("invalid pattern not explicit tool failure: %s", lines[3])
	}
	if err := json.Unmarshal([]byte(lines[4]), &raw); err != nil {
		t.Fatal(err)
	}
	if raw.Result.IsError || len(raw.Result.StructuredContent.Rows) != 1 {
		t.Fatalf("MCP explicit zero support lost: %s", lines[4])
	}
}
