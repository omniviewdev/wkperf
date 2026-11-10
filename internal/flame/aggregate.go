package flame

import "slices"

type functionWork struct {
	function    Function
	self, total sum
}

type edgeKey struct{ caller, callee frameKey }
type edgeWork struct {
	edge  Edge
	total sum
}

type pathWork struct {
	path  Path
	total sum
}

func (tree *Tree) aggregate(leafIDs []string) {
	functions := make(map[frameKey]*functionWork)
	edges := make(map[edgeKey]*edgeWork)
	paths := make(map[string]*pathWork)
	functionSeen := make(map[frameKey]int)
	edgeSeen := make(map[edgeKey]int)
	sampleIndex := 0
	tree.EachSample(func(sample Sample) {
		generation := sampleIndex + 1
		for i, frame := range sample.Frames {
			k := key(frame)
			function := functions[k]
			if function == nil {
				function = &functionWork{function: Function{ID: FunctionID(frame), Frame: frame}}
				functions[k] = function
			}
			if functionSeen[k] != generation {
				function.total.add(sample.WeightMs)
				function.function.Samples++
				functionSeen[k] = generation
			}
			if i == len(sample.Frames)-1 {
				function.self.add(sample.WeightMs)
				function.function.SelfSamples++
			}
			if i != 0 {
				caller := sample.Frames[i-1]
				ek := edgeKey{key(caller), k}
				if edgeSeen[ek] == generation {
					continue
				}
				edge := edges[ek]
				if edge == nil {
					edge = &edgeWork{edge: Edge{CallerID: functions[ek.caller].function.ID, CalleeID: function.function.ID, Caller: caller, Callee: frame}}
					edges[ek] = edge
				}
				edge.total.add(sample.WeightMs)
				edge.edge.Samples++
				edgeSeen[ek] = generation
			}
		}
		id := leafIDs[sampleIndex]
		path := paths[id]
		if path == nil {
			path = &pathWork{path: Path{ID: id, Frames: sample.Frames}}
			paths[id] = path
		}
		path.total.add(sample.WeightMs)
		path.path.Samples++
		sampleIndex++
	})
	tree.functions = make([]Function, 0, len(functions))
	for _, work := range functions {
		work.function.SelfMs, work.function.TotalMs = work.self.total(), work.total.total()
		tree.functions = append(tree.functions, work.function)
	}
	slices.SortFunc(tree.functions, func(a, b Function) int { return compareFrame(a.Frame, b.Frame) })
	tree.edges = make([]Edge, 0, len(edges))
	for _, work := range edges {
		work.edge.TotalMs = work.total.total()
		tree.edges = append(tree.edges, work.edge)
	}
	slices.SortFunc(tree.edges, func(a, b Edge) int {
		if c := compareFrame(a.Caller, b.Caller); c != 0 {
			return c
		}
		return compareFrame(a.Callee, b.Callee)
	})
	tree.paths = make([]Path, 0, len(paths))
	for _, work := range paths {
		work.path.SelfMs, work.path.TotalMs = work.total.total(), work.total.total()
		tree.paths = append(tree.paths, work.path)
	}
	slices.SortFunc(tree.paths, func(a, b Path) int { return compareStack(a.Frames, b.Frames) })
}

// Functions returns a read-only, identity-sorted aggregate view.
func (tree *Tree) Functions() []Function {
	if tree == nil {
		return []Function{}
	}
	return tree.functions
}

// Edges returns a read-only view sorted by caller, then callee identity.
func (tree *Tree) Edges() []Edge {
	if tree == nil {
		return []Edge{}
	}
	return tree.edges
}

func (tree *Tree) Callers(functionID string) []Edge {
	out := make([]Edge, 0)
	if tree != nil {
		for _, edge := range tree.edges {
			if edge.CalleeID == functionID {
				out = append(out, edge)
			}
		}
	}
	return out
}

func (tree *Tree) Callees(functionID string) []Edge {
	out := make([]Edge, 0)
	if tree != nil {
		for _, edge := range tree.edges {
			if edge.CallerID == functionID {
				out = append(out, edge)
			}
		}
	}
	return out
}

// Paths returns read-only full stacks, sorted by frame identity at each depth.
func (tree *Tree) Paths() []Path {
	if tree == nil {
		return []Path{}
	}
	return tree.paths
}

// SamplePathIDs returns read-only canonical path IDs aligned with Samples.
func (tree *Tree) SamplePathIDs() []string {
	if tree == nil {
		return []string{}
	}
	return tree.samplePathIDs
}

// SampleCount counts timed samples and untimed samples retained by this view.
func (tree *Tree) SampleCount() int {
	if tree == nil {
		return 0
	}
	return len(tree.Samples) + len(tree.Untimed)
}

// EachSample visits included untimed samples first, then time-sorted samples.
// The samples and their frame slices are read-only. No combined slice is made.
func (tree *Tree) EachSample(visit func(Sample)) {
	if tree == nil {
		return
	}
	for _, sample := range tree.Untimed {
		visit(sample)
	}
	for _, sample := range tree.Samples {
		visit(sample)
	}
}
