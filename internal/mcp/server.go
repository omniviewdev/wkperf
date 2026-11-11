// Package mcp exposes query.Engine over newline-framed MCP JSON-RPC stdio.
package mcp

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/omniviewdev/wkperf/internal/diagnostic"
	"github.com/omniviewdev/wkperf/internal/query"
)

const (
	ProtocolVersion  = "2025-06-18"
	MaxMessageBytes  = 4 << 20
	MaxResponseBytes = 4 << 20
)

type Server struct {
	Engine  *query.Engine
	Version string
	Sources query.SourceOptions
}

type request struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params"`
}

type tool struct {
	Name         string          `json:"name"`
	Description  string          `json:"description"`
	InputSchema  map[string]any  `json:"inputSchema"`
	OutputSchema map[string]any  `json:"outputSchema"`
	Annotations  map[string]bool `json:"annotations"`
}

type textContent struct {
	Type string `json:"type"`
	Text string `json:"text"`
}
type callResult struct {
	Content           []textContent   `json:"content"`
	StructuredContent json.RawMessage `json:"structuredContent,omitempty"`
	IsError           bool            `json:"isError"`
}

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

type rpcResult struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Result  any             `json:"result"`
}

// Serve writes only protocol messages to output. Notifications have no reply.
func (s *Server) Serve(ctx context.Context, input io.Reader, output io.Writer) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	engine := s.Engine
	if engine == nil {
		engine = &query.Engine{}
	}
	version := s.Version
	if version == "" {
		version = "dev"
	}
	reader := messageReader{reader: bufio.NewReader(input)}
	encoder := json.NewEncoder(output)
	encoder.SetEscapeHTML(false)
	initialized, ready := false, false
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		line, oversized, err := reader.read()
		if err == io.EOF {
			return ctx.Err()
		}
		if err != nil {
			return fmt.Errorf("read MCP message: %w", err)
		}
		if oversized {
			if err := writeError(encoder, nil, -32600, "Request exceeds 4 MiB"); err != nil {
				return err
			}
			continue
		}
		line = bytes.TrimSpace(line)
		if len(line) == 0 {
			continue
		}
		var req request
		if !json.Valid(line) {
			if err := writeError(encoder, nil, -32700, "Parse error"); err != nil {
				return err
			}
			continue
		}
		if err := json.Unmarshal(line, &req); err != nil || req.JSONRPC != "2.0" || req.Method == "" || !validID(req.ID) {
			if err := writeError(encoder, nil, -32600, "Invalid Request"); err != nil {
				return err
			}
			continue
		}
		notification := len(req.ID) == 0
		if notification {
			if req.Method == "notifications/initialized" && initialized {
				ready = true
			}
			continue
		}
		var result any
		var payload []byte
		switch req.Method {
		case "ping":
			result = struct{}{}
		case "initialize":
			var params struct {
				ProtocolVersion string `json:"protocolVersion"`
			}
			if initialized {
				if err := writeError(encoder, req.ID, -32600, "Already initialized"); err != nil {
					return err
				}
				continue
			}
			if err := json.Unmarshal(req.Params, &params); err != nil || params.ProtocolVersion == "" {
				if err := writeError(encoder, req.ID, -32602, "initialize requires protocolVersion"); err != nil {
					return err
				}
				continue
			}
			protocol := ProtocolVersion
			for _, supported := range []string{"2024-11-05", "2025-03-26", ProtocolVersion} {
				if params.ProtocolVersion == supported {
					protocol = supported
				}
			}
			result = map[string]any{"protocolVersion": protocol, "capabilities": map[string]any{"tools": map[string]any{"listChanged": false}}, "serverInfo": map[string]any{"name": "wkperf", "version": version}, "instructions": "Read local WebKit recordings with bounded deterministic queries. Times are recording-relative seconds. Resolve ambiguous selectors using returned IDs."}
			initialized = true
		case "tools/list":
			if !ready {
				if err := writeError(encoder, req.ID, -32002, "Server not initialized"); err != nil {
					return err
				}
				continue
			}
			var params struct {
				Cursor string `json:"cursor"`
			}
			if len(req.Params) > 0 && string(req.Params) != "null" {
				if err := json.Unmarshal(req.Params, &params); err != nil || params.Cursor != "" {
					if err := writeError(encoder, req.ID, -32602, "Invalid tools/list cursor or parameters"); err != nil {
						return err
					}
					continue
				}
			}
			tools := make([]tool, 0, len(query.Operations))
			for _, op := range query.Operations {
				tools = append(tools, tool{Name: op, Description: description(op), InputSchema: query.RequestSchema(op), OutputSchema: query.ResultSchema(), Annotations: map[string]bool{"readOnlyHint": true, "destructiveHint": false, "idempotentHint": true, "openWorldHint": false}})
			}
			result = struct {
				Tools []tool `json:"tools"`
			}{Tools: tools}
		case "tools/call":
			if !ready {
				if err := writeError(encoder, req.ID, -32002, "Server not initialized"); err != nil {
					return err
				}
				continue
			}
			var params struct {
				Name      string          `json:"name"`
				Arguments json.RawMessage `json:"arguments"`
			}
			if err := json.Unmarshal(req.Params, &params); err != nil || !knownOperation(params.Name) {
				if err := writeError(encoder, req.ID, -32602, "Unknown tool or invalid tools/call parameters"); err != nil {
					return err
				}
				continue
			}
			var queryRequest query.Request
			arguments := struct {
				*query.Request
				MinShare       *float64 `json:"min_share"`
				Depth          *int     `json:"depth"`
				MinSamples     *int     `json:"min_samples"`
				TieRatio       *float64 `json:"tie_ratio"`
				MaxStackFrames *int     `json:"max_stack_frames"`
			}{Request: &queryRequest}
			decoder := json.NewDecoder(bytes.NewReader(params.Arguments))
			decoder.DisallowUnknownFields()
			if err := decoder.Decode(&arguments); err != nil {
				result = failedCall(params.Name, diagnostic.Wrap(diagnostic.UsageError, "", fmt.Errorf("invalid tool arguments: %w", err)))
				break
			}
			if queryRequest.Operation != "" {
				result = failedCall(params.Name, diagnostic.Wrap(diagnostic.UsageError, "", fmt.Errorf("operation is selected by the tool name")))
				break
			}
			if arguments.MinShare != nil {
				queryRequest.MinShare, queryRequest.MinShareSet = *arguments.MinShare, true
			}
			if arguments.Depth != nil {
				queryRequest.Depth, queryRequest.DepthSet = *arguments.Depth, true
			}
			if arguments.MinSamples != nil {
				queryRequest.MinSamples, queryRequest.MinSamplesSet = *arguments.MinSamples, true
			}
			if arguments.TieRatio != nil {
				queryRequest.TieRatio, queryRequest.TieRatioSet = *arguments.TieRatio, true
			}
			if arguments.MaxStackFrames != nil {
				queryRequest.MaxStackFrames, queryRequest.MaxStackFramesSet = *arguments.MaxStackFrames, true
			}
			if queryRequest.Sourcemaps == nil {
				queryRequest.Sourcemaps = s.Sources.Sourcemaps
			}
			if queryRequest.AppURLs == nil {
				queryRequest.AppURLs = s.Sources.Ownership.AppURLs
			}
			if queryRequest.LibraryURLs == nil {
				queryRequest.LibraryURLs = s.Sources.Ownership.LibraryURLs
			}
			queryRequest.Operation = params.Name
			queryResult, err := engine.Run(ctx, queryRequest)
			if err != nil {
				result = failedCall(params.Name, err)
				break
			}
			cap := min(2*queryResult.MaxBytes+1024, MaxResponseBytes)
			_, payload, err = query.Bound(queryResult, cap, func(value *query.Result) ([]byte, error) {
				call, err := queryCall(value)
				if err != nil {
					return nil, err
				}
				data, err := json.Marshal(rpcResult{JSONRPC: "2.0", ID: req.ID, Result: call})
				if err != nil {
					return nil, err
				}
				return append(data, '\n'), nil
			})
			if err != nil {
				result = failedCall(params.Name, err)
				payload = nil
			}
		default:
			if err := writeError(encoder, req.ID, -32601, "Method not found"); err != nil {
				return err
			}
			continue
		}
		if payload != nil {
			if _, err := output.Write(payload); err != nil {
				return err
			}
			continue
		}
		if err := writeResult(output, req.ID, result); err != nil {
			return err
		}
	}
}

