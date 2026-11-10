package insights

import (
	"fmt"
	"io"
	"strings"
	"unicode/utf8"
)

const termWidth = 100

type painter struct{ on bool }

func (p painter) wrap(code, s string) string {
	if !p.on {
		return s
	}
	return "\x1b[" + code + "m" + s + "\x1b[0m"
}
func (p painter) bold(s string) string { return p.wrap("1", s) }
func (p painter) dim(s string) string  { return p.wrap("2", s) }
func (p painter) badge(sev Severity) string {
	label := " " + strings.ToUpper(string(sev)) + " "
	switch sev {
	case SevHigh:
		return p.wrap("1;97;41", label)
	case SevMedium:
		return p.wrap("1;30;43", label)
	case SevLow:
		return p.wrap("1;30;46", label)
	}
	return p.wrap("1;30;47", label)
}

func bar(pct float64, width int) string {
	if pct < 0 {
		pct = 0
	}
	if pct > 100 {
		pct = 100
	}
	eighths := int(pct/100*float64(width)*8 + 0.5)
	full, rem := eighths/8, eighths%8
	partial := []string{"", "▏", "▎", "▍", "▌", "▋", "▊", "▉"}
	s := strings.Repeat("█", full) + partial[rem]
	return s + strings.Repeat(" ", width-utf8.RuneCountInString(s))
}

func trunc(s string, n int) string {
	if utf8.RuneCountInString(s) <= n {
		return s
	}
	r := []rune(s)
	return string(r[:n-1]) + "…"
}

// wrapText word-wraps s to width columns.
func wrapText(s string, width int) []string {
	var lines []string
	cur := ""
	for _, w := range strings.Fields(s) {
		if cur != "" && utf8.RuneCountInString(cur)+1+utf8.RuneCountInString(w) > width {
			lines = append(lines, cur)
			cur = w
			continue
		}
		if cur == "" {
			cur = w
		} else {
			cur += " " + w
		}
	}
	if cur != "" {
		lines = append(lines, cur)
	}
	return lines
}

func hotLabel(name, url string, line int) string {
	return funcLabel(modelFrame(name, url, line))
}

// RenderTerm writes a terminal report (~100 columns). color enables ANSI.
func RenderTerm(w io.Writer, r *Report, color bool) error {
	p := painter{color}
	var b strings.Builder
	ln := func(f string, a ...any) { fmt.Fprintf(&b, f+"\n", a...) }
	sec := func(t string) {
		b.WriteString("\n" + p.bold(t) + "\n" + p.dim(strings.Repeat("─", termWidth)) + "\n")
	}

	ln("%s", p.bold("wkperf profile: "+r.Name))
	ln("%s", p.dim(fmt.Sprintf("source %s · %.2fs recording", r.Source, r.DurationSec)))

	sec("Wall-time breakdown")
	for _, s := range r.Breakdown {
		ln("  %-10s %s %5.1f%% %9.1fms", s.Name, bar(s.Pct, 40), s.Pct, s.Ms)
	}

	sec("Top hotspots (self time)")
	if len(r.Hotspots) == 0 {
		ln("  %s", p.dim("no samples"))
	}
	for i, h := range r.Hotspots {
		ln("  %2d. %9.1fms self %9.1fms total  %s", i+1, h.SelfMs, h.TotalMs, trunc(hotLabel(h.Name, h.URL, h.Line), 60))
	}

	sec("Top modules")
	if len(r.Modules) == 0 {
		ln("  %s", p.dim("no samples"))
	}
	for _, m := range r.Modules {
		ln("  %s %5.1f%% %9.1fms  %s", bar(m.Pct, 20), m.Pct, m.SelfMs, trunc(m.Name, 55))
	}

	sec("Slow frames")
	if len(r.SlowFrames) == 0 {
		ln("  %s", p.dim("none over threshold"))
	}
	for _, f := range r.SlowFrames {
		ln("  t=%7.2fs %7.1fms  script %.1f  layout %.1f  paint %.1f  gc %.1f", f.T, f.Ms, f.ScriptMs, f.LayoutMs, f.PaintMs, f.GCMs)
		if f.TopEvent != "" || f.TopFunc != "" {
			ln("      %s", p.dim(trunc(strings.TrimSpace(f.TopEvent+"  "+f.TopFunc), termWidth-8)))
		}
	}

	sec("Long tasks")
	if len(r.LongTasks) == 0 {
		ln("  %s", p.dim("none over threshold"))
	}
	for _, t := range r.LongTasks {
		ln("  t=%7.2fs %7.1fms  %s", t.T, t.Ms, trunc(strings.TrimSpace(t.EventType+" "+t.Detail+"  "+t.TopFunc), 70))
	}

	sec("Findings")
	if len(r.Findings) == 0 {
		ln("  %s", p.dim("no issues detected"))
	}
	for i, f := range r.Findings {
		ln("%s %s %s", fmt.Sprintf("%2d.", i+1), p.badge(f.Severity), p.bold(f.Title))
		ln("    %s", p.dim(fmt.Sprintf("%s · %s · impact %.1fms (%.1f%%)", f.ID, f.Category, f.ImpactMs, f.ImpactPct)))
		for _, e := range f.Evidence {
			for j, l := range wrapText(e, termWidth-8) {
				if j == 0 {
					ln("    • %s", l)
				} else {
					ln("      %s", l)
				}
			}
		}
		for j, l := range wrapText(f.Suggestion, termWidth-12) {
			if j == 0 {
				ln("    → %s", p.bold("Fix:")+" "+l)
			} else {
				ln("          %s", l)
			}
		}
	}
	_, err := io.WriteString(w, b.String())
	return err
}

