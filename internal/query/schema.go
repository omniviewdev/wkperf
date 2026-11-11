package query

import (
	"reflect"
	"strings"

	"github.com/omniviewdev/wkperf/internal/diagnostic"
	"github.com/omniviewdev/wkperf/internal/flame"
)

var categories = []string{"app", "framework", "native", "gc", "unknown-bundled", "injected"}
var errorCodes = []string{"input_error", "load_error", "source_map_error", "window_error", "selector_error", "ambiguous_selector", "unknown_key", "usage_error", "regression", "fixture_mismatch", "score_below_minimum"}

// ResultSchema describes the v2 wire document, not the internal row union.
// Each operation selects exactly one concrete sparse row shape.
func ResultSchema() map[string]any {
	out := objectSchema(reflect.TypeFor[Document]())
	out["$schema"] = "https://json-schema.org/draft/2020-12/schema"
	out["title"] = Schema
	out["description"] = "Bounded deterministic recording query. Sampled weights are statistical attribution, not measured wall-clock occupancy. Operation selects the row shape."
	properties := out["properties"].(map[string]any)
	properties["schema"].(map[string]any)["const"] = Schema
	properties["status"].(map[string]any)["enum"] = []string{"ok", "ambiguous", "error"}
	properties["by"].(map[string]any)["enum"] = []string{"self", "total"}
	properties["limit"].(map[string]any)["minimum"] = 1
	properties["limit"].(map[string]any)["maximum"] = MaxLimit
	properties["max_bytes"].(map[string]any)["minimum"] = 1
	properties["max_bytes"].(map[string]any)["maximum"] = MaxBytes
	for _, key := range []string{"total_count", "returned_count", "offset", "next_offset"} {
		properties[key].(map[string]any)["minimum"] = 0
	}
	properties["truncated_by"].(map[string]any)["items"].(map[string]any)["enum"] = []string{"limit", "bytes", "stack_frames"}
	branches := make([]any, 0, len(Operations))
	for _, operation := range Operations {
		if operation == "window" {
			continue
		} // Window results name the selected underlying operation.
		branch := map[string]any{"properties": map[string]any{
			"operation": map[string]any{"const": operation, "description": "Executed operation selecting this concrete sparse row shape."},
			"status":    map[string]any{"enum": []string{"ok", "ambiguous"}, "description": "Successful query or function-selection ambiguity; failures use the error branch."},
			"rows":      map[string]any{"type": "array", "description": "Ordered " + operation + " rows after offset, row limit and actual-byte bounds.", "items": rowSchema(operation)},
		}}
		if operation == "top" || operation == "diff" {
			branch["required"] = []string{"by"}
		} else {
			branch["not"] = map[string]any{"required": []string{"by"}}
		}
		branches = append(branches, branch)
	}
	branches = append(branches, map[string]any{"properties": map[string]any{
		"status": map[string]any{"const": "error", "description": "Typed query or tool-argument failure; successful data rows are absent."},
		"rows":   map[string]any{"type": "array", "description": "No successful rows are returned by an error result.", "maxItems": 0, "items": map[string]any{"description": "Error results contain no successful rows."}},
		"errors": map[string]any{"minItems": 1, "description": "At least one typed failure explaining why no rows were returned."},
	}, "required": []string{"errors"}})
	out["oneOf"] = branches
	return out
}

func rowSchema(operation string) map[string]any {
	switch operation {
	case "metrics":
		return objectSchema(reflect.TypeFor[ValueRow]())
	case "rootcause":
		return objectSchema(reflect.TypeFor[RootCauseRow]())
	case "keys":
		return objectSchema(reflect.TypeFor[KeyRow]())
	case "events":
		return objectSchema(reflect.TypeFor[EventRow]())
	case "diff":
		return objectSchema(reflect.TypeFor[DiffRow]())
	case "depth":
		return objectSchema(reflect.TypeFor[DepthRow]())
	case "callers", "callees":
		return objectSchema(reflect.TypeFor[EdgeRow]())
	case "paths", "stacks", "subtree":
		return objectSchema(reflect.TypeFor[PathRow]())
	case "hot-path":
		return objectSchema(reflect.TypeFor[HotPathRow]())
	default:
		return objectSchema(reflect.TypeFor[FunctionRow]())
	}
}

