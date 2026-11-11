package insights

import (
	"bytes"
	"fmt"
	"strings"
	"testing"

	"github.com/omniviewdev/wkperf/internal/flame"
	"github.com/omniviewdev/wkperf/internal/metrics"
	"github.com/omniviewdev/wkperf/internal/rootcause"
	"github.com/omniviewdev/wkperf/internal/testgen"
)

func outputContractReport() *Report {
	r := Analyze(testgen.DeepMinified(), "/tmp/we ird'$x|`dir/capture.json", Options{})
	r.Findings = nil
	for i := range 9 {
		r.Findings = append(r.Findings, Finding{ID: fmt.Sprintf("finding-%d", i), Title: fmt.Sprintf("Measured problem %d", i), Severity: SevHigh, ImpactMs: float64(90 - i), Suggestion: "Repeat equivalent capture and compare measured cost.", Verify: []Verification{{MetricKey: fmt.Sprintf("findings.finding-%d.impact_ms", i), Direction: "decrease", Unit: "ms", Target: 5}}})
	}
	return r
}

func TestOutputBriefKeepsLeafFindingsAndCompactContext(t *testing.T) {
	r := outputContractReport()
	var b bytes.Buffer
	if err := RenderMarkdownWithOptions(&b, r, MarkdownOptions{Brief: true}); err != nil {
		t.Fatal(err)
	}
	out := b.String()
	for _, f := range r.Findings {
		if !strings.Contains(out, f.ID) {
			t.Errorf("brief dropped finding %s", f.ID)
		}
	}
	leaf := frameLabel(r.RootCause.HotPath.Frames[len(r.RootCause.HotPath.Frames)-1])
	if !strings.Contains(out, leaf) {
		t.Errorf("brief dropped hot-path leaf %q", leaf)
	}
	if count := strings.Count(out, r.Source); count > 3 {
		t.Errorf("source context repeated %d times", count)
	}
	if b.Len() > 12*1024 {
		t.Errorf("brief bytes=%d exceeds 12 KiB", b.Len())
	}
	if strings.Contains(out, "\\|`dir") {
		t.Error("code span corrupts shell path with literal backslash")
	}
}

func TestOutputMarkdownGreedyBudgetKeepsIdentities(t *testing.T) {
	r := outputContractReport()
	var full bytes.Buffer
	if err := RenderMarkdown(&full, r); err != nil {
		t.Fatal(err)
	}
	for _, n := range []int{64, 200, 1000, 4000, 20000, full.Len() - 1} {
		t.Run(fmt.Sprint(n), func(t *testing.T) {
			var b bytes.Buffer
			if err := RenderMarkdownWithOptions(&b, r, MarkdownOptions{MaxBytes: n}); err != nil {
				t.Fatal(err)
			}
			out := b.String()
			if b.Len() > n || !strings.HasSuffix(out, MarkdownTruncationMarker+"\n") {
				t.Fatalf("bytes=%d cap=%d; missing final marker", b.Len(), n)
			}
			if n >= 300 && !strings.Contains(out, "- Source:") {
				t.Error("bounded report dropped source")
			}
			if n >= 4000 {
				for _, f := range r.Findings {
					if !strings.Contains(out, f.ID) {
						t.Errorf("bounded report dropped finding %s", f.ID)
					}
				}
				if b.Len() < n*9/10 {
					t.Errorf("greedy packing wasted bytes: used=%d cap=%d", b.Len(), n)
				}
			}
		})
	}
}

func TestOutputDistinctStackAppendixAndPreview(t *testing.T) {
	frames := make([]flame.Frame, 400)
	metricFrames := make([]metrics.FunctionFrame, len(frames))
	for i := range frames {
		frames[i] = flame.Frame{Name: fmt.Sprintf("frame-%03d", i), URL: "bundle.js", Line: 1, Column: i + 1}
		metricFrames[i] = metrics.FunctionFrame{Name: frames[i].Name, URL: frames[i].URL, Line: 1, Column: i + 1}
	}
	stack := rootcause.Stack{Frames: frames, SelfMs: 10, Samples: 5, Command: "wkperf query capture.json stacks n_exact --format json"}
	r := &Report{Name: "Deep", Source: "capture.json", DurationSec: 1, CommonStacks: []metrics.StackStat{{Frames: metricFrames, WeightMs: 10, Samples: 5}}, Hotspots: []metrics.FuncStat{{Name: "frame-399", CommonStack: metricFrames}}, RootCause: &rootcause.Report{HotPath: rootcause.Path{Frames: frames}, Depth: rootcause.DepthStats{Outliers: []rootcause.DepthOutlier{{Stack: stack}}}, ContextAttribution: []rootcause.Attribution{{Frame: frames[0], Stacks: []rootcause.Stack{stack}}}}, Findings: []Finding{{ID: "deep-stack", Stacks: []rootcause.Stack{stack}}}}
	var b bytes.Buffer
	if err := RenderMarkdown(&b, r); err != nil {
		t.Fatal(err)
	}
	out := b.String()
	if !strings.Contains(out, "## Stacks") || strings.Count(out, "### S1") != 1 {
		t.Error("distinct stack appendix missing")
	}
	if strings.Contains(out, "frame-200") || !strings.Contains(out, "frame-399") || !strings.Contains(out, "378 frames elided") {
		t.Error("stack preview must retain root6 and leaf16 only")
	}
	if b.Len() > 150*1024 {
		t.Errorf("deep report bytes=%d exceeds 150 KiB", b.Len())
	}
}

