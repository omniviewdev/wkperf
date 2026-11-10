package compare

import (
	"strings"
	"testing"

	"github.com/omniviewdev/wkperf/internal/metrics"
)

func TestStackQueryUsesContributingRun(t *testing.T) {
	f := metrics.FuncStat{ID: "function-stable", Name: "leaf", URL: "bundle.js", Line: 1, Column: 90, SelfMs: 20, CommonStackID: "stack-stable", CommonStack: []metrics.FunctionFrame{{Name: "leaf", URL: "bundle.js", Line: 1, Column: 90}}}
	r := Compare([]*metrics.Summary{{}}, []*metrics.Summary{{Source: "empty.json"}, {Source: "actual  candidate.json", Functions: []metrics.FuncStat{f}}}, DefaultOptions())
	if len(r.FuncRegressed) != 1 {
		t.Fatal("missing function regression")
	}
	want := "wkperf query 'actual  candidate.json' stacks 'stack-stable' --format json"
	if got := r.FuncRegressed[0].StackCommand(); got != want {
		t.Fatalf("wrong stack source: %s", got)
	}
	r = Compare([]*metrics.Summary{{Source: "actual  baseline.json", Functions: []metrics.FuncStat{f}}}, []*metrics.Summary{{Source: "empty.json"}}, DefaultOptions())
	if len(r.FuncImproved) != 1 || !strings.Contains(r.FuncImproved[0].StackCommand(), "'actual  baseline.json'") {
		t.Fatal("removed function lost stack provenance")
	}
}
