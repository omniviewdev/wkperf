//go:build unix

package sourcemap

import (
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/omniviewdev/wkperf/internal/model"
)

func TestResolveRejectsFIFOMap(t *testing.T) {
	for _, mode := range []string{"explicit", "directory", "adjacent"} {
		t.Run(mode, func(t *testing.T) {
			dir := t.TempDir()
			mapPath := filepath.Join(dir, "bundle.js.map")
			if err := syscall.Mkfifo(mapPath, 0600); err != nil {
				t.Fatal(err)
			}
			paths := []string{mapPath}
			recordingPath := ""
			switch mode {
			case "directory":
				paths = []string{dir}
			case "adjacent":
				paths = nil
				recordingPath = filepath.Join(dir, "capture.json")
			}
			rec := &model.Recording{Samples: []model.TargetSamples{{StackTraces: []model.StackTrace{{StackFrames: []model.StackFrame{{Name: "a", URL: "https://app.test/bundle.js", Line: 1, Column: 2}}}}}}}
			type result struct {
				diagnostics Diagnostics
				err         error
			}
			done := make(chan result, 1)
			go func() {
				d, err := Resolve(rec, paths, recordingPath)
				done <- result{d, err}
			}()
			select {
			case got := <-done:
				if mode == "explicit" {
					if got.err == nil || !strings.Contains(got.err.Error(), "not a regular file") || !strings.Contains(got.err.Error(), mapPath) {
						t.Fatalf("non-regular map error: %v", got.err)
					}
				} else if got.err != nil || len(got.diagnostics.Warnings) != 1 || !strings.Contains(got.diagnostics.Warnings[0], "not a regular file") {
					t.Fatalf("discovered non-regular map diagnostics: %+v %v", got.diagnostics, got.err)
				}
			case <-time.After(2 * time.Second):
				t.Fatal("source-map resolution blocked on FIFO")
			}
		})
	}
}
