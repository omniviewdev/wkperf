package compare

import (
	"fmt"
	"html"
	"io"
	"strings"

	"github.com/omniviewdev/wkperf/internal/metrics"
)

// RenderMarkdown writes a concise comparison, including its verdict and fixture
// comparability. Each metric keeps its own verdict and confidence.
func RenderMarkdown(w io.Writer, r *Result) error {
	if r == nil {
		r = &Result{}
	}
	var b strings.Builder
	verdict := Unchanged
	for _, m := range r.Metrics {
		if m.Verdict == Improved && verdict == Unchanged {
			verdict = Improved
		}
		if m.Verdict == Regressed && m.Confidence != "low" {
			verdict = Regressed
		}
	}
	fmt.Fprintf(&b, "# wkperf comparison\n\n- Verdict: **%s** (%d regressed, %d improved, %d noisy, %d unchanged).\n", verdict, r.Counts.Regressed, r.Counts.Improved, r.Counts.Noisy, r.Counts.Unchanged)
	fmt.Fprintf(&b, "- Baseline: %d run(s); %s\n- Candidate: %d run(s); %s\n", r.Baseline.Runs, markdownText(strings.Join(r.Baseline.Sources, ", ")), r.Cand.Runs, markdownText(strings.Join(r.Cand.Sources, ", ")))
	fmt.Fprintf(&b, "- Comparability: **%.2f** (1 = matching fixture)", r.Comparability.Score)
	if r.Comparability.Normalized {
		b.WriteString("; extensive metrics and function times normalized per second")
	}
	b.WriteByte('\n')
	for _, warning := range r.Comparability.Warnings {
		fmt.Fprintf(&b, "- [%s] %s: %s\n", markdownText(warning.Level), markdownText(warning.Code), markdownText(warning.Message))
	}
	if len(r.Metrics) == 0 {
		b.WriteString("\n_No comparison metrics._\n")
	} else {
		b.WriteString("\n## Metrics\n\n| Metric key | Baseline | Candidate | Delta | Verdict | Confidence |\n|---|---:|---:|---:|---|---|\n")
		shown := 0
		for _, m := range r.Metrics {
			if m.Verdict == Unchanged || m.Verdict == Info {
				continue
			}
			if shown == 10 {
				break
			}
			unit := string(m.Metric.Unit)
			if r.Comparability.Normalized && m.Metric.Extensive {
				unit += "/s"
			}
			fmt.Fprintf(&b, "| %s | %.2f %s | %.2f %s | %+.2f | %s | %s |\n", markdownText(m.Metric.Key), m.Base, unit, m.Cand, unit, m.Delta, m.Verdict, markdownText(m.Confidence))
			shown++
		}
		if remaining := len(r.Metrics) - shown; remaining > 0 {
			fmt.Fprintf(&b, "\n%d unchanged, informational, or additional metrics omitted; use --format json for all metrics.\n", remaining)
		}
	}
	unit := "ms"
	if r.Comparability.Normalized {
		unit = "ms/s"
	}
	for _, list := range []struct {
		title string
		rows  []FuncDiff
	}{{"More function self time", r.FuncRegressed}, {"Less function self time", r.FuncImproved}} {
		if len(list.rows) == 0 {
			continue
		}
		fmt.Fprintf(&b, "\n## %s\n\n| Function / ID | Generated → original location | Baseline self %s | Candidate self %s | Baseline inclusive %s | Candidate inclusive %s | Verdict | Common stack preview | Retrieve exact stack |\n|---|---|---:|---:|---:|---:|---|---|---|\n", list.title, unit, unit, unit, unit)
		for _, f := range list.rows {
			name := f.Name
			if name == "" {
				name = "(anonymous)"
			}
			if f.ID != "" {
				name += " [" + f.ID + "]"
			}
			source := f.CommonStackSource
			if source == "" {
				sources := r.Cand.Sources
				if f.Verdict == Removed {
					sources = r.Baseline.Sources
				}
				if len(sources) > 0 {
					source = sources[0]
				}
			}
			f.CommonStackSource = source
			command := f.StackCommand()
			stack := ""
			if len(f.CommonStack) > 8 && command != "" {
				stack = stackPreview(f.CommonStack)
			} else {
				stack = f.StackLabel()
			}
			fmt.Fprintf(&b, "| %s | %s | %.2f | %.2f | %.2f | %.2f | %s | %s | <code style=\"white-space:pre-wrap\">%s</code> |\n", markdownText(name), markdownText(f.Location()), f.BaseSelf, f.CandSelf, f.BaseTotal, f.CandTotal, f.Verdict, markdownText(stack), commandText(command))
		}
	}
	_, err := io.WriteString(w, b.String())
	return err
}

var markdownEscaper = strings.NewReplacer("|", "\\|", "`", "'", "\n", " ", "\r", " ")
var commandEscaper = strings.NewReplacer("|", "&#124;", "`", "&#96;", "\n", "&#10;", "\r", "&#13;")

func markdownText(s string) string {
	return markdownEscaper.Replace(html.EscapeString(s))
}

func commandText(s string) string {
	return commandEscaper.Replace(html.EscapeString(s))
}

func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", "'\\''") + "'"
}

// StackCommand retrieves the exact unabridged stack from its original recording.
func (f FuncDiff) StackCommand() string {
	if f.CommonStackSource == "" || f.CommonStackID == "" {
		return ""
	}
	return "wkperf query " + shellQuote(f.CommonStackSource) + " stacks " + shellQuote(f.CommonStackID) + " --format json"
}

func stackPreview(frames []metrics.FunctionFrame) string {
	var b strings.Builder
	for i, frame := range frames {
		if i >= 4 && i < len(frames)-4 {
			if i == 4 {
				fmt.Fprintf(&b, " → [%d frames omitted; exact stack command or --format json retrieves all frames]", len(frames)-8)
			}
			continue
		}
		if i > 0 {
			b.WriteString(" → ")
		}
		name := frame.Name
		if name == "" {
			name = "(anonymous)"
		}
		b.WriteString(name + " @ " + frame.Location())
	}
	return b.String()
}

// Location retains generated identity even when an original source is known.
func (f FuncDiff) Location() string {
	return (metrics.FunctionFrame{Name: f.Name, URL: f.URL, Line: f.Line, Column: f.Column, SourceID: f.SourceID, Original: f.Original}).Location()
}

// StackLabel keeps the full exact common stack in root-to-leaf order.
func (f FuncDiff) StackLabel() string {
	var b strings.Builder
	for i, frame := range f.CommonStack {
		if i > 0 {
			b.WriteString(" → ")
		}
		name := frame.Name
		if name == "" {
			name = "(anonymous)"
		}
		b.WriteString(name + " @ " + frame.Location())
	}
	return b.String()
}
