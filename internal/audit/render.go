package audit

import (
	"fmt"
	"io"
	"strings"
	"unicode/utf8"

	"github.com/omniviewdev/wkperf/internal/metrics"
	"github.com/omniviewdev/wkperf/internal/scorecard"
)

const termWidth = 110

type painter struct{ on bool }

func (p painter) wrap(code, s string) string {
	if !p.on {
		return s
	}
	return "\x1b[" + code + "m" + s + "\x1b[0m"
}
func (p painter) bold(s string) string { return p.wrap("1", s) }
func (p painter) dim(s string) string  { return p.wrap("2", s) }

// grade colors a letter grade (A green .. F red).
func (p painter) grade(g string) string {
	switch g {
	case "A":
		return p.wrap("1;32", g)
	case "B":
		return p.wrap("32", g)
	case "C":
		return p.wrap("33", g)
	case "D":
		return p.wrap("1;33", g)
	}
	return p.wrap("1;31", g)
}

func (p painter) rating(r scorecard.Rating) string {
	s := string(r)
	switch r {
	case scorecard.Good:
		return p.wrap("32", s)
	case scorecard.NeedsImprovement:
		return p.wrap("33", s)
	case scorecard.Poor:
		return p.wrap("31", s)
	}
	return p.dim(s)
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
	if n < 1 {
		return ""
	}
	r := []rune(s)
	return string(r[:n-1]) + "…"
}

// padR left-aligns s in n columns (rune count, ANSI-free input).
func padR(s string, n int) string {
	s = trunc(s, n)
	return s + strings.Repeat(" ", n-utf8.RuneCountInString(s))
}

func wrapText(s string, width int) []string {
	var lines []string
	cur := ""
	for _, w := range strings.Fields(s) {
		switch {
		case cur == "":
			cur = w
		case utf8.RuneCountInString(cur)+1+utf8.RuneCountInString(w) <= width:
			cur += " " + w
		default:
			lines = append(lines, cur)
			cur = w
		}
	}
	if cur != "" {
		lines = append(lines, cur)
	}
	return lines
}

func mdEscape(s string) string {
	return strings.NewReplacer("|", "\\|", "\n", " ").Replace(s)
}

func funcLabel(name, url string, line int) string {
	if name == "" {
		name = "(anonymous)"
	}
	loc := url
	if loc != "" && line > 0 {
		loc = fmt.Sprintf("%s:%d", loc, line)
	}
	if loc == "" {
		return name
	}
	return name + " " + loc
}

// FormatValue renders v with the unit conventions used across the audit
// output: ratios as percentages, everything else as number plus unit.
func FormatValue(unit metrics.Unit, v float64) string {
	switch unit {
	case metrics.UnitRatio:
		return fmt.Sprintf("%.1f%%", v*100)
	case metrics.UnitPct:
		return fmt.Sprintf("%.1f%%", v)
	case metrics.UnitCount:
		return fmt.Sprintf("%.1f", v)
	case "":
		return fmt.Sprintf("%.1f", v)
	}
	return fmt.Sprintf("%.1f %s", v, unit)
}

// StdUnit finds the unit of standard id in c, falling back to the registry.
func StdUnit(c *scorecard.Scorecard, id string) metrics.Unit {
	if c != nil {
		for _, r := range c.Results {
			if r.ID == id {
				return r.Unit
			}
		}
	}
	for _, s := range scorecard.Standards() {
		if s.ID == id {
			return s.Unit
		}
	}
	return ""
}

// CardCell formats the value of standard id in c, or "n/a".
func CardCell(c *scorecard.Scorecard, id string) string {
	v, ok := CardValue(c, id)
	if !ok {
		return "n/a"
	}
	return FormatValue(StdUnit(c, id), v)
}

// TotalRuns is the number of recordings across all scenes.
func (r *Report) TotalRuns() int {
	n := 0
	for _, s := range r.Scenes {
		n += s.Runs
	}
	return n
}

