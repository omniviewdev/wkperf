// Package term renders a compare.Result as a colored terminal report.
package term

import (
	"fmt"
	"io"
	"math"
	"strings"
	"unicode/utf8"

	"github.com/omniviewdev/wkperf/internal/compare"
	"github.com/omniviewdev/wkperf/internal/metrics"
)

// Options control rendering.
type Options struct {
	Color   bool // emit ANSI escape codes
	Width   int  // target line width; default 100
	Verbose bool // show unchanged and info rows
	Top     int  // max functions per list; default 10
}

const (
	ansiReset  = "\x1b[0m"
	ansiBold   = "\x1b[1m"
	ansiDim    = "\x1b[2m"
	ansiRed    = "\x1b[31m"
	ansiGreen  = "\x1b[32m"
	ansiYellow = "\x1b[33m"
)

type painter struct{ on bool }

func (p painter) paint(code, s string) string {
	if !p.on || s == "" {
		return s
	}
	return code + s + ansiReset
}

func (p painter) verdict(v compare.Verdict, s string) string {
	switch v {
	case compare.Improved:
		return p.paint(ansiGreen, s)
	case compare.Regressed:
		return p.paint(ansiRed, s)
	case compare.Noisy:
		return p.paint(ansiYellow, s)
	default:
		return p.paint(ansiDim, s)
	}
}

// Render writes the report to w.
func Render(w io.Writer, r *compare.Result, o Options) error {
	if o.Width <= 0 {
		o.Width = 100
	}
	if o.Top <= 0 {
		o.Top = 10
	}
	var b strings.Builder
	p := painter{on: o.Color}

	renderHeader(&b, p, r)
	renderHeadline(&b, p, r)
	renderGroups(&b, p, r, o)
	renderFuncs(&b, p, "Top regressed functions", r.FuncRegressed, compare.Regressed, r.Comparability.Normalized, o)
	renderFuncs(&b, p, "Top improved functions", r.FuncImproved, compare.Improved, r.Comparability.Normalized, o)

	_, err := io.WriteString(w, b.String())
	return err
}

func renderHeader(b *strings.Builder, p painter, r *compare.Result) {
	b.WriteString(p.paint(ansiBold, "wkperf — WebKit timeline comparison") + "\n")
	side := func(name string, s compare.Side) {
		label := s.Label
		if label == "" {
			label = name
		}
		fmt.Fprintf(b, "  %-10s %s  (%s)\n", label+":", sourceList(s.Sources), plural(s.Runs, "run"))
	}
	side("baseline", r.Baseline)
	side("candidate", r.Cand)
	renderComparability(b, p, r.Comparability)
	b.WriteString("\n")
}

// renderComparability prints the score line and one bullet per warning. A
// zero Comparability (no score, no warnings) is not rendered.
func renderComparability(b *strings.Builder, p painter, c compare.Comparability) {
	if c.Score <= 0 && len(c.Warnings) == 0 && !c.Normalized {
		return
	}
	line := fmt.Sprintf("Comparability: %.2f", c.Score)
	if c.Normalized {
		line += " (normalized per second)"
	}
	code := ansiRed
	switch {
	case c.Score >= 0.8:
		code = ansiGreen
	case c.Score >= 0.5:
		code = ansiYellow
	}
	b.WriteString("  " + p.paint(code, line) + "\n")
	for _, w := range c.Warnings {
		text := fmt.Sprintf("[%s] %s", w.Code, w.Message)
		if w.Level == compare.LevelWarn {
			b.WriteString("    " + p.paint(ansiYellow, "! "+text) + "\n")
		} else {
			b.WriteString("    " + p.paint(ansiDim, "· "+text) + "\n")
		}
	}
}

