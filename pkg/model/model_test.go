package model_test

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/omniviewdev/wkperf/pkg/model"
)

func TestCaptureCapabilitiesRoundTrip(t *testing.T) {
	const capabilities = `{"CPUProfiler":"captured","Memory":"enabled-no-samples","futureInstrument":"future-status"}`
	const payload = `{"records":[],"captureCapabilities":` + capabilities + `}`
	for _, tc := range []struct {
		name, input, want string
	}{
		{"flat", payload, capabilities},
		{"wrapped", `{"version":1,"recording":` + payload + `,"overview":{}}`, capabilities},
		{"absent", `{"records":[]}`, ""},
		{"empty", `{"records":[],"captureCapabilities":{}}`, `{}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			recording, err := model.Parse(strings.NewReader(tc.input))
			if err != nil {
				t.Fatal(err)
			}
			if (recording.CaptureCapabilities == nil) != (tc.want == "") {
				t.Fatalf("unexpected decoded capabilities: %#v", recording.CaptureCapabilities)
			}
			data, err := json.Marshal(recording)
			if err != nil {
				t.Fatal(err)
			}
			var export struct {
				CaptureCapabilities json.RawMessage `json:"captureCapabilities"`
			}
			if err := json.Unmarshal(data, &export); err != nil {
				t.Fatal(err)
			}
			if got := string(export.CaptureCapabilities); got != tc.want {
				t.Fatalf("encoded captureCapabilities = %q, want %q", got, tc.want)
			}
		})
	}
}
