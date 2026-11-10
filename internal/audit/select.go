package audit

import (
	"encoding/json"

	"github.com/omniviewdev/wkperf/internal/flame"
	"github.com/omniviewdev/wkperf/internal/insights"
	"github.com/omniviewdev/wkperf/internal/rootcause"
)

// SelectEvidence uses each evidence scene's analysis cap for audit references.
// Full numeric rollups and in-memory scene analyses remain unchanged.
func SelectEvidence(report *Report) *Report {
	if report == nil {
		return nil
	}
	r := *report
	r.Scenes = append([]SceneResult{}, r.Scenes...)
	limits := make([]int, len(r.Scenes))
	for i := range r.Scenes {
		scene := &r.Scenes[i]
		scene.Rep = insights.SelectEvidence(scene.Rep)
		top := 10
		if scene.Rep != nil && scene.Rep.Top > 0 {
			top = scene.Rep.Top
		}
		limits[i] = top
	}
	r.Priorities = append([]Priority{}, r.Priorities...)
	for i := range r.Priorities {
		p := &r.Priorities[i]
		top := 10
		if p.EvidenceSceneIndex >= 0 && p.EvidenceSceneIndex < len(limits) {
			top = limits[p.EvidenceSceneIndex]
		}
		p.StacksTotal = max(p.StacksTotal, max(len(p.Stacks), len(p.StackIDs)))
		p.NativeReadsTotal = max(p.NativeReadsTotal, len(p.NativeReads))
		p.NativeReads = p.NativeReads[:min(len(p.NativeReads), top)]
		p.Stacks = p.Stacks[:min(len(p.Stacks), top)]
		if len(p.Stacks) > 0 {
			p.StackIDs = make([]string, len(p.Stacks))
			for j, stack := range p.Stacks {
				p.StackIDs[j] = stack.StackID
				if p.StackIDs[j] == "" {
					p.StackIDs[j] = flame.StackID(stack.Frames)
				}
			}
		} else if len(p.StackIDs) > 0 {
			p.StackIDs = p.StackIDs[:min(len(p.StackIDs), top)]
		} else {
			p.StackIDs = []string{}
		}
	}
	return &r
}

type reportFields Report

func (r *Report) MarshalJSON() ([]byte, error) {
	selected := SelectEvidence(r)
	return json.Marshal((*reportFields)(selected))
}

type PriorityFields Priority

// WirePriority keeps depth identities scoped to EvidenceSceneIndex, like HotFunction.
type WirePriority struct {
	PriorityFields
	Depth *WireDepthFacets `json:"depth,omitempty"`
}

type DepthFields rootcause.DepthFacets

type WireDepthFacets struct {
	DepthFields
	RepeatedFrame *WireRepeatedFrame `json:"repeatedFrame,omitempty"`
	Recursion     *WireRepeatedFrame `json:"recursion,omitempty"`
}

type WireRepeatedFrame struct {
	ID    string `json:"id"`
	Count int    `json:"count"`
}

func (p Priority) MarshalJSON() ([]byte, error) {
	wire := WirePriority{PriorityFields: PriorityFields(p)}
	if p.Depth != nil {
		wire.Depth = &WireDepthFacets{DepthFields: DepthFields(*p.Depth)}
		if repeated := p.Depth.RepeatedFrame; repeated != nil {
			wire.Depth.RepeatedFrame = &WireRepeatedFrame{ID: flame.FunctionID(repeated.Frame), Count: repeated.Count}
		}
		if recursion := p.Depth.Recursion; recursion != nil {
			wire.Depth.Recursion = &WireRepeatedFrame{ID: flame.FunctionID(recursion.Frame), Count: recursion.Count}
		}
	}
	return json.Marshal(wire)
}
