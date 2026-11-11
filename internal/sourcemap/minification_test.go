package sourcemap

import (
	"fmt"
	"math/rand/v2"
	"strings"
	"testing"

	"github.com/omniviewdev/wkperf/internal/model"
)

func denseMinifiedFrames() []model.StackFrame {
	const bundle = "wails://localhost/_/plugins/example/assets/entry2.js"
	return []model.StackFrame{
		{Name: "ga", URL: bundle, Line: 3, Column: 160},
		{Name: "wE", URL: bundle, Line: 4, Column: 1120},
		{Name: "k3", URL: bundle, Line: 5, Column: 2440},
		{Name: "SE", URL: bundle, Line: 6, Column: 3600},
		{Name: "FE", URL: bundle, Line: 8, Column: 5200},
	}
}

func minificationRecording(frames []model.StackFrame) *model.Recording {
	target := model.TargetSamples{}
	for i, frame := range frames {
		target.StackTraces = append(target.StackTraces, model.StackTrace{
			Timestamp: float64(i), StackFrames: []model.StackFrame{frame},
		})
	}
	return &model.Recording{Samples: []model.TargetSamples{target}}
}

func TestMinificationHintDenseLowGeneratedLines(t *testing.T) {
	frames := denseMinifiedFrames()
	for _, bundle := range []string{frames[0].URL, "https://app.test/assets/chunk.js", "file:///dist/renamed.mjs"} {
		for i := range frames {
			frames[i].URL = bundle
		}
		rec := minificationRecording(frames)
		if !DetectMinification(rec) {
			t.Fatalf("dense two-character names on generated lines 3–8 lost hint for %s", bundle)
		}
	}
}

func TestMinificationHintThreeCharacterIdentifiers(t *testing.T) {
	frames := denseMinifiedFrames()
	for i, name := range []string{"gA1", "wE2", "k_3", "S$4", "FE5"} {
		frames[i].Name = name
	}
	if !DetectMinification(minificationRecording(frames)) {
		t.Fatal("dense three-character identifier distribution lost hint")
	}
}

func TestMinificationHintConservativeEvidence(t *testing.T) {
	const bundle = "https://app.test/assets/bundle.js"
	cases := []struct {
		name   string
		frames []model.StackFrame
	}{
		{"single short function", []model.StackFrame{{Name: "ga", URL: bundle, Line: 1, Column: 5200}}},
		{"readable source helpers", []model.StackFrame{
			{Name: "add", URL: "https://app.test/src/math.ts", Line: 3, Column: 8},
			{Name: "sub", URL: "https://app.test/src/math.ts", Line: 4, Column: 8},
			{Name: "mul", URL: "https://app.test/src/math.ts", Line: 5, Column: 8},
			{Name: "div", URL: "https://app.test/src/math.ts", Line: 6, Column: 8},
		}},
		{"short readable JavaScript helpers", []model.StackFrame{
			{Name: "get", URL: "https://app.test/src/store.js", Line: 1, Column: 10},
			{Name: "set", URL: "https://app.test/src/store.js", Line: 2, Column: 10},
			{Name: "has", URL: "https://app.test/src/store.js", Line: 3, Column: 10},
			{Name: "del", URL: "https://app.test/src/store.js", Line: 4, Column: 10},
		}},
		{"readable names on wide lines", []model.StackFrame{
			{Name: "renderRows", URL: bundle, Line: 3, Column: 1600},
			{Name: "commitRows", URL: bundle, Line: 4, Column: 2600},
			{Name: "measureRows", URL: bundle, Line: 5, Column: 3600},
			{Name: "updateStore", URL: bundle, Line: 6, Column: 4600},
		}},
		{"short names on high source lines", []model.StackFrame{
			{Name: "ga", URL: bundle, Line: 31, Column: 1600},
			{Name: "wE", URL: bundle, Line: 42, Column: 2600},
			{Name: "k3", URL: bundle, Line: 53, Column: 3600},
			{Name: "SE", URL: bundle, Line: 64, Column: 4600},
		}},
		{"same name at distinct positions", []model.StackFrame{
			{Name: "ga", URL: bundle, Line: 1, Column: 120},
			{Name: "ga", URL: bundle, Line: 1, Column: 1120},
			{Name: "ga", URL: bundle, Line: 1, Column: 2120},
			{Name: "ga", URL: bundle, Line: 1, Column: 3120},
		}},
		{"same position with distinct names", []model.StackFrame{
			{Name: "ga", URL: bundle, Line: 1, Column: 1120},
			{Name: "wE", URL: bundle, Line: 1, Column: 1120},
			{Name: "k3", URL: bundle, Line: 1, Column: 1120},
			{Name: "SE", URL: bundle, Line: 1, Column: 1120},
		}},
		{"distinct unrelated files", []model.StackFrame{
			{Name: "ga", URL: "https://app.test/one.js", Line: 1, Column: 1600},
			{Name: "wE", URL: "https://app.test/two.js", Line: 1, Column: 2600},
			{Name: "k3", URL: "https://app.test/three.js", Line: 1, Column: 3600},
			{Name: "SE", URL: "https://app.test/four.js", Line: 1, Column: 4600},
		}},
		{"short names lack concentration", []model.StackFrame{
			{Name: "ga", URL: bundle, Line: 1, Column: 120},
			{Name: "wE", URL: bundle, Line: 1, Column: 1120},
			{Name: "renderRows", URL: bundle, Line: 1, Column: 2120},
			{Name: "commitRows", URL: bundle, Line: 1, Column: 3120},
			{Name: "measureRows", URL: bundle, Line: 1, Column: 4120},
		}},
		{"invalid identifier labels", []model.StackFrame{
			{Name: "1a", URL: bundle, Line: 1, Column: 120},
			{Name: "a-b", URL: bundle, Line: 1, Column: 1120},
		}},
		{"unnamed frames", []model.StackFrame{
			{URL: bundle, Line: 1, Column: 1120},
			{Name: "(anonymous)", URL: bundle, Line: 1, Column: 2120},
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if DetectMinification(minificationRecording(tc.frames)) {
				t.Fatal("weak or readable evidence became a minification hint")
			}
		})
	}
}

