// Package insightshtml renders insights reports (one recording each) as a
// single self-contained HTML document: inline CSS and SVG, no scripts, light
// and dark themes.
package insightshtml

import (
	"fmt"
	"html/template"
	"io"
	"strings"
	"time"

	"github.com/omniviewdev/wkperf/internal/insights"
)

var palette = map[string]string{
	"Script":    "#e0a400",
	"Style":     "#9b6bd6",
	"Layout":    "#4b8bd9",
	"Paint":     "#2fa86b",
	"Composite": "#17a2a2",
	"GC":        "#d9534f",
	"Other":     "#8a94a3",
	"Idle":      "#cdd3db",
}

var fallbackPalette = []string{"#c26d3f", "#6f8f2f", "#b24f8a", "#3f7fc2"}

func sliceColor(name string, i int) string {
	for k, v := range palette {
		if strings.EqualFold(k, name) {
			return v
		}
	}
	return fallbackPalette[i%len(fallbackPalette)]
}

type sliceView struct {
	Name, Color string
	Ms, Pct     string
	X, W        string
}

type barRow struct {
	Name, Detail string
	Value, Pct   string
	W            string
}

type reportView struct {
	R          *insights.Report
	Duration   string
	Slices     []sliceView
	Hotspots   []barRow
	Modules    []barRow
	SlowFrames []insights.SlowFrame
	LongTasks  []insights.LongTask
	Findings   []findingView
}

type findingView struct {
	insights.Finding
	Impact string
}

type pageView struct {
	Reports   []reportView
	Generated string
	AI        template.HTML
}

func f1(v float64) string { return fmt.Sprintf("%.1f", v) }

func clampPct(v float64) float64 {
	if v < 0 {
		return 0
	}
	if v > 100 {
		return 100
	}
	return v
}

func buildView(r *insights.Report) reportView {
	v := reportView{R: r, Duration: fmt.Sprintf("%.2fs", r.DurationSec)}
	x := 0.0
	for i, s := range r.Breakdown {
		w := clampPct(s.Pct)
		if x+w > 100 {
			w = 100 - x
		}
		v.Slices = append(v.Slices, sliceView{
			Name: s.Name, Color: sliceColor(s.Name, i), Ms: f1(s.Ms), Pct: f1(s.Pct),
			X: fmt.Sprintf("%.3f", x), W: fmt.Sprintf("%.3f", w),
		})
		x += w
	}
	maxSelf := 0.0
	for _, h := range r.Hotspots {
		if h.SelfMs > maxSelf {
			maxSelf = h.SelfMs
		}
	}
	for _, h := range r.Hotspots {
		loc := h.URL
		if loc != "" && h.Line > 0 {
			loc = fmt.Sprintf("%s:%d", loc, h.Line)
		}
		name := h.Name
		if name == "" {
			name = "(anonymous)"
		}
		w := 0.0
		if maxSelf > 0 {
			w = h.SelfMs / maxSelf * 100
		}
		v.Hotspots = append(v.Hotspots, barRow{Name: name, Detail: loc, Value: f1(h.SelfMs) + " ms", Pct: f1(h.TotalMs) + " ms", W: f1(w)})
	}
	for _, m := range r.Modules {
		v.Modules = append(v.Modules, barRow{Name: m.Name, Value: f1(m.SelfMs) + " ms", Pct: f1(m.Pct) + "%", W: f1(clampPct(m.Pct))})
	}
	v.SlowFrames = r.SlowFrames
	v.LongTasks = r.LongTasks
	for _, f := range r.Findings {
		v.Findings = append(v.Findings, findingView{Finding: f, Impact: fmt.Sprintf("%.1f ms (%.1f%%)", f.ImpactMs, f.ImpactPct)})
	}
	return v
}

// Render writes a standalone HTML analysis of reps.
func Render(w io.Writer, reps []*insights.Report) error {
	return RenderWithAI(w, reps, "")
}

// RenderWithAI is Render plus an AI answer (Markdown text) appended as an
// escaped, preformatted-free section. Empty ai omits the section.
func RenderWithAI(w io.Writer, reps []*insights.Report, ai string) error {
	pv := pageView{Generated: time.Now().Format("2006-01-02 15:04:05")}
	for _, r := range reps {
		if r != nil {
			pv.Reports = append(pv.Reports, buildView(r))
		}
	}
	if strings.TrimSpace(ai) != "" {
		pv.AI = AISection(ai)
	}
	return pageTmpl.Execute(w, pv)
}

