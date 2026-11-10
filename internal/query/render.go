package query

import (
	"encoding/csv"
	"encoding/json"
	"fmt"
	"io"
	"strconv"
	"strings"
)

func Render(w io.Writer, result *Result, format string) error {
	if format == "json" {
		enc := json.NewEncoder(w)
		enc.SetEscapeHTML(false)
		enc.SetIndent("", "  ")
		return enc.Encode(result)
	}
	if format != "md" && format != "tsv" {
		return fmt.Errorf("unknown format %q (want json|md|tsv)", format)
	}
	headers, records := table(result)
	functionsTruncated := false
	for _, row := range result.Rows {
		if row.FunctionsTruncated {
			functionsTruncated = true
			break
		}
	}
	if format == "md" {
		if _, err := fmt.Fprintf(w, "# Query: %s\n\nReproduce: `%s`\n\nReturned %d of %d rows.\n\n", result.Operation, strings.ReplaceAll(CommandString(result.Command), "`", "\\`"), len(records), result.TotalCount); err != nil {
			return err
		}
		if result.Status == "ambiguous" {
			if _, err := fmt.Fprint(w, "Ambiguous selector. Choose an ID from these candidates.\n\n"); err != nil {
				return err
			}
		}
		writeLine := func(fields []string) error {
			escaped := make([]string, len(fields))
			for i, field := range fields {
				escaped[i] = strings.ReplaceAll(strings.ReplaceAll(strings.ReplaceAll(field, "\\", "\\\\"), "|", "\\|"), "\n", " ")
			}
			_, err := fmt.Fprintf(w, "| %s |\n", strings.Join(escaped, " | "))
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
		if result.Truncated {
			if _, err := fmt.Fprintf(w, "\n**TRUNCATED: returned %d of %d; limit=%d.**\n", len(records), result.TotalCount, result.Limit); err != nil {
				return err
			}
		}
		if functionsTruncated {
			if _, err := fmt.Fprintf(w, "\n**TRUNCATED: trigger function lists use limit=%d; full counts appear in each row.**\n", result.Limit); err != nil {
				return err
			}
		}
		if result.HotPathStopReason != "" {
			if _, err := fmt.Fprintf(w, "\nHot path stop: %s. Competing branches: %d of %d shown.\n", result.HotPathStopReason, len(result.HotPathBranches), result.HotPathBranchesTotal); err != nil {
				return err
			}
			for _, branch := range result.HotPathBranches {
				if _, err := fmt.Fprintf(w, "- %s @ %s: %s ms inclusive (%s%%), %d samples.\n", functionName(branch.Function), location(branch.Function), number(branch.TotalMS), number(branch.Percent), branch.Samples); err != nil {
					return err
				}
			}
		}
		if len(result.DeepestStack) > 0 {
			names := make([]string, len(result.DeepestStack))
			for i, f := range result.DeepestStack {
				names[i] = functionName(f)
			}
			if _, err := fmt.Fprintf(w, "\nDeepest stack: %s\n", strings.Join(names, " → ")); err != nil {
				return err
			}
		}
		return nil
	}
	writer := csv.NewWriter(w)
	writer.Comma = '\t'
	if err := writer.Write([]string{"# schema", result.Schema}); err != nil {
		return err
	}
	if err := writer.Write([]string{"# reproduce", CommandString(result.Command)}); err != nil {
		return err
	}
	if err := writer.Write([]string{"# status", result.Status, "total_count", strconv.Itoa(result.TotalCount), "limit", strconv.Itoa(result.Limit), "truncated", strconv.FormatBool(result.Truncated)}); err != nil {
		return err
	}
	if err := writer.Write(headers); err != nil {
		return err
	}
	if err := writer.WriteAll(records); err != nil {
		return err
	}
	if result.Truncated {
		if err := writer.Write([]string{"# TRUNCATED", fmt.Sprintf("returned %d of %d; limit=%d", len(records), result.TotalCount, result.Limit)}); err != nil {
			return err
		}
	}
	if functionsTruncated {
		if err := writer.Write([]string{"# TRUNCATED", fmt.Sprintf("trigger function lists use limit=%d; full counts appear in each row", result.Limit)}); err != nil {
			return err
		}
	}
	if result.HotPathStopReason != "" {
		if err := writer.Write([]string{"# hot_path_stop", result.HotPathStopReason, "branches_shown", strconv.Itoa(len(result.HotPathBranches)), "branches_total", strconv.Itoa(result.HotPathBranchesTotal)}); err != nil {
			return err
		}
		for _, branch := range result.HotPathBranches {
			if err := writer.Write([]string{"# branch", branch.ID, functionName(branch.Function), location(branch.Function), number(branch.TotalMS), number(branch.Percent), strconv.Itoa(branch.Samples)}); err != nil {
				return err
			}
		}
	}
	if len(result.DeepestStack) > 0 {
		record := []string{"# deepest_stack"}
		for _, f := range result.DeepestStack {
			record = append(record, f.ID+" "+functionName(f))
		}
		if err := writer.Write(record); err != nil {
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
	if result.Operation == "metrics" || result.Operation == "rootcause" {
		for _, row := range result.Rows {
			rows = append(rows, []string{row.Key, number(row.Value), row.Unit})
		}
		return []string{"key", "value", "unit"}, rows
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
	for _, row := range result.Rows {
		names := make([]string, len(row.Stack))
		for i, f := range row.Stack {
			names[i] = functionName(f) + "@" + location(f)
		}
		rows = append(rows, []string{row.ID, row.NodeID, functionName(row.Function), location(row.Function), number(row.SelfMS), number(row.TotalMS), number(row.Percent), strconv.Itoa(row.Samples), strconv.Itoa(row.Depth), strings.Join(names, " → ")})
	}
	return []string{"id", "node_id", "name", "source", "self_ms", "total_ms", "percent", "samples", "depth", "stack"}, rows
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
