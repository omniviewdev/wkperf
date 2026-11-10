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

func TestProtocolConversationInMemory(t *testing.T) {
	rec := &model.Recording{StartTime: 1, EndTime: 2, Samples: []model.TargetSamples{{StackTraces: []model.StackTrace{{Timestamp: 1.1, StackFrames: []model.StackFrame{{Name: "work", URL: "app.js", Line: 1}}}}, Durations: []float64{.025}}}}
	engine := &query.Engine{Loader: func(_ context.Context, file string, options query.SourceOptions) (*model.Recording, error) {
		if file != "local input.json" {
			t.Fatalf("file changed: %q", file)
		}
		if len(options.Sourcemaps) != 1 || options.Sourcemaps[0] != "app.js.map" {
			t.Fatalf("sourcemaps lost: %+v", options)
		}
		return rec, nil
	}}
	input := strings.Join([]string{
		`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18","capabilities":{},"clientInfo":{"name":"test","version":"1"}}}`,
		`{"jsonrpc":"2.0","method":"notifications/initialized"}`,
		`{"jsonrpc":"2.0","id":"list","method":"tools/list"}`,
		`{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"top","arguments":{"file":"local input.json","sourcemaps":["app.js.map"],"limit":1}}}`,
		`{"jsonrpc":"2.0","id":4,"method":"ping"}`,
		`{"jsonrpc":"2.0","id":5,"method":"tools/call","params":{"name":"paths","arguments":{"file":"local input.json","sourcemaps":["app.js.map"],"selector":"missing"}}}`,
	}, "\n") + "\n"
	var output bytes.Buffer
	if err := (&Server{Engine: engine}).Serve(context.Background(), strings.NewReader(input), &output); err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(output.String()), "\n")
	if len(lines) != 5 {
		t.Fatalf("notifications wrote protocol output: %s", output.String())
	}
	var initialize, list, call, ping, failed map[string]any
	for i, target := range []*map[string]any{&initialize, &list, &call, &ping, &failed} {
		if err := json.Unmarshal([]byte(lines[i]), target); err != nil {
			t.Fatalf("stdout not JSON-RPC: %s", lines[i])
		}
		if (*target)["jsonrpc"] != "2.0" {
			t.Fatal("missing JSON-RPC version")
		}
	}
	if initialize["result"].(map[string]any)["protocolVersion"] != "2025-06-18" {
		t.Fatalf("bad negotiation: %v", initialize)
	}
	tools := list["result"].(map[string]any)["tools"].([]any)
	if len(tools) != len(query.Operations) {
		t.Fatalf("tool operations differ: %d != %d", len(tools), len(query.Operations))
	}
	for _, value := range tools {
		tool := value.(map[string]any)
		if tool["inputSchema"] == nil || tool["outputSchema"] == nil {
			t.Fatalf("missing schemas: %v", tool)
		}
	}
	result := call["result"].(map[string]any)
	structured := result["structuredContent"].(map[string]any)
	if structured["schema"] != query.Schema || structured["total_count"] != float64(1) || structured["truncated"] != false {
		t.Fatalf("not same bounded schema: %v", result)
	}
	text := result["content"].([]any)[0].(map[string]any)["text"].(string)
	var content map[string]any
	if err := json.Unmarshal([]byte(text), &content); err != nil {
		t.Fatal(err)
	}
	if content["schema"] != structured["schema"] {
		t.Fatal("content differs from structuredContent")
	}
	if _, ok := ping["result"]; !ok {
		t.Fatalf("ping failed: %v", ping)
	}
	if failed["result"].(map[string]any)["isError"] != true {
		t.Fatalf("execution error not tool error: %v", failed)
	}
}

func TestProtocolErrorsAndNotificationSilence(t *testing.T) {
	input := strings.Join([]string{
		`not json`,
		`{"jsonrpc":"2.0","id":7,"method":"tools/list"}`,
		`{"jsonrpc":"2.0","id":8,"method":"initialize","params":{"protocolVersion":"2025-06-18"}}`,
		`{"jsonrpc":"2.0","method":"notifications/initialized"}`,
		`{"jsonrpc":"2.0","id":9,"method":"missing"}`,
		`{"jsonrpc":"2.0","method":"missing"}`,
		`{"jsonrpc":"2.0","id":10,"method":"tools/call","params":{"name":"top","arguments":{"file":"x","limit":1001}}}`,
		`{"jsonrpc":"2.0","id":11,"method":"tools/call","params":{"name":"absent","arguments":{}}}`,
	}, "\n") + "\n"
	var output bytes.Buffer
	if err := (&Server{}).Serve(context.Background(), strings.NewReader(input), &output); err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(output.String()), "\n")
	if len(lines) != 6 {
		t.Fatalf("bad response count: %s", output.String())
	}
	for i, want := range []float64{-32700, -32002, 0, -32601, 0, -32602} {
		var message map[string]any
		if err := json.Unmarshal([]byte(lines[i]), &message); err != nil {
			t.Fatal(err)
		}
		if want != 0 && message["error"].(map[string]any)["code"] != want {
			t.Fatalf("line %d: %v", i, message)
		}
	}
}

func TestProtocolCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := (&Server{}).Serve(ctx, strings.NewReader(""), &bytes.Buffer{}); err != context.Canceled {
		t.Fatalf("lost cancellation: %v", err)
	}
}
