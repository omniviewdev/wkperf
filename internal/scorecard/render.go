package scorecard

import (
	"fmt"
	"io"
	"strings"

	"github.com/omniviewdev/wkperf/internal/metrics"
)

const (
	ansiReset  = "\x1b[0m"
	ansiDim    = "\x1b[2m"
	ansiBold   = "\x1b[1m"
	ansiGreen  = "\x1b[32m"
	ansiYellow = "\x1b[33m"
	ansiRed    = "\x1b[31m"
)

func fmtVal(u metrics.Unit, v float64) string {
	switch u {
	case metrics.UnitMs:
		return fmt.Sprintf("%.1fms", v)
	case metrics.UnitRatio:
		return fmt.Sprintf("%.1f%%", v*100)
	case metrics.UnitFPS:
		return fmt.Sprintf("%.1f fps", v)
	case metrics.UnitMB:
		return fmt.Sprintf("%.2f MB", v)
	case metrics.UnitPct:
		return fmt.Sprintf("%.1f%%", v)
	}
	return fmt.Sprintf("%.2f", v)
}

// fmtStd formats a value with the standard's rate suffix where the unit is
// ambiguous (counts and MB are per-time rates for some standards).
func fmtStd(s Standard, v float64) string {
	out := fmtVal(s.Unit, v)
	switch s.ID {
	case "tbt":
		return out + "/10s"
	case "long-task-rate":
		return out + "/min"
	case "memory-growth":
		return out + "/min"
	case "forced-layout-rate":
		return out + "/s"
	}
	return out
}

func cmpPrefix(s Standard) string {
	if s.HigherBetter {
		return "≥ "
	}
	return "≤ "
}

func paint(color bool, code, s string) string {
	if !color {
		return s
	}
	return code + s + ansiReset
}

func ratingColor(r Rating) string {
	switch r {
	case Good:
		return ansiGreen
	case NeedsImprovement:
		return ansiYellow
	case Poor:
		return ansiRed
	}
	return ansiDim
}

func stdByID(id string) Standard {
	for _, s := range Standards() {
		if s.ID == id {
			return s
		}
	}
	return Standard{ID: id}
}

func gapLine(g GapItem) string {
	s := stdByID(g.ID)
	if s.Name == "" {
		s.Name = g.Name
	}
	verb := "is"
	if s.HigherBetter {
		return fmt.Sprintf("%s is %.1fx below the %s%s target (now %s)", g.Name, g.Factor, cmpPrefix(s), fmtStd(s, g.Target), fmtStd(s, g.Value))
	}
	return fmt.Sprintf("%s %s %.1fx the %s target (now %s)", g.Name, verb, g.Factor, fmtStd(s, g.Target), fmtStd(s, g.Value))
}

func scoreLabel(c *Scorecard) string {
	if c.Partial {
		return fmt.Sprintf("partial score %.0f/100", c.Score)
	}
	return fmt.Sprintf("score %.0f/100", c.Score)
}

func gradeRequirement() string {
	return fmt.Sprintf("Letter grade requires at least %.0f%% standard coverage and available frame standards; every aggregate input must also support a grade.", MinCoverage*100)
}

// RenderTerm writes the scorecard as a terminal report. color enables ANSI.
func RenderTerm(w io.Writer, c *Scorecard, color bool) error {
	var b strings.Builder
	b.WriteString(paint(color, ansiBold, fmt.Sprintf("Performance scorecard: grade %s, %s, coverage %.1f%%", c.Grade, scoreLabel(c), c.Coverage*100)) + "\n\n")
	if c.Partial {
		b.WriteString(gradeRequirement() + "\n\n")
	}

	rows := [][]string{{"Standard", "Value", "Target (good)", "Rating", "Source"}}
	for _, r := range c.Results {
		val, detail := "n/a", ""
		if r.Rating != Unavailable {
			val = fmtStd(r.Standard, r.Value)
		}
		if r.Detail != "" {
			detail = " (" + r.Detail + ")"
		}
		rows = append(rows, []string{r.Name, val + detail, cmpPrefix(r.Standard) + fmtStd(r.Standard, r.Good), string(r.Rating), r.Source})
	}
	widths := make([]int, 5)
	for _, row := range rows {
		for i, cell := range row {
			if n := len([]rune(cell)); n > widths[i] {
				widths[i] = n
			}
		}
	}
	for ri, row := range rows {
		var line strings.Builder
		for i, cell := range row {
			pad := strings.Repeat(" ", widths[i]-len([]rune(cell)))
			text := cell
			if ri == 0 {
				text = paint(color, ansiBold, cell)
			} else if i == 3 {
				text = paint(color, ratingColor(Rating(cell)), cell)
			} else if row[3] == string(Unavailable) {
				text = paint(color, ansiDim, cell)
			}
			line.WriteString(text + pad)
			if i < 4 {
				line.WriteString("  ")
			}
		}
		b.WriteString(strings.TrimRight(line.String(), " ") + "\n")
	}

	b.WriteString("\n" + paint(color, ansiBold, "How far from a performant UI") + "\n")
	if len(c.Gap) == 0 {
		if c.Coverage == 0 {
			b.WriteString("No standards have sufficient data.\n")
		} else {
			b.WriteString("Every available standard is met.\n")
		}
	}
	for _, g := range c.Gap {
		b.WriteString("  " + paint(color, ratingColor(g.Rating), "•") + " " + gapLine(g) + "\n")
	}
	_, err := io.WriteString(w, b.String())
	return err
}

// RenderMarkdown writes the same content as RenderTerm as Markdown.
func RenderMarkdown(w io.Writer, c *Scorecard) error {
	var b strings.Builder
	fmt.Fprintf(&b, "## Performance scorecard: grade %s, %s, coverage %.1f%%\n\n", c.Grade, scoreLabel(c), c.Coverage*100)
	if c.Partial {
		b.WriteString(gradeRequirement() + "\n\n")
	}
	b.WriteString("| Standard | Value | Target (good) | Rating | Source |\n|---|---|---|---|---|\n")
	for _, r := range c.Results {
		val := "n/a"
		if r.Rating != Unavailable {
			val = fmtStd(r.Standard, r.Value)
		}
		if r.Detail != "" {
			val += " (" + r.Detail + ")"
		}
		fmt.Fprintf(&b, "| %s | %s | %s%s | %s | %s |\n", r.Name, val, cmpPrefix(r.Standard), fmtStd(r.Standard, r.Good), r.Rating, r.Source)
	}
	b.WriteString("\n### How far from a performant UI\n\n")
	if len(c.Gap) == 0 {
		if c.Coverage == 0 {
			b.WriteString("No standards have sufficient data.\n")
		} else {
			b.WriteString("Every available standard is met.\n")
		}
	}
	for _, g := range c.Gap {
		b.WriteString("- " + gapLine(g) + "\n")
	}
	var notes []string
	for _, r := range c.Results {
		if r.Note != "" {
			notes = append(notes, fmt.Sprintf("- **%s**: %s", r.Name, r.Note))
		}
	}
	b.WriteString("\n### Notes\n\n" + strings.Join(notes, "\n") + "\n")
	_, err := io.WriteString(w, b.String())
	return err
}
