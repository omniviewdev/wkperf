package query

import (
	"encoding/csv"
	"encoding/json"
	"fmt"
	"io"
	"strconv"
	"strings"
)

// Render writes only after the complete representation fits its byte budget.
func Render(w io.Writer, result *Result, format string) error {
	_, data, err := Prepare(result, format)
	if err != nil {
		return err
	}
	_, err = w.Write(data)
	return err
}

func renderUnbounded(w io.Writer, result *Result, format string) error {
	if format == "json" {
		encoder := json.NewEncoder(w)
		encoder.SetEscapeHTML(false)
		return encoder.Encode(Wire(result))
	}
	headers, records := table(result)
	if format == "md" {
		if _, err := fmt.Fprintf(w, "# Query: %s\n\n", result.Operation); err != nil {
			return err
		}
		if len(result.Errors) > 0 {
			if _, err := fmt.Fprint(w, "## Errors\n\n"); err != nil {
				return err
			}
			for _, failure := range result.Errors {
				if _, err := fmt.Fprintf(w, "- %s: %s: %s\n", codeSpan(failure.File), failure.Code, singleLine(failure.Message)); err != nil {
					return err
				}
			}
			if _, err := fmt.Fprint(w, "\n"); err != nil {
				return err
			}
		}
		if _, err := fmt.Fprintf(w, "Schema: %s\n\nReproduce: %s\n\nReturned %d of %d rows; offset=%d; limit=%d; max_bytes=%d.\n\n", result.Schema, codeSpan(CommandString(result.Command)), result.ReturnedCount, result.TotalCount, result.Offset, result.Limit, result.MaxBytes); err != nil {
			return err
		}
		for _, source := range result.Sources {
			if _, err := fmt.Fprintf(w, "- Source: %s; window %s–%s s; untimed samples=%d (%s ms), excluded=%t.\n", codeSpan(source.File), number(source.From), number(source.To), source.UntimedSamples, number(source.UntimedMs), source.UntimedExcluded); err != nil {
				return err
			}
			diagnostics := source.SourceMaps
			if _, err := fmt.Fprintf(w, "  Source maps: mapped=%d, unmapped=%d, minified=%t; missing=%s.\n", diagnostics.MappedFrames, diagnostics.UnmappedFrames, diagnostics.Minified, codeSpan(strings.Join(diagnostics.MissingMaps, ", "))); err != nil {
				return err
			}
			for _, warning := range diagnostics.Warnings {
				if _, err := fmt.Fprintf(w, "  Warning: %s\n", singleLine(warning)); err != nil {
					return err
				}
			}
		}
		if result.DiffNormalization != nil {
			n := result.DiffNormalization
			if _, err := fmt.Fprintf(w, "\nNormalization: %s (%s); raw baseline=%s ms, candidate=%s ms; active baseline=%s ms, candidate=%s ms.\n", n.Mode, n.Unit, number(n.BeforeTotalMs), number(n.AfterTotalMs), number(n.BeforeActiveMs), number(n.AfterActiveMs)); err != nil {
				return err
			}
			for _, warning := range n.Warnings {
				if _, err := fmt.Fprintf(w, "- %s: %s\n", warning.Code, singleLine(warning.Message)); err != nil {
					return err
				}
			}
		}
		if result.Status == "ambiguous" {
			if _, err := fmt.Fprint(w, "\nAmbiguous selector. Choose an ID from these candidates.\n"); err != nil {
				return err
			}
		}
		writeLine := func(fields []string) error {
			escaped := make([]string, len(fields))
			for i, field := range fields {
				escaped[i] = strings.ReplaceAll(strings.ReplaceAll(singleLine(field), "\\", "\\\\"), "|", "\\|")
			}
			_, err := fmt.Fprintf(w, "| %s |\n", strings.Join(escaped, " | "))
			return err
		}
		if _, err := fmt.Fprint(w, "\n"); err != nil {
			return err
		}
		if err := writeLine(headers); err != nil {
			return err
		}
		separator := make([]string, len(headers))
		for i := range separator {
			separator[i] = "---"
		}
		if err := writeLine(separator); err != nil {
			return err
		}
		for _, record := range records {
			if err := writeLine(record); err != nil {
				return err
			}
		}
		if result.HotPathStopReason != "" {
			if _, err := fmt.Fprintf(w, "\nHot path stop: %s. Competing branches: %d of %d shown.\n", result.HotPathStopReason, len(result.HotPathBranches), result.HotPathBranchesTotal); err != nil {
				return err
			}
			for _, branch := range result.HotPathBranches {
				if _, err := fmt.Fprintf(w, "- %s @ %s: %s ms inclusive (%s%%), %d samples.\n", singleLine(functionName(branch.Function)), singleLine(location(branch.Function)), number(branch.TotalMS), number(branch.Percent), branch.Samples); err != nil {
					return err
				}
			}
		}
		if len(result.DeepestStack) > 0 {
			if _, err := fmt.Fprintf(w, "\nDeepest stack (%d frames): %s\n", len(result.DeepestStack), singleLine(stackLabels(result.DeepestStack, stackLimit(result)))); err != nil {
				return err
			}
		}
		if result.Truncated {
			next := ""
			if result.NextOffset != nil {
				next = fmt.Sprintf("; next_offset=%d", *result.NextOffset)
			}
			if _, err := fmt.Fprintf(w, "\n**TRUNCATED: returned %d of %d; offset=%d; by=%s%s.**\n", result.ReturnedCount, result.TotalCount, result.Offset, strings.Join(result.TruncatedBy, ","), next); err != nil {
				return err
			}
		}
		return nil
	}
	writer := csv.NewWriter(w)
	writer.Comma = '\t'
	write := func(fields ...string) error { return writer.Write(fields) }
	for _, failure := range result.Errors {
		if err := write("# error", failure.File, string(failure.Code), failure.Message); err != nil {
			return err
		}
	}
	if err := write("# schema", result.Schema); err != nil {
		return err
	}
	if err := write("# reproduce", CommandString(result.Command)); err != nil {
		return err
	}
	if err := write("# status", result.Status, "total_count", strconv.Itoa(result.TotalCount), "returned_count", strconv.Itoa(result.ReturnedCount), "offset", strconv.Itoa(result.Offset), "limit", strconv.Itoa(result.Limit), "max_bytes", strconv.Itoa(result.MaxBytes), "truncated", strconv.FormatBool(result.Truncated), "truncated_by", strings.Join(result.TruncatedBy, ",")); err != nil {
		return err
	}
	if result.NextOffset != nil {
		if err := write("# next_offset", strconv.Itoa(*result.NextOffset)); err != nil {
			return err
		}
	}
	for _, source := range result.Sources {
		if err := write("# source", source.File, "from_s", number(source.From), "to_s", number(source.To), "untimed_samples", strconv.Itoa(source.UntimedSamples), "untimed_ms", number(source.UntimedMs), "untimed_excluded", strconv.FormatBool(source.UntimedExcluded)); err != nil {
			return err
		}
		d := source.SourceMaps
		if err := write("# source_maps", source.File, "mapped_frames", strconv.Itoa(d.MappedFrames), "unmapped_frames", strconv.Itoa(d.UnmappedFrames), "minified", strconv.FormatBool(d.Minified), "missing_maps", strings.Join(d.MissingMaps, "; ")); err != nil {
			return err
		}
		for _, warning := range d.Warnings {
			if err := write("# source_map_warning", source.File, warning); err != nil {
				return err
			}
		}
	}
	if result.DiffNormalization != nil {
		n := result.DiffNormalization
		if err := write("# normalization", string(n.Mode), "unit", n.Unit, "baseline_total_ms", number(n.BeforeTotalMs), "candidate_total_ms", number(n.AfterTotalMs), "baseline_active_ms", number(n.BeforeActiveMs), "candidate_active_ms", number(n.AfterActiveMs)); err != nil {
			return err
		}
		for _, warning := range n.Warnings {
			if err := write("# warning", warning.Code, warning.Message); err != nil {
				return err
			}
		}
	}
	if err := writer.Write(headers); err != nil {
		return err
	}
	if err := writer.WriteAll(records); err != nil {
		return err
	}
	if result.HotPathStopReason != "" {
		if err := write("# hot_path_stop", result.HotPathStopReason, "branches_shown", strconv.Itoa(len(result.HotPathBranches)), "branches_total", strconv.Itoa(result.HotPathBranchesTotal)); err != nil {
			return err
		}
		for _, branch := range result.HotPathBranches {
			if err := write("# branch", branch.ID, functionName(branch.Function), location(branch.Function), number(branch.TotalMS), number(branch.Percent), strconv.Itoa(branch.Samples)); err != nil {
				return err
			}
		}
	}
	if len(result.DeepestStack) > 0 {
		if err := write("# deepest_stack", "depth", strconv.Itoa(len(result.DeepestStack)), stackLabels(result.DeepestStack, stackLimit(result))); err != nil {
			return err
		}
	}
	if result.Truncated {
		if err := write("# TRUNCATED", "returned_count", strconv.Itoa(result.ReturnedCount), "total_count", strconv.Itoa(result.TotalCount), "truncated_by", strings.Join(result.TruncatedBy, ",")); err != nil {
			return err
		}
	}
	writer.Flush()
	return writer.Error()
}

