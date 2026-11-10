// Package mcp exposes query.Engine over newline-framed MCP JSON-RPC stdio.
package mcp

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"

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
	Content           []textContent `json:"content"`
	StructuredContent *query.Result `json:"structuredContent,omitempty"`
	IsError           bool          `json:"isError"`
}

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
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
	scanner := bufio.NewScanner(input)
	scanner.Buffer(make([]byte, 4096), MaxMessageBytes)
	encoder := json.NewEncoder(output)
	encoder.SetEscapeHTML(false)
	initialized, ready := false, false
	for scanner.Scan() {
		if err := ctx.Err(); err != nil {
			return err
		}
		line := bytes.TrimSpace(scanner.Bytes())
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
				MinShare *float64 `json:"min_share"`
				Depth    *int     `json:"depth"`
			}{Request: &queryRequest}
			decoder := json.NewDecoder(bytes.NewReader(params.Arguments))
			decoder.DisallowUnknownFields()
			if err := decoder.Decode(&arguments); err != nil {
				result = failedCall("Invalid tool arguments: " + err.Error())
				break
			}
			if queryRequest.Operation != "" {
				result = failedCall("operation is selected by the tool name")
				break
			}
			if arguments.MinShare != nil {
				queryRequest.MinShare, queryRequest.MinShareSet = *arguments.MinShare, true
			}
			if arguments.Depth != nil {
				queryRequest.Depth, queryRequest.DepthSet = *arguments.Depth, true
			}
			queryRequest.Operation = params.Name
			queryResult, err := engine.Run(ctx, queryRequest)
			if err != nil {
				result = failedCall(err.Error())
				break
			}
			data, err := json.Marshal(queryResult)
			if err != nil {
				result = failedCall(err.Error())
				break
			}
			result = callResult{Content: []textContent{{Type: "text", Text: string(data)}}, StructuredContent: queryResult, IsError: queryResult.Status == "ambiguous"}
		default:
			if err := writeError(encoder, req.ID, -32601, "Method not found"); err != nil {
				return err
			}
			continue
		}
		if err := writeResult(output, req.ID, result); err != nil {
			return err
		}
	}
	if err := scanner.Err(); err != nil {
		return fmt.Errorf("read MCP message (maximum %d bytes): %w", MaxMessageBytes, err)
	}
	return ctx.Err()
}

func validID(id json.RawMessage) bool {
	if len(id) > 1024 {
		return false
	}
	if len(id) == 0 {
		return true
	}
	var value any
	if json.Unmarshal(id, &value) != nil {
		return false
	}
	switch value.(type) {
	case nil, string, float64:
		return true
	default:
		return false
	}
}

func writeError(encoder *json.Encoder, id json.RawMessage, code int, message string) error {
	return encoder.Encode(map[string]any{"jsonrpc": "2.0", "id": id, "error": rpcError{Code: code, Message: message}})
}

func failedCall(message string) callResult {
	if len(message) > 4096 {
		message = message[:4096] + " [TRUNCATED error message]"
	}
	return callResult{Content: []textContent{{Type: "text", Text: message}}, IsError: true}
}

func writeResult(output io.Writer, id json.RawMessage, result any) error {
	payload, err := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": id, "result": result})
	if err != nil {
		return err
	}
	if len(payload) >= MaxResponseBytes {
		payload, err = json.Marshal(map[string]any{"jsonrpc": "2.0", "id": id, "result": failedCall("Query response exceeds the 4 MiB protocol bound; reduce limit or narrow the window/selector. No rows were returned.")})
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
		"stacks":    "Heaviest complete weighted sampled stacks, optionally one stable stack ID.",
		"subtree":   "Calling-context subtree bounded by depth, minimum milliseconds, and row limit.",
		"hot-path":  "Greedy heaviest-child path until the minimum sampled-time share.",
		"by-root":   "Sampled time grouped by root function or trigger.",
		"find":      "Regex search over generated and original functions with self and inclusive time.",
		"diff":      "Candidate-minus-baseline self and inclusive deltas, sorted by absolute delta.",
		"depth":     "Weighted stack-depth distribution and deterministic deepest full stack.",
		"window":    "Run a query_operation (default top) inside an explicit recording-relative from/to interval.",
		"metrics":   "Scalar analyzer and insight metrics; optionally select an exact metric key.",
		"events":    "Trigger counts and total, mean, p95, and maximum instrumented durations.",
		"rootcause": "Typed root-cause measurements; optionally select an exact statistic key.",
	}
	return strings.TrimSpace(descriptions[operation])
}