// RequestSchema is the tool-input contract. The tool name selects operation.
func RequestSchema(operation string) map[string]any {
	out := objectSchema(reflect.TypeFor[Request]())
	properties := out["properties"].(map[string]any)
	delete(properties, "operation")
	out["required"] = []string{"file"}
	if operation == "diff" {
		out["required"] = []string{"file", "candidate"}
	}
	if operation == "callers" || operation == "callees" || operation == "paths" || operation == "subtree" || operation == "find" {
		out["required"] = []string{"file", "selector"}
	}
	if operation == "stacks" {
		properties["selector"].(map[string]any)["description"] = "Optional exact canonical node_id from paths or stacks output. Omit to list stacks; use paths to search by function name. Report-local S labels are not canonical IDs."
	}
	if operation == "window" {
		operations := make([]string, 0, len(Operations)-1)
		for _, op := range Operations {
			if op != "window" {
				operations = append(operations, op)
			}
		}
		out["required"] = []string{"file", "from", "to"}
		properties["query_operation"].(map[string]any)["enum"] = operations
		properties["query_operation"].(map[string]any)["default"] = "top"
	} else {
		delete(properties, "query_operation")
	}
	properties["file"].(map[string]any)["minLength"] = 1
	for _, name := range []string{"limit", "offset", "max_bytes", "max_stack_frames", "top", "depth", "min_samples"} {
		properties[name].(map[string]any)["minimum"] = 0
	}
	properties["limit"].(map[string]any)["maximum"] = MaxLimit
	properties["limit"].(map[string]any)["default"] = DefaultLimit
	properties["max_bytes"].(map[string]any)["maximum"] = MaxBytes
	properties["max_bytes"].(map[string]any)["default"] = DefaultMaxBytes
	properties["max_stack_frames"].(map[string]any)["default"] = DefaultMaxStackFrames
	properties["top"].(map[string]any)["default"] = 10
	properties["depth"].(map[string]any)["default"] = 8
	properties["min_samples"].(map[string]any)["default"] = 5
	properties["by"].(map[string]any)["enum"] = []string{"self", "total"}
	properties["by"].(map[string]any)["default"] = "self"
	properties["category"].(map[string]any)["enum"] = append([]string{""}, categories...)
	properties["normalize"].(map[string]any)["enum"] = []string{"auto", "none", "per-second", "share"}
	properties["normalize"].(map[string]any)["default"] = "auto"
	for _, name := range []string{"from", "to", "trim_start", "trim_end", "min_ms", "min_share", "tie_ratio", "long_task_ms", "jank_ms"} {
		properties[name].(map[string]any)["minimum"] = 0
	}
	properties["min_share"].(map[string]any)["maximum"] = 1
	properties["min_share"].(map[string]any)["default"] = .05
	properties["tie_ratio"].(map[string]any)["maximum"] = 1
	properties["tie_ratio"].(map[string]any)["default"] = .8
	properties["jank_ms"].(map[string]any)["default"] = 16.7
	properties["long_task_ms"].(map[string]any)["default"] = float64(50)
	return out
}

