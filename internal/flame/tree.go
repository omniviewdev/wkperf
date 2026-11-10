package flame

import (
	"cmp"
	"math"
	"slices"
	"sort"

	"github.com/omniviewdev/wkperf/internal/model"
	"github.com/omniviewdev/wkperf/internal/profile"
)

// Build reverses WebKit's leaf-first stacks without mutating the recording.
func Build(rec *model.Recording, opts ...Options) *Tree {
	if rec == nil {
		return assemble(nil, 0, 0, nil, nil)
	}
	var options Options
	if len(opts) != 0 {
		options = opts[len(opts)-1]
	}
	origin := rec.StartTime
	if !finite(origin) {
		origin = 0
	}
	var samples []Sample
	canonical := make(map[frameKey]Frame)
	normalized := make(map[model.StackFrame]Frame)
	end := normalizeTime(rec.EndTime - origin)
	bounded := finite(end) && end > 0
	if !bounded {
		end = 0
	}
	for _, target := range rec.Samples {
		for i, trace := range target.StackTraces {
			weight := .001
			if i < len(target.Durations) {
				weight = target.Durations[i]
			}
			weight *= 1000
			timestamp := normalizeTime(trace.Timestamp - origin)
			if !finite(weight) || weight <= 0 || !finite(timestamp) || len(trace.StackFrames) == 0 {
				continue
			}
			frames := make([]Frame, len(trace.StackFrames))
			for j, raw := range trace.StackFrames {
				frame, ok := normalized[raw]
				if !ok {
					frame = fromModel(raw, options.Mapper)
					normalized[raw] = frame
				}
				frames[len(frames)-1-j] = frame
				k := key(frame)
				if previous, ok := canonical[k]; ok {
					canonical[k] = chooseFrame(previous, frame)
				} else {
					canonical[k] = frame
				}
			}
			samples = append(samples, Sample{Frames: frames, Timestamp: timestamp, WeightMs: weight, TargetID: target.Target.Identifier})
			if !bounded && timestamp >= end {
				end = normalizeTime(timestamp + 1e-9)
				if end <= timestamp {
					end = math.Nextafter(timestamp, math.Inf(1))
				}
			}
		}
	}
	if !finite(end) {
		end = 0
	}
	for i := range samples {
		for j, frame := range samples[i].Frames {
			samples[i].Frames[j] = canonical[key(frame)]
		}
	}
	from, to := 0.0, end
	if options.From != nil {
		from = *options.From
	}
	if options.To != nil {
		to = *options.To
	}
	from, to = normalizeTime(from), normalizeTime(to)
	if !finite(from) || !finite(to) {
		return assemble(nil, 0, 0, nil, nil)
	}
	from, to = min(end, max(0, from)), min(end, max(0, to))
	if to < from {
		to = from
	}
	active := make([]Interval, 0)
	for _, span := range profile.ActiveIntervals(rec, origin+from, origin+to) {
		relative := Interval{Start: normalizeTime(span.Start - origin), End: normalizeTime(span.End - origin)}
		if relative.End > relative.Start {
			active = append(active, relative)
		}
	}
	pauses := complement(active, from, to)
	if options.Intervals != nil {
		active = intersect(active, union(options.Intervals))
	}
	return assemble(selectSamples(samples, active), from, to, active, pauses)
}

// Window selects statistical samples by timestamp, retaining their full weights.
func (tree *Tree) Window(from, to float64) *Tree {
	from, to = normalizeTime(from), normalizeTime(to)
	if tree == nil || !finite(from) || !finite(to) || to < from {
		return assemble(nil, 0, 0, nil, nil)
	}
	from, to = min(tree.EndTime, max(from, tree.StartTime)), max(tree.StartTime, min(to, tree.EndTime))
	if to < from {
		to = from
	}
	bounds := []Interval{{Start: from, End: to}}
	active := intersect(tree.active, bounds)
	out := assemble(selectSamples(tree.Samples, active), from, to, active, intersect(tree.Discontinuities, bounds))
	if tree.inverted {
		return out.Inverted()
	}
	return out
}

// InIntervals selects a union of recording-relative windows without duplication.
func (tree *Tree) InIntervals(intervals []Interval) *Tree {
	if tree == nil {
		return assemble(nil, 0, 0, nil, nil)
	}
	active := intersect(tree.active, union(intervals))
	out := assemble(selectSamples(tree.Samples, active), tree.StartTime, tree.EndTime, active, tree.Discontinuities)
	if tree.inverted {
		return out.Inverted()
	}
	return out
}

