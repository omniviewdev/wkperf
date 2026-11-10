// Package html renders a compare.Result as a single self-contained HTML file.
package html

import (
	_ "embed"
	"fmt"
	stdhtml "html"
	"html/template"
	"io"
	"math"
	"strings"
	"time"

	"github.com/omniviewdev/wkperf/internal/compare"
	"github.com/omniviewdev/wkperf/internal/metrics"
)

//go:embed template.html
var templateSrc string

// Options control report presentation.
type Options struct {
	Title     string
	Generated time.Time
}

var tmpl = template.Must(template.New("report").Parse(templateSrc))

type cardView struct {
	Label, Base, Cand, Pct, Class, Glyph string
}

type rowView struct {
	Label, Key, Verdict, Class, Glyph string
	Base, Cand, Pct, Unit             string
	BaseRaw, CandRaw, PctRaw          float64
	Runs                              string
	Tooltip                           string
	Bar                               template.HTML
	Confidence                        string
}

type groupView struct {
	Name string
	Rows []rowView
}

type funcRow struct {
	Name, Loc, Class, Glyph, Pct string
	Base, Cand, Delta            string
	Bars                         template.HTML
	ID, Stack                    string
}

type chartView struct {
	Title string
	SVG   template.HTML
}

type warnView struct {
	Level, Class, Glyph, Code, Message string
}

// compView is the comparability banner; Show is false when the result carries
// no comparability data.
type compView struct {
	Show       bool
	Score      string
	Class      string
	Normalized bool
	Warnings   []warnView
}

type view struct {
	Title        string
	Generated    string
	Base, Cand   compare.Side
	Counts       compare.Counts
	Overall      string
	OverallClass string
	Comp         compView
	Unit         string // function time unit: "ms", or "ms/s" when normalized
	Cards        []cardView
	Groups       []groupView
	Charts       []chartView
	Regressed    []funcRow
	Improved     []funcRow
}

// Render writes the report to w.
func Render(w io.Writer, r *compare.Result, o Options) error {
	if r == nil {
		r = &compare.Result{}
	}
	v := buildView(r, o)
	return tmpl.Execute(w, v)
}

func buildView(r *compare.Result, o Options) view {
	title := o.Title
	if title == "" {
		title = "wkperf report"
	}
	gen := ""
	if !o.Generated.IsZero() {
		gen = o.Generated.Format("2006-01-02 15:04:05 MST")
	}
	v := view{Title: title, Generated: gen, Base: r.Baseline, Cand: r.Cand, Counts: r.Counts}
	v.Overall, v.OverallClass = overall(r.Counts)
	v.Comp = makeComp(r.Comparability)
	v.Unit = "ms"
	if r.Comparability.Normalized {
		v.Unit = "ms/s"
	}

	byKey := map[string]compare.MetricDiff{}
	var order []string
	groups := map[string]*groupView{}
	for _, m := range r.Metrics {
		byKey[m.Metric.Key] = m
		g := groups[m.Metric.Group]
		if g == nil {
			g = &groupView{Name: m.Metric.Group}
			groups[m.Metric.Group] = g
			order = append(order, m.Metric.Group)
		}
		g.Rows = append(g.Rows, makeRow(m))
	}
	for _, name := range order {
		v.Groups = append(v.Groups, *groups[name])
	}
	for _, k := range []string{"frames.p95_ms", "frames.fps", "script.total_ms", "layout.total_ms", "cpu.mean_pct", "memory.peak_mb"} {
		if m, ok := byKey[k]; ok {
			c, g := classOf(m.Verdict)
			v.Cards = append(v.Cards, cardView{
				Label: m.Metric.Label, Base: fmtVal(m.Base, m.Metric.Unit), Cand: fmtVal(m.Cand, m.Metric.Unit),
				Pct: fmtPct(m.Pct), Class: c, Glyph: g,
			})
		}
	}

	if r.BaseRep != nil || r.CandRep != nil {
		var bf, cf []float64
		var bc, cc, bm, cm []metrics.Point
		if r.BaseRep != nil {
			bf, bc, bm = r.BaseRep.FrameMs, r.BaseRep.CPU, r.BaseRep.MemoryMB
		}
		if r.CandRep != nil {
			cf, cc, cm = r.CandRep.FrameMs, r.CandRep.CPU, r.CandRep.MemoryMB
		}
		if len(bf)+len(cf) > 0 {
			v.Charts = append(v.Charts, chartView{"Frame duration histogram", histogramSVG(bf, cf)})
		}
		if len(bc)+len(cc) > 0 {
			v.Charts = append(v.Charts, chartView{"CPU usage over time (%)", lineSVG(bc, cc, "%")})
		}
		if len(bm)+len(cm) > 0 {
			v.Charts = append(v.Charts, chartView{"Memory over time (MB)", lineSVG(bm, cm, " MB")})
		}
	}
	v.Regressed = funcRows(r.FuncRegressed)
	v.Improved = funcRows(r.FuncImproved)
	return v
}

