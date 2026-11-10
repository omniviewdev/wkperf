package flame

import (
	"bytes"
	"encoding/json"
	"encoding/xml"
	"io"
	"math"
	"strings"
	"testing"

	core "github.com/omniviewdev/wkperf/internal/flame"
	"github.com/omniviewdev/wkperf/internal/model"
)

func fixtureTree() *core.Tree {
	caller := model.StackFrame{Name: "start", URL: "app.js", Line: 1, Column: 2}
	leaf := model.StackFrame{Name: "work", URL: "bundle.js", Line: 3, Column: 4,
		Original: &model.SourceLocation{Name: "originalWork", URL: "src/work.ts", Line: 7, Column: 8}}
	native := model.StackFrame{Name: "native"}
	return core.Build(&model.Recording{EndTime: .012, Samples: []model.TargetSamples{{
		StackTraces: []model.StackTrace{
			{StackFrames: []model.StackFrame{caller}},
			{Timestamp: .005, StackFrames: []model.StackFrame{leaf, caller}},
			{Timestamp: .009, StackFrames: []model.StackFrame{native, caller}},
		},
		Durations: []float64{.005, .004, .003},
	}}})
}

func TestLayoutConservesWidthsAndCulls(t *testing.T) {
	tree := fixtureTree()
	rects := Layout(tree, Options{})
	if len(rects) != 4 || rects[0].Width != 1 || rects[0].Depth != 0 {
		t.Fatalf("unexpected layout: %+v", rects)
	}
	for _, rect := range rects {
		if rect.X < 0 || rect.Width < 0 || rect.X+rect.Width > 1+1e-12 {
			t.Errorf("frame outside layout: %+v", rect)
		}
	}
	if got := Layout(tree, Options{MinMS: 4}); len(got) != 3 {
		t.Fatalf("min-ms must cull native while preserving total: %+v", got)
	}
	if math.Abs(rects[2].Width-3.0/12) > 1e-12 || math.Abs(rects[3].X-3.0/12) > 1e-12 {
		t.Fatalf("child geometry lost self-time gap: %+v", rects)
	}
}

func TestJSONAndSpeedscopeExports(t *testing.T) {
	charts := []Chart{{Label: "one recording", Tree: fixtureTree()}}
	var out bytes.Buffer
	if err := Render(&out, "json", charts, Options{}); err != nil {
		t.Fatal(err)
	}
	var doc Document
	if err := json.Unmarshal(out.Bytes(), &doc); err != nil || doc.Schema != Schema || doc.Mode != "profile" || len(doc.Charts) != 1 || doc.Charts[0].Tree.Root.TotalMs != 12 {
		t.Fatalf("invalid tree document: %v %s", err, out.String())
	}
	out.Reset()
	if err := Render(&out, "speedscope", charts, Options{}); err != nil {
		t.Fatal(err)
	}
	var speed Speedscope
	if err := json.Unmarshal(out.Bytes(), &speed); err != nil {
		t.Fatal(err)
	}
	if speed.Schema != "https://www.speedscope.app/file-format-schema.json" || len(speed.Shared.Frames) != 3 || len(speed.Profiles) != 1 {
		t.Fatalf("invalid speedscope structure: %+v", speed)
	}
	p := speed.Profiles[0]
	if p.Type != "sampled" || p.Unit != "milliseconds" || p.StartValue != 0 || p.EndValue != 12 || len(p.Samples) != 3 || len(p.Weights) != 3 {
		t.Fatalf("invalid sampled profile: %+v", p)
	}
	var sum float64
	for i, stack := range p.Samples {
		sum += p.Weights[i]
		for _, frame := range stack {
			if frame < 0 || frame >= len(speed.Shared.Frames) {
				t.Fatalf("frame index outside shared table: %d", frame)
			}
		}
	}
	if sum != p.EndValue {
		t.Fatalf("weights %v differ from extent %v", sum, p.EndValue)
	}
	out.Reset()
	if err := Render(&out, "folded", charts, Options{}); err != nil {
		t.Fatal(err)
	}
	for _, line := range []string{"start@app.js:1:2 5", "start@app.js:1:2;originalWork@src/work.ts:7:8", "start@app.js:1:2;native@native:"} {
		if !strings.Contains(out.String(), line) {
			t.Errorf("folded output lacks %q: %s", line, out.String())
		}
	}
}

