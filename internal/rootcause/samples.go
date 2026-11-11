package rootcause

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/omniviewdev/wkperf/internal/flame"
)

// UntimedSampleStatus labels overlapping export-order spans, never sample times.
func UntimedSampleStatus(spans []flame.UntimedSpan, from, to, offset float64) string {
	var labels []string
	for _, span := range spans {
		if span.FromSec >= to || span.ToSec <= from {
			continue
		}
		labels = append(labels, fmt.Sprintf("%d untimed samples (%.6g ms), captured between +%s s and +%s s by export order, cannot be placed in time", span.Samples, span.Ms, decimal(span.FromSec+offset), decimal(span.ToSec+offset)))
	}
	if len(labels) == 0 {
		return ""
	}
	return "No timed samples in this interval; " + strings.Join(labels, "; ") + "."
}

// UntimedAttributionNote describes missing clocks when at least 10% of weight is untimed.
// sampledMs includes untimed weight even when the selected window excludes it.
func UntimedAttributionNote(untimedMs, sampledMs float64) string {
	if sampledMs <= 0 || untimedMs <= 0 || untimedMs/sampledMs < 0.1 {
		return ""
	}
	share := untimedMs / sampledMs * 100
	return fmt.Sprintf("%.0f%% of sampled weight has no timestamp; time-based attribution uses the remaining %.0f%%.", share, 100-share)
}

// SampleIDs identifies retained evidence in one selected tree, including duplicate
// samples. IDs stay internal and must be computed from the same tree for overlap.
func SampleIDs(tree *flame.Tree, keep func(flame.Sample) bool) []string {
	if tree == nil {
		return nil
	}
	var ids []string
	index := 0
	tree.EachSample(func(s flame.Sample) {
		if keep == nil || keep(s) {
			ids = append(ids, digest(sampleSignature(s), strconv.Itoa(index)))
		}
		index++
	})
	return ids
}

func sampleSignature(s flame.Sample) string {
	return digest(s.TargetID, decimal(s.Timestamp), strconv.FormatFloat(s.WeightMs, 'g', -1, 64), strconv.FormatBool(s.Untimed), stackKey(s.Frames))
}

type sampleRef struct {
	frame   *flame.Frame
	depth   int
	at, ms  float64
	target  string
	untimed bool
}

func sampleReference(s flame.Sample) sampleRef {
	ref := sampleRef{depth: len(s.Frames), at: s.Timestamp, ms: s.WeightMs, target: s.TargetID, untimed: s.Untimed}
	if len(s.Frames) > 0 {
		ref.frame = &s.Frames[0]
	}
	return ref
}

func (b *builder) indexSamples() {
	b.sampleEvidence = make(map[sampleRef][]string, b.tree.SampleCount())
	index := 0
	b.tree.EachSample(func(s flame.Sample) {
		ref := sampleReference(s)
		b.sampleEvidence[ref] = append(b.sampleEvidence[ref], digest(sampleSignature(s), strconv.Itoa(index)))
		index++
	})
}

func (b builder) sampleIDs(samples []flame.Sample) []string {
	var ids []string
	seen := make(map[sampleRef]bool, len(samples))
	for _, s := range samples {
		ref := sampleReference(s)
		if !seen[ref] {
			seen[ref] = true
			ids = append(ids, b.sampleEvidence[ref]...)
		}
	}
	return ids
}