func objectSchema(t reflect.Type) map[string]any {
	properties := map[string]any{}
	required := []string{}
	for i := range t.NumField() {
		field := t.Field(i)
		if field.Anonymous {
			nested := objectSchema(field.Type)
			for name, schema := range nested["properties"].(map[string]any) {
				properties[name] = schema
			}
			required = append(required, nested["required"].([]string)...)
			continue
		}
		tag := strings.Split(field.Tag.Get("json"), ",")
		name := tag[0]
		if name == "-" {
			continue
		}
		if name == "" {
			name = field.Name
		}
		schema := typeSchema(field.Type)
		meaning := fieldMeaning(t.Name(), name)
		schema["description"] = meaning.description
		if meaning.unit != "" {
			schema["x-unit"] = meaning.unit
		}
		if item, ok := schema["items"].(map[string]any); ok && meaning.item != "" {
			item["description"] = meaning.item
		}
		properties[name] = schema
		optional := false
		for _, option := range tag[1:] {
			if option == "omitempty" {
				optional = true
			}
		}
		if !optional {
			required = append(required, name)
		}
	}
	if t == reflect.TypeFor[flame.Frame]() {
		properties["category"].(map[string]any)["enum"] = categories
	}
	if t == reflect.TypeFor[EventRow]() {
		properties["kind"].(map[string]any)["enum"] = []string{"dispatch", "timer", "rAF", "microtask", "observer"}
	}
	if t == reflect.TypeFor[KeyRow]() {
		properties["op"].(map[string]any)["enum"] = []string{"metrics", "rootcause"}
	}
	if t == reflect.TypeFor[flame.DiffNormalization]() {
		properties["mode"].(map[string]any)["enum"] = []string{"none", "per-second", "share"}
		properties["unit"].(map[string]any)["enum"] = []string{"ms", "ms/s", "percentage-points"}
	}
	if t == reflect.TypeFor[flame.DiffWarning]() {
		properties["level"].(map[string]any)["enum"] = []string{"info", "warn"}
		properties["code"].(map[string]any)["enum"] = []string{"duration-mismatch", "sample-sparse", "normalization-unavailable", "duration-unavailable"}
	}
	return map[string]any{"type": "object", "properties": properties, "required": required, "additionalProperties": false}
}

func typeSchema(t reflect.Type) map[string]any {
	if t.Kind() == reflect.Pointer {
		return typeSchema(t.Elem())
	}
	if t == reflect.TypeFor[StackRef]() {
		return map[string]any{"description": "Root-to-leaf function reference, or the number of omitted middle frames.", "oneOf": []any{
			map[string]any{"type": "string", "pattern": "^f_", "description": "Canonical generated function ID in this result's functions dictionary."},
			map[string]any{"type": "object", "properties": map[string]any{"elided": map[string]any{"type": "integer", "minimum": 1, "description": "Number of omitted middle frames; retained roots and leaves are not included.", "x-unit": "frames"}}, "required": []string{"elided"}, "additionalProperties": false},
		}}
	}
	if t == reflect.TypeFor[diagnostic.Code]() {
		return map[string]any{"type": "string", "enum": errorCodes}
	}
	if t == reflect.TypeFor[flame.DiffMode]() {
		return map[string]any{"type": "string", "enum": []string{"auto", "none", "per-second", "share"}}
	}
	switch t.Kind() {
	case reflect.Struct:
		return objectSchema(t)
	case reflect.Slice, reflect.Array:
		return map[string]any{"type": "array", "items": typeSchema(t.Elem())}
	case reflect.Map:
		return map[string]any{"type": "object", "propertyNames": map[string]any{"type": "string", "pattern": "^f_", "description": "Canonical generated function ID, stable across sample order."}, "additionalProperties": typeSchema(t.Elem())}
	case reflect.Interface:
		return map[string]any{}
	case reflect.String:
		return map[string]any{"type": "string"}
	case reflect.Bool:
		return map[string]any{"type": "boolean"}
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64, reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		return map[string]any{"type": "integer"}
	case reflect.Float32, reflect.Float64:
		return map[string]any{"type": "number"}
	default:
		panic("query schema contains unsupported type: " + t.String())
	}
}

type meaning struct{ description, unit, item string }

func fieldMeaning(owner, name string) meaning {
	if value, ok := specificMeanings[owner+"."+name]; ok {
		return value
	}
	if value, ok := fieldMeanings[name]; ok {
		return value
	}
	panic("query schema lacks semantic meaning for " + owner + "." + name)
}

