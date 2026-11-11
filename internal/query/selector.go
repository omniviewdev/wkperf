package query

import (
	"encoding/hex"
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/omniviewdev/wkperf/internal/flame"
)

var ErrNoMatches = errors.New("selector matched no functions")

// AmbiguousError contains stable function candidates instead of guessing.
type AmbiguousError struct{ Candidates []Function }

func (e *AmbiguousError) Error() string {
	var labels []string
	for _, f := range e.Candidates {
		labels = append(labels, fmt.Sprintf("%s %s %s:%d:%d", f.ID, f.Name, f.URL, f.Line, f.Column))
	}
	return "ambiguous selector; choose a function or node ID: " + strings.Join(labels, "; ")
}

type selection struct {
	functions []flame.Function
	nodes     []*flame.Node
	exactNode bool
}

// ResolveNodes shares query selector semantics with flame focus. A unique
// function can occur in several calling contexts, so callers requiring one
// context must require one returned node and show node IDs otherwise.
func ResolveNodes(tree *flame.Tree, selector string) ([]*flame.Node, error) {
	sel, err := resolve(tree, selector)
	if err != nil {
		return nil, err
	}
	return sel.nodes, nil
}

func nodes(tree *flame.Tree) []*flame.Node {
	var out []*flame.Node
	if tree == nil || tree.Root == nil {
		return out
	}
	var visit func(*flame.Node)
	visit = func(n *flame.Node) {
		out = append(out, n)
		for _, c := range n.Children {
			visit(c)
		}
	}
	for _, n := range tree.Root.Children {
		visit(n)
	}
	sort.Slice(out, func(i, j int) bool {
		if frameLess(out[i].Frame, out[j].Frame) {
			return true
		}
		if frameLess(out[j].Frame, out[i].Frame) {
			return false
		}
		return out[i].ID < out[j].ID
	})
	return out
}

func resolve(tree *flame.Tree, text string) (selection, error) {
	var out selection
	if strings.TrimSpace(text) == "" {
		return out, fmt.Errorf("function selector is required")
	}
	if tree == nil || tree.Root == nil {
		return out, fmt.Errorf("%w: %q", ErrNoMatches, text)
	}
	allFunctions, allNodes := tree.Functions(), nodes(tree)
	byID := make(map[string]flame.Function, len(allFunctions))
	for _, f := range allFunctions {
		byID[f.ID] = f
	}
	for _, n := range allNodes {
		if n.ID == text {
			out.nodes = []*flame.Node{n}
			out.exactNode = true
			out.functions = []flame.Function{byID[flame.FunctionID(n.Frame)]}
			return out, nil
		}
	}
	if f, ok := byID[text]; ok {
		out.functions = []flame.Function{f}
	} else {
		match, multiple, err := selectorMatcher(text)
		if err != nil {
			return out, err
		}
		for _, f := range allFunctions {
			if match(f.Frame) {
				out.functions = append(out.functions, f)
			}
		}
		sort.Slice(out.functions, func(i, j int) bool { return frameLess(out.functions[i].Frame, out.functions[j].Frame) })
		if len(out.functions) > 1 && !multiple {
			candidates := make([]Function, len(out.functions))
			for i, f := range out.functions {
				candidates[i] = function(f.Frame)
			}
			return out, &AmbiguousError{Candidates: candidates}
		}
	}
	if len(out.functions) == 0 {
		return out, fmt.Errorf("%w: %q", ErrNoMatches, text)
	}
	wanted := make(map[string]bool, len(out.functions))
	for _, f := range out.functions {
		wanted[f.ID] = true
	}
	for _, n := range allNodes {
		if wanted[flame.FunctionID(n.Frame)] {
			out.nodes = append(out.nodes, n)
		}
	}
	return out, nil
}

func selectorMatcher(text string) (func(flame.Frame) bool, bool, error) {
	if strings.HasPrefix(text, "/") && strings.HasSuffix(text, "/") {
		if len(text) < 2 {
			return nil, false, fmt.Errorf("malformed regex selector %q: require /regex/", text)
		}
		re, err := regexp.Compile(text[1 : len(text)-1])
		if err != nil {
			return nil, false, fmt.Errorf("malformed regex selector: %w", err)
		}
		return func(f flame.Frame) bool {
			if re.MatchString(f.Name) || re.MatchString(f.URL) {
				return true
			}
			return f.Original != nil && (re.MatchString(f.Original.Name) || re.MatchString(f.Original.URL))
		}, true, nil
	}
	if strings.HasPrefix(text, "fn:") || strings.HasPrefix(text, "node:") || strings.HasPrefix(text, "fn-") || strings.HasPrefix(text, "node-") {
		return nil, false, fmt.Errorf("unknown function or node ID %q", text)
	}
	if strings.HasPrefix(text, "f_") || strings.HasPrefix(text, "n_") {
		if len(text) != 66 {
			return nil, false, fmt.Errorf("malformed function or node ID %q", text)
		}
		if _, err := hex.DecodeString(text[2:]); err != nil {
			return nil, false, fmt.Errorf("malformed function or node ID %q", text)
		}
		return func(flame.Frame) bool { return false }, false, nil
	}
	last := strings.LastIndexByte(text, ':')
	if last >= 0 {
		before := strings.LastIndexByte(text[:last], ':')
		if before >= 0 {
			line, lineErr := strconv.Atoi(text[before+1 : last])
			column, columnErr := strconv.Atoi(text[last+1:])
			if lineErr != nil || columnErr != nil || line < 0 || column < 0 || before == 0 {
				return nil, false, fmt.Errorf("malformed source selector %q: require url:line:column", text)
			}
			url := text[:before]
			return func(f flame.Frame) bool {
				if f.URL == url && f.Line == line && f.Column == column {
					return true
				}
				return f.Original != nil && f.Original.URL == url && f.Original.Line == line && f.Original.Column == column
			}, false, nil
		}
	}
	if strings.HasPrefix(text, "/") {
		return nil, false, fmt.Errorf("malformed regex selector %q: require /regex/", text)
	}
	return func(f flame.Frame) bool {
		return f.Name == text || (f.Name == "" && text == "(anonymous)") || (f.Original != nil && f.Original.Name == text)
	}, false, nil
}

func frameLess(a, b flame.Frame) bool {
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
	return a.SourceID < b.SourceID
}

func function(f flame.Frame) Function {
	name := f.Name
	if name == "" {
		name = "(anonymous)"
	}
	module := f.URL
	if f.Original != nil && f.Original.URL != "" {
		module = f.Original.URL
	}
	return Function{ID: flame.FunctionID(f), Name: name, URL: f.URL, Line: f.Line, Column: f.Column, SourceID: f.SourceID, Module: module, Category: string(f.Category), Native: f.Native, Anonymous: f.Anonymous, Original: f.Original}
}

func stack(frames []flame.Frame) []Function {
	out := make([]Function, len(frames))
	for i, f := range frames {
		out[i] = function(f)
	}
	return out
}

func nodeFrames(n *flame.Node) []flame.Frame {
	var frames []flame.Frame
	for current := n; current != nil && current.Parent != nil; current = current.Parent {
		frames = append(frames, current.Frame)
	}
	for i, j := 0, len(frames)-1; i < j; i, j = i+1, j-1 {
		frames[i], frames[j] = frames[j], frames[i]
	}
	return frames
}
