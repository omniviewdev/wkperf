package cmd

import (
	"reflect"
	"testing"

	"github.com/omniviewdev/wkperf/internal/metrics"
)

func TestExpandVS(t *testing.T) {
	got := expandVS([]string{"compare", "a", "--vs", "b", "c", "--format", "json"})
	want := []string{"compare", "a", "--vs=b", "--vs=c", "--format", "json"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v want %v", got, want)
	}
}

func TestSplitGroups(t *testing.T) {
	b, c, err := splitGroups([]string{"a", "b"}, nil)
	if err != nil || len(b) != 1 || b[0] != "a" || c[0] != "b" {
		t.Fatalf("1v1: %v %v %v", b, c, err)
	}
	if _, _, err := splitGroups([]string{"a", "b", "c"}, nil); err == nil {
		t.Fatal("expected error without --vs")
	}
	if _, _, err := splitGroups(nil, []string{"x"}); err == nil {
		t.Fatal("expected error without baseline")
	}
}

func TestReportPath(t *testing.T) {
	cases := []struct{ out, format, ext, want string }{
		{"", "html", ".html", "wkperf-report.html"},
		{"r.html", "html", ".html", "r.html"},
		{"x/", "all", ".json", "x/wkperf-report.json"},
		{"out/rep.html", "all", ".json", "out/rep.json"},
		{"pre", "all", ".html", "pre.html"},
	}
	for _, c := range cases {
		if got := reportPath(c.out, c.format, c.ext); got != c.want {
			t.Errorf("reportPath(%q,%q,%q)=%q want %q", c.out, c.format, c.ext, got, c.want)
		}
	}
}

func TestMedianMetrics(t *testing.T) {
	mk := func(v float64) *metrics.Summary {
		return &metrics.Summary{Metrics: []metrics.Metric{{Key: "k", Value: v}}}
	}
	got := medianMetrics([]*metrics.Summary{mk(9), mk(1), mk(5), mk(100)})
	if got[0].Value != 7 {
		t.Fatalf("median = %v, want 7", got[0].Value)
	}
}
