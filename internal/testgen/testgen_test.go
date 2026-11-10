package testgen

import (
	"bytes"
	"encoding/json"
	"math"
	"reflect"
	"testing"

	"github.com/omniviewdev/wkperf/internal/model"
)

func TestDeterministic(t *testing.T) {
	a := Generate(7, Baseline())
	b := Generate(7, Baseline())
	if !reflect.DeepEqual(a, b) {
		t.Fatal("same seed produced different recordings")
	}
	c := Generate(8, Baseline())
	if reflect.DeepEqual(a, c) {
		t.Fatal("different seeds produced identical recordings")
	}
}

func TestRoundTripParse(t *testing.T) {
	rec := Generate(1, Baseline())
	data, err := json.Marshal(rec)
	if err != nil {
		t.Fatal(err)
	}
	got, err := model.Parse(bytes.NewReader(data))
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Records) != len(rec.Records) {
		t.Fatalf("records = %d, want %d", len(got.Records), len(rec.Records))
	}
	if len(got.Samples) != 1 || len(got.Samples[0].StackTraces) != len(got.Samples[0].Durations) {
		t.Fatalf("bad samples: %+v", len(got.Samples))
	}
}

func TestFrameMeanFollowsParams(t *testing.T) {
	mean := func(p Params) float64 {
		rec := Generate(3, p)
		var sum float64
		var n int
		for _, r := range rec.Records {
			if r.Type == model.TypeRenderingFrame {
				sum += r.Duration() * 1000
				n++
			}
		}
		return sum / float64(n)
	}
	p := Baseline()
	p.DurationSec = 60
	if m := mean(p); math.Abs(m-p.FrameMeanMs) > 1 {
		t.Errorf("baseline mean frame = %.2f ms, want ~%.1f", m, p.FrameMeanMs)
	}
	q := Improved()
	q.DurationSec = 60
	if mean(q) >= mean(p) {
		t.Error("improved frames not faster than baseline")
	}
}

func TestSampleWeights(t *testing.T) {
	p := Baseline()
	p.DurationSec = 100
	rec := Generate(5, p)
	counts := map[string]int{}
	for _, st := range rec.Samples[0].StackTraces {
		top := st.StackFrames[len(st.StackFrames)-1]
		counts[top.Name]++
	}
	var total float64
	for _, f := range p.Functions {
		total += f.Weight
	}
	n := float64(len(rec.Samples[0].StackTraces))
	for _, f := range p.Functions {
		got, want := float64(counts[f.Name])/n, f.Weight/total
		if math.Abs(got-want) > 0.03 {
			t.Errorf("%s share = %.3f, want ~%.3f", f.Name, got, want)
		}
	}
}

func TestContainsGCAndAllRecordKinds(t *testing.T) {
	p := Baseline()
	p.DurationSec = 30
	rec := Generate(2, p)
	seen := map[string]bool{}
	for _, r := range rec.Records {
		seen[r.Type] = true
		if r.EventType == "garbage-collected" && !bytes.Contains(r.Details, []byte("GarbageCollection")) {
			t.Error("GC record missing GarbageCollection details")
		}
		if r.EventType == "garbage-collected" {
			seen["gc"] = true
		}
	}
	for _, k := range []string{model.TypeRenderingFrame, model.TypeLayout, model.TypeScript, model.TypeCPU, model.TypeMemory, model.TypeNetwork, "gc"} {
		if !seen[k] {
			t.Errorf("missing %s", k)
		}
	}
}
