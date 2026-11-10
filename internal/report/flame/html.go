package flame

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"html"
	"io"
)

//go:embed flame.js
var script string

const style = `.wk-flame{position:relative;background:#111827;color:#e5e7eb;border:1px solid #334155;border-radius:12px;padding:24px;margin:20px 0;font:14px/1.5 system-ui,-apple-system,sans-serif;box-sizing:border-box}.wk-flame *{box-sizing:border-box}.wk-flame h2{font-size:24px;letter-spacing:-.03em;margin:0 0 6px}.wk-flame h3{font-size:16px;margin:24px 0 4px;overflow-wrap:anywhere}.wk-flame p{color:#a8b4c7;margin:6px 0}.wk-flame-controls{display:flex;flex-wrap:wrap;gap:12px;align-items:end;margin:20px 0 12px}.wk-flame-controls label{display:flex;flex-direction:column;gap:4px;font-size:12px;color:#cbd5e1}.wk-flame-controls label.wk-flame-toggle{flex-direction:row;align-items:center;align-self:center}.wk-flame input{background:#1e293b;color:#f8fafc;border:1px solid #475569;border-radius:5px;padding:7px;min-width:0;font:inherit}.wk-flame input[type=number]{width:88px}.wk-flame input[type=checkbox]{accent-color:#60a5fa}.wk-flame input[aria-invalid=true]{border-color:#f87171}.wk-flame-controls button,.wk-flame-breadcrumb button{background:#243247;border:1px solid #475569;border-radius:5px;color:#e2e8f0;padding:7px 11px;cursor:pointer}.wk-flame button:focus-visible,.wk-flame input:focus-visible{outline:3px solid #38bdf8;outline-offset:2px}.wk-flame-legend{display:flex;gap:15px;flex-wrap:wrap;font-size:12px;color:#cbd5e1}.wk-flame-legend span:before{content:"";display:inline-block;width:10px;height:10px;border-radius:3px;margin-right:5px;background:var(--swatch)}.wk-flame-status{min-height:22px;font-size:12px}.wk-flame-breadcrumb{display:flex;gap:5px;flex-wrap:wrap;margin:10px 0}.wk-flame-breadcrumb button{font-size:11px;padding:2px 7px;max-width:260px;overflow:hidden;text-overflow:ellipsis;white-space:nowrap}.wk-flame-summary{font-size:12px;color:#9caec6}.wk-flame-canvas{position:relative;width:100%;min-height:27px;border:1px solid #334155;border-radius:4px;background:#0b1220;overflow:hidden;margin-top:10px}.wk-flame-node{position:absolute;height:24px;overflow:hidden;white-space:nowrap;text-overflow:ellipsis;text-align:left;font:11px/22px ui-monospace,SFMono-Regular,Consolas,monospace;border:1px solid #111827;border-radius:2px;background:#60a5fa;color:#0b1220;padding:0 5px;cursor:pointer;min-width:0}.wk-flame-node:hover{filter:brightness(1.2);z-index:2}.wk-flame-framework{background:#a78bfa}.wk-flame-native{background:#94a3b8}.wk-flame-gc{background:#fbbf24}.wk-flame-match{box-shadow:inset 0 0 0 3px #fef08a;filter:brightness(1.15);z-index:1}.wk-flame-tooltip{position:absolute;z-index:20;pointer-events:none;background:#020617;color:#e2e8f0;border:1px solid #64748b;border-radius:7px;padding:12px;box-shadow:0 8px 24px #0008;white-space:pre-wrap;overflow-wrap:anywhere;max-width:min(520px,95%);font:12px/1.6 ui-monospace,SFMono-Regular,monospace}.wk-flame-empty{padding:14px;border:1px dashed #475569;border-radius:6px}.wk-flame-note{font-size:12px}`

type htmlTree struct {
	Root *viewNode `json:"root"`
}
type htmlChart struct {
	Label    string   `json:"label"`
	Tree     htmlTree `json:"tree"`
	Inverted htmlTree `json:"inverted"`
}
type htmlData struct {
	Mode   string      `json:"mode"`
	MinMS  float64     `json:"minMs"`
	Charts []htmlChart `json:"charts"`
}

type textWriter struct {
	w   io.Writer
	err error
}

func (w *textWriter) text(s string) {
	if w.err == nil {
		_, w.err = io.WriteString(w.w, s)
	}
}
func (w *textWriter) printf(format string, args ...any) {
	if w.err == nil {
		_, w.err = fmt.Fprintf(w.w, format, args...)
	}
}

// RenderHTML writes a standalone page without network dependencies.
func RenderHTML(w io.Writer, charts []Chart, opts Options) error {
	if err := validate(charts, opts); err != nil {
		return err
	}
	out := &textWriter{w: w}
	out.printf("<!doctype html>\n<html lang=\"en\"><head><meta charset=\"utf-8\"><meta name=\"viewport\" content=\"width=device-width,initial-scale=1\"><title>%s</title><style>body{margin:0;padding:24px;background:#080e19}main{max-width:1600px;margin:auto}@media(max-width:700px){body{padding:8px}}</style></head><body><main>\n", html.EscapeString(title(opts)))
	if out.err == nil {
		out.err = HTMLFragment(w, charts, opts)
	}
	out.text("</main></body></html>\n")
	return out.err
}