// messageReader borrows the reader's buffer for short lines and reuses bounded
// storage for longer lines. Oversized lines are discarded through their newline.
type messageReader struct {
	reader *bufio.Reader
	buffer []byte
}

func (r *messageReader) read() ([]byte, bool, error) {
	r.buffer = r.buffer[:0]
	oversized := false
	for {
		chunk, err := r.reader.ReadSlice('\n')
		if err != nil && err != bufio.ErrBufferFull && err != io.EOF {
			return nil, false, err
		}
		if err == nil {
			chunk = chunk[:len(chunk)-1]
		}
		if !oversized {
			if len(chunk) > MaxMessageBytes-len(r.buffer) {
				oversized = true
			} else if len(r.buffer) == 0 && err != bufio.ErrBufferFull {
				if len(chunk) == 0 && err == io.EOF {
					return nil, false, io.EOF
				}
				return chunk, false, nil
			} else {
				if needed := len(r.buffer) + len(chunk); needed > cap(r.buffer) {
					capacity := min(MaxMessageBytes, max(2*cap(r.buffer), needed))
					buffer := make([]byte, len(r.buffer), capacity)
					copy(buffer, r.buffer)
					r.buffer = buffer
				}
				r.buffer = append(r.buffer, chunk...)
			}
		}
		if err == bufio.ErrBufferFull {
			continue
		}
		if oversized {
			return nil, true, nil
		}
		return r.buffer, false, nil
	}
}

