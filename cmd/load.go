package cmd

import (
	"fmt"
	"math"
	"os"
	"path/filepath"
	"sort"

	"github.com/spf13/cobra"

	"github.com/omniviewdev/wkperf/internal/analyze"
	"github.com/omniviewdev/wkperf/internal/diagnostic"
	"github.com/omniviewdev/wkperf/internal/metrics"
	"github.com/omniviewdev/wkperf/internal/model"
	"github.com/omniviewdev/wkperf/internal/profile"
	"github.com/omniviewdev/wkperf/internal/sourcemap"
)

// expandPaths turns files and directories into a flat list of recording
// files. A directory contributes its *.json children (non-recursive).
func expandPaths(paths []string) ([]string, error) {
	var files []string
	for _, p := range paths {
		st, err := os.Stat(p)
		if err != nil {
			return nil, diagnostic.Wrap(diagnostic.LoadError, p, err)
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
			return nil, diagnostic.Wrap(diagnostic.InputError, p, fmt.Errorf("directory contains no .json files"))
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
		if _, _, err := windowBounds(rec, wo); err != nil {
			return nil, nil, diagnostic.Wrap(diagnostic.WindowError, f, err)
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

// windowBounds validates the shared recording-relative selection before any
// metrics or grades are computed. Stopped capture time is not active evidence.
func windowBounds(rec *model.Recording, wo analyze.Options) (lo, hi float64, err error) {
	lo = rec.StartTime + math.Max(0, math.Max(wo.TrimStartSec, wo.FromSec))
	hi = rec.EndTime - math.Max(0, wo.TrimEndSec)
	if wo.ToSec > 0 {
		hi = math.Min(hi, rec.StartTime+wo.ToSec)
	}
	if hi <= lo {
		return lo, hi, fmt.Errorf("window is empty: %.2fs..%.2fs of a %.2fs recording", lo-rec.StartTime, hi-rec.StartTime, rec.EndTime-rec.StartTime)
	}
	if profile.ActiveDuration(rec, lo, hi) <= 0 {
		return lo, hi, fmt.Errorf("window has no active capture time: %.2fs..%.2fs of a %.2fs recording", lo-rec.StartTime, hi-rec.StartTime, rec.EndTime-rec.StartTime)
	}
	return lo, hi, nil
}

// clipRecording returns rec restricted to the window wo, with the same
// semantics as analyze.SummarizeWith. Untimed samples and their original span
// metadata survive clipping but are excluded from windowed sample aggregates.
// It returns rec itself when the window covers the whole recording.
func clipRecording(rec *model.Recording, wo analyze.Options) (*model.Recording, error) {
	lo, hi, err := windowBounds(rec, wo)
	if err != nil {
		return nil, err
	}
	if profile.CoversRecording(rec, lo-rec.StartTime, hi-rec.StartTime) {
		return rec, nil
	}
	return profile.Clip(rec, lo, hi), nil
}
