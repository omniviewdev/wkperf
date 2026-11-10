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

func TestAmbiguousToolKeepsStructuredCandidates(t *testing.T) {
	rec := &model.Recording{StartTime: 0, EndTime: 1, Samples: []model.TargetSamples{{StackTraces: []model.StackTrace{{Timestamp: .1, StackFrames: []model.StackFrame{{Name: "same", URL: "one.js"}}}, {Timestamp: .2, StackFrames: []model.StackFrame{{Name: "same", URL: "two.js"}}}}, Durations: []float64{.01, .02}}}}
	engine := &query.Engine{Loader: func(context.Context, string, query.SourceOptions) (*model.Recording, error) { return rec, nil }}
	input := `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18"}}` + "\n" + `{"jsonrpc":"2.0","method":"notifications/initialized"}` + "\n" + `{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"paths","arguments":{"file":"sample.json","selector":"same"}}}` + "\n"
	var out bytes.Buffer
	if err := (&Server{Engine: engine}).Serve(context.Background(), strings.NewReader(input), &out); err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(out.String()), "\n")
	var response struct {
		Result struct {
			IsError           bool         `json:"isError"`
			StructuredContent query.Result `json:"structuredContent"`
		} `json:"result"`
	}
	if err := json.Unmarshal([]byte(lines[1]), &response); err != nil {
		t.Fatal(err)
	}
	if !response.Result.IsError || response.Result.StructuredContent.Status != "ambiguous" || len(response.Result.StructuredContent.Candidates) != 2 {
		t.Fatalf("ambiguity guessed or discarded: %s", lines[1])
	}
}

func TestResponseByteBound(t *testing.T) {
	var out bytes.Buffer
	if err := writeResult(&out, json.RawMessage(`1`), callResult{Content: []textContent{{Type: "text", Text: strings.Repeat("\\\"", MaxResponseBytes)}}}); err != nil {
		t.Fatal(err)
	}
	if out.Len() > MaxResponseBytes {
		t.Fatalf("unbounded result: %d", out.Len())
	}
	var response map[string]any
	if err := json.Unmarshal(out.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if response["result"].(map[string]any)["isError"] != true {
		t.Fatalf("missing explicit response-bound error: %s", out.String())
	}
}

func TestProtocolExplicitZeroAndWindow(t *testing.T) {
	rec := &model.Recording{StartTime: 0, EndTime: 1, Samples: []model.TargetSamples{{StackTraces: []model.StackTrace{{Timestamp: .1, StackFrames: []model.StackFrame{{Name: "tiny", URL: "a.js", Line: 2}, {Name: "root", URL: "a.js", Line: 1}}}, {Timestamp: .2, StackFrames: []model.StackFrame{{Name: "root", URL: "a.js", Line: 1}}}}, Durations: []float64{.001, .999}}}}
	engine := &query.Engine{Loader: func(context.Context, string, query.SourceOptions) (*model.Recording, error) { return rec, nil }}
	input := strings.Join([]string{
		`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18"}}`,
		`{"jsonrpc":"2.0","method":"notifications/initialized"}`,
		`{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"hot-path","arguments":{"file":"x","min_share":0}}}`,
		`{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"hot-path","arguments":{"file":"x"}}}`,
		`{"jsonrpc":"2.0","id":4,"method":"tools/call","params":{"name":"window","arguments":{"file":"x","from":0.15,"to":0.5,"query_operation":"top"}}}`,
		`{"jsonrpc":"2.0","id":5,"method":"tools/call","params":{"name":"subtree","arguments":{"file":"x","selector":"root","depth":0}}}`,
	}, "\n") + "\n"
	var out bytes.Buffer
	if err := (&Server{Engine: engine}).Serve(context.Background(), strings.NewReader(input), &out); err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(out.String()), "\n")
	for i, count := range []int{2, 1, 1, 1} {
		var response struct {
			Result struct {
				StructuredContent query.Result `json:"structuredContent"`
				IsError           bool         `json:"isError"`
			} `json:"result"`
		}
		if err := json.Unmarshal([]byte(lines[i+1]), &response); err != nil {
			t.Fatal(err)
		}
		if response.Result.IsError || len(response.Result.StructuredContent.Rows) != count {
			t.Fatalf("bad tool result: %s", lines[i+1])
		}
	}
}
