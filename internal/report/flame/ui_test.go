package flame

import (
	"bytes"
	"encoding/json"
	"encoding/xml"
	"fmt"
	"io"
	"math"
	"strconv"
	"strings"
	"testing"

	core "github.com/omniviewdev/wkperf/internal/flame"
	"github.com/omniviewdev/wkperf/internal/model"
	"github.com/omniviewdev/wkperf/internal/testgen"
)

func focusFixtureTree() *core.Tree {
	main := model.StackFrame{Name: "main", URL: "file:///app/app.js", Line: 1, Column: 5}
	a := model.StackFrame{Name: "A", URL: "file:///app/app.js", Line: 10, Column: 5}
	b := model.StackFrame{Name: "B", URL: "file:///app/app.js", Line: 20, Column: 5}
	c := model.StackFrame{Name: "C", URL: "file:///app/lib.js", Line: 5, Column: 3}
	animation := model.StackFrame{Name: "getAnimations"}
	return core.Build(&model.Recording{EndTime: 10, Samples: []model.TargetSamples{{
		StackTraces: []model.StackTrace{
			{Timestamp: 1, StackFrames: []model.StackFrame{animation, b, main}},
			{Timestamp: 2, StackFrames: []model.StackFrame{b, a, main}},
			{Timestamp: 3, StackFrames: []model.StackFrame{c, a, main}},
			{Timestamp: 4, StackFrames: []model.StackFrame{a, main}},
			{Timestamp: 5, StackFrames: []model.StackFrame{a, a, main}},
			{Timestamp: 6, StackFrames: []model.StackFrame{c, b, a, main}},
		},
		Durations: []float64{.030, .020, .020, .005, .004, .001},
	}}})
}

func TestDiffPaletteContrast(t *testing.T) {
	luminance := func(hex string) float64 {
		if len(hex) != 7 || hex[0] != '#' {
			t.Fatalf("paint must use opaque hexadecimal color, got %q", hex)
		}
		var channels [3]float64
		for i := range channels {
			n, err := strconv.ParseUint(hex[1+i*2:3+i*2], 16, 8)
			if err != nil {
				t.Fatal(err)
			}
			v := float64(n) / 255
			if v <= .04045 {
				channels[i] = v / 12.92
			} else {
				channels[i] = math.Pow((v+.055)/1.055, 2.4)
			}
		}
		return .2126*channels[0] + .7152*channels[1] + .0722*channels[2]
	}
	for i, paint := range diffPalette {
		if paint.Foreground != "#000000" && paint.Foreground != "#ffffff" {
			t.Errorf("palette[%d] uses non-extreme text %q", i, paint.Foreground)
		}
		bg, fg := luminance(paint.Background), luminance(paint.Foreground)
		ratio := (math.Max(bg, fg) + .05) / (math.Min(bg, fg) + .05)
		if ratio < 4.5 {
			t.Errorf("palette[%d] contrast %.6f: %s on %s", i, ratio, paint.Foreground, paint.Background)
		}
		share := float64(i-diffPaletteSteps) / diffPaletteSteps
		got := nodePaint(&viewNode{DeltaMS: share * 80}, true, 80)
		if got != paint {
			t.Errorf("rendered ramp differs at %.2f: %+v, want %+v", share, got, paint)
		}
	}
	if got := nodePaint(&viewNode{Category: core.Category("unknown-bundled")}, false, 0); got.Background != "#718096" {
		t.Fatalf("unknown category must not claim app ownership: %+v", got)
	}
	if got := nodePaint(&viewNode{Category: core.Category("injected")}, false, 0); got.Background != "#718096" {
		t.Fatalf("injected category must stay neutral: %+v", got)
	}
}

func TestHTMLFocusCatalogOutermostContexts(t *testing.T) {
	chart := makeHTMLChart(Chart{Label: "tiny", Tree: focusFixtureTree()})
	index := func(root *viewNode) map[string]*viewNode {
		out := make(map[string]*viewNode)
		var visit func(*viewNode)
		visit = func(n *viewNode) {
			out[n.ID] = n
			for _, child := range n.Children {
				visit(child)
			}
		}
		visit(root)
		return out
	}
	forward, inverted := index(chart.Tree.Root), index(chart.Inverted.Root)
	seen := 0
	for _, function := range chart.Functions {
		var want float64
		switch function.Frame.Name {
		case "A":
			want = 50
		case "B":
			want = 51
		default:
			continue
		}
		seen++
		for direction, contexts := range map[string][]string{"forward": function.Contexts, "inverted": function.InvertedContexts} {
			lookup := forward
			if direction == "inverted" {
				lookup = inverted
			}
			total := 0.0
			for _, id := range contexts {
				node := lookup[id]
				if node == nil || node.Frame.ID != function.ID {
					t.Fatalf("%s %s has wrong focus context %q", direction, function.Frame.Name, id)
				}
				total += node.TotalMS
			}
			if total != want {
				t.Errorf("%s focus %s = %g, want %g", direction, function.Frame.Name, total, want)
			}
		}
		if function.TotalMS != want {
			t.Errorf("candidate total %s = %g, want %g", function.Frame.Name, function.TotalMS, want)
		}
	}
	if seen != 2 {
		t.Fatalf("focus catalog lacks A or B: %+v", chart.Functions)
	}
}

