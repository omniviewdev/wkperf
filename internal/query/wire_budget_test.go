package query

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/omniviewdev/wkperf/internal/diagnostic"
	"github.com/omniviewdev/wkperf/internal/sourcemap"
)

func deepWireResult(rows, depth int) *Result {
	result := &Result{Schema: Schema, Operation: "stacks", Status: "ok", Sources: []Source{{File: "deep.json", To: 12}}, Command: []string{"wkperf", "query", "deep.json", "stacks"}, By: "self", TotalMS: float64(rows), TotalCount: rows, Limit: rows, Rows: make([]Row, rows)}
	for i := range rows {
		frames := make([]Function, depth)
		for j := range depth {
			name := fmt.Sprintf("render%03d", j)
			if j == depth-1 {
				name = fmt.Sprintf("leaf%03d", i)
			}
			frames[j] = Function{ID: fmt.Sprintf("f_%064x", j+1), Name: name, URL: "wails://app/assets/index.js", Line: j + 1, Column: 1, Category: "app"}
		}
		frames[depth-1].ID = fmt.Sprintf("f_%064x", depth+i+1)
		result.Rows[i] = Row{Function: frames[depth-1], NodeID: fmt.Sprintf("n_%064x", i), SelfMS: 1, TotalMS: 1, Samples: 1, Percent: 100 / float64(rows), Depth: depth, Stack: frames}
	}
	return result
}

func wireObject(t *testing.T, result *Result, format string) (map[string]any, []byte) {
	t.Helper()
	var out bytes.Buffer
	if err := Render(&out, result, format); err != nil {
		t.Fatal(err)
	}
	var document map[string]any
	if err := json.Unmarshal(out.Bytes(), &document); err != nil {
		t.Fatal(err)
	}
	return document, out.Bytes()
}

func TestV2WireSparseRowsAndMeasuredZero(t *testing.T) {
	result := &Result{Schema: Schema, Operation: "metrics", Status: "ok", Sources: []Source{}, Command: []string{}, Rows: []Row{{Key: "samples.count", Value: 0, Unit: "count"}}, TotalCount: 1, Limit: DefaultLimit}
	doc, _ := wireObject(t, result, "json")
	row := doc["rows"].([]any)[0].(map[string]any)
	if len(row) != 3 || row["key"] != "samples.count" || row["value"] != float64(0) || row["unit"] != "count" {
		t.Fatalf("scalar row contains unrelated defaults or loses zero: %#v", row)
	}
	if _, ok := doc["total_ms"]; ok {
		t.Fatal("v2 still uses ambiguous total_ms envelope field")
	}
	if _, ok := doc["by"]; ok {
		t.Fatal("scalar query exposes irrelevant ranking")
	}
	if doc["sampled_total_ms"] != float64(0) {
		t.Fatal("genuine sampled total zero missing")
	}
}

func TestV2DefaultDeepBudgetPreservesUsefulRows(t *testing.T) {
	result := deepWireResult(50, 400)
	result.Limit, result.TotalCount, result.Truncated = DefaultLimit, 120, true
	result.TruncatedBy = []string{"limit"}
	doc, encoded := wireObject(t, result, "json")
	if len(encoded) > DefaultMaxBytes {
		t.Fatalf("default output = %d bytes, budget %d", len(encoded), DefaultMaxBytes)
	}
	rows := doc["rows"].([]any)
	if len(rows) == 0 || len(rows) >= 50 || doc["returned_count"] != float64(len(rows)) {
		t.Fatalf("not useful bounded prefix: rows=%d returned=%v", len(rows), doc["returned_count"])
	}
	if doc["next_offset"] != float64(len(rows)) || !strings.Contains(fmt.Sprint(doc["truncated_by"]), "bytes") {
		t.Fatalf("missing byte pagination metadata: %#v", doc)
	}
	stack := rows[0].(map[string]any)["stack"].([]any)
	if len(stack) != 49 || stack[8].(map[string]any)["elided"] != float64(352) {
		t.Fatalf("root8+leaf40 preview incorrect: %#v", stack)
	}
	if rows[0].(map[string]any)["stack_depth"] != float64(400) {
		t.Fatal("full stack depth lost")
	}
	functions := doc["functions"].(map[string]any)
	used := map[string]bool{}
	for _, raw := range rows {
		row := raw.(map[string]any)
		used[row["function_id"].(string)] = true
		for _, ref := range row["stack"].([]any) {
			if id, ok := ref.(string); ok {
				used[id] = true
			}
		}
	}
	if len(functions) != len(used) {
		t.Fatalf("unused frames retained: dictionary=%d referenced=%d", len(functions), len(used))
	}
	for id := range used {
		frame, ok := functions[id].(map[string]any)
		if !ok || frame["id"] != id || frame["native"] != false || frame["anonymous"] != false {
			t.Fatalf("noncanonical frame metadata: %#v", frame)
		}
	}
	_, repeated := wireObject(t, result, "json")
	if !bytes.Equal(encoded, repeated) {
		t.Fatal("repeated render changed bytes")
	}
}