func table(result *Result) ([]string, [][]string) {
	rows := [][]string{}
	if result.Status == "ambiguous" {
		for _, f := range result.Candidates {
			rows = append(rows, []string{f.ID, functionName(f), location(f), f.Category})
		}
		return []string{"id", "name", "source", "category"}, rows
	}
	if result.Operation == "keys" {
		for _, row := range result.Rows {
			rows = append(rows, []string{row.Key, row.Operation, row.Unit, row.Description, row.Basis, row.AliasOf})
		}
		return []string{"key", "op", "unit", "description", "basis", "alias_of"}, rows
	}
	if result.Operation == "metrics" {
		for _, row := range result.Rows {
			rows = append(rows, []string{row.Key, number(row.Value), row.Unit})
		}
		return []string{"key", "value", "unit"}, rows
	}
	if result.Operation == "rootcause" {
		for _, row := range result.Rows {
			rows = append(rows, []string{row.Key, number(row.Value), row.Unit, row.Basis})
		}
		return []string{"key", "value", "unit", "basis"}, rows
	}
	if result.Operation == "events" {
		for _, row := range result.Rows {
			rows = append(rows, []string{row.Kind, row.Name, strconv.Itoa(row.Count), number(row.TotalMS), number(row.MeanMS), number(row.P95MS), number(row.MaxMS), costLabels(row.TopSelf), strconv.Itoa(row.TopSelfTotalCount), costLabels(row.TopInclusive), strconv.Itoa(row.TopInclusiveTotalCount), strconv.FormatBool(row.FunctionsTruncated)})
		}
		return []string{"kind", "name", "count", "total_ms", "mean_ms", "p95_ms", "max_ms", "top_self", "top_self_total_count", "top_inclusive", "top_inclusive_total_count", "functions_truncated"}, rows
	}
	if result.Operation == "diff" {
		for _, row := range result.Rows {
			rows = append(rows, []string{row.ID, functionName(row.Function), location(row.Function), number(row.BaselineSelfMS), number(row.CandidateSelfMS), number(row.DeltaSelfMS), number(row.BaselineTotalMS), number(row.CandidateTotalMS), number(row.DeltaTotalMS), strconv.Itoa(row.Samples)})
		}
		return []string{"id", "name", "source", "baseline_self_ms", "candidate_self_ms", "delta_self_ms", "baseline_total_ms", "candidate_total_ms", "delta_total_ms", "delta_samples"}, rows
	}
	if result.Operation == "depth" {
		for _, row := range result.Rows {
			rows = append(rows, []string{strconv.Itoa(row.Depth), number(row.SelfMS), number(row.TotalMS), strconv.Itoa(row.Samples), number(row.Percent)})
		}
		return []string{"depth", "self_ms", "total_ms", "samples", "percent"}, rows
	}
	headers := []string{"id", "node_id", "name", "source"}
	if result.Operation != "callers" && result.Operation != "callees" {
		headers = append(headers, "self_ms")
	}
	headers = append(headers, "total_ms", "percent", "samples")
	path := result.Operation == "paths" || result.Operation == "stacks" || result.Operation == "subtree"
	if path || result.Operation == "hot-path" {
		headers = append(headers, "depth")
	}
	if path {
		headers = append(headers, "stack_depth", "stack")
	}
	for _, row := range result.Rows {
		fields := []string{row.ID, row.NodeID, functionName(row.Function), location(row.Function)}
		if result.Operation != "callers" && result.Operation != "callees" {
			fields = append(fields, number(row.SelfMS))
		}
		fields = append(fields, number(row.TotalMS), number(row.Percent), strconv.Itoa(row.Samples))
		if path || result.Operation == "hot-path" {
			fields = append(fields, strconv.Itoa(row.Depth))
		}
		if path {
			fields = append(fields, strconv.Itoa(len(row.Stack)), stackLabels(row.Stack, stackLimit(result)))
		}
		rows = append(rows, fields)
	}
	return headers, rows
}

