package cmd

import (
	"encoding/json"
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"github.com/omniviewdev/wkperf/internal/diagnostic"
	"github.com/omniviewdev/wkperf/internal/model"
	"github.com/omniviewdev/wkperf/internal/schema"
)

func TestSummaryAndCompareRejectEmptyActiveWindows(t *testing.T) {
	for _, tc := range []struct {
		name string
		rec  model.Recording
		args []string
	}{
		{name: "outside", rec: model.Recording{StartTime: 10, EndTime: 12}, args: []string{"--from", "1000", "--to", "2000"}},
		{name: "zero-duration", rec: model.Recording{StartTime: 10, EndTime: 10}},
		{name: "paused-selection", rec: model.Recording{StartTime: 10, EndTime: 12, Discontinuities: []model.Discontinuity{{StartTime: 10.5, EndTime: 11.5}}}, args: []string{"--from", "0.5", "--to", "1.5"}},
		{name: "all-paused", rec: model.Recording{StartTime: 10, EndTime: 12, Discontinuities: []model.Discontinuity{{StartTime: 10, EndTime: 12}}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			file := filepath.Join(t.TempDir(), "empty.json")
			tc.rec.Records = []model.Record{}
			writeFlameJSON(t, file, &tc.rec)
			for _, format := range []string{"json", "term"} {
				for _, command := range []string{"summary", "compare"} {
					t.Run(command+"-"+format, func(t *testing.T) {
						args := []string{command, file}
						if command == "compare" {
							args = append(args, file, "--strict-fixture", "--min-comparability", "0.99", "--fail-on-regression")
						}
						args = append(args, tc.args...)
						args = append(args, "--format", format)
						out, stderr, err := runPipeline(t, args...)
						var input *diagnostic.Error
						if !errors.As(err, &input) || input.Code != "window_error" || input.File != file {
							t.Fatalf("want typed exit-1 window error; got %v, stdout %.300s", err, out)
						}
						var exit *ExitError
						if errors.As(err, &exit) && exit.Code != 1 {
							t.Fatalf("empty window reached CI gates: %v", err)
						}
						if !strings.Contains(stderr, file) || !strings.Contains(stderr, "window") {
							t.Fatalf("missing window diagnostic: %q", stderr)
						}
						if format == "json" {
							var doc schema.ErrorDocument
							if decodeErr := json.Unmarshal([]byte(out), &doc); decodeErr != nil || len(doc.Errors) != 1 || doc.Errors[0].Code != "window_error" || doc.Errors[0].File != file {
								t.Fatalf("want one structured window error: %v %s", decodeErr, out)
							}
							for _, forbidden := range []string{"comparability", "metrics", "scorecard"} {
								if _, ok := decodeDocument(t, out)[forbidden]; ok {
									t.Fatalf("empty window rendered %s", forbidden)
								}
							}
						} else if out != "" {
							t.Fatalf("empty window rendered healthy output: %q", out)
						}
					})
				}
			}
		})
	}
}

func TestSummaryKeepsValidIdleWindow(t *testing.T) {
	file := filepath.Join(t.TempDir(), "idle.json")
	writeFlameJSON(t, file, &model.Recording{StartTime: 10, EndTime: 12, Records: []model.Record{}})
	out, _, err := runPipeline(t, "summary", file, "--from", "0.25", "--to", "0.75", "--format", "json")
	if err != nil || !strings.Contains(out, "overview.duration_s") {
		t.Fatalf("idle is active capture time, not an empty selection: %v %s", err, out)
	}
}
