package compare

import (
	"strings"
	"testing"

	"github.com/omniviewdev/wkperf/internal/metrics"
)

func TestZeroActiveWindowCannotBeComparable(t *testing.T) {
	valid := []*metrics.Summary{run(10)}
	empty := []*metrics.Summary{run(0)}
	for _, tc := range []struct {
		name       string
		base, cand []*metrics.Summary
	}{
		{"baseline", empty, valid},
		{"candidate", valid, empty},
		{"both", empty, empty},
		{"one-empty-run", valid, []*metrics.Summary{run(10), run(0)}},
		{"no-baseline", nil, valid},
		{"no-runs", nil, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c := Compare(tc.base, tc.cand, DefaultOptions()).Comparability
			if c.Score != 0 {
				t.Errorf("empty selection cannot pass fixture gate: %+v", c)
			}
			found := false
			for _, w := range c.Warnings {
				if w.Code == "window-empty" && w.Level == LevelWarn && strings.Contains(w.Message, "active") {
					found = true
				}
			}
			if !found {
				t.Errorf("missing actionable window-empty warning: %+v", c)
			}
		})
	}
}
