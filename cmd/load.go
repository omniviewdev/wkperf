package cmd

import (
	"fmt"
	"math"
	"os"
	"path/filepath"
	"sort"

	"github.com/spf13/cobra"

	"github.com/omniviewdev/wkperf/internal/analyze"
	"github.com/omniviewdev/wkperf/internal/metrics"
	"github.com/omniviewdev/wkperf/internal/model"
	"github.com/omniviewdev/wkperf/internal/sourcemap"
)

// expandPaths turns files and directories into a flat list of recording
// files. A directory contributes its *.json children (non-recursive).
func expandPaths(paths []string) ([]string, error) {
	var files []string
	for _, p := range paths {
		st, err := os.Stat(p)
		if err != nil {
			return nil, err
		}
		if !st.IsDir() {
			files = append(files, p)
			continue
		}
		matches, err := filepath.Glob(filepath.Join(p, "*.json"))
		if err != nil {
			return nil, err
		}
		sort.Strings(matches)
		var n int
		for _, m := range matches {
			if st, err := os.Stat(m); err == nil && !st.IsDir() {
				files = append(files, m)
				n++
			}
		}
		if n == 0 {
			return nil, fmt.Errorf("%s: directory contains no .json files", p)
		}
	}
	return files, nil
}

// loaded is one summarized recording plus the (unwindowed) recording itself,
// kept so insights can be built later for the representative runs.
type loaded struct {
	Sum       *metrics.Summary
	Rec       *model.Recording
	SourceMap sourcemap.Diagnostics
}

// loadGroup loads every file (or directory of files) and summarizes it
// within the window wo. Errors name the offending file.
func loadGroup(cmd *cobra.Command, paths []string, wo analyze.Options) ([]*loaded, []string, error) {
	files, err := expandPaths(paths)
	if err != nil {
		return nil, nil, err
	}
	if len(files) == 0 {
		return nil, nil, fmt.Errorf("no input files")
	}
	out := make([]*loaded, 0, len(files))
	for _, f := range files {
		rec, diagnostics, err := loadRecording(cmd, f)
		if err != nil {
			return nil, nil, err
		}
		out = append(out, &loaded{Sum: analyze.SummarizeWith(rec, f, wo), Rec: rec, SourceMap: diagnostics})
	}
	return out, files, nil
}

func summaries(ls []*loaded) []*metrics.Summary {
	out := make([]*metrics.Summary, len(ls))
	for i, l := range ls {
		out[i] = l.Sum
	}
	return out
}

// clipRecording returns rec restricted to the window wo, with the same
// semantics as analyze.SummarizeWith: spans are clipped, instants and samples
// kept by timestamp. Times stay absolute; StartTime/EndTime become the window.
// It returns rec itself when the window covers the whole recording.
func clipRecording(rec *model.Recording, wo analyze.Options) (*model.Recording, error) {
	lo := rec.StartTime + math.Max(0, math.Max(wo.TrimStartSec, wo.FromSec))
	hi := rec.EndTime - math.Max(0, wo.TrimEndSec)
	if wo.ToSec > 0 {
		hi = math.Min(hi, rec.StartTime+wo.ToSec)
	}
	if lo == rec.StartTime && hi == rec.EndTime {
		return rec, nil
	}
	if hi <= lo {
		return nil, fmt.Errorf("window is empty: %.2fs..%.2fs of a %.2fs recording", lo-rec.StartTime, hi-rec.StartTime, rec.EndTime-rec.StartTime)
	}
	at := func(t float64) bool { return t >= lo && t < hi }
	out := *rec
	out.StartTime, out.EndTime = lo, hi
	out.Records = make([]model.Record, 0, len(rec.Records))
	for _, r := range rec.Records {
		span := r.StartTime != 0 || r.EndTime != 0
		switch {
		case !span:
			if !at(r.Timestamp) {
				continue
			}
		case r.EndTime <= r.StartTime:
			if !at(r.StartTime) {
				continue
			}
		default:
			if r.EndTime <= lo || r.StartTime >= hi {
				continue
			}
			r.StartTime = math.Max(r.StartTime, lo)
			r.EndTime = math.Min(r.EndTime, hi)
		}
		out.Records = append(out.Records, r)
	}
	out.Markers = nil
	for _, m := range rec.Markers {
		if at(m.Time) {
			out.Markers = append(out.Markers, m)
		}
	}
	out.MemoryPressureEvents = nil
	for _, m := range rec.MemoryPressureEvents {
		if at(m.Timestamp) {
			out.MemoryPressureEvents = append(out.MemoryPressureEvents, m)
		}
	}
	out.Samples = nil
	for _, ts := range rec.Samples {
		n := ts
		n.StackTraces, n.Durations = nil, nil
		for i, st := range ts.StackTraces {
			if !at(st.Timestamp) {
				continue
			}
			n.StackTraces = append(n.StackTraces, st)
			if i < len(ts.Durations) {
				n.Durations = append(n.Durations, ts.Durations[i])
			}
		}
		out.Samples = append(out.Samples, n)
	}
	return &out, nil
}
