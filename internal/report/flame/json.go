package flame

import (
	"encoding/json"
	"io"

	core "github.com/omniviewdev/wkperf/internal/flame"
)

const Schema = "wkperf-flame/v2"

// Document is the compact public JSON contract, separate from the private HTML
// payload. Frames occur once across all charts. Every node and sample retains
// exact evidence; neither visual inversion nor min-ms changes exported stacks.
type Document struct {
	Schema string       `json:"schema"`
	Mode   string       `json:"mode"`
	Frames []core.Frame `json:"frames"`
	Charts []WireChart  `json:"charts"`
	View   Options      `json:"view"`
}

// WireChart has one profile tree or both raw trees and their normalized delta.
// Frame references in every tree resolve against Document.Frames.
type WireChart struct {
	Label           string          `json:"label"`
	Tree            *WireTree       `json:"tree,omitempty"`
	Before          *WireTree       `json:"before,omitempty"`
	After           *WireTree       `json:"after,omitempty"`
	Comparison      *WireComparison `json:"comparison,omitempty"`
	UntimedSamples  int             `json:"untimedSamples"`
	UntimedMs       float64         `json:"untimedMs"`
	UntimedExcluded bool            `json:"untimedExcluded"`
	Empty           bool            `json:"empty"`
	EmptyReason     string          `json:"emptyReason,omitempty"`
}

// WireTree stores canonical root-to-leaf contexts in a flat preorder array.
// Root is a node index. Samples retain their recording-relative clock in seconds
// and exact statistical weight in milliseconds, not occupied wall-clock spans.
// Untimed samples have no t field; they must never acquire a synthetic clock.
type WireTree struct {
	Root             int                `json:"root"`
	Nodes            []WireNode         `json:"nodes"`
	Samples          []WireSample       `json:"samples"`
	Untimed          []WireSampleRef    `json:"untimed"`
	UntimedTotal     core.SampleTotal   `json:"untimedTotal"`
	UntimedIncluded  bool               `json:"untimedIncluded"`
	UntimedSpans     []core.UntimedSpan `json:"untimed_spans,omitempty"`
	StartTime        float64            `json:"startTime"`
	EndTime          float64            `json:"endTime"`
	ActiveDurationMs float64            `json:"activeDurationMs"`
	ActiveIntervals  []core.Interval    `json:"activeIntervals"`
	Discontinuities  []core.Interval    `json:"discontinuities,omitempty"`
}

// WireNode references its parent node and document frame by zero-based index.
// The synthetic root uses -1 for both parent and frame; it is not a function.
type WireNode struct {
	ID      string  `json:"id"`
	Parent  int     `json:"parent"`
	Frame   int     `json:"frame"`
	SelfMs  float64 `json:"selfMs"`
	TotalMs float64 `json:"totalMs"`
	Samples int     `json:"samples"`
}

// WireSampleRef identifies one exact canonical stack by its terminal n_ node ID.
// Following parent indices reconstructs its complete stack without truncation.
// W is the original sample weight in milliseconds, with no rounding.
type WireSampleRef struct {
	Path     string  `json:"path"`
	W        float64 `json:"w"`
	TargetID string  `json:"targetID,omitempty"`
}

// WireSample adds a recording-relative timestamp in seconds to a timed sample.
type WireSample struct {
	WireSampleRef
	T float64 `json:"t"`
}

// WireDelta keeps raw before/after milliseconds alongside normalized deltas.
// SelfMs and TotalMs use WireComparison.Normalization.Unit despite their legacy
// field names. Samples remains an unnormalized after-minus-before count.
type WireDelta struct {
	BeforeSelfMs  float64 `json:"beforeSelfMs"`
	AfterSelfMs   float64 `json:"afterSelfMs"`
	SelfMs        float64 `json:"selfMs"`
	BeforeTotalMs float64 `json:"beforeTotalMs"`
	AfterTotalMs  float64 `json:"afterTotalMs"`
	TotalMs       float64 `json:"totalMs"`
	BeforeSamples int     `json:"beforeSamples"`
	AfterSamples  int     `json:"afterSamples"`
	Samples       int     `json:"samples"`
}

type WireNodeDelta struct {
	ID     string `json:"id"`
	Parent int    `json:"parent"`
	Frame  int    `json:"frame"`
	WireDelta
}

type WireFunctionDelta struct {
	ID    string `json:"id"`
	Frame int    `json:"frame"`
	WireDelta
}

// WireComparison uses the same flat context and shared frame references as a
// profile. Raw totals and active durations remain available with mode, unit and
// comparability warnings. Its root and function deltas use the same resolved mode.
type WireComparison struct {
	Root          int                    `json:"root"`
	Nodes         []WireNodeDelta        `json:"nodes"`
	Functions     []WireFunctionDelta    `json:"functions"`
	BeforeTotalMs float64                `json:"beforeTotalMs"`
	AfterTotalMs  float64                `json:"afterTotalMs"`
	TotalMs       float64                `json:"totalMs"`
	Normalization core.DiffNormalization `json:"normalization"`
}

