package mcp

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/omniviewdev/wkperf/internal/model"
	"github.com/omniviewdev/wkperf/internal/query"
	"github.com/omniviewdev/wkperf/internal/sourcemap"
)

type wireQuery[Row any] struct {
	query.Document
	Rows         []Row             `json:"rows"`
	DeepestStack []json.RawMessage `json:"deepest_stack"`
}

type wireCall[Row any] struct {
	Content           []textContent   `json:"content"`
	StructuredContent *wireQuery[Row] `json:"structuredContent"`
	IsError           bool            `json:"isError"`
}

func deepMCPRecording(samples, depth int, escaped bool) *model.Recording {
	rec := &model.Recording{StartTime: 0, EndTime: 10}
	target := model.TargetSamples{}
	prefix := "work"
	if escaped {
		prefix = strings.Repeat("\"\\", 2048)
	}
	for sample := range samples {
		frames := make([]model.StackFrame, depth)
		for frame := range frames {
			name := fmt.Sprintf("%s%03d", prefix, frame)
			if frame == 0 {
				name = fmt.Sprintf("leaf%03d", sample)
			}
			frames[frame] = model.StackFrame{Name: name, URL: "app.js", Line: frame + 1, Column: 1}
		}
		target.StackTraces = append(target.StackTraces, model.StackTrace{Timestamp: .1 + float64(sample)*.01, StackFrames: frames})
		target.Durations = append(target.Durations, .001)
	}
	rec.Samples = []model.TargetSamples{target}
	return rec
}

func mcpQueryResponse(t *testing.T, engine *query.Engine, operation, arguments string) []byte {
	t.Helper()
	input := strings.Join([]string{
		`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18"}}`,
		`{"jsonrpc":"2.0","method":"notifications/initialized"}`,
		`{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"` + operation + `","arguments":` + arguments + `}}`,
	}, "\n") + "\n"
	var out bytes.Buffer
	if err := (&Server{Engine: engine}).Serve(context.Background(), strings.NewReader(input), &out); err != nil {
		t.Fatal(err)
	}
	lines := bytes.Split(bytes.TrimSpace(out.Bytes()), []byte{'\n'})
	if len(lines) != 2 {
		t.Fatalf("want initialize and one query reply: %.500s", out.Bytes())
	}
	return lines[1]
}

func TestMCPDeepDefaultReturnsBoundedEvidence(t *testing.T) {
	rec := deepMCPRecording(120, 400, false)
	engine := &query.Engine{Loader: func(context.Context, string, query.SourceOptions) (*model.Recording, sourcemap.Diagnostics, error) {
		return rec, sourcemap.Diagnostics{}, nil
	}}
	data := mcpQueryResponse(t, engine, "stacks", `{"file":"deep.json"}`)
	var response struct {
		Result wireCall[json.RawMessage] `json:"result"`
	}
	if err := json.Unmarshal(data, &response); err != nil {
		t.Fatal(err)
	}
	result := response.Result.StructuredContent
	if response.Result.IsError || result == nil || len(result.Rows) == 0 || !result.Truncated || !containsReason(result.TruncatedBy, "bytes") {
		t.Fatalf("default deep query discarded useful evidence: %.1000s", data)
	}
	if len(data)+1 > 2*query.DefaultMaxBytes+1024 || len(data)+1 > MaxResponseBytes {
		t.Fatalf("escaped duplicated envelope exceeded query budget: %d", len(data)+1)
	}
	var structured json.RawMessage
	var textObject struct {
		Result struct {
			Structured json.RawMessage `json:"structuredContent"`
		} `json:"result"`
	}
	if err := json.Unmarshal(data, &textObject); err != nil {
		t.Fatal(err)
	}
	structured = textObject.Result.Structured
	var compact bytes.Buffer
	if err := json.Compact(&compact, structured); err != nil {
		t.Fatal(err)
	}
	if len(response.Result.Content) != 1 || response.Result.Content[0].Text != compact.String() {
		t.Fatal("MCP text must retain same bounded JSON as structured content")
	}
	data = mcpQueryResponse(t, engine, "stacks", `{"file":"deep.json","limit":1,"max_stack_frames":0,"max_bytes":4194304}`)
	if err := json.Unmarshal(data, &response); err != nil {
		t.Fatal(err)
	}
	var path struct {
		Stack      []json.RawMessage `json:"stack"`
		StackDepth int               `json:"stack_depth"`
	}
	if err := json.Unmarshal(response.Result.StructuredContent.Rows[0], &path); err != nil {
		t.Fatal(err)
	}
	if response.Result.IsError || path.StackDepth != 400 || len(path.Stack) != 400 {
		t.Fatalf("explicit exact stack lost frames: %.1000s", data)
	}
}

