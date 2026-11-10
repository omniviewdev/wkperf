package insights

import (
	"fmt"
	"sort"
	"strings"

	"github.com/omniviewdev/wkperf/internal/flame"
	"github.com/omniviewdev/wkperf/internal/metrics"
	"github.com/omniviewdev/wkperf/internal/rootcause"
)

// StackPreview is a bounded display of one exact catalog stack. ID and Command
// always address the complete stack, including frames omitted from the display.
type StackPreview struct {
	ID, Label, LeafLabel, Command string
	Depth, Elided                 int
	Root, Leaf                    []flame.Frame
}

// PreviewFrames retains both ends of a path without changing its exact evidence.
func PreviewFrames(frames []flame.Frame, brief bool) (root, leaf []flame.Frame, elided int) {
	roots, leaves := 6, 16
	if brief {
		roots, leaves = 3, 9
	}
	if len(frames) <= roots+leaves {
		return frames, nil, 0
	}
	return frames[:roots], frames[len(frames)-leaves:], len(frames) - roots - leaves
}

// MetricStackID returns the canonical path selector for location-only frames.
func MetricStackID(frames []metrics.FunctionFrame) string {
	path := make([]flame.Frame, len(frames))
	for i, frame := range frames {
		path[i] = flame.Frame{Name: frame.Name, URL: frame.URL, Line: frame.Line, Column: frame.Column, SourceID: frame.SourceID}
	}
	return flame.StackID(path)
}

// StackPreviews assigns deterministic report-local S labels to catalog stacks.
// Callers select evidence before constructing their display so every format
// refers to the same complete paths.
func StackPreviews(r *Report, brief bool) []StackPreview {
	catalog := Catalog(r)
	ids := make([]string, 0, len(catalog.Stacks))
	for id := range catalog.Stacks {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	previews := make([]StackPreview, 0, len(ids))
	for i, id := range ids {
		stack := catalog.Stacks[id]
		roots, leaves := 6, 16
		if brief {
			roots, leaves = 3, 9
		}
		elided := max(0, len(stack.Frames)-roots-leaves)
		if elided == 0 {
			roots, leaves = len(stack.Frames), 0
		}
		display := make([]flame.Frame, roots+leaves)
		for j := range roots {
			display[j] = catalog.Frames[stack.Frames[j]]
		}
		for j := range leaves {
			display[roots+j] = catalog.Frames[stack.Frames[len(stack.Frames)-leaves+j]]
		}
		preview := StackPreview{ID: id, Label: fmt.Sprintf("S%d", i+1), Depth: stack.Depth, Root: display[:roots], Leaf: display[roots:], Elided: elided, Command: "wkperf query " + shellQuote(r.Source) + " stacks " + shellQuote(id) + " --format json" + queryFlags(r)}
		if len(stack.Frames) > 0 {
			preview.LeafLabel = frameLabel(catalog.Frames[stack.Frames[len(stack.Frames)-1]])
		}
		previews = append(previews, preview)
	}
	return previews
}

func (p StackPreview) Reference() string {
	return fmt.Sprintf("stack %s (%d frames; leaf %s)", p.Label, p.Depth, p.LeafLabel)
}

// FacetLabels shares concise measured facets across text, HTML and AI output.
func FacetLabels(reads []rootcause.NativeRead, depth *rootcause.DepthFacets, related []string) []string {
	var labels []string
	for _, read := range reads {
		labels = append(labels, fmt.Sprintf("%s: %s flush hypothesis; %d/%d timed samples inside recalculate-styles (%.6gms covering records), %d/%d inside layout (%.6gms covering records)", read.API, read.Flush, read.StyleSamples, read.Samples, read.StyleMs, read.LayoutSamples, read.Samples, read.LayoutMs))
	}
	if depth != nil {
		label := fmt.Sprintf("Depth facets: max depth %d; commit frames %d; React fiber levels ~%d", depth.MaxDepth, depth.CommitFrames, depth.ReactFiberLevels)
		if depth.RepeatedFrame != nil {
			label += fmt.Sprintf("; repeated %s ×%d", frameLabel(depth.RepeatedFrame.Frame), depth.RepeatedFrame.Count)
		}
		if depth.Recursion != nil {
			label += fmt.Sprintf("; possible non-framework recursion: %s ×%d", frameLabel(depth.Recursion.Frame), depth.Recursion.Count)
		}
		labels = append(labels, label)
	}
	if len(related) > 0 {
		labels = append(labels, "Related findings/facets (shared evidence, not independent impact): "+strings.Join(related, ", "))
	}
	return labels
}