func TestMinificationHintExcludesNativeAndInjectedFrames(t *testing.T) {
	for _, sourceID := range []string{"native", "4294967295"} {
		frames := denseMinifiedFrames()
		for i := range frames {
			frames[i].SourceID = sourceID
			frames[i].Line = 1
		}
		if DetectMinification(minificationRecording(frames)) {
			t.Fatalf("native source ID %q supplied bundle evidence", sourceID)
		}
	}
	frames := denseMinifiedFrames()
	for i := range frames {
		frames[i].URL = "user-script:2"
		frames[i].Line = 1
	}
	if DetectMinification(minificationRecording(frames)) {
		t.Fatal("injected frames supplied bundle evidence")
	}
}

func TestMinificationHintDeduplicatesSamples(t *testing.T) {
	frame := denseMinifiedFrames()[0]
	frame.Line, frame.Column = 1, 5200
	frames := make([]model.StackFrame, 100)
	for i := range frames {
		frames[i] = frame
		frames[i].SourceID = fmt.Sprint(i)
	}
	rec := minificationRecording(frames)
	rec.Samples = append(rec.Samples, rec.Samples[0])
	if DetectMinification(rec) {
		t.Fatal("repeated samples or source IDs fabricated a distinct-function distribution")
	}
	frames = []model.StackFrame{
		{Name: "ga", URL: frame.URL, Line: 1, Column: 120},
		{Name: "wE", URL: frame.URL, Line: 1, Column: 1120},
		{Name: "renderRows", URL: frame.URL, Line: 1, Column: 2120},
		{Name: "commitRows", URL: frame.URL, Line: 1, Column: 3120},
	}
	for range 100 {
		frames = append(frames, frames[0], frames[1])
	}
	if DetectMinification(minificationRecording(frames)) {
		t.Fatal("repeated short-name samples overwhelmed distinct readable functions")
	}
}

func TestMinificationHintFileLocalAndOrderIndependent(t *testing.T) {
	frames := denseMinifiedFrames()
	for i := range 40 {
		frames = append(frames, model.StackFrame{
			Name: fmt.Sprintf("readableLibraryFunction%d", i),
			URL:  "https://app.test/library.js", Line: 100 + i, Column: 4,
		})
	}
	rng := rand.New(rand.NewPCG(17, 23))
	for range 20 {
		rng.Shuffle(len(frames), func(i, j int) { frames[i], frames[j] = frames[j], frames[i] })
		rec := minificationRecording(frames)
		traces := rec.Samples[0].StackTraces
		rec.Samples[0].StackTraces = traces[:len(traces)/2]
		rec.Samples = append(rec.Samples, model.TargetSamples{StackTraces: traces[len(traces)/2:]})
		rng.Shuffle(len(rec.Samples), func(i, j int) { rec.Samples[i], rec.Samples[j] = rec.Samples[j], rec.Samples[i] })
		if !DetectMinification(rec) {
			t.Fatal("unrelated library frames or sample order hid a minified file")
		}
	}
}

func TestResolveDenseMinificationHintPreservesIdentity(t *testing.T) {
	frames := denseMinifiedFrames()
	rec := minificationRecording(frames)
	d, err := Resolve(rec, nil, "")
	if err != nil || !d.Minified || d.UnmappedFrames != len(frames) || len(d.MissingMaps) != 1 {
		t.Fatalf("dense minification diagnostics: %+v %v", d, err)
	}
	if len(d.Warnings) != 1 || !strings.Contains(d.Warnings[0], "Likely minified JavaScript") {
		t.Fatalf("missing-map advice did not receive hint: %v", d.Warnings)
	}
	for i, stack := range rec.Samples[0].StackTraces {
		if got := stack.StackFrames[0]; got != frames[i] {
			t.Fatalf("minification hint changed generated frame identity: %+v", got)
		}
	}
}
