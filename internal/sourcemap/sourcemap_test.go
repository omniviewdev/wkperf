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

// Synthetic declaration and parameter tokens use WebKit's function-start columns.
const functionMap = `{"version":3,"file":"index-abc.js","sources":["../../src/app.js"],"names":["PodRow","props","item","Table","r"],"mappings":"AAAA,UAAaA,EAAUC,eACGC,8BAEVC,CAAK,wBACFC"}`

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
		rec := &model.Recording{Samples: []model.TargetSamples{{StackTraces: []model.StackTrace{{StackFrames: []model.StackFrame{{Name: "a", URL: bundle, Line: 1, Column: 12}}}}}}}
		d, err := Resolve(rec, paths, filepath.Join(dir, "capture.json"))
		if err != nil || d.MappedFrames != 1 {
			t.Fatalf("resolve %v: %+v %v", paths, d, err)
		}
		f := rec.Samples[0].StackTraces[0].StackFrames[0]
		if f.Name != "a" || f.Column != 12 || f.Original == nil || f.Original.Name != "flush" || f.Original.Line != 2 {
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
	if err := os.MkdirAll(filepath.Join(dir, "src"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "src", "view.ts"), []byte("export function render() {}"), 0600); err != nil {
		t.Fatal(err)
	}
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
		target.StackTraces = append(target.StackTraces, model.StackTrace{Timestamp: 1, StackFrames: []model.StackFrame{{Name: "a", URL: "https://app.test/" + owner + "/bundle.js", Line: 1, Column: 2}}})
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

func TestResolveFunctionDeclarationNames(t *testing.T) {
	dir := t.TempDir()
	mapPath := filepath.Join(dir, "index-abc.js.map")
	if err := os.WriteFile(mapPath, []byte(functionMap), 0600); err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		name, originalName string
		column, line, col  int
	}{
		{"n", "PodRow", 13, 1, 14},
		{"t", "Table", 59, 4, 17},
		{"", "", 28, 2, 27},
		{"(anonymous)", "", 83, 5, 20},
		{"", "", 13, 1, 24},
		{"(anonymous)", "", 59, 4, 22},
		{"n", "PodRow", 13, 1, 14},
	}
	var frames []model.StackFrame
	for _, tc := range cases {
		frames = append(frames, model.StackFrame{Name: tc.name, URL: "wails://wails.localhost/assets/index-abc.js", Line: 1, Column: tc.column})
	}
	rec := &model.Recording{Samples: []model.TargetSamples{{StackTraces: []model.StackTrace{{StackFrames: frames}}}}}
	d, err := Resolve(rec, []string{mapPath}, "")
	if err != nil || d.MappedFrames != len(cases) || d.UnmappedFrames != 0 {
		t.Fatalf("declaration resolution: %+v %v", d, err)
	}
	for i, tc := range cases {
		got := rec.Samples[0].StackTraces[0].StackFrames[i]
		if got.Name != tc.name || got.Line != 1 || got.Column != tc.column {
			t.Errorf("generated identity changed: %+v", got)
		}
		if got.Original == nil || got.Original.Name != tc.originalName || got.Original.Line != tc.line || got.Original.Column != tc.col {
			t.Errorf("%q@1:%d: original %+v, want %q@%d:%d", tc.name, tc.column, got.Original, tc.originalName, tc.line, tc.col)
		}
	}
}

func TestResolveFunctionNameBoundaries(t *testing.T) {
	const boundaryMap = `{"version":3,"file":"bundle.js","sources":["https://app.test/src/view.ts"],"names":["declaration","parameter"],"mappings":"AAAAA,QAAI,CAACC"}`
	cases := []struct {
		name, data string
		column     int
		mapped     bool
		original   int
	}{
		{"first token", boundaryMap, 1, true, 1},
		{"unnamed preceding token", boundaryMap, 10, true, 6},
		{"unmapped current token", `{"version":3,"file":"bundle.js","sources":["https://app.test/src/view.ts"],"names":["declaration"],"mappings":"AAAAA,U"}`, 11, false, 0},
		{"indexed section boundary", `{"version":3,"file":"bundle.js","sections":[{"offset":{"line":0,"column":0},"map":` + boundaryMap + `},{"offset":{"line":0,"column":10},"map":` + boundaryMap + `}]}`, 11, true, 1},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			mapPath := filepath.Join(t.TempDir(), "bundle.js.map")
			if err := os.WriteFile(mapPath, []byte(tc.data), 0600); err != nil {
				t.Fatal(err)
			}
			rec := &model.Recording{Samples: []model.TargetSamples{{StackTraces: []model.StackTrace{{StackFrames: []model.StackFrame{{Name: "a", URL: "https://app.test/bundle.js", Line: 1, Column: tc.column}}}}}}}
			d, err := Resolve(rec, []string{mapPath}, "")
			if err != nil {
				t.Fatal(err)
			}
			got := rec.Samples[0].StackTraces[0].StackFrames[0].Original
			if tc.mapped {
				if got == nil || got.Name != "" || got.Column != tc.original || d.MappedFrames != 1 {
					t.Fatalf("conservative name/location: %+v, %+v", got, d)
				}
			} else if got != nil || d.UnmappedFrames != 1 {
				t.Fatalf("declaration name invented a location: %+v, %+v", got, d)
			}
		})
	}
}

