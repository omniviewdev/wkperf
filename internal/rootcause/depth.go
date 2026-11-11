package rootcause

import (
	"fmt"
	"math"
	"sort"
	"strings"

	"github.com/omniviewdev/wkperf/internal/flame"
)

func percentile(values []float64, p float64) float64 {
	if len(values) == 0 {
		return 0
	}
	r := p * float64(len(values)-1)
	lo := int(math.Floor(r))
	hi := int(math.Ceil(r))
	return values[lo] + (values[hi]-values[lo])*(r-float64(lo))
}

func stackDepth(frames []flame.Frame) (repeated, commit int) {
	counts := map[string]int{}
	for _, f := range frames {
		key := flame.FunctionID(f)
		counts[key]++
		if counts[key] > repeated {
			repeated = counts[key]
		}
		if commitFrame(f) {
			commit++
		}
	}
	return repeated, commit
}

func (b builder) depth() DepthStats {
	d := DepthStats{Command: b.query("depth", b.from, b.to)}
	if len(b.paths) == 0 {
		d.Command = b.reproduce("depth.max")
	}
	values := make([]float64, 0, b.tree.SampleCount())
	for _, path := range b.paths {
		for range path.Samples {
			values = append(values, float64(len(path.Frames)))
		}
		d.Max = max(d.Max, len(path.Frames))
		repeat, commit := stackDepth(path.Frames)
		d.MaxRepeatedFrameDepth = max(d.MaxRepeatedFrameDepth, repeat)
		d.MaxCommitDepth = max(d.MaxCommitDepth, commit)
	}
	sort.Float64s(values)
	d.Median = number(percentile(values, .5))
	d.P95 = number(percentile(values, .95))
	d.P99 = number(percentile(values, .99))
	deviations := make([]float64, len(values))
	for i, v := range values {
		deviations[i] = math.Abs(v - d.Median)
	}
	sort.Float64s(deviations)
	mad := percentile(deviations, .5)
	// A fixed excess guards against tiny MAD in healthy, nearly uniform stacks.
	d.OutlierThreshold = number(math.Max(32, d.Median+math.Max(16, 6*1.4826*mad)))
	var outlierSamples []flame.Sample
	b.tree.EachSample(func(s flame.Sample) {
		if float64(len(s.Frames)) > d.OutlierThreshold {
			outlierSamples = append(outlierSamples, s)
		}
	})
	for _, stack := range b.stacks(outlierSamples, "depth.outlier") {
		repeat, commit := stackDepth(stack.Frames)
		key := "depth.outlier." + stackKey(stack.Frames)
		d.Outliers = append(d.Outliers, DepthOutlier{Depth: len(stack.Frames), RepeatedFrameDepth: repeat, CommitDepth: commit, Stack: stack, Command: stack.Command, Stats: []Stat{
			b.stat(key+".depth", "Outlier stack depth", float64(len(stack.Frames)), "frames", sampledBasis),
			b.stat(key+".repeated_frame_depth", "Repeated identical frame appearances", float64(repeat), "frames", sampledBasis),
			b.stat(key+".commit_depth", "Commit-frame appearances on the stack", float64(commit), "frames", sampledBasis),
		}})
	}
	sort.Slice(d.Outliers, func(i, j int) bool {
		if d.Outliers[i].Depth != d.Outliers[j].Depth {
			return d.Outliers[i].Depth > d.Outliers[j].Depth
		}
		if d.Outliers[i].Stack.SelfMs != d.Outliers[j].Stack.SelfMs {
			return d.Outliers[i].Stack.SelfMs > d.Outliers[j].Stack.SelfMs
		}
		return framesLess(d.Outliers[i].Stack.Frames, d.Outliers[j].Stack.Frames)
	})
	d.Stats = []Stat{
		b.stat("depth.median", "Median sampled stack depth", d.Median, "frames", sampledBasis),
		b.stat("depth.p95", "P95 sampled stack depth", d.P95, "frames", sampledBasis),
		b.stat("depth.p99", "P99 sampled stack depth", d.P99, "frames", sampledBasis),
		b.stat("depth.max", "Maximum sampled stack depth", float64(d.Max), "frames", sampledBasis),
		b.stat("depth.outlier_threshold", "Robust depth outlier threshold", d.OutlierThreshold, "frames", "median + max(16, 6 × 1.4826 × median absolute deviation), minimum 32"),
		b.stat("depth.max_repeated_frame", "Maximum repeated identical frame count", float64(d.MaxRepeatedFrameDepth), "frames", sampledBasis),
		b.stat("depth.max_commit", "Maximum commit frame count", float64(d.MaxCommitDepth), "frames", sampledBasis),
		b.stat("depth.outlier_stacks", "Distinct depth outlier stacks", float64(len(d.Outliers)), "count", sampledBasis),
	}
	return d
}

func repeatedFrame(frames []flame.Frame) *RepeatedFrame {
	counts := make(map[string]int, len(frames))
	var best RepeatedFrame
	for _, f := range frames {
		key := flame.FunctionID(f)
		counts[key]++
		if best.Count == 0 || counts[key] > best.Count || counts[key] == best.Count && frameLess(f, best.Frame) {
			best = RepeatedFrame{Frame: f, Count: counts[key]}
		}
	}
	if best.Count == 0 {
		return nil
	}
	return &best
}