// HTMLFragment scopes controls to its own section; multiple fragments need no IDs.
func HTMLFragment(w io.Writer, charts []Chart, opts Options) error {
	if err := validate(charts, opts); err != nil {
		return err
	}
	data := htmlData{Mode: mode(charts), MinMS: opts.MinMS, Charts: make([]htmlChart, len(charts))}
	for i, chart := range charts {
		data.Charts[i] = htmlChart{Label: chart.Label, Tree: htmlTree{chartView(chart, false)}, Inverted: htmlTree{chartView(chart, true)}}
	}
	encoded, err := json.Marshal(data)
	if err != nil {
		return fmt.Errorf("encode flame data: %w", err)
	}
	out := &textWriter{w: w}
	out.printf("<section class=\"wk-flame\" aria-label=\"%s\"><style>%s</style>\n<h2>%s</h2>\n", html.EscapeString(title(opts)), style, html.EscapeString(title(opts)))
	if data.Mode == "diff" {
		out.text("<p>Calling-context difference · current minus baseline. Rectangle widths show union time; colors show Delta ms.</p><div class=\"wk-flame-legend\"><span style=\"--swatch:#dc3c41\">slower · positive delta</span><span style=\"--swatch:#239669\">faster · negative delta</span><span style=\"--swatch:#718096\">unchanged</span></div>\n")
	} else {
		out.text("<p>Duration-weighted calling contexts · widths preserve self time.</p><div class=\"wk-flame-legend\"><span style=\"--swatch:#60a5fa\">app</span><span style=\"--swatch:#a78bfa\">framework</span><span style=\"--swatch:#94a3b8\">native</span><span style=\"--swatch:#fbbf24\">GC</span></div>\n")
	}
	out.text("<div class=\"wk-flame-controls\"><label>Regex search<input data-action=\"search\" type=\"search\" placeholder=\"name or file regex\" autocomplete=\"off\"></label><label>Focus selector<input data-action=\"focus\" type=\"text\" placeholder=\"name or file\" autocomplete=\"off\"></label><button type=\"button\" data-action=\"apply-focus\">Focus</button><button type=\"button\" data-action=\"reset\">Reset zoom</button><label class=\"wk-flame-toggle\"><input data-action=\"invert\" type=\"checkbox\"")
	if opts.Invert {
		out.text(" checked")
	}
	out.text(">Invert · callers</label><label>Min width (px)<input data-action=\"minwidth\" type=\"number\" min=\"0\" step=\"0.5\" value=\"0.5\"></label></div>\n<p class=\"wk-flame-status\" role=\"status\" aria-live=\"polite\">Click any frame to zoom. Use breadcrumbs or Reset to return.</p><div class=\"wk-flame-charts\">\n")
	for i, chart := range data.Charts {
		root := chart.Tree.Root
		if opts.Invert {
			root = chart.Inverted.Root
		}
		out.printf("<article class=\"wk-flame-chart\"><h3>%s</h3><p class=\"wk-flame-summary\">%s ms</p><nav class=\"wk-flame-breadcrumb\" aria-label=\"Zoom path\"></nav>\n", html.EscapeString(chart.Label), milliseconds(root.TotalMS))
		if charts[i].Comparison == nil && root.TotalMS == 0 {
			out.text("<p class=\"wk-flame-empty\">No weighted samples in selected window.</p>")
		}
		rects, depth := viewLayout(root, opts.MinMS)
		out.printf("<div class=\"wk-flame-canvas\" style=\"height:%dpx\">", (depth+1)*25+2)
		for _, rect := range rects {
			out.printf("<button type=\"button\" class=\"wk-flame-node\" style=\"left:%.8f%%;width:%.8f%%;bottom:%dpx;background:%s\" title=\"%s\">%s</button>", rect.x*100, rect.width*100, rect.depth*25, fill(rect.node, data.Mode == "diff"), html.EscapeString(tooltip(rect.node, root, data.Mode == "diff")), html.EscapeString(frameName(rect.node.Frame)))
		}
		out.text("</div></article>\n")
	}
	out.text("</div><p class=\"wk-flame-note\">Each recording stays separate. Folded and sampled exports retain canonical root → leaf stacks. ")
	if opts.MinMS > 0 {
		out.printf("Visual contexts below %s ms are hidden without changing totals. ", milliseconds(opts.MinMS))
	}
	out.text("Hover or keyboard-focus frames for generated and original locations, self/total time, and category.</p><noscript>Interactive zoom, search, focus, and caller inversion require JavaScript. Static chart remains visible.</noscript><div class=\"wk-flame-tooltip\" role=\"tooltip\" hidden></div><script type=\"application/json\" class=\"wk-flame-data\">")
	out.text(string(encoded))
	out.text("</script></section>\n<script>")
	out.text(script)
	out.text("</script>\n")
	return out.err
}
