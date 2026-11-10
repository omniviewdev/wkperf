package flame

import "slices"

// HotPathResult retains supported contexts and every competing child at the
// stopping point. Branches are sorted by inclusive weight, then frame identity.
// Nodes exclude the synthetic root. Node weights always remain milliseconds.
type HotPathResult struct {
	Nodes      []*Node `json:"nodes"`
	Branches   []*Node `json:"branches"`
	StopReason string  `json:"stopReason"`
}

// SupportedHotPath keeps the greedy HotPath selection but stops before weak
// sample support or near-tied children. minFraction is relative to the complete
// tree total. minSamples=0 disables support gating; tieRatio=0 disables ties.
// Share is checked before support, and support before near-ties. StopReason is
// no-samples, min-share, sample-support, near-tie, or leaf.
func (tree *Tree) SupportedHotPath(minFraction float64, minSamples int, tieRatio float64) HotPathResult {
	result := HotPathResult{Nodes: make([]*Node, 0), Branches: make([]*Node, 0), StopReason: "no-samples"}
	if tree == nil || tree.Root == nil || tree.Root.TotalMs <= 0 {
		return result
	}
	node := tree.Root
	threshold := max(0, minFraction) * node.TotalMs
	for {
		if len(node.Children) == 0 {
			result.StopReason = "leaf"
			break
		}
		best := node.Children[0]
		var second *Node
		for _, child := range node.Children[1:] {
			if compareHotPathBranch(child, best) < 0 {
				second, best = best, child
			} else if second == nil || compareHotPathBranch(child, second) < 0 {
				second = child
			}
		}
		if !finite(minFraction) || best.TotalMs < threshold {
			result.StopReason = "min-share"
			break
		}
		if node.Samples < minSamples || best.Samples < minSamples {
			result.StopReason = "sample-support"
			break
		}
		if tieRatio > 0 && second != nil && second.TotalMs >= tieRatio*best.TotalMs {
			result.StopReason = "near-tie"
			break
		}
		result.Nodes = append(result.Nodes, best)
		node = best
	}
	if len(node.Children) != 0 {
		result.Branches = slices.Clone(node.Children)
		slices.SortFunc(result.Branches, compareHotPathBranch)
	}
	return result
}

func compareHotPathBranch(a, b *Node) int {
	if a.TotalMs > b.TotalMs {
		return -1
	}
	if a.TotalMs < b.TotalMs {
		return 1
	}
	return compareFrame(a.Frame, b.Frame)
}
