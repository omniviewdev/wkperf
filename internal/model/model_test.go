package model

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func TestParseFlatWrappedAndLegacy(t *testing.T) {
	flat := `{"displayName":"Real capture","startTime":10,"endTime":20,"discontinuities":[{"startTime":12,"endTime":18}],"records":[],"samples":[{"stackTraces":[{"timestamp":11,"stackFrames":[{"sourceID":42,"name":"leaf","line":1,"column":81}]}],"durations":[0.003]}]}`
	for _, input := range []string{flat, `{"version":1,"recording":` + flat + `,"overview":{"records":"not recording data"}}`} {
		rec, err := Parse(strings.NewReader(input))
		if err != nil {
			t.Fatal(err)
		}
		if rec.DisplayName != "Real capture" || len(rec.Discontinuities) != 1 || rec.Discontinuities[0].EndTime != 18 {
			t.Fatalf("recording not decoded: %+v", rec)
		}
		if rec.Samples[0].StackTraces[0].StackFrames[0].SourceID != "42" {
			t.Fatal("numeric sourceID not preserved")
		}
	}
	rec, err := Parse(strings.NewReader(`{"sampleStackTraces":[{"stackFrames":[{"name":"leaf"}]}],"sampleDurations":[0.002]}`))
	if err != nil || len(rec.Samples) != 1 || rec.SampleStackTraces != nil {
		t.Fatalf("legacy normalization: %+v %v", rec, err)
	}
}

func TestParseInvalidListsActualAndExpectedKeys(t *testing.T) {
	for _, input := range []string{`{"version":1,"overview":{}}`, `{"version":1,"recording":{"banana":7},"overview":{}}`, `{"recording":null}`, `[]`, `{"records":[]} {}`} {
		_, err := Parse(strings.NewReader(input))
		if err == nil {
			t.Fatalf("accepted invalid recording %s", input)
		}
		if strings.Contains(input, "banana") && (!strings.Contains(err.Error(), "banana") || !strings.Contains(err.Error(), "records") || !strings.Contains(err.Error(), "samples")) {
			t.Fatalf("error lacks actual/expected keys: %v", err)
		}
	}
}

func TestInternalSampleEvidenceIsNotExported(t *testing.T) {
	rec := &Recording{StartTime: 10, EndTime: 12, Records: []Record{}, UntimedExcluded: true,
		SampleEvidence: &SampleEvidence{StartTime: 1, EndTime: 20, Samples: []TargetSamples{}},
	}
	data, err := json.Marshal(rec)
	if err != nil {
		t.Fatal(err)
	}
	for _, field := range []string{"UntimedExcluded", "untimedExcluded", "SampleEvidence", "sampleEvidence"} {
		if strings.Contains(string(data), field) {
			t.Fatalf("internal evidence field %s leaked: %s", field, data)
		}
	}
	parsed, err := Parse(strings.NewReader(string(data)))
	if err != nil || parsed.UntimedExcluded || parsed.SampleEvidence != nil {
		t.Fatalf("internal evidence survived JSON round trip: %+v, %v", parsed, err)
	}
}

func TestLoadRegularRecording(t *testing.T) {
	path := filepath.Join(t.TempDir(), "recording.json")
	if err := os.WriteFile(path, []byte(`{"displayName":"local recording","startTime":0,"endTime":1,"records":[]}`), 0600); err != nil {
		t.Fatal(err)
	}
	for _, file := range []string{path, path + ".link"} {
		t.Run(filepath.Base(file), func(t *testing.T) {
			if file != path {
				if err := os.Symlink(path, file); err != nil {
					t.Skipf("symlinks unavailable: %v", err)
				}
			}
			rec, err := Load(file)
			if err != nil || rec.DisplayName != "local recording" {
				t.Fatalf("regular recording did not load: %+v, %v", rec, err)
			}
		})
	}
	if _, err := Load(path + ".missing"); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("missing recording error changed: %v", err)
	}
}

func TestLoadRejectsNonRegularFiles(t *testing.T) {
	dir := t.TempDir()
	for _, path := range []string{dir, os.DevNull, filepath.Join(dir, "directory.link")} {
		t.Run(filepath.Base(path), func(t *testing.T) {
			if strings.HasSuffix(path, ".link") {
				if err := os.Symlink(dir, path); err != nil {
					t.Skipf("symlinks unavailable: %v", err)
				}
			}
			if _, err := Load(path); err == nil || !strings.Contains(err.Error(), "not a regular file") || !strings.Contains(err.Error(), path) {
				t.Fatalf("missing non-regular input error: %v", err)
			}
		})
	}
}

func TestLoadRejectsFIFO(t *testing.T) {
	const helperEnv = "WKPERF_TEST_RECORDING_FIFO"
	if path := os.Getenv(helperEnv); path != "" {
		if _, err := Load(path); err == nil || !strings.Contains(err.Error(), "not a regular file") {
			t.Fatalf("missing FIFO input error: %v", err)
		}
		return
	}
	if runtime.GOOS == "windows" {
		t.Skip("named pipes use different semantics on Windows")
	}
	mkfifo, err := exec.LookPath("mkfifo")
	if err != nil {
		t.Skip("mkfifo unavailable")
	}
	path := filepath.Join(t.TempDir(), "recording.fifo")
	if output, err := exec.Command(mkfifo, path).CombinedOutput(); err != nil {
		t.Fatalf("create FIFO: %v: %s", err, output)
	}
	binary, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, binary, "-test.run=^TestLoadRejectsFIFO$")
	command.Env = append(os.Environ(), helperEnv+"="+path)
	output, err := command.CombinedOutput()
	if ctx.Err() != nil {
		t.Fatalf("Load blocked opening a FIFO: %v", ctx.Err())
	}
	if err != nil {
		t.Fatalf("FIFO load failed: %v: %s", err, output)
	}
}
