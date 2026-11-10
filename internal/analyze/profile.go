package analyze

import (
	"io"
	"sort"

	"github.com/omniviewdev/wkperf/internal/flame"
	"github.com/omniviewdev/wkperf/internal/metrics"
	"github.com/omniviewdev/wkperf/internal/model"
	"github.com/omniviewdev/wkperf/internal/profile"
)

func metricFrame(f flame.Frame, id string) metrics.FunctionFrame {
	return metrics.FunctionFrame{ID: id, Name: f.Name, URL: f.URL, Line: f.Line, Column: f.Column, SourceID: f.SourceID, Original: (*metrics.SourceLocation)(f.Original)}
}

func frameLess(a, b metrics.FunctionFrame) bool {
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

func sortEdges(edges []metrics.CallEdge) {
	sort.Slice(edges, func(i, j int) bool {
		if edges[i].WeightMs != edges[j].WeightMs {
			return edges[i].WeightMs > edges[j].WeightMs
		}
		if edges[i].Samples != edges[j].Samples {
			return edges[i].Samples > edges[j].Samples
		}
		return frameLess(edges[i].Function, edges[j].Function)
	})
}

func applyTree(s *metrics.Summary, tree *flame.Tree) {
	if tree == nil {
		return
	}
	functions := tree.Functions()
	byID := make(map[string]int, len(functions))
	framesByValue := make(map[flame.Frame]metrics.FunctionFrame, len(functions))
	s.Functions = make([]metrics.FuncStat, 0, len(functions))
	for _, f := range functions {
		frame := metricFrame(f.Frame, f.ID)
		framesByValue[f.Frame] = frame
		byID[f.ID] = len(s.Functions)
		s.Functions = append(s.Functions, metrics.FuncStat{ID: f.ID, Name: frame.Name, URL: frame.URL, Line: frame.Line, Column: frame.Column, SourceID: frame.SourceID, Original: frame.Original, SelfMs: f.SelfMs, TotalMs: f.TotalMs, SelfSamples: f.SelfSamples})
	}
	for _, edge := range tree.Edges() {
		if i, ok := byID[edge.CalleeID]; ok {
			s.Functions[i].Callers = append(s.Functions[i].Callers, metrics.CallEdge{Function: metricFrame(edge.Caller, edge.CallerID), WeightMs: edge.TotalMs, Samples: edge.Samples})
		}
		if i, ok := byID[edge.CallerID]; ok {
			s.Functions[i].Callees = append(s.Functions[i].Callees, metrics.CallEdge{Function: metricFrame(edge.Callee, edge.CalleeID), WeightMs: edge.TotalMs, Samples: edge.Samples})
		}
	}
	for i := range s.Functions {
		sortEdges(s.Functions[i].Callers)
		sortEdges(s.Functions[i].Callees)
	}
	paths := tree.Paths()
	s.CommonStacks = make([]metrics.StackStat, 0, len(paths))
	for _, path := range paths {
		frames := make([]metrics.FunctionFrame, len(path.Frames))
		for i, f := range path.Frames {
			frame, ok := framesByValue[f]
			if !ok {
				frame = metricFrame(f, flame.FunctionID(f))
				framesByValue[f] = frame
			}
			frames[i] = frame
		}
		s.CommonStacks = append(s.CommonStacks, metrics.StackStat{ID: path.ID, Frames: frames, WeightMs: path.TotalMs, Samples: path.Samples})
	}
	sort.Slice(s.CommonStacks, func(i, j int) bool {
		a, b := s.CommonStacks[i], s.CommonStacks[j]
		if a.Samples != b.Samples {
			return a.Samples > b.Samples
		}
		if a.WeightMs != b.WeightMs {
			return a.WeightMs > b.WeightMs
		}
		for k := 0; k < len(a.Frames) && k < len(b.Frames); k++ {
			if frameLess(a.Frames[k], b.Frames[k]) {
				return true
			}
			if frameLess(b.Frames[k], a.Frames[k]) {
				return false
			}
		}
		return len(a.Frames) < len(b.Frames)
	})
	// Share immutable paths instead of copying a deep full stack per function.
	for _, stack := range s.CommonStacks {
		for _, frame := range stack.Frames {
			if i, ok := byID[frame.ID]; ok && len(s.Functions[i].CommonStack) == 0 {
				s.Functions[i].CommonStack = stack.Frames
				s.Functions[i].CommonStackID = stack.ID
			}
		}
	}
	sort.Slice(s.Functions, func(i, j int) bool {
		a, b := s.Functions[i], s.Functions[j]
		if a.SelfMs != b.SelfMs {
			return a.SelfMs > b.SelfMs
		}
		if a.TotalMs != b.TotalMs {
			return a.TotalMs > b.TotalMs
		}
		return frameLess(a.Frame(), b.Frame())
	})
	s.InclusiveFunctions = append([]metrics.FuncStat(nil), s.Functions...)
	sort.Slice(s.InclusiveFunctions, func(i, j int) bool {
		a, b := s.InclusiveFunctions[i], s.InclusiveFunctions[j]
		if a.TotalMs != b.TotalMs {
			return a.TotalMs > b.TotalMs
		}
		if a.SelfMs != b.SelfMs {
			return a.SelfMs > b.SelfMs
		}
		return frameLess(a.Frame(), b.Frame())
	})
}

func relativeIntervals(rec *model.Recording, intervals []profile.Interval) []flame.Interval {
	out := make([]flame.Interval, len(intervals))
	for i, span := range intervals {
		out[i] = flame.Interval{Start: span.Start - rec.StartTime, End: span.End - rec.StartTime}
	}
	return out
}

// FunctionsInIntervals uses timestamp membership and retains sample weights.
func FunctionsInIntervals(rec *model.Recording, intervals []profile.Interval) []metrics.FuncStat {
	if rec == nil {
		return nil
	}
	return FunctionsInIntervalsWithTree(rec, flame.Build(rec), intervals)
}

// FunctionsInIntervalsWithTree reuses a recording's canonical sampling tree.
func FunctionsInIntervalsWithTree(rec *model.Recording, tree *flame.Tree, intervals []profile.Interval) []metrics.FuncStat {
	if rec == nil || tree == nil {
		return nil
	}
	s := &metrics.Summary{}
	applyTree(s, tree.InIntervals(relativeIntervals(rec, intervals)))
	return s.Functions
}

// RenderFolded writes root-to-leaf stacks weighted in milliseconds.
func RenderFolded(w io.Writer, rec *model.Recording) error {
	_, err := io.WriteString(w, flame.Build(rec).Folded())
	return err
}
