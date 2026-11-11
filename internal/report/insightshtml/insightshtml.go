// Package insightshtml renders insights reports (one recording each) as a
// single self-contained HTML document: inline CSS and SVG, no scripts, light
// and dark themes.
package insightshtml

import (
	"fmt"
	"html/template"
	"io"
	"strings"

	"github.com/omniviewdev/wkperf/internal/insights"
	"github.com/omniviewdev/wkperf/internal/metrics"
	flamehtml "github.com/omniviewdev/wkperf/internal/report/flame"
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

type sliceView struct {
	Name, Color string
	Ms, Pct     string
	X, W        string
}

type barRow struct {
	Name, Detail              string
	Value, Pct                string
	W                         string
	Callers, Callees          []metrics.CallEdge
	SelfCommand, TotalCommand string
	Report                    *insights.Report
	Function                  metrics.FuncStat
	StackRef, StackLabel      string
}

type reportView struct {
	R                    *insights.Report
	Duration             string
	Slices               []sliceView
	Hotspots             []barRow
	Inclusive            []barRow
	CommonStacks         []metrics.StackStat
	Modules              []barRow
	SlowFrames           []insights.SlowFrame
	LongTasks            []insights.LongTask
	Findings             []findingView
	StackPreviews        []insights.StackPreview
	StackReferences      map[string]string
	MetricBase, RootBase string
	RootCause, Flame     template.HTML
	AnchorPrefix         string
}

type findingView struct {
	insights.Finding
	Impact    string
	StackHTML template.HTML
	Facets    []string
}

type pageView struct {
	Reports []reportView
	AI      template.HTML
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

func buildView(r *insights.Report, prefix string) (reportView, error) {
	if len(r.CommonStacks) == 0 && r.Summary != nil {
		copy := *r
		copy.CommonStacks = r.Summary.CommonStacks
		r = &copy
	}
	r = insights.SelectEvidence(r)
	v := reportView{R: r, Duration: fmt.Sprintf("%.2fs", r.DurationSec), StackPreviews: insights.StackPreviews(r, false), StackReferences: make(map[string]string), AnchorPrefix: prefix}
	stackMap := make(map[string]insights.StackPreview, len(v.StackPreviews))
	for _, preview := range v.StackPreviews {
		stackMap[preview.ID] = preview
		v.StackReferences[preview.ID] = preview.Reference()
	}
	v.MetricBase = strings.Replace(insights.NumericCommand(r, ""), " metrics ''", " metrics", 1)
	v.RootBase = strings.Replace(v.MetricBase, " metrics --format", " rootcause --format", 1)
	var err error
	v.RootCause, err = rootCauseHTML(r.RootCause, stackMap, prefix)
	if err != nil {
		return reportView{}, err
	}
	if r.FlameTree != nil {
		var fragment strings.Builder
		if err := flamehtml.HTMLFragment(&fragment, []flamehtml.Chart{{Label: r.Name, Tree: r.FlameTree}}, flamehtml.Options{}); err != nil {
			return reportView{}, err
		}
		v.Flame = template.HTML(fragment.String())
	}
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
	v.Hotspots = functionRows(r, r.Hotspots, false, v.StackReferences, prefix)
	v.Inclusive = functionRows(r, r.InclusiveHotspots, true, v.StackReferences, prefix)
	v.CommonStacks = r.CommonStacks
	for _, m := range r.Modules {
		v.Modules = append(v.Modules, barRow{Name: m.Name, Value: f1(m.SelfMs) + " ms", Pct: f1(m.Pct) + "%", W: f1(clampPct(m.Pct))})
	}
	v.SlowFrames = r.SlowFrames
	v.LongTasks = r.LongTasks
	for _, f := range r.Findings {
		var stacks strings.Builder
		for _, stack := range f.Stacks {
			id := rootStackID(stack)
			label := rootStackReference(stack)
			if preview, ok := stackMap[id]; ok {
				label = preview.Reference()
			}
			fmt.Fprintf(&stacks, "<p><a href=\"#stack-%s\">%s</a></p>", template.HTMLEscapeString(prefix+id), template.HTMLEscapeString(label))
		}
		v.Findings = append(v.Findings, findingView{Finding: f, Impact: fmt.Sprintf("%.1f ms (%.1f%%)", f.ImpactMs, f.ImpactPct), StackHTML: template.HTML(stacks.String()), Facets: insights.FacetLabels(f.NativeReads, f.Depth, f.RelatedFindings)})
	}
	return v, nil
}
func functionRows(r *insights.Report, functions []metrics.FuncStat, inclusive bool, references map[string]string, prefix string) []barRow {
	maxTime := 0.0
	for _, h := range functions {
		value := h.SelfMs
		if inclusive {
			value = h.TotalMs
		}
		maxTime = max(maxTime, value)
	}
	rows := make([]barRow, 0, len(functions))
	for _, h := range functions {
		name := h.Name
		if name == "" {
			name = "(anonymous)"
		}
		value := h.SelfMs
		if inclusive {
			value = h.TotalMs
		}
		width := 0.0
		if maxTime > 0 {
			width = value / maxTime * 100
		}
		stackID := h.CommonStackID
		if stackID == "" && len(h.CommonStack) > 0 {
			stackID = insights.MetricStackID(h.CommonStack)
		}
		stackRef := stackID
		if stackRef != "" {
			stackRef = prefix + stackRef
		}
		rows = append(rows, barRow{Name: name, Detail: h.Location(), Value: f1(h.SelfMs) + " ms", Pct: f1(h.TotalMs) + " ms", W: f1(width), Callers: h.Callers, Callees: h.Callees, SelfCommand: insights.FunctionCommand(r, h, "self_ms"), TotalCommand: insights.FunctionCommand(r, h, "total_ms"), Report: r, Function: h, StackRef: stackRef, StackLabel: references[stackID]})
	}
	return rows
}

// RenderFragment writes recording sections without a document wrapper.
// Callers must include the report stylesheet when embedding the fragment.
func RenderFragment(w io.Writer, reps []*insights.Report, namespace string) error {
	for i, r := range reps {
		if r != nil {
			v, err := buildView(r, fmt.Sprintf("%sreport-%d-", namespace, i))
			if err != nil {
				return err
			}
			if err := pageTmpl.ExecuteTemplate(w, "report", v); err != nil {
				return err
			}
		}
	}
	return nil
}

// Render writes a standalone HTML analysis of reps.
func Render(w io.Writer, reps []*insights.Report) error {
	return RenderWithAI(w, reps, "")
}

// RenderWithAI is Render plus an AI answer (Markdown text) appended as an
// escaped, preformatted-free section. Empty ai omits the section.
func RenderWithAI(w io.Writer, reps []*insights.Report, ai string) error {
	pv := pageView{}
	for i, r := range reps {
		if r != nil {
			v, err := buildView(r, fmt.Sprintf("report-%d-", i))
			if err != nil {
				return err
			}
			pv.Reports = append(pv.Reports, v)
		}
	}
	if strings.TrimSpace(ai) != "" {
		pv.AI = AISection(ai)
	}
	return pageTmpl.Execute(w, pv)
}

const CSS = `
:root{--bg:#f6f7f9;--card:#fff;--fg:#1c2430;--mut:#667085;--bd:#e1e5ea;--bar:#4b8bd9;--hi:#d9534f;--med:#e0a400;--lo:#4b8bd9;--info:#8a94a3;--code:#eef1f5}
@media (prefers-color-scheme:dark){:root{--bg:#12161c;--card:#1a2029;--fg:#e4e8ee;--mut:#97a1b0;--bd:#2b3340;--code:#232b36}}
*{box-sizing:border-box}
body{margin:0;background:var(--bg);color:var(--fg);font:14px/1.5 -apple-system,BlinkMacSystemFont,"Segoe UI",Roboto,sans-serif}
main{max-width:1000px;margin:0 auto;padding:24px 16px 64px}
h1{font-size:22px;margin:0 0 4px}h2{font-size:17px;margin:28px 0 10px}h3{font-size:14px;margin:16px 0 6px}
.mut{color:var(--mut)}.card{background:var(--card);border:1px solid var(--bd);border-radius:8px;padding:14px 16px;margin:10px 0}
table{width:100%;border-collapse:collapse}th,td{text-align:left;padding:5px 8px;border-bottom:1px solid var(--bd);vertical-align:middle;overflow-wrap:anywhere}
th{color:var(--mut);font-weight:600;font-size:12px}td.n,th.n{text-align:right;font-variant-numeric:tabular-nums}
.bar{background:var(--code);border-radius:3px;height:10px;min-width:80px}.bar i{display:block;height:100%;background:var(--bar);border-radius:3px}
.legend{display:flex;flex-wrap:wrap;gap:6px 18px;margin-top:10px;padding:0;list-style:none}
.legend span.sw{display:inline-block;width:10px;height:10px;border-radius:2px;margin-right:6px}
.badge{display:inline-block;padding:1px 8px;border-radius:10px;font-size:11px;font-weight:700;text-transform:uppercase;color:#fff}
.sev-high{background:var(--hi)}.sev-medium{background:var(--med)}.sev-low{background:var(--lo)}.sev-info{background:var(--info)}
.finding h3{display:flex;gap:10px;align-items:center;margin:0 0 6px}
.finding ul{margin:4px 0 8px 18px;padding:0}.sug{background:var(--code);border-radius:6px;padding:8px 10px}
code{background:var(--code);border-radius:3px;padding:0 4px;font-size:12.5px;white-space:pre-wrap;overflow-wrap:anywhere}
pre{max-width:100%;white-space:pre-wrap;overflow-wrap:anywhere}
.ai pre.code{background:var(--code);padding:10px;border-radius:6px;overflow:auto}
.ai{white-space:normal}.ai h4,.ai h5{margin:14px 0 4px}
svg{display:block;width:100%;height:auto}
`

func displayName(name string) string {
	if name == "" {
		return "(anonymous)"
	}
	return name
}

var pageTmpl = template.Must(template.New("page").Funcs(template.FuncMap{
	"name":                   displayName,
	"numeric":                insights.NumericCommand,
	"triggerCommand":         insights.TriggerCommand,
	"triggerFunctionCommand": insights.TriggerFunctionCommand,
	"edgeCommand":            insights.EdgeCommand,
	"stackCommand":           insights.StackCommand,
	"metricStackID":          insights.MetricStackID,
	"rootFrame":              rootFrameLabel,
}).Parse(`<!DOCTYPE html>
<html lang="en"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1">
<title>wkperf analysis</title><style>` + CSS + `</style></head><body><main>
<h1>wkperf analysis</h1>
{{range .Reports}}{{template "report" .}}{{else}}<p class="mut">No recordings analyzed.</p>{{end}}
{{if .AI}}<h2>AI analysis</h2><div class="card ai">{{.AI}}</div>{{end}}
</main></body></html>
{{define "report"}}
<section>
{{$r := .R}}
	{{$refs := .StackReferences}}
{{$prefix := .AnchorPrefix}}
<h2>{{if .R.Name}}{{.R.Name}}{{else}}{{.R.Source}}{{end}}</h2>
<div class="mut">{{.R.Source}} &middot; {{.Duration}}</div>
<p class="mut">Reproduce scalar values using the displayed key after its operation. Source, window and source maps apply throughout.</p><p>Metrics: <code>{{.MetricBase}}</code><br>Root cause: <code>{{.RootBase}}</code></p>
{{if .RootCause}}{{.RootCause}}{{end}}
{{if .Flame}}<h3>Sampled flame graph</h3>{{.Flame}}{{end}}

<h3>Where time goes</h3>
<div class="card">
{{if .Slices}}<svg viewBox="0 0 100 8" preserveAspectRatio="none" role="img" aria-label="Wall-time breakdown">
{{range .Slices}}<rect x="{{.X}}" y="0" width="{{.W}}" height="8" fill="{{.Color}}"><title>{{.Name}}: {{.Ms}} ms ({{.Pct}}%)</title></rect>{{end}}</svg>
<ul class="legend">{{range .Slices}}<li><span class="sw" style="background:{{.Color}}"></span>{{.Name}} <span class="mut">{{.Ms}} ms &middot; {{.Pct}}%</span></li>{{end}}</ul>
{{else}}<span class="mut">No breakdown available.</span>{{end}}
</div>
{{if .R.GCMs}}<h3>GC collections</h3><div class="card"><p>GC collector interval overlay: {{printf "%.1f" .R.GCMs}} ms <code>{{numeric $r "gc.total_ms"}}</code>; GC overlap with work: {{printf "%.1f" .R.GCOverlapWithWorkMs}} ms <code>{{numeric $r "gc.overlap_with_work_ms"}}</code>.</p><p class="mut">Collector intervals may run concurrently. This is not a measured pause or an exclusive main-thread work slice.</p></div>{{end}}

<h3>Findings</h3>
{{range .Findings}}<div class="card finding">
<h3><span class="badge sev-{{.Severity}}">{{.Severity}}</span>{{.Title}}</h3>
<div class="mut">{{.Category}} &middot; impact {{.Impact}} &middot; <code>{{.ID}}</code></div>
{{if .Evidence}}<ul>{{range .Evidence}}<li>{{.}}</li>{{end}}</ul>{{end}}
{{range .Facets}}<p>{{.}}</p>{{end}}
{{if .MetricKeys}}<div><b>Metric keys:</b> {{range .MetricKeys}}<code>{{.}}</code> {{end}}</div>{{end}}
{{if .NumericEvidence}}<table><tr><th>Numeric evidence</th><th class="n">Value</th><th>Basis / description</th><th>Reproduce</th></tr>{{range .NumericEvidence}}<tr><td><code>{{.MetricKey}}</code></td><td class="n">{{printf "%.3f" .Value}} {{.Unit}}</td><td>{{.Basis}} · {{.Description}}</td><td><code>{{.Command}}</code></td></tr>{{end}}</table>{{end}}
{{if .Confidence}}<div><b>Confidence:</b> {{.Confidence}} · <b>Evidence basis:</b> {{.EvidenceBasis}} · <b>Replication:</b> seen in {{.Replication.RunsWithFinding}} of {{.Replication.Runs}} runs ({{.Replication.ParsedRuns}} parsed){{range .ConfidenceReasons}} · {{.}}{{end}}</div>{{end}}
{{if .Verify}}<h4>Verify</h4><table><tr><th>Metric key</th><th>Direction</th><th class="n">Target</th><th>Check</th><th>Reproduce</th></tr>{{range .Verify}}<tr><td><code>{{.MetricKey}}</code></td><td>{{.Direction}}</td><td class="n">{{printf "%.3f" .Target}} {{.Unit}}</td><td>{{.Description}}</td><td><code>{{.Command}}</code></td></tr>{{end}}</table>{{end}}
{{if .SuggestedWindows}}<h4>Suggested windows</h4><ul>{{range .SuggestedWindows}}<li>{{printf "%.3f" .FromSec}}–{{printf "%.3f" .ToSec}} s: {{.Reason}} <code>{{range .Flags}}{{.}} {{end}}</code></li>{{end}}</ul>{{end}}
{{if .Functions}}<div><b>Functions:</b> {{range .Functions}}<code>{{.}}</code> {{end}}</div>{{end}}
{{if .Triggers}}<div><b>Triggers:</b> {{range .Triggers}}<code>{{.}}</code> {{end}}</div>{{end}}
{{if .Nodes}}<h4>Recorded nodes</h4><ul>{{range .Nodes}}<li>{{.EventType}}: <code>{{.CSSPath}}</code> {{.DisplayName}}</li>{{end}}</ul><div class="mut">Recorded node identifiers do not provide stylesheet source locations.</div>{{end}}
{{if .Command}}<div><b>Reproduce:</b> <code>{{.Command}}</code></div>{{end}}
{{if .StackHTML}}{{.StackHTML}}{{end}}
{{if .Suggestion}}<div class="sug"><b>Suggestion:</b> {{.Suggestion}}</div>{{end}}
</div>{{else}}<div class="card mut">No findings.</div>{{end}}

<h3>Hotspot functions (self time)</h3>
<div class="card">{{if .Hotspots}}<table><tr><th>Function</th><th>Location</th><th class="n">Self</th><th class="n">Total</th><th></th></tr>
{{range .Hotspots}}<tr><td><code>{{.Name}}</code>{{template "edges" .}}</td><td class="mut">{{.Detail}}</td><td class="n">{{.Value}}<div><code>{{.SelfCommand}}</code></div></td><td class="n">{{.Pct}}<div><code>{{.TotalCommand}}</code></div></td><td><div class="bar"><i style="width:{{.W}}%"></i></div></td></tr>{{end}}</table>
{{else}}<span class="mut">No sampling data.</span>{{end}}</div>

<h3>Inclusive hotspot functions</h3>
<div class="card">{{if .Inclusive}}<p class="mut">Ranked by inclusive sampled time. Parent totals overlap child totals; do not sum this table.</p><table><tr><th>Function / call context</th><th>Location</th><th class="n">Self</th><th class="n">Inclusive</th><th></th></tr>
{{range .Inclusive}}<tr><td><code>{{.Name}}</code>{{template "edges" .}}</td><td class="mut">{{.Detail}}</td><td class="n">{{.Value}}<div><code>{{.SelfCommand}}</code></div></td><td class="n">{{.Pct}}<div><code>{{.TotalCommand}}</code></div></td><td><div class="bar"><i style="width:{{.W}}%"></i></div></td></tr>{{end}}</table>
{{else}}<span class="mut">No inclusive sampling data.</span>{{end}}</div>

<h3>Common stacks</h3>
<div class="card">{{if .CommonStacks}}<table><tr><th>Root → leaf stack</th><th class="n">Sample weight ms</th><th class="n">Samples</th></tr>
{{range $index, $stack := .CommonStacks}}{{$id := $stack.ID}}{{if not $id}}{{$id = metricStackID $stack.Frames}}{{end}}<tr><td><a href="#stack-{{$prefix}}{{$id}}">{{index $refs $id}}</a></td><td class="n">{{printf "%.3f" .WeightMs}}<div><code>{{stackCommand $r $index "weight_ms"}}</code></div></td><td class="n">{{.Samples}}<div><code>{{stackCommand $r $index "samples"}}</code></div></td></tr>{{end}}</table>
{{else}}<span class="mut">No stack samples.</span>{{end}}</div>

<h3>Trigger timing</h3>
<div class="card">{{if .R.Triggers}}<table><tr><th>Kind</th><th>Trigger</th><th class="n">Count</th><th class="n">Total ms</th><th class="n">Mean ms</th><th class="n">P95 ms</th><th class="n">Max ms</th></tr>
{{range .R.Triggers}}<tr><td>{{.Kind}}</td><td>{{.Name}}</td><td class="n">{{.Count}}<div><code>{{triggerCommand $r . "count"}}</code></div></td><td class="n">{{printf "%.3f" .TotalMs}}<div><code>{{triggerCommand $r . "total_ms"}}</code></div></td><td class="n">{{printf "%.3f" .MeanMs}}<div><code>{{triggerCommand $r . "mean_ms"}}</code></div></td><td class="n">{{printf "%.3f" .P95Ms}}<div><code>{{triggerCommand $r . "p95_ms"}}</code></div></td><td class="n">{{printf "%.3f" .MaxMs}}<div><code>{{triggerCommand $r . "max_ms"}}</code></div></td></tr>{{end}}</table>
{{else}}<span class="mut">No script trigger records.</span>{{end}}</div>

<h3>Trigger code attribution</h3>
<div class="card"><p class="mut">Sample overlap links code to trigger windows; overlap alone does not prove causation.</p>
{{range .R.Triggers}}{{$trigger := .}}<h4>{{.Kind}}: {{.Name}}</h4>{{if .SampleMessage}}<p class="mut">{{.SampleMessage}}</p>{{end}}
<table><tr><th>Ranking</th><th>Function</th><th>Generated → original location</th><th class="n">Sampled ms</th></tr>
{{range .TopSelf}}<tr><td>Self</td><td><code>{{name .Name}}</code></td><td>{{.Location}} · {{.SelfSamples}} self samples</td><td class="n">{{printf "%.3f" .SelfMs}}<div><code>{{triggerFunctionCommand $r $trigger . "self_ms"}}</code></div></td></tr>{{end}}
{{range .TopInclusive}}<tr><td>Inclusive</td><td><code>{{name .Name}}</code></td><td>{{.Location}} · {{.SelfSamples}} self samples</td><td class="n">{{printf "%.3f" .TotalMs}}<div><code>{{triggerFunctionCommand $r $trigger . "total_ms"}}</code></div></td></tr>{{end}}
</table>{{else}}<span class="mut">No trigger attribution data.</span>{{end}}</div>
<p class="mut">Wakeups: {{.R.Wakeups.Count}} callbacks <code>{{numeric $r "wakeups.count"}}</code>; {{printf "%.3f" .R.Wakeups.PerSecond}} per second <code>{{numeric $r "wakeups.per_second"}}</code>. {{.R.SampleCoverage.Interpretation}}</p>
{{with .R.SampleCoverage}}{{if .UntimedSampleCount}}<p class="mut">Untimed samples: {{.UntimedSampleCount}} ({{printf "%.1f" .UntimedMs}} ms): {{if .UntimedExcluded}}excluded from this window{{else}}included in rankings, excluded from frame, task, trigger, window and periodic attribution{{end}}.</p>{{end}}{{end}}

<h3>Modules</h3>
<div class="card">{{if .Modules}}<table><tr><th>Module</th><th class="n">Self</th><th class="n">Share</th><th></th></tr>
{{range .Modules}}<tr><td><code>{{.Name}}</code></td><td class="n">{{.Value}}</td><td class="n">{{.Pct}}</td><td><div class="bar"><i style="width:{{.W}}%"></i></div></td></tr>{{end}}</table>
{{else}}<span class="mut">No module data.</span>{{end}}</div>

<h3>Slow frames</h3>
<p class="mut">Work-bound active intervals above {{printf "%.1f" .R.JankMs}} ms; {{len .SlowFrames}}/{{.R.SlowFramesTotal}} shown. Fixed scorecard frame standards use 16.7 ms. GC remains a separate collector overlay.</p>
<div class="card">{{if .SlowFrames}}<table><tr><th class="n">At (s)</th><th class="n">Observed interval ms</th><th class="n">Main-thread work ms</th><th class="n">Script</th><th class="n">Layout</th><th class="n">Paint</th><th class="n">GC collector interval</th><th class="n">GC overlap with work</th><th>Top event / function</th></tr>
{{range .SlowFrames}}<tr><td class="n">{{printf "%.2f" .T}}</td><td class="n">{{printf "%.1f" .Ms}}</td><td class="n">{{printf "%.1f" .WorkMs}}</td><td class="n">{{printf "%.1f" .ScriptMs}}</td><td class="n">{{printf "%.1f" .LayoutMs}}</td><td class="n">{{printf "%.1f" .PaintMs}}</td><td class="n">{{printf "%.1f" .GCMs}}</td><td class="n">{{printf "%.1f" .GCOverlapWithWorkMs}}</td><td>{{.TopEvent}}{{if .TopFunc}} &rarr; <code>{{.TopFunc}}</code>{{end}}{{if .SampleStatus}}<div class="mut">{{.SampleStatus}}</div>{{end}}</td></tr>{{end}}</table>
{{else}}<span class="mut">No slow frames.</span>{{end}}</div>

{{if .R.InputStalls}}<h3>Input stalls</h3>
<p class="mut">Input arrived without a timely rendering update. These observed intervals are excluded from work-bound frame statistics and jank; interval length is not measured main-thread blocking time. {{len .R.InputStalls}}/{{.R.InputStallsTotal}} shown.</p>
<div class="card"><table><tr><th class="n">At (s)</th><th class="n">Observed interval ms</th><th class="n">Main-thread work ms</th><th>Top event / function</th></tr>
{{range .R.InputStalls}}<tr><td class="n">{{printf "%.2f" .T}}</td><td class="n">{{printf "%.1f" .Ms}}</td><td class="n">{{printf "%.3f" .WorkMs}}</td><td>{{.TopEvent}}{{if .TopFunc}} &rarr; <code>{{.TopFunc}}</code>{{end}}{{if .SampleStatus}}<div class="mut">{{.SampleStatus}}</div>{{end}}</td></tr>{{end}}</table></div>{{end}}

<h3>Long tasks</h3>
<p class="mut">Maximal overlapping script tasks above {{printf "%.1f" .R.LongTaskMs}} ms; {{len .LongTasks}}/{{.R.LongTasksTotal}} shown. Nested events do not count as separate tasks.</p>
<div class="card">{{if .LongTasks}}<table><tr><th class="n">At (s)</th><th class="n">ms</th><th>Event</th><th>Detail</th><th>Top function</th></tr>
{{range .LongTasks}}<tr><td class="n">{{printf "%.2f" .T}}</td><td class="n">{{printf "%.1f" .Ms}}</td><td>{{.EventType}}{{if .Children}}<div class="mut">Nested events: {{len .Children}}/{{.ChildrenTotal}} shown · {{range $i, $child := .Children}}{{if $i}}, {{end}}{{$child}}{{end}}</div>{{end}}</td><td>{{.Detail}}</td><td>{{if .TopFunc}}<code>{{.TopFunc}}</code>{{end}}{{if .SampleStatus}}<div class="mut">{{.SampleStatus}}</div>{{end}}</td></tr>{{end}}</table>
{{else}}<span class="mut">No long tasks.</span>{{end}}</div>

{{if .StackPreviews}}<h3>Stack appendix</h3><p class="mut">Each distinct selected stack appears once. Query returns every exact frame.</p>
{{range .StackPreviews}}<details id="stack-{{$prefix}}{{.ID}}"><summary>{{.Label}} · <code>{{.ID}}</code> · {{.Depth}} frames</summary><ol>{{range .Root}}<li><code>{{rootFrame .}}</code></li>{{end}}{{if .Elided}}<li>… {{.Elided}} frames elided.</li>{{end}}{{range .Leaf}}<li><code>{{rootFrame .}}</code></li>{{end}}</ol><p>Full stack: <code>{{.Command}}</code></p></details>{{end}}{{end}}
</section>
{{end}}
{{define "edges"}}
{{$report := .Report}}{{$function := .Function}}
{{if .Function.ID}}<div class="mut">ID <code>{{.Function.ID}}</code></div>{{end}}
<div class="mut">{{.Function.SelfSamples}} self samples</div>
{{if .StackRef}}<p>Common stack: <a href="#stack-{{.StackRef}}">{{.StackLabel}}</a></p>{{end}}
{{if .Callers}}<details><summary>Callers</summary><ul>{{range .Callers}}<li><code>{{name .Function.Name}} @ {{.Function.Location}}</code> — {{printf "%.3f" .WeightMs}} ms <code>{{edgeCommand $report $function . "callers" "weight_ms"}}</code>, {{.Samples}} samples <code>{{edgeCommand $report $function . "callers" "samples"}}</code></li>{{end}}</ul></details>{{end}}
{{if .Callees}}<details><summary>Callees</summary><ul>{{range .Callees}}<li><code>{{name .Function.Name}} @ {{.Function.Location}}</code> — {{printf "%.3f" .WeightMs}} ms <code>{{edgeCommand $report $function . "callees" "weight_ms"}}</code>, {{.Samples}} samples <code>{{edgeCommand $report $function . "callees" "samples"}}</code></li>{{end}}</ul></details>{{end}}
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
