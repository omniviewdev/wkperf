package model

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/omniviewdev/wkperf/internal/diagnostic"
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

func TestParseWrappedEnvelopePrecedence(t *testing.T) {
	const payload = `{"displayName":"quote \" slash \\ braces } ] { ,","records":[],"samples":[{"stackTraces":[{"timestamp":1.25,"stackFrames":[{"sourceID":9007199254740993,"name":"leaf","line":1,"column":81,"original":{"url":"src.ts","line":2,"column":5}},{"sourceID":"\u0032","name":"root","line":3,"column":9}]}],"durations":[0.005]}]}`
	want, err := Parse(strings.NewReader(payload))
	if err != nil {
		t.Fatal(err)
	}
	input := "\n\t" + `{"records":"ignored","recording":[false],"overview":{"recording":{"samples":[]}},"rEc\u006FrDiNg" : ` + payload + `  }` + "\r\n"
	got, err := Parse(strings.NewReader(input))
	if err != nil || !reflect.DeepEqual(got, want) {
		t.Fatalf("wrapped payload = %+v, %v; want %+v", got, err, want)
	}
	trace := got.Samples[0].StackTraces[0]
	leaf, root := trace.StackFrames[0], trace.StackFrames[1]
	if trace.Timestamp != 1.25 || got.Samples[0].Durations[0] != 0.005 ||
		leaf.SourceID != "9007199254740993" || leaf.Name != "leaf" || leaf.Line != 1 || leaf.Column != 81 ||
		leaf.Original == nil || *leaf.Original != (SourceLocation{URL: "src.ts", Line: 2, Column: 5}) ||
		root.SourceID != "2" || root.Name != "root" || root.Line != 3 || root.Column != 9 {
		t.Fatalf("changed sample coordinates or frame identity: %+v", trace)
	}
}

func TestParseMalformedEnvelopePrecedence(t *testing.T) {
	for _, tc := range []struct{ input, want string }{
		{`{"recording":{"records":[]},"RECORDING":null,"records":[]}`, "actual top-level keys: [RECORDING, recording, records]"},
		{`{"recording":{"reco\u0072ds":false,"a":[]},"overview":null}`, "actual keys: [a, records]"},
		{`{"records":[}],"recording":{"records":[]}}`, "expected an object containing"},
		{`{"recording":null} trailing`, "expected exactly one JSON object"},
		{`{"records":[]} false`, "expected exactly one JSON object"},
		{`null`, "actual keys: []"},
	} {
		if got, err := Parse(strings.NewReader(tc.input)); got != nil || err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("Parse(%s) = %+v, %v; want error containing %q", tc.input, got, err, tc.want)
		}
	}
}

func TestStackFrameSourceIDCompatibility(t *testing.T) {
	for _, tc := range []struct {
		input, want string
		wantErr     bool
	}{
		{`{"sourceID":false,"SOURCEID":-12.30e+45}`, "-12.30e+45", false},
		{`{"sourceID":123,"sourceID":null}`, "previous", false},
		{`{"sourceID":"valid","sourceID":[]}`, "previous", true},
		{`{"sourceID":""}`, "", false},
		{`{"sourceID":"source-\uD800"}`, "source-\uFFFD", false},
		{"{\"sourceID\":\"\xff\"}", "\uFFFD", false},
	} {
		frame := StackFrame{SourceID: "previous"}
		err := json.Unmarshal([]byte(tc.input), &frame)
		if frame.SourceID != tc.want || (err != nil) != tc.wantErr {
			t.Errorf("decode %q: got sourceID %q, error %v; want %q, error %t", tc.input, frame.SourceID, err, tc.want, tc.wantErr)
		}
	}
}

func benchmarkRecordingPath(b *testing.B) string {
	b.Helper()
	path := os.Getenv("WKPERF_BENCH_RECORDING")
	if path == "" {
		b.Skip("set WKPERF_BENCH_RECORDING to an absolute path to a WebKit recording")
	}
	return path
}