func mdEscape(s string) string {
	return strings.NewReplacer("|", "\\|", "\n", " ").Replace(s)
}

// RenderMarkdown writes the report as markdown suitable for an AI agent.
func RenderMarkdown(w io.Writer, r *Report) error {
	var b strings.Builder
	ln := func(f string, a ...any) { fmt.Fprintf(&b, f+"\n", a...) }
	ln("# wkperf profile: %s", r.Name)
	ln("")
	ln("- Source: `%s`", r.Source)
	ln("- Duration: %.2fs", r.DurationSec)

	ln("\n## Wall-time breakdown\n")
	ln("| Category | ms | %% |")
	ln("|---|---:|---:|")
	for _, s := range r.Breakdown {
		ln("| %s | %.1f | %.1f |", s.Name, s.Ms, s.Pct)
	}

	ln("\n## Top hotspots (self time)\n")
	if len(r.Hotspots) == 0 {
		ln("_No samples._")
	} else {
		ln("| # | Function | Self ms | Total ms |")
		ln("|--:|---|---:|---:|")
		for i, h := range r.Hotspots {
			ln("| %d | `%s` | %.1f | %.1f |", i+1, mdEscape(hotLabel(h.Name, h.URL, h.Line)), h.SelfMs, h.TotalMs)
		}
	}

	ln("\n## Top modules\n")
	if len(r.Modules) == 0 {
		ln("_No samples._")
	} else {
		ln("| Module | Self ms | %% |")
		ln("|---|---:|---:|")
		for _, m := range r.Modules {
			ln("| `%s` | %.1f | %.1f |", mdEscape(m.Name), m.SelfMs, m.Pct)
		}
	}

	ln("\n## Slow frames\n")
	if len(r.SlowFrames) == 0 {
		ln("_None over threshold._")
	} else {
		ln("| t (s) | Frame ms | Script | Layout+style | Paint+composite | GC | Top event | Top function |")
		ln("|---:|---:|---:|---:|---:|---:|---|---|")
		for _, f := range r.SlowFrames {
			ln("| %.2f | %.1f | %.1f | %.1f | %.1f | %.1f | %s | %s |", f.T, f.Ms, f.ScriptMs, f.LayoutMs, f.PaintMs, f.GCMs, mdEscape(f.TopEvent), mdEscape(f.TopFunc))
		}
	}

	ln("\n## Long tasks\n")
	if len(r.LongTasks) == 0 {
		ln("_None over threshold._")
	} else {
		ln("| t (s) | ms | Event | Detail | Top function |")
		ln("|---:|---:|---|---|---|")
		for _, t := range r.LongTasks {
			ln("| %.2f | %.1f | %s | %s | %s |", t.T, t.Ms, mdEscape(t.EventType), mdEscape(t.Detail), mdEscape(t.TopFunc))
		}
	}

	ln("\n## Findings\n")
	if len(r.Findings) == 0 {
		ln("_No issues detected._")
	}
	for i, f := range r.Findings {
		ln("### %d. [%s] %s", i+1, strings.ToUpper(string(f.Severity)), f.Title)
		ln("")
		ln("`%s` · category: %s · impact: %.1fms (%.1f%% of wall time)", f.ID, f.Category, f.ImpactMs, f.ImpactPct)
		ln("")
		ln("Evidence:")
		for _, e := range f.Evidence {
			ln("- %s", e)
		}
		ln("")
		ln("**Suggestion:** %s", f.Suggestion)
		ln("")
	}
	_, err := io.WriteString(w, b.String())
	return err
}