// TotalSec is the recorded time summed over scenes (median duration x runs).
func (r *Report) TotalSec() float64 {
	t := 0.0
	for _, s := range r.Scenes {
		t += s.DurationSec * float64(s.Runs)
	}
	return t
}

func overallScore(r *Report) (string, float64) {
	if r.Overall == nil {
		return "-", 0
	}
	return r.Overall.Grade, r.Overall.Score
}

// WorstRows returns the "worst scene per standard" rows in registry order.
func (r *Report) WorstRows() []WorstRow {
	var out []WorstRow
	for _, std := range scorecard.Standards() {
		name, ok := r.WorstByStd[std.ID]
		if !ok {
			continue
		}
		cell := "n/a"
		for _, s := range r.Scenes {
			if s.Name == name {
				cell = CardCell(s.Card, std.ID)
				break
			}
		}
		out = append(out, WorstRow{Standard: std, Scene: name, Value: cell})
	}
	return out
}

// WorstRow is one row of the worst-scene-per-standard table.
type WorstRow struct {
	Standard scorecard.Standard
	Scene    string
	Value    string // formatted
}

func plural(n int, one, many string) string {
	if n == 1 {
		return one
	}
	return many
}

// RenderTerm writes a terminal report. color enables ANSI escapes.
func RenderTerm(w io.Writer, r *Report, color bool) error {
	p := painter{color}
	var b strings.Builder
	ln := func(f string, a ...any) { fmt.Fprintf(&b, f+"\n", a...) }
	sec := func(t string) {
		b.WriteString("\n" + p.bold(t) + "\n" + p.dim(strings.Repeat("─", termWidth)) + "\n")
	}

	g, score := overallScore(r)
	ln("%s", p.bold("wkperf audit"))
	ln("%s", p.dim(fmt.Sprintf("%d %s · %d %s · %.1fs recorded", len(r.Scenes), plural(len(r.Scenes), "scene", "scenes"),
		r.TotalRuns(), plural(r.TotalRuns(), "run", "runs"), r.TotalSec())))
	ln("Overall (typical scene): grade %s · score %.0f/100", p.grade(g), score)
	if len(r.Scenes) == 0 {
		ln("%s", p.dim("no scenes"))
	}

	sec("Scene leaderboard (worst first)")
	if len(r.Scenes) > 0 {
		ln("  %s", p.dim(fmt.Sprintf("%-24s %-5s %5s %12s %9s %10s  %s", "scene", "grade", "score", "frame p95", "jank", "TBT", "top finding")))
	}
	for _, s := range r.Scenes {
		ln("  %s %s %5.0f %12s %9s %10s  %s",
			padR(s.Name, 24), padGrade(p, s.Card.Grade, 5), s.Card.Score,
			CardCell(s.Card, "frame-p95"), CardCell(s.Card, "jank-ratio"), CardCell(s.Card, "tbt"),
			trunc(orDash(s.TopFinding), 40))
	}

	sec("Where the time goes (duration-weighted)")
	if len(r.Breakdown) == 0 {
		ln("  %s", p.dim("no breakdown"))
	}
	for _, s := range r.Breakdown {
		ln("  %-10s %s %5.1f%% %10.1fms", s.Name, bar(s.Pct, 40), s.Pct, s.Ms)
	}

	sec("Fix first")
	if len(r.Priorities) == 0 {
		ln("  %s", p.dim("no findings"))
	}
	for i, pr := range r.Priorities {
		ln("%2d. %s %s", i+1, sevBadge(p, string(pr.Severity)), p.bold(pr.Title))
		ln("    %s", p.dim(fmt.Sprintf("%s · %s · %d %s · %.1fms total · %.1f%% avg impact · rank %.1f",
			pr.ID, pr.Category, len(pr.Scenes), plural(len(pr.Scenes), "scene", "scenes"), pr.TotalImpactMs, pr.AvgImpactPct, pr.Rank)))
		ln("    scenes: %s", trunc(strings.Join(pr.Scenes, ", "), termWidth-14))
		for _, e := range pr.Evidence {
			for j, l := range wrapText(e, termWidth-8) {
				if j == 0 {
					ln("    • %s", l)
				} else {
					ln("      %s", l)
				}
			}
		}
		for j, l := range wrapText(pr.Suggestion, termWidth-12) {
			if j == 0 {
				ln("    → %s %s", p.bold("Fix:"), l)
			} else {
				ln("          %s", l)
			}
		}
	}

	sec("Hot functions across scenes")
	if len(r.Hotspots) == 0 {
		ln("  %s", p.dim("no samples"))
	}
	for i, h := range r.Hotspots {
		ln("  %2d. %9.1fms %5.1f%% %2d %-6s %s", i+1, h.SelfMs, h.Pct, h.SceneCount, plural(h.SceneCount, "scene", "scenes"),
			trunc(funcLabel(h.Name, h.URL, h.Line), 65))
	}

	sec("Hot modules")
	if len(r.Modules) == 0 {
		ln("  %s", p.dim("no samples"))
	}
	for _, m := range r.Modules {
		ln("  %s %5.1f%% %9.1fms  %s", bar(m.Pct, 20), m.Pct, m.SelfMs, trunc(m.Name, 60))
	}

	sec("Worst scene per standard")
	rows := r.WorstRows()
	if len(rows) == 0 {
		ln("  %s", p.dim("no data"))
	}
	for _, row := range rows {
		ln("  %s %-24s %12s", padR(row.Standard.Name, 34), trunc(row.Scene, 24), row.Value)
	}

	sec("Distance from a performant UI (typical scene)")
	if r.Overall == nil || len(r.Overall.Gap) == 0 {
		ln("  %s", p.dim("every available standard is rated good"))
	} else {
		for _, gi := range r.Overall.Gap {
			u := StdUnit(r.Overall, gi.ID)
			ln("  %s %s %12s → target %-12s %5.1f× over", padR(gi.Name, 34), p.rating(gi.Rating),
				FormatValue(u, gi.Value), FormatValue(u, gi.Target), gi.Factor)
		}
	}
	_, err := io.WriteString(w, b.String())
	return err
}

