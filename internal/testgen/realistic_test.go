package testgen

import (
	"bytes"
	"testing"

	"github.com/omniviewdev/wkperf/internal/model"
)

func TestWrappedEmissionAndRealisticFixtures(t *testing.T) {
	for _, rec := range []*model.Recording{IdleMinified(), DeepMinified(), GetAnimations(), React(), Periodic(), PausedMinified()} {
		var out bytes.Buffer
		if err := Write(&out, rec, true); err != nil {
			t.Fatal(err)
		}
		if !bytes.Contains(out.Bytes(), []byte(`"version":1`)) || !bytes.Contains(out.Bytes(), []byte(`"recording":`)) {
			t.Fatalf("not wrapped: %s", out.Bytes())
		}
		got, err := model.Parse(&out)
		if err != nil || len(got.Records) != len(rec.Records) {
			t.Fatalf("round trip: %v", err)
		}
	}
	rec := DeepMinified()
	maxDepth := 0
	for _, st := range rec.Samples[0].StackTraces {
		if len(st.StackFrames) > maxDepth {
			maxDepth = len(st.StackFrames)
		}
	}
	if got := MinifiedSourceMap(); len(got) == 0 {
		t.Fatal("missing reusable source-map fixture")
	}
	if maxDepth != 366 {
		t.Fatalf("deep profile max depth = %d", maxDepth)
	}
}