func TestOutputCommonMarkCodeSpanPreservesCommand(t *testing.T) {
	r := &Report{Name: "Code", Source: "/tmp/with|pipe`tick/capture.json", DurationSec: 1}
	var b bytes.Buffer
	if err := RenderMarkdownWithOptions(&b, r, MarkdownOptions{Brief: true}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(b.String(), "``"+r.Source+"``") {
		t.Fatalf("source code span changes literal content: %s", b.String())
	}
}

func TestOutputCommonMarkCodeSpanDelimiters(t *testing.T) {
	for _, tc := range []struct{ source, span string }{
		{"/tmp/a``b|c", "```/tmp/a``b|c```"},
		{"`capture.json", "`` `capture.json ``"},
		{" capture.json ", "`  capture.json  `"},
		{"  ", "`  `"},
	} {
		t.Run(tc.source, func(t *testing.T) {
			var b bytes.Buffer
			if err := RenderMarkdownWithOptions(&b, &Report{Source: tc.source}, MarkdownOptions{Brief: true}); err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(b.String(), "- Source: "+tc.span+"\n") {
				t.Errorf("code span does not preserve literal source: %q", b.String())
			}
		})
	}
}

func TestOutputTableCodeEscapesPipesOnlyInsideCells(t *testing.T) {
	r := &Report{Source: "capture|file.json", Hotspots: []metrics.FuncStat{{Name: "leaf|`part", URL: "bundle|.js", Line: 1, Column: 2}}}
	var b bytes.Buffer
	if err := RenderMarkdownWithOptions(&b, r, MarkdownOptions{Brief: true}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(b.String(), "- Source: `capture|file.json`\n") || !strings.Contains(b.String(), "| ``leaf\\|`part (bundle\\|.js:1:2)`` |") {
		t.Fatalf("table and ordinary code spans need distinct pipe handling: %s", b.String())
	}
	b.Reset()
	r.Source = `capture\|file.json`
	if err := RenderTermWithOptions(&b, r, false, MarkdownOptions{Brief: true}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(b.String(), "- Source: "+r.Source+"\n") {
		t.Error("terminal conversion changed literal backslash in source")
	}
}

func TestOutputBriefCapsDetailsAndVerificationWindows(t *testing.T) {
	r := outputContractReport()
	for i := range r.Findings {
		r.Findings[i].SuggestedWindows = []SuggestedWindow{{FromSec: float64(i), ToSec: float64(i + 1), Reason: "Measured interval", Flags: []string{"--from", fmt.Sprint(i), "--to", fmt.Sprint(i + 1)}}}
	}
	var b bytes.Buffer
	if err := RenderMarkdownWithOptions(&b, r, MarkdownOptions{Brief: true}); err != nil {
		t.Fatal(err)
	}
	out := b.String()
	if strings.Count(out, "### [HIGH]") != 3 || strings.Count(out, "- Verify window ") != 5 || !strings.Contains(out, "windows: 5 of ") {
		t.Fatalf("brief must keep three details and five windows with totals: %s", out)
	}
}

func TestOutputBriefTimelineOnlyWindowUsesScalarEvidence(t *testing.T) {
	r := &Report{Source: "capture.json", RootCause: &rootcause.Report{Windows: []rootcause.Window{{Label: "timeline window", Stats: []rootcause.Stat{{Key: "triage.window.duration_ms", Value: 50, Unit: "ms"}}}}}}
	var b bytes.Buffer
	if err := RenderMarkdownWithOptions(&b, r, MarkdownOptions{Brief: true}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(b.String(), "(no samples; timeline-only)") || !strings.Contains(b.String(), "[rootcause: `triage.window.duration_ms`]") {
		t.Errorf("timeline-only verification hides missing sampler evidence: %s", b.String())
	}
}