func TestHTMLAndSVGEscapingAndSourceTooltip(t *testing.T) {
	tree := fixtureTree()
	payload := `</script><img src=x onerror="alert(1)">&`
	tree.Root.Children[0].Children[0].Frame.Name = payload
	charts := []Chart{{Label: payload, Tree: tree}}
	var html bytes.Buffer
	if err := RenderHTML(&html, charts, Options{Title: payload}); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(html.String(), payload) || strings.Contains(html.String(), "<img src=x") {
		t.Fatal("hostile label escaped script or markup context")
	}
	for _, need := range []string{"\\u003c/script\\u003e", "src/work.ts", "bundle.js", "data-action=\"search\"", "data-action=\"invert\"", "data-action=\"minwidth\"", "addEventListener(\"click\"", "new RegExp", "Original:", "Generated:", "Self:", "Total:"} {
		if !strings.Contains(html.String(), need) {
			t.Errorf("HTML lacks %q", need)
		}
	}
	for _, external := range []string{"<script src=", "<link ", "<iframe", "fetch(", "https://"} {
		if strings.Contains(html.String(), external) {
			t.Errorf("self-contained HTML has external dependency %q", external)
		}
	}
	var svg bytes.Buffer
	if err := RenderSVG(&svg, charts, Options{}); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(svg.String(), payload) || !strings.Contains(svg.String(), "src/work.ts:7:8") || !strings.Contains(svg.String(), "bundle.js:3:4") {
		t.Fatalf("unsafe or incomplete SVG tooltip: %s", svg.String())
	}
	decoder := xml.NewDecoder(bytes.NewReader(svg.Bytes()))
	for {
		if _, err := decoder.Token(); err != nil {
			if err != io.EOF {
				t.Fatal(err)
			}
			break
		}
	}
}

func TestDeterministicFormatsAfterShuffledSamples(t *testing.T) {
	frame := func(name string) model.StackFrame {
		return model.StackFrame{Name: name, URL: "app.js", Line: 1, Column: 2}
	}
	a := model.StackTrace{Timestamp: 0, StackFrames: []model.StackFrame{frame("leaf"), frame("caller")}}
	b := model.StackTrace{Timestamp: .002, StackFrames: []model.StackFrame{frame("other"), frame("caller")}}
	rec := &model.Recording{StartTime: 0, EndTime: 1, Samples: []model.TargetSamples{{StackTraces: []model.StackTrace{a, b}, Durations: []float64{.002, .003}}}}
	first := core.Build(rec)
	rec.Samples[0].StackTraces = []model.StackTrace{b, a}
	rec.Samples[0].Durations = []float64{.003, .002}
	second := core.Build(rec)
	for _, format := range []string{"html", "svg", "json", "speedscope", "folded"} {
		t.Run(format, func(t *testing.T) {
			var one, two bytes.Buffer
			if err := Render(&one, format, []Chart{{Label: "same", Tree: first}}, Options{}); err != nil {
				t.Fatal(err)
			}
			if err := Render(&two, format, []Chart{{Label: "same", Tree: second}}, Options{}); err != nil {
				t.Fatal(err)
			}
			if one.String() != two.String() {
				t.Fatal("shuffled samples changed output")
			}
		})
	}
}

func TestMultipleInputsStaySeparate(t *testing.T) {
	charts := []Chart{{Label: "baseline", Tree: fixtureTree()}, {Label: "candidate", Tree: fixtureTree()}}
	for _, format := range []string{"html", "svg", "json", "speedscope", "folded"} {
		var out bytes.Buffer
		if err := Render(&out, format, charts, Options{}); err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(out.String(), "baseline") || !strings.Contains(out.String(), "candidate") {
			t.Fatalf("%s merged unrelated recording labels", format)
		}
	}
}

func TestDiffLayoutAndSignedExports(t *testing.T) {
	makeTree := func(name string) *core.Tree {
		return core.Build(&model.Recording{EndTime: 1, Samples: []model.TargetSamples{{StackTraces: []model.StackTrace{{StackFrames: []model.StackFrame{{Name: name, URL: "app.js"}}}}, Durations: []float64{.012}}}})
	}
	before, after := makeTree("removed"), makeTree("added")
	comparison := core.Diff(before, after)
	charts := []Chart{{Label: "candidate − baseline", Before: before, After: after, Comparison: comparison}}
	for _, format := range []string{"html", "svg", "json", "speedscope", "folded"} {
		var out bytes.Buffer
		if err := Render(&out, format, charts, Options{}); err != nil {
			t.Fatal(err)
		}
		if format == "html" || format == "svg" {
			for _, legend := range []string{"slower", "faster", "Delta"} {
				if !strings.Contains(out.String(), legend) {
					t.Errorf("%s lacks differential legend %q", format, legend)
				}
			}
		}
	}
	root := diffView(comparison.Root)
	if root.LayoutMS != 24 || len(root.Children) != 2 {
		t.Fatalf("union geometry overlaps or drops disjoint frames: %+v", root)
	}
}

func TestRendererPropagatesWriterErrors(t *testing.T) {
	for _, format := range []string{"html", "svg", "json", "speedscope", "folded"} {
		if err := Render(failedWriter{}, format, []Chart{{Label: "x", Tree: fixtureTree()}}, Options{}); err == nil {
			t.Errorf("%s swallowed writer error", format)
		}
	}
}

type failedWriter struct{}

func (failedWriter) Write([]byte) (int, error) { return 0, io.ErrClosedPipe }
