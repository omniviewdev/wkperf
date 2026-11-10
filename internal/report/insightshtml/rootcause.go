package insightshtml

import (
	"bytes"
	"fmt"
	"html/template"
	"io"

	"github.com/omniviewdev/wkperf/internal/flame"
	"github.com/omniviewdev/wkperf/internal/insights"
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

func stackTime(value *float64) string {
	if value == nil {
		return "unknown"
	}
	return fmt.Sprintf("%.3f", *value)
}

func rootCauseHTML(r *rootcause.Report, stacks map[string]insights.StackPreview, prefix string) (template.HTML, error) {
	if r == nil {
		return "", nil
	}
	tmpl, err := rootTmpl.Clone()
	if err != nil {
		return "", err
	}
	tmpl.Funcs(template.FuncMap{"stackID": func(stack rootcause.Stack) string { return prefix + rootStackID(stack) }, "stackRef": func(stack rootcause.Stack) string {
		if preview, ok := stacks[rootStackID(stack)]; ok {
			return preview.Reference()
		}
		return rootStackReference(stack)
	}})
	var b bytes.Buffer
	if err := tmpl.Execute(&b, r); err != nil {
		return "", err
	}
	return template.HTML(b.String()), nil
}

// RenderStacks writes compact references to exact sampled stacks. Analysis
// fragments carry their distinct, bounded frame previews in the stack appendix.
func RenderStacks(w io.Writer, stacks []rootcause.Stack, prefix string) error {
	if len(stacks) == 0 {
		return nil
	}
	tmpl, err := rootTmpl.Clone()
	if err != nil {
		return err
	}
	tmpl.Funcs(template.FuncMap{"stackID": func(stack rootcause.Stack) string { return prefix + rootStackID(stack) }})
	for _, stack := range stacks {
		if err := tmpl.ExecuteTemplate(w, "stack", stack); err != nil {
			return err
		}
	}
	return nil
}

func rootStackID(stack rootcause.Stack) string {
	if stack.StackID != "" {
		return stack.StackID
	}
	return flame.StackID(stack.Frames)
}

func rootStackReference(stack rootcause.Stack) string {
	leaf := ""
	if len(stack.Frames) > 0 {
		leaf = rootFrameLabel(stack.Frames[len(stack.Frames)-1])
	}
	return fmt.Sprintf("stack %s (%d frames; leaf %s)", rootStackID(stack), len(stack.Frames), leaf)
}

type pathPreview struct {
	Root, Leaf []flame.Frame
	Elided     int
}

var rootTmpl = template.Must(template.New("rootcause").Funcs(template.FuncMap{
	"frame": rootFrameLabel, "time": stackTime, "stackID": rootStackID, "stackRef": rootStackReference,
	"preview": func(frames []flame.Frame) pathPreview {
		root, leaf, elided := insights.PreviewFrames(frames, false)
		return pathPreview{Root: root, Leaf: leaf, Elided: elided}
	},
}).Parse(`
<p class="mut">Selected evidence shown/total: application attributions {{len .AppAttribution}}/{{.AppAttributionTotal}}; periodic roots {{len .PeriodicRoots}}/{{.PeriodicRootsTotal}}; depth outliers {{len .Depth.Outliers}}/{{.Depth.OutliersTotal}}; long tasks {{len .LongTasks}}/{{.LongTasksTotal}}; slow frames {{len .SlowFrames}}/{{.SlowFramesTotal}}; input stalls {{len .InputStalls}}/{{.InputStallsTotal}}; event bursts {{len .EventBursts}}/{{.EventBurstsTotal}}.</p>
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
<div class="card">{{range .PeriodicRoots}}<h4><code>{{frame .Root}}</code></h4><p>{{.FireCount}} fires · period {{printf "%.3f" .PeriodMs}} ms · jitter {{printf "%.3f" .JitterMs}} ms · regularity {{printf "%.3f" .Regularity}} · mean cost {{printf "%.3f" .MeanCostMs}} ms · total {{printf "%.3f" .TotalMs}} ms</p>{{if .SampleStatus}}<p class="mut">{{.SampleStatus}}</p>{{end}}{{template "stats" .Stats}}{{template "command" .Command}}<table><tr><th>From s</th><th>To s</th><th>Self ms</th><th>Samples</th><th>Event / reproduce</th></tr>{{range .Fires}}<tr><td>{{printf "%.3f" .FromSec}}</td><td>{{printf "%.3f" .ToSec}}</td><td>{{printf "%.3f" .SelfMs}}</td><td>{{.Samples}}</td><td>{{.Event}}{{template "stats" .Stats}}{{template "command" .Command}}</td></tr>{{end}}</table>{{range .Evidence}}{{template "stack" .}}{{end}}{{else}}<p class="mut">No periodic sampled root.</p>{{end}}</div>
<h3>GC collector interval overlap</h3>
<div class="card">{{range .GCOverlaps}}<h4>{{printf "%.3f" .FromSec}}–{{printf "%.3f" .ToSec}} s · {{printf "%.3f" .DurationMs}} ms</h4><p>{{.Attribution}}</p>{{template "stats" .Stats}}{{template "command" .Command}}<table><tr><th>Overlapping event</th><th>From s</th><th>To s</th><th>Overlap ms</th><th>Reproduce</th></tr>{{range .Events}}<tr><td>{{.Event}}</td><td>{{printf "%.3f" .FromSec}}</td><td>{{printf "%.3f" .ToSec}}</td><td>{{printf "%.3f" .OverlapMs}}</td><td>{{template "stats" .Stats}}<code>{{.Command}}</code></td></tr>{{end}}</table>{{else}}<p class="mut">No instrumented GC overlap.</p>{{end}}</div>
<h3>Stack depth</h3>
<div class="card"><p>Median {{printf "%.3f" .Depth.Median}} · p95 {{printf "%.3f" .Depth.P95}} · p99 {{printf "%.3f" .Depth.P99}} · max {{.Depth.Max}} · outlier threshold {{printf "%.3f" .Depth.OutlierThreshold}} · repeated-frame depth {{.Depth.MaxRepeatedFrameDepth}} · commit depth {{.Depth.MaxCommitDepth}}</p>{{template "stats" .Depth.Stats}}{{template "command" .Depth.Command}}{{range .Depth.Outliers}}<details><summary>Outlier depth {{.Depth}} · repeated {{.RepeatedFrameDepth}} · commit {{.CommitDepth}}</summary>{{template "stats" .Stats}}{{template "stack" .Stack}}{{template "command" .Command}}</details>{{end}}</div>
<h3>Worst-window triage</h3>
<div class="card">{{range .Windows}}{{template "window" .}}{{else}}<p class="mut">No worst windows.</p>{{end}}</div>
{{if .LongTasks}}<h4>Long-task windows</h4><div class="card">{{range .LongTasks}}{{template "window" .}}{{end}}</div>{{end}}
{{if .SlowFrames}}<h4>Slow-frame windows</h4><div class="card">{{range .SlowFrames}}{{template "window" .}}{{end}}</div>{{end}}
{{if .InputStalls}}<h4>Input-stall windows</h4><p class="mut">Observed input latency, not measured main-thread blocking time.</p><div class="card">{{range .InputStalls}}{{template "window" .}}{{end}}</div>{{end}}
{{if .EventBursts}}<h4>Event-burst windows</h4><div class="card">{{range .EventBursts}}{{template "window" .}}{{end}}</div>{{end}}
{{define "command"}}{{if .}}<div><b>Reproduce:</b> <code>{{.}}</code></div>{{end}}{{end}}
{{define "stats"}}{{if .}}<table><tr><th>Root-cause metric key</th><th class="n">Value</th><th>Basis</th></tr>{{range .}}<tr><td><code>{{.Key}}</code> {{.Label}}</td><td class="n">{{printf "%.3f" .Value}} {{.Unit}}</td><td>{{.Basis}}</td></tr>{{end}}</table>{{end}}{{end}}
{{define "path"}}<p>{{printf "%.3f" .SampledMs}} sampled ms · {{.Samples}} samples</p><ol>{{with preview .Frames}}{{range .Root}}<li><code>{{frame .}}</code></li>{{end}}{{if .Elided}}<li>… {{.Elided}} frames elided; query returns the complete path.</li>{{end}}{{range .Leaf}}<li><code>{{frame .}}</code></li>{{end}}{{end}}</ol>{{template "stats" .Stats}}{{template "command" .Command}}{{end}}
{{define "stack"}}<p><a href="#stack-{{stackID .}}">{{stackRef .}}</a>: {{printf "%.3f" .SelfMs}} self ms · {{.Samples}} samples · {{if .FirstSec}}{{time .FirstSec}}–{{time .LastSec}} s{{else}}time unknown{{end}}{{if .UntimedSamples}} · {{.UntimedSamples}} untimed samples{{end}}</p>{{end}}
{{define "window"}}<h4>{{.Kind}}: {{.Label}} · {{printf "%.3f" .FromSec}}–{{printf "%.3f" .ToSec}} s</h4><p>{{printf "%.3f" .DurationMs}} observed interval ms · {{printf "%.3f" .ActiveMs}} instrumented main-thread work ms · {{.SampleStatus}}</p>{{template "stats" .Stats}}{{template "command" .Command}}{{with .TopStack}}{{template "stack" .}}{{end}}{{end}}
`))