func makeComp(c compare.Comparability) compView {
	if c.Score <= 0 && len(c.Warnings) == 0 && !c.Normalized {
		return compView{}
	}
	cv := compView{Show: true, Score: fmt.Sprintf("%.2f", c.Score), Normalized: c.Normalized}
	switch {
	case c.Score >= 0.8:
		cv.Class = "good"
	case c.Score >= 0.5:
		cv.Class = "noisy"
	default:
		cv.Class = "bad"
	}
	for _, w := range c.Warnings {
		wv := warnView{Level: w.Level, Code: w.Code, Message: w.Message, Class: "flat", Glyph: "ℹ"}
		if w.Level == compare.LevelWarn {
			wv.Class, wv.Glyph = "noisy", "⚠"
		}
		cv.Warnings = append(cv.Warnings, wv)
	}
	return cv
}

func overall(c compare.Counts) (string, string) {
	switch {
	case c.Regressed > 0 && c.Regressed >= c.Improved:
		return "▼ Regressed", "bad"
	case c.Improved > 0 && c.Regressed == 0:
		return "▲ Improved", "good"
	case c.Improved > 0:
		return "▲ Mixed (more improved)", "good"
	case c.Noisy > 0:
		return "~ Within noise", "noisy"
	}
	return "= No change", "flat"
}

func classOf(v compare.Verdict) (class, glyph string) {
	switch v {
	case compare.Improved:
		return "good", "▲"
	case compare.Regressed:
		return "bad", "▼"
	case compare.Noisy:
		return "noisy", "~"
	case compare.NewMetric:
		return "flat", "+"
	case compare.Removed:
		return "flat", "−"
	}
	return "flat", "·"
}

func makeRow(m compare.MetricDiff) rowView {
	c, g := classOf(m.Verdict)
	rv := rowView{
		Label: m.Metric.Label, Key: m.Metric.Key, Verdict: string(m.Verdict), Class: c, Glyph: g,
		Base: fmtVal(m.Base, m.Metric.Unit), Cand: fmtVal(m.Cand, m.Metric.Unit), Pct: fmtPct(m.Pct),
		Unit: string(m.Metric.Unit), BaseRaw: m.Base, CandRaw: m.Cand, PctRaw: m.Pct,
		Confidence: m.Confidence, Bar: divergingBar(m.Pct, c),
	}
	if len(m.BaseRuns) > 1 || len(m.CandRuns) > 1 {
		rv.Runs = fmt.Sprintf("%d/%d", len(m.BaseRuns), len(m.CandRuns))
		var parts []string
		if len(m.BaseRuns) > 0 {
			lo, hi := minMax(m.BaseRuns)
			parts = append(parts, fmt.Sprintf("baseline runs: %s – %s", fmtVal(lo, m.Metric.Unit), fmtVal(hi, m.Metric.Unit)))
		}
		if len(m.CandRuns) > 0 {
			lo, hi := minMax(m.CandRuns)
			parts = append(parts, fmt.Sprintf("candidate runs: %s – %s", fmtVal(lo, m.Metric.Unit), fmtVal(hi, m.Metric.Unit)))
		}
		rv.Tooltip = strings.Join(parts, "; ")
	}
	return rv
}

func minMax(xs []float64) (lo, hi float64) {
	lo, hi = xs[0], xs[0]
	for _, x := range xs[1:] {
		lo = math.Min(lo, x)
		hi = math.Max(hi, x)
	}
	return
}

func fmtVal(v float64, u metrics.Unit) string {
	switch u {
	case metrics.UnitMB:
		return fmt.Sprintf("%.2f MB", v)
	case metrics.UnitBytes:
		return fmtBytes(v)
	case metrics.UnitCount:
		if v == math.Trunc(v) {
			return fmt.Sprintf("%.0f", v)
		}
		return fmt.Sprintf("%.2f", v)
	case metrics.UnitPct:
		return fmt.Sprintf("%.2f%%", v)
	case metrics.UnitMs:
		return fmt.Sprintf("%.2f ms", v)
	case metrics.UnitFPS:
		return fmt.Sprintf("%.1f fps", v)
	case metrics.UnitRatio:
		return fmt.Sprintf("%.3f", v)
	}
	return fmt.Sprintf("%.2f", v)
}