func TestResolveRelocatedSourcePaths(t *testing.T) {
	dir := t.TempDir()
	source := filepath.Join(dir, "src", "app.js")
	buildDir := filepath.Join(dir, "dist", "assets")
	sidecarDir := filepath.Join(dir, "relocated", "exports", "recordings")
	for _, part := range []string{filepath.Dir(source), buildDir, sidecarDir} {
		if err := os.MkdirAll(part, 0700); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(source, []byte("export const PodRow = props => props.items;"), 0600); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name, mapDir, original string
		warnings               int
	}{
		{"in place", buildDir, source, 0},
		{"relocated sidecar", sidecarDir, "src/app.js", 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			mapPath := filepath.Join(tc.mapDir, "index-abc.js.map")
			if err := os.WriteFile(mapPath, []byte(functionMap), 0600); err != nil {
				t.Fatal(err)
			}
			rec := &model.Recording{Samples: []model.TargetSamples{{StackTraces: []model.StackTrace{{StackFrames: []model.StackFrame{
				{Name: "n", URL: "wails://wails.localhost/assets/index-abc.js", Line: 1, Column: 13},
				{Name: "t", URL: "wails://wails.localhost/assets/index-abc.js", Line: 1, Column: 59},
			}}}}}}
			paths := []string{mapPath}
			recordingPath := ""
			if tc.name == "relocated sidecar" {
				paths = nil
				recordingPath = filepath.Join(tc.mapDir, "capture.json")
			}
			d, err := Resolve(rec, paths, recordingPath)
			if err != nil || d.MappedFrames != 2 || len(d.Warnings) != tc.warnings {
				t.Fatalf("source-path diagnostics: %+v %v", d, err)
			}
			for _, frame := range rec.Samples[0].StackTraces[0].StackFrames {
				if frame.Original == nil || frame.Original.URL != tc.original {
					t.Errorf("source path %+v, want %q", frame.Original, tc.original)
				}
			}
			if tc.warnings > 0 && (!strings.Contains(d.Warnings[0], mapPath) || !strings.Contains(d.Warnings[0], "--sourcemap")) {
				t.Fatalf("unresolved warning lacks map path or advice: %v", d.Warnings)
			}
		})
	}
}

func TestResolveUnresolvedSourcePathForms(t *testing.T) {
	for _, tc := range []struct {
		raw, original string
		warnings      int
	}{
		{"../../src/app.js", "src/app.js", 1},
		{"./src/app.js", "src/app.js", 1},
		{"webpack://app/./src/app.js", "src/app.js", 1},
		{"webpack:///../src/app.js", "src/app.js", 1},
		{"https://app.test/src/app.js", "https://app.test/src/app.js", 0},
	} {
		t.Run(tc.raw, func(t *testing.T) {
			mapPath := filepath.Join(t.TempDir(), "index-abc.js.map")
			data := strings.Replace(functionMap, "../../src/app.js", tc.raw, 1)
			if err := os.WriteFile(mapPath, []byte(data), 0600); err != nil {
				t.Fatal(err)
			}
			rec := &model.Recording{Samples: []model.TargetSamples{{StackTraces: []model.StackTrace{{StackFrames: []model.StackFrame{{Name: "n", URL: "https://app.test/index-abc.js", Line: 1, Column: 13}}}}}}}
			d, err := Resolve(rec, []string{mapPath}, "")
			if err != nil || len(d.Warnings) != tc.warnings {
				t.Fatalf("source-path diagnostics: %+v %v", d, err)
			}
			got := rec.Samples[0].StackTraces[0].StackFrames[0].Original
			if got == nil || got.URL != tc.original {
				t.Fatalf("source path %+v, want %q", got, tc.original)
			}
		})
	}
}

func TestResolveDirectorySkipsUnsupportedMaps(t *testing.T) {
	dir := t.TempDir()
	valid := filepath.Join(dir, "bundle.js.map")
	invalid := filepath.Join(dir, "vendor.js.map")
	if err := os.WriteFile(valid, []byte(`{"version":3,"file":"bundle.js","sources":["https://app.test/src/view.ts"],"names":["render"],"mappings":"AAAAA"}`), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(invalid, []byte(`{"version":3,"sections":[{"offset":{"line":0,"column":0},"url":"chunk.js.map"}]}`), 0600); err != nil {
		t.Fatal(err)
	}
	for _, paths := range [][]string{{dir}, {invalid}, {dir, invalid}, {invalid, dir}} {
		rec := &model.Recording{Samples: []model.TargetSamples{{StackTraces: []model.StackTrace{{StackFrames: []model.StackFrame{{Name: "a", URL: "https://app.test/bundle.js", Line: 1, Column: 2}}}}}}}
		d, err := Resolve(rec, paths, "")
		if len(paths) > 1 || paths[0] == invalid {
			if err == nil || !strings.Contains(err.Error(), invalid) || !strings.Contains(err.Error(), "external source-map section URLs") {
				t.Fatalf("explicit unsupported map was not rejected: %v", err)
			}
			continue
		}
		if err != nil || d.MappedFrames != 1 || d.UnmappedFrames != 0 || len(d.Warnings) != 1 {
			t.Fatalf("unrelated map aborted resolution: %+v %v", d, err)
		}
		if !strings.Contains(d.Warnings[0], invalid) || !strings.Contains(d.Warnings[0], "external source-map section URLs") {
			t.Fatalf("unsupported map warning lacks reason: %v", d.Warnings)
		}
		got := rec.Samples[0].StackTraces[0].StackFrames[0].Original
		if got == nil || got.Name != "render" {
			t.Fatalf("valid directory map was not used: %+v", got)
		}
	}
}