var specificMeanings = map[string]meaning{
	"Request.operation":          {"Operation selected by the CLI or MCP tool; window delegates to query_operation.", "", ""},
	"Request.limit":              {"Maximum returned rows before the byte bound; zero selects the default 50.", "rows", ""},
	"Request.max_bytes":          {"Actual rendered-byte budget, including metadata and escaping; zero selects 128 KiB, with a 4 MiB hard maximum.", "bytes", ""},
	"Request.depth":              {"Maximum subtree distance from the selected root; omitted uses eight and explicit zero keeps the selected root only.", "frames", ""},
	"Request.by":                 {"Rank top/find functions by self or inclusive sample weight; for diff rank by absolute normalized delta.", "", ""},
	"Request.selector":           {"Function ID, node ID, exact name, URL:line:column or /RE2 regex/ for function operations. Scalar operations also accept a trailing .* prefix; keys accepts a plain prefix.", "", ""},
	"Frame.name":                 {"Raw captured generated function name; empty names remain empty and anonymous is explicit.", "", ""},
	"Frame.url":                  {"Raw captured generated source URL, not a mapped original-source path.", "", ""},
	"Location.name":              {"Original declaration name when source-map evidence identifies one; never a guessed parameter name.", "", ""},
	"Location.url":               {"Mapped original source path or URL; unresolved relocated-map paths remain honest relative paths.", "", ""},
	"EventRow.name":              {"Recorded trigger name within its kind, such as timer ID or dispatched event name.", "", ""},
	"EventRow.total_ms":          {"Sum of recorded durations for this trigger group, not sampled function weight.", "ms", ""},
	"DiffRow.delta_self_ms":      {"Candidate minus baseline self weight after the resolved normalization; the unit comes from diff_normalization.unit, despite the historical _ms suffix.", "diff_normalization.unit", ""},
	"DiffRow.delta_total_ms":     {"Candidate minus baseline inclusive weight after the resolved normalization; the unit comes from diff_normalization.unit, despite the historical _ms suffix.", "diff_normalization.unit", ""},
	"PathRow.depth":              {"Complete path frame count for paths/stacks; subtree uses distance from the selected root instead.", "frames", ""},
	"HotPathRow.depth":           {"Zero-based distance from the supported hot-path root.", "frames", ""},
	"Source.untimed_samples":     {"Whole-recording positive-weight samples without usable timestamps, preserved even when a window excludes them.", "samples", ""},
	"Source.untimed_ms":          {"Whole-recording untimed statistical sample weight, not an inferred occupied interval.", "ms", ""},
	"UntimedSpan.samples":        {"Untimed observations in this export-order run; span bounds do not place their weight on the timeline.", "samples", ""},
	"UntimedSpan.ms":             {"Statistical weight of untimed samples in this run; not span wall duration.", "ms", ""},
	"DiffNormalization.unit":     {"Physical unit for normalized query delta columns; raw before/after values always remain milliseconds.", "", ""},
	"DiffNormalization.warnings": {"Comparison caveats from active-duration, sampling and denominator checks.", "", ""},
	"Error.candidates":           {"At most ten deterministic nearest scalar-key suggestions for unknown_key errors.", "", "Suggested canonical scalar key, ordered by shared prefix then edit distance."},
	"Document.candidates":        {"Ambiguous function selectors return candidate IDs, never an arbitrarily selected function.", "", "Function ID resolved through the result's functions dictionary."},
	"Document.rows":              {"Operation-specific rows; the operation-discriminated oneOf below defines their exact shape.", "", "Operation-specific row; see the matching operation branch."},
}