const css = `
:root{--bg:#f6f7f9;--card:#fff;--fg:#1c2430;--mut:#667085;--bd:#e1e5ea;--bar:#4b8bd9;--hi:#d9534f;--med:#e0a400;--lo:#4b8bd9;--info:#8a94a3;--code:#eef1f5}
@media (prefers-color-scheme:dark){:root{--bg:#12161c;--card:#1a2029;--fg:#e4e8ee;--mut:#97a1b0;--bd:#2b3340;--code:#232b36}}
*{box-sizing:border-box}
body{margin:0;background:var(--bg);color:var(--fg);font:14px/1.5 -apple-system,BlinkMacSystemFont,"Segoe UI",Roboto,sans-serif}
main{max-width:1000px;margin:0 auto;padding:24px 16px 64px}
h1{font-size:22px;margin:0 0 4px}h2{font-size:17px;margin:28px 0 10px}h3{font-size:14px;margin:16px 0 6px}
.mut{color:var(--mut)}.card{background:var(--card);border:1px solid var(--bd);border-radius:8px;padding:14px 16px;margin:10px 0}
table{width:100%;border-collapse:collapse}th,td{text-align:left;padding:5px 8px;border-bottom:1px solid var(--bd);vertical-align:middle}
th{color:var(--mut);font-weight:600;font-size:12px}td.n,th.n{text-align:right;font-variant-numeric:tabular-nums}
.bar{background:var(--code);border-radius:3px;height:10px;min-width:80px}.bar i{display:block;height:100%;background:var(--bar);border-radius:3px}
.legend{display:flex;flex-wrap:wrap;gap:6px 18px;margin-top:10px;padding:0;list-style:none}
.legend span.sw{display:inline-block;width:10px;height:10px;border-radius:2px;margin-right:6px}
.badge{display:inline-block;padding:1px 8px;border-radius:10px;font-size:11px;font-weight:700;text-transform:uppercase;color:#fff}
.sev-high{background:var(--hi)}.sev-medium{background:var(--med)}.sev-low{background:var(--lo)}.sev-info{background:var(--info)}
.finding h3{display:flex;gap:10px;align-items:center;margin:0 0 6px}
.finding ul{margin:4px 0 8px 18px;padding:0}.sug{background:var(--code);border-radius:6px;padding:8px 10px}
code{background:var(--code);border-radius:3px;padding:0 4px;font-size:12.5px}
.ai pre.code{background:var(--code);padding:10px;border-radius:6px;overflow:auto}
.ai{white-space:normal}.ai h4,.ai h5{margin:14px 0 4px}
svg{display:block;width:100%;height:auto}
`

var pageTmpl = template.Must(template.New("page").Parse(`<!DOCTYPE html>
<html lang="en"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1">
<title>wkperf analysis</title><style>` + css + `</style></head><body><main>
<h1>wkperf analysis</h1><div class="mut">Generated {{.Generated}}</div>
{{range .Reports}}{{template "report" .}}{{else}}<p class="mut">No recordings analyzed.</p>{{end}}
{{if .AI}}<h2>AI analysis</h2><div class="card ai">{{.AI}}</div>{{end}}
</main></body></html>
{{define "report"}}
<section>
<h2>{{if .R.Name}}{{.R.Name}}{{else}}{{.R.Source}}{{end}}</h2>
<div class="mut">{{.R.Source}} &middot; {{.Duration}}</div>

<h3>Where time goes</h3>
<div class="card">
{{if .Slices}}<svg viewBox="0 0 100 8" preserveAspectRatio="none" role="img" aria-label="Wall-time breakdown">
{{range .Slices}}<rect x="{{.X}}" y="0" width="{{.W}}" height="8" fill="{{.Color}}"><title>{{.Name}}: {{.Ms}} ms ({{.Pct}}%)</title></rect>{{end}}</svg>
<ul class="legend">{{range .Slices}}<li><span class="sw" style="background:{{.Color}}"></span>{{.Name}} <span class="mut">{{.Ms}} ms &middot; {{.Pct}}%</span></li>{{end}}</ul>
{{else}}<span class="mut">No breakdown available.</span>{{end}}
</div>

<h3>Findings</h3>
{{range .Findings}}<div class="card finding">
<h3><span class="badge sev-{{.Severity}}">{{.Severity}}</span>{{.Title}}</h3>
<div class="mut">{{.Category}} &middot; impact {{.Impact}} &middot; <code>{{.ID}}</code></div>
{{if .Evidence}}<ul>{{range .Evidence}}<li>{{.}}</li>{{end}}</ul>{{end}}
{{if .Suggestion}}<div class="sug"><b>Suggestion:</b> {{.Suggestion}}</div>{{end}}
</div>{{else}}<div class="card mut">No findings.</div>{{end}}

<h3>Hotspot functions (self time)</h3>
<div class="card">{{if .Hotspots}}<table><tr><th>Function</th><th>Location</th><th class="n">Self</th><th class="n">Total</th><th></th></tr>
{{range .Hotspots}}<tr><td><code>{{.Name}}</code></td><td class="mut">{{.Detail}}</td><td class="n">{{.Value}}</td><td class="n">{{.Pct}}</td><td><div class="bar"><i style="width:{{.W}}%"></i></div></td></tr>{{end}}</table>
{{else}}<span class="mut">No sampling data.</span>{{end}}</div>

<h3>Modules</h3>
<div class="card">{{if .Modules}}<table><tr><th>Module</th><th class="n">Self</th><th class="n">Share</th><th></th></tr>
{{range .Modules}}<tr><td><code>{{.Name}}</code></td><td class="n">{{.Value}}</td><td class="n">{{.Pct}}</td><td><div class="bar"><i style="width:{{.W}}%"></i></div></td></tr>{{end}}</table>
{{else}}<span class="mut">No module data.</span>{{end}}</div>

<h3>Slow frames</h3>
<div class="card">{{if .SlowFrames}}<table><tr><th class="n">At (s)</th><th class="n">Frame ms</th><th class="n">Script</th><th class="n">Layout</th><th class="n">Paint</th><th class="n">GC</th><th>Top event / function</th></tr>
{{range .SlowFrames}}<tr><td class="n">{{printf "%.2f" .T}}</td><td class="n">{{printf "%.1f" .Ms}}</td><td class="n">{{printf "%.1f" .ScriptMs}}</td><td class="n">{{printf "%.1f" .LayoutMs}}</td><td class="n">{{printf "%.1f" .PaintMs}}</td><td class="n">{{printf "%.1f" .GCMs}}</td><td>{{.TopEvent}}{{if .TopFunc}} &rarr; <code>{{.TopFunc}}</code>{{end}}</td></tr>{{end}}</table>
{{else}}<span class="mut">No slow frames.</span>{{end}}</div>

<h3>Long tasks</h3>
<div class="card">{{if .LongTasks}}<table><tr><th class="n">At (s)</th><th class="n">ms</th><th>Event</th><th>Detail</th><th>Top function</th></tr>
{{range .LongTasks}}<tr><td class="n">{{printf "%.2f" .T}}</td><td class="n">{{printf "%.1f" .Ms}}</td><td>{{.EventType}}</td><td>{{.Detail}}</td><td>{{if .TopFunc}}<code>{{.TopFunc}}</code>{{end}}</td></tr>{{end}}</table>
{{else}}<span class="mut">No long tasks.</span>{{end}}</div>
</section>
{{end}}`))

