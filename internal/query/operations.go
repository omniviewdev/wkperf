package query

import (
	"math"
	"regexp"
	"sort"
	"strings"

	"github.com/omniviewdev/wkperf/internal/flame"
)

func accepts(f flame.Frame, req Request) bool {
	ref := function(f)
	return (req.Module == "" || strings.Contains(ref.Module, req.Module)) && (req.Category == "" || req.Category == ref.Category)
}

func functionRows(tree *flame.Tree, req Request) ([]Row, error) {
	var match *regexp.Regexp
	var selected map[string]bool
	if req.Operation == "top" && req.Selector != "" {
		sel, err := resolve(tree, req.Selector)
		if err != nil {
			return nil, err
		}
		if sel.exactNode {
			return []Row{nodeRow(sel.nodes[0], tree.Root.TotalMs)}, nil
		}
		selected = map[string]bool{}
		for _, f := range sel.functions {
			selected[f.ID] = true
		}
	}
	if req.Operation == "find" {
		pattern := req.Selector
		if len(pattern) >= 2 && strings.HasPrefix(pattern, "/") && strings.HasSuffix(pattern, "/") {
			pattern = pattern[1 : len(pattern)-1]
		}
		var err error
		match, err = regexp.Compile(pattern)
		if err != nil {
			return nil, err
		}
	}
	rows := []Row{}
	for _, f := range tree.Functions() {
		if !accepts(f.Frame, req) {
			continue
		}
		if selected != nil && !selected[f.ID] {
			continue
		}
		if match != nil && !match.MatchString(f.Frame.Name) && !match.MatchString(f.Frame.URL) && (f.Frame.Original == nil || (!match.MatchString(f.Frame.Original.Name) && !match.MatchString(f.Frame.Original.URL))) {
			continue
		}
		rows = append(rows, Row{Function: function(f.Frame), SelfMS: clean(f.SelfMs), TotalMS: clean(f.TotalMs), Samples: f.Samples, Percent: percent(f.TotalMs, tree.Root.TotalMs)})
	}
	sortRows(rows, req.By)
	return rows, nil
}

func selectedRows(tree *flame.Tree, req Request) ([]Row, error) {
	sel, err := resolve(tree, req.Selector)
	if err != nil {
		return nil, err
	}
	if req.Operation == "paths" {
		return pathRows(tree, &sel), nil
	}
	if req.Operation == "subtree" {
		return subtreeRows(tree, sel, req), nil
	}
	rows := []Row{}
	denominator := 0.0
	if sel.exactNode {
		n := sel.nodes[0]
		denominator = n.TotalMs
		if req.Operation == "callers" {
			if n.Parent != nil && n.Parent.Parent != nil {
				row := nodeRow(n.Parent, denominator)
				row.TotalMS, row.Samples, row.Percent = clean(n.TotalMs), n.Samples, percent(n.TotalMs, denominator)
				rows = append(rows, row)
			}
		} else {
			for _, child := range n.Children {
				rows = append(rows, nodeRow(child, denominator))
			}
		}
	} else {
		byID := map[string]*Row{}
		for _, f := range sel.functions {
			denominator += f.TotalMs
			edges := tree.Callers(f.ID)
			if req.Operation == "callees" {
				edges = tree.Callees(f.ID)
			}
			for _, edge := range edges {
				frame := edge.Caller
				if req.Operation == "callees" {
					frame = edge.Callee
				}
				id := flame.FunctionID(frame)
				row := byID[id]
				if row == nil {
					row = &Row{Function: function(frame)}
					byID[id] = row
				}
				row.TotalMS += edge.TotalMs
				row.Samples += edge.Samples
			}
		}
		for _, row := range byID {
			row.TotalMS = clean(row.TotalMS)
			row.Percent = percent(row.TotalMS, denominator)
			rows = append(rows, *row)
		}
	}
	sortRows(rows, "total")
	return rows, nil
}

func pathRows(tree *flame.Tree, sel *selection) []Row {
	rows := []Row{}
	var prefix []flame.Frame
	wanted := map[string]bool{}
	if sel != nil {
		for _, f := range sel.functions {
			wanted[f.ID] = true
		}
		if sel.exactNode {
			prefix = nodeFrames(sel.nodes[0])
		}
	}
	for _, path := range tree.Paths() {
		matches := sel == nil
		if len(prefix) > 0 {
			matches = len(path.Frames) >= len(prefix)
			if matches {
				for i, f := range prefix {
					if flame.FunctionID(path.Frames[i]) != flame.FunctionID(f) {
						matches = false
						break
					}
				}
			}
		} else if sel != nil {
			for _, f := range path.Frames {
				if wanted[flame.FunctionID(f)] {
					matches = true
					break
				}
			}
		}
		if !matches || len(path.Frames) == 0 {
			continue
		}
		rows = append(rows, Row{Function: function(path.Frames[len(path.Frames)-1]), NodeID: path.ID, SelfMS: clean(path.SelfMs), TotalMS: clean(path.TotalMs), Samples: path.Samples, Percent: percent(path.TotalMs, tree.Root.TotalMs), Depth: len(path.Frames), Stack: stack(path.Frames)})
	}
	sortRows(rows, "total")
	return rows
}

func nodeRow(n *flame.Node, total float64) Row {
	return Row{Function: function(n.Frame), NodeID: n.ID, SelfMS: clean(n.SelfMs), TotalMS: clean(n.TotalMs), Samples: n.Samples, Percent: percent(n.TotalMs, total)}
}

