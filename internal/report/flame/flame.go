// Package flame renders shared flame trees without combining unrelated recordings.
// JSON documents use compact, interned schema wkperf-flame/v2. Every chart keeps
// one recording; differential charts retain raw inputs and normalized deltas.
// Raw durations are milliseconds. Folded weights use integer microseconds.
// Invert and MinMS affect visual layout only, never exported sample weights.
package flame

import (
	"fmt"
	"io"
	"math"
	"strconv"
	"strings"

	core "github.com/omniviewdev/wkperf/internal/flame"
)

// Chart keeps each recording separate. A differential chart supplies Before,
// After, and Comparison instead of Tree. Labels identify original inputs.
type Chart struct {
	Label           string           `json:"label"`
	Tree            *core.Tree       `json:"tree,omitempty"`
	Before          *core.Tree       `json:"before,omitempty"`
	After           *core.Tree       `json:"after,omitempty"`
	Comparison      *core.Comparison `json:"comparison,omitempty"`
	UntimedSamples  int              `json:"untimedSamples"`
	UntimedMs       float64          `json:"untimedMs"`
	UntimedExcluded bool             `json:"untimedExcluded"`
	Empty           bool             `json:"empty"`
	EmptyReason     string           `json:"emptyReason,omitempty"`
}

// Options controls presentation. MinMS hides smaller visual contexts, not time.
type Options struct {
	Title  string  `json:"title,omitempty"`
	Invert bool    `json:"invert"`
	MinMS  float64 `json:"minMs"`
}

// Render writes one of html, svg, folded, speedscope, or json.
func Render(w io.Writer, format string, charts []Chart, opts Options) error {
	if err := validate(charts, opts); err != nil {
		return err
	}
	switch format {
	case "html":
		return RenderHTML(w, charts, opts)
	case "svg":
		return RenderSVG(w, charts, opts)
	case "folded":
		return renderFolded(w, charts)
	case "speedscope":
		return renderSpeedscope(w, charts)
	case "json":
		return renderJSON(w, charts, opts)
	default:
		return fmt.Errorf("unknown flame format %q (want html|svg|folded|speedscope|json)", format)
	}
}

func chartMetadata(chart Chart) Chart {
	chart.UntimedSamples, chart.UntimedMs, chart.UntimedExcluded = 0, 0, false
	chart.Empty, chart.EmptyReason = false, ""
	add := func(tree *core.Tree) {
		chart.UntimedSamples += tree.UntimedTotal.Count
		chart.UntimedMs += tree.UntimedTotal.Ms
		chart.UntimedExcluded = chart.UntimedExcluded || tree.UntimedTotal.Count > 0 && !tree.UntimedIncluded
	}
	if chart.Comparison != nil {
		add(chart.Before)
		add(chart.After)
		chart.Empty = chart.Before.Root.TotalMs == 0 && chart.After.Root.TotalMs == 0
		if chart.Empty {
			chart.EmptyReason = "no weighted samples in baseline or current selected window"
		}
	} else {
		add(chart.Tree)
		chart.Empty = chart.Tree.Root.TotalMs == 0
		if chart.Empty {
			chart.EmptyReason = emptyReason(chart.Tree)
			if note := exclusionNote(chart.Tree); note != "" {
				chart.EmptyReason += "; " + note
			}
		}
	}
	return chart
}

func exclusionNote(tree *core.Tree) string {
	if tree.UntimedIncluded || tree.UntimedTotal.Count == 0 {
		return ""
	}
	return fmt.Sprintf("%d samples (%g ms) have no timestamp and are excluded from this window", tree.UntimedTotal.Count, tree.UntimedTotal.Ms)
}

func emptyReason(tree *core.Tree) string {
	return fmt.Sprintf("no weighted samples in %.3f–%.3f s (%s ms active)", tree.StartTime, tree.EndTime, milliseconds(tree.ActiveDurationMs))
}