func recursionFrame(frames []flame.Frame) *RepeatedFrame {
	var prior, previous flame.Frame
	run, pairRun, seen := 0, 0, 0
	var best RepeatedFrame
	for _, f := range frames {
		if !contextFrame(f) {
			continue
		}
		if seen > 0 && flame.FunctionID(f) == flame.FunctionID(previous) {
			run++
		} else {
			run = 1
		}
		if seen >= 2 && flame.FunctionID(f) == flame.FunctionID(prior) {
			pairRun++
		} else {
			pairRun = min(seen+1, 2)
		}
		count := max(run, (pairRun+1)/2)
		if count >= 32 && (best.Count == 0 || count > best.Count || count == best.Count && frameLess(f, best.Frame)) {
			best = RepeatedFrame{Frame: f, Count: count}
		}
		prior, previous = previous, f
		seen++
	}
	if best.Count == 0 {
		return nil
	}
	return &best
}

func reactFiberLevels(frames []flame.Frame) int {
	counts := map[string]int{}
	maximum := 0
	for _, f := range frames {
		if f.Category != flame.CategoryFramework {
			continue
		}
		for _, name := range frameNames(f) {
			if strings.HasSuffix(name, "OnFiber") || strings.HasPrefix(name, "recursivelyTraverse") {
				key := flame.FunctionID(f)
				counts[key]++
				maximum = max(maximum, counts[key])
				break
			}
		}
	}
	return maximum
}

func (b builder) depthFindings(d DepthStats) []Finding {
	if len(d.Outliers) == 0 {
		return nil
	}
	facets := &DepthFacets{}
	stacks := make([]Stack, 0, len(d.Outliers))
	var ms float64
	for _, o := range d.Outliers {
		stacks = append(stacks, o.Stack)
		ms += o.Stack.SelfMs
		facets.MaxDepth = max(facets.MaxDepth, o.Depth)
		facets.CommitFrames = max(facets.CommitFrames, o.CommitDepth)
		facets.ReactFiberLevels = max(facets.ReactFiberLevels, reactFiberLevels(o.Stack.Frames))
		if repeated := repeatedFrame(o.Stack.Frames); repeated != nil && (facets.RepeatedFrame == nil || repeated.Count > facets.RepeatedFrame.Count || repeated.Count == facets.RepeatedFrame.Count && frameLess(repeated.Frame, facets.RepeatedFrame.Frame)) {
			facets.RepeatedFrame = repeated
		}
		if recursion := recursionFrame(o.Stack.Frames); recursion != nil && (facets.Recursion == nil || recursion.Count > facets.Recursion.Count || recursion.Count == facets.Recursion.Count && frameLess(recursion.Frame, facets.Recursion.Frame)) {
			facets.Recursion = recursion
		}
	}
	title := "Unusually deep sampled stacks"
	explanation := "Stack depths exceed a robust distribution-based threshold. Repeated frame appearances and commit-frame counts are facets of this one observation; sampled self cost does not establish that depth caused the work."
	recursionCount := 0
	if facets.Recursion != nil {
		recursionCount = facets.Recursion.Count
		title = "Deep sampled stacks with possible non-framework recursion"
		explanation += " A non-framework function repeats at least 32 times consecutively or in a cycle of at most two functions, after excluding framework frames. This supports possible recursion, not unbounded execution."
	} else if facets.ReactFiberLevels > 0 && facets.CommitFrames >= 32 {
		title = fmt.Sprintf("Deep React fiber tree (~%d levels; commit traversal depth %d)", facets.ReactFiberLevels, facets.CommitFrames)
		explanation += " Known React commit traversal follows fiber-tree depth by design; the scheduling component need not appear on the captured stack."
	}
	sampleIDs := SampleIDs(b.tree, func(s flame.Sample) bool { return float64(len(s.Frames)) > d.OutlierThreshold })
	repeatedCount := 0
	if facets.RepeatedFrame != nil {
		repeatedCount = facets.RepeatedFrame.Count
	}
	return []Finding{{ID: "deep-stack", Title: title, Explanation: explanation, Depth: facets, SampleIDs: sampleIDs, Stats: []Stat{
		b.stat("deep-stack.self_ms", "Depth outlier stack self time", ms, "ms", sampledBasis),
		b.stat("deep-stack.stacks", "Distinct matching outlier stacks", float64(len(stacks)), "count", sampledBasis),
		b.stat("deep-stack.max_depth", "Maximum outlier stack depth", float64(facets.MaxDepth), "frames", sampledBasis),
		b.stat("deep-stack.repeated_frame_count", "Maximum appearances of one exact generated frame", float64(repeatedCount), "frames", sampledBasis),
		b.stat("deep-stack.commit_frames", "Maximum commit-frame appearances", float64(facets.CommitFrames), "frames", sampledBasis),
		b.stat("deep-stack.react_fiber_levels", "Maximum repeated React traversal-frame count", float64(facets.ReactFiberLevels), "levels", sampledBasis),
		b.stat("deep-stack.recursion_count", "Non-framework short-cycle repetition count", float64(recursionCount), "frames", sampledBasis),
	}, Evidence: stacks, Command: b.query("depth", b.from, b.to)}}
}