func fmtBytes(v float64) string {
	units := []string{"B", "KB", "MB", "GB", "TB"}
	i := 0
	a := math.Abs(v)
	for a >= 1024 && i < len(units)-1 {
		a /= 1024
		v /= 1024
		i++
	}
	if i == 0 {
		return fmt.Sprintf("%.0f B", v)
	}
	return fmt.Sprintf("%.2f %s", v, units[i])
}

func fmtPct(p float64) string {
	return fmt.Sprintf("%+.1f%%", p*100)
}

var colors = map[string]string{"good": "var(--good)", "bad": "var(--bad)", "noisy": "var(--noisy)", "flat": "var(--flat)"}

// divergingBar draws a bar growing right (increase) or left (decrease) of a
// center axis. Full scale is ±100%.
func divergingBar(pct float64, class string) template.HTML {
	const w, h, half = 120.0, 14.0, 60.0
	frac := math.Max(-1, math.Min(1, pct))
	if math.IsNaN(frac) {
		frac = 0
	}
	bw := math.Abs(frac) * half
	x := half
	if frac < 0 {
		x = half - bw
	}
	return template.HTML(fmt.Sprintf(
		`<svg class="bar" width="%.0f" height="%.0f" viewBox="0 0 %.0f %.0f" role="img" aria-label="%s"><line x1="%.0f" y1="0" x2="%.0f" y2="%.0f" stroke="var(--axis)"/><rect x="%.1f" y="3" width="%.1f" height="8" fill="%s"/></svg>`,
		w, h, w, h, fmtPct(pct), half, half, h, x, bw, colors[class]))
}

func funcRows(fs []compare.FuncDiff) []funcRow {
	var mx float64
	for _, f := range fs {
		mx = math.Max(mx, math.Max(f.BaseSelf, f.CandSelf))
	}
	out := make([]funcRow, 0, len(fs))
	for _, f := range fs {
		c, g := classOf(f.Verdict)
		if f.Verdict == "" {
			c, g = "flat", "·"
		}
		loc := f.Location()
		name := f.Name
		if name == "" {
			name = "(anonymous)"
		}
		out = append(out, funcRow{
			Name: name, Loc: loc, Class: c, Glyph: g, Pct: fmtPct(f.Pct),
			ID: f.ID, Stack: f.StackLabel(),
			Base: fmt.Sprintf("%.2f", f.BaseSelf), Cand: fmt.Sprintf("%.2f", f.CandSelf),
			Delta: fmt.Sprintf("%+.2f", f.Delta), Bars: funcBars(f.BaseSelf, f.CandSelf, mx),
		})
	}
	return out
}

func funcBars(b, c, mx float64) template.HTML {
	const w = 140.0
	if mx <= 0 {
		mx = 1
	}
	return template.HTML(fmt.Sprintf(
		`<svg class="bar" width="%.0f" height="16" viewBox="0 0 %.0f 16" role="img" aria-label="base %.2f ms, candidate %.2f ms"><rect x="0" y="1" width="%.1f" height="6" fill="var(--base)"/><rect x="0" y="9" width="%.1f" height="6" fill="var(--cand)"/></svg>`,
		w, w, b, c, b/mx*w, c/mx*w))
}

const (
	cw, ch                 = 560.0, 220.0
	padL, padR, padT, padB = 44.0, 12.0, 12.0, 28.0
)

func svgOpen(label string) *strings.Builder {
	var sb strings.Builder
	fmt.Fprintf(&sb, `<svg class="chart" viewBox="0 0 %.0f %.0f" role="img" aria-label="%s">`, cw, ch, stdhtml.EscapeString(label))
	return &sb
}

func legend(sb *strings.Builder) {
	fmt.Fprintf(sb, `<rect x="%.0f" y="4" width="10" height="10" fill="var(--base)"/><text x="%.0f" y="13" class="lt">baseline</text>`, cw-150, cw-136)
	fmt.Fprintf(sb, `<rect x="%.0f" y="4" width="10" height="10" fill="var(--cand)"/><text x="%.0f" y="13" class="lt">candidate</text>`, cw-80, cw-66)
}