// Inverted reverses presentation contexts, not canonical samples or aggregates.
// Original leaf self time belongs to the inverted first-level node.
func (tree *Tree) Inverted() *Tree {
	if tree == nil {
		return assemble(nil, 0, 0, nil, nil)
	}
	if tree.inverted {
		return tree
	}
	out := *tree
	out.inverted = true
	out.Root, out.byID, _ = buildNodes(tree.Samples, true)
	return &out
}

// FocusNode filters one exact context and makes that frame the stack root.
// Unknown IDs select an empty tree. Selector ambiguity is handled by query.
func (tree *Tree) FocusNode(nodeID string) *Tree {
	if tree == nil {
		return assemble(nil, 0, 0, nil, nil)
	}
	node := tree.byID[nodeID]
	if node == tree.Root {
		return tree
	}
	var path []Frame
	for current := node; current != nil && current.Parent != nil; current = current.Parent {
		path = append(path, current.Frame)
	}
	slices.Reverse(path)
	var samples []Sample
	if len(path) != 0 {
		for _, sample := range tree.Samples {
			if len(sample.Frames) < len(path) {
				continue
			}
			matches := true
			for i, frame := range path {
				j := i
				if tree.inverted {
					j = len(sample.Frames) - 1 - i
				}
				if key(frame) != key(sample.Frames[j]) {
					matches = false
					break
				}
			}
			if !matches {
				continue
			}
			if tree.inverted {
				sample.Frames = sample.Frames[:len(sample.Frames)-len(path)+1]
			} else {
				sample.Frames = sample.Frames[len(path)-1:]
			}
			samples = append(samples, sample)
		}
	}
	out := assemble(samples, tree.StartTime, tree.EndTime, tree.active, tree.Discontinuities)
	if tree.inverted {
		return out.Inverted()
	}
	return out
}

// HotPath follows the heaviest child while its total reaches minFraction of the
// whole tree. Ties use frame identity order. The synthetic root is excluded.
func (tree *Tree) HotPath(minFraction float64) []*Node {
	out := make([]*Node, 0)
	if tree == nil || tree.Root == nil || !finite(minFraction) || tree.Root.TotalMs <= 0 {
		return out
	}
	threshold := max(0, minFraction) * tree.Root.TotalMs
	for node := tree.Root; len(node.Children) != 0; {
		best := node.Children[0]
		for _, child := range node.Children[1:] {
			if child.TotalMs > best.TotalMs || (child.TotalMs == best.TotalMs && compareFrame(child.Frame, best.Frame) < 0) {
				best = child
			}
		}
		if best.TotalMs < threshold {
			break
		}
		out = append(out, best)
		node = best
	}
	return out
}

func finite(value float64) bool { return !math.IsNaN(value) && !math.IsInf(value, 0) }

func normalizeTime(value float64) float64 {
	if !finite(value) || math.Abs(value) > math.MaxFloat64/1e9 {
		return value
	}
	value = math.Round(value*1e9) / 1e9
	if value == 0 {
		return 0
	}
	return value
}

func contains(intervals []Interval, timestamp float64) bool {
	i := sort.Search(len(intervals), func(i int) bool { return intervals[i].End > timestamp })
	return i < len(intervals) && intervals[i].Start <= timestamp
}

func selectSamples(samples []Sample, active []Interval) []Sample {
	out := make([]Sample, 0, len(samples))
	for _, sample := range samples {
		if contains(active, sample.Timestamp) {
			out = append(out, sample)
		}
	}
	return out
}

func union(intervals []Interval) []Interval {
	out := make([]Interval, 0, len(intervals))
	for _, span := range intervals {
		span.Start, span.End = normalizeTime(span.Start), normalizeTime(span.End)
		if finite(span.Start) && finite(span.End) && span.End > span.Start {
			out = append(out, span)
		}
	}
	slices.SortFunc(out, func(a, b Interval) int {
		if c := cmp.Compare(a.Start, b.Start); c != 0 {
			return c
		}
		return cmp.Compare(a.End, b.End)
	})
	n := 0
	for _, span := range out {
		if n != 0 && span.Start <= out[n-1].End {
			out[n-1].End = max(out[n-1].End, span.End)
		} else {
			out[n] = span
			n++
		}
	}
	return out[:n]
}