// AISection converts model Markdown into a small, escaped HTML fragment
// (headings, lists, fenced code, paragraphs, **bold**, `code`). The fragment
// carries no scripts and is safe to embed in any page.
func AISection(md string) template.HTML {
	var b strings.Builder
	lines := strings.Split(strings.ReplaceAll(md, "\r\n", "\n"), "\n")
	var para []string
	list := ""
	flushPara := func() {
		if len(para) > 0 {
			b.WriteString("<p>" + inline(strings.Join(para, " ")) + "</p>\n")
			para = nil
		}
	}
	closeList := func() {
		if list != "" {
			b.WriteString("</" + list + ">\n")
			list = ""
		}
	}
	for i := 0; i < len(lines); i++ {
		ln := lines[i]
		t := strings.TrimSpace(ln)
		switch {
		case strings.HasPrefix(t, "```"):
			flushPara()
			closeList()
			var code []string
			for i++; i < len(lines) && !strings.HasPrefix(strings.TrimSpace(lines[i]), "```"); i++ {
				code = append(code, lines[i])
			}
			b.WriteString("<pre class=\"code\">" + template.HTMLEscapeString(strings.Join(code, "\n")) + "</pre>\n")
		case t == "":
			flushPara()
			closeList()
		case strings.HasPrefix(t, "#"):
			flushPara()
			closeList()
			n := len(t) - len(strings.TrimLeft(t, "#"))
			tag := "h4"
			if n > 2 {
				tag = "h5"
			}
			b.WriteString("<" + tag + ">" + inline(strings.TrimSpace(t[n:])) + "</" + tag + ">\n")
		case strings.HasPrefix(t, "- ") || strings.HasPrefix(t, "* "):
			flushPara()
			if list != "ul" {
				closeList()
				b.WriteString("<ul>")
				list = "ul"
			}
			b.WriteString("<li>" + inline(t[2:]) + "</li>")
		case isNumbered(t):
			flushPara()
			if list != "ol" {
				closeList()
				b.WriteString("<ol>")
				list = "ol"
			}
			b.WriteString("<li>" + inline(t[strings.Index(t, ". ")+2:]) + "</li>")
		default:
			closeList()
			para = append(para, t)
		}
	}
	flushPara()
	closeList()
	return template.HTML(b.String()) //nolint:gosec // all text escaped above
}

func isNumbered(t string) bool {
	i := 0
	for i < len(t) && t[i] >= '0' && t[i] <= '9' {
		i++
	}
	return i > 0 && strings.HasPrefix(t[i:], ". ")
}

// inline escapes s and then applies **bold** and `code`.
func inline(s string) string {
	s = template.HTMLEscapeString(s)
	s = wrapPairs(s, "`", "<code>", "</code>")
	return wrapPairs(s, "**", "<b>", "</b>")
}

func wrapPairs(s, delim, open, close string) string {
	parts := strings.Split(s, delim)
	if len(parts)%2 == 0 { // unbalanced: leave untouched
		return s
	}
	var b strings.Builder
	for i, p := range parts {
		if i > 0 {
			if i%2 == 1 {
				b.WriteString(open)
			} else {
				b.WriteString(close)
			}
		}
		b.WriteString(p)
	}
	return b.String()
}
