package query

import (
	"strings"
	"testing"

	"github.com/omniviewdev/wkperf/internal/testgen"
)

func TestThresholdQuerySettingsReproduceDerivedEvidence(t *testing.T) {
	rec := testgen.ThresholdFrames()
	for key, want := range map[string]float64{"findings.jank.ratio": .5, "findings.jank.threshold_ms": 325, "frames.jank_ratio": 1} {
		result := run(t, engineFor(rec), Request{Operation: "metrics", Selector: key, JankMS: 325, LongTaskMS: 100})
		if len(result.Rows) != 1 || result.Rows[0].Value != want {
			t.Errorf("%s = %+v; want %g", key, result.Rows, want)
		}
		command := strings.Join(result.Command, " ")
		if !strings.Contains(command, "--jank-ms 325") || !strings.Contains(command, "--long-task-ms 100") {
			t.Errorf("command loses thresholds: %s", command)
		}
	}
}

func TestThresholdRequestSchemaExplainsFixedStandards(t *testing.T) {
	for _, operation := range []string{"metrics", "rootcause", "events", "window"} {
		properties := RequestSchema(operation)["properties"].(map[string]any)
		for key, want := range map[string]float64{"jank_ms": 16.7, "long_task_ms": 50} {
			field := properties[key].(map[string]any)
			description, _ := field["description"].(string)
			if field["default"] != want || description == "" || key == "jank_ms" && !strings.Contains(description, "16.7") {
				t.Errorf("%s schema loses threshold semantics: %v", key, field)
			}
		}
	}
}
