package query

import (
	"bytes"
	"strings"
	"testing"

	"github.com/omniviewdev/wkperf/internal/flame"
	"github.com/omniviewdev/wkperf/internal/model"
	"github.com/omniviewdev/wkperf/internal/rootcause"
)

func TestHotPathUsesSharedSupportAndTieSelection(t *testing.T) {
	rec := &model.Recording{StartTime: 0, EndTime: 1, Samples: []model.TargetSamples{{}}}
	root := model.StackFrame{Name: "root", URL: "src/root.ts", Line: 1}
	for i := range 10 {
		name := "left"
		if i%2 != 0 {
			name = "right"
		}
		rec.Samples[0].StackTraces = append(rec.Samples[0].StackTraces, model.StackTrace{Timestamp: float64(i) / 100, StackFrames: []model.StackFrame{{Name: name, URL: "src/branch.ts", Line: 2}, root}})
		rec.Samples[0].Durations = append(rec.Samples[0].Durations, .01)
	}
	tree := flame.Build(rec)
	report := rootcause.Build(rec, tree, rootcause.Options{Source: "sample.json"})
	result := run(t, engineFor(rec), Request{Operation: "hot-path", Limit: 1})
	if result.HotPathStopReason != "near-tie" || len(result.Rows) != len(report.HotPath.Frames) || len(result.Rows) != 1 || result.Rows[0].ID != report.HotPath.Frames[0].ID {
		t.Fatalf("report/query hot paths differ: report=%+v result=%+v", report.HotPath, result)
	}
	if len(result.HotPathBranches) != 1 || result.HotPathBranchesTotal != 2 {
		t.Fatalf("branch cap/total lost: %+v", result)
	}
	for _, format := range []string{"json", "md", "tsv"} {
		var out bytes.Buffer
		if err := Render(&out, result, format); err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(out.String(), "near-tie") || !strings.Contains(out.String(), "left") {
			t.Errorf("%s hides branch stop: %s", format, out.String())
		}
	}
	raw := run(t, engineFor(rec), Request{Operation: "hot-path", MinSamplesSet: true, TieRatioSet: true})
	if len(raw.Rows) != 2 || raw.Rows[1].Name != "left" || raw.HotPathStopReason != "leaf" {
		t.Fatalf("explicit zero escape must preserve deterministic greedy path: %+v", raw)
	}
	command := strings.Join(raw.Command, " ")
	if !strings.Contains(command, "--min-samples 0") || !strings.Contains(command, "--tie-ratio 0") {
		t.Fatalf("raw reproduction loses explicit zero: %s", command)
	}
}
