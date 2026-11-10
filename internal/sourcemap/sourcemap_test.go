package sourcemap

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/omniviewdev/wkperf/internal/model"
	"github.com/omniviewdev/wkperf/internal/testgen"
)

const tinyMap = `{"version":3,"file":"bundle.js","sourceRoot":"../src","sources":["view.ts"],"names":["render","flush"],"mappings":"AAAAA,UACIC"}`

func TestV3VLQNamesAndOneBasedLocations(t *testing.T) {
	m, err := Parse(strings.NewReader(tinyMap))
	if err != nil {
		t.Fatal(err)
	}
	first, ok := m.Lookup(1, 1)
	if !ok || first.Name != "render" || first.Line != 1 || first.Column != 1 || first.URL != "../src/view.ts" {
		t.Fatalf("first mapping: %+v %v", first, ok)
	}
	second, ok := m.Lookup(1, 15)
	if !ok || second.Name != "flush" || second.Line != 2 || second.Column != 5 {
		t.Fatalf("VLQ/name deltas: %+v %v", second, ok)
	}
	if _, ok := m.Lookup(2, 1); ok {
		t.Fatal("mapping leaked between generated lines")
	}
	for _, bad := range []string{`{"version":2}`, `{"version":3,"sources":["x"],"mappings":"!"}`, `{"version":3,"sources":["x"],"mappings":"g"}`} {
		if _, err := Parse(strings.NewReader(bad)); err == nil {
			t.Fatalf("accepted invalid map %s", bad)
		}
	}
}

func TestResolveFileDirectoryAndAdjacent(t *testing.T) {
	dir := t.TempDir()
	bundle := filepath.Join(dir, "bundle.js")
	mapPath := bundle + ".map"
	if err := os.WriteFile(bundle, []byte("function a(){}"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(mapPath, []byte(tinyMap), 0600); err != nil {
		t.Fatal(err)
	}
	for _, paths := range [][]string{{mapPath}, {dir}, nil} {
		rec := &model.Recording{Samples: []model.TargetSamples{{StackTraces: []model.StackTrace{{StackFrames: []model.StackFrame{{Name: "a", URL: bundle, Line: 1, Column: 11}}}}}}}
		d, err := Resolve(rec, paths, filepath.Join(dir, "capture.json"))
		if err != nil || d.MappedFrames != 1 {
			t.Fatalf("resolve %v: %+v %v", paths, d, err)
		}
		f := rec.Samples[0].StackTraces[0].StackFrames[0]
		if f.Name != "a" || f.Column != 11 || f.Original == nil || f.Original.Name != "flush" || f.Original.Line != 2 {
			t.Fatalf("generated/original lost: %+v", f)
		}
	}
	rec := &model.Recording{Samples: []model.TargetSamples{{StackTraces: []model.StackTrace{{StackFrames: []model.StackFrame{{Name: "a", URL: "https://app/bundle.js", Line: 1, Column: 11}}}}}}}
	d, err := Resolve(rec, []string{dir}, "")
	if err != nil || d.MappedFrames != 1 {
		t.Fatalf("URL basename matching: %+v %v", d, err)
	}
}

func TestMissingMapsAndMinificationHint(t *testing.T) {
	frames := []model.StackFrame{{Name: "a", URL: "https://app/assets/bundle.js", Line: 1, Column: 123}, {Name: "b", URL: "https://app/assets/bundle.js", Line: 1, Column: 456}}
	rec := &model.Recording{Samples: []model.TargetSamples{{StackTraces: []model.StackTrace{{StackFrames: frames}}}}}
	d, err := Resolve(rec, nil, "")
	if err != nil || !d.Minified || len(d.MissingMaps) != 1 || d.UnmappedFrames != 2 || len(d.Warnings) == 0 {
		t.Fatalf("missing-map diagnostics: %+v %v", d, err)
	}
}

func TestMinificationHintIndependentOfSampleOrder(t *testing.T) {
	frames := []model.StackFrame{{Name: "a", URL: "bundle.js", Line: 1, Column: 120}, {Name: "b", URL: "bundle.js", Line: 1, Column: 2}}
	rec := &model.Recording{Samples: []model.TargetSamples{{StackTraces: []model.StackTrace{{StackFrames: frames}}}}}
	if !DetectMinification(rec) {
		t.Fatal("high-column frame before low-column frame lost minification hint")
	}
	frames[0], frames[1] = frames[1], frames[0]
	if !DetectMinification(rec) {
		t.Fatal("minification depends on sample order")
	}
}

func TestUnmappedSegmentsAndIndexedSections(t *testing.T) {
	m, err := Parse(strings.NewReader(`{"version":3,"sources":["a.ts"],"mappings":"AAAA,U"}`))
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := m.Lookup(1, 11); ok {
		t.Fatal("unmapped segment inherited prior mapping")
	}
	m, err = Parse(strings.NewReader(`{"version":3,"sections":[{"offset":{"line":1,"column":5},"map":` + tinyMap + `}]}`))
	if err != nil {
		t.Fatal(err)
	}
	got, ok := m.Lookup(2, 6)
	if !ok || got.Name != "render" || got.Line != 1 || got.Column != 1 {
		t.Fatalf("indexed offset: %+v %v", got, ok)
	}
}

func TestResolveRejectsAmbiguousBasename(t *testing.T) {
	dir := t.TempDir()
	for _, part := range []string{"one", "two"} {
		if err := os.MkdirAll(filepath.Join(dir, part), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, part, "bundle.js.map"), []byte(tinyMap), 0600); err != nil {
			t.Fatal(err)
		}
	}
	rec := &model.Recording{Samples: []model.TargetSamples{{StackTraces: []model.StackTrace{{StackFrames: []model.StackFrame{{Name: "a", URL: "https://app/bundle.js", Line: 1, Column: 11}}}}}}}
	d, err := Resolve(rec, []string{dir}, "")
	if err != nil || d.MappedFrames != 0 || len(d.Warnings) == 0 {
		t.Fatalf("ambiguous map guessed: %+v %v", d, err)
	}
}

func TestResolveRecordingAdjacentRemoteMapWithoutBundle(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "bundle.js.map"), []byte(testgen.MinifiedSourceMap()), 0600); err != nil {
		t.Fatal(err)
	}
	rec := testgen.IdleMinified()
	d, err := Resolve(rec, nil, filepath.Join(dir, "capture.json"))
	if err != nil || d.MappedFrames == 0 || d.UnmappedFrames != 0 {
		t.Fatalf("recording-adjacent remote-bundle map not found: %+v %v", d, err)
	}
	frame := rec.Samples[0].StackTraces[0].StackFrames[0]
	if frame.Name != "a" || frame.Original == nil || frame.Original.Name != "flush" {
		t.Fatalf("generated/original identity lost: %+v", frame)
	}
}