func TestV2ExplicitFullStackAndActualFormatBounds(t *testing.T) {
	result := deepWireResult(1, 400)
	result.MaxBytes, result.MaxStackFramesSet = MaxBytes, true
	doc, _ := wireObject(t, result, "json")
	stack := doc["rows"].([]any)[0].(map[string]any)["stack"].([]any)
	if len(stack) != 400 {
		t.Fatalf("explicit zero frame limit returned %d frames", len(stack))
	}
	for _, ref := range stack {
		if _, ok := ref.(string); !ok {
			t.Fatal("explicit full stack elided despite available budget")
		}
	}
	for _, format := range []string{"json", "md", "tsv"} {
		t.Run(format, func(t *testing.T) {
			bounded := deepWireResult(50, 400)
			bounded.MaxBytes = 5000
			var out bytes.Buffer
			if err := Render(&out, bounded, format); err != nil {
				t.Fatal(err)
			}
			if out.Len() > bounded.MaxBytes || out.Len() == 0 {
				t.Fatalf("actual %s output = %d, budget %d", format, out.Len(), bounded.MaxBytes)
			}
		})
	}
}

func TestV2BytePrefixUsesActualEnvelopeAndBinarySearch(t *testing.T) {
	result := deepWireResult(1000, 1)
	result.MaxBytes = 7777
	calls := 0
	encode := func(value *Result) ([]byte, error) {
		calls++
		data, err := jsonBytes(value)
		if err != nil {
			return nil, err
		}
		return json.Marshal(struct {
			Text       string          `json:"text"`
			Structured json.RawMessage `json:"structured"`
		}{string(data), data})
	}
	bounded, data, err := Bound(result, 12000, encode)
	if err != nil {
		t.Fatal(err)
	}
	if len(data) > 12000 || bounded.ReturnedCount == 0 || bounded.ReturnedCount >= len(result.Rows) {
		t.Fatalf("wrong actual-envelope prefix: bytes=%d rows=%d", len(data), bounded.ReturnedCount)
	}
	if calls > 18 {
		t.Fatalf("prefix fitting used %d encodes; expected logarithmic search", calls)
	}
	if bounded.MaxBytes != result.MaxBytes {
		t.Fatal("envelope cap overwrote requested data budget")
	}
	if len(result.Rows) != 1000 || result.ReturnedCount != 0 || result.NextOffset != nil {
		t.Fatal("bound mutated its input")
	}
	next := *bounded
	next.Rows = result.Rows[:bounded.ReturnedCount+1]
	next.ReturnedCount++
	offset := next.Offset + next.ReturnedCount
	next.NextOffset = &offset
	nextData, err := encode(&next)
	if err != nil {
		t.Fatal(err)
	}
	if len(nextData) <= 12000 {
		t.Fatal("binary search did not select the largest fitting row prefix")
	}
}

func TestV2TooSmallBudgetReturnsTypedGuidanceWithoutOutput(t *testing.T) {
	result := deepWireResult(1, 400)
	result.MaxBytes = 64
	var out bytes.Buffer
	err := Render(&out, result, "json")
	var typed *diagnostic.Error
	if !errors.As(err, &typed) || typed.Code != diagnostic.UsageError || !strings.Contains(typed.Message(), "increase max_bytes") {
		t.Fatalf("missing structured budget guidance: %v", err)
	}
	if out.Len() != 0 {
		t.Fatal("budget failure leaked a partial document")
	}
	if _, _, err := Bound(result, MaxBytes+1, jsonBytes); err == nil {
		t.Fatal("hard 4 MiB cap ignored")
	}
}

func TestV2FormatsKeepSourceDiagnosticsAndErrors(t *testing.T) {
	result := deepWireResult(1, 3)
	result.Sources[0].SourceMaps = sourcemap.Diagnostics{MappedFrames: 2, UnmappedFrames: 1, MissingMaps: []string{"bundle.js"}, Warnings: []string{"source map hint"}, Minified: true}
	result.Errors = []Error{{File: "bad.json", Code: diagnostic.SourceMapError, Message: "failed mapping"}}
	doc, _ := wireObject(t, result, "json")
	source := doc["sources"].([]any)[0].(map[string]any)
	diagnostics := source["source_maps"].(map[string]any)
	if diagnostics["mappedFrames"] != float64(2) || diagnostics["unmappedFrames"] != float64(1) || diagnostics["minified"] != true {
		t.Fatalf("diagnostics lost: %#v", diagnostics)
	}
	for _, format := range []string{"md", "tsv"} {
		var out bytes.Buffer
		if err := Render(&out, result, format); err != nil {
			t.Fatal(err)
		}
		text := out.String()
		for _, want := range []string{"source map hint", "bundle.js", "source_map_error", "failed mapping"} {
			if !strings.Contains(text, want) {
				t.Fatalf("%s dropped %q", format, want)
			}
		}
		if strings.Index(text, "failed mapping") > strings.Index(strings.ToLower(text), "reproduce") {
			t.Fatalf("%s errors are not mandatory leading metadata", format)
		}
	}
}

