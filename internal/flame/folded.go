package flame

import (
	"math"
	"slices"
	"strconv"
	"strings"
)

// FoldedLabel preserves spaces and backslashes, which collapsed-stack importers
// treat literally. Percent encoding protects separators and ASCII controls;
// encoding percent itself prevents distinct labels from colliding.
func FoldedLabel(label string) string {
	const hex = "0123456789ABCDEF"
	var out strings.Builder
	start := 0
	for i := range len(label) {
		b := label[i]
		if b != '%' && b != ';' && b >= 0x20 && b != 0x7f {
			continue
		}
		out.WriteString(label[start:i])
		out.WriteByte('%')
		out.WriteByte(hex[b>>4])
		out.WriteByte(hex[b&15])
		start = i + 1
	}
	if start == 0 {
		return label
	}
	out.WriteString(label[start:])
	return out.String()
}

func foldedPath(frames []Frame) string {
	var out strings.Builder
	for i, frame := range frames {
		if i != 0 {
			out.WriteByte(';')
		}
		out.WriteString(FoldedLabel(FrameLabel(frame)))
	}
	return out.String()
}

func weightString(value float64) string {
	return strconv.FormatFloat(math.Round(value*1000), 'f', 0, 64)
}

type foldedWeight struct {
	weight sum
}

func (tree *Tree) foldedWeights() map[string]*foldedWeight {
	out := make(map[string]*foldedWeight)
	for _, path := range tree.Paths() {
		label := foldedPath(path.Frames)
		entry := out[label]
		if entry == nil {
			entry = &foldedWeight{}
			out[label] = entry
		}
		entry.weight.add(path.TotalMs)
	}
	return out
}

// Folded emits location-qualified labels and integer microsecond weights.
// Rounding occurs once per emitted stack, after aggregating exact sample weights.
func (tree *Tree) Folded() string {
	weights := tree.foldedWeights()
	labels := make([]string, 0, len(weights))
	for label := range weights {
		labels = append(labels, label)
	}
	slices.Sort(labels)
	var out strings.Builder
	for _, label := range labels {
		out.WriteString(label)
		out.WriteByte(' ')
		out.WriteString(weightString(weights[label].weight.total()))
		out.WriteByte('\n')
	}
	return out.String()
}

// FoldedDiff emits sorted stacks with raw before and after integer microseconds.
func FoldedDiff(before, after *Tree) string {
	type pair struct {
		frames        []Frame
		before, after float64
	}
	paths := make(map[string]pair)
	for _, path := range before.Paths() {
		paths[path.ID] = pair{frames: path.Frames, before: path.TotalMs}
	}
	for _, path := range after.Paths() {
		entry := paths[path.ID]
		if entry.frames == nil {
			entry.frames = path.Frames
		} else {
			frames := make([]Frame, len(entry.frames))
			for i := range frames {
				frames[i] = chooseFrame(entry.frames[i], path.Frames[i])
			}
			entry.frames = frames
		}
		entry.after = path.TotalMs
		paths[path.ID] = entry
	}
	type weights struct{ before, after sum }
	labels := make(map[string]*weights)
	ids := make([]string, 0, len(paths))
	for id := range paths {
		ids = append(ids, id)
	}
	slices.Sort(ids)
	for _, id := range ids {
		path := paths[id]
		label := foldedPath(path.frames)
		entry := labels[label]
		if entry == nil {
			entry = &weights{}
			labels[label] = entry
		}
		entry.before.add(path.before)
		entry.after.add(path.after)
	}
	ordered := make([]string, 0, len(labels))
	for label := range labels {
		ordered = append(ordered, label)
	}
	slices.Sort(ordered)
	var out strings.Builder
	for _, label := range ordered {
		out.WriteString(label)
		out.WriteByte(' ')
		out.WriteString(weightString(labels[label].before.total()))
		out.WriteByte(' ')
		out.WriteString(weightString(labels[label].after.total()))
		out.WriteByte('\n')
	}
	return out.String()
}

func chooseFrame(a, b Frame) Frame {
	if a.Original == nil && b.Original != nil {
		return b
	}
	if b.Original == nil && a.Original != nil {
		return a
	}
	if compareFrame(a, b) <= 0 {
		return a
	}
	return b
}