func location(f Function) string {
	generated := fmt.Sprintf("%s:%d:%d", f.URL, f.Line, f.Column)
	if f.Original != nil {
		generated += fmt.Sprintf(" → %s %s:%d:%d", f.Original.Name, f.Original.URL, f.Original.Line, f.Original.Column)
	}
	return generated
}
func number(value float64) string { return strconv.FormatFloat(value, 'g', -1, 64) }

func costLabels(costs []FunctionCost) string {
	labels := make([]string, len(costs))
	for i, cost := range costs {
		labels[i] = functionName(cost.Function) + "@" + location(cost.Function) + " (self=" + number(cost.SelfMS) + "ms, total=" + number(cost.TotalMS) + "ms, samples=" + strconv.Itoa(cost.SelfSamples) + ", category=" + cost.Category + ")"
	}
	return strings.Join(labels, "; ")
}

// CommandString quotes arguments for display without invoking a shell.
func CommandString(args []string) string {
	quoted := make([]string, len(args))
	for i, arg := range args {
		if arg != "" && !strings.ContainsAny(arg, " \t\r\n'\"`$\\;|&()<>{}[]*?!#~") {
			quoted[i] = arg
		} else {
			quoted[i] = "'" + strings.ReplaceAll(arg, "'", "'\"'\"'") + "'"
		}
	}
	return strings.Join(quoted, " ")
}