func intersect(a, b []Interval) []Interval {
	out := make([]Interval, 0)
	i, j := 0, 0
	for i < len(a) && j < len(b) {
		start, end := max(a[i].Start, b[j].Start), min(a[i].End, b[j].End)
		if end > start {
			out = append(out, Interval{Start: start, End: end})
		}
		if a[i].End < b[j].End {
			i++
		} else {
			j++
		}
	}
	return out
}

func complement(active []Interval, from, to float64) []Interval {
	var out []Interval
	for _, span := range active {
		if span.Start > from {
			out = append(out, Interval{Start: from, End: span.Start})
		}
		from = max(from, span.End)
	}
	if from < to {
		out = append(out, Interval{Start: from, End: to})
	}
	return out
}

type sum struct{ value, correction float64 }

func (s *sum) add(value float64) {
	next := s.value + value
	if math.Abs(s.value) >= math.Abs(value) {
		s.correction += (s.value - next) + value
	} else {
		s.correction += (value - next) + s.value
	}
	s.value = next
}

func (s sum) total() float64 { return s.value + s.correction }

type nodeWork struct {
	node        *Node
	children    map[frameKey]*nodeWork
	self, total sum
}

func buildNodes(samples []Sample, inverted bool) (*Node, map[string]*Node, []string) {
	root := &Node{ID: "root", Frame: Frame{Name: "(root)"}, Children: make([]*Node, 0)}
	rootWork := &nodeWork{node: root}
	all := []*nodeWork{rootWork}
	byID := map[string]*Node{root.ID: root}
	var leafIDs []string
	if !inverted {
		leafIDs = make([]string, 0, len(samples))
	}
	ids := make(map[frameKey]string)
	for _, sample := range samples {
		current := rootWork
		current.total.add(sample.WeightMs)
		current.node.Samples++
		for i := range sample.Frames {
			index := i
			if inverted {
				index = len(sample.Frames) - 1 - i
			}
			frame := sample.Frames[index]
			k := key(frame)
			if current.children == nil {
				current.children = make(map[frameKey]*nodeWork)
			}
			child := current.children[k]
			if child == nil {
				functionID, ok := ids[k]
				if !ok {
					functionID = FunctionID(frame)
					ids[k] = functionID
				}
				node := &Node{ID: contextID(current.node.ID, functionID), Frame: frame, Parent: current.node, Children: make([]*Node, 0)}
				child = &nodeWork{node: node}
				current.children[k] = child
				current.node.Children = append(current.node.Children, node)
				all = append(all, child)
				byID[node.ID] = node
			}
			child.total.add(sample.WeightMs)
			child.node.Samples++
			if (!inverted && i == len(sample.Frames)-1) || (inverted && i == 0) {
				child.self.add(sample.WeightMs)
			}
			current = child
		}
		if !inverted {
			leafIDs = append(leafIDs, current.node.ID)
		}
	}
	for _, work := range all {
		work.node.TotalMs, work.node.SelfMs = work.total.total(), work.self.total()
		slices.SortFunc(work.node.Children, func(a, b *Node) int { return compareFrame(a.Frame, b.Frame) })
	}
	return root, byID, leafIDs
}

func assemble(samples []Sample, from, to float64, active, pauses []Interval) *Tree {
	if samples == nil {
		samples = make([]Sample, 0)
	}
	slices.SortFunc(samples, func(a, b Sample) int {
		if c := cmp.Compare(a.Timestamp, b.Timestamp); c != 0 {
			return c
		}
		if c := compareStack(a.Frames, b.Frames); c != 0 {
			return c
		}
		if c := cmp.Compare(a.TargetID, b.TargetID); c != 0 {
			return c
		}
		return cmp.Compare(a.WeightMs, b.WeightMs)
	})
	tree := &Tree{Samples: samples, StartTime: from, EndTime: to, active: active, Discontinuities: pauses}
	var duration sum
	for _, span := range active {
		duration.add((span.End - span.Start) * 1000)
	}
	tree.ActiveDurationMs = duration.total()
	tree.Root, tree.byID, tree.samplePathIDs = buildNodes(samples, false)
	tree.aggregate(tree.samplePathIDs)
	return tree
}