var fieldMeanings = map[string]meaning{
	"schema":                    {"Versioned query wire discriminator; v2 uses sparse operation rows and frame references.", "", ""},
	"operation":                 {"Executed query operation; window queries report their resolved underlying operation.", "", ""},
	"status":                    {"ok means the query completed; ambiguous requires choosing a candidate; error carries typed failures.", "", ""},
	"sources":                   {"Selected recording windows and preserved source-map and untimed-evidence diagnostics.", "", ""},
	"command":                   {"Literal reproducible CLI arguments, never an executable shell string.", "", "One literal CLI argument; preserve argument boundaries when replaying."},
	"selector":                  {"Selector used to choose functions, stacks, trigger groups or scalar keys.", "", ""},
	"by":                        {"Selected top/diff ranking measure: self weight or inclusive total weight.", "", ""},
	"sampled_total_ms":          {"Total selected statistical profiler sample weight, including untimed weight only for full-recording queries; not wall-clock duration.", "ms", ""},
	"total_count":               {"Matching rows before offset, row limit and byte trimming; ambiguous results count candidates instead.", "rows", ""},
	"returned_count":            {"Rows or ambiguity candidates actually retained in this response after every bound.", "rows", ""},
	"offset":                    {"Zero-based index of the first requested matching row.", "rows", ""},
	"next_offset":               {"Offset immediately after the retained prefix; absent when no matching rows remain.", "rows", ""},
	"limit":                     {"Applied row-count limit before actual-byte trimming.", "rows", ""},
	"max_bytes":                 {"Requested data/render budget in bytes; transports may additionally enforce a smaller envelope allowance.", "bytes", ""},
	"truncated":                 {"True when row limits, actual bytes or stack previews omit matching result detail.", "", ""},
	"truncated_by":              {"Applied omission bounds in stable order: limit, bytes, stack_frames. Stack elision alone does not create another page.", "", "One bound that omitted matching rows or detail."},
	"functions":                 {"Canonical captured frame metadata keyed by generated function ID; unused frames are pruned after every bound.", "", ""},
	"deepest_stack":             {"Root-to-leaf preview of the deterministically selected deepest stack.", "", ""},
	"deepest_stack_depth":       {"Complete deepest stack length before preview elision.", "frames", ""},
	"hot_path_branches":         {"Retained competing branches at the supported hot-path stopping point.", "", ""},
	"hot_path_branches_total":   {"Competing branches before row/detail byte trimming.", "branches", ""},
	"hot_path_stop_reason":      {"Why supported traversal stopped, such as low share, weak support, near tie or a leaf.", "", ""},
	"diff_normalization":        {"Resolved shared flame/query normalization with raw sample totals, active durations and comparison warnings.", "", ""},
	"errors":                    {"Typed failures with source paths recorded once, separately from their reason text.", "", ""},
	"file":                      {"Local recording path identifying this source or failure; never executed as a command.", "", ""},
	"from":                      {"Selected lower recording-relative wall-time bound, inclusive; timestamps determine sample membership.", "s", ""},
	"to":                        {"Selected upper recording-relative wall-time bound, exclusive; request zero means recording end.", "s", ""},
	"untimed_samples":           {"Untimed positive-weight sample count for the source recording.", "samples", ""},
	"untimed_ms":                {"Untimed statistical profiler weight retained as source-level evidence.", "ms", ""},
	"untimed_excluded":          {"True when a partial window excludes whole-recording untimed sample weight from attribution.", "", ""},
	"untimed_spans":             {"Export-order inferred span labels, never timestamps or permission to attribute untimed weight to a window.", "", ""},
	"source_maps":               {"Measured source-map coverage, missing bundle identities, resolver warnings and a heuristic minification hint.", "", ""},
	"mappedFrames":              {"Captured frame occurrences with an original location resolved from a matching source map.", "frames", ""},
	"unmappedFrames":            {"Captured frame occurrences without a resolved original location, including native frames.", "frames", ""},
	"missingMaps":               {"Generated bundle URLs lacking matching source maps when mapping evidence is needed.", "", "Captured generated bundle URL without a matching source map."},
	"warnings":                  {"Actionable source-map resolution warnings; warnings are preserved in every query format.", "", "One actionable resolver or comparison warning."},
	"minified":                  {"Heuristic hint that generated JavaScript appears minified, not proof about every short function name.", "", ""},
	"from_sec":                  {"Lower recording-relative export-order span label; not a measured timestamp for its untimed samples.", "s", ""},
	"to_sec":                    {"Upper recording-relative export-order span label; never used for sample attribution.", "s", ""},
	"ms":                        {"Statistical profiler sample weight in milliseconds.", "ms", ""},
	"id":                        {"Canonical generated function identity, stable across sample ordering and independent of source maps.", "", ""},
	"name":                      {"Captured function or event name; mapped declaration names live in original metadata.", "", ""},
	"url":                       {"Source location URL or path; generated and original locations remain separate.", "", ""},
	"line":                      {"One-based source line when known; zero or -1 may represent native or unknown source locations.", "source-line", ""},
	"column":                    {"One-based source column when known; zero or -1 may represent native or unknown source locations.", "source-column", ""},
	"sourceID":                  {"Raw captured source identity used to distinguish URL-less generated functions; not a mapped path.", "", ""},
	"native":                    {"True when the captured frame is native runtime code rather than a JavaScript source location.", "", ""},
	"anonymous":                 {"True when the captured generated frame had no declared function name.", "", ""},
	"category":                  {"Effective frame ownership: explicit library rules precede app rules; native, GC and injected owners remain fixed.", "", ""},
	"original":                  {"Source-map supplied original declaration location; it enriches but never changes generated identity.", "", ""},
	"function_id":               {"Canonical function reference into this result's functions dictionary.", "", ""},
	"node_id":                   {"Canonical calling-context path ID; recursive occurrences have different context identities.", "", ""},
	"self_ms":                   {"Statistical sample weight whose terminal frame is this function or context; zero is a genuine measured aggregate.", "ms", ""},
	"total_ms":                  {"Inclusive statistical sample weight traversing this function, edge or context; recursive functions count once per sample.", "ms", ""},
	"samples":                   {"Positive-weight profiler observations contributing to this aggregate, not estimated calls or measured executions.", "samples", ""},
	"percent":                   {"Inclusive weight as a percentage, not a fraction; callers/callees divide by selected function inclusive weight, other sampled rows by sampled_total_ms.", "%", ""},
	"depth":                     {"Complete sampled stack length for depth histograms; context row semantics are operation-specific.", "frames", ""},
	"stack":                     {"Root-to-leaf frame references with an explicit omitted-middle marker when previewed.", "", ""},
	"stack_depth":               {"Complete referenced stack length, including frames omitted from its preview.", "frames", ""},
	"baseline_self_ms":          {"Raw baseline sampled self weight before normalization; absent functions retain genuine zero.", "ms", ""},
	"candidate_self_ms":         {"Raw candidate sampled self weight before normalization; absent functions retain genuine zero.", "ms", ""},
	"baseline_total_ms":         {"Raw baseline sampled inclusive weight before normalization.", "ms", ""},
	"candidate_total_ms":        {"Raw candidate sampled inclusive weight before normalization.", "ms", ""},
	"delta_samples":             {"Candidate observation count minus baseline observation count; this signed count is not time-normalized.", "samples", ""},
	"key":                       {"Canonical scalar key; function keys use functions.f_ID rather than encoded raw-frame composites.", "", ""},
	"value":                     {"Numeric scalar result, including genuine zero; its physical unit is supplied by this row's unit property.", "row.unit", ""},
	"unit":                      {"Physical unit for the scalar key, such as ms, s, count, ratio, %, fps, bytes or count/s; ratio need not be bounded by one.", "", ""},
	"basis":                     {"Evidence basis supplied by the scalar or trigger computation; distinguishes timeline measurement from statistical sample attribution.", "", ""},
	"op":                        {"Scalar query operation that owns this discoverable key.", "", ""},
	"description":               {"Key-specific semantic explanation, including what is counted or attributed.", "", ""},
	"alias_of":                  {"Canonical scalar key sharing the exact value and rounding of this retained alias.", "", ""},
	"kind":                      {"Instrumented trigger kind used to group callback records; garbage collection is not a trigger row.", "", ""},
	"count":                     {"Recorded trigger occurrences in this group, not profiler sample count.", "events", ""},
	"mean_ms":                   {"Arithmetic mean of recorded trigger durations in this group.", "ms", ""},
	"p95_ms":                    {"95th-percentile recorded trigger duration using the shared deterministic percentile calculation.", "ms", ""},
	"max_ms":                    {"Longest recorded trigger duration in this group.", "ms", ""},
	"top_self":                  {"Retained functions ranked by sampled self weight attributed to this trigger group.", "", ""},
	"top_inclusive":             {"Retained functions ranked by sampled inclusive weight attributed to this trigger group.", "", ""},
	"top_self_total_count":      {"Distinct self-attributed functions before nested list limits and byte trimming.", "functions", ""},
	"top_inclusive_total_count": {"Distinct inclusive-attributed functions before nested list limits and byte trimming.", "functions", ""},
	"functions_truncated":       {"True when either trigger function list omits matching functions.", "", ""},
	"self_samples":              {"Positive-weight profiler observations terminating in this function.", "samples", ""},
	"requestedMode":             {"Requested diff normalization; auto chooses per-second only for a known active-duration mismatch greater than 10%.", "", ""},
	"mode":                      {"Resolved normalization actually applied to deltas; unavailable denominators force raw none with a warning.", "", ""},
	"beforeTotalMs":             {"Raw baseline selected statistical sample weight, before diff normalization.", "ms", ""},
	"afterTotalMs":              {"Raw candidate selected statistical sample weight, before diff normalization.", "ms", ""},
	"beforeActiveMs":            {"Baseline selected active recording duration, excluding known paused intervals.", "ms", ""},
	"afterActiveMs":             {"Candidate selected active recording duration, excluding known paused intervals.", "ms", ""},
	"level":                     {"Comparison warning severity: info explains an applied normalization, warn flags limited evidence or unavailable denominators.", "", ""},
	"code":                      {"Stable failure category used to decide whether to fix input, loading, mapping, window, selector, key or usage.", "", ""},
	"message":                   {"Human-readable reason without repeating the separately recorded source file path.", "", ""},
	"query_operation":           {"Underlying operation for a window query; nested windows are rejected.", "", ""},
	"baseline":                  {"Baseline local recording path for diff; defaults to file.", "", ""},
	"candidate":                 {"Candidate local recording path for diff.", "", ""},
	"module":                    {"Case-sensitive substring filter on effective mapped-or-generated source module paths.", "", ""},
	"max_stack_frames":          {"Maximum retained stack frames; omitted uses root eight plus leaf forty, while explicit zero requests the exact full stack if the byte budget allows it.", "frames", ""},
	"normalize":                 {"Shared diff normalization: auto, raw none, active-time per-second, or percentage-point share of sampled totals.", "", ""},
	"top":                       {"Root-cause detail size independent of returned row limit; zero selects ten.", "rows", ""},
	"min_ms":                    {"Minimum inclusive statistical sample weight for retained subtree contexts.", "ms", ""},
	"min_share":                 {"Hot-path branch support fraction of sampled total; omitted uses 0.05 and explicit zero disables share gating.", "ratio", ""},
	"min_samples":               {"Hot-path sample support floor; omitted uses five and explicit zero disables sample gating.", "samples", ""},
	"tie_ratio":                 {"Stop before second-best/best branch weight ratio reaches this threshold; zero disables near-tie stopping.", "ratio", ""},
	"long_task_ms":              {"Configured script-task finding threshold; zero selects 50 ms. Fixed scorecard long-task standards remain 50 ms.", "ms", ""},
	"jank_ms":                   {"Configured slow-frame finding threshold; zero selects 16.7 ms. Fixed frames.* scorecard standards remain 16.7 ms.", "ms", ""},
	"trim_start":                {"Additional active query lower bound relative to recording start; combines with from using the later bound.", "s", ""},
	"trim_end":                  {"Wall-time amount removed from recording end before applying an explicit upper bound.", "s", ""},
	"app_url":                   {"Whole-URL ownership globs applied to generated and original locations; ** crosses path separators.", "", "One application URL glob; matching library rules take precedence."},
	"library_url":               {"Whole-URL library ownership globs with precedence over application globs; native, GC and injected categories stay fixed.", "", "One library URL glob; ** crosses path separators."},
	"sourcemaps":                {"Explicit local source-map files or directories, distinct from returned source_maps diagnostics.", "", "One source-map file or build-output directory; explicit bad files fail, unsupported directory maps warn and skip."},
}