// WriteNotes reports valid empty selections and untimed exclusions without
// adding nonstandard fields or comments to folded and speedscope exports.
func WriteNotes(w io.Writer, charts []Chart) error {
	write := func(label string, tree *core.Tree) error {
		note := exclusionNote(tree)
		if tree.Root.TotalMs == 0 {
			if note != "" {
				note += "; "
			}
			note += emptyReason(tree)
		}
		if note == "" {
			return nil
		}
		if len(charts) == 1 && charts[0].Comparison == nil {
			_, err := fmt.Fprintf(w, "wkperf: %s\n", note)
			return err
		}
		_, err := fmt.Fprintf(w, "wkperf: %s: %s\n", label, note)
		return err
	}
	for _, chart := range charts {
		if chart.Comparison != nil {
			for _, warning := range chart.Comparison.Normalization.Warnings {
				if _, err := fmt.Fprintf(w, "wkperf: %s: [%s] %s\n", chart.Label, warning.Code, warning.Message); err != nil {
					return err
				}
			}
			if err := write(chart.Label+" · baseline", chart.Before); err != nil {
				return err
			}
			if err := write(chart.Label+" · current", chart.After); err != nil {
				return err
			}
		} else if err := write(chart.Label, chart.Tree); err != nil {
			return err
		}
	}
	return nil
}

func validate(charts []Chart, opts Options) error {
	if len(charts) == 0 {
		return fmt.Errorf("no flame charts")
	}
	if math.IsNaN(opts.MinMS) || math.IsInf(opts.MinMS, 0) || opts.MinMS < 0 {
		return fmt.Errorf("--min-ms must be finite and nonnegative")
	}
	diff := charts[0].Comparison != nil
	for _, chart := range charts {
		if (chart.Comparison != nil) != diff {
			return fmt.Errorf("cannot mix profile and differential charts")
		}
		if diff {
			if chart.Comparison.Root == nil || chart.Before == nil || chart.After == nil || chart.Before.Root == nil || chart.After.Root == nil {
				return fmt.Errorf("differential chart %q requires before and after trees", chart.Label)
			}
			if _, err := core.ParseDiffMode(string(chart.Comparison.Normalization.RequestedMode)); err != nil {
				return fmt.Errorf("differential chart %q: %w", chart.Label, err)
			}
		} else if chart.Tree == nil || chart.Tree.Root == nil {
			return fmt.Errorf("chart %q has no tree", chart.Label)
		}
	}
	return nil
}

func mode(charts []Chart) string {
	if len(charts) > 0 && charts[0].Comparison != nil {
		return "diff"
	}
	return "profile"
}

func title(opts Options) string {
	if opts.Title != "" {
		return opts.Title
	}
	return "Flame graph"
}

func milliseconds(v float64) string {
	if v == 0 {
		return "0"
	}
	return strconv.FormatFloat(v, 'f', 3, 64)
}

func percent(v, total float64) string {
	if total <= 0 {
		return "0%"
	}
	return strconv.FormatFloat(100*v/total, 'f', 2, 64) + "%"
}

func renderFolded(w io.Writer, charts []Chart) error {
	for _, chart := range charts {
		var folded string
		if chart.Comparison != nil {
			folded = core.FoldedDiff(chart.Before, chart.After)
		} else {
			folded = chart.Tree.Folded()
		}
		if len(charts) > 1 {
			prefix := core.FoldedLabel(chart.Label) + ";"
			for _, line := range strings.Split(strings.TrimSuffix(folded, "\n"), "\n") {
				if line == "" {
					continue
				}
				if _, err := io.WriteString(w, prefix+line+"\n"); err != nil {
					return err
				}
			}
		} else if _, err := io.WriteString(w, folded); err != nil {
			return err
		}
	}
	return nil
}

// Rect is a normalized visual rectangle. Width and X lie in [0,1]. Depth zero
// is the synthetic root, displayed at the bottom of a conventional flame graph.
type Rect struct {
	Node  *core.Node
	X     float64
	Width float64
	Depth int
}

// Layout preserves self-time gaps and never renormalizes culled contexts.
func Layout(tree *core.Tree, opts Options) []Rect {
	if tree == nil || tree.Root == nil {
		return nil
	}
	if opts.Invert {
		tree = tree.Inverted()
	}
	var result []Rect
	var visit func(*core.Node, float64, float64, int)
	visit = func(node *core.Node, x, width float64, depth int) {
		if depth > 0 && (node.TotalMs < opts.MinMS || width <= 0) {
			return
		}
		result = append(result, Rect{Node: node, X: x, Width: width, Depth: depth})
		for _, child := range node.Children {
			span := 0.0
			if node.TotalMs > 0 {
				span = width * child.TotalMs / node.TotalMs
			}
			visit(child, x, span, depth+1)
			x += span
		}
	}
	visit(tree.Root, 0, 1, 0)
	return result
}

