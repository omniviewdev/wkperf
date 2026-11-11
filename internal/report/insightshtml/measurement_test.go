package insightshtml

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"github.com/omniviewdev/wkperf/internal/insights"
)

func TestMeasurementHTMLSeparatesInputStallsAndGCOverlay(t *testing.T) {
	var report insights.Report
	if err := json.Unmarshal([]byte(`{"source":"capture.json","durationSec":2,"gcMs":80,"gcOverlapWithWorkMs":25,"inputStalls":[{"t":0,"ms":1200,"workMs":0.35,"topEvent":"wheel <unsafe>","sampleStatus":"timed samples unavailable"}],"inputStallsTotal":1,"slowFrames":[{"t":1.2,"ms":300,"workMs":200,"scriptMs":200,"gcMs":30,"gcOverlapWithWorkMs":20}],"rootCause":{"inputStalls":[{"kind":"input-stall","label":"wheel <unsafe>","durationMs":1200,"activeMs":0.35}],"inputStallsTotal":1}}`), &report); err != nil {
		t.Fatal(err)
	}
	report.LongTasks = []insights.LongTask{{EventType: "timer-fired", Children: []string{"microtask-dispatched"}, ChildrenTotal: 2}}
	var out bytes.Buffer
	if err := Render(&out, []*insights.Report{&report}); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"Input stalls", "Observed interval", "Main-thread work", "1200.0", "0.3", "wheel &lt;unsafe&gt;", "GC collections", "collector interval", "not a measured pause", "80.0", "25.0", "GC overlap with work", "Input-stall windows", "timed samples unavailable"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("missing honest measurement label %q", want)
		}
	}
	if !strings.Contains(out.String(), "Nested events: 1/2 shown") {
		t.Fatal("nested event truncation lacks shown/total counts")
	}
	if strings.Contains(out.String(), "wheel <unsafe>") || strings.Contains(out.String(), "GC pauses") {
		t.Fatal("unsafe event or invented GC pause rendered")
	}
}
