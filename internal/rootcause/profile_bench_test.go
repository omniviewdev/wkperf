package rootcause_test

import (
	"os"
	"testing"

	"github.com/omniviewdev/wkperf/internal/flame"
	"github.com/omniviewdev/wkperf/internal/model"
	"github.com/omniviewdev/wkperf/internal/rootcause"
)

// BenchmarkRealProfile uses a local WebKit recording without timing parsing.
//
//	WKPERF_BENCH_RECORDING=/absolute/path/to/recording.json go test ./internal/rootcause -run '^$' -bench '^BenchmarkRealProfile$' -benchmem
func BenchmarkRealProfile(b *testing.B) {
	path := os.Getenv("WKPERF_BENCH_RECORDING")
	if path == "" {
		b.Skip("set WKPERF_BENCH_RECORDING to an absolute path to a WebKit recording")
	}
	rec, err := model.Load(path)
	if err != nil {
		b.Fatal(err)
	}
	tree := flame.Build(rec)
	if tree.SampleCount() == 0 {
		b.Fatal("recording has no retained JavaScript stack samples")
	}
	b.Run("Flame", func(b *testing.B) {
		b.ReportAllocs()
		for b.Loop() {
			flame.Build(rec)
		}
	})
	b.Run("SampleIDs", func(b *testing.B) {
		b.ReportAllocs()
		for b.Loop() {
			rootcause.SampleIDs(tree, nil)
		}
	})
	b.Run("Rootcause", func(b *testing.B) {
		b.ReportAllocs()
		for b.Loop() {
			rootcause.Build(rec, tree, rootcause.Options{})
		}
	})
}
