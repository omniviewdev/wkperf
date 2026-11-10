package cmd

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/omniviewdev/wkperf/internal/analyze"
	"github.com/omniviewdev/wkperf/internal/metrics"
	"github.com/omniviewdev/wkperf/internal/model"
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

// loadGroup loads every file (or directory of files) and summarizes it.
// Errors name the offending file.
func loadGroup(paths []string) ([]*metrics.Summary, []string, error) {
	files, err := expandPaths(paths)
	if err != nil {
		return nil, nil, err
	}
	if len(files) == 0 {
		return nil, nil, fmt.Errorf("no input files")
	}
	sums := make([]*metrics.Summary, 0, len(files))
	for _, f := range files {
		rec, err := model.Load(f)
		if err != nil {
			if !strings.Contains(err.Error(), f) {
				err = fmt.Errorf("%s: %w", f, err)
			}
			return nil, nil, err
		}
		sums = append(sums, analyze.Summarize(rec, f))
	}
	return sums, files, nil
}