type viewNode struct {
	ID         string         `json:"id"`
	Frame      core.Frame     `json:"frame"`
	Original   *core.Location `json:"original,omitempty"`
	Category   core.Category  `json:"category"`
	SelfMS     float64        `json:"selfMs"`
	TotalMS    float64        `json:"totalMs"`
	LayoutMS   float64        `json:"layoutMs"`
	BaselineMS float64        `json:"baselineMs,omitempty"`
	CurrentMS  float64        `json:"currentMs,omitempty"`
	DeltaMS    float64        `json:"deltaMs,omitempty"`
	Children   []*viewNode    `json:"children"`
}

func normalView(node *core.Node) *viewNode {
	v := &viewNode{ID: node.ID, Frame: node.Frame, Original: node.Frame.Original, Category: node.Frame.Category, SelfMS: node.SelfMs, TotalMS: node.TotalMs, LayoutMS: node.TotalMs, Children: make([]*viewNode, 0, len(node.Children))}
	for _, child := range node.Children {
		v.Children = append(v.Children, normalView(child))
	}
	return v
}

func diffView(node *core.NodeDelta) *viewNode { return diffNodeView(node, false) }

func diffNodeView(node *core.NodeDelta, inverted bool) *viewNode {
	v := &viewNode{ID: node.ID, Frame: node.Frame, Original: node.Frame.Original, Category: node.Frame.Category,
		SelfMS: node.AfterSelfMs, TotalMS: node.AfterTotalMs, BaselineMS: node.BeforeTotalMs, CurrentMS: node.AfterTotalMs, DeltaMS: node.TotalMs,
		Children: make([]*viewNode, 0, len(node.Children))}
	if !inverted {
		v.LayoutMS = math.Max(node.BeforeSelfMs, node.AfterSelfMs)
	}
	for _, child := range node.Children {
		c := diffNodeView(child, inverted)
		v.Children = append(v.Children, c)
		v.LayoutMS += c.LayoutMS
	}
	v.LayoutMS = math.Max(v.LayoutMS, math.Max(node.BeforeTotalMs, node.AfterTotalMs))
	return v
}

func chartView(chart Chart, invert bool) *viewNode {
	if chart.Comparison != nil {
		if invert {
			comparison, _ := core.DiffNormalized(chart.Before.Inverted(), chart.After.Inverted(), chart.Comparison.Normalization.RequestedMode)
			return diffNodeView(comparison.Root, true)
		}
		return diffView(chart.Comparison.Root)
	}
	if invert {
		return normalView(chart.Tree.Inverted().Root)
	}
	return normalView(chart.Tree.Root)
}

type viewRect struct {
	node     *viewNode
	x, width float64
	depth    int
}

func viewLayout(root *viewNode, minMS float64) ([]viewRect, int) {
	var result []viewRect
	maxDepth := 0
	var visit func(*viewNode, float64, float64, int)
	visit = func(node *viewNode, x, width float64, depth int) {
		if depth > 0 && (node.LayoutMS < minMS || width <= 0) {
			return
		}
		result = append(result, viewRect{node: node, x: x, width: width, depth: depth})
		maxDepth = max(maxDepth, depth)
		for _, child := range node.Children {
			span := 0.0
			if node.LayoutMS > 0 {
				span = width * child.LayoutMS / node.LayoutMS
			}
			visit(child, x, span, depth+1)
			x += span
		}
	}
	visit(root, 0, 1, 0)
	return result, maxDepth
}

func frameName(frame core.Frame) string {
	if frame.Original != nil && frame.Original.Name != "" {
		return frame.Original.Name
	}
	if frame.Name == "" {
		return "(anonymous)"
	}
	return frame.Name
}

func location(frame core.Frame) string {
	return fmt.Sprintf("%s:%d:%d", frame.URL, frame.Line, frame.Column)
}
