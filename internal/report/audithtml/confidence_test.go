package audithtml

import (
	"bytes"
	"fmt"
	"strings"
	"testing"

	"github.com/omniviewdev/wkperf/internal/audit"
	"github.com/omniviewdev/wkperf/internal/insights"
	"github.com/omniviewdev/wkperf/internal/model"
)

func TestConfidenceHTMLCarriesActualReplicationAndMissingRuns(t *testing.T) {
	rec := &model.Recording{EndTime: 12, Records: []model.Record{{Type: model.TypeScript, EventType: "function-call", StartTime: 1, EndTime: 2}}}
	var runs []audit.Run
	for i := range 4 {
		runs = append(runs, audit.Run{Report: insights.Analyze(rec, fmt.Sprintf("run-%d.json", i), insights.Options{})})
	}
	report := audit.Build([]audit.Scene{{Name: "scene", Runs: runs, MissingRuns: 1}}, audit.Options{})
	var out bytes.Buffer
	if err := Render(&out, report, ""); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"Confidence:</b> high", "Evidence basis:</b> measured", "Replication:</b> seen in 4 of 5 runs (4 parsed)", "4 parsed of 5 attempted", "Evidence is incomplete"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("missing %q", want)
		}
	}
}
