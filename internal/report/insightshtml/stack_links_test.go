package insightshtml

import (
	"bytes"
	"regexp"
	"testing"

	"github.com/omniviewdev/wkperf/internal/insights"
	"github.com/omniviewdev/wkperf/internal/model"
)

func TestStackLinksStayWithinDistinctCaptureEvidence(t *testing.T) {
	rec := &model.Recording{StartTime: 0, EndTime: 1, Samples: []model.TargetSamples{{
		StackTraces: []model.StackTrace{{Timestamp: .5, StackFrames: []model.StackFrame{{Name: "work", URL: "app.js", Line: 1, Column: 3}}}},
		Durations:   []float64{.005},
	}}}
	reports := []*insights.Report{
		insights.Analyze(rec, "one.json", insights.Options{}),
		insights.Analyze(rec, "two.json", insights.Options{}),
	}
	var out bytes.Buffer
	if err := Render(&out, reports); err != nil {
		t.Fatal(err)
	}
	ids := regexp.MustCompile(`id="(stack-[^"]+)"`).FindAllStringSubmatch(out.String(), -1)
	seen := make(map[string]bool)
	for _, id := range ids {
		if seen[id[1]] {
			t.Errorf("captures share an ambiguous stack target: %s", id[1])
		}
		seen[id[1]] = true
	}
	if len(seen) != 2 {
		t.Errorf("want one distinct stack target per capture, got %d", len(seen))
	}
	for _, link := range regexp.MustCompile(`href="#(stack-[^"]+)"`).FindAllStringSubmatch(out.String(), -1) {
		if !seen[link[1]] {
			t.Errorf("stack link has no target: %s", link[1])
		}
	}
}