func TestMCPEscapedEnvelopeHardBound(t *testing.T) {
	rec := deepMCPRecording(1, 400, true)
	engine := &query.Engine{Loader: func(context.Context, string, query.SourceOptions) (*model.Recording, sourcemap.Diagnostics, error) {
		return rec, sourcemap.Diagnostics{}, nil
	}}
	data := mcpQueryResponse(t, engine, "stacks", `{"file":"deep.json","limit":1,"max_stack_frames":0,"max_bytes":4194304}`)
	var response struct {
		Result wireCall[json.RawMessage] `json:"result"`
	}
	if err := json.Unmarshal(data, &response); err != nil {
		t.Fatal(err)
	}
	if len(data)+1 > MaxResponseBytes || response.Result.IsError || response.Result.StructuredContent == nil || len(response.Result.StructuredContent.Rows) != 1 || !containsReason(response.Result.StructuredContent.TruncatedBy, "bytes") {
		t.Fatalf("escaped protocol envelope must trim, not discard row: bytes=%d %.1000s", len(data)+1, data)
	}
}

func TestMCPTypedErrorsAndSourceMaps(t *testing.T) {
	rec := deepMCPRecording(1, 3, false)
	diagnostics := sourcemap.Diagnostics{MappedFrames: 2, UnmappedFrames: 1, MissingMaps: []string{"bundle.js"}, Warnings: []string{"Source map missing; use --sourcemap FILE or DIR."}, Minified: true}
	engine := &query.Engine{Loader: func(context.Context, string, query.SourceOptions) (*model.Recording, sourcemap.Diagnostics, error) {
		return rec, diagnostics, nil
	}}
	data := mcpQueryResponse(t, engine, "top", `{"file":"capture.json","offset":1,"limit":1}`)
	var response struct {
		Result wireCall[json.RawMessage] `json:"result"`
	}
	if err := json.Unmarshal(data, &response); err != nil {
		t.Fatal(err)
	}
	result := response.Result.StructuredContent
	if response.Result.IsError || result == nil || result.Offset != 1 || len(result.Sources) != 1 || result.Sources[0].SourceMaps.MappedFrames != 2 || !result.Sources[0].SourceMaps.Minified || !strings.Contains(response.Result.Content[0].Text, "Source map missing") {
		t.Fatalf("mapping diagnostics or offset lost: %.1000s", data)
	}
	for _, tc := range []struct{ operation, arguments, code string }{
		{"top", `{"file":"capture.json","limit":1001}`, "usage_error"},
		{"paths", `{"file":"capture.json","selector":"absent"}`, "selector_error"},
		{"top", `{"file":"capture.json","bogus":true}`, "usage_error"},
		{"metrics", `{"file":"capture.json","selector":"samples.total_ms"}`, "unknown_key"},
	} {
		data = mcpQueryResponse(t, engine, tc.operation, tc.arguments)
		if err := json.Unmarshal(data, &response); err != nil {
			t.Fatal(err)
		}
		result = response.Result.StructuredContent
		if !response.Result.IsError || result == nil || len(result.Errors) != 1 || string(result.Errors[0].Code) != tc.code || len(response.Result.Content) != 1 || !json.Valid([]byte(response.Result.Content[0].Text)) {
			t.Fatalf("typed error must retain both protocol contents: %.1200s", data)
		}
	}
}

func containsReason(reasons []string, want string) bool {
	for _, reason := range reasons {
		if reason == want {
			return true
		}
	}
	return false
}

func TestMCPKeysAndDiffNormalize(t *testing.T) {
	rec := deepMCPRecording(1, 3, false)
	engine := &query.Engine{Loader: func(context.Context, string, query.SourceOptions) (*model.Recording, sourcemap.Diagnostics, error) {
		return rec, sourcemap.Diagnostics{}, nil
	}}
	data := mcpQueryResponse(t, engine, "keys", `{"file":"capture.json","selector":"samples.","offset":1,"limit":2,"max_bytes":4096}`)
	var keys struct {
		Result wireCall[query.KeyRow] `json:"result"`
	}
	if err := json.Unmarshal(data, &keys); err != nil {
		t.Fatal(err)
	}
	result := keys.Result.StructuredContent
	if keys.Result.IsError || result == nil || len(result.Rows) != 2 || result.Offset != 1 || result.NextOffset == nil || *result.NextOffset != 3 {
		t.Fatalf("MCP key discovery must preserve pagination: %.1000s", data)
	}
	for _, row := range result.Rows {
		if !strings.HasPrefix(row.Key, "samples.") || row.Description == "" || row.Operation == "" || row.Basis == "" {
			t.Fatalf("MCP key metadata missing: %+v", row)
		}
	}
	data = mcpQueryResponse(t, engine, "diff", `{"baseline":"before.json","candidate":"after.json","normalize":"share"}`)
	var diff struct {
		Result wireCall[query.DiffRow] `json:"result"`
	}
	if err := json.Unmarshal(data, &diff); err != nil {
		t.Fatal(err)
	}
	if diff.Result.IsError || diff.Result.StructuredContent == nil || diff.Result.StructuredContent.DiffNormalization == nil || diff.Result.StructuredContent.DiffNormalization.Mode != "share" {
		t.Fatalf("MCP diff normalization lost: %.1000s", data)
	}
}