func validID(id json.RawMessage) bool {
	if len(id) > 1024 {
		return false
	}
	if len(id) == 0 {
		return true
	}
	if id[0] == '"' {
		return json.Valid(id)
	}
	digits := id
	if digits[0] == '-' {
		digits = digits[1:]
	}
	if len(digits) == 0 {
		return false
	}
	if digits[0] == '0' {
		return len(digits) == 1
	}
	if digits[0] < '1' || digits[0] > '9' {
		return false
	}
	for _, digit := range digits[1:] {
		if digit < '0' || digit > '9' {
			return false
		}
	}
	return true
}

func writeError(encoder *json.Encoder, id json.RawMessage, code int, message string) error {
	return encoder.Encode(struct {
		JSONRPC string          `json:"jsonrpc"`
		ID      json.RawMessage `json:"id"`
		Error   rpcError        `json:"error"`
	}{JSONRPC: "2.0", ID: id, Error: rpcError{Code: code, Message: message}})
}

func queryCall(result *query.Result) (callResult, error) {
	data, err := json.Marshal(result)
	if err != nil {
		return callResult{}, err
	}
	return callResult{Content: []textContent{{Type: "text", Text: string(data)}}, StructuredContent: data, IsError: result.Status != "ok"}, nil
}

func failedCall(operation string, err error) callResult {
	failure := query.Error{Code: diagnostic.UsageError, Message: err.Error()}
	var typed *diagnostic.Error
	if errors.As(err, &typed) {
		failure.File, failure.Code, failure.Message = typed.File, typed.Code, typed.Message()
		failure.Candidates = typed.Candidates
	}
	if len(failure.Message) > 4096 {
		failure.Message = failure.Message[:4096] + " [TRUNCATED error message]"
	}
	if len(failure.File) > 4096 {
		failure.File = failure.File[:4096] + " [TRUNCATED file path]"
	}
	copiedCandidates := false
	for i, candidate := range failure.Candidates {
		if len(candidate) > 1024 {
			if !copiedCandidates {
				failure.Candidates = append([]string(nil), failure.Candidates...)
				copiedCandidates = true
			}
			failure.Candidates[i] = candidate[:1024] + " [TRUNCATED candidate]"
		}
	}
	result := &query.Result{Schema: query.Schema, Operation: operation, Status: "error", Limit: query.DefaultLimit, MaxBytes: query.DefaultMaxBytes, Errors: []query.Error{failure}}
	// The failure document contains only finite zero values and bounded strings.
	call, _ := queryCall(result)
	return call
}

func writeResult(output io.Writer, id json.RawMessage, result any) error {
	payload, err := json.Marshal(rpcResult{JSONRPC: "2.0", ID: id, Result: result})
	if err != nil {
		return err
	}
	if len(payload)+1 > MaxResponseBytes {
		payload, err = json.Marshal(rpcResult{JSONRPC: "2.0", ID: id, Result: failedCall("", diagnostic.Wrap(diagnostic.UsageError, "", fmt.Errorf("response metadata exceeds the 4 MiB protocol bound; narrow the request")))})
		if err != nil {
			return err
		}
	}
	_, err = output.Write(append(payload, '\n'))
	return err
}
func knownOperation(name string) bool {
	for _, op := range query.Operations {
		if op == name {
			return true
		}
	}
	return false
}

func description(operation string) string {
	descriptions := map[string]string{
		"top":       "Rank weighted self or inclusive function time; optionally select one function.",
		"callers":   "Direct callers with milliseconds, percentage of selected function total, and sample counts.",
		"callees":   "Direct callees with milliseconds, percentage of selected function total, and sample counts.",
		"paths":     "Heaviest complete root-to-leaf sampled stacks containing a function or calling context.",
		"stacks":    "Complete weighted sampled stacks; optionally select an exact node_id from paths or stacks output. Use paths to search by function name.",
		"subtree":   "Calling-context subtree bounded by depth, minimum milliseconds, and row limit.",
		"hot-path":  "Follow supported heavy branches, stopping at insufficient sample support or near ties; alternatives remain visible.",
		"by-root":   "Sampled time grouped by root function or trigger.",
		"find":      "Regex search over generated and original functions with self and inclusive time.",
		"diff":      "Candidate-minus-baseline self and inclusive deltas, sorted by absolute delta.",
		"depth":     "Weighted stack-depth distribution and deterministic deepest full stack.",
		"window":    "Run a query_operation (default top) inside an explicit recording-relative from/to interval.",
		"keys":      "Discover metric and root-cause keys with units, descriptions, evidence basis, prefix/regex filtering and pagination.",
		"metrics":   "Scalar analyzer and insight metrics; select an exact key, trailing .* prefix, or /regex/.",
		"events":    "Trigger counts and total, mean, p95, and maximum instrumented durations.",
		"rootcause": "Typed root-cause measurements; select an exact key, trailing .* prefix, or /regex/.",
	}
	return strings.TrimSpace(descriptions[operation])
}