func functionName(f Function) string {
	if f.Name == "" {
		return "(anonymous)"
	}
	return f.Name
}

func stackLabels(frames []Function, limit int) string {
	head, tail, omitted := previewFrames(frames, limit)
	size := len(head) + len(tail)
	if omitted > 0 {
		size++
	}
	labels := make([]string, 0, size)
	for _, f := range head {
		labels = append(labels, functionName(f)+"@"+location(f))
	}
	if omitted > 0 {
		labels = append(labels, fmt.Sprintf("… %d frames elided …", omitted))
	}
	for _, f := range tail {
		labels = append(labels, functionName(f)+"@"+location(f))
	}
	return strings.Join(labels, " → ")
}

var lineEscaper = strings.NewReplacer("\r", " ", "\n", " ")

func singleLine(value string) string {
	return lineEscaper.Replace(value)
}

func codeSpan(value string) string {
	value = singleLine(value)
	longest, run := 0, 0
	for _, ch := range value {
		if ch == '`' {
			run++
			longest = max(longest, run)
		} else {
			run = 0
		}
	}
	fence := strings.Repeat("`", longest+1)
	if strings.HasPrefix(value, "`") || strings.HasSuffix(value, "`") {
		return fence + " " + value + " " + fence
	}
	return fence + value + fence
}
