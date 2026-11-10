package insightshtml

import (
	"bytes"
	"strings"
	"testing"

	"github.com/omniviewdev/wkperf/internal/insights"
	"github.com/omniviewdev/wkperf/internal/model"
)

func TestConfidenceHTMLCarriesBasisAndReplication(t *testing.T) {
	rec := &model.Recording{EndTime: 12, Records: []model.Record{{Type: model.TypeScript, EventType: "function-call", StartTime: 1, EndTime: 2}}}
	profile := insights.Analyze(rec, "confidence.json", insights.Options{})
	var out bytes.Buffer
	if err := Render(&out, []*insights.Report{profile}); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"Confidence:</b> medium", "Evidence basis:</b> measured", "Replication:</b> seen in 1 of 1 runs (1 parsed)"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("missing %q", want)
		}
	}
}
