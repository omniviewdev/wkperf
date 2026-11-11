package insights

import (
	"strings"
	"testing"

	"github.com/omniviewdev/wkperf/internal/flame"
	"github.com/omniviewdev/wkperf/internal/metrics"
)

func TestNumericFunctionKeysUseCanonicalIdentities(t *testing.T) {
	r := Analyze(confidenceRecording(10, 128, 0.06), "canonical.json", Options{})
	values := NumericValues(r)
	for _, f := range r.Summary.Functions {
		prefix := "functions." + f.ID
		if value, ok := values[prefix+".self_ms"]; !ok || value.Value != f.SelfMs {
			t.Errorf("canonical self key missing for %s", f.ID)
		}
		if command := FunctionCommand(r, f, "self_ms"); !strings.Contains(command, prefix+".self_ms") {
			t.Errorf("noncanonical function command: %s", command)
		}
		for _, relation := range []struct {
			name  string
			edges []metrics.CallEdge
		}{{"callers", f.Callers}, {"callees", f.Callees}} {
			for _, edge := range relation.edges {
				key := prefix + "." + relation.name + "." + edge.Function.ID + ".weight_ms"
				if value, ok := values[key]; !ok || value.Value != edge.WeightMs {
					t.Errorf("canonical edge key missing: %s", key)
				}
				if command := EdgeCommand(r, f, edge, relation.name, "weight_ms"); !strings.Contains(command, key) {
					t.Errorf("noncanonical edge command: %s", command)
				}
			}
		}
	}
	for key := range values {
		if strings.Contains(key, "%00") {
			t.Errorf("obsolete composite function key retained: %s", key)
		}
	}
	frame := metrics.FunctionFrame{Name: "native", SourceID: "7", Line: -1, Column: -1}
	want := flame.FunctionID(flame.Frame{Name: frame.Name, SourceID: frame.SourceID, Line: frame.Line, Column: frame.Column})
	if got := functionFrameID(frame); got != want {
		t.Errorf("missing ID did not use generated canonical identity: %s, want %s", got, want)
	}
}
