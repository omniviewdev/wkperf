package model

import (
	"encoding/json"
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

func TestInternalSampleEvidenceIsNotExported(t *testing.T) {
	rec := &Recording{StartTime: 10, EndTime: 12, Records: []Record{}, UntimedExcluded: true,
		SampleEvidence: &SampleEvidence{StartTime: 1, EndTime: 20, Samples: []TargetSamples{}},
	}
	data, err := json.Marshal(rec)
	if err != nil {
		t.Fatal(err)
	}
	for _, field := range []string{"UntimedExcluded", "untimedExcluded", "SampleEvidence", "sampleEvidence"} {
		if strings.Contains(string(data), field) {
			t.Fatalf("internal evidence field %s leaked: %s", field, data)
		}
	}
	parsed, err := Parse(strings.NewReader(string(data)))
	if err != nil || parsed.UntimedExcluded || parsed.SampleEvidence != nil {
		t.Fatalf("internal evidence survived JSON round trip: %+v, %v", parsed, err)
	}
}