func sourceList(src []string) string {
	if len(src) == 0 {
		return "-"
	}
	names := make([]string, len(src))
	for i, s := range src {
		if j := strings.LastIndexAny(s, `/\`); j >= 0 {
			s = s[j+1:]
		}
		names[i] = s
	}
	return strings.Join(names, ", ")
}

func plural(n int, word string) string {
	if n == 1 {
		return fmt.Sprintf("%d %s", n, word)
	}
	return fmt.Sprintf("%d %ss", n, word)
}

func renderHeadline(b *strings.Builder, p painter, r *compare.Result) {
	c := r.Counts
	fmt.Fprintf(b, "%s · %s · %s · %s\n",
		p.verdict(compare.Improved, fmt.Sprintf("%d improved", c.Improved)),
		p.verdict(compare.Regressed, fmt.Sprintf("%d regressed", c.Regressed)),
		p.verdict(compare.Noisy, fmt.Sprintf("%d noisy", c.Noisy)),
		p.verdict(compare.Unchanged, fmt.Sprintf("%d unchanged", c.Unchanged)))

	switch overall(r) {
	case compare.Regressed:
		b.WriteString(p.paint(ansiBold+ansiRed, "Overall: REGRESSED — high-confidence regressions in frames/script/layout") + "\n")
	case compare.Improved:
		b.WriteString(p.paint(ansiBold+ansiGreen, "Overall: IMPROVED") + "\n")
	default:
		b.WriteString(p.paint(ansiBold+ansiDim, "Overall: no significant change") + "\n")
	}
	b.WriteString("\n")
}

func overall(r *compare.Result) compare.Verdict {
	improved := r.Counts.Improved > 0
	for _, m := range r.Metrics {
		switch m.Metric.Group {
		case "frames", "script", "layout":
			if m.Verdict == compare.Regressed && m.Confidence != "low" {
				return compare.Regressed
			}
		}
		if m.Verdict == compare.Improved {
			improved = true
		}
	}
	if improved {
		return compare.Improved
	}
	return compare.Unchanged
}

type row struct {
	cells   [5]string
	bar     string
	marker  string
	verdict compare.Verdict
}

func hidden(m compare.MetricDiff) bool {
	return m.Verdict == compare.Unchanged || m.Verdict == compare.Info
}

func renderGroups(b *strings.Builder, p painter, r *compare.Result, o Options) {
	var order []string
	groups := map[string][]compare.MetricDiff{}
	for _, m := range r.Metrics {
		g := m.Metric.Group
		if _, ok := groups[g]; !ok {
			order = append(order, g)
		}
		groups[g] = append(groups[g], m)
	}
	for _, g := range order {
		var rows []row
		nHidden := 0
		for _, m := range groups[g] {
			if hidden(m) && !o.Verbose {
				nHidden++
				continue
			}
			rows = append(rows, metricRow(m))
		}
		b.WriteString(p.paint(ansiBold, strings.ToUpper(g)) + "\n")
		if len(rows) > 0 {
			writeTable(b, p, rows, o.Width)
		}
		if nHidden > 0 {
			b.WriteString(p.paint(ansiDim, fmt.Sprintf("  (%d unchanged)", nHidden)) + "\n")
		}
		b.WriteString("\n")
	}
}

func metricRow(m compare.MetricDiff) row {
	u := m.Metric.Unit
	name := m.Metric.Label
	if name == "" {
		name = m.Metric.Key
	}
	if m.Verdict == compare.Noisy {
		name = "~" + name
	}
	r := row{verdict: m.Verdict}
	r.cells[0] = name
	switch m.Verdict {
	case compare.NewMetric:
		r.cells[1], r.cells[2] = "-", FormatValue(m.Cand, u)
	case compare.Removed:
		r.cells[1], r.cells[2] = FormatValue(m.Base, u), "-"
	default:
		r.cells[1], r.cells[2] = FormatValue(m.Base, u), FormatValue(m.Cand, u)
		r.cells[3] = FormatDelta(m.Delta, u)
		r.cells[4] = FormatPct(m.Pct)
		r.bar = Bar(m.Pct)
		switch {
		case m.Delta > 0:
			r.marker = "▲"
		case m.Delta < 0:
			r.marker = "▼"
		}
	}
	if m.Verdict == compare.NewMetric || m.Verdict == compare.Removed {
		r.marker = string(m.Verdict)
	}
	return r
}

// writeTable prints rows with aligned columns. Column 0 is left-aligned and
// truncated to fit width; the rest are right-aligned.
func writeTable(b *strings.Builder, p painter, rows []row, width int) {
	headers := [5]string{"Metric", "Baseline", "Candidate", "Δ", "Δ%"}
	var w [5]int
	for i, h := range headers {
		w[i] = utf8.RuneCountInString(h)
	}
	for _, r := range rows {
		for i, c := range r.cells {
			if n := utf8.RuneCountInString(c); n > w[i] {
				w[i] = n
			}
		}
	}
	// indent(2) + 4 gaps(2) each + bar(barLen) + marker(8) space.
	fixed := 2 + w[1] + w[2] + w[3] + w[4] + 4*2 + 2 + barLen + 2 + 9
	if avail := width - fixed; w[0] > avail {
		w[0] = max(avail, 12)
	}
	line := func(cells [5]string, bar, marker string, v compare.Verdict, head bool) {
		cells[0] = Truncate(cells[0], w[0])
		s := "  " + padRight(cells[0], w[0])
		for i := 1; i < 5; i++ {
			s += "  " + padLeft(cells[i], w[i])
		}
		s += "  " + padRight(bar, barLen) + "  " + marker
		if head {
			b.WriteString(p.paint(ansiDim, strings.TrimRight(s, " ")) + "\n")
			return
		}
		b.WriteString(p.verdict(v, strings.TrimRight(s, " ")) + "\n")
	}
	line(headers, "", "", "", true)
	for _, r := range rows {
		marker := r.marker
		if r.verdict != "" && r.verdict != compare.Info {
			if marker != "" {
				marker += " "
			}
			marker += string(r.verdict)
		}
		line(r.cells, r.bar, marker, r.verdict, false)
	}
}

func renderFuncs(b *strings.Builder, p painter, title string, fs []compare.FuncDiff, v compare.Verdict, perSecond bool, o Options) {
	if len(fs) == 0 {
		return
	}
	if len(fs) > o.Top {
		fs = fs[:o.Top]
	}
	b.WriteString(p.paint(ansiBold, title) + "\n")
	type frow struct{ base, cand, delta, pct, name string }
	rows := make([]frow, len(fs))
	w := [4]int{len("Base"), len("Cand"), len("Δ"), len("Δ%")}
	for i, f := range fs {
		rows[i] = frow{
			FormatValue(f.BaseSelf, metrics.UnitMs),
			FormatValue(f.CandSelf, metrics.UnitMs),
			FormatDelta(f.Delta, metrics.UnitMs),
			FormatPct(f.Pct),
			FuncLabel(f.Name, f.URL, f.Line),
		}
		for j, c := range []string{rows[i].base, rows[i].cand, rows[i].delta, rows[i].pct} {
			if n := utf8.RuneCountInString(c); n > w[j] {
				w[j] = n
			}
		}
	}
	nameW := max(o.Width-(2+w[0]+w[1]+w[2]+w[3]+4*2), 20)
	col := "Function (self ms)"
	if perSecond {
		col = "Function (self ms per s)"
	}
	hdr := fmt.Sprintf("  %s  %s  %s  %s  %s", padLeft("Base", w[0]), padLeft("Cand", w[1]),
		padLeft("Δ", w[2]), padLeft("Δ%", w[3]), col)
	b.WriteString(p.paint(ansiDim, hdr) + "\n")
	for _, r := range rows {
		line := fmt.Sprintf("  %s  %s  %s  %s  %s", padLeft(r.base, w[0]), padLeft(r.cand, w[1]),
			padLeft(r.delta, w[2]), padLeft(r.pct, w[3]), Truncate(r.name, nameW))
		b.WriteString(p.verdict(v, line) + "\n")
	}
	b.WriteString("\n")
}

// FuncLabel returns "name  url:line" with the URL trimmed to its last path
// segments.
func FuncLabel(name, url string, line int) string {
	if name == "" {
		name = "(anonymous)"
	}
	if url == "" {
		return name
	}
	u := url
	if i := strings.IndexAny(u, "?#"); i >= 0 {
		u = u[:i]
	}
	parts := strings.Split(strings.TrimRight(u, "/"), "/")
	if len(parts) > 2 {
		parts = parts[len(parts)-2:]
	}
	u = strings.Join(parts, "/")
	if line > 0 {
		u = fmt.Sprintf("%s:%d", u, line)
	}
	return name + "  " + u
}

const barLen = 8

// Bar returns a unicode bar whose length scales with |pct| (a fraction),
// saturating at 100%.
func Bar(pct float64) string {
	a := math.Abs(pct)
	if a == 0 || math.IsNaN(a) {
		return ""
	}
	n := int(math.Ceil(math.Min(a, 1) * barLen))
	return strings.Repeat("█", max(n, 1))
}

// Truncate shortens s to at most n runes, ending with "…" when cut.
func Truncate(s string, n int) string {
	if n <= 0 || utf8.RuneCountInString(s) <= n {
		return s
	}
	r := []rune(s)
	return string(r[:n-1]) + "…"
}

func padLeft(s string, n int) string {
	if d := n - utf8.RuneCountInString(s); d > 0 {
		return strings.Repeat(" ", d) + s
	}
	return s
}

func padRight(s string, n int) string {
	if d := n - utf8.RuneCountInString(s); d > 0 {
		return s + strings.Repeat(" ", d)
	}
	return s
}

// FormatValue formats v with its unit.
func FormatValue(v float64, u metrics.Unit) string {
	switch u {
	case metrics.UnitMs:
		return thousands(fmt.Sprintf("%.2f", v)) + " ms"
	case metrics.UnitCount:
		if v != math.Trunc(v) {
			return thousands(fmt.Sprintf("%.2f", v))
		}
		return thousands(fmt.Sprintf("%.0f", v))
	case metrics.UnitPct:
		return fmt.Sprintf("%.1f%%", v)
	case metrics.UnitMB:
		return fmt.Sprintf("%.2f MB", v)
	case metrics.UnitBytes:
		return humanBytes(v)
	case metrics.UnitFPS:
		return fmt.Sprintf("%.1f fps", v)
	case metrics.UnitRatio:
		return fmt.Sprintf("%.1f%%", v*100)
	}
	return fmt.Sprintf("%.2f", v)
}

// FormatDelta formats a signed delta with its unit.
func FormatDelta(d float64, u metrics.Unit) string {
	s := FormatValue(math.Abs(d), u)
	switch {
	case d > 0:
		return "+" + s
	case d < 0:
		return "-" + s
	}
	return s
}

// FormatPct formats a fraction (0.123) as a signed percentage.
func FormatPct(f float64) string {
	if math.IsNaN(f) || math.IsInf(f, 0) {
		return "n/a"
	}
	s := fmt.Sprintf("%.1f%%", math.Abs(f)*100)
	switch {
	case s == "0.0%":
		return s
	case f > 0:
		return "+" + s
	}
	return "-" + s
}

func humanBytes(v float64) string {
	a := math.Abs(v)
	switch {
	case a >= 1<<20:
		return fmt.Sprintf("%.2f MB", v/(1<<20))
	case a >= 1<<10:
		return fmt.Sprintf("%.1f KB", v/(1<<10))
	}
	return fmt.Sprintf("%.0f B", v)
}

// thousands inserts commas into the integer part of a decimal string.
func thousands(s string) string {
	neg := strings.HasPrefix(s, "-")
	s = strings.TrimPrefix(s, "-")
	intPart, frac, _ := strings.Cut(s, ".")
	var out []byte
	for i, c := range []byte(intPart) {
		if i > 0 && (len(intPart)-i)%3 == 0 {
			out = append(out, ',')
		}
		out = append(out, c)
	}
	res := string(out)
	if frac != "" || strings.Contains(s, ".") {
		res += "." + frac
	}
	if neg {
		res = "-" + res
	}
	return res
}