func BenchmarkParseRecording(b *testing.B) {
	path := benchmarkRecordingPath(b)
	data, err := os.ReadFile(path)
	if err != nil {
		b.Fatal(err)
	}
	b.SetBytes(int64(len(data)))
	b.ReportAllocs()
	for b.Loop() {
		if _, err := Parse(bytes.NewReader(data)); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkLoadRecording(b *testing.B) {
	path := benchmarkRecordingPath(b)
	info, err := os.Stat(path)
	if err != nil {
		b.Fatal(err)
	}
	b.SetBytes(info.Size())
	b.ReportAllocs()
	for b.Loop() {
		if _, err := Load(path); err != nil {
			b.Fatal(err)
		}
	}
}

func TestLoadMatchesParse(t *testing.T) {
	const flat = `{"records":[],"startTime":13.25,"endTime":18.5,"samples":[{"stackTraces":[{"timestamp":14,"stackFrames":[{"sourceID":9007199254740993,"line":1,"column":81}]}],"durations":[0.005]}]}`
	path := filepath.Join(t.TempDir(), "recording.json")
	for _, input := range []string{
		flat, " \r\n" + `{"recording":` + flat + `,"overview":null}` + "\t ",
		`{"sampleStackTraces":[{"stackFrames":[{"name":"leaf"}]}],"sampleDurations":[0.002]}`,
		"", " \t\r\n", "{", `{"records":[{"type":"unfinished`,
		`{"x":"\`, `{"x":"\u12`, `{"x":true,`, `{"x":-`, `{"x":1e`,
		`{"records":[}]}`, `{"x":1e }`, `false`, `null`,
		`{"records":[]}null`, `{"records":"bad"} ?`, `{"recording":null}{`,
		`{"recording":null}`, `{"recording":{"records":"bad"}}`,
	} {
		if err := os.WriteFile(path, []byte(input), 0600); err != nil {
			t.Fatal(err)
		}
		want, wantErr := Parse(strings.NewReader(input))
		got, gotErr := Load(path)
		if !reflect.DeepEqual(got, want) {
			t.Errorf("Load(%q) = %+v; Parse = %+v", input, got, want)
		}
		if wantErr == nil {
			if gotErr != nil {
				t.Errorf("Load(%q): unexpected error %v", input, gotErr)
			}
			continue
		}
		var typed *diagnostic.Error
		if !errors.As(gotErr, &typed) || typed.Code != diagnostic.LoadError || typed.File != path ||
			typed.Message() != wantErr.Error() {
			t.Errorf("Load(%q): %v; want a typed load error for %q", input, gotErr, wantErr)
		}
		for _, target := range []error{io.EOF, io.ErrUnexpectedEOF} {
			if errors.Is(gotErr, target) != errors.Is(wantErr, target) {
				t.Errorf("Load(%q): %v does not match Parse's %v wrapping", input, gotErr, target)
			}
		}
	}
}

func TestLoadRejectsSparseRecordingWithMalformedPrefix(t *testing.T) {
	path := filepath.Join(t.TempDir(), "sparse.json")
	if err := os.WriteFile(path, []byte(`!`), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Truncate(path, maxBufferedRecordingSize+1); err != nil {
		t.Fatal(err)
	}
	var syntax *json.SyntaxError
	if rec, err := Load(path); rec != nil || !errors.As(err, &syntax) || syntax.Offset != 1 {
		t.Fatalf("Load sparse malformed recording = %+v, %v; want syntax error at byte 1", rec, err)
	}
}

func TestLoadRecordingFileReadsPastReportedSize(t *testing.T) {
	path := filepath.Join(t.TempDir(), "recording.json")
	if err := os.WriteFile(path, []byte(`{"records":[]}`), 0600); err != nil {
		t.Fatal(err)
	}
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if rec, err := loadRecordingFile(f, 4); err != nil || rec.Records == nil {
		t.Fatalf("load with stale size = %+v, %v; want recording with empty records", rec, err)
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
