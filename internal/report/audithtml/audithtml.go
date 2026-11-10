// Package audithtml renders an audit report (many scenes of one build) as a
// single self-contained HTML document: inline CSS and SVG, no scripts, light
// and dark themes. Every string is escaped by html/template.
package audithtml

import (
	"fmt"
	"html/template"
	"io"
	"strings"

	"github.com/omniviewdev/wkperf/internal/audit"
	"github.com/omniviewdev/wkperf/internal/insights"
	"github.com/omniviewdev/wkperf/internal/report/insightshtml"
	"github.com/omniviewdev/wkperf/internal/scorecard"
)

var palette = map[string]string{
	"Script":    "#e0a400",
	"Style":     "#9b6bd6",
	"Layout":    "#4b8bd9",
	"Paint":     "#2fa86b",
	"Composite": "#17a2a2",
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

type sliceView struct {
	Name, Color string
	Ms, Pct     string
	X, W        string
}

type sceneView struct {
	Name, Grade, Score, ScoreW string
	Partial                    bool
	Coverage                   string
	Runs                       int
	AttemptedRuns, MissingRuns int
	Duration                   string
	FrameP95, Jank, TBT        string
	TopFinding                 string
}

type priorityView struct {
	Rank       int
	Title      string
	ID         string
	Category   string
	Severity   string
	Scenes     []string
	SceneCount int
	Total      string
	AvgPct     string
	Evidence   []string
	Suggestion string
	Details    audit.Priority
	StackHTML  template.HTML
	Facets     []string
}

type funcView struct {
	Name, Loc             string
	Self, Pct             string
	SceneCount            int
	Scenes                string
	W                     string
	ID, Stack, StackScene string
}

type moduleView struct {
	Name, Self, Pct, W string
}

type worstView struct {
	Standard, Source, Scene, Value string
}

type gapView struct {
	Name, Rating, Label, Value, Target, Factor string
}

type sceneDetail struct {
	Name    string
	Content template.HTML
}

type pageView struct {
	Empty     bool
	Scenes    int
	Runs      int
	Recorded  string
	Grade     string
	Score     string
	ScoreW    string
	Partial   bool
	Coverage  string
	SceneRows []sceneView
	Slices    []sliceView
	Priority  []priorityView
	Funcs     []funcView
	Modules   []moduleView
	Worst     []worstView
	Gap       []gapView
	Details   []sceneDetail
	AI        string
}

func buildView(r *audit.Report, ai string) pageView {
	pv := pageView{
		Empty:    len(r.Scenes) == 0,
		Scenes:   len(r.Scenes),
		Runs:     r.TotalRuns(),
		Recorded: fmt.Sprintf("%.1fs", r.TotalSec()),
		Grade:    "-",
		AI:       strings.TrimSpace(ai),
	}
	if r.Overall != nil {
		pv.Grade = r.Overall.Grade
		pv.Score = fmt.Sprintf("%.0f", r.Overall.Score)
		pv.ScoreW = f1(clampPct(r.Overall.Score))
		pv.Partial = r.Overall.Partial
		pv.Coverage = fmt.Sprintf("%.0f%% coverage", 100*r.Overall.Coverage)
	}

	for _, s := range r.Scenes {
		pv.SceneRows = append(pv.SceneRows, sceneView{
			Name: s.Name, Grade: s.Card.Grade, Score: fmt.Sprintf("%.0f", s.Card.Score), ScoreW: f1(clampPct(s.Card.Score)),
			Partial: s.Card.Partial, Coverage: fmt.Sprintf("%.0f%% coverage", 100*s.Card.Coverage),
			Runs: s.Runs, AttemptedRuns: s.AttemptedRuns, MissingRuns: s.MissingRuns, Duration: fmt.Sprintf("%.1fs", s.DurationSec),
			FrameP95: audit.CardCell(s.Card, "frame-p95"), Jank: audit.CardCell(s.Card, "jank-ratio"), TBT: audit.CardCell(s.Card, "tbt"),
			TopFinding: s.TopFinding,
		})
	}

	x := 0.0
	for i, s := range r.Breakdown {
		w := clampPct(s.Pct)
		if x+w > 100 {
			w = 100 - x
		}
		pv.Slices = append(pv.Slices, sliceView{
			Name: s.Name, Color: sliceColor(s.Name, i), Ms: f1(s.Ms), Pct: f1(s.Pct),
			X: fmt.Sprintf("%.3f", x), W: fmt.Sprintf("%.3f", w),
		})
		x += w
	}

	for i, p := range r.Priorities {
		pv.Priority = append(pv.Priority, priorityView{
			Rank: i + 1, Title: p.Title, ID: p.ID, Category: p.Category, Severity: sevClass(p.Severity),
			Scenes: p.Scenes, SceneCount: len(p.Scenes),
			Total: f1(p.TotalImpactMs), AvgPct: f1(p.AvgImpactPct), Evidence: p.Evidence, Suggestion: p.Suggestion,
			Details: p,
			Facets:  insights.FacetLabels(p.NativeReads, p.Depth, p.RelatedFindings),
		})
	}

	maxSelf := 0.0
	for _, h := range r.Hotspots {
		if h.SelfMs > maxSelf {
			maxSelf = h.SelfMs
		}
	}
	for _, h := range r.Hotspots {
		loc := h.Location()
		name := h.Name
		if name == "" {
			name = "(anonymous)"
		}
		w := 0.0
		if maxSelf > 0 {
			w = h.SelfMs / maxSelf * 100
		}
		pv.Funcs = append(pv.Funcs, funcView{Name: name, Loc: loc, Self: f1(h.SelfMs), Pct: f1(h.Pct),
			SceneCount: h.SceneCount, Scenes: strings.Join(h.Scenes, ", "), W: f1(w), ID: h.ID, Stack: h.StackLabel(), StackScene: h.CommonStackScene})
	}
	for _, m := range r.Modules {
		pv.Modules = append(pv.Modules, moduleView{Name: m.Name, Self: f1(m.SelfMs), Pct: f1(m.Pct), W: f1(clampPct(m.Pct))})
	}
	for _, row := range r.WorstRows() {
		pv.Worst = append(pv.Worst, worstView{Standard: row.Standard.Name, Source: row.Standard.Source, Scene: row.Scene, Value: row.Value})
	}
	if r.Overall != nil {
		for _, g := range r.Overall.Gap {
			u := audit.StdUnit(r.Overall, g.ID)
			pv.Gap = append(pv.Gap, gapView{
				Name: g.Name, Rating: ratingClass(g.Rating), Label: string(g.Rating),
				Value: audit.FormatValue(u, g.Value), Target: audit.FormatValue(u, g.Target), Factor: f1(g.Factor),
			})
		}
	}
	return pv
}

// sevClass maps a severity onto a fixed CSS class suffix, so arbitrary text
// never reaches a class attribute.
func sevClass(s insights.Severity) string {
	switch s {
	case insights.SevHigh, insights.SevMedium, insights.SevLow:
		return string(s)
	}
	return "info"
}

func ratingClass(r scorecard.Rating) string {
	switch r {
	case scorecard.Good:
		return "good"
	case scorecard.NeedsImprovement:
		return "needs"
	case scorecard.Poor:
		return "poor"
	}
	return "na"
}

// Render writes a standalone HTML audit. A non-empty aiMarkdown is shown
// verbatim (escaped) in a preformatted block.
func Render(w io.Writer, r *audit.Report, aiMarkdown string) error {
	if r == nil {
		r = &audit.Report{}
	}
	r = audit.SelectEvidence(r)
	pv := buildView(r, aiMarkdown)
	for i, priority := range r.Priorities {
		var content strings.Builder
		if err := insightshtml.RenderStacks(&content, priority.Stacks, fmt.Sprintf("scene-%d-report-0-", priority.EvidenceSceneIndex)); err != nil {
			return err
		}
		if priority.StacksTotal > 0 {
			fmt.Fprintf(&content, "<p>Stack evidence: %d of %d shown.</p>", len(priority.Stacks), priority.StacksTotal)
		}
		pv.Priority[i].StackHTML = template.HTML(content.String())
	}
	for i, scene := range r.Scenes {
		if scene.Rep == nil {
			continue
		}
		var content strings.Builder
		if err := insightshtml.RenderFragment(&content, []*insights.Report{scene.Rep}, fmt.Sprintf("scene-%d-", i)); err != nil {
			return err
		}
		pv.Details = append(pv.Details, sceneDetail{Name: scene.Name, Content: template.HTML(content.String())})
	}
	return pageTmpl.Execute(w, pv)
}

const css = `
:root{--bg:#f6f7f9;--card:#fff;--fg:#1c2430;--mut:#667085;--bd:#e1e5ea;--bar:#4b8bd9;--hi:#d9534f;--med:#e0a400;--lo:#4b8bd9;--info:#8a94a3;--code:#eef1f5;
--gA:#2fa86b;--gB:#7cb342;--gC:#e0a400;--gD:#ef8a1c;--gF:#d9534f;--gX:#8a94a3}
@media (prefers-color-scheme:dark){:root{--bg:#12161c;--card:#1a2029;--fg:#e4e8ee;--mut:#97a1b0;--bd:#2b3340;--code:#232b36}}
*{box-sizing:border-box}
body{margin:0;background:var(--bg);color:var(--fg);font:14px/1.5 -apple-system,BlinkMacSystemFont,"Segoe UI",Roboto,sans-serif}
main{max-width:1060px;margin:0 auto;padding:24px 16px 64px}
h1{font-size:22px;margin:0 0 4px}h2{font-size:17px;margin:28px 0 10px}
.mut{color:var(--mut)}.card{background:var(--card);border:1px solid var(--bd);border-radius:8px;padding:14px 16px;margin:10px 0}
.stats{display:flex;flex-wrap:wrap;gap:12px}.stat{flex:1 1 140px;background:var(--card);border:1px solid var(--bd);border-radius:8px;padding:10px 14px}
.stat b{display:block;font-size:22px}
table{width:100%;border-collapse:collapse}th,td{text-align:left;padding:5px 8px;border-bottom:1px solid var(--bd);vertical-align:middle}
th{color:var(--mut);font-weight:600;font-size:12px}td.n,th.n{text-align:right;font-variant-numeric:tabular-nums}
.bar{background:var(--code);border-radius:3px;height:10px;min-width:80px}.bar i{display:block;height:100%;background:var(--bar);border-radius:3px}
.score i.gA{background:var(--gA)}.score i.gB{background:var(--gB)}.score i.gC{background:var(--gC)}.score i.gD{background:var(--gD)}.score i.gF,.score i.gX{background:var(--gF)}
.legend{display:flex;flex-wrap:wrap;gap:6px 18px;margin-top:10px;padding:0;list-style:none}
.legend span.sw{display:inline-block;width:10px;height:10px;border-radius:2px;margin-right:6px}
.badge{display:inline-block;padding:1px 8px;border-radius:10px;font-size:11px;font-weight:700;text-transform:uppercase;color:#fff;background:var(--info)}
.sev-high{background:var(--hi)}.sev-medium{background:var(--med)}.sev-low{background:var(--lo)}.sev-info{background:var(--info)}
.grade{display:inline-block;min-width:26px;text-align:center;padding:1px 6px;border-radius:5px;font-weight:700;color:#fff;background:var(--gX)}
.gA{background:var(--gA)}.gB{background:var(--gB)}.gC{background:var(--gC)}.gD{background:var(--gD)}.gF{background:var(--gF)}
.rate-good{color:var(--gA)}.rate-needs{color:var(--gD)}.rate-poor{color:var(--gF)}.rate-na{color:var(--mut)}
.prio h3{display:flex;gap:10px;align-items:center;margin:0 0 6px;font-size:14px}
.prio ul{margin:4px 0 8px 18px;padding:0}.sug{background:var(--code);border-radius:6px;padding:8px 10px}
code{background:var(--code);border-radius:3px;padding:0 4px;font-size:12.5px}
pre.ai{background:var(--code);padding:10px;border-radius:6px;overflow:auto;white-space:pre-wrap;word-break:break-word;font-size:12.5px}
svg{display:block;width:100%;height:auto}
`

var pageTmpl = template.Must(template.New("page").Parse(`<!DOCTYPE html>
<html lang="en"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1">
<title>wkperf audit</title><style>` + insightshtml.CSS + css + `</style></head><body><main>
<h1>wkperf audit</h1>
<div class="stats">
<div class="stat"><span class="mut">Scenes</span><b>{{.Scenes}}</b></div>
<div class="stat"><span class="mut">Runs</span><b>{{.Runs}}</b></div>
<div class="stat"><span class="mut">Recorded</span><b>{{.Recorded}}</b></div>
<div class="stat"><span class="mut">Overall (typical scene) · {{if .Partial}}Partial score{{else}}Score{{end}}</span><b><span class="grade g{{.Grade}}">{{.Grade}}</span> {{.Score}}<span class="mut">/100</span></b><span class="mut">{{.Coverage}}{{if .Partial}} · insufficient data for an overall grade{{end}}</span></div>
</div>
{{if .Empty}}<p class="mut">No scenes audited.</p>{{end}}

<h2>Scene leaderboard</h2>
<div class="card">{{if .SceneRows}}<table><tr><th>Scene</th><th>Grade</th><th class="n">Score</th><th></th><th class="n">Frame p95</th><th class="n">Jank</th><th class="n">TBT</th><th>Top finding</th></tr>
{{range .SceneRows}}<tr><td>{{.Name}}<div class="mut">{{.Runs}} parsed of {{.AttemptedRuns}} attempted run(s) · {{.MissingRuns}} missing &middot; {{.Duration}}</div></td><td><span class="grade g{{.Grade}}">{{.Grade}}</span>{{if .Partial}}<div class="mut">insufficient data</div>{{end}}</td><td class="n">{{.Score}}<div class="mut">{{if .Partial}}Partial score · {{end}}{{.Coverage}}</div></td><td><div class="bar score"><i class="g{{.Grade}}" style="width:{{.ScoreW}}%"></i></div></td><td class="n">{{.FrameP95}}</td><td class="n">{{.Jank}}</td><td class="n">{{.TBT}}</td><td>{{.TopFinding}}</td></tr>{{end}}</table>
{{else}}<span class="mut">No scenes.</span>{{end}}</div>

<h2>Where the time goes</h2>
<div class="card">
{{if .Slices}}<svg viewBox="0 0 100 8" preserveAspectRatio="none" role="img" aria-label="Duration-weighted wall-time breakdown">
{{range .Slices}}<rect x="{{.X}}" y="0" width="{{.W}}" height="8" fill="{{.Color}}"><title>{{.Name}}: {{.Ms}} ms ({{.Pct}}%)</title></rect>{{end}}</svg>
<ul class="legend">{{range .Slices}}<li><span class="sw" style="background:{{.Color}}"></span>{{.Name}} <span class="mut">{{.Pct}}% &middot; {{.Ms}} ms</span></li>{{end}}</ul>
{{else}}<span class="mut">No breakdown available.</span>{{end}}
</div>

<h2>Fix first</h2>
{{range .Priority}}<div class="card prio">
<h3><span class="mut">#{{.Rank}}</span><span class="badge sev-{{.Severity}}">{{.Severity}}</span>{{.Title}}</h3>
<div class="mut">{{.Category}} &middot; <code>{{.ID}}</code> &middot; {{.SceneCount}} scene(s) &middot; {{.Total}} ms total &middot; {{.AvgPct}}% avg impact over all scenes</div>
<div class="mut">Scenes: {{range $i, $s := .Scenes}}{{if $i}}, {{end}}{{$s}}{{end}}</div>
{{if .Evidence}}<ul>{{range .Evidence}}<li>{{.}}</li>{{end}}</ul>{{end}}
{{range .Facets}}<p>{{.}}</p>{{end}}
{{with .Details}}
{{if .EvidenceScene}}<div class="mut">Evidence scene: {{.EvidenceScene}}</div>{{end}}
{{if .MetricKeys}}<div><b>Metric keys:</b> {{range .MetricKeys}}<code>{{.}}</code> {{end}}</div>{{end}}
{{if .NumericEvidence}}<table><tr><th>Numeric evidence</th><th class="n">Value</th><th>Basis / description</th><th>Reproduce</th></tr>{{range .NumericEvidence}}<tr><td><code>{{.MetricKey}}</code></td><td class="n">{{printf "%.3f" .Value}} {{.Unit}}</td><td>{{.Basis}} · {{.Description}}</td><td><code>{{.Command}}</code></td></tr>{{end}}</table>{{end}}
{{if .Confidence}}<div><b>Confidence:</b> {{.Confidence}} · <b>Evidence basis:</b> {{.EvidenceBasis}} · <b>Replication:</b> seen in {{.Replication.RunsWithFinding}} of {{.Replication.Runs}} runs ({{.Replication.ParsedRuns}} parsed){{range .ConfidenceReasons}} · {{.}}{{end}}</div>{{end}}
{{if .Verify}}<h4>Verify</h4><table><tr><th>Metric key</th><th>Direction</th><th class="n">Target</th><th>Check / reproduce</th></tr>{{range .Verify}}<tr><td><code>{{.MetricKey}}</code></td><td>{{.Direction}}</td><td class="n">{{printf "%.3f" .Target}} {{.Unit}}</td><td>{{.Description}} <code>{{.Command}}</code></td></tr>{{end}}</table>{{end}}
{{if .SuggestedWindows}}<h4>Suggested windows</h4><ul>{{range .SuggestedWindows}}<li>{{printf "%.3f" .FromSec}}–{{printf "%.3f" .ToSec}} s: {{.Reason}} <code>{{range .Flags}}{{.}} {{end}}</code></li>{{end}}</ul>{{end}}
{{if .Functions}}<div><b>Functions:</b> {{range .Functions}}<code>{{.}}</code> {{end}}</div>{{end}}
{{if .Triggers}}<div><b>Triggers:</b> {{range .Triggers}}<code>{{.}}</code> {{end}}</div>{{end}}
{{if .Nodes}}<h4>Recorded nodes</h4><ul>{{range .Nodes}}<li>{{.EventType}}: <code>{{.CSSPath}}</code> {{.DisplayName}}</li>{{end}}</ul><div class="mut">Recorded node identifiers do not provide stylesheet source locations.</div>{{end}}
{{if .Command}}<div><b>Reproduce:</b> <code>{{.Command}}</code></div>{{end}}
{{end}}
{{if .StackHTML}}{{.StackHTML}}{{end}}
{{if .Suggestion}}<div class="sug"><b>Suggestion:</b> {{.Suggestion}}</div>{{end}}
</div>{{else}}<div class="card mut">No findings.</div>{{end}}

<h2>Hot functions across scenes</h2>
<div class="card">{{if .Funcs}}<table><tr><th>Function</th><th>Location</th><th class="n">Self ms</th><th class="n">Share</th><th class="n">Scenes</th><th></th></tr>
{{range .Funcs}}<tr><td><code>{{.Name}}</code>{{if .ID}}<div class="mut">ID <code>{{.ID}}</code></div>{{end}}{{if .Stack}}<details><summary>Common stack · {{.StackScene}}</summary><code>{{.Stack}}</code></details>{{end}}</td><td class="mut">{{.Loc}}</td><td class="n">{{.Self}}</td><td class="n">{{.Pct}}%</td><td class="n" title="{{.Scenes}}">{{.SceneCount}}</td><td><div class="bar"><i style="width:{{.W}}%"></i></div></td></tr>{{end}}</table>
{{else}}<span class="mut">No sampling data.</span>{{end}}</div>

<h2>Hot modules</h2>
<div class="card">{{if .Modules}}<table><tr><th>Module</th><th class="n">Self ms</th><th class="n">Share</th><th></th></tr>
{{range .Modules}}<tr><td><code>{{.Name}}</code></td><td class="n">{{.Self}}</td><td class="n">{{.Pct}}%</td><td><div class="bar"><i style="width:{{.W}}%"></i></div></td></tr>{{end}}</table>
{{else}}<span class="mut">No module data.</span>{{end}}</div>

<h2>Worst scene per standard</h2>
<div class="card">{{if .Worst}}<table><tr><th>Standard</th><th>Source</th><th>Scene</th><th class="n">Value</th></tr>
{{range .Worst}}<tr><td>{{.Standard}}</td><td class="mut">{{.Source}}</td><td>{{.Scene}}</td><td class="n">{{.Value}}</td></tr>{{end}}</table>
{{else}}<span class="mut">No data.</span>{{end}}</div>

<h2>Distance from a performant UI</h2>
<div class="card">{{if .Gap}}<table><tr><th>Standard</th><th>Rating</th><th class="n">Typical scene</th><th class="n">Target</th><th class="n">Over target</th></tr>
{{range .Gap}}<tr><td>{{.Name}}</td><td class="rate-{{.Rating}}">{{.Label}}</td><td class="n">{{.Value}}</td><td class="n">{{.Target}}</td><td class="n">{{.Factor}}&times;</td></tr>{{end}}</table>
{{else}}<span class="mut">Every available standard is rated good for the typical scene.</span>{{end}}</div>
{{if .Details}}<h2>Scene details</h2>{{range .Details}}<details class="card" open><summary>{{.Name}}</summary>{{.Content}}</details>{{end}}{{end}}
{{if .AI}}<h2>AI analysis</h2><div class="card"><pre class="ai">{{.AI}}</pre></div>{{end}}
</main></body></html>`))
