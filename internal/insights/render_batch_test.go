package insights

import (
	"bytes"
	"fmt"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/omniviewdev/wkperf/internal/testgen"
)

func TestOutputBatchReservesSourcesAndCountsFinalMarker(t *testing.T) {
	one := outputContractReport()
	two := outputContractReport()
	one.Source, two.Source = "one.json", "two.json"
	documents := []MarkdownDocument{{Report: one, Suffix: "\n## Scorecard\n\n" + strings.Repeat("- Scorecard detail.\n", 100)}, {Report: two}}
	for _, terminal := range []bool{false, true} {
		for _, color := range []bool{false, true} {
			for _, n := range []int{64, 200, 1000, 4000, 20000} {
				t.Run(fmt.Sprintf("term=%v/color=%v/cap=%d", terminal, color, n), func(t *testing.T) {
					var b bytes.Buffer
					var err error
					if terminal {
						err = RenderTermDocuments(&b, documents, "", color, MarkdownOptions{MaxBytes: n})
					} else {
						err = RenderMarkdownDocuments(&b, documents, "", MarkdownOptions{MaxBytes: n})
					}
					if err != nil {
						t.Fatal(err)
					}
					out := b.String()
					if b.Len() > n || !strings.HasSuffix(out, MarkdownTruncationMarker+"\n") {
						t.Fatalf("bytes=%d cap=%d; missing last marker", b.Len(), n)
					}
					if n >= 1000 && (!strings.Contains(out, one.Source) || !strings.Contains(out, two.Source)) {
						t.Error("global budget dropped recording identity")
					}
					if n >= 4000 {
						for _, f := range one.Findings {
							if strings.Count(out, f.ID+":")+strings.Count(out, f.ID+"`:") < 2 {
								t.Errorf("finding %s missing from one recording", f.ID)
							}
						}
					}
				})
			}
		}
	}
}

func TestOutputBriefTerminalUsesSameEvidenceAndCaps(t *testing.T) {
	r := outputContractReport()
	var markdown, terminal bytes.Buffer
	if err := RenderMarkdownWithOptions(&markdown, r, MarkdownOptions{Brief: true}); err != nil {
		t.Fatal(err)
	}
	if err := RenderTermWithOptions(&terminal, r, false, MarkdownOptions{Brief: true}); err != nil {
		t.Fatal(err)
	}
	if terminal.String() != terminalText(markdown.String(), false) {
		t.Error("brief term and Markdown select different evidence")
	}
	terminal.Reset()
	if err := RenderTermWithOptions(&terminal, r, true, MarkdownOptions{MaxBytes: 5000}); err != nil {
		t.Fatal(err)
	}
	if terminal.Len() > 5000 || !strings.HasSuffix(terminal.String(), MarkdownTruncationMarker+"\n") {
		t.Error("terminal cap must count ANSI bytes and final marker")
	}
}

func TestOutputDeepMarkdownSizeDoesNotScaleWithStackCount(t *testing.T) {
	var sizes []int
	for _, count := range []int{80, 160} {
		r := Analyze(testgen.DeepStacks(400, count, 200), "deep.json", Options{})
		var b bytes.Buffer
		if err := RenderMarkdown(&b, r); err != nil {
			t.Fatal(err)
		}
		if b.Len() > 150000 {
			t.Errorf("deep=%d Markdown bytes=%d exceeds 150 KB", count, b.Len())
		}
		sizes = append(sizes, b.Len())
		t.Logf("deep=%d Markdown bytes=%d", count, b.Len())
	}
	if sizes[1] > sizes[0]*105/100 {
		t.Errorf("doubling deep stack count grows Markdown by >5%%: %v", sizes)
	}
}

func TestOutputPreambleErrorsStayInsideFinalCap(t *testing.T) {
	var b bytes.Buffer
	preamble := "- Error: failed.json: invalid recording\n"
	if err := RenderMarkdownDocuments(&b, []MarkdownDocument{{Report: outputContractReport()}}, preamble, MarkdownOptions{MaxBytes: 1000}); err != nil {
		t.Fatal(err)
	}
	if b.Len() > 1000 || !strings.Contains(b.String(), preamble) || !strings.HasSuffix(b.String(), MarkdownTruncationMarker+"\n") {
		t.Error("error context must remain inside report cap")
	}
}

func TestOutputTinyBudgetsKeepLongSourceIdentities(t *testing.T) {
	one, two := outputContractReport(), outputContractReport()
	prefix := strings.Repeat("/长路径", 30)
	one.Source, two.Source = prefix+"/one.json", prefix+"/two.json"
	one.Name, two.Name = strings.Repeat("capture", 100), strings.Repeat("scene", 150)
	documents := []MarkdownDocument{{Report: one}, {Report: two}}
	for _, terminal := range []bool{false, true} {
		for _, budget := range []int{64, 200, 1000} {
			var out bytes.Buffer
			options := MarkdownOptions{MaxBytes: budget}
			var err error
			if terminal {
				err = RenderTermDocuments(&out, documents, "", false, options)
			} else {
				err = RenderMarkdownDocuments(&out, documents, "", options)
			}
			if err != nil {
				t.Fatal(err)
			}
			if out.Len() > budget || out.Len() < budget*9/10 || !utf8.Valid(out.Bytes()) {
				t.Errorf("terminal=%v budget=%d: unusable or invalid bounded output, %d bytes", terminal, budget, out.Len())
			}
			if budget >= 200 && (!strings.Contains(out.String(), "one.json") || !strings.Contains(out.String(), "two.json")) {
				t.Errorf("terminal=%v budget=%d: lost input identities", terminal, budget)
			}
		}
	}
}