// NewDocument interns shared frame metadata and flattens calling contexts. Chart
// inputs must satisfy the same profile/diff invariants checked by Render.
func NewDocument(charts []Chart, opts Options) Document {
	trees := make([]*core.Tree, 0, len(charts)*2)
	for _, chart := range charts {
		if chart.Comparison == nil {
			trees = append(trees, chart.Tree)
		} else {
			trees = append(trees, chart.Before, chart.After)
		}
	}
	doc := Document{Schema: Schema, Mode: mode(charts), Frames: core.FrameCatalog(trees...), Charts: make([]WireChart, 0, len(charts)), View: opts}
	indices := make(map[string]int, len(doc.Frames))
	for i, frame := range doc.Frames {
		indices[frame.ID] = i
	}
	for _, input := range charts {
		chart := chartMetadata(input)
		wire := WireChart{Label: chart.Label, UntimedSamples: chart.UntimedSamples, UntimedMs: chart.UntimedMs,
			UntimedExcluded: chart.UntimedExcluded, Empty: chart.Empty, EmptyReason: chart.EmptyReason}
		if chart.Comparison == nil {
			wire.Tree = flattenTree(chart.Tree.Canonical(), indices)
		} else {
			before, after := chart.Before.Canonical(), chart.After.Canonical()
			comparison := chart.Comparison
			if before != chart.Before || after != chart.After {
				comparison, _ = core.DiffNormalized(before, after, chart.Comparison.Normalization.RequestedMode)
			}
			wire.Before, wire.After = flattenTree(before, indices), flattenTree(after, indices)
			wire.Comparison = flattenComparison(comparison, indices)
		}
		doc.Charts = append(doc.Charts, wire)
	}
	return doc
}

func renderJSON(w io.Writer, charts []Chart, opts Options) error {
	return json.NewEncoder(w).Encode(NewDocument(charts, opts))
}

func flattenTree(tree *core.Tree, indices map[string]int) *WireTree {
	wire := &WireTree{Nodes: make([]WireNode, 0, len(tree.Functions())+1), Samples: make([]WireSample, len(tree.Samples)), Untimed: make([]WireSampleRef, len(tree.Untimed)),
		UntimedTotal: tree.UntimedTotal, UntimedIncluded: tree.UntimedIncluded, UntimedSpans: tree.UntimedSpans,
		StartTime: tree.StartTime, EndTime: tree.EndTime, ActiveDurationMs: tree.ActiveDurationMs,
		ActiveIntervals: tree.ActiveIntervals(), Discontinuities: tree.Discontinuities}
	type work struct {
		node   *core.Node
		parent int
	}
	pending := []work{{tree.Root, -1}}
	for len(pending) > 0 {
		current := pending[len(pending)-1]
		pending = pending[:len(pending)-1]
		frame := -1
		if current.parent >= 0 {
			frame = indices[current.node.Frame.ID]
		}
		index := len(wire.Nodes)
		wire.Nodes = append(wire.Nodes, WireNode{ID: current.node.ID, Parent: current.parent, Frame: frame,
			SelfMs: current.node.SelfMs, TotalMs: current.node.TotalMs, Samples: current.node.Samples})
		for i := len(current.node.Children) - 1; i >= 0; i-- {
			pending = append(pending, work{current.node.Children[i], index})
		}
	}
	for i, sample := range tree.Samples {
		wire.Samples[i] = WireSample{WireSampleRef: WireSampleRef{Path: tree.SamplePathIDs()[i], W: sample.WeightMs, TargetID: sample.TargetID}, T: sample.Timestamp}
	}
	for i, sample := range tree.Untimed {
		wire.Untimed[i] = WireSampleRef{Path: tree.UntimedPathIDs()[i], W: sample.WeightMs, TargetID: sample.TargetID}
	}
	return wire
}

func flattenComparison(comparison *core.Comparison, indices map[string]int) *WireComparison {
	wire := &WireComparison{Nodes: make([]WireNodeDelta, 0), Functions: make([]WireFunctionDelta, len(comparison.Functions)),
		BeforeTotalMs: comparison.BeforeTotalMs, AfterTotalMs: comparison.AfterTotalMs, TotalMs: comparison.TotalMs, Normalization: comparison.Normalization}
	type work struct {
		node   *core.NodeDelta
		parent int
	}
	pending := []work{{comparison.Root, -1}}
	for len(pending) > 0 {
		current := pending[len(pending)-1]
		pending = pending[:len(pending)-1]
		frame := -1
		if current.parent >= 0 {
			frame = indices[current.node.Frame.ID]
		}
		index := len(wire.Nodes)
		node := current.node
		wire.Nodes = append(wire.Nodes, WireNodeDelta{ID: node.ID, Parent: current.parent, Frame: frame, WireDelta: WireDelta{
			BeforeSelfMs: node.BeforeSelfMs, AfterSelfMs: node.AfterSelfMs, SelfMs: node.SelfMs,
			BeforeTotalMs: node.BeforeTotalMs, AfterTotalMs: node.AfterTotalMs, TotalMs: node.TotalMs,
			BeforeSamples: node.BeforeSamples, AfterSamples: node.AfterSamples, Samples: node.Samples}})
		for i := len(node.Children) - 1; i >= 0; i-- {
			pending = append(pending, work{node.Children[i], index})
		}
	}
	for i, function := range comparison.Functions {
		wire.Functions[i] = WireFunctionDelta{ID: function.ID, Frame: indices[function.ID], WireDelta: WireDelta{
			BeforeSelfMs: function.BeforeSelfMs, AfterSelfMs: function.AfterSelfMs, SelfMs: function.SelfMs,
			BeforeTotalMs: function.BeforeTotalMs, AfterTotalMs: function.AfterTotalMs, TotalMs: function.TotalMs,
			BeforeSamples: function.BeforeSamples, AfterSamples: function.AfterSamples, Samples: function.Samples}}
	}
	return wire
}
