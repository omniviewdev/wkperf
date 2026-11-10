package insightshtml

import (
	"bytes"
	"html/template"
	"io"

	"github.com/omniviewdev/wkperf/internal/flame"
	"github.com/omniviewdev/wkperf/internal/metrics"
	"github.com/omniviewdev/wkperf/internal/rootcause"
)

func rootFrameLabel(f flame.Frame) string {
	frame := metrics.FunctionFrame{Name: f.Name, URL: f.URL, Line: f.Line, Column: f.Column, SourceID: f.SourceID}
	if f.Original != nil {
		frame.Original = &metrics.SourceLocation{Name: f.Original.Name, URL: f.Original.URL, Line: f.Original.Line, Column: f.Original.Column}
	}
	name := f.Name
	if name == "" {
		name = "(anonymous)"
	}
	return name + " @ " + frame.Location()
}

func rootCauseHTML(r *rootcause.Report) (template.HTML, error) {
	if r == nil {
		return "", nil
	}
	var b bytes.Buffer
	if err := rootTmpl.Execute(&b, r); err != nil {
		return "", err
	}
	return template.HTML(b.String()), nil
}

// RenderStacks writes exact sampled-stack evidence without a document wrapper.
func RenderStacks(w io.Writer, stacks []rootcause.Stack) error {
	for _, stack := range stacks {
		if err := rootTmpl.ExecuteTemplate(w, "stack", stack); err != nil {
			return err
		}
	}
	return nil
}

