package flame

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"html"
	"io"

	core "github.com/omniviewdev/wkperf/internal/flame"
)

//go:embed flame.js
var script string

//go:embed flame.css
var style string

type htmlTree struct {
	Root *viewNode `json:"root"`
}
type htmlChart struct {
	Label     string          `json:"label"`
	Tree      htmlTree        `json:"tree"`
	Inverted  htmlTree        `json:"inverted"`
	Functions []focusFunction `json:"functions"`
	Unit      string          `json:"unit,omitempty"`
}
type htmlData struct {
	Mode    string       `json:"mode"`
	MinMS   float64      `json:"minMs"`
	Charts  []htmlChart  `json:"charts"`
	Palette []framePaint `json:"palette"`
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
	out.printf("<!doctype html>\n<html lang=\"en\"><head><meta charset=\"utf-8\"><meta name=\"viewport\" content=\"width=device-width,initial-scale=1\"><title>%s</title><style>body{margin:0;background:#16191d}main{margin:auto}</style></head><body><main>\n", html.EscapeString(title(opts)))
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
	data := htmlData{Mode: mode(charts), MinMS: opts.MinMS, Charts: make([]htmlChart, len(charts)), Palette: diffPalette[:]}
	for i, chart := range charts {
		data.Charts[i] = makeHTMLChart(chart)
	}
	encoded, err := json.Marshal(data)
	if err != nil {
		return fmt.Errorf("encode flame data: %w", err)
	}
	out := &textWriter{w: w}
	out.printf("<section class=\"wk-flame\" aria-label=\"%s\"><style>%s</style><header class=\"wk-title\"><b>wkperf</b><span>Performance tools</span><h2>%s</h2></header>\n", html.EscapeString(title(opts)), style, html.EscapeString(title(opts)))
	if data.Mode == "diff" {
		out.text("<p>Calling-context difference · current minus baseline. Rectangle widths show union time; colors show Delta in each recording's stated unit.</p><div class=\"wk-flame-legend\"><span style=\"--swatch:#dc3c41\">slower · positive delta</span><span style=\"--swatch:#239669\">faster · negative delta</span><span style=\"--swatch:#718096\">unchanged</span></div>\n")
	} else {
		out.text("<p>Duration-weighted calling contexts · widths preserve self time.</p><div class=\"wk-flame-legend\"><span style=\"--swatch:#60a5fa\">app</span><span style=\"--swatch:#a78bfa\">framework</span><span style=\"--swatch:#94a3b8\">native</span><span style=\"--swatch:#fbbf24\">GC</span><span style=\"--swatch:#718096\">unknown-bundled / injected / other</span></div>\n")
	}
	out.text("<div class=\"wk-flame-controls\"><label>Regex search<input data-action=\"search\" type=\"search\" placeholder=\"name or file regex\" autocomplete=\"off\"></label><label>Focus selector<input data-action=\"focus\" type=\"text\" placeholder=\"exact name, url:line:column, f_/n_ID, /regex/\" autocomplete=\"off\"></label><button type=\"button\" data-action=\"apply-focus\">Focus</button><button type=\"button\" data-action=\"reset\">Reset zoom</button><label class=\"wk-flame-toggle\"><input data-action=\"invert\" type=\"checkbox\"")
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
		if data.Mode == "diff" {
			out.printf("<p class=\"wk-flame-note\">Delta unit: %s. Baseline and current values remain raw ms.</p>\n", html.EscapeString(chart.Unit))
		}
		note := func(label string, tree *core.Tree) {
			if tree.UntimedTotal.Count == 0 {
				return
			}
			policy := "included in rankings, excluded from frame, task, trigger, window and periodic attribution"
			if !tree.UntimedIncluded {
				policy = "excluded from this window"
			}
			out.printf("<p class=\"wk-flame-note\">%sUntimed samples: %d (%g ms): %s.</p>\n", html.EscapeString(label), tree.UntimedTotal.Count, tree.UntimedTotal.Ms, policy)
		}
		if charts[i].Comparison != nil {
			note("Baseline: ", charts[i].Before)
			note("Current: ", charts[i].After)
		} else {
			note("", charts[i].Tree)
		}
		if charts[i].Comparison == nil && root.TotalMS == 0 {
			out.text("<p class=\"wk-flame-empty\">No weighted samples in selected window.</p>")
		}
		rects, depth := viewLayout(root, opts.MinMS)
		maximum := maxAbsDelta(root)
		out.printf("<div class=\"wk-workspace\"><div class=\"wk-plot\"><div class=\"wk-scale\" aria-label=\"Fraction of visible weighted stack width\"><span>0%%</span><span>25%%</span><span>50%%</span><span>75%%</span><span>100%%</span></div><div class=\"wk-flame-canvas\" style=\"height:%dpx\">", (depth+1)*20+2)
		for _, rect := range rects {
			paint := nodePaint(rect.node, data.Mode == "diff", maximum)
			out.printf("<button type=\"button\" class=\"wk-flame-node\" style=\"left:%.8f%%;width:%.8f%%;top:%dpx;background:%s;color:%s\" title=\"%s\" aria-label=\"%s\">%s</button>", rect.x*100, rect.width*100, rect.depth*20, paint.Background, paint.Foreground, html.EscapeString(tooltipWithUnit(rect.node, root, data.Mode == "diff", chart.Unit)), html.EscapeString(shortARIA(rect.node)), html.EscapeString(frameName(rect.node.Frame)))
		}
		out.text("</div></div><aside class=\"wk-inspector\" hidden><h4>Frame inspector</h4><strong class=\"wk-name\"></strong><dl></dl><h4 class=\"wk-branch-label\"></h4><div class=\"wk-branches\"></div></aside></div></article>\n")
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