func TestV2EveryOperationUsesItsConcreteRows(t *testing.T) {
	expected := map[string]string{
		"top":       "function_id,node_id,self_ms,total_ms,samples,percent",
		"find":      "function_id,node_id,self_ms,total_ms,samples,percent",
		"by-root":   "function_id,node_id,self_ms,total_ms,samples,percent",
		"callers":   "function_id,node_id,total_ms,samples,percent",
		"callees":   "function_id,node_id,total_ms,samples,percent",
		"paths":     "function_id,node_id,self_ms,total_ms,samples,percent,depth,stack,stack_depth",
		"stacks":    "function_id,node_id,self_ms,total_ms,samples,percent,depth,stack,stack_depth",
		"subtree":   "function_id,node_id,self_ms,total_ms,samples,percent,depth,stack,stack_depth",
		"hot-path":  "function_id,node_id,self_ms,total_ms,samples,percent,depth",
		"depth":     "depth,self_ms,total_ms,samples,percent",
		"diff":      "function_id,baseline_self_ms,candidate_self_ms,delta_self_ms,baseline_total_ms,candidate_total_ms,delta_total_ms,delta_samples",
		"metrics":   "key,value,unit",
		"rootcause": "key,value,unit,basis",
		"keys":      "key,op,unit,description,basis,alias_of",
		"events":    "kind,name,count,total_ms,mean_ms,p95_ms,max_ms,top_self,top_inclusive,top_self_total_count,top_inclusive_total_count,functions_truncated",
	}
	for operation, fields := range expected {
		t.Run(operation, func(t *testing.T) {
			result := deepWireResult(1, 3)
			result.Operation = operation
			row := &result.Rows[0]
			row.Key, row.Unit, row.Operation, row.Description, row.Basis, row.AliasOf = "zero", "count", "metrics", "Count of observations", "sampled", "canonical.zero"
			row.SelfMS, row.TotalMS, row.Percent, row.Samples = 0, 0, 0, 0
			row.Kind = "timer"
			doc, _ := wireObject(t, result, "json")
			actual := doc["rows"].([]any)[0].(map[string]any)
			names := strings.Split(fields, ",")
			if len(actual) != len(names) {
				t.Fatalf("wrong sparse shape: %#v", actual)
			}
			for _, name := range names {
				if _, ok := actual[name]; !ok {
					t.Fatalf("missing meaningful field %q: %#v", name, actual)
				}
			}
			_, hasBy := doc["by"]
			if hasBy != (operation == "top" || operation == "diff") {
				t.Fatalf("irrelevant by metadata: %#v", doc)
			}
			if operation == "metrics" || operation == "rootcause" || operation == "keys" || operation == "depth" || operation == "events" {
				if _, ok := doc["functions"]; ok {
					t.Fatal("unreferenced domain function escaped to dictionary")
				}
			}
		})
	}
}

func TestV2SchemaSemanticsAndOperationBranches(t *testing.T) {
	schema := ResultSchema()
	branches := schema["oneOf"].([]any)
	seen := map[string]bool{}
	for _, raw := range branches {
		properties := raw.(map[string]any)["properties"].(map[string]any)
		op, ok := properties["operation"].(map[string]any)
		if !ok {
			continue
		}
		name := op["const"].(string)
		seen[name] = true
		row := properties["rows"].(map[string]any)["items"].(map[string]any)
		if row["additionalProperties"] != false {
			t.Fatalf("loose %s row schema", name)
		}
		if name == "metrics" {
			leaves := row["properties"].(map[string]any)
			if len(leaves) != 3 || leaves["value"].(map[string]any)["x-unit"] != "row.unit" {
				t.Fatal("scalar schema lacks dynamic physical unit")
			}
		}
	}
	for _, operation := range Operations {
		if operation != "window" && !seen[operation] {
			t.Fatalf("missing operation branch %s", operation)
		}
	}
	var check func(any, string)
	check = func(node any, path string) {
		switch value := node.(type) {
		case map[string]any:
			if properties, ok := value["properties"].(map[string]any); ok {
				for name, raw := range properties {
					property := raw.(map[string]any)
					if property["description"] == nil || property["description"] == "" {
						t.Fatalf("missing semantic description at %s.%s", path, name)
					}
					if property["type"] == "number" || property["type"] == "integer" {
						if property["x-unit"] == nil || property["x-unit"] == "" {
							t.Fatalf("missing physical unit at %s.%s", path, name)
						}
					}
				}
			}
			for key, child := range value {
				check(child, path+"."+key)
			}
		case []any:
			for i, child := range value {
				check(child, fmt.Sprintf("%s[%d]", path, i))
			}
		}
	}
	check(schema, "result")
	for _, operation := range Operations {
		check(RequestSchema(operation), operation)
	}
}
