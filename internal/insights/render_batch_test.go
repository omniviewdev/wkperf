package insights

import (
	"bytes"
	"fmt"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/omniviewdev/wkperf/internal/metrics"
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

func TestOutputBriefBatchKeepsOmissionsLocal(t *testing.T) {
	documents := make([]MarkdownDocument, 3)
	for i := range documents {
		r := outputContractReport()
		r.Source, r.Name = fmt.Sprintf("capture-%d.json", i), fmt.Sprintf("capture-%d", i)
		r.Hotspots = nil
		for j := range 3 {
			r.Hotspots = append(r.Hotspots, metrics.FuncStat{
				Name: fmt.Sprintf("measured-leaf-%d-%s", j, strings.Repeat("界", 600)),
				URL:  "app.js", Line: 1, Column: j + 1, SelfMs: 10, TotalMs: 20, SelfSamples: 5,
			})
		}
		r.InclusiveHotspots = r.Hotspots
		documents[i].Report = r
	}
	var complete bytes.Buffer
	if err := RenderMarkdownDocuments(&complete, documents, "", MarkdownOptions{Brief: true}); err != nil {
		t.Fatal(err)
	}
	if complete.Len() <= 30000 {
		t.Fatalf("fixture does not exercise the byte cap: %d bytes", complete.Len())
	}
	for _, terminal := range []bool{false, true} {
		for _, color := range []bool{false, true} {
			t.Run(fmt.Sprintf("term=%v/color=%v", terminal, color), func(t *testing.T) {
				render := func(options MarkdownOptions) string {
					t.Helper()
					var b bytes.Buffer
					var err error
					if terminal {
						err = RenderTermDocuments(&b, documents, "", color, options)
					} else {
						err = RenderMarkdownDocuments(&b, documents, "", options)
					}
					if err != nil {
						t.Fatal(err)
					}
					return b.String()
				}
				options := MarkdownOptions{Brief: true, MaxBytes: 30000}
				bounded := render(options)
				if len(bounded) > options.MaxBytes || !utf8.ValidString(bounded) {
					t.Fatalf("invalid bounded output: %d bytes", len(bounded))
				}
				if render(options) != bounded {
					t.Error("bounded rendering is not deterministic")
				}
				full := complete.String()
				if terminal {
					full = terminalText(full, color)
				}
				if render(MarkdownOptions{Brief: true, MaxBytes: len(full)}) != full {
					t.Error("a fitting cap changes the complete report")
				}
				stripColor := strings.NewReplacer("\x1b[1m", "", "\x1b[0m", "")
				out := stripColor.Replace(bounded)
				full = stripColor.Replace(full)
				profiles := strings.Split(out, "wkperf profile: ")[1:]
				fullProfiles := strings.Split(full, "wkperf profile: ")[1:]
				if len(profiles) != len(documents) {
					t.Fatalf("lost recording identities: got %d", len(profiles))
				}
				for i, profile := range profiles {
					for _, title := range []string{"Hot functions", "Verification"} {
						section := outputSection(profile, title, terminal)
						fullSection := outputSection(fullProfiles[i], title, terminal)
						rows := outputEvidenceRows(fullSection)
						if len(rows) == 0 {
							t.Fatalf("fixture has no %s evidence", title)
						}
						cut := false
						for _, row := range rows {
							if !strings.Contains(section, row) {
								cut = true
							}
						}
						if cut && !outputHasLocalOmission(section) {
							t.Errorf("recording %d hides omitted %s evidence without local retrieval guidance", i, title)
						}
						if strings.Contains(section, "| Function |") && !strings.Contains(section, "| measured-leaf-") && !strings.Contains(section, "| `measured-leaf-") {
							t.Errorf("recording %d leaves a header-only hotspot table", i)
						}
					}
				}
			})
		}
	}
}

func outputSection(profile, title string, terminal bool) string {
	lines := strings.Split(profile, "\n")
	start, end := -1, len(lines)
	for i, line := range lines {
		if strings.TrimLeft(line, "# ") == title {
			start = i + 1
			continue
		}
		if start >= 0 && ((!terminal && strings.HasPrefix(line, "## ")) ||
			(terminal && (line == "Finding details" || line == "Verification"))) {
			end = i
			break
		}
		if start >= 0 && strings.HasPrefix(line, "[truncated: ") && strings.Contains(line, "bytes shown;") {
			end = i
			break
		}
	}
	if start < 0 {
		return ""
	}
	return strings.Join(lines[start:end], "\n")
}

func outputEvidenceRows(section string) []string {
	var rows []string
	for _, line := range strings.Split(section, "\n") {
		if strings.HasPrefix(line, "- ") || strings.HasPrefix(line, "| `") || strings.HasPrefix(line, "| measured-leaf-") {
			rows = append(rows, line)
		}
	}
	return rows
}

func outputHasLocalOmission(section string) bool {
	lower := strings.ToLower(section)
	return (strings.Contains(lower, "omitted") || strings.Contains(lower, "truncat")) &&
		(strings.Contains(lower, "json") || strings.Contains(lower, "wkperf query"))
}

func TestOutputBudgetRejectsHeaderOnlyTablePrefixes(t *testing.T) {
	r := outputContractReport()
	for _, terminal := range []bool{false, true} {
		for _, color := range []bool{false, true} {
			documents := documentBlocks([]MarkdownDocument{{Report: r}}, true, terminal, color)
			for _, block := range documents[0].details {
				if !strings.Contains(block.text, "Hot functions") {
					continue
				}
				firstRow := strings.Index(block.text, "\n|---")
				if firstRow < 0 {
					t.Fatal("fixture has no hotspot table")
				}
				firstRow += strings.IndexByte(block.text[firstRow+1:], '\n') + 2
				block.detail = false
				if used := takeLines(&block, firstRow); used != 0 || block.shown != 0 {
					t.Errorf("term=%v/color=%v counts table scaffolding as evidence: %q", terminal, color, block.text[:block.shown])
				}
				secondRow := strings.Index(block.text[firstRow:], "\n|---")
				if secondRow < 0 {
					t.Fatal("fixture has no inclusive ranking table")
				}
				secondRow += firstRow
				secondRow += strings.IndexByte(block.text[secondRow+1:], '\n') + 2
				takeLines(&block, secondRow)
				prefix := block.text[:block.shown]
				if strings.Count(prefix, "| Function |") != 1 || strings.Contains(prefix, "Inclusive ranking") {
					t.Errorf("term=%v/color=%v leaves an empty second ranking: %q", terminal, color, prefix)
				}
			}
		}
	}
}
