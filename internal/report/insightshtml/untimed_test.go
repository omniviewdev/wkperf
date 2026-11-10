package insightshtml

import (
	"bytes"
	"strings"
	"testing"

	"github.com/omniviewdev/wkperf/internal/insights"
	"github.com/omniviewdev/wkperf/internal/profile"
	"github.com/omniviewdev/wkperf/internal/rootcause"
	"github.com/omniviewdev/wkperf/internal/testgen"
)

func TestUntimedHTMLStackHasNoInventedTime(t *testing.T) {
	var out bytes.Buffer
	if err := RenderStacks(&out, []rootcause.Stack{{SelfMs: 25, Samples: 25, UntimedSamples: 25}}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "time unknown") || !strings.Contains(out.String(), "25 untimed samples") || strings.Contains(out.String(), "%!") {
		t.Fatalf("untimed stack has fabricated/broken time: %s", out.String())
	}
	first, last := 0.0, 1.0
	out.Reset()
	if err := RenderStacks(&out, []rootcause.Stack{{FirstSec: &first, LastSec: &last, Samples: 1}}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "0.000–1.000 s") || strings.Contains(out.String(), "%!") {
		t.Fatalf("timed stack pointer formatting failed: %s", out.String())
	}
}

func TestUntimedHTMLWindowExplainsExclusion(t *testing.T) {
	rec := testgen.UntimedSamples()
	window := profile.Clip(rec, 10.1, 10.4)
	report := insights.Analyze(window, "fixture.json", insights.Options{WindowStartSec: .1})
	var out bytes.Buffer
	if err := Render(&out, []*insights.Report{report}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "Untimed samples: 40") || !strings.Contains(out.String(), "excluded from this window") || strings.Contains(out.String(), "%!") {
		t.Fatal("HTML omits excluded samples or corrupts nullable stack times")
	}
}