func TestResolveBundleDirectoriesDisambiguateBasename(t *testing.T) {
	dir := t.TempDir()
	rec := testgen.Empty()
	target := model.TargetSamples{}
	for _, owner := range []string{"app", "plugin"} {
		mapDir := filepath.Join(dir, owner)
		if err := os.MkdirAll(mapDir, 0700); err != nil {
			t.Fatal(err)
		}
		data := strings.ReplaceAll(testgen.MinifiedSourceMap(), `"render"`, `"`+owner+`Render"`)
		if err := os.WriteFile(filepath.Join(mapDir, "bundle.js.map"), []byte(data), 0600); err != nil {
			t.Fatal(err)
		}
		target.StackTraces = append(target.StackTraces, model.StackTrace{Timestamp: 1, StackFrames: []model.StackFrame{{Name: "a", URL: "https://app.test/" + owner + "/bundle.js", Line: 1, Column: 1}}})
		target.Durations = append(target.Durations, .001)
	}
	rec.Samples = []model.TargetSamples{target}
	d, err := Resolve(rec, []string{dir}, "")
	if err != nil || d.MappedFrames != 2 || len(d.Warnings) != 0 {
		t.Fatalf("directory-specific bundles remain ambiguous: %+v %v", d, err)
	}
	for i, owner := range []string{"app", "plugin"} {
		frame := rec.Samples[0].StackTraces[i].StackFrames[0]
		if frame.Original == nil || frame.Original.Name != owner+"Render" {
			t.Fatalf("wrong bundle map selected for %s: %+v", owner, frame)
		}
	}
}