func TestHTMLSharedPaletteAndDeepRoot(t *testing.T) {
	tree := core.Build(testgen.DeepStacks(373, 2, 0))
	var out bytes.Buffer
	if err := RenderHTML(&out, []Chart{{Label: "synthetic deep", Tree: tree}}, Options{}); err != nil {
		t.Fatal(err)
	}
	body := out.String()
	start := strings.Index(body, `<script type="application/json" class="wk-flame-data">`)
	if start < 0 {
		t.Fatal("missing embedded data")
	}
	dataStart := start + len(`<script type="application/json" class="wk-flame-data">`)
	dataEnd := strings.Index(body[dataStart:], "</script>")
	var data htmlData
	if err := json.Unmarshal([]byte(body[dataStart:dataStart+dataEnd]), &data); err != nil {
		t.Fatal(err)
	}
	if len(data.Palette) != len(diffPalette) {
		t.Fatalf("browser palette has %d colors, want %d", len(data.Palette), len(diffPalette))
	}
	for i := range diffPalette {
		if data.Palette[i] != diffPalette[i] {
			t.Fatalf("browser palette[%d] does not use tested Go palette", i)
		}
	}
	static := body[:start]
	root := strings.Index(static, `class="wk-flame-node"`)
	if root < 0 {
		t.Fatal("missing static root")
	}
	rootTag := static[root : root+strings.IndexByte(static[root:], '>')]
	if !strings.Contains(rootTag, "top:0px") || strings.Contains(rootTag, "bottom:") {
		t.Fatalf("deep root must start at canvas top: %s", rootTag)
	}
	if !strings.Contains(rootTag, `aria-label="(root), total 2.000 ms, self 0 ms"`) {
		t.Fatalf("root lacks short accessible label: %s", rootTag)
	}
}

type normalizedUnit string

func (u normalizedUnit) DeltaUnit() string { return string(u) }

func TestNormalizedDeltaTooltip(t *testing.T) {
	for _, unit := range []string{"ms", "ms/s", "percentage-points"} {
		if got := deltaUnit(normalizedUnit(unit)); got != unit {
			t.Errorf("unit = %q, want %q", got, unit)
		}
		node := &viewNode{Frame: core.Frame{Name: "work", URL: "app.js", Line: 1, Column: 2}, SelfMS: 5, TotalMS: 20, BaselineMS: 10, CurrentMS: 20, DeltaMS: 2}
		text := tooltipWithUnit(node, &viewNode{TotalMS: 80}, true, unit)
		for _, need := range []string{"Generated: work · app.js:1:2", "Self: 5.000 ms", "Total: 20.000 ms", "Baseline: 10.000 ms", "Current: 20.000 ms", fmt.Sprintf("Delta: +2.000 %s", unit)} {
			if !strings.Contains(text, need) {
				t.Errorf("tooltip lacks %q: %s", need, text)
			}
		}
	}
	if got := deltaUnit((*core.Comparison)(nil)); got != "ms" {
		t.Errorf("absent optional backend unit = %q, want ms", got)
	}
}

func TestSVGDiffLabelsUseSharedPalette(t *testing.T) {
	before := focusFixtureTree()
	after := fixtureTree()
	var out bytes.Buffer
	if err := RenderSVG(&out, []Chart{{Label: "synthetic diff", Before: before, After: after, Comparison: core.Diff(before, after)}}, Options{}); err != nil {
		t.Fatal(err)
	}
	palette := make(map[framePaint]bool)
	for _, paint := range diffPalette {
		palette[paint] = true
	}
	decoder := xml.NewDecoder(bytes.NewReader(out.Bytes()))
	background := ""
	labels := 0
	for {
		token, err := decoder.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		element, ok := token.(xml.StartElement)
		if !ok {
			continue
		}
		attrs := make(map[string]string)
		for _, attr := range element.Attr {
			attrs[attr.Name.Local] = attr.Value
		}
		if element.Name.Local == "rect" && attrs["stroke"] == "#111827" {
			background = attrs["fill"]
		}
		if element.Name.Local == "text" && attrs["class"] == "frame-label" {
			paint := framePaint{background, strings.TrimPrefix(attrs["style"], "fill:")}
			if !palette[paint] {
				t.Fatalf("SVG label does not consume tested shared palette: %+v", paint)
			}
			labels++
		}
	}
	if labels == 0 {
		t.Fatal("SVG has no readable differential labels")
	}
}
