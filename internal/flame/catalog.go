package flame

import "slices"

// FrameCatalog interns generated identities across trees in deterministic frame
// order. Available original metadata enriches an identity without changing its ID.
// Exporters can map frame.ID to a zero-based index without revisiting samples.
func FrameCatalog(trees ...*Tree) []Frame {
	byID := make(map[string]Frame)
	for _, tree := range trees {
		for _, function := range tree.Functions() {
			frame := function.Frame
			frame.ID = function.ID
			if previous, exists := byID[function.ID]; exists {
				frame = chooseFrame(previous, frame)
			}
			byID[function.ID] = frame
		}
	}
	frames := make([]Frame, 0, len(byID))
	for _, frame := range byID {
		frames = append(frames, frame)
	}
	slices.SortFunc(frames, compareFrame)
	return frames
}
