package flame

import (
	"math"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"github.com/omniviewdev/wkperf/internal/model"
	"github.com/omniviewdev/wkperf/internal/testgen"
)

func TestFoldedIntegerMicrosecondsAndLiteralLabels(t *testing.T) {
	labels := []string{"bound work\\path", "semi;colon", "percent%3B", "control\x00\t\n\r\x7f", ""}
	var stacks [][]model.StackFrame
	for _, name := range labels {
		stacks = append(stacks, []model.StackFrame{{Name: name}})
	}
	// Zero durations disappear. A missing duration keeps its 1 ms default.
	tree := Build(recording(stacks, []float64{.00123456789, .0020004, .0000002, 0}))
	folded := tree.Folded()
	pattern := regexp.MustCompile(`^(.*) ([0-9]+)$`)
	var total int64
	lines := strings.Split(strings.TrimSuffix(folded, "\n"), "\n")
	for _, line := range lines {
		match := pattern.FindStringSubmatch(line)
		if match == nil {
			t.Fatalf("standard collapsed-stack importer rejects %q", line)
		}
		count, err := strconv.ParseInt(match[2], 10, 64)
		if err != nil {
			t.Fatal(err)
		}
		total += count
	}
	if math.Abs(float64(total)-math.Round(tree.Root.TotalMs*1000)) > float64(len(lines)) {
		t.Fatalf("microsecond rounding lost weight: %d vs %g ms", total, tree.Root.TotalMs)
	}
	for _, literal := range []string{`bound work\path@native`, "semi%3Bcolon@native", "percent%253B@native", "(anonymous)@native"} {
		if !strings.Contains(folded, literal) {
			t.Errorf("folded label lost %q: %q", literal, folded)
		}
	}
	controls := Build(recording([][]model.StackFrame{{{Name: labels[3]}}}, []float64{.001})).Folded()
	if !strings.Contains(controls, "control%00%09%0A%0D%7F@native") {
		t.Fatalf("control characters break folded syntax: %q", controls)
	}
}

func TestFoldedDiffIntegerMicroseconds(t *testing.T) {
	tree := Build(testgen.FlameTiny())
	pattern := regexp.MustCompile(`^(.*) ([0-9]+) ([0-9]+)$`)
	for _, line := range strings.Split(strings.TrimSuffix(FoldedDiff(tree, tree), "\n"), "\n") {
		match := pattern.FindStringSubmatch(line)
		if match == nil {
			t.Fatalf("diff importer rejects %q", line)
		}
		if match[2] != match[3] {
			t.Fatalf("equal inputs differ: %q", line)
		}
	}
	if !strings.Contains(FoldedDiff(nil, tree), " 0 30000\n") {
		t.Fatal("diff weights are not integer microseconds")
	}
}
