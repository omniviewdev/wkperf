package flame

import "slices"

// Diff matches generated identities and path IDs, using automatic duration
// normalization. Raw before/after milliseconds always remain available.
func Diff(before, after *Tree) *Comparison {
	result, _ := DiffNormalized(before, after, DiffAuto)
	return result
}

// DiffNormalized compares functions and contexts using one shared normalization
// contract. Only delta fields change units; samples and raw input values do not.
func DiffNormalized(before, after *Tree, mode DiffMode) (*Comparison, error) {
	normalization, err := ResolveDiffNormalization(before, after, mode)
	if err != nil {
		return nil, err
	}
	result := &Comparison{Functions: make([]FunctionDelta, 0), Normalization: normalization,
		BeforeTotalMs: normalization.BeforeTotalMs, AfterTotalMs: normalization.AfterTotalMs}
	result.TotalMs = normalization.Delta(result.BeforeTotalMs, result.AfterTotalMs)
	functions := make(map[string]FunctionDelta)
	for _, function := range before.Functions() {
		functions[function.ID] = FunctionDelta{ID: function.ID, Frame: function.Frame,
			BeforeSelfMs: function.SelfMs, BeforeTotalMs: function.TotalMs, BeforeSamples: function.Samples}
	}
	for _, function := range after.Functions() {
		delta, exists := functions[function.ID]
		if !exists {
			delta.ID, delta.Frame = function.ID, function.Frame
		} else {
			delta.Frame = chooseFrame(delta.Frame, function.Frame)
		}
		delta.AfterSelfMs, delta.AfterTotalMs, delta.AfterSamples = function.SelfMs, function.TotalMs, function.Samples
		functions[function.ID] = delta
	}
	for _, delta := range functions {
		delta.SelfMs = normalization.Delta(delta.BeforeSelfMs, delta.AfterSelfMs)
		delta.TotalMs = normalization.Delta(delta.BeforeTotalMs, delta.AfterTotalMs)
		delta.Samples = delta.AfterSamples - delta.BeforeSamples
		result.Functions = append(result.Functions, delta)
	}
	slices.SortFunc(result.Functions, func(a, b FunctionDelta) int { return compareFrame(a.Frame, b.Frame) })
	var beforeRoot, afterRoot *Node
	if before != nil {
		beforeRoot = before.Root
	}
	if after != nil {
		afterRoot = after.Root
	}
	result.Root = diffNode(beforeRoot, afterRoot, normalization)
	type work struct {
		before, after *Node
		output        *NodeDelta
	}
	pending := []work{{beforeRoot, afterRoot, result.Root}}
	for len(pending) != 0 {
		current := pending[len(pending)-1]
		pending = pending[:len(pending)-1]
		pairs := make(map[string]work)
		if current.before != nil {
			for _, node := range current.before.Children {
				pairs[node.ID] = work{before: node}
			}
		}
		if current.after != nil {
			for _, node := range current.after.Children {
				pair := pairs[node.ID]
				pair.after = node
				pairs[node.ID] = pair
			}
		}
		for _, pair := range pairs {
			pair.output = diffNode(pair.before, pair.after, normalization)
			current.output.Children = append(current.output.Children, pair.output)
			pending = append(pending, pair)
		}
		slices.SortFunc(current.output.Children, func(a, b *NodeDelta) int { return compareFrame(a.Frame, b.Frame) })
	}
	return result, nil
}

func diffNode(before, after *Node, normalization DiffNormalization) *NodeDelta {
	delta := &NodeDelta{ID: "root", Frame: Frame{Name: "(root)"}, Children: make([]*NodeDelta, 0)}
	if before != nil {
		delta.ID, delta.Frame = before.ID, before.Frame
		delta.BeforeSelfMs, delta.BeforeTotalMs, delta.BeforeSamples = before.SelfMs, before.TotalMs, before.Samples
	}
	if after != nil {
		delta.ID = after.ID
		if before == nil {
			delta.Frame = after.Frame
		} else {
			delta.Frame = chooseFrame(delta.Frame, after.Frame)
		}
		delta.AfterSelfMs, delta.AfterTotalMs, delta.AfterSamples = after.SelfMs, after.TotalMs, after.Samples
	}
	delta.SelfMs = normalization.Delta(delta.BeforeSelfMs, delta.AfterSelfMs)
	delta.TotalMs = normalization.Delta(delta.BeforeTotalMs, delta.AfterTotalMs)
	delta.Samples = delta.AfterSamples - delta.BeforeSamples
	return delta
}