// histogramSVG overlays baseline and candidate frame-duration histograms with
// guide lines at 16.7 ms and 33 ms.
func histogramSVG(base, cand []float64) template.HTML {
	const nb = 40
	hi := 40.0
	for _, s := range [][]float64{base, cand} {
		for _, x := range s {
			if x > hi {
				hi = x
			}
		}
	}
	// Clip the axis so rare long frames do not squash the distribution.
	if hi > 100 {
		hi = 100
	}
	bin := func(s []float64) []float64 {
		c := make([]float64, nb)
		if len(s) == 0 {
			return c
		}
		for _, x := range s {
			i := int(x / hi * nb)
			if i < 0 {
				i = 0
			}
			if i >= nb {
				i = nb - 1
			}
			c[i] += 100 / float64(len(s)) // percent of frames
		}
		return c
	}
	bb, cb := bin(base), bin(cand)
	mx := 1.0
	for i := 0; i < nb; i++ {
		mx = math.Max(mx, math.Max(bb[i], cb[i]))
	}
	pw, ph := cw-padL-padR, ch-padT-padB
	bwid := pw / nb
	sb := svgOpen("Frame duration histogram")
	axes(sb, 0, hi, mx, "ms", "% frames")
	draw := func(c []float64, fill string) {
		for i, v := range c {
			if v == 0 {
				continue
			}
			h := v / mx * ph
			fmt.Fprintf(sb, `<rect x="%.1f" y="%.1f" width="%.1f" height="%.1f" fill="%s" fill-opacity="0.55"/>`,
				padL+float64(i)*bwid, padT+ph-h, bwid, h, fill)
		}
	}
	draw(bb, "var(--base)")
	draw(cb, "var(--cand)")
	for _, g := range []float64{16.7, 33} {
		if g < hi {
			x := padL + g/hi*pw
			fmt.Fprintf(sb, `<line x1="%.1f" y1="%.0f" x2="%.1f" y2="%.1f" stroke="var(--guide)" stroke-dasharray="4 3"/><text x="%.1f" y="%.0f" class="lt">%gms</text>`,
				x, padT, x, padT+ph, x+3, padT+22, g)
		}
	}
	legend(sb)
	sb.WriteString(`</svg>`)
	return template.HTML(sb.String())
}

func axes(sb *strings.Builder, x0, x1, ymax float64, xl, yl string) {
	pw, ph := cw-padL-padR, ch-padT-padB
	fmt.Fprintf(sb, `<line x1="%.0f" y1="%.0f" x2="%.0f" y2="%.0f" stroke="var(--axis)"/><line x1="%.0f" y1="%.0f" x2="%.0f" y2="%.0f" stroke="var(--axis)"/>`,
		padL, padT, padL, padT+ph, padL, padT+ph, padL+pw, padT+ph)
	for i := 0; i <= 4; i++ {
		f := float64(i) / 4
		fmt.Fprintf(sb, `<text x="%.1f" y="%.0f" class="lt" text-anchor="middle">%s</text>`, padL+f*pw, ch-12, trimNum(x0+f*(x1-x0)))
		fmt.Fprintf(sb, `<text x="%.0f" y="%.1f" class="lt" text-anchor="end">%s</text>`, padL-4, padT+ph-f*ph+3, trimNum(f*ymax))
	}
	fmt.Fprintf(sb, `<text x="%.0f" y="%.0f" class="lt" text-anchor="end">%s</text><text x="%.0f" y="12" class="lt">%s</text>`,
		padL+pw, ch-1, stdhtml.EscapeString(xl), 2.0, stdhtml.EscapeString(yl))
}

func trimNum(v float64) string {
	s := fmt.Sprintf("%.1f", v)
	return strings.TrimSuffix(s, ".0")
}

// lineSVG overlays two time series. X axis is seconds; Y starts at zero.
func lineSVG(base, cand []metrics.Point, unit string) template.HTML {
	var tmax, vmax float64
	for _, s := range [][]metrics.Point{base, cand} {
		for _, p := range s {
			tmax = math.Max(tmax, p.T)
			vmax = math.Max(vmax, p.V)
		}
	}
	if tmax <= 0 {
		tmax = 1
	}
	if vmax <= 0 {
		vmax = 1
	}
	pw, ph := cw-padL-padR, ch-padT-padB
	sb := svgOpen("Time series" + unit)
	axes(sb, 0, tmax, vmax, "s", strings.TrimSpace(unit))
	path := func(s []metrics.Point, color string) {
		if len(s) == 0 {
			return
		}
		sb.WriteString(`<polyline fill="none" stroke-width="1.6" stroke="` + color + `" points="`)
		for _, p := range s {
			fmt.Fprintf(sb, "%.1f,%.1f ", padL+p.T/tmax*pw, padT+ph-p.V/vmax*ph)
		}
		sb.WriteString(`"/>`)
	}
	path(base, "var(--base)")
	path(cand, "var(--cand)")
	legend(sb)
	sb.WriteString(`</svg>`)
	return template.HTML(sb.String())
}
