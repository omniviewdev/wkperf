package model

import (
	"strings"
	"testing"
)

func TestHasRenderingFrames(t *testing.T) {
	var missing *Recording
	for _, tc := range []struct {
		name string
		rec  *Recording
		want bool
	}{
		{"nil recording", missing, false},
		{"no records", &Recording{}, false},
		{"layout and script only", &Recording{Records: []Record{{Type: TypeLayout}, {Type: TypeScript}}}, false},
		{"a rendering-frame record", &Recording{Records: []Record{{Type: TypeScript}, {Type: TypeRenderingFrame}}}, true},
		{"clipped away but present in the source", &Recording{SourceHadRenderingFrames: true}, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.rec.HasRenderingFrames(); got != tc.want {
				t.Fatalf("HasRenderingFrames = %v, want %v", got, tc.want)
			}
		})
	}
}

// Exports list the rendering-frame instrument without containing any frame
// record; the instrument list alone must not count as frame data.
func TestInstrumentListIsNotRenderingFrameData(t *testing.T) {
	rec, err := Parse(strings.NewReader(`{"startTime":1,"endTime":2,"instrumentTypes":["timeline-record-type-rendering-frame"],"records":[{"type":"timeline-record-type-layout","eventType":"composite","startTime":1,"endTime":1.002}]}`))
	if err != nil {
		t.Fatal(err)
	}
	if rec.HasRenderingFrames() {
		t.Fatal("an instrument without records is not frame data")
	}
}
