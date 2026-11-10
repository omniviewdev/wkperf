package mcp

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/omniviewdev/wkperf/internal/model"
	"github.com/omniviewdev/wkperf/internal/query"
	"github.com/omniviewdev/wkperf/internal/sourcemap"
	"github.com/omniviewdev/wkperf/internal/testgen"
)

func TestMCPThresholdSettingsReachSharedQuery(t *testing.T) {
	rec := testgen.ThresholdFrames()
	engine := &query.Engine{Loader: func(context.Context, string, query.SourceOptions) (*model.Recording, sourcemap.Diagnostics, error) {
		return rec, sourcemap.Diagnostics{}, nil
	}}
	input := strings.Join([]string{
		`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18"}}`,
		`{"jsonrpc":"2.0","method":"notifications/initialized"}`,
		`{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"metrics","arguments":{"file":"capture.json","selector":"findings.jank.ratio","jank_ms":325,"long_task_ms":100}}}`,
	}, "\n") + "\n"
	var output bytes.Buffer
	if err := (&Server{Engine: engine}).Serve(context.Background(), strings.NewReader(input), &output); err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(output.String()), "\n")
	if len(lines) != 2 {
		t.Fatalf("unexpected protocol output: %s", output.String())
	}
	var response struct {
		Result wireCall[query.ValueRow] `json:"result"`
	}
	if err := json.Unmarshal([]byte(lines[1]), &response); err != nil || response.Result.IsError || response.Result.StructuredContent == nil {
		t.Fatalf("threshold tool failed: %v %s", err, lines[1])
	}
	result := response.Result.StructuredContent
	command := strings.Join(result.Command, " ")
	if len(result.Rows) != 1 || result.Rows[0].Value != .5 || !strings.Contains(command, "--jank-ms 325") || !strings.Contains(command, "--long-task-ms 100") {
		t.Fatalf("MCP did not share threshold query: %+v", result)
	}
	var text wireQuery[query.ValueRow]
	if len(response.Result.Content) != 1 || json.Unmarshal([]byte(response.Result.Content[0].Text), &text) != nil || len(text.Rows) != 1 || text.Rows[0].Value != .5 {
		t.Fatal("MCP text and structured threshold evidence differ")
	}
}
