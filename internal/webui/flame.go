package webui

import (
	"cmp"
	"math"
	"slices"
	"strconv"
	"unicode/utf8"

	"github.com/omniviewdev/wkperf/internal/flame"
)

const (
	maxFlameContexts = 320
	maxFlameLevels   = 24
	minFlameShare    = 0.001 // 0.1% of the recording's sampled weight.
)

type flameGraph struct {
	Frames  []flameFrame
	Levels  int
	Clipped bool
}

type flameFrame struct {
	Index, Parent, Depth int
	Offset, Share        float64 // percentages of the full sample weight
	Node                 *flame.Node
}

// layoutFlame keeps caller prefixes, orders siblings by inclusive weight, and
// never stretches the retained frames to fill space left by omitted nodes.
func layoutFlame(tree *flame.Tree) flameGraph {
	if tree == nil || tree.Root == nil {
		return flameGraph{}
	}
	root := tree.Root
	if root.TotalMs <= 0 || math.IsInf(root.TotalMs, 0) || math.IsNaN(root.TotalMs) {
		return flameGraph{}
	}
	var graph flameGraph
	var siblings []*flame.Node
	for i := -1; i < len(graph.Frames); i++ {
		node, parent, depth, offset := root, 0, 0, 0.0
		if i >= 0 {
			caller := graph.Frames[i]
			node, parent, depth, offset = caller.Node, caller.Index, caller.Depth+1, caller.Offset
		}
		if len(node.Children) == 0 {
			continue
		}
		if depth == maxFlameLevels || len(graph.Frames) == maxFlameContexts {
			graph.Clipped = true
			continue
		}
		siblings = siblings[:0]
		var largest *flame.Node
		for _, child := range node.Children {
			if child.TotalMs/root.TotalMs >= minFlameShare {
				siblings = append(siblings, child)
			}
			if i < 0 && (largest == nil || child.TotalMs > largest.TotalMs) {
				largest = child
			}
		}
		if i < 0 && len(siblings) == 0 {
			// A very wide forest can fall entirely below the display threshold.
			graph.Clipped = true
			siblings = append(siblings, largest)
		}
		slices.SortStableFunc(siblings, func(a, b *flame.Node) int {
			return cmp.Compare(b.TotalMs, a.TotalMs)
		})
		n := min(len(siblings), maxFlameContexts-len(graph.Frames))
		graph.Clipped = graph.Clipped || n != len(node.Children)
		for _, child := range siblings[:n] {
			share := 100 * (child.TotalMs / root.TotalMs)
			graph.Frames = append(graph.Frames, flameFrame{len(graph.Frames) + 1, parent, depth, offset, share, child})
			offset += share
			graph.Levels = depth + 1
		}
	}
	return graph
}

func frameName(frame flame.Frame) string {
	if frame.Original != nil && frame.Original.Name != "" {
		return boundedText(frame.Original.Name, 160)
	}
	return generatedName(frame)
}

func generatedName(frame flame.Frame) string {
	if frame.Name == "" {
		return "(anonymous)"
	}
	return boundedText(frame.Name, 160)
}

func generatedLocation(frame flame.Frame) string {
	if frame.URL != "" {
		return sourceLocation(frame.URL, frame.Line, frame.Column)
	}
	if frame.SourceID != "" {
		return sourceLocation("source ID "+boundedText(frame.SourceID, 120), frame.Line, frame.Column)
	}
	return "No source location recorded"
}

func originalLocation(original *flame.Location) string {
	if original == nil || original.URL == "" {
		return ""
	}
	return sourceLocation(original.URL, original.Line, original.Column)
}

func sourceLocation(url string, line, column int) string {
	location := boundedText(url, 512)
	if line > 0 {
		location += ":" + strconv.Itoa(line)
		if column > 0 {
			location += ":" + strconv.Itoa(column)
		}
	}
	return location
}

func boundedText(text string, limit int) string {
	if len(text) <= limit {
		return text
	}
	for limit > 0 && !utf8.RuneStart(text[limit]) {
		limit--
	}
	return text[:limit] + "…"
}

func categoryLabel(category string) string {
	switch flame.Category(category) {
	case flame.CategoryApp:
		return "Application"
	case flame.CategoryFramework:
		return "Framework / library"
	case flame.CategoryNative:
		return "Native"
	case flame.CategoryGC:
		return "Garbage collection"
	case flame.CategoryInjected:
		return "Injected script"
	case flame.CategoryUnknownBundled:
		return "Unresolved bundle"
	default:
		return "Unclassified"
	}
}