func subtreeRows(tree *flame.Tree, sel selection, req Request) []Row {
	roots := append([]*flame.Node(nil), sel.nodes...)
	sort.Slice(roots, func(i, j int) bool {
		if roots[i].TotalMs != roots[j].TotalMs {
			return roots[i].TotalMs > roots[j].TotalMs
		}
		if frameLess(roots[i].Frame, roots[j].Frame) {
			return true
		}
		if frameLess(roots[j].Frame, roots[i].Frame) {
			return false
		}
		return roots[i].ID < roots[j].ID
	})
	rows := []Row{}
	seen := map[string]bool{}
	var visit func(*flame.Node, int)
	visit = func(n *flame.Node, depth int) {
		if seen[n.ID] || depth > req.Depth || n.TotalMs < req.MinMS {
			return
		}
		seen[n.ID] = true
		row := nodeRow(n, tree.Root.TotalMs)
		row.Depth = depth
		row.Stack = stack(nodeFrames(n))
		rows = append(rows, row)
		children := append([]*flame.Node(nil), n.Children...)
		sort.Slice(children, func(i, j int) bool {
			if children[i].TotalMs != children[j].TotalMs {
				return children[i].TotalMs > children[j].TotalMs
			}
			return frameLess(children[i].Frame, children[j].Frame)
		})
		for _, child := range children {
			visit(child, depth+1)
		}
	}
	for _, root := range roots {
		visit(root, 0)
	}
	return rows
}

func diffRows(before, after *flame.Tree, req Request) []Row {
	byID := map[string]*Row{}
	for _, f := range before.Functions() {
		if !accepts(f.Frame, req) {
			continue
		}
		byID[f.ID] = &Row{Function: function(f.Frame), BaselineSelfMS: clean(f.SelfMs), BaselineTotalMS: clean(f.TotalMs), Samples: -f.Samples}
	}
	for _, f := range after.Functions() {
		if !accepts(f.Frame, req) {
			continue
		}
		row := byID[f.ID]
		if row == nil {
			row = &Row{Function: function(f.Frame)}
			byID[f.ID] = row
		}
		row.CandidateSelfMS, row.CandidateTotalMS = clean(f.SelfMs), clean(f.TotalMs)
		row.Samples += f.Samples
	}
	rows := make([]Row, 0, len(byID))
	for _, row := range byID {
		row.DeltaSelfMS = clean(row.CandidateSelfMS - row.BaselineSelfMS)
		row.DeltaTotalMS = clean(row.CandidateTotalMS - row.BaselineTotalMS)
		row.SelfMS, row.TotalMS = row.DeltaSelfMS, row.DeltaTotalMS
		rows = append(rows, *row)
	}
	sort.Slice(rows, func(i, j int) bool {
		a, b := math.Abs(rows[i].DeltaSelfMS), math.Abs(rows[j].DeltaSelfMS)
		if req.By == "total" {
			a, b = math.Abs(rows[i].DeltaTotalMS), math.Abs(rows[j].DeltaTotalMS)
		}
		if a != b {
			return a > b
		}
		return rowLess(rows[i], rows[j])
	})
	return rows
}

func depthRows(tree *flame.Tree) ([]Row, []Function) {
	byDepth := map[int]*Row{}
	var deepest []flame.Frame
	for _, path := range tree.Paths() {
		depth := len(path.Frames)
		row := byDepth[depth]
		if row == nil {
			row = &Row{Depth: depth}
			byDepth[depth] = row
		}
		row.TotalMS += path.TotalMs
		row.SelfMS += path.SelfMs
		row.Samples += path.Samples
		if depth > len(deepest) || (depth == len(deepest) && stackLess(path.Frames, deepest)) {
			deepest = path.Frames
		}
	}
	rows := make([]Row, 0, len(byDepth))
	for _, row := range byDepth {
		row.TotalMS, row.SelfMS = clean(row.TotalMS), clean(row.SelfMS)
		row.Percent = percent(row.TotalMS, tree.Root.TotalMs)
		rows = append(rows, *row)
	}
	sort.Slice(rows, func(i, j int) bool { return rows[i].Depth < rows[j].Depth })
	return rows, stack(deepest)
}

func stackLess(a, b []flame.Frame) bool {
	for i := 0; i < len(a) && i < len(b); i++ {
		if frameLess(a[i], b[i]) {
			return true
		}
		if frameLess(b[i], a[i]) {
			return false
		}
	}
	return len(a) < len(b)
}

func rowLess(a, b Row) bool {
	if a.Name != b.Name {
		return a.Name < b.Name
	}
	if a.URL != b.URL {
		return a.URL < b.URL
	}
	if a.Line != b.Line {
		return a.Line < b.Line
	}
	if a.Column != b.Column {
		return a.Column < b.Column
	}
	if a.ID != b.ID {
		return a.ID < b.ID
	}
	return a.NodeID < b.NodeID
}

func sortRows(rows []Row, by string) {
	sort.Slice(rows, func(i, j int) bool {
		a, b := rows[i].SelfMS, rows[j].SelfMS
		if by == "total" {
			a, b = rows[i].TotalMS, rows[j].TotalMS
		}
		if a != b {
			return a > b
		}
		return rowLess(rows[i], rows[j])
	})
}
