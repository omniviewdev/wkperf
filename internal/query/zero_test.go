package query

import "testing"

func TestExplicitZeroDepthAndHotPathShare(t *testing.T) {
	engine := engineFor(fixture())
	zeroDepth := run(t, engine, Request{Operation: "subtree", Selector: "render", DepthSet: true})
	if len(zeroDepth.Rows) != 1 || zeroDepth.Rows[0].Name != "render" {
		t.Fatalf("zero depth ignored: %+v", zeroDepth.Rows)
	}
	rec := fixture()
	rec.Samples[0].Durations = []float64{.001, .0005, .9, .0003}
	engine = engineFor(rec)
	all := run(t, engine, Request{Operation: "hot-path", MinShareSet: true, MinSamplesSet: true, TieRatioSet: true})
	bounded := run(t, engine, Request{Operation: "hot-path", MinSamplesSet: true, TieRatioSet: true})
	if len(all.Rows) <= len(bounded.Rows) {
		t.Fatalf("explicit zero threshold ignored: %+v / %+v", all.Rows, bounded.Rows)
	}
}

func TestRootcauseTopIndependentOfRowLimit(t *testing.T) {
	result := run(t, engineFor(fixture()), Request{Operation: "rootcause", RootCauseTop: 17, Limit: 1})
	found := false
	for i, arg := range result.Command {
		if arg == "--top" && i+1 < len(result.Command) && result.Command[i+1] == "17" {
			found = true
		}
	}
	if !found || result.Limit != 1 {
		t.Fatalf("detail limit lost or conflated: %+v", result)
	}
	if _, err := normalize(Request{File: "x", Operation: "rootcause", RootCauseTop: -1}); err == nil {
		t.Fatal("negative detail limit accepted")
	}
	if _, err := normalize(Request{File: "x", Operation: "rootcause", RootCauseTop: MaxLimit + 1, Limit: 1}); err != nil {
		t.Fatalf("detail limit must not inherit row cap: %v", err)
	}
}