func orDash(s string) string {
	if s == "" {
		return "-"
	}
	return s
}

func padGrade(p painter, g string, width int) string {
	pad := width - utf8.RuneCountInString(g)
	if pad < 0 {
		pad = 0
	}
	return p.grade(g) + strings.Repeat(" ", pad)
}

func sevBadge(p painter, sev string) string {
	label := " " + strings.ToUpper(sev) + " "
	switch sev {
	case "high":
		return p.wrap("1;97;41", label)
	case "medium":
		return p.wrap("1;30;43", label)
	case "low":
		return p.wrap("1;30;46", label)
	}
	return p.wrap("1;30;47", label)
}

// RenderMarkdown writes the report as markdown suitable for an AI agent.
func RenderMarkdown(w io.Writer, r *Report) error {
	var b strings.Builder
	ln := func(f string, a ...any) { fmt.Fprintf(&b, f+"\n", a...) }

	g, score := overallScore(r)
	ln("# wkperf audit")
	ln("")
	ln("- Scenes: %d", len(r.Scenes))
	ln("- Runs: %d", r.TotalRuns())
	ln("- Recorded time: %.1fs", r.TotalSec())
	ln("- Overall (typical scene): grade **%s**, score %.0f/100", g, score)

	ln("")
	ln("## Scene leaderboard (worst first)")
	ln("")
	if len(r.Scenes) == 0 {
		ln("_No scenes._")
	} else {
		ln("| Scene | Runs | Grade | Score | Frame p95 | Jank | TBT | Top finding |")
		ln("|---|---:|:---:|---:|---:|---:|---:|---|")
		for _, s := range r.Scenes {
			ln("| %s | %d | %s | %.0f | %s | %s | %s | %s |", mdEscape(s.Name), s.Runs, s.Card.Grade, s.Card.Score,
				CardCell(s.Card, "frame-p95"), CardCell(s.Card, "jank-ratio"), CardCell(s.Card, "tbt"), mdEscape(orDash(s.TopFinding)))
		}
	}

	ln("")
	ln("## Where the time goes")
	ln("")
	if len(r.Breakdown) == 0 {
		ln("_No breakdown._")
	} else {
		ln("| Slice | Share | ms |")
		ln("|---|---:|---:|")
		for _, s := range r.Breakdown {
			ln("| %s | %.1f%% | %.1f |", mdEscape(s.Name), s.Pct, s.Ms)
		}
	}

	ln("")
	ln("## Fix first")
	ln("")
	if len(r.Priorities) == 0 {
		ln("_No findings._")
	}
	for i, pr := range r.Priorities {
		ln("%d. **%s** (%s, `%s`, %s)", i+1, pr.Title, pr.Severity, pr.ID, pr.Category)
		ln("   - Scenes affected (%d): %s", len(pr.Scenes), strings.Join(pr.Scenes, ", "))
		ln("   - Impact: %.1f ms total, %.1f%% average over all scenes, rank %.1f", pr.TotalImpactMs, pr.AvgImpactPct, pr.Rank)
		for _, e := range pr.Evidence {
			ln("   - Evidence: %s", strings.ReplaceAll(e, "\n", " "))
		}
		if pr.Suggestion != "" {
			ln("   - Suggestion: %s", strings.ReplaceAll(pr.Suggestion, "\n", " "))
		}
	}

	ln("")
	ln("## Hot functions across scenes")
	ln("")
	if len(r.Hotspots) == 0 {
		ln("_No sampling data._")
	} else {
		ln("| Function | Self ms | Share | Scenes |")
		ln("|---|---:|---:|---:|")
		for _, h := range r.Hotspots {
			ln("| `%s` | %.1f | %.1f%% | %d |", mdEscape(strings.ReplaceAll(funcLabel(h.Name, h.URL, h.Line), "`", "'")), h.SelfMs, h.Pct, h.SceneCount)
		}
	}

	ln("")
	ln("## Hot modules")
	ln("")
	if len(r.Modules) == 0 {
		ln("_No sampling data._")
	} else {
		ln("| Module | Self ms | Share |")
		ln("|---|---:|---:|")
		for _, m := range r.Modules {
			ln("| %s | %.1f | %.1f%% |", mdEscape(m.Name), m.SelfMs, m.Pct)
		}
	}

	ln("")
	ln("## Worst scene per standard")
	ln("")
	rows := r.WorstRows()
	if len(rows) == 0 {
		ln("_No data._")
	} else {
		ln("| Standard | Scene | Value |")
		ln("|---|---|---:|")
		for _, row := range rows {
			ln("| %s | %s | %s |", mdEscape(row.Standard.Name), mdEscape(row.Scene), row.Value)
		}
	}

	ln("")
	ln("## Distance from a performant UI")
	ln("")
	if r.Overall == nil || len(r.Overall.Gap) == 0 {
		ln("_Every available standard is rated good for the typical scene._")
	} else {
		ln("| Standard | Rating | Value | Target | Factor |")
		ln("|---|---|---:|---:|---:|")
		for _, gi := range r.Overall.Gap {
			u := StdUnit(r.Overall, gi.ID)
			ln("| %s | %s | %s | %s | %.1f× |", mdEscape(gi.Name), gi.Rating, FormatValue(u, gi.Value), FormatValue(u, gi.Target), gi.Factor)
		}
	}
	_, err := io.WriteString(w, b.String())
	return err
}
