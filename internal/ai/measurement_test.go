package ai

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/omniviewdev/wkperf/internal/audit"
	"github.com/omniviewdev/wkperf/internal/insights"
)

func TestMeasurementPromptSeparatesInputStallsAndGCOverlay(t *testing.T) {
	var report insights.Report
	if err := json.Unmarshal([]byte(`{"source":"capture.json","durationSec":2,"gcMs":80,"gcOverlapWithWorkMs":25,"inputStalls":[{"t":0,"ms":1200,"workMs":0.35,"topEvent":"wheel","sampleStatus":"timed samples unavailable"}],"inputStallsTotal":1,"slowFrames":[{"t":1.2,"ms":300,"workMs":200,"gcMs":30,"gcOverlapWithWorkMs":20}],"rootCause":{"inputStalls":[{"kind":"input-stall","label":"wheel","durationMs":1200,"activeMs":0.35}],"inputStallsTotal":1}}`), &report); err != nil {
		t.Fatal(err)
	}
	report.LongTasks = []insights.LongTask{{EventType: "timer-fired", Children: []string{"microtask-dispatched"}, ChildrenTotal: 2}}
	prompt := BuildAnalyzePrompt([]*insights.Report{&report}, Context{})
	for _, want := range []string{"Input stalls", "observed interval=1200", "main-thread work=0.35", "GC collections", "collector interval", "not a measured pause", "overlap with work=25", "input-stall", "timed samples unavailable"} {
		if !strings.Contains(prompt.User, want) {
			t.Errorf("missing honest prompt measurement %q", want)
		}
	}
	if !strings.Contains(prompt.User, "nested events: 1/2 shown") {
		t.Fatal("nested event truncation lacks shown/total counts")
	}
}

func TestPartialScorePromptLabelsCoverageAndReasons(t *testing.T) {
	var report audit.Report
	if err := json.Unmarshal([]byte(`{"overall":{"score":97,"grade":"n/a","partial":true,"coverage":0.33,"results":[{"id":"frame-p95","rating":"n/a","detail":"insufficient data: 2 active frames"}]},"scenes":[{"name":"idle","runs":1,"durationSec":2,"card":{"score":97,"grade":"n/a","partial":true,"coverage":0.33}}]}`), &report); err != nil {
		t.Fatal(err)
	}
	prompt := BuildAuditPrompt(&report, Context{})
	if strings.Count(prompt.User, "partial score") < 2 || !strings.Contains(prompt.User, "33% coverage") || !strings.Contains(prompt.User, "insufficient data: 2 active frames") {
		t.Fatalf("partial prompt score lacks qualification: %s", prompt.User)
	}
}
