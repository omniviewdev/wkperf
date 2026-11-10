package model

import (
	"strings"
	"testing"
)

func TestParseFlatWrappedAndLegacy(t *testing.T) {
	flat := `{"displayName":"Real capture","startTime":10,"endTime":20,"discontinuities":[{"startTime":12,"endTime":18}],"records":[],"samples":[{"stackTraces":[{"timestamp":11,"stackFrames":[{"sourceID":42,"name":"leaf","line":1,"column":81}]}],"durations":[0.003]}]}`
	for _, input := range []string{flat, `{"version":1,"recording":` + flat + `,"overview":{"records":"not recording data"}}`} {
		rec, err := Parse(strings.NewReader(input))
		if err != nil {
			t.Fatal(err)
		}
		if rec.DisplayName != "Real capture" || len(rec.Discontinuities) != 1 || rec.Discontinuities[0].EndTime != 18 {
			t.Fatalf("recording not decoded: %+v", rec)
		}
		if rec.Samples[0].StackTraces[0].StackFrames[0].SourceID != "42" {
			t.Fatal("numeric sourceID not preserved")
		}
	}
	rec, err := Parse(strings.NewReader(`{"sampleStackTraces":[{"stackFrames":[{"name":"leaf"}]}],"sampleDurations":[0.002]}`))
	if err != nil || len(rec.Samples) != 1 || rec.SampleStackTraces != nil {
		t.Fatalf("legacy normalization: %+v %v", rec, err)
	}
}

func TestParseInvalidListsActualAndExpectedKeys(t *testing.T) {
	for _, input := range []string{`{"version":1,"overview":{}}`, `{"version":1,"recording":{"banana":7},"overview":{}}`, `{"recording":null}`, `[]`, `{"records":[]} {}`} {
		_, err := Parse(strings.NewReader(input))
		if err == nil {
			t.Fatalf("accepted invalid recording %s", input)
		}
		if strings.Contains(input, "banana") && (!strings.Contains(err.Error(), "banana") || !strings.Contains(err.Error(), "records") || !strings.Contains(err.Error(), "samples")) {
			t.Fatalf("error lacks actual/expected keys: %v", err)
		}
	}
}
