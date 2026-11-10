package rootcause

import (
	"math"
	"sort"

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
			b.stat(key+".commit_depth", "Commit self-frame ancestors", float64(commit), "frames", sampledBasis),
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

func (b builder) depthFindings(d DepthStats) []Finding {
	var recursion, commit, all []Stack
	for _, o := range d.Outliers {
		all = append(all, o.Stack)
		if o.RepeatedFrameDepth >= 32 {
			recursion = append(recursion, o.Stack)
		}
		if o.CommitDepth >= 32 {
			commit = append(commit, o.Stack)
		}
	}
	var findings []Finding
	add := func(id, title, explanation string, stacks []Stack) {
		if len(stacks) == 0 {
			return
		}
		var ms float64
		for _, s := range stacks {
			ms += s.SelfMs
		}
		findings = append(findings, Finding{ID: id, Title: title, Severity: "medium", Explanation: explanation, Stats: []Stat{
			b.stat(id+".self_ms", "Depth outlier stack self time", ms, "ms", sampledBasis),
			b.stat(id+".stacks", "Distinct matching outlier stacks", float64(len(stacks)), "count", sampledBasis),
		}, Evidence: stacks, Command: b.query("depth", b.from, b.to)})
	}
	add("deep-stack", "Unusually deep sampled stacks", "Stack depths exceed a robust distribution-based threshold. Deep stacks alone do not prove recursion or runaway work.", all)
	add("deep-recursion", "Possible runaway recursion in depth outliers", "An unusually deep full stack repeats an identical generated function at least 32 times. This is a sampled recursion warning, not proof of unbounded execution.", recursion)
	add("deep-commit", "Unusually deep React commit traversal", "An unusually deep full stack contains at least 32 distinct commit-frame appearances. Repeated traversal depth is evidence of a deep commit, not proof of an infinite render loop.", commit)
	return findings
}