var rootTmpl = template.Must(template.New("rootcause").Funcs(template.FuncMap{"frame": rootFrameLabel}).Parse(`
<h3>Hot path</h3>
<div class="card"><p class="mut">Heaviest sampled prefix path, root → leaf. Sample weight estimates execution cost, not instrumented wall time.</p>{{template "path" .HotPath}}{{template "stats" .Stats}}</div>
<h3>Owner breakdown</h3>
<div class="card"><p class="mut">Exclusive sampled self time by owner. Inclusive parent and child costs are not added.</p>
{{range .Owners}}<h4>{{.Category}}</h4><p>{{printf "%.3f" .SelfMs}} ms · {{printf "%.3f" .Pct}}% · {{.Samples}} samples</p>{{template "stats" .Stats}}{{template "command" .Command}}{{else}}<p class="mut">No owner samples.</p>{{end}}</div>
<h3>Nearest application attribution</h3>
<div class="card"><p class="mut">Framework/native self cost is charged to the closest application ancestor in the exact sampled stack. This is attribution, not proof of causation.</p>
{{range .AppAttribution}}<h4><code>{{frame .App}}</code></h4><table><tr><th>App self ms</th><th>Framework ms</th><th>Native ms</th><th>Total attributed ms</th></tr><tr><td>{{printf "%.3f" .SelfMs}}</td><td>{{printf "%.3f" .FrameworkMs}}</td><td>{{printf "%.3f" .NativeMs}}</td><td>{{printf "%.3f" .TotalMs}}</td></tr></table>{{template "stats" .Stats}}{{template "command" .Command}}{{range .Stacks}}{{template "stack" .}}{{end}}{{else}}<p class="mut">No application ancestor attribution.</p>{{end}}</div>
<h3>Exact-stack findings</h3>
{{range .Findings}}<div class="card"><h4>{{.Title}} <span class="mut">{{.Severity}} · {{.ID}}</span></h4><p><b>Hypothesis / interpretation:</b> {{.Explanation}}</p>{{template "stats" .Stats}}{{template "command" .Command}}{{range .Evidence}}{{template "stack" .}}{{end}}{{range .Windows}}<p>Window {{printf "%.3f" .FromSec}}–{{printf "%.3f" .ToSec}} s</p>{{template "command" .Command}}{{end}}</div>{{else}}<div class="card mut">No exact-stack findings.</div>{{end}}
<h3>Periodic roots</h3>
<div class="card">{{range .PeriodicRoots}}<h4><code>{{frame .Root}}</code></h4><p>{{.FireCount}} fires · period {{printf "%.3f" .PeriodMs}} ms · jitter {{printf "%.3f" .JitterMs}} ms · regularity {{printf "%.3f" .Regularity}} · mean cost {{printf "%.3f" .MeanCostMs}} ms · total {{printf "%.3f" .TotalMs}} ms</p>{{template "stats" .Stats}}{{template "command" .Command}}<table><tr><th>From s</th><th>To s</th><th>Self ms</th><th>Samples</th><th>Event / reproduce</th></tr>{{range .Fires}}<tr><td>{{printf "%.3f" .FromSec}}</td><td>{{printf "%.3f" .ToSec}}</td><td>{{printf "%.3f" .SelfMs}}</td><td>{{.Samples}}</td><td>{{.Event}}{{template "stats" .Stats}}{{template "command" .Command}}</td></tr>{{end}}</table>{{range .Evidence}}{{template "stack" .}}{{end}}{{else}}<p class="mut">No periodic sampled root.</p>{{end}}</div>
<h3>GC overlap</h3>
<div class="card">{{range .GCOverlaps}}<h4>{{printf "%.3f" .FromSec}}–{{printf "%.3f" .ToSec}} s · {{printf "%.3f" .DurationMs}} ms</h4><p>{{.Attribution}}</p>{{template "stats" .Stats}}{{template "command" .Command}}<table><tr><th>Overlapping event</th><th>From s</th><th>To s</th><th>Overlap ms</th><th>Reproduce</th></tr>{{range .Events}}<tr><td>{{.Event}}</td><td>{{printf "%.3f" .FromSec}}</td><td>{{printf "%.3f" .ToSec}}</td><td>{{printf "%.3f" .OverlapMs}}</td><td>{{template "stats" .Stats}}<code>{{.Command}}</code></td></tr>{{end}}</table>{{else}}<p class="mut">No instrumented GC overlap.</p>{{end}}</div>
<h3>Stack depth</h3>
<div class="card"><p>Median {{printf "%.3f" .Depth.Median}} · p95 {{printf "%.3f" .Depth.P95}} · p99 {{printf "%.3f" .Depth.P99}} · max {{.Depth.Max}} · outlier threshold {{printf "%.3f" .Depth.OutlierThreshold}} · repeated-frame depth {{.Depth.MaxRepeatedFrameDepth}} · commit depth {{.Depth.MaxCommitDepth}}</p>{{template "stats" .Depth.Stats}}{{template "command" .Depth.Command}}{{range .Depth.Outliers}}<details><summary>Outlier depth {{.Depth}} · repeated {{.RepeatedFrameDepth}} · commit {{.CommitDepth}}</summary>{{template "stats" .Stats}}{{template "stack" .Stack}}{{template "command" .Command}}</details>{{end}}</div>
<h3>Worst-window triage</h3>
<div class="card">{{range .Windows}}{{template "window" .}}{{else}}<p class="mut">No worst windows.</p>{{end}}</div>
{{if .LongTasks}}<h4>Long-task windows</h4><div class="card">{{range .LongTasks}}{{template "window" .}}{{end}}</div>{{end}}
{{if .SlowFrames}}<h4>Slow-frame windows</h4><div class="card">{{range .SlowFrames}}{{template "window" .}}{{end}}</div>{{end}}
{{if .EventBursts}}<h4>Event-burst windows</h4><div class="card">{{range .EventBursts}}{{template "window" .}}{{end}}</div>{{end}}
{{define "command"}}{{if .}}<div><b>Reproduce:</b> <code>{{.}}</code></div>{{end}}{{end}}
{{define "stats"}}{{if .}}<table><tr><th>Metric key</th><th class="n">Value</th><th>Basis</th><th>Reproduce</th></tr>{{range .}}<tr><td><code>{{.Key}}</code> {{.Label}}</td><td class="n">{{printf "%.3f" .Value}} {{.Unit}}</td><td>{{.Basis}}</td><td><code>{{.Command}}</code></td></tr>{{end}}</table>{{end}}{{end}}
{{define "path"}}<p>{{printf "%.3f" .SampledMs}} sampled ms · {{.Samples}} samples</p><ol>{{range .Frames}}<li><code>{{frame .}}</code></li>{{end}}</ol>{{template "stats" .Stats}}{{template "command" .Command}}{{end}}
{{define "stack"}}<details open><summary>Exact stack: {{printf "%.3f" .SelfMs}} self ms · {{.Samples}} samples · {{printf "%.3f" .FirstSec}}–{{printf "%.3f" .LastSec}} s</summary><ol>{{range .Frames}}<li><code>{{frame .}}</code></li>{{end}}</ol>{{template "stats" .Stats}}{{template "command" .Command}}</details>{{end}}
{{define "window"}}<h4>{{.Kind}}: {{.Label}} · {{printf "%.3f" .FromSec}}–{{printf "%.3f" .ToSec}} s</h4><p>{{printf "%.3f" .DurationMs}} duration ms · {{printf "%.3f" .ActiveMs}} active ms · {{.SampleStatus}}</p>{{template "stats" .Stats}}{{template "command" .Command}}{{with .HotPath}}{{template "path" .}}{{end}}{{with .TopStack}}{{template "stack" .}}{{end}}{{end}}
`))
