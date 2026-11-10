package rootcause_test

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/omniviewdev/wkperf/internal/flame"
	"github.com/omniviewdev/wkperf/internal/model"
	"github.com/omniviewdev/wkperf/internal/rootcause"
)

func TestNativeCorroborationUsesHalfOpenTimedSamplesAndUnionRecords(t *testing.T) {
	r := recording()
	r.Records = append(r.Records,
		model.Record{Type: model.TypeLayout, EventType: "recalculate-styles", StartTime: r.StartTime + .09, EndTime: r.StartTime + .2},
		model.Record{Type: model.TypeLayout, EventType: "recalculate-styles", StartTime: r.StartTime + .1, EndTime: r.StartTime + .17},
	)
	for _, at := range []float64{.09, .199, .2} {
		sample(r, at, 2, app("tick"), model.StackFrame{Name: "getAnimations"})
	}
	sample(r, -10, 30, app("tick"), model.StackFrame{Name: "getAnimations"})
	report := build(r)
	f := finding(report, "forced-style-or-layout-read")
	if f == nil || len(f.NativeReads) != 1 || f.NativeReads[0].Samples != 3 || f.NativeReads[0].StyleSamples != 2 {
		t.Fatalf("unknown clock or boundary fabricated overlap: %+v", f)
	}
	near(t, f.NativeReads[0].StyleMs, 110)
	near(t, f.NativeReads[0].SelfMs, 6)
	if !strings.Contains(f.Explanation, "no timestamp") || len(f.SampleIDs) != 3 {
		t.Fatalf("untimed attribution caveat missing: %+v", f)
	}
	if got := rootcause.SampleIDs(flame.Build(r), func(s flame.Sample) bool { return !s.Untimed }); len(got) != len(f.SampleIDs) {
		t.Fatalf("exact timed membership changed: %v vs %v", got, f.SampleIDs)
	}
}

func TestNativeFacetSelectionPreservesTotalsAndUnselectedStats(t *testing.T) {
	r := recording()
	sample(r, .1, 7, app("measure"), model.StackFrame{Name: "getAnimations"}, model.StackFrame{Name: "getBoundingClientRect"})
	report := build(r)
	selected := rootcause.SelectEvidence(report, 1)
	full := finding(report, "forced-style-or-layout-read")
	visible := finding(selected, "forced-style-or-layout-read")
	if full == nil || visible == nil || len(full.NativeReads) != 2 || len(visible.NativeReads) != 1 || visible.NativeReadsTotal != 2 {
		t.Fatalf("native facet cap lost exact total: full=%+v selected=%+v", full, visible)
	}
	near(t, stat(rootcause.Stats(report), "native_reads.getBoundingClientRect.self_ms"), 7)
	near(t, stat(rootcause.Stats(report), "native_reads.self_ms"), 7)
}

func TestOwnershipSettingsSurviveSuppliedTreeAndAllScalarCommands(t *testing.T) {
	r := recording()
	sample(r, .1, 2, bundled("measure"), model.StackFrame{Name: "getAnimations"})
	rules := flame.OwnershipRules{AppURLs: []string{"wails://localhost/assets/*.js"}, LibraryURLs: []string{"wails://localhost/vendor/*.js"}}
	tree := flame.Build(r, flame.Options{Ownership: rules})
	report := rootcause.Build(r, tree, rootcause.Options{Source: "capture.json", Ownership: flame.OwnershipRules{LibraryURLs: []string{"wrong/*"}}})
	if len(report.ContextAttribution) != 1 || report.ContextAttribution[0].Frame.Category != flame.CategoryApp || len(report.Ownership.AppURLs) != 1 || report.Ownership.AppURLs[0] != rules.AppURLs[0] {
		t.Fatalf("effective ownership settings or context lost: %+v", report)
	}
	for _, s := range rootcause.Stats(report) {
		if !strings.Contains(s.Command, "--app-url 'wails://localhost/assets/*.js'") || !strings.Contains(s.Command, "--library-url 'wails://localhost/vendor/*.js'") || strings.Contains(s.Command, "wrong/*") {
			t.Fatalf("scalar command cannot reproduce effective ownership: %+v", s)
		}
	}
}

func TestRawEvidenceOmitsCompetingSeverityAndSampleMembership(t *testing.T) {
	r := recording()
	sample(r, .1, 2, app("measure"), model.StackFrame{Name: "getAnimations"})
	f := finding(build(r), "forced-style-or-layout-read")
	payload, err := json.Marshal(f)
	if err != nil {
		t.Fatal(err)
	}
	if f == nil || len(f.SampleIDs) != 1 || strings.Contains(string(payload), `"severity"`) || strings.Contains(string(payload), "sampleIds") || strings.Contains(string(payload), "sampleIDs") {
		t.Fatalf("raw evidence invented priority or leaked membership: %s", payload)
	}
}

func TestSampleMembershipKeepsDuplicateSamplesDistinct(t *testing.T) {
	r := recording()
	for range 6 {
		sample(r, .1, 2, app("measure"), model.StackFrame{Name: "getAnimations"})
	}
	tree := flame.Build(r)
	all := rootcause.SampleIDs(tree, nil)
	f := finding(rootcause.Build(r, tree, rootcause.Options{}), "forced-style-or-layout-read")
	seen := map[string]bool{}
	for _, id := range all {
		seen[id] = true
	}
	if len(all) != 6 || len(seen) != 6 || f == nil || len(f.SampleIDs) != 6 {
		t.Fatalf("duplicate samples collapsed: ids=%v finding=%+v", all, f)
	}
	for _, id := range f.SampleIDs {
		if !seen[id] {
			t.Fatalf("raw and canonical membership schemes differ: %s", id)
		}
	}
}
