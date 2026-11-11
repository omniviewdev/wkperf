package cmd

import (
	"strings"
	"testing"

	"github.com/omniviewdev/wkperf/internal/testgen"
)

func TestFlameCLIFocusTinyFolded(t *testing.T) {
	path := writeFlameRecording(t, t.TempDir(), "tiny.json", testgen.FlameTiny())
	const a = "A@file:///app/app.js:10:5"
	const b = "B@file:///app/app.js:20:5"
	const c = "C@file:///app/lib.js:5:3"
	for name, lines := range map[string][]string{
		"A": {a + " 5000", a + ";" + a + " 4000", a + ";" + b + " 20000", a + ";" + b + ";" + c + " 1000", a + ";" + c + " 20000"},
		"B": {b + " 20000", b + ";" + c + " 1000", b + ";getAnimations@native:4294967295:-1:-1 30000"},
	} {
		out, err := runFlame(t, path, "--focus", name, "--format", "folded")
		if err != nil {
			t.Fatal(err)
		}
		actual := strings.Split(strings.TrimSuffix(out, "\n"), "\n")
		if len(actual) != len(lines) {
			t.Fatalf("%s focused paths = %d, want %d", name, len(actual), len(lines))
		}
		for _, line := range lines {
			if !strings.Contains(out, line+"\n") {
				t.Errorf("%s focus lost %q: %s", name, line, out)
			}
		}
	}
}
