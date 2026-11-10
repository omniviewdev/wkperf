package flame

import (
	"cmp"
	"fmt"
	"slices"

	core "github.com/omniviewdev/wkperf/internal/flame"
)

type focusFunction struct {
	ID               string     `json:"id"`
	Frame            core.Frame `json:"frame"`
	TotalMS          float64    `json:"totalMs"`
	Contexts         []string   `json:"contexts"`
	InvertedContexts []string   `json:"invertedContexts"`
	totalCorrection  float64
}

func makeHTMLChart(chart Chart) htmlChart {
	out := htmlChart{
		Label: chart.Label, Tree: htmlTree{chartView(chart, false)},
		Inverted: htmlTree{chartView(chart, true)}, Unit: deltaUnit(chart.Comparison),
	}
	out.Functions = focusCatalog(out.Tree.Root, out.Inverted.Root)
	return out
}

// Each function owns disjoint outermost contexts in each display direction.
// These linear-size references let the browser merge contexts without embedding
// a separate deep tree for every function or repeating individual samples.
func focusCatalog(root, inverted *viewNode) []focusFunction {
	functions := make(map[string]*focusFunction)
	collect := func(treeRoot *viewNode, inverse bool) {
		active := make(map[string]int)
		var visit func(*viewNode)
		visit = func(node *viewNode) {
			id := node.Frame.ID
			if id != "" {
				function := functions[id]
				if function == nil {
					function = &focusFunction{ID: id, Frame: node.Frame, Contexts: []string{}, InvertedContexts: []string{}}
					functions[id] = function
				}
				if active[id] == 0 {
					if inverse {
						function.InvertedContexts = append(function.InvertedContexts, node.ID)
					} else {
						function.Contexts = append(function.Contexts, node.ID)
						next := node.TotalMS - function.totalCorrection
						total := function.TotalMS + next
						function.totalCorrection = (total - function.TotalMS) - next
						function.TotalMS = total
					}
				}
				active[id]++
			}
			for _, child := range node.Children {
				visit(child)
			}
			if id != "" {
				active[id]--
			}
		}
		visit(treeRoot)
	}
	collect(root, false)
	collect(inverted, true)
	out := make([]focusFunction, 0, len(functions))
	for _, function := range functions {
		out = append(out, *function)
	}
	slices.SortFunc(out, func(a, b focusFunction) int {
		if c := cmp.Compare(a.Frame.Name, b.Frame.Name); c != 0 {
			return c
		}
		if c := cmp.Compare(a.Frame.URL, b.Frame.URL); c != 0 {
			return c
		}
		if c := cmp.Compare(a.Frame.Line, b.Frame.Line); c != 0 {
			return c
		}
		if c := cmp.Compare(a.Frame.Column, b.Frame.Column); c != 0 {
			return c
		}
		return cmp.Compare(a.ID, b.ID)
	})
	return out
}

// DeltaUnit is optional so older producers retain the private HTML data shape.
// Baseline/current/self/total remain raw milliseconds; only delta uses this unit.
func deltaUnit(comparison any) string {
	if value, ok := comparison.(interface{ DeltaUnit() string }); ok {
		if unit := value.DeltaUnit(); unit != "" {
			return unit
		}
	}
	return "ms"
}

func tooltipWithUnit(node, root *viewNode, diff bool, unit string) string {
	text := frameName(node.Frame) + "\nGenerated: " + node.Frame.Name + " · " + location(node.Frame)
	if node.Original != nil {
		text += fmt.Sprintf("\nOriginal: %s · %s:%d:%d", node.Original.Name, node.Original.URL, node.Original.Line, node.Original.Column)
	}
	text += "\nCategory: " + string(node.Category)
	text += "\nSelf: " + milliseconds(node.SelfMS) + " ms (" + percent(node.SelfMS, root.TotalMS) + ")"
	text += "\nTotal: " + milliseconds(node.TotalMS) + " ms (" + percent(node.TotalMS, root.TotalMS) + ")"
	if diff {
		if unit == "" {
			unit = "ms"
		}
		text += fmt.Sprintf("\nBaseline: %s ms\nCurrent: %s ms\nDelta: %+.3f %s", milliseconds(node.BaselineMS), milliseconds(node.CurrentMS), node.DeltaMS, unit)
	}
	return text
}

func shortARIA(node *viewNode) string {
	name := frameName(node.Frame)
	count := 0
	for offset := range name {
		if count == 79 {
			name = name[:offset] + "…"
			break
		}
		count++
	}
	return fmt.Sprintf("%s, total %s ms, self %s ms", name, milliseconds(node.TotalMS), milliseconds(node.SelfMS))
}
