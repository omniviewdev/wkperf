package flame

import (
	"encoding/xml"
	"io"
	"strings"
)

func xmlText(s string) string {
	var out strings.Builder
	_ = xml.EscapeText(&out, []byte(s))
	return out.String()
}

// RenderSVG uses the same width-preserving layout as the interactive page.
func RenderSVG(w io.Writer, charts []Chart, opts Options) error {
	if err := validate(charts, opts); err != nil {
		return err
	}
	type drawing struct {
		label         string
		root          *viewNode
		rects         []viewRect
		depth, height int
	}
	drawings := make([]drawing, len(charts))
	height := 100
	for i, chart := range charts {
		root := chartView(chart, opts.Invert)
		rects, depth := viewLayout(root, opts.MinMS)
		h := (depth+1)*25 + 80
		drawings[i] = drawing{chart.Label, root, rects, depth, h}
		height += h
	}
	diff := mode(charts) == "diff"
	out := &textWriter{w: w}
	out.printf("<svg xmlns=\"http://www.w3.org/2000/svg\" width=\"1200\" height=\"%d\" viewBox=\"0 0 1200 %d\" role=\"img\" aria-labelledby=\"flame-title\">\n<title id=\"flame-title\">%s</title>\n<rect width=\"1200\" height=\"%d\" fill=\"#111827\"/>\n<style>text{font-family:system-ui,-apple-system,sans-serif;fill:#e5e7eb}.frame-label{font:11px ui-monospace,SFMono-Regular,monospace;fill:#0b1220}.small{font-size:12px;fill:#b8c5d8}.heading{font-size:22px;font-weight:650}.chart-heading{font-size:15px;font-weight:600}</style>\n<text class=\"heading\" x=\"20\" y=\"33\">%s</text>\n", height, height, xmlText(title(opts)), height, xmlText(title(opts)))
	if diff {
		out.text("<text class=\"small\" x=\"20\" y=\"57\">Delta ms = current − baseline · union-width layout</text><rect x=\"530\" y=\"46\" width=\"11\" height=\"11\" fill=\"#dc3c41\"/><text class=\"small\" x=\"547\" y=\"57\">slower (+)</text><rect x=\"650\" y=\"46\" width=\"11\" height=\"11\" fill=\"#239669\"/><text class=\"small\" x=\"667\" y=\"57\">faster (−)</text><rect x=\"770\" y=\"46\" width=\"11\" height=\"11\" fill=\"#718096\"/><text class=\"small\" x=\"787\" y=\"57\">unchanged</text>\n")
	} else {
		out.text("<text class=\"small\" x=\"20\" y=\"57\">Widths show weighted milliseconds · app / framework / native / GC</text>\n")
	}
	offset := 75
	direction := "root → leaf"
	if opts.Invert {
		direction = "self → callers"
	}
	for chartIndex, chart := range drawings {
		out.printf("<g><text class=\"chart-heading\" x=\"20\" y=\"%d\">%s</text><text class=\"small\" x=\"20\" y=\"%d\">%s ms · %s</text>\n", offset+15, xmlText(chart.label), offset+33, milliseconds(chart.root.TotalMS), direction)
		for nodeIndex, rect := range chart.rects {
			width := rect.width * 1160
			if width < .5 {
				continue
			}
			x, y := 20+rect.x*1160, offset+44+(chart.depth-rect.depth)*25
			out.printf("<g><title>%s</title><rect x=\"%.4f\" y=\"%d\" width=\"%.4f\" height=\"24\" rx=\"2\" fill=\"%s\" stroke=\"#111827\" stroke-width=\"1\"/>\n", xmlText(tooltip(rect.node, chart.root, diff)), x, y, width, fill(rect.node, diff))
			if width >= 28 {
				out.printf("<clipPath id=\"frame-%d-%d\"><rect x=\"%.4f\" y=\"%d\" width=\"%.4f\" height=\"24\"/></clipPath><text class=\"frame-label\" x=\"%.4f\" y=\"%d\" clip-path=\"url(#frame-%d-%d)\">%s</text>\n", chartIndex, nodeIndex, x+4, y, width-8, x+5, y+16, chartIndex, nodeIndex, xmlText(frameName(rect.node.Frame)))
			}
			out.text("</g>\n")
		}
		out.text("</g>\n")
		offset += chart.height
	}
	out.printf("<text class=\"small\" x=\"20\" y=\"%d\">Each recording stays separate. Hover frames for generated/original locations and self/total time.</text>\n</svg>\n", height-12)
	return out.err
}
