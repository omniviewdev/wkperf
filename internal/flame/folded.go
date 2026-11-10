package flame

import (
	"slices"
	"strconv"
	"strings"
)

var foldedEscapes = strings.NewReplacer("\\", "\\\\", ";", "\\;", " ", "\\ ", "\t", "\\t", "\n", "\\n", "\r", "\\r")

func foldedPath(frames []Frame) string {
	var out strings.Builder
	for i, frame := range frames {
		if i != 0 {
			out.WriteByte(';')
		}
		out.WriteString(foldedEscapes.Replace(FrameLabel(frame)))
	}
	return out.String()
}

func weightString(value float64) string { return strconv.FormatFloat(value, 'f', -1, 64) }

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

// Folded uses escaped, location-qualified labels and millisecond weights.
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

// FoldedDiff emits sorted stacks with before and after millisecond weights.
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
