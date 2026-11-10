package insightshtml

import (
	"bytes"
	"strings"
	"testing"

	"github.com/omniviewdev/wkperf/internal/flame"
	"github.com/omniviewdev/wkperf/internal/insights"
	"github.com/omniviewdev/wkperf/internal/rootcause"
)

func TestRootCauseHTMLVisibleAndEscaped(t *testing.T) {
	frame := flame.Frame{Name: "App<script>", URL: "bundle.js", Line: 1, Column: 44, Original: &flame.Location{URL: "src/App.tsx", Line: 5, Column: 6}}
	stat := rootcause.Stat{Key: "samples.self_ms", Value: 8, Unit: "ms", Basis: "sampled", Command: "wkperf query 'x<unsafe>.json' --view owners"}
	stack := rootcause.Stack{Frames: []flame.Frame{frame}, SelfMs: 8, Samples: 4, Stats: []rootcause.Stat{stat}, Command: stat.Command}
	rc := &rootcause.Report{HotPath: rootcause.Path{Frames: []flame.Frame{frame}, SampledMs: 8, Samples: 4, Stats: []rootcause.Stat{stat}, Command: "wkperf query x.json --view hot-path"}, Owners: []rootcause.Owner{{Category: "app", SelfMs: 8, Pct: 100, Stats: []rootcause.Stat{stat}}}, ContextAttribution: []rootcause.Attribution{{Frame: frame, TotalMs: 8, Stacks: []rootcause.Stack{stack}}}, Findings: []rootcause.Finding{{Title: "Repeated commits", Explanation: "hypothesis <unsafe>", Evidence: []rootcause.Stack{stack}}}, Windows: []rootcause.Window{{Kind: "long-task", FromSec: 0.25, ToSec: 0.4, DurationMs: 150, TopStack: &stack, Command: "wkperf query x.json --from 0.25 --to 0.4"}}}
	rc.HotPath.Nodes = []rootcause.PathNode{{Frame: frame, TotalMs: 8, Share: 1, Samples: 7}}
	rc.HotPath.Branches = []rootcause.PathNode{{Frame: flame.Frame{Name: "branch", URL: "bundle.js", Line: 2}, TotalMs: 4, Share: .5, Samples: 3}}
	rc.HotPath.BranchesTotal, rc.HotPath.StopReason = 1, "sample-support"
	rc.Findings[0].NativeReads = []rootcause.NativeRead{{API: "getAnimations", Flush: "style", Samples: 7, StyleSamples: 7, StyleMs: 8}}
	rc.Findings[0].Depth = &rootcause.DepthFacets{MaxDepth: 40, CommitFrames: 32, ReactFiberLevels: 16}
	rc.Findings[0].RelatedFindings = []string{"style-recalc-heavy"}
	rc.ContextAttribution[0].Frame.Category = flame.CategoryUnknownBundled
	rc.ContextUnavailable = rootcause.AttributionUnavailable{SelfMs: 3, Samples: 2, Reason: "scheduling component is not visible"}
	var b bytes.Buffer
	if err := Render(&b, []*insights.Report{{Name: "profile", RootCause: rc}}); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"Hot path", "Owner breakdown", "Nearest non-framework", "Exact-stack", "Worst-window triage", "bundle.js:1:44", "src/App.tsx:5:6", "samples.self_ms", "--view hot-path", "--from 0.25 --to 0.4", "App&lt;script&gt;", "hypothesis &lt;unsafe&gt;"} {
		if !strings.Contains(b.String(), want) {
			t.Errorf("missing %q", want)
		}
	}
	for _, want := range []string{"7 samples", "100.0% share", "Competing branches", "sample-support", "unknown-bundled", "scheduling component", "style flush", "7/7 timed samples", "Depth facets", "style-recalc-heavy"} {
		if !strings.Contains(b.String(), want) {
			t.Errorf("missing facet %q", want)
		}
	}
	if strings.Contains(b.String(), "<unsafe>") || strings.Contains(b.String(), "App<script>") {
		t.Fatal("unescaped root cause data")
	}
}
